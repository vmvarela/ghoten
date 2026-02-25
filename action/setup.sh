#!/usr/bin/env bash
# Ghoten Action — Setup: install the ghoten binary
set -euo pipefail

# ─── Determine version ────────────────────────────────────────────────────────
VERSION="${INPUT_VERSION:-}"
if [[ -z "$VERSION" ]]; then
  VERSION_FILE="${GITHUB_ACTION_PATH}/version/VERSION"
  if [[ -f "$VERSION_FILE" ]]; then
    VERSION=$(tr -d '[:space:]' < "$VERSION_FILE")
  else
    echo "::error title=Ghoten Setup::Could not determine version. Set the 'version' input."
    exit 1
  fi
fi
VERSION="${VERSION#v}"

# ─── Check if already installed ───────────────────────────────────────────────
if command -v ghoten &>/dev/null; then
  INSTALLED=$(ghoten version 2>/dev/null | head -1 | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' || true)
  if [[ "$INSTALLED" == "$VERSION" ]]; then
    echo "✅ ghoten v${VERSION} is already installed"
    ghoten version
    exit 0
  fi
  echo "ℹ️  Found ghoten v${INSTALLED}, upgrading to v${VERSION}"
fi

# ─── Detect platform ─────────────────────────────────────────────────────────
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case "$ARCH" in
  x86_64)        ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  armv7l)        ARCH="arm"   ;;
  i686|i386)     ARCH="386"   ;;
  *)             echo "::error title=Ghoten Setup::Unsupported architecture: ${ARCH}"; exit 1 ;;
esac

case "$OS" in
  linux|darwin|freebsd|netbsd|openbsd) ;;
  mingw*|msys*|cygwin*)                OS="windows" ;;
  *)                                   echo "::error title=Ghoten Setup::Unsupported OS: ${OS}"; exit 1 ;;
esac

# ─── Download ─────────────────────────────────────────────────────────────────
ARCHIVE="ghoten_${VERSION}_${OS}_${ARCH}.tar.gz"
URL="https://github.com/vmvarela/ghoten/releases/download/v${VERSION}/${ARCHIVE}"
TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

echo "::group::📦 Installing ghoten v${VERSION} (${OS}/${ARCH})"
echo "Downloading ${URL}"

HTTP_CODE=""
if command -v curl &>/dev/null; then
  HTTP_CODE=$(curl -fsSL -w '%{http_code}' -o "${TMPDIR}/${ARCHIVE}" "$URL" 2>/dev/null) || true
elif command -v wget &>/dev/null; then
  if wget -q -O "${TMPDIR}/${ARCHIVE}" "$URL" 2>/dev/null; then
    HTTP_CODE="200"
  else
    HTTP_CODE="download failed (wget)"
  fi
else
  echo "::error title=Ghoten Setup::Neither curl nor wget found"
  exit 1
fi

if [[ ! -f "${TMPDIR}/${ARCHIVE}" || ! -s "${TMPDIR}/${ARCHIVE}" ]]; then
  echo "::error title=Ghoten Setup::Download failed (${HTTP_CODE}). Check that v${VERSION} exists at https://github.com/vmvarela/ghoten/releases"
  exit 1
fi

# ─── Extract and install ─────────────────────────────────────────────────────
tar -xzf "${TMPDIR}/${ARCHIVE}" -C "$TMPDIR"

INSTALL_DIR="${RUNNER_TOOL_CACHE:-/usr/local/bin}/ghoten/${VERSION}"
mkdir -p "$INSTALL_DIR"

BINARY="ghoten"
[[ "$OS" == "windows" ]] && BINARY="ghoten.exe"

mv "${TMPDIR}/${BINARY}" "${INSTALL_DIR}/${BINARY}"
chmod +x "${INSTALL_DIR}/${BINARY}"

echo "${INSTALL_DIR}" >> "$GITHUB_PATH"
export PATH="${INSTALL_DIR}:${PATH}"

echo ""
echo "✅ ghoten v${VERSION} installed"
"${INSTALL_DIR}/${BINARY}" version
echo "::endgroup::"
