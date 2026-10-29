package engine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	gorillawebsocket "github.com/gorilla/websocket"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	_ "github.com/jasonczech/apexload/pkg/protocol/grpc"
	_ "github.com/jasonczech/apexload/pkg/protocol/websocket"
	googlegrpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
)

func TestReplayGRPCFailureRetainsEffectiveMetadata(t *testing.T) {
	const secret = "grpc-config-private"
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	seen := make(chan metadata.MD, 8)
	server := googlegrpc.NewServer(googlegrpc.UnaryInterceptor(func(ctx context.Context, req any, info *googlegrpc.UnaryServerInfo, handler googlegrpc.UnaryHandler) (any, error) {
		md, _ := metadata.FromIncomingContext(ctx)
		seen <- md.Copy()
		return nil, status.Error(codes.PermissionDenied, "rejected")
	}))
	healthpb.RegisterHealthServer(server, health.NewServer())
	reflection.Register(server)
	go server.Serve(listener)
	defer server.Stop()
	for _, authType := range []string{"basic", "bearer"} {
		for _, override := range []bool{false, true} {
			for _, debug := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/override=%t/debug=%t", authType, override, debug), func(t *testing.T) {
					auth := dsl.AuthConfig{Type: authType, Username: "user", Password: secret, Token: secret}
					config := dsl.ScriptConfig{BaseURL: listener.Addr().String()}
					config.GRPC.PlainText, config.GRPC.UseReflection = true, true
					config.GRPC.Metadata = map[string]string{"x-private": secret, "x-override": "default", "x-raw-bin": string([]byte{0xff, 0x00, 0xfe}), "user-agent": "authored-agent", "content-type": "authored-type", "te": "authored-te"}
					config.HTTP.Auth = auth
					action := dsl.Action{Name: "check", Type: "grpc_unary", Target: "grpc.health.v1.Health/Check", Body: `{}`, Headers: map[string]string{"x-override": "step", "X-Override": "alternate"}}
					expectedAuth := "Bearer " + secret
					if authType == "basic" {
						expectedAuth = "Basic " + base64.StdEncoding.EncodeToString([]byte("user:"+secret))
					}
					if override {
						action.Headers["authorization"] = "Bearer step-override"
						expectedAuth = "Bearer step-override"
					}
					var progress *ReplayActionEvent
					report, err := ReplayScript(context.Background(), &dsl.Script{Name: "grpc", Protocol: "grpc", Config: config, Actions: []dsl.Action{action}}, ReplayOptions{UnredactedDebug: debug, RedactionRules: []RedactionRule{{Pattern: secret}}, DebugController: replayDebugControllerFunc(func(context.Context, ReplayDebugAction) error { return nil }), OnProgress: func(p ReplayProgressEvent) {
						if p.Type == ReplayProgressEventRun {
							progress = p.Event
						}
					}})
					if err != nil || len(report.Events) != 1 || progress == nil || report.Events[0].Snapshot == nil {
						t.Fatalf("report=%#v progress=%#v err=%v", report, progress, err)
					}
					actual := <-seen
					if actual.Get("user-agent")[0] == "authored-agent" {
						t.Fatalf("reserved user agent unexpectedly reached server: %#v", actual)
					}
					if actual.Get("authorization")[0] != expectedAuth || (actual.Get("x-override")[0] != "step" && actual.Get("x-override")[0] != "alternate") || actual.Get("x-private")[0] != secret || actual.Get("x-raw-bin")[0] != string([]byte{0xff, 0x00, 0xfe}) {
						t.Fatalf("actual metadata=%#v", actual)
					}
					for _, event := range []*ReplayActionEvent{&report.Events[0], progress} {
						encoded, err := json.Marshal(event)
						var copy ReplayActionEvent
						if err != nil || json.Unmarshal(encoded, &copy) != nil || copy.Snapshot == nil {
							t.Fatalf("snapshot=%s err=%v", encoded, err)
						}
						if debug && (!copy.Snapshot.RequestWireHeadersUnavailable || copy.Snapshot.RequestHeaders["user-agent"] != "authored-agent" || copy.Snapshot.RequestHeaders["content-type"] != "authored-type" || copy.Snapshot.RequestHeaders["te"] != "authored-te" || copy.Snapshot.RequestHeaders["authorization"] != expectedAuth || copy.Snapshot.RequestHeaders["x-override"] != actual.Get("x-override")[0] || copy.Snapshot.RequestHeaders["x-private"] != secret || copy.Snapshot.RequestHeaders["x-raw-bin"] != base64.RawStdEncoding.EncodeToString([]byte(actual.Get("x-raw-bin")[0]))) {
							t.Fatalf("effective metadata=%#v", copy.Snapshot.RequestHeaders)
						}
						if !debug && (strings.Contains(string(encoded), secret) || strings.Contains(string(encoded), base64.StdEncoding.EncodeToString([]byte("user:"+secret)))) {
							t.Fatal("ordinary replay leaked credentials")
						}
					}
				})
			}
		}
	}
}

func TestReplayWebSocketRejectedHandshakeRetainsEffectiveHeaders(t *testing.T) {
	const secret = "handshake-private"
	seen := make(chan *http.Request, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Clone(context.Background())
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	for _, debug := range []bool{false, true} {
		for _, userAgent := range []string{"default", "duplicate", "empty"} {
			t.Run(fmt.Sprintf("debug=%t/user-agent=%s", debug, userAgent), func(t *testing.T) {
				config := dsl.ScriptConfig{BaseURL: server.URL}
				config.HTTP.DefaultHeaders = map[string]string{"X-Default": secret, "X-Override": "default"}
				config.HTTP.Auth = dsl.AuthConfig{Type: "bearer", Token: secret}
				config.WebSocket.Origin = "https://origin.example"
				action := dsl.Action{Name: "connect", Type: "ws_connect", Target: "/ws", Headers: map[string]string{"X-Override": "action", "X-Multi": "ignored"}, HeaderList: dsl.HeaderList{{Name: "X-Multi", Value: "one"}, {Name: "X-Multi", Value: "two"}}}
				if userAgent == "duplicate" {
					action.HeaderList = append(action.HeaderList, dsl.HeaderEntry{Name: "User-Agent", Value: "first"}, dsl.HeaderEntry{Name: "User-Agent", Value: "second"})
				}
				if userAgent == "empty" {
					action.HeaderList = append(action.HeaderList, dsl.HeaderEntry{Name: "User-Agent", Value: ""})
				}
				var progress *ReplayActionEvent
				report, err := ReplayScript(context.Background(), &dsl.Script{Name: "ws", Protocol: "websocket", Config: config, Actions: []dsl.Action{action}}, ReplayOptions{UnredactedDebug: debug, RedactionRules: []RedactionRule{{Pattern: secret}}, DebugController: replayDebugControllerFunc(func(context.Context, ReplayDebugAction) error { return nil }), OnProgress: func(p ReplayProgressEvent) {
					if p.Type == ReplayProgressEventRun {
						progress = p.Event
					}
				}})
				if err != nil || len(report.Events) != 1 || progress == nil || report.Events[0].Snapshot == nil {
					t.Fatalf("report=%#v progress=%#v err=%v", report, progress, err)
				}
				actual := <-seen
				if strings.Join(actual.Header.Values("X-Multi"), "\n") != "one\ntwo" || actual.Header.Get("X-Default") != secret || actual.Header.Get("X-Override") != "action" || actual.Header.Get("Authorization") != "Bearer "+secret {
					t.Fatalf("actual headers=%#v", actual.Header)
				}
				for _, event := range []*ReplayActionEvent{&report.Events[0], progress} {
					if debug {
						expected := actual.Header.Clone()
						expected.Set("Host", actual.Host)
						actualHeaders := make(http.Header)
						for key, value := range event.Snapshot.RequestHeaders {
							actualHeaders[http.CanonicalHeaderKey(key)] = strings.Split(value, "\n")
						}
						if !reflect.DeepEqual(actualHeaders, expected) {
							t.Fatalf("snapshot headers=%#v actual=%#v", actualHeaders, expected)
						}
					}
					encoded, err := json.Marshal(event)
					var copy ReplayActionEvent
					if err != nil || json.Unmarshal(encoded, &copy) != nil || copy.Snapshot == nil {
						t.Fatalf("snapshot=%s err=%v", encoded, err)
					}
					h := copy.Snapshot.RequestHeaders
					if debug && (h["X-Multi"] != "one\ntwo" || h["X-Default"] != secret || h["X-Override"] != "action" || h["Authorization"] != "Bearer "+secret || h["Origin"] != "https://origin.example" || h["Sec-WebSocket-Key"] != actual.Header.Get("Sec-Websocket-Key") || h["Host"] != actual.Host) {
						t.Fatalf("effective headers=%#v actual=%#v", h, actual.Header)
					}
					if !debug && strings.Contains(string(encoded), secret) {
						t.Fatal("ordinary replay leaked credentials")
					}
				}
			})
		}
	}
}

func TestEffectiveSnapshotHeadersAreRedactedBeforeSink(t *testing.T) {
	const secret = "sink-private"
	sink := &captureResultSink{}
	vu := &VUser{Script: &dsl.Script{}, Variables: map[string]string{}, Metrics: NewMetricsCollector(4), SnapshotStore: NewSnapshotStore(4), ResultSink: sink, RedactionConfig: WithSecretValueRedaction(nil, []string{secret})}
	result := &protocol.Result{Success: false, Snapshot: &protocol.ErrorSnapshot{RequestHeaders: map[string]string{"Authorization": "Bearer " + secret, "X-Repeat": "one\n" + secret}}}
	raw := captureUnredactedReplayResult(result)
	vu.recordResult(result, dsl.ErrorHandlingConfig{}, dsl.LogConfig{})
	data, err := json.Marshal(sink.result)
	if err != nil || strings.Contains(string(data), secret) || raw.Snapshot.RequestHeaders["X-Repeat"] != "one\n"+secret {
		t.Fatalf("sink=%s err=%v", data, err)
	}
}

func TestReplayWebSocketDroppedHandshakeReportsUnavailableWireHeaders(t *testing.T) {
	seen := make(chan *http.Request, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Clone(context.Background())
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
	}))
	defer server.Close()
	var progress *ReplayActionEvent
	report, err := ReplayScript(context.Background(), &dsl.Script{Name: "dropped", Protocol: "websocket", Config: dsl.ScriptConfig{BaseURL: server.URL}, Actions: []dsl.Action{{Name: "connect", Type: "ws_connect", Target: "/ws", Headers: map[string]string{"X-Supplied": "value"}}}}, ReplayOptions{UnredactedDebug: true, DebugController: replayDebugControllerFunc(func(context.Context, ReplayDebugAction) error { return nil }), OnProgress: func(p ReplayProgressEvent) {
		if p.Type == ReplayProgressEventRun {
			progress = p.Event
		}
	}})
	actual := <-seen
	if actual.Header.Get("Sec-Websocket-Key") == "" {
		t.Fatal("peer did not receive generated key")
	}
	if err != nil || len(report.Events) != 1 || progress == nil {
		t.Fatalf("report=%#v progress=%#v err=%v", report, progress, err)
	}
	for _, event := range []*ReplayActionEvent{&report.Events[0], progress} {
		encoded, err := json.Marshal(event)
		var copy ReplayActionEvent
		if err != nil || json.Unmarshal(encoded, &copy) != nil || copy.Snapshot == nil || !copy.Snapshot.RequestWireHeadersUnavailable || copy.Snapshot.RequestHeadersOmitted || copy.Snapshot.RequestHeaders["X-Supplied"] != "value" {
			t.Fatalf("event=%s err=%v", encoded, err)
		}
	}
}

func TestReplayWebSocketRejectedReconnectRetainsHandshake(t *testing.T) {
	const secret = "reconnect-private"
	seen := make(chan *http.Request, 4)
	count := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		seen <- r.Clone(context.Background())
		if count == 1 {
			conn, err := (&gorillawebsocket.Upgrader{}).Upgrade(w, r, nil)
			if err == nil {
				_ = conn.Close()
			}
			return
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	for _, debug := range []bool{true, false} {
		count = 0
		config := dsl.ScriptConfig{BaseURL: server.URL, ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: dsl.ContinueOnErrorValue(true)}}
		config.WebSocket.Reconnect = true
		config.HTTP.DefaultHeaders = map[string]string{"X-Default": secret}
		actions := []dsl.Action{{Name: "connect", Type: "ws_connect", Target: "/socket", HeaderList: dsl.HeaderList{{Name: "X-Multi", Value: "one"}, {Name: "X-Multi", Value: "two"}}}, {Name: "receive", Type: "ws_receive"}, {Name: "send", Type: "ws_send", Body: "hello", Target: "/different"}}
		var progress *ReplayActionEvent
		report, err := ReplayScript(context.Background(), &dsl.Script{Name: "reconnect", Protocol: "websocket", Config: config, Actions: actions}, ReplayOptions{UnredactedDebug: debug, RedactionRules: []RedactionRule{{Pattern: secret}}, DebugController: replayDebugControllerFunc(func(context.Context, ReplayDebugAction) error { return nil }), OnProgress: func(p ReplayProgressEvent) {
			if p.Type == ReplayProgressEventRun && p.Event != nil && p.Event.ActionName == "send" {
				progress = p.Event
			}
		}})
		<-seen
		actual := <-seen
		if err != nil || len(report.Events) != 3 || progress == nil {
			t.Fatalf("report=%#v progress=%#v err=%v", report, progress, err)
		}
		for _, event := range []*ReplayActionEvent{&report.Events[2], progress} {
			encoded, err := json.Marshal(event)
			var copy ReplayActionEvent
			if err != nil || json.Unmarshal(encoded, &copy) != nil || copy.Snapshot == nil {
				t.Fatalf("event=%s err=%v", encoded, err)
			}
			if debug && (copy.Snapshot.RequestMethod != "ws_reconnect" || !strings.HasSuffix(copy.Snapshot.RequestURL, "/socket") || copy.Snapshot.ResponseStatus != http.StatusForbidden || copy.Snapshot.RequestHeaders["X-Default"] != secret || copy.Snapshot.RequestHeaders["X-Multi"] != "one\ntwo" || copy.Snapshot.RequestHeaders["Sec-WebSocket-Key"] != actual.Header.Get("Sec-Websocket-Key") || copy.Snapshot.RequestWireHeadersUnavailable) {
				t.Fatalf("event=%s actual=%#v", encoded, actual.Header)
			}
			if !debug && strings.Contains(string(encoded), secret) {
				t.Fatalf("ordinary event leaked: %s", encoded)
			}
		}
	}
}

func TestGRPCBinaryMetadataDebugProjectionDoesNotReachOrdinarySinks(t *testing.T) {
	const secret = "supersecret"
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := googlegrpc.NewServer(googlegrpc.UnaryInterceptor(func(ctx context.Context, req any, info *googlegrpc.UnaryServerInfo, handler googlegrpc.UnaryHandler) (any, error) {
		return nil, status.Error(codes.PermissionDenied, "rejected")
	}))
	healthpb.RegisterHealthServer(server, health.NewServer())
	reflection.Register(server)
	go server.Serve(listener)
	defer server.Stop()
	for _, prefix := range []string{"", "x", "xy", "xyz"} {
		t.Run(fmt.Sprintf("offset=%d", len(prefix)), func(t *testing.T) {
			value := prefix + secret + "suffix"
			wire := base64.RawStdEncoding.EncodeToString([]byte(value))
			adapter, err := protocol.Get("grpc")
			if err != nil {
				t.Fatal(err)
			}
			err = adapter.Init(context.Background(), map[string]any{"base_url": listener.Addr().String(), "grpc_plain_text": true, "grpc_use_reflection": true, "grpc_metadata": map[string]string{"payload-bin": value}, protocol.ReplayUnredactedDebugConfigKey: true})
			if err != nil {
				t.Fatal(err)
			}
			defer adapter.Teardown(context.Background())
			result, err := adapter.Execute(context.Background(), &protocol.Action{Name: "call", Type: "grpc_unary", Target: "grpc.health.v1.Health/Check", Body: []byte(`{}`)})
			if err != nil || result.Snapshot == nil || result.Snapshot.RequestHeaders["payload-bin"] != wire {
				t.Fatalf("result=%#v err=%v", result, err)
			}
			raw := captureUnredactedReplayResult(result)
			sink := &captureResultSink{}
			vu := &VUser{Script: &dsl.Script{}, Variables: map[string]string{}, Metrics: NewMetricsCollector(4), SnapshotStore: NewSnapshotStore(4), ResultSink: sink, RedactionConfig: WithSecretValueRedaction(nil, []string{secret})}
			if err := vu.recordResult(result, dsl.ErrorHandlingConfig{SnapshotOnError: true}, dsl.LogConfig{}); err != nil {
				t.Fatal(err)
			}
			if raw.Snapshot.RequestHeaders["payload-bin"] != wire {
				t.Fatal("debug capture changed")
			}
			for _, item := range []any{sink.result, vu.SnapshotStore.Snapshots()} {
				encoded, err := json.Marshal(item)
				if err != nil || strings.Contains(string(encoded), wire) || strings.Contains(string(encoded), secret) {
					t.Fatalf("ordinary leak=%s err=%v", encoded, err)
				}
			}
			if sink.result.Snapshot.RequestHeaders["payload-bin"] != redactedPayloadValue || vu.SnapshotStore.Snapshots()[0].RequestHeaders["payload-bin"] != redactedPayloadValue {
				t.Fatal("binary metadata was not redacted")
			}
			config := dsl.ScriptConfig{BaseURL: listener.Addr().String()}
			config.GRPC.PlainText = true
			config.GRPC.UseReflection = true
			config.GRPC.Metadata = map[string]string{"payload-bin": value}
			var progress *ReplayActionEvent
			report, err := ReplayScript(context.Background(), &dsl.Script{Name: "binary", Protocol: "grpc", Config: config, Actions: []dsl.Action{{Name: "call", Type: "grpc_unary", Target: "grpc.health.v1.Health/Check", Body: `{}`}}}, ReplayOptions{UnredactedDebug: true, RedactionRules: []RedactionRule{{Pattern: secret}}, DebugController: replayDebugControllerFunc(func(context.Context, ReplayDebugAction) error { return nil }), OnProgress: func(p ReplayProgressEvent) {
				if p.Type == ReplayProgressEventRun {
					progress = p.Event
				}
			}})
			if err != nil || len(report.Events) != 1 || progress == nil {
				t.Fatalf("report=%#v err=%v", report, err)
			}
			for _, event := range []*ReplayActionEvent{&report.Events[0], progress} {
				encoded, err := json.Marshal(event)
				var clone ReplayActionEvent
				if err != nil || json.Unmarshal(encoded, &clone) != nil || clone.Snapshot == nil || clone.Snapshot.RequestHeaders["payload-bin"] != wire {
					t.Fatalf("debug event=%s err=%v", encoded, err)
				}
			}
		})
	}
}

func TestGRPCBinaryMetadataMixedValuesFailClosed(t *testing.T) {
	secret := "supersecret"
	for _, encoding := range []*base64.Encoding{base64.RawStdEncoding, base64.StdEncoding, base64.RawURLEncoding, base64.URLEncoding} {
		values := []string{}
		for _, prefix := range []string{"", "x", "xy"} {
			values = append(values, encoding.EncodeToString([]byte(prefix+secret+"tail")))
		}
		original := strings.Join(values, "\n")
		sink := &captureResultSink{}
		vu := &VUser{Script: &dsl.Script{}, Variables: map[string]string{}, Metrics: NewMetricsCollector(4), SnapshotStore: NewSnapshotStore(4), ResultSink: sink, RedactionConfig: WithSecretValueRedaction(nil, []string{secret})}
		result := &protocol.Result{Snapshot: &protocol.ErrorSnapshot{RequestMethod: "grpc_unary", RequestHeaders: map[string]string{"Payload-Bin": original}, ResponseHeaders: map[string]string{"other-bin": original}}}
		raw := captureUnredactedReplayResult(result)
		if err := vu.recordResult(result, dsl.ErrorHandlingConfig{SnapshotOnError: true}, dsl.LogConfig{}); err != nil {
			t.Fatal(err)
		}
		if raw.Snapshot.RequestHeaders["Payload-Bin"] != original || sink.result.Snapshot.RequestHeaders["Payload-Bin"] != redactedPayloadValue || sink.result.Snapshot.ResponseHeaders["other-bin"] != redactedPayloadValue || vu.SnapshotStore.Snapshots()[0].RequestHeaders["Payload-Bin"] != redactedPayloadValue {
			t.Fatalf("raw=%#v sink=%#v", raw.Snapshot, sink.result.Snapshot)
		}
	}
}
