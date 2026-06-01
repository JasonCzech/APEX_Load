package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	Events            []engine.ReplayActionEvent  `json:"events,omitempty"`
	Logs              []ScriptDebugLogEntry       `json:"logs,omitempty"`
	RawOutput         []string                    `json:"raw_output,omitempty"`
	FinalReport       *engine.ReplayReport        `json:"report,omitempty"`
	Error             string                      `json:"error,omitempty"`

	mu       sync.RWMutex
	cancel   context.CancelFunc
	stepOnce bool
}

type ScriptDebugManager struct {
	hub      *Hub
	sessions map[string]*ScriptDebugSession
	mu       sync.RWMutex
}

func NewScriptDebugManager(hub *Hub) *ScriptDebugManager {
	return &ScriptDebugManager{
		hub:      hub,
		sessions: make(map[string]*ScriptDebugSession),
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
		cancel:            cancel,
	}
	if req.StartPaused {
		session.Status = ScriptDebugPaused
	}
	session.appendLogLocked("info", "session", "Debugger session started", "", nil)
	if req.StartPaused {
		session.appendLogLocked("info", "control", "Session is waiting at the first action", "", nil)
	}

	m.mu.Lock()
	m.sessions[sessionID] = session
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
		eventIndex := session.CurrentEventIndex
		session.mu.Unlock()

		m.broadcast(messageType, session, eventIndex, nil, nil, report)
	}()

	return session.snapshot()
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
			session.appendLogLocked("debug", "action", fmt.Sprintf("Started %s[%d] %s", actionCopy.Phase, actionCopy.ActionIndex, actionCopy.ActionName), "", nil)
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
	case engine.ReplayProgressComplete:
		session.mu.Lock()
		if event.Report != nil {
			session.FinalReport = cloneBrowserReplayReport(event.Report)
		}
		session.mu.Unlock()
	}
}

func (m *ScriptDebugManager) Get(sessionID string) (*ScriptDebugSession, bool) {
	m.mu.RLock()
	session, ok := m.sessions[sessionID]
	m.mu.RUnlock()
	if !ok {
		return nil, false
	}
	return session.snapshot(), true
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

func (m *ScriptDebugManager) updateStatus(sessionID string, nextStatus string, stepOnce bool, messageType string, logMessage string) (*ScriptDebugSession, bool, bool) {
	m.mu.RLock()
	session, ok := m.sessions[sessionID]
	m.mu.RUnlock()
	if !ok {
		return nil, false, false
	}

	session.mu.Lock()
	changed := false
	switch nextStatus {
	case ScriptDebugPaused:
		if session.Status == ScriptDebugRunning {
			session.Status = ScriptDebugPaused
			session.stepOnce = false
			changed = true
		}
	case ScriptDebugRunning:
		if session.Status == ScriptDebugPaused {
			session.Status = ScriptDebugRunning
			session.stepOnce = stepOnce
			changed = true
		}
	}
	if changed {
		session.appendLogLocked("info", "control", logMessage, "", nil)
	}
	snapshot := session.snapshotLocked()
	session.mu.Unlock()

	if changed {
		m.broadcast(messageType, snapshot, snapshot.CurrentEventIndex, nil, nil, nil)
	}
	return snapshot, true, changed
}

func (m *ScriptDebugManager) Stop(sessionID string) (*ScriptDebugSession, bool) {
	m.mu.RLock()
	session, ok := m.sessions[sessionID]
	m.mu.RUnlock()
	if !ok {
		return nil, false
	}

	session.mu.Lock()
	if (session.Status == ScriptDebugRunning || session.Status == ScriptDebugPaused) && session.cancel != nil {
		session.Status = ScriptDebugStopped
		session.stepOnce = false
		session.appendLogLocked("warn", "control", "Stop requested", "", nil)
		session.cancel()
	}
	snapshot := session.snapshotLocked()
	session.mu.Unlock()
	m.broadcast("script_debug_stopped", snapshot, snapshot.CurrentEventIndex, nil, nil, nil)
	return snapshot, true
}

func (m *ScriptDebugManager) UpdateBreakpoints(sessionID string, breakpoints []ScriptDebugBreakpoint) (*ScriptDebugSession, bool) {
	m.mu.RLock()
	session, ok := m.sessions[sessionID]
	m.mu.RUnlock()
	if !ok {
		return nil, false
	}

	session.mu.Lock()
	session.Breakpoints = normalizeDebugBreakpoints(breakpoints)
	session.appendLogLocked("info", "breakpoint", fmt.Sprintf("Updated %d breakpoint(s)", len(session.Breakpoints)), "", nil)
	snapshot := session.snapshotLocked()
	session.mu.Unlock()
	m.broadcast("script_debug_breakpoints_updated", snapshot, snapshot.CurrentEventIndex, nil, nil, nil)
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
		clone.Breakpoints = append([]ScriptDebugBreakpoint(nil), s.Breakpoints...)
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
			if c.session.Status == ScriptDebugRunning && c.session.hasBreakpointLocked(actionCopy) {
				c.session.Status = ScriptDebugPaused
				c.session.stepOnce = false
				c.session.appendLogLocked("info", "breakpoint", fmt.Sprintf("Paused before %s[%d] %s", actionCopy.Phase, actionCopy.ActionIndex, actionCopy.ActionName), "", nil)
			} else {
				c.session.appendLogLocked("debug", "action", fmt.Sprintf("Ready to execute %s[%d] %s", actionCopy.Phase, actionCopy.ActionIndex, actionCopy.ActionName), "", nil)
			}
		}
		status := c.session.Status
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
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (s *ScriptDebugSession) hasBreakpointLocked(action engine.ReplayDebugAction) bool {
	for _, breakpoint := range s.Breakpoints {
		if breakpoint.Phase == action.Phase && breakpoint.ActionIndex == action.ActionIndex {
			return true
		}
	}
	return false
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
		key := fmt.Sprintf("%s:%d", phase, breakpoint.ActionIndex)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		output = append(output, ScriptDebugBreakpoint{Phase: phase, ActionIndex: breakpoint.ActionIndex})
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
