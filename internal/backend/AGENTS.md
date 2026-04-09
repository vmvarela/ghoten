# internal/backend

Backend abstraction layer. Interfaces for state storage + operation execution.

## KEY INTERFACES

| Interface | File | Role |
|-----------|------|------|
| `Backend` | `backend.go` | Base: `StateMgr()`, `Configure()`, `Workspaces()` |
| `Enhanced` | `backend.go` | Extends Backend: `Operation()` for plan/apply |
| `CLI` | `cli.go` | CLI integration callbacks |

## SUBPACKAGES

| Package | Purpose |
|---------|---------|
| `init/` | Backend registry — `Init()` returns all backends, `RemovedBackends` map |
| `local/` | Local backend — runs operations via `ghoten.Context` |
| `remote/` | Terraform Cloud/Enterprise backend |
| `remote-state/` | 11 remote state backends (s3, azure, gcs, consul, cos, http, inmem, kubernetes, oras, oss, pg) |

## WHERE TO LOOK

| Task | Location | Notes |
|------|----------|-------|
| Add new backend | `init/init.go` + `remote-state/newbackend/` | Register in `Init()` map |
| ORAS backend | `remote-state/oras/` | See subdirectory AGENTS.md |
| Deprecate backend | `init/init.go:RemovedBackends` | Map name → message |
| Backend testing | `testing.go` | Test helpers with `t.Helper()` |

## CONVENTIONS

- Backend names lowercase: `s3`, `azurerm`, `oras`, `gcs`
- Hyphenated dir `remote-state/` is inherited from Terraform — don't rename
- Each backend implements `Backend` interface minimum
- `local` backend is the only `Enhanced` implementation (runs graph operations)
- Removed backends: artifactory, azure (old), etcd, etcdv3, manta, swift
