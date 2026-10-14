# Quickstart

## What is this?

This guide gets you from clone to a working `ghoten plan` with an OCI-backed state in minutes. It focuses on the happy path and skips optional flags.

If you need full backend tuning (`retry`, `rate_limit`, TLS options), use the ORAS backend guide after this one.

## Install the binary

The fastest way to install `ghoten` is the one-liner:

```bash
curl -sSfL https://raw.githubusercontent.com/vmvarela/ghoten/master/scripts/install.sh | sh
```

To install a specific version or to a custom directory:

```bash
VERSION=1.12.0 INSTALL_DIR=./bin curl -sSfL https://raw.githubusercontent.com/vmvarela/ghoten/master/scripts/install.sh | sh
```

## Build from source

If you prefer to build from source:

```bash
git clone https://github.com/vmvarela/ghoten.git
cd ghoten
make build
./ghoten version
```

For CI or reproducible local environments, you can also use the published image:

```bash
docker pull ghcr.io/vmvarela/ghoten:<version>
```

## Minimal ORAS backend config

Start with the one setting that matters: the OCI repository.

```hcl
terraform {
  backend "oras" {
    repository = "ghcr.io/acme/infra-state"
  }
}
```

This is enough to run. Add compression and retention only after the basics work.

## Authenticate and run

Login once, then initialize and plan.

```bash
echo "$GITHUB_TOKEN" | docker login ghcr.io -u YOUR_GITHUB_USER --password-stdin
./ghoten init
./ghoten plan
```

When things fail, use debug logs immediately instead of guessing:

```bash
TF_LOG=DEBUG ./ghoten plan
```

## Production baseline

Once the minimal setup works, this is the baseline we recommend for most teams:

```hcl
terraform {
  backend "oras" {
    repository   = "ghcr.io/myorg/infra-state"
    compression  = "gzip"
    lock_ttl     = 300
    max_versions = 10
  }
}
```

`lock_ttl` avoids manual cleanup after crashed jobs, and `max_versions` keeps history without unbounded growth.

## Provider mirrors and checksum trust

If you use a `network_mirror` in your `.tofurc` file, `ghoten` accepts hashes the mirror reports for the platform you are installing from, but by default it ignores anything else the mirror reports. If your teams install the same configuration on multiple platforms, you can opt in to the mirror's hashes for all platforms:

```hcl
provider_installation {
  network_mirror {
    url              = "https://mirrors.example.com/"
    trust_all_hashes = true
  }
}
```

`trust_all_hashes` defaults to `false`. With it enabled, the lock file also gains hashes the mirror reports for other platforms — useful because mirrors can serve packages for platforms the current machine never installs from. Use it only for mirrors you control or explicitly trust: a compromised or mistyped mirror can report hashes for every platform it serves.

Different lock file hashes have different grounds for trust, and it helps to keep them apart:

- **Reported**: supplied by the origin registry or, with `trust_all_hashes = true`, by the network mirror. A reported hash alone does not establish local verification or a publisher signature.
- **Verified**: calculated by `ghoten` directly from the downloaded package.
- **Signed**: a hash covered by a GPG signature from a signing key reported by the provider's origin registry.

`ghoten` verifies the downloaded package against its matching hash. Hashes reported for other platforms by a trusted mirror are not checked against those packages on your machine, and they are not publisher signatures. Enabling `trust_all_hashes` opts into trusting those reports; it does not create or verify a GPG signature.

`GHOTEN_ENFORCE_GPG_VALIDATION` controls origin-registry signature policy and which newly reported hashes are eligible for inclusion in the lock file; it does not make `network_mirror` downloads require a GPG signature. When GPG signing is required, mirror-only hash reports are excluded from the lock file, while the hash verified against the package you downloaded can still be recorded. This setting does not remove hashes already present in the lock file.

## Next steps

- For CI pipelines, continue with [GitHub Action guide](github-action.md).
- For backend details and wire format, continue with [ORAS backend guide](oras-backend.md).
- For running tests locally and in CI, continue with [Testing guide](testing.md).
