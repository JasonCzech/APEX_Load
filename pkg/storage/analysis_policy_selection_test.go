package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestAnalysisPolicySelectionSnapshotKeepsHistoricalRevisionAndLatestDeletion(t *testing.T) {
	store, _, _ := monitorFixture(t)
	checkAnalysisPolicySelectionSnapshot(t, store)
}

func checkAnalysisPolicySelectionSnapshot(t *testing.T, store *SQLiteStore) {
	t.Helper()
	in := AnalysisDefinition{ID: "chosen", ProjectID: "monitor-p", Kind: AnalysisDefinitionKindBrowserPolicy, Name: "Original", Definition: json.RawMessage(`{"version":1}`)}
	if _, err := store.SaveAnalysisDefinition(in, 0); err != nil {
		t.Fatal(err)
	}
	in.Name = "Edited"
	if _, err := store.SaveAnalysisDefinition(in, 1); err != nil {
		t.Fatal(err)
	}
	old, current, err := store.GetAnalysisPolicyRevision(in.ProjectID, in.ID, 1)
	if err != nil || old.Revision != 1 || old.Name != "Original" || current.Revision != 2 || current.Deleted {
		t.Fatal(old, current, err)
	}
	for _, tc := range []struct {
		project  string
		revision int64
	}{{"other", 1}, {in.ProjectID, 0}, {in.ProjectID, 99}} {
		if _, _, err := store.GetAnalysisPolicyRevision(tc.project, in.ID, tc.revision); !errors.Is(err, sql.ErrNoRows) {
			t.Fatal("foreign or missing revision accepted", err)
		}
	}
	in.Deleted = true
	if _, err := store.SaveAnalysisDefinition(in, 2); err != nil {
		t.Fatal(err)
	}
	old, current, err = store.GetAnalysisPolicyRevision(in.ProjectID, in.ID, 1)
	if err != nil || old.Deleted || old.Revision != 1 || !current.Deleted || current.Revision != 3 {
		t.Fatal(old, current, err)
	}
}

func TestRunAnalysisPolicySelectionsPersistAndUpgradeLegacyRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policies.db")
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	chosen := map[string]any{"browser": map[string]any{"id": "browser", "revision": float64(2)}}
	now := time.Now().UTC()
	if err := store.SaveTest(&TestRecord{ID: "selected", ProjectID: DefaultProjectID, Name: "Selected", Status: "QUEUED", StartTime: now, AnalysisPolicySelections: chosen}); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.GetTest("selected")
	if err != nil || !reflect.DeepEqual(loaded.AnalysisPolicySelections, chosen) {
		t.Fatal(loaded, err)
	}
	for _, statement := range []string{`ALTER TABLE tests DROP COLUMN analysis_policy_selections_json`, `DELETE FROM schema_migrations WHERE version>=73`} {
		if _, err := store.db.Exec(statement); err != nil {
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
	loaded, err = store.GetTest("selected")
	if err != nil || loaded.AnalysisPolicySelections != nil {
		t.Fatal("legacy row invented selection", loaded, err)
	}
	loaded.AnalysisPolicySelections = chosen
	if err := store.SaveTest(loaded); err != nil {
		t.Fatal(err)
	}
	loaded, err = store.GetTest("selected")
	if err != nil || !reflect.DeepEqual(loaded.AnalysisPolicySelections, chosen) {
		t.Fatal(loaded, err)
	}
}
