package lock

import (
	"encoding/json"
	"testing"
	"time"
)

var baseTime = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

func newTestFSM() (*FSM, *mockClock) {
	clk := &mockClock{now: baseTime}
	return NewFSMWithClock(clk), clk
}

func createLease(t *testing.T, f *FSM, owner string, ttl time.Duration) uint64 {
	t.Helper()
	resp, err := f.Apply(Command{Type: CmdCreateLease, OwnerID: owner, TTL: ttl})
	if err != nil {
		t.Fatalf("createLease: %v", err)
	}
	return resp.(*CreateLeaseResponse).LeaseID
}

func acquireLock(t *testing.T, f *FSM, name, owner string, leaseID uint64) uint64 {
	t.Helper()
	resp, err := f.Apply(Command{Type: CmdAcquireLock, LockName: name, OwnerID: owner, LeaseID: leaseID})
	if err != nil {
		t.Fatalf("acquireLock: %v", err)
	}
	return resp.(*AcquireLockResponse).FencingToken
}

func releaseLock(t *testing.T, f *FSM, name string, leaseID uint64) {
	t.Helper()
	_, err := f.Apply(Command{Type: CmdReleaseLock, LockName: name, LeaseID: leaseID})
	if err != nil {
		t.Fatalf("releaseLock: %v", err)
	}
}

func assertError(t *testing.T, got, want error) {
	t.Helper()
	if got != want {
		t.Fatalf("got error %v, want %v", got, want)
	}
}

func assertEqual[T comparable](t *testing.T, got, want T) {
	t.Helper()
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// === Group 1: CreateLease ===

func TestCreateLease_Success(t *testing.T) {
	f, _ := newTestFSM()
	resp, err := f.Apply(Command{Type: CmdCreateLease, OwnerID: "client-1", TTL: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	r := resp.(*CreateLeaseResponse)
	assertEqual(t, r.LeaseID, uint64(1))
	assertEqual(t, r.ExpiresAt, baseTime.Add(10*time.Second))
	lease, ok := f.GetLease(1)
	if !ok {
		t.Fatal("lease not found")
	}
	assertEqual(t, lease.OwnerID, "client-1")
	assertEqual(t, lease.TTL, 10*time.Second)
}

func TestCreateLease_IDsIncrement(t *testing.T) {
	f, _ := newTestFSM()
	for i := uint64(1); i <= 3; i++ {
		id := createLease(t, f, "owner", 5*time.Second)
		assertEqual(t, id, i)
	}
}

func TestCreateLease_ZeroTTL(t *testing.T) {
	f, _ := newTestFSM()
	_, err := f.Apply(Command{Type: CmdCreateLease, OwnerID: "c", TTL: 0})
	assertError(t, err, ErrInvalidTTL)
	assertEqual(t, f.Stats().ActiveLeases, 0)
}

func TestCreateLease_NegativeTTL(t *testing.T) {
	f, _ := newTestFSM()
	_, err := f.Apply(Command{Type: CmdCreateLease, OwnerID: "c", TTL: -1})
	assertError(t, err, ErrInvalidTTL)
}

func TestCreateLease_MultipleOwners(t *testing.T) {
	f, _ := newTestFSM()
	idA := createLease(t, f, "client-A", 5*time.Second)
	idB := createLease(t, f, "client-B", 5*time.Second)
	if idA == idB {
		t.Fatal("IDs must differ")
	}
	la, _ := f.GetLease(idA)
	lb, _ := f.GetLease(idB)
	assertEqual(t, la.OwnerID, "client-A")
	assertEqual(t, lb.OwnerID, "client-B")
}

// === Group 2: RenewLease ===

func TestRenewLease_Success(t *testing.T) {
	f, clk := newTestFSM()
	id := createLease(t, f, "c", 10*time.Second)
	clk.Advance(5 * time.Second)
	resp, err := f.Apply(Command{Type: CmdRenewLease, LeaseID: id})
	if err != nil {
		t.Fatal(err)
	}
	r := resp.(*RenewLeaseResponse)
	want := baseTime.Add(5*time.Second + 10*time.Second)
	assertEqual(t, r.ExpiresAt, want)
}

func TestRenewLease_NotFound(t *testing.T) {
	f, _ := newTestFSM()
	_, err := f.Apply(Command{Type: CmdRenewLease, LeaseID: 999})
	assertError(t, err, ErrLeaseNotFound)
}

func TestRenewLease_Expired(t *testing.T) {
	f, clk := newTestFSM()
	id := createLease(t, f, "c", 5*time.Second)
	clk.Advance(5 * time.Second)
	_, err := f.Apply(Command{Type: CmdRenewLease, LeaseID: id})
	assertError(t, err, ErrLeaseExpired)
}

func TestRenewLease_MultipleRenewals(t *testing.T) {
	f, clk := newTestFSM()
	id := createLease(t, f, "c", 10*time.Second)
	for i := 0; i < 3; i++ {
		clk.Advance(5 * time.Second)
		_, err := f.Apply(Command{Type: CmdRenewLease, LeaseID: id})
		if err != nil {
			t.Fatalf("renewal %d failed: %v", i, err)
		}
	}
	lease, _ := f.GetLease(id)
	if !f.clock.Now().Before(lease.ExpiresAt) {
		t.Fatal("lease should still be valid after renewals")
	}
}

func TestRenewLease_DoesNotChangeOwner(t *testing.T) {
	f, clk := newTestFSM()
	id := createLease(t, f, "original-owner", 10*time.Second)
	clk.Advance(2 * time.Second)
	f.Apply(Command{Type: CmdRenewLease, LeaseID: id})
	lease, _ := f.GetLease(id)
	assertEqual(t, lease.OwnerID, "original-owner")
	assertEqual(t, lease.ID, id)
	assertEqual(t, lease.TTL, 10*time.Second)
}

// === Group 3: AcquireLock ===

func TestAcquireLock_Success(t *testing.T) {
	f, _ := newTestFSM()
	id := createLease(t, f, "c1", 30*time.Second)
	token := acquireLock(t, f, "my-lock", "c1", id)
	assertEqual(t, token, uint64(1))
	lock, ok := f.GetLock("my-lock")
	if !ok {
		t.Fatal("lock not found")
	}
	assertEqual(t, lock.OwnerID, "c1")
	assertEqual(t, lock.LeaseID, id)
}

func TestAcquireLock_FencingTokenIncrements(t *testing.T) {
	f, _ := newTestFSM()
	id := createLease(t, f, "c", 30*time.Second)
	t1 := acquireLock(t, f, "a", "c", id)
	releaseLock(t, f, "a", id)
	t2 := acquireLock(t, f, "a", "c", id)
	t3 := acquireLock(t, f, "b", "c", id)
	if !(t1 < t2 && t2 < t3) {
		t.Fatalf("tokens not monotonic: %d, %d, %d", t1, t2, t3)
	}
}

func TestAcquireLock_Reentrant(t *testing.T) {
	f, _ := newTestFSM()
	id := createLease(t, f, "c", 30*time.Second)
	t1 := acquireLock(t, f, "foo", "c", id)
	t2 := acquireLock(t, f, "foo", "c", id)
	assertEqual(t, t1, t2)
	assertEqual(t, f.Stats().FencingCounter, uint64(1))
}

func TestAcquireLock_Contention(t *testing.T) {
	f, _ := newTestFSM()
	id1 := createLease(t, f, "c1", 30*time.Second)
	id2 := createLease(t, f, "c2", 30*time.Second)
	acquireLock(t, f, "foo", "c1", id1)
	_, err := f.Apply(Command{Type: CmdAcquireLock, LockName: "foo", OwnerID: "c2", LeaseID: id2})
	assertError(t, err, ErrLockAlreadyHeld)
}

func TestAcquireLock_ExpiredLease(t *testing.T) {
	f, clk := newTestFSM()
	id := createLease(t, f, "c", 5*time.Second)
	clk.Advance(5 * time.Second)
	_, err := f.Apply(Command{Type: CmdAcquireLock, LockName: "foo", OwnerID: "c", LeaseID: id})
	assertError(t, err, ErrLeaseExpired)
}

func TestAcquireLock_LeaseNotFound(t *testing.T) {
	f, _ := newTestFSM()
	_, err := f.Apply(Command{Type: CmdAcquireLock, LockName: "foo", OwnerID: "c", LeaseID: 999})
	assertError(t, err, ErrLeaseNotFound)
}

func TestAcquireLock_OwnerMismatch(t *testing.T) {
	f, _ := newTestFSM()
	id := createLease(t, f, "client-A", 30*time.Second)
	_, err := f.Apply(Command{Type: CmdAcquireLock, LockName: "foo", OwnerID: "client-B", LeaseID: id})
	assertError(t, err, ErrNotLockOwner)
}

func TestAcquireLock_MultipleLocksSameLease(t *testing.T) {
	f, _ := newTestFSM()
	id := createLease(t, f, "c", 30*time.Second)
	t1 := acquireLock(t, f, "lock-1", "c", id)
	t2 := acquireLock(t, f, "lock-2", "c", id)
	if t1 == t2 {
		t.Fatal("different locks should have different tokens")
	}
	assertEqual(t, f.Stats().ActiveLocks, 2)
}

func TestAcquireLock_AfterRelease(t *testing.T) {
	f, _ := newTestFSM()
	id1 := createLease(t, f, "c1", 30*time.Second)
	id2 := createLease(t, f, "c2", 30*time.Second)
	t1 := acquireLock(t, f, "foo", "c1", id1)
	releaseLock(t, f, "foo", id1)
	t2 := acquireLock(t, f, "foo", "c2", id2)
	if t2 <= t1 {
		t.Fatalf("new token %d must be > old token %d", t2, t1)
	}
}

func TestAcquireLock_AfterLeaseExpiry(t *testing.T) {
	f, _ := newTestFSM()
	id1 := createLease(t, f, "c1", 30*time.Second)
	acquireLock(t, f, "foo", "c1", id1)
	f.Apply(Command{Type: CmdExpireLease, LeaseID: id1})
	id2 := createLease(t, f, "c2", 30*time.Second)
	token := acquireLock(t, f, "foo", "c2", id2)
	if token <= 1 {
		t.Fatal("token should be > 1 after re-acquire")
	}
}

// === Group 4: ReleaseLock ===

func TestReleaseLock_Success(t *testing.T) {
	f, _ := newTestFSM()
	id := createLease(t, f, "c", 30*time.Second)
	acquireLock(t, f, "foo", "c", id)
	resp, err := f.Apply(Command{Type: CmdReleaseLock, LockName: "foo", LeaseID: id})
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, resp.(*ReleaseLockResponse).Released, true)
	_, ok := f.GetLock("foo")
	if ok {
		t.Fatal("lock should not exist after release")
	}
}

func TestReleaseLock_NotOwner(t *testing.T) {
	f, _ := newTestFSM()
	id1 := createLease(t, f, "c1", 30*time.Second)
	id2 := createLease(t, f, "c2", 30*time.Second)
	acquireLock(t, f, "foo", "c1", id1)
	_, err := f.Apply(Command{Type: CmdReleaseLock, LockName: "foo", LeaseID: id2})
	assertError(t, err, ErrNotLockOwner)
	_, ok := f.GetLock("foo")
	if !ok {
		t.Fatal("lock should still be held")
	}
}

func TestReleaseLock_NotFound(t *testing.T) {
	f, _ := newTestFSM()
	_, err := f.Apply(Command{Type: CmdReleaseLock, LockName: "nonexistent", LeaseID: 1})
	assertError(t, err, ErrLockNotFound)
}

func TestReleaseLock_DoubleRelease(t *testing.T) {
	f, _ := newTestFSM()
	id := createLease(t, f, "c", 30*time.Second)
	acquireLock(t, f, "foo", "c", id)
	releaseLock(t, f, "foo", id)
	_, err := f.Apply(Command{Type: CmdReleaseLock, LockName: "foo", LeaseID: id})
	assertError(t, err, ErrLockNotFound)
}

func TestReleaseLock_DoesNotAffectOtherLocks(t *testing.T) {
	f, _ := newTestFSM()
	id := createLease(t, f, "c", 30*time.Second)
	acquireLock(t, f, "lock-A", "c", id)
	acquireLock(t, f, "lock-B", "c", id)
	releaseLock(t, f, "lock-A", id)
	_, ok := f.GetLock("lock-B")
	if !ok {
		t.Fatal("lock-B should still be held")
	}
}

// === Group 5: ExpireLease ===

func TestExpireLease_NoLocks(t *testing.T) {
	f, _ := newTestFSM()
	id := createLease(t, f, "c", 30*time.Second)
	resp, err := f.Apply(Command{Type: CmdExpireLease, LeaseID: id})
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, resp.(*ExpireLeaseResponse).LocksReleased, 0)
	_, ok := f.GetLease(id)
	if ok {
		t.Fatal("lease should be gone")
	}
}

func TestExpireLease_WithLocks(t *testing.T) {
	f, _ := newTestFSM()
	id := createLease(t, f, "c", 30*time.Second)
	acquireLock(t, f, "a", "c", id)
	acquireLock(t, f, "b", "c", id)
	acquireLock(t, f, "c-lock", "c", id)
	resp, err := f.Apply(Command{Type: CmdExpireLease, LeaseID: id})
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, resp.(*ExpireLeaseResponse).LocksReleased, 3)
	for _, name := range []string{"a", "b", "c-lock"} {
		if _, ok := f.GetLock(name); ok {
			t.Fatalf("lock %s should be released", name)
		}
	}
}

func TestExpireLease_NotFound(t *testing.T) {
	f, _ := newTestFSM()
	_, err := f.Apply(Command{Type: CmdExpireLease, LeaseID: 999})
	assertError(t, err, ErrLeaseNotFound)
}

func TestExpireLease_DoesNotAffectOtherLeases(t *testing.T) {
	f, _ := newTestFSM()
	id1 := createLease(t, f, "c1", 30*time.Second)
	id2 := createLease(t, f, "c2", 30*time.Second)
	acquireLock(t, f, "lock-A", "c1", id1)
	acquireLock(t, f, "lock-B", "c2", id2)
	f.Apply(Command{Type: CmdExpireLease, LeaseID: id1})
	_, ok := f.GetLease(id2)
	if !ok {
		t.Fatal("lease 2 should still exist")
	}
	_, ok = f.GetLock("lock-B")
	if !ok {
		t.Fatal("lock-B should still be held by lease 2")
	}
}

func TestExpireLease_LockReclaimable(t *testing.T) {
	f, _ := newTestFSM()
	id1 := createLease(t, f, "c1", 30*time.Second)
	acquireLock(t, f, "foo", "c1", id1)
	f.Apply(Command{Type: CmdExpireLease, LeaseID: id1})
	id2 := createLease(t, f, "c2", 30*time.Second)
	token := acquireLock(t, f, "foo", "c2", id2)
	if token <= 1 {
		t.Fatal("reclaimed lock must have higher token")
	}
}

// === Group 6: Fencing Token Guarantees ===

func TestFencingToken_StrictlyMonotonic(t *testing.T) {
	f, _ := newTestFSM()
	id := createLease(t, f, "c", 60*time.Second)
	var prev uint64
	for i := 0; i < 5; i++ {
		name := string(rune('a' + i))
		token := acquireLock(t, f, name, "c", id)
		if token <= prev {
			t.Fatalf("token %d not > prev %d", token, prev)
		}
		prev = token
	}
}

func TestFencingToken_SurvivesReleaseReacquire(t *testing.T) {
	f, _ := newTestFSM()
	id := createLease(t, f, "c", 60*time.Second)
	t1 := acquireLock(t, f, "foo", "c", id)
	releaseLock(t, f, "foo", id)
	t2 := acquireLock(t, f, "foo", "c", id)
	if t2 <= t1 {
		t.Fatalf("reacquire token %d must be > original %d", t2, t1)
	}
}

func TestFencingToken_GlobalAcrossLocks(t *testing.T) {
	f, _ := newTestFSM()
	id := createLease(t, f, "c", 60*time.Second)
	t1 := acquireLock(t, f, "lock-A", "c", id)
	t2 := acquireLock(t, f, "lock-B", "c", id)
	assertEqual(t, t1, uint64(1))
	assertEqual(t, t2, uint64(2))
}

func TestFencingToken_ReentrantDoesNotIncrement(t *testing.T) {
	f, _ := newTestFSM()
	id := createLease(t, f, "c", 60*time.Second)
	t1 := acquireLock(t, f, "foo", "c", id)
	t2 := acquireLock(t, f, "foo", "c", id)
	assertEqual(t, t1, t2)
	assertEqual(t, f.Stats().FencingCounter, uint64(1))
}

// === Group 7: Snapshot & Restore ===

func TestSnapshot_EmptyFSM(t *testing.T) {
	f, _ := newTestFSM()
	snap := f.CreateSnapshot()
	f2, _ := newTestFSM()
	data, _ := json.Marshal(snap)
	var restored Snapshot
	json.Unmarshal(data, &restored)
	f2.RestoreSnapshot(&restored)
	assertEqual(t, f2.Stats().ActiveLocks, 0)
	assertEqual(t, f2.Stats().ActiveLeases, 0)
}

func TestSnapshot_WithState(t *testing.T) {
	f, _ := newTestFSM()
	createLease(t, f, "c1", 30*time.Second)
	id2 := createLease(t, f, "c2", 30*time.Second)
	acquireLock(t, f, "x", "c2", id2)
	acquireLock(t, f, "y", "c2", id2)
	acquireLock(t, f, "z", "c2", id2)

	snap := f.CreateSnapshot()
	data, _ := json.Marshal(snap)
	var restored Snapshot
	json.Unmarshal(data, &restored)

	f2, _ := newTestFSM()
	f2.RestoreSnapshot(&restored)
	assertEqual(t, f2.Stats().ActiveLeases, 2)
	assertEqual(t, f2.Stats().ActiveLocks, 3)
	assertEqual(t, f2.Stats().FencingCounter, uint64(3))
}

func TestSnapshot_RestoredFSMIsOperational(t *testing.T) {
	f, _ := newTestFSM()
	id := createLease(t, f, "c", 30*time.Second)
	acquireLock(t, f, "foo", "c", id)

	snap := f.CreateSnapshot()
	data, _ := json.Marshal(snap)
	var restored Snapshot
	json.Unmarshal(data, &restored)

	f2, _ := newTestFSM()
	f2.RestoreSnapshot(&restored)

	newID := createLease(t, f2, "c2", 30*time.Second)
	if newID <= id {
		t.Fatalf("restored FSM lease ID %d should continue from %d", newID, id)
	}
	token := acquireLock(t, f2, "bar", "c2", newID)
	if token <= 1 {
		t.Fatal("restored FSM fencing token should continue")
	}
}

func TestSnapshot_IndependentOfOriginal(t *testing.T) {
	f, _ := newTestFSM()
	id := createLease(t, f, "c", 30*time.Second)
	acquireLock(t, f, "foo", "c", id)

	snap := f.CreateSnapshot()
	data, _ := json.Marshal(snap)
	var restored Snapshot
	json.Unmarshal(data, &restored)

	f2, _ := newTestFSM()
	f2.RestoreSnapshot(&restored)

	newID := createLease(t, f2, "c2", 30*time.Second)
	acquireLock(t, f2, "bar", "c2", newID)

	assertEqual(t, f.Stats().ActiveLocks, 1)
	assertEqual(t, f.Stats().ActiveLeases, 1)
}

// === Group 8: Integration Scenarios ===

func TestScenario_FullLockLifecycle(t *testing.T) {
	f, _ := newTestFSM()
	id := createLease(t, f, "c", 30*time.Second)
	acquireLock(t, f, "job-lock", "c", id)
	releaseLock(t, f, "job-lock", id)
	f.Apply(Command{Type: CmdExpireLease, LeaseID: id})
	s := f.Stats()
	assertEqual(t, s.ActiveLocks, 0)
	assertEqual(t, s.ActiveLeases, 0)
}

func TestScenario_LeaseExpiryReleasesAllLocks(t *testing.T) {
	f, _ := newTestFSM()
	id := createLease(t, f, "c", 30*time.Second)
	acquireLock(t, f, "a", "c", id)
	acquireLock(t, f, "b", "c", id)
	acquireLock(t, f, "c-lock", "c", id)
	resp, _ := f.Apply(Command{Type: CmdExpireLease, LeaseID: id})
	assertEqual(t, resp.(*ExpireLeaseResponse).LocksReleased, 3)
	assertEqual(t, f.Stats().ActiveLocks, 0)
	assertEqual(t, f.Stats().FencingCounter, uint64(3))
}

func TestScenario_ConcurrentLeasesSeparateLocks(t *testing.T) {
	f, _ := newTestFSM()
	idA := createLease(t, f, "A", 30*time.Second)
	idB := createLease(t, f, "B", 30*time.Second)
	acquireLock(t, f, "lock-1", "A", idA)
	acquireLock(t, f, "lock-2", "B", idB)
	assertEqual(t, f.Stats().ActiveLocks, 2)
	releaseLock(t, f, "lock-1", idA)
	_, ok := f.GetLock("lock-2")
	if !ok {
		t.Fatal("lock-2 should still be held")
	}
}

func TestScenario_StaleLeaseCannotAcquire(t *testing.T) {
	f, clk := newTestFSM()
	id := createLease(t, f, "c", 5*time.Second)
	clk.Advance(5 * time.Second)
	_, err := f.Apply(Command{Type: CmdAcquireLock, LockName: "foo", OwnerID: "c", LeaseID: id})
	assertError(t, err, ErrLeaseExpired)
	_, err = f.Apply(Command{Type: CmdRenewLease, LeaseID: id})
	assertError(t, err, ErrLeaseExpired)
	newID := createLease(t, f, "c", 30*time.Second)
	token := acquireLock(t, f, "foo", "c", newID)
	if token == 0 {
		t.Fatal("should acquire with valid token")
	}
}

func TestScenario_FencingTokenOrderAfterExpiry(t *testing.T) {
	f, _ := newTestFSM()
	id1 := createLease(t, f, "c1", 30*time.Second)
	t1 := acquireLock(t, f, "foo", "c1", id1)
	f.Apply(Command{Type: CmdExpireLease, LeaseID: id1})
	id2 := createLease(t, f, "c2", 30*time.Second)
	t2 := acquireLock(t, f, "foo", "c2", id2)
	if t2 <= t1 {
		t.Fatalf("post-expiry token %d must be > pre-expiry token %d", t2, t1)
	}
}

func TestScenario_RapidCreateReleaseCycle(t *testing.T) {
	f, _ := newTestFSM()
	id := createLease(t, f, "c", 600*time.Second)
	for i := 0; i < 100; i++ {
		acquireLock(t, f, "hot-lock", "c", id)
		releaseLock(t, f, "hot-lock", id)
	}
	assertEqual(t, f.Stats().FencingCounter, uint64(100))
	assertEqual(t, f.Stats().ActiveLocks, 0)
}
