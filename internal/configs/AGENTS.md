# internal/configs

HCL configuration parsing. Converts `.tf` files into typed Go config structs.

## WHERE TO LOOK

| Task | File | Notes |
|------|------|-------|
| Resource config | `resource.go` | `Resource`, `ManagedResource`, `DataResource` types |
| Provider config | `provider.go` | `Provider`, `RequiredProvider` |
| Module calls | `module_call.go` | `ModuleCall` struct |
| Variable/output | `named_values.go` | `Variable`, `Output`, `Local` |
| Backend config | `backend.go` | `Backend` struct from `terraform {}` block |
| Test files | `test_file.go` | `.tftest.hcl` parsing |
| Config loading | `config.go`, `parser.go` | `LoadConfigDir()`, `Parser` |

## SUBPACKAGES

| Package | Purpose |
|---------|---------|
| `configschema/` | Provider schema description — `Block`, `Attribute`, `NestingMode`, `ImpliedType()` |
| `configload/` | Module loader with source resolution |
| `hcl2shim/` | _(in legacy/)_ HCL2-to-flatmap adapters |

## CONVENTIONS

- Config structs are immutable after loading — never modify in place
- Use `tfdiags.Diagnostics` for parse errors, not `error`
- `configschema.Block.InternalValidate()` for schema self-checking
- `NestingMode`: Single(0), Group(1), List(2), Set(3), Map(4)
- Testdata in `testdata/valid-files/` and `testdata/invalid-files/`
