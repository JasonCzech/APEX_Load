package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func newExternalResultProjectIsolationStore(t *testing.T) *storage.SQLiteStore {
	t.Helper()
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "external-result-isolation.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	const projectID = "external-result-project-b"
	if err := store.SaveProject(&storage.Project{ID: projectID, Name: projectID, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	if err := store.SaveApplication(&storage.Application{ID: "external-result-app-b", ProjectID: projectID, Name: "app", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveApplication: %v", err)
	}
	if err := store.SaveRelease(&storage.Release{ID: "external-result-release-b", ApplicationID: "external-result-app-b", ProjectID: projectID, Name: "release", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveRelease: %v", err)
	}
	if err := store.SaveScript(&dsl.Script{ID: "external-result-script-b", ProjectID: projectID, Name: "script", Protocol: "http", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveScript: %v", err)
	}
	if err := store.SaveScenario(&storage.Scenario{ID: "external-result-scenario-b", Name: "scenario", Duration: "1m", CreatedAt: now, UpdatedAt: now}, []storage.ScenarioScript{{ScenarioID: "external-result-scenario-b", ScriptID: "external-result-script-b", VUsers: 1}}); err != nil {
		t.Fatalf("SaveScenario: %v", err)
	}
	return store
}

func TestResolveExternalResultImportRejectsCrossProjectParents(t *testing.T) {
	store := newExternalResultProjectIsolationStore(t)
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "release", body: `{"project_id":"unassigned","release_id":"external-result-release-b"}`},
		{name: "scenario", body: `{"project_id":"unassigned","scenario_id":"external-result-scenario-b"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/results/import", strings.NewReader(tc.body))
			projectID, status, message := resolveExternalResultImportProjectID(req, store)
			if projectID != "" || status != http.StatusBadRequest || !strings.Contains(message, "project") {
				t.Fatalf("resolve = project %q status %d message %q, want project mismatch", projectID, status, message)
			}
		})
	}
}

func TestExternalResultImportCannotEscapeAuthorizedProjectBinding(t *testing.T) {
	store := newExternalResultProjectIsolationStore(t)
	server := NewServer(ServerConfig{Store: store})
	body := bytes.NewBufferString(`{
		"provider":"k6",
		"project_id":"unassigned",
		"release_id":"external-result-release-b",
		"result":{"metrics":{"http_reqs":{"count":1}}}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/results/import", body)
	bindAuthorizedProjectToRequest(req, storage.DefaultProjectID)
	resp := httptest.NewRecorder()
	server.handleImportExternalResults(resp, req)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 body=%s", resp.Code, resp.Body.String())
	}
	imports, err := store.ListExternalResultImports(storage.ExternalResultImportFilter{ProjectID: "external-result-project-b"})
	if err != nil {
		t.Fatalf("ListExternalResultImports: %v", err)
	}
	if len(imports) != 0 {
		t.Fatalf("cross-project import was persisted: %#v", imports)
	}
}
