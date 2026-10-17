#!/usr/bin/env python3
from pathlib import Path
import re
import sys

root = Path(sys.argv[1])

def edit(rel, fn):
    path = root / rel
    text = path.read_text()
    new = fn(text)
    if new == text:
        raise SystemExit(f"{rel}: compatibility edit made no change")
    path.write_text(new)

def replace_once(text, old, new, rel):
    if old not in text:
        raise SystemExit(f"{rel}: expected pattern not found: {old!r}")
    return text.replace(old, new, 1)

# Current OpenTofu CLI plumbing: retain its CommandLine/Bind* implementation and
# add only Smart Refresh mode parsing.
rel = "internal/command/arguments/extended.go"
def extended(text):
    text = replace_once(text,
        "\tRefresh bool\n\n\t// Targets",
        "\tRefresh bool\n\n\t// RefreshMode selects all/none/smart remote-state refresh.\n\tRefreshMode plans.RefreshMode\n\trefreshSet  bool\n\n\t// Targets", rel)
    text = replace_once(text,
        "\tvar destroyRaw bool\n\tvar refreshOnlyRaw bool",
        "\tvar destroyRaw bool\n\tvar refreshOnlyRaw bool\n\tvar refreshRaw string", rel)
    pattern = re.compile(r'\tcli\.BoolVar\(&o\.Refresh, "refresh", true,\n.*?\n\t\)\.SetDisplay\("=false"\)', re.S)
    repl = '\trefreshFlag := cli.StringVar(&refreshRaw, "refresh", "smart", `Refresh remote objects before planning: true, false, or smart.`).SetDisplay("=true|false|smart")'
    text, n = pattern.subn(repl, text, count=1)
    if n != 1:
        raise SystemExit(f"{rel}: refresh flag binding not found")
    marker = "\tcli.PreHook(func() tfdiags.Diagnostics {\n\t\tvar diags tfdiags.Diagnostics\n"
    parse = '''\tcli.PreHook(func() tfdiags.Diagnostics {
\t\tvar diags tfdiags.Diagnostics

\t\to.RefreshExplicit = refreshFlag.IsSet != nil && refreshFlag.IsSet()
\t\tswitch refreshRaw {
\t\tcase "true":
\t\t\to.RefreshMode = plans.RefreshAll
\t\t\to.Refresh = true
\t\tcase "false":
\t\t\to.RefreshMode = plans.RefreshNone
\t\t\to.Refresh = false
\t\tcase "smart", "":
\t\t\to.RefreshMode = plans.RefreshSmart
\t\t\to.Refresh = true
\t\tdefault:
\t\t\tdiags = diags.Append(tfdiags.Sourceless(
\t\t\t\ttfdiags.Error,
\t\t\t\t"Invalid refresh option",
\t\t\t\tfmt.Sprintf("Invalid value %q for -refresh. Use true, false, or smart.", refreshRaw),
\t\t\t))
\t\t}
'''
    text = replace_once(text, marker, parse, rel)
    old = '''\t\tcase destroyRaw:
\t\t\to.PlanMode = plans.DestroyMode
\t\tcase refreshOnlyRaw:
\t\t\to.PlanMode = plans.RefreshOnlyMode
\t\t\tif !o.Refresh {
\t\t\t\tdiags = diags.Append(tfdiags.Sourceless(
\t\t\t\t\ttfdiags.Error,
\t\t\t\t\t"Incompatible refresh options",
\t\t\t\t\t"It doesn't make sense to use -refresh-only at the same time as -refresh=false, because OpenTofu would have nothing to do.",
\t\t\t\t))
\t\t\t}
'''
    new = '''\t\tcase destroyRaw:
\t\t\to.PlanMode = plans.DestroyMode
\t\t\tif o.RefreshMode == plans.RefreshSmart {
\t\t\t\tif o.RefreshExplicit {
\t\t\t\t\tdiags = diags.Append(tfdiags.Sourceless(tfdiags.Error, "Incompatible refresh options", "Smart refresh is not supported with -destroy. Use -refresh=true."))
\t\t\t\t} else {
\t\t\t\t\to.RefreshMode = plans.RefreshAll
\t\t\t\t}
\t\t\t}
\t\tcase refreshOnlyRaw:
\t\t\to.PlanMode = plans.RefreshOnlyMode
\t\t\tif o.RefreshMode == plans.RefreshSmart {
\t\t\t\tif o.RefreshExplicit {
\t\t\t\t\tdiags = diags.Append(tfdiags.Sourceless(tfdiags.Error, "Incompatible refresh options", "Smart refresh is not supported with -refresh-only. Use -refresh=true."))
\t\t\t\t} else {
\t\t\t\t\to.RefreshMode = plans.RefreshAll
\t\t\t\t}
\t\t\t}
\t\t\tif o.RefreshMode == plans.RefreshNone {
\t\t\t\tdiags = diags.Append(tfdiags.Sourceless(
\t\t\t\t\ttfdiags.Error,
\t\t\t\t\t"Incompatible refresh options",
\t\t\t\t\t"It doesn't make sense to use -refresh-only at the same time as -refresh=false, because OpenTofu would have nothing to do.",
\t\t\t\t))
\t\t\t}
'''
    return replace_once(text, old, new, rel)
edit(rel, extended)

# The destroy alias sets its mode after BindOperation has parsed flags.
rel = "internal/command/arguments/apply.go"
def apply_args(text):
    old = "\t\t\tapply.Operation.PlanMode = plans.DestroyMode\n"
    new = old + '''\t\t\tif apply.Operation.RefreshMode == plans.RefreshSmart {
\t\t\t\tif apply.Operation.RefreshExplicit {
\t\t\t\t\treturn tfdiags.New(tfdiags.Sourceless(tfdiags.Error, "Incompatible refresh options", "Smart refresh is not supported with destroy. Use -refresh=true."))
\t\t\t\t}
\t\t\t\tapply.Operation.RefreshMode = plans.RefreshAll
\t\t\t}
'''
    return replace_once(text, old, new, rel)
edit(rel, apply_args)

# Backend operation and PlanOpts transport only the refresh strategy.
rel = "internal/backend/backend.go"
edit(rel, lambda t: replace_once(t,
    "\tPlanId         string\n\tPlanRefresh    bool   // PlanRefresh will do a refresh before a plan\n\tPlanOutPath    string // PlanOutPath is the path to save the plan",
    "\tPlanId         string\n\tPlanRefresh    bool              // PlanRefresh will do a refresh before a plan\n\tRefreshMode    plans.RefreshMode // RefreshMode controls smart/all/none refresh strategy\n\tPlanOutPath    string            // PlanOutPath is the path to save the plan", rel))

rel = "internal/backend/local/backend_local.go"
edit(rel, lambda t: replace_once(t,
    "\t\tSkipRefresh:        op.Type != backend.OperationTypeRefresh && !op.PlanRefresh,\n\t\tGenerateConfigPath: op.GenerateConfigOut,",
    "\t\tSkipRefresh:        op.Type != backend.OperationTypeRefresh && !op.PlanRefresh,\n\t\tRefreshMode:        op.RefreshMode,\n\t\tGenerateConfigPath: op.GenerateConfigOut,", rel))

rel = "internal/command/apply.go"
edit(rel, lambda t: replace_once(t,
    "\topReq.PlanRefresh = applyArgs.Operation.Refresh\n",
    "\topReq.PlanRefresh = applyArgs.Operation.Refresh\n\topReq.RefreshMode = applyArgs.Operation.RefreshMode\n", rel))

rel = "internal/command/plan.go"
edit(rel, lambda t: replace_once(t,
    "\topReq.PlanRefresh = args.Refresh\n",
    "\topReq.PlanRefresh = args.Refresh\n\topReq.RefreshMode = args.RefreshMode\n", rel))

# Keep PostSkipRefresh optional: existing OpenTofu hooks must not be forced to
# implement a Ghoten-specific telemetry method.
rel = "internal/tofu/hook.go"
def optional_skip_hook(text):
    anchor = "type HookAction byte\n"
    helper = """type postSkipRefreshHook interface {
\tPostSkipRefresh(addr addrs.AbsResourceInstance, gen states.Generation) (HookAction, error)
}

func callPostSkipRefresh(h Hook, addr addrs.AbsResourceInstance, gen states.Generation) (HookAction, error) {
\tif hook, ok := h.(postSkipRefreshHook); ok {
\t\treturn hook.PostSkipRefresh(addr, gen)
\t}
\treturn HookActionContinue, nil
}

"""
    return replace_once(text, anchor, helper + anchor, rel)
edit(rel, optional_skip_hook)

# Rewrite legacy direct hook calls to the optional capability helper.
for path in (root / "internal/tofu").glob("*.go"):
    text = path.read_text()
    new = text.replace("return h.PostSkipRefresh(", "return callPostSkipRefresh(h, ")
    if new != text:
        path.write_text(new)
