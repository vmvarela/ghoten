#!/usr/bin/env python3
from pathlib import Path
import re
import sys

root = Path(sys.argv[1])
args_dir = root / "internal/command/arguments"

# Existing upstream table tests construct Operation values without RefreshMode.
# Update only the canonical PlanMode/Parallelism/Refresh triplets.
for name in ("apply_test.go", "plan_test.go"):
    path = args_dir / name
    text = path.read_text()
    normal = re.compile(
        r"(\t+PlanMode:\s+plans\.NormalMode,\n"
        r"\t+Parallelism:\s+10,\n"
        r"\t+Refresh:\s+true,)(?!\n\t+RefreshMode:)"
    )
    destroy = re.compile(
        r"(\t+PlanMode:\s+plans\.DestroyMode,\n"
        r"\t+Parallelism:\s+10,\n"
        r"\t+Refresh:\s+true,)(?!\n\t+RefreshMode:)"
    )
    text, n_normal = normal.subn(r"\1\n\t\t\t\t\tRefreshMode: plans.RefreshSmart,", text)
    text, n_destroy = destroy.subn(r"\1\n\t\t\t\t\tRefreshMode: plans.RefreshAll,", text)
    if n_normal == 0:
        raise SystemExit(f"{name}: no normal-mode expectations updated")
    if n_destroy == 0:
        raise SystemExit(f"{name}: no destroy-mode expectations updated")
    path.write_text(text)

(args_dir / "ghoten_smart_refresh_test.go").write_text(r'''package arguments

import (
    "testing"

    "github.com/opentofu/opentofu/internal/plans"
)

func TestGhotenRefreshModes(t *testing.T) {
    tests := map[string]struct {
        args []string
        want plans.RefreshMode
    }{
        "default smart": {nil, plans.RefreshSmart},
        "full":          {[]string{"-refresh=true"}, plans.RefreshAll},
        "none":          {[]string{"-refresh=false"}, plans.RefreshNone},
        "smart":         {[]string{"-refresh=smart"}, plans.RefreshSmart},
        "destroy default full": {[]string{"-destroy"}, plans.RefreshAll},
        "refresh-only default full": {[]string{"-refresh-only"}, plans.RefreshAll},
    }

    for name, tc := range tests {
        t.Run(name, func(t *testing.T) {
            got, _, diags := ParsePlan(tc.args)
            if diags.HasErrors() {
                t.Fatalf("unexpected diagnostics: %v", diags.Err())
            }
            if got.Operation.RefreshMode != tc.want {
                t.Fatalf("refresh mode = %s, want %s", got.Operation.RefreshMode.UIName(), tc.want.UIName())
            }
        })
    }
}

func TestGhotenExplicitSmartRejectsUnsupportedPlanModes(t *testing.T) {
    for _, args := range [][]string{
        {"-destroy", "-refresh=smart"},
        {"-refresh-only", "-refresh=smart"},
    } {
        _, _, diags := ParsePlan(args)
        if !diags.HasErrors() {
            t.Fatalf("expected %v to be rejected", args)
        }
    }
}
''')
