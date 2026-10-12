package javasidecar

import (
	"context"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/javarunner"
	"github.com/jasonczech/apexload/pkg/javarunner/broker"
)

// BrokerRuntime owns one generator's facade connection and owner lease. It may
// prepare independent groups concurrently. Closing a group never releases this
// owner or the other groups. The caller must close it on generator shutdown.
// Active Vuser leases remain the responsibility of their individual engines.
type BrokerRuntime struct {
	client     *BrokerFacadeClient
	caps       broker.BrokerCapabilities
	ctx        context.Context
	cancel     context.CancelCauseFunc
	mu         sync.Mutex
	closing    bool
	work       sync.WaitGroup
	done       chan struct{}
	cleanupErr error
}

// BrokerPreparation is trusted operator/controller input, frozen before reserve.
// PreparationTimeout covers provisioning and the wait for an explicit commit.
// RunLifetime is a fixed wall-clock bound including ramps, pauses and teardown.
type BrokerPreparation struct {
	ExecutionID, GroupID            string
	Profile                         broker.BrokerProfileCapability
	PreparationTimeout, RunLifetime time.Duration
	Sessions                        []javarunner.DedicatedHostContext
}

// PreparedBrokerRun has reached harness readiness without running user code.
// Commit and Activate are explicit; neither calls user initialization. Close
// requests containment cleanup but does not assert that capacity is released.
type PreparedBrokerRun struct {
	runtime    *BrokerRuntime
	run        *BrokerFacadeRun
	ctx        context.Context
	cancel     context.CancelCauseFunc
	done       chan struct{}
	opMu       sync.Mutex
	transferMu sync.Mutex
	mu         sync.Mutex
	committed  bool
	deadline   time.Time
	duration   time.Duration
	cleanupErr error
	wake       chan struct{}
}

func NewBrokerRuntime(ctx context.Context, client *BrokerFacadeClient) (*BrokerRuntime, error) {
	if ctx == nil || client == nil {
		return nil, ErrFacadeState
	}
	caps, err := client.Capabilities(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range caps.Profiles {
		if p.HostLeaseMS < 300 {
			return nil, ErrFacadeState
		}
	}
	if err = client.Claim(ctx); err != nil {
		return nil, err
	}
	life, cancel := context.WithCancelCause(ctx)
	r := &BrokerRuntime{client: client, caps: caps, ctx: life, cancel: cancel, done: make(chan struct{})}
	go r.keepOwner()
	return r, nil
}

// Capabilities returns a copy of the negotiated snapshot. The broker validates
// the exact profile again at reservation, so drift cannot silently change it.
func (r *BrokerRuntime) Capabilities() broker.BrokerCapabilities {
	c := r.caps
	c.Profiles = append([]broker.BrokerProfileCapability(nil), c.Profiles...)
	return c
}
func (r *BrokerRuntime) Err() error            { return context.Cause(r.ctx) }
func (r *BrokerRuntime) Done() <-chan struct{} { return r.ctx.Done() }
func (r *BrokerRuntime) Close() error {
	r.cancel(ErrFacadeOwner)
	<-r.done
	return r.cleanupErr
}
func (r *BrokerRuntime) keepOwner() {
	defer close(r.done)
	interval := r.client.LeaseInterval() / 3
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	defer r.client.Close()
	for r.ctx.Err() == nil {
		select {
		case <-r.ctx.Done():
		case <-ticker.C:
			call, cancel := context.WithTimeout(r.ctx, interval)
			err := r.client.Renew(call)
			cancel()
			if err != nil {
				r.cancel(err)
			}
		}
	}
	r.mu.Lock()
	r.closing = true
	r.mu.Unlock()
	r.work.Wait()
	cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r.cleanupErr = r.client.Release(cleanup)
}

func (r *BrokerRuntime) Prepare(ctx context.Context, o BrokerPreparation) (*PreparedBrokerRun, error) {
	if ctx == nil {
		return nil, ErrFacadeState
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	found := false
	for _, p := range r.caps.Profiles {
		if p == o.Profile {
			found = true
		}
	}
	if !found || o.PreparationTimeout < time.Millisecond || o.PreparationTimeout > time.Duration(o.Profile.PreparationMS)*time.Millisecond || o.RunLifetime < time.Millisecond || o.RunLifetime > time.Duration(o.Profile.MaxRunMS)*time.Millisecond || len(o.Sessions) < 1 || len(o.Sessions) > r.caps.MaxHosts || len(o.Sessions) > r.caps.MaxVUsers || int64(len(o.Sessions)) > r.caps.MaxMemoryBytes/o.Profile.MemoryBytes {
		return nil, ErrFacadeState
	}
	// Registration and shutdown's Wait are fenced under the same mutex. In-flight
	// reserves are cancelled and joined even when their replies arrive late.
	r.mu.Lock()
	if r.closing || r.ctx.Err() != nil {
		r.mu.Unlock()
		return nil, ErrFacadeOwner
	}
	r.work.Add(1)
	r.mu.Unlock()
	defer r.work.Done()
	deadline := time.Now().Add(o.PreparationTimeout)
	stage, cancel := context.WithDeadline(ctx, deadline)
	detach := context.AfterFunc(r.ctx, cancel)
	defer func() { detach(); cancel() }()
	run, err := r.client.Reserve(stage, o.ExecutionID, o.GroupID, o.Profile, o.RunLifetime, append([]javarunner.DedicatedHostContext(nil), o.Sessions...))
	if err != nil {
		return nil, err
	}
	life, stop := context.WithCancelCause(r.ctx)
	p := &PreparedBrokerRun{runtime: r, run: run, ctx: life, cancel: stop, done: make(chan struct{}), deadline: deadline, duration: o.RunLifetime, wake: make(chan struct{}, 1)}
	r.work.Add(1)
	go p.keep()
	// A cancelled preparation cannot leave a ready reservation for a later caller.
	defer func() {
		if err != nil {
			p.cancel(err)
			<-p.done
		}
	}()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		var ready bool
		ready, err = run.Ready(stage)
		if err != nil {
			return nil, err
		}
		if ready {
			if err = stage.Err(); err == nil {
				err = p.Err()
			}
			if err != nil {
				return nil, err
			}
			return p, nil
		}
		select {
		case <-stage.Done():
			err = stage.Err()
			return nil, err
		case <-p.ctx.Done():
			err = p.Err()
			return nil, err
		case <-ticker.C:
		}
	}
}

func (p *PreparedBrokerRun) Profile() broker.BrokerProfileCapability { return p.run.Profile() }
func (p *PreparedBrokerRun) Err() error {
	if err := context.Cause(p.ctx); err != nil {
		return err
	}
	p.mu.Lock()
	expired := !time.Now().Before(p.deadline)
	p.mu.Unlock()
	if expired {
		return context.DeadlineExceeded
	}
	return nil
}
func (p *PreparedBrokerRun) Done() <-chan struct{} { return p.ctx.Done() }
func (p *PreparedBrokerRun) Close() error {
	p.cancel(ErrFacadeLease)
	<-p.done
	return p.cleanupErr
}
func (p *PreparedBrokerRun) callContext(ctx context.Context) (context.Context, func()) {
	p.mu.Lock()
	deadline := p.deadline
	p.mu.Unlock()
	call, cancel := context.WithDeadline(ctx, deadline)
	detach := context.AfterFunc(p.ctx, cancel)
	return call, func() { detach(); cancel() }
}
func (p *PreparedBrokerRun) Commit(ctx context.Context) error {
	if ctx == nil {
		return ErrFacadeState
	}
	p.opMu.Lock()
	defer p.opMu.Unlock()
	if err := p.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	committed := p.committed
	p.mu.Unlock()
	if committed {
		return ErrFacadeState
	}
	call, cancel := p.callContext(ctx)
	defer cancel()
	// Conservatively begin the local execution bound before the remote commit.
	until := time.Now().Add(p.duration)
	err := p.run.Commit(call)
	p.mu.Lock()
	if err == nil && (p.ctx.Err() != nil || !time.Now().Before(p.deadline) || call.Err() != nil) {
		err = ErrFacadeLease
	}
	if err == nil {
		p.committed = true
		p.deadline = until
	}
	p.mu.Unlock()
	if err != nil {
		p.cancel(err)
		return err
	}
	select {
	case p.wake <- struct{}{}:
	default:
	}
	return nil
}
func (p *PreparedBrokerRun) Activate(ctx context.Context, sessionID string) (*BrokerFacadeSession, error) {
	if ctx == nil {
		return nil, ErrFacadeState
	}
	p.opMu.Lock()
	defer p.opMu.Unlock()
	if err := p.Err(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	committed := p.committed
	p.mu.Unlock()
	if !committed {
		return nil, ErrFacadeState
	}
	p.transferMu.Lock()
	defer p.transferMu.Unlock()
	bounded, stop := context.WithTimeout(ctx, p.run.LeaseInterval()/3)
	defer stop()
	call, cancel := p.callContext(bounded)
	defer cancel()
	s, err := p.run.Activate(call, sessionID)
	if err == nil {
		err = p.Err()
	}
	if err == nil {
		err = call.Err()
	}
	if err != nil {
		p.cancel(err)
		return nil, err
	}
	return s, nil
}
func (p *PreparedBrokerRun) keep() {
	defer p.runtime.work.Done()
	defer close(p.done)
	interval := p.run.LeaseInterval() / 3
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for p.ctx.Err() == nil {
		p.mu.Lock()
		remaining := time.Until(p.deadline)
		p.mu.Unlock()
		timer := time.NewTimer(max(0, remaining))
		select {
		case <-p.ctx.Done():
		case <-p.wake:
		case <-timer.C:
			// Commit may have replaced the preparation deadline since this timer was
			// created. Re-read it before expiring the reservation.
			if err := p.Err(); err != nil {
				p.cancel(err)
			}
		case <-ticker.C:
			// Queue behind a bounded activation instead of skipping ticks: a
			// sustained handoff must not starve pending-host lease renewal.
			// Activation observes p.ctx, so cancellation releases this lock.
			p.transferMu.Lock()
			call, cancel := context.WithTimeout(p.ctx, interval)
			bounded, finish := p.callContext(call)
			err := p.run.Renew(bounded)
			finish()
			cancel()
			p.transferMu.Unlock()
			if err != nil && p.ctx.Err() == nil {
				p.cancel(err)
			}

		}
		timer.Stop()
	}
	cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p.cleanupErr = p.run.Abort(cleanup)
}

// Cancel fences local use immediately and requests asynchronous containment
// cleanup. Close additionally waits for that request to finish.
func (p *PreparedBrokerRun) Cancel() { p.cancel(ErrFacadeLease) }

// ID is the opaque reservation identity, never its authority token.
func (p *PreparedBrokerRun) ID() string { return p.run.id }

// NativeDataAvailable is valid only before Commit and comes from every actual
// prepared JVM, through the existing authenticated owner/wave response.
func (p *PreparedBrokerRun) NativeDataAvailable() bool {
	return p.Err() == nil && p.run.NativeDataAvailable()
}
