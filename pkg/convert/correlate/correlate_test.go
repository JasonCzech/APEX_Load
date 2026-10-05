package correlate

import (
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/protocol"
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
	if got := ApplyVarsWithEncodedValues("query=tok%2B%2F%3D%3D&variant=tok+%2F%3D%3D&raw=tok+/==", vars, encoded); got != "query={{var.corr_token}}&variant=tok+%2F%3D%3D&raw={{var.corr_token}}" {
		t.Fatalf("ApplyVarsWithEncodedValues must rewrite only the exact runtime query spelling, got %q", got)
	}
}

func TestQueryEncodedFormOfIsExactRuntimeSpelling(t *testing.T) {
	if got, want := QueryEncodedFormOf("token /+ value"), "token+%2F%2B+value"; got != want {
		t.Fatalf("QueryEncodedFormOf() = %q, want %q", got, want)
	}
	if got := QueryEncodedFormOf("plain-token"); got != "" {
		t.Fatalf("unreserved value should not have a distinct encoded spelling, got %q", got)
	}
}

func TestEncodedReuseExtractorConvertAvoidsDoubleEncoding(t *testing.T) {
	tests := []struct {
		name          string
		sourceConvert string
		want          string
		ok            bool
	}{
		{name: "raw source", want: "url_encode", ok: true},
		{name: "already encoded source", sourceConvert: "url_decode", want: "", ok: true},
		{name: "already encoded output", sourceConvert: "url_encode", want: "url_encode", ok: true},
		{name: "unsupported chain", sourceConvert: "html_decode", ok: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := EncodedReuseExtractorConvert(tc.sourceConvert)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("EncodedReuseExtractorConvert(%q) = %q, %v; want %q, %v", tc.sourceConvert, got, ok, tc.want, tc.ok)
			}
		})
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
	candidates := formCandidates("aura.token=tok-abcdef123456&sid=sid-abcdef123456&cshc=cshc-abcdef123456")
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
	if got := byRule[RuleSalesforceCSHC].Expression; got != `(?i)(?:^|&)cshc=([^&]*)` {
		t.Fatalf("cshc expression = %q, want form field key regex", got)
	}
	if byRule[RuleSalesforceAuraToken].Convert != "url_decode" || byRule[RuleSalesforceFrontdoorSID].Convert != "url_decode" || byRule[RuleSalesforceCSHC].Convert != "url_decode" {
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

func TestDetectSetCookieSIDUsesCookieGrammarInsteadOfSalesforceRegex(t *testing.T) {
	const value = "sid-cookie-value-123456"
	correlations := Detect([]Observation{
		{ID: 1, ResponseHeaders: map[string]string{"Set-Cookie": "sid=" + value + "; Path=/; HttpOnly"}},
		{ID: 2, RequestHeaders: map[string]string{"Cookie": "sid=" + value}},
	})
	if len(correlations) != 1 {
		t.Fatalf("correlations = %#v, want one", correlations)
	}
	if correlations[0].RuleID != RuleSetCookieSession || correlations[0].Name != "corr_cookie_sid" {
		t.Fatalf("sid cookie correlation used broad Salesforce detection: %#v", correlations[0])
	}
}

func TestDetectSetCookieRejectsAttributePrefixedMalformedField(t *testing.T) {
	observations := []Observation{
		{ID: 1, ResponseHeaders: map[string]string{"Set-Cookie": "Path=/account; Max-Age=3600; session=sid-abcdef123456; HttpOnly"}},
		{ID: 2, RequestHeaders: map[string]string{"Cookie": "session=sid-abcdef123456"}},
	}
	correlations := Detect(observations)
	if len(correlations) != 0 {
		t.Fatalf("attribute-prefixed malformed Set-Cookie produced correlations: %#v", correlations)
	}
}

func TestDetectRepeatedSameNameSetCookieUsesOrdinalRegex(t *testing.T) {
	const second = "sid-second-abcdef123456"
	correlations := Detect([]Observation{
		{ID: 1, ResponseHeaders: map[string]string{"Set-Cookie": "session=sid-first-abcdef123456; Path=/one\nsession=" + second + "; Path=/two"}},
		{ID: 2, RequestHeaders: map[string]string{"Cookie": "session=" + second}},
	})
	if len(correlations) != 1 {
		t.Fatalf("correlations = %#v, want reused second cookie", correlations)
	}
	corr := correlations[0]
	if corr.ExtractType != "regex" || !strings.Contains(corr.Expression, "{1}") {
		t.Fatalf("correlation = %#v, want second-occurrence cookie regex", corr)
	}
	re, err := regexp.Compile(corr.Expression)
	if err != nil {
		t.Fatalf("compile repeated Set-Cookie regex: %v", err)
	}
	flattened := "Set-Cookie: session=sid-first-abcdef123456; Path=/one\nDate: now\nSet-Cookie: session=" + second + "; Path=/two\n"
	match := re.FindStringSubmatch(flattened)
	if len(match) < 2 || match[1] != second {
		t.Fatalf("cookie regex %q captured %#v, want %q", corr.Expression, match, second)
	}
}

func TestDetectRepeatedDynamicHeaderUsesOrdinalRegex(t *testing.T) {
	const value = "rotating-token-abcdef123456"
	correlations := Detect([]Observation{
		{ID: 1, ResponseHeaders: map[string]string{"X-Session-Token": "fixed-token-value\n" + value}},
		{ID: 2, RequestHeaders: map[string]string{"X-Session-Token": value}},
	})
	if len(correlations) != 1 {
		t.Fatalf("correlations = %#v, want the reused second header value", correlations)
	}
	corr := correlations[0]
	if corr.ExtractType != "regex" || !strings.Contains(corr.Expression, "{1}") {
		t.Fatalf("correlation = %#v, want second-occurrence header regex", corr)
	}
	re, err := regexp.Compile(corr.Expression)
	if err != nil {
		t.Fatalf("compile repeated-header expression: %v", err)
	}
	match := re.FindStringSubmatch("X-Session-Token: fixed-token-value\nX-Session-Token: " + value + "\n")
	if len(match) < 2 || match[1] != value {
		t.Fatalf("header regex %q captured %#v, want %q", corr.Expression, match, value)
	}
}

func TestHeaderCandidatesPreserveRuntimeValueWhitespace(t *testing.T) {
	const (
		first  = "  first-token-123456  "
		second = "\tsecond-token-654321  "
	)
	candidates := headerCandidates(map[string]string{"X-Session-Token": first + "\n" + second})
	if len(candidates) != 2 {
		t.Fatalf("header candidates = %#v, want both occurrences", candidates)
	}
	flattened := "X-Session-Token: " + first + "\nX-Session-Token: " + second + "\n"
	for index, want := range []string{first, second} {
		candidate := candidates[index]
		if candidate.Value != want {
			t.Fatalf("candidate %d value = %q, want exact header bytes %q", index, candidate.Value, want)
		}
		match := regexp.MustCompile(candidate.Expression).FindStringSubmatch(flattened)
		if len(match) < 2 || match[1] != want {
			t.Fatalf("candidate %d regex %q captured %#v, want %q", index, candidate.Expression, match, want)
		}
	}

	single := headerCandidates(map[string]string{"X-Session-Token": first})
	if len(single) != 1 || single[0].ExtractType != "header" || single[0].Value != first {
		t.Fatalf("single header candidate = %#v, want direct exact header value", single)
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
			if corr.ExtractType != "regex" {
				t.Fatalf("xml attr extractor = %#v, want byte-exact regex", corr)
			}
			if got := candidateRegexCapture(t, Candidate{ExtractType: corr.ExtractType, Expression: corr.Expression}, observations[0].ResponseBody); got != value {
				t.Fatalf("xml attr extractor captured %q, want %q", got, value)
			}
			return
		}
	}
	t.Fatalf("xml attribute correlation not found: %#v", correlations)
}

func TestDetectXMLPreservesRuntimeTextWhitespace(t *testing.T) {
	const value = "  csrf-token-abcdef123456  "
	source := Observation{ID: 1, ResponseBody: `<Envelope><csrfToken>` + value + `</csrfToken></Envelope>`}

	trimmedConsumer := Observation{ID: 2, RequestBody: "csrf=" + strings.TrimSpace(value)}
	if correlations := Detect([]Observation{source, trimmedConsumer}); len(correlations) != 0 {
		t.Fatalf("trimmed consumer correlations = %#v, want none because XPath replay preserves text whitespace", correlations)
	}

	exactConsumer := Observation{ID: 2, RequestBody: "csrf=" + value}
	correlations := Detect([]Observation{source, exactConsumer})
	if len(correlations) != 1 {
		t.Fatalf("exact consumer correlations = %#v, want one", correlations)
	}
	if correlations[0].Value != value || correlations[0].ExtractType != "regex" {
		t.Fatalf("XML correlation = %#v, want byte-exact regex value %q", correlations[0], value)
	}
	if got := candidateRegexCapture(t, Candidate{ExtractType: correlations[0].ExtractType, Expression: correlations[0].Expression}, source.ResponseBody); got != value {
		t.Fatalf("XML regex captured %q, want whitespace-preserving %q", got, value)
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

func TestDetectSalesforceHeaderCandidateUsesHeaderExtractor(t *testing.T) {
	const sid = "00Dxx0000000001!AQ0AQHabcdef"
	location := "https://acme.my.salesforce.com/secur/frontdoor.jsp?sid=" + sid
	correlations := Detect([]Observation{
		{ID: 1, ResponseHeaders: map[string]string{"Location": location}},
		{ID: 2, RequestURL: location},
	})

	var found *Correlation
	for index := range correlations {
		if correlations[index].RuleID == RuleSalesforceFrontdoorSID {
			found = &correlations[index]
			break
		}
	}
	if found == nil {
		t.Fatalf("frontdoor SID correlation not found: %#v", correlations)
	}
	if found.Source != "header" || found.ExtractType != "regex" {
		t.Fatalf("header candidate = %#v, want header regex extractor", *found)
	}
	re := regexp.MustCompile(found.Expression)
	match := re.FindStringSubmatch("Location: " + location)
	if len(match) < 2 || match[1] != sid {
		t.Fatalf("header extractor %q captured %#v, want SID %q in group 1", found.Expression, match, sid)
	}
}

func TestSalesforceRegexCandidatesCaptureSelectedValueInGroupOne(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		ruleID string
		value  string
	}{
		{
			name:   "visualforce viewstate",
			body:   `<input type="hidden" name="com.salesforce.visualforce.ViewState" value="STATE-abcdef123456">`,
			ruleID: RuleSalesforceViewState,
			value:  "STATE-abcdef123456",
		},
		{
			name:   "aura page URI form spelling",
			body:   `prefix aura.pageURI=/lightning/page/abcdef123456&suffix=1`,
			ruleID: RuleSalesforceAuraPageURI,
			value:  "/lightning/page/abcdef123456",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var found *Candidate
			for _, candidate := range salesforceRegexCandidates(tt.body) {
				if candidate.RuleID == tt.ruleID && candidate.Value == tt.value {
					copy := candidate
					found = &copy
					break
				}
			}
			if found == nil {
				t.Fatalf("candidate %s/%q not found", tt.ruleID, tt.value)
			}
			re := regexp.MustCompile(found.Expression)
			match := re.FindStringSubmatch(tt.body)
			if len(match) < 2 || match[1] != tt.value {
				t.Fatalf("extractor %q captured %#v, want %q in group 1", found.Expression, match, tt.value)
			}
		})
	}
}

func TestMalformedJSONFallbackEmitsReplayableRegex(t *testing.T) {
	const token = "csrf-abcdef123456"
	body := ")]}'\n" + `{"csrf_token":"` + token + `","valid":"static-value"}`
	candidates := jsonCandidates(body)
	if len(candidates) != 1 {
		t.Fatalf("fallback candidates = %#v, want only the token-boundary CSRF field", candidates)
	}
	candidate := candidates[0]
	if candidate.Value != token || candidate.ExtractType != "regex" {
		t.Fatalf("fallback candidate = %#v, want replayable regex for %q", candidate, token)
	}
	match := regexp.MustCompile(candidate.Expression).FindStringSubmatch(body)
	if len(match) < 2 || match[1] != token {
		t.Fatalf("fallback regex %q captured %#v, want %q", candidate.Expression, match, token)
	}

	correlations := Detect([]Observation{
		{ID: 1, ResponseBody: body},
		{ID: 2, RequestBody: "csrf_token=" + token},
	})
	if len(correlations) != 1 || correlations[0].ExtractType != "regex" {
		t.Fatalf("correlations = %#v, want one regex-backed fallback", correlations)
	}
}

func TestMalformedJSONCaseVariantsKeepDistinctRuntimeIdentity(t *testing.T) {
	body := ")]}'\n" + `{"csrf_token":"first-token-123456","CSRF_TOKEN":"second-token-654321"}`
	candidates := jsonCandidates(body)
	if len(candidates) != 2 {
		t.Fatalf("fallback candidates = %#v, want both case-distinct fields", candidates)
	}
	for index, want := range []string{"first-token-123456", "second-token-654321"} {
		if got := candidateRegexCapture(t, candidates[index], body); got != want {
			t.Fatalf("candidate %d captured %q, want %q", index, got, want)
		}
	}
}

func TestRepeatedMalformedJSONFieldsUseRegexOrdinals(t *testing.T) {
	body := ")]}'\n" + `{"csrf_token":"first-token-123456","csrf_token":"second-token-654321"}`
	candidates := jsonCandidates(body)
	if len(candidates) != 2 {
		t.Fatalf("fallback candidates = %#v, want two repeated fields", candidates)
	}
	if candidates[0].ExtractType != "regex" {
		t.Fatalf("first fallback candidate = %#v, want first-match regex", candidates[0])
	}
	if candidates[1].ExtractType != "regex_ordinal" {
		t.Fatalf("second fallback candidate = %#v, want regex_ordinal", candidates[1])
	}
	expression, ordinal, ok := decodeRegexOrdinalExpression(candidates[1].Expression)
	if !ok || expression != jsonFallbackStringFieldRegex("csrf_token") || ordinal != 2 {
		t.Fatalf("second fallback config = %q/%d/%t", expression, ordinal, ok)
	}
}

func TestRepeatedHiddenFieldsUseRegexOrdinals(t *testing.T) {
	tests := []struct {
		name       string
		candidates func(string) []Candidate
		body       string
	}{
		{
			name:       "html",
			candidates: htmlInputCandidates,
			body: `<input name="csrf_token" value="first-token-123456">` +
				`<input name="csrf_token" value="second-token-654321">`,
		},
		{
			name:       "aspnet",
			candidates: aspNetHiddenCandidates,
			body: `<input name="__VIEWSTATE" value="first-state-123456">` +
				`<input name="__VIEWSTATE" value="second-state-654321">`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidates := test.candidates(test.body)
			if len(candidates) != 2 {
				t.Fatalf("candidates = %#v, want two", candidates)
			}
			if candidates[1].ExtractType != "regex_ordinal" {
				t.Fatalf("second candidate = %#v, want regex_ordinal", candidates[1])
			}
			_, ordinal, ok := decodeRegexOrdinalExpression(candidates[1].Expression)
			if !ok || ordinal != 2 {
				t.Fatalf("second candidate ordinal = %d/%t", ordinal, ok)
			}
		})
	}
}

func TestHTMLHiddenExtractorMatchesDiscoveryCaseAndShape(t *testing.T) {
	const value = "target-token-123456"
	body := `<div name="csrf_token" value="wrong-token-123456"></div>` +
		`<INPUT NAME="CSRF_TOKEN" VALUE="abc">` +
		`<INPUT NAME="CSRF_TOKEN" VALUE="` + value + `">`
	candidates := htmlInputCandidates(body)
	if len(candidates) != 1 {
		t.Fatalf("HTML candidates = %#v, want only the eligible input", candidates)
	}
	if got := candidateRegexCapture(t, candidates[0], body); got != value {
		t.Fatalf("HTML runtime regex captured %q, want %q", got, value)
	}
}

func TestCandidatesCheckedSkipsRegexOrdinalBeyondRuntimeLimit(t *testing.T) {
	var body strings.Builder
	for index := 0; index <= maxCorrelationRegexOrdinal; index++ {
		fmt.Fprintf(&body, `<input name="csrf_token" value="token-%06d">`, index)
	}
	candidates, err := CandidatesChecked(body.String(), nil)
	if err != nil {
		t.Fatalf("CandidatesChecked regex ordinal overflow must degrade, got error %v", err)
	}
	var html []Candidate
	for _, candidate := range candidates {
		if candidate.RuleID == RuleHTMLHiddenToken {
			html = append(html, candidate)
		}
	}
	// Ordinals 1..maxCorrelationRegexOrdinal stay replayable; the one occurrence
	// beyond the runtime limit is omitted instead of aborting the analysis.
	if len(html) != maxCorrelationRegexOrdinal {
		t.Fatalf("HTML candidates = %d, want %d replayable ordinals", len(html), maxCorrelationRegexOrdinal)
	}
}

func TestRepeatedSalesforceRegexCandidatesUseRegexOrdinals(t *testing.T) {
	body := `{"aura.token":"first-aura-token-123456","aura.token":"second-aura-token-654321"}`
	var aura []Candidate
	for _, candidate := range salesforceRegexCandidates(body) {
		if candidate.RuleID == RuleSalesforceAuraToken {
			aura = append(aura, candidate)
		}
	}
	if len(aura) != 2 || aura[1].ExtractType != "regex_ordinal" {
		t.Fatalf("Aura candidates = %#v, want repeated body match ordinals", aura)
	}
	_, ordinal, ok := decodeRegexOrdinalExpression(aura[1].Expression)
	if !ok || ordinal != 2 {
		t.Fatalf("second Aura ordinal = %d/%t", ordinal, ok)
	}
}

func TestSalesforceKnownJSONFieldsUseOnlyFaithfulJSONPathValues(t *testing.T) {
	t.Run("top-level escaped value is decoded like runtime", func(t *testing.T) {
		body := `{"nextRecordsUrl":"\u002fservices\u002fdata\u002fv61.0\u002fquery\u002f01g-records"}`
		var found *Candidate
		for _, candidate := range salesforceRegexCandidates(body) {
			if candidate.RuleID == RuleSalesforceNextRecordsURL {
				copy := candidate
				found = &copy
				break
			}
		}
		if found == nil || found.ExtractType != "jsonpath" || found.Expression != "$.nextRecordsUrl" {
			t.Fatalf("candidate = %#v, want exact top-level JSONPath", found)
		}
		if want := "/services/data/v61.0/query/01g-records"; found.Value != want {
			t.Fatalf("candidate value = %q, want runtime-decoded %q", found.Value, want)
		}
	})

	for _, test := range []struct {
		name string
		body string
	}{
		{name: "case variant", body: `{"NextRecordsUrl":"/services/data/v61.0/query/01g-case"}`},
		{name: "nested field", body: `{"result":{"nextRecordsUrl":"/services/data/v61.0/query/01g-nested"}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			var found *Candidate
			for _, candidate := range salesforceRegexCandidates(test.body) {
				if candidate.RuleID == RuleSalesforceNextRecordsURL {
					copy := candidate
					found = &copy
					break
				}
			}
			if found == nil || found.ExtractType != "regex" {
				t.Fatalf("candidate = %#v, want regex because JSONPath is not source-identical", found)
			}
			if got := candidateRegexCapture(t, *found, test.body); got != found.Value {
				t.Fatalf("runtime regex captured %q, candidate value %q", got, found.Value)
			}
		})
	}
}

func TestLocationCandidatesPreserveWireEncodingAndRepeatedOrdinals(t *testing.T) {
	const (
		firstLocation  = "https://example.test/callback?state=first%2Fvalue"
		secondLocation = "https://example.test/callback?state=space+token%2F123456&state=last%2Btoken"
	)
	headers := map[string]string{"Location": firstLocation + "\n" + secondLocation}
	var locations []Candidate
	for _, candidate := range headerCandidates(headers) {
		if candidate.RuleID == RuleLocationParam {
			locations = append(locations, candidate)
		}
	}
	if len(locations) != 3 {
		t.Fatalf("Location candidates = %#v, want all three query occurrences", locations)
	}
	flattened := "Location: " + firstLocation + "\nLocation: " + secondLocation + "\n"
	wants := []struct {
		value string
		wire  string
	}{
		{value: "first/value", wire: "first%2Fvalue"},
		{value: "space token/123456", wire: "space+token%2F123456"},
		{value: "last+token", wire: "last%2Btoken"},
	}
	for index, want := range wants {
		candidate := locations[index]
		if candidate.Value != want.value || candidate.Convert != "url_decode" {
			t.Fatalf("Location candidate %d = %#v, want decoded value %q with url_decode", index, candidate, want.value)
		}
		match := regexp.MustCompile(candidate.Expression).FindStringSubmatch(flattened)
		if len(match) < 2 || match[1] != want.wire {
			t.Fatalf("Location candidate %d regex %q captured %#v, want wire value %q", index, candidate.Expression, match, want.wire)
		}
	}

	correlations, err := DetectChecked([]Observation{
		{ID: 1, ResponseHeaders: headers},
		{ID: 2, RequestURL: "https://example.test/use?state=space+token%2F123456"},
	})
	if err != nil {
		t.Fatalf("DetectChecked returned error: %v", err)
	}
	var found *Correlation
	for index := range correlations {
		if correlations[index].Value == "space token/123456" {
			found = &correlations[index]
			break
		}
	}
	if found == nil || found.Convert != "url_decode" {
		t.Fatalf("correlations = %#v, want decoded second-Location correlation", correlations)
	}
	validator := NewExactQueryEncodingValidator()
	if !validator.Matches(1, "", headers, *found, "space+token%2F123456") {
		t.Fatalf("second Location extractor did not validate its exact wire spelling: %#v", *found)
	}
	if convert, ok := EncodedReuseExtractorConvert(found.Convert); !ok || convert != "" {
		t.Fatalf("encoded reuse conversion = %q/%t, want raw wire capture without double encoding", convert, ok)
	}
}

func TestJSONNumericCandidatesAreLosslessAndRequireBoundedIDs(t *testing.T) {
	const largeID = "9007199254740993"
	body := `{"order_id":` + largeID + `,"id":2,"valid":123456,"ratio_id":12.34}`
	candidates := jsonCandidates(body)
	if len(candidates) != 1 {
		t.Fatalf("numeric candidates = %#v, want only the 4+ digit token-bounded id", candidates)
	}
	if candidates[0].Value != largeID || candidates[0].Expression != "$.order_id" {
		t.Fatalf("large numeric candidate = %#v, want exact lossless value %s", candidates[0], largeID)
	}

	correlations := Detect([]Observation{
		{ID: 1, ResponseBody: body},
		{ID: 2, RequestURL: "https://api.example.test/orders/" + largeID + "/v2"},
	})
	if len(correlations) != 1 || correlations[0].Value != largeID {
		t.Fatalf("large-id correlations = %#v, want exact %s", correlations, largeID)
	}

	fallbackBody := ")]}'\n" + `{"valid":123456,"order_id":` + largeID + `}`
	fallback := jsonCandidates(fallbackBody)
	if len(fallback) != 1 || fallback[0].Value != largeID || fallback[0].ExtractType != "regex" {
		t.Fatalf("numeric fallback = %#v, want one token-bounded lossless regex candidate", fallback)
	}
	match := regexp.MustCompile(fallback[0].Expression).FindStringSubmatch(fallbackBody)
	if len(match) < 2 || match[1] != largeID {
		t.Fatalf("numeric fallback regex %q captured %#v", fallback[0].Expression, match)
	}
}

func TestRepeatedXMLNamesUseRuntimeRegexOrdinals(t *testing.T) {
	body := `<root correlationId="      ">` +
		`<Token>      </Token>` +
		`<item correlationId="attr-one-123"><Token>value-one-123</Token></item>` +
		`<item correlationId="attr-two-456"><Token>value-two-456</Token></item>` +
		`</root>`
	candidates := xmlCandidates(body)
	byValue := make(map[string]Candidate, len(candidates))
	for _, candidate := range candidates {
		byValue[candidate.Value] = candidate
	}
	for _, value := range []string{"value-one-123", "value-two-456", "attr-one-123", "attr-two-456"} {
		candidate, ok := byValue[value]
		if !ok {
			t.Fatalf("candidate %q missing from %#v", value, candidates)
		}
		if candidate.ExtractType != "regex_ordinal" {
			t.Errorf("candidate %q type = %q, want regex_ordinal because a non-candidate runtime match precedes it", value, candidate.ExtractType)
		}
		if got := candidateRegexCapture(t, candidate, body); got != value {
			t.Errorf("candidate %q runtime capture = %q", value, got)
		}
	}
	if _, exists := byValue["      "]; exists {
		t.Fatalf("whitespace-only XML value unexpectedly emitted as a candidate: %#v", candidates)
	}
}

func candidateRegexCapture(t *testing.T, candidate Candidate, input string) string {
	t.Helper()
	expression := candidate.Expression
	ordinal := 1
	if candidate.ExtractType == "regex_ordinal" {
		var ok bool
		expression, ordinal, ok = decodeRegexOrdinalExpression(expression)
		if !ok {
			t.Fatalf("invalid regex ordinal expression %q", candidate.Expression)
		}
	}
	matches := regexp.MustCompile(expression).FindAllStringSubmatch(input, ordinal)
	if len(matches) < ordinal || len(matches[ordinal-1]) < 2 {
		t.Fatalf("regex %q ordinal %d did not capture input", expression, ordinal)
	}
	return matches[ordinal-1][1]
}

func TestApplyVarsDoesNotRewriteInsertedTemplates(t *testing.T) {
	vars := map[string]string{
		"opaque-token-value": "{{var.corr_access_token}}",
		"access":             "{{var.corr_csrf_token}}",
	}
	if got, want := ApplyVars("opaque-token-value access", vars), "{{var.corr_access_token}} {{var.corr_csrf_token}}"; got != want {
		t.Fatalf("ApplyVars() = %q, want non-recursive %q", got, want)
	}

	encodedVars := map[string]string{
		"opaque token/value": "{{var.corr_access_token}}",
		"access":             "{{var.corr_csrf_token}}",
	}
	encodedValues := map[string]bool{"opaque token/value": true}
	if got, want := ApplyVarsWithEncodedValues("value=opaque+token%2Fvalue&label=access", encodedVars, encodedValues), "value={{var.corr_access_token}}&label={{var.corr_csrf_token}}"; got != want {
		t.Fatalf("ApplyVarsWithEncodedValues() = %q, want non-recursive %q", got, want)
	}
}

func TestApplyVarsWithOutputLimitBoundsExpandingReplacement(t *testing.T) {
	got, truncated := ApplyVarsWithOutputLimit(
		strings.Repeat("x", 100),
		map[string]string{"x": "[REDACTED]"},
		25,
	)
	if !truncated || len(got) != 25 {
		t.Fatalf("bounded replacement = %q, truncated %t; want exactly 25 retained bytes", got, truncated)
	}
	if strings.Contains(got, "x") {
		t.Fatalf("bounded replacement retained raw secret bytes: %q", got)
	}

	got, truncated = ApplyVarsWithOutputLimit("safe", nil, 4)
	if truncated || got != "safe" {
		t.Fatalf("exact-boundary replacement = %q, %t", got, truncated)
	}
}

func TestCandidatesCheckedRejectsCandidateOverflow(t *testing.T) {
	var body strings.Builder
	body.WriteByte('[')
	for index := 0; index <= maxCorrelationCandidatesPerObservation; index++ {
		if index > 0 {
			body.WriteByte(',')
		}
		fmt.Fprintf(&body, `{"token":"token-%06d"}`, index)
	}
	body.WriteByte(']')

	candidates, err := CandidatesChecked(body.String(), nil)
	if err != nil {
		t.Fatalf("CandidatesChecked overflow must truncate, got error %v", err)
	}
	if len(candidates) != maxCorrelationCandidatesPerObservation {
		t.Fatalf("CandidatesChecked overflow candidates = %d, want the per-observation cap %d", len(candidates), maxCorrelationCandidatesPerObservation)
	}
	if correlations, err := DetectChecked([]Observation{{ID: 1, ResponseBody: body.String()}}); !errors.Is(err, ErrCorrelationWorkLimit) || correlations != nil {
		t.Fatalf("DetectChecked overflow = %#v, error %v; want nil correlations with ErrCorrelationWorkLimit", correlations, err)
	}
}

func TestExactlyCandidateCapIsComplete(t *testing.T) {
	var body strings.Builder
	body.WriteByte('[')
	for index := 0; index < maxCorrelationCandidatesPerObservation; index++ {
		if index > 0 {
			body.WriteByte(',')
		}
		fmt.Fprintf(&body, `{"token":"token-%06d"}`, index)
	}
	body.WriteByte(']')

	candidates, truncated, err := candidatesCheckedWithTruncation(body.String(), nil)
	if err != nil {
		t.Fatalf("exact-cap candidates: %v", err)
	}
	if len(candidates) != maxCorrelationCandidatesPerObservation || truncated {
		t.Fatalf("exact-cap candidates = %d, truncated=%t; want complete cap", len(candidates), truncated)
	}
	correlations, warnings, err := DetectCheckedWithWarnings([]Observation{{ID: 1, ResponseBody: body.String()}})
	if err != nil || len(warnings) != 0 || len(correlations) != 0 {
		t.Fatalf("exact-cap detection = %#v, warnings=%v, error=%v; want complete empty result", correlations, warnings, err)
	}
	if correlations, err := DetectChecked([]Observation{{ID: 1, ResponseBody: body.String()}}); err != nil || len(correlations) != 0 {
		t.Fatalf("DetectChecked exact cap = %#v, error=%v", correlations, err)
	}
}

// Regression for the review finding that ~100 candidates over a ~700KB request
// surface (well inside the documented evidence caps) hard-failed the analysis:
// realistic recordings must complete with all correlations detected.
func TestDetectCheckedHandlesRealisticCandidateBySurfaceProducts(t *testing.T) {
	const candidateCount = 100
	responseParts := make([]string, 0, candidateCount)
	requestParts := make([]string, 0, candidateCount)
	for index := 0; index < candidateCount; index++ {
		value := fmt.Sprintf("token-%06d-secret", index)
		responseParts = append(responseParts, fmt.Sprintf(`"token_%d":"%s"`, index, value))
		requestParts = append(requestParts, value)
	}
	requestBody := strings.Join(requestParts, "&") + strings.Repeat("x", 700_000)
	observations := []Observation{
		{ID: 1, ResponseBody: "{" + strings.Join(responseParts, ",") + "}"},
		{ID: 2, RequestBody: requestBody},
	}

	correlations, warnings, err := DetectCheckedWithWarnings(observations)
	if err != nil {
		t.Fatalf("DetectChecked realistic workload error = %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("DetectChecked realistic workload warnings = %v, want none", warnings)
	}
	if len(correlations) != candidateCount {
		t.Fatalf("DetectChecked correlations = %d, want %d", len(correlations), candidateCount)
	}
}

func TestDetectCheckedWithWarningsTruncatesWhenSearchBudgetExhausted(t *testing.T) {
	restore := maxCorrelationSearchBytes
	maxCorrelationSearchBytes = 64
	t.Cleanup(func() { maxCorrelationSearchBytes = restore })

	observations := []Observation{
		{ID: 1, ResponseBody: `{"csrf_token":"token-value-123456"}`},
		{ID: 2, RequestBody: "token-value-123456" + strings.Repeat("x", 512)},
	}
	correlations, warnings, err := DetectCheckedWithWarnings(observations)
	if err != nil {
		t.Fatalf("budget exhaustion must degrade, got error %v", err)
	}
	if len(correlations) != 0 {
		t.Fatalf("truncated analysis correlations = %#v, want none", correlations)
	}
	if len(warnings) == 0 {
		t.Fatal("budget exhaustion must record a truncation warning")
	}
	if checked, checkedErr := DetectChecked(observations); !errors.Is(checkedErr, ErrCorrelationWorkLimit) || checked != nil {
		t.Fatalf("DetectChecked truncated analysis = %#v, error %v; want nil correlations with ErrCorrelationWorkLimit", checked, checkedErr)
	}
	// The unchecked legacy form stays silent but must not fail either.
	if correlations := Detect(observations); len(correlations) != 0 {
		t.Fatalf("legacy Detect after truncation = %#v, want none", correlations)
	}
}

func TestMalformedJSONFallbackRejectsRawMatchOverflowBeforeFiltering(t *testing.T) {
	var body strings.Builder
	body.WriteString(")]}'\n{")
	for index := 0; index <= maxCorrelationCandidatesPerObservation; index++ {
		if index > 0 {
			body.WriteByte(',')
		}
		// "valid" passes the broad raw-regex shape because it contains "id",
		// but dynamicJSONKey deliberately rejects it as a false positive.
		fmt.Fprintf(&body, `"valid":"static-%06d"`, index)
	}
	body.WriteString(`,"csrf_token":"target-token-123456"}`)

	// The bounded fallback keeps only the first per-observation-cap raw matches;
	// values beyond that window are skipped instead of failing the analysis.
	candidates, err := CandidatesChecked(body.String(), nil)
	if err != nil {
		t.Fatalf("CandidatesChecked raw fallback overflow must truncate, got error %v", err)
	}
	for _, candidate := range candidates {
		if candidate.Value == "target-token-123456" {
			t.Fatalf("candidate beyond the bounded raw-match window must be skipped, got %#v", candidate)
		}
	}
	if correlations, err := DetectChecked([]Observation{{ID: 1, ResponseBody: body.String()}}); !errors.Is(err, ErrCorrelationWorkLimit) || correlations != nil {
		t.Fatalf("DetectChecked raw fallback overflow = %#v, error %v; want fail-closed work-limit error", correlations, err)
	}
}

func TestFormCandidatesUseReplayableOrdinalsAndDeterministicKeyOrder(t *testing.T) {
	body := "sid=sid-first-123456&sid=sid-second-654321&aura.token=aura-token-123456"
	candidates := formCandidates(body)
	if len(candidates) != 3 {
		t.Fatalf("form candidates = %#v, want three", candidates)
	}
	if candidates[0].RuleID != RuleSalesforceAuraToken {
		t.Fatalf("first candidate = %#v, want sorted aura.token before sid", candidates[0])
	}

	for index, candidate := range candidates[1:] {
		if candidate.RuleID != RuleSalesforceFrontdoorSID || candidate.ExtractType != "regex_ordinal" {
			t.Fatalf("sid candidate %d = %#v, want regex_ordinal", index, candidate)
		}
		expression, ordinal, ok := decodeRegexOrdinalExpression(candidate.Expression)
		if !ok || expression != formFieldRegex("sid") || ordinal != index+1 {
			t.Fatalf("sid candidate %d ordinal config = %q/%d/%t", index, expression, ordinal, ok)
		}
	}
}

func TestSalesforceCandidatesSuppressDuplicateGlobalHeaderRegexes(t *testing.T) {
	firstSID := "00Dxx0000000001!AQ0AQHfirst123456"
	secondSID := "00Dxx0000000001!AQ0AQHsecond654321"
	candidates := salesforceCandidates("", map[string]string{
		"X-A": "https://acme.example/secur/frontdoor.jsp?sid=" + firstSID,
		"X-B": "https://acme.example/secur/frontdoor.jsp?sid=" + secondSID,
	})

	frontdoor := make([]Candidate, 0, 2)
	for _, candidate := range candidates {
		if candidate.RuleID == RuleSalesforceFrontdoorSID {
			frontdoor = append(frontdoor, candidate)
		}
	}
	if len(frontdoor) != 1 {
		t.Fatalf("frontdoor candidates = %#v, want one replayable global-header extractor", frontdoor)
	}
	if frontdoor[0].Value != firstSID {
		t.Fatalf("frontdoor candidate value = %q, want first sorted header value %q", frontdoor[0].Value, firstSID)
	}
}

func TestFormCandidatesSkipEncodedWireKeys(t *testing.T) {
	candidates := formCandidates("aura%2Etoken=token-value-123456")
	if len(candidates) != 0 {
		t.Fatalf("encoded form-key candidates = %#v, want none because the generated regex cannot match the wire key", candidates)
	}
}

func TestAuraContextCandidatesTrackActualRegexOccurrences(t *testing.T) {
	appOnly := url.QueryEscape(`{"app":"markup://one:one"}`)
	fwuid := url.QueryEscape(`{"fwuid":"fwuid-value-123456"}`)
	candidates := formCandidates("aura.context=" + appOnly + "&aura.context=" + fwuid)
	var found *Candidate
	for index := range candidates {
		if candidates[index].RuleID == RuleSalesforceAuraContextFWUID {
			found = &candidates[index]
			break
		}
	}
	if found == nil {
		t.Fatalf("Aura context candidates = %#v, want fwuid from second outer field", candidates)
	}
	if found.ExtractType != "regex" {
		t.Fatalf("fwuid candidate = %#v, want first actual regex match rather than outer-field ordinal 2", *found)
	}

	secondFWUID := url.QueryEscape(`{"fwuid":"fwuid-second-654321"}`)
	candidates = formCandidates("aura.context=" + fwuid + "&aura.context=" + secondFWUID)
	var fwuidCandidates []Candidate
	for _, candidate := range candidates {
		if candidate.RuleID == RuleSalesforceAuraContextFWUID {
			fwuidCandidates = append(fwuidCandidates, candidate)
		}
	}
	if len(fwuidCandidates) != 2 || fwuidCandidates[1].ExtractType != "regex_ordinal" {
		t.Fatalf("repeated fwuid candidates = %#v, want second actual match ordinal", fwuidCandidates)
	}
	_, ordinal, ok := decodeRegexOrdinalExpression(fwuidCandidates[1].Expression)
	if !ok || ordinal != 2 {
		t.Fatalf("second fwuid ordinal = %d/%t", ordinal, ok)
	}
}

func TestAuraContextCandidatesCountSkippedRuntimeMatches(t *testing.T) {
	goodValue := "fwuid-good-123456"
	goodContext := url.QueryEscape(`{"fwuid":"` + goodValue + `"}`)
	tests := []struct {
		name         string
		firstContext string
	}{
		{
			name:         "short discovery value still matches wire regex",
			firstContext: url.QueryEscape(`{"fwuid":"x"}`),
		},
		{
			name:         "duplicate discovery value still matches wire regex",
			firstContext: url.QueryEscape(`{"fwuid":"first-fwuid-123456","fwuid":"second-fwuid-654321"}`),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := "aura.context=" + test.firstContext + "&aura.context=" + goodContext
			var found *Candidate
			for _, candidate := range formCandidates(body) {
				if candidate.RuleID == RuleSalesforceAuraContextFWUID && candidate.Value == goodValue {
					copy := candidate
					found = &copy
					break
				}
			}
			if found == nil || found.ExtractType != "regex_ordinal" {
				t.Fatalf("Aura context candidates = %#v, want good fwuid at its runtime ordinal", formCandidates(body))
			}
			_, ordinal, ok := decodeRegexOrdinalExpression(found.Expression)
			if !ok || ordinal != 2 {
				t.Fatalf("good fwuid ordinal = %d/%t, want 2", ordinal, ok)
			}
			wireValue := candidateRegexCapture(t, *found, body)
			decoded, err := url.QueryUnescape(wireValue)
			if err != nil || decoded != goodValue {
				t.Fatalf("runtime capture = %q (%v), want %q", decoded, err, goodValue)
			}
		})
	}
}

func TestAuraContextCandidatesSkipDuplicateInnerJSONFields(t *testing.T) {
	context := url.QueryEscape(`{"fwuid":"first-fwuid-123456","fwuid":"second-fwuid-654321"}`)
	for _, candidate := range formCandidates("aura.context=" + context) {
		if candidate.RuleID == RuleSalesforceAuraContextFWUID {
			t.Fatalf("ambiguous duplicate inner fwuid emitted as %#v", candidate)
		}
	}
}

func TestFormCandidatesDoNotCountKeyOnlyFieldsAsRegexMatches(t *testing.T) {
	body := "aura.token&aura.token=token-value-123456"
	var found *Candidate
	for _, candidate := range formCandidates(body) {
		if candidate.RuleID == RuleSalesforceAuraToken {
			copy := candidate
			found = &copy
			break
		}
	}
	if found == nil || found.ExtractType != "regex" {
		t.Fatalf("form candidates = %#v, want the sole key=value occurrence at ordinal 1", formCandidates(body))
	}
	if got := candidateRegexCapture(t, *found, body); got != "token-value-123456" {
		t.Fatalf("runtime capture = %q, want token-value-123456", got)
	}
}

func TestLocationCandidatesDoNotCountUnmatchableQueryFields(t *testing.T) {
	for _, rawQuery := range []string{
		"state&state=token-value-123456",
		"state=&state=token-value-123456",
	} {
		t.Run(rawQuery, func(t *testing.T) {
			location := "https://example.test/callback?" + rawQuery
			var found *Candidate
			for _, candidate := range headerCandidates(map[string]string{"Location": location}) {
				if candidate.RuleID == RuleLocationParam && candidate.Value == "token-value-123456" {
					copy := candidate
					found = &copy
					break
				}
			}
			if found == nil {
				t.Fatalf("Location candidates = %#v, want token candidate", headerCandidates(map[string]string{"Location": location}))
			}
			flattened := "Location: " + location + "\n"
			matches := regexp.MustCompile(found.Expression).FindStringSubmatch(flattened)
			if len(matches) < 2 || matches[1] != "token-value-123456" {
				t.Fatalf("Location regex %q captured %#v, want token-value-123456", found.Expression, matches)
			}
		})
	}
}

func TestLocationCandidatesSkipEncodedWireKeys(t *testing.T) {
	for _, candidate := range headerCandidates(map[string]string{
		"Location": "https://example.test/callback?st%61te=token-value-123456",
	}) {
		if candidate.RuleID == RuleLocationParam {
			t.Fatalf("encoded Location query key emitted as %#v", candidate)
		}
	}
}

func TestFormCandidatesIndexWideWireBodyWithoutPerKeyRescans(t *testing.T) {
	var body strings.Builder
	for index := 0; index < maxCorrelationStructuredFields-2; index++ {
		fmt.Fprintf(&body, "field%d=x&", index)
	}
	body.WriteString("aura.token=token-value-123456")

	candidates, err := CandidatesChecked(body.String(), nil)
	if err != nil {
		t.Fatalf("CandidatesChecked wide form: %v", err)
	}
	var found *Candidate
	for index := range candidates {
		if candidates[index].RuleID == RuleSalesforceAuraToken && candidates[index].Value == "token-value-123456" {
			found = &candidates[index]
			break
		}
	}
	if found == nil || found.ExtractType != "regex" {
		t.Fatalf("wide form candidates = %#v, want exact aura.token", candidates)
	}
}

func TestLocationCandidatesIndexWideQueryWithoutPerKeyRescans(t *testing.T) {
	var location strings.Builder
	location.WriteString("https://example.test/callback?")
	for index := 0; index < maxCorrelationStructuredFields-2; index++ {
		fmt.Fprintf(&location, "field%d=x&", index)
	}
	location.WriteString("state=token-value-123456")

	candidates, err := CandidatesChecked("", map[string]string{"Location": location.String()})
	if err != nil {
		t.Fatalf("CandidatesChecked wide Location: %v", err)
	}
	var found *Candidate
	for index := range candidates {
		if candidates[index].RuleID == RuleLocationParam && candidates[index].Value == "token-value-123456" {
			found = &candidates[index]
			break
		}
	}
	if found == nil {
		t.Fatalf("wide Location candidates = %#v, want state token", candidates)
	}
	flattened := "Location: " + location.String() + "\n"
	match := regexp.MustCompile(found.Expression).FindStringSubmatch(flattened)
	if len(match) < 2 || match[1] != "token-value-123456" {
		t.Fatalf("wide Location extractor captured %#v", match)
	}
}

func TestCandidatesCheckedSkipsJSONWalkForOversizedStructures(t *testing.T) {
	var body strings.Builder
	body.WriteByte('[')
	for index := 0; index < maxCorrelationJSONTokens/2+1; index++ {
		if index > 0 {
			body.WriteByte(',')
		}
		body.WriteString("{}")
	}
	body.WriteByte(']')

	// Beyond the streaming token cap the parsed walk is skipped (fallback regex
	// scanning still runs); ordinary wide documents must not fail the analysis.
	candidates, err := CandidatesChecked(body.String(), nil)
	if err != nil {
		t.Fatalf("CandidatesChecked wide JSON must degrade, got error %v", err)
	}
	if len(candidates) != 0 {
		t.Fatalf("wide empty-object JSON candidates = %#v, want none", candidates)
	}
}

// Regression for the review finding that one large SOQL-style page (each
// record carrying an Id field) aborted the whole analysis: past the
// per-response cap the walk must return the bounded candidate prefix instead
// of an error.
func TestCandidatesCheckedTruncatesLargeRecordPageInsteadOfFailing(t *testing.T) {
	records := maxCorrelationCandidatesPerObservation + 500
	var body strings.Builder
	fmt.Fprintf(&body, `{"totalSize":%d,"done":true,"records":[`, records)
	for index := 0; index < records; index++ {
		if index > 0 {
			body.WriteByte(',')
		}
		fmt.Fprintf(&body, `{"Id":"001xx%011d"}`, index)
	}
	body.WriteString(`]}`)

	candidates, err := CandidatesChecked(body.String(), nil)
	if err != nil {
		t.Fatalf("%d-record page must degrade, got error %v", records, err)
	}
	if len(candidates) != maxCorrelationCandidatesPerObservation {
		t.Fatalf("%d-record page candidates = %d, want truncation at %d", records, len(candidates), maxCorrelationCandidatesPerObservation)
	}
}

func TestJSONCandidateWalkTruncatesWhenConstructionBudgetExhausted(t *testing.T) {
	budget := newCorrelationJSONWalkBudget()
	budget.maxConstructionBytes = 24
	candidates, valid, err := jsonCandidatesFromParsedBodyChecked(
		`{"outer":{"csrf_token":"token-value-123456"}}`,
		budget,
	)
	if !valid || err != nil || len(candidates) != 0 {
		t.Fatalf("bounded JSON walk = candidates %#v, valid %t, error %v; want truncated empty success", candidates, valid, err)
	}
}

func TestDetectCheckedRejectsASPNetRawMatchTruncation(t *testing.T) {
	var body strings.Builder
	for index := 0; index <= maxCorrelationCandidatesPerObservation; index++ {
		body.WriteString(`<input name="__EVENTVALIDATION" value="">`)
	}
	body.WriteString(`<input name="__EVENTVALIDATION" value="target-token-123456">`)

	candidates, truncated, err := candidatesCheckedWithTruncation(body.String(), nil)
	if err != nil {
		t.Fatalf("ASP.NET raw-match truncation: %v", err)
	}
	if !truncated {
		t.Fatalf("ASP.NET raw-match prefix was presented as complete: %#v", candidates)
	}
	if correlations, err := DetectChecked([]Observation{{ID: 1, ResponseBody: body.String()}}); !errors.Is(err, ErrCorrelationWorkLimit) || correlations != nil {
		t.Fatalf("DetectChecked ASP.NET truncation = %#v, error %v; want fail-closed work-limit error", correlations, err)
	}
}

func TestDetectCheckedRejectsXMLRawMatchTruncation(t *testing.T) {
	var body strings.Builder
	for index := 0; index <= maxCorrelationCandidatesPerObservation; index++ {
		body.WriteString(`<token>static-value</wrong>`)
	}
	body.WriteString(`<token>target-token-123456</token>`)

	candidates, truncated, err := candidatesCheckedWithTruncation(body.String(), nil)
	if err != nil {
		t.Fatalf("XML raw-match truncation: %v", err)
	}
	if len(candidates) != 0 || !truncated {
		t.Fatalf("XML raw-match candidates = %#v, truncated=%t; want incomplete empty prefix", candidates, truncated)
	}
	if correlations, err := DetectChecked([]Observation{{ID: 1, ResponseBody: body.String()}}); !errors.Is(err, ErrCorrelationWorkLimit) || correlations != nil {
		t.Fatalf("DetectChecked XML truncation = %#v, error %v; want fail-closed work-limit error", correlations, err)
	}
}

func TestDetectCheckedRejectsJSONConstructionTruncationBelowCandidateCap(t *testing.T) {
	const fieldCount = maxCorrelationStructuredFields
	longKeyPrefix := strings.Repeat("ordinary_field_x", 23)
	var body strings.Builder
	body.Grow(fieldCount * (len(longKeyPrefix) + 20))
	body.WriteByte('{')
	for index := 0; index < fieldCount; index++ {
		if index > 0 {
			body.WriteByte(',')
		}
		fmt.Fprintf(&body, "%q:%q", fmt.Sprintf("%s_%04d", longKeyPrefix, index), "value")
	}
	body.WriteByte('}')

	candidates, truncated, err := candidatesCheckedWithTruncation(body.String(), nil)
	if err != nil {
		t.Fatalf("best-effort construction-limited candidates: %v", err)
	}
	if len(candidates) != 0 || !truncated {
		t.Fatalf("construction-limited candidates = %d, truncated=%t; want incomplete empty prefix", len(candidates), truncated)
	}
	if correlations, err := DetectChecked([]Observation{{ID: 1, ResponseBody: body.String()}}); !errors.Is(err, ErrCorrelationWorkLimit) || correlations != nil {
		t.Fatalf("DetectChecked construction truncation = %#v, error %v; want fail-closed work-limit error", correlations, err)
	}
}

func TestCandidatesCheckedSkipsDeepGenericJSONWalk(t *testing.T) {
	body := strings.Repeat(`{"x":`, maxCorrelationJSONDepth+1) + `"leaf"` + strings.Repeat(`}`, maxCorrelationJSONDepth+1)
	if _, err := CandidatesChecked(body, nil); err != nil {
		t.Fatalf("CandidatesChecked deep JSON must degrade to the regex fallback, got error %v", err)
	}
}

func TestCheckedAnalysisRejectsDuplicateObservationIDs(t *testing.T) {
	observations := []Observation{{ID: 7}, {ID: 7}}
	if _, err := DetectChecked(observations); !errors.Is(err, ErrCorrelationWorkLimit) {
		t.Fatalf("DetectChecked duplicate ID error = %v, want ErrCorrelationWorkLimit", err)
	}
}

func TestCandidatesCheckedSkipsStructuredHeaderOccurrenceOverflow(t *testing.T) {
	headers := map[string]string{
		"Set-Cookie": strings.Repeat("sid=value1234\n", maxCorrelationStructuredFields),
	}
	candidates, err := CandidatesChecked("", headers)
	if err != nil {
		t.Fatalf("CandidatesChecked header occurrence overflow must degrade, got error %v", err)
	}
	// The pathologically wide header's occurrence split is skipped (linear
	// whole-surface scans may still run), not the whole analysis.
	for _, candidate := range candidates {
		if candidate.RuleID == RuleSetCookieSession || candidate.RuleID == RuleSetCookieOther {
			t.Fatalf("wide Set-Cookie header must skip per-occurrence candidates, got %#v", candidate)
		}
	}
	if correlations, err := DetectChecked([]Observation{{ID: 1, ResponseHeaders: headers}}); !errors.Is(err, ErrCorrelationWorkLimit) || correlations != nil {
		t.Fatalf("DetectChecked structured-header overflow = %#v, error %v; want fail-closed work-limit error", correlations, err)
	}
}

func TestCandidatesCheckedBoundsWideLocationQuery(t *testing.T) {
	var location strings.Builder
	location.WriteString("https://example.test/callback?")
	for index := 0; index <= maxCorrelationStructuredFields; index++ {
		fmt.Fprintf(&location, "field%d=x&", index)
	}
	location.WriteString("state=target-token-123456")
	headers := map[string]string{"Location": location.String()}

	candidates, truncated, err := candidatesCheckedWithTruncation("", headers)
	if err != nil {
		t.Fatalf("wide Location candidates: %v", err)
	}
	for _, candidate := range candidates {
		if candidate.Value == "target-token-123456" {
			t.Fatalf("wide Location query escaped the structured bound: %#v", candidate)
		}
	}
	if !truncated {
		t.Fatal("wide Location query was presented as complete")
	}
	if correlations, err := DetectChecked([]Observation{{ID: 1, ResponseHeaders: headers}}); !errors.Is(err, ErrCorrelationWorkLimit) || correlations != nil {
		t.Fatalf("DetectChecked wide Location = %#v, error %v; want fail-closed work-limit error", correlations, err)
	}
}

func TestCandidatesCheckedOnlyInspectsLeadingSetCookiePair(t *testing.T) {
	var line strings.Builder
	for index := 0; index <= maxCorrelationCandidatesPerObservation; index++ {
		fmt.Fprintf(&line, "sid=value-%06d;", index)
	}
	line.WriteString("sid=final-target-value")
	headers := map[string]string{"Set-Cookie": line.String()}

	candidates, truncated, err := candidatesCheckedWithTruncation("", headers)
	if err != nil {
		t.Fatalf("wide Set-Cookie candidates: %v", err)
	}
	if truncated {
		t.Fatal("Set-Cookie extension attributes incorrectly consumed the candidate budget")
	}
	if len(candidates) != 1 || candidates[0].Value != "value-000000" {
		t.Fatalf("wide Set-Cookie candidates = %#v, want only the leading cookie-pair", candidates)
	}
	match := regexp.MustCompile(candidates[0].Expression).FindStringSubmatch("Set-Cookie: " + line.String() + "\n")
	if len(match) < 2 || match[1] != "value-000000" {
		t.Fatalf("leading cookie extractor %q captured %#v", candidates[0].Expression, match)
	}
}

func TestTruncateIsUTF8SafeAndHandlesNonPositiveLimits(t *testing.T) {
	got := Truncate("ab€cd", 4)
	if got != "ab..." || !utf8.ValidString(got) {
		t.Fatalf("Truncate split multibyte = %q, want valid UTF-8 %q", got, "ab...")
	}
	if got := Truncate("value", 0); got != "..." {
		t.Fatalf("Truncate zero limit = %q, want ellipsis", got)
	}
	if got := Truncate("value", -1); got != "..." {
		t.Fatalf("Truncate negative limit = %q, want ellipsis", got)
	}
}

func TestHeaderOrdinalExtractorsStayWithinReplayRegexLimit(t *testing.T) {
	_, headerExpression, ok := headerExtractorForOccurrence("X-Session-Token", maxCorrelationRegexOrdinal, maxCorrelationRegexOrdinal+2)
	if !ok {
		t.Fatal("header extractor rejected the supported ordinal boundary")
	}
	if _, err := regexp.Compile(headerExpression); err != nil {
		t.Fatalf("compile boundary header extractor: %v", err)
	}
	if _, _, ok := headerExtractorForOccurrence("X-Session-Token", maxCorrelationRegexOrdinal+1, maxCorrelationRegexOrdinal+2); ok {
		t.Fatal("header extractor accepted an ordinal beyond the replay limit")
	}

	cookieExpression, ok := setCookieExtractorForOccurrence("Set-Cookie", "session", maxCorrelationRegexOrdinal)
	if !ok {
		t.Fatal("Set-Cookie extractor rejected the supported ordinal boundary")
	}
	if _, err := regexp.Compile(cookieExpression); err != nil {
		t.Fatalf("compile boundary Set-Cookie extractor: %v", err)
	}
	if _, ok := setCookieExtractorForOccurrence("Set-Cookie", "session", maxCorrelationRegexOrdinal+1); ok {
		t.Fatal("Set-Cookie extractor accepted an ordinal beyond the replay limit")
	}

	locationExpression, ok := locationHeaderQueryRegex("Location", "state", maxCorrelationRegexOrdinal, maxCorrelationRegexOrdinal+2, maxCorrelationRegexOrdinal)
	if !ok {
		t.Fatal("Location extractor rejected the supported ordinal boundary")
	}
	if _, err := regexp.Compile(locationExpression); err != nil {
		t.Fatalf("compile boundary Location extractor: %v", err)
	}
	if _, ok := locationHeaderQueryRegex("Location", "state", maxCorrelationRegexOrdinal+1, maxCorrelationRegexOrdinal+2, 0); ok {
		t.Fatal("Location extractor accepted an oversized header ordinal")
	}
	if _, ok := locationHeaderQueryRegex("Location", "state", 0, 1, maxCorrelationRegexOrdinal+1); ok {
		t.Fatal("Location extractor accepted an oversized query-value ordinal")
	}
}

func TestCandidatesCheckedSkipsHeaderRegexOrdinalOverflow(t *testing.T) {
	lineValues := func(format string) string {
		values := make([]string, maxCorrelationRegexOrdinal+2)
		for index := range values {
			values[index] = fmt.Sprintf(format, index)
		}
		return strings.Join(values, "\n")
	}
	var repeatedQuery strings.Builder
	repeatedQuery.WriteString("https://example.test/callback?")
	for index := 0; index < maxCorrelationRegexOrdinal+2; index++ {
		if index > 0 {
			repeatedQuery.WriteByte('&')
		}
		fmt.Fprintf(&repeatedQuery, "state=state-%06d-abcdef", index)
	}

	tests := []struct {
		name    string
		headers map[string]string
	}{
		{name: "dynamic header", headers: map[string]string{"X-Session-Token": lineValues("token-%06d-abcdef")}},
		{name: "Set-Cookie", headers: map[string]string{"Set-Cookie": lineValues("session=sid-%06d-abcdef; Path=/")}},
		{name: "Location header occurrence", headers: map[string]string{"Location": lineValues("https://example.test/callback?state=state-%06d-abcdef")}},
		{name: "Location query occurrence", headers: map[string]string{"Location": repeatedQuery.String()}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidates, err := CandidatesChecked("", test.headers)
			if err != nil {
				t.Fatalf("CandidatesChecked ordinal overflow must degrade, got error %v", err)
			}
			// Ordinals 0..maxCorrelationRegexOrdinal remain replayable; only the
			// occurrence beyond the runtime limit is omitted.
			if len(candidates) != maxCorrelationRegexOrdinal+1 {
				t.Fatalf("candidates = %d, want %d replayable occurrences", len(candidates), maxCorrelationRegexOrdinal+1)
			}
		})
	}
}

func TestLocationQueryExtractorIgnoresNestedAndPathEmbeddedKeys(t *testing.T) {
	tests := []struct {
		name     string
		location string
		want     string
	}{
		{
			name:     "unencoded nested redirect URL",
			location: "https://example.test/auth?next=/cb?code=SESSIONAAAA&code=SESSIONBBBB",
			want:     "SESSIONBBBB",
		},
		{
			name:     "ampersand and key inside the path",
			location: "/search/a&code=1111/results?code=real2222",
			want:     "real2222",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var found *Candidate
			for _, candidate := range headerCandidates(map[string]string{"Location": test.location}) {
				if candidate.RuleID == RuleLocationParam && candidate.Name == "corr_location_code" {
					copy := candidate
					found = &copy
					break
				}
			}
			if found == nil || found.Value != test.want {
				t.Fatalf("Location code candidate = %#v, want top-level value %q", found, test.want)
			}
			flattened := "Location: " + test.location + "\n"
			match := regexp.MustCompile(found.Expression).FindStringSubmatch(flattened)
			if len(match) < 2 || match[1] != test.want {
				t.Fatalf("Location regex %q captured %#v, want %q", found.Expression, match, test.want)
			}
		})
	}
}

func TestLocationHeaderOrdinalSurvivesEmptyHeaderValues(t *testing.T) {
	headers := map[string]string{"Location": "\n/a?code=xxxx1111\n/b?code=yyyy2222"}
	byValue := map[string]Candidate{}
	for _, candidate := range headerCandidates(headers) {
		if candidate.RuleID == RuleLocationParam {
			byValue[candidate.Value] = candidate
		}
	}
	flattened := "Location: \nLocation: /a?code=xxxx1111\nLocation: /b?code=yyyy2222\n"
	for _, want := range []string{"xxxx1111", "yyyy2222"} {
		candidate, ok := byValue[want]
		if !ok {
			t.Fatalf("missing Location candidate %q in %#v", want, byValue)
		}
		match := regexp.MustCompile(candidate.Expression).FindStringSubmatch(flattened)
		if len(match) < 2 || match[1] != want {
			t.Fatalf("Location ordinal regex %q captured %#v, want %q (empty first header must not shift the occurrence)", candidate.Expression, match, want)
		}
	}
}

func TestLocationQueryValuesWithLiteralWhitespaceAreNotReplayable(t *testing.T) {
	headers := map[string]string{"Location": "/cb?code=abc def123&state=xyz9"}
	var state *Candidate
	for _, candidate := range headerCandidates(headers) {
		if candidate.RuleID != RuleLocationParam {
			continue
		}
		if candidate.Name == "corr_location_code" {
			t.Fatalf("whitespace-containing query value must not be indexed as replayable: %#v", candidate)
		}
		if candidate.Name == "corr_location_state" {
			copy := candidate
			state = &copy
		}
	}
	if state == nil {
		t.Fatal("whitespace in a sibling value must not suppress the clean state candidate")
	}
	flattened := "Location: /cb?code=abc def123&state=xyz9\n"
	match := regexp.MustCompile(state.Expression).FindStringSubmatch(flattened)
	if len(match) < 2 || match[1] != "xyz9" {
		t.Fatalf("state regex %q captured %#v, want xyz9", state.Expression, match)
	}
}

func TestSetCookieGrammarTreatsOnlyLeadingPairAsCookie(t *testing.T) {
	line := "sid=aaaa1111; other=bbbb2222; sid=cccc3333; Path=/"
	occurrences := map[string]int{}
	candidates := setCookieCandidates("Set-Cookie", line, occurrences)
	byName := map[string]Candidate{}
	for _, candidate := range candidates {
		byName[candidate.Name] = candidate
	}
	sid, ok := byName["corr_cookie_sid"]
	if !ok || sid.Value != "aaaa1111" {
		t.Fatalf("sid candidate = %#v, want the RFC leading cookie-pair value aaaa1111", byName)
	}
	if occurrences["sid"] != 1 {
		t.Fatalf("sid line-occurrence count = %d, want 1", occurrences["sid"])
	}
	if _, exists := byName["corr_cookie_other"]; exists {
		t.Fatalf("unknown Set-Cookie extension attribute was treated as another cookie: %#v", byName)
	}
	flattened := "Set-Cookie: " + line + "\n"
	match := regexp.MustCompile(sid.Expression).FindStringSubmatch(flattened)
	if len(match) < 2 || match[1] != "aaaa1111" {
		t.Fatalf("sid regex %q captured %#v, want aaaa1111", sid.Expression, match)
	}
}

func TestSetCookieCandidatesPreserveQuotedValueAndCaseSensitiveNames(t *testing.T) {
	headers := map[string]string{
		"Set-Cookie": "$SID=\"quoted-token-123456\"; Path=/\n$Sid=second-token-654321; Path=/",
	}
	candidates := headerCandidates(headers)
	if len(candidates) != 2 {
		t.Fatalf("Set-Cookie candidates = %#v, want two case-distinct cookie names", candidates)
	}
	flattened := "Set-Cookie: $SID=\"quoted-token-123456\"; Path=/\nSet-Cookie: $Sid=second-token-654321; Path=/\n"
	for index, want := range []string{"quoted-token-123456", "second-token-654321"} {
		if candidates[index].Value != want {
			t.Fatalf("candidate %d value = %q, want %q", index, candidates[index].Value, want)
		}
		match := regexp.MustCompile(candidates[index].Expression).FindStringSubmatch(flattened)
		if len(match) < 2 || match[1] != want {
			t.Fatalf("candidate %d regex %q captured %#v, want %q", index, candidates[index].Expression, match, want)
		}
	}
}

func TestSetCookieCandidatesRejectMalformedCookiePairs(t *testing.T) {
	for _, line := range []string{
		`sid="unterminated-token-123456; Path=/`,
		`sid=comma,value123456; Path=/`,
		`sid=space value123456; Path=/`,
		`bad name=value123456; Path=/`,
	} {
		if candidates := setCookieCandidates("Set-Cookie", line, map[string]int{}); len(candidates) != 0 {
			t.Fatalf("malformed Set-Cookie %q produced candidates %#v", line, candidates)
		}
	}
}

func TestSetCookieCandidatesOmitPairsTheReplayRegexCannotAddress(t *testing.T) {
	tests := []struct {
		name       string
		line       string
		wantValues map[string]string
	}{
		{
			name: "comma-folded second cookie",
			line: "sid=aaaa1111; Path=/, other=bbbb2222; Path=/",
			wantValues: map[string]string{
				"corr_cookie_sid": "aaaa1111",
			},
		},
		{
			name: "name value nested inside attribute",
			line: "sid=aaaa1111; Comment=redirect=https://example.test/?token=bbbb2222; Path=/",
			wantValues: map[string]string{
				"corr_cookie_sid": "aaaa1111",
			},
		},
		{
			name: "expires comma does not hide first cookie",
			line: "sid=aaaa1111; Expires=Wed, 21 Oct 2030 07:28:00 GMT; Path=/",
			wantValues: map[string]string{
				"corr_cookie_sid": "aaaa1111",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidates := setCookieCandidates("Set-Cookie", test.line, map[string]int{})
			got := make(map[string]string, len(candidates))
			for _, candidate := range candidates {
				got[candidate.Name] = candidate.Value
				match := regexp.MustCompile(candidate.Expression).FindStringSubmatch("Set-Cookie: " + test.line + "\n")
				if len(match) < 2 || match[1] != candidate.Value {
					t.Fatalf("candidate %#v is not replayable against its source line; match = %#v", candidate, match)
				}
			}
			if !reflect.DeepEqual(got, test.wantValues) {
				t.Fatalf("cookie candidates = %#v, want %#v", got, test.wantValues)
			}
		})
	}
}

func TestSetCookieOrdinalCountsLinesAcrossRepeatedHeaders(t *testing.T) {
	headers := map[string]string{"Set-Cookie": "sid=aaaa1111; Path=/\nsid=bbbb2222; Path=/"}
	var sids []Candidate
	for _, candidate := range headerCandidates(headers) {
		if candidate.Name == "corr_cookie_sid" || candidate.Name == "corr_cookie_sid_2" {
			sids = append(sids, candidate)
		}
	}
	if len(sids) != 2 {
		t.Fatalf("sid candidates = %#v, want one per header line", sids)
	}
	flattened := "Set-Cookie: sid=aaaa1111; Path=/\nSet-Cookie: sid=bbbb2222; Path=/\n"
	for index, want := range []string{"aaaa1111", "bbbb2222"} {
		match := regexp.MustCompile(sids[index].Expression).FindStringSubmatch(flattened)
		if len(match) < 2 || match[1] != want {
			t.Fatalf("sid ordinal %d regex %q captured %#v, want %q", index, sids[index].Expression, match, want)
		}
	}
}

func TestSetCookieOrdinalCountsSkippedShortLeadingValue(t *testing.T) {
	headers := map[string]string{"Set-Cookie": "sid=abc; Path=/\nsid=long-session-token-123456; Path=/"}
	var sid Candidate
	for _, candidate := range headerCandidates(headers) {
		if candidate.Name == "corr_cookie_sid" {
			sid = candidate
			break
		}
	}
	if sid.Value != "long-session-token-123456" {
		t.Fatalf("sid candidate = %#v, want only the useful second value", sid)
	}
	flattened := "Set-Cookie: sid=abc; Path=/\nSet-Cookie: sid=long-session-token-123456; Path=/\n"
	match := regexp.MustCompile(sid.Expression).FindStringSubmatch(flattened)
	if len(match) < 2 || match[1] != sid.Value {
		t.Fatalf("sid regex %q captured %#v, want %q after the skipped short occurrence", sid.Expression, match, sid.Value)
	}
}

func TestSetCookieOrdinalMatchesReplayGrammarAcrossOmittedFields(t *testing.T) {
	tests := []struct {
		name   string
		values string
		want   string
	}{
		{
			name:   "empty field does not consume but short field does",
			values: "sid=; Path=/\nsid=abc; Path=/\nsid=long-session-token-123456; Path=/",
			want:   "long-session-token-123456",
		},
		{
			name:   "malformed matchable quote still consumes an ordinal",
			values: "sid=\"unterminated-token-123456; Path=/\nsid=valid-session-token-654321; Path=/",
			want:   "valid-session-token-654321",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			headers := map[string]string{"Set-Cookie": test.values}
			var candidate Candidate
			for _, current := range headerCandidates(headers) {
				if current.Value == test.want {
					candidate = current
					break
				}
			}
			if candidate.Value != test.want {
				t.Fatalf("candidates = %#v, want %q", headerCandidates(headers), test.want)
			}
			var flattened strings.Builder
			for _, value := range strings.Split(test.values, "\n") {
				flattened.WriteString("Set-Cookie: ")
				flattened.WriteString(value)
				flattened.WriteByte('\n')
			}
			match := regexp.MustCompile(candidate.Expression).FindStringSubmatch(flattened.String())
			if len(match) < 2 || match[1] != test.want {
				t.Fatalf("cookie regex %q captured %#v, want %q", candidate.Expression, match, test.want)
			}
		})
	}
}

func TestHeaderOrdinalsSpanCaseVariantMapKeys(t *testing.T) {
	headers := map[string]string{
		"Location":   "https://example.test/callback?token=first-location-token-123456",
		"location":   "https://example.test/callback?token=second-location-token-654321",
		"Set-Cookie": "sid=abc; Path=/",
		"set-cookie": "sid=case-variant-cookie-token-123456; Path=/",
	}
	// flattenHeaders sorts exact map keys, then emits their physical values.
	flattened := "Location: " + headers["Location"] + "\n" +
		"Set-Cookie: " + headers["Set-Cookie"] + "\n" +
		"location: " + headers["location"] + "\n" +
		"set-cookie: " + headers["set-cookie"] + "\n"

	want := map[string]bool{
		"second-location-token-654321":     false,
		"case-variant-cookie-token-123456": false,
	}
	for _, candidate := range headerCandidates(headers) {
		if _, relevant := want[candidate.Value]; !relevant {
			continue
		}
		match := regexp.MustCompile(candidate.Expression).FindStringSubmatch(flattened)
		if len(match) < 2 || match[1] != candidate.Value {
			t.Fatalf("case-variant candidate %#v captured %#v", candidate, match)
		}
		want[candidate.Value] = true
	}
	for value, found := range want {
		if !found {
			t.Fatalf("case-variant header candidate %q was not replayable: %#v", value, headerCandidates(headers))
		}
	}
}

func TestDetectCheckedToleratesWideRequestQueryStructures(t *testing.T) {
	requestURL := "https://example.test/search?" + strings.Repeat("x=1&", maxCorrelationStructuredFields)
	if _, err := DetectChecked([]Observation{{ID: 1, RequestURL: requestURL}}); err != nil {
		t.Fatalf("DetectChecked wide request query must succeed, got error %v", err)
	}
}

func TestCandidateDiscoveryOmitsValuesAndExpressionsTheRuntimeCannotStore(t *testing.T) {
	normal := "normal-csrf-token-123456"
	body := `{"access_token":"` + strings.Repeat("x", protocol.MaxExtractionValueBytes+1) +
		`","csrf_token":"` + normal + `"}`
	candidates, err := CandidatesChecked(body, nil)
	if err != nil {
		t.Fatalf("CandidatesChecked oversized value: %v", err)
	}
	if len(candidates) != 1 || candidates[0].Value != normal {
		t.Fatalf("candidates = %#v, want only the runtime-storable csrf token", candidates)
	}

	hugeKey := strings.Repeat("x", maxCorrelationExtractorExpressionBytes) + "_token"
	malformed := `garbage{"` + hugeKey + `":"large-key-value-123456","csrf_token":"` + normal + `"}`
	candidates, err = CandidatesChecked(malformed, nil)
	if err != nil {
		t.Fatalf("CandidatesChecked oversized expression: %v", err)
	}
	if len(candidates) != 1 || candidates[0].Value != normal {
		t.Fatalf("fallback candidates = %#v, want only the runtime-compilable csrf token", candidates)
	}
}

func TestCorrelationExtractionValueFitsRuntimeValueBoundary(t *testing.T) {
	if !CorrelationExtractionValueFits("corr_token", strings.Repeat("x", protocol.MaxExtractionValueBytes)) {
		t.Fatal("value at the runtime per-extraction limit was rejected")
	}
	if CorrelationExtractionValueFits("corr_token", strings.Repeat("x", protocol.MaxExtractionValueBytes+1)) {
		t.Fatal("value beyond the runtime per-extraction limit was accepted")
	}
	if CorrelationExtractionValueFits(strings.Repeat("n", protocol.MaxExtractionKeyBytes), "value") {
		t.Fatal("name without room for a collision suffix was accepted")
	}
	if got, ok := CorrelationExtractionEntryBytes("corr_token", "value"); !ok || got != len("corr_tokenvalue") {
		t.Fatalf("CorrelationExtractionEntryBytes() = %d, %t", got, ok)
	}
}

// A percent-encoded frontdoor sid must be captured whole, not truncated to the
// org prefix that also prefixes every later session value.
func TestFrontdoorSIDCapturesPercentEncodedSession(t *testing.T) {
	const sid = "00Dxx0000000001%21AQ0AQHabcdefSyntheticSession"
	location := "https://acme.my.salesforce.com/secur/frontdoor.jsp?sid=" + sid + "&retURL=%2Fhome"
	correlations := Detect([]Observation{
		{ID: 1, ResponseHeaders: map[string]string{"Location": location}},
		{ID: 2, RequestURL: location},
	})
	for _, correlation := range correlations {
		if correlation.RuleID == RuleSalesforceFrontdoorSID {
			if correlation.Value != sid {
				t.Fatalf("frontdoor sid value = %q, want %q", correlation.Value, sid)
			}
			return
		}
	}
	t.Fatalf("frontdoor SID correlation not found: %#v", correlations)
}

// Lightning returns aura.token as the __Host-ERIC_* cookie; later /aura bodies
// echo it percent-encoded. It must be a high-confidence Salesforce producer.
func TestSalesforceAuraTokenCookieIsHighConfidenceProducer(t *testing.T) {
	const token = "eyJub25jZSI6InN5bnRoZXRpYyJ9.c3ludGhldGljLXNpZ25hdHVyZQ=="
	correlations := Detect([]Observation{
		{ID: 1, ResponseHeaders: map[string]string{"Set-Cookie": "__Host-ERIC_PROD-3360561830751937475=" + token + "; Path=/; Max-Age=86400; Secure; SameSite=Lax"}},
		{ID: 2, RequestBody: "message=%7B%7D&aura.token=" + url.QueryEscape(token)},
	})
	for _, correlation := range correlations {
		if correlation.Value == token {
			if correlation.RuleID != RuleSalesforceAuraTokenCookie || !HighConfidence(correlation) || correlation.Source != "header" {
				t.Fatalf("ERIC cookie correlation = %#v, want high-confidence Salesforce aura.token cookie from the header", correlation)
			}
			return
		}
	}
	t.Fatalf("ERIC cookie aura.token producer not detected: %#v", correlations)
}
