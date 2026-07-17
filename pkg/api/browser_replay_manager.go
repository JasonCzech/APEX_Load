package api

import (
	"context"
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
}

// replayCompletionHook is invoked once a replay session reaches a terminal
// report. The server wires it to feed selector self-heals back to the object
// repository; the managers themselves hold no store.
type replayCompletionHook func(projectID string, script *dsl.Script, report *engine.ReplayReport)

type BrowserReplayManager struct {
	hub           *Hub
	sessions      map[string]*BrowserReplaySession
	mu            sync.RWMutex
	onComplete    replayCompletionHook
	retention     replaySessionRetentionConfig
	janitorCancel context.CancelFunc
}

func NewBrowserReplayManager(hub *Hub) *BrowserReplayManager {
	return &BrowserReplayManager{
		hub:       hub,
		sessions:  make(map[string]*BrowserReplaySession),
		retention: defaultReplaySessionRetentionConfig(),
	}
}

func (m *BrowserReplayManager) setCompletionHook(hook replayCompletionHook) {
	m.onComplete = hook
}

func (m *BrowserReplayManager) StartJanitor(ctx context.Context) {
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

func (m *BrowserReplayManager) Close() {
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

func (m *BrowserReplayManager) Start(scriptID string, script *dsl.Script, req BrowserReplayStartRequest) *BrowserReplaySession {
	sessionID := strings.TrimSpace(req.ReplaySessionID)
	if sessionID == "" {
		sessionID = uuid.New().String()
	}
	groupName := req.GroupName
	if groupName == "" {
		groupName = script.Name + "-browser-replay"
	}
	now := m.retention.clock()
	ctx, cancel := context.WithCancel(context.Background())
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
	}
	if req.StartPaused {
		session.Status = BrowserReplayPaused
	}

	m.mu.Lock()
	m.pruneExpiredLocked(now)
	if existing := m.sessions[sessionID]; existing != nil {
		existing.mu.Lock()
		m.cancelSessionLocked(existing, now)
		existing.mu.Unlock()
		delete(m.sessions, sessionID)
	}
	m.sessions[sessionID] = session
	m.enforceMaxSessionsLocked(now, sessionID)
	m.mu.Unlock()

	m.broadcast("browser_replay_started", session, -1, nil, nil)
	if req.StartPaused {
		m.broadcast("browser_replay_paused", session, -1, nil, nil)
	}

	go func() {
		if !m.waitWhilePaused(ctx, session) {
			completedAt := m.retention.clock()
			session.mu.Lock()
			session.CompletedAt = &completedAt
			if replaySessionStatusTerminal(session.Status) && session.terminalAt == nil {
				session.terminalAt = &completedAt
			}
			session.signalStateChangedLocked()
			eventIndex := session.CurrentEventIndex
			session.mu.Unlock()
			m.broadcast("browser_replay_stopped", session, eventIndex, nil, nil)
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
				if event.Type == engine.ReplayProgressEventRun {
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
					_ = m.waitWhilePaused(ctx, session)
				}
			},
		})

		completedAt := m.retention.clock()
		messageType := "browser_replay_completed"
		session.mu.Lock()
		session.CompletedAt = &completedAt
		if report != nil {
			session.FinalReport = report
			session.Error = report.Error
		}
		if session.Status == BrowserReplayStopped {
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
		if replaySessionStatusTerminal(session.Status) && session.terminalAt == nil {
			session.terminalAt = &completedAt
		}
		session.signalStateChangedLocked()
		eventIndex := session.CurrentEventIndex
		session.mu.Unlock()

		m.broadcast(messageType, session, eventIndex, nil, report)

		if m.onComplete != nil && report != nil {
			m.onComplete(session.ProjectID, script, report)
		}
	}()

	return session.snapshot()
}

func (m *BrowserReplayManager) PruneExpired() int {
	if m == nil {
		return 0
	}
	now := m.retention.clock()
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pruneExpiredLocked(now)
}

func (m *BrowserReplayManager) pruneExpiredLocked(now time.Time) int {
	removed := 0
	for id, session := range m.sessions {
		session.mu.Lock()
		m.cancelIdleRunningSessionLocked(session, now)
		if m.shouldPruneSessionLocked(session, now) {
			m.cancelSessionLocked(session, now)
			delete(m.sessions, id)
			removed++
		}
		session.mu.Unlock()
	}
	return removed + m.enforceMaxSessionsLocked(now, "")
}

func (m *BrowserReplayManager) enforceMaxSessionsLocked(now time.Time, protectedID string) int {
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
		m.cancelSessionLocked(session, now)
		session.mu.Unlock()
		delete(m.sessions, id)
		removed++
	}
	return removed
}

func (m *BrowserReplayManager) oldestPrunableSessionLocked(protectedID string, terminalOnly bool) (string, *BrowserReplaySession) {
	var selectedID string
	var selected *BrowserReplaySession
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

func (m *BrowserReplayManager) cancelSessionLocked(session *BrowserReplaySession, now time.Time) {
	if session == nil {
		return
	}
	if session.Status == BrowserReplayRunning || session.Status == BrowserReplayPaused {
		session.Status = BrowserReplayStopped
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

func (m *BrowserReplayManager) Get(sessionID string) (*BrowserReplaySession, bool) {
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

func (m *BrowserReplayManager) Pause(sessionID string) (*BrowserReplaySession, bool, bool) {
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
	if session.Status == BrowserReplayRunning {
		session.Status = BrowserReplayPaused
		session.signalStateChangedLocked()
		changed = true
	}
	snapshot := session.snapshotLocked()
	session.mu.Unlock()

	if changed {
		m.broadcast("browser_replay_paused", snapshot, snapshot.CurrentEventIndex, nil, snapshot.FinalReport)
	}
	return snapshot, true, changed
}

func (m *BrowserReplayManager) Resume(sessionID string) (*BrowserReplaySession, bool, bool) {
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
	if session.Status == BrowserReplayPaused {
		session.Status = BrowserReplayRunning
		session.signalStateChangedLocked()
		changed = true
	}
	snapshot := session.snapshotLocked()
	session.mu.Unlock()

	if changed {
		m.broadcast("browser_replay_resumed", snapshot, snapshot.CurrentEventIndex, nil, snapshot.FinalReport)
	}
	return snapshot, true, changed
}

func (m *BrowserReplayManager) Stop(sessionID string) (*BrowserReplaySession, bool) {
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
	if session.Status == BrowserReplayRunning || session.Status == BrowserReplayPaused {
		session.Status = BrowserReplayStopped
		if session.cancel != nil {
			session.cancel()
		}
		session.signalStateChangedLocked()
	}
	if replaySessionStatusTerminal(session.Status) && session.terminalAt == nil {
		terminalAt := now
		session.terminalAt = &terminalAt
	}
	snapshot := session.snapshotLocked()
	session.mu.Unlock()
	return snapshot, true
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
		StartedAt:         s.StartedAt,
		CompletedAt:       s.CompletedAt,
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
	cloned.Headers = cloneBrowserStringMap(event.Headers)
	cloned.Extractions = cloneBrowserStringMap(event.Extractions)
	cloned.Console = cloneBrowserConsoleEntries(event.Console)
	cloned.Network = cloneBrowserNetworkEntries(event.Network)
	cloned.DOMSummary = cloneBrowserDOMSummary(event.DOMSummary)
	cloned.ClientMetrics = cloneBrowserClientMetrics(event.ClientMetrics)
	cloned.Diagnosis = cloneBrowserReplayDiagnosis(event.Diagnosis)
	cloned.RepairSuggestions = cloneBrowserReplayRepairSuggestions(event.RepairSuggestions)
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
