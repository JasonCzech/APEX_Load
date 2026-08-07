package api

import (
	"context"
	"fmt"
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
	createdAt := time.Now().UTC()
	const pendingReason = "waiting for project quota evidence to become available"
	test := &ManagedTest{ID: "race-run", ProjectID: "proj-race", Status: "RUNNING", CreatedAt: createdAt, TotalVUsers: 7, PendingReason: pendingReason}
	tr.mu.Lock()
	tr.tests[test.ID] = test
	tr.mu.Unlock()

	// Scope note: the concurrent mutator below is load-bearing under -race and must
	// not be removed. refreshPendingQueueMetadataWithTimeslotsLocked writes
	// PendingReason and QueuePosition for a non-PENDING test (pkg/api/capacity.go).
	// RunArchiveProvenance has no field of either name, but it does carry
	// PendingReason: provenance.Governance comes from
	// buildRunArchiveGovernanceSnapshot, which reads PendingReason and copies it into
	// Governance.LaunchPreflight.Conflicts. That write/read pair on PendingReason is
	// the memory -race uses to catch a buildRunArchiveProvenance that reads the live
	// *ManagedTest instead of a tr.mu snapshot — with the snapshot removed, every race
	// this test reports is on PendingReason, so dropping the mutator loop (or moving a
	// PendingReason write out from under tr.mu) removes the guard.
	// The value assertions are the secondary, non-race detector. The mutator re-arms
	// PendingReason under the same lock the refresh clears it with, so a snapshot read
	// can only observe a coherent pair: Governance == nil (reason cleared) or a launch
	// preflight carrying exactly that reason. An unlocked read can observe the reason
	// set when the preflight is created and cleared again when its conflicts are
	// built, yielding a preflight with no conflicts that correct code cannot produce.
	// That window is only the few instructions between those two reads, so it needs
	// volume: at this build count it caught a snapshot-less buildRunArchiveProvenance
	// in 20 of 26 measured runs without -race (at 400 builds: 0 of 15), and it never
	// fires against correct code.
	// -race remains the dependable detector, alongside the deterministic
	// TestBuildRunArchiveProvenanceReadsManagedTestUnderLock below.
	const builds = 40000
	described := 0
	var unexpected *RunArchiveProvenance
	torn := ""
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < builds; i++ {
			provenance := buildRunArchiveProvenance(tr, test)
			if detail := incoherentGovernanceDetail(provenance.Governance, pendingReason, "RUNNING"); detail != "" && torn == "" {
				torn = detail
			}
			if provenance.ProjectID == "proj-race" && provenance.RequestedAt.Equal(createdAt) && provenance.TotalVUsers == 7 {
				described++
				continue
			}
			if unexpected == nil {
				captured := provenance
				unexpected = &captured
			}
		}
	}()
	for i := 0; i < builds; i++ {
		tr.mu.Lock()
		// Re-arm the field the refresh clears so every iteration writes it, keeping
		// the write/read pair that -race reports live for the whole loop.
		test.PendingReason = pendingReason
		tr.refreshPendingQueueMetadataWithTimeslotsLocked(time.Now(), nil, nil, nil)
		tr.mu.Unlock()
	}
	<-done

	if torn != "" {
		t.Fatalf("provenance governance could not have come from a single tr.mu snapshot: %s", torn)
	}
	if described != builds {
		detail := "no provenance was built at all"
		if unexpected != nil {
			detail = fmt.Sprintf("first mismatch: project=%q requested_at=%v vusers=%d",
				unexpected.ProjectID, unexpected.RequestedAt, unexpected.TotalVUsers)
		}
		t.Fatalf("provenance described this run %d/%d times, want %d (want %q/%v/7); %s",
			described, builds, builds, "proj-race", createdAt, detail)
	}
}

// incoherentGovernanceDetail reports why a governance snapshot could not have been
// built from a single ManagedTest snapshot taken under tr.mu, for a fixture whose
// only governance input is PendingReason (no runtime-settings review, timeslot,
// execution-plan approval or Error). buildRunArchiveGovernanceSnapshot emits a
// launch preflight only when PendingReason or Error is non-empty and fills its
// conflicts from the same two fields, so with a coherent snapshot the governance is
// either absent or carries exactly that one reason. Anything else means the reader
// saw PendingReason change underneath it.
func incoherentGovernanceDetail(governance *RunArchiveGovernanceSnapshot, pendingReason, status string) string {
	if governance == nil {
		return ""
	}
	preflight := governance.LaunchPreflight
	if preflight == nil {
		return "governance snapshot present without a launch preflight"
	}
	if len(preflight.Conflicts) != 1 || preflight.Conflicts[0] != pendingReason {
		return fmt.Sprintf("launch preflight conflicts %#v, want exactly [%q]", preflight.Conflicts, pendingReason)
	}
	if preflight.Status != status {
		return fmt.Sprintf("launch preflight status %q, want %q", preflight.Status, status)
	}
	return ""
}

// buildRunArchiveProvenance documents (pkg/api/execution_backend.go) that it reads
// the ManagedTest fields from a snapshot taken under tr.mu. The pending-refresh test
// above proves that dependably only under -race (its value check depends on hitting a
// narrow interleaving). This proves it deterministically: a writer holds tr.mu across
// a window in which the ManagedTest is deliberately torn and restores it before
// releasing, so a reader that takes the lock can only ever observe the restored
// state, while a reader that touches the live ManagedTest observes the torn values.
// Correct code cannot fail this regardless of scheduling — the torn state is
// unreachable once the lock is out.
func TestBuildRunArchiveProvenanceReadsManagedTestUnderLock(t *testing.T) {
	tr := NewTestRunner(10, NewHub(), nil)
	createdAt := time.Now().UTC()
	test := &ManagedTest{ID: "lock-run", ProjectID: "proj-lock", Status: "RUNNING", CreatedAt: createdAt, TotalVUsers: 9}
	tr.mu.Lock()
	tr.tests[test.ID] = test
	tr.mu.Unlock()

	torn := make(chan struct{})
	reading := make(chan struct{})
	restored := make(chan struct{})
	go func() {
		defer close(restored)
		tr.mu.Lock()
		test.ProjectID = "proj-torn"
		test.TotalVUsers = 0
		test.CreatedAt = time.Time{}
		close(torn)
		// Hold the torn state until the reader is about to run, then keep it live
		// long enough that an unlocked read is certain to land inside the window.
		<-reading
		time.Sleep(50 * time.Millisecond)
		test.ProjectID = "proj-lock"
		test.TotalVUsers = 9
		test.CreatedAt = createdAt
		tr.mu.Unlock()
	}()

	<-torn
	close(reading)
	provenance := buildRunArchiveProvenance(tr, test)
	<-restored

	if provenance.ProjectID != "proj-lock" || provenance.TotalVUsers != 9 || !provenance.RequestedAt.Equal(createdAt) {
		t.Fatalf("provenance read the live ManagedTest instead of a tr.mu snapshot: project=%q vusers=%d requested_at=%v, want %q/9/%v",
			provenance.ProjectID, provenance.TotalVUsers, provenance.RequestedAt, "proj-lock", createdAt)
	}
}
