package engine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// lr_exit(LR_EXIT_MAIN_ITERATION_AND_CONTINUE) ends only the current debug
// iteration: the replay goes on to the next one, as a load run does.
func TestReplayIterationExitContinuesWithTheNextIteration(t *testing.T) {
	protocolName := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
	})
	script := &dsl.Script{
		Name:     "ExitIteration",
		Protocol: protocolName,
		Actions: []dsl.Action{
			{Name: "main-step", Type: "GET", Method: "GET", Target: "/main"},
			{Name: "exit", Type: "source_runtime", Params: map[string]any{
				"function": "lr_exit",
				"args":     []any{"LR_EXIT_MAIN_ITERATION_AND_CONTINUE", "LR_PASS"},
			}},
			{Name: "after", Type: "GET", Method: "GET", Target: "/after"},
		},
		Teardown: []dsl.Action{{Name: "teardown-step", Type: "GET", Method: "GET", Target: "/teardown"}},
	}
	report, err := ReplayScript(context.Background(), script, ReplayOptions{IgnoreThinkTime: true, Iterations: 3})
	if err != nil {
		t.Fatalf("ReplayScript error: %v", err)
	}
	var phases []string
	for _, ev := range report.Events {
		phases = append(phases, ev.Phase+":"+ev.ActionName)
	}
	want := []string{
		"action:main-step", "action:exit",
		"action:main-step", "action:exit",
		"action:main-step", "action:exit",
		"teardown:teardown-step",
	}
	if !slices.Equal(phases, want) {
		t.Fatalf("phases=%v want %v", phases, want)
	}
	if !report.Success || report.Error != "" {
		t.Fatalf("report success=%v error=%q, want a successful three-iteration replay", report.Success, report.Error)
	}
}

// An async conversation the script never stops ends with its iteration, so
// the next debug iteration can start it again instead of failing with
// "already running".
func TestReplayIterationsStopUnstoppedAsyncConversations(t *testing.T) {
	var mu sync.Mutex
	polls := 0
	protocolName := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		if action.Name == "poll" {
			mu.Lock()
			polls++
			mu.Unlock()
		}
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
	})
	script := &dsl.Script{
		Name:     "AsyncIterations",
		Protocol: protocolName,
		Actions: []dsl.Action{
			asyncPollStart(nil),
			{Name: "main-step", Type: "GET", Method: "GET", Target: "/main"},
		},
	}
	report, err := ReplayScript(context.Background(), script, ReplayOptions{IgnoreThinkTime: true, Iterations: 2})
	if err != nil {
		t.Fatalf("ReplayScript error: %v", err)
	}
	if !report.Success {
		t.Fatalf("report = success=%v error=%q, want both iterations to start the conversation", report.Success, report.Error)
	}
	for _, ev := range report.Events {
		if strings.Contains(ev.ErrorMessage, "already running") {
			t.Fatalf("iteration restarted a conversation that was still running: %+v", ev)
		}
	}
	starts := 0
	for _, ev := range report.Events {
		if ev.ActionName == "start poll" && ev.Success {
			starts++
		}
	}
	if starts != 2 {
		t.Fatalf("successful async starts = %d, want 2", starts)
	}
}

// http.clear_cookies_per_iteration applies between debug iterations as it
// does under load: iteration 2 must not reuse iteration 1's session cookie.
func TestReplayIterationsClearCookiesPerIteration(t *testing.T) {
	var mu sync.Mutex
	var checkCookies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/check":
			mu.Lock()
			checkCookies = append(checkCookies, r.Header.Get("Cookie"))
			mu.Unlock()
		case "/login":
			http.SetCookie(w, &http.Cookie{Name: "sid", Value: "iteration-session", Path: "/"})
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	for _, clear := range []bool{true, false} {
		mu.Lock()
		checkCookies = nil
		mu.Unlock()
		clearPerIteration := clear
		script := &dsl.Script{
			Name:     "ClearCookies",
			Protocol: "http",
			Config: dsl.ScriptConfig{
				BaseURL: server.URL,
				HTTP:    dsl.HTTPConfig{ClearCookiesPerIter: &clearPerIteration},
			},
			Actions: []dsl.Action{
				{Name: "check", Type: "GET", Method: "GET", Target: "/check"},
				{Name: "login", Type: "GET", Method: "GET", Target: "/login"},
			},
		}
		report, err := ReplayScript(context.Background(), script, ReplayOptions{IgnoreThinkTime: true, Iterations: 2})
		if err != nil || !report.Success {
			t.Fatalf("clear=%v: ReplayScript err=%v report=%+v", clear, err, report)
		}
		mu.Lock()
		got := append([]string(nil), checkCookies...)
		mu.Unlock()
		if len(got) != 2 || got[0] != "" {
			t.Fatalf("clear=%v: check cookies = %q, want two checks, the first without a cookie", clear, got)
		}
		if clear && got[1] != "" {
			t.Fatalf("iteration 2 sent %q, want no cookie after clear_cookies_per_iteration", got[1])
		}
		if !clear && !strings.Contains(got[1], "sid=") {
			t.Fatalf("iteration 2 sent %q, want the jar to keep sid without clear_cookies_per_iteration", got[1])
		}
	}
}

type correlationIterationRecorder struct {
	mu         sync.Mutex
	iterations []int
}

func (r *correlationIterationRecorder) BeforeAction(context.Context, ReplayDebugAction) error {
	return nil
}

func (r *correlationIterationRecorder) ObserveCorrelationEvidence(_ context.Context, action ReplayDebugAction, _ *ReplayActionEvent) {
	r.mu.Lock()
	r.iterations = append(r.iterations, action.MainIteration)
	r.mu.Unlock()
}

// The correlation observer learns which action iteration an event belongs to,
// so a multi-iteration session can keep iteration 1 as its baseline.
func TestReplayDebugActionCarriesMainIteration(t *testing.T) {
	protocolName := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
	})
	script := &dsl.Script{
		Name:     "MainIteration",
		Protocol: protocolName,
		Init:     []dsl.Action{{Name: "init-step", Type: "GET", Method: "GET", Target: "/init"}},
		Actions:  []dsl.Action{{Name: "main-step", Type: "GET", Method: "GET", Target: "/main"}},
	}
	recorder := &correlationIterationRecorder{}
	if _, err := ReplayScript(context.Background(), script, ReplayOptions{IgnoreThinkTime: true, Iterations: 2, DebugController: recorder}); err != nil {
		t.Fatalf("ReplayScript error: %v", err)
	}
	if want := []int{0, 1, 2}; !slices.Equal(recorder.iterations, want) {
		t.Fatalf("observed main iterations = %v, want %v (init, iteration 1, iteration 2)", recorder.iterations, want)
	}
}
