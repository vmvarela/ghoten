# Ghoten

> **Name origin:** *Ghoten* blends **G**it**H**ub and **O**pen**T**ofu, with a nod to [Goten](https://dragonball.fandom.com/wiki/Goten) from *Dragon Ball Z*.

[![Test](https://github.com/vmvarela/ghoten/actions/workflows/test.yaml/badge.svg)](https://github.com/vmvarela/ghoten/actions/workflows/test.yaml)
[![Release](https://img.shields.io/github/v/release/vmvarela/ghoten?logo=github)](https://github.com/vmvarela/ghoten/releases/latest)
[![License: MPL 2.0](https://img.shields.io/github/license/vmvarela/ghoten)](LICENSE)

## What is this?

Ghoten is an [OpenTofu](https://opentofu.org/) fork focused on faster, simpler infrastructure workflows. It includes a native `oras` backend for storing state in OCI registries (like GHCR) and an opt-in Smart Refresh planner for large states.

The ORAS backend lets teams reuse registry auth, permissions, and auditing instead of running a separate state service. Smart Refresh reduces unnecessary provider reads while keeping a conservative correctness fallback.

> **Upstream policy:** Ghoten tracks [OpenTofu](https://github.com/opentofu/opentofu) and keeps its changes isolated, testable, and compatible with existing HCL and providers.

## Quick Start

Build and run `ghoten` locally:

```bash
git clone https://github.com/vmvarela/ghoten.git
cd ghoten
make build
./ghoten version
```

Use ORAS backend in your HCL:

```hcl
terraform {
  backend "oras" {
    repository = "ghcr.io/acme/infra-state"
  }
}
```

Authenticate and initialize with GHCR:

```bash
echo "$GITHUB_TOKEN" | docker login ghcr.io -u YOUR_GITHUB_USER --password-stdin
./ghoten init
./ghoten plan
```

## Why this approach?

- **Registry-first state**: state, locks, and versions are OCI artifacts.
- **Operationally simple**: no separate backend service to deploy and maintain.
- **Safe by default**: locking, retries, and optional compression are built in.
- **Works in GitHub Actions**: action handles install, auth, init, PR comments, and summaries.

## Smart Refresh

Ghoten includes an opt-in `-refresh=smart` mode for large infrastructures. The normal default remains a full refresh.

```bash
# Refresh all managed resources (default, OpenTofu-compatible behavior)
ghoten plan

# Skip refresh entirely
ghoten plan -refresh=false

# Reuse only resource state that Smart Refresh can prove is still safe
ghoten plan -refresh=smart
```

Smart Refresh uses two levels of filtering:

1. A cheap dependency-graph pass selects obviously affected resources and their evaluation closure.
2. Before any remaining `ReadResource` call is skipped, Ghoten fingerprints the **evaluated resource instance configuration** together with the resolved provider instance/configuration, schema version, and dependency signature.

That second check means changes from variables, tfvars, locals, module inputs, `count.index`, `each.key` / `each.value`, and evaluated provider configuration (for example region, profile, project, cluster, organization, or endpoint when represented in provider config) invalidate the cached state even when the HCL source itself did not change.

Provider context that is completely opaque to Core — for example credentials or account selection changed only through an external mechanism the provider reads internally — cannot be fingerprinted generically without changing the provider protocol. The 24-hour freshness bound guarantees that even such cases are eventually re-read; use the default full refresh when immediate external drift/account detection is required.

The fingerprint and last successful remote refresh time are stored as optional state metadata. Old states remain readable; missing or unknown metadata forces a conservative refresh and is populated after a successful read. Matching entries are reused for at most **24 hours**, after which Ghoten refreshes them again to bound undetected drift.

Smart Refresh is intentionally not supported with `-destroy` or `-refresh-only`; those modes require complete remote-state synchronization. Use `-refresh=true` for them.

The plan/apply output reports how many resource refreshes were performed and skipped.

## Documentation

- [Quickstart & installation](docs/quickstart.md)
- [GitHub Action guide](docs/github-action.md)
- [ORAS backend guide](docs/oras-backend.md)
- [Testing guide](docs/testing.md)
- [Contributing](CONTRIBUTING.md)
- [Security policy](SECURITY.md)

## Limitations

- Validated against GHCR and [Zot](https://zotregistry.dev/); other OCI registries are expected to work but have not been tested yet. See the [compatibility matrix](docs/oras-backend.md#verified-oci-registries).
- If you need advanced backend workflows (multi-region replication policies, custom APIs), dedicated backend platforms may be a better fit.

## License

[Mozilla Public License 2.0](LICENSE)
