package lock

import (
	"fmt"
	"sync"
)

type FSM struct {
	mu sync.RWMutex

	locks  map[string]*Lock
	leases map[uint64]*Lease

	fencingCounter uint64
	nextLeaseID    uint64

	clock Clock
}

func NewFSM() *FSM {
	return NewFSMWithClock(realClock{})
}

func NewFSMWithClock(clock Clock) *FSM {
	return &FSM{
		locks:       make(map[string]*Lock),
		leases:      make(map[uint64]*Lease),
		nextLeaseID: 1,
		clock:       clock,
	}
}

func (f *FSM) Apply(cmd Command) (any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	switch cmd.Type {
	case CmdCreateLease:
		return f.applyCreateLease(cmd)
	case CmdRenewLease:
		return f.applyRenewLease(cmd)
	case CmdAcquireLock:
		return f.applyAcquireLock(cmd)
	case CmdReleaseLock:
		return f.applyReleaseLock(cmd)
	case CmdExpireLease:
		return f.applyExpireLease(cmd)
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnknownCommand, cmd.Type)
	}
}

func (f *FSM) applyCreateLease(cmd Command) (*CreateLeaseResponse, error) {
	if cmd.TTL <= 0 {
		return nil, ErrInvalidTTL
	}

	id := f.nextLeaseID
	f.nextLeaseID++

	now := f.clock.Now()
	lease := &Lease{
		ID:        id,
		OwnerID:   cmd.OwnerID,
		TTL:       cmd.TTL,
		ExpiresAt: now.Add(cmd.TTL),
	}
	f.leases[id] = lease

	return &CreateLeaseResponse{
		LeaseID:   id,
		ExpiresAt: lease.ExpiresAt,
	}, nil
}

func (f *FSM) applyRenewLease(cmd Command) (*RenewLeaseResponse, error) {
	lease, exists := f.leases[cmd.LeaseID]
	if !exists {
		return nil, ErrLeaseNotFound
	}

	now := f.clock.Now()
	if !now.Before(lease.ExpiresAt) {
		return nil, ErrLeaseExpired
	}

	lease.ExpiresAt = now.Add(lease.TTL)

	return &RenewLeaseResponse{
		ExpiresAt: lease.ExpiresAt,
		TTL:       lease.TTL,
	}, nil
}

func (f *FSM) applyAcquireLock(cmd Command) (*AcquireLockResponse, error) {
	lease, exists := f.leases[cmd.LeaseID]
	if !exists {
		return nil, ErrLeaseNotFound
	}

	now := f.clock.Now()
	if !now.Before(lease.ExpiresAt) {
		return nil, ErrLeaseExpired
	}

	if lease.OwnerID != cmd.OwnerID {
		return nil, ErrNotLockOwner
	}

	if existing, held := f.locks[cmd.LockName]; held {
		if existing.LeaseID == cmd.LeaseID {
			return &AcquireLockResponse{FencingToken: existing.FencingToken}, nil
		}
		return nil, ErrLockAlreadyHeld
	}

	f.fencingCounter++
	lock := &Lock{
		Name:         cmd.LockName,
		OwnerID:      cmd.OwnerID,
		FencingToken: f.fencingCounter,
		LeaseID:      cmd.LeaseID,
	}
	f.locks[cmd.LockName] = lock

	return &AcquireLockResponse{FencingToken: f.fencingCounter}, nil
}

func (f *FSM) applyReleaseLock(cmd Command) (*ReleaseLockResponse, error) {
	lock, held := f.locks[cmd.LockName]
	if !held {
		return nil, ErrLockNotFound
	}

	if lock.LeaseID != cmd.LeaseID {
		return nil, ErrNotLockOwner
	}

	delete(f.locks, cmd.LockName)
	return &ReleaseLockResponse{Released: true}, nil
}

func (f *FSM) applyExpireLease(cmd Command) (*ExpireLeaseResponse, error) {
	_, exists := f.leases[cmd.LeaseID]
	if !exists {
		return nil, ErrLeaseNotFound
	}

	released := 0
	for name, lock := range f.locks {
		if lock.LeaseID == cmd.LeaseID {
			delete(f.locks, name)
			released++
		}
	}

	delete(f.leases, cmd.LeaseID)

	return &ExpireLeaseResponse{LocksReleased: released}, nil
}

func (f *FSM) GetLock(name string) (*Lock, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	lock, exists := f.locks[name]
	return lock, exists
}

func (f *FSM) GetLease(id uint64) (*Lease, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	lease, exists := f.leases[id]
	return lease, exists
}

func (f *FSM) Stats() Stats {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return Stats{
		ActiveLocks:    len(f.locks),
		ActiveLeases:   len(f.leases),
		FencingCounter: f.fencingCounter,
	}
}

func (f *FSM) GetExpiredLeases() []uint64 {
	f.mu.RLock()
	defer f.mu.RUnlock()

	var expired []uint64
	currentTime := f.clock.Now()
	for id, lease := range f.leases {
		if !currentTime.Before(lease.ExpiresAt) {
			expired = append(expired, id)
		}
	}
	return expired
}

func (f *FSM) CreateSnapshot() *Snapshot {
	f.mu.RLock()
	defer f.mu.RUnlock()

	snap := &Snapshot{
		Locks:          make(map[string]*Lock, len(f.locks)),
		Leases:         make(map[uint64]*Lease, len(f.leases)),
		FencingCounter: f.fencingCounter,
		NextLeaseID:    f.nextLeaseID,
	}

	for name, lock := range f.locks {
		lockCopy := *lock
		snap.Locks[name] = &lockCopy
	}

	for id, lease := range f.leases {
		leaseCopy := *lease
		snap.Leases[id] = &leaseCopy
	}

	return snap
}

func (f *FSM) RestoreSnapshot(snap *Snapshot) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.locks = make(map[string]*Lock, len(snap.Locks))
	for name, lock := range snap.Locks {
		lockCopy := *lock
		f.locks[name] = &lockCopy
	}

	f.leases = make(map[uint64]*Lease, len(snap.Leases))
	for id, lease := range snap.Leases {
		leaseCopy := *lease
		f.leases[id] = &leaseCopy
	}

	f.fencingCounter = snap.FencingCounter
	f.nextLeaseID = snap.NextLeaseID
}
