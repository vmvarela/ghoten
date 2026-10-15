// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package simple_test

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/vmvarela/ghoten/internal/configs/configschema"
	simplev5 "github.com/vmvarela/ghoten/internal/provider-simple"
	simplev6 "github.com/vmvarela/ghoten/internal/provider-simple-v6"
	"github.com/vmvarela/ghoten/internal/providers"
	"github.com/zclconf/go-cty/cty"
)

// Comprueba que solo los recursos administrados expongan argumentos de solo
// escritura y que cada tipo de esquema tenga su propio mapa de atributos.

func wantManagedBlock() *configschema.Block {
	return &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id": {
				Computed: true,
				Type:     cty.String,
			},
			"value": {
				Optional: true,
				Type:     cty.String,
			},
			"value_wo": {
				Optional:  true,
				Type:      cty.String,
				WriteOnly: true,
			},
		},
		BlockTypes: map[string]*configschema.NestedBlock{
			"nested_block": {
				Nesting: configschema.NestingSingle,
				Block: configschema.Block{
					Attributes: map[string]*configschema.Attribute{
						"nested_block_attr": {
							Type:      cty.String,
							Optional:  true,
							WriteOnly: true,
						},
					},
				},
			},
		},
	}
}

func wantPlainBlock() *configschema.Block {
	return &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id": {
				Computed: true,
				Type:     cty.String,
			},
			"value": {
				Optional: true,
				Type:     cty.String,
			},
		},
	}
}

func TestProviderSchemas(t *testing.T) {
	providerImpls := map[string]func() providers.Interface{
		"provider-simple":    simplev5.Provider,
		"provider-simple-v6": simplev6.Provider,
	}

	for name, newProvider := range providerImpls {
		t.Run(name, func(t *testing.T) {
			schema := newProvider().GetProviderSchema(context.Background())

			t.Run("managed-resource-schema", func(t *testing.T) {
				got := schema.ResourceTypes["simple_resource"].Block
				if diff := cmp.Diff(wantManagedBlock(), got, cmp.Comparer(cty.Type.Equals), cmpopts.EquateEmpty()); diff != "" {
					t.Errorf("wrong managed resource schema (-want, +got)\n%s", diff)
				}
			})

			t.Run("data-source-schema", func(t *testing.T) {
				got := schema.DataSources["simple_resource"].Block
				if diff := cmp.Diff(wantPlainBlock(), got, cmp.Comparer(cty.Type.Equals), cmpopts.EquateEmpty()); diff != "" {
					t.Errorf("wrong data source schema (-want, +got)\n%s", diff)
				}
			})

			t.Run("ephemeral-resource-schema", func(t *testing.T) {
				got := schema.EphemeralResources["simple_resource"].Block
				if diff := cmp.Diff(wantPlainBlock(), got, cmp.Comparer(cty.Type.Equals), cmpopts.EquateEmpty()); diff != "" {
					t.Errorf("wrong ephemeral resource schema (-want, +got)\n%s", diff)
				}
			})

			t.Run("role-schemas-are-independent", func(t *testing.T) {
				// La mutación del esquema de datos no debe propagarse a los
				// esquemas efímero ni administrado.
				delete(schema.DataSources["simple_resource"].Block.Attributes, "id")
				schema.DataSources["simple_resource"].Block.Attributes["value"].Optional = false

				if _, ok := schema.EphemeralResources["simple_resource"].Block.Attributes["id"]; !ok {
					t.Error("ephemeral resource lost id after data source schema was mutated")
				}
				if !schema.EphemeralResources["simple_resource"].Block.Attributes["value"].Optional {
					t.Error("ephemeral resource value.Optional changed after data source schema was mutated")
				}
				if !schema.ResourceTypes["simple_resource"].Block.Attributes["value"].Optional {
					t.Error("managed resource value.Optional changed after data source schema was mutated")
				}
				if _, ok := schema.ResourceTypes["simple_resource"].Block.Attributes["value_wo"]; !ok {
					t.Error("managed resource lost value_wo after data source schema was mutated")
				}
			})
		})
	}
}
