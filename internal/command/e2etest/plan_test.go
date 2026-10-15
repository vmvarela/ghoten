// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package e2etest

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/vmvarela/ghoten/internal/addrs"
	"github.com/vmvarela/ghoten/internal/e2e"
	"github.com/vmvarela/ghoten/internal/plans"
)

// The tests in this file are for the following sequence:
// tofu init
// tofu plan

func TestPlanConsolidatedWarningsForDeprecatedMarks(t *testing.T) {
	t.Parallel()

	implicitFixturePath := filepath.Join("testdata", "consolidated-warnings-for-deprecated-marks")
	tf := e2e.NewBinary(t, ghotenBin, implicitFixturePath)

	t.Run("consolidated warnings for deprecated marks", func(t *testing.T) {
		_, initErr, err := tf.Run("init")
		if err != nil {
			t.Fatalf("expected no errors on init, got error %v: %s", err, initErr)
		}

		planStdout, planErr, err := tf.Run("plan")
		if err != nil {
			t.Fatalf("expected no errors on plan, got error %v: %s", err, planErr)
		}

		expectedOutput := `
No changes. Your infrastructure matches the configuration.

Ghoten has compared your real infrastructure against your configuration and
found no differences, so no changes are needed.
╷
│ Warning: Variable marked as deprecated by the module author
│ 
│   on main.tf line 3, in module "call":
│    3:   input  = "test"
│ 
│ Variable "input" is marked as deprecated with the following message:
│ this is local deprecated
│ 
│ (and one more similar warning elsewhere)
╵
╷
│ Warning: Variable marked as deprecated by the module author
│ 
│   on main.tf line 4, in module "call":
│    4:   input2 = "test2"
│ 
│ Variable "input2" is marked as deprecated with the following message:
│ this is local deprecated2
│ 
│ (and one more similar warning elsewhere)
╵
╷
│ Warning: Value derived from a deprecated source
│ 
│   on main.tf line 14, in locals:
│   14:   i1 = module.call.modout1
│ 
│ This value is derived from module.call.modout1, which is deprecated with
│ the following message:
│ 
│ output deprecated
│ 
│ (and 2 more similar warnings elsewhere)
╵
╷
│ Warning: Value derived from a deprecated source
│ 
│   on main.tf line 15, in locals:
│   15:   i2 = module.call.modout2
│ 
│ This value is derived from module.call.modout2, which is deprecated with
│ the following message:
│ 
│ output deprecated
│ 
│ (and 2 more similar warnings elsewhere)
╵
`
		if diff := cmp.Diff(strings.TrimSpace(stripAnsi(planStdout)), strings.TrimSpace(stripAnsi(expectedOutput))); diff != "" {
			t.Errorf("wrong output.\n%s", diff)
		}
	})
}

func TestPlanOnDeprecated(t *testing.T) {
	t.Parallel()

	fixturePath := filepath.Join("testdata", "deprecated-values")
	tf := e2e.NewBinary(t, ghotenBin, fixturePath)

	//// INIT
	_, stderr, err := tf.Run("init", "-input=false")
	if err != nil {
		t.Fatalf("unexpected init error: %s\nstderr:\n%s", err, stderr)
	}

	//// PLAN
	stdout, stderr, err := tf.Run("plan")
	if err != nil {
		t.Fatalf("unexpected plan error: %s\nstderr:\n%s", err, stderr)
	}

	expected := []string{
		`Variable marked as deprecated by the module author`,
		`Variable "input" is marked as deprecated with the following message`,
		`This var is deprecated`,
		`Value derived from a deprecated source`,
		`This value is derived from module.call.output, which is deprecated with the`,
		`following message:`,
		`this output is deprecated`,
	}
	for _, want := range expected {
		if !strings.Contains(stdout, want) {
			t.Errorf("invalid plan output. expected to contain %q but it does not:\n%s", want, stdout)
		}
	}
}

func TestPlanOnMultipleDeprecatedMarksSliceBug(t *testing.T) {
	t.Parallel()

	// Test for [the bug](https://github.com/vmvarela/ghoten/issues/3104) where modifying
	// pathMarks slice during iteration would cause slice bounds errors when multiple
	// deprecated marks exist
	fixturePath := filepath.Join("testdata", "multiple-deprecated-marks-slice-bug")
	tf := e2e.NewBinary(t, ghotenBin, fixturePath)

	t.Run("multiple deprecated marks slice bug", func(t *testing.T) {
		_, initErr, err := tf.Run("init")
		if err != nil {
			t.Fatalf("expected no errors on init, got error %v: %s", err, initErr)
		}

		planStdout, planErr, err := tf.Run("plan")
		if err != nil {
			t.Fatalf("expected no errors on plan, got error %v: %s", err, planErr)
		}

		// Should not crash and should show deprecation warnings for all outputs
		expectedContents := []string{
			"Changes to Outputs:",
			"trigger = {",
			"Value derived from a deprecated source",
			"Use new_out1",
			"Use new_out2",
			"Use new_out3",
		}

		// Strip ANSI codes for consistent testing
		cleanOutput := stripAnsi(planStdout)
		for _, want := range expectedContents {
			if !strings.Contains(cleanOutput, want) {
				t.Errorf("plan output missing expected content %q:\n%s", want, cleanOutput)
			}
		}
	})
}

// TestPlanShowWithEmbeddedSchema is a regression test for showing a plan of
// a configuration containing an ephemeral resource: the schemas stored in the
// plan file must be enough for "show" to render the plan, even when the
// provider cannot be launched anymore.
func TestPlanShowWithEmbeddedSchema(t *testing.T) {
	t.Parallel()

	fixturePath := filepath.Join("testdata", "plan-with-embedded-ephemeral-config")
	tf := e2e.NewBinary(t, ghotenBin, fixturePath)
	buildSimpleProvider(t, "6", tf.WorkDir(), "simple")

	{ // INIT
		_, stderr, err := tf.Run("init", "-plugin-dir=cache")
		if err != nil {
			t.Fatalf("unexpected init error: %s\nstderr:\n%s", err, stderr)
		}
	}

	{ // PLAN
		stdout, stderr, err := tf.Run("plan", "-out=tfplan")
		if err != nil {
			t.Fatalf("unexpected plan error: %s\nstderr:\n%s", err, stderr)
		}
		checker := outputEntriesChecker{phase: "plan"}
		checker.addChecks(
			outputEntry{[]string{"ephemeral.simple_resource.test_ephemeral: Opening..."}, true},
			outputEntry{[]string{"ephemeral.simple_resource.test_ephemeral: Open complete after"}, true},
			outputEntry{[]string{"ephemeral.simple_resource.test_ephemeral: Closing..."}, true},
			outputEntry{[]string{"ephemeral.simple_resource.test_ephemeral: Close complete after"}, true},
			outputEntry{[]string{`+ resource "simple_resource" "test_res"`}, true},
		)
		out := stripAnsi(stdout)
		checker.check(t, out)

		// assert plan file content
		plan, err := tf.Plan("tfplan")
		if err != nil {
			t.Fatalf("failed to read the plan file: %s", err)
		}
		idx := slices.IndexFunc(plan.Changes.Resources, func(src *plans.ResourceInstanceChangeSrc) bool {
			return src.Addr.Resource.Resource.Mode == addrs.EphemeralResourceMode
		})
		if idx < 0 {
			t.Fatalf("no ephemeral resource found in the plan file")
		}
		res := plan.Changes.Resources[idx]
		if res.Before != nil {
			t.Errorf("ephemeral resource %q from plan contains before value but it shouldn't: %s", res.Addr.String(), res.Before)
		}
		if res.After != nil {
			t.Errorf("ephemeral resource %q from plan contains after value but it shouldn't: %s", res.Addr.String(), res.After)
		}
		if got, want := res.Action, plans.Open; got != want {
			t.Errorf("ephemeral resource %q from plan contains wrong actions. want %q; got %q", res.Addr.String(), want, got)
		}
	}

	// Break the installed provider so "show" could not launch it and can
	// only succeed through the schemas embedded in the plan file.
	if err := os.RemoveAll(tf.Path(".terraform", "providers")); err != nil {
		t.Fatalf("failed to remove installed provider: %s", err)
	}

	{ // SHOW -json
		stdout, stderr, err := tf.Run("show", "-json", "tfplan")
		if err != nil {
			t.Fatalf("unexpected show -json error: %s\nstderr:\n%s", err, stderr)
		}
		checker := outputEntriesChecker{phase: "show -json"}
		checker.addChecks(
			outputEntry{[]string{`{"address":"ephemeral.simple_resource.test_ephemeral","mode":"ephemeral","type":"simple_resource","name":"test_ephemeral","provider_config_key":"simple"`}, true},
		)
		out := stripAnsi(stdout)
		checker.check(t, out)
	}
	{ // SHOW (human)
		stdout, stderr, err := tf.Run("show", "tfplan")
		if err != nil {
			t.Fatalf("unexpected show error: %s\nstderr:\n%s", err, stderr)
		}
		checker := outputEntriesChecker{phase: "show"}
		checker.addChecks(
			outputEntry{[]string{`+ resource "simple_resource" "test_res"`}, true},
		)
		out := stripAnsi(stdout)
		checker.check(t, out)
	}
}
