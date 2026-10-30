package api

import "github.com/jasonczech/apexload/pkg/internal/importreview"

// handlers_import_har — split from handlers_scripts.go (same package, behavior-preserving).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/har"
	"github.com/jasonczech/apexload/pkg/convert/paramize"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/convert/sample"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func (s *Server) handleImportHAR(w http.ResponseWriter, r *http.Request) {
	importReq, ok := readHARImportRequest(w, r)
	if !ok {
		return
	}
	runHARImportWithinWindow(w, r, func(w http.ResponseWriter, r *http.Request) {
		s.importHAR(w, r, importReq)
	})
}

// importHAR converts an uploaded HAR, and its comparison HAR when one was
// sent, and writes the import response. It runs under
// runHARImportWithinWindow.
func (s *Server) importHAR(w http.ResponseWriter, r *http.Request, importReq *harImportRequest) {
	profile, err := s.projectRecordingProfile(normalizeProjectID(importReq.ProjectID), importReq.RecordingProfileID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	importReq.Opts.Profile = profile
	importReq.Opts.RecordingProfileID = strings.TrimSpace(importReq.RecordingProfileID)
	importReq.Opts.EntryPoint = sample.EntryPointImportHAR
	// The comparison HAR converts while the baseline does; it reads its own
	// bytes and a copy of the options, whose shared pointers (Profile and the
	// option flags) conversion only reads.
	comparison := startHARComparison(r.Context(), importReq.SecondaryData, importReq.Opts)
	// Every return cancels the comparison. Only the success path waits for it
	// (below): an error response must not wait for a conversion nobody will
	// use. A canceled comparison ends at its next stage and drops its result.
	defer comparison.release()
	importReq.SecondaryData = nil

	convertStarted := time.Now()
	result, err := har.Convert(importReq.Data, importReq.Opts)
	baselineConversion := time.Since(convertStarted)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "conversion failed: "+err.Error())
		return
	}
	importReq.noteSalvage(result)
	protectedRequirements := result.ProtectedRequirements.Take()
	defer clearCredentialRequirementValues(protectedRequirements)
	if !harImportStageOK(w, r) {
		return
	}
	if comparison != nil {
		finished := comparison.wait(r.Context(), baselineConversion)
		if !harImportStageOK(w, r) {
			return
		}
		if finished {
			comparison.attach(r.Context(), result)
		} else {
			// The comparison is optional, so running out of its share of the
			// window costs only the comparison: the baseline is returned.
			recordUncomparedHARPair(result, "the comparison HAR did not finish converting within its share of the processing window (the controller may be converting other comparison HARs)")
		}
	} else {
		dropUnperformedHARPairMode(result)
	}
	if result.Script != nil {
		// Scope the HAR draft even when a native Salesforce rewrite replaces it
		// below: the raw conversion result still travels in the response.
		result.Script.ProjectID = normalizeProjectID(importReq.ProjectID)
	}

	salesforceNative := salesforceNativeRequested(r)
	agentforceTransform := agentforceTransformRequested(r)
	disableSalesforce := false
	if result.EffectiveSettings != nil {
		salesforceNative = result.EffectiveSettings.SalesforceNative
		agentforceTransform = result.EffectiveSettings.AgentforceTransform
		disableSalesforce = result.EffectiveSettings.DisableSalesforce
	}
	// A Salesforce HAR may ship a native rewrite instead of the raw HAR draft.
	// Everything downstream of that choice — which script, which source profile,
	// which sections — is the same for both outcomes.
	var salesforceAugmentation salesforceHARAugmentation
	if !harImportStageOK(w, r) {
		return
	}
	if !disableSalesforce {
		salesforceAugmentation = importreview.AugmentHARImportWithSalesforceProfile(result, importReq.Data, importReq.ProjectID, importReq.Opts.ScriptName, "har", salesforceNative, agentforceTransform, true, profile)
	}
	script := result.Script
	source := "har"
	sections := harImportReviewSections(result)
	correlations := any(result.Correlations)
	summary := harImportSummary(result)
	preApplied := sampleCorrelationsToPreApplied(result.Correlations)
	if salesforceAugmentation.Detected {
		script = salesforceAugmentation.Script
		source = salesforceAugmentation.Source
		sections = salesforceAugmentation.ReviewSections
		correlations = salesforceAugmentation.Correlations
		summary = salesforceAugmentation.Summary
		if script != result.Script {
			preApplied = nil
		}
	}

	var preparedCredentialReview []importreview.SecretBindingRequirement
	var credentialReviewPrepared bool
	draft := importDraft{
		Source:           source,
		SourceScriptName: importReq.Opts.ScriptName,
		ProjectID:        importReq.ProjectID,
		Script:           script,
		SourceReview:     result.SourceReview,
		Unsupported:      result.Unsupported,
		Sections:         sections,
		Warnings:         result.Warnings,
		Accounting:       result.Accounting(),
		BeforeFinalize: func(s *dsl.Script, warnings *[]string, unsupported *diag.Diagnostics) error {
			applyClientGeneratedUUIDs(s, importReq.Data, result)
			applyClientTimestamps(s, importReq.Data, result)
			*warnings = result.Warnings
			*unsupported = result.Unsupported
			hasSelectedIDs := len(paramize.SelectedIDsForScript(importReq.Opts.ParameterizationSelections, 0)) > 0
			if result.EffectiveSettings != nil && result.EffectiveSettings.Parameterize && !hasSelectedIDs {
				result.Warnings = append(result.Warnings, importreview.ApplyAutoParameterizationToScript(s)...)
				*warnings = result.Warnings
			}
			if hasSelectedIDs {
				if _, err := importreview.ApplyParameterizationSelectionsToScript(s, 0, importReq.Opts.ParameterizationSelections); err != nil {
					return err
				}
			}
			if len(importReq.SecretBindings) > 0 {
				var err error
				_, preparedCredentialReview, credentialReviewPrepared, err = importreview.ApplySecretBindingsToImportDraft(s, 0, importReq.SecretBindings, protectedRequirements)
				if err != nil {
					return err
				}
			}
			sample.AttachEffectiveSettings(s, result.EffectiveSettings)
			return nil
		},
	}
	augmented, err := draft.AugmentWithAgentforceTransformChecked(agentforceTransform)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	result.Warnings = augmented.Warnings
	if !harImportStageOK(w, r) {
		return
	}
	parameterization := buildImportParameterization(script, preApplied)
	if result.Diff != nil && result.Diff.Compared {
		// Judged on the final script, after every correlation and replacement.
		importreview.ResolveHARPairUnhandledInputs(result.Diff, script, parameterization)
		if warning := importreview.HARPairUnhandledInputsWarning(result.Diff); warning != "" {
			result.Warnings = append(result.Warnings, warning)
		}
		augmented.Sections = importreview.AppendHARPairUnhandledInputs(augmented.Sections, result.Diff)
	}

	s.writeScriptImportResponseWithSourceSecrets(w, r, http.StatusOK, scriptImportResponseOptions{
		Result:                   result,
		Script:                   script,
		Warnings:                 result.Warnings,
		Stats:                    result.Stats,
		Correlations:             correlations,
		Diagnostics:              result.Unsupported,
		UnsupportedElements:      result.Unsupported.Strings(),
		ReviewSections:           augmented.Sections,
		SourceReview:             result.SourceReview,
		Summary:                  summary,
		Parameterization:         parameterization,
		Preflight:                augmented.Preflight,
		EffectiveSettings:        result.EffectiveSettings,
		ProtectedRequirements:    protectedRequirements,
		PreparedCredentialReview: preparedCredentialReview,
		CredentialReviewPrepared: credentialReviewPrepared,
	})
}

// harImportProcessingTimeout bounds the work a HAR or Salesforce HAR import
// does after its upload has been read. The server's 60 s WriteTimeout is
// shorter than a large capture pair can take on a slow machine, and a response
// written after it is silently lost, so the import extends its write deadline
// to this window (plus harImportErrorWriteGrace, so the timeout response can
// still be written). runHARImportWithinWindow answers 503 when the window
// ends, and the import stops at its next stage.
var harImportProcessingTimeout = 5 * time.Minute

const harImportErrorWriteGrace = 10 * time.Second

// harImportProcessingRequest starts the processing window for an import whose
// upload has been read. A shorter deadline the caller already set is kept.
func harImportProcessingRequest(w http.ResponseWriter, r *http.Request) (*http.Request, context.CancelFunc) {
	deadline := time.Now().Add(harImportProcessingTimeout)
	if callerDeadline, ok := r.Context().Deadline(); ok && callerDeadline.Before(deadline) {
		deadline = callerDeadline
	}
	_ = http.NewResponseController(w).SetWriteDeadline(deadline.Add(harImportErrorWriteGrace))
	ctx, cancel := context.WithDeadline(r.Context(), deadline)
	return r.WithContext(ctx), cancel
}

// runHARImportWithinWindow runs a HAR or Salesforce HAR import with its
// processing window enforced. The import runs on its own goroutine and writes
// through a harImportResponseGuard. When the window ends before the import
// has written its response, the client gets 503 at once; when the client goes
// away, the request ends without a response. Either way the import's late
// writes are dropped and it stops at its next stage (harImportStageOK): a
// conversion cannot be interrupted part-way, so finishing the stage it is in
// is the most an import does after its handler has returned.
func runHARImportWithinWindow(w http.ResponseWriter, r *http.Request, run func(http.ResponseWriter, *http.Request)) {
	r, cancel := harImportProcessingRequest(w, r)
	defer cancel()
	// The guard starts from the request's headers so code inside the import
	// that reads them (errorReferenceFor and X-Request-ID) sees what the
	// handler would have seen.
	guard := &harImportResponseGuard{w: w, header: w.Header().Clone()}
	finished := make(chan struct{})
	panicked := make(chan any, 1)
	go func() {
		defer close(finished)
		defer func() {
			if recovered := recover(); recovered != nil {
				panicked <- recovered
			}
		}()
		run(guard, r)
	}()
	select {
	case <-finished:
		guard.finish()
		select {
		case recovered := <-panicked:
			// Re-raised on the handler goroutine, where net/http recovers it
			// as it would a panic in the handler itself.
			panic(recovered)
		default:
		}
	case <-r.Context().Done():
		guard.abandon(errors.Is(r.Context().Err(), context.DeadlineExceeded))
		go func() {
			<-finished
			select {
			case recovered := <-panicked:
				slog.Error("HAR import panicked after its request ended", "panic", fmt.Sprint(recovered))
			default:
			}
		}()
	}
}

// harImportResponseGuard is the import goroutine's ResponseWriter. It holds
// the status until the body is written, so the handler can still answer 503
// in its place, and drops every write once the handler has finished.
type harImportResponseGuard struct {
	w      http.ResponseWriter
	header http.Header

	mu     sync.Mutex
	status int
	sent   bool
	closed bool
}

func (g *harImportResponseGuard) Header() http.Header { return g.header }

func (g *harImportResponseGuard) WriteHeader(status int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.status == 0 {
		g.status = status
	}
}

func (g *harImportResponseGuard) Write(data []byte) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return 0, http.ErrHandlerTimeout
	}
	g.sendHeaderLocked()
	return g.w.Write(data)
}

func (g *harImportResponseGuard) sendHeaderLocked() {
	if g.sent {
		return
	}
	g.sent = true
	header := g.w.Header()
	for name, values := range g.header {
		header[name] = values
	}
	if g.status == 0 {
		g.status = http.StatusOK
	}
	g.w.WriteHeader(g.status)
}

// finish passes on a status the import set without writing a body, and closes
// the guard.
func (g *harImportResponseGuard) finish() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.closed && !g.sent && g.status != 0 {
		g.sendHeaderLocked()
	}
	g.closed = true
}

// abandon closes the guard for an import that is still running. One that ran
// out of its window gets 503 unless its response is already under way.
func (g *harImportResponseGuard) abandon(timedOut bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.closed = true
	if g.sent || !timedOut {
		return
	}
	g.sent = true
	writeHARImportTimeout(g.w)
}

func writeHARImportTimeout(w http.ResponseWriter) {
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{
		"error": fmt.Sprintf("the HAR import did not finish within its %s processing window; import a shorter capture or split the recording", harImportProcessingTimeout),
	})
}

// harImportStageOK reports whether an import should start its next expensive
// stage. A client that canceled or disconnected gets nothing more: the
// request stops so an abandoned import does not keep burning CPU. One that
// ran out of the processing window gets 503 saying so.
func harImportStageOK(w http.ResponseWriter, r *http.Request) bool {
	err := r.Context().Err()
	if err == nil {
		return true
	}
	if errors.Is(err, context.DeadlineExceeded) {
		writeHARImportTimeout(w)
	}
	return false
}

// harComparisonJob converts a comparison HAR in the background while the
// baseline converts. Its fields are written by the goroutine and read only
// after done is closed.
type harComparisonJob struct {
	done     chan struct{}
	cancel   context.CancelFunc
	prepared *har.ComparisonRecording
	salvage  *har.TruncationSalvage
	err      error
}

// startHARComparison begins preparing the comparison HAR, or returns nil when
// the import has none. opts is the caller's copy; conversion only reads the
// pointers it shares with the baseline (Profile and the option flags). The
// job ends with ctx or stop, whichever comes first.
func startHARComparison(ctx context.Context, data []byte, opts har.ConvertOptions) *harComparisonJob {
	if len(data) == 0 {
		return nil
	}
	ctx, cancel := context.WithCancel(ctx)
	job := &harComparisonJob{done: make(chan struct{}), cancel: cancel}
	// The job returns its slot to the semaphore it took it from.
	slots := harComparisonSlots
	go func() {
		defer close(job.done)
		// A comparison outlives a failed or abandoned import until its
		// conversion returns, so the slots bound how many can run at once: a
		// client that resends right after an error queues behind the slot
		// instead of piling up conversions.
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		case <-ctx.Done():
			job.err = ctx.Err()
			return
		}
		if err := ctx.Err(); err != nil {
			job.err = err
			return
		}
		defer func() {
			// The comparison is optional: a converter panic costs only the
			// comparison, never the controller.
			if recovered := recover(); recovered != nil {
				slog.Error("HAR comparison conversion panicked", "panic", fmt.Sprint(recovered))
				job.prepared, job.err = nil, errors.New("comparison HAR: conversion failed unexpectedly")
			}
		}()
		job.prepared, job.salvage, job.err = harComparisonPreparer(ctx, data, opts)
	}()
	return job
}

// harComparisonSlots bounds the comparison HAR conversions that run at once,
// across all imports. A comparison of up to harImportMaxFileBytes takes
// several times its size in memory to convert.
var harComparisonSlots = make(chan struct{}, 2)

// harComparisonPreparer is prepareHARComparison; tests replace it to hold a
// comparison mid-conversion.
var harComparisonPreparer = prepareHARComparison

// prepareHARComparison unwraps, salvages (under the same salvage_truncated
// opt-in as the baseline) and converts a comparison HAR.
func prepareHARComparison(ctx context.Context, data []byte, opts har.ConvertOptions) (*har.ComparisonRecording, *har.TruncationSalvage, error) {
	data, err := unwrapHARArchive(data)
	if err != nil {
		return nil, nil, fmt.Errorf("comparison HAR upload: %w", err)
	}
	var salvage *har.TruncationSalvage
	if salvaged, report, truncated := har.SalvageTruncated(data); truncated {
		if opts.SalvageTruncated == nil || !*opts.SalvageTruncated {
			return nil, nil, errors.New("comparison HAR: " + report.Hint())
		}
		data, salvage = salvaged, &report
	}
	prepared, err := har.PrepareComparisonContext(ctx, data, opts)
	if err != nil {
		return nil, salvage, fmt.Errorf("comparison HAR: %w", err)
	}
	return prepared, salvage, nil
}

// release cancels the comparison without waiting for it. One still waiting
// for a slot ends at once. A conversion already under way cannot be
// interrupted; the comparison checks for cancellation between its stages and
// then ends on its own, still holding its slot until it does.
func (job *harComparisonJob) release() {
	if job == nil {
		return
	}
	job.cancel()
}

// An import waiting for its comparison keeps part of its remaining time for
// the stages after it (the diff, the Salesforce review, finalizing and the
// response), which grow with the baseline: the larger of
// 1/harComparisonResponseShare of that time and harComparisonReservePerConvert
// times the baseline's conversion, but never more than half of it. This is a
// best effort, not a bound on those stages.
const (
	harComparisonResponseShare     = 4
	harComparisonReservePerConvert = 10
)

// wait blocks until the comparison is prepared, ctx ends, or only the time
// the import keeps for its later stages is left (see
// harComparisonResponseShare). baselineConversion is how long the baseline
// took to convert. It reports whether the comparison finished; when it did
// not, the import continues without it, so a comparison queued behind
// harComparisonSlots, or one too large to convert in time, costs the
// comparison rather than the import. A comparison given up on is canceled at
// once, so one still queued never takes a slot that frees later.
func (job *harComparisonJob) wait(ctx context.Context, baselineConversion time.Duration) bool {
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		reserve := max(remaining/harComparisonResponseShare, harComparisonReservePerConvert*baselineConversion)
		reserve = min(reserve, remaining/2)
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, remaining-reserve)
		defer cancel()
	}
	select {
	case <-job.done:
		return true
	case <-ctx.Done():
		select {
		case <-job.done:
			return true
		default:
			job.release()
			return false
		}
	}
}

// attach classifies the baseline against the prepared comparison and records
// the outcome on result. A comparison that could not be prepared or diffed
// does not fail the import: the baseline is returned with a warning and a
// har_diff that says why nothing was compared. The diff stops with ctx.
func (job *harComparisonJob) attach(ctx context.Context, result *har.ConvertResult) {
	err := job.err
	var diff *har.DiffReport
	if err == nil {
		diff, err = har.DiffPreparedContext(ctx, result, job.prepared)
	}
	job.prepared = nil
	if err != nil {
		recordUncomparedHARPair(result, "HAR pair diff failed: "+err.Error())
		return
	}
	if job.salvage != nil {
		// Recorded like the baseline's salvage (noteSalvage), under its own code.
		warning := "Comparison HAR: " + job.salvage.Warning()
		diff.Warnings = append(diff.Warnings, warning)
		result.Warnings = append(result.Warnings, warning)
		result.Unsupported = append(result.Unsupported, diag.Diagnostic{Code: "har_comparison_truncated_salvaged", Element: "HAR truncation", Location: "comparison HAR file", Detail: warning, Severity: diag.Warning})
	}
	result.Diff = diff
}

// recordUncomparedHARPair records on result that its comparison HAR was not
// compared, and why: the baseline is imported alone, with a warning and a
// har_diff that carries the reason.
func recordUncomparedHARPair(result *har.ConvertResult, reason string) {
	dropUnperformedHARPairMode(result)
	result.Warnings = append(result.Warnings, "The comparison HAR was not compared, so no correlation is labeled dynamic or static: "+reason)
	result.Diff = &har.DiffReport{
		ArtifactMode:         result.ArtifactMode,
		Reason:               reason,
		BaselineCorrelations: len(result.Correlations),
	}
}

// dropUnperformedHARPairMode reports a conversion as a single design HAR when
// no comparison was performed, whatever artifact mode the upload implied.
func dropUnperformedHARPairMode(result *har.ConvertResult) {
	if result == nil || result.ArtifactMode != har.ArtifactModeDesignHARPair {
		return
	}
	result.ArtifactMode = har.ArtifactModeDesignHAR
	if result.Governance != nil {
		result.Governance.ArtifactMode = har.ArtifactModeDesignHAR
	}
}

func salesforceNativeRequested(r *http.Request) bool {
	if r == nil {
		return true
	}
	return parseBoolDefault(r.URL.Query().Get("salesforce_native"), true)
}

var harImportSummary = importreview.HarImportSummary

// harGovernanceItems states the governance facts as sentences. The raw enum
// values stay available in the summary's governance block.
var harGovernanceItems = importreview.HarGovernanceItems

// harImportReviewSections is the HAR importer's own sections. Converter
// diagnostics are not included: the shared augmentation pass appends those for
// every format, and adding them here too would show each one twice.
var harImportReviewSections = importreview.HarImportReviewSections

// harImportUploadPolicy is what an import route accepts on top of the
// baseline capture.
type harImportUploadPolicy struct {
	// body is the route's whole envelope.
	body int64
	// keepComparison keeps a comparison HAR for the pair diff. A route that
	// converts one capture drains a comparison file without keeping it and
	// only notes that one was sent.
	keepComparison bool
}

var (
	harPairImportUpload    = harImportUploadPolicy{body: harImportMaxBodyBytes, keepComparison: true}
	salesforceImportUpload = harImportUploadPolicy{body: salesforceImportMaxBodyBytes}
)

// readHARImportRequest reads a /scripts/import/har upload: a baseline capture
// and, optionally, a comparison capture.
func readHARImportRequest(w http.ResponseWriter, r *http.Request) (*harImportRequest, bool) {
	req, _, ok := readHARImportUpload(w, r, harPairImportUpload)
	return req, ok
}

// readSalesforceImportRequest reads a /scripts/import/salesforce upload.
// comparisonSent reports a comparison HAR the route does not use.
func readSalesforceImportRequest(w http.ResponseWriter, r *http.Request) (req *harImportRequest, comparisonSent bool, ok bool) {
	return readHARImportUpload(w, r, salesforceImportUpload)
}

func readHARImportUpload(w http.ResponseWriter, r *http.Request, policy harImportUploadPolicy) (*harImportRequest, bool, bool) {
	req, comparisonSent, ok := decodeHARImportRequest(w, r, policy)
	if !ok {
		return nil, false, false
	}
	// A capture may arrive zipped; everything after this sees the HAR itself.
	// The comparison HAR is unwrapped and checked with its conversion
	// (prepareHARComparison), where a bad one costs only the comparison.
	if !unwrapHARImportArchives(w, req) {
		return nil, false, false
	}
	// A truncated capture is imported only on explicit request; otherwise the
	// error says how many complete entries it holds and how to keep them.
	if salvaged, report, truncated := har.SalvageTruncated(req.Data); truncated {
		if req.Opts.SalvageTruncated == nil || !*req.Opts.SalvageTruncated {
			writeError(w, http.StatusUnprocessableEntity, "conversion failed: "+report.Hint())
			return nil, false, false
		}
		req.Data = salvaged
		req.Salvage = &report
	}
	return req, comparisonSent, true
}

func decodeHARImportRequest(w http.ResponseWriter, r *http.Request, policy harImportUploadPolicy) (*harImportRequest, bool, bool) {
	limits := importUploadLimits{Body: policy.body, File: harImportMaxFileBytes}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		// Only the multipart reader streams its parts; a JSON or raw upload is
		// read whole, so it gets one capture's envelope (as the route catalog
		// sets for the body limit before authentication reads it).
		limits.Body = min(limits.Body, harSingleCaptureMaxBodyBytes)
	}
	if policy.keepComparison {
		limits.Files = harComparisonFileFields
	} else {
		limits.Discard = harComparisonFileFields
	}
	upload, files, ok := decodeImportUploadWithLimits(w, r, limits)
	if !ok {
		return nil, false, false
	}
	if upload.Transport == importTransportJSON {
		var req struct {
			Name                       string                                `json:"name,omitempty"`
			ProjectID                  string                                `json:"project_id,omitempty"`
			Content                    string                                `json:"content"`
			SecondaryContent           string                                `json:"secondary_content,omitempty"`
			ComparisonContent          string                                `json:"comparison_content,omitempty"`
			ContentB                   string                                `json:"content_b,omitempty"`
			Options                    har.ConvertOptions                    `json:"options,omitempty"`
			RecordingProfileID         string                                `json:"recording_profile_id,omitempty"`
			ProfileID                  string                                `json:"profile_id,omitempty"`
			ConversionPolicy           string                                `json:"conversion_policy,omitempty"`
			CorrelationMode            string                                `json:"correlation_mode,omitempty"`
			ResourceMode               string                                `json:"resource_mode,omitempty"`
			PageTitleChecks            *bool                                 `json:"page_title_checks,omitempty"`
			IncludeAssertions          *bool                                 `json:"include_assertions,omitempty"`
			InferExtractors            *bool                                 `json:"infer_extractors,omitempty"`
			IncludeStaticAssets        *bool                                 `json:"include_static_assets,omitempty"`
			RedactSensitive            *bool                                 `json:"redact_sensitive,omitempty"`
			SalvageTruncated           *bool                                 `json:"salvage_truncated,omitempty"`
			AsyncScan                  *bool                                 `json:"async_scan,omitempty"`
			HTTPVersionMode            string                                `json:"http_version_mode,omitempty"`
			DisableSalesforce          *bool                                 `json:"disable_salesforce,omitempty"`
			SalesforceNative           *bool                                 `json:"salesforce_native,omitempty"`
			AgentforceTransform        *bool                                 `json:"agentforce_transform,omitempty"`
			Parameterize               *bool                                 `json:"parameterize,omitempty"`
			ParameterizationSelections []paramize.Selection                  `json:"parameterization_selections,omitempty"`
			SecretBindings             []importreview.SecretBindingSelection `json:"secret_bindings,omitempty"`
		}
		if err := json.Unmarshal(upload.Data, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return nil, false, false
		}
		profileID := strings.TrimSpace(firstNonEmpty(
			req.RecordingProfileID,
			req.ProfileID,
			req.Options.RecordingProfileID,
			upload.Value("recording_profile_id"),
			upload.Value("profile_id"),
		))
		req.Options.RecordingProfileID = profileID
		req.Options.ConversionPolicy = strings.TrimSpace(firstNonEmpty(req.ConversionPolicy, req.Options.ConversionPolicy, upload.Value("conversion_policy")))
		req.Options.CorrelationMode = strings.TrimSpace(firstNonEmpty(req.CorrelationMode, req.Options.CorrelationMode, upload.Value("correlation_mode")))
		req.Options.ResourceMode = strings.TrimSpace(firstNonEmpty(req.ResourceMode, req.Options.ResourceMode, upload.Value("resource_mode")))
		req.Options.HTTPVersionMode = strings.TrimSpace(firstNonEmpty(req.HTTPVersionMode, req.Options.HTTPVersionMode, upload.Value("http_version_mode")))
		req.Options.PageTitleChecks = firstNonNilBool(req.PageTitleChecks, req.Options.PageTitleChecks, parseOptionalBool(upload.Value("page_title_checks")))
		req.Options.IncludeAssertions = firstNonNilBool(req.IncludeAssertions, req.Options.IncludeAssertions, parseOptionalBool(upload.Value("include_assertions")))
		req.Options.InferExtractors = firstNonNilBool(req.InferExtractors, req.Options.InferExtractors, parseOptionalBool(upload.Value("infer_extractors")))
		req.Options.IncludeStaticAssets = firstNonNilBool(req.IncludeStaticAssets, req.Options.IncludeStaticAssets, parseOptionalBool(upload.Value("include_static_assets")))
		req.Options.RedactSensitive = firstNonNilBool(req.RedactSensitive, req.Options.RedactSensitive, parseOptionalBool(upload.Value("redact_sensitive")))
		req.Options.SalvageTruncated = firstNonNilBool(req.SalvageTruncated, req.Options.SalvageTruncated, parseOptionalBool(upload.Value("salvage_truncated")))
		req.Options.AsyncScan = firstNonNilBool(req.AsyncScan, req.Options.AsyncScan, parseOptionalBool(upload.Value("async_scan")))
		req.Options.DisableSalesforce = firstNonNilBool(req.DisableSalesforce, req.Options.DisableSalesforce, parseOptionalBool(upload.Value("disable_salesforce")))
		req.Options.SalesforceNative = firstNonNilBool(req.SalesforceNative, req.Options.SalesforceNative, parseOptionalBool(upload.Value("salesforce_native")))
		req.Options.AgentforceTransform = firstNonNilBool(req.AgentforceTransform, req.Options.AgentforceTransform, parseOptionalBool(upload.Value("agentforce_transform")))
		if param := firstNonNilBool(req.Parameterize, parseOptionalBool(upload.Value("parameterize"))); param != nil {
			req.Options.Parameterize = *param
		}
		if len(paramize.SelectedIDsForScript(req.ParameterizationSelections, 0)) > 0 ||
			(len(paramize.SelectedIDsForScript(req.Options.ParameterizationSelections, 0)) == 0 && len(req.ParameterizationSelections) > 0) {
			req.Options.ParameterizationSelections = req.ParameterizationSelections
		}
		if !validHTTPVersionMode(w, req.Options.HTTPVersionMode) || !validHARImportPolicyOptions(w, req.Options) {
			return nil, false, false
		}
		req.Options.ScriptName = firstNonEmpty(req.Options.ScriptName, req.Name)
		secondary := firstNonEmpty(req.SecondaryContent, req.ComparisonContent, req.ContentB)
		comparisonSent := secondary != ""
		if !policy.keepComparison {
			// The route's envelope fits one capture, which bounds what the
			// unused comparison cost to decode.
			secondary = ""
		}
		// The envelope holds both captures, so each is held to the per-file
		// limit a multipart part gets.
		if int64(len(req.Content)) > harImportMaxFileBytes {
			importUploadTooLarge(w, "the HAR content", harImportMaxFileBytes)
			return nil, false, false
		}
		if int64(len(secondary)) > harImportMaxFileBytes {
			importUploadTooLarge(w, "the comparison HAR content", harImportMaxFileBytes)
			return nil, false, false
		}
		req.Options.ArtifactMode = normalizeHARArtifactMode(req.Options.ArtifactMode, secondary != "")
		return &harImportRequest{
			Data:               []byte(req.Content),
			SecondaryData:      []byte(secondary),
			ProjectID:          upload.jsonProjectID(req.ProjectID),
			Opts:               req.Options,
			RecordingProfileID: profileID,
			SecretBindings:     req.SecretBindings,
		}, comparisonSent, true
	}

	// A comparison HAR is a second file, so it only travels on a multipart
	// upload; the other transports carry the baseline capture alone. A route
	// that does not keep it sees a nil entry when one was sent.
	var secondaryData []byte
	comparisonSent := false
	for _, field := range harComparisonFileFields {
		if data, ok := files[field]; ok {
			secondaryData, comparisonSent = data, true
			break
		}
	}
	httpVersionMode := strings.TrimSpace(upload.Value("http_version_mode"))
	profileID := strings.TrimSpace(firstNonEmpty(upload.Value("recording_profile_id"), upload.Value("profile_id")))
	opts := har.ConvertOptions{
		ScriptName:          firstNonEmpty(upload.Value("name"), upload.Name),
		BaseURL:             upload.Value("base_url"),
		RecordingProfileID:  profileID,
		ConversionPolicy:    strings.TrimSpace(upload.Value("conversion_policy")),
		CorrelationMode:     strings.TrimSpace(upload.Value("correlation_mode")),
		ResourceMode:        strings.TrimSpace(upload.Value("resource_mode")),
		ArtifactMode:        normalizeHARArtifactMode(upload.Value("artifact_mode"), len(secondaryData) > 0),
		RetentionDays:       parseOptionalIntDefault(upload.Value("retention_days"), 0),
		RedactSensitive:     parseOptionalBool(upload.Value("redact_sensitive")),
		IncludeAssertions:   parseOptionalBool(upload.Value("include_assertions")),
		InferExtractors:     parseOptionalBool(upload.Value("infer_extractors")),
		IncludeStaticAssets: parseOptionalBool(upload.Value("include_static_assets")),
		PageTitleChecks:     parseOptionalBool(upload.Value("page_title_checks")),
		SalvageTruncated:    parseOptionalBool(upload.Value("salvage_truncated")),
		AsyncScan:           parseOptionalBool(upload.Value("async_scan")),
		DisableSalesforce:   parseOptionalBool(upload.Value("disable_salesforce")),
		SalesforceNative:    parseOptionalBool(upload.Value("salesforce_native")),
		AgentforceTransform: parseOptionalBool(upload.Value("agentforce_transform")),
		Parameterize:        parseBoolDefault(upload.Value("parameterize"), false),
		HTTPVersionMode:     httpVersionMode,
	}
	if !validHTTPVersionMode(w, httpVersionMode) || !validHARImportPolicyOptions(w, opts) {
		return nil, false, false
	}
	if rawSelections := strings.TrimSpace(upload.Value("parameterization_selections")); rawSelections != "" {
		if err := json.Unmarshal([]byte(rawSelections), &opts.ParameterizationSelections); err != nil {
			writeError(w, http.StatusBadRequest, "invalid parameterization_selections: "+err.Error())
			return nil, false, false
		}
	}
	var secretBindings []importreview.SecretBindingSelection
	if rawBindings := strings.TrimSpace(upload.Value("secret_bindings")); rawBindings != "" {
		if err := json.Unmarshal([]byte(rawBindings), &secretBindings); err != nil {
			writeError(w, http.StatusBadRequest, "invalid secret_bindings: "+err.Error())
			return nil, false, false
		}
	}
	return &harImportRequest{
		Data:               upload.Data,
		SecondaryData:      secondaryData,
		ProjectID:          upload.ProjectID,
		RecordingProfileID: profileID,
		Opts:               opts,
		SecretBindings:     secretBindings,
	}, comparisonSent, true
}

func firstNonNilBool(values ...*bool) *bool {
	for _, value := range values {
		if value != nil {
			copied := *value
			return &copied
		}
	}
	return nil
}

func validHARImportPolicyOptions(w http.ResponseWriter, opts har.ConvertOptions) bool {
	if _, err := sample.ValidateConversionPolicy(opts.ConversionPolicy); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return false
	}
	if _, err := sample.ValidateCorrelationMode(opts.CorrelationMode); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return false
	}
	if _, err := sample.ValidateResourceMode(opts.ResourceMode); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return false
	}
	return true
}

func normalizeHARArtifactMode(value string, hasComparison bool) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	if hasComparison {
		return har.ArtifactModeDesignHARPair
	}
	return har.ArtifactModeDesignHAR
}

// harComparisonFileFields are the multipart fields a comparison HAR may use,
// in priority order.
var harComparisonFileFields = []string{"secondary_file", "comparison_file", "file_b"}

func parseOptionalIntDefault(value string, defaultValue int) int {
	if strings.TrimSpace(value) == "" {
		return defaultValue
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return defaultValue
	}
	return parsed
}

func parseOptionalBool(value string) *bool {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parsed := parseBoolDefault(value, false)
	return &parsed
}

// validHTTPVersionMode writes 400 for an http_version_mode import option that
// is not upgrade, exact or inherit; empty selects upgrade.
func validHTTPVersionMode(w http.ResponseWriter, mode string) bool {
	if _, err := requestimport.NormalizeHTTPVersionMode(mode); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return false
	}
	return true
}
