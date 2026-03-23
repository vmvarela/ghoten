// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package ociauthconfig

import (
	"context"
	"sync"
	"time"
)

const (
	// DefaultCredentialHelperCacheTTL is the default TTL for successful credential
	// helper results (and "not found" errors). Callers that want a different TTL
	// can pass a custom value to [NewCachedCredentialsLookupEnv].
	DefaultCredentialHelperCacheTTL = 5 * time.Minute

	// credentialHelperErrorCacheTTL is a short TTL for unexpected errors so that
	// transient failures recover quickly.
	credentialHelperErrorCacheTTL = 10 * time.Second
)

type credentialHelperCacheKey struct {
	helperName string
	serverURL  string
}

type credentialHelperCacheEntry struct {
	result   DockerCredentialHelperGetResult
	err      error
	expires  time.Time
	hasValue bool
}

// singleflightCall deduplicates concurrent credential helper invocations
// for the same key so that expensive helpers (e.g. STS AssumeRole) are not
// called N times in parallel.
type singleflightCall struct {
	wg     sync.WaitGroup
	result DockerCredentialHelperGetResult
	err    error
}

// CachedCredentialsLookupEnv wraps a [CredentialsLookupEnvironment] and adds a
// TTL cache with singleflight deduplication so that each unique
// (helperName, serverURL) pair is resolved at most once per cache window.
//
// Use [NewCachedCredentialsLookupEnv] to construct an instance.
type CachedCredentialsLookupEnv struct {
	inner CredentialsLookupEnvironment
	ttl   time.Duration
	now   func() time.Time

	mu       sync.Mutex
	cache    map[credentialHelperCacheKey]credentialHelperCacheEntry
	inflight map[credentialHelperCacheKey]*singleflightCall
}

var _ CredentialsLookupEnvironment = (*CachedCredentialsLookupEnv)(nil)

// NewCachedCredentialsLookupEnv wraps inner with TTL caching and singleflight
// deduplication. A ttl of zero or negative disables caching and every call is
// forwarded directly to inner.
func NewCachedCredentialsLookupEnv(inner CredentialsLookupEnvironment, ttl time.Duration) *CachedCredentialsLookupEnv {
	return &CachedCredentialsLookupEnv{
		inner:    inner,
		ttl:      ttl,
		now:      time.Now,
		cache:    make(map[credentialHelperCacheKey]credentialHelperCacheEntry),
		inflight: make(map[credentialHelperCacheKey]*singleflightCall),
	}
}

// QueryDockerCredentialHelper implements [CredentialsLookupEnvironment].
//
// When caching is enabled (ttl > 0) the first call for a given
// (helperName, serverURL) pair invokes the underlying environment and caches
// the result. Subsequent calls within the TTL window return the cached value
// without executing the helper again. Concurrent calls for the same key are
// serialised via a singleflight mechanism so the helper is invoked exactly once.
func (e *CachedCredentialsLookupEnv) QueryDockerCredentialHelper(ctx context.Context, helperName string, serverURL string) (DockerCredentialHelperGetResult, error) {
	if err := ctx.Err(); err != nil {
		return DockerCredentialHelperGetResult{}, err
	}
	if e.ttl <= 0 {
		return e.inner.QueryDockerCredentialHelper(ctx, helperName, serverURL)
	}

	key := credentialHelperCacheKey{helperName: helperName, serverURL: serverURL}
	now := e.now()

	// Fast path: return a cached entry that has not yet expired.
	e.mu.Lock()
	if entry, ok := e.cache[key]; ok && entry.hasValue && now.Before(entry.expires) {
		e.mu.Unlock()
		return entry.result, entry.err
	}

	// Deduplicate concurrent calls for the same key (singleflight).
	if call, ok := e.inflight[key]; ok {
		e.mu.Unlock()
		call.wg.Wait()
		return call.result, call.err
	}
	call := &singleflightCall{}
	call.wg.Add(1)
	e.inflight[key] = call
	e.mu.Unlock()

	result, err := e.inner.QueryDockerCredentialHelper(ctx, helperName, serverURL)

	call.result = result
	call.err = err
	call.wg.Done()

	// Use the full TTL for success and "not found" errors; use a short TTL
	// for unexpected errors so transient failures recover quickly.
	cacheTTL := e.ttl
	if err != nil && !IsCredentialsNotFoundError(err) {
		cacheTTL = credentialHelperErrorCacheTTL
	}

	e.mu.Lock()
	e.cache[key] = credentialHelperCacheEntry{
		result:   result,
		err:      err,
		expires:  now.Add(cacheTTL),
		hasValue: true,
	}
	delete(e.inflight, key)
	e.mu.Unlock()

	return result, err
}
