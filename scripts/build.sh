#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=/dev/null
source "$ROOT/ghoten.env"

PATCHES=(
  "$ROOT/patches/smart-refresh.patch"
  "$ROOT/patches/oras.patch"
)

for patch in "${PATCHES[@]}"; do
  if [[ ! -s "$patch" ]]; then
    echo "ERROR: missing patch: $patch" >&2
    exit 1
  fi
done

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
SRC="$WORK/opentofu"

git clone --quiet --filter=blob:none https://github.com/opentofu/opentofu.git "$SRC"
git -C "$SRC" checkout --quiet "$OPENTOFU_COMMIT"

actual="$(git -C "$SRC" rev-parse HEAD)"
if [[ "$actual" != "$OPENTOFU_COMMIT" ]]; then
  echo "ERROR: expected OpenTofu $OPENTOFU_COMMIT, got $actual" >&2
  exit 1
fi

for patch in "${PATCHES[@]}"; do
  git -C "$SRC" apply --check "$patch"
  git -C "$SRC" apply "$patch"
done

mkdir -p "$ROOT/dist"
(
  cd "$SRC"
  GOWORK=off go build -trimpath -o "$ROOT/dist/ghoten" ./cmd/tofu
)

echo "Built dist/ghoten from OpenTofu $GHOTEN_VERSION"
