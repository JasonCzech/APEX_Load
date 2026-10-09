package enterprisearchive

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func convertWebSocketSource(t *testing.T, body string, files map[string]string) *ConvertResult {
	t.Helper()
	parsed, err := ParseActionFile("Action.c", "Action()\n{\n"+body+"\n\treturn 0;\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{archiveFiles: files})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestConvertWebSocketSteps(t *testing.T) {
	files := map[string]string{"WebSocketCallbacks.c": `
void OnOpenCB0 (const char* connectionID, const char * responseHeader, int length)
{
	lr_save_string("open", "SocketState");
}
void OnMessageCB0 (const char* connectionID, int isbinary, const char * data, int length)
{
	lr_output_message("WebSocket ID = %s got message=%s length = %d", connectionID, data, length);
	lr_save_string(data, "Reply");
}
`}
	result := convertWebSocketSource(t, `
	web_websocket_connect("ID=0", "URI=wss://chat.example.test/socket?room={Room}", "Origin=https://chat.example.test",
		"OnOpenCB=OnOpenCB0", "OnMessageCB=OnMessageCB0", "SendCookies=1", LAST);
	web_websocket_send("ID=0", "Buffer={\"say\":\"{Text}\"}", "IsBinary=0", LAST);
	web_websocket_send("ID=0", "Buffer=\x01\x02", "IsBinary=1", LAST);
	web_websocket_send("ID=0", "Buffer/Binary=\\x03\\x04", LAST);
	web_websocket_close("ID=0", "Code=1001", "Reason=going away", LAST);`, files)
	actions := result.Script.Actions
	if len(actions) != 6 {
		t.Fatalf("actions = %+v", actions)
	}
	if result.Script.Config.BaseURL != "https://chat.example.test" {
		t.Fatalf("a script with only WebSocket URLs takes its base URL from them: %q", result.Script.Config.BaseURL)
	}
	connect := actions[0]
	if connect.Type != "ws_connect" || connect.Target != "wss://chat.example.test/socket?room={{var.room}}" || connect.Headers["Origin"] != "https://chat.example.test" {
		t.Fatalf("connect = %+v", connect)
	}
	raw, _ := json.Marshal(connect.Metadata)
	for _, want := range []string{webSocketCallbacksReasonCode, webSocketAttributeReasonCode} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("connect findings miss %q: %s", want, raw)
		}
	}
	onMessage := actions[1]
	if onMessage.Type != "ws_on_message" || onMessage.Control == nil || len(onMessage.Control.Steps) != 1 {
		t.Fatalf("on message = %+v", onMessage)
	}
	if raw, _ := json.Marshal(onMessage.Control.Steps); !strings.Contains(string(raw), "{{var.ws_message}}") {
		t.Fatalf("OnMessageCB's data argument must read var.ws_message: %s", raw)
	}
	if text := actions[2]; text.Type != "ws_send" || text.Body != `{"say":"{{var.text}}"}` || text.Params != nil {
		t.Fatalf("text send = %+v", text)
	}
	for i, want := range [][]byte{{1, 2}, {3, 4}} {
		binary := actions[3+i]
		decoded, _ := base64.StdEncoding.DecodeString(binary.Body)
		if binary.BodyEncoding != "base64" || binary.Params["type"] != "binary" || string(decoded) != string(want) {
			t.Fatalf("binary send %d = %+v", i, binary)
		}
	}
	if closing := actions[5]; closing.Type != "ws_close" || closing.Params["code"] != 1001 || closing.Params["reason"] != "going away" {
		t.Fatalf("close = %+v", closing)
	}
	if !strings.Contains(strings.Join(result.Warnings, "\n"), "OnMessageCB OnMessageCB0: 1 logging call(s)") {
		t.Fatalf("warnings = %v", result.Warnings)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("the converted script must validate: %v", issues)
	}
}

func TestConvertWebSocketReviewCases(t *testing.T) {
	cases := map[string]struct {
		body, function, notes string
	}{
		"second connection": {
			body:     `web_websocket_connect("ID=0", "URI=ws://a.test/x", LAST); web_websocket_connect("ID=1", "URI=ws://a.test/y", LAST);`,
			function: "web_websocket_connect", notes: "one WebSocket per virtual user",
		},
		"send without connect": {
			body:     `web_websocket_send("ID=0", "Buffer=hi", LAST);`,
			function: "web_websocket_send", notes: "no converted WebSocket",
		},
		"close of another ID": {
			body:     `web_websocket_connect("ID=0", "URI=ws://a.test/x", LAST); web_websocket_close("ID=1", LAST);`,
			function: "web_websocket_close", notes: "no converted WebSocket",
		},
		"connect without URI": {
			body:     `web_websocket_connect("ID=0", LAST);`,
			function: "web_websocket_connect", notes: "no URI",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			result := convertWebSocketSource(t, "\t"+tc.body, nil)
			found := false
			for _, mapping := range result.SourceReview.Mappings {
				if mapping.Function == tc.function && mapping.Status == "review_only" {
					found = true
					if !strings.Contains(mapping.Notes, tc.notes) {
						t.Fatalf("mapping = %+v", mapping)
					}
				}
			}
			if !found {
				t.Fatalf("no review mapping for %s: %+v", tc.function, result.SourceReview.Mappings)
			}
		})
	}
}
