# PROJECT KNOWLEDGE BASE

**Generated:** 2026-04-09
**Commit:** a1a9a4b1b8
**Branch:** master

## OVERVIEW

Ghoten is an OpenTofu fork adding a native ORAS backend for storing Terraform state as OCI artifacts in container registries (GHCR, Zot). Also adds `-refresh=smart` to skip refresh for unchanged resources. Module: `github.com/vmvarela/ghoten`, Go 1.26, CLI via `mitchellh/cli`.

## STRUCTURE

```
ghoten/
├── cmd/ghoten/          # CLI entry point, command registration (mitchellh/cli factory map)
├── internal/
│   ├── ghoten/          # Core engine: graph builder/walker, plan/apply context (203 files)
│   ├── command/         # CLI commands, views (human/JSON), argument parsing (105 files)
│   ├── backend/         # Backend interfaces + remote-state/ implementations
│   │   └── remote-state/oras/  # ORAS backend — Ghoten's key differentiator
│   ├── engine/          # New execution engine (planning/, applying/)
│   ├── configs/         # HCL configuration parsing, configschema
│   ├── addrs/           # Address types for all Terraform objects (cross-cutting)
│   ├── states/          # State management, statefile serialization (v0–v4)
│   ├── lang/            # HCL expression evaluation, eval graph, language editions
│   ├── legacy/          # Frozen code: old schema SDK, hcl2shim, state upgrades
│   ├── tfdiags/         # Diagnostic system (replaces Go errors project-wide, 389 files)
│   ├── dag/             # DAG implementation for graph walking
│   ├── plans/           # Plan types, plan file read/write
│   └── ...              # 40+ more packages (see subdirectory AGENTS.md files)
├── action/              # GitHub Action shell scripts (not Go)
├── tools/               # protobuf-compile, selected-go-version
├── docs/                # User documentation
└── version/             # Version info package
```

## WHERE TO LOOK

| Task | Location | Notes |
|------|----------|-------|
| Add CLI command | `cmd/ghoten/commands.go` + `internal/command/` | Register in factory map, embed `Meta` |
| ORAS backend changes | `internal/backend/remote-state/oras/` | client.go is main impl (1217 lines) |
| Smart refresh logic | `internal/ghoten/transform_smart_refresh.go` | Config hash comparison |
| Plan/apply flow | `internal/ghoten/context_plan.go`, `context_apply.go` | Graph builder → walker → node execution |
| Graph transforms | `internal/ghoten/transform_*.go` | Implement `GraphTransformer` interface |
| Provider interaction | `internal/ghoten/node_resource_abstract_instance.go` | 3446 lines, resource lifecycle |
| Configuration parsing | `internal/configs/` | HCL → config structs |
| State serialization | `internal/states/statefile/` | Versioned format, encryption-aware |
| Address types | `internal/addrs/` | `Module`, `Resource`, `Provider`, generic `Set[T]`/`Map[K,V]` |
| Error/diagnostic handling | `internal/tfdiags/` | Use `tfdiags.Diagnostics` not `error` |
| Expression evaluation | `internal/lang/eval/` | configgraph (depth 5), Maybe[T] pattern |
| Legacy compat | `internal/legacy/` | Frozen — do not extend |
| JSON output format | `internal/command/jsonformat/` | Composite Diff pattern, renderer interface |
| Build & release | `Makefile`, `.github/workflows/release.yaml` | 14-platform cross-compile, cosign signing |
| Integration tests | `Makefile` targets | `test-s3`, `test-pg`, `test-consul`, `test-zot`, etc. |

## CONVENTIONS

### Skills

- `golang-pro` applies to **all Go work** in this repo: implementation, tests, refactors, and code review.
- When multiple skills apply (e.g. writing a Go algorithm with formal derivation), load both.

### Pre-commit checklist

Run **all** of the following before every commit. Fix all failures before committing.

```bash
# 1. Unit tests (race detector, no cache, reasonable timeout)
go test -race -count=1 -timeout 30m ./...

# 2. Linter
make golangci-lint

# 3. Module tidiness (only if dependencies changed)
go mod tidy

# 4. Generated files (only if //go:generate sources changed)
make generate-check
```

### Code style

- Branch names: `issue-{number}/{short-description}`
- Commits: [Conventional Commits](https://www.conventionalcommits.org/) (`feat:`, `fix:`, `ci:`, `chore:`, `docs:`, etc.)
- PRs: squash merge, delete branch after merge
- Errors: return `tfdiags.Diagnostics` not `error` (project-wide convention)
- Mocks: manual mocks with `Fn` callback fields + `var _ Interface = (*Mock)(nil)` compile-time check; `go.uber.org/mock` for generated mocks
- Tests: table-driven with `map[string]struct{}` + `t.Run()`, assertions via `go-cmp` (`cmp.Diff`), NOT testify
- Helpers: always call `t.Helper()`, return cleanup funcs
- Build tags: `//go:build windows`, `//go:build !race`, `//go:build ignore` for manual test utils
- Generated code: `//go:generate go tool stringer`, `mockgen`, `protoc-gen-go` — DO NOT EDIT these files

### Linter (golangci-lint v2)

- Exclusion presets: comments, common-false-positives, legacy, std-error-handling
- Excluded paths: `internal/ipaddr/` (frozen), `internal/legacy/` (frozen), `website/`
- Disabled staticcheck: `-QF1008`, `-ST1003`, `-ST1005`, `-ST1016`

## ANTI-PATTERNS (THIS PROJECT)

- **DO NOT** use `testify` assertions — use `cmp.Diff` from `google/go-cmp`
- **DO NOT** edit files under `internal/legacy/` — frozen code
- **DO NOT** edit files under `internal/ipaddr/` — frozen code
- **DO NOT** edit `*.pb.go`, `*_string.go` or any `// Code generated` files
- **DO NOT** change magic cookie values in `internal/plugin/serve.go` / `internal/plugin6/serve.go`
- **DO NOT** use `log.Printf` for new code — use `logging.NewLogger(name)` (hclog)
- **NEVER** store `context.Context` in structs — pass as first param
- `remote-state` dir uses hyphens (inherited from Terraform) — don't rename

## UNIQUE STYLES

- **go.mod `godebug`**: `tlsmlkem=0` disables ML-KEM TLS (AWS/GCP compatibility issue)
- **go.mod `replace`**: `hashicorp/hcl/v2` → `opentofu/hcl/v2` (custom fork)
- **No goreleaser**: fully custom release pipeline in `.github/workflows/release.yaml`
- **Cosign keyless signing** + SLSA3 provenance for all releases
- **Dual Dockerfile**: `Dockerfile` (alpine+git) and `Dockerfile.minimal` (scratch)
- **Dependabot grouping**: AWS, Azure, GCP, HashiCorp, OTEL, k8s, OpenTofu/ORAS
- **PR labels**: area-based (`area/core`, `area/backend`, etc.) via `.github/labeler.yml`
- **`Maybe[T]` generics**: used in `internal/lang/eval/internal/configgraph/` for unknown/known values

## COMMANDS

```bash
make build                  # Build with git-describe version via ldflags
make test                   # go test -race -count=1 -v ./...
make test-with-coverage     # Coverage with atomic mode
make golangci-lint          # Install + run golangci-lint v2
make generate               # go generate ./...
make generate-check         # generate + git diff (CI gate)
make protobuf               # Compile .proto files via custom tool
make test-s3                # S3 backend integration tests
make test-pg                # PostgreSQL backend integration tests
make test-consul            # Consul backend integration tests
make test-zot               # Zot OCI registry integration tests (ORAS)
make test-kubernetes        # Kubernetes backend integration tests
make test-azure             # Azure backend integration tests
```

## NOTES

- This is an **OpenTofu fork** — upstream tracking policy keeps changes focused on ORAS + smart refresh
- 535K lines of Go across 1773 files, 245 files >500 lines — large codebase
- `internal/ghoten/node_resource_abstract_instance.go` (3446 lines) is NOT a god object — it handles full resource lifecycle with 40+ focused methods
- State version is currently 3 (upgrade path: v1→v2→v3 in legacy/)
- Removed backends: artifactory, azure (old), etcd, etcdv3, manta, swift — see `internal/backend/init/init.go`
- E2E tests in `internal/cloud/e2e/` and `internal/command/e2etest/`
- Many `TODO meta-refactor:` comments indicate ongoing view-system migration
- 340 TODOs, 117 FIXMEs across codebase — significant tech debt from upstream
