package recordedshape

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func at(ms int) time.Time { return t0.Add(time.Duration(ms) * time.Millisecond) }

type recorded struct {
	url, contentType string
	page             bool
	at, dur          int
}

func build(entries []recorded) (*dsl.Script, []Step) {
	script := &dsl.Script{Name: "s", Protocol: "http"}
	var steps []Step
	for i, entry := range entries {
		script.Actions = append(script.Actions, dsl.Action{Name: "step " + string(rune('A'+i)), Type: "request", Method: "GET", Target: entry.url})
		steps = append(steps, Step{StartedAt: at(entry.at), Duration: time.Duration(entry.dur) * time.Millisecond, URL: "http://app.test" + entry.url, Method: "GET", ContentType: entry.contentType, Page: entry.page})
	}
	return script, steps
}

func types(script *dsl.Script) string {
	var out []string
	for _, action := range script.Actions {
		out = append(out, action.Type)
	}
	return strings.Join(out, ",")
}

func TestShapeCollapsesAPollAndStopsItAtThePageChange(t *testing.T) {
	script, steps := build([]recorded{
		{url: "/home", contentType: "text/html", page: true, at: 0, dur: 50},
		{url: "/api/status?_=1", contentType: "application/json", at: 1000, dur: 20},
		{url: "/api/status?_=2", contentType: "application/json", at: 2000, dur: 20},
		{url: "/api/other", contentType: "application/json", at: 2500, dur: 20},
		{url: "/api/status?_=3", contentType: "application/json", at: 3000, dur: 20},
		{url: "/next", contentType: "text/html", page: true, at: 5000, dur: 50},
	})
	script.Actions[2].Extractors = []dsl.Extractor{{Name: "later", Source: "body", Type: "jsonpath", Expression: "$.x"}}
	result := Shape(script, steps, nil, Options{AsyncScan: true})
	if got := types(script); got != "request,http_async_start,request,http_async_stop,request" {
		t.Fatalf("actions = %s", got)
	}
	start := script.Actions[1]
	if start.Params["pattern"] != "poll" || start.Params["interval_ms"] != 1000 || start.Params["id"] != "async_1" {
		t.Fatalf("start = %+v", start.Params)
	}
	request := start.Params["request"].(map[string]any)
	if extractors, _ := request["extractors"].([]any); len(extractors) != 1 {
		t.Fatalf("extractors of folded repeats must move to the conversation's request: %+v", request["extractors"])
	}
	raw, _ := json.Marshal(start.Metadata)
	if !strings.Contains(string(raw), AsyncConversationReasonCode) {
		t.Fatalf("the conversation needs its promotion finding: %s", raw)
	}
	if len(result.Conversations) != 1 || result.Conversations[0].Occurrences != 3 {
		t.Fatalf("conversations = %+v", result.Conversations)
	}
	for path, want := range map[string]string{
		"actions[0]":               "actions[0]",
		"actions[1]":               "actions[1]",
		"actions[1].extractors[0]": "actions[1].params.request.extractors[0]",
		"actions[3]":               "actions[2]",
		"actions[5].assertions[0]": "actions[4].assertions[0]",
	} {
		if got, ok := result.RemapPath(path); !ok || got != want {
			t.Errorf("RemapPath(%s) = %s %v, want %s", path, got, ok, want)
		}
	}
	if _, ok := result.RemapPath("actions[2]"); ok {
		t.Error("a folded repeat has no path in the shaped script")
	}
}

func TestShapeClassifiesConversations(t *testing.T) {
	cases := map[string]struct {
		entries []recorded
		want    string
	}{
		"long poll": {[]recorded{
			{url: "/poll", contentType: "application/json", at: 0, dur: 1500},
			{url: "/poll", contentType: "application/json", at: 1550, dur: 1500},
			{url: "/poll", contentType: "application/json", at: 3100, dur: 1500},
		}, "long_poll"},
		"push": {[]recorded{
			{url: "/events", contentType: "text/event-stream", at: 0, dur: 30000},
		}, "push"},
		"irregular repeats stay inline": {[]recorded{
			{url: "/api/search?q=a", contentType: "application/json", at: 0, dur: 20},
			{url: "/api/search?q=b", contentType: "application/json", at: 400, dur: 20},
			{url: "/api/search?q=c", contentType: "application/json", at: 5000, dur: 20},
		}, ""},
		"two repeats stay inline": {[]recorded{
			{url: "/poll", contentType: "application/json", at: 0, dur: 20},
			{url: "/poll", contentType: "application/json", at: 1000, dur: 20},
		}, ""},
		"pages stay inline": {[]recorded{
			{url: "/page", contentType: "text/html", page: true, at: 0, dur: 20},
			{url: "/page", contentType: "text/html", page: true, at: 1000, dur: 20},
			{url: "/page", contentType: "text/html", page: true, at: 2000, dur: 20},
		}, ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			script, steps := build(tc.entries)
			result := Shape(script, steps, nil, Options{AsyncScan: true})
			got := ""
			if len(result.Conversations) > 0 {
				got = result.Conversations[0].Pattern
			}
			if got != tc.want {
				t.Fatalf("pattern = %q, want %q (actions %s)", got, tc.want, types(script))
			}
		})
	}
}

func TestShapeFoldsThinkTimeIntoTheStepBefore(t *testing.T) {
	script, steps := build([]recorded{
		{url: "/home", contentType: "text/html", page: true, at: 0, dur: 100},
		{url: "/poll", contentType: "application/json", at: 1000, dur: 100},
		{url: "/poll", contentType: "application/json", at: 2000, dur: 100},
		{url: "/poll", contentType: "application/json", at: 3000, dur: 100},
	})
	for i := range script.Actions[:3] {
		script.Actions[i].ThinkTimeAfter = true
		script.Actions[i].ThinkTime = dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 900}
	}
	Shape(script, steps, nil, Options{AsyncScan: true})
	start := script.Actions[1]
	// The start keeps its own 900 ms, plus each folded repeat's 100 ms
	// request and its 900 ms think time (the last one has none).
	if start.Type != "http_async_start" || start.ThinkTime.MeanMs != 900+100+900+100 {
		t.Fatalf("start = %s think %v", start.Type, start.ThinkTime)
	}
}

func TestShapePlacesSocketsAndCorrelatesReceivedFrames(t *testing.T) {
	script, steps := build([]recorded{
		{url: "/login", contentType: "text/html", page: true, at: 0, dur: 50},
		{url: "/history?room=r-12345", contentType: "application/json", at: 3000, dur: 20},
	})
	sockets := []Socket{
		{URL: "wss://app.test/chat", StartedAt: at(1000), Headers: map[string]string{"Origin": "https://app.test", "Cookie": "SESSION=x", "Sec-WebSocket-Key": "k"}, Frames: []Frame{
			{Send: true, Payload: `{"type":"join"}`, At: at(1100)},
			{Payload: `{"room":"r-12345","members":["ann"]}`, At: at(1200)},
			{Send: true, Payload: "AAE=", Base64: true, At: at(1300)},
		}},
		{URL: "wss://app.test/other", StartedAt: at(1250), EndedAt: at(1400)},
	}
	result := Shape(script, steps, sockets, Options{})
	if got := types(script); got != "request,ws_connect,ws_send,ws_receive,ws_send,ws_close,request" {
		t.Fatalf("actions = %s", got)
	}
	if result.Sockets != 1 || result.SkippedSockets != 1 || len(result.Warnings) != 1 {
		t.Fatalf("an overlapping socket must be skipped with a warning: %+v", result)
	}
	connect := script.Actions[1]
	if connect.Headers["Origin"] != "https://app.test" || connect.Headers["Cookie"] != "" || connect.Headers["Sec-WebSocket-Key"] != "" {
		t.Fatalf("connect headers = %v", connect.Headers)
	}
	receive := script.Actions[3]
	if len(receive.Extractors) != 1 || receive.Extractors[0].Source != "message" || receive.Extractors[0].Expression != "$.room" || receive.Body != "" {
		t.Fatalf("receive = %+v", receive)
	}
	if binary := script.Actions[4]; binary.BodyEncoding != "base64" || binary.Params["type"] != "binary" {
		t.Fatalf("binary send = %+v", binary)
	}
	if history := script.Actions[6]; history.Target != "/history?room={{var.ws_room}}" {
		t.Fatalf("history target = %q", history.Target)
	}
	if login := script.Actions[0]; !login.ThinkTimeAfter || login.ThinkTime.MeanMs != 950 {
		t.Fatalf("the step before the socket thinks until it opens: %+v", login.ThinkTime)
	}
	if closing := script.Actions[5]; !closing.ThinkTimeAfter || closing.ThinkTime.MeanMs != 1700 {
		t.Fatalf("the last socket step thinks until the next HTTP step: %+v", closing.ThinkTime)
	}
}

func TestShapeLeavesMisalignedScriptsAlone(t *testing.T) {
	script, steps := build([]recorded{{url: "/a", at: 0}, {url: "/b", at: 10}})
	result := Shape(script, steps[:1], []Socket{{URL: "wss://x", StartedAt: at(5)}}, Options{AsyncScan: true})
	if result.Changed() || len(script.Actions) != 2 || len(result.Warnings) != 1 {
		t.Fatalf("result = %+v actions %s", result, types(script))
	}
}

func TestIsPage(t *testing.T) {
	if !IsPage("text/html; charset=utf-8", map[string]string{"Sec-Fetch-Dest": "document"}) {
		t.Fatal("a navigated HTML document is a page")
	}
	if IsPage("text/html", map[string]string{"X-Requested-With": "XMLHttpRequest"}) || IsPage("text/html", map[string]string{"Sec-Fetch-Dest": "empty"}) || IsPage("application/json", nil) {
		t.Fatal("background HTML fragments and JSON are not pages")
	}
	if WebSocketURL("https://a.test/s") != "wss://a.test/s" || WebSocketURL("http://a.test/s") != "ws://a.test/s" || WebSocketURL("wss://a.test") != "wss://a.test" {
		t.Fatal("WebSocketURL")
	}
}
