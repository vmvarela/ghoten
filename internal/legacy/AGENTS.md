# internal/legacy

**FROZEN CODE — DO NOT EXTEND.**

Backward-compatibility shims from Terraform heritage. Excluded from linting.

## SUBPACKAGES

| Package | Purpose |
|---------|---------|
| `helper/schema/` | Old provider SDK (terraform-plugin-sdk predecessor) — `Resource`, `Schema`, `ResourceData` |
| `helper/hashcode/` | Hash code utilities |
| `helper/acctest/` | Acceptance testing helpers |
| `ghoten/` | Legacy state, diff, resource types — state v1/v2/v3 upgrade paths |
| `hcl2shim/` | HCL2-to-flatmap adapters: `FlatmapValueFromHCL2()`, `HCL2ValueFromFlatmap()` |

## ANTI-PATTERNS

- **DO NOT** add new code here — create in modern packages instead
- **DO NOT** refactor — this code is intentionally frozen
- Deprecated functions: `VersionString()`, `UnsafeSetFieldRaw()`, `MigrateState` field
- State upgrade path: v1 → v2 → v3 (in `ghoten/state_upgrade_*.go`)
- V0 binary state no longer supported (must upgrade via 0.6.x first)

## NOTES

- Linter exclusion: `internal/legacy/` in `.golangci.yml`
- Contains both OpenTofu and HashiCorp MPL 2.0 copyright headers
- `data_source_resource_shim.go` — backward-compat for old data sources
