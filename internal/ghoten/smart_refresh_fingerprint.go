// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package ghoten

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"time"

	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"
	ctymsgpack "github.com/zclconf/go-cty/cty/msgpack"

	"github.com/vmvarela/ghoten/internal/addrs"
	"github.com/vmvarela/ghoten/internal/configs/configschema"
	"github.com/vmvarela/ghoten/internal/lang/marks"
)

// smartRefreshMaxAge bounds how long Smart Refresh can trust a matching
// fingerprint without re-reading the remote object. This keeps the fast path
// useful while still guaranteeing periodic drift detection.
const smartRefreshMaxAge = 24 * time.Hour

func smartRefreshPathHasConfiguredValue(v cty.Value, path cty.Path) bool {
	if v == cty.NilVal || !v.IsWhollyKnown() {
		return true
	}
	if len(path) == 0 {
		return !v.IsNull()
	}
	pathVal, err := path.Apply(v)
	if err != nil || pathVal == cty.NilVal || !pathVal.IsWhollyKnown() {
		return true
	}
	return !pathVal.IsNull()
}

func smartRefreshSchemaContainsPersistUnsafeValue(schema *configschema.Block, v cty.Value) bool {
	if schema == nil || v == cty.NilVal {
		return true
	}

	// Schema traversal operates on the shape/value only. Marks from variables
	// and expressions are checked separately by smartRefreshValueFingerprint.
	unmarked, _ := v.UnmarkDeep()

	for _, pvm := range schema.ValueMarks(unmarked, nil) {
		if _, sensitive := pvm.Marks[marks.Sensitive]; sensitive && smartRefreshPathHasConfiguredValue(unmarked, pvm.Path) {
			return true
		}
		if _, ephemeral := pvm.Marks[marks.Ephemeral]; ephemeral && smartRefreshPathHasConfiguredValue(unmarked, pvm.Path) {
			return true
		}
	}

	for _, path := range schema.WriteOnlyPaths(unmarked, nil) {
		if smartRefreshPathHasConfiguredValue(unmarked, path) {
			return true
		}
	}
	return false
}

func smartRefreshValueFingerprint(v cty.Value) ([]byte, error) {
	if v == cty.NilVal {
		return nil, fmt.Errorf("cannot fingerprint cty.NilVal")
	}
	if marks.ContainsAnyMark(v, marks.Sensitive, marks.Ephemeral) {
		return nil, fmt.Errorf("cannot persist a fingerprint derived from sensitive or ephemeral values")
	}

	unmarked, _ := v.UnmarkDeep()
	if !unmarked.IsWhollyKnown() {
		return nil, fmt.Errorf("cannot safely fingerprint an unknown cty value")
	}

	typeSrc, err := ctyjson.MarshalType(unmarked.Type())
	if err != nil {
		return nil, fmt.Errorf("marshal cty type: %w", err)
	}
	valueSrc, err := ctymsgpack.Marshal(unmarked, unmarked.Type())
	if err != nil {
		return nil, fmt.Errorf("marshal cty value: %w", err)
	}

	h := sha256.New()
	_, _ = h.Write(typeSrc)
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(valueSrc)
	return h.Sum(nil), nil
}

func smartRefreshInstanceFingerprint(
	configVal cty.Value,
	addr addrs.AbsResourceInstance,
	providerAddr addrs.AbsProviderConfig,
	providerKey addrs.InstanceKey,
	providerFingerprint []byte,
	schemaVersion uint64,
	dependencies []addrs.ConfigResource,
) ([]byte, error) {
	if len(providerFingerprint) == 0 {
		return nil, fmt.Errorf("provider configuration fingerprint is unavailable")
	}

	configFingerprint, err := smartRefreshValueFingerprint(configVal)
	if err != nil {
		return nil, err
	}

	h := sha256.New()
	fmt.Fprintf(h, "resource:%s\n", addr.String())
	fmt.Fprintf(h, "provider:%s\n", providerAddr.String())
	fmt.Fprintf(h, "schema-version:%d\n", schemaVersion)
	if providerKey != addrs.NoKey {
		fmt.Fprintf(h, "provider-key:%s\n", providerKey.String())
	}
	_, _ = h.Write(configFingerprint)
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(providerFingerprint)

	deps := make([]string, 0, len(dependencies))
	for _, dep := range dependencies {
		deps = append(deps, dep.String())
	}
	sort.Strings(deps)
	for _, dep := range deps {
		fmt.Fprintf(h, "\ndep:%s", dep)
	}

	return h.Sum(nil), nil
}

func smartRefreshMetadataFresh(lastRefreshUnix int64, now time.Time) bool {
	if lastRefreshUnix <= 0 {
		return false
	}
	last := time.Unix(lastRefreshUnix, 0).UTC()
	age := now.UTC().Sub(last)
	return age >= 0 && age <= smartRefreshMaxAge
}
