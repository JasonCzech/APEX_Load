package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/paramize"
	"github.com/jasonczech/apexload/pkg/convert/sample"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/internal/importreview"
)

func postExcludeImportDraftActions(t *testing.T, server *Server, payload map[string]any) (*httptest.ResponseRecorder, map[string]json.RawMessage) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/exclude-actions", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	var envelope map[string]json.RawMessage
	if resp.Code == http.StatusOK {
		if err := json.Unmarshal(resp.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
	}
	return resp, envelope
}

func blockedHARAction(name, uid string) dsl.Action {
	return dsl.Action{
		Name:     name,
		Type:     "http_request",
		Method:   "POST",
		Target:   "https://example.test/" + uid,
		Metadata: map[string]any{"import_action_uid": uid, "execution_blockers": []string{sample.HARRequestBodyNotExportedCode}},
	}
}

// positionalExcludeDraft is a HAR draft whose source mappings, protected
// credential and parameterization all name the action after a blocked one by
// its position.
func positionalExcludeDraft(t *testing.T) *dsl.Script {
	t.Helper()
	script := &dsl.Script{
		Name:     "Positional_Exclude",
		Version:  "1.0.0",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{
			{Name: "home", Type: "http_request", Method: "GET", Target: "https://example.test/home"},
			blockedHARAction("save", "uid-save"),
			{
				Name:    "orders",
				Type:    "http_request",
				Method:  "GET",
				Target:  "https://example.test/orders?customer_id=42",
				Headers: map[string]string{"X-Tenant-Ref": "{{secret.API_TOKEN}}"},
			},
		},
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Source: "har", Preserved: map[string]any{
			"source_review": map[string]any{
				"source_files": []any{map[string]any{"path": "capture.har", "content": "{}", "original_bytes": 2}},
				"mappings": []any{
					map[string]any{"id": "m-home", "source_file": "capture.har", "start_line": 1, "target_path": "actions[0]", "target_kind": "action", "status": "converted"},
					map[string]any{"id": "m-save", "source_file": "capture.har", "start_line": 2, "target_path": "actions[1]", "target_kind": "action", "status": "converted"},
					map[string]any{"id": "m-orders", "source_file": "capture.har", "start_line": 3, "target_path": "actions[2].extractors[0]", "target_kind": "extractor", "status": "converted"},
				},
			},
		}}},
	}
	raw, err := importreview.ScriptAsJSONValue(script)
	if err != nil {
		t.Fatal(err)
	}
	locations := importreview.FindProtectedImportCredentialLocations(raw, "{{secret.API_TOKEN}}")
	if len(locations) != 1 || !strings.HasPrefix(locations[0].Path, "/actions/2/") {
		t.Fatalf("fixture credential locations = %#v, want one under /actions/2/", locations)
	}
	importreview.SetProtectedImportCredentialDescriptors(script, []importreview.ProtectedImportCredentialDescriptor{{
		ID:            "cred-api-token",
		Label:         "API key",
		Context:       "header",
		SuggestedName: "API_TOKEN",
		Replacement:   "{{secret.API_TOKEN}}",
		AffectedPaths: []string{"entry-3", locations[0].Path},
		Locations:     locations,
	}})
	return script
}

func TestExcludeImportDraftActionsRemapsPositionalReferences(t *testing.T) {
	server := NewServer(ServerConfig{})
	draft := positionalExcludeDraft(t)
	staleIDs := map[string]bool{}
	for _, suggestion := range buildImportParameterization(cloneScriptDefinition(draft), nil).Suggestions {
		staleIDs[suggestion.ID] = true
	}

	resp, envelope := postExcludeImportDraftActions(t, server, map[string]any{"script": draft, "action_uids": []string{"uid-save"}})
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.Code, resp.Body.String())
	}
	var script dsl.Script
	if err := json.Unmarshal(envelope["script"], &script); err != nil {
		t.Fatal(err)
	}
	if len(script.Actions) != 2 || script.Actions[1].Name != "orders" {
		t.Fatalf("actions = %#v, want home then orders", script.Actions)
	}

	t.Run("source mappings follow their actions", func(t *testing.T) {
		review := sourceReviewValue(script.Metadata.Import.Preserved["source_review"])
		if review == nil {
			t.Fatal("source_review missing from the edited draft")
		}
		byID := map[string]map[string]string{}
		for _, mapping := range review.Mappings {
			byID[mapping.ID] = map[string]string{"path": mapping.TargetPath, "status": mapping.Status, "notes": mapping.Notes}
		}
		if got := byID["m-home"]["path"]; got != "actions[0]" {
			t.Errorf("m-home target = %q, want actions[0]", got)
		}
		if got := byID["m-orders"]["path"]; got != "actions[1].extractors[0]" {
			t.Errorf("m-orders target = %q, want actions[1].extractors[0]", got)
		}
		if got := byID["m-save"]; got["path"] != "" || got["status"] != "review_only" || got["notes"] != excludedSourceMappingNote {
			t.Errorf("m-save = %#v, want no target and a review_only note", got)
		}
	})

	t.Run("protected credential locations follow their action", func(t *testing.T) {
		descriptors, err := importreview.LoadProtectedImportCredentialDescriptors(&script)
		if err != nil || len(descriptors) != 1 {
			t.Fatalf("descriptors = %#v, %v; want the API_TOKEN descriptor", descriptors, err)
		}
		descriptor := descriptors[0]
		if len(descriptor.Locations) != 1 || !strings.HasPrefix(descriptor.Locations[0].Path, "/actions/1/") {
			t.Fatalf("locations = %#v, want one under /actions/1/", descriptor.Locations)
		}
		for _, path := range descriptor.AffectedPaths {
			if strings.HasPrefix(path, "/actions/2/") {
				t.Fatalf("affected paths %v still name the old position", descriptor.AffectedPaths)
			}
		}
		var requirements []SecretBindingRequirement
		if err := json.Unmarshal(envelope["secret_binding_requirements"], &requirements); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, requirement := range requirements {
			if requirement.ID == "cred-api-token" {
				found = strings.HasPrefix(requirement.Path, "/actions/1/")
			}
		}
		if !found {
			t.Fatalf("secret_binding_requirements = %#v, want cred-api-token at its new location", requirements)
		}
	})

	t.Run("parameterization is re-derived for the new positions", func(t *testing.T) {
		var parameterization paramize.Result
		if err := json.Unmarshal(envelope["parameterization"], &parameterization); err != nil {
			t.Fatalf("parameterization: %v (%s)", err, envelope["parameterization"])
		}
		selected := ""
		for _, suggestion := range parameterization.Suggestions {
			if suggestion.Kind == paramize.KindDataParam && suggestion.VarName == "customer_id" {
				selected = suggestion.ID
			}
		}
		if selected == "" {
			t.Fatalf("no customer_id suggestion in %#v", parameterization.Suggestions)
		}
		if staleIDs[selected] {
			t.Fatalf("suggestion %q kept its pre-exclusion ID; the test would not show a refresh", selected)
		}
		applyResp, _ := postParameterizeApply(t, server, importParameterizeApplyRequest{Script: &script, SelectedIDs: []string{selected}})
		if applyResp.Code != http.StatusOK {
			t.Fatalf("apply refreshed suggestion status = %d: %s", applyResp.Code, applyResp.Body.String())
		}
	})
}

func TestExcludeImportDraftActionsKeepsConverterWarningsAndAddsNoFidelityReport(t *testing.T) {
	server := NewServer(ServerConfig{})
	draft := &dsl.Script{
		Name:     "No_Report",
		Version:  "1.0.0",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{
			{Name: "home", Type: "http_request", Method: "GET", Target: "https://example.test/home"},
			blockedHARAction("first", "uid-first"),
			blockedHARAction("second", "uid-second"),
			// An invalid step gives the draft a static validation issue.
			{Name: "broken", Type: "http_request", Method: "GET"},
		},
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Source: "har", Preserved: map[string]any{"source_zip_entry": "capture.har"}}},
	}

	// Excluding one blocker while another remains is when a readiness
	// evaluation reports the draft blocked.
	resp, envelope := postExcludeImportDraftActions(t, server, map[string]any{"script": draft, "action_uids": []string{"uid-first"}})
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.Code, resp.Body.String())
	}
	var script dsl.Script
	if err := json.Unmarshal(envelope["script"], &script); err != nil {
		t.Fatal(err)
	}
	if _, has := script.Metadata.Import.Preserved["fidelity_report"]; has {
		t.Fatalf("exclusion added a fidelity_report the HAR draft never had: %#v", script.Metadata.Import.Preserved["fidelity_report"])
	}
	if _, has := envelope["warnings"]; has {
		t.Fatalf("response carries warnings %s; validation issues must not replace the import's converter warnings", envelope["warnings"])
	}
	var issues []string
	if err := json.Unmarshal(envelope["validation_issues"], &issues); err != nil || len(issues) == 0 {
		t.Fatalf("validation_issues = %s, want the broken step's issue", envelope["validation_issues"])
	}

	t.Run("a draft that had a report keeps a refreshed one", func(t *testing.T) {
		withReport := cloneScriptDefinition(draft)
		withReport.Metadata.Import.Preserved["fidelity_report"] = map[string]any{"promotion_status": "blocked", "blockers": []any{"uid-first at actions[1]"}}
		resp, envelope := postExcludeImportDraftActions(t, server, map[string]any{"script": withReport, "action_uids": []string{"uid-first"}})
		if resp.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", resp.Code, resp.Body.String())
		}
		var script dsl.Script
		if err := json.Unmarshal(envelope["script"], &script); err != nil {
			t.Fatal(err)
		}
		report, _ := script.Metadata.Import.Preserved["fidelity_report"].(map[string]any)
		if report == nil || report["promotion_status"] == nil {
			t.Fatalf("fidelity_report = %#v, want the existing report recomputed", script.Metadata.Import.Preserved["fidelity_report"])
		}
	})
}

func TestExcludeImportDraftActionsReadsLegacyBlockerAndParallelBranches(t *testing.T) {
	server := NewServer(ServerConfig{})
	draft := &dsl.Script{
		Name:     "Legacy_And_Parallel",
		Version:  "1.0.0",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{
			{Name: "home", Type: "http_request", Method: "GET", Target: "https://example.test/home"},
			{
				Name:     "legacy gate",
				Type:     "http_request",
				Method:   "GET",
				Target:   "https://example.test/legacy",
				Metadata: map[string]any{"import_action_uid": "uid-legacy", "execution_blocker": "import_review_required"},
			},
			{
				Name: "parallel",
				Type: "control_parallel",
				Control: &dsl.ControlFlow{Branches: []dsl.ParallelBranch{
					{Name: "left", Steps: []dsl.Action{blockedHARAction("left blocked", "uid-left")}},
					{Name: "right", Steps: []dsl.Action{
						{Name: "right ok", Type: "http_request", Method: "GET", Target: "https://example.test/right"},
						blockedHARAction("right blocked", "uid-right"),
					}},
				}},
			},
			{
				// The code only appears inside another blocker's name.
				Name:     "lookalike",
				Type:     "http_request",
				Method:   "GET",
				Target:   "https://example.test/lookalike",
				Metadata: map[string]any{"execution_blockers": []string{sample.HARRequestBodyNotExportedCode + "_elsewhere"}},
			},
		},
	}

	resp, envelope := postExcludeImportDraftActions(t, server, map[string]any{
		"script":                    draft,
		"action_uids":               []string{"uid-legacy"},
		"exclude_unexported_bodies": true,
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.Code, resp.Body.String())
	}
	var removed int
	if err := json.Unmarshal(envelope["removed_count"], &removed); err != nil || removed != 3 {
		t.Fatalf("removed_count = %s, want 3 (legacy gate and both parallel blockers)", envelope["removed_count"])
	}
	var script dsl.Script
	if err := json.Unmarshal(envelope["script"], &script); err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(script.Actions))
	for _, action := range script.Actions {
		names = append(names, action.Name)
	}
	if strings.Join(names, ",") != "home,parallel,lookalike" {
		t.Fatalf("actions = %v, want home,parallel,lookalike", names)
	}
	branches := script.Actions[1].Control.Branches
	if len(branches) != 1 || branches[0].Name != "right" || len(branches[0].Steps) != 1 || branches[0].Steps[0].Name != "right ok" {
		t.Fatalf("parallel branches = %#v, want only right with its unblocked step", branches)
	}
}

// TestExcludeImportDraftActionsKeepsRetainedCredentialRequirements covers a
// credential-preserving HAR draft: the exclusion edits the exact server-owned
// draft, whose retained credentials are literals, yet must report the same
// binding requirements the import did, from the masked projection.
func TestExcludeImportDraftActionsKeepsRetainedCredentialRequirements(t *testing.T) {
	server, _, _, projectID := newProjectSecretAPIServer(t)
	settings := server.runner.GetSystemSettings()
	settings.ScriptImports.AllowSourceSecretMigration = true
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatal(err)
	}
	const bearer = "Bearer synthetic-retained-token-value-123456"
	entry := func(method, path, extraHeader, postData string) string {
		return `{"startedDateTime":"2026-01-01T00:00:00.000Z","request":{"method":"` + method + `","url":"https://api.example.test/` + path + `","httpVersion":"HTTP/1.1",` +
			`"headers":[{"name":"Authorization","value":"` + bearer + `"}` + extraHeader + `]` + postData + `},` +
			`"response":{"status":200,"httpVersion":"HTTP/1.1","headers":[{"name":"Content-Type","value":"application/json"}],"content":{"mimeType":"application/json","text":"{\"ok\":true}"}}}`
	}
	harJSON := `{"log":{"version":"1.2","creator":{"name":"synthetic","version":"1"},"entries":[` +
		entry("GET", "home", "", "") + "," +
		entry("POST", "save", `,{"name":"Content-Length","value":"42"},{"name":"Content-Type","value":"application/json"}`, "") + "," +
		entry("GET", "after", "", "") + `]}}`

	payload, err := json.Marshal(map[string]any{"name": "Retained Exclude", "project_id": projectID, "content": harJSON})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/har?project_id="+projectID, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("import status = %d: %s", resp.Code, resp.Body.String())
	}
	var imported struct {
		Script                    json.RawMessage            `json:"script"`
		SecretBindingRequirements []SecretBindingRequirement `json:"secret_binding_requirements"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &imported); err != nil {
		t.Fatal(err)
	}
	if len(imported.SecretBindingRequirements) == 0 {
		t.Fatalf("import listed no secret binding requirements; the fixture must retain a credential: %s", resp.Body.String())
	}

	excludeResp, envelope := postExcludeImportDraftActions(t, server, map[string]any{
		"script":                    imported.Script,
		"exclude_unexported_bodies": true,
	})
	if excludeResp.Code != http.StatusOK {
		t.Fatalf("exclude status = %d: %s", excludeResp.Code, excludeResp.Body.String())
	}
	var requirements []SecretBindingRequirement
	if err := json.Unmarshal(envelope["secret_binding_requirements"], &requirements); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, requirement := range requirements {
		names[requirement.SuggestedName] = true
	}
	for _, requirement := range imported.SecretBindingRequirements {
		// The excluded POST's own credential goes with it.
		if strings.Contains(requirement.Context, "POST save") {
			if names[requirement.SuggestedName] {
				t.Fatalf("requirement %q of the excluded action survived", requirement.SuggestedName)
			}
			continue
		}
		if !names[requirement.SuggestedName] {
			t.Fatalf("after exclusion requirements = %#v, want %q the import listed %#v", requirements, requirement.SuggestedName, imported.SecretBindingRequirements)
		}
	}
}
