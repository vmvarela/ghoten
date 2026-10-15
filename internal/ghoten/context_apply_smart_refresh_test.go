// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package ghoten

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/vmvarela/ghoten/internal/addrs"
	"github.com/vmvarela/ghoten/internal/plans"
	"github.com/vmvarela/ghoten/internal/plugins"
	"github.com/vmvarela/ghoten/internal/providers"
	"github.com/vmvarela/ghoten/internal/states"
)

// TestContext2Apply_smartRefreshPersistsMetadata verifies that a successful
// apply stores Smart Refresh metadata on the resource instance, so that the
// next smart plan can skip the remote read for it.
func TestContext2Apply_smartRefreshPersistsMetadata(t *testing.T) {
	m := testModuleInline(t, map[string]string{
		"main.tf": `
			locals { ami = "ami-1" }
			resource "aws_instance" "foo" {
				ami = local.ami
			}
		`,
	})

	p := testProvider("aws")
	p.PlanResourceChangeFn = testDiffFn
	p.ApplyResourceChangeFn = testApplyFn

	var reads atomic.Int32
	p.ReadResourceFn = func(req providers.ReadResourceRequest) providers.ReadResourceResponse {
		reads.Add(1)
		return providers.ReadResourceResponse{NewState: req.PriorState}
	}

	ctx := testContext2(t, &ContextOpts{
		Plugins: plugins.NewLibrary(map[addrs.Provider]providers.Factory{
			addrs.NewDefaultProvider("aws"): testProviderFuncFixed(p),
		}, nil),
	})

	planOpts := &PlanOpts{
		Mode:        plans.NormalMode,
		RefreshMode: plans.RefreshSmart,
	}

	plan, diags := ctx.Plan(context.Background(), m, states.NewState(), planOpts)
	if diags.HasErrors() {
		t.Fatalf("unexpected plan errors: %s", diags.Err())
	}

	// This apply is the step under test: it must persist the evaluated
	// fingerprint and its timestamp on the new state.
	state, diags := ctx.Apply(context.Background(), plan, m, nil)
	if diags.HasErrors() {
		t.Fatalf("unexpected apply errors: %s", diags.Err())
	}

	current := state.RootModule().ResourceInstance(mustResourceInstanceAddr("aws_instance.foo").Resource)
	if current == nil || current.Current == nil {
		t.Fatal("resource missing from state after apply")
	}
	if len(current.Current.RefreshFingerprint) == 0 {
		t.Fatal("apply did not persist RefreshFingerprint")
	}
	if current.Current.LastRefreshUnix <= 0 {
		t.Fatalf("apply persisted LastRefreshUnix %d; want > 0", current.Current.LastRefreshUnix)
	}

	// The metadata persisted by apply must let the next smart plan skip the
	// remote read.
	reads.Store(0)
	plan, diags = ctx.Plan(context.Background(), m, state, planOpts)
	if diags.HasErrors() {
		t.Fatalf("unexpected plan errors: %s", diags.Err())
	}
	if got := reads.Load(); got != 0 {
		t.Fatalf("smart plan after apply performed %d ReadResource calls; want 0", got)
	}
	for _, c := range plan.Changes.Resources {
		if c.Action != plans.NoOp {
			t.Fatalf("unexpected %s action for %s; want NoOp", c.Action, c.Addr)
		}
	}
}

// TestContext2Apply_smartRefreshFailedApplyDropsMetadata verifies that a
// failed apply clears the Smart Refresh metadata, so the next smart plan
// falls back to a remote read.
func TestContext2Apply_smartRefreshFailedApplyDropsMetadata(t *testing.T) {
	seeded := testModuleInline(t, map[string]string{
		"main.tf": `
			locals { ami = "ami-1" }
			resource "aws_instance" "foo" {
				ami = local.ami
			}
		`,
	})
	changed := testModuleInline(t, map[string]string{
		"main.tf": `
			locals { ami = "ami-2" }
			resource "aws_instance" "foo" {
				ami = local.ami
			}
		`,
	})

	p := testProvider("aws")
	p.PlanResourceChangeFn = testDiffFn

	failApply := false
	p.ApplyResourceChangeFn = func(req providers.ApplyResourceChangeRequest) (resp providers.ApplyResourceChangeResponse) {
		if failApply {
			resp.Diagnostics = resp.Diagnostics.Append(fmt.Errorf("apply failed"))
			resp.NewState = req.PriorState
			return resp
		}
		return testApplyFn(req)
	}

	var reads atomic.Int32
	p.ReadResourceFn = func(req providers.ReadResourceRequest) providers.ReadResourceResponse {
		reads.Add(1)
		return providers.ReadResourceResponse{NewState: req.PriorState}
	}

	ctx := testContext2(t, &ContextOpts{
		Plugins: plugins.NewLibrary(map[addrs.Provider]providers.Factory{
			addrs.NewDefaultProvider("aws"): testProviderFuncFixed(p),
		}, nil),
	})

	planOpts := &PlanOpts{
		Mode:        plans.NormalMode,
		RefreshMode: plans.RefreshSmart,
	}

	// Seed the metadata through a successful create.
	plan, diags := ctx.Plan(context.Background(), seeded, states.NewState(), planOpts)
	if diags.HasErrors() {
		t.Fatalf("unexpected plan errors: %s", diags.Err())
	}
	state, diags := ctx.Apply(context.Background(), plan, seeded, nil)
	if diags.HasErrors() {
		t.Fatalf("unexpected apply errors: %s", diags.Err())
	}

	// Now an update whose provider call fails.
	failApply = true
	plan, diags = ctx.Plan(context.Background(), changed, state, planOpts)
	if diags.HasErrors() {
		t.Fatalf("unexpected plan errors: %s", diags.Err())
	}
	state, diags = ctx.Apply(context.Background(), plan, changed, nil)
	if !diags.HasErrors() {
		t.Fatal("expected apply errors from provider failure")
	}

	// A failed apply must clear the persisted Smart Refresh metadata so the
	// next smart plan refreshes conservatively.
	res := state.RootModule().ResourceInstance(mustResourceInstanceAddr("aws_instance.foo").Resource)
	if res == nil || res.Current == nil {
		t.Fatal("resource missing from state after failed apply")
	}
	if len(res.Current.RefreshFingerprint) != 0 {
		t.Fatal("failed apply kept RefreshFingerprint")
	}

	reads.Store(0)
	_, diags = ctx.Plan(context.Background(), changed, state, planOpts)
	if diags.HasErrors() {
		t.Fatalf("unexpected plan errors: %s", diags.Err())
	}
	if got := reads.Load(); got != 1 {
		t.Fatalf("smart plan after failed apply performed %d ReadResource calls; want 1", got)
	}
}
