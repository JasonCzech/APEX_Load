package api

import (
	"context"
	"errors"
)

// Debugger HTTP authorization consults the durable settings store before
// authentication. Keep a bounded number of those reads in flight so a burst
// of unauthenticated requests cannot fill the shared database pool with
// fresh-policy lookups. WebSocket delivery uses a separate path and does not
// consume these HTTP admission slots. Four slots permit concurrent debugger
// polls and controls while limiting the per-controller database impact.
const maxConcurrentDebugHTTPAuthReads = 4

// One slot covers the entire WebSocket authorization, including identity and
// project reads. Waiting is bounded by the caller's deadline and cancellation.
const maxConcurrentDebugWSAuthChecks = 4

var errDebugHTTPAuthBusy = errors.New("debugger authentication settings busy")

func (s *Server) debugHTTPAuthSettings(ctx context.Context) (AuthSettings, error) {
	if err := ctx.Err(); err != nil {
		return AuthSettings{}, err
	}
	s.debugHTTPAuthSlotsOnce.Do(func() {
		s.debugHTTPAuthSlots = make(chan struct{}, maxConcurrentDebugHTTPAuthReads)
	})
	select {
	case s.debugHTTPAuthSlots <- struct{}{}:
		defer func() { <-s.debugHTTPAuthSlots }()
	default:
		return AuthSettings{}, errDebugHTTPAuthBusy
	}
	return s.websocketDurableAuthSettings(ctx)
}

func (s *Server) acquireDebugWSAuth(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.debugWSAuthSlotsOnce.Do(func() {
		s.debugWSAuthSlots = make(chan struct{}, maxConcurrentDebugWSAuthChecks)
	})
	select {
	case s.debugWSAuthSlots <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-s.debugWSAuthSlots
			return nil, err
		}
		return func() { <-s.debugWSAuthSlots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *Server) debugWSMayDeliver(parent context.Context, projectID string, authorize func(context.Context, string, bool, bool) bool) bool {
	ctx, cancel := context.WithTimeout(parent, defaultCacheWriteTimeout)
	defer cancel()
	release, err := s.acquireDebugWSAuth(ctx)
	if err != nil {
		// The editor polls its session snapshot while a run is active, which
		// recovers missed events and terminal state after temporary overload.
		return false
	}
	defer release()
	return authorize(ctx, projectID, true, true)
}
