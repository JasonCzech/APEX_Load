package storage

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// A row's created_at is its incarnation: authoring preconditions, recording
// bindings and rollback fences compare it exactly with the stored row. Go's
// clock has nanoseconds on Linux and PostgreSQL keeps microseconds, so every
// create path must leave the caller's copy equal to what it stored. These
// exercises run on SQLite here and on PostgreSQL in
// created_at_precision_postgres_integration_test.go.
var createdAtPrecisionExercises = []struct {
	name string
	run  func(*testing.T, *SQLiteStore)
}{
	{"script_creates", exerciseScriptCreatesKeepStoredCreatedAt},
	{"scenario_creates", exerciseScenarioCreatesKeepStoredCreatedAt},
	{"bruno_import", exerciseBrunoImportKeepsStoredTimestamps},
	{"github_import", exerciseGitHubImportCreateKeepsStoredCreatedAt},
	{"external_result_import", exerciseExternalResultImportRollbackFence},
	{"run_archive_cleanup", exerciseRunArchiveCleanupMatchesPublishedRecord},
}

func TestCreatedAtPrecision(t *testing.T) {
	for _, tc := range createdAtPrecisionExercises {
		t.Run(tc.name, func(t *testing.T) {
			store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "created-at-precision.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			tc.run(t, store)
		})
	}
}

// nanosecondCreatedAt has digits below a microsecond, as time.Now() has on Linux.
var nanosecondCreatedAt = time.Date(2026, 10, 18, 12, 0, 0, 123456789, time.UTC)

func requireStoredTimestamp(t *testing.T, what string, held, stored time.Time) {
	t.Helper()
	if !held.Equal(stored) || !held.Equal(held.Truncate(time.Microsecond)) {
		t.Errorf("%s held %s, stored %s: want the stored value, at microseconds", what, held.Format(time.RFC3339Nano), stored.Format(time.RFC3339Nano))
	}
}

func exerciseScriptCreatesKeepStoredCreatedAt(t *testing.T, store *SQLiteStore) {
	createIfAbsent := func(script *dsl.Script) error {
		created, err := store.CreateScriptIfAbsent(script)
		if err == nil && !created {
			err = fmt.Errorf("script %s was not created", script.ID)
		}
		return err
	}
	for index, tc := range []struct {
		name      string
		createdAt time.Time
		create    func(*dsl.Script) error
	}{
		{"save_stamped", time.Time{}, store.SaveScript},
		{"create_if_absent_stamped", time.Time{}, createIfAbsent},
		{"create_if_absent_supplied", nanosecondCreatedAt, createIfAbsent},
		{"authoring_create_supplied", nanosecondCreatedAt, func(script *dsl.Script) error {
			result, err := store.CreateScriptAuthoringIfAbsent(script)
			if err == nil && (result == nil || !result.Applied) {
				err = fmt.Errorf("script %s was not created", script.ID)
			}
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := &dsl.Script{
				ID: fmt.Sprintf("precision-script-%d", index), RefKey: fmt.Sprintf("DEV-SCR-%d-v1", 900+index),
				Name: tc.name, Protocol: "http", Version: "1", ProjectID: DefaultProjectID, CreatedAt: tc.createdAt,
			}
			if err := tc.create(script); err != nil {
				t.Fatal(err)
			}
			persisted, err := store.GetScript(script.ID)
			if err != nil {
				t.Fatal(err)
			}
			requireStoredTimestamp(t, "created_at", script.CreatedAt, persisted.CreatedAt)
			if ScriptIncarnation(script.ID, script.ProjectID, script.RefKey, script.CreatedAt) !=
				ScriptIncarnation(persisted.ID, persisted.ProjectID, persisted.RefKey, persisted.CreatedAt) {
				t.Error("the created copy names a different script incarnation than the stored row")
			}
			snapshot, err := store.GetScriptAuthoringSnapshot(script.ID)
			if err != nil {
				t.Fatal(err)
			}
			edited := *script
			edited.Name += " edited"
			result, err := store.UpdateScriptAuthoringIfCurrent(&edited, ScriptAuthoringPrecondition{
				ProjectID: script.ProjectID, ContentRevision: snapshot.ContentRevision, CreatedAt: script.CreatedAt,
			})
			if err != nil || result == nil || !result.Applied {
				t.Fatalf("edit conditioned on the created copy = %+v, %v", result, err)
			}
		})
	}
}

func exerciseScenarioCreatesKeepStoredCreatedAt(t *testing.T, store *SQLiteStore) {
	if err := store.SaveScript(&dsl.Script{ID: "precision-scenario-script", Name: "Script", Protocol: "http", ProjectID: DefaultProjectID}); err != nil {
		t.Fatal(err)
	}
	for index, tc := range []struct {
		name      string
		createdAt time.Time
		create    func(*Scenario, []ScenarioScript) error
	}{
		{"save_stamped", time.Time{}, store.SaveScenario},
		{"create_if_absent_supplied", nanosecondCreatedAt, func(scenario *Scenario, scripts []ScenarioScript) error {
			created, err := store.CreateScenarioWithDataPoolBindingsIfAbsent(scenario, scripts, nil)
			if err == nil && !created {
				err = fmt.Errorf("scenario %s was not created", scenario.ID)
			}
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scenario := &Scenario{
				ID: fmt.Sprintf("precision-scenario-%d", index), RefKey: fmt.Sprintf("DEV-SCN-%d", 900+index),
				Name: tc.name, Duration: "1m", CreatedAt: tc.createdAt,
			}
			scripts := []ScenarioScript{{ScenarioID: scenario.ID, ScriptID: "precision-scenario-script", VUsers: 1}}
			if err := tc.create(scenario, scripts); err != nil {
				t.Fatal(err)
			}
			persisted, _, err := store.GetScenario(scenario.ID)
			if err != nil {
				t.Fatal(err)
			}
			requireStoredTimestamp(t, "created_at", scenario.CreatedAt, persisted.CreatedAt)
			expected := func() *ScenarioAuthoringPrecondition {
				snapshot, err := store.GetScenarioAuthoringSnapshot(scenario.ID)
				if err != nil {
					t.Fatal(err)
				}
				return &ScenarioAuthoringPrecondition{ProjectID: snapshot.ProjectID, ContentRevision: snapshot.ContentRevision, CreatedAt: scenario.CreatedAt}
			}
			if _, _, applied, err := store.UpdateScenarioDataPoolBindingsGuarded(scenario.ID, nil, scenario.UpdatedAt, ScenarioWriterGuard{Expected: expected()}); err != nil || !applied {
				t.Fatalf("guarded write conditioned on the created copy = %t, %v", applied, err)
			}
			edited := *scenario
			edited.Name += " edited"
			result, err := store.UpdateScenarioAuthoringIfCurrent(&edited, scripts, nil, *expected())
			if err != nil || result == nil || !result.Applied {
				t.Fatalf("edit conditioned on the created copy = %+v, %v", result, err)
			}
		})
	}
}

func exerciseBrunoImportKeepsStoredTimestamps(t *testing.T, store *SQLiteStore) {
	seedBrunoImportProject(t, store)
	first := brunoImportCommitFixture("precision-bruno", "project-bruno", "precision-revision-1", "precision-raw-1", "precision-sanitized-1", "sha-raw-precision-1", "sha-sanitized-precision-1")
	created, err := store.CommitBrunoImport(first)
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := store.GetScript("precision-bruno")
	if err != nil {
		t.Fatal(err)
	}
	requireStoredTimestamp(t, "created created_at", created.Script.CreatedAt, persisted.CreatedAt)
	requireStoredTimestamp(t, "created updated_at", created.Script.UpdatedAt, persisted.UpdatedAt)

	// A re-import names the version the first import returned.
	second := brunoImportCommitFixture("precision-bruno", "project-bruno", "precision-revision-2", "precision-raw-2", "precision-sanitized-2", "sha-raw-precision-2", "sha-sanitized-precision-2")
	second.ExpectedScriptUpdatedAt = &created.Script.UpdatedAt
	second.ExpectedActiveRevisionID = "precision-revision-1"
	second.Script.CreatedAt = created.Script.CreatedAt
	updated, err := store.CommitBrunoImport(second)
	if err != nil {
		t.Fatalf("re-import conditioned on the created version: %v", err)
	}
	persisted, err = store.GetScript("precision-bruno")
	if err != nil {
		t.Fatal(err)
	}
	requireStoredTimestamp(t, "updated updated_at", updated.Script.UpdatedAt, persisted.UpdatedAt)
}

func exerciseGitHubImportCreateKeepsStoredCreatedAt(t *testing.T, store *SQLiteStore) {
	requireProjectFixtures(t, store, "github-precision")
	script := &dsl.Script{
		ID: "github-precision", RefKey: "S-GH-PRECISION", Name: "Precise", Protocol: "http", Version: "1",
		ProjectID: "github-precision", CreatedAt: nanosecondCreatedAt, UpdatedAt: nanosecondCreatedAt,
	}
	link := &GitHubScriptLink{
		ScriptID: "github-precision", Owner: "octo", Repo: "scripts", Branch: "main", Path: "precision.json",
		LastCommitSHA: "1", RemoteBlobSHA: "1", RemoteCommitSHA: "commit-1", ContentHash: "hash-1",
	}
	if outcome, err := store.CommitGitHubScriptImport(script, link, time.Time{}); err != nil || outcome != GitHubScriptImportCreated {
		t.Fatalf("github import create = %q, %v", outcome, err)
	}
	persisted, err := store.GetScript("github-precision")
	if err != nil {
		t.Fatal(err)
	}
	requireStoredTimestamp(t, "created_at", script.CreatedAt, persisted.CreatedAt)
}

func exerciseExternalResultImportRollbackFence(t *testing.T, store *SQLiteStore) {
	for _, tc := range []struct {
		name      string
		createdAt time.Time
		create    func(*ExternalResultImport) error
	}{
		{"create_if_absent_supplied", nanosecondCreatedAt, func(result *ExternalResultImport) error {
			created, err := store.CreateExternalResultImportIfAbsent(result)
			if err == nil && !created {
				err = fmt.Errorf("external result import %s was not created", result.ID)
			}
			return err
		}},
		{"save_stamped", time.Time{}, store.SaveExternalResultImport},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := &ExternalResultImport{
				ID: "precision-" + tc.name, Provider: "apexload", Name: "precision evidence", ProjectID: DefaultProjectID,
				Source: "library://source/" + tc.name, Summary: map[string]any{"status": "passed"},
				RawResult: map[string]any{"content_hash": "sha256:" + tc.name}, CreatedAt: tc.createdAt,
			}
			if err := tc.create(result); err != nil {
				t.Fatal(err)
			}
			persisted, err := store.GetExternalResultImport(result.ID)
			if err != nil {
				t.Fatal(err)
			}
			requireStoredTimestamp(t, "created_at", result.CreatedAt, persisted.CreatedAt)
			// A failed import rolls back with the copy it created.
			if deleted, err := store.DeleteExternalResultImportIfUnchanged(result); err != nil || !deleted {
				t.Fatalf("rollback with the created copy = %t, %v", deleted, err)
			}
		})
	}
}

func exerciseRunArchiveCleanupMatchesPublishedRecord(t *testing.T, store *SQLiteStore) {
	requireProjectFixtures(t, store, "archive-precision")
	if err := store.SaveTest(&TestRecord{ID: "archive-precision-run", Name: "archive precision", Status: "COMPLETE", ProjectID: "archive-precision", StartTime: nanosecondCreatedAt}); err != nil {
		t.Fatal(err)
	}
	// Archive records carry the run's clock, and a controller without HA keeps
	// the record it published.
	published := &RunArchiveRecord{
		TestID: "archive-precision-run", ArchiveBackend: "filesystem", ArchivePath: "/archives/precision",
		ManifestPath: "/archives/precision/manifest.json", ManifestSHA256: "precision-sha",
		CreatedAt: nanosecondCreatedAt, CompletedAt: nanosecondCreatedAt.Add(time.Minute),
	}
	if err := store.SaveRunArchive(published); err != nil {
		t.Fatal(err)
	}
	created, err := store.SaveArchiveCleanupJobIfArchiveUnchanged(&ArchiveCleanupJob{TestID: published.TestID, ProjectID: "archive-precision", ArchiveRecord: *published})
	if err != nil || !created {
		t.Fatalf("cleanup intent for the published record = %t, %v", created, err)
	}
	if deleted, err := store.DeleteRunArchiveIfUnchanged(published); err != nil || !deleted {
		t.Fatalf("delete the published record = %t, %v", deleted, err)
	}
}
