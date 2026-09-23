package engine

import (
	"bytes"
	"context"
	gorilla "github.com/gorilla/websocket"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	ws "github.com/jasonczech/apexload/pkg/protocol/websocket"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNativeErrorOnlyReplayAndDisabledLogging(t *testing.T) {
	for _, level := range []string{"standard", "extended", "disabled"} {
		t.Run(level, func(t *testing.T) {
			var output bytes.Buffer
			original := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&output, nil)))
			defer slog.SetDefault(original)
			protocolName := registerReplayProtocol(t, func(a *protocol.Action) (*protocol.Result, error) {
				return &protocol.Result{ActionName: a.Name, StartTime: time.Now(), Success: a.Name != "failure", StatusCode: 200}, nil
			})
			script := &dsl.Script{Protocol: protocolName, Config: dsl.ScriptConfig{Log: dsl.LogConfig{Level: level, OnErrorOnly: true, ErrorBufferBytes: 4096}}, Init: []dsl.Action{{Name: "init history", Type: "request"}}, Actions: []dsl.Action{{Name: "success history", Type: "request"}, {Name: "failure", Type: "request"}}, Teardown: []dsl.Action{{Name: "successful teardown", Type: "request"}}}
			before := errorLogProcessBytes.Load()
			_, err := ReplayScript(context.Background(), script, ReplayOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if level == "disabled" {
				if output.Len() != 0 {
					t.Fatal("disabled logging emitted diagnostics")
				}
			} else {
				if !strings.Contains(output.String(), "init history") || !strings.Contains(output.String(), "success history") || strings.Contains(output.String(), "successful teardown") {
					t.Fatalf("error history incorrect: %s", output.String())
				}
			}
			if errorLogProcessBytes.Load() != before {
				t.Fatal("replay leaked error-history capacity")
			}
		})
	}
}
func TestNativeWebSocketCallbacksExecuteSerially(t *testing.T) {
	received := make(chan string, 3)
	peerCtx, cancelPeer := context.WithCancel(context.Background())
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		conn, e := (&gorilla.Upgrader{}).Upgrade(rw, r, nil)
		if e != nil {
			return
		}
		defer conn.Close()
		for _, s := range []string{"first", "second", "third"} {
			if conn.WriteMessage(gorilla.TextMessage, []byte(s)) != nil {
				return
			}
		}
		for i := 0; i < 3; i++ {
			_, b, e := conn.ReadMessage()
			if e != nil {
				return
			}
			received <- string(b)
		}
		// Close only after the test has verified every callback, avoiding a race
		// between the success loop and the intentional terminal read error.
		<-peerCtx.Done()
	}))
	defer func() { cancelPeer(); server.Close() }()
	p := &ws.WebSocket{}
	if e := p.Init(context.Background(), map[string]any{"websocket_callback_interval_ms": 5}); e != nil {
		t.Fatal(e)
	}
	defer p.Teardown(context.Background())
	script := &dsl.Script{Protocol: "websocket", Config: dsl.ScriptConfig{Log: dsl.LogConfig{Level: "disabled"}}}
	vu := &VUser{Proto: p, Script: script, Metrics: NewMetricsCollector(32), Variables: map[string]string{}}
	actions := []dsl.Action{{Name: "connect", Type: "ws_connect", Target: "ws" + strings.TrimPrefix(server.URL, "http")}, {Name: "handler", Type: "ws_on_message", Control: &dsl.ControlFlow{Steps: []dsl.Action{{Name: "echo callback", Type: "ws_send", Body: "{{var.ws_message}}"}}}}}
	if !vu.RunLifecycleBlock(context.Background(), actions, "init") {
		t.Fatal("registration failed")
	}

	rejected := dsl.Action{Name: "replace active handler", Type: "ws_on_message", Control: &dsl.ControlFlow{Steps: []dsl.Action{{Type: "ws_send", Body: "wrong handler"}}}}
	if vu.RunLifecycleBlock(context.Background(), []dsl.Action{rejected}, "actions") {
		t.Fatal("duplicate registration unexpectedly accepted")
	}
	if len(vu.webSocketCallbackSteps) != 1 || vu.webSocketCallbackSteps[0].Body != "{{var.ws_message}}" {
		t.Fatal("failed registration replaced callback body")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for len(received) < 3 && ctx.Err() == nil {
		if !vu.RunLifecycleBlock(ctx, []dsl.Action{{Name: "dispatch", Type: "ws_dispatch"}}, "actions") {
			t.Fatal("callback dispatch failed")
		}
	}
	for _, want := range []string{"first", "second", "third"} {
		select {
		case got := <-received:
			if got != want {
				t.Fatalf("got %q want %q", got, want)
			}
		case <-ctx.Done():
			t.Fatal("callback missing")
		}
	}
	cancelPeer()
	for {
		err := vu.dispatchWebSocketCallbacks(ctx, true)
		if err != nil {
			if ctx.Err() != nil {
				t.Fatal("peer close did not reach callback dispatch")
			}
			break
		}
	}
	vu.Metrics.Start(time.Hour)
	vu.Metrics.Stop()
	snapshot, ok := vu.Metrics.LatestSnapshot()
	metric := snapshot.ByAction["WebSocket callback dispatch"]
	if !ok || metric == nil || metric.Errors != 1 {
		t.Fatal("callback failure was absent from aggregate error metrics")
	}

}

func TestNativeWindows1252FormParameterSubstitution(t *testing.T) {
	vu := &VUser{Variables: map[string]string{"name": "café & €"}}
	action := dsl.Action{Type: "request", Method: "POST", Target: "https://example.invalid", Body: "name={{var.name}}", BodyEncoding: "windows-1252", Metadata: map[string]any{dsl.ImportURLFormTemplateValueEncodingMetadataKey: dsl.ImportURLFormTemplateValueEncodingQueryComponent}}
	resolved := vu.resolveAction(action)
	if got := string(resolved.Body); got != "name=caf%E9+%26+%80" {
		t.Fatalf("form bytes changed: %q", got)
	}
	vu.Variables["name"] = "😀"
	vu.resolveAction(action)
	if len(vu.unresolvedParameterRefs) == 0 {
		t.Fatal("unrepresentable form parameter was accepted")
	}
}
