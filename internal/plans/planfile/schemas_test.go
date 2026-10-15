// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package planfile

import (
	"bytes"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/zclconf/go-cty/cty"

	"github.com/vmvarela/ghoten/internal/addrs"
	"github.com/vmvarela/ghoten/internal/configs/configload"
	"github.com/vmvarela/ghoten/internal/configs/configschema"
	"github.com/vmvarela/ghoten/internal/encryption"
	"github.com/vmvarela/ghoten/internal/plans"
	"github.com/vmvarela/ghoten/internal/providers"
	"github.com/vmvarela/ghoten/internal/states"
	"github.com/vmvarela/ghoten/internal/states/statefile"
	tfversion "github.com/vmvarela/ghoten/version"
)

// exportAll lets cmp descend into the unexported internals of cty.Type.
var exportAll = cmp.Exporter(func(t reflect.Type) bool { return true })

// protoRoundTrip reports differences between schemas before and after a
// trip through the proto representation, ignoring nil-vs-empty maps since
// the conversion always produces empty maps for empty blocks.
var protoRoundTrip = cmp.Options{exportAll, cmpopts.EquateEmpty()}

// testProviderSchemas builds a full provider schema covering managed,
// data and ephemeral resource types, exercising the fields that survive
// the proto round-trip.
func testProviderSchemas() map[addrs.Provider]providers.ProviderSchema {
	managedSchema := providers.Schema{
		Version: 2,
		Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"id":     {Type: cty.String, Computed: true},
				"name":   {Type: cty.String, Optional: true},
				"secret": {Type: cty.String, Optional: true, Sensitive: true},
			},
			BlockTypes: map[string]*configschema.NestedBlock{
				"settings": {
					Block: configschema.Block{
						Attributes: map[string]*configschema.Attribute{
							"key": {Type: cty.String, Required: true},
						},
					},
					Nesting:  configschema.NestingList,
					MinItems: 1,
					MaxItems: 3,
				},
			},
		},
	}
	dataSchema := providers.Schema{
		Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"id": {Type: cty.String, Computed: true},
			},
		},
	}
	ephemeralSchema := providers.Schema{
		Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"token":    {Type: cty.String, Required: true, WriteOnly: true},
				"settings": {NestedType: &configschema.Object{Attributes: map[string]*configschema.Attribute{"key": {Type: cty.String}}, Nesting: configschema.NestingSingle}},
			},
		},
	}

	return map[addrs.Provider]providers.ProviderSchema{
		addrs.NewDefaultProvider("test"): {
			Provider: providers.Schema{
				Block: &configschema.Block{
					Attributes: map[string]*configschema.Attribute{
						"region": {Type: cty.String, Optional: true},
					},
				},
			},
			ResourceTypes:      map[string]providers.Schema{"test_thing": managedSchema},
			DataSources:        map[string]providers.Schema{"test_data": dataSchema},
			EphemeralResources: map[string]providers.Schema{"test_ephemeral": ephemeralSchema},
		},
	}
}

func TestWriteReadSchemasRoundTrip(t *testing.T) {
	schemasIn := testProviderSchemas()

	var buf bytes.Buffer
	if err := writeSchemas(schemasIn, &buf); err != nil {
		t.Fatalf("failed to write schemas: %s", err)
	}

	schemasOut, err := readSchemas(&buf)
	if err != nil {
		t.Fatalf("failed to read schemas: %s", err)
	}

	if diff := cmp.Diff(schemasIn, schemasOut, protoRoundTrip); diff != "" {
		t.Errorf("schemas did not survive round-trip\n%s", diff)
	}
}

func TestTrimSchemas(t *testing.T) {
	testProvider := addrs.NewDefaultProvider("test")
	full := testProviderSchemas()

	// Include a data source in the full schemas that nothing references,
	// to verify it gets trimmed away.
	unused := full[testProvider]
	unused.DataSources["test_unused"] = providers.Schema{
		Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"id": {Type: cty.String, Computed: true},
			},
		},
	}
	full[testProvider] = unused

	makeChangeSrc := func(mode addrs.ResourceMode, typeName string) *plans.ResourceInstanceChangeSrc {
		return &plans.ResourceInstanceChangeSrc{
			Addr: addrs.Resource{
				Mode: mode,
				Type: typeName,
				Name: "a",
			}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance),
			ProviderAddr: addrs.AbsProviderConfig{
				Provider: testProvider,
				Module:   addrs.RootModule,
			},
		}
	}

	plan := &plans.Plan{
		Changes: &plans.Changes{
			Resources: []*plans.ResourceInstanceChangeSrc{
				makeChangeSrc(addrs.ManagedResourceMode, "test_thing"),
				makeChangeSrc(addrs.DataResourceMode, "test_data"),
				makeChangeSrc(addrs.EphemeralResourceMode, "test_ephemeral"),
			},
		},
	}

	want := map[addrs.Provider]providers.ProviderSchema{
		testProvider: testProviderSchemas()[testProvider],
	}
	got := prepareSchemasForWrite(plan, nil, full)
	if diff := cmp.Diff(want, got, protoRoundTrip); diff != "" {
		t.Errorf("trimmed schemas did not match expected\n%s", diff)
	}

	// A plan referencing an entirely unknown provider yields no schemas,
	// so the writer falls back to not embedding anything.
	unknownPlan := &plans.Plan{
		Changes: &plans.Changes{
			Resources: []*plans.ResourceInstanceChangeSrc{
				{
					Addr: addrs.Resource{
						Mode: addrs.ManagedResourceMode,
						Type: "test_thing",
						Name: "a",
					}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance),
					ProviderAddr: addrs.AbsProviderConfig{
						Provider: addrs.NewDefaultProvider("missing"),
						Module:   addrs.RootModule,
					},
				},
			},
		},
	}
	if got := prepareSchemasForWrite(unknownPlan, nil, full); got != nil {
		t.Errorf("expected nil schema set for unknown provider, got %#v", got)
	}
}

func TestCreateWithSchemas(t *testing.T) {
	planIn := &plans.Plan{
		Backend: plans.Backend{
			Type:      "local",
			Config:    plans.DynamicValue([]byte("config placeholder")),
			Workspace: "default",
		},
		Changes: &plans.Changes{
			Resources: []*plans.ResourceInstanceChangeSrc{
				{
					Addr: addrs.Resource{
						Mode: addrs.EphemeralResourceMode,
						Type: "test_ephemeral",
						Name: "a",
					}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance),
					ProviderAddr: addrs.AbsProviderConfig{
						Provider: addrs.NewDefaultProvider("test"),
						Module:   addrs.RootModule,
					},
				},
			},
		},
	}

	planFn := filepath.Join(t.TempDir(), "tfplan")
	stateFileIn := &statefile.File{
		TerraformVersion: tfversion.SemVer,
		Serial:           2,
		Lineage:          "abc123",
		State:            states.NewState(),
		EncryptionStatus: encryption.StatusSatisfied,
	}
	prevStateFileIn := &statefile.File{
		TerraformVersion: tfversion.SemVer,
		Serial:           1,
		Lineage:          "abc123",
		State:            states.NewState(),
		EncryptionStatus: encryption.StatusSatisfied,
	}
	err := Create(planFn, CreateArgs{
		ConfigSnapshot:       configload.NewEmptySnapshot(),
		Plan:                 planIn,
		StateFile:            stateFileIn,
		PreviousRunStateFile: prevStateFileIn,
		Schemas:              testProviderSchemas(),
	}, encryption.PlanEncryptionDisabled())
	if err != nil {
		t.Fatalf("failed to create plan file: %s", err)
	}

	r, err := Open(planFn, encryption.PlanEncryptionDisabled())
	if err != nil {
		t.Fatalf("failed to open plan file: %s", err)
	}

	schemasOut, err := r.ReadSchemas()
	if err != nil {
		t.Fatalf("failed to read schemas: %s", err)
	}

	want := testProviderSchemas()
	// The trimmed result only keeps the referenced ephemeral resource type.
	trimmed := want[addrs.NewDefaultProvider("test")]
	trimmed.ResourceTypes = make(map[string]providers.Schema)
	trimmed.DataSources = make(map[string]providers.Schema)
	want[addrs.NewDefaultProvider("test")] = trimmed

	if diff := cmp.Diff(want, schemasOut, protoRoundTrip); diff != "" {
		t.Errorf("schemas did not survive round-trip\n%s", diff)
	}
}

func TestReadSchemasWithoutSchemasEntry(t *testing.T) {
	planIn := &plans.Plan{
		Backend: plans.Backend{
			Type:      "local",
			Config:    plans.DynamicValue([]byte("config placeholder")),
			Workspace: "default",
		},
		Changes: &plans.Changes{
			Resources: []*plans.ResourceInstanceChangeSrc{},
		},
	}

	planFn := filepath.Join(t.TempDir(), "tfplan")
	stateFileIn := &statefile.File{
		TerraformVersion: tfversion.SemVer,
		Serial:           2,
		Lineage:          "abc123",
		State:            states.NewState(),
		EncryptionStatus: encryption.StatusSatisfied,
	}
	prevStateFileIn := &statefile.File{
		TerraformVersion: tfversion.SemVer,
		Serial:           1,
		Lineage:          "abc123",
		State:            states.NewState(),
		EncryptionStatus: encryption.StatusSatisfied,
	}
	err := Create(planFn, CreateArgs{
		ConfigSnapshot:       configload.NewEmptySnapshot(),
		Plan:                 planIn,
		StateFile:            stateFileIn,
		PreviousRunStateFile: prevStateFileIn,
	}, encryption.PlanEncryptionDisabled())
	if err != nil {
		t.Fatalf("failed to create plan file: %s", err)
	}

	r, err := Open(planFn, encryption.PlanEncryptionDisabled())
	if err != nil {
		t.Fatalf("failed to open plan file: %s", err)
	}

	_, err = r.ReadSchemas()
	if !errors.Is(err, ErrNoSchemas) {
		t.Fatalf("expected ErrNoSchemas, got %v", err)
	}
}
