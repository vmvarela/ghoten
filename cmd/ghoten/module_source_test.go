// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"context"
	"errors"
	"testing"

	"github.com/vmvarela/ghoten/internal/command/cliconfig/ociauthconfig"
	"github.com/vmvarela/ghoten/internal/getmodules"
)

// TestRemoteModulePackageFetcher verifies that remoteModulePackageFetcher returns
// a non-nil *getmodules.PackageFetcher when given a valid ociCredsPolicyBuilder.
func TestRemoteModulePackageFetcher(t *testing.T) {
	ctx := context.Background()

	policy := func(_ context.Context) (ociauthconfig.CredentialsConfigs, error) {
		return ociauthconfig.CredentialsConfigs{}, nil
	}

	fetcher := remoteModulePackageFetcher(ctx, policy)
	if fetcher == nil {
		t.Fatal("expected non-nil PackageFetcher, got nil")
	}
}

// TestRemoteModulePackageFetcher_PolicyError verifies that remoteModulePackageFetcher
// returns a non-nil *getmodules.PackageFetcher even when the policy builder would
// return an error — the error surfaces lazily only when OCIRepositoryStore is called.
func TestRemoteModulePackageFetcher_PolicyError(t *testing.T) {
	ctx := context.Background()

	policy := func(_ context.Context) (ociauthconfig.CredentialsConfigs, error) {
		return ociauthconfig.CredentialsConfigs{}, errors.New("creds error")
	}

	fetcher := remoteModulePackageFetcher(ctx, policy)
	if fetcher == nil {
		t.Fatal("expected non-nil PackageFetcher, got nil")
	}
}

// TestModulePackageFetcherEnvironment_OCIRepositoryStore_CredError verifies that
// OCIRepositoryStore surfaces the error from ociCredsPolicyBuilder as an
// "invalid credentials configuration" error.
func TestModulePackageFetcherEnvironment_OCIRepositoryStore_CredError(t *testing.T) {
	ctx := context.Background()
	credErr := errors.New("credential helper failed")

	env := &modulePackageFetcherEnvironment{
		getOCICredsPolicy: func(_ context.Context) (ociauthconfig.CredentialsConfigs, error) {
			return ociauthconfig.CredentialsConfigs{}, credErr
		},
	}

	store, err := env.OCIRepositoryStore(ctx, "registry.example.com", "myorg/mymodule")
	if store != nil {
		t.Errorf("expected nil store on error, got %v", store)
	}
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, credErr) {
		t.Errorf("expected error to wrap %v, got: %v", credErr, err)
	}
}

// TestModulePackageFetcherEnvironment_ImplementsInterface verifies that
// *modulePackageFetcherEnvironment satisfies getmodules.PackageFetcherEnvironment.
func TestModulePackageFetcherEnvironment_ImplementsInterface(t *testing.T) {
	var _ getmodules.PackageFetcherEnvironment = (*modulePackageFetcherEnvironment)(nil)
}
