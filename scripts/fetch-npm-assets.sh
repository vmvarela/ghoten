#!/usr/bin/env bash
set -euo pipefail

# Usage: fetch-npm-assets.sh <version> <dest-dir>
# Downloads and checksum-verifies the archives needed to repair npm publication
# for an already published stable release. It never creates or changes a release.

VERSION="${1:-}"
DEST="${2:-}"

usage() {
  echo "usage: fetch-npm-assets.sh <stable version> <dest-dir>" >&2
  exit 2
}

[[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || usage
[[ -n "$DEST" ]] || usage

fail() {
  echo "fetch-npm-assets: $*" >&2
  exit 1
}

TAG="v$VERSION"
ARCHIVES=(
  "ghoten_${VERSION}_linux_amd64.tar.gz"
  "ghoten_${VERSION}_linux_arm64.tar.gz"
  "ghoten_${VERSION}_darwin_amd64.tar.gz"
  "ghoten_${VERSION}_darwin_arm64.tar.gz"
  "ghoten_${VERSION}_windows_amd64.zip"
)
REQUIRED=("${ARCHIVES[@]}" sha256sums.txt)

META="$(gh release view "$TAG" --json tagName,isDraft,isPrerelease,assets)" ||
  fail "cannot read release $TAG"

jq -e --arg tag "$TAG" '.tagName == $tag' <<<"$META" >/dev/null ||
  fail "release tag must be $TAG"
jq -e '.isDraft == false and .isPrerelease == false' <<<"$META" >/dev/null ||
  fail "$TAG must be published and not a prerelease"

for name in "${REQUIRED[@]}"; do
  count="$(jq --arg name "$name" '[.assets[].name | select(. == $name)] | length' <<<"$META")"
  [[ "$count" == 1 ]] || fail "release $TAG must provide exactly one $name asset (found $count)"
done

mkdir -p "$DEST"
for name in "${REQUIRED[@]}"; do
  gh release download "$TAG" --dir "$DEST" --pattern "$name" ||
    fail "cannot download $name from $TAG"
done

# Rebuild a strict manifest from the required archives only: every archive must
# appear exactly once, so missing or duplicated entries fail closed.
MANIFEST="$DEST/sha256sums.txt"
VERIFIED="$DEST/verified-sha256sums.txt"
: >"$VERIFIED"
for archive in "${ARCHIVES[@]}"; do
  lines="$(awk -v file="$archive" '$2 == file || $2 == "*" file' "$MANIFEST")"
  count="$(grep -c . <<<"$lines" || true)"
  [[ "$count" == 1 ]] || fail "sha256sums.txt must list $archive exactly once (found $count)"
  printf '%s\n' "$lines" >>"$VERIFIED"
done

(cd "$DEST" && sha256sum --check --strict verified-sha256sums.txt) ||
  fail "checksum verification failed for $TAG assets"
rm -f "$VERIFIED"

echo "Verified release assets for $TAG"
