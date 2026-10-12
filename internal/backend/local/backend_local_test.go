// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package local

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zclconf/go-cty/cty"

	"github.com/vmvarela/ghoten/internal/backend"
	"github.com/vmvarela/ghoten/internal/command/arguments"
	"github.com/vmvarela/ghoten/internal/command/clistate"
	"github.com/vmvarela/ghoten/internal/command/views"
	"github.com/vmvarela/ghoten/internal/configs/configload"
	"github.com/vmvarela/ghoten/internal/configs/configschema"
	"github.com/vmvarela/ghoten/internal/encryption"
	"github.com/vmvarela/ghoten/internal/ghoten"
	"github.com/vmvarela/ghoten/internal/initwd"
	"github.com/vmvarela/ghoten/internal/plans"
	"github.com/vmvarela/ghoten/internal/plans/planfile"
	"github.com/vmvarela/ghoten/internal/providers"
	"github.com/vmvarela/ghoten/internal/states"
	"github.com/vmvarela/ghoten/internal/states/statefile"
	"github.com/vmvarela/ghoten/internal/states/statemgr"
	"github.com/vmvarela/ghoten/internal/terminal"
	"github.com/vmvarela/ghoten/internal/tfdiags"
)

func TestLocalRun(t *testing.T) {
	configDir := "./testdata/empty"
	b := TestLocal(t)

	_, configLoader := initwd.MustLoadConfigForTests(t, configDir, "tests")

	streams, _ := terminal.StreamsForTesting(t)
	view := views.NewView(streams)
	stateLocker := clistate.NewLocker(0, views.NewStateLocker(arguments.ViewOptions{ViewType: arguments.ViewHuman}, view))

	op := &backend.Operation{
		ConfigDir:    configDir,
		ConfigLoader: configLoader,
		Workspace:    backend.DefaultStateName,
		StateLocker:  stateLocker,
	}

	_, _, diags := b.LocalRun(context.Background(), t.Context(), op)
	if diags.HasErrors() {
		t.Fatalf("unexpected error: %s", diags.Err().Error())
	}

	// LocalRun() retains a lock on success
	assertBackendStateLocked(t, b)
}

func TestLocalRun_error(t *testing.T) {
	configDir := "./testdata/invalid"
	b := TestLocal(t)

	// This backend will return an error when asked to RefreshState, which
	// should then cause LocalRun to return with the state unlocked.
	b.Backend = backendWithStateStorageThatFailsRefresh{}

	_, configLoader := initwd.MustLoadConfigForTests(t, configDir, "tests")

	streams, _ := terminal.StreamsForTesting(t)
	view := views.NewView(streams)
	stateLocker := clistate.NewLocker(0, views.NewStateLocker(arguments.ViewOptions{ViewType: arguments.ViewHuman}, view))

	op := &backend.Operation{
		ConfigDir:    configDir,
		ConfigLoader: configLoader,
		Workspace:    backend.DefaultStateName,
		StateLocker:  stateLocker,
	}

	_, _, diags := b.LocalRun(context.Background(), t.Context(), op)
	if !diags.HasErrors() {
		t.Fatal("unexpected success")
	}

	// LocalRun() unlocks the state on failure
	assertBackendStateUnlocked(t, b)
}

func TestLocalRun_cloudPlan(t *testing.T) {
	configDir := "./testdata/apply"
	b := TestLocal(t)

	_, configLoader := initwd.MustLoadConfigForTests(t, configDir, "tests")

	planPath := "./testdata/plan-bookmark/bookmark.json"

	planFile, err := planfile.OpenWrapped(planPath, encryption.PlanEncryptionDisabled())
	if err != nil {
		t.Fatalf("unexpected error reading planfile: %s", err)
	}

	streams, _ := terminal.StreamsForTesting(t)
	view := views.NewView(streams)
	stateLocker := clistate.NewLocker(0, views.NewStateLocker(arguments.ViewOptions{ViewType: arguments.ViewHuman}, view))

	op := &backend.Operation{
		ConfigDir:    configDir,
		ConfigLoader: configLoader,
		PlanFile:     planFile,
		Workspace:    backend.DefaultStateName,
		StateLocker:  stateLocker,
	}

	_, _, diags := b.LocalRun(context.Background(), t.Context(), op)
	if !diags.HasErrors() {
		t.Fatal("unexpected success")
	}

	// LocalRun() unlocks the state on failure
	assertBackendStateUnlocked(t, b)
}

func TestLocalRun_stalePlan(t *testing.T) {
	configDir := "./testdata/apply"
	b := TestLocal(t)

	_, configLoader := initwd.MustLoadConfigForTests(t, configDir, "tests")

	// Write an empty state file with serial 3
	sf, err := os.Create(b.StatePath)
	if err != nil {
		t.Fatalf("unexpected error creating state file %s: %s", b.StatePath, err)
	}
	if err := statefile.Write(statefile.New(states.NewState(), "boop", 3), sf, encryption.StateEncryptionDisabled()); err != nil {
		t.Fatalf("unexpected error writing state file: %s", err)
	}

	// Refresh the state
	sm, err := b.StateMgr(t.Context(), "")
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if err := sm.RefreshState(t.Context()); err != nil {
		t.Fatalf("unexpected error refreshing state: %s", err)
	}

	// Create a minimal plan which also has state file serial 2, so is stale
	backendConfig := cty.ObjectVal(map[string]cty.Value{
		"path":          cty.NullVal(cty.String),
		"workspace_dir": cty.NullVal(cty.String),
	})
	backendConfigRaw, err := plans.NewDynamicValue(backendConfig, backendConfig.Type())
	if err != nil {
		t.Fatal(err)
	}
	plan := &plans.Plan{
		UIMode:  plans.NormalMode,
		Changes: plans.NewChanges(),
		Backend: plans.Backend{
			Type:   "local",
			Config: backendConfigRaw,
		},
		PrevRunState: states.NewState(),
		PriorState:   states.NewState(),
	}
	prevStateFile := statefile.New(plan.PrevRunState, "boop", 1)
	stateFile := statefile.New(plan.PriorState, "boop", 2)

	// Roundtrip through serialization as expected by the operation
	outDir := t.TempDir()
	defer os.RemoveAll(outDir)
	planPath := filepath.Join(outDir, "plan.tfplan")
	planfileArgs := planfile.CreateArgs{
		ConfigSnapshot:       configload.NewEmptySnapshot(),
		PreviousRunStateFile: prevStateFile,
		StateFile:            stateFile,
		Plan:                 plan,
	}
	if err := planfile.Create(planPath, planfileArgs, encryption.PlanEncryptionDisabled()); err != nil {
		t.Fatalf("unexpected error writing planfile: %s", err)
	}
	planFile, err := planfile.OpenWrapped(planPath, encryption.PlanEncryptionDisabled())
	if err != nil {
		t.Fatalf("unexpected error reading planfile: %s", err)
	}

	streams, _ := terminal.StreamsForTesting(t)
	view := views.NewView(streams)
	stateLocker := clistate.NewLocker(0, views.NewStateLocker(arguments.ViewOptions{ViewType: arguments.ViewHuman}, view))

	op := &backend.Operation{
		ConfigDir:    configDir,
		ConfigLoader: configLoader,
		PlanFile:     planFile,
		Workspace:    backend.DefaultStateName,
		StateLocker:  stateLocker,
	}

	_, _, diags := b.LocalRun(context.Background(), t.Context(), op)
	if !diags.HasErrors() {
		t.Fatal("unexpected success")
	}

	// LocalRun() unlocks the state on failure
	assertBackendStateUnlocked(t, b)
}

type backendWithStateStorageThatFailsRefresh struct {
}

var _ backend.Backend = backendWithStateStorageThatFailsRefresh{}

func (b backendWithStateStorageThatFailsRefresh) StateMgr(_ context.Context, workspace string) (statemgr.Full, error) {
	return &stateStorageThatFailsRefresh{}, nil
}

func (b backendWithStateStorageThatFailsRefresh) ConfigSchema() *configschema.Block {
	return &configschema.Block{}
}

func (b backendWithStateStorageThatFailsRefresh) PrepareConfig(in cty.Value) (cty.Value, tfdiags.Diagnostics) {
	return in, nil
}

func (b backendWithStateStorageThatFailsRefresh) Configure(context.Context, cty.Value) tfdiags.Diagnostics {
	return nil
}

func (b backendWithStateStorageThatFailsRefresh) DeleteWorkspace(_ context.Context, name string, force bool) error {
	return fmt.Errorf("unimplemented")
}

func (b backendWithStateStorageThatFailsRefresh) Workspaces(context.Context) ([]string, error) {
	return []string{"default"}, nil
}

type stateStorageThatFailsRefresh struct {
	locked bool
}

var _ statemgr.Full = (*stateStorageThatFailsRefresh)(nil)

func (s *stateStorageThatFailsRefresh) Lock(_ context.Context, info *statemgr.LockInfo) (string, error) {
	if s.locked {
		return "", fmt.Errorf("already locked")
	}
	s.locked = true
	return "locked", nil
}

func (s *stateStorageThatFailsRefresh) Unlock(_ context.Context, id string) error {
	if !s.locked {
		return fmt.Errorf("not locked")
	}
	s.locked = false
	return nil
}

func (s *stateStorageThatFailsRefresh) State() *states.State {
	return nil
}

func (s *stateStorageThatFailsRefresh) GetRootOutputValues(_ context.Context) (map[string]*states.OutputValue, error) {
	return nil, fmt.Errorf("unimplemented")
}

func (s *stateStorageThatFailsRefresh) WriteState(*states.State) error {
	return fmt.Errorf("unimplemented")
}

func (s *stateStorageThatFailsRefresh) MutateState(fn func(*states.State) *states.State) error {
	return fmt.Errorf("unimplemented")
}

func (s *stateStorageThatFailsRefresh) RefreshState(_ context.Context) error {
	return fmt.Errorf("intentionally failing for testing purposes")
}

func (s *stateStorageThatFailsRefresh) PersistState(_ context.Context, schemas *ghoten.Schemas) error {
	return fmt.Errorf("unimplemented")
}

// lockRecordingBackend is a state backend whose state manager delegates to a
// real filesystem state but records every Lock/Unlock call, so tests can
// prove lock release deterministically.
type lockRecordingBackend struct {
	statePath string

	state *lockRecordingState
}

var _ backend.Backend = (*lockRecordingBackend)(nil)

func newLockRecordingBackend(t *testing.T) *lockRecordingBackend {
	t.Helper()
	return &lockRecordingBackend{statePath: filepath.Join(t.TempDir(), "state.tfstate")}
}

func (b *lockRecordingBackend) StateMgr(_ context.Context, _ string) (statemgr.Full, error) {
	if b.state == nil {
		b.state = &lockRecordingState{
			Filesystem: statemgr.NewFilesystem(b.statePath, encryption.StateEncryptionDisabled()),
		}
	}
	return b.state, nil
}

func (b *lockRecordingBackend) ConfigSchema() *configschema.Block {
	return &configschema.Block{}
}

func (b *lockRecordingBackend) PrepareConfig(in cty.Value) (cty.Value, tfdiags.Diagnostics) {
	return in, nil
}

func (b *lockRecordingBackend) Configure(context.Context, cty.Value) tfdiags.Diagnostics {
	return nil
}

func (b *lockRecordingBackend) DeleteWorkspace(_ context.Context, name string, force bool) error {
	return fmt.Errorf("unimplemented")
}

func (b *lockRecordingBackend) Workspaces(context.Context) ([]string, error) {
	return []string{"default"}, nil
}

func (b *lockRecordingBackend) counts() (locks, unlocks int) {
	if b.state == nil {
		return 0, 0
	}
	b.state.lockMu.Lock()
	defer b.state.lockMu.Unlock()
	return b.state.lockCalls, b.state.unlockCalls
}

// unlockResults reports whether the Unlock context was already cancelled and
// whether the delegated Filesystem.Unlock succeeded, guarded by the same
// mutex as the call counts.
func (b *lockRecordingBackend) unlockResults() (ctxCancelled bool, err error) {
	if b.state == nil {
		return false, nil
	}
	b.state.lockMu.Lock()
	defer b.state.lockMu.Unlock()
	return b.state.unlockCtxCancelled, b.state.unlockErr
}

type lockRecordingState struct {
	*statemgr.Filesystem

	lockMu             sync.Mutex
	lockCalls          int
	unlockCalls        int
	unlockCtxCancelled bool
	unlockErr          error
}

var _ statemgr.Full = (*lockRecordingState)(nil)

func (s *lockRecordingState) Lock(ctx context.Context, info *statemgr.LockInfo) (string, error) {
	s.lockMu.Lock()
	s.lockCalls++
	s.lockMu.Unlock()
	return s.Filesystem.Lock(ctx, info)
}

func (s *lockRecordingState) Unlock(ctx context.Context, lockID string) error {
	s.lockMu.Lock()
	s.unlockCalls++
	ctxCancelled := ctx.Err() != nil
	err := s.Filesystem.Unlock(ctx, lockID)
	s.unlockCtxCancelled = ctxCancelled
	s.unlockErr = err
	s.lockMu.Unlock()
	return err
}

// blockedOnPromptUIInput is a ghoten.UIInput mock that signals a channel once
// a prompt is entered, then blocks until either its context is cancelled or a
// generous failsafe timeout expires.
type blockedOnPromptUIInput struct {
	entered chan struct{}
	after   time.Duration
	once    sync.Once
}

func (m *blockedOnPromptUIInput) Input(ctx context.Context, _ *ghoten.InputOpts) (string, error) {
	m.once.Do(func() { close(m.entered) })
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(m.after):
		return "", fmt.Errorf("ui input was not closed within %s", m.after)
	}
}

// newBlockingUIInput returns a UIInput mock and the channel closed when its
// first prompt is entered (proving the prompt is actually blocked).
func newBlockingUIInput(t *testing.T, after time.Duration) (*blockedOnPromptUIInput, <-chan struct{}) {
	t.Helper()
	entered := make(chan struct{})
	return &blockedOnPromptUIInput{entered: entered, after: after}, entered
}

// waitForPromptEntry proves that an interactive prompt is actually blocked
// before the test asks the backend to stop, so cancellation timing is
// deterministic rather than sleep-based.
func waitForPromptEntry(t *testing.T, entered <-chan struct{}) {
	t.Helper()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the operation never reached an interactive prompt")
	}
}

func awaitStoppedOperation(t *testing.T, run *backend.RunningOperation) {
	t.Helper()
	go run.Stop()
	select {
	case <-run.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("hit the timeout. expected for the operation to finish before the timeout")
	}
	if run.Result != backend.OperationFailure {
		t.Fatal("operation succeeded but expected to fail")
	}
}

func TestLocalRun_cancelledVariablePrompt(t *testing.T) {
	b := TestLocal(t)

	// testOperationApply supplies no UIIn, so the variable prompt is exercised
	// by this test's own real blocking input rather than by upstream's test.
	op, done := testOperationApply(t, "./testdata/apply-with-vars")
	in, entered := newBlockingUIInput(t, 10*time.Second)
	op.UIIn = in
	b.OpInput = true

	run, err := b.Operation(context.Background(), op)
	if err != nil {
		t.Fatalf("bad: %s", err)
	}

	waitForPromptEntry(t, entered)
	awaitStoppedOperation(t, run)

	expectedErrHeader := "Error: No value for required variable"
	if errOutput := done(t).Stderr(); !strings.Contains(errOutput, expectedErrHeader) {
		t.Fatalf("unexpected error output. Expected to contain %q but it does not:\n%s", expectedErrHeader, errOutput)
	}
}

func TestLocalRun_cancelledProviderPrompt(t *testing.T) {
	b := TestLocal(t)

	p := TestLocalProvider(t, b, "test", providerSchemaRequiredAttribute())
	p.ApplyResourceChangeResponse = &providers.ApplyResourceChangeResponse{NewState: cty.ObjectVal(map[string]cty.Value{
		"id":  cty.StringVal("yes"),
		"ami": cty.StringVal("bar"),
	})}

	op, done := testOperationApply(t, "./testdata/apply-with-required-provider-configs")
	op.Variables = map[string]backend.UnparsedVariableValue{}
	in, entered := newBlockingUIInput(t, 10*time.Second)
	op.UIIn = in
	b.OpInput = true

	run, err := b.Operation(context.Background(), op)
	if err != nil {
		t.Fatalf("bad: %s", err)
	}

	waitForPromptEntry(t, entered)
	awaitStoppedOperation(t, run)

	expectedErrHeader := "Error: execution halted"
	if errOutput := done(t).Stderr(); !strings.Contains(errOutput, expectedErrHeader) {
		t.Fatalf("unexpected error output. Expected to contain %q but it does not:\n%s", expectedErrHeader, errOutput)
	}
}

func TestLocalRun_cancelledPromptReleasesStateLock(t *testing.T) {
	b := TestLocal(t)

	rec := newLockRecordingBackend(t)
	b.Backend = rec

	op, done := testOperationApply(t, "./testdata/apply-with-vars")
	in, entered := newBlockingUIInput(t, 10*time.Second)
	op.UIIn = in
	b.OpInput = true

	// Replace the noop locker with a real one backed by the recording
	// state manager, so we can prove the lock was actually taken and
	// then released after the cancelled prompt failed the operation.
	streams, _ := terminal.StreamsForTesting(t)
	view := views.NewView(streams)
	op.StateLocker = clistate.NewLocker(0, views.NewStateLocker(arguments.ViewOptions{ViewType: arguments.ViewHuman}, view))

	run, err := b.Operation(context.Background(), op)
	if err != nil {
		t.Fatalf("bad: %s", err)
	}

	waitForPromptEntry(t, entered)
	if locks, unlocks := rec.counts(); locks != 1 || unlocks != 0 {
		t.Fatalf("expected the state lock to be held while prompting, got %d locks and %d unlocks", locks, unlocks)
	}
	awaitStoppedOperation(t, run)
	_ = done(t)

	locks, unlocks := rec.counts()
	if locks != 1 {
		t.Fatalf("expected exactly 1 lock call, got %d", locks)
	}
	if unlocks != 1 {
		t.Fatalf("expected exactly 1 unlock call after cancellation, got %d", unlocks)
	}
	unlockCtxCancelled, unlockErr := rec.unlockResults()
	if unlockCtxCancelled {
		t.Fatal("expected Unlock to be called with a non-cancelled context, but the context was already cancelled")
	}
	if unlockErr != nil {
		t.Fatalf("expected the delegated Filesystem.Unlock to succeed, got error: %s", unlockErr)
	}

	// Reacquire and release the same real filesystem state lock to prove
	// the backend's lock was actually released and the lock is available.
	lockID, err := rec.state.Filesystem.Lock(t.Context(), statemgr.NewLockInfo())
	if err != nil {
		t.Fatalf("expected to be able to reacquire the state lock, got error: %s", err)
	}
	if err := rec.state.Filesystem.Unlock(t.Context(), lockID); err != nil {
		t.Fatalf("expected to be able to release the reacquired state lock, got error: %s", err)
	}
}
