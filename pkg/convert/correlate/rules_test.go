package correlate

import "testing"

func TestDetectASPNetState(t *testing.T) {
	observations := []Observation{
		{
			ID: 1,
			ResponseBody: `<form>
<input type="hidden" name="__VIEWSTATE" value="/wEPDwUKLTی123abcLONGVALUE==">
<input type="hidden" name="__EVENTVALIDATION" value="/wEdAAZvalidation999XYZ==">
</form>`,
		},
		{
			ID:          2,
			RequestBody: "__VIEWSTATE=%2FwEPDwUKLTی123abcLONGVALUE%3D%3D&__EVENTVALIDATION=%2FwEdAAZvalidation999XYZ%3D%3D",
		},
	}
	// Use raw (un-encoded) values so the request-body contains check matches.
	observations[1].RequestBody = "__VIEWSTATE=/wEPDwUKLTی123abcLONGVALUE==&__EVENTVALIDATION=/wEdAAZvalidation999XYZ=="

	correlations := Detect(observations)
	if len(correlations) != 2 {
		t.Fatalf("expected 2 ASP.NET correlations, got %d: %#v", len(correlations), correlations)
	}
	for _, c := range correlations {
		if c.RuleID != RuleASPNetState {
			t.Fatalf("expected rule %s, got %s", RuleASPNetState, c.RuleID)
		}
		if c.Confidence < 0.9 {
			t.Fatalf("ASP.NET state should be high-confidence, got %.2f", c.Confidence)
		}
		if !HighConfidence(c) {
			t.Fatalf("ASP.NET state should be auto-applicable")
		}
	}
}

func TestConfidenceRankingOrder(t *testing.T) {
	// A CSRF token (high base) reused twice should outrank a numeric id reused once.
	observations := []Observation{
		{
			ID:           1,
			ResponseBody: `{"csrf_token":"abc123def456ghi789","order_id":4242}`,
		},
		{
			ID:          2,
			RequestBody: "token=abc123def456ghi789",
		},
		{
			ID:         3,
			RequestURL: "https://x/api?token=abc123def456ghi789&order_id=4242",
		},
	}
	correlations := Detect(observations)
	if len(correlations) < 2 {
		t.Fatalf("expected >=2 correlations, got %d: %#v", len(correlations), correlations)
	}
	if correlations[0].RuleID != RuleJSONCSRFToken {
		t.Fatalf("highest-confidence correlation should be the CSRF token, got %s (%.2f)", correlations[0].RuleID, correlations[0].Confidence)
	}
	if correlations[0].Confidence <= correlations[len(correlations)-1].Confidence {
		t.Fatalf("expected descending confidence order: %#v", correlations)
	}
	// Reuse must show up in the reasons.
	foundReuse := false
	for _, r := range correlations[0].Reasons {
		if r == "reused in 2 later requests" {
			foundReuse = true
		}
	}
	if !foundReuse {
		t.Fatalf("expected reuse reason, got %#v", correlations[0].Reasons)
	}
}

func TestScoreConfidenceClampAndSignals(t *testing.T) {
	// Short numeric value with a weak base is penalized.
	low, _ := scoreConfidence(Candidate{Value: "12", BaseConfidence: 0.5}, 1)
	if low >= 0.5 {
		t.Fatalf("short numeric value should be penalized, got %.2f", low)
	}
	// Long high-entropy value reused many times approaches the ceiling.
	high, reasons := scoreConfidence(Candidate{Value: "aB3$xY9!zQ2#wE7%rT4&uI1*oP0", BaseConfidence: 0.9}, 5)
	if high < 0.95 {
		t.Fatalf("strong candidate should be near-certain, got %.2f", high)
	}
	if high > 0.99 {
		t.Fatalf("confidence must be clamped to <= 0.99, got %.2f", high)
	}
	if len(reasons) == 0 {
		t.Fatalf("expected reasons")
	}
}

func TestRuleCatalogAndFamily(t *testing.T) {
	catalog := RuleCatalog()
	if len(catalog) == 0 {
		t.Fatal("catalog should not be empty")
	}
	seen := map[string]bool{}
	for _, r := range catalog {
		if seen[r.ID] {
			t.Fatalf("duplicate rule id %s", r.ID)
		}
		seen[r.ID] = true
		if FamilyForRule(r.ID) != r.Family {
			t.Fatalf("rule %s family mismatch: catalog=%s derived=%s", r.ID, r.Family, FamilyForRule(r.ID))
		}
	}
	if FamilyForRule("nonsense") != "" {
		t.Fatalf("unknown rule should map to empty family")
	}
}

func TestSetCookieSessionVsOtherConfidence(t *testing.T) {
	obs := []Observation{
		{ID: 1, ResponseHeaders: map[string]string{"Set-Cookie": "JSESSIONID=abcd1234efgh; Path=/"}},
		{ID: 2, RequestHeaders: map[string]string{"Cookie": "JSESSIONID=abcd1234efgh"}},
	}
	correlations := Detect(obs)
	if len(correlations) != 1 {
		t.Fatalf("expected 1, got %d", len(correlations))
	}
	if correlations[0].RuleID != RuleSetCookieSession {
		t.Fatalf("JSESSIONID should be session cookie rule, got %s", correlations[0].RuleID)
	}
}

func TestDetectExpandedResponseFamilies(t *testing.T) {
	observations := []Observation{
		{
			ID: 1,
			ResponseBody: `<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/">
  <soap:Header><wsa:MessageID>urn:uuid:msg-123456789</wsa:MessageID></soap:Header>
  <soap:Body><m:CreateOrderResponse correlationId="soap-corr-abcdef123456">ok</m:CreateOrderResponse></soap:Body>
</soap:Envelope>`,
			ResponseHeaders: map[string]string{
				"Location":         "https://idp.example.test/callback?code=code-abcdef123456&state=state-abcdef123456",
				"JMSCorrelationID": "jms-corr-abcdef123456",
			},
		},
		{
			ID:             2,
			RequestURL:     "https://client.example.test/callback?code=code-abcdef123456&state=state-abcdef123456",
			RequestBody:    "message=urn:uuid:msg-123456789&soap=soap-corr-abcdef123456",
			RequestHeaders: map[string]string{"JMSCorrelationID": "jms-corr-abcdef123456"},
		},
	}

	correlations := Detect(observations)
	wantRules := map[string]bool{
		RuleXMLSOAPToken:     false,
		RuleLocationParam:    false,
		RuleJMSCorrelationID: false,
	}
	for _, c := range correlations {
		if _, ok := wantRules[c.RuleID]; ok {
			wantRules[c.RuleID] = true
		}
	}
	for rule, found := range wantRules {
		if !found {
			t.Fatalf("missing rule %s in correlations: %#v", rule, correlations)
		}
	}
}

func TestDetectWebSocketCorrelationID(t *testing.T) {
	observations := []Observation{
		{ID: 1, ResponseBody: `{"subscriptionId":"sub-abcdef123456","messageId":"msg-abcdef123456"}`},
		{ID: 2, RequestBody: `{"ack":"msg-abcdef123456","subscriptionId":"sub-abcdef123456"}`},
	}
	correlations := Detect(observations)
	if len(correlations) < 2 {
		t.Fatalf("expected websocket correlations, got %#v", correlations)
	}
	for _, c := range correlations {
		if c.RuleID != RuleWebSocketID {
			t.Fatalf("expected websocket rule, got %#v", c)
		}
	}
}
