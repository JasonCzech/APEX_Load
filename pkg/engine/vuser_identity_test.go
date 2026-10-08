package engine

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

type identitySink struct {
	mu      sync.Mutex
	results []protocol.Result
}

func (s *identitySink) RecordResult(result *protocol.Result) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.results = append(s.results, *result)
	return nil
}

func (s *identitySink) RecordTransaction(result *protocol.Result) error {
	return s.RecordResult(result)
}

// Every result carries the run-wide VUser ID and its 1-based iteration;
// journey accounting groups archived rows by them.
func TestVUserStampsResultsWithIterationIdentity(t *testing.T) {
	vu := newControlFlowVUser(&captureProtocol{}, nil, []dsl.Action{click("login"), click("checkout")})
	vu.ID = 42
	vu.Script.Config.RunLogic.Iterations = 3
	vu.Script.Init = []dsl.Action{click("warmup")}
	vu.Script.Teardown = []dsl.Action{click("logout")}
	sink := &identitySink{}
	vu.ResultSink = sink
	vu.Run(context.Background())
	iterations := map[int64][]string{}
	for _, result := range sink.results {
		if result.VUserID != 42 {
			t.Fatalf("result %q has VUser %d", result.ActionName, result.VUserID)
		}
		iterations[result.Iteration] = append(iterations[result.Iteration], result.ActionName)
	}
	for iteration := int64(1); iteration <= 3; iteration++ {
		if names := iterations[iteration]; len(names) < 2 || names[0] != "login" || names[1] != "checkout" {
			t.Fatalf("iteration %d results %v (all %v)", iteration, names, iterations)
		}
	}
	if len(iterations[4]) != 0 {
		t.Fatalf("a fourth iteration was stamped: %v", iterations[4])
	}
	// Init and teardown belong to no iteration.
	if names := iterations[0]; len(names) != 2 || names[0] != "warmup" || names[1] != "logout" {
		t.Fatalf("outside iterations %v (all %v)", names, iterations)
	}
}

// A parallel branch's samples belong to the parent VUser's iteration.
func TestParallelBranchResultsCarryParentIteration(t *testing.T) {
	sink := &identitySink{}
	vu := &VUser{
		ID: 3, Group: "parallel", Proto: &parallelTestProtocol{},
		Variables: map[string]string{}, Metrics: NewMetricsCollector(32), ResultSink: sink,
		Script: &dsl.Script{Protocol: parallelTestProtocolName, Actions: []dsl.Action{{Name: "unused", Type: "GET", Target: "/"}}},
	}
	vu.currentIteration.Store(7)
	control := &dsl.ControlFlow{Concurrency: 2, Branches: []dsl.ParallelBranch{
		{Name: "catalog", Steps: []dsl.Action{{Name: "catalog-call", Type: "GET", Target: "/catalog"}}},
		{Name: "cart", Steps: []dsl.Action{{Name: "cart-call", Type: "GET", Target: "/cart"}}},
	}}
	if !vu.runParallelBranches(context.Background(), control, "action") {
		t.Fatal("parallel branches failed")
	}
	names := map[string]bool{}
	for _, result := range sink.results {
		if result.VUserID != 3 || result.Iteration != 7 {
			t.Fatalf("branch result %q stamped VUser %d iteration %d", result.ActionName, result.VUserID, result.Iteration)
		}
		names[result.ActionName] = true
	}
	if !names["catalog-call"] || !names["cart-call"] {
		t.Fatalf("branch results %v", sink.results)
	}
}

// stoppingProtocol stops the run while executing one named action, and
// optionally fails another.
type stoppingProtocol struct {
	stopOn string
	stop   context.CancelFunc
	failOn string
}

func (*stoppingProtocol) Name() string                               { return "http" }
func (*stoppingProtocol) Init(context.Context, map[string]any) error { return nil }
func (*stoppingProtocol) Teardown(context.Context) error             { return nil }
func (*stoppingProtocol) HealthCheck(context.Context) error          { return nil }
func (p *stoppingProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	if action.Name == p.stopOn {
		p.stop()
	}
	return &protocol.Result{ActionName: action.Name, StartTime: time.Now(), Duration: time.Millisecond, Success: action.Name != p.failOn}, nil
}

func resultsNamed(sink *identitySink, name string) []protocol.Result {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	var out []protocol.Result
	for _, result := range sink.results {
		if result.ActionName == name {
			out = append(out, result)
		}
	}
	return out
}

// A graceful stop abandons the iteration at the next action boundary; the
// failed iteration transaction it leaves behind is the stop's, so it is
// marked interrupted. The action that ran to completion is not.
func TestGracefulStopMarksTheAbandonedIterationTransactionInterrupted(t *testing.T) {
	// A graceful stop cancels Run's context and the action boundary, never
	// the hard-stop context the action in flight runs under.
	run, stop := context.WithCancel(context.Background())
	defer stop()
	proto := &stoppingProtocol{stopOn: "login", stop: stop}
	vu := newControlFlowVUser(proto, nil, []dsl.Action{click("login"), click("checkout")})
	vu.ActionBoundaryCtx, vu.HardStopCtx = run, context.Background()
	sink := &identitySink{}
	vu.ResultSink = sink
	vu.Run(run)
	login := resultsNamed(sink, "login")
	if len(login) != 1 || !login[0].Success || login[0].Interrupted || login[0].Iteration != 1 {
		t.Fatalf("login %+v", login)
	}
	if checkout := resultsNamed(sink, "checkout"); len(checkout) != 0 {
		t.Fatalf("checkout ran after the stop: %+v", checkout)
	}
	iteration := resultsNamed(sink, "ControlFlow")
	if len(iteration) != 1 || !iteration[0].BusinessTransaction || iteration[0].Success || !iteration[0].Interrupted || iteration[0].Iteration != 1 {
		t.Fatalf("iteration transaction %+v", iteration)
	}
}

// An iteration that genuinely failed before any stop is not interrupted.
func TestGenuineIterationFailureIsNotInterrupted(t *testing.T) {
	proto := &stoppingProtocol{stop: func() {}, failOn: "login"}
	vu := newControlFlowVUser(proto, nil, []dsl.Action{click("login"), click("checkout")})
	sink := &identitySink{}
	vu.ResultSink = sink
	vu.Run(context.Background())
	login, iteration := resultsNamed(sink, "login"), resultsNamed(sink, "ControlFlow")
	if len(login) != 1 || login[0].Success || login[0].Interrupted || len(iteration) != 1 || iteration[0].Success || iteration[0].Interrupted {
		t.Fatalf("login %+v iteration %+v", login, iteration)
	}
}

// A hard stop cancels the action in flight: its failure is interrupted; a
// success, or a failure recorded before any stop, is not.
func TestHardStopMarksTheInFlightFailureInterrupted(t *testing.T) {
	sink := &identitySink{}
	vu := &VUser{ID: 5, Group: "web", ResultSink: sink, Metrics: NewMetricsCollector(16), Script: &dsl.Script{Protocol: "http"}}
	hard, cancel := context.WithCancel(context.Background())
	vu.HardStopCtx = hard
	vu.recordResult(&protocol.Result{ActionName: "before", Success: false}, dsl.ErrorHandlingConfig{}, dsl.LogConfig{})
	cancel()
	vu.recordResult(&protocol.Result{ActionName: "cut", Success: false}, dsl.ErrorHandlingConfig{}, dsl.LogConfig{})
	vu.recordResult(&protocol.Result{ActionName: "done", Success: true}, dsl.ErrorHandlingConfig{}, dsl.LogConfig{})
	vu.recordTransaction("tx", time.Millisecond, false, nil)
	for name, want := range map[string]bool{"before": false, "cut": true, "done": false, "tx": true} {
		if got := resultsNamed(sink, name); len(got) != 1 || got[0].Interrupted != want {
			t.Fatalf("%s: %+v, want interrupted=%v", name, got, want)
		}
	}
	// Without a hard-stop context, Run's own context is the in-flight one.
	run, cancelRun := context.WithCancel(context.Background())
	direct := &VUser{ID: 6, Group: "web", ResultSink: sink, Metrics: NewMetricsCollector(16), Script: &dsl.Script{Protocol: "http"}, runCtx: run}
	cancelRun()
	direct.recordResult(&protocol.Result{ActionName: "direct", Success: false}, dsl.ErrorHandlingConfig{}, dsl.LogConfig{})
	if got := resultsNamed(sink, "direct"); len(got) != 1 || !got[0].Interrupted {
		t.Fatalf("direct: %+v", got)
	}
	// An abandoned transaction is interrupted; the flag does not leak.
	quiet := &VUser{ID: 7, Group: "web", ResultSink: sink, Metrics: NewMetricsCollector(16), Script: &dsl.Script{Protocol: "http"}}
	quiet.recordAbandonedTransaction("abandoned", time.Millisecond, nil)
	quiet.recordTransaction("genuine", time.Millisecond, false, nil)
	if got := resultsNamed(sink, "abandoned"); len(got) != 1 || !got[0].Interrupted || got[0].Success {
		t.Fatalf("abandoned: %+v", got)
	}
	if got := resultsNamed(sink, "genuine"); len(got) != 1 || got[0].Interrupted {
		t.Fatalf("genuine: %+v", got)
	}
}
