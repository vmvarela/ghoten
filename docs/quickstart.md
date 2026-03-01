# Quickstart

## What is this?

This guide gets you from clone to a working `ghoten plan` with an OCI-backed state in minutes. It focuses on the happy path and skips optional flags.

If you need full backend tuning (`retry`, `rate_limit`, TLS options), use the ORAS backend guide after this one.

## Local install in 4 commands

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

## Next steps

- For CI pipelines, continue with [GitHub Action guide](github-action.md).
- For backend details and wire format, continue with [ORAS backend guide](oras-backend.md).
- For running tests locally and in CI, continue with [Testing guide](testing.md).
