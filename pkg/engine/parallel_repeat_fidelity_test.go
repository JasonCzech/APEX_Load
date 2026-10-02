package engine

import (
	"context"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestParallelRepeatLoopControlIndependentTraffic(t *testing.T) {
	var first, second, skipped atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/first":
			first.Add(1)
		case "/second":
			second.Add(1)
		default:
			skipped.Add(1)
		}
		w.WriteHeader(204)
	}))
	defer server.Close()
	p, err := protocol.Get("http")
	if err != nil {
		t.Fatal(err)
	}
	script := &dsl.Script{Protocol: "http", Config: dsl.ScriptConfig{BaseURL: server.URL}}
	vu := &VUser{ID: 1, Script: script, Proto: p, Variables: map[string]string{}, Metrics: NewMetricsCollector(100)}
	get := func(path string) dsl.Action { return dsl.Action{Name: path, Type: "GET", Target: path} }
	loop := func(control string, path string, count int) dsl.Action {
		return dsl.Action{Type: "control_loop", Control: &dsl.ControlFlow{Count: count, Steps: []dsl.Action{get(path), {Type: "control_if", Control: &dsl.ControlFlow{Steps: []dsl.Action{{Type: control}}}}, get("/skipped")}}}
	}
	branches := []dsl.ParallelBranch{{Name: "early", Steps: []dsl.Action{loop("control_break", "/first", 4)}}, {Name: "repeat", Steps: []dsl.Action{loop("control_continue", "/second", 3)}}}
	if !vu.runParallelBranches(context.Background(), &dsl.ControlFlow{Concurrency: 2, Branches: branches}, "action") {
		t.Fatal("branches failed")
	}
	if first.Load() != 1 || second.Load() != 3 || skipped.Load() != 0 {
		t.Fatalf("traffic %d/%d/%d", first.Load(), second.Load(), skipped.Load())
	}
	if parallelActionsRuntimeSafe([]dsl.ParallelBranch{{Name: "stray", Steps: []dsl.Action{{Type: "control_break"}}}}) {
		t.Fatal("loop control escaped branch ownership")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if vu.runParallelBranches(ctx, &dsl.ControlFlow{Branches: branches}, "action") {
		t.Fatal("cancelled composition ran")
	}
	if first.Load() != 1 || second.Load() != 3 {
		t.Fatal("cancel produced target traffic")
	}
}
