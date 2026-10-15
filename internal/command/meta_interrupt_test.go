// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"
)

// TestInterrupt_oneEventCancelsAllPrompts proves that a single event on the
// ShutdownCh cancels every concurrently active interruptible context, because
// the command's coordinator has one consumer of the channel instead of the
// contexts competing for each event.
func TestInterrupt_oneEventCancelsAllPrompts(t *testing.T) {
	shutdownCh := make(chan struct{})
	m := &Meta{ShutdownCh: shutdownCh}

	waitDone := func(ctx context.Context) <-chan struct{} {
		ch := make(chan struct{})
		go func() {
			<-ctx.Done()
			close(ch)
		}()
		return ch
	}

	// Two sibling prompt contexts, like the interactive input call sites
	// create when they run concurrently.
	ctxA, cancelA := m.InterruptibleContext(context.Background())
	defer cancelA()
	ctxB, cancelB := m.InterruptibleContext(context.Background())
	defer cancelB()
	doneA := waitDone(ctxA)
	doneB := waitDone(ctxB)

	// The coordinator's watcher is the only receiver, so this single send
	// is guaranteed to reach the it once and cancel both contexts.
	shutdownCh <- struct{}{}

	for name, ch := range map[string]<-chan struct{}{"A": doneA, "B": doneB} {
		select {
		case <-ch:
		case <-time.After(5 * time.Second):
			t.Fatalf("context %s was not cancelled by the single signal", name)
		}
	}
	if err := ctxA.Err(); !errors.Is(err, context.Canceled) {
		t.Fatalf("context A error is %v, want context.Canceled", err)
	}
	if err := ctxB.Err(); !errors.Is(err, context.Canceled) {
		t.Fatalf("context B error is %v, want context.Canceled", err)
	}
}

// TestInterrupt_secondEventFiresFatal proves that a second event fires the
// fatal broadcast exactly once, and that it is not fired before that.
func TestInterrupt_secondEventFiresFatal(t *testing.T) {
	shutdownCh := make(chan struct{})
	m := &Meta{ShutdownCh: shutdownCh}
	graceful, fatal := m.interruptBroadcasts()

	shutdownCh <- struct{}{}
	<-graceful // the first event fired the graceful broadcast

	// The fatal level must not have fired yet.
	select {
	case <-fatal:
		t.Fatal("fatal broadcast fired before a second event")
	case <-time.After(5 * time.Millisecond):
	}

	fatalDone := make(chan struct{})
	go func() {
		<-fatal
		close(fatalDone)
	}()
	if !sendWithin(shutdownCh, 5*time.Second) {
		t.Fatal("watcher was not waiting for a second event")
	}

	select {
	case <-fatalDone:
	case <-time.After(5 * time.Second):
		t.Fatal("second event did not fire the fatal broadcast")
	}
}

// TestInterrupt_copiesShareEventAfterInit proves that copies of a Meta
// made after [Meta.InitInterruptCoordinator] (like the "package main"
// scaffolding does) all share the one coordinator and its single
// ShutdownCh consumer: a single event must cancel interruptible contexts
// on every copy, and no extra consumer may swallow events.
func TestInterrupt_copiesShareEventAfterInit(t *testing.T) {
	shutdownCh := make(chan struct{})
	m := &Meta{ShutdownCh: shutdownCh}
	m.InitInterruptCoordinator()

	// Copies such as the per-command Meta scaffolding makes.
	a, b := *m, *m
	if a.interrupt != b.interrupt || a.interrupt != m.interrupt {
		t.Fatal("copies made after InitInterruptCoordinator do not share one coordinator")
	}

	ctxA, cancelA := a.InterruptibleContext(context.Background())
	defer cancelA()
	ctxB, cancelB := b.InterruptibleContext(context.Background())
	defer cancelB()
	doneCh := make(chan struct{})
	go func() {
		<-ctxA.Done()
		close(doneCh)
	}()

	// This single send must be consumed once and cancel both copies; two
	// independent consumers would leave one of the contexts waiting.
	shutdownCh <- struct{}{}

	select {
	case <-ctxB.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("context B was not cancelled by the single signal")
	}
	select {
	case <-doneCh:
	case <-time.After(5 * time.Second):
		t.Fatal("context A was not cancelled by the single signal")
	}
}

// sendWithin sends one event and reports whether a consumer received it.
func sendWithin(ch chan<- struct{}, d time.Duration) bool {
	select {
	case ch <- struct{}{}:
		return true
	case <-time.After(d):
		return false
	}
}

// TestInterrupt_lateWaiterObservesFirstEvent proves that a waiter created
// after the first event still observes it. Because the broadcasts are
// pre-created (no dynamic registration), the old lost-signal race — a waiter
// registered during the graceful wake-up left without a consumer — cannot
// occur: the late interruptible context observes the already-fired graceful
// broadcast.
func TestInterrupt_lateWaiterObservesFirstEvent(t *testing.T) {
	shutdownCh := make(chan struct{})
	m := &Meta{ShutdownCh: shutdownCh}

	// Create the coordinator (its watcher) before the interrupt, like a
	// command does during its Run.
	m.interruptBroadcasts()

	shutdownCh <- struct{}{}

	// Created only after the first event, like a prompt racing the
	// graceful wake-up.
	ctx, cancel := m.InterruptibleContext(context.Background())
	defer cancel()
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("a waiter created after the first event missed the signal")
	}
}

// TestInterrupt_nilShutdownCh proves that a Meta without a ShutdownCh
// produces uncancelable interrupt behavior, so unit tests without interrupt
// plumbing keep their current behavior.
func TestInterrupt_nilShutdownCh(t *testing.T) {
	m := &Meta{} // no ShutdownCh
	if c := m.interruptCoordinator(); c != nil {
		t.Fatalf("expected no coordinator, got %v", c)
	}
	ctx, cancel := m.InterruptibleContext(context.Background())
	defer cancel()

	// The cancel function must be a no-op.
	cancel()
	if ctx.Err() != nil {
		t.Fatalf("expected an uncancelable context, got %v", ctx.Err())
	}
}

// TestInterrupt_commandFinishesStopsWatcher proves that the coordinator's
// goroutine exits when the command finishes: it must not keep consuming
// the command's ShutdownCh forever.
func TestInterrupt_commandFinishesStopsWatcher(t *testing.T) {
	exited := func() <-chan struct{} {
		// Everything is out of scope by the time the function returns,
		// standing in for the command having finished.
		m := &Meta{ShutdownCh: make(chan struct{})}
		_, cancel := m.InterruptibleContext(context.Background())
		defer cancel()
		return m.interruptCoordinator().exited
	}()

	// Trace the Meta's reachability so the cleanup closing the done
	// channel exists to be collected after the GC's finalizer pass.
	runtime.GC()
	runtime.GC()
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("coordinator goroutine did not exit when the command finished")
	}
}
