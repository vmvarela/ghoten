#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=/dev/null
source "$ROOT/ghoten.env"

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

LEGACY="$WORK/legacy"
BASE="$WORK/base"
PRODUCT_PATCH="$WORK/product.patch"
DEPS_PATCH="$WORK/deps.patch"
mkdir -p "$ROOT/patches"

git clone --quiet --filter=blob:none https://github.com/vmvarela/ghoten.git "$LEGACY"
git -C "$LEGACY" checkout --quiet "$LEGACY_GHOTEN_COMMIT"
git -C "$LEGACY" remote add opentofu https://github.com/opentofu/opentofu.git
git -C "$LEGACY" fetch --quiet --depth=1 opentofu "$OPENTOFU_COMMIT"

# Normalize only historical fork-wide Go identifiers. Never rewrite generated
# protobuf descriptors: module paths embedded there are length-delimited bytes.
if [[ -d "$LEGACY/cmd/ghoten" ]]; then
  git -C "$LEGACY" mv cmd/ghoten cmd/tofu
fi
if [[ -d "$LEGACY/internal/ghoten" ]]; then
  git -C "$LEGACY" mv internal/ghoten internal/tofu
fi
if [[ -d "$LEGACY/internal/ghotenmigrate" ]]; then
  git -C "$LEGACY" mv internal/ghotenmigrate internal/tofumigrate
fi

if grep -q '^module github.com/vmvarela/ghoten$' "$LEGACY/go.mod"; then
  sed -i 's#^module github.com/vmvarela/ghoten$#module github.com/opentofu/opentofu#' "$LEGACY/go.mod"
fi

while IFS= read -r -d '' file; do
  case "$file" in
    *.pb.go|*.proto) continue ;;
  esac
  sed -i 's#github.com/vmvarela/ghoten/#github.com/opentofu/opentofu/#g' "$LEGACY/$file"
done < <(git -C "$LEGACY" grep -Ilz 'github.com/vmvarela/ghoten/' -- . || true)

while IFS= read -r -d '' file; do
  sed -i 's#github.com/opentofu/opentofu/internal/ghoten#github.com/opentofu/opentofu/internal/tofu#g' "$LEGACY/$file"
  sed -i 's/\bghoten\./tofu./g' "$LEGACY/$file"
done < <(git -C "$LEGACY" grep -Ilz 'github.com/opentofu/opentofu/internal/ghoten' -- '*.go' || true)

while IFS= read -r -d '' file; do
  sed -i 's#github.com/opentofu/opentofu/internal/ghotenmigrate#github.com/opentofu/opentofu/internal/tofumigrate#g' "$LEGACY/$file"
  sed -i 's/\bghotenmigrate\./tofumigrate./g' "$LEGACY/$file"
done < <(git -C "$LEGACY" grep -Ilz 'github.com/opentofu/opentofu/internal/ghotenmigrate' -- '*.go' || true)

while IFS= read -r -d '' file; do
  sed -i 's/^package ghoten$/package tofu/' "$LEGACY/$file"
done < <(git -C "$LEGACY" grep -Ilz '^package ghoten$' -- internal/tofu || true)

while IFS= read -r -d '' file; do
  sed -i 's/^package ghotenmigrate$/package tofumigrate/' "$LEGACY/$file"
done < <(git -C "$LEGACY" grep -Ilz '^package ghotenmigrate$' -- internal/tofumigrate || true)

git -C "$LEGACY" add -A
mapfile -t PATCH_PATHS < <(grep -Ev '^[[:space:]]*(#|$)' "$ROOT/patches/include.txt")

# Product code comes from Ghoten; module metadata never does.
git -C "$LEGACY" diff --cached --binary "$OPENTOFU_COMMIT" -- "${PATCH_PATHS[@]}" > "$PRODUCT_PATCH"

# Module metadata is derived from the exact clean OpenTofu base after
# applying only the product delta. This makes go.mod/go.sum a consequence of
# Ghoten features rather than inherited fork history.
git clone --quiet --filter=blob:none https://github.com/opentofu/opentofu.git "$BASE"
git -C "$BASE" checkout --quiet "$OPENTOFU_COMMIT"
git -C "$BASE" apply "$PRODUCT_PATCH"
(
  cd "$BASE"
  GOWORK=off go mod edit -require="github.com/vmvarela/ghoten-oras-backend@$ORAS_BACKEND_VERSION"
  GOWORK=off go mod tidy
)
git -C "$BASE" diff --binary "$OPENTOFU_COMMIT" -- go.mod go.sum > "$DEPS_PATCH"

cat "$PRODUCT_PATCH" "$DEPS_PATCH" > "$ROOT/patches/ghoten.patch"

if [[ ! -s "$ROOT/patches/ghoten.patch" ]]; then
  echo "ERROR: generated patch is empty" >&2
  exit 1
fi

echo "Generated patches/ghoten.patch"
echo "  Ghoten feature source: $LEGACY_GHOTEN_COMMIT"
echo "  OpenTofu base:         $OPENTOFU_REF @ $OPENTOFU_COMMIT"
echo "  ORAS adapter:          $ORAS_BACKEND_VERSION"
