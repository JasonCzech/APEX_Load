package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	_ "github.com/jasonczech/apexload/pkg/protocol/grpc"
	_ "github.com/jasonczech/apexload/pkg/protocol/java_http"
	_ "github.com/jasonczech/apexload/pkg/protocol/soap"
	_ "github.com/jasonczech/apexload/pkg/protocol/websocket"
)

func TestNativeAdapterDebugFailureSnapshotsAndSinkRedaction(t *testing.T) {
	const secret = "adapter-request-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/xml")
		fmt.Fprint(w, `<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/"><soapenv:Body><soapenv:Fault><faultcode>soapenv:Client</faultcode><faultstring>failed</faultstring></soapenv:Fault></soapenv:Body></soapenv:Envelope>`)
	}))
	defer server.Close()
	for _, kind := range []string{"grpc", "websocket", "soap", "java_http"} {
		for _, debug := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/debug=%t", kind, debug), func(t *testing.T) {
				proto, err := protocol.Get(kind)
				if err != nil {
					t.Fatal(err)
				}
				config := map[string]any{protocol.ReplayUnredactedDebugConfigKey: debug, "base_url": server.URL, "soap_fault_fail_on_fault": true}
				if kind == "grpc" {
					config["base_url"] = "127.0.0.1:1"
					config["grpc_plain_text"] = true
				}
				if kind == "websocket" {
					config["base_url"] = "ws://example.test"
				}
				if err := proto.Init(context.Background(), config); err != nil {
					t.Fatal(err)
				}
				defer proto.Teardown(context.Background())
				action := &protocol.Action{Name: "request", Type: "invalid_action", Method: "POST", Target: "/?token=" + secret, RedactedTarget: "/?token=[REDACTED]", Headers: map[string]string{"Authorization": "Bearer " + secret}, RedactedHeaders: map[string]string{"Authorization": "Bearer [REDACTED]"}, Body: []byte(secret), RedactedBody: []byte("[REDACTED]")}
				if kind == "soap" {
					action.Target = server.URL + "/?token=" + secret
					action.RedactedTarget = server.URL + "/?token=[REDACTED]"
					action.Body = []byte("<Token>" + secret + "</Token>")
					action.SOAP = &protocol.SOAPActionConfig{BodyMode: "generated", Operation: "Login" + secret, Action: "urn:" + secret}
					action.RedactedSOAP = &protocol.SOAPActionConfig{BodyMode: "generated", Operation: "Login[REDACTED]", Action: "urn:[REDACTED]"}
					action.RedactedBody = []byte("<Token>[REDACTED]</Token>")
				}
				if kind == "java_http" {
					action.BodyEncoding = "base64"
				}
				result, err := proto.Execute(context.Background(), action)
				if err != nil || result == nil || result.Success || result.Snapshot == nil {
					t.Fatalf("result=%#v err=%v", result, err)
				}
				snapshot := result.Snapshot
				for _, value := range []string{snapshot.RequestURL, snapshotHeaderValue(snapshot.RequestHeaders, "Authorization"), snapshot.RequestBody} {
					if strings.Contains(value, secret) != debug {
						t.Fatalf("debug=%t snapshot=%#v", debug, snapshot)
					}
				}
				if kind == "soap" && debug && !strings.Contains(snapshot.RequestBody, "Envelope") {
					t.Fatalf("SOAP envelope missing: %q", snapshot.RequestBody)
				}
				if debug {
					sink := &captureResultSink{}
					vu := &VUser{Script: &dsl.Script{}, Variables: map[string]string{}, Metrics: NewMetricsCollector(4), SnapshotStore: NewSnapshotStore(4), ResultSink: sink, RedactionConfig: WithSecretValueRedaction(nil, []string{secret})}
					detached := captureUnredactedReplayResult(result)
					vu.recordResult(result, dsl.ErrorHandlingConfig{}, dsl.LogConfig{})
					encoded, _ := json.Marshal(sink.result)
					if strings.Contains(string(encoded), secret) || detached.Snapshot == nil || !strings.Contains(detached.Snapshot.RequestBody, secret) {
						t.Fatalf("sink or detached snapshot: sink=%s raw=%#v", encoded, detached.Snapshot)
					}
				}
			})
		}
	}
}

func TestAsyncDebugRawNameDoesNotLeakToSink(t *testing.T) {
	const secret = "async-private-name"
	sink := &captureResultSink{}
	vu := &VUser{unredactedDebug: true, Script: &dsl.Script{}, Variables: map[string]string{}, Metrics: NewMetricsCollector(4), SnapshotStore: NewSnapshotStore(4), ResultSink: sink, RedactionConfig: WithSecretValueRedaction(nil, []string{secret})}
	report := &ReplayReport{}
	vu.onAsyncResult = func(result *protocol.Result, raw *ReplayActionEvent) { appendReplayAsyncEvent(vu, report, result, raw) }
	conversation := &asyncConversation{id: secret, pattern: asyncOnce}
	conversation.push(asyncItem{result: &protocol.Result{ActionName: secret, Success: true}})
	if err := vu.applyAsyncResults(context.Background(), conversation); err != nil {
		t.Fatal(err)
	}
	if len(report.Events) != 1 || report.Events[0].ActionName != secret || sink.result == nil || strings.Contains(sink.result.ActionName, secret) {
		t.Fatalf("event=%#v sink=%#v", report.Events, sink.result)
	}
}

func TestReplayAdapterFailureSnapshotsReachDebugProgress(t *testing.T) {
	const secret = "replay-adapter-private"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/xml")
		fmt.Fprint(w, `<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/"><soapenv:Body><soapenv:Fault><faultcode>soapenv:Client</faultcode><faultstring>failed</faultstring></soapenv:Fault></soapenv:Body></soapenv:Envelope>`)
	}))
	defer server.Close()
	for _, kind := range []string{"grpc", "websocket", "soap", "java_http"} {
		for _, debug := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/debug=%t", kind, debug), func(t *testing.T) {
				action := dsl.Action{Name: "request", Type: "invalid_action", Method: "POST", Target: "/?token={{var.session_token}}", Headers: map[string]string{"Authorization": "Bearer {{var.session_token}}"}, Body: "{{var.session_token}}"}
				config := dsl.ScriptConfig{BaseURL: server.URL}
				switch kind {
				case "grpc":
					config.BaseURL = "127.0.0.1:1"
					config.GRPC.PlainText = true
				case "websocket":
					config.BaseURL = "ws://example.test"
				case "soap":
					action.Target = server.URL + "/?token={{var.session_token}}"
					action.Body = "<Token>{{var.session_token}}</Token>"
					action.SOAP = &dsl.SOAPActionConfig{BodyMode: "generated", Operation: "Login{{var.session_token}}", Action: "urn:{{var.session_token}}"}
					action.Headers["SOAPAction"] = "authored-value"
					config.SOAP.FaultHandling.FailOnFault = true
				case "java_http":
					action.BodyEncoding = "base64"
				}
				var progress *ReplayActionEvent
				report, err := ReplayScript(context.Background(), &dsl.Script{Name: "snapshots", Protocol: kind, Config: config, Actions: []dsl.Action{action}}, ReplayOptions{UnredactedDebug: debug, Variables: map[string]string{"session_token": secret}, RedactionRules: []RedactionRule{{Pattern: secret}}, DebugController: replayDebugControllerFunc(func(context.Context, ReplayDebugAction) error { return nil }), OnProgress: func(p ReplayProgressEvent) {
					if p.Type == ReplayProgressEventRun {
						progress = p.Event
					}
				}})
				if err != nil || len(report.Events) != 1 || progress == nil {
					t.Fatalf("report=%#v progress=%#v err=%v", report, progress, err)
				}
				for _, event := range []*ReplayActionEvent{&report.Events[0], progress} {
					encoded, err := json.Marshal(event)
					var decoded ReplayActionEvent
					if err != nil || json.Unmarshal(encoded, &decoded) != nil || decoded.Snapshot == nil {
						t.Fatalf("missing snapshot: %s (%v)", encoded, err)
					}
					for _, value := range []string{decoded.Snapshot.RequestURL, snapshotHeaderValue(decoded.Snapshot.RequestHeaders, "Authorization"), decoded.Snapshot.RequestBody} {
						if strings.Contains(value, secret) != debug {
							t.Fatalf("debug=%t snapshot=%#v", debug, decoded.Snapshot)
						}
					}
					if kind == "soap" && debug && !strings.Contains(decoded.Snapshot.RequestBody, "Envelope") {
						t.Fatalf("SOAP envelope missing: %#v", decoded.Snapshot)
					}
					if kind == "soap" {
						metadata, _ := decoded.ProtocolEvidence["metadata"].(map[string]any)
						soap, _ := metadata["soap"].(map[string]any)
						headers, _ := metadata["headers"].(map[string]any)
						if (debug && (headers["Content-Type"] == nil || headers["Accept"] == nil || !strings.Contains(fmt.Sprint(headers["SOAPAction"]), secret))) || strings.Contains(fmt.Sprint(soap["action"]), secret) != debug || strings.Contains(fmt.Sprint(soap["operation"]), secret) != debug || (!debug && strings.Contains(fmt.Sprint(headers["SOAPAction"]), secret)) {
							t.Fatalf("debug=%t SOAP prepared metadata=%#v", debug, metadata)
						}
					}
				}
			})
		}
	}
}

func TestReplayRawMetadataRestoresWhitespacePaddedTarget(t *testing.T) {
	event := ReplayActionEvent{ProtocolEvidence: map[string]any{"target": "[REDACTED]", "metadata": map[string]any{"headers": map[string]string{"Content-Type": "application/example", "Authorization": "[REDACTED]"}}}}
	action := &protocol.Action{Target: "  raw-secret  ", RedactedTarget: "  [REDACTED]  ", Headers: map[string]string{"Authorization": "raw-secret"}, RedactedHeaders: map[string]string{"Authorization": "[REDACTED]"}}
	applyRawReplayActionEvidence(&event, action)
	headers := event.ProtocolEvidence["metadata"].(map[string]any)["headers"].(map[string]string)
	if event.ProtocolEvidence["target"] != "raw-secret" || headers["Authorization"] != "raw-secret" || headers["Content-Type"] != "application/example" {
		t.Fatalf("target or prepared headers lost: %#v", event.ProtocolEvidence)
	}
}

func snapshotHeaderValue(headers map[string]string, name string) string {
	for key, value := range headers {
		if strings.EqualFold(key, name) {
			return value
		}
	}
	return ""
}
