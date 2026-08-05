package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestQueuedBrunoInspectCannotPersistAfterProjectDeletion(t *testing.T) {
	server, store, artifactDir := newBrunoImportAPITestServer(t)

	// Hold an earlier admitted mutation so the delete queues as a writer. Once
	// that writer is waiting, the Bruno request can finish body/project
	// resolution but must queue behind it at project-mutation admission.
	releaseEarlierMutation := server.projectMutations.acquire(brunoImportTestProject, false)
	defer releaseEarlierMutation()

	deleteResponse := httptest.NewRecorder()
	deleteDone := make(chan struct{})
	go func() {
		server.mux.ServeHTTP(deleteResponse, httptest.NewRequest(
			http.MethodDelete,
			"/api/v1/projects/"+brunoImportTestProject,
			nil,
		))
		close(deleteDone)
	}()
	waitForProjectMutationRefs(t, server, brunoImportTestProject, 2)

	payload, err := json.Marshal(brunoInspectJSONRequest{
		ProjectID: brunoImportTestProject,
		Filename:  "queued.yml",
		Content:   "info: {name: Queued, type: http}\nhttp: {method: GET, url: https://example.test}\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	inspectRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/scripts/import/bruno/inspect",
		bytes.NewReader(payload),
	)
	inspectRequest.Header.Set("Content-Type", "application/json")
	inspectResponse := httptest.NewRecorder()
	inspectDone := make(chan struct{})
	go func() {
		server.mux.ServeHTTP(inspectResponse, inspectRequest)
		close(inspectDone)
	}()
	waitForProjectMutationRefs(t, server, brunoImportTestProject, 3)

	releaseEarlierMutation()
	waitForResponse(t, deleteDone, "project deletion")
	waitForResponse(t, inspectDone, "queued Bruno inspection")

	if deleteResponse.Code != http.StatusOK {
		t.Fatalf("delete status = %d, want 200; body=%s", deleteResponse.Code, deleteResponse.Body.String())
	}
	if inspectResponse.Code != http.StatusNotFound {
		t.Fatalf("queued inspect status = %d, want 404; body=%s", inspectResponse.Code, inspectResponse.Body.String())
	}
	if _, err := store.GetProject(brunoImportTestProject); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleted project lookup error = %v, want sql.ErrNoRows", err)
	}

	server.brunoImportMu.Lock()
	brunoSessions := len(server.brunoImportSessions)
	server.brunoImportMu.Unlock()
	if brunoSessions != 0 {
		t.Fatalf("queued inspection persisted %d Bruno session(s) after deletion", brunoSessions)
	}
	server.sourceSecretMu.Lock()
	sourceSecretSessions := len(server.sourceSecretSessions)
	server.sourceSecretMu.Unlock()
	if sourceSecretSessions != 0 {
		t.Fatalf("queued inspection persisted %d source-secret session(s) after deletion", sourceSecretSessions)
	}
	assertDirectoryHasNoRegularFiles(t, artifactDir)
}

func waitForProjectMutationRefs(t *testing.T, server *Server, projectID string, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		server.projectMutations.mu.Lock()
		entry := server.projectMutations.entries[normalizeProjectID(projectID)]
		refs := 0
		if entry != nil {
			refs = entry.refs
		}
		server.projectMutations.mu.Unlock()
		if refs >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("project mutation references did not reach %d", want)
}

func waitForResponse(t *testing.T, done <-chan struct{}, operation string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", operation)
	}
}
