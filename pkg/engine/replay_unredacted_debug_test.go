package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestReplayUnredactedDebugEventsAndVariables(t *testing.T) {
	const secret = "secret-debug-value-123"
	name := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{Success: true, Extractions: map[string]string{"session_token": secret}, Snapshot: &protocol.ErrorSnapshot{RequestURL: "https://example.test/?token=" + secret, RequestHeaders: map[string]string{"Authorization": "Bearer " + secret}, ResponseBody: secret, ResponseBodyState: "captured"}}, nil
	})
	script := &dsl.Script{Name: "debug", Protocol: name, Actions: []dsl.Action{{Name: "request", Type: "GET", Target: "https://example.test/?token=" + secret, Headers: map[string]string{"Authorization": "Bearer " + secret}}}}
	for _, tc := range []struct {
		name                string
		enabled, controller bool
		want                string
	}{
		{"debug", true, true, secret},
		{"ordinary", false, true, redactedPayloadValue},
		{"requires debugger", true, false, redactedPayloadValue},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := ReplayOptions{UnredactedDebug: tc.enabled}
			if tc.controller {
				opts.DebugController = replayDebugControllerFunc(func(context.Context, ReplayDebugAction) error { return nil })
			}
			var event *ReplayActionEvent
			opts.OnProgress = func(p ReplayProgressEvent) {
				if p.Type == ReplayProgressActionCompleted {
					event = p.Event
				}
			}
			report, err := ReplayScript(context.Background(), script, opts)
			if err != nil {
				t.Fatal(err)
			}
			if event == nil || len(report.Events) != 1 {
				t.Fatalf("missing events: %#v", report)
			}
			if got := event.Extractions["session_token"]; got != tc.want {
				t.Errorf("progress extraction = %q, want %q", got, tc.want)
			}
			if got := report.FinalVariables["session_token"]; got != tc.want {
				t.Errorf("final variable = %q, want %q", got, tc.want)
			}
			if tc.name == "debug" {
				if event.Target != script.Actions[0].Target || event.Headers["Authorization"] != "Bearer "+secret {
					t.Errorf("request details not raw: %#v", event)
				}
				if event.Snapshot == nil || event.Snapshot.ResponseBody != secret || event.Snapshot.RequestHeaders["Authorization"] != "Bearer "+secret {
					t.Errorf("snapshot not raw: %#v", event.Snapshot)
				}
			} else if event.Snapshot != nil && event.Snapshot.ResponseBody == secret {
				t.Error("ordinary replay leaked snapshot")
			}
		})
	}
}

func TestUnredactedDebugDoesNotChangeResultSinkOrLogs(t *testing.T) {
	const secret = "debug-secret-4321"
	redaction := WithSecretValueRedaction(nil, []string{secret})
	sink := &captureResultSink{}
	vu := &VUser{unredactedDebug: true, Script: &dsl.Script{}, Variables: map[string]string{}, Metrics: NewMetricsCollector(4), SnapshotStore: NewSnapshotStore(4), ResultSink: sink, RedactionConfig: redaction}
	result := &protocol.Result{Success: true, Extractions: map[string]string{"session_token": secret}, ResponseBody: secret, Snapshot: &protocol.ErrorSnapshot{ResponseBody: secret, ResponseBodyState: "captured"}}
	action := &protocol.Action{Target: "https://example.test/" + secret, Body: []byte(secret)}
	now := time.Now()
	event, _ := finishReplayProtocolAction(context.Background(), vu, dsl.Action{Name: "request", Type: "GET"}, action, result, nil, now, now, "action", 0, 0, "id", false, true, protocol.ThinkTimeConfig{}, &ReplayReport{}, false, nil, nil, nil)
	if event.Extractions["session_token"] != secret || event.Snapshot == nil || event.Snapshot.ResponseBody != secret {
		t.Fatalf("debugger lost original values: %#v", event)
	}
	if sink.result == nil || sink.result.Extractions["session_token"] == secret || sink.result.Snapshot == nil || sink.result.Snapshot.ResponseBody == secret {
		t.Fatalf("result sink leaked raw values: %#v", sink.result)
	}
	attrs := actionLogAttrs("request", action, &protocol.Result{ResponseBody: secret, ErrorMessage: secret}, dsl.LogConfig{ExtendedOptions: []string{"parameter_substitution", "data_returned"}}, true, redaction)
	if strings.Contains(fmt.Sprint(attrs), secret) {
		t.Fatalf("action log leaked secret: %v", attrs)
	}
}

func TestReplayResultAndErrorRetainsOnlyDebugDiagnosticEvidence(t *testing.T) {
	const secret = "failed-attempt-private-value"
	for _, tc := range []struct {
		name     string
		debug    bool
		prepared bool
	}{
		{name: "sequential-debug", debug: true},
		{name: "concurrent-debug", debug: true, prepared: true},
		{name: "ordinary", debug: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sink := &captureResultSink{}
			vu := &VUser{unredactedDebug: tc.debug, Script: &dsl.Script{}, Variables: map[string]string{}, Metrics: NewMetricsCollector(4), SnapshotStore: NewSnapshotStore(4), ResultSink: sink, RedactionConfig: WithSecretValueRedaction(nil, []string{secret})}
			result := &protocol.Result{Success: false, StatusCode: 503, Extractions: map[string]string{"session_token": secret}, Snapshot: &protocol.ErrorSnapshot{ResponseBody: secret, ResponseBodyState: "captured"}, ProtocolEvidence: map[string]any{"protocol": "http", "detail": secret, "concurrent_group_extractions_omitted": true, "concurrent_group_extractions_omitted_count": 1}}
			var prepared *ReplayActionEvent
			if tc.prepared {
				prepared = captureUnredactedConcurrentResult(result, 2)
			}
			now := time.Now()
			event, ok := finishReplayProtocolAction(context.Background(), vu, dsl.Action{Name: "request", Type: "GET"}, &protocol.Action{Type: "GET"}, result, fmt.Errorf("retry stopped: %s", secret), now, now, "action", 0, 0, "id", false, true, protocol.ThinkTimeConfig{}, &ReplayReport{}, false, nil, prepared, nil)
			if ok || event.Success || len(event.Extractions) != 0 || len(vu.Variables) != 0 || sink.result != nil {
				t.Fatalf("failed attempt was treated as admitted: event=%#v variables=%#v sink=%#v", event, vu.Variables, sink.result)
			}
			if tc.debug {
				if event.StatusCode != 503 || event.ErrorMessage != "retry stopped: "+secret || event.Snapshot == nil || event.Snapshot.ResponseBody != secret || event.ProtocolEvidence["detail"] != secret || event.ProtocolEvidence["concurrent_group_extractions_omitted"] != true {
					t.Fatalf("debug evidence lost: %#v", event)
				}
				outputs, ok := event.UnappliedExtractions, event.UnappliedExtractions != nil
				if !ok || outputs["session_token"] != secret {
					t.Fatalf("unapplied outputs missing: %#v", event.ProtocolEvidence)
				}
				if prepared != nil && prepared.Extractions["session_token"] != secret {
					t.Fatal("debug publication mutated prepared evidence")
				}
			} else {
				encoded, err := json.Marshal(event)
				if err != nil || strings.Contains(string(encoded), secret) {
					t.Fatalf("ordinary error event leaked discarded result: %s (%v)", encoded, err)
				}
			}
		})
	}
}

func TestReplayRetryCancellationRetainsDebugAttemptEvidence(t *testing.T) {
	const secret = "retry-attempt-private-value"
	for _, debug := range []bool{false, true} {
		t.Run(fmt.Sprintf("debug=%t", debug), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			name := registerReplayProtocol(t, func(*protocol.Action) (*protocol.Result, error) {
				cancel()
				return &protocol.Result{Success: false, StatusCode: 503, Extractions: map[string]string{"session_token": secret}, Snapshot: &protocol.ErrorSnapshot{ResponseBody: secret, ResponseBodyState: "captured"}, ProtocolEvidence: map[string]any{"protocol": "http", "detail": secret}}, nil
			})
			script := &dsl.Script{Name: "retry cancellation", Protocol: name, Config: dsl.ScriptConfig{ErrorHandling: dsl.ErrorHandlingConfig{MaxRetries: 1, RetryDelayMs: 1000}}, Actions: []dsl.Action{{Name: "request", Type: "GET"}}}
			report, err := ReplayScript(ctx, script, ReplayOptions{UnredactedDebug: debug, IgnoreThinkTime: true, DebugController: replayDebugControllerFunc(func(context.Context, ReplayDebugAction) error { return nil }), RedactionRules: []RedactionRule{{Pattern: secret}}})
			if err != nil || report == nil || len(report.Events) != 1 {
				t.Fatalf("report=%#v err=%v", report, err)
			}
			event := report.Events[0]
			if event.Success || !strings.Contains(event.ErrorMessage, "context canceled") || len(event.Extractions) != 0 || len(report.FinalVariables) != 0 {
				t.Fatalf("canceled attempt was treated as admitted: %#v variables=%#v", event, report.FinalVariables)
			}
			encoded, err := json.Marshal(event)
			if err != nil || strings.Contains(string(encoded), secret) != debug {
				t.Fatalf("debug=%t event=%s err=%v", debug, encoded, err)
			}
			if debug && (event.Snapshot == nil || event.Snapshot.ResponseBody != secret || event.ProtocolEvidence["detail"] != secret) {
				t.Fatalf("canceled attempt lost its evidence: %#v", event)
			}
		})
	}
}

func TestReplayResultAndErrorBuildsFallbackWhenNativeEvidenceIsAbsent(t *testing.T) {
	vu := &VUser{unredactedDebug: true, Script: &dsl.Script{}, Variables: map[string]string{}, Metrics: NewMetricsCollector(4), SnapshotStore: NewSnapshotStore(4)}
	result := &protocol.Result{Success: false, Extractions: map[string]string{"value": "observed"}}
	now := time.Now()
	event, ok := finishReplayProtocolAction(context.Background(), vu, dsl.Action{Name: "query", Type: "db_query"}, &protocol.Action{Type: "db_query", Target: "database"}, result, context.Canceled, now, now, "action", 0, 0, "id", false, true, protocol.ThinkTimeConfig{}, &ReplayReport{}, false, nil, nil, nil)
	outputs := event.UnappliedExtractions
	if ok || event.Success || event.ProtocolEvidence["status"] != "failed" || outputs["value"] != "observed" || len(event.Extractions) != 0 {
		t.Fatalf("fallback or unapplied output missing: %#v", event)
	}
}

func TestUnredactedDebugFallbackProtocolEvidence(t *testing.T) {
	const secret = "database-private-123"
	for _, debug := range []bool{false, true} {
		t.Run(fmt.Sprintf("debug=%t", debug), func(t *testing.T) {
			vu := &VUser{unredactedDebug: debug, Script: &dsl.Script{}, Variables: map[string]string{}, Metrics: NewMetricsCollector(4), SnapshotStore: NewSnapshotStore(4), RedactionConfig: WithSecretValueRedaction(nil, []string{secret})}
			result := &protocol.Result{Success: true, DependencyType: "database", DependencyTarget: "db/" + secret, ResponseBody: `{"rows":[{"name":"` + secret + `"}]}`}
			action := &protocol.Action{Type: "db_query", Target: "db/" + secret, RedactedTarget: "db/[REDACTED]", Headers: map[string]string{"X-Private": secret}, RedactedHeaders: map[string]string{"X-Private": "[REDACTED]"}}
			now := time.Now()
			event, ok := finishReplayProtocolAction(context.Background(), vu, dsl.Action{Name: "query", Type: "db_query"}, action, result, nil, now, now, "action", 0, 0, "id", false, true, protocol.ThinkTimeConfig{}, &ReplayReport{}, false, nil, nil, nil)
			if !ok || event.ProtocolEvidence["protocol"] != "database" || event.ProtocolEvidence["status"] != "passed" {
				t.Fatalf("missing fallback evidence: %#v", event.ProtocolEvidence)
			}
			encoded, err := json.Marshal(event.ProtocolEvidence)
			if err != nil || strings.Contains(string(encoded), secret) != debug {
				t.Fatalf("debug=%t evidence=%s error=%v", debug, encoded, err)
			}
		})
	}
}

type replayFailingStreamProtocol struct{ streamingProtocolStub }

func (*replayFailingStreamProtocol) ExecuteStream(_ context.Context, action *protocol.Action, emit func(*protocol.Result) error) (*protocol.Result, error) {
	if err := emit(&protocol.Result{ActionName: "child", Success: false, ErrorMessage: "child secret-failure", Snapshot: &protocol.ErrorSnapshot{ResponseBody: "child secret-body", ResponseBodyState: "captured"}}); err != nil {
		return nil, err
	}
	return &protocol.Result{ActionName: action.Name, Success: true, Snapshot: &protocol.ErrorSnapshot{ResponseBody: "parent body", ResponseBodyState: "captured"}}, nil
}

type replayExtractionFailureStreamProtocol struct{ streamingProtocolStub }

func (*replayExtractionFailureStreamProtocol) ExecuteStream(_ context.Context, action *protocol.Action, emit func(*protocol.Result) error) (*protocol.Result, error) {
	if err := emit(&protocol.Result{ActionName: "child", Success: true, Extractions: map[string]string{"value": "too large for VUser"}}); err != nil {
		return nil, err
	}
	return &protocol.Result{ActionName: action.Name, Success: true}, nil
}

func TestUnredactedDebugRetainsStreamedChildFailure(t *testing.T) {
	const name = "replay-debug-failing-stream"
	protocol.Register(name, func() protocol.Protocol { return &replayFailingStreamProtocol{} })
	script := &dsl.Script{Name: "stream", Protocol: name, Actions: []dsl.Action{{Name: "parent", Type: "streaming_run"}}}
	for _, debug := range []bool{false, true} {
		report, err := ReplayScript(context.Background(), script, ReplayOptions{UnredactedDebug: debug, IgnoreThinkTime: true, DebugController: replayDebugControllerFunc(func(context.Context, ReplayDebugAction) error { return nil }), RedactionRules: []RedactionRule{{Pattern: "secret"}}})
		if err != nil || len(report.Events) != 1 {
			t.Fatalf("debug=%t report=%#v error=%v", debug, report, err)
		}
		event := report.Events[0]
		if event.Success || !strings.Contains(event.ErrorMessage, "child") || event.Snapshot == nil || !strings.Contains(event.Snapshot.ResponseBody, "child") {
			t.Fatalf("debug=%t lost child failure: %#v", debug, event)
		}
		if strings.Contains(event.ErrorMessage, "secret") != debug || strings.Contains(event.Snapshot.ResponseBody, "secret") != debug {
			t.Fatalf("debug=%t unexpected child disclosure: %#v", debug, event)
		}
	}
}

func TestUnredactedDebugFailedHTTPSnapshotDoesNotUseScrubbedStoreCopy(t *testing.T) {
	const secret = "failed-http-private-123"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("response " + secret))
	}))
	defer server.Close()
	script := &dsl.Script{Name: "failed HTTP", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: server.URL, ErrorHandling: dsl.ErrorHandlingConfig{SnapshotOnError: true}}, Actions: []dsl.Action{{Name: "request", Type: "GET", Method: "GET", Target: "/failure", Headers: map[string]string{"Authorization": "Bearer " + secret}}}}
	for _, debug := range []bool{false, true} {
		t.Run(fmt.Sprintf("debug=%t", debug), func(t *testing.T) {
			var progress *ReplayActionEvent
			report, err := ReplayScript(context.Background(), script, ReplayOptions{UnredactedDebug: debug, IgnoreThinkTime: true, DebugController: replayDebugControllerFunc(func(context.Context, ReplayDebugAction) error { return nil }), RedactionRules: []RedactionRule{{Pattern: secret}}, OnProgress: func(p ReplayProgressEvent) {
				if p.Type == ReplayProgressActionFailed {
					progress = p.Event
				}
			}})
			if err != nil || report.Success || len(report.Events) != 1 || len(report.Snapshots) != 1 || progress == nil {
				t.Fatalf("report=%#v progress=%#v error=%v", report, progress, err)
			}
			for _, snapshot := range []*protocol.ErrorSnapshot{report.Events[0].Snapshot, report.Snapshots[0], progress.Snapshot} {
				if snapshot == nil || snapshot.ResponseStatus != http.StatusInternalServerError {
					t.Fatalf("missing failure snapshot: %#v", snapshot)
				}
				if strings.Contains(snapshot.ResponseBody, secret) != debug || strings.Contains(snapshot.RequestHeaders["Authorization"], secret) != debug {
					t.Fatalf("debug=%t unexpected failure snapshot disclosure: %#v", debug, snapshot)
				}
			}
		})
	}
}

func TestUnredactedDebugPreservesExtractionAdmissionFailures(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, success := range []bool{false, true} {
			t.Run(fmt.Sprintf("prepared=%t/success=%t", prepared, success), func(t *testing.T) {
				vu := &VUser{unredactedDebug: true, Script: &dsl.Script{}, Variables: map[string]string{}, Metrics: NewMetricsCollector(4), SnapshotStore: NewSnapshotStore(4), extractedVariableBytes: maxVUserExtractionBytes}
				result := &protocol.Result{Success: success, DependencyType: "database", Extractions: map[string]string{"value": "rejected"}, Snapshot: &protocol.ErrorSnapshot{ResponseBody: "response", ResponseBodyState: "captured"}}
				if !success {
					result.ErrorMessage = "original failure"
				}
				var original *ReplayActionEvent
				if prepared {
					original = captureUnredactedReplayResult(result)
				}
				now := time.Now()
				event, ok := finishReplayProtocolAction(context.Background(), vu, dsl.Action{Name: "request", Type: "GET"}, &protocol.Action{}, result, nil, now, now, "action", 0, 0, "id", false, true, protocol.ThinkTimeConfig{}, &ReplayReport{}, false, nil, original, nil)
				if ok || event.Success || (!success && !strings.Contains(event.ErrorMessage, "original failure")) || !strings.Contains(event.ErrorMessage, "extraction retention failed") || len(event.Extractions) != 0 {
					t.Fatalf("admission failure lost: %#v", event)
				}
				if event.Snapshot == nil || event.Snapshot.ResponseBody != "response" {
					t.Fatalf("raw snapshot lost: %#v", event.Snapshot)
				}
				if metadata, ok := event.ProtocolEvidence["metadata"].(map[string]any); ok && metadata["extractions"] != nil {
					t.Fatalf("rejected extractions remained in protocol evidence: %#v", metadata)
				}
				if event.ProtocolEvidence["status"] != "failed" {
					t.Fatalf("protocol evidence lost failure status: %#v", event.ProtocolEvidence)
				}
			})
		}
	}
}

func TestUnredactedDebugPreservesStreamedChildExtractionFailure(t *testing.T) {
	vu := &VUser{unredactedDebug: true, Proto: &replayExtractionFailureStreamProtocol{}, Script: &dsl.Script{}, Variables: map[string]string{}, Metrics: NewMetricsCollector(4), SnapshotStore: NewSnapshotStore(4), extractedVariableBytes: maxVUserExtractionBytes}
	report := &ReplayReport{}
	var progress *ReplayActionEvent
	event, ok, _ := runReplayUnit(context.Background(), vu, replayUnit{action: dsl.Action{Name: "parent", Type: "GET"}, phase: "action"}, nil, false, true, protocol.ThinkTimeConfig{}, report, func(p ReplayProgressEvent) {
		if p.Type == ReplayProgressEventRun {
			progress = p.Event
		}
	}, nil)
	if ok || event.Success || !strings.Contains(event.ErrorMessage, "extraction retention failed") || event.UnappliedExtractions["value"] != "too large for VUser" {
		t.Fatalf("streamed extraction failure lost: %#v", event)
	}
	for _, observed := range []*ReplayActionEvent{&report.Events[0], progress} {
		data, err := json.Marshal(observed)
		var decoded ReplayActionEvent
		if err != nil || json.Unmarshal(data, &decoded) != nil || decoded.UnappliedExtractions["value"] != "too large for VUser" || len(decoded.Extractions) != 0 {
			t.Fatalf("streamed output lost from serialized event: %s (%v)", data, err)
		}
	}
}

func TestReplayExtractionFailureRemainsVisibleAfterLongOriginalError(t *testing.T) {
	event := &ReplayActionEvent{ErrorMessage: strings.Repeat("x", maxReplayErrorBytes)}
	applyReplayExtractionFailure(event, fmt.Errorf("capacity exceeded"))
	if len(event.ErrorMessage) > maxReplayErrorBytes || !strings.Contains(event.ErrorMessage, "extraction retention failed: capacity exceeded") {
		t.Fatalf("admission failure omitted from %d byte error", len(event.ErrorMessage))
	}
}

func TestUnredactedDebugPreservesInvalidExtractionFailure(t *testing.T) {
	vu := &VUser{unredactedDebug: true, Script: &dsl.Script{}, Variables: map[string]string{}, Metrics: NewMetricsCollector(4), SnapshotStore: NewSnapshotStore(4)}
	result := &protocol.Result{Success: true, Extractions: map[string]string{"": "invalid"}}
	now := time.Now()
	event, ok := finishReplayProtocolAction(context.Background(), vu, dsl.Action{Name: "request", Type: "GET"}, &protocol.Action{}, result, nil, now, now, "action", 0, 0, "id", false, true, protocol.ThinkTimeConfig{}, &ReplayReport{}, false, nil, nil, nil)
	if ok || event.Success || !strings.Contains(event.ErrorMessage, "extraction retention failed: extraction name is required") || len(event.Extractions) != 0 {
		t.Fatalf("invalid extraction failure lost: %#v", event)
	}
}

func TestUnredactedDebugConcurrentExtractions(t *testing.T) {
	const secret = "concurrent-secret-123"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Session", secret+r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	child := func(name string) dsl.Action {
		return dsl.Action{Name: name, Type: "GET", Target: "/" + name, Extractors: []dsl.Extractor{{Name: "session_token_" + name, Source: "header", Type: "header", Expression: "X-Session"}}}
	}
	group := dsl.Action{Name: "group", Type: "http_sequence", Params: map[string]any{"execution_mode": "concurrent", "max_concurrency": 2, "requests": []dsl.Action{child("left"), child("right")}}}
	script := &dsl.Script{Name: "concurrent", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: server.URL}, Actions: []dsl.Action{group}}
	report, err := ReplayScript(context.Background(), script, ReplayOptions{UnredactedDebug: true, IgnoreThinkTime: true, DebugController: replayDebugControllerFunc(func(context.Context, ReplayDebugAction) error { return nil })})
	if err != nil || !report.Success || len(report.Events) != 2 {
		t.Fatalf("report = %#v, error = %v", report, err)
	}
	for _, event := range report.Events {
		if got := event.Extractions["session_token_"+event.ActionName]; got != secret+"/"+event.ActionName {
			t.Errorf("%s extraction = %q", event.ActionName, got)
		}
	}
}

func TestUnredactedDebugConcurrentFallbackProtocolEvidence(t *testing.T) {
	const secret = "concurrent-evidence-private-123"
	const name = "concurrent-fallback-debug"
	protocol.Register(name, func() protocol.Protocol {
		return &concurrentReplayTestProtocol{replayTestProtocol{execute: func(action *protocol.Action) (*protocol.Result, error) {
			return &protocol.Result{Success: true, DependencyType: "database", DependencyTarget: "db/" + secret, ResponseBody: `{"rows":[{"name":"` + secret + `"}]}`}, nil
		}}}
	})
	child := dsl.Action{Name: "child", Type: "GET", Target: "/" + secret}
	group := dsl.Action{Name: "group", Type: "http_sequence", Params: map[string]any{"execution_mode": "concurrent", "max_concurrency": 1, "requests": []dsl.Action{child}}}
	script := &dsl.Script{Name: "concurrent fallback", Protocol: name, Actions: []dsl.Action{group}}
	for _, debug := range []bool{false, true} {
		report, err := ReplayScript(context.Background(), script, ReplayOptions{UnredactedDebug: debug, IgnoreThinkTime: true, DebugController: replayDebugControllerFunc(func(context.Context, ReplayDebugAction) error { return nil }), RedactionRules: []RedactionRule{{Pattern: secret}}})
		if err != nil || len(report.Events) != 1 || (debug && report.Events[0].ProtocolEvidence["protocol"] != "database") {
			t.Fatalf("debug=%t report=%#v error=%v", debug, report, err)
		}
		if _, ok := report.Events[0].ProtocolEvidence["concurrent_group_duration_ms"]; !ok {
			t.Fatalf("debug=%t missing concurrent timing receipt: %#v", debug, report.Events[0].ProtocolEvidence)
		}
		encoded, err := json.Marshal(report.Events[0].ProtocolEvidence)
		if err != nil || strings.Contains(string(encoded), secret) != debug {
			t.Fatalf("debug=%t evidence=%s error=%v", debug, encoded, err)
		}
	}
}

type failingReplayIterationProtocol struct{ replayTestProtocol }

func (*failingReplayIterationProtocol) BeginIteration(context.Context) error {
	return fmt.Errorf("iteration secret=iteration-private-123")
}

func TestUnredactedDebugIterationFailureAndFinalization(t *testing.T) {
	const secret = "iteration-private-123"
	const name = "replay-iteration-debug-secret"
	protocol.Register(name, func() protocol.Protocol {
		return &failingReplayIterationProtocol{replayTestProtocol{execute: func(*protocol.Action) (*protocol.Result, error) { return &protocol.Result{Success: true}, nil }}}
	})
	script := &dsl.Script{Name: "iteration", Protocol: name, Actions: []dsl.Action{{Name: "a", Type: "GET"}}}
	for _, enabled := range []bool{false, true} {
		opts := ReplayOptions{UnredactedDebug: enabled, DebugController: replayDebugControllerFunc(func(context.Context, ReplayDebugAction) error { return nil }), RedactionRules: []RedactionRule{{Pattern: secret}}}
		report, err := ReplayScript(context.Background(), script, opts)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(report.Error, secret) != enabled {
			t.Errorf("enabled=%t iteration error = %q", enabled, report.Error)
		}
		redaction := WithSecretValueRedaction(nil, []string{secret})
		vu := &VUser{unredactedDebug: enabled, Variables: map[string]string{}, Metrics: NewMetricsCollector(4), RedactionConfig: redaction}
		result := vu.recordTransactionSpan(secret, time.Second, false, &protocol.Result{ActionName: secret, ErrorMessage: secret}, time.Now())
		final := &ReplayReport{}
		appendReplaySourceTransactionFinalizations(final, []*protocol.Result{result}, redaction, nil, enabled)
		if len(final.Events) != 1 || strings.Contains(final.Events[0].ErrorMessage, secret) != enabled || strings.Contains(final.Error, secret) != enabled {
			t.Errorf("enabled=%t finalization=%#v", enabled, final)
		}
		async := &ReplayReport{}
		appendReplayAsyncEvent(vu, async, &protocol.Result{Success: false, ErrorMessage: secret})
		if len(async.Events) != 1 || strings.Contains(async.Events[0].ErrorMessage, secret) != enabled {
			t.Errorf("enabled=%t async=%#v", enabled, async)
		}
	}
}

type concurrentReplayTestProtocol struct{ replayTestProtocol }

func (*concurrentReplayTestProtocol) SharedSessionConcurrentSafe() bool                  { return true }
func (*concurrentReplayTestProtocol) ValidateSharedSessionAction(*protocol.Action) error { return nil }

func TestUnredactedDebugConcurrentDuplicateOutputsPreserved(t *testing.T) {
	const name = "concurrent-duplicate-debug"
	protocol.Register(name, func() protocol.Protocol {
		return &concurrentReplayTestProtocol{replayTestProtocol{execute: func(action *protocol.Action) (*protocol.Result, error) {
			return &protocol.Result{Success: true, Extractions: map[string]string{"session_token": "duplicate-secret/" + action.Name}}, nil
		}}}
	})
	child := func(name string) dsl.Action { return dsl.Action{Name: name, Type: "GET", Target: "/" + name} }
	group := dsl.Action{Name: "group", Type: "http_sequence", Params: map[string]any{"execution_mode": "concurrent", "max_concurrency": 2, "requests": []dsl.Action{child("one"), child("two")}}}
	script := &dsl.Script{Name: "duplicate", Protocol: name, Actions: []dsl.Action{group}}
	report, err := ReplayScript(context.Background(), script, ReplayOptions{UnredactedDebug: true, IgnoreThinkTime: true, DebugController: replayDebugControllerFunc(func(context.Context, ReplayDebugAction) error { return nil })})
	if err != nil || len(report.Events) != 2 {
		t.Fatalf("report = %#v, error=%v", report, err)
	}
	for _, event := range report.Events {
		if got := event.Extractions["session_token"]; got != "duplicate-secret/"+event.ActionName {
			t.Errorf("event %s extraction = %q", event.ActionName, got)
		}
	}
}

func TestUnredactedConcurrentResultSharesGroupBudget(t *testing.T) {
	const count = 256
	allowance := maxConcurrentRichEvidenceBytes / count
	big := strings.Repeat("x", 2<<20)
	result := &protocol.Result{ErrorMessage: big, HTML: big, ScreenshotBase64: big, PageURL: big, Extractions: map[string]string{"token": big}, ProtocolEvidence: map[string]any{"text": big}, Snapshot: &protocol.ErrorSnapshot{RequestBody: big, ResponseBody: big, ResponseBodyState: "captured"}}
	captured := captureUnredactedConcurrentResult(result, count)
	if captured == nil {
		t.Fatal("missing debugger projection")
	}
	encoded, err := json.Marshal(captured)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > allowance {
		t.Fatalf("retained child has %d bytes, allowance %d", len(encoded), allowance)
	}
	if captured.Snapshot == nil || len(captured.Snapshot.ResponseBody) >= len(big) {
		t.Fatal("snapshot did not respect child allowance")
	}
	if len(captured.HTML) >= len(big) || len(captured.Screenshot) >= len(big) || len(captured.Extractions) != 0 {
		t.Fatal("large raw fields were not bounded")
	}
	if result.HTML != big || result.Snapshot.ResponseBody != big {
		t.Fatal("projection modified original result")
	}
}

func TestUnredactedConcurrentResultReportsOmittedExtractions(t *testing.T) {
	const count = 256
	result := &protocol.Result{Success: true, Extractions: map[string]string{
		"small":     "visible",
		"large_one": strings.Repeat("a", 12<<10),
		"large_two": strings.Repeat("b", 9<<10),
	}, ProtocolEvidence: map[string]any{"protocol": "http"}}
	if err := protocol.ValidateExtractions(result.Extractions); err != nil {
		t.Fatalf("test requires valid extractions: %v", err)
	}
	captured := captureUnredactedConcurrentResult(result, count)
	if len(captured.Extractions) != 1 || captured.Extractions["small"] != "visible" {
		t.Fatalf("retained extractions = %#v", captured.Extractions)
	}
	if captured.ProtocolEvidence["concurrent_group_extractions_omitted"] != true || captured.ProtocolEvidence["concurrent_group_extractions_omitted_count"] != 2 || captured.ProtocolEvidence["protocol"] != "http" {
		t.Fatalf("missing omission receipt or original evidence: %#v", captured.ProtocolEvidence)
	}
	if len(result.Extractions) != 3 || len(result.ProtocolEvidence) != 1 {
		t.Fatal("projection modified the source result")
	}
	allRetained := captureUnredactedConcurrentResult(result, 1)
	if len(allRetained.Extractions) != 3 || allRetained.ProtocolEvidence["concurrent_group_extractions_omitted"] != nil {
		t.Fatalf("complete extraction evidence incorrectly marked omitted: %#v", allRetained)
	}
}

func TestUnredactedConcurrentOmissionReceiptReachesReplayEvent(t *testing.T) {
	const name = "concurrent-omission-receipt-debug"
	const extractionName = "large_value"
	large := strings.Repeat("a", 12<<10)
	protocol.Register(name, func() protocol.Protocol {
		return &concurrentReplayTestProtocol{replayTestProtocol{execute: func(action *protocol.Action) (*protocol.Result, error) {
			if action.Name == "first" {
				return &protocol.Result{Success: true, Extractions: map[string]string{extractionName: large}}, nil
			}
			return &protocol.Result{Success: true}, nil
		}}}
	})
	requests := make([]dsl.Action, 256)
	for index := range requests {
		requests[index] = dsl.Action{Name: fmt.Sprintf("child-%d", index), Type: "GET", Target: "/"}
	}
	requests[0].Name = "first"
	script := &dsl.Script{Name: "concurrent", Protocol: name, Actions: []dsl.Action{{
		Name: "group", Type: "http_sequence", Params: map[string]any{"execution_mode": "concurrent", "max_concurrency": 16, "requests": requests},
	}}}
	report, err := ReplayScript(context.Background(), script, ReplayOptions{UnredactedDebug: true, IgnoreThinkTime: true, DebugController: replayDebugControllerFunc(func(context.Context, ReplayDebugAction) error { return nil })})
	if err != nil || report == nil {
		t.Fatalf("concurrent report: %#v error=%v", report, err)
	}
	if !report.Success || len(report.Events) != len(requests) {
		t.Fatalf("concurrent report: success=%v events=%d", report.Success, len(report.Events))
	}
	event := report.Events[0]
	if len(event.Extractions) != 0 || event.ProtocolEvidence["concurrent_group_extractions_omitted"] != true || event.ProtocolEvidence["concurrent_group_extractions_omitted_count"] != 1 {
		t.Fatalf("missing event omission receipt: %#v", event.ProtocolEvidence)
	}
	if report.FinalVariables[extractionName] != large {
		t.Fatal("valid extraction was not retained in VUser variables")
	}
}

func TestUnredactedReplayResultBoundsUntrustedEvidenceBeforeCloning(t *testing.T) {
	large := strings.Repeat("x", 8<<20)
	entries := make([]protocol.ConsoleEntry, 100_000)
	for index := range entries {
		entries[index].Text = large
	}
	result := &protocol.Result{
		ErrorMessage:     large,
		Extractions:      map[string]string{"oversized": large},
		HTML:             large,
		ScreenshotBase64: large,
		ConsoleEntries:   entries,
		DOMSummary:       &protocol.DOMSummary{VisibleText: []string{large}},
		Snapshot:         &protocol.ErrorSnapshot{ResponseBody: large, ResponseBodyState: "captured"},
	}
	got := captureUnredactedReplayResult(result)
	if got == nil || len(got.Extractions) != 0 || len(got.Console) >= len(entries) {
		t.Fatalf("unbounded projection or accepted invalid extraction: %#v", got)
	}
	if len(got.ErrorMessage) > maxReplayErrorBytes || len(got.HTML) >= len(large) || got.Snapshot == nil || len(got.Snapshot.ResponseBody) >= len(large) {
		t.Fatal("large evidence was not bounded")
	}
	encoded, err := json.Marshal(got)
	if err != nil || len(encoded) > maxConcurrentRichEvidenceBytes {
		t.Fatalf("projection exceeded its shared evidence budget: %d bytes, error=%v", len(encoded), err)
	}
	if len(result.ConsoleEntries) != len(entries) || result.HTML != large || result.Extractions["oversized"] != large {
		t.Fatal("bounded capture mutated the source result")
	}
}

func TestReplayFailedAdapterAndClassificationPreserveDebugEvidence(t *testing.T) {
	const secret = "diagnostic-secret-result"
	for _, concurrent := range []bool{false, true} {
		for _, adapterError := range []bool{false, true} {
			for _, debug := range []bool{false, true} {
				t.Run(fmt.Sprintf("concurrent=%t/error=%t/debug=%t", concurrent, adapterError, debug), func(t *testing.T) {
					name := "test-diagnostic-" + strings.NewReplacer("/", "-", "=", "-").Replace(t.Name())
					protocol.Register(name, func() protocol.Protocol {
						return &concurrentReplayTestProtocol{replayTestProtocol{execute: func(*protocol.Action) (*protocol.Result, error) {
							result := &protocol.Result{Success: false, StatusCode: 503, ExtractionsIncomplete: !adapterError, Extractions: map[string]string{"session_token": secret}, Snapshot: &protocol.ErrorSnapshot{ResponseBody: secret, ResponseBodyState: "captured"}, ProtocolEvidence: map[string]any{"detail": secret}}
							if adapterError {
								return result, fmt.Errorf("adapter failed: %s", secret)
							}
							return result, nil
						}}}
					})
					child := dsl.Action{Name: "child", Type: "GET", Target: "/"}
					actions := []dsl.Action{child}
					if concurrent {
						actions = []dsl.Action{{Name: "group", Type: "http_sequence", Params: map[string]any{"execution_mode": "concurrent", "requests": []dsl.Action{child}}}}
					}
					script := &dsl.Script{Name: "diagnostic", Protocol: name, Actions: actions}
					var progress *ReplayActionEvent
					report, err := ReplayScript(context.Background(), script, ReplayOptions{UnredactedDebug: debug, IgnoreThinkTime: true, DebugController: replayDebugControllerFunc(func(context.Context, ReplayDebugAction) error { return nil }), RedactionRules: []RedactionRule{{Pattern: secret}}, OnProgress: func(p ReplayProgressEvent) {
						if p.Type == ReplayProgressActionFailed {
							progress = p.Event
						}
					}})
					if err != nil || len(report.Events) != 1 || progress == nil {
						t.Fatalf("report=%#v progress=%#v err=%v", report, progress, err)
					}
					for _, event := range []*ReplayActionEvent{&report.Events[0], progress} {
						encoded, marshalErr := json.Marshal(event)
						if marshalErr != nil || strings.Contains(string(encoded), secret) != debug {
							t.Fatalf("debug=%t event=%s err=%v", debug, encoded, marshalErr)
						}
						if event.Success || len(event.Extractions) != 0 || len(report.FinalVariables) != 0 {
							t.Fatalf("failure became applied: %#v variables=%#v", event, report.FinalVariables)
						}
						if debug && (event.Snapshot == nil || event.Snapshot.ResponseBody != secret || event.ProtocolEvidence["detail"] != secret || event.UnappliedExtractions["session_token"] != secret) {
							t.Fatalf("diagnostic missing: %#v", event)
						}
					}
				})
			}
		}
	}
}

func TestReplayUnappliedExtractionsSurviveReportAndProgressClones(t *testing.T) {
	values := map[string]string{"large": strings.Repeat("x", 12<<10)}
	for i := 0; i < 150; i++ {
		values[fmt.Sprintf("item-%03d", i)] = "value"
	}
	name := registerReplayProtocol(t, func(*protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{Extractions: values}, fmt.Errorf("adapter error")
	})
	var progress *ReplayActionEvent
	report, err := ReplayScript(context.Background(), &dsl.Script{Name: "bounded", Protocol: name, Actions: []dsl.Action{{Name: "child", Type: "GET"}}}, ReplayOptions{UnredactedDebug: true, IgnoreThinkTime: true, DebugController: replayDebugControllerFunc(func(context.Context, ReplayDebugAction) error { return nil }), OnProgress: func(p ReplayProgressEvent) {
		if p.Type == ReplayProgressActionFailed {
			progress = p.Event
		}
	}})
	if err != nil || len(report.Events) != 1 || progress == nil {
		t.Fatalf("report=%#v progress=%#v err=%v", report, progress, err)
	}
	for _, event := range []*ReplayActionEvent{&report.Events[0], progress} {
		data, err := json.Marshal(event)
		var restored ReplayActionEvent
		if err != nil || json.Unmarshal(data, &restored) != nil || len(restored.UnappliedExtractions) != len(values) || restored.UnappliedExtractions["large"] != values["large"] || restored.UnappliedExtractionsOmittedCount != 0 {
			t.Fatalf("unapplied outputs truncated: count=%d large=%d omitted=%d err=%v", len(restored.UnappliedExtractions), len(restored.UnappliedExtractions["large"]), restored.UnappliedExtractionsOmittedCount, err)
		}
	}
}

func TestReplayUnappliedExtractionOmissionCountSurvivesClone(t *testing.T) {
	result := &protocol.Result{Extractions: map[string]string{"small": "visible", "large": strings.Repeat("x", 12<<10)}}
	event := captureUnredactedConcurrentResult(result, 256)
	moveUnappliedReplayExtractions(event)
	cloned := cloneReplayActionEvent(*event)
	if cloned.UnappliedExtractions["small"] != "visible" || cloned.UnappliedExtractionsOmittedCount != 1 || len(cloned.Extractions) != 0 {
		t.Fatalf("missing bounded output receipt: %#v", cloned)
	}
	compact := BoundReplayActionEventForRetention(*cloned, false)
	if len(compact.UnappliedExtractions) != 0 || compact.UnappliedExtractionsOmittedCount != 2 {
		t.Fatalf("compact retention omitted output without receipt: %#v", compact)
	}
}

func TestReplayClassificationFailureNeverPublishesRawResultToSink(t *testing.T) {
	const secret = "classification-sink-secret"
	for _, adapterError := range []bool{false, true} {
		t.Run(fmt.Sprintf("adapterError=%t", adapterError), func(t *testing.T) {
			sink := &captureResultSink{}
			vu := &VUser{unredactedDebug: true, Script: &dsl.Script{}, Variables: map[string]string{}, Metrics: NewMetricsCollector(4), SnapshotStore: NewSnapshotStore(4), ResultSink: sink, Proto: &replayTestProtocol{execute: func(*protocol.Action) (*protocol.Result, error) {
				result := &protocol.Result{Success: false, ExtractionsIncomplete: !adapterError, Extractions: map[string]string{"token": secret}, Snapshot: &protocol.ErrorSnapshot{ResponseBody: secret}, ProtocolEvidence: map[string]any{"detail": secret}}
				if adapterError {
					return result, fmt.Errorf("error %s", secret)
				}
				return result, nil
			}}}
			event, ok := runReplayAction(context.Background(), vu, dsl.Action{Name: "a", Type: "GET"}, "action", 0, 0, "id", false, true, protocol.ThinkTimeConfig{}, &ReplayReport{}, false)
			if ok || event.UnappliedExtractions["token"] != secret {
				t.Fatalf("missing diagnostic event: %#v", event)
			}
			if adapterError && sink.result != nil {
				t.Fatalf("discarded adapter result reached sink: %#v", sink.result)
			}
			if !adapterError {
				encoded, err := json.Marshal(sink.result)
				if err != nil || sink.result == nil || strings.Contains(string(encoded), secret) {
					t.Fatalf("sink leaked diagnostic evidence: %s (%v)", encoded, err)
				}
			}
		})
	}
}

func TestReplayRetainsUnappliedOnlyRichEvidence(t *testing.T) {
	report := &ReplayReport{}
	event := appendReplayReportEvent(report, ReplayActionEvent{UnappliedExtractions: map[string]string{"value": "observed"}})
	if event.UnappliedExtractions["value"] != "observed" || len(report.Events) != 1 || report.Events[0].UnappliedExtractions["value"] != "observed" {
		t.Fatalf("unapplied-only evidence discarded: %#v", event)
	}
}

type replayMultiChildDiagnosticProtocol struct {
	streamingProtocolStub
	terminalError bool
}

func (p *replayMultiChildDiagnosticProtocol) ExecuteStream(_ context.Context, action *protocol.Action, emit func(*protocol.Result) error) (*protocol.Result, error) {
	for _, child := range []*protocol.Result{
		{ActionName: "first", Success: true, Extractions: map[string]string{"first_value": "first-raw"}},
		{ActionName: "rejected", Success: false, ErrorMessage: "initial-raw-error", ExtractionsIncomplete: true, Extractions: map[string]string{"rejected_value": "rejected-raw"}},
		{ActionName: "last", Success: false, ErrorMessage: "later-raw-error", Extractions: map[string]string{"last_value": "last-raw"}},
	} {
		if err := emit(child); err != nil {
			return nil, err
		}
	}
	if p.terminalError {
		return nil, fmt.Errorf("terminal-raw-error")
	}
	return &protocol.Result{ActionName: action.Name, Success: true}, nil
}

func TestReplayStreamedChildrenPreserveAppliedAndRejectedOutputs(t *testing.T) {
	for _, terminalError := range []bool{false, true} {
		t.Run(fmt.Sprintf("terminalError=%t", terminalError), func(t *testing.T) {
			name := fmt.Sprintf("stream-diagnostics-%t", terminalError)
			protocol.Register(name, func() protocol.Protocol { return &replayMultiChildDiagnosticProtocol{terminalError: terminalError} })
			var progress *ReplayActionEvent
			report, err := ReplayScript(context.Background(), &dsl.Script{Name: "stream", Protocol: name, Actions: []dsl.Action{{Name: "parent", Type: "streaming_run"}}}, ReplayOptions{UnredactedDebug: true, IgnoreThinkTime: true, DebugController: replayDebugControllerFunc(func(context.Context, ReplayDebugAction) error { return nil }), OnProgress: func(p ReplayProgressEvent) {
				if p.Type == ReplayProgressEventRun {
					progress = p.Event
				}
			}})
			if err != nil || len(report.Events) != 1 || progress == nil {
				t.Fatalf("report=%#v progress=%#v err=%v", report, progress, err)
			}
			for _, event := range []*ReplayActionEvent{&report.Events[0], progress} {
				data, err := json.Marshal(event)
				var decoded ReplayActionEvent
				if err != nil || json.Unmarshal(data, &decoded) != nil || len(decoded.StreamedChildren) != 3 {
					t.Fatalf("children missing: %s (%v)", data, err)
				}
				children := decoded.StreamedChildren
				if children[0].Extractions["first_value"] != "first-raw" || children[1].UnappliedExtractions["rejected_value"] != "rejected-raw" || children[2].Extractions["last_value"] != "last-raw" || children[2].ErrorMessage != "later-raw-error" || children[2].CredentialClassificationError == "" {
					t.Fatalf("stream diagnostics incorrect: %s", data)
				}
				if terminalError && decoded.ErrorMessage != "terminal-raw-error" {
					t.Fatalf("terminal error lost: %s", data)
				}
			}
			if report.FinalVariables["first_value"] != "first-raw" || report.FinalVariables["last_value"] != "last-raw" || report.FinalVariables["rejected_value"] != "" {
				t.Fatalf("live variables incorrect: %#v", report.FinalVariables)
			}
		})
	}
}

func TestReplayDebugEvidencePreservesLargeValuesAndReportsOmissions(t *testing.T) {
	long := strings.Repeat("x", 12<<10)
	frames := make([]any, 600)
	for i := range frames {
		frames[i] = fmt.Sprintf("frame-%d", i)
	}
	name := registerReplayProtocol(t, func(*protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{Success: true, ProtocolEvidence: map[string]any{"payload": long, "frames": frames}}, nil
	})
	var progress *ReplayActionEvent
	report, err := ReplayScript(context.Background(), &dsl.Script{Name: "evidence", Protocol: name, Actions: []dsl.Action{{Name: "wait", Type: "GET"}}}, ReplayOptions{UnredactedDebug: true, DebugController: replayDebugControllerFunc(func(context.Context, ReplayDebugAction) error { return nil }), OnProgress: func(p ReplayProgressEvent) {
		if p.Type == ReplayProgressEventRun {
			progress = p.Event
		}
	}})
	if err != nil || len(report.Events) != 1 || progress == nil {
		t.Fatalf("report=%#v progress=%#v err=%v", report, progress, err)
	}
	for _, event := range []*ReplayActionEvent{&report.Events[0], progress} {
		data, err := json.Marshal(event)
		var decoded ReplayActionEvent
		if err != nil || json.Unmarshal(data, &decoded) != nil {
			t.Fatalf("serialization: %s (%v)", data, err)
		}
		gotFrames, _ := decoded.ProtocolEvidence["frames"].([]any)
		if decoded.ProtocolEvidence["payload"] != long || len(gotFrames) != 512 || decoded.ProtocolEvidenceOmittedCount != 88 {
			t.Fatalf("evidence truncated without receipt: frames=%d payload=%d omitted=%d", len(gotFrames), len(fmt.Sprint(decoded.ProtocolEvidence["payload"])), decoded.ProtocolEvidenceOmittedCount)
		}
	}
}

func TestAsyncDebugPreservesOriginalErrorAfterClassificationFailure(t *testing.T) {
	vu := &VUser{unredactedDebug: true, Script: &dsl.Script{}, Variables: map[string]string{}, Metrics: NewMetricsCollector(4), SnapshotStore: NewSnapshotStore(4)}
	report := &ReplayReport{}
	vu.onAsyncResult = func(result *protocol.Result, raw *ReplayActionEvent) { appendReplayAsyncEvent(vu, report, result, raw) }
	conversation := &asyncConversation{id: "test", pattern: asyncOnce}
	conversation.push(asyncItem{result: &protocol.Result{Success: false, ExtractionsIncomplete: true, ErrorMessage: "first-raw-error"}})
	conversation.push(asyncItem{result: &protocol.Result{Success: false, ErrorMessage: "second-raw-error"}})
	if err := vu.applyAsyncResults(context.Background(), conversation); err != nil {
		t.Fatal(err)
	}
	if len(report.Events) != 2 || report.Events[1].ErrorMessage != "second-raw-error" || report.Events[1].CredentialClassificationError == "" {
		t.Fatalf("async raw error lost: %#v", report.Events)
	}
}

func TestReplayDebugRetainsOriginalErrorAfterPriorClassificationFailure(t *testing.T) {
	const secret = "original-private-error"
	sink := &captureResultSink{}
	vu := &VUser{unredactedDebug: true, Script: &dsl.Script{}, Variables: map[string]string{}, Metrics: NewMetricsCollector(4), SnapshotStore: NewSnapshotStore(4), ResultSink: sink, RedactionConfig: WithSecretValueRedaction(nil, []string{secret})}
	vu.failRuntimeCredentialRedaction(fmt.Errorf("classification unavailable"))
	now := time.Now()
	event, ok := finishReplayProtocolAction(context.Background(), vu, dsl.Action{Name: "request", Type: "GET"}, &protocol.Action{Type: "GET"}, &protocol.Result{Success: false, ErrorMessage: secret}, nil, now, now, "action", 0, 0, "id", false, true, protocol.ThinkTimeConfig{}, &ReplayReport{}, false, nil, nil, nil)
	if ok || event.ErrorMessage != secret || event.CredentialClassificationError == "" {
		t.Fatalf("raw error or classification reason lost: %#v", event)
	}
	data, err := json.Marshal(sink.result)
	if err != nil || strings.Contains(string(data), secret) {
		t.Fatalf("sink leaked raw error: %s (%v)", data, err)
	}
}

func TestReplayDebugStreamedChildrenRemainBoundedAndDetached(t *testing.T) {
	children := make([]ReplayStreamedChildEvidence, maxReplayStreamedChildren+2)
	for i := range children {
		children[i] = ReplayStreamedChildEvidence{ActionName: fmt.Sprintf("child-%d", i), Extractions: map[string]string{"token": "original"}}
	}
	event := ReplayActionEvent{StreamedChildren: children}
	cloned := BoundReplayActionEventForRetention(event, true)
	children[0].Extractions["token"] = "changed"
	if len(cloned.StreamedChildren) != maxReplayStreamedChildren || cloned.StreamedChildrenOmittedCount != 2 || cloned.StreamedChildren[0].Extractions["token"] != "original" {
		t.Fatalf("bounded clone incorrect: count=%d omitted=%d", len(cloned.StreamedChildren), cloned.StreamedChildrenOmittedCount)
	}
}
