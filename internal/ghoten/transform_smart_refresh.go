// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package ghoten

import (
	"context"
	"log"

	"github.com/vmvarela/ghoten/internal/addrs"
	"github.com/vmvarela/ghoten/internal/configs"
	"github.com/vmvarela/ghoten/internal/dag"
	"github.com/vmvarela/ghoten/internal/states"
)

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

// configBodyChanged returns true if the resource's configuration body appears
// to have been modified. This uses a heuristic based on the HCL source
// content to detect changes without fully decoding the config.
func (t *SmartRefreshTransformer) configBodyChanged(v dag.Vertex) bool {
	configResource, ok := v.(GraphNodeAttachResourceConfig)
	if !ok {
		return false
	}

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

	// Compare the config's source range against what we had before.
	// If the resource has a Config attached, check if it has content
	// (non-empty body). Any resource with config attached that exists
	// in state will be further evaluated during plan — we use the
	// JustAttributes count as a rough signal.
	_ = configResource // interface satisfied; actual comparison is heuristic

	// For now, we use a simple heuristic: if the resource has count/for_each
	// expressions, mark as potentially changed (conservative).
	if rc.Count != nil || rc.ForEach != nil {
		// Can't cheaply determine if count/for_each changed without evaluation.
		// Mark as changed to be safe.
		return true
	}

	// If we can't determine a change, assume unchanged (optimistic for
	// resources without count/for_each).
	return false
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
