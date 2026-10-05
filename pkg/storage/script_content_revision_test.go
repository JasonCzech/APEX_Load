package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func removeScriptRevisionTrackingForTest(store *SQLiteStore) error {
	if store.db.dialect == storeDialectPostgres {
		for _, entry := range []struct{ name, table string }{
			{"apexload_script_content_parent", "scripts"},
			{"apexload_script_content_placement", "script_library_assignments"},
			{"apexload_script_content_release", "script_library_assignments_releases"},
		} {
			if _, err := store.db.Exec(`DROP TRIGGER IF EXISTS ` + entry.name + ` ON ` + entry.table); err != nil {
				return err
			}
		}
		for _, function := range postgresScriptRevisionFunctions() {
			if _, err := store.db.Exec(`DROP FUNCTION IF EXISTS ` + function.name + `() `); err != nil {
				return err
			}
		}
	} else {
		for _, trigger := range sqliteScriptRevisionTriggers() {
			if _, err := store.db.Exec(`DROP TRIGGER IF EXISTS ` + trigger.name); err != nil {
				return err
			}
		}
	}
	return nil
}

func newScriptRevisionTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "script-revision.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func requireScriptAuthoring(t *testing.T, store *SQLiteStore, id string) *ScriptAuthoringSnapshot {
	t.Helper()
	snapshot, err := store.GetScriptAuthoringSnapshot(id)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot == nil || snapshot.Script == nil || !ValidScriptContentRevision(snapshot.ContentRevision) || snapshot.LibraryAssignment == nil {
		t.Fatalf("invalid snapshot: %#v", snapshot)
	}
	return snapshot
}

func scriptAuthoringExpected(snapshot *ScriptAuthoringSnapshot) ScriptAuthoringPrecondition {
	return ScriptAuthoringPrecondition{ProjectID: snapshot.Script.ProjectID, CreatedAt: snapshot.Script.CreatedAt, ContentRevision: snapshot.ContentRevision}
}

func requireRawScriptRevision(t *testing.T, store *SQLiteStore, id string) string {
	t.Helper()
	var value string
	if err := store.db.QueryRow(`SELECT content_revision FROM scripts WHERE id=?`, id).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if !ValidScriptContentRevision(value) {
		t.Fatalf("invalid persisted nonce %q", value)
	}
	return value
}

func TestScriptContentRevisionGrammar(t *testing.T) {
	valid := "scr-v1-" + strings.Repeat("a0", 32)
	for _, tc := range []struct {
		value string
		valid bool
	}{
		{valid, true}, {"", false}, {"scr-v1-" + strings.Repeat("A0", 32), false}, {valid + "0", false}, {valid[:len(valid)-1], false}, {"scr-v2-" + valid[7:], false}, {" " + valid, false}, {valid[:len(valid)-1] + "g", false},
	} {
		if got := ValidScriptContentRevision(tc.value); got != tc.valid {
			t.Fatalf("grammar %q=%t want%t", tc.value, got, tc.valid)
		}
	}
}

func exerciseScriptContentParentTriggers(t *testing.T, store *SQLiteStore) {
	t.Helper()
	now := time.Date(2026, 1, 2, 3, 4, 5, 123456000, time.UTC)
	for _, id := range []string{"revision-project", "revision-other"} {
		if err := store.SaveProject(&Project{ID: id, Name: id}); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"revision-folder", "revision-folder-2"} {
		if err := store.SaveFolder(&Folder{ID: id, ProjectID: "revision-project", Name: id, Category: "scripts"}); err != nil {
			t.Fatal(err)
		}
	}
	script := &dsl.Script{ID: "revision-parent", ProjectID: "revision-project", Name: "Original", Version: "1", Protocol: "http", CreatedAt: now}
	if err := store.SaveScript(script); err != nil {
		t.Fatal(err)
	}
	columns := []struct {
		column string
		value  any
	}{
		{"ref_key", "script:REV:1"}, {"name", "Changed"}, {"protocol", "grpc"}, {"version", "2"},
		{"project_id", "revision-other"}, {"folder_id", "revision-folder"}, {"generated_by", "author"}, {"generated_from", "source"},
		{"metadata_json", `{}`}, {"goal_config_json", `{}`}, {"config", `{"base_url":"https://example.invalid"}`}, {"data_sources", `[]`},
		{"parameters", `[]`}, {"init", `[]`}, {"actions", `[]`}, {"teardown", `[]`}, {"created_at", now.Add(time.Hour)},
	}
	for _, tc := range columns {
		t.Run(tc.column, func(t *testing.T) {
			// Set a distinct first state so JSON nil/empty changes are exercised too.
			var prior any
			if err := store.db.QueryRow(`SELECT `+tc.column+` FROM scripts WHERE id=?`, script.ID).Scan(&prior); err != nil {
				t.Fatal(err)
			}
			before := requireRawScriptRevision(t, store, script.ID)
			if _, err := store.db.Exec(`UPDATE scripts SET `+tc.column+`=? WHERE id=?`, tc.value, script.ID); err != nil {
				t.Fatal(err)
			}
			after := requireRawScriptRevision(t, store, script.ID)
			// [] could already be encoded as [] after a preceding direct writer.
			if reflect.DeepEqual(prior, tc.value) {
				if before != after {
					t.Fatal("equal authored value changed nonce")
				}
			} else if before == after {
				t.Fatalf("authored column %s did not rotate", tc.column)
			}
			if _, err := store.db.Exec(`UPDATE scripts SET `+tc.column+`=? WHERE id=?`, tc.value, script.ID); err != nil {
				t.Fatal(err)
			}
			if got := requireRawScriptRevision(t, store, script.ID); got != after {
				t.Fatal("null-safe equal write changed nonce")
			}
			if _, err := store.db.Exec(`UPDATE scripts SET `+tc.column+`=? WHERE id=?`, prior, script.ID); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(prior, tc.value) && requireRawScriptRevision(t, store, script.ID) == before {
				t.Fatal("away/back reused original nonce")
			}
		})
	}
	before := requireRawScriptRevision(t, store, script.ID)
	for _, query := range []string{`UPDATE scripts SET id=id WHERE id=?`, `UPDATE scripts SET updated_at=updated_at WHERE id=?`, `UPDATE scripts SET updated_at=CURRENT_TIMESTAMP WHERE id=?`} {
		if _, err := store.db.Exec(query, script.ID); err != nil {
			t.Fatal(err)
		}
	}
	if got := requireRawScriptRevision(t, store, script.ID); got != before {
		t.Fatal("lock/timestamp-only update rotated nonce")
	}
	if _, err := store.db.Exec(`UPDATE scripts SET id=? WHERE id=?`, "revision-parent-renamed", script.ID); err != nil {
		t.Fatal(err)
	}
	if requireRawScriptRevision(t, store, "revision-parent-renamed") == before {
		t.Fatal("id incarnation change did not rotate")
	}
	if _, err := store.db.Exec(`INSERT INTO scripts (id,name,protocol,version,project_id,config,data_sources,parameters,init,actions,teardown,created_at,updated_at,content_revision) VALUES (?, 'Inserted','http','1',?,'{}','[]','[]','[]','[]','[]',?,?,?)`, "revision-insert", DefaultProjectID, now, now, "scr-v1-"+strings.Repeat("0", 64)); err != nil {
		t.Fatal(err)
	}
	if got := requireRawScriptRevision(t, store, "revision-insert"); got == "scr-v1-"+strings.Repeat("0", 64) {
		t.Fatal("INSERT accepted caller-supplied nonce")
	}
}

func TestScriptContentRevisionParentTriggers(t *testing.T) {
	exerciseScriptContentParentTriggers(t, newScriptRevisionTestStore(t))
}

func exerciseScriptAuthoringCAS(t *testing.T, store *SQLiteStore) {
	t.Helper()
	script := &dsl.Script{ID: "revision-cas", ProjectID: DefaultProjectID, Name: "Original", Protocol: "http", Version: "same-user-version", GoalConfig: scriptGoalsFixture()}
	created, err := store.CreateScriptAuthoringIfAbsent(script)
	if err != nil || !created.Applied {
		t.Fatalf("create: %#v %v", created, err)
	}
	snapshot := created.Snapshot
	if !ValidScriptContentRevision(snapshot.ContentRevision) {
		t.Fatal("create omitted actual minted nonce")
	}
	other := *snapshot.Script
	other.Name = "Must not overwrite"
	conflict, err := store.CreateScriptAuthoringIfAbsent(&other)
	if err != nil || conflict.Applied || conflict.Snapshot.ContentRevision != snapshot.ContentRevision || conflict.Snapshot.Script.Name != "Original" {
		t.Fatalf("create-only conflict: %#v %v", conflict, err)
	}
	candidate := *snapshot.Script
	candidate.Name = "Updated"
	candidate.GoalConfig = &dsl.GoalConfig{}
	applied, err := store.UpdateScriptAuthoringIfCurrent(&candidate, scriptAuthoringExpected(snapshot))
	if err != nil || !applied.Applied || applied.Snapshot.Script.Name != "Updated" || applied.Snapshot.ContentRevision == snapshot.ContentRevision {
		t.Fatalf("conditional write: %#v %v", applied, err)
	}
	if current := requireScriptAuthoring(t, store, script.ID); !reflect.DeepEqual(current, applied.Snapshot) {
		t.Fatal("returned snapshot differs from final committed content")
	}
	for _, tc := range []struct {
		name     string
		expected ScriptAuthoringPrecondition
		want     error
	}{
		{"stale", scriptAuthoringExpected(snapshot), ErrScriptAuthoringConflict},
		{"foreign", ScriptAuthoringPrecondition{ProjectID: "revision-foreign", CreatedAt: snapshot.Script.CreatedAt, ContentRevision: applied.Snapshot.ContentRevision}, ErrScriptAuthoringNotFound},
		{"incarnation", ScriptAuthoringPrecondition{ProjectID: DefaultProjectID, CreatedAt: snapshot.Script.CreatedAt.Add(time.Second), ContentRevision: applied.Snapshot.ContentRevision}, ErrScriptAuthoringNotFound},
		{"malformed", ScriptAuthoringPrecondition{ProjectID: DefaultProjectID, CreatedAt: snapshot.Script.CreatedAt, ContentRevision: "bad"}, ErrScriptAuthoringInvalidRevision},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := *applied.Snapshot.Script
			candidate.Name = "Rejected"
			// Candidate project is server-controlled current expected ownership.
			if tc.name == "foreign" {
				candidate.ProjectID = tc.expected.ProjectID
				if err := store.SaveProject(&Project{ID: candidate.ProjectID, Name: "Foreign"}); err != nil {
					t.Fatal(err)
				}
			}
			got, err := store.UpdateScriptAuthoringIfCurrent(&candidate, tc.expected)
			if !errors.Is(err, tc.want) || got != nil {
				t.Fatalf("result %#v error%v want%v", got, err, tc.want)
			}
			if current := requireScriptAuthoring(t, store, script.ID); current.ContentRevision != applied.Snapshot.ContentRevision || current.Script.Name != "Updated" {
				t.Fatal("rejected CAS changed stored content")
			}
		})
	}
	unchanged := *applied.Snapshot.Script
	same, err := store.UpdateScriptAuthoringIfCurrent(&unchanged, scriptAuthoringExpected(applied.Snapshot))
	if err != nil || same.Snapshot.ContentRevision != applied.Snapshot.ContentRevision {
		t.Fatalf("logical no-op token churn %#v %v", same, err)
	}
	// Compatibility timestamp methods still operate, but nonce methods preserve
	// identity across equal timestamp direct writes.
	before := requireScriptAuthoring(t, store, script.ID)
	if _, err := store.db.Exec(`UPDATE scripts SET name=?,updated_at=? WHERE id=?`, "Direct import", before.Script.UpdatedAt, script.ID); err != nil {
		t.Fatal(err)
	}
	stale := *before.Script
	stale.Name = "Lost"
	if got, err := store.UpdateScriptAuthoringIfCurrent(&stale, scriptAuthoringExpected(before)); got != nil || !errors.Is(err, ErrScriptAuthoringConflict) {
		t.Fatalf("equal timestamp bypass: %#v %v", got, err)
	}
	current := requireScriptAuthoring(t, store, script.ID)
	legacy := *current.Script
	legacy.Name = "Legacy CAS"
	if ok, err := store.UpdateScriptIfUnchanged(&legacy, current.Script.UpdatedAt); err != nil || !ok {
		t.Fatalf("timestamp compatibility %t %v", ok, err)
	}
}

func TestScriptAuthoringCAS(t *testing.T) {
	exerciseScriptAuthoringCAS(t, newScriptRevisionTestStore(t))
}

func exerciseScriptAuthoringPlacement(t *testing.T, store *SQLiteStore) {
	t.Helper()
	seedLibraryAssignments(t, store)
	snapshot := requireScriptAuthoring(t, store, "la-script")
	if before, result, err := store.ReplaceScriptLibraryAssignmentIfCurrent("missing-script", scriptAuthoringExpected(snapshot), nil, nil, 0); before != nil || result != nil || !errors.Is(err, ErrScriptAuthoringNotFound) {
		t.Fatalf("missing conditional placement %#v %#v %v", before, result, err)
	}
	if _, _, err := store.ReplaceLibraryAssignment("script", "missing-script", snapshot.Script.ProjectID, nil, nil, 0); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("legacy missing placement error changed: %v", err)
	}
	_, empty, err := store.ReplaceScriptLibraryAssignmentIfCurrent("la-script", scriptAuthoringExpected(snapshot), nil, nil, 0)
	if err != nil || empty.Snapshot.ContentRevision != snapshot.ContentRevision || empty.Snapshot.LibraryAssignment.Revision != 1 {
		t.Fatalf("empty placement materialization %#v %v", empty, err)
	}
	app := "la-app"
	_, assigned, err := store.ReplaceScriptLibraryAssignmentIfCurrent("la-script", scriptAuthoringExpected(empty.Snapshot), &app, []string{"la-family", "la-r2", "la-r2"}, 1)
	if err != nil || assigned.Snapshot.ContentRevision == snapshot.ContentRevision || len(assigned.Snapshot.LibraryAssignment.Releases) != 2 {
		t.Fatalf("placement %#v %v", assigned, err)
	}
	if current := requireScriptAuthoring(t, store, "la-script"); !reflect.DeepEqual(current, assigned.Snapshot) {
		t.Fatal("placement returned earlier trigger nonce")
	}
	_, same, err := store.ReplaceScriptLibraryAssignmentIfCurrent("la-script", scriptAuthoringExpected(assigned.Snapshot), &app, []string{"la-r2", "la-r1"}, 2)
	if err != nil || same.Snapshot.ContentRevision != assigned.Snapshot.ContentRevision || same.Snapshot.LibraryAssignment.Revision != 3 {
		t.Fatalf("equivalent normalized set churn %#v %v", same, err)
	}
	for _, query := range []string{`UPDATE applications SET name='Renamed' WHERE id='la-app'`, `UPDATE releases SET name='Renamed' WHERE id='la-r1'`, `UPDATE script_library_assignments SET revision=revision+1 WHERE item_id='la-script'`, `UPDATE script_library_assignments_releases SET item_id=item_id,release_id=release_id WHERE item_id='la-script'`} {
		if _, err := store.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if requireScriptAuthoring(t, store, "la-script").ContentRevision != same.Snapshot.ContentRevision {
		t.Fatal("names/counter/edge no-op churned nonce")
	}
	if err := store.SaveScript(&dsl.Script{ID: "la-transfer", ProjectID: "la-project", Name: "Transfer"}); err != nil {
		t.Fatal(err)
	}
	other := requireScriptAuthoring(t, store, "la-transfer")
	if _, err := store.db.Exec(`INSERT INTO script_library_assignments(item_id,application_id,revision) VALUES ('la-transfer','la-app',0)`); err != nil {
		t.Fatal(err)
	}
	if requireRawScriptRevision(t, store, "la-transfer") == other.ContentRevision {
		t.Fatal("app insertion did not rotate")
	}
	beforeA := requireRawScriptRevision(t, store, "la-script")
	beforeB := requireRawScriptRevision(t, store, "la-transfer")
	if _, err := store.db.Exec(`UPDATE script_library_assignments_releases SET item_id='la-transfer' WHERE item_id='la-script' AND release_id='la-r1'`); err != nil {
		t.Fatal(err)
	}
	if requireRawScriptRevision(t, store, "la-script") == beforeA || requireRawScriptRevision(t, store, "la-transfer") == beforeB {
		t.Fatal("release transfer failed to rotate both owners")
	}
	beforeB = requireRawScriptRevision(t, store, "la-transfer")
	if _, err := store.db.Exec(`DELETE FROM script_library_assignments WHERE item_id='la-transfer'`); err != nil {
		t.Fatal(err)
	}
	if requireRawScriptRevision(t, store, "la-transfer") == beforeB {
		t.Fatal("placement deletion did not rotate")
	}
	current := requireScriptAuthoring(t, store, "la-script")
	if ok, err := store.DeleteScriptAuthoringIfCurrent("la-script", scriptAuthoringExpected(snapshot)); ok || !errors.Is(err, ErrScriptAuthoringConflict) {
		t.Fatalf("stale delete %t %v", ok, err)
	}
	// Remove referencing scenario first; deletion then cleans placement children
	// and must not self-conflict after their immediate trigger bumps.
	if err := store.DeleteScenario("la-scenario"); err != nil {
		t.Fatal(err)
	}
	if ok, err := store.DeleteScriptAuthoringIfCurrent("la-script", scriptAuthoringExpected(current)); err != nil || !ok {
		t.Fatalf("conditional delete with children %t %v", ok, err)
	}
	if _, err := store.GetScriptAuthoringSnapshot("la-script"); !errors.Is(err, ErrScriptAuthoringNotFound) {
		t.Fatalf("deleted parent lookup%v", err)
	}
}

func TestScriptAuthoringPlacement(t *testing.T) {
	exerciseScriptAuthoringPlacement(t, newScriptRevisionTestStore(t))
}

func exerciseScriptAuthoringPolicyAndRuleset(t *testing.T, store *SQLiteStore) {
	t.Helper()
	script := &dsl.Script{ID: "revision-policy", ProjectID: DefaultProjectID, Name: "Policy"}
	if err := store.SaveScript(script); err != nil {
		t.Fatal(err)
	}
	old := requireScriptAuthoring(t, store, script.ID)
	project, err := store.GetProject(DefaultProjectID)
	if err != nil {
		t.Fatal(err)
	}
	project.Settings.Authoring = &ProjectAuthoringPolicy{RequireScriptSaveIfMatch: true}
	if err := store.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	expected := scriptAuthoringExpected(old)
	expected.LegacyOrdinarySave = true
	candidate := *old.Script
	candidate.Name = "Rejected compatibility"
	if result, err := store.UpdateScriptAuthoringIfCurrent(&candidate, expected); result != nil || !errors.Is(err, ErrScriptAuthoringPreconditionRequired) {
		t.Fatalf("locked policy %#v %v", result, err)
	}
	expected.LegacyOrdinarySave = false
	ruleset := &CorrelationRuleset{ID: "revision-rules", ProjectID: DefaultProjectID, Name: "Rules"}
	if err := store.SaveCorrelationRuleset(ruleset); err != nil {
		t.Fatal(err)
	}
	prior := ruleset.UpdatedAt
	ruleset.Name = "Changed"
	if err := store.SaveCorrelationRuleset(ruleset); err != nil {
		t.Fatal(err)
	}
	if result, err := store.UpdateScriptAuthoringIfCurrentAndCorrelationRulesetCurrent(&candidate, expected, ruleset.ID, DefaultProjectID, prior); result != nil || !errors.Is(err, ErrScriptAuthoringConflict) {
		t.Fatalf("ruleset stale %#v %v", result, err)
	}
	currentRuleset, err := store.GetCorrelationRuleset(ruleset.ID)
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.UpdateScriptAuthoringIfCurrentAndCorrelationRulesetCurrent(&candidate, expected, ruleset.ID, DefaultProjectID, currentRuleset.UpdatedAt)
	if err != nil || !result.Applied {
		t.Fatalf("explicit save under policy %#v %v", result, err)
	}
	folder := "revision-policy-folder"
	if err := store.SaveFolder(&Folder{ID: folder, ProjectID: DefaultProjectID, Name: "Folder", Category: "scripts"}); err != nil {
		t.Fatal(err)
	}
	moved, err := store.MoveScriptAuthoringIfCurrent(script.ID, &folder, scriptAuthoringExpected(result.Snapshot))
	if err != nil || moved.Snapshot.Script.FolderID == nil || *moved.Snapshot.Script.FolderID != folder || moved.Snapshot.ContentRevision == result.Snapshot.ContentRevision {
		t.Fatalf("move %#v %v", moved, err)
	}
	if _, err := store.MoveScriptAuthoringIfCurrent(script.ID, nil, scriptAuthoringExpected(result.Snapshot)); !errors.Is(err, ErrScriptAuthoringConflict) {
		t.Fatalf("stale move %v", err)
	}
}

func TestScriptAuthoringPolicyAndRuleset(t *testing.T) {
	exerciseScriptAuthoringPolicyAndRuleset(t, newScriptRevisionTestStore(t))
}

func TestScriptContentRevisionMigrationReopenAndRepair(t *testing.T) {
	path := filepath.Join(t.TempDir(), "revision-migration.db")
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveScript(&dsl.Script{ID: "migration-revision", Name: "Migration", ProjectID: DefaultProjectID, GoalConfig: scriptGoalsFixture()}); err != nil {
		t.Fatal(err)
	}
	before, err := store.SchemaStatus()
	if err != nil {
		t.Fatal(err)
	}
	if err := removeScriptRevisionTrackingForTest(store); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{`ALTER TABLE scripts DROP COLUMN content_revision`, `DELETE FROM schema_migrations WHERE version=37`} {
		if _, err := store.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	after, err := store.SchemaStatus()
	if err != nil || after.AppliedVersion != 37 || !reflect.DeepEqual(before.Applied[:36], after.Applied[:36]) {
		t.Fatalf("ledger %#v %v", after, err)
	}
	snapshot := requireScriptAuthoring(t, store, "migration-revision")
	if !reflect.DeepEqual(snapshot.Script.GoalConfig, scriptGoalsFixture()) {
		t.Fatal("release36 goals changed")
	}
	// Same-name wrong trigger shape must be repaired, while a valid nonce survives.
	if _, err := store.db.Exec(`DROP TRIGGER apexload_script_content_update`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`CREATE TRIGGER apexload_script_content_update AFTER UPDATE ON scripts BEGIN SELECT 1; END`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if current := requireScriptAuthoring(t, store, snapshot.Script.ID); current.ContentRevision != snapshot.ContentRevision {
		t.Fatal("reopen reminted valid token")
	}
	if _, err := store.db.Exec(`UPDATE scripts SET name='Repaired' WHERE id='migration-revision'`); err != nil {
		t.Fatal(err)
	}
	repaired := requireScriptAuthoring(t, store, snapshot.Script.ID)
	if repaired.ContentRevision == snapshot.ContentRevision {
		t.Fatal("same-name trigger wasn't repaired")
	}
	if _, err := store.db.Exec(`UPDATE scripts SET content_revision='' WHERE id='migration-revision'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetScriptAuthoringSnapshot(snapshot.Script.ID); !errors.Is(err, ErrScriptAuthoringUnavailable) {
		t.Fatalf("missing token accepted %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	backfilled := requireScriptAuthoring(t, store, snapshot.Script.ID)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if requireScriptAuthoring(t, store, snapshot.Script.ID).ContentRevision != backfilled.ContentRevision {
		t.Fatal("missing token backfill wasn't idempotent")
	}
	for _, invalid := range []string{"bad", " ", "scr-v1-" + strings.Repeat("A", 64)} {
		if _, err := store.db.Exec(`UPDATE scripts SET content_revision=? WHERE id=?`, invalid, snapshot.Script.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := store.GetScript(snapshot.Script.ID); !errors.Is(err, ErrScriptAuthoringUnavailable) {
			t.Fatalf("invalid persisted token accepted %v", err)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := NewSQLiteStore(path); !errors.Is(err, ErrScriptAuthoringUnavailable) {
			t.Fatalf("invalid token silently repaired %v", err)
		}
		// Privileged fixture repair only, keeping application initialization closed.
		raw, err := sql.Open("sqlite", sqliteDSN(path))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := raw.Exec(`UPDATE scripts SET content_revision=? WHERE id=?`, backfilled.ContentRevision, snapshot.Script.ID); err != nil {
			t.Fatal(err)
		}
		raw.Close()
		store, err = NewSQLiteStore(path)
		if err != nil {
			t.Fatal(err)
		}
	}
	store.Close()
}

func TestScriptAuthoringConcurrentCreateAndSnapshots(t *testing.T) {
	path := filepath.Join(t.TempDir(), "concurrent-authoring.db")
	first, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	var wg sync.WaitGroup
	outcomes := make(chan *ScriptAuthoringMutationResult, 2)
	failures := make(chan error, 2)
	for _, store := range []*SQLiteStore{first, second} {
		wg.Add(1)
		go func(store *SQLiteStore) {
			defer wg.Done()
			result, err := store.CreateScriptAuthoringIfAbsent(&dsl.Script{ID: "parallel-create", ProjectID: DefaultProjectID, Name: "Created"})
			outcomes <- result
			failures <- err
		}(store)
	}
	wg.Wait()
	close(outcomes)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	applied := 0
	var nonce string
	for outcome := range outcomes {
		if outcome.Applied {
			applied++
		}
		if nonce != "" && nonce != outcome.Snapshot.ContentRevision {
			t.Fatal("same ID returned different creation snapshots")
		}
		nonce = outcome.Snapshot.ContentRevision
	}
	if applied != 1 {
		t.Fatalf("created%d times", applied)
	}
	exerciseScriptAuthoringCoherentRead(t, first, second)
}

func exerciseScriptAuthoringReferenceLookup(t *testing.T, store *SQLiteStore) {
	t.Helper()
	seedLibraryAssignments(t, store)
	original := requireScriptAuthoring(t, store, "la-script")
	script := *original.Script
	script.RefKey = "LOC-SCR-0001-v1"
	script.GoalConfig = scriptGoalsFixture()
	if err := store.SaveScript(&script); err != nil {
		t.Fatal(err)
	}
	app := "la-app"
	if _, _, err := store.ReplaceLibraryAssignment("script", script.ID, script.ProjectID, &app, []string{"la-r1", "la-r2"}, 0); err != nil {
		t.Fatal(err)
	}
	expected := requireScriptAuthoring(t, store, script.ID)
	found, err := store.GetScriptAuthoringSnapshotByRefKey(script.RefKey)
	if err != nil || !reflect.DeepEqual(found, expected) {
		t.Fatalf("reference lost paired content/goals/placement/nonce: %#v %v", found, err)
	}
	for _, key := range []string{"", " ", "LOC-SCR-0099-v1", strings.ToLower(script.RefKey), " " + script.RefKey, script.ID, "' OR 1=1 --"} {
		if found, err := store.GetScriptAuthoringSnapshotByRefKey(key); found != nil || !errors.Is(err, ErrScriptAuthoringNotFound) {
			t.Fatalf("inexact/missing reference %q: %#v %v", key, found, err)
		}
	}
	if _, err := store.db.Exec(`UPDATE scripts SET ref_key=? WHERE id=?`, "LOC-SCR-0002-v1", script.ID); err != nil {
		t.Fatal(err)
	}
	if found, err := store.GetScriptAuthoringSnapshotByRefKey(script.RefKey); found != nil || !errors.Is(err, ErrScriptAuthoringNotFound) {
		t.Fatalf("obsolete reference resolved: %#v %v", found, err)
	}
	expected = requireScriptAuthoring(t, store, script.ID)
	if found, err := store.GetScriptAuthoringSnapshotByRefKey(expected.Script.RefKey); err != nil || !reflect.DeepEqual(found, expected) || found.ContentRevision == original.ContentRevision {
		t.Fatalf("changed reference did not resolve current durable snapshot: %#v %v", found, err)
	}
	if _, err := store.db.Exec(`UPDATE scripts SET content_revision='invalid' WHERE id=?`, script.ID); err != nil {
		t.Fatal(err)
	}
	if found, err := store.GetScriptAuthoringSnapshotByRefKey(expected.Script.RefKey); found != nil || !errors.Is(err, ErrScriptAuthoringUnavailable) {
		t.Fatalf("invalid persisted reference nonce accepted: %#v %v", found, err)
	}
	if _, err := store.db.Exec(`UPDATE scripts SET content_revision=? WHERE id=?`, expected.ContentRevision, script.ID); err != nil {
		t.Fatal(err)
	}
	// A damaged legacy index must not turn ambiguous references into random
	// ownership selection. This disposable fixture intentionally drops it.
	if _, err := store.db.Exec(`DROP INDEX idx_scripts_ref_key`); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveScript(&dsl.Script{ID: "duplicate-reference-owner", RefKey: expected.Script.RefKey, Name: "Duplicate", ProjectID: "la-foreign"}); err != nil {
		t.Fatal(err)
	}
	if found, err := store.GetScriptAuthoringSnapshotByRefKey(expected.Script.RefKey); found != nil || !errors.Is(err, ErrScriptAuthoringUnavailable) {
		t.Fatalf("ambiguous cross-project reference resolved: %#v %v", found, err)
	}
}

func TestScriptAuthoringReferenceLookup(t *testing.T) {
	exerciseScriptAuthoringReferenceLookup(t, newScriptRevisionTestStore(t))
}

func exerciseScriptAuthoringCoherentRead(t *testing.T, reader, writer *SQLiteStore) {
	t.Helper()
	seedLibraryAssignments(t, writer)
	if _, err := writer.db.Exec(`UPDATE scripts SET ref_key='LOC-SCR-0010-v1' WHERE id='la-script'`); err != nil {
		t.Fatal(err)
	}
	app := "la-app"
	if _, _, err := writer.ReplaceLibraryAssignment("script", "la-script", "la-project", &app, []string{"la-r1"}, 0); err != nil {
		t.Fatal(err)
	}
	tx, err := reader.beginScriptAuthoringRead()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	old, err := readScriptAuthoringSnapshotByRefKeyTx(tx, "LOC-SCR-0010-v1")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		tx, err := writer.db.BeginTx(context.Background(), &sql.TxOptions{})
		if err != nil {
			done <- err
			return
		}
		defer tx.Rollback()
		if _, err = tx.Exec(`UPDATE scripts SET name='New coherent placement',ref_key='LOC-SCR-0011-v1' WHERE id='la-script'`); err == nil {
			_, err = tx.Exec(`UPDATE script_library_assignments SET application_id='la-other',revision=revision+1 WHERE item_id='la-script'`)
		}
		if err == nil {
			_, err = tx.Exec(`DELETE FROM script_library_assignments_releases WHERE item_id='la-script'`)
		}
		if err == nil {
			_, err = tx.Exec(`INSERT INTO script_library_assignments_releases(item_id,release_id) VALUES ('la-script','la-other-r')`)
		}
		if err == nil {
			err = tx.Commit()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("controlled coherent read writer timed out")
	}
	paired, err := readScriptAuthoringSnapshotByRefKeyTx(tx, "LOC-SCR-0010-v1")
	if err != nil || !reflect.DeepEqual(paired, old) {
		t.Fatalf("reference resolution mixed versions within read transaction: %#v %v", paired, err)
	}
	assignments, err := readLibraryAssignments(tx, "script_library_assignments", []string{"la-script"})
	if err != nil {
		t.Fatal(err)
	}
	if old.Script.Name != "Script" || assignments["la-script"].Application.ID != "la-app" || assignments["la-script"].Releases[0].ID != "la-r1" {
		t.Fatal("read transaction mixed old parent with new placement")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	current := requireScriptAuthoring(t, reader, "la-script")
	if stale, err := reader.GetScriptAuthoringSnapshotByRefKey("LOC-SCR-0010-v1"); stale != nil || !errors.Is(err, ErrScriptAuthoringNotFound) {
		t.Fatalf("replica retained old reference: %#v %v", stale, err)
	}
	if found, err := reader.GetScriptAuthoringSnapshotByRefKey("LOC-SCR-0011-v1"); err != nil || !reflect.DeepEqual(found, current) {
		t.Fatalf("replica did not resolve current durable reference: %#v %v", found, err)
	}
	if current.Script.Name != "New coherent placement" || current.LibraryAssignment.Application.ID != "la-other" || current.LibraryAssignment.Releases[0].ID != "la-other-r" || current.ContentRevision == old.ContentRevision {
		t.Fatal("current snapshot not wholly new")
	}
	listed, err := reader.ListScriptAuthoringSnapshots("la-project", nil)
	if err != nil || len(listed) != 1 || !reflect.DeepEqual(listed[0], current) {
		t.Fatalf("coherent list%#v %v", listed, err)
	}
}

func exerciseScriptRevisionImports(t *testing.T, store *SQLiteStore) {
	t.Helper()
	script := &dsl.Script{ID: "revision-github", Name: "GitHub", Protocol: "http", ProjectID: DefaultProjectID, GoalConfig: scriptGoalsFixture()}
	link := &GitHubScriptLink{ScriptID: script.ID, Owner: "owner", Repo: "repo", Branch: "main", Path: "revision.json"}
	if outcome, err := store.CommitGitHubScriptImport(script, link, time.Time{}); err != nil || outcome != GitHubScriptImportCreated {
		t.Fatalf("github create%s %v", outcome, err)
	}
	snapshot := requireScriptAuthoring(t, store, script.ID)
	retry := *snapshot.Script
	if outcome, err := store.CommitGitHubScriptImport(&retry, link, retry.UpdatedAt); err != nil || outcome != GitHubScriptImportUnchanged {
		t.Fatalf("github no-op%s %v", outcome, err)
	}
	if requireScriptAuthoring(t, store, script.ID).ContentRevision != snapshot.ContentRevision {
		t.Fatal("github no-op rotated nonce")
	}
	candidate := *snapshot.Script
	candidate.GoalConfig = &dsl.GoalConfig{}
	candidate.UpdatedAt = snapshot.Script.UpdatedAt.Add(time.Second)
	if outcome, err := store.CommitGitHubScriptImport(&candidate, link, snapshot.Script.UpdatedAt); err != nil || outcome != GitHubScriptImportUpdated {
		t.Fatalf("github goals%s %v", outcome, err)
	}
	if requireScriptAuthoring(t, store, script.ID).ContentRevision == snapshot.ContentRevision {
		t.Fatal("github direct writer omitted nonce")
	}
	if err := store.SaveProject(&Project{ID: "project-bruno", Name: "Bruno"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveFolder(&Folder{ID: "native-folder", ProjectID: "project-bruno", Name: "Native", Category: "scripts"}); err != nil {
		t.Fatal(err)
	}
	first := brunoImportCommitFixture("revision-bruno", "project-bruno", "revision-source-1", "revision-raw-1", "revision-san-1", "revision-raw-sha-1", "revision-san-sha-1")
	result, err := store.CommitBrunoImport(first)
	if err != nil {
		t.Fatal(err)
	}
	if current := requireScriptAuthoring(t, store, first.Script.ID); !reflect.DeepEqual(current, result.AuthoringSnapshot) {
		t.Fatal("Bruno return isn't final TX snapshot")
	}
	original := result.AuthoringSnapshot
	expected := scriptAuthoringExpected(original)
	second := brunoImportCommitFixture(first.Script.ID, "project-bruno", "revision-source-2", "revision-raw-2", "revision-san-2", "revision-raw-sha-2", "revision-san-sha-2")
	second.ExpectedScriptAuthoring = &expected
	second.ExpectedScriptUpdatedAt = &original.Script.UpdatedAt
	second.ExpectedActiveRevisionID = first.Revision.ID
	result, err = store.CommitBrunoImport(second)
	if err != nil || result.AuthoringSnapshot.ContentRevision == original.ContentRevision {
		t.Fatalf("Bruno opaque update %#v %v", result, err)
	}
	stale := brunoImportCommitFixture(first.Script.ID, "project-bruno", "revision-source-stale", "revision-raw-stale", "revision-san-stale", "revision-raw-sha-stale", "revision-san-sha-stale")
	stale.ExpectedScriptAuthoring = &expected
	stale.ExpectedActiveRevisionID = second.Revision.ID
	if _, err := store.CommitBrunoImport(stale); !errors.Is(err, ErrScriptAuthoringConflict) {
		t.Fatalf("Bruno stale nonce%v", err)
	}
	if _, err := store.GetImportSourceArtifact("revision-raw-stale", "project-bruno"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("stale import persisted artifact%v", err)
	}
	beforeRollback := requireScriptAuthoring(t, store, first.Script.ID)
	if _, _, err := store.ActivateImportSourceRevisionIfScriptCurrent(first.Script.ID, "project-bruno", first.Revision.ID, second.Revision.ID, expected); !errors.Is(err, ErrScriptAuthoringConflict) {
		t.Fatalf("stale source rollback%v", err)
	}
	revision, rolledBack, err := store.ActivateImportSourceRevisionIfScriptCurrent(first.Script.ID, "project-bruno", first.Revision.ID, second.Revision.ID, scriptAuthoringExpected(beforeRollback))
	if err != nil || !revision.Active || rolledBack.ContentRevision == beforeRollback.ContentRevision {
		t.Fatalf("source rollback %#v %v", rolledBack, err)
	}
	if current := requireScriptAuthoring(t, store, first.Script.ID); !reflect.DeepEqual(current, rolledBack) {
		t.Fatal("source rollback response token/content mismatch")
	}
	if _, _, err := store.ActivateImportSourceRevisionIfScriptCurrent(first.Script.ID, "project-bruno", second.Revision.ID, second.Revision.ID, scriptAuthoringExpected(rolledBack)); !errors.Is(err, ErrBrunoImportConcurrentChange) {
		t.Fatalf("source-active fence lost%v", err)
	}
	batch := brunoNativeImportCommitFixture("revision-native", "revision-native-digest")
	missing := "missing-revision-folder"
	valid := batch.Scripts[1].FolderID
	batch.Scripts[1].FolderID = &missing
	if _, err := store.CommitBrunoNativeImport(batch); err == nil {
		t.Fatal("invalid batch committed")
	}
	for _, script := range batch.Scripts {
		if _, err := store.GetScriptAuthoringSnapshot(script.ID); !errors.Is(err, ErrScriptAuthoringNotFound) {
			t.Fatalf("partial batch published%v", err)
		}
	}
	batch.Scripts[1].FolderID = valid
	native, err := store.CommitBrunoNativeImport(batch)
	if err != nil {
		t.Fatal(err)
	}
	revisions := map[string]string{}
	for _, script := range native.Scripts {
		revisions[script.ID] = requireScriptAuthoring(t, store, script.ID).ContentRevision
	}
	if _, err := store.CommitBrunoNativeImport(batch); err != nil {
		t.Fatal(err)
	}
	for id, revision := range revisions {
		if requireScriptAuthoring(t, store, id).ContentRevision != revision {
			t.Fatal("immutable receipt replay rotated authored nonce")
		}
	}
	// Narrow ref-key repair is authored; existing key and row locks are not.
	refScript := &dsl.Script{ID: "revision-refkey", Name: "Ref key", ProjectID: DefaultProjectID}
	if err := store.SaveScript(refScript); err != nil {
		t.Fatal(err)
	}
	beforeRef := requireScriptAuthoring(t, store, refScript.ID)
	if _, _, err := store.EnsureScriptRefKey(refScript.ID, "script:REV:9"); err != nil {
		t.Fatal(err)
	}
	afterRef := requireScriptAuthoring(t, store, refScript.ID)
	if afterRef.ContentRevision == beforeRef.ContentRevision {
		t.Fatal("refkey fill omitted nonce")
	}
	if _, _, err := store.EnsureScriptRefKey(refScript.ID, "script:REV:10"); err != nil {
		t.Fatal(err)
	}
	if requireScriptAuthoring(t, store, refScript.ID).ContentRevision != afterRef.ContentRevision {
		t.Fatal("existing refkey rotated nonce")
	}
}

func TestScriptContentRevisionImportWriters(t *testing.T) {
	exerciseScriptRevisionImports(t, newScriptRevisionTestStore(t))
}

func exerciseScriptRevisionBlankIDRepair(t *testing.T, store *SQLiteStore) {
	t.Helper()
	if err := store.SaveApplication(&Application{ID: "repair-app", ProjectID: DefaultProjectID, Name: "App"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRelease(&Release{ID: "repair-release", ApplicationID: "repair-app", Name: "Release"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := store.db.Exec(`INSERT INTO scripts(id,name,protocol,version,project_id,config,data_sources,parameters,init,actions,teardown,created_at,updated_at) VALUES (' ', 'Repair','http','1',?,'{}','[]','[]','[]','[]','[]',?,?)`, DefaultProjectID, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO script_library_assignments(item_id,application_id,revision) VALUES (' ','repair-app',7)`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO script_library_assignments_releases(item_id,release_id) VALUES (' ','repair-release')`); err != nil {
		t.Fatal(err)
	}
	before := requireRawScriptRevision(t, store, " ")
	if err := store.backfillBlankScriptIDs(); err != nil {
		t.Fatal(err)
	}
	listed, err := store.ListScriptAuthoringSnapshots(DefaultProjectID, nil)
	if err != nil || len(listed) != 1 {
		t.Fatalf("repair listing%#v %v", listed, err)
	}
	current := listed[0]
	if strings.TrimSpace(current.Script.ID) == "" || current.ContentRevision == before || current.LibraryAssignment.Application == nil || current.LibraryAssignment.Application.ID != "repair-app" || current.LibraryAssignment.Revision != 7 || len(current.LibraryAssignment.Releases) != 1 || current.LibraryAssignment.Releases[0].ID != "repair-release" {
		t.Fatalf("repair lost placement/incarnation: %#v", current)
	}
}

func TestScriptContentRevisionBlankIDRepairPreservesPlacement(t *testing.T) {
	exerciseScriptRevisionBlankIDRepair(t, newScriptRevisionTestStore(t))
}

func TestScriptContentRevisionFailedTransactionDoesNotPublish(t *testing.T) {
	store := newScriptRevisionTestStore(t)
	if err := store.SaveScript(&dsl.Script{ID: "rollback-revision", Name: "Original", ProjectID: DefaultProjectID}); err != nil {
		t.Fatal(err)
	}
	before := requireScriptAuthoring(t, store, "rollback-revision")
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`UPDATE scripts SET name='Temporary' WHERE id='rollback-revision'`); err != nil {
		t.Fatal(err)
	}
	inside, err := readScriptAuthoringSnapshotTx(tx, "rollback-revision")
	if err != nil {
		t.Fatal(err)
	}
	if inside.ContentRevision == before.ContentRevision {
		t.Fatal("immediate nonce not visible in TX")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if after := requireScriptAuthoring(t, store, "rollback-revision"); !reflect.DeepEqual(before, after) {
		t.Fatal("rolled-back content or nonce leaked")
	}
}

func TestScriptContentRevisionCommitFailureDiscardsSnapshot(t *testing.T) {
	store := newScriptRevisionTestStore(t)
	if err := store.SaveScript(&dsl.Script{ID: "commit-failure", Name: "Original", ProjectID: DefaultProjectID}); err != nil {
		t.Fatal(err)
	}
	original := requireScriptAuthoring(t, store, "commit-failure")
	if _, err := store.db.Exec(`CREATE TABLE script_revision_commit_failure (script_id TEXT REFERENCES scripts(id) DEFERRABLE INITIALLY DEFERRED)`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`CREATE TRIGGER script_revision_commit_failure AFTER UPDATE OF name ON scripts WHEN NEW.name='Fail at commit' BEGIN INSERT INTO script_revision_commit_failure(script_id) VALUES('nonexistent-parent'); END`); err != nil {
		t.Fatal(err)
	}
	candidate := *original.Script
	candidate.Name = "Fail at commit"
	result, err := store.UpdateScriptAuthoringIfCurrent(&candidate, scriptAuthoringExpected(original))
	if err == nil || result != nil {
		t.Fatalf("commit failure published a snapshot: %#v %v", result, err)
	}
	if current := requireScriptAuthoring(t, store, "commit-failure"); !reflect.DeepEqual(current, original) {
		t.Fatal("failed commit published content or minted nonce")
	}
}

func TestScriptContentRevisionConcurrentInitialization(t *testing.T) {
	path := filepath.Join(t.TempDir(), "parallel-init.db")
	// Seed a real prior36 installation, then let two controllers converge it.
	seeded, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := seeded.SaveScript(&dsl.Script{ID: "parallel-init", ProjectID: DefaultProjectID, Name: "Original"}); err != nil {
		t.Fatal(err)
	}
	if err := removeScriptRevisionTrackingForTest(seeded); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{`ALTER TABLE scripts DROP COLUMN content_revision`, `DELETE FROM schema_migrations WHERE version=37`} {
		if _, err := seeded.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	seeded.Close()
	outcomes := make(chan *SQLiteStore, 2)
	failures := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); store, err := NewSQLiteStore(path); outcomes <- store; failures <- err }()
	}
	wg.Wait()
	close(outcomes)
	close(failures)
	var opened []*SQLiteStore
	for store := range outcomes {
		if store != nil {
			opened = append(opened, store)
			t.Cleanup(func() { store.Close() })
		}
	}
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	var revision string
	for _, store := range opened {
		snapshot := requireScriptAuthoring(t, store, "parallel-init")
		if revision != "" && snapshot.ContentRevision != revision {
			t.Fatal("concurrent init minted more than one final nonce")
		}
		revision = snapshot.ContentRevision
		status, err := store.SchemaStatus()
		if err != nil || status.AppliedVersion != 37 || len(status.Pending) != 0 {
			t.Fatalf("concurrent ledger %#v %v", status, err)
		}
		store.Close()
	}
}

func TestScriptContentRevisionRemainsOutsideExecutableAndImportJSON(t *testing.T) {
	store := newScriptRevisionTestStore(t)
	result, err := store.CreateScriptAuthoringIfAbsent(&dsl.Script{ID: "nonce-boundary", Name: "Boundary", ProjectID: DefaultProjectID})
	if err != nil {
		t.Fatal(err)
	}
	expected := scriptAuthoringExpected(result.Snapshot)
	expected.LegacyOrdinarySave = true
	for _, value := range []any{result.Snapshot.Script, &BrunoImportCommit{Script: result.Snapshot.Script, ExpectedScriptAuthoring: &expected}} {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), result.Snapshot.ContentRevision) || strings.Contains(string(encoded), "LegacyOrdinarySave") || strings.Contains(string(encoded), "ExpectedScriptAuthoring") {
			t.Fatal("server nonce/admission intent entered executable/import JSON")
		}
	}
}
