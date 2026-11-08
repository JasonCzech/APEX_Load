package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/recordbridge"
)

func TestHandleStartScriptDebugValidatesIterations(t *testing.T) {
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
	})
	server := NewServer(ServerConfig{})
	for index, tc := range []struct {
		iterations int
		wantStatus int
	}{
		{iterations: -1, wantStatus: http.StatusBadRequest},
		{iterations: 101, wantStatus: http.StatusBadRequest},
		{iterations: 0, wantStatus: http.StatusOK},
		{iterations: 1, wantStatus: http.StatusOK},
		{iterations: 100, wantStatus: http.StatusOK},
	} {
		t.Run(fmt.Sprint(tc.iterations), func(t *testing.T) {
			sessionID := fmt.Sprintf("11111111-1111-4111-8111-%012d", 200+index)
			body, _ := json.Marshal(map[string]any{
				"project_id":        "project-debug-iterations",
				"replay_session_id": sessionID,
				"start_paused":      true,
				"iterations":        tc.iterations,
				"script": map[string]any{
					"name": "Iterations", "version": "1.0.0", "protocol": protocolName,
					"config":  map[string]any{"base_url": "https://example.com"},
					"actions": []map[string]any{{"name": "Get home", "type": "GET", "method": "GET", "target": "/"}},
				},
			})
			req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/debug/sessions", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			server.mux.ServeHTTP(resp, req)
			if resp.Code != tc.wantStatus {
				t.Fatalf("iterations=%d status = %d, want %d body=%s", tc.iterations, resp.Code, tc.wantStatus, resp.Body.String())
			}
			if tc.wantStatus == http.StatusBadRequest {
				if !strings.Contains(resp.Body.String(), scriptDebugIterationsError) {
					t.Fatalf("400 body = %s, want %q", resp.Body.String(), scriptDebugIterationsError)
				}
				return
			}
			stop := httptest.NewRecorder()
			server.mux.ServeHTTP(stop, httptest.NewRequest(http.MethodPost, "/api/v1/script-debug/sessions/"+sessionID+"/stop", nil))
			if stop.Code != http.StatusOK {
				t.Fatalf("stop status = %d body=%s", stop.Code, stop.Body.String())
			}
		})
	}
}

// The requested iteration count reaches the engine, and a default session
// records the HAR capture options the engine applies.
func TestScriptDebugManagerRunsRequestedIterationsWithRecordedHAROptions(t *testing.T) {
	var mu sync.Mutex
	executed := 0
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		mu.Lock()
		executed++
		mu.Unlock()
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
	})
	manager := NewScriptDebugManager(NewHub())
	manager.Start("", &dsl.Script{
		Name: "Iterations", Protocol: protocolName, Version: "1.0.0",
		Actions: []dsl.Action{{Name: "main", Type: "GET", Target: "/main"}},
	}, ScriptDebugStartRequest{ReplaySessionID: "debug-iterations", Iterations: 3})
	completed := waitForScriptDebugStatus(t, manager, "debug-iterations", ScriptDebugCompleted)
	mu.Lock()
	got := executed
	mu.Unlock()
	if got != 3 || completed.FinalReport == nil || len(completed.FinalReport.Events) != 3 {
		t.Fatalf("executed=%d report=%v, want three action iterations", got, completed.FinalReport)
	}
	defaults := engine.DefaultDebugReplayHAROptions()
	if completed.RuntimeProfile.HAROptions == nil || *completed.RuntimeProfile.HAROptions != defaults {
		t.Fatalf("session HAR options = %+v, want the engine defaults %+v", completed.RuntimeProfile.HAROptions, defaults)
	}
	if options := scriptDebugHARAvailability(completed).Options; options == nil || *options != defaults {
		t.Fatalf("HAR availability options = %+v, want the recorded defaults", options)
	}
}

func TestDefaultDebugHARExportDescribesItsCaptureOptions(t *testing.T) {
	profile := replayRuntimeProfileForDebug(&dsl.Script{Name: "s", Protocol: "http"}, ScriptDebugStartRequest{}, false)
	if profile.HAROptions == nil {
		t.Fatal("expected default HAR options on a debug runtime profile")
	}
	var out bytes.Buffer
	if _, err := recordbridge.WriteReplaySyntheticHARWithOptions(nil, &out, profile.HAROptions); err != nil {
		t.Fatalf("WriteReplaySyntheticHARWithOptions: %v", err)
	}
	var har struct {
		Log struct {
			Meta struct {
				CaptureOptions *protocol.ReplayHAROptions `json:"capture_options"`
				OmittedFields  []string                   `json:"omitted_fields"`
			} `json:"_apexload"`
		} `json:"log"`
	}
	if err := json.Unmarshal(out.Bytes(), &har); err != nil {
		t.Fatalf("decode HAR: %v\n%s", err, out.String())
	}
	if har.Log.Meta.CaptureOptions == nil || !har.Log.Meta.CaptureOptions.Redirects || !har.Log.Meta.CaptureOptions.Timings {
		t.Fatalf("capture_options = %+v, want the default capture described", har.Log.Meta.CaptureOptions)
	}
	for _, field := range har.Log.Meta.OmittedFields {
		if field == "timings.send" {
			t.Fatalf("omitted_fields = %v, timings.send is captured by default", har.Log.Meta.OmittedFields)
		}
	}
}

// Later iterations of a multi-iteration session repeat the same top-level
// action ids; iteration 1 stays the correlation baseline instead of the
// session rejecting its evidence as duplicates.
func TestDebugCorrelationEvidenceKeepsFirstIterationBaseline(t *testing.T) {
	session := &ScriptDebugSession{ID: "iteration-evidence", Status: ScriptDebugRunning}
	evidence := func(iteration int, target string) (engine.ReplayDebugAction, *engine.ReplayActionEvent) {
		return engine.ReplayDebugAction{Phase: "action", ActionIndex: 0, MainIteration: iteration},
			&engine.ReplayActionEvent{Phase: "action", ActionIndex: 0, Success: true, CorrelationEvidence: &protocol.CorrelationEvidence{
				RequestURL: "https://example.test" + target, AuthoredRequestURL: "https://example.test" + target,
			}}
	}
	action, event := evidence(1, "/first")
	session.appendCorrelationObservationLocked(action, event, time.Now())
	action, event = evidence(2, "/second")
	session.appendCorrelationObservationLocked(action, event, time.Now())
	if session.correlationEvidenceErr != "" {
		t.Fatalf("evidence rejected: %s", session.correlationEvidenceErr)
	}
	if len(session.correlationObservations) != 1 || session.correlationObservations[0].RequestURL != "https://example.test/first" {
		t.Fatalf("observations = %+v, want iteration 1 only", session.correlationObservations)
	}

	// A real duplicate inside one iteration is still rejected.
	action, event = evidence(1, "/again")
	session.appendCorrelationObservationLocked(action, event, time.Now())
	if !strings.Contains(session.correlationEvidenceErr, "duplicate top-level replay action id 0") {
		t.Fatalf("correlationEvidenceErr = %q, want a duplicate rejection within one iteration", session.correlationEvidenceErr)
	}
}
