// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package statefile

import (
	"bytes"
	"strings"
	"testing"

	"github.com/vmvarela/ghoten/internal/addrs"
	"github.com/vmvarela/ghoten/internal/encryption"
	"github.com/vmvarela/ghoten/internal/states"
)

func TestVersion4RefreshMetadataRoundTrip(t *testing.T) {
	addr := addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "test_instance", Name: "foo"}

	t.Run("with metadata", func(t *testing.T) {
		obj := baseFixtureObj()
		obj.RefreshFingerprint = []byte("fp-1234")
		obj.LastRefreshUnix = 1729000000
		file := fixtureFileAddr(addr, map[addrs.InstanceKey]*states.ResourceInstanceObjectSrc{addrs.NoKey: obj}, nil)

		var buf bytes.Buffer
		if err := Write(file, &buf, encryption.StateEncryptionDisabled()); err != nil {
			t.Fatalf("unexpected error:\n%s", err)
		}
		out := buf.String()
		if !strings.Contains(out, `"refresh_fingerprint":"ZnAtMTIzNA=="`) {
			t.Fatalf("written v4 JSON lacks refresh_fingerprint:\n%s", out)
		}
		if !strings.Contains(out, `"last_refresh_unix":1729000000`) {
			t.Fatalf("written v4 JSON lacks last_refresh_unix:\n%s", out)
		}

		got, err := Read(bytes.NewReader(buf.Bytes()), encryption.StateEncryptionDisabled())
		if err != nil {
			t.Fatalf("unexpected error:\n%s", err)
		}
		current := got.State.RootModule().ResourceInstance(addr.Instance(addrs.NoKey)).Current
		if current == nil {
			t.Fatal("resource missing from state after round trip")
		}
		if !bytes.Equal(current.RefreshFingerprint, []byte("fp-1234")) {
			t.Fatalf("got RefreshFingerprint %q after round trip", current.RefreshFingerprint)
		}
		if current.LastRefreshUnix != 1729000000 {
			t.Fatalf("got LastRefreshUnix %d after round trip", current.LastRefreshUnix)
		}
	})

	t.Run("without metadata", func(t *testing.T) {
		file := fixtureFile(addrs.ManagedResourceMode, map[addrs.InstanceKey]*states.ResourceInstanceObjectSrc{addrs.NoKey: baseFixtureObj()}, nil)

		var buf bytes.Buffer
		if err := Write(file, &buf, encryption.StateEncryptionDisabled()); err != nil {
			t.Fatalf("unexpected error:\n%s", err)
		}
		out := buf.String()
		if strings.Contains(out, "refresh_fingerprint") || strings.Contains(out, "last_refresh_unix") {
			t.Fatalf("written v4 JSON contains empty refresh metadata:\n%s", out)
		}

		got, err := Read(bytes.NewReader(buf.Bytes()), encryption.StateEncryptionDisabled())
		if err != nil {
			t.Fatalf("unexpected error:\n%s", err)
		}
		current := got.State.RootModule().ResourceInstance(addr.Instance(addrs.NoKey)).Current
		if current == nil {
			t.Fatal("resource missing from state after round trip")
		}
		if len(current.RefreshFingerprint) != 0 || current.LastRefreshUnix != 0 {
			t.Fatalf("got RefreshFingerprint %q, LastRefreshUnix %d after round trip", current.RefreshFingerprint, current.LastRefreshUnix)
		}
	})
}
