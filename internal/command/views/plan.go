// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package views

import (
	"fmt"

	"github.com/vmvarela/ghoten/internal/command/arguments"
	"github.com/vmvarela/ghoten/internal/tfdiags"
	"github.com/vmvarela/ghoten/internal/ghoten"
)

// The Plan view is used for the plan command.
type Plan interface {
	Operation() Operation
	Hooks() []ghoten.Hook

	Diagnostics(diags tfdiags.Diagnostics)
	HelpPrompt()
}

// NewPlan returns an initialized Plan implementation for the given ViewType.
func NewPlan(args arguments.ViewOptions, view *View) Plan {
	var plan Plan
	switch args.ViewType {
	case arguments.ViewJSON:
		plan = &PlanJSON{
			view: NewJSONView(view, nil),
		}
	case arguments.ViewHuman:
		plan = &PlanHuman{
			view:         view,
			inAutomation: view.RunningInAutomation(),
		}
	default:
		panic(fmt.Sprintf("unknown view type %v", args.ViewType))
	}

	if args.JSONInto != nil {
		plan = PlanMulti{plan, &PlanJSON{view: NewJSONView(view, args.JSONInto)}}
	}

	return plan
}

type PlanMulti []Plan

var _ Plan = (PlanMulti)(nil)

func (m PlanMulti) Operation() Operation {
	var operation OperationMulti
	for _, plan := range m {
		operation = append(operation, plan.Operation())
	}
	return operation
}

func (m PlanMulti) Hooks() []ghoten.Hook {
	var hooks []ghoten.Hook
	for _, plan := range m {
		hooks = append(hooks, plan.Hooks()...)
	}
	return hooks
}

func (m PlanMulti) Diagnostics(diags tfdiags.Diagnostics) {
	for _, plan := range m {
		plan.Diagnostics(diags)
	}
}

func (m PlanMulti) HelpPrompt() {
	for _, plan := range m {
		plan.HelpPrompt()
	}
}

// The PlanHuman implementation renders human-readable text logs, suitable for
// a scrolling terminal.
type PlanHuman struct {
	view *View

	inAutomation bool
}

var _ Plan = (*PlanHuman)(nil)

func (v *PlanHuman) Operation() Operation {
	return NewOperation(arguments.ViewHuman, v.inAutomation, v.view)
}

func (v *PlanHuman) Hooks() []ghoten.Hook {
	return []ghoten.Hook{NewUIOptionalHook(v.view)}
}

func (v *PlanHuman) Diagnostics(diags tfdiags.Diagnostics) {
	v.view.Diagnostics(diags)
}

func (v *PlanHuman) HelpPrompt() {
	v.view.HelpPrompt("plan")
}

// The PlanJSON implementation renders streaming JSON logs, suitable for
// integrating with other software.
type PlanJSON struct {
	view *JSONView
}

var _ Plan = (*PlanJSON)(nil)

func (v *PlanJSON) Operation() Operation {
	return &OperationJSON{view: v.view}
}

func (v *PlanJSON) Hooks() []ghoten.Hook {
	return []ghoten.Hook{
		newJSONHook(v.view),
	}
}

func (v *PlanJSON) Diagnostics(diags tfdiags.Diagnostics) {
	v.view.Diagnostics(diags)
}

func (v *PlanJSON) HelpPrompt() {
}
