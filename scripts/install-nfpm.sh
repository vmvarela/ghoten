#!/usr/bin/env bash
set -euo pipefail
# Keep the release and verification toolchain identical.
VERSION=2.48.0
SHA256=6d7f9a28b2f397934403950fcbba83f598b08d02edbe79a0815b3b4897341750
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
curl --fail --silent --show-error --location \
  "https://github.com/goreleaser/nfpm/releases/download/v${VERSION}/nfpm_${VERSION}_Linux_x86_64.tar.gz" \
  -o "$WORK/nfpm.tar.gz"
printf '%s  %s\n' "$SHA256" "$WORK/nfpm.tar.gz" | sha256sum --check -
sudo tar -xzf "$WORK/nfpm.tar.gz" -C /usr/local/bin nfpm
