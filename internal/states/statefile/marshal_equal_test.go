// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package statefile

import (
	"fmt"
	"testing"

	"github.com/vmvarela/ghoten/internal/addrs"
	"github.com/vmvarela/ghoten/internal/states"
)

// newEqualTestFile returns a File whose state contains a single ready
// root-module resource with the given id; ids can be varied to make valid
// states differ from each other.
func newEqualTestFile(id string, status states.ObjectStatus) *File {
	obj := &states.ResourceInstanceObjectSrc{
		AttrsJSON:     []byte(fmt.Sprintf(`{"id":%q}`, id)),
		Status:        status,
		SchemaVersion: 0,
	}
	return newTestFile(obj, addrs.ManagedResourceMode)
}

// States that cannot be serialized (nil states, or snapshots containing
// objects that are invalid in a state file) must yield false without
// panicking, and must never be claimed as equal even when their bytes would
// match.
func TestStatesMarshalEqualInvalidStates(t *testing.T) {
	tests := map[string]struct {
		a, b *states.State
		want bool
	}{
		"both nil states": {
			a:    nil,
			b:    nil,
			want: false,
		},
		"nil receiver": {
			a:    nil,
			b:    states.NewState(),
			want: false,
		},
		"equal valid states": {
			a:    newEqualTestFile("x", states.ObjectReady).State,
			b:    newEqualTestFile("x", states.ObjectReady).State,
			want: true,
		},
		"different valid states": {
			a:    newEqualTestFile("x", states.ObjectReady).State,
			b:    newEqualTestFile("y", states.ObjectReady).State,
			want: false,
		},
		"invalid (planned) snapshots must not panic and never compare equal": {
			a:    newEqualTestFile("x", states.ObjectPlanned).State,
			b:    newEqualTestFile("x", states.ObjectPlanned).State,
			want: false,
		},
		"invalid vs valid snapshot": {
			a:    newEqualTestFile("x", states.ObjectPlanned).State,
			b:    newEqualTestFile("x", states.ObjectReady).State,
			want: false,
		},
		"valid vs invalid snapshot": {
			a:    newEqualTestFile("x", states.ObjectReady).State,
			b:    newEqualTestFile("x", states.ObjectPlanned).State,
			want: false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := StatesMarshalEqual(tc.a, tc.b)
			if got != tc.want {
				t.Errorf("expected %v, got %v", tc.want, got)
			}
		})
	}
}
