# Agent Instructions

### Notes

- `golang-pro` applies to **all Go work** in this repo: implementation, tests, refactors, and code review.
- When multiple skills apply (e.g. writing a Go algorithm with formal derivation), load both.

## Pre-commit checklist

Run **all** of the following before every commit. Fix all failures before committing.

```bash
# 1. Unit tests (race detector, no cache, reasonable timeout)
go test -race -count=1 -timeout 30m ./...

# 2. Linter
make golangci-lint

# 3. Module tidiness (only if dependencies changed)
go mod tidy

# 4. Generated files (only if //go:generate sources changed)
make generate-check
```

## Conventions

- Branch names: `issue-{number}/{short-description}`
- Commits: [Conventional Commits](https://www.conventionalcommits.org/) (`feat:`, `fix:`, `ci:`, `chore:`, `docs:`, etc.)
- PRs: squash merge, delete branch after merge
