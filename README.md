# Ghoten

Ghoten is a thin, reproducible distribution of OpenTofu focused on two features:

- **Smart Refresh** — skips safe, fresh provider reads during planning.
- **OCI state** — adds the `oras` backend, powered by the shared `oras-state` library.

Ghoten does not vendor or rename the OpenTofu source tree.

## Architecture

A build starts from one exact OpenTofu commit and applies two independent patches:

```text
OpenTofu <version pinned in ghoten.env>
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

## Install

Install the latest stable release on Linux or macOS:

```sh
curl -sSfL https://raw.githubusercontent.com/vmvarela/ghoten/master/scripts/install.sh | sh
```

The installer selects the published binary for the current OS and architecture, verifies it against the release `sha256sums.txt`, and installs only the `ghoten` executable. It never builds OpenTofu or applies patches locally.

Use a writable destination or install a specific published version when needed:

```sh
curl -sSfL https://raw.githubusercontent.com/vmvarela/ghoten/master/scripts/install.sh | INSTALL_DIR="$HOME/.local/bin" sh
curl -sSfL https://raw.githubusercontent.com/vmvarela/ghoten/master/scripts/install.sh | VERSION=1.13.1 INSTALL_DIR="$HOME/.local/bin" sh
```

Supported installer targets are Linux amd64/arm64 and macOS amd64/arm64. Windows uses the published ZIP artifact.

Homebrew users can install the same stable release artifacts through the maintained tap:

```sh
brew install vmvarela/tap/ghoten
```

Linux users can install stable releases from the maintained `vmvarela` APT repository, shared with sql-pipe:

```sh
sudo mkdir -p /etc/apt/keyrings
curl -fsSL https://vmvarela.github.io/apt-packages/key.gpg \
  | sudo tee /etc/apt/keyrings/vmvarela.asc
echo "deb [signed-by=/etc/apt/keyrings/vmvarela.asc] https://vmvarela.github.io/apt-packages stable main" \
  | sudo tee /etc/apt/sources.list.d/vmvarela.list
sudo apt update
sudo apt install ghoten
```

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

Ghoten follows exactly one OpenTofu line: the globally latest stable SemVer release.

The scheduled `OpenTofu update` workflow checks upstream daily. When a newer stable version becomes the global latest, it opens a dependency pull request that updates `GHOTEN_VERSION`, `OPENTOFU_REF`, and `OPENTOFU_COMMIT` together. Backport or fix releases for older minor lines are ignored.

The update pull request never changes patches automatically and never merges or publishes a release. `Verify` is the compatibility gate: if a patch no longer applies, adapt only that patch and rerun verification.

A base-version update should not require a fork merge.

## Releases

OpenTofu owns Ghoten's compatibility version. Ghoten does not calculate its own stable SemVer from its changes.

For a new upstream stable release:

1. Let the `OpenTofu update` workflow open the version bump pull request.
2. Adapt patches only if `Verify` requires it, then merge the green pull request.
3. Dispatch `Release` as a prerelease, normally with `rc.1`, and test the published binaries.
4. When the prerelease is satisfactory, dispatch `Release` as `stable`.

The release workflow independently verifies that `ghoten.env` points to OpenTofu's globally latest stable SemVer release and that the pinned commit is exactly the commit behind the upstream tag. Stable Ghoten releases therefore use the same base version as the OpenTofu release they wrap.

Release Drafter prepares notes only under the neutral `next` draft. It never chooses a Ghoten version. Prereleases do not consume that stable-release draft.

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
