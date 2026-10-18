//go:build postgres_integration

package storage

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// TestPostgresObjectActivityTiesListInInsertionOrder runs the shared object
// activity tie-order checks on a fresh schema, then upgrades a schema holding
// object activity from before insert_seq together with audit events that were
// never copied into object activity, some from before the audit insert_seq,
// which the first start backfills. The ownership fallback is the shared
// insertSeqTable path, covered for audit_events by
// TestPostgresAuditEventTiesListInInsertionOrder. Run with:
//
//	APEXLOAD_TEST_POSTGRES_URL='postgres://...' go test -tags postgres_integration \
//		./pkg/storage -run TestPostgresObjectActivityTiesListInInsertionOrder
func TestPostgresObjectActivityTiesListInInsertionOrder(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("APEXLOAD_TEST_POSTGRES_URL"))
	if databaseURL == "" {
		t.Skip("APEXLOAD_TEST_POSTGRES_URL is not set")
	}
	t.Run("fresh", func(t *testing.T) {
		warnings := captureInsertSeqWarnings(t)
		store, err := NewPostgresStore(postgresIsolatedSchema(t, databaseURL, "activity_fresh"), StoreConnectionOptions{}, false)
		if err != nil {
			t.Fatalf("NewPostgresStore returned error: %v", err)
		}
		defer store.Close()
		if state, err := store.inspectPostgresInsertSeq(objectActivityTable); err != nil || !state.linked || state.sequenceOwner != state.tableOwner {
			t.Fatalf("fresh object activity sequence state = %+v (err=%v), want linked and owned by the table owner", state, err)
		}
		if got := warnings(); len(got) != 0 {
			t.Fatalf("fresh install logged insert_seq warnings: %+v", got)
		}
		checkObjectActivityTiesListInInsertionOrder(t, store.SQLiteStore)
		checkObjectActivityBackfillKeepsAuditTieOrder(t, store.SQLiteStore)
	})
	t.Run("upgrade", func(t *testing.T) {
		checkPostgresObjectActivityInsertSeqUpgrade(t, postgresIsolatedSchema(t, databaseURL, "activity_upgrade"))
	})
}

func checkPostgresObjectActivityInsertSeqUpgrade(t *testing.T, scopedURL string) {
	t.Helper()
	const project, backfillProject, summaryProject = "project-activity-upgrade", "project-activity-upgrade-backfill", "project-activity-upgrade-summaries"
	tied := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	legacyDB, err := sql.Open("pgx", scopedURL)
	if err != nil {
		t.Fatalf("open legacy PostgreSQL schema: %v", err)
	}
	for _, statement := range []string{
		legacyAuditEventsDDL,
		`CREATE TABLE object_activity_events (id TEXT PRIMARY KEY, project_id TEXT NOT NULL, resource_type TEXT NOT NULL, resource_id TEXT NOT NULL, resource_name TEXT NOT NULL, action TEXT NOT NULL, actor_subject TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL, status TEXT NOT NULL DEFAULT 'success', payload TEXT NOT NULL)`,
	} {
		if _, err := legacyDB.ExecContext(ctx, statement); err != nil {
			legacyDB.Close()
			t.Fatalf("create legacy tables: %v", err)
		}
	}
	exec := func(step, statement string, args ...any) {
		t.Helper()
		if _, err := legacyDB.ExecContext(ctx, statement, args...); err != nil {
			legacyDB.Close()
			t.Fatalf("%s: %v", step, err)
		}
	}
	saveAudit := func(id string) {
		t.Helper()
		exec("seed legacy audit event "+id, `INSERT INTO audit_events
			(id, action, actor_type, actor_subject, actor_display_name, project_id, domain_id, resource_type, resource_id, status, details_json, created_at)
			VALUES ($1, 'script.update', 'user', 'alice', '', $2, '', 'script', 'backfilled-script', 'success', '{"name":"Checkout"}', $3)`, id, backfillProject, tied)
	}
	saveLegacyActivity := func(id, projectID, resource, action string) {
		t.Helper()
		payload := fmt.Sprintf(`{"id":%q,"action":%q,"actor_type":"user","actor_subject":"alice","project_id":%q,"resource_type":"script","resource_id":%q,"resource_name":"Checkout","status":"success","created_at":%q}`, id, action, projectID, resource, tied.Format(time.RFC3339Nano))
		exec("seed legacy object activity "+id, `INSERT INTO object_activity_events
			(id, project_id, resource_type, resource_id, resource_name, action, actor_subject, created_at, status, payload)
			VALUES ($1, $2, 'script', $3, 'Checkout', $4, 'alice', $5, 'success', $6)`, id, projectID, resource, action, tied, payload)
	}
	// Audit events never copied (no object activity migration marker yet): the
	// first start copies them oldest first, so the copies list as ListAuditEvents
	// lists them. Three predate the audit insert_seq and two follow it, so the
	// copying runs NULLs first and then the sequence.
	for _, id := range []string{"audit-b", "audit-c", "audit-a"} {
		saveAudit(id)
	}
	exec("add the audit insert_seq", auditEventsTable.postgresMigration())
	for _, id := range []string{"audit-z", "audit-y"} {
		saveAudit(id)
	}
	// Neither the physical order (b,c,a) nor its reverse matches ID order, so the
	// expected legacy-c,legacy-b,legacy-a can only come from the id DESC fallback.
	for _, id := range []string{"legacy-b", "legacy-c", "legacy-a"} {
		saveLegacyActivity(id, project, "legacy-script", "modify")
	}
	// Activity from before insert_seq next to new activity at the same time:
	// "reborn" is a reused ID, modified before the upgrade and created after
	// it, so the old modify is not the new object's; "kept" is created before
	// the upgrade and modified after it, so the modify counts.
	saveLegacyActivity("z-legacy-reborn-modify", summaryProject, "reborn", "modify")
	saveLegacyActivity("a-legacy-kept-create", summaryProject, "kept", "create")
	// "idonly" has a create and a modify that both predate insert_seq: their
	// IDs decide, so the create (higher ID) counts as newer and the modify does
	// not count. "gone" was deleted before the upgrade and is created again
	// after it, so it exists.
	saveLegacyActivity("z-legacy-idonly-create", summaryProject, "idonly", "create")
	saveLegacyActivity("a-legacy-idonly-modify", summaryProject, "idonly", "modify")
	saveLegacyActivity("z-legacy-gone-delete", summaryProject, "gone", "delete")
	var legacyFile int64
	if err := legacyDB.QueryRowContext(ctx, `SELECT pg_relation_filenode('object_activity_events')`).Scan(&legacyFile); err != nil {
		legacyDB.Close()
		t.Fatalf("read legacy object_activity_events file node: %v", err)
	}
	if err := legacyDB.Close(); err != nil {
		t.Fatalf("close legacy PostgreSQL connection: %v", err)
	}

	// Cleanups run before the schema drop registered earlier; Close is
	// idempotent, so the explicit Close before reopening is safe too.
	open := func(step string) *PostgresStore {
		t.Helper()
		store, err := NewPostgresStore(scopedURL, StoreConnectionOptions{}, false)
		if err != nil {
			t.Fatalf("%s: %v", step, err)
		}
		t.Cleanup(func() { _ = store.Close() })
		return store
	}
	save := func(store *PostgresStore, id string) {
		t.Helper()
		if err := store.SaveObjectActivity(&ObjectActivityEvent{ID: id, ProjectID: project, ResourceType: "script", ResourceID: "legacy-script", ResourceName: "Checkout", Action: "modify", ActorType: "user", ActorSubject: "alice", Status: "success", CreatedAt: tied}); err != nil {
			t.Fatalf("SaveObjectActivity(%s): %v", id, err)
		}
	}
	list := func(store *PostgresStore) string {
		t.Helper()
		events, err := store.ListObjectActivity(ObjectActivityFilter{ProjectID: project})
		if err != nil {
			t.Fatalf("ListObjectActivity returned error: %v", err)
		}
		return strings.Join(objectActivityIDs(events), ",")
	}

	warnings := captureInsertSeqWarnings(t)
	store := open("initialize PostgreSQL store over legacy object_activity_events")
	var legacyNulls int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM object_activity_events WHERE insert_seq IS NULL`).Scan(&legacyNulls); err != nil {
		t.Fatalf("count legacy rows without insert_seq: %v", err)
	}
	if legacyNulls != 8 {
		t.Fatalf("legacy rows without insert_seq = %d, want 8 (the backfilled copies are numbered)", legacyNulls)
	}
	var upgradedFile int64
	if err := store.db.QueryRow(`SELECT pg_relation_filenode('object_activity_events')`).Scan(&upgradedFile); err != nil {
		t.Fatalf("read upgraded object_activity_events file node: %v", err)
	}
	if upgradedFile != legacyFile {
		t.Fatalf("object_activity_events file node = %d, want %d (the upgrade must not rewrite the table)", upgradedFile, legacyFile)
	}
	if state, err := store.inspectPostgresInsertSeq(objectActivityTable); err != nil || !state.linked {
		t.Fatalf("upgraded object activity sequence state = %+v (err=%v), want linked", state, err)
	}
	audit, err := store.ListAuditEvents(AuditEventFilter{ProjectID: backfillProject, Limit: 10})
	if err != nil {
		t.Fatalf("ListAuditEvents returned error: %v", err)
	}
	backfilled, err := store.ListObjectActivity(ObjectActivityFilter{ProjectID: backfillProject})
	if err != nil {
		t.Fatalf("ListObjectActivity returned error: %v", err)
	}
	if got, want := strings.Join(auditEventIDs(audit), ","), "audit-y,audit-z,audit-c,audit-b,audit-a"; got != want {
		t.Fatalf("legacy audit order = %s, want %s", got, want)
	}
	if got, want := strings.Join(objectActivityIDs(backfilled), ","), "legacy:audit-y,legacy:audit-z,legacy:audit-c,legacy:audit-b,legacy:audit-a"; got != want {
		t.Fatalf("backfilled activity order = %s, want %s (as the audit list)", got, want)
	}
	for _, event := range []*ObjectActivityEvent{
		{ID: "a-new-reborn-create", Action: "create", ResourceID: "reborn"},
		{ID: "z-new-kept-modify", Action: "modify", ResourceID: "kept"},
		{ID: "a-new-gone-create", Action: "create", ResourceID: "gone"},
	} {
		event.ProjectID, event.ResourceType, event.ResourceName, event.ActorType, event.ActorSubject, event.Status, event.CreatedAt = summaryProject, "script", "Checkout", "user", "alice", "success", tied
		if err := store.SaveObjectActivity(event); err != nil {
			t.Fatalf("SaveObjectActivity(%s): %v", event.ID, err)
		}
	}
	summaries, err := store.ObjectActivitySummaries(summaryProject, "script", []string{"reborn", "kept", "idonly", "gone"})
	if err != nil {
		t.Fatalf("ObjectActivitySummaries returned error: %v", err)
	}
	id := func(event *ObjectActivityEvent) string {
		if event == nil {
			return ""
		}
		return event.ID
	}
	for resource, want := range map[string][2]string{
		"reborn": {"a-new-reborn-create", ""},
		"kept":   {"a-legacy-kept-create", "z-new-kept-modify"},
		"idonly": {"z-legacy-idonly-create", ""},
		"gone":   {"a-new-gone-create", ""},
	} {
		summary := summaries[resource]
		if got := [2]string{id(summary.Creator), id(summary.LastModifier)}; got != want {
			t.Errorf("resource %s creator/last modifier = %v, want %v", resource, got, want)
		}
	}
	activity, err := store.ListObjectActivity(ObjectActivityFilter{ProjectID: summaryProject})
	if err != nil {
		t.Fatalf("ListObjectActivity returned error: %v", err)
	}
	// Numbered rows first, newest first; then the NULL rows by ID.
	if got, want := strings.Join(objectActivityIDs(activity), ","), "a-new-gone-create,z-new-kept-modify,a-new-reborn-create,z-legacy-reborn-modify,z-legacy-idonly-create,z-legacy-gone-delete,a-legacy-kept-create,a-legacy-idonly-modify"; got != want {
		t.Fatalf("summary project activity order = %s, want %s", got, want)
	}
	for _, event := range activity {
		if event.Deleted {
			t.Errorf("%s (resource %s) is marked deleted; every summary object exists", event.ID, event.ResourceID)
		}
	}
	// Rows from before the upgrade list below newer rows with the same time.
	save(store, "z-new-first")
	save(store, "a-new-second")
	if got, want := list(store), "a-new-second,z-new-first,legacy-c,legacy-b,legacy-a"; got != want {
		t.Fatalf("activity order after upgrade = %s, want %s", got, want)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close upgraded PostgreSQL store: %v", err)
	}

	// A second initialization skips the migration, and the sequence keeps counting.
	reopened := open("reopen upgraded PostgreSQL store")
	save(reopened, "m-new-third")
	if got, want := list(reopened), "m-new-third,a-new-second,z-new-first,legacy-c,legacy-b,legacy-a"; got != want {
		t.Fatalf("activity order after reopen = %s, want %s", got, want)
	}
	if got := warnings(); len(got) != 0 {
		t.Fatalf("linked sequences logged insert_seq warnings: %+v", got)
	}
}
