#!/bin/bash
# Setup Ghoten for GitHub Actions
# This script downloads and installs Ghoten to the GitHub Actions tool cache

set -euo pipefail

# Configuration
GITHUB_REPO="vmvarela/ghoten"
BINARY_NAME="ghoten"

# Colors (only if running in terminal)
if [ -t 1 ]; then
  RED='\033[0;31m'
  GREEN='\033[0;32m'
  YELLOW='\033[1;33m'
  NC='\033[0m'
else
  RED=''
  GREEN=''
  YELLOW=''
  NC=''
fi

info() {
  echo -e "${GREEN}[INFO]${NC} $1"
}

warn() {
  echo -e "${YELLOW}[WARN]${NC} $1"
}

error() {
  echo -e "${RED}[ERROR]${NC} $1"
  exit 1
}

# Map GitHub Actions runner OS to ghoten OS name
map_os() {
  local runner_os="${RUNNER_OS:-}"
  case "${runner_os,,}" in
    linux)   echo "linux" ;;
    macos)   echo "darwin" ;;
    windows) echo "windows" ;;
    *)       error "Unsupported operating system: ${runner_os}" ;;
  esac
}

# Map GitHub Actions runner architecture to ghoten arch name
map_arch() {
  local runner_arch="${RUNNER_ARCH:-}"
  case "${runner_arch,,}" in
    x64)   echo "amd64" ;;
    x86)   echo "386" ;;
    arm64) echo "arm64" ;;
    arm)   echo "arm" ;;
    *)     error "Unsupported architecture: ${runner_arch}" ;;
  esac
}

# Read version from version file
read_version_file() {
  local file="$1"
  
  if [ ! -f "$file" ]; then
    error "Version file not found: $file"
  fi
  
  local filename
  filename=$(basename "$file")
  
  case "$filename" in
    .ghoten-version)
      # Plain version string
      cat "$file" | tr -d '[:space:]'
      ;;
    .tool-versions)
      # asdf format: tool version
      grep -E '^ghoten[[:space:]]' "$file" | awk '{print $2}' | tr -d '[:space:]'
      ;;
    *)
      # Assume plain version string
      cat "$file" | tr -d '[:space:]'
      ;;
  esac
}

# Get latest version from GitHub API
get_latest_version() {
  local api_url="https://api.github.com/repos/${GITHUB_REPO}/releases/latest"
  local response
  
  if [ -n "${GITHUB_TOKEN:-}" ]; then
    response=$(curl -sSL -H "Authorization: Bearer ${GITHUB_TOKEN}" "$api_url")
  else
    response=$(curl -sSL "$api_url")
  fi
  
  echo "$response" | grep '"tag_name"' | sed -E 's/.*"tag_name"[[:space:]]*:[[:space:]]*"([^"]+)".*/\1/'
}

# Verify checksum
verify_checksum() {
  local file="$1"
  local checksums_file="$2"
  local filename
  filename=$(basename "$file")
  
  info "Verifying checksum..."
  
  local expected
  expected=$(grep -E "^[a-f0-9]+[[:space:]]+${filename}$" "$checksums_file" | awk '{print $1}')
  
  if [ -z "$expected" ]; then
    warn "Could not find checksum for ${filename} in SHA256SUMS, skipping verification"
    return 0
  fi
  
  local actual
  if command -v sha256sum >/dev/null 2>&1; then
    actual=$(sha256sum "$file" | awk '{print $1}')
  elif command -v shasum >/dev/null 2>&1; then
    actual=$(shasum -a 256 "$file" | awk '{print $1}')
  else
    warn "Neither sha256sum nor shasum available, skipping verification"
    return 0
  fi
  
  if [ "$expected" != "$actual" ]; then
    error "Checksum verification failed!\nExpected: ${expected}\nActual:   ${actual}"
  fi
  
  info "Checksum verified successfully"
}

# Main function
main() {
  info "Setting up Ghoten..."
  
  # Determine version to install
  local version=""
  
  # Check version file first (takes precedence)
  if [ -n "${INPUT_GHOTEN_VERSION_FILE:-}" ]; then
    info "Reading version from file: ${INPUT_GHOTEN_VERSION_FILE}"
    version=$(read_version_file "${INPUT_GHOTEN_VERSION_FILE}")
  fi
  
  # Fall back to version input
  if [ -z "$version" ]; then
    version="${INPUT_GHOTEN_VERSION:-latest}"
  fi
  
  # Resolve 'latest' to actual version
  if [ "$version" = "latest" ]; then
    info "Fetching latest version..."
    version=$(get_latest_version)
    if [ -z "$version" ]; then
      error "Could not determine latest version"
    fi
  fi
  
  # Normalize version (add 'v' prefix if missing for download URL)
  local version_tag="$version"
  if [[ ! "$version_tag" =~ ^v ]]; then
    version_tag="v${version}"
  fi
  
  # Version without 'v' prefix for output
  local version_clean="${version_tag#v}"
  
  info "Version: ${version_tag}"
  
  # Detect platform
  local os arch
  os=$(map_os)
  arch=$(map_arch)
  info "Platform: ${os}/${arch}"
  
  # Build artifact name
  local binary_suffix=""
  if [ "$os" = "windows" ]; then
    binary_suffix=".exe"
  fi
  
  local artifact_name="ghoten_${os}_${arch}${binary_suffix}"
  
  # Build download URLs
  local base_url="https://github.com/${GITHUB_REPO}/releases/download/${version_tag}"
  local binary_url="${base_url}/${artifact_name}"
  local checksums_url="${base_url}/SHA256SUMS"
  
  info "Downloading ${artifact_name}..."
  
  # Create tool cache directory
  local tool_dir="${RUNNER_TOOL_CACHE:-/tmp}/ghoten/${version_clean}/${arch}"
  mkdir -p "$tool_dir"
  
  # Download binary
  local binary_path="${tool_dir}/${BINARY_NAME}${binary_suffix}"
  
  local curl_opts=(-fsSL)
  if [ -n "${GITHUB_TOKEN:-}" ]; then
    curl_opts+=(-H "Authorization: Bearer ${GITHUB_TOKEN}")
  fi
  
  if ! curl "${curl_opts[@]}" "$binary_url" -o "$binary_path"; then
    error "Failed to download binary from ${binary_url}"
  fi
  
  # Download checksums
  local checksums_path="${tool_dir}/SHA256SUMS"
  if curl "${curl_opts[@]}" "$checksums_url" -o "$checksums_path" 2>/dev/null; then
    # Verify checksum (need to rename file temporarily for verification)
    local temp_verify="${tool_dir}/${artifact_name}"
    cp "$binary_path" "$temp_verify"
    verify_checksum "$temp_verify" "$checksums_path"
    rm -f "$temp_verify"
  else
    warn "Could not download SHA256SUMS, skipping checksum verification"
  fi
  
  # Make executable
  chmod +x "$binary_path"
  
  # Verify installation
  info "Verifying installation..."
  if ! "${binary_path}" version >/dev/null 2>&1; then
    error "Installation verification failed - binary does not execute"
  fi
  
  local installed_version
  installed_version=$("${binary_path}" version | head -1)
  info "Installed: ${installed_version}"
  
  # Add to PATH
  info "Adding ${tool_dir} to PATH"
  echo "${tool_dir}" >> "${GITHUB_PATH}"
  
  # Set output
  echo "version=${version_clean}" >> "${GITHUB_OUTPUT}"
  
  info "Ghoten ${version_tag} has been installed successfully!"
}

main "$@"
