package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

// The disclosure projection re-encodes a form field whose value is JSON, so a
// form body that also carries a plain password cannot be aligned byte for
// byte with its recorded value. Swapping the password for a project secret
// would change the other field's bytes, which the binding route refuses. The
// review must say so up front, keep the value as recorded, and let the draft
// save.
const unalignedFormJSONField = `data={"api_key":"k-synthetic-0123456789abcdef"}`

func unalignedFormCredentialScript(password string) *dsl.Script {
	return &dsl.Script{
		Name: "Unaligned form", Version: "1.0.0", Protocol: "http", ProjectID: storage.DefaultProjectID,
		Config:   dsl.ScriptConfig{BaseURL: "https://example.test"},
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Source: "har", Preserved: map[string]any{}}},
		Actions: []dsl.Action{
			{Name: "Open login", Type: "request", Method: "GET", Target: "/login"},
			{
				Name: "Submit login", Type: "request", Method: "POST", Target: "/login",
				Headers: map[string]string{"Content-Type": "application/x-www-form-urlencoded"},
				Body:    "password=" + password + "&" + unalignedFormJSONField,
			},
		},
	}
}

func singleBodyRequirement(t *testing.T, requirements []SecretBindingRequirement) SecretBindingRequirement {
	t.Helper()
	var found []SecretBindingRequirement
	for _, requirement := range requirements {
		if requirement.Path == "/actions/1/body" {
			found = append(found, requirement)
		}
	}
	if len(found) != 1 {
		t.Fatalf("body requirements = %#v, want exactly one", requirements)
	}
	return found[0]
}

func TestUnalignedCredentialIsKeptAsRecordedAndNotOfferedForRebinding(t *testing.T) {
	const password = "synthetic-form-password-4d21"
	script := unalignedFormCredentialScript(password)
	response := newScriptImportResponse(scriptImportResponseOptions{Script: script})
	requirement := singleBodyRequirement(t, response["secret_binding_requirements"].([]SecretBindingRequirement))
	if !requirement.DestinationValuePreserved || requirement.PreservationUnavailableReason != "" {
		t.Fatalf("requirement = %#v, want the recorded value kept", requirement)
	}
	if requirement.RebindUnavailableReason != secretBindingRebindUnavailableReason ||
		requirement.RebindUnavailableDescription == "" {
		t.Fatalf("requirement = %#v, want rebinding reported unavailable", requirement)
	}

	// Saving without a binding keeps every recorded byte.
	resolved, remaining, err := applyCredentialPreservingSecretBindings(script, 0, nil)
	if err != nil {
		t.Fatalf("keep recorded value: %v", err)
	}
	if got := resolved.Actions[1].Body; got != script.Actions[1].Body {
		t.Fatalf("body = %q, want recorded bytes", got)
	}
	if kept := singleBodyRequirement(t, remaining); kept.RebindUnavailableReason == "" || !kept.DestinationValuePreserved {
		t.Fatalf("remaining requirement = %#v, want it still kept and not rebindable", kept)
	}
	committed, err := replaceUnavailableImportCredentialValues(script)
	if err != nil {
		t.Fatalf("commit replacement: %v", err)
	}
	if got := committed.Actions[1].Body; got != script.Actions[1].Body {
		t.Fatalf("committed body = %q, want recorded bytes", got)
	}
}

func TestUnalignedRebindRefusalNamesTheActionAndField(t *testing.T) {
	script := unalignedFormCredentialScript("synthetic-form-password-7c90")
	response := newScriptImportResponse(scriptImportResponseOptions{Script: script})
	requirement := singleBodyRequirement(t, response["secret_binding_requirements"].([]SecretBindingRequirement))
	_, _, err := applyCredentialPreservingSecretBindings(script, 0, []secretBindingSelection{{
		RequirementID: requirement.ID,
		SecretName:    "LOGIN_PASSWORD",
	}})
	var refusal *secretBindingRebindUnavailableError
	if !errors.As(err, &refusal) {
		t.Fatalf("err = %v, want a rebind refusal", err)
	}
	if refusal.requirement.ID != requirement.ID {
		t.Fatalf("refusal names %q, want %q", refusal.requirement.ID, requirement.ID)
	}
	want := `Request body in action 2 ("Submit login") can't use a project secret`
	if !strings.HasPrefix(err.Error(), want) {
		t.Fatalf("message = %q, want prefix %q", err.Error(), want)
	}
}

func TestSecretBindingLocationNamesActionsReadably(t *testing.T) {
	root := map[string]any{
		"init":     []any{map[string]any{"name": "Warm up"}},
		"actions":  []any{map[string]any{"name": ""}, map[string]any{"name": "Pay " + maskedScriptSecret}},
		"teardown": []any{map[string]any{"name": "Sign out"}},
	}
	for path, want := range map[string]string{
		"/init/0/headers/Authorization":                  `init action 1 ("Warm up")`,
		"/actions/0/body":                                `action 1`,
		"/actions/1/control/steps/3/params/requests/0/x": `action 2, step 4, request 1`,
		"/actions/0/control/else/2/body":                 `action 1, else step 3`,
		"/actions/0/control/cases/1/steps/0/target":      `action 1, case 2, step 1`,
		"/actions/0/headers/steps":                       `action 1`,
		"/teardown/0/body":                               `teardown action 1 ("Sign out")`,
		"/config/http/auth/password":                     `the script configuration`,
		"/parameters/0/value":                            `a script parameter`,
		"/data_sources/0/rows/0/1":                       `a data source`,
		"/metadata/import/x":                             `the script`,
	} {
		if got := secretBindingLocation(root, path); got != want {
			t.Errorf("secretBindingLocation(%q) = %q, want %q", path, got, want)
		}
	}
}

// A field that only aligns structurally (formatted or reordered JSON) can
// keep its value but cannot take a spliced project-secret reference; one whose
// structurally aligned value is a recorded placeholder can do neither.
func TestStructurallyAlignedCredentialIsNotOfferedForRebinding(t *testing.T) {
	scriptNode := func(body string) []any {
		return []any{map[string]any{
			"protocol": "http", "config": map[string]any{},
			"actions": []any{map[string]any{"name": "Login", "body": body}},
		}}
	}
	for _, test := range []struct {
		name          string
		recorded      string
		wantPreserved bool
		wantReason    string
	}{
		{name: "real value", recorded: "synthetic-structural-password-2c4d", wantPreserved: true},
		{name: "recorded placeholder", recorded: maskedScriptSecret, wantReason: "redacted_source_value"},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw := scriptNode("{\n  \"user\": \"alice\",\n  \"password\": \"" + test.recorded + "\"\n}")
			projected := scriptNode(`{"password":"` + maskedScriptSecret + `","user":"alice"}`)
			requirements := annotateSecretBindingDestinationValues([]SecretBindingRequirement{{
				ID: "password", Path: "/actions/0/body",
			}}, raw, projected)
			got := requirements[0]
			if got.DestinationValuePreserved != test.wantPreserved || got.PreservationUnavailableReason != test.wantReason {
				t.Fatalf("requirement = %#v, want preserved=%t reason=%q", got, test.wantPreserved, test.wantReason)
			}
			if got.RebindUnavailableReason != secretBindingRebindUnavailableReason {
				t.Fatalf("requirement = %#v, want rebinding reported unavailable", got)
			}
		})
	}
}

// Unaligned fields cannot isolate an occurrence's bytes. A recorded
// placeholder elsewhere in the field must not mark a real credential as
// having no usable value: the projection masking more than the recording
// held means it replaced at least one real value.
func TestUnalignedFieldWithUnrelatedPlaceholderTextKeepsItsCredential(t *testing.T) {
	script := unalignedFormCredentialScript("synthetic-form-password-3f8a")
	script.Actions[1].Body += "&note=" + maskedScriptSecret
	response := newScriptImportResponse(scriptImportResponseOptions{Script: script})
	var password *SecretBindingRequirement
	for _, requirement := range response["secret_binding_requirements"].([]SecretBindingRequirement) {
		if requirement.Path == "/actions/1/body" && requirement.Occurrence == 0 {
			password = &requirement
		}
	}
	if password == nil || !password.DestinationValuePreserved || password.PreservationUnavailableReason != "" ||
		password.RebindUnavailableReason == "" {
		t.Fatalf("password requirement = %#v, want the real credential kept and not rebindable", password)
	}
}

// A field that is itself a redaction placeholder has no usable value, and
// the same misalignment rules out a project secret. Neither is offered, and
// committing the draft keeps the placeholder rather than failing the save.
func TestUnalignedPlaceholderCanBeNeitherKeptNorReboundButStillCommits(t *testing.T) {
	script := unalignedFormCredentialScript(maskedScriptSecret)
	response := newScriptImportResponse(scriptImportResponseOptions{Script: script})
	requirement := singleBodyRequirement(t, response["secret_binding_requirements"].([]SecretBindingRequirement))
	if requirement.DestinationValuePreserved || requirement.PreservationUnavailableReason != "redacted_source_value" {
		t.Fatalf("requirement = %#v, want preservation unavailable for a placeholder", requirement)
	}
	if requirement.RebindUnavailableReason == "" {
		t.Fatalf("requirement = %#v, want rebinding reported unavailable", requirement)
	}
	committed, err := replaceUnavailableImportCredentialValues(script)
	if err != nil {
		t.Fatalf("commit replacement: %v", err)
	}
	if got := committed.Actions[1].Body; got != script.Actions[1].Body {
		t.Fatalf("committed body = %q, want recorded bytes", got)
	}
}

// Formatted JSON keeps its layout through projection, so it still aligns
// byte for byte and stays rebindable.
func TestAlignedCredentialStaysRebindable(t *testing.T) {
	script := unalignedFormCredentialScript("synthetic-form-password-1a2b")
	script.Actions[1].Headers = map[string]string{"Content-Type": "application/json"}
	script.Actions[1].Body = "{\n  \"note\": \"x\",\n  \"password\": \"synthetic-json-password-5e6f\"\n}"
	response := newScriptImportResponse(scriptImportResponseOptions{Script: script})
	requirement := singleBodyRequirement(t, response["secret_binding_requirements"].([]SecretBindingRequirement))
	if requirement.RebindUnavailableReason != "" || !requirement.DestinationValuePreserved {
		t.Fatalf("requirement = %#v, want preserved and rebindable", requirement)
	}
	resolved, _, err := applyCredentialPreservingSecretBindings(script, 0, []secretBindingSelection{{
		RequirementID: requirement.ID,
		SecretName:    "LOGIN_PASSWORD",
	}})
	if err != nil {
		t.Fatalf("rebind: %v", err)
	}
	if want := "{\n  \"note\": \"x\",\n  \"password\": \"{{secret.LOGIN_PASSWORD}}\"\n}"; resolved.Actions[1].Body != want {
		t.Fatalf("body = %q, want %q", resolved.Actions[1].Body, want)
	}
}

// End to end: the import response flags the requirement, the binding route
// refuses a rebind with a readable location instead of a scrubbed JSON
// Pointer, and the batch save keeps the recorded value.
func TestUnalignedCredentialImportDraftSavesThroughTheAPI(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "unaligned-credential.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store})
	const password = "synthetic-form-password-api-9b3e"
	script := unalignedFormCredentialScript(password)
	preview := server.newScriptImportResponseWithSourceSecrets(
		httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/har", nil),
		scriptImportResponseOptions{Script: script, Scripts: []*dsl.Script{script}},
	)
	requirement := singleBodyRequirement(t, preview["secret_binding_requirements"].([]SecretBindingRequirement))
	if requirement.RebindUnavailableReason == "" || !requirement.DestinationValuePreserved {
		t.Fatalf("requirement = %#v, want kept as recorded and not rebindable", requirement)
	}
	masked, ok := preview["scripts"].([]any)
	if !ok || len(masked) != 1 {
		t.Fatalf("preview scripts = %#v", preview["scripts"])
	}

	refused := postSecretBindingJSON(t, server, "/api/v1/scripts/secret-bindings/apply", map[string]any{
		"script":   masked[0],
		"bindings": []map[string]string{{"requirement_id": requirement.ID, "secret_name": "LOGIN_PASSWORD"}},
	})
	if refused.Code != http.StatusBadRequest {
		t.Fatalf("apply status = %d body=%s", refused.Code, refused.Body.String())
	}
	var refusal struct {
		Error         string `json:"error"`
		Code          string `json:"code"`
		RequirementID string `json:"requirement_id"`
		Path          string `json:"path"`
	}
	if err := json.Unmarshal(refused.Body.Bytes(), &refusal); err != nil {
		t.Fatal(err)
	}
	var refreshed struct {
		Requirements []SecretBindingRequirement `json:"secret_binding_requirements"`
	}
	if err := json.Unmarshal(refused.Body.Bytes(), &refreshed); err != nil {
		t.Fatal(err)
	}
	if got := singleBodyRequirement(t, refreshed.Requirements); got.ID != requirement.ID || got.RebindUnavailableReason == "" {
		t.Fatalf("refreshed requirement = %#v, want the refused one flagged", got)
	}
	if refusal.Code != secretBindingRebindUnavailableCode || refusal.RequirementID != requirement.ID ||
		refusal.Path != "/actions/1/body" ||
		!strings.Contains(refusal.Error, `Request body in action 2 ("Submit login")`) ||
		strings.Contains(refusal.Error, maskedScriptSecret) {
		t.Fatalf("refusal = %#v, want a readable location", refusal)
	}

	created := postGenericImportBatch(t, server, map[string]any{
		"operation_id":   "unaligned-credential-save",
		"source_profile": "har",
		"scripts":        masked,
	})
	if created.Code != http.StatusCreated {
		t.Fatalf("save status = %d body=%s", created.Code, created.Body.String())
	}
	var saved struct {
		Scripts []StoredScript `json:"scripts"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.Scripts) != 1 {
		t.Fatalf("saved = %s", created.Body.String())
	}
	stored, ok := server.runner.GetScript(saved.Scripts[0].ID)
	if !ok || stored == nil || stored.Script == nil {
		t.Fatal("saved script is unavailable")
	}
	if got := stored.Script.Actions[1].Body; !bytes.Equal([]byte(got), []byte(script.Actions[1].Body)) {
		t.Fatalf("saved body = %q, want recorded bytes", got)
	}
}
