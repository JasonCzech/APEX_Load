package recordbridge

import (
	"testing"
	"time"

	recbrowser "github.com/jasonczech/apexload/pkg/recorder/browser"
)

// The stop watchdog outlasts the recorder's own stop budget; a shorter one
// stops the Playwright driver while the HAR archive is still being written.
func TestBrowserRecordingStopWatchdogOutlastsTheRecorderStopBudget(t *testing.T) {
	if DefaultBrowserRecordingStopTimeout <= recbrowser.MaxStopDuration {
		t.Fatalf("watchdog %s does not outlast the recorder stop budget %s", DefaultBrowserRecordingStopTimeout, recbrowser.MaxStopDuration)
	}
	if got := NewBrowserRecordingManager(newTestEventSink()).stopTimeout; got != DefaultBrowserRecordingStopTimeout {
		t.Fatalf("manager stop timeout = %s", got)
	}
}

// A force-finish during the HAR export reports the HAR as unavailable for
// that reason, and the session view carries the report.
func TestForceStopDuringHARExportReportsTheHARUnavailable(t *testing.T) {
	hub := newTestEventSink()
	m := NewBrowserRecordingManager(hub)
	m.stopTimeout = time.Hour
	artifact := recordedBrowserArtifact()
	artifact.HAR = nil // a live snapshot has no HAR until the export finishes
	artifact.HARPath = "/tmp/capture.har"
	artifact.HARFinalization = &recbrowser.HARFinalization{Phase: recbrowser.HARFinalizationExporting, BudgetMs: 30000}
	fake := newFakeBrowserRecorder(artifact)
	fake.stopStarted = make(chan struct{})
	fake.stopRelease = make(chan struct{})
	defer close(fake.stopRelease)
	fake.install(m)

	started, err := m.Start(BrowserRecordingStartRequest{StartURL: "https://shop.example/"})
	if err != nil {
		t.Fatal(err)
	}
	m.Stop(started.ID)
	<-fake.stopStarted
	stopping, _ := m.Get(started.ID)
	if stopping.HARFinalization == nil || stopping.HARFinalization.Phase != recbrowser.HARFinalizationExporting {
		t.Fatalf("stopping view finalization = %+v, want the live exporting phase", stopping.HARFinalization)
	}
	forced, ok, err := m.ForceStop(started.ID)
	if !ok || err != nil {
		t.Fatalf("ForceStop() ok=%v err=%v", ok, err)
	}
	report := forced.HARFinalization
	if report == nil || report.Phase != recbrowser.HARFinalizationFailed || report.Reason != recbrowser.HARFinalizationForceStopped {
		t.Fatalf("forced finalization = %+v", report)
	}
	if summary := m.List(""); len(summary) == 0 || summary[0].HARFinalization == nil || summary[0].HARFinalization.Reason != recbrowser.HARFinalizationForceStopped {
		t.Fatalf("list summary finalization missing: %+v", summary)
	}
}

// A late graceful-stop result without a HAR keeps the force-finish report;
// one that brings the HAR is adopted with its own report.
func TestLateStopResultKeepsTheForcedHARReportUnlessItBringsTheHAR(t *testing.T) {
	forced := &recbrowser.Session{HARFinalization: &recbrowser.HARFinalization{Phase: recbrowser.HARFinalizationFailed, Reason: recbrowser.HARFinalizationForceStopped}}
	late := &recbrowser.Session{HARFinalization: &recbrowser.HARFinalization{Phase: recbrowser.HARFinalizationProcessing}}
	if got := recoverLateBrowserRecordingArtifact(forced, late).HARFinalization; got.Reason != recbrowser.HARFinalizationForceStopped {
		t.Fatalf("late result without HAR reported %+v", got)
	}
	late = &recbrowser.Session{HAR: []byte(`{"log":{"entries":[]}}`), HARFinalization: &recbrowser.HARFinalization{Phase: recbrowser.HARFinalizationComplete, WriterCompleted: true}}
	if got := recoverLateBrowserRecordingArtifact(forced, late).HARFinalization; got.Phase != recbrowser.HARFinalizationComplete {
		t.Fatalf("late result with HAR reported %+v", got)
	}
	noForcedReport := &recbrowser.Session{}
	late = &recbrowser.Session{HARFinalization: &recbrowser.HARFinalization{Phase: recbrowser.HARFinalizationExporting}}
	if got := recoverLateBrowserRecordingArtifact(noForcedReport, late).HARFinalization; got.Phase != recbrowser.HARFinalizationFailed {
		t.Fatalf("late in-progress phase adopted: %+v", got)
	}
}

// A late result that knows its own cause (here a size limit) replaces the
// generic force-finish report.
func TestLateStopResultKeepsItsOwnHARFailureCause(t *testing.T) {
	forced := &recbrowser.Session{HARFinalization: &recbrowser.HARFinalization{Phase: recbrowser.HARFinalizationFailed, Reason: recbrowser.HARFinalizationForceStopped}}
	for reason, want := range map[string]string{
		recbrowser.HARFinalizationSizeLimit:         recbrowser.HARFinalizationSizeLimit,
		recbrowser.HARFinalizationArchiveInvalid:    recbrowser.HARFinalizationArchiveInvalid,
		recbrowser.HARFinalizationExportFailed:      recbrowser.HARFinalizationExportFailed,
		recbrowser.HARFinalizationBrowserClosed:     recbrowser.HARFinalizationBrowserClosed,
		recbrowser.HARFinalizationWriterUnconfirmed: recbrowser.HARFinalizationWriterUnconfirmed,
	} {
		late := &recbrowser.Session{HARFinalization: &recbrowser.HARFinalization{Phase: recbrowser.HARFinalizationFailed, Reason: reason}}
		if got := recoverLateBrowserRecordingArtifact(forced, late).HARFinalization.Reason; got != want {
			t.Errorf("late %s -> %s, want %s", reason, got, want)
		}
	}
}

// A HAR the graceful stop finishes after a force-finish is adopted even when
// the operator already edited the stopped artifact; the edit is kept.
func TestLateHARIsAdoptedOntoACuratedForceFinishedRecording(t *testing.T) {
	m := NewBrowserRecordingManager(newTestEventSink())
	m.stopTimeout = time.Hour
	artifact := recordedBrowserArtifact()
	artifact.HAR = nil
	artifact.HARPath = "/tmp/capture.har"
	artifact.HARFinalization = &recbrowser.HARFinalization{Phase: recbrowser.HARFinalizationProcessing}
	fake := newFakeBrowserRecorder(artifact)
	fake.stopStarted = make(chan struct{})
	fake.stopRelease = make(chan struct{})
	fake.install(m)

	started, err := m.Start(BrowserRecordingStartRequest{StartURL: "https://shop.example/"})
	if err != nil {
		t.Fatal(err)
	}
	m.Stop(started.ID)
	<-fake.stopStarted
	if _, ok, err := m.ForceStop(started.ID); !ok || err != nil {
		t.Fatalf("ForceStop ok=%v err=%v", ok, err)
	}
	forced, _ := m.Get(started.ID)
	if len(forced.Steps) == 0 {
		t.Fatal("no steps to curate")
	}
	if _, ok, err := m.SetStepMark(started.ID, forced.Steps[0].Seq, true, false); !ok || err != nil {
		t.Fatalf("SetStepMark ok=%v err=%v", ok, err)
	}
	fake.mu.Lock()
	fake.session.HAR = []byte(`{"log":{"entries":[]}}`)
	fake.session.HARFinalization = &recbrowser.HARFinalization{Phase: recbrowser.HARFinalizationComplete, WriterCompleted: true}
	fake.mu.Unlock()
	close(fake.stopRelease)
	waitForCondition(t, time.Second, func() bool {
		got, _ := m.Get(started.ID)
		return got.HARAvailable
	}, "late HAR adopted onto the curated artifact")
	after, _ := m.Get(started.ID)
	if after.HARFinalization == nil || after.HARFinalization.Phase != recbrowser.HARFinalizationComplete {
		t.Fatalf("finalization = %+v", after.HARFinalization)
	}
	if !after.Steps[0].MarkSecret {
		t.Fatal("the operator's mark was lost to the late result")
	}
}
