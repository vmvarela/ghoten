#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "$ROOT/ghoten.env"

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
SOURCE="$WORK/source"
BASE="$WORK/base"
mkdir -p "$ROOT/patches"

git clone --quiet --filter=blob:none https://github.com/vmvarela/ghoten.git "$SOURCE"
git -C "$SOURCE" checkout --quiet "$LEGACY_GHOTEN_COMMIT"
git clone --quiet --filter=blob:none https://github.com/opentofu/opentofu.git "$BASE"
git -C "$BASE" checkout --quiet "$OPENTOFU_COMMIT"

mkdir -p "$BASE/internal/backend/remote-state/oras"
cp "$SOURCE/internal/backend/remote-state/oras/backend.go" "$BASE/internal/backend/remote-state/oras/backend.go"
cp "$SOURCE/internal/backend/remote-state/oras/client.go" "$BASE/internal/backend/remote-state/oras/client.go"

# Normalize only the wrapper's module identity. The shared ORAS engine remains
# an external dependency and contains all OCI state semantics.
sed -i   -e 's#github.com/vmvarela/ghoten/#github.com/opentofu/opentofu/#g'   -e 's/httpclient\.GhotenUserAgent(/httpclient.OpenTofuUserAgent(/g'   "$BASE/internal/backend/remote-state/oras/backend.go"   "$BASE/internal/backend/remote-state/oras/client.go"

python3 - "$BASE" <<'PY'
from pathlib import Path
import re
import sys

root = Path(sys.argv[1])

# Retention is synchronous at the shared backend adapter boundary. Do not leak
# a backend-specific capability into OpenTofu remote.Client.
client = root / "internal/backend/remote-state/oras/client.go"
text = client.read_text()
text = text.replace("var _ remote.ClientRetentionWaiter = (*RemoteClient)(nil)\n", "")
text = re.sub(
    r"\nfunc \(c \*RemoteClient\) WaitForRetention\(\) \{\n\tc\.mgr\.WaitForRetention\(\)\n\}\n",
    "\n",
    text,
    count=1,
)
client.write_text(text)

# Register exactly one new backend in the upstream registry.
init = root / "internal/backend/init/init.go"
text = init.read_text()
import_anchor = 'backendKubernetes "github.com/opentofu/opentofu/internal/backend/remote-state/kubernetes"\n'
if import_anchor not in text:
    raise SystemExit("backend init: kubernetes import anchor not found")
text = text.replace(
    import_anchor,
    import_anchor + 'backendORAS "github.com/opentofu/opentofu/internal/backend/remote-state/oras"\n',
    1,
)
map_anchor = '\t\t"kubernetes": func(enc encryption.StateEncryption) backend.Backend { return backendKubernetes.New(enc) },\n'
if map_anchor not in text:
    raise SystemExit("backend init: kubernetes map anchor not found")
text = text.replace(
    map_anchor,
    map_anchor + '\t\t"oras":       func(enc encryption.StateEncryption) backend.Backend { return backendORAS.New(enc) },\n',
    1,
)
init.write_text(text)
PY

(
  cd "$BASE"
  GOWORK=off go mod edit -require="github.com/vmvarela/ghoten-oras-backend@$ORAS_BACKEND_VERSION"
  GOWORK=off go mod tidy
  gofmt -w internal/backend/remote-state/oras internal/backend/init/init.go
  GOWORK=off go test ./internal/backend/remote-state/oras ./internal/backend/init
  GOWORK=off go build -trimpath -o "$WORK/ghoten-oras" ./cmd/tofu
)

git -C "$BASE" diff --binary "$OPENTOFU_COMMIT" --   internal/backend/remote-state/oras   internal/backend/init/init.go   go.mod go.sum > "$ROOT/patches/oras.patch"

test -s "$ROOT/patches/oras.patch"
echo "Generated patches/oras.patch"
