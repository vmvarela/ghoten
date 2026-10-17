#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=/dev/null
source "$ROOT/ghoten.env"

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
  GOWORK=off go get github.com/vmvarela/oras-state@v0.0.0-20261017122938-0c4285e6f88b

  GOWORK=off go test     ./internal/tofu     ./internal/states/...     ./internal/plans     ./internal/command/arguments     ./internal/command/views/...     ./internal/backend/local     ./internal/backend/remote-state/oras     ./internal/backend/init

  GOWORK=off go build -trimpath -o "$WORK/ghoten" ./cmd/tofu
)

echo "Verified Ghoten $GHOTEN_VERSION against OpenTofu $OPENTOFU_COMMIT"
