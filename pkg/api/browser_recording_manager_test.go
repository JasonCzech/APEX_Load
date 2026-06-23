package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	recbrowser "github.com/jasonczech/apexload/pkg/recorder/browser"
	"github.com/jasonczech/apexload/pkg/selector"
)

// fakeBrowserRecorderBackend stands in for the Playwright-driven recorder so
// manager and handler tests never launch Chromium.
type fakeBrowserRecorderBackend struct {
	mu           sync.Mutex
	opts         recbrowser.Options
	startErr     error
	stopErr      error
	navigateErr  error
	focusErr     error
	panelErr     error
	started      bool
	paused       bool
	assertMode   bool
	section      string
	stopCalls    int
	focusCalls   int
	navigatedTo  []string
	panelURLs    []string
	panelStorage map[string]string
	stopStarted  chan struct{}
	stopRelease  chan struct{}
	doneOnce     sync.Once
	done         chan struct{}
	session      *recbrowser.Session
}

func newFakeBrowserRecorder(session *recbrowser.Session) *fakeBrowserRecorderBackend {
	if session == nil {
		session = &recbrowser.Session{ID: "rec_fake", StartedAt: time.Now()}
	}
	return &fakeBrowserRecorderBackend{done: make(chan struct{}), session: session}
}

// install wires the fake into a manager's factory and captures the options
// (including OnEvent) the manager passes in.
func (f *fakeBrowserRecorderBackend) install(m *BrowserRecordingManager) {
	m.factory = func(opts recbrowser.Options) browserRecorderBackend {
		f.mu.Lock()
		f.opts = opts
		if f.session.StartURL == "" {
			f.session.StartURL = opts.StartURL
		}
		f.mu.Unlock()
		return f
	}
}

func (f *fakeBrowserRecorderBackend) Start() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.startErr != nil {
		return f.startErr
	}
	f.started = true
	return nil
}

func (f *fakeBrowserRecorderBackend) Stop() (*recbrowser.Session, error) {
	f.mu.Lock()
	f.stopCalls++
	err := f.stopErr
	session := f.session
	started := f.stopStarted
	release := f.stopRelease
	f.mu.Unlock()
	if started != nil {
		select {
		case <-started:
		default:
			close(started)
		}
	}
	if release != nil {
		<-release
	}
	if err != nil {
		return nil, err
	}
	f.closeBrowser()
	return session, nil
}

func (f *fakeBrowserRecorderBackend) Done() <-chan struct{} { return f.done }

func (f *fakeBrowserRecorderBackend) Snapshot() *recbrowser.Session {
	f.mu.Lock()
	defer f.mu.Unlock()
	clone := *f.session
	return &clone
}

func (f *fakeBrowserRecorderBackend) closeBrowser() {
	f.doneOnce.Do(func() { close(f.done) })
}

func (f *fakeBrowserRecorderBackend) emit(event recbrowser.Event) {
	f.mu.Lock()
	onEvent := f.opts.OnEvent
	f.mu.Unlock()
	if onEvent != nil {
		onEvent(event)
	}
}

// Pause/Resume mirror the real recorder contract: flip state and emit a
// pause_state event synchronously (the manager updates status from the event).
func (f *fakeBrowserRecorderBackend) Pause()  { f.setPaused(true) }
func (f *fakeBrowserRecorderBackend) Resume() { f.setPaused(false) }

func (f *fakeBrowserRecorderBackend) setPaused(paused bool) {
	f.mu.Lock()
	if f.paused == paused {
		f.mu.Unlock()
		return
	}
	f.paused = paused
	f.mu.Unlock()
	f.emit(recbrowser.Event{Type: recbrowser.EventPauseState, Paused: paused})
}

func (f *fakeBrowserRecorderBackend) Navigate(url string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.navigateErr != nil {
		return f.navigateErr
	}
	f.navigatedTo = append(f.navigatedTo, url)
	return nil
}

func (f *fakeBrowserRecorderBackend) Focus() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.focusCalls++
	return f.focusErr
}

func (f *fakeBrowserRecorderBackend) OpenControlPanel(url string, localStorage map[string]string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.panelErr != nil {
		return f.panelErr
	}
	f.panelURLs = append(f.panelURLs, url)
	f.panelStorage = make(map[string]string, len(localStorage))
	for k, v := range localStorage {
		f.panelStorage[k] = v
	}
	return nil
}

func (f *fakeBrowserRecorderBackend) AddMarker(kind, name, text string) (recbrowser.Marker, error) {
	f.mu.Lock()
	marker, err := f.session.AddMarker(recbrowser.Marker{Kind: kind, Name: name, Text: text, Timestamp: time.Now()})
	f.mu.Unlock()
	if err != nil {
		return recbrowser.Marker{}, err
	}
	f.emit(recbrowser.Event{Type: recbrowser.EventMarker, Marker: &marker})
	return marker, nil
}

func (f *fakeBrowserRecorderBackend) SetSection(section string) string {
	f.mu.Lock()
	f.section = section
	f.mu.Unlock()
	f.emit(recbrowser.Event{Type: recbrowser.EventSection, Section: section})
	return section
}

func (f *fakeBrowserRecorderBackend) SetAssertMode(on bool) {
	f.mu.Lock()
	f.assertMode = on
	f.mu.Unlock()
	f.emit(recbrowser.Event{Type: recbrowser.EventPickState, Picking: on})
}

func (f *fakeBrowserRecorderBackend) DeleteLastStep() (*recbrowser.Step, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.session.Steps) - 1; i >= 0; i-- {
		if !f.session.Steps[i].Deleted {
			f.session.Steps[i].Deleted = true
			step := f.session.Steps[i]
			return &step, nil
		}
	}
	return nil, fmt.Errorf("no step to delete")
}

func (f *fakeBrowserRecorderBackend) SetStepExcluded(seq int, excluded bool) (*recbrowser.Step, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.session.Steps {
		if f.session.Steps[i].Seq == seq {
			f.session.Steps[i].Excluded = excluded
			step := f.session.Steps[i]
			return &step, nil
		}
	}
	return nil, fmt.Errorf("step %d not found", seq)
}

func (f *fakeBrowserRecorderBackend) SetStepMark(seq int, secret, param bool) (*recbrowser.Step, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.session.Steps {
		if f.session.Steps[i].Seq == seq {
			f.session.Steps[i].MarkSecret = secret
			f.session.Steps[i].MarkParam = param
			step := f.session.Steps[i]
			return &step, nil
		}
	}
	return nil, fmt.Errorf("step %d not found", seq)
}

func drainHubBroadcasts(t *testing.T, hub *Hub) []map[string]any {
	t.Helper()
	var out []map[string]any
	for {
		select {
		case msg := <-hub.broadcast:
			var decoded map[string]any
			if err := json.Unmarshal(msg, &decoded); err != nil {
				t.Fatalf("broadcast not JSON: %v", err)
			}
			out = append(out, decoded)
		default:
			return out
		}
	}
}

func broadcastTypes(messages []map[string]any) []string {
	types := make([]string, 0, len(messages))
	for _, m := range messages {
		if t, _ := m["type"].(string); t != "" {
			types = append(types, t)
		}
	}
	return types
}

func waitForRecordingStatus(t *testing.T, m *BrowserRecordingManager, id string, want string) *BrowserRecordingSession {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		session, ok := m.Get(id)
		if ok && session.Status == want {
			return session
		}
		time.Sleep(10 * time.Millisecond)
	}
	session, _ := m.Get(id)
	t.Fatalf("session never reached status %q: %+v", want, session)
	return nil
}

func recordedBrowserArtifact() *recbrowser.Session {
	passwordInput := &selector.ElementMetadata{Tag: "input", Name: "user_password", Type: "password", CSSPath: "form > input"}
	button := &selector.ElementMetadata{Tag: "button", DataTestID: "go", Attributes: map[string]string{"data-testid": "go"}, CSSPath: "form > button"}
	base := time.Now().Add(-time.Minute)
	return &recbrowser.Session{
		ID:        "rec_fake",
		StartURL:  "https://shop.example/login",
		StartedAt: base,
		Steps: []recbrowser.Step{
			{Seq: 1, Kind: recbrowser.StepFill, Masked: true, Timestamp: base.Add(time.Second), Element: passwordInput, Candidates: selector.Candidates(*passwordInput)},
			{Seq: 2, Kind: recbrowser.StepClick, Timestamp: base.Add(2 * time.Second), Element: button, Candidates: selector.Candidates(*button)},
		},
		NavEvents:     []recbrowser.NavEvent{{Timestamp: base, URL: "https://shop.example/login"}},
		NetworkEvents: []time.Time{base.Add(time.Second)},
		WebSockets:    []recbrowser.WebSocketSummary{{URL: "wss://shop.example/live", OpenedAt: base}},
		HAR:           []byte(`{"log":{"version":"1.2","entries":[]}}`),
	}
}

func TestBrowserRecordingManagerStartAndConflict(t *testing.T) {
	hub := NewHub()
	m := NewBrowserRecordingManager(hub)
	fake := newFakeBrowserRecorder(nil)
	fake.install(m)

	session, err := m.Start(BrowserRecordingStartRequest{Name: "Checkout", StartURL: "https://shop.example/", ProjectID: "proj-1"})
	if err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	if session.Status != BrowserRecordingActive || session.ProjectID != "proj-1" {
		t.Errorf("session = %+v", session)
	}
	if !fake.started {
		t.Error("backend was not started")
	}

	if _, err := m.Start(BrowserRecordingStartRequest{Name: "Second", StartURL: "https://shop.example/"}); !errors.Is(err, errBrowserRecordingActive) {
		t.Errorf("second Start() error = %v, want errBrowserRecordingActive", err)
	}

	messages := drainHubBroadcasts(t, hub)
	if len(messages) == 0 || messages[0]["type"] != "browser_recording_started" {
		t.Fatalf("broadcasts = %v", broadcastTypes(messages))
	}
	if messages[0]["project_id"] != "proj-1" {
		t.Errorf("started broadcast project_id = %v, want proj-1 (hub clients filter on it)", messages[0]["project_id"])
	}
}

func TestBrowserRecordingManagerStopRequested(t *testing.T) {
	hub := NewHub()
	m := NewBrowserRecordingManager(hub)
	fake := newFakeBrowserRecorder(recordedBrowserArtifact())
	fake.install(m)

	started, err := m.Start(BrowserRecordingStartRequest{Name: "Checkout", StartURL: "https://shop.example/login", ProjectID: "proj-1"})
	if err != nil {
		t.Fatal(err)
	}

	stopped, ok := m.Stop(started.ID)
	if !ok {
		t.Fatal("Stop() did not find session")
	}
	if stopped.Status != BrowserRecordingStopping || stopped.StopReason != browserRecordingStopRequested {
		t.Errorf("initial stop response = status %q reason %q, want stopping/requested", stopped.Status, stopped.StopReason)
	}
	stopped = waitForRecordingStatus(t, m, started.ID, BrowserRecordingStopped)
	if stopped.Status != BrowserRecordingStopped || stopped.StopReason != browserRecordingStopRequested {
		t.Errorf("stopped session = status %q reason %q", stopped.Status, stopped.StopReason)
	}
	if stopped.StepCount != 2 || stopped.WebSocketCount != 1 || stopped.NetworkRequestCount != 1 {
		t.Errorf("counts = %+v", stopped)
	}
	if !stopped.HARAvailable {
		t.Error("HARAvailable = false, want true")
	}
	if fake.stopCalls != 1 {
		t.Errorf("backend stop calls = %d, want 1", fake.stopCalls)
	}

	// The slot frees up for the next recording.
	if _, err := m.Start(BrowserRecordingStartRequest{Name: "Next", StartURL: "https://shop.example/"}); err != nil {
		t.Errorf("Start() after stop error: %v", err)
	}

	types := broadcastTypes(drainHubBroadcasts(t, hub))
	want := map[string]bool{"browser_recording_started": false, "browser_recording_stopped": false}
	for _, typ := range types {
		if _, ok := want[typ]; ok {
			want[typ] = true
		}
	}
	for typ, seen := range want {
		if !seen {
			t.Errorf("missing broadcast %q in %v", typ, types)
		}
	}
}

func TestBrowserRecordingManagerPromoteStepSelectorOnStoppedArtifact(t *testing.T) {
	m := NewBrowserRecordingManager(NewHub())
	fake := newFakeBrowserRecorder(recordedBrowserArtifact())
	fake.install(m)

	started, err := m.Start(BrowserRecordingStartRequest{StartURL: "https://shop.example/login"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Stop(started.ID); !ok {
		t.Fatal("Stop() did not find session")
	}
	waitForRecordingStatus(t, m, started.ID, BrowserRecordingStopped)

	patched, ok, err := m.PromoteStepSelector(started.ID, 2, `[data-testid="healed-go"]`)
	if err != nil || !ok {
		t.Fatalf("PromoteStepSelector() ok=%v err=%v", ok, err)
	}
	if len(patched.Steps) < 2 {
		t.Fatalf("patched steps = %+v", patched.Steps)
	}
	if got := patched.Steps[1].Candidates[0].Value; got != `[data-testid="healed-go"]` {
		t.Fatalf("promoted selector = %q", got)
	}
	if got := fake.session.Steps[1].Candidates[0].Value; got != `[data-testid="healed-go"]` {
		t.Fatalf("final artifact selector = %q", got)
	}
}

func TestBrowserRecordingManagerStopIsIdempotent(t *testing.T) {
	m := NewBrowserRecordingManager(NewHub())
	fake := newFakeBrowserRecorder(recordedBrowserArtifact())
	fake.install(m)

	started, err := m.Start(BrowserRecordingStartRequest{StartURL: "https://shop.example/"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Stop(started.ID); !ok {
		t.Fatal("first Stop() missing session")
	}
	again, ok := m.Stop(started.ID)
	if !ok || again.Status != BrowserRecordingStopping {
		t.Errorf("second Stop() = %+v ok=%v", again, ok)
	}
	waitForRecordingStatus(t, m, started.ID, BrowserRecordingStopped)
	if fake.stopCalls != 1 {
		t.Errorf("backend stop calls = %d, want 1", fake.stopCalls)
	}
}

func TestBrowserRecordingManagerStopsFromInPageControlEvent(t *testing.T) {
	hub := NewHub()
	m := NewBrowserRecordingManager(hub)
	m.stopTimeout = time.Hour
	fake := newFakeBrowserRecorder(recordedBrowserArtifact())
	fake.stopStarted = make(chan struct{})
	fake.stopRelease = make(chan struct{})
	fake.install(m)

	started, err := m.Start(BrowserRecordingStartRequest{StartURL: "https://shop.example/"})
	if err != nil {
		t.Fatal(err)
	}
	drainHubBroadcasts(t, hub)

	fake.emit(recbrowser.Event{Type: recbrowser.EventStopRequested})
	stopping := waitForRecordingStatus(t, m, started.ID, BrowserRecordingStopping)
	if stopping.StopReason != browserRecordingStopRequested {
		t.Fatalf("in-page stop reason = %q, want requested", stopping.StopReason)
	}
	select {
	case <-fake.stopStarted:
	case <-time.After(time.Second):
		t.Fatal("backend Stop was not started from in-page stop event")
	}
	close(fake.stopRelease)

	stopped := waitForRecordingStatus(t, m, started.ID, BrowserRecordingStopped)
	if stopped.StopReason != browserRecordingStopRequested {
		t.Fatalf("stopped reason = %q, want requested", stopped.StopReason)
	}
	if fake.stopCalls != 1 {
		t.Errorf("backend stop calls = %d, want 1", fake.stopCalls)
	}
	types := broadcastTypes(drainHubBroadcasts(t, hub))
	if !containsString(types, "browser_recording_stopping") || !containsString(types, "browser_recording_stopped") {
		t.Errorf("broadcasts = %v, want stopping and stopped", types)
	}
}

func TestBrowserRecordingManagerForceStopsStuckCleanup(t *testing.T) {
	hub := NewHub()
	m := NewBrowserRecordingManager(hub)
	m.stopTimeout = time.Hour
	fake := newFakeBrowserRecorder(recordedBrowserArtifact())
	fake.stopStarted = make(chan struct{})
	fake.stopRelease = make(chan struct{})
	fake.install(m)

	started, err := m.Start(BrowserRecordingStartRequest{StartURL: "https://shop.example/"})
	if err != nil {
		t.Fatal(err)
	}
	drainHubBroadcasts(t, hub)

	stopping, ok := m.Stop(started.ID)
	if !ok || stopping.Status != BrowserRecordingStopping {
		t.Fatalf("Stop() = %+v ok=%v, want stopping", stopping, ok)
	}
	select {
	case <-fake.stopStarted:
	case <-time.After(time.Second):
		t.Fatal("backend Stop was not started")
	}

	forced, ok, err := m.ForceStop(started.ID)
	if !ok || err != nil {
		t.Fatalf("ForceStop() ok=%v err=%v", ok, err)
	}
	if forced.Status != BrowserRecordingStopped || forced.StopReason != browserRecordingStopForced {
		t.Fatalf("forced session = status %q reason %q", forced.Status, forced.StopReason)
	}
	if forced.StepCount != 2 {
		t.Errorf("forced StepCount = %d, want captured steps preserved", forced.StepCount)
	}
	if len(forced.OpenTransactions) != 0 {
		t.Errorf("forced open transactions = %v, want closed", forced.OpenTransactions)
	}
	if !strings.Contains(strings.Join(forced.Warnings, "\n"), "force-finished") {
		t.Errorf("forced warnings = %v, want force-finished warning", forced.Warnings)
	}
	if active := m.Active(); active != nil {
		t.Errorf("Active() after force stop = %+v, want nil", active)
	}
	if _, err := m.Start(BrowserRecordingStartRequest{StartURL: "https://shop.example/next"}); err != nil {
		t.Errorf("Start() after force stop error: %v", err)
	}

	close(fake.stopRelease)
	waitForRecordingStatus(t, m, started.ID, BrowserRecordingStopped)
	after, _ := m.Get(started.ID)
	if after.StopReason != browserRecordingStopForced {
		t.Errorf("late graceful cleanup changed stop reason to %q", after.StopReason)
	}
	if types := broadcastTypes(drainHubBroadcasts(t, hub)); !containsString(types, "browser_recording_force_stopped") {
		t.Errorf("broadcasts = %v, want force stop broadcast", types)
	}
}

func TestBrowserRecordingManagerStopWatchdogForceStops(t *testing.T) {
	m := NewBrowserRecordingManager(NewHub())
	m.stopTimeout = 10 * time.Millisecond
	fake := newFakeBrowserRecorder(recordedBrowserArtifact())
	fake.stopStarted = make(chan struct{})
	fake.stopRelease = make(chan struct{})
	fake.install(m)

	started, err := m.Start(BrowserRecordingStartRequest{StartURL: "https://shop.example/"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Stop(started.ID); !ok {
		t.Fatal("Stop() missing session")
	}
	select {
	case <-fake.stopStarted:
	case <-time.After(time.Second):
		t.Fatal("backend Stop was not started")
	}
	forced := waitForRecordingStatus(t, m, started.ID, BrowserRecordingStopped)
	if forced.StopReason != browserRecordingStopForced {
		t.Fatalf("watchdog stop reason = %q, want forced", forced.StopReason)
	}
	close(fake.stopRelease)
}

func TestBrowserRecordingManagerBrowserClosedByUser(t *testing.T) {
	hub := NewHub()
	m := NewBrowserRecordingManager(hub)
	fake := newFakeBrowserRecorder(recordedBrowserArtifact())
	fake.install(m)

	started, err := m.Start(BrowserRecordingStartRequest{Name: "Checkout", StartURL: "https://shop.example/login"})
	if err != nil {
		t.Fatal(err)
	}

	fake.closeBrowser()

	session := waitForRecordingStatus(t, m, started.ID, BrowserRecordingStopped)
	if session.StopReason != browserRecordingStopBrowserClosed {
		t.Errorf("stop reason = %q, want %q", session.StopReason, browserRecordingStopBrowserClosed)
	}
	if fake.stopCalls != 1 {
		t.Errorf("backend stop calls = %d, want 1 (HAR still collected after window close)", fake.stopCalls)
	}
	if !session.HARAvailable {
		t.Error("HARAvailable = false, want true after browser-closed stop")
	}
}

func TestBrowserRecordingManagerStartFailureFreesSlot(t *testing.T) {
	hub := NewHub()
	m := NewBrowserRecordingManager(hub)
	fake := newFakeBrowserRecorder(nil)
	fake.startErr = errors.New("playwright driver missing")
	fake.install(m)

	_, err := m.Start(BrowserRecordingStartRequest{StartURL: "https://shop.example/"})
	if err == nil || err.Error() != "playwright driver missing" {
		t.Fatalf("Start() error = %v", err)
	}

	types := broadcastTypes(drainHubBroadcasts(t, hub))
	if len(types) == 0 || types[len(types)-1] != "browser_recording_failed" {
		t.Errorf("broadcasts = %v, want trailing browser_recording_failed", types)
	}

	healthy := newFakeBrowserRecorder(nil)
	healthy.install(m)
	if _, err := m.Start(BrowserRecordingStartRequest{StartURL: "https://shop.example/"}); err != nil {
		t.Errorf("Start() after failure error: %v", err)
	}
}

func TestBrowserRecordingManagerLiveEvents(t *testing.T) {
	hub := NewHub()
	m := NewBrowserRecordingManager(hub)
	fake := newFakeBrowserRecorder(nil)
	fake.install(m)

	started, err := m.Start(BrowserRecordingStartRequest{StartURL: "https://shop.example/", ProjectID: "proj-7"})
	if err != nil {
		t.Fatal(err)
	}
	drainHubBroadcasts(t, hub) // discard the started event

	fake.emit(recbrowser.Event{Type: recbrowser.EventNavigation, Nav: &recbrowser.NavEvent{Timestamp: time.Now(), URL: "https://shop.example/cart"}})
	fake.emit(recbrowser.Event{Type: recbrowser.EventStep, Step: &recbrowser.Step{Seq: 1, Kind: recbrowser.StepClick, URL: "https://shop.example/cart"}})
	fake.emit(recbrowser.Event{Type: recbrowser.EventWarning, Warning: "dropped capture event: boom"})

	messages := drainHubBroadcasts(t, hub)
	types := broadcastTypes(messages)
	wantTypes := []string{"browser_recording_navigation", "browser_recording_step", "browser_recording_warning"}
	if len(types) != len(wantTypes) {
		t.Fatalf("broadcast types = %v, want %v", types, wantTypes)
	}
	for i, want := range wantTypes {
		if types[i] != want {
			t.Errorf("broadcast[%d] = %q, want %q", i, types[i], want)
		}
		if messages[i]["project_id"] != "proj-7" {
			t.Errorf("broadcast[%d] project_id = %v, want proj-7", i, messages[i]["project_id"])
		}
	}
	if _, ok := messages[1]["step"]; !ok {
		t.Error("step broadcast missing step payload")
	}

	summary := m.Active()
	if summary == nil || summary.ID != started.ID {
		t.Fatalf("Active() = %+v", summary)
	}
	if summary.StepCount != 1 || summary.NavigationCount != 1 || summary.WarningCount != 1 {
		t.Errorf("live counts = %+v", summary)
	}
	if summary.CurrentURL != "https://shop.example/cart" {
		t.Errorf("CurrentURL = %q", summary.CurrentURL)
	}
}

func TestBrowserRecordingManagerPauseResume(t *testing.T) {
	hub := NewHub()
	m := NewBrowserRecordingManager(hub)
	fake := newFakeBrowserRecorder(nil)
	fake.install(m)

	started, err := m.Start(BrowserRecordingStartRequest{StartURL: "https://shop.example/", ProjectID: "proj-1"})
	if err != nil {
		t.Fatal(err)
	}
	drainHubBroadcasts(t, hub)

	paused, ok, err := m.Pause(started.ID)
	if !ok || err != nil {
		t.Fatalf("Pause() ok=%v err=%v", ok, err)
	}
	if paused.Status != BrowserRecordingPaused {
		t.Errorf("status after pause = %q", paused.Status)
	}
	// Idempotent: pausing a paused session is a no-op without error.
	again, _, err := m.Pause(started.ID)
	if err != nil || again.Status != BrowserRecordingPaused {
		t.Errorf("second Pause() = %+v err=%v", again.Status, err)
	}

	resumed, ok, err := m.Resume(started.ID)
	if !ok || err != nil || resumed.Status != BrowserRecordingActive {
		t.Fatalf("Resume() = %q ok=%v err=%v", resumed.Status, ok, err)
	}

	types := broadcastTypes(drainHubBroadcasts(t, hub))
	want := []string{"browser_recording_paused", "browser_recording_resumed"}
	if len(types) != len(want) || types[0] != want[0] || types[1] != want[1] {
		t.Errorf("broadcasts = %v, want %v", types, want)
	}
}

func TestBrowserRecordingManagerToolbarPauseSyncs(t *testing.T) {
	hub := NewHub()
	m := NewBrowserRecordingManager(hub)
	fake := newFakeBrowserRecorder(nil)
	fake.install(m)

	started, err := m.Start(BrowserRecordingStartRequest{StartURL: "https://shop.example/"})
	if err != nil {
		t.Fatal(err)
	}
	drainHubBroadcasts(t, hub)

	// The in-page toolbar flips state recorder-side; the manager only sees
	// the pause_state event and must follow.
	fake.setPaused(true)
	session, _ := m.Get(started.ID)
	if session.Status != BrowserRecordingPaused {
		t.Errorf("status after toolbar pause = %q", session.Status)
	}
	if types := broadcastTypes(drainHubBroadcasts(t, hub)); len(types) != 1 || types[0] != "browser_recording_paused" {
		t.Errorf("broadcasts = %v", types)
	}
}

func TestBrowserRecordingManagerStopFromPaused(t *testing.T) {
	m := NewBrowserRecordingManager(NewHub())
	fake := newFakeBrowserRecorder(recordedBrowserArtifact())
	fake.install(m)

	started, err := m.Start(BrowserRecordingStartRequest{StartURL: "https://shop.example/"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Pause(started.ID); err != nil {
		t.Fatal(err)
	}
	stopped, ok := m.Stop(started.ID)
	if !ok || stopped.Status != BrowserRecordingStopping {
		t.Errorf("Stop from paused = %q ok=%v", stopped.Status, ok)
	}
	waitForRecordingStatus(t, m, started.ID, BrowserRecordingStopped)
	if fake.stopCalls != 1 {
		t.Errorf("stop calls = %d", fake.stopCalls)
	}
}

func TestBrowserRecordingManagerNavigateAndFocus(t *testing.T) {
	m := NewBrowserRecordingManager(NewHub())
	fake := newFakeBrowserRecorder(nil)
	fake.install(m)

	started, err := m.Start(BrowserRecordingStartRequest{StartURL: "https://shop.example/"})
	if err != nil {
		t.Fatal(err)
	}

	if _, ok, err := m.Navigate(started.ID, "https://shop.example/cart"); !ok || err != nil {
		t.Fatalf("Navigate() ok=%v err=%v", ok, err)
	}
	if len(fake.navigatedTo) != 1 || fake.navigatedTo[0] != "https://shop.example/cart" {
		t.Errorf("navigatedTo = %v", fake.navigatedTo)
	}
	if _, ok, err := m.Focus(started.ID); !ok || err != nil {
		t.Fatalf("Focus() ok=%v err=%v", ok, err)
	}
	if fake.focusCalls != 1 {
		t.Errorf("focus calls = %d", fake.focusCalls)
	}
	panelStorage := map[string]string{"apexload.browserRecorderBridge.http://127.0.0.1:8080": `{"origin":"http://127.0.0.1:27777"}`}
	if _, ok, err := m.OpenControlPanel(started.ID, "http://127.0.0.1:8080/#/recorder-panel?session="+started.ID, panelStorage); !ok || err != nil {
		t.Fatalf("OpenControlPanel() ok=%v err=%v", ok, err)
	}
	if len(fake.panelURLs) != 1 || !strings.Contains(fake.panelURLs[0], "#/recorder-panel") {
		t.Errorf("panelURLs = %v", fake.panelURLs)
	}
	if got := fake.panelStorage["apexload.browserRecorderBridge.http://127.0.0.1:8080"]; !strings.Contains(got, "27777") {
		t.Errorf("panelStorage = %v", fake.panelStorage)
	}

	// Controls refuse once the session ends.
	m.Stop(started.ID)
	if _, _, err := m.Navigate(started.ID, "https://shop.example/"); err == nil {
		t.Error("Navigate after stop must error")
	}
	if _, _, err := m.OpenControlPanel(started.ID, "http://127.0.0.1:8080/#/recorder-panel?session="+started.ID, nil); err == nil {
		t.Error("OpenControlPanel after stop must error")
	}
	if _, _, err := m.Pause(started.ID); err == nil {
		t.Error("Pause after stop must error")
	}
}

func TestBrowserRecordingManagerMarkers(t *testing.T) {
	hub := NewHub()
	m := NewBrowserRecordingManager(hub)
	fake := newFakeBrowserRecorder(nil)
	fake.install(m)

	started, err := m.Start(BrowserRecordingStartRequest{StartURL: "https://shop.example/", ProjectID: "proj-1"})
	if err != nil {
		t.Fatal(err)
	}
	drainHubBroadcasts(t, hub)

	session, marker, ok, err := m.AddMarker(started.ID, recbrowser.MarkerTransactionStart, "Checkout", "")
	if !ok || err != nil {
		t.Fatalf("AddMarker() ok=%v err=%v", ok, err)
	}
	if marker.Kind != recbrowser.MarkerTransactionStart || marker.Name != "Checkout" {
		t.Errorf("marker = %+v", marker)
	}
	if len(session.OpenTransactions) != 1 || session.OpenTransactions[0] != "Checkout" {
		t.Errorf("open transactions = %v", session.OpenTransactions)
	}

	if _, _, _, err := m.AddMarker(started.ID, recbrowser.MarkerTransactionEnd, "Ghost", ""); err == nil {
		t.Error("ending an unopened transaction must error")
	}

	session, _, _, err = m.AddMarker(started.ID, recbrowser.MarkerTransactionEnd, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(session.OpenTransactions) != 0 {
		t.Errorf("open transactions after end = %v", session.OpenTransactions)
	}

	if _, _, _, err := m.AddMarker(started.ID, recbrowser.MarkerNote, "", "remember the promo banner"); err != nil {
		t.Fatal(err)
	}

	messages := drainHubBroadcasts(t, hub)
	markerEvents := 0
	for _, msg := range messages {
		if msg["type"] == "browser_recording_marker" {
			markerEvents++
			if msg["project_id"] != "proj-1" {
				t.Errorf("marker broadcast project_id = %v", msg["project_id"])
			}
			if _, hasMarker := msg["marker"]; !hasMarker {
				t.Error("marker broadcast missing marker payload")
			}
		}
	}
	if markerEvents != 3 {
		t.Errorf("marker broadcasts = %d, want 3", markerEvents)
	}
}

func TestBrowserRecordingManagerNotifyConverted(t *testing.T) {
	hub := NewHub()
	m := NewBrowserRecordingManager(hub)
	fake := newFakeBrowserRecorder(nil)
	fake.install(m)

	started, err := m.Start(BrowserRecordingStartRequest{StartURL: "https://shop.example/", ProjectID: "proj-1"})
	if err != nil {
		t.Fatal(err)
	}
	drainHubBroadcasts(t, hub)

	m.NotifyConverted(started.ID, "script-1", "Checkout")
	messages := drainHubBroadcasts(t, hub)
	if len(messages) != 1 || messages[0]["type"] != "browser_recording_converted" {
		t.Fatalf("broadcasts = %v", broadcastTypes(messages))
	}
	if messages[0]["script_id"] != "script-1" || messages[0]["project_id"] != "proj-1" {
		t.Errorf("converted broadcast = %+v", messages[0])
	}
}
