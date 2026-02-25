// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"github.com/vmvarela/ghoten/internal/backend"
	"github.com/vmvarela/ghoten/internal/cloud"
)

const failedToLoadSchemasMessage = `
Warning: Failed to update data for external integrations

Ghoten was unable to generate a description of the updated
state for use with external integrations in the cloud backend.
Any integrations configured for this workspace which depend on
information from the state may not work correctly when using the
result of this action.

This problem occurs when Ghoten cannot read the schema for
one or more of the providers used in the state. The next successful
apply will correct the problem by re-generating the JSON description
of the state:
    ghoten apply
`

func isCloudMode(b backend.Enhanced) bool {
	_, ok := b.(*cloud.Cloud)

	return ok
}
