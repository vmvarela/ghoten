#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "$ROOT/ghoten.env"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
SOURCE="$WORK/source"
BASE="$WORK/base"
SERIES="$WORK/series"
mkdir -p "$SERIES" "$ROOT/patches"

git clone --quiet --filter=blob:none https://github.com/vmvarela/ghoten.git "$SOURCE"
git clone --quiet --filter=blob:none https://github.com/opentofu/opentofu.git "$BASE"
git -C "$BASE" checkout --quiet "$OPENTOFU_COMMIT"
mapfile -t PATHS < <(grep -Ev '^[[:space:]]*(#|$)' "$ROOT/patches/smart-refresh-paths.txt")

n=0
while read -r from to; do
  [[ -z "${from:-}" ]] && continue
  n=$((n+1))
  raw="$SERIES/$n.raw.patch"
  patch="$SERIES/$n.patch"
  git -C "$SOURCE" diff --binary "$from" "$to" -- "${PATHS[@]}" > "$raw"
  sed \
    -e 's#a/internal/ghoten/#a/internal/tofu/#g' \
    -e 's#b/internal/ghoten/#b/internal/tofu/#g' \
    -e 's#github.com/vmvarela/ghoten/#github.com/opentofu/opentofu/#g' \
    -e 's#github.com/opentofu/opentofu/internal/ghoten#github.com/opentofu/opentofu/internal/tofu#g' \
    -e 's/\bghoten\./tofu./g' \
    -e 's/^ package ghoten$/ package tofu/' \
    -e 's/^-package ghoten$/-package tofu/' \
    -e 's/^+package ghoten$/+package tofu/' \
    "$raw" > "$patch"
  [[ -s "$patch" ]] || continue
  echo "Applying Smart Refresh core delta $n: $from -> $to"
  if git -C "$BASE" apply --check "$patch"; then
    git -C "$BASE" apply "$patch"
  else
    git -C "$BASE" apply --reject --whitespace=nowarn "$patch" || true
    if [[ "$n" -eq 4 ]]; then
      python3 "$ROOT/scripts/port-smart-refresh-cleanup.py" "$BASE"
      mapfile -t rejects < <(find "$BASE" -name '*.rej' -print)
      for reject in "${rejects[@]}"; do
        case "$reject" in
          */internal/tofu/context_apply.go.rej|*/internal/tofu/transform_smart_refresh.go.rej) rm -f "$reject" ;;
          *) echo "Unexpected reject: $reject" >&2; cat "$reject"; exit 1 ;;
        esac
      done
    else
      find "$BASE" -name '*.rej' -print -exec cat {} \;
      exit 1
    fi
  fi
done < "$ROOT/patches/smart-refresh-series.txt"

# Port state/API and current OpenTofu CLI plumbing surgically. These scripts
# edit the exact 1.13.0 structures rather than copying fork-era source files.
python3 "$ROOT/scripts/port-compat.py" "$BASE"
python3 "$ROOT/scripts/port-plumbing.py" "$BASE"

# countHook must satisfy the extended Hook interface. Keep telemetry minimal;
# the product feature is the skip decision itself, not UI counters.
python3 - "$BASE" <<'PY'
from pathlib import Path
import sys
p=Path(sys.argv[1])/"internal/command/views/hook_count.go"
s=p.read_text()
if "PostSkipRefresh(" not in s:
    marker="func (h *countHook) PostRefresh("
    i=s.find(marker)
    if i < 0:
        raise SystemExit("hook_count.go: PostRefresh not found")
    method='func (h *countHook) PostSkipRefresh(_ addrs.AbsResourceInstance, _ states.Generation) (tofu.HookAction, error) {\n\treturn tofu.HookActionContinue, nil\n}\n\n'
    s=s[:i]+method+s[i:]
    p.write_text(s)
PY

gofmt -w \
  "$BASE/internal/tofu" \
  "$BASE/internal/states/instance_object.go" \
  "$BASE/internal/states/instance_object_src.go" \
  "$BASE/internal/states/state_deepcopy.go" \
  "$BASE/internal/states/statefile/version4.go" \
  "$BASE/internal/command/arguments/extended.go" \
  "$BASE/internal/command/arguments/apply.go" \
  "$BASE/internal/command/views/hook_count.go" \
  "$BASE/internal/backend/backend.go" \
  "$BASE/internal/backend/local/backend_local.go" \
  "$BASE/internal/command/apply.go" \
  "$BASE/internal/command/plan.go"

(
  cd "$BASE"
  GOWORK=off go test ./internal/tofu ./internal/states/... ./internal/plans ./internal/command/arguments ./internal/command/views/... ./internal/backend/local
  GOWORK=off go build -trimpath -o "$WORK/ghoten-smart" ./cmd/tofu
)

git -C "$BASE" diff --binary "$OPENTOFU_COMMIT" -- internal > "$ROOT/patches/smart-refresh.patch"
test -s "$ROOT/patches/smart-refresh.patch"
echo "Generated patches/smart-refresh.patch"
