package engine

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func admitPauseLease(t *testing.T, g *runtimePauseCoordinator, ctx context.Context, kind protocol.RuntimePauseActivity) protocol.RuntimePauseLease {
	t.Helper()
	l, err := protocol.AdmitRuntimePause(ctx, g, kind)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func pauseEngineFixture(g *runtimePauseCoordinator) *MultiGroupEngine {
	return &MultiGroupEngine{runtimePause: &engineRuntimePause{groups: map[string]*runtimePauseCoordinator{"group": g}}, baseCtx: context.Background(), launchOpen: true, controlsReady: true, pauseCh: make(chan struct{})}
}

func TestRuntimePauseCancellationIsNotQuiescence(t *testing.T) {
	g := newRuntimePauseCoordinator(false)
	receive := admitPauseLease(t, g, context.Background(), protocol.RuntimePauseReceive)
	tail := admitPauseLease(t, g, context.Background(), protocol.RuntimePauseTail)
	epoch := g.latch()
	for _, lease := range []protocol.RuntimePauseLease{receive, tail} {
		if !errors.Is(context.Cause(lease.Context()), protocol.ErrRuntimePauseSuspended) {
			t.Fatal("unbound background operation was not cancelled by pause")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := g.wait(ctx, epoch); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ignored cancellation was mistaken for completion: %v", err)
	}
	receive.Release()
	if g.proven(epoch) {
		t.Fatal("tail operation that has not returned lost its proof charge")
	}
	tail.Release()
	tail.Release()
	if err := g.wait(context.Background(), epoch); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimePauseActionDelegationAndCheckpointActualReturn(t *testing.T) {
	g := newRuntimePauseCoordinator(false)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	action := admitPauseLease(t, g, ctx, protocol.RuntimePauseAction)
	borrowed := admitPauseLease(t, g, action.Context(), protocol.RuntimePauseAction)
	borrowed.Release()
	epoch := g.latch()
	receive := admitPauseLease(t, g, action.Context(), protocol.RuntimePauseReceive)
	checkpoint := admitPauseLease(t, g, action.Context(), protocol.RuntimePauseCheckpoint)
	if receive.Context().Err() != nil || checkpoint.Context().Err() != nil || action.Context().Err() != nil {
		t.Fatal("pause cancelled an original admitted action or delegated work")
	}
	if got, _ := receive.Context().Deadline(); !got.Equal(func() time.Time { v, _ := ctx.Deadline(); return v }()) {
		t.Fatal("delegation replaced the original action budget")
	}
	action.Release()
	if !errors.Is(context.Cause(receive.Context()), protocol.ErrRuntimePauseSuspended) {
		t.Fatal("surplus receive retained released action authority")
	}
	receive.Release()
	if g.proven(epoch) {
		t.Fatal("pending checkpoint CAS lost its charge when its action returned")
	}
	checkpoint.Release()
	if !g.proven(epoch) {
		t.Fatal("returned checkpoint did not discharge proof")
	}
	if _, err := g.Admit(action.Context(), protocol.RuntimePauseCheckpoint); !errors.Is(err, protocol.ErrRuntimePauseAdmissionInvalid) && !errors.Is(err, context.Canceled) {
		t.Fatalf("stale action token acquired checkpoint authority: %v", err)
	}
}

func TestRuntimePauseRejectsForeignAndUnboundCheckpoint(t *testing.T) {
	g := newRuntimePauseCoordinator(false)
	other := newRuntimePauseCoordinator(false)
	action := admitPauseLease(t, other, context.Background(), protocol.RuntimePauseAction)
	defer action.Release()
	for _, tc := range []struct {
		ctx  context.Context
		kind protocol.RuntimePauseActivity
	}{{context.Background(), protocol.RuntimePauseCheckpoint}, {action.Context(), protocol.RuntimePauseCheckpoint}, {action.Context(), protocol.RuntimePauseAction}, {context.Background(), "unknown"}} {
		if _, err := g.Admit(tc.ctx, tc.kind); !errors.Is(err, protocol.ErrRuntimePauseAdmissionInvalid) {
			t.Fatalf("untrusted action admitted: %v", err)
		}
	}
}

func TestRuntimePauseAndWaitDoesNotHoldEngineLock(t *testing.T) {
	g := newRuntimePauseCoordinator(false)
	receive := admitPauseLease(t, g, context.Background(), protocol.RuntimePauseReceive)
	mge := pauseEngineFixture(g)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- mge.PauseAndWait(ctx) }()
	select {
	case <-receive.Context().Done():
	case <-time.After(time.Second):
		t.Fatal("pause never latched")
	}
	getter := make(chan struct{})
	go func() { mge.GetGroupVUsers(); close(getter) }()
	select {
	case <-getter:
	case <-time.After(time.Second):
		t.Fatal("quiescence wait held the engine lock")
	}
	if at, paused := mge.PauseStartedAt(); !paused || at.IsZero() {
		t.Fatal("local active clock latch unavailable")
	}
	err := <-result
	var pending *PauseQuiescenceUnprovenError
	if !errors.Is(err, ErrPauseQuiescenceUnproven) || !errors.Is(err, context.DeadlineExceeded) || !errors.As(err, &pending) || pending.Cause == nil {
		t.Fatalf("unproved latch returned success or lost its cause/state: %v", err)
	}
	if _, paused := mge.PauseStartedAt(); !paused {
		t.Fatal("failed proof silently undid the latch")
	}
	if err := mge.Resume(); err != nil {
		t.Fatal(err)
	}
	receive.Release()
	if err := mge.PauseAndWait(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimePauseConcurrentResumeOrStopInvalidatesProof(t *testing.T) {
	for _, stop := range []bool{false, true} {
		t.Run(map[bool]string{false: "resume", true: "stop"}[stop], func(t *testing.T) {
			g := newRuntimePauseCoordinator(false)
			receive := admitPauseLease(t, g, context.Background(), protocol.RuntimePauseReceive)
			mge := pauseEngineFixture(g)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			result := make(chan error, 1)
			go func() { result <- mge.PauseAndWait(ctx) }()
			<-receive.Context().Done()
			if stop {
				mge.Stop()
			} else if err := mge.Resume(); err != nil {
				t.Fatal(err)
			}
			receive.Release()
			if err := <-result; !errors.Is(err, ErrPauseQuiescenceUnproven) {
				t.Fatalf("superseded pause advertised proof: %v", err)
			}
			if !stop {
				action := admitPauseLease(t, g, context.Background(), protocol.RuntimePauseAction)
				action.Release()
			}
		})
	}
}

func TestRuntimePauseStartPausedAndUnsupportedSource(t *testing.T) {
	g := newRuntimePauseCoordinator(true)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := g.Admit(ctx, protocol.RuntimePauseAction); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("StartPaused crossed execution admission: %v", err)
	}
	if !g.proven(g.latch()) {
		t.Fatal("traffic-free StartPaused failed empty quiescence")
	}
	g.resume()
	action := admitPauseLease(t, g, context.Background(), protocol.RuntimePauseAction)
	action.Release()
	g.unsupported = true
	if err := pauseEngineFixture(g).PauseAndWait(context.Background()); !errors.Is(err, ErrPauseQuiescenceUnproven) {
		t.Fatalf("unsupported runtime acquired a strong acknowledgement: %v", err)
	}
}

type pauseActionBudgetFixture struct{ calls atomic.Int64 }

func (p *pauseActionBudgetFixture) Name() string                             { return "eventhubs" }
func (*pauseActionBudgetFixture) Init(context.Context, map[string]any) error { return nil }
func (*pauseActionBudgetFixture) Teardown(context.Context) error             { return nil }
func (*pauseActionBudgetFixture) HealthCheck(context.Context) error          { return nil }
func (p *pauseActionBudgetFixture) Execute(ctx context.Context, _ *protocol.Action) (*protocol.Result, error) {
	p.calls.Add(1)
	// The adapter owns its original bounded action budget after admission.
	budget, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancel()
	select {
	case <-time.After(5 * time.Millisecond):
		return &protocol.Result{ActionName: "send", Success: true, Duration: 5 * time.Millisecond}, nil
	case <-budget.Done():
		return nil, budget.Err()
	}
}

func TestRuntimePauseAdmissionPrecedesActionBudgetAndClock(t *testing.T) {
	g := newRuntimePauseCoordinator(true)
	p := &pauseActionBudgetFixture{}
	mc := NewMetricsCollector(8)
	mc.resCollector = nil
	var timings []ResultTiming
	mc.timingObserver = func(timing ResultTiming) { timings = append(timings, timing) }
	mc.Start(5 * time.Millisecond)
	defer mc.Stop()
	script := &dsl.Script{Protocol: "eventhubs", Config: dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1, AutoTransactions: "per_action"}}, Actions: []dsl.Action{{Type: "eventhubs_produce", Name: "send"}}}
	vu := &VUser{ID: 0, Script: script, Proto: p, Metrics: mc, Variables: map[string]string{}}
	ctx, cancel := context.WithTimeout(protocol.WithRuntimePauseGate(context.Background(), "eventhubs", g), time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { vu.Run(ctx); close(done) }()
	// Holding admission longer than the request budget must not consume that
	// budget or become part of the measured per-action transaction duration.
	time.Sleep(100 * time.Millisecond)
	if p.calls.Load() != 0 {
		t.Fatal("paused action reached the adapter")
	}
	g.resume()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("admitted action did not complete")
	}
	mc.Stop()
	if p.calls.Load() != 1 || mc.TotalTransactions() != 1 || mc.TotalErrors() != 0 {
		t.Fatalf("pause affected action populations: calls=%d transactions=%d errors=%d", p.calls.Load(), mc.TotalTransactions(), mc.TotalErrors())
	}
	found := false
	for _, timing := range timings {
		if timing.BusinessTransaction {
			found = true
			if !timing.Success || timing.Duration >= 100*time.Millisecond {
				t.Fatalf("pause admission leaked into the action transaction clock: %+v", timing)
			}
		}
	}
	if !found {
		t.Fatal("action transaction timing was not observed")
	}
}

func TestRuntimePauseLeaseIdentityOverflowFailsClosed(t *testing.T) {
	g := newRuntimePauseCoordinator(false)
	live := admitPauseLease(t, g, context.Background(), protocol.RuntimePauseReceive)
	g.next = ^uint64(0)
	if _, err := g.Admit(context.Background(), protocol.RuntimePauseAction); !errors.Is(err, protocol.ErrRuntimePauseAdmissionInvalid) {
		t.Fatalf("wrapped lease identity admitted: %v", err)
	}
	epoch := g.latch()
	if g.proven(epoch) {
		t.Fatal("identity exhaustion erased a live lease")
	}
	live.Release()
	if !g.proven(epoch) {
		t.Fatal("original lease could not discharge after identity exhaustion")
	}
}

func TestRuntimePauseStartedAtClampsFutureBarrier(t *testing.T) {
	mge := pauseEngineFixture(newRuntimePauseCoordinator(false))
	future := time.Now().Add(time.Second)
	mge.runStartedAt = future
	if err := mge.PauseAndWait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if at, paused := mge.PauseStartedAt(); !paused || !at.Equal(future) {
		t.Fatalf("local pause clock lost future barrier clamp: %v %v", at, paused)
	}
}

type pauseLifecycleTokenFixture struct {
	gate  *runtimePauseCoordinator
	calls int
}

func (*pauseLifecycleTokenFixture) Name() string                               { return "eventhubs" }
func (*pauseLifecycleTokenFixture) Init(context.Context, map[string]any) error { return nil }
func (*pauseLifecycleTokenFixture) Teardown(context.Context) error             { return nil }
func (*pauseLifecycleTokenFixture) HealthCheck(context.Context) error          { return nil }
func (p *pauseLifecycleTokenFixture) Execute(ctx context.Context, action *protocol.Action) (*protocol.Result, error) {
	gate, present := protocol.RuntimePauseGateFromContext(ctx, "eventhubs")
	if !present || gate != p.gate {
		return nil, errors.New("phase gate missing")
	}
	// Even fresh teardown must carry newly admitted action authority; copying
	// the main action's released token would fail this actual checkpoint lease.
	checkpoint, err := protocol.AdmitRuntimePause(ctx, gate, protocol.RuntimePauseCheckpoint)
	if err != nil {
		return nil, err
	}
	checkpoint.Release()
	p.calls++
	return &protocol.Result{ActionName: action.Name, Success: true, Duration: time.Microsecond}, nil
}

func TestRuntimePauseInitMainAndFreshTeardownAuthority(t *testing.T) {
	g := newRuntimePauseCoordinator(false)
	p := &pauseLifecycleTokenFixture{gate: g}
	mc := NewMetricsCollector(8)
	mc.resCollector = nil
	mc.Start(time.Millisecond)
	defer mc.Stop()
	action := func(name string) dsl.Action { return dsl.Action{Type: "eventhubs_produce", Name: name} }
	script := &dsl.Script{Protocol: "eventhubs", Config: dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1}}, Init: []dsl.Action{action("init")}, Actions: []dsl.Action{action("main")}, Teardown: []dsl.Action{action("teardown")}}
	vu := &VUser{Script: script, Proto: p, Metrics: mc, Variables: map[string]string{}}
	vu.Run(protocol.WithRuntimePauseGate(context.Background(), "eventhubs", g))
	mc.Stop()
	if p.calls != 3 || mc.TotalErrors() != 0 {
		t.Fatalf("phase authority missing: calls=%d errors=%d", p.calls, mc.TotalErrors())
	}
	if !g.proven(g.latch()) {
		t.Fatal("completed lifecycle retained action/child authority")
	}
}

func TestRuntimePauseBindingsExcludeProducerOnlyAndIncludeZeroPeak(t *testing.T) {
	producer := processorResourceScript()
	producer.Actions = []dsl.Action{{Type: "eventhubs_produce"}}
	consumer := processorResourceScript()
	resources, err := newEngineRuntimeResources([]GroupConfig{{Name: "producer", Script: producer, VUsers: 1}, {Name: "zero", Script: consumer, VUsers: 0, RuntimeResourcePeakFrozen: true}})
	if err != nil {
		t.Fatal(err)
	}
	pauses := newEngineRuntimePause(resources, true)
	if gate, present := protocol.RuntimePauseGateFromContext(pauses.bind(context.Background(), "producer"), "eventhubs"); present || gate != nil {
		t.Fatal("producer-only context gained processor pause authority")
	}
	gate, present := protocol.RuntimePauseGateFromContext(pauses.bind(context.Background(), "zero"), "eventhubs")
	if !present || gate == nil {
		t.Fatal("zero-peak consumer lost frozen coordinator")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if _, err := protocol.AdmitRuntimePause(ctx, gate, protocol.RuntimePauseAction); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("zero-peak StartPaused admitted action", err)
	}
}

func TestRuntimePauseTerminalCleanupKeepsOrdinaryAdmissionClosed(t *testing.T) {
	g := newRuntimePauseCoordinator(false)
	epoch := g.latch()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	cleanup := bindRuntimePauseTeardown(ctx, "eventhubs", g)
	waiting := make(chan protocol.RuntimePauseLease, 1)
	go func() { lease, _ := g.Admit(cleanup, protocol.RuntimePauseAction); waiting <- lease }()
	select {
	case <-waiting:
		t.Fatal("ordinary paused teardown bypassed its boundary")
	case <-time.After(20 * time.Millisecond):
	}
	if !g.proven(epoch) {
		t.Fatal("waiting cleanup intent changed ordinary pause proof")
	}
	g.beginTerminal(false)
	if g.proven(epoch) {
		t.Fatal("terminal cleanup retained a prior PAUSED proof")
	}
	var action protocol.RuntimePauseLease
	select {
	case action = <-waiting:
	case <-time.After(time.Second):
		t.Fatal("explicit terminal cleanup remained parked")
	}
	if action == nil {
		t.Fatal("terminal action was rejected")
	}
	if _, err := g.Admit(ctx, protocol.RuntimePauseAction); !errors.Is(err, protocol.ErrRuntimePauseAdmissionInvalid) {
		t.Fatal("terminal boundary reopened main admission", err)
	}
	// Background loops stay parked until lifetime/demand turnover; treating
	// this boundary as an adapter fault would kill cleanup's existing session.
	for _, kind := range []protocol.RuntimePauseActivity{protocol.RuntimePauseReceive, protocol.RuntimePauseTail} {
		blocked, stop := context.WithTimeout(cleanup, time.Millisecond)
		_, err := g.Admit(blocked, kind)
		stop()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("terminal boundary reopened %s: %v", kind, err)
		}
	}
	receive := admitPauseLease(t, g, action.Context(), protocol.RuntimePauseReceive)
	checkpoint := admitPauseLease(t, g, action.Context(), protocol.RuntimePauseCheckpoint)
	g.beginTerminal(true)
	for _, admitted := range []protocol.RuntimePauseLease{action, receive, checkpoint} {
		if admitted.Context().Err() != nil {
			t.Fatal("hard escalation canceled existing fresh bounded teardown")
		}
	}
	action.Release()
	receive.Release()
	g.mu.Lock()
	charged := len(g.leases)
	g.mu.Unlock()
	if charged != 1 {
		t.Fatal("cleanup checkpoint lost actual-return charge", charged)
	}
	checkpoint.Release()
	g.close()
	if _, err := g.Admit(cleanup, protocol.RuntimePauseAction); !errors.Is(err, protocol.ErrRuntimePauseAdmissionInvalid) {
		t.Fatal("cleanup resurrected final closed authority")
	}
}

func TestRuntimePauseTerminalCleanupRequiresBoundedSameGateContext(t *testing.T) {
	g := newRuntimePauseCoordinator(false)
	g.beginTerminal(true)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	cleanup := bindRuntimePauseTeardown(ctx, "eventhubs", g)
	other := newRuntimePauseCoordinator(false)
	other.beginTerminal(false)
	for _, candidate := range []struct {
		gate *runtimePauseCoordinator
		ctx  context.Context
	}{
		{g, context.WithoutCancel(cleanup)},
		{other, cleanup},
		{g, bindRuntimePauseTeardown(context.Background(), "eventhubs", g)},
	} {
		if _, err := candidate.gate.Admit(candidate.ctx, protocol.RuntimePauseAction); !errors.Is(err, protocol.ErrRuntimePauseAdmissionInvalid) {
			t.Fatal("invalid cleanup authority admitted", err)
		}
	}
	action := admitPauseLease(t, g, cleanup, protocol.RuntimePauseAction)
	action.Release()
}

type pausedTerminalCleanupFixture struct {
	gate         *runtimePauseCoordinator
	engine       *MultiGroupEngine
	mainReturned chan struct{}
	cleanupCalls atomic.Int64
}

func (*pausedTerminalCleanupFixture) Name() string                               { return "eventhubs" }
func (*pausedTerminalCleanupFixture) Init(context.Context, map[string]any) error { return nil }
func (*pausedTerminalCleanupFixture) Teardown(context.Context) error             { return nil }
func (*pausedTerminalCleanupFixture) HealthCheck(context.Context) error          { return nil }
func (p *pausedTerminalCleanupFixture) Execute(ctx context.Context, action *protocol.Action) (*protocol.Result, error) {
	if action.Name == "main" {
		if err := p.engine.Pause(); err != nil {
			return nil, err
		}
		close(p.mainReturned)
	} else {
		if _, bounded := ctx.Deadline(); !bounded {
			return nil, errors.New("cleanup lost teardown TTL")
		}
		receive, err := protocol.AdmitRuntimePause(ctx, p.gate, protocol.RuntimePauseReceive)
		if err != nil {
			return nil, err
		}
		receive.Release()
		checkpoint, err := protocol.AdmitRuntimePause(ctx, p.gate, protocol.RuntimePauseCheckpoint)
		if err != nil {
			return nil, err
		}
		checkpoint.Release()
		p.cleanupCalls.Add(1)
	}
	return &protocol.Result{ActionName: action.Name, Success: true}, nil
}
func TestRuntimePausePausedStopRunsBoundedUserTeardown(t *testing.T) {
	t.Setenv("APEXLOAD_TEARDOWN_TIMEOUT", "500ms")
	for _, stop := range []string{"graceful", "hard", "cancelled"} {
		t.Run(stop, func(t *testing.T) {
			g := newRuntimePauseCoordinator(false)
			mge := pauseEngineFixture(g)
			base, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx := protocol.WithRuntimePauseGate(base, "eventhubs", g)
			boundary, softCancel := context.WithCancel(ctx)
			defer softCancel()
			mge.baseCtx, mge.cancel, mge.softStop = base, cancel, softCancel
			if stop == "cancelled" {
				go mge.closeRuntimeAdmissionOnDone(base)
			}
			mge.runStartedAt = time.Now()
			p := &pausedTerminalCleanupFixture{gate: g, engine: mge, mainReturned: make(chan struct{})}
			mc := NewMetricsCollector(8)
			mc.resCollector = nil
			mc.Start(time.Millisecond)
			defer mc.Stop()
			script := &dsl.Script{Protocol: "eventhubs", Config: dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1}}, Actions: []dsl.Action{{Type: "eventhubs_produce", Name: "main"}}, Teardown: []dsl.Action{{Type: "eventhubs_consume", Name: "cleanup"}}}
			vu := &VUser{Script: script, Proto: p, Metrics: mc, Variables: map[string]string{}, HardStopCtx: ctx, ActionBoundaryCtx: boundary, PauseControl: mge}
			done := make(chan struct{})
			go func() { vu.Run(boundary); close(done) }()
			select {
			case <-p.mainReturned:
			case <-time.After(time.Second):
				t.Fatal("main never reached pause boundary")
			}
			proof, proofCancel := context.WithTimeout(context.Background(), time.Second)
			defer proofCancel()
			if err := mge.PauseAndWait(proof); err != nil {
				t.Fatal(err)
			}
			if p.cleanupCalls.Load() != 0 {
				t.Fatal("ordinary PAUSED acknowledgement admitted compensation")
			}
			switch stop {
			case "hard":
				mge.Stop()
			case "graceful":
				mge.GracefulStop()
			case "cancelled":
				cancel()
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("paused stopping cleanup missed bounded teardown")
			}
			mc.Stop()
			if p.cleanupCalls.Load() != 1 || mc.TotalErrors() != 0 {
				t.Fatalf("cleanup missing or invalid: calls=%d errors=%d", p.cleanupCalls.Load(), mc.TotalErrors())
			}
		})
	}
}

func TestRuntimePauseCancellationStrippedTokenCannotMintNewAuthority(t *testing.T) {
	for _, cause := range []string{"expired", "hard-stop"} {
		t.Run(cause, func(t *testing.T) {
			g := newRuntimePauseCoordinator(false)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
			defer cancel()
			if cause == "hard-stop" {
				ctx, cancel = context.WithCancel(context.Background())
				defer cancel()
			}
			action := admitPauseLease(t, g, ctx, protocol.RuntimePauseAction)
			if cause == "hard-stop" {
				g.beginTerminal(true)
			} else {
				// Wait on the lease's own context: a parent's Done closes
				// before its children are cancelled, so the lease can still
				// be live for a moment after ctx.Done().
				<-action.Context().Done()
			}
			stripped := context.WithoutCancel(action.Context())
			for _, kind := range []protocol.RuntimePauseActivity{protocol.RuntimePauseAction, protocol.RuntimePauseReceive, protocol.RuntimePauseCheckpoint} {
				if _, err := g.Admit(stripped, kind); !errors.Is(err, protocol.ErrRuntimePauseAdmissionInvalid) {
					t.Fatalf("cancelled parent minted %s through stripped cancellation: %v", kind, err)
				}
			}
			g.mu.Lock()
			charged := len(g.leases)
			g.mu.Unlock()
			if charged != 1 {
				t.Fatal("cancelled action lost actual-return proof charge", charged)
			}
			action.Release()
			g.mu.Lock()
			charged = len(g.leases)
			g.mu.Unlock()
			if charged != 0 {
				t.Fatal("returned action retained stale proof charge", charged)
			}
		})
	}
}
