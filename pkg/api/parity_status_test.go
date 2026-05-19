package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandleGetParityStatusReturnsReleaseGateSummary(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/parity-status", nil)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var payload ParityStatusResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.Overall != "partial" {
		t.Fatalf("overall = %q, want partial", payload.Overall)
	}
	if len(payload.Areas) != 10 {
		t.Fatalf("area count = %d, want 10", len(payload.Areas))
	}
	if len(payload.CompetitiveParity) == 0 {
		t.Fatalf("competitive parity section is empty")
	}
	if len(payload.BrowserBenchmarks) != 3 {
		t.Fatalf("browser benchmark profiles = %d, want 3", len(payload.BrowserBenchmarks))
	}
	required := map[string]bool{
		"architecture":         false,
		"fleet":                false,
		"authoring":            false,
		"ai_assistance":        false,
		"load_modeling":        false,
		"browser_testing":      false,
		"vugen_web2_migration": false,
		"monitoring":           false,
		"reporting":            false,
		"scheduling":           false,
	}
	for _, area := range payload.Areas {
		if _, ok := required[area.ID]; ok {
			required[area.ID] = area.ReleaseGate && area.Status != "" && len(area.Evidence) > 0
		}
	}
	for id, ok := range required {
		if !ok {
			t.Fatalf("missing populated parity area %q in %#v", id, payload.Areas)
		}
	}
}

func TestHandleGetBrowserBenchmarkEvidenceReturnsProfilesAndClaimPolicy(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/browser-benchmarks", nil)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var payload BrowserBenchmarkEvidenceResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if !strings.Contains(payload.ClaimPolicy, "diagnostic canaries") {
		t.Fatalf("claim policy = %q, want diagnostic canary policy", payload.ClaimPolicy)
	}
	if payload.HardwareProfile.CPUCores != 4 || payload.HardwareProfile.MemoryGB != 8 {
		t.Fatalf("hardware profile = %#v, want 4 vCPU / 8 GB reference", payload.HardwareProfile)
	}
	if len(payload.Profiles) != 3 {
		t.Fatalf("profiles = %d, want 3", len(payload.Profiles))
	}
	seen := map[string]bool{}
	for _, profile := range payload.Profiles {
		seen[profile.ID] = true
		if profile.HighDensityClaimAllowed {
			t.Fatalf("profile %s allows high-density claims: %#v", profile.ID, profile)
		}
		if profile.Acceptance.LaunchP50TargetMs == 0 || profile.Acceptance.ActionOverheadTargetMs == 0 {
			t.Fatalf("profile %s missing acceptance targets: %#v", profile.ID, profile.Acceptance)
		}
		if profile.DensityEnvelope.RecommendedCanaryVUsersPerGenerator <= 0 {
			t.Fatalf("profile %s missing diagnostic canary envelope: %#v", profile.ID, profile.DensityEnvelope)
		}
	}
	for _, id := range []string{"desktop_chromium_1080p", "mobile_chromium_pixel_8", "mobile_webkit_iphone_15_pro"} {
		if !seen[id] {
			t.Fatalf("missing benchmark profile %q in %#v", id, payload.Profiles)
		}
	}
	if len(payload.EnvironmentMatrix) != 2 {
		t.Fatalf("environment matrix = %d, want Chromium and WebKit entries", len(payload.EnvironmentMatrix))
	}
}

func TestCompetitiveParityStatusExposesEvidenceAndNonGoals(t *testing.T) {
	areas := buildCompetitiveParityStatus()
	if len(areas) != 7 {
		t.Fatalf("competitive area count = %d, want 7", len(areas))
	}

	required := map[string]bool{
		"developer_experience":             false,
		"managed_scale_load_zones":         false,
		"authoring_recording":              false,
		"browser_testing":                  false,
		"analysis_integrations":            false,
		"specialized_enterprise_protocols": false,
		"packaging_samples_trust":          false,
	}
	competitorCoverage := map[string]bool{
		"k6":      false,
		"NeoLoad": false,
	}
	for _, area := range areas {
		if _, ok := required[area.ID]; ok {
			required[area.ID] = area.Status != "" &&
				area.Summary != "" &&
				area.Decision != "" &&
				area.EvidenceLevel != "" &&
				len(area.Evidence) > 0 &&
				area.Evidence[0] == competitiveSupportMatrixEvidence &&
				len(area.Remaining) > 0
		}
		for _, competitor := range area.Competitors {
			if _, ok := competitorCoverage[competitor]; ok {
				competitorCoverage[competitor] = true
			}
		}
		if area.ID == "specialized_enterprise_protocols" {
			if area.Status != "gap" {
				t.Fatalf("specialized protocol status = %q, want gap", area.Status)
			}
			if area.Decision != "demand_gated_non_goal" {
				t.Fatalf("specialized protocol decision = %q, want demand_gated_non_goal", area.Decision)
			}
		}
	}
	for id, ok := range required {
		if !ok {
			t.Fatalf("missing populated competitive parity area %q in %#v", id, areas)
		}
	}
	for competitor, ok := range competitorCoverage {
		if !ok {
			t.Fatalf("missing competitive coverage for %q in %#v", competitor, areas)
		}
	}
}

func TestCompetitiveSupportMatrixDocumentsParityStatusClaims(t *testing.T) {
	contentBytes, err := os.ReadFile(filepath.Join("..", "..", competitiveSupportMatrixEvidence))
	if err != nil {
		t.Fatalf("read competitive support matrix: %v", err)
	}
	content := string(contentBytes)
	for _, required := range []string{
		"Competitive Parity API Coverage",
		"k6",
		"NeoLoad",
		"LoadRunner",
		"JMeter",
		"DevWeb",
		"HAR",
		"Native Stormrunner JSON DSL",
		"Browser feature support and browser density claims are separate",
	} {
		if !strings.Contains(content, required) {
			t.Fatalf("competitive support matrix missing %q", required)
		}
	}
	for _, area := range buildCompetitiveParityStatus() {
		if !strings.Contains(content, area.Name) {
			t.Fatalf("competitive support matrix does not document API dimension %q", area.Name)
		}
		if area.Status == "gap" && !strings.Contains(content, "Gap") {
			t.Fatalf("competitive support matrix missing gap status vocabulary for %q", area.Name)
		}
		if area.EvidenceLevel == "" || area.Decision == "" || len(area.Remaining) == 0 {
			t.Fatalf("competitive parity area %q is missing release-review evidence fields: %#v", area.ID, area)
		}
	}
}

func TestAggregateParityStatusReportsBlockedGate(t *testing.T) {
	status := aggregateParityStatus([]ParityStatusArea{
		{ID: "ready", Status: "complete"},
		{ID: "blocked", Status: "partial", BlockingGate: true},
	})
	if status != "blocked" {
		t.Fatalf("status = %q, want blocked", status)
	}
}
