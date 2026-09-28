package storage

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testArchiveManifestPreparation(t *testing.T, s *SQLiteStore) {
	t.Helper()
	id := "manifest-preparation"
	now := time.Now().UTC()
	if err := s.SaveTest(&TestRecord{ID: id, Name: id, Status: "COMPLETE", StartTime: now, EndTime: &now}); err != nil {
		t.Fatal(err)
	}
	record := RunArchiveRecord{TestID: id, CreatedAt: now, CompletedAt: now, ArchiveBackend: "filesystem"}
	job := &ArchiveFinalizationJob{TestID: id, ProjectID: DefaultProjectID, ArchiveRecord: record, PersistArtifacts: true}
	if err := s.SaveArchiveFinalizationJob(job); err != nil {
		t.Fatal(err)
	}
	data := `{"test_id":"manifest-preparation","schema_version":"apexload.archive.v4"}`
	sum := sha256.Sum256([]byte(data))
	record.ManifestSHA256 = hex.EncodeToString(sum[:])
	for _, invalid := range []string{"", `{"test_id":"wrong"}`, "{", strings.Repeat("x", MaxArchivePreparedManifestBytes+1)} {
		if ok, err := s.PrepareArchiveFinalizationManifestIfToken(id, job.IntentToken, &record, invalid); err == nil || ok {
			t.Fatal("invalid preparation accepted")
		}
	}
	mismatch := record
	mismatch.ManifestSHA256 = strings.Repeat("0", 64)
	if ok, err := s.PrepareArchiveFinalizationManifestIfToken(id, job.IntentToken, &mismatch, data); err == nil || ok {
		t.Fatal("wrong digest accepted")
	}
	if ok, err := s.PrepareArchiveFinalizationManifestIfToken(id, "stale", &record, data); err != nil || ok {
		t.Fatalf("stale intent: %v %v", ok, err)
	}
	if ok, err := s.PrepareArchiveFinalizationManifestIfToken(id, job.IntentToken, &record, data); err != nil || !ok {
		t.Fatalf("prepare: %v %v", ok, err)
	}
	got, err := s.GetArchiveFinalizationJob(id)
	if err != nil || got.PreparedManifestJSON != data || got.ArchiveRecord.ManifestSHA256 != record.ManifestSHA256 {
		t.Fatalf("inconsistent durable preparation: %+v %v", got, err)
	}
	if ok, err := s.UpdateArchiveFinalizationRecordIfToken(id, job.IntentToken, &mismatch); err == nil || ok {
		t.Fatal("record-only update detached prepared bytes from their digest")
	}
	got, err = s.GetArchiveFinalizationJob(id)
	if err != nil || got.PreparedManifestJSON != data || got.ArchiveRecord.ManifestSHA256 != record.ManifestSHA256 {
		t.Fatalf("rejected stale update changed preparation: %+v %v", got, err)
	}
	snapshots, err := s.ListArchiveFinalizationJobs(10)
	if err != nil || len(snapshots) != 1 || snapshots[0].PreparedManifestJSON != "" {
		t.Fatalf("list loaded large payload: %v", err)
	}
	record.TrendPointJSON = `{"run_id":"manifest-preparation"}`
	if ok, err := s.UpdateArchiveFinalizationRecordIfToken(id, job.IntentToken, &record); err != nil || !ok {
		t.Fatalf("trend update: %v %v", ok, err)
	}
	got, err = s.GetArchiveFinalizationJob(id)
	if err != nil || got.PreparedManifestJSON != data {
		t.Fatalf("ordinary update erased preparation: %v", err)
	}
	refresh, err := s.BumpArchiveRefreshJob(id)
	if err != nil {
		t.Fatal(err)
	}
	rebound, ok, err := s.RebindArchiveRefreshToFinalization(id, refresh.IntentToken)
	if err != nil || !ok || rebound.PreparedManifestJSON != data {
		t.Fatalf("pending refresh lost preparation: %+v %v", rebound, err)
	}
	got, err = s.GetArchiveFinalizationJob(id)
	if err != nil || got.PreparedManifestJSON != data {
		t.Fatalf("rebind did not persist preparation: %v", err)
	}
	replacement := &ArchiveFinalizationJob{TestID: id, ProjectID: DefaultProjectID, ArchiveRecord: record, PreparedManifestJSON: data}
	if err := s.SaveArchiveFinalizationJob(replacement); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetArchiveFinalizationJob(id)
	if err != nil || got.PreparedManifestJSON != "" {
		t.Fatalf("replacement inherited old preparation: %v", err)
	}
	if replacement.PreparedManifestJSON != "" {
		t.Fatal("replacement retained old in-memory preparation")
	}
	if ok, err := s.PrepareArchiveFinalizationManifestIfToken(id, replacement.IntentToken, &record, data); err != nil || !ok {
		t.Fatalf("prepare replacement: %v %v", ok, err)
	}
	if err := s.SaveRunArchive(&record); err != nil {
		t.Fatal(err)
	}
	refresh, err = s.BumpArchiveRefreshJob(id)
	if err != nil {
		t.Fatal(err)
	}
	rebound, ok, err = s.RebindArchiveRefreshToFinalization(id, refresh.IntentToken)
	if err != nil || !ok || rebound.PreparedManifestJSON != "" {
		t.Fatalf("catalog refresh inherited preparation: %+v %v", rebound, err)
	}
	got, err = s.GetArchiveFinalizationJob(id)
	if err != nil || got.PreparedManifestJSON != "" {
		t.Fatalf("catalog rebind retained durable preparation: %v", err)
	}
}

func TestArchiveManifestPreparationSQLite(t *testing.T) {
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	testArchiveManifestPreparation(t, s)
}

func TestArchiveManifestPreparationMigratesLegacyTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE archive_finalization_jobs(test_id TEXT PRIMARY KEY, project_id TEXT NOT NULL, archive_record_json TEXT NOT NULL, persist_artifacts INTEGER NOT NULL DEFAULT 1, generation INTEGER NOT NULL DEFAULT 1, intent_token TEXT NOT NULL DEFAULT '', refresh_token TEXT NOT NULL DEFAULT '', attempts INTEGER NOT NULL DEFAULT 0, last_error TEXT, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL)`)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(schemaMigrationLedgerDDL); err != nil {
		t.Fatal(err)
	}
	appliedAt := time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)
	for _, prior := range schemaReleases[:17] {
		if _, err := db.Exec(`INSERT INTO schema_migrations (version, name, fingerprint, applied_at) VALUES (?, ?, ?, ?)`, prior.Version, prior.Name, prior.Fingerprint, appliedAt); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	status, err := s.SchemaStatus()
	if err != nil || status.AppliedVersion != 18 || len(status.Applied) != 18 || len(status.Pending) != 0 {
		t.Fatalf("release 17 upgrade: %+v %v", status, err)
	}
	for i, applied := range status.Applied[:17] {
		prior := schemaReleases[i]
		if applied.Name != prior.Name || applied.Fingerprint != prior.Fingerprint || !applied.AppliedAt.Equal(appliedAt) {
			t.Fatalf("upgrade changed prior release: %+v", applied)
		}
	}
	testArchiveManifestPreparation(t, s)
}
