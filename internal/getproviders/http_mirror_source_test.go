// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package getproviders

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/apparentlymart/go-versions/versions"
	"github.com/google/go-cmp/cmp"
	"github.com/hashicorp/go-retryablehttp"
	"github.com/opentofu/svchost"
	"github.com/opentofu/svchost/svcauth"
	"github.com/vmvarela/ghoten/internal/httpclient"

	"github.com/vmvarela/ghoten/internal/addrs"
)

func TestHTTPMirrorSource(t *testing.T) {
	// For mirrors we require a HTTPS server, so we'll use httptest to create
	// one. However, that means we need to instantiate the source in an unusual
	// way to force it to use the test client that is configured to trust the
	// test server.
	httpServer := httptest.NewTLSServer(http.HandlerFunc(testHTTPMirrorSourceHandler))
	defer httpServer.Close()
	httpClient := httpServer.Client()
	baseURL, err := url.Parse(httpServer.URL)
	if err != nil {
		t.Fatalf("httptest.NewTLSServer returned a server with an invalid URL")
	}
	creds := svcauth.StaticCredentialsSource(map[svchost.Hostname]svcauth.HostCredentials{
		svchost.Hostname(baseURL.Host): svcauth.HostCredentialsToken("placeholder-token"),
	})
	retryHTTPClient := retryablehttp.NewClient()
	retryHTTPClient.HTTPClient = httpClient
	source := newHTTPMirrorSourceWithHTTPClient(baseURL, creds, retryHTTPClient, LocationConfig{})

	existingProvider := addrs.MustParseProviderSourceString("terraform.io/test/exists")
	missingProvider := addrs.MustParseProviderSourceString("terraform.io/test/missing")
	failingProvider := addrs.MustParseProviderSourceString("terraform.io/test/fails")
	redirectingProvider := addrs.MustParseProviderSourceString("terraform.io/test/redirects")
	redirectLoopProvider := addrs.MustParseProviderSourceString("terraform.io/test/redirect-loop")
	tosPlatform := Platform{OS: "tos", Arch: "m68k"}

	clientBuilderFromHTTPLocation := func(t *testing.T, expectedRetries int) func(ctx context.Context) *retryablehttp.Client {
		return func(ctx context.Context) *retryablehttp.Client {
			return packageHTTPUrlClientWithRetry(ctx, expectedRetries)
		}
	}
	// For the PackageHTTPURL.ClientBuilder we are interested strictly in comparing the max retries and nothing else.
	// Comparing the whole client gets into different issues so we keep this comparison simple.
	cmpClientBuilder := cmp.Comparer(func(a, b func(ctx context.Context) *retryablehttp.Client) bool {
		given := a(t.Context())
		got := b(t.Context())
		if got.RetryMax != given.RetryMax {
			t.Logf("expected to have retry max as %d but got %d", got.RetryMax, given.RetryMax)
		}
		return true
	})

	t.Run("AvailableVersions for provider that exists", func(t *testing.T) {
		got, _, err := source.AvailableVersions(context.Background(), existingProvider)
		if err != nil {
			t.Fatalf("unexpected error: %s", err)
		}
		want := VersionList{
			MustParseVersion("1.0.0"),
			MustParseVersion("1.0.1"),
			MustParseVersion("1.0.2-beta.1"),
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("wrong result\n%s", diff)
		}
	})
	t.Run("AvailableVersions for provider that doesn't exist", func(t *testing.T) {
		_, _, err := source.AvailableVersions(context.Background(), missingProvider)
		switch err := err.(type) {
		case ErrProviderNotFound:
			if got, want := err.Provider, missingProvider; got != want {
				t.Errorf("wrong provider in error\ngot:  %s\nwant: %s", got, want)
			}
		default:
			t.Fatalf("wrong error type %T; want ErrProviderNotFound", err)
		}
	})
	t.Run("AvailableVersions without required credentials", func(t *testing.T) {
		unauthSource := newHTTPMirrorSourceWithHTTPClient(baseURL, nil, retryHTTPClient, LocationConfig{})
		_, _, err := unauthSource.AvailableVersions(context.Background(), existingProvider)
		switch err := err.(type) {
		case ErrUnauthorized:
			if got, want := string(err.Hostname), baseURL.Host; got != want {
				t.Errorf("wrong hostname in error\ngot:  %s\nwant: %s", got, want)
			}
		default:
			t.Fatalf("wrong error type %T; want ErrUnauthorized", err)
		}
	})
	t.Run("AvailableVersions when the response is a server error", func(t *testing.T) {
		_, _, err := source.AvailableVersions(context.Background(), failingProvider)
		switch err := err.(type) {
		case ErrQueryFailed:
			if got, want := err.Provider, failingProvider; got != want {
				t.Errorf("wrong provider in error\ngot:  %s\nwant: %s", got, want)
			}
			if err.MirrorURL != source.baseURL {
				t.Errorf("error does not refer to the mirror URL")
			}
		default:
			t.Fatalf("wrong error type %T; want ErrQueryFailed", err)
		}
	})
	t.Run("AvailableVersions for provider that redirects", func(t *testing.T) {
		got, _, err := source.AvailableVersions(context.Background(), redirectingProvider)
		if err != nil {
			t.Fatalf("unexpected error: %s", err)
		}
		want := VersionList{
			MustParseVersion("1.0.0"),
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("wrong result\n%s", diff)
		}
	})
	t.Run("AvailableVersions for provider that redirects too much", func(t *testing.T) {
		_, _, err := source.AvailableVersions(context.Background(), redirectLoopProvider)
		if err == nil {
			t.Fatalf("succeeded; expected error")
		}
	})
	t.Run("PackageMeta for a version that exists and has a hash", func(t *testing.T) {
		version := MustParseVersion("1.0.0")
		got, err := source.PackageMeta(context.Background(), existingProvider, version, tosPlatform)
		if err != nil {
			t.Fatalf("unexpected error: %s", err)
		}

		want := PackageMeta{
			Provider:       existingProvider,
			Version:        version,
			TargetPlatform: tosPlatform,
			Filename:       "terraform-provider-test_v1.0.0_tos_m68k.zip",
			Location:       PackageHTTPURL{URL: httpServer.URL + "/terraform.io/test/exists/terraform-provider-test_v1.0.0_tos_m68k.zip", ClientBuilder: clientBuilderFromHTTPLocation(t, retryHTTPClient.RetryMax)},
			Authentication: packageHashAuthentication{
				RequiredHashes: []Hash{"h1:placeholder-hash"},
				AllHashes:      []Hash{"h1:placeholder-hash", "h0:unacceptable-hash"},
				Platform:       Platform{"tos", "m68k"},
			},
		}
		if diff := cmp.Diff(want, got, cmpClientBuilder); diff != "" {
			t.Errorf("wrong result\n%s", diff)
		}
	})
	t.Run("PackageMeta for a version that exists and has no hash", func(t *testing.T) {
		version := MustParseVersion("1.0.1")
		got, err := source.PackageMeta(context.Background(), existingProvider, version, tosPlatform)
		if err != nil {
			t.Fatalf("unexpected error: %s", err)
		}

		want := PackageMeta{
			Provider:       existingProvider,
			Version:        version,
			TargetPlatform: tosPlatform,
			Filename:       "terraform-provider-test_v1.0.1_tos_m68k.zip",
			Location:       PackageHTTPURL{URL: httpServer.URL + "/terraform.io/test/exists/terraform-provider-test_v1.0.1_tos_m68k.zip", ClientBuilder: clientBuilderFromHTTPLocation(t, retryHTTPClient.RetryMax)},
			Authentication: nil,
		}
		if diff := cmp.Diff(want, got, cmpClientBuilder); diff != "" {
			t.Errorf("wrong result\n%s", diff)
		}
	})
	t.Run("PackageMeta for a version that exists but has no archives", func(t *testing.T) {
		version := MustParseVersion("1.0.2-beta.1")
		_, err := source.PackageMeta(context.Background(), existingProvider, version, tosPlatform)
		switch err := err.(type) {
		case ErrPlatformNotSupported:
			if got, want := err.Provider, existingProvider; got != want {
				t.Errorf("wrong provider in error\ngot:  %s\nwant: %s", got, want)
			}
			if got, want := err.Platform, tosPlatform; got != want {
				t.Errorf("wrong platform in error\ngot:  %s\nwant: %s", got, want)
			}
			if err.MirrorURL != source.baseURL {
				t.Errorf("error does not contain the mirror URL")
			}
		default:
			t.Fatalf("wrong error type %T; want ErrPlatformNotSupported", err)
		}
	})
	t.Run("PackageMeta with redirect to a version that exists", func(t *testing.T) {
		version := MustParseVersion("1.0.0")
		got, err := source.PackageMeta(context.Background(), redirectingProvider, version, tosPlatform)
		if err != nil {
			t.Fatalf("unexpected error: %s", err)
		}

		want := PackageMeta{
			Provider:       redirectingProvider,
			Version:        version,
			TargetPlatform: tosPlatform,
			Filename:       "terraform-provider-test.zip",

			// NOTE: The final URL is interpreted relative to the redirect
			// target, not relative to what we originally requested.
			Location: PackageHTTPURL{URL: httpServer.URL + "/redirect-target/terraform-provider-test.zip", ClientBuilder: clientBuilderFromHTTPLocation(t, retryHTTPClient.RetryMax)},
		}
		if diff := cmp.Diff(want, got, cmpClientBuilder); diff != "" {
			t.Errorf("wrong result\n%s", diff)
		}
	})
	t.Run("PackageMeta when the response is a server error", func(t *testing.T) {
		version := MustParseVersion("1.0.0")
		_, err := source.PackageMeta(context.Background(), failingProvider, version, tosPlatform)
		switch err := err.(type) {
		case ErrQueryFailed:
			if got, want := err.Provider, failingProvider; got != want {
				t.Errorf("wrong provider in error\ngot:  %s\nwant: %s", got, want)
			}
			if err.MirrorURL != source.baseURL {
				t.Errorf("error does not contain the mirror URL")
			}
		default:
			t.Fatalf("wrong error type %T; want ErrQueryFailed", err)
		}
	})
}

// Exercises the [LocationConfig.TrustAllHashes] opt-in for the HTTP mirror
// source: when disabled, only the target platform's hashes are considered;
// when enabled, hashes for every platform the mirror reports are collected.
// Malformed hashes in any considered entry always fail the query.
func TestHTTPMirrorSourceTrustAllHashes(t *testing.T) {
	// TLS server + trusting client, because [newHTTPMirrorSourceWithHTTPClient]
	// requires an https URL. This test only inspects the resulting package
	// metadata; it never installs a package through the [PackageHTTPURL] client.
	httpServer := httptest.NewTLSServer(http.HandlerFunc(testHTTPMirrorSourceHandlerNoAuth))
	defer httpServer.Close()
	baseURL, err := url.Parse(httpServer.URL)
	if err != nil {
		t.Fatalf("unexpected error parsing the url: %s", err)
	}
	retryHTTPClient := retryablehttp.NewClient()
	retryHTTPClient.HTTPClient = httpServer.Client()

	multiHashProvider := addrs.MustParseProviderSourceString("terraform.io/test/multihash")
	malformedHashProvider := addrs.MustParseProviderSourceString("terraform.io/test/malformedhash")
	version := MustParseVersion("1.0.0")
	targetPlatform := Platform{OS: "tos", Arch: "m68k"}

	// Sort the collected hashes, because the source iterates a map of
	// platforms and so the order is not deterministic.
	hashStrings := func(hashes []Hash) []string {
		ret := make([]string, len(hashes))
		for i, hash := range hashes {
			ret[i] = hash.String()
		}
		sort.Strings(ret)
		return ret
	}

	verifyAuthentication := func(t *testing.T, meta PackageMeta, wantTrust bool, want []string) {
		t.Helper()
		auth, ok := meta.Authentication.(packageHashAuthentication)
		if !ok {
			t.Fatalf("wrong authentication type: %T", meta.Authentication)
		}
		if got, want := auth.TrustAllHashes, wantTrust; got != want {
			t.Errorf("wrong TrustAllHashes\ngot:  %t\nwant: %t", got, want)
		}
		if got, want := hashStrings(auth.AllHashes), want; !slices.Equal(got, want) {
			t.Errorf("wrong AllHashes\ngot:  %v\nwant: %v", got, want)
		}
		wantHashes := make([]Hash, len(want))
		for i, hashStr := range want {
			wantHashes[i] = Hash(hashStr)
		}
		if got, want := hashStrings(auth.RequiredHashes), hashStrings(PreferredHashes(wantHashes)); !slices.Equal(got, want) {
			t.Errorf("wrong RequiredHashes\ngot:  %v\nwant: %v", got, want)
		}
	}

	t.Run("trust_all_hashes false only gathers target platform hashes", func(t *testing.T) {
		source := newHTTPMirrorSourceWithHTTPClient(baseURL, nil, retryHTTPClient, LocationConfig{TrustAllHashes: false})
		got, err := source.PackageMeta(t.Context(), multiHashProvider, version, targetPlatform)
		if err != nil {
			t.Fatalf("unexpected error: %s", err)
		}
		verifyAuthentication(t, got, false, []string{"h0:filterme-out", "h1:tos-hash"})
	})

	t.Run("trust_all_hashes true gathers hashes for every platform", func(t *testing.T) {
		source := newHTTPMirrorSourceWithHTTPClient(baseURL, nil, retryHTTPClient, LocationConfig{TrustAllHashes: true})
		got, err := source.PackageMeta(t.Context(), multiHashProvider, version, targetPlatform)
		if err != nil {
			t.Fatalf("unexpected error: %s", err)
		}
		verifyAuthentication(t, got, true, []string{"h0:filterme-out", "h1:offplatform-hash", "h1:tos-hash"})
	})

	t.Run("malformed hash in other platform archive fails when trusted", func(t *testing.T) {
		source := newHTTPMirrorSourceWithHTTPClient(baseURL, nil, retryHTTPClient, LocationConfig{TrustAllHashes: true})
		_, err := source.PackageMeta(t.Context(), malformedHashProvider, version, targetPlatform)
		switch err := err.(type) {
		case ErrQueryFailed:
			if got, want := err.Provider, malformedHashProvider; got != want {
				t.Errorf("wrong provider in error\ngot:  %s\nwant: %s", got, want)
			}
			if substr := `provider mirror returned invalid provider hash "this-is-not-a-hash"`; !strings.Contains(err.Error(), substr) {
				t.Fatalf("expected err %q to contain %q", err.Error(), substr)
			}
		default:
			t.Fatalf("wrong error type %T; want ErrQueryFailed", err)
		}
	})

	t.Run("malformed hash in other platform archive is ignored when untrusted", func(t *testing.T) {
		source := newHTTPMirrorSourceWithHTTPClient(baseURL, nil, retryHTTPClient, LocationConfig{TrustAllHashes: false})
		got, err := source.PackageMeta(t.Context(), malformedHashProvider, version, targetPlatform)
		if err != nil {
			t.Fatalf("unexpected error: %s", err)
		}
		verifyAuthentication(t, got, false, []string{"h1:tos-hash"})
	})
}

// Regression for Oracle Gate37 finding 1: an archives entry that is present
// but JSON-null must not panic. With trust_all_hashes enabled the nil
// off-platform entry makes the whole query fail with a clear error; with
// trust disabled it is ignored, as before.
func TestHTTPMirrorSourceNullArchiveEntry(t *testing.T) {
	httpServer := httptest.NewTLSServer(http.HandlerFunc(testHTTPMirrorSourceHandlerNoAuth))
	defer httpServer.Close()
	baseURL, err := url.Parse(httpServer.URL)
	if err != nil {
		t.Fatalf("unexpected error parsing the url: %s", err)
	}
	retryHTTPClient := retryablehttp.NewClient()
	retryHTTPClient.HTTPClient = httpServer.Client()

	provider := addrs.MustParseProviderSourceString("terraform.io/test/nullarchive")
	version := MustParseVersion("1.0.0")
	targetPlatform := Platform{OS: "tos", Arch: "m68k"}

	t.Run("trust_all_hashes true fails with clear error", func(t *testing.T) {
		source := newHTTPMirrorSourceWithHTTPClient(baseURL, nil, retryHTTPClient, LocationConfig{TrustAllHashes: true})
		got, err := source.PackageMeta(t.Context(), provider, version, targetPlatform)
		if got.Authentication != nil {
			t.Errorf("wrong result: got authentication %#v, want nil", got.Authentication)
		}
		switch err := err.(type) {
		case ErrQueryFailed:
			if got, want := err.Provider, provider; got != want {
				t.Errorf("wrong provider in error\ngot:  %s\nwant: %s", got, want)
			}
			if substr := `provider mirror returned archive entry for platform "other_p9k" with no metadata`; !strings.Contains(err.Error(), substr) {
				t.Fatalf("expected err %q to contain %q", err.Error(), substr)
			}
		default:
			t.Fatalf("wrong error type %T; want ErrQueryFailed", err)
		}
	})

	t.Run("trust_all_hashes false ignores the null off-platform entry", func(t *testing.T) {
		source := newHTTPMirrorSourceWithHTTPClient(baseURL, nil, retryHTTPClient, LocationConfig{TrustAllHashes: false})
		got, err := source.PackageMeta(t.Context(), provider, version, targetPlatform)
		if err != nil {
			t.Fatalf("unexpected error: %s", err)
		}
		auth, ok := got.Authentication.(packageHashAuthentication)
		if !ok {
			t.Fatalf("wrong authentication type: %T", got.Authentication)
		}
		if got, want := auth.TrustAllHashes, false; got != want {
			t.Errorf("wrong TrustAllHashes\ngot:  %t\nwant: %t", got, want)
		}
		wantAll := []string{"h1:tos-hash"}
		gotAll := make([]string, len(auth.AllHashes))
		for i, hash := range auth.AllHashes {
			gotAll[i] = hash.String()
		}
		sort.Strings(gotAll)
		if diff := cmp.Diff(wantAll, gotAll); diff != "" {
			t.Errorf("wrong AllHashes\n%s", diff)
		}
	})
}

func testHTTPMirrorSourceHandler(resp http.ResponseWriter, req *http.Request) {
	if auth := req.Header.Get("authorization"); auth != "Bearer placeholder-token" {
		resp.WriteHeader(401)
		fmt.Fprintln(resp, "incorrect auth token")
	}
	testHTTPMirrorSourceHandlerNoAuth(resp, req)
}

func testHTTPMirrorSourceHandlerNoAuth(resp http.ResponseWriter, req *http.Request) {
	switch req.URL.Path {
	case "/terraform.io/test/exists/index.json":
		resp.Header().Add("Content-Type", "application/json; ignored=yes")
		resp.WriteHeader(200)
		fmt.Fprint(resp, `
			{
				"versions": {
					"1.0.0": {},
					"1.0.1": {},
					"1.0.2-beta.1": {}
				}
			}
		`)

	case "/terraform.io/test/fails/index.json", "/terraform.io/test/fails/1.0.0.json":
		resp.WriteHeader(500)
		fmt.Fprint(resp, "server error")

	case "/terraform.io/test/exists/1.0.0.json":
		resp.Header().Add("Content-Type", "application/json; ignored=yes")
		resp.WriteHeader(200)
		fmt.Fprint(resp, `
			{
				"archives": {
					"tos_m68k": {
						"url": "terraform-provider-test_v1.0.0_tos_m68k.zip",
						"hashes": [
							"h1:placeholder-hash",
							"h0:unacceptable-hash"
						]
					}
				}
			}
		`)

	case "/terraform.io/test/exists/1.0.1.json":
		resp.Header().Add("Content-Type", "application/json; ignored=yes")
		resp.WriteHeader(200)
		fmt.Fprint(resp, `
			{
				"archives": {
					"tos_m68k": {
						"url": "terraform-provider-test_v1.0.1_tos_m68k.zip"
					}
				}
			}
		`)

	case "/terraform.io/test/exists/1.0.2-beta.1.json":
		resp.Header().Add("Content-Type", "application/json; ignored=yes")
		resp.WriteHeader(200)
		fmt.Fprint(resp, `
			{
				"archives": {}
			}
		`)

	case "/terraform.io/test/multihash/1.0.0.json":
		resp.Header().Add("Content-Type", "application/json; ignored=yes")
		resp.WriteHeader(200)
		fmt.Fprint(resp, `
			{
				"archives": {
					"tos_m68k": {
						"url": "terraform-provider-test_v1.0.0_tos_m68k.zip",
						"hashes": [
							"h1:tos-hash",
							"h0:filterme-out"
						]
					},
					"other_p9k": {
						"url": "terraform-provider-test_v1.0.0_other_p9k.zip",
						"hashes": [
							"h1:offplatform-hash"
						]
					}
				}
			}
		`)

	case "/terraform.io/test/malformedhash/1.0.0.json":
		resp.Header().Add("Content-Type", "application/json; ignored=yes")
		resp.WriteHeader(200)
		fmt.Fprint(resp, `
			{
				"archives": {
					"tos_m68k": {
						"url": "terraform-provider-test_v1.0.0_tos_m68k.zip",
						"hashes": [
							"h1:tos-hash"
						]
					},
					"other_p9k": {
						"url": "terraform-provider-test_v1.0.0_other_p9k.zip",
						"hashes": [
							"this-is-not-a-hash"
						]
					}
				}
			}
		`)

	case "/terraform.io/test/nullarchive/1.0.0.json":
		resp.Header().Add("Content-Type", "application/json; ignored=yes")
		resp.WriteHeader(200)
		fmt.Fprint(resp, `
			{
				"archives": {
					"tos_m68k": {
						"url": "terraform-provider-test_v1.0.0_tos_m68k.zip",
						"hashes": [
							"h1:tos-hash"
						]
					},
					"other_p9k": null
				}
			}
		`)

	case "/terraform.io/test/redirects/index.json":
		resp.Header().Add("location", "/redirect-target/index.json")
		resp.WriteHeader(301)
		fmt.Fprint(resp, "redirect")

	case "/redirect-target/index.json":
		resp.Header().Add("Content-Type", "application/json")
		resp.WriteHeader(200)
		fmt.Fprint(resp, `
			{
				"versions": {
					"1.0.0": {}
				}
			}
		`)

	case "/terraform.io/test/redirects/1.0.0.json":
		resp.Header().Add("location", "/redirect-target/1.0.0.json")
		resp.WriteHeader(301)
		fmt.Fprint(resp, "redirect")

	case "/redirect-target/1.0.0.json":
		resp.Header().Add("Content-Type", "application/json")
		resp.WriteHeader(200)
		fmt.Fprint(resp, `
			{
				"archives": {
					"tos_m68k": {
						"url": "terraform-provider-test.zip"
					}
				}
			}
		`)

	case "/terraform.io/test/redirect-loop/index.json":
		// This is intentionally redirecting to itself, to create a loop.
		resp.Header().Add("location", req.URL.Path)
		resp.WriteHeader(301)
		fmt.Fprint(resp, "redirect loop")

	case "/terraform.io/missing/providerbinary/1.2.0.json":
		resp.Header().Add("Content-Type", "application/json; ignored=yes")
		resp.WriteHeader(200)
		fmt.Fprint(resp, `
			{
				"archives": {
					"tos_m68k": {
						"url": "terraform-missing-providerbinary_v1.2.0_tos_m68k.zip",
						"hashes": [
							"h1:placeholder-hash",
							"h0:unacceptable-hash"
						]
					}
				}
			}
		`)
	case "/terraform.io/missing/providerbinary/terraform-missing-providerbinary_v1.2.0_tos_m68k.zip":
		resp.Header().Add("Content-Type", "application/json; ignored=yes")
		// Just return a retryable status code
		resp.WriteHeader(http.StatusInternalServerError)

	default:
		resp.WriteHeader(404)
		fmt.Fprintln(resp, "not found")
	}
}

// Checks that the [LocationConfig] is used properly to configure the [PackageHTTPURL] http client,
// meaning that the retries are configured as expected.
func TestHTTPMirrorLocationRetriesConfiguredCorrectly(t *testing.T) {
	// Using the NoAuth handler and NoTLSServer here because the http client
	// used to configure the [PackageHTTPURL] does not have the credentials and the
	// CA forwarded from the HTTPMirrorSource.
	httpServer := httptest.NewServer(http.HandlerFunc(testHTTPMirrorSourceHandlerNoAuth))
	defer httpServer.Close()
	baseURL, err := url.Parse(httpServer.URL)
	if err != nil {
		t.Fatalf("unexpected error parsing the url: %s", err)
	}
	creds := svcauth.StaticCredentialsSource(map[svchost.Hostname]svcauth.HostCredentials{
		svchost.Hostname(baseURL.Host): svcauth.HostCredentialsToken("placeholder-token"),
	})
	retryHTTPClient := retryablehttp.NewClient()
	retryHTTPClient.HTTPClient = httpclient.New(t.Context())
	// The same reason as above applies on why using here directly the struct for initialisation
	// instead of calling [newHTTPMirrorSourceWithHTTPClient]. We have no way to forward the CA
	// from the http client got from the TLS server into the [PackageHTTPURL] inner client.
	source := &HTTPMirrorSource{
		baseURL:        baseURL,
		creds:          creds,
		httpClient:     retryHTTPClient,
		locationConfig: LocationConfig{ProviderDownloadRetries: 2},
	}

	providerAddr := addrs.MustParseProviderSourceString("terraform.io/missing/providerbinary")
	version := versions.MustParseVersion("1.2.0")
	platform := Platform{OS: "tos", Arch: "m68k"}
	got, err := source.PackageMeta(t.Context(), providerAddr, version, platform)
	if err != nil {
		t.Fatalf("unexpected error got from packageMeta: %s", err)
	}
	tmp := t.TempDir()
	_, err = got.Location.InstallProviderPackage(t.Context(), got, tmp, nil)
	if err == nil {
		t.Fatalf("expected error but got nothing")
	}
	if expectedSuffix := "giving up after 3 attempt(s)"; !strings.HasSuffix(err.Error(), expectedSuffix) {
		t.Fatalf("expected err %q to have suffix %q", err.Error(), expectedSuffix)
	}
}
