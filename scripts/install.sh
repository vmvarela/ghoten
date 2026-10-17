#!/bin/sh
set -eu

REPO="vmvarela/ghoten"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"

fail() {
  printf 'error: %s\n' "$1" >&2
  exit 1
}

command -v curl >/dev/null 2>&1 || fail "curl is required"
command -v tar >/dev/null 2>&1 || fail "tar is required"

case "$(uname -s 2>/dev/null || true)" in
  Linux) os="linux" ;;
  Darwin) os="darwin" ;;
  *) fail "unsupported operating system (supported: Linux, macOS)" ;;
esac

case "$(uname -m 2>/dev/null || true)" in
  x86_64|amd64) arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *) fail "unsupported architecture (supported: amd64, arm64)" ;;
esac

if [ -n "${VERSION:-}" ]; then
  version="${VERSION#v}"
else
  location="$(
    curl -fsSI "https://github.com/${REPO}/releases/latest" |
      awk 'tolower($1) == "location:" { print $2; exit }' |
      tr -d '\r'
  )"
  [ -n "$location" ] || fail "could not resolve the latest stable Ghoten release"
  tag="${location##*/}"
  case "$tag" in
    v*) version="${tag#v}" ;;
    *) fail "unexpected latest release tag: $tag" ;;
  esac
fi

old_ifs="$IFS"
IFS=.
set -- $version
IFS="$old_ifs"
[ "$#" -eq 3 ] || fail "invalid VERSION: $version"
for component in "$@"; do
  case "$component" in
    ""|*[!0-9]*) fail "invalid VERSION: $version" ;;
  esac
done

tag="v${version}"
asset="ghoten_${version}_${os}_${arch}.tar.gz"
base_url="https://github.com/${REPO}/releases/download/${tag}"

tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/ghoten.XXXXXX")" || fail "could not create temporary directory"
cleanup() {
  rm -rf "$tmp_dir"
}
trap cleanup EXIT HUP INT TERM

printf '==> Downloading Ghoten %s (%s/%s)\n' "$version" "$os" "$arch"
curl -fsSL "${base_url}/${asset}" -o "${tmp_dir}/${asset}" ||
  fail "release asset not found: ${tag}/${asset}"
curl -fsSL "${base_url}/sha256sums.txt" -o "${tmp_dir}/sha256sums.txt" ||
  fail "sha256sums.txt not found for ${tag}"

expected="$(
  awk -v asset="$asset" '$2 == asset { print $1; exit }' "${tmp_dir}/sha256sums.txt"
)"
[ -n "$expected" ] || fail "checksum for $asset is missing from sha256sums.txt"

if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "${tmp_dir}/${asset}" | awk '{ print $1 }')"
elif command -v shasum >/dev/null 2>&1; then
  actual="$(shasum -a 256 "${tmp_dir}/${asset}" | awk '{ print $1 }')"
elif command -v openssl >/dev/null 2>&1; then
  actual="$(openssl dgst -sha256 "${tmp_dir}/${asset}" | awk '{ print $NF }')"
else
  fail "no SHA-256 tool found (need sha256sum, shasum, or openssl)"
fi

[ "$actual" = "$expected" ] || fail "checksum mismatch for $asset"

mkdir "${tmp_dir}/extract"
tar -xzf "${tmp_dir}/${asset}" -C "${tmp_dir}/extract" ghoten ||
  fail "release archive does not contain the expected ghoten executable"
[ -f "${tmp_dir}/extract/ghoten" ] || fail "ghoten executable missing after extraction"

mkdir -p "$INSTALL_DIR" || fail "cannot create install directory: $INSTALL_DIR"
destination="${INSTALL_DIR%/}/ghoten"

if command -v install >/dev/null 2>&1 && install -m 0755 "${tmp_dir}/extract/ghoten" "$destination" 2>/dev/null; then
  :
else
  cp "${tmp_dir}/extract/ghoten" "$destination" 2>/dev/null ||
    fail "cannot write to $destination (try running with suitable permissions)"
  chmod 0755 "$destination" || fail "cannot make $destination executable"
fi

printf '==> Installed Ghoten %s to %s\n' "$version" "$destination"
