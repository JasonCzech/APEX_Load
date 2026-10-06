package storage

import (
	"strconv"
	"sync"
	"testing"
	"time"
)

func acceptanceRecord(id, platform, result string, recordedAt time.Time) *FeatureAcceptanceRecord {
	return &FeatureAcceptanceRecord{ID: id, RequirementID: "BE-4", Feature: "browser-har", Platform: platform, CommitSHA: "abcdef1", Suite: "suite", Result: result, RecordedAt: recordedAt, RecordedBy: "user:admin"}
}

// exerciseFeatureAcceptanceSequence proves "latest" follows insertion order,
// not a node clock, and that concurrent appends take distinct sequences.
func exerciseFeatureAcceptanceSequence(t *testing.T, store *SQLiteStore) {
	t.Helper()
	now := time.Now().UTC()
	// The pass is written by a node whose clock runs ahead; the revoke that
	// follows it carries an earlier time but is still the latest record.
	if err := store.AppendFeatureAcceptanceRecord(acceptanceRecord("pass", "linux", AcceptancePassed, now.Add(5*time.Second))); err != nil {
		t.Fatal(err)
	}
	revoke := acceptanceRecord("revoke", "linux", AcceptanceRevoked, now)
	if err := store.AppendFeatureAcceptanceRecord(revoke); err != nil {
		t.Fatal(err)
	}
	if revoke.Seq < 2 || revoke.Artifacts == nil {
		t.Fatalf("append did not report its sequence and artifacts: %#v", revoke)
	}
	latest, truncated, err := store.LatestFeatureAcceptanceRecords("BE-4", "browser-har", 10)
	if err != nil || truncated || len(latest) != 1 || latest[0].ID != "revoke" {
		t.Fatalf("latest by sequence: %#v %v %v", latest, truncated, err)
	}
	listed, err := store.ListFeatureAcceptanceRecords("BE-4", "browser-har", 10, 0)
	if err != nil || len(listed) != 2 || listed[0].ID != "revoke" {
		t.Fatalf("newest first by sequence: %#v %v", listed, err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := range 12 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs <- store.AppendFeatureAcceptanceRecord(acceptanceRecord("concurrent-"+strconv.Itoa(i), "p"+strconv.Itoa(i), AcceptanceFailed, now))
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent append: %v", err)
		}
	}
	all, err := store.ListFeatureAcceptanceRecords("BE-4", "browser-har", 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int64]bool{}
	for _, record := range all {
		if record.Seq < 1 || seen[record.Seq] {
			t.Fatalf("sequence %d repeated or missing", record.Seq)
		}
		seen[record.Seq] = true
	}
	limited, truncated, err := store.LatestFeatureAcceptanceRecords("BE-4", "", 5)
	if err != nil || !truncated || len(limited) != 5 {
		t.Fatalf("subject limit: %d %v %v", len(limited), truncated, err)
	}
}

func TestFeatureAcceptanceSequence(t *testing.T) {
	exerciseFeatureAcceptanceSequence(t, newAuthoringReceiptTestStore(t))
}
