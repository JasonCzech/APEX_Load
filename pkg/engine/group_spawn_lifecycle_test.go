package engine

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

type spawnLifecycleState struct {
	initErr       error
	initCalls     atomic.Int64
	teardownCalls atomic.Int64
}

type spawnLifecycleProtocol struct {
	state *spawnLifecycleState
}

func (*spawnLifecycleProtocol) Name() string { return "spawn-lifecycle-test" }

func (p *spawnLifecycleProtocol) Init(context.Context, map[string]any) error {
	p.state.initCalls.Add(1)
	return p.state.initErr
}

func (*spawnLifecycleProtocol) Execute(context.Context, *protocol.Action) (*protocol.Result, error) {
	return &protocol.Result{Success: true}, nil
}

func (p *spawnLifecycleProtocol) Teardown(context.Context) error {
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
		vuserStopping:  make(map[int]bool),
		vuserChanged:   make(chan struct{}),
		vuserIDsInUse:  map[int]struct{}{1: {}},
		vuserIDSpan:    10,
		gracefulStopCh: make(chan struct{}),
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
		},
	}
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
				var wg sync.WaitGroup
				if workload == "closed" {
					launched, err := engine.spawnVUser(1, group, nil, &wg, false)
					wg.Wait()
					return launched, err, state
				}
				var releases atomic.Int64
				launched, err := engine.spawnOpenWorker(1, group, nil, &wg, func() {
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
