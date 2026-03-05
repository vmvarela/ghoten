# ORAS Backend Guide

## What is this?

The `oras` backend stores OpenTofu state directly as OCI artifacts. Instead of running an HTTP backend service, you use an OCI registry you already operate (for many teams: GHCR).

This backend is a good fit when your org already has registry auth, audit trails, and permissions in place. It is not ideal if you need backend-specific APIs or advanced policy engines that OCI registries do not provide.

## Minimal config

This is the smallest valid backend configuration:

```hcl
terraform {
  backend "oras" {
    repository = "ghcr.io/acme/infra-state"
  }
}
```

`repository` is the one required setting. Everything else is optional tuning.

## Production baseline

Use this baseline first, then optimize only if you observe concrete bottlenecks:

```hcl
terraform {
  backend "oras" {
    repository   = "ghcr.io/myorg/infra-state"
    compression  = "gzip"
    lock_ttl     = 300
    max_versions = 10
  }

  encryption {
    key_provider "pbkdf2" "main" {
      passphrase = var.state_passphrase
    }
    method "aes_gcm" "main" {
      key_provider = key_provider.pbkdf2.main
    }
    state { method = method.aes_gcm.main }
    plan  { method = method.aes_gcm.main }
  }
}
```

For real production systems, prefer a KMS-backed key provider over PBKDF2 passphrases.

## Configuration reference

| Parameter | Type | Default | Env Var | Notes |
|---|---|---|---|---|
| `repository` | string | `""` | `TF_BACKEND_ORAS_REPOSITORY` | Required OCI repo (`<registry>/<repo>`) |
| `compression` | string | `"none"` | — | `none` or `gzip` |
| `insecure` | bool | `false` | — | Skip TLS verification |
| `ca_file` | string | `""` | — | PEM CA bundle path |
| `retry_max` | int | `2` | `TF_BACKEND_ORAS_RETRY_MAX` | Retry count for transient errors |
| `retry_wait_min` | int | `1` | `TF_BACKEND_ORAS_RETRY_WAIT_MIN` | Backoff min (seconds) |
| `retry_wait_max` | int | `30` | `TF_BACKEND_ORAS_RETRY_WAIT_MAX` | Backoff max (seconds) |
| `lock_ttl` | int | `0` | `TF_BACKEND_ORAS_LOCK_TTL` | `> 0` enables stale-lock cleanup |
| `rate_limit` | int | `0` | `TF_BACKEND_ORAS_RATE_LIMIT` | Requests/sec, `0` disables |
| `rate_limit_burst` | int | `0` | `TF_BACKEND_ORAS_RATE_LIMIT_BURST` | Token bucket burst |
| `max_versions` | int | `0` | — | Historical versions to keep |

## How state is represented

Tags are stable pointers inside your OCI repository:

| Purpose | Tag pattern |
|---|---|
| Current state | `state-<workspace>` |
| Active lock | `locked-<workspace>` |
| Historical version | `state-<workspace>-v<N>` |

When a workspace name is not a valid OCI tag, Ghoten hashes it to `ws-<sha256>` and stores the original name in annotations.

## Auth resolution order

Ghoten resolves credentials in this order:

1. Docker credential helpers (`docker login`, credential store)
2. CLI host credentials (`ghoten login` / Terraform-style host tokens)

If auth fails, run `docker login <registry>` first; it fixes most cases faster than tweaking backend flags.

## Troubleshooting

| Problem | What to do |
|---|---|
| `unauthorized` or `denied` | Re-run `docker login`. On GHCR, ensure `read:packages` and `write:packages` scopes |
| Lock appears stuck | Set `lock_ttl = 300` to auto-clear stale locks on next acquisition |
| Deleting versions fails on GHCR | Ghoten falls back to GitHub Packages API; ensure package delete permission is granted |
| Need deep diagnostics | Run with `TF_LOG=DEBUG ./ghoten plan` |

## Verified OCI registries

The ORAS backend is primarily developed against GHCR, but has been validated against additional registries to give adopters a clearer compatibility baseline.

| Registry | Version tested | State | Lock | Retention | Notes |
|---|---|---|---|---|---|
| **GHCR** (ghcr.io) | — | OK | OK | OK (via GitHub Packages API fallback) | Primary development target |
| **Zot** | v2.1.0 | OK | OK | OK (native manifest delete) | OCI Distribution Spec 1.1.0 compliant; tested with anonymous HTTP access |

### Running the Zot validation yourself

The integration suite spins up a local Zot container via Docker and exercises state, locking, retention, multi-workspace, and compression scenarios:

```bash
make test-zot
```

Or directly:

```bash
TF_ORAS_ZOT_TEST=1 go test -v -timeout 120s ./internal/backend/remote-state/oras/... -run Zot
```

Requires Docker. The test uses `ghcr.io/project-zot/zot-linux-amd64:v2.1.0` and binds a random free port on localhost.

### Known limitations / workarounds

- **Auth**: Zot supports HTTP Basic auth and Bearer tokens. The integration tests run with anonymous access for simplicity. For authenticated setups, `docker login <zot-host>` works the same as with GHCR.
- **Tag deletion**: Unlike GHCR (which returns 405 for manifest DELETE and needs a GitHub API fallback), Zot supports the standard OCI manifest delete endpoint natively. No workaround needed.
- **TLS**: Not tested in the automated suite. For TLS endpoints, set `ca_file` in the backend config or `insecure = true` for self-signed certificates.
