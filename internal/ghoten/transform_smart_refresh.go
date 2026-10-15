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
// refresh. Smart Refresh uses the graph only to mark safe candidates for
// skipping; the final decision is made per resource instance after expression
// evaluation and provider resolution.
type GraphNodeSkipRefresh interface {
	SetSkipRefresh(bool)
}

// SmartRefreshTransformer marks configured managed resources as candidates for
// skipping remote refresh. It deliberately does not try to infer configuration
// changes from HCL syntax.
//
// The old implementation built a syntactic ConfigExprHash and repeatedly walked
// dependency closures. That could miss variable/provider changes and made the
// transform itself increasingly expensive on large graphs.
//
// The final decision now happens in NodePlannableResourceInstance, where Ghoten
// has the evaluated per-instance configuration, provider instance fingerprint,
// and freshness metadata. Orphans, deposed objects, destroy nodes, and data
// sources remain conservative and are never marked as skip candidates here.
type SmartRefreshTransformer struct {
	// Config and State are retained for source compatibility with graph-builder
	// wiring and tests. The evaluated implementation no longer needs to inspect
	// either one during graph transformation.
	Config *configs.Config
	State  *states.State

	Active bool
}

func (t *SmartRefreshTransformer) Transform(_ context.Context, g *Graph) error {
	if !t.Active {
		return nil
	}

	for _, v := range g.Vertices() {
		sr, ok := v.(GraphNodeSkipRefresh)
		if !ok || isDataSource(v) {
			continue
		}

		// State-only objects cannot produce a trustworthy evaluated fingerprint,
		// so always refresh them conservatively.
		switch v.(type) {
		case *NodePlannableResourceInstanceOrphan,
			*NodePlanDeposedResourceInstanceObject,
			*NodePlanDestroyableResourceInstance:
			continue
		}

		log.Printf("[DEBUG] SmartRefreshTransformer: %q is an evaluated skip candidate", dag.VertexName(v))
		sr.SetSkipRefresh(true)
	}

	return nil
}

// isDataSource returns true if the given graph vertex represents a data source.
// Data sources are always re-read during planning and therefore are never
// candidates for managed-resource refresh skipping.
func isDataSource(v dag.Vertex) bool {
	rn, ok := v.(GraphNodeConfigResource)
	if !ok {
		return false
	}
	return rn.ResourceAddr().Resource.Mode == addrs.DataResourceMode
}
