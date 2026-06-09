package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestHandlePreviewBrowserToHTTPGeneratesReviewDraftAndScenarioTemplate(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	body, _ := json.Marshal(BrowserToHTTPPreviewRequest{
		Name:      "Checkout HTTP Draft",
		ProjectID: "project-web2",
		SourceBrowserScript: &dsl.Script{
			ID:        "script-browser-web2",
			Name:      "Checkout Web2",
			ProjectID: "project-web2",
			Protocol:  "browser",
			Actions: []dsl.Action{
				{Name: "Open Cart", Type: "browser_navigate", Target: "https://shop.example.test/cart"},
				{Name: "Submit Order", Type: "browser_click", Target: "#submit", Selector: "#submit"},
			},
			Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Source: "loadrunner", Preserved: map[string]any{"source_profile": "vugen_web2 browser profile"}}},
		},
		NetworkEntries: []protocol.NetworkEntry{
			{URL: "https://shop.example.test/api/cart?customer_id=123&csrf_token=abcdef0123456789abcdef0123456789", Method: "GET", StatusCode: 200, ResourceType: "fetch", DurationMs: 12},
			{URL: "https://shop.example.test/assets/app.js", Method: "GET", StatusCode: 200, ResourceType: "script", DurationMs: 4},
			{URL: "https://shop.example.test/api/orders?order_id=987", Method: "POST", StatusCode: 201, ResourceType: "xhr", DurationMs: 31},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/browser-to-http/preview", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var payload BrowserToHTTPPreviewResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.SchemaVersion != browserToHTTPSchemaVersion {
		t.Fatalf("schema = %q, want %q", payload.SchemaVersion, browserToHTTPSchemaVersion)
	}
	if payload.GeneratedScript == nil || payload.GeneratedScript.Protocol != "http" {
		t.Fatalf("generated script = %#v, want HTTP script", payload.GeneratedScript)
	}
	if payload.GeneratedScript.ProjectID != "project-web2" || payload.GeneratedScript.GeneratedFrom != "browser_replay" {
		t.Fatalf("generated metadata = project %q from %q", payload.GeneratedScript.ProjectID, payload.GeneratedScript.GeneratedFrom)
	}
	if payload.Review.Status != "needs_repair" || payload.Review.CapturedRequests != 2 || payload.Review.GeneratedHTTPActions != 2 || payload.Review.SkippedStaticAssets != 1 {
		t.Fatalf("review = %#v, want two converted requests, one skipped static asset, needs_repair", payload.Review)
	}
	if len(payload.Review.CorrelationCandidates) == 0 || payload.Review.CorrelationCandidates[0].Name != "csrf_token" {
		t.Fatalf("correlation candidates = %#v, want csrf_token", payload.Review.CorrelationCandidates)
	}
	if len(payload.Review.DataSourceCandidates) < 2 {
		t.Fatalf("data-source candidates = %#v, want customer/order id candidates", payload.Review.DataSourceCandidates)
	}
	if len(payload.ScenarioTemplate.Groups) != 2 || payload.ScenarioTemplate.Groups[0].Protocol != "browser" || payload.ScenarioTemplate.Groups[1].Protocol != "http" {
		t.Fatalf("scenario template = %#v, want browser canary plus HTTP load groups", payload.ScenarioTemplate)
	}
	if payload.Governance.Status != "blocked" || !payload.Governance.ApprovalRequired || len(payload.Governance.Blockers) == 0 {
		t.Fatalf("governance = %#v, want blocked approval-required draft", payload.Governance)
	}
	if payload.Diff.EndpointCoveragePct != 100 || !containsString(payload.Diff.Hosts, "shop.example.test") || !containsString(payload.Diff.Methods, "POST") {
		t.Fatalf("diff = %#v, want full endpoint coverage for filtered requests", payload.Diff)
	}
	if !strings.Contains(resp.Body.String(), "conversion_requires_approval") {
		t.Fatalf("response missing approval metadata: %s", resp.Body.String())
	}
}

func TestHandlePreviewBrowserToHTTPRunsCorrelationEngineOnObservations(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	body, _ := json.Marshal(BrowserToHTTPPreviewRequest{
		Name:            "Observed HTTP Draft",
		ReplaySessionID: "replay-123",
		SourceBrowserScript: &dsl.Script{
			ID:       "browser-script",
			Name:     "Browser Checkout",
			Protocol: "browser",
			Actions:  []dsl.Action{{Name: "Checkout", Type: "browser_click", Selector: "#checkout"}},
		},
		NetworkEntries: []protocol.NetworkEntry{
			{URL: "https://shop.example.test/login", Method: "GET", StatusCode: 200, ResourceType: "document", DurationMs: 11},
			{URL: "https://shop.example.test/checkout", Method: "POST", StatusCode: 200, ResourceType: "xhr", DurationMs: 24},
		},
		CorrelationObservations: []BrowserToHTTPCorrelationObservation{
			{ID: 1, ResponseBody: `{"csrf_token":"abcdef0123456789abcdef0123456789"}`},
			{ID: 2, RequestBody: `csrf_token=abcdef0123456789abcdef0123456789`},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/browser-to-http/preview", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var payload BrowserToHTTPPreviewResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.Review.CorrelationEngine != "apexload-correlate" || len(payload.Review.CorrelationCandidates) == 0 {
		t.Fatalf("review = %#v, want correlation-engine candidates", payload.Review)
	}
	if payload.Review.CorrelationCandidates[0].Name != "corr_csrf_token" || !strings.Contains(payload.Review.CorrelationCandidates[0].Evidence, "confidence") {
		t.Fatalf("correlation candidates = %#v, want corr_csrf_token confidence evidence", payload.Review.CorrelationCandidates)
	}
	if payload.Governance.Status != "blocked" || !strings.Contains(resp.Body.String(), "Correlation candidates require review") {
		t.Fatalf("governance/body = %#v/%s, want correlation review blocker", payload.Governance, resp.Body.String())
	}
	if payload.GeneratedScript.Metadata.Import.Preserved["browser_to_http_governance"] == nil || payload.GeneratedScript.Metadata.Import.Preserved["browser_to_http_diff"] == nil {
		t.Fatalf("generated metadata = %#v, want governance and diff preserved", payload.GeneratedScript.Metadata.Import.Preserved)
	}
}

func TestHandlePreviewBrowserToHTTPRequiresNetworkEvidence(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	body, _ := json.Marshal(BrowserToHTTPPreviewRequest{SourceBrowserScript: &dsl.Script{Name: "No Replay", Protocol: "browser"}})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/browser-to-http/preview", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "requires replay network entries") {
		t.Fatalf("error body = %s, want replay network guidance", resp.Body.String())
	}
}

func TestHandlePreviewBrowserToHTTPUsesPostedNetworkEvidenceWhenSessionIDIsStale(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	body, _ := json.Marshal(BrowserToHTTPPreviewRequest{
		ReplaySessionID:     "stale-browser-replay",
		SourceBrowserScript: &dsl.Script{Name: "Checkout Web2", Protocol: "browser"},
		NetworkEntries: []protocol.NetworkEntry{
			{URL: "https://shop.example.test/api/cart", Method: "GET", StatusCode: 200, ResourceType: "fetch", DurationMs: 10},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/browser-to-http/preview", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var payload BrowserToHTTPPreviewResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.ReplaySessionID != "stale-browser-replay" || payload.Review.CapturedRequests != 1 {
		t.Fatalf("payload = %#v, want stale replay ID retained with posted request evidence", payload)
	}
}
