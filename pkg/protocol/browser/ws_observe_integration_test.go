package browser

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gorillawebsocket "github.com/gorilla/websocket"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestBrowserReplayWebSocketSession(t *testing.T) {
	upgrader := gorillawebsocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ws":
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Logf("upgrade failed: %v", err)
				return
			}
			defer conn.Close()
			_ = conn.WriteMessage(gorillawebsocket.TextMessage, []byte(`{"type":"ready","token":"integration-secret"}`))
			for {
				_, payload, err := conn.ReadMessage()
				if err != nil {
					return
				}
				if string(payload) == "probe-after-failed-navigation" {
					if err := conn.WriteMessage(gorillawebsocket.TextMessage, []byte(`{"token":"post-failure-secret"}`)); err != nil {
						return
					}
				}
			}
		default:
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<!doctype html>
<html><body>
  <div id="status">opening</div>
  <script>
    const wsURL = (location.protocol === 'https:' ? 'wss://' : 'ws://') + location.host + '/ws';
    const ws = new WebSocket(wsURL);
    ws.onopen = () => ws.send(JSON.stringify({type: 'client-ready'}));
    ws.onmessage = event => { document.querySelector('#status').textContent = event.data; };
  </script>
</body></html>`))
		}
	}))
	defer server.Close()

	for _, debug := range []bool{false, true} {
		t.Run(fmt.Sprintf("debug=%v", debug), func(t *testing.T) {
			b := &Browser{}
			if err := b.Init(context.Background(), map[string]any{
				"base_url":                              server.URL,
				"browser_headless":                      true,
				protocol.ReplayUnredactedDebugConfigKey: debug,
			}); err != nil {
				requireBrowserRuntime(t, "", err)
			}
			defer b.Teardown(context.Background())

			// A percent-encoded host character and a backslash separator are both
			// accepted and normalized by the browser's WHATWG URL parser.
			escapedFirst := fmt.Sprintf("%02X", server.URL[len("http://")])
			encodedHostURL := "http://%" + escapedFirst[:1] + "\t" + escapedFirst[1:] + server.URL[len("http://")+1:]
			navigate, err := b.Execute(context.Background(), &protocol.Action{
				Name:   "Open WebSocket page",
				Type:   "browser_navigate",
				Target: encodedHostURL + `\socket-page`,
			})
			if err != nil {
				t.Fatalf("navigate returned error: %v", err)
			}
			if !navigate.Success {
				t.Fatalf("navigate failed: %s", navigate.ErrorMessage)
			}
			if got := b.currentPage().URL(); got != server.URL+"/socket-page" {
				t.Fatalf("browser normalized backslash URL to %q", got)
			}

			wait, err := b.Execute(context.Background(), &protocol.Action{
				Name: "Wait for ready frame",
				Type: "browser_ws_wait",
				Params: map[string]any{
					"url_contains": "/ws",
					"direction":    "received",
					"match":        `integration-secret`,
					"match_mode":   "contains",
					"timeout_ms":   3000,
					"count":        1,
				},
			})
			if err != nil {
				t.Fatalf("ws wait returned error: %v", err)
			}
			if !wait.Success {
				t.Fatalf("ws wait failed: %s", wait.ErrorMessage)
			}
			if wait.ProtocolEvidence["browser_ws_matches"] == nil {
				t.Fatalf("ws wait missing evidence: %+v", wait.ProtocolEvidence)
			}
			if got := strings.Contains(fmt.Sprint(wait.ProtocolEvidence), "integration-secret"); got != debug {
				t.Fatalf("secret visibility=%v, debug=%v; evidence=%v", got, debug, wait.ProtocolEvidence)
			}

			assertResult, err := b.Execute(context.Background(), &protocol.Action{
				Name: "Assert ready frame",
				Type: "browser_ws_assert",
				Params: map[string]any{
					"url_contains": "/ws",
					"direction":    "received",
					"match":        `integration-secret`,
					"match_mode":   "contains",
					"count":        1,
				},
			})
			if err != nil {
				t.Fatalf("ws assert returned error: %v", err)
			}
			if !assertResult.Success {
				t.Fatalf("ws assert failed: %s", assertResult.ErrorMessage)
			}
			if got := strings.Contains(fmt.Sprint(assertResult.ProtocolEvidence), "integration-secret"); got != debug {
				t.Fatalf("assert secret visibility=%v, debug=%v; evidence=%v", got, debug, assertResult.ProtocolEvidence)
			}

			failed, err := b.Execute(context.Background(), &protocol.Action{Type: "browser_navigate", Target: "http://%zz"})
			if err == nil && failed.Success {
				t.Fatal("invalid navigation unexpectedly succeeded")
			}
			if _, err := b.currentPage().Evaluate(`() => ws.send("probe-after-failed-navigation")`); err != nil {
				t.Fatalf("existing socket unavailable: %v", err)
			}
			afterFailure, err := b.Execute(context.Background(), &protocol.Action{Type: "browser_ws_wait", Params: map[string]any{"match": "post-failure-secret", "direction": "received", "timeout_ms": 3000}})
			if err != nil || !afterFailure.Success {
				t.Fatalf("failed navigation retired socket: result=%+v err=%v", afterFailure, err)
			}
			if got := strings.Contains(fmt.Sprint(afterFailure.ProtocolEvidence), "post-failure-secret"); got != debug {
				t.Fatalf("post-failure evidence visibility=%v, debug=%v", got, debug)
			}
		})
	}
}
