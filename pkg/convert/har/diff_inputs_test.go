package har

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/convert/sample"
	"github.com/jasonczech/apexload/pkg/dsl"
)

type inputEntry struct {
	method, url, body, responseBody string
	headers                         map[string]string
}

func inputHAR(t *testing.T, entries ...inputEntry) []byte {
	t.Helper()
	items := make([]any, 0, len(entries))
	for _, entry := range entries {
		headers := []map[string]string{}
		for name, value := range entry.headers {
			headers = append(headers, map[string]string{"name": name, "value": value})
		}
		request := map[string]any{"method": entry.method, "url": entry.url, "headers": headers}
		if entry.body != "" {
			headers = append(headers, map[string]string{"name": "Content-Type", "value": "application/x-www-form-urlencoded"})
			request["headers"] = headers
			request["postData"] = map[string]any{"mimeType": "application/x-www-form-urlencoded", "text": entry.body}
		}
		items = append(items, map[string]any{
			"startedDateTime": "2026-01-01T00:00:00Z",
			"time":            10,
			"request":         request,
			"response": map[string]any{
				"status":  200,
				"headers": []map[string]string{{"name": "Content-Type", "value": "text/html"}},
				"content": map[string]any{"mimeType": "text/html", "text": entry.responseBody},
			},
		})
	}
	raw, err := json.Marshal(map[string]any{"log": map[string]any{"entries": items}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// inputRecording is one recording of a flow whose requests send a per-page
// id, a counter, a client timestamp, a value with no captured origin, a value
// an earlier response returns, a constant, and two requests the recordings
// issue in different orders.
func inputRecording(t *testing.T, run int) []byte {
	pageID := []string{"aaaa1111bbbb2222", "cccc3333dddd4444"}[run]
	counter := []string{"12;a", "15;a"}[run]
	timestamp := []string{"1761800000123", "1761800999456"}[run]
	mystery := []string{"Zq9vT0kenAAA", "Pp8uU1lfoBBB"}[run]
	server := []string{"srvvalue-one-77", "srvvalue-two-88"}[run]
	items := []string{"alpha-one", "beta-two"}
	if run == 1 {
		items[0], items[1] = items[1], items[0]
	}
	return inputHAR(t,
		inputEntry{method: "GET", url: "https://app.test/home", responseBody: "<p>current value " + server + " here</p>"},
		inputEntry{method: "POST", url: "https://app.test/api/save?op=save", headers: map[string]string{"X-Page-Id": pageID},
			body: "seq=" + counter + "&ts=" + timestamp + "&mystery=" + mystery + "&echo=" + server + "&static=constantvalue", responseBody: "ok"},
		inputEntry{method: "POST", url: "https://app.test/api/batch?op=list", body: "item=" + items[0], responseBody: "ok"},
		inputEntry{method: "POST", url: "https://app.test/api/batch?op=list", body: "item=" + items[1], responseBody: "ok"},
	)
}

func unhandledByField(report *DiffReport) map[string]DiffRequestInput {
	out := map[string]DiffRequestInput{}
	for _, input := range report.UnhandledInputs {
		out[input.Field] = input
	}
	return out
}

// The pair is compared request by request, not only correlation by
// correlation: every request value that changed between the recordings and
// that the script still sends as recorded is listed with what it most likely
// is. Values that are stable but were sent in another order are not.
func TestDiffListsRequestInputsTheScriptStillSendsAsRecorded(t *testing.T) {
	baseline, comparison := inputRecording(t, 0), inputRecording(t, 1)
	opts := ConvertOptions{ScriptName: "inputs"}
	result, err := Convert(baseline, opts)
	if err != nil {
		t.Fatal(err)
	}
	report, err := DiffWithBaseline(result, comparison, opts)
	if err != nil {
		t.Fatal(err)
	}
	report.ResolveUnhandledInputs(func(value string) bool { return ScriptRequestValues(result.Script)[value] })
	got := unhandledByField(report)
	for field, kind := range map[string]string{
		"header:x-page-id": DiffInputGeneratedID,
		"form:seq":         DiffInputCounter,
		"form:ts":          DiffInputTimestamp,
		"form:mystery":     DiffInputUnknown,
		"form:echo":        DiffInputServerValue,
	} {
		input, ok := got[field]
		if !ok {
			t.Errorf("unhandled inputs = %#v, want %s", report.UnhandledInputs, field)
			continue
		}
		if input.Kind != kind || input.Requests != 1 || input.DistinctValues != 1 || len(input.BaselineEntries) != 1 || input.BaselineEntries[0] != 2 || input.Reason == "" {
			t.Errorf("%s = %#v, want kind %s in baseline entry 2", field, input, kind)
		}
	}
	for _, field := range []string{"form:static", "form:item"} {
		if _, ok := got[field]; ok {
			t.Errorf("%s listed although its values are the same in both recordings", field)
		}
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "Zq9vT0kenAAA") || strings.Contains(string(raw), "aaaa1111bbbb2222") {
		t.Fatalf("report discloses recorded values: %s", raw)
	}
}

// A value the final script no longer sends as recorded (correlated,
// parameterized or replaced) is not unhandled.
func TestDiffInputsTheScriptReplacedAreNotUnhandled(t *testing.T) {
	baseline, comparison := inputRecording(t, 0), inputRecording(t, 1)
	opts := ConvertOptions{ScriptName: "inputs"}
	result, err := Convert(baseline, opts)
	if err != nil {
		t.Fatal(err)
	}
	report, err := DiffWithBaseline(result, comparison, opts)
	if err != nil {
		t.Fatal(err)
	}
	for index := range result.Script.Actions {
		action := &result.Script.Actions[index]
		action.Body = strings.Replace(action.Body, "mystery=Zq9vT0kenAAA", "mystery={{var.mystery}}", 1)
	}
	report.ResolveUnhandledInputs(func(value string) bool { return ScriptRequestValues(result.Script)[value] })
	if _, ok := unhandledByField(report)["form:mystery"]; ok {
		t.Fatalf("unhandled inputs = %#v, want the templated field left out", report.UnhandledInputs)
	}
	if _, ok := unhandledByField(report)["form:seq"]; !ok {
		t.Fatalf("unhandled inputs = %#v, want the untouched fields kept", report.UnhandledInputs)
	}
}

func TestScriptRequestValuesReadsEveryRequestSurface(t *testing.T) {
	disabled := false
	script := &dsl.Script{Actions: []dsl.Action{{
		Name: "One", Type: "POST", Method: "POST",
		Target:     "/api?q=hello%20there&tpl={{var.x}}",
		Headers:    map[string]string{"X-Id": "header-value"},
		HeaderList: dsl.HeaderList{{Name: "X-Off", Value: "disabled-value", Enabled: &disabled}},
		Body:       "message=" + `%7B%22actions%22%3A%5B%7B%22id%22%3A%2212%3Ba%22%7D%5D%7D` + "&plain=form-value",
	}}}
	values := ScriptRequestValues(script)
	for _, want := range []string{"hello there", "{{var.x}}", "header-value", "12;a", "form-value"} {
		if !values[want] {
			t.Errorf("values = %v, want %q", values, want)
		}
	}
	if values["disabled-value"] {
		t.Errorf("a disabled header's value was read as sent")
	}
}

func jsonInputObservation(id int, body string) sample.HARSourceObservation {
	return sample.HARSourceObservation{Method: "POST", Observation: correlate.Observation{
		ID: id, RequestURL: "https://app.test/api/batch", RequestBody: body,
		RequestHeaders: map[string]string{"Content-Type": "application/json"},
	}}
}

// JSON bodies are compared leaf by leaf with numbers kept as written (19-digit
// ids that float64 would merge stay apart), identifier-shaped object keys do
// not reach a field name, and a request that sends a changed field in three
// array items counts once.
func TestDiffRequestInputsReadJSONBodies(t *testing.T) {
	baseline := []sample.HARSourceObservation{jsonInputObservation(1,
		`{"big":1234567890123456789,"records":{"001A000001abcdeQAA":{"name":"same"}},"actions":[{"id":"Zq9vT0kenAAA"},{"id":"Zq9vT0kenBBB"},{"id":"Zq9vT0kenCCC"}]}`)}
	comparison := []sample.HARSourceObservation{jsonInputObservation(1,
		`{"big":1234567890123456788,"records":{"001A000009zyxwvQAA":{"name":"same"}},"actions":[{"id":"Pp8uU1lfoAAA"},{"id":"Pp8uU1lfoBBB"},{"id":"Pp8uU1lfoCCC"}]}`)}
	inputs, _, err := diffRequestInputs(context.Background(), baseline, comparison)
	if err != nil {
		t.Fatal(err)
	}
	byField := map[string]DiffRequestInput{}
	for _, input := range inputs {
		byField[input.Field] = input
		if strings.Contains(input.Field, "001A000001") {
			t.Fatalf("field %q carries a record id", input.Field)
		}
	}
	if _, ok := byField["body.big"]; !ok {
		t.Fatalf("inputs = %#v, want the 19-digit id that changed in its last digit", inputs)
	}
	if ids := byField["body.actions[].id"]; ids.Requests != 1 || ids.DistinctValues != 3 {
		t.Fatalf("body.actions[].id = %#v, want one request with three values", ids)
	}
	if _, ok := byField["body.records.*.name"]; ok {
		t.Fatalf("an unchanged leaf under a masked key was listed: %#v", inputs)
	}
}

// The response search is bounded: past its budget, or for a short value, a
// value is reported as not checked rather than as having no captured origin.
func TestDiffResponseScanSaysWhenItCouldNotLook(t *testing.T) {
	observations := []sample.HARSourceObservation{
		{Observation: correlate.Observation{ID: 1, ResponseBody: "the value server-sent-token here"}},
		{Observation: correlate.Observation{ID: 2}},
	}
	scan := &diffResponseScan{observations: observations}
	if found, checked := scan.before(1, "server-sent-token"); !found || !checked {
		t.Fatalf("before = %v, %v, want found", found, checked)
	}
	if found, checked := scan.before(1, "missing-token-value"); found || !checked {
		t.Fatalf("missing value = %v, %v, want checked and not found", found, checked)
	}
	if _, checked := scan.before(1, "short"); checked {
		t.Fatal("a short value was searched for")
	}
	// A token-shaped value is answered by the token index, which costs no
	// search budget; a value with delimiters is searched for, within budget.
	scan.remaining = 0
	if found, checked := scan.before(1, "another-missing-value"); found || !checked {
		t.Fatalf("indexed lookup past the budget = %v, %v, want checked and not found", found, checked)
	}
	if found, checked := scan.before(1, "another missing value"); found || checked {
		t.Fatalf("search past the budget = %v, %v, want not checked", found, checked)
	}
	if kind := diffInputKind("another missing value", "other missing value!!", scan, 1); kind != DiffInputNotChecked {
		t.Fatalf("kind = %q, want %q", kind, DiffInputNotChecked)
	}
}

func TestDiffRequestInputsStopWithTheImport(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	baseline := []sample.HARSourceObservation{jsonInputObservation(1, `{"a":"Zq9vT0kenAAA"}`)}
	if _, _, err := diffRequestInputs(ctx, baseline, baseline); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestDiffUnhandledInputsAreBounded(t *testing.T) {
	report := &DiffReport{}
	for index := 0; index < maxDiffUnhandledInputs+5; index++ {
		report.requestInputs = append(report.requestInputs, DiffRequestInput{
			Field:  "form:f" + strconv.Itoa(index),
			values: map[string]*diffInputValue{"value-" + strconv.Itoa(index): {requests: []int{index + 1}, kind: DiffInputUnknown}},
		})
	}
	report.ResolveUnhandledInputs(func(string) bool { return true })
	if len(report.UnhandledInputs) != maxDiffUnhandledInputs || !report.UnhandledInputsTruncated {
		t.Fatalf("listed %d (truncated %v), want %d and truncated", len(report.UnhandledInputs), report.UnhandledInputsTruncated, maxDiffUnhandledInputs)
	}
	if got := report.UnhandledRequestCount(nil); got != maxDiffUnhandledInputs {
		t.Fatalf("UnhandledRequestCount = %d, want %d", got, maxDiffUnhandledInputs)
	}
}

// A value an earlier response returns is a server value even when the two
// recordings' values differ only in digits, so a missed correlation still
// warns; only a short-number difference with no captured origin is a counter.
func TestDiffInputKindPrefersAServerValueOverACounter(t *testing.T) {
	scan := &diffResponseScan{observations: []sample.HARSourceObservation{
		{Observation: correlate.Observation{ID: 1, ResponseBody: `{"orderId":"ORD-104233"}`}},
		{Observation: correlate.Observation{ID: 2}},
	}}
	if kind := diffInputKind("ORD-104233", "ORD-104871", scan, 1); kind != DiffInputServerValue {
		t.Fatalf("kind = %q, want %q", kind, DiffInputServerValue)
	}
	if kind := diffInputKind("12;a", "15;a", scan, 1); kind != DiffInputCounter {
		t.Fatalf("Aura action id kind = %q, want %q", kind, DiffInputCounter)
	}
	if kind := diffInputKind("case-482913", "case-517204", scan, 1); kind == DiffInputCounter {
		t.Fatal("a six-digit difference was read as a counter")
	}
	// The budget running out on a later encoded form keeps the hit the raw
	// form already found.
	value := `say "quoted" token`
	scan = &diffResponseScan{observations: []sample.HARSourceObservation{
		{Observation: correlate.Observation{ID: 1, ResponseBody: "x " + value}},
		{Observation: correlate.Observation{ID: 2}},
	}}
	scan.build()
	scan.remaining = int64(len(scan.joined)) + 1
	if found, checked := scan.before(1, value); !found || !checked {
		t.Fatalf("hit lost when the budget ran out on a later form: %v, %v", found, checked)
	}
}

func TestDiffIdentifierSegmentsKeepFieldNames(t *testing.T) {
	for key, want := range map[string]bool{
		"001A000001abcdeQAA":                   true,
		"0015g00000XyZ12AAB":                   true,
		"123456789":                            true,
		"6f1c2a9e-1b2c-4d5e-8f90-1234567890ab": true,
		"addressLine1Txt":                      false,
		"addressLine2Txt":                      false,
		"name":                                 false,
	} {
		if got := diffIdentifierSegment(key); got != want {
			t.Errorf("diffIdentifierSegment(%q) = %v, want %v", key, got, want)
		}
	}
}

// Inputs that need attention sort before counters, so a run of counter
// fields cannot push them past the list limit.
func TestDiffUnhandledInputsListActionableFirst(t *testing.T) {
	report := &DiffReport{}
	for index := 0; index < maxDiffUnhandledInputs; index++ {
		report.requestInputs = append(report.requestInputs, DiffRequestInput{
			Field:  "form:counter" + strconv.Itoa(index),
			values: map[string]*diffInputValue{"c" + strconv.Itoa(index) + ";a": {requests: []int{1, 2, 3}, kind: DiffInputCounter}},
		})
	}
	report.requestInputs = append(report.requestInputs, DiffRequestInput{
		Field:  "form:token",
		values: map[string]*diffInputValue{"Zq9vT0kenAAA": {requests: []int{9}, kind: DiffInputUnknown}},
	})
	report.ResolveUnhandledInputs(func(string) bool { return true })
	if report.UnhandledInputs[0].Field != "form:token" {
		t.Fatalf("first listed = %q, want the actionable form:token", report.UnhandledInputs[0].Field)
	}
	if !report.UnhandledInputsTruncated || len(report.UnhandledInputs) != maxDiffUnhandledInputs {
		t.Fatalf("listed %d (truncated %v), want the list cut at %d", len(report.UnhandledInputs), report.UnhandledInputsTruncated, maxDiffUnhandledInputs)
	}
}

// A response skipped to fit the budget may hold a value, so a value not found
// before a request is "not checked" when a skipped response precedes it, and
// checked when every skipped response comes after the request.
func TestDiffResponseScanAccountsForSkippedResponses(t *testing.T) {
	scan := &diffResponseScan{observations: []sample.HARSourceObservation{
		{Observation: correlate.Observation{ID: 1, ResponseBody: "skipped holds value-in-skipped"}},
		{Observation: correlate.Observation{ID: 2}},
		{Observation: correlate.Observation{ID: 3, ResponseBody: "later value-in-skipped"}},
		{Observation: correlate.Observation{ID: 4}},
	}}
	scan.build()
	scan.firstSkipped = 0 // as if response 0 had not fit
	if found, checked := scan.before(1, "never-returned-value"); found || checked {
		t.Fatalf("value after a skipped response = %v, %v, want not checked", found, checked)
	}
	scan.firstSkipped = 3
	if found, checked := scan.before(1, "never-returned-value"); found || !checked {
		t.Fatalf("value before every skipped response = %v, %v, want checked", found, checked)
	}
}

func TestDiffShortCounterValuesAllowLongerRuns(t *testing.T) {
	if !diffSameShapeSmallNumbers("12345;a", "1234;a") {
		t.Fatal("a long session's Aura action id was not read as a counter")
	}
	if diffSameShapeSmallNumbers("case-482913", "case-517204") {
		t.Fatal("a long value with six-digit runs was read as a counter")
	}
}

// A request group's requests count as sent whether the script holds them as
// typed actions or, after a JSON round trip, in their JSON-shaped form.
func TestScriptRequestValuesReadsRequestGroupRequests(t *testing.T) {
	child := map[string]any{"name": "a", "type": "request", "method": "GET", "target": "/data?token=group-value"}
	script := &dsl.Script{Actions: []dsl.Action{{
		Name: "group", Type: "http_sequence", Method: "SEQUENCE", Target: "/data",
		Params: map[string]any{"execution_mode": dsl.HTTPSequenceConcurrent, "requests": []any{child}},
	}}}
	if !ScriptRequestValues(script)["group-value"] {
		t.Fatal("a JSON-shaped group request's value was not read as sent")
	}
}
