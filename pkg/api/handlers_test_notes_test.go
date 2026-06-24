package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
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

func TestTestNotesTrimAndLengthValidation(t *testing.T) {
	server, _, testID := newTestNotesAPIServer(t)

	trimmedBody, _ := json.Marshal(testNoteRequest{Body: "  trimmed note  \n"})
	trimmedReq := httptest.NewRequest(http.MethodPost, "/api/v1/tests/"+testID+"/notes", bytes.NewReader(trimmedBody))
	trimmedResp := httptest.NewRecorder()
	server.mux.ServeHTTP(trimmedResp, trimmedReq)
	if trimmedResp.Code != http.StatusCreated {
		t.Fatalf("trimmed POST status = %d, want 201 (body=%s)", trimmedResp.Code, trimmedResp.Body.String())
	}
	var trimmed storage.TestNote
	if err := json.Unmarshal(trimmedResp.Body.Bytes(), &trimmed); err != nil {
		t.Fatalf("decode trimmed note: %v", err)
	}
	if trimmed.Body != "trimmed note" {
		t.Fatalf("trimmed body = %q, want %q", trimmed.Body, "trimmed note")
	}

	cjkBody, _ := json.Marshal(testNoteRequest{Body: strings.Repeat("中", 22000)})
	cjkReq := httptest.NewRequest(http.MethodPost, "/api/v1/tests/"+testID+"/notes", bytes.NewReader(cjkBody))
	cjkResp := httptest.NewRecorder()
	server.mux.ServeHTTP(cjkResp, cjkReq)
	if cjkResp.Code != http.StatusCreated {
		t.Fatalf("CJK POST status = %d, want 201 (body=%s)", cjkResp.Code, cjkResp.Body.String())
	}

	tooLongBody, _ := json.Marshal(testNoteRequest{Body: strings.Repeat("a", testNoteBodyMaxCharacters+1)})
	tooLongReq := httptest.NewRequest(http.MethodPost, "/api/v1/tests/"+testID+"/notes", bytes.NewReader(tooLongBody))
	tooLongResp := httptest.NewRecorder()
	server.mux.ServeHTTP(tooLongResp, tooLongReq)
	if tooLongResp.Code != http.StatusBadRequest {
		t.Fatalf("too-long POST status = %d, want 400 (body=%s)", tooLongResp.Code, tooLongResp.Body.String())
	}
	if !strings.Contains(tooLongResp.Body.String(), "max 64000 characters") {
		t.Fatalf("too-long response = %s, want friendly max characters error", tooLongResp.Body.String())
	}
}

func TestTestNotesRejectsCrossTestNoteMutation(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	for _, testID := range []string{"notes-run-a", "notes-run-b"} {
		if err := store.SaveTest(&storage.TestRecord{ID: testID, Name: testID, Status: "COMPLETE", ProjectID: storage.DefaultProjectID, StartTime: now}); err != nil {
			t.Fatalf("SaveTest(%s) returned error: %v", testID, err)
		}
	}
	if err := store.CreateTestNote(&storage.TestNote{ID: "note-a", TestID: "notes-run-a", Body: "owned by A"}); err != nil {
		t.Fatalf("CreateTestNote returned error: %v", err)
	}
	server := NewServer(ServerConfig{Store: store})

	updateBody, _ := json.Marshal(testNoteRequest{Body: "cross update"})
	patchReq := httptest.NewRequest(http.MethodPatch, "/api/v1/tests/notes-run-b/notes/note-a", bytes.NewReader(updateBody))
	patchResp := httptest.NewRecorder()
	server.mux.ServeHTTP(patchResp, patchReq)
	if patchResp.Code != http.StatusNotFound {
		t.Fatalf("cross-test PATCH status = %d, want 404 (body=%s)", patchResp.Code, patchResp.Body.String())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/tests/notes-run-b/notes/note-a", nil)
	deleteResp := httptest.NewRecorder()
	server.mux.ServeHTTP(deleteResp, deleteReq)
	if deleteResp.Code != http.StatusNotFound {
		t.Fatalf("cross-test DELETE status = %d, want 404 (body=%s)", deleteResp.Code, deleteResp.Body.String())
	}

	note, err := store.GetTestNote("note-a")
	if err != nil || note.Body != "owned by A" {
		t.Fatalf("cross-test mutation changed note: note=%#v err=%v", note, err)
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
	if containsRole(postRule.GlobalRoles, RoleViewer) || postRule.MaxBodyBytes != 262144 {
		t.Fatalf("POST rule = %#v, want editor+ and 256KiB limit", postRule)
	}

	patchReq := httptest.NewRequest(http.MethodPatch, "/api/v1/tests/t1/notes/n1", nil)
	patchReq.SetPathValue("id", "t1")
	patchReq.SetPathValue("note_id", "n1")
	patchRule, ok := accessRequirementForRequest(patchReq)
	if !ok {
		t.Fatal("expected access requirement for PATCH notes")
	}
	if containsRole(patchRule.GlobalRoles, RoleViewer) || patchRule.MaxBodyBytes != 262144 {
		t.Fatalf("PATCH rule = %#v, want editor+ and 256KiB limit", patchRule)
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

func TestTestNotesTrustedHeaderViewerCannotWriteAndEditorAttribution(t *testing.T) {
	server, store, testID := newTestNotesAPIServer(t)
	enableTrustedHeaderAuth(t, server)

	viewerGet := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testID+"/notes", nil)
	viewerGet.Header.Set("X-Auth-Request-User", "viewer-user")
	viewerGet.Header.Set("X-APEXLoad-Project-Roles", storage.DefaultProjectID+"="+RoleViewer)
	viewerGetResp := httptest.NewRecorder()
	server.mux.ServeHTTP(viewerGetResp, viewerGet)
	if viewerGetResp.Code != http.StatusOK {
		t.Fatalf("viewer GET status = %d, want 200 (body=%s)", viewerGetResp.Code, viewerGetResp.Body.String())
	}

	viewerBody, _ := json.Marshal(testNoteRequest{Body: "viewer write"})
	viewerPost := httptest.NewRequest(http.MethodPost, "/api/v1/tests/"+testID+"/notes", bytes.NewReader(viewerBody))
	viewerPost.Header.Set("X-Auth-Request-User", "viewer-user")
	viewerPost.Header.Set("X-APEXLoad-Project-Roles", storage.DefaultProjectID+"="+RoleViewer)
	viewerPostResp := httptest.NewRecorder()
	server.mux.ServeHTTP(viewerPostResp, viewerPost)
	if viewerPostResp.Code != http.StatusForbidden {
		t.Fatalf("viewer POST status = %d, want 403 (body=%s)", viewerPostResp.Code, viewerPostResp.Body.String())
	}

	editorBody, _ := json.Marshal(testNoteRequest{Body: "editor note"})
	editorPost := httptest.NewRequest(http.MethodPost, "/api/v1/tests/"+testID+"/notes", bytes.NewReader(editorBody))
	editorPost.Header.Set("X-Auth-Request-User", "editor-user")
	editorPost.Header.Set("X-Auth-Request-Name", "Editor User")
	editorPost.Header.Set("X-APEXLoad-Project-Roles", storage.DefaultProjectID+"="+RoleEditor)
	editorPostResp := httptest.NewRecorder()
	server.mux.ServeHTTP(editorPostResp, editorPost)
	if editorPostResp.Code != http.StatusCreated {
		t.Fatalf("editor POST status = %d, want 201 (body=%s)", editorPostResp.Code, editorPostResp.Body.String())
	}
	var created storage.TestNote
	if err := json.Unmarshal(editorPostResp.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode editor note: %v", err)
	}
	if created.CreatedBy != "Editor User" || created.UpdatedBy != "Editor User" {
		t.Fatalf("created attribution = created_by %q updated_by %q, want Editor User", created.CreatedBy, created.UpdatedBy)
	}
	persisted, err := store.GetTestNote(created.ID)
	if err != nil {
		t.Fatalf("GetTestNote returned error: %v", err)
	}
	if persisted.CreatedBy != "Editor User" || persisted.UpdatedBy != "Editor User" {
		t.Fatalf("persisted attribution = created_by %q updated_by %q, want Editor User", persisted.CreatedBy, persisted.UpdatedBy)
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
