# internal/backend/remote-state/oras

**Ghoten's key differentiator.** Thin adapter over the [`ghoten-oras-backend`](https://github.com/vmvarela/ghoten-oras-backend) library, storing Terraform state as OCI artifacts in container registries.

The core ORAS logic (push/pull, locking, versioning, GHCR fallback, retry, compression) lives in the standalone library. This package handles only ghoten-specific concerns: HCL schema, environment variables, credential policy, and bridging library types to ghoten interfaces.

## FILES

| File | Purpose |
|------|---------|
| `backend.go` | HCL schema, config parsing, credential policy, delegates to `oraslib.StateBackend` |
| `client.go` | Thin adapter: bridges `oraslib.StateMgr` → `remote.Client` + `remote.ClientLocker` + `remote.ClientRetentionWaiter` |
| `backend_test.go` | Backend configuration tests |
| `zot_test.go` | Zot registry integration tests (requires `TF_ORAS_ZOT_TEST=1`) |
| `transport_compat_test.go` | Test-only: re-exports HTTP client construction for `zot_test.go` |
| `tags_compat_test.go` | Test-only: re-exports tag constants for `backend_test.go` |

## KEY CONCEPTS

- **Adapter pattern**: `backend.go` configures `oraslib.New(ctx, cfg)` and stores the `StateBackend`; `client.go` wraps `StateMgr` into ghoten's `remote.Client` interface
- **Credential delegation**: CLI config → `ociauthconfig.CredentialsPolicy` → `oraslib.CredentialFunc`
- **Encryption boundary**: encryption is applied by `remote.NewState()` wrapping the adapter — the library sees raw `[]byte` only
- **Type bridging**: `remote.Payload` ↔ `oraslib.StatePayload`, `statemgr.LockInfo` ↔ `oraslib.LockInfo`, `statemgr.LockError` ↔ `oraslib.LockError`

## LIBRARY

Core implementation: [`github.com/vmvarela/ghoten-oras-backend`](https://github.com/vmvarela/ghoten-oras-backend)

Handles: state CRUD, generation-based locking, versioning with retention, gzip compression, GHCR tag-deletion fallback, retry with exponential backoff, rate limiting, OpenTelemetry instrumentation.

## INTEGRATION TESTS

```bash
make test-zot    # Requires Docker, starts Zot registry container
```

## ANTI-PATTERNS

- `//nolint:nilnil` in `client.go` is intentional — `remote.Client.Get()` returns `(nil, nil)` for missing state
- Keep this adapter thin: no business logic, only plumbing
- Do not duplicate logic that belongs in the library
