// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package registry

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/hashicorp/go-retryablehttp"
	version "github.com/hashicorp/go-version"
	regaddr "github.com/opentofu/registry-address/v2"
	"github.com/opentofu/svchost"
	"github.com/opentofu/svchost/disco"
	"github.com/opentofu/svchost/svcauth"
	"github.com/ulikunitz/xz"

	"github.com/vmvarela/ghoten/internal/addrs"
	"github.com/vmvarela/ghoten/internal/httpclient"
	"github.com/vmvarela/ghoten/internal/registry/response"
	"github.com/vmvarela/ghoten/internal/registry/test"
)

func TestLookupModuleVersions(t *testing.T) {
	server := test.Registry()
	defer server.Close()

	client := NewClient(t.Context(), test.Disco(server), nil)

	// test with and without a hostname
	for _, src := range []string{
		"example.com/test-versions/name/provider",
		"test-versions/name/provider",
	} {
		modsrc := testParseModulePackageAddr(t, src)
		resp, err := client.ModulePackageVersions(context.Background(), modsrc)
		if err != nil {
			t.Fatal(err)
		}

		if len(resp.Modules) != 1 {
			t.Fatal("expected 1 module, got", len(resp.Modules))
		}

		mod := resp.Modules[0]
		name := "test-versions/name/provider"
		if mod.Source != name {
			t.Fatalf("expected module name %q, got %q", name, mod.Source)
		}

		if len(mod.Versions) != 4 {
			t.Fatal("expected 4 versions, got", len(mod.Versions))
		}

		for _, v := range mod.Versions {
			_, err := version.NewVersion(v.Version)
			if err != nil {
				t.Fatalf("invalid version %q: %s", v.Version, err)
			}
		}
	}
}

func TestInvalidRegistry(t *testing.T) {
	server := test.Registry()
	defer server.Close()

	client := NewClient(t.Context(), test.Disco(server), nil)

	src := "non-existent.localhost.localdomain/test-versions/name/provider"
	modsrc := testParseModulePackageAddr(t, src)

	if _, err := client.ModulePackageVersions(context.Background(), modsrc); err == nil {
		t.Fatal("expected error")
	}
}

func TestRegistryAuth(t *testing.T) {
	// Credentials are only sent to https destinations, so use a TLS registry
	// and a client that trusts its self-signed certificate.
	server := test.RegistryTLS()
	defer server.Close()

	retryable := retryablehttp.NewClient()
	retryable.HTTPClient = server.Client()
	client := NewClient(t.Context(), test.Disco(server), retryable)

	src := "private/name/provider"
	modsrc := testParseModulePackageAddr(t, src)

	_, err := client.ModulePackageVersions(context.Background(), modsrc)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.ModulePackageLocation(context.Background(), modsrc, "1.0.0", "")
	if err != nil {
		t.Fatal(err)
	}

	// Also test without a credentials source
	client.services.SetCredentialsSource(nil)

	// both should fail without auth
	_, err = client.ModulePackageVersions(context.Background(), modsrc)
	if err == nil {
		t.Fatal("expected error")
	}
	_, err = client.ModulePackageLocation(context.Background(), modsrc, "1.0.0", "")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRegistryAuth_noCredentialsOverHTTP(t *testing.T) {
	// An http registry must not receive credentials: the registry mock
	// rejects requests without them, so the lookup must fail.
	server := test.Registry()
	defer server.Close()

	client := NewClient(t.Context(), test.Disco(server), nil)
	modsrc := testParseModulePackageAddr(t, "private/name/provider")

	if _, err := client.ModulePackageVersions(context.Background(), modsrc); err == nil {
		t.Fatal("expected credentials to be withheld from an http registry")
	}
}

func TestLookupModuleLocationRelative(t *testing.T) {
	server := test.Registry()
	defer server.Close()

	client := NewClient(t.Context(), test.Disco(server), nil)

	src := "relative/foo/bar"
	modsrc := testParseModulePackageAddr(t, src)

	got, err := client.ModulePackageLocation(context.Background(), modsrc, "0.2.0", "")
	if err != nil {
		t.Fatal(err)
	}

	want := PackageLocationIndirect{
		SourceAddr: addrs.ModuleSourceRemote{
			Package: addrs.ModulePackage(server.URL + "/relative-path"),
		},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Error("wrong location\n" + diff)
	}
}

func TestAccLookupModuleVersions(t *testing.T) {
	if os.Getenv("TF_ACC") == "" {
		t.Skip()
	}
	regDisco := disco.New(
		disco.WithHTTPClient(httpclient.New(t.Context())),
	)

	// test with and without a hostname
	for _, src := range []string{
		"terraform-aws-modules/vpc/aws",
		regaddr.DefaultModuleRegistryHost.String() + "/terraform-aws-modules/vpc/aws",
	} {
		modsrc := testParseModulePackageAddr(t, src)

		s := NewClient(t.Context(), regDisco, nil)
		resp, err := s.ModulePackageVersions(context.Background(), modsrc)
		if err != nil {
			t.Fatal(err)
		}

		if len(resp.Modules) != 1 {
			t.Fatal("expected 1 module, got", len(resp.Modules))
		}

		mod := resp.Modules[0]
		if len(mod.Versions) == 0 {
			t.Fatal("expected multiple versions, got 0")
		}
		for _, v := range mod.Versions {
			_, err := version.NewVersion(v.Version)
			if err != nil {
				t.Fatalf("invalid version %q: %s", v.Version, err)
			}
		}
	}
}

// the error should reference the config source exactly, not the discovered path.
func TestLookupLookupModuleError(t *testing.T) {
	server := test.Registry()
	defer server.Close()

	client := NewClient(t.Context(), test.Disco(server), nil)

	// this should not be found in the registry
	src := "bad/local/path"
	modsrc := testParseModulePackageAddr(t, src)

	// Instrument CheckRetry to make sure 404s are not retried
	retries := 0
	oldCheck := client.client.CheckRetry
	client.client.CheckRetry = func(ctx context.Context, resp *http.Response, err error) (bool, error) {
		if retries > 0 {
			t.Fatal("retried after module not found")
		}
		retries++
		return oldCheck(ctx, resp, err)
	}

	_, err := client.ModulePackageLocation(context.Background(), modsrc, "0.2.0", "")
	if err == nil {
		t.Fatal("expected error")
	}

	// Check for the exact quoted string to ensure we prepended the registry
	// hostname. As a convention we always use the fully-qualified address
	// syntax when reporting errors because it's helpful for debugging to be
	// explicit about what registry we were trying to talk to.
	//
	// (Historically this code made the opposite decision and just echoed back
	// the provided source address exactly as given, but we later made a
	// cross-cutting decision to use canonical forms of addresses in error
	// messages unless we're reporting a syntax error in particular, which
	// is not the case here: this is a failed lookup of a valid address.)
	if !strings.Contains(err.Error(), `"registry.opentofu.org/bad/local/path"`) {
		t.Fatal("error should not include the hostname. got:", err)
	}
}

func TestLookupModuleRetryError(t *testing.T) {
	server := test.RegistryRetryableErrorsServer()
	defer server.Close()

	client := NewClient(t.Context(), test.Disco(server), nil)

	src := "example.com/test-versions/name/provider"
	modsrc := testParseModulePackageAddr(t, src)
	resp, err := client.ModulePackageVersions(context.Background(), modsrc)
	if err == nil {
		t.Fatal("expected requests to exceed retry", err)
	}
	if resp != nil {
		t.Fatal("unexpected response", *resp)
	}

	// verify maxRetryErrorHandler handler returned the error
	if !strings.Contains(err.Error(), "request failed after 2 attempts") {
		t.Fatal("unexpected error, got:", err)
	}
}

func TestLookupModuleNoRetryError(t *testing.T) {
	server := test.RegistryRetryableErrorsServer()
	defer server.Close()

	client := NewClient(
		t.Context(), test.Disco(server),
		// Retries are disabled by the second argument to this function
		httpclient.NewForRegistryRequests(t.Context(), 0, 10*time.Second),
	)

	src := "example.com/test-versions/name/provider"
	modsrc := testParseModulePackageAddr(t, src)
	resp, err := client.ModulePackageVersions(context.Background(), modsrc)
	if err == nil {
		t.Fatal("expected request to fail", err)
	}
	if resp != nil {
		t.Fatal("unexpected response", *resp)
	}

	// verify maxRetryErrorHandler handler returned the error
	if !strings.Contains(err.Error(), "request failed:") {
		t.Fatal("unexpected error, got:", err)
	}
}

func TestLookupModuleNetworkError(t *testing.T) {
	server := test.RegistryRetryableErrorsServer()
	client := NewClient(t.Context(), test.Disco(server), nil)

	// Shut down the server to simulate network failure
	server.Close()

	src := "example.com/test-versions/name/provider"
	modsrc := testParseModulePackageAddr(t, src)
	resp, err := client.ModulePackageVersions(context.Background(), modsrc)
	if err == nil {
		t.Fatal("expected request to fail", err)
	}
	if resp != nil {
		t.Fatal("unexpected response", *resp)
	}

	// verify maxRetryErrorHandler handler returned the correct error
	if !strings.Contains(err.Error(), "request failed after 2 attempts") {
		t.Fatal("unexpected error, got:", err)
	}
}

func TestModuleLocation_readRegistryResponse(t *testing.T) {
	makeIndirectLocation := func(packageAddr string, subDir string) PackageLocationIndirect {
		return PackageLocationIndirect{
			SourceAddr: addrs.ModuleSourceRemote{
				Package: addrs.ModulePackage(packageAddr),
				Subdir:  subDir,
			},
		}
	}
	mustParseURL := func(s string) *url.URL {
		ret, err := url.Parse(s)
		if err != nil {
			t.Fatal(err)
		}
		return ret
	}

	cases := map[string]struct {
		src           string
		subdir        string
		handlerFunc   func(w http.ResponseWriter, r *http.Request)
		registryFlags []uint8
		want          PackageLocation
		// wantAgainstServer is an alternative to want for cases whose expected
		// location depends on the test registry server's base URL, e.g. when
		// a relative location is resolved against the metadata URL.
		wantAgainstServer    func(registryServerURL string) PackageLocation
		wantErrorStr         string
		wantToReadFromHeader bool
		wantStatusCode       int
	}{
		"shall find direct module location in the registry response body, opting to use the registry's credentials": {
			src: "exists-in-registry/identifier/provider",
			want: PackageLocationDirect{
				packageAddr: testParseModulePackageAddr(t, "exists-in-registry/identifier/provider"),
				packageURL:  mustParseURL("https://example.com/package.zip"),
				// The registry metadata is fetched over plain HTTP here, so
				// the reuse of registry credentials is refused (a
				// man-in-the-middle attacker could choose the package URL to
				// steal them). See TestModulePackageLocation_MetadataOverHTTP
				// and ...MetadataOverHTTPS for the end-to-end behavior.
				useRegistryCredentials: false,
			},
			wantStatusCode: http.StatusOK,
			handlerFunc: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"location":"https://example.com/package.zip","use_registry_credentials":true}`))
			},
		},
		"shall find direct module location in the registry response body, not opting to use the registry's credentials": {
			src: "exists-in-registry/identifier/provider",
			want: PackageLocationDirect{
				packageAddr:            testParseModulePackageAddr(t, "exists-in-registry/identifier/provider"),
				packageURL:             mustParseURL("https://example.com/package.zip"),
				useRegistryCredentials: false,
			},
			wantStatusCode: http.StatusOK,
			handlerFunc: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"location":"https://example.com/package.zip","use_registry_credentials":false}`))
			},
		},
		"shall resolve a relative direct location against the registry metadata URL": {
			src: "exists-in-registry/identifier/provider",
			wantAgainstServer: func(registryServerURL string) PackageLocation {
				return PackageLocationDirect{
					packageAddr:            testParseModulePackageAddr(t, "exists-in-registry/identifier/provider"),
					packageURL:             mustParseURL(registryServerURL + "/relative-path"),
					useRegistryCredentials: false,
				}
			},
			wantStatusCode: http.StatusOK,
			handlerFunc: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"location":"/relative-path","use_registry_credentials":false}`))
			},
		},
		"shall resolve a relative direct location without a leading slash against the metadata endpoint path": {
			src: "exists-in-registry/identifier/provider",
			wantAgainstServer: func(registryServerURL string) PackageLocation {
				// A relative reference replaces the last segment of the
				// metadata URL (.../0.2.0/download), so the result is
				// .../0.2.0/pkgs/x.zip. The registry metadata is fetched
				// over plain HTTP here, so the reuse of registry credentials
				// is refused; see TestModulePackageLocation_MetadataOverHTTPS
				// for the end-to-end https case.
				return PackageLocationDirect{
					packageAddr:            testParseModulePackageAddr(t, "exists-in-registry/identifier/provider"),
					packageURL:             mustParseURL(registryServerURL + "/v1/modules/exists-in-registry/identifier/provider/0.2.0/pkgs/x.zip"),
					useRegistryCredentials: false,
				}
			},
			wantStatusCode: http.StatusOK,
			handlerFunc: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"location":"pkgs/x.zip","use_registry_credentials":true}`))
			},
		},
		"shall reject a direct location with an unsupported scheme": {
			src:            "exists-in-registry/identifier/provider",
			wantErrorStr:   `must be http or https URL`,
			wantStatusCode: http.StatusOK,
			handlerFunc: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"location":"file:///package.zip","use_registry_credentials":false}`))
			},
		},
		"shall reject a direct location that includes a fragment": {
			src:            "exists-in-registry/identifier/provider",
			wantErrorStr:   `must not include fragment part`,
			wantStatusCode: http.StatusOK,
			handlerFunc: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"location":"https://example.com/package.zip#frag","use_registry_credentials":false}`))
			},
		},
		"shall reject a direct location that is not a valid URL": {
			src:            "exists-in-registry/identifier/provider",
			wantErrorStr:   `registry returned an invalid package URL`,
			wantStatusCode: http.StatusOK,
			handlerFunc: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"location":"http://exa mple.com/package.zip","use_registry_credentials":false}`))
			},
		},
		"shall fall back to indirect when use_registry_credentials is null": {
			src:            "exists-in-registry/identifier/provider",
			want:           makeIndirectLocation("file:///registry/exists", ""),
			wantStatusCode: http.StatusOK,
			handlerFunc: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"location":"file:///registry/exists","use_registry_credentials":null}`))
			},
		},
		"shall reject a non-boolean use_registry_credentials": {
			src:            "exists-in-registry/identifier/provider",
			wantErrorStr:   `must be either true or false`,
			wantStatusCode: http.StatusOK,
			handlerFunc: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"location":"file:///registry/exists","use_registry_credentials":"yes"}`))
			},
		},
		"shall find indirect module location in the registry response body": {
			src:            "exists-in-registry/identifier/provider",
			want:           makeIndirectLocation("file:///registry/exists", ""),
			wantStatusCode: http.StatusOK,
			handlerFunc: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(response.ModuleLocationRegistryResp{Location: "file:///registry/exists"})
			},
		},
		"shall find indirect module location when use_registry_credentials is absent from the JSON": {
			// This body is deliberately hand-written rather than produced by
			// json.Marshal, which would serialize a nil UseRegistryCredentials
			// pointer as an explicit null and would not cover the truly-absent
			// property case.
			src:            "exists-in-registry/identifier/provider",
			want:           makeIndirectLocation("file:///registry/exists", ""),
			wantStatusCode: http.StatusOK,
			handlerFunc: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"location":"file:///registry/exists"}`))
			},
		},
		"shall fall back to the X-Terraform-Get header when a JSON body has no location": {
			src:                  "exists-in-registry/identifier/provider",
			want:                 makeIndirectLocation("file:///registry/from-header", ""),
			wantToReadFromHeader: true,
			wantStatusCode:       http.StatusOK,
			handlerFunc: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Terraform-Get", "file:///registry/from-header")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"unrelated":"property"}`))
			},
		},
		"shall reject an indirect location that is a recursive registry address": {
			src:            "exists-in-registry/identifier/provider",
			wantErrorStr:   `must be a direct remote package address`,
			wantStatusCode: http.StatusOK,
			handlerFunc: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				// A valid registry address, which would just point the client
				// back at another registry lookup: registries must return
				// remote package locations instead.
				_, _ = w.Write([]byte(`{"location":"registry.opentofu.org/other/recursive/provider"}`))
			},
		},
		"shall reject an indirect location that is a local module source address": {
			// Note that this reaches the ModuleSourceLocal parse branch (rather
			// than the relative-URL resolution branch) because the Windows-style
			// relative prefix is not treated as a relative URL prefix.
			src:            "exists-in-registry/identifier/provider",
			wantErrorStr:   `must be a direct remote package address`,
			wantStatusCode: http.StatusOK,
			handlerFunc: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"location":".\\l1\\l2"}`))
			},
		},
		"shall join the requested subdirectory with the indirect location's subdirectory": {
			src:            "exists-in-registry/identifier/provider",
			subdir:         "pkg-two/from-source",
			want:           makeIndirectLocation("file:///registry/pkg-one", "in-location/pkg-two/from-source"),
			wantStatusCode: http.StatusOK,
			handlerFunc: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(response.ModuleLocationRegistryResp{Location: "file:///registry/pkg-one//in-location"})
			},
		},
		"shall reject an indirect location that cannot be parsed as a remote source address": {
			src:            "foo/bar/baz",
			wantErrorStr:   `registry returned invalid package location`,
			wantStatusCode: http.StatusOK,
			handlerFunc: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				// Neither a remote package address nor any other supported
				// module source type.
				_, _ = w.Write([]byte(`{"location":"not-a-registry"}`))
			},
		},
		"shall reject an indirect location that has invalid source syntax": {
			src:            "foo/bar/baz",
			wantErrorStr:   `registry returned invalid package location`,
			wantStatusCode: http.StatusOK,
			handlerFunc: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"location":"http://exa mple.com/not valid"}`))
			},
		},
		"shall find indirect module location in the registry response header": {
			src:                  "exists-in-registry/identifier/provider",
			registryFlags:        []uint8{test.WithModuleLocationInHeader},
			want:                 makeIndirectLocation("file:///registry/exists", ""),
			wantToReadFromHeader: true,
			wantStatusCode:       http.StatusNoContent,
			handlerFunc: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Terraform-Get", "file:///registry/exists")
				w.WriteHeader(http.StatusNoContent)
			},
		},
		"shall read indirect location from the registry response body even if the header with location address is also set": {
			src:                  "exists-in-registry/identifier/provider",
			want:                 makeIndirectLocation("file:///registry/exists", ""),
			wantStatusCode:       http.StatusOK,
			wantToReadFromHeader: false,
			registryFlags:        []uint8{test.WithModuleLocationInBody, test.WithModuleLocationInHeader},
			handlerFunc: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Terraform-Get", "file:///registry/exists-header")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(response.ModuleLocationRegistryResp{Location: "file:///registry/exists"})
			},
		},
		"shall fail to find the module": {
			src: "not-exist/identifier/provider",
			// note that the version is fixed in the mock
			// see: /internal/registry/test/mock_registry.go:testMods
			wantErrorStr:   `module "registry.opentofu.org/not-exist/identifier/provider" version "0.2.0" not found`,
			wantStatusCode: http.StatusNotFound,
			handlerFunc: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
			},
		},
		"shall fail because of reading response body error": {
			src:            "foo/bar/baz",
			wantErrorStr:   "error reading response body from registry",
			wantStatusCode: http.StatusOK,
			handlerFunc: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Length", "1000") // Set incorrect content length
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("{")) // Only write a partial response
				// The connection will close after handler returns, but client will expect more data
			},
		},
		"shall fail to deserialize JSON response": {
			src:            "foo/bar/baz",
			wantErrorStr:   `module "registry.opentofu.org/foo/bar/baz" version "0.2.0" failed to deserialize 1-byte response body (content-type "text/plain; charset=utf-8"): unexpected end of JSON input`,
			wantStatusCode: http.StatusOK,
			handlerFunc: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("{"))
			},
		},
		"shall fail because of unexpected protocol change - 422 http status": {
			src:            "foo/bar/baz",
			wantErrorStr:   `error getting download location for "registry.opentofu.org/foo/bar/baz": 422 Unprocessable Entity (content-type "text/plain; charset=utf-8", 3 bytes)`,
			wantStatusCode: http.StatusUnprocessableEntity,
			handlerFunc: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = w.Write([]byte("bar"))
			},
		},
		"shall fail because location is not found in the response": {
			src:            "foo/bar/baz",
			wantErrorStr:   `registry did not return a location for this package`,
			wantStatusCode: http.StatusOK,
			handlerFunc: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				// note that the response emulates a contract change
				_, _ = w.Write([]byte(`{"foo":"git::https://github.com/foo/terraform-baz-bar?ref=v0.2.0"}`))
			},
		},
	}

	t.Parallel()
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			mockServer := httptest.NewServer(http.HandlerFunc(tc.handlerFunc))
			defer mockServer.Close()

			registryServer := test.Registry(tc.registryFlags...)
			defer registryServer.Close()

			transport := &testTransport{
				mockURL: mockServer.URL,
			}
			httpClient := retryablehttp.NewClient()
			httpClient.HTTPClient.Transport = transport
			client := NewClient(t.Context(), test.Disco(registryServer), httpClient)
			modsrc := testParseModulePackageAddr(t, tc.src)

			got, err := client.ModulePackageLocation(context.Background(), modsrc, "0.2.0", tc.subdir)

			// Validate the results
			if tc.wantErrorStr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got none (location: %#v)", tc.wantErrorStr, got)
				}
				if !strings.Contains(err.Error(), tc.wantErrorStr) {
					t.Fatalf("unexpected error content: want=%s, got=%v", tc.wantErrorStr, err)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Allow filling in the expected location now that we know the
			// test registry server's base URL, for the cases that need it.
			want := tc.want
			if tc.wantAgainstServer != nil {
				want = tc.wantAgainstServer(registryServer.URL)
			}

			if diff := cmp.Diff(want, got, cmp.AllowUnexported(PackageLocationDirect{})); diff != "" {
				t.Fatal("unexpected location\n" + diff)
			}

			// Verify status code if we have a successful response
			if transport.lastResponse != nil {
				gotStatusCode := transport.lastResponse.StatusCode
				if tc.wantStatusCode != gotStatusCode {
					t.Fatalf("unexpected response status code: want=%d, got=%d", tc.wantStatusCode, gotStatusCode)
				}

				// Check if we expected to read from header
				if tc.wantToReadFromHeader && err == nil {
					headerVal := transport.lastResponse.Header.Get("X-Terraform-Get")
					if headerVal == "" {
						t.Fatalf("expected to read location from header but X-Terraform-Get header was not set")
					}
				}
			}
		})
	}
}

// TestModuleLocationRegistryResp_useRegistryCredentialsUnmarshal tests the
// StrictBool semantics of the use_registry_credentials property directly,
// without going through the registry client. An explicit JSON null and an
// absent property are both covered by the pointer semantics (a nil pointer),
// while any other non-boolean token must be rejected.
func TestModuleLocationRegistryResp_useRegistryCredentialsUnmarshal(t *testing.T) {
	t.Parallel()

	boolVal := func(b bool) *response.StrictBool {
		v := response.StrictBool(b)
		return &v
	}

	cases := map[string]struct {
		body    string
		want    *response.StrictBool
		wantNil bool
		wantErr string
	}{
		"property absent": {
			body:    `{"location":"x"}`,
			wantNil: true,
		},
		"explicit null remains nil per pointer semantics": {
			body:    `{"location":"x","use_registry_credentials":null}`,
			wantNil: true,
		},
		"true": {
			body: `{"location":"x","use_registry_credentials":true}`,
			want: boolVal(true),
		},
		"false": {
			body: `{"location":"x","use_registry_credentials":false}`,
			want: boolVal(false),
		},
		"string literal rejected": {
			body:    `{"location":"x","use_registry_credentials":"true"}`,
			wantErr: "must be either true or false",
		},
		"number literal rejected": {
			body:    `{"location":"x","use_registry_credentials":1}`,
			wantErr: "must be either true or false",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var got response.ModuleLocationRegistryResp
			err := json.Unmarshal([]byte(tc.body), &got)

			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got none (UseRegistryCredentials: %#v)", tc.wantErr, got.UseRegistryCredentials)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("unexpected error content: want=%s, got=%v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tc.wantNil {
				if got.UseRegistryCredentials != nil {
					t.Fatalf("expected nil UseRegistryCredentials, got %#v", got.UseRegistryCredentials)
				}
				return
			}
			if diff := cmp.Diff(tc.want, got.UseRegistryCredentials); diff != "" {
				t.Error("unexpected UseRegistryCredentials\n" + diff)
			}
		})
	}

	t.Run("null directly into a non-pointer StrictBool is rejected", func(t *testing.T) {
		t.Parallel()

		// For a top-level JSON null, encoding/json calls UnmarshalJSON
		// directly on the Unmarshaler (unlike unmarshalling null into a
		// *StrictBool field, which the decoder resolves to nil), so a
		// non-pointer StrictBool must reject it rather than silently take
		// the zero value.
		var value response.StrictBool
		err := json.Unmarshal([]byte("null"), &value)
		if err == nil {
			t.Fatalf("expected error, got none (value: %#v)", bool(value))
		}
		if !strings.Contains(err.Error(), "must be either true or false") {
			t.Fatalf("unexpected error content: %v", err)
		}
	})
}

func testParseModulePackageAddr(t *testing.T, src string) regaddr.ModulePackage {
	sourceAddr, err := addrs.ParseModuleSourceRegistry(src)
	if err != nil {
		t.Fatalf("invalid module package address %q: %s", src, err)
	}
	// We used ParseModuleSourceRegistry and it didn't return an error, so this
	// type assertion should always succeed.
	registrySourceAddr := sourceAddr.(addrs.ModuleSourceRegistry)
	if registrySourceAddr.Subdir != "" {
		// Handling the "subdir" part of a source address is not part of
		// the registry client's scope: that must be handled by the main
		// module installer code after the registry client deals with
		// the module-package-level questions.
		t.Fatalf("invalid module package address %q: subdirectory part not allowed", src)
	}
	return registrySourceAddr.Package
}

// testTransport is a custom http.RoundTripper that redirects requests to the mock server
// and captures the response for inspection
type testTransport struct {
	mockURL string
	// Store the last response received from the mock server
	lastResponse *http.Response
}

func (t *testTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Create a new request to the mock server with the same path, method, body, etc.
	mockReq := &http.Request{
		Method: req.Method,
		URL: &url.URL{
			Scheme: "http",
			Host:   strings.TrimPrefix(t.mockURL, "http://"),
			Path:   req.URL.Path,
		},
		Header:     req.Header,
		Body:       req.Body,
		Host:       req.Host,
		Proto:      req.Proto,
		ProtoMajor: req.ProtoMajor,
		ProtoMinor: req.ProtoMinor,
	}

	// Send the request to the mock server
	resp, err := http.DefaultTransport.RoundTrip(mockReq)
	if err == nil {
		t.lastResponse = resp
	}
	return resp, err
}

// createTestZip writes a minimal zip archive with the given files. Files are
// stored uncompressed (zip.Store) so that the encoded archive size stays
// predictable and can exceed HTTP response buffering where needed.
func createTestZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, content := range files {
		f, err := w.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			t.Fatalf("failed to create file in zip: %v", err)
		}
		if _, err := f.Write([]byte(content)); err != nil {
			t.Fatalf("failed to write file content: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("failed to close zip writer: %v", err)
	}
	return buf.Bytes()
}

// createTestTarGz writes a minimal gzip-compressed tar archive with the
// given files.
func createTestTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name: name,
			Mode: 0o644,
			Size: int64(len(content)),
		}); err != nil {
			t.Fatalf("failed to write tar header: %v", err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("failed to write file content: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("failed to close tar writer: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("failed to close gzip writer: %v", err)
	}
	return buf.Bytes()
}

// createTestTarXz writes a minimal xz-compressed tar archive with the
// given files.
func createTestTarXz(t *testing.T, files map[string]string) ([]byte, error) {
	t.Helper()
	var buf bytes.Buffer
	xw, err := xz.NewWriter(&buf)
	if err != nil {
		return nil, err
	}
	tw := tar.NewWriter(xw)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name: name,
			Mode: 0o644,
			Size: int64(len(content)),
		}); err != nil {
			return nil, err
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := xw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

const testPackageBzip2Content = "# test module bzip2\n"

// testPackageBzip2 is a small tar.bz2 archive containing a single main.tf file
// with the content testPackageBzip2Content. The Go standard library cannot
// write bzip2 streams, so this fixture was produced once using the bzip2 CLI.
var testPackageBzip2 = mustDecodeBase64([]byte(`QlpoOTFBWSZTWT225AcAAK9/sP+4A4hYAf+iOmb/8P93/9AARA4AAIgwAU1ZmDJCmp5J7RT0Ro2o9AgPU0YRoGRgMoMjEI1JhI0BoA0NDQAAAAAAAA2pKmnptJ5SeptEGjQBpkAAGIAAB6Jdz+k5D/fBoR6kVxlIaagCUnaIf0KAHEhNdG0nFggrNZJOYAXhadnsI2E906RaHCUqZJCe24xWVMgK3lC8NqmSJUf85WAMQH1EAMpQxFp9nBbLLcG/l7+u39Sb9ZUwv+5cTz4O/qqWVBI56HJIicYyNW3NlGWhg6Jcxw1gWINmIkByQ6id6bycsgPQ3Jhk/bUDGHxDC4USQpNk4UcHSEJSFgcMhAQRLnYUk6DCA7OhZoM5cegnMObEMGXHjEj0nuEXnyZ06WLXPse+fwUXWqHWhsTR3TdLhg8DUE+BsMbxmoiI0NbswCQJy6jEdh10gKp3BhW+b1UOnb5Gc/P2pRoJiqTFFBEwkkCQzhq9ccVVNINApSi8jElT1Zh/kTmNv0ISwU92ZUpqWMaZJJZBdyRThQkD225AcA==`))

func mustDecodeBase64(src []byte) []byte {
	decoded, err := base64.StdEncoding.DecodeString(string(src))
	if err != nil {
		panic(fmt.Sprintf("invalid embedded test fixture: %s", err))
	}
	return decoded
}

// TestInstallModulePackage tests the InstallModulePackage method
func TestInstallModulePackage(t *testing.T) {
	t.Parallel()

	newLocation := func(t *testing.T, serverURL string, opts ...func(*PackageLocationDirect)) PackageLocationDirect {
		t.Helper()
		packageURL, err := url.Parse(serverURL + "/package.bin")
		if err != nil {
			t.Fatal(err)
		}
		location := PackageLocationDirect{
			packageAddr: testParseModulePackageAddr(t, "test/module/provider"),
			packageURL:  packageURL,
		}
		for _, opt := range opts {
			opt(&location)
		}
		return location
	}

	t.Run("successful zip download and extraction", func(t *testing.T) {
		zipContent := createTestZip(t, map[string]string{
			"main.tf": "# test module",
		})

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(zipContent)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(zipContent)
		}))
		defer server.Close()

		client := NewClient(t.Context(), nil, nil)
		targetDir := t.TempDir()

		modDir, err := client.InstallModulePackage(context.Background(), newLocation(t, server.URL), targetDir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if modDir != filepath.Clean(targetDir) {
			t.Errorf("expected modDir %q, got %q", targetDir, modDir)
		}

		content, err := os.ReadFile(filepath.Join(targetDir, "main.tf"))
		if err != nil {
			t.Fatalf("failed to read extracted file: %v", err)
		}
		if string(content) != "# test module" {
			t.Errorf("unexpected file content: %q", content)
		}
	})

	t.Run("tar.gz download and extraction", func(t *testing.T) {
		content := createTestTarGz(t, map[string]string{
			"main.tf": "# test module gzip",
		})

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(content)))
			_, _ = w.Write(content)
		}))
		defer server.Close()

		client := NewClient(t.Context(), nil, nil)
		targetDir := t.TempDir()

		if _, err := client.InstallModulePackage(context.Background(), newLocation(t, server.URL), targetDir); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got, err := os.ReadFile(filepath.Join(targetDir, "main.tf"))
		if err != nil {
			t.Fatalf("failed to read extracted file: %v", err)
		}
		if string(got) != "# test module gzip" {
			t.Errorf("unexpected file content: %q", got)
		}
	})

	t.Run("tar.xz download and extraction", func(t *testing.T) {
		content, err := createTestTarXz(t, map[string]string{
			"main.tf": "# test module xz",
		})
		if err != nil {
			t.Fatalf("failed to create tar.xz: %v", err)
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(content)))
			_, _ = w.Write(content)
		}))
		defer server.Close()

		client := NewClient(t.Context(), nil, nil)
		targetDir := t.TempDir()

		if _, err := client.InstallModulePackage(context.Background(), newLocation(t, server.URL), targetDir); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got, err := os.ReadFile(filepath.Join(targetDir, "main.tf"))
		if err != nil {
			t.Fatalf("failed to read extracted file: %v", err)
		}
		if string(got) != "# test module xz" {
			t.Errorf("unexpected file content: %q", got)
		}
	})

	t.Run("tar.bz2 download and extraction", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(testPackageBzip2)))
			_, _ = w.Write(testPackageBzip2)
		}))
		defer server.Close()

		client := NewClient(t.Context(), nil, nil)
		targetDir := t.TempDir()

		if _, err := client.InstallModulePackage(context.Background(), newLocation(t, server.URL), targetDir); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got, err := os.ReadFile(filepath.Join(targetDir, "main.tf"))
		if err != nil {
			t.Fatalf("failed to read extracted file: %v", err)
		}
		if string(got) != testPackageBzip2Content {
			t.Errorf("unexpected file content: %q", got)
		}
	})

	t.Run("Content-Length mismatch error", func(t *testing.T) {
		zipContent := createTestZip(t, map[string]string{
			"main.tf": "# test module",
		})

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Claim more bytes than we actually send
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(zipContent)+100))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(zipContent)
		}))
		defer server.Close()

		client := NewClient(t.Context(), nil, nil)
		targetDir := t.TempDir()

		_, err := client.InstallModulePackage(context.Background(), newLocation(t, server.URL), targetDir)
		if err == nil {
			t.Fatal("expected error for Content-Length mismatch")
		}
		// The error could be either a Content-Length mismatch or an EOF error
		// depending on how the HTTP client handles the truncated response
		if !strings.Contains(err.Error(), "server promised") && !strings.Contains(err.Error(), "EOF") {
			t.Errorf("expected Content-Length mismatch or EOF error, got: %v", err)
		}
	})

	t.Run("no Content-Length exactness check for chunked response", func(t *testing.T) {
		// An archive written without a Content-Length header, large enough to
		// exceed Go's response buffering, arrives with chunked encoding. The
		// exact-size check must be skipped in that case.
		zipContent := createTestZip(t, map[string]string{
			"main.tf": strings.Repeat("x", 64*1024),
		})

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// No Content-Length header: Go switches to chunked response encoding.
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(zipContent)
		}))
		defer server.Close()

		client := NewClient(t.Context(), nil, nil)
		targetDir := t.TempDir()

		if _, err := client.InstallModulePackage(context.Background(), newLocation(t, server.URL), targetDir); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("network failure handling", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Close connection immediately to simulate network failure
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Fatal("server doesn't support hijacking")
			}
			conn, _, err := hj.Hijack()
			if err != nil {
				t.Fatal(err)
			}
			conn.Close()
		}))
		defer server.Close()

		client := NewClient(t.Context(), nil, nil)
		targetDir := t.TempDir()

		_, err := client.InstallModulePackage(context.Background(), newLocation(t, server.URL), targetDir)
		if err == nil {
			t.Fatal("expected error for network failure")
		}
	})

	t.Run("request cancellation", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(createTestZip(t, map[string]string{"main.tf": "# test module"}))
		}))
		defer server.Close()

		client := NewClient(t.Context(), nil, nil)
		targetDir := t.TempDir()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := client.InstallModulePackage(ctx, newLocation(t, server.URL), targetDir)
		if err == nil {
			t.Fatal("expected error for cancelled request")
		}
		// retryablehttp doesn't wrap with %w on all retry paths, so match on
		// the message as well as the sentinel.
		if !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "canceled") {
			t.Errorf("expected cancellation error, got: %v", err)
		}
	})

	t.Run("subdirectory path handling", func(t *testing.T) {
		zipContent := createTestZip(t, map[string]string{
			"subdir/main.tf": "# test module in subdir",
		})

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(zipContent)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(zipContent)
		}))
		defer server.Close()

		client := NewClient(t.Context(), nil, nil)
		targetDir := t.TempDir()

		location := newLocation(t, server.URL, func(l *PackageLocationDirect) {
			l.subdir = "subdir"
		})

		modDir, err := client.InstallModulePackage(context.Background(), location, targetDir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		expectedModDir := filepath.Join(targetDir, "subdir")
		if modDir != expectedModDir {
			t.Errorf("expected modDir %q, got %q", expectedModDir, modDir)
		}

		// Verify the file was extracted to the subdirectory
		content, err := os.ReadFile(filepath.Join(modDir, "main.tf"))
		if err != nil {
			t.Fatalf("failed to read extracted file: %v", err)
		}
		if string(content) != "# test module in subdir" {
			t.Errorf("unexpected file content: %q", content)
		}
	})

	t.Run("HTTP error response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("not found"))
		}))
		defer server.Close()

		client := NewClient(t.Context(), nil, nil)
		targetDir := t.TempDir()

		_, err := client.InstallModulePackage(context.Background(), newLocation(t, server.URL), targetDir)
		if err == nil {
			t.Fatal("expected error for HTTP error response")
		}
		// The error should indicate a download failure due to the non-2xx HTTP status
		if !strings.Contains(err.Error(), "failed to download module package") {
			t.Errorf("expected download error, got: %v", err)
		}
	})

	t.Run("invalid archive format", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("this is not a valid archive"))
		}))
		defer server.Close()

		client := NewClient(t.Context(), nil, nil)
		targetDir := t.TempDir()

		_, err := client.InstallModulePackage(context.Background(), newLocation(t, server.URL), targetDir)
		if err == nil {
			t.Fatal("expected error for invalid archive format")
		}
		if !strings.Contains(err.Error(), "module package is not zip archive") {
			t.Errorf("expected archive format error, got: %v", err)
		}
	})
}

// TestInstallModulePackage_CredentialAttachment tests that credentials are
// correctly attached or omitted based on the useRegistryCredentials flag
func TestInstallModulePackage_CredentialAttachment(t *testing.T) {
	t.Parallel()

	newCredLocation := func(t *testing.T, packageURL string, useRegistryCredentials bool) PackageLocationDirect {
		t.Helper()
		return PackageLocationDirect{
			packageAddr: regaddr.ModulePackage{
				Host:         "registry.opentofu.org",
				Namespace:    "test",
				Name:         "module",
				TargetSystem: "provider",
			},
			packageURL:             mustParsePackageURL(t, packageURL),
			useRegistryCredentials: useRegistryCredentials,
		}
	}

	// tlsClientFor returns a client whose transport trusts the self-signed
	// certificates of all the given httptest TLS servers, so tests can use
	// https destinations.
	tlsClientFor := func(t *testing.T, services *disco.Disco, servers ...*httptest.Server) *Client {
		t.Helper()
		pool := x509.NewCertPool()
		for _, s := range servers {
			pool.AddCert(s.Certificate())
		}
		base := &http.Client{
			Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}},
		}
		retryable := retryablehttp.NewClient()
		retryable.HTTPClient = base
		return NewClient(t.Context(), services, retryable)
	}

	t.Run("credentials attached when useRegistryCredentials is true", func(t *testing.T) {
		// Credentials are only sent to https destinations, so the mock
		// package server must be an HTTPS server.
		zipContent := createTestZip(t, map[string]string{"main.tf": "# test"})
		var receivedAuthHeader string

		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			receivedAuthHeader = r.Header.Get("Authorization")
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(zipContent)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(zipContent)
		}))
		defer server.Close()

		// Create a disco with credentials using the test package's approach
		discoServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		defer discoServer.Close()
		d := test.Disco(discoServer)

		client := tlsClientFor(t, d, server)
		targetDir := t.TempDir()

		_, err := client.InstallModulePackage(context.Background(), newCredLocation(t, server.URL+"/package.zip", true), targetDir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if receivedAuthHeader == "" {
			t.Error("expected Authorization header to be set when useRegistryCredentials is true")
		}
		if !strings.Contains(receivedAuthHeader, "test-auth-token") {
			t.Errorf("expected Authorization header to contain token, got: %q", receivedAuthHeader)
		}
	})

	t.Run("credentials not attached when useRegistryCredentials is false", func(t *testing.T) {
		zipContent := createTestZip(t, map[string]string{"main.tf": "# test"})
		var receivedAuthHeader string

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			receivedAuthHeader = r.Header.Get("Authorization")
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(zipContent)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(zipContent)
		}))
		defer server.Close()

		// Create a disco with credentials using the test package's approach
		discoServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		defer discoServer.Close()
		d := test.Disco(discoServer)

		client := NewClient(t.Context(), d, nil)
		targetDir := t.TempDir()

		_, err := client.InstallModulePackage(context.Background(), newCredLocation(t, server.URL+"/package.zip", false), targetDir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if receivedAuthHeader != "" {
			t.Errorf("expected no Authorization header when useRegistryCredentials is false, got: %q", receivedAuthHeader)
		}
	})

	t.Run("credentials preserved when redirected to the same hostname", func(t *testing.T) {
		zipContent := createTestZip(t, map[string]string{"main.tf": "# test"})
		var firstAuthHeader, redirectedAuthHeader string

		// Both paths are on the same server, so the redirect hostname matches
		// the original hostname and net/http retains the Authorization header.
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/package.zip":
				firstAuthHeader = r.Header.Get("Authorization")
				http.Redirect(w, r, "/redirected.zip", http.StatusFound)
			case "/redirected.zip":
				redirectedAuthHeader = r.Header.Get("Authorization")
				w.Header().Set("Content-Length", fmt.Sprintf("%d", len(zipContent)))
				_, _ = w.Write(zipContent)
			}
		}))
		defer server.Close()

		// Create a disco with credentials using the test package's approach
		discoServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		defer discoServer.Close()
		d := test.Disco(discoServer)

		client := tlsClientFor(t, d, server)
		targetDir := t.TempDir()

		_, err := client.InstallModulePackage(context.Background(), newCredLocation(t, server.URL+"/package.zip", true), targetDir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(firstAuthHeader, "test-auth-token") {
			t.Errorf("expected Authorization header on the initial request, got: %q", firstAuthHeader)
		}
		if !strings.Contains(redirectedAuthHeader, "test-auth-token") {
			t.Errorf("expected Authorization header to be preserved on the same-host redirect, got: %q", redirectedAuthHeader)
		}
	})

	t.Run("credentials dropped when redirected to a different hostname", func(t *testing.T) {
		zipContent := createTestZip(t, map[string]string{"main.tf": "# test"})
		var redirectAuthHeader, finalAuthHeader string

		packageServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			finalAuthHeader = r.Header.Get("Authorization")
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(zipContent)))
			_, _ = w.Write(zipContent)
		}))
		defer packageServer.Close()

		// httptest servers always bind 127.0.0.1, but the redirect target
		// uses a different host:port than the origin, so the redirect
		// changes host and the Authorization header must be dropped.
		redirectServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			redirectAuthHeader = r.Header.Get("Authorization")
			http.Redirect(w, r, packageServer.URL+"/redirected.zip", http.StatusFound)
		}))
		defer redirectServer.Close()

		// Create a disco with credentials using the test package's approach
		discoServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		defer discoServer.Close()
		d := test.Disco(discoServer)

		client := tlsClientFor(t, d, redirectServer, packageServer)
		targetDir := t.TempDir()

		_, err := client.InstallModulePackage(context.Background(), newCredLocation(t, redirectServer.URL+"/package.zip", true), targetDir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(redirectAuthHeader, "test-auth-token") {
			t.Errorf("expected Authorization header on the initial request, got: %q", redirectAuthHeader)
		}
		if finalAuthHeader != "" {
			t.Errorf("expected Authorization header to be dropped on the redirected request, got: %q", finalAuthHeader)
		}
	})

	// The requests are performed in order, so each hop's handler runs to
	// completion before the next request is issued; assertions are made
	// directly in the handlers to avoid sharing state with the test
	// goroutine.
	t.Run("credentials preserved across a multi-hop same-host redirect chain", func(t *testing.T) {
		zipContent := createTestZip(t, map[string]string{"main.tf": "# test"})

		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/package.zip":
				http.Redirect(w, r, "/hop1.zip", http.StatusFound)
			case "/hop1.zip":
				if auth := r.Header.Get("Authorization"); !strings.Contains(auth, "test-auth-token") {
					t.Errorf("expected Authorization header on the first redirect hop, got %q", auth)
				}
				http.Redirect(w, r, "/final.zip", http.StatusFound)
			case "/final.zip":
				if auth := r.Header.Get("Authorization"); !strings.Contains(auth, "test-auth-token") {
					t.Errorf("expected Authorization header on the final redirect hop, got %q", auth)
				}
				w.Header().Set("Content-Length", fmt.Sprintf("%d", len(zipContent)))
				_, _ = w.Write(zipContent)
			default:
				t.Errorf("unexpected request path %q", r.URL.Path)
			}
		}))
		defer server.Close()

		discoServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		defer discoServer.Close()
		d := test.Disco(discoServer)

		client := tlsClientFor(t, d, server)
		targetDir := t.TempDir()

		if _, err := client.InstallModulePackage(context.Background(), newCredLocation(t, server.URL+"/package.zip", true), targetDir); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got, err := os.ReadFile(filepath.Join(targetDir, "main.tf")); err != nil || string(got) != "# test" {
			t.Errorf("expected downloaded module file content %q, got %q (err: %v)", "# test", got, err)
		}
	})

	t.Run("no credentials sent to an insecure http destination", func(t *testing.T) {
		// Credentials must only be attached to https destinations, so a
		// registry that reports an http package URL must not receive the
		// registry's credentials.
		zipContent := createTestZip(t, map[string]string{"main.tf": "# test"})
		var receivedAuthHeader string

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			receivedAuthHeader = r.Header.Get("Authorization")
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(zipContent)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(zipContent)
		}))
		defer server.Close()

		discoServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		defer discoServer.Close()
		d := test.Disco(discoServer)

		client := NewClient(t.Context(), d, nil)
		targetDir := t.TempDir()

		_, err := client.InstallModulePackage(context.Background(), newCredLocation(t, server.URL+"/package.zip", true), targetDir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if receivedAuthHeader != "" {
			t.Errorf("expected no Authorization header for an http destination, got: %q", receivedAuthHeader)
		}
	})

	t.Run("refuses to follow an https to http downgrade redirect", func(t *testing.T) {
		// A redirect from an https package location to an http destination
		// would send the registry's credentials over an insecure connection,
		// so the download must be refused.
		downgradeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Errorf("request unexpectedly reached the insecure http destination")
		}))
		defer downgradeServer.Close()

		zipContent := createTestZip(t, map[string]string{"main.tf": "# test"})
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, downgradeServer.URL+"/redirected.zip", http.StatusFound)
		}))
		defer server.Close()

		// The archive is never fetched in this test, but we need a valid
		// response in case the redirect were followed despite the policy.
		_ = zipContent

		// Create a disco with credentials using the test package's approach
		discoServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		defer discoServer.Close()
		d := test.Disco(discoServer)

		client := tlsClientFor(t, d, server)
		targetDir := t.TempDir()

		_, err := client.InstallModulePackage(context.Background(), newCredLocation(t, server.URL+"/package.zip", true), targetDir)
		if err == nil {
			t.Fatal("expected error for https to http downgrade redirect")
		}
		if !strings.Contains(err.Error(), "would send registry credentials over an insecure connection") {
			t.Errorf("expected insecure-connection downgrade error, got: %v", err)
		}
	})
}
func mustParsePackageURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// Credential material used to detect leaks of package URL secrets into
// user-facing strings. The username deliberately has no password, like the
// userinfo of many "presigned" URL mechanisms, which net/http's own error
// rendering does not redact.
const (
	testSecretUser       = "sentinel-secret-user"                          // bare-username userinfo
	testSecretQueryParam = "X-Amz-Signature=" + "sentinel-signature-token" // URL query credentials
	testSecretURLHost    = "https://example.invalid/package.zip?" + testSecretQueryParam
)

// makeSecretPackageURL returns a URL that embeds credentials in the userinfo
// and query string, like real-world presigned package URLs.
func makeSecretPackageURL(raw string) *url.URL {
	u, _ := url.Parse(raw)
	u.User = url.User(testSecretUser)
	return u
}

func assertNoURLSecrets(t *testing.T, where string, got string) {
	t.Helper()
	for _, secret := range []string{testSecretUser, testSecretQueryParam} {
		if strings.Contains(got, secret) {
			t.Errorf("%s leaked URL credential %q in %q", where, secret, got)
		}
	}
}

// TestPackageLocationUILabelRedactsSecrets checks that UI labels for direct
// package locations never include credentials from the package URL.
func TestPackageLocationUILabelRedactsSecrets(t *testing.T) {
	t.Parallel()

	loc := PackageLocationDirect{
		packageAddr:            testParseModulePackageAddr(t, "test/module/provider"),
		packageURL:             makeSecretPackageURL(testSecretURLHost),
		useRegistryCredentials: false,
	}

	got := loc.UILabel()
	assertNoURLSecrets(t, "UILabel", got)
	// The non-secret parts of the URL should still be present so that the
	// label remains useful for a human reader.
	if !strings.Contains(got, "example.invalid/package.zip") {
		t.Errorf("UILabel should keep the non-secret URL parts, got %q", got)
	}
}

// TestInstallModulePackage_RedactsSecretsFromErrors checks that errors from a
// direct package download don't include the package URL's credentials.
func TestInstallModulePackage_RedactsSecretsFromErrors(t *testing.T) {
	t.Parallel()

	// The server is closed immediately so that the download fails with a
	// transport error, whose text would otherwise embed the full request URL
	// (with userinfo and query string).
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	packageURL := makeSecretPackageURL(server.URL + "/package.zip?" + testSecretQueryParam)
	server.Close()

	client := NewClient(t.Context(), nil, nil)
	targetDir := t.TempDir()
	location := PackageLocationDirect{
		packageAddr:            testParseModulePackageAddr(t, "test/module/provider"),
		packageURL:             packageURL,
		useRegistryCredentials: true,
	}

	_, err := client.InstallModulePackage(context.Background(), location, targetDir)
	if err == nil {
		t.Fatal("expected error for unreachable package server")
	}
	got := err.Error()
	assertNoURLSecrets(t, "install error", got)

	// The non-secret path should still appear; the query string must be
	// completely gone.
	if !strings.Contains(got, "/package.zip") {
		t.Errorf("expected the redacted URL path in the error, got %q", got)
	}
	if strings.Contains(got, "?") {
		// the query must not merely be mangled
		t.Errorf("install error seems to include a query string: %q", got)
	}
}

// TestInstallModulePackage_PreservesSignedURL checks that the download
// request itself is built from the full signed package URL: the query string
// and userinfo of the package URL can carry the credentials needed to
// authorize the download, and must only be redacted from logs and errors,
// never from the request.
func TestInstallModulePackage_PreservesSignedURL(t *testing.T) {
	t.Parallel()

	zipContent := createTestZip(t, map[string]string{"main.tf": "# test signed"})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/package.zip" {
			t.Errorf("unexpected request path %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("X-Amz-Signature"); got != "sentinel-signature-token" {
			t.Errorf("server did not receive the signed query parameter, got query %q", r.URL.RawQuery)
		}
		// net/http turns userinfo in the request URL into HTTP Basic auth.
		if user, pass, ok := r.BasicAuth(); !ok || user != "presignuser" || pass != "presignpass" {
			t.Errorf("server did not receive the userinfo credentials, got user=%q ok=%v", user, ok)
		}
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(zipContent)))
		_, _ = w.Write(zipContent)
	}))
	defer server.Close()

	packageURL := makeSecretPackageURL(server.URL + "/package.zip?" + testSecretQueryParam)
	packageURL.User = url.UserPassword("presignuser", "presignpass")

	client := NewClient(t.Context(), nil, nil)
	targetDir := t.TempDir()
	location := PackageLocationDirect{
		packageAddr: testParseModulePackageAddr(t, "test/module/provider"),
		packageURL:  packageURL,
	}

	modDir, err := client.InstallModulePackage(context.Background(), location, targetDir)
	if err != nil {
		t.Fatalf("expected signed URL download to succeed: %v", err)
	}
	if modDir != filepath.Clean(targetDir) {
		t.Errorf("expected modDir %q, got %q", targetDir, modDir)
	}
	if got, err := os.ReadFile(filepath.Join(targetDir, "main.tf")); err != nil || string(got) != "# test signed" {
		t.Errorf("expected extracted file content %q, got %q (err: %v)", "# test signed", got, err)
	}
}

// TestInstallModulePackage_RedactsSecretsFromRedirectErrors checks that
// neither the returned error nor the log output leaks the credentials
// (query string) of a signed redirect target, even when the redirect
// request itself fails at the transport level.
func TestInstallModulePackage_RedactsSecretsFromRedirectErrors(t *testing.T) {
	// Not parallel: this test temporarily captures the global log output.
	// (Go runs non-parallel tests to completion before resuming parallel
	// ones, so no other test's logging is captured here.)
	const redirectSecret = "X-Amz-Signature=redirect-hop-secret-token"

	var logs bytes.Buffer
	prevOutput := log.Writer()
	prevFlags := log.Flags()
	log.SetOutput(&logs)
	t.Cleanup(func() {
		log.SetFlags(prevFlags)
		log.SetOutput(prevOutput)
	})

	// The redirect target closes the connection, producing a transport
	// error whose text would otherwise embed the full signed redirect URL.
	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("server doesn't support hijacking")
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Fatal(err)
		}
		conn.Close()
	}))
	defer targetServer.Close()

	packageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, targetServer.URL+"/redirected.zip?"+redirectSecret, http.StatusFound)
	}))
	defer packageServer.Close()

	client := NewClient(t.Context(), nil, nil)
	targetDir := t.TempDir()
	location := PackageLocationDirect{
		packageAddr: testParseModulePackageAddr(t, "test/module/provider"),
		packageURL:  mustParsePackageURL(t, packageServer.URL+"/package.zip"),
	}

	_, err := client.InstallModulePackage(context.Background(), location, targetDir)
	if err == nil {
		t.Fatal("expected transport error from the closed redirect target")
	}

	got := err.Error()
	if strings.Contains(got, redirectSecret) {
		t.Errorf("install error leaked the signed redirect query: %q", got)
	}
	if !strings.Contains(got, "/redirected.zip") {
		t.Errorf("expected the redacted redirect path in the error, got %q", got)
	}
	if captured := logs.String(); strings.Contains(captured, redirectSecret) {
		t.Errorf("log output leaked the signed redirect query: %q", captured)
	}
}

// TestInstallModulePackage_GzipEncodedResponse checks that a server that
// gzip-encodes the package response is handled: relying on net/http's
// transparent decompression means we see the decoded archive bytes, not the
// raw gzip stream.
func TestInstallModulePackage_GzipEncodedResponse(t *testing.T) {
	t.Parallel()

	zipContent := createTestZip(t, map[string]string{"main.tf": "# test module gzip"})
	var gzipped bytes.Buffer
	zw := gzip.NewWriter(&gzipped)
	if _, err := zw.Write(zipContent); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Length", fmt.Sprintf("%d", gzipped.Len()))
		_, _ = w.Write(gzipped.Bytes())
	}))
	defer server.Close()

	client := NewClient(t.Context(), nil, nil)
	targetDir := t.TempDir()

	packageURL, err := url.Parse(server.URL + "/package.zip")
	if err != nil {
		t.Fatal(err)
	}
	location := PackageLocationDirect{
		packageAddr: testParseModulePackageAddr(t, "test/module/provider"),
		packageURL:  packageURL,
	}

	if _, err := client.InstallModulePackage(context.Background(), location, targetDir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(targetDir, "main.tf"))
	if err != nil {
		t.Fatalf("failed to read extracted file: %v", err)
	}
	if string(got) != "# test module gzip" {
		t.Errorf("unexpected file content: %q", got)
	}
}

// TestInstallModulePackage_SlowBody checks that the package download doesn't
// inherit the (much shorter) registry metadata client's deadline for reading
// the response body.
func TestInstallModulePackage_SlowBody(t *testing.T) {
	t.Parallel()

	zipContent := createTestZip(t, map[string]string{"main.tf": "# test module slow"})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(zipContent)))
		w.WriteHeader(http.StatusOK)
		// Drip the archive out slowly, in two parts, pausing longer than a
		// registry metadata request's timeout.
		if _, err := w.Write(zipContent[:len(zipContent)/2]); err != nil {
			return
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		time.Sleep(600 * time.Millisecond)
		_, _ = w.Write(zipContent[len(zipContent)/2:])
	}))
	defer server.Close()

	// A client whose registry metadata timeout is shorter than the time the
	// server needs to finish writing the response body.
	client := NewClient(t.Context(), nil, httpclient.NewForRegistryRequests(t.Context(), 0, 200*time.Millisecond))
	targetDir := t.TempDir()

	packageURL, err := url.Parse(server.URL + "/package.zip")
	if err != nil {
		t.Fatal(err)
	}
	location := PackageLocationDirect{
		packageAddr: testParseModulePackageAddr(t, "test/module/provider"),
		packageURL:  packageURL,
	}

	if _, err := client.InstallModulePackage(context.Background(), location, targetDir); err != nil {
		t.Fatalf("expected slow download to complete within the package deadline: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(targetDir, "main.tf"))
	if err != nil {
		t.Fatalf("failed to read extracted file: %v", err)
	}
	if string(got) != "# test module slow" {
		t.Errorf("unexpected file content: %q", got)
	}
}

// tlsRegistryClientFor returns a registry Client whose transport trusts the
// self-signed certificates of the given httptest TLS servers.
func tlsRegistryClientFor(t *testing.T, services *disco.Disco, servers ...*httptest.Server) *Client {
	t.Helper()
	pool := x509.NewCertPool()
	for _, s := range servers {
		pool.AddCert(s.Certificate())
	}
	base := &http.Client{
		Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}},
	}
	retryable := retryablehttp.NewClient()
	retryable.HTTPClient = base
	return NewClient(t.Context(), services, retryable)
}

// directLocationDisco returns a disco whose modules.v1 service for
// "example.com" points at the given metadata URL and whose credentials for
// that host carry the test token.
func directLocationDisco(metadataURL string) *disco.Disco {
	d := disco.New(disco.WithCredentials(svcauth.StaticCredentialsSource(map[svchost.Hostname]svcauth.HostCredentials{
		"example.com": svcauth.HostCredentialsToken("direct-location-token"),
	})))
	d.ForceHostServices(svchost.Hostname("example.com"), map[string]interface{}{
		"modules.v1": metadataURL,
	})
	return d
}

// TestModulePackageLocation_CredentialsOnlyOverHTTPSMetadata checks that a
// direct package location reported by registry metadata is only allowed to
// reuse the registry's credentials when that metadata was fetched over
// https: over plain http, a man-in-the-middle attacker controls the
// returned Location URL and could steal the registry's credentials by
// pointing it at their own server.
func TestModulePackageLocation_CredentialsOnlyOverHTTPSMetadata(t *testing.T) {
	t.Parallel()
	zipContent := createTestZip(t, map[string]string{"main.tf": "# metadata-scheme test"})

	t.Run("credentials reused when the metadata was fetched over https", func(t *testing.T) {
		var packageAuth string
		// The https registry reports a direct location and serves the
		// package. Its location points at its own host, a distinct https
		// authority from the metadata endpoint's host:port pair, since the
		// rules under test change only with the metadata URL's scheme.
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case strings.HasSuffix(r.URL.Path, "/download"):
				if got := r.Header.Get("Authorization"); !strings.Contains(got, "direct-location-token") {
					t.Errorf("expected registry credentials on the https metadata request, got %q", got)
				}
				_, _ = fmt.Fprintf(w, `{"location":"https://%s/package.zip","use_registry_credentials":true}`, r.Host)
			case strings.HasSuffix(r.URL.Path, "/package.zip"):
				packageAuth = r.Header.Get("Authorization")
				w.Header().Set("Content-Length", fmt.Sprintf("%d", len(zipContent)))
				_, _ = w.Write(zipContent)
			default:
				t.Errorf("unexpected request path %q", r.URL.Path)
			}
		}))
		defer server.Close()

		client := tlsRegistryClientFor(t, directLocationDisco(server.URL+"/v1/modules/"), server)
		modsrc := testParseModulePackageAddr(t, "example.com/creds-test/metadata-scheme/provider")

		loc, err := client.ModulePackageLocation(context.Background(), modsrc, "1.2.3", "")
		if err != nil {
			t.Fatal(err)
		}
		direct, ok := loc.(PackageLocationDirect)
		if !ok {
			t.Fatalf("expected a direct location, got %T", loc)
		}
		if !direct.useRegistryCredentials {
			t.Fatal("expected registry credentials to be reused for https metadata")
		}

		if _, err := client.InstallModulePackage(context.Background(), direct, t.TempDir()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(packageAuth, "direct-location-token") {
			t.Errorf("expected the package download to carry the registry credentials, got %q", packageAuth)
		}
	})

	t.Run("credentials withheld when the metadata was fetched over http", func(t *testing.T) {
		// The package host is a different https authority, standing in for an
		// attacker-chosen destination; it records whether it received the
		// registry's credentials.
		var evilAuthHeader string
		evilServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			evilAuthHeader = r.Header.Get("Authorization")
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(zipContent)))
			_, _ = w.Write(zipContent)
		}))
		defer evilServer.Close()

		// The registry reports its location over plain http.
		metadataServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = fmt.Fprintf(w, `{"location":%q,"use_registry_credentials":true}`, evilServer.URL+"/package.zip")
		}))
		defer metadataServer.Close()

		client := tlsRegistryClientFor(t, directLocationDisco(metadataServer.URL+"/v1/modules/"), evilServer)
		modsrc := testParseModulePackageAddr(t, "example.com/creds-test/metadata-scheme/provider")

		loc, err := client.ModulePackageLocation(context.Background(), modsrc, "1.2.3", "")
		if err != nil {
			t.Fatal(err)
		}
		direct, ok := loc.(PackageLocationDirect)
		if !ok {
			t.Fatalf("expected a direct location, got %T", loc)
		}
		if direct.useRegistryCredentials {
			t.Fatal("expected registry credentials to be withheld for http metadata")
		}

		// The download still proceeds (without credentials), proving the
		// attacker's https server was contacted but did not receive the
		// registry's credentials.
		if _, err := client.InstallModulePackage(context.Background(), direct, t.TempDir()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if evilAuthHeader != "" {
			t.Errorf("expected no Authorization header sent to an attacker-chosen https host over http metadata, got %q", evilAuthHeader)
		}
	})
}

// TestInstallModulePackage_RetriesTransientFailures checks that direct
// package downloads still use the retryable HTTP client (e.g. retry 5xx
// responses), rather than a plain HTTP client.
func TestInstallModulePackage_RetriesTransientFailures(t *testing.T) {
	t.Parallel()
	zipContent := createTestZip(t, map[string]string{"main.tf": "# retried download"})

	var attempts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			http.Error(w, "try again later", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(zipContent)))
		_, _ = w.Write(zipContent)
	}))
	defer server.Close()

	client := NewClient(t.Context(), nil, nil) // fallback policy: one retry per request
	// Speed up the retry backoff for this test.
	client.client.RetryWaitMin = time.Millisecond
	client.client.RetryWaitMax = 10 * time.Millisecond

	location := PackageLocationDirect{
		packageAddr: testParseModulePackageAddr(t, "test/module/provider"),
		packageURL:  mustParsePackageURL(t, server.URL+"/package.zip"),
	}
	if _, err := client.InstallModulePackage(context.Background(), location, t.TempDir()); err != nil {
		t.Fatalf("expected the download to succeed on retry: %v", err)
	}
	if attempts != 2 {
		t.Errorf("expected the download request to be retried, got %d attempts", attempts)
	}
}

// TestPreparePackageLocationDirect_RedactsInvalidLocations checks that
// errors about invalid registry-provided package locations do not echo the
// raw location text, which can carry credentials.
func TestPreparePackageLocationDirect_RedactsInvalidLocations(t *testing.T) {
	t.Parallel()
	const secret = "invalid-location-secret-token"

	baseURL, err := url.Parse("https://registry.example.com/v1/modules/")
	if err != nil {
		t.Fatal(err)
	}
	packageAddr := testParseModulePackageAddr(t, "example.com/name/invalid-location/provider")

	cases := map[string]struct {
		locationRaw   string
		wantErrorPart string
	}{
		"unparseable userinfo": {"https://user:" + secret + "%zz@example.com/package.zip", "registry returned an invalid package URL"},
		"unparseable path":     {"https://example.com/pkg%zz?sig=" + secret, "registry returned an invalid package URL"},
		"unsupported scheme":   {"ftp://files.example.com/package.zip?sig=" + secret, "must be http or https URL"},
		"fragment":             {"https://files.example.com/package.zip#sig=" + secret, "must not include fragment part"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := preparePackageLocationDirect(tc.locationRaw, packageAddr, "", baseURL, false)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.wantErrorPart) {
				t.Errorf("expected error containing %q, got %q", tc.wantErrorPart, err.Error())
			}
			if strings.Contains(err.Error(), secret) {
				t.Errorf("error echoed the secret-carrying location: %q", err.Error())
			}
		})
	}
}

// TestRedactURLError_UnparseableHopURL checks that URLs which fail to parse
// in embedded transport errors are redacted wholesale, so that credential
// material from an unparseable redirect hop is never echoed.
func TestRedactURLError_UnparseableHopURL(t *testing.T) {
	t.Parallel()
	const secret = "hop-url-secret-token"

	base, err := url.Parse("https://registry.example.com/package.zip")
	if err != nil {
		t.Fatal(err)
	}
	err = &url.Error{
		Op:  "Get",
		URL: "https://registry.example.com/package.zip",
		Err: &url.Error{
			Op:  "Get",
			URL: "https://user:" + secret + "%zz@files.example.com/hop.zip",
			Err: errors.New("connection reset"),
		},
	}
	got := redactURLError(base, err).Error()
	if strings.Contains(got, secret) {
		t.Errorf("error echoed the unparseable hop URL's secret: %q", got)
	}
}

// TestInstallModulePackage_RedactsRefererOnCrossHostRedirect checks that the
// redirect policy removes the Referer header that net/http adds on each
// redirect hop, so that a cross-host redirect destination never receives the
// signed query string (or userinfo) of the original request in any header.
func TestInstallModulePackage_RedactsRefererOnCrossHostRedirect(t *testing.T) {
	t.Parallel()
	const origSecret = "orig-sig=referer-orig-secret-token"
	const redirectSecret = "redirect-sig=referer-redirect-secret-token"
	const secretPassword = "referer-presign-pass"

	zipContent := createTestZip(t, map[string]string{"main.tf": "# referer test"})

	var targetHeaders http.Header
	targetServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHeaders = r.Header
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(zipContent)))
		_, _ = w.Write(zipContent)
	}))
	defer targetServer.Close()

	packageServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, targetServer.URL+"/package.zip?"+redirectSecret, http.StatusFound)
	}))
	defer packageServer.Close()

	client := tlsRegistryClientFor(t, nil, packageServer, targetServer)

	// The original request carries credential material both in its userinfo
	// (sent as the Authorization header after userinfo processing) and its
	// signed query string, both of which net/http would echo to the redirect
	// destination in the Referer header without the redaction.
	packageURL := mustParsePackageURL(t, packageServer.URL+"/package.zip")
	packageURL.User = url.UserPassword("presignuser", secretPassword)
	packageURL.RawQuery = origSecret
	location := PackageLocationDirect{
		packageAddr: testParseModulePackageAddr(t, "test/module/provider"),
		packageURL:  packageURL,
	}

	if _, err := client.InstallModulePackage(context.Background(), location, t.TempDir()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for key, vals := range targetHeaders {
		for _, val := range vals {
			for _, leak := range []string{origSecret, redirectSecret, secretPassword, "presignuser"} {
				if strings.Contains(val, leak) {
					t.Errorf("redirect destination received header %q containing credential material %q: %q", key, leak, val)
				}
			}
		}
	}
	// The redirect itself should still have happened and served the package.
	if targetHeaders == nil {
		t.Fatal("the redirect destination was never contacted")
	}
}

// TestRedactURLError_RedirectLocationParseError checks that when net/http
// fails to parse a redirect Location header (e.g. one containing an invalid
// percent escape), the returned error does not echo the Location's
// credential-bearing query string: net/http flattens the parse error into
// text, so the usual scrubbing can't reach it and the message must be
// sanitized generically.
func TestInstallModulePackage_RedactsUnparseableRedirectLocation(t *testing.T) {
	testcases := map[string]struct {
		location string
		secret   string
	}{
		"query": {
			location: "https://files.example/p%zz?sig=secret-query-token",
			secret:   "secret-query-token",
		},
		"scheme-relative userinfo": {
			// Scheme-relative credentials have no "://" or "?" for the
			// residual-span heuristic to match, so they must still be
			// treated as URL-like and sanitized generically.
			location: "//user:secret-urlencoded-userinfo@files.example/p%zz",
			secret:   "secret-urlencoded-userinfo",
		},
		"quote-escaped span": {
			// The %q-quoted residual contains \" before the URL, which must not
			// close the quoted span early and hide the URL-like text.
			location: `/p%zz"https://files.example/p?sig=secret-quoted-sig`,
			secret:   "secret-quoted-sig",
		},
		"fragment": {
			location: "/p%zz#access_token=secret-fragment-token",
			secret:   "secret-fragment-token",
		},
	}
	for name, tc := range testcases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			packageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, tc.location, http.StatusFound)
			}))
			defer packageServer.Close()

			client := NewClient(t.Context(), nil, nil)
			location := PackageLocationDirect{
				packageAddr: testParseModulePackageAddr(t, "test/module/provider"),
				packageURL:  mustParsePackageURL(t, packageServer.URL+"/package.zip"),
			}

			_, err := client.InstallModulePackage(context.Background(), location, t.TempDir())
			if err == nil {
				t.Fatal("expected error for the unparseable redirect Location")
			}
			if got := err.Error(); strings.Contains(got, tc.secret) {
				t.Errorf("error echoed the unparseable Location's secret: %q", got)
			}
		})
	}
}

// TestInstallModulePackage_RedactsFragmentOnRedirect checks that a redirect
// to "/package.zip#access_token=..." whose target then fails keeps the
// OAuth-style fragment secret out of the returned error, on top of the
// span-attribute guarantee tested in the httpclient package.
func TestInstallModulePackage_RedactsFragmentOnRedirect(t *testing.T) {
	t.Parallel()
	const secret = "fragment-redirect-secret-token"

	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Error("server doesn't support hijacking")
			return
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Errorf("hijack failed: %v", err)
			return
		}
		conn.Close()
	}))
	defer targetServer.Close()

	packageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/package.zip#access_token="+secret, http.StatusFound)
	}))
	defer packageServer.Close()

	client := NewClient(t.Context(), nil, nil)
	targetDir := t.TempDir()
	location := PackageLocationDirect{
		packageAddr: testParseModulePackageAddr(t, "test/module/provider"),
		packageURL:  mustParsePackageURL(t, packageServer.URL+"/download"),
	}

	_, err := client.InstallModulePackage(context.Background(), location, targetDir)
	if err == nil {
		t.Fatal("expected transport error from the closed redirect target")
	}
	if got := err.Error(); strings.Contains(got, secret) || strings.Contains(got, "/package.zip#") {
		t.Errorf("install error leaked the credential-bearing fragment: %q", got)
	}
}

// TestModulePackageLocation_DoesNotEchoBodyInDeserializationErrors checks
// that deserialization errors don't include the raw response body: a
// registry-controlled body can carry credential-bearing location URLs.
func TestModulePackageLocation_DoesNotEchoBodyInDeserializationErrors(t *testing.T) {
	t.Parallel()
	// A credential-bearing URL embedded in an invalid JSON body.
	body := "not json: https://user:pw@registry.example.com/x?sig=body-deserialize-secret-token"

	metadataServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
		_, _ = w.Write([]byte(body))
	}))
	defer metadataServer.Close()

	client := NewClient(t.Context(), directLocationDisco(metadataServer.URL+"/v1/modules/"), nil)
	modsrc := testParseModulePackageAddr(t, "example.com/body-echo/creds/provider")

	_, err := client.ModulePackageLocation(context.Background(), modsrc, "1.2.3", "")
	if err == nil {
		t.Fatal("expected a deserialization error for the invalid body")
	}
	got := err.Error()
	for _, leak := range []string{"pw", "body-deserialize-secret-token"} {
		if strings.Contains(got, leak) {
			t.Errorf("deserialization error echoed credential body material (%q): %q", leak, got)
		}
	}
}
