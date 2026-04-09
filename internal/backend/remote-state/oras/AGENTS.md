# internal/backend/remote-state/oras

**Ghoten's key differentiator.** Native ORAS backend storing Terraform state as OCI artifacts in container registries.

## FILES

| File | Purpose |
|------|---------|
| `backend.go` | Backend registration, schema, configuration (repository, compression, versioning) |
| `client.go` | Core implementation — push/pull state, locking via OCI manifests, versioning |
| `github.go` | GHCR-specific: package visibility, lock cleanup via GitHub API |
| `helper_test.go` | Test utilities |
| `backend_test.go` | Backend configuration tests |
| `client_test.go` | Client operation tests |
| `github_test.go` | GitHub API integration tests |
| `zot_test.go` | Zot registry integration tests (requires `TF_ORAS_ZOT_TEST=1`) |

## KEY CONCEPTS

- **State as OCI artifact**: state.json pushed/pulled via ORAS protocol
- **Locking via manifests**: generation-based locks with atomic verification
- **Versioning**: `versioning_max_versions` for state history retention
- **Compression**: optional gzip compression for state artifacts
- **Auth**: reuses Docker/OCI registry credentials (docker login, GHCR tokens)

## INTEGRATION TESTS

```bash
make test-zot    # Requires Docker, starts Zot registry container
```

## ANTI-PATTERNS

- `//nolint:errcheck` and `//nolint:nilnil` present — these are intentional for specific error paths
- GitHub API calls in `github.go` are GHCR-specific — don't generalize without testing other registries
