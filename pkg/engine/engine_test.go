package engine

import (
	"context"
	"testing"
	"time"

	"github.com/jasonczech/stormrunner/pkg/dsl"
	"github.com/jasonczech/stormrunner/pkg/protocol"
)

type stubProtocol struct {
	initCalled     bool
	executeCalled  int
	teardownCalled bool
	initConfig     map[string]any
}

type slowStubProtocol struct{}

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

func init() {
	protocol.Register("stub", func() protocol.Protocol {
		return &stubProtocol{}
	})
	protocol.Register("slow-stub", func() protocol.Protocol {
		return &slowStubProtocol{}
	})
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
