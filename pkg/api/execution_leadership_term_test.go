package api

import (
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

type databaseTermLeaseStore struct {
	mu           sync.Mutex
	lease        storage.SchedulerLease
	afterAcquire func()
	releases     []string
}

func (s *databaseTermLeaseStore) TryAcquireSchedulerLease(string, string, time.Time, time.Duration) (bool, error) {
	panic("generation-aware term method should be used")
}

func (s *databaseTermLeaseStore) TryAcquireSchedulerLeaseTerm(name, owner string, _ time.Time, _ time.Duration) (bool, *storage.SchedulerLease, error) {
	s.mu.Lock()
	lease := s.lease
	lease.Name = name
	lease.OwnerID = owner
	after := s.afterAcquire
	s.mu.Unlock()
	if after != nil {
		after()
	}
	return true, &lease, nil
}

func (s *databaseTermLeaseStore) ReleaseSchedulerLease(_ string, owner string) error {
	s.mu.Lock()
	s.releases = append(s.releases, owner)
	s.mu.Unlock()
	return nil
}

func TestExecutionLeadershipUsesAtomicDurableGenerationTerm(t *testing.T) {
	localNow := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	store := &databaseTermLeaseStore{lease: storage.SchedulerLease{
		Generation: 42,
		UpdatedAt:  localNow,
		ExpiresAt:  localNow.Add(time.Minute),
	}}
	leadership := newExecutionLeadership(store, "controller-a")
	leadership.ttl = 30 * time.Second
	leadership.now = func() time.Time { return localNow }
	leadership.tick()
	owner, generation := leadership.executionLeaseTerm()
	if owner == "" || generation != 42 {
		t.Fatalf("execution lease term = (%q, %d), want non-empty/42", owner, generation)
	}
}

func TestExecutionLeadershipDelayedTermResponseCannotExtendLocalDeadline(t *testing.T) {
	requestStart := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	localNow := requestStart
	store := &databaseTermLeaseStore{lease: storage.SchedulerLease{
		Generation: 9,
		// Simulate a database clock far ahead. Its absolute expiry must never
		// be used as the controller's local freshness deadline.
		UpdatedAt: requestStart.Add(24 * time.Hour),
		ExpiresAt: requestStart.Add(24*time.Hour + time.Minute),
	}}
	store.afterAcquire = func() { localNow = requestStart.Add(31 * time.Second) }
	leadership := newExecutionLeadership(store, "controller-delayed")
	leadership.ttl = 30 * time.Second
	leadership.now = func() time.Time { return localNow }
	leadership.tick()
	if leadership.IsLeader() {
		t.Fatal("delayed response admitted leadership after the conservative local deadline")
	}
	if owner, generation := leadership.executionLeaseTerm(); owner != "" || generation != 0 {
		t.Fatalf("expired delayed term = (%q, %d), want empty", owner, generation)
	}
	store.mu.Lock()
	released := len(store.releases) == 1
	store.mu.Unlock()
	if !released {
		t.Fatal("expired delayed acquisition was not released")
	}
}

func TestExecutionLeadershipDatabaseClockSkewCannotOutliveLocalTTL(t *testing.T) {
	requestStart := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	localNow := requestStart
	store := &databaseTermLeaseStore{lease: storage.SchedulerLease{
		Generation: 13,
		UpdatedAt:  requestStart.Add(12 * time.Hour),
		ExpiresAt:  requestStart.Add(12*time.Hour + 30*time.Second),
	}}
	leadership := newExecutionLeadership(store, "controller-skew")
	leadership.ttl = 30 * time.Second
	leadership.now = func() time.Time { return localNow }
	leadership.tick()
	if !leadership.IsLeader() {
		t.Fatal("leadership was not admitted before its local deadline")
	}
	localNow = requestStart.Add(30 * time.Second)
	if leadership.IsLeader() {
		t.Fatal("database clock skew extended leadership beyond the local TTL")
	}
}

func TestExecutionLeadershipRejectsInvalidDurableGeneration(t *testing.T) {
	now := time.Now().UTC()
	store := &databaseTermLeaseStore{lease: storage.SchedulerLease{
		Generation: 0,
		UpdatedAt:  now,
		ExpiresAt:  now.Add(time.Minute),
	}}
	leadership := newExecutionLeadership(store, "controller-invalid-generation")
	leadership.now = func() time.Time { return now }
	leadership.tick()
	if leadership.IsLeader() {
		t.Fatal("leadership admitted a database term with generation zero")
	}
	store.mu.Lock()
	released := len(store.releases) == 1
	store.mu.Unlock()
	if !released {
		t.Fatal("invalid generation term was not released")
	}
}
