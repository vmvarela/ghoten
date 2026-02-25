// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package eval

import (
	"github.com/vmvarela/ghoten/internal/addrs"
	"github.com/vmvarela/ghoten/internal/configs"
	"github.com/vmvarela/ghoten/internal/configs/configschema"
	"github.com/vmvarela/ghoten/internal/lang/eval/internal/evalglue"
	"github.com/vmvarela/ghoten/internal/lang/eval/internal/ghoten2024"
	"github.com/vmvarela/ghoten/internal/providers"
)

// The symbols aliased in this file are defined in [evalglue] really just to
// avoid a dependency between this package and the "compiler" packages
// like ./internal/tofu2024, but we do still need them in our exported API
// here so that other parts of Ghoten can interact with the evaluator.

type EvalContext = evalglue.EvalContext
type ProvidersSchema = evalglue.ProvidersSchema
type ProvisionersSchema = evalglue.ProvisionersSchema
type ExternalModules = evalglue.ExternalModules
type UncompiledModule = evalglue.UncompiledModule

func ModulesForTesting(modules map[addrs.ModuleSourceLocal]*configs.Module) ExternalModules {
	// This one actually lives in tofu2024 because evalglue isn't allowed to
	// depend on tofu2024 itself, but from the caller's perspective this is
	// still presented as an evalglue re-export because the return type belongs
	// to that package.
	return ghoten2024.ModulesForTesting(modules)
}

func ProvidersForTesting(schemas map[addrs.Provider]*providers.GetProviderSchemaResponse) ProvidersSchema {
	return evalglue.ProvidersForTesting(schemas)
}

func ProvisionersForTesting(schemas map[string]*configschema.Block) ProvisionersSchema {
	return evalglue.ProvisionersForTesting(schemas)
}
