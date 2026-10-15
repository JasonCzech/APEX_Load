package correlate

import (
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// approvalPageBody is shaped like the Salesforce Mobile SDK approval page:
// a 200 response whose script sends the web view to the app's private-scheme
// OAuth callback twice (replace() and an href fallback) with the same code.
const approvalPageBody = `<html><body><script>
if (window.webkit) { window.location.replace('testsfdc:///mobilesdk/detect/oauth/done?display=touch&code=aPrxAbCdEf123456%3D%3D'); }
else { window.location.href ='testsfdc:///mobilesdk/detect/oauth/done?display=touch&code=aPrxAbCdEf123456%3D%3D'; }
var ready = a ? b:c ? d : e;
</script>
<a href="https://example.test/callback?code=webcodevalue123">web</a>
<a href="com.example.app:/oauth/done?state=s1&amp;code=htmlEscaped9876">app</a>
<a href="com.example.app:/oauth/done?code=short">too short</a>
<a href="com.example.app:/oauth/other?state=nocode">no code</a>
</body></html>`

func TestAppSchemeOAuthCodeCandidatesFindsCallbackCodes(t *testing.T) {
	candidates := appSchemeOAuthCodeCandidates(approvalPageBody, map[string]string{"Content-Type": "text/html"})
	want := map[string]bool{"aPrxAbCdEf123456==": false, "htmlEscaped9876": false}
	if len(candidates) != len(want) {
		t.Fatalf("got %d candidates, want %d (one per distinct app-scheme code): %#v", len(candidates), len(want), candidates)
	}
	for _, candidate := range candidates {
		if _, ok := want[candidate.Value]; !ok {
			t.Fatalf("unexpected candidate %#v", candidate)
		}
		want[candidate.Value] = true
		if candidate.RuleID != RuleHTMLAppSchemeOAuthCode || candidate.Source != "body" ||
			candidate.ExtractType != "regex" || candidate.Convert != "url_decode" || candidate.BaseConfidence < 0.9 {
			t.Fatalf("candidate shape = %#v", candidate)
		}
		// The extractor must capture the wire spelling, which url_decode turns
		// back into the candidate value.
		match := regexp.MustCompile(candidate.Expression).FindStringSubmatch(approvalPageBody)
		if len(match) < 2 {
			t.Fatalf("expression %q does not match the page", candidate.Expression)
		}
		decoded, err := url.QueryUnescape(match[1])
		if err != nil || decoded != candidate.Value {
			t.Fatalf("expression %q extracts %q (decoded %q, err %v), want %q", candidate.Expression, match[1], decoded, err, candidate.Value)
		}
	}
	for value, found := range want {
		if !found {
			t.Fatalf("missing candidate for %q", value)
		}
	}
}

func TestAppSchemeOAuthCodeCandidatesIgnoresWebAndCodelessURLs(t *testing.T) {
	for name, body := range map[string]string{
		"web callback":    `location.href = 'https://example.test/callback?code=webcodevalue123';`,
		"no code":         `location.href = 'com.example.app:/oauth/done?state=abcdefghijk';`,
		"host and port":   `fetch('api.example.test:8443/callback?code=webcodevalue123');`,
		"javascript link": `<a href="javascript:void(0)?code=notacallback123">x</a>`,
	} {
		if candidates := appSchemeOAuthCodeCandidates(body, nil); len(candidates) != 0 {
			t.Fatalf("%s: unexpected candidates %#v", name, candidates)
		}
	}
}

func TestDetectAppSchemeOAuthCodeFeedsTokenRequest(t *testing.T) {
	observations := []Observation{
		{
			ID:           1,
			RequestURL:   "https://acme.my.salesforce.com/_ui/identity/oauth/ui/AuthorizationPage",
			RequestBody:  "_CONFIRMATIONTOKEN=abc&oauth_response=approve",
			ResponseBody: approvalPageBody,
		},
		{
			ID:         2,
			RequestURL: "https://acme.my.salesforce.com/services/oauth2/token",
			RequestBody: "format=json&redirect_uri=testsfdc%3A%2F%2F%2Fmobilesdk%2Fdetect%2Foauth%2Fdone&client_id=3MVGexampleclient" +
				"&code_verifier=verifierExample123&grant_type=hybrid_auth_code&code=aPrxAbCdEf123456%3D%3D",
		},
	}
	var code *Correlation
	correlations := Detect(observations)
	for index := range correlations {
		if correlations[index].RuleID == RuleHTMLAppSchemeOAuthCode {
			code = &correlations[index]
		}
	}
	if code == nil {
		t.Fatalf("no app-scheme OAuth code correlation in %#v", correlations)
	}
	if code.FoundInID != 1 || !slices.Contains(code.UsedInIDs, 2) || !HighConfidence(*code) {
		t.Fatalf("code correlation = found %d, used %v, confidence %.2f; want found in the approval page, used by the token request, high confidence",
			code.FoundInID, code.UsedInIDs, code.Confidence)
	}
	if !strings.Contains(code.Expression, regexp.QuoteMeta("testsfdc:///mobilesdk/detect/oauth/done")) {
		t.Fatalf("expression %q is not anchored to the callback URL", code.Expression)
	}
}

// An OAuth server can return the callback inside a JSON string, escaping its
// slashes and ampersands.
func TestAppSchemeOAuthCodeCandidatesReadsJSONEscapedCallback(t *testing.T) {
	body := `{"redirect":"com.example.app:\/\/oauth\/done?state=s1\u0026code=jsonEscaped%3D9876\u0026x=1"}`
	candidates := appSchemeOAuthCodeCandidates(body, map[string]string{"content-type": "application/json"})
	if len(candidates) != 1 || candidates[0].Value != "jsonEscaped=9876" {
		t.Fatalf("candidates = %#v; want the decoded JSON-escaped code", candidates)
	}
	match := regexp.MustCompile(candidates[0].Expression).FindStringSubmatch(body)
	if len(match) < 2 || match[1] != "jsonEscaped%3D9876" {
		t.Fatalf("expression %q extracts %q, want the wire code", candidates[0].Expression, match)
	}
}

func TestAppSchemeOAuthCodeCandidatesSkipsScriptBundles(t *testing.T) {
	headers := map[string]string{"Content-Type": "application/javascript; charset=utf-8"}
	if candidates := appSchemeOAuthCodeCandidates(approvalPageBody, headers); len(candidates) != 0 {
		t.Fatalf("a script bundle must not be scanned: %#v", candidates)
	}
}

// The candidate value must equal what the extractor captures, which stops at
// a backslash, even when an escaped character follows the code directly.
func TestAppSchemeOAuthCodeCandidateValueMatchesExtractorAtEscape(t *testing.T) {
	body := `{"redirect":"com.example.app:\/cb?code=abcdefgh1234\/tail"}`
	candidates := appSchemeOAuthCodeCandidates(body, nil)
	if len(candidates) != 1 || candidates[0].Value != "abcdefgh1234" {
		t.Fatalf("candidates = %#v; want the code up to the escape", candidates)
	}
	match := regexp.MustCompile(candidates[0].Expression).FindStringSubmatch(body)
	if len(match) < 2 || match[1] != candidates[0].Value {
		t.Fatalf("expression %q extracts %q, want %q", candidates[0].Expression, match, candidates[0].Value)
	}
}

// A too-short code earlier on the same callback still counts toward the
// extractor's occurrence ordinal, so the later, real code is the one replay reads.
func TestAppSchemeOAuthCodeCandidatesKeepOrdinalAfterSkippedCode(t *testing.T) {
	body := `<a href="com.example.app:/done?code=short">a</a><a href="com.example.app:/done?code=longenough123">b</a>`
	candidates := appSchemeOAuthCodeCandidates(body, nil)
	if len(candidates) != 1 || candidates[0].Value != "longenough123" || candidates[0].ExtractType != "regex_ordinal" {
		t.Fatalf("candidates = %#v; want the second occurrence addressed by ordinal", candidates)
	}
}

// The token response names the signed-in user's identity URL, which the
// Mobile SDK requests next; each virtual user has its own.
func TestDetectSalesforceIdentityURLFeedsIdentityRequest(t *testing.T) {
	identity := "https://login.salesforce.com/id/00D5g000004ExAmPLE/0055g00000ABCDeAAH"
	observations := []Observation{
		{
			ID:           1,
			RequestURL:   "https://acme.my.salesforce.com/services/oauth2/token",
			ResponseBody: `{"access_token":"00D5g000004ExAmPLE!AQ0AQexampleToken","instance_url":"https://acme.my.salesforce.com","id":"` + identity + `","token_type":"Bearer"}`,
		},
		{ID: 2, RequestURL: identity},
	}
	var found *Correlation
	correlations := Detect(observations)
	for index := range correlations {
		if correlations[index].RuleID == RuleSalesforceOAuthIdentityURL {
			found = &correlations[index]
		}
	}
	if found == nil {
		t.Fatalf("no identity URL correlation in %#v", correlations)
	}
	if found.Value != identity || found.FoundInID != 1 || !slices.Contains(found.UsedInIDs, 2) || found.ExtractType != "jsonpath" ||
		found.Expression != "$.id" || !HighConfidence(*found) {
		t.Fatalf("identity correlation = %#v", found)
	}
}

func TestSalesforceOAuthIdentityCandidatesNeedATokenResponse(t *testing.T) {
	for name, body := range map[string]string{
		"no access token": `{"instance_url":"https://acme.my.salesforce.com","id":"https://login.salesforce.com/id/00D/005"}`,
		"no instance":     `{"access_token":"tok","id":"https://login.salesforce.com/id/00D/005"}`,
		"not an identity": `{"access_token":"tok","instance_url":"https://acme.my.salesforce.com","id":"https://acme.my.salesforce.com/services/data"}`,
		"plain http":      `{"access_token":"tok","instance_url":"https://acme.my.salesforce.com","id":"http://login.salesforce.com/id/00D/005"}`,
		"not JSON":        `access_token=tok&instance_url=x&id=https://login.salesforce.com/id/00D/005`,
	} {
		if candidates := salesforceOAuthIdentityCandidates(body); len(candidates) != 0 {
			t.Fatalf("%s: candidates = %#v", name, candidates)
		}
	}
}

// The authorize endpoint states each sign-in in the approval page's source
// parameter; the approval page request must carry the replayed sign-in's own.
func TestDetectSalesforceAuthorizeSourceFeedsApprovalPage(t *testing.T) {
	source := "SRCa1b2c3d4e5f6g7h8i9j0k1l2m3n4o5p6q7r8s9t0"
	approval := "https://acme.my.salesforce.com/setup/secur/RemoteAccessAuthorizationPage.apexp?source=" + source + "&display=touch"
	observations := []Observation{
		{ID: 1, RequestURL: "https://acme.my.salesforce.com/services/oauth2/authorize?response_type=code", ResponseHeaders: map[string]string{"Location": approval}},
		{ID: 2, RequestURL: approval},
	}
	var found *Correlation
	correlations := Detect(observations)
	for index := range correlations {
		if correlations[index].RuleID == RuleSalesforceOAuthAuthorizePage {
			found = &correlations[index]
		}
	}
	if found == nil {
		t.Fatalf("no authorize source correlation in %#v", correlations)
	}
	if found.Value != source || found.Name != "corr_salesforce_authorize_source" || found.FoundInID != 1 ||
		!slices.Contains(found.UsedInIDs, 2) || found.Source != "header" || !HighConfidence(*found) {
		t.Fatalf("authorize source correlation = %#v", found)
	}
	match := regexp.MustCompile(found.Expression).FindStringSubmatch("Location: " + approval)
	if len(match) < 2 || match[1] != source {
		t.Fatalf("expression %q extracts %q, want the source", found.Expression, match)
	}

	// The same parameter on any other redirect is not a sign-in state.
	other := "https://app.example.test/landing?source=" + source
	for _, candidate := range locationCandidates("Location", other, 0, 1) {
		if candidate.RuleID == RuleSalesforceOAuthAuthorizePage {
			t.Fatalf("a non-Salesforce redirect produced %#v", candidate)
		}
	}
}
