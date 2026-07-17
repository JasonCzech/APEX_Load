package engine

import (
	"context"
	"errors"
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
