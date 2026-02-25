// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package applying

import (
	"context"
	"log"

	"github.com/zclconf/go-cty/cty"

	"github.com/vmvarela/ghoten/internal/engine/internal/exec"
	"github.com/vmvarela/ghoten/internal/lang/eval"
	"github.com/vmvarela/ghoten/internal/tfdiags"
)

// DataRead implements [exec.Operations].
func (ops *execOperations) DataRead(
	ctx context.Context,
	desired *eval.DesiredResourceInstance,
	plannedVal cty.Value,
	providerClient *exec.ProviderClient,
) (*exec.ResourceInstanceObject, tfdiags.Diagnostics) {
	log.Printf("[TRACE] apply phase: DataRead %s using %s", desired.Addr, providerClient.InstanceAddr)
	panic("unimplemented")
}
