#!/usr/bin/env bash
# Ghoten Action — Auth: authenticate to GHCR via Docker config
set -euo pipefail

TOKEN="${INPUT_GITHUB_TOKEN:-}"
ACTOR="${GITHUB_ACTOR:-}"

if [[ -z "$TOKEN" ]]; then
  echo "::error title=Ghoten Auth::github-token is required for GHCR authentication"
  exit 1
fi

if [[ -z "$ACTOR" ]]; then
  echo "::error title=Ghoten Auth::GITHUB_ACTOR is not set — cannot authenticate to GHCR"
  exit 1
fi

DOCKER_CONFIG="${HOME}/.docker"
mkdir -p "$DOCKER_CONFIG"

# base64 encoding — handle GNU (with -w0) and macOS/BusyBox (without)
AUTH=$(printf '%s:%s' "$ACTOR" "$TOKEN" | base64 -w0 2>/dev/null || printf '%s:%s' "$ACTOR" "$TOKEN" | base64)

# Merge with existing config if present, otherwise create fresh
if [[ -f "$DOCKER_CONFIG/config.json" ]]; then
  if jq -e . "$DOCKER_CONFIG/config.json" >/dev/null 2>&1; then
    tmp_cfg="$(mktemp)"
    jq --arg auth "$AUTH" '
      .auths |= (. // {}) |
      .auths["ghcr.io"] |= (. // {}) |
      .auths["ghcr.io"].auth = $auth
    ' "$DOCKER_CONFIG/config.json" > "$tmp_cfg"
    mv "$tmp_cfg" "$DOCKER_CONFIG/config.json"
  else
    # Existing config is not valid JSON — start fresh
    printf '{"auths":{"ghcr.io":{"auth":"%s"}}}' "$AUTH" > "$DOCKER_CONFIG/config.json"
  fi
else
  printf '{"auths":{"ghcr.io":{"auth":"%s"}}}' "$AUTH" > "$DOCKER_CONFIG/config.json"
fi

echo "DOCKER_CONFIG=${DOCKER_CONFIG}" >> "$GITHUB_ENV"

echo "🔐 Authenticated to ghcr.io as ${ACTOR}"
