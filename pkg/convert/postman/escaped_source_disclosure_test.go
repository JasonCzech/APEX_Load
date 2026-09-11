package postman

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConvertPreservesEscapedCredentialVariableInSourceReview(t *testing.T) {
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
	if len(requirements) != 0 {
		t.Fatalf("protected requirements = %#v, preserved credential must not require reentry", requirements)
	}
	if got := result.Script.Actions[0].Headers["X-Api-Key"]; got != "postman-secret" {
		t.Fatalf("destination header = %q, want decoded imported credential", got)
	}
	if result.SourceReview == nil || len(result.SourceReview.SourceFiles) == 0 {
		t.Fatalf("source review = %#v, want source files", result.SourceReview)
	}
	if got := result.SourceReview.SourceFiles[0].Content; got != source {
		t.Fatalf("source review changed imported collection:\n%s", got)
	}
}

func TestConvertPreservesBaseURLUserinfoAndPercentQueryInSourceReview(t *testing.T) {
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
	if len(requirements) != 0 {
		t.Fatalf("protected requirements = %#v, preserved base URL must not require reentry", requirements)
	}
	wantBaseURL := "https://alice:postman-pass@api.example.test/root?api_key=postman%2Dquery"
	if result.Script.Config.BaseURL != wantBaseURL {
		t.Fatalf("base URL = %q, want imported value %q", result.Script.Config.BaseURL, wantBaseURL)
	}
	if len(result.Script.Actions) != 1 ||
		result.Script.Actions[0].Metadata["execution_blocker"] != nil {
		t.Fatalf("preserved base URL userinfo blocked execution: %#v", result.Script.Actions)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), "postman-pass") ||
		!strings.Contains(string(encoded), "postman%2Dquery") {
		t.Fatalf("serialized destination lost imported base credentials: %s", encoded)
	}
	if result.SourceReview == nil || len(result.SourceReview.SourceFiles) != 1 ||
		result.SourceReview.SourceFiles[0].Content != source {
		t.Fatalf("source review changed imported collection: %#v", result.SourceReview)
	}
}
