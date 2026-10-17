# Ghoten

Ghoten is a thin, reproducible distribution of OpenTofu focused on two features:

- **Smart Refresh** — skips safe, fresh provider reads during planning.
- **OCI state** — adds the `oras` backend, powered by the shared `oras-state` library.

Ghoten does not vendor or rename the OpenTofu source tree.

## Architecture

A build starts from one exact OpenTofu commit and applies two independent patches:

```text
OpenTofu 1.13.0
    |
    +-- patches/smart-refresh.patch
    |
    +-- patches/oras.patch
    |
    +-- build cmd/tofu as dist/ghoten
```

The separation is intentional:

- `smart-refresh.patch` owns planning, state metadata, CLI refresh modes, and Smart Refresh tests.
- `oras.patch` owns only the ORAS backend adapter, backend registration, and its Go dependency.

There is no maintained fork-wide source copy and no migration/generation layer.

## Build

```sh
make build
./dist/ghoten version
```

The build clones the exact OpenTofu commit pinned in `ghoten.env`, checks both patches, applies them, and builds one executable.

## Verify

```sh
make verify
```

Verification starts from a clean OpenTofu checkout, applies the same two patches, runs the focused upstream/Ghoten test suites, and builds the executable.

## Updating OpenTofu

1. Change `GHOTEN_VERSION`, `OPENTOFU_REF`, and `OPENTOFU_COMMIT` in `ghoten.env`.
2. Rebase only the patch that no longer applies.
3. Run `make verify`.

A base-version update should not require a fork merge.

## Read-only ORAS state history

When the active backend is `oras` and version retention is enabled with `max_versions`, Ghoten exposes retained state history without exposing OCI tag details:

```sh
ghoten history list
ghoten history list --json
ghoten history show 3
ghoten history show --json 3
ghoten history pull --output recovery.tfstate 3
```

The commands are deliberately read-only. `history list` returns metadata only and never state contents; `history show` explicitly displays one retained state; and `history pull` writes one version to a new file without overwriting an existing file.

Backends that do not expose the optional history capability return a clear error. Restore, deletion, and garbage collection are intentionally outside this MVP.

## Updating oras-state

Update the shared ORAS state engine with one command:

```sh
make update-oras-state VERSION=v0.2.0
```

The updater always starts from the exact OpenTofu commit pinned in `ghoten.env`, applies the current ORAS patch, lets `go get` and `go mod tidy` resolve the requested `oras-state` version and checksums, regenerates only the `go.mod` and `go.sum` hunks in `patches/oras.patch`, and finishes by running the normal Ghoten verification gate.

No OpenTofu source tree is retained or committed.

## Design rules

- Ghoten version tracks its OpenTofu base version.
- OpenTofu source is never committed here.
- Published artifacts contain one executable: `ghoten`.
- Smart Refresh and ORAS remain independent patches.
- OCI state semantics live in `oras-state`, not OpenTofu Core.
- Generic OpenTofu changes belong upstream, not in Ghoten.
