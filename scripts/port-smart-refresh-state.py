#!/usr/bin/env python3
from pathlib import Path
import sys

root = Path(sys.argv[1])

def rewrite(rel, edits):
    path = root / rel
    text = path.read_text()
    for old, new in edits:
        if old not in text:
            raise SystemExit(f"{rel}: expected pattern not found:\n{old}")
        text = text.replace(old, new, 1)
    path.write_text(text)

metadata_fields = """\t// ConfigExprHash is the legacy coarse-grained Smart Refresh fingerprint.\n\tConfigExprHash []byte\n\n\t// RefreshFingerprint is the authoritative evaluated per-instance fingerprint.\n\tRefreshFingerprint []byte\n\n\t// LastRefreshUnix is the UTC Unix time of the last successful remote read.\n\tLastRefreshUnix int64\n\n"""

rewrite("internal/states/instance_object.go", [
    ("\tSkipDestroy bool\n\n\t// Deferred", "\tSkipDestroy bool\n\n" + metadata_fields + "\t// Deferred"),
    ("\t\tSkipDestroy:             o.SkipDestroy,\n\t\tDeferred:                o.Deferred,",
     "\t\tSkipDestroy:             o.SkipDestroy,\n\t\tConfigExprHash:          o.ConfigExprHash,\n\t\tRefreshFingerprint:      o.RefreshFingerprint,\n\t\tLastRefreshUnix:         o.LastRefreshUnix,\n\t\tDeferred:                o.Deferred,"),
])

rewrite("internal/states/instance_object_src.go", [
    ("\tSkipDestroy         bool\n\t// Deferred", "\tSkipDestroy         bool\n\n" + metadata_fields + "\t// Deferred"),
    ("\tif os.SkipDestroy != other.SkipDestroy {\n\t\treturn false\n\t}\n\n\tif !bytes.Equal(os.IdentityJSON, other.IdentityJSON) {",
     "\tif os.SkipDestroy != other.SkipDestroy {\n\t\treturn false\n\t}\n\n\tif !bytes.Equal(os.ConfigExprHash, other.ConfigExprHash) {\n\t\treturn false\n\t}\n\tif !bytes.Equal(os.RefreshFingerprint, other.RefreshFingerprint) {\n\t\treturn false\n\t}\n\tif os.LastRefreshUnix != other.LastRefreshUnix {\n\t\treturn false\n\t}\n\n\tif !bytes.Equal(os.IdentityJSON, other.IdentityJSON) {"),
    ("\t\tSkipDestroy:         os.SkipDestroy,\n\t\tDeferred:            os.Deferred,",
     "\t\tSkipDestroy:         os.SkipDestroy,\n\t\tConfigExprHash:      os.ConfigExprHash,\n\t\tRefreshFingerprint:  os.RefreshFingerprint,\n\t\tLastRefreshUnix:     os.LastRefreshUnix,\n\t\tDeferred:            os.Deferred,"),
])

rewrite("internal/states/state_deepcopy.go", [
    ("\tvar private []byte\n\tif os.Private != nil {\n\t\tprivate = make([]byte, len(os.Private))\n\t\tcopy(private, os.Private)\n\t}\n\n\t// Some addrs.Referenceable",
     "\tvar private []byte\n\tif os.Private != nil {\n\t\tprivate = make([]byte, len(os.Private))\n\t\tcopy(private, os.Private)\n\t}\n\n\tconfigExprHash := append([]byte(nil), os.ConfigExprHash...)\n\trefreshFingerprint := append([]byte(nil), os.RefreshFingerprint...)\n\n\t// Some addrs.Referenceable"),
    ("\t\tSkipDestroy:             os.SkipDestroy,\n\t\tDeferred:                os.Deferred,",
     "\t\tSkipDestroy:             os.SkipDestroy,\n\t\tConfigExprHash:          configExprHash,\n\t\tRefreshFingerprint:      refreshFingerprint,\n\t\tLastRefreshUnix:         os.LastRefreshUnix,\n\t\tDeferred:                os.Deferred,"),
    ("\tvar private []byte\n\tif o.Private != nil {\n\t\tprivate = make([]byte, len(o.Private))\n\t\tcopy(private, o.Private)\n\t}\n\n\t// Some addrs.Referenceable",
     "\tvar private []byte\n\tif o.Private != nil {\n\t\tprivate = make([]byte, len(o.Private))\n\t\tcopy(private, o.Private)\n\t}\n\n\tconfigExprHash := append([]byte(nil), o.ConfigExprHash...)\n\trefreshFingerprint := append([]byte(nil), o.RefreshFingerprint...)\n\n\t// Some addrs.Referenceable"),
    ("\t\tSkipDestroy:         o.SkipDestroy,\n\t\tDeferred:            o.Deferred,",
     "\t\tSkipDestroy:         o.SkipDestroy,\n\t\tConfigExprHash:      configExprHash,\n\t\tRefreshFingerprint:  refreshFingerprint,\n\t\tLastRefreshUnix:     o.LastRefreshUnix,\n\t\tDeferred:            o.Deferred,"),
])

rewrite("internal/states/statefile/version4.go", [
    ("\t\t\tobj := &states.ResourceInstanceObjectSrc{\n\t\t\t\tSchemaVersion:       isV4.SchemaVersion,\n\t\t\t\tCreateBeforeDestroy: isV4.CreateBeforeDestroy,\n\t\t\t\tSkipDestroy:         isV4.SkipDestroy,\n\t\t\t}",
     "\t\t\tobj := &states.ResourceInstanceObjectSrc{\n\t\t\t\tSchemaVersion:       isV4.SchemaVersion,\n\t\t\t\tCreateBeforeDestroy: isV4.CreateBeforeDestroy,\n\t\t\t\tSkipDestroy:         isV4.SkipDestroy,\n\t\t\t\tConfigExprHash:      isV4.ConfigExprHash,\n\t\t\t\tRefreshFingerprint:  isV4.RefreshFingerprint,\n\t\t\t\tLastRefreshUnix:     isV4.LastRefreshUnix,\n\t\t\t}"),
    ("\t\tSkipDestroy:             obj.SkipDestroy,\n\t\tIdentity:                identity,",
     "\t\tSkipDestroy:             obj.SkipDestroy,\n\t\tConfigExprHash:          obj.ConfigExprHash,\n\t\tRefreshFingerprint:      obj.RefreshFingerprint,\n\t\tLastRefreshUnix:         obj.LastRefreshUnix,\n\t\tIdentity:                identity,"),
    ("\tCreateBeforeDestroy bool `json:\"create_before_destroy,omitempty\"`\n\tSkipDestroy         bool `json:\"skip_destroy,omitempty\"`\n\n\tIdentity",
     "\tCreateBeforeDestroy bool `json:\"create_before_destroy,omitempty\"`\n\tSkipDestroy         bool `json:\"skip_destroy,omitempty\"`\n\n\tConfigExprHash      []byte `json:\"config_expr_hash,omitempty\"`\n\tRefreshFingerprint []byte `json:\"refresh_fingerprint,omitempty\"`\n\tLastRefreshUnix     int64  `json:\"last_refresh_unix,omitempty\"`\n\n\tIdentity"),
])
