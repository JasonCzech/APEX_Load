package api

import (
	"context"
	"errors"
	"fmt"
	"sort"
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
	browserRecordingStopForced        = "forced"
)

var errBrowserRecordingActive = errors.New("a browser recording is already in progress on this controller")

const defaultBrowserRecordingStopTimeout = 20 * time.Second
const defaultBrowserRecordingRetention = 24 * time.Hour

// browserRecorderBackend abstracts recbrowser.Recorder so manager and handler
// tests run without launching Chromium.
type browserRecorderBackend interface {
	Start() error
	Stop() (*recbrowser.Session, error)
	ForceStop()
	Done() <-chan struct{}
	Snapshot() *recbrowser.Session
	Pause()
	Resume()
	Navigate(url string) error
	Focus() error
	OpenControlPanel(url string, localStorage map[string]string) error
	AddMarker(kind, name, text string, atStepSeq int) (recbrowser.Marker, error)
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
	hub            *Hub
	mu             sync.RWMutex
	sessions       map[string]*BrowserRecordingSession
	activeID       string
	factory        browserRecorderFactory
	stopTimeout    time.Duration
	retention      time.Duration
	harArtifactDir string
	janitorCancel  context.CancelFunc
}

func NewBrowserRecordingManager(hub *Hub) *BrowserRecordingManager {
	return &BrowserRecordingManager{
		hub:         hub,
		sessions:    make(map[string]*BrowserRecordingSession),
		stopTimeout: defaultBrowserRecordingStopTimeout,
		retention:   defaultBrowserRecordingRetention,
		factory: func(opts recbrowser.Options) browserRecorderBackend {
			return recbrowser.New(opts)
		},
	}
}

func (m *BrowserRecordingManager) SetHARArtifactDir(dir string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.harArtifactDir = strings.TrimSpace(dir)
	m.mu.Unlock()
}

func (m *BrowserRecordingManager) StartJanitor(ctx context.Context) {
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
	interval := m.retention / 2
	if interval <= 0 {
		interval = time.Hour
	}
	if interval < time.Minute {
		interval = time.Minute
	}
	m.mu.Unlock()

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.PruneExpired(time.Now())
			}
		}
	}()
}

func (m *BrowserRecordingManager) Close() {
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
	harArtifactDir := m.harArtifactDir
	m.activeID = session.ID
	m.sessions[session.ID] = session
	m.mu.Unlock()

	backend := m.factory(recbrowser.Options{
		StartURL:            session.StartURL,
		CaptureHAR:          captureHAR,
		HARArtifactDir:      harArtifactDir,
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

// List returns session summaries, newest first. projectID filters when supplied.
func (m *BrowserRecordingManager) List(projectID string) []*BrowserRecordingSession {
	projectID = strings.TrimSpace(projectID)
	if projectID != "" {
		projectID = normalizeProjectID(projectID)
	}
	m.mu.RLock()
	sessions := make([]*BrowserRecordingSession, 0, len(m.sessions))
	for _, session := range m.sessions {
		summary := session.summary()
		if projectID != "" && normalizeProjectID(summary.ProjectID) != projectID {
			continue
		}
		sessions = append(sessions, summary)
	}
	m.mu.RUnlock()
	sort.SliceStable(sessions, func(i, j int) bool {
		return sessions[i].StartedAt.After(sessions[j].StartedAt)
	})
	return sessions
}

// Delete removes a terminal recording session from the in-memory registry.
// Live sessions must be stopped or force-stopped first so a browser process is
// never orphaned by a delete request.
func (m *BrowserRecordingManager) Delete(id string) (*BrowserRecordingSession, bool, error) {
	m.mu.RLock()
	session, ok := m.sessions[id]
	active := m.activeID == id
	m.mu.RUnlock()
	if !ok {
		return nil, false, nil
	}
	if active || session.isLive() {
		return session.fullSnapshot(), true, fmt.Errorf("recording is live; stop it before deleting")
	}
	snapshot := session.fullSnapshot()
	m.mu.Lock()
	if current := m.sessions[id]; current == session && m.activeID != id {
		delete(m.sessions, id)
	}
	m.mu.Unlock()
	m.broadcast("browser_recording_deleted", session, nil)
	return snapshot, true, nil
}

// PruneExpired removes terminal sessions older than the configured retention
// window. Live sessions and sessions without a terminal timestamp are retained.
func (m *BrowserRecordingManager) PruneExpired(now time.Time) int {
	retention := m.retention
	if retention <= 0 {
		return 0
	}
	cutoff := now.Add(-retention)
	removed := 0
	m.mu.Lock()
	for id, session := range m.sessions {
		if m.activeID == id {
			continue
		}
		session.mu.RLock()
		status := session.Status
		stoppedAt := session.StoppedAt
		session.mu.RUnlock()
		if (status == BrowserRecordingStopped || status == BrowserRecordingFailed) && stoppedAt != nil && stoppedAt.Before(cutoff) {
			delete(m.sessions, id)
			removed++
		}
	}
	m.mu.Unlock()
	return removed
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

// Stop requests a graceful recording stop. Browser/HAR cleanup can hang inside
// Playwright, so cleanup runs asynchronously and a watchdog force-finishes the
// API session if the backend does not return.
func (m *BrowserRecordingManager) Stop(id string) (*BrowserRecordingSession, bool) {
	m.mu.RLock()
	session, ok := m.sessions[id]
	m.mu.RUnlock()
	if !ok {
		return nil, false
	}

	startCleanup := false
	session.mu.Lock()
	switch session.Status {
	case BrowserRecordingActive, BrowserRecordingPaused:
		session.Status = BrowserRecordingStopping
		session.StopReason = browserRecordingStopRequested
		startCleanup = true
	case BrowserRecordingStopping:
		// Idempotent: cleanup is already in-flight.
	default:
		session.mu.Unlock()
		return session.fullSnapshot(), true
	}
	session.mu.Unlock()

	if startCleanup {
		m.broadcast("browser_recording_stopping", session, nil)
		go m.finishStop(session)
		go m.watchStopTimeout(session)
	}
	return session.fullSnapshot(), true
}

// ForceStop is the kill-switch path for a recording stuck in the graceful
// stopping phase. It preserves captured browser steps from the latest snapshot,
// marks HAR/storage cleanup as bypassed via warnings, frees the active slot, and
// lets any already-running backend cleanup goroutine finish in the background.
func (m *BrowserRecordingManager) ForceStop(id string) (*BrowserRecordingSession, bool, error) {
	m.mu.RLock()
	session, ok := m.sessions[id]
	m.mu.RUnlock()
	if !ok {
		return nil, false, nil
	}
	session.mu.RLock()
	status := session.Status
	session.mu.RUnlock()
	switch status {
	case BrowserRecordingStopping:
		return m.forceStopSession(session, "graceful stop did not finish; force-finished recording and preserved captured browser steps"), true, nil
	case BrowserRecordingStopped, BrowserRecordingFailed:
		return session.fullSnapshot(), true, nil
	default:
		return session.fullSnapshot(), true, fmt.Errorf("recording is not stuck in stopping state (status %s)", status)
	}
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

// OpenControlPanel opens the companion control panel inside the same browser
// context as the recorded page so it appears in that browser's tab group.
func (m *BrowserRecordingManager) OpenControlPanel(id string, panelURL string, localStorage map[string]string) (*BrowserRecordingSession, bool, error) {
	session, backend, err := m.liveBackend(id)
	if session == nil {
		return nil, false, nil
	}
	if err != nil {
		return session.fullSnapshot(), true, err
	}
	if err := backend.OpenControlPanel(panelURL, localStorage); err != nil {
		return session.fullSnapshot(), true, err
	}
	return session.fullSnapshot(), true, nil
}

// AddMarker inserts a transaction boundary or note into the live recording.
func (m *BrowserRecordingManager) AddMarker(id string, kind, name, text string, atStepSeq int) (*BrowserRecordingSession, *recbrowser.Marker, bool, error) {
	session, backend, err := m.liveBackend(id)
	if session == nil {
		return nil, nil, false, nil
	}
	if err != nil {
		return session.fullSnapshot(), nil, true, err
	}
	marker, err := backend.AddMarker(kind, name, text, atStepSeq)
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
		return m.deleteLastStepFromArtifact(id, session)
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
		return m.setArtifactStepExcluded(id, session, seq, excluded)
	}
	if _, err := backend.SetStepExcluded(seq, excluded); err != nil {
		return session.fullSnapshot(), true, err
	}
	return session.fullSnapshot(), true, nil
}

// PromoteStepSelector applies a replay-verified selector promotion to the
// stopped recording artifact. Live sessions still use the recorder backend path
// for mutable operations; selector promotion is intentionally scoped to verify
// results from a frozen artifact.
func (m *BrowserRecordingManager) PromoteStepSelector(id string, seq int, selector string) (*BrowserRecordingSession, bool, error) {
	m.mu.RLock()
	session, ok := m.sessions[id]
	m.mu.RUnlock()
	if !ok {
		return nil, false, nil
	}
	step, err := mutateStoppedArtifactStep(session, func(artifact *recbrowser.Session) (*recbrowser.Step, error) {
		step, ok := artifact.PromoteStepSelector(seq, selector)
		if !ok {
			return nil, fmt.Errorf("step %d not found", seq)
		}
		return step, nil
	})
	if err != nil {
		return session.fullSnapshot(), true, err
	}
	m.broadcast("browser_recording_step_update", session, map[string]any{"step": step})
	return session.fullSnapshot(), true, nil
}

// PatchStepMark applies partial mark_secret/mark_param updates without
// clobbering whichever flag the caller omitted.
func (m *BrowserRecordingManager) PatchStepMark(id string, seq int, secret, param *bool) (*BrowserRecordingSession, bool, error) {
	if secret == nil && param == nil {
		return nil, false, fmt.Errorf("no step mark update provided")
	}
	m.mu.RLock()
	session, ok := m.sessions[id]
	m.mu.RUnlock()
	if !ok {
		return nil, false, nil
	}
	currentSecret, currentParam := browserRecordingStepMarkState(session.artifact(), seq)
	nextSecret := currentSecret
	nextParam := currentParam
	if secret != nil {
		nextSecret = *secret
	}
	if param != nil {
		nextParam = *param
	}
	return m.SetStepMark(id, seq, nextSecret, nextParam)
}

// SetStepMark flags a fill step as a runtime secret or data parameter.
func (m *BrowserRecordingManager) SetStepMark(id string, seq int, secret, param bool) (*BrowserRecordingSession, bool, error) {
	session, backend, err := m.liveBackend(id)
	if session == nil {
		return nil, false, nil
	}
	if err != nil {
		return m.setArtifactStepMark(id, session, seq, secret, param)
	}
	if _, err := backend.SetStepMark(seq, secret, param); err != nil {
		return session.fullSnapshot(), true, err
	}
	return session.fullSnapshot(), true, nil
}

func browserRecordingStepMarkState(artifact *recbrowser.Session, seq int) (bool, bool) {
	if artifact == nil {
		return false, false
	}
	for _, step := range artifact.Steps {
		if step.Seq == seq {
			return step.MarkSecret, step.MarkParam
		}
	}
	return false, false
}

func (m *BrowserRecordingManager) deleteLastStepFromArtifact(id string, session *BrowserRecordingSession) (*BrowserRecordingSession, bool, error) {
	step, err := mutateStoppedArtifactStep(session, func(artifact *recbrowser.Session) (*recbrowser.Step, error) {
		for i := len(artifact.Steps) - 1; i >= 0; i-- {
			if !artifact.Steps[i].Deleted {
				artifact.Steps[i].Deleted = true
				step := artifact.Steps[i]
				return &step, nil
			}
		}
		return nil, fmt.Errorf("no step to delete")
	})
	if err != nil {
		return session.fullSnapshot(), true, err
	}
	m.broadcast("browser_recording_step_update", session, map[string]any{"step": step})
	return session.fullSnapshot(), true, nil
}

func (m *BrowserRecordingManager) setArtifactStepExcluded(id string, session *BrowserRecordingSession, seq int, excluded bool) (*BrowserRecordingSession, bool, error) {
	step, err := mutateStoppedArtifactStep(session, func(artifact *recbrowser.Session) (*recbrowser.Step, error) {
		for i := range artifact.Steps {
			if artifact.Steps[i].Seq == seq {
				artifact.Steps[i].Excluded = excluded
				step := artifact.Steps[i]
				return &step, nil
			}
		}
		return nil, fmt.Errorf("step %d not found", seq)
	})
	if err != nil {
		return session.fullSnapshot(), true, err
	}
	m.broadcast("browser_recording_step_update", session, map[string]any{"step": step})
	return session.fullSnapshot(), true, nil
}

func (m *BrowserRecordingManager) setArtifactStepMark(id string, session *BrowserRecordingSession, seq int, secret, param bool) (*BrowserRecordingSession, bool, error) {
	step, err := mutateStoppedArtifactStep(session, func(artifact *recbrowser.Session) (*recbrowser.Step, error) {
		for i := range artifact.Steps {
			if artifact.Steps[i].Seq == seq {
				artifact.Steps[i].MarkSecret = secret
				artifact.Steps[i].MarkParam = param
				step := artifact.Steps[i]
				return &step, nil
			}
		}
		return nil, fmt.Errorf("step %d not found", seq)
	})
	if err != nil {
		return session.fullSnapshot(), true, err
	}
	m.broadcast("browser_recording_step_update", session, map[string]any{"step": step})
	return session.fullSnapshot(), true, nil
}

func mutateStoppedArtifactStep(session *BrowserRecordingSession, mutate func(*recbrowser.Session) (*recbrowser.Step, error)) (*recbrowser.Step, error) {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.Status != BrowserRecordingStopped && session.Status != BrowserRecordingFailed {
		return nil, fmt.Errorf("recording is not live (status %s)", session.Status)
	}
	if session.final == nil {
		return nil, fmt.Errorf("recording has no stopped artifact")
	}
	return mutate(session.final)
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
	m.broadcast("browser_recording_stopping", session, nil)
	go m.watchStopTimeout(session)
	m.finishStop(session)
}

func (m *BrowserRecordingManager) watchStopTimeout(session *BrowserRecordingSession) {
	timeout := m.stopTimeout
	if timeout <= 0 {
		return
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	<-timer.C
	session.mu.RLock()
	stillStopping := session.Status == BrowserRecordingStopping
	session.mu.RUnlock()
	if !stillStopping {
		return
	}
	m.forceStopSession(session, fmt.Sprintf("graceful stop did not finish within %s; force-finished recording and preserved captured browser steps", timeout.Round(time.Second)))
}

// finishStop drives the backend to completion and broadcasts the terminal
// state. Exactly one goroutine reaches it per session (the recording→stopping
// transition is guarded by the session mutex).
func (m *BrowserRecordingManager) finishStop(session *BrowserRecordingSession) {
	final, err := session.stopBackend()
	now := time.Now()
	session.mu.Lock()
	if session.Status != BrowserRecordingStopping {
		if final != nil {
			session.final = recoverLateBrowserRecordingArtifact(session.final, final)
		}
		session.mu.Unlock()
		return
	}
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

func (m *BrowserRecordingManager) forceStopSession(session *BrowserRecordingSession, warning string) *BrowserRecordingSession {
	now := time.Now()
	artifact := session.artifact()
	if artifact != nil {
		finalizeForcedBrowserRecordingArtifact(artifact, now, warning)
	}

	session.mu.Lock()
	if session.Status != BrowserRecordingStopping {
		session.mu.Unlock()
		return session.fullSnapshot()
	}
	session.Status = BrowserRecordingStopped
	session.StopReason = browserRecordingStopForced
	session.StoppedAt = &now
	session.Error = ""
	session.OpenTransactions = nil
	if artifact != nil {
		session.final = artifact
	}
	session.mu.Unlock()
	m.clearActive(session.ID)
	session.forceStopBackend()
	m.broadcast("browser_recording_force_stopped", session, nil)
	return session.fullSnapshot()
}

func recoverLateBrowserRecordingArtifact(existing, late *recbrowser.Session) *recbrowser.Session {
	if late == nil {
		return existing
	}
	if existing == nil {
		return late
	}
	if late.StoppedAt.IsZero() {
		late.StoppedAt = existing.StoppedAt
	}
	for _, warning := range existing.Warnings {
		if warning != "" && !browserRecordingStringSliceContains(late.Warnings, warning) {
			late.Warnings = append(late.Warnings, warning)
		}
	}
	return late
}

func browserRecordingStringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func finalizeForcedBrowserRecordingArtifact(session *recbrowser.Session, stoppedAt time.Time, warning string) {
	if session == nil {
		return
	}
	session.StoppedAt = stoppedAt
	if n := len(session.PauseIntervals); n > 0 && session.PauseIntervals[n-1].To.IsZero() {
		session.PauseIntervals[n-1].To = stoppedAt
	}
	for i := len(session.OpenTransactions) - 1; i >= 0; i-- {
		name := session.OpenTransactions[i]
		session.Markers = append(session.Markers, recbrowser.Marker{
			Kind:      recbrowser.MarkerTransactionEnd,
			Name:      name,
			Timestamp: stoppedAt,
			Auto:      true,
		})
	}
	session.OpenTransactions = nil
	if warning != "" {
		session.Warnings = append(session.Warnings, warning)
	}
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
	case recbrowser.EventStopRequested:
		m.Stop(session.ID)
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

func (s *BrowserRecordingSession) forceStopBackend() {
	s.mu.RLock()
	backend := s.backend
	s.mu.RUnlock()
	if backend != nil {
		go backend.ForceStop()
	}
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
