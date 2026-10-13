// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package remoteexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"github.com/apparentlymart/go-shquot/shquot"
	"github.com/mitchellh/go-linereader"
	"github.com/vmvarela/ghoten/internal/communicator"
	"github.com/vmvarela/ghoten/internal/communicator/remote"
	"github.com/vmvarela/ghoten/internal/communicator/ssh"
	"github.com/vmvarela/ghoten/internal/configs/configschema"
	"github.com/vmvarela/ghoten/internal/provisioners"
	"github.com/vmvarela/ghoten/internal/tfdiags"
	"github.com/zclconf/go-cty/cty"
)

func New() provisioners.Interface {
	ctx, cancel := context.WithCancel(context.Background())
	return &provisioner{
		ctx:    ctx,
		cancel: cancel,
	}
}

type provisioner struct {
	// We store a context here tied to the lifetime of the provisioner.
	// This allows the Stop method to cancel any in-flight requests.
	ctx    context.Context
	cancel context.CancelFunc
}

func (p *provisioner) GetSchema() (resp provisioners.GetSchemaResponse) {
	schema := &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"inline": {
				Type:     cty.List(cty.String),
				Optional: true,
			},
			"script": {
				Type:     cty.String,
				Optional: true,
			},
			"scripts": {
				Type:     cty.List(cty.String),
				Optional: true,
			},
		},
		Ephemeral: true,
	}

	resp.Provisioner = schema
	return resp
}

func (p *provisioner) ValidateProvisionerConfig(req provisioners.ValidateProvisionerConfigRequest) (resp provisioners.ValidateProvisionerConfigResponse) {
	cfg, err := p.GetSchema().Provisioner.CoerceValue(req.Config)
	if err != nil {
		resp.Diagnostics = resp.Diagnostics.Append(tfdiags.WholeContainingBody(
			tfdiags.Error,
			"Invalid remote-exec provisioner configuration",
			err.Error(),
		))
		return resp
	}

	inline := cfg.GetAttr("inline")
	script := cfg.GetAttr("script")
	scripts := cfg.GetAttr("scripts")

	set := 0
	if !inline.IsNull() {
		set++
	}
	if !script.IsNull() {
		set++
	}
	if !scripts.IsNull() {
		set++
	}
	if set != 1 {
		resp.Diagnostics = resp.Diagnostics.Append(tfdiags.WholeContainingBody(
			tfdiags.Error,
			"Invalid remote-exec provisioner configuration",
			`Only one of "inline", "script", or "scripts" must be set`,
		))
	}
	return resp
}

func (p *provisioner) ProvisionResource(req provisioners.ProvisionResourceRequest) (resp provisioners.ProvisionResourceResponse) {
	if req.Connection.IsNull() {
		resp.Diagnostics = resp.Diagnostics.Append(tfdiags.WholeContainingBody(
			tfdiags.Error,
			"remote-exec provisioner error",
			"Missing connection configuration for provisioner.",
		))
		return resp
	}

	comm, err := communicator.New(req.Connection)
	if err != nil {
		resp.Diagnostics = resp.Diagnostics.Append(tfdiags.WholeContainingBody(
			tfdiags.Error,
			"remote-exec provisioner error",
			err.Error(),
		))
		return resp
	}

	// Collect the scripts
	scripts, err := collectScripts(req.Config)
	if err != nil {
		resp.Diagnostics = resp.Diagnostics.Append(tfdiags.WholeContainingBody(
			tfdiags.Error,
			"remote-exec provisioner error",
			err.Error(),
		))
		return resp
	}
	for _, s := range scripts {
		defer s.Close()
	}

	// Copy and execute each script
	if err := runScripts(p.ctx, req.UIOutput, comm, scripts, posixQuotedCommands(req.Connection)); err != nil {
		resp.Diagnostics = resp.Diagnostics.Append(tfdiags.WholeContainingBody(
			tfdiags.Error,
			"remote-exec provisioner error",
			err.Error(),
		))
		return resp
	}

	return resp
}

func (p *provisioner) Stop() error {
	p.cancel()
	return nil
}

func (p *provisioner) Close() error {
	return nil
}

// generateScripts takes the configuration and creates a script from each inline config
func generateScripts(inline cty.Value) ([]string, error) {
	var lines []string
	for _, l := range inline.AsValueSlice() {
		if l.IsNull() {
			return nil, errors.New("invalid null string in 'scripts'")
		}

		s := l.AsString()
		if s == "" {
			return nil, errors.New("invalid empty string in 'scripts'")
		}
		lines = append(lines, s)
	}
	lines = append(lines, "")

	return []string{strings.Join(lines, "\n")}, nil
}

// collectScripts is used to collect all the scripts we need
// to execute in preparation for copying them.
func collectScripts(v cty.Value) ([]io.ReadCloser, error) {
	// Check if inline
	if inline := v.GetAttr("inline"); !inline.IsNull() {
		scripts, err := generateScripts(inline)
		if err != nil {
			return nil, err
		}

		var r []io.ReadCloser
		for _, script := range scripts {
			r = append(r, io.NopCloser(bytes.NewReader([]byte(script))))
		}

		return r, nil
	}

	// Collect scripts
	var scripts []string
	if script := v.GetAttr("script"); !script.IsNull() {
		s := script.AsString()
		if s == "" {
			return nil, errors.New("invalid empty string in 'script'")
		}
		scripts = append(scripts, s)
	}

	if scriptList := v.GetAttr("scripts"); !scriptList.IsNull() {
		for _, script := range scriptList.AsValueSlice() {
			if script.IsNull() {
				return nil, errors.New("invalid null string in 'script'")
			}
			s := script.AsString()
			if s == "" {
				return nil, errors.New("invalid empty string in 'script'")
			}
			scripts = append(scripts, s)
		}
	}

	// Open all the scripts
	var fhs []io.ReadCloser
	for _, s := range scripts {
		fh, err := os.Open(s)
		if err != nil {
			for _, fh := range fhs {
				fh.Close()
			}
			return nil, fmt.Errorf("Failed to open script '%s': %w", s, err)
		}
		fhs = append(fhs, fh)
	}

	// Done, return the file handles
	return fhs, nil
}

// posixQuotedCommands reports whether the remote command token sent for this
// connection is interpreted by a POSIX shell, based on the validated
// connection block only. SSH connections default to a Unix target when
// "target_platform" is omitted or null; Windows SSH targets and WinRM keep
// the legacy unquoted command construction.
func posixQuotedCommands(conn cty.Value) bool {
	typ := connStringAttr(conn, "type")
	if typ == "winrm" || (typ != "" && typ != "ssh") {
		return false
	}
	return connStringAttr(conn, "target_platform") != ssh.TargetPlatformWindows
}

func connStringAttr(v cty.Value, name string) string {
	if v.IsNull() {
		return ""
	}
	t := v.Type()
	if !t.IsObjectType() || !t.HasAttribute(name) {
		return ""
	}
	attr := v.GetAttr(name)
	if attr.IsNull() {
		return ""
	}
	return attr.AsString()
}

// runScripts is used to copy and execute a set of scripts. quoteCommand
// selects POSIX-shell quoting of the script path command for SSH connections
// to Unix targets; it must be false for Windows SSH and WinRM targets, where
// the command token is passed through unquoted.
func runScripts(ctx context.Context, o provisioners.UIOutput, comm communicator.Communicator, scripts []io.ReadCloser, quoteCommand bool) error {
	retryCtx, cancel := context.WithTimeout(ctx, comm.Timeout())
	defer cancel()

	// Wait and retry until we establish the connection
	err := communicator.Retry(retryCtx, func() error {
		return comm.Connect(o)
	})
	if err != nil {
		return err
	}

	// Wait for the context to end and then disconnect
	go func() {
		<-ctx.Done()
		if err := comm.Disconnect(); err != nil {
			log.Printf("[ERROR] Unable to close provisioner connection: %s", err.Error())
		}
	}()

	for _, script := range scripts {
		var cmd *remote.Cmd

		outR, outW := io.Pipe()
		errR, errW := io.Pipe()
		defer outW.Close()
		defer errW.Close()

		go copyUIOutput(o, outR)
		go copyUIOutput(o, errR)

		remotePath := comm.ScriptPath()

		if err := comm.UploadScript(remotePath, script); err != nil {
			return fmt.Errorf("Failed to upload script: %w", err)
		}

		// On a Unix SSH target the server interprets the command via the
		// user's shell, so the script path must stay literal (OpenTofu #4330).
		command := remotePath
		if quoteCommand {
			command = shquot.POSIXShell([]string{remotePath})
		}

		cmd = &remote.Cmd{
			Command: command,
			Stdout:  outW,
			Stderr:  errW,
		}
		if err := comm.Start(cmd); err != nil {
			return fmt.Errorf("Error starting script: %w", err)
		}

		if err := cmd.Wait(); err != nil {
			return err
		}

		// Upload a blank follow up file in the same path to prevent residual
		// script contents from remaining on remote machine
		empty := bytes.NewReader([]byte(""))
		if err := comm.Upload(remotePath, empty); err != nil {
			// This feature is best-effort.
			log.Printf("[WARN] Failed to upload empty follow up script: %v", err)
		}
	}

	return nil
}

func copyUIOutput(o provisioners.UIOutput, r io.Reader) {
	lr := linereader.New(r)
	for line := range lr.Ch {
		o.Output(line)
	}
}
