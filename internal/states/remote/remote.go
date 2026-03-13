// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package remote

import (
	"context"

	"github.com/vmvarela/ghoten/internal/states/statemgr"
)

// Client is the interface that must be implemented for a remote state
// driver. It supports dumb put/get/delete, and the higher level structs
// handle persisting the state properly here.
type Client interface {
	Get(context.Context) (*Payload, error)
	Put(context.Context, []byte) error
	Delete(context.Context) error
}

// ClientForcePusher is an optional interface that allows a remote
// state to force push by managing a flag on the client that is
// toggled on by a call to EnableForcePush.
type ClientForcePusher interface {
	Client
	EnableForcePush()
}

// ClientLocker is an optional interface that allows a remote state
// backend to enable state lock/unlock.
type ClientLocker interface {
	Client
	statemgr.Locker
}

// OptionalClientLocker is an optional interface that allows callers to
// to determine whether or not locking is actually enabled.
// See OptionalLocker for more details.
type OptionalClientLocker interface {
	ClientLocker
	IsLockingEnabled() bool
}

// ClientRetentionWaiter is an optional interface for remote state clients that
// perform async background work after Put (e.g., version-tag pruning). When a
// client implements this interface, callers must invoke WaitForRetention before
// the process exits to ensure all background work completes.
//
// Pre:  true
// Post: ∀ goroutine G launched by a prior Put call: G has returned
//
//	∧ N(v : v ∈ versionTags : true) ≤ maxVersions  (backend-specific)
type ClientRetentionWaiter interface {
	WaitForRetention()
}

// Payload is the return value from the remote state storage.
type Payload struct {
	MD5  []byte
	Data []byte
}

// Factory is the factory function to create a remote client.
type Factory func(map[string]string) (Client, error)
