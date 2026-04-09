# internal/lang

HCL expression evaluation engine. Evaluates variables, functions, references in Terraform configs.

## SUBPACKAGES

| Package | Purpose |
|---------|---------|
| `eval/` | Expression evaluation — `Scope`, `EvalData` |
| `eval/internal/configgraph/` | Core evaluation graph (depth 5) — `Maybe[T]`, `OnceValuer`, `InstanceSelector` |
| `eval/internal/evalglue/` | Interfaces for external evaluation |
| `eval/internal/ghoten2024/` | Language edition compiler |
| `funcs/` | Built-in function implementations (28 files) |

## KEY PATTERNS

- **`Maybe[T]` generics** in `configgraph/maybe.go` — models unknown/known values (like Option type)
- **`OnceValuer`** — memoization wrapper ensuring single evaluation with cycle detection
- **`InstanceSelector` interface** — abstracts count/for_each/enabled from graph nodes
- **`CheckGroup`** — concurrent validation using goroutines

## WHERE TO LOOK

| Task | File | Notes |
|------|------|-------|
| Add built-in function | `funcs/` | Follow existing function patterns |
| Expression evaluation | `eval.go` (1161 lines) | All HCL expression types |
| Config graph nodes | `eval/internal/configgraph/` | No dependency on `configs` package |
| Language editions | `eval/internal/ghoten2024/` | Compiler for 2024 edition |

## CONVENTIONS

- `configgraph/` has NO dependency on `configs` package — only uses HCL primitives
- Functions return `(cty.Value, error)` not diagnostics
- Module boundary: `configgraph` → `evalglue` → `ghoten2024` (strict layering)
