package engine

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

type stubProtocol struct {
	initCalled     bool
	executeCalled  int
	teardownCalled bool
	initConfig     map[string]any
}

type slowStubProtocol struct{}
type lifecycleCountingProtocol struct{}
type goalInitCancellationProtocol struct {
	state *goalInitCancellationState
	index int64
}

type goalInitCancellationState struct {
	nextInit       atomic.Int64
	executions     atomic.Int64
	blockedStarted chan struct{}
	blockedStopped chan struct{}
	startOnce      sync.Once
	stopOnce       sync.Once
}

type startupIsolationProtocol struct {
	state *startupIsolationState
	group string
}

type startupIsolationState struct {
	failingInitStarted   chan struct{}
	releaseFailingInit   chan struct{}
	failingActionStarted chan struct{}
	laterInitStarted     chan struct{}
	laterActionStarted   chan struct{}
	failingInitCalls     atomic.Int64
	failingOnce          sync.Once
	failingActionOnce    sync.Once
	laterInitOnce        sync.Once
	laterActionOnce      sync.Once
	releaseOnce          sync.Once
}

var (
	goalInitCancellationMu           sync.Mutex
	goalInitCancellationStateCurrent *goalInitCancellationState
	startupIsolationMu               sync.Mutex
	startupIsolationStateCurrent     *startupIsolationState
)

var lifecycleInitActions atomic.Int64
var lifecycleMainActions atomic.Int64
var lifecycleTeardownActions atomic.Int64

func (s *stubProtocol) Name() string { return "stub" }
func (s *stubProtocol) Init(_ context.Context, config map[string]any) error {
	s.initCalled = true
	s.initConfig = config
	return nil
}
func (s *stubProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	s.executeCalled++
	return &protocol.Result{
		ActionName: action.Name,
		Success:    true,
		Duration:   time.Millisecond,
	}, nil
}
func (s *stubProtocol) Teardown(_ context.Context) error {
	s.teardownCalled = true
	return nil
}
func (s *stubProtocol) HealthCheck(_ context.Context) error { return nil }

func (s *slowStubProtocol) Name() string                                   { return "slow-stub" }
func (s *slowStubProtocol) Init(_ context.Context, _ map[string]any) error { return nil }
func (s *slowStubProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	time.Sleep(200 * time.Millisecond)
	return &protocol.Result{
		ActionName: action.Name,
		Success:    true,
		Duration:   200 * time.Millisecond,
	}, nil
}
func (s *slowStubProtocol) Teardown(_ context.Context) error    { return nil }
func (s *slowStubProtocol) HealthCheck(_ context.Context) error { return nil }

func (p *lifecycleCountingProtocol) Name() string { return "lifecycle-counting" }
func (p *lifecycleCountingProtocol) Init(_ context.Context, _ map[string]any) error {
	return nil
}
func (p *lifecycleCountingProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	switch action.Name {
	case "setup":
		lifecycleInitActions.Add(1)
	case "main":
		lifecycleMainActions.Add(1)
	case "cleanup":
		lifecycleTeardownActions.Add(1)
	}
	return &protocol.Result{ActionName: action.Name, Success: true, Duration: time.Millisecond}, nil
}
func (p *lifecycleCountingProtocol) Teardown(_ context.Context) error    { return nil }
func (p *lifecycleCountingProtocol) HealthCheck(_ context.Context) error { return nil }

func (*goalInitCancellationProtocol) Name() string { return "goal-init-cancellation-test" }
func (p *goalInitCancellationProtocol) Init(ctx context.Context, _ map[string]any) error {
	if p.state == nil {
		return nil
	}
	p.index = p.state.nextInit.Add(1) - 1
	if p.index == 0 {
		return nil
	}
	p.state.startOnce.Do(func() { close(p.state.blockedStarted) })
	<-ctx.Done()
	p.state.stopOnce.Do(func() { close(p.state.blockedStopped) })
	return ctx.Err()
}
func (*goalInitCancellationProtocol) Execute(context.Context, *protocol.Action) (*protocol.Result, error) {
	panic("goal init cancellation protocol requires ExecuteStream")
}
func (p *goalInitCancellationProtocol) ExecuteStream(ctx context.Context, action *protocol.Action, emit func(*protocol.Result) error) (*protocol.Result, error) {
	if p.state != nil {
		select {
		case <-p.state.blockedStarted:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		p.state.executions.Add(1)
	}
	if emit != nil {
		if err := emit(&protocol.Result{
			ActionName:          "goal transaction",
			BusinessTransaction: true,
			Success:             true,
			Duration:            time.Millisecond,
		}); err != nil {
			return nil, err
		}
	}
	return &protocol.Result{ActionName: action.Name, Success: true, Duration: time.Millisecond}, nil
}
func (*goalInitCancellationProtocol) Teardown(context.Context) error    { return nil }
func (*goalInitCancellationProtocol) HealthCheck(context.Context) error { return nil }

func (*startupIsolationProtocol) Name() string { return "startup-isolation-test" }
func (p *startupIsolationProtocol) Init(ctx context.Context, config map[string]any) error {
	if group, ok := config["group_name"].(string); ok {
		p.group = group
	}
	if p.state == nil {
		return nil
	}
	switch p.group {
	case "startup-failure":
		// The first configured VUser is healthy. The second one blocks and
		// fails so the regression covers the whole configured group, not only
		// a first-user startup probe.
		if p.state.failingInitCalls.Add(1) == 1 {
			return nil
		}
		p.state.failingOnce.Do(func() { close(p.state.failingInitStarted) })
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-p.state.releaseFailingInit:
			return errors.New("injected startup failure")
		}
	case "must-not-start":
		p.state.laterInitOnce.Do(func() { close(p.state.laterInitStarted) })
	}
	return nil
}
func (p *startupIsolationProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	if p.state != nil {
		switch p.group {
		case "startup-failure":
			p.state.failingActionOnce.Do(func() { close(p.state.failingActionStarted) })
		case "must-not-start":
			p.state.laterActionOnce.Do(func() { close(p.state.laterActionStarted) })
		}
	}
	return &protocol.Result{ActionName: action.Name, Success: true, Duration: time.Millisecond}, nil
}
func (*startupIsolationProtocol) Teardown(context.Context) error    { return nil }
func (*startupIsolationProtocol) HealthCheck(context.Context) error { return nil }

func init() {
	protocol.Register("stub", func() protocol.Protocol {
		return &stubProtocol{}
	})
	protocol.Register("slow-stub", func() protocol.Protocol {
		return &slowStubProtocol{}
	})
	protocol.Register("lifecycle-counting", func() protocol.Protocol {
		return &lifecycleCountingProtocol{}
	})
	protocol.Register("goal-init-cancellation-test", func() protocol.Protocol {
		goalInitCancellationMu.Lock()
		defer goalInitCancellationMu.Unlock()
		return &goalInitCancellationProtocol{state: goalInitCancellationStateCurrent, index: -1}
	})
	protocol.Register("startup-isolation-test", func() protocol.Protocol {
		startupIsolationMu.Lock()
		defer startupIsolationMu.Unlock()
		return &startupIsolationProtocol{state: startupIsolationStateCurrent}
	})
}

func installGoalInitCancellationState(t *testing.T) *goalInitCancellationState {
	t.Helper()
	state := &goalInitCancellationState{
		blockedStarted: make(chan struct{}),
		blockedStopped: make(chan struct{}),
	}
	goalInitCancellationMu.Lock()
	goalInitCancellationStateCurrent = state
	goalInitCancellationMu.Unlock()
	t.Cleanup(func() {
		goalInitCancellationMu.Lock()
		if goalInitCancellationStateCurrent == state {
			goalInitCancellationStateCurrent = nil
		}
		goalInitCancellationMu.Unlock()
	})
	return state
}

func installStartupIsolationState(t *testing.T) *startupIsolationState {
	t.Helper()
	state := &startupIsolationState{
		failingInitStarted:   make(chan struct{}),
		releaseFailingInit:   make(chan struct{}),
		failingActionStarted: make(chan struct{}),
		laterInitStarted:     make(chan struct{}),
		laterActionStarted:   make(chan struct{}),
	}
	startupIsolationMu.Lock()
	startupIsolationStateCurrent = state
	startupIsolationMu.Unlock()
	t.Cleanup(func() {
		state.releaseOnce.Do(func() { close(state.releaseFailingInit) })
		startupIsolationMu.Lock()
		if startupIsolationStateCurrent == state {
			startupIsolationStateCurrent = nil
		}
		startupIsolationMu.Unlock()
	})
	return state
}

func TestNew_Valid(t *testing.T) {
	script := &dsl.Script{
		Name:     "test",
		Protocol: "stub",
		Actions:  []dsl.Action{{Name: "a", Type: "request", Target: "/"}},
	}

	e, err := New(Config{
		Script:   script,
		VUsers:   5,
		Duration: time.Second,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.vusers != 5 {
		t.Errorf("vusers = %d, want 5", e.vusers)
	}
}

func TestNew_NilScript(t *testing.T) {
	_, err := New(Config{VUsers: 1, Duration: time.Second})
	if err == nil {
		t.Fatal("expected error for nil script")
	}
}

func TestNew_InvalidVUsers(t *testing.T) {
	script := &dsl.Script{Name: "t", Protocol: "stub", Actions: []dsl.Action{{Name: "a", Type: "r", Target: "/"}}}

	_, err := New(Config{Script: script, VUsers: 0, Duration: time.Second})
	if err == nil {
		t.Fatal("expected error for zero vusers")
	}

	_, err = New(Config{Script: script, VUsers: -1, Duration: time.Second})
	if err == nil {
		t.Fatal("expected error for negative vusers")
	}
}

func TestNew_InvalidDuration(t *testing.T) {
	script := &dsl.Script{Name: "t", Protocol: "stub", Actions: []dsl.Action{{Name: "a", Type: "r", Target: "/"}}}

	_, err := New(Config{Script: script, VUsers: 1, Duration: 0})
	if err == nil {
		t.Fatal("expected error for zero duration")
	}
}

func TestEngine_Run(t *testing.T) {
	script := &dsl.Script{
		Name:     "run-test",
		Protocol: "stub",
		Actions: []dsl.Action{
			{Name: "homepage", Type: "request", Target: "/"},
		},
	}

	e, err := New(Config{
		Script:   script,
		VUsers:   2,
		Duration: 500 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result, err := e.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.TotalTransactions == 0 {
		t.Error("expected some transactions")
	}
	if result.Duration == 0 {
		t.Error("expected non-zero duration")
	}
}

func TestEngine_RunStopsAtGlobalIterationGoal(t *testing.T) {
	script := &dsl.Script{
		Name:     "global-iteration-goal",
		Protocol: "stub",
		Config: dsl.ScriptConfig{
			RunLogic: dsl.RunLogicConfig{GlobalIterations: 5},
		},
		Actions: []dsl.Action{{Name: "homepage", Type: "request", Target: "/"}},
	}
	e, err := New(Config{Script: script, VUsers: 3, Duration: time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result, err := e.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.TotalTransactions != 5 {
		t.Fatalf("total transactions = %d, want 5 from global iteration goal", result.TotalTransactions)
	}
}

func TestEngine_RunCountsMultiActionIterationAsSingleTransaction(t *testing.T) {
	script := &dsl.Script{
		Name:     "multi-action-iteration",
		Protocol: "stub",
		Config: dsl.ScriptConfig{
			RunLogic: dsl.RunLogicConfig{Iterations: 3},
		},
		Actions: []dsl.Action{
			{Name: "home", Type: "request", Target: "/"},
			{Name: "search", Type: "request", Target: "/search"},
			{Name: "cart", Type: "request", Target: "/cart"},
			{Name: "checkout", Type: "request", Target: "/checkout"},
			{Name: "confirm", Type: "request", Target: "/confirm"},
		},
	}
	e, err := New(Config{Script: script, VUsers: 1, Duration: time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result, err := e.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.TotalTransactions != 3 {
		t.Fatalf("total transactions = %d, want one per successful script iteration", result.TotalTransactions)
	}
}

func TestEngine_RunCountsExplicitTransactionsWithinScript(t *testing.T) {
	script := &dsl.Script{
		Name:     "explicit-transactions",
		Protocol: "stub",
		Config: dsl.ScriptConfig{
			RunLogic: dsl.RunLogicConfig{Iterations: 1},
		},
		Actions: []dsl.Action{
			{Name: "Login", Type: "source_transaction", Params: map[string]any{"function": "lr_start_transaction"}},
			{Name: "login-page", Type: "request", Target: "/login"},
			{Name: "Login", Type: "source_transaction", Params: map[string]any{"function": "lr_end_transaction"}},
			{Name: "Checkout", Type: "source_transaction", Params: map[string]any{"function": "lr_start_transaction"}},
			{Name: "cart", Type: "request", Target: "/cart"},
			{Name: "payment", Type: "request", Target: "/payment"},
			{Name: "confirm", Type: "request", Target: "/confirm"},
			{Name: "Checkout", Type: "source_transaction", Params: map[string]any{"function": "lr_end_transaction"}},
		},
	}
	e, err := New(Config{Script: script, VUsers: 1, Duration: time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result, err := e.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.TotalTransactions != 2 {
		t.Fatalf("total transactions = %d, want two successful explicit transactions", result.TotalTransactions)
	}
}

func TestEngine_RunStopsAtGlobalTransactionGoal(t *testing.T) {
	script := &dsl.Script{
		Name:     "global-transaction-goal",
		Protocol: "stub",
		Config: dsl.ScriptConfig{
			RunLogic: dsl.RunLogicConfig{MaxTransactions: 4},
		},
		Actions: []dsl.Action{
			{Name: "home", Type: "request", Target: "/"},
			{Name: "checkout", Type: "request", Target: "/checkout"},
		},
	}
	e, err := New(Config{Script: script, VUsers: 3, Duration: time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result, err := e.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.TotalTransactions != 4 {
		t.Fatalf("total transactions = %d, want 4 from global transaction goal", result.TotalTransactions)
	}
}

func TestEngine_RunRecordsReservedIterationAfterGoalHit(t *testing.T) {
	script := &dsl.Script{
		Name:     "reserved-iteration-goal",
		Protocol: "slow-stub",
		Config: dsl.ScriptConfig{
			RunLogic: dsl.RunLogicConfig{MaxTransactions: 4},
		},
		Actions: []dsl.Action{{Name: "checkout", Type: "request", Target: "/checkout"}},
	}
	e, err := New(Config{Script: script, VUsers: 6, Duration: 3 * time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result, err := e.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.TotalTransactions != 4 {
		t.Fatalf("total transactions = %d, want all 4 reserved transactions recorded", result.TotalTransactions)
	}
}

func TestEngineGracefulStopFinishesCurrentActionAndStopsAtBoundary(t *testing.T) {
	state := installGracefulBoundaryState(t)
	script := &dsl.Script{
		Name: "direct-graceful-boundary", Protocol: "graceful-boundary-test",
		Config: dsl.ScriptConfig{
			RunLogic:  dsl.RunLogicConfig{Iterations: 1},
			ThinkTime: dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 5_000},
		},
		Actions: []dsl.Action{
			{Name: "first", Type: "request", Target: "/first", ThinkTimeAfter: true},
			{Name: "second", Type: "request", Target: "/second"},
		},
	}
	e, err := New(Config{Script: script, VUsers: 1, Duration: 10 * time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, runErr := e.Run(context.Background())
		done <- runErr
	}()
	select {
	case <-state.firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first action did not start")
	}

	e.GracefulStop()
	select {
	case <-state.firstReturned:
		t.Fatal("graceful stop interrupted the in-flight action")
	default:
	}
	close(state.releaseFirst)
	select {
	case runErr := <-done:
		if runErr != nil {
			t.Fatalf("Run: %v", runErr)
		}
	case <-time.After(time.Second):
		t.Fatal("direct engine graceful stop waited through action think time")
	}
	if got := boundaryActionNames(state); len(got) != 1 || got[0] != "first" {
		t.Fatalf("executed actions = %v, want only the in-flight first action", got)
	}
}

func TestEngineGracefulStopFinishesCurrentInitActionAndStopsAtBoundary(t *testing.T) {
	state := installGracefulBoundaryState(t)
	script := &dsl.Script{
		Name: "direct-graceful-init-boundary", Protocol: "graceful-boundary-test",
		Init: []dsl.Action{
			{Name: "first", Type: "request", Target: "/first"},
			{Name: "second", Type: "request", Target: "/second"},
		},
		Actions: []dsl.Action{{Name: "main", Type: "request", Target: "/main"}},
	}
	e, err := New(Config{Script: script, VUsers: 1, Duration: 10 * time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, runErr := e.Run(context.Background())
		done <- runErr
	}()
	select {
	case <-state.firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first init action did not start")
	}

	e.GracefulStop()
	select {
	case runErr := <-done:
		t.Fatalf("graceful stop interrupted the in-flight init action: %v", runErr)
	case <-time.After(50 * time.Millisecond):
	}
	close(state.releaseFirst)
	select {
	case runErr := <-done:
		if runErr != nil {
			t.Fatalf("Run: %v", runErr)
		}
	case <-time.After(time.Second):
		t.Fatal("graceful stop did not finish after the current init action")
	}
	if got := boundaryActionNames(state); len(got) != 1 || got[0] != "first" {
		t.Fatalf("executed actions = %v, want only the in-flight first init action", got)
	}
}

func TestEngineHardStopInterruptsCurrentInitAction(t *testing.T) {
	state := installGracefulBoundaryState(t)
	script := &dsl.Script{
		Name: "direct-hard-stop-init", Protocol: "graceful-boundary-test",
		Init: []dsl.Action{
			{Name: "first", Type: "request", Target: "/first"},
			{Name: "second", Type: "request", Target: "/second"},
		},
		Actions: []dsl.Action{{Name: "main", Type: "request", Target: "/main"}},
	}
	e, err := New(Config{Script: script, VUsers: 1, Duration: 10 * time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, runErr := e.Run(context.Background())
		done <- runErr
	}()
	select {
	case <-state.firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first init action did not start")
	}

	e.Stop()
	select {
	case runErr := <-done:
		if runErr != nil {
			t.Fatalf("Run: %v", runErr)
		}
	case <-time.After(time.Second):
		t.Fatal("hard stop did not interrupt the in-flight init action")
	}
	if got := boundaryActionNames(state); len(got) != 1 || got[0] != "first" {
		t.Fatalf("executed actions = %v, want only the interrupted first init action", got)
	}
}

func TestEngine_RunOpenWorkloadStopsAtGlobalTransactionGoal(t *testing.T) {
	script := &dsl.Script{
		Name:     "open-workload-transaction-goal",
		Protocol: "stub",
		Config: dsl.ScriptConfig{
			RunLogic: dsl.RunLogicConfig{WorkloadModel: "open", MaxTransactions: 4},
			Pacing:   dsl.PacingConfig{Type: "tps", TPS: 1000},
		},
		Actions: []dsl.Action{{Name: "homepage", Type: "request", Target: "/"}},
	}
	e, err := New(Config{Script: script, VUsers: 1, Duration: time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result, err := e.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.TotalTransactions != 4 {
		t.Fatalf("total transactions = %d, want 4 from open workload transaction goal", result.TotalTransactions)
	}
}

func TestEngineOpenWorkloadGracefulStopDoesNotCancelInflightAction(t *testing.T) {
	state := installGracefulBoundaryState(t)
	script := &dsl.Script{
		Name: "direct-open-graceful-boundary", Protocol: "graceful-boundary-test",
		Config: dsl.ScriptConfig{
			RunLogic: dsl.RunLogicConfig{WorkloadModel: "open", Iterations: 1},
			Pacing:   dsl.PacingConfig{Type: "tps", TPS: 10},
		},
		Actions: []dsl.Action{
			{Name: "first", Type: "request", Target: "/first"},
			{Name: "second", Type: "request", Target: "/second"},
		},
	}
	e, err := New(Config{Script: script, VUsers: 1, Duration: 5 * time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, runErr := e.Run(context.Background())
		done <- runErr
	}()
	select {
	case <-state.firstStarted:
	case <-time.After(time.Second):
		t.Fatal("open-workload action did not start")
	}

	e.GracefulStop()
	select {
	case runErr := <-done:
		t.Fatalf("graceful stop canceled the in-flight open-workload action: %v", runErr)
	case <-time.After(50 * time.Millisecond):
	}
	close(state.releaseFirst)
	select {
	case runErr := <-done:
		if runErr != nil {
			t.Fatalf("Run: %v", runErr)
		}
	case <-time.After(time.Second):
		t.Fatal("open workload did not finish after the in-flight action returned")
	}
	got := boundaryActionNames(state)
	if len(got) == 0 || got[0] != "first" {
		t.Fatalf("executed actions = %v, want the in-flight first action", got)
	}
	for _, name := range got {
		if name == "second" {
			t.Fatalf("executed actions = %v, graceful stop crossed an action boundary", got)
		}
	}
}

func TestMultiGroup_RunStopsAtGlobalTransactionGoal(t *testing.T) {
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups: []GroupConfig{{
			Name: "buyers",
			Script: &dsl.Script{
				Name:     "multi-group-transaction-goal",
				Protocol: "stub",
				Config: dsl.ScriptConfig{
					RunLogic: dsl.RunLogicConfig{MaxTransactions: 4},
				},
				Actions: []dsl.Action{{Name: "a", Type: "request", Target: "/"}},
			},
			VUsers: 6,
		}},
		Duration: time.Second,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}

	result, err := mge.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.TotalTransactions != 4 {
		t.Fatalf("total transactions = %d, want 4 from multi-group transaction goal", result.TotalTransactions)
	}
}

func TestMultiGroupExactGoalStopsUnboundedPeer(t *testing.T) {
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups: []GroupConfig{
			{
				Name: "exact-goal",
				Script: &dsl.Script{
					Name:     "exact-goal",
					Protocol: "stub",
					Config: dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{
						Iterations:      1,
						MaxTransactions: 1,
					}},
					Actions: []dsl.Action{{Name: "bounded", Type: "request", Target: "/bounded"}},
				},
				VUsers: 1,
			},
			{
				Name: "unbounded-peer",
				Script: &dsl.Script{
					Name:     "unbounded-peer",
					Protocol: "stub",
					Actions:  []dsl.Action{{Name: "peer", Type: "request", Target: "/peer"}},
				},
				VUsers: 1,
			},
		},
		Duration: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}

	started := time.Now()
	result, err := mge.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("exact goal did not stop unbounded peer promptly: elapsed=%s", elapsed)
	}
	if result.TotalTransactions < 1 {
		t.Fatalf("total transactions = %d, want the exact-goal owner to finish", result.TotalTransactions)
	}
}

func TestEngineClosedGoalCancelsUnentitledProtocolInit(t *testing.T) {
	state := installGoalInitCancellationState(t)
	script := &dsl.Script{
		Name: "closed-init-goal", Protocol: "goal-init-cancellation-test",
		Config: dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{
			Iterations:      1,
			MaxTransactions: 1,
		}},
		Actions: []dsl.Action{{
			Name:     "goal action",
			Type:     "java_vuser_run",
			Metadata: map[string]any{"emits_business_transactions": true},
		}},
	}
	engine, err := New(Config{Script: script, VUsers: 2, Duration: 5 * time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	started := time.Now()
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("closed goal waited through unentitled protocol init: elapsed=%s", elapsed)
	}
	select {
	case <-state.blockedStarted:
	default:
		t.Fatal("second closed VUser did not enter protocol init")
	}
	select {
	case <-state.blockedStopped:
	default:
		t.Fatal("goal cancellation did not cancel the second closed protocol init")
	}
	if got := state.executions.Load(); got != 1 {
		t.Fatalf("executions = %d, want only the goal owner", got)
	}
	if result.TotalTransactions != 1 {
		t.Fatalf("total transactions = %d, want one streamed goal transaction", result.TotalTransactions)
	}
}

func TestEngineOpenGoalCancelsUnentitledProtocolInit(t *testing.T) {
	state := installGoalInitCancellationState(t)
	script := &dsl.Script{
		Name: "open-init-goal", Protocol: "goal-init-cancellation-test",
		Config: dsl.ScriptConfig{
			RunLogic: dsl.RunLogicConfig{
				WorkloadModel:   "open",
				Iterations:      10,
				MaxInFlight:     2,
				MaxTransactions: 1,
			},
			Pacing: dsl.PacingConfig{Type: "tps", TPS: 100},
		},
		Actions: []dsl.Action{{
			Name:     "goal action",
			Type:     "java_vuser_run",
			Metadata: map[string]any{"emits_business_transactions": true},
		}},
	}
	engine, err := New(Config{Script: script, VUsers: 1, Duration: 5 * time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	started := time.Now()
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("open goal waited through unentitled protocol init: elapsed=%s", elapsed)
	}
	select {
	case <-state.blockedStarted:
	default:
		t.Fatal("second open arrival did not enter protocol init")
	}
	select {
	case <-state.blockedStopped:
	default:
		t.Fatal("goal cancellation did not cancel the second open protocol init")
	}
	if got := state.executions.Load(); got != 1 {
		t.Fatalf("executions = %d, want only the goal owner", got)
	}
	if result.TotalTransactions != 1 {
		t.Fatalf("total transactions = %d, want one streamed goal transaction", result.TotalTransactions)
	}
}

func TestMultiGroupClosedGoalCancelsPreparedRampPeer(t *testing.T) {
	script := &dsl.Script{
		Name:     "multi-group-prepared-goal",
		Protocol: "stub",
		Config: dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{
			Iterations:      1,
			MaxTransactions: 1,
		}},
		Actions: []dsl.Action{{Name: "goal action", Type: "request", Target: "/"}},
	}
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups: []GroupConfig{{
			Name:   "closed",
			Script: script,
			VUsers: 2,
			RampUp: 10 * time.Second,
		}},
		Duration: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}

	started := time.Now()
	result, err := mge.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("closed goal waited for the prepared peer's ramp deadline: elapsed=%s", elapsed)
	}
	if result.TotalTransactions != 1 {
		t.Fatalf("total transactions = %d, want one", result.TotalTransactions)
	}
	if got := mge.activeVUserCountByGroup()["closed"]; got != 0 {
		t.Fatalf("active VUsers after goal cleanup = %d, want 0", got)
	}
	mge.mu.Lock()
	defer mge.mu.Unlock()
	if len(mge.vuserCancels) != 0 || len(mge.vuserRunning) != 0 || len(mge.vuserIDsInUse) != 0 {
		t.Fatalf("VUser state after goal cleanup = reserved %d / running %d / ids %d, want 0 / 0 / 0",
			len(mge.vuserCancels), len(mge.vuserRunning), len(mge.vuserIDsInUse))
	}
}

func TestMultiGroupGoalCancellationTreatsUnlaunchedProtocolInitAsGraceful(t *testing.T) {
	tests := []struct {
		name string
		open bool
	}{
		{name: "open", open: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state := installGoalInitCancellationState(t)
			runLogic := dsl.RunLogicConfig{
				Iterations:      1,
				MaxTransactions: 1,
			}
			pacing := dsl.PacingConfig{}
			vusers := 2
			if tc.open {
				runLogic.WorkloadModel = "open"
				runLogic.Iterations = 10
				runLogic.MaxInFlight = 2
				pacing = dsl.PacingConfig{Type: "tps", TPS: 100}
				vusers = 1
			}
			script := &dsl.Script{
				Name:     "multi-group-init-goal-" + tc.name,
				Protocol: "goal-init-cancellation-test",
				Config:   dsl.ScriptConfig{RunLogic: runLogic, Pacing: pacing},
				Actions: []dsl.Action{{
					Name:     "goal action",
					Type:     "java_vuser_run",
					Metadata: map[string]any{"emits_business_transactions": true},
				}},
			}
			mge, err := NewMultiGroup(MultiGroupConfig{
				Groups:   []GroupConfig{{Name: tc.name, Script: script, VUsers: vusers}},
				Duration: 5 * time.Second,
			})
			if err != nil {
				t.Fatalf("NewMultiGroup: %v", err)
			}
			result, err := mge.Run(context.Background())
			if err != nil {
				t.Fatalf("Run treated context-aware canceled Init as fatal: %v", err)
			}
			select {
			case <-state.blockedStarted:
			default:
				t.Fatal("unlaunched peer did not enter protocol Init")
			}
			select {
			case <-state.blockedStopped:
			default:
				t.Fatal("shared goal did not cancel the unlaunched peer Init")
			}
			if got := state.executions.Load(); got != 1 {
				t.Fatalf("executions = %d, want only the launched goal owner", got)
			}
			if result.TotalTransactions != 1 {
				t.Fatalf("total transactions = %d, want one", result.TotalTransactions)
			}
		})
	}
}

func TestMultiGroupAtOnceCanceledAdapterInitIsGracefulOnCallerStop(t *testing.T) {
	state := installGoalInitCancellationState(t)
	state.nextInit.Store(1)
	script := &dsl.Script{
		Name:     "canceled-at-once-adapter-init",
		Protocol: "goal-init-cancellation-test",
		Init:     []dsl.Action{{Name: "user init", Type: "request", Target: "/init"}},
		Actions:  []dsl.Action{{Name: "main", Type: "request", Target: "/main"}},
	}
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups: []GroupConfig{{
			Name: "at-once", Script: script, VUsers: 1, InitStrategy: "at_once",
		}},
		Duration: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, runErr := mge.Run(ctx)
		done <- runErr
	}()
	select {
	case <-state.blockedStarted:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("at-once adapter did not enter cooperative Init")
	}
	cancel()
	select {
	case runErr := <-done:
		if runErr != nil {
			t.Fatalf("caller cancellation turned cooperative adapter Init into startup failure: %v", runErr)
		}
	case <-time.After(time.Second):
		t.Fatal("at-once adapter Init did not stop with its caller context")
	}
	select {
	case <-state.blockedStopped:
	default:
		t.Fatal("adapter Init did not observe caller cancellation")
	}
	if got := state.executions.Load(); got != 0 {
		t.Fatalf("user executions = %d, want none before launch", got)
	}
}

func TestMultiGroupZeroStartReadyAllowsRuntimeScale(t *testing.T) {
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups: []GroupConfig{{
			Name: "buyers",
			Script: &dsl.Script{
				Name:     "zero-start",
				Protocol: "stub",
				Actions:  []dsl.Action{{Name: "main", Type: "request", Target: "/"}},
			},
			VUsers:          0,
			AllowZeroVUsers: true,
		}},
		Duration: time.Second,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := mge.Run(ctx)
		done <- err
	}()

	select {
	case <-mge.Ready():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for zero-start engine readiness")
	}
	if err := mge.ScaleVUsers("buyers", 1); err != nil {
		t.Fatalf("ScaleVUsers after Ready: %v", err)
	}
	waitForActiveVUsers(t, mge, 1)
	if err := mge.ScaleVUsers("buyers", 0); err != nil {
		t.Fatalf("ScaleVUsers back to zero: %v", err)
	}
	waitForActiveVUsers(t, mge, 0)
	if err := mge.ScaleVUsers("buyers", 1); err != nil {
		t.Fatalf("ScaleVUsers after reaching zero: %v", err)
	}
	waitForActiveVUsers(t, mge, 1)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error after cancel: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("zero-start engine did not stop after cancel")
	}
}

func TestScaleVUsersRollsBackWhenInitIsCanceledBeforeLaunch(t *testing.T) {
	state := installGoalInitCancellationState(t)
	// Make the first protocol instance created by the scale-up take the
	// context-aware blocking Init path.
	state.nextInit.Store(1)
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups: []GroupConfig{{
			Name: "buyers",
			Script: &dsl.Script{
				Name:     "canceled-scale-init",
				Protocol: "goal-init-cancellation-test",
				Actions:  []dsl.Action{{Name: "main", Type: "request", Target: "/"}},
			},
			VUsers:          0,
			AllowZeroVUsers: true,
		}},
		Duration: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}
	runDone := make(chan error, 1)
	go func() {
		_, runErr := mge.Run(context.Background())
		runDone <- runErr
	}()
	select {
	case <-mge.Ready():
	case <-time.After(time.Second):
		t.Fatal("engine did not become ready")
	}
	scaleDone := make(chan error, 1)
	go func() {
		scaleDone <- mge.ScaleVUsers("buyers", 1)
	}()
	select {
	case <-state.blockedStarted:
	case <-time.After(time.Second):
		t.Fatal("scaled VUser did not enter protocol Init")
	}
	mge.GracefulStop()
	select {
	case scaleErr := <-scaleDone:
		if scaleErr == nil {
			t.Fatal("ScaleVUsers reported success for a VUser that never launched")
		}
	case <-time.After(time.Second):
		t.Fatal("ScaleVUsers did not return after Init cancellation")
	}
	mge.mu.Lock()
	target := mge.groups[0].VUsers
	live := mge.activeVUserCount()
	mge.mu.Unlock()
	if target != 0 || live != 0 {
		t.Fatalf("rolled-back scale target/live = %d/%d, want 0/0", target, live)
	}
	select {
	case runErr := <-runDone:
		if runErr != nil {
			t.Fatalf("Run: %v", runErr)
		}
	case <-time.After(time.Second):
		t.Fatal("engine did not stop after graceful cancellation")
	}
}

func TestMultiGroupNormallyStartedGroupCanPauseAtZeroAndResume(t *testing.T) {
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups: []GroupConfig{{
			Name: "buyers",
			Script: &dsl.Script{
				Name:     "normal-start",
				Protocol: "stub",
				Actions:  []dsl.Action{{Name: "main", Type: "request", Target: "/"}},
			},
			VUsers: 1,
		}},
		Duration: time.Second,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, runErr := mge.Run(ctx)
		done <- runErr
	}()
	select {
	case <-mge.Ready():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for engine readiness")
	}
	waitForActiveVUsers(t, mge, 1)
	if err := scaleAfterInitialRamp(t, mge, "buyers", 0); err != nil {
		t.Fatalf("ScaleVUsers to zero: %v", err)
	}
	waitForActiveVUsers(t, mge, 0)
	if err := mge.ScaleVUsers("buyers", 1); err != nil {
		t.Fatalf("ScaleVUsers after zero pause: %v", err)
	}
	waitForActiveVUsers(t, mge, 1)
	cancel()
	select {
	case runErr := <-done:
		if runErr != nil {
			t.Fatalf("Run returned error after cancel: %v", runErr)
		}
	case <-time.After(time.Second):
		t.Fatal("engine did not stop after cancel")
	}
}

func scaleAfterInitialRamp(t *testing.T, mge *MultiGroupEngine, group string, target int) error {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		err := mge.ScaleVUsers(group, target)
		if !errors.Is(err, ErrInitialRampInProgress) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(time.Millisecond)
	}
}

func waitForActiveVUsers(t *testing.T, mge *MultiGroupEngine, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		mge.mu.Lock()
		got := mge.activeVUserCount()
		mge.mu.Unlock()
		if got == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	mge.mu.Lock()
	got := mge.activeVUserCount()
	mge.mu.Unlock()
	t.Fatalf("active vusers = %d, want %d", got, want)
}

func TestMultiGroupRampDownIsProgressive(t *testing.T) {
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups: []GroupConfig{{
			Name: "buyers",
			Script: &dsl.Script{
				Name:     "progressive-ramp-down",
				Protocol: "stub",
				Actions:  []dsl.Action{{Name: "main", Type: "request", Target: "/"}},
			},
			VUsers:   3,
			RampDown: 90 * time.Millisecond,
		}},
		Duration: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}

	started := time.Now()
	if _, err := mge.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if elapsed := time.Since(started); elapsed < 160*time.Millisecond {
		t.Fatalf("ramp-down ended too early: %v", elapsed)
	}
}

func TestMultiGroupPauseIsRejectedAfterNaturalRampDownStarts(t *testing.T) {
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups: []GroupConfig{{
			Name: "buyers",
			Script: &dsl.Script{
				Name:     "pause-during-ramp-down",
				Protocol: "stub",
				Actions:  []dsl.Action{{Name: "main", Type: "request", Target: "/"}},
			},
			VUsers:   2,
			RampDown: 200 * time.Millisecond,
		}},
		Duration: 40 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, runErr := mge.Run(ctx)
		done <- runErr
	}()
	select {
	case <-mge.Ready():
	case <-time.After(time.Second):
		t.Fatal("engine did not become ready")
	}
	mge.mu.Lock()
	launchCtx := mge.launchCtx
	mge.mu.Unlock()
	select {
	case <-launchCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("natural duration did not start ramp-down")
	}

	if err := mge.Pause(); err == nil {
		cancel()
		t.Fatal("Pause succeeded after natural ramp-down started")
	}
	select {
	case runErr := <-done:
		if runErr != nil {
			t.Fatalf("Run: %v", runErr)
		}
	case <-time.After(time.Second):
		cancel()
		t.Fatal("engine did not complete progressive ramp-down")
	}
}

func TestMultiGroup_RunOpenWorkloadStopsAtGlobalTransactionGoal(t *testing.T) {
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups: []GroupConfig{{
			Name: "open-group",
			Script: &dsl.Script{
				Name:     "multi-group-open-transaction-goal",
				Protocol: "stub",
				Config: dsl.ScriptConfig{
					RunLogic: dsl.RunLogicConfig{WorkloadModel: "open", MaxTransactions: 4},
					Pacing:   dsl.PacingConfig{Type: "tps", TPS: 1000},
				},
				Actions: []dsl.Action{{Name: "a", Type: "request", Target: "/"}},
			},
			VUsers: 1,
		}},
		Duration: time.Second,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}

	result, err := mge.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.TotalTransactions != 4 {
		t.Fatalf("total transactions = %d, want 4 from multi-group open workload transaction goal", result.TotalTransactions)
	}
}

func TestMultiGroupRunExecutesAtOnceLifecycleOncePerGroup(t *testing.T) {
	resetLifecycleCountingProtocol()
	script := &dsl.Script{
		Name:     "at-once-lifecycle",
		Protocol: "lifecycle-counting",
		Config: dsl.ScriptConfig{
			RunLogic: dsl.RunLogicConfig{Iterations: 1},
		},
		Init:     []dsl.Action{{Name: "setup", Type: "request", Target: "/setup"}},
		Actions:  []dsl.Action{{Name: "main", Type: "request", Target: "/main"}},
		Teardown: []dsl.Action{{Name: "cleanup", Type: "request", Target: "/cleanup"}},
	}
	engine, err := NewMultiGroup(MultiGroupConfig{
		Duration: time.Second,
		Groups: []GroupConfig{{
			Name:         "buyers",
			Script:       script,
			VUsers:       3,
			InitStrategy: "at_once",
		}},
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}

	if _, err := engine.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := lifecycleInitActions.Load(); got != 1 {
		t.Fatalf("init actions = %d, want 1", got)
	}
	if got := lifecycleMainActions.Load(); got != 3 {
		t.Fatalf("main actions = %d, want 3", got)
	}
	if got := lifecycleTeardownActions.Load(); got != 1 {
		t.Fatalf("teardown actions = %d, want 1", got)
	}
}

func TestMultiGroupRunKeepsDefaultLifecyclePerVUser(t *testing.T) {
	resetLifecycleCountingProtocol()
	script := &dsl.Script{
		Name:     "at-runtime-lifecycle",
		Protocol: "lifecycle-counting",
		Config: dsl.ScriptConfig{
			RunLogic: dsl.RunLogicConfig{Iterations: 1},
		},
		Init:     []dsl.Action{{Name: "setup", Type: "request", Target: "/setup"}},
		Actions:  []dsl.Action{{Name: "main", Type: "request", Target: "/main"}},
		Teardown: []dsl.Action{{Name: "cleanup", Type: "request", Target: "/cleanup"}},
	}
	engine, err := NewMultiGroup(MultiGroupConfig{
		Duration: time.Second,
		Groups: []GroupConfig{{
			Name:   "buyers",
			Script: script,
			VUsers: 3,
		}},
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}

	if _, err := engine.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := lifecycleInitActions.Load(); got != 3 {
		t.Fatalf("init actions = %d, want 3", got)
	}
	if got := lifecycleMainActions.Load(); got != 3 {
		t.Fatalf("main actions = %d, want 3", got)
	}
	if got := lifecycleTeardownActions.Load(); got != 3 {
		t.Fatalf("teardown actions = %d, want 3", got)
	}
}

func resetLifecycleCountingProtocol() {
	lifecycleInitActions.Store(0)
	lifecycleMainActions.Store(0)
	lifecycleTeardownActions.Store(0)
}

func TestEngine_Stop(t *testing.T) {
	script := &dsl.Script{
		Name:     "stop-test",
		Protocol: "stub",
		Actions: []dsl.Action{
			{Name: "a", Type: "request", Target: "/"},
		},
	}

	e, err := New(Config{
		Script:   script,
		VUsers:   2,
		Duration: 10 * time.Second,
		RampUp:   10 * time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	done := make(chan struct{})
	go func() {
		e.Run(context.Background())
		close(done)
	}()

	time.Sleep(100 * time.Millisecond)
	e.Stop()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Engine.Stop did not terminate Run within 2s")
	}
}

func TestEngine_RampDelay(t *testing.T) {
	e := &Engine{rampUp: 10 * time.Second, vusers: 10}
	delay := e.rampDelay()
	if delay != time.Second {
		t.Errorf("rampDelay = %v, want 1s", delay)
	}
}

func TestEngine_RampDelay_NoRamp(t *testing.T) {
	e := &Engine{rampUp: 0, vusers: 10}
	if e.rampDelay() != 0 {
		t.Error("rampDelay should be 0 when rampUp is 0")
	}
}

func TestEngine_RampDelay_SingleVUser(t *testing.T) {
	e := &Engine{rampUp: 10 * time.Second, vusers: 1}
	if e.rampDelay() != 0 {
		t.Error("rampDelay should be 0 for single vuser")
	}
}

func TestMultiGroup_RunIncludesBaselinePeriods(t *testing.T) {
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups: []GroupConfig{{
			Name:   "baseline-group",
			Script: &dsl.Script{Name: "baseline", Protocol: "stub", Actions: []dsl.Action{{Name: "a", Type: "request", Target: "/"}}},
			VUsers: 1,
		}},
		Duration:     60 * time.Millisecond,
		PreBaseline:  40 * time.Millisecond,
		PostBaseline: 40 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}

	result, err := mge.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Duration < 120*time.Millisecond {
		t.Fatalf("expected runtime to include baseline windows, got %v", result.Duration)
	}
}

func TestEngine_RunHonorsSharedTPSPacing(t *testing.T) {
	script := &dsl.Script{
		Name:     "tps-run-test",
		Protocol: "stub",
		Config: dsl.ScriptConfig{
			RunLogic: dsl.RunLogicConfig{Iterations: 2},
			Pacing:   dsl.PacingConfig{Type: "tps", TPS: 2},
		},
		Actions: []dsl.Action{{Name: "homepage", Type: "request", Target: "/"}},
	}

	e, err := New(Config{
		Script:   script,
		VUsers:   2,
		Duration: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	started := time.Now()
	result, err := e.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.TotalTransactions != 4 {
		t.Fatalf("transactions = %d, want 4", result.TotalTransactions)
	}
	if elapsed := time.Since(started); elapsed < 1500*time.Millisecond {
		t.Fatalf("expected shared TPS pacing to stretch runtime, got %v", elapsed)
	}
}

func TestMultiGroup_RunHonorsSharedTPSPacing(t *testing.T) {
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups: []GroupConfig{{
			Name: "tps-group",
			Script: &dsl.Script{
				Name:     "tps-group-script",
				Protocol: "stub",
				Config: dsl.ScriptConfig{
					RunLogic: dsl.RunLogicConfig{Iterations: 2},
					Pacing:   dsl.PacingConfig{Type: "tps", TPS: 2},
				},
				Actions: []dsl.Action{{Name: "a", Type: "request", Target: "/"}},
			},
			VUsers: 2,
		}},
		Duration: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}

	started := time.Now()
	result, err := mge.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.TotalTransactions != 4 {
		t.Fatalf("transactions = %d, want 4", result.TotalTransactions)
	}
	if elapsed := time.Since(started); elapsed < 1500*time.Millisecond {
		t.Fatalf("expected shared TPS pacing to stretch runtime, got %v", elapsed)
	}
}

func TestEngine_RunOpenWorkloadOverlapsSlowIterations(t *testing.T) {
	script := &dsl.Script{
		Name:     "open-workload-run-test",
		Protocol: "slow-stub",
		Config: dsl.ScriptConfig{
			RunLogic: dsl.RunLogicConfig{Iterations: 5, WorkloadModel: "open"},
			Pacing:   dsl.PacingConfig{Type: "tps", TPS: 10},
		},
		Actions: []dsl.Action{{Name: "homepage", Type: "request", Target: "/"}},
	}

	e, err := New(Config{
		Script:   script,
		VUsers:   1,
		Duration: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	started := time.Now()
	result, err := e.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.TotalTransactions != 5 {
		t.Fatalf("transactions = %d, want 5", result.TotalTransactions)
	}
	if elapsed := time.Since(started); elapsed > 900*time.Millisecond {
		t.Fatalf("expected open workload to overlap slow iterations, got %v", elapsed)
	}
}

func TestMultiGroup_RunOpenWorkloadOverlapsSlowIterations(t *testing.T) {
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups: []GroupConfig{{
			Name: "open-group",
			Script: &dsl.Script{
				Name:     "open-workload-group-script",
				Protocol: "slow-stub",
				Config: dsl.ScriptConfig{
					RunLogic: dsl.RunLogicConfig{Iterations: 5, WorkloadModel: "open"},
					Pacing:   dsl.PacingConfig{Type: "tps", TPS: 10},
				},
				Actions: []dsl.Action{{Name: "a", Type: "request", Target: "/"}},
			},
			VUsers: 1,
		}},
		Duration: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}

	started := time.Now()
	result, err := mge.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.TotalTransactions != 5 {
		t.Fatalf("transactions = %d, want 5", result.TotalTransactions)
	}
	if elapsed := time.Since(started); elapsed > 900*time.Millisecond {
		t.Fatalf("expected open workload to overlap slow iterations, got %v", elapsed)
	}
}

func TestMultiGroupRunResultPopulatesCumulativeGroupCounters(t *testing.T) {
	groupScript := func(iterations int) *dsl.Script {
		return &dsl.Script{
			Name:     "group-results",
			Protocol: "stub",
			Config:   dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: iterations}},
			Actions:  []dsl.Action{{Name: "transaction", Type: "request", Target: "/"}},
		}
	}
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups: []GroupConfig{
			{Name: "buyers", Script: groupScript(2), VUsers: 1},
			{Name: "sellers", Script: groupScript(3), VUsers: 1},
		},
		Duration: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}
	result, err := mge.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(result.Groups) != 2 {
		t.Fatalf("group results = %#v", result.Groups)
	}
	if got := result.Groups[0]; got.Name != "buyers" || got.TotalTransactions != 2 || got.TotalErrors != 0 {
		t.Fatalf("buyers result = %#v", got)
	}
	if got := result.Groups[1]; got.Name != "sellers" || got.TotalTransactions != 3 || got.TotalErrors != 0 {
		t.Fatalf("sellers result = %#v", got)
	}
}

func TestMultiGroupRampsClosedGroupsConcurrently(t *testing.T) {
	groupScript := func(name string) *dsl.Script {
		return &dsl.Script{
			Name:     name,
			Protocol: "stub",
			Actions:  []dsl.Action{{Name: "transaction", Type: "request", Target: "/"}},
		}
	}
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups: []GroupConfig{
			// Put the slow ramp first so the original serialized implementation
			// cannot reach the fast group within the assertion window.
			{Name: "buyers", Script: groupScript("buyers"), VUsers: 3, RampUp: 30 * time.Second},
			{Name: "sellers", Script: groupScript("sellers"), VUsers: 3, RampUp: 300 * time.Millisecond},
		},
		Duration: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, runErr := mge.Run(ctx)
		done <- runErr
	}()

	var stopOnce sync.Once
	var stopMu sync.Mutex
	var stopped bool
	var stopErr error
	stop := func() error {
		stopOnce.Do(cancel)
		stopMu.Lock()
		defer stopMu.Unlock()
		if stopped {
			return stopErr
		}
		select {
		case stopErr = <-done:
			stopped = true
		case <-time.After(2 * time.Second):
			return errors.New("engine did not stop after cancellation")
		}
		return stopErr
	}
	t.Cleanup(func() {
		if err := stop(); err != nil {
			t.Errorf("cleanup multi-group ramp: %v", err)
		}
	})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		counts := mge.activeVUserCountByGroup()
		if counts["sellers"] == 3 {
			if counts["buyers"] != 1 {
				t.Fatalf("slow group active VUsers = %d, want 1 while fast group completed its ramp", counts["buyers"])
			}
			if err := stop(); err != nil {
				t.Fatalf("stop ramped engine: %v", err)
			}
			mge.mu.Lock()
			allocatedHandles := len(mge.vuserIDsInUse)
			allocatedIdentities := len(mge.vuserIdentities)
			mge.mu.Unlock()
			if allocatedHandles != 0 || allocatedIdentities != 0 {
				t.Fatalf("VUser identities after stop = handles:%d identities:%d, want 0/0", allocatedHandles, allocatedIdentities)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	counts := mge.activeVUserCountByGroup()
	t.Fatalf("group VUsers = %v, want sellers fully ramped while buyers remains at 1", counts)
}

func TestMultiGroupStartupFailureDoesNotStartLaterGroup(t *testing.T) {
	state := installStartupIsolationState(t)
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups: []GroupConfig{
			{
				Name:   "startup-failure",
				Script: &dsl.Script{Name: "failing", Protocol: "startup-isolation-test", Actions: []dsl.Action{{Name: "main", Type: "request", Target: "/"}}},
				VUsers: 2,
				RampUp: 20 * time.Millisecond,
			},
			{
				Name:         "must-not-start",
				Script:       &dsl.Script{Name: "later", Protocol: "startup-isolation-test", Init: []dsl.Action{{Name: "setup", Type: "request", Target: "/setup"}}, Actions: []dsl.Action{{Name: "main", Type: "request", Target: "/"}}},
				VUsers:       1,
				InitStrategy: "at_once",
			},
		},
		Duration: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	type runOutcome struct {
		result *MultiGroupRunResult
		err    error
	}
	done := make(chan runOutcome, 1)
	go func() {
		result, runErr := mge.Run(ctx)
		done <- runOutcome{result: result, err: runErr}
	}()

	var waitMu sync.Mutex
	var joined bool
	var outcome runOutcome
	waitRun := func() (runOutcome, error) {
		waitMu.Lock()
		defer waitMu.Unlock()
		if joined {
			return outcome, nil
		}
		select {
		case outcome = <-done:
			joined = true
			return outcome, nil
		case <-time.After(2 * time.Second):
			return runOutcome{}, errors.New("engine did not finish after startup failure")
		}
	}
	t.Cleanup(func() {
		state.releaseOnce.Do(func() { close(state.releaseFailingInit) })
		cancel()
		if _, err := waitRun(); err != nil {
			t.Errorf("cleanup startup isolation run: %v", err)
		}
	})

	select {
	case <-state.failingInitStarted:
	case <-time.After(time.Second):
		t.Fatal("failing group did not enter protocol initialization")
	}
	select {
	case <-state.failingActionStarted:
		t.Fatal("an earlier VUser executed before every configured VUser passed startup")
	case <-state.laterInitStarted:
		t.Fatal("later group initialized before the earlier group passed startup")
	case <-state.laterActionStarted:
		t.Fatal("later group executed setup before the earlier group passed startup")
	case <-time.After(250 * time.Millisecond):
	}

	state.releaseOnce.Do(func() { close(state.releaseFailingInit) })
	got, waitErr := waitRun()
	if waitErr != nil {
		t.Fatal(waitErr)
	}
	if got.result != nil || got.err == nil || !strings.Contains(got.err.Error(), "injected startup failure") {
		t.Fatalf("Run outcome = (%#v, %v), want nil result with injected startup failure", got.result, got.err)
	}
	select {
	case <-state.failingActionStarted:
		t.Fatal("an earlier VUser executed despite a later configured VUser startup failure")
	default:
	}
	select {
	case <-state.laterInitStarted:
		t.Fatal("later group initialized after the earlier startup failure")
	default:
	}
	select {
	case <-state.laterActionStarted:
		t.Fatal("later group executed setup after the earlier startup failure")
	default:
	}
}

func TestMultiGroupOpenWorkloadPreBaselineDoesNotCreateStartupCatchUp(t *testing.T) {
	const arrivals = 1100
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups: []GroupConfig{{
			Name: "open-group",
			Script: &dsl.Script{
				Name:     "pre-baseline-open-workload",
				Protocol: "stub",
				Config: dsl.ScriptConfig{
					RunLogic: dsl.RunLogicConfig{Iterations: arrivals, WorkloadModel: "open"},
					Pacing:   dsl.PacingConfig{Type: "tps", TPS: 20_000},
				},
				Actions: []dsl.Action{{Name: "transaction", Type: "request", Target: "/"}},
			},
			VUsers: 1,
		}},
		Duration:    2 * time.Second,
		PreBaseline: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}

	result, err := mge.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.TotalTransactions != arrivals {
		t.Fatalf("transactions = %d, want all %d arrivals after pre-baseline", result.TotalTransactions, arrivals)
	}
	if result.FinalSnapshot.Telemetry != nil && result.FinalSnapshot.Telemetry.SheddedArrivals != 0 {
		t.Fatalf("pre-baseline created %d shed arrivals", result.FinalSnapshot.Telemetry.SheddedArrivals)
	}
}

func TestNewMultiGroupRejectsDuplicateEffectiveNames(t *testing.T) {
	script := func(name string) *dsl.Script {
		return &dsl.Script{
			Name:     name,
			Protocol: "stub",
			Actions:  []dsl.Action{{Name: "transaction", Type: "request", Target: "/"}},
		}
	}
	tests := []struct {
		name   string
		groups []GroupConfig
	}{
		{
			name: "explicit names",
			groups: []GroupConfig{
				{Name: "buyers", Script: script("first"), VUsers: 1},
				{Name: "buyers", Script: script("second"), VUsers: 1},
			},
		},
		{
			name: "explicit and script fallback",
			groups: []GroupConfig{
				{Name: "buyers", Script: script("first"), VUsers: 1},
				{Script: script("buyers"), VUsers: 1},
			},
		},
		{
			name: "normalized explicit names",
			groups: []GroupConfig{
				{Name: "  Buyers   East ", Script: script("first"), VUsers: 1},
				{Name: "buyers east", Script: script("second"), VUsers: 1},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewMultiGroup(MultiGroupConfig{Groups: tt.groups, Duration: time.Second}); err == nil || !strings.Contains(err.Error(), "effective group name") {
				t.Fatalf("NewMultiGroup error = %v, want duplicate effective group name", err)
			}
		})
	}
}
