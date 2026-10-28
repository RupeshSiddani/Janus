package lock

import "time"

type Lease struct {
	ID        uint64        `json:"id"`
	OwnerID   string        `json:"owner_id"`
	TTL       time.Duration `json:"ttl"`
	ExpiresAt time.Time     `json:"expires_at"`
}

type Lock struct {
	Name         string `json:"name"`
	OwnerID      string `json:"owner_id"`
	FencingToken uint64 `json:"fencing_token"`
	LeaseID      uint64 `json:"lease_id"`
}

type CreateLeaseResponse struct {
	LeaseID   uint64
	ExpiresAt time.Time
}

type RenewLeaseResponse struct {
	ExpiresAt time.Time
	TTL       time.Duration
}

type AcquireLockResponse struct {
	FencingToken uint64
}

type ReleaseLockResponse struct {
	Released bool
}

type ExpireLeaseResponse struct {
	LocksReleased int
}

type Snapshot struct {
	Locks          map[string]*Lock  `json:"locks"`
	Leases         map[uint64]*Lease `json:"leases"`
	FencingCounter uint64            `json:"fencing_counter"`
	NextLeaseID    uint64            `json:"next_lease_id"`
}

type Stats struct {
	ActiveLocks    int
	ActiveLeases   int
	FencingCounter uint64
}

// Clock abstracts time for testability.
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// mockClock allows tests to control time.
type mockClock struct {
	now time.Time
}

func (m *mockClock) Now() time.Time          { return m.now }
func (m *mockClock) Advance(d time.Duration) { m.now = m.now.Add(d) }
