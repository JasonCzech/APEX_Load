package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
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
	if len(initial.Sections) != len(reportCommentarySectionOrder("executive")) {
		t.Fatalf("sections = %d, want %d", len(initial.Sections), len(reportCommentarySectionOrder("executive")))
	}
	if initial.Annotations == nil || initial.Annotations.Title == "" {
		t.Fatalf("expected default annotations with a title, got %#v", initial.Annotations)
	}
	if _, ok := initial.Annotations.SectionComments["decision"]; !ok {
		t.Fatalf("expected executive decision commentary default, got %#v", initial.Annotations.SectionComments)
	}

	auditReq := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testID+"/report/annotations?profile=audit", nil)
	auditResp := httptest.NewRecorder()
	server.mux.ServeHTTP(auditResp, auditReq)
	if auditResp.Code != http.StatusOK {
		t.Fatalf("audit GET status = %d, want 200 (body=%s)", auditResp.Code, auditResp.Body.String())
	}
	var auditInitial reportAnnotationsResponse
	if err := json.Unmarshal(auditResp.Body.Bytes(), &auditInitial); err != nil {
		t.Fatalf("decode audit GET: %v", err)
	}
	if len(auditInitial.Sections) != len(reportCommentarySectionOrder("audit")) {
		t.Fatalf("audit sections = %d, want %d", len(auditInitial.Sections), len(reportCommentarySectionOrder("audit")))
	}
	if _, ok := auditInitial.Annotations.SectionComments["audit_scope"]; !ok {
		t.Fatalf("expected audit commentary defaults, got %#v", auditInitial.Annotations.SectionComments)
	}

	// PUT analyst edits, including a verdict override and a curated finding.
	body, _ := json.Marshal(reportAnnotationsRequest{
		Title:              "Edited Title",
		PreparedBy:         "Jane Analyst",
		ExecutiveNarrative: "Edited narrative.",
		SectionComments:    map[string]string{"decision": "edited decision note"},
		VerdictOverride:    "Conditional Pass",
		VerdictExplanation: "Transient breach recovered without intervention.",
		Findings:           []ReportFinding{{Title: "Curated finding", Severity: "minor", Observed: "analyst observation"}},
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
	if saved.Annotations.SectionComments["decision"] != "edited decision note" {
		t.Fatalf("decision comment = %q, want edited", saved.Annotations.SectionComments["decision"])
	}
	if saved.Annotations.UpdatedBy == "" {
		t.Fatal("expected UpdatedBy to be recorded")
	}
	if saved.Annotations.VerdictOverride != "Conditional Pass" {
		t.Fatalf("verdict override = %q, want Conditional Pass", saved.Annotations.VerdictOverride)
	}
	if saved.Annotations.VerdictExplanation != "Transient breach recovered without intervention." {
		t.Fatalf("verdict explanation = %q, want analyst text", saved.Annotations.VerdictExplanation)
	}
	if len(saved.Annotations.Findings) != 1 {
		t.Fatalf("findings = %d, want 1 curated finding", len(saved.Annotations.Findings))
	}
	if got := saved.Annotations.Findings[0]; got.Title != "Curated finding" || got.ID != "F-01" {
		t.Fatalf("curated finding = %#v, want title 'Curated finding' with backfilled ID F-01", got)
	}
}

func TestReportAnnotationsRejectsStaleFullDocumentOverwrite(t *testing.T) {
	server, testID := newReportAnnotationsTestServer(t)
	path := "/api/v1/tests/" + testID + "/report/annotations?profile=executive"

	firstBody, _ := json.Marshal(reportAnnotationsRequest{
		Title:              "Initial",
		ExecutiveNarrative: "first narrative",
		SectionComments:    map[string]string{},
	})
	firstResp := httptest.NewRecorder()
	server.mux.ServeHTTP(firstResp, httptest.NewRequest(http.MethodPut, path, bytes.NewReader(firstBody)))
	if firstResp.Code != http.StatusOK {
		t.Fatalf("first PUT status = %d, body=%s", firstResp.Code, firstResp.Body.String())
	}
	var first reportAnnotationsResponse
	if err := json.Unmarshal(firstResp.Body.Bytes(), &first); err != nil {
		t.Fatalf("decode first PUT: %v", err)
	}

	secondBody, _ := json.Marshal(reportAnnotationsRequest{
		ExpectedUpdatedAt:  first.Annotations.UpdatedAt,
		Title:              "Composer update",
		ExecutiveNarrative: "newer composer narrative",
		SectionComments:    map[string]string{},
	})
	secondResp := httptest.NewRecorder()
	server.mux.ServeHTTP(secondResp, httptest.NewRequest(http.MethodPut, path, bytes.NewReader(secondBody)))
	if secondResp.Code != http.StatusOK {
		t.Fatalf("second PUT status = %d, body=%s", secondResp.Code, secondResp.Body.String())
	}

	staleVerdictBody, _ := json.Marshal(reportAnnotationsRequest{
		ExpectedUpdatedAt:  first.Annotations.UpdatedAt,
		Title:              "Initial",
		ExecutiveNarrative: "first narrative",
		SectionComments:    map[string]string{},
		VerdictOverride:    "Fail",
		VerdictExplanation: "stale verdict writer",
	})
	staleResp := httptest.NewRecorder()
	server.mux.ServeHTTP(staleResp, httptest.NewRequest(http.MethodPut, path, bytes.NewReader(staleVerdictBody)))
	if staleResp.Code != http.StatusConflict {
		t.Fatalf("stale PUT status = %d, want 409 (body=%s)", staleResp.Code, staleResp.Body.String())
	}

	getResp := httptest.NewRecorder()
	server.mux.ServeHTTP(getResp, httptest.NewRequest(http.MethodGet, path, nil))
	var current reportAnnotationsResponse
	if err := json.Unmarshal(getResp.Body.Bytes(), &current); err != nil {
		t.Fatalf("decode final GET: %v", err)
	}
	if current.Annotations.ExecutiveNarrative != "newer composer narrative" {
		t.Fatalf("narrative = %q, want newer composer narrative", current.Annotations.ExecutiveNarrative)
	}
	if current.Annotations.VerdictOverride != "" {
		t.Fatalf("stale verdict overwrote current annotations: %#v", current.Annotations)
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

func TestReportAnnotationsRejectsUnknownSectionsAndOversizedFindings(t *testing.T) {
	server, testID := newReportAnnotationsTestServer(t)
	path := "/api/v1/tests/" + testID + "/report/annotations?profile=executive"
	tests := []struct {
		name string
		req  reportAnnotationsRequest
	}{
		{name: "unknown section", req: reportAnnotationsRequest{SectionComments: map[string]string{"overview": "wrong profile"}}},
		{name: "too many findings", req: reportAnnotationsRequest{SectionComments: map[string]string{}, Findings: make([]ReportFinding, reportAnnotationsMaxFindings+1)}},
		{name: "oversized narrative", req: reportAnnotationsRequest{SectionComments: map[string]string{}, ExecutiveNarrative: strings.Repeat("x", reportAnnotationsMaxNarrative+1)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, err := json.Marshal(tt.req)
			if err != nil {
				t.Fatal(err)
			}
			resp := httptest.NewRecorder()
			server.mux.ServeHTTP(resp, httptest.NewRequest(http.MethodPut, path, bytes.NewReader(body)))
			if resp.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body=%s)", resp.Code, resp.Body.String())
			}
		})
	}
}

func TestReportAnnotationsRejectsNormalizedDocumentOverStoredSizeLimit(t *testing.T) {
	server, testID := newReportAnnotationsTestServer(t)
	findings := make([]ReportFinding, 50)
	for i := range findings {
		findings[i] = ReportFinding{
			Title:     "Finding",
			Observed:  strings.Repeat("o", 1400),
			Evidence:  strings.Repeat("e", 1400),
			RootCause: strings.Repeat("r", 1400),
			Impact:    strings.Repeat("i", 1400),
		}
	}
	body, err := json.Marshal(reportAnnotationsRequest{
		Title:           "Large but individually valid",
		SectionComments: map[string]string{},
		Findings:        findings,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(body) <= reportAnnotationsMaxStoredBytes || len(body) >= reportAnnotationsMaxRequestBytes {
		t.Fatalf("test payload size = %d, want between stored limit %d and request limit %d", len(body), reportAnnotationsMaxStoredBytes, reportAnnotationsMaxRequestBytes)
	}

	path := "/api/v1/tests/" + testID + "/report/annotations?profile=executive"
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, httptest.NewRequest(http.MethodPut, path, bytes.NewReader(body)))
	if resp.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 (body=%s)", resp.Code, resp.Body.String())
	}
}

func TestReportAnnotationsRejectsRequestBodyOverEnvelope(t *testing.T) {
	server, testID := newReportAnnotationsTestServer(t)
	body := []byte(`{"executive_narrative":"` + strings.Repeat("x", reportAnnotationsMaxRequestBytes) + `"}`)
	if len(body) <= reportAnnotationsMaxRequestBytes {
		t.Fatalf("test body size = %d, want over %d", len(body), reportAnnotationsMaxRequestBytes)
	}

	path := "/api/v1/tests/" + testID + "/report/annotations?profile=executive"
	req := httptest.NewRequest(http.MethodPut, path, bytes.NewReader(body))
	// Exercise MaxBytesReader rather than relying on a Content-Length shortcut.
	req.ContentLength = -1
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 (body=%s)", resp.Code, resp.Body.String())
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
