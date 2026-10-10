package browser

import (
	"archive/zip"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mxschmitt/playwright-go"
)

type pageLimitBrowserContext struct {
	playwright.BrowserContext
	pages []playwright.Page
}

func (c *pageLimitBrowserContext) Pages() []playwright.Page { return c.pages }

type pageLimitPage struct {
	playwright.Page
	closed bool
}

func (*pageLimitPage) Opener() (playwright.Page, error) { return nil, nil }
func (*pageLimitPage) URL() string                      { return "about:blank" }
func (p *pageLimitPage) Close(...playwright.PageCloseOptions) error {
	p.closed = true
	return nil
}

// Unit tests for the recorder's capture pipeline that do not need a real
// browser: the binding handler, live event emission, and session snapshots.

func TestManagedHARTempPathUsesArtifactPrefix(t *testing.T) {
	dir := t.TempDir()
	path := managedHARTempPath(dir, `rec/..\secret`)
	if filepath.Dir(path) != dir {
		t.Fatalf("dir = %q, want %q", filepath.Dir(path), dir)
	}
	name := filepath.Base(path)
	if !strings.HasPrefix(name, "apexload_browser_rec_.._secret") || !strings.HasSuffix(name, ".har.zip") {
		t.Fatalf("managed HAR temp name = %q, want managed browser artifact prefix and temp suffix", name)
	}
}

func TestPoliceHARFlushTruncatesAndAbortsOversizedArtifact(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flush.har")
	abort := make(chan struct{})
	const limit = int64(32 << 10)
	flush := func() error {
		file, err := os.Create(path)
		if err != nil {
			return err
		}
		defer file.Close()
		chunk := make([]byte, 4<<10)
		for i := 0; i < 64; i++ {
			select {
			case <-abort:
				return nil
			default:
			}
			if _, err := file.Write(chunk); err != nil {
				return err
			}
			time.Sleep(time.Millisecond)
		}
		return nil
	}
	exceeded, err := policeHARFlush(path, limit, flush, func() { close(abort) })
	if err != nil {
		t.Fatal(err)
	}
	if !exceeded {
		t.Fatal("oversized flush was not detected")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > limit {
		t.Fatalf("policed HAR size = %d, limit %d", info.Size(), limit)
	}
}

func TestPoliceHARFlushBoundsPreOversizedArtifactThatGrowsDuringClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pre-oversized-flush.har")
	const limit = int64(16 << 10)
	if err := os.WriteFile(path, make([]byte, limit+1), 0o600); err != nil {
		t.Fatal(err)
	}
	abort := make(chan struct{})
	flush := func() error {
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
		if err != nil {
			return err
		}
		defer file.Close()
		chunk := make([]byte, 4<<10)
		for {
			select {
			case <-abort:
				return nil
			default:
				if _, err := file.Write(chunk); err != nil {
					return err
				}
			}
		}
	}
	exceeded, err := policeHARFlush(path, limit, flush, func() { close(abort) })
	if err != nil {
		t.Fatal(err)
	}
	if !exceeded {
		t.Fatal("pre-oversized HAR was not detected")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > limit {
		t.Fatalf("policed HAR size = %d, limit %d", info.Size(), limit)
	}
}

func TestPoliceHARFlushReturnsWhenFlushAndAbortNeverReturn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stuck.har")
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	abortCalled := make(chan struct{}, 1)
	release := make(chan struct{})
	defer close(release)
	started := time.Now()
	_, err := policeHARFlushUntil(path, 1024, func() error {
		<-release
		return nil
	}, func() {
		abortCalled <- struct{}{}
		<-release
	}, time.Now().Add(25*time.Millisecond))
	if err == nil || !strings.Contains(err.Error(), "deadline") {
		t.Fatalf("stuck flush error = %v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatalf("stuck flush blocked for %s", time.Since(started))
	}
	select {
	case <-abortCalled:
	case <-time.After(time.Second):
		t.Fatal("stuck flush did not invoke abort")
	}
}

func TestWaitGroupUntilBoundsStuckThumbnailWorker(t *testing.T) {
	var group sync.WaitGroup
	group.Add(1)
	if waitGroupUntil(&group, time.Now().Add(20*time.Millisecond)) {
		t.Fatal("waitGroupUntil reported completion for blocked worker")
	}
	group.Done()
	if !waitGroupUntil(&group, time.Now().Add(time.Second)) {
		t.Fatal("waitGroupUntil did not observe released worker")
	}
}

func TestShutdownOperationsDoNotStartAfterDeadline(t *testing.T) {
	called := false
	err := runOperationUntil(time.Now().Add(-time.Millisecond), func() error {
		called = true
		return nil
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "deadline") {
		t.Fatalf("expired operation error = %v", err)
	}
	if called {
		t.Fatal("operation started after the recorder shutdown deadline")
	}

	path := filepath.Join(t.TempDir(), "capture.har")
	if err := os.WriteFile(path, []byte(`{"log":{"entries":[]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := loadAndFilterHARUntil(path, maxBrowserHARBytes, HARFilters{}, time.Now().Add(-time.Millisecond)); err == nil || !strings.Contains(err.Error(), "deadline") {
		t.Fatalf("expired HAR processing error = %v", err)
	}
}

func TestPrepareHARPathCreatesPrivateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "apexload_browser_rec_123.har.tmp")
	if err := prepareHARPath(path); err != nil {
		t.Fatalf("prepareHARPath: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat prepared HAR: %v", err)
	}
	if info.IsDir() {
		t.Fatal("prepared HAR path is a directory")
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %#o, want 0600", got)
	}
}

func TestReadFileLimitedRejectsOversizedHAR(t *testing.T) {
	path := filepath.Join(t.TempDir(), "capture.har")
	if err := os.WriteFile(path, []byte("0123456789"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if _, err := readFileLimited(path, 9); err == nil || !strings.Contains(err.Error(), "maximum") {
		t.Fatalf("readFileLimited error = %v, want size rejection", err)
	}
	data, err := readFileLimited(path, 10)
	if err != nil || string(data) != "0123456789" {
		t.Fatalf("bounded read = %q, %v", data, err)
	}
}

func TestReadAttachedHARZipRejectsEntryFloodAndLongNames(t *testing.T) {
	writeArchive := func(t *testing.T, names []string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "attached.har.zip")
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		writer := zip.NewWriter(file)
		for _, name := range names {
			entry, createErr := writer.Create(name)
			if createErr != nil {
				t.Fatal(createErr)
			}
			if name == "har.har" {
				_, _ = entry.Write([]byte(`{"log":{"entries":[]}}`))
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		return path
	}

	names := make([]string, 0, maxAttachedHARZipEntries+1)
	names = append(names, "har.har")
	for index := 1; index <= maxAttachedHARZipEntries; index++ {
		names = append(names, fmt.Sprintf("empty-%05d", index))
	}
	if _, err := readAttachedHARZip(writeArchive(t, names), maxBrowserHARBytes); err == nil || !strings.Contains(err.Error(), "entries") {
		t.Fatalf("entry flood error = %v", err)
	}
	if _, err := readAttachedHARZip(writeArchive(t, []string{"har.har", strings.Repeat("x", maxAttachedHARZipNameBytes+1)}), maxBrowserHARBytes); err == nil || !strings.Contains(err.Error(), "name") {
		t.Fatalf("long-name error = %v", err)
	}
}

func TestReadHARArtifactRehydratesAttachedBodies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "capture.har.zip")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	harEntry, _ := writer.Create("har.har")
	_, _ = harEntry.Write([]byte(`{"log":{"entries":[{"response":{"content":{"mimeType":"application/octet-stream","_file":"resources/body.bin"}}}]}}`))
	bodyEntry, _ := writer.Create("resources/body.bin")
	_, _ = bodyEntry.Write([]byte{0x00, 0xff, 0x01})
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	data, err := readHARArtifact(path, 1<<20)
	if err != nil {
		t.Fatalf("readHARArtifact: %v", err)
	}
	if strings.Contains(string(data), `"_file"`) {
		t.Fatalf("hydrated HAR retained detached reference: %s", data)
	}
	if !strings.Contains(string(data), `"encoding":"base64"`) || !strings.Contains(string(data), base64.StdEncoding.EncodeToString([]byte{0x00, 0xff, 0x01})) {
		t.Fatalf("hydrated HAR did not embed binary body: %s", data)
	}
}

func TestRegisterPageRejectsPageBeyondLimit(t *testing.T) {
	existing := make([]playwright.Page, maxBrowserPages)
	for i := range existing {
		existing[i] = &pageLimitPage{}
	}
	var events []Event
	r := New(Options{StartURL: "https://app.example/", OnEvent: func(event Event) {
		events = append(events, event)
	}})
	r.context = &pageLimitBrowserContext{pages: existing}
	excess := &pageLimitPage{}

	r.registerPage(excess)

	if !excess.closed {
		t.Fatal("page 33 was not closed at the configured page limit")
	}
	if warnings := r.Snapshot().Warnings; len(warnings) != 1 || !strings.Contains(warnings[0], "page limit") {
		t.Fatalf("warnings = %v, want page-limit warning", warnings)
	}
	if hits := r.Snapshot().CapHits["pages"]; hits != 1 {
		t.Fatalf("page cap hits = %d, want 1", hits)
	}
	var sawCap, sawWarning bool
	for _, event := range events {
		sawCap = sawCap || event.Type == EventCapHit && event.CapName == "pages" && event.CapCount == 1
		sawWarning = sawWarning || event.Type == EventWarning && strings.Contains(event.Warning, "page limit")
	}
	if !sawCap || !sawWarning {
		t.Fatalf("events = %#v, want live page-cap and warning events", events)
	}
}

func TestForceStopRemovesManagedHARTemp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "apexload_browser_rec_123.har.tmp")
	if err := os.WriteFile(path, []byte("raw-cookie=secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := New(Options{StartURL: "https://app.example/"})
	r.mu.Lock()
	r.harTemp = true
	r.session.HARPath = path
	r.mu.Unlock()

	r.ForceStop()

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("managed HAR temp stat err = %v, want removed", err)
	}
	if got := r.Snapshot().HARPath; got != "" {
		t.Fatalf("snapshot HARPath = %q, want cleared after force-stop discard", got)
	}
}

func TestHandleRecordBindingEmitsStepEvents(t *testing.T) {
	var events []Event
	r := New(Options{StartURL: "https://app.example/", OnEvent: func(e Event) { events = append(events, e) }})

	payload := `{"kind":"click","ts":1700000000000,"url":"https://app.example/","element":{"tag":"button","id":"go","css_path":"#go"}}`
	r.handleRecordBinding(nil, payload)

	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	if events[0].Type != EventStep || events[0].Step == nil {
		t.Fatalf("event = %+v, want step event", events[0])
	}
	if events[0].Step.Kind != StepClick || events[0].Step.Seq != 1 {
		t.Errorf("step = %+v", events[0].Step)
	}
	if got := len(r.Snapshot().Steps); got != 1 {
		t.Errorf("session steps = %d, want 1", got)
	}
}

func TestHandleRecordBindingEmitsCollapsedFill(t *testing.T) {
	var events []Event
	r := New(Options{StartURL: "https://app.example/", OnEvent: func(e Event) { events = append(events, e) }})

	fill := `{"kind":"fill","value":"%s","ts":1700000000000,"element":{"tag":"input","name":"q","css_path":"form > input"}}`
	r.handleRecordBinding(nil, strings.ReplaceAll(fill, "%s", "partial"))
	r.handleRecordBinding(nil, strings.ReplaceAll(fill, "%s", "partial"))

	if len(events) != 2 {
		t.Fatalf("events = %d, want 2 (one per binding call)", len(events))
	}
	// Both events reference the same collapsed step.
	if events[1].Step.Seq != 1 || events[1].Step.Value != "partial" {
		t.Errorf("collapsed step event = %+v", events[1].Step)
	}
	if steps := r.Snapshot().Steps; len(steps) != 1 || steps[0].Value != "partial" {
		t.Errorf("session steps = %+v, want single collapsed fill", steps)
	}
}

func TestHandleRecordBindingEmitsWarningOnBadPayload(t *testing.T) {
	var events []Event
	r := New(Options{StartURL: "https://app.example/", OnEvent: func(e Event) { events = append(events, e) }})

	r.handleRecordBinding(nil, "{not json")

	if len(events) != 1 || events[0].Type != EventWarning {
		t.Fatalf("events = %+v, want one warning event", events)
	}
	if !strings.Contains(events[0].Warning, "dropped capture event") {
		t.Errorf("warning = %q", events[0].Warning)
	}
	if warnings := r.Snapshot().Warnings; len(warnings) != 1 {
		t.Errorf("session warnings = %v", warnings)
	}
}

func TestRecorderWithoutObserverDoesNotPanic(t *testing.T) {
	r := New(Options{StartURL: "https://app.example/"})
	r.handleRecordBinding(nil, `{"kind":"click","ts":1,"element":{"tag":"button","css_path":"#x"}}`)
	if got := len(r.Snapshot().Steps); got != 1 {
		t.Errorf("steps = %d, want 1", got)
	}
}

func TestSnapshotIsStableCopy(t *testing.T) {
	r := New(Options{StartURL: "https://app.example/"})
	r.handleRecordBinding(nil, `{"kind":"click","ts":1,"element":{"tag":"button","css_path":"#a"}}`)

	snap := r.Snapshot()
	r.handleRecordBinding(nil, `{"kind":"click","ts":2,"element":{"tag":"button","css_path":"#b"}}`)

	if len(snap.Steps) != 1 {
		t.Errorf("snapshot steps = %d, want 1 (must not grow with the live session)", len(snap.Steps))
	}
	if live := r.Snapshot(); len(live.Steps) != 2 {
		t.Errorf("live steps = %d, want 2", len(live.Steps))
	}
}

func TestPauseGatesCaptureAndRecordsIntervals(t *testing.T) {
	var events []Event
	r := New(Options{StartURL: "https://app.example/", OnEvent: func(e Event) { events = append(events, e) }})

	click := `{"kind":"click","ts":1,"element":{"tag":"button","css_path":"#a"}}`
	r.handleRecordBinding(nil, click)
	r.Pause()
	r.handleRecordBinding(nil, click) // dropped while paused
	r.Resume()
	r.handleRecordBinding(nil, click)

	session := r.Snapshot()
	if len(session.Steps) != 2 {
		t.Errorf("steps = %d, want 2 (paused click dropped)", len(session.Steps))
	}
	if len(session.PauseIntervals) != 1 {
		t.Fatalf("pause intervals = %+v, want 1", session.PauseIntervals)
	}
	if session.PauseIntervals[0].To.IsZero() {
		t.Error("pause interval not closed on resume")
	}

	var types []string
	for _, e := range events {
		types = append(types, e.Type)
	}
	want := []string{EventStep, EventPauseState, EventPauseState, EventStep}
	if len(types) != len(want) {
		t.Fatalf("event types = %v, want %v", types, want)
	}
	if events[1].Paused != true || events[2].Paused != false {
		t.Errorf("pause events = %+v / %+v", events[1], events[2])
	}
}

func TestPauseIsIdempotentAndStopClosesInterval(t *testing.T) {
	r := New(Options{StartURL: "https://app.example/"})
	r.Pause()
	r.Pause() // no second interval
	session, err := r.Stop()
	if err != nil {
		t.Fatal(err)
	}
	if len(session.PauseIntervals) != 1 {
		t.Fatalf("pause intervals = %+v, want 1", session.PauseIntervals)
	}
	if session.PauseIntervals[0].To.IsZero() {
		t.Error("Stop did not close the open pause interval")
	}
}

func TestPauseToggleControlEventFromToolbar(t *testing.T) {
	var events []Event
	r := New(Options{StartURL: "https://app.example/", OnEvent: func(e Event) { events = append(events, e) }})

	r.handleRecordBinding(nil, `{"kind":"pause_toggle","ts":0}`)
	if r.Paused() {
		t.Fatal("forged pause_toggle without nonce paused the recorder")
	}
	r.handleRecordBinding(nil, `{"kind":"pause_toggle","nonce":"`+r.controlNonce+`","ts":1}`)
	if !r.Paused() {
		t.Fatal("pause_toggle did not pause the recorder")
	}
	r.handleRecordBinding(nil, `{"kind":"pause_toggle","nonce":"`+r.controlNonce+`","ts":2}`)
	if r.Paused() {
		t.Fatal("second pause_toggle did not resume the recorder")
	}
	var pauseEvents int
	var warningEvents int
	for _, event := range events {
		if event.Type == EventPauseState {
			pauseEvents++
		}
		if event.Type == EventWarning && strings.Contains(event.Warning, "untrusted origin") {
			warningEvents++
		}
	}
	if pauseEvents != 2 || warningEvents != 1 {
		t.Errorf("events = %+v, want two pause_state events and one untrusted warning", events)
	}
	if warnings := r.Snapshot().Warnings; len(warnings) != 0 {
		t.Errorf("transient control warnings must not persist on snapshot: %v", warnings)
	}
}

func TestRecordingStopControlEventFromToolbar(t *testing.T) {
	var events []Event
	r := New(Options{StartURL: "https://app.example/", OnEvent: func(e Event) { events = append(events, e) }})

	r.handleRecordBinding(nil, `{"kind":"recording_stop","nonce":"`+r.controlNonce+`","ts":1}`)
	if len(events) != 1 || events[0].Type != EventStopRequested {
		t.Fatalf("events = %+v, want one stop_requested event", events)
	}
	snapshot := r.Snapshot()
	if len(snapshot.Steps) != 0 {
		t.Errorf("recording_stop control event recorded steps: %+v", snapshot.Steps)
	}
	if len(snapshot.Warnings) != 0 {
		t.Errorf("recording_stop control event produced warnings: %v", snapshot.Warnings)
	}
}

func TestAddMarkerTransactionLifecycle(t *testing.T) {
	var events []Event
	r := New(Options{StartURL: "https://app.example/", OnEvent: func(e Event) { events = append(events, e) }})

	if _, err := r.AddMarker(MarkerTransactionStart, "Checkout", "", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AddMarker(MarkerTransactionStart, "Checkout", "", 0); err == nil {
		t.Error("duplicate open transaction must be rejected")
	}
	if _, err := r.AddMarker(MarkerTransactionStart, "Payment", "", 0); err != nil {
		t.Fatal(err)
	}
	if open := r.Snapshot().OpenTransactions; len(open) != 2 || open[0] != "Checkout" || open[1] != "Payment" {
		t.Errorf("open transactions = %v", open)
	}

	// Empty name ends the innermost open transaction.
	marker, err := r.AddMarker(MarkerTransactionEnd, "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if marker.Name != "Payment" {
		t.Errorf("default end closed %q, want Payment (innermost)", marker.Name)
	}
	if _, err := r.AddMarker(MarkerTransactionEnd, "Nope", "", 0); err == nil {
		t.Error("ending an unopened transaction must be rejected")
	}
	if _, err := r.AddMarker(MarkerNote, "", "watch the cart badge", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AddMarker(MarkerNote, "", "   ", 0); err == nil {
		t.Error("empty note must be rejected")
	}

	if got := len(events); got != 4 { // 2 starts + 1 end + 1 note
		t.Errorf("marker events = %d, want 4", got)
	}

	// Stop auto-closes the still-open Checkout transaction.
	session, err := r.Stop()
	if err != nil {
		t.Fatal(err)
	}
	if len(session.OpenTransactions) != 0 {
		t.Errorf("open transactions after stop = %v", session.OpenTransactions)
	}
	last := session.Markers[len(session.Markers)-1]
	if last.Kind != MarkerTransactionEnd || last.Name != "Checkout" || !last.Auto {
		t.Errorf("auto-close marker = %+v", last)
	}
	foundWarning := false
	for _, w := range session.Warnings {
		if strings.Contains(w, "Checkout") && strings.Contains(w, "closed automatically") {
			foundWarning = true
		}
	}
	if !foundWarning {
		t.Errorf("warnings = %v, want auto-close notice", session.Warnings)
	}
}

func TestAddMarkerCanAnchorAtRecordedStep(t *testing.T) {
	r := New(Options{StartURL: "https://app.example/"})
	r.handleRecordBinding(nil, `{"kind":"click","ts":1700000000123,"element":{"tag":"button","css_path":"#cart"}}`)
	step := r.Snapshot().Steps[0]

	marker, err := r.AddMarker(MarkerTransactionStart, "Cart", "", step.Seq)
	if err != nil {
		t.Fatalf("AddMarker anchored to step: %v", err)
	}
	if !marker.Timestamp.Equal(step.Timestamp) {
		t.Fatalf("marker timestamp = %s, want step timestamp %s", marker.Timestamp, step.Timestamp)
	}
	if _, err := r.AddMarker(MarkerTransactionStart, "Missing", "", 99); err == nil {
		t.Fatal("expected missing anchor step to fail")
	}
}

func TestSectionTagsCapturedSteps(t *testing.T) {
	r := New(Options{StartURL: "https://app.example/", Section: SectionInit})
	r.handleRecordBinding(nil, `{"kind":"click","ts":1,"element":{"tag":"button","css_path":"#a"}}`)
	if got := r.SetSection(SectionTeardown); got != SectionTeardown {
		t.Fatalf("SetSection returned %q", got)
	}
	r.handleRecordBinding(nil, `{"kind":"click","ts":2,"element":{"tag":"button","css_path":"#b"}}`)

	steps := r.Snapshot().Steps
	if len(steps) != 2 {
		t.Fatalf("steps = %d", len(steps))
	}
	if steps[0].Section != SectionInit {
		t.Errorf("step 0 section = %q, want init", steps[0].Section)
	}
	if steps[1].Section != SectionTeardown {
		t.Errorf("step 1 section = %q, want teardown", steps[1].Section)
	}
}

func TestDeleteLastAndExcludeStep(t *testing.T) {
	var updates int
	r := New(Options{StartURL: "https://app.example/", OnEvent: func(e Event) {
		if e.Type == EventStepUpdate {
			updates++
		}
	}})
	r.handleRecordBinding(nil, `{"kind":"click","ts":1,"element":{"tag":"button","css_path":"#a"}}`)
	r.handleRecordBinding(nil, `{"kind":"click","ts":2,"element":{"tag":"button","css_path":"#b"}}`)

	if _, err := r.SetStepExcluded(1, true); err != nil {
		t.Fatal(err)
	}
	deleted, err := r.DeleteLastStep()
	if err != nil {
		t.Fatal(err)
	}
	if deleted.Seq != 2 {
		t.Errorf("deleted seq = %d, want 2", deleted.Seq)
	}
	steps := r.Snapshot().Steps
	if !steps[0].Excluded {
		t.Error("step 1 not excluded")
	}
	if !steps[1].Deleted {
		t.Error("step 2 not deleted")
	}
	if updates != 2 {
		t.Errorf("step_update events = %d, want 2", updates)
	}
	// Delete-last skips the already-deleted step and falls back to the excluded one.
	again, err := r.DeleteLastStep()
	if err != nil || again.Seq != 1 {
		t.Errorf("second delete = %+v err=%v", again, err)
	}
}

func TestSetStepMarkRequiresFill(t *testing.T) {
	r := New(Options{StartURL: "https://app.example/"})
	r.handleRecordBinding(nil, `{"kind":"fill","value":"secret","ts":1,"element":{"tag":"input","name":"tok","css_path":"#tok"}}`)
	r.handleRecordBinding(nil, `{"kind":"click","ts":2,"element":{"tag":"button","css_path":"#go"}}`)

	if _, err := r.SetStepMark(1, true, false); err != nil {
		t.Fatalf("mark fill: %v", err)
	}
	if !r.Snapshot().Steps[0].MarkSecret {
		t.Error("fill not marked secret")
	}
	if _, err := r.SetStepMark(2, true, false); err == nil {
		t.Error("marking a non-fill step should error")
	}
}

func TestRecordAnnotationGatedByPause(t *testing.T) {
	var annotations int
	r := New(Options{StartURL: "https://app.example/", OnEvent: func(e Event) {
		if e.Type == EventAnnotation {
			annotations++
		}
	}})
	r.recordAnnotation(Annotation{Kind: "console_error", Text: "boom"})
	r.setPaused(true)
	r.recordAnnotation(Annotation{Kind: "console_error", Text: "while paused"})
	r.setPaused(false)
	r.recordAnnotation(Annotation{Kind: "request_failed", Text: "500 /api"})

	session := r.Snapshot()
	if len(session.Annotations) != 2 {
		t.Fatalf("annotations = %d, want 2 (paused one dropped)", len(session.Annotations))
	}
	if annotations != 2 {
		t.Errorf("annotation events = %d, want 2", annotations)
	}
}

func TestRecordAnnotationBoundsCountAndText(t *testing.T) {
	r := New(Options{StartURL: "https://app.example/"})
	r.session.Annotations = make([]Annotation, maxBrowserAnnotations-1)
	r.recordAnnotation(Annotation{Kind: "console_error", Text: strings.Repeat("x", maxBrowserAnnotationLen+100)})
	r.recordAnnotation(Annotation{Kind: "console_error", Text: "overflow"})
	session := r.Snapshot()
	if len(session.Annotations) != maxBrowserAnnotations {
		t.Fatalf("annotations = %d, want cap %d", len(session.Annotations), maxBrowserAnnotations)
	}
	if got := len(session.Annotations[len(session.Annotations)-1].Text); got != maxBrowserAnnotationLen {
		t.Fatalf("annotation text length = %d, want %d", got, maxBrowserAnnotationLen)
	}
}

func TestRecordWSFrameOptIn(t *testing.T) {
	// Without opt-in, only counts move.
	r := New(Options{StartURL: "https://app.example/"})
	r.session.WebSockets = append(r.session.WebSockets, WebSocketSummary{URL: "wss://x"})
	r.recordWSFrame(0, "sent", []byte(`{"token":"abc"}`))
	if got := r.Snapshot().WebSockets[0]; got.FramesSent != 1 || len(got.Frames) != 0 {
		t.Errorf("no-opt-in WS = %+v, want count only", got)
	}

	// With opt-in + a scrubber, payloads are captured and redacted.
	scrubbed := New(Options{StartURL: "https://app.example/", CaptureWSPayloads: true,
		Scrubber: func(s string) string { return strings.ReplaceAll(s, "abc", "REDACTED") }})
	scrubbed.session.WebSockets = append(scrubbed.session.WebSockets, WebSocketSummary{URL: "wss://x"})
	scrubbed.recordWSFrame(0, "received", []byte(`{"token":"abc"}`))
	ws := scrubbed.Snapshot().WebSockets[0]
	if len(ws.Frames) != 1 || ws.FramesReceived != 1 {
		t.Fatalf("opt-in WS = %+v", ws)
	}
	if !strings.Contains(ws.Frames[0].Payload, "REDACTED") {
		t.Errorf("payload not scrubbed: %q", ws.Frames[0].Payload)
	}
}

func TestRecordWSFrameScrubsBinaryPayloads(t *testing.T) {
	// A payload with a control byte fails the printable check; the scrubber must
	// still run before base64 so secrets are redacted, not stored verbatim.
	r := New(Options{StartURL: "https://app.example/", CaptureWSPayloads: true,
		Scrubber: func(s string) string { return strings.ReplaceAll(s, "SECRET", "***") }})
	r.session.WebSockets = append(r.session.WebSockets, WebSocketSummary{URL: "wss://x"})
	r.recordWSFrame(0, "received", []byte("SECRET\x00token"))
	frame := r.Snapshot().WebSockets[0].Frames[0]
	if frame.Encoding != "base64" {
		t.Fatalf("encoding = %q, want base64", frame.Encoding)
	}
	decoded, err := base64decode(frame.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(decoded, "SECRET") {
		t.Errorf("binary payload not scrubbed before base64: %q", decoded)
	}
}

func TestRecordWSFrameIgnoredAfterStop(t *testing.T) {
	r := New(Options{StartURL: "https://app.example/", CaptureWSPayloads: true})
	r.session.WebSockets = append(r.session.WebSockets, WebSocketSummary{URL: "wss://x"})
	r.stopped = true
	r.recordWSFrame(0, "sent", []byte("late"))
	if got := r.Snapshot().WebSockets[0]; got.FramesSent != 0 || len(got.Frames) != 0 {
		t.Errorf("post-stop WS frame recorded: %+v", got)
	}
}

func TestRecordWSFrameCapsStoredFrames(t *testing.T) {
	r := New(Options{StartURL: "https://app.example/", CaptureWSPayloads: true})
	r.session.WebSockets = append(r.session.WebSockets, WebSocketSummary{URL: "wss://x", Frames: make([]WebSocketFrame, maxBrowserWSFrames)})
	r.wsFrames = maxBrowserWSFrames
	r.recordWSFrame(0, "sent", []byte("overflow"))
	if got := len(r.Snapshot().WebSockets[0].Frames); got != maxBrowserWSFrames {
		t.Fatalf("frames = %d, want cap %d", got, maxBrowserWSFrames)
	}
}

func TestBindingDropsEventsAfterStop(t *testing.T) {
	r := New(Options{StartURL: "https://app.example/"})
	r.stopped = true
	r.handleRecordBinding(nil, `{"kind":"click","ts":1,"element":{"tag":"button","css_path":"#go"}}`)
	if got := len(r.Snapshot().Steps); got != 0 {
		t.Fatalf("post-stop steps = %d, want 0", got)
	}
}

func TestPickToggleControlEvent(t *testing.T) {
	var pickEvents []bool
	r := New(Options{StartURL: "https://app.example/", OnEvent: func(e Event) {
		if e.Type == EventPickState {
			pickEvents = append(pickEvents, e.Picking)
		}
	}})
	r.handleRecordBinding(nil, `{"kind":"pick_toggle","ts":0}`)
	if r.AssertMode() {
		t.Fatal("forged pick_toggle without nonce enabled assert mode")
	}
	r.handleRecordBinding(nil, `{"kind":"pick_toggle","nonce":"`+r.controlNonce+`","ts":1}`)
	if !r.AssertMode() {
		t.Fatal("pick_toggle did not enable assert mode")
	}
	r.handleRecordBinding(nil, `{"kind":"pick_toggle","nonce":"`+r.controlNonce+`","ts":2}`)
	if r.AssertMode() {
		t.Fatal("second pick_toggle did not disable assert mode")
	}
	if len(pickEvents) != 2 || pickEvents[0] != true || pickEvents[1] != false {
		t.Errorf("pick events = %v", pickEvents)
	}
}

func TestCapturedAssertionConsumesAuthoritativePickMode(t *testing.T) {
	for _, kind := range []string{"assert", "click"} {
		t.Run(kind, func(t *testing.T) {
			var pickEvents []bool
			r := New(Options{StartURL: "https://app.example/", OnEvent: func(e Event) {
				if e.Type == EventPickState {
					pickEvents = append(pickEvents, e.Picking)
				}
			}})
			r.SetAssertMode(true)
			r.handleRecordBinding(nil, `{"kind":"`+kind+`","ts":1,"value":"Ready","element":{"tag":"div","css_path":"#result"}}`)
			if len(r.Snapshot().Steps) != 1 {
				t.Fatal("capture was not retained")
			}
			if got, want := r.AssertMode(), kind != "assert"; got != want {
				t.Fatalf("picking = %v, want %v", got, want)
			}
			if kind == "assert" && (len(pickEvents) != 2 || pickEvents[1]) {
				t.Fatalf("missing observer reset: %v", pickEvents)
			}
		})
	}
}

func TestControlEventKindRecognizesSidebarActions(t *testing.T) {
	cases := map[string]string{
		`{"kind":"pause_toggle"}`:                 "pause_toggle",
		`{"kind":"pick_toggle"}`:                  "pick_toggle",
		`{"kind":"section_set","section":"init"}`: "section_set",
		`{"kind":"transaction_start","name":"x"}`: "transaction_start",
		`{"kind":"transaction_end","name":"x"}`:   "transaction_end",
		`{"kind":"note_add","text":"hi"}`:         "note_add",
		`{"kind":"navigate","url":"https://x/"}`:  "navigate",
		`{"kind":"delete_last_step"}`:             "delete_last_step",
		`{"kind":"step_exclude","seq":1}`:         "step_exclude",
		`{"kind":"step_mark","seq":1}`:            "step_mark",
		`{"kind":"recording_stop"}`:               "recording_stop",
		// Capture steps must NOT be classified as control events.
		`{"kind":"click"}`: "",
		`{"kind":"fill"}`:  "",
		`{not json`:        "",
	}
	for payload, want := range cases {
		if got := controlEventKind(payload); got != want {
			t.Errorf("controlEventKind(%s) = %q, want %q", payload, got, want)
		}
	}
}

func TestSidebarControlEventsRouteToRecorderMethods(t *testing.T) {
	r := New(Options{StartURL: "https://app.example/"})

	// Section switch from the sidebar tags subsequent steps and is not recorded.
	// State-mutating control events carry the closure nonce (as the real sidebar
	// stamps it) so the trust gate accepts them.
	r.handleRecordBinding(nil, `{"kind":"section_set","section":"teardown","nonce":"`+r.controlNonce+`","ts":1}`)
	if got := r.Section(); got != SectionTeardown {
		t.Fatalf("section after section_set = %q, want teardown", got)
	}

	// Transaction lifecycle through the sidebar binding.
	r.handleRecordBinding(nil, `{"kind":"transaction_start","name":"Checkout","nonce":"`+r.controlNonce+`","ts":2}`)
	if open := r.Snapshot().OpenTransactions; len(open) != 1 || open[0] != "Checkout" {
		t.Fatalf("open transactions = %v, want [Checkout]", open)
	}
	r.handleRecordBinding(nil, `{"kind":"transaction_end","name":"Checkout","nonce":"`+r.controlNonce+`","ts":3}`)
	if open := r.Snapshot().OpenTransactions; len(open) != 0 {
		t.Errorf("open transactions after end = %v, want none", open)
	}

	// Note marker through the sidebar binding.
	r.handleRecordBinding(nil, `{"kind":"note_add","text":"watch the cart","nonce":"`+r.controlNonce+`","ts":4}`)

	// Record two steps, then exclude/mark/delete via the sidebar binding.
	r.handleRecordBinding(nil, `{"kind":"fill","value":"tok","ts":5,"element":{"tag":"input","name":"t","css_path":"#t"}}`)
	r.handleRecordBinding(nil, `{"kind":"click","ts":6,"element":{"tag":"button","css_path":"#go"}}`)
	r.handleRecordBinding(nil, `{"kind":"step_exclude","seq":1,"excluded":true,"nonce":"`+r.controlNonce+`","ts":7}`)
	r.handleRecordBinding(nil, `{"kind":"step_mark","seq":1,"secret":true,"nonce":"`+r.controlNonce+`","ts":8}`)
	r.handleRecordBinding(nil, `{"kind":"delete_last_step","nonce":"`+r.controlNonce+`","ts":9}`)

	steps := r.Snapshot().Steps
	if len(steps) != 2 {
		t.Fatalf("steps = %d, want 2 (control events must not record steps)", len(steps))
	}
	if !steps[0].Excluded || !steps[0].MarkSecret {
		t.Errorf("step 1 = %+v, want excluded+secret", steps[0])
	}
	if !steps[1].Deleted {
		t.Errorf("step 2 not deleted by delete_last_step")
	}
	if markers := r.Snapshot().Markers; len(markers) != 3 { // start + end + note
		t.Errorf("markers = %d, want 3", len(markers))
	}
	if warnings := r.Snapshot().Warnings; len(warnings) != 0 {
		t.Errorf("sidebar control events must not produce artifact warnings: %v", warnings)
	}
}

func TestSidebarControlEventsWorkWhilePaused(t *testing.T) {
	r := New(Options{StartURL: "https://app.example/"})
	r.Pause()
	// A capture step is dropped while paused, but a section switch still applies.
	r.handleRecordBinding(nil, `{"kind":"click","ts":1,"element":{"tag":"button","css_path":"#a"}}`)
	r.handleRecordBinding(nil, `{"kind":"section_set","section":"init","nonce":"`+r.controlNonce+`","ts":2}`)
	if got := r.Section(); got != SectionInit {
		t.Errorf("section while paused = %q, want init (control events bypass the pause gate)", got)
	}
	if steps := r.Snapshot().Steps; len(steps) != 0 {
		t.Errorf("steps = %d, want 0 (capture gated while paused)", len(steps))
	}
}

func TestSidebarControlEventErrorEmitsWarningOnly(t *testing.T) {
	var warnings []string
	r := New(Options{StartURL: "https://app.example/", OnEvent: func(e Event) {
		if e.Type == EventWarning {
			warnings = append(warnings, e.Warning)
		}
	}})
	// Ending a transaction when none is open errors; it surfaces as a warning
	// event but is NOT persisted to the recorded artifact's warnings. The nonce
	// makes the event pass the trust gate so it reaches the (failing) marker path
	// rather than being rejected as untrusted.
	r.handleRecordBinding(nil, `{"kind":"transaction_end","name":"Nope","nonce":"`+r.controlNonce+`","ts":1}`)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "transaction_end") {
		t.Fatalf("warning events = %v, want one transaction_end warning", warnings)
	}
	if got := len(r.Snapshot().Warnings); got != 0 {
		t.Errorf("artifact warnings = %d, want 0 (control errors stay transient)", got)
	}
}

func TestDestructiveControlEventsRequireSessionNonce(t *testing.T) {
	var warnings []string
	var stopRequested bool
	r := New(Options{StartURL: "https://app.example/", OnEvent: func(e Event) {
		switch e.Type {
		case EventWarning:
			warnings = append(warnings, e.Warning)
		case EventStopRequested:
			stopRequested = true
		}
	}})

	// Record two steps the way the page would (capture events need no nonce).
	r.handleRecordBinding(nil, `{"kind":"fill","value":"tok","ts":1,"element":{"tag":"input","name":"t","css_path":"#t"}}`)
	r.handleRecordBinding(nil, `{"kind":"click","ts":2,"element":{"tag":"button","css_path":"#go"}}`)

	// A recorded page forging destructive control events without (or with the
	// wrong) closure nonce must be rejected — no step mutation, no stop, only
	// transient warnings.
	forged := []string{
		`{"kind":"delete_last_step","ts":3}`,
		`{"kind":"step_exclude","seq":1,"excluded":true,"ts":4}`,
		`{"kind":"step_mark","seq":1,"secret":true,"ts":5}`,
		`{"kind":"recording_stop","ts":6}`,
		`{"kind":"step_mark","seq":1,"secret":true,"nonce":"wrong","ts":7}`,
	}
	for _, payload := range forged {
		r.handleRecordBinding(nil, payload)
	}

	steps := r.Snapshot().Steps
	if len(steps) != 2 {
		t.Fatalf("steps = %d, want 2 (forged delete must not apply)", len(steps))
	}
	if steps[0].Excluded || steps[0].MarkSecret || steps[1].Deleted {
		t.Errorf("forged control events mutated steps: %+v", steps)
	}
	if stopRequested {
		t.Error("forged recording_stop should not request a stop")
	}
	if len(warnings) != len(forged) {
		t.Errorf("warnings = %d, want %d untrusted-origin rejections: %v", len(warnings), len(forged), warnings)
	}

	// With the correct nonce the same action applies.
	r.handleRecordBinding(nil, `{"kind":"step_exclude","seq":1,"excluded":true,"nonce":"`+r.controlNonce+`","ts":9}`)
	if !r.Snapshot().Steps[0].Excluded {
		t.Error("authentic step_exclude (with nonce) did not apply")
	}
}

func TestRecordedPageCannotForgeCaptureEventsThroughBinding(t *testing.T) {
	r := New(Options{StartURL: "https://app.example/"})
	source := &playwright.BindingSource{}
	click := `{"kind":"click","ts":1,"element":{"tag":"button","css_path":"#go"}}`
	r.handleRecordBinding(source, click)
	if got := len(r.Snapshot().Steps); got != 0 {
		t.Fatalf("untrusted page binding recorded %d forged step(s)", got)
	}

	trusted := strings.TrimSuffix(click, "}") + `,"nonce":"` + r.controlNonce + `"}`
	r.handleRecordBinding(source, trusted)
	if got := len(r.Snapshot().Steps); got != 1 {
		t.Fatalf("trusted capture recorded %d steps, want 1", got)
	}
}

func TestAtomicCaptureBatchReservesPageSwitchCapacity(t *testing.T) {
	r := New(Options{StartURL: "https://app.example/"})
	first := &pageLimitPage{}
	second := &pageLimitPage{}
	r.activePage = first
	r.session.Steps = make([]Step, maxBrowserSteps-2)
	if switched := r.recordPageSwitchIfNeededLocked(second, time.Now(), SectionActions, 2); switched != nil {
		t.Fatal("page switch consumed capacity reserved for an atomic two-step batch")
	}
	if got := len(r.session.Steps); got != maxBrowserSteps-2 {
		t.Fatalf("steps = %d after omitted switch, want %d", got, maxBrowserSteps-2)
	}
}

func TestAtomicCaptureBatchReportsStepCapFromCapMinusOne(t *testing.T) {
	var events []Event
	r := New(Options{
		StartURL: "https://app.example/",
		OnEvent: func(event Event) {
			events = append(events, event)
		},
	})
	r.session.Steps = make([]Step, maxBrowserSteps-1)
	payload := `{"kind":"capture_batch","events":[` +
		`{"kind":"fill","value":"alice","ts":1700000000000,"element":{"tag":"input","id":"first","css_path":"#first"}},` +
		`{"kind":"press_key","value":"Tab","ts":1700000000000,"element":{"tag":"input","id":"first","css_path":"#first"}}]}`

	if err := r.handleRecordBinding(nil, payload); err != nil {
		t.Fatal(err)
	}
	snapshot := r.Snapshot()
	if got := len(snapshot.Steps); got != maxBrowserSteps-1 {
		t.Fatalf("steps = %d, want atomic batch dropped at %d", got, maxBrowserSteps-1)
	}
	if snapshot.CapHits["steps"] != 1 {
		t.Fatalf("step cap hits = %#v, want one reported drop", snapshot.CapHits)
	}
	var capEvent, warningEvent bool
	for _, event := range events {
		if event.Type == EventCapHit && event.CapName == "steps" && event.CapCount == 1 {
			capEvent = true
		}
		if event.Type == EventWarning && strings.Contains(event.Warning, "step limit") {
			warningEvent = true
		}
	}
	if !capEvent || !warningEvent {
		t.Fatalf("cap events = %#v, want live cap + warning", events)
	}
}

func TestRecordBindingReportsEveryAggregateByteCapHit(t *testing.T) {
	var events []Event
	r := New(Options{
		StartURL: "https://app.example/",
		OnEvent: func(event Event) {
			events = append(events, event)
		},
	})
	payload := `{"kind":"click","ts":1,"element":{"tag":"button","css_path":"#go"}}`
	r.bindingBytes = maxBrowserBindingTotalBytes - int64(len(payload)) + 1

	r.handleRecordBinding(nil, payload)
	r.handleRecordBinding(nil, payload)

	snapshot := r.Snapshot()
	if snapshot.CapHits["binding_bytes"] != 2 || len(snapshot.Steps) != 0 {
		t.Fatalf("binding cap state = hits:%#v steps:%d", snapshot.CapHits, len(snapshot.Steps))
	}
	if len(snapshot.Warnings) != 1 || !strings.Contains(snapshot.Warnings[0], "capture byte limit") {
		t.Fatalf("warnings = %#v, want one retained byte-limit warning", snapshot.Warnings)
	}
	var counts []int64
	warnings := 0
	for _, event := range events {
		if event.Type == EventCapHit && event.CapName == "binding_bytes" {
			counts = append(counts, event.CapCount)
		}
		if event.Type == EventWarning && strings.Contains(event.Warning, "capture byte limit") {
			warnings++
		}
	}
	if !slices.Equal(counts, []int64{1, 2}) || warnings != 1 {
		t.Fatalf("events = %#v, want cap counts [1 2] and one warning", events)
	}
}

func TestNavigableURLRejectsNonHTTPSchemes(t *testing.T) {
	cases := []struct {
		url string
		ok  bool
	}{
		{"https://app.example/login", true},
		{"http://localhost:3000/", true},
		{"HTTPS://App.Example/", true},
		{"file:///etc/passwd", false},
		{"chrome://settings", false},
		{"view-source://https://app.example", false},
		{"about:blank", false},
		{"", false},
		{"/relative/path", false},
	}
	for _, tc := range cases {
		err := navigableURL(tc.url)
		if tc.ok && err != nil {
			t.Errorf("navigableURL(%q) = %v, want nil", tc.url, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("navigableURL(%q) = nil, want rejection", tc.url)
		}
	}
}

func TestSidebarNavigateControlEventRejectsFileScheme(t *testing.T) {
	var warnings []string
	r := New(Options{StartURL: "https://app.example/", OnEvent: func(e Event) {
		if e.Type == EventWarning {
			warnings = append(warnings, e.Warning)
		}
	}})
	// A recorded page's JS could call this through the context-wide binding; the
	// file:// scheme must be rejected with a warning and never reach page.Goto.
	r.handleRecordBinding(nil, `{"kind":"navigate","url":"file:///etc/passwd","ts":1}`)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "navigate") {
		t.Fatalf("warning events = %v, want one navigate scheme warning", warnings)
	}
}

func TestBuildSidebarStateSnapshot(t *testing.T) {
	r := New(Options{StartURL: "https://app.example/", Section: SectionInit})
	r.session.NavEvents = append(r.session.NavEvents, NavEvent{URL: "https://app.example/next"})
	r.handleRecordBinding(nil, `{"kind":"fill","value":"hunter2","ts":1,"element":{"tag":"input","name":"u","css_path":"#u","text":""}}`)
	r.handleRecordBinding(nil, `{"kind":"click","ts":2,"element":{"tag":"button","css_path":"#go","text":"Submit"}}`)
	if _, err := r.AddMarker(MarkerTransactionStart, "Login", "", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SetStepExcluded(2, true); err != nil {
		t.Fatal(err)
	}

	state := r.buildSidebarStateLocked()
	if state.Section != SectionInit {
		t.Errorf("section = %q, want init", state.Section)
	}
	if state.NavCount != 1 {
		t.Errorf("navCount = %d, want 1", state.NavCount)
	}
	if state.StepCount != 1 {
		t.Errorf("stepCount = %d, want 1 (excluded step does not count)", state.StepCount)
	}
	if len(state.OpenTransactions) != 1 || state.OpenTransactions[0] != "Login" {
		t.Errorf("openTransactions = %v, want [Login]", state.OpenTransactions)
	}
	if len(state.RecentSteps) != 2 {
		t.Fatalf("recentSteps = %d, want 2", len(state.RecentSteps))
	}
	if !state.RecentSteps[0].Fill {
		t.Errorf("step 1 should be flagged as a fill")
	}
	if state.RecentSteps[1].Detail != "Submit" || !state.RecentSteps[1].Excluded {
		t.Errorf("step 2 view = %+v, want detail=Submit excluded=true", state.RecentSteps[1])
	}

	// JSON marshals with stable, JS-facing field names.
	if encoded := r.buildSidebarStateJSON(); !strings.Contains(encoded, `"recentSteps"`) || !strings.Contains(encoded, `"openTransactions"`) {
		t.Errorf("sidebar state JSON missing expected keys: %s", encoded)
	}
}

func TestCaptureSidebarUsesTransactionStartStopToggle(t *testing.T) {
	if !strings.Contains(captureScript, `class="txnToggle"`) {
		t.Fatal("capture sidebar must render a single transaction toggle button")
	}
	if !strings.Contains(captureScript, "txnToggle.textContent = 'Stop'") {
		t.Fatal("capture sidebar must switch the transaction toggle to visible Stop text")
	}
	if !strings.Contains(captureScript, `txnToggle.title = 'Stop transaction "' + current + '"'`) {
		t.Fatal("capture sidebar stop toggle should name the active transaction in its title")
	}
	if !strings.Contains(captureScript, "send({ kind: 'transaction_end', name: current, ts: Date.now() });") {
		t.Fatal("capture sidebar stop toggle must end the active transaction")
	}
}

func TestCaptureToolbarProvidesRecordingStopControl(t *testing.T) {
	if !strings.Contains(captureScript, `class="stopRec" title="Stop recording">Stop</button>`) {
		t.Fatal("compact recorder toolbar must render a visible Stop button")
	}
	if strings.Count(captureScript, `class="stopRec" title="Stop recording">Stop</button>`) < 2 {
		t.Fatal("expanded recorder controls must also render a visible Stop button")
	}
	if !strings.Contains(captureScript, "send({ kind: 'recording_stop', ts: Date.now() });") {
		t.Fatal("recorder Stop button must send a recording_stop control event")
	}
}

func TestCaptureScriptRecordsP3InputParityEvents(t *testing.T) {
	for _, fragment := range []string{
		`window.addEventListener('contextmenu'`,
		`window.addEventListener('auxclick'`,
		`window.addEventListener('input'`,
		`const editableFillStates = new Map()`,
		`function contentEditableHost`,
		`scheduleEditableFill(el, 'input')`,
		`flushEditableFill(el, 'focusout')`,
		`window.addEventListener('focusout'`,
		`window.addEventListener('mouseover'`,
		`record('hover'`,
		`window.addEventListener('scroll'`,
		`record('scroll'`,
		`values: values`,
		`[contenteditable]`,
	} {
		if !strings.Contains(captureScript, fragment) {
			t.Fatalf("capture script missing %s", fragment)
		}
	}
}

func TestCaptureScriptPinsTrustedBindingAndRejectsSyntheticInput(t *testing.T) {
	for _, fragment := range []string{
		"const RECORD_BINDING = typeof window.__apexloadRecord",
		"const SAFE_JSON_STRINGIFY = JSON.stringify.bind(JSON)",
		"const CONTROL_NONCE_SUFFIX =",
		"const result = RECORD_BINDING(serialized)",
	} {
		if !strings.Contains(captureScript, fragment) {
			t.Fatalf("capture script missing trusted-binding guard %q", fragment)
		}
	}
	if strings.Contains(captureScript, "window.__apexloadRecord(serialized)") {
		t.Fatal("capture script must not dynamically resolve the page-mutable binding")
	}
	if got := strings.Count(captureScript, "if (!ev.isTrusted) return;"); got < 13 {
		t.Fatalf("capture script has %d synthetic-event guards, want at least 13", got)
	}
}

func TestControlPanelRouteIsNotInstrumented(t *testing.T) {
	if err := ValidateControlPanelURL("http://127.0.0.1:8080/#/recorder-panel?session=abc"); err != nil {
		t.Fatalf("valid control panel URL rejected: %v", err)
	}
	for _, raw := range []string{
		"http://127.0.0.1:8080/#/scripts",
		"file:///tmp/dashboard.html#/recorder-panel",
		"/#/recorder-panel?session=abc",
	} {
		if err := ValidateControlPanelURL(raw); err == nil {
			t.Errorf("ValidateControlPanelURL(%q) succeeded, want error", raw)
		}
	}
	if !strings.Contains(captureScript, "isRecorderControlPanelPage") {
		t.Fatal("capture script should detect recorder-panel route")
	}
	if !strings.Contains(captureScript, "if (isRecorderControlPanelPage()) return;") {
		t.Fatal("capture script must bail out before installing listeners on the control panel")
	}
}

func TestSidebarStepDetailMasksFills(t *testing.T) {
	masked := sidebarStepDetail(Step{Kind: StepFill, Masked: true, Value: ""})
	if masked != "••••" {
		t.Errorf("masked fill detail = %q, want ••••", masked)
	}
	nav := sidebarStepDetail(Step{Kind: StepNavigate, URL: "https://x/y"})
	if nav != "https://x/y" {
		t.Errorf("navigate detail = %q", nav)
	}
}

func TestConfigScriptReflectsSidebarOption(t *testing.T) {
	on := New(Options{StartURL: "https://app.example/", Sidebar: true}).configScript()
	if !strings.Contains(on, `"sidebar":true`) || !strings.Contains(on, "__apexloadRecorderConfig") {
		t.Errorf("config script (enabled) = %q", on)
	}
	off := New(Options{StartURL: "https://app.example/", Sidebar: false}).configScript()
	if !strings.Contains(off, `"sidebar":false`) {
		t.Errorf("config script (disabled) = %q", off)
	}
}

func TestSessionCloneCopiesSlices(t *testing.T) {
	session := &Session{
		ID:        "rec_x",
		StartURL:  "https://app.example/",
		StartedAt: time.Now(),
		Warnings:  []string{"w1"},
		HAR:       []byte(`{"log":{}}`),
	}
	session.appendStep(Step{Kind: StepClick, Timestamp: time.Now()})

	clone := session.clone()
	session.appendStep(Step{Kind: StepClick, Timestamp: time.Now()})
	session.Warnings = append(session.Warnings, "w2")
	session.HAR[0] = 'X'

	if len(clone.Steps) != 1 {
		t.Errorf("clone steps = %d, want 1", len(clone.Steps))
	}
	if len(clone.Warnings) != 1 {
		t.Errorf("clone warnings = %v", clone.Warnings)
	}
	if clone.HAR[0] != '{' {
		t.Errorf("clone HAR mutated: %q", clone.HAR[:1])
	}
}

func base64decode(s string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	return string(b), err
}

// TestControlKindRequiresTrustCoversStateMutatingKinds asserts that the nonce
// trust gate covers not just destructive controls but also the state-mutating
// controls reachable by arbitrary recorded-page JS via the context-wide binding.
func TestControlKindRequiresTrustCoversStateMutatingKinds(t *testing.T) {
	trusted := []string{
		"recording_stop", "delete_last_step", "step_exclude", "step_mark",
		"section_set", "transaction_start", "transaction_end", "note_add",
		"pause_toggle", "pick_toggle",
	}
	for _, kind := range trusted {
		if !controlKindRequiresTrust(kind) {
			t.Errorf("controlKindRequiresTrust(%q) = false, want true (must be nonce-gated)", kind)
		}
	}
	// A read-only / non-mutating kind does not require the nonce.
	for _, kind := range []string{"navigate", "focus", "", "unknown_kind"} {
		if controlKindRequiresTrust(kind) {
			t.Errorf("controlKindRequiresTrust(%q) = true, want false", kind)
		}
	}
}

func TestRecorderLaunchArgsDisableQUIC(t *testing.T) {
	if args := New(Options{DisableQUIC: true}).windowArgs(); !slices.Contains(args, "--disable-quic") {
		t.Fatalf("args = %v", args)
	}
	if args := New(Options{}).windowArgs(); slices.Contains(args, "--disable-quic") {
		t.Fatalf("args = %v", args)
	}
}
