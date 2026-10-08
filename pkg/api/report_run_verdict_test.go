package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func putReportAnnotations(t *testing.T, server *Server, testID, profile string, req reportAnnotationsRequest) *httptest.ResponseRecorder {
	t.Helper()
	if req.SectionComments == nil {
		req.SectionComments = map[string]string{}
	}
	body, _ := json.Marshal(req)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, httptest.NewRequest(http.MethodPut, "/api/v1/tests/"+testID+"/report/annotations?profile="+profile, bytes.NewReader(body)))
	return resp
}

func getReportAnnotations(t *testing.T, server *Server, testID, profile string) *ReportAnnotations {
	t.Helper()
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testID+"/report/annotations?profile="+profile, nil))
	if resp.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d (body=%s)", profile, resp.Code, resp.Body.String())
	}
	var decoded reportAnnotationsResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode GET %s: %v", profile, err)
	}
	return decoded.Annotations
}

func TestAnalystVerdictIsSharedByEveryReportAudience(t *testing.T) {
	server, testID := newReportAnnotationsTestServer(t)

	if resp := putReportAnnotations(t, server, testID, "executive", reportAnnotationsRequest{Title: "Exec", VerdictOverride: "Pass", VerdictExplanation: "Signed off for release"}); resp.Code != http.StatusOK {
		t.Fatalf("executive PUT = %d (%s)", resp.Code, resp.Body.String())
	}
	for _, profile := range []string{"executive", "engineer", "audit"} {
		ann := getReportAnnotations(t, server, testID, profile)
		if ann.VerdictOverride != "Pass" || ann.VerdictExplanation != "Signed off for release" {
			t.Fatalf("%s verdict = %q/%q, want the executive Pass", profile, ann.VerdictOverride, ann.VerdictExplanation)
		}
		if ann.VerdictProfile != "executive" || ann.VerdictUpdatedAt == nil || ann.VerdictUpdatedBy == "" {
			t.Fatalf("%s verdict provenance = %q/%v/%q", profile, ann.VerdictProfile, ann.VerdictUpdatedAt, ann.VerdictUpdatedBy)
		}
	}

	// Saving engineer commentary with the loaded verdict leaves it untouched.
	engineer := getReportAnnotations(t, server, testID, "engineer")
	if resp := putReportAnnotations(t, server, testID, "engineer", reportAnnotationsRequest{
		Title: "Eng", ExecutiveNarrative: "engineer notes", VerdictOverride: engineer.VerdictOverride, VerdictExplanation: engineer.VerdictExplanation, ExpectedVerdictUpdatedAt: engineer.VerdictUpdatedAt,
	}); resp.Code != http.StatusOK {
		t.Fatalf("engineer PUT = %d (%s)", resp.Code, resp.Body.String())
	}
	if got := getReportAnnotations(t, server, testID, "audit"); got.VerdictProfile != "executive" {
		t.Fatalf("unchanged verdict was re-attributed to %q", got.VerdictProfile)
	}

	// A change from another audience replaces it everywhere.
	audit := getReportAnnotations(t, server, testID, "audit")
	if resp := putReportAnnotations(t, server, testID, "audit", reportAnnotationsRequest{Title: "Audit", VerdictOverride: "Fail", VerdictExplanation: "Evidence gap", ExpectedVerdictUpdatedAt: audit.VerdictUpdatedAt}); resp.Code != http.StatusOK {
		t.Fatalf("audit PUT = %d (%s)", resp.Code, resp.Body.String())
	}
	if got := getReportAnnotations(t, server, testID, "executive"); got.VerdictOverride != "Fail" || got.VerdictProfile != "audit" {
		t.Fatalf("executive verdict = %q from %q, want Fail from audit", got.VerdictOverride, got.VerdictProfile)
	}

	// A stale editor cannot silently revert the newer verdict.
	exec := getReportAnnotations(t, server, testID, "executive")
	stale := engineer.VerdictUpdatedAt
	if resp := putReportAnnotations(t, server, testID, "executive", reportAnnotationsRequest{ExpectedUpdatedAt: exec.UpdatedAt, Title: "Exec", VerdictOverride: "Pass", VerdictExplanation: "stale", ExpectedVerdictUpdatedAt: stale}); resp.Code != http.StatusConflict {
		t.Fatalf("stale verdict PUT = %d, want 409 (%s)", resp.Code, resp.Body.String())
	}

	// Clearing is a change like any other and applies to every audience.
	if resp := putReportAnnotations(t, server, testID, "executive", reportAnnotationsRequest{ExpectedUpdatedAt: exec.UpdatedAt, Title: "Exec", ExpectedVerdictUpdatedAt: exec.VerdictUpdatedAt}); resp.Code != http.StatusOK {
		t.Fatalf("clear PUT = %d (%s)", resp.Code, resp.Body.String())
	}
	if got := getReportAnnotations(t, server, testID, "engineer"); got.VerdictOverride != "" || got.VerdictExplanation != "" {
		t.Fatalf("cleared verdict still shown to engineer: %q/%q", got.VerdictOverride, got.VerdictExplanation)
	}
}

func TestAnalystVerdictFallsBackToLatestLegacyProfileVerdict(t *testing.T) {
	server, testID := newReportAnnotationsTestServer(t)
	older := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	for _, ann := range []*ReportAnnotations{
		{TestID: testID, Profile: "engineer", SectionComments: map[string]string{}, VerdictOverride: "Fail", VerdictExplanation: "old", UpdatedAt: older, UpdatedBy: "Eng"},
		{TestID: testID, Profile: "executive", SectionComments: map[string]string{}, VerdictOverride: "Pass", VerdictExplanation: "newest", UpdatedAt: older.Add(time.Hour), UpdatedBy: "Exec"},
		{TestID: testID, Profile: "audit", SectionComments: map[string]string{}, UpdatedAt: older.Add(2 * time.Hour), UpdatedBy: "Auditor"},
	} {
		if err := saveReportAnnotations(server.store, ann); err != nil {
			t.Fatal(err)
		}
	}
	got := getReportAnnotations(t, server, testID, "audit")
	if got.VerdictOverride != "Pass" || got.VerdictExplanation != "newest" || got.VerdictProfile != "executive" || got.VerdictAuthor() != "Exec" {
		t.Fatalf("legacy verdict = %+v, want the newest saved profile verdict", got)
	}
}

func TestEveryAudienceExportShowsTheAnalystVerdictOnce(t *testing.T) {
	report := samplePDFRunArchiveReport()
	for _, profileID := range []string{"executive", "engineer"} {
		annotations := defaultReportAnnotations(report, report.Manifest.TestID, profileID)
		annotations.VerdictOverride = "Pass"
		annotations.VerdictExplanation = "Release approved"
		annotations.VerdictUpdatedBy = "Release manager"
		html, err := renderRunArchiveHTMLReportWithAnnotations(report, profileID, annotations, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Count(string(html), "Analyst assessment: Pass"); got != 1 {
			t.Errorf("%s export shows the analyst verdict %d times, want once", profileID, got)
		}
		if !strings.Contains(string(html), "Release manager") {
			t.Errorf("%s export does not name the verdict author", profileID)
		}
	}
}
