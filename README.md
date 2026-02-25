# Ghoten

> **Name origin:** *Ghoten* blends **Gh**itHub and Open**Tofu**, with a nod to [Goten](https://dragonball.fandom.com/wiki/Goten) from *Dragon Ball Z*.

[![Test](https://github.com/vmvarela/ghoten/actions/workflows/test.yaml/badge.svg)](https://github.com/vmvarela/ghoten/actions/workflows/test.yaml)
[![Release](https://img.shields.io/github/v/release/vmvarela/ghoten?logo=github)](https://github.com/vmvarela/ghoten/releases/latest)
[![License: MPL 2.0](https://img.shields.io/github/license/vmvarela/ghoten)](LICENSE)

**An [OpenTofu](https://opentofu.org/) fork with a native ORAS backend for OCI registry state storage.**

Use an OCI-compatible registry — such as GitHub Container Registry (GHCR) — as the durable store for your infrastructure state, without external HTTP backends or third-party wrappers.

---

## Why Ghoten?

OpenTofu and Terraform have not added new built-in remote state backends in years, preferring the generic HTTP backend for custom implementations. If you already operate an OCI registry for container images, forcing state through an extra HTTP proxy adds moving parts, latency, and operational burden.

Ghoten adds a first-class **`oras`** backend that speaks the OCI distribution protocol directly. State snapshots, locks, and version history are stored as OCI artifacts inside a repository you control, reusing existing authentication, authorization, and operational tooling.

> **Upstream**: Ghoten tracks [OpenTofu](https://github.com/opentofu/opentofu) and carries only the additions required for the ORAS backend. All standard OpenTofu functionality is preserved.

---

## Key Features

### OpenTofu Core

- **Infrastructure as Code** — Describe infrastructure with a high-level configuration syntax. Version, share, and reuse blueprints like any other code.
- **Execution Plans** — Preview exactly what will change before applying, avoiding surprises.
- **Resource Graph** — Parallel creation and modification of non-dependent resources for efficient operations.
- **Change Automation** — Apply complex changesets with minimal human interaction.

### ORAS Backend

- **Native OCI storage** — State, locks, and versions stored as OCI manifests and layers via [ORAS](https://oras.land/).
- **Atomic locking with generation semantics** — Prevents concurrent lock holders through post-write verification.
- **Stale lock cleanup (TTL)** — Crashed-process locks are automatically cleared during the next acquisition.
- **State versioning with async retention** — Historical snapshots with non-blocking background cleanup.
- **Gzip compression** — Optional state compression before pushing to the registry.
- **Rate limiting** — Token-bucket rate limiter to stay within registry quotas.
- **Retry with exponential backoff** — Automatic retries for transient errors (429, 502–504, timeouts, DNS failures).
- **GHCR deletion fallback** — Falls back to the GitHub Packages API when OCI `DELETE` returns HTTP 405.
- **Docker credential helpers** — Reuses Docker config / credential store with caching and singleflight deduplication.
- **OpenTelemetry tracing** — All OCI HTTP calls are instrumented via `otelhttp`.
- **Client-side encryption** — Combine with [OpenTofu state encryption](https://opentofu.org/docs/language/state/encryption/) for end-to-end protection.

---

## Installation

### Build from Source

```bash
git clone https://github.com/vmvarela/ghoten.git
cd ghoten
make build        # produces ./ghoten binary
```

### Docker

```bash
# Full image (Alpine, includes git/bash/openssh)
docker pull ghcr.io/vmvarela/ghoten:<version>

# Minimal image (scratch, binary only)
docker pull ghcr.io/vmvarela/ghoten:<version>-minimal
```

### GitHub Releases

Pre-built binaries for 17 platform combinations (linux, darwin, windows, freebsd, netbsd, openbsd, solaris) are available on the [Releases](https://github.com/vmvarela/ghoten/releases) page.

---

## GitHub Action

Use Ghoten directly in your workflows with zero configuration. The action installs the binary, authenticates to GHCR, initializes the ORAS backend, and runs your command — with **PR comments** and **Job Summaries** out of the box.

### Quick Start

```yaml
# Plan on pull requests
on: pull_request

jobs:
  plan:
    runs-on: ubuntu-latest
    permissions:
      contents: read
      packages: write
      pull-requests: write
    steps:
      - uses: actions/checkout@v6
      - uses: vmvarela/ghoten@v1
```

That's it. Ghoten will:
- ✅ Install the matching binary
- 🔐 Authenticate to GHCR
- 🔧 Initialize with ORAS backend (`ghcr.io/<owner>/tf-state.<repo>`)
- 📋 Run `ghoten plan`
- 💬 Post the plan as a PR comment (auto-updated on each push)
- 📊 Generate a Job Summary

### Plan on PR, Apply on Merge

```yaml
name: Infrastructure

on:
  pull_request:
  push:
    branches: [main]

jobs:
  plan:
    if: github.event_name == 'pull_request'
    runs-on: ubuntu-latest
    permissions:
      contents: read
      packages: write
      pull-requests: write
    steps:
      - uses: actions/checkout@v6
      - uses: vmvarela/ghoten@v1

  apply:
    if: github.ref == 'refs/heads/main'
    runs-on: ubuntu-latest
    permissions:
      contents: read
      packages: write
    steps:
      - uses: actions/checkout@v6
      - uses: vmvarela/ghoten@v1
        with:
          command: apply
```

### Before & After

<details><summary><b>Before</b> — manual setup (~25 lines)</summary>

```yaml
jobs:
  plan:
    runs-on: ubuntu-latest
    container:
      image: ghcr.io/vmvarela/ghoten:1.12.0
      credentials:
        username: ${{ github.actor }}
        password: ${{ secrets.GITHUB_TOKEN }}
    permissions:
      contents: read
      packages: write
    steps:
      - uses: actions/checkout@v6
      - name: Login to GHCR
        run: |
          DOCKER_CONFIG="${HOME}/.docker"
          mkdir -p "$DOCKER_CONFIG"
          AUTH=$(printf '%s:%s' "$GITHUB_ACTOR" "$GITHUB_TOKEN" | base64 -w0 2>/dev/null || printf '%s:%s' "$GITHUB_ACTOR" "$GITHUB_TOKEN" | base64)
          printf '{"auths":{"ghcr.io":{"auth":"%s"}}}' "$AUTH" > "$DOCKER_CONFIG/config.json"
          echo "DOCKER_CONFIG=$DOCKER_CONFIG" >> "$GITHUB_ENV"
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
      - name: Init & Plan
        run: |
          ghoten init
          ghoten plan -out=tfplan
        env:
          TF_BACKEND_ORAS_REPOSITORY: ghcr.io/${{ github.repository_owner }}/tf-state
          TF_WORKSPACE: my-workspace
```

</details>

<details><summary><b>After</b> — one step (4 lines)</summary>

```yaml
jobs:
  plan:
    runs-on: ubuntu-latest
    permissions:
      contents: read
      packages: write
      pull-requests: write
    steps:
      - uses: actions/checkout@v6
      - uses: vmvarela/ghoten@v1
        with:
          workspace: my-workspace
```

</details>

### Action Inputs

| Input | Default | Description |
|---|---|---|
| `command` | `plan` | Command: `plan`, `apply`, `destroy`, `fmt`, `validate` |
| `working-directory` | `.` | Path to HCL configuration files |
| `workspace` | `default` | Terraform workspace |
| `var-file` | | Path to a `.tfvars` file |
| `variables` | | Newline-separated `key=value` variable pairs |
| `backend-repository` | `ghcr.io/<owner>/tf-state.<repo>` | ORAS backend OCI repository |
| `backend-config` | | Additional `-backend-config` key=value pairs |
| `github-token` | `${{ github.token }}` | Token for GHCR auth and PR comments |
| `comment-on-pr` | `true` | Post command output as PR comment |
| `add-summary` | `true` | Generate Job Summary |
| `args` | | Additional CLI arguments |
| `init-args` | | Additional arguments for `ghoten init` |
| `auto-init` | `true` | Automatically run `ghoten init` |
| `compression` | `gzip` | State compression: `none` or `gzip` |
| `lock-ttl` | `300` | Lock TTL in seconds |
| `max-versions` | `10` | State versions to retain |
| `fmt-check` | `false` | Use `-check` mode for fmt |
| `version` | *(auto)* | Ghoten version to install |

### Action Outputs

| Output | Description |
|---|---|
| `exitcode` | Exit code of the command |
| `stdout` | Full command output |
| `plan-has-changes` | `true`/`false` — whether plan detected changes |
| `plan-file` | Path to binary plan file (when `command=plan`) |
| `fmt-result` | `true`/`false` — whether fmt found differences |

### More Examples

**Format check on PR:**
```yaml
- uses: vmvarela/ghoten@v1
  with:
    command: fmt
    fmt-check: true
```

**Validate then Plan:**
```yaml
- uses: vmvarela/ghoten@v1
  with:
    command: validate

- uses: vmvarela/ghoten@v1
  with:
    command: plan
```

**Multiple workspaces:**
```yaml
- uses: vmvarela/ghoten@v1
  with:
    command: plan
    workspace: production
    working-directory: infra/
```

**With variables:**
```yaml
- uses: vmvarela/ghoten@v1
  with:
    command: plan
    var-file: prod.tfvars
    variables: |
      region=eu-west-1
      environment=production
```

**Custom backend repository:**
```yaml
- uses: vmvarela/ghoten@v1
  with:
    command: plan
    backend-repository: ghcr.io/myorg/custom-state-repo
```

**Plan → Apply in same job (auto-detects plan file):**
```yaml
- uses: vmvarela/ghoten@v1
  id: plan
  with:
    command: plan

- uses: vmvarela/ghoten@v1
  if: steps.plan.outputs.plan-has-changes == 'true'
  with:
    command: apply
```

### Permissions

The `GITHUB_TOKEN` requires these permissions:

| Permission | Required for |
|---|---|
| `contents: read` | Repository checkout |
| `packages: write` | ORAS backend (read/write state to GHCR) |
| `pull-requests: write` | PR comments (optional, only if `comment-on-pr: true`) |

> **Note**: If you enable version retention (`max-versions > 0`), ensure the token also grants `delete:packages` for GHCR version cleanup.

---

## ORAS Backend

### Security Considerations

State snapshots frequently contain sensitive data (provider credentials, resource attributes, and sometimes plaintext secrets). Treat the OCI repository with the same care as any other credentials store:

- **Enable client-side encryption** — OpenTofu supports [state encryption](https://opentofu.org/docs/language/state/encryption/) which encrypts state before it leaves your machine. This is the recommended approach.
- Keep the repository **private** and issue **least-privilege** tokens.
- Prefer registries that provide **encryption at rest** and **audit trails**.
- Be deliberate about retention/versioning; a long history increases exposure.

> **Note**: The implementation has primarily been exercised against GitHub Container Registry (`ghcr.io`). Other registries _should_ work but haven't yet been validated.

### Configuration Parameters

| Parameter | Type | Default | Env Var | Description |
|---|---|---|---|---|
| `repository` | string | `""` | `TF_BACKEND_ORAS_REPOSITORY` | OCI repository (`<registry>/<repo>`), without tag or digest. **Required.** |
| `compression` | string | `"none"` | — | State compression: `none` or `gzip`. |
| `insecure` | bool | `false` | — | Skip TLS certificate verification. |
| `ca_file` | string | `""` | — | Path to PEM-encoded CA certificate bundle. |
| `retry_max` | int | `2` | `TF_BACKEND_ORAS_RETRY_MAX` | Retries for transient requests. Total attempts = `retry_max + 1`. |
| `retry_wait_min` | int | `1` | `TF_BACKEND_ORAS_RETRY_WAIT_MIN` | Minimum backoff in seconds. |
| `retry_wait_max` | int | `30` | `TF_BACKEND_ORAS_RETRY_WAIT_MAX` | Maximum backoff in seconds. |
| `lock_ttl` | int | `0` | `TF_BACKEND_ORAS_LOCK_TTL` | Lock TTL in seconds. `> 0` enables automatic stale-lock clearing. |
| `rate_limit` | int | `0` | `TF_BACKEND_ORAS_RATE_LIMIT` | Max registry requests/sec. `0` disables. |
| `rate_limit_burst` | int | `0` | `TF_BACKEND_ORAS_RATE_LIMIT_BURST` | Burst size. Defaults to `1` when `rate_limit > 0`. |
| `max_versions` | int | `0` | — | Historical state versions to retain. `0` disables versioning. |

### How State Is Stored (Tags)

Tags act as stable references inside the OCI repository:

| Purpose | Tag pattern |
|---|---|
| State | `state-<workspaceTag>` |
| Lock | `locked-<workspaceTag>` |
| Version | `state-<workspaceTag>-v<N>` |

`workspaceTag` equals the workspace name when it is a valid OCI tag. Otherwise the backend uses a stable `ws-<sha256>` form and persists the real workspace name in OCI annotations.

### Wire Format

State objects use ORAS manifest v1.1 packing:

- Manifest `artifactType`: `application/vnd.terraform.state.v1`
- Layer media type:
  - `application/vnd.terraform.statefile.v1` (uncompressed)
  - `application/vnd.terraform.statefile.v1+gzip` (gzip)
- Annotations:
  - `org.terraform.workspace` — workspace name
  - `org.terraform.state.updated_at` — RFC 3339 timestamp

Lock objects:

- Manifest `artifactType`: `application/vnd.terraform.lock.v1`
- Annotations:
  - `org.terraform.workspace` — workspace name
  - `org.terraform.lock.id` — lock ID
  - `org.terraform.lock.info` — JSON-encoded lock metadata
  - `org.terraform.lock.generation` — JSON with `generation`, `lease_expiry`, and `holder_id`

Reads are strict: unexpected artifact types or media types raise errors instead of silently proceeding.

### Locking

The lock lives at `locked-<ws>` tags and carries metadata in annotations. Unlocking deletes that manifest via OCI `DELETE`. Registries that respond with HTTP 405 trigger a fallback: the lock reference is retagged to an `unlocked-<ws>` placeholder.

**Generation semantics** prevent concurrent lock holders:

1. Read the current generation **before** any modifications.
2. Clear stale locks if `lock_ttl > 0` and the existing lock has expired.
3. Write a new lock manifest with `generation = currentGen + 1`.
4. **Post-write verification**: re-read the lock and confirm the generation matches. A mismatch means another process won the race.

### Stale Lock Cleanup (TTL)

When `lock_ttl > 0`, each lock includes a `lease_expiry` timestamp. During lock acquisition, if the existing lock's lease has expired, it is automatically cleared before the new lock attempt proceeds. This handles the common case where a crashed process left a lock behind — the next `ghoten apply` clears it automatically.

### Versioning & Retention

When `max_versions > 0`, every successful state write gets an additional `state-<ws>-v<N>` tag. Version numbers are computed by scanning existing tags and picking `max + 1`.

Retention cleanup runs **asynchronously** in a background goroutine (30-second timeout, limited to 3 concurrent cleanups) so that `ghoten apply` returns immediately without waiting for old version pruning.

### Authentication

Credentials are discovered in this order:

1. **Docker credential helpers** — Docker config / credential store, with caching (5-minute TTL for successful lookups, 10-second TTL for errors) and singleflight deduplication.
2. **CLI host credentials** — OpenTofu/Terraform CLI login tokens (`ghoten login`).

If Docker credentials are missing or fail, the backend falls back to CLI tokens automatically.

### GHCR Deletion Fallback

GitHub Container Registry (`ghcr.io`) returns HTTP 405 for OCI `DELETE` operations. The backend automatically falls back to the **GitHub Packages REST API** to delete manifests. The token used for registry access must include `delete:packages` when retention is enabled or manual lock cleanup is needed.

---

## Usage

### Minimal

```hcl
terraform {
  backend "oras" {
    repository = "ghcr.io/acme/infra-state"
  }
}
```

### Production-Ready

```hcl
terraform {
  backend "oras" {
    repository  = "ghcr.io/myorg/infra-state"
    compression = "gzip"

    # Lock reliability
    lock_ttl = 300   # 5 minutes

    # State versioning
    max_versions = 10
  }

  # Client-side state encryption (OpenTofu feature)
  encryption {
    key_provider "pbkdf2" "main" {
      passphrase = var.state_passphrase
    }

    method "aes_gcm" "main" {
      key_provider = key_provider.pbkdf2.main
    }

    state {
      method = method.aes_gcm.main
    }

    plan {
      method = method.aes_gcm.main
    }
  }
}

variable "state_passphrase" {
  type      = string
  sensitive = true
}
```

> **Tip**: For production, consider using a KMS-backed key provider (AWS KMS, GCP KMS, etc.) instead of PBKDF2 with a passphrase.

### GitHub Actions CI

The recommended approach is the [Ghoten GitHub Action](#github-action):

```yaml
- uses: actions/checkout@v6
- uses: vmvarela/ghoten@v1
```

Or manually:

```yaml
- name: Ghoten Init
  env:
    TF_BACKEND_ORAS_REPOSITORY: ghcr.io/${{ github.repository_owner }}/tf-state
  run: |
    echo "${{ secrets.GITHUB_TOKEN }}" | docker login ghcr.io -u ${{ github.actor }} --password-stdin
    ghoten init
```

The token must be able to **read/write** the repository. If you enable version retention or rely on GHCR deletion fallbacks, it must also include `delete:packages`.

---

## Testing

```bash
# Full test suite
go test ./...

# ORAS backend only (offline, uses in-memory fake OCI registry)
go test ./internal/backend/remote-state/oras/...
```

---

## Troubleshooting

### "unauthorized" or "denied" errors

- Confirm `docker login <registry>` works with the same credentials.
- For GHCR, ensure the token grants `read:packages` + `write:packages` (and `delete:packages` if retention is enabled).

### Lock stuck after a crashed run

**Option 1: Automatic cleanup with TTL** (recommended)

```hcl
lock_ttl = 300  # 5 minutes
```

The next `ghoten apply` will automatically clear any lock older than the TTL.

**Option 2: Manual cleanup**

Delete the `locked-<workspace>` tag from the registry UI or API.

### Version deletion on GHCR

- GHCR does not support OCI `DELETE`; the backend automatically falls back to the GitHub Packages API.
- Ensure the token has `delete:packages` permission.
- If the API call also fails (e.g., insufficient permissions), old versions accumulate but writes still succeed.

### Debug mode

```bash
TF_LOG=DEBUG ghoten plan
```

---

## Limitations

- Only GHCR is exercised in automated tests; other registries may exhibit different quirks.
- Registries that refuse OCI `DELETE` degrade lock/unlock behavior (GHCR has a dedicated fallback via GitHub Packages API).
- `insecure = true` disables TLS verification — use only in controlled environments.

---

## Getting Help & Contributing

- Open a [GitHub issue](https://github.com/vmvarela/ghoten/issues)
- Read the [Contributing Guide](CONTRIBUTING.md)
- Review the [Code of Conduct](CODE_OF_CONDUCT.md)
- For upstream OpenTofu questions, see [OpenTofu Discussions](https://github.com/orgs/opentofu/discussions)

---

## Security

Please report security vulnerabilities through [GitHub Security Advisories](https://github.com/vmvarela/ghoten/security/advisories/new). See [SECURITY.md](SECURITY.md) for details.

---

## License

[Mozilla Public License 2.0](LICENSE)
