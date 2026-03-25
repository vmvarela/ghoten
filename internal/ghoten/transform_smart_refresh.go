// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package ghoten

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"sort"

	"github.com/vmvarela/ghoten/internal/addrs"
	"github.com/vmvarela/ghoten/internal/configs"
	"github.com/vmvarela/ghoten/internal/dag"
	"github.com/vmvarela/ghoten/internal/states"
)

// configExprHash computes a deterministic SHA-256 fingerprint over the
// structural meta-arguments of a resource configuration block:
//
//   - The source ranges of count and for_each expressions (filename + byte
//     offsets).  If either expression changes its source location the byte
//     offsets shift and the digest changes.
//   - The sorted list of depends_on traversal source ranges.
//
// Attribute values (e.g. ami = "…") are intentionally excluded: attribute
// changes are caught by the plan diff, and we only need a cheap pre-signal
// that the *structural* parts have changed.
//
// The returned slice is always 32 bytes (sha256.Size).
func configExprHash(r *configs.Resource) []byte {
	h := sha256.New()

	// count expression — include file + byte range so relocating the block
	// (e.g. adding a resource above it) also changes the digest.
	if r.Count != nil {
		rng := r.Count.StartRange()
		fmt.Fprintf(h, "count:%s:%d-%d\n", rng.Filename, rng.Start.Byte, rng.End.Byte)
	}

	// for_each expression.
	if r.ForEach != nil {
		rng := r.ForEach.StartRange()
		fmt.Fprintf(h, "for_each:%s:%d-%d\n", rng.Filename, rng.Start.Byte, rng.End.Byte)
	}

	// depends_on — collect as strings and sort for determinism.
	deps := make([]string, 0, len(r.DependsOn))
	for _, traversal := range r.DependsOn {
		ref, diags := addrs.ParseRef(traversal)
		if diags.HasErrors() {
			// Fall back to source-range fingerprint for un-parseable traversals.
			rng := traversal.SourceRange()
			deps = append(deps, fmt.Sprintf("raw:%s:%d-%d", rng.Filename, rng.Start.Byte, rng.End.Byte))
			continue
		}
		deps = append(deps, ref.Subject.String())
	}
	sort.Strings(deps)
	for _, d := range deps {
		fmt.Fprintf(h, "dep:%s\n", d)
	}

	return h.Sum(nil)
}

// GraphNodeSkipRefresh is implemented by graph nodes that support selective
// refresh. The SmartRefreshTransformer uses this interface to mark nodes
// that should skip their refresh (ReadResource) call.
type GraphNodeSkipRefresh interface {
	SetSkipRefresh(bool)
}

// SmartRefreshTransformer is a GraphTransformer that selectively sets
// skipRefresh on resource nodes based on whether their configuration has
// changed compared to the prior state. Only resources with configuration
// changes, their ancestors, and their descendants are refreshed; all
// other resources skip the refresh call.
//
// This transformer runs after ReferenceTransformer (so dependency edges
// exist) and before TargetingTransformer (so targeting further narrows
// the graph).
type SmartRefreshTransformer struct {
	// Config is the root configuration.
	Config *configs.Config

	// State is the prior run state to compare against.
	State *states.State

	// Active indicates whether smart refresh is enabled.
	// When false, the transformer is a no-op.
	Active bool
}

func (t *SmartRefreshTransformer) Transform(_ context.Context, g *Graph) error {
	if !t.Active {
		return nil
	}

	// If there's no prior state, everything is new — refresh all.
	if t.State == nil || t.State.Empty() {
		log.Printf("[DEBUG] SmartRefreshTransformer: no prior state, refresh all")
		return nil
	}

	// Build the set of resource config addresses that have changed.
	// A resource is considered "changed" if it exists in the config but
	// not in state (new), or if it exists in state with recorded
	// dependencies that differ from the current config references.
	// Since we don't have decoded attribute values at this stage, we use
	// a conservative heuristic: resources present in both config and state
	// are considered unchanged unless the config block itself differs.
	changedNodes := t.identifyChangedNodes(g)

	if len(changedNodes) == 0 {
		// No config changes detected — skip refresh for all resources.
		log.Printf("[DEBUG] SmartRefreshTransformer: no config changes detected, skipping all refresh")
		t.skipAllRefresh(g)
		return nil
	}

	// Build the refresh set: changed nodes + ancestors + descendants.
	refreshSet := t.buildRefreshSet(g, changedNodes)

	// Apply: nodes NOT in refreshSet get skipRefresh=true.
	for _, v := range g.Vertices() {
		if _, ok := v.(GraphNodeSkipRefresh); !ok {
			continue
		}
		if refreshSet.Include(v) {
			log.Printf("[DEBUG] SmartRefreshTransformer: will refresh %q", dag.VertexName(v))
		} else {
			log.Printf("[DEBUG] SmartRefreshTransformer: skipping refresh for %q", dag.VertexName(v))
			v.(GraphNodeSkipRefresh).SetSkipRefresh(true)
		}
	}

	return nil
}

// identifyChangedNodes returns the set of graph vertices that represent
// resources with configuration changes relative to the prior state.
func (t *SmartRefreshTransformer) identifyChangedNodes(g *Graph) dag.Set {
	changed := make(dag.Set)

	for _, v := range g.Vertices() {
		rn, ok := v.(GraphNodeConfigResource)
		if !ok {
			continue
		}

		// Only care about managed resources and data sources.
		addr := rn.ResourceAddr()
		if addr.Resource.Mode != addrs.ManagedResourceMode &&
			addr.Resource.Mode != addrs.DataResourceMode {
			continue
		}

		// Check if this resource exists in the prior state.
		// If it doesn't exist in state, it's new — mark as changed.
		hasState := false
		for _, rs := range t.State.Resources(addr) {
			if rs != nil {
				hasState = true
				break
			}
		}

		if !hasState {
			// New resource — needs refresh (well, there's nothing to refresh,
			// but we include it so its descendants are also in the set).
			changed.Add(v)
			log.Printf("[DEBUG] SmartRefreshTransformer: %q is new (not in state)", dag.VertexName(v))
			continue
		}

		// Resource exists in both config and state.
		// Check if the config has changed by comparing the config body's
		// references against the stored dependencies.
		if t.configReferencesChanged(v) {
			changed.Add(v)
			log.Printf("[DEBUG] SmartRefreshTransformer: %q has changed references", dag.VertexName(v))
			continue
		}

		// Check if the resource config block content has been modified.
		// We do this by checking if the resource config's source range
		// or attribute count differs. This is a heuristic — the real
		// diff happens during plan, but we need a fast pre-check.
		if t.configBodyChanged(v) {
			changed.Add(v)
			log.Printf("[DEBUG] SmartRefreshTransformer: %q has changed config body", dag.VertexName(v))
		}
	}

	return changed
}

// configReferencesChanged returns true if the references from the resource's
// config differ from the dependencies stored in the prior state.
func (t *SmartRefreshTransformer) configReferencesChanged(v dag.Vertex) bool {
	rn, ok := v.(GraphNodeReferencer)
	if !ok {
		return false
	}

	configResource, ok := v.(GraphNodeConfigResource)
	if !ok {
		return false
	}

	// Get the current config references.
	configRefs := rn.References()
	configRefStrs := make(map[string]struct{}, len(configRefs))
	for _, ref := range configRefs {
		configRefStrs[ref.Subject.String()] = struct{}{}
	}

	// Get the stored dependencies from state.
	addr := configResource.ResourceAddr()
	for _, rs := range t.State.Resources(addr) {
		for _, is := range rs.Instances {
			if is.Current == nil {
				continue
			}
			stateDeps := make(map[string]struct{}, len(is.Current.Dependencies))
			for _, dep := range is.Current.Dependencies {
				stateDeps[dep.String()] = struct{}{}
			}

			// If reference counts differ, config has changed.
			if len(configRefStrs) != len(stateDeps) {
				return true
			}
			// If any reference is missing from state deps, config changed.
			for ref := range configRefStrs {
				if _, found := stateDeps[ref]; !found {
					return true
				}
			}
			// Check only the first instance — dependencies are per-resource,
			// not per-instance.
			return false
		}
	}

	return false
}

// configBodyChanged returns true if the resource's structural meta-arguments
// (count, for_each, depends_on) appear to have changed relative to the
// fingerprint stored in the prior state.
//
// If no fingerprint is stored (older state or first run after the feature
// was introduced), the method falls back to the conservative answer: assume
// changed, which forces a full refresh.
func (t *SmartRefreshTransformer) configBodyChanged(v dag.Vertex) bool {
	rn, ok := v.(GraphNodeConfigResource)
	if !ok {
		return false
	}

	// Walk the config tree to find our resource's config block.
	addr := rn.ResourceAddr()
	modCfg := t.Config.Descendent(addr.Module)
	if modCfg == nil {
		// Can't find module config — conservatively mark as changed.
		return true
	}

	var rc *configs.Resource
	for _, r := range modCfg.Module.ManagedResources {
		if r.Name == addr.Resource.Name && r.Type == addr.Resource.Type {
			rc = r
			break
		}
	}
	if rc == nil {
		for _, r := range modCfg.Module.DataResources {
			if r.Name == addr.Resource.Name && r.Type == addr.Resource.Type {
				rc = r
				break
			}
		}
	}
	if rc == nil {
		// Resource not found in config — orphan, mark as changed.
		return true
	}

	currentHash := configExprHash(rc)

	// Compare against every instance's stored hash. If any instance has
	// a nil hash (old state) or a different hash, mark as changed.
	for _, rs := range t.State.Resources(addr) {
		for _, is := range rs.Instances {
			if is.Current == nil {
				continue
			}
			if is.Current.ConfigExprHash == nil {
				// No fingerprint stored — conservative fallback.
				log.Printf("[DEBUG] SmartRefreshTransformer: %q has no stored fingerprint, assuming changed", addr)
				return true
			}
			if !bytes.Equal(is.Current.ConfigExprHash, currentHash) {
				log.Printf("[DEBUG] SmartRefreshTransformer: %q fingerprint mismatch, config body changed", addr)
				return true
			}
			// Only need to check the first instance — structural attrs are
			// per-resource, not per-instance.
			return false
		}
	}

	// No instances in state — treat as new/changed.
	return true
}

// buildRefreshSet constructs the complete set of nodes that need refresh:
// the changed nodes, all their ancestors (upstream dependencies), and all
// their descendants (downstream dependents).
func (t *SmartRefreshTransformer) buildRefreshSet(g *Graph, changedNodes dag.Set) dag.Set {
	refreshSet := make(dag.Set)

	for _, v := range changedNodes {
		refreshSet.Add(v)

		// Add all ancestors (dependencies).
		ancestors, _ := g.Ancestors(v)
		for _, a := range ancestors {
			refreshSet.Add(a)
		}

		// Add all descendants (dependents) — conservative approach:
		// refresh everything downstream.
		descendants, _ := g.Descendents(v)
		for _, d := range descendants {
			refreshSet.Add(d)
		}
	}

	return refreshSet
}

// skipAllRefresh sets skipRefresh=true on all refreshable nodes.
func (t *SmartRefreshTransformer) skipAllRefresh(g *Graph) {
	for _, v := range g.Vertices() {
		if sr, ok := v.(GraphNodeSkipRefresh); ok {
			sr.SetSkipRefresh(true)
		}
	}
}
