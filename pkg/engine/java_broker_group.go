package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/javarunner"
	"github.com/jasonczech/apexload/pkg/javarunner/broker"
	"github.com/jasonczech/apexload/pkg/javasidecar"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// JavaBrokerGroupOptions is trusted generator wiring, never script input. The
// shared Runtime belongs to the generator; this group closes only its own run.
// ScriptSHA256 approves the exact DSL with the immutable operator profile.
type JavaBrokerGroupOptions struct {
	// PreparedVUsers reserves unused sessions for later increases, without Init.
	PreparedVUsers                                  int
	Runtime                                         *javasidecar.BrokerRuntime
	Profile                                         broker.BrokerProfileCapability
	ScriptSHA256, TargetClass, ExecutionID, GroupID string
	PreparationTimeout, RunLifetime                 time.Duration
}
type javaBrokerGroup struct {
	execution javaBrokerExecution
	options   JavaBrokerGroupOptions
	contexts  []javarunner.DedicatedHostContext
	offset    int
	mu        sync.Mutex
	life      context.Context
	cancel    context.CancelFunc
	started   bool
	taken     map[int]bool
}

func newJavaBrokerGroup(group *GroupConfig, duration time.Duration) error {
	o := *group.JavaBroker
	prepared := o.PreparedVUsers
	if prepared == 0 {
		prepared = group.VUsers
	}
	if o.Runtime == nil || group.VUsers < 0 || prepared < 1 || prepared < group.VUsers || prepared > MaxEngineVUsers || (group.VUserIDSpan > 0 && prepared > group.VUserIDSpan) || groupLifecycleAtOnce(*group) {
		return errors.New("java_broker_group_invalid")
	}
	caps := o.Runtime.Capabilities()
	if prepared > caps.MaxHosts || prepared > caps.MaxVUsers || o.Profile.MemoryBytes < 1 || int64(prepared) > caps.MaxMemoryBytes/o.Profile.MemoryBytes {
		return errors.New("java_broker_admission_mismatch")
	}
	cfg := JavaBrokerOptions{Profile: o.Profile, ScriptSHA256: o.ScriptSHA256, TargetClass: o.TargetClass, ExecutionID: o.ExecutionID, GroupID: o.GroupID, PreparationTimeout: o.PreparationTimeout, RunLifetime: o.RunLifetime}
	script, err := validateJavaBrokerScript(group.Script, duration, cfg)
	if err != nil {
		return err
	}
	if group.VUserIDOffset < 0 || group.VUserIDOffset > int(^uint(0)>>1)-prepared {
		return errors.New("java_broker_identity_invalid")
	}
	life, cancel := context.WithCancel(context.Background())
	b := &javaBrokerGroup{options: o, offset: group.VUserIDOffset, life: life, cancel: cancel, taken: map[int]bool{}}
	b.execution.options = cfg
	b.execution.runtime = o.Runtime
	b.contexts = make([]javarunner.DedicatedHostContext, prepared)
	for i := range b.contexts {
		id := group.VUserIDOffset + i
		b.contexts[i] = javarunner.DedicatedHostContext{Contract: javarunner.DedicatedHostContextContract, SessionID: fmt.Sprintf("vuser-%d", id), VUserID: int64(id), GroupName: group.Name, ScenarioID: group.ScenarioID, IgnoreThinkTime: script.Config.ThinkTime.Ignore, ThinkTimeLimitMS: int64(script.Config.ThinkTime.LimitMs)}
	}
	group.Script = script
	group.JavaBroker = &o
	group.javaBroker = b
	return nil
}
func (b *javaBrokerGroup) prepare(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.life.Err() != nil {
		return errors.New("java_broker_group_closed")
	}
	if b.execution.run != nil {
		return b.execution.run.Err()
	}
	call, cancel := context.WithCancel(ctx)
	detach := context.AfterFunc(b.life, cancel)
	defer func() { detach(); cancel() }()
	o := b.options
	run, err := o.Runtime.Prepare(call, javasidecar.BrokerPreparation{ExecutionID: o.ExecutionID, GroupID: o.GroupID, Profile: o.Profile, PreparationTimeout: o.PreparationTimeout, RunLifetime: o.RunLifetime, Sessions: b.contexts})
	if err != nil {
		b.cancel()
		return err
	}
	b.execution.run = run
	context.AfterFunc(b.life, run.Cancel)
	if b.life.Err() != nil {
		run.Cancel()
		return errors.New("java_broker_group_closed")
	}
	return nil
}
func (b *javaBrokerGroup) start(ctx context.Context, stop context.CancelFunc) error {
	if err := b.prepare(ctx); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.started {
		return errors.New("java_broker_group_already_started")
	}
	b.started = true
	call, cancel := context.WithCancel(ctx)
	detach := context.AfterFunc(b.life, cancel)
	defer func() { detach(); cancel() }()
	if err := b.execution.run.Commit(call); err != nil {
		return err
	}
	keeper, kcancel := context.WithCancel(ctx)
	b.execution.cancel = kcancel
	b.execution.done = make(chan struct{})
	go func() {
		defer close(b.execution.done)
		select {
		case <-keeper.Done():
			return
		case <-b.life.Done():
		case <-b.execution.run.Done():
		case <-b.options.Runtime.Done():
		}
		if keeper.Err() == nil && b.life.Err() == nil {
			b.execution.fail(errors.New("java_broker_lease_lost"))
			stop()
		}
	}()
	return b.execution.activate(call, keeper, stop, b.contexts)
}
func (b *javaBrokerGroup) protocol(id int) (protocol.Protocol, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	index := id - b.offset
	if b.life.Err() != nil || !b.started || index < 0 || index >= len(b.execution.adapters) || b.taken[id] {
		return nil, errors.New("java_broker_admission_unavailable")
	}
	b.taken[id] = true
	return b.execution.adapters[index], nil
}
func (b *javaBrokerGroup) close() error {
	b.cancel()
	b.mu.Lock()
	defer b.mu.Unlock()
	// Stop the watcher before requesting normal closure so it cannot turn normal
	// teardown into an execution failure. Lifetime cancellation still fences I/O.
	return b.execution.finish()
}

// ClosePreparedRuntime abandons staged Java hosts without releasing the shared
// generator owner. Distributed staging must call it on rollback/expiry. A nil
// error acknowledges cleanup requests, not positive termination or free capacity.
func (mge *MultiGroupEngine) ClosePreparedRuntime() error {
	mge.mu.Lock()
	groups := append([]GroupConfig(nil), mge.groups...)
	mge.mu.Unlock()
	var err error
	for _, g := range groups {
		if g.javaBroker != nil {
			err = errors.Join(err, g.javaBroker.close())
		}
	}
	return err
}

// JavaBrokerPlacement is token-free evidence of a prepared group reservation.
type JavaBrokerPlacement struct {
	ReservationID string
	Profile       broker.BrokerProfileCapability
}

func (mge *MultiGroupEngine) JavaBrokerPlacements() []JavaBrokerPlacement {
	mge.mu.Lock()
	groups := append([]GroupConfig(nil), mge.groups...)
	mge.mu.Unlock()
	var out []JavaBrokerPlacement
	for _, g := range groups {
		if g.javaBroker == nil {
			continue
		}
		b := g.javaBroker
		b.mu.Lock()
		if b.execution.run != nil && b.life.Err() == nil && b.execution.run.Err() == nil {
			out = append(out, JavaBrokerPlacement{ReservationID: b.execution.run.ID(), Profile: b.options.Profile})
		}
		b.mu.Unlock()
	}
	return out
}
