#!/bin/sh
set -eu

REPOSITORY="${GHOTEN_REPOSITORY:-vmvarela/ghoten}"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
API_URL="https://api.github.com/repos/${REPOSITORY}"
RELEASE_URL="https://github.com/${REPOSITORY}/releases/download"

fail() {
  printf 'ghoten installer: %s\n' "$*" >&2
  exit 1
}

need() {
  command -v "$1" >/dev/null 2>&1 || fail "required command not found: $1"
}

download() {
  url=$1
  output=$2

  if command -v curl >/dev/null 2>&1; then
    curl --fail --location --silent --show-error "$url" --output "$output"
  elif command -v wget >/dev/null 2>&1; then
    wget -q "$url" -O "$output"
  else
    fail "curl or wget is required"
  fi
}

latest_version() {
  tmp=$1
  download "$API_URL/releases/latest" "$tmp"

  version=$(sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"v\([^"]*\)".*/\1/p' "$tmp" | head -n 1)
  [ -n "$version" ] || fail "could not determine the latest stable release"
  printf '%s\n' "$version"
}

checksum() {
  file=$1

  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$file" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$file" | awk '{print $1}'
  else
    fail "sha256sum or shasum is required"
  fi
}

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) fail "unsupported operating system: $(uname -s)" ;;
esac

case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) fail "unsupported architecture: $(uname -m)" ;;
esac

need tar
need awk
need sed

tmpdir=$(mktemp -d 2>/dev/null || mktemp -d -t ghoten)
trap 'rm -rf "$tmpdir"' EXIT HUP INT TERM

version=${VERSION:-}
if [ -z "$version" ]; then
  version=$(latest_version "$tmpdir/latest.json")
fi
version=${version#v}

case "$version" in
  ''|*[!0-9A-Za-z.-]*) fail "invalid VERSION: $version" ;;
esac

tag="v$version"
archive="ghoten_${version}_${os}_${arch}.tar.gz"
archive_path="$tmpdir/$archive"
checksums_path="$tmpdir/sha256sums.txt"

printf 'Installing Ghoten %s for %s/%s\n' "$version" "$os" "$arch"

download "$RELEASE_URL/$tag/$archive" "$archive_path"
download "$RELEASE_URL/$tag/sha256sums.txt" "$checksums_path"

expected=$(awk -v file="$archive" '$2 == file || $2 == "*" file { print $1; exit }' "$checksums_path")
[ -n "$expected" ] || fail "checksum for $archive not found in sha256sums.txt"
actual=$(checksum "$archive_path")
[ "$actual" = "$expected" ] || fail "checksum mismatch for $archive"

tar -xzf "$archive_path" -C "$tmpdir"
[ -f "$tmpdir/ghoten" ] || fail "release archive does not contain ghoten"

mkdir -p "$INSTALL_DIR" 2>/dev/null || fail "cannot create $INSTALL_DIR"
if command -v install >/dev/null 2>&1; then
  install -m 0755 "$tmpdir/ghoten" "$INSTALL_DIR/ghoten" ||
    fail "cannot install to $INSTALL_DIR/ghoten (set INSTALL_DIR to a writable directory)"
else
  cp "$tmpdir/ghoten" "$INSTALL_DIR/ghoten" ||
    fail "cannot install to $INSTALL_DIR/ghoten (set INSTALL_DIR to a writable directory)"
  chmod 0755 "$INSTALL_DIR/ghoten"
fi

printf 'Installed %s\n' "$INSTALL_DIR/ghoten"
