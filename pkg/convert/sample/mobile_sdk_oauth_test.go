package sample

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// Synthetic values shaped like a Salesforce Mobile SDK hybrid sign-in: the org
// ID prefixes the access token and the frontdoor session id, as it does on a
// real org.
const (
	mobileSDKOrgID       = "00D5g000004XyZa"
	mobileSDKAccessToken = mobileSDKOrgID + "!AQ0AQHk7Z2pR9sTxYwVbN3mLq8JdFgH1cKeU4oPiA6zSyBnWtXrD5vMhQjG0lCfE2uIk9aY"
	mobileSDKCodeWire    = "aPrxQk2Lm9ZtR4vB7nWc1sYe5uJh8gDf3xKoA6pI0qTzEyNbMw%3D%3D"
	mobileSDKCallback    = "testsfdc:///mobilesdk/detect/oauth/done"
)

func formRequest(entry map[string]any, body string, headers ...map[string]any) map[string]any {
	request := entry["request"].(map[string]any)
	request["postData"] = map[string]any{"mimeType": "application/x-www-form-urlencoded", "text": body}
	request["headers"] = append([]any{map[string]any{"name": "Content-Type", "value": "application/x-www-form-urlencoded"}}, anyHeaders(headers)...)
	return entry
}

func withRequestHeaders(entry map[string]any, headers ...map[string]any) map[string]any {
	entry["request"].(map[string]any)["headers"] = anyHeaders(headers)
	return entry
}

func withResponseHeaders(entry map[string]any, headers ...map[string]any) map[string]any {
	response := entry["response"].(map[string]any)
	response["headers"] = append(response["headers"].([]any), anyHeaders(headers)...)
	return entry
}

func anyHeaders(headers []map[string]any) []any {
	out := make([]any, 0, len(headers))
	for _, header := range headers {
		out = append(out, header)
	}
	return out
}

func mobileSDKSignInHAR(t *testing.T) string {
	t.Helper()
	approvalPage := `<html><body><script>
if (window.webkit) { window.location.replace('` + mobileSDKCallback + `?display=touch&code=` + mobileSDKCodeWire + `'); }
else { window.location.href ='` + mobileSDKCallback + `?display=touch&code=` + mobileSDKCodeWire + `'; }
</script></body></html>`
	tokenResponse := `{"access_token":"` + mobileSDKAccessToken + `","refresh_token":"5Aep861dlMxAL.LhVTuPRa_k9Zx3vWqH8sJbTnYcGfEoLi2Ur","instance_url":"https://acme.my.salesforce.com",` +
		`"id":"https://login.salesforce.com/id/` + mobileSDKOrgID + `EAA/0055g00000ABCDeAAH","token_type":"Bearer"}`
	bearer := map[string]any{"name": "Authorization", "value": "Bearer " + mobileSDKAccessToken}
	return recorderHAR(t,
		recorderEntry("2026-01-01T00:00:00Z", "GET", "https://acme.my.salesforce.com/.well-known/auth-configuration", 200, "application/json",
			`{"OrgId":"`+mobileSDKOrgID+`","LoginPageEnabled":true}`, nil),
		formRequest(recorderEntry("2026-01-01T00:00:05Z", "POST", "https://acme.my.salesforce.com/_ui/identity/oauth/ui/AuthorizationPage", 200, "text/html",
			approvalPage, nil), "oauth_response=approve&display=touch"),
		formRequest(recorderEntry("2026-01-01T00:00:06Z", "POST", "https://acme.my.salesforce.com/services/oauth2/token", 200, "application/json",
			tokenResponse, nil),
			"format=json&redirect_uri=testsfdc%3A%2F%2F%2Fmobilesdk%2Fdetect%2Foauth%2Fdone&client_id=3MVGexampleConsumerKey"+
				"&code_verifier=verifierExampleValue1234567890&grant_type=hybrid_auth_code&code="+mobileSDKCodeWire),
		withRequestHeaders(recorderEntry("2026-01-01T00:00:07Z", "GET", "https://acme.my.salesforce.com/services/data/v66.0/query?q=SELECT+Id+FROM+Contact", 200,
			"application/json", `{"totalSize":0,"done":true,"records":[]}`, nil), bearer),
		withRequestHeaders(recorderEntry("2026-01-01T00:00:08Z", "GET", "https://acme.my.salesforce.com/services/data/v66.0/limits", 200,
			"application/json", `{"DailyApiRequests":{"Max":15000}}`, nil), bearer),
	)
}

// The proxy recorder converts with HighConfidenceOnly. A Mobile SDK sign-in
// must come out replayable: the code from the approval page's app-scheme
// callback feeds the token request, and the token response's access token is
// wired into the Bearer header even though the org ID it starts with is a
// separate, review-only candidate.
func TestMobileSDKHybridSignInCorrelatesCodeAndBearerToken(t *testing.T) {
	result, err := Convert(ImportRequest{Format: "har", Content: mobileSDKSignInHAR(t), Options: ConvertOptions{HighConfidenceOnly: true, OmitSourceReview: true}})
	if err != nil {
		t.Fatal(err)
	}
	// The sign-in, through the token exchange, runs in init; the API calls
	// after it are the iteration.
	if len(result.Script.Init) != 3 || len(result.Script.Actions) != 2 {
		t.Fatalf("init %d steps, actions %d; want the sign-in (3) in init and 2 API calls", len(result.Script.Init), len(result.Script.Actions))
	}
	actions := append(append([]dsl.Action(nil), result.Script.Init...), result.Script.Actions...)
	var code, token *Correlation
	for index := range result.Correlations {
		switch result.Correlations[index].RuleID {
		case correlate.RuleHTMLAppSchemeOAuthCode:
			code = &result.Correlations[index]
		case correlate.RuleJSONAuthToken:
			if strings.Contains(result.Correlations[index].Name, "access_token") {
				token = &result.Correlations[index]
			}
		}
	}
	if code == nil || !code.PreApplied || code.ReviewAction != "" || code.FoundInStep != 2 {
		t.Fatalf("code correlation = %+v; want it applied from the approval page", code)
	}
	if body := actions[2].Body; !strings.Contains(body, "code={{var."+code.Name+"}}") || strings.Contains(body, mobileSDKCodeWire) {
		t.Fatalf("token request body = %q; want the recorded code replaced by {{var.%s}}", body, code.Name)
	}
	if !hasExtractor(actions[1].Extractors, code.Name) {
		t.Fatalf("approval page extractors = %+v; want %s", actions[1].Extractors, code.Name)
	}
	if token == nil || !token.PreApplied || token.ReviewAction != "" {
		t.Fatalf("access token correlation = %+v; want it applied despite the org ID inside it", token)
	}
	for _, step := range []int{3, 4} {
		if got := actions[step].Headers["Authorization"]; got != "Bearer {{var."+token.Name+"}}" {
			t.Fatalf("action %d Authorization = %q; want the extracted token", step+1, got)
		}
	}
	for _, corr := range result.Correlations {
		if corr.Value == mobileSDKOrgID && (corr.PreApplied || corr.ReviewAction != "manual_review_required") {
			t.Fatalf("the low-confidence org ID must stay for review: %+v", corr)
		}
	}
}

// Without HighConfidenceOnly every candidate is applied, so an org ID inside
// the access token is a real collision between two rewrites and both stay for
// manual replacement, as before.
func TestAppliedLiteralInsideAppliedLiteralStillCollides(t *testing.T) {
	result, err := Convert(ImportRequest{Format: "har", Content: mobileSDKSignInHAR(t), Options: ConvertOptions{OmitSourceReview: true}})
	if err != nil {
		t.Fatal(err)
	}
	for _, corr := range result.Correlations {
		if corr.Value == mobileSDKAccessToken && corr.ReviewAction != manualCorrelationValueReplacement {
			t.Fatalf("access token = %+v; want manual replacement while the applied org ID collides with it", corr)
		}
	}
}

// Two rules recognize the same frontdoor session id in one Location header.
// They collapse to one correlation, which is applied instead of both being
// held for review as mutual collisions.
func TestSameSourceDuplicateCorrelationsCollapse(t *testing.T) {
	sid := mobileSDKAccessToken
	location := "https://acme.my.salesforce.com/secur/frontdoor.jsp?sid=" + strings.ReplaceAll(sid, "!", "%21") + "&retURL=%2Fhome%2Fhome.jsp&display=touch"
	content := recorderHAR(t,
		withResponseHeaders(formRequest(recorderEntry("2026-01-01T00:00:00Z", "POST", "https://acme.my.salesforce.com/", 302, "text/html", "", nil),
			"un=user%40example.test&pw=example-password&display=touch"), map[string]any{"name": "Location", "value": location}),
		recorderEntry("2026-01-01T00:00:01Z", "GET", location, 200, "text/html", "<p>home</p>", nil),
	)
	result, err := Convert(ImportRequest{Format: "har", Content: content, Options: ConvertOptions{HighConfidenceOnly: true, OmitSourceReview: true}})
	if err != nil {
		t.Fatal(err)
	}
	var sidCorrelations []Correlation
	for _, corr := range result.Correlations {
		if corr.Value == sid || corr.Value == strings.ReplaceAll(sid, "!", "%21") {
			sidCorrelations = append(sidCorrelations, corr)
		}
	}
	if len(sidCorrelations) != 1 {
		t.Fatalf("got %d correlations for the session id, want 1: %+v", len(sidCorrelations), sidCorrelations)
	}
	if !sidCorrelations[0].PreApplied || sidCorrelations[0].ReviewAction != "" {
		t.Fatalf("session id correlation = %+v; want it applied", sidCorrelations[0])
	}
	if target := result.Script.Actions[1].Target; strings.Contains(target, "%21AQ0AQ") || !strings.Contains(target, "{{var."+sidCorrelations[0].Name) {
		t.Fatalf("frontdoor target = %q; want the session id replaced by {{var.%s}}", target, sidCorrelations[0].Name)
	}
}

func TestCollapseSameSourceDuplicateCorrelationsKeepsHigherConfidence(t *testing.T) {
	exchanges := []exchange{
		{URL: "https://acme.test/login"},
		{URL: "https://acme.test/home"},
		{URL: "https://acme.test/frontdoor?sid=abc123XYZ"},
		{URL: "https://acme.test/other"},
		{URL: "https://acme.test/next?x=other-value-1"},
		{URL: "https://acme.test/also?sid=abc123XYZ"},
	}
	collapsed := collapseSameSourceDuplicateCorrelations(exchanges, []Correlation{
		{Name: "corr_location_sid", Value: "abc123XYZ", FoundInStep: 2, UsedInSteps: []int{3}, UsedInEntries: []int{4}, Confidence: 0.88},
		{Name: "corr_other", Value: "other-value-1", FoundInStep: 2, UsedInSteps: []int{5}, Confidence: 0.9},
		{Name: "corr_salesforce_frontdoor_sid", Value: "abc123XYZ", FoundInStep: 2, UsedInSteps: []int{6}, UsedInEntries: []int{7}, Confidence: 0.95},
		{Name: "corr_same_value_other_source", Value: "abc123XYZ", FoundInStep: 4, UsedInSteps: []int{6}, Confidence: 0.9},
	})
	if len(collapsed) != 3 {
		t.Fatalf("got %d correlations, want 3: %+v", len(collapsed), collapsed)
	}
	kept := collapsed[0]
	if kept.Name != "corr_salesforce_frontdoor_sid" || !equalInts(kept.UsedInSteps, []int{3, 6}) || !equalInts(kept.UsedInEntries, []int{4, 7}) {
		t.Fatalf("kept = %+v; want the higher-confidence rule with the union of consumers, in the first one's position", kept)
	}
	if collapsed[2].FoundInStep != 4 {
		t.Fatalf("a different source step must not collapse: %+v", collapsed)
	}
}

// A rule that keeps the wire spelling and one that decodes the same parameter
// are duplicates; the kept detection must still be able to rewrite every
// consumer of the dropped one, or both stay.
func TestCollapseSameSourceDuplicateCorrelationsMatchesDecodedAndWireSpelling(t *testing.T) {
	exchanges := []exchange{
		{URL: "https://acme.test/login"},
		{URL: "https://acme.test/frontdoor?sid=00Dx%21AQabc"},
		{URL: "https://acme.test/header-only", Headers: map[string]string{"X-Session": "00Dx!AQabc"}},
	}
	wire := Correlation{Name: "corr_salesforce_frontdoor_sid", Value: "00Dx%21AQabc", FoundInStep: 1, UsedInSteps: []int{2}, Confidence: 0.95}
	decoded := Correlation{Name: "corr_location_sid", Value: "00Dx!AQabc", Convert: "url_decode", FoundInStep: 1, UsedInSteps: []int{2}, Confidence: 0.88}
	if collapsed := collapseSameSourceDuplicateCorrelations(exchanges, []Correlation{decoded, wire}); len(collapsed) != 1 || collapsed[0].Name != wire.Name {
		t.Fatalf("collapsed = %+v; want only the higher-confidence wire-spelling detection", collapsed)
	}
	// The decoded detection also feeds a header that carries the decoded
	// spelling, which the wire-spelling value cannot rewrite.
	decoded.UsedInSteps = []int{2, 3}
	if collapsed := collapseSameSourceDuplicateCorrelations(exchanges, []Correlation{decoded, wire}); len(collapsed) != 2 {
		t.Fatalf("collapsed = %+v; want both kept when the survivor cannot serve every consumer", collapsed)
	}
	unrelated := Correlation{Name: "corr_plain", Value: "00Dx%21AQabc", FoundInStep: 1, UsedInSteps: []int{2}, Confidence: 0.9}
	other := Correlation{Name: "corr_plain_decoded", Value: "00Dx!AQabc", FoundInStep: 1, UsedInSteps: []int{2}, Confidence: 0.8}
	if collapsed := collapseSameSourceDuplicateCorrelations(exchanges, []Correlation{other, unrelated}); len(collapsed) != 2 {
		t.Fatalf("collapsed = %+v; without url_decode, different spellings are different values", collapsed)
	}
}

func hasExtractor(extractors []dsl.Extractor, name string) bool {
	for _, extractor := range extractors {
		if extractor.Name == name {
			return true
		}
	}
	return false
}

func equalInts(left, right []int) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
