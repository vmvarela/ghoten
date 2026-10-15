// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package plugin

import (
	"context"
	"fmt"
	"net/rpc"

	"github.com/hashicorp/go-plugin"
	"github.com/vmvarela/ghoten/internal/ghoten"
)

// UIInput is an implementation of ghoten.UIInput that communicates
// over RPC.
type UIInput struct {
	Client *rpc.Client
}

func (i *UIInput) Input(ctx context.Context, opts *ghoten.InputOpts) (string, error) {
	type callResult struct {
		value string
		err   error
	}
	ch := make(chan callResult, 1)
	go func() {
		var resp UIInputInputResponse
		err := i.Client.Call("Plugin.Input", opts, &resp)
		if err == nil && resp.Error != nil {
			err = resp.Error
		}
		ch <- callResult{resp.Value, err}
	}()
	var result callResult
	select {
	case result = <-ch:
	case <-ctx.Done():
		// The user was interrupted while the plugin was asking for input.
		// The answer (if any) is discarded; the buffered channel keeps the
		// RPC results goroutine from leaking.
		return "", fmt.Errorf("interrupted: %w", ctx.Err())
	}
	if result.err != nil {
		return "", result.err
	}
	return result.value, nil
}

type UIInputInputResponse struct {
	Value string
	Error *plugin.BasicError
}

// UIInputServer is a net/rpc compatible structure for serving
// a UIInputServer. This should not be used directly.
type UIInputServer struct {
	UIInput ghoten.UIInput
}

func (s *UIInputServer) Input(
	opts *ghoten.InputOpts,
	reply *UIInputInputResponse) error {
	value, err := s.UIInput.Input(context.Background(), opts)
	*reply = UIInputInputResponse{
		Value: value,
		Error: plugin.NewBasicError(err),
	}

	return nil
}
