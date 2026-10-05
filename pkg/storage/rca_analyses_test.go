package storage

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func rcaFixture(t *testing.T) (*SQLiteStore, time.Time) {
	t.Helper()
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "rca.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	requireProjectFixtures(t, s, "rca-p", "rca-other")
	now := time.Date(2026, 10, 5, 15, 0, 0, 0, time.UTC)
	for _, run := range []*TestRecord{
		{ID: "rca-run", ProjectID: "rca-p", Name: "run", Status: "COMPLETE", StartTime: now},
		{ID: "rca-other-run", ProjectID: "rca-other", Name: "other", Status: "COMPLETE", StartTime: now},
	} {
		if err := s.SaveTest(run); err != nil {
			t.Fatal(err)
		}
	}
	return s, now
}

var digestA, digestB = strings.Repeat("a", 64), strings.Repeat("b", 64)

func TestRCAAnalysisIsIdempotentPerInputDigest(t *testing.T) {
	s, now := rcaFixture(t)
	first, created, err := s.CreateRCAAnalysis("rca-p", "rca-run", digestA, []byte(`{"request":{}}`), now)
	if err != nil || !created || first.Revision != 1 || first.State != RCAStateQueued {
		t.Fatalf("first: %+v %v %v", first, created, err)
	}
	again, created, err := s.CreateRCAAnalysis("rca-p", "rca-run", digestA, []byte(`{"request":{}}`), now.Add(time.Minute))
	if err != nil || created || again.ID != first.ID {
		t.Fatalf("repeat POST created a second analysis: %+v %v %v", again, created, err)
	}
	other, created, err := s.CreateRCAAnalysis("rca-p", "rca-run", digestB, []byte(`{}`), now)
	if err != nil || !created || other.Revision != 2 {
		t.Fatalf("new inputs: %+v %v %v", other, created, err)
	}
	// A cancelled analysis does not block a new one with the same inputs.
	if ok, err := s.CancelRCAAnalysis("rca-p", "rca-run", first.ID, now); err != nil || !ok {
		t.Fatal(ok, err)
	}
	retried, created, err := s.CreateRCAAnalysis("rca-p", "rca-run", digestA, []byte(`{}`), now)
	if err != nil || !created || retried.Revision != 3 {
		t.Fatalf("retry after cancel: %+v %v %v", retried, created, err)
	}
	// Runs of other projects and malformed digests are refused.
	if _, _, err := s.CreateRCAAnalysis("rca-p", "rca-other-run", digestA, []byte(`{}`), now); err == nil {
		t.Fatal("another project's run accepted")
	}
	if _, _, err := s.CreateRCAAnalysis("rca-p", "rca-run", "abc", []byte(`{}`), now); err == nil {
		t.Fatal("malformed digest accepted")
	}
}

func TestRCAOwnerChangeMidJobRecordsOneOutcome(t *testing.T) {
	s, now := rcaFixture(t)
	analysis, _, err := s.CreateRCAAnalysis("rca-p", "rca-run", digestA, []byte(`{}`), now)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimRCAAnalysis("controller-a", now, time.Minute)
	if err != nil || claimed == nil || claimed.ID != analysis.ID || claimed.State != RCAStateRunning {
		t.Fatalf("claim: %+v %v", claimed, err)
	}
	if again, err := s.ClaimRCAAnalysis("controller-b", now.Add(30*time.Second), time.Minute); err != nil || again != nil {
		t.Fatalf("a live lease was taken over: %+v %v", again, err)
	}
	// controller-a stalls; after its lease expires controller-b takes over.
	takeover, err := s.ClaimRCAAnalysis("controller-b", now.Add(2*time.Minute), time.Minute)
	if err != nil || takeover == nil || takeover.ID != analysis.ID || takeover.Revision != analysis.Revision {
		t.Fatalf("takeover: %+v %v", takeover, err)
	}
	if ok, err := s.CompleteRCAAnalysisFenced(analysis.ID, "controller-b", RCAStateCompleted, []byte(`{"assessment":{"revision":1}}`), now.Add(3*time.Minute), "", "", 0); err != nil || !ok {
		t.Fatal(ok, err)
	}
	// The stalled owner's late result is rejected.
	if ok, err := s.CompleteRCAAnalysisFenced(analysis.ID, "controller-a", RCAStateCompleted, []byte(`{"assessment":{"revision":99}}`), now.Add(4*time.Minute), "", "", 0); err != nil || ok {
		t.Fatalf("stale owner completed: %v %v", ok, err)
	}
	latest, err := s.LatestCompletedRCAAnalysis("rca-p", "rca-run")
	if err != nil || latest == nil || latest.ID != analysis.ID || !strings.Contains(string(latest.Payload), `"revision":1`) || latest.CompletedAt == nil {
		t.Fatalf("latest: %+v %v", latest, err)
	}
	// A cancelled job's result is dropped too.
	cancelled, _, _ := s.CreateRCAAnalysis("rca-p", "rca-run", digestB, []byte(`{}`), now)
	if _, err := s.ClaimRCAAnalysis("controller-b", now.Add(5*time.Minute), time.Minute); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.CancelRCAAnalysis("rca-p", "rca-run", cancelled.ID, now.Add(5*time.Minute)); !ok {
		t.Fatal("running analysis not cancelled")
	}
	if ok, err := s.CompleteRCAAnalysisFenced(cancelled.ID, "controller-b", RCAStateCompleted, []byte(`{}`), now.Add(6*time.Minute), "", "", 0); err != nil || ok {
		t.Fatalf("cancelled analysis completed: %v %v", ok, err)
	}
	if got, _ := s.GetRCAAnalysis("rca-p", "rca-run", cancelled.ID); got.State != RCAStateCancelled {
		t.Fatalf("state %s", got.State)
	}
}

func TestRCAEvidenceRevisionsAppend(t *testing.T) {
	s, now := rcaFixture(t)
	if latest, err := s.LatestRCAEvidenceRevision("rca-p", "rca-run"); err != nil || latest != nil {
		t.Fatalf("no evidence yet: %+v %v", latest, err)
	}
	for i := 1; i <= 2; i++ {
		payload, _ := json.Marshal(map[string]int{"capture": i})
		revision, err := s.AppendRCAEvidenceRevision("rca-p", "rca-run", payload, now, nil)
		if err != nil || revision.Revision != int64(i) {
			t.Fatalf("revision %d: %+v %v", i, revision, err)
		}
	}
	latest, err := s.LatestRCAEvidenceRevision("rca-p", "rca-run")
	if err != nil || latest.Revision != 2 || string(latest.Payload) != `{"capture":2}` {
		t.Fatalf("latest: %+v %v", latest, err)
	}
	if first, err := s.GetRCAEvidenceRevision("rca-p", "rca-run", 1); err != nil || string(first.Payload) != `{"capture":1}` {
		t.Fatalf("first: %+v %v", first, err)
	}
	if _, err := s.AppendRCAEvidenceRevision("rca-p", "rca-run", []byte(`not json`), now, nil); err == nil {
		t.Fatal("invalid evidence accepted")
	}
}

func countRCARows(t *testing.T, s *SQLiteStore, test string) int {
	t.Helper()
	var analyses, evidence int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM rca_analyses WHERE test_id = ?`, test).Scan(&analyses); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM rca_evidence_revisions WHERE test_id = ?`, test).Scan(&evidence); err != nil {
		t.Fatal(err)
	}
	return analyses + evidence
}

func TestRCAHistoryFollowsTheRunTheProjectAndTheArchive(t *testing.T) {
	seed := func(t *testing.T) (*SQLiteStore, time.Time) {
		s, now := rcaFixture(t)
		if _, err := s.AppendRCAEvidenceRevision("rca-p", "rca-run", []byte(`{}`), now, nil); err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.CreateRCAAnalysis("rca-p", "rca-run", digestA, []byte(`{}`), now); err != nil {
			t.Fatal(err)
		}
		if countRCARows(t, s, "rca-run") != 2 {
			t.Fatal("seed rows missing")
		}
		return s, now
	}
	t.Run("run deletion", func(t *testing.T) {
		s, _ := seed(t)
		if err := s.DeleteTest("rca-run"); err != nil {
			t.Fatal(err)
		}
		if n := countRCARows(t, s, "rca-run"); n != 0 {
			t.Fatalf("%d rows survived the run", n)
		}
	})
	t.Run("project deletion", func(t *testing.T) {
		s, _ := seed(t)
		if err := s.DeleteProject("rca-p"); err != nil {
			t.Fatal(err)
		}
		if n := countRCARows(t, s, "rca-run"); n != 0 {
			t.Fatalf("%d rows survived the project", n)
		}
	})
	t.Run("archive retention", func(t *testing.T) {
		s, now := seed(t)
		record := &RunArchiveRecord{TestID: "rca-run", SchemaVersion: "apexload.archive.v5", ArchivePath: "/archive/rca-run", ManifestPath: "/archive/rca-run/manifest.json", CreatedAt: now, CompletedAt: now}
		if err := s.SaveRunArchive(record); err != nil {
			t.Fatal(err)
		}
		saved, err := s.GetRunArchive("rca-run")
		if err != nil {
			t.Fatal(err)
		}
		if ok, err := s.DeleteRunArchiveIfUnchanged(saved); err != nil || !ok {
			t.Fatal(ok, err)
		}
		if n := countRCARows(t, s, "rca-run"); n != 0 {
			t.Fatalf("%d rows survived the archive", n)
		}
	})
}

func TestRCAEvidenceReuseAndLeaseRenewal(t *testing.T) {
	s, now := rcaFixture(t)
	sameArchive := func(archive string) func(*RCAEvidenceRevision) bool {
		return func(latest *RCAEvidenceRevision) bool {
			return strings.Contains(string(latest.Payload), `"archive":"`+archive+`"`)
		}
	}
	first, err := s.AppendRCAEvidenceRevision("rca-p", "rca-run", []byte(`{"archive":"a","at":1}`), now, sameArchive("a"))
	if err != nil || first.Revision != 1 {
		t.Fatal(first, err)
	}
	reused, err := s.AppendRCAEvidenceRevision("rca-p", "rca-run", []byte(`{"archive":"a","at":2}`), now, sameArchive("a"))
	if err != nil || reused.Revision != 1 || string(reused.Payload) != `{"archive":"a","at":1}` {
		t.Fatalf("same evidence was not reused: %+v %v", reused, err)
	}
	if changed, err := s.AppendRCAEvidenceRevision("rca-p", "rca-run", []byte(`{"archive":"b"}`), now, sameArchive("b")); err != nil || changed.Revision != 2 {
		t.Fatalf("changed evidence: %+v %v", changed, err)
	}

	analysis, _, _ := s.CreateRCAAnalysis("rca-p", "rca-run", digestA, []byte(`{}`), now)
	if _, err := s.ClaimRCAAnalysis("owner", now, time.Minute); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.RenewRCAAnalysis(analysis.ID, "owner", now.Add(50*time.Second), time.Minute); err != nil || !ok {
		t.Fatal(ok, err)
	}
	// Renewed, the lease survives past its first expiry.
	if other, err := s.ClaimRCAAnalysis("other", now.Add(90*time.Second), time.Minute); err != nil || other != nil {
		t.Fatalf("renewed lease taken over: %+v %v", other, err)
	}
	if ok, _ := s.RenewRCAAnalysis(analysis.ID, "not-the-owner", now, time.Minute); ok {
		t.Fatal("another owner renewed the lease")
	}
}

func TestRCACompletionIsFencedByTheExecutionTerm(t *testing.T) {
	s, now := rcaFixture(t)
	analysis, _, _ := s.CreateRCAAnalysis("rca-p", "rca-run", digestA, []byte(`{}`), now)
	if _, err := s.ClaimRCAAnalysis("job-owner", time.Now(), time.Hour); err != nil {
		t.Fatal(err)
	}
	ok, lease, err := s.TryAcquireSchedulerLeaseTerm("controller-execution-leader", "leader-1", time.Now(), time.Minute)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	// A deposed term records nothing; the current term does.
	if done, err := s.CompleteRCAAnalysisFenced(analysis.ID, "job-owner", RCAStateCompleted, []byte(`{}`), now, "controller-execution-leader", "leader-1", lease.Generation+1); err != nil || done {
		t.Fatalf("stale term completed: %v %v", done, err)
	}
	if done, err := s.CompleteRCAAnalysisFenced(analysis.ID, "job-owner", RCAStateCompleted, []byte(`{}`), now, "controller-execution-leader", "leader-1", lease.Generation); err != nil || !done {
		t.Fatalf("current term: %v %v", done, err)
	}
}

func TestRCALegacyRunWithoutProjectBelongsToTheDefaultProject(t *testing.T) {
	s, now := rcaFixture(t)
	if err := s.SaveTest(&TestRecord{ID: "legacy-run", Name: "legacy", Status: "COMPLETE", StartTime: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE tests SET project_id = NULL WHERE id = ?`, "legacy-run"); err != nil {
		t.Fatal(err)
	}
	if _, created, err := s.CreateRCAAnalysis(DefaultProjectID, "legacy-run", digestA, []byte(`{}`), now); err != nil || !created {
		t.Fatalf("legacy run: %v %v", created, err)
	}
}
