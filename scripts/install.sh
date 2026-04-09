#!/bin/sh
# Ghoten universal installer
#
# Usage:
#   curl -sSfL https://raw.githubusercontent.com/vmvarela/ghoten/master/scripts/install.sh | sh
#
# Environment variables:
#   VERSION      — version to install (default: latest release)
#   INSTALL_DIR  — installation directory (default: /usr/local/bin)
#
# Supports: Linux (x86_64, arm64, armv7, i686), macOS (Intel, Apple Silicon),
#           FreeBSD, NetBSD, OpenBSD, Solaris.

set -e

REPO="vmvarela/ghoten"
DEFAULT_INSTALL_DIR="/usr/local/bin"

# ─── Helpers ──────────────────────────────────────────────────────────────────

log()   { printf '%s\n' "$*"; }
info()  { printf '  %s\n' "$*"; }
err()   { printf 'Error: %s\n' "$*" >&2; exit 1; }

need_cmd() {
  if ! command -v "$1" > /dev/null 2>&1; then
    err "required command not found: $1"
  fi
}

# ─── HTTP helpers (curl or wget) ─────────────────────────────────────────────

http_get() {
  _url="$1"
  _dest="$2"
  if command -v curl > /dev/null 2>&1; then
    curl -fsSL -o "$_dest" "$_url"
  elif command -v wget > /dev/null 2>&1; then
    wget -qO "$_dest" "$_url"
  else
    err "either curl or wget is required"
  fi
}

http_get_stdout() {
  _url="$1"
  if command -v curl > /dev/null 2>&1; then
    curl -fsSL "$_url"
  elif command -v wget > /dev/null 2>&1; then
    wget -qO- "$_url"
  else
    err "either curl or wget is required"
  fi
}

# ─── SHA-256 verification ────────────────────────────────────────────────────

verify_sha256() {
  _file="$1"
  _expected="$2"
  _actual=""
  if command -v sha256sum > /dev/null 2>&1; then
    _actual=$(sha256sum "$_file" | awk '{print $1}')
  elif command -v shasum > /dev/null 2>&1; then
    _actual=$(shasum -a 256 "$_file" | awk '{print $1}')
  else
    err "either sha256sum or shasum is required for checksum verification"
  fi
  if [ "$_actual" != "$_expected" ]; then
    err "checksum mismatch for $(basename "$_file")\n  expected: $_expected\n  got:      $_actual"
  fi
}

# ─── Detect platform ─────────────────────────────────────────────────────────

detect_os() {
  _os=$(uname -s | tr '[:upper:]' '[:lower:]')
  case "$_os" in
    linux|darwin|freebsd|netbsd|openbsd|solaris) echo "$_os" ;;
    mingw*|msys*|cygwin*)                        echo "windows" ;;
    *) err "unsupported operating system: $_os" ;;
  esac
}

detect_arch() {
  _arch=$(uname -m)
  case "$_arch" in
    x86_64|amd64)  echo "amd64" ;;
    aarch64|arm64) echo "arm64" ;;
    armv7l|armv7)  echo "arm"   ;;
    i686|i386)     echo "386"   ;;
    *) err "unsupported architecture: $_arch" ;;
  esac
}

# ─── Resolve latest version ──────────────────────────────────────────────────

resolve_version() {
  if [ -n "${VERSION:-}" ]; then
    echo "${VERSION#v}"
    return
  fi
  _tag=$(http_get_stdout "https://api.github.com/repos/${REPO}/releases/latest" \
    | grep '"tag_name"' | head -1 | sed 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/')
  if [ -z "$_tag" ]; then
    err "could not determine latest version. Set VERSION explicitly."
  fi
  echo "${_tag#v}"
}

# ─── Main ─────────────────────────────────────────────────────────────────────

main() {
  need_cmd uname
  need_cmd tar
  need_cmd awk
  need_cmd mktemp

  OS=$(detect_os)
  ARCH=$(detect_arch)

  log "Detecting platform... ${OS}/${ARCH}"

  VERSION_RESOLVED=$(resolve_version)
  log "Installing ghoten v${VERSION_RESOLVED}"

  INSTALL_DIR="${INSTALL_DIR:-$DEFAULT_INSTALL_DIR}"

  ARCHIVE="ghoten_${VERSION_RESOLVED}_${OS}_${ARCH}.tar.gz"
  BASE_URL="https://github.com/${REPO}/releases/download/v${VERSION_RESOLVED}"
  ARCHIVE_URL="${BASE_URL}/${ARCHIVE}"
  SHASUMS_URL="${BASE_URL}/ghoten_${VERSION_RESOLVED}_SHA256SUMS"

  TMPDIR=$(mktemp -d)
  trap 'rm -rf "$TMPDIR"' EXIT

  info "Downloading ${ARCHIVE_URL}"
  http_get "$ARCHIVE_URL" "${TMPDIR}/${ARCHIVE}" \
    || err "download failed. Check that v${VERSION_RESOLVED} exists at https://github.com/${REPO}/releases"

  info "Verifying SHA-256 checksum"
  http_get "$SHASUMS_URL" "${TMPDIR}/SHA256SUMS" \
    || err "could not download checksum file"
  EXPECTED_SHA=$(grep "${ARCHIVE}" "${TMPDIR}/SHA256SUMS" | awk '{print $1}')
  if [ -z "$EXPECTED_SHA" ]; then
    err "archive ${ARCHIVE} not found in checksum file"
  fi
  verify_sha256 "${TMPDIR}/${ARCHIVE}" "$EXPECTED_SHA"

  info "Extracting to ${INSTALL_DIR}"
  tar -xzf "${TMPDIR}/${ARCHIVE}" -C "$TMPDIR"

  BINARY="ghoten"
  if [ "$OS" = "windows" ]; then
    BINARY="ghoten.exe"
  fi

  if [ -w "$INSTALL_DIR" ]; then
    mv "${TMPDIR}/${BINARY}" "${INSTALL_DIR}/${BINARY}"
  else
    info "Elevated permissions required for ${INSTALL_DIR}"
    sudo mv "${TMPDIR}/${BINARY}" "${INSTALL_DIR}/${BINARY}"
  fi
  chmod +x "${INSTALL_DIR}/${BINARY}"

  log ""
  log "ghoten v${VERSION_RESOLVED} installed to ${INSTALL_DIR}/${BINARY}"
  "${INSTALL_DIR}/${BINARY}" version
}

main
