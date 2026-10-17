#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=/dev/null
source "$ROOT/ghoten.env"

VERSION="${1:-}"
if [[ -z "$VERSION" ]]; then
  echo "Usage: $0 vX.Y.Z" >&2
  exit 2
fi
if [[ ! "$VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([+-][0-9A-Za-z.-]+)?$ ]]; then
  echo "ERROR: oras-state version must be a Go semantic version such as v0.2.0 or v0.2.0-rc.1." >&2
  exit 2
fi

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
SRC="$WORK/opentofu"
DEPS_PATCH="$WORK/deps.patch"
NEW_PATCH="$WORK/oras.patch"

git clone --quiet --filter=blob:none https://github.com/opentofu/opentofu.git "$SRC"
git -C "$SRC" checkout --quiet "$OPENTOFU_COMMIT"

git -C "$SRC" apply --check "$ROOT/patches/oras.patch"
git -C "$SRC" apply "$ROOT/patches/oras.patch"

GO_VERSION="$(awk '$1 == "go" { print $2; exit }' "$SRC/go.mod")"
if [[ -z "$GO_VERSION" ]]; then
  echo "ERROR: could not determine the pinned OpenTofu Go version." >&2
  exit 1
fi

(
  cd "$SRC"
  export GOTOOLCHAIN="go$GO_VERSION"
  GOWORK=off go get "github.com/vmvarela/oras-state@$VERSION"
  GOWORK=off go mod tidy
)

git -C "$SRC" diff --check -- go.mod go.sum
git -C "$SRC" diff --no-ext-diff --binary -- go.mod go.sum > "$DEPS_PATCH"

python3 - "$ROOT/patches/oras.patch" "$DEPS_PATCH" "$NEW_PATCH" <<'PY'
from pathlib import Path
import sys

old_path, deps_path, out_path = map(Path, sys.argv[1:])
old = old_path.read_text()
deps = deps_path.read_text()

lines = old.splitlines(keepends=True)
starts = [i for i, line in enumerate(lines) if line.startswith("diff --git ")]
starts.append(len(lines))

kept = []
for start, end in zip(starts, starts[1:]):
    section = lines[start:end]
    if not section:
        continue
    header = section[0]
    if header.startswith("diff --git a/go.mod b/go.mod"):
        continue
    if header.startswith("diff --git a/go.sum b/go.sum"):
        continue
    kept.extend(section)

if not deps.strip():
    raise SystemExit("generated dependency patch is empty")

result = deps
if not result.endswith("\n"):
    result += "\n"
result += "".join(kept)

Path(out_path).write_text(result)
PY

if cmp -s "$NEW_PATCH" "$ROOT/patches/oras.patch"; then
  echo "oras-state $VERSION is already reflected in patches/oras.patch"
else
  cp "$NEW_PATCH" "$ROOT/patches/oras.patch"
  echo "Updated patches/oras.patch for oras-state $VERSION"
fi

echo "Running the normal Ghoten verification gate"
bash "$ROOT/scripts/verify.sh"
