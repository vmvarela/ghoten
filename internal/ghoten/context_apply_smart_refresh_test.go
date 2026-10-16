// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package ghoten

import (
	"bytes"
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/vmvarela/ghoten/internal/addrs"
	"github.com/vmvarela/ghoten/internal/configs"
	"github.com/vmvarela/ghoten/internal/configs/configschema"
	"github.com/vmvarela/ghoten/internal/plans"
	"github.com/vmvarela/ghoten/internal/plugins"
	"github.com/vmvarela/ghoten/internal/providers"
	"github.com/vmvarela/ghoten/internal/states"
	"github.com/zclconf/go-cty/cty"
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

// TestContext2Apply_smartRefreshProviderMetaAlwaysRefreshes verifies that a
// module declaring provider_meta never gets a fingerprint, so the next smart
// plan keeps reading the resource remotely.
func TestContext2Apply_smartRefreshProviderMetaAlwaysRefreshes(t *testing.T) {
	m := testModuleInline(t, map[string]string{
		"main.tf": `
			terraform {
				provider_meta "test" {
					baz = "quux"
				}
			}
			resource "test_instance" "foo" {
				foo = "bar"
			}
		`,
	})

	p := testProvider("test")
	p.PlanResourceChangeFn = testDiffFn
	p.ApplyResourceChangeFn = testApplyFn
	p.GetProviderSchemaResponse.ProviderMeta = providers.Schema{
		Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"baz": {Type: cty.String, Required: true},
			},
		},
	}

	var reads atomic.Int32
	p.ReadResourceFn = func(req providers.ReadResourceRequest) providers.ReadResourceResponse {
		reads.Add(1)
		return providers.ReadResourceResponse{NewState: req.PriorState}
	}

	ctx := testContext2(t, &ContextOpts{
		Plugins: plugins.NewLibrary(map[addrs.Provider]providers.Factory{
			addrs.NewDefaultProvider("test"): testProviderFuncFixed(p),
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
	state, diags := ctx.Apply(context.Background(), plan, m, nil)
	if diags.HasErrors() {
		t.Fatalf("unexpected apply errors: %s", diags.Err())
	}

	// Unchanged configuration: the plan must still refresh because of provider_meta.
	reads.Store(0)
	if _, diags = ctx.Plan(context.Background(), m, state, planOpts); diags.HasErrors() {
		t.Fatalf("unexpected plan errors: %s", diags.Err())
	}
	if got := reads.Load(); got != 1 {
		t.Fatalf("smart plan with provider_meta performed %d ReadResource calls; want 1", got)
	}
}

// TestContext2Apply_smartRefreshTargetAndExcludeDoNotBlessOmittedResources
// verifies that a partial apply leaves omitted resources' metadata untouched,
// so the next full smart plan still refreshes them.
func TestContext2Apply_smartRefreshTargetAndExcludeDoNotBlessOmittedResources(t *testing.T) {
	configFor := func(amiA, amiB string) *configs.Config {
		return testModuleInline(t, map[string]string{
			"main.tf": fmt.Sprintf(`
				resource "aws_instance" "a" {
					ami = %q
				}
				resource "aws_instance" "b" {
					ami = %q
				}
			`, amiA, amiB),
		})
	}
	addrB := mustResourceInstanceAddr("aws_instance.b")

	run := func(t *testing.T, partial PlanOpts) {
		p := testProvider("aws")
		p.PlanResourceChangeFn = testDiffFn
		p.ApplyResourceChangeFn = testApplyFn

		var reads atomic.Int32
		var readAMIs []string
		p.ReadResourceFn = func(req providers.ReadResourceRequest) providers.ReadResourceResponse {
			reads.Add(1)
			readAMIs = append(readAMIs, req.PriorState.GetAttr("ami").AsString())
			return providers.ReadResourceResponse{NewState: req.PriorState}
		}

		ctx := testContext2(t, &ContextOpts{
			Plugins: plugins.NewLibrary(map[addrs.Provider]providers.Factory{
				addrs.NewDefaultProvider("aws"): testProviderFuncFixed(p),
			}, nil),
		})
		smart := &PlanOpts{Mode: plans.NormalMode, RefreshMode: plans.RefreshSmart}

		// Full apply seeds metadata for both resources.
		plan, diags := ctx.Plan(context.Background(), configFor("ami-1", "ami-1"), states.NewState(), smart)
		if diags.HasErrors() {
			t.Fatalf("unexpected plan errors: %s", diags.Err())
		}
		state, diags := ctx.Apply(context.Background(), plan, configFor("ami-1", "ami-1"), nil)
		if diags.HasErrors() {
			t.Fatalf("unexpected apply errors: %s", diags.Err())
		}
		before := state.RootModule().ResourceInstance(addrB.Resource).Current.RefreshFingerprint

		// Partial plan and apply change both resources but only touch the selected one.
		partial.Mode = plans.NormalMode
		partial.RefreshMode = plans.RefreshSmart
		plan, diags = ctx.Plan(context.Background(), configFor("ami-2", "ami-2"), state, &partial)
		if diags.HasErrors() {
			t.Fatalf("unexpected partial plan errors: %s", diags.Err())
		}
		state, diags = ctx.Apply(context.Background(), plan, configFor("ami-2", "ami-2"), nil)
		if diags.HasErrors() {
			t.Fatalf("unexpected partial apply errors: %s", diags.Err())
		}
		if got := state.RootModule().ResourceInstance(addrB.Resource).Current.RefreshFingerprint; !bytes.Equal(got, before) {
			t.Fatal("partial apply rewrote metadata of an omitted resource")
		}

		// Full smart plan: only the omitted resource b may be read; a is still fresh.
		reads.Store(0)
		readAMIs = nil
		if _, diags = ctx.Plan(context.Background(), configFor("ami-2", "ami-2"), state, smart); diags.HasErrors() {
			t.Fatalf("unexpected full plan errors: %s", diags.Err())
		}
		if got := reads.Load(); got != 1 || readAMIs[0] != "ami-1" {
			t.Fatalf("full smart plan read %d resources (prior ami %v); want only omitted b with prior ami ami-1", got, readAMIs)
		}
	}

	t.Run("target", func(t *testing.T) {
		run(t, PlanOpts{Targets: []addrs.Targetable{mustResourceInstanceAddr("aws_instance.a").ContainingResource()}})
	})
	t.Run("exclude", func(t *testing.T) {
		run(t, PlanOpts{Excludes: []addrs.Targetable{mustResourceInstanceAddr("aws_instance.b").ContainingResource()}})
	})
}

// TestContext2Apply_smartRefreshForEachValueChangeRefreshesOnlyThatInstance
// verifies that changing one for_each value (same HCL source) refreshes only
// the affected instance.
func TestContext2Apply_smartRefreshForEachValueChangeRefreshesOnlyThatInstance(t *testing.T) {
	m := testModuleInline(t, map[string]string{
		"main.tf": `
			variable "m" {
				type = map(string)
			}
			resource "aws_instance" "foo" {
				for_each = var.m
				ami      = each.value
			}
		`,
	})
	vars := func(x, y string) InputValues {
		return InputValues{
			"m": &InputValue{Value: cty.MapVal(map[string]cty.Value{
				"x": cty.StringVal(x),
				"y": cty.StringVal(y),
			}), SourceType: ValueFromCaller},
		}
	}

	p := testProvider("aws")
	p.PlanResourceChangeFn = testDiffFn
	p.ApplyResourceChangeFn = testApplyFn
	var readAMIs []string
	p.ReadResourceFn = func(req providers.ReadResourceRequest) providers.ReadResourceResponse {
		readAMIs = append(readAMIs, req.PriorState.GetAttr("ami").AsString())
		return providers.ReadResourceResponse{NewState: req.PriorState}
	}

	ctx := testContext2(t, &ContextOpts{
		Plugins: plugins.NewLibrary(map[addrs.Provider]providers.Factory{
			addrs.NewDefaultProvider("aws"): testProviderFuncFixed(p),
		}, nil),
	})
	smart := func(v InputValues) *PlanOpts {
		return &PlanOpts{Mode: plans.NormalMode, RefreshMode: plans.RefreshSmart, SetVariables: v}
	}

	plan, diags := ctx.Plan(context.Background(), m, states.NewState(), smart(vars("ami-1", "ami-3")))
	if diags.HasErrors() {
		t.Fatalf("unexpected plan errors: %s", diags.Err())
	}
	state, diags := ctx.Apply(context.Background(), plan, m, nil)
	if diags.HasErrors() {
		t.Fatalf("unexpected apply errors: %s", diags.Err())
	}

	// Only x changes; its source line is identical, so only its value differs.
	readAMIs = nil
	if _, diags = ctx.Plan(context.Background(), m, state, smart(vars("ami-2", "ami-3"))); diags.HasErrors() {
		t.Fatalf("unexpected plan errors: %s", diags.Err())
	}
	if len(readAMIs) != 1 || readAMIs[0] != "ami-1" {
		t.Fatalf("for_each value change read prior amis %v; want only the changed instance x (prior ami-1)", readAMIs)
	}
}
