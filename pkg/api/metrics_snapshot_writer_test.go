package api

import (
	"context"
	"sync"
	"testing"
	"time"
)

type blockingRunSnapshotStore struct {
	mu           sync.Mutex
	writes       []RunLiveStateSnapshot
	active       int
	maxActive    int
	started      chan RunLiveStateSnapshot
	releaseFirst chan struct{}
}

func (s *blockingRunSnapshotStore) SetRunSnapshot(ctx context.Context, snapshot RunLiveStateSnapshot, _ time.Duration) error {
	s.mu.Lock()
	index := len(s.writes)
	s.active++
	if s.active > s.maxActive {
		s.maxActive = s.active
	}
	s.mu.Unlock()
	s.started <- snapshot
	if index == 0 {
		select {
		case <-s.releaseFirst:
		case <-ctx.Done():
			s.mu.Lock()
			s.active--
			s.mu.Unlock()
			return ctx.Err()
		}
	}
	s.mu.Lock()
	s.writes = append(s.writes, snapshot)
	s.active--
	s.mu.Unlock()
	return nil
}

func (*blockingRunSnapshotStore) GetRunSnapshot(context.Context, string) (*RunLiveStateSnapshot, error) {
	return nil, nil
}

func (*blockingRunSnapshotStore) SetGeneratorInventory(context.Context, []GeneratorInventoryEntry, time.Duration) error {
	return nil
}

func (*blockingRunSnapshotStore) GetGeneratorInventory(context.Context) ([]GeneratorInventoryEntry, error) {
	return nil, nil
}

func TestCacheRunSnapshotCoalescesAndSerializesPerRun(t *testing.T) {
	store := &blockingRunSnapshotStore{
		started:      make(chan RunLiveStateSnapshot, 4),
		releaseFirst: make(chan struct{}),
	}
	test := &ManagedTest{ID: "run-1", Status: "RUNNING"}
	runner := &TestRunner{liveState: store, tests: map[string]*ManagedTest{test.ID: test}}
	base := time.Now().UTC()
	runner.cacheRunSnapshot(RunLiveStateSnapshot{TestID: "run-1", Status: "RUNNING", UpdatedAt: base})
	select {
	case <-store.started:
	case <-time.After(time.Second):
		t.Fatal("first cache write did not start")
	}

	runner.mu.Lock()
	test.Status = "STOPPING"
	runner.mu.Unlock()
	runner.cacheRunSnapshot(RunLiveStateSnapshot{TestID: "run-1", Status: "STOPPING", UpdatedAt: base.Add(time.Second)})
	runner.mu.Lock()
	test.Status = "COMPLETE"
	runner.mu.Unlock()
	runner.cacheRunSnapshot(RunLiveStateSnapshot{TestID: "run-1", Status: "COMPLETE", UpdatedAt: base.Add(2 * time.Second)})
	select {
	case unexpected := <-store.started:
		t.Fatalf("overlapping cache write started: %+v", unexpected)
	case <-time.After(25 * time.Millisecond):
	}
	close(store.releaseFirst)

	select {
	case second := <-store.started:
		if second.Status != "COMPLETE" {
			t.Fatalf("coalesced status = %q, want COMPLETE", second.Status)
		}
	case <-time.After(time.Second):
		t.Fatal("coalesced cache write did not start")
	}

	deadline := time.Now().Add(time.Second)
	for {
		store.mu.Lock()
		writes := append([]RunLiveStateSnapshot(nil), store.writes...)
		maxActive := store.maxActive
		store.mu.Unlock()
		if len(writes) == 2 {
			if writes[0].Status != "RUNNING" || writes[1].Status != "COMPLETE" {
				t.Fatalf("writes = %+v, want RUNNING then COMPLETE", writes)
			}
			if maxActive != 1 {
				t.Fatalf("maximum concurrent writes = %d, want 1", maxActive)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("cache writes did not finish: %+v", writes)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestCacheRunSnapshotDoesNotReplaceNewerPendingState(t *testing.T) {
	store := &blockingRunSnapshotStore{
		started:      make(chan RunLiveStateSnapshot, 4),
		releaseFirst: make(chan struct{}),
	}
	test := &ManagedTest{ID: "run-2", Status: "RUNNING"}
	runner := &TestRunner{liveState: store, tests: map[string]*ManagedTest{test.ID: test}}
	base := time.Now().UTC()
	runner.cacheRunSnapshot(RunLiveStateSnapshot{TestID: "run-2", Status: "RUNNING", UpdatedAt: base})
	<-store.started
	runner.mu.Lock()
	test.Status = "COMPLETE"
	runner.mu.Unlock()
	runner.cacheRunSnapshot(RunLiveStateSnapshot{TestID: "run-2", Status: "COMPLETE", UpdatedAt: base.Add(2 * time.Second)})
	runner.cacheRunSnapshot(RunLiveStateSnapshot{TestID: "run-2", Status: "COMPLETE", UpdatedAt: base.Add(time.Second)})
	close(store.releaseFirst)
	select {
	case second := <-store.started:
		if second.Status != "COMPLETE" {
			t.Fatalf("older pending state replaced terminal state: %+v", second)
		}
	case <-time.After(time.Second):
		t.Fatal("coalesced cache write did not start")
	}
	deadline := time.Now().Add(time.Second)
	for {
		runner.snapshotWriteMu.Lock()
		state := runner.snapshotWrites["run-2"]
		idle := state != nil && !state.writing
		runner.snapshotWriteMu.Unlock()
		if idle {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("snapshot writer did not become idle")
		}
		time.Sleep(time.Millisecond)
	}

	// The watermark remains after the writer drains. A delayed same-status
	// caller must not start a fresh writer that overwrites the newer snapshot.
	runner.cacheRunSnapshot(RunLiveStateSnapshot{TestID: "run-2", Status: "COMPLETE", UpdatedAt: base.Add(time.Second)})
	select {
	case stale := <-store.started:
		t.Fatalf("drained writer accepted stale snapshot: %+v", stale)
	case <-time.After(25 * time.Millisecond):
	}
}
