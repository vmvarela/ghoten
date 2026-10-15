# Ghoten

> **Name origin:** *Ghoten* blends **G**it**H**ub and **O**pen**T**ofu, with a nod to [Goten](https://dragonball.fandom.com/wiki/Goten) from *Dragon Ball Z*.

[![Test](https://github.com/vmvarela/ghoten/actions/workflows/test.yaml/badge.svg)](https://github.com/vmvarela/ghoten/actions/workflows/test.yaml)
[![Release](https://img.shields.io/github/v/release/vmvarela/ghoten?logo=github)](https://github.com/vmvarela/ghoten/releases/latest)
[![License: MPL 2.0](https://img.shields.io/github/license/vmvarela/ghoten)](LICENSE)

## What is this?

Ghoten is an [OpenTofu](https://opentofu.org/) fork that adds one opinionated thing: a native `oras` backend for storing state in OCI registries (like GHCR), without extra services.

We built it for teams that already trust container registries and want fewer moving parts in Terraform/OpenTofu state management. Instead of running a custom HTTP backend, you can reuse registry auth, permissions, and auditing you already have.

> **Upstream policy:** Ghoten tracks [OpenTofu](https://github.com/opentofu/opentofu) while keeping its fork-specific changes focused on OCI state and opt-in performance experiments such as Smart Refresh.

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

Ghoten includes an opt-in `-refresh=smart` mode for large infrastructures:

```bash
# Full refresh (default and OpenTofu-compatible behavior)
ghoten plan

# Skip refresh entirely
ghoten plan -refresh=false

# Selective refresh using evaluated per-instance fingerprints
ghoten plan -refresh=smart
```

Smart Refresh does **not** rely on HCL source text. For each eligible managed resource instance it fingerprints the evaluated configuration (including variables, locals, module inputs, `count.index`, `each.key` and `each.value`), the resolved provider instance and its evaluated configuration, the resource schema version, dependencies, and the supported lifecycle values (`create_before_destroy` and `destroy`).

A remote `ReadResource` call is skipped only when the fingerprint matches the last successful synchronization and that synchronization is less than 24 hours old. Missing/unknown fingerprints and stale entries refresh conservatively. Resources involving protected values (`sensitive` or `ephemeral`/write-only), advanced lifecycle semantics (`ignore_changes`, `replace_triggered_by`, `prevent_destroy`), custom conditions, data sources, orphaned objects and deposed objects also refresh conservatively.

A normal `plan` keeps OpenTofu's non-mutating state semantics: it does not write a newly refreshed fingerprint/timestamp back to the backend. That metadata becomes durable when the refreshed state is subsequently applied (including a saved plan that is applied). Therefore, once the 24-hour freshness window expires, repeated standalone plans will continue to refresh until a corresponding state update is persisted.

`-refresh-only -refresh=smart` is normalized to a full refresh because refresh-only promises state reconciliation. `-destroy -refresh=smart` is rejected; destroy currently requires a full pre-destroy refresh.

The output reports refreshed vs. skipped resources, making the API-call reduction visible.

Provider fingerprints cover the provider configuration value visible to OpenTofu Core and the resolved provider instance. Legacy provider protocols do not expose every provider-internal context derived from external environment (for example, an account selected only through credentials/environment variables). If that external context changes independently of HCL, use a full refresh; the 24-hour freshness bound limits, but cannot eliminate, that protocol-level blind spot.

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
