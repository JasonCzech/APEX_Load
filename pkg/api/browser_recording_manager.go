package api

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	browserprotocol "github.com/jasonczech/apexload/pkg/protocol/browser"
	recbrowser "github.com/jasonczech/apexload/pkg/recorder/browser"
	"github.com/jasonczech/apexload/pkg/scrub"
)

// Browser recording session states. A session is "live" while recording,
// paused, or stopping; stopped/failed are terminal.
const (
	BrowserRecordingActive   = "recording"
	BrowserRecordingPaused   = "paused"
	BrowserRecordingStopping = "stopping"
	BrowserRecordingStopped  = "stopped"
	BrowserRecordingFailed   = "failed"
)

// Stop reasons distinguish an explicit stop from the user closing the window.
const (
	browserRecordingStopRequested     = "requested"
	browserRecordingStopBrowserClosed = "browser_closed"
)

var errBrowserRecordingActive = errors.New("a browser recording is already in progress on this controller")

// browserRecorderBackend abstracts recbrowser.Recorder so manager and handler
// tests run without launching Chromium.
type browserRecorderBackend interface {
	Start() error
	Stop() (*recbrowser.Session, error)
	Done() <-chan struct{}
	Snapshot() *recbrowser.Session
	Pause()
	Resume()
	Navigate(url string) error
	Focus() error
	AddMarker(kind, name, text string) (recbrowser.Marker, error)
	SetSection(section string) string
	SetAssertMode(on bool)
	DeleteLastStep() (*recbrowser.Step, error)
	SetStepExcluded(seq int, excluded bool) (*recbrowser.Step, error)
	SetStepMark(seq int, secret, param bool) (*recbrowser.Step, error)
}

// browserRecorderFactory builds the backend for one recording run. The manager
// owns the OnEvent wiring through the options it passes in.
type browserRecorderFactory func(opts recbrowser.Options) browserRecorderBackend

// BrowserRecordingStartRequest is the POST /browser-recorder/sessions body.
type BrowserRecordingStartRequest struct {
	Name       string `json:"name"`
	StartURL   string `json:"start_url"`
	ProjectID  string `json:"project_id,omitempty"`
	CaptureHAR *bool  `json:"capture_har,omitempty"` // default true
	// DevicePreset emulates a mobile/tablet/desktop profile (see GET .../devices).
	DevicePreset string `json:"device_preset,omitempty"`
	// StorageStatePath seeds the context with a saved Playwright storage state.
	StorageStatePath string `json:"storage_state_path,omitempty"`
	// CaptureScreenshots opts into per-step viewport thumbnails.
	CaptureScreenshots bool `json:"capture_screenshots,omitempty"`
	// CaptureWSPayloads opts into recording scrubbed WebSocket frame payloads.
	CaptureWSPayloads   bool `json:"capture_ws_payloads,omitempty"`
	WSPayloadLimitBytes int  `json:"ws_payload_limit_bytes,omitempty"`
	// Section is the initial recording section (init|actions|teardown).
	Section string `json:"section,omitempty"`
	// IncludeHosts/ExcludeHosts filter the side-car HAR by request host.
	IncludeHosts []string `json:"include_hosts,omitempty"`
	ExcludeHosts []string `json:"exclude_hosts,omitempty"`
	// Window position/size hints for the recording browser (desktop ergonomics).
	WindowX      int `json:"window_x,omitempty"`
	WindowY      int `json:"window_y,omitempty"`
	WindowWidth  int `json:"window_width,omitempty"`
	WindowHeight int `json:"window_height,omitempty"`
	// Sidebar toggles the in-page advanced control sidebar embedded in the
	// recorded browser. Default true; set false for only the minimal toolbar.
	Sidebar *bool `json:"sidebar,omitempty"`
}

// BrowserRecordingSession is the API view of one headed-browser recording.
// Steps and Warnings are populated on full snapshots (GET) but omitted from
// the lightweight summaries embedded in WebSocket broadcasts.
type BrowserRecordingSession struct {
	ID                  string                  `json:"id"`
	Name                string                  `json:"name"`
	ProjectID           string                  `json:"project_id,omitempty"`
	Status              string                  `json:"status"`
	StartURL            string                  `json:"start_url"`
	CaptureHAR          bool                    `json:"capture_har"`
	StartedAt           time.Time               `json:"started_at"`
	StoppedAt           *time.Time              `json:"stopped_at,omitempty"`
	StopReason          string                  `json:"stop_reason,omitempty"`
	Error               string                  `json:"error,omitempty"`
	CurrentURL          string                  `json:"current_url,omitempty"`
	StepCount           int                     `json:"step_count"`
	NavigationCount     int                     `json:"navigation_count"`
	NetworkRequestCount int                     `json:"network_request_count"`
	WebSocketCount      int                     `json:"websocket_count"`
	WarningCount        int                     `json:"warning_count"`
	MarkerCount         int                     `json:"marker_count"`
	AnnotationCount     int                     `json:"annotation_count"`
	OpenTransactions    []string                `json:"open_transactions,omitempty"`
	Section             string                  `json:"section,omitempty"`
	DeviceProfile       string                  `json:"device_profile,omitempty"`
	Picking             bool                    `json:"picking,omitempty"`
	HARAvailable        bool                    `json:"har_available"`
	StorageStateReady   bool                    `json:"storage_state_available"`
	Steps               []recbrowser.Step       `json:"steps,omitempty"`
	Markers             []recbrowser.Marker     `json:"markers,omitempty"`
	Annotations         []recbrowser.Annotation `json:"annotations,omitempty"`
	Warnings            []string                `json:"warnings,omitempty"`

	mu      sync.RWMutex
	backend browserRecorderBackend
	final   *recbrowser.Session
}

// BrowserRecordingManager owns the controller's headed-browser recordings: one
// live session at a time, lifecycle transitions, and hub broadcasts.
type BrowserRecordingManager struct {
	hub      *Hub
	mu       sync.RWMutex
	sessions map[string]*BrowserRecordingSession
	activeID string
	factory  browserRecorderFactory
}

func NewBrowserRecordingManager(hub *Hub) *BrowserRecordingManager {
	return &BrowserRecordingManager{
		hub:      hub,
		sessions: make(map[string]*BrowserRecordingSession),
		factory: func(opts recbrowser.Options) browserRecorderBackend {
			return recbrowser.New(opts)
		},
	}
}

// Start launches a headed recording browser. It blocks until the browser is up
// (or failed to launch) and enforces a single live recording per controller.
func (m *BrowserRecordingManager) Start(req BrowserRecordingStartRequest) (*BrowserRecordingSession, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "Browser Recording"
	}
	captureHAR := true
	if req.CaptureHAR != nil {
		captureHAR = *req.CaptureHAR
	}
	sidebar := true
	if req.Sidebar != nil {
		sidebar = *req.Sidebar
	}

	var device *recbrowser.DeviceEmulation
	deviceName := ""
	if preset := strings.TrimSpace(req.DevicePreset); preset != "" {
		profile, ok := browserprotocol.LookupDevice(preset)
		if !ok {
			return nil, fmt.Errorf("unknown device preset %q", preset)
		}
		deviceName = profile.Name
		device = &recbrowser.DeviceEmulation{
			Name:              profile.Name,
			Width:             profile.Width,
			Height:            profile.Height,
			DeviceScaleFactor: profile.DeviceScaleFactor,
			Mobile:            profile.Mobile,
			HasTouch:          profile.HasTouch,
			UserAgent:         profile.UserAgent,
		}
	}

	session := &BrowserRecordingSession{
		ID:            uuid.New().String(),
		Name:          name,
		ProjectID:     normalizeProjectID(req.ProjectID),
		Status:        BrowserRecordingActive,
		StartURL:      strings.TrimSpace(req.StartURL),
		CaptureHAR:    captureHAR,
		StartedAt:     time.Now(),
		CurrentURL:    strings.TrimSpace(req.StartURL),
		Section:       recbrowserNormalizeSection(req.Section),
		DeviceProfile: deviceName,
	}

	m.mu.Lock()
	if m.activeID != "" {
		if active, ok := m.sessions[m.activeID]; ok && active.isLive() {
			m.mu.Unlock()
			return nil, errBrowserRecordingActive
		}
	}
	m.activeID = session.ID
	m.sessions[session.ID] = session
	m.mu.Unlock()

	backend := m.factory(recbrowser.Options{
		StartURL:            session.StartURL,
		CaptureHAR:          captureHAR,
		OnEvent:             func(event recbrowser.Event) { m.handleEvent(session, event) },
		Device:              device,
		StorageStatePath:    strings.TrimSpace(req.StorageStatePath),
		CaptureScreenshots:  req.CaptureScreenshots,
		CaptureWSPayloads:   req.CaptureWSPayloads,
		WSPayloadLimitBytes: req.WSPayloadLimitBytes,
		Section:             session.Section,
		HARFilters: recbrowser.HARFilters{
			IncludeHosts: req.IncludeHosts,
			ExcludeHosts: req.ExcludeHosts,
		},
		WindowX:      req.WindowX,
		WindowY:      req.WindowY,
		WindowWidth:  req.WindowWidth,
		WindowHeight: req.WindowHeight,
		Sidebar:      sidebar,
		Scrubber:     func(s string) string { return scrub.Value(s, scrub.Policy{}) },
	})
	session.mu.Lock()
	session.backend = backend
	session.mu.Unlock()

	if err := backend.Start(); err != nil {
		now := time.Now()
		session.mu.Lock()
		session.Status = BrowserRecordingFailed
		session.Error = err.Error()
		session.StoppedAt = &now
		session.mu.Unlock()
		m.clearActive(session.ID)
		m.broadcast("browser_recording_failed", session, nil)
		return nil, err
	}

	go m.watchDone(session)
	m.broadcast("browser_recording_started", session, nil)
	return session.fullSnapshot(), nil
}

// Get returns a full snapshot (including steps) of one session.
func (m *BrowserRecordingManager) Get(id string) (*BrowserRecordingSession, bool) {
	m.mu.RLock()
	session, ok := m.sessions[id]
	m.mu.RUnlock()
	if !ok {
		return nil, false
	}
	return session.fullSnapshot(), true
}

// Active returns a summary of the live session, or nil when idle.
func (m *BrowserRecordingManager) Active() *BrowserRecordingSession {
	m.mu.RLock()
	session := m.sessions[m.activeID]
	m.mu.RUnlock()
	if session == nil || !session.isLive() {
		return nil
	}
	return session.summary()
}

// Stop ends a recording: the browser closes, the side-car HAR is collected,
// and the terminal state broadcasts. Idempotent for already-stopped sessions.
func (m *BrowserRecordingManager) Stop(id string) (*BrowserRecordingSession, bool) {
	m.mu.RLock()
	session, ok := m.sessions[id]
	m.mu.RUnlock()
	if !ok {
		return nil, false
	}

	session.mu.Lock()
	if session.Status != BrowserRecordingActive && session.Status != BrowserRecordingPaused {
		session.mu.Unlock()
		return session.fullSnapshot(), true
	}
	session.Status = BrowserRecordingStopping
	session.StopReason = browserRecordingStopRequested
	session.mu.Unlock()

	m.finishStop(session)
	return session.fullSnapshot(), true
}

// Pause suspends capture. The status flip and broadcast flow through the
// recorder's pause_state event so panel-driven and in-page-toolbar-driven
// pauses behave identically.
func (m *BrowserRecordingManager) Pause(id string) (*BrowserRecordingSession, bool, error) {
	return m.setPaused(id, true)
}

// Resume re-enables capture after Pause.
func (m *BrowserRecordingManager) Resume(id string) (*BrowserRecordingSession, bool, error) {
	return m.setPaused(id, false)
}

func (m *BrowserRecordingManager) setPaused(id string, paused bool) (*BrowserRecordingSession, bool, error) {
	m.mu.RLock()
	session, ok := m.sessions[id]
	m.mu.RUnlock()
	if !ok {
		return nil, false, nil
	}
	session.mu.RLock()
	status := session.Status
	backend := session.backend
	session.mu.RUnlock()
	if status != BrowserRecordingActive && status != BrowserRecordingPaused {
		return session.fullSnapshot(), true, fmt.Errorf("recording is not live (status %s)", status)
	}
	if backend == nil {
		return session.fullSnapshot(), true, fmt.Errorf("browser recorder backend unavailable")
	}
	if paused {
		backend.Pause()
	} else {
		backend.Resume()
	}
	return session.fullSnapshot(), true, nil
}

// Navigate drives the recording browser to a URL from the control panel.
func (m *BrowserRecordingManager) Navigate(id string, url string) (*BrowserRecordingSession, bool, error) {
	session, backend, err := m.liveBackend(id)
	if session == nil {
		return nil, false, nil
	}
	if err != nil {
		return session.fullSnapshot(), true, err
	}
	if err := backend.Navigate(url); err != nil {
		return session.fullSnapshot(), true, err
	}
	return session.fullSnapshot(), true, nil
}

// Focus brings the recording browser window to the front.
func (m *BrowserRecordingManager) Focus(id string) (*BrowserRecordingSession, bool, error) {
	session, backend, err := m.liveBackend(id)
	if session == nil {
		return nil, false, nil
	}
	if err != nil {
		return session.fullSnapshot(), true, err
	}
	if err := backend.Focus(); err != nil {
		return session.fullSnapshot(), true, err
	}
	return session.fullSnapshot(), true, nil
}

// AddMarker inserts a transaction boundary or note into the live recording.
func (m *BrowserRecordingManager) AddMarker(id string, kind, name, text string) (*BrowserRecordingSession, *recbrowser.Marker, bool, error) {
	session, backend, err := m.liveBackend(id)
	if session == nil {
		return nil, nil, false, nil
	}
	if err != nil {
		return session.fullSnapshot(), nil, true, err
	}
	marker, err := backend.AddMarker(kind, name, text)
	if err != nil {
		return session.fullSnapshot(), nil, true, err
	}
	return session.fullSnapshot(), &marker, true, nil
}

// SetSection switches the active recording section (init|actions|teardown).
func (m *BrowserRecordingManager) SetSection(id, section string) (*BrowserRecordingSession, bool, error) {
	session, backend, err := m.liveBackend(id)
	if session == nil {
		return nil, false, nil
	}
	if err != nil {
		return session.fullSnapshot(), true, err
	}
	backend.SetSection(section)
	return session.fullSnapshot(), true, nil
}

// SetAssertMode toggles the in-page element picker (verification mode).
func (m *BrowserRecordingManager) SetAssertMode(id string, on bool) (*BrowserRecordingSession, bool, error) {
	session, backend, err := m.liveBackend(id)
	if session == nil {
		return nil, false, nil
	}
	if err != nil {
		return session.fullSnapshot(), true, err
	}
	backend.SetAssertMode(on)
	return session.fullSnapshot(), true, nil
}

// DeleteLastStep tombstones the most recent step.
func (m *BrowserRecordingManager) DeleteLastStep(id string) (*BrowserRecordingSession, bool, error) {
	session, backend, err := m.liveBackend(id)
	if session == nil {
		return nil, false, nil
	}
	if err != nil {
		return session.fullSnapshot(), true, err
	}
	if _, err := backend.DeleteLastStep(); err != nil {
		return session.fullSnapshot(), true, err
	}
	return session.fullSnapshot(), true, nil
}

// SetStepExcluded toggles whether a step is skipped at conversion.
func (m *BrowserRecordingManager) SetStepExcluded(id string, seq int, excluded bool) (*BrowserRecordingSession, bool, error) {
	session, backend, err := m.liveBackend(id)
	if session == nil {
		return nil, false, nil
	}
	if err != nil {
		return session.fullSnapshot(), true, err
	}
	if _, err := backend.SetStepExcluded(seq, excluded); err != nil {
		return session.fullSnapshot(), true, err
	}
	return session.fullSnapshot(), true, nil
}

// SetStepMark flags a fill step as a runtime secret or data parameter.
func (m *BrowserRecordingManager) SetStepMark(id string, seq int, secret, param bool) (*BrowserRecordingSession, bool, error) {
	session, backend, err := m.liveBackend(id)
	if session == nil {
		return nil, false, nil
	}
	if err != nil {
		return session.fullSnapshot(), true, err
	}
	if _, err := backend.SetStepMark(seq, secret, param); err != nil {
		return session.fullSnapshot(), true, err
	}
	return session.fullSnapshot(), true, nil
}

// SuggestTransactions derives transaction-boundary suggestions from the
// recording's navigations.
func (m *BrowserRecordingManager) SuggestTransactions(id string) ([]recbrowser.TransactionSuggestion, bool) {
	artifact, _, ok := m.Artifact(id)
	if !ok {
		return nil, false
	}
	if artifact == nil {
		return nil, true
	}
	return recbrowser.SuggestTransactionsFromSession(artifact), true
}

// liveBackend resolves a session and its backend, erroring when the recording
// is no longer controllable. A nil session means not found.
func (m *BrowserRecordingManager) liveBackend(id string) (*BrowserRecordingSession, browserRecorderBackend, error) {
	m.mu.RLock()
	session, ok := m.sessions[id]
	m.mu.RUnlock()
	if !ok {
		return nil, nil, nil
	}
	session.mu.RLock()
	status := session.Status
	backend := session.backend
	session.mu.RUnlock()
	if status != BrowserRecordingActive && status != BrowserRecordingPaused {
		return session, nil, fmt.Errorf("recording is not live (status %s)", status)
	}
	if backend == nil {
		return session, nil, fmt.Errorf("browser recorder backend unavailable")
	}
	return session, backend, nil
}

// Artifact returns the underlying recorded session for conversion or HAR
// export, plus a summary of the API session. The artifact must be treated as
// read-only; for live sessions it is a stable snapshot copy.
func (m *BrowserRecordingManager) Artifact(id string) (*recbrowser.Session, *BrowserRecordingSession, bool) {
	m.mu.RLock()
	session, ok := m.sessions[id]
	m.mu.RUnlock()
	if !ok {
		return nil, nil, false
	}
	return session.artifact(), session.summary(), true
}

// NotifyConverted broadcasts that a recording was saved as a script.
func (m *BrowserRecordingManager) NotifyConverted(id string, scriptID string, scriptName string) {
	m.mu.RLock()
	session, ok := m.sessions[id]
	m.mu.RUnlock()
	if !ok {
		return
	}
	m.broadcast("browser_recording_converted", session, map[string]any{
		"script_id":   scriptID,
		"script_name": scriptName,
	})
}

// watchDone waits for the browser to disappear out from under the recording
// (user closed the window). The HAR still finalizes through backend.Stop().
func (m *BrowserRecordingManager) watchDone(session *BrowserRecordingSession) {
	session.mu.RLock()
	backend := session.backend
	session.mu.RUnlock()
	if backend == nil {
		return
	}
	<-backend.Done()

	session.mu.Lock()
	if session.Status != BrowserRecordingActive && session.Status != BrowserRecordingPaused {
		session.mu.Unlock()
		return
	}
	session.Status = BrowserRecordingStopping
	session.StopReason = browserRecordingStopBrowserClosed
	session.mu.Unlock()
	m.finishStop(session)
}

// finishStop drives the backend to completion and broadcasts the terminal
// state. Exactly one goroutine reaches it per session (the recording→stopping
// transition is guarded by the session mutex).
func (m *BrowserRecordingManager) finishStop(session *BrowserRecordingSession) {
	final, err := session.stopBackend()
	now := time.Now()
	session.mu.Lock()
	session.StoppedAt = &now
	if err != nil {
		session.Status = BrowserRecordingFailed
		session.Error = err.Error()
	} else {
		session.Status = BrowserRecordingStopped
		session.final = final
	}
	session.mu.Unlock()
	m.clearActive(session.ID)

	if err != nil {
		m.broadcast("browser_recording_failed", session, nil)
		return
	}
	m.broadcast("browser_recording_stopped", session, nil)
}

func (m *BrowserRecordingManager) handleEvent(session *BrowserRecordingSession, event recbrowser.Event) {
	switch event.Type {
	case recbrowser.EventStep:
		if event.Step == nil {
			return
		}
		session.mu.Lock()
		if event.Step.Seq > session.StepCount {
			session.StepCount = event.Step.Seq
		}
		if event.Step.URL != "" {
			session.CurrentURL = event.Step.URL
		}
		session.mu.Unlock()
		m.broadcast("browser_recording_step", session, map[string]any{"step": event.Step})
	case recbrowser.EventNavigation:
		if event.Nav == nil {
			return
		}
		session.mu.Lock()
		session.NavigationCount++
		session.CurrentURL = event.Nav.URL
		session.mu.Unlock()
		m.broadcast("browser_recording_navigation", session, map[string]any{"url": event.Nav.URL})
	case recbrowser.EventWarning:
		session.mu.Lock()
		session.WarningCount++
		session.mu.Unlock()
		m.broadcast("browser_recording_warning", session, map[string]any{"warning": event.Warning})
	case recbrowser.EventPauseState:
		messageType := ""
		session.mu.Lock()
		switch {
		case event.Paused && session.Status == BrowserRecordingActive:
			session.Status = BrowserRecordingPaused
			messageType = "browser_recording_paused"
		case !event.Paused && session.Status == BrowserRecordingPaused:
			session.Status = BrowserRecordingActive
			messageType = "browser_recording_resumed"
		}
		session.mu.Unlock()
		if messageType != "" {
			m.broadcast(messageType, session, nil)
		}
	case recbrowser.EventMarker:
		if event.Marker == nil {
			return
		}
		session.mu.Lock()
		session.MarkerCount++
		switch event.Marker.Kind {
		case recbrowser.MarkerTransactionStart:
			session.OpenTransactions = append(session.OpenTransactions, event.Marker.Name)
		case recbrowser.MarkerTransactionEnd:
			for i, name := range session.OpenTransactions {
				if name == event.Marker.Name {
					session.OpenTransactions = append(session.OpenTransactions[:i], session.OpenTransactions[i+1:]...)
					break
				}
			}
		}
		session.mu.Unlock()
		m.broadcast("browser_recording_marker", session, map[string]any{"marker": event.Marker})
	case recbrowser.EventPickState:
		session.mu.Lock()
		session.Picking = event.Picking
		session.mu.Unlock()
		m.broadcast("browser_recording_pick", session, map[string]any{"picking": event.Picking})
	case recbrowser.EventSection:
		session.mu.Lock()
		session.Section = event.Section
		session.mu.Unlock()
		m.broadcast("browser_recording_section", session, map[string]any{"section": event.Section})
	case recbrowser.EventAnnotation:
		if event.Annotation == nil {
			return
		}
		session.mu.Lock()
		session.AnnotationCount++
		session.mu.Unlock()
		m.broadcast("browser_recording_annotation", session, map[string]any{"annotation": event.Annotation})
	case recbrowser.EventStepUpdate:
		if event.Step == nil {
			return
		}
		m.broadcast("browser_recording_step_update", session, map[string]any{"step": event.Step})
	}
}

// recbrowserNormalizeSection normalizes a requested recording section the same
// way the recorder does (empty/unknown → actions).
func recbrowserNormalizeSection(section string) string {
	switch strings.ToLower(strings.TrimSpace(section)) {
	case recbrowser.SectionInit:
		return recbrowser.SectionInit
	case recbrowser.SectionTeardown:
		return recbrowser.SectionTeardown
	default:
		return recbrowser.SectionActions
	}
}

func (m *BrowserRecordingManager) clearActive(id string) {
	m.mu.Lock()
	if m.activeID == id {
		m.activeID = ""
	}
	m.mu.Unlock()
}

func (m *BrowserRecordingManager) broadcast(messageType string, session *BrowserRecordingSession, extra map[string]any) {
	if m.hub == nil || session == nil {
		return
	}
	summary := session.summary()
	message := map[string]any{
		"type":                 messageType,
		"recording_session_id": summary.ID,
		"project_id":           summary.ProjectID,
		"timestamp":            time.Now(),
		"session":              summary,
	}
	for key, value := range extra {
		message[key] = value
	}
	m.hub.BroadcastJSON(message)
}

func (s *BrowserRecordingSession) isLive() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Status == BrowserRecordingActive || s.Status == BrowserRecordingPaused || s.Status == BrowserRecordingStopping
}

func (s *BrowserRecordingSession) stopBackend() (*recbrowser.Session, error) {
	s.mu.RLock()
	backend := s.backend
	s.mu.RUnlock()
	if backend == nil {
		return nil, fmt.Errorf("browser recorder backend unavailable")
	}
	return backend.Stop()
}

// artifact returns the finished session after stop, or a live snapshot copy
// while recording.
func (s *BrowserRecordingSession) artifact() *recbrowser.Session {
	s.mu.RLock()
	final := s.final
	backend := s.backend
	s.mu.RUnlock()
	if final != nil {
		return final
	}
	if backend != nil {
		return backend.Snapshot()
	}
	return nil
}

func (s *BrowserRecordingSession) summary() *BrowserRecordingSession {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.summaryLocked()
}

func (s *BrowserRecordingSession) summaryLocked() *BrowserRecordingSession {
	return &BrowserRecordingSession{
		ID:                  s.ID,
		Name:                s.Name,
		ProjectID:           s.ProjectID,
		Status:              s.Status,
		StartURL:            s.StartURL,
		CaptureHAR:          s.CaptureHAR,
		StartedAt:           s.StartedAt,
		StoppedAt:           s.StoppedAt,
		StopReason:          s.StopReason,
		Error:               s.Error,
		CurrentURL:          s.CurrentURL,
		StepCount:           s.StepCount,
		NavigationCount:     s.NavigationCount,
		NetworkRequestCount: s.NetworkRequestCount,
		WebSocketCount:      s.WebSocketCount,
		WarningCount:        s.WarningCount,
		MarkerCount:         s.MarkerCount,
		AnnotationCount:     s.AnnotationCount,
		OpenTransactions:    append([]string(nil), s.OpenTransactions...),
		Section:             s.Section,
		DeviceProfile:       s.DeviceProfile,
		Picking:             s.Picking,
		HARAvailable:        s.HARAvailable,
		StorageStateReady:   s.StorageStateReady,
	}
}

// fullSnapshot resolves the freshest artifact and folds its contents (steps,
// warnings, exact counts) into the API session view.
func (s *BrowserRecordingSession) fullSnapshot() *BrowserRecordingSession {
	artifact := s.artifact()
	clone := s.summary()
	if artifact == nil {
		return clone
	}
	clone.Steps = visibleSteps(artifact.Steps)
	clone.Markers = artifact.Markers
	clone.Annotations = artifact.Annotations
	clone.Warnings = artifact.Warnings
	clone.StepCount = countVisibleSteps(artifact.Steps)
	clone.NavigationCount = len(artifact.NavEvents)
	clone.NetworkRequestCount = len(artifact.NetworkEvents)
	clone.WebSocketCount = len(artifact.WebSockets)
	clone.WarningCount = len(artifact.Warnings)
	clone.MarkerCount = len(artifact.Markers)
	clone.AnnotationCount = len(artifact.Annotations)
	clone.OpenTransactions = artifact.OpenTransactions
	clone.HARAvailable = len(artifact.HAR) > 0
	clone.StorageStateReady = len(artifact.StorageState) > 0
	if artifact.DeviceProfile != "" {
		clone.DeviceProfile = artifact.DeviceProfile
	}
	if len(artifact.NavEvents) > 0 {
		clone.CurrentURL = artifact.NavEvents[len(artifact.NavEvents)-1].URL
	}
	return clone
}

// visibleSteps drops deleted steps from the API view (excluded steps stay,
// flagged, so the user can re-include them).
func visibleSteps(steps []recbrowser.Step) []recbrowser.Step {
	out := make([]recbrowser.Step, 0, len(steps))
	for _, s := range steps {
		if !s.Deleted {
			out = append(out, s)
		}
	}
	return out
}

func countVisibleSteps(steps []recbrowser.Step) int {
	n := 0
	for _, s := range steps {
		if !s.Deleted && !s.Excluded {
			n++
		}
	}
	return n
}
