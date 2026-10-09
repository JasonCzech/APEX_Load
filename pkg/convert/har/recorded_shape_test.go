package har

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/recordingprofile"
)

// A browser HAR's WebSocket entry (Chrome/Firefox _webSocketMessages)
// becomes WebSocket steps among the HTTP steps, in recorded order.
func TestConvertPlacesHARWebSocketMessages(t *testing.T) {
	har := map[string]any{"log": map[string]any{"version": "1.2", "creator": map[string]any{"name": "Chrome"}, "entries": []any{
		map[string]any{
			"startedDateTime": "2026-01-01T00:00:00.000Z", "time": 20,
			"request":  map[string]any{"method": "POST", "url": "https://chat.example.test/login", "httpVersion": "HTTP/1.1", "headers": []any{}, "postData": map[string]any{"mimeType": "application/x-www-form-urlencoded", "text": "user=ada"}},
			"response": map[string]any{"status": 200, "httpVersion": "HTTP/1.1", "headers": []any{map[string]any{"name": "Content-Type", "value": "text/html"}}, "content": map[string]any{"mimeType": "text/html", "text": "<html><title>In</title></html>"}},
		},
		map[string]any{
			"startedDateTime": "2026-01-01T00:00:01.000Z", "time": 5000, "_resourceType": "websocket",
			"request":  map[string]any{"method": "GET", "url": "wss://chat.example.test/socket", "httpVersion": "HTTP/1.1", "headers": []any{map[string]any{"name": "Upgrade", "value": "websocket"}, map[string]any{"name": "Origin", "value": "https://chat.example.test"}}},
			"response": map[string]any{"status": 101, "httpVersion": "HTTP/1.1", "headers": []any{map[string]any{"name": "Upgrade", "value": "websocket"}}, "content": map[string]any{}},
			"_webSocketMessages": []any{
				map[string]any{"type": "send", "time": 1767225601.1, "opcode": 1, "data": `{"type":"join"}`},
				map[string]any{"type": "receive", "time": 1767225601.2, "opcode": 1, "data": `{"room":"r-12345"}`},
			},
		},
		map[string]any{
			"startedDateTime": "2026-01-01T00:00:03.000Z", "time": 20,
			"request":  map[string]any{"method": "GET", "url": "https://chat.example.test/history?room=r-12345", "httpVersion": "HTTP/1.1", "headers": []any{map[string]any{"name": "X-Requested-With", "value": "XMLHttpRequest"}}},
			"response": map[string]any{"status": 200, "httpVersion": "HTTP/1.1", "headers": []any{map[string]any{"name": "Content-Type", "value": "application/json"}}, "content": map[string]any{"mimeType": "application/json", "text": "[]"}},
		},
	}}}
	raw, _ := json.Marshal(har)
	result, err := Convert(raw, ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	for _, action := range result.Script.Actions {
		types = append(types, action.Type)
	}
	if got := strings.Join(types, ","); got != "request,ws_connect,ws_send,ws_receive,ws_close,request" {
		t.Fatalf("actions = %s", got)
	}
	if connect := result.Script.Actions[1]; connect.Target != "wss://chat.example.test/socket" || connect.Headers["Origin"] != "https://chat.example.test" {
		t.Fatalf("connect = %+v", connect)
	}
	if history := result.Script.Actions[5]; !strings.Contains(history.Target, "{{var.ws_room}}") {
		t.Fatalf("the room received over the socket must be correlated: %q", history.Target)
	}
	for _, mapping := range result.SourceReview.Mappings {
		if mapping.TargetPath == "actions[1]" && mapping.TargetName != result.Script.Actions[1].Name {
			t.Fatalf("source mappings must follow the shaped actions: %+v", mapping)
		}
	}
	if strings.Contains(strings.Join(result.Warnings, "\n"), "WebSocket upgrade entries were skipped") {
		t.Fatalf("warnings = %v", result.Warnings)
	}
}

func TestConvertAppliesRecordingProfile(t *testing.T) {
	entry := func(at, url, contentType, body string, headers ...map[string]any) map[string]any {
		requestHeaders := []any{}
		for _, header := range headers {
			requestHeaders = append(requestHeaders, header)
		}
		return map[string]any{
			"startedDateTime": at, "time": 20,
			"request":  map[string]any{"method": "GET", "url": url, "httpVersion": "HTTP/1.1", "headers": requestHeaders},
			"response": map[string]any{"status": 200, "httpVersion": "HTTP/1.1", "headers": []any{map[string]any{"name": "Content-Type", "value": contentType}}, "content": map[string]any{"mimeType": contentType, "text": body}},
		}
	}
	har := map[string]any{"log": map[string]any{"version": "1.2", "creator": map[string]any{"name": "Chrome"}, "entries": []any{
		entry("2026-01-01T00:00:00.000Z", "https://shop.test/start", "text/html", `<a href="/order?order=A9F3K2&x=1">`, map[string]any{"name": "X-Debug", "value": "1"}),
		entry("2026-01-01T00:00:00.500Z", "https://shop.test/logo.png", "image/png", "PNG"),
		entry("2026-01-01T00:00:01.000Z", "https://shop.test/order?order=A9F3K2", "text/html", "<html>ok</html>"),
	}}}
	raw, _ := json.Marshal(har)
	profile := &recordingprofile.Profile{
		ID: "p", Name: "P", DropHeaders: []string{"X-Debug"}, ExcludeContentTypes: []string{"image/*"}, ThinkTimeThresholdMs: 2000,
		Correlation: recordingprofile.CorrelationSettings{UserRules: []recordingprofile.UserRule{{Name: "OrderID", Type: "boundary", Left: "order=", Right: "&"}}},
	}
	result, err := Convert(raw, ConvertOptions{Profile: profile, IncludeStaticAssets: boolPointer(true)})
	if err != nil {
		t.Fatal(err)
	}
	actions := result.Script.Actions
	if len(actions) != 2 {
		t.Fatalf("the excluded content type must not become a step: %+v", actions)
	}
	if _, ok := actions[0].Headers["X-Debug"]; ok || actions[0].ThinkTimeAfter {
		t.Fatalf("drop list and think-time threshold: %+v", actions[0])
	}
	if !strings.Contains(actions[1].Target, "{{var.") {
		t.Fatalf("the project rule must correlate the order id: %q", actions[1].Target)
	}
}
