#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=/dev/null
source "$ROOT/ghoten.env"

PATCH="$ROOT/patches/ghoten.patch"
if [[ ! -s "$PATCH" ]]; then
  echo "ERROR: $PATCH is missing. Run ./scripts/generate-patch.sh first." >&2
  exit 1
fi

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

git -C "$SRC" apply --check "$PATCH"
git -C "$SRC" apply "$PATCH"

mkdir -p "$ROOT/dist"
(
  cd "$SRC"
  go build -trimpath -o "$ROOT/dist/ghoten" ./cmd/tofu
)

echo "Built dist/ghoten from OpenTofu $GHOTEN_VERSION"
