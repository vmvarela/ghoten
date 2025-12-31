#!/bin/sh
# Ghoten Installer
# Usage: curl -sSL https://raw.githubusercontent.com/vmvarela/ghoten/develop/install.sh | sh
#
# Environment variables:
#   GHOTEN_VERSION     - Specific version to install (e.g., v1.12.0). Default: latest
#   GHOTEN_INSTALL_DIR - Installation directory. Default: /usr/local/bin
#   GHOTEN_BINARY_NAME - Binary name. Default: ghoten

set -e

GITHUB_REPO="vmvarela/ghoten"
BINARY_NAME="${GHOTEN_BINARY_NAME:-ghoten}"
INSTALL_DIR="${GHOTEN_INSTALL_DIR:-/usr/local/bin}"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

info() {
    printf "${GREEN}[INFO]${NC} %s\n" "$1"
}

warn() {
    printf "${YELLOW}[WARN]${NC} %s\n" "$1"
}

error() {
    printf "${RED}[ERROR]${NC} %s\n" "$1"
    exit 1
}

# Detect OS
detect_os() {
    OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
    case "$OS" in
        linux*)  OS="linux" ;;
        darwin*) OS="darwin" ;;
        freebsd*) OS="freebsd" ;;
        openbsd*) OS="openbsd" ;;
        mingw*|msys*|cygwin*) OS="windows" ;;
        *) error "Unsupported operating system: $OS" ;;
    esac
    echo "$OS"
}

# Detect architecture
detect_arch() {
    ARCH="$(uname -m)"
    case "$ARCH" in
        x86_64|amd64) ARCH="amd64" ;;
        aarch64|arm64) ARCH="arm64" ;;
        armv7l|armv6l) ARCH="arm" ;;
        i386|i686) ARCH="386" ;;
        *) error "Unsupported architecture: $ARCH" ;;
    esac
    echo "$ARCH"
}

# Get latest version from GitHub API
get_latest_version() {
    if command -v curl >/dev/null 2>&1; then
        curl -sSL "https://api.github.com/repos/${GITHUB_REPO}/releases/latest" | grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/'
    elif command -v wget >/dev/null 2>&1; then
        wget -qO- "https://api.github.com/repos/${GITHUB_REPO}/releases/latest" | grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/'
    else
        error "Neither curl nor wget found. Please install one of them."
    fi
}

# Download file
download() {
    URL="$1"
    OUTPUT="$2"
    
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL "$URL" -o "$OUTPUT"
    elif command -v wget >/dev/null 2>&1; then
        wget -q "$URL" -O "$OUTPUT"
    else
        error "Neither curl nor wget found. Please install one of them."
    fi
}

# Main installation
main() {
    info "Ghoten Installer"
    echo ""
    
    # Detect platform
    OS=$(detect_os)
    ARCH=$(detect_arch)
    info "Detected platform: ${OS}/${ARCH}"
    
    # Get version
    VERSION="${GHOTEN_VERSION:-}"
    if [ -z "$VERSION" ]; then
        info "Fetching latest version..."
        VERSION=$(get_latest_version)
        if [ -z "$VERSION" ]; then
            error "Could not determine latest version. Please set GHOTEN_VERSION environment variable."
        fi
    fi
    info "Version: ${VERSION}"
    
    # Build download URL
    BINARY_SUFFIX=""
    if [ "$OS" = "windows" ]; then
        BINARY_SUFFIX=".exe"
    fi
    
    ARTIFACT_NAME="ghoten_${OS}_${ARCH}${BINARY_SUFFIX}"
    DOWNLOAD_URL="https://github.com/${GITHUB_REPO}/releases/download/${VERSION}/${ARTIFACT_NAME}"
    
    info "Downloading from: ${DOWNLOAD_URL}"
    
    # Create temp directory
    TMP_DIR=$(mktemp -d)
    trap "rm -rf $TMP_DIR" EXIT
    
    TMP_FILE="${TMP_DIR}/${ARTIFACT_NAME}"
    
    # Download binary
    if ! download "$DOWNLOAD_URL" "$TMP_FILE"; then
        error "Download failed. Check if version ${VERSION} exists and has binaries for ${OS}/${ARCH}."
    fi
    
    if [ ! -f "$TMP_FILE" ]; then
        error "Download failed - file not found"
    fi
    
    # Make executable
    chmod +x "$TMP_FILE"
    
    # Install
    INSTALL_PATH="${INSTALL_DIR}/${BINARY_NAME}${BINARY_SUFFIX}"
    
    info "Installing to: ${INSTALL_PATH}"
    
    # Check if we need sudo
    if [ -w "$INSTALL_DIR" ]; then
        mv "$TMP_FILE" "$INSTALL_PATH"
    else
        warn "Need elevated permissions to install to ${INSTALL_DIR}"
        if command -v sudo >/dev/null 2>&1; then
            sudo mv "$TMP_FILE" "$INSTALL_PATH"
            sudo chmod +x "$INSTALL_PATH"
        else
            error "Cannot write to ${INSTALL_DIR} and sudo is not available. Try setting TOFU_ORAS_INSTALL_DIR to a writable directory."
        fi
    fi
    
    # Verify installation
    if [ -x "$INSTALL_PATH" ]; then
        echo ""
        info "Installation complete!"
        echo ""
        info "Binary installed: ${INSTALL_PATH}"
        info "Version: $(${INSTALL_PATH} version 2>/dev/null || echo "${VERSION}")"
        echo ""
        info "Usage:"
        echo "  ${BINARY_NAME} init"
        echo "  ${BINARY_NAME} plan"
        echo "  ${BINARY_NAME} apply"
        echo ""
        info "Documentation: https://github.com/${GITHUB_REPO}/blob/develop/internal/backend/remote-state/oras/README.md"
    else
        error "Installation verification failed"
    fi
}

main "$@"
