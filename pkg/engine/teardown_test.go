package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// blockingTeardownProtocol blocks in Teardown until its context is cancelled. If
// teardown ran under an uncancellable context.Background() it would hang forever;
// the bounded teardown context must unblock it.
type blockingTeardownProtocol struct{}

func (p *blockingTeardownProtocol) Name() string                                   { return "blocking-teardown-stub" }
func (p *blockingTeardownProtocol) Init(_ context.Context, _ map[string]any) error { return nil }
func (p *blockingTeardownProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	return &protocol.Result{ActionName: action.Name, Success: true, Duration: time.Millisecond}, nil
}
func (p *blockingTeardownProtocol) Teardown(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}
func (p *blockingTeardownProtocol) HealthCheck(_ context.Context) error { return nil }

type blockingShakeoutTeardownActionProtocol struct{}

type teardownRegressionProtocol struct{}

type teardownActionProtocol struct {
	cancel  context.CancelFunc
	actions []string
}

func (*teardownActionProtocol) Name() string { return "teardown-action-protocol" }
func (*teardownActionProtocol) Init(context.Context, map[string]any) error {
	return nil
}
func (p *teardownActionProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	p.actions = append(p.actions, action.Name)
	if action.Name == "trigger stop" && p.cancel != nil {
		p.cancel()
	}
	return &protocol.Result{ActionName: action.Name, Success: true, Duration: time.Millisecond}, nil
}
func (*teardownActionProtocol) Teardown(context.Context) error    { return nil }
func (*teardownActionProtocol) HealthCheck(context.Context) error { return nil }

var teardownRegressionState = struct {
	sync.Mutex
	initCalls  int
	failInitAt int
	failAction string
	actions    map[string]int
}{actions: make(map[string]int)}

func (*teardownRegressionProtocol) Name() string { return "teardown-regression-stub" }
func (*teardownRegressionProtocol) Init(context.Context, map[string]any) error {
	teardownRegressionState.Lock()
	defer teardownRegressionState.Unlock()
	teardownRegressionState.initCalls++
	if teardownRegressionState.failInitAt > 0 && teardownRegressionState.initCalls == teardownRegressionState.failInitAt {
		return errors.New("injected protocol initialization failure")
	}
	return nil
}
func (*teardownRegressionProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	teardownRegressionState.Lock()
	defer teardownRegressionState.Unlock()
	teardownRegressionState.actions[action.Name]++
	success := action.Name != teardownRegressionState.failAction
	return &protocol.Result{ActionName: action.Name, Success: success, ErrorMessage: map[bool]string{false: "injected action failure"}[success]}, nil
}
func (*teardownRegressionProtocol) Teardown(context.Context) error    { return nil }
func (*teardownRegressionProtocol) HealthCheck(context.Context) error { return nil }

func resetTeardownRegression(failAction string, failInitAt int) {
	teardownRegressionState.Lock()
	defer teardownRegressionState.Unlock()
	teardownRegressionState.initCalls = 0
	teardownRegressionState.failInitAt = failInitAt
	teardownRegressionState.failAction = failAction
	teardownRegressionState.actions = make(map[string]int)
}

func teardownRegressionActions(name string) int {
	teardownRegressionState.Lock()
	defer teardownRegressionState.Unlock()
	return teardownRegressionState.actions[name]
}

func (*blockingShakeoutTeardownActionProtocol) Name() string { return "blocking-shakeout-action-stub" }
func (*blockingShakeoutTeardownActionProtocol) Init(context.Context, map[string]any) error {
	return nil
}
func (*blockingShakeoutTeardownActionProtocol) Execute(ctx context.Context, action *protocol.Action) (*protocol.Result, error) {
	if action.Name == "blocking-teardown-action" {
		<-ctx.Done()
		return &protocol.Result{ActionName: action.Name, Success: false, ErrorMessage: ctx.Err().Error()}, nil
	}
	return &protocol.Result{ActionName: action.Name, Success: true}, nil
}
func (*blockingShakeoutTeardownActionProtocol) Teardown(context.Context) error    { return nil }
func (*blockingShakeoutTeardownActionProtocol) HealthCheck(context.Context) error { return nil }

func init() {
	protocol.Register("blocking-teardown-stub", func() protocol.Protocol { return &blockingTeardownProtocol{} })
	protocol.Register("blocking-shakeout-action-stub", func() protocol.Protocol { return &blockingShakeoutTeardownActionProtocol{} })
	protocol.Register("teardown-regression-stub", func() protocol.Protocol { return &teardownRegressionProtocol{} })
}

func TestShakeoutProtocolTeardownIsBounded(t *testing.T) {
	t.Setenv("APEXLOAD_TEARDOWN_TIMEOUT", "20ms")
	script := &dsl.Script{
		Name:     "blocking-shakeout-protocol-teardown",
		Protocol: "blocking-teardown-stub",
		Actions:  []dsl.Action{{Name: "ok", Type: "request", Target: "/"}},
	}
	start := time.Now()
	if err := RunScriptShakeout(context.Background(), "shakeout", script); err != nil {
		t.Fatalf("RunScriptShakeout returned error: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatalf("shakeout protocol teardown took %s, want configured bounded cleanup", elapsed)
	}
}

func TestShakeoutTeardownActionsAreBounded(t *testing.T) {
	t.Setenv("APEXLOAD_TEARDOWN_TIMEOUT", "20ms")
	script := &dsl.Script{
		Name:     "blocking-shakeout-action-teardown",
		Protocol: "blocking-shakeout-action-stub",
		Actions:  []dsl.Action{{Name: "ok", Type: "request", Target: "/"}},
		Teardown: []dsl.Action{{Name: "blocking-teardown-action", Type: "request", Target: "/"}},
	}
	start := time.Now()
	if err := RunScriptShakeout(context.Background(), "shakeout", script); err != nil {
		t.Fatalf("RunScriptShakeout returned error: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatalf("shakeout teardown action took %s, want configured bounded cleanup", elapsed)
	}
}

func TestVUserPausedRunTeardownThinkTimeUsesBoundedWallClock(t *testing.T) {
	t.Setenv("APEXLOAD_TEARDOWN_TIMEOUT", "40ms")
	protoStub := &teardownActionProtocol{}
	activeSleepCalled := make(chan struct{})
	vu := &VUser{
		Proto:        protoStub,
		Metrics:      NewMetricsCollector(16),
		PauseControl: &blockingPacingController{started: activeSleepCalled},
		Script: &dsl.Script{
			Name:     "paused-run-teardown-think-time",
			Protocol: "http",
			Config:   dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1}},
			Actions:  []dsl.Action{{Name: "main", Type: "GET", Method: "GET", Target: "/main"}},
			Teardown: []dsl.Action{
				{
					Name:           "cleanup one",
					Type:           "GET",
					Method:         "GET",
					Target:         "/cleanup-one",
					ThinkTimeAfter: true,
					ThinkTime:      dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 5},
				},
				{Name: "cleanup two", Type: "GET", Method: "GET", Target: "/cleanup-two"},
			},
		},
	}

	startedAt := time.Now()
	vu.Run(context.Background())
	if elapsed := time.Since(startedAt); elapsed >= 250*time.Millisecond {
		t.Fatalf("paused teardown took %s, want ordinary wall-clock think time", elapsed)
	}
	if got := strings.Join(protoStub.actions, ","); got != "main,cleanup one,cleanup two" {
		t.Fatalf("actions = %q, teardown stalled before later cleanup", got)
	}
	select {
	case <-activeSleepCalled:
		t.Fatal("teardown delegated think time to the paused active-time controller")
	default:
	}
}

func TestVUserGracefulStopTeardownSourceScopeUsesFreshBoundary(t *testing.T) {
	softCtx, softCancel := context.WithCancel(context.Background())
	defer softCancel()
	runLogic := dsl.RunLogicConfig{
		Iterations:       1,
		AutoTransactions: "none",
		MaxTransactions:  1,
	}
	limiter := NewExecutionGoalLimiter(runLogic, softCancel)
	protoStub := &teardownActionProtocol{cancel: softCancel}
	sink := &sequenceCaptureSink{}
	vu := &VUser{
		Proto:             protoStub,
		Metrics:           NewMetricsCollector(16),
		ResultSink:        sink,
		ExecutionGoals:    limiter,
		HardStopCtx:       context.Background(),
		ActionBoundaryCtx: softCtx,
		Script: &dsl.Script{
			Name:     "graceful-stop-teardown-source-scope",
			Protocol: "http",
			Config:   dsl.ScriptConfig{RunLogic: runLogic},
			Actions:  []dsl.Action{{Name: "trigger stop", Type: "GET", Method: "GET", Target: "/stop"}},
			Teardown: []dsl.Action{
				sourceTransactionAction("lr_start_transaction", ""),
				{Name: "scoped cleanup", Type: "GET", Method: "GET", Target: "/cleanup"},
				sourceTransactionAction("lr_end_transaction", "LR_PASS"),
			},
		},
	}

	vu.Run(softCtx)

	if got := strings.Join(protoStub.actions, ","); got != "trigger stop,scoped cleanup" {
		t.Fatalf("actions = %q, canceled run boundary aborted teardown source scope", got)
	}
	if len(sink.transactions) != 1 || !sink.transactions[0].Success {
		t.Fatalf("transactions = %#v, want completed teardown source transaction", sink.transactions)
	}
}

func TestVUserAndShakeoutRunDSLTeardownAfterInitFailure(t *testing.T) {
	newScript := func() *dsl.Script {
		return &dsl.Script{
			Name:     "init cleanup regression",
			Protocol: "teardown-regression-stub",
			Config:   dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1}},
			Init: []dsl.Action{
				{Name: "init-success", Type: "request", Target: "/setup"},
				{Name: "init-failure", Type: "request", Target: "/fail"},
			},
			Actions:  []dsl.Action{{Name: "must-not-run", Type: "request", Target: "/main"}},
			Teardown: []dsl.Action{{Name: "cleanup", Type: "request", Target: "/cleanup"}},
		}
	}

	resetTeardownRegression("init-failure", 0)
	vu := &VUser{
		ID:        1,
		Proto:     &teardownRegressionProtocol{},
		Script:    newScript(),
		Metrics:   NewMetricsCollector(16),
		Variables: make(map[string]string),
	}
	vu.Run(context.Background())
	if got := teardownRegressionActions("cleanup"); got != 1 {
		t.Fatalf("VUser cleanup actions = %d, want exactly one", got)
	}
	if got := teardownRegressionActions("must-not-run"); got != 0 {
		t.Fatalf("VUser main actions = %d after init failure", got)
	}

	resetTeardownRegression("init-failure", 0)
	if err := RunScriptShakeout(context.Background(), "shakeout", newScript()); err == nil {
		t.Fatal("shakeout unexpectedly succeeded")
	}
	if got := teardownRegressionActions("cleanup"); got != 1 {
		t.Fatalf("shakeout cleanup actions = %d, want exactly one", got)
	}
}

func TestVUserTeardownOnlyLifecycleStartsAfterIterationBoundary(t *testing.T) {
	newVUser := func() *VUser {
		return &VUser{
			ID:    1,
			Proto: &teardownRegressionProtocol{},
			Script: &dsl.Script{
				Name:     "teardown-only lifecycle",
				Protocol: "teardown-regression-stub",
				Config:   dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1}},
				Teardown: []dsl.Action{{Name: "cleanup", Type: "request", Target: "/cleanup"}},
			},
			Metrics:   NewMetricsCollector(16),
			Variables: make(map[string]string),
		}
	}

	resetTeardownRegression("", 0)
	newVUser().Run(context.Background())
	if got := teardownRegressionActions("cleanup"); got != 1 {
		t.Fatalf("cleanup actions after iteration boundary = %d, want 1", got)
	}

	resetTeardownRegression("", 0)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	newVUser().Run(canceled)
	if got := teardownRegressionActions("cleanup"); got != 0 {
		t.Fatalf("cleanup actions before user work started = %d, want 0", got)
	}
}

func TestShakeoutRunsDSLTeardownAfterDataSourceStartupFailure(t *testing.T) {
	resetTeardownRegression("", 0)
	script := &dsl.Script{
		Name:        "data startup cleanup",
		Protocol:    "teardown-regression-stub",
		DataSources: []dsl.DataSource{{Type: "csv"}},
		Actions:     []dsl.Action{{Name: "must-not-run", Type: "request", Target: "/main"}},
		Teardown:    []dsl.Action{{Name: "cleanup", Type: "request", Target: "/cleanup"}},
	}
	if err := RunScriptShakeout(context.Background(), "shakeout", script); err == nil || !strings.Contains(err.Error(), "data source init") {
		t.Fatalf("shakeout error = %v, want data source startup failure", err)
	}
	if got := teardownRegressionActions("cleanup"); got != 1 {
		t.Fatalf("shakeout cleanup actions after data startup failure = %d, want exactly one", got)
	}
	if got := teardownRegressionActions("must-not-run"); got != 0 {
		t.Fatalf("shakeout main actions after data startup failure = %d", got)
	}
}

func TestAtOnceLifecycleRunsTeardownAfterPartialInitFailure(t *testing.T) {
	resetTeardownRegression("init-failure", 0)
	script := &dsl.Script{
		Name:     "partial at-once init",
		Protocol: "teardown-regression-stub",
		Config:   dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1}},
		Init: []dsl.Action{
			{Name: "init-success", Type: "request", Target: "/setup"},
			{Name: "init-failure", Type: "request", Target: "/fail"},
		},
		Actions:  []dsl.Action{{Name: "main", Type: "request", Target: "/main"}},
		Teardown: []dsl.Action{{Name: "cleanup", Type: "request", Target: "/cleanup"}},
	}
	engine, err := NewMultiGroup(MultiGroupConfig{
		Duration: time.Second,
		Groups:   []GroupConfig{{Name: "at-once", Script: script, VUsers: 1, InitStrategy: "at_once"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Run(context.Background()); err == nil {
		t.Fatal("partial at-once init unexpectedly succeeded")
	}
	if got := teardownRegressionActions("cleanup"); got != 1 {
		t.Fatalf("partial at-once cleanup actions = %d, want exactly one", got)
	}
}

func TestAtOnceTeardownContinuesAfterOrdinaryActionFailure(t *testing.T) {
	resetTeardownRegression("teardown-failure", 0)
	script := &dsl.Script{
		Name:     "best effort at-once teardown",
		Protocol: "teardown-regression-stub",
		Config:   dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1}},
		Actions:  []dsl.Action{{Name: "main", Type: "request", Target: "/main"}},
		Teardown: []dsl.Action{
			{Name: "teardown-failure", Type: "request", Target: "/fail"},
			{Name: "cleanup", Type: "request", Target: "/cleanup"},
		},
	}
	engine, err := NewMultiGroup(MultiGroupConfig{
		Duration: time.Second,
		Groups:   []GroupConfig{{Name: "at-once", Script: script, VUsers: 1, InitStrategy: "at_once"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Run(context.Background()); err != nil {
		t.Fatalf("best-effort at-once teardown failed the run: %v", err)
	}
	if got := teardownRegressionActions("teardown-failure"); got != 1 {
		t.Fatalf("failing teardown executions = %d, want 1", got)
	}
	if got := teardownRegressionActions("cleanup"); got != 1 {
		t.Fatalf("cleanup executions = %d, want 1 after prior failure", got)
	}
}

func TestAtOnceLifecycleRunsTeardownAfterLaterGroupStartupFailure(t *testing.T) {
	for _, open := range []bool{false, true} {
		name := "closed"
		if open {
			name = "open"
		}
		t.Run(name, func(t *testing.T) {
			resetTeardownRegression("", 3)
			first := &dsl.Script{
				Name:     "initialized group",
				Protocol: "teardown-regression-stub",
				Config:   dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1}},
				Init:     []dsl.Action{{Name: "init-success", Type: "request", Target: "/setup"}},
				Actions:  []dsl.Action{{Name: "main", Type: "request", Target: "/main"}},
				Teardown: []dsl.Action{{Name: "cleanup", Type: "request", Target: "/cleanup"}},
			}
			second := &dsl.Script{
				Name:     "failing group",
				Protocol: "teardown-regression-stub",
				Config:   dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1}},
				Actions:  []dsl.Action{{Name: "second-main", Type: "request", Target: "/main"}},
			}
			if open {
				second.Config.RunLogic.WorkloadModel = "open"
				second.Config.Pacing = dsl.PacingConfig{Type: "tps", TPS: 1000}
			}
			engine, err := NewMultiGroup(MultiGroupConfig{
				Duration: time.Second,
				Groups: []GroupConfig{
					{Name: "initialized", Script: first, VUsers: 1, InitStrategy: "at_once"},
					{Name: "startup-failure", Script: second, VUsers: 1},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := engine.Run(context.Background()); err == nil {
				t.Fatal("later group startup unexpectedly succeeded")
			}
			if got := teardownRegressionActions("cleanup"); got != 1 {
				t.Fatalf("cleanup actions after %s startup failure = %d, want exactly one", name, got)
			}
		})
	}
}

func TestTeardownTimeout_EnvOverride(t *testing.T) {
	t.Setenv("APEXLOAD_TEARDOWN_TIMEOUT", "250ms")
	if got := teardownTimeout(); got != 250*time.Millisecond {
		t.Fatalf("teardownTimeout() = %v, want 250ms", got)
	}

	t.Setenv("APEXLOAD_TEARDOWN_TIMEOUT", "garbage")
	if got := teardownTimeout(); got != defaultTeardownTimeout {
		t.Fatalf("teardownTimeout() with bad value = %v, want default %v", got, defaultTeardownTimeout)
	}
}

func TestEngine_Run_BlockingTeardownIsBounded(t *testing.T) {
	// A short timeout keeps the test fast; without bounding, the blocking
	// Teardown would hang on context.Background() and the test would time out.
	t.Setenv("APEXLOAD_TEARDOWN_TIMEOUT", "150ms")

	script := &dsl.Script{
		Name:     "blocking-teardown-run",
		Protocol: "blocking-teardown-stub",
		Actions:  []dsl.Action{{Name: "ok", Type: "request", Target: "/"}},
	}

	e, err := New(Config{Script: script, VUsers: 3, Duration: 100 * time.Millisecond})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	start := time.Now()
	if _, err := e.Run(context.Background()); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	// Run duration (100ms) + bounded teardown (150ms) + slack. A regression to
	// an unbounded teardown would block well past this.
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("Run took %v; teardown appears unbounded", elapsed)
	}
}

type contextIgnoringTeardownProtocol struct {
	entered chan struct{}
	release chan struct{}
}

func (p *contextIgnoringTeardownProtocol) Teardown(context.Context) error {
	close(p.entered)
	<-p.release
	return nil
}

type unprovenContainmentTeardownProtocol struct{}

func (*unprovenContainmentTeardownProtocol) Teardown(context.Context) error {
	return fmt.Errorf("adapter-specific containment detail: %w", protocol.ErrContainmentUnproven)
}

func TestCallProtocolTeardownBoundsAdapterThatIgnoresContext(t *testing.T) {
	proto := &contextIgnoringTeardownProtocol{entered: make(chan struct{}), release: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := callProtocolTeardown(ctx, proto, nil)
	if err == nil || !strings.Contains(err.Error(), "exceeded deadline") {
		t.Fatalf("callProtocolTeardown error = %v, want bounded deadline error", err)
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatalf("context-ignoring teardown took %s", elapsed)
	}
	select {
	case <-proto.entered:
	default:
		t.Fatal("adapter teardown was not invoked")
	}
	// Let the isolated adapter goroutine exit so the regression test itself does
	// not retain a goroutine after proving the caller is independently bounded.
	close(proto.release)
}

func TestStrictProtocolTeardownRemainsOnExactDonePathAfterDeadline(t *testing.T) {
	proto := &contextIgnoringTeardownProtocol{entered: make(chan struct{}), release: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- callProtocolTeardownWithMode(ctx, proto, nil, make(chan struct{}, 1), true)
	}()
	select {
	case <-proto.entered:
	case <-time.After(time.Second):
		t.Fatal("strict teardown did not begin")
	}
	<-ctx.Done()
	select {
	case err := <-done:
		t.Fatalf("strict teardown detached at its context deadline: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(proto.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("strict teardown after release: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("strict teardown did not rejoin after adapter returned")
	}
}

func TestContainmentUnprovenReturnsToToolingButRetainsStrictGeneratorBarrier(t *testing.T) {
	proto := &unprovenContainmentTeardownProtocol{}
	if err := callProtocolTeardownWithMode(context.Background(), proto, nil, make(chan struct{}, 1), false); !errors.Is(err, protocol.ErrContainmentUnproven) {
		t.Fatalf("tooling teardown error = %v, want ErrContainmentUnproven", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- callProtocolTeardownWithMode(ctx, proto, nil, make(chan struct{}, 1), true)
	}()
	<-ctx.Done()
	select {
	case err := <-done:
		t.Fatalf("strict teardown released an unproven containment barrier: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
}

func TestCallProtocolTeardownSaturationHonorsDeadlineWithoutSpawningAdapter(t *testing.T) {
	slots := make(chan struct{}, 1)
	first := &contextIgnoringTeardownProtocol{entered: make(chan struct{}), release: make(chan struct{})}
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- callProtocolTeardownWithSlots(context.Background(), first, nil, slots)
	}()
	select {
	case <-first.entered:
	case <-time.After(time.Second):
		t.Fatal("first teardown did not acquire the only slot")
	}

	secondEntered := make(chan struct{})
	second := &contextIgnoringTeardownProtocol{entered: secondEntered, release: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := callProtocolTeardownWithSlots(ctx, second, nil, slots)
	if err == nil || !strings.Contains(err.Error(), "could not start before deadline") {
		t.Fatalf("saturated teardown error = %v", err)
	}
	select {
	case <-secondEntered:
		t.Fatal("saturated teardown spawned an adapter goroutine")
	default:
	}

	close(first.release)
	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatalf("first teardown: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("first teardown did not release its slot")
	}
}
