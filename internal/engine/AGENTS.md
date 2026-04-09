# internal/engine

New execution engine. Separates planning and applying into distinct subsystems.

## SUBPACKAGES

| Package | Purpose |
|---------|---------|
| `planning/` | Plan execution — wraps `ghoten.Context.Plan()` with pre/post hooks |
| `applying/` | Apply execution — wraps `ghoten.Context.Apply()` with operation lifecycle |
| `internal/` | Shared internal utilities |
| `plugins/` | Plugin management for engine operations |

## RELATIONSHIP TO internal/ghoten

- `engine/` is the **new** orchestration layer sitting above `internal/ghoten/`
- `internal/ghoten/` has the graph builder/walker (core logic)
- `engine/planning/` and `engine/applying/` coordinate the full operation lifecycle
- Many `TODO` items in engine files — this is actively evolving

## NOTES

- `planning/plan_managed.go` has 15+ TODOs — incomplete implementation areas
- `applying/operations_resource_managed.go` has 10+ TODOs
- Engine delegates graph operations to `ghoten.Context` — does not duplicate graph logic
