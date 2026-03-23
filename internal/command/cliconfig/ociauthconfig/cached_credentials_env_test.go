// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package ociauthconfig

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// countingLookupEnv is a test double that records the number of
// QueryDockerCredentialHelper calls.
type countingLookupEnv struct {
	mu    sync.Mutex
	calls int

	result DockerCredentialHelperGetResult
	err    error
}

func (e *countingLookupEnv) QueryDockerCredentialHelper(_ context.Context, _ string, _ string) (DockerCredentialHelperGetResult, error) {
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

// blockingLookupEnv wraps a countingLookupEnv and blocks until gate is closed.
type blockingLookupEnv struct {
	inner *countingLookupEnv
	gate  <-chan struct{}
}

func (e *blockingLookupEnv) QueryDockerCredentialHelper(ctx context.Context, helperName string, serverURL string) (DockerCredentialHelperGetResult, error) {
	<-e.gate
	return e.inner.QueryDockerCredentialHelper(ctx, helperName, serverURL)
}

// slowLookupEnv blocks until its context is cancelled, simulating a hung
// credential helper process.
type slowLookupEnv struct{}

func (e *slowLookupEnv) QueryDockerCredentialHelper(ctx context.Context, _ string, _ string) (DockerCredentialHelperGetResult, error) {
	<-ctx.Done()
	return DockerCredentialHelperGetResult{}, ctx.Err()
}

func TestCachedCredentialsLookupEnv_CachesSuccessWithinTTL(t *testing.T) {
	inner := &countingLookupEnv{
		result: DockerCredentialHelperGetResult{
			ServerURL: "https://example.com",
			Username:  "u",
			Secret:    "s",
		},
	}
	env := NewCachedCredentialsLookupEnv(inner, time.Minute)
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

func TestCachedCredentialsLookupEnv_ExpiresAfterTTL(t *testing.T) {
	inner := &countingLookupEnv{
		result: DockerCredentialHelperGetResult{
			ServerURL: "https://example.com",
			Username:  "u",
			Secret:    "s",
		},
	}
	env := NewCachedCredentialsLookupEnv(inner, 10*time.Second)
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

func TestCachedCredentialsLookupEnv_CachesNotFoundErrorWithinTTL(t *testing.T) {
	notFoundErr := NewCredentialsNotFoundError(context.Canceled)
	inner := &countingLookupEnv{err: notFoundErr}
	env := NewCachedCredentialsLookupEnv(inner, time.Minute)
	base := time.Unix(100, 0)
	env.now = func() time.Time { return base }

	ctx := context.Background()
	_, err := env.QueryDockerCredentialHelper(ctx, "example", "https://example.com")
	if err == nil || !IsCredentialsNotFoundError(err) {
		t.Fatalf("expected not-found error, got %v", err)
	}
	_, err = env.QueryDockerCredentialHelper(ctx, "example", "https://example.com")
	if err == nil || !IsCredentialsNotFoundError(err) {
		t.Fatalf("expected not-found error, got %v", err)
	}

	if got, want := inner.Calls(), 1; got != want {
		t.Fatalf("inner calls = %d, want %d", got, want)
	}
}

func TestCachedCredentialsLookupEnv_UnexpectedErrorUsesShortTTL(t *testing.T) {
	// Unexpected errors (not "credentials not found") should be cached with
	// a short TTL so that transient failures recover quickly.
	transientErr := fmt.Errorf("connection refused")
	inner := &countingLookupEnv{err: transientErr}
	env := NewCachedCredentialsLookupEnv(inner, time.Minute)
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

	// Advance past the short error TTL (credentialHelperErrorCacheTTL = 10s) but not the full TTL (60s).
	now = base.Add(11 * time.Second)
	_, err = env.QueryDockerCredentialHelper(ctx, "example", "https://example.com")
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if got, want := inner.Calls(), 2; got != want {
		t.Fatalf("expected 2 inner calls after short TTL expired, got %d", got)
	}
}

func TestCachedCredentialsLookupEnv_SingleflightDeduplication(t *testing.T) {
	// Verify that concurrent calls for the same key are deduplicated.
	gate := make(chan struct{})
	inner := &countingLookupEnv{
		result: DockerCredentialHelperGetResult{
			ServerURL: "https://example.com",
			Username:  "u",
			Secret:    "s",
		},
	}
	// Wrap inner to block until gate is released.
	blocking := &blockingLookupEnv{inner: inner, gate: gate}
	env := NewCachedCredentialsLookupEnv(blocking, time.Minute)
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

func TestCachedCredentialsLookupEnv_TimeoutErrorUsesShortTTL(t *testing.T) {
	// Use a very short TTL so that a simulated timeout expires quickly.
	const shortCacheTTL = time.Minute

	now := time.Now()
	clock := now

	env := NewCachedCredentialsLookupEnv(&slowLookupEnv{}, shortCacheTTL)
	env.now = func() time.Time { return clock }

	// Use a context with a very short timeout to simulate the helper hanging.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := env.QueryDockerCredentialHelper(ctx, "test-helper", "https://example.com")
	if err == nil {
		t.Fatal("expected an error from a timed-out credential helper, got nil")
	}

	// The error must NOT be a CredentialsNotFoundError — it is a context error.
	if IsCredentialsNotFoundError(err) {
		t.Fatalf("expected a non-not-found error (timeout), got a CredentialsNotFoundError: %v", err)
	}

	// A timeout error should be cached with credentialHelperErrorCacheTTL (10s), not the full TTL.
	// Advance the clock by errorCacheTTL + 1s and verify the cache entry has expired.
	clock = now.Add(credentialHelperErrorCacheTTL + time.Second)

	inner2 := &countingLookupEnv{err: fmt.Errorf("second call")}
	env.inner = inner2

	_, _ = env.QueryDockerCredentialHelper(context.Background(), "test-helper", "https://example.com")
	if got := inner2.Calls(); got != 1 {
		t.Fatalf("expected cache to have expired after errorCacheTTL, but inner was called %d times (want 1)", got)
	}
}

func TestCachedCredentialsLookupEnv_ZeroTTLDisablesCache(t *testing.T) {
	inner := &countingLookupEnv{
		result: DockerCredentialHelperGetResult{
			ServerURL: "https://example.com",
			Username:  "u",
			Secret:    "s",
		},
	}
	env := NewCachedCredentialsLookupEnv(inner, 0)

	ctx := context.Background()
	for i := 0; i < 3; i++ {
		_, err := env.QueryDockerCredentialHelper(ctx, "example", "https://example.com")
		if err != nil {
			t.Fatalf("unexpected error on call %d: %v", i+1, err)
		}
	}

	if got, want := inner.Calls(), 3; got != want {
		t.Fatalf("inner calls = %d, want %d (zero TTL should bypass cache)", got, want)
	}
}
