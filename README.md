# Ghoten

Ghoten is a reproducible patch distribution of OpenTofu that adds Smart Refresh and OCI-native state while preserving OpenTofu version compatibility.

## Architecture

This repository does **not** vendor OpenTofu. A build:

1. reads `ghoten.env`;
2. clones the exact OpenTofu commit pinned there;
3. applies `patches/ghoten.patch`;
4. builds a single `ghoten` executable from OpenTofu's original `cmd/tofu`.

The public Ghoten version always equals its OpenTofu base version. Updating OpenTofu is therefore a version/commit change in one file followed by patch verification.

During the architecture migration, `scripts/generate-patch.sh` can reproduce the current Ghoten 1.13.0 delta from the exact legacy commit. This is a transition aid only: the long-term source of truth is the small patch stored here.

## Build

```sh
./scripts/build.sh
./dist/ghoten version
```

## Regenerate the parity patch

```sh
./scripts/generate-patch.sh
```

The generated patch is normalized back to OpenTofu package/module names before diffing, so mechanical fork renames do not become part of the maintained delta.

## Design rules

- Ghoten version == OpenTofu version.
- OpenTofu source is never committed here.
- Published artifacts contain one executable: `ghoten`.
- Ghoten-specific behavior must be isolated or represented by minimal integration points.
- ORAS state semantics belong in the shared ORAS state library, not in OpenTofu Core.
