package k6

import (
	"strings"
	"testing"
)

func TestConvertPreservesEscapedCredentialInSourceReview(t *testing.T) {
	const source = `http.get("https://api.example.test/orders?api_key=k6\u002dsecret");`
	result, err := Convert(ImportRequest{Name: "escaped credential", Content: source})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if len(result.SecretRequirements) != 0 {
		t.Fatalf("preserved credential unexpectedly requires rebinding: %#v", result.SecretRequirements)
	}
	if len(result.Script.Actions) != 1 ||
		!strings.Contains(result.Script.Actions[0].Target, "api_key=k6-secret") {
		t.Fatalf("destination target did not preserve decoded credential: %#v", result.Script.Actions)
	}
	if result.SourceReview == nil || len(result.SourceReview.SourceFiles) != 1 {
		t.Fatalf("source review = %#v, want one source file", result.SourceReview)
	}
	content := result.SourceReview.SourceFiles[0].Content
	if !strings.Contains(content, `k6\u002dsecret`) || result.SourceReview.SourceFiles[0].Redacted {
		t.Fatalf("source review changed an escaped credential: %q", content)
	}
}

func TestConvertPreservesBacktickTemplateLiteralCredentialInSourceReview(t *testing.T) {
	const source = "http.get(`https://api.example.test/orders?api_key=k6-backtick-secret`);"
	result, err := Convert(ImportRequest{Name: "backtick credential", Content: source})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if len(result.Script.Actions) != 1 ||
		result.Script.Actions[0].Metadata["execution_blocker"] != "import_review_required" {
		t.Fatalf("static backtick literal was not retained as a blocked draft: %#v", result.Script.Actions)
	}
	if result.SourceReview == nil || len(result.SourceReview.SourceFiles) != 1 {
		t.Fatalf("source review = %#v, want one source file", result.SourceReview)
	}
	file := result.SourceReview.SourceFiles[0]
	if file.Redacted || !strings.Contains(file.Content, "k6-backtick-secret") {
		t.Fatalf("source review changed a backtick credential: %#v", file)
	}
}

func TestConvertPreservesPercentEncodedURLAndFormCredentials(t *testing.T) {
	const source = `http.get("https://api.example.test/orders?api_key=k6%2Dquery");
http.post("https://api.example.test/login", "password=k6%2Dform", {headers: {"Content-Type": "application/x-www-form-urlencoded"}});`
	result, err := Convert(ImportRequest{Name: "percent credentials", Content: source})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if len(result.SecretRequirements) != 0 {
		t.Fatalf("preserved credentials unexpectedly require rebinding: %#v", result.SecretRequirements)
	}
	if len(result.Script.Actions) != 2 ||
		!strings.Contains(result.Script.Actions[0].Target, "api_key=k6%2Dquery") ||
		result.Script.Actions[1].Body != "password=k6%2Dform" {
		t.Fatalf("destination actions did not preserve encoded credentials: %#v", result.Script.Actions)
	}
	if result.SourceReview == nil || len(result.SourceReview.SourceFiles) != 1 {
		t.Fatalf("source review = %#v, want one source file", result.SourceReview)
	}
	content := result.SourceReview.SourceFiles[0].Content
	for _, canary := range []string{"k6%2Dquery", "k6%2Dform"} {
		if !strings.Contains(content, canary) {
			t.Fatalf("source review lost percent-encoded credential %q: %q", canary, content)
		}
	}
}

func TestConvertCommentApostropheCannotHideEscapedCredential(t *testing.T) {
	const source = `// don't treat this apostrophe as the request quote
http.get('https://api.example.test/orders?api_key=k6\u002dcomment');`
	result, err := Convert(ImportRequest{Name: "comment apostrophe", Content: source})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if len(result.SecretRequirements) != 0 {
		t.Fatalf("preserved credential unexpectedly requires rebinding: %#v", result.SecretRequirements)
	}
	if len(result.Script.Actions) != 1 ||
		!strings.Contains(result.Script.Actions[0].Target, "api_key=k6-comment") {
		t.Fatalf("destination target did not preserve decoded credential: %#v", result.Script.Actions)
	}
	if result.SourceReview == nil || len(result.SourceReview.SourceFiles) != 1 {
		t.Fatalf("source review = %#v, want one source file", result.SourceReview)
	}
	content := result.SourceReview.SourceFiles[0].Content
	if !strings.Contains(content, `k6\u002dcomment`) || result.SourceReview.SourceFiles[0].Redacted {
		t.Fatalf("source review changed escaped credential after comment: %q", content)
	}
}

func TestConvertPreservesExplicitSecretReferenceWithoutCreatingRequirement(t *testing.T) {
	const source = `http.get("https://api.example.test/orders?api_key={{secret.EXISTING_API_KEY}}");`
	result, err := Convert(ImportRequest{Name: "existing reference", Content: source})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if len(result.Script.Actions) != 1 ||
		!strings.Contains(result.Script.Actions[0].Target, "api_key={{secret.EXISTING_API_KEY}}") {
		t.Fatalf("authored secret reference changed: %#v", result.Script.Actions)
	}
	if len(result.SecretRequirements) != 0 {
		t.Fatalf("authored secret reference created a redundant requirement: %#v", result.SecretRequirements)
	}
}

func TestConvertPreservesStructuredCredentialBytesButBlocksMalformedAndDuplicateJSON(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		reason string
	}{
		{
			name:   "malformed",
			body:   `{"password":"malformed-canary"`,
			reason: "credential_json_malformed",
		},
		{
			name:   "duplicate key",
			body:   `{"password":"first-canary","password":"second-canary"}`,
			reason: "credential_json_duplicate_key",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			quotedBody := strings.ReplaceAll(strings.ReplaceAll(test.body, `\`, `\\`), `"`, `\"`)
			source := `http.post("https://api.example.test/login", "` + quotedBody +
				`", {headers: {"Content-Type": "application/json"}});`
			result, err := Convert(ImportRequest{Name: test.name, Content: source})
			if err != nil {
				t.Fatalf("Convert returned error: %v", err)
			}
			if len(result.Script.Actions) != 1 {
				t.Fatalf("actions = %#v", result.Script.Actions)
			}
			action := result.Script.Actions[0]
			if action.Body != test.body {
				t.Fatalf("body = %q, want byte-preserved %q", action.Body, test.body)
			}
			if action.Metadata["execution_blocker"] != "import_review_required" {
				t.Fatalf("malformed/duplicate JSON was not blocked: %#v", action.Metadata)
			}
			review, ok := action.Metadata["import_review"].(map[string]any)
			if !ok || !containsStringValue(review["reason_codes"], test.reason) {
				t.Fatalf("import review = %#v, want %q", action.Metadata["import_review"], test.reason)
			}
			assertK6SourceReviewRedacted(t, result,
				"malformed-canary", "first-canary", "second-canary")
		})
	}
}

func containsStringValue(value any, want string) bool {
	values, ok := value.([]string)
	if !ok {
		return false
	}
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
