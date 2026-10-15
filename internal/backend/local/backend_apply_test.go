// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package local

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/vmvarela/ghoten/internal/addrs"
	"github.com/vmvarela/ghoten/internal/backend"
	"github.com/vmvarela/ghoten/internal/command/arguments"
	"github.com/vmvarela/ghoten/internal/command/clistate"
	"github.com/vmvarela/ghoten/internal/command/views"
	"github.com/vmvarela/ghoten/internal/configs/configschema"
	"github.com/vmvarela/ghoten/internal/depsfile"
	"github.com/vmvarela/ghoten/internal/encryption"
	"github.com/vmvarela/ghoten/internal/initwd"
	"github.com/vmvarela/ghoten/internal/plans"
	"github.com/vmvarela/ghoten/internal/providers"
	"github.com/vmvarela/ghoten/internal/states"
	"github.com/vmvarela/ghoten/internal/states/statemgr"
	"github.com/vmvarela/ghoten/internal/terminal"
	"github.com/vmvarela/ghoten/internal/tfdiags"
)

func TestLocal_applyBasic(t *testing.T) {
	b := TestLocal(t)

	p := TestLocalProvider(t, b, "test", applyFixtureSchema())
	p.ApplyResourceChangeResponse = &providers.ApplyResourceChangeResponse{NewState: cty.ObjectVal(map[string]cty.Value{
		"id":  cty.StringVal("yes"),
		"ami": cty.StringVal("bar"),
	})}

	op, done := testOperationApply(t, "./testdata/apply")

	run, err := b.Operation(context.Background(), op)
	if err != nil {
		t.Fatalf("bad: %s", err)
	}
	<-run.Done()
	if run.Result != backend.OperationSuccess {
		t.Fatal("operation failed")
	}

	if p.ReadResourceCalled {
		t.Fatal("ReadResource should not be called")
	}

	if !p.PlanResourceChangeCalled {
		t.Fatal("diff should be called")
	}

	if !p.ApplyResourceChangeCalled {
		t.Fatal("apply should be called")
	}

	checkState(t, b.StateOutPath, `
test_instance.foo:
  ID = yes
  provider = provider["registry.opentofu.org/hashicorp/test"]
  ami = bar
`)

	if errOutput := done(t).Stderr(); errOutput != "" {
		t.Fatalf("unexpected error output:\n%s", errOutput)
	}
}
func TestLocal_applyCheck(t *testing.T) {
	b := TestLocal(t)

	p := TestLocalProvider(t, b, "test", applyFixtureSchema())
	p.ApplyResourceChangeResponse = &providers.ApplyResourceChangeResponse{NewState: cty.ObjectVal(map[string]cty.Value{
		"id":  cty.StringVal("yes"),
		"ami": cty.StringVal("bar"),
	})}

	op, done := testOperationApply(t, "./testdata/apply-check")

	run, err := b.Operation(context.Background(), op)
	if err != nil {
		t.Fatalf("bad: %s", err)
	}
	<-run.Done()
	if run.Result != backend.OperationSuccess {
		t.Fatal("operation failed")
	}

	if p.ReadResourceCalled {
		t.Fatal("ReadResource should not be called")
	}

	if !p.PlanResourceChangeCalled {
		t.Fatal("diff should be called")
	}

	if !p.ApplyResourceChangeCalled {
		t.Fatal("apply should be called")
	}

	d := done(t)
	if errOutput := d.Stderr(); errOutput != "" {
		t.Fatalf("unexpected error output:\n%s", errOutput)
	}

	if stdOutput := d.Stdout(); strings.Contains(stdOutput, "Check block assertion known after apply") {
		// As we are running an auto approved plan the warning that was
		// generated during the plan should have been hidden.
		t.Fatalf("std output contained unexpected check output:\n%s", stdOutput)
	}
}

func TestLocal_applyEmptyDir(t *testing.T) {
	b := TestLocal(t)

	p := TestLocalProvider(t, b, "test", providers.ProviderSchema{})
	p.ApplyResourceChangeResponse = &providers.ApplyResourceChangeResponse{NewState: cty.ObjectVal(map[string]cty.Value{"id": cty.StringVal("yes")})}

	op, done := testOperationApply(t, "./testdata/empty")

	run, err := b.Operation(context.Background(), op)
	if err != nil {
		t.Fatalf("bad: %s", err)
	}
	<-run.Done()
	if run.Result == backend.OperationSuccess {
		t.Fatal("operation succeeded; want error")
	}

	if p.ApplyResourceChangeCalled {
		t.Fatal("apply should not be called")
	}

	if _, err := os.Stat(b.StateOutPath); err == nil {
		t.Fatal("should not exist")
	}

	// the backend should be unlocked after a run
	assertBackendStateUnlocked(t, b)

	if got, want := done(t).Stderr(), "Error: No configuration files"; !strings.Contains(got, want) {
		t.Fatalf("unexpected error output:\n%s\nwant: %s", got, want)
	}
}

func TestLocal_applyEmptyDirDestroy(t *testing.T) {
	b := TestLocal(t)

	p := TestLocalProvider(t, b, "test", providers.ProviderSchema{})
	p.ApplyResourceChangeResponse = &providers.ApplyResourceChangeResponse{}

	op, done := testOperationApply(t, "./testdata/empty")
	op.PlanMode = plans.DestroyMode

	run, err := b.Operation(context.Background(), op)
	if err != nil {
		t.Fatalf("bad: %s", err)
	}
	<-run.Done()
	if run.Result != backend.OperationSuccess {
		t.Fatalf("apply operation failed")
	}

	if p.ApplyResourceChangeCalled {
		t.Fatal("apply should not be called")
	}

	checkState(t, b.StateOutPath, `<no state>`)

	if errOutput := done(t).Stderr(); errOutput != "" {
		t.Fatalf("unexpected error output:\n%s", errOutput)
	}
}

func TestLocal_applyError(t *testing.T) {
	b := TestLocal(t)

	schema := providers.ProviderSchema{
		ResourceTypes: map[string]providers.Schema{
			"test_instance": {
				Block: &configschema.Block{
					Attributes: map[string]*configschema.Attribute{
						"ami": {Type: cty.String, Optional: true},
						"id":  {Type: cty.String, Computed: true},
					},
				},
			},
		},
	}
	p := TestLocalProvider(t, b, "test", schema)

	var lock sync.Mutex
	errored := false
	p.ApplyResourceChangeFn = func(
		r providers.ApplyResourceChangeRequest) providers.ApplyResourceChangeResponse {

		lock.Lock()
		defer lock.Unlock()
		var diags tfdiags.Diagnostics

		ami := r.Config.GetAttr("ami").AsString()
		if !errored && ami == "error" {
			errored = true
			diags = diags.Append(errors.New("ami error"))
			return providers.ApplyResourceChangeResponse{
				Diagnostics: diags,
			}
		}
		return providers.ApplyResourceChangeResponse{
			Diagnostics: diags,
			NewState: cty.ObjectVal(map[string]cty.Value{
				"id":  cty.StringVal("foo"),
				"ami": cty.StringVal("bar"),
			}),
		}
	}

	op, done := testOperationApply(t, "./testdata/apply-error")

	run, err := b.Operation(context.Background(), op)
	if err != nil {
		t.Fatalf("bad: %s", err)
	}
	<-run.Done()
	if run.Result == backend.OperationSuccess {
		t.Fatal("operation succeeded; want failure")
	}

	checkState(t, b.StateOutPath, `
test_instance.foo:
  ID = foo
  provider = provider["registry.opentofu.org/hashicorp/test"]
  ami = bar
	`)

	// the backend should be unlocked after a run
	assertBackendStateUnlocked(t, b)

	if got, want := done(t).Stderr(), "Error: ami error"; !strings.Contains(got, want) {
		t.Fatalf("unexpected error output:\n%s\nwant: %s", got, want)
	}
}

func TestLocal_applyBackendFail(t *testing.T) {
	b := TestLocal(t)

	p := TestLocalProvider(t, b, "test", applyFixtureSchema())

	p.ApplyResourceChangeResponse = &providers.ApplyResourceChangeResponse{
		NewState: cty.ObjectVal(map[string]cty.Value{
			"id":  cty.StringVal("yes"),
			"ami": cty.StringVal("bar"),
		}),
		Diagnostics: tfdiags.Diagnostics.Append(nil, errors.New("error before backend failure")),
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current working directory")
	}
	t.Chdir(filepath.Dir(b.StatePath))

	op, done := testOperationApply(t, wd+"/testdata/apply")

	b.Backend = &backendWithFailingState{}

	run, err := b.Operation(context.Background(), op)
	if err != nil {
		t.Fatalf("bad: %s", err)
	}
	<-run.Done()

	output := done(t)

	if run.Result == backend.OperationSuccess {
		t.Fatalf("apply succeeded; want error")
	}

	diagErr := output.Stderr()

	if !strings.Contains(diagErr, "Error saving state: fake failure") {
		t.Fatalf("missing \"fake failure\" message in diags:\n%s", diagErr)
	}

	if !strings.Contains(diagErr, "error before backend failure") {
		t.Fatalf("missing 'error before backend failure' diagnostic from apply")
	}

	// The fallback behavior should've created a file errored.tfstate in the
	// current working directory.
	checkState(t, "errored.tfstate", `
test_instance.foo: (tainted)
  ID = yes
  provider = provider["registry.opentofu.org/hashicorp/test"]
  ami = bar
	`)

	// the backend should be unlocked after a run
	assertBackendStateUnlocked(t, b)
}

func TestLocal_applyRefreshFalse(t *testing.T) {
	b := TestLocal(t)

	p := TestLocalProvider(t, b, "test", planFixtureSchema())
	testStateFile(t, b.StatePath, testPlanState())

	op, done := testOperationApply(t, "./testdata/plan")

	run, err := b.Operation(context.Background(), op)
	if err != nil {
		t.Fatalf("bad: %s", err)
	}
	<-run.Done()
	if run.Result != backend.OperationSuccess {
		t.Fatalf("plan operation failed")
	}

	if p.ReadResourceCalled {
		t.Fatal("ReadResource should not be called")
	}

	if errOutput := done(t).Stderr(); errOutput != "" {
		t.Fatalf("unexpected error output:\n%s", errOutput)
	}
}

type backendWithFailingState struct {
	Local
}

func (b *backendWithFailingState) StateMgr(_ context.Context, name string) (statemgr.Full, error) {
	return &failingState{
		statemgr.NewFilesystem("failing-state.tfstate", encryption.StateEncryptionDisabled()),
	}, nil
}

type failingState struct {
	*statemgr.Filesystem
}

var _ statemgr.Full = (*failingState)(nil)

func (s failingState) WriteState(state *states.State) error {
	return errors.New("fake failure")
}

func testOperationApply(t *testing.T, configDir string) (*backend.Operation, func(*testing.T) *terminal.TestOutput) {
	t.Helper()

	_, configLoader := initwd.MustLoadConfigForTests(t, configDir, "tests")

	streams, done := terminal.StreamsForTesting(t)
	view := views.NewOperation(arguments.ViewHuman, false, views.NewView(streams))

	// Many of our tests use an overridden "test" provider that's just in-memory
	// inside the test process, not a separate plugin on disk.
	depLocks := depsfile.NewLocks()
	depLocks.SetProviderOverridden(addrs.MustParseProviderSourceString("registry.opentofu.org/hashicorp/test"))

	return &backend.Operation{
		Type:            backend.OperationTypeApply,
		Encryption:      encryption.Disabled(),
		ConfigDir:       configDir,
		ConfigLoader:    configLoader,
		StateLocker:     clistate.NewNoopLocker(),
		View:            view,
		DependencyLocks: depLocks,
	}, done
}

// applyFixtureSchema returns a schema suitable for processing the
// configuration in testdata/apply . This schema should be
// assigned to a mock provider named "test".
func applyFixtureSchema() providers.ProviderSchema {
	return providers.ProviderSchema{
		ResourceTypes: map[string]providers.Schema{
			"test_instance": {
				Block: &configschema.Block{
					Attributes: map[string]*configschema.Attribute{
						"ami": {Type: cty.String, Optional: true},
						"id":  {Type: cty.String, Computed: true},
					},
				},
			},
		},
	}
}

// providerSchemaRequiredAttribute returns a provider schema with a required attribute
func providerSchemaRequiredAttribute() providers.ProviderSchema {
	return providers.ProviderSchema{
		Provider: providers.Schema{
			Block: &configschema.Block{
				Attributes: map[string]*configschema.Attribute{
					"required": {Type: cty.String, Required: true},
				},
			},
		},
	}
}

func TestApply_applyCanceledAutoApprove(t *testing.T) {
	b := TestLocal(t)

	TestLocalProvider(t, b, "test", applyFixtureSchema())

	op, done := testOperationApply(t, "./testdata/apply")
	op.AutoApprove = true
	defer func() {
		output := done(t)
		if !strings.Contains(output.Stderr(), "execution halted") {
			t.Fatal("expected 'execution halted', got:\n", output.All())
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	testHookStopPlanApply = cancel
	defer func() {
		testHookStopPlanApply = nil
	}()

	run, err := b.Operation(ctx, op)
	if err != nil {
		t.Fatalf("error starting operation: %v", err)
	}

	<-run.Done()
	if run.Result == backend.OperationSuccess {
		t.Fatal("expected apply operation to fail")
	}

}

func TestGetEnvAsInt(t *testing.T) {
	const testEnv = "TEST_GET_ENV_AS_INT"

	tests := []struct {
		name         string
		setEnv       bool
		envValue     string
		defaultValue int
		wantValue    int
		wantError    bool
	}{
		{
			name:         "env not set returns default",
			setEnv:       false,
			defaultValue: 20,
			wantValue:    20,
		},
		{
			name:         "valid integer is parsed",
			setEnv:       true,
			envValue:     "30",
			defaultValue: 20,
			wantValue:    30,
		},
		{
			name:      "empty value returns error",
			setEnv:    true,
			envValue:  "",
			wantError: true,
		},
		{
			name:      "non-integer value returns error",
			setEnv:    true,
			envValue:  "abc",
			wantError: true,
		},
		{
			name:      "float value returns error",
			setEnv:    true,
			envValue:  "1.5",
			wantError: true,
		},
		{
			name:      "value exceeding integer range returns error",
			setEnv:    true,
			envValue:  "99999999999999999999999",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setEnv {
				t.Setenv(testEnv, tt.envValue)
			} else {
				original, wasSet := os.LookupEnv(testEnv)
				if err := os.Unsetenv(testEnv); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if wasSet {
						if err := os.Setenv(testEnv, original); err != nil {
							t.Errorf("failed to restore %s: %s", testEnv, err)
						}
					} else if err := os.Unsetenv(testEnv); err != nil {
						t.Errorf("failed to unset %s: %s", testEnv, err)
					}
				})
			}

			got, diags := getEnvAsInt(testEnv, tt.defaultValue)
			if tt.wantError {
				if !diags.HasErrors() {
					t.Errorf("expected error but got none, value=%d", got)
				}
				return
			}
			if diags.HasErrors() {
				t.Fatalf("unexpected error: %s", diags.Err())
			}
			if got != tt.wantValue {
				t.Errorf("got %d, want %d", got, tt.wantValue)
			}
		})
	}
}

// runInvalidPersistIntervalApply runs an apply operation with
// TF_STATE_PERSIST_INTERVAL set to an invalid value, using a real state
// locker backed by a real filesystem state (recorded via lockRecordingBackend)
// so release of the state lock can be proven.
func runInvalidPersistIntervalApply(t *testing.T, envValue string) {
	t.Helper()
	t.Setenv(persistIntervalEnvironmentVariableName, envValue)

	b := TestLocal(t)
	p := TestLocalProvider(t, b, "test", applyFixtureSchema())

	rec := newLockRecordingBackend(t)
	b.Backend = rec

	op, done := testOperationApply(t, "./testdata/apply")

	// Replace the noop locker with a real one so the deferred unlock in
	// opApply is exercised against a real filesystem lock.
	streams, _ := terminal.StreamsForTesting(t)
	view := views.NewView(streams)
	op.StateLocker = clistate.NewLocker(0, views.NewStateLocker(arguments.ViewOptions{ViewType: arguments.ViewHuman}, view))

	run, err := b.Operation(context.Background(), op)
	if err != nil {
		t.Fatalf("unexpected error starting operation: %v", err)
	}
	<-run.Done()
	if run.Result == backend.OperationSuccess {
		t.Fatalf("expected operation to fail with invalid %s=%s", persistIntervalEnvironmentVariableName, envValue)
	}

	if p.ApplyResourceChangeCalled {
		t.Fatal("provider apply should not have been called")
	}

	if got, want := done(t).Stderr(), "Invalid environment variable value"; !strings.Contains(got, want) {
		t.Errorf("expected stderr to contain %q, got:\n%s", want, got)
	}

	if locks, unlocks := rec.counts(); locks != 1 || unlocks != 1 {
		t.Errorf("expected the state lock to be taken and then released, got %d locks and %d unlocks", locks, unlocks)
	}

	// Reacquire the same real filesystem state lock to prove the backend's
	// lock was actually released and the lock is available.
	lockID, err := rec.state.Filesystem.Lock(t.Context(), statemgr.NewLockInfo())
	if err != nil {
		t.Errorf("expected to be able to reacquire the state lock, got error: %s", err)
	} else if err := rec.state.Filesystem.Unlock(t.Context(), lockID); err != nil {
		t.Errorf("expected to be able to release the reacquired state lock, got error: %s", err)
	}
}

func TestLocal_applyInvalidPersistInterval(t *testing.T) {
	t.Run("non-integer value causes error diagnostic", func(t *testing.T) {
		runInvalidPersistIntervalApply(t, "abc")
	})

	t.Run("below minimum value causes error diagnostic", func(t *testing.T) {
		runInvalidPersistIntervalApply(t, "5")
	})

	t.Run("one below minimum value causes error diagnostic", func(t *testing.T) {
		runInvalidPersistIntervalApply(t, "19")
	})

	t.Run("value at the minimum boundary succeeds", func(t *testing.T) {
		t.Setenv(persistIntervalEnvironmentVariableName, "20")

		b := TestLocal(t)
		p := TestLocalProvider(t, b, "test", applyFixtureSchema())
		p.ApplyResourceChangeResponse = &providers.ApplyResourceChangeResponse{NewState: cty.ObjectVal(map[string]cty.Value{
			"id":  cty.StringVal("yes"),
			"ami": cty.StringVal("bar"),
		})}

		op, done := testOperationApply(t, "./testdata/apply")

		run, err := b.Operation(context.Background(), op)
		if err != nil {
			t.Fatalf("unexpected error starting operation: %v", err)
		}
		<-run.Done()
		if run.Result != backend.OperationSuccess {
			t.Fatalf("expected operation to succeed with %s=20", persistIntervalEnvironmentVariableName)
		}
		if !p.ApplyResourceChangeCalled {
			t.Fatal("provider apply should have been called")
		}
		if errOutput := done(t).Stderr(); errOutput != "" {
			t.Fatalf("unexpected error output:\n%s", errOutput)
		}
	})
}
