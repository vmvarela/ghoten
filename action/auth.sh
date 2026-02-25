#!/usr/bin/env bash
# Ghoten Action — Auth: authenticate to GHCR via Docker config
set -euo pipefail

TOKEN="${INPUT_GITHUB_TOKEN:-}"
ACTOR="${GITHUB_ACTOR:-}"

if [[ -z "$TOKEN" ]]; then
  echo "::error title=Ghoten Auth::github-token is required for GHCR authentication"
  exit 1
fi

DOCKER_CONFIG="${HOME}/.docker"
mkdir -p "$DOCKER_CONFIG"

# base64 encoding — handle GNU (with -w0) and macOS/BusyBox (without)
AUTH=$(printf '%s:%s' "$ACTOR" "$TOKEN" | base64 -w0 2>/dev/null || printf '%s:%s' "$ACTOR" "$TOKEN" | base64)

# Merge with existing config if present, otherwise create fresh
if [[ -f "$DOCKER_CONFIG/config.json" ]]; then
  EXISTING=$(cat "$DOCKER_CONFIG/config.json")
  echo "$EXISTING" | python3 -c "
import sys, json
try:
    cfg = json.load(sys.stdin)
except:
    cfg = {}
cfg.setdefault('auths', {})['ghcr.io'] = {'auth': '$AUTH'}
json.dump(cfg, sys.stdout)
" > "$DOCKER_CONFIG/config.json" 2>/dev/null || \
    printf '{"auths":{"ghcr.io":{"auth":"%s"}}}' "$AUTH" > "$DOCKER_CONFIG/config.json"
else
  printf '{"auths":{"ghcr.io":{"auth":"%s"}}}' "$AUTH" > "$DOCKER_CONFIG/config.json"
fi

echo "DOCKER_CONFIG=${DOCKER_CONFIG}" >> "$GITHUB_ENV"

echo "🔐 Authenticated to ghcr.io as ${ACTOR}"
