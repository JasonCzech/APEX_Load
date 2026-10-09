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
	if _, err := store.db.Exec(`INSERT INTO scenarios (id, name, duration, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`, "retained-scenario", "Retained", "1m", now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO scenario_schedules
		(id, scenario_id, project_id, name, cron_expr, last_run_test_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, "retained-schedule", "retained-scenario", project.ID, "Retained", "0 * * * *", testID, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO scenario_schedule_occurrences
		(id, schedule_id, scenario_id, project_id, planned_at, status, test_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, "retained-occurrence", "retained-schedule", "retained-scenario", project.ID, now, "materialized", testID, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO powerbi_publishes
		(id, test_id, project_id, workspace_id, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, "retained-powerbi", testID, project.ID, "workspace", "complete", now, now); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSetting("report:annotations:"+testID+":engineer", `{}`); err != nil {
		t.Fatal(err)
	}
	if claimed, err := store.ClaimRunNotification(testID, "telemetry_acceptance", now); err != nil || !claimed {
		t.Fatalf("notification claim = %v %v", claimed, err)
	}
	if _, err := store.db.Exec(`INSERT INTO runtime_secret_provisioning_attempts
		(id, project_id, test_id, actor_subject, secret_names_json, lease_expires_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, "secret-attempt-delete", project.ID, testID, "secret-attempt-owner", `[]`, now.Add(time.Hour), now); err != nil {
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
	var attemptCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM runtime_secret_provisioning_attempts WHERE test_id = ?`, testID).Scan(&attemptCount); err != nil {
		t.Fatal(err)
	}
	if attemptCount != 1 {
		t.Fatalf("runtime secret attempt count after rolled-back delete = %d, want 1", attemptCount)
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
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM runtime_secret_provisioning_attempts WHERE test_id = ?`, testID).Scan(&attemptCount); err != nil {
		t.Fatal(err)
	}
	if attemptCount != 0 {
		t.Fatalf("runtime secret attempt count after committed delete = %d, want 0", attemptCount)
	}
	job, err := store.GetArchiveCleanupJob(testID)
	if err != nil {
		t.Fatal(err)
	}
	if job.ProjectID != project.ID || job.ArchiveRecord.ArchivePath != record.ArchivePath || job.Attempts != 0 {
		t.Fatalf("cleanup job = %+v, want serialized archive and project", job)
	}
	var lastRunTestID, occurrenceTestID sql.NullString
	if err := store.db.QueryRow(`SELECT last_run_test_id FROM scenario_schedules WHERE id = ?`, "retained-schedule").Scan(&lastRunTestID); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT test_id FROM scenario_schedule_occurrences WHERE id = ?`, "retained-occurrence").Scan(&occurrenceTestID); err != nil {
		t.Fatal(err)
	}
	if lastRunTestID.Valid || occurrenceTestID.Valid {
		t.Fatalf("schedule references survived test deletion: last=%v occurrence=%v", lastRunTestID, occurrenceTestID)
	}
	for _, query := range []string{
		`SELECT COUNT(*) FROM powerbi_publishes WHERE test_id = 'delete-with-reservation'`,
		`SELECT COUNT(*) FROM app_settings WHERE key = 'report:annotations:delete-with-reservation:engineer'`,
		`SELECT COUNT(*) FROM app_settings WHERE key = 'run:notifications:delete-with-reservation:telemetry_acceptance'`,
	} {
		var count int
		if err := store.db.QueryRow(query).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("query %q count = %d, want 0", query, count)
		}
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

// stageMidRefreshArchiveDivergence reproduces the durable window that the
// archive refresh flow opens in production: the intent row has already been
// advanced to the NEW object (processArchiveFinalizationJobUnderFence calls
// UpdateArchiveFinalizationRecordIfToken before publishing) while run_archives
// still names the previous one. It returns (published, pending).
func stageMidRefreshArchiveDivergence(t *testing.T, store *SQLiteStore, projectID, testID string) (RunArchiveRecord, RunArchiveRecord) {
	t.Helper()
	now := time.Now().UTC()
	publishedRoot := "s3://archives/evidence/_versions/v1/published/" + testID
	published := RunArchiveRecord{
		TestID: testID, SchemaVersion: archiveRecordSchemaVersion, ArchiveBackend: "s3",
		ArchivePath:  "/archive/staging/published/" + testID,
		ManifestPath: "/archive/staging/published/" + testID + "/manifest.json",
		ArchiveURI:   publishedRoot, ManifestURI: publishedRoot + "/manifest.json",
		CreatedAt: now, CompletedAt: now,
	}
	if err := store.SaveRunArchive(&published); err != nil {
		t.Fatal(err)
	}
	refresh, err := store.BumpArchiveRefreshJob(testID)
	if err != nil {
		t.Fatal(err)
	}
	intent, rebound, err := store.RebindArchiveRefreshToFinalization(testID, refresh.IntentToken)
	if err != nil || !rebound || intent == nil {
		t.Fatalf("rebind refresh to finalization = %+v, %v, %v", intent, rebound, err)
	}
	pendingRoot := "s3://archives/evidence/_versions/v1/in-flight/" + testID
	pending := RunArchiveRecord{
		TestID: testID, SchemaVersion: archiveRecordSchemaVersion, ArchiveBackend: "s3",
		ArchivePath:  "/archive/staging/in-flight/" + testID,
		ManifestPath: "/archive/staging/in-flight/" + testID + "/manifest.json",
		ArchiveURI:   pendingRoot, ManifestURI: pendingRoot + "/manifest.json",
		CreatedAt: now, CompletedAt: now,
	}
	advanced, err := store.UpdateArchiveFinalizationRecordIfToken(testID, intent.IntentToken, &pending)
	if err != nil || !advanced {
		t.Fatalf("advance finalization record = %v, %v", advanced, err)
	}
	current, err := store.GetArchiveFinalizationJob(testID)
	if err != nil {
		t.Fatal(err)
	}
	if current.ArchiveRecord.ArchiveURI != pending.ArchiveURI {
		t.Fatalf("staged intent URI = %q, want %q", current.ArchiveRecord.ArchiveURI, pending.ArchiveURI)
	}
	catalog, err := store.GetRunArchive(testID)
	if err != nil {
		t.Fatal(err)
	}
	if catalog.ArchiveURI != published.ArchiveURI {
		t.Fatalf("catalog URI = %q, want the pre-refresh %q", catalog.ArchiveURI, published.ArchiveURI)
	}
	return published, pending
}

func archiveVersionCleanupURIs(t *testing.T, store *SQLiteStore) map[string]bool {
	t.Helper()
	jobs, err := store.ListArchiveVersionCleanupJobs(50)
	if err != nil {
		t.Fatal(err)
	}
	uris := map[string]bool{}
	for _, job := range jobs {
		uris[job.ArchiveRecord.ArchiveURI] = true
	}
	return uris
}

// TestDeleteTestMidArchiveRefreshQueuesBothTheCatalogedAndInFlightObjects covers
// the intersection of two features the suite otherwise exercises separately:
// a catalogued run archive (TestDeleteTestAtomicallyReleases...) and a pending
// finalization intent (TestDeleteTestConvertsPendingArchiveFinalizationIntoCleanup).
// Deleting during a refresh drops the intent row, so unless its object is queued
// too the in-flight version is orphaned in object storage forever.
func TestDeleteTestMidArchiveRefreshQueuesBothTheCatalogedAndInFlightObjects(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now().UTC()
	projectID := "delete-mid-refresh"
	testID := "delete-mid-refresh-test"
	if err := store.SaveProject(&Project{ID: projectID, Name: projectID, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTest(&TestRecord{ID: testID, Name: testID, Status: "COMPLETE", ProjectID: projectID, StartTime: now}); err != nil {
		t.Fatal(err)
	}
	published, pending := stageMidRefreshArchiveDivergence(t, store, projectID, testID)

	if err := store.DeleteTest(testID); err != nil {
		t.Fatal(err)
	}
	cleanup, err := store.GetArchiveCleanupJob(testID)
	if err != nil {
		t.Fatal(err)
	}
	if cleanup.ArchiveRecord.ArchiveURI != published.ArchiveURI {
		t.Fatalf("primary cleanup URI = %q, want the catalogued %q", cleanup.ArchiveRecord.ArchiveURI, published.ArchiveURI)
	}
	uris := archiveVersionCleanupURIs(t, store)
	if !uris[pending.ArchiveURI] {
		t.Fatalf("in-flight refresh object %q was orphaned by test deletion; queued = %#v", pending.ArchiveURI, uris)
	}
	if uris[published.ArchiveURI] {
		t.Fatalf("catalogued object %q was queued twice; queued = %#v", published.ArchiveURI, uris)
	}
	if _, err := store.GetArchiveFinalizationJob(testID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("finalization intent after delete err = %v, want sql.ErrNoRows", err)
	}
	if _, err := store.GetArchiveRefreshJob(testID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("refresh job after delete err = %v, want sql.ErrNoRows", err)
	}
}

// TestDeleteProjectMidArchiveRefreshQueuesBothTheCatalogedAndInFlightObjects is
// the project-scoped half of the same intersection. deleteProjectGraphTx skips a
// pending intent whenever the same test is already catalogued, which silently
// discards a diverged in-flight object.
func TestDeleteProjectMidArchiveRefreshQueuesBothTheCatalogedAndInFlightObjects(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now().UTC()
	projectID := "delete-project-mid-refresh"
	catalogedOnly := "delete-project-mid-refresh-cataloged"
	diverged := "delete-project-mid-refresh-diverged"
	if err := store.SaveProject(&Project{ID: projectID, Name: projectID, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{catalogedOnly, diverged} {
		if err := store.SaveTest(&TestRecord{ID: id, Name: id, Status: "COMPLETE", ProjectID: projectID, StartTime: now}); err != nil {
			t.Fatal(err)
		}
	}
	quiet := RunArchiveRecord{
		TestID: catalogedOnly, SchemaVersion: archiveRecordSchemaVersion, ArchiveBackend: "s3",
		ArchivePath:  "/archive/staging/quiet/" + catalogedOnly,
		ManifestPath: "/archive/staging/quiet/" + catalogedOnly + "/manifest.json",
		ArchiveURI:   "s3://archives/evidence/quiet/" + catalogedOnly,
		ManifestURI:  "s3://archives/evidence/quiet/" + catalogedOnly + "/manifest.json",
		CreatedAt:    now, CompletedAt: now,
	}
	if err := store.SaveRunArchive(&quiet); err != nil {
		t.Fatal(err)
	}
	published, pending := stageMidRefreshArchiveDivergence(t, store, projectID, diverged)

	if err := store.DeleteProject(projectID); err != nil {
		t.Fatal(err)
	}
	for _, want := range []struct {
		testID string
		uri    string
	}{{catalogedOnly, quiet.ArchiveURI}, {diverged, published.ArchiveURI}} {
		cleanup, err := store.GetArchiveCleanupJob(want.testID)
		if err != nil {
			t.Fatalf("cleanup job for %s: %v", want.testID, err)
		}
		if cleanup.ArchiveRecord.ArchiveURI != want.uri {
			t.Fatalf("cleanup URI for %s = %q, want %q", want.testID, cleanup.ArchiveRecord.ArchiveURI, want.uri)
		}
	}
	uris := archiveVersionCleanupURIs(t, store)
	if !uris[pending.ArchiveURI] {
		t.Fatalf("in-flight refresh object %q was orphaned by project deletion; queued = %#v", pending.ArchiveURI, uris)
	}
	if uris[published.ArchiveURI] || uris[quiet.ArchiveURI] {
		t.Fatalf("catalogued objects were queued twice; queued = %#v", uris)
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

// TestRetentionTombstoneMidArchiveRefreshQueuesTheInFlightObject is the
// retention-path half of the same intersection the delete-path tests above
// cover. Archive retention does not go through DeleteTest: it installs a
// cleanup tombstone for the CATALOGUED publication and drops the pending
// finalization intent in the same transaction. Object deletion is version
// scoped (archiveDeletionRuntime narrows the bucket prefix to that
// publication's version), so a refresh that already advanced the intent to a
// different object leaves those bytes unreachable unless they are queued here.
func TestRetentionTombstoneMidArchiveRefreshQueuesTheInFlightObject(t *testing.T) {
	cases := []struct {
		name      string
		tombstone func(t *testing.T, store *SQLiteStore, job *ArchiveCleanupJob)
	}{
		{
			name: "SaveArchiveCleanupJobIfArchiveUnchanged",
			tombstone: func(t *testing.T, store *SQLiteStore, job *ArchiveCleanupJob) {
				t.Helper()
				created, err := store.SaveArchiveCleanupJobIfArchiveUnchanged(job)
				if err != nil {
					t.Fatal(err)
				}
				if !created {
					t.Fatal("conditional retention tombstone was not created")
				}
			},
		},
		{
			name: "SaveArchiveCleanupJob",
			tombstone: func(t *testing.T, store *SQLiteStore, job *ArchiveCleanupJob) {
				t.Helper()
				if err := store.SaveArchiveCleanupJob(job); err != nil {
					t.Fatal(err)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()

			now := time.Now().UTC()
			projectID := "retention-mid-refresh"
			testID := "retention-mid-refresh-test"
			if err := store.SaveProject(&Project{ID: projectID, Name: projectID, CreatedAt: now}); err != nil {
				t.Fatal(err)
			}
			if err := store.SaveTest(&TestRecord{ID: testID, Name: testID, Status: "COMPLETE", ProjectID: projectID, StartTime: now}); err != nil {
				t.Fatal(err)
			}
			published, pending := stageMidRefreshArchiveDivergence(t, store, projectID, testID)

			record := published
			tc.tombstone(t, store, &ArchiveCleanupJob{TestID: testID, ProjectID: projectID, ArchiveRecord: record})

			cleanup, err := store.GetArchiveCleanupJob(testID)
			if err != nil {
				t.Fatal(err)
			}
			if cleanup.ArchiveRecord.ArchiveURI != published.ArchiveURI {
				t.Fatalf("primary cleanup URI = %q, want the catalogued %q", cleanup.ArchiveRecord.ArchiveURI, published.ArchiveURI)
			}
			if _, err := store.GetArchiveFinalizationJob(testID); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("finalization intent after retention tombstone err = %v, want sql.ErrNoRows", err)
			}
			uris := archiveVersionCleanupURIs(t, store)
			if !uris[pending.ArchiveURI] {
				t.Fatalf("in-flight refresh object %q was orphaned by the retention tombstone; queued = %#v", pending.ArchiveURI, uris)
			}
			if uris[published.ArchiveURI] {
				t.Fatalf("catalogued object %q was queued twice; queued = %#v", published.ArchiveURI, uris)
			}
		})
	}
}

// TestRetentionTombstoneWithoutRefreshQueuesExactlyOneCleanup pins the ordinary
// case against the fix above: when the intent and the catalog name the SAME
// object there is nothing divergent to queue, so a retention tombstone must
// still produce exactly one cleanup job and no version-cleanup rows.
func TestRetentionTombstoneWithoutRefreshQueuesExactlyOneCleanup(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now().UTC()
	projectID := "retention-no-refresh"
	testID := "retention-no-refresh-test"
	if err := store.SaveProject(&Project{ID: projectID, Name: projectID, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTest(&TestRecord{ID: testID, Name: testID, Status: "COMPLETE", ProjectID: projectID, StartTime: now}); err != nil {
		t.Fatal(err)
	}
	root := "s3://archives/evidence/_versions/v1/settled/" + testID
	record := RunArchiveRecord{
		TestID: testID, SchemaVersion: archiveRecordSchemaVersion, ArchiveBackend: "s3",
		ArchivePath:  "/archive/staging/settled/" + testID,
		ManifestPath: "/archive/staging/settled/" + testID + "/manifest.json",
		ArchiveURI:   root, ManifestURI: root + "/manifest.json",
		CreatedAt: now, CompletedAt: now,
	}
	if err := store.SaveRunArchive(&record); err != nil {
		t.Fatal(err)
	}
	// Stage the intent too, naming the SAME object as the catalog. Without this
	// row the tombstone never reaches the identical-pointer short circuit in
	// enqueuePendingArchiveObjectCleanupTx, so the assertion below would hold
	// even if that dedup were deleted.
	intentRecord := record
	if err := store.SaveArchiveFinalizationJob(&ArchiveFinalizationJob{
		TestID: testID, ProjectID: projectID, ArchiveRecord: intentRecord, PersistArtifacts: true,
	}); err != nil {
		t.Fatal(err)
	}
	intent, err := store.GetArchiveFinalizationJob(testID)
	if err != nil {
		t.Fatal(err)
	}
	if intent.ArchiveRecord.ArchiveURI != record.ArchiveURI {
		t.Fatalf("staged intent URI = %q, want the catalogued %q", intent.ArchiveRecord.ArchiveURI, record.ArchiveURI)
	}
	copyOfRecord := record
	created, err := store.SaveArchiveCleanupJobIfArchiveUnchanged(&ArchiveCleanupJob{
		TestID: testID, ProjectID: projectID, ArchiveRecord: copyOfRecord,
	})
	if err != nil || !created {
		t.Fatalf("retention tombstone = %v, %v", created, err)
	}
	if _, err := store.GetArchiveFinalizationJob(testID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("finalization intent after retention tombstone err = %v, want sql.ErrNoRows", err)
	}
	uris := archiveVersionCleanupURIs(t, store)
	if len(uris) != 0 {
		t.Fatalf("retention tombstone queued version cleanups with no refresh in flight: %#v", uris)
	}
}
