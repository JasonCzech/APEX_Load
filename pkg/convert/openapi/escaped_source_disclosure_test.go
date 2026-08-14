package openapi

import (
	"net/url"
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
			if requirements := result.ProtectedRequirements.Take(); requirements != nil {
				t.Fatalf("preserved credential unexpectedly requires rebinding: %#v", requirements)
			}
			if len(result.Script.Actions) != 1 ||
				result.Script.Actions[0].Headers["X-Api-Key"] != test.decoded {
				t.Fatalf("destination script did not preserve decoded credential: %#v", result.Script.Actions)
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
			if requirements := result.ProtectedRequirements.Take(); requirements != nil {
				t.Fatalf("preserved block-scalar credential unexpectedly requires rebinding: %#v", requirements)
			}
			if len(result.Script.Actions) != 1 {
				t.Fatalf("actions = %#v", result.Script.Actions)
			}
			decodedTarget, decodeErr := url.QueryUnescape(result.Script.Actions[0].Target)
			if decodeErr != nil || !strings.Contains(decodedTarget, test.decoded) {
				t.Fatalf("destination target = %q, want preserved %q", result.Script.Actions[0].Target, test.decoded)
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
	if requirements := result.ProtectedRequirements.Take(); requirements != nil {
		t.Fatalf("preserved base URL credentials unexpectedly require rebinding: %#v", requirements)
	}
	const wantBaseURL = "https://alice:openapi-pass@api.example.test/root?api_key=openapi%2Dquery"
	if result.Script.Config.BaseURL != wantBaseURL {
		t.Fatalf("base URL = %q, want preserved %q", result.Script.Config.BaseURL, wantBaseURL)
	}
	if len(result.Script.Actions) != 1 || result.Script.Actions[0].Metadata["execution_blocker"] != nil {
		t.Fatalf("preserved base URL should remain executable: %#v", result.Script.Actions)
	}
	assertOpenAPISourceReviewRedacted(t, result,
		`openapi\u002dpass`, "openapi-pass", "openapi%2Dquery", "openapi-query")
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
	if requirements := result.ProtectedRequirements.Take(); len(requirements) != 1 ||
		requirements[0].SuggestedName != "OPENAPI_OAUTH_CLIENT_SECRET" ||
		requirements[0].SourceValueAvailable {
		t.Fatalf("generated OAuth client-secret requirement = %#v", requirements)
	}
	if len(result.Script.Init) != 1 {
		t.Fatalf("init = %#v", result.Script.Init)
	}
	init := result.Script.Init[0]
	const wantTokenURL = "https://alice:oauth-pass@api.example.test/oauth/token"
	if init.Target != wantTokenURL ||
		init.Params["token_url"] != init.Target ||
		init.Metadata["execution_blocker"] != nil {
		t.Fatalf("relative OAuth token URL did not preserve executable base credential: %#v", init)
	}
	assertOpenAPISourceReviewRedacted(t, result, `oauth\u002dpass`, "oauth-pass")
}
