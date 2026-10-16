#!/usr/bin/env python3
from pathlib import Path
import sys

root = Path(sys.argv[1])

context = root / "internal/tofu/context_apply.go"
text = context.read_text()
start_marker = "\n\t// Backfill config expression hashes"
end_marker = "\n\tif plan.UIMode"
start = text.find(start_marker)
if start < 0:
    raise SystemExit("context_apply.go: expected legacy backfill marker not found")
end = text.find(end_marker, start)
if end < 0:
    raise SystemExit("context_apply.go: plan.UIMode marker not found after legacy backfill")
context.write_text(text[:start] + text[end:])

transform = root / "internal/tofu/transform_smart_refresh.go"
text = transform.read_text()
marker = "\n// backfillConfigExprHashes iterates over all resource instances in the state"
idx = text.find(marker)
if idx < 0:
    raise SystemExit("transform_smart_refresh.go: expected legacy backfill helper not found")
transform.write_text(text[:idx].rstrip() + "\n")
