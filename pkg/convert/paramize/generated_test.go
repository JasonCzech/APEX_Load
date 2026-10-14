package paramize

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

const (
	testMessageID      = "3f2b8c1e-9a4d-4e7b-8c21-5d6e7f8a9b0c"
	testConversationID = "a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d"
	testTraceID        = "4bf92f3577b34da6"
	testSpanID         = "a3ce929d0e0e4736"
	testCredentialID   = "11111111-2222-4333-8444-555555555555"
)

func generatedScript(actions ...dsl.Action) *dsl.Script {
	return &dsl.Script{
		Name: "test", Version: "1.0", Protocol: "http",
		Config:  dsl.ScriptConfig{BaseURL: "https://api.example.com"},
		Actions: actions,
	}
}

func generatedOnly(result Result) []Suggestion {
	var out []Suggestion
	for _, suggestion := range result.Suggestions {
		if suggestion.Kind == KindGenerated {
			out = append(out, suggestion)
		}
	}
	return out
}

func suggestGenerated(script *dsl.Script) []Suggestion {
	return generatedOnly(Suggest(script, Options{EnableGenerated: true, EnableDataParams: true, EnableSecrets: true}))
}

// A SOAP request's message ID and time stamp, and a Jaeger-style trace header,
// are made up per request; each becomes a parameter generated every iteration.
func TestGeneratedValuesFromASOAPRequest(t *testing.T) {
	script := generatedScript(dsl.Action{
		Name: "inquire", Type: "http_request", Method: "POST", Target: "/soap",
		HeaderList: dsl.HeaderList{
			{Name: "idp-trace-id", Value: testTraceID + ":" + testSpanID + ":0:1"},
			{Name: "X-Correlation-Id", Value: "Partner~CNG-CSI~" + testConversationID},
			{Name: "Authorization", Value: "Bearer " + testCredentialID},
		},
		Body: `<soap:Envelope><soap:Header><ns2:messageId>` + testMessageID + `</ns2:messageId>` +
			`<dateTimeStamp>2018-05-04T10:22:33.123Z</dateTimeStamp></soap:Header></soap:Envelope>`,
	})
	suggestions := suggestGenerated(script)
	byRule := map[string][]Suggestion{}
	for _, suggestion := range suggestions {
		byRule[suggestion.RuleID] = append(byRule[suggestion.RuleID], suggestion)
		if !suggestion.AutoApply {
			t.Errorf("%s (%s) is not selected: %s", suggestion.VarName, suggestion.RuleID, suggestion.Rationale)
		}
		if strings.Contains(suggestion.Rationale, suggestion.LiteralValue) {
			t.Errorf("rationale %q quotes the recorded value", suggestion.Rationale)
		}
	}
	if got := len(byRule[generatedRuleUUID]); got != 2 {
		t.Fatalf("uuid suggestions = %d, want the message and correlation IDs: %+v", got, suggestions)
	}
	if got := len(byRule[generatedRuleHex]); got != 2 {
		t.Fatalf("hex suggestions = %d, want the trace and span IDs", got)
	}
	if got := len(byRule[generatedRuleTimestamp]); got != 1 {
		t.Fatalf("timestamp suggestions = %d, want 1", got)
	}
	for _, suggestion := range suggestions {
		for _, location := range suggestion.Locations {
			if strings.EqualFold(location.Field, "headers.Authorization") {
				t.Errorf("a credential header was offered for generation: %+v", suggestion)
			}
		}
	}
	stamp := byRule[generatedRuleTimestamp][0]
	if stamp.Parameter.Type != "date_time" || stamp.Parameter.Format != "2006-01-02T15:04:05.000Z07:00" || stamp.Parameter.Timezone != "UTC" {
		t.Errorf("timestamp parameter = %+v", stamp.Parameter)
	}
	if hex := byRule[generatedRuleHex][0].Parameter; hex.Type != "random_string" || hex.Length != 16 || hex.Charset != "0123456789abcdef" {
		t.Errorf("hex parameter = %+v", hex)
	}

	selected := map[string]bool{}
	for _, suggestion := range suggestions {
		selected[suggestion.ID] = true
	}
	applied, _, err := ApplySuggestions(script, suggestions, selected)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	action := applied.Actions[0]
	if strings.Contains(action.Body, testMessageID) || strings.Contains(action.Body, "2018-05-04") {
		t.Errorf("body still holds recorded values: %s", action.Body)
	}
	if got := action.HeaderList[1].Value; !strings.HasPrefix(got, "Partner~CNG-CSI~{{var.") || strings.Contains(got, testConversationID) {
		t.Errorf("correlation header = %q, want the prefix kept and the UUID generated", got)
	}
	if got := action.HeaderList[0].Value; strings.Count(got, "{{var.") != 2 || !strings.HasSuffix(got, ":0:1") {
		t.Errorf("trace header = %q, want both IDs generated and the flags kept", got)
	}
	if got := action.HeaderList[2].Value; got != "Bearer "+testCredentialID {
		t.Errorf("credential header = %q, want it untouched", got)
	}
	if len(applied.Parameters) != 5 {
		t.Fatalf("parameters = %+v, want one per generated value", applied.Parameters)
	}
	if issues := dsl.ValidateScriptIssues(applied); len(issues) > 0 {
		t.Fatalf("applied script is invalid: %v", issues)
	}
}

// A value used twice in one request becomes one parameter, so both places
// still send the same value.
func TestGeneratedValueUsedTwiceIsOneParameter(t *testing.T) {
	script := generatedScript(dsl.Action{
		Name: "send", Type: "http_request", Method: "POST", Target: "/messages",
		HeaderList: dsl.HeaderList{{Name: "X-Message-Id", Value: testMessageID}},
		Body:       `{"messageId":"` + testMessageID + `"}`,
	})
	suggestions := suggestGenerated(script)
	if len(suggestions) != 1 || len(suggestions[0].Locations) != 2 {
		t.Fatalf("suggestions = %+v, want one with two locations", suggestions)
	}
	if !strings.Contains(suggestions[0].Rationale, "and 1 other place send") {
		t.Errorf("rationale = %q, want it to count the other place", suggestions[0].Rationale)
	}
}

// Values a target may already know wait for the operator: a session ID, a
// GUID in a field that names no request identifier, a date that may mean
// something. Path segments name resources and are left alone, as are a hex
// run whose field says nothing and anything already templated.
func TestGeneratedValuesThatNeedReview(t *testing.T) {
	script := generatedScript(dsl.Action{
		Name: "update", Type: "http_request", Method: "POST",
		Target: "/accounts/" + testConversationID + "?requestId=" + testMessageID,
		HeaderList: dsl.HeaderList{
			{Name: "idpctx-session-id", Value: "Partner~CNG-CSI~" + testConversationID},
			{Name: "idpctx-conversation-id", Value: "c0ffee00-1234-4abc-8def-0123456789ab"},
			{Name: "X-Template", Value: "{{var.existing}}"},
		},
		Body: `{"accountRef":"0d9a8b7c-6e5f-4a3b-9c2d-1e0f9a8b7c6d","startDate":"2024-01-01T00:00:00Z","hash":"` + testTraceID + testSpanID + `"}`,
	})
	suggestions := suggestGenerated(script)
	auto := map[string]bool{}
	for _, suggestion := range suggestions {
		auto[suggestion.LiteralValue] = suggestion.AutoApply
		for _, location := range suggestion.Locations {
			if location.Field == "target" && suggestion.LiteralValue == testConversationID {
				t.Errorf("a resource path segment was offered for generation")
			}
		}
	}
	if !auto[testMessageID] {
		t.Errorf("requestId query parameter should be selected: %+v", suggestions)
	}
	for value, wantPresent := range map[string]bool{
		testConversationID:                     true, // the session header, for review
		"c0ffee00-1234-4abc-8def-0123456789ab": true, // a conversation the target may hold
		"0d9a8b7c-6e5f-4a3b-9c2d-1e0f9a8b7c6d": true,
		"2024-01-01T00:00:00Z":                 true,
		testTraceID + testSpanID:               false,
	} {
		selectedNow, present := auto[value]
		if present != wantPresent {
			t.Errorf("%s present = %v, want %v", value, present, wantPresent)
		}
		if present && selectedNow {
			t.Errorf("%s arrived selected; it needs review", value)
		}
	}
	for _, suggestion := range suggestions {
		if !suggestion.AutoApply && !strings.Contains(suggestion.Rationale, "Leave it if") {
			t.Errorf("review rationale %q does not say when to leave it", suggestion.Rationale)
		}
	}
}

// A literal holding a generated value stays with the generated pass, so a
// data parameter never claims the same text.
func TestDataParamLeavesValuesHoldingAGeneratedPart(t *testing.T) {
	script := generatedScript(dsl.Action{
		Name: "order", Type: "http_request", Method: "POST", Target: "/orders",
		Body: `{"orderId":"ORD-` + testMessageID + `","customerId":"42"}`,
	})
	result := Suggest(script, Options{EnableGenerated: true, EnableDataParams: true})
	for _, suggestion := range result.Suggestions {
		if suggestion.Kind == KindDataParam && strings.Contains(suggestion.LiteralValue, testMessageID) {
			t.Errorf("data parameter %q claims a generated value", suggestion.VarName)
		}
	}
	if len(generatedOnly(result)) != 1 {
		t.Errorf("generated suggestions = %+v, want the order's UUID", generatedOnly(result))
	}
}

func TestGeneratedTimestampLayoutKeepsTheSourceFormat(t *testing.T) {
	for value, want := range map[string]string{
		"2018-05-04T10:22:33Z":          "2006-01-02T15:04:05Z07:00",
		"2018-05-04T10:22:33.12Z":       "2006-01-02T15:04:05.00Z07:00",
		"2018-05-04T10:22:33.1234567Z":  "2006-01-02T15:04:05.0000000Z07:00",
		"2018-05-04T10:22:33-05:00":     "2006-01-02T15:04:05-07:00",
		"2018-05-04T10:22:33+0530":      "2006-01-02T15:04:05-0700",
		"2018-05-04T10:22:33.000+05:30": "2006-01-02T15:04:05.000-07:00",
		"2018-05-04T10:22:33":           "2006-01-02T15:04:05",
	} {
		if got := generatedTimestampLayout(value); got != want {
			t.Errorf("layout(%q) = %q, want %q", value, got, want)
		}
	}
}

func TestClassifyGeneratedKey(t *testing.T) {
	for key, want := range map[string]generatedKeyRole{
		"idp-trace-id":      {identifier: true},
		"traceparent":       {identifier: true},
		"messageId":         {identifier: true},
		"x-requestid":       {identifier: true},
		"requestedDate":     {},
		"dateTimeStamp":     {timeStamp: true},
		"requestTime":       {identifier: true, timeStamp: true},
		"idpctx-session-id": {session: true},
		"conversationId":    {reference: true},
		"transaction_id":    {reference: true},
		"Idempotency-Key":   {identifier: true},
		"accountRef":        {},
	} {
		if got := classifyGeneratedKey(key); got != want {
			t.Errorf("classify(%q) = %+v, want %+v", key, got, want)
		}
	}
}

// A value glued to a separator still stands alone; one the path also holds,
// or one inside a longer value in the same field, stays as recorded, so a
// replacement can never rewrite part of something else.
func TestGeneratedValueBoundaries(t *testing.T) {
	script := generatedScript(dsl.Action{
		Name: "send", Type: "http_request", Method: "POST",
		Target:     "/orders/" + testMessageID + "?requestId=" + testMessageID + "&traceId=" + testTraceID + testSpanID,
		HeaderList: dsl.HeaderList{{Name: "X-Request-Id", Value: "msg_" + testConversationID}},
		Body:       `{"sentAt":"2018-05-04T10:22:33.123Z","timestamp":"2018-05-04T10:22:33"}`,
	})
	suggestions := suggestGenerated(script)
	values := map[string]bool{}
	for _, suggestion := range suggestions {
		values[suggestion.LiteralValue] = true
	}
	if values[testMessageID] {
		t.Errorf("a query value the path also holds was offered")
	}
	if !values[testConversationID] {
		t.Errorf("msg_<uuid> was not offered: %+v", suggestions)
	}
	if !values["2018-05-04T10:22:33.123Z"] || values["2018-05-04T10:22:33"] {
		t.Errorf("time stamps = %v, want only the one not inside another", values)
	}
	selected := map[string]bool{}
	for _, suggestion := range suggestions {
		selected[suggestion.ID] = true
	}
	if _, _, err := ApplySuggestions(script, suggestions, selected); err != nil {
		t.Fatalf("applying every suggestion: %v", err)
	}
}

// Generation is off unless an importer asks for it.
func TestGeneratedValuesAreOptIn(t *testing.T) {
	script := generatedScript(dsl.Action{
		Name: "send", Type: "http_request", Method: "POST", Target: "/messages",
		Body: `{"messageId":"` + testMessageID + `"}`,
	})
	if got := generatedOnly(Suggest(script, Options{EnableDataParams: true})); len(got) != 0 {
		t.Errorf("suggestions without EnableGenerated = %+v", got)
	}
}
