package postman

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConvertDoesNotDiscloseEscapedCredentialVariableInSourceReview(t *testing.T) {
	const source = `{
  "info": {"name": "escaped"},
  "variable": [{"key": "sourceCredential", "value": "postman\u002dsecret"}],
  "item": [{
    "name": "request",
    "request": {
      "method": "GET",
      "url": "https://api.example.test/orders",
      "header": [{"key": "X-Api-Key", "value": "{{sourceCredential}}"}]
    }
  }]
}`
	result, err := Convert(ImportRequest{Content: source})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	requirements := result.ProtectedRequirements.Take()
	defer clearPostmanProtectedValues(requirements)
	if len(requirements) != 1 || requirements[0].Value != "postman-secret" {
		t.Fatalf("protected requirements = %#v, want decoded credential", requirements)
	}
	if result.SourceReview == nil || len(result.SourceReview.SourceFiles) == 0 {
		t.Fatalf("source review = %#v, want source files", result.SourceReview)
	}
	for _, file := range result.SourceReview.SourceFiles {
		if strings.Contains(file.Content, `postman\u002dsecret`) ||
			strings.Contains(file.Content, "postman-secret") {
			t.Fatalf("source review file %q disclosed an escaped credential: %q", file.Path, file.Content)
		}
	}
}

func TestConvertProjectsBaseURLUserinfoAndPercentQueryWithoutPublicDisclosure(t *testing.T) {
	const source = `{
  "info": {"name": "base credentials"},
  "variable": [{
    "key": "baseUrl",
    "value": "https://alice:postman\u002dpass@api.example.test/root?api_key=postman%2Dquery"
  }],
  "item": [{
    "name": "request",
    "request": {
      "method": "GET",
      "url": "{{baseUrl}}"
    }
  }]
}`
	result, err := Convert(ImportRequest{Content: source})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	requirements := result.ProtectedRequirements.Take()
	defer clearPostmanProtectedValues(requirements)
	if len(requirements) != 2 {
		t.Fatalf("protected requirements = %#v, want userinfo and query bindings", requirements)
	}
	if strings.Contains(result.Script.Config.BaseURL, "postman-pass") ||
		strings.Contains(result.Script.Config.BaseURL, "postman%2Dquery") ||
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
		`postman\u002dpass`,
		"postman-pass",
		"postman%2Dquery",
		"postman-query",
	} {
		if strings.Contains(string(encoded), canary) {
			t.Fatalf("serialized conversion disclosed %q: %s", canary, encoded)
		}
	}
}
