package java_vuser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/javarunner"
	"github.com/jasonczech/apexload/pkg/javasidecar"
	"github.com/jasonczech/apexload/pkg/protocol"
)

type pooledSlot interface {
	Invoke(context.Context, string, map[string]string, time.Duration) (*javasidecar.PooledInvocationResult, error)
	Close(context.Context) error
	TargetClass() string
	HostID() string
	SlotID() string
	Mode() string
	Err() error
}

// PooledSlotProtocol adapts an explicitly prepared pilot slot to the engine's
// streaming protocol. It is not registered as a selectable script protocol.
// The caller must own staging, commit, leases, and the frozen qualification.
// Init and HealthCheck never run user code. Teardown only closes the slot;
// the authored end action owns user lifecycle teardown.
type PooledSlotProtocol struct {
	nativeServiceBinding
	slot      pooledSlot
	target    string
	timeout   time.Duration
	executeMu sync.Mutex
	terminal  atomic.Bool
}

var _ protocol.Protocol = (*PooledSlotProtocol)(nil)
var _ protocol.StreamingProtocol = (*PooledSlotProtocol)(nil)

func NewPooledSlotProtocol(slot *javasidecar.PooledClientSlot, timeout time.Duration) (*PooledSlotProtocol, error) {
	if slot == nil || slot.HostID() == "" || slot.SlotID() == "" {
		return nil, errors.New("pooled_slot_unavailable")
	}
	return newPooledSlotProtocol(slot, timeout)
}

func newPooledSlotProtocol(slot pooledSlot, timeout time.Duration) (*PooledSlotProtocol, error) {
	if !javarunner.ValidPooledTargetClass(slot.TargetClass()) || timeout < time.Millisecond || timeout > javasidecar.MaximumExecutionTimeout {
		return nil, errors.New("pooled_adapter_configuration_invalid")
	}
	return &PooledSlotProtocol{slot: slot, target: slot.TargetClass(), timeout: timeout}, nil
}
func (*PooledSlotProtocol) Name() string { return javaVuserProtocolName }
func (p *PooledSlotProtocol) Init(ctx context.Context, _ map[string]any) error {
	return p.HealthCheck(ctx)
}
func (p *PooledSlotProtocol) HealthCheck(ctx context.Context) error {
	if code := p.healthCode(ctx); code != "" {
		return errors.New(code)
	}
	return nil
}
func (p *PooledSlotProtocol) healthCode(ctx context.Context) string {
	if err := ctx.Err(); err != nil {
		return pooledProtocolErrorCode(err)
	}
	if p.terminal.Load() {
		return "pooled_slot_unusable"
	}
	if err := p.slot.Err(); err != nil {
		return pooledProtocolErrorCode(err)
	}
	return ""
}
func (p *PooledSlotProtocol) Teardown(ctx context.Context) error {
	p.terminal.Store(true)
	if err := p.slot.Close(ctx); err != nil {
		return errors.New(pooledProtocolErrorCode(err))
	}
	return nil
}
func (p *PooledSlotProtocol) Execute(ctx context.Context, a *protocol.Action) (*protocol.Result, error) {
	return p.ExecuteStream(ctx, a, nil)
}

func (p *PooledSlotProtocol) ExecuteStream(ctx context.Context, a *protocol.Action, emit func(*protocol.Result) error) (*protocol.Result, error) {
	start := time.Now()
	result := &protocol.Result{StartTime: start, Success: true, StatusCode: 200, Extractions: map[string]string{}, DependencyType: javaVuserProtocolName}
	if a != nil {
		result.ActionName = a.Name
		result.DependencyTarget = strings.TrimSpace(a.Target)
	}
	fail := func(code string) (*protocol.Result, error) {
		finalizePersistentResult(result, start, a, runnerEnvelope{}, nil, 0, false, errors.New("java_vuser pooled execution failed ("+code+")"))
		p.evidence(result)
		return result, nil
	}
	if !p.executeMu.TryLock() {
		return fail("pooled_slot_busy")
	}
	defer p.executeMu.Unlock()
	if code := p.healthCode(ctx); code != "" {
		return fail(code)
	}
	if a == nil || a.Type != "java_vuser_run" || strings.TrimSpace(a.Target) != p.target {
		return fail("pooled_action_invalid")
	}
	method := actionMethod(a)
	// The pilot's qualified reflective methods are exact. Other supported
	// dedicated lifecycle spellings must be selected as dedicated in preflight.
	if method != "init" && method != "action" && method != "end" {
		return fail("pooled_method_invalid")
	}
	timeout, err := actionTimeout(a, p.timeout)
	if err != nil || timeout < time.Millisecond || timeout > javasidecar.MaximumExecutionTimeout {
		return fail("pooled_timeout_invalid")
	}
	params, err := pooledParameters(a.Params)
	if err != nil {
		return fail("pooled_parameters_invalid")
	}
	ctx, closeNative, err := p.nativeContext(ctx, "pooled-invoke")
	if err != nil {
		return fail("native_service_unavailable")
	}
	defer closeNative()
	invocation, err := p.slot.Invoke(ctx, method, params, timeout)
	if err != nil {
		p.terminal.Store(true)
		return fail(pooledProtocolErrorCode(err))
	}
	events, err := pooledProtocolEvents(invocation, p.slot.SlotID())
	if err != nil || invocation.Method != method {
		p.terminal.Store(true)
		// Keep the malformed-result error authoritative; cleanup failure is still
		// owned by the run and reported by its eventual Close.
		closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = p.slot.Close(closeCtx)
		cancel()
		return fail("pooled_event_capture_invalid")
	}
	success := invocation.ReturnCode == 0
	envelope := runnerEnvelope{Contract: javarunner.PooledIPCContract, Kind: "result", RequestID: invocation.EventRequestID, Success: success, ClassName: p.target, Method: method, DurationMS: invocation.DurationMS, EventsTruncated: invocation.EventsTruncated}
	if !success {
		envelope.Error = "pooled_return_code_nonzero"
		if method == "init" {
			p.terminal.Store(true)
		}
	}
	finalizePersistentResult(result, start, a, envelope, events, len(invocation.Events), invocation.EventsTruncated, nil)
	p.evidence(result)
	result.CorrelationID = "invoke-" + p.slot.SlotID() + "-" + strconv.FormatUint(invocation.Sequence, 10)
	result.ProtocolEvidence["runner_return_code"] = invocation.ReturnCode
	if invocation.Exit != nil {
		result.ProtocolEvidence["runner_exit_option"] = invocation.Exit.Option
		result.ProtocolEvidence["runner_exit_status"] = invocation.Exit.Status
		result.LifecycleControl = &protocol.LifecycleControl{EndIteration: invocation.Exit.Option == 2, Failed: invocation.Exit.Status != 0}
	}
	if emit != nil {
		attributedEmit := func(child *protocol.Result) error { p.evidence(child); return emit(child) }
		if err := emitRunnerTransactionsAt(a, events, result.StartTime.Add(result.Duration), invocation.EventsTruncated, attributedEmit); err != nil {
			return result, err
		}
		if invocation.Exit != nil {
			event := runnerEvent{Contract: javaVuserEventV2, RequestID: invocation.EventRequestID, Type: "runtime", Name: "exit", Message: fmt.Sprintf("%d %d", invocation.Exit.Option, invocation.Exit.Status)}
			if child := failedRunnerExitResult(a, event); child != nil {
				if err := attributedEmit(child); err != nil {
					return result, err
				}
			}
		}
	}
	return result, nil
}

func (p *PooledSlotProtocol) evidence(result *protocol.Result) {
	if result.ProtocolEvidence == nil {
		result.ProtocolEvidence = map[string]any{}
	}
	result.ProtocolEvidence["runner_contract"] = javarunner.PooledIPCContract
	result.ProtocolEvidence["runner_backend"] = "sidecar"
	result.ProtocolEvidence["runner_mode"] = p.slot.Mode()
	result.ProtocolEvidence["runner_host_generation"] = p.slot.HostID()
	result.ProtocolEvidence["runner_slot_id"] = p.slot.SlotID()
}

// Pilot parameters are strings, as supplied by the engine's runtime parameter
// binding. lr_parameters takes precedence, matching the dedicated harness.
// Complex/non-string authored values require dedicated qualification for now.
func pooledParameters(values map[string]any) (map[string]string, error) {
	out := map[string]string{}
	for _, key := range []string{"parameters", "lr_parameters"} {
		raw, exists := values[key]
		if !exists || raw == nil {
			continue
		}
		add := func(k, v string) error {
			if len(k) == 0 || len(k) > 126 || len(v) > 65536 || !utf8.ValidString(k) || !utf8.ValidString(v) {
				return errors.New("invalid")
			}
			out[k] = v
			if len(out) > 256 {
				return errors.New("invalid")
			}
			return nil
		}
		switch entries := raw.(type) {
		case map[string]string:
			if len(entries) > 256 {
				return nil, errors.New("invalid")
			}
			for k, v := range entries {
				if err := add(k, v); err != nil {
					return nil, err
				}
			}
		case map[string]any:
			if len(entries) > 256 {
				return nil, errors.New("invalid")
			}
			for k, v := range entries {
				s, ok := v.(string)
				if !ok {
					return nil, errors.New("invalid")
				}
				if err := add(k, s); err != nil {
					return nil, err
				}
			}
		default:
			return nil, errors.New("invalid")
		}
	}
	size := 0
	for k, v := range out {
		size += len(k) + len(v)
	}
	if size > 256<<10 {
		return nil, errors.New("invalid")
	}
	return out, nil
}

// Parse every record strictly rather than silently dropping malformed typed
// timings. The client already authenticates and binds this capture. Namespace
// transaction IDs by slot because different hosts can reuse IPC request IDs.
func pooledProtocolEvents(r *javasidecar.PooledInvocationResult, slot string) ([]runnerEvent, error) {
	invalid := errors.New("pooled_event_capture_invalid")
	if r == nil || r.Sequence == 0 || r.EventRequestID == "" || len(r.Events) > javasidecar.MaxCaptureBytes || !utf8.ValidString(r.Events) {
		return nil, invalid
	}
	if r.Exit != nil && (r.Method != "action" || (r.Exit.Option != 1 && r.Exit.Option != 2)) {
		return nil, invalid
	}
	var events []runnerEvent
	var sequence int64
	for _, line := range strings.Split(r.Events, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var e runnerEvent
		if json.Unmarshal([]byte(line), &e) != nil || e.Contract != javaVuserEventV2 || e.RequestID != r.EventRequestID || e.Sequence <= sequence || strings.TrimSpace(e.Type) == "" {
			return nil, invalid
		}
		sequence = e.Sequence
		for _, duration := range []*int64{e.DurationMS, e.RawDurationMS, e.ThinkTimeMS, e.WastedTimeMS} {
			if duration != nil && (*duration < 0 || *duration > javasidecar.MaximumExecutionTimeout.Milliseconds()+2000) {
				return nil, invalid
			}
		}
		if e.TransactionID != "" {
			e.TransactionID = slot + ":" + r.EventRequestID + ":" + e.TransactionID
		}
		if e.ParentTransactionID != "" {
			e.ParentTransactionID = slot + ":" + r.EventRequestID + ":" + e.ParentTransactionID
		}
		// Control and failed-exit reporting use the authoritative frame even if
		// the capture was truncated or contained duplicate runtime exit records.
		if strings.EqualFold(strings.TrimSpace(e.Type), "runtime") && strings.EqualFold(strings.TrimSpace(e.Name), "exit") {
			continue
		}
		events = append(events, e)
	}
	return events, nil
}

func pooledProtocolErrorCode(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "execution_canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "execution_timeout"
	case errors.Is(err, javasidecar.ErrPooledClientClosed):
		return "pooled_client_closed"
	case errors.Is(err, javasidecar.ErrPooledClientState):
		return "pooled_client_state_invalid"
	case errors.Is(err, javasidecar.ErrPooledClientDecision):
		return "pooled_client_decision_changed"
	}
	var remote *javasidecar.RemoteError
	if errors.As(err, &remote) {
		switch remote.Code {
		case "pooled_host_reaped", "pooled_run_fenced", "pooled_owner_fenced", "pooled_host_state_invalid", "pooled_event_capture_invalid", "pooled_slot_busy", "pooled_request_conflict", "pooled_control_failed", "pooled_cleanup_failed", "server_draining":
			return remote.Code
		}
	}
	return "pooled_execution_failed"
}
