package engine

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

var ErrPauseQuiescenceUnproven = errors.New("pause quiescence could not be proven")

// A successful latch is retained when proof fails. The control plane must
// compensate it or stop the run; cancellation is not a successful pause.
type PauseQuiescenceUnprovenError struct{ Cause error }

func (e *PauseQuiescenceUnprovenError) Error() string { return ErrPauseQuiescenceUnproven.Error() }
func (e *PauseQuiescenceUnprovenError) Unwrap() error { return e.Cause }
func (e *PauseQuiescenceUnprovenError) Is(err error) bool {
	return err == ErrPauseQuiescenceUnproven
}

const maxRuntimePauseLeases = 65536

type runtimePauseCleanupKey struct{}
type runtimePauseCleanupAuthority struct {
	gate     *runtimePauseCoordinator
	deadline time.Time
}

type runtimePauseActionKey struct{}
type runtimePauseActionToken struct {
	gate *runtimePauseCoordinator
	id   uint64
}

type runtimePauseCoordinator struct {
	mu          sync.Mutex
	paused      bool
	closed      bool
	terminal    bool
	unsupported bool
	epoch       uint64
	next        uint64
	changed     chan struct{}
	leases      map[uint64]*runtimePauseLease
}

type runtimePauseLease struct {
	gate    *runtimePauseCoordinator
	id      uint64
	parent  uint64
	kind    protocol.RuntimePauseActivity
	cleanup bool
	ctx     context.Context
	cancel  context.CancelCauseFunc
}

func (l *runtimePauseLease) Context() context.Context { return l.ctx }
func (l *runtimePauseLease) Release() {
	if l.gate == nil { // Borrowed action authority does not own the outer lease.
		return
	}
	g := l.gate
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.leases[l.id] != l {
		return
	}
	delete(g.leases, l.id)
	if l.kind == protocol.RuntimePauseAction {
		for _, child := range g.leases {
			if child.parent == l.id && child.kind == protocol.RuntimePauseReceive {
				// Surplus delegated reads lose their action's authority. Actual
				// returns, not cancellation requests, release their proof charge.
				child.cancel(protocol.ErrRuntimePauseSuspended)
			}
		}
	}
	// Publish the delegated receive cause before parent cancellation propagates.
	l.cancel(context.Canceled)
	g.notifyLocked()
}

func newRuntimePauseCoordinator(paused bool) *runtimePauseCoordinator {
	return &runtimePauseCoordinator{paused: paused, changed: make(chan struct{}), leases: make(map[uint64]*runtimePauseLease)}
}
func (g *runtimePauseCoordinator) notifyLocked() {
	close(g.changed)
	g.changed = make(chan struct{})
}

func (g *runtimePauseCoordinator) Admit(ctx context.Context, kind protocol.RuntimePauseActivity) (protocol.RuntimePauseLease, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	switch kind {
	case protocol.RuntimePauseAction, protocol.RuntimePauseReceive, protocol.RuntimePauseTail, protocol.RuntimePauseCheckpoint:
	default:
		return nil, protocol.ErrRuntimePauseAdmissionInvalid
	}
	for {
		g.mu.Lock()
		if g.closed || ctx.Err() != nil {
			g.mu.Unlock()
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, protocol.ErrRuntimePauseAdmissionInvalid
		}
		token, present := ctx.Value(runtimePauseActionKey{}).(runtimePauseActionToken)
		parent := uint64(0)
		if present {
			p := g.leases[token.id]
			if token.gate != g || p == nil || p.kind != protocol.RuntimePauseAction || p.ctx.Err() != nil {
				g.mu.Unlock()
				return nil, protocol.ErrRuntimePauseAdmissionInvalid
			}
			parent = token.id
			if kind == protocol.RuntimePauseAction {
				g.mu.Unlock()
				return &runtimePauseLease{ctx: ctx}, nil
			}
		}
		if kind == protocol.RuntimePauseCheckpoint && parent == 0 {
			g.mu.Unlock()
			return nil, protocol.ErrRuntimePauseAdmissionInvalid
		}
		cleanup := false
		if authority, present := ctx.Value(runtimePauseCleanupKey{}).(runtimePauseCleanupAuthority); present {
			deadline, bounded := ctx.Deadline()
			cleanup = bounded && !deadline.After(authority.deadline)
			if authority.gate != g || !cleanup && kind == protocol.RuntimePauseAction {
				g.mu.Unlock()
				return nil, protocol.ErrRuntimePauseAdmissionInvalid
			}
		}
		if g.terminal && kind == protocol.RuntimePauseAction && !cleanup {
			g.mu.Unlock()
			return nil, protocol.ErrRuntimePauseAdmissionInvalid
		}
		// Only a live pre-latch action may delegate a receive/checkpoint while
		// paused. Tail polls never inherit an action's exception.
		delegated := parent != 0 && (kind == protocol.RuntimePauseReceive || kind == protocol.RuntimePauseCheckpoint)
		if g.paused && !delegated && !(g.terminal && cleanup && kind == protocol.RuntimePauseAction) {
			changed := g.changed
			g.mu.Unlock()
			select {
			case <-changed:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		if len(g.leases) >= maxRuntimePauseLeases || g.next == ^uint64(0) {
			g.mu.Unlock()
			return nil, protocol.ErrRuntimePauseAdmissionInvalid
		}
		g.next++
		leaseCtx, cancel := context.WithCancelCause(ctx)
		if kind == protocol.RuntimePauseAction {
			leaseCtx = context.WithValue(leaseCtx, runtimePauseActionKey{}, runtimePauseActionToken{gate: g, id: g.next})
		}
		l := &runtimePauseLease{gate: g, id: g.next, parent: parent, kind: kind, cleanup: cleanup, ctx: leaseCtx, cancel: cancel}
		g.leases[l.id] = l
		g.notifyLocked()
		g.mu.Unlock()
		return l, nil
	}
}

func (g *runtimePauseCoordinator) latch() uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.paused {
		g.paused = true
		g.epoch++
	}
	for _, l := range g.leases {
		if l.kind == protocol.RuntimePauseTail || l.kind == protocol.RuntimePauseReceive && l.parent == 0 {
			l.cancel(protocol.ErrRuntimePauseSuspended)
		}
	}
	g.notifyLocked()
	return g.epoch
}
func (g *runtimePauseCoordinator) wait(ctx context.Context, epoch uint64) error {
	for {
		g.mu.Lock()
		if g.closed || g.terminal || !g.paused || g.epoch != epoch || g.unsupported {
			g.mu.Unlock()
			return protocol.ErrRuntimePauseAdmissionInvalid
		}
		if ctx.Err() != nil {
			g.mu.Unlock()
			return ctx.Err()
		}
		if len(g.leases) == 0 {
			g.mu.Unlock()
			return nil
		}
		changed := g.changed
		g.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
func (g *runtimePauseCoordinator) proven(epoch uint64) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return !g.closed && !g.terminal && g.paused && g.epoch == epoch && !g.unsupported && len(g.leases) == 0
}

func (g *runtimePauseCoordinator) resume() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.closed && !g.terminal && g.paused {
		g.paused = false
		g.epoch++
		g.notifyLocked()
	}
}

// Explicit stop closes ordinary admission but keeps the original bounded
// teardown obligation. Final close revokes this authority after cleanup exits.
func (g *runtimePauseCoordinator) beginTerminal(hard bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return
	}
	if !g.terminal {
		g.terminal = true
		g.paused = true
		g.epoch++
	}
	for _, lease := range g.leases {
		if hard && !lease.cleanup || lease.kind == protocol.RuntimePauseTail || lease.kind == protocol.RuntimePauseReceive && lease.parent == 0 {
			lease.cancel(context.Canceled)
		}
	}
	g.notifyLocked()
}

// Only trusted engine lifecycle code can mint this bounded cleanup intent.
// During an ordinary pause it still waits at admission; only an explicit
// terminal transition authorizes cleanup while paused. No old action is copied.
func bindRuntimePauseTeardown(ctx context.Context, name string, gate protocol.RuntimePauseGate) context.Context {
	ctx = protocol.WithRuntimePauseGate(ctx, name, gate)
	g, trusted := gate.(*runtimePauseCoordinator)
	deadline, bounded := ctx.Deadline()
	if !trusted || g == nil || !bounded {
		return ctx
	}
	g.mu.Lock()
	closed := g.closed
	g.mu.Unlock()
	if closed {
		return ctx
	}
	return context.WithValue(ctx, runtimePauseCleanupKey{}, runtimePauseCleanupAuthority{gate: g, deadline: deadline})
}

func (g *runtimePauseCoordinator) close() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.closed {
		g.closed = true
		g.epoch++
		for _, l := range g.leases {
			l.cancel(context.Canceled)
		}
		g.notifyLocked()
	}
}

type engineRuntimePause struct {
	groups map[string]*runtimePauseCoordinator
}

func newEngineRuntimePause(resources *engineRuntimeResources, paused bool) *engineRuntimePause {
	p := &engineRuntimePause{groups: map[string]*runtimePauseCoordinator{}}
	resources.mu.Lock()
	defer resources.mu.Unlock()
	for name, binding := range resources.bindings {
		if !binding.plan.RequiresReservation() {
			continue
		}
		g := newRuntimePauseCoordinator(paused)
		// Inspect a fresh registered adapter without Init, credentials, or probes.
		g.unsupported = !runtimePauseSupported(binding.plan.protocolName)
		p.groups[name] = g
	}
	return p
}
func runtimePauseSupported(name string) (supported bool) {
	defer func() {
		if recover() != nil {
			supported = false
		}
	}()
	instance, err := protocol.Get(name)
	if err != nil {
		return false
	}
	supported, err = protocol.ReadRuntimePauseSupport(instance)
	return supported && err == nil
}

func (p *engineRuntimePause) bind(ctx context.Context, name string) context.Context {
	if p != nil {
		if gate := p.groups[name]; gate != nil {
			return protocol.WithRuntimePauseGate(ctx, "eventhubs", gate)
		}
	}
	return ctx
}
func (p *engineRuntimePause) latch() map[*runtimePauseCoordinator]uint64 {
	epochs := map[*runtimePauseCoordinator]uint64{}
	if p != nil {
		for _, g := range p.groups {
			epochs[g] = g.latch()
		}
	}
	return epochs
}
func (p *engineRuntimePause) resume() {
	if p != nil {
		for _, g := range p.groups {
			g.resume()
		}
	}
}
func (p *engineRuntimePause) beginTerminal(hard bool) {
	if p != nil {
		for _, g := range p.groups {
			g.beginTerminal(hard)
		}
	}
}
func (p *engineRuntimePause) bindTeardown(ctx context.Context, name string) context.Context {
	if p != nil {
		if gate := p.groups[name]; gate != nil {
			return bindRuntimePauseTeardown(ctx, "eventhubs", gate)
		}
	}
	return ctx
}
func (p *engineRuntimePause) close() {
	if p != nil {
		for _, g := range p.groups {
			g.close()
		}
	}
}

func (vu *VUser) admitRuntimePauseAction(ctx context.Context) (context.Context, func(), error) {
	if vu.Script == nil {
		return ctx, func() {}, nil
	}
	gate, present := protocol.RuntimePauseGateFromContext(ctx, vu.Script.Protocol)
	if !present {
		return ctx, func() {}, nil
	}
	lease, err := protocol.AdmitRuntimePause(ctx, gate, protocol.RuntimePauseAction)
	if err != nil {
		return nil, nil, err
	}
	return lease.Context(), lease.Release, nil
}
