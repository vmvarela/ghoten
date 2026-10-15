// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package ghoten

import (
	"bytes"
	"testing"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"

	"github.com/vmvarela/ghoten/internal/addrs"
	"github.com/vmvarela/ghoten/internal/configs"
	"github.com/vmvarela/ghoten/internal/configs/configschema"
	"github.com/vmvarela/ghoten/internal/lang/marks"
)

func TestSmartRefreshValueFingerprint_DeterministicAndValueSensitive(t *testing.T) {
	a := cty.ObjectVal(map[string]cty.Value{
		"name": cty.StringVal("api"),
		"size": cty.NumberIntVal(1),
	})
	b := cty.ObjectVal(map[string]cty.Value{
		"name": cty.StringVal("api"),
		"size": cty.NumberIntVal(2),
	})

	a1, err := smartRefreshValueFingerprint(a)
	if err != nil {
		t.Fatal(err)
	}
	a2, err := smartRefreshValueFingerprint(a)
	if err != nil {
		t.Fatal(err)
	}
	b1, err := smartRefreshValueFingerprint(b)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(a1, a2) {
		t.Fatalf("same evaluated value produced different fingerprints")
	}
	if bytes.Equal(a1, b1) {
		t.Fatalf("different evaluated values produced the same fingerprint")
	}
}

func TestSmartRefreshInstanceFingerprint_IncludesProviderConfig(t *testing.T) {
	addr := addrs.RootModuleInstance.ResourceInstance(
		addrs.ManagedResourceMode, "test_instance", "example", addrs.NoKey,
	)
	providerAddr := addrs.AbsProviderConfig{
		Module:   addrs.RootModule,
		Provider: addrs.NewDefaultProvider("test"),
	}
	config := cty.ObjectVal(map[string]cty.Value{
		"name": cty.StringVal("example"),
	})

	fpA, err := smartRefreshInstanceFingerprint(
		config, addr, providerAddr, addrs.NoKey, []byte("provider-a"), 1, nil, false, false,
	)
	if err != nil {
		t.Fatal(err)
	}
	fpB, err := smartRefreshInstanceFingerprint(
		config, addr, providerAddr, addrs.NoKey, []byte("provider-b"), 1, nil, false, false,
	)
	if err != nil {
		t.Fatal(err)
	}

	if bytes.Equal(fpA, fpB) {
		t.Fatalf("provider configuration change did not change resource fingerprint")
	}
}

func TestSmartRefreshInstanceFingerprint_IncludesDependencies(t *testing.T) {
	addr := addrs.RootModuleInstance.ResourceInstance(
		addrs.ManagedResourceMode, "test_instance", "example", addrs.NoKey,
	)
	providerAddr := addrs.AbsProviderConfig{
		Module:   addrs.RootModule,
		Provider: addrs.NewDefaultProvider("test"),
	}
	config := cty.ObjectVal(map[string]cty.Value{
		"name": cty.StringVal("example"),
	})
	depA := addrs.RootModule.Resource(addrs.ManagedResourceMode, "test_instance", "a")
	depB := addrs.RootModule.Resource(addrs.ManagedResourceMode, "test_instance", "b")

	fpA, err := smartRefreshInstanceFingerprint(
		config, addr, providerAddr, addrs.NoKey, []byte("provider"), 1, []addrs.ConfigResource{depA}, false, false,
	)
	if err != nil {
		t.Fatal(err)
	}
	fpB, err := smartRefreshInstanceFingerprint(
		config, addr, providerAddr, addrs.NoKey, []byte("provider"), 1, []addrs.ConfigResource{depA, depB}, false, false,
	)
	if err != nil {
		t.Fatal(err)
	}

	if bytes.Equal(fpA, fpB) {
		t.Fatalf("dependency change did not change resource fingerprint")
	}
}

func TestSmartRefreshMetadataFresh(t *testing.T) {
	now := time.Unix(2_000_000_000, 0).UTC()

	tests := []struct {
		name string
		last int64
		want bool
	}{
		{name: "missing", last: 0, want: false},
		{name: "recent", last: now.Add(-time.Hour).Unix(), want: true},
		{name: "boundary", last: now.Add(-smartRefreshMaxAge).Unix(), want: true},
		{name: "stale", last: now.Add(-smartRefreshMaxAge - time.Second).Unix(), want: false},
		{name: "future", last: now.Add(time.Second).Unix(), want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := smartRefreshMetadataFresh(test.last, now); got != test.want {
				t.Fatalf("got %t, want %t", got, test.want)
			}
		})
	}
}

func TestSmartRefreshInstanceFingerprint_IncludesSchemaVersion(t *testing.T) {
	addr := addrs.RootModuleInstance.ResourceInstance(
		addrs.ManagedResourceMode, "test_instance", "example", addrs.NoKey,
	)
	providerAddr := addrs.AbsProviderConfig{
		Module:   addrs.RootModule,
		Provider: addrs.NewDefaultProvider("test"),
	}
	fp1, err := smartRefreshInstanceFingerprint(
		cty.EmptyObjectVal, addr, providerAddr, addrs.NoKey, []byte("provider"), 1, nil, false, false,
	)
	if err != nil {
		t.Fatal(err)
	}
	fp2, err := smartRefreshInstanceFingerprint(
		cty.EmptyObjectVal, addr, providerAddr, addrs.NoKey, []byte("provider"), 2, nil, false, false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(fp1, fp2) {
		t.Fatal("schema version change did not invalidate the resource fingerprint")
	}
}

func TestSmartRefreshSchemaContainsPersistUnsafeValue(t *testing.T) {
	schema := &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"plain":      {Type: cty.String, Optional: true},
			"sensitive":  {Type: cty.String, Optional: true, Sensitive: true},
			"write_only": {Type: cty.String, Optional: true, WriteOnly: true},
		},
	}

	tests := map[string]struct {
		value cty.Value
		want  bool
	}{
		"only plain configured": {
			value: cty.ObjectVal(map[string]cty.Value{
				"plain":      cty.StringVal("ok"),
				"sensitive":  cty.NullVal(cty.String),
				"write_only": cty.NullVal(cty.String),
			}),
			want: false,
		},
		"sensitive configured": {
			value: cty.ObjectVal(map[string]cty.Value{
				"plain":      cty.NullVal(cty.String),
				"sensitive":  cty.StringVal("secret"),
				"write_only": cty.NullVal(cty.String),
			}),
			want: true,
		},
		"write-only configured": {
			value: cty.ObjectVal(map[string]cty.Value{
				"plain":      cty.NullVal(cty.String),
				"sensitive":  cty.NullVal(cty.String),
				"write_only": cty.StringVal("secret"),
			}),
			want: true,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if got := smartRefreshSchemaContainsPersistUnsafeValue(schema, test.value); got != test.want {
				t.Fatalf("got %t, want %t", got, test.want)
			}
		})
	}
}

func TestSmartRefreshValueFingerprint_RejectsSensitiveAndEphemeralValues(t *testing.T) {
	tests := map[string]cty.Value{
		"sensitive": cty.StringVal("secret").Mark(marks.Sensitive),
		"ephemeral": cty.StringVal("temporary").Mark(marks.Ephemeral),
	}
	for name, value := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := smartRefreshValueFingerprint(value); err == nil {
				t.Fatalf("%s value must force a conservative refresh", name)
			}
		})
	}
}

func TestSmartRefreshValueFingerprint_RejectsUnknownValues(t *testing.T) {
	if _, err := smartRefreshValueFingerprint(cty.UnknownVal(cty.String)); err == nil {
		t.Fatal("unknown values must force a conservative refresh")
	}
}

func TestSmartRefreshInstanceFingerprint_RequiresProviderFingerprint(t *testing.T) {
	addr := addrs.RootModuleInstance.ResourceInstance(
		addrs.ManagedResourceMode, "test_instance", "example", addrs.NoKey,
	)
	providerAddr := addrs.AbsProviderConfig{
		Module:   addrs.RootModule,
		Provider: addrs.NewDefaultProvider("test"),
	}
	_, err := smartRefreshInstanceFingerprint(
		cty.EmptyObjectVal, addr, providerAddr, addrs.NoKey, nil, 1, nil, false, false,
	)
	if err == nil {
		t.Fatalf("expected missing provider fingerprint to force conservative fallback")
	}
}

func TestSmartRefreshInstanceFingerprint_IncludesLifecycleFlags(t *testing.T) {
	addr := addrs.RootModuleInstance.ResourceInstance(
		addrs.ManagedResourceMode, "test_instance", "example", addrs.NoKey,
	)
	providerAddr := addrs.AbsProviderConfig{
		Module:   addrs.RootModule,
		Provider: addrs.NewDefaultProvider("test"),
	}
	config := cty.ObjectVal(map[string]cty.Value{
		"name": cty.StringVal("example"),
	})

	base, err := smartRefreshInstanceFingerprint(
		config, addr, providerAddr, addrs.NoKey, []byte("provider"), 1, nil, false, false,
	)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name        string
		cbd         bool
		skipDestroy bool
	}{
		{name: "create_before_destroy", cbd: true},
		{name: "skip_destroy", skipDestroy: true},
		{name: "both", cbd: true, skipDestroy: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			flagged, err := smartRefreshInstanceFingerprint(
				config, addr, providerAddr, addrs.NoKey, []byte("provider"), 1, nil, test.cbd, test.skipDestroy,
			)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Equal(base, flagged) {
				t.Fatalf("lifecycle flag change did not invalidate the resource fingerprint")
			}
		})
	}
}

func TestSmartRefreshSupportsConfig(t *testing.T) {
	tests := map[string]struct {
		config *configs.Resource
		want   bool
	}{
		"nil config":           {config: nil, want: false},
		"data resource":        {config: &configs.Resource{}, want: false},
		"simple managed":       {config: &configs.Resource{Managed: &configs.ManagedResource{}}, want: true},
		"ignore_all_changes":   {config: &configs.Resource{Managed: &configs.ManagedResource{IgnoreAllChanges: true}}, want: false},
		"ignore_changes":       {config: &configs.Resource{Managed: &configs.ManagedResource{IgnoreChanges: []hcl.Traversal{hcl.Traversal{hcl.TraverseRoot{Name: "x"}}}}}, want: false},
		"prevent_destroy":      {config: &configs.Resource{Managed: &configs.ManagedResource{PreventDestroy: hcl.StaticExpr(cty.True, hcl.Range{})}}, want: false},
		"triggers_replacement": {config: &configs.Resource{TriggersReplacement: []hcl.Expression{hcl.StaticExpr(cty.StringVal("x"), hcl.Range{})}}, want: false},
		"preconditions":        {config: &configs.Resource{Managed: &configs.ManagedResource{}, Preconditions: []*configs.CheckRule{{}}}, want: false},
		"postconditions":       {config: &configs.Resource{Managed: &configs.ManagedResource{}, Postconditions: []*configs.CheckRule{{}}}, want: false},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if got := smartRefreshSupportsConfig(test.config); got != test.want {
				t.Fatalf("got %t, want %t", got, test.want)
			}
		})
	}
}
