package aura

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

const request = `{"actions":[{"id":"1;a","descriptor":"apex://Synthetic/ACTION$one","params":{"value":"private-payload"}},{"id":"2;a","descriptor":"apex://Synthetic/ACTION$two"}]}`
const response = `{"actions":[{"id":"2;a","state":"SUCCESS","returnValue":"private-result"},{"id":"1;a","state":"SUCCESS"}]}`

func TestValidateCoverageAndStates(t *testing.T) {
	for _, tc := range []struct {
		name, request, response string
		success, complete       bool
		code                    Code
		matched                 int
	}{
		{"reordered", request, response, true, true, "", 2},
		{"form request", url.Values{"message": {request}, "aura.token": {"private-token"}}.Encode(), response, true, true, "", 2},
		{"error", request, strings.Replace(response, `"SUCCESS"`, `"ERROR"`, 1), false, true, NonSuccessState, 2},
		{"incomplete", request, strings.Replace(response, `"SUCCESS"`, `"INCOMPLETE"`, 1), false, true, NonSuccessState, 2},
		{"aborted", request, strings.Replace(response, `"SUCCESS"`, `"ABORTED"`, 1), false, true, NonSuccessState, 2},
		{"unknown state", request, strings.Replace(response, `"SUCCESS"`, `"private-unknown-state"`, 1), false, false, UnknownState, 2},
		{"missing response", request, `{"actions":[{"id":"1;a","state":"SUCCESS"}]}`, false, false, MissingResponse, 1},
		{"unexpected response", request, strings.Replace(response, `"2;a"`, `"3;a"`, 1), false, false, MissingResponse, 1},
		{"duplicate response ID", request, strings.Replace(response, `"2;a"`, `"1;a"`, 1), false, false, DuplicateID, 0},
		{"duplicate request ID", strings.Replace(request, `"2;a"`, `"1;a"`, 1), response, false, false, DuplicateID, 0},
		{"missing state", request, `{"actions":[{"id":"1;a"}]}`, false, false, MissingState, 0},
		{"missing descriptor", `{"actions":[{"id":"1;a"}]}`, response, false, false, MissingDescriptor, 0},
		{"missing ID", `{"actions":[{"descriptor":"synthetic"}]}`, response, false, false, InvalidID, 0},
		{"numeric ID", `{"actions":[{"id":1,"descriptor":"synthetic"}]}`, response, false, false, InvalidID, 0},
		{"empty ID", `{"actions":[{"id":" ","descriptor":"synthetic"}]}`, response, false, false, InvalidID, 0},
		{"invalid action", `{"actions":[null]}`, response, false, false, InvalidAction, 0},
		{"empty request actions", `{"actions":[]}`, response, false, false, EmptyActions, 0},
		{"empty response actions", request, `{"actions":[]}`, false, false, EmptyActions, 0},
		{"no envelope", request, `[]`, false, false, InvalidEnvelope, 0},
		{"trailing value", request, response + `{}`, false, false, InvalidJSON, 0},
		{"malformed", request, `{"actions":[`, false, false, InvalidJSON, 0},
		{"missing form message", `aura.token=private-token`, response, false, false, MissingMessage, 0},
		{"bad percent escape", `message=%XX`, response, false, false, InvalidForm, 0},
		{"duplicate message", `message=` + url.QueryEscape(request) + `&message=` + url.QueryEscape(request), response, false, false, DuplicateMessage, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := Validate([]byte(tc.request), []byte(tc.response))
			if report.Success != tc.success || report.Complete != tc.complete || !report.HasResponse || report.MatchedActions != tc.matched {
				t.Fatalf("report=%+v", report)
			}
			if tc.code != "" && (len(report.Issues) == 0 || report.Issues[0].Code != tc.code) {
				t.Fatalf("issues=%+v want %s", report.Issues, tc.code)
			}
			if (report.Err() == nil) != tc.success {
				t.Fatalf("Err disagrees with success: %v", report.Err())
			}
		})
	}
	got := Validate([]byte(request), []byte(response))
	if len(got.Outcomes) != 2 || got.Outcomes[0].Ordinal != 1 || got.Outcomes[1].Ordinal != 2 {
		t.Fatalf("outcomes=%+v", got.Outcomes)
	}
	mismatch := Validate([]byte(request), []byte(strings.Replace(response, `"2;a"`, `"3;a"`, 1)))
	if len(mismatch.Issues) != 2 || mismatch.Issues[1].Code != UnexpectedResponse {
		t.Fatalf("missing and unexpected coverage=%+v", mismatch)
	}
}

func TestRequestOnlyAndResponseOnlyCoverage(t *testing.T) {
	req := ValidateRequest([]byte(request))
	if !req.Success || !req.Complete || req.HasResponse || req.RequestActions != 2 || req.ResponseActions != 0 || len(req.Outcomes) != 0 {
		t.Fatalf("request report=%+v", req)
	}
	res := InspectResponse([]byte(strings.Replace(response, `"SUCCESS"`, `"ERROR"`, 1)))
	if res.Success || !res.Complete || !res.HasResponse || res.RequestActions != 0 || res.ResponseActions != 2 || res.MatchedActions != 0 || res.Outcomes[0].State != Error {
		t.Fatalf("response report=%+v", res)
	}
}

func TestDuplicateMembersAndStrictUnicode(t *testing.T) {
	for _, body := range []string{
		`{"actions":[],"actions":[]}`,
		`{"actions":[{"id":"x","id":"y","descriptor":"synthetic"}]}`,
		`{"actions":[{"id":"x","\u0069d":"y","descriptor":"synthetic"}]}`,
		`{"actions":[{"id":"x","descriptor":"synthetic","\u0064escriptor":"other"}]}`,
	} {
		report := ValidateRequest([]byte(body))
		if report.Complete || len(report.Issues) == 0 || report.Issues[0].Code != DuplicateMember {
			t.Fatalf("duplicate accepted: %+v", report)
		}
	}
	// Members the validator does not read cannot change its result, so their
	// duplicates (common in captured component payloads) are accepted.
	if report := ValidateRequest([]byte(`{"actions":[{"id":"x","descriptor":"synthetic","params":{"a":1,"a":2},"params":{}}],"actions_count":1,"actions_count":2}`)); !report.Success {
		t.Fatalf("unread duplicate rejected: %+v", report)
	}
	for _, body := range []string{
		`{"actions":[{"id":"x","state":"SUCCESS","returnValue":{"icon":1,"icon":2}}]}`,
		`{"actions":[{"id":"x","state":"SUCCESS","error":[],"error":[]}],"context":{"loaded":{"a":1,"a":1}},"context":{}}`,
		`{"actions":[{"id":"x","state":"SUCCESS","returnValue":[{"\u0073tate":"ERROR","state":"ERROR"}]}]}`,
	} {
		if report := InspectResponse([]byte(body)); !report.Success || !report.Complete {
			t.Fatalf("unread duplicate rejected: %s %+v", body, report)
		}
	}
	for _, body := range []string{
		`{"actions":[{"id":"x","state":"SUCCESS"}],"actions":[{"id":"x","state":"SUCCESS"}]}`,
		`{"actions":[{"id":"x","state":"SUCCESS","id":"x"}]}`,
		`{"exceptionEvent":false,"exceptionEvent":false,"actions":[{"id":"x","state":"SUCCESS"}]}`,
	} {
		if report := InspectResponse([]byte(body)); report.Complete || len(report.Issues) == 0 || report.Issues[0].Code != DuplicateMember {
			t.Fatalf("read duplicate accepted: %s %+v", body, report)
		}
	}
	for _, body := range []string{
		`{"actions":[{"id":"\ud800","descriptor":"synthetic"}]}`,
		`{"actions":[{"id":"x","descriptor":"\udfff"}]}`,
		`{"actions":[{"id":"x","descriptor":"synthetic","params":{"\ud800":1}}]}`,
		`{"actions":[{"id":"x","descriptor":"synthetic","params":{"value":"\ud800x"}}]}`,
		"{\"actions\":[{\"id\":\"x\",\"descriptor\":\"synthetic\",\"params\":\"\xff\"}]}",
	} {
		report := ValidateRequest([]byte(body))
		if report.Complete || len(report.Issues) == 0 || report.Issues[0].Code != InvalidJSON {
			t.Fatalf("malformed Unicode accepted: %+v", report)
		}
	}
	valid := `{"actions":[{"id":"\ud83d\ude00\ufffd","descriptor":"synthetic"}]}`
	res := `{"actions":[{"id":"😀�","state":"\u0053UCCESS"}]}`
	if report := Validate([]byte(valid), []byte(res)); !report.Success {
		t.Fatalf("valid Unicode identity failed: %+v", report)
	}
	duplicatedState := `{"actions":[{"id":"x","state":"SUCCESS","\u0073tate":"ERROR"}]}`
	if report := InspectResponse([]byte(duplicatedState)); report.Complete || report.Issues[0].Code != DuplicateMember {
		t.Fatalf("duplicate state accepted: %+v", report)
	}
}

func TestLimits(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		code       Code
	}{
		{"bytes", strings.Repeat(" ", MaxEnvelopeBytes+1), ByteLimit},
		{"depth", `{"actions":[{"id":"x","descriptor":"synthetic","params":` + strings.Repeat("[", MaxDepth) + `0` + strings.Repeat("]", MaxDepth) + `}]}`, DepthLimit},
		{"tokens", `{"actions":[{"id":"x","descriptor":"synthetic","params":[` + strings.Repeat("0,", MaxTokens) + `0]}]}`, WorkLimit},
		{"actions", `{"actions":[` + strings.TrimSuffix(strings.Repeat(`{"id":"x","descriptor":"synthetic"},`, MaxActions+1), ",") + `]}`, ActionLimit},
		{"form fields", `message=` + url.QueryEscape(request) + strings.Repeat("&a=", MaxFormFields), WorkLimit},
		{"identity bytes", `{"actions":[{"id":"` + strings.Repeat("x", MaxIDBytes+1) + `","descriptor":"synthetic"}]}`, InvalidID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := ValidateRequest([]byte(tc.body))
			if report.Complete || report.Success || len(report.Issues) == 0 || report.Issues[0].Code != tc.code {
				t.Fatalf("report=%+v want %s", report, tc.code)
			}
		})
	}
	var items []string
	for i := 0; i < MaxActions; i++ {
		items = append(items, fmt.Sprintf(`{"id":"%d","descriptor":"synthetic"}`, i))
	}
	if report := ValidateRequest([]byte(`{"actions":[` + strings.Join(items, ",") + `]}`)); !report.Success || report.RequestActions != MaxActions {
		t.Fatalf("action boundary rejected: %+v", report)
	}
	padded := request + strings.Repeat(" ", MaxEnvelopeBytes-len(request))
	if report := ValidateRequest([]byte(padded)); !report.Success {
		t.Fatalf("byte boundary rejected: %+v", report)
	}
	form := `message=` + url.QueryEscape(request) + strings.Repeat("&a=", MaxFormFields-1)
	if report := ValidateRequest([]byte(form)); !report.Success {
		t.Fatalf("form boundary rejected: %+v", report)
	}
}

func TestReportsAndErrorsNeverDiscloseSourceValues(t *testing.T) {
	req := strings.ReplaceAll(request, "1;a", "secret-identity")
	res := strings.ReplaceAll(strings.Replace(response, `"SUCCESS"`, `"secret-state"`, 1), "1;a", "secret-identity")
	for _, report := range []Report{ValidateRequest([]byte(req)), InspectResponse([]byte(res)), Validate([]byte(req), []byte(res))} {
		encoded, err := json.Marshal(report)
		if err != nil {
			t.Fatal(err)
		}
		public := string(encoded) + fmt.Sprint(report.Err())
		for _, secret := range []string{"secret-identity", "secret-state", "private-payload", "private-result", "apex://", "descriptor", "returnValue"} {
			if strings.Contains(public, secret) {
				t.Fatalf("report disclosed source value %q", secret)
			}
		}
	}
}

func TestParseBaseline(t *testing.T) {
	if recorded, err := ParseBaseline(nil); recorded != nil || err != nil {
		t.Fatalf("nil baseline = %v, %v", recorded, err)
	}
	typed := Baseline{Recorded: []RecordedOutcome{{Ordinal: 2, State: Error}, {Ordinal: 3, State: Aborted}}}
	var decoded any
	if err := json.Unmarshal([]byte(`{"recorded":[{"ordinal":2,"state":"ERROR"},{"ordinal":3,"state":"ABORTED"}]}`), &decoded); err != nil {
		t.Fatal(err)
	}
	for _, form := range []any{typed, &typed, decoded, map[string][]RecordedOutcome{"recorded": typed.Recorded}} {
		recorded, err := ParseBaseline(form)
		if err != nil || len(recorded) != 2 || recorded[2] != Error || recorded[3] != Aborted {
			t.Fatalf("ParseBaseline(%#v) = %v, %v", form, recorded, err)
		}
	}
	for _, invalid := range []string{
		`"SUCCESS"`, `{}`, `{"recorded":[]}`, `{"recorded":[{"ordinal":0,"state":"ERROR"}]}`,
		`{"recorded":[{"ordinal":1,"state":"SUCCESS"}]}`, `{"recorded":[{"ordinal":1,"state":"ERROR"},{"ordinal":1,"state":"ERROR"}]}`,
		`{"recorded":[{"ordinal":1,"state":"ERROR"}],"extra":true}`, `{"recorded":[{"ordinal":1.5,"state":"ERROR"}]}`,
		`{"recorded":[{"ordinal":1,"state":"ERROR","note":"x"}]}`, `{"recorded":[{"ordinal":"1","state":"ERROR"}]}`,
	} {
		var value any
		if err := json.Unmarshal([]byte(invalid), &value); err != nil {
			t.Fatal(err)
		}
		if _, err := ParseBaseline(value); err == nil {
			t.Errorf("ParseBaseline(%s) accepted", invalid)
		}
	}
}

// An action that failed in the recording fails again labelled as such; with
// accept it passes when it ends in the same state, but a new failure, a
// different failure state and structural problems are never excused.
func TestApplyRecordedBaseline(t *testing.T) {
	failedSecond := strings.Replace(response, `"2;a","state":"SUCCESS"`, `"2;a","state":"ERROR"`, 1)
	recorded := map[int]State{2: Error}

	report := Validate([]byte(request), []byte(failedSecond))
	report.ApplyRecordedBaseline(recorded, false)
	if report.Success || len(report.Issues) != 1 || !report.Issues[0].RecordedFailure || report.Outcomes[1].Recorded != Error ||
		!strings.Contains(report.Err().Error(), "non_success_state at action 2 (also failed in the recording)") {
		t.Fatalf("require success: %+v err=%v", report, report.Err())
	}

	report = Validate([]byte(request), []byte(failedSecond))
	report.ApplyRecordedBaseline(recorded, true)
	if !report.Success || report.AcceptedRecordedFailures != 1 || len(report.Issues) != 0 {
		t.Fatalf("accept recorded: %+v", report)
	}

	for name, tc := range map[string]struct {
		response string
		recorded map[int]State
	}{
		"new failure":         {strings.Replace(response, `"1;a","state":"SUCCESS"`, `"1;a","state":"ERROR"`, 1), recorded},
		"other failure state": {strings.Replace(response, `"2;a","state":"SUCCESS"`, `"2;a","state":"INCOMPLETE"`, 1), recorded},
		"missing response":    {`{"actions":[{"id":"1;a","state":"SUCCESS"}]}`, recorded},
	} {
		report := Validate([]byte(request), []byte(tc.response))
		report.ApplyRecordedBaseline(tc.recorded, true)
		if report.Success || report.AcceptedRecordedFailures != 0 {
			t.Errorf("%s accepted: %+v", name, report)
		}
	}

	recovered := Validate([]byte(request), []byte(response))
	recovered.ApplyRecordedBaseline(recorded, false)
	if !recovered.Success {
		t.Fatalf("a replay that succeeds where the recording failed must pass: %+v", recovered)
	}
}

// The Aura client unwraps a response ending in /*ERROR*/ (the JSON between the
// first */ and the last /*) and treats an exception event as a framework
// error; it reads a non-200 exception body the same way without the wrapper.
func TestFrameworkErrorResponses(t *testing.T) {
	event := func(descriptor string) string {
		return `{"exceptionEvent":true,"event":{"descriptor":"` + descriptor + `","attributes":{"values":{"newToken":"private-token"}}},"message":"private-message"}`
	}
	for name, tc := range map[string]struct {
		response string
		want     Code
	}{
		"invalid session":         {"*/" + event("markup://aura:invalidSession") + "/*ERROR*/", InvalidSession},
		"client out of sync":      {"*/" + event("markup://aura:clientOutOfSync") + "/*ERROR*/\n", ClientOutOfSync},
		"other event":             {"*/" + event("markup://c:privateEvent") + "/*ERROR*/", FrameworkError},
		"exception without event": {`*/{"exceptionEvent":true,"message":"private-message"}/*ERROR*/`, FrameworkError},
		"not an exception event":  {`*/{"message":"private-message"}/*ERROR*/`, FrameworkError},
		"malformed JSON":          {"*/{not json/*ERROR*/", FrameworkError},
		"no opening marker":       {`{"exceptionEvent":true}/*ERROR*/`, FrameworkError},
		"unwrapped exception":     {event("markup://aura:invalidSession"), InvalidSession},
		"unread duplicate":        {`*/{"exceptionEvent":true,"event":{"descriptor":"markup://aura:invalidSession","attributes":{},"attributes":{}},"message":"private-message"}/*ERROR*/`, InvalidSession},
		"duplicate descriptor":    {`*/{"exceptionEvent":true,"event":{"descriptor":"markup://aura:invalidSession","descriptor":"markup://aura:clientOutOfSync"}}/*ERROR*/`, FrameworkError},
	} {
		report := Validate([]byte(request), []byte(tc.response))
		if !report.Complete || report.Success || len(report.Issues) != 1 || report.Issues[0].Code != tc.want || len(report.Outcomes) != 0 {
			t.Errorf("%s: report = %+v, want complete failure %s", name, report, tc.want)
			continue
		}
		if got := report.Err().Error(); got != "Aura validation failed: "+string(tc.want) {
			t.Errorf("%s: error = %q", name, got)
		}
		encoded, _ := json.Marshal(report)
		for _, private := range []string{"private-token", "private-message", "privateEvent", "invalidSession", "clientOutOfSync"} {
			if strings.Contains(string(encoded)+report.Err().Error(), private) {
				t.Errorf("%s: report discloses %q: %s", name, private, encoded)
			}
		}
		if inspected := InspectResponse([]byte(tc.response)); !inspected.Complete || inspected.Issues[0].Code != tc.want {
			t.Errorf("%s: InspectResponse = %+v", name, inspected)
		}
	}

	// A successful envelope that merely contains the marker is not an error.
	marker := `{"actions":[{"id":"1;a","state":"SUCCESS","returnValue":"/*ERROR*/"},{"id":"2;a","state":"SUCCESS"}]}`
	if report := Validate([]byte(request), []byte(marker)); !report.Success {
		t.Fatalf("marker inside a value treated as an error: %+v", report)
	}
}

// An Aura request's actions are named by descriptor, and an Apex call by its
// namespace, class and method; nothing the actions send is kept.
func TestRequestActionsNamesWithoutParameters(t *testing.T) {
	message := `{"actions":[` +
		`{"id":"1;a","descriptor":"aura://ApexActionController/ACTION$execute","params":{"namespace":"","classname":"AccountSearch","method":"find","params":{"term":"secret-term-123"},"cacheable":false}},` +
		`{"id":"2;a","descriptor":"aura://ApexActionController/ACTION$execute","params":{"namespace":"acme","classname":"Orders","method":"submit","params":{"id":"secret-order"}}},` +
		`{"id":"3;a","descriptor":"serviceComponent://ui.force.components.controllers.recordGlobalValueProvider.RecordGvpController/ACTION$getRecord","params":{"recordDescriptor":"secret-record"}}` +
		`]}`
	body := "message=" + url.QueryEscape(message) + "&aura.token=secret-token"
	actions, err := RequestActions([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"AccountSearch.find", "acme.Orders.submit", "serviceComponent://ui.force.components.controllers.recordGlobalValueProvider.RecordGvpController/ACTION$getRecord"}
	if len(actions) != len(want) {
		t.Fatalf("actions = %+v", actions)
	}
	for i, action := range actions {
		if action.Name() != want[i] {
			t.Fatalf("action %d name = %q, want %q", i, action.Name(), want[i])
		}
	}
	encoded := fmt.Sprintf("%+v", actions)
	for _, secret := range []string{"secret-term-123", "secret-order", "secret-record", "secret-token"} {
		if strings.Contains(encoded, secret) {
			t.Fatalf("a parameter value was kept: %s", encoded)
		}
	}
	if _, err := RequestActions([]byte("message=%7Bnot-json")); err == nil {
		t.Fatal("a malformed request was named")
	}
}
