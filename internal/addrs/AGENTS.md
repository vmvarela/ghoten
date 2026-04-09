# internal/addrs

Address types for all Terraform objects. Cross-cutting — imported by nearly every package.

## KEY TYPES

| Type | Purpose |
|------|---------|
| `Module` | Static module path (zero value = root) |
| `ModuleInstance` | Dynamic instance with count/for_each key |
| `Resource` | Unqualified resource `type.name` |
| `AbsResourceInstance` | Fully qualified `module.x.resource.type.name[key]` |
| `Provider` | Provider address `registry/namespace/type` |
| `InputVariable` | Input variable address |
| `OutputValue` | Output value address |
| `Set[T UniqueKeyer]` | Generic set using `UniqueKey` dedup |
| `Map[K UniqueKeyer, V any]` | Generic map with address keys |

## CONVENTIONS

- All address types implement `UniqueKeyer` for deduplication
- `String()` methods produce HCL-compatible representations
- `ParseXxx()` functions return `(result, tfdiags.Diagnostics)` not `error`
- Move endpoint resolution in `move_endpoint_module.go` (745 lines) — handles `moved {}` blocks
- 64 files — large package, but flat by design (no subpackages)

## ANTI-PATTERNS

- **DO NOT** compare addresses with `==` — use `UniqueKey()` or `Equal()` methods
- **DO NOT** construct addresses manually — use `Parse*` or builder functions
