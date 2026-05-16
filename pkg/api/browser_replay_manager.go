package api

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/stormrunner/pkg/dsl"
	"github.com/jasonczech/stormrunner/pkg/engine"
	"github.com/jasonczech/stormrunner/pkg/protocol"
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
	ID                string                     `json:"id"`
	ScriptID          string                     `json:"script_id,omitempty"`
	ProjectID         string                     `json:"project_id,omitempty"`
	Status            string                     `json:"status"`
	Group             string                     `json:"group"`
	Protocol          string                     `json:"protocol"`
	StartedAt         *time.Time                 `json:"started_at,omitempty"`
	CompletedAt       *time.Time                 `json:"completed_at,omitempty"`
	CurrentEventIndex int                        `json:"current_event_index"`
	CurrentEvent      *engine.ReplayActionEvent  `json:"current_event,omitempty"`
	Events            []engine.ReplayActionEvent `json:"events,omitempty"`
	FinalReport       *engine.ReplayReport       `json:"report,omitempty"`
	Error             string                     `json:"error,omitempty"`

	mu     sync.RWMutex
	cancel context.CancelFunc
}

type BrowserReplayManager struct {
	hub      *Hub
	sessions map[string]*BrowserReplaySession
	mu       sync.RWMutex
}

func NewBrowserReplayManager(hub *Hub) *BrowserReplayManager {
	return &BrowserReplayManager{
		hub:      hub,
		sessions: make(map[string]*BrowserReplaySession),
	}
}

func (m *BrowserReplayManager) Start(scriptID string, script *dsl.Script, req BrowserReplayStartRequest) *BrowserReplaySession {
	sessionID := req.ReplaySessionID
	if sessionID == "" {
		sessionID = uuid.New().String()
	}
	groupName := req.GroupName
	if groupName == "" {
		groupName = script.Name + "-browser-replay"
	}
	now := time.Now()
	ctx, cancel := context.WithCancel(context.Background())
	session := &BrowserReplaySession{
		ID:                sessionID,
		ScriptID:          scriptID,
		ProjectID:         normalizeProjectID(script.ProjectID),
		Status:            BrowserReplayRunning,
		Group:             groupName,
		Protocol:          script.Protocol,
		StartedAt:         &now,
		CurrentEventIndex: -1,
		cancel:            cancel,
	}

	m.mu.Lock()
	m.sessions[sessionID] = session
	m.mu.Unlock()

	m.broadcast("browser_replay_started", session, -1, nil, nil)
	if req.StartPaused {
		session.mu.Lock()
		session.Status = BrowserReplayPaused
		session.mu.Unlock()
		m.broadcast("browser_replay_paused", session, -1, nil, nil)
	}

	ignoreThinkTime := true
	if req.IgnoreThinkTime != nil {
		ignoreThinkTime = *req.IgnoreThinkTime
	}

	go func() {
		if !m.waitWhilePaused(ctx, session) {
			completedAt := time.Now()
			session.mu.Lock()
			session.CompletedAt = &completedAt
			eventIndex := session.CurrentEventIndex
			session.mu.Unlock()
			m.broadcast("browser_replay_stopped", session, eventIndex, nil, nil)
			return
		}

		report, err := engine.ReplayScript(ctx, script, engine.ReplayOptions{
			GroupName:       groupName,
			Variables:       req.Variables,
			RedactionRules:  req.RedactionRules,
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

		completedAt := time.Now()
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
		eventIndex := session.CurrentEventIndex
		session.mu.Unlock()

		m.broadcast(messageType, session, eventIndex, nil, report)
	}()

	return session.snapshot()
}

func (m *BrowserReplayManager) Get(sessionID string) (*BrowserReplaySession, bool) {
	m.mu.RLock()
	session, ok := m.sessions[sessionID]
	m.mu.RUnlock()
	if !ok {
		return nil, false
	}
	return session.snapshot(), true
}

func (m *BrowserReplayManager) Pause(sessionID string) (*BrowserReplaySession, bool, bool) {
	m.mu.RLock()
	session, ok := m.sessions[sessionID]
	m.mu.RUnlock()
	if !ok {
		return nil, false, false
	}

	session.mu.Lock()
	changed := false
	if session.Status == BrowserReplayRunning {
		session.Status = BrowserReplayPaused
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
	m.mu.RLock()
	session, ok := m.sessions[sessionID]
	m.mu.RUnlock()
	if !ok {
		return nil, false, false
	}

	session.mu.Lock()
	changed := false
	if session.Status == BrowserReplayPaused {
		session.Status = BrowserReplayRunning
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
	m.mu.RLock()
	session, ok := m.sessions[sessionID]
	m.mu.RUnlock()
	if !ok {
		return nil, false
	}

	session.mu.Lock()
	if (session.Status == BrowserReplayRunning || session.Status == BrowserReplayPaused) && session.cancel != nil {
		session.Status = BrowserReplayStopped
		session.cancel()
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
		ScriptID:          s.ScriptID,
		ProjectID:         s.ProjectID,
		Status:            s.Status,
		Group:             s.Group,
		Protocol:          s.Protocol,
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

func cloneBrowserReplayReport(report *engine.ReplayReport) *engine.ReplayReport {
	if report == nil {
		return nil
	}
	cloned := *report
	cloned.FinalVariables = cloneBrowserStringMap(report.FinalVariables)
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
	if len(entries) == 0 {
		return nil
	}
	cloned := make([]protocol.NetworkEntry, len(entries))
	copy(cloned, entries)
	return cloned
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

func (m *BrowserReplayManager) waitWhilePaused(ctx context.Context, session *BrowserReplaySession) bool {
	for {
		session.mu.RLock()
		status := session.Status
		session.mu.RUnlock()

		switch status {
		case BrowserReplayPaused:
			select {
			case <-ctx.Done():
				return false
			case <-time.After(50 * time.Millisecond):
			}
		case BrowserReplayStopped, BrowserReplayFailed, BrowserReplayCompleted:
			return false
		default:
			return true
		}
	}
}
