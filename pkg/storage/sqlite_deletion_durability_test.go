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
