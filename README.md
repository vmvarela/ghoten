# Ghoten

> **Name origin:** *Ghoten* blends **Gh**itHub and Open**Tofu**, with a nod to [Goten](https://dragonball.fandom.com/wiki/Goten) from *Dragon Ball Z*.

[![Test](https://github.com/vmvarela/ghoten/actions/workflows/test.yaml/badge.svg)](https://github.com/vmvarela/ghoten/actions/workflows/test.yaml)
[![Release](https://img.shields.io/github/v/release/vmvarela/ghoten?logo=github)](https://github.com/vmvarela/ghoten/releases/latest)
[![License: MPL 2.0](https://img.shields.io/github/license/vmvarela/ghoten)](LICENSE)

## What is this?

Ghoten is an [OpenTofu](https://opentofu.org/) fork that adds one opinionated thing: a native `oras` backend for storing state in OCI registries (like GHCR), without extra services.

We built it for teams that already trust container registries and want fewer moving parts in Terraform/OpenTofu state management. Instead of running a custom HTTP backend, you can reuse registry auth, permissions, and auditing you already have.

> **Upstream policy:** Ghoten tracks [OpenTofu](https://github.com/opentofu/opentofu) and keeps changes focused on the ORAS backend and related automation.

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

## Documentation

- [Quickstart & installation](docs/quickstart.md)
- [GitHub Action guide](docs/github-action.md)
- [ORAS backend guide](docs/oras-backend.md)
- [Testing guide](docs/testing.md)
- [Contributing](CONTRIBUTING.md)
- [Security policy](SECURITY.md)

## Limitations

- Most production validation so far is on GHCR; other OCI registries are expected to work but are less battle-tested.
- If you need advanced backend workflows (multi-region replication policies, custom APIs), dedicated backend platforms may be a better fit.

## License

[Mozilla Public License 2.0](LICENSE)
