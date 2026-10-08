package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
)

const (
	ScriptDebugQueued    = "queued"
	ScriptDebugRunning   = "running"
	ScriptDebugPaused    = "paused"
	ScriptDebugCompleted = "completed"
	ScriptDebugFailed    = "failed"
	ScriptDebugStopped   = "stopped"

	defaultScriptDebugCorrelationEvidenceGlobalBytes  = 64 << 20
	defaultScriptDebugCorrelationEvidenceProjectBytes = 24 << 20
	maxScriptDebugLogEntries                          = 5000
	maxScriptDebugLogBytes                            = 32 << 20
)

var (
	errScriptDebugCorrelationSessionNotFound = errors.New("replay session not found")
	errScriptDebugCorrelationScriptMismatch  = errors.New("replay session belongs to a different script")
	errScriptDebugCorrelationGraphMismatch   = errors.New("replay session belongs to a different action graph")
	errScriptDebugCorrelationSessionActive   = errors.New("replay session has not completed")
	errScriptDebugCorrelationUnavailable     = errors.New("raw replay correlation evidence is unavailable")
	errScriptDebugSessionIDConflict          = errors.New("script debug session id is already in use")
	errScriptDebugSessionCapacity            = errors.New("script debug session capacity is exhausted")
	errScriptDebugManagerClosed              = errors.New("script debug manager is closed")
)

type ScriptDebugBreakpoint struct {
	Disabled bool `json:"disabled,omitempty"`
	// HitCount pauses on exactly this eligible visit (1-based). Zero means every visit.
	HitCount int `json:"hit_count,omitempty"`
	// Iteration targets the innermost loop, zero-based. Nil matches any iteration.
	Iteration *int `json:"iteration,omitempty"`
	// Once is a temporary run-to breakpoint; it is removed when reached.
	Once        bool   `json:"once,omitempty"`
	Phase       string `json:"phase"`
	ActionIndex int    `json:"action_index"`
	// Optional sub-request / sub-step targeting within a container action. When
	// both are absent the breakpoint is a whole-action breakpoint (stops once, at
	// the first executed leaf). RequestIndex targets an http_sequence child;
	// StepPath targets a control-flow leaf by its static path (fires every loop
	// iteration). RequestIndex is also expressible as StepPath ["requests[i]"].
	RequestIndex *int     `json:"request_index,omitempty"`
	StepPath     []string `json:"step_path,omitempty"`
}

type ScriptDebugLogEntry struct {
	ID          string    `json:"id"`
	Timestamp   time.Time `json:"timestamp"`
	Level       string    `json:"level"`
	Category    string    `json:"category"`
	Message     string    `json:"message"`
	Raw         string    `json:"raw,omitempty"`
	Phase       string    `json:"phase,omitempty"`
	ActionIndex int       `json:"action_index,omitempty"`
	ActionName  string    `json:"action_name,omitempty"`
}

type ScriptDebugSession struct {
	SessionIncarnation         string                      `json:"session_incarnation"`
	StateRevision              string                      `json:"state_revision"`
	RuntimeState               ScriptDebugRuntimeState     `json:"runtime_state"`
	RuntimeEvidenceUnavailable bool                        `json:"runtime_evidence_unavailable,omitempty"`
	ID                         string                      `json:"id"`
	Mode                       string                      `json:"mode,omitempty"`
	ScriptID                   string                      `json:"script_id,omitempty"`
	ProjectID                  string                      `json:"project_id,omitempty"`
	Status                     string                      `json:"status"`
	Group                      string                      `json:"group"`
	Protocol                   string                      `json:"protocol"`
	LogLevel                   string                      `json:"log_level"`
	RuntimeProfile             engine.ReplayRuntimeProfile `json:"runtime_profile,omitempty"`
	Preflight                  *ReplayPreflight            `json:"preflight,omitempty"`
	StartedAt                  *time.Time                  `json:"started_at,omitempty"`
	CompletedAt                *time.Time                  `json:"completed_at,omitempty"`
	CurrentEventIndex          int                         `json:"current_event_index"`
	CurrentAction              *engine.ReplayDebugAction   `json:"current_action,omitempty"`
	CurrentEvent               *engine.ReplayActionEvent   `json:"current_event,omitempty"`
	Breakpoints                []ScriptDebugBreakpoint     `json:"breakpoints,omitempty"`
	BreakOnFailure             bool                        `json:"break_on_failure"`
	Events                     []engine.ReplayActionEvent  `json:"events,omitempty"`
	DroppedEventCount          int                         `json:"dropped_event_count,omitempty"`
	Logs                       []ScriptDebugLogEntry       `json:"logs,omitempty"`
	RawOutput                  []string                    `json:"raw_output,omitempty"`
	logBytes                   int
	FinalReport                *engine.ReplayReport `json:"report,omitempty"`
	Error                      string               `json:"error,omitempty"`
	// ConditionEvidence carries the most recent evaluated operands/result for each
	// control_if / control_loop container, keyed by conditionEvidenceKey, so the
	// debugger can show why a branch was taken or a loop pass ran.
	ConditionEvidence map[string]engine.ConditionEvidence `json:"condition_evidence,omitempty"`

	mu                            sync.RWMutex
	cancel                        context.CancelFunc
	breakpointHits                map[int]int
	stepOnce                      bool
	stepMode                      string        // "" | over | loop_end while a step-through is in flight
	stepBoundary                  *stepBoundary // resume-through subtree for stepMode
	lastActivityAt                time.Time
	terminalAt                    *time.Time
	stateChanged                  chan struct{}
	correlationObservations       []correlate.Observation
	correlationObservationIDs     map[int]struct{}
	correlationEvidenceBytes      int
	correlationEvidenceErr        string
	correlationActionGraphDigest  string
	correlationEvidenceRetainedAt time.Time
	reservation                   *replaySessionLeaseSignal
	releaseID                     func()
	workerDone                    chan struct{}
	workerFinished                bool
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
	retainedEventJSONBytes   int
	eventJSONBytes           []int
	executionContext         context.Context
	workerContext            context.Context
	heldBoundary             *scriptDebugHeldBoundary
	activeRuntimeHook        *scriptDebugHeldBoundary
	runtimeRevision          uint64
	runtimeRevisionExhausted bool
	runtimeUnavailableReason string
}

type ScriptDebugManager struct {
	hub                             *Hub
	sessions                        map[string]*ScriptDebugSession
	mu                              sync.RWMutex
	onComplete                      replayCompletionHook
	reserveID                       replaySessionIDReserver
	retention                       replaySessionRetentionConfig
	correlationEvidenceGlobalBytes  int
	correlationEvidenceProjectBytes int
	// correlationBudgetMu serializes correlation-evidence budget enforcement,
	// which scans every session. It is always acquired after m.mu (read) and
	// before any session mutex, and never blocks progress paths that only need
	// the manager read lock.
	correlationBudgetMu sync.Mutex
	janitorCancel       context.CancelFunc
	closed              bool
	closeOnce           sync.Once
	closeDone           chan struct{}
	closeTimeout        time.Duration
}

func NewScriptDebugManager(hub *Hub) *ScriptDebugManager {
	return &ScriptDebugManager{
		hub:       hub,
		sessions:  make(map[string]*ScriptDebugSession),
		retention: defaultReplaySessionRetentionConfig(),
		correlationEvidenceGlobalBytes: firstConfiguredPositiveInt(defaultScriptDebugCorrelationEvidenceGlobalBytes,
			"APEXLOAD_REPLAY_CORRELATION_EVIDENCE_MAX_BYTES"),
		correlationEvidenceProjectBytes: firstConfiguredPositiveInt(defaultScriptDebugCorrelationEvidenceProjectBytes,
			"APEXLOAD_REPLAY_CORRELATION_EVIDENCE_PROJECT_MAX_BYTES"),
		closeDone:    make(chan struct{}),
		closeTimeout: replaySessionCloseDrainTimeout,
	}
}

func (m *ScriptDebugManager) setCompletionHook(hook replayCompletionHook) {
	m.onComplete = hook
}

func (m *ScriptDebugManager) setSessionIDReserver(reserve replaySessionIDReserver) {
	m.reserveID = reserve
}

func (m *ScriptDebugManager) StartJanitor(ctx context.Context) {
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

func (m *ScriptDebugManager) Close() {
	if m == nil {
		return
	}
	m.closeOnce.Do(func() {
		type closeCleanup struct {
			session   *ScriptDebugSession
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
			session.invalidateRuntimeStateLocked(ScriptDebugRuntimeSessionRetired)
			session.retired = true
			if session.Status == ScriptDebugRunning || session.Status == ScriptDebugPaused {
				session.Status = ScriptDebugStopped
			}
			if replaySessionStatusTerminal(session.Status) && session.terminalAt == nil {
				terminalAt := now
				session.terminalAt = &terminalAt
			}
			session.rejectCorrelationEvidenceLocked("debugger manager closed")
			// Drop the remaining replay/debug artifacts as well. Although public
			// projections are scrubbed, the closed manager must not keep diagnostic
			// payloads alive while an uncooperative protocol drains.
			session.CurrentAction = nil
			session.CurrentEvent = nil
			session.Events = nil
			session.Logs = nil
			session.RawOutput = nil
			session.FinalReport = nil
			session.ConditionEvidence = nil
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

func (m *ScriptDebugManager) Start(scriptID string, script *dsl.Script, req ScriptDebugStartRequest) *ScriptDebugSession {
	session, _ := m.StartChecked(scriptID, script, req)
	return session
}

// StartChecked starts a session without ever replacing an existing global
// session capability and reports a collision to transport callers.
func (m *ScriptDebugManager) StartChecked(scriptID string, script *dsl.Script, req ScriptDebugStartRequest) (*ScriptDebugSession, error) {
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
	groupName := strings.TrimSpace(req.GroupName)
	if groupName == "" {
		groupName = strings.TrimSpace(script.Name) + "-debug"
		if strings.TrimSpace(script.Name) == "" {
			groupName = "script-debug"
		}
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
	actionGraphDigest := strings.TrimSpace(req.ActionGraphDigest)
	if actionGraphDigest == "" {
		actionGraphDigest, _ = scriptCorrelationActionGraphDigest(script)
	}
	session := &ScriptDebugSession{
		SessionIncarnation:           uuid.New().String(),
		executionContext:             executionCtx,
		workerContext:                workerCtx,
		runtimeUnavailableReason:     ScriptDebugRuntimeNotPaused,
		ID:                           sessionID,
		Mode:                         normalizeReplaySessionMode(req.Mode),
		ScriptID:                     scriptID,
		ProjectID:                    normalizeProjectID(script.ProjectID),
		Status:                       ScriptDebugRunning,
		Group:                        groupName,
		Protocol:                     script.Protocol,
		LogLevel:                     normalizeDebugLogLevel(req.LogLevel),
		RuntimeProfile:               replayRuntimeProfileForDebug(script, req, ignoreThinkTime),
		Preflight:                    cloneReplayPreflight(req.Preflight),
		StartedAt:                    &now,
		CurrentEventIndex:            -1,
		Breakpoints:                  normalizeDebugBreakpoints(req.Breakpoints),
		BreakOnFailure:               req.BreakOnFailure,
		cancel:                       cancel,
		lastActivityAt:               now,
		stateChanged:                 make(chan struct{}),
		correlationActionGraphDigest: actionGraphDigest,
		reservation:                  leaseSignal,
		releaseID:                    releaseReservation,
		workerDone:                   make(chan struct{}),
	}
	if req.StartPaused {
		session.Status = ScriptDebugPaused
		session.runtimeUnavailableReason = ScriptDebugRuntimePausePending
	}
	session.appendLogLocked("info", "session", "Debugger session started", "", nil)
	if req.StartPaused {
		session.appendLogLocked("info", "control", "Session is waiting at the first action", "", nil)
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
		return nil, errScriptDebugManagerClosed
	}
	_, pruneReleases = m.pruneExpiredLocked(now)
	if existing := m.sessions[sessionID]; existing != nil {
		// IDs are global event/evidence capabilities. Never let a colliding start
		// cancel or substitute an established session from another project.
		m.mu.Unlock()
		cancel()
		releaseExecutionContext()
		releaseReservation()
		return nil, errScriptDebugSessionIDConflict
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
		return nil, errScriptDebugSessionCapacity
	}
	m.mu.Unlock()

	m.broadcastIfCurrent("script_debug_started", session, -1, nil, nil, nil)
	if req.StartPaused {
		m.broadcastIfCurrent("script_debug_paused", session, -1, nil, nil, nil)
	}
	go m.reconcileWorkerCancellation(ctx, executionCtx, session)

	go func() {
		defer m.finishWorker(session)
		defer releaseExecutionContext()
		defer cancel()
		report, err := engine.ReplayScript(ctx, script, engine.ReplayOptions{
			ReplaySessionID: sessionID,
			GroupName:       groupName,
			RuntimeProfile:  &session.RuntimeProfile,
			Variables:       req.Variables,
			RedactionRules:  req.RedactionRules,
			SecretResolver:  req.SecretResolver,
			IgnoreThinkTime: ignoreThinkTime,
			DebugController: scriptDebugController{manager: m, session: session},
			// Debug sessions offer no HAR: one the script's own capture policy
			// recorded is retired rather than left in the HAR directory.
			DiscardHAR: retireReplayHARArtifacts,
			OnProgress: func(event engine.ReplayProgressEvent) {
				m.handleReplayProgress(ctx, session, event)
			},
		})
		if report != nil {
			retireReplayHARArtifacts(report.TakeHARArtifacts())
		}

		completedAt := m.retention.clock()
		messageType := "script_debug_completed"
		leaseErr := leaseFailure.retainForTerminal(m.retention.terminalTTL)
		fenceErr := replayExecutionFenceError(executionCtx, leaseFailure)
		terminalFenceEstablished := leaseErr == nil && fenceErr == nil
		eventIndex := -1
		abandoned := false
		var terminalReport *engine.ReplayReport
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
			session.invalidateRuntimeStateLocked(ScriptDebugRuntimeSessionTerminal)
			session.terminalFenceEstablished = terminalFenceEstablished
			if report != nil && report.RuntimeEvidenceUnavailable {
				session.omitRuntimeEvidenceLocked()
			}
			if report != nil && terminalFenceEstablished {
				session.FinalReport = report
				session.Events = nil
				session.CurrentEvent = nil
				session.retainedEventJSONBytes = 0
				session.eventJSONBytes = nil
				if report.DroppedEventCount > session.DroppedEventCount {
					session.DroppedEventCount = report.DroppedEventCount
				}
				terminalReport = session.FinalReport
				session.Error = report.Error
			} else if !terminalFenceEstablished {
				session.FinalReport = nil
			}
			if !terminalFenceEstablished {
				session.Status = ScriptDebugFailed
				if fenceErr != nil {
					session.Error = fenceErr.Error()
				} else {
					session.Error = leaseErr.Error()
				}
				session.rejectCorrelationEvidenceLocked("debugger session lost its replay-session integrity fence")
				messageType = "script_debug_failed"
				session.appendLogLocked("error", "session", session.Error, "", nil)
			} else if session.Status == ScriptDebugStopped {
				if report != nil {
					report.Stopped = true
					report.Success = false
					// A deliberate stop is not an execution error. Keep action
					// failures in Events for inspection.
					report.Error = ""
				}
				session.Error = ""

				session.rejectCorrelationEvidenceLocked("debugger session was stopped before replay completion")
				messageType = "script_debug_stopped"
				session.appendLogLocked("warn", "control", "Debugger session stopped", "", nil)
			} else if err != nil && !errors.Is(err, context.Canceled) {
				session.Status = ScriptDebugFailed
				session.Error = err.Error()
				messageType = "script_debug_failed"
				session.appendLogLocked("error", "session", err.Error(), "", nil)
			} else if report != nil && !report.Success {
				session.Status = ScriptDebugFailed
				messageType = "script_debug_failed"
				if report.Error != "" {
					session.appendLogLocked("error", "session", report.Error, "", nil)
				}
			} else if session.Status != ScriptDebugStopped {
				session.Status = ScriptDebugCompleted
				session.appendLogLocked("info", "session", "Debugger session completed", "", nil)
			}
			if replaySessionStatusTerminal(session.Status) {
				// A stop request can precede worker completion by an arbitrary
				// amount. Start terminal retention when the report/evidence
				// boundary is finalized so readers receive the full TTL.
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
			if m.broadcastTerminal(messageType, session, eventIndex, nil, nil, terminalReport) && m.onComplete != nil && report != nil {
				if messageType != "script_debug_stopped" {
					m.onComplete(executionCtx, session.ProjectID, script, report)
				}
			}
		} else {
			// The integrity fence was lost: keep the report payload suppressed but
			// still push the terminal status so WebSocket-only consumers observe
			// the failure instead of a session that appears to run forever, then
			// retire the local capability before finishWorker releases its global
			// reservation.
			m.broadcastTerminal(messageType, session, eventIndex, nil, nil, nil)
			m.retireUnfencedSession(session)
		}
	}()

	return session.snapshot(), nil
}

// reconcileWorkerCancellation makes execution-epoch and replay-session lease
// loss visible even when a protocol adapter ignores context cancellation. The
// worker remains capacity-counted during a bounded grace period, then the
// existing abandon backstop force-releases its local slot and durable lease.
// Fence loss also rejects private correlation evidence and any final report
// that raced into the public session snapshot.
func (m *ScriptDebugManager) reconcileWorkerCancellation(workerCtx, executionCtx context.Context, session *ScriptDebugSession) {
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
		live := session.Status == ScriptDebugRunning || session.Status == ScriptDebugPaused
		if !live && (session.terminalFenceEstablished || fenceErr == nil) {
			// Normal completion and the explicit/idle/Close paths all establish
			// terminal state before canceling the worker context. Once a terminal
			// fence is retained, a later execution-epoch release cannot invalidate
			// the already-fenced report.
			return
		}
		failure := fenceErr
		if failure == nil {
			failure = workerCtx.Err()
		}
		if failure == nil {
			failure = context.Canceled
		}
		session.Status = ScriptDebugFailed
		session.invalidateRuntimeStateLocked(ScriptDebugRuntimeContextCanceled)
		session.Error = failure.Error()
		session.FinalReport = nil
		session.stepOnce = false
		session.clearStepLocked()
		session.rejectCorrelationEvidenceLocked("debugger session lost its execution integrity fence")
		session.appendLogLocked("error", "session", session.Error, "", nil)
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
		m.broadcastTerminal("script_debug_failed", session, eventIndex, nil, nil, nil)
	}
}

func (m *ScriptDebugManager) PruneExpired() int {
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
func (m *ScriptDebugManager) pruneExpiredLocked(now time.Time) (int, []*replaySessionLeaseSignal) {
	removed := 0
	var releases []*replaySessionLeaseSignal
	for id, session := range m.sessions {
		session.mu.Lock()
		m.cancelIdleRunningSessionLocked(session, now)
		if m.shouldPruneSessionLocked(session, now) {
			m.cancelSessionLocked(session, now, "Debugger session pruned after retention window")
			session.retired = true
			session.invalidateRuntimeStateLocked(ScriptDebugRuntimeSessionRetired)
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
func (m *ScriptDebugManager) enforceMaxSessionsLocked(now time.Time, protectedID string) (int, []*replaySessionLeaseSignal) {
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
		m.cancelSessionLocked(session, now, "Debugger session pruned to enforce session cap")
		session.retired = true
		session.invalidateRuntimeStateLocked(ScriptDebugRuntimeSessionRetired)
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

func (m *ScriptDebugManager) oldestTerminalSessionLocked(protectedID string) (string, *ScriptDebugSession) {
	var selectedID string
	var selected *ScriptDebugSession
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

func (m *ScriptDebugManager) cancelIdleRunningSessionLocked(session *ScriptDebugSession, now time.Time) {
	if session == nil || m.retention.idleCancel <= 0 || (session.Status != ScriptDebugRunning && session.Status != ScriptDebugPaused) {
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
	m.cancelSessionLocked(session, now, "Debugger session stopped after idle timeout")
}

func (m *ScriptDebugManager) shouldPruneSessionLocked(session *ScriptDebugSession, now time.Time) bool {
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

func (m *ScriptDebugManager) cancelSessionLocked(session *ScriptDebugSession, now time.Time, reason string) {
	if session == nil {
		return
	}
	session.invalidateRuntimeStateLocked(ScriptDebugRuntimeSessionTerminal)
	if session.Status == ScriptDebugRunning || session.Status == ScriptDebugPaused {
		// A stopped status does not prove the replay goroutine has exited. Preserve
		// the active capacity lease until the worker completion path runs so an
		// adapter that ignores cancellation cannot be hidden behind a tombstone.
		if err := session.reservation.failureOrExpired(time.Now()); err != nil {
			session.Status = ScriptDebugFailed
			session.Error = err.Error()
			session.rejectCorrelationEvidenceLocked("debugger session lost its replay-session integrity fence")
		} else {
			session.Status = ScriptDebugStopped
			session.rejectCorrelationEvidenceLocked("debugger session was stopped before replay completion")
		}
		session.stepOnce = false
		session.clearStepLocked()
		if reason != "" {
			session.appendLogLocked("warn", "control", reason, "", nil)
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
func (m *ScriptDebugManager) scheduleWorkerAbandonBackstopLocked(session *ScriptDebugSession) {
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
func (m *ScriptDebugManager) abandonUnresponsiveWorker(session *ScriptDebugSession, grace time.Duration) {
	session.mu.Lock()
	if session.workerFinished || session.workerAbandoned {
		session.mu.Unlock()
		return
	}
	session.workerAbandoned = true
	session.invalidateRuntimeStateLocked(ScriptDebugRuntimeSessionRetired)
	session.Status = ScriptDebugFailed
	session.Error = "replay worker ignored cancellation past the abandon grace period; session orphaned and its lease force-released"
	session.rejectCorrelationEvidenceLocked("debugger session was orphaned after its worker ignored cancellation")
	session.appendLogLocked("error", "session", session.Error, "", nil)
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

	slog.Error("script debug worker ignored cancellation past the grace period; force-releasing its session slot and lease",
		"replay_session_id", session.ID, "project_id", session.ProjectID, "grace", grace.String())
	// Fence first so the wedged worker cannot publish or retain afterwards.
	reservation.fail(errReplaySessionLeaseLost)
	m.broadcastTerminal("script_debug_failed", session, -1, nil, nil, nil)

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

func sessionRetentionSortTime(startedAt *time.Time, terminalAt *time.Time, lastActivityAt time.Time) time.Time {
	if terminalAt != nil {
		return *terminalAt
	}
	if !lastActivityAt.IsZero() {
		return lastActivityAt
	}
	if startedAt != nil {
		return *startedAt
	}
	return time.Time{}
}

func (m *ScriptDebugManager) handleReplayProgress(ctx context.Context, session *ScriptDebugSession, event engine.ReplayProgressEvent) {
	unavailableSignal := event.RuntimeEvidenceUnavailable || (event.Report != nil && event.Report.RuntimeEvidenceUnavailable)
	if session == nil || (ctx.Err() != nil && !unavailableSignal) {
		return
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed || m.sessions[session.ID] != session || session.reservation.failureOrExpired(time.Now()) != nil {
		return
	}
	session.mu.Lock()
	newlyUnavailable := unavailableSignal && !session.RuntimeEvidenceUnavailable && !session.workerAbandoned
	if unavailableSignal && !session.workerAbandoned {
		session.omitRuntimeEvidenceLocked()
	}
	unavailable := session.RuntimeEvidenceUnavailable
	abandoned := session.workerAbandoned
	if !abandoned {
		// Engine callbacks are authoritative liveness evidence for WebSocket-only
		// clients as well as the dashboard's polling client.
		session.lastActivityAt = m.retention.clock()
	}
	session.mu.Unlock()
	if abandoned {
		return
	}
	if ctx.Err() != nil {
		// Draining cancelled workers can discover a classification failure.
		// Admit only that omission signal; never restore cancelled progress.
		if newlyUnavailable {
			m.broadcast("script_debug_evidence_unavailable", session, event.EventIndex, nil, nil, nil)
		}
		return
	}
	if unavailable {
		// A later callback cannot restore detail retained under an older matcher.
		event.Action = cloneScriptDebugAction(event.Action)
		engine.OmitUnavailableReplayDebugAction(event.Action)
		event.Condition = nil
		if event.Event != nil {
			event.Event = omitUnavailableScriptDebugEvent(event.Event)
			if event.Event == nil {
				session.mu.Lock()
				session.DroppedEventCount++
				session.mu.Unlock()
			}
		}
	}
	switch event.Type {
	case engine.ReplayProgressActionStarted:
		session.mu.Lock()
		session.clearActiveRuntimeCandidateLocked()
		session.invalidateRuntimeStateLocked(ScriptDebugRuntimeNotPaused)
		if event.Action != nil {
			actionCopy := *event.Action
			session.CurrentAction = &actionCopy
			session.appendLogLocked("debug", "action", fmt.Sprintf("Started %s[%d] %s", actionCopy.Phase, actionCopy.ActionIndex, actionCopy.ActionName)+debugLeafSuffix(actionCopy), "", nil)
		}
		session.mu.Unlock()
		m.broadcast("script_debug_action_started", session, event.EventIndex, nil, event.Action, nil)
	case engine.ReplayProgressActionCompleted, engine.ReplayProgressActionFailed:
		messageType := "script_debug_action_completed"
		if event.Type == engine.ReplayProgressActionFailed {
			messageType = "script_debug_action_failed"
		}
		if event.Event != nil {
			m.broadcast(messageType, session, event.EventIndex, event.Event, event.Action, nil)
		} else if newlyUnavailable {
			m.broadcast("script_debug_evidence_unavailable", session, event.EventIndex, nil, nil, nil)
		}
	case engine.ReplayProgressEventRun:
		var retainedEvent *engine.ReplayActionEvent
		session.mu.Lock()
		if event.Event != nil {
			cloned := retainReplaySessionEvent(
				&session.Events,
				&session.eventJSONBytes,
				&session.retainedEventJSONBytes,
				&session.DroppedEventCount,
				*event.Event,
			)
			session.CurrentEventIndex = event.EventIndex
			session.CurrentEvent = &cloned
			retainedEvent = &cloned
			level := "info"
			if !cloned.Success {
				level = "error"
			}
			raw := formatDebugEventRawForLogLevel(session.LogLevel, cloned)
			session.appendLogLocked(level, "action", debugActionResultMessage(cloned), raw, &cloned)
		}
		shouldPauseAfterStep := session.stepOnce && session.Status == ScriptDebugRunning
		if shouldPauseAfterStep {
			session.stepOnce = false
			session.Status = ScriptDebugPaused
			session.invalidateRuntimeStateLocked(ScriptDebugRuntimePausePending)
			session.appendLogLocked("info", "control", "Step completed; debugger paused", "", nil)
		}
		session.mu.Unlock()
		m.broadcast("script_debug_event", session, event.EventIndex, retainedEvent, nil, nil)
		if shouldPauseAfterStep {
			m.broadcast("script_debug_paused", session, event.EventIndex, nil, nil, nil)
		}
	case engine.ReplayProgressCondition:
		if unavailable {
			// The first unavailable callback can be a condition delta. Publish the
			// admission flag even though its operands must be omitted.
			m.broadcast("script_debug_condition", session, event.EventIndex, nil, event.Action, nil)
			break
		}
		if event.Action != nil && event.Condition != nil {
			session.mu.Lock()
			if session.ConditionEvidence == nil {
				session.ConditionEvidence = make(map[string]engine.ConditionEvidence)
			}
			key := conditionEvidenceKey(event.Action.Phase, event.Action.ActionIndex, event.Action.StepPath)
			session.ConditionEvidence[key] = *event.Condition
			session.mu.Unlock()
			m.broadcast("script_debug_condition", session, event.EventIndex, nil, event.Action, nil)
		}
	default:
		// Final refresh can fail before deferred protocol cleanup returns.
		// WebSocket-only clients must invalidate prior details immediately.
		if newlyUnavailable {
			m.broadcast("script_debug_evidence_unavailable", session, event.EventIndex, nil, nil, nil)
		}
	}
}

// LookupProjectID returns the current capability owner without extending the
// session's idle lifetime. It is intended for authorization middleware.
func (m *ScriptDebugManager) LookupProjectID(sessionID string) (string, bool) {
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

func (m *ScriptDebugManager) Get(sessionID string) (*ScriptDebugSession, bool) {
	return m.getForProject(sessionID, "")
}

func (m *ScriptDebugManager) GetForProject(sessionID, projectID string) (*ScriptDebugSession, bool) {
	return m.getForProject(sessionID, projectID)
}

func (m *ScriptDebugManager) getForProject(sessionID, projectID string) (*ScriptDebugSession, bool) {
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
	session.reconcileRuntimeStateLocked()
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

// CorrelationObservations returns a detached copy of the bounded raw replay
// evidence for a completed session. The evidence is intentionally unavailable
// through Get, snapshots, exports, and broadcasts; this is the sole consumer
// boundary, and it binds the caller to the authorized project and stored script
// id before returning any sensitive values.
func (m *ScriptDebugManager) CorrelationObservations(sessionID, projectID, scriptID, actionGraphDigest string) ([]correlate.Observation, error) {
	if m == nil {
		return nil, errScriptDebugCorrelationSessionNotFound
	}
	now := m.retention.clock()
	var pruneReleases []*replaySessionLeaseSignal
	defer func() { releaseRetainedReplaySessionSignals(pruneReleases) }()
	m.mu.Lock()
	_, pruneReleases = m.pruneExpiredLocked(now)
	session := m.sessions[strings.TrimSpace(sessionID)]
	if session == nil {
		m.mu.Unlock()
		return nil, errScriptDebugCorrelationSessionNotFound
	}

	session.mu.Lock()
	defer func() {
		session.mu.Unlock()
		m.mu.Unlock()
	}()
	// A replay session id is a body-referenced capability, not part of the route
	// authorization lookup. Mask a foreign project exactly like an unknown id,
	// and do so before checking script or graph identity so those fields cannot
	// become a cross-project existence oracle.
	if normalizeProjectID(session.ProjectID) != normalizeProjectID(projectID) {
		return nil, errScriptDebugCorrelationSessionNotFound
	}
	if session.reservation.failureOrExpired(time.Now()) != nil {
		return nil, errScriptDebugCorrelationSessionNotFound
	}
	session.lastActivityAt = now
	if strings.TrimSpace(session.ScriptID) != strings.TrimSpace(scriptID) {
		return nil, errScriptDebugCorrelationScriptMismatch
	}
	if strings.TrimSpace(actionGraphDigest) == "" || session.correlationActionGraphDigest == "" || session.correlationActionGraphDigest != strings.TrimSpace(actionGraphDigest) {
		return nil, errScriptDebugCorrelationGraphMismatch
	}
	switch session.Status {
	case ScriptDebugCompleted, ScriptDebugFailed:
		// Status alone is insufficient: raw observations are trusted only after
		// the replay has produced both terminal artifacts under the same session.
	case ScriptDebugStopped:
		return nil, fmt.Errorf("%w: debugger session was stopped before replay completion", errScriptDebugCorrelationUnavailable)
	default:
		return nil, errScriptDebugCorrelationSessionActive
	}
	if session.CompletedAt == nil || session.CompletedAt.IsZero() || session.FinalReport == nil {
		return nil, fmt.Errorf("%w: replay terminal artifacts are incomplete", errScriptDebugCorrelationUnavailable)
	}
	if session.correlationEvidenceErr != "" {
		return nil, fmt.Errorf("%w: %s", errScriptDebugCorrelationUnavailable, session.correlationEvidenceErr)
	}
	if len(session.correlationObservations) == 0 {
		return nil, errScriptDebugCorrelationUnavailable
	}
	observations := make([]correlate.Observation, len(session.correlationObservations))
	for i, observation := range session.correlationObservations {
		observations[i] = cloneCorrelationObservation(observation)
	}
	return observations, nil
}

func (m *ScriptDebugManager) Pause(sessionID string) (*ScriptDebugSession, bool, bool) {
	return m.updateStatus(sessionID, "", ScriptDebugPaused, false, "script_debug_paused", "Debugger paused")
}

func (m *ScriptDebugManager) PauseForProject(sessionID, projectID string) (*ScriptDebugSession, bool, bool) {
	return m.updateStatus(sessionID, projectID, ScriptDebugPaused, false, "script_debug_paused", "Debugger paused")
}

func (m *ScriptDebugManager) Resume(sessionID string) (*ScriptDebugSession, bool, bool) {
	return m.updateStatus(sessionID, "", ScriptDebugRunning, false, "script_debug_resumed", "Debugger resumed")
}

func (m *ScriptDebugManager) ResumeForProject(sessionID, projectID string) (*ScriptDebugSession, bool, bool) {
	return m.updateStatus(sessionID, projectID, ScriptDebugRunning, false, "script_debug_resumed", "Debugger resumed")
}

func (m *ScriptDebugManager) Step(sessionID string) (*ScriptDebugSession, bool, bool) {
	return m.updateStatus(sessionID, "", ScriptDebugRunning, true, "script_debug_resumed", "Debugger stepping one action")
}

// StepMode advances the debugger using a step mode. "into" (the default) pauses at
// the next leaf like Step; "over" resumes through the container holding the paused
// leaf; "loop_end" resumes through the nearest enclosing control_loop. The
// over/loop_end resume pauses at the first action that falls outside the subtree,
// and falls back to step-into when no boundary can be derived.
func (m *ScriptDebugManager) StepMode(sessionID string, mode string) (*ScriptDebugSession, bool, bool) {
	return m.stepModeForProject(sessionID, "", mode)
}

func (m *ScriptDebugManager) StepModeForProject(sessionID, projectID, mode string) (*ScriptDebugSession, bool, bool) {
	return m.stepModeForProject(sessionID, projectID, mode)
}

func (m *ScriptDebugManager) stepModeForProject(sessionID, projectID, mode string) (*ScriptDebugSession, bool, bool) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode != scriptStepOver && mode != scriptStepLoopEnd {
		return m.updateStatus(sessionID, projectID, ScriptDebugRunning, true, "script_debug_resumed", "Debugger stepping one action")
	}
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
	session.reconcileRuntimeStateLocked()
	if session.reservation.failureOrExpired(time.Now()) != nil ||
		!replaySessionProjectMatches(session.ProjectID, projectID) {
		session.mu.Unlock()
		m.mu.Unlock()
		return nil, false, false
	}
	session.lastActivityAt = now
	changed := false
	if session.Status == ScriptDebugPaused {
		session.invalidateRuntimeStateLocked(ScriptDebugRuntimeNotPaused)
		boundary := computeStepBoundary(session.CurrentAction, mode)
		if boundary == nil {
			// Nothing to resume through: behave like a single step.
			session.Status = ScriptDebugRunning
			session.stepOnce = true
			session.clearStepLocked()
			session.appendLogLocked("info", "control", "Debugger stepping one action", "", nil)
		} else {
			session.Status = ScriptDebugRunning
			session.stepOnce = false
			session.stepMode = mode
			session.stepBoundary = boundary
			session.appendLogLocked("info", "control", stepModeLogMessage(mode), "", nil)
		}
		changed = true
	}
	if changed {
		session.signalStateChangedLocked()
	}
	snapshot := session.snapshotLocked()
	session.mu.Unlock()
	m.mu.Unlock()

	if changed {
		m.broadcastIfCurrent("script_debug_resumed", session, snapshot.CurrentEventIndex, nil, nil, nil)
	}
	return snapshot, true, changed
}

func stepModeLogMessage(mode string) string {
	switch mode {
	case scriptStepOver:
		return "Debugger stepping over the current block"
	case scriptStepLoopEnd:
		return "Debugger running to end of loop"
	default:
		return "Debugger stepping one action"
	}
}

func (m *ScriptDebugManager) updateStatus(sessionID, projectID string, nextStatus string, stepOnce bool, messageType string, logMessage string) (*ScriptDebugSession, bool, bool) {
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
	session.reconcileRuntimeStateLocked()
	if session.reservation.failureOrExpired(time.Now()) != nil ||
		!replaySessionProjectMatches(session.ProjectID, projectID) {
		session.mu.Unlock()
		m.mu.Unlock()
		return nil, false, false
	}
	session.lastActivityAt = now
	changed := false
	switch nextStatus {
	case ScriptDebugPaused:
		if session.Status == ScriptDebugRunning {
			session.invalidateRuntimeStateLocked(ScriptDebugRuntimePausePending)
			session.Status = ScriptDebugPaused
			session.stepOnce = false
			session.clearStepLocked()
			changed = true
		}
	case ScriptDebugRunning:
		if session.Status == ScriptDebugPaused {
			session.invalidateRuntimeStateLocked(ScriptDebugRuntimeNotPaused)
			session.Status = ScriptDebugRunning
			session.stepOnce = stepOnce
			session.clearStepLocked()
			changed = true
		}
	}
	if changed {
		session.appendLogLocked("info", "control", logMessage, "", nil)
		session.signalStateChangedLocked()
	}
	snapshot := session.snapshotLocked()
	session.mu.Unlock()
	m.mu.Unlock()

	if changed {
		m.broadcastIfCurrent(messageType, session, snapshot.CurrentEventIndex, nil, nil, nil)
	}
	return snapshot, true, changed
}

func (m *ScriptDebugManager) Stop(sessionID string) (*ScriptDebugSession, bool) {
	return m.stopForProject(sessionID, "")
}

func (m *ScriptDebugManager) StopForProject(sessionID, projectID string) (*ScriptDebugSession, bool) {
	return m.stopForProject(sessionID, projectID)
}

func (m *ScriptDebugManager) stopForProject(sessionID, projectID string) (*ScriptDebugSession, bool) {
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
	session.reconcileRuntimeStateLocked()
	if session.reservation.failureOrExpired(time.Now()) != nil ||
		!replaySessionProjectMatches(session.ProjectID, projectID) {
		session.mu.Unlock()
		m.mu.Unlock()
		return nil, false
	}
	session.lastActivityAt = now
	if (session.Status == ScriptDebugRunning || session.Status == ScriptDebugPaused) && session.cancel != nil {
		session.invalidateRuntimeStateLocked(ScriptDebugRuntimeSessionTerminal)
		// Keep the lease active while cancellation drains. Retention begins only
		// after ReplayScript returns, immediately before terminal publication.
		if err := session.reservation.failureOrExpired(time.Now()); err != nil {
			session.Status = ScriptDebugFailed
			session.Error = err.Error()
			session.rejectCorrelationEvidenceLocked("debugger session lost its replay-session integrity fence")
		} else {
			session.Status = ScriptDebugStopped
			session.rejectCorrelationEvidenceLocked("debugger session was stopped before replay completion")
		}
		session.stepOnce = false
		session.appendLogLocked("warn", "control", "Stop requested", "", nil)
		session.cancel()
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
	messageType := "script_debug_stopped"
	if snapshot.Status == ScriptDebugFailed {
		messageType = "script_debug_failed"
	}
	// Terminal transitions are pushed even when the integrity fence is lost so
	// WebSocket-only consumers do not keep showing a live session.
	m.broadcastTerminal(messageType, session, snapshot.CurrentEventIndex, nil, nil, nil)
	return snapshot, true
}

func (m *ScriptDebugManager) UpdateBreakpoints(sessionID string, breakpoints []ScriptDebugBreakpoint) (*ScriptDebugSession, bool) {
	return m.updateBreakpointsForProject(sessionID, "", breakpoints)
}

func (m *ScriptDebugManager) UpdateBreakpointsForProject(sessionID, projectID string, breakpoints []ScriptDebugBreakpoint) (*ScriptDebugSession, bool) {
	return m.updateBreakpointsForProject(sessionID, projectID, breakpoints)
}

func (m *ScriptDebugManager) updateBreakpointsForProject(sessionID, projectID string, breakpoints []ScriptDebugBreakpoint) (*ScriptDebugSession, bool) {
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
	session.reconcileRuntimeStateLocked()
	if session.reservation.failureOrExpired(time.Now()) != nil ||
		!replaySessionProjectMatches(session.ProjectID, projectID) {
		session.mu.Unlock()
		m.mu.Unlock()
		return nil, false
	}
	session.lastActivityAt = now
	session.Breakpoints = normalizeDebugBreakpoints(breakpoints)
	session.advanceRuntimeRevisionLocked()
	session.breakpointHits = nil
	session.appendLogLocked("info", "breakpoint", fmt.Sprintf("Updated %d breakpoint(s)", len(session.Breakpoints)), "", nil)
	snapshot := session.snapshotLocked()
	session.mu.Unlock()
	m.mu.Unlock()
	m.broadcastIfCurrent("script_debug_breakpoints_updated", session, snapshot.CurrentEventIndex, nil, nil, nil)
	return snapshot, true
}

func (m *ScriptDebugManager) SetBreakOnFailure(sessionID string, enabled bool) (*ScriptDebugSession, bool) {
	return m.setBreakOnFailureForProject(sessionID, "", enabled)
}

func (m *ScriptDebugManager) SetBreakOnFailureForProject(sessionID, projectID string, enabled bool) (*ScriptDebugSession, bool) {
	return m.setBreakOnFailureForProject(sessionID, projectID, enabled)
}

func (m *ScriptDebugManager) setBreakOnFailureForProject(sessionID, projectID string, enabled bool) (*ScriptDebugSession, bool) {
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
	session.reconcileRuntimeStateLocked()
	if session.reservation.failureOrExpired(time.Now()) != nil ||
		!replaySessionProjectMatches(session.ProjectID, projectID) {
		session.mu.Unlock()
		m.mu.Unlock()
		return nil, false
	}
	session.lastActivityAt = now
	session.BreakOnFailure = enabled
	session.advanceRuntimeRevisionLocked()
	state := "disabled"
	if enabled {
		state = "enabled"
	}
	session.appendLogLocked("info", "control", fmt.Sprintf("Break on failure %s", state), "", nil)
	snapshot := session.snapshotLocked()
	session.mu.Unlock()
	m.mu.Unlock()
	m.broadcastIfCurrent("script_debug_options_updated", session, snapshot.CurrentEventIndex, nil, nil, nil)
	return snapshot, true
}

func (m *ScriptDebugManager) Export(sessionID string, format string) ([]byte, string, bool) {
	return m.exportForProject(sessionID, "", format)
}

func (m *ScriptDebugManager) ExportForProject(sessionID, projectID, format string) ([]byte, string, bool) {
	return m.exportForProject(sessionID, projectID, format)
}

func (m *ScriptDebugManager) exportForProject(sessionID, projectID, format string) ([]byte, string, bool) {
	session, ok := m.getForProject(sessionID, projectID)
	if !ok {
		return nil, "", false
	}
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "log", "txt", "text":
		return []byte(strings.Join(session.RawOutput, "\n")), "text/plain; charset=utf-8", true
	default:
		payload, err := json.MarshalIndent(map[string]any{"session": session}, "", "  ")
		if err != nil {
			return []byte("{}"), "application/json", true
		}
		return payload, "application/json", true
	}
}

// retireUnfencedSession removes a terminal record that could not retain its
// global capability, then releases whichever active/retained reservation still
// owns the ID. The local lookup disappears first, so no stale record can
// overlap a retry that reuses the same externally supplied session ID.
func (m *ScriptDebugManager) retireUnfencedSession(session *ScriptDebugSession) {
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
			session.invalidateRuntimeStateLocked(ScriptDebugRuntimeSessionRetired)
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

func (m *ScriptDebugManager) broadcast(messageType string, session *ScriptDebugSession, eventIndex int, event *engine.ReplayActionEvent, action *engine.ReplayDebugAction, report *engine.ReplayReport) {
	if m.hub == nil || session == nil {
		return
	}
	// Capture status, identity, revision and full runtime state under one lock.
	// Leaf deltas carry only fixed metadata; accumulated state stays bounded to
	// lifecycle/configuration publications.
	full := true
	switch messageType {
	case "script_debug_action", "script_debug_action_started", "action_started",
		"script_debug_event", "script_debug_action_completed", "script_debug_action_failed",
		"script_debug_condition":
		full = false
	}
	session.mu.Lock()
	session.reconcileRuntimeStateLocked()
	var fullSession *ScriptDebugSession
	if full {
		if report != nil {
			fullSession = session.snapshotBaseLocked()
		} else {
			fullSession = session.snapshotLocked()
		}
	}
	message := map[string]any{
		"type":                messageType,
		"replay_session_id":   session.ID,
		"debug_session_id":    session.ID,
		"session_incarnation": session.SessionIncarnation,
		"state_revision":      strconv.FormatUint(session.runtimeRevision, 10),
		"status":              session.Status,
		"script_id":           session.ScriptID,
		"project_id":          session.ProjectID,
		"timestamp":           time.Now(),
	}
	unavailable := session.RuntimeEvidenceUnavailable
	if full {
		message["session"] = fullSession
		message["state_revision"] = fullSession.StateRevision
	}
	if report != nil {
		report = cloneBrowserReplayReport(report)
	}
	session.mu.Unlock()
	if unavailable {
		message["runtime_evidence_unavailable"] = true
		action = cloneScriptDebugAction(action)
		engine.OmitUnavailableReplayDebugAction(action)
		if event != nil {
			event = omitUnavailableScriptDebugEvent(event)
		}
		engine.OmitUnavailableReplayReport(report)
	}
	if eventIndex >= 0 {
		message["event_index"] = eventIndex
	}
	if event != nil {
		message["event_id"] = event.EventID
		message["event"] = event
	}
	if action != nil {
		if action.EventID != "" {
			message["event_id"] = action.EventID
		}
		message["action"] = action
	}
	if report != nil {
		message["report"] = report
	}
	m.hub.BroadcastJSON(message)
}

func (m *ScriptDebugManager) withCurrentSession(session *ScriptDebugSession, fn func()) bool {
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

func (m *ScriptDebugManager) withCurrentWorkerSession(session *ScriptDebugSession, fn func()) bool {
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

func (m *ScriptDebugManager) broadcastIfCurrent(messageType string, session *ScriptDebugSession, eventIndex int, event *engine.ReplayActionEvent, action *engine.ReplayDebugAction, report *engine.ReplayReport) bool {
	return m.withCurrentWorkerSession(session, func() {
		m.broadcast(messageType, session, eventIndex, event, action, report)
	})
}

// broadcastTerminal publishes a terminal lifecycle message for a session that
// is still current in this manager. Unlike broadcastIfCurrent it does not
// require a live integrity fence: after a lease loss progress payloads stay
// suppressed, but the terminal status itself must still reach push consumers
// so live views do not show a dead session running forever.
func (m *ScriptDebugManager) broadcastTerminal(messageType string, session *ScriptDebugSession, eventIndex int, event *engine.ReplayActionEvent, action *engine.ReplayDebugAction, report *engine.ReplayReport) bool {
	return m.withCurrentSession(session, func() {
		m.broadcast(messageType, session, eventIndex, event, action, report)
	})
}

func (m *ScriptDebugManager) finishWorker(session *ScriptDebugSession) {
	if session == nil {
		return
	}
	session.mu.Lock()
	if session.workerFinished {
		session.mu.Unlock()
		return
	}
	session.invalidateRuntimeStateLocked(ScriptDebugRuntimeSessionTerminal)
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

// workerHasFinishedLocked distinguishes a terminal status from a drained
// worker. A nil worker channel denotes a terminal record with no live goroutine
// (used by restored/manual records and tests), so it is safe to prune. An
// abandoned worker counts as finished: its slot and lease were already
// force-released by the backstop, so the session must remain evictable.
func (s *ScriptDebugSession) workerHasFinishedLocked() bool {
	return s == nil || s.workerFinished || s.workerAbandoned || s.workerDone == nil
}

func (s *ScriptDebugSession) snapshot() *ScriptDebugSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

func (s *ScriptDebugSession) terminalSnapshot() *ScriptDebugSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotBaseLocked()
}

func (s *ScriptDebugSession) snapshotLocked() *ScriptDebugSession {
	clone := s.snapshotBaseLocked()
	clone.FinalReport = cloneBrowserReplayReport(s.FinalReport)
	if s.RuntimeEvidenceUnavailable {
		engine.OmitUnavailableReplayReport(clone.FinalReport)
	}
	if s.CurrentAction != nil {
		clone.CurrentAction = cloneScriptDebugAction(s.CurrentAction)
		if s.RuntimeEvidenceUnavailable {
			engine.OmitUnavailableReplayDebugAction(clone.CurrentAction)
		}
	}
	if s.CurrentEvent != nil {
		currentEvent := cloneBrowserReplayEvent(*s.CurrentEvent)
		clone.CurrentEvent = &currentEvent
		if s.RuntimeEvidenceUnavailable {
			clone.CurrentEvent = omitUnavailableScriptDebugEvent(&currentEvent)
		}
	}
	if len(s.Events) > 0 {
		clone.Events = make([]engine.ReplayActionEvent, len(s.Events))
		for i, event := range s.Events {
			clone.Events[i] = cloneBrowserReplayEvent(event)
		}
		if s.RuntimeEvidenceUnavailable {
			projection := &engine.ReplayReport{Events: clone.Events}
			engine.OmitUnavailableReplayReport(projection)
			clone.Events = projection.Events
		}
	}
	return clone
}

func (s *ScriptDebugSession) snapshotBaseLocked() *ScriptDebugSession {
	runtimeState := s.runtimeStateLocked()
	clone := &ScriptDebugSession{
		SessionIncarnation:         s.SessionIncarnation,
		StateRevision:              runtimeState.StateRevision,
		RuntimeState:               runtimeState,
		ID:                         s.ID,
		RuntimeEvidenceUnavailable: s.RuntimeEvidenceUnavailable,
		Mode:                       s.Mode,
		ScriptID:                   s.ScriptID,
		ProjectID:                  s.ProjectID,
		Status:                     s.Status,
		Group:                      s.Group,
		Protocol:                   s.Protocol,
		LogLevel:                   s.LogLevel,
		RuntimeProfile:             cloneEngineReplayRuntimeProfile(&s.RuntimeProfile),
		Preflight:                  cloneReplayPreflight(s.Preflight),
		StartedAt:                  cloneReplaySessionTime(s.StartedAt),
		CompletedAt:                cloneReplaySessionTime(s.CompletedAt),
		CurrentEventIndex:          s.CurrentEventIndex,
		DroppedEventCount:          s.DroppedEventCount,
		BreakOnFailure:             s.BreakOnFailure,
		Error:                      s.Error,
	}
	if len(s.Breakpoints) > 0 {
		clone.Breakpoints = make([]ScriptDebugBreakpoint, len(s.Breakpoints))
		for i, bp := range s.Breakpoints {
			cloned := bp
			if bp.Iteration != nil {
				iteration := *bp.Iteration
				cloned.Iteration = &iteration
			}
			if bp.RequestIndex != nil {
				idx := *bp.RequestIndex
				cloned.RequestIndex = &idx
			}
			if len(bp.StepPath) > 0 {
				cloned.StepPath = append([]string(nil), bp.StepPath...)
			}
			clone.Breakpoints[i] = cloned
		}
	}
	if s.RuntimeEvidenceUnavailable {
		clone.Group = "script debug"
		clone.RuntimeProfile = engine.ReplayRuntimeProfile{}
		clone.Error = "Runtime evidence unavailable"
		return clone
	}
	if len(s.Logs) > 0 {
		clone.Logs = append([]ScriptDebugLogEntry(nil), s.Logs...)
	}
	if len(s.RawOutput) > 0 {
		clone.RawOutput = append([]string(nil), s.RawOutput...)
	}
	if len(s.ConditionEvidence) > 0 {
		clone.ConditionEvidence = make(map[string]engine.ConditionEvidence, len(s.ConditionEvidence))
		for key, evidence := range s.ConditionEvidence {
			clone.ConditionEvidence[key] = evidence
		}
	}
	return clone
}

// The bounded report projection may evict even its sole event when numeric
// evidence cannot be encoded. Keep that omission safe at every API boundary.
func omitUnavailableScriptDebugEvent(event *engine.ReplayActionEvent) *engine.ReplayActionEvent {
	if event == nil {
		return nil
	}
	projection := &engine.ReplayReport{Events: []engine.ReplayActionEvent{*event}}
	engine.OmitUnavailableReplayReport(projection)
	if len(projection.Events) == 0 {
		return nil
	}
	return &projection.Events[0]
}

func cloneScriptDebugAction(action *engine.ReplayDebugAction) *engine.ReplayDebugAction {
	if action == nil {
		return nil
	}
	cloned := *action
	if action.RequestIndex != nil {
		requestIndex := *action.RequestIndex
		cloned.RequestIndex = &requestIndex
	}
	if action.Iteration != nil {
		iteration := *action.Iteration
		cloned.Iteration = &iteration
	}
	if len(action.StepPath) > 0 {
		cloned.StepPath = append([]string(nil), action.StepPath...)
	}
	if len(action.IterationPath) > 0 {
		cloned.IterationPath = append([]int(nil), action.IterationPath...)
	}
	return &cloned
}

// omitRuntimeEvidenceLocked invalidates every public cache once credential
// classification fails. Private diagnosis is also rejected as incomplete.
// The engine's terminal report retains bounded structural/numeric outcomes.
func (s *ScriptDebugSession) omitRuntimeEvidenceLocked() {
	if s.RuntimeEvidenceUnavailable {
		return
	}
	s.RuntimeEvidenceUnavailable = true
	s.invalidateRuntimeStateLocked(ScriptDebugRuntimeEvidenceUnavailable)
	s.CurrentAction, s.CurrentEvent = nil, nil
	s.Events, s.eventJSONBytes = nil, nil
	s.retainedEventJSONBytes = 0
	s.Logs, s.RawOutput, s.ConditionEvidence = nil, nil, nil
	s.logBytes = 0
	s.Error = "Runtime evidence unavailable"
	s.rejectCorrelationEvidenceLocked("runtime evidence unavailable")
}

func (s *ScriptDebugSession) appendLogLocked(level string, category string, message string, raw string, event *engine.ReplayActionEvent) {
	if s.RuntimeEvidenceUnavailable {
		return
	}
	level = normalizeDebugLogLevel(level)
	if debugLogLevelWeight(level) > debugLogLevelWeight(s.LogLevel) {
		return
	}
	entry := ScriptDebugLogEntry{
		ID:        uuid.New().String(),
		Timestamp: time.Now(),
		Level:     level,
		Category:  strings.TrimSpace(category),
		Message:   strings.TrimSpace(message),
		Raw:       strings.TrimSpace(raw),
	}
	if event != nil {
		entry.Phase = event.Phase
		entry.ActionName = event.ActionName
		entry.ActionIndex = s.CurrentActionIndexForEvent(event)
	}
	line := formatDebugLogLine(entry)
	if entry.Raw != "" {
		line = line + "\n" + entry.Raw
	}
	s.Logs = append(s.Logs, entry)
	s.RawOutput = append(s.RawOutput, line)
	s.logBytes += len(entry.Message) + len(entry.Raw) + len(line)
	for len(s.Logs) > maxScriptDebugLogEntries || s.logBytes > maxScriptDebugLogBytes {
		if len(s.Logs) == 0 || len(s.RawOutput) == 0 {
			break
		}
		s.logBytes -= len(s.Logs[0].Message) + len(s.Logs[0].Raw) + len(s.RawOutput[0])
		s.Logs = s.Logs[1:]
		s.RawOutput = s.RawOutput[1:]
	}
}

func (s *ScriptDebugSession) stateChangedChanLocked() <-chan struct{} {
	if s.stateChanged == nil {
		s.stateChanged = make(chan struct{})
	}
	return s.stateChanged
}

func (s *ScriptDebugSession) signalStateChangedLocked() {
	ch := s.stateChanged
	if ch == nil {
		s.stateChanged = make(chan struct{})
		return
	}
	close(ch)
	s.stateChanged = make(chan struct{})
}

func (s *ScriptDebugSession) CurrentActionIndexForEvent(event *engine.ReplayActionEvent) int {
	if s.CurrentAction != nil && s.CurrentAction.Phase == event.Phase && s.CurrentAction.ActionName == event.ActionName {
		return s.CurrentAction.ActionIndex
	}
	return -1
}

type scriptDebugController struct {
	manager *ScriptDebugManager
	session *ScriptDebugSession
}

// ObserveCorrelationEvidence implements
// engine.ReplayDebugControllerCorrelationObserver. The callback runs before the
// engine scrubs and clears the ephemeral event evidence. Only action-phase,
// one-event-per-top-level-action observations are currently safe to pair with a
// stored baseline. Container/loop duplicates and init/teardown evidence are
// rejected for the whole session instead of producing a misleading diagnosis.
func (c scriptDebugController) ObserveCorrelationEvidence(_ context.Context, action engine.ReplayDebugAction, event *engine.ReplayActionEvent) {
	if c.manager == nil || c.session == nil || event == nil || event.CorrelationEvidence == nil {
		return
	}
	// This is a per-action hot path: hold only the manager read lock so
	// concurrently replaying debug sessions never serialize behind each other.
	// Per-session state stays guarded by the session mutex, and the cross-session
	// budget sweep is serialized on its own dedicated mutex below.
	c.manager.mu.RLock()
	defer c.manager.mu.RUnlock()
	if c.manager.closed || c.manager.sessions[c.session.ID] != c.session || c.session.reservation.failureOrExpired(time.Now()) != nil {
		return
	}
	c.session.mu.Lock()
	if c.session.workerAbandoned {
		c.session.mu.Unlock()
		return
	}
	if event.CorrelationEvidence.UnavailableReason != "" {
		c.session.rejectCorrelationEvidenceLocked(event.CorrelationEvidence.UnavailableReason)
		c.session.mu.Unlock()
		return
	}
	c.session.appendCorrelationObservationLocked(action, event, c.manager.retention.clock())
	retainedBytes := c.session.correlationEvidenceBytes
	c.session.mu.Unlock()
	if retainedBytes == 0 {
		// The observation was rejected or freed; budgets cannot have grown.
		return
	}
	c.manager.correlationBudgetMu.Lock()
	c.manager.enforceCorrelationEvidenceBudgetsLocked()
	c.manager.correlationBudgetMu.Unlock()
}

func (s *ScriptDebugSession) appendCorrelationObservationLocked(action engine.ReplayDebugAction, event *engine.ReplayActionEvent, retainedAt time.Time) {
	if s == nil || event == nil || event.CorrelationEvidence == nil {
		return
	}
	if s.Status == ScriptDebugStopped {
		s.rejectCorrelationEvidenceLocked("debugger session was stopped before replay completion")
		return
	}
	if event.CorrelationEvidence.UnavailableReason != "" {
		s.rejectCorrelationEvidenceLocked(event.CorrelationEvidence.UnavailableReason)
		return
	}
	if s.correlationEvidenceErr != "" {
		return
	}
	eventPhase := strings.TrimSpace(event.Phase)
	actionPhase := strings.TrimSpace(action.Phase)
	if eventPhase != "action" || actionPhase != "action" || eventPhase != actionPhase {
		s.rejectCorrelationEvidenceLocked(fmt.Sprintf("unsupported replay phase %q", eventPhase))
		return
	}
	if event.ActionIndex < 0 || action.ActionIndex != event.ActionIndex {
		s.rejectCorrelationEvidenceLocked("invalid top-level replay action identity")
		return
	}
	if action.ParentActionName != "" || action.RequestIndex != nil || len(action.StepPath) > 0 || action.Iteration != nil || len(action.IterationPath) > 0 ||
		event.ParentActionName != "" || event.RequestIndex != nil || len(event.StepPath) > 0 || event.Iteration != nil || len(event.IterationPath) > 0 {
		s.rejectCorrelationEvidenceLocked("nested replay leaves cannot be bound to a top-level recording action id")
		return
	}
	if s.correlationObservationIDs == nil {
		s.correlationObservationIDs = make(map[int]struct{})
	}
	if _, duplicate := s.correlationObservationIDs[event.ActionIndex]; duplicate {
		s.rejectCorrelationEvidenceLocked(fmt.Sprintf("duplicate top-level replay action id %d", event.ActionIndex))
		return
	}
	if len(s.correlationObservations) >= maxRecordedBaselineObservations {
		s.rejectCorrelationEvidenceLocked("raw replay observation count exceeds the retention limit")
		return
	}

	evidence := event.CorrelationEvidence
	responseSuccess := event.Success
	if evidence.ResponseSuccess != nil {
		responseSuccess = *evidence.ResponseSuccess
	}
	observation := correlate.Observation{
		ID:                     event.ActionIndex,
		RequestURL:             evidence.RequestURL,
		RequestBody:            evidence.RequestBody,
		RequestBodyTransformed: evidence.RequestBodyTransformed,
		RequestHeaders:         cloneStringMap(evidence.RequestHeaders),
		EditableRequestURL:     evidence.RequestURL == evidence.AuthoredRequestURL,
		EditableRequestBody:    !evidence.RequestBodyTransformed && evidence.RequestBody == evidence.AuthoredRequestBody,
		EditableRequestHeaders: editableRequestHeaderProof(evidence.RequestHeaders, evidence.AuthoredRequestHeaders),
		ResponseBody:           evidence.ResponseBody,
		ResponseHeaders:        cloneStringMap(evidence.ResponseHeaders),
		ResponseStatus:         evidence.ResponseStatus,
		ResponseSuccess:        &responseSuccess,
	}
	nextBytes := s.correlationEvidenceBytes
	if len(s.correlationObservations) == 0 {
		nextBytes = 2 // JSON array brackets.
	} else {
		nextBytes++ // comma between observations.
	}
	nextBytes += estimatedRecordingObservationJSONSize(rawBaselineObservation(observation))
	if nextBytes > maxRecordedBaselinePlaintextSize {
		s.rejectCorrelationEvidenceLocked("raw replay evidence exceeds the retention byte limit")
		return
	}
	s.correlationObservationIDs[event.ActionIndex] = struct{}{}
	s.correlationObservations = append(s.correlationObservations, observation)
	s.correlationEvidenceBytes = nextBytes
	if s.correlationEvidenceRetainedAt.IsZero() {
		s.correlationEvidenceRetainedAt = retainedAt.UTC()
	}
}

func editableRequestHeaderProof(wire, authored map[string]string) map[string]bool {
	if len(wire) == 0 {
		return nil
	}
	proof := make(map[string]bool, len(wire))
	for wireName, wireValue := range wire {
		exact := false
		matches := 0
		for authoredName, authoredValue := range authored {
			if strings.EqualFold(wireName, authoredName) {
				matches++
				exact = wireValue == authoredValue
			}
		}
		// Multiple authored keys that collapse to one case-insensitive HTTP
		// header name are ambiguous. Fail closed regardless of map iteration
		// order, even if one of the values happens to match the wire value.
		proof[wireName] = matches == 1 && exact
	}
	return proof
}

func (s *ScriptDebugSession) rejectCorrelationEvidenceLocked(reason string) {
	s.correlationObservations = nil
	s.correlationObservationIDs = nil
	s.correlationEvidenceBytes = 0
	s.correlationEvidenceRetainedAt = time.Time{}
	s.correlationEvidenceErr = strings.TrimSpace(reason)
}

// enforceCorrelationEvidenceBudgetsLocked clears only private raw evidence;
// public debugger sessions, scrubbed events, and reports retain their normal
// lifecycle. Oldest evidence is selected by first-retained time and then ID so
// eviction is deterministic even though sessions is a map. Callers must hold
// m.mu (read suffices — sessions is only iterated, and per-session state is
// mutated under each session mutex) plus correlationBudgetMu so only one
// budget sweep runs at a time.
func (m *ScriptDebugManager) enforceCorrelationEvidenceBudgetsLocked() {
	if m == nil {
		return
	}
	if m.correlationEvidenceProjectBytes > 0 {
		projects := make(map[string]struct{})
		for _, session := range m.sessions {
			session.mu.RLock()
			if session.correlationEvidenceBytes > 0 {
				projects[normalizeProjectID(session.ProjectID)] = struct{}{}
			}
			session.mu.RUnlock()
		}
		for projectID := range projects {
			for m.correlationEvidenceUsageLocked(projectID, true) > m.correlationEvidenceProjectBytes {
				victim := m.oldestCorrelationEvidenceSessionLocked(projectID, true)
				if victim == nil {
					break
				}
				victim.mu.Lock()
				victim.rejectCorrelationEvidenceLocked("raw replay evidence evicted to enforce the project retention byte budget")
				victim.mu.Unlock()
			}
		}
	}
	if m.correlationEvidenceGlobalBytes > 0 {
		for m.correlationEvidenceUsageLocked("", false) > m.correlationEvidenceGlobalBytes {
			victim := m.oldestCorrelationEvidenceSessionLocked("", false)
			if victim == nil {
				break
			}
			victim.mu.Lock()
			victim.rejectCorrelationEvidenceLocked("raw replay evidence evicted to enforce the global retention byte budget")
			victim.mu.Unlock()
		}
	}
}

func (m *ScriptDebugManager) correlationEvidenceUsageLocked(projectID string, projectScoped bool) int {
	total := 0
	for _, session := range m.sessions {
		session.mu.RLock()
		if !projectScoped || normalizeProjectID(session.ProjectID) == projectID {
			total += session.correlationEvidenceBytes
		}
		session.mu.RUnlock()
	}
	return total
}

func (m *ScriptDebugManager) oldestCorrelationEvidenceSessionLocked(projectID string, projectScoped bool) *ScriptDebugSession {
	var selected *ScriptDebugSession
	var selectedAt time.Time
	var selectedID string
	for id, session := range m.sessions {
		session.mu.RLock()
		bytes := session.correlationEvidenceBytes
		retainedAt := session.correlationEvidenceRetainedAt
		candidateProjectID := normalizeProjectID(session.ProjectID)
		session.mu.RUnlock()
		if bytes <= 0 || (projectScoped && candidateProjectID != projectID) {
			continue
		}
		if selected == nil || retainedAt.Before(selectedAt) || (retainedAt.Equal(selectedAt) && id < selectedID) {
			selected = session
			selectedAt = retainedAt
			selectedID = id
		}
	}
	return selected
}

func cloneCorrelationObservation(observation correlate.Observation) correlate.Observation {
	cloned := observation
	cloned.RequestHeaders = cloneStringMap(observation.RequestHeaders)
	if observation.EditableRequestHeaders != nil {
		cloned.EditableRequestHeaders = make(map[string]bool, len(observation.EditableRequestHeaders))
		for name, editable := range observation.EditableRequestHeaders {
			cloned.EditableRequestHeaders[name] = editable
		}
	}
	cloned.ResponseHeaders = cloneStringMap(observation.ResponseHeaders)
	if observation.ResponseSuccess != nil {
		responseSuccess := *observation.ResponseSuccess
		cloned.ResponseSuccess = &responseSuccess
	}
	return cloned
}

func (c scriptDebugController) BeforeAction(ctx context.Context, action engine.ReplayDebugAction) error {
	return c.beforeAction(ctx, action, nil)
}

func (c scriptDebugController) BeforeActionWithRuntimeState(ctx context.Context, action engine.ReplayDebugAction, snapshot engine.ReplayRuntimeSnapshot) error {
	return c.beforeAction(ctx, action, &snapshot)
}

func (c scriptDebugController) beforeAction(ctx context.Context, action engine.ReplayDebugAction, snapshot *engine.ReplayRuntimeSnapshot) error {
	if c.manager == nil || c.session == nil {
		return nil
	}
	actionCopy := *cloneScriptDebugAction(&action)
	token := &scriptDebugHeldBoundary{ctx: ctx, snapshot: c.runtimeCandidate(snapshot, action, "before_action")}
	defer c.leaveRuntimeBoundary(token)
	c.enterRuntimeHook(token)
	actionBroadcasted := false
	pausedBroadcasted := false
	for {
		var (
			status       string
			stateChanged <-chan struct{}
		)
		if !c.manager.withCurrentWorkerSession(c.session, func() {
			c.session.mu.Lock()
			defer c.session.mu.Unlock()
			if replaySessionStatusTerminal(c.session.Status) {
				status = ScriptDebugStopped
				return
			}
			if !actionBroadcasted {
				c.session.CurrentAction = &actionCopy
				switch {
				case c.session.Status != ScriptDebugRunning:
					c.session.appendLogLocked("debug", "action", fmt.Sprintf("Ready to execute %s[%d] %s", actionCopy.Phase, actionCopy.ActionIndex, actionCopy.ActionName)+debugLeafSuffix(actionCopy), "", nil)
				case c.session.hasBreakpointLocked(actionCopy):
					c.session.invalidateRuntimeStateLocked(ScriptDebugRuntimePausePending)
					c.session.Status = ScriptDebugPaused
					c.session.stepOnce = false
					c.session.clearStepLocked()
					c.session.appendLogLocked("info", "breakpoint", fmt.Sprintf("Paused before %s[%d] %s", actionCopy.Phase, actionCopy.ActionIndex, actionCopy.ActionName)+debugLeafSuffix(actionCopy), "", nil)
				case c.session.stepBoundaryExitLocked(actionCopy):
					// A step-over / finish-loop resume has left its subtree: pause before
					// the first action outside it.
					c.session.invalidateRuntimeStateLocked(ScriptDebugRuntimePausePending)
					c.session.Status = ScriptDebugPaused
					c.session.stepOnce = false
					c.session.clearStepLocked()
					c.session.appendLogLocked("info", "control", fmt.Sprintf("Stepped to %s[%d] %s", actionCopy.Phase, actionCopy.ActionIndex, actionCopy.ActionName)+debugLeafSuffix(actionCopy), "", nil)
				default:
					c.session.appendLogLocked("debug", "action", fmt.Sprintf("Ready to execute %s[%d] %s", actionCopy.Phase, actionCopy.ActionIndex, actionCopy.ActionName)+debugLeafSuffix(actionCopy), "", nil)
				}
			}
			status = c.session.Status
			stateChanged = c.session.stateChangedChanLocked()
		}) {
			return context.Canceled
		}
		if status == ScriptDebugStopped {
			return context.Canceled
		}

		if !actionBroadcasted {
			c.manager.broadcastIfCurrent("script_debug_action", c.session, actionCopy.EventIndex, nil, &actionCopy, nil)
			actionBroadcasted = true
		}
		if status == ScriptDebugPaused {
			// The action delta precedes the new authoritative pause revision.
			readmitted := false
			if !c.manager.withCurrentWorkerSession(c.session, func() {
				c.session.mu.Lock()
				readmitted = c.holdRuntimeBoundaryLocked(token)
				status = c.session.Status
				stateChanged = c.session.stateChangedChanLocked()
				c.session.mu.Unlock()
			}) {
				return context.Canceled
			}
			if status == ScriptDebugPaused && (readmitted || !pausedBroadcasted) {
				c.manager.broadcastIfCurrent("script_debug_paused", c.session, actionCopy.EventIndex, nil, &actionCopy, nil)
			}
			pausedBroadcasted = true
		}
		if status != ScriptDebugPaused {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-stateChanged:
		}
	}
}

// AfterAction implements engine.ReplayDebugControllerAfter. When break-on-failure
// is enabled and the action just failed, it pauses the session and holds the
// replay goroutine here — before the engine applies the stop-on-error policy — so
// the operator can inspect the failure and then resume, step, or stop. When the
// session is already paused (e.g. via Step) or break-on-failure is off, control
// is left to the before-action pause mechanism on the next action.
func (c scriptDebugController) AfterAction(ctx context.Context, action engine.ReplayDebugAction, event *engine.ReplayActionEvent) error {
	return c.afterAction(ctx, action, event, nil)
}

func (c scriptDebugController) AfterActionWithRuntimeState(ctx context.Context, action engine.ReplayDebugAction, event *engine.ReplayActionEvent, snapshot engine.ReplayRuntimeSnapshot) error {
	return c.afterAction(ctx, action, event, &snapshot)
}

func (c scriptDebugController) afterAction(ctx context.Context, action engine.ReplayDebugAction, event *engine.ReplayActionEvent, snapshot *engine.ReplayRuntimeSnapshot) error {
	if c.manager == nil || c.session == nil {
		return nil
	}
	token := &scriptDebugHeldBoundary{ctx: ctx, snapshot: c.runtimeCandidate(snapshot, action, "after_action")}
	defer c.leaveRuntimeBoundary(token)
	c.enterRuntimeHook(token)
	if event == nil || event.Success {
		return nil
	}

	actionCopy := *cloneScriptDebugAction(&action)
	paused := false
	if !c.manager.withCurrentWorkerSession(c.session, func() {
		c.session.mu.Lock()
		defer c.session.mu.Unlock()
		if !c.session.BreakOnFailure || c.session.Status != ScriptDebugRunning {
			return
		}
		// Concurrent leaves publish their evidence after joining. The actual
		// pause boundary is the group supplied by the engine, which must also
		// be authoritative in polling snapshots.
		c.session.CurrentAction = &actionCopy
		if actionCopy.ActionType == "http_sequence" {
			// The final sibling may have succeeded, or the failed leaf may have
			// been omitted. Neither is a valid substitute for failure evidence.
			c.session.CurrentEvent = pausedConcurrentFailureEvent(c.session.Events, actionCopy, *event)
			c.session.CurrentEventIndex = -1
			if c.session.CurrentEvent != nil {
				c.session.CurrentEventIndex = c.session.CurrentEvent.EventIndex
			}
		}
		c.session.Status = ScriptDebugPaused
		c.session.stepOnce = false
		c.holdRuntimeBoundaryLocked(token)
		c.session.appendLogLocked("warn", "breakpoint", fmt.Sprintf("Paused after failure in %s[%d] %s", action.Phase, action.ActionIndex, action.ActionName)+debugLeafSuffix(action), "", nil)
		paused = true
	}) {
		return context.Canceled
	}
	if !paused {
		return nil
	}

	c.manager.broadcastIfCurrent("script_debug_paused", c.session, actionCopy.EventIndex, nil, &actionCopy, nil)

	for {
		var (
			status       string
			stateChanged <-chan struct{}
			readmitted   bool
		)
		if !c.manager.withCurrentWorkerSession(c.session, func() {
			c.session.mu.Lock()
			status = c.session.Status
			if status == ScriptDebugPaused {
				readmitted = c.holdRuntimeBoundaryLocked(token)
			}
			stateChanged = c.session.stateChangedChanLocked()
			c.session.mu.Unlock()
		}) {
			return context.Canceled
		}
		if replaySessionStatusTerminal(status) {
			return context.Canceled
		}
		if status != ScriptDebugPaused {
			return nil
		}
		if readmitted {
			c.manager.broadcastIfCurrent("script_debug_paused", c.session, actionCopy.EventIndex, nil, &actionCopy, nil)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-stateChanged:
		}
	}
}

// pausedConcurrentFailureEvent selects only retained evidence from this exact
// group invocation. Nil explicitly means no matching failed evidence remains.
func pausedConcurrentFailureEvent(events []engine.ReplayActionEvent, action engine.ReplayDebugAction, group engine.ReplayActionEvent) *engine.ReplayActionEvent {
	for _, leaf := range events {
		if leaf.Success || leaf.Phase != action.Phase || leaf.ActionIndex != action.ActionIndex || leaf.StartedAt.Before(group.StartedAt) || !slices.Equal(leaf.IterationPath, action.IterationPath) {
			continue
		}
		child := leaf.RequestIndex != nil && leaf.ParentActionName == action.ActionName && !leaf.CompletedAt.After(group.CompletedAt) && len(leaf.StepPath) > len(action.StepPath) && slices.Equal(leaf.StepPath[:len(action.StepPath)], action.StepPath)
		// An admission failure is synthesized after the attempted group returns.
		parent := leaf.RequestIndex == nil && leaf.ActionName == action.ActionName && leaf.ActionType == "http_sequence" && slices.Equal(leaf.StepPath, action.StepPath)
		if child || parent {
			failure := cloneBrowserReplayEvent(leaf)
			return &failure
		}
	}
	return nil
}

// debugLeafSuffix renders the sub-request / sub-step identity of a leaf action
// (child of an http_sequence / control_if / control_loop) for log legibility,
// e.g. " · checkout · request 43/78 · loop iter 2". Empty for a plain action.
func debugLeafSuffix(action engine.ReplayDebugAction) string {
	parts := make([]string, 0, 3)
	if action.ParentActionName != "" {
		parts = append(parts, action.ParentActionName)
	}
	if action.RequestIndex != nil {
		if action.RequestCount > 0 {
			parts = append(parts, fmt.Sprintf("request %d/%d", *action.RequestIndex+1, action.RequestCount))
		} else {
			parts = append(parts, fmt.Sprintf("request %d", *action.RequestIndex+1))
		}
	}
	if len(action.IterationPath) > 0 {
		iters := make([]string, len(action.IterationPath))
		for i, n := range action.IterationPath {
			iters[i] = strconv.Itoa(n)
		}
		parts = append(parts, "loop iter "+strings.Join(iters, "·"))
	} else if action.Iteration != nil {
		parts = append(parts, fmt.Sprintf("loop iter %d", *action.Iteration))
	}
	if len(parts) == 0 {
		return ""
	}
	return " · " + strings.Join(parts, " · ")
}

func (s *ScriptDebugSession) hasBreakpointLocked(action engine.ReplayDebugAction) bool {
	hit := false
	var consumed []int
	for index, breakpoint := range s.Breakpoints {
		if breakpoint.Disabled || breakpoint.Phase != action.Phase || breakpoint.ActionIndex != action.ActionIndex {
			continue
		}
		matches := false
		switch {
		case len(breakpoint.StepPath) > 0:
			matches = stepPathEqual(breakpoint.StepPath, action.StepPath)
		case breakpoint.RequestIndex != nil:
			matches = action.RequestIndex != nil && *breakpoint.RequestIndex == *action.RequestIndex
		default:
			matches = action.LeafOrdinal == 0
		}
		if !matches {
			continue
		}
		if breakpoint.Iteration != nil && (action.Iteration == nil || *action.Iteration != *breakpoint.Iteration) {
			continue
		}
		if s.breakpointHits == nil {
			s.breakpointHits = make(map[int]int)
		}
		s.breakpointHits[index]++
		if breakpoint.HitCount > 0 && s.breakpointHits[index] != breakpoint.HitCount {
			continue
		}
		hit = true
		if breakpoint.Once {
			consumed = append(consumed, index)
		}
	}
	// Count every matching breakpoint before removing temporary stops. An
	// overlapping run-to must not hide this visit from a permanent condition.
	for n := len(consumed) - 1; n >= 0; n-- {
		index := consumed[n]
		s.Breakpoints = append(s.Breakpoints[:index], s.Breakpoints[index+1:]...)
		for i := index; i < len(s.Breakpoints); i++ {
			s.breakpointHits[i] = s.breakpointHits[i+1]
		}
		delete(s.breakpointHits, len(s.Breakpoints))
	}
	return hit
}

func stepPathEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func normalizeDebugBreakpoints(input []ScriptDebugBreakpoint) []ScriptDebugBreakpoint {
	if len(input) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(input))
	output := make([]ScriptDebugBreakpoint, 0, len(input))
	for _, breakpoint := range input {
		if len(output) >= 1024 {
			break
		}
		if breakpoint.HitCount < 0 || breakpoint.HitCount > 1000000 || (breakpoint.Iteration != nil && (*breakpoint.Iteration < 0 || *breakpoint.Iteration > 1000000)) {
			continue
		}
		phase := strings.TrimSpace(breakpoint.Phase)
		if phase == "" {
			phase = "action"
		}
		if breakpoint.ActionIndex < 0 {
			continue
		}
		reqKey := "*"
		var requestIndex *int
		if breakpoint.RequestIndex != nil {
			if *breakpoint.RequestIndex < 0 {
				continue
			}
			idx := *breakpoint.RequestIndex
			requestIndex = &idx
			reqKey = strconv.Itoa(idx)
		}
		stepKey := strings.Join(breakpoint.StepPath, "/")
		key := fmt.Sprintf("%s:%d:%s:%s:%t", phase, breakpoint.ActionIndex, reqKey, stepKey, breakpoint.Once)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		normalized := ScriptDebugBreakpoint{Phase: phase, ActionIndex: breakpoint.ActionIndex, RequestIndex: requestIndex, Disabled: breakpoint.Disabled, HitCount: breakpoint.HitCount, Once: breakpoint.Once}
		if breakpoint.Iteration != nil {
			iteration := *breakpoint.Iteration
			normalized.Iteration = &iteration
		}
		if len(breakpoint.StepPath) > 0 {
			normalized.StepPath = append([]string(nil), breakpoint.StepPath...)
		}
		output = append(output, normalized)
	}
	return output
}

func normalizeReplaySessionMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "replay", "browser_replay", "script_debug":
		return strings.ToLower(strings.TrimSpace(mode))
	default:
		return "script_debug"
	}
}

func replayRuntimeProfileForDebug(script *dsl.Script, req ScriptDebugStartRequest, ignoreThinkTime bool) engine.ReplayRuntimeProfile {
	profile := engine.ReplayRuntimeProfile{}
	if req.RuntimeProfile != nil {
		profile = cloneEngineReplayRuntimeProfile(req.RuntimeProfile)
	}
	if profile.Name == "" {
		if strings.TrimSpace(req.ReplayProfile) != "" {
			profile.Name = strings.TrimSpace(req.ReplayProfile)
		} else {
			profile.Name = "debug trace"
		}
	}
	if profile.CaptureMode == "" {
		profile.CaptureMode = "diagnostic"
	}
	profile.LogLevel = normalizeDebugLogLevel(req.LogLevel)
	profile.IgnoreThinkTime = ignoreThinkTime
	if script != nil {
		if profile.BrowserEngine == "" {
			profile.BrowserEngine = strings.TrimSpace(script.Config.Browser.Engine)
		}
		if profile.DeviceProfile == "" {
			profile.DeviceProfile = strings.TrimSpace(script.Config.Browser.DeviceProfile)
		}
		if profile.Headless == nil && script.Config.Browser.Headless != nil {
			headless := *script.Config.Browser.Headless
			profile.Headless = &headless
		}
		if profile.FailurePolicy == "" {
			if script.Config.ErrorHandling.ContinueOnErrorEnabled() {
				profile.FailurePolicy = "continue_on_error"
			} else {
				profile.FailurePolicy = "stop_on_error"
			}
		}
	}
	return profile
}

func replayRuntimeProfileForBrowser(script *dsl.Script, req BrowserReplayStartRequest, ignoreThinkTime bool) engine.ReplayRuntimeProfile {
	profile := engine.ReplayRuntimeProfile{
		Name:            "browser canary",
		CaptureMode:     "browser_evidence",
		LogLevel:        "info",
		IgnoreThinkTime: ignoreThinkTime,
	}
	if script != nil {
		profile.BrowserEngine = strings.TrimSpace(script.Config.Browser.Engine)
		profile.DeviceProfile = strings.TrimSpace(script.Config.Browser.DeviceProfile)
		if script.Config.Browser.Headless != nil {
			headless := *script.Config.Browser.Headless
			profile.Headless = &headless
		}
		if script.Config.ErrorHandling.ContinueOnErrorEnabled() {
			profile.FailurePolicy = "continue_on_error"
		} else {
			profile.FailurePolicy = "stop_on_error"
		}
	}
	if req.Browser != nil {
		if req.Browser.Engine != "" {
			profile.BrowserEngine = strings.TrimSpace(req.Browser.Engine)
		}
		if req.Browser.DeviceProfile != "" {
			profile.DeviceProfile = strings.TrimSpace(req.Browser.DeviceProfile)
		}
		if req.Browser.Headless != nil {
			headless := *req.Browser.Headless
			profile.Headless = &headless
		}
	}
	return profile
}

func cloneEngineReplayRuntimeProfile(profile *engine.ReplayRuntimeProfile) engine.ReplayRuntimeProfile {
	if profile == nil {
		return engine.ReplayRuntimeProfile{}
	}
	cloned := *profile
	if profile.Headless != nil {
		headless := *profile.Headless
		cloned.Headless = &headless
	}
	return cloned
}

func normalizeDebugLogLevel(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "error", "warn", "info", "debug", "trace":
		return strings.ToLower(strings.TrimSpace(level))
	default:
		return "info"
	}
}

func debugLogLevelWeight(level string) int {
	switch normalizeDebugLogLevel(level) {
	case "error":
		return 0
	case "warn":
		return 1
	case "info":
		return 2
	case "debug":
		return 3
	case "trace":
		return 4
	default:
		return 2
	}
}

func debugActionResultMessage(event engine.ReplayActionEvent) string {
	name := strings.TrimSpace(event.ActionName)
	if name == "" {
		name = "Unnamed action"
	}
	if event.Success {
		return fmt.Sprintf("%s completed in %.1f ms", name, event.DurationMs)
	}
	if event.ErrorMessage != "" {
		return fmt.Sprintf("%s failed: %s", name, event.ErrorMessage)
	}
	return fmt.Sprintf("%s failed", name)
}

func formatDebugEventRaw(event engine.ReplayActionEvent) string {
	var lines []string
	requestLine := strings.TrimSpace(strings.Join([]string{event.Method, event.Target}, " "))
	if requestLine == "" {
		requestLine = strings.TrimSpace(strings.Join([]string{event.ActionType, event.Target}, " "))
	}
	if requestLine != "" {
		lines = append(lines, "REQUEST "+requestLine)
	}
	if len(event.Headers) > 0 {
		lines = append(lines, "HEADERS")
		for key, value := range event.Headers {
			lines = append(lines, fmt.Sprintf("%s: %s", key, value))
		}
	}
	if event.Body != "" {
		lines = append(lines, "BODY", event.Body)
	}
	if event.StatusCode != 0 || event.ErrorMessage != "" {
		response := fmt.Sprintf("RESPONSE %d", event.StatusCode)
		if event.ErrorMessage != "" {
			response = strings.TrimSpace(response + " " + event.ErrorMessage)
		}
		lines = append(lines, response)
	}
	if len(event.Extractions) > 0 {
		lines = append(lines, "EXTRACTIONS")
		for key, value := range event.Extractions {
			lines = append(lines, fmt.Sprintf("%s=%s", key, value))
		}
	}
	if len(event.Console) > 0 {
		lines = append(lines, "BROWSER CONSOLE")
		for _, entry := range event.Console {
			lines = append(lines, fmt.Sprintf("[%s] %s", entry.Level, entry.Text))
		}
	}
	if len(event.Network) > 0 {
		lines = append(lines, "BROWSER NETWORK")
		for _, entry := range event.Network {
			lines = append(lines, fmt.Sprintf("%s %d %s", entry.Method, entry.StatusCode, entry.URL))
		}
	}
	return strings.Join(lines, "\n")
}

func formatDebugEventRawForLogLevel(logLevel string, event engine.ReplayActionEvent) string {
	if debugLogLevelWeight(logLevel) < debugLogLevelWeight("debug") {
		return ""
	}
	return formatDebugEventRaw(event)
}

func formatDebugLogLine(entry ScriptDebugLogEntry) string {
	location := ""
	if entry.Phase != "" {
		location = fmt.Sprintf(" %s[%d]", entry.Phase, entry.ActionIndex)
	}
	return fmt.Sprintf("[%s] %-5s %-12s%s %s", entry.Timestamp.Format(time.RFC3339), strings.ToUpper(entry.Level), entry.Category, location, entry.Message)
}
