// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package remoteexec

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/apparentlymart/go-shquot/shquot"
	"github.com/mitchellh/cli"
	"github.com/vmvarela/ghoten/internal/communicator"
	"github.com/vmvarela/ghoten/internal/communicator/remote"
	"github.com/vmvarela/ghoten/internal/provisioners"
	"github.com/zclconf/go-cty/cty"
)

func TestResourceProvider_Validate_good(t *testing.T) {
	c := cty.ObjectVal(map[string]cty.Value{
		"inline": cty.ListVal([]cty.Value{cty.StringVal("echo foo")}),
	})

	resp := New().ValidateProvisionerConfig(provisioners.ValidateProvisionerConfigRequest{
		Config: c,
	})
	if len(resp.Diagnostics) > 0 {
		t.Fatal(resp.Diagnostics.ErrWithWarnings())
	}
}

func TestResourceProvider_Validate_bad(t *testing.T) {
	c := cty.ObjectVal(map[string]cty.Value{
		"invalid": cty.StringVal("nope"),
	})

	resp := New().ValidateProvisionerConfig(provisioners.ValidateProvisionerConfigRequest{
		Config: c,
	})
	if !resp.Diagnostics.HasErrors() {
		t.Fatalf("Should have errors")
	}
}

var expectedScriptOut = `cd /tmp
wget http://foobar
exit 0
`

func TestResourceProvider_generateScript(t *testing.T) {
	inline := cty.ListVal([]cty.Value{
		cty.StringVal("cd /tmp"),
		cty.StringVal("wget http://foobar"),
		cty.StringVal("exit 0"),
	})

	out, err := generateScripts(inline)
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	if len(out) != 1 {
		t.Fatal("expected 1 out")
	}

	if out[0] != expectedScriptOut {
		t.Fatalf("bad: %v", out)
	}
}

func TestResourceProvider_generateScriptEmptyInline(t *testing.T) {
	inline := cty.ListVal([]cty.Value{cty.StringVal("")})

	_, err := generateScripts(inline)
	if err == nil {
		t.Fatal("expected error, got none")
	}

	if !strings.Contains(err.Error(), "empty string") {
		t.Fatalf("expected empty string error, got: %s", err)
	}
}

func TestResourceProvider_CollectScripts_inline(t *testing.T) {
	conf := map[string]cty.Value{
		"inline": cty.ListVal([]cty.Value{
			cty.StringVal("cd /tmp"),
			cty.StringVal("wget http://foobar"),
			cty.StringVal("exit 0"),
		}),
	}

	scripts, err := collectScripts(cty.ObjectVal(conf))
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	if len(scripts) != 1 {
		t.Fatalf("bad: %v", scripts)
	}

	var out bytes.Buffer
	_, err = io.Copy(&out, scripts[0])
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	if out.String() != expectedScriptOut {
		t.Fatalf("bad: %v", out.String())
	}
}

func TestResourceProvider_CollectScripts_script(t *testing.T) {
	p := New()
	schema := p.GetSchema().Provisioner

	conf, err := schema.CoerceValue(cty.ObjectVal(map[string]cty.Value{
		"scripts": cty.ListVal([]cty.Value{
			cty.StringVal("testdata/script1.sh"),
		}),
	}))
	if err != nil {
		t.Fatal(err)
	}

	scripts, err := collectScripts(conf)
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	if len(scripts) != 1 {
		t.Fatalf("bad: %v", scripts)
	}

	var out bytes.Buffer
	_, err = io.Copy(&out, scripts[0])
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	expectedOutput := normaliseNewlines(expectedScriptOut)
	actualOutput := normaliseNewlines(out.String())
	if actualOutput != expectedOutput {
		t.Fatalf("bad: %v", out.String())
	}
}

func TestResourceProvider_CollectScripts_scripts(t *testing.T) {
	p := New()
	schema := p.GetSchema().Provisioner

	conf, err := schema.CoerceValue(cty.ObjectVal(map[string]cty.Value{
		"scripts": cty.ListVal([]cty.Value{
			cty.StringVal("testdata/script1.sh"),
			cty.StringVal("testdata/script1.sh"),
			cty.StringVal("testdata/script1.sh"),
		}),
	}))
	if err != nil {
		log.Fatal(err)
	}

	scripts, err := collectScripts(conf)
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	if len(scripts) != 3 {
		t.Fatalf("bad: %v", scripts)
	}

	for idx := range scripts {
		var out bytes.Buffer
		_, err = io.Copy(&out, scripts[idx])
		if err != nil {
			t.Fatalf("err: %v", err)
		}

		expectedOutput := normaliseNewlines(expectedScriptOut)
		actualOutput := normaliseNewlines(out.String())
		if actualOutput != expectedOutput {
			t.Fatalf("bad: %v", out.String())
		}
	}
}

func TestResourceProvider_CollectScripts_scriptsEmpty(t *testing.T) {
	p := New()
	schema := p.GetSchema().Provisioner

	conf, err := schema.CoerceValue(cty.ObjectVal(map[string]cty.Value{
		"scripts": cty.ListVal([]cty.Value{cty.StringVal("")}),
	}))
	if err != nil {
		t.Fatal(err)
	}

	_, err = collectScripts(conf)
	if err == nil {
		t.Fatal("expected error")
	}

	if !strings.Contains(err.Error(), "empty string") {
		t.Fatalf("Expected empty string error, got: %s", err)
	}
}

func TestProvisionerTimeout(t *testing.T) {
	o := cli.NewMockUi()
	c := new(communicator.MockCommunicator)

	disconnected := make(chan struct{})
	c.DisconnectFunc = func() error {
		close(disconnected)
		return nil
	}

	completed := make(chan struct{})
	c.CommandFunc = func(cmd *remote.Cmd) error {
		defer close(completed)
		cmd.Init()
		time.Sleep(2 * time.Second)
		cmd.SetExitStatus(0, nil)
		return nil
	}
	c.ConnTimeout = time.Second
	c.UploadScripts = map[string]string{"hello": "echo hello"}
	c.RemoteScriptPath = "hello"

	conf := map[string]cty.Value{
		"inline": cty.ListVal([]cty.Value{cty.StringVal("echo hello")}),
	}

	scripts, err := collectScripts(cty.ObjectVal(conf))
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	done := make(chan struct{})

	var runErr error
	go func() {
		defer close(done)
		runErr = runScripts(ctx, o, c, scripts, true)
	}()

	select {
	case <-disconnected:
		t.Fatal("communicator disconnected before command completed")
	case <-completed:
	}

	<-done
	if runErr != nil {
		t.Fatal(err)
	}
}

// Validate that Stop can Close can be called even when not provisioning.
func TestResourceProvisioner_StopClose(t *testing.T) {
	p := New()
	if err := p.Stop(); err != nil {
		t.Fatal(err)
	}
	p.Close()
}

func TestResourceProvisioner_connectionRequired(t *testing.T) {
	p := New()
	resp := p.ProvisionResource(provisioners.ProvisionResourceRequest{})
	if !resp.Diagnostics.HasErrors() {
		t.Fatal("expected error")
	}

	got := resp.Diagnostics.Err().Error()
	if !strings.Contains(got, "Missing connection") {
		t.Fatalf("expected 'Missing connection' error: got %q", got)
	}
}

func TestResourceProvisioner_nullsInOptionals(t *testing.T) {
	output := cli.NewMockUi()
	p := New()
	schema := p.GetSchema().Provisioner

	for i, cfg := range []cty.Value{
		cty.ObjectVal(map[string]cty.Value{
			"script": cty.StringVal("echo"),
			"inline": cty.NullVal(cty.List(cty.String)),
		}),
		cty.ObjectVal(map[string]cty.Value{
			"inline": cty.ListVal([]cty.Value{
				cty.NullVal(cty.String),
			}),
		}),
		cty.ObjectVal(map[string]cty.Value{
			"script": cty.NullVal(cty.String),
		}),
		cty.ObjectVal(map[string]cty.Value{
			"scripts": cty.NullVal(cty.List(cty.String)),
		}),
		cty.ObjectVal(map[string]cty.Value{
			"scripts": cty.ListVal([]cty.Value{
				cty.NullVal(cty.String),
			}),
		}),
	} {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {

			cfg, err := schema.CoerceValue(cfg)
			if err != nil {
				t.Fatal(err)
			}

			// verifying there are no panics
			p.ProvisionResource(provisioners.ProvisionResourceRequest{
				Config:   cfg,
				UIOutput: output,
			})
		})
	}
}

// uploadRecorder records the paths received by the cleanup Upload calls while
// delegating everything else to the wrapped communicator.
type uploadRecorder struct {
	communicator.Communicator
	uploads []string
}

func (c *uploadRecorder) Upload(path string, _ io.Reader) error {
	c.uploads = append(c.uploads, path)
	return nil
}

// inlineScripts builds a single script from an "inline" configuration, the
// same way ProvisionResource does.
func inlineScripts(t *testing.T, content string) []io.ReadCloser {
	t.Helper()
	scripts, err := collectScripts(cty.ObjectVal(map[string]cty.Value{
		"inline": cty.ListVal([]cty.Value{cty.StringVal(content)}),
	}))
	if err != nil {
		t.Fatal(err)
	}
	return scripts
}

// TestRunScripts_quotesShellSensitiveScriptPath is a regression test for
// OpenTofu #4330: for SSH connections to Unix targets the remote command is
// interpreted by the user's POSIX shell, so the script path command token
// must be quoted to stay literal. The command is executed by a real shell,
// which makes any reinterpretation (like the injected file below) observable.
func TestRunScripts_quotesShellSensitiveScriptPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell interpretation is a Unix-only concern")
	}
	shellPath, err := exec.LookPath("sh")
	if err != nil {
		t.Skipf("POSIX shell not available: %s", err)
	}

	dir := t.TempDir()
	// The injected file is only created if the shell-sensitive script name
	// gets reinterpreted by the shell. The apostrophes are balanced so
	// that, with the legacy unquoted command, the injection is actually
	// executed by the shell.
	markerName := "injected-by-remote-exec"
	marker := filepath.Join(dir, markerName)
	rawPath := filepath.Join(dir, "tf-4330 'script'; touch "+markerName+"; echo \"done.sh\"")
	ranOk := filepath.Join(dir, "ran-ok")
	scriptContent := "#!/bin/sh\ntouch \"" + filepath.ToSlash(ranOk) + "\"\n"
	// Only shell-insensitive names go into the script body; the script must
	// behave identically regardless of its own filename.
	//
	// The fake communicator stands in for the uploaded file; enforcing the
	// chmod's outcome is taken care by the SSH communicator test.
	if err := os.WriteFile(rawPath, []byte(scriptContent), 0o755); err != nil {
		t.Fatal(err)
	}

	mock := &communicator.MockCommunicator{
		RemoteScriptPath: rawPath,
		UploadScripts:    map[string]string{rawPath: scriptContent},
	}
	recorder := &uploadRecorder{Communicator: mock}

	var gotCommands []string
	mock.CommandFunc = func(cmd *remote.Cmd) error {
		gotCommands = append(gotCommands, cmd.Command)
		sh := exec.Command(shellPath, "-c", cmd.Command)
		sh.Dir = dir
		sh.Stdout = cmd.Stdout
		sh.Stderr = cmd.Stderr
		if err := sh.Run(); err != nil {
			cmd.SetExitStatus(1, err)
			return nil
		}
		cmd.SetExitStatus(0, nil)
		return nil
	}

	if err := runScripts(context.Background(), cli.NewMockUi(), recorder, inlineScripts(t, scriptContent), true); err != nil {
		t.Fatalf("runScripts failed: %s", err)
	}

	// The command token is the POSIX-quoted raw path.
	want := shquot.POSIXShell([]string{rawPath})
	if len(gotCommands) != 1 || gotCommands[0] != want {
		t.Fatalf("expected exactly one command %q, got %#v", want, gotCommands)
	}
	if gotCommands[0] == rawPath {
		t.Fatal("command was not quoted")
	}

	// Upload (guaranteed by the mock's content match on the raw path) and
	// cleanup still receive the raw path.
	if len(recorder.uploads) != 1 || recorder.uploads[0] != rawPath {
		t.Fatalf("expected upload records %#v, got %#v", []string{rawPath, rawPath}, recorder.uploads)
	}

	// The script really ran through its literal filename.
	if _, err := os.Stat(ranOk); err != nil {
		t.Fatalf("script did not execute its body: %s", err)
	}

	// The shell-sensitive name stayed literal: no injected side effect.
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("script path was reinterpreted by the shell: %s was created", marker)
	}
}

// TestRunScripts_preservesWindowsAndWinRMCommands verifies that the existing
// unquoted command construction is preserved for Windows SSH targets and
// WinRM connections, with both plain and shell-sensitive path values. This
// establishes no regression only; it does not establish safety of the legacy
// command construction on those targets.
func TestRunScripts_preservesWindowsAndWinRMCommands(t *testing.T) {
	cases := map[string]struct {
		path string
		conn cty.Value
	}{
		"windows ssh": {
			path: "C:/windows/temp/terraform_%RAND%.cmd",
			conn: cty.ObjectVal(map[string]cty.Value{
				"type":            cty.StringVal("ssh"),
				"target_platform": cty.StringVal("windows"),
			}),
		},
		"windows ssh with spaces": {
			path: "C:/Program Files/tf provisioner/terraform_%RAND%.cmd",
			conn: cty.ObjectVal(map[string]cty.Value{
				"type":            cty.StringVal("ssh"),
				"target_platform": cty.StringVal("windows"),
			}),
		},
		"winrm": {
			path: "C:/windows/temp/terraform_%RAND%.cmd",
			conn: cty.ObjectVal(map[string]cty.Value{"type": cty.StringVal("winrm")}),
		},
		"winrm with spaces": {
			path: "C:/Program Files/tf provisioner/terraform_%RAND%.cmd",
			conn: cty.ObjectVal(map[string]cty.Value{"type": cty.StringVal("winrm")}),
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			mock := &communicator.MockCommunicator{
				RemoteScriptPath: tc.path,
				UploadScripts:    map[string]string{tc.path: "exit 0"},
			}
			var gotCommands []string
			mock.CommandFunc = func(cmd *remote.Cmd) error {
				gotCommands = append(gotCommands, cmd.Command)
				cmd.SetExitStatus(0, nil)
				return nil
			}

			recorder := &uploadRecorder{Communicator: mock}
			if err := runScripts(context.Background(), cli.NewMockUi(), recorder, inlineScripts(t, "exit 0"), posixQuotedCommands(tc.conn)); err != nil {
				t.Fatalf("runScripts failed: %s", err)
			}

			if len(gotCommands) != 1 || gotCommands[0] != tc.path {
				t.Fatalf("expected command %q to be passed through unquoted, got %#v", tc.path, gotCommands)
			}
		})
	}
}

// TestPosixQuotedCommands verifies the routing decision: it depends only on
// the validated connection settings, never on the local GOOS or on the path
// value. SSH targets default to Unix, preserved commands are Windows SSH and
// WinRM.
func TestPosixQuotedCommands(t *testing.T) {
	sshType := cty.StringVal("ssh")
	winrmType := cty.StringVal("winrm")
	unix := cty.StringVal("unix")
	windows := cty.StringVal("windows")
	nullStr := cty.NullVal(cty.String)

	cases := map[string]struct {
		conn map[string]cty.Value
		want bool
	}{
		"ssh, omitted target_platform": {
			conn: map[string]cty.Value{"type": sshType},
			want: true,
		},
		"ssh, null target_platform": {
			conn: map[string]cty.Value{"type": sshType, "target_platform": nullStr},
			want: true,
		},
		"ssh, unix target_platform": {
			conn: map[string]cty.Value{"type": sshType, "target_platform": unix},
			want: true,
		},
		"ssh, windows target_platform": {
			conn: map[string]cty.Value{"type": sshType, "target_platform": windows},
			want: false,
		},
		"omitted type defaults to ssh with omitted target_platform": {
			conn: map[string]cty.Value{},
			want: true,
		},
		"null type with omitted target_platform": {
			conn: map[string]cty.Value{"type": nullStr},
			want: true,
		},
		"null type with null target_platform": {
			conn: map[string]cty.Value{"type": nullStr, "target_platform": nullStr},
			want: true,
		},
		"winrm ignores target_platform": {
			conn: map[string]cty.Value{"type": winrmType, "target_platform": unix},
			want: false,
		},
		"winrm with omitted target_platform": {
			conn: map[string]cty.Value{"type": winrmType},
			want: false,
		},
		"null type with windows target_platform": {
			conn: map[string]cty.Value{"type": nullStr, "target_platform": windows},
			want: false,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := posixQuotedCommands(cty.ObjectVal(tc.conn)); got != tc.want {
				t.Fatalf("wrong routing result: got %t, want %t", got, tc.want)
			}
		})
	}
}

func normaliseNewlines(input string) string {
	return strings.ReplaceAll(input, "\r\n", "\n")
}
