package engine

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

type spawnLifecycleState struct {
	initErr               error
	initCalls             atomic.Int64
	teardownCalls         atomic.Int64
	ctxMu                 sync.Mutex
	initCtx               context.Context
	ctxCanceledAtExecute  atomic.Bool
	ctxCanceledAtTeardown atomic.Bool
	waitForExecuteCancel  bool
	executeStarted        chan struct{}
	teardownStarted       chan struct{}
	releaseTeardown       chan struct{}
	executeStartedOnce    sync.Once
	teardownStartedOnce   sync.Once
	releaseTeardownOnce   sync.Once
}

type spawnLifecycleProtocol struct {
	state *spawnLifecycleState
}

func (*spawnLifecycleProtocol) Name() string { return "spawn-lifecycle-test" }

func (p *spawnLifecycleProtocol) Init(ctx context.Context, _ map[string]any) error {
	p.state.initCalls.Add(1)
	p.state.ctxMu.Lock()
	p.state.initCtx = ctx
	p.state.ctxMu.Unlock()
	return p.state.initErr
}

func (p *spawnLifecycleProtocol) Execute(ctx context.Context, _ *protocol.Action) (*protocol.Result, error) {
	p.state.ctxMu.Lock()
	initCtx := p.state.initCtx
	p.state.ctxMu.Unlock()
	if initCtx != nil && initCtx.Err() != nil {
		p.state.ctxCanceledAtExecute.Store(true)
	}
	if p.state.executeStarted != nil {
		p.state.executeStartedOnce.Do(func() { close(p.state.executeStarted) })
	}
	if p.state.waitForExecuteCancel {
		<-ctx.Done()
	}
	return &protocol.Result{Success: true}, nil
}

func (p *spawnLifecycleProtocol) Teardown(ctx context.Context) error {
	p.state.ctxMu.Lock()
	initCtx := p.state.initCtx
	p.state.ctxMu.Unlock()
	if initCtx != nil && initCtx.Err() != nil {
		p.state.ctxCanceledAtTeardown.Store(true)
	}
	if p.state.teardownStarted != nil {
		p.state.teardownStartedOnce.Do(func() { close(p.state.teardownStarted) })
	}
	if p.state.releaseTeardown != nil {
		select {
		case <-p.state.releaseTeardown:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	p.state.teardownCalls.Add(1)
	return nil
}

func (*spawnLifecycleProtocol) HealthCheck(context.Context) error { return nil }

var (
	spawnLifecycleStateMu      sync.Mutex
	spawnLifecycleStateCurrent *spawnLifecycleState
)

func init() {
	protocol.Register("spawn-lifecycle-test", func() protocol.Protocol {
		spawnLifecycleStateMu.Lock()
		defer spawnLifecycleStateMu.Unlock()
		return &spawnLifecycleProtocol{state: spawnLifecycleStateCurrent}
	})
}

func installSpawnLifecycleState(t *testing.T, initErr error) *spawnLifecycleState {
	t.Helper()
	state := &spawnLifecycleState{initErr: initErr}
	spawnLifecycleStateMu.Lock()
	spawnLifecycleStateCurrent = state
	spawnLifecycleStateMu.Unlock()
	t.Cleanup(func() {
		spawnLifecycleStateMu.Lock()
		if spawnLifecycleStateCurrent == state {
			spawnLifecycleStateCurrent = nil
		}
		spawnLifecycleStateMu.Unlock()
	})
	return state
}

func newSpawnLifecycleEngine(t *testing.T, parent context.Context) *MultiGroupEngine {
	t.Helper()
	baseCtx, baseCancel := context.WithCancel(parent)
	loopCtx, loopCancel := context.WithCancel(baseCtx)
	gracefulCtx, gracefulCancel := context.WithCancel(baseCtx)
	t.Cleanup(baseCancel)
	t.Cleanup(loopCancel)
	t.Cleanup(gracefulCancel)
	return &MultiGroupEngine{
		metrics:        NewMetricsCollector(16),
		baseCtx:        baseCtx,
		loopCtx:        loopCtx,
		gracefulCtx:    gracefulCtx,
		vuserCancels:   make(map[int]context.CancelFunc),
		vuserGroups:    make(map[int]string),
		vuserRunning:   make(map[int]struct{}),
		vuserStopping:  make(map[int]bool),
		vuserChanged:   make(chan struct{}),
		vuserIDsInUse:  map[int]struct{}{1: {}},
		vuserIDSpan:    10,
		gracefulStopCh: make(chan struct{}),
		launchOpen:     true,
	}
}

func spawnLifecycleGroup() GroupConfig {
	return GroupConfig{
		Name:   "spawn-contract",
		VUsers: 1,
		Script: &dsl.Script{
			Name:     "spawn contract",
			Protocol: "spawn-lifecycle-test",
			Config:   dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1}},
			Actions:  []dsl.Action{{Name: "main", Type: "request", Target: "/"}},
		},
	}
}

func spawnLifecycleGoalGroup() GroupConfig {
	group := spawnLifecycleGroup()
	group.Script.Config.RunLogic.GlobalIterations = 1
	return group
}

func TestSpawnVUserAndOpenWorkerReportLaunchDisposition(t *testing.T) {
	initFailure := errors.New("injected init failure")
	for _, workload := range []string{"closed", "open"} {
		t.Run(workload, func(t *testing.T) {
			spawn := func(t *testing.T, parent context.Context, initErr error) (bool, error, *spawnLifecycleState) {
				t.Helper()
				state := installSpawnLifecycleState(t, initErr)
				engine := newSpawnLifecycleEngine(t, parent)
				group := spawnLifecycleGroup()
				identity := vuserIdentity{handle: 1, local: 1}
				var wg sync.WaitGroup
				if workload == "closed" {
					launched, err := engine.spawnVUser(identity, group, nil, &wg, false)
					wg.Wait()
					return launched, err, state
				}
				var releases atomic.Int64
				launched, err := engine.spawnOpenWorker(identity, group, nil, &wg, func() {
					releases.Add(1)
				})
				wg.Wait()
				if releases.Load() != 1 {
					t.Fatalf("open-worker release calls = %d, want 1", releases.Load())
				}
				return launched, err, state
			}

			t.Run("canceled before launch", func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				launched, err, state := spawn(t, ctx, nil)
				if launched || err != nil {
					t.Fatalf("spawn = (%v, %v), want (false, nil)", launched, err)
				}
				if state.initCalls.Load() != 0 || state.teardownCalls.Load() != 0 {
					t.Fatalf("protocol lifecycle calls = init %d teardown %d, want 0/0", state.initCalls.Load(), state.teardownCalls.Load())
				}
			})

			t.Run("successful launch", func(t *testing.T) {
				launched, err, state := spawn(t, context.Background(), nil)
				if !launched || err != nil {
					t.Fatalf("spawn = (%v, %v), want (true, nil)", launched, err)
				}
				if state.initCalls.Load() != 1 || state.teardownCalls.Load() != 1 {
					t.Fatalf("protocol lifecycle calls = init %d teardown %d, want 1/1", state.initCalls.Load(), state.teardownCalls.Load())
				}
				if state.ctxCanceledAtExecute.Load() {
					t.Fatal("protocol Init context was canceled before the VUser executed")
				}
				if state.ctxCanceledAtTeardown.Load() {
					t.Fatal("protocol Init context was canceled before protocol teardown")
				}
				state.ctxMu.Lock()
				initCtx := state.initCtx
				state.ctxMu.Unlock()
				if initCtx == nil || initCtx.Err() == nil {
					t.Fatal("protocol Init context was not canceled during VUser cleanup")
				}
			})

			t.Run("initialization error", func(t *testing.T) {
				launched, err, state := spawn(t, context.Background(), initFailure)
				if launched || err == nil {
					t.Fatalf("spawn = (%v, %v), want (false, error)", launched, err)
				}
				if state.initCalls.Load() != 1 || state.teardownCalls.Load() != 1 {
					t.Fatalf("protocol lifecycle calls = init %d teardown %d, want 1/1", state.initCalls.Load(), state.teardownCalls.Load())
				}
			})
		})
	}
}

func TestPreparedVUserRetainsProtocolContextThroughEntitledGoalIteration(t *testing.T) {
	for _, workload := range []string{"closed", "open"} {
		t.Run(workload, func(t *testing.T) {
			state := installSpawnLifecycleState(t, nil)
			engine := newSpawnLifecycleEngine(t, context.Background())
			group := spawnLifecycleGoalGroup()

			// Use a real loop cancellation function for the goal callback without
			// canceling the per-VUser shed context.
			baseCtx, baseCancel := context.WithCancel(context.Background())
			loopCtx, loopCancel := context.WithCancel(baseCtx)
			gracefulCtx, gracefulCancel := context.WithCancel(baseCtx)
			t.Cleanup(baseCancel)
			t.Cleanup(loopCancel)
			t.Cleanup(gracefulCancel)
			engine.baseCtx = baseCtx
			engine.loopCtx = loopCtx
			engine.gracefulCtx = gracefulCtx
			group.executionGoals = NewExecutionGoalLimiter(group.Script.Config.RunLogic, loopCancel)

			identity := vuserIdentity{handle: 1, local: 1}
			var wg sync.WaitGroup
			var launched bool
			var err error
			if workload == "closed" {
				launched, err = engine.spawnVUser(identity, group, nil, &wg, false)
			} else {
				launched, err = engine.spawnOpenWorker(identity, group, nil, &wg, nil)
			}
			if !launched || err != nil {
				t.Fatalf("spawn = (%v, %v), want (true, nil)", launched, err)
			}
			wg.Wait()
			if state.ctxCanceledAtExecute.Load() {
				t.Fatal("shared-goal soft stop canceled the retained protocol Init context before Execute")
			}
			if state.ctxCanceledAtTeardown.Load() {
				t.Fatal("shared-goal soft stop canceled the retained protocol Init context before Teardown")
			}
		})
	}
}

func TestVUserStopRetainsProtocolContextThroughTeardown(t *testing.T) {
	for _, workload := range []string{"closed", "open"} {
		t.Run(workload, func(t *testing.T) {
			state := installSpawnLifecycleState(t, nil)
			state.waitForExecuteCancel = true
			state.executeStarted = make(chan struct{})
			state.teardownStarted = make(chan struct{})
			state.releaseTeardown = make(chan struct{})
			t.Cleanup(func() {
				state.releaseTeardownOnce.Do(func() { close(state.releaseTeardown) })
			})

			engine := newSpawnLifecycleEngine(t, context.Background())
			group := spawnLifecycleGroup()
			identity := vuserIdentity{handle: 1, local: 1}
			var wg sync.WaitGroup
			var launched bool
			var err error
			if workload == "closed" {
				launched, err = engine.spawnVUser(identity, group, nil, &wg, false)
			} else {
				launched, err = engine.spawnOpenWorker(identity, group, nil, &wg, nil)
			}
			if !launched || err != nil {
				t.Fatalf("spawn = (%v, %v), want (true, nil)", launched, err)
			}
			select {
			case <-state.executeStarted:
			case <-time.After(time.Second):
				t.Fatal("VUser did not enter Execute")
			}

			engine.mu.Lock()
			cancelVUser := engine.vuserCancels[identity.handle]
			engine.mu.Unlock()
			if cancelVUser == nil {
				t.Fatal("active VUser cancellation was not registered")
			}
			cancelVUser()

			select {
			case <-state.teardownStarted:
			case <-time.After(time.Second):
				t.Fatal("protocol teardown did not start after VUser stop")
			}
			if state.ctxCanceledAtTeardown.Load() {
				t.Fatal("VUser stop canceled retained protocol Init context before Teardown")
			}
			state.ctxMu.Lock()
			initCtx := state.initCtx
			state.ctxMu.Unlock()
			if initCtx == nil || initCtx.Err() != nil {
				t.Fatalf("protocol Init context during Teardown = %v, want live", initCtx)
			}

			state.releaseTeardownOnce.Do(func() { close(state.releaseTeardown) })
			wg.Wait()
			if initCtx.Err() == nil {
				t.Fatal("protocol Init context was not canceled after Teardown")
			}
		})
	}
}

func TestScaledClosedVUserRetainsProtocolContextThroughTeardown(t *testing.T) {
	state := installSpawnLifecycleState(t, nil)
	state.waitForExecuteCancel = true
	state.executeStarted = make(chan struct{})
	state.teardownStarted = make(chan struct{})
	state.releaseTeardown = make(chan struct{})
	t.Cleanup(func() {
		state.releaseTeardownOnce.Do(func() { close(state.releaseTeardown) })
	})

	group := spawnLifecycleGroup()
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups:   []GroupConfig{group},
		Duration: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}
	runCtx, cancelRun := context.WithCancel(context.Background())
	t.Cleanup(cancelRun)
	done := make(chan error, 1)
	go func() {
		_, runErr := mge.Run(runCtx)
		done <- runErr
	}()

	select {
	case <-state.executeStarted:
	case <-time.After(time.Second):
		t.Fatal("closed VUser did not enter Execute")
	}
	deadline := time.Now().Add(time.Second)
	for !mge.ScalingReady() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if err := mge.ScaleVUsers(group.Name, 0); err != nil {
		t.Fatalf("ScaleVUsers: %v", err)
	}

	select {
	case <-state.teardownStarted:
	case <-time.After(time.Second):
		t.Fatal("protocol teardown did not start after scale-down")
	}
	state.ctxMu.Lock()
	initCtx := state.initCtx
	state.ctxMu.Unlock()
	if state.ctxCanceledAtTeardown.Load() || initCtx == nil || initCtx.Err() != nil {
		t.Fatalf("protocol Init context during scaled cleanup = %v, canceled_at_teardown=%v; want live", initCtx, state.ctxCanceledAtTeardown.Load())
	}

	state.releaseTeardownOnce.Do(func() { close(state.releaseTeardown) })
	deadline = time.Now().Add(time.Second)
	for initCtx.Err() == nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if initCtx.Err() == nil {
		t.Fatal("protocol Init context was not canceled after scaled cleanup")
	}
	mge.GracefulStop()
	select {
	case runErr := <-done:
		if runErr != nil {
			t.Fatalf("Run: %v", runErr)
		}
	case <-time.After(time.Second):
		t.Fatal("run did not finish after scaled cleanup")
	}
}

func TestOpenVUserDeadlineRetainsProtocolContextThroughTeardown(t *testing.T) {
	state := installSpawnLifecycleState(t, nil)
	state.waitForExecuteCancel = true
	state.executeStarted = make(chan struct{})
	state.teardownStarted = make(chan struct{})
	state.releaseTeardown = make(chan struct{})
	t.Cleanup(func() {
		state.releaseTeardownOnce.Do(func() { close(state.releaseTeardown) })
	})

	script := spawnLifecycleGroup().Script
	script.Config.RunLogic = dsl.RunLogicConfig{WorkloadModel: "open", Iterations: 1}
	// One TPS plus a sub-second duration admits exactly the immediate arrival,
	// keeping the retained Init context and Teardown observation one-to-one.
	script.Config.Pacing = dsl.PacingConfig{Type: "tps", TPS: 1}
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups:   []GroupConfig{{Name: "open", Script: script, VUsers: 1}},
		Duration: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, runErr := mge.Run(context.Background())
		done <- runErr
	}()

	select {
	case <-state.executeStarted:
	case <-time.After(time.Second):
		t.Fatal("open VUser did not enter Execute")
	}
	select {
	case <-state.teardownStarted:
	case <-time.After(time.Second):
		t.Fatal("protocol teardown did not start at the natural deadline")
	}
	state.ctxMu.Lock()
	initCtx := state.initCtx
	state.ctxMu.Unlock()
	if state.ctxCanceledAtTeardown.Load() || initCtx == nil || initCtx.Err() != nil {
		t.Fatalf("protocol Init context during deadline cleanup = %v, canceled_at_teardown=%v; want live", initCtx, state.ctxCanceledAtTeardown.Load())
	}

	state.releaseTeardownOnce.Do(func() { close(state.releaseTeardown) })
	select {
	case runErr := <-done:
		if runErr != nil {
			t.Fatalf("Run: %v", runErr)
		}
	case <-time.After(time.Second):
		t.Fatal("run did not finish after deadline cleanup")
	}
	if initCtx.Err() == nil {
		t.Fatal("protocol Init context was not canceled after deadline cleanup")
	}
}

func TestAtOnceDeadlineRetainsProtocolContextThroughTeardown(t *testing.T) {
	state := installSpawnLifecycleState(t, nil)
	state.waitForExecuteCancel = true
	state.executeStarted = make(chan struct{})
	state.teardownStarted = make(chan struct{})
	state.releaseTeardown = make(chan struct{})
	t.Cleanup(func() {
		state.releaseTeardownOnce.Do(func() { close(state.releaseTeardown) })
	})

	script := spawnLifecycleGroup().Script
	script.Init = []dsl.Action{{Name: "shared init", Type: "request", Target: "/init"}}
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups: []GroupConfig{{
			Name:         "at-once",
			Script:       script,
			VUsers:       1,
			InitStrategy: "at_once",
		}},
		Duration: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, runErr := mge.Run(context.Background())
		done <- runErr
	}()

	select {
	case <-state.executeStarted:
	case <-time.After(time.Second):
		t.Fatal("at-once init action did not enter Execute")
	}
	select {
	case <-state.teardownStarted:
	case <-time.After(time.Second):
		t.Fatal("at-once protocol teardown did not start at the natural deadline")
	}
	state.ctxMu.Lock()
	initCtx := state.initCtx
	state.ctxMu.Unlock()
	if state.ctxCanceledAtTeardown.Load() || initCtx == nil || initCtx.Err() != nil {
		t.Fatalf("at-once protocol Init context during deadline cleanup = %v, canceled_at_teardown=%v; want live", initCtx, state.ctxCanceledAtTeardown.Load())
	}

	state.releaseTeardownOnce.Do(func() { close(state.releaseTeardown) })
	select {
	case runErr := <-done:
		if runErr != nil {
			t.Fatalf("Run: %v", runErr)
		}
	case <-time.After(time.Second):
		t.Fatal("at-once run did not finish after deadline cleanup")
	}
	if initCtx.Err() == nil {
		t.Fatal("at-once protocol Init context was not canceled after deadline cleanup")
	}
}
