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
