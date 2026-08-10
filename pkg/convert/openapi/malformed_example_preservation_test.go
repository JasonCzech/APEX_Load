package openapi

import (
	"strings"
	"testing"
)

// An authored example that cannot be marshalled still renders into an
// executable request body. Scrubbing that render replaced credential material
// the operator already owned with [REDACTED] inside the body the draft would
// send. The review gate (malformed_authored_example) is what keeps the draft
// from executing; the bytes must survive so the operator can repair it.
func TestOpenAPIMalformedAuthoredExamplePreservesCredentialBytes(t *testing.T) {
	const canary = "openapi-nan-example-canary-6a21"
	const content = `openapi: 3.0.3
info:
  title: Malformed authored example with credentials
  version: "1"
servers:
  - url: https://api.example.test
paths:
  /reports:
    post:
      operationId: createReport
      requestBody:
        content:
          application/json:
            example:
              payload: "<password>` + canary + `</password>"
              ratio: .nan
      responses:
        "204":
          description: ok
`
	result, err := Convert(ImportRequest{Content: content})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	action := openAPIActionFull(result, "createReport")
	if action.Name == "" {
		t.Fatalf("Convert produced no action named %q", "createReport")
	}
	if !strings.Contains(action.Body, canary) {
		t.Fatalf("action body = %q, want the authored example value preserved", action.Body)
	}
	if strings.Contains(action.Body, "[REDACTED]") {
		t.Fatalf("action body = %q, want no redaction sentinel", action.Body)
	}
	review, ok := action.Metadata["import_review"].(map[string]any)
	if !ok || !containsString(stringSlice(review["reason_codes"]), "malformed_authored_example") {
		t.Fatalf("import review = %#v, want the malformed_authored_example gate retained",
			action.Metadata["import_review"])
	}
}
