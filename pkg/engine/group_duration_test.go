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

var (
	rejectScaleInit      atomic.Bool
	successfulScaleInits atomic.Int64
)

type scaleInitFailureProtocol struct{}

type gracefulBoundaryTestState struct {
	firstStarted     chan struct{}
	firstReturned    chan struct{}
	releaseFirst     chan struct{}
	allowFinalGoal   chan struct{}
	finalGoalReached chan struct{}
	actions          chan string
	firstOnce        sync.Once
	returnedOnce     sync.Once
	goalOnce         sync.Once
	actionCanceled   atomic.Bool
	adapterTeardowns atomic.Int64
	initCtxMu        sync.Mutex
	initCtx          context.Context
}

type gracefulBoundaryTestProtocol struct {
	state *gracefulBoundaryTestState
}

type goalAfterInitBoundaryProtocol struct {
	state *gracefulBoundaryTestState
}

type blockingFinalGoalProtocol struct {
	state *gracefulBoundaryTestState
}

type contextIgnoringLifecycleInitProtocol struct {
	state *gracefulBoundaryTestState
}

var (
	gracefulBoundaryStateMu sync.Mutex
	gracefulBoundaryState   *gracefulBoundaryTestState
)

func (*scaleInitFailureProtocol) Name() string { return "scale-init-failure-test" }
func (*scaleInitFailureProtocol) Init(context.Context, map[string]any) error {
	if rejectScaleInit.Load() {
		return errors.New("injected init failure")
	}
	successfulScaleInits.Add(1)
	return nil
}
func (*scaleInitFailureProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	return &protocol.Result{ActionName: action.Name, Success: true}, nil
}
func (*scaleInitFailureProtocol) Teardown(context.Context) error    { return nil }
func (*scaleInitFailureProtocol) HealthCheck(context.Context) error { return nil }

func (*gracefulBoundaryTestProtocol) Name() string { return "graceful-boundary-test" }
func (p *gracefulBoundaryTestProtocol) Init(ctx context.Context, _ map[string]any) error {
	if p.state != nil {
		p.state.initCtxMu.Lock()
		p.state.initCtx = ctx
		p.state.initCtxMu.Unlock()
	}
	return nil
}
func (p *gracefulBoundaryTestProtocol) Execute(ctx context.Context, action *protocol.Action) (*protocol.Result, error) {
	if p.state != nil && p.state.actions != nil {
		p.state.actions <- action.Name
	}
	if p.state != nil && action.Name == "first" {
		p.state.firstOnce.Do(func() { close(p.state.firstStarted) })
		select {
		case <-p.state.releaseFirst:
			p.state.returnedOnce.Do(func() { close(p.state.firstReturned) })
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return &protocol.Result{ActionName: action.Name, Success: true, Duration: time.Millisecond}, nil
}
func (*gracefulBoundaryTestProtocol) Teardown(context.Context) error    { return nil }
func (*gracefulBoundaryTestProtocol) HealthCheck(context.Context) error { return nil }

func (*goalAfterInitBoundaryProtocol) Name() string { return "goal-after-init-boundary-test" }
func (*goalAfterInitBoundaryProtocol) Init(context.Context, map[string]any) error {
	return nil
}
func (p *goalAfterInitBoundaryProtocol) Execute(ctx context.Context, action *protocol.Action) (*protocol.Result, error) {
	if p.state != nil {
		select {
		case <-p.state.firstStarted:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return &protocol.Result{
		ActionName:          action.Name,
		BusinessTransaction: true,
		Success:             true,
		Duration:            time.Millisecond,
	}, nil
}
func (*goalAfterInitBoundaryProtocol) Teardown(context.Context) error    { return nil }
func (*goalAfterInitBoundaryProtocol) HealthCheck(context.Context) error { return nil }

func (*blockingFinalGoalProtocol) Name() string { return "blocking-final-goal-test" }
func (*blockingFinalGoalProtocol) Init(context.Context, map[string]any) error {
	return nil
}
func (*blockingFinalGoalProtocol) Execute(context.Context, *protocol.Action) (*protocol.Result, error) {
	panic("blocking final goal protocol requires ExecuteStream")
}
func (p *blockingFinalGoalProtocol) ExecuteStream(ctx context.Context, action *protocol.Action, emit func(*protocol.Result) error) (*protocol.Result, error) {
	if p.state != nil {
		p.state.firstOnce.Do(func() { close(p.state.firstStarted) })
		select {
		case <-p.state.allowFinalGoal:
		case <-ctx.Done():
			p.state.actionCanceled.Store(true)
			return nil, ctx.Err()
		}
	}
	if emit != nil {
		if err := emit(&protocol.Result{
			ActionName:          "final goal transaction",
			BusinessTransaction: true,
			Success:             true,
			Duration:            time.Millisecond,
		}); err != nil {
			return nil, err
		}
	}
	if p.state != nil {
		p.state.goalOnce.Do(func() { close(p.state.finalGoalReached) })
		select {
		case <-p.state.releaseFirst:
			p.state.returnedOnce.Do(func() { close(p.state.firstReturned) })
		case <-ctx.Done():
			p.state.actionCanceled.Store(true)
			return nil, ctx.Err()
		}
	}
	return &protocol.Result{ActionName: action.Name, Success: true, Duration: time.Millisecond}, nil
}
func (*blockingFinalGoalProtocol) Teardown(context.Context) error    { return nil }
func (*blockingFinalGoalProtocol) HealthCheck(context.Context) error { return nil }

func (*contextIgnoringLifecycleInitProtocol) Name() string {
	return "context-ignoring-lifecycle-init-test"
}
func (p *contextIgnoringLifecycleInitProtocol) Init(context.Context, map[string]any) error {
	if p.state != nil {
		p.state.firstOnce.Do(func() { close(p.state.firstStarted) })
		<-p.state.releaseFirst
	}
	return nil
}
func (p *contextIgnoringLifecycleInitProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	if p.state != nil && p.state.actions != nil {
		p.state.actions <- action.Name
	}
	return &protocol.Result{ActionName: action.Name, Success: true}, nil
}
func (p *contextIgnoringLifecycleInitProtocol) Teardown(context.Context) error {
	if p.state != nil {
		p.state.adapterTeardowns.Add(1)
	}
	return nil
}
func (*contextIgnoringLifecycleInitProtocol) HealthCheck(context.Context) error { return nil }

func init() {
	protocol.Register("scale-init-failure-test", func() protocol.Protocol { return &scaleInitFailureProtocol{} })
	protocol.Register("graceful-boundary-test", func() protocol.Protocol {
		gracefulBoundaryStateMu.Lock()
		defer gracefulBoundaryStateMu.Unlock()
		return &gracefulBoundaryTestProtocol{state: gracefulBoundaryState}
	})
	protocol.Register("goal-after-init-boundary-test", func() protocol.Protocol {
		gracefulBoundaryStateMu.Lock()
		defer gracefulBoundaryStateMu.Unlock()
		return &goalAfterInitBoundaryProtocol{state: gracefulBoundaryState}
	})
	protocol.Register("blocking-final-goal-test", func() protocol.Protocol {
		gracefulBoundaryStateMu.Lock()
		defer gracefulBoundaryStateMu.Unlock()
		return &blockingFinalGoalProtocol{state: gracefulBoundaryState}
	})
	protocol.Register("context-ignoring-lifecycle-init-test", func() protocol.Protocol {
		gracefulBoundaryStateMu.Lock()
		defer gracefulBoundaryStateMu.Unlock()
		return &contextIgnoringLifecycleInitProtocol{state: gracefulBoundaryState}
	})
}

func installGracefulBoundaryState(t *testing.T) *gracefulBoundaryTestState {
	t.Helper()
	state := &gracefulBoundaryTestState{
		firstStarted:     make(chan struct{}),
		firstReturned:    make(chan struct{}),
		releaseFirst:     make(chan struct{}),
		allowFinalGoal:   make(chan struct{}),
		finalGoalReached: make(chan struct{}),
		actions:          make(chan string, 16),
	}
	gracefulBoundaryStateMu.Lock()
	gracefulBoundaryState = state
	gracefulBoundaryStateMu.Unlock()
	t.Cleanup(func() {
		gracefulBoundaryStateMu.Lock()
		if gracefulBoundaryState == state {
			gracefulBoundaryState = nil
		}
		gracefulBoundaryStateMu.Unlock()
	})
	return state
}

func boundaryActionNames(state *gracefulBoundaryTestState) []string {
	var names []string
	for {
		select {
		case name := <-state.actions:
			names = append(names, name)
		default:
			return names
		}
	}
}

func TestAdjustDurationRetainsCompletedPauseCompensation(t *testing.T) {
	ready := make(chan struct{})
	close(ready)
	loopCtx, loopCancel := context.WithCancel(context.Background())
	defer loopCancel()
	launchCtx, launchCancel := context.WithCancel(loopCtx)
	defer launchCancel()
	mge := &MultiGroupEngine{
		baseCtx:            context.Background(),
		loopCtx:            loopCtx,
		launchCtx:          launchCtx,
		launchOpen:         true,
		pauseCh:            ready,
		pauseChanged:       make(chan struct{}),
		baseTargetDuration: time.Minute,
		targetDuration:     time.Minute,
		controlsReady:      true,
		runStartedAt:       time.Now(),
	}
	if err := mge.Pause(); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	mge.mu.Lock()
	mge.pauseStartTime = time.Now().Add(-30 * time.Second)
	mge.mu.Unlock()
	if err := mge.Resume(); err != nil {
		t.Fatalf("Resume: %v", err)
	}

	mge.mu.Lock()
	compensation := mge.pauseCompensation
	mge.mu.Unlock()
	if compensation < 30*time.Second {
		t.Fatalf("pause compensation = %s, want at least 30s", compensation)
	}

	if err := mge.AdjustDuration(2 * time.Minute); err != nil {
		t.Fatalf("AdjustDuration: %v", err)
	}
	if got, want := mge.GetTargetDuration(), 2*time.Minute+compensation; got != want {
		t.Fatalf("target duration after adjust = %s, want %s", got, want)
	}
}

func TestGracefulStopBeforeRunIsLatched(t *testing.T) {
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups: []GroupConfig{{
			Name:   "group",
			Script: &dsl.Script{Name: "script", Protocol: "stub", Actions: []dsl.Action{{Name: "request", Type: "request", Target: "/"}}},
			VUsers: 1,
		}},
		Duration: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}
	mge.GracefulStop()
	result, err := mge.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.TotalTransactions != 0 {
		t.Fatalf("pre-run graceful stop emitted %d transactions", result.TotalTransactions)
	}
}

func TestGracefulStopSynchronouslyClosesScaleAdmission(t *testing.T) {
	baseCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	mge := &MultiGroupEngine{
		groups: []GroupConfig{{
			Name: "group",
			Script: &dsl.Script{
				Name:     "scale-fence",
				Protocol: "stub",
				Actions:  []dsl.Action{{Name: "request", Type: "request", Target: "/"}},
			},
			AllowZeroVUsers: true,
		}},
		baseCtx:       baseCtx,
		loopCtx:       baseCtx,
		wg:            &wg,
		controlsReady: true,
		scalingOpen:   true,
		softStop:      func() {},
	}

	mge.GracefulStop()
	mge.mu.Lock()
	scalingOpen := mge.scalingOpen
	mge.mu.Unlock()
	if scalingOpen {
		t.Fatal("GracefulStop returned with scale admission still open")
	}

	const callers = 16
	errs := make(chan error, callers)
	for i := 0; i < callers; i++ {
		go func() {
			errs <- mge.ScaleVUsers("group", 1)
		}()
	}
	for i := 0; i < callers; i++ {
		if err := <-errs; err == nil || !strings.Contains(err.Error(), "not running") {
			t.Fatalf("ScaleVUsers after GracefulStop error = %v, want engine not running", err)
		}
	}
	if got := mge.GetGroupVUsers()["group"]; got != 0 {
		t.Fatalf("post-fence scale changed target to %d, want 0", got)
	}
}

func TestGracefulStopInterruptsPreBaselinePromptly(t *testing.T) {
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups: []GroupConfig{{
			Name:   "group",
			Script: &dsl.Script{Name: "script", Protocol: "stub", Actions: []dsl.Action{{Name: "request", Type: "request", Target: "/"}}},
			VUsers: 1,
		}},
		Duration:    time.Second,
		PreBaseline: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, runErr := mge.Run(context.Background())
		done <- runErr
	}()
	deadline := time.Now().Add(time.Second)
	for {
		mge.mu.Lock()
		initialized := mge.softStop != nil
		mge.mu.Unlock()
		if initialized {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("engine did not initialize soft-stop control")
		}
		time.Sleep(time.Millisecond)
	}

	started := time.Now()
	mge.GracefulStop()
	select {
	case runErr := <-done:
		if !errors.Is(runErr, context.Canceled) {
			t.Fatalf("Run error = %v, want soft-stop cancellation", runErr)
		}
		if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
			t.Fatalf("graceful stop took %s during pre-baseline", elapsed)
		}
	case <-time.After(time.Second):
		t.Fatal("graceful stop waited for the full pre-baseline window")
	}
}

func TestGracefulStopFinishesCurrentActionAndStopsAtBoundary(t *testing.T) {
	state := installGracefulBoundaryState(t)
	script := &dsl.Script{
		Name: "graceful-boundary", Protocol: "graceful-boundary-test",
		Config: dsl.ScriptConfig{
			RunLogic:  dsl.RunLogicConfig{Iterations: 1},
			ThinkTime: dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 5000},
		},
		Actions: []dsl.Action{
			{Name: "first", Type: "request", Target: "/first", ThinkTimeAfter: true},
			{Name: "second", Type: "request", Target: "/second"},
		},
	}
	mge, err := NewMultiGroup(MultiGroupConfig{Groups: []GroupConfig{{Name: "group", Script: script, VUsers: 1}}, Duration: 10 * time.Second})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, runErr := mge.Run(context.Background())
		done <- runErr
	}()
	select {
	case <-state.firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first action did not start")
	}

	started := time.Now()
	mge.GracefulStop()
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
		if elapsed := time.Since(started); elapsed > time.Second {
			t.Fatalf("graceful stop waited through think time: %s", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("graceful stop did not finish after the current action")
	}
	if got := boundaryActionNames(state); len(got) != 1 || got[0] != "first" {
		t.Fatalf("executed actions = %v, want only the in-flight first action", got)
	}
}

func TestGracefulStopWakesPausedVUserAtActionBoundary(t *testing.T) {
	state := installGracefulBoundaryState(t)
	script := &dsl.Script{
		Name: "paused-graceful-boundary", Protocol: "graceful-boundary-test",
		Config: dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1}},
		Actions: []dsl.Action{
			{Name: "first", Type: "request", Target: "/first"},
			{Name: "second", Type: "request", Target: "/second"},
		},
	}
	mge, err := NewMultiGroup(MultiGroupConfig{Groups: []GroupConfig{{Name: "group", Script: script, VUsers: 1}}, Duration: 10 * time.Second})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, runErr := mge.Run(context.Background())
		done <- runErr
	}()
	select {
	case <-state.firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first action did not start")
	}
	if err := mge.Pause(); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	close(state.releaseFirst)
	select {
	case <-state.firstReturned:
	case <-time.After(time.Second):
		t.Fatal("first action did not return")
	}
	// The VUser is now held at the next action's pause gate. GracefulStop must
	// wake that gate without requiring Resume and without starting action two.
	mge.GracefulStop()
	select {
	case runErr := <-done:
		if runErr != nil {
			t.Fatalf("Run: %v", runErr)
		}
	case <-time.After(time.Second):
		t.Fatal("paused VUser was not released by graceful stop")
	}
	if got := boundaryActionNames(state); len(got) != 1 || got[0] != "first" {
		t.Fatalf("executed actions = %v, want only first", got)
	}
}

func TestSharedGoalExhaustionDoesNotTruncateReservedIteration(t *testing.T) {
	state := installGracefulBoundaryState(t)
	script := &dsl.Script{
		Name: "reserved-goal-boundary", Protocol: "graceful-boundary-test",
		Config: dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1, GlobalIterations: 1}},
		Actions: []dsl.Action{
			{Name: "first", Type: "request", Target: "/first"},
			{Name: "second", Type: "request", Target: "/second"},
		},
	}
	mge, err := NewMultiGroup(MultiGroupConfig{Groups: []GroupConfig{{Name: "group", Script: script, VUsers: 2}}, Duration: 10 * time.Second})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, runErr := mge.Run(context.Background())
		done <- runErr
	}()
	select {
	case <-state.firstStarted:
	case <-time.After(time.Second):
		t.Fatal("reserved iteration did not start")
	}
	deadline := time.Now().Add(time.Second)
	for {
		mge.mu.Lock()
		goalExhausted := mge.loopCtx != nil && mge.loopCtx.Err() != nil
		mge.mu.Unlock()
		if goalExhausted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("second VUser did not trip shared goal exhaustion")
		}
		time.Sleep(time.Millisecond)
	}
	close(state.releaseFirst)
	select {
	case runErr := <-done:
		if runErr != nil {
			t.Fatalf("Run: %v", runErr)
		}
	case <-time.After(time.Second):
		t.Fatal("reserved iteration did not complete")
	}
	if got := boundaryActionNames(state); len(got) != 2 || got[0] != "first" || got[1] != "second" {
		t.Fatalf("executed actions = %v, want complete reserved iteration [first second]", got)
	}
}

func TestMultiGroupAtOnceInitGracefulStopFinishesOnlyCurrentAction(t *testing.T) {
	state := installGracefulBoundaryState(t)
	script := &dsl.Script{
		Name: "at-once-graceful-boundary", Protocol: "graceful-boundary-test",
		Init: []dsl.Action{
			{Name: "first", Type: "request", Target: "/first"},
			{Name: "second", Type: "request", Target: "/second"},
		},
		Actions: []dsl.Action{{Name: "main", Type: "request", Target: "/main"}},
	}
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups: []GroupConfig{{
			Name: "at-once", Script: script, VUsers: 1, InitStrategy: "at_once",
		}},
		Duration: 10 * time.Second,
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
	case <-state.firstStarted:
	case <-time.After(time.Second):
		t.Fatal("at-once first init action did not start")
	}

	mge.GracefulStop()
	select {
	case runErr := <-done:
		t.Fatalf("graceful stop interrupted the current at-once init action: %v", runErr)
	case <-time.After(50 * time.Millisecond):
	}
	close(state.releaseFirst)
	select {
	case runErr := <-done:
		if runErr != nil {
			t.Fatalf("Run: %v", runErr)
		}
	case <-time.After(time.Second):
		t.Fatal("at-once graceful stop did not finish after the current action")
	}
	if got := boundaryActionNames(state); len(got) != 1 || got[0] != "first" {
		t.Fatalf("at-once init actions = %v, want only first", got)
	}
}

func TestMultiGroupAtOnceInitStopsAfterEarlierGroupGoal(t *testing.T) {
	state := installGracefulBoundaryState(t)
	goalScript := &dsl.Script{
		Name: "goal-owner", Protocol: "goal-after-init-boundary-test",
		Config: dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{
			Iterations:      1,
			MaxTransactions: 1,
		}},
		Actions: []dsl.Action{{
			Name:     "goal transaction",
			Type:     "java_vuser_run",
			Metadata: map[string]any{"emits_business_transactions": true},
		}},
	}
	atOnceScript := &dsl.Script{
		Name: "later-at-once", Protocol: "graceful-boundary-test",
		Init: []dsl.Action{
			{Name: "first", Type: "request", Target: "/first"},
			{Name: "second", Type: "request", Target: "/second"},
		},
		Actions: []dsl.Action{{Name: "main", Type: "request", Target: "/main"}},
	}
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups: []GroupConfig{
			{Name: "goal", Script: goalScript, VUsers: 1},
			{Name: "at-once", Script: atOnceScript, VUsers: 1, InitStrategy: "at_once"},
		},
		Duration: 10 * time.Second,
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
	case <-state.firstStarted:
	case <-time.After(time.Second):
		t.Fatal("later at-once init action did not start")
	}
	deadline := time.Now().Add(time.Second)
	for {
		mge.mu.Lock()
		goalStoppedRun := mge.loopCtx != nil && mge.loopCtx.Err() != nil
		mge.mu.Unlock()
		if goalStoppedRun {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("earlier group did not satisfy its exact goal")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case runErr := <-done:
		t.Fatalf("goal cancellation interrupted the current at-once action: %v", runErr)
	case <-time.After(50 * time.Millisecond):
	}
	state.initCtxMu.Lock()
	retainedInitCtx := state.initCtx
	state.initCtxMu.Unlock()
	if retainedInitCtx == nil || retainedInitCtx.Err() != nil {
		t.Fatalf("at-once protocol Init context after peer goal = %v, want live until the entitled action drains", retainedInitCtx)
	}
	close(state.releaseFirst)
	select {
	case runErr := <-done:
		if runErr != nil {
			t.Fatalf("Run: %v", runErr)
		}
	case <-time.After(time.Second):
		t.Fatal("run did not finish after the current at-once init action")
	}
	if got := boundaryActionNames(state); len(got) != 1 || got[0] != "first" {
		t.Fatalf("at-once init actions = %v, want only first after peer goal", got)
	}
}

func TestMultiGroupAtOnceDoesNotRunUserTeardownWhenAdapterInitReturnsAfterStop(t *testing.T) {
	state := installGracefulBoundaryState(t)
	goalScript := &dsl.Script{
		Name: "goal owner", Protocol: "goal-after-init-boundary-test",
		Config: dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{
			Iterations:      1,
			MaxTransactions: 1,
		}},
		Actions: []dsl.Action{{
			Name:     "goal transaction",
			Type:     "java_vuser_run",
			Metadata: map[string]any{"emits_business_transactions": true},
		}},
	}
	atOnceScript := &dsl.Script{
		Name:     "stopped during adapter init",
		Protocol: "context-ignoring-lifecycle-init-test",
		Init:     []dsl.Action{{Name: "user init", Type: "request", Target: "/init"}},
		Actions:  []dsl.Action{{Name: "user main", Type: "request", Target: "/main"}},
		Teardown: []dsl.Action{{Name: "user teardown", Type: "request", Target: "/teardown"}},
	}
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups: []GroupConfig{
			{Name: "goal", Script: goalScript, VUsers: 1},
			{Name: "at-once", Script: atOnceScript, VUsers: 1, InitStrategy: "at_once"},
		},
		Duration: 5 * time.Second,
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
	case <-state.firstStarted:
	case <-time.After(time.Second):
		t.Fatal("context-ignoring lifecycle adapter did not enter Init")
	}
	deadline := time.Now().Add(time.Second)
	for {
		mge.mu.Lock()
		stopped := mge.loopCtx != nil && mge.loopCtx.Err() != nil
		mge.mu.Unlock()
		if stopped {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("goal owner did not stop the shared loop")
		}
		time.Sleep(time.Millisecond)
	}
	close(state.releaseFirst)
	select {
	case runErr := <-done:
		if runErr != nil {
			t.Fatalf("Run: %v", runErr)
		}
	case <-time.After(time.Second):
		t.Fatal("run did not finish after the context-ignoring Init returned")
	}
	close(state.actions)
	var userActions []string
	for action := range state.actions {
		userActions = append(userActions, action)
	}
	if len(userActions) != 0 {
		t.Fatalf("user lifecycle actions = %v, want none after adapter-only Init", userActions)
	}
	if got := state.adapterTeardowns.Load(); got != 1 {
		t.Fatalf("adapter teardown calls = %d, want exactly the initialized adapter cleanup", got)
	}
}

func TestMultiGroupZeroStartScaleRejectsUntilAtOnceInitCompletes(t *testing.T) {
	state := installGracefulBoundaryState(t)
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(state.releaseFirst) }) }
	t.Cleanup(release)

	script := &dsl.Script{
		Name:     "zero-start-at-once",
		Protocol: "context-ignoring-lifecycle-init-test",
		Config:   dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1}},
		Init:     []dsl.Action{{Name: "user init", Type: "request", Target: "/init"}},
		Actions:  []dsl.Action{{Name: "main", Type: "request", Target: "/main"}},
	}
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups: []GroupConfig{{
			Name: "at-once", Script: script, VUsers: 0, AllowZeroVUsers: true, InitStrategy: "at_once",
		}},
		Duration: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	joined := false
	go func() {
		_, runErr := mge.Run(ctx)
		done <- runErr
	}()
	t.Cleanup(func() {
		release()
		cancel()
		if joined {
			return
		}
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("zero-start at-once run did not stop")
		}
	})

	select {
	case <-state.firstStarted:
	case <-time.After(time.Second):
		t.Fatal("at-once adapter did not enter shared Init")
	}
	started := time.Now()
	if err := mge.ScaleVUsers("at-once", 1); !errors.Is(err, ErrInitialRampInProgress) {
		t.Fatalf("scale during shared at-once Init = %v, want initial-ramp rejection", err)
	}
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("scale rejection took %s, want prompt return", elapsed)
	}
	select {
	case action := <-state.actions:
		t.Fatalf("action %q ran before shared at-once Init completed", action)
	case <-time.After(100 * time.Millisecond):
	}

	release()
	deadlineReady := time.Now().Add(time.Second)
	for !mge.ScalingReady() && time.Now().Before(deadlineReady) {
		time.Sleep(time.Millisecond)
	}
	if !mge.ScalingReady() {
		t.Fatal("scaling did not become ready after shared at-once Init")
	}
	if err := mge.ScaleVUsers("at-once", 1); err != nil {
		t.Fatalf("scale after shared at-once Init: %v", err)
	}
	seenMain := false
	deadline := time.After(time.Second)
	for !seenMain {
		select {
		case action := <-state.actions:
			seenMain = action == "main"
		case <-deadline:
			t.Fatal("scaled VUser did not run after shared at-once Init")
		}
	}

	cancel()
	select {
	case runErr := <-done:
		joined = true
		if runErr != nil {
			t.Fatalf("Run: %v", runErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("zero-start at-once run did not stop after cancellation")
	}
}

func TestAtOnceSourceTransactionsRejectNonlinearPaths(t *testing.T) {
	script := &dsl.Script{
		Name: "nonlinear source transaction", Protocol: "stub",
		Init: []dsl.Action{{
			Name: "branch", Type: "control_if",
			Control: &dsl.ControlFlow{
				Condition: &dsl.Condition{Expr: "true"},
				Steps:     []dsl.Action{sourceTransactionAction("lr_start_transaction", "")},
				Else:      []dsl.Action{sourceTransactionAction("lr_end_transaction", "LR_PASS")},
			},
		}},
		Actions: []dsl.Action{{Name: "main", Type: "request", Target: "/"}},
	}
	_, err := NewMultiGroup(MultiGroupConfig{
		Groups:   []GroupConfig{{Name: "at-once", Script: script, VUsers: 1, InitStrategy: "at_once"}},
		Duration: time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "source transaction") {
		t.Fatalf("NewMultiGroup error = %v, want nonlinear at-once source transaction rejection", err)
	}
}

func TestAtOnceLifecycleFinalizesOpenSourceTransactionBeforeDiscard(t *testing.T) {
	installTeardownGoalDenialState(t)
	sink := &sequenceCaptureSink{}
	script := &dsl.Script{
		Name:     "phase-local incomplete source transaction",
		Protocol: "teardown-goal-denial-test",
		Config: dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{
			Iterations:      1,
			MaxTransactions: 1,
		}},
		Init: []dsl.Action{
			sourceTransactionAction("lr_start_transaction", ""),
			{
				Name:     "stream transaction",
				Type:     "request",
				Target:   "/stream",
				Metadata: map[string]any{"emits_business_transactions": true},
			},
			sourceTransactionAction("lr_end_transaction", "LR_PASS"),
		},
		Actions: []dsl.Action{{Name: "main", Type: "request", Target: "/main"}},
	}
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups: []GroupConfig{{
			Name: "at-once", Script: script, VUsers: 1, InitStrategy: "at_once", ResultSink: sink,
		}},
		Duration: time.Second,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}
	if _, err := mge.Run(context.Background()); err == nil {
		t.Fatal("at-once init unexpectedly succeeded after its nested transaction claim was denied")
	}
	if len(sink.transactions) != 1 {
		t.Fatalf("transactions = %#v, want one finalized phase-local source transaction", sink.transactions)
	}
	if sink.transactions[0].Success || sink.transactions[0].ErrorMessage == "" {
		t.Fatalf("finalized source transaction = %#v, want failed incomplete evidence", sink.transactions[0])
	}
}

func TestMultiGroupExactGoalCancelsUnentitledPeerPacing(t *testing.T) {
	state := installGracefulBoundaryState(t)
	peerScript := &dsl.Script{
		Name: "unentitled-peer", Protocol: "graceful-boundary-test",
		Config: dsl.ScriptConfig{Pacing: dsl.PacingConfig{
			Type: "fixed_delay", DelayMs: 3000,
		}},
		Actions: []dsl.Action{{Name: "peer", Type: "request", Target: "/peer"}},
	}
	goalScript := &dsl.Script{
		Name: "goal-owner", Protocol: "graceful-boundary-test",
		Config: dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{
			Iterations:      1,
			MaxTransactions: 1,
		}},
		Init:    []dsl.Action{{Name: "first", Type: "request", Target: "/gate"}},
		Actions: []dsl.Action{{Name: "bounded", Type: "request", Target: "/bounded"}},
	}
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups: []GroupConfig{
			{Name: "peer", Script: peerScript, VUsers: 1},
			{Name: "goal", Script: goalScript, VUsers: 1},
		},
		Duration: 5 * time.Second,
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
	case <-state.firstStarted:
	case <-time.After(time.Second):
		t.Fatal("goal owner did not enter its init gate")
	}
	peerRan := false
	deadline := time.After(time.Second)
	for !peerRan {
		select {
		case name := <-state.actions:
			peerRan = name == "peer"
		case <-deadline:
			t.Fatal("unbounded peer did not complete its first iteration")
		}
	}
	// Let the peer enter its long second-iteration pacing wait.
	time.Sleep(20 * time.Millisecond)
	releasedAt := time.Now()
	close(state.releaseFirst)
	select {
	case runErr := <-done:
		if runErr != nil {
			t.Fatalf("Run: %v", runErr)
		}
	case <-time.After(time.Second):
		t.Fatal("exact goal waited through the unentitled peer's pacing delay")
	}
	if elapsed := time.Since(releasedAt); elapsed >= time.Second {
		t.Fatalf("goal stop elapsed = %s, want prompt peer pacing cancellation", elapsed)
	}
}

func TestMultiGroupRejectsScaleDownWhileFinalExactGoalActionDrains(t *testing.T) {
	state := installGracefulBoundaryState(t)
	var allowGoalOnce sync.Once
	allowGoal := func() { allowGoalOnce.Do(func() { close(state.allowFinalGoal) }) }
	defer allowGoal()
	var releaseActionOnce sync.Once
	releaseAction := func() { releaseActionOnce.Do(func() { close(state.releaseFirst) }) }
	defer releaseAction()

	script := &dsl.Script{
		Name: "blocking-final-goal", Protocol: "blocking-final-goal-test",
		Config: dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{
			Iterations:      1,
			MaxTransactions: 1,
		}},
		Actions: []dsl.Action{{
			Name:     "final goal action",
			Type:     "java_vuser_run",
			Metadata: map[string]any{"emits_business_transactions": true},
		}},
	}
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups:   []GroupConfig{{Name: "goal", Script: script, VUsers: 1}},
		Duration: 10 * time.Second,
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
	case <-state.firstStarted:
	case <-time.After(time.Second):
		t.Fatal("final exact-goal action did not start")
	}

	deadline := time.Now().Add(time.Second)
	for !mge.ScalingReady() {
		select {
		case runErr := <-done:
			t.Fatalf("Run finished before scaling admission opened: %v", runErr)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("scaling admission did not open after the initial ramp")
		}
		time.Sleep(time.Millisecond)
	}

	// Let the streaming adapter emit the final reserved transaction. The goal
	// cancellation closes loop/launch admission synchronously, but the action is
	// still entitled to drain on its goal-immune per-VUser context.
	allowGoal()
	select {
	case <-state.finalGoalReached:
	case <-time.After(time.Second):
		t.Fatal("final transaction did not satisfy the exact goal")
	}
	if mge.ScalingReady() {
		t.Fatal("ScalingReady remained true after the exact goal closed launch admission")
	}
	if err := mge.ScaleVUsers("goal", 0); err == nil || !strings.Contains(err.Error(), "engine not running") {
		t.Fatalf("ScaleVUsers after exact goal = %v, want engine not running", err)
	}
	if state.actionCanceled.Load() {
		t.Fatal("rejected scale-down canceled the entitled final action")
	}
	select {
	case runErr := <-done:
		t.Fatalf("Run finished before the entitled final action drained: %v", runErr)
	case <-time.After(50 * time.Millisecond):
	}

	releaseAction()
	select {
	case runErr := <-done:
		if runErr != nil {
			t.Fatalf("Run: %v", runErr)
		}
	case <-time.After(time.Second):
		t.Fatal("run did not finish after the entitled final action drained")
	}
	if state.actionCanceled.Load() {
		t.Fatal("entitled final action observed cancellation")
	}
}

func TestGracefulStopSkipsPostBaselineButNaturalCompletionRetainsIt(t *testing.T) {
	t.Run("explicit graceful stop", func(t *testing.T) {
		state := installGracefulBoundaryState(t)
		close(state.releaseFirst)
		script := &dsl.Script{
			Name: "post-baseline-stop", Protocol: "graceful-boundary-test",
			Config:  dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1}},
			Actions: []dsl.Action{{Name: "first", Type: "request", Target: "/first"}},
		}
		mge, err := NewMultiGroup(MultiGroupConfig{Groups: []GroupConfig{{Name: "group", Script: script, VUsers: 1}}, Duration: time.Second, PostBaseline: 5 * time.Second})
		if err != nil {
			t.Fatalf("NewMultiGroup: %v", err)
		}
		done := make(chan error, 1)
		go func() {
			_, runErr := mge.Run(context.Background())
			done <- runErr
		}()
		select {
		case <-state.firstReturned:
		case <-time.After(time.Second):
			t.Fatal("action did not finish")
		}
		deadline := time.Now().Add(time.Second)
		for {
			if mge.activeVUserCountByGroup()["group"] == 0 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("VUser did not finish before post-baseline")
			}
			time.Sleep(time.Millisecond)
		}
		mge.GracefulStop()
		select {
		case runErr := <-done:
			if runErr != nil {
				t.Fatalf("Run: %v", runErr)
			}
		case <-time.After(time.Second):
			t.Fatal("explicit graceful stop waited for post-baseline")
		}
	})

	t.Run("natural completion", func(t *testing.T) {
		mge, err := NewMultiGroup(MultiGroupConfig{
			Groups: []GroupConfig{{
				Name: "group",
				Script: &dsl.Script{
					Name: "natural-post-baseline", Protocol: "stub",
					Config:  dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1}},
					Actions: []dsl.Action{{Name: "request", Type: "request", Target: "/"}},
				},
				VUsers: 1,
			}},
			Duration: time.Second, PostBaseline: 80 * time.Millisecond,
		})
		if err != nil {
			t.Fatalf("NewMultiGroup: %v", err)
		}
		started := time.Now()
		if _, err := mge.Run(context.Background()); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if elapsed := time.Since(started); elapsed < 60*time.Millisecond {
			t.Fatalf("natural completion skipped post-baseline: %s", elapsed)
		}
	})
}

func TestPauseBeforeRunIsLatched(t *testing.T) {
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups: []GroupConfig{{
			Name:   "group",
			Script: &dsl.Script{Name: "script", Protocol: "stub", Actions: []dsl.Action{{Name: "request", Type: "request", Target: "/"}}},
			VUsers: 1,
		}},
		Duration: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}
	if err := mge.Pause(); err != nil {
		t.Fatalf("Pause before Run: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, runErr := mge.Run(context.Background())
		done <- runErr
	}()
	select {
	case <-mge.Ready():
	case <-time.After(time.Second):
		t.Fatal("engine did not become ready")
	}
	select {
	case err := <-done:
		t.Fatalf("paused engine finished before resume: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := mge.Resume(); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("resumed engine did not finish")
	}
}

func TestPauseResumeOpenWorkloadWithoutClosedModelRateLimiter(t *testing.T) {
	script := &dsl.Script{
		Name:     "open-workload",
		Protocol: "stub",
		Config: dsl.ScriptConfig{
			RunLogic: dsl.RunLogicConfig{WorkloadModel: "open"},
			Pacing:   dsl.PacingConfig{Type: "tps", TPS: 10},
		},
		Actions: []dsl.Action{{Name: "request", Type: "request", Target: "/"}},
	}
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups:   []GroupConfig{{Name: "group", Script: script, VUsers: 1}},
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
	case <-mge.Ready():
	case <-time.After(time.Second):
		cancel()
		t.Fatal("engine did not become ready")
	}

	// Open workloads have no per-group TokenBucket. Pause and Resume must use
	// the shared execution gate without dereferencing that nil limiter.
	if err := mge.Pause(); err != nil {
		cancel()
		t.Fatalf("Pause: %v", err)
	}
	if err := mge.Resume(); err != nil {
		cancel()
		t.Fatalf("Resume: %v", err)
	}
	cancel()
	select {
	case runErr := <-done:
		if runErr != nil {
			t.Fatalf("Run: %v", runErr)
		}
	case <-time.After(time.Second):
		t.Fatal("engine did not stop")
	}
}

func TestScaleVUsersFailsPromptlyBeforeReady(t *testing.T) {
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups: []GroupConfig{{
			Name:   "group",
			Script: &dsl.Script{Name: "script", Protocol: "stub", Actions: []dsl.Action{{Name: "request", Type: "request", Target: "/"}}},
			VUsers: 1,
		}},
		Duration:    50 * time.Millisecond,
		PreBaseline: 150 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, runErr := mge.Run(context.Background())
		done <- runErr
	}()

	deadline := time.Now().Add(time.Second)
	for {
		mge.mu.Lock()
		initialized := mge.baseCtx != nil
		mge.mu.Unlock()
		if initialized {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("engine did not initialize")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case <-mge.Ready():
		t.Fatal("engine unexpectedly ready during pre-baseline")
	default:
	}
	started := time.Now()
	if err := mge.ScaleVUsers("group", 2); err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("ScaleVUsers before Ready error = %v, want engine not running", err)
	}
	if elapsed := time.Since(started); elapsed > 50*time.Millisecond {
		t.Fatalf("ScaleVUsers before Ready took %s, want prompt rejection", elapsed)
	}
	if got := mge.GetGroupVUsers()["group"]; got != 1 {
		t.Fatalf("group target changed before Ready: got %d, want 1", got)
	}

	select {
	case runErr := <-done:
		if runErr != nil {
			t.Fatalf("Run: %v", runErr)
		}
	case <-time.After(time.Second):
		t.Fatal("engine did not finish")
	}
}

func TestSleepActiveFreezesWhilePaused(t *testing.T) {
	baseCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	openGate := make(chan struct{})
	close(openGate)
	mge := &MultiGroupEngine{
		baseCtx:            baseCtx,
		controlsReady:      true,
		pauseCh:            openGate,
		pauseChanged:       make(chan struct{}),
		runStartedAt:       time.Now(),
		baseTargetDuration: time.Second,
		targetDuration:     time.Second,
	}

	done := make(chan struct{})
	go func() {
		mge.SleepActive(baseCtx, 80*time.Millisecond)
		close(done)
	}()
	time.Sleep(20 * time.Millisecond)
	if err := mge.Pause(); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	select {
	case <-done:
		t.Fatal("active-time sleep expired during pause")
	case <-time.After(100 * time.Millisecond):
	}
	if err := mge.Resume(); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	select {
	case <-done:
	case <-time.After(150 * time.Millisecond):
		t.Fatal("active-time sleep did not finish after resume")
	}
}

func TestSleepActiveCanceledAtTimerBoundaryReturnsPromptly(t *testing.T) {
	ready := make(chan struct{})
	close(ready)
	mge := &MultiGroupEngine{
		baseCtx:            context.Background(),
		controlsReady:      true,
		pauseCh:            ready,
		pauseChanged:       make(chan struct{}),
		runStartedAt:       time.Now(),
		baseTargetDuration: time.Second,
		targetDuration:     time.Second,
	}
	// A pre-cancelled context and an effectively-expired timer make both select
	// cases eligible. Repeating exercises the cancellation branch that used to
	// Stop and then drain the synchronous Go 1.23+ timer channel indefinitely.
	for i := 0; i < 256; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		done := make(chan struct{})
		go func() {
			mge.SleepActive(ctx, time.Nanosecond)
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(250 * time.Millisecond):
			t.Fatalf("SleepActive deadlocked at timer/cancellation boundary on iteration %d", i)
		}
	}
}

func TestScaleVUsersRollsBackWhenProtocolInitFails(t *testing.T) {
	rejectScaleInit.Store(false)
	defer rejectScaleInit.Store(false)
	successfulScaleInits.Store(0)
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups: []GroupConfig{{
			Name: "group",
			Script: &dsl.Script{
				Name:     "script",
				Protocol: "scale-init-failure-test",
				Actions:  []dsl.Action{{Name: "request", Type: "request", Target: "/"}},
			},
			VUsers: 1,
		}},
		Duration: 5 * time.Second,
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
	waitForActiveVUsers(t, mge, 1)
	deadline := time.Now().Add(time.Second)
	for successfulScaleInits.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if successfulScaleInits.Load() == 0 {
		t.Fatal("initial vuser protocol did not finish initialization")
	}

	rejectScaleInit.Store(true)
	if err := scaleAfterInitialRamp(t, mge, "group", 2); err == nil || !strings.Contains(err.Error(), "injected init failure") {
		t.Fatalf("ScaleVUsers error = %v, want injected init failure", err)
	}
	rejectScaleInit.Store(false)
	if got := mge.GetGroupVUsers()["group"]; got != 1 {
		t.Fatalf("group target after failed scale = %d, want 1", got)
	}
	waitForActiveVUsers(t, mge, 1)
	cancel()
	select {
	case runErr := <-done:
		if runErr != nil {
			t.Fatalf("Run: %v", runErr)
		}
	case <-time.After(time.Second):
		t.Fatal("engine did not stop")
	}
}

func TestMultiGroupRunFailsWhenConfiguredVUserCannotInitialize(t *testing.T) {
	rejectScaleInit.Store(true)
	defer rejectScaleInit.Store(false)
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups: []GroupConfig{{
			Name:   "group",
			Script: &dsl.Script{Name: "script", Protocol: "scale-init-failure-test", Actions: []dsl.Action{{Name: "request", Type: "request", Target: "/"}}},
			VUsers: 1,
		}},
		Duration: time.Second,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}
	result, runErr := mge.Run(context.Background())
	if runErr == nil || !strings.Contains(runErr.Error(), "injected init failure") {
		t.Fatalf("Run error = %v, want init failure", runErr)
	}
	if result != nil {
		t.Fatalf("Run result = %#v, want nil on initialization failure", result)
	}
	if got := mge.activeVUserCountByGroup()["group"]; got != 0 {
		t.Fatalf("active vusers after failed startup = %d, want 0", got)
	}
}

func failingOpenWorkloadScript() *dsl.Script {
	return &dsl.Script{
		Name:     "failing-open",
		Protocol: "scale-init-failure-test",
		Config: dsl.ScriptConfig{
			RunLogic: dsl.RunLogicConfig{WorkloadModel: "open", Iterations: 1},
			Pacing:   dsl.PacingConfig{Type: "tps", TPS: 100},
		},
		Actions: []dsl.Action{{Name: "request", Type: "request", Target: "/"}},
	}
}

func TestMultiGroupOpenWorkloadFailsWhenNoArrivalWorkerInitializes(t *testing.T) {
	rejectScaleInit.Store(true)
	defer rejectScaleInit.Store(false)
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups:   []GroupConfig{{Name: "open", Script: failingOpenWorkloadScript(), VUsers: 2}},
		Duration: time.Second,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}
	result, runErr := mge.Run(context.Background())
	if runErr == nil || !strings.Contains(runErr.Error(), "could not initialize any arrival worker") {
		t.Fatalf("Run error = %v, want open worker initialization failure", runErr)
	}
	if result != nil {
		t.Fatalf("Run result = %#v, want nil", result)
	}
}

func TestLegacyOpenWorkloadFailsWhenNoArrivalWorkerInitializes(t *testing.T) {
	rejectScaleInit.Store(true)
	defer rejectScaleInit.Store(false)
	eng, err := New(Config{Script: failingOpenWorkloadScript(), VUsers: 2, Duration: time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	result, runErr := eng.Run(context.Background())
	if runErr == nil || !strings.Contains(runErr.Error(), "could not initialize any arrival worker") {
		t.Fatalf("Run error = %v, want open worker initialization failure", runErr)
	}
	if result != nil {
		t.Fatalf("Run result = %#v, want nil", result)
	}
}

func TestLegacyClosedWorkloadFailsWhenConfiguredVUserCannotInitialize(t *testing.T) {
	rejectScaleInit.Store(true)
	defer rejectScaleInit.Store(false)
	script := &dsl.Script{
		Name:     "failing-closed",
		Protocol: "scale-init-failure-test",
		Actions:  []dsl.Action{{Name: "request", Type: "request", Target: "/"}},
	}
	eng, err := New(Config{Script: script, VUsers: 2, Duration: time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	result, runErr := eng.Run(context.Background())
	if runErr == nil || !strings.Contains(runErr.Error(), "injected init failure") {
		t.Fatalf("Run error = %v, want configured vuser initialization failure", runErr)
	}
	if result != nil {
		t.Fatalf("Run result = %#v, want nil", result)
	}
}

func TestVUserIDPoolReusesReleasedIDsAndStaysBounded(t *testing.T) {
	mge := &MultiGroupEngine{
		vuserIDSpan:   3,
		vuserIDsInUse: make(map[int]struct{}),
	}
	for want := 0; want < 3; want++ {
		got, err := mge.allocateVUserID()
		if err != nil || got != want {
			t.Fatalf("allocation %d = (%d, %v), want (%d, nil)", want, got, err, want)
		}
	}
	if _, err := mge.allocateVUserID(); err == nil || !strings.Contains(err.Error(), "namespace exhausted") {
		t.Fatalf("exhausted allocation error = %v", err)
	}

	mge.mu.Lock()
	mge.releaseVUserIDLocked(1)
	mge.mu.Unlock()
	got, err := mge.allocateVUserID()
	if err != nil || got != 1 {
		t.Fatalf("allocation after release = (%d, %v), want (1, nil)", got, err)
	}
}

func TestGroupVUserIDPoolsAreIndependentAndReusable(t *testing.T) {
	mge := &MultiGroupEngine{
		groups: []GroupConfig{
			{Name: "buyers", VUserIDSpan: 3},
			{Name: "sellers", VUserIDSpan: 2},
		},
		vuserIDSpan: 10,
	}
	mge.mu.Lock()
	mge.resetVUserIDStateLocked()
	mge.mu.Unlock()

	allocated := make(map[string][]vuserIdentity)
	for group, count := range map[string]int{"buyers": 3, "sellers": 2} {
		for wantLocal := 0; wantLocal < count; wantLocal++ {
			identity, err := mge.allocateVUserIdentity(group)
			if err != nil || identity.local != wantLocal {
				t.Fatalf("%s allocation %d = (%#v, %v), want local %d", group, wantLocal, identity, err, wantLocal)
			}
			allocated[group] = append(allocated[group], identity)
		}
		if _, err := mge.allocateVUserIdentity(group); err == nil || !strings.Contains(err.Error(), "namespace exhausted") {
			t.Fatalf("%s exhausted allocation error = %v", group, err)
		}
	}

	released := allocated["buyers"][1]
	mge.mu.Lock()
	mge.releaseVUserIDLocked(released.handle)
	mge.mu.Unlock()
	reused, err := mge.allocateVUserIdentity("buyers")
	if err != nil || reused.local != released.local {
		t.Fatalf("buyers allocation after release = (%#v, %v), want local %d", reused, err, released.local)
	}
}
