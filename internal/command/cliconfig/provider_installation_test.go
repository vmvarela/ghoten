// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package cliconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/opentofu/svchost"

	"github.com/vmvarela/ghoten/internal/addrs"
	"github.com/vmvarela/ghoten/internal/getproviders"
)

func TestLoadConfig_providerInstallation(t *testing.T) {
	for _, configFile := range []string{"provider-installation", "provider-installation.json"} {
		t.Run(configFile, func(t *testing.T) {
			got, diags := loadConfigFile(filepath.Join(fixtureDir, configFile))
			if diags.HasErrors() {
				t.Errorf("unexpected diagnostics: %s", diags.Err().Error())
			}

			want := &Config{
				ProviderInstallation: []*ProviderInstallation{
					{
						Methods: []*ProviderInstallationMethod{
							{
								Location: ProviderInstallationFilesystemMirror("/tmp/example1"),
								Include:  []string{"example.com/*/*"},
							},
							{
								Location: ProviderInstallationNetworkMirror("https://tf-Mirror.example.com/"),
								Include:  []string{"registry.opentofu.org/*/*"},
								Exclude:  []string{"registry.OpenTofu.org/foobar/*"},
								Retries: func() (int, bool) {
									return 2, true
								},
								Trusted: func() bool {
									return true
								},
							},
							{
								Location: ProviderInstallationFilesystemMirror("/tmp/example2"),
							},
							{
								Location: ProviderInstallationDirect,
								Exclude:  []string{"example.com/*/*"},
								Retries: func() (int, bool) {
									return 3, true
								},
							},
						},

						DevOverrides: map[addrs.Provider]getproviders.PackageLocalDir{
							addrs.MustParseProviderSourceString("hashicorp/boop"):  getproviders.PackageLocalDir(filepath.FromSlash("/tmp/boop")),
							addrs.MustParseProviderSourceString("hashicorp/blorp"): getproviders.PackageLocalDir(filepath.FromSlash("/tmp/blorp")),
						},
					},
				},
			}

			if diff := cmp.Diff(want, got,
				cmp.Comparer(func(a, b ProviderInstallationMethodRetries) bool {
					if (a == nil && b != nil) || (a != nil && b == nil) {
						return false
					}
					if a == nil && b == nil {
						return true
					}
					ar, aok := a()
					br, bok := b()
					return ar == br && aok == bok
				}),
				cmp.Comparer(func(a, b ProviderInstallationMethodTrusted) bool {
					if (a == nil && b != nil) || (a != nil && b == nil) {
						return false
					}
					if a == nil && b == nil {
						return true
					}
					return a() == b()
				})); diff != "" {
				t.Errorf("wrong result\n%s", diff)
			}
		})
	}
}

// TestLoadConfig_providerInstallationTrustAllHashes checks the decoding of
// trust_all_hashes for network_mirror blocks in both native HCL and JSON
// syntax: true, false, and omitted all default to explicit behavior.
func TestLoadConfig_providerInstallationTrustAllHashes(t *testing.T) {
	hclTemplate := `provider_installation {
  network_mirror {
    url = "https://tf-Mirror.example.com/"
%s
  }
}
`
	jsonTemplate := `{"provider_installation": {"network_mirror": [{"url": "https://tf-Mirror.example.com/"%s}]}}`
	cases := map[string]struct {
		attributeHCL  string // inserted verbatim into the HCL template ("" when omitted)
		attributeJSON string // inserted verbatim into the JSON template ("" when omitted)
		wantTrusted   bool
		wantError     bool
	}{
		"omitted defaults to false": {
			wantTrusted: false,
		},
		"explicit false": {
			attributeHCL:  "    trust_all_hashes = false",
			attributeJSON: `, "trust_all_hashes": false`,
			wantTrusted:   false,
		},
		"explicit true": {
			attributeHCL:  "    trust_all_hashes = true",
			attributeJSON: `, "trust_all_hashes": true`,
			wantTrusted:   true,
		},
		"non-boolean value is an error": {
			attributeHCL:  `    trust_all_hashes = "yes"`,
			attributeJSON: `, "trust_all_hashes": "yes"`,
			wantTrusted:   false,
			wantError:     true,
		},
	}
	for name, tt := range cases {
		for _, syntax := range []string{"native-syntax", "json"} {
			t.Run(fmt.Sprintf("%s/%s", name, syntax), func(t *testing.T) {
				tt := tt
				syntax := syntax
				var content string
				var attribute string
				if syntax == "json" {
					attribute = tt.attributeJSON
					content = fmt.Sprintf(jsonTemplate, attribute)
				} else {
					attribute = tt.attributeHCL
					content = fmt.Sprintf(hclTemplate, attribute)
				}
				path := filepath.Join(t.TempDir(), "test.tfrc")
				if err := os.WriteFile(path, []byte(content), 0666); err != nil {
					t.Fatalf("unexpected error writing temp config file: %s", err)
				}

				got, diags := loadConfigFile(path)
				if tt.wantError {
					if !diags.HasErrors() {
						t.Fatalf("unexpected success; want error")
					}
					if gotErr := diags.Err().Error(); !strings.Contains(gotErr, "trust_all_hashes") {
						t.Errorf("expected diagnostic about trust_all_hashes, got:\n%s", gotErr)
					}
					return
				}
				if diags.HasErrors() {
					t.Fatalf("unexpected diagnostics: %s", diags.Err().Error())
				}

				gotMethods := got.ProviderInstallation[0].Methods
				if len(gotMethods) != 1 {
					t.Fatalf("wrong number of provider installation methods %d; want 1", len(gotMethods))
				}
				gotMethod := gotMethods[0]
				if _, ok := gotMethod.Location.(ProviderInstallationNetworkMirror); !ok {
					t.Fatalf("wrong location type %T; want ProviderInstallationNetworkMirror", gotMethod.Location)
				}
				if gotMethod.Include != nil || gotMethod.Exclude != nil {
					t.Errorf("unexpected include/exclude: %v / %v", gotMethod.Include, gotMethod.Exclude)
				}
				if r, ok := gotMethod.Retries(); r != 0 || ok {
					t.Errorf("unexpected retries: got (%d, %t); want (0, false) [download_retry_count is not set by this test]", r, ok)
				}
				if gotMethod.Trusted == nil {
					t.Fatalf("Trusted must be set for network_mirror methods, even when the argument is omitted")
				}
				if gotTrusted := gotMethod.Trusted(); gotTrusted != tt.wantTrusted {
					t.Errorf("wrong Trusted result: got %t; want %t", gotTrusted, tt.wantTrusted)
				}
			})
		}
	}
}

func TestLoadConfig_providerInstallationErrors(t *testing.T) {
	_, diags := loadConfigFile(filepath.Join(fixtureDir, "provider-installation-errors"))
	want := `7 problems:

- Invalid provider_installation method block: Unknown provider installation method "not_a_thing" at 2:3.
- Invalid provider_installation method block: Invalid filesystem_mirror block at 1:1: "path" argument is required.
- Invalid provider_installation method block: Invalid network_mirror block at 1:1: "url" argument is required.
- Invalid provider_installation method block: The items inside the provider_installation block at 1:1 must all be blocks.
- Invalid provider_installation method block: The blocks inside the provider_installation block at 1:1 may not have any labels.
- Invalid provider_installation block: The provider_installation block at 9:1 must not have any labels.
- Invalid provider_installation block: The provider_installation block at 11:1 must not be introduced with an equals sign.`

	// The above error messages include only line/column location information
	// and not file location information because HCL 1 does not store
	// information about the filename a location belongs to. (There is a field
	// for it in token.Pos but it's always an empty string in practice.)

	if got := diags.Err().Error(); got != want {
		t.Errorf("wrong diagnostics\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestLoadConfig_providerInstallationOCIMirror(t *testing.T) {
	for _, configFile := range []string{"provider-installation-oci", "provider-installation-oci.json"} {
		t.Run(configFile, func(t *testing.T) {
			gotConfig, diags := loadConfigFile(filepath.Join(fixtureDir, configFile))
			if diags.HasErrors() {
				t.Fatalf("unexpected diagnostics: %s", diags.Err().Error())
			}

			gotInstBlocks := gotConfig.ProviderInstallation
			if got, want := len(gotInstBlocks), 1; got != want {
				t.Fatalf("wrong number of provider_installation blocks %d; want %d", got, want)
			}
			gotMethods := gotInstBlocks[0].Methods
			if got, want := len(gotMethods), 4; got != want {
				t.Fatalf("wrong number of provider installation methods %d; want %d", got, want)
			}

			providerAddr := addrs.Provider{
				Hostname:  svchost.Hostname("registry.opentofu.org"),
				Namespace: "opentofu",
				Type:      "foo",
			}
			t.Run("all segments in template", func(t *testing.T) {
				method := gotMethods[0]
				loc, ok := method.Location.(ProviderInstallationOCIMirror)
				if !ok {
					t.Fatalf("wrong location type %T; want %T", method.Location, loc)
				}
				gotRegistry, gotRepository, err := loc.RepositoryMapping(providerAddr)
				if err != nil {
					t.Fatalf("unexpected error: %s", err)
				}
				if got, want := gotRegistry, "example.com"; got != want {
					t.Errorf("wrong registry domain %q; want %q", got, want)
				}
				if got, want := gotRepository, "registry.opentofu.org/opentofu/foo"; got != want {
					t.Errorf("wrong repository name %q; want %q", got, want)
				}
			})
			t.Run("hostname chosen by include", func(t *testing.T) {
				method := gotMethods[1]
				loc, ok := method.Location.(ProviderInstallationOCIMirror)
				if !ok {
					t.Fatalf("wrong location type %T; want %T", method.Location, loc)
				}
				gotRegistry, gotRepository, err := loc.RepositoryMapping(providerAddr)
				if err != nil {
					t.Fatalf("unexpected error: %s", err)
				}
				if got, want := gotRegistry, "example.net"; got != want {
					t.Errorf("wrong registry domain %q; want %q", got, want)
				}
				if got, want := gotRepository, "opentofu-registry/opentofu/foo"; got != want {
					t.Errorf("wrong repository name %q; want %q", got, want)
				}
			})
			t.Run("hostname and namespace chosen by include", func(t *testing.T) {
				method := gotMethods[2]
				loc, ok := method.Location.(ProviderInstallationOCIMirror)
				if !ok {
					t.Fatalf("wrong location type %T; want %T", method.Location, loc)
				}
				gotRegistry, gotRepository, err := loc.RepositoryMapping(providerAddr)
				if err != nil {
					t.Fatalf("unexpected error: %s", err)
				}
				if got, want := gotRegistry, "example.net"; got != want {
					t.Errorf("wrong registry domain %q; want %q", got, want)
				}
				if got, want := gotRepository, "opentofu-registry/opentofu-namespace/foo"; got != want {
					t.Errorf("wrong repository name %q; want %q", got, want)
				}
			})
			t.Run("all components chosen by include", func(t *testing.T) {
				method := gotMethods[3]
				loc, ok := method.Location.(ProviderInstallationOCIMirror)
				if !ok {
					t.Fatalf("wrong location type %T; want %T", method.Location, loc)
				}
				gotRegistry, gotRepository, err := loc.RepositoryMapping(providerAddr)
				if err != nil {
					t.Fatalf("unexpected error: %s", err)
				}
				if got, want := gotRegistry, "example.net"; got != want {
					t.Errorf("wrong registry domain %q; want %q", got, want)
				}
				if got, want := gotRepository, "opentofu-registry/opentofu-namespace/foo-type"; got != want {
					t.Errorf("wrong repository name %q; want %q", got, want)
				}
			})
		})
	}
}

func TestLoadConfig_providerInstallationOCIMirrorErrors(t *testing.T) {
	t.Run("missing hostname reference", func(t *testing.T) {
		_, diags := loadConfigFile(filepath.Join(fixtureDir, "provider-installation-oci-missinghostname"))
		if !diags.HasErrors() {
			t.Fatalf("unexpected success; want error")
		}
		if got, want := diags.Err().Error(), `template must refer to the "hostname" symbol`; !strings.Contains(got, want) {
			t.Errorf("missing expected error\ngot: %s\nwant substring: %s", got, want)
		}
	})
	t.Run("missing namespace reference", func(t *testing.T) {
		_, diags := loadConfigFile(filepath.Join(fixtureDir, "provider-installation-oci-missingnamespace"))
		if !diags.HasErrors() {
			t.Fatalf("unexpected success; want error")
		}
		if got, want := diags.Err().Error(), `template must refer to the "namespace" symbol`; !strings.Contains(got, want) {
			t.Errorf("missing expected error\ngot: %s\nwant substring: %s", got, want)
		}
	})
	t.Run("missing type reference", func(t *testing.T) {
		_, diags := loadConfigFile(filepath.Join(fixtureDir, "provider-installation-oci-missingtype"))
		if !diags.HasErrors() {
			t.Fatalf("unexpected success; want error")
		}
		if got, want := diags.Err().Error(), `template must refer to the "type" symbol`; !strings.Contains(got, want) {
			t.Errorf("missing expected error\ngot: %s\nwant substring: %s", got, want)
		}
	})
	t.Run("type error in template", func(t *testing.T) {
		_, diags := loadConfigFile(filepath.Join(fixtureDir, "provider-installation-oci-typeerror"))
		if !diags.HasErrors() {
			t.Fatalf("unexpected success; want error")
		}
		if got, want := diags.Err().Error(), `This value does not have any indices`; !strings.Contains(got, want) {
			t.Errorf("missing expected error\ngot: %s\nwant substring: %s", got, want)
		}
	})
	t.Run("value error in template", func(t *testing.T) {
		_, diags := loadConfigFile(filepath.Join(fixtureDir, "provider-installation-oci-valueerror"))
		if !diags.HasErrors() {
			t.Fatalf("unexpected success; want error")
		}
		if got, want := diags.Err().Error(), `a number is required`; !strings.Contains(got, want) {
			t.Errorf("missing expected error\ngot: %s\nwant substring: %s", got, want)
		}
	})
	t.Run("dynamic error in template", func(t *testing.T) {
		cfg, diags := loadConfigFile(filepath.Join(fixtureDir, "provider-installation-oci-dynerror"))
		if diags.HasErrors() {
			t.Fatalf("unexpected error for configuration load (error should be only during template evaluation)")
		}
		method, ok := cfg.ProviderInstallation[0].Methods[0].Location.(ProviderInstallationOCIMirror)
		if !ok {
			t.Fatalf("wrong installation method location type")
		}
		_, _, err := method.RepositoryMapping(addrs.Provider{
			Hostname:  svchost.Hostname("example.net"), // This template fails for anything other than "example.com"
			Namespace: "whatever",
			Type:      "anything",
		})
		if err == nil {
			t.Fatalf("unexpected success; want error")
		}
		if got, want := err.Error(), `The given key does not identify an element in this collection value`; !strings.Contains(got, want) {
			t.Errorf("missing expected error\ngot: %s\nwant substring: %s", got, want)
		}
	})
	t.Run("unmappable characters in provider source address", func(t *testing.T) {
		// This deals with a particularly-annoying case: Ghoten provider source addresses
		// support a wide range of unicode characters with the intent that folks can name
		// their private providers using the alphabet of their native language, but OCI Distribution
		// only allows ASCII characters in repository names, so for now the OCI mirror
		// installation method can only work with providers whose namespace and type
		// are ASCII-only. Non-ASCII characters are pretty rare in practice for public
		// providers, but we can't tell whether they are more common in private provider
		// registries. For now we treat this as an error but we might try to find a better
		// answer for this in a future release if it proves to be a problem in practice.
		cfg, diags := loadConfigFile(filepath.Join(fixtureDir, "provider-installation-oci-passthru"))
		if diags.HasErrors() {
			t.Fatalf("unexpected error for configuration load (error should be only during template evaluation)")
		}
		method, ok := cfg.ProviderInstallation[0].Methods[0].Location.(ProviderInstallationOCIMirror)
		if !ok {
			t.Fatalf("wrong installation method location type")
		}
		_, _, err := method.RepositoryMapping(addrs.Provider{
			Hostname:  svchost.Hostname("example.com"),
			Namespace: "ほげ",
			Type:      "ふが",
		})
		if err == nil {
			t.Fatalf("unexpected success; want error")
		}
		if got, want := err.Error(), `invalid repository "example.com/ほげ/ふが"`; !strings.Contains(got, want) {
			t.Errorf("missing expected error\ngot: %s\nwant substring: %s", got, want)
		}
	})
}
