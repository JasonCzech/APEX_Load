package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	brunoconvert "github.com/jasonczech/apexload/pkg/convert/bruno"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestRetiredBrunoAPIKeepsDefaultNativeImport(t *testing.T) {
	server, _, _ := newBrunoImportAPITestServer(t)
	result := inspectBrunoJSON(t, server, []byte("info: {name: Health, type: http}\nhttp: {method: GET, url: https://example.test/health}\n"), "")
	if result.Result.OutputMode != brunoconvert.OutputModeNativeRequests || result.Result.Script != nil || len(result.Result.Scripts) != 1 {
		t.Fatalf("native inspection = %#v", result.Result)
	}
}

func TestRetiredBrunoAPIRejectsRunnerInspectOptions(t *testing.T) {
	server, _, _ := newBrunoImportAPITestServer(t)
	for name, payload := range map[string]brunoInspectJSONRequest{
		"canonical": {Options: brunoconvert.InspectOptions{OutputMode: "canonical"}},
		"sandbox":   {Sandbox: "safe"},
		"sync":      {SyncScriptID: "legacy-script"},
	} {
		t.Run(name, func(t *testing.T) {
			payload.ProjectID = brunoImportTestProject
			payload.Filename = "health.yml"
			payload.Content = "info: {name: Health, type: http}\nhttp: {method: GET, url: https://example.test/health}\n"
			raw, _ := json.Marshal(payload)
			request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/bruno/inspect", bytes.NewReader(raw))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			server.mux.ServeHTTP(response, request)
			if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "bruno_runner_retired") {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestRetiredBrunoCanonicalSessionCannotCommitAsNative(t *testing.T) {
	server, _, _ := newBrunoImportAPITestServer(t)
	inspection := inspectBrunoJSON(t, server, []byte("info: {name: Health, type: http}\nhttp: {method: GET, url: https://example.test/health}\n"), "")
	owner := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/bruno/commit", nil)
	session, err := server.loadBrunoImportSession(owner, inspection.ImportSessionID, brunoImportTestProject)
	if err != nil {
		t.Fatal(err)
	}
	session.ID = uuid.NewString()
	session.Result.OutputMode = "" // Older persisted sessions omitted this field.
	session.Result.Script = &dsl.Script{Protocol: "bruno"}
	if err := server.persistBrunoImportSession(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	folder := "nonexistent-folder"
	response := commitBrunoImportWithStatus(t, server, brunoCommitRequest{ProjectID: brunoImportTestProject, ImportSessionID: session.ID, Mode: "create_native_batch", FolderID: &folder})
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "bruno_runner_retired") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if _, err := server.loadBrunoImportSession(owner, session.ID, brunoImportTestProject); err != nil {
		t.Fatalf("rejected session was consumed: %v", err)
	}
}

func TestRetiredBrunoKeepsLegacySourceAndRejectsReactivation(t *testing.T) {
	server, store, _ := newBrunoImportAPITestServer(t)
	source := []byte("info: {name: Health, type: http}\nhttp: {method: GET, url: https://example.test/health}\n")
	inspection := inspectBrunoJSON(t, server, source, "")
	owner := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/bruno/commit", nil)
	session, err := server.loadBrunoImportSession(owner, inspection.ImportSessionID, brunoImportTestProject)
	if err != nil {
		t.Fatal(err)
	}
	const scriptID = "legacy-bruno-source"
	fixture := func(revisionID, secret string) *storage.BrunoImportCommit {
		return &storage.BrunoImportCommit{
			Script: &dsl.Script{
				ID: scriptID, Name: "Legacy collection", Protocol: "bruno", ProjectID: brunoImportTestProject,
				Config:  dsl.ScriptConfig{Bruno: dsl.BrunoConfig{SecretBindings: map[string]string{"TOKEN": secret}}},
				Actions: []dsl.Action{{Name: "Run collection", Type: "bruno_run"}},
			},
			RawArtifact: session.RawArtifact, SanitizedArtifact: session.SanitizedArtifact,
			Revision: storage.ImportSourceRevision{ID: revisionID, SourceFormat: "bruno", SanitizedAvailable: true},
		}
	}
	if _, err := store.CommitBrunoImport(fixture("old-revision", "old-private-value")); err != nil {
		t.Fatal(err)
	}
	previous, err := store.GetScript(scriptID)
	if err != nil {
		t.Fatal(err)
	}
	current := fixture("current-revision", "current-private-value")
	current.ExpectedScriptUpdatedAt = &previous.UpdatedAt
	current.ExpectedActiveRevisionID = "old-revision"
	committed, err := store.CommitBrunoImport(current)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(committed.Script)
	for _, route := range []string{"imports", "bruno-imports"} {
		t.Run(route, func(t *testing.T) {
			base := "/api/v1/scripts/" + scriptID + "/" + route
			list := httptest.NewRecorder()
			server.mux.ServeHTTP(list, httptest.NewRequest(http.MethodGet, base+"/revisions", nil))
			if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), "old-revision") || strings.Contains(list.Body.String(), "private-value") {
				t.Fatalf("retained revisions status=%d body=%s", list.Code, list.Body.String())
			}
			for _, raw := range []bool{false, true} {
				target := base + "/source"
				if raw {
					target += "?raw=true"
				}
				download := httptest.NewRecorder()
				server.mux.ServeHTTP(download, httptest.NewRequest(http.MethodGet, target, nil))
				if download.Code != http.StatusOK || download.Body.Len() == 0 {
					t.Fatalf("retained source raw=%v status=%d body=%s", raw, download.Code, download.Body.String())
				}
				if raw && !bytes.Equal(download.Body.Bytes(), source) {
					t.Fatal("retained raw source changed")
				}
			}
			rollback := httptest.NewRecorder()
			server.mux.ServeHTTP(rollback, httptest.NewRequest(http.MethodPost, base+"/rollback", strings.NewReader(`{"revision_id":"old-revision"}`)))
			if rollback.Code != http.StatusUnprocessableEntity || !strings.Contains(rollback.Body.String(), "bruno_runner_retired") {
				t.Fatalf("reactivation status=%d body=%s", rollback.Code, rollback.Body.String())
			}
		})
	}
	active, err := store.GetActiveImportSourceRevision(scriptID, brunoImportTestProject)
	if err != nil || active.ID != "current-revision" {
		t.Fatalf("active revision changed: %#v, %v", active, err)
	}
	stored, err := store.GetScript(scriptID)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(stored)
	if !bytes.Equal(before, after) {
		t.Fatal("rejected reactivation modified the durable script")
	}
}
