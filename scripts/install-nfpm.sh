#!/usr/bin/env bash
set -euo pipefail
# Keep the release and verification toolchain identical.
VERSION=2.45.1
SHA256=317b2747561b6329d6c5e1fe002a1a33f90b2c3c3da2b7b4fe4c3154e98c5ff8
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
curl --fail --silent --show-error --location \
  "https://github.com/goreleaser/nfpm/releases/download/v${VERSION}/nfpm_${VERSION}_Linux_x86_64.tar.gz" \
  -o "$WORK/nfpm.tar.gz"
printf '%s  %s\n' "$SHA256" "$WORK/nfpm.tar.gz" | sha256sum --check -
sudo tar -xzf "$WORK/nfpm.tar.gz" -C /usr/local/bin nfpm
