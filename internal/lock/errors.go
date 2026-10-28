package lock

import "errors"

var (
	ErrLeaseNotFound   = errors.New("lease not found")
	ErrLeaseExpired    = errors.New("lease expired")
	ErrLockAlreadyHeld = errors.New("lock already held by another lease")
	ErrNotLockOwner    = errors.New("not the lock owner")
	ErrInvalidTTL      = errors.New("invalid TTL: must be greater than zero")
	ErrLockNotFound    = errors.New("lock not found")
	ErrUnknownCommand  = errors.New("unknown command type")
)
