package salesforce

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
)

// The Salesforce nested-JSON pass shares the detector's precision rules: an
// OAuth token_type and an Aura action state are names, not server state, and
// a generic id the client already sent is not produced by the later response.
func TestNestedJSONCorrelationsSkipValuesThatAreNotServerState(t *testing.T) {
	const token = "00Dxx0000001gPL!AQ0AQBHIS08hfp220wZsey"
	const recordID = "a1k4M000004XZFrQAO"
	const echoedID = "0156g000000k3foAAA"
	observations := []correlate.Observation{
		{ID: 1, RequestURL: "https://example.my.salesforce.com/start?asset=" + echoedID},
		{
			ID:           2,
			RequestURL:   "https://example.my.salesforce.com/services/oauth2/token",
			ResponseBody: `{"token_type":"Bearer","access_token":"` + token + `","actions":[{"state":"SUCCESS","returnValue":{"recordId":"` + recordID + `","assetSourceId":"` + echoedID + `"}}]}`,
		},
		{
			ID:             3,
			RequestURL:     "https://example.lightning.force.com/aura?r=1",
			RequestHeaders: map[string]string{"Authorization": "Bearer " + token},
			RequestBody:    `message={"actions":[{"state":"SUCCESS","params":{"recordId":"` + recordID + `","assetSourceId":"` + echoedID + `"}}]}`,
		},
	}
	correlations, err := detectNestedJSONCorrelations(observations, map[string]bool{}, newSalesforceCorrelationBudget())
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{}
	for _, corr := range correlations {
		values[corr.Value] = corr.RuleID
	}
	for _, rejected := range []string{"Bearer", "SUCCESS", echoedID} {
		if rule, found := values[rejected]; found {
			t.Errorf("%q correlated by %s; it is not server state", rejected, rule)
		}
	}
	for _, kept := range []string{token, recordID} {
		if _, found := values[kept]; !found {
			t.Errorf("%q not correlated; got %v", kept, values)
		}
	}
}

// A shape extractor must not take a value away from an earlier generic
// producer: the session Set-Cookie precedes the Aura response that echoes it,
// and every request in between needs the cookie variable.
func TestDetectCorrelationsKeepsEarliestProducerAcrossShapeAndGenericPasses(t *testing.T) {
	const session = "00Ddq000002bZ5h!AQEAQCHrlSessionValue0123456789"
	entries := []harEntry{
		{ID: 1, URL: "https://example.my.salesforce.com/login", ResponseHeaders: map[string]string{"Set-Cookie": "sid=" + session + "; Path=/; Secure"}},
		{ID: 2, URL: "https://example.lightning.force.com/one/one.app", RequestHeaders: map[string]string{"Cookie": "sid=" + session}},
		{ID: 3, URL: "https://example.lightning.force.com/aura?r=1", RequestHeaders: map[string]string{"Cookie": "sid=" + session}, ResponseBody: `{"actions":[{"returnValue":{"sfdcSessionKey":"` + session + `"}}]}`},
		{ID: 4, URL: "https://example.lightning.force.com/aura?r=2", RequestHeaders: map[string]string{"Cookie": "sid=" + session}},
	}
	correlations, warnings, err := detectCorrelationsChecked(entries)
	if err != nil || len(warnings) != 0 {
		t.Fatalf("detect: err=%v warnings=%v", err, warnings)
	}
	var found []correlate.Correlation
	for _, corr := range correlations {
		if corr.Value == session {
			found = append(found, corr)
		}
	}
	if len(found) != 1 || found[0].FoundInID != 1 || found[0].RuleID != correlate.RuleSetCookieSession {
		t.Fatalf("session correlations = %#v, want only the Set-Cookie producer in entry 1", found)
	}
}
