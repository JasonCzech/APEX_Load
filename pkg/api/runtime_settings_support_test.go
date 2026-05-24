package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestRuntimeSettingsSupportMatrixEndpointCoversW3ProtocolsAndPresets(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/runtime-settings/support-matrix", nil)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var payload RuntimeSettingsSupportResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if payload.SchemaVersion != runtimeSettingsSupportSchemaVersion {
		t.Fatalf("schema version = %q", payload.SchemaVersion)
	}
	for _, protocol := range []string{"browser", "vugen_web2", "trueclient2_web", "soap", "java_http", "websocket", "grpc", "jms-solace", "devweb", "har", "jmeter"} {
		if !containsString(payload.Protocols, protocol) {
			t.Fatalf("protocols = %#v, missing %s", payload.Protocols, protocol)
		}
	}
	for _, status := range []string{"executed", "partial", "compatibility_only", "non_applicable", "blocked", "planned"} {
		if !runtimeSettingsVocabularyContains(payload.StatusVocabulary, status) {
			t.Fatalf("status vocabulary missing %s", status)
		}
	}
	for _, preset := range []string{"ci_smoke", "release_gate", "migration_replay", "vugen_web2_migration_review", "browser_canary", "debug_trace", "soak_stress"} {
		if !runtimeSettingsPresetContains(payload.Presets, preset) {
			t.Fatalf("presets missing %s", preset)
		}
	}
	if !runtimeSettingsSupportEntryContains(payload.SupportMatrix, "browser_recorded_resources_metadata", "executed") {
		t.Fatalf("support matrix missing browser recorded resource metadata guard")
	}
	if !runtimeSettingsSupportEntryContains(payload.SupportMatrix, "devweb_runtime_mapping", "partial") {
		t.Fatalf("support matrix missing partial DevWeb runtime classification")
	}
	if !runtimeSettingsSupportEntryContains(payload.SupportMatrix, "vugen_web2_browser_device_emulation", "executed") {
		t.Fatalf("support matrix missing Web 2.0 browser device emulation runtime classification")
	}
	if !runtimeSettingsSupportEntryContains(payload.SupportMatrix, "vugen_web2_browser_replay_evidence", "executed") {
		t.Fatalf("support matrix missing Web 2.0 replay evidence classification")
	}
	if !runtimeSettingsSupportEntryContains(payload.SupportMatrix, "vugen_web2_http_conversion_readiness", "executed") {
		t.Fatalf("support matrix missing Web 2.0 HTTP conversion readiness classification")
	}
	if !runtimeSettingsDecisionContains(payload.Decisions, "streaming-media-runtime", "executed") {
		t.Fatalf("streaming/media decision = %#v, want executed", payload.Decisions)
	}
}

func TestRuntimeSettingsReportEndpointsExportScriptAndScenarioEvidence(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	script := &dsl.Script{
		ID:       "script-rts-report",
		Name:     "Streaming RTS",
		Protocol: "http",
		Version:  "1.0.0",
		Config: dsl.ScriptConfig{
			BaseURL: "https://example.test",
			Log:     dsl.LogConfig{Level: "extended"},
			WebRuntime: dsl.WebRuntimeConfig{
				Streaming: dsl.StreamingConfig{
					Mode:              "fixed",
					FixedBitrateKbps:  1200,
					BufferSizeSeconds: 15,
				},
			},
		},
		Actions: []dsl.Action{{Name: "Home", Type: "GET", Target: "/"}},
	}
	server.runner.StoreScript(script)
	scenario, err := server.runner.StoreScenario(CreateScenarioRequest{
		Name:     "RTS Scenario",
		Duration: "5m",
		Groups: []CreateGroupEntry{{
			Name:     "web",
			ScriptID: script.ID,
			VUsers:   2,
		}},
	})
	if err != nil {
		t.Fatalf("StoreScenario returned error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/"+script.ID+"/runtime-settings/report", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("script report status = %d body=%s", resp.Code, resp.Body.String())
	}
	var report RuntimeSettingsReport
	if err := json.Unmarshal(resp.Body.Bytes(), &report); err != nil {
		t.Fatalf("unmarshal script report: %v", err)
	}
	if report.Status != "ready" {
		t.Fatalf("script report status = %q, want ready", report.Status)
	}
	if report.Support.MatrixEndpoint != "/api/v1/runtime-settings/support-matrix" || len(report.Presets) == 0 {
		t.Fatalf("script report support/presets = %#v presets=%d", report.Support, len(report.Presets))
	}
	if !runtimeSettingsReportHasEntry(report, "streaming") {
		t.Fatalf("script report entries = %#v, want streaming", report.Entries)
	}
	if hasRuntimeSettingsWarning(report.Findings) {
		t.Fatalf("script findings = %#v, want no streaming warning", report.Findings)
	}

	csvReq := httptest.NewRequest(http.MethodGet, "/api/v1/scenarios/"+scenario.ID+"/runtime-settings/report?format=csv", nil)
	csvResp := httptest.NewRecorder()
	server.mux.ServeHTTP(csvResp, csvReq)
	if csvResp.Code != http.StatusOK {
		t.Fatalf("scenario csv status = %d body=%s", csvResp.Code, csvResp.Body.String())
	}
	if ct := csvResp.Header().Get("Content-Type"); !strings.Contains(ct, "text/csv") {
		t.Fatalf("content type = %q, want csv", ct)
	}
	body := csvResp.Body.String()
	if !strings.Contains(body, "streaming") || !strings.Contains(body, "executed") || !strings.Contains(body, "web") {
		t.Fatalf("scenario csv body missing runtime evidence:\n%s", body)
	}
}

func TestScenarioRuntimeSettingsValidationSurfacesCompatibilityAndBlockers(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	server.runner.StoreScript(&dsl.Script{
		ID:       "script-browser-recorded-missing",
		Name:     "Browser Recorded Missing Metadata",
		Protocol: "browser",
		Version:  "1.0.0",
		Config: dsl.ScriptConfig{
			BaseURL: "https://example.test",
			Browser: dsl.BrowserConfig{
				Engine:            "chromium",
				DownloadResources: "recorded",
			},
		},
		Actions: []dsl.Action{{Name: "Open", Type: "browser_navigate", Target: "/"}},
	})

	body, _ := json.Marshal(CreateScenarioRequest{
		Name:     "Blocked RTS",
		Duration: "1m",
		Groups: []CreateGroupEntry{{
			Name:     "browser",
			ScriptID: "script-browser-recorded-missing",
			VUsers:   1,
		}},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scenarios/validate-load-model", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("validation status = %d body=%s", resp.Code, resp.Body.String())
	}
	var report LoadModelValidationReport
	if err := json.Unmarshal(resp.Body.Bytes(), &report); err != nil {
		t.Fatalf("unmarshal validation report: %v", err)
	}
	if report.RuntimeSettingsReview == nil || report.RuntimeSettingsReview.Status != "blocked" {
		t.Fatalf("runtime settings review = %#v, want blocked", report.RuntimeSettingsReview)
	}
	assertLoadModelValidationItem(t, report.Items, "runtime_settings", "blocked")

	_, err := server.runner.CreateTest(CreateTestRequest{
		Name:     "Blocked RTS",
		Duration: "1m",
		Groups: []CreateGroupEntry{{
			Name:     "browser",
			ScriptID: "script-browser-recorded-missing",
			VUsers:   1,
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "runtime settings blocked") {
		t.Fatalf("CreateTest error = %v, want runtime settings blocked", err)
	}
}

func TestTrustedHeaderAuthRequiresViewerForRuntimeSettingsSupportMatrix(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	unauthorizedReq := httptest.NewRequest(http.MethodGet, "/api/v1/runtime-settings/support-matrix", nil)
	unauthorizedResp := httptest.NewRecorder()
	server.mux.ServeHTTP(unauthorizedResp, unauthorizedReq)
	if unauthorizedResp.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized support matrix status = %d, want %d body=%s", unauthorizedResp.Code, http.StatusUnauthorized, unauthorizedResp.Body.String())
	}

	viewerReq := httptest.NewRequest(http.MethodGet, "/api/v1/runtime-settings/support-matrix", nil)
	viewerReq.Header.Set("X-Auth-Request-User", "viewer-user")
	viewerReq.Header.Set("X-APEXLoad-Roles", RoleViewer)
	viewerResp := httptest.NewRecorder()
	server.mux.ServeHTTP(viewerResp, viewerReq)
	if viewerResp.Code != http.StatusOK {
		t.Fatalf("viewer support matrix status = %d, want %d body=%s", viewerResp.Code, http.StatusOK, viewerResp.Body.String())
	}
}

func runtimeSettingsVocabularyContains(vocab []RuntimeSettingsStatusVocab, status string) bool {
	for _, entry := range vocab {
		if entry.Status == status {
			return true
		}
	}
	return false
}

func runtimeSettingsPresetContains(presets []RuntimeSettingsProfilePreset, id string) bool {
	for _, preset := range presets {
		if preset.ID == id {
			return true
		}
	}
	return false
}

func runtimeSettingsSupportEntryContains(entries []RuntimeSettingsSupportEntry, id string, status string) bool {
	for _, entry := range entries {
		if entry.ID == id && entry.Status == status {
			return true
		}
	}
	return false
}

func runtimeSettingsDecisionContains(decisions []RuntimeSettingsDecision, id string, decision string) bool {
	for _, item := range decisions {
		if item.ID == id && item.Decision == decision {
			return true
		}
	}
	return false
}

func runtimeSettingsReportHasEntry(report RuntimeSettingsReport, id string) bool {
	for _, entry := range report.Entries {
		if entry.ID == id {
			return true
		}
	}
	return false
}
