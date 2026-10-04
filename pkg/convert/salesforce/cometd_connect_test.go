package salesforce

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/har"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestCometdConnectAdmissionRequiresSingleDecodedChannel(t *testing.T) {
	const single = `[{"id":"1","channel":"/meta/connect","clientId":"synthetic"}]`
	for _, test := range []struct {
		name, body, encoding string
		fold                 bool
	}{
		{name: "raw array", body: single, fold: true},
		{name: "raw object", body: `{"id":"1","channel":"/meta/connect"}`, fold: true},
		{name: "optional id", body: `[{"channel":"/meta/connect"}]`, fold: true},
		{name: "form encoded", body: "flag&message=" + url.QueryEscape(single) + "&keep=1&keep=2", fold: true},
		{name: "escaped member and channel", body: `[{"id":"1","chan\u006eel":"\u002fmeta\u002fconnect"}]`, fold: true},
		{name: "all connect batch", body: `[{"id":"1","channel":"/meta/connect"},{"id":"2","channel":"/meta/connect"}]`},
		{name: "mixed encoded batch", body: "message=" + url.QueryEscape(`[{"id":"1","channel":"/meta/connect"},{"id":"2","channel":"/meta/subscribe"}]`)},
		{name: "channel text in data", body: `[{"id":"1","channel":"/meta/subscribe","data":"/meta/connect"}]`},
		{name: "duplicate channel", body: `[{"id":"1","channel":"/meta/subscribe","channel":"/meta/connect"}]`},
		{name: "duplicate escaped channel", body: `[{"id":"1","channel":"/meta/connect","chan\u006eel":"/meta/connect"}]`},
		{name: "duplicate form message", body: "message=" + url.QueryEscape(single) + "&mess%61ge=" + url.QueryEscape(single)},
		{name: "malformed message escape", body: "message=%xy"},
		{name: "malformed unrelated value escape", body: "message=" + url.QueryEscape(single) + "&keep=%xy"},
		{name: "raw form semicolon", body: "message=" + url.QueryEscape(single) + "&keep=1;2"},
		{name: "unpaired surrogate value", body: `[{"id":"1","channel":"/meta/connect","ext":"\ud800"}]`},
		{name: "unpaired surrogate member", body: `[{"id":"1","channel":"/meta/connect","\udc00":1}]`},
		{name: "paired surrogate value", body: `[{"id":"1","channel":"/meta/connect","ext":"\ud83d\ude00"}]`, fold: true},
		{name: "literal backslash escape", body: `[{"id":"1","channel":"/meta/connect","ext":"\\ud800"}]`, fold: true},
		{name: "malformed unrelated key escape", body: "%xy=value&message=" + url.QueryEscape(single)},
		{name: "malformed json", body: `[{"id":"1","channel":"/meta/connect"}`},
		{name: "trailing json", body: single + `{}`},
		{name: "invalid id", body: `[{"id":{},"channel":"/meta/connect"}]`},
		{name: "wire encoding requires manual conversion", body: single, encoding: "base64"},
		{name: "byte bound", body: single + strings.Repeat(" ", maxCometdConnectBodyBytes)},
		{name: "form field bound", body: strings.Repeat("&", maxCometdConnectFormFields) + "message=" + url.QueryEscape(single)},
		{name: "depth bound", body: `{"id":"1","channel":"/meta/connect","data":` + strings.Repeat("[", maxCometdConnectDepth+1) + "0" + strings.Repeat("]", maxCometdConnectDepth+1) + "}"},
		{name: "below token bound", body: `{"id":"1","channel":"/meta/connect","data":[` + strings.Repeat("0,", maxCometdConnectTokens/2) + "0]}", fold: true},
		{name: "token bound", body: `{"id":"1","channel":"/meta/connect","data":[` + strings.Repeat("0,", maxCometdConnectTokens) + "0]}"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.name == "token bound" && !json.Valid([]byte(test.body)) {
				t.Fatal("token-bound fixture must be valid JSON")
			}
			action := dsl.Action{Type: "POST", Body: test.body, BodyEncoding: test.encoding, Metadata: map[string]any{"salesforce": map[string]any{"classification": "cometd"}}}
			if got := isCometdConnectAction(&action); got != test.fold {
				t.Fatalf("fold admission = %t, want %t", got, test.fold)
			}
			if !test.fold {
				original := action.Body
				actions := collapseCometdConnectRun([]dsl.Action{action, action})
				if len(actions) != 2 || actions[0].Body != original || actions[1].Body != original {
					t.Fatal("ambiguous or bounded-out wire request was changed or folded")
				}
			}
		})
	}
}

func TestHARFormEncodedCometdConnectAndGenericRequestNativeReferences(t *testing.T) {
	const clientID = "synthetic-client-123"
	const siblings = "&flag&timeout=110000&keep=%2b+%26&keep=second"
	capture := syntheticFormEncodedCometdCapture()
	converted, err := har.Convert(capture, har.ConvertOptions{ScriptName: "synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	converted.ProtectedRequirements.Take()
	if len(converted.Script.Actions) != 4 || converted.Script.Actions[0].Type != "request" {
		t.Fatal("real HAR importer did not produce the expected filtered generic requests")
	}
	original, _ := json.Marshal(converted.Script)
	review, err := Analyze(converted.Script, AnalyzeOptions{BuildNative: true, HARData: capture})
	if err != nil {
		t.Fatal(err)
	}
	preview := review.NativePreview
	if preview == nil || preview.Script == nil || len(preview.Script.Actions) != 3 {
		t.Fatal("form encoded connect run was not converted through the actual HAR pipeline")
	}
	loop := preview.Script.Actions[2]
	if loop.Type != "control_loop" || loop.Control == nil || loop.Control.Count != 2 || len(loop.Control.Steps) != 1 {
		t.Fatal("native CometD long-poll loop missing")
	}
	body := loop.Control.Steps[0].Body
	if !strings.HasPrefix(body, "mess%61ge=") || !strings.HasSuffix(body, siblings) {
		t.Fatal("loop rewriting changed unselected form spelling/order/duplicate fields")
	}
	values, err := url.ParseQuery(body)
	if err != nil {
		t.Fatal(err)
	}
	var messages []map[string]any
	if err := json.Unmarshal([]byte(values.Get("message")), &messages); err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0]["channel"] != "/meta/connect" || messages[0]["id"] != cometdLoopIndexTemplate || messages[0]["clientId"] == clientID {
		t.Fatal("loop request did not preserve the decoded channel and correlate its client/message identity")
	}
	BindNativePreviewReferences(preview, converted.SourceReview, preview.Script, FinalNativePathDomain)
	coverage := preview.CorrelationCoverage
	if coverage == nil || coverage.NativeMappedProducerCorrelations != 1 || coverage.NativeMappedConsumerReferences != 2 || coverage.NativeUnmappedConsumerReferences != 1 || coverage.SourceLinkedConsumerReferences != 3 {
		t.Fatal("generic request alias did not retain full exact source/native reference accounting")
	}
	if len(preview.Correlations) != 1 || preview.Correlations[0].ProducerReference == nil || preview.Correlations[0].ProducerReference.HAREntryID != 2 || preview.Correlations[0].ProducerReference.OriginalActionPath != "actions[0]" || preview.Correlations[0].ProducerReference.NativeActionPath != "actions[0]" {
		t.Fatal("generic HAR producer lost its exact source/native identity")
	}
	assertNativeReferencePath(t, preview, 4, "actions[2]", "actions[2].control.steps[0]")
	assertNativeReferencePath(t, preview, 5, "actions[3]", "")
	after, _ := json.Marshal(converted.Script)
	if string(original) != string(after) {
		t.Fatal("native analysis or final reference binding changed the source draft")
	}
	// Generic request is only HTTP in an HTTP-compatible enclosing script.
	for _, protocol := range []string{"browser", "soap", "java_http", "http/1.1", "http/2"} {
		preview.Script.Protocol = protocol
		BindNativePreviewReferences(preview, converted.SourceReview, preview.Script, FinalNativePathDomain)
		if preview.CorrelationCoverage.NativeMappedProducerCorrelations != 0 || preview.CorrelationCoverage.NativeMappedConsumerReferences != 0 {
			t.Fatal("generic non-HTTP request was incorrectly treated as a verified HTTP destination")
		}
	}
}

func syntheticFormEncodedCometdCapture() []byte {
	const endpoint = "https://synthetic.my.salesforce.com/cometd/60.0"
	const clientID = "synthetic-client-123"
	const siblings = "&flag&timeout=110000&keep=%2b+%26&keep=second"
	entries := []any{map[string]any{"request": map[string]any{"method": "GET", "url": "https://synthetic.my.salesforce.com/static/app.js"}, "response": map[string]any{"status": 200, "content": map[string]any{"mimeType": "application/javascript", "text": "/* synthetic */"}}}}
	channels := []string{"/meta/handshake", "/meta/subscribe", "/meta/connect", "/meta/connect"}
	for index, channel := range channels {
		message := map[string]any{"id": string(rune('1' + index)), "channel": channel}
		response := map[string]any{"id": message["id"], "channel": channel, "successful": true}
		if index == 0 {
			response["clientId"] = clientID
		} else {
			message["clientId"] = clientID
		}
		if index == 1 {
			message["subscription"] = "/event/Synthetic__e"
		}
		wire, _ := json.Marshal([]any{message})
		responseWire, _ := json.Marshal([]any{response})
		entries = append(entries, map[string]any{"request": map[string]any{"method": "POST", "url": endpoint, "httpVersion": "HTTP/1.1", "headers": []any{map[string]any{"name": "Content-Type", "value": "application/x-www-form-urlencoded"}}, "postData": map[string]any{"mimeType": "application/x-www-form-urlencoded", "text": "mess%61ge=" + url.QueryEscape(string(wire)) + siblings}}, "response": map[string]any{"status": 200, "content": map[string]any{"mimeType": "application/json", "text": string(responseWire)}}})
	}
	capture, _ := json.Marshal(map[string]any{"log": map[string]any{"entries": entries}})
	return capture
}

func TestHARFormEncodedCometdDoesNotFoldDifferentRequestSemantics(t *testing.T) {
	for _, test := range []struct {
		name    string
		message func(map[string]any)
		request func(map[string]any)
		action  func(*dsl.Action)
	}{
		{name: "client identity", message: func(message map[string]any) { message["clientId"] = "synthetic-other-client" }},
		{name: "connection type", message: func(message map[string]any) { message["connectionType"] = "websocket" }},
		{name: "extension state", message: func(message map[string]any) { message["ext"] = map[string]any{"ack": 2} }},
		{name: "request url", request: func(request map[string]any) { request["url"] = request["url"].(string) + "?different=1" }},
		{name: "request headers", request: func(request map[string]any) {
			request["headers"] = append(request["headers"].([]any), map[string]any{"name": "X-Synthetic", "value": "different"})
		}},
		{name: "recorded http version", request: func(request map[string]any) { request["httpVersion"] = "HTTP/2" }},
		{name: "assertion", action: func(action *dsl.Action) {
			action.Assertions = append(action.Assertions, dsl.Assertion{Type: "body_contains", Expected: "synthetic-marker"})
		}},
		{name: "think time", action: func(action *dsl.Action) {
			action.ThinkTime = dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 20}
			action.ThinkTimeAfter = true
		}},
		{name: "execution metadata", action: func(action *dsl.Action) { action.Metadata["body_template_value_encoding"] = "url_form" }},
		{name: "unknown har metadata", action: func(action *dsl.Action) { action.Metadata["har.unknown_future_semantics"] = "different" }},
		{name: "timeout", action: func(action *dsl.Action) { action.TimeoutMs = 150000 }},
		{name: "pacing", action: func(action *dsl.Action) { action.PacingIDs = []string{"synthetic-pacing"} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			capture := syntheticFormEncodedCometdCapture()
			var archive map[string]any
			if err := json.Unmarshal(capture, &archive); err != nil {
				t.Fatal(err)
			}
			request := archive["log"].(map[string]any)["entries"].([]any)[4].(map[string]any)["request"].(map[string]any)
			if test.message != nil {
				post := request["postData"].(map[string]any)
				values, err := url.ParseQuery(post["text"].(string))
				if err != nil {
					t.Fatal(err)
				}
				var messages []map[string]any
				if err := json.Unmarshal([]byte(values.Get("message")), &messages); err != nil {
					t.Fatal(err)
				}
				test.message(messages[0])
				wire, _ := json.Marshal(messages)
				post["text"] = "mess%61ge=" + url.QueryEscape(string(wire)) + "&flag&timeout=110000&keep=%2b+%26&keep=second"
			}
			if test.request != nil {
				test.request(request)
			}
			capture, _ = json.Marshal(archive)
			converted, err := har.Convert(capture, har.ConvertOptions{ScriptName: "synthetic"})
			if err != nil {
				t.Fatal(err)
			}
			converted.ProtectedRequirements.Take()
			if test.action != nil {
				if converted.Script.Actions[3].Metadata == nil {
					converted.Script.Actions[3].Metadata = map[string]any{}
				}
				test.action(&converted.Script.Actions[3])
			}
			original, _ := json.Marshal(converted.Script)
			review, err := Analyze(converted.Script, AnalyzeOptions{BuildNative: true, HARData: capture})
			if err != nil {
				t.Fatal(err)
			}
			if review.NativePreview == nil || review.NativePreview.Script == nil || len(review.NativePreview.Script.Actions) != 4 {
				t.Fatal("different recorded connect execution semantics were collapsed")
			}
			for _, action := range review.NativePreview.Script.Actions {
				if action.Type == "control_loop" {
					t.Fatal("different connect requests became a repeated first request")
				}
			}
			after, _ := json.Marshal(converted.Script)
			if string(original) != string(after) {
				t.Fatal("connect comparison changed original capture semantics")
			}
		})
	}
}

func TestCometdConnectComparisonRetainsUnknownMetadataAndBounds(t *testing.T) {
	first := dsl.Action{Name: "First", Type: "POST", Body: `message=[{"id":"1","channel":"/meta/connect","clientId":"synthetic"}]`, Metadata: map[string]any{"salesforce": map[string]any{"classification": "cometd", "sequence": 1, "har_match_id": 1}}}
	second := first
	second.Name = "Second"
	second.Body = strings.Replace(first.Body, `"id":"1"`, `"id":"2"`, 1)
	second.Metadata = map[string]any{"salesforce": map[string]any{"classification": "cometd", "sequence": 2, "har_match_id": 2}}
	if got := collapseCometdConnectRun([]dsl.Action{first, second}); len(got) != 1 || got[0].Type != "control_loop" {
		t.Fatal("ID-only differences and known source identity did not fold")
	}
	second.Metadata["salesforce"].(map[string]any)["unknown_future_semantics"] = "different"
	if got := collapseCometdConnectRun([]dsl.Action{first, second}); len(got) != 2 {
		t.Fatal("unknown Salesforce metadata was discarded")
	}
	second.Metadata = first.Metadata
	large := strings.Repeat("x", 256<<10)
	first.Params = map[string]any{"synthetic": large}
	second.Params = first.Params
	if got := collapseCometdConnectRun([]dsl.Action{first, second}); len(got) != 2 {
		t.Fatal("oversized semantic comparison was not kept discrete")
	}
	cycle := map[string]any{}
	cycle["self"] = cycle
	first.Params, second.Params = cycle, cycle
	if got := collapseCometdConnectRun([]dsl.Action{first, second}); len(got) != 2 {
		t.Fatal("cyclic semantic comparison was not kept discrete")
	}
}

func TestCometdConnectRunInspectsEachRequestOnceWithDifferentExtensionState(t *testing.T) {
	for _, equivalent := range []bool{false, true} {
		actions := make([]dsl.Action, 200)
		for index := range actions {
			extension := index
			if equivalent {
				extension = 0
			}
			message, _ := json.Marshal([]any{map[string]any{"id": strconv.Itoa(index), "channel": "/meta/connect", "ext": map[string]any{"ack": extension}}})
			actions[index] = dsl.Action{Type: "POST", Body: "message=" + url.QueryEscape(string(message)), Metadata: map[string]any{"salesforce": map[string]any{"classification": "cometd", "sequence": index + 1, "har_match_id": index + 1}}}
		}
		original, _ := json.Marshal(actions)
		out, inspected := collapseCometdConnectRunInspected(actions)
		if inspected != len(actions) {
			t.Fatal("connect fallback re-inspected prior or pending requests")
		}
		if equivalent {
			if len(out) != 1 || out[0].Control == nil || out[0].Control.Count != len(actions) {
				t.Fatal("equivalent ID-only requests were not folded")
			}
		} else if len(out) != len(actions) {
			t.Fatal("varying extension state was discarded")
		}
		after, _ := json.Marshal(actions)
		if string(original) != string(after) {
			t.Fatal("bounded connect inspection changed source requests")
		}
	}
}
