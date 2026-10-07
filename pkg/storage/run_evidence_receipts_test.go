package storage

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func evidenceReceiptFixture(test, source, generation string, executionGeneration uint64) RunEvidenceReceipt {
	return RunEvidenceReceipt{TestID: test, ExecutionGeneration: executionGeneration, SourceIdentity: source, Target: "gen-1:9090", SourceGeneration: generation, Sequence: 1, Kind: EvidenceKindHARNative, Name: "browser_har_" + generation + ".har", SHA256: strings.Repeat("a", 64), Bytes: 42}
}

func testRunEvidenceReceipts(t *testing.T, s *SQLiteStore) {
	t.Helper()
	now := time.Now().UTC()
	for _, id := range []string{"evidence-run", "archived-run"} {
		if err := s.SaveTest(&TestRecord{ID: id, Name: id, Status: "COMPLETE", StartTime: now, EndTime: &now}); err != nil {
			t.Fatal(err)
		}
	}
	accept := func(r RunEvidenceReceipt, want string) {
		t.Helper()
		state, err := s.AcceptRunEvidenceReceipt(r)
		if err != nil || state != want {
			t.Fatalf("accept %s/%s: state %q err %v, want %q", r.SourceIdentity, r.SourceGeneration, state, err, want)
		}
	}
	first := evidenceReceiptFixture("evidence-run", "source-a", "g1", 2)
	accept(first, EvidenceReceiptAccepted)
	// A repeated upload of the same artifact is idempotent.
	accept(first, EvidenceReceiptAccepted)
	changed := first
	changed.SHA256 = strings.Repeat("b", 64)
	if _, err := s.AcceptRunEvidenceReceipt(changed); !errors.Is(err, ErrEvidenceReceiptConflict) {
		t.Fatalf("changed content for one artifact: %v", err)
	}
	// An older execution generation is fenced.
	stale := evidenceReceiptFixture("evidence-run", "source-b", "g2", 1)
	accept(stale, EvidenceReceiptTombstoned)
	second := evidenceReceiptFixture("evidence-run", "source-b", "g3", 2)
	accept(second, EvidenceReceiptAccepted)
	if n, err := s.PublishRunEvidenceReceipts("evidence-run"); err != nil || n != 2 {
		t.Fatalf("published %d: %v", n, err)
	}
	// A published receipt is final.
	accept(first, EvidenceReceiptPublished)
	// The stale generation's upload was refused without a row.
	receipts, err := s.ListRunEvidenceReceipts("evidence-run")
	if err != nil || len(receipts) != 2 {
		t.Fatalf("receipts %+v %v", receipts, err)
	}
	states := map[string]string{}
	for _, r := range receipts {
		states[r.SourceGeneration] = r.State
		if r.Kind != EvidenceKindHARNative || r.Bytes != 42 || r.ExecutionGeneration == 0 {
			t.Fatalf("receipt lost its fields: %+v", r)
		}
	}
	if _, recorded := states["g2"]; recorded || states["g1"] != EvidenceReceiptPublished || states["g3"] != EvidenceReceiptPublished {
		t.Fatalf("states %v", states)
	}
	// After the archive is published an upload is tombstoned, and deleting
	// the archive tombstones what it held.
	if err := s.SaveRunArchive(&RunArchiveRecord{TestID: "archived-run", ArchiveBackend: "filesystem", ArchivePath: "/tmp/x", ManifestPath: "/tmp/x/manifest.json", CreatedAt: now, CompletedAt: now}); err != nil {
		t.Fatal(err)
	}
	accept(evidenceReceiptFixture("archived-run", "source-a", "late", 1), EvidenceReceiptTombstoned)
	if err := s.SaveRunArchive(&RunArchiveRecord{TestID: "evidence-run", ArchiveBackend: "filesystem", ArchivePath: "/tmp/y", ManifestPath: "/tmp/y/manifest.json", CreatedAt: now, CompletedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteRunArchive("evidence-run"); err != nil {
		t.Fatal(err)
	}
	receipts, _ = s.ListRunEvidenceReceipts("evidence-run")
	for _, r := range receipts {
		if r.State != EvidenceReceiptTombstoned {
			t.Fatalf("deleted archive kept a live receipt: %+v", r)
		}
	}
	// Retention's guarded archive delete tombstones receipts too.
	if err := s.SaveTest(&TestRecord{ID: "retained-run", Name: "retained", Status: "COMPLETE", StartTime: now, EndTime: &now}); err != nil {
		t.Fatal(err)
	}
	accept(evidenceReceiptFixture("retained-run", "source-a", "g7", 1), EvidenceReceiptAccepted)
	retained := &RunArchiveRecord{TestID: "retained-run", ArchiveBackend: "filesystem", ArchivePath: "/tmp/z", ManifestPath: "/tmp/z/manifest.json", CreatedAt: now, CompletedAt: now}
	if err := s.SaveRunArchive(retained); err != nil {
		t.Fatal(err)
	}
	stored, err := s.GetRunArchive("retained-run")
	if err != nil {
		t.Fatal(err)
	}
	if deleted, err := s.DeleteRunArchiveIfUnchanged(stored); err != nil || !deleted {
		t.Fatalf("retention delete: %v %v", deleted, err)
	}
	if receipts, _ := s.ListRunEvidenceReceipts("retained-run"); len(receipts) != 1 || receipts[0].State != EvidenceReceiptTombstoned {
		t.Fatalf("retention left a live receipt: %+v", receipts)
	}
	// A staging attempt forgets an earlier attempt's accepted claims, and a
	// collection retry without the execution fence (generation 0) inherits
	// the newest generation instead of tombstoning its evidence.
	if err := s.SaveTest(&TestRecord{ID: "retry-run", Name: "retry", Status: "COMPLETE", StartTime: now, EndTime: &now}); err != nil {
		t.Fatal(err)
	}
	accept(evidenceReceiptFixture("retry-run", "source-a", "g10", 3), EvidenceReceiptAccepted)
	accept(evidenceReceiptFixture("retry-run", "source-a", "g11", 0), EvidenceReceiptAccepted)
	if receipts, _ := s.ListRunEvidenceReceipts("retry-run"); len(receipts) != 2 || receipts[1].ExecutionGeneration != 3 {
		t.Fatalf("unfenced retry receipts %+v", receipts)
	}
	// An older generation's attempt (a deposed controller) neither forgets a
	// newer generation's claims nor changes them.
	if n, err := s.ResetAcceptedRunEvidenceReceipts("retry-run", 2); err != nil || n != 0 {
		t.Fatalf("stale reset %d: %v", n, err)
	}
	if state, err := s.AcceptRunEvidenceReceipt(evidenceReceiptFixture("retry-run", "source-a", "g10", 2)); err != nil || state != EvidenceReceiptTombstoned {
		t.Fatalf("stale accept %q %v", state, err)
	}
	if receipts, _ := s.ListRunEvidenceReceipts("retry-run"); len(receipts) != 2 || receipts[0].State != EvidenceReceiptAccepted || receipts[0].ExecutionGeneration != 3 {
		t.Fatalf("a stale attempt changed newer claims: %+v", receipts)
	}
	if n, err := s.ResetAcceptedRunEvidenceReceipts("retry-run", 0); err != nil || n != 2 {
		t.Fatalf("reset %d: %v", n, err)
	}
	if receipts, _ := s.ListRunEvidenceReceipts("retry-run"); len(receipts) != 0 {
		t.Fatalf("reset kept %+v", receipts)
	}
	// Deleting a run tombstones its receipts; the rows outlive it.
	if err := s.SaveTest(&TestRecord{ID: "deleted-run-graph", Name: "deleted", Status: "COMPLETE", StartTime: now, EndTime: &now}); err != nil {
		t.Fatal(err)
	}
	accept(evidenceReceiptFixture("deleted-run-graph", "source-a", "g8", 1), EvidenceReceiptAccepted)
	if err := s.DeleteTest("deleted-run-graph"); err != nil {
		t.Fatal(err)
	}
	if receipts, err := s.ListRunEvidenceReceipts("deleted-run-graph"); err != nil || len(receipts) != 1 || receipts[0].State != EvidenceReceiptTombstoned {
		t.Fatalf("deleted run receipts %+v %v", receipts, err)
	}
	// An unknown run is tombstoned; malformed receipts are refused.
	accept(evidenceReceiptFixture("deleted-run", "source-a", "g9", 1), EvidenceReceiptTombstoned)
	for name, mutate := range map[string]func(*RunEvidenceReceipt){
		"kind":     func(r *RunEvidenceReceipt) { r.Kind = "screenshot" },
		"sha":      func(r *RunEvidenceReceipt) { r.SHA256 = "ABC" },
		"source":   func(r *RunEvidenceReceipt) { r.SourceIdentity = "" },
		"bytes":    func(r *RunEvidenceReceipt) { r.Bytes = -1 },
		"test":     func(r *RunEvidenceReceipt) { r.TestID = " " },
		"name":     func(r *RunEvidenceReceipt) { r.Name = "" },
		"sequence": func(r *RunEvidenceReceipt) { r.Sequence = -1 },
	} {
		bad := evidenceReceiptFixture("evidence-run", "source-a", "bad", 1)
		mutate(&bad)
		if _, err := s.AcceptRunEvidenceReceipt(bad); err == nil {
			t.Fatalf("malformed %s receipt accepted", name)
		}
	}
}

func TestRunEvidenceReceiptsSQLite(t *testing.T) {
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	testRunEvidenceReceipts(t, s)
}

// Completing an archive finalization publishes the receipts accepted for it
// in the same transaction, without a separate publish call.
func TestArchiveFinalizationCompletionPublishesAcceptedReceipts(t *testing.T) {
	testArchiveFinalizationPublishesReceipts(t, newHAFencingStore(t))
}

func testArchiveFinalizationPublishesReceipts(t *testing.T, s *SQLiteStore) {
	t.Helper()
	f := seedArchiveFailureRun(t, s)
	if state, err := s.AcceptRunEvidenceReceipt(evidenceReceiptFixture(f.TestID, "source-a", "g1", 1)); err != nil || state != EvidenceReceiptAccepted {
		t.Fatalf("accept: %q %v", state, err)
	}
	record := RunArchiveRecord{TestID: f.TestID, ArchivePath: "/archive/run", ManifestPath: "/archive/run/manifest.json", CreatedAt: f.StartTime, CompletedAt: f.EndTime}
	job := &ArchiveFinalizationJob{TestID: f.TestID, ProjectID: f.ProjectID, ArchiveRecord: record}
	if err := s.SaveArchiveFinalizationJob(job); err != nil {
		t.Fatal(err)
	}
	if done, err := s.CompleteArchiveFinalization(f.TestID, job.IntentToken, "", &record); err != nil || !done {
		t.Fatalf("complete: %v %v", done, err)
	}
	receipts, err := s.ListRunEvidenceReceipts(f.TestID)
	if err != nil || len(receipts) != 1 || receipts[0].State != EvidenceReceiptPublished {
		t.Fatalf("receipts after completion %+v %v", receipts, err)
	}
}
