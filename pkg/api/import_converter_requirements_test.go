package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestScriptImportResponseMergesProtectedAndGeneratedCredentialRequirements(t *testing.T) {
	script := converterRequirementTestScript("Hybrid", "plans/hybrid.jmx")
	script.Actions = append(script.Actions, dsl.Action{
		Name: "API key request", Type: "request", Method: "GET", Target: "/key",
		HeaderList: dsl.HeaderList{{Name: "X-API-Key", Value: "{{secret.API_KEY}}"}},
	})
	protected := converterRequirementTestRequirement("protected-password", "plans/hybrid.jmx")
	protected.Value = "protected-password-value"
	protected.SourceValueAvailable = true
	protected.SourceValueStatus = credentialpolicy.SourceCredentialValueAvailable
	generated := credentialpolicy.Requirement{
		ID: "generated-api-key", Key: "api_key", Label: "Generated API key",
		Context: "Generated API key used by API key request", SuggestedName: "API_KEY",
		Replacement: "{{secret.API_KEY}}",
		Source:      credentialpolicy.SourceRef{Format: "jmeter", Path: "plans/hybrid.jmx"},
		Targets: []credentialpolicy.TargetRef{{
			RequestKey: "API key request", Field: "header_list", Index: 0,
		}},
		SourceValueStatus: credentialpolicy.SourceCredentialValueMissing,
	}
	server := NewServer(ServerConfig{})
	response := server.newScriptImportResponseWithSourceSecrets(
		httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/jmeter", nil),
		scriptImportResponseOptions{
			Script:                 script,
			ProtectedRequirements:  []credentialpolicy.Requirement{protected},
			CredentialRequirements: []credentialpolicy.Requirement{generated},
		},
	)
	requirements, ok := response["secret_binding_requirements"].([]SecretBindingRequirement)
	if !ok || len(requirements) != 2 {
		t.Fatalf("hybrid requirements = %#v, want protected + generated", response["secret_binding_requirements"])
	}
	seen := map[string]bool{}
	for _, requirement := range requirements {
		seen[requirement.ID] = true
	}
	if !seen[protected.ID] || !seen[generated.ID] {
		t.Fatalf("hybrid requirement ids = %#v", requirements)
	}
}

func TestMergeProtectedImportCredentialReviewSupersedesGenericSameSlot(t *testing.T) {
	response := map[string]any{"secret_binding_requirements": []SecretBindingRequirement{{
		ID: "generic", ScriptIndex: 1, Path: "/actions/0/header_list/0/value",
	}}}
	authoritative := SecretBindingRequirement{
		ID: "converter", ScriptIndex: 1, Path: "/actions/0/header_list/0/value",
		AffectedPaths: []string{"/actions/0/header_list/0/value", "/actions/0/headers/Authorization"},
	}
	mergeProtectedImportCredentialReview(response, []SecretBindingRequirement{authoritative})
	merged, _ := response["secret_binding_requirements"].([]SecretBindingRequirement)
	if len(merged) != 1 || merged[0].ID != authoritative.ID {
		t.Fatalf("merged same-slot review = %#v", merged)
	}
}

func TestMergeReferencedImportCredentialRequirementsScopesCoveragePerScript(t *testing.T) {
	scripts := []*dsl.Script{
		converterRequirementTestScript("First", "plans/first.jmx"),
		converterRequirementTestScript("Second", "plans/second.jmx"),
	}
	first := converterRequirementTestRequirement("first-requirement", "plans/first.jmx")
	merged := mergeReferencedImportCredentialRequirements(scripts, []credentialpolicy.Requirement{first})
	if len(merged) != 2 {
		t.Fatalf("merged requirements = %#v, want converter guidance for first and synthesized guidance for second", merged)
	}
	public, err := attachImportCredentialReview(scripts, merged)
	if err != nil {
		t.Fatalf("attach scoped merged requirements: %v", err)
	}
	if len(public) != 2 || public[0].ScriptIndex != 0 || public[0].ID != first.ID ||
		public[1].ScriptIndex != 1 || public[1].ID == first.ID {
		t.Fatalf("scoped public requirements = %#v", public)
	}
}

func TestMergeReferencedImportCredentialRequirementsScopesSharedValueFreeDescriptor(t *testing.T) {
	scripts := []*dsl.Script{
		converterRequirementTestScript("First", ""),
		converterRequirementTestScript("Second", ""),
	}
	shared := converterRequirementTestRequirement("shared-requirement", "")
	shared.Targets = []credentialpolicy.TargetRef{{Field: "header_list"}}
	merged := mergeReferencedImportCredentialRequirements(scripts, []credentialpolicy.Requirement{shared})
	if len(merged) != 2 || merged[0].ID == merged[1].ID {
		t.Fatalf("scoped shared requirements = %#v, want distinct per-script descriptors", merged)
	}
	public, err := attachImportCredentialReview(scripts, merged)
	if err != nil {
		t.Fatalf("attach shared value-free requirements: %v", err)
	}
	if len(public) != 2 || public[0].ScriptIndex != 0 || public[1].ScriptIndex != 1 {
		t.Fatalf("shared public requirements = %#v", public)
	}
}

func TestAttachImportCredentialReviewScopesSameReplacementBySourceFile(t *testing.T) {
	scripts := []*dsl.Script{
		converterRequirementTestScript("First", "plans/first.jmx"),
		converterRequirementTestScript("Second", "plans/second.jmx"),
	}
	requirements := []credentialpolicy.Requirement{
		converterRequirementTestRequirement("first-requirement", "plans/first.jmx"),
		converterRequirementTestRequirement("second-requirement", "plans/second.jmx"),
	}

	public, err := attachImportCredentialReview(scripts, requirements)
	if err != nil {
		t.Fatalf("attach converter requirements: %v", err)
	}
	if len(public) != 2 {
		t.Fatalf("public requirements = %#v, want two", public)
	}
	for index, wantID := range []string{"first-requirement", "second-requirement"} {
		if public[index].ID != wantID || public[index].ScriptIndex != index ||
			public[index].ScriptName != scripts[index].Name {
			t.Fatalf("public requirement %d = %#v", index, public[index])
		}
		descriptors, err := loadProtectedImportCredentialDescriptors(scripts[index])
		if err != nil {
			t.Fatalf("load script %d descriptors: %v", index, err)
		}
		if len(descriptors) != 1 || descriptors[0].ID != wantID {
			t.Fatalf("script %d descriptors = %#v, want only %q", index, descriptors, wantID)
		}
	}
}

func TestAttachImportCredentialReviewScopesSameSourceByRequestIdentity(t *testing.T) {
	scripts := []*dsl.Script{
		converterRequirementTestScript("Orders producer", "contracts/asyncapi.yaml"),
		converterRequirementTestScript("Invoices producer", "contracts/asyncapi.yaml"),
	}
	scripts[0].Actions[0].Name = "Orders producer"
	scripts[1].Actions[0].Name = "Invoices producer"
	first := converterRequirementTestRequirement("orders-eventhubs", "contracts/asyncapi.yaml")
	first.Targets[0].RequestKey = "Orders producer"
	second := converterRequirementTestRequirement("invoices-eventhubs", "contracts/asyncapi.yaml")
	second.Targets[0].RequestKey = "Invoices producer"

	public, err := attachImportCredentialReview(scripts, []credentialpolicy.Requirement{first, second})
	if err != nil {
		t.Fatalf("attach same-source requirements: %v", err)
	}
	if len(public) != 2 || public[0].ScriptIndex != 0 || public[0].ID != first.ID ||
		public[1].ScriptIndex != 1 || public[1].ID != second.ID {
		t.Fatalf("same-source scoped requirements = %#v", public)
	}
}

func TestAttachImportCredentialReviewBoundsHighCardinalityWorkByProvenance(t *testing.T) {
	const count = 200
	scripts := make([]*dsl.Script, 0, count)
	requirements := make([]credentialpolicy.Requirement, 0, count)
	for index := 0; index < count; index++ {
		path := fmt.Sprintf("plans/%03d.jmx", index)
		script := converterRequirementTestScript(fmt.Sprintf("Plan %03d", index), path)
		scripts = append(scripts, script)
		requirement := converterRequirementTestRequirement(fmt.Sprintf("requirement-%03d", index), path)
		requirements = append(requirements, requirement)
	}
	work := &importCredentialReviewWork{}
	public, err := attachImportCredentialReviewWithWork(scripts, requirements, work)
	if err != nil {
		t.Fatalf("attach high-cardinality requirements: %v", err)
	}
	if len(public) != count {
		t.Fatalf("public requirement count = %d, want %d", len(public), count)
	}
	if work.ScriptSerializations != count {
		t.Fatalf("script serializations = %d, want exactly %d", work.ScriptSerializations, count)
	}
	if work.LocationScans != count {
		t.Fatalf("location scans = %d, want one provenance-scoped scan per requirement", work.LocationScans)
	}
}

func converterRequirementTestScript(name, sourcePath string) *dsl.Script {
	return &dsl.Script{
		Name: name, Version: "1", Protocol: "http",
		Config: dsl.ScriptConfig{BaseURL: "https://example.test"},
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "jmeter",
			Preserved: map[string]any{
				"source_zip_entry": sourcePath,
			},
		}},
		Actions: []dsl.Action{{
			Name: "Request", Type: "request", Method: "GET", Target: "/",
			HeaderList: dsl.HeaderList{{Name: "Authorization", Value: "Bearer {{secret.PASSWORD}}"}},
		}},
	}
}

func converterRequirementTestRequirement(id, sourcePath string) credentialpolicy.Requirement {
	return credentialpolicy.Requirement{
		ID: id, Key: "authorization", Label: "Authorization credential",
		Context: "Authorization header used by Request", SuggestedName: "PASSWORD",
		Replacement: "{{secret.PASSWORD}}",
		Source:      credentialpolicy.SourceRef{Format: "jmeter", Path: sourcePath},
		Targets: []credentialpolicy.TargetRef{{
			RequestKey: "Request", Field: "header_list", Index: 0,
		}},
		SourceValueStatus: credentialpolicy.SourceCredentialValueMissing,
	}
}

// RA-15: a project secret the importer wrote into a request names that
// request, so a telemetry beacon reads as one rather than as "Sample raw body
// 1ee1a2332399 project secret".
func TestReferencedImportSecretsAreNamedByTheirRequest(t *testing.T) {
	const pathCredential = "path-credential-canary-7c41"
	script := &dsl.Script{
		Name: "Recorded HTTP", Version: "1", Protocol: "http",
		Config: dsl.ScriptConfig{BaseURL: "https://www.example.test"},
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Source: "har"}},
		Actions: []dsl.Action{
			{Name: "beacon", Type: "request", Method: "POST", Target: "https://telemetry.example.test/ctp?session=abc", Body: "{{secret.SAMPLE_RAW_BODY_1EE1A2332399}}"},
			{Name: "beacon again", Type: "request", Method: "post", Target: "https://telemetry.example.test/v2/ctp", Body: "{{secret.SAMPLE_RAW_BODY_1EE1A2332399}}"},
			{Name: "login", Type: "request", Method: "POST", Target: "/api/login", Headers: map[string]string{"Authorization": "Bearer {{secret.LOGIN_TOKEN}}"}},
			{Name: "key", Type: "request", Method: "GET", Target: "/key", HeaderList: dsl.HeaderList{{Name: "X-API-Key", Value: "{{secret.API_KEY}}"}}},
			{Name: "templated", Type: "request", Method: "PUT", Target: "{{env.API}}/v1/items/{{var.id}}?k=1", Body: "{{secret.TEMPLATED_BODY}}"},
			// The path carries a credential the draft masks (the basic-auth
			// password below); the label must not show it either.
			{Name: "path secret", Type: "request", Method: "POST", Target: "/v1/key/" + pathCredential, Body: "{{secret.PATH_BODY}}"},
		},
	}
	script.Config.HTTP.Auth = dsl.AuthConfig{Type: "basic", Username: "operator", Password: pathCredential}
	merged := mergeReferencedImportCredentialRequirements([]*dsl.Script{script}, nil)
	labels := map[string]string{}
	for _, requirement := range merged {
		labels[requirement.SuggestedName] = requirement.Label
	}
	if got, want := labels["SAMPLE_RAW_BODY_1EE1A2332399"], "Body of POST telemetry.example.test/ctp and 1 more request"; got != want {
		t.Errorf("beacon label = %q, want %q", got, want)
	}
	if got, want := labels["LOGIN_TOKEN"], "Authorization header of POST www.example.test/api/login"; got != want {
		t.Errorf("login label = %q, want %q", got, want)
	}
	if got, want := labels["API_KEY"], "X-API-Key header of GET www.example.test/key"; got != want {
		t.Errorf("header list label = %q, want %q", got, want)
	}
	if got, want := labels["TEMPLATED_BODY"], "Body of PUT {{env.API}}/v1/items/{{var.id}}"; got != want {
		t.Errorf("templated label = %q, want %q", got, want)
	}
	if got := labels["PATH_BODY"]; got == "" || strings.Contains(got, pathCredential) {
		t.Errorf("path secret label = %q, want the masked path", got)
	}
}
