// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package ghoten

import (
	"bytes"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hcltest"
	"github.com/zclconf/go-cty/cty"

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
	dataSource := &mockDataSourceNode{name: "data_source"}
	// A non-refresh node should be unaffected.
	n3 := &mockNonRefreshNode{name: "n3"}
	g.Add(n1)
	g.Add(n2)
	g.Add(dataSource)
	g.Add(n3)

	tf := &SmartRefreshTransformer{}
	tf.skipAllRefresh(g)

	if !n1.skipRefresh {
		t.Errorf("n1.skipRefresh should be true after skipAllRefresh")
	}
	if !n2.skipRefresh {
		t.Errorf("n2.skipRefresh should be true after skipAllRefresh")
	}
	if dataSource.skipRefresh {
		t.Errorf("data sources should not receive skipRefresh=true in skipAllRefresh")
	}
}

func TestSmartRefreshTransformer_applyRefreshSet_SkipsManagedOnly(t *testing.T) {
	g := &Graph{Path: addrs.RootModuleInstance}
	managedInSet := &mockRefreshNode{name: "managed_in_set"}
	managedOutOfSet := &mockRefreshNode{name: "managed_out_of_set"}
	dataSource := &mockDataSourceNode{name: "data_source"}

	g.Add(managedInSet)
	g.Add(managedOutOfSet)
	g.Add(dataSource)

	refreshSet := make(dag.Set)
	refreshSet.Add(managedInSet)

	tf := &SmartRefreshTransformer{}
	tf.applyRefreshSet(g, refreshSet)

	if managedInSet.skipRefresh {
		t.Errorf("managed resource in refresh set should keep skipRefresh=false")
	}
	if !managedOutOfSet.skipRefresh {
		t.Errorf("managed resource outside refresh set should get skipRefresh=true")
	}
	if dataSource.skipRefresh {
		t.Errorf("data sources should not receive skipRefresh=true in applyRefreshSet")
	}
}

// mockDataSourceNode is a graph node that implements both GraphNodeSkipRefresh
// and GraphNodeConfigResource with DataResourceMode — used to verify that data
// sources are excluded from the refresh set.
type mockDataSourceNode struct {
	name        string
	skipRefresh bool
}

func (n *mockDataSourceNode) Name() string          { return n.name }
func (n *mockDataSourceNode) SetSkipRefresh(v bool) { n.skipRefresh = v }
func (n *mockDataSourceNode) ResourceAddr() addrs.ConfigResource {
	return addrs.ConfigResource{
		Module: addrs.RootModule,
		Resource: addrs.Resource{
			Mode: addrs.DataResourceMode,
			Type: "github_organization",
			Name: n.name,
		},
	}
}

// Verify interface satisfaction at compile time.
var _ GraphNodeSkipRefresh = (*mockDataSourceNode)(nil)
var _ GraphNodeConfigResource = (*mockDataSourceNode)(nil)

func TestSmartRefreshTransformer_buildRefreshSet(t *testing.T) {
	// buildRefreshSet should include the changed node, its ancestors, and its
	// descendants — but NOT data sources.
	g := &Graph{Path: addrs.RootModuleInstance}
	ancestor := &mockRefreshNode{name: "ancestor"}
	changed := &mockRefreshNode{name: "changed"}
	descendant := &mockRefreshNode{name: "descendant"}
	unrelated := &mockRefreshNode{name: "unrelated"}
	dataSource := &mockDataSourceNode{name: "data_ancestor"}

	g.Add(ancestor)
	g.Add(changed)
	g.Add(descendant)
	g.Add(unrelated)
	g.Add(dataSource)

	// dataSource → ancestor → changed → descendant
	g.Connect(dag.BasicEdge(dataSource, ancestor)) // data source is a dependency of ancestor
	g.Connect(dag.BasicEdge(ancestor, changed))    // ancestor is a dependency of changed
	g.Connect(dag.BasicEdge(changed, descendant))  // descendant depends on changed

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

	// Data sources must be excluded even if they are ancestors of a changed node.
	if refreshSet.Include(dataSource) {
		t.Errorf("data source %q should NOT be in the refresh set (data sources are re-read during plan)", dataSource.name)
	}
}

func TestSmartRefreshTransformer_buildRefreshSet_DataSourceChangedNode(t *testing.T) {
	// When a data source itself is in changedNodes (e.g. it has no stored state),
	// it must NOT be added to the refresh set — data sources don't need refresh.
	g := &Graph{Path: addrs.RootModuleInstance}
	managed := &mockRefreshNode{name: "managed"}
	dataSource := &mockDataSourceNode{name: "data_source"}

	g.Add(managed)
	g.Add(dataSource)

	// data source is a dependency of managed
	g.Connect(dag.BasicEdge(dataSource, managed))

	tf := &SmartRefreshTransformer{}

	// Suppose the data source itself is in changedNodes.
	changedSet := make(dag.Set)
	changedSet.Add(dataSource)

	refreshSet := tf.buildRefreshSet(g, changedSet)

	if refreshSet.Include(dataSource) {
		t.Errorf("data source should NOT be in the refresh set even when it is a changed node")
	}

	// Its descendant (managed) should still be included since the data source changed.
	if !refreshSet.Include(managed) {
		t.Errorf("managed resource %q should be in the refresh set as a descendant of the changed data source", managed.name)
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

// --- configExprHash tests ---

// makeTestResource builds a minimal configs.Resource suitable for
// configExprHash tests.
func makeTestResource(count, forEach hcl.Expression, dependsOn []hcl.Traversal) *configs.Resource {
	return &configs.Resource{
		Mode:      addrs.ManagedResourceMode,
		Type:      "aws_instance",
		Name:      "web",
		Config:    configs.SynthBody("", map[string]cty.Value{}),
		Count:     count,
		ForEach:   forEach,
		DependsOn: dependsOn,
	}
}

func TestConfigExprHash_Deterministic(t *testing.T) {
	// The same resource must always produce the same hash.
	count := hcltest.MockExprLiteral(cty.NumberIntVal(3))
	r := makeTestResource(count, nil, nil)

	h1 := configExprHash(r)
	h2 := configExprHash(r)
	if !bytes.Equal(h1, h2) {
		t.Errorf("configExprHash is not deterministic: %x != %x", h1, h2)
	}
}

func TestConfigExprHash_EmptyResource(t *testing.T) {
	// A resource with no count, for_each or depends_on must still produce
	// a non-nil, fixed-length digest.
	r := makeTestResource(nil, nil, nil)
	h := configExprHash(r)
	if len(h) != 32 {
		t.Errorf("expected 32-byte SHA-256 digest, got %d bytes", len(h))
	}
}

func TestConfigExprHash_CountChanges(t *testing.T) {
	// A resource with count must produce a different hash than one without.
	withCount := makeTestResource(hcltest.MockExprLiteral(cty.NumberIntVal(2)), nil, nil)
	withoutCount := makeTestResource(nil, nil, nil)

	hWith := configExprHash(withCount)
	hWithout := configExprHash(withoutCount)

	if bytes.Equal(hWith, hWithout) {
		t.Errorf("expected different hashes for resources with/without count, got the same")
	}
}

func TestConfigExprHash_ForEachChanges(t *testing.T) {
	// A resource with for_each must produce a different hash than one without.
	withForEach := makeTestResource(nil, hcltest.MockExprLiteral(cty.StringVal("a")), nil)
	withoutForEach := makeTestResource(nil, nil, nil)

	hWith := configExprHash(withForEach)
	hWithout := configExprHash(withoutForEach)

	if bytes.Equal(hWith, hWithout) {
		t.Errorf("expected different hashes for resources with/without for_each, got the same")
	}
}

func TestConfigExprHash_DependsOnChanges(t *testing.T) {
	// Adding a depends_on entry must change the hash.
	dep := hcl.Traversal{
		hcl.TraverseRoot{Name: "aws_security_group"},
		hcl.TraverseAttr{Name: "main"},
	}
	withDep := makeTestResource(nil, nil, []hcl.Traversal{dep})
	withoutDep := makeTestResource(nil, nil, nil)

	hWith := configExprHash(withDep)
	hWithout := configExprHash(withoutDep)

	if bytes.Equal(hWith, hWithout) {
		t.Errorf("expected different hashes for resources with/without depends_on, got the same")
	}
}

func TestConfigExprHash_DependsOnOrderIndependent(t *testing.T) {
	// The hash must be stable regardless of the order depends_on entries appear.
	depA := hcl.Traversal{
		hcl.TraverseRoot{Name: "aws_security_group"},
		hcl.TraverseAttr{Name: "main"},
	}
	depB := hcl.Traversal{
		hcl.TraverseRoot{Name: "aws_vpc"},
		hcl.TraverseAttr{Name: "main"},
	}

	rAB := makeTestResource(nil, nil, []hcl.Traversal{depA, depB})
	rBA := makeTestResource(nil, nil, []hcl.Traversal{depB, depA})

	hAB := configExprHash(rAB)
	hBA := configExprHash(rBA)

	if !bytes.Equal(hAB, hBA) {
		t.Errorf("configExprHash is not order-independent for depends_on: %x != %x", hAB, hBA)
	}
}

// --- backfillConfigExprHashes tests ---

func TestBackfillConfigExprHashes_PopulatesHash(t *testing.T) {
	// backfillConfigExprHashes must set ConfigExprHash on instances that don't
	// already have one.
	rc := makeTestResource(nil, nil, nil)
	expectedHash := configExprHash(rc)

	state := states.BuildState(func(s *states.SyncState) {
		s.SetResourceInstanceCurrent(
			addrs.Resource{
				Mode: addrs.ManagedResourceMode,
				Type: rc.Type,
				Name: rc.Name,
			}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance),
			&states.ResourceInstanceObjectSrc{
				Status:    states.ObjectReady,
				AttrsJSON: []byte(`{"id":"abc"}`),
				// ConfigExprHash deliberately left nil
			},
			addrs.AbsProviderConfig{
				Module:   addrs.RootModule,
				Provider: addrs.NewDefaultProvider("aws"),
			},
			addrs.NoKey,
		)
	})

	cfg := &configs.Config{
		Module: &configs.Module{
			ManagedResources: map[string]*configs.Resource{
				rc.Type + "." + rc.Name: rc,
			},
		},
	}

	backfillConfigExprHashes(state, cfg)

	// Verify the hash was set.
	ms := state.Module(addrs.RootModuleInstance)
	if ms == nil {
		t.Fatal("root module not found in state after backfill")
	}
	rsAddr := addrs.Resource{Mode: addrs.ManagedResourceMode, Type: rc.Type, Name: rc.Name}
	rs := ms.Resource(rsAddr)
	if rs == nil {
		t.Fatal("resource not found in state after backfill")
	}
	is := rs.Instance(addrs.NoKey)
	if is == nil || is.Current == nil {
		t.Fatal("resource instance not found in state after backfill")
	}
	if !bytes.Equal(is.Current.ConfigExprHash, expectedHash) {
		t.Errorf("ConfigExprHash not set correctly: got %x, want %x", is.Current.ConfigExprHash, expectedHash)
	}
}

func TestBackfillConfigExprHashes_PreservesExistingHash(t *testing.T) {
	// backfillConfigExprHashes must NOT overwrite an existing hash that already
	// matches the current config.
	rc := makeTestResource(nil, nil, nil)
	existingHash := configExprHash(rc)

	state := states.BuildState(func(s *states.SyncState) {
		s.SetResourceInstanceCurrent(
			addrs.Resource{
				Mode: addrs.ManagedResourceMode,
				Type: rc.Type,
				Name: rc.Name,
			}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance),
			&states.ResourceInstanceObjectSrc{
				Status:         states.ObjectReady,
				AttrsJSON:      []byte(`{"id":"abc"}`),
				ConfigExprHash: existingHash,
			},
			addrs.AbsProviderConfig{
				Module:   addrs.RootModule,
				Provider: addrs.NewDefaultProvider("aws"),
			},
			addrs.NoKey,
		)
	})

	cfg := &configs.Config{
		Module: &configs.Module{
			ManagedResources: map[string]*configs.Resource{
				rc.Type + "." + rc.Name: rc,
			},
		},
	}

	backfillConfigExprHashes(state, cfg)

	ms := state.Module(addrs.RootModuleInstance)
	rs := ms.Resource(addrs.Resource{Mode: addrs.ManagedResourceMode, Type: rc.Type, Name: rc.Name})
	is := rs.Instance(addrs.NoKey)
	if !bytes.Equal(is.Current.ConfigExprHash, existingHash) {
		t.Errorf("backfill overwrote an already-correct hash: got %x, want %x",
			is.Current.ConfigExprHash, existingHash)
	}
}

func TestBackfillConfigExprHashes_NilState(t *testing.T) {
	// Must not panic on nil state.
	cfg := &configs.Config{Module: &configs.Module{}}
	backfillConfigExprHashes(nil, cfg)
}

func TestBackfillConfigExprHashes_NilConfig(t *testing.T) {
	// Must not panic on nil config.
	state := states.NewState()
	backfillConfigExprHashes(state, nil)
}
