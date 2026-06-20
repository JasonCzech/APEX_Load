package engine

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// panickingProtocol panics on every Execute. It verifies that a faulty protocol
// adapter fails its action (via safeProtoExecute) instead of unwinding the
// goroutine and crashing the whole generator process.
type panickingProtocol struct{}

var panicExecuteCalls atomic.Int64

func (p *panickingProtocol) Name() string                                   { return "panic-stub" }
func (p *panickingProtocol) Init(_ context.Context, _ map[string]any) error { return nil }
func (p *panickingProtocol) Execute(_ context.Context, _ *protocol.Action) (*protocol.Result, error) {
	panicExecuteCalls.Add(1)
	panic("boom: simulated protocol adapter fault")
}
func (p *panickingProtocol) Teardown(_ context.Context) error    { return nil }
func (p *panickingProtocol) HealthCheck(_ context.Context) error { return nil }

// panicTeardownProtocol executes fine but panics on Teardown. It verifies the
// spawn-site backstop (recoverVUserGoroutine) contains panics that escape the
// per-action recovery — e.g. a panic in protocol Teardown.
type panicTeardownProtocol struct{}

func (p *panicTeardownProtocol) Name() string                                   { return "panic-teardown-stub" }
func (p *panicTeardownProtocol) Init(_ context.Context, _ map[string]any) error { return nil }
func (p *panicTeardownProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	return &protocol.Result{ActionName: action.Name, Success: true, Duration: time.Millisecond}, nil
}
func (p *panicTeardownProtocol) Teardown(_ context.Context) error {
	panic("boom: simulated teardown fault")
}
func (p *panicTeardownProtocol) HealthCheck(_ context.Context) error { return nil }

func init() {
	protocol.Register("panic-stub", func() protocol.Protocol { return &panickingProtocol{} })
	protocol.Register("panic-teardown-stub", func() protocol.Protocol { return &panicTeardownProtocol{} })
}

func TestSafeProtoExecute_RecoversPanic(t *testing.T) {
	vu := &VUser{
		Proto:  &panickingProtocol{},
		Script: &dsl.Script{Protocol: "panic-stub"},
	}

	result, err := vu.safeProtoExecute(context.Background(), &protocol.Action{Name: "boom"})
	if err == nil {
		t.Fatal("expected an error from a panicking protocol, got nil")
	}
	if result != nil {
		t.Fatalf("expected nil result on panic, got %+v", result)
	}
	if !strings.Contains(err.Error(), "panicked") {
		t.Errorf("error should describe the panic, got: %v", err)
	}
	if !strings.Contains(err.Error(), "panic-stub") {
		t.Errorf("error should name the protocol, got: %v", err)
	}
}

func TestEngine_Run_SurvivesProtocolExecutePanic(t *testing.T) {
	before := panicExecuteCalls.Load()
	script := &dsl.Script{
		Name:     "panic-run",
		Protocol: "panic-stub",
		Actions:  []dsl.Action{{Name: "boom", Type: "request", Target: "/"}},
	}

	e, err := New(Config{Script: script, VUsers: 4, Duration: 300 * time.Millisecond})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Without recovery, the panicking VUser goroutines would crash the test
	// binary; reaching the assertions below proves the panic was contained.
	if _, err := e.Run(context.Background()); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if panicExecuteCalls.Load() <= before {
		t.Fatal("expected the panicking protocol Execute to be invoked")
	}
}

func TestEngine_Run_SurvivesProtocolTeardownPanic(t *testing.T) {
	script := &dsl.Script{
		Name:     "panic-teardown-run",
		Protocol: "panic-teardown-stub",
		Actions:  []dsl.Action{{Name: "ok", Type: "request", Target: "/"}},
	}

	e, err := New(Config{Script: script, VUsers: 3, Duration: 200 * time.Millisecond})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// A panic in protocol Teardown escapes per-action recovery; the spawn-site
	// backstop must contain it so Run returns instead of crashing the process.
	result, err := e.Run(context.Background())
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.TotalTransactions == 0 {
		t.Error("expected successful transactions before the teardown panic")
	}
}
