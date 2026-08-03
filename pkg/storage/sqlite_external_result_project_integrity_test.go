package storage

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func newExternalResultPlacementStore(t *testing.T) *SQLiteStore {
	t.Helper()
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "external-result-placement.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	for _, suffix := range []string{"a", "b"} {
		projectID := "external-result-project-" + suffix
		if err := store.SaveProject(&Project{ID: projectID, Name: projectID, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatalf("SaveProject(%s): %v", suffix, err)
		}
		applicationID := "external-result-application-" + suffix
		if err := store.SaveApplication(&Application{ID: applicationID, ProjectID: projectID, Name: applicationID, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatalf("SaveApplication(%s): %v", suffix, err)
		}
		releaseID := "external-result-release-" + suffix
		if err := store.SaveRelease(&Release{ID: releaseID, ApplicationID: applicationID, ProjectID: projectID, Name: releaseID, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatalf("SaveRelease(%s): %v", suffix, err)
		}
		scriptID := "external-result-script-" + suffix
		if err := store.SaveScript(&dsl.Script{ID: scriptID, ProjectID: projectID, Name: scriptID, Protocol: "http", CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatalf("SaveScript(%s): %v", suffix, err)
		}
		scenarioID := "external-result-scenario-" + suffix
		if err := store.SaveScenario(&Scenario{ID: scenarioID, Name: scenarioID, Duration: "1m", CreatedAt: now, UpdatedAt: now}, []ScenarioScript{{ScenarioID: scenarioID, ScriptID: scriptID, VUsers: 1}}); err != nil {
			t.Fatalf("SaveScenario(%s): %v", suffix, err)
		}
	}
	return store
}

func externalResultForProject(id, suffix string) *ExternalResultImport {
	return &ExternalResultImport{
		ID:            id,
		Provider:      "k6",
		Name:          id,
		ProjectID:     "external-result-project-" + suffix,
		ApplicationID: "external-result-application-" + suffix,
		ReleaseID:     "external-result-release-" + suffix,
		ScenarioID:    "external-result-scenario-" + suffix,
		Summary:       map[string]any{"status": "passed"},
		RawResult:     map[string]any{"source": suffix},
		CreatedAt:     time.Now().UTC(),
	}
}

func TestExternalResultWritesRejectMissingAndCrossProjectParents(t *testing.T) {
	writers := []struct {
		name  string
		write func(*SQLiteStore, *ExternalResultImport) error
	}{
		{name: "SaveExternalResultImport", write: func(store *SQLiteStore, result *ExternalResultImport) error {
			return store.SaveExternalResultImport(result)
		}},
		{name: "CreateExternalResultImportIfAbsent", write: func(store *SQLiteStore, result *ExternalResultImport) error {
			created, err := store.CreateExternalResultImportIfAbsent(result)
			if err == nil && !created {
				return errors.New("external result import was not created")
			}
			return err
		}},
	}
	cases := []struct {
		name       string
		mutate     func(*ExternalResultImport)
		wantErrSub string
	}{
		{name: "missing project", mutate: func(result *ExternalResultImport) { result.ProjectID = "missing-project" }, wantErrSub: "project not found"},
		{name: "missing application", mutate: func(result *ExternalResultImport) { result.ApplicationID = "missing-application" }, wantErrSub: "application not found"},
		{name: "missing release", mutate: func(result *ExternalResultImport) { result.ReleaseID = "missing-release" }, wantErrSub: "release not found"},
		{name: "missing scenario", mutate: func(result *ExternalResultImport) { result.ScenarioID = "missing-scenario" }, wantErrSub: "scenario not found"},
		{name: "foreign release", mutate: func(result *ExternalResultImport) { result.ReleaseID = "external-result-release-b" }, wantErrSub: "belongs to project"},
		{name: "foreign scenario", mutate: func(result *ExternalResultImport) { result.ScenarioID = "external-result-scenario-b" }, wantErrSub: "belongs to project"},
	}
	for _, writer := range writers {
		writer := writer
		for _, tc := range cases {
			tc := tc
			t.Run(writer.name+"/"+tc.name, func(t *testing.T) {
				store := newExternalResultPlacementStore(t)
				result := externalResultForProject("rejected-"+strings.ReplaceAll(tc.name, " ", "-"), "a")
				tc.mutate(result)
				if err := writer.write(store, result); err == nil || !strings.Contains(err.Error(), tc.wantErrSub) {
					t.Fatalf("write error = %v, want %q", err, tc.wantErrSub)
				}
				if _, err := store.GetExternalResultImport(result.ID); !errors.Is(err, sql.ErrNoRows) {
					t.Fatalf("rejected external result was persisted: %v", err)
				}
			})
		}
	}
}

func TestSaveExternalResultImportRejectsProjectRelocation(t *testing.T) {
	store := newExternalResultPlacementStore(t)
	original := externalResultForProject("immutable-external-result", "a")
	if err := store.SaveExternalResultImport(original); err != nil {
		t.Fatalf("SaveExternalResultImport(original): %v", err)
	}
	relocation := externalResultForProject(original.ID, "b")
	if err := store.SaveExternalResultImport(relocation); err == nil || !strings.Contains(err.Error(), "belongs to project") {
		t.Fatalf("SaveExternalResultImport(relocation) error = %v", err)
	}
	stored, err := store.GetExternalResultImport(original.ID)
	if err != nil {
		t.Fatalf("GetExternalResultImport: %v", err)
	}
	if stored.ProjectID != original.ProjectID || stored.ApplicationID != original.ApplicationID || stored.ReleaseID != original.ReleaseID || stored.ScenarioID != original.ScenarioID {
		t.Fatalf("relocation changed durable evidence: %#v", stored)
	}
}

func TestCreateExternalResultImportCollisionCannotRelocateProject(t *testing.T) {
	store := newExternalResultPlacementStore(t)
	original := externalResultForProject("immutable-create-external-result", "a")
	created, err := store.CreateExternalResultImportIfAbsent(original)
	if err != nil || !created {
		t.Fatalf("CreateExternalResultImportIfAbsent(original) = %t, %v", created, err)
	}
	relocation := externalResultForProject(original.ID, "b")
	created, err = store.CreateExternalResultImportIfAbsent(relocation)
	if err != nil || created {
		t.Fatalf("CreateExternalResultImportIfAbsent(relocation) = %t, %v", created, err)
	}
	stored, err := store.GetExternalResultImport(original.ID)
	if err != nil {
		t.Fatalf("GetExternalResultImport: %v", err)
	}
	if stored.ProjectID != original.ProjectID {
		t.Fatalf("collision moved external result to %q", stored.ProjectID)
	}
}
