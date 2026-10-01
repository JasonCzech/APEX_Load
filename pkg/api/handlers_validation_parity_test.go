package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/enterprisearchive"
	"github.com/jasonczech/apexload/pkg/dsl"
	browserprotocol "github.com/jasonczech/apexload/pkg/protocol/browser"
)

func TestScriptValidationParityAcrossCurrentEndpoints(t *testing.T) {
	server := NewServer(ServerConfig{})

	invalidScript := func() dsl.Script {
		return dsl.Script{
			Name:     "Invalid Browser Draft",
			Version:  "1.0.0",
			Protocol: "browser",
			Config: dsl.ScriptConfig{
				Browser: dsl.BrowserConfig{Engine: "chromium"},
			},
			Actions: []dsl.Action{{Name: "Capture current page"}},
		}
	}
	validBrowserScript := &dsl.Script{
		Name:     "Valid Browser Script",
		Version:  "1.0.0",
		Protocol: "browser",
		Config: dsl.ScriptConfig{
			Browser: dsl.BrowserConfig{Engine: "chromium"},
		},
		Actions: []dsl.Action{{Name: "Capture current page", Type: "browser_screenshot"}},
	}
	storedValid := server.runner.StoreScript(validBrowserScript)
	storedInvalid := server.runner.StoreScript(func() *dsl.Script {
		script := invalidScript()
		return &script
	}())

	expectedIssues := validateScript(func() *dsl.Script {
		script := invalidScript()
		return &script
	}())
	if len(expectedIssues) == 0 {
		t.Fatal("expected invalid script to produce validation issues")
	}

	invalidScriptBody := func(t *testing.T) []byte {
		t.Helper()
		body, err := json.Marshal(map[string]any{"script": invalidScript()})
		if err != nil {
			t.Fatalf("json.Marshal returned error: %v", err)
		}
		return body
	}

	testCases := []struct {
		name       string
		method     string
		path       string
		body       func(*testing.T) []byte
		wantStatus int
	}{
		{name: "create", method: http.MethodPost, path: "/api/v1/scripts", body: invalidScriptBody, wantStatus: http.StatusBadRequest},
		{name: "update", method: http.MethodPut, path: "/api/v1/scripts/" + storedValid.ID, body: invalidScriptBody, wantStatus: http.StatusBadRequest},
		{name: "debug draft", method: http.MethodPost, path: "/api/v1/scripts/debug/sessions", body: invalidScriptBody, wantStatus: http.StatusBadRequest},
		{name: "debug stored", method: http.MethodPost, path: "/api/v1/scripts/" + storedInvalid.ID + "/debug/sessions", wantStatus: http.StatusBadRequest},
		{name: "replay session draft", method: http.MethodPost, path: "/api/v1/scripts/replay/sessions", body: invalidScriptBody, wantStatus: http.StatusBadRequest},
		{name: "replay session stored", method: http.MethodPost, path: "/api/v1/scripts/" + storedInvalid.ID + "/replay/sessions", wantStatus: http.StatusBadRequest},
		{name: "validate draft", method: http.MethodPost, path: "/api/v1/scripts/validate", body: invalidScriptBody, wantStatus: http.StatusOK},
		{name: "validate stored", method: http.MethodPost, path: "/api/v1/scripts/" + storedInvalid.ID + "/validate", wantStatus: http.StatusOK},
		{name: "replay draft", method: http.MethodPost, path: "/api/v1/scripts/replay", body: invalidScriptBody, wantStatus: http.StatusBadRequest},
		{name: "replay stored", method: http.MethodPost, path: "/api/v1/scripts/" + storedInvalid.ID + "/replay", wantStatus: http.StatusBadRequest},
		{name: "browser replay draft", method: http.MethodPost, path: "/api/v1/scripts/browser-replay/sessions", body: invalidScriptBody, wantStatus: http.StatusBadRequest},
		{name: "browser replay stored", method: http.MethodPost, path: "/api/v1/scripts/" + storedInvalid.ID + "/browser-replay/sessions", wantStatus: http.StatusBadRequest},
	}

	t.Run("parser and disk use the same validator", func(t *testing.T) {
		data := invalidScriptBody(t)
		if _, err := dsl.Parse(data); err == nil {
			t.Fatal("parser accepted invalid draft")
		} else {
			for _, issue := range expectedIssues {
				if !strings.Contains(err.Error(), issue) {
					t.Fatalf("parser lost issue %q: %v", issue, err)
				}
			}
		}
		path := filepath.Join(t.TempDir(), "script.json")
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := dsl.ParseFile(path); err == nil {
			t.Fatal("disk parser accepted invalid draft")
		} else {
			for _, issue := range expectedIssues {
				if !strings.Contains(err.Error(), issue) {
					t.Fatalf("disk parser lost issue %q: %v", issue, err)
				}
			}
		}
	})

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var bodyReader *bytes.Reader
			if tc.body != nil {
				bodyReader = bytes.NewReader(tc.body(t))
			} else {
				bodyReader = bytes.NewReader(nil)
			}

			req := httptest.NewRequest(tc.method, tc.path, bodyReader)
			if tc.body != nil {
				req.Header.Set("Content-Type", "application/json")
			}
			resp := httptest.NewRecorder()
			server.mux.ServeHTTP(resp, req)

			if resp.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d body=%s", resp.Code, tc.wantStatus, resp.Body.String())
			}

			issues := extractValidationIssues(t, resp.Body.Bytes())
			if !reflect.DeepEqual(issues, expectedIssues) {
				t.Fatalf("issues = %#v, want %#v body=%s", issues, expectedIssues, resp.Body.String())
			}
		})
	}
}

func TestValidateScriptIncludesWebKitWarnings(t *testing.T) {
	server := NewServer(ServerConfig{})
	stored := server.runner.StoreScript(&dsl.Script{
		Name:     "Valid WebKit Script",
		Version:  "1.0.0",
		Protocol: "browser",
		Config: dsl.ScriptConfig{
			Browser: dsl.BrowserConfig{Engine: "webkit"},
		},
		Actions: []dsl.Action{{Name: "Capture current page", Type: "browser_screenshot"}},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/"+stored.ID+"/validate", bytes.NewReader(nil))
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	warnings := extractValidationWarnings(t, resp.Body.Bytes())
	expected := browserprotocol.RuntimeInstallWarning("webkit")
	if expected == "" && len(warnings) != 0 {
		t.Fatalf("warnings = %#v, want none for an installed WebKit runtime", warnings)
	}
	if expected != "" && (len(warnings) != 1 || warnings[0] != expected) {
		t.Fatalf("warnings = %#v, want %#v", warnings, []string{expected})
	}
}

func TestValidateScriptTreatsStreamingSettingsAsExecutable(t *testing.T) {
	server := NewServer(ServerConfig{})
	stored := server.runner.StoreScript(&dsl.Script{
		Name:     "Streaming Import Draft",
		Version:  "1.0.0",
		Protocol: "http",
		Config: dsl.ScriptConfig{
			WebRuntime: dsl.WebRuntimeConfig{
				Streaming: dsl.StreamingConfig{
					Mode:                "fixed",
					BufferSizeSeconds:   30,
					FixedBitrateKbps:    1800,
					BandwidthEstimation: "instant",
				},
			},
		},
		Actions: []dsl.Action{{Name: "Homepage", Type: "GET", Target: "/"}},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/"+stored.ID+"/validate", bytes.NewReader(nil))
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	warnings := extractValidationWarnings(t, resp.Body.Bytes())
	if len(warnings) != 0 {
		t.Fatalf("warnings = %#v, want no streaming compatibility warning", warnings)
	}
	parity := extractValidationRuntimeSettingParity(t, resp.Body.Bytes())
	if len(parity) != 1 || parity[0].ID != "streaming" || parity[0].Status != enterprisearchive.RuntimeSettingStatusExecuted {
		t.Fatalf("runtime_settings_parity = %#v, want executable streaming entry", parity)
	}
}

func TestValidateScriptTreatsNativeMappedSourceRuntimeAttributesAsExecuted(t *testing.T) {
	server := NewServer(ServerConfig{})
	stored := server.runner.StoreScript(&dsl.Script{
		Name:     "TLS Source Runtime Metadata",
		Version:  "1.0.0",
		Protocol: "http",
		Config: dsl.ScriptConfig{
			HTTP: dsl.HTTPConfig{
				TLS: dsl.TLSConfig{MinVersion: "1.2", MaxVersion: "1.2"},
			},
			WebRuntime: dsl.WebRuntimeConfig{
				AdditionalAttributes: map[string]string{
					"web_set_sockets_option.SSL_VERSION": "TLS1.2",
				},
			},
		},
		Actions: []dsl.Action{{Name: "Homepage", Type: "GET", Target: "/"}},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/"+stored.ID+"/validate", bytes.NewReader(nil))
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	parity := extractValidationRuntimeSettingParity(t, resp.Body.Bytes())
	if len(parity) != 1 || parity[0].ID != "tls" || parity[0].Status != enterprisearchive.RuntimeSettingStatusExecuted {
		t.Fatalf("runtime_settings_parity = %#v, want only executable TLS entry", parity)
	}
	counts := extractValidationRuntimeSettingStatusCounts(t, resp.Body.Bytes())
	if counts["compatibility_only"] != 0 {
		t.Fatalf("runtime_settings_status_counts = %#v, want no compatibility-only runtime setting", counts)
	}
	health := extractValidationAuthoringHealth(t, resp.Body.Bytes())
	item := authoringHealthItem(t, health, "runtime_parity")
	if item.Status != "pass" || item.Count != 0 {
		t.Fatalf("runtime parity health item = %#v, want pass without count", item)
	}
}

func TestValidateScriptRuntimeParityHealthCountUsesAffectedSettings(t *testing.T) {
	response := buildScriptValidationResponse("", &dsl.Script{
		Name:     "Compatibility Runtime Metadata",
		Version:  "1.0.0",
		Protocol: "http",
		Config: dsl.ScriptConfig{
			WebRuntime: dsl.WebRuntimeConfig{
				AdditionalAttributes: map[string]string{
					// An arbitrary imported RTS attribute with no native APEX
					// equivalent stays compatibility-only and docks parity health.
					"BusinessProcess": "Checkout",
				},
			},
		},
		Actions: []dsl.Action{{Name: "Homepage", Type: "GET", Target: "/"}},
	})

	if response.RuntimeSettingsStatusCounts["compatibility_only"] != 1 {
		t.Fatalf("runtime_settings_status_counts = %#v, want one compatibility-only setting", response.RuntimeSettingsStatusCounts)
	}
	item := authoringHealthItem(t, response.AuthoringHealth, "runtime_parity")
	if item.Status != "warning" || item.Count != 1 {
		t.Fatalf("runtime parity health item = %#v, want warning count=1", item)
	}
}

func TestValidateScriptIncludesRecordedResourceMetadataWarnings(t *testing.T) {
	server := NewServer(ServerConfig{})
	stored := server.runner.StoreScript(&dsl.Script{
		Name:     "Recorded Resources Browser Script",
		Version:  "1.0.0",
		Protocol: "browser",
		Config: dsl.ScriptConfig{
			Browser: dsl.BrowserConfig{
				Engine:            "chromium",
				DownloadResources: "recorded",
			},
		},
		Actions: []dsl.Action{{Name: "Capture current page", Type: "browser_screenshot"}},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/"+stored.ID+"/validate", bytes.NewReader(nil))
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	warnings := extractValidationWarnings(t, resp.Body.Bytes())
	if len(warnings) != 1 || !strings.Contains(strings.ToLower(warnings[0]), "recorded-resource metadata") {
		t.Fatalf("warnings = %#v, want recorded resource metadata guidance", warnings)
	}
}

func TestScriptValidationIncludesRuntimeSettingParityForImportedDraftsAndStoredScripts(t *testing.T) {
	server := NewServer(ServerConfig{})
	importedScript := dsl.Script{
		Name:     "Imported Runtime Script",
		Version:  "1.0.0",
		Protocol: "browser",
		Config: dsl.ScriptConfig{
			RunLogic: dsl.RunLogicConfig{
				Iterations:       3,
				AutoTransactions: "per_action",
			},
			Log: dsl.LogConfig{
				Level: "extended",
			},
			Browser: dsl.BrowserConfig{
				DownloadResources: "recorded",
			},
			WebRuntime: dsl.WebRuntimeConfig{
				Streaming: dsl.StreamingConfig{
					Mode:              "fixed",
					BufferSizeSeconds: 30,
				},
			},
		},
		Actions: []dsl.Action{{Name: "Capture current page", Type: "browser_screenshot"}},
		Metadata: &dsl.ScriptMetadata{
			Import: &dsl.ImportMetadata{
				Source: "enterprise_script_archive",
				Preserved: map[string]any{
					"runtime_settings_parity": []enterprisearchive.RuntimeSettingParityEntry{
						{ID: "run_logic_iterations", Area: "Run Logic", Concept: "Iterations", Status: enterprisearchive.RuntimeSettingStatusExecuted, RuntimeFiles: []string{"default.cfg"}},
						{ID: "run_logic_auto_transactions", Area: "Run Logic", Concept: "Auto-transactions", Status: enterprisearchive.RuntimeSettingStatusExecuted, RuntimeFiles: []string{"default.cfg"}},
						{ID: "logging", Area: "Logging", Concept: "Log level, error-only, and extended logging options", Status: enterprisearchive.RuntimeSettingStatusExecuted, RuntimeFiles: []string{"default.cfg"}},
						{ID: "browser_download_resources_recorded", Area: "Browser Emulation", Concept: "Download resources = recorded", Status: enterprisearchive.RuntimeSettingStatusExecuted, RuntimeFiles: []string{"default.cfg"}},
						{ID: "streaming", Area: "Streaming", Concept: "Adaptive or fixed playback settings", Status: enterprisearchive.RuntimeSettingStatusExecuted, RuntimeFiles: []string{"default.cfg"}},
					},
					"runtime_settings_migration_readiness": &enterprisearchive.RuntimeSettingMigrationReadiness{
						ScorePct:         0,
						Blocked:          false,
						BlockingStatuses: []enterprisearchive.RuntimeSettingStatus{},
						BlockingSettings: []enterprisearchive.RuntimeSettingParityEntry{},
						Policy: enterprisearchive.RuntimeSettingMigrationPolicy{
							BlockStatuses: []enterprisearchive.RuntimeSettingStatus{enterprisearchive.RuntimeSettingStatusPartial, enterprisearchive.RuntimeSettingStatusCompatibilityOnly},
						},
					},
				},
			},
		},
	}
	stored := server.runner.StoreScript(&importedScript)

	draftBody, err := json.Marshal(map[string]any{"script": importedScript})
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}

	testCases := []struct {
		name string
		path string
		body []byte
	}{
		{name: "draft", path: "/api/v1/scripts/validate", body: draftBody},
		{name: "stored", path: "/api/v1/scripts/" + stored.ID + "/validate"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.path, bytes.NewReader(tc.body))
			if tc.body != nil {
				req.Header.Set("Content-Type", "application/json")
			}
			resp := httptest.NewRecorder()
			server.mux.ServeHTTP(resp, req)

			if resp.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
			}

			parity := extractValidationRuntimeSettingParity(t, resp.Body.Bytes())
			if len(parity) != 5 {
				t.Fatalf("runtime_settings_parity = %#v, want 5 entries", parity)
			}
			if parity[0].RuntimeFiles == nil {
				t.Fatalf("runtime_settings_parity[0].runtime_files = %#v, want preserved runtime file metadata", parity[0].RuntimeFiles)
			}

			counts := extractValidationRuntimeSettingStatusCounts(t, resp.Body.Bytes())
			if counts["executed"] != 5 || counts["partial"] != 0 || counts["compatibility_only"] != 0 {
				t.Fatalf("runtime_settings_status_counts = %#v, want executed=5 partial=0 compatibility_only=0", counts)
			}

			readiness := extractValidationMigrationReadiness(t, resp.Body.Bytes())
			if readiness == nil {
				t.Fatal("expected migration_readiness")
			}
			if readiness.Blocked || readiness.ScorePct != 100 {
				t.Fatalf("migration_readiness = %#v, want ready score=100", readiness)
			}
			if len(readiness.Policy.BlockStatuses) != 2 || readiness.Policy.BlockStatuses[0] != enterprisearchive.RuntimeSettingStatusPartial || readiness.Policy.BlockStatuses[1] != enterprisearchive.RuntimeSettingStatusCompatibilityOnly {
				t.Fatalf("migration_readiness.policy = %#v, want partial + compatibility_only", readiness.Policy)
			}
		})
	}
}

func TestValidationPreservesImportRuntimeFindingsWhilePolicyRelaxationUnblocksReadiness(t *testing.T) {
	scriptWithPolicy := func(blockStatuses []enterprisearchive.RuntimeSettingStatus) *dsl.Script {
		return &dsl.Script{
			Name:     "Imported runtime review",
			Version:  "1.0.0",
			Protocol: "http",
			Config: dsl.ScriptConfig{
				BaseURL: "https://example.test",
				ThinkTime: dsl.ThinkTimeConfig{
					Type:       "recorded",
					Multiplier: 1,
				},
			},
			Actions: []dsl.Action{{Name: "request", Type: "request", Method: http.MethodGet, Target: "/"}},
			Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
				Source: "enterprise_script_archive",
				Preserved: map[string]any{
					"runtime_settings_parity": []enterprisearchive.RuntimeSettingParityEntry{
						{
							ID:           "think_time",
							Area:         "Think Time",
							Concept:      "Replay recorded think time",
							Status:       enterprisearchive.RuntimeSettingStatusPartial,
							Notes:        "Source RANDOM percentage mode has no exact destination equivalent; recorded at 100% pending review.",
							RuntimeFiles: []string{"profiles/default.cfg"},
						},
						{
							ID:           "unmapped_source_settings",
							Area:         "Source Runtime",
							Concept:      "Preserved source-only setting",
							Status:       enterprisearchive.RuntimeSettingStatusPartial,
							Notes:        "Raw option FutureControl=enabled is preserved for review.",
							RuntimeFiles: []string{"profiles/default.cfg"},
						},
					},
					"runtime_settings_migration_readiness": &enterprisearchive.RuntimeSettingMigrationReadiness{
						Policy: enterprisearchive.RuntimeSettingMigrationPolicy{BlockStatuses: blockStatuses},
					},
				},
			}},
		}
	}

	strict := buildScriptValidationResponse("strict", scriptWithPolicy([]enterprisearchive.RuntimeSettingStatus{
		enterprisearchive.RuntimeSettingStatusPartial,
	}))
	if len(strict.RuntimeSettingsParity) != 2 {
		t.Fatalf("strict parity = %#v, want matched partial plus preserved-only finding", strict.RuntimeSettingsParity)
	}
	if got := strict.RuntimeSettingsParity[0]; got.ID != "think_time" ||
		got.Status != enterprisearchive.RuntimeSettingStatusPartial ||
		got.Notes == "" ||
		!reflect.DeepEqual(got.RuntimeFiles, []string{"profiles/default.cfg"}) {
		t.Fatalf("matched import finding = %#v, want partial status and source evidence", got)
	}
	if got := strict.RuntimeSettingsParity[1]; got.ID != "unmapped_source_settings" ||
		got.Status != enterprisearchive.RuntimeSettingStatusPartial ||
		got.Notes == "" ||
		!reflect.DeepEqual(got.RuntimeFiles, []string{"profiles/default.cfg"}) {
		t.Fatalf("preserved-only finding = %#v, want intact source evidence", got)
	}
	if strict.MigrationReadiness == nil || !strict.MigrationReadiness.Blocked ||
		len(strict.MigrationReadiness.BlockingSettings) != 2 {
		t.Fatalf("strict readiness = %#v, want both partial findings blocked", strict.MigrationReadiness)
	}

	relaxed := buildScriptValidationResponse("relaxed", scriptWithPolicy([]enterprisearchive.RuntimeSettingStatus{
		enterprisearchive.RuntimeSettingStatusBlocked,
	}))
	if relaxed.MigrationReadiness == nil || relaxed.MigrationReadiness.Blocked {
		t.Fatalf("relaxed readiness = %#v, want policy relaxation to unblock", relaxed.MigrationReadiness)
	}
	if len(relaxed.RuntimeSettingsParity) != 2 ||
		relaxed.RuntimeSettingsStatusCounts[string(enterprisearchive.RuntimeSettingStatusPartial)] != 2 {
		t.Fatalf("relaxed parity/counts = %#v / %#v, want evidence retained after relaxation",
			relaxed.RuntimeSettingsParity, relaxed.RuntimeSettingsStatusCounts)
	}
}

func TestBrowserCompatibilityMatrixParityAcrossValidationAndReplayEndpoints(t *testing.T) {
	server := NewServer(ServerConfig{})

	invalidScript := dsl.Script{
		Name:     "Invalid Browser Compatibility Draft",
		Version:  "1.0.0",
		Protocol: "browser",
		Config: dsl.ScriptConfig{
			Browser: dsl.BrowserConfig{
				Engine:        "chromium",
				DeviceProfile: "iphone_15_pro",
			},
		},
		Actions: []dsl.Action{{Name: "Capture current page", Type: "browser_screenshot"}},
	}
	storedInvalid := server.runner.StoreScript(&invalidScript)

	expectedIssues := validateScript(&invalidScript)
	if len(expectedIssues) == 0 {
		t.Fatal("expected invalid compatibility script to produce validation issues")
	}

	draftBody := func(t *testing.T) []byte {
		t.Helper()
		body, err := json.Marshal(map[string]any{"script": invalidScript})
		if err != nil {
			t.Fatalf("json.Marshal returned error: %v", err)
		}
		return body
	}

	testCases := []struct {
		name       string
		method     string
		path       string
		body       func(*testing.T) []byte
		wantStatus int
	}{
		{name: "validate draft", method: http.MethodPost, path: "/api/v1/scripts/validate", body: draftBody, wantStatus: http.StatusOK},
		{name: "validate stored", method: http.MethodPost, path: "/api/v1/scripts/" + storedInvalid.ID + "/validate", wantStatus: http.StatusOK},
		{name: "replay draft", method: http.MethodPost, path: "/api/v1/scripts/replay", body: draftBody, wantStatus: http.StatusBadRequest},
		{name: "replay stored", method: http.MethodPost, path: "/api/v1/scripts/" + storedInvalid.ID + "/replay", wantStatus: http.StatusBadRequest},
		{name: "browser replay draft", method: http.MethodPost, path: "/api/v1/scripts/browser-replay/sessions", body: draftBody, wantStatus: http.StatusBadRequest},
		{name: "browser replay stored", method: http.MethodPost, path: "/api/v1/scripts/" + storedInvalid.ID + "/browser-replay/sessions", wantStatus: http.StatusBadRequest},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var bodyReader *bytes.Reader
			if tc.body != nil {
				bodyReader = bytes.NewReader(tc.body(t))
			} else {
				bodyReader = bytes.NewReader(nil)
			}

			req := httptest.NewRequest(tc.method, tc.path, bodyReader)
			if tc.body != nil {
				req.Header.Set("Content-Type", "application/json")
			}
			resp := httptest.NewRecorder()
			server.mux.ServeHTTP(resp, req)

			if resp.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d body=%s", resp.Code, tc.wantStatus, resp.Body.String())
			}

			issues := extractValidationIssues(t, resp.Body.Bytes())
			if !reflect.DeepEqual(issues, expectedIssues) {
				t.Fatalf("issues = %#v, want %#v body=%s", issues, expectedIssues, resp.Body.String())
			}
		})
	}
}

func extractValidationIssues(t *testing.T, body []byte) []string {
	t.Helper()

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}

	if rawIssues, ok := payload["issues"]; ok {
		items, ok := rawIssues.([]any)
		if !ok {
			t.Fatalf("issues payload = %#v, want []any", rawIssues)
		}
		issues := make([]string, 0, len(items))
		for _, item := range items {
			text, ok := item.(string)
			if !ok {
				t.Fatalf("issue item = %#v, want string", item)
			}
			issues = append(issues, text)
		}
		return issues
	}

	rawError, ok := payload["error"]
	if !ok {
		t.Fatalf("validation payload missing issues and error: %#v", payload)
	}
	text, ok := rawError.(string)
	if !ok {
		t.Fatalf("error payload = %#v, want string", rawError)
	}
	return strings.Split(text, "; ")
}

func extractValidationWarnings(t *testing.T, body []byte) []string {
	t.Helper()

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	rawWarnings, ok := payload["warnings"]
	if !ok {
		return nil
	}
	items, ok := rawWarnings.([]any)
	if !ok {
		t.Fatalf("warnings payload = %#v, want []any", rawWarnings)
	}
	warnings := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			t.Fatalf("warning item = %#v, want string", item)
		}
		warnings = append(warnings, text)
	}
	return warnings
}

func extractValidationRuntimeSettingParity(t *testing.T, body []byte) []enterprisearchive.RuntimeSettingParityEntry {
	t.Helper()

	var payload struct {
		RuntimeSettingsParity []enterprisearchive.RuntimeSettingParityEntry `json:"runtime_settings_parity"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	return payload.RuntimeSettingsParity
}

func extractValidationRuntimeSettingStatusCounts(t *testing.T, body []byte) map[string]int {
	t.Helper()

	var payload struct {
		RuntimeSettingsStatusCounts map[string]int `json:"runtime_settings_status_counts"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	return payload.RuntimeSettingsStatusCounts
}

func extractValidationMigrationReadiness(t *testing.T, body []byte) *enterprisearchive.RuntimeSettingMigrationReadiness {
	t.Helper()

	var payload struct {
		MigrationReadiness *enterprisearchive.RuntimeSettingMigrationReadiness `json:"migration_readiness"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	return payload.MigrationReadiness
}

func extractValidationAuthoringHealth(t *testing.T, body []byte) AuthoringHealth {
	t.Helper()

	var payload struct {
		AuthoringHealth AuthoringHealth `json:"authoring_health"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	return payload.AuthoringHealth
}

func authoringHealthItem(t *testing.T, health AuthoringHealth, key string) AuthoringHealthItem {
	t.Helper()
	for _, item := range health.Inputs {
		if item.Key == key {
			return item
		}
	}
	t.Fatalf("authoring health item %q not found in %#v", key, health.Inputs)
	return AuthoringHealthItem{}
}
