// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package ghoten

import (
	"bytes"
	"testing"
	"time"

	"github.com/zclconf/go-cty/cty"

	"github.com/vmvarela/ghoten/internal/addrs"
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
		config, addr, providerAddr, addrs.NoKey, []byte("provider-a"), nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	fpB, err := smartRefreshInstanceFingerprint(
		config, addr, providerAddr, addrs.NoKey, []byte("provider-b"), nil,
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
		config, addr, providerAddr, addrs.NoKey, []byte("provider"), []addrs.ConfigResource{depA},
	)
	if err != nil {
		t.Fatal(err)
	}
	fpB, err := smartRefreshInstanceFingerprint(
		config, addr, providerAddr, addrs.NoKey, []byte("provider"), []addrs.ConfigResource{depA, depB},
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
		{name: "stale", last: now.Add(-smartRefreshMaxAge-time.Second).Unix(), want: false},
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

func TestSmartRefreshInstanceFingerprint_RequiresProviderFingerprint(t *testing.T) {
	addr := addrs.RootModuleInstance.ResourceInstance(
		addrs.ManagedResourceMode, "test_instance", "example", addrs.NoKey,
	)
	providerAddr := addrs.AbsProviderConfig{
		Module:   addrs.RootModule,
		Provider: addrs.NewDefaultProvider("test"),
	}
	_, err := smartRefreshInstanceFingerprint(
		cty.EmptyObjectVal, addr, providerAddr, addrs.NoKey, nil, nil,
	)
	if err == nil {
		t.Fatalf("expected missing provider fingerprint to force conservative fallback")
	}
}
