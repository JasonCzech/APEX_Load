package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/javasidecar"
	"github.com/jasonczech/apexload/pkg/protocol"
	javaadapter "github.com/jasonczech/apexload/pkg/protocol/java_vuser"
)

// JavaPooledPilotOptions is trusted operator wiring, never script/API input.
// ScriptSHA256 must be approved together with Run.Decision's immutable runtime
// identity. The supplied client must already own the generator epoch. This
// bounded single-group pilot has no fallback, distributed placement, or scaling.
type JavaPooledPilotOptions struct {
	// CollectDiagnostics requires prior FeaturePooledDiagnosticsV1 negotiation.
	CollectDiagnostics bool
	Client             *javasidecar.Client
	Run                javasidecar.PooledClientOptions
	ScriptSHA256       string
	PreparationTimeout time.Duration
	ActionTimeout      time.Duration
}

type javaPooledPilot struct {
	options           JavaPooledPilotOptions
	used              atomic.Bool
	run               *javasidecar.PooledClientRun
	adapters          []protocol.Protocol
	keeperCancel      context.CancelFunc
	keeperDone        chan struct{}
	mu                sync.Mutex
	failure           error
	diagnostics       []*javaHostEvidenceCollector
	diagnosticsCancel context.CancelFunc
	diagnosticsDone   chan struct{}
}

// JavaPooledScriptSHA256 hashes the exact JSON-serializable DSL snapshot. This
// computes identity only; it does not approve a workload or runtime profile.
func JavaPooledScriptSHA256(script *dsl.Script) (string, error) {
	if script == nil {
		return "", errors.New("pooled_script_invalid")
	}
	data, err := json.Marshal(script)
	if err != nil {
		return "", errors.New("pooled_script_invalid")
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func NewJavaPooledPilot(cfg Config, options JavaPooledPilotOptions) (*Engine, error) {
	if options.Client == nil || !options.Run.Decision.InvocationEnabled || cfg.VUsers > options.Run.Decision.MaxVUsers || options.PreparationTimeout < time.Millisecond || options.PreparationTimeout > time.Hour || options.ActionTimeout < time.Millisecond || options.ActionTimeout > javasidecar.MaximumExecutionTimeout {
		return nil, errors.New("pooled_pilot_configuration_invalid")
	}
	digest, err := JavaPooledScriptSHA256(cfg.Script)
	if err != nil || digest != options.ScriptSHA256 || (options.Run.Decision.ScriptSHA256 != "" && options.Run.Decision.ScriptSHA256 != digest) {
		return nil, errors.New("pooled_script_identity_mismatch")
	}
	// Freeze the script along with the approved digest; caller edits after
	// construction cannot redirect the admitted pilot workload.
	data, _ := json.Marshal(cfg.Script)
	var script dsl.Script
	if json.Unmarshal(data, &script) != nil {
		return nil, errors.New("pooled_script_invalid")
	}
	if script.Protocol != "java_vuser" || isOpenWorkload(&script) || len(script.Config.RunLogic.ActionSequence) != 0 || script.Config.RunLogic.BlockScheduling != "" && script.Config.RunLogic.BlockScheduling != "sequential" {
		return nil, errors.New("pooled_pilot_workload_unsupported")
	}
	for i, phase := range [][]dsl.Action{script.Init, script.Actions, script.Teardown} {
		if len(phase) != 1 {
			return nil, errors.New("pooled_pilot_lifecycle_unsupported")
		}
		a := phase[0]
		if a.Type != "java_vuser_run" || a.Target != options.Run.Decision.TargetClass || a.Control != nil || a.Params["method"] != []string{"init", "action", "end"}[i] {
			return nil, errors.New("pooled_pilot_lifecycle_unsupported")
		}
		merged := map[string]string{}
		for _, key := range []string{"parameters", "lr_parameters"} {
			if raw := a.Params[key]; raw != nil {
				m, ok := raw.(map[string]any)
				if !ok || len(m) > 256 {
					return nil, errors.New("pooled_pilot_parameters_unsupported")
				}
				for k, v := range m {
					value, ok := v.(string)
					if !ok || len(k) == 0 || len(k) > 126 || len(value) > 65536 || !utf8.ValidString(k) || !utf8.ValidString(value) {
						return nil, errors.New("pooled_pilot_parameters_unsupported")
					}
					merged[k] = value
				}
			}
		}
		size := 0
		for k, v := range merged {
			size += len(k) + len(v)
		}
		if len(merged) > 256 || size > 256<<10 {
			return nil, errors.New("pooled_pilot_parameters_unsupported")
		}
		if raw, ok := a.Params["timeout_ms"]; ok {
			timeout, err := protocol.BoundedMilliseconds(raw, options.ActionTimeout, dsl.MaxConfiguredProtocolTimeout)
			if err != nil || timeout < time.Millisecond || timeout > javasidecar.MaximumExecutionTimeout {
				return nil, errors.New("pooled_pilot_timeout_invalid")
			}
		}
	}
	if math.IsNaN(script.Config.ThinkTime.LimitMs) || math.IsInf(script.Config.ThinkTime.LimitMs, 0) || script.Config.ThinkTime.LimitMs < 0 || script.Config.ThinkTime.LimitMs > 3600000 {
		return nil, errors.New("pooled_pilot_think_time_invalid")
	}
	cfg.Script = &script
	e, err := New(cfg)
	if err != nil {
		return nil, err
	}
	e.pooledPilot = &javaPooledPilot{options: options}
	return e, nil
}

func (p *javaPooledPilot) stage(ctx context.Context, cancelRun context.CancelFunc, e *Engine) error {
	digest, err := JavaPooledScriptSHA256(e.script)
	if err != nil || digest != p.options.ScriptSHA256 {
		return errors.New("pooled_script_identity_mismatch")
	}
	stageCtx, cancel := context.WithTimeout(ctx, p.options.PreparationTimeout)
	// The claim keeps the run lifetime context. Expired staging cancels that
	// lifetime too, without attaching a successful run to a temporary context.
	abort := context.AfterFunc(stageCtx, cancelRun)
	defer func() { abort(); cancel() }()
	p.run, err = p.options.Client.OpenPooledClientRun(ctx, p.options.Run)
	if err != nil {
		return errors.New("pooled_pilot_claim_failed")
	}
	if err = p.run.RenewOwner(stageCtx); err != nil {
		return errors.New("pooled_pilot_owner_fenced")
	}
	keeperCtx, keeperCancel := context.WithCancel(ctx)
	p.keeperCancel, p.keeperDone = keeperCancel, make(chan struct{})
	go p.keepOwner(keeperCtx, cancelRun)
	users := make([]javasidecar.PooledSlotContext, e.vusers)
	for i := range users {
		users[i] = javasidecar.PooledSlotContext{VUserID: int64(i), IgnoreThinkTime: e.script.Config.ThinkTime.Ignore, ThinkTimeLimitMS: int64(e.script.Config.ThinkTime.LimitMs)}
	}
	slots, err := p.run.Prepare(stageCtx, users, p.options.PreparationTimeout)
	if err != nil {
		return errors.New("pooled_pilot_preparation_failed")
	}
	hosts := map[string]*javaHostEvidenceCollector{}
	for _, slot := range slots {
		adapter, err := javaadapter.NewPooledSlotProtocol(slot, p.options.ActionTimeout)
		if err != nil {
			return errors.New("pooled_pilot_adapter_failed")
		}
		var evidence *javaHostEvidenceCollector
		if p.options.CollectDiagnostics {
			evidence = hosts[slot.HostID()]
			if evidence == nil {
				evidence = &javaHostEvidenceCollector{evidence: JavaHostEvidence{HostID: slot.HostID(), GCStatus: "unobserved", UnflushedTail: true}, read: p.run.Diagnostics}
				hosts[slot.HostID()] = evidence
				p.diagnostics = append(p.diagnostics, evidence)
			}
			evidence.remaining++
			evidence.evidence.SlotIDs = append(evidence.evidence.SlotIDs, slot.SlotID())
		}
		p.adapters = append(p.adapters, &javaPooledPilotProtocol{PooledSlotProtocol: adapter, pilot: p, evidence: evidence})
	}
	if err = p.run.Commit(stageCtx); err != nil {
		return errors.New("pooled_pilot_commit_failed")
	}
	p.startDiagnostics(ctx)
	return stageCtx.Err()
}

func (p *javaPooledPilot) keepOwner(ctx context.Context, cancelRun context.CancelFunc) {
	defer close(p.keeperDone)
	ticker := time.NewTicker(javasidecar.RunnerOwnerLeaseRenewInterval)
	defer ticker.Stop()
	for {
		var failure error
		select {
		case <-ctx.Done():
			return
		case <-p.run.Done():
			failure = errors.New("pooled_pilot_run_lease_lost")
		case <-ticker.C:
			callCtx, cancel := context.WithTimeout(ctx, javasidecar.RunnerOwnerLeaseRenewInterval)
			if err := p.run.RenewOwner(callCtx); err != nil && ctx.Err() == nil {
				failure = errors.New("pooled_pilot_owner_lease_lost")
			}
			cancel()
		}
		if failure != nil {
			if ctx.Err() != nil {
				return
			}
			p.recordFailure(failure)
			cancelRun()
			return
		}
	}
}

// Preserve teardown errors that the ordinary engine logs without returning.
// Embedding the concrete adapter retains its streaming result contract.
type javaPooledPilotProtocol struct {
	*javaadapter.PooledSlotProtocol
	pilot     *javaPooledPilot
	evidence  *javaHostEvidenceCollector
	closeOnce sync.Once
}

var _ protocol.StreamingProtocol = (*javaPooledPilotProtocol)(nil)

func (p *javaPooledPilotProtocol) Teardown(ctx context.Context) error {
	if p.evidence != nil {
		p.evidence.mu.Lock()
		defer p.evidence.mu.Unlock()
		p.closeOnce.Do(func() { p.evidence.collect(ctx); p.evidence.remaining-- })
	}
	err := p.PooledSlotProtocol.Teardown(ctx)
	if err != nil {
		p.pilot.recordFailure(errors.New("pooled_pilot_slot_cleanup_failed"))
	}
	return err
}

func (p *javaPooledPilot) recordFailure(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failure == nil {
		p.failure = err
	}
}

func (p *javaPooledPilot) finish() error {
	if p.diagnosticsCancel != nil {
		p.diagnosticsCancel()
		<-p.diagnosticsDone
	}
	if p.keeperCancel != nil {
		p.keeperCancel()
		<-p.keeperDone
	}
	p.mu.Lock()
	failure := p.failure
	p.mu.Unlock()
	if p.run != nil {
		ctx, cancel := context.WithTimeout(context.Background(), javasidecar.RuntimeLeaseTTL+time.Second)
		defer cancel()
		if err := p.run.Close(ctx); err != nil {
			failure = errors.Join(failure, errors.New("pooled_pilot_cleanup_unconfirmed"))
		}
		// A lease may fail just as the last Vuser exits, before the keeper has
		// observed Done. Read after Close so renewal failure immediately before
		// cancellation is also retained (the managed run keeps its first cause).
		if cause := p.run.Err(); cause != nil && !errors.Is(cause, context.Canceled) && !errors.Is(cause, context.DeadlineExceeded) && !errors.Is(cause, javasidecar.ErrPooledClientClosed) {
			failure = errors.Join(failure, errors.New("pooled_pilot_run_lease_lost"))
		}
	}
	return failure
}

func (e *Engine) vuserProtocol(id int) (protocol.Protocol, error) {
	if e.brokerExecution != nil {
		if id < 0 || id >= len(e.brokerExecution.adapters) {
			return nil, errors.New("java_broker_session_unavailable")
		}
		return e.brokerExecution.adapters[id], nil
	}
	if e.pooledPilot != nil {
		if id < 0 || id >= len(e.pooledPilot.adapters) {
			return nil, errors.New("pooled_pilot_slot_unavailable")
		}
		return e.pooledPilot.adapters[id], nil
	}
	return protocol.Get(e.script.Protocol)
}
