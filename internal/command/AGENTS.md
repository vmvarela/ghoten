# internal/command

CLI commands implementation. 105 Go files + subpackages for views, arguments, JSON output.

## COMMAND FLOW

```
Run(rawArgs)
  1. arguments.ParseView(rawArgs) → extract global flags (ViewType)
  2. c.View.Configure(common)
  3. arguments.ParseXxx(rawArgs) → typed args struct
  4. views.NewXxx(viewType, ...) → human or JSON view
  5. c.Meta.PrepareBackend() → backend.Enhanced
  6. c.Meta.RunOperation() → backend operation
  7. view.Diagnostics(), view.ResourceCount(), view.Outputs()
```

## KEY TYPES

| Type | File | Role |
|------|------|------|
| `Meta` | `meta.go` | Shared CLI state (984 lines) — Streams, View, WorkingDir, backend init |
| `ApplyCommand` | `apply.go` | Plan + apply combined |
| `PlanCommand` | `plan.go` | Plan only |
| `InitCommand` | `init.go` | Module fetch, backend setup, provider download |
| `TestCommand` | `test.go` | Integration test runner |

## SUBPACKAGES

| Package | Purpose |
|---------|---------|
| `arguments/` | CLI argument parsing — `ParseApply()`, `ParsePlan()`, `ViewType` enum |
| `views/` | Output formatting — `Apply`, `Plan`, `Operation` interfaces with Human/JSON impls |
| `jsonformat/` | JSON plan rendering — Composite Diff pattern, renderer interface |
| `jsonentities/` | JSON output types for plan, state, diagnostic |
| `jsonconfig/` | JSON config output |
| `jsonplan/` | JSON plan output (1011 lines) |
| `cliconfig/` | CLI config: credentials, OCI auth, provider installation |
| `e2etest/` | End-to-end CLI tests |

## WHERE TO LOOK

| Task | File | Notes |
|------|------|-------|
| Add new command | Create `newcmd.go`, register in `cmd/ghoten/commands.go` |
| Backend init logic | `meta_backend.go` (1772 lines) | Complex: local/remote/cloud/oras |
| State migration | `meta_backend_migrate.go` (1130 lines) | Single→multi scenarios |
| OCI credentials | `cliconfig/oci_credentials.go` (782 lines) | Docker config, env, static auth |
| View layer | `views/view.go` | Base `View` struct with Streams, colorize |
| Multi-output | `views/apply.go:ApplyMulti` | `-json-into` flag: file + terminal |

## CONVENTIONS

- All commands embed `Meta` struct — never create standalone
- Arguments parsed in `arguments/` package, return `(args, closer, diags)`
- Views: interface + Human/JSON impl per command
- `Meta.PrepareBackend()` not `Meta.Backend()` for new code
- Return `0` success, non-zero failure from `Run()`

## ANTI-PATTERNS

- **DO NOT** put argument parsing in command files — use `arguments/` package
- **DO NOT** write directly to stdout — use `views` layer
- Many `TODO meta-refactor:` comments — ongoing migration to new view system
