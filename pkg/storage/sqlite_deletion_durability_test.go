package storage

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestDeleteTestAtomicallyReleasesReservationAndEnqueuesArchiveCleanup(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now().UTC()
	project := &Project{ID: "deletion-durability", Name: "Deletion Durability", CreatedAt: now}
	if err := store.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	testID := "delete-with-reservation"
	if err := store.SaveTest(&TestRecord{ID: testID, Name: testID, Status: "COMPLETE", ProjectID: project.ID, StartTime: now}); err != nil {
		t.Fatal(err)
	}
	status := "COMPLETE"
	if err := store.SaveCapacityReservation(&CapacityReservation{
		ID: "bound-slot", ProjectID: project.ID, StartAt: now, EndAt: now.Add(time.Hour),
		ApprovalState: "approved", BoundTestID: &testID, BoundTestStatus: &status,
	}); err != nil {
		t.Fatal(err)
	}
	record := &RunArchiveRecord{
		TestID: testID, SchemaVersion: archiveRecordSchemaVersion, ArchiveBackend: "filesystem",
		ArchivePath: "/safe/archive/" + testID, ManifestPath: "/safe/archive/" + testID + "/manifest.json",
		CreatedAt: now, CompletedAt: now,
	}
	if err := store.SaveRunArchive(record); err != nil {
		t.Fatal(err)
	}

	// Force a late failure after the binding release/outbox write. Both must roll
	// back with the test deletion rather than leaving half-applied durable state.
	if _, err := store.db.Exec(`CREATE TRIGGER reject_test_delete BEFORE DELETE ON tests
		WHEN OLD.id = 'delete-with-reservation' BEGIN SELECT RAISE(ABORT, 'injected delete failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteTest(testID); err == nil {
		t.Fatal("DeleteTest succeeded despite injected failure")
	}
	reservation, err := store.GetCapacityReservation("bound-slot")
	if err != nil {
		t.Fatal(err)
	}
	if reservation.BoundTestID == nil || *reservation.BoundTestID != testID || reservation.BoundTestStatus == nil {
		t.Fatalf("binding changed after rolled-back delete: %+v", reservation)
	}
	if _, err := store.GetArchiveCleanupJob(testID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cleanup job after rolled-back delete err = %v, want sql.ErrNoRows", err)
	}
	if _, err := store.GetTest(testID); err != nil {
		t.Fatalf("test disappeared after rolled-back delete: %v", err)
	}

	if _, err := store.db.Exec(`DROP TRIGGER reject_test_delete`); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteTest(testID); err != nil {
		t.Fatal(err)
	}
	reservation, err = store.GetCapacityReservation("bound-slot")
	if err != nil {
		t.Fatal(err)
	}
	if reservation.BoundTestID != nil || reservation.BoundTestStatus != nil {
		t.Fatalf("binding survived committed delete: %+v", reservation)
	}
	if _, err := store.GetTest(testID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("GetTest after delete err = %v, want sql.ErrNoRows", err)
	}
	if _, err := store.GetRunArchive(testID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("GetRunArchive after delete err = %v, want sql.ErrNoRows", err)
	}
	job, err := store.GetArchiveCleanupJob(testID)
	if err != nil {
		t.Fatal(err)
	}
	if job.ProjectID != project.ID || job.ArchiveRecord.ArchivePath != record.ArchivePath || job.Attempts != 0 {
		t.Fatalf("cleanup job = %+v, want serialized archive and project", job)
	}
}

func TestDeleteProjectRetainsAuditHistoryAndQueuesArchiveCleanup(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now().UTC()
	projectID := "audited-project-delete"
	if err := store.SaveProject(&Project{ID: projectID, Name: "Audited", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	testID := "audited-project-test"
	if err := store.SaveTest(&TestRecord{ID: testID, Name: testID, Status: "COMPLETE", ProjectID: projectID, StartTime: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRunArchive(&RunArchiveRecord{
		TestID: testID, SchemaVersion: archiveRecordSchemaVersion, ArchiveBackend: "filesystem",
		ArchivePath: "/safe/archive/" + testID, ManifestPath: "/safe/archive/" + testID + "/manifest.json",
		CreatedAt: now, CompletedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAuditEvent(&AuditEvent{
		ID: "audit-before-delete", Action: "test.complete", ActorType: "user", ActorSubject: "operator",
		ProjectID: projectID, ResourceType: "test", ResourceID: testID, Status: "success", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	if err := store.DeleteProject(projectID); err != nil {
		t.Fatal(err)
	}
	events, err := store.ListAuditEvents(AuditEventFilter{ProjectID: projectID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].ID != "audit-before-delete" {
		t.Fatalf("retained audit events = %+v, want audit-before-delete", events)
	}
	job, err := store.GetArchiveCleanupJob(testID)
	if err != nil {
		t.Fatal(err)
	}
	if job.ProjectID != projectID || job.ArchiveRecord.TestID != testID {
		t.Fatalf("cleanup job = %+v, want deleted project's archive", job)
	}
}

func TestDeleteTestConvertsPendingArchiveFinalizationIntoCleanup(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now().UTC()
	projectID := "pending-finalization-delete"
	if err := store.SaveProject(&Project{ID: projectID, Name: "Pending Finalization", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	testID := "pending-finalization-test"
	if err := store.SaveTest(&TestRecord{ID: testID, Name: testID, Status: "FAILED", ProjectID: projectID, StartTime: now}); err != nil {
		t.Fatal(err)
	}
	record := RunArchiveRecord{
		TestID: testID, SchemaVersion: archiveRecordSchemaVersion, ArchiveBackend: "filesystem",
		ArchivePath: "/safe/archive/" + testID, ManifestPath: "/safe/archive/" + testID + "/manifest.json",
		CreatedAt: now, CompletedAt: now,
	}
	if err := store.SaveArchiveFinalizationJob(&ArchiveFinalizationJob{
		TestID: testID, ProjectID: projectID, ArchiveRecord: record, PersistArtifacts: true,
	}); err != nil {
		t.Fatal(err)
	}

	if err := store.DeleteTest(testID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetArchiveFinalizationJob(testID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("finalization job after delete err = %v, want sql.ErrNoRows", err)
	}
	cleanup, err := store.GetArchiveCleanupJob(testID)
	if err != nil {
		t.Fatal(err)
	}
	if cleanup.ProjectID != projectID || cleanup.ArchiveRecord.ArchivePath != record.ArchivePath {
		t.Fatalf("cleanup job = %+v, want pending finalization archive", cleanup)
	}
}

func TestArchiveFinalizationJobRoundTripAndFailureAccounting(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now().UTC()
	projectID := "finalization-roundtrip"
	if err := store.SaveProject(&Project{ID: projectID, Name: "Finalization", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	testID := "finalization-roundtrip-test"
	if err := store.SaveTest(&TestRecord{ID: testID, Name: testID, Status: "COMPLETE", ProjectID: projectID, StartTime: now}); err != nil {
		t.Fatal(err)
	}
	job := &ArchiveFinalizationJob{
		TestID: testID, ProjectID: projectID, PersistArtifacts: true,
		ArchiveRecord: RunArchiveRecord{TestID: testID, ArchivePath: "/archive/roundtrip", ManifestPath: "/archive/roundtrip/manifest.json", CreatedAt: now, CompletedAt: now},
	}
	if err := store.SaveArchiveFinalizationJob(job); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkArchiveFinalizationJobFailed(testID, errors.New("upload unavailable"), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.GetArchiveFinalizationJob(testID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ProjectID != projectID || !loaded.PersistArtifacts || loaded.Attempts != 1 || loaded.LastError != "upload unavailable" || loaded.ArchiveRecord.ArchivePath != job.ArchiveRecord.ArchivePath {
		t.Fatalf("finalization job = %+v, want round-tripped record and failure", loaded)
	}
	listed, err := store.ListArchiveFinalizationJobs(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].TestID != testID {
		t.Fatalf("listed finalization jobs = %+v", listed)
	}
	if err := store.DeleteArchiveFinalizationJob(testID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetArchiveFinalizationJob(testID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("finalization job after completion err = %v, want sql.ErrNoRows", err)
	}
}

func TestArchiveFinalizationTokenCASRejectsStaleWorker(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now().UTC()
	projectID := "finalization-token-cas"
	testID := "finalization-token-cas-test"
	if err := store.SaveProject(&Project{ID: projectID, Name: projectID, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTest(&TestRecord{ID: testID, Name: testID, Status: "COMPLETE", ProjectID: projectID, StartTime: now}); err != nil {
		t.Fatal(err)
	}
	first := &ArchiveFinalizationJob{TestID: testID, ProjectID: projectID, ArchiveRecord: RunArchiveRecord{
		TestID: testID, ArchivePath: "/archive/first", ManifestPath: "/archive/first/manifest.json", CreatedAt: now, CompletedAt: now,
	}}
	if err := store.SaveArchiveFinalizationJob(first); err != nil {
		t.Fatal(err)
	}
	second := &ArchiveFinalizationJob{TestID: testID, ProjectID: projectID, ArchiveRecord: RunArchiveRecord{
		TestID: testID, ArchivePath: "/archive/second", ManifestPath: "/archive/second/manifest.json", CreatedAt: now, CompletedAt: now,
	}}
	if err := store.SaveArchiveFinalizationJob(second); err != nil {
		t.Fatal(err)
	}
	if first.IntentToken == "" || second.IntentToken == "" || first.IntentToken == second.IntentToken {
		t.Fatalf("intent tokens were not opaque and unique: %q / %q", first.IntentToken, second.IntentToken)
	}

	updated, err := store.UpdateArchiveFinalizationRecordIfToken(testID, first.IntentToken, &first.ArchiveRecord)
	if err != nil || updated {
		t.Fatalf("stale record CAS = %v, %v; want false, nil", updated, err)
	}
	if err := store.MarkArchiveFinalizationJobFailedIfToken(testID, first.IntentToken, errors.New("stale"), now); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("stale failure annotation error = %v, want sql.ErrNoRows", err)
	}
	committed, err := store.CompleteArchiveFinalization(testID, first.IntentToken, "", &first.ArchiveRecord)
	if err != nil || committed {
		t.Fatalf("stale completion = %v, %v; want false, nil", committed, err)
	}
	current, err := store.GetArchiveFinalizationJob(testID)
	if err != nil {
		t.Fatal(err)
	}
	if current.IntentToken != second.IntentToken || current.Attempts != 0 || current.ArchiveRecord.ArchivePath != second.ArchiveRecord.ArchivePath {
		t.Fatalf("replacement intent was changed by stale worker: %+v", current)
	}
	committed, err = store.CompleteArchiveFinalization(testID, second.IntentToken, "", &second.ArchiveRecord)
	if err != nil || !committed {
		t.Fatalf("current completion = %v, %v; want true, nil", committed, err)
	}
	if _, err := store.GetArchiveFinalizationJob(testID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("completed intent still exists: %v", err)
	}
	published, err := store.GetRunArchive(testID)
	if err != nil || published.ArchivePath != second.ArchiveRecord.ArchivePath {
		t.Fatalf("published record = %+v, %v", published, err)
	}
}

func TestLegacyArchiveFinalizationIntentGetsOpaqueTokenBeforeWorkersStart(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "apexload.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	projectID := "legacy-finalization-token"
	testID := "legacy-finalization-token-test"
	if err := store.SaveProject(&Project{ID: projectID, Name: projectID, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTest(&TestRecord{ID: testID, Name: testID, Status: "COMPLETE", ProjectID: projectID, StartTime: now}); err != nil {
		t.Fatal(err)
	}
	job := &ArchiveFinalizationJob{TestID: testID, ProjectID: projectID, ArchiveRecord: RunArchiveRecord{
		TestID: testID, ArchivePath: "/archive/legacy", ManifestPath: "/archive/legacy/manifest.json", CreatedAt: now, CompletedAt: now,
	}}
	if err := store.SaveArchiveFinalizationJob(job); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE archive_finalization_jobs SET intent_token = '' WHERE test_id = ?`, testID); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	upgraded, err := reopened.GetArchiveFinalizationJob(testID)
	if err != nil {
		t.Fatal(err)
	}
	if upgraded.IntentToken == "" {
		t.Fatal("legacy finalization row was exposed to workers with an empty token")
	}
	if err := reopened.MarkArchiveFinalizationJobFailedIfToken(testID, "", errors.New("unsafe legacy worker"), now); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("empty-token failure annotation error = %v, want sql.ErrNoRows", err)
	}
}

func TestArchiveRefreshCompletionAcknowledgesOnlyObservedToken(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	projectID := "archive-refresh-cas"
	testID := "archive-refresh-cas-test"
	if err := store.SaveProject(&Project{ID: projectID, Name: projectID, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTest(&TestRecord{ID: testID, Name: testID, Status: "COMPLETE", ProjectID: projectID, StartTime: now}); err != nil {
		t.Fatal(err)
	}
	record := &RunArchiveRecord{TestID: testID, ArchivePath: "/archive/refresh", ManifestPath: "/archive/refresh/manifest.json", CreatedAt: now, CompletedAt: now}
	if err := store.SaveRunArchive(record); err != nil {
		t.Fatal(err)
	}
	first, err := store.BumpArchiveRefreshJob(testID)
	if err != nil {
		t.Fatal(err)
	}
	finalization, rebound, err := store.RebindArchiveRefreshToFinalization(testID, first.IntentToken)
	if err != nil || !rebound || finalization == nil {
		t.Fatalf("refresh bind = %+v, %v, %v", finalization, rebound, err)
	}
	second, err := store.BumpArchiveRefreshJob(testID)
	if err != nil {
		t.Fatal(err)
	}
	if second.IntentToken == first.IntentToken {
		t.Fatal("refresh token was reused")
	}
	committed, err := store.CompleteArchiveFinalization(testID, finalization.IntentToken, finalization.RefreshToken, &finalization.ArchiveRecord)
	if err != nil || !committed {
		t.Fatalf("finalization completion = %v, %v", committed, err)
	}
	remaining, err := store.GetArchiveRefreshJob(testID)
	if err != nil {
		t.Fatal(err)
	}
	if remaining.IntentToken != second.IntentToken {
		t.Fatalf("newer refresh was acknowledged by stale token: %+v", remaining)
	}
}

func TestArchiveRefreshQueuesEverySupersededObjectVersionThroughFinalDeletion(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	projectID := "archive-version-cleanup"
	testID := "archive-version-cleanup-test"
	if err := store.SaveProject(&Project{ID: projectID, Name: projectID, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTest(&TestRecord{ID: testID, Name: testID, Status: "COMPLETE", ProjectID: projectID, StartTime: now}); err != nil {
		t.Fatal(err)
	}

	completeVersion := func(version string) RunArchiveRecord {
		t.Helper()
		root := "s3://archives/evidence/_versions/v1/" + version + "/" + testID
		record := RunArchiveRecord{
			TestID: testID, ArchiveBackend: "s3", ArchivePath: "/archive/staging/" + version + "/" + testID,
			ManifestPath: "/archive/staging/" + version + "/" + testID + "/manifest.json",
			ArchiveURI:   root, ManifestURI: root + "/manifest.json", CreatedAt: now, CompletedAt: now,
		}
		job := &ArchiveFinalizationJob{TestID: testID, ProjectID: projectID, ArchiveRecord: record, PersistArtifacts: true}
		if err := store.SaveArchiveFinalizationJob(job); err != nil {
			t.Fatal(err)
		}
		committed, err := store.CompleteArchiveFinalization(testID, job.IntentToken, "", &record)
		if err != nil || !committed {
			t.Fatalf("complete version %s = %v, %v", version, committed, err)
		}
		return record
	}

	first := completeVersion("version-a")
	second := completeVersion("version-b")
	third := completeVersion("version-c")
	jobs, err := store.ListArchiveVersionCleanupJobs(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 {
		t.Fatalf("superseded cleanup jobs = %d, want 2", len(jobs))
	}
	gotURIs := map[string]bool{}
	for _, job := range jobs {
		gotURIs[job.ArchiveRecord.ArchiveURI] = true
	}
	if !gotURIs[first.ArchiveURI] || !gotURIs[second.ArchiveURI] || gotURIs[third.ArchiveURI] {
		t.Fatalf("queued superseded URIs = %#v", gotURIs)
	}

	if err := store.DeleteTest(testID); err != nil {
		t.Fatal(err)
	}
	currentCleanup, err := store.GetArchiveCleanupJob(testID)
	if err != nil {
		t.Fatal(err)
	}
	if currentCleanup.ArchiveRecord.ArchiveURI != third.ArchiveURI {
		t.Fatalf("test deletion queued %q, want current %q", currentCleanup.ArchiveRecord.ArchiveURI, third.ArchiveURI)
	}
	jobs, err = store.ListArchiveVersionCleanupJobs(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 {
		t.Fatalf("test deletion discarded superseded cleanup jobs: %d", len(jobs))
	}
}
