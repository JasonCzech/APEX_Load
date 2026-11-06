package browser

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	playwright "github.com/mxschmitt/playwright-go"
)

// Stop budgets. Exporting the side-car HAR is not closing a file: Playwright
// finishes pending capture, writes the HAR and every response-body
// attachment, and finalizes the archive. That gets its own budget instead of
// whatever the earlier shutdown steps left, and the steps after it get theirs.
const (
	// browserHARFinalizationBudget bounds the HAR export (the replay runtime
	// uses the same figure for its HAR finalization).
	browserHARFinalizationBudget = 30 * time.Second
	// browserHARWriterJoinGrace is how long Stop waits, after aborting an
	// export that overran its budget, for the writer to actually return.
	browserHARWriterJoinGrace = 5 * time.Second
	// browserCleanupBudget bounds closing the browser and stopping the driver.
	browserCleanupBudget = 5 * time.Second
	// browserHARProcessingBudget bounds reading, validating and filtering the
	// finished archive.
	browserHARProcessingBudget = 30 * time.Second
	// browserForceStopHARWait is how long a ForceStop racing a graceful stop
	// waits for it to finish with the HAR before removing the capture.
	browserForceStopHARWait = 5 * time.Second

	// MaxStopDuration bounds a graceful Stop end to end. A supervisor that
	// force-finishes a stuck stop must wait at least this long, or it stops
	// the driver while the HAR is still being written.
	MaxStopDuration = maxBrowserStopDuration + browserHARFinalizationBudget + browserHARWriterJoinGrace +
		browserCleanupBudget + browserHARProcessingBudget + maxBrowserOperationDuration
)

// Package variables so tests can shorten the budgets.
var (
	harFinalizationBudget = browserHARFinalizationBudget
	harWriterJoinGrace    = browserHARWriterJoinGrace
	harProcessingBudget   = browserHARProcessingBudget
	harArchiveLoader      = loadAndFilterHARUntil
	forceStopHARWait      = browserForceStopHARWait
)

// HAR finalization phases and reasons. They describe the shutdown, never a
// captured value.
const (
	HARFinalizationExporting  = "exporting"
	HARFinalizationProcessing = "processing"
	HARFinalizationComplete   = "complete"
	HARFinalizationFailed     = "unavailable"

	HARFinalizationBudgetExhausted   = "export_budget_exhausted"
	HARFinalizationWriterUnconfirmed = "writer_not_confirmed"
	HARFinalizationExportFailed      = "export_failed"
	HARFinalizationArchiveInvalid    = "archive_invalid"
	HARFinalizationProcessingBudget  = "processing_budget_exhausted"
	HARFinalizationSizeLimit         = "size_limit"
	HARFinalizationBrowserClosed     = "browser_closed_before_export"
	HARFinalizationForceStopped      = "force_stopped"
)

// HARFinalization reports how the side-car HAR was finished: the phase it
// reached, why it is unavailable if it is, and how long each part took.
type HARFinalization struct {
	Phase string `json:"phase"`
	// Reason says why the HAR is unavailable; empty when it is complete.
	Reason string `json:"reason,omitempty"`
	// WriterCompleted is true once the export returned, so no writer can
	// still be changing the archive.
	WriterCompleted bool  `json:"writer_completed"`
	ExportMs        int64 `json:"export_ms,omitempty"`
	ProcessMs       int64 `json:"process_ms,omitempty"`
	ArchiveBytes    int64 `json:"archive_bytes,omitempty"`
	BudgetMs        int64 `json:"budget_ms,omitempty"`
}

// Clone returns a detached copy.
func (f *HARFinalization) Clone() *HARFinalization {
	if f == nil {
		return nil
	}
	clone := *f
	return &clone
}

var (
	errHARExportBudget     = errors.New("HAR export exceeded its finalization budget")
	errHARProcessingBudget = errors.New("HAR processing exceeded recorder shutdown deadline")
	errArtifactTooLarge    = errors.New("size limit exceeded")
)

// harFlushResult is the outcome of one policed HAR export.
type harFlushResult struct {
	exceeded bool
	// writerDone: the export call returned, so nothing is still writing.
	writerDone bool
	timedOut   bool
	// err is the export's own result once it returned, or errHARExportBudget
	// while it is still unconfirmed.
	err error
}

// policeHARFlushJoined runs flush (the export) and polices the archive size
// while it writes. When the deadline passes it aborts the export and then
// waits up to joinGrace for flush to return: an abort is a request, and a
// timeout is never treated as proof that the writer stopped.
func policeHARFlushJoined(path string, limit int64, flush func() error, abort func(), deadline time.Time, joinGrace time.Duration) harFlushResult {
	if flush == nil {
		return harFlushResult{writerDone: true}
	}
	done := make(chan error, 1)
	go func() { done <- flush() }()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	deadlineTimer := time.NewTimer(time.Until(deadline))
	defer deadlineTimer.Stop()
	var result harFlushResult
	aborted := false
	abortOnce := func() {
		if !aborted && abort != nil {
			aborted = true
			go abort()
		}
	}
	check := func() {
		info, err := os.Stat(path)
		if err != nil || info.Size() <= limit {
			return
		}
		result.exceeded = true
		_ = os.Truncate(path, limit)
		abortOnce()
	}
	check()
	for {
		select {
		case err := <-done:
			check()
			result.writerDone = true
			result.err = err
			return result
		case <-ticker.C:
			check()
		case <-deadlineTimer.C:
			check()
			abortOnce()
			result.timedOut = true
			result.err = errHARExportBudget
			join := time.NewTimer(joinGrace)
			defer join.Stop()
			for {
				select {
				case err := <-done:
					check()
					result.writerDone = true
					result.err = err
					return result
				case <-ticker.C:
					check()
				case <-join.C:
					return result
				}
			}
		}
	}
}

// closeHARContext closes a recording context so its HAR export is joined. The
// owned close returns only after the complete export; the ordinary close may
// return early for a context that is already closing.
func closeHARContext(context playwright.BrowserContext) error {
	if owned, ok := context.(playwright.OwnedHARBrowserContext); ok {
		return owned.CloseHAROwned()
	}
	return context.Close()
}

// harExportFailureReason classifies an export that returned an error. The
// owned close joins every failure with ErrHARCloseOwnershipUnproven, so only a
// refusal on a context that was closed before the close began means the
// browser closed first; anything else is a real export failure.
func harExportFailureReason(alreadyClosed bool, err error) string {
	if alreadyClosed && errors.Is(err, playwright.ErrHARCloseOwnershipUnproven) {
		return HARFinalizationBrowserClosed
	}
	return HARFinalizationExportFailed
}

func contextClosed(context playwright.BrowserContext) bool {
	closed, ok := context.(interface{ IsClosed() bool })
	return ok && closed.IsClosed()
}

func harArchiveBytes(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

// finalizeHARExport closes the recording context so Playwright exports the
// side-car HAR, within its own budget, and records how that went. It never
// reads the archive: that waits until the driver has stopped.
func (r *Recorder) finalizeHARExport(context playwright.BrowserContext) {
	budget := harFinalizationBudget
	started := time.Now()
	r.mu.Lock()
	path := r.harCapturePath
	r.session.HARFinalization = &HARFinalization{Phase: HARFinalizationExporting, BudgetMs: budget.Milliseconds()}
	if r.forceStopped {
		// A ForceStop already took the browser: nothing can export now.
		r.session.HARFinalization.Phase, r.session.HARFinalization.Reason = HARFinalizationFailed, HARFinalizationForceStopped
		r.appendWarningLocked("side-car HAR unavailable: the recording was force-finished before the HAR was finalized")
		r.mu.Unlock()
		return
	}
	r.mu.Unlock()
	// Whether the context was already closed decides, below, if a refused
	// owned close means "the browser closed first" or a real export failure.
	alreadyClosed := contextClosed(context)

	result := policeHARFlushJoined(path, maxBrowserHARCaptureBytes, func() error {
		return closeHARContext(context)
	}, r.closeBrowserAsync, started.Add(budget), harWriterJoinGrace)

	r.mu.Lock()
	defer r.mu.Unlock()
	report := r.session.HARFinalization
	report.WriterCompleted = result.writerDone
	report.ExportMs = time.Since(started).Milliseconds()
	report.ArchiveBytes = harArchiveBytes(path)
	switch {
	case result.err != nil && r.forceStopped:
		// A ForceStop stopped the driver under the export; say so rather
		// than blaming a closed browser or a failed export.
		report.Phase, report.Reason = HARFinalizationFailed, HARFinalizationForceStopped
		r.appendWarningLocked("side-car HAR unavailable: the recording was force-finished before the HAR was finalized")
	case result.exceeded:
		r.harLimitExceeded = true
		report.Phase, report.Reason = HARFinalizationFailed, HARFinalizationSizeLimit
		r.appendWarningLocked(fmt.Sprintf("side-car HAR capture exceeded %d bytes during final flush and was discarded", maxBrowserHARCaptureBytes))
	case !result.writerDone:
		report.Phase, report.Reason = HARFinalizationFailed, HARFinalizationWriterUnconfirmed
		r.appendWarningLocked(fmt.Sprintf("side-car HAR export did not finish within %s and its writer did not confirm it stopped; the archive was not read", budget.Round(time.Second)))
	case result.err != nil && harExportFailureReason(alreadyClosed, result.err) == HARFinalizationBrowserClosed:
		// Nothing was exported, so there is no archive to read.
		report.Phase, report.Reason = HARFinalizationFailed, HARFinalizationBrowserClosed
		r.appendWarningLocked("side-car HAR unavailable: the browser closed before the HAR could be exported")
	case result.err != nil:
		// The writer returned, so nothing can still change the archive. The
		// close can fail after the archive was fully written, so the archive
		// check decides; the export's reason applies only if it fails too.
		r.harExportFailure = HARFinalizationExportFailed
		if result.timedOut {
			r.harExportFailure = HARFinalizationBudgetExhausted
			r.appendWarningLocked(fmt.Sprintf("side-car HAR export did not finish within %s and was aborted: %s", budget.Round(time.Second), harExportErrorText(result.err)))
		} else {
			r.appendWarningLocked(fmt.Sprintf("side-car HAR final flush did not complete cleanly: %s", harExportErrorText(result.err)))
		}
		report.Phase = HARFinalizationProcessing
	default:
		// An export that finished in the grace after its budget is complete;
		// the archive check below still decides whether it is usable.
		if result.timedOut {
			r.appendWarningLocked(fmt.Sprintf("side-car HAR export overran its %s budget but finished", budget.Round(time.Second)))
		}
		report.Phase = HARFinalizationProcessing
	}
}

// harArchiveOutcome is the processed archive, ready to apply to the session.
type harArchiveOutcome struct {
	attempted bool
	artifact  harArtifact
	err       error
	processMs int64
}

// processHARArchive reads, validates and filters a HAR whose export finished.
// It runs without the recorder lock; the caller applies the outcome under it.
func (r *Recorder) processHARArchive() harArchiveOutcome {
	r.mu.Lock()
	path := r.harCapturePath
	filters := r.opts.HARFilters
	ready := path != "" && !r.harLimitExceeded && r.session.HARFinalization != nil &&
		r.session.HARFinalization.Phase == HARFinalizationProcessing
	r.mu.Unlock()
	if !ready {
		return harArchiveOutcome{}
	}
	started := time.Now()
	artifact, err := harArchiveLoader(path, maxBrowserHARBytes, filters, started.Add(harProcessingBudget))
	return harArchiveOutcome{attempted: true, artifact: artifact, err: err, processMs: time.Since(started).Milliseconds()}
}

// applyHARArchiveLocked attaches a processed HAR to the session and writes it
// to a caller-requested path. The caller holds r.mu.
func (r *Recorder) applyHARArchiveLocked(outcome harArchiveOutcome) {
	report := r.session.HARFinalization
	if r.harLimitExceeded {
		// Includes a cap tripped mid-recording, which closed the context
		// itself, so no export report exists yet.
		if report == nil {
			r.session.HARFinalization = &HARFinalization{Phase: HARFinalizationFailed, Reason: HARFinalizationSizeLimit}
		} else if report.Reason == "" {
			report.Phase, report.Reason = HARFinalizationFailed, HARFinalizationSizeLimit
		}
		_ = os.Remove(r.harCapturePath)
		return
	}
	if !outcome.attempted {
		switch {
		case report == nil && r.forceStopped:
			// A ForceStop took the browser before this stop could export.
			r.session.HARFinalization = &HARFinalization{Phase: HARFinalizationFailed, Reason: HARFinalizationForceStopped}
			r.appendWarningLocked("side-car HAR unavailable: the recording was force-finished before the HAR was finalized")
		case report == nil:
			// The context was already gone at stop, so nothing exported a HAR.
			r.session.HARFinalization = &HARFinalization{Phase: HARFinalizationFailed, Reason: HARFinalizationBrowserClosed}
			r.appendWarningLocked("side-car HAR unavailable: the browser closed before the HAR could be exported")
		case report.Phase != HARFinalizationFailed:
			// A finished session never reports a phase still in progress.
			report.Phase, report.Reason = HARFinalizationFailed, HARFinalizationExportFailed
		}
		return
	}
	report.ProcessMs = outcome.processMs
	if outcome.err != nil {
		report.Phase, report.Reason = HARFinalizationFailed, harProcessingFailureReason(outcome.err)
		if r.harExportFailure != "" && report.Reason == HARFinalizationArchiveInvalid {
			// An archive the failed export left incomplete: the export is why.
			report.Reason = r.harExportFailure
		}
		r.appendWarningLocked(fmt.Sprintf("side-car HAR unavailable: %v", outcome.err))
		return
	}
	for _, warning := range outcome.artifact.warnings(maxBrowserHARBytes) {
		r.appendWarningLocked(warning)
	}
	data := outcome.artifact.data
	r.session.HAR = data
	report.Phase = HARFinalizationComplete
	if !r.harTemp && r.session.HARPath != r.harCapturePath {
		path := r.session.HARPath
		if writeErr := runOperationUntil(time.Now().Add(maxBrowserOperationDuration), func() error { return os.WriteFile(path, data, 0o600) }, nil); writeErr != nil {
			r.appendWarningLocked(fmt.Sprintf("write side-car HAR: %v", writeErr))
		}
	}
}

// harProcessingFailureReason separates a complete archive that ran out of
// time or was too large from one that is incomplete or invalid.
func harProcessingFailureReason(err error) string {
	switch {
	case errors.Is(err, errHARProcessingBudget):
		return HARFinalizationProcessingBudget
	case errors.Is(err, errArtifactTooLarge):
		return HARFinalizationSizeLimit
	}
	return HARFinalizationArchiveInvalid
}

// harExportErrorText is an export error for an operator: the owned close's
// internal ownership marker is dropped from it.
func harExportErrorText(err error) string {
	var parts []string
	var collect func(error)
	collect = func(err error) {
		if err == nil || err == playwright.ErrHARCloseOwnershipUnproven {
			return
		}
		if joined, ok := err.(interface{ Unwrap() []error }); ok {
			for _, inner := range joined.Unwrap() {
				collect(inner)
			}
			return
		}
		parts = append(parts, err.Error())
	}
	collect(err)
	if len(parts) == 0 {
		return "the export could not be confirmed complete"
	}
	return strings.Join(parts, "; ")
}
