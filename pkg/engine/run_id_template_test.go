package engine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestRunIDTemplateResolvesOnlyManagedIdentity(t *testing.T) {
	vu := &VUser{RunID: "test-first"}
	if got := vu.interpolate("prefix-{{run.id}}-suffix"); got != "prefix-test-first-suffix" {
		t.Fatalf("run identity = %q", got)
	}
	for _, expr := range []string{"{{run.id}}", "{{run.other}}"} {
		vu = &VUser{}
		if got := vu.interpolate(expr); got != expr {
			t.Fatalf("unavailable identity %q = %q", expr, got)
		}
		if err := vu.checkResolvedSecrets(); err == nil || !strings.Contains(err.Error(), "run identity") {
			t.Fatalf("unavailable identity %q did not fail closed: %v", expr, err)
		}
	}
}

func TestRunIDTemplateInHTTPDefaultHeadersResolvesOrFailsClosed(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if got := r.Header.Get("X-Test-Map"); got != "managed-test" {
			t.Errorf("default header map run ID = %q", got)
		}
		if got := r.Header.Get("X-Test-List"); got != "managed-test" {
			t.Errorf("default header list run ID = %q", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	script := &dsl.Script{
		Name: "default header run identity", Protocol: "http",
		Config: dsl.ScriptConfig{BaseURL: server.URL, HTTP: dsl.HTTPConfig{
			DefaultHeaders:    map[string]string{"X-Test-Map": "{{run.id}}"},
			DefaultHeaderList: dsl.HeaderList{{Name: "X-Test-List", Value: "{{run.id}}"}},
		}},
		Actions: []dsl.Action{{Name: "request", Type: "request", Method: "GET", Target: "/"}},
	}
	if err := RunScriptShakeout(context.Background(), "users", script); err == nil || !strings.Contains(err.Error(), "run identity") {
		t.Fatalf("direct shakeout did not reject unresolved default header: %v", err)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("direct shakeout sent %d requests", got)
	}
	if err := RunScriptShakeoutWithRunIDAndSecretResolver(context.Background(), "users", script, "managed-test", nil); err != nil {
		t.Fatal(err)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("managed shakeout sent %d requests, want one", got)
	}
}

func TestRunIDTemplatePropagatesToEveryGroupVUserPath(t *testing.T) {
	for _, mode := range []string{"closed", "at-once", "open"} {
		t.Run(mode, func(t *testing.T) {
			observed := make(chan string, 16)
			protocolName := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
				observed <- action.Headers["X-Test"]
				return &protocol.Result{ActionName: action.Name, Success: true}, nil
			})
			script := &dsl.Script{
				Name: "same reusable script", Protocol: protocolName,
				Config: dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1}},
				Actions: []dsl.Action{{Name: "request", Type: "request", Target: "/",
					Headers: map[string]string{"X-Test": "{{run.id}}"}}},
			}
			group := GroupConfig{Name: "users", Script: script, RunID: "test-unique", VUsers: 1}
			switch mode {
			case "at-once":
				group.InitStrategy = "at_once"
				script.Init = []dsl.Action{{Name: "setup", Type: "request", Target: "/setup",
					Headers: map[string]string{"X-Test": "{{run.id}}"}}}
			case "open":
				script.Config.RunLogic.WorkloadModel = "open"
				script.Config.RunLogic.GlobalIterations = 1
				script.Config.Pacing = dsl.PacingConfig{Type: "tps", TPS: 100}
			}
			multi, err := NewMultiGroup(MultiGroupConfig{Duration: time.Second, Groups: []GroupConfig{group}})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if _, err := multi.Run(ctx); err != nil {
				t.Fatal(err)
			}
			if len(observed) == 0 {
				t.Fatal("no action executed")
			}
			for len(observed) > 0 {
				if got := <-observed; got != "test-unique" {
					t.Fatalf("%s action used run identity %q", mode, got)
				}
			}
		})
	}
}

func TestManagedShakeoutResolvesRunIDButDirectShakeoutFailsClosed(t *testing.T) {
	observed := make(chan string, 1)
	protocolName := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		observed <- action.Headers["X-Test"]
		return &protocol.Result{ActionName: action.Name, Success: true}, nil
	})
	script := &dsl.Script{Name: "shakeout run identity", Protocol: protocolName,
		Actions: []dsl.Action{{Name: "request", Type: "request", Target: "/",
			Headers: map[string]string{"X-Test": "{{run.id}}"}}}}
	if err := RunScriptShakeout(context.Background(), "users", script); err == nil || !strings.Contains(err.Error(), "run identity") {
		t.Fatalf("direct shakeout did not fail closed: %v", err)
	}
	if len(observed) != 0 {
		t.Fatal("direct shakeout sent an unresolved run identity")
	}
	if err := RunScriptShakeoutWithRunIDAndSecretResolver(context.Background(), "users", script, "managed-test", nil); err != nil {
		t.Fatal(err)
	}
	if got := <-observed; got != "managed-test" {
		t.Fatalf("managed shakeout identity = %q", got)
	}
}
