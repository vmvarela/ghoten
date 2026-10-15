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

// configExprHash computes a cheap, coarse-grained fingerprint used only to
// seed graph-level Smart Refresh pruning. It is intentionally not authoritative:
// source ranges can miss same-length expression edits and ordinary attributes
// are not represented here. The per-instance evaluated RefreshFingerprint is
// the correctness boundary before ReadResource can be skipped.
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

	t.applyRefreshSet(g, refreshSet)

	return nil
}

func (t *SmartRefreshTransformer) applyRefreshSet(g *Graph, refreshSet dag.Set) {
	for _, v := range g.Vertices() {
		sr, ok := v.(GraphNodeSkipRefresh)
		if !ok || isDataSource(v) {
			continue
		}
		if refreshSet.Include(v) {
			log.Printf("[DEBUG] SmartRefreshTransformer: will refresh %q", dag.VertexName(v))
		} else {
			log.Printf("[DEBUG] SmartRefreshTransformer: skipping refresh for %q", dag.VertexName(v))
			sr.SetSkipRefresh(true)
		}
	}
}

// identifyChangedNodes returns the set of graph vertices that represent
// resources with configuration changes relative to the prior state.
func (t *SmartRefreshTransformer) identifyChangedNodes(g *Graph) dag.Set {
	changed := make(dag.Set)

	// First pass: identify directly changed nodes (new, orphan, hash mismatch).
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
		// Check if the resource config block structure has been modified
		// by comparing the configExprHash fingerprint (covers count,
		// for_each, and depends_on) against the stored hash in state.
		if t.configBodyChanged(v) {
			changed.Add(v)
			log.Printf("[DEBUG] SmartRefreshTransformer: %q has changed config body", dag.VertexName(v))
		}
	}

	return changed
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
	foundCurrent := false

	// Compare against every instance's stored hash. If any instance has
	// a nil hash (old state) or a different hash, mark as changed.
	for _, rs := range t.State.Resources(addr) {
		keys := make([]addrs.InstanceKey, 0, len(rs.Instances))
		for key := range rs.Instances {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool {
			return addrs.InstanceKeyLess(keys[i], keys[j])
		})
		for _, key := range keys {
			is := rs.Instances[key]
			if is.Current == nil {
				continue
			}
			foundCurrent = true
			if is.Current.ConfigExprHash == nil {
				// No fingerprint stored — conservative fallback.
				log.Printf("[DEBUG] SmartRefreshTransformer: %q has no stored fingerprint, assuming changed", addr)
				return true
			}
			if !bytes.Equal(is.Current.ConfigExprHash, currentHash) {
				log.Printf("[DEBUG] SmartRefreshTransformer: %q fingerprint mismatch, config body changed", addr)
				return true
			}
		}
	}

	if !foundCurrent {
		// No current instances in state — treat as new/changed.
		return true
	}

	// Every current instance that exists has the same coarse structural hash.
	// The authoritative per-instance evaluated fingerprint is checked later.
	return false
}

// isDataSource returns true if the given graph vertex represents a data source
// (DataResourceMode). Data sources are always re-read during the plan phase, so
// they never need an explicit refresh call and can be safely excluded from the
// refresh set.
func isDataSource(v dag.Vertex) bool {
	rn, ok := v.(GraphNodeConfigResource)
	if !ok {
		return false
	}
	return rn.ResourceAddr().Resource.Mode == addrs.DataResourceMode
}

// buildRefreshSet constructs the complete set of nodes that need refresh:
// the changed nodes, all their ancestors (upstream dependencies), and all
// their descendants (downstream dependents).
//
// Data sources are excluded from the refresh set: they are always re-read
// during the plan phase and do not have remote state that can drift, so
// including them in the refresh set is wasteful.
func (t *SmartRefreshTransformer) buildRefreshSet(g *Graph, changedNodes dag.Set) dag.Set {
	// Phase 1: walk upstream in graph edge direction from dirty seeds to find
	// every dependent. ReferenceTransformer connects depender -> dependency,
	// so UpEdges are the reverse links: dependency -> dependers.
	affected := make(dag.Set)
	queue := make([]dag.Vertex, 0, len(changedNodes))
	for _, v := range changedNodes {
		if !affected.Include(v) {
			affected.Add(v)
			queue = append(queue, v)
		}
	}
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		for _, next := range g.UpEdges(v) {
			if affected.Include(next) {
				continue
			}
			affected.Add(next)
			queue = append(queue, next)
		}
	}

	// Phase 2: every affected node may have other dependencies that are needed
	// to evaluate it safely. Walk dependency edges from all affected nodes in
	// one multi-source traversal. Each vertex/edge is considered at most a
	// constant number of times, avoiding repeated Ancestors/Descendents DFS.
	closure := make(dag.Set)
	queue = queue[:0]
	for _, v := range affected {
		if !closure.Include(v) {
			closure.Add(v)
			queue = append(queue, v)
		}
	}
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		for _, next := range g.DownEdges(v) {
			if closure.Include(next) {
				continue
			}
			closure.Add(next)
			queue = append(queue, next)
		}
	}

	refreshSet := make(dag.Set)
	for _, v := range closure {
		if !isDataSource(v) {
			refreshSet.Add(v)
		}
	}
	return refreshSet
}

// skipAllRefresh sets skipRefresh=true on all refreshable nodes.
func (t *SmartRefreshTransformer) skipAllRefresh(g *Graph) {
	for _, v := range g.Vertices() {
		if sr, ok := v.(GraphNodeSkipRefresh); ok && !isDataSource(v) {
			sr.SetSkipRefresh(true)
		}
	}
}
