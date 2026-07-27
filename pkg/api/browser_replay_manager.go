package api

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
)

const (
	BrowserReplayQueued    = "queued"
	BrowserReplayRunning   = "running"
	BrowserReplayPaused    = "paused"
	BrowserReplayCompleted = "completed"
	BrowserReplayFailed    = "failed"
	BrowserReplayStopped   = "stopped"
)

var (
	errBrowserReplaySessionIDConflict = errors.New("browser replay session id is already in use")
	errBrowserReplaySessionCapacity   = errors.New("browser replay session capacity is exhausted")
	errBrowserReplayManagerClosed     = errors.New("browser replay manager is closed")
)

type BrowserReplaySession struct {
	ID                string                      `json:"id"`
	Mode              string                      `json:"mode,omitempty"`
	ScriptID          string                      `json:"script_id,omitempty"`
	ProjectID         string                      `json:"project_id,omitempty"`
	Status            string                      `json:"status"`
	Group             string                      `json:"group"`
	Protocol          string                      `json:"protocol"`
	RuntimeProfile    engine.ReplayRuntimeProfile `json:"runtime_profile,omitempty"`
	Preflight         *ReplayPreflight            `json:"preflight,omitempty"`
	StartedAt         *time.Time                  `json:"started_at,omitempty"`
	CompletedAt       *time.Time                  `json:"completed_at,omitempty"`
	CurrentEventIndex int                         `json:"current_event_index"`
	CurrentEvent      *engine.ReplayActionEvent   `json:"current_event,omitempty"`
	Events            []engine.ReplayActionEvent  `json:"events,omitempty"`
	FinalReport       *engine.ReplayReport        `json:"report,omitempty"`
	Error             string                      `json:"error,omitempty"`

	mu             sync.RWMutex
	cancel         context.CancelFunc
	lastActivityAt time.Time
	terminalAt     *time.Time
	stateChanged   chan struct{}
	reservation    *replaySessionLeaseSignal
	releaseID      func()
	workerDone     chan struct{}
	workerFinished bool
	// workerAbandoned marks a worker that ignored cancellation past the abandon
	// grace period: its slot and lease were force-released by the backstop and
	// its late finalization must no-op.
	workerAbandoned bool
	abandonArmed    bool
	retired         bool
	// terminalFenceEstablished means the worker completed and retained the
	// global session capability while its execution epoch was still valid.
	// A terminal-looking status without this proof must be cleared/retired when
	// cancellation reconciliation observes a lost fence.
	terminalFenceEstablished bool
}

// replayCompletionHook is invoked once a replay session reaches a terminal
// report. The execution context belongs to the immutable leadership epoch that
// started the replay, allowing persistence hooks to reject stale completion
// work even if this process has since reacquired leadership.
type replayCompletionHook func(context.Context, string, *dsl.Script, *engine.ReplayReport)

type BrowserReplayManager struct {
	hub           *Hub
	sessions      map[string]*BrowserReplaySession
	mu            sync.RWMutex
	onComplete    replayCompletionHook
	reserveID     replaySessionIDReserver
	retention     replaySessionRetentionConfig
	janitorCancel context.CancelFunc
	closed        bool
	closeOnce     sync.Once
	closeDone     chan struct{}
	closeTimeout  time.Duration
}

func NewBrowserReplayManager(hub *Hub) *BrowserReplayManager {
	return &BrowserReplayManager{
		hub:          hub,
		sessions:     make(map[string]*BrowserReplaySession),
		retention:    defaultReplaySessionRetentionConfig(),
		closeDone:    make(chan struct{}),
		closeTimeout: replaySessionCloseDrainTimeout,
	}
}

func (m *BrowserReplayManager) setCompletionHook(hook replayCompletionHook) {
	m.onComplete = hook
}

func (m *BrowserReplayManager) setSessionIDReserver(reserve replaySessionIDReserver) {
	m.reserveID = reserve
}

func (m *BrowserReplayManager) StartJanitor(ctx context.Context) {
	if m == nil {
		return
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	if m.janitorCancel != nil {
		m.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	m.janitorCancel = cancel
	interval := replaySessionJanitorInterval(m.retention)
	m.mu.Unlock()

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.PruneExpired()
			}
		}
	}()
}

func (m *BrowserReplayManager) Close() {
	if m == nil {
		return
	}
	m.closeOnce.Do(func() {
		type closeCleanup struct {
			session   *BrowserReplaySession
			releaseID func()
		}
		now := m.retention.clock()
		m.mu.Lock()
		m.closed = true
		cancelJanitor := m.janitorCancel
		m.janitorCancel = nil
		cleanups := make([]closeCleanup, 0, len(m.sessions))
		for id, session := range m.sessions {
			session.mu.Lock()
			session.retired = true
			if session.Status == BrowserReplayRunning || session.Status == BrowserReplayPaused {
				session.Status = BrowserReplayStopped
			}
			if replaySessionStatusTerminal(session.Status) && session.terminalAt == nil {
				terminalAt := now
				session.terminalAt = &terminalAt
			}
			// The manager is no longer an addressable evidence boundary. Drop all
			// retained replay artifacts before waking/canceling the worker.
			session.CurrentEvent = nil
			session.Events = nil
			session.FinalReport = nil
			session.Preflight = nil
			if session.cancel != nil {
				session.cancel()
			}
			releaseID := session.releaseID
			session.releaseID = nil
			session.signalStateChangedLocked()
			session.mu.Unlock()
			cleanups = append(cleanups, closeCleanup{session: session, releaseID: releaseID})
			delete(m.sessions, id)
		}
		m.mu.Unlock()
		if cancelJanitor != nil {
			cancelJanitor()
		}

		waitFor := make([]<-chan struct{}, 0, len(cleanups)*2)
		for _, cleanup := range cleanups {
			cleanupDone := make(chan struct{})
			go func(cleanup closeCleanup, done chan<- struct{}) {
				defer close(done)
				cleanup.session.reservation.releaseAll()
				if cleanup.releaseID != nil {
					cleanup.releaseID()
				}
			}(cleanup, cleanupDone)
			waitFor = append(waitFor, cleanupDone)
		}
		for _, cleanup := range cleanups {
			waitFor = append(waitFor, cleanup.session.workerDone)
		}
		waitForReplaySessionWorkers(waitFor, m.closeTimeout)
		close(m.closeDone)
	})
	if m.closeDone != nil {
		<-m.closeDone
	}
}

func (m *BrowserReplayManager) Start(scriptID string, script *dsl.Script, req BrowserReplayStartRequest) (*BrowserReplaySession, error) {
	if err := replayReviewGateError(script); err != nil {
		if req.releaseExecutionContext != nil {
			req.releaseExecutionContext()
		}
		return nil, err
	}
	executionCtx := req.executionContext
	if executionCtx == nil {
		executionCtx = context.Background()
	}
	releaseExecutionContext := req.releaseExecutionContext
	if releaseExecutionContext == nil {
		releaseExecutionContext = func() {}
	}
	sessionID := strings.TrimSpace(req.ReplaySessionID)
	releaseReservation := func() {}
	var leaseSignal *replaySessionLeaseSignal
	if m.reserveID != nil {
		reservedID, release, signal, err := m.reserveID(executionCtx, sessionID, script.ProjectID)
		if err != nil {
			releaseExecutionContext()
			return nil, err
		}
		sessionID = reservedID
		releaseReservation = release
		leaseSignal = signal
	} else if sessionID == "" {
		sessionID = uuid.New().String()
	}
	groupName := req.GroupName
	if groupName == "" {
		groupName = script.Name + "-browser-replay"
	}
	now := m.retention.clock()
	workerCtx, workerCancel := context.WithCancel(executionCtx)
	cancel := workerCancel
	ctx := workerCtx
	leaseFailure := watchReplaySessionLease(ctx, cancel, leaseSignal)
	ignoreThinkTime := true
	if req.IgnoreThinkTime != nil {
		ignoreThinkTime = *req.IgnoreThinkTime
	}
	session := &BrowserReplaySession{
		ID:                sessionID,
		Mode:              "browser_replay",
		ScriptID:          scriptID,
		ProjectID:         normalizeProjectID(script.ProjectID),
		Status:            BrowserReplayRunning,
		Group:             groupName,
		Protocol:          script.Protocol,
		RuntimeProfile:    replayRuntimeProfileForBrowser(script, req, ignoreThinkTime),
		Preflight:         cloneReplayPreflight(req.Preflight),
		StartedAt:         &now,
		CurrentEventIndex: -1,
		cancel:            cancel,
		lastActivityAt:    now,
		stateChanged:      make(chan struct{}),
		reservation:       leaseSignal,
		releaseID:         releaseReservation,
		workerDone:        make(chan struct{}),
	}
	if req.StartPaused {
		session.Status = BrowserReplayPaused
	}

	// Retained-lease releases collected during pruning/eviction run once m.mu is
	// dropped (they are synchronous store calls in HA mode).
	var pruneReleases []*replaySessionLeaseSignal
	defer func() { releaseRetainedReplaySessionSignals(pruneReleases) }()
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		cancel()
		releaseExecutionContext()
		releaseReservation()
		return nil, errBrowserReplayManagerClosed
	}
	_, pruneReleases = m.pruneExpiredLocked(now)
	if existing := m.sessions[sessionID]; existing != nil {
		// Session IDs are externally addressable capabilities shared by every
		// project. Replacing an existing entry would let one project cancel another
		// project's run and creates a substitution race for evidence consumers.
		// Fail closed and leave the established session untouched.
		m.mu.Unlock()
		cancel()
		releaseExecutionContext()
		releaseReservation()
		return nil, errBrowserReplaySessionIDConflict
	}
	m.sessions[sessionID] = session
	_, enforceReleases := m.enforceMaxSessionsLocked(now, sessionID)
	pruneReleases = append(pruneReleases, enforceReleases...)
	if m.retention.maxSessions > 0 && len(m.sessions) > m.retention.maxSessions {
		delete(m.sessions, sessionID)
		m.mu.Unlock()
		cancel()
		releaseExecutionContext()
		releaseReservation()
		return nil, errBrowserReplaySessionCapacity
	}
	m.mu.Unlock()

	m.broadcastIfCurrent("browser_replay_started", session, -1, nil, nil)
	if req.StartPaused {
		m.broadcastIfCurrent("browser_replay_paused", session, -1, nil, nil)
	}
	go m.reconcileWorkerCancellation(ctx, executionCtx, session)

	go func() {
		defer m.finishWorker(session)
		defer releaseExecutionContext()
		defer cancel()
		if !m.waitWhilePaused(ctx, session) {
			completedAt := m.retention.clock()
			leaseErr := leaseFailure.retainForTerminal(m.retention.terminalTTL)
			fenceErr := replayExecutionFenceError(executionCtx, leaseFailure)
			terminalFenceEstablished := leaseErr == nil && fenceErr == nil
			messageType := "browser_replay_stopped"
			eventIndex := -1
			abandoned := false
			current := m.withCurrentSession(session, func() {
				session.mu.Lock()
				if session.workerAbandoned {
					// The abandon backstop already force-released this session's slot
					// and lease and marked it failed; the late worker must not touch
					// the orphaned record.
					abandoned = true
					session.mu.Unlock()
					return
				}
				session.CompletedAt = &completedAt
				session.terminalFenceEstablished = terminalFenceEstablished
				if !terminalFenceEstablished {
					session.Status = BrowserReplayFailed
					if fenceErr != nil {
						session.Error = fenceErr.Error()
					} else {
						session.Error = leaseErr.Error()
					}
					messageType = "browser_replay_failed"
				}
				if replaySessionStatusTerminal(session.Status) {
					// Retention starts when the worker has drained and its terminal
					// artifacts become observable, not when cancellation was first
					// requested. Otherwise a slow-but-cooperative worker can publish
					// a report whose local retention window already elapsed.
					session.terminalAt = &completedAt
				}
				session.signalStateChangedLocked()
				eventIndex = session.CurrentEventIndex
				session.mu.Unlock()
			})
			if !current || abandoned {
				return
			}
			// Publish after the state transition, off the mutation path. A lost
			// integrity fence keeps payload publication suppressed elsewhere, but
			// the terminal status itself is still pushed so WebSocket-only
			// consumers do not watch a dead session run forever.
			m.broadcastTerminal(messageType, session, eventIndex, nil, nil)
			if !terminalFenceEstablished {
				m.retireUnfencedSession(session)
			}
			return
		}

		report, err := engine.ReplayScript(ctx, script, engine.ReplayOptions{
			ReplaySessionID: sessionID,
			GroupName:       groupName,
			RuntimeProfile:  &session.RuntimeProfile,
			Variables:       req.Variables,
			RedactionRules:  req.RedactionRules,
			SecretResolver:  req.SecretResolver,
			IgnoreThinkTime: ignoreThinkTime,
			OnProgress: func(event engine.ReplayProgressEvent) {
				m.handleReplayProgress(ctx, session, event)
			},
		})

		completedAt := m.retention.clock()
		messageType := "browser_replay_completed"
		leaseErr := leaseFailure.retainForTerminal(m.retention.terminalTTL)
		fenceErr := replayExecutionFenceError(executionCtx, leaseFailure)
		terminalFenceEstablished := leaseErr == nil && fenceErr == nil
		eventIndex := -1
		abandoned := false
		current := m.withCurrentSession(session, func() {
			session.mu.Lock()
			if session.workerAbandoned {
				// The abandon backstop already force-released this session's slot
				// and lease and marked it failed; the late worker must not touch
				// the orphaned record.
				abandoned = true
				session.mu.Unlock()
				return
			}
			session.CompletedAt = &completedAt
			session.terminalFenceEstablished = terminalFenceEstablished
			if report != nil && terminalFenceEstablished {
				session.FinalReport = report
				session.Error = report.Error
			} else if !terminalFenceEstablished {
				session.FinalReport = nil
			}
			if !terminalFenceEstablished {
				session.Status = BrowserReplayFailed
				if fenceErr != nil {
					session.Error = fenceErr.Error()
				} else {
					session.Error = leaseErr.Error()
				}
				messageType = "browser_replay_failed"
			} else if session.Status == BrowserReplayStopped {
				messageType = "browser_replay_stopped"
			} else if err != nil {
				session.Status = BrowserReplayFailed
				session.Error = err.Error()
				messageType = "browser_replay_failed"
			} else if report != nil && !report.Success {
				session.Status = BrowserReplayFailed
				messageType = "browser_replay_failed"
			} else {
				session.Status = BrowserReplayCompleted
			}
			if replaySessionStatusTerminal(session.Status) {
				// Reset a Stop/idle-cancellation timestamp now that the final
				// report is actually visible to readers.
				session.terminalAt = &completedAt
			}
			session.signalStateChangedLocked()
			eventIndex = session.CurrentEventIndex
			session.mu.Unlock()
		})
		if !current || abandoned {
			return
		}
		// Broadcast and run the completion hook only after the manager read lock
		// is released: the self-heal hook performs multi-round-trip storage
		// writes, and holding m.mu across them stalls every other session's
		// handlers and progress callbacks. Currency is re-checked at publish time.
		if terminalFenceEstablished {
			if m.broadcastTerminal(messageType, session, eventIndex, nil, report) && m.onComplete != nil && report != nil {
				if messageType != "browser_replay_stopped" {
					m.onComplete(executionCtx, session.ProjectID, script, report)
				}
			}
		} else {
			// The integrity fence was lost: keep the report payload suppressed but
			// still push the terminal status for WebSocket-only consumers, then
			// retire the local capability before finishWorker releases its global
			// reservation.
			m.broadcastTerminal(messageType, session, eventIndex, nil, nil)
			m.retireUnfencedSession(session)
		}
	}()

	return session.snapshot(), nil
}

// reconcileWorkerCancellation turns an execution-epoch or replay-session lease
// cancellation into an immediate terminal state and arms the same bounded
// abandon backstop used by operator cancellation. Some protocol adapters may
// ignore ctx cancellation; without this reconciliation they would leave a
// session looking live and consuming local/durable capacity indefinitely.
//
// A fence loss also clears any report that raced with cancellation. Terminal
// WebSocket payload suppression alone is insufficient because Get snapshots
// are another publication surface.
func (m *BrowserReplayManager) reconcileWorkerCancellation(workerCtx, executionCtx context.Context, session *BrowserReplaySession) {
	if workerCtx == nil || session == nil {
		return
	}
	<-workerCtx.Done()
	fenceErr := replayExecutionFenceError(executionCtx, session.reservation)
	eventIndex := -1
	transitioned := false
	m.withCurrentSession(session, func() {
		session.mu.Lock()
		defer session.mu.Unlock()
		if session.workerFinished || session.workerAbandoned {
			return
		}
		live := session.Status == BrowserReplayRunning || session.Status == BrowserReplayPaused
		if !live && (session.terminalFenceEstablished || fenceErr == nil) {
			// Normal worker completion, Stop, idle cancellation, and Close all
			// establish a terminal status before canceling the worker context.
			// Once a terminal fence is retained, a later execution-epoch release
			// cannot invalidate the already-fenced report.
			return
		}
		failure := fenceErr
		if failure == nil {
			failure = workerCtx.Err()
		}
		if failure == nil {
			failure = context.Canceled
		}
		session.Status = BrowserReplayFailed
		session.Error = failure.Error()
		session.FinalReport = nil
		terminalAt := m.retention.clock()
		if session.terminalAt == nil {
			session.terminalAt = &terminalAt
		}
		m.scheduleWorkerAbandonBackstopLocked(session)
		session.signalStateChangedLocked()
		eventIndex = session.CurrentEventIndex
		transitioned = true
	})
	if transitioned {
		m.broadcastTerminal("browser_replay_failed", session, eventIndex, nil, nil)
	}
}

func (m *BrowserReplayManager) PruneExpired() int {
	if m == nil {
		return 0
	}
	now := m.retention.clock()
	m.mu.Lock()
	removed, releases := m.pruneExpiredLocked(now)
	m.mu.Unlock()
	releaseRetainedReplaySessionSignals(releases)
	return removed
}

// pruneExpiredLocked removes expired sessions but never performs the durable
// tombstone release itself: in HA mode releaseRetainedID is a synchronous
// store call, and executing it under m.mu freezes every handler and every
// running replay's progress path. Callers must release the returned signals
// after dropping m.mu.
func (m *BrowserReplayManager) pruneExpiredLocked(now time.Time) (int, []*replaySessionLeaseSignal) {
	removed := 0
	var releases []*replaySessionLeaseSignal
	for id, session := range m.sessions {
		session.mu.Lock()
		m.cancelIdleRunningSessionLocked(session, now)
		if m.shouldPruneSessionLocked(session, now) {
			m.cancelSessionLocked(session, now)
			session.retired = true
			releaseRetained := session.workerHasFinishedLocked()
			delete(m.sessions, id)
			removed++
			session.mu.Unlock()
			if releaseRetained {
				releases = append(releases, session.reservation)
			}
			continue
		}
		session.mu.Unlock()
	}
	enforced, enforcedReleases := m.enforceMaxSessionsLocked(now, "")
	return removed + enforced, append(releases, enforcedReleases...)
}

// enforceMaxSessionsLocked follows the same contract as pruneExpiredLocked:
// retained-lease releases are collected and must run after m.mu is dropped.
func (m *BrowserReplayManager) enforceMaxSessionsLocked(now time.Time, protectedID string) (int, []*replaySessionLeaseSignal) {
	if m.retention.maxSessions <= 0 {
		return 0, nil
	}
	removed := 0
	var releases []*replaySessionLeaseSignal
	for len(m.sessions) > m.retention.maxSessions {
		id, session := m.oldestTerminalSessionLocked(protectedID)
		if session == nil {
			return removed, releases
		}
		session.mu.Lock()
		m.cancelSessionLocked(session, now)
		session.retired = true
		releaseRetained := session.workerHasFinishedLocked()
		session.mu.Unlock()
		delete(m.sessions, id)
		if releaseRetained {
			releases = append(releases, session.reservation)
		}
		removed++
	}
	return removed, releases
}

func (m *BrowserReplayManager) oldestTerminalSessionLocked(protectedID string) (string, *BrowserReplaySession) {
	var selectedID string
	var selected *BrowserReplaySession
	var selectedAt time.Time
	for id, session := range m.sessions {
		if id == protectedID {
			continue
		}
		session.mu.RLock()
		terminal := replaySessionStatusTerminal(session.Status)
		workerFinished := session.workerHasFinishedLocked()
		if !terminal || !workerFinished {
			session.mu.RUnlock()
			continue
		}
		at := sessionRetentionSortTime(session.StartedAt, session.terminalAt, session.lastActivityAt)
		session.mu.RUnlock()
		if selected == nil || at.Before(selectedAt) {
			selectedID = id
			selected = session
			selectedAt = at
		}
	}
	return selectedID, selected
}

func (m *BrowserReplayManager) cancelIdleRunningSessionLocked(session *BrowserReplaySession, now time.Time) {
	if session == nil || m.retention.idleCancel <= 0 || (session.Status != BrowserReplayRunning && session.Status != BrowserReplayPaused) {
		return
	}
	lastActivity := session.lastActivityAt
	if lastActivity.IsZero() {
		lastActivity = now
		session.lastActivityAt = now
	}
	if now.Sub(lastActivity) <= m.retention.idleCancel {
		return
	}
	m.cancelSessionLocked(session, now)
}

func (m *BrowserReplayManager) shouldPruneSessionLocked(session *BrowserReplaySession, now time.Time) bool {
	if session == nil || m.retention.terminalTTL <= 0 || !replaySessionStatusTerminal(session.Status) || !session.workerHasFinishedLocked() {
		return false
	}
	terminalAt := session.terminalAt
	if terminalAt == nil {
		if session.CompletedAt != nil {
			completedAt := *session.CompletedAt
			session.terminalAt = &completedAt
		} else {
			terminal := now
			session.terminalAt = &terminal
		}
		terminalAt = session.terminalAt
	}
	return terminalAt != nil && now.Sub(*terminalAt) > m.retention.terminalTTL
}

func (m *BrowserReplayManager) cancelSessionLocked(session *BrowserReplaySession, now time.Time) {
	if session == nil {
		return
	}
	if session.Status == BrowserReplayRunning || session.Status == BrowserReplayPaused {
		// Cancellation is only a request until the worker actually exits. Keep its
		// active reservation (and renewal) in place so a cancellation-ignoring
		// protocol cannot disappear from the global/project capacity counts.
		if err := session.reservation.failureOrExpired(time.Now()); err != nil {
			session.Status = BrowserReplayFailed
			session.Error = err.Error()
		} else {
			session.Status = BrowserReplayStopped
		}
		if session.cancel != nil {
			session.cancel()
		}
		m.scheduleWorkerAbandonBackstopLocked(session)
	}
	if replaySessionStatusTerminal(session.Status) && session.terminalAt == nil {
		terminalAt := now
		session.terminalAt = &terminalAt
	}
	session.signalStateChangedLocked()
}

// scheduleWorkerAbandonBackstopLocked arms a one-shot backstop for a session
// whose worker was just cancelled. Requires session.mu. If the worker still
// has not returned after the generous grace period, the backstop force-releases
// the session's capacity slot and HA lease and marks the session failed, so a
// cancellation-ignoring protocol adapter cannot permanently consume capacity
// with no operator remedy short of a restart.
func (m *BrowserReplayManager) scheduleWorkerAbandonBackstopLocked(session *BrowserReplaySession) {
	grace := m.retention.workerAbandonGrace
	if grace <= 0 || session == nil || session.abandonArmed || session.workerHasFinishedLocked() {
		return
	}
	session.abandonArmed = true
	workerDone := session.workerDone
	go func() {
		timer := time.NewTimer(grace)
		defer timer.Stop()
		select {
		case <-workerDone:
			return
		case <-timer.C:
		}
		m.abandonUnresponsiveWorker(session, grace)
	}()
}

// abandonUnresponsiveWorker orphans a session whose worker ignored cancellation
// past the grace period: it fences the reservation so the late worker can no
// longer publish or retain, releases the slot and durable lease, and marks the
// session failed. The worker's own finalization detects workerAbandoned and
// no-ops; every release below is idempotent, so a worker finishing concurrently
// is harmless.
func (m *BrowserReplayManager) abandonUnresponsiveWorker(session *BrowserReplaySession, grace time.Duration) {
	session.mu.Lock()
	if session.workerFinished || session.workerAbandoned {
		session.mu.Unlock()
		return
	}
	session.workerAbandoned = true
	session.Status = BrowserReplayFailed
	session.Error = "replay worker ignored cancellation past the abandon grace period; session orphaned and its lease force-released"
	terminalAt := m.retention.clock()
	if session.terminalAt == nil {
		session.terminalAt = &terminalAt
	}
	if session.CompletedAt == nil {
		session.CompletedAt = &terminalAt
	}
	releaseID := session.releaseID
	// The abandon path must be the sole releaser from this point forward. A
	// worker that returns concurrently will run finishWorker, but it must not
	// make the global ID reusable before the stale local record is retired.
	session.releaseID = nil
	reservation := session.reservation
	session.signalStateChangedLocked()
	session.mu.Unlock()

	slog.Error("browser replay worker ignored cancellation past the grace period; force-releasing its session slot and lease",
		"replay_session_id", session.ID, "project_id", session.ProjectID, "grace", grace.String())
	// Fence first so the wedged worker cannot publish or retain afterwards.
	reservation.fail(errReplaySessionLeaseLost)
	m.broadcastTerminal("browser_replay_failed", session, -1, nil, nil)

	// The global ID may become reusable as soon as the reservation is released.
	// Retire the stale local record first so this replica cannot keep serving an
	// old failed session under the same externally addressable capability while
	// another replica (or a retry here) owns the reused ID.
	m.mu.Lock()
	if m.sessions[session.ID] == session {
		session.mu.Lock()
		session.retired = true
		delete(m.sessions, session.ID)
		session.mu.Unlock()
	}
	m.mu.Unlock()

	// Tear down whichever lifecycle state currently owns the reservation only
	// after the local lookup surface has stopped exposing the old capability.
	reservation.releaseAll()
	if releaseID != nil {
		releaseID()
	}
}

// LookupProjectID resolves the current session owner without refreshing idle
// retention. Authorization lookups must not let denied probes keep sessions
// alive.
func (m *BrowserReplayManager) LookupProjectID(sessionID string) (string, bool) {
	if m == nil {
		return "", false
	}
	m.mu.RLock()
	session := m.sessions[sessionID]
	if session == nil || session.reservation.failureOrExpired(time.Now()) != nil {
		m.mu.RUnlock()
		return "", false
	}
	session.mu.RLock()
	projectID := normalizeProjectID(session.ProjectID)
	session.mu.RUnlock()
	m.mu.RUnlock()
	return projectID, true
}

func (m *BrowserReplayManager) Get(sessionID string) (*BrowserReplaySession, bool) {
	return m.getForProject(sessionID, "")
}

func (m *BrowserReplayManager) GetForProject(sessionID, projectID string) (*BrowserReplaySession, bool) {
	return m.getForProject(sessionID, projectID)
}

func (m *BrowserReplayManager) getForProject(sessionID, projectID string) (*BrowserReplaySession, bool) {
	now := m.retention.clock()
	var pruneReleases []*replaySessionLeaseSignal
	defer func() { releaseRetainedReplaySessionSignals(pruneReleases) }()
	m.mu.Lock()
	_, pruneReleases = m.pruneExpiredLocked(now)
	session, ok := m.sessions[sessionID]
	if !ok {
		m.mu.Unlock()
		return nil, false
	}
	session.mu.Lock()
	if session.reservation.failureOrExpired(time.Now()) != nil ||
		!replaySessionProjectMatches(session.ProjectID, projectID) {
		session.mu.Unlock()
		m.mu.Unlock()
		return nil, false
	}
	session.lastActivityAt = now
	snapshot := session.snapshotLocked()
	session.mu.Unlock()
	m.mu.Unlock()
	return snapshot, true
}

func (m *BrowserReplayManager) Pause(sessionID string) (*BrowserReplaySession, bool, bool) {
	return m.pauseForProject(sessionID, "")
}

func (m *BrowserReplayManager) PauseForProject(sessionID, projectID string) (*BrowserReplaySession, bool, bool) {
	return m.pauseForProject(sessionID, projectID)
}

func (m *BrowserReplayManager) pauseForProject(sessionID, projectID string) (*BrowserReplaySession, bool, bool) {
	now := m.retention.clock()
	var pruneReleases []*replaySessionLeaseSignal
	defer func() { releaseRetainedReplaySessionSignals(pruneReleases) }()
	m.mu.Lock()
	_, pruneReleases = m.pruneExpiredLocked(now)
	session, ok := m.sessions[sessionID]
	if !ok {
		m.mu.Unlock()
		return nil, false, false
	}

	session.mu.Lock()
	if session.reservation.failureOrExpired(time.Now()) != nil ||
		!replaySessionProjectMatches(session.ProjectID, projectID) {
		session.mu.Unlock()
		m.mu.Unlock()
		return nil, false, false
	}
	session.lastActivityAt = now
	changed := false
	if session.Status == BrowserReplayRunning {
		session.Status = BrowserReplayPaused
		session.signalStateChangedLocked()
		changed = true
	}
	snapshot := session.snapshotLocked()
	session.mu.Unlock()
	m.mu.Unlock()

	if changed {
		m.broadcastIfCurrent("browser_replay_paused", session, snapshot.CurrentEventIndex, nil, snapshot.FinalReport)
	}
	return snapshot, true, changed
}

func (m *BrowserReplayManager) Resume(sessionID string) (*BrowserReplaySession, bool, bool) {
	return m.resumeForProject(sessionID, "")
}

func (m *BrowserReplayManager) ResumeForProject(sessionID, projectID string) (*BrowserReplaySession, bool, bool) {
	return m.resumeForProject(sessionID, projectID)
}

func (m *BrowserReplayManager) resumeForProject(sessionID, projectID string) (*BrowserReplaySession, bool, bool) {
	now := m.retention.clock()
	var pruneReleases []*replaySessionLeaseSignal
	defer func() { releaseRetainedReplaySessionSignals(pruneReleases) }()
	m.mu.Lock()
	_, pruneReleases = m.pruneExpiredLocked(now)
	session, ok := m.sessions[sessionID]
	if !ok {
		m.mu.Unlock()
		return nil, false, false
	}

	session.mu.Lock()
	if session.reservation.failureOrExpired(time.Now()) != nil ||
		!replaySessionProjectMatches(session.ProjectID, projectID) {
		session.mu.Unlock()
		m.mu.Unlock()
		return nil, false, false
	}
	session.lastActivityAt = now
	changed := false
	if session.Status == BrowserReplayPaused {
		session.Status = BrowserReplayRunning
		session.signalStateChangedLocked()
		changed = true
	}
	snapshot := session.snapshotLocked()
	session.mu.Unlock()
	m.mu.Unlock()

	if changed {
		m.broadcastIfCurrent("browser_replay_resumed", session, snapshot.CurrentEventIndex, nil, snapshot.FinalReport)
	}
	return snapshot, true, changed
}

func (m *BrowserReplayManager) Stop(sessionID string) (*BrowserReplaySession, bool) {
	return m.stopForProject(sessionID, "")
}

func (m *BrowserReplayManager) StopForProject(sessionID, projectID string) (*BrowserReplaySession, bool) {
	return m.stopForProject(sessionID, projectID)
}

func (m *BrowserReplayManager) stopForProject(sessionID, projectID string) (*BrowserReplaySession, bool) {
	now := m.retention.clock()
	var pruneReleases []*replaySessionLeaseSignal
	defer func() { releaseRetainedReplaySessionSignals(pruneReleases) }()
	m.mu.Lock()
	_, pruneReleases = m.pruneExpiredLocked(now)
	session, ok := m.sessions[sessionID]
	if !ok {
		m.mu.Unlock()
		return nil, false
	}

	session.mu.Lock()
	if session.reservation.failureOrExpired(time.Now()) != nil ||
		!replaySessionProjectMatches(session.ProjectID, projectID) {
		session.mu.Unlock()
		m.mu.Unlock()
		return nil, false
	}
	session.lastActivityAt = now
	if session.Status == BrowserReplayRunning || session.Status == BrowserReplayPaused {
		// Do not turn the active lease into a capacity-free tombstone until the
		// replay goroutine has returned. Some protocol adapters can outlive context
		// cancellation, and those workers must remain tracked and capacity-counted.
		if err := session.reservation.failureOrExpired(time.Now()); err != nil {
			session.Status = BrowserReplayFailed
			session.Error = err.Error()
		} else {
			session.Status = BrowserReplayStopped
		}
		if session.cancel != nil {
			session.cancel()
		}
		m.scheduleWorkerAbandonBackstopLocked(session)
		session.signalStateChangedLocked()
	}
	if replaySessionStatusTerminal(session.Status) && session.terminalAt == nil {
		terminalAt := now
		session.terminalAt = &terminalAt
	}
	snapshot := session.snapshotLocked()
	session.mu.Unlock()
	m.mu.Unlock()
	return snapshot, true
}

// retireUnfencedSession removes a terminal record that could not retain its
// global capability, then releases whichever active/retained reservation still
// owns the ID. The local lookup disappears first, so no stale record can
// overlap a retry that reuses the same externally supplied session ID.
func (m *BrowserReplayManager) retireUnfencedSession(session *BrowserReplaySession) {
	if m == nil || session == nil {
		return
	}
	var (
		releaseID   func()
		reservation *replaySessionLeaseSignal
		retired     bool
	)
	m.mu.Lock()
	if m.sessions[session.ID] == session {
		session.mu.Lock()
		if !session.terminalFenceEstablished {
			session.retired = true
			releaseID = session.releaseID
			session.releaseID = nil
			reservation = session.reservation
			delete(m.sessions, session.ID)
			retired = true
		}
		session.mu.Unlock()
	}
	m.mu.Unlock()
	if retired {
		reservation.releaseAll()
		if releaseID != nil {
			releaseID()
		}
	}
}

func (m *BrowserReplayManager) withCurrentSession(session *BrowserReplaySession, fn func()) bool {
	if m == nil || session == nil {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed || m.sessions[session.ID] != session {
		return false
	}
	if fn != nil {
		fn()
	}
	return true
}

func (m *BrowserReplayManager) withCurrentWorkerSession(session *BrowserReplaySession, fn func()) bool {
	if m == nil || session == nil {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed || m.sessions[session.ID] != session || session.reservation.failureOrExpired(time.Now()) != nil {
		return false
	}
	session.mu.RLock()
	abandoned := session.workerAbandoned
	session.mu.RUnlock()
	if abandoned {
		return false
	}
	if fn != nil {
		fn()
	}
	return true
}

func (m *BrowserReplayManager) broadcastIfCurrent(messageType string, session *BrowserReplaySession, eventIndex int, event *engine.ReplayActionEvent, report *engine.ReplayReport) bool {
	return m.withCurrentWorkerSession(session, func() {
		m.broadcast(messageType, session, eventIndex, event, report)
	})
}

// broadcastTerminal publishes a terminal lifecycle message for a session that
// is still current in this manager. Unlike broadcastIfCurrent it does not
// require a live integrity fence: after a lease loss progress payloads stay
// suppressed, but the terminal status itself must still reach push consumers
// so live views do not show a dead session running forever.
func (m *BrowserReplayManager) broadcastTerminal(messageType string, session *BrowserReplaySession, eventIndex int, event *engine.ReplayActionEvent, report *engine.ReplayReport) bool {
	return m.withCurrentSession(session, func() {
		m.broadcast(messageType, session, eventIndex, event, report)
	})
}

func (m *BrowserReplayManager) handleReplayProgress(ctx context.Context, session *BrowserReplaySession, event engine.ReplayProgressEvent) {
	if event.Type != engine.ReplayProgressEventRun {
		return
	}
	if !m.withCurrentWorkerSession(session, func() {
		session.mu.Lock()
		session.CurrentEventIndex = event.EventIndex
		if event.Event != nil {
			cloned := cloneBrowserReplayEvent(*event.Event)
			session.CurrentEvent = &cloned
			if event.EventIndex >= 0 {
				for len(session.Events) <= event.EventIndex {
					session.Events = append(session.Events, engine.ReplayActionEvent{})
				}
				session.Events[event.EventIndex] = cloned
			} else {
				session.Events = append(session.Events, cloned)
			}
		}
		session.mu.Unlock()
		m.broadcast("browser_replay_event", session, event.EventIndex, event.Event, nil)
	}) {
		return
	}
	_ = m.waitWhilePaused(ctx, session)
}

func (m *BrowserReplayManager) finishWorker(session *BrowserReplaySession) {
	if session == nil {
		return
	}
	session.mu.Lock()
	if session.workerFinished {
		session.mu.Unlock()
		return
	}
	session.workerFinished = true
	retired := session.retired
	done := session.workerDone
	releaseID := session.releaseID
	session.mu.Unlock()

	if releaseID != nil {
		releaseID()
	}
	if retired {
		session.reservation.releaseRetainedID()
	}
	if done != nil {
		close(done)
	}
}

// workerHasFinishedLocked treats a session without a worker channel as an
// already-materialized terminal record. Production sessions always have a
// workerDone channel; the nil case preserves pruning for restored/test records
// that have no live goroutine to drain. An abandoned worker counts as finished:
// its slot and lease were already force-released by the backstop, so the
// session must remain evictable.
func (s *BrowserReplaySession) workerHasFinishedLocked() bool {
	return s == nil || s.workerFinished || s.workerAbandoned || s.workerDone == nil
}

func (m *BrowserReplayManager) broadcast(messageType string, session *BrowserReplaySession, eventIndex int, event *engine.ReplayActionEvent, report *engine.ReplayReport) {
	if m.hub == nil || session == nil {
		return
	}
	sessionPayload := session.snapshot()
	message := map[string]any{
		"type":              messageType,
		"replay_session_id": sessionPayload.ID,
		"script_id":         sessionPayload.ScriptID,
		"project_id":        sessionPayload.ProjectID,
		"timestamp":         time.Now(),
		"session":           sessionPayload,
	}
	if eventIndex >= 0 {
		message["event_index"] = eventIndex
	}
	if event != nil {
		message["event_id"] = event.EventID
		message["event"] = event
	}
	if report != nil {
		message["report"] = report
	}
	m.hub.BroadcastJSON(message)
}

func (s *BrowserReplaySession) snapshot() *BrowserReplaySession {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshotLocked()
}

func (s *BrowserReplaySession) snapshotLocked() *BrowserReplaySession {
	clone := &BrowserReplaySession{
		ID:                s.ID,
		Mode:              s.Mode,
		ScriptID:          s.ScriptID,
		ProjectID:         s.ProjectID,
		Status:            s.Status,
		Group:             s.Group,
		Protocol:          s.Protocol,
		RuntimeProfile:    cloneEngineReplayRuntimeProfile(&s.RuntimeProfile),
		Preflight:         cloneReplayPreflight(s.Preflight),
		StartedAt:         cloneReplaySessionTime(s.StartedAt),
		CompletedAt:       cloneReplaySessionTime(s.CompletedAt),
		CurrentEventIndex: s.CurrentEventIndex,
		FinalReport:       cloneBrowserReplayReport(s.FinalReport),
		Error:             s.Error,
	}
	if s.CurrentEvent != nil {
		current := cloneBrowserReplayEvent(*s.CurrentEvent)
		clone.CurrentEvent = &current
	}
	if len(s.Events) > 0 {
		clone.Events = make([]engine.ReplayActionEvent, len(s.Events))
		for i, event := range s.Events {
			clone.Events[i] = cloneBrowserReplayEvent(event)
		}
	}
	return clone
}

func (s *BrowserReplaySession) stateChangedChanLocked() <-chan struct{} {
	if s.stateChanged == nil {
		s.stateChanged = make(chan struct{})
	}
	return s.stateChanged
}

func (s *BrowserReplaySession) signalStateChangedLocked() {
	ch := s.stateChanged
	if ch == nil {
		s.stateChanged = make(chan struct{})
		return
	}
	close(ch)
	s.stateChanged = make(chan struct{})
}

func cloneBrowserReplayReport(report *engine.ReplayReport) *engine.ReplayReport {
	if report == nil {
		return nil
	}
	cloned := *report
	cloned.FinalVariables = cloneBrowserStringMap(report.FinalVariables)
	cloned.RuntimeProfile = cloneEngineReplayRuntimeProfile(&report.RuntimeProfile)
	cloned.ArtifactManifest = cloneBrowserReplayArtifactManifest(report.ArtifactManifest)
	cloned.DiagnosisSummary = cloneBrowserReplayDiagnosisSummary(report.DiagnosisSummary)
	cloned.RepairSuggestions = cloneBrowserReplayRepairSuggestions(report.RepairSuggestions)
	if len(report.Events) > 0 {
		cloned.Events = make([]engine.ReplayActionEvent, len(report.Events))
		for i, event := range report.Events {
			cloned.Events[i] = cloneBrowserReplayEvent(event)
		}
	}
	if len(report.Snapshots) > 0 {
		cloned.Snapshots = make([]*protocol.ErrorSnapshot, len(report.Snapshots))
		for i, snapshot := range report.Snapshots {
			cloned.Snapshots[i] = cloneBrowserErrorSnapshot(snapshot)
		}
	}
	return &cloned
}

func cloneBrowserErrorSnapshot(snapshot *protocol.ErrorSnapshot) *protocol.ErrorSnapshot {
	if snapshot == nil {
		return nil
	}
	cloned := *snapshot
	cloned.RequestHeaders = cloneBrowserStringMap(snapshot.RequestHeaders)
	cloned.ResponseHeaders = cloneBrowserStringMap(snapshot.ResponseHeaders)
	cloned.ConsoleEntries = cloneBrowserConsoleEntries(snapshot.ConsoleEntries)
	cloned.NetworkEntries = cloneBrowserNetworkEntries(snapshot.NetworkEntries)
	cloned.DOMSummary = cloneBrowserDOMSummary(snapshot.DOMSummary)
	return &cloned
}

func cloneBrowserReplayEvent(event engine.ReplayActionEvent) engine.ReplayActionEvent {
	cloned := event
	// Raw correlation evidence is an internal, single-use value and must never
	// cross a public session snapshot boundary.
	cloned.CorrelationEvidence = nil
	cloned.Headers = cloneBrowserStringMap(event.Headers)
	cloned.Extractions = cloneBrowserStringMap(event.Extractions)
	cloned.Console = cloneBrowserConsoleEntries(event.Console)
	cloned.Network = cloneBrowserNetworkEntries(event.Network)
	cloned.DOMSummary = cloneBrowserDOMSummary(event.DOMSummary)
	cloned.ClientMetrics = cloneBrowserClientMetrics(event.ClientMetrics)
	cloned.ProtocolEvidence = protocol.BoundProtocolEvidence(event.ProtocolEvidence)
	cloned.Diagnosis = cloneBrowserReplayDiagnosis(event.Diagnosis)
	cloned.RepairSuggestions = cloneBrowserReplayRepairSuggestions(event.RepairSuggestions)
	if event.RequestIndex != nil {
		requestIndex := *event.RequestIndex
		cloned.RequestIndex = &requestIndex
	}
	if event.Iteration != nil {
		iteration := *event.Iteration
		cloned.Iteration = &iteration
	}
	if len(event.StepPath) > 0 {
		cloned.StepPath = append([]string(nil), event.StepPath...)
	}
	if len(event.IterationPath) > 0 {
		cloned.IterationPath = append([]int(nil), event.IterationPath...)
	}
	if event.Snapshot != nil {
		snapshot := *event.Snapshot
		snapshot.RequestHeaders = cloneBrowserStringMap(event.Snapshot.RequestHeaders)
		snapshot.ResponseHeaders = cloneBrowserStringMap(event.Snapshot.ResponseHeaders)
		snapshot.ConsoleEntries = cloneBrowserConsoleEntries(event.Snapshot.ConsoleEntries)
		snapshot.NetworkEntries = cloneBrowserNetworkEntries(event.Snapshot.NetworkEntries)
		snapshot.DOMSummary = cloneBrowserDOMSummary(event.Snapshot.DOMSummary)
		cloned.Snapshot = &snapshot
	}
	return cloned
}

func cloneBrowserReplayArtifactManifest(manifest *engine.ReplayArtifactManifest) *engine.ReplayArtifactManifest {
	if manifest == nil {
		return nil
	}
	cloned := *manifest
	cloned.RuntimeProfile = cloneEngineReplayRuntimeProfile(&manifest.RuntimeProfile)
	if len(manifest.DataSources) > 0 {
		cloned.DataSources = append([]string(nil), manifest.DataSources...)
	}
	if len(manifest.SeedVariables) > 0 {
		cloned.SeedVariables = append([]string(nil), manifest.SeedVariables...)
	}
	if len(manifest.ProtocolArtifacts) > 0 {
		cloned.ProtocolArtifacts = make(map[string]int, len(manifest.ProtocolArtifacts))
		for key, value := range manifest.ProtocolArtifacts {
			cloned.ProtocolArtifacts[key] = value
		}
	}
	return &cloned
}

func cloneReplaySessionTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneBrowserReplayDiagnosis(diagnosis *engine.ReplayDiagnosis) *engine.ReplayDiagnosis {
	if diagnosis == nil {
		return nil
	}
	cloned := *diagnosis
	if len(diagnosis.Signals) > 0 {
		cloned.Signals = append([]string(nil), diagnosis.Signals...)
	}
	return &cloned
}

func cloneBrowserReplayRepairSuggestions(suggestions []engine.ReplayRepairSuggestion) []engine.ReplayRepairSuggestion {
	if len(suggestions) == 0 {
		return nil
	}
	cloned := make([]engine.ReplayRepairSuggestion, len(suggestions))
	for i, suggestion := range suggestions {
		cloned[i] = suggestion
		if len(suggestion.Fields) > 0 {
			cloned[i].Fields = cloneBrowserStringMap(suggestion.Fields)
		}
	}
	return cloned
}

func cloneBrowserReplayDiagnosisSummary(summary *engine.ReplayDiagnosisSummary) *engine.ReplayDiagnosisSummary {
	if summary == nil {
		return nil
	}
	cloned := *summary
	if len(summary.Categories) > 0 {
		cloned.Categories = make(map[string]int, len(summary.Categories))
		for key, value := range summary.Categories {
			cloned.Categories[key] = value
		}
	}
	return &cloned
}

func cloneBrowserStringMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(input))
	for key, value := range input {
		cloned[key] = value
	}
	return cloned
}

func cloneBrowserConsoleEntries(entries []protocol.ConsoleEntry) []protocol.ConsoleEntry {
	if len(entries) == 0 {
		return nil
	}
	cloned := make([]protocol.ConsoleEntry, len(entries))
	copy(cloned, entries)
	return cloned
}

func cloneBrowserNetworkEntries(entries []protocol.NetworkEntry) []protocol.NetworkEntry {
	return protocol.CloneNetworkEntries(entries)
}

func cloneBrowserDOMSummary(summary *protocol.DOMSummary) *protocol.DOMSummary {
	if summary == nil {
		return nil
	}
	cloned := *summary
	if len(summary.VisibleText) > 0 {
		cloned.VisibleText = append([]string(nil), summary.VisibleText...)
	}
	return &cloned
}

func cloneBrowserClientMetrics(metrics *protocol.ClientMetrics) *protocol.ClientMetrics {
	if metrics == nil {
		return nil
	}
	cloned := *metrics
	return &cloned
}

func (m *BrowserReplayManager) waitWhilePaused(ctx context.Context, session *BrowserReplaySession) bool {
	for {
		session.mu.Lock()
		status := session.Status
		stateChanged := session.stateChangedChanLocked()
		session.mu.Unlock()

		switch status {
		case BrowserReplayPaused:
			select {
			case <-ctx.Done():
				return false
			case <-stateChanged:
			}
		case BrowserReplayStopped, BrowserReplayFailed, BrowserReplayCompleted:
			return false
		default:
			return true
		}
	}
}
