# cmd/ghoten

CLI entry point. Uses `mitchellh/cli` framework with factory-pattern command registration.

## WHERE TO LOOK

| Task | File | Notes |
|------|------|-------|
| Add new command | `commands.go:initCommands()` | Add to `commands` map with `Meta` |
| Modify CLI startup | `main.go:realMain()` | UI init, signal handling, plugin reattach |
| Change primary commands | `commands.go:primaryCommands` | Controls `ghoten -help` ordering |
| Hide a command | `commands.go:hiddenCommands` | Not shown in help |
| Credential sources | `commands.go:credentialsSource()` | OCI + service credentials |
| Environment merging | `main.go:mergeEnvArgs()` | `TF_CLI_ARGS` / `TF_CLI_ARGS_*` env vars |

## CONVENTIONS

- Commands implement `cli.Command` interface: `Run([]string) int`, `Help() string`, `Synopsis() string`
- All commands embed `command.Meta` struct for shared state (Streams, View, WorkingDir)
- Command factories are closures capturing `meta` — do NOT store `meta` as global
- `init()` in main.go initializes `Ui` — this runs before `realMain()`
- Exit codes: 0=success, 1=error, 2=CLI usage error

## ANTI-PATTERNS

- **DO NOT** add provider reattach logic outside `parseReattachProviders()` — security boundary
- **DO NOT** bypass `initCommands()` for command registration
