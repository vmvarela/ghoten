// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package ghoten

import (
	"testing"

	"github.com/vmvarela/ghoten/internal/addrs"
)

type mockRefreshNode struct {
	name        string
	skipRefresh bool
}

func (n *mockRefreshNode) Name() string          { return n.name }
func (n *mockRefreshNode) SetSkipRefresh(v bool) { n.skipRefresh = v }

type mockDataSourceNode struct {
	mockRefreshNode
	addr addrs.ConfigResource
}

func (n *mockDataSourceNode) ResourceAddr() addrs.ConfigResource { return n.addr }

var _ GraphNodeSkipRefresh = (*mockRefreshNode)(nil)
var _ GraphNodeConfigResource = (*mockDataSourceNode)(nil)

func TestSmartRefreshTransformerInactive(t *testing.T) {
	g := &Graph{Path: addrs.RootModuleInstance}
	node := &mockRefreshNode{name: "test_instance.foo"}
	g.Add(node)

	if err := (&SmartRefreshTransformer{Active: false}).Transform(t.Context(), g); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if node.skipRefresh {
		t.Fatal("inactive transformer must not mark skip candidates")
	}
}

func TestSmartRefreshTransformerMarksCandidates(t *testing.T) {
	g := &Graph{Path: addrs.RootModuleInstance}
	node := &mockRefreshNode{name: "test_instance.foo"}
	g.Add(node)

	if err := (&SmartRefreshTransformer{Active: true}).Transform(t.Context(), g); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if !node.skipRefresh {
		t.Fatal("configured managed resource candidate should be marked for evaluated skip")
	}
}

func TestSmartRefreshTransformerDoesNotSkipDataSources(t *testing.T) {
	g := &Graph{Path: addrs.RootModuleInstance}
	node := &mockDataSourceNode{
		mockRefreshNode: mockRefreshNode{name: "data.test.foo"},
		addr: addrs.ConfigResource{
			Module: addrs.RootModule,
			Resource: addrs.Resource{
				Mode: addrs.DataResourceMode,
				Type: "test",
				Name: "foo",
			},
		},
	}
	g.Add(node)

	if err := (&SmartRefreshTransformer{Active: true}).Transform(t.Context(), g); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if node.skipRefresh {
		t.Fatal("data sources must never be marked as managed-resource skip candidates")
	}
}
