package k6

import (
	"strings"
	"testing"
)

func TestConvertDoesNotDiscloseEscapedCredentialInSourceReview(t *testing.T) {
	const source = `http.get("https://api.example.test/orders?api_key=k6\u002dsecret");`
	result, err := Convert(ImportRequest{Name: "escaped credential", Content: source})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if len(result.SecretRequirements) != 1 {
		t.Fatalf("secret requirements = %#v, want one projected query credential", result.SecretRequirements)
	}
	if result.SourceReview == nil || len(result.SourceReview.SourceFiles) != 1 {
		t.Fatalf("source review = %#v, want one source file", result.SourceReview)
	}
	content := result.SourceReview.SourceFiles[0].Content
	if strings.Contains(content, `k6\u002dsecret`) || strings.Contains(content, "k6-secret") {
		t.Fatalf("source review disclosed an escaped credential: %q", content)
	}
}

func TestConvertOmitsBacktickTemplateLiteralCredentialFromSourceReview(t *testing.T) {
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
	if !file.Redacted || strings.Contains(file.Content, "k6-backtick-secret") {
		t.Fatalf("source review disclosed a backtick credential: %#v", file)
	}
}

func TestConvertDoesNotDisclosePercentEncodedURLAndFormCredentials(t *testing.T) {
	const source = `http.get("https://api.example.test/orders?api_key=k6%2Dquery");
http.post("https://api.example.test/login", "password=k6%2Dform", {headers: {"Content-Type": "application/x-www-form-urlencoded"}});`
	result, err := Convert(ImportRequest{Name: "percent credentials", Content: source})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if len(result.SecretRequirements) != 2 {
		t.Fatalf("secret requirements = %#v, want URL and form credentials", result.SecretRequirements)
	}
	if result.SourceReview == nil || len(result.SourceReview.SourceFiles) != 1 {
		t.Fatalf("source review = %#v, want one source file", result.SourceReview)
	}
	content := result.SourceReview.SourceFiles[0].Content
	for _, canary := range []string{"k6%2Dquery", "k6-query", "k6%2Dform", "k6-form"} {
		if strings.Contains(content, canary) {
			t.Fatalf("source review disclosed percent-encoded credential %q: %q", canary, content)
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
	if len(result.SecretRequirements) != 1 {
		t.Fatalf("secret requirements = %#v, want escaped query credential", result.SecretRequirements)
	}
	if result.SourceReview == nil || len(result.SourceReview.SourceFiles) != 1 {
		t.Fatalf("source review = %#v, want one source file", result.SourceReview)
	}
	content := result.SourceReview.SourceFiles[0].Content
	if strings.Contains(content, `k6\u002dcomment`) ||
		strings.Contains(content, "k6-comment") {
		t.Fatalf("comment apostrophe hid escaped credential from source redaction: %q", content)
	}
}
