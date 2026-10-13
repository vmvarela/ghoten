// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package statefile

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	version "github.com/hashicorp/go-version"

	"github.com/vmvarela/ghoten/internal/addrs"
	"github.com/vmvarela/ghoten/internal/encryption"
	"github.com/vmvarela/ghoten/internal/states"
	"github.com/vmvarela/ghoten/internal/tfdiags"
	tfversion "github.com/vmvarela/ghoten/version"
)

// countingWriter records every Write call, so that tests can assert that a
// failed serialization never reaches the writer at all.
type countingWriter struct {
	writes int
	size   int
}

var _ io.Writer = (*countingWriter)(nil)

func (w *countingWriter) Write(p []byte) (int, error) {
	w.writes++
	w.size += len(p)
	return len(p), nil
}

// fakeStateEncryption is a manual test double for encryption.StateEncryption.
type fakeStateEncryption struct {
	encrypt func([]byte) ([]byte, error)
}

var _ encryption.StateEncryption = (*fakeStateEncryption)(nil)

func (f *fakeStateEncryption) DecryptState(b []byte) ([]byte, encryption.EncryptionStatus, error) {
	return b, encryption.StatusUnknown, nil
}

func (f *fakeStateEncryption) EncryptState(b []byte) ([]byte, error) {
	if f.encrypt == nil {
		return b, nil
	}
	return f.encrypt(b)
}

// newTestFile returns a File containing a single valid root-module resource.
// Callers may pass a custom instance object or resource mode to create
// invalid states on purpose.
func newTestFile(obj *states.ResourceInstanceObjectSrc, mode addrs.ResourceMode) *File {
	state := states.NewState()
	providerConfig := addrs.AbsProviderConfig{
		Provider: addrs.NewDefaultProvider("test"),
		Module:   addrs.RootModule,
	}
	addr := addrs.Resource{Mode: mode, Type: "test_instance", Name: "foo"}
	if obj == nil {
		obj = &states.ResourceInstanceObjectSrc{
			AttrsJSON:     []byte(`{"id":"x"}`),
			Status:        states.ObjectReady,
			SchemaVersion: 0,
		}
	}
	state.RootModule().SetResourceProvider(addr, providerConfig)
	state.RootModule().SetResourceInstanceCurrent(addr.Instance(addrs.NoKey), obj, providerConfig, addrs.NoKey)
	return &File{
		Serial:  1,
		Lineage: "test-lineage",
		State:   state,
	}
}

func plannedStatusObj() *states.ResourceInstanceObjectSrc {
	return &states.ResourceInstanceObjectSrc{
		AttrsJSON:     []byte(`{"id":"x"}`),
		Status:        states.ObjectPlanned,
		SchemaVersion: 0,
	}
}

// Both Write and WriteForTest must produce zero writer writes and return the
// accumulated diagnostics (converted to a Go error by Write's API) when any
// validation or conversion step fails.
func TestVersion4WriteErrorsProduceZeroWrites(t *testing.T) {
	invalidMode := newTestFile(nil, addrs.InvalidResourceMode)
	plannedStatus := newTestFile(plannedStatusObj(), addrs.ManagedResourceMode)

	// Errors from two different conversion phases must both be accumulated
	// and preserved: an unserializable resource mode plus an instance object
	// whose status cannot be serialized.
	state := states.NewState()
	providerConfig := addrs.AbsProviderConfig{
		Provider: addrs.NewDefaultProvider("test"),
		Module:   addrs.RootModule,
	}
	validAddr := addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "test_instance", Name: "foo"}
	invalidAddr := addrs.Resource{Mode: addrs.InvalidResourceMode, Type: "other", Name: "bar"}
	state.RootModule().SetResourceProvider(validAddr, providerConfig)
	state.RootModule().SetResourceProvider(invalidAddr, providerConfig)
	state.RootModule().SetResourceInstanceCurrent(validAddr.Instance(addrs.NoKey), plannedStatusObj(), providerConfig, addrs.NoKey)
	accumulated := &File{Serial: 1, Lineage: "test-lineage", State: state}

	tests := map[string]struct {
		file           *File
		expectContains []string
	}{
		"invalid resource mode": {
			file:           invalidMode,
			expectContains: []string{"Failed to serialize resource in state"},
		},
		"invalid instance status": {
			file:           plannedStatus,
			expectContains: []string{"Failed to serialize resource instance in state"},
		},
		"errors accumulated across phases are preserved": {
			file: accumulated,
			expectContains: []string{
				"Failed to serialize resource in state",
				"Failed to serialize resource instance in state",
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Run("Write", func(t *testing.T) {
				cw := &countingWriter{}
				err := Write(tc.file, cw, encryption.StateEncryptionDisabled())
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if cw.writes != 0 {
					t.Errorf("expected zero writer writes, got %d (%d bytes)", cw.writes, cw.size)
				}
				for _, frag := range tc.expectContains {
					if !strings.Contains(err.Error(), frag) {
						t.Errorf("expected error to contain %q, got:\n%s", frag, err.Error())
					}
				}
			})
			t.Run("WriteForTest", func(t *testing.T) {
				cw := &countingWriter{}
				err := WriteForTest(tc.file, cw)
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if cw.writes != 0 {
					t.Errorf("expected zero writer writes, got %d (%d bytes)", cw.writes, cw.size)
				}
				for _, frag := range tc.expectContains {
					if !strings.Contains(err.Error(), frag) {
						t.Errorf("expected error to contain %q, got:\n%s", frag, err.Error())
					}
				}
			})
		})
	}
}

// An encryption failure is fatal for the write even when the encryption
// implementation also returns candidate bytes: the writer must receive
// nothing.
func TestVersion4WriteEncryptionErrorZeroWrites(t *testing.T) {
	for name, bytesBack := range map[string][]byte{
		"error with bytes":    []byte(`{"fake":"encrypted-payload"}`),
		"error without bytes": nil,
	} {
		t.Run(name, func(t *testing.T) {
			enc := &fakeStateEncryption{
				encrypt: func(b []byte) ([]byte, error) {
					return bytesBack, errors.New("boom")
				},
			}
			cw := &countingWriter{}
			err := Write(newTestFile(nil, addrs.ManagedResourceMode), cw, enc)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), "boom") {
				t.Errorf("expected encryption error to be preserved, got:\n%s", err)
			}
			if cw.writes != 0 {
				t.Errorf("expected zero writer writes, got %d (%d bytes)", cw.writes, cw.size)
			}
		})
	}
}

// A diagnostics-aware encryption double may carry warning-only diagnostics
// through its error return (tfdiags.NonFatalError from ErrWithWarnings).
// These warnings must be accumulated and preserved, must not suppress valid
// output, and the public Write must return nil for warning-only
// serialization.
func TestVersion4WriteEncryptionWarningsPreserved(t *testing.T) {
	encryptCalled := false
	enc := &fakeStateEncryption{
		encrypt: func(b []byte) ([]byte, error) {
			encryptCalled = true
			var diags tfdiags.Diagnostics
			diags = diags.Append(tfdiags.Sourceless(
				tfdiags.Warning,
				"Encryption state migration warning",
				"The given state fileformat is out of date, so the state file will be rewritten with a new format.",
			))
			// Valid bytes plus a warning-only error channel value.
			return b, diags.ErrWithWarnings()
		},
	}

	// Internal check: writeStateV4 accumulates the warnings with no errors
	// and still produces output.
	cw := &countingWriter{}
	file := newTestFile(nil, addrs.ManagedResourceMode)
	diags := writeStateV4(file, cw, enc, false)
	if !encryptCalled {
		t.Fatal("encryption was never called")
	}
	if diags.HasErrors() {
		t.Errorf("expected no errors in accumulated diagnostics, got:\n%s", diags.Err())
	}
	if len(diags) == 0 {
		t.Fatal("expected accumulated warning diagnostics, got none")
	}
	for i, diag := range diags {
		if got := diag.Severity(); got != tfdiags.Warning {
			t.Errorf("expected diagnostic %d to be a warning, got %s: %v", i, got, diag)
		}
	}
	if cw.writes == 0 || cw.size == 0 {
		t.Errorf("expected valid output despite warnings, got %d writes (%d bytes)", cw.writes, cw.size)
	}

	// Public API check: Write returns nil error and valid, readable bytes
	// for warning-only serialization.
	var buf bytes.Buffer
	if err := Write(newTestFile(nil, addrs.ManagedResourceMode), &buf, enc); err != nil {
		t.Errorf("expected nil error from Write for warning-only serialization, got:\n%s", err)
	}
	if buf.Len() == 0 {
		t.Fatal("expected non-empty output from Write")
	}
	if _, reDiags := readStateV4(buf.Bytes()); reDiags.HasErrors() {
		t.Errorf("written output is not a valid state file:\n%s", reDiags.Err())
	}
}

// Warning-only diagnostics must not suppress valid output. This fixture
// triggers the read-side "Provider field conflict in state" warning but no
// error; the resulting state must still serialize without error.
func TestVersion4WriteWarningsStillProduceValidOutput(t *testing.T) {
	src := []byte(`{
		"version": 4,
		"terraform_version": "1.0.0",
		"serial": 1,
		"lineage": "abc",
		"outputs": {},
		"resources": [{
			"mode": "managed",
			"type": "test_instance",
			"name": "foo",
			"provider": "provider[\"registry.opentofu.org/hashicorp/test\"]",
			"instances": [{
				"provider": "provider[\"registry.opentofu.org/hashicorp/test\"]",
				"schema_version": 0,
				"attributes": {"id": "x"}
			}]
		}]
	}`)

	file, diags := readStateV4(src)
	if diags.HasErrors() {
		t.Fatalf("expected no errors from fixture, got:\n%s", diags.Err())
	}
	if len(diags) == 0 {
		t.Fatal("expected a warning from fixture, got none")
	}

	t.Run("Write", func(t *testing.T) {
		var buf bytes.Buffer
		if err := Write(file, &buf, encryption.StateEncryptionDisabled()); err != nil {
			t.Fatalf("unexpected error writing warning-only state:\n%s", err)
		}
		checkValidOutput(t, buf.Bytes())
	})
	t.Run("WriteForTest", func(t *testing.T) {
		var buf bytes.Buffer
		if err := WriteForTest(file, &buf); err != nil {
			t.Fatalf("unexpected error writing warning-only state:\n%s", err)
		}
		if buf.Len() == 0 {
			t.Fatal("expected non-empty output")
		}
		checkValidOutput(t, buf.Bytes())
	})
}

func checkValidOutput(t *testing.T, out []byte) {
	t.Helper()
	if _, diags := readStateV4(out); diags.HasErrors() {
		t.Fatalf("written output is not a valid state file:\n%s", diags.Err())
	}
}

// compactStateV4Golden devuelve la serialización compacta exacta de
// newTestFile(nil, addrs.ManagedResourceMode) con la versión indicada.
func compactStateV4Golden(versionStamp string) string {
	instanceJSON := `"instances":[{"schema_version":0,"attributes":{"id":"x"},"sensitive_attributes":[]}]`
	return fmt.Sprintf(`{"version":4,"terraform_version":%q,"serial":1,"lineage":"test-lineage","outputs":{},"resources":[{"mode":"managed","type":"test_instance","name":"foo","provider":"provider[\"registry.opentofu.org/hashicorp/test\"]",%s}],"check_results":null}`+"\n", versionStamp, instanceJSON)
}

// indentedStateV4Golden devuelve la serialización del mismo estado con dos
// espacios de sangría, como producen WriteIndent y WriteForTest.
func indentedStateV4Golden(versionStamp string) string {
	return fmt.Sprintf(`{
  "version": 4,
  "terraform_version": %q,
  "serial": 1,
  "lineage": "test-lineage",
  "outputs": {},
  "resources": [
    {
      "mode": "managed",
      "type": "test_instance",
      "name": "foo",
      "provider": "provider[\"registry.opentofu.org/hashicorp/test\"]",
      "instances": [
        {
          "schema_version": 0,
          "attributes": {
            "id": "x"
          },
          "sensitive_attributes": []
        }
      ]
    }
  ],
  "check_results": null
}
`, versionStamp)
}

// Comprueba los bytes exactos: Write sigue siendo compacto y actualiza la
// versión; WriteIndent usa dos espacios y un único salto final; WriteForTest
// mantiene ese formato sin modificar la versión indicada por la prueba.
func TestVersion4WriteForTestGoldenOutput(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteForTest(newTestFile(nil, addrs.ManagedResourceMode), &buf); err != nil {
		t.Fatalf("unexpected error:\n%s", err)
	}
	if got := buf.String(); got != indentedStateV4Golden("") {
		t.Errorf("wrong WriteForTest output\n got: %s\nwant: %s", got, indentedStateV4Golden(""))
	}

	if testing.Short() {
		return
	}
	// Write actualiza la versión y conserva el formato compacto.
	var buf2 bytes.Buffer
	if err := Write(newTestFile(nil, addrs.ManagedResourceMode), &buf2, encryption.StateEncryptionDisabled()); err != nil {
		t.Fatalf("unexpected error:\n%s", err)
	}
	wantStamped := compactStateV4Golden(tfversion.SemVer.String())
	if got := buf2.String(); got != wantStamped {
		t.Errorf("wrong Write output\n got: %s\nwant: %s", got, wantStamped)
	}

	// WriteIndent actualiza la versión y aplica sangría.
	var buf3 bytes.Buffer
	if err := WriteIndent(newTestFile(nil, addrs.ManagedResourceMode), &buf3, encryption.StateEncryptionDisabled()); err != nil {
		t.Fatalf("unexpected error:\n%s", err)
	}
	wantIndentStamped := indentedStateV4Golden(tfversion.SemVer.String())
	if got := buf3.String(); got != wantIndentStamped {
		t.Errorf("wrong WriteIndent output\n got: %s\nwant: %s", got, wantIndentStamped)
	}
}

// WriteForTest conserva la versión proporcionada; Write la reemplaza por la
// versión actual.
func TestVersion4WriteForTestPreservesVersionStamp(t *testing.T) {
	file := newTestFile(nil, addrs.ManagedResourceMode)
	file.TerraformVersion = version.Must(version.NewVersion("0.0.0-test"))
	var buf bytes.Buffer
	if err := WriteForTest(file, &buf); err != nil {
		t.Fatalf("unexpected error:\n%s", err)
	}
	if got, want := file.TerraformVersion.String(), "0.0.0-test"; got != want {
		t.Errorf("WriteForTest mutated the caller's version stamp: got %q, want %q", got, want)
	}
	if got, want := buf.String(), indentedStateV4Golden("0.0.0-test"); got != want {
		t.Errorf("wrong WriteForTest output\n got: %s\nwant: %s", got, want)
	}

	if testing.Short() {
		return
	}
	// Write reemplaza la versión proporcionada por la versión actual.
	stamped := newTestFile(nil, addrs.ManagedResourceMode)
	stamped.TerraformVersion = version.Must(version.NewVersion("0.0.0-test"))
	var buf2 bytes.Buffer
	if err := Write(stamped, &buf2, encryption.StateEncryptionDisabled()); err != nil {
		t.Fatalf("unexpected error:\n%s", err)
	}
	if got, want := stamped.TerraformVersion.String(), tfversion.SemVer.String(); got != want {
		t.Errorf("Write did not stamp the current version: got %q, want %q", got, want)
	}
	if got, want := buf2.String(), compactStateV4Golden(tfversion.SemVer.String()); got != want {
		t.Errorf("wrong Write output\n got: %s\nwant: %s", got, want)
	}
}

// reversibleEncryption simula un cifrado determinista: registra el texto en
// claro y lo codifica en base64 compacto. El sobre no lleva saltos de línea,
// por lo que no debe exponer el formato del texto en claro.
type reversibleEncryption struct {
	plaintext func([]byte)
}

var _ encryption.StateEncryption = (*reversibleEncryption)(nil)

func (e *reversibleEncryption) DecryptState(b []byte) ([]byte, encryption.EncryptionStatus, error) {
	const prefix = "enc:"
	payload, ok := bytes.CutPrefix(b, []byte(prefix))
	if !ok {
		return nil, encryption.StatusUnknown, fmt.Errorf("not an enc: envelope")
	}
	decoded, err := base64.StdEncoding.DecodeString(string(payload))
	if err != nil {
		return nil, encryption.StatusUnknown, fmt.Errorf("bad envelope: %s", err)
	}
	return decoded, encryption.StatusSatisfied, nil
}

func (e *reversibleEncryption) EncryptState(b []byte) ([]byte, error) {
	if e.plaintext != nil {
		e.plaintext(b)
	}
	return append([]byte("enc:"), base64.StdEncoding.EncodeToString(b)...), nil
}

// La sangría se aplica antes del cifrado: el cifrador recibe texto con dos
// espacios y un único salto final; el sobre permanece compacto y se descifra
// a un estado válido. Write debe enviar texto compacto al cifrador.
func TestVersion4WriteEncryptsIndentedPlaintext(t *testing.T) {
	var indentedPlaintext []byte
	var declaredEnvelope []byte
	enc := &reversibleEncryption{
		plaintext: func(b []byte) {
			indentedPlaintext = b
			declaredEnvelope = append([]byte("enc:"), base64.StdEncoding.EncodeToString(b)...)
		},
	}

	var buf bytes.Buffer
	if err := WriteIndent(newTestFile(nil, addrs.ManagedResourceMode), &buf, enc); err != nil {
		t.Fatalf("unexpected error:\n%s", err)
	}

	if got, want := string(indentedPlaintext), indentedStateV4Golden(tfversion.SemVer.String()); got != want {
		t.Errorf("encryptor did not receive the indented plaintext\n got: %s\nwant: %s", got, want)
	}
	if got := buf.Bytes(); !bytes.Equal(got, declaredEnvelope) {
		t.Errorf("written envelope differs from the encryptor output:\ngot:  %q\nwant: %q", got, declaredEnvelope)
	}
	if bytes.Contains(buf.Bytes(), []byte("\n")) {
		t.Error("encrypted envelope must stay compact, found a newline")
	}

	// El sobre se descifra a bytes de estado indentados y válidos.
	decrypted, status, err := enc.DecryptState(buf.Bytes())
	if err != nil {
		t.Fatalf("failed to decrypt the envelope: %s", err)
	}
	if status != encryption.StatusSatisfied {
		t.Errorf("unexpected decryption status %v", status)
	}
	if got, want := string(decrypted), indentedStateV4Golden(tfversion.SemVer.String()); got != want {
		t.Errorf("decrypted plaintext is not the indented state file\n got: %s\nwant: %s", got, want)
	}
	f, diags := readStateV4(decrypted)
	if diags.HasErrors() {
		t.Fatalf("decrypted plaintext is not a valid state file:\n%s", diags.Err())
	}
	if got, want := f.TerraformVersion.String(), tfversion.SemVer.String(); got != want {
		t.Errorf("wrong version stamp\n got: %q\nwant: %q", got, want)
	}

	// Write también conserva el formato compacto antes del cifrado.
	var compactPlaintext []byte
	compactEnc := &reversibleEncryption{
		plaintext: func(b []byte) { compactPlaintext = b },
	}
	var buf2 bytes.Buffer
	if err := Write(newTestFile(nil, addrs.ManagedResourceMode), &buf2, compactEnc); err != nil {
		t.Fatalf("unexpected error:\n%s", err)
	}
	if got, want := string(compactPlaintext), compactStateV4Golden(tfversion.SemVer.String()); got != want {
		t.Errorf("compact Write did not hand the encryptor compact plaintext\n got: %s\nwant: %s", got, want)
	}
}
