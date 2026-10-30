package paramize

import (
	"fmt"
	"reflect"
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

// A request ID recorded once per request becomes one parameter that is new
// each time it is sent, not one parameter per request; an ID two requests
// share keeps its own parameter, so they still send the same value.
func TestGeneratedValuesShareOneEachUseParameterAcrossRequests(t *testing.T) {
	ids := []string{
		"0a1b2c3d-1111-4aaa-8bbb-000000000001",
		"0a1b2c3d-2222-4aaa-8bbb-000000000002",
		"0a1b2c3d-3333-4aaa-8bbb-000000000003",
	}
	var actions []dsl.Action
	for index, id := range ids {
		actions = append(actions, dsl.Action{
			Name: "call " + string(rune('a'+index)), Type: "http_request", Method: "GET", Target: "/orders",
			HeaderList: dsl.HeaderList{
				{Name: "X-Request-Id", Value: id},
				{Name: "X-Conversation-Id", Value: testConversationID},
				{Name: "idp-trace-id", Value: fmt.Sprintf("4bf92f3577b34da%d:a3ce929d0e0e473%d:0:1", index, index)},
			},
		})
	}
	script := generatedScript(actions...)
	suggestions := suggestGenerated(script)
	var perRequest, shared *Suggestion
	var trace []Suggestion
	for index := range suggestions {
		switch suggestions[index].Locations[0].Field {
		case "headers.idp-trace-id":
			trace = append(trace, suggestions[index])
		case "headers.X-Request-Id":
			if perRequest != nil {
				t.Fatalf("request IDs became more than one suggestion: %+v", suggestions)
			}
			perRequest = &suggestions[index]
		case "headers.X-Conversation-Id":
			shared = &suggestions[index]
		}
	}
	if perRequest == nil || shared == nil {
		t.Fatalf("suggestions = %+v", suggestions)
	}
	if len(perRequest.Locations) != 3 || perRequest.Parameter.Update != "each_occurrence" {
		t.Fatalf("request ID suggestion = %+v, want 3 places and a parameter new each time", perRequest)
	}
	for index, location := range perRequest.Locations {
		if location.SearchText != ids[index] || location.ReplaceText != perRequest.Replacement {
			t.Errorf("location %d = %+v", index, location)
		}
	}
	if strings.Contains(perRequest.Rationale, ids[0]) || !strings.Contains(perRequest.Rationale, "each of 3 places") {
		t.Errorf("rationale = %q", perRequest.Rationale)
	}
	if len(shared.Locations) != 3 || shared.Parameter.Update != "each_iteration" {
		t.Fatalf("shared ID suggestion = %+v, want its own parameter, new each iteration", shared)
	}
	// The trace header's trace IDs share one parameter and its span IDs another.
	if len(trace) != 2 {
		t.Fatalf("trace header suggestions = %+v, want one for the trace IDs and one for the span IDs", trace)
	}
	for _, suggestion := range trace {
		if len(suggestion.Locations) != 3 || suggestion.Parameter.Update != "each_occurrence" {
			t.Errorf("trace header suggestion = %+v, want 3 places, new each time", suggestion)
		}
	}
	if trace[0].Locations[0].SearchText != "4bf92f3577b34da0" || trace[1].Locations[0].SearchText != "a3ce929d0e0e4730" {
		t.Errorf("trace header groups = %q / %q, want the trace IDs first, then the span IDs",
			trace[0].Locations[0].SearchText, trace[1].Locations[0].SearchText)
	}

	applied, diffs, err := ApplySuggestions(script, suggestions, map[string]bool{perRequest.ID: true, shared.ID: true})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(applied.Parameters) != 2 {
		t.Fatalf("parameters = %+v, want one per suggestion", applied.Parameters)
	}
	for index, action := range applied.Actions {
		if got := action.HeaderList[0].Value; got != perRequest.Replacement {
			t.Errorf("request %d X-Request-Id = %q", index, got)
		}
	}
	found := false
	for _, diff := range diffs {
		if diff.Kind == "parameter_added" && strings.Contains(diff.After, "new each time it is sent") {
			found = true
		}
	}
	if !found {
		t.Errorf("diffs = %+v, want the each-use parameter described", diffs)
	}
}

// A suggestion never takes the name of a variable the script reads, even one
// nothing defines (an input the source left unset): it would supply that
// input with its own value.
func TestGeneratedValuesAvoidAVariableTheScriptReads(t *testing.T) {
	script := generatedScript(dsl.Action{
		Name: "one", Type: "http_request", Method: "POST", Target: "/orders",
		HeaderList: dsl.HeaderList{{Name: "X-Trace", Value: "{{var.trace_id}}"}},
		Body:       `{"trace_id": "` + testMessageID + `"}`,
	})
	suggestions := suggestGenerated(script)
	if len(suggestions) != 1 {
		t.Fatalf("suggestions = %+v", suggestions)
	}
	if suggestions[0].VarName == "trace_id" {
		t.Fatalf("the generated value took trace_id, the name of an input the script reads")
	}
	if suggestions[0].VarName != "trace_id_2" {
		t.Errorf("var name = %q, want trace_id_2", suggestions[0].VarName)
	}
}

func TestGeneratedValuesCountReplacementsWithinOneField(t *testing.T) {
	for _, tc := range []struct {
		name       string
		set        func(*dsl.Action, string)
		parameters int
		update     string
	}{
		{name: "body", set: func(a *dsl.Action, id string) { a.Body = fmt.Sprintf(`{"requestId":%q,"parentRequestId":%q}`, id, id) }, parameters: 2, update: "each_iteration"},
		{name: "header", set: func(a *dsl.Action, id string) {
			a.HeaderList = dsl.HeaderList{{Name: "X-Request-Id", Value: id + ":" + id}}
		}, parameters: 2, update: "each_iteration"},
		{name: "duplicate ordered headers", set: func(a *dsl.Action, id string) {
			a.HeaderList = dsl.HeaderList{{Name: "X-Request-Id", Value: id}, {Name: "X-Request-Id", Value: id}}
			a.Headers = map[string]string{"X-Request-Id": id}
		}, parameters: 2, update: "each_iteration"},
		{name: "same header name different text and casing", set: func(a *dsl.Action, id string) {
			a.HeaderList = dsl.HeaderList{{Name: "X-Request-Id", Value: id}, {Name: "x-request-id", Value: "prefix" + id}}
		}, parameters: 2, update: "each_iteration"},
		{name: "compatibility header map is not another occurrence", set: func(a *dsl.Action, id string) {
			a.HeaderList = dsl.HeaderList{{Name: "X-Request-Id", Value: id}}
			a.Headers = map[string]string{"x-request-id": id}
		}, parameters: 1, update: "each_occurrence"},
		{name: "query", set: func(a *dsl.Action, id string) { a.Target = "/requests?requestId=" + id + "&parentRequestId=" + id }, parameters: 2, update: "each_iteration"},
		{name: "embedded second copy", set: func(a *dsl.Action, id string) {
			a.Body = fmt.Sprintf(`{"requestId":%q,"messageId":"prefix%s"}`, id, id)
		}, parameters: 2, update: "each_iteration"},
		{name: "template is not replaced", set: func(a *dsl.Action, id string) {
			a.Body = fmt.Sprintf(`{"requestId":%q,"template":"{{var.%s}}"}`, id, id)
		}, parameters: 1, update: "each_occurrence"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var actions []dsl.Action
			for i, id := range []string{testMessageID, testConversationID} {
				action := dsl.Action{Name: fmt.Sprint(i), Type: "http_request", Method: "POST", Target: "/requests"}
				tc.set(&action, id)
				actions = append(actions, action)
			}
			suggestions := suggestGenerated(generatedScript(actions...))
			if len(suggestions) != tc.parameters {
				t.Fatalf("suggestions=%+v, want %d parameters", suggestions, tc.parameters)
			}
			for _, suggestion := range suggestions {
				if suggestion.Parameter.Update != tc.update {
					t.Fatalf("update=%s, want %s", suggestion.Parameter.Update, tc.update)
				}
			}
		})
	}
}

func TestGeneratedValueConfidenceIncludesEveryOccurrenceInAField(t *testing.T) {
	script := generatedScript(dsl.Action{Name: "send", Type: "http_request", Method: "POST", Target: "/requests", Body: fmt.Sprintf(`{"requestId":%q,"accountRef":%q}`, testMessageID, testMessageID)})
	suggestions := suggestGenerated(script)
	if len(suggestions) != 1 || suggestions[0].AutoApply || suggestions[0].Parameter.Update != "each_iteration" {
		t.Fatalf("the second occurrence's business role was ignored: %+v", suggestions)
	}
}

func TestGeneratedHeaderReplacementTextsIndex(t *testing.T) {
	for _, tc := range []struct {
		name   string
		action dsl.Action
		want   map[string][]string
	}{
		{name: "empty", want: map[string][]string{}},
		{name: "ordered duplicates and compatibility mirror", action: dsl.Action{
			HeaderList: dsl.HeaderList{{Name: "X-Request-Id", Value: "a"}, {Name: "x-request-id", Value: "a"}, {Name: "X-Request-Id", Value: "prefix-a"}},
			Headers:    map[string]string{"X-Request-Id": "mirror", "X-Other": "fallback"},
		}, want: map[string][]string{"x-request-id": {"a", "a", "prefix-a"}, "x-other": {"fallback"}}},
		{name: "map casing variants", action: dsl.Action{Headers: map[string]string{"X-Request-Id": "a", "x-request-id": "b"}}, want: map[string][]string{"x-request-id": {"a", "b"}}},
		{name: "empty ordered header still shadows map", action: dsl.Action{
			HeaderList: dsl.HeaderList{{Name: "X-Request-Id", Value: ""}}, Headers: map[string]string{"x-request-id": "mirror"},
		}, want: map[string][]string{"x-request-id": {""}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := generatedHeaderReplacementTexts(&tc.action); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("texts=%v, want %v", got, tc.want)
			}
		})
	}
}

// Lightning sends a random page-cache id in X-SFDC-Page-Cache, one per page
// load and repeated by every request of that page. Each recorded page's id
// becomes its own parameter generated every iteration, so a replayed page
// gets a fresh id shared by its requests, as a browser's page load does.
func TestGeneratedPageCacheIDIsOneParameterPerPage(t *testing.T) {
	const firstPage, secondPage = "a1b2c3d4e5f60718", "0f1e2d3c4b5a6978"
	request := func(name, pageCache string) dsl.Action {
		return dsl.Action{Name: name, Type: "http_request", Method: "POST", Target: "/aura",
			HeaderList: dsl.HeaderList{{Name: "X-SFDC-Page-Cache", Value: pageCache}}}
	}
	script := generatedScript(request("a", firstPage), request("b", firstPage), request("c", secondPage), request("d", secondPage))
	suggestions := suggestGenerated(script)
	if len(suggestions) != 2 {
		t.Fatalf("suggestions = %+v, want one per page", suggestions)
	}
	for _, suggestion := range suggestions {
		if !suggestion.AutoApply || len(suggestion.Locations) != 2 || suggestion.Parameter == nil || suggestion.Parameter.Update != "each_iteration" {
			t.Fatalf("suggestion = %+v, want an automatic parameter generated each iteration for both requests of its page", suggestion)
		}
	}
}

// Requests a HAR import replays as a concurrent request group keep their
// generated values: the suggestion locates each one by its index in the
// group, and applying it rewrites that request.
func TestGeneratedValuesInsideAConcurrentRequestGroup(t *testing.T) {
	child := func(name, traceID string) dsl.Action {
		return dsl.Action{Name: name, Type: "request", Method: "GET", Target: "/data",
			HeaderList: dsl.HeaderList{{Name: "X-B3-TraceId", Value: traceID}}}
	}
	group := dsl.Action{Name: "group", Type: "http_sequence", Method: "SEQUENCE", Target: "/data",
		Params: map[string]any{"execution_mode": dsl.HTTPSequenceConcurrent, "requests": []dsl.Action{child("a", testTraceID), child("b", testSpanID)}}}
	script := generatedScript(group)
	suggestions := suggestGenerated(script)
	if len(suggestions) != 1 || len(suggestions[0].Locations) != 2 {
		t.Fatalf("suggestions = %+v, want one each-use parameter for both group requests", suggestions)
	}
	for index, location := range suggestions[0].Locations {
		if location.RequestIndex == nil || *location.RequestIndex != index || location.ActionIndex != 0 {
			t.Fatalf("location %d = %+v, want group request %d", index, location, index)
		}
	}
	applied, diffs, err := ApplySuggestions(script, suggestions, map[string]bool{suggestions[0].ID: true})
	if err != nil {
		t.Fatal(err)
	}
	// The apply clone decodes the group's requests, so they come back typed.
	children := applied.Actions[0].Params["requests"].([]dsl.Action)
	for _, request := range children {
		if value := request.HeaderList[0].Value; !strings.HasPrefix(value, "{{var.") {
			t.Fatalf("group request %q header = %q, want the parameter (diffs %+v)", request.Name, value, diffs)
		}
	}
	if len(diffs) != 3 || !strings.Contains(diffs[0].Path, "/params/requests/0/") || !strings.Contains(diffs[1].Path, "/params/requests/1/") {
		t.Fatalf("diffs = %+v, want a replacement per group request with its path, then the parameter", diffs)
	}
}
