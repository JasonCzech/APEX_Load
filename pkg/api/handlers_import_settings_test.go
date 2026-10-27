package api

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/importartifact"
)

func settingsTestScript() *dsl.Script {
	return &dsl.Script{Name: "settings", Version: "1.0", Protocol: "http", ProjectID: "p", Config: dsl.ScriptConfig{BaseURL: "https://example.test"}, Actions: []dsl.Action{{Name: "request", Type: "http_request", Method: "GET", Target: "/"}}, Parameters: []dsl.Parameter{{Name: "region", Type: "list", Values: []string{"west"}, Selection: "sequential", Update: "once"}}}
}
func settingChange(path, value string) ImportSettingsChange {
	return ImportSettingsChange{Path: path, Value: json.RawMessage(value)}
}
func TestApplyImportSettingsAtomicAndScoped(t *testing.T) {
	for _, test := range []struct {
		name, category string
		changes        []ImportSettingsChange
		wantError      bool
	}{
		{"native HTTP policies", "HTTP", []ImportSettingsChange{settingChange("config.http.page_timeout_ms", "500000"), settingChange("config.http.ip_version", `"ipv4"`), settingChange("config.http.cache_max_unused_iterations", "2"), settingChange("config.http.html_parse_mode", `"text"`), settingChange("config.http.cache_excluded_mime_types", `["text/xml"]`), settingChange("config.http.html_excluded_mime_types", `["text/plain"]`)}, false},
		{"page deadline bound", "HTTP", []ImportSettingsChange{settingChange("config.http.page_timeout_ms", "3600001")}, true},
		{"MIME wildcard rejected", "HTTP", []ImportSettingsChange{settingChange("config.http.html_excluded_mime_types", `["text/*"]`)}, true},
		{"unknown IP enum rejected", "HTTP", []ImportSettingsChange{settingChange("config.http.ip_version", `"2"`)}, true},
		{"timeout", "HTTP", []ImportSettingsChange{settingChange("config.http.request_timeout_ms", "1200")}, false},
		{"idle timeout", "HTTP", []ImportSettingsChange{settingChange("config.http.idle_timeout_ms", "60000")}, false},
		{"idle timeout boundary", "HTTP", []ImportSettingsChange{settingChange("config.http.idle_timeout_ms", "3600000")}, false},
		{"idle timeout exceeds bound", "HTTP", []ImportSettingsChange{settingChange("config.http.idle_timeout_ms", "6000000")}, true},
		{"wrong category", "Pacing", []ImportSettingsChange{settingChange("config.http.request_timeout_ms", "1200")}, true},
		{"negative", "HTTP", []ImportSettingsChange{settingChange("config.http.request_timeout_ms", "-1")}, true},
		{"wrong type", "HTTP", []ImportSettingsChange{settingChange("config.http.request_timeout_ms", `"bad"`)}, true},
		{"partial failure", "HTTP", []ImportSettingsChange{settingChange("config.http.request_timeout_ms", "1200"), settingChange("config.http.max_conns_per_host", "-1")}, true},
		{"unknown field", "HTTP", []ImportSettingsChange{settingChange("metadata.import.preserved", "{}")}, true},
		{"credentials", "HTTP", []ImportSettingsChange{settingChange("config.http.auth.password", `"literal"`)}, true},
		{"ordinary list", "parameters:list", []ImportSettingsChange{{Parameter: "region", Path: "values", Value: json.RawMessage(`["east","west"]`)}}, false},
		{"missing parameter", "parameters:list", []ImportSettingsChange{{Parameter: "missing", Path: "values", Value: json.RawMessage(`["east"]`)}}, true},
		{"invalid allocation", "parameters:list", []ImportSettingsChange{{Parameter: "region", Path: "auto_allocate", Value: json.RawMessage(`true`)}}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			original := settingsTestScript()
			before, _ := json.Marshal(original)
			draft, errs := applyImportSettings(original, test.category, test.changes)
			if (len(errs) > 0) != test.wantError {
				t.Fatalf("errors=%v", errs)
			}
			after, _ := json.Marshal(original)
			if !bytes.Equal(before, after) {
				t.Fatal("source was mutated")
			}
			if !test.wantError && draft == nil {
				t.Fatal("missing result")
			}
		})
	}
}
func TestImportSettingsSchemaMatchesDSL(t *testing.T) {
	for _, field := range importSettingFields {
		t.Run(field.Path, func(t *testing.T) {
			wire := map[string]any{}
			var value any = "example"
			switch field.Type {
			case "number":
				value = 1
			case "boolean":
				value = true
			case "select":
				value = field.Options[0]
			case "lines":
				value = []string{"example"}
			}
			setImportSettingValue(wire, strings.Split(field.Path, "."), value)
			b, _ := json.Marshal(wire)
			var script dsl.Script
			decoder := json.NewDecoder(bytes.NewReader(b))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&script); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestImportSettingsPreservesProtectedCandidate(t *testing.T) {
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{Key: bytes.Repeat([]byte{0x5a}, 32), KeyID: "settings-test"})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{BrunoArtifactDir: t.TempDir(), BrunoArtifactWrapper: wrapper})
	draft := settingsTestScript()
	draft.Config.HTTP.Auth = dsl.AuthConfig{Type: "basic", Username: "operator", Password: "preserve-this-password"}
	draft.Actions[0].Metadata = map[string]any{"execution_blocker": "unsupported_source_behavior"}
	ensureScriptImportPreserved(draft, enterpriseArchiveSaveProfile)
	draft.Metadata.Import.Preserved["runtime_files"] = map[string]any{"default.cfg": map[string]any{"HTTP": map[string]any{"ReceiveTimeout": "10"}}}
	r := httptest.NewRequest("POST", "/", nil)
	id, _, err := server.registerImportCandidateSession(r, enterpriseArchiveSaveProfile, draft)
	if err != nil {
		t.Fatal(err)
	}
	draft.Metadata.Import.Preserved[importDraftAttestationMetadataKey] = importDraftProtectedTokenPrefix + id
	projected := repairDisclosure(draft)
	req := ImportSettingsRequest{Script: projected, Category: "HTTP", Changes: []ImportSettingsChange{settingChange("config.http.request_timeout_ms", "2500")}}
	call := func(req ImportSettingsRequest) (int, []byte) {
		b, _ := json.Marshal(req)
		w := httptest.NewRecorder()
		server.handleImportSettings(w, httptest.NewRequest("POST", "/api/v1/scripts/import/settings", bytes.NewReader(b)))
		return w.Code, w.Body.Bytes()
	}
	wrong := req
	wrong.Script = cloneScriptDefinition(projected)
	wrong.Script.ProjectID = "other"
	if code, _ := call(wrong); code != 409 {
		t.Fatalf("cross-project %d", code)
	}
	invalid := req
	invalid.Changes = []ImportSettingsChange{settingChange("config.http.request_timeout_ms", "-1")}
	if code, _ := call(invalid); code != 422 {
		t.Fatalf("invalid category %d", code)
	}
	code, b := call(req)
	if code != 200 {
		t.Fatalf("apply %d: %s", code, b)
	}
	if bytes.Contains(b, []byte("preserve-this-password")) {
		t.Fatal("credential disclosure")
	}
	var result ImportSettingsResponse
	if err := json.Unmarshal(b, &result); err != nil {
		t.Fatal(err)
	}
	if result.Script.Config.HTTP.RequestTimeoutMs != 2500 {
		t.Fatal("setting not updated")
	}
	if result.Readiness.ReplayReady || result.ImportDraftExpiresAt == nil {
		t.Fatal("settings must refresh expiry without clearing unrelated replay blockers")
	}
	if reflect.DeepEqual(result.Script.Metadata.Import.Preserved[importDraftAttestationMetadataKey], projected.Metadata.Import.Preserved[importDraftAttestationMetadataKey]) {
		t.Fatal("draft capability was not rotated")
	}
	claim, err := server.claimImportDraftMutation(r, result.Script)
	if err != nil {
		t.Fatal(err)
	}
	defer server.releaseImportDraftAttestation(claim)
	if claim.Script.Config.HTTP.Auth.Password != "preserve-this-password" {
		t.Fatal("lost unrelated protected credential")
	}
	if !reflect.DeepEqual(claim.Script.Metadata.Import.Preserved["runtime_files"], draft.Metadata.Import.Preserved["runtime_files"]) {
		t.Fatal("source evidence changed")
	}
	if code, _ := call(req); code != 409 {
		t.Fatalf("old revision accepted: %d", code)
	}
}

func TestImportSettingsDoesNotInventSourceParity(t *testing.T) {
	server := NewServer(ServerConfig{})
	draft := settingsTestScript()
	ensureScriptImportPreserved(draft, enterpriseArchiveSaveProfile)
	draft.Metadata.Import.Source = "jmeter"
	req := ImportSettingsRequest{Script: draft, Category: "HTTP", Changes: []ImportSettingsChange{settingChange("config.http.request_timeout_ms", "6500")}}
	b, _ := json.Marshal(req)
	w := httptest.NewRecorder()
	server.handleImportSettings(w, httptest.NewRequest("POST", "/api/v1/scripts/import/settings", bytes.NewReader(b)))
	if w.Code != 200 {
		t.Fatalf("apply %d: %s", w.Code, w.Body.String())
	}
	var result ImportSettingsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Script.Metadata.Import.Preserved["runtime_settings_parity"] != nil {
		t.Fatal("runtime edit invented source mapping evidence")
	}
	if result.Script.Config.HTTP.RequestTimeoutMs != 6500 {
		t.Fatal("runtime setting not updated")
	}
}

func TestImportSettingsPreservesLargeIntegers(t *testing.T) {
	source := settingsTestScript()
	seed := int64(9007199254740993)
	source.Parameters = append(source.Parameters, dsl.Parameter{Name: "generated", Type: "random_number", Seed: &seed})
	source.Actions[0].Assertions = []dsl.Assertion{{Type: "status_code", Expected: float64(200)}}
	updated, errs := applyImportSettings(source, "HTTP", []ImportSettingsChange{settingChange("config.http.request_timeout_ms", "1200")})
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	if *updated.Parameters[1].Seed != seed {
		t.Fatalf("unrelated seed changed: %d", *updated.Parameters[1].Seed)
	}
	before, _ := json.Marshal(source.Actions)
	after, _ := json.Marshal(updated.Actions)
	if !bytes.Equal(before, after) {
		t.Fatalf("unrelated assertion changed: %s -> %s", before, after)
	}
}

func TestApplyImportRandomPercentageSettings(t *testing.T) {
	original := settingsTestScript()
	draft, errs := applyImportSettings(original, "Think time", []ImportSettingsChange{
		settingChange("config.think_time.type", `"random_percentage"`),
		settingChange("config.think_time.min_percent", "50"),
		settingChange("config.think_time.max_percent", "150"),
	})
	if len(errs) > 0 || draft == nil {
		t.Fatalf("apply: %v", errs)
	}
	if draft.Config.ThinkTime.Type != "random_percentage" || draft.Config.ThinkTime.MinPercent != 50 || draft.Config.ThinkTime.MaxPercent != 150 {
		t.Fatal(draft.Config.ThinkTime)
	}
	for _, value := range []string{"200", "10001", "-1"} {
		_, errs = applyImportSettings(draft, "Think time", []ImportSettingsChange{settingChange("config.think_time.min_percent", value)})
		if len(errs) == 0 {
			t.Fatalf("invalid lower bound %s accepted", value)
		}
	}
	if draft.Config.ThinkTime.MinPercent != 50 {
		t.Fatal("invalid edit mutated the draft")
	}
}

func TestImportReplayPoliciesAtomicValidation(t *testing.T) {
	for _, tc := range []struct {
		category  string
		changes   []ImportSettingsChange
		wantError bool
	}{
		{"HTTP", []ImportSettingsChange{settingChange("config.http.http_errors_as_warnings", "false"), settingChange("config.http.fail_noncritical_resources", "true"), settingChange("config.http.resource_timeout_as_warning", "false"), settingChange("config.http.legacy_html_checks", "false"), settingChange("config.http.dns_cache", "true"), settingChange("config.http.challenge_delay_ms", "25")}, false},
		{"Error handling", []ImportSettingsChange{settingChange("config.error_handling.fail_transaction_on_error_message", "false")}, false},
		{"Browser emulation", []ImportSettingsChange{settingChange("config.browser_emulation.cache.clear_cache_for_new_user", "false")}, false},
		{"Logging", []ImportSettingsChange{settingChange("config.log.max_response_bytes", "128")}, false},
		{"Logging", []ImportSettingsChange{settingChange("config.log.max_response_bytes", "1048577")}, true},
		{"HTTP", []ImportSettingsChange{settingChange("config.http.dns_cache", "true"), settingChange("config.http.challenge_delay_ms", "3600001")}, true},
		{"HTTP", []ImportSettingsChange{settingChange("config.http.challenge_delay_ms", "0.5")}, true},
	} {
		original := settingsTestScript()
		original.Config.HTTP.Auth.Password = "source credential"
		before, _ := json.Marshal(original)
		result, errs := applyImportSettings(original, tc.category, tc.changes)
		if (len(errs) > 0) != tc.wantError {
			t.Fatalf("category=%s errors=%v", tc.category, errs)
		}
		after, _ := json.Marshal(original)
		if !bytes.Equal(before, after) {
			t.Fatal("mutated source")
		}
		if tc.wantError {
			if result != nil {
				t.Fatal("partial candidate returned")
			}
			continue
		}
		if result.Config.HTTP.Auth.Password != "source credential" {
			t.Fatal("credential changed")
		}
	}
}

func TestImportSettingsTLSSelectionClearsOnlyTLSGates(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "unrelated edit", true: "TLS edit"}[explicit], func(t *testing.T) {
			script := settingsTestScript()
			script.Init = []dsl.Action{legacyTLSGateForTest("first", legacyTLSWarningForTest("AUTO")), legacyTLSGateForTest("second", legacyTLSWarningForTest("TLS-next"))}
			script.Actions[0].Metadata = map[string]any{"execution_blocker": "unrelated_requirement"}
			before, _ := json.Marshal(script)
			changes := []ImportSettingsChange{settingChange("config.http.request_timeout_ms", "2000")}
			if explicit {
				changes = []ImportSettingsChange{settingChange("config.http.tls.min_version", `"1.3"`), settingChange("config.http.tls.max_version", `"1.3"`)}
			}
			draft, errs := applyImportSettings(script, "HTTP", changes)
			if len(errs) != 0 {
				t.Fatal(errs)
			}
			if got := len(executionGatePaths(draft, "legacy_tls_replacement")); (got == 0) != explicit {
				t.Fatalf("TLS gates = %d, explicit = %v", got, explicit)
			}
			if !hasExecutionCode(draft.Actions[0].Metadata, "unrelated_requirement") {
				t.Fatal("unrelated gate was cleared")
			}
			if explicit && (draft.Init[0].Metadata["migration_change"] == nil || draft.Init[1].Metadata["migration_change"] == nil) {
				t.Fatal("TLS migration evidence missing")
			}
			after, _ := json.Marshal(script)
			if !bytes.Equal(before, after) {
				t.Fatal("source draft mutated")
			}
		})
	}
}

func TestImportSettingsTLSRestampsFidelityAndPreflight(t *testing.T) {
	server := newGuidedRepairArchiveServer(t, nil)
	script := storedRepairScript("inline-tls-settings", "http")
	id, _, err := server.registerImportCandidateSession(httptest.NewRequest("POST", "/", nil), enterpriseArchiveSaveProfile, script)
	if err != nil {
		t.Fatal(err)
	}
	script.Metadata.Import.Preserved[importDraftAttestationMetadataKey] = importDraftProtectedTokenPrefix + id
	payload, _ := json.Marshal(ImportSettingsRequest{Script: repairDisclosure(script), Category: "HTTP", Changes: []ImportSettingsChange{settingChange("config.http.tls.min_version", `"1.3"`), settingChange("config.http.tls.max_version", `"1.3"`)}})
	response := httptest.NewRecorder()
	server.handleImportSettings(response, httptest.NewRequest("POST", "/api/v1/scripts/import/settings", bytes.NewReader(payload)))
	if response.Code != 200 {
		t.Fatalf("apply %d: %s", response.Code, response.Body.String())
	}
	var result ImportSettingsResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if paths := executionGatePaths(result.Script, "legacy_tls_replacement"); len(paths) != 0 {
		t.Fatalf("TLS gates = %v", paths)
	}
	if !result.Readiness.ReplayReady {
		t.Fatalf("readiness = %+v", result.Readiness)
	}
	assertRepairedFidelityReport(t, "settings response", result.FidelityReport, "promotable")
	assertRepairedFidelityReport(t, "settings draft", result.Script.Metadata.Import.Preserved["fidelity_report"], "promotable")
	if preflight := preservedReplayPreflight(result.Script); preflight == nil || !preflight.Valid {
		t.Fatalf("preflight = %+v", preflight)
	}
}

func TestImportSettingsSelectsTLSRequiresAnExplicitTLSValue(t *testing.T) {
	for _, tt := range []struct {
		name    string
		changes []ImportSettingsChange
		want    bool
	}{
		{name: "empty"},
		{name: "default", changes: []ImportSettingsChange{settingChange("config.http.tls.min_version", " null ")}},
		{name: "unrelated", changes: []ImportSettingsChange{settingChange("config.http.request_timeout_ms", "1000")}},
		{name: "minimum", changes: []ImportSettingsChange{settingChange("config.http.tls.min_version", `"1.2"`)}, want: true},
		{name: "maximum", changes: []ImportSettingsChange{settingChange("config.http.tls.max_version", `"1.3"`)}, want: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := importSettingsSelectsTLS(tt.changes); got != tt.want {
				t.Fatalf("selects TLS = %v, want %v", got, tt.want)
			}
		})
	}
}
