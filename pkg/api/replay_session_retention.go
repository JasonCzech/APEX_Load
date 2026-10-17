package api

import (
	"context"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/recordbridge"
	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	defaultReplaySessionTerminalTTL        = 2 * time.Hour
	defaultReplaySessionIdleCancel         = 30 * time.Minute
	defaultReplaySessionMaxSessions        = 250
	replaySessionCloseDrainTimeout         = 5 * time.Second
	defaultReplaySessionWorkerAbandonGrace = 2 * time.Minute
)

var errReplaySessionIDInvalid = recordbridge.ErrReplaySessionIDInvalid

// replaySessionProjectMatches treats an empty expected project as an
// unrestricted (global-admin/internal) lookup. Project-scoped HTTP handlers
// pass the project bound by middleware so an ID retired and reused between
// authorization and execution cannot substitute a different tenant's session.
func replaySessionProjectMatches(actualProjectID, expectedProjectID string) bool {
	return recordbridge.ReplaySessionProjectMatches(actualProjectID, expectedProjectID)
}

// replaySessionIDReserver owns the shared progress-event namespace. The
// release callback releases an active reservation and may safely be called
// more than once. Terminal managers transition the accompanying guard to a
// retained tombstone before publishing terminal state.
type replaySessionIDReserver func(context.Context, string, string) (sessionID string, release func(), leaseSignal *replaySessionLeaseSignal, err error)

type replaySessionLeaseSignal struct {
	once                sync.Once
	done                chan struct{}
	mu                  sync.RWMutex
	err                 error
	validUntil          time.Time
	retainForTerminalFn func(time.Duration) error
	releaseRetainedIDFn func()
	releaseAllFn        func()
}

func newReplaySessionLeaseSignal() *replaySessionLeaseSignal {
	return &replaySessionLeaseSignal{done: make(chan struct{})}
}

func (s *replaySessionLeaseSignal) fail(err error) {
	if s == nil {
		return
	}
	s.once.Do(func() {
		if err == nil {
			err = errReplaySessionLeaseLost
		}
		s.mu.Lock()
		s.err = err
		s.mu.Unlock()
		close(s.done)
	})
}

func (s *replaySessionLeaseSignal) setValidUntil(validUntil time.Time) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.validUntil = validUntil
	s.mu.Unlock()
}

func (s *replaySessionLeaseSignal) clearValidUntil() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.validUntil = time.Time{}
	s.mu.Unlock()
}

// failureOrExpired closes the local fence even when the renewal goroutine was
// delayed long enough that it has not yet observed the deadline. Callers must
// use this immediately before publishing a terminal result.
func (s *replaySessionLeaseSignal) failureOrExpired(now time.Time) error {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	err := s.err
	validUntil := s.validUntil
	s.mu.RUnlock()
	if err != nil {
		return err
	}
	if !validUntil.IsZero() && !now.Before(validUntil) {
		s.fail(errReplaySessionLeaseLost)
		return errReplaySessionLeaseLost
	}
	return nil
}

func (s *replaySessionLeaseSignal) retainForTerminal(ttl time.Duration) error {
	if s == nil {
		return nil
	}
	if err := s.failureOrExpired(time.Now()); err != nil {
		return err
	}
	if s.retainForTerminalFn == nil {
		return nil
	}
	return s.retainForTerminalFn(ttl)
}

func (s *replaySessionLeaseSignal) releaseRetainedID() {
	if s != nil && s.releaseRetainedIDFn != nil {
		s.releaseRetainedIDFn()
	}
}

// releaseAll atomically tears down whichever lifecycle state currently owns
// the session ID. Shutdown cannot safely compose releaseRetainedID with the
// active-release callback: a worker could transition active -> retained between
// those two calls and leave a durable tombstone behind.
func (s *replaySessionLeaseSignal) releaseAll() {
	if s != nil && s.releaseAllFn != nil {
		s.releaseAllFn()
	}
}

// releaseRetainedReplaySessionSignals releases retained tombstone reservations
// collected while a manager mutex was held. Callers must invoke it only after
// dropping the manager mutex: in HA mode every release is a synchronous store
// call (up to the lease operation timeout each), and performing them under the
// manager lock freezes every handler and running replay's progress path.
// releaseRetainedID is idempotent and lifecycle-fenced, so racing a concurrent
// release of the same reservation is safe.
func releaseRetainedReplaySessionSignals(signals []*replaySessionLeaseSignal) {
	for _, signal := range signals {
		signal.releaseRetainedID()
	}
}

func waitForReplaySessionWorkers(sessions []<-chan struct{}, timeout time.Duration) {
	recordbridge.WaitForReplaySessionWorkers(sessions, timeout)
}

// replaySessionLeaseStore is the narrow durable coordination boundary required
// for caller-visible replay IDs in HA mode. SQLite and PostgreSQL implement it
// with a dedicated lease/tombstone table and database clock.
type replaySessionLeaseStore interface {
	TryAcquireReplaySessionLease(context.Context, string, string, string, time.Duration, int, int) (storage.ReplaySessionLeaseAcquireResult, error)
	RenewReplaySessionLease(context.Context, string, string, string, time.Duration) (bool, error)
	RetainReplaySessionLease(context.Context, string, string, string, time.Duration) (bool, error)
	ReleaseReplaySessionLease(context.Context, string, string, string) (bool, error)
	ReleaseRetainedReplaySessionLease(context.Context, string, string, string) (bool, error)
}

func watchReplaySessionLease(ctx context.Context, cancel context.CancelFunc, signal *replaySessionLeaseSignal) *replaySessionLeaseSignal {
	if signal == nil {
		return nil
	}
	go func() {
		select {
		case <-signal.done:
			cancel()
		case <-ctx.Done():
		}
	}()
	return signal
}

func replaySessionTerminalTTL(ttl time.Duration) time.Duration {
	return recordbridge.ReplaySessionTerminalTTL(ttl)
}

// validateExternalReplaySessionID limits client-selected event-correlation IDs
// to unpredictable 122-bit UUIDv4 capabilities. The dashboard selects the ID
// before the HTTP response so it can subscribe to early WebSocket events; an
// empty value remains valid for callers that do not need that synchronization.
var validateExternalReplaySessionID = recordbridge.ValidateExternalReplaySessionID

type replaySessionRetentionConfig struct {
	terminalTTL time.Duration
	idleCancel  time.Duration
	maxSessions int
	// workerAbandonGrace bounds how long a cancelled replay worker may ignore
	// cancellation before its session slot and HA lease are force-released and
	// the session is marked failed/orphaned. Zero disables the backstop.
	workerAbandonGrace time.Duration
	now                func() time.Time
}

func defaultReplaySessionRetentionConfig() replaySessionRetentionConfig {
	config := recordbridge.DefaultReplayRetentionConfig()
	return replaySessionRetentionConfig{terminalTTL: config.TerminalTTL, idleCancel: config.IdleCancel, maxSessions: config.MaxSessions, workerAbandonGrace: config.WorkerAbandonGrace, now: config.Now}
}

func (c replaySessionRetentionConfig) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func firstConfiguredPositiveInt(fallback int, names ...string) int {
	return recordbridge.FirstConfiguredPositiveInt(fallback, names...)
}

func replaySessionJanitorInterval(config replaySessionRetentionConfig) time.Duration {
	return recordbridge.ReplaySessionJanitorInterval(recordbridge.ReplayRetentionConfig{TerminalTTL: config.terminalTTL, IdleCancel: config.idleCancel})
}

func replaySessionStatusTerminal(status string) bool {
	return recordbridge.ReplaySessionStatusTerminal(status)
}

// Exported lease methods adapt controller ownership to the shared replay manager.
func (s *replaySessionLeaseSignal) Done() <-chan struct{} {
	if s == nil {
		return nil
	}
	return s.done
}
func (s *replaySessionLeaseSignal) Fail(err error) { s.fail(err) }
func (s *replaySessionLeaseSignal) FailureOrExpired(now time.Time) error {
	return s.failureOrExpired(now)
}
func (s *replaySessionLeaseSignal) RetainForTerminal(ttl time.Duration) error {
	return s.retainForTerminal(ttl)
}
func (s *replaySessionLeaseSignal) ReleaseRetainedID() { s.releaseRetainedID() }
func (s *replaySessionLeaseSignal) ReleaseAll()        { s.releaseAll() }
