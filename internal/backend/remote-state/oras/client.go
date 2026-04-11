package oras

import (
	"context"
	"errors"

	oraslib "github.com/vmvarela/ghoten-oras-backend/backend/oras"
	"github.com/vmvarela/ghoten/internal/states/remote"
	"github.com/vmvarela/ghoten/internal/states/statemgr"
)

// RemoteClient adapts oraslib.StateMgr to ghoten's remote state interfaces.
type RemoteClient struct {
	mgr oraslib.StateMgr
}

var _ remote.Client = (*RemoteClient)(nil)
var _ remote.ClientLocker = (*RemoteClient)(nil)
var _ remote.ClientRetentionWaiter = (*RemoteClient)(nil)

func (c *RemoteClient) Get(ctx context.Context) (*remote.Payload, error) {
	p, err := c.mgr.Get(ctx)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, nil //nolint:nilnil
	}
	return &remote.Payload{MD5: p.MD5, Data: p.Data}, nil
}

func (c *RemoteClient) Put(ctx context.Context, data []byte) error {
	return c.mgr.Put(ctx, data)
}

func (c *RemoteClient) Delete(ctx context.Context) error {
	return c.mgr.Delete(ctx)
}

func (c *RemoteClient) Lock(ctx context.Context, info *statemgr.LockInfo) (string, error) {
	id, err := c.mgr.Lock(ctx, toLockInfo(info))
	if err != nil {
		var lockErr *oraslib.LockError
		if errors.As(err, &lockErr) {
			return "", &statemgr.LockError{
				Info:             fromLockInfo(lockErr.Info),
				Err:              lockErr.Err,
				InconsistentRead: lockErr.InconsistentRead,
			}
		}
		return "", err
	}
	return id, nil
}

func (c *RemoteClient) Unlock(ctx context.Context, id string) error {
	return c.mgr.Unlock(ctx, id)
}

func (c *RemoteClient) WaitForRetention() {
	c.mgr.WaitForRetention()
}

func toLockInfo(info *statemgr.LockInfo) *oraslib.LockInfo {
	if info == nil {
		return nil
	}
	return &oraslib.LockInfo{
		ID:        info.ID,
		Operation: info.Operation,
		Info:      info.Info,
		Who:       info.Who,
		Version:   info.Version,
		Created:   info.Created,
		Path:      info.Path,
	}
}

func fromLockInfo(info *oraslib.LockInfo) *statemgr.LockInfo {
	if info == nil {
		return nil
	}
	return &statemgr.LockInfo{
		ID:        info.ID,
		Operation: info.Operation,
		Info:      info.Info,
		Who:       info.Who,
		Version:   info.Version,
		Created:   info.Created,
		Path:      info.Path,
	}
}
