package java_vuser

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jasonczech/apexload/pkg/javarunner"
	"github.com/jasonczech/apexload/pkg/javasidecar"
	"github.com/jasonczech/apexload/pkg/protocol"
)

type dedicatedBrokerSession interface {
	Execute(context.Context, string, map[string]string) (javasidecar.ExecutionResponse, error)
	Close(context.Context) error
	HostID() string
	SessionID() string
	Err() error
}

// DedicatedBrokerProtocol connects an explicitly staged dedicated pod to the
// engine's existing result/transaction/control pipeline. Trusted admission must
// bind target to the frozen broker profile before construction. This is not an
// automatically selectable script backend; it never claims or renews leases.
type DedicatedBrokerProtocol struct {
	session                                                     dedicatedBrokerSession
	reservationID, profileID, policySHA256, configurationSHA256 string
	target                                                      string
	timeout                                                     time.Duration
	invokeMu                                                    sync.Mutex
	terminal                                                    atomic.Bool
}

var _ protocol.StreamingProtocol = (*DedicatedBrokerProtocol)(nil)
var _ protocol.Protocol = (*DedicatedBrokerProtocol)(nil)

func NewDedicatedBrokerProtocol(s *javasidecar.DedicatedBrokerSession, target string, timeout time.Duration) (*DedicatedBrokerProtocol, error) {
	if s == nil {
		return nil, errors.New("java_broker_session_unavailable")
	}
	return newDedicatedBrokerProtocol(s, target, timeout)
}

// NewBrokerFacadeProtocol binds an activated loopback session to the frozen target.
func NewBrokerFacadeProtocol(s *javasidecar.BrokerFacadeSession, target string, timeout time.Duration) (*DedicatedBrokerProtocol, error) {
	if s == nil {
		return nil, errors.New("java_broker_session_unavailable")
	}
	p, e := newDedicatedBrokerProtocol(s, target, timeout)
	if e != nil {
		return nil, e
	}
	profile := s.Profile()
	p.reservationID, p.profileID, p.policySHA256, p.configurationSHA256 = s.RunID(), profile.ID, profile.PolicySHA256, profile.ConfigurationSHA256
	return p, nil
}
func newDedicatedBrokerProtocol(s dedicatedBrokerSession, target string, timeout time.Duration) (*DedicatedBrokerProtocol, error) {
	if s == nil || s.HostID() == "" || s.SessionID() == "" || !javarunner.ValidPooledTargetClass(target) || timeout < time.Millisecond || timeout > javasidecar.MaximumExecutionTimeout {
		return nil, errors.New("java_broker_adapter_invalid")
	}
	return &DedicatedBrokerProtocol{session: s, target: target, timeout: timeout}, nil
}
func (*DedicatedBrokerProtocol) Name() string { return javaVuserProtocolName }
func (p *DedicatedBrokerProtocol) Init(ctx context.Context, _ map[string]any) error {
	return p.HealthCheck(ctx)
}
func (p *DedicatedBrokerProtocol) HealthCheck(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if p.terminal.Load() || p.session.Err() != nil {
		return errors.New("java_broker_session_unusable")
	}
	return nil
}
func (p *DedicatedBrokerProtocol) Teardown(ctx context.Context) error {
	p.terminal.Store(true)
	return p.session.Close(ctx)
}
func (p *DedicatedBrokerProtocol) Execute(ctx context.Context, a *protocol.Action) (*protocol.Result, error) {
	return p.ExecuteStream(ctx, a, nil)
}
func (p *DedicatedBrokerProtocol) ExecuteStream(ctx context.Context, a *protocol.Action, emit func(*protocol.Result) error) (*protocol.Result, error) {
	start := time.Now()
	result := &protocol.Result{StartTime: start, Success: true, StatusCode: 200, Extractions: map[string]string{}, DependencyType: javaVuserProtocolName}
	if a != nil {
		result.ActionName = a.Name
		result.DependencyTarget = strings.TrimSpace(a.Target)
	}
	fail := func(code string) (*protocol.Result, error) {
		finalizePersistentResult(result, start, a, runnerEnvelope{}, nil, 0, false, errors.New("java_vuser broker execution failed ("+code+")"))
		p.evidence(result)
		return result, nil
	}
	if !p.invokeMu.TryLock() {
		return fail("java_broker_session_busy")
	}
	defer p.invokeMu.Unlock()
	if e := p.HealthCheck(ctx); e != nil {
		return fail("java_broker_session_unusable")
	}
	if a == nil || a.Type != "java_vuser_run" || strings.TrimSpace(a.Target) != p.target {
		return fail("java_broker_action_invalid")
	}
	method := actionMethod(a)
	if method != "init" && method != "action" && method != "end" {
		return fail("java_broker_method_invalid")
	}
	timeout, e := actionTimeout(a, p.timeout)
	if e != nil || timeout < time.Millisecond || timeout > javasidecar.MaximumExecutionTimeout {
		return fail("java_broker_timeout_invalid")
	}
	params, e := pooledParameters(a.Params)
	if e != nil {
		return fail("java_broker_parameters_invalid")
	}
	call, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, e := p.session.Execute(call, method, params)
	if e != nil {
		p.terminal.Store(true)
		return fail("java_broker_session_unusable")
	}
	if method == "end" {
		p.terminal.Store(true)
	}
	// The facade already authenticated and translated the complete structured
	// capture. Parsing stderr again would duplicate or misattribute host-call IDs.
	events := filterV2RunnerEvents(parseRunnerEvents(out.Events), out.ExecutionID)
	finalizePersistentResult(result, start, a, runnerEnvelope{Contract: javaVuserIPCContract, Kind: "result", RequestID: out.ExecutionID, Success: out.Success, Error: boundedSidecarHarnessError(out.Error), DurationMS: out.DurationMS, Stdout: out.Stdout, Stderr: out.Stderr, StdoutTruncated: out.StdoutTruncated, StderrTruncated: out.StderrTruncated}, events, len(out.Events), out.EventsTruncated, nil)
	result.CorrelationID = out.ExecutionID
	p.evidence(result)
	if emit != nil {
		attributed := func(child *protocol.Result) error { p.evidence(child); return emit(child) }
		if e := emitRunnerTransactionsAt(a, events, result.StartTime.Add(result.Duration), out.EventsTruncated, attributed); e != nil {
			return result, e
		}
	}
	return result, nil
}
func (p *DedicatedBrokerProtocol) evidence(r *protocol.Result) {
	if r.ProtocolEvidence == nil {
		r.ProtocolEvidence = map[string]any{}
	}
	r.ProtocolEvidence["runner_backend"] = "broker"
	r.ProtocolEvidence["runner_mode"] = "dedicated"
	r.ProtocolEvidence["runner_containment"] = "execution_pod"
	r.ProtocolEvidence["runner_host_id"] = p.session.HostID()
	r.ProtocolEvidence["runner_session_id"] = p.session.SessionID()
	if p.reservationID != "" {
		r.ProtocolEvidence["runner_reservation_id"] = p.reservationID
		r.ProtocolEvidence["runner_profile_id"] = p.profileID
		r.ProtocolEvidence["runner_resource_profile"] = p.policySHA256
		r.ProtocolEvidence["runner_configuration_sha256"] = p.configurationSHA256
	}

}
