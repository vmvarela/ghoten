// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package ghoten

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/hashicorp/hcl/v2"
	"github.com/vmvarela/ghoten/internal/addrs"
	"github.com/vmvarela/ghoten/internal/configs/configschema"
	"github.com/vmvarela/ghoten/internal/lang/marks"
	"github.com/vmvarela/ghoten/internal/plans"
	"github.com/vmvarela/ghoten/internal/providers"
	"github.com/vmvarela/ghoten/internal/refactoring"
	"github.com/vmvarela/ghoten/internal/states"
	"github.com/vmvarela/ghoten/internal/tfdiags"
	"github.com/zclconf/go-cty/cty"
)

func TestNodePlanDeposedResourceInstanceObject_Execute(t *testing.T) {
	tests := []struct {
		description           string
		nodeAddress           string
		nodeEndpointsToRemove []*refactoring.RemoveStatement
		wantAction            plans.Action
		wantDiags             tfdiags.Diagnostics
	}{
		{
			description:           "no remove blocks",
			nodeAddress:           "test_instance.foo",
			nodeEndpointsToRemove: make([]*refactoring.RemoveStatement, 0),
			wantAction:            plans.Delete,
		},
		{
			description: "remove block is targeting another resource name of same type",
			nodeAddress: "test_instance.foo",
			nodeEndpointsToRemove: []*refactoring.RemoveStatement{
				{
					From: mustConfigResourceAddr("test_instance.bar"),
				},
			},
			wantAction: plans.Delete,
		},
		{
			description: "remove block is targeting a module but current node is from root module",
			nodeAddress: "test_instance.foo",
			nodeEndpointsToRemove: []*refactoring.RemoveStatement{
				{
					From: addrs.Module{"boop"},
				},
			},
			wantAction: plans.Delete,
		},
		{
			description: "remove block is targeting current node",
			nodeAddress: "test_instance.foo",
			nodeEndpointsToRemove: []*refactoring.RemoveStatement{
				{
					From: mustConfigResourceAddr("test_instance.foo"),
				},
			},
			wantAction: plans.Forget,
			wantDiags: tfdiags.Diagnostics{}.Append(&hcl.Diagnostic{
				Severity: hcl.DiagWarning,
				Summary:  "Resource will be removed from the state",
				Detail:   fmt.Sprintf("After this plan is applied, the resource %s will not be managed anymore by Ghoten.\n\nIn case you want to manage the resource again, you will have to import it.", "test_instance.foo"),
			}),
		},
		{
			description: "remove block is targeting current node and required to get it destroyed",
			nodeAddress: "test_instance.foo",
			nodeEndpointsToRemove: []*refactoring.RemoveStatement{
				{
					From:    mustConfigResourceAddr("test_instance.foo"),
					Destroy: true,
				},
			},
			wantAction: plans.Delete,
		},
		{
			description: "remove block is targeting a resource and the current node is an instance of that",
			nodeAddress: "test_instance.foo[1]",
			nodeEndpointsToRemove: []*refactoring.RemoveStatement{
				{
					From: mustConfigResourceAddr("test_instance.foo"),
				},
			},
			wantAction: plans.Forget,
			wantDiags: tfdiags.Diagnostics{}.Append(&hcl.Diagnostic{
				Severity: hcl.DiagWarning,
				Summary:  "Resource will be removed from the state",
				Detail:   fmt.Sprintf("After this plan is applied, the resource %s will not be managed anymore by Ghoten.\n\nIn case you want to manage the resource again, you will have to import it.", "test_instance.foo[1]"),
			}),
		},
		{
			description: "remove block is targeting a resource to be destroyed and the current node is an instance of that",
			nodeAddress: "test_instance.foo[1]",
			nodeEndpointsToRemove: []*refactoring.RemoveStatement{
				{
					From:    mustConfigResourceAddr("test_instance.foo"),
					Destroy: true,
				},
			},
			wantAction: plans.Delete,
		},
		{
			description: "remove block is targeting a resource from a module which is the current node",
			nodeAddress: "module.boop.test_instance.foo",
			nodeEndpointsToRemove: []*refactoring.RemoveStatement{
				{
					From: mustConfigResourceAddr("module.boop.test_instance.foo"),
				},
			},
			wantAction: plans.Forget,
			wantDiags: tfdiags.Diagnostics{}.Append(&hcl.Diagnostic{
				Severity: hcl.DiagWarning,
				Summary:  "Resource will be removed from the state",
				Detail:   fmt.Sprintf("After this plan is applied, the resource %s will not be managed anymore by Ghoten.\n\nIn case you want to manage the resource again, you will have to import it.", "module.boop.test_instance.foo"),
			}),
		},
		{
			description: "remove block is targeting a resource from a module to be destroyed which is the current node",
			nodeAddress: "module.boop.test_instance.foo",
			nodeEndpointsToRemove: []*refactoring.RemoveStatement{
				{
					From:    mustConfigResourceAddr("module.boop.test_instance.foo"),
					Destroy: true,
				},
			},
			wantAction: plans.Delete,
		},
		{
			description: "remove block is targeting a resource from a module to be destroyed which is the current node",
			nodeAddress: "module.boop.test_instance.foo",
			nodeEndpointsToRemove: []*refactoring.RemoveStatement{
				{
					From:    mustConfigResourceAddr("module.boop.test_instance.foo"),
					Destroy: true,
				},
			},
			wantAction: plans.Delete,
		},
		{
			description: "remove block is targeting a resource from a module of which the current node is an instance of",
			nodeAddress: "module.boop[1].test_instance.foo[1]",
			nodeEndpointsToRemove: []*refactoring.RemoveStatement{
				{
					From: mustConfigResourceAddr("module.boop.test_instance.foo"),
				},
			},
			wantAction: plans.Forget,
			wantDiags: tfdiags.Diagnostics{}.Append(&hcl.Diagnostic{
				Severity: hcl.DiagWarning,
				Summary:  "Resource will be removed from the state",
				Detail:   fmt.Sprintf("After this plan is applied, the resource %s will not be managed anymore by Ghoten.\n\nIn case you want to manage the resource again, you will have to import it.", "module.boop[1].test_instance.foo[1]"),
			}),
		},
		{
			description: "remove block is targeting a module and the current node is a resource of that module",
			nodeAddress: "module.boop.test_instance.foo",
			nodeEndpointsToRemove: []*refactoring.RemoveStatement{
				{
					From: addrs.Module{"boop"},
				},
			},
			wantAction: plans.Forget,
			wantDiags: tfdiags.Diagnostics{}.Append(&hcl.Diagnostic{
				Severity: hcl.DiagWarning,
				Summary:  "Resource will be removed from the state",
				Detail:   fmt.Sprintf("After this plan is applied, the resource %s will not be managed anymore by Ghoten.\n\nIn case you want to manage the resource again, you will have to import it.", "module.boop.test_instance.foo"),
			}),
		},
		{
			description: "remove block is targeting a module and the current node is a resource of one of the module instances",
			nodeAddress: "module.boop[1].test_instance.foo",
			nodeEndpointsToRemove: []*refactoring.RemoveStatement{
				{
					From: addrs.Module{"boop"},
				},
			},
			wantAction: plans.Forget,
			wantDiags: tfdiags.Diagnostics{}.Append(&hcl.Diagnostic{
				Severity: hcl.DiagWarning,
				Summary:  "Resource will be removed from the state",
				Detail:   fmt.Sprintf("After this plan is applied, the resource %s will not be managed anymore by Ghoten.\n\nIn case you want to manage the resource again, you will have to import it.", "module.boop[1].test_instance.foo"),
			}),
		},
	}

	for _, test := range tests {
		t.Run(fmt.Sprintf("%s %s", test.wantAction, test.description), func(t *testing.T) {
			deposedKey := states.NewDeposedKey()
			absResource := mustResourceInstanceAddr(test.nodeAddress)

			evalCtx, p := initMockEvalContext(t.Context(), test.nodeAddress, deposedKey)

			node := NodePlanDeposedResourceInstanceObject{
				NodeAbstractResourceInstance: &NodeAbstractResourceInstance{
					Addr: absResource,
					NodeAbstractResource: NodeAbstractResource{
						ResolvedProvider: mustResolvedProviderInRoot("test", p),
					},
				},
				DeposedKey:       deposedKey,
				RemoveStatements: test.nodeEndpointsToRemove,
			}

			gotDiags := node.Execute(t.Context(), evalCtx, walkPlan)
			assertDiags(t, gotDiags, test.wantDiags)

			if !p.UpgradeResourceStateCalled {
				t.Errorf("UpgradeResourceState wasn't called; should've been called to upgrade the previous run's object")
			}
			if !p.ReadResourceCalled {
				t.Errorf("ReadResource wasn't called; should've been called to refresh the deposed object")
			}

			change := evalCtx.Changes().GetResourceInstanceChange(absResource, deposedKey)
			if got, want := change.ChangeSrc.Action, test.wantAction; got != want {
				t.Fatalf("wrong planned action\ngot:  %s\nwant: %s", got, want)
			}
		})
	}
}

func TestNodeDestroyDeposedResourceInstanceObject_Execute(t *testing.T) {
	deposedKey := states.NewDeposedKey()
	absResourceAddr := "test_instance.foo"
	evalCtx, p := initMockEvalContext(t.Context(), absResourceAddr, deposedKey)

	absResource := mustResourceInstanceAddr(absResourceAddr)
	node := NodeDestroyDeposedResourceInstanceObject{
		NodeAbstractResourceInstance: &NodeAbstractResourceInstance{
			Addr: absResource,
			NodeAbstractResource: NodeAbstractResource{
				ResolvedProvider: mustResolvedProviderInRoot("test", p),
			},
		},
		DeposedKey: deposedKey,
	}
	if diags := node.Execute(t.Context(), evalCtx, walkApply); diags.HasErrors() {
		t.Fatalf("unexpected error: %s", diags.Err())
	}

	// Assert on the state actually under the walk, not a freshly allocated
	// empty state like before.
	if is := evalCtx.StateState.ResourceInstance(absResource); is != nil {
		if is.Current != nil {
			t.Fatalf("unexpected current object left in state after destroy")
		}
		if len(is.Deposed) != 0 {
			t.Fatalf("deposed objects left in state after destroy: %v", is.Deposed)
		}
	}
}

func TestNodeDestroyDeposedResourceInstanceObject_WriteResourceInstanceState(t *testing.T) {
	state := states.NewState()
	evalCtx := new(MockEvalContext)
	evalCtx.StateState = state.SyncWrapper()
	evalCtx.PathPath = addrs.RootModuleInstance
	mockProvider := mockProviderWithResourceTypeSchema("aws_instance", &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id": {
				Type:     cty.String,
				Optional: true,
			},
		},
	})
	evalCtx.installProvider(addrs.NewDefaultProvider("aws"), mockProvider)

	obj := &states.ResourceInstanceObject{
		Value: cty.ObjectVal(map[string]cty.Value{
			"id": cty.StringVal("i-abc123"),
		}),
		Status: states.ObjectReady,
	}
	node := &NodeDestroyDeposedResourceInstanceObject{
		NodeAbstractResourceInstance: &NodeAbstractResourceInstance{
			NodeAbstractResource: NodeAbstractResource{
				ResolvedProvider: mustResolvedProviderInRoot("aws", mockProvider),
			},
			Addr: mustResourceInstanceAddr("aws_instance.foo"),
		},
		DeposedKey: states.NewDeposedKey(),
	}
	err := node.writeResourceInstanceState(t.Context(), evalCtx, obj)
	if err != nil {
		t.Fatalf("unexpected error: %s", err.Error())
	}

	checkStateString(t, state, `
aws_instance.foo: (1 deposed)
  ID = <not created>
  provider = provider["registry.opentofu.org/hashicorp/aws"]
  Deposed ID 1 = i-abc123
	`)
}

func TestNodeDestroyDeposedResourceInstanceObject_WriteResourceInstanceState_EphemeralGuard(t *testing.T) {
	writeOnlySchema := &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id": {
				Type:     cty.String,
				Optional: true,
			},
			"password": {
				Type:      cty.String,
				Optional:  true,
				WriteOnly: true,
			},
		},
	}

	newWriteOnlyBase := func() cty.Value {
		return cty.ObjectVal(map[string]cty.Value{
			"id":       cty.StringVal("i-abc123"),
			"password": cty.NullVal(cty.String),
		})
	}

	seed := &states.ResourceInstanceObjectSrc{
		Status:    states.ObjectReady,
		AttrsJSON: []byte(`{"id":"keep"}`),
	}
	providerCfg := mustProviderConfig(`provider["registry.opentofu.org/hashicorp/test"]`)

	tests := map[string]struct {
		val      cty.Value
		wantErr  bool
		wantMark bool
	}{
		"root ephemeral mark rejected": {
			val:     cty.ObjectVal(map[string]cty.Value{"id": cty.StringVal("i-abc123")}).Mark(marks.Ephemeral),
			wantErr: true,
		},
		"nested ephemeral mark rejected": {
			val: cty.ObjectVal(map[string]cty.Value{
				"id": cty.StringVal("i-abc123").Mark(marks.Ephemeral),
			}),
			wantErr: true,
		},
		"correctly nullified write-only ephemeral accepted": {
			val: newWriteOnlyBase().MarkWithPaths([]cty.PathValueMarks{{
				Path:  cty.Path{cty.GetAttrStep{Name: "password"}},
				Marks: cty.NewValueMarks(marks.Ephemeral),
			}}),
			wantMark: false,
		},
		"nullified write-only ephemeral accepted but simultaneous leak elsewhere rejected": {
			val: newWriteOnlyBase().MarkWithPaths([]cty.PathValueMarks{
				{
					Path:  cty.Path{cty.GetAttrStep{Name: "password"}},
					Marks: cty.NewValueMarks(marks.Ephemeral),
				},
				{
					Path:  cty.Path{cty.GetAttrStep{Name: "id"}},
					Marks: cty.NewValueMarks(marks.Ephemeral),
				},
			}),
			wantErr: true,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			state := states.NewState()
			evalCtx := new(MockEvalContext)
			evalCtx.StateState = state.SyncWrapper()
			evalCtx.PathPath = addrs.RootModuleInstance
			p := mockProviderWithResourceTypeSchema("test_instance", writeOnlySchema)
			evalCtx.installProvider(addrs.NewDefaultProvider("test"), p)

			addr := mustResourceInstanceAddr("test_instance.foo")
			deposedKey := states.NewDeposedKey()
			state.Module(addr.Module).SetResourceInstanceDeposed(addr.Resource, deposedKey, seed, providerCfg, addrs.NoKey)

			node := &NodeDestroyDeposedResourceInstanceObject{
				NodeAbstractResourceInstance: &NodeAbstractResourceInstance{
					Addr: addr,
					NodeAbstractResource: NodeAbstractResource{
						ResolvedProvider: mustResolvedProviderInRoot("test", p),
					},
				},
				DeposedKey: deposedKey,
			}
			err := node.writeResourceInstanceState(t.Context(), evalCtx, &states.ResourceInstanceObject{
				Value:  test.val,
				Status: states.ObjectReady,
			})
			if test.wantErr != (err != nil) {
				t.Fatalf("expected error %t, got error: %v", test.wantErr, err)
			}

			// A rejected write must not replace the preseeded deposed object.
			is := state.ResourceInstance(addr)
			if is == nil {
				t.Fatalf("state lost the resource entirely")
			}
			got := is.Deposed[deposedKey]
			if test.wantErr {
				if diff := cmp.Diff(seed, got); diff != "" {
					t.Errorf("rejected write replaced the preseeded deposed object unexpectedly (-want +got):\n%s", diff)
				}
				return
			}
			if got == nil || got == seed {
				t.Fatalf("expected the write to replace the preseeded deposed object, but it did not")
			}
			for _, pvm := range got.TransientPathValueMarks {
				if _, ok := pvm.Marks[marks.Ephemeral]; ok {
					t.Errorf("ephemeral mark leaked into written deposed state: %v", pvm.Path)
				}
			}
		})
	}
}

func TestNodeDestroyDeposedResourceInstanceObject_Execute_WriteErrorsExposeAndRetainObject(t *testing.T) {
	idObjType := cty.Object(map[string]cty.Type{"id": cty.String})

	buildContext := func(deposedKey states.DeposedKey, srcSeed func(*states.ResourceInstanceObjectSrc), upgradedVal cty.Value, applyFn func(providers.ApplyResourceChangeRequest) providers.ApplyResourceChangeResponse) (*MockEvalContext, *states.State, *MockProvider) {
		absResource := mustResourceInstanceAddr("test_instance.foo")
		src := &states.ResourceInstanceObjectSrc{
			Status:    states.ObjectTainted,
			AttrsJSON: []byte(`{"id":"bar"}`),
		}
		if srcSeed != nil {
			srcSeed(src)
		}
		state := states.NewState()
		state.Module(absResource.Module).SetResourceInstanceDeposed(
			absResource.Resource,
			deposedKey,
			src,
			mustProviderConfig(`provider["registry.opentofu.org/hashicorp/test"]`),
			addrs.NoKey,
		)

		p := testProvider("test")
		p.ConfigureProvider(t.Context(), providers.ConfigureProviderRequest{})
		p.GetProviderSchemaResponse = &providers.GetProviderSchemaResponse{
			ResourceTypes: map[string]providers.Schema{
				"test_instance": {
					Block: &configschema.Block{
						Attributes: map[string]*configschema.Attribute{
							"id": {Type: cty.String, Computed: true},
						},
					},
				},
			},
		}
		p.UpgradeResourceStateResponse = &providers.UpgradeResourceStateResponse{
			UpgradedState: upgradedVal,
		}
		if applyFn != nil {
			p.ApplyResourceChangeFn = applyFn
		}

		evalCtx := &MockEvalContext{
			PrevRunStateState: state.DeepCopy().SyncWrapper(),
			RefreshStateState: state.DeepCopy().SyncWrapper(),
			StateState:        state.SyncWrapper(),
			ChangesChanges:    plans.NewChanges().SyncWrapper(),
		}
		evalCtx.installProvider(addrs.NewDefaultProvider("test"), p)
		return evalCtx, state, p
	}

	newNode := func(p *MockProvider, deposedKey states.DeposedKey) NodeDestroyDeposedResourceInstanceObject {
		return NodeDestroyDeposedResourceInstanceObject{
			NodeAbstractResourceInstance: &NodeAbstractResourceInstance{
				Addr: mustResourceInstanceAddr("test_instance.foo"),
				NodeAbstractResource: NodeAbstractResource{
					ResolvedProvider: mustResolvedProviderInRoot("test", p),
				},
			},
			DeposedKey: deposedKey,
		}
	}

	t.Run("write error alone with clean destroy", func(t *testing.T) {
		deposedKey := states.NewDeposedKey()
		// Null-valued deposed object whose root is marked ephemeral: the
		// destroy plan is a NoOp, destroy itself succeeds cleanly, and only
		// the state write exposes the leaked ephemeral mark.
		wantSrc := &states.ResourceInstanceObjectSrc{
			Status:    states.ObjectTainted,
			AttrsJSON: []byte(`null`),
			TransientPathValueMarks: []cty.PathValueMarks{{
				Path:  cty.Path{},
				Marks: cty.NewValueMarks(marks.Ephemeral),
			}},
		}
		evalCtx, state, p := buildContext(
			deposedKey,
			func(src *states.ResourceInstanceObjectSrc) {
				src.AttrsJSON = []byte(`null`)
				src.TransientPathValueMarks = wantSrc.TransientPathValueMarks
			},
			cty.NullVal(idObjType),
			nil,
		)

		node := newNode(p, deposedKey)
		diags := node.Execute(t.Context(), evalCtx, walkApply)
		if !diags.HasErrors() {
			t.Fatalf("expected write error to surface, got: %#v", diags)
		}
		if len(diags) != 1 {
			t.Errorf("expected exactly one diagnostic, got:\n%s", diags.Err())
		}
		if msg := diags.Err().Error(); !strings.Contains(msg, "ephemeral marks found at the following paths") {
			t.Errorf("expected the leaked ephemeral write error, got: %s", msg)
		}
		is := state.ResourceInstance(mustResourceInstanceAddr("test_instance.foo"))
		if is == nil {
			t.Fatal("state lost the resource entirely")
		}
		if diff := cmp.Diff(wantSrc, is.Deposed[deposedKey]); diff != "" {
			t.Errorf("failed-destroy retention broken: prior deposed object was replaced (-want +got):\n%s", diff)
		}
	})

	t.Run("write error alongside provider destroy error", func(t *testing.T) {
		deposedKey := states.NewDeposedKey()
		// Marked "id" on a non-null deposed object: destroy fails, so the
		// prior (marked) object is retained and then rejected by the writer.
		wantSrc := &states.ResourceInstanceObjectSrc{
			Status:    states.ObjectTainted,
			AttrsJSON: []byte(`{"id":"bar"}`),
			TransientPathValueMarks: []cty.PathValueMarks{{
				Path:  cty.Path{cty.GetAttrStep{Name: "id"}},
				Marks: cty.NewValueMarks(marks.Ephemeral),
			}},
		}
		evalCtx, state, p := buildContext(
			deposedKey,
			func(src *states.ResourceInstanceObjectSrc) {
				src.TransientPathValueMarks = wantSrc.TransientPathValueMarks
			},
			cty.ObjectVal(map[string]cty.Value{"id": cty.StringVal("bar")}),
			func(req providers.ApplyResourceChangeRequest) providers.ApplyResourceChangeResponse {
				resp := providers.ApplyResourceChangeResponse{NewState: cty.NilVal}
				resp.Diagnostics = resp.Diagnostics.Append(tfdiags.Sourceless(
					tfdiags.Error,
					"Provider failed to destroy",
					"destroy failed",
				))
				return resp
			},
		)

		node := newNode(p, deposedKey)
		diags := node.Execute(t.Context(), evalCtx, walkApply)
		if !diags.HasErrors() {
			t.Fatalf("expected errors, got: %#v", diags)
		}
		if len(diags) != 2 {
			t.Errorf("expected exactly two diagnostics (destroy + write), got %d:\n%s", len(diags), diags.Err())
		}
		msg := diags.Err().Error()
		if !strings.Contains(msg, "Provider failed to destroy") {
			t.Errorf("expected the provider destroy error, got: %s", msg)
		}
		if !strings.Contains(msg, "ephemeral marks found at the following paths") {
			t.Errorf("expected the leaked ephemeral write error, got: %s", msg)
		}
		if idxDestroy := strings.Index(msg, "Provider failed to destroy"); idxDestroy > strings.Index(msg, "ephemeral marks found") {
			t.Errorf("expected the write error after the destroy error, got: %s", msg)
		}
		is := state.ResourceInstance(mustResourceInstanceAddr("test_instance.foo"))
		if is == nil {
			t.Fatal("state lost the resource entirely")
		}
		if diff := cmp.Diff(wantSrc, is.Deposed[deposedKey]); diff != "" {
			t.Errorf("failed-destroy retention broken: prior deposed object was replaced (-want +got):\n%s", diff)
		}
	})

	t.Run("write error still triggers the post-apply hook", func(t *testing.T) {
		// The pre-apply hook starts an apply heartbeat (e.g. in the JSON
		// output hook) that only the post-apply hook can close, so even when
		// the state write fails the post-apply hook must be called.
		deposedKey := states.NewDeposedKey()
		// Same setup as "write error alone with clean destroy": the destroy
		// itself succeeds and only the state write fails.
		evalCtx, _, p := buildContext(
			deposedKey,
			func(src *states.ResourceInstanceObjectSrc) {
				src.AttrsJSON = []byte(`null`)
				src.TransientPathValueMarks = []cty.PathValueMarks{{
					Path:  cty.Path{},
					Marks: cty.NewValueMarks(marks.Ephemeral),
				}}
			},
			cty.NullVal(idObjType),
			nil,
		)
		hook := &testHook{}
		evalCtx.HookHook = hook

		node := newNode(p, deposedKey)
		diags := node.Execute(t.Context(), evalCtx, walkApply)
		if !diags.HasErrors() {
			t.Fatalf("expected write error to surface, got: %#v", diags)
		}

		want := []*testHookCall{
			{Action: "PreApply", InstanceID: "test_instance.foo"},
			{Action: "PostApply", InstanceID: "test_instance.foo"},
		}
		if diff := cmp.Diff(want, hook.Calls); diff != "" {
			t.Errorf("wrong hook calls (-want +got):\n%s", diff)
		}
	})

	t.Run("ordinary failed provider destroy retains unmarked object and retry removes the deposed key", func(t *testing.T) {
		deposedKey := states.NewDeposedKey()
		absResource := mustResourceInstanceAddr("test_instance.foo")
		evalCtx, state, p := buildContext(
			deposedKey,
			nil,
			cty.ObjectVal(map[string]cty.Value{"id": cty.StringVal("bar")}),
			func(req providers.ApplyResourceChangeRequest) providers.ApplyResourceChangeResponse {
				resp := providers.ApplyResourceChangeResponse{NewState: cty.NilVal}
				resp.Diagnostics = resp.Diagnostics.Append(tfdiags.Sourceless(
					tfdiags.Error,
					"Provider failed to destroy",
					"destroy failed",
				))
				return resp
			},
		)
		node := newNode(p, deposedKey)

		// Failed destroy: only the provider error is reported and the
		// unmarked, serializable deposed object is retained for a retry.
		diags := node.Execute(t.Context(), evalCtx, walkApply)
		if !diags.HasErrors() {
			t.Fatalf("expected destroy error, got: %#v", diags)
		}
		if len(diags) != 1 {
			t.Errorf("expected exactly one diagnostic, got %d:\n%s", len(diags), diags.Err())
		}
		if msg := diags.Err().Error(); !strings.Contains(msg, "Provider failed to destroy") {
			t.Errorf("expected the provider destroy error, got: %s", msg)
		}
		retained := state.ResourceInstance(absResource)
		if retained == nil || retained.Deposed[deposedKey] == nil {
			t.Fatalf("failed destroy must retain the deposed object for retry")
		}
		got := retained.Deposed[deposedKey]
		if !bytes.Equal(got.AttrsJSON, []byte(`{"id":"bar"}`)) || got.Status != states.ObjectTainted {
			t.Errorf("retained deposed object was modified unexpectedly: %#v", got)
		}

		// Successful retry removes the actual deposed key and prunes the
		// resource from the state the failed attempt ran against.
		p.ApplyResourceChangeFn = nil
		if retryDiags := node.Execute(t.Context(), evalCtx, walkApply); retryDiags.HasErrors() {
			t.Fatalf("unexpected error from successful retry: %s", retryDiags.Err())
		}
		if retryIs := state.ResourceInstance(absResource); retryIs != nil && len(retryIs.Deposed) != 0 {
			t.Errorf("successful retry must remove the deposed object, got: %v", retryIs.Deposed)
		}
	})
}

func TestNodeDestroyDeposedResourceInstanceObject_ExecuteMissingState(t *testing.T) {
	p := simpleMockProvider()
	evalCtx := &MockEvalContext{
		StateState:     states.NewState().SyncWrapper(),
		ChangesChanges: plans.NewChanges().SyncWrapper(),
	}
	evalCtx.installProvider(addrs.NewDefaultProvider("test"), p)

	node := NodeDestroyDeposedResourceInstanceObject{
		NodeAbstractResourceInstance: &NodeAbstractResourceInstance{
			Addr: mustResourceInstanceAddr("test_object.foo"),
			NodeAbstractResource: NodeAbstractResource{
				ResolvedProvider: mustResolvedProviderInRoot("test", p),
			},
		},
		DeposedKey: states.NewDeposedKey(),
	}
	err := node.Execute(t.Context(), evalCtx, walkApply)

	if err == nil {
		t.Fatal("expected error")
	}
}

func TestNodeForgetDeposedResourceInstanceObject_Execute(t *testing.T) {
	deposedKey := states.NewDeposedKey()
	absResourceAddr := "test_instance.foo"
	evalCtx, p := initMockEvalContext(t.Context(), absResourceAddr, deposedKey)

	absResource := mustResourceInstanceAddr(absResourceAddr)
	node := NodeForgetDeposedResourceInstanceObject{
		NodeAbstractResourceInstance: &NodeAbstractResourceInstance{
			Addr: absResource,
			NodeAbstractResource: NodeAbstractResource{
				ResolvedProvider: mustResolvedProviderInRoot("test", p),
			},
		},
		DeposedKey: deposedKey,
	}
	err := node.Execute(t.Context(), evalCtx, walkApply)

	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if got := evalCtx.StateState.ResourceInstance(absResource); got != nil {
		if _, exists := got.Deposed[deposedKey]; exists {
			t.Fatalf("deposed object %q left in state after forget", deposedKey)
		}
	}
}

func initMockEvalContext(ctx context.Context, resourceAddrs string, deposedKey states.DeposedKey) (*MockEvalContext, *MockProvider) {
	state := states.NewState()
	absResource := mustResourceInstanceAddr(resourceAddrs)

	if !absResource.Module.IsRoot() {
		state.EnsureModule(addrs.RootModuleInstance.Child(absResource.Module[0].Name, absResource.Module[0].InstanceKey))
	}

	state.Module(absResource.Module).SetResourceInstanceDeposed(
		absResource.Resource,
		deposedKey,
		&states.ResourceInstanceObjectSrc{
			Status:    states.ObjectTainted,
			AttrsJSON: []byte(`{"id":"bar"}`),
		},
		mustProviderConfig(`provider["registry.opentofu.org/hashicorp/test"]`),
		addrs.NoKey,
	)

	schema := providers.ProviderSchema{
		ResourceTypes: map[string]providers.Schema{
			"test_instance": {
				Block: &configschema.Block{
					Attributes: map[string]*configschema.Attribute{
						"id": {
							Type:     cty.String,
							Computed: true,
						},
					},
				},
			},
		},
	}

	p := testProvider("test")
	p.ConfigureProvider(ctx, providers.ConfigureProviderRequest{})
	p.GetProviderSchemaResponse = &schema

	p.UpgradeResourceStateResponse = &providers.UpgradeResourceStateResponse{
		UpgradedState: cty.ObjectVal(map[string]cty.Value{
			"id": cty.StringVal("bar"),
		}),
	}
	evalCtx := &MockEvalContext{
		PrevRunStateState: state.DeepCopy().SyncWrapper(),
		RefreshStateState: state.DeepCopy().SyncWrapper(),
		StateState:        state.SyncWrapper(),
		ChangesChanges:    plans.NewChanges().SyncWrapper(),
	}
	evalCtx.installProvider(addrs.NewDefaultProvider("test"), p)

	return evalCtx, p
}

func assertDiags(t *testing.T, got, want tfdiags.Diagnostics) {
	if len(got) != len(want) {
		t.Errorf("invalid number of diags wanted %d but got %d.\nwant:\n\t%s\ngot:\n\t%s", len(want), len(got), want, got)
		return
	}
	for _, gd := range got {
		var found bool
		for _, wd := range want {
			if !gd.Description().Equal(wd.Description()) {
				continue
			}
			if !gd.Source().Equal(wd.Source()) {
				continue
			}
			if gd.Severity() != wd.Severity() {
				continue
			}
			if gd.ExtraInfo() != wd.ExtraInfo() {
				continue
			}
			if gd.FromExpr() != wd.FromExpr() {
				continue
			}
			found = true
		}
		if !found {
			t.Errorf("got a diagnostic that is not expected.\nwanted:\n\t%s\ngot:\n\t%s", want, gd)
		}
	}
}
