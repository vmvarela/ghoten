#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
: "${VERSION:?}" "${GOARCH:?}" "${GHOTEN_BIN:?}" "${PACKAGE_OUTPUT:?}"
[[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]
case "$GOARCH" in amd64|arm64) ;; *) exit 1 ;; esac
[[ -x "$GHOTEN_BIN" ]]
mkdir -p "$PACKAGE_OUTPUT"
OUTPUT="$(cd "$PACKAGE_OUTPUT" && pwd)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
cp "$GHOTEN_BIN" "$WORK/ghoten"
cp "$ROOT/LICENSE" "$WORK/LICENSE"
cd "$WORK"
for packager in deb rpm apk; do
  nfpm package -p "$packager" -f "$ROOT/packaging/nfpm.yaml" -t "$OUTPUT/"
done
