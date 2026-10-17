package api

import (
	"encoding/json"
	"fmt"
	"github.com/jasonczech/apexload/pkg/recordbridge"
	recbrowser "github.com/jasonczech/apexload/pkg/recorder/browser"
	"github.com/jasonczech/apexload/pkg/selector"
	"sync"
	"testing"
	"time"
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
	forceCalls   int
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

// factory constructs the injectable backend factory and captures the options
// (including OnEvent) the manager passes in.
//
// Only the FIRST Start receives this fake. Every later one gets an independent
// backend with its own artifact, because that is what the real factory does:
// each recording session constructs its own recorder and owns its own
// *recbrowser.Session.
//
// Handing the same fake to every Start let two live sessions share one artifact
// and mutate it under two different session mutexes — a data race the real
// backend cannot produce. It is reachable whenever a test starts a second
// recording while the first is still finishing (ForceStop frees the active slot
// immediately, while the first session's graceful stop is still in flight).
func (f *fakeBrowserRecorderBackend) factory() recordbridge.RecorderFactory {
	var issuedMu sync.Mutex
	issued := 0
	return func(opts recbrowser.Options) recordbridge.RecorderBackend {
		issuedMu.Lock()
		issued++
		first := issued == 1
		ordinal := issued
		issuedMu.Unlock()

		if !first {
			return newFakeBrowserRecorder(&recbrowser.Session{
				ID:        fmt.Sprintf("rec_fake_%d", ordinal),
				StartedAt: time.Now(),
				StartURL:  opts.StartURL,
			})
		}
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

func (f *fakeBrowserRecorderBackend) ForceStop() {
	f.mu.Lock()
	f.forceCalls++
	f.mu.Unlock()
	f.closeBrowser()
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

func (f *fakeBrowserRecorderBackend) AddMarker(kind, name, text string, atStepSeq int) (recbrowser.Marker, error) {
	f.mu.Lock()
	timestamp := time.Now()
	if atStepSeq > 0 {
		found := false
		for _, step := range f.session.Steps {
			if step.Seq == atStepSeq {
				timestamp = step.Timestamp
				found = true
				break
			}
		}
		if !found {
			f.mu.Unlock()
			return recbrowser.Marker{}, fmt.Errorf("step #%d not found for marker anchor", atStepSeq)
		}
	}
	marker, err := f.session.AddMarker(recbrowser.Marker{Kind: kind, Name: name, Text: text, Timestamp: timestamp})
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
			if err := json.Unmarshal(msg.payload, &decoded); err != nil {
				t.Fatalf("broadcast not JSON: %v", err)
			}
			out = append(out, decoded)
		default:
			return out
		}
	}
}

func waitForHubBroadcastType(t *testing.T, hub *Hub, want string) map[string]any {
	t.Helper()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for {
		select {
		case msg := <-hub.broadcast:
			var decoded map[string]any
			if err := json.Unmarshal(msg.payload, &decoded); err != nil {
				t.Fatalf("broadcast not JSON: %v", err)
			}
			if decoded["type"] == want {
				return decoded
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for broadcast %q", want)
			return nil
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

func waitForCondition(t *testing.T, timeout time.Duration, ok func() bool, label string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", label)
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

// installBrowserRecorderBackend replaces the unused native recorder while keeping
// the controller event sink and artifact ownership configured for the fixture.
func installBrowserRecorderBackend(t *testing.T, server *Server, fake *fakeBrowserRecorderBackend) {
	t.Helper()
	server.browserRecording.Close()
	server.browserRecording = NewBrowserRecordingManager(server.hub, recordbridge.RecordingManagerOptions{RecorderFactory: fake.factory()})
	server.browserRecording.SetHARArtifactDir(server.runner.GetSystemSettings().ArchiveRetention.HARArtifactDir)
	t.Cleanup(server.browserRecording.Close)
}

func stopBrowserRecordingFixture(t *testing.T, server *Server, artifact *recbrowser.Session, projectID, name string) *BrowserRecordingSession {
	t.Helper()
	installBrowserRecorderBackend(t, server, newFakeBrowserRecorder(artifact))
	info, err := server.browserRecording.Start(BrowserRecordingStartRequest{Name: name, StartURL: artifact.StartURL, ProjectID: projectID})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := server.browserRecording.Stop(info.ID); !ok {
		t.Fatal("recording vanished before stop")
	}
	waitForReplayLifecycleCondition(t, "stopped recording fixture", func() bool {
		var ok bool
		info, ok = server.browserRecording.Get(info.ID)
		return ok && info.Status == BrowserRecordingStopped
	})
	return info
}
