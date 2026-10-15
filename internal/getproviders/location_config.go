// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package getproviders

import "time"

// LocationConfig provides configuration options
// to be carried from the [Source] to the [PackageLocation] where applicable.
type LocationConfig struct {
	// ProviderDownloadRetries is used for [PackageHTTPURL] to configure the
	// http client to retry when a 5xx retryable error occurs.
	ProviderDownloadRetries int

	// TODO - use this when we'll introduce per installation method configuration
	ProviderDownloadTimeout time.Duration

	// TrustAllHashes is set if the user has explicitly opted into trusting
	// that this source reports accurate hashes for every platform for which
	// it provides packages, not just the platform that is currently being
	// installed. Opt-in only, via network_mirror `trust_all_hashes`; the
	// zero value preserves the pre-existing behavior.
	TrustAllHashes bool
}
