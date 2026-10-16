#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=/dev/null
source "$ROOT/ghoten.env"

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

SOURCE="$WORK/source"
BASE="$WORK/base"
SERIES_OUT="$WORK/series"
mkdir -p "$SERIES_OUT" "$ROOT/patches"

git clone --quiet --filter=blob:none https://github.com/vmvarela/ghoten.git "$SOURCE"
git clone --quiet --filter=blob:none https://github.com/opentofu/opentofu.git "$BASE"
git -C "$BASE" checkout --quiet "$OPENTOFU_COMMIT"

mapfile -t PATHS < <(grep -Ev '^[[:space:]]*(#|$)' "$ROOT/patches/smart-refresh-paths.txt")

n=0
while read -r from to; do
  [[ -z "${from:-}" || "$from" == #* ]] && continue
  n=$((n + 1))
  raw="$SERIES_OUT/$n.raw.patch"
  normalized="$SERIES_OUT/$n.patch"

  git -C "$SOURCE" diff --binary "$from" "$to" -- "${PATHS[@]}" > "$raw"

  # Normalize the historical fork's core package/module naming to OpenTofu.
  sed     -e 's#a/internal/ghoten/#a/internal/tofu/#g'     -e 's#b/internal/ghoten/#b/internal/tofu/#g'     -e 's#github.com/vmvarela/ghoten/#github.com/opentofu/opentofu/#g'     -e 's/\bghoten\./tofu./g'     -e 's/^ package ghoten$/ package tofu/'     -e 's/^-package ghoten$/-package tofu/'     -e 's/^+package ghoten$/+package tofu/'     "$raw" > "$normalized"

  if [[ ! -s "$normalized" ]]; then
    continue
  fi

  echo "Applying Smart Refresh delta $n: $from -> $to"
  if ! git -C "$BASE" apply --check "$normalized"; then
    echo "ERROR: Smart Refresh delta $n does not apply cleanly." >&2
    git -C "$BASE" apply --reject --whitespace=nowarn "$normalized" || true
    find "$BASE" -name '*.rej' -print -exec cat {} \;
    exit 1
  fi
  git -C "$BASE" apply "$normalized"
done < "$ROOT/patches/smart-refresh-series.txt"

(
  cd "$BASE"
  GOWORK=off go mod tidy
  GOWORK=off go test ./internal/tofu ./internal/states/... ./internal/plans ./internal/command/arguments
  GOWORK=off go build -trimpath -o "$WORK/ghoten-smart" ./cmd/tofu
)

git -C "$BASE" diff --binary "$OPENTOFU_COMMIT" --   internal backend-not-used 2>/dev/null > /dev/null || true

# The final patch is always generated from the clean OpenTofu base after all
# feature deltas have applied, so intermediate legacy context never survives.
git -C "$BASE" diff --binary "$OPENTOFU_COMMIT" --   internal go.mod go.sum > "$ROOT/patches/smart-refresh.patch"

echo "Smart Refresh feature series ported successfully."
echo "Patch: patches/smart-refresh.patch"
