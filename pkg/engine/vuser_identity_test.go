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

// Every result of one VUser lifetime carries the same random lifetime; a
// later VUser reusing the ID gets another, and parallel branches share it.
func TestVUserResultsCarryTheirLifetime(t *testing.T) {
	lifetimes := map[uint64]int{}
	for run := 0; run < 2; run++ {
		vu := newControlFlowVUser(&captureProtocol{}, nil, []dsl.Action{click("login")})
		vu.ID = 3
		sink := &identitySink{}
		vu.ResultSink = sink
		vu.Run(context.Background())
		seen := map[uint64]bool{}
		for _, result := range sink.results {
			seen[result.VUserLifetime] = true
		}
		if len(seen) != 1 || seen[0] {
			t.Fatalf("run %d lifetimes %v", run, seen)
		}
		for lifetime := range seen {
			lifetimes[lifetime]++
		}
	}
	if len(lifetimes) != 2 {
		t.Fatalf("two VUsers shared a lifetime: %v", lifetimes)
	}
	sink := &identitySink{}
	vu := &VUser{ID: 3, Group: "parallel", Proto: &parallelTestProtocol{}, Variables: map[string]string{}, Metrics: NewMetricsCollector(32), ResultSink: sink,
		Script: &dsl.Script{Protocol: parallelTestProtocolName, Actions: []dsl.Action{{Name: "unused", Type: "GET", Target: "/"}}}}
	vu.lifetime = 99
	control := &dsl.ControlFlow{Concurrency: 2, Branches: []dsl.ParallelBranch{{Name: "a", Steps: []dsl.Action{{Name: "a-call", Type: "GET", Target: "/a"}}}, {Name: "b", Steps: []dsl.Action{{Name: "b-call", Type: "GET", Target: "/b"}}}}}
	if !vu.runParallelBranches(context.Background(), control, "action") {
		t.Fatal("parallel branches failed")
	}
	for _, result := range sink.results {
		if result.VUserLifetime != 99 {
			t.Fatalf("branch result lifetime %d", result.VUserLifetime)
		}
	}
}

// A transaction whose duration excludes time starts at its real start and
// carries its wall-clock span.
func TestTransactionSpanKeepsTheWallClock(t *testing.T) {
	sink := &identitySink{}
	vu := &VUser{ID: 1, Group: "web", ResultSink: sink, Metrics: NewMetricsCollector(16), Script: &dsl.Script{Protocol: "http"}}
	started := time.Now().Add(-3 * time.Second)
	vu.recordTransactionSpan("Browse", time.Second, true, nil, started)
	vu.recordTransaction("Plain", time.Second, true, nil)
	browse, plain := resultsNamed(sink, "Browse"), resultsNamed(sink, "Plain")
	if len(browse) != 1 || !browse[0].StartTime.Equal(started) || browse[0].WallElapsed < 3*time.Second || browse[0].Duration != time.Second {
		t.Fatalf("span %+v", browse)
	}
	if len(plain) != 1 || plain[0].WallElapsed != 0 {
		t.Fatalf("plain %+v", plain)
	}
	// A source transaction left open is recorded in the iteration that
	// opened it, even after teardown reset the iteration.
	vu.sourceTransactions = map[string]*sourceTransactionRuntimeState{"checkout": {name: "Checkout", startedAt: started, iteration: 4}}
	vu.finalizeSourceTransaction("checkout", "ended without an end marker")
	if checkout := resultsNamed(sink, "Checkout"); len(checkout) != 1 || checkout[0].Iteration != 4 || vu.currentIteration.Load() != 0 {
		t.Fatalf("finalized %+v iteration now %d", checkout, vu.currentIteration.Load())
	}
}

// A parallel block that genuinely fails ends a fail-fast iteration as a
// failure, not as a stop.
func TestParallelFailFastIsAGenuineFailure(t *testing.T) {
	no := false
	vu := &VUser{ID: 2, Group: "parallel", Proto: &parallelTestProtocol{}, Variables: map[string]string{}, Metrics: NewMetricsCollector(32),
		Script: &dsl.Script{Name: "Parallel", Protocol: parallelTestProtocolName, Config: dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1}, ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: &no}},
			Actions: []dsl.Action{{Name: "fan-out", Type: "control_parallel", Control: &dsl.ControlFlow{Concurrency: 2, Branches: []dsl.ParallelBranch{{Name: "a", Steps: []dsl.Action{{Name: "transport-error", Type: "GET", Target: "/"}}}, {Name: "b", Steps: []dsl.Action{{Name: "b-call", Type: "GET", Target: "/b"}}}}}}}}}
	sink := &identitySink{}
	vu.ResultSink = sink
	vu.Run(context.Background())
	iteration := resultsNamed(sink, "Parallel")
	if len(iteration) != 1 || iteration[0].Success || iteration[0].Interrupted {
		t.Fatalf("iteration transaction %+v", iteration)
	}
}

func sourceTx(name, function string) dsl.Action {
	return dsl.Action{Name: name, Type: "source_transaction", Params: map[string]any{"function": function}}
}

// A transaction left open when a stop ends the VUser is the stop's: its
// failure is interrupted and does not end the iteration. One left open when
// the VUser ends on its own (a genuine failure, fail-fast) ends its
// iteration, in the iteration that opened it, over its wall-clock span.
func TestOpenTransactionsAtExitSayWhetherTheIterationEndedOnItsOwn(t *testing.T) {
	run, stop := context.WithCancel(context.Background())
	defer stop()
	stopped := newControlFlowVUser(&stoppingProtocol{stopOn: "login", stop: stop}, nil, []dsl.Action{sourceTx("Checkout", "lr_start_transaction"), click("login"), click("pay"), sourceTx("Checkout", "lr_end_transaction")})
	stopped.ActionBoundaryCtx, stopped.HardStopCtx = run, context.Background()
	sink := &identitySink{}
	stopped.ResultSink = sink
	stopped.Run(run)
	if got := resultsNamed(sink, "Checkout"); len(got) != 1 || got[0].Success || !got[0].Interrupted || got[0].EndsIteration || got[0].Iteration != 1 {
		t.Fatalf("stopped VUser's open transaction %+v", got)
	}

	failing := newControlFlowVUser(&stoppingProtocol{stop: func() {}, failOn: "login"}, nil, []dsl.Action{sourceTx("Checkout", "lr_start_transaction"), click("login"), click("pay"), sourceTx("Checkout", "lr_end_transaction")})
	sink = &identitySink{}
	failing.ResultSink = sink
	failing.Run(context.Background())
	got := resultsNamed(sink, "Checkout")
	if len(got) != 1 || got[0].Success || got[0].Interrupted || !got[0].EndsIteration || got[0].Iteration != 1 || got[0].WallElapsed <= 0 {
		t.Fatalf("fail-fast VUser's open transaction %+v", got)
	}
	if login := resultsNamed(sink, "login"); len(login) != 1 || login[0].EndsIteration {
		t.Fatalf("an ordinary step was marked as ending the iteration: %+v", login)
	}
}

// The iteration transaction is recorded when the iteration ends on its own:
// it ends the iteration. One a stop abandons does not.
func TestIterationTransactionEndsItsIteration(t *testing.T) {
	vu := newControlFlowVUser(&captureProtocol{}, nil, []dsl.Action{click("login")})
	vu.Script.Config.RunLogic.Iterations = 2
	sink := &identitySink{}
	vu.ResultSink = sink
	vu.Run(context.Background())
	got := resultsNamed(sink, "ControlFlow")
	if len(got) != 2 || !got[0].EndsIteration || !got[1].EndsIteration || got[1].Iteration != 2 {
		t.Fatalf("iteration transactions %+v", got)
	}
	run, stop := context.WithCancel(context.Background())
	defer stop()
	abandoned := newControlFlowVUser(&stoppingProtocol{stopOn: "login", stop: stop}, nil, []dsl.Action{click("login"), click("checkout")})
	abandoned.ActionBoundaryCtx, abandoned.HardStopCtx = run, context.Background()
	sink = &identitySink{}
	abandoned.ResultSink = sink
	abandoned.Run(run)
	if got := resultsNamed(sink, "ControlFlow"); len(got) != 1 || got[0].EndsIteration || !got[0].Interrupted {
		t.Fatalf("abandoned iteration transaction %+v", got)
	}
}

// A transaction still open when the next iteration starts is carried into
// it, so its iteration never goes backward.
func TestOpenTransactionsAreCarriedIntoTheNextIteration(t *testing.T) {
	vu := &VUser{sourceTransactions: map[string]*sourceTransactionRuntimeState{
		"checkout": {name: "Checkout", iteration: 1},
		"init":     {name: "Init", iteration: 0},
	}}
	vu.carryOpenSourceTransactions(3)
	if vu.sourceTransactions["checkout"].iteration != 3 || vu.sourceTransactions["init"].iteration != 0 {
		t.Fatalf("carried %+v", vu.sourceTransactions)
	}
}

// reservingProtocol lets a peer VUser reserve a goal slot when the named
// action runs.
type reservingProtocol struct {
	captureProtocol
	on      string
	reserve func()
}

func (p *reservingProtocol) Execute(ctx context.Context, action *protocol.Action) (*protocol.Result, error) {
	if action.Name == p.on {
		p.reserve()
	}
	return &protocol.Result{ActionName: action.Name, StartTime: time.Now(), Duration: time.Millisecond, Success: true}, nil
}

// A goal limiter can refuse a transaction slot without cancelling anything
// (another VUser holds a pending reservation): the VUser abandons the
// iteration at that goal boundary, so the transactions it leaves open are
// the boundary's, interrupted, and do not end the iteration.
func TestGoalBoundaryRefusalLeavesOpenTransactionsInterrupted(t *testing.T) {
	limiter := NewExecutionGoalLimiter(dsl.RunLogicConfig{MaxTransactions: 2}, func() {})
	// Inside E2E, another VUser reserves the last slot and has not committed
	// it, so starting Pay is refused without stopping anything.
	proto := &reservingProtocol{on: "browse", reserve: func() {
		if !limiter.TryReserveTransaction() {
			t.Error("the peer reservation failed")
		}
	}}
	vu := newControlFlowVUser(proto, nil, []dsl.Action{sourceTx("E2E", "lr_start_transaction"), click("browse"), sourceTx("Pay", "lr_start_transaction"), click("pay"), sourceTx("Pay", "lr_end_transaction"), sourceTx("E2E", "lr_end_transaction")})
	vu.ExecutionGoals = limiter
	sink := &identitySink{}
	vu.ResultSink = sink
	vu.Run(context.Background())
	e2e := resultsNamed(sink, "E2E")
	if len(e2e) != 1 || e2e[0].Success || !e2e[0].Interrupted || e2e[0].EndsIteration {
		t.Fatalf("E2E cut at the goal boundary %+v", e2e)
	}
	if pay := resultsNamed(sink, "pay"); len(pay) != 0 {
		t.Fatalf("pay ran past the goal boundary: %+v", pay)
	}
}
