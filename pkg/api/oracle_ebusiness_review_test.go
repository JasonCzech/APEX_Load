package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOracleEBusinessReviewDetectsOracleWebCorrelationAndDataFit(t *testing.T) {
	review := buildOracleEBusinessReview(oracleEBusinessReviewRequest{
		ProjectID: "project-oeb",
		Name:      "oracle-web",
		SourceText: `
			GET /OA_HTML/OA.jsp?OAFunc=XX_SEARCH&p_instance=123 HTTP/1.1
			Cookie: ICX_SESSION=abc; JSESSIONID=def
			username={{user}} password={{secret}} responsibility=Payables search=invoice
			<input name="_FORM" value="token">
		`,
	})

	if review.Package != "oracle_web" {
		t.Fatalf("package = %q, want oracle_web", review.Package)
	}
	if review.Status != "needs_correlation_review" {
		t.Fatalf("status = %q, want needs_correlation_review", review.Status)
	}
	if len(review.CorrelationCandidates) < 4 {
		t.Fatalf("correlation candidates = %#v, want Oracle-Web tokens", review.CorrelationCandidates)
	}
	if !oracleEBusinessCandidateContains(review.CorrelationCandidates, "ICX_SESSION") || !oracleEBusinessCandidateContains(review.CorrelationCandidates, "OAFunc") {
		t.Fatalf("candidates = %#v, want ICX_SESSION and OAFunc", review.CorrelationCandidates)
	}
	if len(review.DataSourceFindings) == 0 {
		t.Fatalf("data source findings = %#v, want parameter data fit findings", review.DataSourceFindings)
	}
	if review.ExecutionPosture.BlocksExecution {
		t.Fatalf("execution posture = %#v, Oracle-Web should be HTTP-backed", review.ExecutionPosture)
	}
}

func TestOracleEBusinessReviewBlocksOracleNCAExecution(t *testing.T) {
	review := buildOracleEBusinessReview(oracleEBusinessReviewRequest{
		SourceText: `nca_connect_server("forms.example.test"); nca_set_window("Navigator"); GET /forms/frmservlet?config=prod`,
	})

	if review.Package != "oracle_nca" {
		t.Fatalf("package = %q, want oracle_nca", review.Package)
	}
	if review.Status != "review_only" {
		t.Fatalf("status = %q, want review_only", review.Status)
	}
	if !review.ExecutionPosture.BlocksExecution || review.ExecutionPosture.RunnerRequirement != "oracle_forms_client_runner" {
		t.Fatalf("execution posture = %#v, want NCA external-runner block", review.ExecutionPosture)
	}
	if len(review.RepairSuggestions) == 0 {
		t.Fatalf("repair suggestions = %#v, want execution boundary suggestion", review.RepairSuggestions)
	}
}

func TestOracleEBusinessSupportMatrixEndpoint(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/import/oracle-ebusiness/support-matrix", nil)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var payload oracleEBusinessSupportResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.SupportMatrix) == 0 || len(payload.CorrelationPacks) < 3 || len(payload.ScenarioTemplates) == 0 {
		t.Fatalf("payload = %#v, want matrix, packs, and templates", payload)
	}
	if !payload.NCABoundary.BlocksExecution {
		t.Fatalf("nca boundary = %#v, want blocked native execution", payload.NCABoundary)
	}
}

func TestOracleEBusinessReviewEndpointAndRBAC(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)
	body := []byte(`{"project_id":"project-oeb","source_text":"SWEUserName=user&SWEPassword=secret&SWEC=1&SWEView=Account List View&SWERowId=1-ABC"}`)

	viewerReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/oracle-ebusiness/review", bytes.NewReader(body))
	viewerReq.Header.Set("Content-Type", "application/json")
	viewerReq.Header.Set("X-Auth-Request-User", "viewer")
	viewerReq.Header.Set("X-Stormrunner-Project-Roles", "project-oeb="+RoleViewer)
	viewerResp := httptest.NewRecorder()
	server.mux.ServeHTTP(viewerResp, viewerReq)
	if viewerResp.Code != http.StatusForbidden {
		t.Fatalf("viewer status = %d, want forbidden body=%s", viewerResp.Code, viewerResp.Body.String())
	}

	editorReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/oracle-ebusiness/review", bytes.NewReader(body))
	editorReq.Header.Set("Content-Type", "application/json")
	editorReq.Header.Set("X-Auth-Request-User", "editor")
	editorReq.Header.Set("X-Stormrunner-Roles", RoleEditor)
	editorResp := httptest.NewRecorder()
	server.mux.ServeHTTP(editorResp, editorReq)
	if editorResp.Code != http.StatusOK {
		t.Fatalf("editor status = %d, want ok body=%s", editorResp.Code, editorResp.Body.String())
	}
	var review oracleEBusinessReviewResponse
	if err := json.Unmarshal(editorResp.Body.Bytes(), &review); err != nil {
		t.Fatalf("decode review: %v", err)
	}
	if review.Package != "siebel_web" || !oracleEBusinessCandidateContains(review.CorrelationCandidates, "SWERowId") {
		t.Fatalf("review = %#v, want Siebel-Web row-id review", review)
	}
}

func oracleEBusinessCandidateContains(candidates []oracleEBusinessCorrelationCandidate, token string) bool {
	for _, candidate := range candidates {
		if candidate.Token == token {
			return true
		}
	}
	return false
}
