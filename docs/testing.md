# Testing Guide

## What is this?

This guide shows the shortest path to confidence when changing Ghoten: run fast unit tests first, then targeted backend/integration tests only when your change needs them.

That order is intentional. Full integration sweeps are expensive; most regressions are caught earlier with package-level tests.

## Fast feedback loop

Start here for almost every change:

```bash
go test -v ./...
```

If you are changing ORAS backend behavior, run its focused suite directly:

```bash
go test ./internal/backend/remote-state/oras/...
```

## Coverage run

Use this when validating broad impact before release:

```bash
make test-with-coverage
```

This generates `coverage.out` and `coverage.html` at the repository root.

## Integration test targets

Use integration tests only when touching backend-specific runtime behavior.

| Target | What it validates | Key requirement |
|---|---|---|
| `make test-s3` | S3 remote-state backend behavior | AWS credentials + IAM permissions |
| `make test-gcp` | GCS remote-state backend behavior | GCP ADC + project/region env vars |
| `make test-pg` | PostgreSQL remote-state backend behavior | Docker + local port `5432` |
| `make test-consul` | Consul remote-state backend behavior | Docker |
| `make test-kubernetes` | Kubernetes backend behavior | Docker + kind prerequisites |
| `make test-zot` | ORAS backend against Zot OCI registry | Docker |

List all integration helpers with:

```bash
make list-integration-tests
```

## Recommended workflow

1. `go test -v ./...`
2. Run a focused package test for the area you changed.
3. Run one integration target only if your change touches that backend.

This keeps CI time and local feedback fast without skipping meaningful validation.
