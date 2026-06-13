package browser

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

// Unit tests for the recorder's capture pipeline that do not need a real
// browser: the binding handler, live event emission, and session snapshots.

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
	r.handleRecordBinding(nil, strings.ReplaceAll(fill, "%s", "par"))
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

	r.handleRecordBinding(nil, `{"kind":"pause_toggle","ts":1}`)
	if !r.Paused() {
		t.Fatal("pause_toggle did not pause the recorder")
	}
	r.handleRecordBinding(nil, `{"kind":"pause_toggle","ts":2}`)
	if r.Paused() {
		t.Fatal("second pause_toggle did not resume the recorder")
	}
	if len(events) != 2 || events[0].Type != EventPauseState || events[1].Type != EventPauseState {
		t.Errorf("events = %+v, want two pause_state events", events)
	}
	if warnings := r.Snapshot().Warnings; len(warnings) != 0 {
		t.Errorf("control events must not produce warnings: %v", warnings)
	}
}

func TestAddMarkerTransactionLifecycle(t *testing.T) {
	var events []Event
	r := New(Options{StartURL: "https://app.example/", OnEvent: func(e Event) { events = append(events, e) }})

	if _, err := r.AddMarker(MarkerTransactionStart, "Checkout", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AddMarker(MarkerTransactionStart, "Checkout", ""); err == nil {
		t.Error("duplicate open transaction must be rejected")
	}
	if _, err := r.AddMarker(MarkerTransactionStart, "Payment", ""); err != nil {
		t.Fatal(err)
	}
	if open := r.Snapshot().OpenTransactions; len(open) != 2 || open[0] != "Checkout" || open[1] != "Payment" {
		t.Errorf("open transactions = %v", open)
	}

	// Empty name ends the innermost open transaction.
	marker, err := r.AddMarker(MarkerTransactionEnd, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if marker.Name != "Payment" {
		t.Errorf("default end closed %q, want Payment (innermost)", marker.Name)
	}
	if _, err := r.AddMarker(MarkerTransactionEnd, "Nope", ""); err == nil {
		t.Error("ending an unopened transaction must be rejected")
	}
	if _, err := r.AddMarker(MarkerNote, "", "watch the cart badge"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AddMarker(MarkerNote, "", "   "); err == nil {
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

func TestPickToggleControlEvent(t *testing.T) {
	var pickEvents []bool
	r := New(Options{StartURL: "https://app.example/", OnEvent: func(e Event) {
		if e.Type == EventPickState {
			pickEvents = append(pickEvents, e.Picking)
		}
	}})
	r.handleRecordBinding(nil, `{"kind":"pick_toggle","ts":1}`)
	if !r.AssertMode() {
		t.Fatal("pick_toggle did not enable assert mode")
	}
	r.handleRecordBinding(nil, `{"kind":"pick_toggle","ts":2}`)
	if r.AssertMode() {
		t.Fatal("second pick_toggle did not disable assert mode")
	}
	if len(pickEvents) != 2 || pickEvents[0] != true || pickEvents[1] != false {
		t.Errorf("pick events = %v", pickEvents)
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
