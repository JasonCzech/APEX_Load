package correlate

import (
	"strings"
	"testing"
)

func TestDetectHTMLTokenAndApplyLongestFirst(t *testing.T) {
	observations := []Observation{
		{
			ID:           1,
			ResponseBody: `<form><input type="hidden" name="csrf_token" value="tok-abcdef123456"></form>`,
		},
		{
			ID:          2,
			RequestBody: "csrf_token=tok-abcdef123456&id=123456789",
		},
		{
			ID:         3,
			RequestURL: "https://example.com/api?id=123",
		},
	}

	correlations := Detect(observations)
	if len(correlations) != 1 {
		t.Fatalf("correlation count = %d, want 1: %#v", len(correlations), correlations)
	}
	corr := correlations[0]
	if corr.Name != "corr_csrf_token" || corr.ExtractType != "regex" || corr.FoundInID != 1 {
		t.Fatalf("unexpected correlation: %#v", corr)
	}
	if len(corr.UsedInIDs) != 1 || corr.UsedInIDs[0] != 2 {
		t.Fatalf("used ids = %#v, want [2]", corr.UsedInIDs)
	}

	vars := map[string]string{
		"123":       "{{var.short}}",
		"123456789": "{{var.long}}",
	}
	if got := ApplyVars("id=123456789", vars); got != "id={{var.long}}" {
		t.Fatalf("ApplyVars longest first = %q", got)
	}
}

func TestDetectSetCookieValue(t *testing.T) {
	observations := []Observation{
		{
			ID:              1,
			ResponseHeaders: map[string]string{"Set-Cookie": "session=sid-abcdef123456; Path=/; HttpOnly"},
		},
		{
			ID:             2,
			RequestHeaders: map[string]string{"Cookie": "theme=dark; session=sid-abcdef123456"},
		},
	}

	correlations := Detect(observations)
	if len(correlations) != 1 {
		t.Fatalf("correlation count = %d, want 1: %#v", len(correlations), correlations)
	}
	corr := correlations[0]
	if corr.Name != "corr_cookie_session" || corr.Source != "header" || corr.ExtractType != "regex" {
		t.Fatalf("unexpected cookie correlation: %#v", corr)
	}
	if corr.SampleValue != "sid-abcdef123456" {
		t.Fatalf("sample value = %q", corr.SampleValue)
	}
	if !strings.Contains(corr.Expression, "Set-Cookie") || !strings.Contains(corr.Expression, "session=") {
		t.Fatalf("unexpected expression: %q", corr.Expression)
	}
}

func TestDetectNestedJSONPathToken(t *testing.T) {
	observations := []Observation{
		{
			ID:           1,
			ResponseBody: `{"auth":{"access_token":"tok-abcdef123456"}}`,
		},
		{
			ID:             2,
			RequestHeaders: map[string]string{"Authorization": "Bearer tok-abcdef123456"},
		},
	}

	correlations := Detect(observations)
	if len(correlations) != 1 {
		t.Fatalf("correlation count = %d, want 1: %#v", len(correlations), correlations)
	}
	corr := correlations[0]
	if corr.Name != "corr_access_token" || corr.ExtractType != "jsonpath" || corr.Expression != "$.auth.access_token" {
		t.Fatalf("unexpected correlation: %#v", corr)
	}
	if corr.RuleID != RuleJSONAuthToken {
		t.Fatalf("rule = %q, want %q", corr.RuleID, RuleJSONAuthToken)
	}
}

// TestDetectSkipsNonEvaluableBracketJSONPath guards against emitting a
// bracket-quoted JSONPath (e.g. $["x-csrf-token"]) that the replay-side lite
// extractor (extractJSONPathValue) cannot evaluate — it splits only on "." and
// "[N]". A non-identifier nested key must NOT produce a broken extractor; a
// sibling identifier key must still correlate with a clean dot path.
func TestDetectSkipsNonEvaluableBracketJSONPath(t *testing.T) {
	observations := []Observation{
		{
			ID:           1,
			ResponseBody: `{"wrap":{"x-csrf-token":"csrf-abcdef123456"},"auth":{"access_token":"tok-abcdef123456"}}`,
		},
		{
			ID:             2,
			RequestHeaders: map[string]string{"Authorization": "Bearer tok-abcdef123456", "X-CSRF": "csrf-abcdef123456"},
		},
	}

	correlations := Detect(observations)
	if len(correlations) == 0 {
		t.Fatalf("expected at least the access_token correlation, got none")
	}
	var sawAccessToken bool
	for _, corr := range correlations {
		if corr.ExtractType == "jsonpath" && strings.Contains(corr.Expression, "[") {
			t.Fatalf("emitted a bracket-quoted JSONPath the replay extractor cannot evaluate: %q", corr.Expression)
		}
		if corr.Name == "corr_access_token" {
			sawAccessToken = true
			if corr.Expression != "$.auth.access_token" {
				t.Fatalf("access_token expression = %q, want $.auth.access_token", corr.Expression)
			}
		}
	}
	if !sawAccessToken {
		t.Fatalf("identifier-keyed access_token correlation was dropped: %#v", correlations)
	}
}

// TestAuraAppPatternRequiresDescriptorShape guards the A6 fix: the Salesforce Aura
// app-descriptor detector must only fire on values shaped like a real descriptor
// (markup:// or namespace:name), not on any generic "app":"..." JSON field, which
// would otherwise produce spurious 0.9-confidence correlations in mixed HARs.
func TestAuraAppPatternRequiresDescriptorShape(t *testing.T) {
	hasAuraApp := func(body string) (bool, string) {
		for _, c := range Candidates(body, nil) {
			if c.RuleID == RuleSalesforceAuraAppDescriptor {
				return true, c.Value
			}
		}
		return false, ""
	}

	for _, body := range []string{
		`{"app":"MyMobileApp"}`,
		`{"app":"checkout"}`,
		`{"app":"dashboard-v2"}`,
	} {
		if found, value := hasAuraApp(body); found {
			t.Fatalf("body %q produced spurious Aura app descriptor %q", body, value)
		}
	}

	for _, tc := range []struct {
		body string
		want string
	}{
		{`{"app":"markup://one:one"}`, "markup://one:one"},
		{`{"app":"siteforce:communityApp"}`, "siteforce:communityApp"},
	} {
		found, value := hasAuraApp(tc.body)
		if !found || value != tc.want {
			t.Fatalf("body %q -> found=%v value=%q, want descriptor %q", tc.body, found, value, tc.want)
		}
	}
}
