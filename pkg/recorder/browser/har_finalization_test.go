package browser

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	playwright "github.com/mxschmitt/playwright-go"
)

// harExportContext stands in for a recording context: close runs export, the
// owned close is used when owned is set, and IsClosed reports closed.
type harExportContext struct {
	playwright.BrowserContext
	export      func() error
	ownedCalls  atomic.Int32
	plainCalls  atomic.Int32
	closed      bool
	ownedResult error
}

func (c *harExportContext) Close(...playwright.BrowserContextCloseOptions) error {
	c.plainCalls.Add(1)
	return c.export()
}

func (c *harExportContext) IsClosed() bool { return c.closed }

// A stopped recording with no open page: Stop exports nothing but the HAR.
func (c *harExportContext) Pages() []playwright.Page { return nil }

func (c *harExportContext) StorageState(...playwright.BrowserContextStorageStateOptions) (*playwright.StorageState, error) {
	return &playwright.StorageState{}, nil
}

type ownedHARExportContext struct{ *harExportContext }

func (c ownedHARExportContext) CloseHAROwned(...playwright.BrowserContextCloseOptions) error {
	c.ownedCalls.Add(1)
	err := c.export()
	if c.ownedResult != nil {
		return c.ownedResult
	}
	return err
}

func withHARBudgets(t *testing.T, export, grace, processing time.Duration) {
	t.Helper()
	oldExport, oldGrace, oldProcessing := harFinalizationBudget, harWriterJoinGrace, harProcessingBudget
	harFinalizationBudget, harWriterJoinGrace, harProcessingBudget = export, grace, processing
	t.Cleanup(func() {
		harFinalizationBudget, harWriterJoinGrace, harProcessingBudget = oldExport, oldGrace, oldProcessing
	})
}

func harFinalizationRecorder(t *testing.T) (*Recorder, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "capture.har")
	return &Recorder{session: &Session{ID: "rec"}, harCapturePath: path, harTemp: true}, path
}

const finishedHAR = `{"log":{"version":"1.2","creator":{"name":"t","version":"1"},"entries":[{"startedDateTime":"2026-11-05T00:00:00Z","time":1,"request":{"method":"GET","url":"https://app.example.test/","httpVersion":"HTTP/1.1","headers":[],"queryString":[],"cookies":[],"headersSize":-1,"bodySize":0},"response":{"status":200,"statusText":"OK","httpVersion":"HTTP/1.1","headers":[],"cookies":[],"content":{"size":0,"mimeType":"text/html"},"redirectURL":"","headersSize":-1,"bodySize":0},"cache":{},"timings":{"send":0,"wait":1,"receive":0}}]}}`

func finishRecorderHAR(r *Recorder) {
	outcome := r.processHARArchive()
	r.mu.Lock()
	r.applyHARArchiveLocked(outcome)
	r.mu.Unlock()
}

// An export that takes several polling intervals, inside its budget, is
// joined through the owned close and its archive becomes the session HAR.
func TestFinalizeHARExportJoinsASlowExportWithinItsBudget(t *testing.T) {
	withHARBudgets(t, 2*time.Second, 200*time.Millisecond, 2*time.Second)
	r, path := harFinalizationRecorder(t)
	context := ownedHARExportContext{&harExportContext{export: func() error {
		time.Sleep(300 * time.Millisecond) // writing attachments, then the archive
		return os.WriteFile(path, []byte(finishedHAR), 0o600)
	}}}
	r.finalizeHARExport(context)
	if context.ownedCalls.Load() != 1 || context.plainCalls.Load() != 0 {
		t.Fatalf("owned closes = %d, plain closes = %d; want the owned close that joins the export", context.ownedCalls.Load(), context.plainCalls.Load())
	}
	finishRecorderHAR(r)
	report := r.session.HARFinalization
	if len(r.session.HAR) == 0 || report.Phase != HARFinalizationComplete || !report.WriterCompleted || report.Reason != "" {
		t.Fatalf("HAR bytes = %d, report = %+v, warnings = %v", len(r.session.HAR), report, r.session.Warnings)
	}
	if report.ExportMs < 250 || report.ArchiveBytes == 0 || report.BudgetMs != 2000 {
		t.Fatalf("report = %+v, want the export duration, archive size and budget", report)
	}
}

// An export that never returns is aborted at its budget; since its writer
// never confirms it stopped, the archive is not read at all.
func TestFinalizeHARExportDoesNotReadAnArchiveWhoseWriterNeverReturned(t *testing.T) {
	withHARBudgets(t, 50*time.Millisecond, 50*time.Millisecond, time.Second)
	r, path := harFinalizationRecorder(t)
	if err := os.WriteFile(path, []byte(finishedHAR), 0o600); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	context := ownedHARExportContext{&harExportContext{export: func() error { <-release; return nil }}}
	started := time.Now()
	r.finalizeHARExport(context)
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("a hanging export held Stop for %s", elapsed)
	}
	finishRecorderHAR(r)
	report := r.session.HARFinalization
	if report.Phase != HARFinalizationFailed || report.Reason != HARFinalizationWriterUnconfirmed || report.WriterCompleted {
		t.Fatalf("report = %+v", report)
	}
	if len(r.session.HAR) != 0 || report.ProcessMs != 0 {
		t.Fatalf("read an archive a writer may still change: HAR bytes %d, report %+v", len(r.session.HAR), report)
	}
	if !strings.Contains(strings.Join(r.session.Warnings, "\n"), "was not read") {
		t.Fatalf("warnings = %v", r.session.Warnings)
	}
}

// An export that finishes in the grace after its budget is complete: the
// archive check, not the clock, decides whether it is usable.
func TestFinalizeHARExportKeepsAnExportThatFinishesInTheJoinGrace(t *testing.T) {
	withHARBudgets(t, 50*time.Millisecond, 2*time.Second, 2*time.Second)
	r, path := harFinalizationRecorder(t)
	context := ownedHARExportContext{&harExportContext{export: func() error {
		time.Sleep(200 * time.Millisecond)
		return os.WriteFile(path, []byte(finishedHAR), 0o600)
	}}}
	r.finalizeHARExport(context)
	finishRecorderHAR(r)
	if report := r.session.HARFinalization; report.Phase != HARFinalizationComplete || len(r.session.HAR) == 0 {
		t.Fatalf("report = %+v, HAR bytes = %d, warnings = %v", report, len(r.session.HAR), r.session.Warnings)
	}
	if !strings.Contains(strings.Join(r.session.Warnings, "\n"), "overran") {
		t.Fatalf("warnings = %v, want the overrun noted", r.session.Warnings)
	}
}

// An aborted export that then fails reports the exhausted budget.
func TestFinalizeHARExportReportsAnAbortedExport(t *testing.T) {
	withHARBudgets(t, 50*time.Millisecond, 2*time.Second, time.Second)
	r, _ := harFinalizationRecorder(t)
	context := ownedHARExportContext{&harExportContext{export: func() error {
		time.Sleep(150 * time.Millisecond)
		return errors.New("target closed")
	}}}
	r.finalizeHARExport(context)
	finishRecorderHAR(r)
	if report := r.session.HARFinalization; report.Reason != HARFinalizationBudgetExhausted || !report.WriterCompleted {
		t.Fatalf("report = %+v", report)
	}
}

// A truncated archive (no ZIP end record) is refused, not published.
func TestFinalizeHARExportRefusesAnIncompleteArchive(t *testing.T) {
	withHARBudgets(t, time.Second, 100*time.Millisecond, time.Second)
	r, path := harFinalizationRecorder(t)
	context := ownedHARExportContext{&harExportContext{export: func() error {
		return os.WriteFile(path, []byte("PK\x03\x04 truncated archive"), 0o600)
	}}}
	r.finalizeHARExport(context)
	finishRecorderHAR(r)
	report := r.session.HARFinalization
	if report.Phase != HARFinalizationFailed || report.Reason != HARFinalizationArchiveInvalid || len(r.session.HAR) != 0 {
		t.Fatalf("report = %+v, HAR bytes = %d", report, len(r.session.HAR))
	}
}

// A browser the user already closed exported nothing; the report says so.
func TestFinalizeHARExportReportsABrowserClosedBeforeExport(t *testing.T) {
	withHARBudgets(t, time.Second, 100*time.Millisecond, time.Second)
	r, _ := harFinalizationRecorder(t)
	context := ownedHARExportContext{&harExportContext{closed: true, ownedResult: playwright.ErrHARCloseOwnershipUnproven, export: func() error { return nil }}}
	r.finalizeHARExport(context)
	finishRecorderHAR(r)
	if report := r.session.HARFinalization; report.Reason != HARFinalizationBrowserClosed {
		t.Fatalf("report = %+v", report)
	}
	if !strings.Contains(strings.Join(r.session.Warnings, "\n"), "browser closed before the HAR could be exported") {
		t.Fatalf("warnings = %v", r.session.Warnings)
	}
}

// Status reads stay answerable while a large archive is processed: the
// processing runs outside the recorder lock.
func TestProcessHARArchiveDoesNotHoldTheRecorderLock(t *testing.T) {
	withHARBudgets(t, time.Second, 100*time.Millisecond, time.Second)
	r, path := harFinalizationRecorder(t)
	if err := os.WriteFile(path, []byte(finishedHAR), 0o600); err != nil {
		t.Fatal(err)
	}
	r.session.HARFinalization = &HARFinalization{Phase: HARFinalizationProcessing}
	loading, release := make(chan struct{}), make(chan struct{})
	oldLoader := harArchiveLoader
	harArchiveLoader = func(path string, limit int64, filters HARFilters, deadline time.Time) (harArtifact, error) {
		close(loading)
		<-release
		return oldLoader(path, limit, filters, deadline)
	}
	t.Cleanup(func() { harArchiveLoader = oldLoader })
	outcomes := make(chan harArchiveOutcome, 1)
	go func() { outcomes <- r.processHARArchive() }()
	<-loading
	snapshot := make(chan *Session, 1)
	go func() { snapshot <- r.Snapshot() }()
	select {
	case got := <-snapshot:
		if got.HARFinalization == nil || got.HARFinalization.Phase != HARFinalizationProcessing {
			t.Fatalf("snapshot finalization = %+v", got.HARFinalization)
		}
	case <-time.After(time.Second):
		close(release)
		t.Fatal("a status read blocked while the archive was processed")
	}
	close(release)
	if outcome := <-outcomes; !outcome.attempted || outcome.err != nil {
		t.Fatalf("outcome = %+v", outcome)
	}
}

// The owned close joins every failure with ErrHARCloseOwnershipUnproven; a
// real failure on an open context is an export failure with its cause kept.
func TestFinalizeHARExportKeepsTheCauseOfARealExportFailure(t *testing.T) {
	withHARBudgets(t, time.Second, 100*time.Millisecond, time.Second)
	r, _ := harFinalizationRecorder(t)
	inner := &harExportContext{ownedResult: errors.Join(errors.New("zip: disk full"), playwright.ErrHARCloseOwnershipUnproven)}
	// The real close marks the context closed as it runs.
	inner.export = func() error { inner.closed = true; return nil }
	context := ownedHARExportContext{inner}
	r.finalizeHARExport(context)
	finishRecorderHAR(r)
	if report := r.session.HARFinalization; report.Reason != HARFinalizationExportFailed {
		t.Fatalf("report = %+v, want export_failed", report)
	}
	if !strings.Contains(strings.Join(r.session.Warnings, "\n"), "disk full") {
		t.Fatalf("warnings = %v, want the export's own error", r.session.Warnings)
	}
}

// A capture cap tripped mid-recording closed the context itself; the
// finished session still says why there is no HAR.
func TestApplyHARArchiveReportsAMidRecordingSizeLimit(t *testing.T) {
	r, path := harFinalizationRecorder(t)
	if err := os.WriteFile(path, []byte(finishedHAR), 0o600); err != nil {
		t.Fatal(err)
	}
	r.harLimitExceeded = true
	finishRecorderHAR(r)
	if report := r.session.HARFinalization; report == nil || report.Reason != HARFinalizationSizeLimit {
		t.Fatalf("report = %+v", report)
	}
}

// The export gets at least the replay runtime's 30s, not the old fixed 3s.
func TestHARFinalizationBudgetOutlastsTheOldFlushAllowance(t *testing.T) {
	if browserHARFinalizationBudget < 30*time.Second {
		t.Fatalf("HAR finalization budget = %s, want at least 30s", browserHARFinalizationBudget)
	}
}

func stoppableHARRecorder(t *testing.T, export func(path string) error) (*Recorder, string) {
	t.Helper()
	r, path := harFinalizationRecorder(t)
	r.done = make(chan struct{})
	r.session.HARPath = path
	r.context = ownedHARExportContext{&harExportContext{export: func() error { return export(path) }}}
	return r, path
}

// Through Stop: status reads answer while the archive is processed, and the
// finished session carries the HAR.
func TestStopAnswersStatusReadsWhileTheHARIsProcessed(t *testing.T) {
	withHARBudgets(t, 2*time.Second, 200*time.Millisecond, 2*time.Second)
	r, _ := stoppableHARRecorder(t, func(path string) error { return os.WriteFile(path, []byte(finishedHAR), 0o600) })
	loading, release := make(chan struct{}), make(chan struct{})
	oldLoader := harArchiveLoader
	harArchiveLoader = func(path string, limit int64, filters HARFilters, deadline time.Time) (harArtifact, error) {
		close(loading)
		<-release
		return oldLoader(path, limit, filters, deadline)
	}
	t.Cleanup(func() { harArchiveLoader = oldLoader })
	stopped := make(chan *Session, 1)
	go func() { session, _ := r.Stop(); stopped <- session }()
	<-loading
	snapshot := make(chan *Session, 1)
	go func() { snapshot <- r.Snapshot() }()
	select {
	case <-snapshot:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("Snapshot blocked while Stop processed the HAR")
	}
	close(release)
	session := <-stopped
	if len(session.HAR) == 0 || session.HARFinalization.Phase != HARFinalizationComplete {
		t.Fatalf("finished HAR bytes %d, report %+v, warnings %v", len(session.HAR), session.HARFinalization, session.Warnings)
	}
}

// Through Stop: a ForceStop racing a graceful stop's slow export keeps the
// archive for that stop to read, then nothing is left on disk.
func TestForceStopDuringAGracefulStopKeepsTheHARForThatStop(t *testing.T) {
	withHARBudgets(t, 2*time.Second, 200*time.Millisecond, 2*time.Second)
	exporting := make(chan struct{})
	release := make(chan struct{})
	r, path := stoppableHARRecorder(t, func(path string) error {
		if err := os.WriteFile(path, []byte(finishedHAR), 0o600); err != nil {
			return err
		}
		close(exporting)
		<-release
		return nil
	})
	stopped := make(chan *Session, 1)
	go func() { session, _ := r.Stop(); stopped <- session }()
	<-exporting
	forced := make(chan struct{})
	go func() { r.ForceStop(); close(forced) }()
	time.Sleep(50 * time.Millisecond) // ForceStop is waiting on the owning stop
	close(release)
	session := <-stopped
	<-forced
	if len(session.HAR) == 0 {
		t.Fatalf("the graceful stop lost its HAR to the ForceStop: report %+v, warnings %v", session.HARFinalization, session.Warnings)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("temp archive left on disk: %v", err)
	}
}

// A ForceStop whose graceful stop never finishes (a shutdown) still removes
// the temp archive once its bounded wait ends.
func TestForceStopRemovesTheHARWhenTheOwningStopNeverFinishes(t *testing.T) {
	withHARBudgets(t, 5*time.Second, time.Second, time.Second)
	old := forceStopHARWait
	forceStopHARWait = 100 * time.Millisecond
	t.Cleanup(func() { forceStopHARWait = old })
	exporting := make(chan struct{})
	release := make(chan struct{})
	r, path := stoppableHARRecorder(t, func(path string) error {
		if err := os.WriteFile(path, []byte(finishedHAR), 0o600); err != nil {
			return err
		}
		close(exporting)
		<-release
		return nil
	})
	stopped := make(chan struct{})
	go func() { _, _ = r.Stop(); close(stopped) }()
	t.Cleanup(func() { close(release); <-stopped })
	<-exporting
	r.ForceStop()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("temp archive kept after ForceStop's wait: %v", err)
	}
}

// A complete archive that runs out of processing time or is too large is not
// reported as an invalid archive.
func TestApplyHARArchiveSeparatesProcessingFailures(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{errHARProcessingBudget, HARFinalizationProcessingBudget},
		{fmt.Errorf("hydrated HAR exceeds maximum of 1 bytes: %w", errArtifactTooLarge), HARFinalizationSizeLimit},
		{errors.New("attached HAR archive end record is missing"), HARFinalizationArchiveInvalid},
	} {
		r, _ := harFinalizationRecorder(t)
		r.session.HARFinalization = &HARFinalization{Phase: HARFinalizationProcessing}
		r.mu.Lock()
		r.applyHARArchiveLocked(harArchiveOutcome{attempted: true, err: tc.err})
		r.mu.Unlock()
		if got := r.session.HARFinalization.Reason; got != tc.want {
			t.Fatalf("%v -> %q, want %q", tc.err, got, tc.want)
		}
	}
}

// A ForceStop whose wait ends while the owning stop is still reading the
// exported archive leaves it to that stop, which applies the HAR and then
// removes the file.
func TestForceStopDuringHARProcessingLeavesTheArchiveToTheOwningStop(t *testing.T) {
	withHARBudgets(t, 2*time.Second, 200*time.Millisecond, 5*time.Second)
	old := forceStopHARWait
	forceStopHARWait = 50 * time.Millisecond
	t.Cleanup(func() { forceStopHARWait = old })
	r, path := stoppableHARRecorder(t, func(path string) error { return os.WriteFile(path, []byte(finishedHAR), 0o600) })
	loading, release := make(chan struct{}), make(chan struct{})
	oldLoader := harArchiveLoader
	harArchiveLoader = func(path string, limit int64, filters HARFilters, deadline time.Time) (harArtifact, error) {
		close(loading)
		<-release
		return oldLoader(path, limit, filters, deadline)
	}
	t.Cleanup(func() { harArchiveLoader = oldLoader })
	stopped := make(chan *Session, 1)
	go func() { session, _ := r.Stop(); stopped <- session }()
	<-loading
	r.ForceStop() // returns after its bounded wait, Stop still reading
	if _, err := os.Stat(path); err != nil {
		close(release)
		t.Fatalf("ForceStop removed an archive the owning stop was reading: %v", err)
	}
	close(release)
	session := <-stopped
	if len(session.HAR) == 0 || session.HARFinalization.Phase != HARFinalizationComplete {
		t.Fatalf("HAR bytes %d, report %+v", len(session.HAR), session.HARFinalization)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("owning stop left the archive: %v", err)
	}
}

// A close that fails after the archive was fully written still yields the
// HAR: the writer returned, and the archive check decides. The warning keeps
// the export's own error without the internal ownership marker.
func TestFinalizeHARExportReadsACompleteArchiveAfterAFailedClose(t *testing.T) {
	withHARBudgets(t, time.Second, 100*time.Millisecond, time.Second)
	r, path := harFinalizationRecorder(t)
	inner := &harExportContext{ownedResult: errors.Join(errors.New("target closed"), playwright.ErrHARCloseOwnershipUnproven)}
	inner.export = func() error { return os.WriteFile(path, []byte(finishedHAR), 0o600) }
	r.finalizeHARExport(ownedHARExportContext{inner})
	finishRecorderHAR(r)
	if len(r.session.HAR) == 0 || r.session.HARFinalization.Phase != HARFinalizationComplete {
		t.Fatalf("HAR bytes %d, report %+v", len(r.session.HAR), r.session.HARFinalization)
	}
	warnings := strings.Join(r.session.Warnings, "\n")
	if !strings.Contains(warnings, "target closed") || strings.Contains(warnings, playwright.ErrHARCloseOwnershipUnproven.Error()) {
		t.Fatalf("warnings = %q", warnings)
	}
}

// A ForceStop that took the browser before the export reports the force, not
// a browser the user closed.
func TestApplyHARArchiveReportsAForceStopBeforeTheExport(t *testing.T) {
	r, _ := harFinalizationRecorder(t)
	r.forceStopped = true
	finishRecorderHAR(r)
	if report := r.session.HARFinalization; report == nil || report.Reason != HARFinalizationForceStopped {
		t.Fatalf("report = %+v", report)
	}
	if strings.Contains(strings.Join(r.session.Warnings, "\n"), "browser closed") {
		t.Fatalf("warnings blame a browser close: %v", r.session.Warnings)
	}
}

// A ForceStop that already took the browser when the export would start
// reports the force, not a closed browser or a failed export.
func TestFinalizeHARExportAfterAForceStopReportsTheForce(t *testing.T) {
	withHARBudgets(t, time.Second, 100*time.Millisecond, time.Second)
	r, _ := harFinalizationRecorder(t)
	r.forceStopped = true
	context := ownedHARExportContext{&harExportContext{closed: true, ownedResult: playwright.ErrHARCloseOwnershipUnproven, export: func() error { return nil }}}
	r.finalizeHARExport(context)
	finishRecorderHAR(r)
	if report := r.session.HARFinalization; report.Reason != HARFinalizationForceStopped || context.ownedCalls.Load() != 0 {
		t.Fatalf("report = %+v, owned closes %d", report, context.ownedCalls.Load())
	}
}
