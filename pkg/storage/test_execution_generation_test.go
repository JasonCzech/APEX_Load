package storage

import (
	"path/filepath"
	"testing"
	"time"
)

func TestTestExecutionGenerationFollowsExecutingTerm(t *testing.T) {
	checkTestExecutionGenerationFollowsExecutingTerm(t, newHAFencingStore(t))
}

// The durable run records the execution term that runs it: the fenced Start
// commit stamps it, adoption by a later term advances it, and nothing else
// writes it. Orphan archive recovery depends on all three.
func checkTestExecutionGenerationFollowsExecutingTerm(t *testing.T, s *SQLiteStore) {
	t.Helper()
	const lease = "execution-generation"
	held, first, err := s.TryAcquireSchedulerLeaseWithGeneration(lease, "first-term", time.Now(), time.Minute)
	if err != nil || !held || first == 0 {
		t.Fatalf("acquire first term = %v %d %v", held, first, err)
	}
	row := &TestRecord{ID: "generation-run", Name: "generation", ProjectID: DefaultProjectID, StartTime: time.Now().UTC().Add(-time.Minute), Status: "QUEUED", ExecutionGeneration: first}
	if err := s.SaveTest(row); err != nil {
		t.Fatal(err)
	}
	generationOf := func() uint64 {
		t.Helper()
		durable, err := s.GetTest(row.ID)
		if err != nil {
			t.Fatal(err)
		}
		return durable.ExecutionGeneration
	}
	if got := generationOf(); got != 0 {
		t.Fatalf("SaveTest wrote execution generation %d", got)
	}

	start := *row
	start.Status = "INITIALIZING"
	start.ExecutionGeneration = first + 1
	if saved, err := s.SaveTestStartIfExecutionLeaseHeldAndNoDeploymentLease(&start, lease, "first-term", DefaultDeploymentLeaseName, "", ""); err != nil || saved {
		t.Fatalf("Start with a generation the lease does not hold = %v %v", saved, err)
	}
	if durable, err := s.GetTest(row.ID); err != nil || durable.Status != "QUEUED" || durable.ExecutionGeneration != 0 {
		t.Fatalf("refused Start changed the row: %+v %v", durable, err)
	}
	start.ExecutionGeneration = first
	if saved, err := s.SaveTestStartIfExecutionLeaseHeldAndNoDeploymentLease(&start, lease, "first-term", DefaultDeploymentLeaseName, "", ""); err != nil || !saved {
		t.Fatalf("fenced Start = %v %v", saved, err)
	}
	if got := generationOf(); got != first {
		t.Fatalf("Start stamped generation %d, want %d", got, first)
	}

	// Full-row metadata writes, including a successor's terminal reconcile
	// built from its own runtime, never move the executing term.
	durable, err := s.GetTest(row.ID)
	if err != nil {
		t.Fatal(err)
	}
	durable.Status = "RUNNING"
	durable.ExecutionGeneration = first + 10
	if err := s.SaveTest(durable); err != nil {
		t.Fatal(err)
	}
	if got := generationOf(); got != first {
		t.Fatalf("SaveTest moved generation to %d", got)
	}

	if err := s.ReleaseSchedulerLease(lease, "first-term"); err != nil {
		t.Fatal(err)
	}
	held, second, err := s.TryAcquireSchedulerLeaseWithGeneration(lease, "second-term", time.Now(), time.Minute)
	if err != nil || !held || second <= first {
		t.Fatalf("acquire second term = %v %d %v", held, second, err)
	}
	if ok, err := s.AdvanceTestExecutionGenerationIfLeaseTermHeld(row.ID, lease, "first-term", first); err != nil || ok {
		t.Fatalf("lost term advanced the run: %v %v", ok, err)
	}
	if ok, err := s.AdvanceTestExecutionGenerationIfLeaseTermHeld(row.ID, lease, "second-term", second+1); err != nil || ok {
		t.Fatalf("unheld generation advanced the run: %v %v", ok, err)
	}
	if ok, err := s.AdvanceTestExecutionGenerationIfLeaseTermHeld(row.ID, lease, "second-term", second); err != nil || !ok {
		t.Fatalf("adopting term = %v %v", ok, err)
	}
	if got := generationOf(); got != second {
		t.Fatalf("adoption stamped generation %d, want %d", got, second)
	}

	ended := time.Now().UTC()
	if _, err := s.UpdateTestStatusIfActive(row.ID, "FAILED", &ended, "lost during failover"); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.AdvanceTestExecutionGenerationIfLeaseTermHeld(row.ID, lease, "second-term", second); err != nil || ok {
		t.Fatalf("terminal run advanced: %v %v", ok, err)
	}
	if ok, err := s.AdvanceTestExecutionGenerationIfLeaseTermHeld("missing-run", lease, "second-term", second); err != nil || ok {
		t.Fatalf("missing run advanced: %v %v", ok, err)
	}
	for _, args := range []struct {
		id, owner  string
		generation uint64
	}{{"", "second-term", second}, {row.ID, "", second}, {row.ID, "second-term", 0}} {
		if ok, err := s.AdvanceTestExecutionGenerationIfLeaseTermHeld(args.id, lease, args.owner, args.generation); err == nil || ok {
			t.Fatalf("incomplete term %+v accepted: %v %v", args, ok, err)
		}
	}
	if got := generationOf(); got != second {
		t.Fatalf("refused writes moved generation to %d", got)
	}
}

func TestTestExecutionGenerationColumnMigratesExistingDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "release-62.db")
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	ended := time.Now().UTC()
	if err := s.SaveTest(&TestRecord{ID: "legacy-run", Name: "legacy", ProjectID: DefaultProjectID, StartTime: ended.Add(-time.Minute), EndTime: &ended, Status: "COMPLETE"}); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`ALTER TABLE tests DROP COLUMN execution_generation`, `DELETE FROM schema_migrations WHERE version >= 63`} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if status, err := s.SchemaStatus(); err != nil || status.AppliedVersion != currentSchemaRelease().Version {
		t.Fatalf("schema status after upgrade = %+v %v", status, err)
	}
	legacy, err := s.GetTest("legacy-run")
	if err != nil || legacy.ExecutionGeneration != 0 || legacy.Status != "COMPLETE" {
		t.Fatalf("pre-upgrade run = %+v %v", legacy, err)
	}
}
