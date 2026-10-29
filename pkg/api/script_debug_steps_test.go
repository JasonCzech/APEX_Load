package api

import (
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestSegmentKind(t *testing.T) {
	cases := map[string]string{
		"loop[2]":     "loop",
		"then[0]":     "then",
		"else[1]":     "else",
		"requests[3]": "requests",
		"plain":       "plain",
	}
	for in, want := range cases {
		if got := segmentKind(in); got != want {
			t.Errorf("segmentKind(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestComputeStepBoundaryOver(t *testing.T) {
	// Step-over a leaf inside an http_sequence: the container is the sequence.
	b := computeStepBoundary(&engine.ReplayDebugAction{Phase: "action", ActionIndex: 0, StepPath: []string{"requests[1]"}}, scriptStepOver)
	if b == nil || b.Kind != "requests" || len(b.Prefix) != 0 {
		t.Fatalf("over boundary = %#v, want kind=requests prefix=[]", b)
	}
	if !b.contains(engine.ReplayDebugAction{Phase: "action", ActionIndex: 0, StepPath: []string{"requests[2]"}}) {
		t.Error("sibling request must be inside the over boundary")
	}
	if b.contains(engine.ReplayDebugAction{Phase: "action", ActionIndex: 1, StepPath: nil}) {
		t.Error("the next top-level action must be outside the over boundary")
	}

	// Step-over a plain top-level leaf: the whole action.
	whole := computeStepBoundary(&engine.ReplayDebugAction{Phase: "action", ActionIndex: 2}, scriptStepOver)
	if whole == nil || !whole.WholeAction {
		t.Fatalf("over of plain action = %#v, want WholeAction", whole)
	}
	if !whole.contains(engine.ReplayDebugAction{Phase: "action", ActionIndex: 2}) {
		t.Error("the plain action must be inside its own whole-action boundary")
	}
	if whole.contains(engine.ReplayDebugAction{Phase: "action", ActionIndex: 3}) {
		t.Error("a different action must be outside the whole-action boundary")
	}
}

func TestComputeStepBoundaryLoopEnd(t *testing.T) {
	// Finish-loop from inside an if nested in a loop: boundary is the loop.
	b := computeStepBoundary(&engine.ReplayDebugAction{Phase: "action", ActionIndex: 0, StepPath: []string{"loop[1]", "then[0]"}}, scriptStepLoopEnd)
	if b == nil || b.Kind != "loop" || len(b.Prefix) != 0 {
		t.Fatalf("loop_end boundary = %#v, want kind=loop prefix=[]", b)
	}
	// Any leaf still under the loop (different pass/step) stays inside.
	if !b.contains(engine.ReplayDebugAction{Phase: "action", ActionIndex: 0, StepPath: []string{"loop[2]"}}) {
		t.Error("another loop step must be inside the loop boundary")
	}
	if b.contains(engine.ReplayDebugAction{Phase: "action", ActionIndex: 1}) {
		t.Error("the action after the loop must be outside the loop boundary")
	}

	// Nested loop: finish the inner loop, not the outer.
	inner := computeStepBoundary(&engine.ReplayDebugAction{Phase: "action", ActionIndex: 0, StepPath: []string{"loop[0]", "loop[1]"}}, scriptStepLoopEnd)
	if inner == nil || inner.Kind != "loop" || !slices.Equal(inner.Prefix, []string{"loop[0]"}) {
		t.Fatalf("inner loop boundary = %#v, want kind=loop prefix=[loop[0]]", inner)
	}
	if inner.contains(engine.ReplayDebugAction{Phase: "action", ActionIndex: 0, StepPath: []string{"loop[1]"}}) {
		t.Error("a leaf in the OUTER loop must be outside the inner-loop boundary")
	}

	// No enclosing loop: finish the whole action.
	none := computeStepBoundary(&engine.ReplayDebugAction{Phase: "action", ActionIndex: 0, StepPath: []string{"then[0]"}}, scriptStepLoopEnd)
	if none == nil || !none.WholeAction {
		t.Fatalf("loop_end with no loop = %#v, want WholeAction", none)
	}
}

func TestConditionEvidenceKeyStable(t *testing.T) {
	a := conditionEvidenceKey("action", 2, []string{"loop[0]"})
	b := conditionEvidenceKey("action", 2, []string{"loop[0]"})
	if a != b {
		t.Fatalf("keys differ for equal inputs: %q vs %q", a, b)
	}
	if a == conditionEvidenceKey("action", 2, []string{"loop[1]"}) {
		t.Fatal("different step paths must produce different keys")
	}
}

func TestScriptDebugManagerFinishLoopRunsToLoopExit(t *testing.T) {
	var mu sync.Mutex
	var executed []string
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		mu.Lock()
		executed = append(executed, action.Name)
		mu.Unlock()
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
	})

	manager := NewScriptDebugManager(NewHub())
	manager.Start("", &dsl.Script{
		Name:     "Finish Loop",
		Protocol: protocolName,
		Version:  "1.0.0",
		Actions: []dsl.Action{
			{
				Name: "Repeat", Type: "control_loop",
				Control: &dsl.ControlFlow{Count: 3, Steps: []dsl.Action{{Name: "A", Type: "GET", Method: "GET", Target: "/a"}}},
			},
			{Name: "Z", Type: "GET", Method: "GET", Target: "/z"},
		},
	}, ScriptDebugStartRequest{
		ReplaySessionID: "debug-finish-loop",
		// Whole-action breakpoint on the loop: fires once, before the first leaf.
		Breakpoints: []ScriptDebugBreakpoint{{Phase: "action", ActionIndex: 0}},
	})

	paused := waitForScriptDebugStatus(t, manager, "debug-finish-loop", ScriptDebugPaused)
	if paused.CurrentAction == nil || paused.CurrentAction.ActionName != "A" {
		t.Fatalf("paused at %#v, want first loop leaf A", paused.CurrentAction)
	}

	if _, ok, changed := manager.StepMode("debug-finish-loop", scriptStepLoopEnd); !ok || !changed {
		t.Fatal("StepMode loop_end failed")
	}
	after := waitForScriptDebugStatus(t, manager, "debug-finish-loop", ScriptDebugPaused)
	mu.Lock()
	got := append([]string(nil), executed...)
	mu.Unlock()
	if !slices.Equal(got, []string{"A", "A", "A"}) {
		t.Fatalf("executed after finish-loop = %#v, want all 3 loop passes", got)
	}
	if after.CurrentAction == nil || after.CurrentAction.ActionName != "Z" {
		t.Fatalf("paused after finish-loop at %#v, want Z (action after loop)", after.CurrentAction)
	}
}

func TestScriptDebugManagerStepOverContainer(t *testing.T) {
	var mu sync.Mutex
	var executed []string
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		mu.Lock()
		executed = append(executed, action.Name)
		mu.Unlock()
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
	})

	manager := NewScriptDebugManager(NewHub())
	manager.Start("", &dsl.Script{
		Name:     "Step Over Seq",
		Protocol: protocolName,
		Version:  "1.0.0",
		Actions: []dsl.Action{
			{
				Name: "txn", Type: "http_sequence", Method: "SEQUENCE", Target: "https://example.test",
				Params: map[string]any{"requests": []dsl.Action{
					{Name: "c0", Type: "GET", Method: "GET", Target: "/0"},
					{Name: "c1", Type: "GET", Method: "GET", Target: "/1"},
				}},
			},
			{Name: "Z", Type: "GET", Method: "GET", Target: "/z"},
		},
	}, ScriptDebugStartRequest{
		ReplaySessionID: "debug-step-over",
		Breakpoints:     []ScriptDebugBreakpoint{{Phase: "action", ActionIndex: 0}},
	})

	paused := waitForScriptDebugStatus(t, manager, "debug-step-over", ScriptDebugPaused)
	if paused.CurrentAction == nil || paused.CurrentAction.ActionName != "c0" {
		t.Fatalf("paused at %#v, want first sub-request c0", paused.CurrentAction)
	}

	if _, ok, changed := manager.StepMode("debug-step-over", scriptStepOver); !ok || !changed {
		t.Fatal("StepMode over failed")
	}
	after := waitForScriptDebugStatus(t, manager, "debug-step-over", ScriptDebugPaused)
	mu.Lock()
	got := append([]string(nil), executed...)
	mu.Unlock()
	if !slices.Equal(got, []string{"c0", "c1"}) {
		t.Fatalf("executed after step-over = %#v, want the whole sequence c0,c1", got)
	}
	if after.CurrentAction == nil || after.CurrentAction.ActionName != "Z" {
		t.Fatalf("paused after step-over at %#v, want Z (action after sequence)", after.CurrentAction)
	}
}

func TestScriptDebugManagerCapturesConditionEvidence(t *testing.T) {
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
	})

	manager := NewScriptDebugManager(NewHub())
	manager.Start("", &dsl.Script{
		Name:     "Evidence",
		Protocol: protocolName,
		Version:  "1.0.0",
		Actions: []dsl.Action{{
			Name: "Branch", Type: "control_if",
			Control: &dsl.ControlFlow{
				Condition: &dsl.Condition{Left: "{{var.role}}", Op: "eq", Right: "admin"},
				Steps:     []dsl.Action{{Name: "Then", Type: "GET", Method: "GET", Target: "/then"}},
				Else:      []dsl.Action{{Name: "Else", Type: "GET", Method: "GET", Target: "/else"}},
			},
		}},
	}, ScriptDebugStartRequest{
		ReplaySessionID: "debug-evidence",
		Variables:       map[string]string{"role": "admin"},
	})

	done := waitForScriptDebugStatus(t, manager, "debug-evidence", ScriptDebugCompleted)
	key := conditionEvidenceKey("action", 0, nil)
	evidence, ok := done.ConditionEvidence[key]
	if !ok {
		t.Fatalf("no condition evidence for key %q; have %#v", key, done.ConditionEvidence)
	}
	if evidence.Left != "admin" || evidence.Op != "eq" || evidence.Right != "admin" || !evidence.Result || evidence.Branch != "then" {
		t.Fatalf("condition evidence = %+v, want left=admin op=eq right=admin result=true branch=then", evidence)
	}
}

func TestScriptDebugManagerShowsSecretValuesInFinalReport(t *testing.T) {
	const secretValue = "debug-condition-project-secret"
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
	})

	manager := NewScriptDebugManager(NewHub())
	manager.Start("", &dsl.Script{
		Name:     "Secret Evidence",
		Protocol: protocolName,
		Version:  "1.0.0",
		Actions: []dsl.Action{{
			Name: "Branch", Type: "control_if",
			Control: &dsl.ControlFlow{
				Condition: &dsl.Condition{Left: "{{secret.API_TOKEN}}", Op: "exists"},
				Steps: []dsl.Action{{Name: "Then", Type: "GET", Method: "GET", Target: "/then"}, {
					Name: "Store secret alias",
					Type: "source_parameter",
					Params: map[string]any{
						"function": "lr_save_string",
						"args":     []any{"{{secret.API_TOKEN}}", "public_alias"},
					},
				}},
			},
		}},
	}, ScriptDebugStartRequest{
		ReplaySessionID: "debug-secret-evidence",
		SecretResolver: engine.SecretResolverFunc(func(name string) (string, bool) {
			return secretValue, name == "API_TOKEN"
		}),
	})

	done := waitForScriptDebugStatus(t, manager, "debug-secret-evidence", ScriptDebugCompleted)
	evidence := done.ConditionEvidence[conditionEvidenceKey("action", 0, nil)]
	if evidence.Left != "{{secret.API_TOKEN}}" || !evidence.Result || evidence.Branch != "then" {
		t.Fatalf("condition evidence = %+v", evidence)
	}
	if done.FinalReport == nil {
		t.Fatal("completed debugger session has no final replay report")
	}
	if got := done.FinalReport.FinalVariables["public_alias"]; got != secretValue {
		t.Fatalf("debug final public_alias = %q, want original value", got)
	}
	payload, err := json.Marshal(done)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(payload), secretValue) {
		t.Fatalf("serialized debugger session did not include the configured secret: %s", payload)
	}
}

func TestScriptDebugManagerFailsBeforeActionWhenStaticSecretIsMissing(t *testing.T) {
	executed := make(chan struct{}, 1)
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		executed <- struct{}{}
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
	})

	manager := NewScriptDebugManager(NewHub())
	manager.Start("", &dsl.Script{
		Name:     "Missing Secret",
		Protocol: protocolName,
		Version:  "1.0.0",
		Actions: []dsl.Action{{
			Name:    "request",
			Type:    "GET",
			Target:  "/",
			Headers: map[string]string{"Authorization": "Bearer {{secret.API_TOKEN}}"},
		}},
	}, ScriptDebugStartRequest{
		ReplaySessionID: "debug-missing-secret",
		SecretResolver: engine.SecretResolverFunc(func(string) (string, bool) {
			return "", false
		}),
	})

	done := waitForScriptDebugStatus(t, manager, "debug-missing-secret", ScriptDebugFailed)
	if !strings.Contains(done.Error, "API_TOKEN") || done.FinalReport != nil {
		t.Fatalf("debug missing-secret failure = error %q report %#v", done.Error, done.FinalReport)
	}
	select {
	case <-executed:
		t.Fatal("debugger executed a protocol action with a missing static secret")
	default:
	}
}

func TestScriptDebugLogRetentionIsBounded(t *testing.T) {
	session := &ScriptDebugSession{LogLevel: "trace"}
	for index := 0; index < maxScriptDebugLogEntries+25; index++ {
		session.appendLogLocked("info", "test", "entry", "raw", nil)
	}
	if len(session.Logs) != maxScriptDebugLogEntries || len(session.RawOutput) != maxScriptDebugLogEntries {
		t.Fatalf("retained logs/raw = %d/%d, want %d", len(session.Logs), len(session.RawOutput), maxScriptDebugLogEntries)
	}
	if session.logBytes > maxScriptDebugLogBytes {
		t.Fatalf("retained log bytes = %d, limit %d", session.logBytes, maxScriptDebugLogBytes)
	}
}
