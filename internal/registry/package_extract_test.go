// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 Hashicorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package registry

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// These tests check that extractModulePackage never writes outside of the
// destination directory, even when the archive tries to escape using parent
// traversal, absolute entry names, or links pointing outside of the archive.

// escapeDirForTest creates an empty, uniquely-named directory to check that
// absolute-path entries don't write their contents there. The directory itself
// is removed again so that nothing can already be in it.
func escapeDirForTest(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ghoten-extract-escape")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

// assertNothingEscaped makes sure that nothing was written outside of
// targetDir: neither a traversal target next to it, nor the absolute escape
// path, nor any other file next to targetDir in targetDirParent.
func assertNothingEscaped(t *testing.T, targetDir string, escapePath string) {
	t.Helper()
	targetDirParent := filepath.Dir(targetDir)
	entries, err := os.ReadDir(targetDirParent)
	if err != nil {
		t.Fatal(err)
	}
	// targetDir itself is always local; for safety compare on full paths.
	for _, entry := range entries {
		full := filepath.Join(targetDirParent, entry.Name())
		if full == targetDir {
			continue
		}
		t.Errorf("unexpected file outside of the extraction destination: %s", full)
	}
	if escapePath != "" {
		if _, err := os.Stat(escapePath); !os.IsNotExist(err) {
			t.Errorf("absolute-path entry wrote outside of the destination: %s", escapePath)
		}
	}
}

// runExtract case calls extractModulePackage with the archive bytes in a
// temporary file, returning any error, after also setting up assertions about
// the destination.
type adversarialCase struct {
	name string
	// make returns the raw archive bytes and expects during construction
	// to know the absolute path that an absolute-path entry should refer
	// to (empty for cases that don't need it).
	archive func(t *testing.T, escapePath string) []byte
	// wantError means extraction must fail (e.g. explictly-rejected
	// traversal entries).
	wantError bool
}

func TestExtractModulePackage_Adversarial(t *testing.T) {
	t.Parallel()

	cases := []adversarialCase{
		{
			name: "zip entry with parent traversal",
			archive: func(t *testing.T, _ string) []byte {
				return createTestZip(t, map[string]string{"../outside.txt": "# escape"})
			},
			wantError: true,
		},
		{
			name: "tar.gz entry with parent traversal",
			archive: func(t *testing.T, _ string) []byte {
				return createTestTarGz(t, map[string]string{"../outside.txt": "# escape"})
			},
			wantError: true,
		},
		{
			name: "zip entry with nested parent traversal",
			archive: func(t *testing.T, _ string) []byte {
				return createTestZip(t, map[string]string{"sub/../../outside.txt": "# escape"})
			},
			wantError: true,
		},
		{
			name: "tar.gz entry with nested parent traversal",
			archive: func(t *testing.T, _ string) []byte {
				return createTestTarGz(t, map[string]string{"sub/../../outside.txt": "# escape"})
			},
			wantError: true,
		},
		{
			name: "zip entry with absolute path",
			archive: func(t *testing.T, escapePath string) []byte {
				return createTestZip(t, map[string]string{escapePath: "# escape"})
			},
		},
		{
			name: "tar.gz entry with absolute path",
			archive: func(t *testing.T, escapePath string) []byte {
				return createTestTarGz(t, map[string]string{escapePath: "# escape"})
			},
		},
		{
			name: "zip entry with symlink outside",
			archive: func(t *testing.T, escapePath string) []byte {
				return createZipWithSymlink(t, "link", escapePath)
			},
		},
		{
			name: "tar.gz entry with symlink outside",
			archive: func(t *testing.T, escapePath string) []byte {
				return createTarGzWithSymlink(t, "link", escapePath)
			},
		},
		{
			name: "tar.gz entry with hardlink outside",
			archive: func(t *testing.T, escapePath string) []byte {
				return createTarGzWithHardlink(t, "link", escapePath)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			sandbox := t.TempDir()
			targetDir := filepath.Join(sandbox, "pkg")
			escapePath := filepath.Join(escapeDirForTest(t), "outside.txt")
			siblingSentinel := filepath.Join(sandbox, "outside.txt")

			archive := tc.archive(t, escapePath)

			f, err := os.CreateTemp("", "ghoten-modpkg-test-")
			if err != nil {
				t.Fatal(err)
			}
			// The extractor needs the file itself open; only the name is
			// handed to the go-getter decompressors.
			defer func() { _ = f.Close(); _ = os.Remove(f.Name()) }()
			if _, err := f.Write(archive); err != nil {
				t.Fatal(err)
			}
			err = extractModulePackage(f, targetDir)

			switch {
			case tc.wantError:
				if err == nil {
					t.Fatal("expected error for malicious archive entry")
				}
			case err != nil:
				t.Fatalf("unexpected error: %v", err)
			}

			// In no case may anything be written outside of targetDir.
			assertNothingEscaped(t, targetDir, escapePath)
			if _, err := os.Stat(siblingSentinel); !os.IsNotExist(err) {
				t.Errorf("parent-traversal entry wrote outside of the destination: %s", siblingSentinel)
			}
			if _, err := os.Stat(escapePath); !os.IsNotExist(err) {
				t.Errorf("link/absolute entry wrote outside of the destination: %s", escapePath)
			}
		})
	}
}

// createZipWithSymlink writes a zip archive containing a symlink entry whose
// content is the given link target, an adversarial trick to try to make the
// extractor follow paths outside of the archive.
func createZipWithSymlink(t *testing.T, name string, target string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	hdr := &zip.FileHeader{
		Name:   name,
		Method: zip.Store,
	}
	hdr.SetMode(fs.ModeSymlink)
	f, err := w.CreateHeader(hdr)
	if err != nil {
		t.Fatalf("failed to create symlink entry in zip: %v", err)
	}
	if _, err := f.Write([]byte(target)); err != nil {
		t.Fatalf("failed to write symlink target in zip: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("failed to close zip writer: %v", err)
	}
	return buf.Bytes()
}

// createTarGzWithSymlink writes a gzip-compressed tar archive containing a
// symlink entry.
func createTarGzWithSymlink(t *testing.T, name string, target string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	if err := tw.WriteHeader(&tar.Header{
		Name:     name,
		Typeflag: tar.TypeSymlink,
		Linkname: target,
		Mode:     0o777,
	}); err != nil {
		t.Fatalf("failed to write symlink header: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("failed to close tar writer: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("failed to close gzip writer: %v", err)
	}
	return buf.Bytes()
}

// createTarGzWithHardlink writes a gzip-compressed tar archive containing a
// hardlink entry referring to a path outside of the archive.
func createTarGzWithHardlink(t *testing.T, name string, target string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	if err := tw.WriteHeader(&tar.Header{
		Name:     name,
		Typeflag: tar.TypeLink,
		Linkname: target,
		Mode:     0o644,
	}); err != nil {
		t.Fatalf("failed to write hardlink header: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("failed to close tar writer: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("failed to close gzip writer: %v", err)
	}
	return buf.Bytes()
}

// TestExtractModulePackage_LinkToExistingFileOutside checks that an
// attacker-controlled archive cannot overwrite an existing file outside of
// the destination by first creating a link whose target is that file and
// then writing a regular file through the link path.
func TestExtractModulePackage_LinkToExistingFileOutside(t *testing.T) {
	t.Parallel()

	sandbox := t.TempDir()
	sentinelPath := filepath.Join(sandbox, "outside.txt")
	const sentinelContent = "# existing sentinel\n"
	if err := os.WriteFile(sentinelPath, []byte(sentinelContent), 0o644); err != nil {
		t.Fatal(err)
	}

	archives := []struct {
		name    string
		archive []byte
	}{
		{"zip", createZipWithSymlinkAndFile(t, "link", sentinelPath, "# attack")},
		{"tar.gz", createTarGzWithSymlinkAndFile(t, "link", sentinelPath, "# attack")},
	}
	for _, tc := range archives {
		t.Run(tc.name, func(t *testing.T) {
			f, err := os.CreateTemp("", "ghoten-modpkg-test-")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = f.Close(); _ = os.Remove(f.Name()) }()
			if _, err := f.Write(tc.archive); err != nil {
				t.Fatal(err)
			}

			targetDir := filepath.Join(sandbox, "pkg-"+tc.name)
			if err := extractModulePackage(f, targetDir); err != nil {
				t.Fatalf("unexpected error extracting archive: %v", err)
			}

			// The sentinel file must be unchanged.
			if got, err := os.ReadFile(sentinelPath); err != nil {
				t.Errorf("failed to read the sentinel file: %v", err)
			} else if string(got) != sentinelContent {
				t.Errorf("a file write through the link path overwrote the sentinel file: got %q, want %q", got, sentinelContent)
			}

			// The extracted link path must be a regular file (or absent),
			// never a symlink.
			fi, err := os.Lstat(filepath.Join(targetDir, "link"))
			if err != nil {
				// The file is allowed to be absent, as long as the
				// sentinel is intact; nothing more to check.
				return
			}
			if fi.Mode()&os.ModeSymlink != 0 {
				t.Errorf("extracted link path is a symlink, got mode %s", fi.Mode())
			}
		})
	}
}

// createZipWithSymlinkAndFile writes a zip archive containing first a
// symlink entry whose target is the given path and then a regular file
// entry at the same path, an adversarial trick to try to write to the
// symlink target.
func createZipWithSymlinkAndFile(t *testing.T, name string, target string, fileContent string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	hdr := &zip.FileHeader{
		Name:   name,
		Method: zip.Store,
	}
	// The decompressor copies the entry's permission bits, so include some
	// so that the archive is otherwise well-formed.
	hdr.SetMode(fs.ModeSymlink | 0o777)
	f, err := w.CreateHeader(hdr)
	if err != nil {
		t.Fatalf("failed to create symlink entry in zip: %v", err)
	}
	if _, err := f.Write([]byte(target)); err != nil {
		t.Fatalf("failed to write symlink target in zip: %v", err)
	}
	f, err = w.CreateHeader(&zip.FileHeader{
		Name:   name,
		Method: zip.Store,
	})
	if err != nil {
		t.Fatalf("failed to create file entry in zip: %v", err)
	}
	if _, err := f.Write([]byte(fileContent)); err != nil {
		t.Fatalf("failed to write file entry in zip: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("failed to close zip writer: %v", err)
	}
	return buf.Bytes()
}

// createTarGzWithSymlinkAndFile writes a gzip-compressed tar archive
// containing first a symlink entry whose target is the given path and then
// a regular file entry at the same path, an adversarial trick to try to
// write to the symlink target.
func createTarGzWithSymlinkAndFile(t *testing.T, name string, target string, fileContent string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	if err := tw.WriteHeader(&tar.Header{
		Name:     name,
		Typeflag: tar.TypeSymlink,
		Linkname: target,
		Mode:     0o777,
	}); err != nil {
		t.Fatalf("failed to write symlink header: %v", err)
	}
	if err := tw.WriteHeader(&tar.Header{
		Name:     name,
		Typeflag: tar.TypeReg,
		Size:     int64(len(fileContent)),
		Mode:     0o644,
	}); err != nil {
		t.Fatalf("failed to write file header: %v", err)
	}
	if _, err := tw.Write([]byte(fileContent)); err != nil {
		t.Fatalf("failed to write file entry: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("failed to close tar writer: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("failed to close gzip writer: %v", err)
	}
	return buf.Bytes()
}

// TestExtractModulePackage_HardlinkToExistingFileOutside checks that an
// attacker-controlled archive cannot write through a hardlink entry pointing
// at an existing file outside of the destination: the hardlink itself must
// not hardlink (or otherwise modify) the outside sentinel, and a subsequent
// regular-file entry written through the same name must not touch it.
func TestExtractModulePackage_HardlinkToExistingFileOutside(t *testing.T) {
	t.Parallel()

	sandbox := t.TempDir()
	sentinelPath := filepath.Join(sandbox, "outside.txt")
	const sentinelContent = "# existing sentinel\n"
	if err := os.WriteFile(sentinelPath, []byte(sentinelContent), 0o644); err != nil {
		t.Fatal(err)
	}
	sentinelInfo, err := os.Stat(sentinelPath)
	if err != nil {
		t.Fatal(err)
	}

	f, err := os.CreateTemp("", "ghoten-modpkg-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close(); _ = os.Remove(f.Name()) }()
	if _, err := f.Write(createTarGzWithHardlinkAndFile(t, "link", sentinelPath, "# attack")); err != nil {
		t.Fatal(err)
	}

	targetDir := filepath.Join(sandbox, "pkg")
	if err := extractModulePackage(f, targetDir); err != nil {
		t.Fatalf("unexpected error extracting archive: %v", err)
	}

	// The sentinel file must be unchanged in content and identity.
	got, err := os.ReadFile(sentinelPath)
	if err != nil {
		t.Errorf("failed to read the sentinel file: %v", err)
	} else if string(got) != sentinelContent {
		t.Errorf("a write through the hardlink path modified the sentinel file: got %q, want %q", got, sentinelContent)
	}
	gotInfo, err := os.Stat(sentinelPath)
	if err != nil {
		t.Fatalf("failed to stat the sentinel file: %v", err)
	}
	if !os.SameFile(sentinelInfo, gotInfo) {
		t.Errorf("the sentinel file's inode changed: original %v, current %v", sentinelInfo, gotInfo)
	}

	// The extracted link path must be a regular file (or absent), never a
	// hardlink to the outside sentinel.
	fi, err := os.Lstat(filepath.Join(targetDir, "link"))
	if err != nil {
		// The file is allowed to be absent, as long as the sentinel is
		// intact; nothing more to check.
		return
	}
	if !fi.Mode().IsRegular() {
		t.Errorf("extracted link path is not a regular file, got mode %s", fi.Mode())
	}
	if os.SameFile(fi, gotInfo) {
		t.Errorf("the extracted path is linked to the sentinel file outside of the destination")
	}
}

// createTarGzWithHardlinkAndFile writes a gzip-compressed tar archive
// containing first a hardlink entry whose target is the given path and then
// a regular file entry at the same path, an adversarial trick to try to
// write to the outside file through the hardlink.
func createTarGzWithHardlinkAndFile(t *testing.T, name string, target string, fileContent string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	if err := tw.WriteHeader(&tar.Header{
		Name:     name,
		Typeflag: tar.TypeLink,
		Linkname: target,
		Mode:     0o644,
	}); err != nil {
		t.Fatalf("failed to write hardlink header: %v", err)
	}
	if err := tw.WriteHeader(&tar.Header{
		Name:     name,
		Typeflag: tar.TypeReg,
		Size:     int64(len(fileContent)),
		Mode:     0o644,
	}); err != nil {
		t.Fatalf("failed to write file header: %v", err)
	}
	if _, err := tw.Write([]byte(fileContent)); err != nil {
		t.Fatalf("failed to write file entry: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("failed to close tar writer: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("failed to close gzip writer: %v", err)
	}
	return buf.Bytes()
}

// TestExtractModulePackage_DecompressorSmokeCheck guards the basic sanity of
// adversarial handling by verifying a benign archive still extracts as
// expected through the same helper the adversarial tests use.
func TestExtractModulePackage_BenignArchiveStillExtracts(t *testing.T) {
	t.Parallel()

	f, err := os.CreateTemp("", "ghoten-modpkg-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close(); _ = os.Remove(f.Name()) }()
	if _, err := f.Write(createTestTarGz(t, map[string]string{"main.tf": "# fine"})); err != nil {
		t.Fatal(err)
	}

	targetDir := t.TempDir()
	if err := extractModulePackage(f, targetDir); err != nil {
		t.Fatalf("unexpected error extracting benign archive: %v", err)
	}
	if _, err := os.Stat(filepath.Join(targetDir, "main.tf")); err != nil {
		t.Fatalf("expected benign archive to extract main.tf: %v", err)
	}
}
