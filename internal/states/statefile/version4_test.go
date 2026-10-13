// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package statefile

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/vmvarela/ghoten/internal/addrs"
	"github.com/vmvarela/ghoten/internal/encryption"
	"github.com/vmvarela/ghoten/internal/lang/marks"
	"github.com/vmvarela/ghoten/internal/states"
	"github.com/vmvarela/ghoten/internal/tfdiags"
	"github.com/zclconf/go-cty/cty"
)

// This test verifies that modules are sorted before resources:
// https://github.com/hashicorp/terraform/issues/21552
func TestVersion4_sort(t *testing.T) {
	resources := sortResourcesV4{
		{
			Module: "module.child",
			Type:   "test_instance",
			Name:   "foo",
		},
		{
			Type: "test_instance",
			Name: "foo",
		},
		{
			Module: "module.kinder",
			Type:   "test_instance",
			Name:   "foo",
		},
		{
			Module: "module.child.grandchild",
			Type:   "test_instance",
			Name:   "foo",
		},
	}
	sort.Stable(resources)

	moduleOrder := []string{"", "module.child", "module.child.grandchild", "module.kinder"}

	for i, resource := range resources {
		if resource.Module != moduleOrder[i] {
			t.Errorf("wrong sort order: expected %q, got %q\n", moduleOrder[i], resource.Module)
		}
	}
}

func TestVersion4_unmarshalPaths(t *testing.T) {
	testCases := map[string]struct {
		json  string
		paths []cty.Path
		diags []string
	}{
		"no paths": {
			json:  `[]`,
			paths: []cty.Path{},
		},
		"attribute path": {
			json: `[
  [
    {
      "type": "get_attr",
	  "value": "password"
    }
  ]
]`,
			paths: []cty.Path{cty.GetAttrPath("password")},
		},
		"attribute and string index": {
			json: `[
  [
    {
      "type": "get_attr",
	  "value": "triggers"
    },
    {
      "type": "index",
      "value": {
        "value": "secret",
		"type": "string"
      }
    }
  ]
]`,
			paths: []cty.Path{cty.GetAttrPath("triggers").IndexString("secret")},
		},
		"attribute, number index, attribute": {
			json: `[
  [
    {
      "type": "get_attr",
	  "value": "identities"
    },
    {
      "type": "index",
      "value": {
        "value": 2,
		"type": "number"
      }
    },
    {
      "type": "get_attr",
      "value": "private_key"
    }
  ]
]`,
			paths: []cty.Path{cty.GetAttrPath("identities").IndexInt(2).GetAttr("private_key")},
		},
		"multiple paths": {
			json: `[
  [
    {
      "type": "get_attr",
	  "value": "alpha"
    }
  ],
  [
    {
      "type": "get_attr",
	  "value": "beta"
    }
  ],
  [
    {
      "type": "get_attr",
	  "value": "gamma"
    }
  ]
]`,
			paths: []cty.Path{cty.GetAttrPath("alpha"), cty.GetAttrPath("beta"), cty.GetAttrPath("gamma")},
		},
		"errors": {
			json: `[
  [
    {
      "type": "get_attr",
	  "value": 5
    }
  ],
  [
    {
      "type": "index",
	  "value": "test"
    }
  ],
  [
    {
      "type": "invalid_type",
	  "value": ["this is invalid too"]
    }
  ]
]`,
			paths: []cty.Path{},
			diags: []string{
				"Failed to unmarshal get attr step name",
				"Failed to unmarshal index step key",
				"Unsupported path step",
			},
		},
		"one invalid path, others valid": {
			json: `[
  [
    {
      "type": "get_attr",
	  "value": "alpha"
    }
  ],
  [
    {
      "type": "invalid_type",
	  "value": ["this is invalid too"]
    }
  ],
  [
    {
      "type": "get_attr",
	  "value": "gamma"
    }
  ]
]`,
			paths: []cty.Path{cty.GetAttrPath("alpha"), cty.GetAttrPath("gamma")},
			diags: []string{"Unsupported path step"},
		},
		"invalid structure": {
			json:  `{}`,
			paths: []cty.Path{},
			diags: []string{"Error unmarshaling path steps"},
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			paths, diags := unmarshalPaths([]byte(tc.json))

			if len(tc.diags) == 0 {
				if len(diags) != 0 {
					t.Errorf("expected no diags, got: %#v", diags)
				}
			} else {
				if got, want := len(diags), len(tc.diags); got != want {
					t.Fatalf("got %d diags, want %d\n%s", got, want, diags.Err())
				}
				for i := range tc.diags {
					got := tfdiags.Diagnostics{diags[i]}.Err().Error()
					if !strings.Contains(got, tc.diags[i]) {
						t.Errorf("expected diag %d to contain %q, but was:\n%s", i, tc.diags[i], got)
					}
				}
			}

			if len(paths) != len(tc.paths) {
				t.Fatalf("got %d paths, want %d", len(paths), len(tc.paths))
			}
			for i, path := range paths {
				if !path.Equals(tc.paths[i]) {
					t.Errorf("wrong paths\n got: %#v\nwant: %#v", path, tc.paths[i])
				}
			}
		})
	}
}

func TestVersion4_marshalPaths(t *testing.T) {
	testCases := map[string]struct {
		paths []cty.Path
		json  string
	}{
		"no paths": {
			paths: []cty.Path{},
			json:  `[]`,
		},
		"attribute path": {
			paths: []cty.Path{cty.GetAttrPath("password")},
			json:  `[[{"type":"get_attr","value":"password"}]]`,
		},
		"attribute, number index, attribute": {
			paths: []cty.Path{cty.GetAttrPath("identities").IndexInt(2).GetAttr("private_key")},
			json:  `[[{"type":"get_attr","value":"identities"},{"type":"index","value":{"value":2,"type":"number"}},{"type":"get_attr","value":"private_key"}]]`,
		},
		"multiple paths": {
			paths: []cty.Path{cty.GetAttrPath("a"), cty.GetAttrPath("b"), cty.GetAttrPath("c")},
			json:  `[[{"type":"get_attr","value":"a"}],[{"type":"get_attr","value":"b"}],[{"type":"get_attr","value":"c"}]]`,
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			json, diags := marshalPaths(tc.paths)

			if len(diags) != 0 {
				t.Fatalf("expected no diags, got: %#v", diags)
			}

			if got, want := string(json), tc.json; got != want {
				t.Fatalf("wrong JSON output\n got: %s\nwant: %s\n", got, want)
			}
		})
	}
}

// fixtureFile builds a File containing a single root-module resource in the
// given mode, typed "test_instance" and named "foo".
func fixtureFile(mode addrs.ResourceMode, objs map[addrs.InstanceKey]*states.ResourceInstanceObjectSrc, deposed map[addrs.InstanceKey]map[states.DeposedKey]*states.ResourceInstanceObjectSrc) *File {
	return fixtureFileAddr(addrs.Resource{Mode: mode, Type: "test_instance", Name: "foo"}, objs, deposed)
}

// fixtureFileAddr builds a File containing a single root-module resource at
// addr. objs are current objects keyed by instance key; deposed maps
// instance keys to deposed objects keyed by DeposedKey.
func fixtureFileAddr(addr addrs.Resource, objs map[addrs.InstanceKey]*states.ResourceInstanceObjectSrc, deposed map[addrs.InstanceKey]map[states.DeposedKey]*states.ResourceInstanceObjectSrc) *File {
	state := states.NewState()
	providerConfig := addrs.AbsProviderConfig{
		Provider: addrs.NewDefaultProvider("test"),
		Module:   addrs.RootModule,
	}
	state.RootModule().SetResourceProvider(addr, providerConfig)
	for key, obj := range objs {
		state.RootModule().SetResourceInstanceCurrent(addr.Instance(key), obj, providerConfig, addrs.NoKey)
	}
	for key, dep := range deposed {
		for dk, obj := range dep {
			state.RootModule().SetResourceInstanceDeposed(addr.Instance(key), dk, obj, providerConfig, addrs.NoKey)
		}
	}
	return &File{Serial: 1, Lineage: "test-lineage", State: state}
}

func baseFixtureObj() *states.ResourceInstanceObjectSrc {
	return &states.ResourceInstanceObjectSrc{
		AttrsJSON:     []byte(`{"id":"x"}`),
		Status:        states.ObjectReady,
		SchemaVersion: 0,
	}
}

func ephMarkedObj(obj *states.ResourceInstanceObjectSrc, path cty.Path) *states.ResourceInstanceObjectSrc {
	obj.TransientPathValueMarks = []cty.PathValueMarks{{
		Path:  path,
		Marks: cty.NewValueMarks(marks.Ephemeral),
	}}
	return obj
}

// The statefile layer must refuse to persist ephemeral values on ordinary
// managed and data resources. The guard scans TransientPathValueMarks (the
// carrier of all non-serialized marks) on both current and deposed objects,
// and the write must be all-or-nothing: even alongside a valid sibling
// instance, a leaked ephemeral mark yields an error and zero writer output.
func TestVersion4EphemeralMarksInTransientPathValueMarks(t *testing.T) {
	nestedPath := cty.GetAttrPath("list").IndexInt(0)
	ephObj := func() *states.ResourceInstanceObjectSrc {
		obj := baseFixtureObj()
		obj.AttrsJSON = []byte(`{"list":["secret"]}`)
		return ephMarkedObj(obj, nestedPath)
	}

	tests := map[string]struct {
		file        *File
		errContains string
	}{
		"managed resource current object with nested ephemeral mark": {
			file:        fixtureFile(addrs.ManagedResourceMode, map[addrs.InstanceKey]*states.ResourceInstanceObjectSrc{addrs.NoKey: ephObj()}, nil),
			errContains: "Failed to serialize resource instance in state",
		},
		"data resource current object with ephemeral mark": {
			file:        fixtureFile(addrs.DataResourceMode, map[addrs.InstanceKey]*states.ResourceInstanceObjectSrc{addrs.NoKey: ephObj()}, nil),
			errContains: "Failed to serialize resource instance in state",
		},
		"deposed object with ephemeral mark": {
			file: fixtureFile(addrs.ManagedResourceMode,
				map[addrs.InstanceKey]*states.ResourceInstanceObjectSrc{addrs.NoKey: baseFixtureObj()},
				map[addrs.InstanceKey]map[states.DeposedKey]*states.ResourceInstanceObjectSrc{
					addrs.NoKey: {states.DeposedKey("deadbeef"): ephObj()},
				}),
			errContains: "Failed to serialize resource instance in state",
		},
		"valid sibling must not suppress the error (all-or-nothing)": {
			file: fixtureFile(addrs.ManagedResourceMode, map[addrs.InstanceKey]*states.ResourceInstanceObjectSrc{
				addrs.IntKey(0): baseFixtureObj(),
				addrs.IntKey(1): ephObj(),
			}, nil),
			errContains: "Failed to serialize resource instance in state",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			cw := &countingWriter{}
			err := Write(tc.file, cw, encryption.StateEncryptionDisabled())
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.errContains) {
				t.Errorf("expected error to contain %q, got:\n%s", tc.errContains, err.Error())
			}
			if !strings.Contains(err.Error(), "ephemeral") {
				t.Errorf("expected error to mention ephemeral values, got:\n%s", err.Error())
			}
			if cw.writes != 0 {
				t.Errorf("expected zero writer writes, got %d (%d bytes)", cw.writes, cw.size)
			}
		})
	}
}

// Unmarked values and sensitive-only marks must still be accepted: the guard
// rejects only the Ephemeral mark.
func TestVersion4AcceptsUnmarkedAndSensitiveOnlyValues(t *testing.T) {
	sensitiveOnly := func() *states.ResourceInstanceObjectSrc {
		path := cty.GetAttrPath("password")
		obj := baseFixtureObj()
		obj.AttrsJSON = []byte(`{"password":"hunter2"}`)
		obj.AttrSensitivePaths = []cty.PathValueMarks{{
			Path:  path,
			Marks: cty.NewValueMarks(marks.Sensitive),
		}}
		// TransientPathValueMarks carries the sensitive mark too; it must
		// not trigger the guard.
		obj.TransientPathValueMarks = []cty.PathValueMarks{{
			Path:  path,
			Marks: cty.NewValueMarks(marks.Sensitive),
		}}
		return obj
	}()

	tests := map[string]*states.ResourceInstanceObjectSrc{
		"unmarked value": baseFixtureObj(),
		"sensitive-only value not rejected by the guard": sensitiveOnly,
	}
	for name, obj := range tests {
		t.Run(name, func(t *testing.T) {
			file := fixtureFile(addrs.ManagedResourceMode, map[addrs.InstanceKey]*states.ResourceInstanceObjectSrc{addrs.NoKey: obj}, nil)
			var buf bytes.Buffer
			if err := Write(file, &buf, encryption.StateEncryptionDisabled()); err != nil {
				t.Fatalf("unexpected error:\n%s", err)
			}
			checkValidOutput(t, buf.Bytes())
		})
	}
}

// A genuine ephemeral resource is intentionally absent from the state file:
// even one carrying real ephemeral marks must leave no structural entry and
// no payload in the serialized output, while ordinary managed sibling
// resources in the same state remain round-trippable.
func TestVersion4EphemeralResourceRemainsOmitted(t *testing.T) {
	state := states.NewState()
	providerConfig := addrs.AbsProviderConfig{
		Provider: addrs.NewDefaultProvider("test"),
		Module:   addrs.RootModule,
	}
	ephAddr := addrs.Resource{Mode: addrs.EphemeralResourceMode, Type: "test_instance", Name: "foo"}
	mgdAddr := addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "test_instance", Name: "bar"}

	ephObj := ephMarkedObj(baseFixtureObj(), cty.GetAttrPath("whatever"))
	ephObj.AttrsJSON = []byte(`{"id":"ephemeral-secret"}`)
	state.RootModule().SetResourceProvider(ephAddr, providerConfig)
	state.RootModule().SetResourceInstanceCurrent(ephAddr.Instance(addrs.NoKey), ephObj, providerConfig, addrs.NoKey)

	state.RootModule().SetResourceProvider(mgdAddr, providerConfig)
	state.RootModule().SetResourceInstanceCurrent(mgdAddr.Instance(addrs.NoKey), baseFixtureObj(), providerConfig, addrs.NoKey)

	var buf bytes.Buffer
	if err := Write(&File{Serial: 1, Lineage: "test-lineage", State: state}, &buf, encryption.StateEncryptionDisabled()); err != nil {
		t.Fatalf("unexpected error:\n%s", err)
	}

	// Structural check of the real serialized JSON: the resources array
	// contains exactly the managed sibling, with no ephemeral entry.
	var doc struct {
		Resources []struct {
			Mode string `json:"mode"`
			Type string `json:"type"`
			Name string `json:"name"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("output is not valid JSON:\n%s", err)
	}
	if len(doc.Resources) != 1 {
		t.Fatalf("expected exactly 1 serialized resource entry, got %d: %#v", len(doc.Resources), doc.Resources)
	}
	res := doc.Resources[0]
	if res.Mode != "managed" || res.Type != "test_instance" || res.Name != "bar" {
		t.Errorf("unexpected serialized resource entry: mode=%q type=%q name=%q", res.Mode, res.Type, res.Name)
	}

	// No ephemeral payload may leak into the bytes.
	if strings.Contains(buf.String(), "ephemeral-secret") {
		t.Errorf("ephemeral resource payload leaked into the state file:\n%s", buf.String())
	}

	// The managed sibling remains round-trippable, and nothing carrying the
	// ephemeral address survives the read.
	reFile, diags := readStateV4(buf.Bytes())
	if diags.HasErrors() {
		t.Fatalf("written output is not re-readable:\n%s", diags.Err())
	}
	if is := reFile.State.RootModule().ResourceInstance(mgdAddr.Instance(addrs.NoKey)); is == nil || !is.HasCurrent() || string(is.Current.AttrsJSON) != `{"id":"x"}` {
		t.Errorf("managed sibling did not round-trip (missing current object or corrupted attributes)")
	}
	if rs := reFile.State.RootModule().Resource(ephAddr); rs != nil {
		t.Errorf("ephemeral resource address survived in the re-read state: %#v", rs)
	}
}
