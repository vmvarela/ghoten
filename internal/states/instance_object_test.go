// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package states

import (
	"bytes"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/vmvarela/ghoten/internal/addrs"
	"github.com/vmvarela/ghoten/internal/lang/marks"
	"github.com/zclconf/go-cty/cty"
)

func TestResourceInstanceObject_encode(t *testing.T) {
	value := cty.ObjectVal(map[string]cty.Value{
		"foo": cty.True,
	})
	// The in-memory order of resource dependencies is random, since they're an
	// unordered set.
	depsOne := []addrs.ConfigResource{
		addrs.RootModule.Resource(addrs.ManagedResourceMode, "test", "honk"),
		addrs.RootModule.Child("child").Resource(addrs.ManagedResourceMode, "test", "flub"),
		addrs.RootModule.Resource(addrs.ManagedResourceMode, "test", "boop"),
	}
	depsTwo := []addrs.ConfigResource{
		addrs.RootModule.Child("child").Resource(addrs.ManagedResourceMode, "test", "flub"),
		addrs.RootModule.Resource(addrs.ManagedResourceMode, "test", "boop"),
		addrs.RootModule.Resource(addrs.ManagedResourceMode, "test", "honk"),
	}

	// multiple instances may have been assigned the same deps slice
	objs := []*ResourceInstanceObject{
		{
			Value:        value,
			Status:       ObjectPlanned,
			Dependencies: depsOne,
		},
		{
			Value:        value,
			Status:       ObjectPlanned,
			Dependencies: depsTwo,
		},
		{
			Value:        value,
			Status:       ObjectPlanned,
			Dependencies: depsOne,
		},
		{
			Value:        value,
			Status:       ObjectPlanned,
			Dependencies: depsOne,
		},
	}

	var encoded []*ResourceInstanceObjectSrc

	// Encoding can happen concurrently, so we need to make sure the shared
	// Dependencies are safely handled
	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, obj := range objs {
		obj := obj
		wg.Add(1)
		go func() {
			defer wg.Done()
			rios, err := obj.Encode(value.Type(), 0)
			if err != nil {
				t.Errorf("unexpected error: %s", err)
			}
			mu.Lock()
			encoded = append(encoded, rios)
			mu.Unlock()
		}()
	}
	wg.Wait()

	// However, identical sets of dependencies should always be written to state
	// in an identical order, so we don't do meaningless state updates on refresh.
	for i := 0; i < len(encoded)-1; i++ {
		if diff := cmp.Diff(encoded[i].Dependencies, encoded[i+1].Dependencies); diff != "" {
			t.Errorf("identical dependencies got encoded in different orders:\n%s", diff)
		}
	}
}

func TestResourceInstanceObject_encode_sensitivity(t *testing.T) {
	depsOne := []addrs.ConfigResource{
		addrs.RootModule.Resource(addrs.ManagedResourceMode, "test", "honk"),
		addrs.RootModule.Child("child").Resource(addrs.ManagedResourceMode, "test", "flub"),
		addrs.RootModule.Resource(addrs.ManagedResourceMode, "test", "boop"),
	}

	tests := []struct {
		inputObj           *ResourceInstanceObject
		wantSensitivePaths bool
		wantTransientPaths bool
	}{
		{
			inputObj: &ResourceInstanceObject{
				Value: cty.ObjectVal(map[string]cty.Value{
					"foo": cty.ObjectVal(map[string]cty.Value{
						"bar": cty.BoolVal(true).Mark(marks.Sensitive),
					}),
				}),
				Status:       ObjectPlanned,
				Dependencies: depsOne,
			},
			wantSensitivePaths: true,
			wantTransientPaths: true,
		},
		{
			inputObj: &ResourceInstanceObject{
				Value: cty.ObjectVal(map[string]cty.Value{
					"foo": cty.ObjectVal(map[string]cty.Value{
						"bar": cty.BoolVal(true).Mark("non-sensitive"),
					}),
				}),
				Status:       ObjectPlanned,
				Dependencies: depsOne,
			},
			wantSensitivePaths: false,
			wantTransientPaths: true,
		},
		{
			inputObj: &ResourceInstanceObject{
				Value: cty.ObjectVal(map[string]cty.Value{
					"foo": cty.ObjectVal(map[string]cty.Value{
						"bar": cty.BoolVal(true),
					}),
				}),
				Status:       ObjectPlanned,
				Dependencies: depsOne,
			},
			wantSensitivePaths: false,
			wantTransientPaths: false,
		},
	}

	for _, test := range tests {
		encoded, err := test.inputObj.Encode(test.inputObj.Value.Type(), 0)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		if test.wantSensitivePaths && len(encoded.AttrSensitivePaths) == 0 {
			t.Fatalf("No AttrSensitivePaths found")
		}

		if !test.wantSensitivePaths && len(encoded.AttrSensitivePaths) != 0 {
			t.Fatalf("Got unexpected AttrSensitivePaths: %v", encoded.AttrSensitivePaths)
		}

		if test.wantTransientPaths && len(encoded.TransientPathValueMarks) == 0 {
			t.Fatalf("No TransientPathValueMarks found")
		}

		if !test.wantTransientPaths && len(encoded.TransientPathValueMarks) != 0 {
			t.Fatalf("Got unexpected TransientPathValueMarks: %v", encoded.TransientPathValueMarks)
		}
	}
}

func TestResourceInstanceObject_DeepCopy_ConfigExprHash(t *testing.T) {
	// Verify that DeepCopy correctly copies ConfigExprHash so that modifying
	// the copy doesn't affect the original.
	originalHash := []byte{0x01, 0x02, 0x03, 0x04}
	obj := &ResourceInstanceObject{
		Value:          cty.NilVal,
		Status:         ObjectReady,
		ConfigExprHash: originalHash,
	}

	copied := obj.DeepCopy()

	// Copied hash should be equal to original.
	if !bytes.Equal(copied.ConfigExprHash, originalHash) {
		t.Errorf("DeepCopy ConfigExprHash mismatch: got %v, want %v", copied.ConfigExprHash, originalHash)
	}

	// Modifying copied hash should not affect original.
	copied.ConfigExprHash[0] = 0xFF
	if !bytes.Equal(obj.ConfigExprHash, originalHash) {
		t.Errorf("Original ConfigExprHash was mutated by copy modification: got %v, want %v", obj.ConfigExprHash, originalHash)
	}
}

func TestResourceInstanceObject_DeepCopy_NilConfigExprHash(t *testing.T) {
	// Verify that DeepCopy handles nil ConfigExprHash correctly.
	obj := &ResourceInstanceObject{
		Value:          cty.NilVal,
		Status:         ObjectReady,
		ConfigExprHash: nil,
	}

	copied := obj.DeepCopy()

	if copied.ConfigExprHash != nil {
		t.Errorf("DeepCopy should preserve nil ConfigExprHash, got %v", copied.ConfigExprHash)
	}
}
