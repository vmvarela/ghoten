#!/usr/bin/env python3
from pathlib import Path
import sys
root = Path(sys.argv[1])

context = root / "internal/tofu/context_apply.go"
text = context.read_text()
start = text.find("\n\t// Backfill config expression hashes")
end = text.find("\n\tif plan.UIMode", start)
if start >= 0 and end >= 0:
    context.write_text(text[:start] + text[end:])

transform = root / "internal/tofu/transform_smart_refresh.go"
if transform.exists():
    text = transform.read_text()
    marker = "\n// backfillConfigExprHashes iterates over all resource instances in the state"
    idx = text.find(marker)
    if idx >= 0:
        transform.write_text(text[:idx].rstrip() + "\n")
