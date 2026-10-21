package engine

import (
	"context"
	"fmt"
	"github.com/jasonczech/apexload/pkg/dsl"
	_ "github.com/jasonczech/apexload/pkg/protocol/http"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestIterationTerminationCompletesTenThousandRequests(t *testing.T) {
	var count atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { count.Add(1); w.WriteHeader(200) }))
	defer server.Close()
	script := &dsl.Script{Name: "finite", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: server.URL, RunLogic: dsl.RunLogicConfig{Iterations: 200}}, Actions: []dsl.Action{{Name: "request", Type: "GET", Method: "GET", Target: "/"}}}
	e, err := NewMultiGroup(MultiGroupConfig{TerminationMode: dsl.TerminationIterations, Groups: []GroupConfig{{Name: "users", Script: script, VUsers: 50, RampUp: time.Second}}})
	if err != nil {
		t.Fatal(err)
	}
	// This asserts complete iteration accounting, not a throughput deadline.
	// Shared race-detector runners need headroom while keeping the wait bounded.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if _, err := e.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if count.Load() != 10000 {
		t.Fatalf("requests=%d", count.Load())
	}
}

func TestIterationPauseResumeStopAndMultipleGroups(t *testing.T) {
	for _, stop := range []bool{false, true} {
		t.Run(fmt.Sprintf("stop=%t", stop), func(t *testing.T) {
			var count atomic.Int64
			first := make(chan struct{}, 1)
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count.Add(1)
				select {
				case first <- struct{}{}:
				default:
				}
				time.Sleep(15 * time.Millisecond)
				w.WriteHeader(500)
			}))
			defer target.Close()
			script := &dsl.Script{Name: "finite", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: target.URL, RunLogic: dsl.RunLogicConfig{Iterations: 8}, ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: dsl.ContinueOnErrorValue(true)}}, Actions: []dsl.Action{{Name: "sample", Type: "GET", Target: "/"}}}
			engine, err := NewMultiGroup(MultiGroupConfig{TerminationMode: "iterations", Groups: []GroupConfig{{Name: "a", Script: script, VUsers: 2}, {Name: "b", Script: script, VUsers: 1}}})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { _, e := engine.Run(ctx); done <- e }()
			select {
			case <-first:
			case <-ctx.Done():
				t.Fatal("no execution")
			}
			if err := engine.Pause(); err != nil {
				t.Fatal(err)
			}
			time.Sleep(50 * time.Millisecond)
			paused := count.Load()
			time.Sleep(60 * time.Millisecond)
			if count.Load() != paused {
				t.Fatal("samples continued while paused")
			}
			if stop {
				engine.Stop()
			} else if err := engine.Resume(); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if !stop && err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("did not terminate")
			}
			if stop && count.Load() >= 24 {
				t.Fatal("stop completed remaining work")
			}
			if !stop && (count.Load() != 24 || engine.CompletedIterations() != 24) {
				t.Fatalf("requests=%d iterations=%d", count.Load(), engine.CompletedIterations())
			}
		})
	}
}

func TestIterationModeMustBeExplicitAtExecutionBoundary(t *testing.T) {
	script := &dsl.Script{Name: "finite", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: "http://127.0.0.1", RunLogic: dsl.RunLogicConfig{TerminationMode: "iterations", Iterations: 2}}, Actions: []dsl.Action{{Name: "sample", Type: "GET", Target: "/"}}}
	if _, err := New(Config{Script: script, VUsers: 1}); err == nil {
		t.Fatal("zero duration inferred an unbounded execution mode")
	}
	if _, err := New(Config{Script: script, VUsers: 1, Duration: time.Second}); err != nil {
		t.Fatal("omitted mode did not retain duration behavior:", err)
	}
	if _, err := New(Config{Script: script, VUsers: 1, TerminationMode: "iterations"}); err != nil {
		t.Fatal(err)
	}
}
