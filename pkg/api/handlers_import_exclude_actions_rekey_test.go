package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/sharedvalue"
)

const excludeRekeyBearerToken = "synthetic-rekey-token-value-123456"

// importExcludeRekeyHAR imports a HAR whose middle POST was exported without
// its body, so its exclusion moves the request after it from actions[2] to
// actions[1]. Every request carries the same bearer token, host and client header.
func importExcludeRekeyHAR(t *testing.T, server *Server, projectID string) map[string]json.RawMessage {
	t.Helper()
	entry := func(method, path, extraHeader string) string {
		return `{"startedDateTime":"2026-01-01T00:00:00.000Z","request":{"method":"` + method + `","url":"https://api.example.test/` + path + `?tenant=acme-tenant-42","httpVersion":"HTTP/1.1",` +
			`"headers":[{"name":"Authorization","value":"Bearer ` + excludeRekeyBearerToken + `"},{"name":"X-Client-Ref","value":"client-ref-alpha-7"}` + extraHeader + `]},` +
			`"response":{"status":200,"httpVersion":"HTTP/1.1","headers":[{"name":"Content-Type","value":"application/json"}],"content":{"mimeType":"application/json","text":"{\"ok\":true}"}}}`
	}
	harJSON := `{"log":{"version":"1.2","creator":{"name":"synthetic","version":"1"},"entries":[` +
		entry("GET", "home", "") + "," +
		entry("POST", "save", `,{"name":"Content-Length","value":"42"},{"name":"Content-Type","value":"application/json"}`) + "," +
		entry("GET", "after", "") + `]}}`
	payload, err := json.Marshal(map[string]any{"name": "Rekey Exclude", "project_id": projectID, "content": harJSON})
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
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(resp.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope
}

func newExcludeRekeyServer(t *testing.T) (*Server, string, func(name string) string) {
	t.Helper()
	server, _, managed, projectID := newProjectSecretAPIServer(t)
	settings := server.runner.GetSystemSettings()
	settings.ScriptImports.AllowSourceSecretMigration = true
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatal(err)
	}
	read := func(name string) string {
		value, _, err := managed.Get(context.Background(), projectID, name)
		if err != nil {
			return ""
		}
		return value
	}
	return server, projectID, read
}

// requirementForAction returns the bearer requirement of the action whose
// context names it.
func requirementForAction(requirements []SecretBindingRequirement, action string) (SecretBindingRequirement, bool) {
	for _, requirement := range requirements {
		if strings.Contains(requirement.Context, action) {
			return requirement, true
		}
	}
	return SecretBindingRequirement{}, false
}

func TestExcludeImportDraftActionsRekeysSourceSecretSessionForMovedCredentials(t *testing.T) {
	server, projectID, readSecret := newExcludeRekeyServer(t)
	imported := importExcludeRekeyHAR(t, server, projectID)
	var importSessionID string
	var importRequirements []SecretBindingRequirement
	if err := json.Unmarshal(imported["source_secret_session_id"], &importSessionID); err != nil || importSessionID == "" {
		t.Fatalf("import returned no source-secret session (%v): %s", err, imported["source_secret_session_id"])
	}
	if err := json.Unmarshal(imported["secret_binding_requirements"], &importRequirements); err != nil {
		t.Fatal(err)
	}
	before, ok := requirementForAction(importRequirements, "after")
	if !ok || !before.SourceValueAvailable || !strings.HasPrefix(before.Path, "/actions/2/") {
		t.Fatalf("import requirements = %#v, want the after request's credential at /actions/2 with a recorded value", importRequirements)
	}

	resp, envelope := postExcludeImportDraftActions(t, server, map[string]any{
		"script":                    imported["script"],
		"exclude_unexported_bodies": true,
		"source_secret_session_id":  importSessionID,
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("exclude status = %d: %s", resp.Code, resp.Body.String())
	}
	if strings.Contains(string(envelope["secret_binding_requirements"]), excludeRekeyBearerToken) {
		t.Fatalf("exclude requirements disclosed the recorded credential")
	}
	var newSessionID string
	if err := json.Unmarshal(envelope["source_secret_session_id"], &newSessionID); err != nil || newSessionID == "" || newSessionID == importSessionID {
		t.Fatalf("exclude source_secret_session_id = %s (%v), want a new session", envelope["source_secret_session_id"], err)
	}
	var requirements []SecretBindingRequirement
	if err := json.Unmarshal(envelope["secret_binding_requirements"], &requirements); err != nil {
		t.Fatal(err)
	}
	after, ok := requirementForAction(requirements, "after")
	if !ok || !strings.HasPrefix(after.Path, "/actions/1/") {
		t.Fatalf("exclude requirements = %#v, want the after request's credential moved to /actions/1", requirements)
	}
	if after.ID == before.ID {
		t.Fatalf("moved requirement kept ID %q; the fixture must shift the credential's ID", after.ID)
	}
	if !after.SourceValueAvailable {
		t.Fatalf("moved requirement %#v lost its recorded value", after)
	}
	if _, excluded := requirementForAction(requirements, "save"); excluded {
		t.Fatalf("excluded request's credential survived: %#v", requirements)
	}

	// The consumed import session no longer migrates anything.
	stale := secretAPIRequest(t, server, http.MethodPost, "/api/v1/scripts/import/secrets/migrate", map[string]any{
		"project_id":               projectID,
		"source_secret_session_id": importSessionID,
		"secrets":                  []map[string]string{{"requirement_id": before.ID, "secret_name": "STALE_TOKEN"}},
	})
	if stale.Code == http.StatusCreated {
		t.Fatalf("consumed import session still migrated: %s", stale.Body.String())
	}
	migration := secretAPIRequest(t, server, http.MethodPost, "/api/v1/scripts/import/secrets/migrate", map[string]any{
		"project_id":               projectID,
		"source_secret_session_id": newSessionID,
		"secrets":                  []map[string]string{{"requirement_id": after.ID, "secret_name": "MOVED_TOKEN"}},
	})
	if migration.Code != http.StatusCreated {
		t.Fatalf("migration through the re-keyed session = %d %s", migration.Code, migration.Body.String())
	}
	if value := readSecret("MOVED_TOKEN"); !strings.Contains(value, excludeRekeyBearerToken) {
		t.Fatalf("migrated value does not hold the recorded token")
	}
}

func TestExcludeImportDraftActionsWithoutSessionLeavesSessionFieldOut(t *testing.T) {
	server, projectID, _ := newExcludeRekeyServer(t)
	imported := importExcludeRekeyHAR(t, server, projectID)
	resp, envelope := postExcludeImportDraftActions(t, server, map[string]any{
		"script":                    imported["script"],
		"exclude_unexported_bodies": true,
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("exclude status = %d: %s", resp.Code, resp.Body.String())
	}
	if _, present := envelope["source_secret_session_id"]; present {
		t.Fatalf("exclude without a session returned source_secret_session_id %s", envelope["source_secret_session_id"])
	}
}

func TestExcludeImportDraftActionsReturnsRefreshedSharedValues(t *testing.T) {
	server, projectID, _ := newExcludeRekeyServer(t)
	imported := importExcludeRekeyHAR(t, server, projectID)
	var importShared sharedvalue.Result
	if err := json.Unmarshal(imported["shared_values"], &importShared); err != nil {
		t.Fatal(err)
	}
	// The header candidate's locations (not serialized) include the request
	// after the excluded one, so its ID changes with the exclusion.
	var stale *sharedvalue.Candidate
	for index := range importShared.Candidates {
		if importShared.Candidates[index].Name == "X_Client_Ref" {
			stale = &importShared.Candidates[index]
		}
	}
	if stale == nil {
		t.Fatalf("import shared values %s have no X-Client-Ref candidate; the fixture must offer one", imported["shared_values"])
	}

	resp, envelope := postExcludeImportDraftActions(t, server, map[string]any{
		"script":                    imported["script"],
		"exclude_unexported_bodies": true,
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("exclude status = %d: %s", resp.Code, resp.Body.String())
	}
	var refreshed sharedvalue.Result
	if raw, present := envelope["shared_values"]; !present || json.Unmarshal(raw, &refreshed) != nil {
		t.Fatalf("exclude response shared_values = %s, want the re-derived candidates", envelope["shared_values"])
	}
	var current *sharedvalue.Candidate
	for index := range refreshed.Candidates {
		if refreshed.Candidates[index].Name == stale.Name && refreshed.Candidates[index].Kind == stale.Kind {
			current = &refreshed.Candidates[index]
		}
	}
	if current == nil || current.ID == stale.ID {
		t.Fatalf("refreshed shared values = %#v, want %q under a new ID", refreshed.Candidates, stale.Name)
	}

	apply := func(id string) *httptest.ResponseRecorder {
		return secretAPIRequest(t, server, http.MethodPost, "/api/v1/scripts/import/shared-values/apply", map[string]any{
			"scripts": []json.RawMessage{envelope["script"]},
			"values":  map[string]string{id: "replacement-value"},
		})
	}
	if got := apply(stale.ID); got.Code != http.StatusConflict {
		t.Fatalf("applying the import's stale candidate = %d %s, want 409", got.Code, got.Body.String())
	}
	if got := apply(current.ID); got.Code != http.StatusOK {
		t.Fatalf("applying the refreshed candidate = %d %s", got.Code, got.Body.String())
	}
}

func TestExcludedImportPointerMoveFollowsNestedActions(t *testing.T) {
	exclusion := &importActionExclusion{paths: map[string]string{
		"actions[1]":                           "",
		"actions[2]":                           "actions[1]",
		"actions[2].params.requests[0]":        "",
		"actions[2].params.requests[3]":        "actions[1].params.requests[2]",
		"actions[4].control.cases[2].steps[1]": "actions[3].control.cases[1].steps[0]",
	}}
	cases := []struct {
		pointer, want string
		removed       bool
	}{
		{"/actions/0/headers/Authorization", "/actions/0/headers/Authorization", false},
		{"/actions/1/headers/Authorization", "", true},
		{"/actions/2/headers/Authorization", "/actions/1/headers/Authorization", false},
		{"/actions/2", "/actions/1", false},
		{"/actions/2/params/requests/0/headers/X", "", true},
		{"/actions/2/params/requests/3/headers/0", "/actions/1/params/requests/2/headers/0", false},
		{"/actions/4/control/cases/2/steps/1/body", "/actions/3/control/cases/1/steps/0/body", false},
		{"/config/http/auth/password", "/config/http/auth/password", false},
	}
	for _, tc := range cases {
		got, removed := excludedImportPointerMove(exclusion, tc.pointer)
		if got != tc.want || removed != tc.removed {
			t.Errorf("excludedImportPointerMove(%q) = %q, %v; want %q, %v", tc.pointer, got, removed, tc.want, tc.removed)
		}
	}
}

func TestExcludedImportRequirementIDMovesFollowsRenumberedSuggestedNames(t *testing.T) {
	// Three credentials share a base suggested name, so the registry numbered
	// them in traversal order. Excluding the first renumbers the survivors.
	exclusion := &importActionExclusion{paths: map[string]string{
		"actions[0]": "",
		"actions[1]": "actions[0]",
		"actions[2]": "actions[1]",
		"actions[3]": "actions[2]",
	}}
	requirement := func(id, path, name, context string) SecretBindingRequirement {
		return SecretBindingRequirement{
			ID: id, Path: path, Label: "Authorization header", Context: context,
			SuggestedName: name, ScriptName: "Rekey",
		}
	}
	const shared = `Sensitive request header "Authorization" on action "POST /save".`
	previous := []SecretBindingRequirement{
		requirement("old-0", "/actions/0/headers/Authorization", "POST_save_token", shared),
		requirement("old-1", "/actions/1/headers/Authorization", "POST_save_token_2", shared),
		requirement("old-2", "/actions/2/headers/Authorization", "POST_save_token_3", shared),
		requirement("old-3", "/actions/3/headers/Authorization", "GET_home_token", `Action "GET /home"`),
	}
	current := []SecretBindingRequirement{
		requirement("new-1", "/actions/0/headers/Authorization", "POST_save_token", shared),
		requirement("new-2", "/actions/1/headers/Authorization", "POST_save_token_2", shared),
		// A different credential now sits where old-3 moved: never re-keyed to it.
		requirement("new-3", "/actions/2/headers/Authorization", "GET_home_token", `Action "GET /other"`),
	}
	moves := excludedImportRequirementIDMoves(exclusion, previous, current)
	want := map[string]string{"old-0": "", "old-1": "new-1", "old-2": "new-2", "old-3": ""}
	for id, wantID := range want {
		if got, tracked := moves[id]; !tracked || got != wantID {
			t.Errorf("moves[%q] = %q (tracked %v), want %q", id, got, tracked, wantID)
		}
	}
}

func TestAllocatedSecretNameBase(t *testing.T) {
	for name, want := range map[string]string{
		"TOKEN":      "TOKEN",
		"TOKEN_2":    "TOKEN",
		"TOKEN_12":   "TOKEN",
		"API_TOKEN":  "API_TOKEN",
		"TOKEN_":     "TOKEN_",
		"_2":         "_2",
		"V2_TOKEN_3": "V2_TOKEN",
		"TOKEN_2a":   "TOKEN_2a",
	} {
		if got := allocatedSecretNameBase(name); got != want {
			t.Errorf("allocatedSecretNameBase(%q) = %q, want %q", name, got, want)
		}
	}
}
