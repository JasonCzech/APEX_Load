package openapi

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConvertDoesNotDiscloseEscapedCredentialExamplesInSourceReview(t *testing.T) {
	tests := []struct {
		name    string
		content string
		encoded string
		decoded string
	}{
		{
			name: "json unicode escape",
			content: `{
  "openapi": "3.0.3",
  "info": {"title": "escaped", "version": "1"},
  "servers": [{"url": "https://api.example.test"}],
  "paths": {"/orders": {"get": {
    "parameters": [{"name": "X-Api-Key", "in": "header", "schema": {"type": "string"}, "example": "openapi\u002djson"}],
    "responses": {"200": {"description": "ok"}}
  }}}
}`,
			encoded: `openapi\u002djson`,
			decoded: "openapi-json",
		},
		{
			name: "yaml unicode escape",
			content: `openapi: 3.0.3
info:
  title: escaped
  version: "1"
servers:
  - url: https://api.example.test
paths:
  /orders:
    get:
      parameters:
        - name: X-Api-Key
          in: header
          schema:
            type: string
          example: "openapi\u002dyaml"
      responses:
        "200":
          description: ok
`,
			encoded: `openapi\u002dyaml`,
			decoded: "openapi-yaml",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := Convert(ImportRequest{Content: test.content})
			if err != nil {
				t.Fatalf("Convert returned error: %v", err)
			}
			requirements := result.ProtectedRequirements.Take()
			defer clearOpenAPIProtectedValues(requirements)
			if len(requirements) != 1 || requirements[0].Value != test.decoded {
				t.Fatalf("protected requirements = %#v, want decoded credential", requirements)
			}
			if result.SourceReview == nil || len(result.SourceReview.SourceFiles) != 1 {
				t.Fatalf("source review = %#v, want one source file", result.SourceReview)
			}
			content := result.SourceReview.SourceFiles[0].Content
			if strings.Contains(content, test.encoded) || strings.Contains(content, test.decoded) {
				t.Fatalf("source review disclosed an escaped credential: %q", content)
			}
		})
	}
}

func TestConvertOmitsSourceReviewWithCredentialInYAMLBlockScalar(t *testing.T) {
	tests := []struct {
		name      string
		indicator string
		lines     string
		decoded   string
	}{
		{
			name:      "literal",
			indicator: "|-",
			lines:     "            openapi-literal\\n            secret",
			decoded:   "openapi-literal\nsecret",
		},
		{
			name:      "folded",
			indicator: ">-",
			lines:     "            openapi-folded\\n            secret",
			decoded:   "openapi-folded secret",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			lines := strings.ReplaceAll(test.lines, `\n`, "\n")
			content := `openapi: 3.0.3
info:
  title: block scalar
  version: "1"
servers:
  - url: https://api.example.test
paths:
  /orders:
    get:
      parameters:
        - name: api_key
          in: query
          schema:
            type: string
          example: ` + test.indicator + "\n" + lines + `
      responses:
        "200":
          description: ok
`
			result, err := Convert(ImportRequest{Content: content})
			if err != nil {
				t.Fatalf("Convert returned error: %v", err)
			}
			requirements := result.ProtectedRequirements.Take()
			defer clearOpenAPIProtectedValues(requirements)
			if len(requirements) == 0 {
				t.Fatalf("protected requirements = %#v, want block-scalar query credential", requirements)
			}
			if result.SourceReview == nil || len(result.SourceReview.SourceFiles) != 1 {
				t.Fatalf("source review = %#v, want one omitted source marker", result.SourceReview)
			}
			file := result.SourceReview.SourceFiles[0]
			if !file.Redacted || file.Content != openAPISourceReviewCredentialOmission {
				t.Fatalf("source review was not fail-closed: %#v", file)
			}
			if strings.Contains(file.Content, test.decoded) ||
				strings.Contains(file.Content, "openapi-"+test.name) {
				t.Fatalf("source review disclosed block-scalar credential: %q", file.Content)
			}
		})
	}
}

func TestConvertProjectsBaseURLUserinfoAndPercentQueryWithoutPublicDisclosure(t *testing.T) {
	const source = `{
  "openapi": "3.0.3",
  "info": {"title": "base credentials", "version": "1"},
  "servers": [{"url": "https://alice:openapi\u002dpass@api.example.test/root?api_key=openapi%2Dquery"}],
  "paths": {"/orders": {"get": {
    "responses": {"200": {"description": "ok"}}
  }}}
}`
	result, err := Convert(ImportRequest{Content: source})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	requirements := result.ProtectedRequirements.Take()
	defer clearOpenAPIProtectedValues(requirements)
	if len(requirements) != 2 {
		t.Fatalf("protected requirements = %#v, want userinfo and query bindings", requirements)
	}
	if strings.Contains(result.Script.Config.BaseURL, "openapi-pass") ||
		strings.Contains(result.Script.Config.BaseURL, "openapi%2Dquery") ||
		!strings.Contains(result.Script.Config.BaseURL, "{{secret.") {
		t.Fatalf("base URL was not projected safely: %q", result.Script.Config.BaseURL)
	}
	if len(result.Script.Actions) != 1 ||
		result.Script.Actions[0].Metadata["execution_blocker"] != "credential_url_userinfo_review_required" {
		t.Fatalf("base URL userinfo did not durably gate the request: %#v", result.Script.Actions)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, canary := range []string{
		`openapi\u002dpass`,
		"openapi-pass",
		"openapi%2Dquery",
		"openapi-query",
	} {
		if strings.Contains(string(encoded), canary) {
			t.Fatalf("serialized conversion disclosed %q: %s", canary, encoded)
		}
	}
}

func TestConvertProjectsBaseURLCredentialFromRelativeOAuthTokenURL(t *testing.T) {
	const source = `{
  "openapi": "3.0.3",
  "info": {"title": "oauth base credentials", "version": "1"},
  "servers": [{"url": "https://alice:oauth\u002dpass@api.example.test"}],
  "components": {"securitySchemes": {"oauth": {
    "type": "oauth2",
    "flows": {"clientCredentials": {
      "tokenUrl": "/oauth/token",
      "scopes": {}
    }}
  }}},
  "security": [{"oauth": []}],
  "paths": {"/orders": {"get": {
    "responses": {"200": {"description": "ok"}}
  }}}
}`
	result, err := Convert(ImportRequest{Content: source})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	requirements := result.ProtectedRequirements.Take()
	defer clearOpenAPIProtectedValues(requirements)
	if len(requirements) != 1 || len(result.Script.Init) != 1 {
		t.Fatalf("requirements/init = %#v / %#v", requirements, result.Script.Init)
	}
	init := result.Script.Init[0]
	if strings.Contains(init.Target, "oauth-pass") ||
		!strings.Contains(init.Target, requirements[0].Replacement) ||
		init.Params["token_url"] != init.Target ||
		init.Metadata["execution_blocker"] != "credential_url_userinfo_review_required" {
		t.Fatalf("relative OAuth token URL retained an ungoverned base credential: %#v", init)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `oauth\u002dpass`) ||
		strings.Contains(string(encoded), "oauth-pass") {
		t.Fatalf("serialized OAuth conversion disclosed base credential: %s", encoded)
	}
}
