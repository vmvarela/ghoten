// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package ghoten

import (
	"testing"

	"github.com/vmvarela/ghoten/internal/addrs"
	"github.com/vmvarela/ghoten/internal/configs"
	"github.com/vmvarela/ghoten/internal/dag"
	"github.com/vmvarela/ghoten/internal/states"
)

// mockRefreshNode is a minimal graph node that implements GraphNodeSkipRefresh
// for testing the SmartRefreshTransformer.
type mockRefreshNode struct {
	name        string
	skipRefresh bool
}

func (n *mockRefreshNode) Name() string          { return n.name }
func (n *mockRefreshNode) SetSkipRefresh(v bool) { n.skipRefresh = v }

// mockNonRefreshNode is a graph node that does NOT implement GraphNodeSkipRefresh.
type mockNonRefreshNode struct {
	name string
}

func (n *mockNonRefreshNode) Name() string { return n.name }

// Verify that mockRefreshNode satisfies the interface at compile time.
var _ GraphNodeSkipRefresh = (*mockRefreshNode)(nil)

func TestSmartRefreshTransformer_Inactive(t *testing.T) {
	// When Active=false, the transformer must be a no-op regardless of state.
	g := &Graph{Path: addrs.RootModuleInstance}
	node := &mockRefreshNode{name: "aws_instance.web"}
	g.Add(node)

	tf := &SmartRefreshTransformer{
		Active: false,
		State:  states.NewState(),
	}
	if err := tf.Transform(t.Context(), g); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if node.skipRefresh {
		t.Errorf("expected skipRefresh=false when transformer is inactive, got true")
	}
}

func TestSmartRefreshTransformer_NilState(t *testing.T) {
	// With no prior state, every resource is new — none should be skipped.
	g := &Graph{Path: addrs.RootModuleInstance}
	node := &mockRefreshNode{name: "aws_instance.web"}
	g.Add(node)

	tf := &SmartRefreshTransformer{
		Active: true,
		State:  nil,
	}
	if err := tf.Transform(t.Context(), g); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if node.skipRefresh {
		t.Errorf("expected skipRefresh=false with nil state (all resources are new), got true")
	}
}

func TestSmartRefreshTransformer_EmptyState(t *testing.T) {
	// Empty state means no resources exist yet — no skipping.
	g := &Graph{Path: addrs.RootModuleInstance}
	node := &mockRefreshNode{name: "aws_instance.web"}
	g.Add(node)

	tf := &SmartRefreshTransformer{
		Active: true,
		State:  states.NewState(),
	}
	if err := tf.Transform(t.Context(), g); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if node.skipRefresh {
		t.Errorf("expected skipRefresh=false with empty state, got true")
	}
}

func TestSmartRefreshTransformer_SkipAllWhenNoChanges(t *testing.T) {
	// When the graph has no nodes that identify as config resources (e.g.
	// all are generic mockRefreshNodes without GraphNodeConfigResource), the
	// transformer detects zero changed nodes and should call skipAllRefresh.
	g := &Graph{Path: addrs.RootModuleInstance}
	nodeA := &mockRefreshNode{name: "aws_instance.a"}
	nodeB := &mockRefreshNode{name: "aws_instance.b"}
	g.Add(nodeA)
	g.Add(nodeB)

	// Populate state with a dummy resource so the transformer proceeds past
	// the "empty state" fast path.
	state := states.BuildState(func(s *states.SyncState) {
		s.SetResourceInstanceCurrent(
			addrs.Resource{
				Mode: addrs.ManagedResourceMode,
				Type: "aws_instance",
				Name: "a",
			}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance),
			&states.ResourceInstanceObjectSrc{
				Status:    states.ObjectReady,
				AttrsJSON: []byte(`{"id":"abc"}`),
			},
			addrs.AbsProviderConfig{
				Module:   addrs.RootModule,
				Provider: addrs.NewDefaultProvider("aws"),
			},
			addrs.NoKey,
		)
	})

	tf := &SmartRefreshTransformer{
		Active: true,
		State:  state,
		Config: &configs.Config{},
	}
	if err := tf.Transform(t.Context(), g); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	// Neither node implements GraphNodeConfigResource so identifyChangedNodes
	// returns an empty set → skipAllRefresh is called.
	if !nodeA.skipRefresh {
		t.Errorf("expected nodeA.skipRefresh=true when no config changes detected")
	}
	if !nodeB.skipRefresh {
		t.Errorf("expected nodeB.skipRefresh=true when no config changes detected")
	}
}

func TestSmartRefreshTransformer_skipAllRefresh(t *testing.T) {
	// Direct unit test for the skipAllRefresh helper.
	g := &Graph{Path: addrs.RootModuleInstance}
	n1 := &mockRefreshNode{name: "n1"}
	n2 := &mockRefreshNode{name: "n2"}
	// A non-refresh node should be unaffected.
	n3 := &mockNonRefreshNode{name: "n3"}
	g.Add(n1)
	g.Add(n2)
	g.Add(n3)

	tf := &SmartRefreshTransformer{}
	tf.skipAllRefresh(g)

	if !n1.skipRefresh {
		t.Errorf("n1.skipRefresh should be true after skipAllRefresh")
	}
	if !n2.skipRefresh {
		t.Errorf("n2.skipRefresh should be true after skipAllRefresh")
	}
}

func TestSmartRefreshTransformer_buildRefreshSet(t *testing.T) {
	// buildRefreshSet should include the changed node, its ancestors, and its
	// descendants.
	g := &Graph{Path: addrs.RootModuleInstance}
	ancestor := &mockRefreshNode{name: "ancestor"}
	changed := &mockRefreshNode{name: "changed"}
	descendant := &mockRefreshNode{name: "descendant"}
	unrelated := &mockRefreshNode{name: "unrelated"}

	g.Add(ancestor)
	g.Add(changed)
	g.Add(descendant)
	g.Add(unrelated)

	// ancestor → changed → descendant
	g.Connect(dag.BasicEdge(ancestor, changed))   // ancestor is a dependency of changed
	g.Connect(dag.BasicEdge(changed, descendant)) // descendant depends on changed

	tf := &SmartRefreshTransformer{}

	changedSet := make(dag.Set)
	changedSet.Add(changed)

	refreshSet := tf.buildRefreshSet(g, changedSet)

	for _, expected := range []dag.Vertex{ancestor, changed, descendant} {
		if !refreshSet.Include(expected) {
			n := expected.(*mockRefreshNode)
			t.Errorf("expected %q to be in the refresh set", n.name)
		}
	}

	if refreshSet.Include(unrelated) {
		t.Errorf("unrelated node should NOT be in the refresh set")
	}
}

func TestGraphNodeSkipRefreshInterface(t *testing.T) {
	// Compile-time assertions that key node types implement GraphNodeSkipRefresh.
	// These will cause a build failure if any node removes the interface.
	var _ GraphNodeSkipRefresh = (*nodeExpandPlannableResource)(nil)
	var _ GraphNodeSkipRefresh = (*NodePlannableResourceInstance)(nil)
	var _ GraphNodeSkipRefresh = (*NodePlannableResourceInstanceOrphan)(nil)
	var _ GraphNodeSkipRefresh = (*NodePlanDestroyableResourceInstance)(nil)
	var _ GraphNodeSkipRefresh = (*NodePlanDeposedResourceInstanceObject)(nil)
}
