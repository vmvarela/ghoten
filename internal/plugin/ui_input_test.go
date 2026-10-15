// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package plugin

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/hashicorp/go-plugin"
	"github.com/vmvarela/ghoten/internal/ghoten"
)

func TestUIInput_impl(t *testing.T) {
	var _ ghoten.UIInput = new(UIInput)
}

func TestUIInput_input(t *testing.T) {
	client, server := plugin.TestRPCConn(t)
	defer client.Close()

	i := new(ghoten.MockUIInput)
	i.InputReturnString = "foo"

	err := server.RegisterName("Plugin", &UIInputServer{
		UIInput: i,
	})
	if err != nil {
		t.Fatalf("err: %s", err)
	}

	input := &UIInput{Client: client}

	opts := &ghoten.InputOpts{
		Id: "foo",
	}

	v, err := input.Input(context.Background(), opts)
	if !i.InputCalled {
		t.Fatal("input should be called")
	}
	if !reflect.DeepEqual(i.InputOpts, opts) {
		t.Fatalf("bad: %#v", i.InputOpts)
	}
	if err != nil {
		t.Fatalf("bad: %#v", err)
	}

	if v != "foo" {
		t.Fatalf("bad: %#v", v)
	}
}

// blockingUIInput is a ghoten.UIInput whose Input blocks until released.
type blockingUIInput struct {
	started chan struct{}
	release chan struct{}
}

var _ ghoten.UIInput = (*blockingUIInput)(nil)

func (i *blockingUIInput) Input(ctx context.Context, opts *ghoten.InputOpts) (string, error) {
	close(i.started)
	<-i.release
	return "", nil
}

// TestUIInput_inputContextCancelled checks that Input returns promptly with
// an error wrapping context.Canceled when the context is cancelled while the
// RPC is still blocked server-side, instead of waiting for the RPC itself.
func TestUIInput_inputContextCancelled(t *testing.T) {
	client, server := plugin.TestRPCConn(t)
	defer client.Close()

	i := &blockingUIInput{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	defer close(i.release) // unblock the server-side RPC goroutine

	err := server.RegisterName("Plugin", &UIInputServer{
		UIInput: i,
	})
	if err != nil {
		t.Fatalf("err: %s", err)
	}

	input := &UIInput{Client: client}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := input.Input(ctx, &ghoten.InputOpts{Id: "foo"})
		done <- err
	}()

	// Wait until the RPC is actually blocked server-side, then cancel.
	<-i.started
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got: %#v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Input did not return promptly after context cancellation")
	}
}
