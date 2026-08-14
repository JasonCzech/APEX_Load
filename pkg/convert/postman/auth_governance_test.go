package postman

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestConvertPostmanSanitizeSensitiveControlsExecutableCredentialProjection(t *testing.T) {
	content := `{
  "info":{"name":"Scrub policy"},
  "item":[{"name":"Login","request":{
    "method":"POST",
    "url":"https://api.example.test/login",
    "header":[{"key":"Authorization","value":"Bearer source-header-secret"},{"key":"Content-Type","value":"application/json"}],
    "body":{"mode":"raw","raw":"{\"password\":\"source-body-secret\",\"label\":\"ordinary\"}"}
  }}]
}`

	t.Run("enabled", func(t *testing.T) {
		sanitize := true
		result, err := Convert(ImportRequest{
			Content: content,
			Options: ConvertOptions{SanitizeSensitive: &sanitize},
		})
		if err != nil {
			t.Fatalf("Convert returned error: %v", err)
		}
		encoded, err := json.Marshal(result.Script)
		if err != nil {
			t.Fatal(err)
		}
		for _, sourceValue := range []string{"source-header-secret", "source-body-secret"} {
			if bytes.Contains(encoded, []byte(sourceValue)) {
				t.Fatalf("scrubbed executable script retained %q: %s", sourceValue, encoded)
			}
		}
		if !strings.Contains(result.Script.Actions[0].Headers["Authorization"], "{{secret.") ||
			!strings.Contains(result.Script.Actions[0].Body, "{{secret.") {
			t.Fatalf("scrubbed action did not use governed secret references: %#v", result.Script.Actions[0])
		}
		if policy := result.Script.Metadata.Import.Preserved[dsl.ImportPreservedCredentialPolicyKey]; policy != nil {
			t.Fatalf("scrubbed draft retained preserve policy: %#v", policy)
		}
		if projection := result.Script.Metadata.Import.Preserved["credential_projection"]; projection != "secret_reference" {
			t.Fatalf("credential projection metadata = %#v", projection)
		}
		requirements := result.ProtectedRequirements.Take()
		if len(requirements) < 2 {
			t.Fatalf("protected requirements = %#v, want header and body credentials", requirements)
		}
		for _, requirement := range requirements {
			if !requirement.SourceValueAvailable || requirement.Value == "" {
				t.Fatalf("scrubbed source value was not available to the protected migration flow: %#v", requirement)
			}
		}
	})

	t.Run("disabled", func(t *testing.T) {
		sanitize := false
		result, err := Convert(ImportRequest{
			Content: content,
			Options: ConvertOptions{SanitizeSensitive: &sanitize},
		})
		if err != nil {
			t.Fatalf("Convert returned error: %v", err)
		}
		encoded, err := json.Marshal(result.Script)
		if err != nil {
			t.Fatal(err)
		}
		for _, sourceValue := range []string{"source-header-secret", "source-body-secret"} {
			if !bytes.Contains(encoded, []byte(sourceValue)) {
				t.Fatalf("scrub-disabled script lost %q: %s", sourceValue, encoded)
			}
		}
	})
}

func TestConvertPostmanMissingAuthUsesRequestScopedGovernedSecrets(t *testing.T) {
	result, err := Convert(ImportRequest{Content: `{
  "info":{"name":"Missing auth"},
  "auth":{"type":"bearer","bearer":[{"key":"token","value":""}]},
  "item":[
    {"name":"First","request":{"method":"GET","url":"https://api.example.test/first"}},
    {"name":"Second","request":{"method":"GET","url":"https://api.example.test/second"}}
  ]
}`})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("actions = %d, want 2", len(result.Script.Actions))
	}
	first := result.Script.Actions[0].Auth.Token
	second := result.Script.Actions[1].Auth.Token
	if first == "" || second == "" || first == second {
		t.Fatalf("missing auth references = %q / %q, want request-scoped values", first, second)
	}
	for _, reference := range []string{first, second} {
		if !strings.HasPrefix(reference, "{{secret.POSTMAN_") || strings.Contains(reference, "API_TOKEN") {
			t.Fatalf("missing auth reference = %q, want governed descriptive secret", reference)
		}
	}
	joinedWarnings := strings.Join(result.Unsupported.Strings(), "\n")
	if !strings.Contains(joinedWarnings, "has no token value") || !strings.Contains(joinedWarnings, "configure secret") {
		t.Fatalf("missing auth diagnostics = %q", joinedWarnings)
	}
	if requirements := result.ProtectedRequirements.Take(); len(requirements) != 0 {
		t.Fatalf("missing auth invented protected source data: %#v", requirements)
	}
}

func TestConvertPostmanMissingAuthFieldsNeverUseSharedEnvironmentPlaceholders(t *testing.T) {
	tests := []struct {
		name     string
		authJSON string
		values   func(*dsl.AuthConfig) []string
	}{
		{
			name:     "basic",
			authJSON: `{"type":"basic","basic":[{"key":"username","value":""}]}`,
			values: func(auth *dsl.AuthConfig) []string {
				return []string{auth.Username, auth.Password}
			},
		},
		{
			name:     "digest",
			authJSON: `{"type":"digest","digest":[]}`,
			values: func(auth *dsl.AuthConfig) []string {
				return []string{auth.Username, auth.Password}
			},
		},
		{
			name:     "oauth2",
			authJSON: `{"type":"oauth2","oauth2":[{"key":"accessToken","value":""}]}`,
			values: func(auth *dsl.AuthConfig) []string {
				return []string{auth.Token}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := Convert(ImportRequest{Content: `{
  "info":{"name":"Missing fields"},
  "item":[{"name":"Request","request":{"method":"GET","url":"https://api.example.test/items","auth":` + test.authJSON + `}}]
}`})
			if err != nil {
				t.Fatalf("Convert returned error: %v", err)
			}
			auth := result.Script.Actions[0].Auth
			if auth == nil {
				t.Fatal("missing auth config")
			}
			seen := make(map[string]struct{})
			for _, value := range test.values(auth) {
				if !strings.HasPrefix(value, "{{secret.POSTMAN_") || strings.Contains(value, "{{env.API_") {
					t.Fatalf("missing %s auth field = %q", test.name, value)
				}
				if _, duplicate := seen[value]; duplicate {
					t.Fatalf("different missing %s fields shared %q", test.name, value)
				}
				seen[value] = struct{}{}
			}
		})
	}
}

func TestConvertPostmanRedactionSentinelsAreNeverPreservableCredentials(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{name: "literal", value: "[REDACTED]"},
		{name: "case varied", value: "[redacted]"},
		{name: "percent encoded", value: "%5BREDACTED%5D"},
		{name: "base64", value: "W1JFREFDVEVEXQ=="},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := Convert(ImportRequest{Content: `{
  "info":{"name":"Sentinel"},
  "item":[{"name":"Request","request":{
    "method":"GET",
    "url":"https://api.example.test/items",
    "header":[{"key":"X-Api-Key","value":"` + test.value + `"}]
  }}]
}`})
			if err != nil {
				t.Fatalf("Convert returned error: %v", err)
			}
			if got := result.Script.Actions[0].Headers["X-Api-Key"]; !strings.HasPrefix(got, "{{secret.") {
				t.Fatalf("sentinel destination = %q, want unconfigured secret reference", got)
			}
			requirements := result.ProtectedRequirements.Take()
			if len(requirements) != 1 {
				t.Fatalf("requirements = %#v", requirements)
			}
			if requirements[0].SourceValueAvailable || requirements[0].Value != "" {
				t.Fatalf("sentinel was presented as a recoverable imported value: %#v", requirements[0])
			}
		})
	}
}
