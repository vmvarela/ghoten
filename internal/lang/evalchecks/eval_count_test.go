// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0
package evalchecks

import (
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/davecgh/go-spew/spew"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hcltest"
	"github.com/vmvarela/ghoten/internal/addrs"
	"github.com/vmvarela/ghoten/internal/lang/marks"
	"github.com/vmvarela/ghoten/internal/tfdiags"
	"github.com/zclconf/go-cty/cty"
)

// returns a mock ref function for the unit tests
func mockEvaluateFunc(val cty.Value) EvaluateFunc {
	return func(_ hcl.Expression) (cty.Value, tfdiags.Diagnostics) {
		return val, tfdiags.Diagnostics{}
	}
}

func TestEvaluateCountExpression_valid(t *testing.T) {
	tests := map[string]struct {
		val      cty.Value
		expected int
	}{
		"1": {
			cty.NumberIntVal(1),
			1,
		},
		"MaxInt32": {
			cty.NumberIntVal(math.MaxInt32),
			math.MaxInt32,
		},
		"sensitive value": {
			cty.NumberIntVal(1).Mark(marks.Sensitive),
			1,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			actual, diags := EvaluateCountExpression(hcltest.MockExprLiteral(test.val), mockEvaluateFunc(test.val), nil)

			if len(diags) != 0 {
				t.Errorf("unexpected diagnostics %s", spew.Sdump(diags))
			}

			if !reflect.DeepEqual(actual, test.expected) {
				t.Errorf(
					"wrong map value\ngot:  %swant: %s",
					spew.Sdump(actual), spew.Sdump(test.expected),
				)
			}
		})
	}
}

func TestEvaluateCountExpression_errors(t *testing.T) {
	tests := map[string]struct {
		val                      cty.Value
		excludableAddr           addrs.Targetable
		Summary, DetailSubstring string
		CausedByUnknown          bool
		CausedByConfidential     bool
	}{
		"null": {
			cty.NullVal(cty.Number),
			nil,
			"Invalid count argument",
			`The given "count" argument value is null. An integer is required.`,
			false,
			false,
		},
		"negative": {
			cty.NumberIntVal(-1),
			nil,
			"Invalid count argument",
			`The given "count" argument value is unsuitable: must be greater than or equal to zero.`,
			false,
			false,
		},
		"string": {
			cty.StringVal("i am definitely a number"),
			nil,
			"Invalid count argument",
			"The given \"count\" argument value is unsuitable: number value is required.",
			false,
			false,
		},
		"list": {
			cty.ListVal([]cty.Value{cty.StringVal("a"), cty.StringVal("a")}),
			nil,
			"Invalid count argument",
			"The given \"count\" argument value is unsuitable: number value is required.",
			false,
			false,
		},
		"tuple": {
			cty.TupleVal([]cty.Value{cty.StringVal("a"), cty.StringVal("b")}),
			nil,
			"Invalid count argument",
			"The given \"count\" argument value is unsuitable: number value is required.",
			false,
			false,
		},
		"unknown": {
			cty.UnknownVal(cty.Number),
			nil,
			"Invalid count argument",
			"The \"count\" value depends on resource attributes that cannot be determined until apply, so Ghoten cannot predict how many instances will be created.\n\nTo work around this, use the -target option to first apply only the resources that the count depends on, and then apply normally to converge.",
			true,
			false,
		},
		"unknown with excludable address": {
			cty.UnknownVal(cty.Number),
			addrs.Resource{
				Mode: addrs.ManagedResourceMode,
				Name: "foo",
				Type: "bar",
			}.Absolute(addrs.RootModuleInstance.Child("a", addrs.NoKey)),
			"Invalid count argument",
			"The \"count\" value depends on resource attributes that cannot be determined until apply, so Ghoten cannot predict how many instances will be created.\n\nTo work around this, use the planning option -exclude=module.a.bar.foo to first apply without this object, and then apply normally to converge.",
			true,
			false,
		},
		"ephemeral value": {
			cty.NumberIntVal(1).Mark(marks.Ephemeral),
			nil,
			"Invalid count argument",
			"Ephemeral values, or values derived from ephemeral values, cannot be used as count arguments. If used, the ephemeral value could be exposed as a resource instance key.",
			false,
			true,
		},
		"ephemeral unknown value": {
			cty.UnknownVal(cty.Number).Mark(marks.Ephemeral),
			nil,
			"Invalid count argument",
			"Ephemeral values, or values derived from ephemeral values, cannot be used as count arguments.",
			false,
			true,
		},
		"ephemeral null value": {
			cty.NullVal(cty.Number).Mark(marks.Ephemeral),
			nil,
			"Invalid count argument",
			"Ephemeral values, or values derived from ephemeral values, cannot be used as count arguments.",
			false,
			true,
		},
		"too large": {
			cty.NumberIntVal(math.MaxInt32 + 1),
			nil,
			"Invalid count argument",
			`The given "count" argument value is unsuitable: must be less than or equal to 2147483647.`,
			false,
			false,
		},
		"ephemeral value with too-large precedence": {
			cty.NumberIntVal(math.MaxInt32 + 1).Mark(marks.Ephemeral),
			nil,
			"Invalid count argument",
			"Ephemeral values, or values derived from ephemeral values, cannot be used as count arguments.",
			false,
			true,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			_, diags := EvaluateCountExpression(hcltest.MockExprLiteral(test.val), mockEvaluateFunc(test.val), test.excludableAddr)

			if len(diags) != 1 {
				t.Fatalf("got %d diagnostics; want 1", diags)
			}
			if got, want := diags[0].Severity(), tfdiags.Error; got != want {
				t.Errorf("wrong diagnostic severity %#v; want %#v", got, want)
			}
			if got, want := diags[0].Description().Summary, test.Summary; got != want {
				t.Errorf("wrong diagnostic summary\ngot:  %s\nwant: %s", got, want)
			}
			if got, want := diags[0].Description().Detail, test.DetailSubstring; !strings.Contains(got, want) {
				t.Errorf("wrong diagnostic detail\ngot: %s\nwant substring: %s", got, want)
			}

			if got, want := tfdiags.DiagnosticCausedByUnknown(diags[0]), test.CausedByUnknown; got != want {
				t.Errorf("wrong result from tfdiags.DiagnosticCausedByUnknown\ngot:  %#v\nwant: %#v", got, want)
			}
			if got, want := tfdiags.DiagnosticCausedByConfidentialValues(diags[0]), test.CausedByConfidential; got != want {
				t.Errorf("wrong result from tfdiags.DiagnosticCausedByConfidentialValues\ngot:  %#v\nwant: %#v", got, want)
			}
		})
	}
}

func TestCountCommandLineExcludeSuggestion(t *testing.T) {
	noKeyResourceAddr := addrs.Resource{
		Mode: addrs.ManagedResourceMode,
		Type: "happycloud_virtual_machine",
		Name: "boop",
	}.Absolute(addrs.RootModuleInstance)
	stringKeyResourceAddr := addrs.Resource{
		Mode: addrs.ManagedResourceMode,
		Type: "happycloud_virtual_machine",
		Name: "boop",
	}.Absolute(addrs.RootModuleInstance.Child("foo", addrs.StringKey("bleep bloop")))

	tests := []struct {
		excludableAddr addrs.Targetable
		goos           string
		want           string
	}{
		{
			nil,
			"linux",
			`To work around this, use the -target option to first apply only the resources that the count depends on, and then apply normally to converge.`,
		},
		{
			nil,
			"windows",
			`To work around this, use the -target option to first apply only the resources that the count depends on, and then apply normally to converge.`,
		},
		{
			nil,
			"darwin",
			`To work around this, use the -target option to first apply only the resources that the count depends on, and then apply normally to converge.`,
		},
		{
			noKeyResourceAddr,
			"linux",
			`To work around this, use the planning option -exclude=happycloud_virtual_machine.boop to first apply without this object, and then apply normally to converge.`,
		},
		{
			noKeyResourceAddr,
			"windows",
			`To work around this, use the planning option -exclude=happycloud_virtual_machine.boop to first apply without this object, and then apply normally to converge.`,
		},
		{
			noKeyResourceAddr,
			"darwin",
			`To work around this, use the planning option -exclude=happycloud_virtual_machine.boop to first apply without this object, and then apply normally to converge.`,
		},
		{
			stringKeyResourceAddr,
			"linux",
			`To work around this, use the planning option -exclude='module.foo["bleep bloop"].happycloud_virtual_machine.boop' to first apply without this object, and then apply normally to converge.`,
		},
		{
			stringKeyResourceAddr,
			"windows",
			`To work around this, use the planning option -exclude="module.foo[\"bleep bloop\"].happycloud_virtual_machine.boop" to first apply without this object, and then apply normally to converge.`,
		},
		{
			stringKeyResourceAddr,
			"darwin",
			`To work around this, use the planning option -exclude='module.foo["bleep bloop"].happycloud_virtual_machine.boop' to first apply without this object, and then apply normally to converge.`,
		},
	}

	for _, test := range tests {
		t.Run(fmt.Sprintf("%v on %s", test.excludableAddr, test.goos), func(t *testing.T) {
			got := countCommandLineExcludeSuggestionImpl(test.excludableAddr, test.goos)
			if got != test.want {
				t.Errorf("wrong suggestion\ngot:  %s\nwant: %s", got, test.want)
			}
		})
	}
}
