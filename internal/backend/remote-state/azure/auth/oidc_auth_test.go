// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/google/go-cmp/cmp"
	"github.com/vmvarela/ghoten/internal/httpclient"
)

func TestAudienceForCloudConfig(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		cloudConfig cloud.Configuration
		expected    string
	}{
		"public cloud": {
			cloudConfig: cloud.AzurePublic,
			expected:    "api://AzureADTokenExchange",
		},
		"china cloud": {
			cloudConfig: cloud.AzureChina,
			expected:    "api://AzureADTokenExchangeChina",
		},
		"us government cloud": {
			cloudConfig: cloud.AzureGovernment,
			expected:    "api://AzureADTokenExchangeUSGov",
		},
		"unknown cloud config": {
			cloudConfig: cloud.Configuration{
				ActiveDirectoryAuthorityHost: "https://login.example.com",
			},
			expected: "api://AzureADTokenExchange",
		},
		"zero cloud config": {
			cloudConfig: cloud.Configuration{},
			expected:    "api://AzureADTokenExchange",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := audienceForCloudConfig(tc.cloudConfig)
			if diff := cmp.Diff(tc.expected, got); diff != "" {
				t.Errorf("audience mismatch (-want, +got): %s", diff)
			}
		})
	}
}

func TestGetTokenFromRemote(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		requestURL    string
		cloudConfig   cloud.Configuration
		expectedQuery string
	}{
		"no audience in URL gets public cloud audience appended": {
			requestURL:    "?foo=bar",
			cloudConfig:   cloud.AzurePublic,
			expectedQuery: "audience=api%3A%2F%2FAzureADTokenExchange&foo=bar",
		},
		"china cloud audience appended": {
			requestURL:    "",
			cloudConfig:   cloud.AzureChina,
			expectedQuery: "audience=api%3A%2F%2FAzureADTokenExchangeChina",
		},
		"us government cloud audience appended": {
			requestURL:    "",
			cloudConfig:   cloud.AzureGovernment,
			expectedQuery: "audience=api%3A%2F%2FAzureADTokenExchangeUSGov",
		},
		"existing audience is preserved": {
			requestURL:    "?audience=api%3AUserChosen&other=yes",
			cloudConfig:   cloud.AzureGovernment,
			expectedQuery: "audience=api%3AUserChosen&other=yes",
		},
		"existing empty audience is preserved": {
			requestURL:    "?audience=&other=yes",
			cloudConfig:   cloud.AzurePublic,
			expectedQuery: "audience=&other=yes",
		},
		"existing audience with noncanonical encoding is preserved byte-for-byte": {
			requestURL:    "?other=a%20b&audience=custom",
			cloudConfig:   cloud.AzureGovernment,
			expectedQuery: "other=a%20b&audience=custom",
		},
		"unknown cloud config gets public cloud audience appended": {
			requestURL:    "?foo=bar",
			cloudConfig:   cloud.Configuration{},
			expectedQuery: "audience=api%3A%2F%2FAzureADTokenExchange&foo=bar",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var gotQuery string
			var gotHeaders http.Header
			server := newTokenServer(t, func(w http.ResponseWriter, r *http.Request) {
				gotQuery = r.URL.RawQuery
				gotHeaders = r.Header.Clone()
				w.Header().Set("Content-Type", "application/json")
				if _, err := w.Write([]byte(`{"value":"fetched-oidc-token"}`)); err != nil {
					t.Errorf("error writing token response: %s", err)
				}
			})

			config := OIDCAuthConfig{
				OIDCRequestURL:   server.URL + tc.requestURL,
				OIDCRequestToken: "actions-token",
			}

			token, err := getTokenFromRemote(httpclient.New(t.Context()), config, tc.cloudConfig)
			if err != nil {
				t.Fatalf("unexpected error fetching token: %s", err)
			}
			if diff := cmp.Diff("fetched-oidc-token", token); diff != "" {
				t.Errorf("returned token mismatch (-want, +got): %s", diff)
			}

			if diff := cmp.Diff(tc.expectedQuery, gotQuery); diff != "" {
				t.Errorf("request query mismatch (-want, +got): %s", diff)
			}
			if got := gotHeaders.Get("Authorization"); got != "Bearer actions-token" {
				t.Errorf("unexpected Authorization header: %q", got)
			}
			if got := gotHeaders.Get("Accept"); got != "application/json; api-version=2.0" {
				t.Errorf("unexpected Accept header: %q", got)
			}
			if got := gotHeaders.Get("Content-Type"); got != "application/json" {
				t.Errorf("unexpected Content-Type header: %q", got)
			}
		})
	}
}

// newTokenServer is an httptest server returning a fixed OIDC token response,
// invoking the given handler before responding.
func newTokenServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}
