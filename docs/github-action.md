# GitHub Action Guide

## What is this?

`vmvarela/ghoten@v1` is the fastest way to run OpenTofu with OCI-backed state in GitHub Actions. It installs `ghoten`, authenticates to GHCR, initializes the ORAS backend, runs your command, and can post PR comments plus a job summary.

Use the action unless you have a strong reason to script every step manually. You get fewer moving parts and fewer auth mistakes.

## Minimal PR plan workflow

Use this when you want plan output in pull requests with almost no setup:

```yaml
name: Infra Plan
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

This defaults to `command: plan`, posts a PR comment, and writes a Job Summary.

## Plan on PR, apply on main

Use this split when you want safe review before mutation:

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

## Inputs you will actually tweak

Most users only need these:

| Input | Default | Why you change it |
|---|---|---|
| `command` | `plan` | Switch to `apply`, `destroy`, `fmt`, or `validate` |
| `working-directory` | `.` | Monorepo or nested IaC folders |
| `backend-repository` | `ghcr.io/<owner>/tf-state.<repo>` | Shared state repo naming conventions |
| `var-file` | `""` | Load environment-specific variables |
| `variables` | `""` | Inject small runtime values in CI |
| `comment-on-pr` | `true` | Disable PR noise in very large repos |
| `add-summary` | `true` | Disable if you already publish custom summaries |

The backend-related defaults (`compression=gzip`, `lock-ttl=300`, `max-versions=10`) are sensible for most teams. Keep them unless you have measured reasons to change.

## Useful output wiring

Use action outputs to gate later steps:

```yaml
- uses: vmvarela/ghoten@v1
  id: plan
  with: { command: plan }

- uses: vmvarela/ghoten@v1
  if: steps.plan.outputs.plan-has-changes == 'true'
  with: { command: apply }
```

## Permissions checklist

- `contents: read` for checkout.
- `packages: write` for GHCR state read/write/delete.
- `pull-requests: write` only if you want PR comments.

If you enable aggressive retention/deletion workflows, ensure the token has enough package permissions in your org policy.
