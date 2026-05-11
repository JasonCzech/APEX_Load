package api

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/stormrunner/pkg/dsl"
	"github.com/jasonczech/stormrunner/pkg/engine"
)

const (
	BrowserReplayQueued    = "queued"
	BrowserReplayRunning   = "running"
	BrowserReplayCompleted = "completed"
	BrowserReplayFailed    = "failed"
	BrowserReplayStopped   = "stopped"
)

type BrowserReplaySession struct {
	ID               string               `json:"id"`
	ScriptID         string               `json:"script_id,omitempty"`
	Status           string               `json:"status"`
	Group            string               `json:"group"`
	Protocol         string               `json:"protocol"`
	StartedAt        *time.Time           `json:"started_at,omitempty"`
	CompletedAt      *time.Time           `json:"completed_at,omitempty"`
	CurrentEventIndex int                 `json:"current_event_index"`
	FinalReport      *engine.ReplayReport `json:"report,omitempty"`
	Error            string               `json:"error,omitempty"`

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
		ID:        sessionID,
		ScriptID:  scriptID,
		Status:    BrowserReplayRunning,
		Group:     groupName,
		Protocol:  script.Protocol,
		StartedAt: &now,
		CurrentEventIndex: -1,
		cancel:    cancel,
	}

	m.mu.Lock()
	m.sessions[sessionID] = session
	m.mu.Unlock()

	m.broadcast("browser_replay_started", session, -1, nil, nil)

	ignoreThinkTime := true
	if req.IgnoreThinkTime != nil {
		ignoreThinkTime = *req.IgnoreThinkTime
	}

	go func() {
		report, err := engine.ReplayScript(ctx, script, engine.ReplayOptions{
			GroupName:       groupName,
			Variables:       req.Variables,
			IgnoreThinkTime: ignoreThinkTime,
			OnProgress: func(event engine.ReplayProgressEvent) {
				if event.Type == engine.ReplayProgressEventRun {
					session.mu.Lock()
					session.CurrentEventIndex = event.EventIndex
					session.mu.Unlock()
					m.broadcast("browser_replay_event", session, event.EventIndex, event.Event, nil)
				}
			},
		})

		completedAt := time.Now()
		session.mu.Lock()
		defer session.mu.Unlock()
		session.CompletedAt = &completedAt
		if report != nil {
			session.FinalReport = report
			session.Error = report.Error
		}
		if session.Status == BrowserReplayStopped {
			m.broadcast("browser_replay_stopped", session, session.CurrentEventIndex, nil, report)
			return
		}
		if err != nil {
			session.Status = BrowserReplayFailed
			session.Error = err.Error()
			m.broadcast("browser_replay_failed", session, session.CurrentEventIndex, nil, report)
			return
		}
		if report != nil && !report.Success {
			session.Status = BrowserReplayFailed
			m.broadcast("browser_replay_failed", session, session.CurrentEventIndex, nil, report)
			return
		}
		session.Status = BrowserReplayCompleted
		m.broadcast("browser_replay_completed", session, session.CurrentEventIndex, nil, report)
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

func (m *BrowserReplayManager) Stop(sessionID string) (*BrowserReplaySession, bool) {
	m.mu.RLock()
	session, ok := m.sessions[sessionID]
	m.mu.RUnlock()
	if !ok {
		return nil, false
	}

	session.mu.Lock()
	if session.Status == BrowserReplayRunning && session.cancel != nil {
		session.Status = BrowserReplayStopped
		session.cancel()
	}
	snapshot := session.snapshotLocked()
	session.mu.Unlock()

	m.broadcast("browser_replay_stopped", snapshot, snapshot.CurrentEventIndex, nil, snapshot.FinalReport)
	return snapshot, true
}

func (m *BrowserReplayManager) broadcast(messageType string, session *BrowserReplaySession, eventIndex int, event *engine.ReplayActionEvent, report *engine.ReplayReport) {
	if m.hub == nil || session == nil {
		return
	}
	message := map[string]any{
		"type":              messageType,
		"replay_session_id": session.ID,
		"script_id":         session.ScriptID,
		"timestamp":         time.Now(),
		"session":           session,
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
	clone := *s
	clone.mu = sync.RWMutex{}
	clone.cancel = nil
	return &clone
}