// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package views

import (
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/vmvarela/ghoten/internal/addrs"
	"github.com/vmvarela/ghoten/internal/ghoten"
	"github.com/vmvarela/ghoten/internal/plans"
	"github.com/vmvarela/ghoten/internal/states"
	"github.com/vmvarela/ghoten/internal/terminal"
	"github.com/zclconf/go-cty/cty"
)

// Test a sequence of hooks associated with creating a resource
func TestJSONHook_create(t *testing.T) {
	streams, done := terminal.StreamsForTesting(t)
	hook := newJSONHook(NewJSONView(NewView(streams), nil))

	var nowMu sync.Mutex
	now := time.Now()
	hook.timeNow = func() time.Time {
		nowMu.Lock()
		defer nowMu.Unlock()
		return now
	}

	after := make(chan time.Time, 1)
	hook.timeAfter = func(time.Duration) <-chan time.Time { return after }

	addr := addrs.Resource{
		Mode: addrs.ManagedResourceMode,
		Type: "test_instance",
		Name: "boop",
	}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance)
	priorState := cty.NullVal(cty.Object(map[string]cty.Type{
		"id":  cty.String,
		"bar": cty.List(cty.String),
	}))
	plannedNewState := cty.ObjectVal(map[string]cty.Value{
		"id": cty.StringVal("test"),
		"bar": cty.ListVal([]cty.Value{
			cty.StringVal("baz"),
		}),
	})

	elapsedChan := make(chan time.Duration)
	action, err := hook.preApply(addr, plans.Create, priorState, plannedNewState, elapsedChan)
	testHookReturnValues(t, action, err)

	action, err = hook.PreProvisionInstanceStep(addr, "local-exec")
	testHookReturnValues(t, action, err)

	hook.ProvisionOutput(addr, "local-exec", `Executing: ["/bin/sh" "-c" "touch /etc/motd"]`)

	action, err = hook.PostProvisionInstanceStep(addr, "local-exec", nil)
	testHookReturnValues(t, action, err)

	// Travel 10s into the future, notify the progress goroutine, and wait
	// for execution via 'elapsed' progress
	nowMu.Lock()
	now = now.Add(10 * time.Second)
	after <- now
	nowMu.Unlock()
	elapsed := <-elapsedChan
	testDurationEqual(t, 10*time.Second, elapsed)

	// Travel 10s into the future, notify the progress goroutine, and wait
	// for execution via 'elapsed' progress
	nowMu.Lock()
	now = now.Add(10 * time.Second)
	after <- now
	nowMu.Unlock()
	elapsed = <-elapsedChan
	testDurationEqual(t, 20*time.Second, elapsed)

	// Travel 2s into the future. We have arrived!
	nowMu.Lock()
	now = now.Add(2 * time.Second)
	nowMu.Unlock()

	// Capture the heartbeat channel before PostApply removes the entry
	// from the applying map.
	hook.applyingLock.Lock()
	heartbeatDone := hook.applying[addr.String()].heartbeatDone
	hook.applyingLock.Unlock()

	action, err = hook.PostApply(addr, states.CurrentGen, plannedNewState, nil)
	testHookReturnValues(t, action, err)

	// PostApply stops the progress goroutine. Join it boundedly and confirm
	// it closed its producer-owned observer before reading the streams, as
	// tests must neither leak the goroutine nor close the observer.
	testHeartbeatWait(t, heartbeatDone)
	if _, ok := <-elapsedChan; ok {
		t.Error("heartbeat goroutine should have closed its elapsed observer")
	}

	wantResource := map[string]interface{}{
		"addr":             string("test_instance.boop"),
		"implied_provider": string("test"),
		"module":           string(""),
		"resource":         string("test_instance.boop"),
		"resource_key":     nil,
		"resource_name":    string("boop"),
		"resource_type":    string("test_instance"),
	}
	want := []map[string]interface{}{
		{
			"@level":   "info",
			"@message": "test_instance.boop: Creating...",
			"@module":  "ghoten.ui",
			"type":     "apply_start",
			"hook": map[string]interface{}{
				"action":   string("create"),
				"resource": wantResource,
			},
		},
		{
			"@level":   "info",
			"@message": "test_instance.boop: Provisioning with 'local-exec'...",
			"@module":  "ghoten.ui",
			"type":     "provision_start",
			"hook": map[string]interface{}{
				"provisioner": "local-exec",
				"resource":    wantResource,
			},
		},
		{
			"@level":   "info",
			"@message": `test_instance.boop: (local-exec): Executing: ["/bin/sh" "-c" "touch /etc/motd"]`,
			"@module":  "ghoten.ui",
			"type":     "provision_progress",
			"hook": map[string]interface{}{
				"output":      `Executing: ["/bin/sh" "-c" "touch /etc/motd"]`,
				"provisioner": "local-exec",
				"resource":    wantResource,
			},
		},
		{
			"@level":   "info",
			"@message": "test_instance.boop: (local-exec) Provisioning complete",
			"@module":  "ghoten.ui",
			"type":     "provision_complete",
			"hook": map[string]interface{}{
				"provisioner": "local-exec",
				"resource":    wantResource,
			},
		},
		{
			"@level":   "info",
			"@message": "test_instance.boop: Still creating... [10s elapsed]",
			"@module":  "ghoten.ui",
			"type":     "apply_progress",
			"hook": map[string]interface{}{
				"action":          string("create"),
				"elapsed_seconds": float64(10),
				"resource":        wantResource,
			},
		},
		{
			"@level":   "info",
			"@message": "test_instance.boop: Still creating... [20s elapsed]",
			"@module":  "ghoten.ui",
			"type":     "apply_progress",
			"hook": map[string]interface{}{
				"action":          string("create"),
				"elapsed_seconds": float64(20),
				"resource":        wantResource,
			},
		},
		{
			"@level":   "info",
			"@message": "test_instance.boop: Creation complete after 22s [id=test]",
			"@module":  "ghoten.ui",
			"type":     "apply_complete",
			"hook": map[string]interface{}{
				"action":          string("create"),
				"elapsed_seconds": float64(22),
				"id_key":          "id",
				"id_value":        "test",
				"resource":        wantResource,
			},
		},
	}

	testJSONViewOutputEquals(t, done(t).Stdout(), want)
}

func TestJSONHook_errors(t *testing.T) {
	streams, done := terminal.StreamsForTesting(t)
	hook := newJSONHook(NewJSONView(NewView(streams), nil))

	addr := addrs.Resource{
		Mode: addrs.ManagedResourceMode,
		Type: "test_instance",
		Name: "boop",
	}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance)
	priorState := cty.NullVal(cty.Object(map[string]cty.Type{
		"id":  cty.String,
		"bar": cty.List(cty.String),
	}))
	plannedNewState := cty.ObjectVal(map[string]cty.Value{
		"id": cty.StringVal("test"),
		"bar": cty.ListVal([]cty.Value{
			cty.StringVal("baz"),
		}),
	})

	action, err := hook.PreApply(addr, states.CurrentGen, plans.Delete, priorState, plannedNewState)
	testHookReturnValues(t, action, err)

	// Capture the heartbeat channel before PostApply removes the entry from
	// the applying map, so the progress goroutine can be joined explicitly
	// before closing the streams.
	hook.applyingLock.Lock()
	heartbeatDone := hook.applying[addr.String()].heartbeatDone
	hook.applyingLock.Unlock()

	provisionError := fmt.Errorf("provisioner didn't want to")
	action, err = hook.PostProvisionInstanceStep(addr, "local-exec", provisionError)
	testHookReturnValues(t, action, err)

	applyError := fmt.Errorf("provider was sad")
	action, err = hook.PostApply(addr, states.CurrentGen, plannedNewState, applyError)
	testHookReturnValues(t, action, err)

	testHeartbeatWait(t, heartbeatDone)

	wantResource := map[string]interface{}{
		"addr":             string("test_instance.boop"),
		"implied_provider": string("test"),
		"module":           string(""),
		"resource":         string("test_instance.boop"),
		"resource_key":     nil,
		"resource_name":    string("boop"),
		"resource_type":    string("test_instance"),
	}
	want := []map[string]interface{}{
		{
			"@level":   "info",
			"@message": "test_instance.boop: Destroying...",
			"@module":  "ghoten.ui",
			"type":     "apply_start",
			"hook": map[string]interface{}{
				"action":   string("delete"),
				"resource": wantResource,
			},
		},
		{
			"@level":   "info",
			"@message": "test_instance.boop: (local-exec) Provisioning errored",
			"@module":  "ghoten.ui",
			"type":     "provision_errored",
			"hook": map[string]interface{}{
				"provisioner": "local-exec",
				"resource":    wantResource,
			},
		},
		{
			"@level":   "info",
			"@message": "test_instance.boop: Destruction errored after 0s",
			"@module":  "ghoten.ui",
			"type":     "apply_errored",
			"hook": map[string]interface{}{
				"action":          string("delete"),
				"elapsed_seconds": float64(0),
				"resource":        wantResource,
			},
		},
	}

	testJSONViewOutputEquals(t, done(t).Stdout(), want)
}

// TestJSONHook_applyNoOp asserts that a NoOp action emits no apply events.
func TestJSONHook_applyNoOp(t *testing.T) {
	streams, done := terminal.StreamsForTesting(t)
	hook := newJSONHook(NewJSONView(NewView(streams), nil))

	addr := addrs.Resource{
		Mode: addrs.ManagedResourceMode,
		Type: "test_instance",
		Name: "boop",
	}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance)
	priorState := cty.NullVal(cty.Object(map[string]cty.Type{
		"id": cty.String,
	}))
	plannedNewState := cty.ObjectVal(map[string]cty.Value{
		"id": cty.StringVal("test"),
	})

	action, err := hook.PreApply(addr, states.CurrentGen, plans.NoOp, priorState, plannedNewState)
	testHookReturnValues(t, action, err)

	action, err = hook.PostApply(addr, states.CurrentGen, plannedNewState, nil)
	testHookReturnValues(t, action, err)

	// NoOp must emit no hook events at all.
	testJSONViewOutputEquals(t, done(t).Stdout(), []map[string]interface{}{{}})
}

// TestJSONHook_heartbeatWithoutElapsedObserver exercises the production path
// (public PreApply, no elapsed observer) and verifies that heartbeats are
// emitted and the goroutine is reaped deterministically. The unbuffered tick
// channel makes a successful send an explicit rendezvous with the progress
// goroutine, which orders each emitted apply_progress record before the next
// tick is delivered. Under the pre-fix behavior (heartbeat sending to a
// never-read elapsed channel) the second send below times out bounded,
// failing without hanging the suite.
func TestJSONHook_heartbeatWithoutElapsedObserver(t *testing.T) {
	streams, closeStreams := terminal.StreamsForTesting(t)
	hook := newJSONHook(NewJSONView(NewView(streams), nil))

	var nowMu sync.Mutex
	now := time.Now()
	hook.timeNow = func() time.Time {
		nowMu.Lock()
		defer nowMu.Unlock()
		return now
	}

	// Unbuffered: a successful send is a rendezvous with the progress
	// goroutine. The clock is advanced inside the timeAfter mock, which the
	// heartbeat itself calls on every loop iteration in the select, so each
	// 10s advance is ordered before the corresponding elapsed read and the
	// progress values stay deterministic.
	after := make(chan time.Time)
	// Producer-owned side channel counting mock timeAfter calls; each call
	// happens at select entry, so count 2 proves the first record was
	// emitted and count 3 proves the second was.
	timeAfterCalls := make(chan struct{}, 4)
	hook.timeAfter = func(time.Duration) <-chan time.Time {
		nowMu.Lock()
		now = now.Add(10 * time.Second)
		nowMu.Unlock()
		timeAfterCalls <- struct{}{}
		return after
	}

	addr := addrs.Resource{
		Mode: addrs.ManagedResourceMode,
		Type: "test_instance",
		Name: "boop",
	}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance)
	priorState := cty.NullVal(cty.Object(map[string]cty.Type{
		"id":  cty.String,
		"bar": cty.List(cty.String),
	}))
	plannedNewState := cty.ObjectVal(map[string]cty.Value{
		"id": cty.StringVal("test"),
		"bar": cty.ListVal([]cty.Value{
			cty.StringVal("baz"),
		}),
	})

	// Teardown safety net, registered right after the streams: on any early
	// failure it stops the producer via PostApply (if the test never got
	// there), joins it boundedly, and only then closes the output streams.
	// If the producer cannot be joined (pre-fix behavior wedges it on its
	// producer-owned observer) testHeartbeatWait fail-stops the package test
	// process, so no goroutine or pipe is left behind. The flags below mark
	// the work the test body already did so cleanup never does it twice.
	var (
		postApplied   bool
		streamsClosed bool
		heartbeatDone chan struct{}
	)
	t.Cleanup(func() {
		if !postApplied {
			postApplied = true
			action, err := hook.PostApply(addr, states.CurrentGen, plannedNewState, nil)
			testHookReturnValues(t, action, err)
		}
		if heartbeatDone != nil {
			testHeartbeatWait(t, heartbeatDone)
		}
		if !streamsClosed {
			closeStreams(t)
		}
	})

	beforeStart := now
	action, err := hook.PreApply(addr, states.CurrentGen, plans.Create, priorState, plannedNewState)

	// Capture the heartbeat channel before PostApply removes the entry from
	// the applying map. Note: no elapsed-observer assertion here, so the
	// pre-fix behavior runs all the way to the bounded second-tick timeout
	// instead of failing early; the assertion lives after the join below.
	hook.applyingLock.Lock()
	progress := hook.applying[addr.String()]
	hook.applyingLock.Unlock()
	elapsedObserver := progress.elapsed
	heartbeatDone = progress.heartbeatDone
	testHookReturnValues(t, action, err)

	// Two heartbeats: each rendezvous return implies the goroutine consumed
	// the tick, and the second imply the first record was already emitted
	// (select re-entry happens after emission).
	testSendTick(t, after, beforeStart)
	testSendTick(t, after, beforeStart)

	// Wait for the producer's third select entry: the third mock timeAfter
	// call occurs only after the second apply_progress record was emitted.
	for i := 0; i < 3; i++ {
		select {
		case <-timeAfterCalls:
		case <-time.After(testHeartbeatTimeout):
			t.Fatal("heartbeat goroutine did not progress as expected")
		}
	}

	postApplied = true
	action, err = hook.PostApply(addr, states.CurrentGen, plannedNewState, nil)
	testHookReturnValues(t, action, err)

	// Explicitly join the heartbeat goroutine before closing the streams.
	testHeartbeatWait(t, heartbeatDone)

	// Relocated production-path assertion: with the pre-fix behavior the
	// test never reaches this point (it fail-stops on the second tick), so
	// the required bounded behavior is what gets exercised.
	if elapsedObserver != nil {
		t.Fatal("production path must not attach an elapsed observer")
	}

	wantResource := map[string]interface{}{
		"addr":             string("test_instance.boop"),
		"implied_provider": string("test"),
		"module":           string(""),
		"resource":         string("test_instance.boop"),
		"resource_key":     nil,
		"resource_name":    string("boop"),
		"resource_type":    string("test_instance"),
	}
	want := []map[string]interface{}{
		{
			"@level":   "info",
			"@message": "test_instance.boop: Creating...",
			"@module":  "ghoten.ui",
			"type":     "apply_start",
			"hook": map[string]interface{}{
				"action":   string("create"),
				"resource": wantResource,
			},
		},
		{
			"@level":   "info",
			"@message": "test_instance.boop: Still creating... [10s elapsed]",
			"@module":  "ghoten.ui",
			"type":     "apply_progress",
			"hook": map[string]interface{}{
				"action":          string("create"),
				"elapsed_seconds": float64(10),
				"resource":        wantResource,
			},
		},
		{
			"@level":   "info",
			"@message": "test_instance.boop: Still creating... [20s elapsed]",
			"@module":  "ghoten.ui",
			"type":     "apply_progress",
			"hook": map[string]interface{}{
				"action":          string("create"),
				"elapsed_seconds": float64(20),
				"resource":        wantResource,
			},
		},
		{
			"@level":   "info",
			"@message": "test_instance.boop: Creation complete after 30s [id=test]",
			"@module":  "ghoten.ui",
			"type":     "apply_complete",
			"hook": map[string]interface{}{
				"action":          string("create"),
				"elapsed_seconds": float64(30),
				"id_key":          "id",
				"id_value":        "test",
				"resource":        wantResource,
			},
		},
	}

	output := closeStreams(t)
	streamsClosed = true
	testJSONViewOutputEquals(t, output.Stdout(), want)
}

func TestJSONHook_refresh(t *testing.T) {
	streams, done := terminal.StreamsForTesting(t)
	hook := newJSONHook(NewJSONView(NewView(streams), nil))

	addr := addrs.Resource{
		Mode: addrs.DataResourceMode,
		Type: "test_data_source",
		Name: "beep",
	}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance)
	state := cty.ObjectVal(map[string]cty.Value{
		"id": cty.StringVal("honk"),
		"bar": cty.ListVal([]cty.Value{
			cty.StringVal("baz"),
		}),
	})

	action, err := hook.PreRefresh(addr, states.CurrentGen, state)
	testHookReturnValues(t, action, err)

	action, err = hook.PostRefresh(addr, states.CurrentGen, state, state)
	testHookReturnValues(t, action, err)

	wantResource := map[string]interface{}{
		"addr":             string("data.test_data_source.beep"),
		"implied_provider": string("test"),
		"module":           string(""),
		"resource":         string("data.test_data_source.beep"),
		"resource_key":     nil,
		"resource_name":    string("beep"),
		"resource_type":    string("test_data_source"),
	}
	want := []map[string]interface{}{
		{
			"@level":   "info",
			"@message": "data.test_data_source.beep: Refreshing state... [id=honk]",
			"@module":  "ghoten.ui",
			"type":     "refresh_start",
			"hook": map[string]interface{}{
				"resource": wantResource,
				"id_key":   "id",
				"id_value": "honk",
			},
		},
		{
			"@level":   "info",
			"@message": "data.test_data_source.beep: Refresh complete [id=honk]",
			"@module":  "ghoten.ui",
			"type":     "refresh_complete",
			"hook": map[string]interface{}{
				"resource": wantResource,
				"id_key":   "id",
				"id_value": "honk",
			},
		},
	}

	testJSONViewOutputEquals(t, done(t).Stdout(), want)
}

func TestJSONHook_ephemeral(t *testing.T) {
	addr := addrs.Resource{
		Mode: addrs.EphemeralResourceMode,
		Type: "test_instance",
		Name: "foo",
	}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance)

	cases := []struct {
		name  string
		preF  func(hook ghoten.Hook) (ghoten.HookAction, error)
		postF func(hook ghoten.Hook) (ghoten.HookAction, error)
		want  []map[string]interface{}
	}{
		{
			name: "opening",
			preF: func(hook ghoten.Hook) (ghoten.HookAction, error) {
				return hook.PreOpen(addr)
			},
			postF: func(hook ghoten.Hook) (ghoten.HookAction, error) {
				return hook.PostOpen(addr, nil)
			},
			want: []map[string]interface{}{
				{
					"@level":   "info",
					"@message": "ephemeral.test_instance.foo: Opening...",
					"@module":  "ghoten.ui",
					"hook": map[string]any{
						"Msg": "Opening...",
						"resource": map[string]any{
							"addr":             "ephemeral.test_instance.foo",
							"implied_provider": "test",
							"module":           "",
							"resource":         "ephemeral.test_instance.foo",
							"resource_key":     nil,
							"resource_name":    "foo",
							"resource_type":    "test_instance",
						},
					},
					"type": "ephemeral_action_started",
				},
				{
					"@level":   "info",
					"@message": "ephemeral.test_instance.foo: Open complete",
					"@module":  "ghoten.ui",
					"hook": map[string]any{
						"Msg": "Open complete",
						"resource": map[string]any{
							"addr":             "ephemeral.test_instance.foo",
							"implied_provider": "test",
							"module":           "",
							"resource":         "ephemeral.test_instance.foo",
							"resource_key":     nil,
							"resource_name":    "foo",
							"resource_type":    "test_instance",
						},
					},
					"type": "ephemeral_action_complete",
				},
			},
		},
		{
			name: "renewing",
			preF: func(hook ghoten.Hook) (ghoten.HookAction, error) {
				return hook.PreRenew(addr)
			},
			postF: func(hook ghoten.Hook) (ghoten.HookAction, error) {
				return hook.PostRenew(addr, nil)
			},
			want: []map[string]interface{}{
				{
					"@level":   "info",
					"@message": "ephemeral.test_instance.foo: Renewing...",
					"@module":  "ghoten.ui",
					"hook": map[string]any{
						"Msg": "Renewing...",
						"resource": map[string]any{
							"addr":             "ephemeral.test_instance.foo",
							"implied_provider": "test",
							"module":           "",
							"resource":         "ephemeral.test_instance.foo",
							"resource_key":     nil,
							"resource_name":    "foo",
							"resource_type":    "test_instance",
						},
					},
					"type": "ephemeral_action_started",
				},
				{
					"@level":   "info",
					"@message": "ephemeral.test_instance.foo: Renew complete",
					"@module":  "ghoten.ui",
					"hook": map[string]any{
						"Msg": "Renew complete",
						"resource": map[string]any{
							"addr":             "ephemeral.test_instance.foo",
							"implied_provider": "test",
							"module":           "",
							"resource":         "ephemeral.test_instance.foo",
							"resource_key":     nil,
							"resource_name":    "foo",
							"resource_type":    "test_instance",
						},
					},
					"type": "ephemeral_action_complete",
				},
			},
		},
		{
			name: "closing",
			preF: func(hook ghoten.Hook) (ghoten.HookAction, error) {
				return hook.PreClose(addr)
			},
			postF: func(hook ghoten.Hook) (ghoten.HookAction, error) {
				return hook.PostClose(addr, nil)
			},
			want: []map[string]interface{}{
				{
					"@level":   "info",
					"@message": "ephemeral.test_instance.foo: Closing...",
					"@module":  "ghoten.ui",
					"hook": map[string]any{
						"Msg": "Closing...",
						"resource": map[string]any{
							"addr":             "ephemeral.test_instance.foo",
							"implied_provider": "test",
							"module":           "",
							"resource":         "ephemeral.test_instance.foo",
							"resource_key":     nil,
							"resource_name":    "foo",
							"resource_type":    "test_instance",
						},
					},
					"type": "ephemeral_action_started",
				},
				{
					"@level":   "info",
					"@message": "ephemeral.test_instance.foo: Close complete",
					"@module":  "ghoten.ui",
					"hook": map[string]any{
						"Msg": "Close complete",
						"resource": map[string]any{
							"addr":             "ephemeral.test_instance.foo",
							"implied_provider": "test",
							"module":           "",
							"resource":         "ephemeral.test_instance.foo",
							"resource_key":     nil,
							"resource_name":    "foo",
							"resource_type":    "test_instance",
						},
					},
					"type": "ephemeral_action_complete",
				},
			},
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			streams, done := terminal.StreamsForTesting(t)
			h := newJSONHook(NewJSONView(NewView(streams), nil))

			action, err := tt.preF(h)
			if err != nil {
				t.Fatal(err)
			}
			if action != ghoten.HookActionContinue {
				t.Fatalf("Expected hook to continue, given: %#v", action)
			}

			<-time.After(1100 * time.Millisecond)

			// call postF that will stop the waiting for the action
			action, err = tt.postF(h)
			if err != nil {
				t.Fatal(err)
			}
			if action != ghoten.HookActionContinue {
				t.Errorf("Expected hook to continue, given: %#v", action)
			}

			testJSONViewOutputEquals(t, done(t).Stdout(), tt.want)
		})
	}
}

func testHookReturnValues(t *testing.T, action ghoten.HookAction, err error) {
	t.Helper()

	if err != nil {
		t.Fatal(err)
	}
	if action != ghoten.HookActionContinue {
		t.Fatalf("Expected hook to continue, given: %#v", action)
	}
}

func testDurationEqual(t *testing.T, wantedDuration time.Duration, gotDuration time.Duration) {
	t.Helper()

	if !cmp.Equal(wantedDuration, gotDuration) {
		t.Errorf("unexpected time elapsed:%s\n", cmp.Diff(wantedDuration, gotDuration))
	}
}

// testHeartbeatTimeout bounds every wait on the heartbeat goroutine in tests
// so a wedged producer fails the test instead of hanging the whole suite.
const testHeartbeatTimeout = 2 * time.Second

// testHeartbeatWait joins the heartbeat goroutine after its done channel was
// closed (by PostApply). It must be called before closing the output streams.
// If the join cannot be proven within the timeout — e.g. the pre-fix
// heartbeat blocks forever on its producer-owned observer — the test process
// is fail-stopped, matching the approved test-only cleanup pattern in
// internal/command/apply_test.go: the blocked goroutine cannot be reaped, so
// exiting avoids leaking or hanging the rest of the package suite.
func testHeartbeatWait(t *testing.T, heartbeatDone chan struct{}) {
	t.Helper()

	select {
	case <-heartbeatDone:
	case <-time.After(testHeartbeatTimeout):
		fmt.Fprintln(os.Stderr, "fatal test cleanup: heartbeat goroutine did not stop after done close; aborting package test process")
		os.Exit(1)
	}
}

// testSendTick delivers a mocked timeAfter tick to the heartbeat goroutine.
// It must only be used with an unbuffered tick channel, so a successful send
// doubles as a rendezvous proving the goroutine consumed the tick.
func testSendTick(t *testing.T, after chan<- time.Time, now time.Time) {
	t.Helper()

	select {
	case after <- now:
	case <-time.After(testHeartbeatTimeout):
		t.Fatal("heartbeat goroutine did not consume a tick in time")
	}
}
