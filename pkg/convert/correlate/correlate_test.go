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

func TestEncodedFormsOfIncludesFormEscapedPlus(t *testing.T) {
	forms := EncodedFormsOf("tok+/==")
	for _, want := range []string{"tok%2B%2F%3D%3D", "tok+%2F%3D%3D"} {
		var found bool
		for _, got := range forms {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("EncodedFormsOf missing %q in %#v", want, forms)
		}
	}
	if !DetectEncodedValueUsage("tok+/==", []string{"aura.token=tok%2B%2F%3D%3D"}) {
		t.Fatal("DetectEncodedValueUsage missed form-encoded + and / value")
	}
	vars := map[string]string{"tok+/==": "{{var.corr_token}}"}
	encoded := map[string]bool{"tok+/==": true}
	if got := ApplyVarsWithEncodedValues("aura.token=tok+%2F%3D%3D&raw=tok+/==", vars, encoded); got != "aura.token={{var.corr_token}}&raw={{var.corr_token}}" {
		t.Fatalf("ApplyVarsWithEncodedValues form-escaped token = %q", got)
	}
}

func TestDetectUniquifiesDuplicateCorrelationNames(t *testing.T) {
	observations := []Observation{
		{
			ID:           1,
			ResponseBody: `{"id":"1111111","user":{"id":"2222222"}}`,
		},
		{
			ID:          2,
			RequestBody: "first=1111111&second=2222222",
		},
	}

	correlations := Detect(observations)
	if len(correlations) != 2 {
		t.Fatalf("correlation count = %d, want 2: %#v", len(correlations), correlations)
	}
	names := map[string]bool{}
	for _, corr := range correlations {
		if names[corr.Name] {
			t.Fatalf("duplicate correlation name %q in %#v", corr.Name, correlations)
		}
		names[corr.Name] = true
	}
	if !names["corr_id"] || !names["corr_id_2"] {
		t.Fatalf("names = %#v, want corr_id and corr_id_2", names)
	}
	vars := BuildVarMap(correlations)
	if vars["1111111"] == "" || vars["2222222"] == "" || vars["1111111"] == vars["2222222"] {
		t.Fatalf("vars = %#v, want distinct replacements for duplicate id values", vars)
	}
}

func TestDynamicJSONKeyAvoidsSubstringFalsePositives(t *testing.T) {
	for _, key := range []string{"author", "consider", "paid", "valid", "candidate"} {
		if dynamicJSONKey(key) {
			t.Fatalf("dynamicJSONKey(%q) = true, want false", key)
		}
	}
	for _, key := range []string{"id", "userId", "user_id", "accessToken", "access_token", "session_id", "sid", "csrfToken"} {
		if !dynamicJSONKey(key) {
			t.Fatalf("dynamicJSONKey(%q) = false, want true", key)
		}
	}
}

func TestDetectSkipsBenignJSONSubstringKeys(t *testing.T) {
	observations := []Observation{
		{
			ID:           1,
			ResponseBody: `{"author":"jason-czech","consider":"review-again","paid":"invoice-complete"}`,
		},
		{
			ID:          2,
			RequestBody: "author=jason-czech&consider=review-again&paid=invoice-complete",
		},
	}
	if correlations := Detect(observations); len(correlations) != 0 {
		t.Fatalf("correlations = %#v, want benign keys ignored", correlations)
	}
}

func TestSalesforceFormExtractorUsesFormFieldKey(t *testing.T) {
	candidates := formCandidates("aura.token=tok-abcdef123456&sid=sid-abcdef123456")
	byRule := map[string]Candidate{}
	for _, candidate := range candidates {
		byRule[candidate.RuleID] = candidate
	}
	if got := byRule[RuleSalesforceAuraToken].Expression; got != `(?i)(?:^|&)aura\.token=([^&]*)` {
		t.Fatalf("aura.token expression = %q, want form field key regex", got)
	}
	if got := byRule[RuleSalesforceFrontdoorSID].Expression; got != `(?i)(?:^|&)sid=([^&]*)` {
		t.Fatalf("sid expression = %q, want form field key regex", got)
	}
	if byRule[RuleSalesforceAuraToken].Convert != "url_decode" || byRule[RuleSalesforceFrontdoorSID].Convert != "url_decode" {
		t.Fatalf("form extractors = %#v, want url_decode convert", byRule)
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

func TestDetectSetCookieSkipsAttributes(t *testing.T) {
	observations := []Observation{
		{ID: 1, ResponseHeaders: map[string]string{"Set-Cookie": "Path=/account; Max-Age=3600; session=sid-abcdef123456; HttpOnly"}},
		{ID: 2, RequestHeaders: map[string]string{"Cookie": "session=sid-abcdef123456"}},
	}
	correlations := Detect(observations)
	if len(correlations) != 1 {
		t.Fatalf("correlation count = %d, want only session cookie: %#v", len(correlations), correlations)
	}
	if correlations[0].Name != "corr_cookie_session" {
		t.Fatalf("unexpected correlation: %#v", correlations[0])
	}
}

func TestDetectLocationQueryEmitsReplayableHeaderRegex(t *testing.T) {
	observations := []Observation{
		{ID: 1, ResponseHeaders: map[string]string{"Location": "https://idp.example.test/callback?code=code-abcdef123456&state=state-abcdef123456"}},
		{ID: 2, RequestURL: "https://client.example.test/callback?code=code-abcdef123456&state=state-abcdef123456"},
	}
	correlations := Detect(observations)
	var found bool
	for _, corr := range correlations {
		if corr.RuleID == RuleLocationParam && corr.Value == "code-abcdef123456" {
			found = true
			if corr.Source != "header" || corr.ExtractType != "regex" || strings.Contains(corr.ExtractType, "header_query") {
				t.Fatalf("location extractor = %#v, want header regex", corr)
			}
			if !strings.Contains(corr.Expression, "Location") || !strings.Contains(corr.Expression, "code") {
				t.Fatalf("location regex = %q, want header/query expression", corr.Expression)
			}
		}
	}
	if !found {
		t.Fatalf("location code correlation not found: %#v", correlations)
	}
}

func TestDetectSkipsShortStaticLocationRedirect(t *testing.T) {
	observations := []Observation{
		{ID: 1, ResponseHeaders: map[string]string{"Location": "/login"}},
		{ID: 2, RequestURL: "https://app.example.test/login"},
	}
	if correlations := Detect(observations); len(correlations) != 0 {
		t.Fatalf("correlations = %#v, want short static redirect ignored", correlations)
	}
}

func TestDetectWholeLocationRedirectRequiresTokenLikeValue(t *testing.T) {
	observations := []Observation{
		{ID: 1, ResponseHeaders: map[string]string{"Location": "/handoff/state-a1b2c3d4e5"}},
		{ID: 2, RequestURL: "https://app.example.test/handoff/state-a1b2c3d4e5"},
	}
	correlations := Detect(observations)
	if len(correlations) != 1 {
		t.Fatalf("correlations = %#v, want token-like bare redirect correlated", correlations)
	}
	if correlations[0].Name != "corr_location" || correlations[0].ExtractType != "header" {
		t.Fatalf("correlation = %#v, want whole Location header fallback", correlations[0])
	}
}

func TestDetectXMLAttributeExtractorTargetsAttributeValue(t *testing.T) {
	const value = "soap-corr-abcdef123456"
	observations := []Observation{
		{ID: 1, ResponseBody: `<Envelope><Body><CreateOrderResponse correlationId="` + value + `">ok</CreateOrderResponse></Body></Envelope>`},
		{ID: 2, RequestBody: `correlationId=` + value},
	}
	correlations := Detect(observations)
	for _, corr := range correlations {
		if corr.Value == value {
			if corr.ExtractType != "xpath" || corr.Expression != "//@*[local-name()='correlationId']" {
				t.Fatalf("xml attr extractor = %#v, want replayable attribute-node XPath", corr)
			}
			return
		}
	}
	t.Fatalf("xml attribute correlation not found: %#v", correlations)
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
		if corr.ExtractType == "jsonpath" && strings.Contains(corr.Expression, "[\"") {
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

func TestDetectRootArrayJSONPathIsReplayEvaluable(t *testing.T) {
	observations := []Observation{
		{ID: 1, ResponseBody: `[{"id":"1234567","name":"Ada"}]`},
		{ID: 2, RequestURL: "https://api.example.test/users/1234567"},
	}
	correlations := Detect(observations)
	if len(correlations) != 1 {
		t.Fatalf("correlation count = %d, want 1: %#v", len(correlations), correlations)
	}
	if correlations[0].ExtractType != "jsonpath" || correlations[0].Expression != "$[0].id" {
		t.Fatalf("correlation = %#v, want jsonpath $[0].id", correlations[0])
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
