package engine

import (
	"context"
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
