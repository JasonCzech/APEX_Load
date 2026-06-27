package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
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
)

type ScriptDebugBreakpoint struct {
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
	ID                string                      `json:"id"`
	Mode              string                      `json:"mode,omitempty"`
	ScriptID          string                      `json:"script_id,omitempty"`
	ProjectID         string                      `json:"project_id,omitempty"`
	Status            string                      `json:"status"`
	Group             string                      `json:"group"`
	Protocol          string                      `json:"protocol"`
	LogLevel          string                      `json:"log_level"`
	RuntimeProfile    engine.ReplayRuntimeProfile `json:"runtime_profile,omitempty"`
	Preflight         *ReplayPreflight            `json:"preflight,omitempty"`
	StartedAt         *time.Time                  `json:"started_at,omitempty"`
	CompletedAt       *time.Time                  `json:"completed_at,omitempty"`
	CurrentEventIndex int                         `json:"current_event_index"`
	CurrentAction     *engine.ReplayDebugAction   `json:"current_action,omitempty"`
	CurrentEvent      *engine.ReplayActionEvent   `json:"current_event,omitempty"`
	Breakpoints       []ScriptDebugBreakpoint     `json:"breakpoints,omitempty"`
	BreakOnFailure    bool                        `json:"break_on_failure"`
	Events            []engine.ReplayActionEvent  `json:"events,omitempty"`
	Logs              []ScriptDebugLogEntry       `json:"logs,omitempty"`
	RawOutput         []string                    `json:"raw_output,omitempty"`
	FinalReport       *engine.ReplayReport        `json:"report,omitempty"`
	Error             string                      `json:"error,omitempty"`
	// ConditionEvidence carries the most recent evaluated operands/result for each
	// control_if / control_loop container, keyed by conditionEvidenceKey, so the
	// debugger can show why a branch was taken or a loop pass ran.
	ConditionEvidence map[string]engine.ConditionEvidence `json:"condition_evidence,omitempty"`

	mu             sync.RWMutex
	cancel         context.CancelFunc
	stepOnce       bool
	stepMode       string        // "" | over | loop_end while a step-through is in flight
	stepBoundary   *stepBoundary // resume-through subtree for stepMode
	lastActivityAt time.Time
	terminalAt     *time.Time
	stateChanged   chan struct{}
}

type ScriptDebugManager struct {
	hub           *Hub
	sessions      map[string]*ScriptDebugSession
	mu            sync.RWMutex
	onComplete    replayCompletionHook
	retention     replaySessionRetentionConfig
	janitorCancel context.CancelFunc
}

func NewScriptDebugManager(hub *Hub) *ScriptDebugManager {
	return &ScriptDebugManager{
		hub:       hub,
		sessions:  make(map[string]*ScriptDebugSession),
		retention: defaultReplaySessionRetentionConfig(),
	}
}

func (m *ScriptDebugManager) setCompletionHook(hook replayCompletionHook) {
	m.onComplete = hook
}

func (m *ScriptDebugManager) StartJanitor(ctx context.Context) {
	if m == nil {
		return
	}
	m.mu.Lock()
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
	m.mu.Lock()
	cancel := m.janitorCancel
	m.janitorCancel = nil
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (m *ScriptDebugManager) Start(scriptID string, script *dsl.Script, req ScriptDebugStartRequest) *ScriptDebugSession {
	sessionID := strings.TrimSpace(req.ReplaySessionID)
	if sessionID == "" {
		sessionID = uuid.New().String()
	}
	groupName := strings.TrimSpace(req.GroupName)
	if groupName == "" {
		groupName = strings.TrimSpace(script.Name) + "-debug"
		if strings.TrimSpace(script.Name) == "" {
			groupName = "script-debug"
		}
	}
	now := time.Now()
	ctx, cancel := context.WithCancel(context.Background())
	ignoreThinkTime := true
	if req.IgnoreThinkTime != nil {
		ignoreThinkTime = *req.IgnoreThinkTime
	}
	session := &ScriptDebugSession{
		ID:                sessionID,
		Mode:              normalizeReplaySessionMode(req.Mode),
		ScriptID:          scriptID,
		ProjectID:         normalizeProjectID(script.ProjectID),
		Status:            ScriptDebugRunning,
		Group:             groupName,
		Protocol:          script.Protocol,
		LogLevel:          normalizeDebugLogLevel(req.LogLevel),
		RuntimeProfile:    replayRuntimeProfileForDebug(script, req, ignoreThinkTime),
		Preflight:         cloneReplayPreflight(req.Preflight),
		StartedAt:         &now,
		CurrentEventIndex: -1,
		Breakpoints:       normalizeDebugBreakpoints(req.Breakpoints),
		BreakOnFailure:    req.BreakOnFailure,
		cancel:            cancel,
		lastActivityAt:    now,
		stateChanged:      make(chan struct{}),
	}
	if req.StartPaused {
		session.Status = ScriptDebugPaused
	}
	session.appendLogLocked("info", "session", "Debugger session started", "", nil)
	if req.StartPaused {
		session.appendLogLocked("info", "control", "Session is waiting at the first action", "", nil)
	}

	m.mu.Lock()
	m.pruneExpiredLocked(now)
	if existing := m.sessions[sessionID]; existing != nil {
		existing.mu.Lock()
		m.cancelSessionLocked(existing, now, "Debugger session replaced by a newer session with the same id")
		existing.mu.Unlock()
		delete(m.sessions, sessionID)
	}
	m.sessions[sessionID] = session
	m.enforceMaxSessionsLocked(now, sessionID)
	m.mu.Unlock()

	m.broadcast("script_debug_started", session, -1, nil, nil, nil)
	if req.StartPaused {
		m.broadcast("script_debug_paused", session, -1, nil, nil, nil)
	}

	go func() {
		report, err := engine.ReplayScript(ctx, script, engine.ReplayOptions{
			ReplaySessionID: sessionID,
			GroupName:       groupName,
			RuntimeProfile:  &session.RuntimeProfile,
			Variables:       req.Variables,
			RedactionRules:  req.RedactionRules,
			IgnoreThinkTime: ignoreThinkTime,
			DebugController: scriptDebugController{manager: m, session: session},
			OnProgress: func(event engine.ReplayProgressEvent) {
				m.handleReplayProgress(session, event)
			},
		})

		completedAt := time.Now()
		messageType := "script_debug_completed"
		session.mu.Lock()
		session.CompletedAt = &completedAt
		if report != nil {
			session.FinalReport = cloneBrowserReplayReport(report)
			session.Error = report.Error
		}
		if session.Status == ScriptDebugStopped {
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
		if replaySessionStatusTerminal(session.Status) && session.terminalAt == nil {
			session.terminalAt = &completedAt
		}
		session.signalStateChangedLocked()
		eventIndex := session.CurrentEventIndex
		session.mu.Unlock()

		m.broadcast(messageType, session, eventIndex, nil, nil, report)

		if m.onComplete != nil && report != nil {
			m.onComplete(session.ProjectID, script, report)
		}
	}()

	return session.snapshot()
}

func (m *ScriptDebugManager) PruneExpired() int {
	if m == nil {
		return 0
	}
	now := m.retention.clock()
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pruneExpiredLocked(now)
}

func (m *ScriptDebugManager) pruneExpiredLocked(now time.Time) int {
	removed := 0
	for id, session := range m.sessions {
		session.mu.Lock()
		m.cancelIdleRunningSessionLocked(session, now)
		if m.shouldPruneSessionLocked(session, now) {
			m.cancelSessionLocked(session, now, "Debugger session pruned after retention window")
			delete(m.sessions, id)
			removed++
		}
		session.mu.Unlock()
	}
	return removed + m.enforceMaxSessionsLocked(now, "")
}

func (m *ScriptDebugManager) enforceMaxSessionsLocked(now time.Time, protectedID string) int {
	if m.retention.maxSessions <= 0 {
		return 0
	}
	removed := 0
	for len(m.sessions) > m.retention.maxSessions {
		id, session := m.oldestPrunableSessionLocked(protectedID, true)
		if session == nil {
			id, session = m.oldestPrunableSessionLocked(protectedID, false)
		}
		if session == nil {
			return removed
		}
		session.mu.Lock()
		m.cancelSessionLocked(session, now, "Debugger session pruned to enforce session cap")
		session.mu.Unlock()
		delete(m.sessions, id)
		removed++
	}
	return removed
}

func (m *ScriptDebugManager) oldestPrunableSessionLocked(protectedID string, terminalOnly bool) (string, *ScriptDebugSession) {
	var selectedID string
	var selected *ScriptDebugSession
	var selectedAt time.Time
	for id, session := range m.sessions {
		if id == protectedID {
			continue
		}
		session.mu.RLock()
		terminal := replaySessionStatusTerminal(session.Status)
		if terminalOnly && !terminal {
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
	if session == nil || m.retention.idleCancel <= 0 || session.Status != ScriptDebugRunning {
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
	m.cancelSessionLocked(session, now, "Debugger session stopped after running idle timeout")
}

func (m *ScriptDebugManager) shouldPruneSessionLocked(session *ScriptDebugSession, now time.Time) bool {
	if session == nil || m.retention.terminalTTL <= 0 || !replaySessionStatusTerminal(session.Status) {
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
	if session.Status == ScriptDebugRunning || session.Status == ScriptDebugPaused {
		session.Status = ScriptDebugStopped
		session.stepOnce = false
		session.clearStepLocked()
		if reason != "" {
			session.appendLogLocked("warn", "control", reason, "", nil)
		}
		if session.cancel != nil {
			session.cancel()
		}
	}
	if replaySessionStatusTerminal(session.Status) && session.terminalAt == nil {
		terminalAt := now
		session.terminalAt = &terminalAt
	}
	session.signalStateChangedLocked()
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

func (m *ScriptDebugManager) handleReplayProgress(session *ScriptDebugSession, event engine.ReplayProgressEvent) {
	if session == nil {
		return
	}
	switch event.Type {
	case engine.ReplayProgressStarted:
		session.mu.Lock()
		if event.Report != nil {
			session.FinalReport = cloneBrowserReplayReport(event.Report)
		}
		session.mu.Unlock()
	case engine.ReplayProgressActionStarted:
		session.mu.Lock()
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
		}
	case engine.ReplayProgressEventRun:
		session.mu.Lock()
		if event.Event != nil {
			cloned := cloneBrowserReplayEvent(*event.Event)
			session.CurrentEventIndex = event.EventIndex
			session.CurrentEvent = &cloned
			if event.EventIndex >= 0 {
				for len(session.Events) <= event.EventIndex {
					session.Events = append(session.Events, engine.ReplayActionEvent{})
				}
				session.Events[event.EventIndex] = cloned
			} else {
				session.Events = append(session.Events, cloned)
			}
			level := "info"
			if !cloned.Success {
				level = "error"
			}
			session.appendLogLocked(level, "action", debugActionResultMessage(cloned), formatDebugEventRaw(cloned), &cloned)
		}
		shouldPauseAfterStep := session.stepOnce && session.Status == ScriptDebugRunning
		if shouldPauseAfterStep {
			session.stepOnce = false
			session.Status = ScriptDebugPaused
			session.appendLogLocked("info", "control", "Step completed; debugger paused", "", nil)
		}
		session.mu.Unlock()
		m.broadcast("script_debug_event", session, event.EventIndex, event.Event, nil, nil)
		if shouldPauseAfterStep {
			m.broadcast("script_debug_paused", session, event.EventIndex, nil, nil, nil)
		}
	case engine.ReplayProgressCondition:
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
	case engine.ReplayProgressComplete:
		session.mu.Lock()
		if event.Report != nil {
			session.FinalReport = cloneBrowserReplayReport(event.Report)
		}
		session.mu.Unlock()
	}
}

func (m *ScriptDebugManager) Get(sessionID string) (*ScriptDebugSession, bool) {
	now := m.retention.clock()
	m.mu.Lock()
	m.pruneExpiredLocked(now)
	session, ok := m.sessions[sessionID]
	m.mu.Unlock()
	if !ok {
		return nil, false
	}
	session.mu.Lock()
	session.lastActivityAt = now
	snapshot := session.snapshotLocked()
	session.mu.Unlock()
	return snapshot, true
}

func (m *ScriptDebugManager) Pause(sessionID string) (*ScriptDebugSession, bool, bool) {
	return m.updateStatus(sessionID, ScriptDebugPaused, false, "script_debug_paused", "Debugger paused")
}

func (m *ScriptDebugManager) Resume(sessionID string) (*ScriptDebugSession, bool, bool) {
	return m.updateStatus(sessionID, ScriptDebugRunning, false, "script_debug_resumed", "Debugger resumed")
}

func (m *ScriptDebugManager) Step(sessionID string) (*ScriptDebugSession, bool, bool) {
	return m.updateStatus(sessionID, ScriptDebugRunning, true, "script_debug_resumed", "Debugger stepping one action")
}

// StepMode advances the debugger using a step mode. "into" (the default) pauses at
// the next leaf like Step; "over" resumes through the container holding the paused
// leaf; "loop_end" resumes through the nearest enclosing control_loop. The
// over/loop_end resume pauses at the first action that falls outside the subtree,
// and falls back to step-into when no boundary can be derived.
func (m *ScriptDebugManager) StepMode(sessionID string, mode string) (*ScriptDebugSession, bool, bool) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode != scriptStepOver && mode != scriptStepLoopEnd {
		return m.Step(sessionID)
	}
	now := m.retention.clock()
	m.mu.Lock()
	m.pruneExpiredLocked(now)
	session, ok := m.sessions[sessionID]
	m.mu.Unlock()
	if !ok {
		return nil, false, false
	}

	session.mu.Lock()
	session.lastActivityAt = now
	changed := false
	if session.Status == ScriptDebugPaused {
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

	if changed {
		m.broadcast("script_debug_resumed", snapshot, snapshot.CurrentEventIndex, nil, nil, nil)
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

func (m *ScriptDebugManager) updateStatus(sessionID string, nextStatus string, stepOnce bool, messageType string, logMessage string) (*ScriptDebugSession, bool, bool) {
	now := m.retention.clock()
	m.mu.Lock()
	m.pruneExpiredLocked(now)
	session, ok := m.sessions[sessionID]
	m.mu.Unlock()
	if !ok {
		return nil, false, false
	}

	session.mu.Lock()
	session.lastActivityAt = now
	changed := false
	switch nextStatus {
	case ScriptDebugPaused:
		if session.Status == ScriptDebugRunning {
			session.Status = ScriptDebugPaused
			session.stepOnce = false
			session.clearStepLocked()
			changed = true
		}
	case ScriptDebugRunning:
		if session.Status == ScriptDebugPaused {
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

	if changed {
		m.broadcast(messageType, snapshot, snapshot.CurrentEventIndex, nil, nil, nil)
	}
	return snapshot, true, changed
}

func (m *ScriptDebugManager) Stop(sessionID string) (*ScriptDebugSession, bool) {
	now := m.retention.clock()
	m.mu.Lock()
	m.pruneExpiredLocked(now)
	session, ok := m.sessions[sessionID]
	m.mu.Unlock()
	if !ok {
		return nil, false
	}

	session.mu.Lock()
	session.lastActivityAt = now
	if (session.Status == ScriptDebugRunning || session.Status == ScriptDebugPaused) && session.cancel != nil {
		session.Status = ScriptDebugStopped
		session.stepOnce = false
		session.appendLogLocked("warn", "control", "Stop requested", "", nil)
		session.cancel()
		session.signalStateChangedLocked()
	}
	if replaySessionStatusTerminal(session.Status) && session.terminalAt == nil {
		terminalAt := now
		session.terminalAt = &terminalAt
	}
	snapshot := session.snapshotLocked()
	session.mu.Unlock()
	m.broadcast("script_debug_stopped", snapshot, snapshot.CurrentEventIndex, nil, nil, nil)
	return snapshot, true
}

func (m *ScriptDebugManager) UpdateBreakpoints(sessionID string, breakpoints []ScriptDebugBreakpoint) (*ScriptDebugSession, bool) {
	now := m.retention.clock()
	m.mu.Lock()
	m.pruneExpiredLocked(now)
	session, ok := m.sessions[sessionID]
	m.mu.Unlock()
	if !ok {
		return nil, false
	}

	session.mu.Lock()
	session.lastActivityAt = now
	session.Breakpoints = normalizeDebugBreakpoints(breakpoints)
	session.appendLogLocked("info", "breakpoint", fmt.Sprintf("Updated %d breakpoint(s)", len(session.Breakpoints)), "", nil)
	snapshot := session.snapshotLocked()
	session.mu.Unlock()
	m.broadcast("script_debug_breakpoints_updated", snapshot, snapshot.CurrentEventIndex, nil, nil, nil)
	return snapshot, true
}

func (m *ScriptDebugManager) SetBreakOnFailure(sessionID string, enabled bool) (*ScriptDebugSession, bool) {
	now := m.retention.clock()
	m.mu.Lock()
	m.pruneExpiredLocked(now)
	session, ok := m.sessions[sessionID]
	m.mu.Unlock()
	if !ok {
		return nil, false
	}

	session.mu.Lock()
	session.lastActivityAt = now
	session.BreakOnFailure = enabled
	state := "disabled"
	if enabled {
		state = "enabled"
	}
	session.appendLogLocked("info", "control", fmt.Sprintf("Break on failure %s", state), "", nil)
	snapshot := session.snapshotLocked()
	session.mu.Unlock()
	m.broadcast("script_debug_options_updated", snapshot, snapshot.CurrentEventIndex, nil, nil, nil)
	return snapshot, true
}

func (m *ScriptDebugManager) Export(sessionID string, format string) ([]byte, string, bool) {
	session, ok := m.Get(sessionID)
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

func (m *ScriptDebugManager) broadcast(messageType string, session *ScriptDebugSession, eventIndex int, event *engine.ReplayActionEvent, action *engine.ReplayDebugAction, report *engine.ReplayReport) {
	if m.hub == nil || session == nil {
		return
	}
	sessionPayload := session.snapshot()
	message := map[string]any{
		"type":              messageType,
		"replay_session_id": sessionPayload.ID,
		"debug_session_id":  sessionPayload.ID,
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

func (s *ScriptDebugSession) snapshot() *ScriptDebugSession {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshotLocked()
}

func (s *ScriptDebugSession) snapshotLocked() *ScriptDebugSession {
	clone := &ScriptDebugSession{
		ID:                s.ID,
		Mode:              s.Mode,
		ScriptID:          s.ScriptID,
		ProjectID:         s.ProjectID,
		Status:            s.Status,
		Group:             s.Group,
		Protocol:          s.Protocol,
		LogLevel:          s.LogLevel,
		RuntimeProfile:    cloneEngineReplayRuntimeProfile(&s.RuntimeProfile),
		Preflight:         cloneReplayPreflight(s.Preflight),
		StartedAt:         s.StartedAt,
		CompletedAt:       s.CompletedAt,
		CurrentEventIndex: s.CurrentEventIndex,
		BreakOnFailure:    s.BreakOnFailure,
		FinalReport:       cloneBrowserReplayReport(s.FinalReport),
		Error:             s.Error,
	}
	if s.CurrentAction != nil {
		currentAction := *s.CurrentAction
		clone.CurrentAction = &currentAction
	}
	if s.CurrentEvent != nil {
		currentEvent := cloneBrowserReplayEvent(*s.CurrentEvent)
		clone.CurrentEvent = &currentEvent
	}
	if len(s.Breakpoints) > 0 {
		clone.Breakpoints = make([]ScriptDebugBreakpoint, len(s.Breakpoints))
		for i, bp := range s.Breakpoints {
			cloned := bp
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
	if len(s.Events) > 0 {
		clone.Events = make([]engine.ReplayActionEvent, len(s.Events))
		for i, event := range s.Events {
			clone.Events[i] = cloneBrowserReplayEvent(event)
		}
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

func (s *ScriptDebugSession) appendLogLocked(level string, category string, message string, raw string, event *engine.ReplayActionEvent) {
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

func (c scriptDebugController) BeforeAction(ctx context.Context, action engine.ReplayDebugAction) error {
	if c.manager == nil || c.session == nil {
		return nil
	}
	actionCopy := action
	actionBroadcasted := false
	pausedBroadcasted := false
	for {
		c.session.mu.Lock()
		if c.session.Status == ScriptDebugStopped {
			c.session.mu.Unlock()
			return context.Canceled
		}
		if !actionBroadcasted {
			c.session.CurrentAction = &actionCopy
			switch {
			case c.session.Status != ScriptDebugRunning:
				c.session.appendLogLocked("debug", "action", fmt.Sprintf("Ready to execute %s[%d] %s", actionCopy.Phase, actionCopy.ActionIndex, actionCopy.ActionName)+debugLeafSuffix(actionCopy), "", nil)
			case c.session.hasBreakpointLocked(actionCopy):
				c.session.Status = ScriptDebugPaused
				c.session.stepOnce = false
				c.session.clearStepLocked()
				c.session.appendLogLocked("info", "breakpoint", fmt.Sprintf("Paused before %s[%d] %s", actionCopy.Phase, actionCopy.ActionIndex, actionCopy.ActionName)+debugLeafSuffix(actionCopy), "", nil)
			case c.session.stepBoundaryExitLocked(actionCopy):
				// A step-over / finish-loop resume has left its subtree: pause before
				// the first action outside it.
				c.session.Status = ScriptDebugPaused
				c.session.stepOnce = false
				c.session.clearStepLocked()
				c.session.appendLogLocked("info", "control", fmt.Sprintf("Stepped to %s[%d] %s", actionCopy.Phase, actionCopy.ActionIndex, actionCopy.ActionName)+debugLeafSuffix(actionCopy), "", nil)
			default:
				c.session.appendLogLocked("debug", "action", fmt.Sprintf("Ready to execute %s[%d] %s", actionCopy.Phase, actionCopy.ActionIndex, actionCopy.ActionName)+debugLeafSuffix(actionCopy), "", nil)
			}
		}
		status := c.session.Status
		stateChanged := c.session.stateChangedChanLocked()
		c.session.mu.Unlock()

		if !actionBroadcasted {
			c.manager.broadcast("script_debug_action", c.session, actionCopy.EventIndex, nil, &actionCopy, nil)
			actionBroadcasted = true
		}
		if status == ScriptDebugPaused && !pausedBroadcasted {
			c.manager.broadcast("script_debug_paused", c.session, actionCopy.EventIndex, nil, &actionCopy, nil)
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
	if c.manager == nil || c.session == nil {
		return nil
	}
	if event == nil || event.Success {
		return nil
	}

	c.session.mu.Lock()
	if !c.session.BreakOnFailure || c.session.Status != ScriptDebugRunning {
		c.session.mu.Unlock()
		return nil
	}
	c.session.Status = ScriptDebugPaused
	c.session.stepOnce = false
	c.session.appendLogLocked("warn", "breakpoint", fmt.Sprintf("Paused after failure in %s[%d] %s", action.Phase, action.ActionIndex, action.ActionName)+debugLeafSuffix(action), "", nil)
	c.session.mu.Unlock()

	actionCopy := action
	c.manager.broadcast("script_debug_paused", c.session, actionCopy.EventIndex, nil, &actionCopy, nil)

	for {
		c.session.mu.Lock()
		status := c.session.Status
		stateChanged := c.session.stateChangedChanLocked()
		c.session.mu.Unlock()
		if status == ScriptDebugStopped {
			return context.Canceled
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
	for _, breakpoint := range s.Breakpoints {
		if breakpoint.Phase != action.Phase || breakpoint.ActionIndex != action.ActionIndex {
			continue
		}
		switch {
		case len(breakpoint.StepPath) > 0:
			// Control-flow / general sub-step breakpoint: match the leaf's static
			// path (so it fires on every loop iteration of that step).
			if stepPathEqual(breakpoint.StepPath, action.StepPath) {
				return true
			}
		case breakpoint.RequestIndex != nil:
			// http_sequence sub-request breakpoint.
			if action.RequestIndex != nil && *breakpoint.RequestIndex == *action.RequestIndex {
				return true
			}
		default:
			// Whole-action breakpoint: stop once, at the first executed leaf, for
			// any action type (plain action, http_sequence, or control flow).
			if action.LeafOrdinal == 0 {
				return true
			}
		}
	}
	return false
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
		key := fmt.Sprintf("%s:%d:%s:%s", phase, breakpoint.ActionIndex, reqKey, stepKey)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		normalized := ScriptDebugBreakpoint{Phase: phase, ActionIndex: breakpoint.ActionIndex, RequestIndex: requestIndex}
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
			if script.Config.ErrorHandling.ContinueOnError {
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
		if script.Config.ErrorHandling.ContinueOnError {
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

func formatDebugLogLine(entry ScriptDebugLogEntry) string {
	location := ""
	if entry.Phase != "" {
		location = fmt.Sprintf(" %s[%d]", entry.Phase, entry.ActionIndex)
	}
	return fmt.Sprintf("[%s] %-5s %-12s%s %s", entry.Timestamp.Format(time.RFC3339), strings.ToUpper(entry.Level), entry.Category, location, entry.Message)
}
