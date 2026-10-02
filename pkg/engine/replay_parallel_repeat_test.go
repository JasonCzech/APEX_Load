package engine

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestReplayParallelRepeatKeepsWritableBranchAndParentVariables(t *testing.T) {
	for _, tc := range []struct {
		name        string
		branchLoops bool
		variables   map[string]string
	}{
		{name: "branch-local break and continue", branchLoops: true},
		{name: "parent loop after empty branch state"},
		{name: "preserve seeded parent loop index", branchLoops: true, variables: map[string]string{"loop_index": "17", "seed": "kept"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var first, second, after, skipped atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/first":
					first.Add(1)
				case "/second":
					second.Add(1)
				case "/after":
					after.Add(1)
				default:
					skipped.Add(1)
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()
			get := func(path string) dsl.Action { return dsl.Action{Name: path, Type: "GET", Target: path} }
			loop := func(name, transfer, path string, count int) dsl.Action {
				steps := []dsl.Action{get(path)}
				if transfer != "" {
					steps = append(steps, dsl.Action{Name: transfer, Type: "control_if", Control: &dsl.ControlFlow{Steps: []dsl.Action{{Name: transfer, Type: transfer}}}}, get("/skipped"))
				}
				return dsl.Action{Name: name, Type: "control_loop", Control: &dsl.ControlFlow{Count: count, Steps: steps}}
			}
			branches := []dsl.ParallelBranch{
				{Name: "first", Steps: []dsl.Action{get("/first")}},
				{Name: "second", Steps: []dsl.Action{get("/second")}},
			}
			if tc.branchLoops {
				branches[0].Steps = []dsl.Action{loop("break early", "control_break", "/first", 4)}
				branches[1].Steps = []dsl.Action{loop("continue each pass", "control_continue", "/second", 3)}
			}
			script := &dsl.Script{Name: tc.name, Protocol: "http", Config: dsl.ScriptConfig{BaseURL: server.URL}, Actions: []dsl.Action{
				{Name: "parallel", Type: "control_parallel", Control: &dsl.ControlFlow{Branches: branches}},
				loop("after parallel", "", "/after", 2),
			}}
			report, err := ReplayScript(context.Background(), script, ReplayOptions{IgnoreThinkTime: true, Variables: tc.variables})
			if err != nil {
				t.Fatal(err)
			}
			secondWant := int64(1)
			if tc.branchLoops {
				secondWant = 3
			}
			if first.Load() != 1 || second.Load() != secondWant || after.Load() != 2 || skipped.Load() != 0 {
				t.Fatalf("traffic first=%d second=%d after=%d skipped=%d", first.Load(), second.Load(), after.Load(), skipped.Load())
			}
			if report.Stopped {
				t.Fatal("replay stopped before its following loop")
			}
			for key, value := range tc.variables {
				if report.FinalVariables[key] != value {
					t.Fatalf("parent variable %q=%q, want %q", key, report.FinalVariables[key], value)
				}
			}
			if tc.variables == nil {
				if _, ok := report.FinalVariables["loop_index"]; ok {
					t.Fatal("branch or following loop index escaped into the parent")
				}
			}
		})
	}
}

func TestReplayParallelAbortRestoresParentBeforeTeardown(t *testing.T) {
	for _, stop := range []string{"debugger", "cancellation"} {
		t.Run(stop, func(t *testing.T) {
			var branch, parentCleanup, branchCleanup, skipped atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/capture":
					branch.Add(1)
					w.Header().Set("X-Marker", "branch")
				case "/parent":
					parentCleanup.Add(1)
				case "/branch":
					branchCleanup.Add(1)
				default:
					skipped.Add(1)
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()
			capture := dsl.Action{Name: "capture", Type: "GET", Target: "/capture", Extractors: []dsl.Extractor{{Name: "marker", Source: "header", Type: "header", Expression: "X-Marker"}}}
			get := func(name, path string) dsl.Action { return dsl.Action{Name: name, Type: "GET", Target: path} }
			script := &dsl.Script{Name: "abort parallel", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: server.URL}, Actions: []dsl.Action{
				{Name: "parallel", Type: "control_parallel", Control: &dsl.ControlFlow{Branches: []dsl.ParallelBranch{
					{Name: "first", Steps: []dsl.Action{capture, get("abort point", "/skipped")}},
					{Name: "second", Steps: []dsl.Action{get("second", "/skipped")}},
				}}},
			}, Teardown: []dsl.Action{get("cleanup", "/{{var.marker}}")}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			opts := ReplayOptions{IgnoreThinkTime: true, Variables: map[string]string{"marker": "parent"}}
			if stop == "debugger" {
				opts.DebugController = replayDebugControllerFunc(func(_ context.Context, action ReplayDebugAction) error {
					if action.ActionName == "abort point" {
						return errors.New("operator stopped replay")
					}
					return nil
				})
			} else {
				opts.OnProgress = func(event ReplayProgressEvent) {
					if event.Type == ReplayProgressActionCompleted && event.Event != nil && event.Event.ActionName == "capture" {
						cancel()
					}
				}
			}
			report, err := ReplayScript(ctx, script, opts)
			if err != nil {
				t.Fatal(err)
			}
			if branch.Load() != 1 || parentCleanup.Load() != 1 || branchCleanup.Load() != 0 || skipped.Load() != 0 {
				t.Fatalf("traffic branch=%d parent cleanup=%d branch cleanup=%d skipped=%d", branch.Load(), parentCleanup.Load(), branchCleanup.Load(), skipped.Load())
			}
			if report.FinalVariables["marker"] != "parent" {
				t.Fatalf("branch state leaked into final variables: %v", report.FinalVariables)
			}
			if report.Success {
				t.Fatal("aborted replay reported success")
			}
			captured := false
			for _, event := range report.Events {
				if event.ActionName == "capture" && event.Extractions["marker"] == "branch" {
					captured = true
				}
			}
			if !captured {
				t.Fatal("fixture did not retain the branch extraction before abort")
			}
		})
	}
}
