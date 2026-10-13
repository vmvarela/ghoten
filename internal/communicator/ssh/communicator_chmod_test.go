// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

//go:build !race

package ssh

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/apparentlymart/go-shquot/shquot"
	"github.com/zclconf/go-cty/cty"
	"golang.org/x/crypto/ssh"
)

// shellExecServer authenticates like a mock SSH server but executes its
// "exec" commands through /bin/sh -c on the test host, the way a Unix sshd
// does. This makes shell interpretation of the commands produced by the SSH
// communicator observable, including any unintended side effects.
type shellExecServer struct {
	t *testing.T

	addr      string
	shellPath string
	workDir   string

	mu       sync.Mutex
	executed []string
}

func startShellExecServer(t *testing.T, workDir string) *shellExecServer {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell interpretation is a Unix-only concern")
	}
	shellPath, err := exec.LookPath("sh")
	if err != nil {
		t.Skipf("POSIX shell not available: %s", err)
	}
	if _, err := exec.LookPath("scp"); err != nil {
		t.Skipf("scp not available: %s", err)
	}

	signer, err := ssh.ParsePrivateKey([]byte(testServerPrivateKey))
	if err != nil {
		t.Fatalf("unable to parse server private key: %s", err)
	}
	serverConfig := &ssh.ServerConfig{
		PasswordCallback: acceptUserPass("user", "pass"),
	}
	serverConfig.AddHostKey(signer)

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("unable to listen: %s", err)
	}

	srv := &shellExecServer{
		t:         t,
		addr:      l.Addr().String(),
		shellPath: shellPath,
		workDir:   workDir,
	}

	var wg sync.WaitGroup
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			_, chans, reqs, err := ssh.NewServerConn(conn, serverConfig)
			if err != nil {
				t.Logf("handshake failed: %s", err)
				conn.Close()
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer conn.Close()
				go ssh.DiscardRequests(reqs)
				for newChannel := range chans {
					channel, requests, err := newChannel.Accept()
					if err != nil {
						t.Logf("unable to accept channel: %s", err)
						continue
					}
					wg.Add(1)
					go func() {
						defer wg.Done()
						srv.serveSession(channel, requests)
					}()
				}
			}()
		}
	}()

	t.Cleanup(func() {
		l.Close()
		wg.Wait()
	})

	return srv
}

func (srv *shellExecServer) serveSession(channel ssh.Channel, requests <-chan *ssh.Request) {
	defer channel.Close()

	for req := range requests {
		switch req.Type {
		case "pty-req":
			// The communicator requests a PTY for Unix commands, but the
			// test server runs the command without one.
			if err := req.Reply(true, nil); err != nil {
				srv.t.Errorf("unable to accept pty request: %s", err)
				return
			}
		case "exec":
			var payload struct {
				Command string
			}
			if err := ssh.Unmarshal(req.Payload, &payload); err != nil {
				if err := req.Reply(false, nil); err != nil {
					srv.t.Errorf("unable to reject malformed exec request: %s", err)
					return
				}
				continue
			}
			srv.recordCommand(payload.Command)
			if err := req.Reply(true, nil); err != nil {
				srv.t.Errorf("unable to accept exec request: %s", err)
				return
			}
			status := uint32(0)
			if srv.execCommand(channel, payload.Command) != nil {
				status = 1
			}
			if _, err := channel.SendRequest("exit-status", false, ssh.Marshal(&struct {
				Status uint32
			}{Status: status})); err != nil {
				srv.t.Logf("unable to send exit status: %s", err)
			}
			return
		default:
			if err := req.Reply(false, nil); err != nil {
				srv.t.Errorf("unable to reject unsupported request %q: %s", req.Type, err)
				return
			}
		}
	}
}

func (srv *shellExecServer) recordCommand(command string) {
	srv.mu.Lock()
	srv.executed = append(srv.executed, command)
	srv.mu.Unlock()
}

func (srv *shellExecServer) commands() []string {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	return append([]string(nil), srv.executed...)
}

// execCommand runs command through /bin/sh -c with the channel as its stdio,
// like sshd does for exec requests. Interfaces directly with the SSH channel
// stdin and stdout, with stderr captured via the channel's stderr stream.
func (srv *shellExecServer) execCommand(channel ssh.Channel, command string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, srv.shellPath, "-c", command)
	cmd.Dir = srv.workDir
	cmd.Stdin = channel
	cmd.Stdout = channel
	cmd.Stderr = channel.Stderr()
	return cmd.Run()
}

// TestUploadScript_quotedChmodRealShell is a regression test for OpenTofu
// #4330: the chmod issued by UploadScript is executed remotely by the user's
// shell, so the script path must stay literal even when it contains spaces,
// apostrophes or shell injection syntax.
func TestUploadScript_quotedChmodRealShell(t *testing.T) {
	dir := t.TempDir()

	// The injected file is only created if the shell-sensitive script name
	// gets reinterpreted by the executing shell. The apostrophes are
	// balanced so that, with the legacy unquoted command, the injection is
	// actually executed by the shell.
	markerName := "injected-by-shell"
	marker := filepath.Join(dir, markerName)
	rawPath := filepath.Join(dir, "tf-4330 'script'; touch "+markerName+"; echo \"done.sh\"")

	srv := startShellExecServer(t, dir)

	host, portStr, err := net.SplitHostPort(srv.addr)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}

	c, err := New(cty.ObjectVal(map[string]cty.Value{
		"type":     cty.StringVal("ssh"),
		"user":     cty.StringVal("user"),
		"password": cty.StringVal("pass"),
		"host":     cty.StringVal(host),
		"port":     cty.NumberIntVal(int64(port)),
		"timeout":  cty.StringVal("30s"),
	}))
	if err != nil {
		t.Fatalf("error creating communicator: %s", err)
	}
	defer func() {
		if err := c.Disconnect(); err != nil {
			t.Log(err)
		}
	}()

	scriptContent := "#!/bin/sh\necho \"I'm being executed verbatim\"\n"
	if err := c.UploadScript(rawPath, strings.NewReader(scriptContent)); err != nil {
		t.Fatalf("UploadScript failed: %s", err)
	}

	// The script was uploaded to the literal path.
	got, err := os.ReadFile(rawPath)
	if err != nil {
		t.Fatalf("script not found at the literal path: %s", err)
	}
	if string(got) != scriptContent {
		t.Fatalf("unexpected script content at literal path %q: %q", rawPath, got)
	}

	// chmod 0777 succeeded on the literal path.
	fi, err := os.Stat(rawPath)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o777 {
		t.Fatalf("chmod 0777 did not succeed on the literal path: mode is %v", fi.Mode())
	}

	// The chmod command is exactly the POSIX-quoted form of the raw path.
	want := shquot.POSIXShell([]string{"chmod", "0777", rawPath})
	found := false
	commands := srv.commands()
	for _, executed := range commands {
		// The communicator appends a newline before sending the command.
		if strings.TrimSpace(executed) == want {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected %q to be executed, only got %#v", want, commands)
	}

	// The shell-sensitive syntax stayed literal: no injected side effect.
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("script path was reinterpreted by the shell: %s was created", marker)
	}
}
