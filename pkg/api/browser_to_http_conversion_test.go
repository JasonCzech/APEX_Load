package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestBrowserToHTTPNetworkEntriesUsesRetainedReplayEvidence(t *testing.T) {
	entry := func(path string) protocol.NetworkEntry {
		return protocol.NetworkEntry{URL: "https://shop.example.test/" + path, Method: http.MethodGet, ResourceType: "fetch"}
	}
	for _, test := range []struct {
		name    string
		session BrowserReplaySession
		paths   []string
	}{
		{
			name: "final report replaces stale progress and includes snapshots",
			session: BrowserReplaySession{
				Status: BrowserReplayCompleted,
				Events: []engine.ReplayActionEvent{{Network: []protocol.NetworkEntry{entry("stale")}}},
				FinalReport: &engine.ReplayReport{Success: true, Events: []engine.ReplayActionEvent{
					{Network: []protocol.NetworkEntry{entry("report")}},
					{Snapshot: &protocol.ErrorSnapshot{NetworkEntries: []protocol.NetworkEntry{entry("snapshot")}}},
				}},
			},
			paths: []string{"report", "snapshot"},
		},
		{
			name: "live progress remains usable",
			session: BrowserReplaySession{
				Status: BrowserReplayRunning,
				Events: []engine.ReplayActionEvent{{Network: []protocol.NetworkEntry{entry("live")}}},
			},
			paths: []string{"live"},
		},
		{
			name: "legacy terminal snapshot retains its event list",
			session: BrowserReplaySession{
				Status:      BrowserReplayCompleted,
				Events:      []engine.ReplayActionEvent{{Network: []protocol.NetworkEntry{entry("legacy")}}},
				FinalReport: &engine.ReplayReport{Success: true},
			},
			paths: []string{"legacy"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			entries, skipped, evidence, err := (&Server{}).browserToHTTPNetworkEntries(BrowserToHTTPPreviewRequest{ReplaySessionID: "retained"}, &test.session)
			if err != nil || skipped != 0 || len(entries) != len(test.paths) || evidence.NetworkEntries != len(test.paths) || !evidence.AllEntriesBound {
				t.Fatalf("entries=%#v skipped=%d evidence=%#v err=%v", entries, skipped, evidence, err)
			}
			for index, path := range test.paths {
				if entries[index].URL != entry(path).URL {
					t.Fatalf("entry %d=%s, want %s", index, entries[index].URL, entry(path).URL)
				}
			}
		})
	}
}

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
			Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Source: "enterprise_script_archive", Preserved: map[string]any{"source_profile": "web2_browser_profile browser profile"}}},
		},
		NetworkEntries: []protocol.NetworkEntry{
			{URL: "https://shop.example.test/api/cart?customer_id=123&csrf_token=abcdef0123456789abcdef0123456789", Method: "GET", StatusCode: 200, ResourceType: "fetch", DurationMs: 12},
			{URL: "https://shop.example.test/assets/app.js", Method: "GET", StatusCode: 200, ResourceType: "script", DurationMs: 4},
			{
				URL:            "https://shop.example.test/api/orders?order_id=987",
				Method:         "POST",
				StatusCode:     201,
				ResourceType:   "xhr",
				DurationMs:     31,
				RequestHeaders: map[string]string{"Content-Type": "application/json"},
				RequestBody:    `{"sku":"sku-123"}`,
			},
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
	if strings.Contains(resp.Body.String(), "abcdef0123456789abcdef0123456789") {
		t.Fatalf("preview response disclosed the captured CSRF literal: %s", resp.Body.String())
	}
	if got := payload.GeneratedScript.Actions[0].Target; !strings.Contains(got, "csrf_token={{secret.csrf_token}}") {
		t.Fatalf("generated target = %q, want governed CSRF secret placeholder", got)
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
	post := payload.GeneratedScript.Actions[1]
	if post.Body != `{"sku":"sku-123"}` || post.Headers["Content-Type"] != "application/json" {
		t.Fatalf("generated POST action = %#v, want captured body and content type", post)
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
	if strings.Contains(resp.Body.String(), "abcdef0123456789abcdef0123456789") {
		t.Fatalf("response disclosed the correlation literal: %s", resp.Body.String())
	}
	if payload.Governance.Status != "blocked" || !strings.Contains(resp.Body.String(), "Correlation candidates require review") {
		t.Fatalf("governance/body = %#v/%s, want correlation review blocker", payload.Governance, resp.Body.String())
	}
	if payload.GeneratedScript.Metadata.Import.Preserved["browser_to_http_governance"] == nil || payload.GeneratedScript.Metadata.Import.Preserved["browser_to_http_diff"] == nil {
		t.Fatalf("generated metadata = %#v, want governance and diff preserved", payload.GeneratedScript.Metadata.Import.Preserved)
	}
}

func TestBrowserToHTTPDynamicBusinessIDIsOnlyACorrelationCandidate(t *testing.T) {
	const liveSessionID = "live-session-id-abcdef0123456789"
	entries := []protocol.NetworkEntry{{
		URL:    "https://shop.example.test/cart?session_id=" + liveSessionID,
		Method: http.MethodGet,
	}}
	correlations := browserToHTTPCorrelationCandidates(entries)
	if len(correlations) != 1 || correlations[0].Name != "session_id" {
		t.Fatalf("correlation candidates = %#v, want structural session_id", correlations)
	}
	if candidates := browserToHTTPDataSourceCandidates(entries); len(candidates) != 0 {
		t.Fatalf("dynamic session id leaked through data-source candidates: %#v", candidates)
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
		Approval:            BrowserToHTTPApproval{Status: "approved", ApprovedBy: "reviewer@example.test"},
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
	if payload.Review.Evidence.ReplaySessionResolved || payload.Review.Evidence.Durable || payload.Review.Evidence.VerificationStatus != "unverified_replay_session_unresolved" {
		t.Fatalf("evidence = %#v, want explicitly unverified unresolved replay", payload.Review.Evidence)
	}
	if payload.Governance.Status != "approval_unverified" || payload.Governance.ApprovalStatus != "unverified_client_claim" || len(payload.Governance.Blockers) == 0 {
		t.Fatalf("governance = %#v, stale replay evidence must not be approval-eligible", payload.Governance)
	}
}

func TestHandlePreviewBrowserToHTTPRejectsCallerApprovalWithoutDurableCorrelationEvidence(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	const sessionID = "11111111-1111-4111-8111-111111111111"
	completeBrowserReplayFixture(t, server, sessionID, true, []protocol.NetworkEntry{{
		URL: "https://shop.example.test/api/cart", Method: http.MethodGet, StatusCode: http.StatusOK, ResourceType: "fetch",
	}})

	body, err := json.Marshal(BrowserToHTTPPreviewRequest{
		ReplaySessionID: sessionID,
		CorrelationObservations: []BrowserToHTTPCorrelationObservation{{
			ID: 1,
		}},
		Approval: BrowserToHTTPApproval{Status: "approved", ApprovedBy: "reviewer@example.test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/browser-to-http/preview", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
	}
	var payload BrowserToHTTPPreviewResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.Review.Evidence.ReplaySessionResolved || !payload.Review.Evidence.ReplaySessionSuccessful || !payload.Review.Evidence.NetworkDurable || payload.Review.Evidence.CorrelationDurable || payload.Review.Evidence.Durable || payload.Review.Evidence.ReplayNetworkEntries != 1 || payload.Review.Evidence.VerificationStatus != "unverified_correlation_evidence" {
		t.Fatalf("evidence = %#v, want durable network but unverified uploaded correlation evidence", payload.Review.Evidence)
	}
	if payload.Governance.Status != "approval_unverified" || !payload.Governance.ApprovalRequired || payload.Governance.ApprovalStatus != "unverified_client_claim" || payload.Governance.ApprovedBy != "" {
		t.Fatalf("governance = %#v, caller approval identity must not be accepted", payload.Governance)
	}
}

func TestHandlePreviewBrowserToHTTPFailedReplayIsNotDurable(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	const sessionID = "22222222-2222-4222-8222-222222222222"
	completeBrowserReplayFixture(t, server, sessionID, false, []protocol.NetworkEntry{{
		URL: "https://shop.example.test/api/cart", Method: http.MethodGet, StatusCode: http.StatusInternalServerError, ResourceType: "fetch",
	}})

	body, err := json.Marshal(BrowserToHTTPPreviewRequest{ReplaySessionID: sessionID})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/browser-to-http/preview", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
	}
	var payload BrowserToHTTPPreviewResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Review.Evidence.ReplaySessionSuccessful || payload.Review.Evidence.NetworkDurable || payload.Review.Evidence.Durable || payload.Review.Evidence.VerificationStatus != "unverified_replay_session_incomplete_or_failed" {
		t.Fatalf("evidence = %#v, failed replay must not be durable", payload.Review.Evidence)
	}
	if !strings.Contains(resp.Body.String(), "not a completed successful replay") {
		t.Fatalf("body = %s, want failed replay blocker", resp.Body.String())
	}
}

func TestHandlePreviewBrowserToHTTPPostedEntryCannotShadowReplayResponse(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	const sessionID = "33333333-3333-4333-8333-333333333333"
	canonical := protocol.NetworkEntry{
		URL:             "https://shop.example.test/api/orders",
		Method:          http.MethodPost,
		StatusCode:      http.StatusCreated,
		ResourceType:    "xhr",
		RequestHeaders:  map[string]string{"Content-Type": "application/json"},
		RequestBody:     `{"sku":"one"}`,
		ResponseHeaders: map[string]string{"X-Attested": "true"},
	}
	completeBrowserReplayFixture(t, server, sessionID, true, []protocol.NetworkEntry{canonical})
	forged := canonical
	forged.StatusCode = http.StatusInternalServerError
	forged.ResponseHeaders = map[string]string{"X-Attested": "false", "X-Forged": "true"}
	body, err := json.Marshal(BrowserToHTTPPreviewRequest{
		ReplaySessionID: sessionID,
		NetworkEntries:  []protocol.NetworkEntry{forged},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/browser-to-http/preview", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
	}
	var payload BrowserToHTTPPreviewResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Review.CapturedRequests != 1 || !payload.Review.Evidence.NetworkDurable {
		t.Fatalf("review = %#v, forged duplicate must be discarded in favor of one canonical request", payload.Review)
	}
	if payload.GeneratedScript == nil || len(payload.GeneratedScript.Actions) != 1 || len(payload.GeneratedScript.Actions[0].Assertions) == 0 || payload.GeneratedScript.Actions[0].Assertions[0].Type != "status_code" || payload.GeneratedScript.Actions[0].Assertions[0].Expected != float64(http.StatusCreated) {
		t.Fatalf("generated action = %#v, want canonical 201 assertion", payload.GeneratedScript)
	}
}

// A correlation work limit must not kill the conversion preview: the draft is
// still generated, correlation hints are skipped, and the degradation is
// reported as a preview warning. (Duplicate observation IDs are one of the
// residual hard work-limit rejections after the engine-level degrade changes.)
func TestHandlePreviewBrowserToHTTPDegradesCorrelationWorkLimitToWarning(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	body, err := json.Marshal(BrowserToHTTPPreviewRequest{
		NetworkEntries: []protocol.NetworkEntry{{URL: "https://shop.example.test/api/cart", Method: http.MethodGet}},
		CorrelationObservations: []BrowserToHTTPCorrelationObservation{
			{ID: 1, ResponseBody: `{"csrf_token":"token-value-123456"}`},
			{ID: 1, ResponseBody: `{"csrf_token":"token-value-654321"}`},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/browser-to-http/preview", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want degraded 200 body=%s", resp.Code, resp.Body.String())
	}
	var payload BrowserToHTTPPreviewResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.GeneratedScript == nil {
		t.Fatal("degraded preview must still generate the HTTP draft")
	}
	found := false
	for _, warning := range payload.Warnings {
		if strings.Contains(warning, "correlation candidate analysis was skipped") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("warnings = %#v, want the correlation degradation warning", payload.Warnings)
	}
}
