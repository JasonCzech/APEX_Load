package api

import (
	"encoding/json"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestLiveTableSandboxDebugRunIdentityAndFailurePolicy(t *testing.T) {
	for _, mode := range []string{"script", "browser"} {
		t.Run(mode, func(t *testing.T) {
			var mu sync.Mutex
			var executed []string
			protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
				mu.Lock()
				executed = append(executed, action.Name+" "+action.Target)
				mu.Unlock()
				if action.Name == "fail" {
					return &protocol.Result{ActionName: action.Name, Success: false, StatusCode: 500, ErrorMessage: "expected failure"}, nil
				}
				return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
			})
			script, seeds := sandboxReplayFixture(t)
			script.Protocol = protocolName
			script.Actions = append(script.Actions,
				dsl.Action{Name: "tag", Type: "GET", Target: "/runs/{{run.id}}"},
				dsl.Action{Name: "fail", Type: "GET", Target: "/fail"},
				dsl.Action{Name: "after", Type: "GET", Target: "/after"},
			)
			scriptManager := NewScriptDebugManager(NewHub())
			browserManager := NewBrowserReplayManager(NewHub())
			t.Cleanup(scriptManager.Close)
			t.Cleanup(browserManager.Close)

			// Reusing an external run tag must not reuse the consumed sandbox.
			// Decode the public request JSON to exercise both merged DTOs.
			for _, carryOn := range []bool{true, false} {
				sessionID := mode + "-" + debugFailurePolicy(carryOn)
				body, err := json.Marshal(map[string]any{
					"live_table_seeds": seeds, "replay_session_id": sessionID,
					"run_id": "production-like-run-42", "continue_on_error": carryOn,
				})
				if err != nil {
					t.Fatal(err)
				}
				var report *engine.ReplayReport
				var runID, failurePolicy string
				var continued bool
				if mode == "script" {
					var request ScriptDebugStartRequest
					if err := json.Unmarshal(body, &request); err != nil {
						t.Fatal(err)
					}
					scriptManager.Start("", script, request)
					session := waitForScriptDebugStatus(t, scriptManager, sessionID, ScriptDebugFailed)
					report, runID, continued, failurePolicy = session.FinalReport, session.RunID, session.ContinueOnError, session.RuntimeProfile.FailurePolicy
				} else {
					var request BrowserReplayStartRequest
					if err := json.Unmarshal(body, &request); err != nil {
						t.Fatal(err)
					}
					if _, err := browserManager.Start("", script, request); err != nil {
						t.Fatal(err)
					}
					deadline := time.Now().Add(2 * time.Second)
					for {
						session, ok := browserManager.Get(sessionID)
						if !ok {
							t.Fatal("browser session disappeared")
						}
						if session.Status == BrowserReplayFailed {
							report, runID, continued, failurePolicy = session.FinalReport, session.RunID, session.ContinueOnError, session.RuntimeProfile.FailurePolicy
							break
						}
						if time.Now().After(deadline) {
							t.Fatalf("browser status = %q, want failed", session.Status)
						}
						time.Sleep(10 * time.Millisecond)
					}
				}
				if runID != "production-like-run-42" || continued != carryOn || failurePolicy != debugFailurePolicy(carryOn) {
					t.Fatalf("session identity/policy = %q/%v/%q", runID, continued, failurePolicy)
				}
				if report == nil || report.Success || report.FinalVariables["taken.values.id"] != "sandbox-only" {
					t.Fatalf("fresh sandbox or failed report lost: %+v", report)
				}
				mu.Lock()
				got := executed
				executed = nil
				mu.Unlock()
				want := []string{"tag /runs/production-like-run-42", "fail /fail"}
				if carryOn {
					want = append(want, "after /after")
				}
				if !slices.Equal(got, want) {
					t.Fatalf("continue=%v: executed %#v, want %#v", carryOn, got, want)
				}
				if seeds[0].Rows[0].Values["id"] != "sandbox-only" {
					t.Fatal("replay changed the supplied seed")
				}
			}
		})
	}
}
