// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/mitchellh/cli"
	"github.com/opentofu/svchost"
	"github.com/opentofu/svchost/disco"

	"github.com/vmvarela/ghoten/internal/command/workdir"
	"github.com/vmvarela/ghoten/internal/getproviders"
)

// More thorough tests for providers mirror can be found in the e2etest
func TestProvidersMirror(t *testing.T) {
	// noop example
	t.Run("noop", func(t *testing.T) {
		c := &ProvidersMirrorCommand{
			Meta{
				WorkingDir: workdir.NewDir("."),
			},
		}
		code := c.Run([]string{"."})
		if code != 0 {
			t.Fatalf("wrong exit code. expected 0, got %d", code)
		}
	})

	t.Run("missing arg error", func(t *testing.T) {
		ui := new(cli.MockUi)
		c := &ProvidersMirrorCommand{
			Meta: Meta{
				WorkingDir: workdir.NewDir("."),
				Ui:         ui,
			},
		}
		code := c.Run([]string{})
		if code != 1 {
			t.Fatalf("wrong exit code. expected 1, got %d", code)
		}

		got := ui.ErrorWriter.String()
		if !strings.Contains(got, "Error: No output directory specified") {
			t.Fatalf("missing directory error from output, got:\n%s\n", got)
		}
	})
}

// mirrorTestHost is the (fake) provider registry host used by the mirror
// tests. Using the default registry host means the fake registry is allowed
// to omit real GPG signing machinery without enforcing signature checks.
const mirrorTestHost = "registry.opentofu.org"

// mirrorProv describes the fake registry state for one provider.
type mirrorProv struct {
	// versions is the list of released versions, ascending.
	versions []string

	// zips maps "<version>|<platform>" to the true archive contents.
	zips map[string][]byte

	// downloadFails maps "<version>|<platform>" to serving HTTP 500 for the
	// archive download, while the package metadata request still succeeds.
	downloadFails map[string]bool

	// authFails maps "<version>|<platform>" to serving corrupted archive
	// contents, so the download succeeds but authentication fails.
	authFails map[string]bool

	// metadata maps version to the package hash metadata mode:
	// "" (no packages at all), "full" (zh + true h1), "zh" (zh only), or
	// "extra" (zh + true h1 + one extra bogus h1).
	metadata map[string]string
}

// mirrorReg is a minimal in-memory fake of the provider registry service
// API on mirrorTestHost, with just enough behavior to exercise the
// "providers mirror" command end to end, hermetically.
type mirrorReg struct {
	t         *testing.T
	serverURL string
	svcs      *disco.Disco
	signer    *openpgp.Entity
	armor     string
	providers map[string]*mirrorProv
	h1        map[string]string // "<providerType>|<version>|<platform>"
	shas      map[string]string // "<providerType>|<version>|<platform>" -> hex sha of the true zip
	sigCache  map[string][]byte // "<providerType>|<version>" -> signature of the sums document
}

const mirrorTestPlatformA = "linux_amd64"
const mirrorTestPlatformB = "darwin_arm64"

// startMirrorRegistry spins up the fake registry and wires it into a
// discovery object for mirrorTestHost. Everything is cleaned up with the test.
func startMirrorRegistry(t *testing.T, providers map[string]*mirrorProv) *mirrorReg {
	reg := &mirrorReg{
		t:         t,
		providers: providers,
		h1:        map[string]string{},
		shas:      map[string]string{},
		sigCache:  map[string][]byte{},
	}

	// A single-use signing entity so that the SHA256SUMS documents are
	// properly signed for the providers served by this fake registry.
	entity, err := openpgp.NewEntity("mirror test signer", "throwaway key used only for testing", "testing@invalid", nil)
	if err != nil {
		t.Fatalf("failed to generate PGP key for testing: %s", err)
	}
	reg.signer = entity
	var armorBuf bytes.Buffer
	armorWriter, err := armor.Encode(&armorBuf, "PGP PUBLIC KEY BLOCK", nil)
	if err != nil {
		t.Fatalf("failed to begin armored public key: %s", err)
	}
	if err := entity.Serialize(armorWriter); err != nil {
		t.Fatalf("failed to serialize public key: %s", err)
	}
	armorWriter.Close()
	reg.armor = armorBuf.String()

	// Precompute the "h1" and "zh" hashes of each true archive.
	baseDir := t.TempDir()
	for mapKey, prov := range providers {
		// The handler keys its hash maps by the provider type only.
		provType := strings.SplitN(mapKey, "/", 2)[1]
		for identity, zipBytes := range prov.zips {
			sum := sha256.Sum256(zipBytes)
			reg.shas[provType+"|"+identity] = hex.EncodeToString(sum[:])

			// Multi-identity map keys use "|", but that character is not
			// valid in paths on Windows, so build the probe filename from
			// sanitized components only.
			probeName := fmt.Sprintf("h1-probe-%s-%s.zip", strings.ReplaceAll(provType, "/", "-"), strings.ReplaceAll(identity, "|", "_"))
			zipPath := filepath.Join(baseDir, probeName)
			if err := os.WriteFile(zipPath, zipBytes, 0644); err != nil {
				t.Fatal(err)
			}
			h, err := getproviders.PackageHashV1(getproviders.PackageLocalArchive(zipPath))
			if err != nil {
				t.Fatal(err)
			}
			reg.h1[provType+"|"+identity] = h.String()
		}
	}

	ts := httptest.NewServer(http.HandlerFunc(reg.handle))
	reg.serverURL = ts.URL
	reg.svcs = disco.New()
	reg.svcs.ForceHostServices(svchost.Hostname(mirrorTestHost), map[string]interface{}{
		"providers.v1": ts.URL + "/providers/v1/",
	})
	t.Cleanup(ts.Close)
	return reg
}

// h1For returns the true "h1" hash for the given provider type and
// "<version>|<platform>" identity.
func (r *mirrorReg) h1For(provType, identity string) string {
	return r.h1[provType+"|"+identity]
}

// zhFor returns the true "zh" hash for the given provider type and
// "<version>|<platform>" identity.
func (r *mirrorReg) zhFor(provType, identity string) string {
	return "zh:" + r.shas[provType+"|"+identity]
}

func (r *mirrorReg) notFound(resp http.ResponseWriter) {
	resp.WriteHeader(404)
	//nolint:errcheck // test helper: nothing useful to do when the client is gone
	resp.Write([]byte("not found"))
}

func (r *mirrorReg) handle(resp http.ResponseWriter, req *http.Request) {
	write := func(data []byte) {
		//nolint:errcheck // test helper: nothing useful to do when the client is gone
		resp.Write(data)
	}
	path := req.URL.EscapedPath()
	switch {
	case strings.HasPrefix(path, "/providers/v1/"):
		parts := strings.Split(strings.TrimPrefix(path, "/providers/v1/"), "/")
		switch {
		case len(parts) == 3 && parts[2] == "versions":
			prov := r.providers[parts[0]+"/"+parts[1]]
			if prov == nil {
				r.notFound(resp)
				return
			}
			type versionMeta struct {
				Version   string   `json:"version"`
				Protocols []string `json:"protocols"`
			}
			versions := make([]versionMeta, 0, len(prov.versions))
			for _, v := range prov.versions {
				versions = append(versions, versionMeta{Version: v, Protocols: []string{"6.0"}})
			}
			encData, err := json.Marshal(map[string]interface{}{"versions": versions})
			if err != nil {
				resp.WriteHeader(500)
				return
			}
			resp.Header().Set("Content-Type", "application/json")
			write(encData)
		case len(parts) == 6 && parts[3] == "download":
			prov := r.providers[parts[0]+"/"+parts[1]]
			if prov == nil {
				r.notFound(resp)
				return
			}
			identity := parts[2] + "|" + parts[4] + "_" + parts[5]
			zipBytes, ok := prov.zips[identity]
			if !ok {
				r.notFound(resp)
				return
			}
			provType := parts[1]
			platformStr := parts[4] + "_" + parts[5]
			arch := parts[5]
			filename := fmt.Sprintf("terraform-provider-%s_%s_%s.zip", provType, parts[2], platformStr)
			body := map[string]interface{}{
				"protocols":             []string{"6.0"},
				"os":                    parts[4],
				"arch":                  arch,
				"filename":              filename,
				"shasum":                r.shas[provType+"|"+identity],
				"download_url":          fmt.Sprintf("%s/pkg/%s/%s/terraform-provider-%s_%s_%s.zip", r.serverURL, parts[0], provType, provType, parts[2], platformStr),
				"shasums_url":           fmt.Sprintf("%s/pkg/%s/%s/%s_SHA256SUMS", r.serverURL, parts[0], provType, parts[2]),
				"shasums_signature_url": fmt.Sprintf("%s/pkg/%s/%s/%s_SHA256SUMS.sig", r.serverURL, parts[0], provType, parts[2]),
				"signing_keys": map[string]interface{}{
					"gpg_public_keys": []interface{}{
						map[string]interface{}{"ascii_armor": r.armor},
					},
				},
			}
			if mode := prov.metadata[parts[2]]; mode != "" {
				hashes := []string{r.zhFor(provType, identity)}
				switch mode {
				case "full":
					hashes = append(hashes, r.h1For(provType, identity))
				case "extra":
					hashes = append(hashes, r.h1For(provType, identity), "h1:zzz-extra-bogus-reported-hash")
				case "zh":
					// zh only
				}
				body["packages"] = map[string]interface{}{
					platformStr: map[string]interface{}{
						"package_size": len(zipBytes),
						"hashes":       hashes,
					},
				}
			}
			encData, err := json.Marshal(body)
			if err != nil {
				resp.WriteHeader(500)
				return
			}
			resp.Header().Set("Content-Type", "application/json")
			write(encData)
		default:
			r.notFound(resp)
		}
	case strings.HasPrefix(path, "/pkg/"):
		parts := strings.Split(strings.TrimPrefix(path, "/pkg/"), "/")
		if len(parts) != 3 {
			r.notFound(resp)
			return
		}
		prov := r.providers[parts[0]+"/"+parts[1]]
		provType := parts[1]
		file := parts[2]
		switch {
		case strings.HasSuffix(file, ".zip"):
			// "<providerType>_<version>_<platform>.zip"
			fields := strings.SplitN(strings.TrimSuffix(file, ".zip"), "_", 3)
			if len(fields) != 3 {
				r.notFound(resp)
				return
			}
			identity := fields[1] + "|" + fields[2]
			if prov == nil {
				r.notFound(resp)
				return
			}
			zipBytes, ok := prov.zips[identity]
			if !ok {
				r.notFound(resp)
				return
			}
			if prov.downloadFails[identity] {
				resp.WriteHeader(500)
				write([]byte("simulated download failure"))
				return
			}
			if prov.authFails[identity] {
				write([]byte("corrupted archive contents"))
				return
			}
			write(zipBytes)
		case strings.HasSuffix(file, "_SHA256SUMS.sig"):
			version := strings.TrimSuffix(file, "_SHA256SUMS.sig")
			doc, ok := r.sumsDocument(provType, prov, version)
			if prov == nil || !ok {
				r.notFound(resp)
				return
			}
			write(r.sumsSignature(provType, version, doc))
		case strings.HasSuffix(file, "_SHA256SUMS"):
			version := strings.TrimSuffix(file, "_SHA256SUMS")
			doc, ok := r.sumsDocument(provType, prov, version)
			if prov == nil || !ok {
				r.notFound(resp)
				return
			}
			write(doc)
		default:
			r.notFound(resp)
		}
	default:
		r.notFound(resp)
	}
}

// sumsDocument builds and caches the SHA256SUMS document for given provider
// version: one "<sha>  <filename>" line per platform archive of the version.
func (r *mirrorReg) sumsDocument(provType string, prov *mirrorProv, version string) ([]byte, bool) {
	if prov == nil {
		return nil, false
	}
	identities := make([]string, 0, len(prov.zips))
	for identity := range prov.zips {
		if strings.HasPrefix(identity, version+"|") {
			identities = append(identities, strings.TrimPrefix(identity, version+"|"))
		}
	}
	sort.Strings(identities)
	if len(identities) == 0 {
		return nil, false
	}
	var buf bytes.Buffer
	for _, platform := range identities {
		sumHex := r.shas[provType+"|"+version+"|"+platform]
		filename := fmt.Sprintf("terraform-provider-%s_%s_%s.zip", provType, version, platform)
		fmt.Fprintf(&buf, "%s  %s\n", sumHex, filename)
	}
	return buf.Bytes(), true
}

// sumsSignature signs the given sums document with the fake registry entity.
func (r *mirrorReg) sumsSignature(provType, version string, doc []byte) []byte {
	cacheKey := provType + "|" + version
	if sig, ok := r.sigCache[cacheKey]; ok {
		return sig
	}
	var buf bytes.Buffer
	if err := openpgp.DetachSign(&buf, r.signer, bytes.NewReader(doc), nil); err != nil {
		r.t.Fatalf("failed to sign sums document for testing: %s", err)
	}
	r.sigCache[cacheKey] = buf.Bytes()
	return buf.Bytes()
}

// mirrorTestZip builds a deterministic in-memory zip archive with the given
// file contents.
func mirrorTestZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range names {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(files[name])); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// mirrorIndexArchive mirrors the JSON shape of one platform entry in a
// generated version index file.
type mirrorIndexArchive struct {
	URL    string   `json:"url"`
	Hashes []string `json:"hashes"`
}

// mirrorVersionIndex mirrors the JSON shape of a generated version index file.
type mirrorVersionIndex struct {
	Archives map[string]mirrorIndexArchive `json:"archives"`
}

// mirrorMainIndex mirrors the JSON shape of a generated index.json file.
type mirrorMainIndex struct {
	Versions map[string]map[string]interface{} `json:"versions"`
}

func readMirrorVersionIndexForTest(t *testing.T, path string) *mirrorVersionIndex {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read version index %s: %s", path, err)
	}
	var idx mirrorVersionIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		t.Fatalf("failed to parse version index %s: %s", path, err)
	}
	return &idx
}

func readMirrorMainIndexForTest(t *testing.T, path string) *mirrorMainIndex {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read main index %s: %s", path, err)
	}
	var idx mirrorMainIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		t.Fatalf("failed to parse main index %s: %s", path, err)
	}
	return &idx
}

// mirrorProviderDir returns the directory for a provider within a mirror.
func mirrorProviderDir(outputDir, provType string) string {
	return filepath.Join(outputDir, mirrorTestHost, "test", provType)
}

// writeMirrorConfig writes a root configuration requiring the given
// provider types from the default provider registry host.
func writeMirrorConfig(t *testing.T, provTypes ...string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("terraform {\n  required_providers {\n")
	for _, provType := range provTypes {
		fmt.Fprintf(&b, "    %s = {\n      source = \"test/%s\"\n    }\n", provType, provType)
	}
	b.WriteString("  }\n}\n")
	if err := os.WriteFile("mirror-config.tf", []byte(b.String()), 0644); err != nil {
		t.Fatal(err)
	}
}

// runMirrorCommand runs the providers mirror command in the test's working
// directory against the mirror output directory, with the fake registry
// services configured on the command.
func runMirrorCommand(t *testing.T, reg *mirrorReg, outputDir string, platforms ...string) (int, *cli.MockUi) {
	t.Helper()
	ui := new(cli.MockUi)
	c := &ProvidersMirrorCommand{
		Meta: Meta{
			WorkingDir: workdir.NewDir("."),
			Ui:         ui,
			Services:   reg.svcs,
		},
	}
	args := make([]string, 0, len(platforms)+1)
	for _, platform := range platforms {
		args = append(args, "-platform="+platform)
	}
	args = append(args, outputDir)
	return c.Run(args), ui
}

// snapshotMirrorDir returns a map of file names (relative to the mirror
// directory) to their contents, for byte-level comparisons. Dot-prefixed
// staging files, which the mirror command may leave behind after a failed
// download or authentication, are excluded: they are never indexed, so they
// do not affect the preserved mirror state.
func snapshotMirrorDir(t *testing.T, mirrorDir string) map[string][]byte {
	t.Helper()
	snapshot := map[string][]byte{}
	err := filepath.Walk(mirrorDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(mirrorDir, path)
		if err != nil {
			return err
		}
		if strings.HasPrefix(info.Name(), ".") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		snapshot[rel] = data
		return nil
	})
	if err != nil {
		t.Fatalf("failed to snapshot mirror directory: %s", err)
	}
	return snapshot
}

func assertSameArchives(t *testing.T, got, want map[string][]byte) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("wrong number of files in mirror directory snapshot; want %d, got %d\nbefore: %v\nafter: %v", len(want), len(got), want, got)
	}
	for name, wantBytes := range want {
		gotBytes, ok := got[name]
		if !ok {
			t.Fatalf("file %q disappeared from the mirror directory", name)
		}
		if !bytes.Equal(wantBytes, gotBytes) {
			t.Fatalf("file %q in the mirror directory changed unexpectedly:\nbefore:\n%s\nafter:\n%s", name, wantBytes, gotBytes)
		}
	}
}

func assertMirrorHashes(t *testing.T, idx mirrorIndexArchive, want []string) {
	t.Helper()
	if !equalStrings(idx.Hashes, want) {
		t.Fatalf("wrong hashes for platform archive %q:\ngot:  %v\nwant: %v", idx.URL, idx.Hashes, want)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// prov2 mirror addresses for the tests
func mirrorArchPath(outputDir, provType, version, platform string) string {
	return filepath.Join(
		mirrorProviderDir(outputDir, provType),
		fmt.Sprintf("terraform-provider-%s_%s_%s.zip", provType, version, platform),
	)
}

const mirrorTestVersion = "2.0.0"
const mirrorTestOldVersion = "1.0.0"

// TestProvidersMirrorSharedHashAcrossPlatforms mirrors a release whose
// archives are byte-identical for two platforms, so their content hashes
// (h1) and zip hashes (zh) are shared. Both platform entries must include
// the shared hashes, and unrelated hashes must not appear on either.
func TestProvidersMirrorSharedHashAcrossPlatforms(t *testing.T) {
	// no t.Parallel: this test uses t.Chdir
	t.Chdir(tempWorkingDir(t).RootModuleDir())
	writeMirrorConfig(t, "mirrorprov")
	sharedZip := mirrorTestZip(t, map[string]string{
		"terraform-provider-mirrorprov": "shared provider binary",
	})
	reg := startMirrorRegistry(t, map[string]*mirrorProv{
		"test/mirrorprov": {
			versions: []string{mirrorTestVersion},
			zips: map[string][]byte{
				mirrorTestVersion + "|" + mirrorTestPlatformA: sharedZip,
				mirrorTestVersion + "|" + mirrorTestPlatformB: sharedZip,
			},
			metadata: map[string]string{mirrorTestVersion: "full"},
		},
	})
	outputDir := filepath.Join(t.TempDir(), "mirror")
	code, ui := runMirrorCommand(t, reg, outputDir, mirrorTestPlatformA, mirrorTestPlatformB)
	if code != 0 {
		t.Fatalf("expected success, got exit code %d:\n%s", code, ui.ErrorWriter.String())
	}

	indexPath := filepath.Join(mirrorProviderDir(outputDir, "mirrorprov"), mirrorTestVersion+".json")
	idx := readMirrorVersionIndexForTest(t, indexPath)

	sharedH1 := reg.h1For("mirrorprov", mirrorTestVersion+"|"+mirrorTestPlatformA)
	sharedZh := reg.zhFor("mirrorprov", mirrorTestVersion+"|"+mirrorTestPlatformA)
	wantHashes := []string{sharedH1, sharedZh}
	for _, platform := range []string{mirrorTestPlatformA, mirrorTestPlatformB} {
		archive, ok := idx.Archives[platform]
		if !ok {
			t.Fatalf("platform %s missing from version index:\n%s", platform, indexPath)
		}
		assertMirrorHashes(t, archive, wantHashes)
		wantURL := fmt.Sprintf("terraform-provider-mirrorprov_%s_%s.zip", mirrorTestVersion, platform)
		if archive.URL != wantURL {
			t.Errorf("wrong archive URL for platform %s; want %q, got %q", platform, wantURL, archive.URL)
		}
	}
}

// TestProvidersMirrorVersionPlatformIsolation ensures that:
//   - two platforms of the same version each get their own hashes (no
//     cross-platform contamination), and
//   - a pre-seeded older version keeps its own inherited prior hashes and
//     never receives the newer version's hashes(no cross-version
//     contamination).
func TestProvidersMirrorVersionPlatformIsolation(t *testing.T) {
	// no t.Parallel: this test uses t.Chdir
	t.Chdir(tempWorkingDir(t).RootModuleDir())
	writeMirrorConfig(t, "mirrorprov")
	reg := startMirrorRegistry(t, map[string]*mirrorProv{
		"test/mirrorprov": {
			versions: []string{mirrorTestOldVersion, mirrorTestVersion},
			zips: map[string][]byte{
				mirrorTestOldVersion + "|" + mirrorTestPlatformA: mirrorTestZip(t, map[string]string{"prov": "old archive"}),
				mirrorTestVersion + "|" + mirrorTestPlatformA:    mirrorTestZip(t, map[string]string{"prov": "new archive for linux"}),
				mirrorTestVersion + "|" + mirrorTestPlatformB:    mirrorTestZip(t, map[string]string{"prov": "new archive for darwin"}),
			},
			metadata: map[string]string{mirrorTestVersion: "full"},
		},
	})

	outputDir := filepath.Join(t.TempDir(), "mirror")
	// Pre-seed an old version with a hand-authored prior index that
	// includes an extra hash which was previously admitted by a mirror run.
	oldArchiveRel := fmt.Sprintf("terraform-provider-mirrorprov_%s_%s.zip", mirrorTestOldVersion, mirrorTestPlatformA)
	if err := os.MkdirAll(mirrorProviderDir(outputDir, "mirrorprov"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mirrorArchPath(outputDir, "mirrorprov", mirrorTestOldVersion, mirrorTestPlatformA), reg.providers["test/mirrorprov"].zips[mirrorTestOldVersion+"|"+mirrorTestPlatformA], 0644); err != nil {
		t.Fatal(err)
	}
	oldIndexData, err := json.MarshalIndent(map[string]interface{}{
		"archives": map[string]interface{}{
			mirrorTestPlatformA: map[string]interface{}{
				"url": oldArchiveRel,
				// sorted order: h1 first, then the fabricated zh
				"hashes": []string{reg.h1For("mirrorprov", mirrorTestOldVersion+"|"+mirrorTestPlatformA), "zh:" + strings.Repeat("f", 64)},
			},
		},
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mirrorProviderDir(outputDir, "mirrorprov"), mirrorTestOldVersion+".json"), oldIndexData, 0644); err != nil {
		t.Fatal(err)
	}

	code, ui := runMirrorCommand(t, reg, outputDir, mirrorTestPlatformA, mirrorTestPlatformB)
	if code != 0 {
		t.Fatalf("expected success, got exit code %d:\n%s", code, ui.ErrorWriter.String())
	}

	provDir := mirrorProviderDir(outputDir, "mirrorprov")

	// The old version keeps its inherited prior hashes and gets nothing
	// from the newer release.
	oldIdx := readMirrorVersionIndexForTest(t, filepath.Join(provDir, mirrorTestOldVersion+".json"))
	linuxEntry, ok := oldIdx.Archives[mirrorTestPlatformA]
	if !ok {
		t.Fatalf("old version's %s entry missing", mirrorTestPlatformA)
	}
	wantOld := []string{
		reg.h1For("mirrorprov", mirrorTestOldVersion+"|"+mirrorTestPlatformA),
		"zh:" + strings.Repeat("f", 64),
	}
	assertMirrorHashes(t, linuxEntry, wantOld)
	if linuxEntry.URL != oldArchiveRel {
		t.Errorf("old archive URL changed: %q", linuxEntry.URL)
	}
	newH1 := reg.h1For("mirrorprov", mirrorTestVersion+"|"+mirrorTestPlatformA)
	newZh := reg.zhFor("mirrorprov", mirrorTestVersion+"|"+mirrorTestPlatformA)
	for _, hash := range linuxEntry.Hashes {
		if hash == newH1 || hash == newZh {
			t.Fatalf("new version's hash %s leaked into the old version's index", hash)
		}
	}

	// The new version's entries contain only their own hashes.
	newIdx := readMirrorVersionIndexForTest(t, filepath.Join(provDir, mirrorTestVersion+".json"))
	assertMirrorHashes(t, newIdx.Archives[mirrorTestPlatformA], []string{newH1, newZh})
	assertMirrorHashes(t, newIdx.Archives[mirrorTestPlatformB], []string{
		reg.h1For("mirrorprov", mirrorTestVersion+"|"+mirrorTestPlatformB),
		reg.zhFor("mirrorprov", mirrorTestVersion+"|"+mirrorTestPlatformB),
	})
}

// TestProvidersMirrorMetadataVariants covers the different #3868 packages
// hash metadata shapes: full, zh-only, absent, and an extra reported h1.
func TestProvidersMirrorMetadataVariants(t *testing.T) {
	// no t.Parallel: this test uses t.Chdir
	tests := map[string]struct {
		mode string
	}{
		"full metadata":     {mode: "full"},
		"zh only metadata":  {mode: "zh"},
		"no metadata":       {mode: ""},
		"extra reported h1": {mode: "extra"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			// no t.Parallel: this test uses t.Chdir
			t.Chdir(tempWorkingDir(t).RootModuleDir())
			writeMirrorConfig(t, "mirrorprov")
			prov := "mirrorprov"
			reg := startMirrorRegistry(t, map[string]*mirrorProv{
				"test/mirrorprov": {
					versions: []string{mirrorTestVersion},
					zips: map[string][]byte{
						mirrorTestVersion + "|" + mirrorTestPlatformA: mirrorTestZip(t, map[string]string{"prov": name}),
					},
					metadata: map[string]string{mirrorTestVersion: test.mode},
				},
			})
			outputDir := filepath.Join(t.TempDir(), "mirror")
			code, ui := runMirrorCommand(t, reg, outputDir, mirrorTestPlatformA)
			if code != 0 {
				t.Fatalf("expected success, got exit code %d:\n%s", code, ui.ErrorWriter.String())
			}
			idx := readMirrorVersionIndexForTest(t, filepath.Join(mirrorProviderDir(outputDir, prov), mirrorTestVersion+".json"))
			entry, ok := idx.Archives[mirrorTestPlatformA]
			if !ok {
				t.Fatalf("%s entry missing from index", mirrorTestPlatformA)
			}
			h1 := reg.h1For(prov, mirrorTestVersion+"|"+mirrorTestPlatformA)
			zh := reg.zhFor(prov, mirrorTestVersion+"|"+mirrorTestPlatformA)
			switch test.mode {
			case "full":
				assertMirrorHashes(t, entry, []string{h1, zh})
			case "zh", "":
				// Without h1 being reported, the archive's own locally
				// verified zip hash is still trusted.
				assertMirrorHashes(t, entry, []string{h1, zh})
			case "extra":
				want := []string{h1, "h1:zzz-extra-bogus-reported-hash", zh}
				assertMirrorHashes(t, entry, want)
			}
		})
	}
}

// TestProvidersMirrorPartialSuccessAuthFailure installs two providers, then
// makes authentication fail for one of them on a second run that also
// installs a new successful identity (a sibling platform of the healthy
// provider). The new identity's index entry must be written even though the
// run fails, while the failed provider's archive and its existing [h1, zh]
// index stay untouched, and the command exits nonzero. Installing the new
// identity in the failing run ensures this test cannot pass with a no-op
// indexing step.
func TestProvidersMirrorPartialSuccessAuthFailure(t *testing.T) {
	// no t.Parallel: this test uses t.Chdir
	t.Chdir(tempWorkingDir(t).RootModuleDir())
	writeMirrorConfig(t, "goodprov", "badprov")
	badprov := &mirrorProv{
		versions: []string{mirrorTestVersion},
		zips: map[string][]byte{
			mirrorTestVersion + "|" + mirrorTestPlatformA: mirrorTestZip(t, map[string]string{"prov": "badprov archive"}),
		},
		authFails: map[string]bool{},
		metadata:  map[string]string{mirrorTestVersion: "full"},
	}
	reg := startMirrorRegistry(t, map[string]*mirrorProv{
		"test/goodprov": {
			versions: []string{mirrorTestVersion},
			zips: map[string][]byte{
				mirrorTestVersion + "|" + mirrorTestPlatformA: mirrorTestZip(t, map[string]string{"prov": "goodprov archive"}),
				// A sibling platform that only the failing (second) run
				// will request, so its index entry can only appear if
				// actually written during that run.
				mirrorTestVersion + "|" + mirrorTestPlatformB: mirrorTestZip(t, map[string]string{"prov": "goodprov darwin archive"}),
			},
			metadata: map[string]string{mirrorTestVersion: "full"},
		},
		"test/badprov": badprov,
	})

	outputDir := filepath.Join(t.TempDir(), "mirror")
	code, ui := runMirrorCommand(t, reg, outputDir, mirrorTestPlatformA)
	if code != 0 {
		t.Fatalf("first run should succeed:\n%s", ui.ErrorWriter.String())
	}
	badprovBefore := snapshotMirrorDir(t, mirrorProviderDir(outputDir, "badprov"))

	// Confirm the pre-existing index is the expected [h1, zh] pair.
	badIdx := readMirrorVersionIndexForTest(t, filepath.Join(mirrorProviderDir(outputDir, "badprov"), mirrorTestVersion+".json"))
	assertMirrorHashes(t, badIdx.Archives[mirrorTestPlatformA], []string{
		reg.h1For("badprov", mirrorTestVersion+"|"+mirrorTestPlatformA),
		reg.zhFor("badprov", mirrorTestVersion+"|"+mirrorTestPlatformA),
	})

	// Now make authentication fail on the second mirror run, which also
	// mirrors the new sibling platform for goodprov.
	badprov.authFails[mirrorTestVersion+"|"+mirrorTestPlatformA] = true
	code, ui = runMirrorCommand(t, reg, outputDir, mirrorTestPlatformA, mirrorTestPlatformB)
	if code != 1 {
		t.Fatalf("expected nonzero exit code on auth failure, got %d:\n%s", code, ui.OutputWriter.String())
	}
	if got := ui.ErrorWriter.String(); !strings.Contains(got, "Invalid provider package") {
		t.Fatalf("expected an authentication failure diagnostic, got:\n%s", got)
	}
	badprovAfter := snapshotMirrorDir(t, mirrorProviderDir(outputDir, "badprov"))
	assertSameArchives(t, badprovAfter, badprovBefore)

	// The failed identity's prior index entry is still exactly [h1, zh].
	badprovAfterIdx := readMirrorVersionIndexForTest(t, filepath.Join(mirrorProviderDir(outputDir, "badprov"), mirrorTestVersion+".json"))
	assertMirrorHashes(t, badprovAfterIdx.Archives[mirrorTestPlatformA], []string{
		reg.h1For("badprov", mirrorTestVersion+"|"+mirrorTestPlatformA),
		reg.zhFor("badprov", mirrorTestVersion+"|"+mirrorTestPlatformA),
	})

	// The healthy provider's run-1 identity is still indexed...
	goodIdx := readMirrorVersionIndexForTest(t, filepath.Join(mirrorProviderDir(outputDir, "goodprov"), mirrorTestVersion+".json"))
	assertMirrorHashes(t, goodIdx.Archives[mirrorTestPlatformA], []string{
		reg.h1For("goodprov", mirrorTestVersion+"|"+mirrorTestPlatformA),
		reg.zhFor("goodprov", mirrorTestVersion+"|"+mirrorTestPlatformA),
	})
	// ...and the new successful identity from this failing run got its
	// own index entry, proving partial success still indexes new work.
	assertMirrorHashes(t, goodIdx.Archives[mirrorTestPlatformB], []string{
		reg.h1For("goodprov", mirrorTestVersion+"|"+mirrorTestPlatformB),
		reg.zhFor("goodprov", mirrorTestVersion+"|"+mirrorTestPlatformB),
	})
	if _, exists := goodIdx.Archives[mirrorTestPlatformB]; !exists {
		t.Fatalf("the new successful identity of the failing run was not indexed")
	}
}

// TestProvidersMirrorDownloadFailure covers a download error for an identity
// with an existing archive and a valid prior [h1, zh] index: archive and
// index bytes are preserved and the command exits nonzero.
func TestProvidersMirrorDownloadFailure(t *testing.T) {
	// no t.Parallel: this test uses t.Chdir
	t.Chdir(tempWorkingDir(t).RootModuleDir())
	writeMirrorConfig(t, "mirrorprov")
	prov := &mirrorProv{
		versions: []string{mirrorTestVersion},
		zips: map[string][]byte{
			mirrorTestVersion + "|" + mirrorTestPlatformA: mirrorTestZip(t, map[string]string{"prov": "mirrorprov archive"}),
		},
		downloadFails: map[string]bool{},
		metadata:      map[string]string{mirrorTestVersion: "full"},
	}
	reg := startMirrorRegistry(t, map[string]*mirrorProv{"test/mirrorprov": prov})

	outputDir := filepath.Join(t.TempDir(), "mirror")
	code, ui := runMirrorCommand(t, reg, outputDir, mirrorTestPlatformA)
	if code != 0 {
		t.Fatalf("first run should succeed:\n%s", ui.ErrorWriter.String())
	}
	before := snapshotMirrorDir(t, mirrorProviderDir(outputDir, "mirrorprov"))

	prov.downloadFails[mirrorTestVersion+"|"+mirrorTestPlatformA] = true
	code, ui = runMirrorCommand(t, reg, outputDir, mirrorTestPlatformA)
	if code != 1 {
		t.Fatalf("expected nonzero exit code on download failure, got %d:\n%s", code, ui.OutputWriter.String())
	}
	if got := ui.ErrorWriter.String(); !strings.Contains(got, "Cannot download provider release") {
		t.Fatalf("expected a download failure diagnostic, got:\n%s", got)
	}
	after := snapshotMirrorDir(t, mirrorProviderDir(outputDir, "mirrorprov"))
	assertSameArchives(t, after, before)
}

// TestProvidersMirrorInvalidPriorIndex covers a prior version index and a
// prior main index that cannot be parsed: the run must fail, leave the
// affected file untouched, and not advertise the affected version.
func TestProvidersMirrorInvalidPriorIndex(t *testing.T) {
	// no t.Parallel: this test uses t.Chdir
	t.Run("invalid version index", func(t *testing.T) {
		// no t.Parallel: this test uses t.Chdir
		t.Chdir(tempWorkingDir(t).RootModuleDir())
		writeMirrorConfig(t, "mirrorprov")
		reg := startMirrorRegistry(t, map[string]*mirrorProv{
			"test/mirrorprov": {
				versions: []string{mirrorTestVersion},
				zips: map[string][]byte{
					mirrorTestVersion + "|" + mirrorTestPlatformA: mirrorTestZip(t, map[string]string{"prov": "mirrorprov archive"}),
				},
				metadata: map[string]string{mirrorTestVersion: "full"},
			},
		})
		outputDir := filepath.Join(t.TempDir(), "mirror")
		code, ui := runMirrorCommand(t, reg, outputDir, mirrorTestPlatformA)
		if code != 0 {
			t.Fatalf("first run should succeed:\n%s", ui.ErrorWriter.String())
		}
		versionIndexPath := filepath.Join(mirrorProviderDir(outputDir, "mirrorprov"), mirrorTestVersion+".json")
		invalidBytes := []byte(`{"archives": {`)
		origIndexData, err := os.ReadFile(mainIndexFile(outputDir, "mirrorprov"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(versionIndexPath, invalidBytes, 0644); err != nil {
			t.Fatal(err)
		}

		code, ui = runMirrorCommand(t, reg, outputDir, mirrorTestPlatformA)
		if code != 1 {
			t.Fatalf("expected nonzero exit code for invalid prior index, got %d:\n%s", code, ui.OutputWriter.String())
		}
		if got := ui.ErrorWriter.String(); !strings.Contains(got, "Invalid existing version index") {
			t.Fatalf("expected an invalid prior index diagnostic, got:\n%s", got)
		}
		gotIndexData, err := os.ReadFile(versionIndexPath)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(gotIndexData, invalidBytes) {
			t.Fatalf("invalid prior version index was overwritten:\n%s", gotIndexData)
		}
		// The invalid version must not be advertised in the main index.
		main := readMirrorMainIndexForTest(t, filepath.Join(mirrorProviderDir(outputDir, "mirrorprov"), "index.json"))
		if _, exists := main.Versions[mirrorTestVersion]; exists {
			t.Fatalf("the version with an invalid index is still advertised: %s", origIndexData)
		}
	})

	t.Run("version index with invalid prior hashes", func(t *testing.T) {
		// no t.Parallel: this test uses t.Chdir
		t.Chdir(tempWorkingDir(t).RootModuleDir())
		writeMirrorConfig(t, "mirrorprov")
		reg := startMirrorRegistry(t, map[string]*mirrorProv{
			"test/mirrorprov": {
				versions: []string{mirrorTestVersion},
				zips: map[string][]byte{
					mirrorTestVersion + "|" + mirrorTestPlatformA: mirrorTestZip(t, map[string]string{"prov": "mirrorprov archive"}),
				},
				metadata: map[string]string{mirrorTestVersion: "full"},
			},
		})
		outputDir := filepath.Join(t.TempDir(), "mirror")
		code, ui := runMirrorCommand(t, reg, outputDir, mirrorTestPlatformA)
		if code != 0 {
			t.Fatalf("first run should succeed:\n%s", ui.ErrorWriter.String())
		}
		versionIndexPath := filepath.Join(mirrorProviderDir(outputDir, "mirrorprov"), mirrorTestVersion+".json")

		// Valid JSON, but one of the prior hashes is not a valid hash
		// string at all. The entry keeps the correct archive URL, so the
		// hash syntax check is the only reason to reject the prior index.
		invalidHashBytes := []byte(fmt.Sprintf(
			`{"archives":{%q:{"hashes":["not-a-hash"],"url":"terraform-provider-mirrorprov_%s_%s.zip"}}}`,
			mirrorTestPlatformA, mirrorTestVersion, mirrorTestPlatformA,
		))
		badBytes := make([]byte, len(invalidHashBytes))
		copy(badBytes, invalidHashBytes)
		if err := os.WriteFile(versionIndexPath, badBytes, 0644); err != nil {
			t.Fatal(err)
		}

		code, ui = runMirrorCommand(t, reg, outputDir, mirrorTestPlatformA)
		if code != 1 {
			t.Fatalf("expected nonzero exit code for a prior index with an invalid hash, got %d:\n%s", code, ui.OutputWriter.String())
		}
		if got := ui.ErrorWriter.String(); !strings.Contains(got, "Invalid existing version index") {
			t.Fatalf("expected an invalid prior index diagnostic, got:\n%s", got)
		}
		gotIndexData, err := os.ReadFile(versionIndexPath)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(gotIndexData, invalidHashBytes) {
			t.Fatalf("prior version index with an invalid hash was overwritten:\n%s", gotIndexData)
		}
		// The invalid version must not be advertised in the main index.
		main := readMirrorMainIndexForTest(t, filepath.Join(mirrorProviderDir(outputDir, "mirrorprov"), "index.json"))
		if _, exists := main.Versions[mirrorTestVersion]; exists {
			t.Fatalf("the version with an invalid prior hash is still advertised: %s", main)
		}
	})

	t.Run("invalid main index", func(t *testing.T) {
		// no t.Parallel: this test uses t.Chdir
		t.Chdir(tempWorkingDir(t).RootModuleDir())
		writeMirrorConfig(t, "mirrorprov")
		reg := startMirrorRegistry(t, map[string]*mirrorProv{
			"test/mirrorprov": {
				versions: []string{mirrorTestVersion},
				zips: map[string][]byte{
					mirrorTestVersion + "|" + mirrorTestPlatformA: mirrorTestZip(t, map[string]string{"prov": "mirrorprov archive"}),
				},
				metadata: map[string]string{mirrorTestVersion: "full"},
			},
		})
		outputDir := filepath.Join(t.TempDir(), "mirror")
		code, ui := runMirrorCommand(t, reg, outputDir, mirrorTestPlatformA)
		if code != 0 {
			t.Fatalf("first run should succeed:\n%s", ui.ErrorWriter.String())
		}
		mainIndexPath := filepath.Join(mirrorProviderDir(outputDir, "mirrorprov"), "index.json")
		invalidBytes := []byte(`{"versions": [`)
		if err := os.WriteFile(mainIndexPath, invalidBytes, 0644); err != nil {
			t.Fatal(err)
		}
		versionIndexPath := filepath.Join(mirrorProviderDir(outputDir, "mirrorprov"), mirrorTestVersion+".json")
		beforeVersion, err := os.ReadFile(versionIndexPath)
		if err != nil {
			t.Fatal(err)
		}

		code, ui = runMirrorCommand(t, reg, outputDir, mirrorTestPlatformA)
		if code != 1 {
			t.Fatalf("expected nonzero exit code for invalid prior main index, got %d:\n%s", code, ui.OutputWriter.String())
		}
		if got := ui.ErrorWriter.String(); !strings.Contains(got, "Invalid existing main index") {
			t.Fatalf("expected an invalid prior main index diagnostic, got:\n%s", got)
		}
		gotMainData, err := os.ReadFile(mainIndexPath)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(gotMainData, invalidBytes) {
			t.Fatalf("invalid prior main index was overwritten:\n%s", gotMainData)
		}
		// The valid version index is untouched because its bytes did not
		// change.
		afterVersion, err := os.ReadFile(versionIndexPath)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(beforeVersion, afterVersion) {
			t.Fatalf("valid version index changed unexpectedly:\n%s", afterVersion)
		}
	})
}

// TestProvidersMirrorURLMismatchPrior covers a prior index whose URL does
// not map to the local archive file: its hashes must not be inherited and a
// warning must be recorded.
func TestProvidersMirrorURLMismatchPrior(t *testing.T) {
	// no t.Parallel: this test uses t.Chdir
	t.Chdir(tempWorkingDir(t).RootModuleDir())
	writeMirrorConfig(t, "mirrorprov")
	reg := startMirrorRegistry(t, map[string]*mirrorProv{
		"test/mirrorprov": {
			versions: []string{mirrorTestVersion},
			zips: map[string][]byte{
				mirrorTestVersion + "|" + mirrorTestPlatformA: mirrorTestZip(t, map[string]string{"prov": "mirrorprov archive"}),
			},
			metadata: map[string]string{mirrorTestVersion: "full"},
		},
	})
	outputDir := filepath.Join(t.TempDir(), "mirror")
	provDir := mirrorProviderDir(outputDir, "mirrorprov")
	if err := os.MkdirAll(provDir, 0755); err != nil {
		t.Fatal(err)
	}
	// A prior index claiming hashes from a different archive file.
	priorData, err := json.MarshalIndent(map[string]interface{}{
		"archives": map[string]interface{}{
			mirrorTestPlatformA: map[string]interface{}{
				"url":    fmt.Sprintf("terraform-provider-mirrorprov_9.9.9_%s.zip", mirrorTestPlatformB),
				"hashes": []string{"zh:" + strings.Repeat("a", 64)},
			},
		},
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(provDir, mirrorTestVersion+".json"), priorData, 0644); err != nil {
		t.Fatal(err)
	}

	code, ui := runMirrorCommand(t, reg, outputDir, mirrorTestPlatformA)
	if code != 0 {
		t.Fatalf("expected success despite URL mismatch, got exit code %d:\n%s", code, ui.ErrorWriter.String())
	}
	if got := ui.ErrorWriter.String(); !strings.Contains(got, "Ignored existing index hashes") {
		t.Fatalf("expected a URL mismatch warning, got:\n%s", got)
	}
	idx := readMirrorVersionIndexForTest(t, filepath.Join(provDir, mirrorTestVersion+".json"))
	entry, ok := idx.Archives[mirrorTestPlatformA]
	if !ok {
		t.Fatalf("%s entry missing", mirrorTestPlatformA)
	}
	assertMirrorHashes(t, entry, []string{
		reg.h1For("mirrorprov", mirrorTestVersion+"|"+mirrorTestPlatformA),
		reg.zhFor("mirrorprov", mirrorTestVersion+"|"+mirrorTestPlatformA),
	})
}

// TestProvidersMirrorDeterministicJSON runs the mirror twice and requires
// byte-identical output, plus (via read-only index files) that unchanged
// index files are not rewritten.
func TestProvidersMirrorDeterministicJSON(t *testing.T) {
	// no t.Parallel: this test uses t.Chdir
	t.Chdir(tempWorkingDir(t).RootModuleDir())
	writeMirrorConfig(t, "mirrorprov")
	reg := startMirrorRegistry(t, map[string]*mirrorProv{
		"test/mirrorprov": {
			versions: []string{mirrorTestVersion},
			zips: map[string][]byte{
				mirrorTestVersion + "|" + mirrorTestPlatformA: mirrorTestZip(t, map[string]string{"prov": "mirrorprov archive"}),
				mirrorTestVersion + "|" + mirrorTestPlatformB: mirrorTestZip(t, map[string]string{"prov": "other archive"}),
			},
			metadata: map[string]string{mirrorTestVersion: "full"},
		},
	})
	outputDir := filepath.Join(t.TempDir(), "mirror")
	code, ui := runMirrorCommand(t, reg, outputDir, mirrorTestPlatformA, mirrorTestPlatformB)
	if code != 0 {
		t.Fatalf("first run should succeed:\n%s", ui.ErrorWriter.String())
	}
	before := snapshotMirrorDir(t, outputDir)

	// Make the index files read-only: if the command tried to rewrite an
	// unchanged index file, the run would fail.
	provDir := mirrorProviderDir(outputDir, "mirrorprov")
	indexFiles := []string{"index.json", mirrorTestVersion + ".json"}
	for _, name := range indexFiles {
		path := filepath.Join(provDir, name)
		if err := os.Chmod(path, 0444); err != nil {
			t.Fatal(err)
		}
	}
	code, ui = runMirrorCommand(t, reg, outputDir, mirrorTestPlatformA, mirrorTestPlatformB)
	if code != 0 {
		t.Fatalf("second run should succeed without rewriting unchanged index files:\n%s", ui.ErrorWriter.String())
	}
	after := snapshotMirrorDir(t, outputDir)
	assertSameArchives(t, after, before)
}

// TestProvidersMirrorRenameErrorWithObstructedTarget exercises the
// rename-error branch of the mirror flow deterministically and portably:
// a nonempty directory is precreated at the intended output archive path,
// so the existing os.Remove(targetPath) fails (ENOTEMPTY), the failure is
// ignored as upstream does, and the following os.Rename then fails instead
// of replacing the target.
//
// Scope note: because the obstructed target is a directory (not the regular
// archive file the flow would normally replace), this test proves only
// that the rename-error branch produces a nonzero/error result, leaves the
// obstructed directory and its sentinel untouched, and records no trusted
// hashes or index entries for the failed platform/version. It does NOT
// prove whether a prior *regular* archive file would be deleted or
// preserved on a rename failure; that remove-before-rename behavior is a
// separate pre-existing property of the flow.
func TestProvidersMirrorRenameErrorWithObstructedTarget(t *testing.T) {
	// no t.Parallel: this test uses t.Chdir
	t.Chdir(tempWorkingDir(t).RootModuleDir())
	writeMirrorConfig(t, "mirrorprov")
	reg := startMirrorRegistry(t, map[string]*mirrorProv{
		"test/mirrorprov": {
			versions: []string{mirrorTestVersion},
			zips: map[string][]byte{
				mirrorTestVersion + "|" + mirrorTestPlatformA: mirrorTestZip(t, map[string]string{"prov": "mirrorprov archive"}),
			},
			metadata: map[string]string{mirrorTestVersion: "full"},
		},
	})
	outputDir := filepath.Join(t.TempDir(), "mirror")
	provDir := mirrorProviderDir(outputDir, "mirrorprov")
	targetPath := mirrorArchPath(outputDir, "mirrorprov", mirrorTestVersion, mirrorTestPlatformA)

	// Precreate a nonempty directory at the intended archive path. This is
	// not a valid mirror artifact, so SearchLocalDirectory ignores it, but
	// it deterministically blocks os.Remove and then os.Rename on both
	// POSIX and Windows.
	if err := os.MkdirAll(targetPath, 0755); err != nil {
		t.Fatal(err)
	}
	sentinelPath := filepath.Join(targetPath, "sentinel.txt")
	sentinelContent := []byte("sentinel")
	if err := os.WriteFile(sentinelPath, sentinelContent, 0644); err != nil {
		t.Fatal(err)
	}

	code, ui := runMirrorCommand(t, reg, outputDir, mirrorTestPlatformA)
	if code != 1 {
		t.Fatalf("expected nonzero exit code on rename failure, got %d:\n%s", code, ui.OutputWriter.String())
	}
	// The specific "Failed to place" wording proves the rename-error branch
	// was reached, and not an earlier download or authentication failure.
	if got := ui.ErrorWriter.String(); !strings.Contains(got, "Cannot download provider release") || !strings.Contains(got, "Failed to place") {
		t.Fatalf("expected a rename failure diagnostic, got:\n%s", got)
	}

	// The obstructed directory and its sentinel must be untouched.
	if err := os.Mkdir(targetPath, 0755); !os.IsExist(err) {
		t.Fatalf("the obstructed target directory is gone:\n%s", err)
	}
	gotSentinel, err := os.ReadFile(sentinelPath)
	if err != nil {
		t.Fatalf("sentinel file disappeared: %s", err)
	}
	if !bytes.Equal(gotSentinel, sentinelContent) {
		t.Fatalf("sentinel file content changed:\n%s", gotSentinel)
	}

	// No trusted hashes or index entries may be recorded for the failed
	// platform/version. The version index for 2.0.0 must not exist, so the
	// version is not advertised in the main index either.
	if _, err := os.Stat(filepath.Join(provDir, mirrorTestVersion+".json")); !os.IsNotExist(err) {
		t.Fatalf("a version index was written despite the rename failure: %s", err)
	}
	if _, err := os.Stat(filepath.Join(provDir, "index.json")); !os.IsNotExist(err) {
		t.Fatalf("a main index was written despite the rename failure: %s", err)
	}
}

func mainIndexFile(outputDir, provType string) string {
	return filepath.Join(mirrorProviderDir(outputDir, provType), "index.json")
}
