# Ghoten

> **Name origin:** *Ghoten* blends **Gh**itHub and Open**Tofu**, with a nod to [Goten](https://dragonball.fandom.com/wiki/Goten) from *Dragon Ball Z*.

[![Test](https://github.com/vmvarela/ghoten/actions/workflows/test.yaml/badge.svg)](https://github.com/vmvarela/ghoten/actions/workflows/test.yaml)
[![Release](https://img.shields.io/github/v/release/vmvarela/ghoten?logo=github)](https://github.com/vmvarela/ghoten/releases/latest)
[![License: MPL 2.0](https://img.shields.io/github/license/vmvarela/ghoten)](LICENSE)

**An [OpenTofu](https://opentofu.org/) fork with a native ORAS backend for OCI registry state storage.**

Ghoten adds a first-class **`oras`** backend that speaks the [OCI distribution protocol](https://oras.land/) directly. Use an OCI-compatible registry — such as GitHub Container Registry (GHCR) — as the durable store for your infrastructure state, without external HTTP backends or third-party wrappers.

> **Upstream**: Ghoten tracks [OpenTofu](https://github.com/opentofu/opentofu) and carries only the additions required for the ORAS backend. All standard OpenTofu functionality is preserved.

---

## Key Features

- **Native OCI storage** — State, locks, and versions stored as OCI artifacts via [ORAS](https://oras.land/)
- **Atomic locking** — Generation semantics prevent concurrent lock holders
- **Stale lock cleanup (TTL)** — Crashed-process locks auto-cleared on next acquisition
- **State versioning** — Historical snapshots with async background retention cleanup
- **Gzip compression** — Optional state compression before pushing
- **Rate limiting & retry** — Token-bucket limiter + exponential backoff for transient errors
- **GHCR deletion fallback** — Falls back to GitHub Packages API when OCI `DELETE` returns 405
- **Docker credential helpers** — Reuses Docker config with caching and singleflight deduplication
- **OpenTelemetry tracing** — All OCI HTTP calls instrumented via `otelhttp`
- **Client-side encryption** — Combine with [OpenTofu state encryption](https://opentofu.org/docs/language/state/encryption/)

---

## Installation

```bash
# Build from source
git clone https://github.com/vmvarela/ghoten.git && cd ghoten
make build

# Or pull Docker images
docker pull ghcr.io/vmvarela/ghoten:<version>          # Alpine (git/bash/openssh)
docker pull ghcr.io/vmvarela/ghoten:<version>-minimal   # Scratch (binary only)
```

Pre-built binaries for 17 platform combinations are available on the [Releases](https://github.com/vmvarela/ghoten/releases) page.

---

## GitHub Action

Run Ghoten in your workflows with zero configuration — the action installs the binary, authenticates to GHCR, initializes the ORAS backend, and runs your command with **PR comments** and **Job Summaries** out of the box.

Your HCL only needs an empty backend block — the action injects `repository`, `compression`, `lock_ttl`, and `max_versions` automatically:

```hcl
terraform {
  backend "oras" {}
}
```

### Quick Start

```yaml
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

The action will install Ghoten, authenticate to GHCR, init with the ORAS backend (`ghcr.io/<owner>/tf-state.<repo>`), run `ghoten plan`, post a PR comment, and generate a Job Summary.

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
    permissions: { contents: read, packages: write, pull-requests: write }
    steps:
      - uses: actions/checkout@v6
      - uses: vmvarela/ghoten@v1

  apply:
    if: github.ref == 'refs/heads/main'
    runs-on: ubuntu-latest
    permissions: { contents: read, packages: write }
    steps:
      - uses: actions/checkout@v6
      - uses: vmvarela/ghoten@v1
        with:
          command: apply
```

### Inputs

| Input | Default | Description |
|---|---|---|
| `command` | `plan` | `plan`, `apply`, `destroy`, `fmt`, `validate` |
| `working-directory` | `.` | Path to HCL configuration files |
| `workspace` | `default` | Terraform workspace |
| `var-file` | | Path to a `.tfvars` file |
| `variables` | | Newline-separated `key=value` pairs |
| `backend-repository` | `ghcr.io/<owner>/tf-state.<repo>` | ORAS backend OCI repository |
| `backend-config` | | Additional `-backend-config` key=value pairs |
| `github-token` | `${{ github.token }}` | Token for GHCR auth and PR comments |
| `comment-on-pr` | `true` | Post output as PR comment |
| `add-summary` | `true` | Generate Job Summary |
| `args` | | Additional CLI arguments |
| `init-args` | | Additional arguments for `ghoten init` |
| `auto-init` | `true` | Automatically run `ghoten init` |
| `compression` | `gzip` | State compression: `none` or `gzip` |
| `lock-ttl` | `300` | Lock TTL in seconds |
| `max-versions` | `10` | State versions to retain |
| `fmt-check` | `false` | Use `-check` mode for fmt |
| `version` | *(auto)* | Ghoten version to install |

### Outputs

| Output | Description |
|---|---|
| `exitcode` | Exit code of the command |
| `stdout` | Full command output |
| `plan-has-changes` | `true`/`false` — whether plan detected changes |
| `plan-file` | Path to binary plan file (when `command=plan`) |
| `fmt-result` | `true`/`false` — whether fmt found differences |

### More Examples

```yaml
# Format check
- uses: vmvarela/ghoten@v1
  with: { command: fmt, fmt-check: true }

# With variables
- uses: vmvarela/ghoten@v1
  with:
    command: plan
    var-file: prod.tfvars
    variables: |
      region=eu-west-1
      environment=production

# Custom backend repository
- uses: vmvarela/ghoten@v1
  with:
    command: plan
    backend-repository: ghcr.io/myorg/custom-state-repo

# Plan → Apply in same job
- uses: vmvarela/ghoten@v1
  id: plan
  with: { command: plan }
- uses: vmvarela/ghoten@v1
  if: steps.plan.outputs.plan-has-changes == 'true'
  with: { command: apply }
```

### Permissions

| Permission | Required for |
|---|---|
| `contents: read` | Repository checkout |
| `packages: write` | ORAS backend (read/write/delete state on GHCR) |
| `pull-requests: write` | PR comments (optional) |

> `packages: write` covers version retention cleanup. The separate `delete:packages` permission is only needed to delete entire packages from GHCR.

---

## ORAS Backend

### Configuration

| Parameter | Type | Default | Env Var | Description |
|---|---|---|---|---|
| `repository` | string | `""` | `TF_BACKEND_ORAS_REPOSITORY` | OCI repository (`<registry>/<repo>`). **Required.** |
| `compression` | string | `"none"` | — | `none` or `gzip` |
| `insecure` | bool | `false` | — | Skip TLS verification |
| `ca_file` | string | `""` | — | PEM-encoded CA bundle path |
| `retry_max` | int | `2` | `TF_BACKEND_ORAS_RETRY_MAX` | Retries for transient errors |
| `retry_wait_min` | int | `1` | `TF_BACKEND_ORAS_RETRY_WAIT_MIN` | Min backoff (seconds) |
| `retry_wait_max` | int | `30` | `TF_BACKEND_ORAS_RETRY_WAIT_MAX` | Max backoff (seconds) |
| `lock_ttl` | int | `0` | `TF_BACKEND_ORAS_LOCK_TTL` | Lock TTL (seconds). `> 0` enables stale-lock clearing |
| `rate_limit` | int | `0` | `TF_BACKEND_ORAS_RATE_LIMIT` | Max requests/sec. `0` disables |
| `rate_limit_burst` | int | `0` | `TF_BACKEND_ORAS_RATE_LIMIT_BURST` | Burst size |
| `max_versions` | int | `0` | — | State versions to retain. `0` disables |

### Usage Examples

**With the GitHub Action** — just declare an empty backend; the action provides everything via env vars and `-backend-config`:

```hcl
terraform {
  backend "oras" {}
}
```

**Standalone — minimal:**

```hcl
terraform {
  backend "oras" {
    repository = "ghcr.io/acme/infra-state"
  }
}
```

**Standalone — production-ready:**

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

> **Tip**: For production, prefer a KMS-backed key provider (AWS KMS, GCP KMS, etc.) over PBKDF2.

### Authentication

Credentials are discovered in order:

1. **Docker credential helpers** — Docker config / credential store (cached, singleflight-deduplicated)
2. **CLI host credentials** — OpenTofu/Terraform login tokens (`ghoten login`)

### How State Is Stored

Tags act as stable references inside the OCI repository:

| Purpose | Tag pattern |
|---|---|
| State | `state-<workspace>` |
| Lock | `locked-<workspace>` |
| Version | `state-<workspace>-v<N>` |

> If the workspace name is not a valid OCI tag, a `ws-<sha256>` form is used instead, with the real name persisted in OCI annotations.

<details><summary><b>Wire format details</b></summary>

**State objects** (ORAS manifest v1.1):
- `artifactType`: `application/vnd.terraform.state.v1`
- Layer: `application/vnd.terraform.statefile.v1` (or `+gzip`)
- Annotations: `org.terraform.workspace`, `org.terraform.state.updated_at`

**Lock objects**:
- `artifactType`: `application/vnd.terraform.lock.v1`
- Annotations: `org.terraform.workspace`, `org.terraform.lock.id`, `org.terraform.lock.info`, `org.terraform.lock.generation`

</details>

### Security

- **Enable [client-side encryption](https://opentofu.org/docs/language/state/encryption/)** — encrypts state before it leaves your machine
- Keep the repository **private** with **least-privilege** tokens
- Prefer registries with **encryption at rest** and **audit trails**

> **Note**: Primarily tested against GHCR. Other OCI registries should work but are not yet validated.

---

## Testing

```bash
go test ./...                                        # Full suite
go test ./internal/backend/remote-state/oras/...     # ORAS backend (in-memory fake registry)
```

---

## Troubleshooting

| Problem | Solution |
|---|---|
| **"unauthorized" / "denied"** | Verify `docker login <registry>`. For GHCR: `read:packages` + `write:packages` (+ `delete:packages` if retention enabled) |
| **Lock stuck after crash** | Set `lock_ttl = 300` — next run auto-clears it. Or delete `locked-<workspace>` tag manually |
| **Version deletion fails on GHCR** | Backend falls back to GitHub Packages API automatically. Ensure `delete:packages` permission |
| **Debug output** | `TF_LOG=DEBUG ghoten plan` |

---

## Contributing

- [GitHub Issues](https://github.com/vmvarela/ghoten/issues) · [Contributing Guide](CONTRIBUTING.md) · [Code of Conduct](CODE_OF_CONDUCT.md)
- For upstream questions: [OpenTofu Discussions](https://github.com/orgs/opentofu/discussions)

## Security

Report vulnerabilities via [GitHub Security Advisories](https://github.com/vmvarela/ghoten/security/advisories/new). See [SECURITY.md](SECURITY.md).

## License

[Mozilla Public License 2.0](LICENSE)
