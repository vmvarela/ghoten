#!/usr/bin/env python3
from pathlib import Path
import re
import sys

root = Path(sys.argv[1])

context = root / "internal/tofu/context_apply.go"
text = context.read_text()
pattern = re.compile(
    r"\n\t// Backfill config expression hashes for all resource instances that were\n"
    r".*?"
    r"\n\tif !diags\.HasErrors\(\) \{\n"
    r"\t\tbackfillConfigExprHashes\(newState, config\)\n"
    r"\t\}\n",
    re.S,
)
text2, n = pattern.subn("\n", text, count=1)
if n != 1:
    raise SystemExit("context_apply.go: expected legacy backfill block not found")
context.write_text(text2)

transform = root / "internal/tofu/transform_smart_refresh.go"
text = transform.read_text()
marker = "\n// backfillConfigExprHashes iterates over all resource instances in the state\n"
idx = text.find(marker)
if idx < 0:
    raise SystemExit("transform_smart_refresh.go: expected backfill helper not found")
transform.write_text(text[:idx].rstrip() + "\n")
