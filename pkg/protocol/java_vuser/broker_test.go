package java_vuser

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/javasidecar"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// Isolate result rendering from transport (real mTLS transport/identity tests
// live with javasidecar.DedicatedBrokerSession).
type brokerAdapterSession struct {
	calls, closes atomic.Int32
	invoke        func(context.Context, string, map[string]string) (javasidecar.ExecutionResponse, error)
}

func (*brokerAdapterSession) Err() error                    { return nil }
func (*brokerAdapterSession) HostID() string                { return "host" }
func (*brokerAdapterSession) SessionID() string             { return "session" }
func (s *brokerAdapterSession) Close(context.Context) error { s.closes.Add(1); return nil }
func (s *brokerAdapterSession) Execute(ctx context.Context, m string, p map[string]string) (javasidecar.ExecutionResponse, error) {
	s.calls.Add(1)
	if s.invoke != nil {
		return s.invoke(ctx, m, p)
	}
	return javasidecar.ExecutionResponse{Contract: javasidecar.ContractVersion, ExecutionID: "9", Success: true}, nil
}
func brokerAdapterFixture(t *testing.T, s *brokerAdapterSession) *DedicatedBrokerProtocol {
	t.Helper()
	p, e := newDedicatedBrokerProtocol(s, "Actions", time.Second)
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func TestDedicatedBrokerAdapterColdBarrierAndLocalValidation(t *testing.T) {
	s := &brokerAdapterSession{}
	p := brokerAdapterFixture(t, s)
	if e := p.Init(t.Context(), nil); e != nil {
		t.Fatal(e)
	}
	if e := p.HealthCheck(t.Context()); e != nil || s.calls.Load() != 0 {
		t.Fatal("cold method ran Java", e)
	}
	badTarget := pooledAction("init")
	badTarget.Target = "Other"
	badTimeout := pooledAction("init")
	badTimeout.Params["timeout_ms"] = -1
	for _, a := range []*protocol.Action{nil, badTarget, badTimeout, pooledAction("full_lifecycle")} {
		r, e := p.Execute(t.Context(), a)
		if e != nil || r.Success || s.calls.Load() != 0 {
			t.Fatal("invalid invocation reached host", r, e)
		}
	}
	if r, e := p.Execute(t.Context(), pooledAction("init")); e != nil || !r.Success {
		t.Fatal(r, e)
	}
	if e := p.Teardown(t.Context()); e != nil {
		t.Fatal(e)
	}
	if r, _ := p.Execute(t.Context(), pooledAction("action")); r.Success || s.calls.Load() != 1 || s.closes.Load() != 1 {
		t.Fatal("retired session reused")
	}
}
func TestDedicatedBrokerAdapterTransactionsTimingAndExitStatus(t *testing.T) {
	text := adapterEvent(1, `"type":"transaction_start","name":"order","transaction_id":"session:9:tx","timestamp_ms":1000`) + adapterEvent(2, `"type":"transaction_end","name":"order","transaction_id":"session:9:tx","timestamp_ms":1020,"duration_ms":17,"raw_duration_ms":20,"think_time_ms":3,"message":"0"`) + adapterEvent(3, `"type":"runtime","name":"exit","message":"2 1"`)
	s := &brokerAdapterSession{invoke: func(context.Context, string, map[string]string) (javasidecar.ExecutionResponse, error) {
		return javasidecar.ExecutionResponse{Contract: javasidecar.ContractVersion, ExecutionID: "9", Success: true, Events: text, DurationMS: 22}, nil
	}}
	p := brokerAdapterFixture(t, s)
	var children []*protocol.Result
	r, e := p.ExecuteStream(t.Context(), pooledAction("action"), func(child *protocol.Result) error { children = append(children, child); return nil })
	if e != nil || !r.Success || r.CorrelationID != "9" || len(children) != 2 {
		t.Fatal(r, children, e)
	}
	if children[0].Duration != 17*time.Millisecond || children[0].CorrelationID != "session:9:tx" || !children[0].BusinessTransaction {
		t.Fatal("transaction timing changed", children[0])
	}
	if children[1].Success || children[1].ProtocolEvidence["exit_status"] != 1 {
		t.Fatal("exit status lost", children[1])
	}
	for _, sample := range append(children, r) {
		if sample.ProtocolEvidence["runner_mode"] != "dedicated" || sample.ProtocolEvidence["runner_host_id"] != "host" {
			t.Fatal("host attribution lost")
		}
	}
}
func TestDedicatedBrokerAdapterTimeoutAndTerminalFailure(t *testing.T) {
	s := &brokerAdapterSession{invoke: func(ctx context.Context, _ string, params map[string]string) (javasidecar.ExecutionResponse, error) {
		if params["value"] != "override" {
			t.Error("parameter precedence lost")
		}
		<-ctx.Done()
		return javasidecar.ExecutionResponse{}, errors.New("private transport information")
	}}
	p := brokerAdapterFixture(t, s)
	a := pooledAction("action")
	a.Params["timeout_ms"] = 5
	a.Params["parameters"] = map[string]string{"value": "base"}
	a.Params["lr_parameters"] = map[string]string{"value": "override"}
	r, e := p.Execute(t.Context(), a)
	if e != nil || r.Success || strings.Contains(r.ErrorMessage, "private") {
		t.Fatal("unsafe error", r, e)
	}
	if r, _ := p.Execute(t.Context(), a); r.Success || s.calls.Load() != 1 {
		t.Fatal("uncertain invocation replayed")
	}
}
