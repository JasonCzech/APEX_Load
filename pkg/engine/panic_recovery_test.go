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

type panicInitProtocol struct{}

var panicInitTeardownCalls atomic.Int64

func (*panicInitProtocol) Name() string { return "panic-init-stub" }
func (*panicInitProtocol) Init(context.Context, map[string]any) error {
	panic("boom: simulated init fault")
}
func (*panicInitProtocol) Execute(context.Context, *protocol.Action) (*protocol.Result, error) {
	return nil, nil
}
func (*panicInitProtocol) Teardown(context.Context) error {
	panicInitTeardownCalls.Add(1)
	return nil
}
func (*panicInitProtocol) HealthCheck(context.Context) error { return nil }

type panicHealthProtocol struct{}

var panicHealthExecuteCalls atomic.Int64

func (*panicHealthProtocol) Name() string                               { return "panic-health-stub" }
func (*panicHealthProtocol) Init(context.Context, map[string]any) error { return nil }
func (*panicHealthProtocol) Teardown(context.Context) error             { return nil }
func (*panicHealthProtocol) HealthCheck(context.Context) error          { panic("boom: simulated health fault") }
func (*panicHealthProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	panicHealthExecuteCalls.Add(1)
	return &protocol.Result{ActionName: action.Name, Success: true, Duration: time.Millisecond}, nil
}

func init() {
	protocol.Register("panic-stub", func() protocol.Protocol { return &panickingProtocol{} })
	protocol.Register("panic-teardown-stub", func() protocol.Protocol { return &panicTeardownProtocol{} })
	protocol.Register("panic-init-stub", func() protocol.Protocol { return &panicInitProtocol{} })
	protocol.Register("panic-health-stub", func() protocol.Protocol { return &panicHealthProtocol{} })
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

func TestRecoveredPanicLogTextScrubsAndBoundsAdapterValue(t *testing.T) {
	secret := "client_secret=panic-log-secret"
	value := secret + strings.Repeat("x", maxRecoveredPanicLogBytes*2)
	got := recoveredPanicLogText(value)
	if strings.Contains(got, "panic-log-secret") {
		t.Fatalf("panic log value retained secret: %q", got)
	}
	if len(got) > maxRecoveredPanicLogBytes {
		t.Fatalf("panic log value length = %d, want <= %d", len(got), maxRecoveredPanicLogBytes)
	}
}

func TestTeardownPanicRecoveryUsesExactRuntimeRedaction(t *testing.T) {
	const secret = "tiny-unpatterned-panic-value"
	proto := teardownPanicValueProtocol{value: secret}
	config := WithSecretValueRedaction(nil, []string{secret})
	err := callProtocolTeardown(context.Background(), proto, config)
	if err == nil || strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), redactedPayloadValue) {
		t.Fatalf("callProtocolTeardown error = %q, want exact-redacted panic", err)
	}
	if got := recoveredPanicLogTextWithConfig("panic "+secret, config); strings.Contains(got, secret) {
		t.Fatalf("recovered panic log exposed exact runtime value: %q", got)
	}
}

func TestRecoveredPanicLogScrubsLongRuntimeSecretBeforeTruncation(t *testing.T) {
	secret := strings.Repeat("q", maxRecoveredPanicLogBytes*2)
	config := WithSecretValueRedaction(nil, []string{secret})
	got := recoveredPanicLogTextWithConfig("panic: "+secret+":end", config)
	if strings.Contains(got, strings.Repeat("q", 256)) || !strings.Contains(got, redactedPayloadValue) {
		t.Fatalf("long panic secret survived as a truncated prefix: %q", got)
	}
	if len(got) > maxRecoveredPanicLogBytes {
		t.Fatalf("panic log value length = %d, want <= %d", len(got), maxRecoveredPanicLogBytes)
	}
}

func TestInitAndHealthCheckPanicRecoveryUsesExactRuntimeRedaction(t *testing.T) {
	const secret = "tiny-unpatterned-lifecycle-value"
	config := WithSecretValueRedaction(nil, []string{secret})
	initErr := callProtocolInit(context.Background(), lifecycleInitPanicValueProtocol{value: secret}, nil, config)
	if initErr == nil || strings.Contains(initErr.Error(), secret) || !strings.Contains(initErr.Error(), redactedPayloadValue) {
		t.Fatalf("callProtocolInit error = %q, want exact-redacted panic", initErr)
	}
	healthErr := callProtocolHealthCheck(context.Background(), lifecycleHealthPanicValueProtocol{value: secret}, config)
	if healthErr == nil || strings.Contains(healthErr.Error(), secret) || !strings.Contains(healthErr.Error(), redactedPayloadValue) {
		t.Fatalf("callProtocolHealthCheck error = %q, want exact-redacted panic", healthErr)
	}
}

type lifecycleInitPanicValueProtocol struct{ value string }

func (p lifecycleInitPanicValueProtocol) Init(context.Context, map[string]any) error { panic(p.value) }

type lifecycleHealthPanicValueProtocol struct{ value string }

func (p lifecycleHealthPanicValueProtocol) HealthCheck(context.Context) error { panic(p.value) }

type teardownPanicValueProtocol struct{ value string }

func (p teardownPanicValueProtocol) Teardown(context.Context) error { panic(p.value) }

func TestReplayAndShakeoutSurviveProtocolTeardownPanic(t *testing.T) {
	script := &dsl.Script{
		Name:     "panic teardown replay",
		Protocol: "panic-teardown-stub",
		Actions:  []dsl.Action{{Name: "ok", Type: "request", Target: "/"}},
	}
	if report, err := ReplayScript(context.Background(), script, ReplayOptions{IgnoreThinkTime: true}); err != nil || report == nil || !report.Success {
		t.Fatalf("ReplayScript = report %#v err %v", report, err)
	}
	if err := RunScriptShakeout(context.Background(), "shakeout", script); err != nil {
		t.Fatalf("RunScriptShakeout: %v", err)
	}
}

func TestReplayAndShakeoutContainProtocolInitPanicAndTeardownPartialState(t *testing.T) {
	before := panicInitTeardownCalls.Load()
	script := &dsl.Script{
		Name:     "panic init replay",
		Protocol: "panic-init-stub",
		Actions:  []dsl.Action{{Name: "never", Type: "request", Target: "/"}},
	}
	if report, err := ReplayScript(context.Background(), script, ReplayOptions{IgnoreThinkTime: true}); err == nil || report != nil || !strings.Contains(err.Error(), "protocol init panicked") {
		t.Fatalf("ReplayScript = report %#v err %v, want contained init panic", report, err)
	}
	if err := RunScriptShakeout(context.Background(), "shakeout", script); err == nil || !strings.Contains(err.Error(), "protocol init panicked") {
		t.Fatalf("RunScriptShakeout error = %v, want contained init panic", err)
	}
	if got := panicInitTeardownCalls.Load() - before; got != 2 {
		t.Fatalf("partial-init teardown calls = %d, want 2", got)
	}
}

func TestEngineRunContainsHealthCheckPanicAndContinues(t *testing.T) {
	before := panicHealthExecuteCalls.Load()
	script := &dsl.Script{
		Name:     "panic health run",
		Protocol: "panic-health-stub",
		Config:   dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1}},
		Actions:  []dsl.Action{{Name: "ok", Type: "request", Target: "/"}},
	}
	e, err := New(Config{Script: script, VUsers: 1, Duration: time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	result, err := e.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result == nil || panicHealthExecuteCalls.Load() <= before {
		t.Fatalf("health panic prevented execution: result=%#v execute_calls=%d", result, panicHealthExecuteCalls.Load()-before)
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
