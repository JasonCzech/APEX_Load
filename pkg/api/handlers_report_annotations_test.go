package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

func newReportAnnotationsTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	server := NewServer(ServerConfig{Store: store})
	testID := "report-annotations-test"
	server.runner.mu.Lock()
	server.runner.tests[testID] = &ManagedTest{ID: testID, Name: "Annotations Run", Status: "COMPLETE"}
	server.runner.mu.Unlock()
	return server, testID
}

func TestReportAnnotationsEndpointsRoundTrip(t *testing.T) {
	server, testID := newReportAnnotationsTestServer(t)

	// GET returns generated defaults plus the section descriptors.
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testID+"/report/annotations?profile=executive", nil)
	getResp := httptest.NewRecorder()
	server.mux.ServeHTTP(getResp, getReq)
	if getResp.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200 (body=%s)", getResp.Code, getResp.Body.String())
	}
	var initial reportAnnotationsResponse
	if err := json.Unmarshal(getResp.Body.Bytes(), &initial); err != nil {
		t.Fatalf("decode GET: %v", err)
	}
	if len(initial.Sections) != len(reportCommentarySectionOrder()) {
		t.Fatalf("sections = %d, want %d", len(initial.Sections), len(reportCommentarySectionOrder()))
	}
	if initial.Annotations == nil || initial.Annotations.Title == "" {
		t.Fatalf("expected default annotations with a title, got %#v", initial.Annotations)
	}

	// PUT analyst edits.
	body, _ := json.Marshal(reportAnnotationsRequest{
		Title:              "Edited Title",
		PreparedBy:         "Jane Analyst",
		ExecutiveNarrative: "Edited narrative.",
		SectionComments:    map[string]string{"overview": "edited overview note"},
	})
	putReq := httptest.NewRequest(http.MethodPut, "/api/v1/tests/"+testID+"/report/annotations?profile=executive", bytes.NewReader(body))
	putResp := httptest.NewRecorder()
	server.mux.ServeHTTP(putResp, putReq)
	if putResp.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200 (body=%s)", putResp.Code, putResp.Body.String())
	}

	// GET reflects the saved edits.
	getReq2 := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testID+"/report/annotations?profile=executive", nil)
	getResp2 := httptest.NewRecorder()
	server.mux.ServeHTTP(getResp2, getReq2)
	var saved reportAnnotationsResponse
	if err := json.Unmarshal(getResp2.Body.Bytes(), &saved); err != nil {
		t.Fatalf("decode GET2: %v", err)
	}
	if saved.Annotations.Title != "Edited Title" {
		t.Fatalf("title = %q, want Edited Title", saved.Annotations.Title)
	}
	if saved.Annotations.ExecutiveNarrative != "Edited narrative." {
		t.Fatalf("narrative = %q, want edited", saved.Annotations.ExecutiveNarrative)
	}
	if saved.Annotations.SectionComments["overview"] != "edited overview note" {
		t.Fatalf("overview comment = %q, want edited", saved.Annotations.SectionComments["overview"])
	}
	if saved.Annotations.UpdatedBy == "" {
		t.Fatal("expected UpdatedBy to be recorded")
	}
}

func TestReportAnnotationsPutUnknownTest(t *testing.T) {
	server, _ := newReportAnnotationsTestServer(t)
	body, _ := json.Marshal(reportAnnotationsRequest{Title: "x"})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/tests/does-not-exist/report/annotations", bytes.NewReader(body))
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.Code)
	}
}

func TestReportAnnotationsAuthRequirement(t *testing.T) {
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/tests/t1/report/annotations", nil)
	getReq.SetPathValue("id", "t1")
	getRule, ok := accessRequirementForRequest(getReq)
	if !ok {
		t.Fatal("expected an access requirement for GET annotations")
	}
	if !containsRole(getRule.GlobalRoles, RoleViewer) {
		t.Fatalf("GET should allow viewer, got %v", getRule.GlobalRoles)
	}

	putReq := httptest.NewRequest(http.MethodPut, "/api/v1/tests/t1/report/annotations", nil)
	putReq.SetPathValue("id", "t1")
	putRule, ok := accessRequirementForRequest(putReq)
	if !ok {
		t.Fatal("expected an access requirement for PUT annotations")
	}
	if containsRole(putRule.GlobalRoles, RoleViewer) {
		t.Fatalf("PUT must not allow viewer, got %v", putRule.GlobalRoles)
	}
	if !containsRole(putRule.GlobalRoles, RoleEditor) {
		t.Fatalf("PUT should allow editor, got %v", putRule.GlobalRoles)
	}
}

func containsRole(roles []string, want string) bool {
	for _, role := range roles {
		if role == want {
			return true
		}
	}
	return false
}
