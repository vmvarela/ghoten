package oras

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vmvarela/ghoten/internal/backend"
	"github.com/vmvarela/ghoten/internal/command/cliconfig/ociauthconfig"
	"github.com/vmvarela/ghoten/internal/configs"
	"github.com/vmvarela/ghoten/internal/encryption"
	"github.com/zclconf/go-cty/cty"
)

func TestBackend_impl(t *testing.T) {
	var _ backend.Backend = new(Backend)
}

func TestORASRetryConfigFromConfig(t *testing.T) {
	conf := map[string]cty.Value{
		"repository":     cty.StringVal("example.com/myorg/ghoten-state"),
		"retry_max":      cty.StringVal("9"),
		"retry_wait_min": cty.StringVal("15"),
		"retry_wait_max": cty.StringVal("150"),
	}

	b := backend.TestBackendConfig(t, New(encryption.StateEncryptionDisabled()), configs.SynthBody("synth", conf)).(*Backend)

	if b.retryCfg.MaxAttempts != 10 { // retry_max is number of retries
		t.Fatalf("expected MaxAttempts %d, got %d", 10, b.retryCfg.MaxAttempts)
	}
	if b.retryCfg.InitialBackoff != 15*time.Second {
		t.Fatalf("expected InitialBackoff %s, got %s", 15*time.Second, b.retryCfg.InitialBackoff)
	}
	if b.retryCfg.MaxBackoff != 150*time.Second {
		t.Fatalf("expected MaxBackoff %s, got %s", 150*time.Second, b.retryCfg.MaxBackoff)
	}
}

func TestORASRetryConfigFromEnv(t *testing.T) {
	t.Setenv(envVarRepository, "example.com/myorg/ghoten-state")
	t.Setenv(envVarRetryMax, "9")
	t.Setenv(envVarRetryWaitMin, "15")
	t.Setenv(envVarRetryWaitMax, "150")

	b := backend.TestBackendConfig(t, New(encryption.StateEncryptionDisabled()), nil).(*Backend)

	if b.retryCfg.MaxAttempts != 10 {
		t.Fatalf("expected MaxAttempts %d, got %d", 10, b.retryCfg.MaxAttempts)
	}
	if b.retryCfg.InitialBackoff != 15*time.Second {
		t.Fatalf("expected InitialBackoff %s, got %s", 15*time.Second, b.retryCfg.InitialBackoff)
	}
	if b.retryCfg.MaxBackoff != 150*time.Second {
		t.Fatalf("expected MaxBackoff %s, got %s", 150*time.Second, b.retryCfg.MaxBackoff)
	}
}

func TestORASVersioningConfigFromConfig(t *testing.T) {
	conf := map[string]cty.Value{
		"repository":   cty.StringVal("example.com/myorg/ghoten-state"),
		"max_versions": cty.StringVal("42"),
	}

	b := backend.TestBackendConfig(t, New(encryption.StateEncryptionDisabled()), configs.SynthBody("synth", conf)).(*Backend)

	if b.versioningMaxVersions != 42 {
		t.Fatalf("expected versioningMaxVersions %d, got %d", 42, b.versioningMaxVersions)
	}
}

func TestORASCompressionConfigFromConfig(t *testing.T) {
	conf := map[string]cty.Value{
		"repository":  cty.StringVal("example.com/myorg/ghoten-state"),
		"compression": cty.StringVal("gzip"),
	}

	b := backend.TestBackendConfig(t, New(encryption.StateEncryptionDisabled()), configs.SynthBody("synth", conf)).(*Backend)
	if b.compression != "gzip" {
		t.Fatalf("expected compression %q, got %q", "gzip", b.compression)
	}
}

func TestORASLockTTLConfigFromConfig(t *testing.T) {
	conf := map[string]cty.Value{
		"repository": cty.StringVal("example.com/myorg/ghoten-state"),
		"lock_ttl":   cty.StringVal("60"),
	}

	b := backend.TestBackendConfig(t, New(encryption.StateEncryptionDisabled()), configs.SynthBody("synth", conf)).(*Backend)
	if b.lockTTL != 60*time.Second {
		t.Fatalf("expected lockTTL %s, got %s", 60*time.Second, b.lockTTL)
	}
}

func TestORASRateLimitConfigFromConfig(t *testing.T) {
	conf := map[string]cty.Value{
		"repository":       cty.StringVal("example.com/myorg/ghoten-state"),
		"rate_limit":       cty.StringVal("10"),
		"rate_limit_burst": cty.StringVal("3"),
		"retry_max":        cty.StringVal("0"),
		"retry_wait_min":   cty.StringVal("1"),
		"retry_wait_max":   cty.StringVal("1"),
		"compression":      cty.StringVal("none"),
	}

	b := backend.TestBackendConfig(t, New(encryption.StateEncryptionDisabled()), configs.SynthBody("synth", conf)).(*Backend)
	if b.rateLimit != 10 {
		t.Fatalf("expected rateLimit %d, got %d", 10, b.rateLimit)
	}
	if b.rateBurst != 3 {
		t.Fatalf("expected rateBurst %d, got %d", 3, b.rateBurst)
	}
}

type blockingLimiter struct {
	ch <-chan struct{}
}

func (l blockingLimiter) Wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-l.ch:
		return nil
	}
}

type countingRoundTripper struct {
	mu    sync.Mutex
	calls int
}

func (rt *countingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.mu.Lock()
	rt.calls++
	rt.mu.Unlock()
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader("ok")),
		Header:     make(http.Header),
		Request:    req,
	}, nil
}

func (rt *countingRoundTripper) Calls() int {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.calls
}

func TestRateLimitedRoundTripper_WaitsBeforeRequest(t *testing.T) {
	gate := make(chan struct{})
	inner := &countingRoundTripper{}
	rt := &rateLimitedRoundTripper{limiter: blockingLimiter{ch: gate}, inner: inner}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.com/", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	done := make(chan struct{})
	go func() {
		_, _ = rt.RoundTrip(req)
		close(done)
	}()

	if inner.Calls() != 0 {
		t.Fatalf("expected no calls before limiter release")
	}

	close(gate)
	<-done

	if inner.Calls() != 1 {
		t.Fatalf("expected exactly 1 call after limiter release, got %d", inner.Calls())
	}
}

func TestUserAgentRoundTripper_DoesNotMutateOriginalRequest(t *testing.T) {
	inner := &countingRoundTripper{}
	rt := &userAgentRoundTripper{userAgent: "TestAgent/1.0", inner: inner}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.com/", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	// Ensure the original request has no User-Agent set
	if req.Header.Get("User-Agent") != "" {
		t.Fatalf("expected no User-Agent on original request")
	}

	_, err = rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("roundtrip: %v", err)
	}

	// The original request must remain unmodified (RoundTripper contract)
	if got := req.Header.Get("User-Agent"); got != "" {
		t.Fatalf("original request was mutated: User-Agent = %q, want empty", got)
	}

	if inner.Calls() != 1 {
		t.Fatalf("expected 1 inner call, got %d", inner.Calls())
	}
}

func TestUserAgentRoundTripper_PreservesExistingUserAgent(t *testing.T) {
	var capturedUA string
	inner := &headerCapturingRoundTripper{capture: func(h http.Header) { capturedUA = h.Get("User-Agent") }}
	rt := &userAgentRoundTripper{userAgent: "TestAgent/1.0", inner: inner}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.com/", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("User-Agent", "CustomAgent/2.0")

	_, err = rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("roundtrip: %v", err)
	}

	if capturedUA != "CustomAgent/2.0" {
		t.Fatalf("expected existing User-Agent to be preserved, got %q", capturedUA)
	}
}

type headerCapturingRoundTripper struct {
	capture func(http.Header)
}

func (rt *headerCapturingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if rt.capture != nil {
		rt.capture(req.Header)
	}
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader("ok")),
		Header:     make(http.Header),
		Request:    req,
	}, nil
}

type countingLookupEnv struct {
	mu    sync.Mutex
	calls int

	result ociauthconfig.DockerCredentialHelperGetResult
	err    error
}

func (e *countingLookupEnv) QueryDockerCredentialHelper(ctx context.Context, helperName string, serverURL string) (ociauthconfig.DockerCredentialHelperGetResult, error) {
	e.mu.Lock()
	e.calls++
	e.mu.Unlock()
	return e.result, e.err
}

func (e *countingLookupEnv) Calls() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

func TestCachedDockerCredentialHelperEnv_CachesSuccessWithinTTL(t *testing.T) {
	inner := &countingLookupEnv{
		result: ociauthconfig.DockerCredentialHelperGetResult{
			ServerURL: "https://example.com",
			Username:  "u",
			Secret:    "s",
		},
	}
	env := newCachedDockerCredentialHelperEnv(inner, time.Minute)
	base := time.Unix(100, 0)
	env.now = func() time.Time { return base }

	ctx := context.Background()
	_, err := env.QueryDockerCredentialHelper(ctx, "example", "https://example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, err = env.QueryDockerCredentialHelper(ctx, "example", "https://example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got, want := inner.Calls(), 1; got != want {
		t.Fatalf("inner calls = %d, want %d", got, want)
	}
}

func TestCachedDockerCredentialHelperEnv_ExpiresAfterTTL(t *testing.T) {
	inner := &countingLookupEnv{
		result: ociauthconfig.DockerCredentialHelperGetResult{
			ServerURL: "https://example.com",
			Username:  "u",
			Secret:    "s",
		},
	}
	env := newCachedDockerCredentialHelperEnv(inner, 10*time.Second)
	base := time.Unix(100, 0)
	now := base
	env.now = func() time.Time { return now }

	ctx := context.Background()
	_, err := env.QueryDockerCredentialHelper(ctx, "example", "https://example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	now = base.Add(11 * time.Second)
	_, err = env.QueryDockerCredentialHelper(ctx, "example", "https://example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got, want := inner.Calls(), 2; got != want {
		t.Fatalf("inner calls = %d, want %d", got, want)
	}
}

func TestCachedDockerCredentialHelperEnv_CachesNotFoundErrorWithinTTL(t *testing.T) {
	notFoundErr := ociauthconfig.NewCredentialsNotFoundError(context.Canceled) // any wrapped error is fine
	inner := &countingLookupEnv{err: notFoundErr}
	env := newCachedDockerCredentialHelperEnv(inner, time.Minute)
	base := time.Unix(100, 0)
	env.now = func() time.Time { return base }

	ctx := context.Background()
	_, err := env.QueryDockerCredentialHelper(ctx, "example", "https://example.com")
	if err == nil || !ociauthconfig.IsCredentialsNotFoundError(err) {
		t.Fatalf("expected not-found error, got %v", err)
	}
	_, err = env.QueryDockerCredentialHelper(ctx, "example", "https://example.com")
	if err == nil || !ociauthconfig.IsCredentialsNotFoundError(err) {
		t.Fatalf("expected not-found error, got %v", err)
	}

	if got, want := inner.Calls(), 1; got != want {
		t.Fatalf("inner calls = %d, want %d", got, want)
	}
}

func TestCachedDockerCredentialHelperEnv_UnexpectedErrorUsesShortTTL(t *testing.T) {
	// Unexpected errors (not "credentials not found") should be cached with
	// a short TTL so that transient failures recover quickly.
	transientErr := fmt.Errorf("connection refused")
	inner := &countingLookupEnv{err: transientErr}
	env := newCachedDockerCredentialHelperEnv(inner, time.Minute)
	base := time.Unix(100, 0)
	now := base
	env.now = func() time.Time { return now }

	ctx := context.Background()
	_, err := env.QueryDockerCredentialHelper(ctx, "example", "https://example.com")
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	// Same time: should be cached.
	_, _ = env.QueryDockerCredentialHelper(ctx, "example", "https://example.com")
	if got, want := inner.Calls(), 1; got != want {
		t.Fatalf("expected 1 inner call (cached), got %d", got)
	}

	// Advance past the short error TTL (errorCacheTTL = 10s) but not the full TTL (60s).
	now = base.Add(11 * time.Second)
	_, err = env.QueryDockerCredentialHelper(ctx, "example", "https://example.com")
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if got, want := inner.Calls(), 2; got != want {
		t.Fatalf("expected 2 inner calls after short TTL expired, got %d", got)
	}
}

func TestCachedDockerCredentialHelperEnv_SingleflightDeduplication(t *testing.T) {
	// Verify that concurrent calls for the same key are deduplicated.
	gate := make(chan struct{})
	inner := &countingLookupEnv{
		result: ociauthconfig.DockerCredentialHelperGetResult{
			ServerURL: "https://example.com",
			Username:  "u",
			Secret:    "s",
		},
	}
	// Wrap inner to block until gate is released.
	blocking := &blockingLookupEnv{inner: inner, gate: gate}
	env := newCachedDockerCredentialHelperEnv(blocking, time.Minute)
	env.now = func() time.Time { return time.Unix(100, 0) }

	ctx := context.Background()
	const concurrency = 5
	results := make(chan error, concurrency)

	for i := 0; i < concurrency; i++ {
		go func() {
			_, err := env.QueryDockerCredentialHelper(ctx, "example", "https://example.com")
			results <- err
		}()
	}

	// Let all goroutines reach the singleflight barrier, then release.
	time.Sleep(50 * time.Millisecond)
	close(gate)

	for i := 0; i < concurrency; i++ {
		if err := <-results; err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	// Despite 5 concurrent callers, only 1 actual helper invocation should have occurred.
	if got := inner.Calls(); got != 1 {
		t.Fatalf("expected 1 inner call (singleflight), got %d", got)
	}
}

// blockingLookupEnv wraps a countingLookupEnv and blocks until gate is closed.
type blockingLookupEnv struct {
	inner *countingLookupEnv
	gate  <-chan struct{}
}

func (e *blockingLookupEnv) QueryDockerCredentialHelper(ctx context.Context, helperName string, serverURL string) (ociauthconfig.DockerCredentialHelperGetResult, error) {
	<-e.gate
	return e.inner.QueryDockerCredentialHelper(ctx, helperName, serverURL)
}

// slowLookupEnv blocks until its context is cancelled, simulating a hung
// credential helper process.
type slowLookupEnv struct{}

func (e *slowLookupEnv) QueryDockerCredentialHelper(ctx context.Context, helperName string, serverURL string) (ociauthconfig.DockerCredentialHelperGetResult, error) {
	<-ctx.Done()
	return ociauthconfig.DockerCredentialHelperGetResult{}, ctx.Err()
}

func TestCachedDockerCredentialHelperEnv_TimeoutErrorUsesShortTTL(t *testing.T) {
	// Use a very short TTL so that a simulated timeout expires quickly.
	const shortCacheTTL = time.Minute

	now := time.Now()
	clock := now

	env := &cachedDockerCredentialHelperEnv{
		inner: &slowLookupEnv{},
		ttl:   shortCacheTTL,
		now:   func() time.Time { return clock },
	}
	env.cache = make(map[dockerCredentialHelperCacheKey]dockerCredentialHelperCacheEntry)
	env.inflight = make(map[dockerCredentialHelperCacheKey]*singleflightCall)

	// Use a context with a very short timeout to simulate the helper hanging.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := env.QueryDockerCredentialHelper(ctx, "test-helper", "https://example.com")
	if err == nil {
		t.Fatal("expected an error from a timed-out credential helper, got nil")
	}

	// The error must NOT be a CredentialsNotFoundError — it is a context error.
	if ociauthconfig.IsCredentialsNotFoundError(err) {
		t.Fatalf("expected a non-not-found error (timeout), got a CredentialsNotFoundError: %v", err)
	}

	// A timeout error should be cached with errorCacheTTL (10s), not the full TTL.
	// Advance the clock by errorCacheTTL + 1s and verify the cache entry has expired.
	clock = now.Add(errorCacheTTL + time.Second)

	inner2 := &countingLookupEnv{err: fmt.Errorf("second call")}
	env.inner = inner2

	_, _ = env.QueryDockerCredentialHelper(context.Background(), "test-helper", "https://example.com")
	if got := inner2.Calls(); got != 1 {
		t.Fatalf("expected cache to have expired after errorCacheTTL, but inner was called %d times (want 1)", got)
	}
}

// writeTempFile writes data to a new file in t.TempDir() and returns the path.
func writeTempFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatalf("writing temp file %s: %v", path, err)
	}
	return path
}

// TestNewORASHTTPClient covers the TLS configuration paths of newORASHTTPClient.
// The insecure and CA-file subtests use a real httptest.TLSServer to verify
// behaviour end-to-end rather than inspecting the (unstable) transport chain.
func TestNewORASHTTPClient(t *testing.T) {
	t.Run("default — no TLS options", func(t *testing.T) {
		client, err := newORASHTTPClient(false, "", 0, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if client == nil {
			t.Fatal("expected non-nil client")
		}
	})

	t.Run("insecure=true succeeds against self-signed TLS server", func(t *testing.T) {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(srv.Close)

		client, err := newORASHTTPClient(true, "", 0, 0)
		if err != nil {
			t.Fatalf("unexpected error building client: %v", err)
		}

		resp, err := client.Get(srv.URL) //nolint:noctx
		if err != nil {
			t.Fatalf("GET to TLS server with insecure=true failed: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("unexpected status %d", resp.StatusCode)
		}
	})

	t.Run("valid CA file trusted against matching TLS server", func(t *testing.T) {
		// httptest.NewTLSServer uses its own self-signed cert. We use the
		// server's TLS config to extract the CA cert PEM and write it as
		// our ca_file. This proves that the loaded pool is actually used.
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(srv.Close)

		// Extract the server's CA cert as PEM.
		serverCert := srv.TLS.Certificates[0]
		parsedCert, err := x509.ParseCertificate(serverCert.Certificate[0])
		if err != nil {
			t.Fatalf("parsing server cert: %v", err)
		}
		caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: parsedCert.Raw})
		caPath := writeTempFile(t, "ca.pem", caPEM)

		client, err := newORASHTTPClient(false, caPath, 0, 0)
		if err != nil {
			t.Fatalf("unexpected error building client: %v", err)
		}

		resp, err := client.Get(srv.URL) //nolint:noctx
		if err != nil {
			t.Fatalf("GET with custom CA file failed: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("unexpected status %d", resp.StatusCode)
		}
	})

	t.Run("non-existent CA file returns error", func(t *testing.T) {
		_, err := newORASHTTPClient(false, "/no/such/file.pem", 0, 0)
		if err == nil {
			t.Fatal("expected error for missing CA file, got nil")
		}
		if !strings.Contains(err.Error(), "reading ca_file") {
			t.Errorf("unexpected error message: %v", err)
		}
	})

	t.Run("CA file with invalid PEM returns error", func(t *testing.T) {
		badPath := writeTempFile(t, "bad.pem", []byte("not-valid-pem"))
		_, err := newORASHTTPClient(false, badPath, 0, 0)
		if err == nil {
			t.Fatal("expected error for invalid PEM content, got nil")
		}
		if !strings.Contains(err.Error(), "no valid certificates") {
			t.Errorf("unexpected error message: %v", err)
		}
	})
}
