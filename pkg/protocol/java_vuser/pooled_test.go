package java_vuser

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/javasidecar"
	"github.com/jasonczech/apexload/pkg/protocol"
)

type adapterSlot struct {
	calls, closes atomic.Int32
	invoke        func(context.Context, string, map[string]string, time.Duration) (*javasidecar.PooledInvocationResult, error)
	close         func(context.Context) error
}

func (*adapterSlot) TargetClass() string { return "Actions" }
func (*adapterSlot) HostID() string      { return "host" }
func (*adapterSlot) SlotID() string      { return "slot" }
func (*adapterSlot) Err() error          { return nil }
func (s *adapterSlot) Close(ctx context.Context) error {
	s.closes.Add(1)
	if s.close != nil {
		return s.close(ctx)
	}
	return nil
}
func (s *adapterSlot) Invoke(ctx context.Context, m string, p map[string]string, d time.Duration) (*javasidecar.PooledInvocationResult, error) {
	s.calls.Add(1)
	if s.invoke != nil {
		return s.invoke(ctx, m, p, d)
	}
	return &javasidecar.PooledInvocationResult{Sequence: 1, EventRequestID: "9", Method: m}, nil
}
func pooledAction(method string) *protocol.Action {
	return &protocol.Action{Name: "Actions." + method, Type: "java_vuser_run", Target: "Actions", Params: map[string]any{"method": method}}
}
func adapterEvent(seq int, extra string) string {
	return fmt.Sprintf(`{"contract":"apexload.java_vuser.lrapi_events.v2","request_id":"9","sequence":%d,%s}`+"\n", seq, extra)
}
func adapterFixture(t *testing.T, s *adapterSlot) *PooledSlotProtocol {
	t.Helper()
	p, err := newPooledSlotProtocol(s, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPooledAdapterColdMethodsAndInputRejection(t *testing.T) {
	s := &adapterSlot{}
	p := adapterFixture(t, s)
	if err := p.Init(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if err := p.HealthCheck(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.calls.Load() != 0 {
		t.Fatal("cold operation invoked user code")
	}
	wrong := pooledAction("init")
	wrong.Target = "Other"
	timeout := pooledAction("init")
	timeout.Params["timeout_ms"] = -1
	params := pooledAction("init")
	params.Params["lr_parameters"] = map[string]any{"secret": 123}
	for _, a := range []*protocol.Action{nil, wrong, timeout, params, pooledAction("full_lifecycle"), pooledAction("vuser_init"), pooledAction("Action")} {
		r, err := p.Execute(context.Background(), a)
		if err != nil || r.Success {
			t.Fatal("invalid action accepted", r, err)
		}
	}
	if s.calls.Load() != 0 {
		t.Fatal("invalid input reached slot")
	}
	if r, err := p.Execute(context.Background(), pooledAction("init")); err != nil || !r.Success {
		t.Fatal(r, err)
	}
	if err := p.Teardown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.closes.Load() != 1 || s.calls.Load() != 1 {
		t.Fatal("teardown ran user lifecycle")
	}
	if r, _ := p.Execute(context.Background(), pooledAction("action")); r.Success || s.calls.Load() != 1 {
		t.Fatal("closed adapter reused")
	}
}

func TestPooledAdapterParametersAndBoundedErrors(t *testing.T) {
	s := &adapterSlot{invoke: func(_ context.Context, m string, p map[string]string, d time.Duration) (*javasidecar.PooledInvocationResult, error) {
		if p["same"] != "override" || p["base"] != "yes" || len(p) != 2 || d != 250*time.Millisecond {
			t.Fatal("bad parameters or timeout", p, d)
		}
		return nil, errors.New("must-not-disclose secret data")
	}}
	p := adapterFixture(t, s)
	a := pooledAction("init")
	a.Params["parameters"] = map[string]string{"base": "yes", "same": "base"}
	a.Params["lr_parameters"] = map[string]any{"same": "override"}
	a.Params["timeout_ms"] = 250
	r, err := p.Execute(context.Background(), a)
	if err != nil || r.Success || strings.Contains(r.ErrorMessage, "secret") {
		t.Fatal("unbounded error", r, err)
	}
	_, _ = p.Execute(context.Background(), a)
	if s.calls.Load() != 1 {
		t.Fatal("terminal failure replayed")
	}
	for _, values := range []map[string]any{
		{"parameters": false}, {"parameters": map[string]string{"": "x"}}, {"lr_parameters": map[string]string{"x": strings.Repeat("x", 65537)}}, {"lr_parameters": map[string]string{"x": string([]byte{255})}},
	} {
		if _, err := pooledParameters(values); err == nil {
			t.Fatal("invalid parameters accepted")
		}
	}
}

func TestPooledAdapterStreamsAuthoritativeExitAndTimings(t *testing.T) {
	for _, option := range []int{1, 2} {
		t.Run(fmt.Sprint(option), func(t *testing.T) {
			text := adapterEvent(1, `"type":"transaction_start","name":"order","transaction_id":"9-tx-1","timestamp_ms":1000`) + adapterEvent(2, `"type":"transaction_end","name":"order","transaction_id":"9-tx-1","timestamp_ms":1020,"duration_ms":17,"raw_duration_ms":20,"think_time_ms":3,"message":"0"`) + adapterEvent(3, `"type":"runtime","name":"exit","message":"2 999"`)
			s := &adapterSlot{invoke: func(context.Context, string, map[string]string, time.Duration) (*javasidecar.PooledInvocationResult, error) {
				return &javasidecar.PooledInvocationResult{Sequence: 3, Method: "action", EventRequestID: "9", Events: text, Exit: &javasidecar.PooledExit{Option: option, Status: 1}}, nil
			}}
			p := adapterFixture(t, s)
			var children []*protocol.Result
			r, err := p.ExecuteStream(context.Background(), pooledAction("action"), func(r *protocol.Result) error { children = append(children, r); return nil })
			if err != nil || !r.Success || len(children) != 2 {
				t.Fatal(r, children, err)
			}
			if r.ProtocolEvidence["runner_exit_status"] != int32(1) || r.CorrelationID != "invoke-slot-3" {
				t.Fatal("lost authoritative status", r)
			}
			if r.LifecycleControl == nil || r.LifecycleControl.EndIteration != (option == 2) || !r.LifecycleControl.Failed {
				t.Fatal("wrong flow receipt", r.LifecycleControl)
			}
			if children[0].Duration != 17*time.Millisecond || children[0].CorrelationID != "slot:9:9-tx-1" || !children[0].BusinessTransaction {
				t.Fatal("timing/id changed", children[0])
			}
			if children[1].ActionName != "lr.exit" || children[1].Success || children[1].ProtocolEvidence["exit_status"] != 1 {
				t.Fatal("forged event exit used", children[1])
			}
			for _, child := range children {
				if child.ProtocolEvidence["runner_host_generation"] != "host" || child.ProtocolEvidence["runner_slot_id"] != "slot" {
					t.Fatal("missing host attribution")
				}
			}
		})
	}
}

func TestPooledAdapterTruncationAndEmitterFailure(t *testing.T) {
	s := &adapterSlot{invoke: func(context.Context, string, map[string]string, time.Duration) (*javasidecar.PooledInvocationResult, error) {
		return &javasidecar.PooledInvocationResult{Sequence: 1, Method: "action", EventRequestID: "9", EventsTruncated: true, Exit: &javasidecar.PooledExit{Option: 2, Status: 1}}, nil
	}}
	p := adapterFixture(t, s)
	var results []*protocol.Result
	_, err := p.ExecuteStream(context.Background(), pooledAction("action"), func(r *protocol.Result) error { results = append(results, r); return nil })
	if err != nil || len(results) != 2 || results[0].Success || results[1].Success {
		t.Fatal("lost exit/truncation failure", results, err)
	}
	sentinel := errors.New("consumer stopped")
	_, err = p.ExecuteStream(context.Background(), pooledAction("action"), func(*protocol.Result) error { return sentinel })
	if !errors.Is(err, sentinel) {
		t.Fatal("consumer error hidden", err)
	}
}

func TestPooledAdapterMalformedEventsFailClosed(t *testing.T) {
	for _, text := range []string{
		adapterEvent(1, `"type":"transaction_end","duration_ms":"invalid"`),
		adapterEvent(1, `"type":"transaction_end","duration_ms":-1`),
		adapterEvent(1, `"type":"message"`) + adapterEvent(1, `"type":"message"`),
		strings.ReplaceAll(adapterEvent(1, `"type":"message"`), `"request_id":"9"`, `"request_id":"10"`),
	} {
		s := &adapterSlot{invoke: func(context.Context, string, map[string]string, time.Duration) (*javasidecar.PooledInvocationResult, error) {
			return &javasidecar.PooledInvocationResult{Sequence: 1, Method: "action", EventRequestID: "9", Events: text}, nil
		}}
		p := adapterFixture(t, s)
		r, _ := p.Execute(context.Background(), pooledAction("action"))
		if r.Success || s.closes.Load() != 1 {
			t.Fatal("malformed evidence accepted", r)
		}
		_, _ = p.Execute(context.Background(), pooledAction("action"))
		if s.calls.Load() != 1 {
			t.Fatal("malformed invocation replayed")
		}
	}
}

func TestPooledAdapterTeardownBypassesInvocation(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	s := &adapterSlot{invoke: func(context.Context, string, map[string]string, time.Duration) (*javasidecar.PooledInvocationResult, error) {
		close(started)
		<-release
		return nil, &javasidecar.RemoteError{Code: "pooled_host_reaped"}
	}, close: func(context.Context) error { close(release); return nil }}
	p := adapterFixture(t, s)
	done := make(chan struct{})
	go func() { defer close(done); _, _ = p.Execute(context.Background(), pooledAction("init")) }()
	<-started
	if r, _ := p.Execute(context.Background(), pooledAction("init")); r.Success {
		t.Fatal("concurrent invocation accepted")
	}
	if err := p.Teardown(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("teardown queued behind invocation")
	}
}

func TestPooledProtocolEventIdentitiesAreSlotAndInvocationScoped(t *testing.T) {
	text := adapterEvent(1, `"type":"transaction_end","name":"child","transaction_id":"tx","parent_transaction_id":"parent","duration_ms":1,"message":"0"`)
	r := &javasidecar.PooledInvocationResult{Sequence: 1, Method: "action", EventRequestID: "9", Events: text}
	a, err := pooledProtocolEvents(r, "slot-a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := pooledProtocolEvents(r, "slot-b")
	if err != nil {
		t.Fatal(err)
	}
	r.EventRequestID = "10"
	r.Events = strings.ReplaceAll(text, `"request_id":"9"`, `"request_id":"10"`)
	c, err := pooledProtocolEvents(r, "slot-a")
	if err != nil {
		t.Fatal(err)
	}
	if a[0].TransactionID == b[0].TransactionID || a[0].TransactionID == c[0].TransactionID || a[0].ParentTransactionID == b[0].ParentTransactionID {
		t.Fatal("cross-slot or invocation collision")
	}
}

func TestPooledAdapterConstructionReturnCodeAndErrorClassification(t *testing.T) {
	if _, err := NewPooledSlotProtocol(nil, time.Second); err == nil {
		t.Fatal("nil slot accepted")
	}
	if _, err := NewPooledSlotProtocol(&javasidecar.PooledClientSlot{}, time.Second); err == nil {
		t.Fatal("unbound slot accepted")
	}
	if _, err := newPooledSlotProtocol(&adapterSlot{}, 0); err == nil {
		t.Fatal("zero timeout accepted")
	}
	for _, method := range []string{"init", "action"} {
		s := &adapterSlot{invoke: func(context.Context, string, map[string]string, time.Duration) (*javasidecar.PooledInvocationResult, error) {
			return &javasidecar.PooledInvocationResult{Sequence: 1, EventRequestID: "9", Method: method, ReturnCode: 7}, nil
		}}
		p := adapterFixture(t, s)
		r, _ := p.Execute(context.Background(), pooledAction(method))
		if r.Success || r.ProtocolEvidence["runner_return_code"] != int32(7) {
			t.Fatal("return code lost", r)
		}
		_, _ = p.Execute(context.Background(), pooledAction(method))
		if method == "init" && s.calls.Load() != 1 {
			t.Fatal("failed init repeated")
		}
	}
	for _, tc := range []struct {
		err  error
		code string
	}{
		{context.Canceled, "execution_canceled"}, {context.DeadlineExceeded, "execution_timeout"},
		{javasidecar.ErrPooledClientClosed, "pooled_client_closed"}, {javasidecar.ErrPooledClientDecision, "pooled_client_decision_changed"},
		{&javasidecar.RemoteError{Code: "pooled_host_reaped"}, "pooled_host_reaped"},
		{&javasidecar.RemoteError{Code: "secret-value"}, "pooled_execution_failed"},
	} {
		if code := pooledProtocolErrorCode(tc.err); code != tc.code {
			t.Fatal(code, tc.code)
		}
	}
	p := adapterFixture(t, &adapterSlot{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if r, _ := p.Execute(ctx, pooledAction("init")); !strings.Contains(r.ErrorMessage, "execution_canceled") {
		t.Fatal("cancellation code lost", r.ErrorMessage)
	}
}
