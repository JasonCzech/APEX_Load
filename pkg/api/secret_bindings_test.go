package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestProtectedImportCredentialBindingRewritesDecodedBase64Body(t *testing.T) {
	requirement, replacement, err := credentialpolicy.NewRequirement(credentialpolicy.RequirementSpec{
		Key:      "password",
		Label:    "Password",
		Context:  "body",
		NameHint: "login_password",
		Source:   credentialpolicy.SourceRef{Format: "fixture", Path: "login.json"},
		Targets: []credentialpolicy.TargetRef{{
			RequestKey: "login", Field: "json", Pointer: "/password",
		}},
		Value: "protected-source-value",
		Mode:  credentialpolicy.SecretReference,
	})
	if err != nil {
		t.Fatal(err)
	}
	wireBody := `{"password":"` + replacement + `","note":"unchanged"}`
	script := &dsl.Script{
		ID: "base64-binding", Name: "Base64 binding", Version: "1", Protocol: "http",
		Actions: []dsl.Action{{
			Name: "login", Type: "request", Method: "POST", Target: "/login",
			Body: base64.StdEncoding.EncodeToString([]byte(wireBody)), BodyEncoding: "base64",
		}},
	}
	public, err := attachProtectedImportCredentialReview(script, []credentialpolicy.Requirement{requirement})
	if err != nil {
		t.Fatalf("attachProtectedImportCredentialReview: %v", err)
	}
	if len(public) != 1 || public[0].Path != "/actions/0/body" {
		t.Fatalf("public requirements = %#v", public)
	}
	descriptors, err := loadProtectedImportCredentialDescriptors(script)
	if err != nil {
		t.Fatal(err)
	}
	if len(descriptors) != 1 || len(descriptors[0].Locations) != 1 ||
		descriptors[0].Locations[0].Encoding != "base64" {
		t.Fatalf("protected descriptors = %#v", descriptors)
	}

	resolved, legacy, remaining, err := applyProtectedImportCredentialBindings(script, []secretBindingSelection{{
		RequirementID: requirement.ID,
		SecretName:    "CUSTOM_LOGIN_PASSWORD",
	}})
	if err != nil {
		t.Fatalf("applyProtectedImportCredentialBindings: %v", err)
	}
	if len(legacy) != 0 || len(remaining) != 0 {
		t.Fatalf("binding leftovers = legacy %#v remaining %#v", legacy, remaining)
	}
	decoded, err := base64.StdEncoding.DecodeString(resolved.Actions[0].Body)
	if err != nil {
		t.Fatal(err)
	}
	wantWire := `{"password":"{{secret.CUSTOM_LOGIN_PASSWORD}}","note":"unchanged"}`
	if string(decoded) != wantWire || resolved.Actions[0].BodyEncoding != "base64" {
		t.Fatalf("resolved wire body = %q (%q), want %q base64", decoded, resolved.Actions[0].BodyEncoding, wantWire)
	}
}

func redactedBindingTestScript() *dsl.Script {
	return &dsl.Script{
		ID:       "redacted-import",
		Name:     "Imported login",
		Version:  "1",
		Protocol: "http",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "har",
			Preserved: map[string]any{
				"source_review": map[string]any{"authorization": "Bearer [REDACTED]"},
			},
		}},
		Config: dsl.ScriptConfig{
			BaseURL: "https://example.test",
			HTTP: dsl.HTTPConfig{Auth: dsl.AuthConfig{
				Type:     "basic",
				Password: maskedScriptSecret,
			}},
		},
		Actions: []dsl.Action{
			{
				Name:    "Login",
				Type:    "request",
				Method:  http.MethodPost,
				Target:  "/login?api_key=" + maskedScriptSecret,
				Headers: map[string]string{"Authorization": "Bearer " + maskedScriptSecret},
				Body:    `{"password":"[REDACTED]","token":"[REDACTED]"}`,
			},
			{
				Name:   "Review source",
				Type:   "source_review",
				Target: "review",
				Metadata: map[string]any{
					"original_authorization": "Bearer [REDACTED]",
				},
			},
		},
	}
}

func TestDiscoverSecretBindingRequirementsFindsExecutableValuesOnly(t *testing.T) {
	script := redactedBindingTestScript()
	script.Actions[0].Metadata = map[string]any{
		"original_url":       "https://csi-bau.it.att.com:[REDACTED].jws",
		"source_transaction": "[REDACTED]",
		"asyncapi_channel":   "[REDACTED]",
	}
	first := discoverSecretBindingRequirements(script, 3)
	second := discoverSecretBindingRequirements(script, 3)
	if len(first) != 5 {
		t.Fatalf("requirements = %d, want 5: %#v", len(first), first)
	}
	if len(second) != len(first) {
		t.Fatalf("second requirements = %d, want %d", len(second), len(first))
	}

	seenIDs := map[string]bool{}
	seenPaths := map[string]int{}
	for index, requirement := range first {
		if requirement.ID == "" || requirement.ID != second[index].ID {
			t.Errorf("requirement %d id = %q, repeated id = %q", index, requirement.ID, second[index].ID)
		}
		if seenIDs[requirement.ID] {
			t.Errorf("duplicate requirement id %q", requirement.ID)
		}
		seenIDs[requirement.ID] = true
		seenPaths[requirement.Path]++
		if requirement.ScriptIndex != 3 || requirement.ScriptID != script.ID || requirement.ScriptName != script.Name {
			t.Errorf("requirement scope = %+v", requirement)
		}
		if requirement.SuggestedName == "" || !strings.Contains(requirement.Replacement, "{{secret.") {
			t.Errorf("requirement binding guidance = %+v", requirement)
		}
		if strings.HasPrefix(requirement.Path, "/metadata") || requirement.Path == "/actions/1/metadata/original_authorization" {
			t.Errorf("non-executable evidence produced a requirement: %+v", requirement)
		}
	}
	if seenPaths["/actions/0/body"] != 2 {
		t.Errorf("body requirements = %d, want 2", seenPaths["/actions/0/body"])
	}
	if seenPaths["/actions/0/headers/Authorization"] != 1 || seenPaths["/actions/0/target"] != 1 || seenPaths["/config/http/auth/password"] != 1 {
		t.Fatalf("requirement paths = %#v", seenPaths)
	}
	if seenPaths["/actions/0/metadata/original_url"] != 0 ||
		seenPaths["/actions/0/metadata/source_transaction"] != 0 ||
		seenPaths["/actions/0/metadata/asyncapi_channel"] != 0 {
		t.Fatalf("action metadata incorrectly produced requirements: %#v", seenPaths)
	}

	header := findSecretBindingRequirement(t, first, "/actions/0/headers/Authorization", 0)
	if header.Label != "Authorization header" || header.Context != `Action "Login"` {
		t.Errorf("header guidance = %+v", header)
	}
	if !strings.HasPrefix(header.Replacement, "Bearer {{secret.") {
		t.Errorf("header replacement = %q, want Bearer wrapper preserved", header.Replacement)
	}
}

func TestAsyncAPIActionMetadataIsNonExecutableForBindingAndSentinelChecks(t *testing.T) {
	script := &dsl.Script{
		Name: "AsyncAPI provenance", Version: "1", Protocol: "kafka",
		Actions: []dsl.Action{{
			Name: "publish", Type: "kafka_produce", Target: "orders",
			Metadata: map[string]any{
				"asyncapi_channel":        maskedScriptSecret,
				"asyncapi_correlation_id": "prefix-" + maskedScriptSecret,
			},
		}},
	}
	if requirements := discoverSecretBindingRequirements(script, 0); len(requirements) != 0 {
		t.Fatalf("AsyncAPI provenance created secret requirements: %#v", requirements)
	}
	projected, err := scriptAsJSONValue(script)
	if err != nil {
		t.Fatal(err)
	}
	if jsonValueContainsExecutableMaskedSecret(projected, "") {
		t.Fatal("AsyncAPI provenance was treated as an executable orphan sentinel")
	}

	script.Actions[0].Body = maskedScriptSecret
	projected, err = scriptAsJSONValue(script)
	if err != nil {
		t.Fatal(err)
	}
	if !jsonValueContainsExecutableMaskedSecret(projected, "") {
		t.Fatal("executable Kafka body sentinel was incorrectly exempted")
	}
}

func TestImportBatchSecretSuggestionsDoNotImplicitlyReuseCredentials(t *testing.T) {
	script := func(id, existingReference string) map[string]any {
		raw, err := scriptAsJSONValue(&dsl.Script{
			ID: id, Name: "Login flow", Protocol: "http",
			Config: dsl.ScriptConfig{
				BaseURL: "https://example.test",
				HTTP:    dsl.HTTPConfig{Auth: dsl.AuthConfig{Type: "bearer", Token: existingReference}},
			},
			Actions: []dsl.Action{{
				Name:    "Login",
				Type:    "request",
				Target:  "/login",
				Headers: map[string]string{"Authorization": "Bearer " + maskedScriptSecret},
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return raw.(map[string]any)
	}

	requirements := secretBindingRequirementsForImportScripts([]any{
		script("first", "{{secret.login-token}}"),
		script("second", ""),
	})
	if len(requirements) != 2 {
		t.Fatalf("requirements = %#v, want two redacted credentials", requirements)
	}
	if requirements[0].SuggestedName != "LOGIN_TOKEN_2" || requirements[1].SuggestedName != "LOGIN_TOKEN_3" {
		t.Fatalf("suggested names = %q, %q; want existing canonical name and batch suggestions reserved",
			requirements[0].SuggestedName, requirements[1].SuggestedName)
	}
	if requirements[0].SuggestedName == requirements[1].SuggestedName ||
		!strings.Contains(requirements[0].Replacement, "{{secret.LOGIN_TOKEN_2}}") ||
		!strings.Contains(requirements[1].Replacement, "{{secret.LOGIN_TOKEN_3}}") {
		t.Fatalf("batch requirements implicitly reused a credential: %#v", requirements)
	}
}

func TestDiscoverSecretBindingRequirementsSkipsIdentityAndReviewOnlyActions(t *testing.T) {
	folderID := maskedScriptSecret
	script := &dsl.Script{
		ID: maskedScriptSecret, Name: maskedScriptSecret, Version: maskedScriptSecret, Protocol: maskedScriptSecret,
		ProjectID: maskedScriptSecret, FolderID: &folderID, GeneratedBy: maskedScriptSecret, GeneratedFrom: maskedScriptSecret,
		Config: dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{
			{Name: "Source review", Type: "source_review", Target: maskedScriptSecret, Body: maskedScriptSecret, Params: map[string]any{"args": []any{maskedScriptSecret}}},
			{Name: "Required review", Type: "review_required", Target: maskedScriptSecret, Value: maskedScriptSecret, Metadata: map[string]any{"source": maskedScriptSecret}},
			{Name: maskedScriptSecret, Type: "request", Target: "/execute?token=" + maskedScriptSecret},
		},
	}
	requirements := discoverSecretBindingRequirements(script, 0)
	if len(requirements) != 1 || requirements[0].Path != "/actions/2/target" {
		t.Fatalf("requirements = %#v, want only executable request target", requirements)
	}
}

func TestSecretBindingRequirementIDsBindProjectedContentAndActionContext(t *testing.T) {
	base := &dsl.Script{
		ID: "scope-a", Name: "Scoped import", Protocol: "http",
		Config: dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{
			{Name: "First", Type: "request", Target: "/first", Headers: map[string]string{"Authorization": "Bearer " + maskedScriptSecret}},
			{Name: "Second", Type: "request", Target: "/second", Headers: map[string]string{"Authorization": "Bearer " + maskedScriptSecret}},
		},
	}
	baseID := findSecretBindingRequirement(t, discoverSecretBindingRequirements(base, 2), "/actions/0/headers/Authorization", 0).ID

	tests := []struct {
		name   string
		mutate func(*dsl.Script)
	}{
		{name: "projected source", mutate: func(script *dsl.Script) { script.Actions[0].Headers["Authorization"] = "Basic " + maskedScriptSecret }},
		{name: "action name", mutate: func(script *dsl.Script) { script.Actions[0].Name = "Renamed" }},
		{name: "action type", mutate: func(script *dsl.Script) { script.Actions[0].Type = "custom_call" }},
		{name: "script scope", mutate: func(script *dsl.Script) { script.ID = "scope-b" }},
		{name: "action reorder", mutate: func(script *dsl.Script) { script.Actions[0], script.Actions[1] = script.Actions[1], script.Actions[0] }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mutated := cloneScriptDefinition(base)
			test.mutate(mutated)
			mutatedID := findSecretBindingRequirement(t, discoverSecretBindingRequirements(mutated, 2), "/actions/0/headers/Authorization", 0).ID
			if mutatedID == baseID {
				t.Fatalf("requirement id remained %q after %s changed", baseID, test.name)
			}
		})
	}
}

func TestApplySecretBindingsUsesRequirementIDsAndExactOccurrences(t *testing.T) {
	script := redactedBindingTestScript()
	requirements := discoverSecretBindingRequirements(script, 4)
	password := findSecretBindingRequirement(t, requirements, "/actions/0/body", 0)
	token := findSecretBindingRequirement(t, requirements, "/actions/0/body", 1)
	header := findSecretBindingRequirement(t, requirements, "/actions/0/headers/Authorization", 0)

	resolved, remaining, err := applySecretBindings(script, 4, []secretBindingSelection{
		{RequirementID: password.ID, SecretName: "LOGIN_PASSWORD"},
		{RequirementID: token.ID, SecretName: "LOGIN_RESPONSE_TOKEN"},
		{RequirementID: header.ID, SecretName: "LOGIN_AUTH_TOKEN"},
	})
	if err != nil {
		t.Fatalf("applySecretBindings: %v", err)
	}
	if resolved.Actions[0].Body != `{"password":"{{secret.LOGIN_PASSWORD}}","token":"{{secret.LOGIN_RESPONSE_TOKEN}}"}` {
		t.Errorf("resolved body = %q", resolved.Actions[0].Body)
	}
	if resolved.Actions[0].Headers["Authorization"] != "Bearer {{secret.LOGIN_AUTH_TOKEN}}" {
		t.Errorf("resolved authorization = %q", resolved.Actions[0].Headers["Authorization"])
	}
	if got := resolved.Metadata.Import.Preserved["source_review"].(map[string]any)["authorization"]; got != "Bearer [REDACTED]" {
		t.Errorf("preserved source evidence = %#v", got)
	}
	if len(remaining) != 2 {
		t.Fatalf("remaining requirements = %d, want config password and target: %#v", len(remaining), remaining)
	}

	for _, invalidName := range []string{"not a valid name", "FOO/BAR", "FOO@BAR"} {
		if _, _, err := applySecretBindings(script, 4, []secretBindingSelection{{RequirementID: password.ID, SecretName: invalidName}}); err == nil {
			t.Errorf("invalid logical secret name %q was accepted", invalidName)
		}
	}
	aliasResolved, _, err := applySecretBindings(script, 4, []secretBindingSelection{{
		RequirementID: password.ID,
		SecretName:    "login.password-key",
	}})
	if err != nil {
		t.Fatalf("compatible alias logical name: %v", err)
	}
	if got := aliasResolved.Actions[0].Body; !strings.Contains(got, "{{secret.LOGIN_PASSWORD_KEY}}") {
		t.Fatalf("compatible alias was not canonicalized in script: %q", got)
	}
	if _, _, err := applySecretBindings(script, 4, []secretBindingSelection{{RequirementID: "secret-binding-unknown", SecretName: "LOGIN_PASSWORD"}}); err == nil {
		t.Fatal("unknown requirement id was accepted")
	}
	validMaxName := strings.Repeat("A", 249)
	maxResolved, _, err := applySecretBindings(script, 4, []secretBindingSelection{{RequirementID: header.ID, SecretName: validMaxName}})
	if err != nil {
		t.Fatalf("249-character logical name: %v", err)
	}
	if got := maxResolved.Actions[0].Headers["Authorization"]; got != "Bearer {{secret."+validMaxName+"}}" {
		t.Fatalf("249-character logical binding was not preserved by response projection: %q", got)
	}
	if _, _, err := applySecretBindings(script, 4, []secretBindingSelection{{RequirementID: header.ID, SecretName: strings.Repeat("A", 250)}}); err == nil {
		t.Fatal("250-character logical secret name was accepted")
	}
}

func TestApplySecretBindingsDiscoversSentinelsFromReturnedProjection(t *testing.T) {
	script := &dsl.Script{
		Name:     "Projection defense",
		Protocol: "http",
		Config: dsl.ScriptConfig{
			BaseURL: "https://example.test",
			HTTP:    dsl.HTTPConfig{Auth: dsl.AuthConfig{Type: "basic", Password: "inline-password"}},
		},
		Actions: []dsl.Action{{
			Name: "Login", Type: "request", Target: "/login",
			Headers: map[string]string{"Authorization": "Bearer " + maskedScriptSecret},
		}},
	}
	requirements := discoverSecretBindingRequirements(script, 0)
	header := findSecretBindingRequirement(t, requirements, "/actions/0/headers/Authorization", 0)
	resolved, remaining, err := applySecretBindings(script, 0, []secretBindingSelection{{RequirementID: header.ID, SecretName: "LOGIN_TOKEN"}})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Config.HTTP.Auth.Password != maskedScriptSecret {
		t.Fatalf("returned inline password = %q, want projected sentinel", resolved.Config.HTTP.Auth.Password)
	}
	password := findSecretBindingRequirement(t, remaining, "/config/http/auth/password", 0)
	if password.ScriptIndex != 0 {
		t.Fatalf("projected requirement = %+v", password)
	}
}

func TestApplySecretBindingsClearsOnlyResolvedCredentialManagementBlockers(t *testing.T) {
	script := &dsl.Script{
		Name:     "Managed credentials",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{
			{
				Name:   "Singular credential blocker",
				Type:   "request",
				Target: "/one",
				Headers: map[string]string{
					"Authorization": "Bearer " + maskedScriptSecret,
				},
				Metadata: map[string]any{
					"execution_blocker":  "import_credentials_unmanaged",
					"execution_blockers": []string{"import_credentials_unmanaged", "import_review_required"},
				},
			},
			{
				Name:   "Mixed blockers",
				Type:   "request",
				Target: "/two?token=" + maskedScriptSecret,
				Metadata: map[string]any{
					"execution_blocker":  "import_missing_variables",
					"execution_blockers": []any{"import_credentials_unmanaged", "import_missing_file_bindings", 7},
					"review_gate":        "source_behavior_review",
				},
			},
		},
	}
	requirements := discoverSecretBindingRequirements(script, 0)
	if len(requirements) != 2 {
		t.Fatalf("requirements = %#v, want two", requirements)
	}
	bindings := make([]secretBindingSelection, 0, len(requirements))
	for index, requirement := range requirements {
		bindings = append(bindings, secretBindingSelection{
			RequirementID: requirement.ID,
			SecretName:    fmt.Sprintf("MANAGED_%d", index+1),
		})
	}
	resolved, remaining, err := applySecretBindings(script, 0, bindings)
	if err != nil {
		t.Fatalf("applySecretBindings: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("remaining requirements = %#v", remaining)
	}

	first := resolved.Actions[0].Metadata
	if _, exists := first["execution_blocker"]; exists {
		t.Fatalf("resolved singular credential blocker remains: %#v", first)
	}
	if got := first["execution_blockers"]; !reflect.DeepEqual(got, []any{"import_review_required"}) {
		t.Fatalf("first execution blockers = %#v", got)
	}

	second := resolved.Actions[1].Metadata
	if got := second["execution_blocker"]; got != "import_missing_variables" {
		t.Fatalf("unrelated singular blocker = %#v", got)
	}
	if got := second["execution_blockers"]; !reflect.DeepEqual(got, []any{"import_missing_file_bindings", float64(7)}) {
		t.Fatalf("mixed execution blockers = %#v", got)
	}
	if got := second["review_gate"]; got != "source_behavior_review" {
		t.Fatalf("review gate = %#v", got)
	}
}

func TestApplySecretBindingsKeepsCredentialBlockerUntilEveryOccurrenceIsGoverned(t *testing.T) {
	script := &dsl.Script{
		Name:     "Partially managed credentials",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{
			Name:   "Request",
			Type:   "request",
			Target: "/resource?token=" + maskedScriptSecret,
			Headers: map[string]string{
				"Authorization": "Bearer " + maskedScriptSecret,
			},
			Metadata: map[string]any{
				"execution_blocker":  "import_credentials_unmanaged",
				"execution_blockers": []string{"import_credentials_unmanaged"},
			},
		}},
	}
	requirements := discoverSecretBindingRequirements(script, 0)
	if len(requirements) != 2 {
		t.Fatalf("requirements = %#v, want two", requirements)
	}
	resolved, remaining, err := applySecretBindings(script, 0, []secretBindingSelection{{
		RequirementID: requirements[0].ID,
		SecretName:    "MANAGED_ONE",
	}})
	if err != nil {
		t.Fatalf("applySecretBindings: %v", err)
	}
	if len(remaining) != 1 {
		t.Fatalf("remaining requirements = %#v, want one", remaining)
	}
	if got := resolved.Actions[0].Metadata["execution_blocker"]; got != "import_credentials_unmanaged" {
		t.Fatalf("credential blocker cleared early: %#v", got)
	}
	if got := resolved.Actions[0].Metadata["execution_blockers"]; !reflect.DeepEqual(got, []any{"import_credentials_unmanaged"}) {
		t.Fatalf("credential blocker list changed early: %#v", got)
	}
}

func TestApplySecretBindingsClearsOnlyTheFullyResolvedActionScope(t *testing.T) {
	script := &dsl.Script{
		Name:     "Scoped credential management",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{
			{
				Name:   "Resolved request",
				Type:   "request",
				Target: "/resolved",
				Headers: map[string]string{
					"Authorization": "Bearer " + maskedScriptSecret,
				},
				Metadata: map[string]any{
					"execution_blocker": "import_credentials_unmanaged",
				},
			},
			{
				Name:   "Unrelated unmanaged request",
				Type:   "request",
				Target: "/unrelated",
				Metadata: map[string]any{
					"execution_blocker":  "import_credentials_unmanaged",
					"execution_blockers": []string{"import_credentials_unmanaged"},
				},
			},
		},
	}
	requirements := discoverSecretBindingRequirements(script, 0)
	if len(requirements) != 1 {
		t.Fatalf("requirements = %#v, want one", requirements)
	}
	resolved, remaining, err := applySecretBindings(script, 0, []secretBindingSelection{{
		RequirementID: requirements[0].ID,
		SecretName:    "SCOPED_TOKEN",
	}})
	if err != nil {
		t.Fatalf("applySecretBindings: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("remaining requirements = %#v", remaining)
	}
	if _, exists := resolved.Actions[0].Metadata["execution_blocker"]; exists {
		t.Fatalf("resolved action kept credential blocker: %#v", resolved.Actions[0].Metadata)
	}
	if got := resolved.Actions[1].Metadata["execution_blocker"]; got != "import_credentials_unmanaged" {
		t.Fatalf("unrelated action blocker = %#v", got)
	}
	if got := resolved.Actions[1].Metadata["execution_blockers"]; !reflect.DeepEqual(got, []any{"import_credentials_unmanaged"}) {
		t.Fatalf("unrelated action blocker list = %#v", got)
	}
}

func TestSecretBindingActionScopeMatchesRecursiveWalkerPaths(t *testing.T) {
	tests := map[string]string{
		"/init/0/auth/password":                              "/init/0",
		"/actions/1/control/steps/2/headers/Authorization":   "/actions/1/control/steps/2",
		"/actions/1/control/else/0/params/requests/3/target": "/actions/1/control/else/0/params/requests/3",
		"/teardown/4/body":                                   "/teardown/4",
		"/config/http/auth/password":                         "",
	}
	for pointer, want := range tests {
		if got := secretBindingActionScope(pointer); got != want {
			t.Errorf("secretBindingActionScope(%q) = %q, want %q", pointer, got, want)
		}
		if want == "" {
			continue
		}
		walkPath := strings.TrimPrefix(strings.ReplaceAll(want, "/", "."), ".")
		walkPath = regexp.MustCompile(`\.([0-9]+)(\.|$)`).ReplaceAllString(walkPath, `[$1]$2`)
		if got := dslWalkPathJSONPointer(walkPath); got != want {
			t.Errorf("dslWalkPathJSONPointer(%q) = %q, want %q", walkPath, got, want)
		}
	}
}

func TestHandleApplyScriptSecretBindings(t *testing.T) {
	server := NewServer(ServerConfig{})
	script := redactedBindingTestScript()
	requirements := discoverSecretBindingRequirements(script, 7)
	header := findSecretBindingRequirement(t, requirements, "/actions/0/headers/Authorization", 0)

	response := postSecretBindingJSON(t, server, "/api/v1/scripts/secret-bindings/apply", map[string]any{
		"script":       script,
		"script_index": 7,
		"bindings": []map[string]string{{
			"requirement_id": header.ID,
			"secret_name":    "LOGIN_TOKEN",
		}},
	})
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Script       dsl.Script                 `json:"script"`
		Requirements []SecretBindingRequirement `json:"secret_binding_requirements"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Script.Actions[0].Headers["Authorization"] != "Bearer {{secret.LOGIN_TOKEN}}" {
		t.Errorf("authorization = %q", payload.Script.Actions[0].Headers["Authorization"])
	}
	if len(payload.Requirements) != 4 {
		t.Errorf("remaining requirements = %d, want 4", len(payload.Requirements))
	}

	bad := postSecretBindingJSON(t, server, "/api/v1/scripts/secret-bindings/apply", map[string]any{
		"script": script,
		"bindings": []map[string]string{{
			"requirement_id": "unknown",
			"secret_name":    "LOGIN_TOKEN",
		}},
	})
	if bad.Code != http.StatusConflict {
		t.Fatalf("unknown id status = %d body=%s", bad.Code, bad.Body.String())
	}
	var stale struct {
		Code         string                     `json:"code"`
		Requirements []SecretBindingRequirement `json:"secret_binding_requirements"`
	}
	if err := json.Unmarshal(bad.Body.Bytes(), &stale); err != nil {
		t.Fatal(err)
	}
	if stale.Code != secretBindingRequirementsChangedCode || len(stale.Requirements) != len(requirements) {
		t.Fatalf("stale response = %+v", stale)
	}

	mutated := cloneScriptDefinition(script)
	mutated.Actions[0].Name = "Renamed login"
	staleResponse := postSecretBindingJSON(t, server, "/api/v1/scripts/secret-bindings/apply", map[string]any{
		"script":       mutated,
		"script_index": 7,
		"bindings": []map[string]string{{
			"requirement_id": header.ID,
			"secret_name":    "LOGIN_TOKEN",
		}},
	})
	if staleResponse.Code != http.StatusConflict {
		t.Fatalf("edited draft status = %d body=%s", staleResponse.Code, staleResponse.Body.String())
	}
	if err := json.Unmarshal(staleResponse.Body.Bytes(), &stale); err != nil {
		t.Fatal(err)
	}
	refreshedHeader := findSecretBindingRequirement(t, stale.Requirements, "/actions/0/headers/Authorization", 0)
	if refreshedHeader.ID == header.ID || refreshedHeader.Context != `Action "Renamed login"` {
		t.Fatalf("refreshed requirement = %+v, stale = %+v", refreshedHeader, header)
	}
}

func TestApplyScriptSecretBindingsRouteUsesCreateScriptEnvelopeAndEditorAccess(t *testing.T) {
	applyRequest := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/secret-bindings/apply", strings.NewReader(`{"script":{}}`))
	createRequest := httptest.NewRequest(http.MethodPost, "/api/v1/scripts", strings.NewReader(`{"script":{}}`))
	if got, want := requestMaxBodyBytes(applyRequest), requestMaxBodyBytes(createRequest); got != want || got != defaultMaxRequestBodyBytes {
		t.Fatalf("apply request body limit = %d, create = %d, default = %d", got, want, defaultMaxRequestBodyBytes)
	}
	requirement, ok := accessRequirementForRequest(applyRequest)
	if !ok || !containsRoleString(requirement.GlobalRoles, RoleEditor) || containsRoleString(requirement.GlobalRoles, RoleViewer) || requirement.ProjectLookup == nil {
		t.Fatalf("apply access requirement = %+v, ok=%t", requirement, ok)
	}
}

func TestCreateScriptReturnsStructuredSecretBindingRequirements(t *testing.T) {
	server := NewServer(ServerConfig{})
	script := &dsl.Script{
		Name:     "Create imported script",
		Protocol: "http",
		Config: dsl.ScriptConfig{
			BaseURL: "https://example.test",
			HTTP: dsl.HTTPConfig{
				Auth:  dsl.AuthConfig{Type: "basic", Password: maskedScriptSecret},
				Proxy: dsl.ProxyConfig{Password: "raw-proxy-password"},
			},
		},
		Actions: []dsl.Action{
			{Name: "Ping", Type: "request", Target: "/ping"},
			{Name: "Source review", Type: "source_review", Target: "review", Metadata: map[string]any{
				"original_request": "Authorization: [REDACTED]",
			}},
		},
	}

	create := postSecretBindingJSON(t, server, "/api/v1/scripts", map[string]any{"script": script})
	if create.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 body=%s", create.Code, create.Body.String())
	}
	var blocked struct {
		Code         string                     `json:"code"`
		Requirements []SecretBindingRequirement `json:"secret_binding_requirements"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &blocked); err != nil {
		t.Fatal(err)
	}
	if blocked.Code != secretBindingsRequiredCode || len(blocked.Requirements) != 2 {
		t.Fatalf("blocked response = %+v body=%s", blocked, create.Body.String())
	}
	if strings.Contains(create.Body.String(), "raw-proxy-password") {
		t.Fatalf("structured binding response leaked mixed inline credential: %s", create.Body.String())
	}
	if got := server.runner.ListScripts(); len(got) != 0 {
		t.Fatalf("unresolved script was persisted: %#v", got)
	}

	authPassword := findSecretBindingRequirement(t, blocked.Requirements, "/config/http/auth/password", 0)
	proxyPassword := findSecretBindingRequirement(t, blocked.Requirements, "/config/http/proxy/password", 0)
	apply := postSecretBindingJSON(t, server, "/api/v1/scripts/secret-bindings/apply", map[string]any{
		"script": script,
		"bindings": []map[string]string{
			{"requirement_id": authPassword.ID, "secret_name": "HTTP_PASSWORD"},
			{"requirement_id": proxyPassword.ID, "secret_name": "PROXY_PASSWORD"},
		},
	})
	if apply.Code != http.StatusOK {
		t.Fatalf("apply status = %d body=%s", apply.Code, apply.Body.String())
	}
	var applied struct {
		Script dsl.Script `json:"script"`
	}
	if err := json.Unmarshal(apply.Body.Bytes(), &applied); err != nil {
		t.Fatal(err)
	}
	create = postSecretBindingJSON(t, server, "/api/v1/scripts", map[string]any{"script": applied.Script})
	if create.Code != http.StatusCreated {
		t.Fatalf("resolved create status = %d body=%s", create.Code, create.Body.String())
	}
	if got := server.runner.ListScripts(); len(got) != 1 || got[0].Script.Config.HTTP.Auth.Password != "{{secret.HTTP_PASSWORD}}" || got[0].Script.Config.HTTP.Proxy.Password != "{{secret.PROXY_PASSWORD}}" {
		t.Fatalf("stored resolved scripts = %#v", got)
	}
}

func TestCreateScriptReturnsBindingRequirementForInlineCredentialWithoutSentinel(t *testing.T) {
	server := NewServer(ServerConfig{})
	script := &dsl.Script{
		Name:     "Inline credential",
		Protocol: "http",
		Config: dsl.ScriptConfig{
			BaseURL: "https://example.test",
			HTTP: dsl.HTTPConfig{
				Auth: dsl.AuthConfig{Type: "basic", Password: "raw-password-without-sentinel"},
			},
		},
		Actions: []dsl.Action{{Name: "Ping", Type: "request", Target: "/ping"}},
	}

	response := postSecretBindingJSON(t, server, "/api/v1/scripts", map[string]any{"script": script})
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "raw-password-without-sentinel") {
		t.Fatalf("binding response disclosed inline credential: %s", response.Body.String())
	}
	var blocked struct {
		Code         string                     `json:"code"`
		Requirements []SecretBindingRequirement `json:"secret_binding_requirements"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &blocked); err != nil {
		t.Fatal(err)
	}
	if blocked.Code != secretBindingsRequiredCode || len(blocked.Requirements) != 1 {
		t.Fatalf("blocked response = %+v body=%s", blocked, response.Body.String())
	}
	if got := server.runner.ListScripts(); len(got) != 0 {
		t.Fatalf("inline credential was persisted: %#v", got)
	}
}

func TestUpdateScriptReturnsBindingRequirementForNewInlineCredential(t *testing.T) {
	server := NewServer(ServerConfig{})
	stored := server.runner.StoreScript(&dsl.Script{
		Name: "Safe stored script", Protocol: "http",
		Config: dsl.ScriptConfig{
			BaseURL: "https://example.test",
			HTTP:    dsl.HTTPConfig{Auth: dsl.AuthConfig{Type: "basic", Password: "{{secret.EXISTING_PASSWORD}}"}},
		},
		Actions: []dsl.Action{{Name: "Ping", Type: "request", Target: "/ping"}},
	})
	incoming := cloneScriptDefinition(stored.Script)
	incoming.Config.HTTP.Proxy.Password = "new-inline-proxy-password"
	body, err := json.Marshal(map[string]any{"script": incoming})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPut, "/api/v1/scripts/"+stored.ID, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)

	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "new-inline-proxy-password") {
		t.Fatalf("binding response disclosed update credential: %s", response.Body.String())
	}
	var blocked struct {
		Code         string                     `json:"code"`
		Requirements []SecretBindingRequirement `json:"secret_binding_requirements"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &blocked); err != nil {
		t.Fatal(err)
	}
	if blocked.Code != secretBindingsRequiredCode || len(blocked.Requirements) != 1 || blocked.Requirements[0].Path != "/config/http/proxy/password" {
		t.Fatalf("blocked response = %+v body=%s", blocked, response.Body.String())
	}
	unchanged, ok := server.runner.GetScript(stored.ID)
	if !ok || unchanged.Script.Config.HTTP.Proxy.Password != "" || unchanged.Script.Config.HTTP.Auth.Password != "{{secret.EXISTING_PASSWORD}}" {
		t.Fatalf("blocked update changed durable script: %#v", unchanged)
	}
}

func TestCreateAndUpdatePersistProjectedReviewMetadata(t *testing.T) {
	server := NewServer(ServerConfig{})
	const createSecret = "create-review-secret-never-store"
	script := &dsl.Script{
		Name: "Projected review evidence", Protocol: "http",
		Config: dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{
			{Name: "Ping", Type: "request", Target: "/ping"},
			{Name: "Source review", Type: "source_review", Target: "review", Metadata: map[string]any{
				"authorization": "Bearer " + createSecret,
			}},
		},
	}
	create := postSecretBindingJSON(t, server, "/api/v1/scripts", map[string]any{"script": script})
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", create.Code, create.Body.String())
	}
	storedScripts := server.runner.ListScripts()
	if len(storedScripts) != 1 {
		t.Fatalf("stored scripts = %#v", storedScripts)
	}
	storedPayload, err := json.Marshal(storedScripts[0].Script)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(storedPayload), createSecret) || !strings.Contains(string(storedPayload), maskedScriptSecret) {
		t.Fatalf("create persisted unprojected review metadata: %s", storedPayload)
	}

	const updateSecret = "update-review-secret-never-store"
	incoming := cloneScriptDefinition(storedScripts[0].Script)
	incoming.Actions[1].Metadata["authorization"] = "Bearer " + updateSecret
	body, err := json.Marshal(map[string]any{"script": incoming})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPut, "/api/v1/scripts/"+storedScripts[0].ID, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("update status = %d body=%s", response.Code, response.Body.String())
	}
	updated, ok := server.runner.GetScript(storedScripts[0].ID)
	if !ok {
		t.Fatal("updated script not found")
	}
	updatedPayload, err := json.Marshal(updated.Script)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(updatedPayload), updateSecret) || !strings.Contains(string(updatedPayload), maskedScriptSecret) {
		t.Fatalf("update persisted unprojected review metadata: %s", updatedPayload)
	}
}

func TestCreateScriptAllowsRedactedImportReviewEvidence(t *testing.T) {
	server := NewServer(ServerConfig{})
	script := &dsl.Script{
		Name:     "Review evidence",
		Protocol: "http",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Source: "har", Preserved: map[string]any{
			"source_review": map[string]any{"request_header": "Authorization: [REDACTED]"},
		}}},
		Config:  dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{Name: "Ping", Type: "request", Target: "/ping"}},
	}
	response := postSecretBindingJSON(t, server, "/api/v1/scripts", map[string]any{"script": script})
	if response.Code != http.StatusCreated {
		t.Fatalf("metadata-only sentinel status = %d, want 201 body=%s", response.Code, response.Body.String())
	}
	if got := server.runner.ListScripts(); len(got) != 1 {
		t.Fatalf("stored scripts = %#v", got)
	}
}

func TestReconcileMaskedScriptSecretsRejectsPromotedOrLegacyExecutableEvidence(t *testing.T) {
	for _, reviewType := range []string{"source_review", "review_required"} {
		t.Run(reviewType+" promotion", func(t *testing.T) {
			prior := &dsl.Script{
				Name: "Review evidence", Protocol: "http",
				Config: dsl.ScriptConfig{BaseURL: "https://example.test"},
				Actions: []dsl.Action{{
					Name: "Review", Type: reviewType, Target: "review",
					Metadata: map[string]any{"original_request": "Authorization: [REDACTED]"},
				}},
			}
			unchanged := cloneScriptDefinition(prior)
			if err := reconcileMaskedScriptSecrets(unchanged, prior); err != nil {
				t.Fatalf("unchanged non-executable evidence: %v", err)
			}
			promoted := cloneScriptDefinition(prior)
			promoted.Actions[0].Type = "request"
			if err := reconcileMaskedScriptSecrets(promoted, prior); err == nil || !strings.Contains(err.Error(), "promotes redacted review evidence") {
				t.Fatalf("promoted executable evidence error = %v", err)
			}
		})
		t.Run(reviewType+" raw credential promotion", func(t *testing.T) {
			prior := &dsl.Script{
				Name: "Raw review evidence", Protocol: "http",
				Config: dsl.ScriptConfig{BaseURL: "https://example.test"},
				Actions: []dsl.Action{{
					Name: "Review", Type: reviewType, Target: "review",
					Metadata: map[string]any{"authorization": "Bearer real-token"},
				}},
			}
			promoted := maskScriptSecrets(prior)
			if promoted == nil || !strings.Contains(promoted.Actions[0].Metadata["authorization"].(string), maskedScriptSecret) {
				t.Fatalf("projected prior = %#v, want redacted review evidence", promoted)
			}
			promoted.Actions[0].Type = "request"
			if err := reconcileMaskedScriptSecrets(promoted, prior); err == nil || !strings.Contains(err.Error(), "promotes redacted review evidence") {
				t.Fatalf("raw review credential promotion error = %v", err)
			}
		})
	}

	legacy := &dsl.Script{
		Name: "Legacy executable sentinel", Protocol: "http",
		Config: dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{
			Name: "Request", Type: "request", Target: "/request",
			Metadata: map[string]any{"authorization": maskedScriptSecret},
		}},
	}
	if err := reconcileMaskedScriptSecrets(cloneScriptDefinition(legacy), legacy); err == nil || !strings.Contains(err.Error(), "orphan") {
		t.Fatalf("legacy executable sentinel error = %v", err)
	}
}

func TestImportResponseIncludesProjectedMultiScriptSecretRequirements(t *testing.T) {
	first := &dsl.Script{
		ID: "script-a", Name: "First", Protocol: "http",
		Config:  dsl.ScriptConfig{BaseURL: "https://example.test", HTTP: dsl.HTTPConfig{Auth: dsl.AuthConfig{Type: "basic", Password: "inline-password-a"}}},
		Actions: []dsl.Action{{Name: "Ping", Type: "request", Target: "/ping", Headers: map[string]string{"Authorization": "Bearer inline-token-a"}}},
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Preserved: map[string]any{
			"source_review": map[string]any{"password": "evidence-only-password"},
		}}},
	}
	second := &dsl.Script{
		ID: "script-b", Name: "Second", Protocol: "http",
		Config:  dsl.ScriptConfig{BaseURL: "https://example.test", HTTP: dsl.HTTPConfig{Auth: dsl.AuthConfig{Type: "basic", Password: "inline-password-b"}}},
		Actions: []dsl.Action{{Name: "Ping", Type: "request", Target: "/ping"}},
	}

	response := newScriptImportResponse(scriptImportResponseOptions{Scripts: []*dsl.Script{first, second}, Script: first})
	requirements, ok := response["secret_binding_requirements"].([]SecretBindingRequirement)
	if !ok {
		t.Fatalf("requirements type = %T", response["secret_binding_requirements"])
	}
	if len(requirements) != 3 {
		t.Fatalf("requirements = %d, want 3: %#v", len(requirements), requirements)
	}
	if requirements[0].ScriptIndex != 0 || requirements[0].ScriptID != "script-a" || requirements[0].ScriptName != "First" {
		t.Errorf("first scope = %+v", requirements[0])
	}
	last := requirements[len(requirements)-1]
	if last.ScriptIndex != 1 || last.ScriptID != "script-b" || last.ScriptName != "Second" {
		t.Errorf("second scope = %+v", last)
	}
	if requirements[0].ID == last.ID {
		t.Errorf("multi-script requirement ids collided: %q", requirements[0].ID)
	}
	header := findSecretBindingRequirement(t, requirements, "/actions/0/headers/Authorization", 0)
	if header.ScriptIndex != 0 || !strings.HasPrefix(header.Replacement, "Bearer {{secret.") {
		t.Errorf("projected authorization wrapper guidance = %+v", header)
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "inline-password") || strings.Contains(string(encoded), "inline-token") || strings.Contains(string(encoded), "evidence-only-password") {
		t.Fatalf("import response leaked inline secret: %s", encoded)
	}
}

func findSecretBindingRequirement(t *testing.T, requirements []SecretBindingRequirement, path string, occurrence int) SecretBindingRequirement {
	t.Helper()
	for _, requirement := range requirements {
		if requirement.Path == path && requirement.Occurrence == occurrence {
			return requirement
		}
	}
	t.Fatalf("requirement %s occurrence %d not found in %#v", path, occurrence, requirements)
	return SecretBindingRequirement{}
}

func postSecretBindingJSON(t *testing.T, server *Server, path string, value any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	return response
}

// The placeholder-secret bypass must require an explicit opt-in and must never be
// reachable via the ambient APEXLOAD_ENV values that compose.yaml already sets
// (it ships APEXLOAD_ENV=local), nor in production even when opted in.
func TestSecretBindingEnforcementRequiresExplicitDevOptIn(t *testing.T) {
	tests := []struct {
		name       string
		appEnv     string
		fakeFlag   string
		wantBypass bool
	}{
		{name: "compose local stack without opt-in", appEnv: "local", wantBypass: false},
		{name: "development without opt-in", appEnv: "development", wantBypass: false},
		{name: "dev without opt-in", appEnv: "dev", wantBypass: false},
		{name: "unset env without opt-in", appEnv: "", wantBypass: false},
		{name: "explicit opt-in on local", appEnv: "local", fakeFlag: "true", wantBypass: true},
		{name: "explicit opt-in with unset env", appEnv: "", fakeFlag: "true", wantBypass: true},
		{name: "opt-in refused in production", appEnv: "production", fakeFlag: "true", wantBypass: false},
		{name: "opt-in refused in prod", appEnv: "prod", fakeFlag: "true", wantBypass: false},
		{name: "opt-in false", appEnv: "local", fakeFlag: "false", wantBypass: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("APEXLOAD_ENV", test.appEnv)
			t.Setenv(devFakeSecretsEnv, test.fakeFlag)
			if got := secretBindingEnforcementDisabled(); got != test.wantBypass {
				t.Fatalf("secretBindingEnforcementDisabled() = %v, want %v (APEXLOAD_ENV=%q %s=%q)",
					got, test.wantBypass, test.appEnv, devFakeSecretsEnv, test.fakeFlag)
			}
		})
	}
}
