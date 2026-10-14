package correlate

import (
	"fmt"
	"strings"
	"testing"
)

func TestIdentifierNameSeparatesNamesFromGeneratedValues(t *testing.T) {
	names := []string{"navigation", "actionBar", "SAMLPartner", "Retail_Home_Page", "SUCCESS", "omiOSVersion", "nativeApiUDIDEncryptionEnabled", "markup:force"}
	for _, value := range names {
		if !identifierName(value) {
			t.Errorf("identifierName(%q) = false, want name", value)
		}
	}
	generated := []string{
		"06m6g0000007jIaAAI", // Salesforce ID
		"a1k4M000004XZFrQAO",
		"00008030-001C44311EF8C02E",
		"YWJjZGVm",   // base64 letters only
		"xKqPzLmWbE", // random mixed case
		"retURL",     // no segment long enough to be a word
		"BCDFGHJK",   // no vowel
		"value-1",
		"",
		"this_identifier_name_is_far_too_long_for_a_name",
	}
	for _, value := range generated {
		if identifierName(value) {
			t.Errorf("identifierName(%q) = true, want generated", value)
		}
	}
}

func TestBareURLOrigin(t *testing.T) {
	for value, want := range map[string]bool{
		"https://attone.my.salesforce.com":         true,
		"https://attone.my.salesforce.com/":        true,
		"http://localhost:8080":                    true,
		"https://example.test/path":                false,
		"https://example.test/?q=1":                false,
		"https://example.test#frag":                false,
		"https://user@example.test":                false,
		"https://example.test?":                    false,
		"example.test":                             false,
		"ftp://example.test":                       false,
		"https://attone.my.salesforce.com/secur/x": false,
	} {
		if got := bareURLOrigin(value); got != want {
			t.Errorf("bareURLOrigin(%q) = %v, want %v", value, got, want)
		}
	}
}

func correlationValues(correlations []Correlation) map[string]string {
	values := make(map[string]string, len(correlations))
	for _, corr := range correlations {
		values[corr.Value] = corr.RuleID
	}
	return values
}

// Values that are not server-generated state must not become correlations:
// each one would add a fail-closed extractor and buy nothing at replay.
func TestDetectRejectsValuesThatAreNotServerState(t *testing.T) {
	const userAgent = "SalesforceMobileSDK/12.0.1 iPadOS/18.5 (iPad)"
	observations := []Observation{
		{
			ID:             1,
			RequestURL:     "https://login.example.test/start",
			RequestHeaders: map[string]string{"User-Agent": userAgent},
			ResponseHeaders: map[string]string{
				"Location": "https://login.example.test/next?PartnerId=https://app.example.test&code=code-abcdef123456",
			},
		},
		{
			ID:             2,
			RequestURL:     "https://app.example.test/bootstrap",
			RequestHeaders: map[string]string{"User-Agent": userAgent},
			ResponseHeaders: map[string]string{
				"Set-Cookie": "omiOSVersion=18.5.1; Path=/",
			},
			ResponseBody: `{"token_type":"Bearer","access_token":"00Dxx0000001gPL!AQ0AQBHIS08hfp220wZsey","localId":"navigation","recordId":"a1k4M000004XZFrQAO"}`,
		},
		{
			ID:         3,
			RequestURL: "https://app.example.test/use?code=code-abcdef123456&tab=navigation&record=a1k4M000004XZFrQAO",
			RequestHeaders: map[string]string{
				"User-Agent":    "SalesforceMobileSDK/12.0.1 iPadOS/18.5.1 (iPad)",
				"Authorization": "Bearer 00Dxx0000001gPL!AQ0AQBHIS08hfp220wZsey",
				"Referer":       "https://app.example.test",
			},
		},
	}
	// The client reported its OS version before the server echoed it back.
	observations[0].RequestHeaders["User-Agent"] = "SalesforceMobileSDK/12.0.1 iPadOS/18.5.1 (iPad)"
	values := correlationValues(Detect(observations))
	for _, rejected := range []string{"https://app.example.test", "18.5.1", "Bearer", "navigation"} {
		if rule, found := values[rejected]; found {
			t.Errorf("%q was correlated by %s; it is not server state", rejected, rule)
		}
	}
	for _, kept := range []string{"code-abcdef123456", "00Dxx0000001gPL!AQ0AQBHIS08hfp220wZsey", "a1k4M000004XZFrQAO"} {
		if _, found := values[kept]; !found {
			t.Errorf("%q was not correlated; got %v", kept, values)
		}
	}
}

// A generic rule's evidence is only a key, cookie or header name, so a word
// or a short setting is never the token that name promises.
func TestDetectRejectsWordsAndSettingsFromGenericRules(t *testing.T) {
	observations := []Observation{
		{
			ID:         1,
			RequestURL: "https://www.example.test/",
			ResponseHeaders: map[string]string{
				"Set-Cookie": "agent_cohort=qualified; Path=/\nsite_locale=en_US; Path=/\nvisitor=v7Kq2xLm9Pz4; Path=/",
			},
			ResponseBody: `{"StorageSessionDurationText":"Session","containerId":"0000000","pageId":"pg-4815162342"}`,
		},
		{
			ID:         2,
			RequestURL: "https://www.example.test/next?cohort=qualified&locale=en_US&duration=Session&container=0000000&page=pg-4815162342&v=v7Kq2xLm9Pz4",
		},
	}
	values := correlationValues(Detect(observations))
	for _, rejected := range []string{"qualified", "en_US", "Session", "0000000"} {
		if rule, found := values[rejected]; found {
			t.Errorf("%q was correlated by %s; it is not an opaque token", rejected, rule)
		}
	}
	for _, kept := range []string{"pg-4815162342", "v7Kq2xLm9Pz4"} {
		if _, found := values[kept]; !found {
			t.Errorf("%q was not correlated; got %v", kept, values)
		}
	}
}

// A run of random letters is one long segment, not a word: a credential rule
// keeps it.
func TestDetectKeepsLetterOnlyTokensFromCredentialRules(t *testing.T) {
	const csrf = "qwhfjdkslaieurtyzmxn"
	const session = "QWERTYUIOPASDFGHJK"
	observations := []Observation{
		{ID: 1, RequestURL: "https://app.example.test/login", ResponseHeaders: map[string]string{"Set-Cookie": "SESSIONID=" + session + "; Path=/"}, ResponseBody: `{"csrf_token":"` + csrf + `"}`},
		{ID: 2, RequestURL: "https://app.example.test/save?csrf=" + csrf + "&s=" + session},
	}
	values := correlationValues(Detect(observations))
	for _, kept := range []string{csrf, session} {
		if _, found := values[kept]; !found {
			t.Errorf("%q was not correlated; got %v", kept, values)
		}
	}
}

func TestTokenShaped(t *testing.T) {
	for value, want := range map[string]bool{
		"v7Kq2xLm9Pz4": true,
		"481516":       true,
		"en_US":        false,
		"0000000":      false,
		"ababab":       false,
		"abcd12":       true,
	} {
		if got := tokenShaped(value); got != want {
			t.Errorf("tokenShaped(%q) = %v, want %v", value, got, want)
		}
	}
}

func TestDetectClientOriginCheckScope(t *testing.T) {
	const cookieValue = "browser-abcdef123456"
	const token = "token-abcdef1234567890"
	observations := []Observation{
		// Earlier occurrences only in Cookie and Authorization: state the client
		// received from a response the HAR did not keep.
		{ID: 1, RequestURL: "https://app.example.test/a", RequestHeaders: map[string]string{"Cookie": "BrowserId=" + cookieValue, "Authorization": "Bearer " + token}},
		{ID: 2, RequestURL: "https://app.example.test/b", ResponseHeaders: map[string]string{"Set-Cookie": "BrowserId=" + cookieValue + "; Path=/"}, ResponseBody: `{"access_token":"` + token + `"}`},
		{ID: 3, RequestURL: "https://app.example.test/c", RequestHeaders: map[string]string{"Cookie": "BrowserId=" + cookieValue, "Authorization": "Bearer " + token}},
	}
	values := correlationValues(Detect(observations))
	if values[cookieValue] != RuleSetCookieOther {
		t.Errorf("cookie re-issued after a Cookie-only occurrence was dropped: %v", values)
	}
	if values[token] != RuleJSONAuthToken {
		t.Errorf("token re-issued after an Authorization-only occurrence was dropped: %v", values)
	}

	// A credential rule keeps its value even when the client already sent it
	// in the URL; the weak cookie rule does not.
	observations[0].RequestURL = "https://app.example.test/a?echo=" + token + "&b=" + cookieValue
	values = correlationValues(Detect(observations))
	if values[token] != RuleJSONAuthToken {
		t.Errorf("credential rule must be exempt from the client-origin check: %v", values)
	}
	if _, found := values[cookieValue]; found {
		t.Errorf("generic cookie already sent in a URL was correlated: %v", values)
	}
}

// Lightning responses repeat the same values thousands of times. Repeats cost
// no scanning and must not exhaust the analysis cap before later responses
// are examined: 5 x 3500 candidates here, but only 3500 distinct values.
func TestDetectCandidateCapCountsOnlyAnalyzedValues(t *testing.T) {
	const token = "late-token-abcdef123456"
	var body strings.Builder
	body.WriteString(`{"records":[`)
	for index := 0; index < 3500; index++ {
		if index > 0 {
			body.WriteString(",")
		}
		fmt.Fprintf(&body, `{"Id":"a1k4M%013d"}`, index)
	}
	body.WriteString("]}")
	var observations []Observation
	for index := 1; index <= 5; index++ {
		observations = append(observations, Observation{ID: index, RequestURL: "https://app.example.test/page", ResponseBody: body.String()})
	}
	if candidates, err := CandidatesChecked(body.String(), nil); err != nil || 5*len(candidates) <= maxCorrelationAnalyzedValuesPerAnalysis {
		t.Fatalf("fixture yields %d candidates per response (err %v); it must exceed the cap in total", len(candidates), err)
	}
	observations = append(observations,
		Observation{ID: 6, RequestURL: "https://app.example.test/login", ResponseBody: `{"access_token":"` + token + `"}`},
		Observation{ID: 7, RequestURL: "https://app.example.test/use", RequestHeaders: map[string]string{"Authorization": "Bearer " + token}},
	)
	correlations, err := DetectChecked(observations)
	if err != nil {
		t.Fatalf("DetectChecked: %v", err)
	}
	if values := correlationValues(correlations); values[token] != RuleJSONAuthToken {
		t.Fatalf("correlations = %v, want the late token", values)
	}
}

// One response with more distinct values than the per-response cap must not
// use up the analysis-wide budget: a token in a later response is still found.
func TestOversizedResponseLeavesBudgetForLaterTokens(t *testing.T) {
	const token = "late-token-abcdef123456"
	var body strings.Builder
	body.WriteString(`{"records":[`)
	for index := 0; index < maxCorrelationCandidatesPerObservation+200; index++ {
		if index > 0 {
			body.WriteString(",")
		}
		fmt.Fprintf(&body, `{"Id":"a1k4M%013d"}`, index)
	}
	body.WriteString("]}")
	observations := []Observation{
		{ID: 1, RequestURL: "https://app.example.test/preload", ResponseBody: body.String()},
		{ID: 2, RequestURL: "https://app.example.test/login", ResponseBody: `{"access_token":"` + token + `"}`},
		{ID: 3, RequestURL: "https://app.example.test/use", RequestHeaders: map[string]string{"Authorization": "Bearer " + token}},
	}
	correlations, warnings, err := DetectCheckedWithWarnings(observations)
	if err != nil {
		t.Fatalf("DetectCheckedWithWarnings: %v", err)
	}
	if values := correlationValues(correlations); values[token] != RuleJSONAuthToken {
		t.Fatalf("correlations = %v (warnings %v), want the late token", values, warnings)
	}
}

// Lightning Aura responses reach ~56,000 JSON tokens. They must be walked as
// JSON (precise JSONPath extractors), not degraded to the capped regex scan.
func TestLargeAuraSizedResponseUsesTheStructuredWalk(t *testing.T) {
	var body strings.Builder
	body.WriteString(`{"actions":[{"returnValue":{"fields":[`)
	for index := 0; index < 6000; index++ {
		if index > 0 {
			body.WriteString(",")
		}
		fmt.Fprintf(&body, `{"label":"Field %d","value":%d,"meta":{"a":1,"b":2}}`, index, index)
	}
	body.WriteString(`],"sessionToken":"sess-abcdef1234567890"}}]}`)
	if extractType := candidateExtractType(t, body.String(), "sess-abcdef1234567890"); extractType != "jsonpath" {
		t.Fatalf("large response degraded to %s extraction, want jsonpath", extractType)
	}
}

// A container wider than the walk accepts would abort it with an empty
// prefix; such a document must keep the regex scan and still find its token.
func TestWideJSONContainerKeepsTheRegexScan(t *testing.T) {
	var body strings.Builder
	body.WriteString(`{"rows":[`)
	for index := 0; index < maxCorrelationStructuredFields+100; index++ {
		if index > 0 {
			body.WriteString(",")
		}
		fmt.Fprintf(&body, `%d`, index)
	}
	body.WriteString(`],"sessionToken":"sess-abcdef1234567890"}`)
	if extractType := candidateExtractType(t, body.String(), "sess-abcdef1234567890"); extractType == "" {
		t.Fatal("token in a document with a wide array was lost")
	}
}

func candidateExtractType(t *testing.T, body, value string) string {
	t.Helper()
	candidates, err := CandidatesChecked(body, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range candidates {
		if candidate.Value == value {
			return candidate.ExtractType
		}
	}
	return ""
}
