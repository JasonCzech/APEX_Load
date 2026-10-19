package correlate

import "testing"

func TestGeneratedSetCookieExtractorRecognizesOnlyGeneratedExpressions(t *testing.T) {
	const name = "__Host-ERIC_PROD6073380544767438430"
	for _, ordinal := range []int{0, 1, 3} {
		expression, ok := setCookieExtractorForOccurrence("Set-Cookie", name, ordinal)
		if !ok {
			t.Fatal("generator refused")
		}
		if got, recognized := GeneratedSetCookieExtractor(expression); !recognized || got != name {
			t.Fatalf("ordinal %d: GeneratedSetCookieExtractor = %q, %v", ordinal, got, recognized)
		}
	}
	dotted, _ := setCookieExtractorForOccurrence("set-cookie", "a.b[c]", 0)
	if got, ok := GeneratedSetCookieExtractor(dotted); !ok || got != "a.b[c]" {
		t.Fatalf("quoted cookie name = %q, %v", got, ok)
	}
	generated, _ := setCookieExtractorForOccurrence("Set-Cookie", name, 0)
	location, _ := setCookieExtractorForOccurrence("Location", name, 0)
	for _, expression := range []string{
		generated + "x",
		"x" + generated,
		location,
		`(?m)^(?i:Set-Cookie):[ \t]*sid=abc123SECRETVALUE` + setCookieCaptureSuffix[1:],
		`token=k3Jq9LmN8vR3sT6wY1uE5iO0aBxYz`,
	} {
		if _, ok := GeneratedSetCookieExtractor(expression); ok {
			t.Errorf("recognized a non-generated expression: %s", expression)
		}
	}
}
