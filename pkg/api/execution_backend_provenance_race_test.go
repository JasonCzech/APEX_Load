package api

import (
	"context"
	"testing"
	"time"
)

type contextBlockingInventoryBackend struct{}

func (contextBlockingInventoryBackend) SystemInventory(ctx context.Context) []GeneratorInventoryEntry {
	<-ctx.Done()
	return nil
}

func TestGeneratorInventoryForProvenanceIsDeadlineBounded(t *testing.T) {
	const timeout = 20 * time.Millisecond
	started := time.Now()
	entries := generatorInventoryForProvenance(contextBlockingInventoryBackend{}, timeout)
	if len(entries) != 0 {
		t.Fatalf("entries = %#v, want none after timeout", entries)
	}
	if elapsed := time.Since(started); elapsed < timeout || elapsed > time.Second {
		t.Fatalf("inventory capture elapsed %s, want bounded near %s", elapsed, timeout)
	}
}

// RN-5: buildRunArchiveProvenance runs in the test-execution goroutine (no tr.mu)
// while the pending-queue refresh mutates ManagedTest fields under tr.mu. This must
// not race — run under `go test -race`.
func TestBuildRunArchiveProvenanceNoRaceWithPendingRefresh(t *testing.T) {
	tr := NewTestRunner(10, NewHub(), nil)
	test := &ManagedTest{ID: "race-run", ProjectID: "proj-race", Status: "RUNNING", CreatedAt: time.Now().UTC()}
	tr.mu.Lock()
	tr.tests[test.ID] = test
	tr.mu.Unlock()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 400; i++ {
			_ = buildRunArchiveProvenance(tr, test)
		}
	}()
	for i := 0; i < 400; i++ {
		tr.mu.Lock()
		tr.refreshPendingQueueMetadataLocked()
		tr.mu.Unlock()
	}
	<-done
}
