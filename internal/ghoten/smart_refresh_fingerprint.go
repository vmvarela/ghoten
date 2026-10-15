// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package ghoten

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"time"

	"github.com/zclconf/go-cty/cty"
	ctymsgpack "github.com/zclconf/go-cty/cty/msgpack"

	"github.com/vmvarela/ghoten/internal/addrs"
	"github.com/vmvarela/ghoten/internal/states"
	"github.com/vmvarela/ghoten/internal/tfdiags"
)

const smartRefreshMaxAge = 24 * time.Hour

var smartRefreshNow = time.Now

func fingerprintCtyValue(val cty.Value) ([]byte, bool) {
	if val == cty.NilVal || !val.IsWhollyKnown() {
		return nil, false
	}

	unmarked, _ := val.UnmarkDeep()
	raw, err := ctymsgpack.Marshal(unmarked, unmarked.Type())
	if err != nil {
		return nil, false
	}

	sum := sha256.Sum256(raw)
	return sum[:], true
}

func providerConfigFingerprint(addr addrs.AbsProviderConfig, key addrs.InstanceKey, configVal cty.Value) ([]byte, bool) {
	configHash, ok := fingerprintCtyValue(configVal)
	if !ok {
		return nil, false
	}

	h := sha256.New()
	fmt.Fprintf(h, "provider:%s\n", addr.String())
	fmt.Fprintf(h, "provider-key:%s\n", key.String())
	h.Write(configHash)
	return h.Sum(nil), true
}

func resourceRefreshFingerprint(
	addr addrs.AbsResourceInstance,
	configVal cty.Value,
	providerAddr addrs.AbsProviderConfig,
	providerKey addrs.InstanceKey,
	providerFingerprint []byte,
	dependencies []addrs.ConfigResource,
	createBeforeDestroy bool,
	skipDestroy bool,
) ([]byte, bool) {
	if len(providerFingerprint) == 0 {
		return nil, false
	}

	configHash, ok := fingerprintCtyValue(configVal)
	if !ok {
		return nil, false
	}

	deps := make([]string, 0, len(dependencies))
	for _, dep := range dependencies {
		deps = append(deps, dep.String())
	}
	sort.Strings(deps)

	h := sha256.New()
	fmt.Fprintf(h, "resource:%s\n", addr.String())
	fmt.Fprintf(h, "provider:%s\n", providerAddr.String())
	fmt.Fprintf(h, "provider-key:%s\n", providerKey.String())
	fmt.Fprintf(h, "create-before-destroy:%t\n", createBeforeDestroy)
	fmt.Fprintf(h, "skip-destroy:%t\n", skipDestroy)
	h.Write(configHash)
	h.Write(providerFingerprint)
	for _, dep := range deps {
		fmt.Fprintf(h, "dep:%s\n", dep)
	}
	return h.Sum(nil), true
}

func smartRefreshCanSkip(prior *states.ResourceInstanceObject, fingerprint []byte, now time.Time) bool {
	if prior == nil || len(fingerprint) == 0 || len(prior.RefreshFingerprint) == 0 {
		return false
	}
	if !bytes.Equal(prior.RefreshFingerprint, fingerprint) {
		return false
	}
	if prior.LastRefreshTime <= 0 {
		return false
	}

	last := time.Unix(prior.LastRefreshTime, 0)
	if last.After(now.Add(5 * time.Minute)) {
		// A timestamp far in the future is suspicious; refresh conservatively.
		return false
	}

	return now.Sub(last) < smartRefreshMaxAge
}

func cloneBytes(src []byte) []byte {
	if src == nil {
		return nil
	}
	ret := make([]byte, len(src))
	copy(ret, src)
	return ret
}

func evaluatedResourceRefreshFingerprint(
	ctx context.Context,
	evalCtx EvalContext,
	n *NodeAbstractResourceInstance,
	forceCreateBeforeDestroy bool,
	skipDestroy bool,
) ([]byte, bool, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	if n == nil || n.Config == nil || n.Config.Managed == nil || n.Schema == nil {
		return nil, false, diags
	}
	if n.ResolvedProvider.ConfigFingerprint == nil {
		return nil, false, diags
	}

	forEach, forEachDiags := evaluateForEachExpression(ctx, n.Config.ForEach, evalCtx, n.Addr)
	diags = diags.Append(forEachDiags)
	if forEachDiags.HasErrors() {
		return nil, false, diags
	}

	keyData := EvalDataForInstanceKey(n.ResourceInstanceAddr().Resource.Key, forEach)
	configVal, _, configDiags := evalCtx.EvaluateBlock(ctx, n.Config.Config, n.Schema, nil, keyData)
	diags = diags.Append(configDiags.InConfigBody(n.Config.Config, n.Addr.String()))
	if configDiags.HasErrors() {
		return nil, false, diags
	}

	providerFingerprint := n.ResolvedProvider.ConfigFingerprint(n.ResolvedProviderKey)
	fingerprint, ok := resourceRefreshFingerprint(
		n.ResourceInstanceAddr(),
		configVal,
		n.ResolvedProvider.ProviderConfig,
		n.ResolvedProviderKey,
		providerFingerprint,
		n.Dependencies,
		n.Config.Managed.CreateBeforeDestroy || forceCreateBeforeDestroy,
		skipDestroy,
	)
	return fingerprint, ok, diags
}

func (n *NodePlannableResourceInstance) evaluatedRefreshFingerprint(
	ctx context.Context,
	evalCtx EvalContext,
	skipDestroy bool,
) ([]byte, bool, tfdiags.Diagnostics) {
	return evaluatedResourceRefreshFingerprint(ctx, evalCtx, n.NodeAbstractResourceInstance, n.ForceCreateBeforeDestroy, skipDestroy)
}
