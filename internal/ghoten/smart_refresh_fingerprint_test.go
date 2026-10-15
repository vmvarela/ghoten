// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package ghoten

import (
	"bytes"
	"testing"
	"time"

	"github.com/zclconf/go-cty/cty"

	"github.com/vmvarela/ghoten/internal/addrs"
	"github.com/vmvarela/ghoten/internal/lang/marks"
	"github.com/vmvarela/ghoten/internal/states"
)

func TestResourceRefreshFingerprintTracksEvaluatedInputs(t *testing.T) {
	providerAddr := addrs.AbsProviderConfig{
		Module:   addrs.RootModule,
		Provider: addrs.NewDefaultProvider("test"),
	}
	resourceAddr := addrs.Resource{
		Mode: addrs.ManagedResourceMode,
		Type: "test_instance",
		Name: "foo",
	}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance)

	providerConfigA := cty.ObjectVal(map[string]cty.Value{"region": cty.StringVal("eu-west-1")})
	providerConfigB := cty.ObjectVal(map[string]cty.Value{"region": cty.StringVal("eu-central-1")})
	providerFingerprintA, ok := providerConfigFingerprint(providerAddr, addrs.NoKey, providerConfigA)
	if !ok {
		t.Fatal("expected provider fingerprint")
	}
	providerFingerprintB, ok := providerConfigFingerprint(providerAddr, addrs.NoKey, providerConfigB)
	if !ok {
		t.Fatal("expected provider fingerprint")
	}

	configA := cty.ObjectVal(map[string]cty.Value{"size": cty.StringVal("small")})
	configB := cty.ObjectVal(map[string]cty.Value{"size": cty.StringVal("large")})
	depA := addrs.ConfigResource{Module: addrs.RootModule, Resource: addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "test_dep", Name: "a"}}
	depB := addrs.ConfigResource{Module: addrs.RootModule, Resource: addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "test_dep", Name: "b"}}

	base, ok := resourceRefreshFingerprint(resourceAddr, configA, providerAddr, addrs.NoKey, providerFingerprintA, 0, []addrs.ConfigResource{depA}, false, false)
	if !ok {
		t.Fatal("expected resource fingerprint")
	}
	same, ok := resourceRefreshFingerprint(resourceAddr, configA, providerAddr, addrs.NoKey, providerFingerprintA, 0, []addrs.ConfigResource{depA}, false, false)
	if !ok || !bytes.Equal(base, same) {
		t.Fatal("fingerprint must be deterministic")
	}

	configChanged, _ := resourceRefreshFingerprint(resourceAddr, configB, providerAddr, addrs.NoKey, providerFingerprintA, 0, []addrs.ConfigResource{depA}, false, false)
	if bytes.Equal(base, configChanged) {
		t.Fatal("evaluated resource configuration changes must change the fingerprint")
	}

	providerChanged, _ := resourceRefreshFingerprint(resourceAddr, configA, providerAddr, addrs.NoKey, providerFingerprintB, 0, []addrs.ConfigResource{depA}, false, false)
	if bytes.Equal(base, providerChanged) {
		t.Fatal("evaluated provider configuration changes must change the fingerprint")
	}

	depsChanged, _ := resourceRefreshFingerprint(resourceAddr, configA, providerAddr, addrs.NoKey, providerFingerprintA, 0, []addrs.ConfigResource{depA, depB}, false, false)
	if bytes.Equal(base, depsChanged) {
		t.Fatal("dependency changes must change the fingerprint")
	}
}

func TestResourceRefreshFingerprintDependencyOrderIndependent(t *testing.T) {
	providerAddr := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("test")}
	resourceAddr := addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "test_instance", Name: "foo"}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance)
	providerFingerprint, _ := providerConfigFingerprint(providerAddr, addrs.NoKey, cty.EmptyObjectVal)
	config := cty.ObjectVal(map[string]cty.Value{"value": cty.StringVal("x")})
	depA := addrs.ConfigResource{Module: addrs.RootModule, Resource: addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "test_dep", Name: "a"}}
	depB := addrs.ConfigResource{Module: addrs.RootModule, Resource: addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "test_dep", Name: "b"}}

	a, _ := resourceRefreshFingerprint(resourceAddr, config, providerAddr, addrs.NoKey, providerFingerprint, 0, []addrs.ConfigResource{depA, depB}, false, false)
	b, _ := resourceRefreshFingerprint(resourceAddr, config, providerAddr, addrs.NoKey, providerFingerprint, 0, []addrs.ConfigResource{depB, depA}, false, false)
	if !bytes.Equal(a, b) {
		t.Fatal("dependency order must not affect the fingerprint")
	}
}

func TestFingerprintRejectsUnknownValues(t *testing.T) {
	if _, ok := fingerprintCtyValue(cty.UnknownVal(cty.String)); ok {
		t.Fatal("unknown values must force conservative refresh")
	}
}

func TestFingerprintRejectsEphemeralValues(t *testing.T) {
	val := cty.ObjectVal(map[string]cty.Value{
		"secret": cty.StringVal("do-not-persist").Mark(marks.Ephemeral),
	})
	if _, ok := fingerprintCtyValue(val); ok {
		t.Fatal("ephemeral values must never be persisted through Smart Refresh fingerprints")
	}
}

func TestFingerprintRejectsSensitiveValues(t *testing.T) {
	val := cty.ObjectVal(map[string]cty.Value{
		"secret": cty.StringVal("do-not-persist").Mark(marks.Sensitive),
	})
	if _, ok := fingerprintCtyValue(val); ok {
		t.Fatal("sensitive values must never be persisted through Smart Refresh fingerprints")
	}
}

func TestSmartRefreshCanSkipHonorsFingerprintAndFreshness(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	fingerprint := []byte{1, 2, 3, 4}
	prior := &states.ResourceInstanceObject{
		RefreshFingerprint: fingerprint,
		LastRefreshTime:    now.Add(-time.Hour).Unix(),
	}
	if !smartRefreshCanSkip(prior, fingerprint, now) {
		t.Fatal("matching fresh fingerprint should be skippable")
	}

	prior.LastRefreshTime = now.Add(-smartRefreshMaxAge - time.Second).Unix()
	if smartRefreshCanSkip(prior, fingerprint, now) {
		t.Fatal("stale fingerprints must force refresh")
	}

	prior.LastRefreshTime = now.Add(-time.Hour).Unix()
	if smartRefreshCanSkip(prior, []byte{9}, now) {
		t.Fatal("fingerprint mismatch must force refresh")
	}

	prior.RefreshFingerprint = nil
	if smartRefreshCanSkip(prior, fingerprint, now) {
		t.Fatal("legacy state without evaluated fingerprint must refresh")
	}
}
