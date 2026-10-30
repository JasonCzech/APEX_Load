package salesforce

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
)

// auraTokenHAR is a capture of auraTokenScript: the /one/one.app bootstrap on
// appHost (with bootstrapSetCookie as its Set-Cookie, when not empty) and two
// Aura POSTs on acme.lightning.force.com sending token.
func auraTokenHAR(t *testing.T, token, appHost, bootstrapSetCookie string) []byte {
	t.Helper()
	body := auraTokenScript(token).Actions[1].Body
	bootstrapHeaders := []map[string]string{}
	if bootstrapSetCookie != "" {
		bootstrapHeaders = append(bootstrapHeaders, map[string]string{"name": "Set-Cookie", "value": bootstrapSetCookie})
	}
	auraEntry := func(r string) map[string]any {
		return map[string]any{
			"request": map[string]any{
				"method":   "POST",
				"url":      "https://acme.lightning.force.com/aura?r=" + r,
				"headers":  []map[string]string{{"name": "Content-Type", "value": "application/x-www-form-urlencoded"}},
				"postData": map[string]any{"mimeType": "application/x-www-form-urlencoded", "text": body},
			},
			"response": map[string]any{
				"status":  200,
				"headers": []map[string]string{{"name": "Content-Type", "value": "application/json"}},
				"content": map[string]any{"mimeType": "application/json", "text": `{"actions":[{"id":"1;a","state":"SUCCESS","returnValue":null}]}`},
			},
		}
	}
	har := map[string]any{"log": map[string]any{"entries": []any{
		map[string]any{
			"request":  map[string]any{"method": "GET", "url": "https://" + appHost + "/one/one.app", "headers": []map[string]string{}},
			"response": map[string]any{"status": 200, "headers": bootstrapHeaders, "content": map[string]any{"mimeType": "text/html", "text": "<html></html>"}},
		},
		auraEntry("1"),
		auraEntry("2"),
	}}}
	raw, err := json.Marshal(har)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

const auraTokenFixture = "eyJub25jZSI6InJlY29yZGVkIn0.c2lnbmF0dXJl"

// A Chrome DevTools HAR drops the __Host-ERIC_* Set-Cookie that issues the
// desktop aura.token. The live server still sends it at replay, so the native
// preview reads the token from the /one/one.app response instead of replaying
// the recorded, stale one, and the review says the first replay confirms it.
func TestUncapturedAuraTokenIsReadFromTheBootstrapCookie(t *testing.T) {
	script := auraTokenScript(auraTokenFixture)
	review, err := Analyze(script, AnalyzeOptions{BuildNative: true, HARData: auraTokenHAR(t, auraTokenFixture, "acme.lightning.force.com", "")})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if review.NativePreview == nil || review.NativePreview.Script == nil {
		t.Fatal("NativePreview = nil")
	}
	var name string
	for _, corr := range review.NativePreview.Correlations {
		if corr.RuleID == correlate.RuleSalesforceAuraTokenUncapturedCookie {
			name = corr.Name
		}
	}
	if name == "" {
		t.Fatalf("correlations = %#v, want the uncaptured aura.token cookie correlation", review.NativePreview.Correlations)
	}
	actions := review.NativePreview.Script.Actions
	var extractor bool
	for _, ext := range actions[0].Extractors {
		if ext.Name == name && ext.Source == "header" && ext.Expression == auraTokenCookieExtractor {
			extractor = true
		}
	}
	if !extractor {
		t.Fatalf("bootstrap extractors = %#v, want %s reading the ERIC cookie", actions[0].Extractors, name)
	}
	for _, action := range actions[1:] {
		if strings.Contains(action.Body, auraTokenFixture) || !strings.Contains(action.Body, "aura.token={{var."+name+"}}") {
			t.Fatalf("%s body = %q, want aura.token read from %s", action.Name, action.Body, name)
		}
	}
	var note bool
	for _, warning := range review.Warnings {
		if strings.Contains(warning, "stale token") {
			t.Fatalf("review still warns of a stale token: %q", warning)
		}
		note = note || strings.Contains(warning, "first replay confirms")
	}
	if !note {
		t.Fatalf("warnings = %q, want the note that the first replay confirms the inferred extractor", review.Warnings)
	}
}

// A captured producer always wins: the inferred extractor is only a fallback.
func TestCapturedAuraTokenCookieKeepsItsOwnCorrelation(t *testing.T) {
	script := auraTokenScript(auraTokenFixture)
	cookie := "__Host-ERIC_PROD-123=" + auraTokenFixture + "; Path=/; Secure; HttpOnly"
	review, err := Analyze(script, AnalyzeOptions{BuildNative: true, HARData: auraTokenHAR(t, auraTokenFixture, "acme.lightning.force.com", cookie)})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	var captured bool
	for _, corr := range review.NativePreview.Correlations {
		if corr.RuleID == correlate.RuleSalesforceAuraTokenUncapturedCookie {
			t.Fatalf("captured cookie also produced the inferred correlation: %#v", corr)
		}
		captured = captured || corr.RuleID == correlate.RuleSalesforceAuraTokenCookie
	}
	if !captured {
		t.Fatalf("correlations = %#v, want the captured aura.token cookie correlation", review.NativePreview.Correlations)
	}
}

// __Host- cookies are host-only: a bootstrap on another host cannot set the
// cookie the Aura host sends, so nothing is inferred and the stale-token
// warning stays.
func TestUncapturedAuraTokenNeedsABootstrapOnTheAuraHost(t *testing.T) {
	script := auraTokenScript(auraTokenFixture)
	review, err := Analyze(script, AnalyzeOptions{BuildNative: true, HARData: auraTokenHAR(t, auraTokenFixture, "acme.my.salesforce.com", "")})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if review.NativePreview != nil {
		for _, corr := range review.NativePreview.Correlations {
			if corr.RuleID == correlate.RuleSalesforceAuraTokenUncapturedCookie {
				t.Fatalf("inferred a cookie from a bootstrap on another host: %#v", corr)
			}
		}
	}
	var stale bool
	for _, warning := range review.Warnings {
		stale = stale || strings.Contains(warning, "no response that issues it")
	}
	if !stale {
		t.Fatalf("warnings = %q, want the stale-token warning", review.Warnings)
	}
}

func TestAuraTokenCookieExtractorReadsTheERICCookieValue(t *testing.T) {
	pattern := regexp.MustCompile(auraTokenCookieExtractor)
	for _, tc := range []struct {
		surface, want string
	}{
		{"Set-Cookie: __Host-ERIC_PROD-123=abc.def-_; Path=/; Secure\r\n", "abc.def-_"},
		{"Set-Cookie: other=1; Path=/\r\nset-cookie: __Host-ERIC_PROD5550001=\"quoted.value\"; Path=/\r\n", "quoted.value"},
		{"Set-Cookie: ERIC_PROD-9=plain\r\n", "plain"},
		// The header extraction surface ends each line in a bare LF.
		{"Content-Type: text/html\nSet-Cookie: __Host-ERIC_PROD-1=lf.value\n", "lf.value"},
		{"Set-Cookie: __Host-ERICX=nope\r\n", ""},
		{"Set-Cookie: sid=__Host-ERIC_PROD-1=nope\r\n", ""},
	} {
		got := ""
		if match := pattern.FindStringSubmatch(tc.surface); match != nil {
			got = match[1]
		}
		if got != tc.want {
			t.Errorf("surface %q captured %q, want %q", tc.surface, got, tc.want)
		}
	}
}

func auraTokenObservation(id int, rawURL, token string) correlate.Observation {
	body := "message=%7B%7D"
	if token != "" {
		body += "&aura.token=" + url.QueryEscape(token)
	}
	return correlate.Observation{ID: id, RequestURL: rawURL, RequestBody: body}
}

// A capture with many Aura requests and no bootstrap (an Experience Cloud
// capture, or one started after page load) is decided in one linear pass.
func TestUncapturedAuraTokenPassIsLinearWithoutABootstrap(t *testing.T) {
	observations := make([]correlate.Observation, 20000)
	for index := range observations {
		observations[index] = auraTokenObservation(index+1, "https://acme.my.site.com/s/sfsites/aura?r="+strconv.Itoa(index), "token-"+strconv.Itoa(index%7)+"-abcdefghij")
	}
	started := time.Now()
	correlations, err := detectUncapturedAuraTokenCorrelations(observations, map[string]bool{}, newSalesforceCorrelationBudget(), maxSalesforceShapeCorrelations)
	if err != nil {
		t.Fatal(err)
	}
	if len(correlations) != 0 {
		t.Fatalf("correlations = %#v, want none without a bootstrap", correlations)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("20000 Aura requests took %s, want a linear pass", elapsed)
	}
}

// Each distinct token is correlated from the bootstrap before its first use,
// under its own name; a percent-encoded token is read decoded; a placeholder
// and a token already correlated are left alone; the limit is honored.
func TestUncapturedAuraTokensAreDecidedAtTheirFirstUse(t *testing.T) {
	const host = "https://acme.lightning.force.com"
	observations := []correlate.Observation{
		{ID: 1, RequestURL: host + "/one/one.app"},
		auraTokenObservation(2, host+"/aura?r=1", "first+token/value=="),
		auraTokenObservation(3, host+"/aura?r=2", "first+token/value=="),
		auraTokenObservation(4, host+"/aura?r=3", "undefined"),
		{ID: 5, RequestURL: host + "/one/one.app"},
		auraTokenObservation(6, host+"/aura?r=4", "second-token-value"),
		auraTokenObservation(7, host+"/aura?r=5", "already-correlated"),
	}
	correlations, err := detectUncapturedAuraTokenCorrelations(observations, map[string]bool{"already-correlated": true}, newSalesforceCorrelationBudget(), maxSalesforceShapeCorrelations)
	if err != nil {
		t.Fatal(err)
	}
	if len(correlations) != 2 {
		t.Fatalf("correlations = %#v, want two tokens", correlations)
	}
	first, second := correlations[0], correlations[1]
	if first.Name != "corr_salesforce_aura_token" || first.Value != "first+token/value==" || first.FoundInID != 1 || len(first.UsedInIDs) != 2 {
		t.Fatalf("first = %#v", first)
	}
	if second.Name != "corr_salesforce_aura_token_2" || second.Value != "second-token-value" || second.FoundInID != 5 {
		t.Fatalf("second = %#v", second)
	}
	limited, err := detectUncapturedAuraTokenCorrelations(observations, map[string]bool{}, newSalesforceCorrelationBudget(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 1 {
		t.Fatalf("limit 1 gave %d correlations", len(limited))
	}
}

// A capture that keeps cookies and shows the bootstrap set no ERIC cookie
// (the browser already held it), or a request already sending the token in an
// ERIC cookie, is evidence against the inference: the token keeps its
// stale-token warning instead of an extractor that would fail every
// iteration at the bootstrap.
func TestUncapturedAuraTokenIsNotInferredAgainstTheCapturesEvidence(t *testing.T) {
	const host = "https://acme.lightning.force.com"
	consumer := auraTokenObservation(2, host+"/aura?r=1", "held-token-value")
	withoutERIC := correlate.Observation{ID: 1, RequestURL: host + "/one/one.app", ResponseHeaders: map[string]string{"Set-Cookie": "BrowserId=xyz123abcdef; Path=/"}}
	if got, _ := detectUncapturedAuraTokenCorrelations([]correlate.Observation{withoutERIC, consumer}, map[string]bool{}, newSalesforceCorrelationBudget(), maxSalesforceShapeCorrelations); len(got) != 0 {
		t.Fatalf("inferred despite a bootstrap response whose Set-Cookie lines set no ERIC cookie: %#v", got)
	}
	held := correlate.Observation{ID: 1, RequestURL: host + "/one/one.app", RequestHeaders: map[string]string{"Cookie": "sid=abc; __Host-ERIC_PROD-1=held-token-value"}}
	if got, _ := detectUncapturedAuraTokenCorrelations([]correlate.Observation{held, consumer}, map[string]bool{}, newSalesforceCorrelationBudget(), maxSalesforceShapeCorrelations); len(got) != 0 {
		t.Fatalf("inferred although the browser already sent the token in an ERIC cookie: %#v", got)
	}
	setsERIC := correlate.Observation{ID: 1, RequestURL: host + "/one/one.app", ResponseHeaders: map[string]string{"Set-Cookie": "BrowserId=xyz123abcdef; Path=/\n__Host-ERIC_PROD-1=other-value; Path=/"}}
	if got, _ := detectUncapturedAuraTokenCorrelations([]correlate.Observation{setsERIC, consumer}, map[string]bool{}, newSalesforceCorrelationBudget(), maxSalesforceShapeCorrelations); len(got) != 1 {
		t.Fatalf("correlations = %#v, want the inference when the bootstrap does set an ERIC cookie", got)
	}
}

func TestAuraTokenInferredWarningNamesASanitizedExportOnlyWhenItIsOne(t *testing.T) {
	inferred := []correlate.Correlation{{Name: "corr_salesforce_aura_token", RuleID: correlate.RuleSalesforceAuraTokenUncapturedCookie}}
	script := auraTokenScript("{{var.corr_salesforce_aura_token}}")
	if warning := auraTokenProvenanceWarning(script, inferred, false); strings.Contains(warning, "sanitized") || !strings.Contains(warning, "first replay confirms") {
		t.Fatalf("unsanitized warning = %q", warning)
	}
	if warning := auraTokenProvenanceWarning(script, inferred, true); !strings.Contains(warning, "sanitized export") {
		t.Fatalf("sanitized warning = %q", warning)
	}
}
