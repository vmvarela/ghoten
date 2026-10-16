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

# Normalize historical fork-wide renames so the patch contains Ghoten
# behavior rather than mechanical module/package renames.
if [[ -d "$LEGACY/cmd/ghoten" ]]; then
  git -C "$LEGACY" mv cmd/ghoten cmd/tofu
fi
if [[ -d "$LEGACY/internal/ghoten" ]]; then
  git -C "$LEGACY" mv internal/ghoten internal/tofu
fi

# Rewrite only the fork module itself. Keep similarly named external modules,
# especially github.com/vmvarela/ghoten-oras-backend, unchanged.
if grep -q '^module github.com/vmvarela/ghoten$' "$LEGACY/go.mod"; then
  sed -i 's#^module github.com/vmvarela/ghoten$#module github.com/opentofu/opentofu#' "$LEGACY/go.mod"
fi

while IFS= read -r -d '' file; do
  sed -i 's#github.com/vmvarela/ghoten/#github.com/opentofu/opentofu/#g' "$LEGACY/$file"
done < <(git -C "$LEGACY" grep -Ilz 'github.com/vmvarela/ghoten/' -- . || true)

while IFS= read -r -d '' file; do
  sed -i 's/^package ghoten$/package tofu/' "$LEGACY/$file"
done < <(git -C "$LEGACY" grep -Ilz '^package ghoten$' -- internal/tofu 2>/dev/null || true)

git -C "$LEGACY" add -A

# Runtime/build delta only. Repository docs and historical release scaffolding
# deliberately stay outside the maintained OpenTofu patch.
git -C "$LEGACY" diff --cached --binary "$OPENTOFU_COMMIT" --   cmd internal version go.mod go.sum > "$ROOT/patches/ghoten.patch"

if [[ ! -s "$ROOT/patches/ghoten.patch" ]]; then
  echo "ERROR: generated patch is empty" >&2
  exit 1
fi

echo "Generated patches/ghoten.patch"
echo "  Ghoten:   $GHOTEN_VERSION @ $LEGACY_GHOTEN_COMMIT"
echo "  OpenTofu: $OPENTOFU_REF @ $OPENTOFU_COMMIT"
