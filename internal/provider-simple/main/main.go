// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"github.com/vmvarela/ghoten/internal/grpcwrap"
	"github.com/vmvarela/ghoten/internal/plugin"
	simple "github.com/vmvarela/ghoten/internal/provider-simple"
	"github.com/vmvarela/ghoten/internal/tfplugin5"
)

func main() {
	plugin.Serve(&plugin.ServeOpts{
		GRPCProviderFunc: func() tfplugin5.ProviderServer {
			return grpcwrap.Provider(simple.Provider())
		},
	})
}
