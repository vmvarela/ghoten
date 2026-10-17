#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GHOTEN_BIN="${GHOTEN_BIN:-$ROOT/dist/ghoten}"
ZOT_IMAGE="${ZOT_IMAGE:-ghcr.io/project-zot/zot:v2.1.21}"

if [[ ! -x "$GHOTEN_BIN" ]]; then
  echo "ERROR: Ghoten binary not found or not executable: $GHOTEN_BIN" >&2
  exit 1
fi

for command in docker curl; do
  if ! command -v "$command" >/dev/null 2>&1; then
    echo "ERROR: required command not found: $command" >&2
    exit 1
  fi
done

WORK="$(mktemp -d)"
WRITER="$WORK/writer"
READER="$WORK/reader"
LOCK_MARKER="$WORK/lock-held"
HOLDER_LOG="$WORK/holder.log"
CONTAINER="ghoten-zot-$RANDOM-$$"
HOLDER_PID=""

cleanup() {
  if [[ -n "$HOLDER_PID" ]] && kill -0 "$HOLDER_PID" >/dev/null 2>&1; then
    kill "$HOLDER_PID" >/dev/null 2>&1 || true
    wait "$HOLDER_PID" >/dev/null 2>&1 || true
  fi
  docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

mkdir -p "$WRITER" "$READER"

echo "Starting disposable Zot registry ($ZOT_IMAGE)"
docker run --detach --rm \
  --name "$CONTAINER" \
  --publish 127.0.0.1::5000 \
  "$ZOT_IMAGE" >/dev/null

PORT=""
for _ in {1..30}; do
  PORT="$(docker port "$CONTAINER" 5000/tcp 2>/dev/null | sed -n 's/.*://p' | head -n1)"
  if [[ -n "$PORT" ]] && curl --fail --silent "http://127.0.0.1:$PORT/v2/" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

if [[ -z "$PORT" ]] || ! curl --fail --silent "http://127.0.0.1:$PORT/v2/" >/dev/null 2>&1; then
  echo "ERROR: Zot did not become ready." >&2
  docker logs "$CONTAINER" >&2 || true
  exit 1
fi

cat > "$WRITER/main.tf" <<'HCL'
terraform {
  backend "oras" {
    repository  = "127.0.0.1:__PORT__/ghoten-e2e"
    insecure    = true
    max_versions = 5
  }
}

variable "value" {
  type = string
}

resource "terraform_data" "value" {
  input            = var.value
  triggers_replace = [var.value]

  provisioner "local-exec" {
    command = "if [ -n \"$GHOTEN_E2E_LOCK_MARKER\" ]; then touch \"$GHOTEN_E2E_LOCK_MARKER\"; sleep \"$GHOTEN_E2E_LOCK_SLEEP\"; fi"
  }
}

output "value" {
  value = terraform_data.value.output
}
HCL

sed -i "s/__PORT__/$PORT/g" "$WRITER/main.tf"
cp "$WRITER/main.tf" "$READER/main.tf"

export TF_IN_AUTOMATION=1
export CHECKPOINT_DISABLE=1
export GHOTEN_E2E_LOCK_MARKER=""
export GHOTEN_E2E_LOCK_SLEEP="0"

echo "Initializing writer through the ORAS backend"
"$GHOTEN_BIN" -chdir="$WRITER" init -input=false -no-color

echo "Applying initial synthetic state"
"$GHOTEN_BIN" -chdir="$WRITER" apply \
  -auto-approve -input=false -no-color -var='value=one'

echo "Initializing an independent reader directory"
"$GHOTEN_BIN" -chdir="$READER" init -input=false -no-color

echo "Checking a fresh-process no-change plan"
set +e
PLAN_OUTPUT="$("$GHOTEN_BIN" -chdir="$READER" plan \
  -detailed-exitcode -input=false -no-color -var='value=one' 2>&1)"
PLAN_STATUS=$?
set -e
if [[ "$PLAN_STATUS" -ne 0 ]]; then
  echo "ERROR: expected no-change plan (exit 0), got exit $PLAN_STATUS." >&2
  printf '%s\n' "$PLAN_OUTPUT" >&2
  exit 1
fi

FIRST_VALUE="$("$GHOTEN_BIN" -chdir="$READER" output -raw value)"
if [[ "$FIRST_VALUE" != "one" ]]; then
  echo "ERROR: fresh reader returned unexpected initial output." >&2
  exit 1
fi

echo "Persisting an updated state"
"$GHOTEN_BIN" -chdir="$WRITER" apply \
  -auto-approve -input=false -no-color -var='value=two'

SECOND_VALUE="$("$GHOTEN_BIN" -chdir="$READER" output -raw value)"
if [[ "$SECOND_VALUE" != "two" ]]; then
  echo "ERROR: fresh reader did not observe the updated state." >&2
  exit 1
fi

echo "Verifying read-only ORAS state history"
HISTORY_LIST="$("$GHOTEN_BIN" -chdir="$READER" history list --json)"
HISTORY_LIST="$HISTORY_LIST" python3 - <<'PY'
import json
import os

versions = json.loads(os.environ["HISTORY_LIST"])
numbers = [item["version"] for item in versions]
if numbers[:2] != [1, 2]:
    raise SystemExit(f"unexpected history versions: {numbers}")
if "one" in os.environ["HISTORY_LIST"] or "two" in os.environ["HISTORY_LIST"]:
    raise SystemExit("history list leaked state contents")
PY

HISTORY_SHOW="$("$GHOTEN_BIN" -chdir="$READER" history show --json 1)"
HISTORY_SHOW="$HISTORY_SHOW" python3 - <<'PY'
import json
import os

state = json.loads(os.environ["HISTORY_SHOW"])
encoded = json.dumps(state)
if "one" not in encoded:
    raise SystemExit("history show did not return version 1 state")
PY

HISTORY_FILE="$WORK/history-v1.tfstate"
"$GHOTEN_BIN" -chdir="$READER" history pull --output "$HISTORY_FILE" 1
HISTORY_FILE="$HISTORY_FILE" python3 - <<'PY'
import json
import os

with open(os.environ["HISTORY_FILE"], encoding="utf-8") as f:
    state = json.load(f)
if "one" not in json.dumps(state):
    raise SystemExit("history pull did not write version 1 state")
PY

LOCAL="$WORK/local"
mkdir -p "$LOCAL"
cat > "$LOCAL/main.tf" <<'HCL'
terraform {}
HCL
"$GHOTEN_BIN" -chdir="$LOCAL" init -input=false -no-color >/dev/null
set +e
UNSUPPORTED_OUTPUT="$("$GHOTEN_BIN" -chdir="$LOCAL" history list 2>&1)"
UNSUPPORTED_STATUS=$?
set -e
if [[ "$UNSUPPORTED_STATUS" -eq 0 ]] || ! grep -qi "does not support read-only state history" <<<"$UNSUPPORTED_OUTPUT"; then
  echo "ERROR: unsupported backend did not return the expected history capability error." >&2
  printf '%s\n' "$UNSUPPORTED_OUTPUT" >&2
  exit 1
fi

echo "Exercising state lock contention"
GHOTEN_E2E_LOCK_MARKER="$LOCK_MARKER" \
GHOTEN_E2E_LOCK_SLEEP="15" \
  "$GHOTEN_BIN" -chdir="$WRITER" apply \
    -auto-approve -input=false -no-color -var='value=locked' \
    >"$HOLDER_LOG" 2>&1 &
HOLDER_PID=$!

for _ in {1..60}; do
  if [[ -f "$LOCK_MARKER" ]]; then
    break
  fi
  if ! kill -0 "$HOLDER_PID" >/dev/null 2>&1; then
    echo "ERROR: lock-holder apply exited before reaching the hold point." >&2
    tail -n 80 "$HOLDER_LOG" >&2 || true
    exit 1
  fi
  sleep 0.5
done

if [[ ! -f "$LOCK_MARKER" ]]; then
  echo "ERROR: timed out waiting for lock-holder apply." >&2
  tail -n 80 "$HOLDER_LOG" >&2 || true
  exit 1
fi

set +e
LOCK_OUTPUT="$("$GHOTEN_BIN" -chdir="$READER" plan \
  -lock-timeout=0s -input=false -no-color -var='value=locked' 2>&1)"
LOCK_STATUS=$?
set -e

if [[ "$LOCK_STATUS" -eq 0 ]]; then
  echo "ERROR: competing process unexpectedly acquired the state lock." >&2
  exit 1
fi
if ! grep -qi "lock" <<<"$LOCK_OUTPUT"; then
  echo "ERROR: competing process failed, but not with a recognizable lock diagnostic." >&2
  printf '%s\n' "$LOCK_OUTPUT" >&2
  exit 1
fi

wait "$HOLDER_PID"
HOLDER_PID=""

FINAL_VALUE="$("$GHOTEN_BIN" -chdir="$READER" output -raw value)"
if [[ "$FINAL_VALUE" != "locked" ]]; then
  echo "ERROR: fresh reader did not observe state written after lock contention." >&2
  exit 1
fi

echo "ORAS backend E2E lifecycle passed."
