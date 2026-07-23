package api

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	defaultReplaySessionTerminalTTL        = 2 * time.Hour
	defaultReplaySessionIdleCancel         = 30 * time.Minute
	defaultReplaySessionMaxSessions        = 250
	replaySessionCloseDrainTimeout         = 5 * time.Second
	defaultReplaySessionWorkerAbandonGrace = 2 * time.Minute
)

var errReplaySessionIDInvalid = errors.New("replay_session_id must be a canonical UUID v4")

// replaySessionProjectMatches treats an empty expected project as an
// unrestricted (global-admin/internal) lookup. Project-scoped HTTP handlers
// pass the project bound by middleware so an ID retired and reused between
// authorization and execution cannot substitute a different tenant's session.
func replaySessionProjectMatches(actualProjectID, expectedProjectID string) bool {
	expectedProjectID = strings.TrimSpace(expectedProjectID)
	return expectedProjectID == "" || normalizeProjectID(actualProjectID) == normalizeProjectID(expectedProjectID)
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
	if len(sessions) == 0 {
		return
	}
	if timeout <= 0 {
		timeout = replaySessionCloseDrainTimeout
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for _, done := range sessions {
		if done == nil {
			continue
		}
		select {
		case <-done:
		case <-timer.C:
			return
		}
	}
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
	if ttl > 0 {
		return ttl
	}
	return defaultReplaySessionTerminalTTL
}

// validateExternalReplaySessionID limits client-selected event-correlation IDs
// to unpredictable 122-bit UUIDv4 capabilities. The dashboard selects the ID
// before the HTTP response so it can subscribe to early WebSocket events; an
// empty value remains valid for callers that do not need that synchronization.
func validateExternalReplaySessionID(value string) error {
	if value == "" {
		return nil
	}
	if strings.TrimSpace(value) != value {
		return errReplaySessionIDInvalid
	}
	parsed, err := uuid.Parse(value)
	if err != nil || parsed.Version() != uuid.Version(4) || parsed.Variant() != uuid.RFC4122 || parsed.String() != value {
		return errReplaySessionIDInvalid
	}
	return nil
}

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
	return replaySessionRetentionConfig{
		terminalTTL: firstConfiguredDuration(defaultReplaySessionTerminalTTL,
			"APEXLOAD_REPLAY_SESSION_TERMINAL_TTL",
			"APEXLOAD_REPLAY_SESSION_TERMINAL_TTL_SECONDS",
		),
		idleCancel: firstConfiguredDuration(defaultReplaySessionIdleCancel,
			"APEXLOAD_REPLAY_SESSION_IDLE_CANCEL",
			"APEXLOAD_REPLAY_SESSION_IDLE_CANCEL_SECONDS",
		),
		maxSessions: firstConfiguredPositiveInt(defaultReplaySessionMaxSessions,
			"APEXLOAD_REPLAY_SESSION_MAX",
			"APEXLOAD_REPLAY_SESSION_MAX_SESSIONS",
		),
		workerAbandonGrace: firstConfiguredDuration(defaultReplaySessionWorkerAbandonGrace,
			"APEXLOAD_REPLAY_SESSION_WORKER_ABANDON_GRACE",
			"APEXLOAD_REPLAY_SESSION_WORKER_ABANDON_GRACE_SECONDS",
		),
		now: time.Now,
	}
}

func (c replaySessionRetentionConfig) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func firstConfiguredPositiveInt(fallback int, names ...string) int {
	for _, name := range names {
		raw := strings.TrimSpace(os.Getenv(name))
		if raw == "" {
			continue
		}
		value, err := strconv.Atoi(raw)
		if err == nil && value > 0 {
			return value
		}
	}
	return fallback
}

func replaySessionJanitorInterval(config replaySessionRetentionConfig) time.Duration {
	interval := time.Minute
	for _, candidate := range []time.Duration{config.terminalTTL / 2, config.idleCancel / 2} {
		if candidate > 0 && candidate < interval {
			interval = candidate
		}
	}
	if interval < time.Second {
		return time.Second
	}
	return interval
}

func replaySessionStatusTerminal(status string) bool {
	switch status {
	case "completed", "failed", "stopped":
		return true
	default:
		return false
	}
}
