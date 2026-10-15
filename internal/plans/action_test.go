// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package plans

import "testing"

func TestActionCanTriggerDownstreamReplace(t *testing.T) {
	// ForgetThenCreate (destructive replacement with lifecycle.destroy =
	// false, which forgets the old object instead of destroying it) must
	// behave like the other create/replace actions and may trigger
	// replace_triggered_by downstream.
	for _, test := range []struct {
		action Action
		want   bool
	}{
		{NoOp, false},
		{Create, true},
		{Read, false},
		{Update, true},
		{DeleteThenCreate, true},
		{CreateThenDelete, true},
		{Delete, false},
		{Forget, false},
		{ForgetThenCreate, true},
		{Open, false},
	} {
		if got := test.action.CanTriggerDownstreamReplace(); got != test.want {
			t.Errorf("action %s: got %t, want %t", test.action, got, test.want)
		}
		if got := test.action.IsReplace(); got != (test.action == DeleteThenCreate || test.action == CreateThenDelete) {
			t.Errorf("action %s: got IsReplace %t, want %t", test.action, got, test.action == DeleteThenCreate || test.action == CreateThenDelete)
		}
	}
}
