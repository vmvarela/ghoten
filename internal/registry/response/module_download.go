// Copyright (c) Ghoten
// SPDX-License-Identifier: MPL-2.0

package response

// ModuleLocationRegistryResp defines the Ghoten registry response
// returned when calling the endpoint /v1/modules/:namespace/:name/:system/:version/download
type ModuleLocationRegistryResp struct {
	// The URL to download the module from.
	Location string `json:"location"`
}
