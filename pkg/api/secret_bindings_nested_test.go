package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// A form field whose value is JSON keeps its recorded encoding through the
// disclosure projection, so review finds the credential inside it, and that
// credential and the form's own password both rebind to project secrets.
func TestNestedFormJSONCredentialsRebindThroughTheAPI(t *testing.T) {
	const password, apiKey = "synthetic-form-password-6a1c", "k-synthetic-0123456789abcdef"
	for _, test := range []struct{ name, field string }{
		{"literal JSON", `data={"api_key":"` + apiKey + `"}`},
		{"percent-encoded JSON", `data=%7B%22api_key%22%3A%22` + apiKey + `%22%7D`},
		{"lowercase escapes", `data=%7b%22api_key%22%3a%22` + apiKey + `%22%7d`},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := NewServer(ServerConfig{})
			script := loginScript("Nested form", "application/x-www-form-urlencoded", "password="+password+"&"+test.field)
			preview := server.newScriptImportResponseWithSourceSecrets(
				httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/har", nil),
				scriptImportResponseOptions{Script: script, Scripts: []*dsl.Script{script}},
			)
			names := map[int]string{0: "LOGIN_PASSWORD", 1: "API_KEY"}
			var bindings []map[string]string
			for _, requirement := range preview["secret_binding_requirements"].([]SecretBindingRequirement) {
				if requirement.Path != "/actions/1/body" {
					continue
				}
				if requirement.RebindUnavailableReason != "" || !requirement.DestinationValuePreserved {
					t.Fatalf("requirement = %#v, want it kept and rebindable", requirement)
				}
				bindings = append(bindings, map[string]string{"requirement_id": requirement.ID, "secret_name": names[requirement.Occurrence]})
			}
			if len(bindings) != 2 {
				t.Fatalf("body requirements = %d, want the password and the nested key", len(bindings))
			}
			masked, ok := preview["scripts"].([]any)
			if !ok || len(masked) != 1 {
				t.Fatalf("preview scripts = %#v", preview["scripts"])
			}

			applied := postSecretBindingJSON(t, server, "/api/v1/scripts/secret-bindings/apply", map[string]any{
				"script": masked[0], "bindings": bindings,
			})
			if applied.Code != http.StatusOK {
				t.Fatalf("apply status = %d body=%s", applied.Code, applied.Body.String())
			}
			var result struct {
				Script       dsl.Script                 `json:"script"`
				Requirements []SecretBindingRequirement `json:"secret_binding_requirements"`
			}
			if err := json.Unmarshal(applied.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			want := "password={{secret.LOGIN_PASSWORD}}&" + strings.Replace(test.field, apiKey, "{{secret.API_KEY}}", 1)
			if got := result.Script.Actions[1].Body; got != want {
				t.Fatalf("body = %q, want %q", got, want)
			}
			for _, requirement := range result.Requirements {
				if requirement.Path == "/actions/1/body" {
					t.Fatalf("requirement %#v remained after both were bound", requirement)
				}
			}
		})
	}
}

// A credential inside a URL nested in a query value is found, rebinds, and
// stays bound: review shows the reference rather than masking it again.
func TestNestedURLCredentialRebindsAndStaysBound(t *testing.T) {
	const password = "synthetic-nested-password-2b7d"
	for _, test := range []struct{ name, target, want string }{
		{
			name:   "encoded once",
			target: "/login?redirect=https%3A%2F%2Finner.example.test%2Fsso%3Fpassword%3D" + password + "%26safe%3Dok&x=1",
			want:   "/login?redirect=https%3A%2F%2Finner.example.test%2Fsso%3Fpassword%3D{{secret.NESTED_PASSWORD}}%26safe%3Dok&x=1",
		},
		{
			name:   "encoded twice",
			target: "/login?r=https%3A%2F%2Fmiddle.example.test%2F%3Fnext%3Dhttps%253A%252F%252Finner.example.test%252F%253Fpassword%253D" + password,
			want:   "/login?r=https%3A%2F%2Fmiddle.example.test%2F%3Fnext%3Dhttps%253A%252F%252Finner.example.test%252F%253Fpassword%253D{{secret.NESTED_PASSWORD}}",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			script := loginScript("Nested URL", "application/x-www-form-urlencoded", "user=alice")
			script.Actions[1].Target = test.target
			response := newScriptImportResponse(scriptImportResponseOptions{Script: script})
			var bindings []secretBindingSelection
			for _, requirement := range response["secret_binding_requirements"].([]SecretBindingRequirement) {
				if requirement.Path != "/actions/1/target" {
					continue
				}
				if requirement.RebindUnavailableReason != "" || !requirement.DestinationValuePreserved {
					t.Fatalf("requirement = %#v, want it kept and rebindable", requirement)
				}
				bindings = append(bindings, secretBindingSelection{RequirementID: requirement.ID, SecretName: "NESTED_PASSWORD"})
			}
			if len(bindings) != 1 {
				t.Fatalf("target requirements = %d, want the nested password", len(bindings))
			}
			resolved, remaining, err := applyCredentialPreservingSecretBindings(script, 0, bindings)
			if err != nil {
				t.Fatalf("rebind: %v", err)
			}
			if got := resolved.Actions[1].Target; got != test.want {
				t.Fatalf("target = %q, want %q", got, test.want)
			}
			for _, requirement := range remaining {
				if requirement.Path == "/actions/1/target" {
					t.Fatalf("requirement %#v remained after binding", requirement)
				}
			}
			if got := maskScriptSecrets(resolved).Actions[1].Target; got != test.want {
				t.Fatalf("review shows %q, want the bound reference %q", got, test.want)
			}
		})
	}
}
