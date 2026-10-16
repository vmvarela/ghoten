#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=/dev/null
source "$ROOT/ghoten.env"

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

LEGACY="$WORK/legacy"
mkdir -p "$ROOT/patches"

git clone --quiet --filter=blob:none https://github.com/vmvarela/ghoten.git "$LEGACY"
git -C "$LEGACY" checkout --quiet "$LEGACY_GHOTEN_COMMIT"

git -C "$LEGACY" remote add opentofu https://github.com/opentofu/opentofu.git
git -C "$LEGACY" fetch --quiet --depth=1 opentofu "$OPENTOFU_COMMIT"

# Normalize only the historical fork-wide Go package/module renames. Product
# behavior, branding strings, tests and Ghoten-specific files remain in the
# generated patch.
if [[ -d "$LEGACY/cmd/ghoten" ]]; then
  git -C "$LEGACY" mv cmd/ghoten cmd/tofu
fi
if [[ -d "$LEGACY/internal/ghoten" ]]; then
  git -C "$LEGACY" mv internal/ghoten internal/tofu
fi
if [[ -d "$LEGACY/internal/ghotenmigrate" ]]; then
  git -C "$LEGACY" mv internal/ghotenmigrate internal/tofumigrate
fi

# Rewrite the fork module itself, but do not touch similarly named external
# modules such as github.com/vmvarela/ghoten-oras-backend.
if grep -q '^module github.com/vmvarela/ghoten$' "$LEGACY/go.mod"; then
  sed -i 's#^module github.com/vmvarela/ghoten$#module github.com/opentofu/opentofu#' "$LEGACY/go.mod"
fi

while IFS= read -r -d '' file; do
  case "$file" in
    *.pb.go|*.proto) continue ;;
  esac
  sed -i 's#github.com/vmvarela/ghoten/#github.com/opentofu/opentofu/#g' "$LEGACY/$file"
done < <(git -C "$LEGACY" grep -Ilz 'github.com/vmvarela/ghoten/' -- . || true)

# Normalize the two Go packages that the historical fork renamed. Qualifier
# rewrites are scoped only to files importing those exact packages so
# internal/legacy/ghoten and other unrelated names are left untouched.
while IFS= read -r -d '' file; do
  sed -i 's#github.com/opentofu/opentofu/internal/ghoten#github.com/opentofu/opentofu/internal/tofu#g' "$LEGACY/$file"
done < <(git -C "$LEGACY" grep -Ilz 'github.com/opentofu/opentofu/internal/ghoten' -- . || true)

while IFS= read -r -d '' file; do
  sed -i 's/\bghoten\./tofu./g' "$LEGACY/$file"
done < <(git -C "$LEGACY" grep -Ilz 'github.com/opentofu/opentofu/internal/tofu' -- '*.go' || true)

while IFS= read -r -d '' file; do
  sed -i 's#github.com/opentofu/opentofu/internal/ghotenmigrate#github.com/opentofu/opentofu/internal/tofumigrate#g' "$LEGACY/$file"
done < <(git -C "$LEGACY" grep -Ilz 'github.com/opentofu/opentofu/internal/ghotenmigrate' -- . || true)

while IFS= read -r -d '' file; do
  sed -i 's/\bghotenmigrate\./tofumigrate./g' "$LEGACY/$file"
done < <(git -C "$LEGACY" grep -Ilz 'github.com/opentofu/opentofu/internal/tofumigrate' -- '*.go' || true)

while IFS= read -r -d '' file; do
  sed -i 's/^package ghoten$/package tofu/' "$LEGACY/$file"
done < <(git -C "$LEGACY" grep -Ilz '^package ghoten$' -- internal/tofu || true)

while IFS= read -r -d '' file; do
  sed -i 's/^package ghotenmigrate$/package tofumigrate/' "$LEGACY/$file"
done < <(git -C "$LEGACY" grep -Ilz '^package ghotenmigrate

# Product delta only. The allowlist is the architecture boundary: if a
# Ghoten feature requires another upstream file, it must be added explicitly.
mapfile -t PATCH_PATHS < <(grep -Ev '^[[:space:]]*(#|$)' "$ROOT/patches/include.txt")
git -C "$LEGACY" diff --cached --binary "$OPENTOFU_COMMIT" -- "${PATCH_PATHS[@]}" > "$ROOT/patches/ghoten.patch"

if [[ ! -s "$ROOT/patches/ghoten.patch" ]]; then
  echo "ERROR: generated patch is empty" >&2
  exit 1
fi

echo "Generated patches/ghoten.patch"
echo "  Ghoten:   $GHOTEN_VERSION @ $LEGACY_GHOTEN_COMMIT"
echo "  OpenTofu: $OPENTOFU_REF @ $OPENTOFU_COMMIT"
 -- internal/tofumigrate || true)

# Pin the external ORAS adapter/state engine used by the new architecture.
# Downloading the exact module revision also records its checksums in go.sum.
(
  cd "$LEGACY"
  GOWORK=off go mod edit -require="github.com/vmvarela/ghoten-oras-backend@$ORAS_BACKEND_VERSION"
  GOWORK=off go mod download "github.com/vmvarela/ghoten-oras-backend@$ORAS_BACKEND_VERSION"
)

git -C "$LEGACY" add -A

# Product delta only. The allowlist is the architecture boundary: if a
# Ghoten feature requires another upstream file, it must be added explicitly.
mapfile -t PATCH_PATHS < <(grep -Ev '^[[:space:]]*(#|$)' "$ROOT/patches/include.txt")
git -C "$LEGACY" diff --cached --binary "$OPENTOFU_COMMIT" -- "${PATCH_PATHS[@]}" > "$ROOT/patches/ghoten.patch"

if [[ ! -s "$ROOT/patches/ghoten.patch" ]]; then
  echo "ERROR: generated patch is empty" >&2
  exit 1
fi

echo "Generated patches/ghoten.patch"
echo "  Ghoten:   $GHOTEN_VERSION @ $LEGACY_GHOTEN_COMMIT"
echo "  OpenTofu: $OPENTOFU_REF @ $OPENTOFU_COMMIT"
