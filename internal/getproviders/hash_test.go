// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package getproviders

import (
	"archive/zip"
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/vmvarela/ghoten/internal/addrs"
	"github.com/vmvarela/ghoten/internal/collections"
)

func TestParseHash(t *testing.T) {
	tests := []struct {
		Input   string
		Want    Hash
		WantErr string
	}{
		{
			Input: "h1:foo",
			Want:  HashScheme1.New("foo"),
		},
		{
			Input: "zh:bar",
			Want:  HashSchemeZip.New("bar"),
		},
		{
			// A scheme we don't know is considered valid syntax, it just won't match anything.
			Input: "unknown:baz",
			Want:  HashScheme("unknown:").New("baz"),
		},
		{
			// A scheme with an empty value is weird, but allowed.
			Input: "unknown:",
			Want:  HashScheme("unknown:").New(""),
		},
		{
			Input:   "",
			WantErr: "hash string must start with a scheme keyword followed by a colon",
		},
		{
			// A naked SHA256 hash in hex format is not sufficient
			Input:   "1e5f7a5f3ade7b8b1d1d59c5cea2e1a2f8d2f8c3f41962dbbe8647e222be8239",
			WantErr: "hash string must start with a scheme keyword followed by a colon",
		},
		{
			// An empty scheme is not allowed
			Input:   ":blah",
			WantErr: "hash string must start with a scheme keyword followed by a colon",
		},
	}

	for _, test := range tests {
		t.Run(test.Input, func(t *testing.T) {
			got, err := ParseHash(test.Input)

			if test.WantErr != "" {
				if err == nil {
					t.Fatalf("want error: %s", test.WantErr)
				}
				if got, want := err.Error(), test.WantErr; got != want {
					t.Fatalf("wrong error\ngot:  %s\nwant: %s", got, want)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %s", err.Error())
			}

			if got != test.Want {
				t.Errorf("wrong result\ngot:  %#v\nwant: %#v", got, test.Want)
			}
		})
	}
}

func TestMergeHashDisposition(t *testing.T) {
	tests := map[string]struct {
		a, b *HashDisposition
		want *HashDisposition
	}{
		"empties": {
			a:    &HashDisposition{},
			b:    &HashDisposition{},
			want: &HashDisposition{},
		},
		"empty with VerifiedLocally": {
			a: &HashDisposition{
				VerifiedLocally: true,
			},
			b: &HashDisposition{},
			want: &HashDisposition{
				VerifiedLocally: true,
			},
		},
		"empty with ReportedByRegistry": {
			a: &HashDisposition{
				ReportedByRegistry: true,
			},
			b: &HashDisposition{},
			want: &HashDisposition{
				ReportedByRegistry: true,
			},
		},
		"empty with ReportedByTrustedMirror": {
			a: &HashDisposition{
				ReportedByTrustedMirror: true,
			},
			b: &HashDisposition{},
			want: &HashDisposition{
				ReportedByTrustedMirror: true,
			},
		},
		"empty with one GPG key": {
			a: &HashDisposition{
				SignedByGPGKeyIDs: collections.NewSet("abc123"),
			},
			b: &HashDisposition{},
			want: &HashDisposition{
				SignedByGPGKeyIDs: collections.NewSet("abc123"),
			},
		},
		"many GPG keys": {
			a: &HashDisposition{
				SignedByGPGKeyIDs: collections.NewSet("abc123", "def456"),
			},
			b: &HashDisposition{
				SignedByGPGKeyIDs: collections.NewSet("def456", "ghi789"),
			},
			want: &HashDisposition{
				SignedByGPGKeyIDs: collections.NewSet("abc123", "def456", "ghi789"),
			},
		},
		"VerifiedLocally with ReportedByRegistry": {
			a: &HashDisposition{
				ReportedByRegistry: true,
			},
			b: &HashDisposition{
				VerifiedLocally: true,
			},
			want: &HashDisposition{
				ReportedByRegistry: true,
				VerifiedLocally:    true,
			},
		},
		"VerifiedLocally with itself": {
			a: &HashDisposition{
				VerifiedLocally: true,
			},
			b: &HashDisposition{
				VerifiedLocally: true,
			},
			want: &HashDisposition{
				VerifiedLocally: true,
			},
		},
		"ReportedByRegistry with itself": {
			a: &HashDisposition{
				ReportedByRegistry: true,
			},
			b: &HashDisposition{
				ReportedByRegistry: true,
			},
			want: &HashDisposition{
				ReportedByRegistry: true,
			},
		},
		"Everything at once": {
			a: &HashDisposition{
				SignedByGPGKeyIDs:  collections.NewSet("def456", "ghi789"),
				ReportedByRegistry: true,
			},
			b: &HashDisposition{
				VerifiedLocally: true,
			},
			want: &HashDisposition{
				SignedByGPGKeyIDs:  collections.NewSet("def456", "ghi789"),
				ReportedByRegistry: true,
				VerifiedLocally:    true,
			},
		},
		"ReportedByTrustedMirror with VerifiedLocally": {
			a: &HashDisposition{
				ReportedByTrustedMirror: true,
			},
			b: &HashDisposition{
				VerifiedLocally: true,
			},
			want: &HashDisposition{
				ReportedByTrustedMirror: true,
				VerifiedLocally:         true,
			},
		},
		"ReportedByTrustedMirror with itself": {
			a: &HashDisposition{
				ReportedByTrustedMirror: true,
			},
			b: &HashDisposition{
				ReportedByTrustedMirror: true,
			},
			want: &HashDisposition{
				ReportedByTrustedMirror: true,
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			// MergeHashDisposition is supposed to be commutative, so
			// we'll test each case in both orders and expect an
			// equivalent result in each case.
			t.Run("a,b", func(t *testing.T) {
				got := MergeHashDisposition(test.a, test.b)
				if diff := cmp.Diff(test.want, got); diff != "" {
					t.Error("wrong result\n" + diff)
				}
			})
			t.Run("b,a", func(t *testing.T) {
				got := MergeHashDisposition(test.b, test.a)
				if diff := cmp.Diff(test.want, got); diff != "" {
					t.Error("wrong result\n" + diff)
				}
			})
		})
	}
}

func TestHashDispositionsMerge(t *testing.T) {
	// HashDispositions.Merge delegates to MergeHashDisposition when both
	// arguments refer to the same hash. We already have lots of tests for
	// MergeHashDisposition in TestMergeHashDisposition, so this test
	// intentionally does not duplicate all of those cases and focuses
	// only on the different cases that HashDispositions.Merge is directly
	// concernd with.

	tests := map[string]struct {
		a, b HashDispositions
		want HashDispositions
	}{
		"empties": {
			a:    HashDispositions{},
			b:    HashDispositions{},
			want: HashDispositions{},
		},
		"one into empty": {
			a: HashDispositions{},
			b: HashDispositions{
				Hash("test:foo"): &HashDisposition{
					ReportedByRegistry: true,
				},
			},
			want: HashDispositions{
				Hash("test:foo"): &HashDisposition{
					ReportedByRegistry: true,
				},
			},
		},
		"independent hashes": {
			a: HashDispositions{
				Hash("test:foo"): &HashDisposition{
					ReportedByRegistry: true,
				},
			},
			b: HashDispositions{
				Hash("test:bar"): &HashDisposition{
					VerifiedLocally: true,
				},
			},
			want: HashDispositions{
				Hash("test:foo"): &HashDisposition{
					ReportedByRegistry: true,
				},
				Hash("test:bar"): &HashDisposition{
					VerifiedLocally: true,
				},
			},
		},
		"overlapping hashes": {
			a: HashDispositions{
				Hash("test:foo"): &HashDisposition{
					ReportedByRegistry: true,
				},
			},
			b: HashDispositions{
				Hash("test:foo"): &HashDisposition{
					VerifiedLocally: true,
				},
			},
			want: HashDispositions{
				// This should be the result of MergeHashDispositions
				// on the two different entries for test:foo.
				Hash("test:foo"): &HashDisposition{
					ReportedByRegistry: true,
					VerifiedLocally:    true,
				},
			},
		},
		"mix of overlapping and independent": {
			a: HashDispositions{
				Hash("test:foo"): &HashDisposition{
					SignedByGPGKeyIDs: collections.NewSet("abc123"),
				},
				Hash("test:bar"): &HashDisposition{
					ReportedByRegistry: true,
				},
			},
			b: HashDispositions{
				Hash("test:foo"): &HashDisposition{
					SignedByGPGKeyIDs: collections.NewSet("def456"),
				},
				Hash("test:baz"): &HashDisposition{
					VerifiedLocally: true,
				},
			},
			want: HashDispositions{
				Hash("test:foo"): &HashDisposition{
					SignedByGPGKeyIDs: collections.NewSet("abc123", "def456"),
				},
				Hash("test:bar"): &HashDisposition{
					ReportedByRegistry: true,
				},
				Hash("test:baz"): &HashDisposition{
					VerifiedLocally: true,
				},
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			// HashDispositions.Merge is supposed to be commutative,
			// so we'll test each case in both orders and expect an
			// equivalent result in each case.
			t.Run("a,b", func(t *testing.T) {
				// We'll make a shallow copy of test.a so that we
				// aren't directly modifying the test table, since
				// otherwise we'll pollute the input to the
				// opposite order test below.
				got := maps.Clone(test.a)
				got.Merge(test.b)
				if diff := cmp.Diff(test.want, got); diff != "" {
					t.Error("wrong result\n" + diff)
				}
			})
			t.Run("b,a", func(t *testing.T) {
				got := maps.Clone(test.b)
				got.Merge(test.a)
				if diff := cmp.Diff(test.want, got); diff != "" {
					t.Error("wrong result\n" + diff)
				}
			})
		})
	}
}

func TestEmptyPackageHashV1(t *testing.T) {
	emptyDir := t.TempDir()
	realHash, err := PackageHashV1(PackageLocalDir(emptyDir))
	if err != nil {
		t.Fatalf("failed to calculate hash of empty package: %s", err)
	}
	if realHash != emptyPackageHashV1 {
		t.Errorf("emptyPackageHashV1 does not match freshly-calculated hash of empty package\nemptyPackageHashV1: %s\ncalculated result:  %s", emptyPackageHashV1, realHash)
	}
}

// testLocalArchiveZip writes a minimal real provider package .zip file
// containing the given files, for exercising the local archive installation
// codepath in tests, and returns its full path.
func testLocalArchiveZip(t *testing.T, files map[string]string) PackageLocalArchive {
	t.Helper()

	zipPath := filepath.Join(t.TempDir(), "fake-provider-package.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("failed to create temporary zip file: %s", err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	// Iterate in a fixed order so that filenames in the resulting archive
	// are deterministic and thus its calculated hash is predictable.
	for _, name := range slices.Sorted(maps.Keys(files)) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("failed to create zip entry %q: %s", name, err)
		}
		if _, err := w.Write([]byte(files[name])); err != nil {
			t.Fatalf("failed to write zip entry %q: %s", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("failed to finish zip file: %s", err)
	}
	return PackageLocalArchive(zipPath)
}

// TestPackageLocalArchiveInstallProviderPackage covers the behavior of
// PackageLocalArchive.InstallProviderPackage when there already is a directory
// entry at the installation target: exact matching entries are reused, empty
// directories are accepted, and anything else (including a symlink to an empty
// directory) must be rejected without modifying the existing entry.
func TestPackageLocalArchiveInstallProviderPackage(t *testing.T) {
	provider := addrs.MustParseProviderSourceString("example.com/test/provider")
	packageFiles := map[string]string{
		"terraform-provider-test_v1.0.0_x5": "fake provider binary",
		"LICENSE":                           "fake license text",
	}
	zipFile := testLocalArchiveZip(t, packageFiles)

	meta := PackageMeta{
		Provider:       provider,
		Version:        MustParseVersion("1.0.0"),
		Location:       zipFile,
		TargetPlatform: CurrentPlatform,
	}
	install := func(t *testing.T, targetDir string) error {
		t.Helper()
		_, err := zipFile.InstallProviderPackage(context.Background(), meta, targetDir, nil)
		return err
	}

	t.Run("fresh install", func(t *testing.T) {
		targetDir := filepath.Join(t.TempDir(), "provider")
		if err := install(t, targetDir); err != nil {
			t.Fatalf("unexpected error: %s", err)
		}
		for name, want := range packageFiles {
			got, err := os.ReadFile(filepath.Join(targetDir, name))
			if err != nil {
				t.Fatalf("expected file %q is missing: %s", name, err)
			}
			if string(got) != want {
				t.Errorf("wrong content for file %q", name)
			}
		}
	})
	t.Run("reuse exact matching entry", func(t *testing.T) {
		targetDir := filepath.Join(t.TempDir(), "provider")
		if err := install(t, targetDir); err != nil {
			t.Fatalf("unexpected error during preparation: %s", err)
		}

		// Making the directory read-only means a second installation that
		// tried to write there would fail, so returning success proves that
		// the matching entry was reused without rewriting it.
		if err := os.Chmod(targetDir, 0o500); err == nil {
			t.Cleanup(func() {
				if err := os.Chmod(targetDir, 0o700); err != nil {
					t.Errorf("failed to restore directory permissions: %s", err)
				}
			})
		} else {
			t.Fatalf("failed to make existing entry read-only: %s", err)
		}
		if err := install(t, targetDir); err != nil {
			t.Errorf("unexpected error when installing into an already-matching entry: %s", err)
		}
	})
	t.Run("reject mismatching entry, preserve it", func(t *testing.T) {
		targetDir := filepath.Join(t.TempDir(), "provider")
		err := os.MkdirAll(targetDir, os.ModePerm)
		if err != nil {
			t.Fatal(err)
		}
		extraFile := filepath.Join(targetDir, "extra.txt")
		const extraContent = "this file is not part of the fake package"
		if err := os.WriteFile(extraFile, []byte(extraContent), os.ModePerm); err != nil {
			t.Fatal(err)
		}

		err = install(t, targetDir)
		if err == nil {
			t.Fatal("unexpected success; want rejection of mismatching entry")
		}
		if want := "does not match the content of the downloaded package"; !strings.Contains(err.Error(), want) {
			t.Errorf("error message missing expected substring %q\ngot: %s", want, err)
		}
		got, err := os.ReadFile(extraFile)
		if err != nil {
			t.Fatalf("mismatching entry was not preserved: %s", err)
		}
		if string(got) != extraContent {
			t.Error("mismatching entry was modified")
		}
	})
	t.Run("accept empty directory", func(t *testing.T) {
		targetDir := filepath.Join(t.TempDir(), "provider")
		if err := os.Mkdir(targetDir, os.ModePerm); err != nil {
			t.Fatal(err)
		}
		if err := install(t, targetDir); err != nil {
			t.Fatalf("unexpected failure installing into a preexisting empty directory: %s", err)
		}
		for name, want := range packageFiles {
			got, err := os.ReadFile(filepath.Join(targetDir, name))
			if err != nil {
				t.Fatalf("expected file %q is missing: %s", name, err)
			}
			if string(got) != want {
				t.Errorf("wrong content for file %q", name)
			}
		}
	})
	t.Run("reject symlink to empty directory", func(t *testing.T) {
		symlinkTarget := filepath.Join(t.TempDir(), "empty-target")
		if err := os.Mkdir(symlinkTarget, os.ModePerm); err != nil {
			t.Fatal(err)
		}
		targetDir := filepath.Join(t.TempDir(), "provider")
		if err := os.Symlink(symlinkTarget, targetDir); err != nil {
			skipIfSymlinkUnavailable(t, err)
		}

		err := install(t, targetDir)
		if err == nil {
			t.Fatal("unexpected success; want rejection of symlink to an empty directory")
		}
		if want := "does not match the content of the downloaded package"; !strings.Contains(err.Error(), want) {
			t.Errorf("error message missing expected substring %q\ngot: %s", want, err)
		}
		// The symlink and its target must be left untouched.
		entries, err := os.ReadDir(symlinkTarget)
		if err != nil {
			t.Fatalf("symlink target disappeared: %s", err)
		}
		if len(entries) != 0 {
			t.Errorf("symlink target was modified: %v", entries)
		}
	})
	t.Run("accept symlink to matching directory", func(t *testing.T) {
		symlinkTarget := filepath.Join(t.TempDir(), "installed")
		if err := install(t, symlinkTarget); err != nil {
			t.Fatalf("unexpected error during preparation: %s", err)
		}
		targetDir := filepath.Join(t.TempDir(), "provider")
		if err := os.Symlink(symlinkTarget, targetDir); err != nil {
			skipIfSymlinkUnavailable(t, err)
		}
		if err := install(t, targetDir); err != nil {
			t.Errorf("unexpected error when installing through a symlink to a matching directory: %s", err)
		}
	})
}

func skipIfSymlinkUnavailable(t *testing.T, err error) {
	t.Helper()
	errMessage := strings.ToLower(err.Error())
	if runtime.GOOS == "windows" &&
		(errors.Is(err, os.ErrPermission) || strings.Contains(errMessage, "privilege") || strings.Contains(errMessage, "access is denied")) {
		t.Skipf("cannot create symlink without Windows privileges: %s", err)
	}
	t.Fatalf("failed to create symlink: %s", err)
}
