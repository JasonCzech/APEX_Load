package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/javarunner"
	"github.com/jasonczech/apexload/pkg/javarunner/broker"
	"github.com/jasonczech/apexload/pkg/javasidecar"
	"github.com/jasonczech/apexload/pkg/protocol"
	javaadapter "github.com/jasonczech/apexload/pkg/protocol/java_vuser"
)

// JavaBrokerOptions is trusted operator wiring for one execution/group. Client
// must be fresh and exclusively owned by this engine. ScriptSHA256 and Profile
// are approved together; scripts cannot select profiles or transport identities.
// This experimental entry point does not enable distributed placement.
type JavaBrokerOptions struct {
	Client                                          *javasidecar.BrokerFacadeClient
	Profile                                         broker.BrokerProfileCapability
	ScriptSHA256, TargetClass, ExecutionID, GroupID string
	PreparationTimeout, RunLifetime                 time.Duration
}
type javaBrokerExecution struct {
	options  JavaBrokerOptions
	used     atomic.Bool
	runtime  *javasidecar.BrokerRuntime
	run      *javasidecar.PreparedBrokerRun
	adapters []*javaBrokerVuser
	cancel   context.CancelFunc
	done     chan struct{}
	mu       sync.Mutex
	failure  error
}

func NewJavaBrokerExecution(cfg Config, o JavaBrokerOptions) (*Engine, error) {
	if o.Client == nil {
		return nil, errors.New("java_broker_configuration_invalid")
	}
	script, e := validateJavaBrokerScript(cfg.Script, cfg.Duration, o)
	if e != nil {
		return nil, e
	}
	cfg.Script = script
	engine, e := New(cfg)
	if e != nil {
		return nil, e
	}
	engine.brokerExecution = &javaBrokerExecution{options: o}
	return engine, nil
}
func validateJavaBrokerScript(input *dsl.Script, duration time.Duration, o JavaBrokerOptions) (*dsl.Script, error) {
	digest, e := JavaPooledScriptSHA256(input)
	if e != nil || digest != o.ScriptSHA256 || o.Profile.MemoryBytes < 1 || o.Profile.HostLeaseMS < 300 || o.Profile.InvocationMS < 1 || o.Profile.InvocationMS > 3600000 || !javarunner.ValidPooledTargetClass(o.TargetClass) || o.PreparationTimeout < time.Millisecond || o.PreparationTimeout > time.Duration(o.Profile.PreparationMS)*time.Millisecond || o.RunLifetime < time.Millisecond || o.RunLifetime.Milliseconds() > o.Profile.MaxRunMS || duration > o.RunLifetime {
		return nil, errors.New("java_broker_configuration_invalid")
	}
	if o.RunLifetime-duration < o.PreparationTimeout+time.Duration(o.Profile.InvocationMS)*time.Millisecond {
		return nil, errors.New("java_broker_lifetime_margin_insufficient")
	}
	// Freeze caller-owned input before any authority is claimed. Broader lifecycle
	// shapes continue through the ordinary dedicated runner until admitted here.
	data, _ := json.Marshal(input)
	var script dsl.Script
	if json.Unmarshal(data, &script) != nil || script.Protocol != "java_vuser" || isOpenWorkload(&script) || len(script.Config.RunLogic.ActionSequence) != 0 || len(script.Config.RunLogic.Blocks) != 0 || script.Config.RunLogic.BlockScheduling != "" && script.Config.RunLogic.BlockScheduling != "sequential" {
		return nil, errors.New("java_broker_workload_unsupported")
	}
	for i, phase := range [][]dsl.Action{script.Init, script.Actions, script.Teardown} {
		if len(phase) != 1 || phase[0].Type != "java_vuser_run" || phase[0].Target != o.TargetClass || phase[0].Control != nil || phase[0].Params["method"] != []string{"init", "action", "end"}[i] {
			return nil, errors.New("java_broker_lifecycle_unsupported")
		}
		if raw, ok := phase[0].Params["timeout_ms"]; ok {
			timeout, err := protocol.BoundedMilliseconds(raw, time.Duration(o.Profile.InvocationMS)*time.Millisecond, dsl.MaxConfiguredProtocolTimeout)
			if err != nil || timeout < time.Millisecond || timeout > time.Duration(o.Profile.InvocationMS)*time.Millisecond {
				return nil, errors.New("java_broker_timeout_exceeds_profile")
			}
		}
	}
	if math.IsNaN(script.Config.ThinkTime.LimitMs) || math.IsInf(script.Config.ThinkTime.LimitMs, 0) || script.Config.ThinkTime.LimitMs < 0 || script.Config.ThinkTime.LimitMs > 3600000 {
		return nil, errors.New("java_broker_think_time_invalid")
	}
	return &script, nil
}
func (b *javaBrokerExecution) fail(e error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failure == nil {
		b.failure = e
	}
}
func (b *javaBrokerExecution) stage(ctx context.Context, stop context.CancelFunc, e *Engine) error {
	stage, cancel := context.WithTimeout(ctx, b.options.PreparationTimeout)
	defer cancel()
	c := b.options.Client
	caps, err := c.Capabilities(stage)
	if err != nil {
		return err
	}
	found := false
	for _, p := range caps.Profiles {
		if p == b.options.Profile {
			found = true
		}
	}
	if !found || e.vusers > caps.MaxHosts || e.vusers > caps.MaxVUsers || int64(e.vusers) > caps.MaxMemoryBytes/b.options.Profile.MemoryBytes {
		return errors.New("java_broker_admission_mismatch")
	}
	b.runtime, err = javasidecar.NewBrokerRuntime(ctx, c)
	if err != nil {
		return err
	}
	contexts := make([]javarunner.DedicatedHostContext, e.vusers)
	for i := range contexts {
		contexts[i] = javarunner.DedicatedHostContext{Contract: javarunner.DedicatedHostContextContract, SessionID: fmt.Sprintf("vuser-%d", i), VUserID: int64(i), GroupName: b.options.GroupID, ScenarioID: b.options.ExecutionID, IgnoreThinkTime: e.script.Config.ThinkTime.Ignore, ThinkTimeLimitMS: int64(e.script.Config.ThinkTime.LimitMs)}
	}
	b.run, err = b.runtime.Prepare(stage, javasidecar.BrokerPreparation{ExecutionID: b.options.ExecutionID, GroupID: b.options.GroupID, Profile: b.options.Profile, PreparationTimeout: b.options.PreparationTimeout, RunLifetime: b.options.RunLifetime, Sessions: contexts})
	if err != nil {
		return err
	}
	if e.script.Config.NativeServices != nil && len(e.script.Config.NativeServices.LiveTables) > 0 && !b.run.NativeDataAvailable() {
		return errors.New("native_data_broker_v1 capability unavailable")
	}
	keeper, kcancel := context.WithCancel(ctx)
	b.cancel = kcancel
	b.done = make(chan struct{})
	go func() {
		defer close(b.done)
		select {
		case <-keeper.Done():
			return
		case <-b.runtime.Done():
		case <-b.run.Done():
		}
		if keeper.Err() == nil {
			b.fail(errors.New("java_broker_lease_lost"))
			stop()
		}
	}()
	if err = b.run.Commit(stage); err != nil {
		return err
	}
	return b.activate(stage, keeper, stop, contexts)
}

func (b *javaBrokerExecution) activate(ctx, keeper context.Context, stop context.CancelFunc, contexts []javarunner.DedicatedHostContext) error {
	// Transfer every admitted host before the ramp clock starts. No Init call or
	// constructor runs during this handoff. Each wrapper owns its own renewal.
	for _, slotContext := range contexts {
		session, err := b.run.Activate(ctx, slotContext.SessionID)
		if err != nil {
			return err
		}
		adapter, err := javaadapter.NewBrokerFacadeProtocol(session, b.options.TargetClass, time.Duration(b.options.Profile.InvocationMS)*time.Millisecond)
		if err != nil {
			return err
		}
		vuCtx, vuCancel := context.WithCancel(keeper)
		wrapper := &javaBrokerVuser{DedicatedBrokerProtocol: adapter, session: session, execution: b, cancel: vuCancel, done: make(chan struct{})}
		b.adapters = append(b.adapters, wrapper)
		go b.keep(vuCtx, stop, session.LeaseInterval()/3, func(ctx context.Context) error {
			err := session.Renew(ctx)
			// End and close may race a renewal response. The end invocation itself
			// remains responsible for reporting any failure once closure starts.
			if wrapper.ending.Load() {
				return nil
			}
			return err
		}, wrapper.done)
	}
	return ctx.Err()
}

func (b *javaBrokerExecution) keep(ctx context.Context, stop context.CancelFunc, interval time.Duration, renew func(context.Context) error, done chan struct{}) {
	defer close(done)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			call, cancel := context.WithTimeout(ctx, interval)
			err := renew(call)
			cancel()
			if err != nil && ctx.Err() == nil {
				b.fail(errors.New("java_broker_lease_lost"))
				stop()
				return
			}
		}
	}
}
func (b *javaBrokerExecution) finish() error {
	if b.options.Client != nil {
		defer b.options.Client.Close()
	}
	for _, adapter := range b.adapters {
		adapter.stopRenewal()
	}
	if b.cancel != nil {
		b.cancel()
		<-b.done
	}
	if b.run != nil {
		if err := b.run.Close(); err != nil {
			b.fail(errors.New("java_broker_run_cleanup_request_failed"))
		}
	}
	if b.runtime != nil && b.options.Client != nil {
		if err := b.runtime.Close(); err != nil {
			b.fail(errors.New("java_broker_owner_cleanup_request_failed"))
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.failure
}

type javaBrokerVuser struct {
	*javaadapter.DedicatedBrokerProtocol
	session   *javasidecar.BrokerFacadeSession
	execution *javaBrokerExecution
	cancel    context.CancelFunc
	done      chan struct{}
	once      sync.Once
	ending    atomic.Bool
}

func (p *javaBrokerVuser) stopRenewal() { p.once.Do(func() { p.cancel(); <-p.done }) }
func (p *javaBrokerVuser) ExecuteStream(ctx context.Context, a *protocol.Action, emit func(*protocol.Result) error) (*protocol.Result, error) {
	// Renew throughout a potentially long end call, then stop before teardown.
	if a != nil && a.Params["method"] == "end" {
		p.ending.Store(true)
		defer p.stopRenewal()
	}

	return p.DedicatedBrokerProtocol.ExecuteStream(ctx, a, emit)
}
func (p *javaBrokerVuser) Execute(ctx context.Context, a *protocol.Action) (*protocol.Result, error) {
	return p.ExecuteStream(ctx, a, nil)
}
func (p *javaBrokerVuser) Teardown(ctx context.Context) error {
	p.stopRenewal()
	err := p.DedicatedBrokerProtocol.Teardown(ctx)
	if err != nil {
		p.execution.fail(errors.New("java_broker_host_cleanup_requested_failed"))
	}
	return err
}
