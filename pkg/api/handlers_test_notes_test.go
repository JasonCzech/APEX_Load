package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func newTestNotesAPIServer(t *testing.T) (*Server, *storage.SQLiteStore, string) {
	t.Helper()
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	testID := "notes-api-run"
	if err := store.SaveTest(&storage.TestRecord{
		ID:        testID,
		Name:      "Notes API Run",
		Status:    "COMPLETE",
		ProjectID: storage.DefaultProjectID,
		StartTime: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SaveTest returned error: %v", err)
	}
	server := NewServer(ServerConfig{Store: store})
	return server, store, testID
}

func TestTestNotesEndpointsRoundTrip(t *testing.T) {
	server, store, testID := newTestNotesAPIServer(t)

	createBody, _ := json.Marshal(testNoteRequest{Body: "# Observation\n\n- p95 spike during ramp"})
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/tests/"+testID+"/notes", bytes.NewReader(createBody))
	createResp := httptest.NewRecorder()
	server.mux.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("POST status = %d, want 201 (body=%s)", createResp.Code, createResp.Body.String())
	}
	var created storage.TestNote
	if err := json.Unmarshal(createResp.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created note: %v", err)
	}
	if created.ID == "" || created.TestID != testID || created.Body != "# Observation\n\n- p95 spike during ramp" {
		t.Fatalf("created note = %#v", created)
	}
	if created.CreatedAt.IsZero() || !created.CreatedAt.Equal(created.UpdatedAt) {
		t.Fatalf("created timestamps = %s/%s, want equal non-zero", created.CreatedAt, created.UpdatedAt)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testID+"/notes", nil)
	listResp := httptest.NewRecorder()
	server.mux.ServeHTTP(listResp, listReq)
	if listResp.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200 (body=%s)", listResp.Code, listResp.Body.String())
	}
	var listed testNotesListResponse
	if err := json.Unmarshal(listResp.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if listed.TestID != testID || len(listed.Notes) != 1 || listed.Notes[0].ID != created.ID {
		t.Fatalf("listed = %#v", listed)
	}

	time.Sleep(2 * time.Millisecond)
	updateBody, _ := json.Marshal(testNoteRequest{Body: "Updated note body"})
	updateReq := httptest.NewRequest(http.MethodPatch, "/api/v1/tests/"+testID+"/notes/"+created.ID, bytes.NewReader(updateBody))
	updateResp := httptest.NewRecorder()
	server.mux.ServeHTTP(updateResp, updateReq)
	if updateResp.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d, want 200 (body=%s)", updateResp.Code, updateResp.Body.String())
	}
	var updated storage.TestNote
	if err := json.Unmarshal(updateResp.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode updated note: %v", err)
	}
	if updated.Body != "Updated note body" {
		t.Fatalf("updated body = %q", updated.Body)
	}
	if !updated.CreatedAt.Equal(created.CreatedAt) || !updated.UpdatedAt.After(created.UpdatedAt) {
		t.Fatalf("updated timestamps = created %s/%s updated %s/%s", created.CreatedAt, updated.CreatedAt, created.UpdatedAt, updated.UpdatedAt)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/tests/"+testID+"/notes/"+created.ID, nil)
	deleteResp := httptest.NewRecorder()
	server.mux.ServeHTTP(deleteResp, deleteReq)
	if deleteResp.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d, want 200 (body=%s)", deleteResp.Code, deleteResp.Body.String())
	}
	var deleted map[string]string
	if err := json.Unmarshal(deleteResp.Body.Bytes(), &deleted); err != nil {
		t.Fatalf("decode delete: %v", err)
	}
	if deleted["deleted"] != created.ID {
		t.Fatalf("deleted payload = %#v", deleted)
	}

	notes, err := store.ListTestNotes(testID)
	if err != nil {
		t.Fatalf("ListTestNotes after delete returned error: %v", err)
	}
	if len(notes) != 0 {
		t.Fatalf("notes after delete = %d, want 0", len(notes))
	}
	events, err := store.ListAuditEvents(storage.AuditEventFilter{ResourceType: "test", ResourceID: testID, Limit: 20})
	if err != nil {
		t.Fatalf("ListAuditEvents returned error: %v", err)
	}
	if !hasAuditAction(events, "test.notes.create") || !hasAuditAction(events, "test.notes.update") || !hasAuditAction(events, "test.notes.delete") {
		t.Fatalf("missing note audit events: %#v", events)
	}
}

func TestTestNotesValidationAndNotFound(t *testing.T) {
	server, _, testID := newTestNotesAPIServer(t)

	emptyBody, _ := json.Marshal(testNoteRequest{Body: "   \n\t"})
	emptyReq := httptest.NewRequest(http.MethodPost, "/api/v1/tests/"+testID+"/notes", bytes.NewReader(emptyBody))
	emptyResp := httptest.NewRecorder()
	server.mux.ServeHTTP(emptyResp, emptyReq)
	if emptyResp.Code != http.StatusBadRequest {
		t.Fatalf("empty POST status = %d, want 400", emptyResp.Code)
	}

	unknownTestReq := httptest.NewRequest(http.MethodGet, "/api/v1/tests/does-not-exist/notes", nil)
	unknownTestResp := httptest.NewRecorder()
	server.mux.ServeHTTP(unknownTestResp, unknownTestReq)
	if unknownTestResp.Code != http.StatusNotFound {
		t.Fatalf("unknown test GET status = %d, want 404", unknownTestResp.Code)
	}

	updateBody, _ := json.Marshal(testNoteRequest{Body: "updated"})
	unknownNotePatch := httptest.NewRequest(http.MethodPatch, "/api/v1/tests/"+testID+"/notes/missing", bytes.NewReader(updateBody))
	unknownNotePatchResp := httptest.NewRecorder()
	server.mux.ServeHTTP(unknownNotePatchResp, unknownNotePatch)
	if unknownNotePatchResp.Code != http.StatusNotFound {
		t.Fatalf("unknown note PATCH status = %d, want 404", unknownNotePatchResp.Code)
	}

	unknownNoteDelete := httptest.NewRequest(http.MethodDelete, "/api/v1/tests/"+testID+"/notes/missing", nil)
	unknownNoteDeleteResp := httptest.NewRecorder()
	server.mux.ServeHTTP(unknownNoteDeleteResp, unknownNoteDelete)
	if unknownNoteDeleteResp.Code != http.StatusNotFound {
		t.Fatalf("unknown note DELETE status = %d, want 404", unknownNoteDeleteResp.Code)
	}
}

func TestTestNotesNilStore(t *testing.T) {
	server := NewServer(ServerConfig{})
	testID := "nil-store-notes"
	server.runner.mu.Lock()
	server.runner.tests[testID] = &ManagedTest{ID: testID, Name: "Nil Store Notes", Status: "COMPLETE"}
	server.runner.mu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testID+"/notes", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil store GET status = %d, want 503", resp.Code)
	}
}

func TestTestNotesAuthRequirement(t *testing.T) {
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/tests/t1/notes", nil)
	getReq.SetPathValue("id", "t1")
	getRule, ok := accessRequirementForRequest(getReq)
	if !ok {
		t.Fatal("expected access requirement for GET notes")
	}
	if !containsRole(getRule.GlobalRoles, RoleViewer) {
		t.Fatalf("GET should allow viewer, got %v", getRule.GlobalRoles)
	}

	postReq := httptest.NewRequest(http.MethodPost, "/api/v1/tests/t1/notes", nil)
	postReq.SetPathValue("id", "t1")
	postRule, ok := accessRequirementForRequest(postReq)
	if !ok {
		t.Fatal("expected access requirement for POST notes")
	}
	if containsRole(postRule.GlobalRoles, RoleViewer) || postRule.MaxBodyBytes != 65536 {
		t.Fatalf("POST rule = %#v, want editor+ and 64KiB limit", postRule)
	}

	patchReq := httptest.NewRequest(http.MethodPatch, "/api/v1/tests/t1/notes/n1", nil)
	patchReq.SetPathValue("id", "t1")
	patchReq.SetPathValue("note_id", "n1")
	patchRule, ok := accessRequirementForRequest(patchReq)
	if !ok {
		t.Fatal("expected access requirement for PATCH notes")
	}
	if containsRole(patchRule.GlobalRoles, RoleViewer) || patchRule.MaxBodyBytes != 65536 {
		t.Fatalf("PATCH rule = %#v, want editor+ and 64KiB limit", patchRule)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/tests/t1/notes/n1", nil)
	deleteReq.SetPathValue("id", "t1")
	deleteReq.SetPathValue("note_id", "n1")
	deleteRule, ok := accessRequirementForRequest(deleteReq)
	if !ok {
		t.Fatal("expected access requirement for DELETE notes")
	}
	if containsRole(deleteRule.GlobalRoles, RoleViewer) || deleteRule.MaxBodyBytes != 0 {
		t.Fatalf("DELETE rule = %#v, want editor+ and no body limit", deleteRule)
	}
}

func hasAuditAction(events []*storage.AuditEvent, action string) bool {
	for _, event := range events {
		if event != nil && event.Action == action {
			return true
		}
	}
	return false
}
