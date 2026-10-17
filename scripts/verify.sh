#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=/dev/null
source "$ROOT/ghoten.env"

sh -n "$ROOT/scripts/install.sh"

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
SRC="$WORK/opentofu"

git clone --quiet --filter=blob:none https://github.com/opentofu/opentofu.git "$SRC"
git -C "$SRC" checkout --quiet "$OPENTOFU_COMMIT"

for patch in "$ROOT/patches/smart-refresh.patch" "$ROOT/patches/oras.patch"; do
  git -C "$SRC" apply --check "$patch"
  git -C "$SRC" apply "$patch"
done

(
  cd "$SRC"
  GOWORK=off go test \
    ./internal/tofu \
    ./internal/states/... \
    ./internal/plans \
    ./internal/command/arguments \
    ./internal/command/views/... \
    ./internal/backend/local \
    ./internal/backend/remote-state/oras \
    ./internal/backend/init

  GOWORK=off go build -trimpath -o "$WORK/ghoten" ./cmd/tofu
)

if [[ -n "${GHOTEN_VERIFY_OUTPUT:-}" ]]; then
  mkdir -p "$(dirname "$GHOTEN_VERIFY_OUTPUT")"
  cp "$WORK/ghoten" "$GHOTEN_VERIFY_OUTPUT"
  chmod +x "$GHOTEN_VERIFY_OUTPUT"
fi

echo "Verified Ghoten $GHOTEN_VERSION against OpenTofu $OPENTOFU_COMMIT"
