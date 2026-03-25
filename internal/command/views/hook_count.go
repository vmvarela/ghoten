// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package views

import (
	"sync"

	"github.com/zclconf/go-cty/cty"

	"github.com/vmvarela/ghoten/internal/addrs"
	"github.com/vmvarela/ghoten/internal/ghoten"
	"github.com/vmvarela/ghoten/internal/plans"
	"github.com/vmvarela/ghoten/internal/states"
)

// countHook is a hook that counts the number of resources
// added, removed, changed during the course of an apply.
type countHook struct {
	Added          int
	Changed        int
	Removed        int
	Imported       int
	Forgotten      int
	Refreshed      int // number of resources that completed a refresh (PostRefresh called)
	RefreshSkipped int // number of resources whose refresh was skipped (PostSkipRefresh called)

	ToAdd          int
	ToChange       int
	ToRemove       int
	ToRemoveAndAdd int

	sync.Mutex
	pending map[string]plans.Action

	ghoten.NilHook
}

var _ ghoten.Hook = (*countHook)(nil)

func (h *countHook) Reset() {
	h.Lock()
	defer h.Unlock()

	h.pending = nil
	h.Added = 0
	h.Changed = 0
	h.Removed = 0
	h.Imported = 0
	h.Forgotten = 0
	h.Refreshed = 0
	h.RefreshSkipped = 0
}

func (h *countHook) PreApply(addr addrs.AbsResourceInstance, gen states.Generation, action plans.Action, priorState, plannedNewState cty.Value) (ghoten.HookAction, error) {
	h.Lock()
	defer h.Unlock()

	if h.pending == nil {
		h.pending = make(map[string]plans.Action)
	}

	h.pending[addr.String()] = action

	return ghoten.HookActionContinue, nil
}

func (h *countHook) PostApply(addr addrs.AbsResourceInstance, gen states.Generation, newState cty.Value, err error) (ghoten.HookAction, error) {
	h.Lock()
	defer h.Unlock()

	if h.pending != nil {
		pendingKey := addr.String()
		if action, ok := h.pending[pendingKey]; ok {
			delete(h.pending, pendingKey)

			if err == nil {
				switch action {
				case plans.CreateThenDelete, plans.DeleteThenCreate:
					h.Added++
					h.Removed++
				case plans.Create:
					h.Added++
				case plans.Delete:
					h.Removed++
				case plans.Update:
					h.Changed++

				}
			}
		}
	}

	return ghoten.HookActionContinue, nil
}

func (h *countHook) PostDiff(addr addrs.AbsResourceInstance, gen states.Generation, action plans.Action, priorState, plannedNewState cty.Value) (ghoten.HookAction, error) {
	h.Lock()
	defer h.Unlock()

	// We don't count anything for data resources and neither for the ephemeral ones.
	if addr.Resource.Resource.Mode == addrs.DataResourceMode || addr.Resource.Resource.Mode == addrs.EphemeralResourceMode {
		return ghoten.HookActionContinue, nil
	}

	switch action {
	case plans.CreateThenDelete, plans.DeleteThenCreate:
		h.ToRemoveAndAdd += 1
	case plans.Create:
		h.ToAdd += 1
	case plans.Delete:
		h.ToRemove += 1
	case plans.Update:
		h.ToChange += 1
	}

	return ghoten.HookActionContinue, nil
}

func (h *countHook) PostApplyImport(addr addrs.AbsResourceInstance, importing plans.ImportingSrc) (ghoten.HookAction, error) {
	h.Lock()
	defer h.Unlock()

	h.Imported++
	return ghoten.HookActionContinue, nil
}

func (h *countHook) PostApplyForget(_ addrs.AbsResourceInstance) (ghoten.HookAction, error) {
	h.Lock()
	defer h.Unlock()

	h.Forgotten++
	return ghoten.HookActionContinue, nil
}

func (h *countHook) PostRefresh(_ addrs.AbsResourceInstance, _ states.Generation, _ cty.Value, _ cty.Value) (ghoten.HookAction, error) {
	h.Lock()
	defer h.Unlock()

	h.Refreshed++
	return ghoten.HookActionContinue, nil
}

// GetRefreshed returns the number of resources refreshed so far.
// This implements the anonymous interface used in backend_plan.go to extract
// the refresh count without creating a circular import.
func (h *countHook) GetRefreshed() int {
	h.Lock()
	defer h.Unlock()
	return h.Refreshed
}

func (h *countHook) PostSkipRefresh(_ addrs.AbsResourceInstance, _ states.Generation) (ghoten.HookAction, error) {
	h.Lock()
	defer h.Unlock()

	h.RefreshSkipped++
	return ghoten.HookActionContinue, nil
}

// GetRefreshSkipped returns the number of resources whose refresh was skipped.
// This implements the anonymous interface used in backend_plan.go.
func (h *countHook) GetRefreshSkipped() int {
	h.Lock()
	defer h.Unlock()
	return h.RefreshSkipped
}
