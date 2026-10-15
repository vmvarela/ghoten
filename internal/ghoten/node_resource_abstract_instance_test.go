// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package ghoten

import (
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/zclconf/go-cty/cty"

	"github.com/vmvarela/ghoten/internal/addrs"
	"github.com/vmvarela/ghoten/internal/configs"
	"github.com/vmvarela/ghoten/internal/configs/configschema"
	"github.com/vmvarela/ghoten/internal/lang/marks"
	"github.com/vmvarela/ghoten/internal/providers"
	"github.com/vmvarela/ghoten/internal/states"
)

func TestNodeAbstractResourceInstanceProvider(t *testing.T) {
	tests := []struct {
		Addr                 addrs.AbsResourceInstance
		Config               *configs.Resource
		StoredProviderConfig addrs.AbsProviderConfig
		Want                 addrs.Provider
	}{
		{
			Addr: addrs.Resource{
				Mode: addrs.ManagedResourceMode,
				Type: "null_resource",
				Name: "baz",
			}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance),
			Want: addrs.Provider{
				Hostname:  addrs.DefaultProviderRegistryHost,
				Namespace: "hashicorp",
				Type:      "null",
			},
		},
		{
			Addr: addrs.Resource{
				Mode: addrs.DataResourceMode,
				Type: "terraform_remote_state",
				Name: "baz",
			}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance),
			Want: addrs.Provider{
				// As a special case, the type prefix "terraform_" maps to
				// the builtin provider, not the default one.
				Hostname:  addrs.BuiltInProviderHost,
				Namespace: addrs.BuiltInProviderNamespace,
				Type:      "terraform",
			},
		},
		{
			Addr: addrs.Resource{
				Mode: addrs.ManagedResourceMode,
				Type: "null_resource",
				Name: "baz",
			}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance),
			Config: &configs.Resource{
				// Just enough configs.Resource for the Provider method. Not
				// actually valid for general use.
				Provider: addrs.Provider{
					Hostname:  addrs.DefaultProviderRegistryHost,
					Namespace: "awesomecorp",
					Type:      "happycloud",
				},
			},
			// The config overrides the default behavior.
			Want: addrs.Provider{
				Hostname:  addrs.DefaultProviderRegistryHost,
				Namespace: "awesomecorp",
				Type:      "happycloud",
			},
		},
		{
			Addr: addrs.Resource{
				Mode: addrs.DataResourceMode,
				Type: "terraform_remote_state",
				Name: "baz",
			}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance),
			Config: &configs.Resource{
				// Just enough configs.Resource for the Provider method. Not
				// actually valid for general use.
				Provider: addrs.Provider{
					Hostname:  addrs.DefaultProviderRegistryHost,
					Namespace: "awesomecorp",
					Type:      "happycloud",
				},
			},
			// The config overrides the default behavior.
			Want: addrs.Provider{
				Hostname:  addrs.DefaultProviderRegistryHost,
				Namespace: "awesomecorp",
				Type:      "happycloud",
			},
		},
		{
			Addr: addrs.Resource{
				Mode: addrs.DataResourceMode,
				Type: "null_resource",
				Name: "baz",
			}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance),
			Config: nil,
			StoredProviderConfig: addrs.AbsProviderConfig{
				Module: addrs.RootModule,
				Provider: addrs.Provider{
					Hostname:  addrs.DefaultProviderRegistryHost,
					Namespace: "awesomecorp",
					Type:      "null",
				},
			},
			// The stored provider config overrides the default behavior.
			Want: addrs.Provider{
				Hostname:  addrs.DefaultProviderRegistryHost,
				Namespace: "awesomecorp",
				Type:      "null",
			},
		},
	}

	for _, test := range tests {
		var name string
		if test.Config != nil {
			name = fmt.Sprintf("%s with configured %s", test.Addr, test.Config.Provider)
		} else {
			name = fmt.Sprintf("%s with no configuration", test.Addr)
		}
		t.Run(name, func(t *testing.T) {
			node := &NodeAbstractResourceInstance{
				// Just enough NodeAbstractResourceInstance for the Provider
				// function. (This would not be valid for some other functions.)
				Addr: test.Addr,
				NodeAbstractResource: NodeAbstractResource{
					Addr:   test.Addr.ConfigResource(),
					Config: test.Config,
					storedProviderConfig: ResolvedProvider{
						ProviderConfig: test.StoredProviderConfig,
					},
				},
			}
			got := node.Provider()
			if got != test.Want {
				t.Errorf("wrong result\naddr:  %s\nconfig: %#v\ngot:   %s\nwant:  %s", test.Addr, test.Config, got, test.Want)
			}
		})
	}
}

func TestNodeAbstractResourceInstance_WriteResourceInstanceState(t *testing.T) {
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

	obj := &states.ResourceInstanceObject{
		Value: cty.ObjectVal(map[string]cty.Value{
			"id": cty.StringVal("i-abc123"),
		}),
		Status: states.ObjectReady,
	}

	node := &NodeAbstractResourceInstance{
		Addr: mustResourceInstanceAddr("aws_instance.foo"),
		// instanceState:        obj,
		NodeAbstractResource: NodeAbstractResource{
			ResolvedProvider: mustResolvedProviderInRoot("aws", mockProvider),
		},
	}
	evalCtx.installProvider(addrs.NewDefaultProvider("aws"), mockProvider)

	err := node.writeResourceInstanceState(t.Context(), evalCtx, obj, workingState)
	if err != nil {
		t.Fatalf("unexpected error: %s", err.Error())
	}

	checkStateString(t, state, `
aws_instance.foo:
  ID = i-abc123
  provider = provider["registry.opentofu.org/hashicorp/aws"]
	`)
}

// writeResourceInstanceStateGuardContext provides empty mock states for all
// three phase states and a mock provider exposing the given schema for the
// managed, data source and ephemeral "test_instance" types.
func writeResourceInstanceStateGuardContext(schema *configschema.Block) (*MockEvalContext, *MockProvider, map[phaseState]*states.State) {
	working := states.NewState()
	refresh := states.NewState()
	prevRun := states.NewState()
	evalCtx := new(MockEvalContext)
	evalCtx.StateState = working.SyncWrapper()
	evalCtx.RefreshStateState = refresh.SyncWrapper()
	evalCtx.PrevRunStateState = prevRun.SyncWrapper()
	evalCtx.PathPath = addrs.RootModuleInstance

	p := mockProviderWithResourceTypeSchema("test_instance", schema)
	p.GetProviderSchemaResponse.DataSources = map[string]providers.Schema{
		"test_instance": {Block: schema},
	}
	p.GetProviderSchemaResponse.EphemeralResources = map[string]providers.Schema{
		"test_instance": {Block: schema},
	}
	evalCtx.installProvider(addrs.NewDefaultProvider("test"), p)

	return evalCtx, p, map[phaseState]*states.State{
		workingState: working,
		refreshState: refresh,
		prevRunState: prevRun,
	}
}

func TestNodeAbstractResourceInstance_WriteResourceInstanceState_EphemeralGuard(t *testing.T) {
	basicSchema := &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id": {
				Type:     cty.String,
				Optional: true,
			},
		},
	}
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

	baseVal := cty.ObjectVal(map[string]cty.Value{"id": cty.StringVal("i-abc123")})
	writeOnlyBaseVal := cty.ObjectVal(map[string]cty.Value{
		"id":       cty.StringVal("i-abc123"),
		"password": cty.NullVal(cty.String),
	})

	tests := map[string]struct {
		addr     string
		schema   *configschema.Block
		val      cty.Value
		wantErr  bool
		wantInst bool
	}{
		"unmarked managed resource accepted": {
			addr:     "test_instance.foo",
			schema:   basicSchema,
			val:      baseVal,
			wantInst: true,
		},
		"sensitive-only mark accepted": {
			addr:   "test_instance.foo",
			schema: basicSchema,
			val: cty.ObjectVal(map[string]cty.Value{
				"id": cty.StringVal("i-abc123").Mark(marks.Sensitive),
			}),
			wantInst: true,
		},
		"root ephemeral mark rejected": {
			addr:    "test_instance.foo",
			schema:  basicSchema,
			val:     baseVal.Mark(marks.Ephemeral),
			wantErr: true,
		},
		"nested ephemeral mark rejected": {
			addr:   "test_instance.foo",
			schema: basicSchema,
			val: cty.ObjectVal(map[string]cty.Value{
				"id": cty.StringVal("i-abc123").Mark(marks.Ephemeral),
			}),
			wantErr: true,
		},
		"data source root ephemeral mark rejected": {
			addr:    "data.test_instance.foo",
			schema:  basicSchema,
			val:     baseVal.Mark(marks.Ephemeral),
			wantErr: true,
		},
		"data source nested ephemeral mark rejected": {
			addr:   "data.test_instance.foo",
			schema: basicSchema,
			val: cty.ObjectVal(map[string]cty.Value{
				"id": cty.StringVal("i-abc123").Mark(marks.Ephemeral),
			}),
			wantErr: true,
		},
		"genuine ephemeral resource with root mark accepted": {
			addr:     "ephemeral.test_instance.foo",
			schema:   basicSchema,
			val:      baseVal.Mark(marks.Ephemeral),
			wantInst: true,
		},
		"correctly nullified write-only ephemeral accepted": {
			addr:   "test_instance.foo",
			schema: writeOnlySchema,
			val: writeOnlyBaseVal.MarkWithPaths([]cty.PathValueMarks{{
				Path:  cty.Path{cty.GetAttrStep{Name: "password"}},
				Marks: cty.NewValueMarks(marks.Ephemeral),
			}}),
			wantInst: true,
		},
		"nullified write-only ephemeral accepted but simultaneous leak elsewhere rejected": {
			addr:   "test_instance.foo",
			schema: writeOnlySchema,
			val: writeOnlyBaseVal.MarkWithPaths([]cty.PathValueMarks{
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
			evalCtx, p, stateByPhase := writeResourceInstanceStateGuardContext(test.schema)
			state := stateByPhase[workingState]

			addr := mustResourceInstanceAddr(test.addr)
			node := &NodeAbstractResourceInstance{
				Addr: addr,
				NodeAbstractResource: NodeAbstractResource{
					ResolvedProvider: mustResolvedProviderInRoot("test", p),
				},
			}
			obj := &states.ResourceInstanceObject{
				Value:  test.val,
				Status: states.ObjectReady,
			}

			err := node.writeResourceInstanceState(t.Context(), evalCtx, obj, workingState)
			if test.wantErr != (err != nil) {
				t.Fatalf("expected error %t, got error: %v", test.wantErr, err)
			}
			if err != nil {
				return
			}

			is := state.ResourceInstance(addr)
			if is == nil || is.Current == nil {
				if test.wantInst {
					t.Fatalf("expected the instance to be written, but state has no current object for %s", test.addr)
				}
				return
			}
			if !test.wantInst {
				t.Fatalf("expected the rejected write to replace nothing, but the state has a current object for %s", test.addr)
			}
			// Written state must never retain ephemeral marks for managed
			// and data resources; ephemeral resources legitimately do.
			if addr.ContainingResource().Resource.Mode != addrs.EphemeralResourceMode {
				for _, pvm := range is.Current.TransientPathValueMarks {
					if _, ok := pvm.Marks[marks.Ephemeral]; ok {
						t.Errorf("ephemeral mark leaked into written state: %v", pvm.Path)
					}
				}
			}
		})
	}
}

func TestNodeAbstractResourceInstance_WriteResourceInstanceState_RejectedWriteKeepsPreseededState(t *testing.T) {
	schema := &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id": {
				Type:     cty.String,
				Optional: true,
			},
		},
	}

	addr := mustResourceInstanceAddr("test_instance.foo")
	deposedKey := states.NewDeposedKey()
	wantSrc := &states.ResourceInstanceObjectSrc{
		Status:    states.ObjectReady,
		AttrsJSON: []byte(`{"id":"keep"}`),
	}
	providerCfg := mustProviderConfig(`provider["registry.opentofu.org/hashicorp/test"]`)

	// Nested ephemeral leak on the "id" attribute.
	leakedObj := &states.ResourceInstanceObject{
		Value: cty.ObjectVal(map[string]cty.Value{
			"id": cty.StringVal("i-abc123").Mark(marks.Ephemeral),
		}),
		Status: states.ObjectReady,
	}

	for _, route := range []string{"current", "deposed"} {
		for _, phase := range []phaseState{workingState, refreshState, prevRunState} {
			t.Run(fmt.Sprintf("%s route writes to %s", route, phase), func(t *testing.T) {
				seed := func(state *states.State) {
					switch route {
					case "current":
						state.Module(addr.Module).SetResourceInstanceCurrent(addr.Resource, wantSrc, providerCfg, addrs.NoKey)
					case "deposed":
						state.Module(addr.Module).SetResourceInstanceDeposed(addr.Resource, deposedKey, wantSrc, providerCfg, addrs.NoKey)
					}
				}
				working, refresh, prevRun := states.NewState(), states.NewState(), states.NewState()
				seed(working)
				seed(refresh)
				seed(prevRun)
				evalCtx := new(MockEvalContext)
				evalCtx.StateState = working.SyncWrapper()
				evalCtx.RefreshStateState = refresh.SyncWrapper()
				evalCtx.PrevRunStateState = prevRun.SyncWrapper()
				evalCtx.PathPath = addrs.RootModuleInstance
				p := mockProviderWithResourceTypeSchema("test_instance", schema)
				evalCtx.installProvider(addrs.NewDefaultProvider("test"), p)

				node := &NodeAbstractResourceInstance{
					Addr: addr,
					NodeAbstractResource: NodeAbstractResource{
						ResolvedProvider: mustResolvedProviderInRoot("test", p),
					},
				}

				var err error
				if route == "deposed" {
					err = node.writeResourceInstanceStateDeposed(t.Context(), evalCtx, deposedKey, leakedObj, phase)
				} else {
					err = node.writeResourceInstanceState(t.Context(), evalCtx, leakedObj, phase)
				}
				if err == nil {
					t.Fatalf("expected error from writeResourceInstanceState, got nil")
				}

				for name, st := range map[string]*states.State{
					"working": working, "refresh": refresh, "prevRun": prevRun,
				} {
					is := st.ResourceInstance(addr)
					if is == nil {
						t.Fatalf("%s state lost the resource entirely", name)
					}
					var got *states.ResourceInstanceObjectSrc
					if route == "deposed" {
						got = is.Deposed[deposedKey]
					} else {
						got = is.Current
					}
					if diff := cmp.Diff(wantSrc, got); diff != "" {
						t.Errorf("%s state %s object was modified unexpectedly (-want +got):\n%s", name, route, diff)
					}
				}
			})
		}
	}
}

func TestFilterResourceProvisioners(t *testing.T) {
	tests := map[string]struct {
		when               configs.ProvisionerWhen
		cfg                *configs.Resource
		removedBlocksProvs []*configs.Provisioner
		wantProvs          []*configs.Provisioner
	}{
		"config and provisioners nil": {
			when:               configs.ProvisionerWhenDestroy,
			cfg:                nil,
			removedBlocksProvs: nil,
			wantProvs:          []*configs.Provisioner{},
		},
		"config nil and provisioners contains targeted provisioners": {
			when: configs.ProvisionerWhenDestroy,
			cfg:  nil,
			removedBlocksProvs: []*configs.Provisioner{
				{Type: "local-exec", When: configs.ProvisionerWhenDestroy},
				{Type: "local-exec2", When: configs.ProvisionerWhenCreate},
			},
			wantProvs: []*configs.Provisioner{
				{Type: "local-exec", When: configs.ProvisionerWhenDestroy},
			},
		},
		"config.managed nil and provisioners contains no targeted provisioners": {
			when: configs.ProvisionerWhenDestroy,
			cfg:  &configs.Resource{},
			removedBlocksProvs: []*configs.Provisioner{
				{Type: "local-exec", When: configs.ProvisionerWhenCreate},
				{Type: "local-exec2", When: configs.ProvisionerWhenCreate},
			},
			wantProvs: []*configs.Provisioner{},
		},
		// This is expecting an empty result because we want to use the resource defined provisioners when
		// config.managed exists
		"config.managed not nil and provisioners contains targeted provisioners": {
			when: configs.ProvisionerWhenCreate,
			cfg: &configs.Resource{
				Managed: &configs.ManagedResource{},
			},
			removedBlocksProvs: []*configs.Provisioner{
				{Type: "local-exec", When: configs.ProvisionerWhenCreate},
				{Type: "local-exec2", When: configs.ProvisionerWhenCreate},
			},
			wantProvs: nil,
		},
		"config.managed is having provisioners therefore removed blocks provisioners are ignored": {
			when: configs.ProvisionerWhenCreate,
			cfg: &configs.Resource{
				Managed: &configs.ManagedResource{
					Provisioners: []*configs.Provisioner{
						{Type: "local-exec3", When: configs.ProvisionerWhenCreate},
						{Type: "local-exec4", When: configs.ProvisionerWhenCreate},
					},
				},
			},
			removedBlocksProvs: []*configs.Provisioner{
				{Type: "local-exec", When: configs.ProvisionerWhenCreate},
				{Type: "local-exec2", When: configs.ProvisionerWhenCreate},
			},
			wantProvs: []*configs.Provisioner{
				{Type: "local-exec3", When: configs.ProvisionerWhenCreate},
				{Type: "local-exec4", When: configs.ProvisionerWhenCreate},
			},
		},
	}
	for name, test := range tests {
		t.Run(fmt.Sprintf("%s-%s", test.when, name), func(t *testing.T) {
			res := filterResourceProvisioners(test.cfg, test.removedBlocksProvs, test.when)
			if diff := cmp.Diff(test.wantProvs, res); diff != "" {
				t.Errorf("expected provisioners different than what we got:\n%s", diff)
			}
		})
	}
}
