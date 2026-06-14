package engine

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// blockingProtocol blocks in Execute until its context is cancelled, recording
// whether it observed the cancellation. It lets a test prove that the iteration
// body honors the per-VUser "shed" context (HardStopCtx).
type blockingProtocol struct {
	started   chan struct{}
	startOnce sync.Once
	sawCancel atomic.Bool
}

func (p *blockingProtocol) Name() string                                   { return "http" }
func (p *blockingProtocol) Init(_ context.Context, _ map[string]any) error { return nil }
func (p *blockingProtocol) Execute(ctx context.Context, action *protocol.Action) (*protocol.Result, error) {
	p.startOnce.Do(func() { close(p.started) })
	<-ctx.Done()
	p.sawCancel.Store(ctx.Err() != nil)
	return &protocol.Result{ActionName: action.Name, Success: false, Duration: time.Millisecond}, nil
}
func (p *blockingProtocol) Teardown(_ context.Context) error    { return nil }
func (p *blockingProtocol) HealthCheck(_ context.Context) error { return nil }

func TestSleepCtxInterruptedPromptly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	sleepCtx(ctx, 5*time.Second)
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("sleepCtx did not return promptly on a cancelled context: %s", elapsed)
	}

	start = time.Now()
	sleepCtx(context.Background(), 40*time.Millisecond)
	if elapsed := time.Since(start); elapsed < 30*time.Millisecond {
		t.Fatalf("sleepCtx returned too early for an uncancelled context: %s", elapsed)
	}
}

// TestVUserShedContextInterruptsInFlightIteration is the regression for the
// scale-down load-shedding fix: cancelling the per-VUser shed context
// (HardStopCtx) must interrupt an in-flight action, not wait for it to finish
// on its own.
func TestVUserShedContextInterruptsInFlightIteration(t *testing.T) {
	proto := &blockingProtocol{started: make(chan struct{})}
	shedCtx, shedCancel := context.WithCancel(context.Background())
	defer shedCancel()

	vu := &VUser{
		ID:    1,
		Group: "Checkout",
		Proto: proto,
		Script: &dsl.Script{
			Protocol: "http",
			Config:   dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1}},
			Actions:  []dsl.Action{{Name: "blocking", Type: "request", Target: "/"}},
		},
		Metrics:     NewMetricsCollector(16),
		HardStopCtx: shedCtx,
	}

	done := make(chan struct{})
	go func() {
		// The loop context is never cancelled; only the shed context is. This
		// isolates the fix: the iteration body must observe the shed context.
		vu.Run(context.Background())
		close(done)
	}()

	select {
	case <-proto.started:
	case <-time.After(2 * time.Second):
		t.Fatal("action never started")
	}

	shedCancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("VUser.Run did not return after shed cancel — in-flight iteration was not interrupted")
	}

	if !proto.sawCancel.Load() {
		t.Fatal("in-flight action did not observe cancellation — iteration body ignored the shed context")
	}
}
