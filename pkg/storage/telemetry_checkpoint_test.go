package storage

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTelemetryCheckpointFencesAndOrdering(t *testing.T) {
	checkTelemetryCheckpointFencesAndOrdering(t, newHAFencingStore(t))
}
func checkTelemetryCheckpointFencesAndOrdering(t *testing.T, s *SQLiteStore) {
	t.Helper()
	const lease, owner = "telemetry-execution", "telemetry-owner"
	held, generation, err := s.TryAcquireSchedulerLeaseWithGeneration(lease, owner, time.Now(), time.Minute)
	if err != nil || !held {
		t.Fatalf("lease %v %v", held, err)
	}
	row := &TestRecord{ID: "telemetry-run", Name: "telemetry", ProjectID: DefaultProjectID, Status: "RUNNING", StartTime: time.Now().UTC(), InitialProvenance: map[string]any{"run_incarnation": "incarnation-a"}}
	if err := s.SaveTest(row); err != nil {
		t.Fatal(err)
	}
	row, err = s.GetTest(row.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.AdvanceTestExecutionGenerationIfLeaseTermHeld(row.ID, lease, owner, generation); err != nil || !ok {
		t.Fatalf("generation %v %v", ok, err)
	}
	w := TelemetryCheckpointWrite{ID: row.ID, RefKey: row.RefKey, ProjectID: row.ProjectID, StartTime: row.StartTime, RunIncarnation: "incarnation-a", Generation: generation, LeaseName: lease, LeaseOwnerID: owner, Revision: 1, Payload: `{"version":1}`}
	save := func(w TelemetryCheckpointWrite, want bool) {
		t.Helper()
		ok, err := s.SaveTelemetryCheckpoint(w)
		if err != nil || ok != want {
			t.Fatalf("write %+v = %v %v want %v", w, ok, err, want)
		}
	}
	save(w, true)
	save(w, false)
	for name, mutate := range map[string]func(*TelemetryCheckpointWrite){
		"owner":       func(w *TelemetryCheckpointWrite) { w.LeaseOwnerID = "stale" },
		"generation":  func(w *TelemetryCheckpointWrite) { w.Generation++ },
		"incarnation": func(w *TelemetryCheckpointWrite) { w.RunIncarnation = "replacement" },
		"project":     func(w *TelemetryCheckpointWrite) { w.ProjectID = "different" },
		"ref":         func(w *TelemetryCheckpointWrite) { w.RefKey = "different" },
		"created":     func(w *TelemetryCheckpointWrite) { w.StartTime = w.StartTime.Add(time.Second) },
		"unfenced":    func(w *TelemetryCheckpointWrite) { w.Generation = 0; w.LeaseName = ""; w.LeaseOwnerID = "" },
	} {
		t.Run(name, func(t *testing.T) { bad := w; bad.Revision = 2; mutate(&bad); save(bad, false) })
	}
	var wg sync.WaitGroup
	for i := int64(2); i <= 12; i++ {
		wg.Add(1)
		go func(revision int64) {
			defer wg.Done()
			next := w
			next.Revision = revision
			next.Payload = fmt.Sprintf(`{"version":1,"revision":%d}`, revision)
			if _, err := s.SaveTelemetryCheckpoint(next); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	raw, revision, err := s.LoadTelemetryCheckpoint(w.ID, w.RefKey, w.ProjectID, w.StartTime)
	if err != nil || revision != 12 || raw != `{"version":1,"revision":12}` {
		t.Fatalf("latest %q %d %v", raw, revision, err)
	}
	row.Name = "metadata update"
	if err := s.SaveTest(row); err != nil {
		t.Fatal(err)
	}
	if _, revision, err = s.LoadTelemetryCheckpoint(w.ID, w.RefKey, w.ProjectID, w.StartTime); err != nil || revision != 12 {
		t.Fatalf("SaveTest clobbered %d %v", revision, err)
	}
	oversized := w
	oversized.Payload = strings.Repeat("x", MaxTelemetryCheckpointBytes+1)
	if ok, err := s.SaveTelemetryCheckpoint(oversized); ok || err == nil {
		t.Fatal("oversized payload accepted")
	}
	if err := s.ReleaseSchedulerLease(lease, owner); err != nil {
		t.Fatal(err)
	}
	w.Revision = 13
	save(w, false)
	held, next, err := s.TryAcquireSchedulerLeaseWithGeneration(lease, "successor", time.Now(), time.Minute)
	if err != nil || !held {
		t.Fatal(err)
	}
	if ok, err := s.AdvanceTestExecutionGenerationIfLeaseTermHeld(row.ID, lease, "successor", next); err != nil || !ok {
		t.Fatal(err)
	}
	save(w, false)
	w.Generation = next
	w.LeaseOwnerID = "successor"
	save(w, true)
	end := time.Now().UTC()
	if _, err := s.UpdateTestStatusIfActive(row.ID, "COMPLETE", &end, ""); err != nil {
		t.Fatal(err)
	}
	w.Revision++
	save(w, false)
	if err := s.DeleteTest(row.ID); err != nil {
		t.Fatal(err)
	}
	if raw, revision, err := s.LoadTelemetryCheckpoint(w.ID, w.RefKey, w.ProjectID, w.StartTime); err != nil || raw != "" || revision != 0 {
		t.Fatalf("deleted payload survived %d %v", revision, err)
	}
	row.Status = "RUNNING"
	row.StartTime = row.StartTime.Add(time.Second)
	row.InitialProvenance = map[string]any{"run_incarnation": "replacement"}
	if err := s.SaveTest(row); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.AdvanceTestExecutionGenerationIfLeaseTermHeld(row.ID, lease, "successor", next); err != nil || !ok {
		t.Fatal(err)
	}
	save(w, false)
}

// Controller-created runs keep a local zone and a monotonic reading, which
// SQLite retains in the stored start_time text. Identity is the instant.
func TestTelemetryCheckpointMatchesTheStoredStartInstant(t *testing.T) {
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "checkpoint.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for name, start := range map[string]time.Time{
		"monotonic":  time.Now(),
		"local zone": time.Now().In(time.FixedZone("EDT", -4*60*60)),
	} {
		t.Run(name, func(t *testing.T) {
			row := &TestRecord{ID: "zoned-" + strings.ReplaceAll(name, " ", "-"), Name: name, ProjectID: DefaultProjectID, Status: "RUNNING", StartTime: start, InitialProvenance: map[string]any{"run_incarnation": "zoned"}}
			if err := s.SaveTest(row); err != nil {
				t.Fatal(err)
			}
			w := TelemetryCheckpointWrite{ID: row.ID, ProjectID: row.ProjectID, StartTime: start, RunIncarnation: "zoned", Revision: 1, Payload: `{"version":1}`}
			if ok, err := s.SaveTelemetryCheckpoint(w); err != nil || !ok {
				t.Fatalf("live start rejected %v %v", ok, err)
			}
			reloaded, err := s.GetTest(row.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, candidate := range []time.Time{start, start.UTC(), reloaded.StartTime} {
				if raw, revision, err := s.LoadTelemetryCheckpoint(row.ID, "", row.ProjectID, candidate); err != nil || raw != w.Payload || revision != 1 {
					t.Fatalf("start %v: %q %d %v", candidate, raw, revision, err)
				}
			}
			w.StartTime, w.Revision = reloaded.StartTime, 2
			if ok, err := s.SaveTelemetryCheckpoint(w); err != nil || !ok {
				t.Fatalf("reloaded start rejected %v %v", ok, err)
			}
			if raw, _, err := s.LoadTelemetryCheckpoint(row.ID, "", row.ProjectID, start.Add(time.Microsecond)); err != nil || raw != "" {
				t.Fatalf("different instant matched %q %v", raw, err)
			}
		})
	}
}

// saveTerminalKeepingCheckpoint makes the row terminal through a whole-row save,
// which (like an older controller's terminal write) leaves the payload, so the
// clear under test is the only one that can remove it.
func saveTerminalKeepingCheckpoint(t *testing.T, s *SQLiteStore, row *TestRecord, end *time.Time, start time.Time) {
	t.Helper()
	row.Status, row.EndTime = "FAILED", end
	if err := s.SaveTest(row); err != nil {
		t.Fatal(err)
	}
	if raw, _, err := s.LoadTelemetryCheckpoint(row.ID, "", row.ProjectID, start); err != nil || raw == "" {
		t.Fatalf("fixture lost its payload before the clear under test %q %v", raw, err)
	}
}

func TestTelemetryCheckpointIsDroppedWithTheTerminalArchiveCommit(t *testing.T) {
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "checkpoint.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	start := time.Now().Add(-time.Minute)
	row := &TestRecord{ID: "archived-checkpoint", Name: "archived", ProjectID: DefaultProjectID, Status: "RUNNING", StartTime: start, InitialProvenance: map[string]any{"run_incarnation": "archived"}}
	if err := s.SaveTest(row); err != nil {
		t.Fatal(err)
	}
	w := TelemetryCheckpointWrite{ID: row.ID, ProjectID: row.ProjectID, StartTime: start, RunIncarnation: "archived", Revision: 3, Payload: `{"version":1}`}
	if ok, err := s.SaveTelemetryCheckpoint(w); err != nil || !ok {
		t.Fatalf("save %v %v", ok, err)
	}
	end := time.Now()
	saveTerminalKeepingCheckpoint(t, s, row, &end, start)
	record := RunArchiveRecord{TestID: row.ID, ArchivePath: "/archive/run", ManifestPath: "/archive/run/manifest.json", CreatedAt: start, CompletedAt: end}
	job := &ArchiveFinalizationJob{TestID: row.ID, ProjectID: row.ProjectID, ArchiveRecord: record}
	if err := s.SaveArchiveFinalizationJob(job); err != nil {
		t.Fatal(err)
	}
	if done, err := s.CompleteArchiveFinalization(row.ID, job.IntentToken, "", &record); err != nil || !done {
		t.Fatalf("complete %v %v", done, err)
	}
	// The revision survives, so no older writer can later reinstate a payload.
	if raw, revision, err := s.LoadTelemetryCheckpoint(row.ID, "", row.ProjectID, start); err != nil || raw != "" || revision != 3 {
		t.Fatalf("terminal payload retained %q %d %v", raw, revision, err)
	}
	w.Revision = 4
	if ok, err := s.SaveTelemetryCheckpoint(w); err != nil || ok {
		t.Fatalf("terminal run accepted a checkpoint %v %v", ok, err)
	}
}

// Terminality itself drops the payload, before any archive outcome; every
// terminal status write shares that lifecycle transaction.
func TestTelemetryCheckpointIsDroppedAtTerminality(t *testing.T) {
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "checkpoint.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for i, terminal := range []func(id string, end *time.Time) error{
		func(id string, end *time.Time) error {
			_, err := s.UpdateTestStatusIfActive(id, "ABORTED", end, "stopped")
			return err
		},
		func(id string, end *time.Time) error { return s.UpdateTestStatus(id, "COMPLETE", end, "") },
	} {
		start := time.Now().Add(-time.Minute)
		id := fmt.Sprintf("terminal-checkpoint-%d", i)
		if err := s.SaveTest(&TestRecord{ID: id, Name: id, ProjectID: DefaultProjectID, Status: "RUNNING", StartTime: start, InitialProvenance: map[string]any{"run_incarnation": id}}); err != nil {
			t.Fatal(err)
		}
		if ok, err := s.SaveTelemetryCheckpoint(TelemetryCheckpointWrite{ID: id, ProjectID: DefaultProjectID, StartTime: start, RunIncarnation: id, Revision: 5, Payload: `{"version":1}`}); err != nil || !ok {
			t.Fatalf("save %v %v", ok, err)
		}
		end := time.Now()
		if err := terminal(id, &end); err != nil {
			t.Fatal(err)
		}
		if raw, revision, err := s.LoadTelemetryCheckpoint(id, "", DefaultProjectID, start); err != nil || raw != "" || revision != 5 {
			t.Fatalf("%s: terminal payload retained %q %d %v", id, raw, revision, err)
		}
	}
}

// A terminal run whose archive never commits drops its payload when the
// definitive archive failure is recorded.
func TestTelemetryCheckpointIsDroppedWithADefinitiveArchiveFailure(t *testing.T) {
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "checkpoint.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	start := time.Now().Add(-time.Minute)
	row := &TestRecord{ID: "failed-archive-checkpoint", Name: "failed", ProjectID: DefaultProjectID, Status: "RUNNING", StartTime: start, InitialProvenance: map[string]any{"run_incarnation": "failed"}}
	if err := s.SaveTest(row); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.SaveTelemetryCheckpoint(TelemetryCheckpointWrite{ID: row.ID, ProjectID: row.ProjectID, StartTime: start, RunIncarnation: "failed", Revision: 2, Payload: `{"version":1}`}); err != nil || !ok {
		t.Fatalf("save %v %v", ok, err)
	}
	end := time.Now()
	saveTerminalKeepingCheckpoint(t, s, row, &end, start)
	stored, err := s.GetTest(row.ID)
	if err != nil || stored.EndTime == nil {
		t.Fatalf("terminal row %+v %v", stored, err)
	}
	failure := &RunArchiveFailure{TestID: row.ID, ProjectID: row.ProjectID, StartTime: stored.StartTime, EndTime: *stored.EndTime, Status: "FAILED", Reason: RunArchiveFailureRemoteUnavailable}
	if saved, err := s.SaveRunArchiveFailure(failure, "", ""); err != nil || !saved {
		t.Fatalf("failure %v %v", saved, err)
	}
	if raw, revision, err := s.LoadTelemetryCheckpoint(row.ID, "", row.ProjectID, start); err != nil || raw != "" || revision != 2 {
		t.Fatalf("terminal payload retained %q %d %v", raw, revision, err)
	}
}

func TestTelemetryCheckpointMigrationAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checkpoint.db")
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	row := &TestRecord{ID: "legacy", Name: "legacy", ProjectID: DefaultProjectID, Status: "RUNNING", StartTime: time.Now().UTC(), InitialProvenance: map[string]any{"run_incarnation": "legacy-incarnation"}}
	if err := s.SaveTest(row); err != nil {
		t.Fatal(err)
	}
	row, err = s.GetTest(row.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`ALTER TABLE tests DROP COLUMN telemetry_checkpoint_json`, `ALTER TABLE tests DROP COLUMN telemetry_checkpoint_revision`, `DELETE FROM schema_migrations WHERE version >= 67`} {
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
	raw, rev, err := s.LoadTelemetryCheckpoint(row.ID, row.RefKey, row.ProjectID, row.StartTime)
	if err != nil || raw != "" || rev != 0 {
		t.Fatalf("legacy acquired evidence %q %d %v", raw, rev, err)
	}
	w := TelemetryCheckpointWrite{ID: row.ID, RefKey: row.RefKey, ProjectID: row.ProjectID, StartTime: row.StartTime, RunIncarnation: "legacy-incarnation", Revision: 1, Payload: `{"future_version":99}`}
	if ok, err := s.SaveTelemetryCheckpoint(w); err != nil || !ok {
		t.Fatalf("single-controller save %v %v", ok, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if raw, rev, err = s.LoadTelemetryCheckpoint(row.ID, row.RefKey, row.ProjectID, row.StartTime); err != nil || raw != w.Payload || rev != 1 {
		t.Fatalf("restart %q %d %v", raw, rev, err)
	}
}
