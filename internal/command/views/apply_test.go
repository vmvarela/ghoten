// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package views

import (
	"fmt"
	"strings"
	"testing"

	"github.com/vmvarela/ghoten/internal/command/arguments"
	"github.com/vmvarela/ghoten/internal/lang/marks"
	"github.com/vmvarela/ghoten/internal/plans"
	"github.com/vmvarela/ghoten/internal/states"
	"github.com/vmvarela/ghoten/internal/terminal"
	"github.com/zclconf/go-cty/cty"
)

// This test is mostly because I am paranoid about having two consecutive
// boolean arguments.
func TestApply_new(t *testing.T) {
	streams, done := terminal.StreamsForTesting(t)
	defer done(t)
	v := NewApply(arguments.ViewOptions{ViewType: arguments.ViewHuman}, false, NewView(streams).SetRunningInAutomation(true))
	hv, ok := v.(*ApplyHuman)
	if !ok {
		t.Fatalf("unexpected return type %t", v)
	}

	if hv.destroy != false {
		t.Fatalf("unexpected destroy value")
	}

	if hv.inAutomation != true {
		t.Fatalf("unexpected inAutomation value")
	}
}

// Basic test coverage of Outputs, since most of its functionality is tested
// elsewhere.
func TestApplyHuman_outputs(t *testing.T) {
	streams, done := terminal.StreamsForTesting(t)
	v := NewApply(arguments.ViewOptions{ViewType: arguments.ViewHuman}, false, NewView(streams))

	v.Outputs(map[string]*states.OutputValue{
		"foo": {Value: cty.StringVal("secret")},
	})

	got := done(t).Stdout()
	for _, want := range []string{"Outputs:", `foo = "secret"`} {
		if !strings.Contains(got, want) {
			t.Errorf("wrong result\ngot:  %q\nwant: %q", got, want)
		}
	}
}

// Outputs should do nothing if there are no outputs to render.
func TestApplyHuman_outputsEmpty(t *testing.T) {
	streams, done := terminal.StreamsForTesting(t)
	v := NewApply(arguments.ViewOptions{ViewType: arguments.ViewHuman}, false, NewView(streams))

	v.Outputs(map[string]*states.OutputValue{})

	got := done(t).Stdout()
	if got != "" {
		t.Errorf("output should be empty, but got: %q", got)
	}
}

// Ensure that the correct view type and in-automation settings propagate to the
// Operation view.
func TestApplyHuman_operation(t *testing.T) {
	streams, done := terminal.StreamsForTesting(t)
	defer done(t)
	v := NewApply(arguments.ViewOptions{ViewType: arguments.ViewHuman}, false, NewView(streams).SetRunningInAutomation(true)).Operation()
	if hv, ok := v.(*OperationHuman); !ok {
		t.Fatalf("unexpected return type %t", v)
	} else if hv.inAutomation != true {
		t.Fatalf("unexpected inAutomation value on Operation view")
	}
}

// This view is used for both apply and destroy commands, so the help output
// needs to cover both.
func TestApplyHuman_help(t *testing.T) {
	testCases := map[string]bool{
		"apply":   false,
		"destroy": true,
	}

	for name, destroy := range testCases {
		t.Run(name, func(t *testing.T) {
			streams, done := terminal.StreamsForTesting(t)
			v := NewApply(arguments.ViewOptions{ViewType: arguments.ViewHuman}, destroy, NewView(streams))
			v.HelpPrompt()
			got := done(t).Stderr()
			if !strings.Contains(got, name) {
				t.Errorf("wrong result\ngot:  %q\nwant: %q", got, name)
			}
		})
	}
}

// Hooks and ResourceCount are tangled up and easiest to test together.
func TestApply_resourceCount(t *testing.T) {
	testCases := map[string]struct {
		destroy    bool
		want       string
		importing  bool
		forgetting bool
	}{
		"apply": {
			false,
			"Apply complete! Resources: 1 added, 2 changed, 3 destroyed.",
			false,
			false,
		},
		"destroy": {
			true,
			"Destroy complete! Resources: 3 destroyed.",
			false,
			false,
		},
		"import": {
			false,
			"Apply complete! Resources: 1 imported, 1 added, 2 changed, 3 destroyed.",
			true,
			false,
		},
		"forget": {
			false,
			"Apply complete! Resources: 1 added, 2 changed, 3 destroyed, 1 forgotten.",
			false,
			true,
		},
	}

	// For compatibility reasons, these tests should hold true for both human
	// and JSON output modes
	views := []arguments.ViewType{arguments.ViewHuman, arguments.ViewJSON}

	for name, tc := range testCases {
		for _, viewType := range views {
			t.Run(fmt.Sprintf("%s (%s view)", name, viewType), func(t *testing.T) {
				streams, done := terminal.StreamsForTesting(t)
				v := NewApply(arguments.ViewOptions{ViewType: viewType}, tc.destroy, NewView(streams))
				hooks := v.Hooks()

				var count *countHook
				for _, hook := range hooks {
					if ch, ok := hook.(*countHook); ok {
						count = ch
					}
				}
				if count == nil {
					t.Fatalf("expected Hooks to include a countHook: %#v", hooks)
				}

				count.Added = 1
				count.Changed = 2
				count.Removed = 3

				if tc.importing {
					count.Imported = 1
				}

				if tc.forgetting {
					count.Forgotten = 1
				}

				v.ResourceCount("")

				got := done(t).Stdout()
				if !strings.Contains(got, tc.want) {
					t.Errorf("wrong result\ngot:  %q\nwant: %q", got, tc.want)
				}
			})
		}
	}
}

func TestApplyHuman_resourceCountStatePath(t *testing.T) {
	testCases := map[string]struct {
		added        int
		changed      int
		removed      int
		statePath    string
		wantContains bool
	}{
		"default state path": {
			added:        1,
			changed:      2,
			removed:      3,
			statePath:    "",
			wantContains: false,
		},
		"only removed": {
			added:        0,
			changed:      0,
			removed:      5,
			statePath:    "foo.tfstate",
			wantContains: false,
		},
		"added": {
			added:        5,
			changed:      0,
			removed:      0,
			statePath:    "foo.tfstate",
			wantContains: true,
		},
		"changed": {
			added:        0,
			changed:      5,
			removed:      0,
			statePath:    "foo.tfstate",
			wantContains: true,
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			streams, done := terminal.StreamsForTesting(t)
			v := NewApply(arguments.ViewOptions{ViewType: arguments.ViewHuman}, false, NewView(streams))
			hooks := v.Hooks()

			var count *countHook
			for _, hook := range hooks {
				if ch, ok := hook.(*countHook); ok {
					count = ch
				}
			}
			if count == nil {
				t.Fatalf("expected Hooks to include a countHook: %#v", hooks)
			}

			count.Added = tc.added
			count.Changed = tc.changed
			count.Removed = tc.removed

			v.ResourceCount(tc.statePath)

			got := done(t).Stdout()
			want := "State path: " + tc.statePath
			contains := strings.Contains(got, want)
			if contains && !tc.wantContains {
				t.Errorf("wrong result\ngot:  %q\nshould not contain: %q", got, want)
			} else if !contains && tc.wantContains {
				t.Errorf("wrong result\ngot:  %q\nshould contain: %q", got, want)
			}
		})
	}
}

func TestApplyHuman_resourceCountSmartRefresh(t *testing.T) {
	testCases := []struct {
		name         string
		refreshed    int
		skipped      int
		wantContains string
		wantMissing  string
	}{
		{
			name:         "all skipped",
			refreshed:    0,
			skipped:      5,
			wantContains: "Refresh: 0 resources refreshed, 5 skipped (smart mode).",
		},
		{
			name:         "mixed",
			refreshed:    2,
			skipped:      3,
			wantContains: "Refresh: 2 resources refreshed, 3 skipped (smart mode).",
		},
		{
			name:         "all refreshed",
			refreshed:    4,
			skipped:      0,
			wantContains: "Refresh: 4 resources refreshed, 0 skipped (smart mode).",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			streams, done := terminal.StreamsForTesting(t)
			v := NewApply(arguments.ViewOptions{ViewType: arguments.ViewHuman}, false, NewView(streams), plans.RefreshSmart)
			hooks := v.Hooks()

			var count *countHook
			for _, hook := range hooks {
				if ch, ok := hook.(*countHook); ok {
					count = ch
				}
			}
			if count == nil {
				t.Fatalf("expected Hooks to include a countHook: %#v", hooks)
			}

			count.Added = 1
			count.Refreshed = tc.refreshed
			count.RefreshSkipped = tc.skipped

			v.ResourceCount("")

			got := done(t).Stdout()
			if !strings.Contains(got, tc.wantContains) {
				t.Errorf("wrong result\ngot:  %q\nwant to contain: %q", got, tc.wantContains)
			}
		})
	}
}

// TestApplyHuman_resourceCountNoSmartRefresh verifies that the refresh line is
// not emitted when smart refresh mode is not active.
func TestApplyHuman_resourceCountNoSmartRefresh(t *testing.T) {
	streams, done := terminal.StreamsForTesting(t)
	v := NewApply(arguments.ViewOptions{ViewType: arguments.ViewHuman}, false, NewView(streams), plans.RefreshAll)
	hooks := v.Hooks()

	var count *countHook
	for _, hook := range hooks {
		if ch, ok := hook.(*countHook); ok {
			count = ch
		}
	}
	if count == nil {
		t.Fatalf("expected Hooks to include a countHook: %#v", hooks)
	}

	count.Added = 1
	count.Refreshed = 3

	v.ResourceCount("")

	got := done(t).Stdout()
	if strings.Contains(got, "smart mode") {
		t.Errorf("should not contain smart mode output when RefreshAll: %q", got)
	}
}

// TestApplyJSON_resourceCountSmartRefresh verifies that the JSON change summary
// includes smart_refresh, refreshed, and refresh_skipped fields when active.
func TestApplyJSON_resourceCountSmartRefresh(t *testing.T) {
	streams, done := terminal.StreamsForTesting(t)
	v := NewApply(arguments.ViewOptions{ViewType: arguments.ViewJSON}, false, NewView(streams), plans.RefreshSmart)
	hooks := v.Hooks()

	var count *countHook
	for _, hook := range hooks {
		if ch, ok := hook.(*countHook); ok {
			count = ch
		}
	}
	if count == nil {
		t.Fatalf("expected Hooks to include a countHook: %#v", hooks)
	}

	count.Added = 1
	count.Refreshed = 2
	count.RefreshSkipped = 3

	v.ResourceCount("")

	want := []map[string]interface{}{
		{
			"@level":   "info",
			"@message": "Apply complete! Resources: 1 added, 0 changed, 0 destroyed.",
			"@module":  "ghoten.ui",
			"type":     "change_summary",
			"changes": map[string]interface{}{
				"add":             float64(1),
				"change":          float64(0),
				"remove":          float64(0),
				"import":          float64(0),
				"forget":          float64(0),
				"operation":       "apply",
				"smart_refresh":   true,
				"refreshed":       float64(2),
				"refresh_skipped": float64(3),
			},
		},
	}
	testJSONViewOutputEquals(t, done(t).Stdout(), want)
}

// TestApplyJSON_resourceCountNoSmartRefresh verifies that the JSON change summary
// does NOT include smart_refresh fields when mode is not smart.
func TestApplyJSON_resourceCountNoSmartRefresh(t *testing.T) {
	streams, done := terminal.StreamsForTesting(t)
	v := NewApply(arguments.ViewOptions{ViewType: arguments.ViewJSON}, false, NewView(streams), plans.RefreshAll)
	hooks := v.Hooks()

	var count *countHook
	for _, hook := range hooks {
		if ch, ok := hook.(*countHook); ok {
			count = ch
		}
	}
	if count == nil {
		t.Fatalf("expected Hooks to include a countHook: %#v", hooks)
	}

	count.Added = 1
	count.Changed = 2
	count.Removed = 3

	v.ResourceCount("")

	want := []map[string]interface{}{
		{
			"@level":   "info",
			"@message": "Apply complete! Resources: 1 added, 2 changed, 3 destroyed.",
			"@module":  "ghoten.ui",
			"type":     "change_summary",
			"changes": map[string]interface{}{
				"add":       float64(1),
				"change":    float64(2),
				"remove":    float64(3),
				"import":    float64(0),
				"forget":    float64(0),
				"operation": "apply",
			},
		},
	}
	testJSONViewOutputEquals(t, done(t).Stdout(), want)
}

// Basic test coverage of Outputs, since most of its functionality is tested
// elsewhere.
func TestApplyJSON_outputs(t *testing.T) {
	streams, done := terminal.StreamsForTesting(t)
	v := NewApply(arguments.ViewOptions{ViewType: arguments.ViewJSON}, false, NewView(streams))

	v.Outputs(map[string]*states.OutputValue{
		"boop_count": {Value: cty.NumberIntVal(92)},
		"password":   {Value: cty.StringVal("horse-battery").Mark(marks.Sensitive), Sensitive: true},
	})

	want := []map[string]interface{}{
		{
			"@level":   "info",
			"@message": "Outputs: 2",
			"@module":  "ghoten.ui",
			"type":     "outputs",
			"outputs": map[string]interface{}{
				"boop_count": map[string]interface{}{
					"sensitive": false,
					"value":     float64(92),
					"type":      "number",
				},
				"password": map[string]interface{}{
					"sensitive": true,
					"type":      "string",
				},
			},
		},
	}
	testJSONViewOutputEquals(t, done(t).Stdout(), want)
}
