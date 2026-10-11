package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestDebugRunPolicyHelpers(t *testing.T) {
	if got := debugRunIdentity("  ", "s1"); got != "debug-s1" {
		t.Fatalf("default run identity = %q", got)
	}
	if got := debugRunIdentity(" ticket-42 ", "s1"); got != "ticket-42" {
		t.Fatalf("requested run identity = %q", got)
	}
	for _, bad := range []string{"has space", "a/b", "{{run.id}}", string(make([]byte, 129))} {
		if validateDebugRunIdentity(bad) == nil {
			t.Fatalf("run identity %q accepted", bad)
		}
	}
	if err := validateDebugRunIdentity("release_7.2:debug-1"); err != nil {
		t.Fatal(err)
	}
	stop, carryOn := false, true
	unset := &dsl.Script{}
	optedOut := &dsl.Script{}
	optedOut.Config.ErrorHandling.ContinueOnError = dsl.ContinueOnErrorValue(false)
	for _, test := range []struct {
		name      string
		requested *bool
		script    *dsl.Script
		want      bool
	}{
		{"unset script continues, as a test run does", nil, unset, true},
		{"script opt-out stops", nil, optedOut, false},
		{"request wins over the script", &carryOn, optedOut, true},
		{"request can stop", &stop, unset, false},
	} {
		if got := debugContinueOnError(test.requested, test.script); got != test.want {
			t.Fatalf("%s: continue = %v", test.name, got)
		}
	}
}

func TestScriptDebugSessionResolvesRunIDAndFailsLikeATestRun(t *testing.T) {
	var mu sync.Mutex
	var executed []string
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		mu.Lock()
		executed = append(executed, action.Name+" "+action.Target)
		mu.Unlock()
		if action.Name == "pay" {
			return &protocol.Result{ActionName: action.Name, Success: false, StatusCode: 500, ErrorMessage: "boom"}, nil
		}
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
	})
	ran := func() []string { mu.Lock(); defer mu.Unlock(); out := executed; executed = nil; return out }
	manager := NewScriptDebugManager(NewHub())
	// The script leaves continue-on-error unset, as most do.
	script := &dsl.Script{Name: "Run identity", Protocol: protocolName, Version: "1.0.0", Actions: []dsl.Action{
		{Name: "cart", Type: "GET", Target: "/runs/{{run.id}}/cart"},
		{Name: "pay", Type: "GET", Target: "/pay"},
		{Name: "receipt", Type: "GET", Target: "/receipt"},
	}}

	session := manager.Start("", script, ScriptDebugStartRequest{ReplaySessionID: "policy-default"})
	if session.RunID != "debug-policy-default" || !session.ContinueOnError || session.RuntimeProfile.FailurePolicy != "continue_on_error" {
		t.Fatalf("session run id/continue/policy = %q/%v/%q", session.RunID, session.ContinueOnError, session.RuntimeProfile.FailurePolicy)
	}
	waitForScriptDebugStatus(t, manager, "policy-default", ScriptDebugFailed)
	if got := ran(); !slices.Equal(got, []string{"cart /runs/debug-policy-default/cart", "pay /pay", "receipt /receipt"}) {
		t.Fatalf("executed = %#v, want the run identity resolved and the run to carry on past the failure", got)
	}

	stop := false
	session = manager.Start("", script, ScriptDebugStartRequest{ReplaySessionID: "policy-stop", RunID: "ticket-42", ContinueOnError: &stop})
	if session.RunID != "ticket-42" || session.ContinueOnError || session.RuntimeProfile.FailurePolicy != "stop_on_error" {
		t.Fatalf("session run id/continue/policy = %q/%v/%q", session.RunID, session.ContinueOnError, session.RuntimeProfile.FailurePolicy)
	}
	waitForScriptDebugStatus(t, manager, "policy-stop", ScriptDebugFailed)
	if got := ran(); !slices.Equal(got, []string{"cart /runs/ticket-42/cart", "pay /pay"}) {
		t.Fatalf("executed = %#v, want the iteration to stop at the failure", got)
	}
}

func TestScriptDebugStartRejectsAnUnsafeRunID(t *testing.T) {
	server := NewServer(ServerConfig{})
	body, err := json.Marshal(map[string]any{
		"run_id": "has space",
		"script": map[string]any{"name": "Draft", "version": "1.0.0", "protocol": "http", "config": map[string]any{"base_url": "https://example.com"},
			"actions": []map[string]any{{"name": "home", "type": "GET", "method": "GET", "target": "/"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/debug/sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusBadRequest || !bytes.Contains(resp.Body.Bytes(), []byte("run_id")) {
		t.Fatalf("status = %d body = %s, want 400 naming run_id", resp.Code, resp.Body.String())
	}
}
