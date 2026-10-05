//go:build eventhubs_real

package eventhubs

import (
	"context"
	"errors"
	"math"
	"sync"

	"github.com/jasonczech/apexload/pkg/protocol"
)

var errProcessorDemandChanged = errors.New("processor consume demand changed")

type processorDemandSet struct {
	mu       sync.Mutex
	next     uint64
	requests map[uint64]context.Context
	epoch    context.Context
	cancel   context.CancelCauseFunc
}

func (s *eventHubsProcessor) registerDemand(ctx context.Context) (func(), error) {
	if s.pauseGate == nil {
		return func() {}, nil
	}
	d := &s.demands
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.requests) >= 32 || d.next == math.MaxUint64 {
		return nil, protocol.ErrRuntimePauseAdmissionInvalid
	}
	if d.requests == nil {
		d.requests = make(map[uint64]context.Context)
	}
	d.next++
	id := d.next
	d.requests[id] = ctx
	d.changed(s.ctx)
	var once sync.Once
	return func() { once.Do(func() { d.mu.Lock(); delete(d.requests, id); d.changed(s.ctx); d.mu.Unlock() }) }, nil
}
func (d *processorDemandSet) changed(lifetime context.Context) {
	if d.cancel != nil {
		d.cancel(errProcessorDemandChanged)
	}
	d.epoch, d.cancel = context.WithCancelCause(lifetime)
}
func (s *eventHubsProcessor) demandContext(wait bool, lifetime context.Context) (context.Context, context.Context, error) {
	if s.pauseGate == nil {
		return lifetime, lifetime, nil
	}
	for {
		d := &s.demands
		d.mu.Lock()
		if d.epoch == nil {
			d.changed(s.ctx)
		}
		epoch := d.epoch
		var selected context.Context
		var oldest uint64 = math.MaxUint64
		for id, ctx := range d.requests {
			if ctx.Err() == nil && id < oldest {
				selected, oldest = ctx, id
			}
		}
		d.mu.Unlock()
		if selected != nil {
			return selected, epoch, nil
		}
		if !wait {
			return lifetime, epoch, nil
		}
		select {
		case <-lifetime.Done():
			return nil, nil, lifetime.Err()
		case <-epoch.Done():
		}
	}
}

// Cancellation never frees a lease. The release closure runs after the actual
// SDK call/handoff returns, including calls that ignore their cancellation.
func (s *eventHubsProcessor) receiveAdmission(wait bool, scopes ...context.Context) (context.Context, func(), error) {
	lifetime := s.ctx
	if len(scopes) > 0 {
		lifetime = scopes[0]
	}
	if err := lifetime.Err(); err != nil {
		return nil, nil, err
	}
	for {
		ctx, epoch, err := s.demandContext(wait, lifetime)
		if err != nil {
			return nil, nil, err
		}
		if s.pauseGate == nil {
			return ctx, func() {}, nil
		}
		admission, cancel := context.WithCancelCause(ctx)
		stopLife := context.AfterFunc(lifetime, func() { cancel(context.Cause(lifetime)) })
		stopDemand := context.AfterFunc(epoch, func() { cancel(errProcessorDemandChanged) })
		lease, err := protocol.AdmitRuntimePause(admission, s.pauseGate, protocol.RuntimePauseReceive)
		if err != nil {
			changed := errors.Is(context.Cause(admission), errProcessorDemandChanged)
			stopLife()
			stopDemand()
			cancel(err)
			if changed && lifetime.Err() == nil {
				continue
			}
			return nil, nil, err
		}
		return lease.Context(), func() { stopLife(); stopDemand(); cancel(context.Canceled); lease.Release() }, nil
	}
}
func (s *eventHubsProcessor) pauseInterrupted(ctx context.Context, err error) bool {
	return s.pauseGate != nil && (errors.Is(context.Cause(ctx), protocol.ErrRuntimePauseSuspended) || errors.Is(context.Cause(ctx), errProcessorDemandChanged) || ctx.Err() != nil && s.ctx.Err() == nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)))
}
