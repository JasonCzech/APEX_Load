package recordbridge

import (
	"context"
	"time"
)

type sessionLease struct{ port SessionLease }

func wrapSessionLease(port SessionLease) *sessionLease {
	if port == nil {
		return nil
	}
	return &sessionLease{port: port}
}
func (s *sessionLease) fail(err error) {
	if s != nil {
		s.port.Fail(err)
	}
}
func (s *sessionLease) failureOrExpired(now time.Time) error {
	if s == nil {
		return nil
	}
	return s.port.FailureOrExpired(now)
}
func (s *sessionLease) retainForTerminal(ttl time.Duration) error {
	if s == nil {
		return nil
	}
	return s.port.RetainForTerminal(ttl)
}
func (s *sessionLease) releaseRetainedID() {
	if s != nil {
		s.port.ReleaseRetainedID()
	}
}
func (s *sessionLease) releaseAll() {
	if s != nil {
		s.port.ReleaseAll()
	}
}
func watchReplaySessionLease(ctx context.Context, cancel context.CancelFunc, signal *sessionLease) *sessionLease {
	if signal == nil {
		return nil
	}
	go func() {
		select {
		case <-signal.port.Done():
			cancel()
		case <-ctx.Done():
		}
	}()
	return signal
}
func releaseRetainedReplaySessionSignals(signals []*sessionLease) {
	for _, signal := range signals {
		signal.releaseRetainedID()
	}
}
func (m *BrowserReplayManager) executionFenceError(ctx context.Context, lease *sessionLease) error {
	if err := lease.failureOrExpired(time.Now()); err != nil {
		return err
	}
	if ctx != nil && ctx.Err() != nil {
		return m.executionContextError
	}
	return nil
}
