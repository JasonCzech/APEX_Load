package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestExtractProjectedRedactions(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		projected string
		want      []string
		ok        bool
	}{
		{name: "whole scalar", raw: "literal-password", projected: maskedScriptSecret, want: []string{"literal-password"}, ok: true},
		{name: "sentinel-like credential", raw: "prefix" + maskedScriptSecret + "suffix", projected: maskedScriptSecret, want: []string{"prefix" + maskedScriptSecret + "suffix"}, ok: true},
		{name: "credential equals sentinel", raw: maskedScriptSecret, projected: maskedScriptSecret, want: []string{maskedScriptSecret}, ok: true},
		{name: "bearer credential equals sentinel", raw: "Bearer " + maskedScriptSecret, projected: "Bearer " + maskedScriptSecret, want: []string{maskedScriptSecret}, ok: true},
		{name: "bearer wrapper", raw: "Bearer literal-token", projected: "Bearer " + maskedScriptSecret, want: []string{"literal-token"}, ok: true},
		{name: "multiple values", raw: `user=alice&password=s3cr3t`, projected: `user=` + maskedScriptSecret + `&password=` + maskedScriptSecret, want: []string{"alice", "s3cr3t"}, ok: true},
		{name: "credential contains projected delimiter", raw: `Bearer first; x=embedded; x=second`, projected: `Bearer ` + maskedScriptSecret + `; x=` + maskedScriptSecret, ok: false},
		{name: "adjacent values are ambiguous", raw: "alicepassword", projected: maskedScriptSecret + maskedScriptSecret, ok: false},
		{name: "unaligned", raw: "abc", projected: "Bearer " + maskedScriptSecret, ok: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := extractProjectedRedactions(test.raw, test.projected)
			if ok != test.ok {
				t.Fatalf("ok = %t, want %t (values=%v)", ok, test.ok, got)
			}
			if len(got) != len(test.want) {
				t.Fatalf("values = %v, want %v", got, test.want)
			}
			for index := range test.want {
				if got[index] != test.want[index] {
					t.Fatalf("values[%d] = %q, want %q", index, got[index], test.want[index])
				}
			}
		})
	}
}

func TestSourceSecretValuesRejectSentinelLikeCredentials(t *testing.T) {
	credentialValue := "prefix" + maskedScriptSecret + "suffix"
	raw := &dsl.Script{
		Name: "Sentinel source", Protocol: "http", ProjectID: "project-a",
		Config: dsl.ScriptConfig{
			HTTP: dsl.HTTPConfig{
				Auth: dsl.AuthConfig{Type: "basic", Password: credentialValue},
			},
		},
		Actions: []dsl.Action{{Name: "request", Type: "request", Target: "/"}},
	}
	projected := maskScriptSecrets(raw)
	requirements := discoverSecretBindingRequirements(projected, 0)
	if len(requirements) != 1 {
		t.Fatalf("requirements = %#v, want one password", requirements)
	}
	values := sourceSecretValuesForImportNodes(
		importResponseRawScriptValue(raw),
		importResponseRawScriptValue(projected),
		requirements,
	)
	if got := values[requirements[0].ID]; got != "" || len(values) != 0 {
		t.Fatalf("source credential = %q (%#v), want unavailable sentinel-like value", got, values)
	}
}

func TestPreservedBindingReviewRetainsCredentialEqualToSentinel(t *testing.T) {
	raw := &dsl.Script{
		Name: "Sentinel password", Protocol: "http", ProjectID: "project-a",
		Config: dsl.ScriptConfig{
			BaseURL: "https://example.test",
			HTTP: dsl.HTTPConfig{
				Auth: dsl.AuthConfig{Type: "basic", Username: "operator", Password: maskedScriptSecret},
			},
		},
		Actions: []dsl.Action{{Name: "request", Type: "request", Method: "GET", Target: "/"}},
	}

	resolved, requirements, err := applyPreservedRedactedSecretBindings(raw, 0, nil)
	if err != nil {
		t.Fatalf("apply preserved bindings: %v", err)
	}
	if resolved == nil || resolved.Config.HTTP.Auth.Password != maskedScriptSecret {
		t.Fatalf("resolved password = %#v, want exact authored sentinel", resolved)
	}
	if len(requirements) != 1 || !requirements[0].DestinationValuePreserved {
		t.Fatalf("requirements = %#v, want exact destination value preserved", requirements)
	}
}

func TestAmbiguousProjectedRedactionsAreUnavailableAcrossImportMigrationPaths(t *testing.T) {
	raw := &dsl.Script{
		Name: "Ambiguous source", Protocol: "http", ProjectID: "project-a",
		Config:  dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{Name: "request", Type: "request", Target: "/", Body: `Bearer first; x=embedded; x=second`}},
	}
	masked := cloneScriptDefinition(raw)
	masked.Actions[0].Body = `Bearer ` + maskedScriptSecret + `; x=` + maskedScriptSecret
	requirements := []SecretBindingRequirement{
		{ID: "first", ScriptIndex: 0, Path: "/actions/0/body", Occurrence: 0},
		{ID: "second", ScriptIndex: 0, Path: "/actions/0/body", Occurrence: 1},
	}

	rawValue := importResponseRawScriptValue(raw)
	maskedValue := importResponseRawScriptValue(masked)
	if values := sourceSecretValuesForImportNodes(rawValue, maskedValue, requirements); len(values) != 0 {
		t.Fatalf("generic import migration guessed ambiguous values: %#v", values)
	}
	if values := sourceValuesForMaskedRequirements(raw, masked, requirements); len(values) != 0 {
		t.Fatalf("Model Existing migration guessed ambiguous values: %#v", values)
	}
}

func TestFormattedJSONBodySourceSecretsRemainAvailableAcrossMigrationPaths(t *testing.T) {
	raw := &dsl.Script{
		Name: "Formatted JSON source", Protocol: "http", ProjectID: "project-a",
		Config: dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{
			Name:   "request",
			Type:   "request",
			Target: "/",
			Body: "{\n" +
				"  \"z_note\": \"ordinary\",\n" +
				"  \"password\": \"source-secret\",\n" +
				"  \"nested\": {\"api_key\": \"source-key\"}\n" +
				"}",
		}},
	}
	masked := maskScriptSecrets(raw)
	if masked == nil || masked.Actions[0].Body == raw.Actions[0].Body {
		t.Fatalf("expected structural projection to canonicalize and redact the body: %#v", masked)
	}
	requirements := discoverSecretBindingRequirements(masked, 0)
	if len(requirements) != 2 {
		t.Fatalf("requirements = %#v, want password and api-key requirements", requirements)
	}

	assertValues := func(label string, values map[string]string) {
		t.Helper()
		got := make(map[string]string, len(values))
		for _, requirement := range requirements {
			switch {
			case strings.HasSuffix(requirement.SuggestedName, "API_KEY"):
				got["API_KEY"] = values[requirement.ID]
			case strings.HasSuffix(requirement.SuggestedName, "PASSWORD"):
				got["PASSWORD"] = values[requirement.ID]
			}
		}
		if got["API_KEY"] != "source-key" || got["PASSWORD"] != "source-secret" {
			t.Fatalf("%s values = %#v, want structurally matched source credentials", label, got)
		}
	}
	assertValues(
		"uploaded import",
		sourceSecretValuesForImportNodes(importResponseRawScriptValue(raw), importResponseRawScriptValue(masked), requirements),
	)
	assertValues("Model Existing", sourceValuesForMaskedRequirements(raw, masked, requirements))
}

func TestSourceSecretValuesForImportResponseUsesRequirementIDs(t *testing.T) {
	script := &dsl.Script{
		ID:        "source-secret-script",
		Name:      "Source secret",
		Protocol:  "http",
		ProjectID: "project-a",
		Config: dsl.ScriptConfig{
			BaseURL: "https://example.test",
			HTTP: dsl.HTTPConfig{
				Auth: dsl.AuthConfig{Type: "basic", Password: "literal-password"},
			},
		},
		Actions: []dsl.Action{{
			Name:    "request",
			Type:    "request",
			Target:  "/",
			Headers: map[string]string{"Authorization": "Bearer literal-token"},
		}},
	}
	response := newScriptImportResponse(scriptImportResponseOptions{Script: script})
	requirements := response["secret_binding_requirements"].([]SecretBindingRequirement)
	values := sourceSecretValuesForImportResponse(scriptImportResponseOptions{Script: script}, requirements)
	if len(values) != 2 {
		t.Fatalf("values = %#v, want two requirement-bound values", values)
	}
	for _, requirement := range requirements {
		if values[requirement.ID] == "" {
			t.Fatalf("requirement %q has no source value: requirements=%#v values=%#v", requirement.ID, requirements, values)
		}
	}
	if got := sourceSecretProjectID(scriptImportResponseOptions{Script: script}); got != "project-a" {
		t.Fatalf("project id = %q, want project-a", got)
	}
}

func TestSourceSecretProjectUsesAuthorizedImportScopeWhenDraftOmitsProject(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/har?project_id=project-query", nil)
	bindAuthorizedProjectToRequest(request, "project-authorized")
	script := &dsl.Script{
		Name:     "Scoped import",
		Protocol: "http",
		Config: dsl.ScriptConfig{
			HTTP: dsl.HTTPConfig{Auth: dsl.AuthConfig{Password: "source-password"}},
		},
	}
	projectID, matches := sourceSecretProjectIDForRequest(request, scriptImportResponseOptions{Script: script})
	if !matches || projectID != "project-authorized" {
		t.Fatalf("source secret project = %q, matches=%t; want authorized scope", projectID, matches)
	}

	script.ProjectID = "project-other"
	if projectID, matches = sourceSecretProjectIDForRequest(request, scriptImportResponseOptions{Script: script}); matches || projectID != "project-authorized" {
		t.Fatalf("mismatched source project = %q, matches=%t; want migration session suppressed", projectID, matches)
	}
}

func TestSourceSecretValuesRejectLiteralRedactionSentinelCredential(t *testing.T) {
	script := &dsl.Script{
		Name:      "Already redacted",
		Protocol:  "http",
		ProjectID: "project-a",
		Config: dsl.ScriptConfig{
			BaseURL: "https://example.test",
			HTTP: dsl.HTTPConfig{
				Auth: dsl.AuthConfig{Type: "basic", Password: maskedScriptSecret},
			},
		},
		Actions: []dsl.Action{{Name: "request", Type: "request", Target: "/"}},
	}
	response := newScriptImportResponse(scriptImportResponseOptions{Script: script})
	requirements := response["secret_binding_requirements"].([]SecretBindingRequirement)
	values := sourceSecretValuesForImportResponse(
		scriptImportResponseOptions{Script: script},
		requirements,
	)
	if len(values) != 0 {
		t.Fatalf("literal sentinel credential values = %#v, want none", values)
	}
	if len(requirements) != 1 || requirements[0].DestinationValuePreserved ||
		requirements[0].PreservationUnavailableReason != "redacted_source_value" {
		t.Fatalf("literal sentinel requirement = %#v, want explicit unavailable reason", requirements)
	}
}

func TestImportResponseSourceSecretSessionIsPolicyGatedAndKeepsOpaqueCredential(t *testing.T) {
	server, _, _, projectID := newProjectSecretAPIServer(t)
	script := &dsl.Script{
		Name:      "Protected import",
		Protocol:  "http",
		ProjectID: projectID,
		Config: dsl.ScriptConfig{
			BaseURL: "https://example.test",
			HTTP: dsl.HTTPConfig{
				Auth: dsl.AuthConfig{Type: "basic", Password: "must-never-reach-the-browser"},
			},
		},
		Actions: []dsl.Action{{Name: "request", Type: "request", Target: "/"}},
	}
	request := httptest.NewRequest("POST", "/api/v1/scripts/import/sample", nil)

	disabled := server.newScriptImportResponseWithSourceSecrets(request, scriptImportResponseOptions{Script: script})
	if _, exists := disabled["source_secret_session_id"]; exists {
		t.Fatal("default-off policy exposed a source-secret session")
	}

	settings := server.runner.GetSystemSettings()
	settings.ScriptImports.AllowSourceSecretMigration = true
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatal(err)
	}
	enabled := server.newScriptImportResponseWithSourceSecrets(request, scriptImportResponseOptions{Script: script})
	sessionID, _ := enabled["source_secret_session_id"].(string)
	if strings.TrimSpace(sessionID) == "" {
		t.Fatalf("enabled response has no source-secret session: %#v", enabled)
	}
	if allowed, _ := enabled["source_secret_migration_allowed"].(bool); !allowed {
		t.Fatalf("source migration capability = %#v", enabled["source_secret_migration_allowed"])
	}
	requirements, _ := enabled["secret_binding_requirements"].([]SecretBindingRequirement)
	if len(requirements) != 1 || !requirements[0].SourceValueAvailable {
		t.Fatalf("requirements = %#v, want one source-available requirement", requirements)
	}
	scriptValue, _ := enabled["script"].(map[string]any)
	encodedScript, err := json.Marshal(scriptValue)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encodedScript), "must-never-reach-the-browser") {
		t.Fatalf("import draft lost the credential: %s", encodedScript)
	}
	encodedRequirements, err := json.Marshal(requirements)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encodedRequirements), "must-never-reach-the-browser") {
		t.Fatalf("credential review metadata disclosed the source value: %s", encodedRequirements)
	}
}

func TestUploadedImportSourceSecretMigratesServerSideWithoutReentry(t *testing.T) {
	server, _, managed, projectID := newProjectSecretAPIServer(t)
	settings := server.runner.GetSystemSettings()
	settings.ScriptImports.AllowSourceSecretMigration = true
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatal(err)
	}
	script := &dsl.Script{
		Name:      "Uploaded protected import",
		Protocol:  "http",
		ProjectID: projectID,
		Config: dsl.ScriptConfig{
			BaseURL: "https://example.test",
			HTTP: dsl.HTTPConfig{
				Auth: dsl.AuthConfig{Type: "basic", Password: "uploaded-source-password"},
			},
		},
		Actions: []dsl.Action{{Name: "request", Type: "request", Target: "/"}},
	}
	request := httptest.NewRequest("POST", "/api/v1/scripts/import/sample", nil)
	response := server.newScriptImportResponseWithSourceSecrets(request, scriptImportResponseOptions{Script: script})
	sessionID, _ := response["source_secret_session_id"].(string)
	requirements, _ := response["secret_binding_requirements"].([]SecretBindingRequirement)
	if sessionID == "" || len(requirements) != 1 || !requirements[0].SourceValueAvailable {
		t.Fatalf("uploaded source-secret response = %#v", response)
	}
	encodedScript, err := json.Marshal(response["script"])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encodedScript), "uploaded-source-password") || strings.Contains(string(encodedScript), maskedScriptSecret) {
		t.Fatalf("import response did not preserve source value: %s", encodedScript)
	}
	encodedRequirements, err := json.Marshal(requirements)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encodedRequirements), "uploaded-source-password") {
		t.Fatalf("uploaded import review disclosed source value: %s", encodedRequirements)
	}

	migration := secretAPIRequest(t, server, "POST", "/api/v1/scripts/import/secrets/migrate", map[string]any{
		"project_id":               projectID,
		"source_secret_session_id": sessionID,
		"secrets": []map[string]string{{
			"requirement_id": requirements[0].ID,
			"secret_name":    "UPLOADED_PASSWORD",
		}},
	})
	if migration.Code != 201 || strings.Contains(migration.Body.String(), "uploaded-source-password") {
		t.Fatalf("uploaded migration = %d %s", migration.Code, migration.Body.String())
	}
	value, _, err := managed.Get(context.Background(), projectID, "UPLOADED_PASSWORD")
	if err != nil || value != "uploaded-source-password" {
		t.Fatalf("uploaded migrated value = %q, %v", value, err)
	}
}

func TestMultiScriptImportKeepsDistinctSourceCredentialsIndependentlyMigratable(t *testing.T) {
	server, _, managed, projectID := newProjectSecretAPIServer(t)
	settings := server.runner.GetSystemSettings()
	settings.ScriptImports.AllowSourceSecretMigration = true
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatal(err)
	}
	script := func(id, value string) *dsl.Script {
		return &dsl.Script{
			ID: id, Name: "Login flow", Protocol: "http", ProjectID: projectID,
			Config: dsl.ScriptConfig{BaseURL: "https://example.test"},
			Actions: []dsl.Action{{
				Name:    "Login",
				Type:    "request",
				Target:  "/login",
				Headers: map[string]string{"Authorization": "Bearer " + value},
			}},
		}
	}
	firstValue := "first-script-source-token"
	secondValue := "second-script-source-token"
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/sample", nil)
	response := server.newScriptImportResponseWithSourceSecrets(request, scriptImportResponseOptions{
		Scripts: []*dsl.Script{script("first", firstValue), script("second", secondValue)},
	})
	sessionID, _ := response["source_secret_session_id"].(string)
	requirements, _ := response["secret_binding_requirements"].([]SecretBindingRequirement)
	if sessionID == "" || len(requirements) != 2 {
		t.Fatalf("multi-script source-secret response = %#v", response)
	}
	if requirements[0].SuggestedName == requirements[1].SuggestedName ||
		requirements[0].ScriptIndex == requirements[1].ScriptIndex ||
		!requirements[0].SourceValueAvailable || !requirements[1].SourceValueAvailable {
		t.Fatalf("multi-script requirements were conflated: %#v", requirements)
	}

	selections := make([]map[string]string, 0, len(requirements))
	for _, requirement := range requirements {
		selections = append(selections, map[string]string{
			"requirement_id": requirement.ID,
			"secret_name":    requirement.SuggestedName,
		})
	}
	migration := secretAPIRequest(t, server, http.MethodPost, "/api/v1/scripts/import/secrets/migrate", map[string]any{
		"project_id":               projectID,
		"source_secret_session_id": sessionID,
		"secrets":                  selections,
	})
	if migration.Code != http.StatusCreated ||
		strings.Contains(migration.Body.String(), firstValue) ||
		strings.Contains(migration.Body.String(), secondValue) {
		t.Fatalf("multi-script migration = %d %s", migration.Code, migration.Body.String())
	}
	for _, requirement := range requirements {
		want := firstValue
		if requirement.ScriptIndex == 1 {
			want = secondValue
		}
		got, _, err := managed.Get(context.Background(), projectID, requirement.SuggestedName)
		if err != nil || got != want {
			t.Fatalf("script %d migrated value = %q, %v; want distinct source credential", requirement.ScriptIndex, got, err)
		}
	}
}

func TestCredentialValueGroupsRequireEqualRecoverableServerValues(t *testing.T) {
	raw := map[string]any{"name": "grouping", "protocol": "http", "config": map[string]any{}, "actions": []any{
		map[string]any{"headers": map[string]any{"Cookie": "same", "Authorization": "different"}},
		map[string]any{"headers": map[string]any{"Cookie": "same", "Authorization": "[REDACTED]"}},
	}}
	projected := map[string]any{"name": "grouping", "protocol": "http", "config": map[string]any{}, "actions": []any{
		map[string]any{"headers": map[string]any{"Cookie": maskedScriptSecret, "Authorization": maskedScriptSecret}},
		map[string]any{"headers": map[string]any{"Cookie": maskedScriptSecret, "Authorization": maskedScriptSecret}},
	}}
	reqs := []SecretBindingRequirement{
		{ID: "a", Path: "/actions/0/headers/Cookie"}, {ID: "b", Path: "/actions/1/headers/Cookie"},
		{ID: "c", Path: "/actions/0/headers/Authorization"}, {ID: "d", Path: "/actions/1/headers/Authorization", ValueGroupID: "untrusted"},
	}
	got := annotateSecretBindingDestinationValues(reqs, raw, projected)
	if got[0].ValueGroupID != "a" || got[1].ValueGroupID != "a" || got[2].ValueGroupID != "c" || got[3].ValueGroupID != "" {
		t.Fatalf("incorrect value grouping: %#v", got)
	}
	if got[3].DestinationValuePreserved {
		t.Fatal("unavailable value grouped as preserved")
	}
	reqs[1].ScriptIndex = 1
	got = annotateSecretBindingDestinationValues(reqs, []any{raw, raw}, []any{projected, projected})
	if got[0].ValueGroupID == got[1].ValueGroupID {
		t.Fatal("group crossed script boundary")
	}
}

// Recorded provenance (a HAR's response cookies in action metadata) is never
// sent and cannot be rebound, so its requirement keeps the recorded value even
// when the masked value does not align; an executable field does not.
func TestProvenanceCredentialRequirementsKeepRecordedValues(t *testing.T) {
	raw := map[string]any{"name": "har", "protocol": "http", "config": map[string]any{}, "actions": []any{
		map[string]any{
			"headers": map[string]any{"Authorization": "[REDACTED]"},
			"metadata": map[string]any{"har.response.cookies": []any{
				map[string]any{"value": "abc.1.0.1.def"},
				map[string]any{"value": "session-cookie-7712"},
				map[string]any{"value": "session-cookie-7712"},
			}},
		},
	}}
	projected := map[string]any{"name": "har", "protocol": "http", "config": map[string]any{}, "actions": []any{
		map[string]any{
			"headers": map[string]any{"Authorization": maskedScriptSecret},
			"metadata": map[string]any{"har.response.cookies": []any{
				map[string]any{"value": maskedScriptSecret + ".1.0.1." + maskedScriptSecret},
				map[string]any{"value": maskedScriptSecret},
				map[string]any{"value": maskedScriptSecret},
			}},
		},
	}}
	reqs := []SecretBindingRequirement{
		{ID: "cookie", Path: "/actions/0/metadata/har.response.cookies/0/value", Occurrence: 1},
		{ID: "script", Path: "/metadata/import/source_note"},
		{ID: "auth", Path: "/actions/0/headers/Authorization"},
		{ID: "same-1", Path: "/actions/0/metadata/har.response.cookies/1/value"},
		{ID: "same-2", Path: "/actions/0/metadata/har.response.cookies/2/value"},
	}
	got := annotateSecretBindingDestinationValues(reqs, raw, projected)
	if !got[0].DestinationValuePreserved || !got[1].DestinationValuePreserved || got[0].PreservationUnavailableReason != "" {
		t.Fatalf("provenance requirements = %#v, want their recorded values kept", got[:2])
	}
	if got[2].DestinationValuePreserved {
		t.Fatal("an unavailable executable credential was marked kept")
	}
	// Equal recorded values still group, so the review lists them once.
	if got[3].ValueGroupID == "" || got[3].ValueGroupID != got[4].ValueGroupID || !got[3].DestinationValuePreserved {
		t.Fatalf("equal provenance values = %#v, want one kept group", got[3:])
	}
	if !secretBindingInProvenance("/teardown/3/metadata") || secretBindingInProvenance("/actions/0/params/metadata/key") {
		t.Fatal("provenance is only an action's or the script's own metadata")
	}
}

func TestPreservedCredentialGroupsRetainBatchIndex(t *testing.T) {
	script := &dsl.Script{Name: "batch second", Protocol: "http", Actions: []dsl.Action{
		{Name: "first", Type: "request", Headers: map[string]string{"Authorization": "Bearer literal-token"}},
		{Name: "second", Type: "request", Headers: map[string]string{"Authorization": "Bearer literal-token"}},
	}}
	_, reqs, err := applyPreservedRedactedSecretBindings(script, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(reqs) != 2 {
		t.Fatalf("requirements = %d", len(reqs))
	}
	for _, req := range reqs {
		if req.ScriptIndex != 2 || !req.DestinationValuePreserved || req.ValueGroupID == "" {
			t.Fatalf("lost batch preservation: %#v", req)
		}
	}
	if reqs[0].ValueGroupID != reqs[1].ValueGroupID {
		t.Fatal("equal values not grouped")
	}
}
