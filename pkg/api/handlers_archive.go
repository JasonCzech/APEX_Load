package api

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

const maxArchiveComparisonRuns = 10

func (s *Server) handleGetRunArchiveManifest(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.runner.GetTest(id); !ok {
		writeError(w, http.StatusNotFound, "test not found")
		return
	}
	manifest, err := s.runner.GetRunArchiveManifest(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, manifest)
}

// handleVerifyRunArchive re-verifies an archived run against the SHA-256 hashes
// recorded per artifact in its manifest, recomputing each artifact's hash on disk
// and reporting match / mismatch (tampered) / missing (corrupt). It always returns
// 200 with the verdict in the body — a tampered or corrupt archive is a reportable
// result, not a server error.
func (s *Server) handleVerifyRunArchive(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.runner.GetTest(id); !ok {
		writeError(w, http.StatusNotFound, "test not found")
		return
	}
	record, ok := s.runner.GetRunArchive(id)
	if !ok {
		writeError(w, http.StatusNotFound, "archive not found")
		return
	}
	if err := ensureArchiveMaterialized(record); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to materialize archive: "+err.Error())
		return
	}
	manifest, err := s.runner.GetRunArchiveManifest(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, verifyRunArchiveIntegrity(record, manifest))
}

func (s *Server) handleDownloadRunArchive(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	test, ok := s.runner.GetTest(id)
	if !ok {
		writeError(w, http.StatusNotFound, "test not found")
		return
	}
	record, ok := s.runner.GetRunArchive(id)
	if !ok {
		writeError(w, http.StatusNotFound, "archive not found")
		return
	}
	if err := ensureArchiveMaterialized(record); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to materialize archive: "+err.Error())
		return
	}
	filename := fmt.Sprintf("%s-%s.zip", sanitizeFilename(test.Name), id)
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
	w.Header().Set("Last-Modified", record.CompletedAt.UTC().Format(http.TimeFormat))
	w.WriteHeader(http.StatusOK)
	if err := zipDirectory(record.ArchivePath, w); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build archive bundle: "+err.Error())
		return
	}
}

func (s *Server) handleDownloadRunArchiveArtifact(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	name := r.PathValue("name")
	test, ok := s.runner.GetTest(id)
	if !ok {
		writeError(w, http.StatusNotFound, "test not found")
		return
	}
	record, ok := s.runner.GetRunArchive(id)
	if !ok {
		writeError(w, http.StatusNotFound, "archive not found")
		return
	}
	if err := ensureArchiveMaterialized(record); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to materialize archive: "+err.Error())
		return
	}
	manifest, err := s.runner.GetRunArchiveManifest(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	name, err = normalizeArchiveArtifactName(name)
	if err != nil {
		writeError(w, http.StatusNotFound, "archive artifact not found")
		return
	}
	artifacts, err := archiveManifestArtifactNames(manifest)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "invalid archive manifest: "+err.Error())
		return
	}
	if _, allowed := artifacts[name]; !allowed {
		writeError(w, http.StatusNotFound, "archive artifact not found")
		return
	}
	fullPath, err := archiveArtifactPath(record.ArchivePath, name)
	if err != nil {
		writeError(w, http.StatusNotFound, "archive artifact not found")
		return
	}
	if _, err := os.Stat(fullPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeError(w, http.StatusNotFound, "archive artifact not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to read archive artifact: "+err.Error())
		return
	}
	filename := fmt.Sprintf("%s-%s-%s", sanitizeFilename(test.Name), id, name)
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
	w.Header().Set("Content-Type", artifactContentType(name))
	http.ServeFile(w, r, fullPath)
}

func (s *Server) handleDownloadRunArchiveBrowserDiagnostics(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	test, ok := s.runner.GetTest(id)
	if !ok {
		writeError(w, http.StatusNotFound, "test not found")
		return
	}
	record, ok := s.runner.GetRunArchive(id)
	if !ok {
		writeError(w, http.StatusNotFound, "archive not found")
		return
	}
	if err := ensureArchiveMaterialized(record); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to materialize archive: "+err.Error())
		return
	}
	bundle, err := buildRunArchiveBrowserDiagnostics(record)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build browser diagnostics bundle: "+err.Error())
		return
	}
	filename := fmt.Sprintf("%s-%s-browser-diagnostics.zip", sanitizeFilename(test.Name), id)
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
	w.Header().Set("Last-Modified", record.CompletedAt.UTC().Format(http.TimeFormat))
	w.WriteHeader(http.StatusOK)
	if err := writeRunArchiveBrowserDiagnosticsZip(bundle, w); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to write browser diagnostics bundle: "+err.Error())
		return
	}
}

func (s *Server) handleGetRunArchiveTransactions(w http.ResponseWriter, r *http.Request) {
	record, ok := s.requireRunArchive(w, r)
	if !ok {
		return
	}
	page, ok := parseArchivePage(w, r)
	if !ok {
		return
	}
	filter, ok := parseTransactionEventFilter(w, r)
	if !ok {
		return
	}
	items, total, err := loadArchiveTransactions(record, filter, page)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read archive transactions: "+err.Error())
		return
	}
	if wantsCSV(r) {
		testID := r.PathValue("id")
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", "attachment; filename=\"apexload-archive-transactions-"+testID+".csv\"")
		if err := writeArchiveTransactionsCSV(items, w); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to write archive transactions csv: "+err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"transactions": items,
		"count":        len(items),
		"total":        total,
		"limit":        page.Limit,
		"offset":       page.Offset,
		"filters": map[string]any{
			"action":          filter.Action,
			"soap_operation":  filter.SOAPOperation,
			"group":           filter.Group,
			"time_from":       filter.TimeFrom,
			"time_to":         filter.TimeTo,
			"status":          filter.Status,
			"min_duration_ms": filter.MinDurationMs,
			"max_duration_ms": filter.MaxDurationMs,
			"min_bytes":       filter.MinBytes,
			"max_bytes":       filter.MaxBytes,
			"error_contains":  filter.ErrorContains,
			"success":         filter.Success,
			"sort_by":         filter.SortBy,
			"sort_dir":        filter.SortDir,
		},
	})
}

func (s *Server) handleGetRunArchiveResources(w http.ResponseWriter, r *http.Request) {
	record, ok := s.requireRunArchive(w, r)
	if !ok {
		return
	}
	page, ok := parseArchivePage(w, r)
	if !ok {
		return
	}
	items, total, err := loadArchiveResourceSamples(record, page)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read archive resources: "+err.Error())
		return
	}
	if wantsCSV(r) {
		testID := r.PathValue("id")
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", "attachment; filename=\"apexload-archive-resources-"+testID+".csv\"")
		if err := writeArchiveResourcesCSV(items, w); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to write archive resources csv: "+err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"resources": items,
		"count":     len(items),
		"total":     total,
		"limit":     page.Limit,
		"offset":    page.Offset,
	})
}

func (s *Server) handleGetRunArchiveSummaries(w http.ResponseWriter, r *http.Request) {
	record, ok := s.requireRunArchive(w, r)
	if !ok {
		return
	}
	page, ok := parseArchivePage(w, r)
	if !ok {
		return
	}
	action := strings.TrimSpace(r.URL.Query().Get("action"))
	groupFilter := parseGroupFilterQuery(r.URL.Query())
	items, total, err := loadArchiveIntervalSummaries(record, page, action, groupFilter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read archive summaries: "+err.Error())
		return
	}
	if wantsCSV(r) {
		testID := r.PathValue("id")
		filename := "apexload-archive-summaries-" + testID
		if action != "" {
			filename += "-" + sanitizeFilename(action)
		}
		for _, groupName := range groupFilter.Names {
			filename += "-" + sanitizeFilename(groupName)
		}
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+".csv\"")
		if err := writeArchiveSummariesCSV(items, w); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to write archive summaries csv: "+err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"summaries": items,
		"count":     len(items),
		"total":     total,
		"limit":     page.Limit,
		"offset":    page.Offset,
		"filters": map[string]any{
			"action": action,
			"groups": groupFilter.Names,
		},
	})
}

func (s *Server) handleGetRunArchiveReport(w http.ResponseWriter, r *http.Request) {
	record, ok := s.requireRunArchive(w, r)
	if !ok {
		return
	}
	report, err := buildRunArchiveReport(record)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build archive report: "+err.Error())
		return
	}
	switch queryFormat(r) {
	case "csv":
		testID := r.PathValue("id")
		s.attachReleaseTrendForProfile(report, r.URL.Query().Get("profile"))
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", "attachment; filename=\"apexload-archive-report-"+testID+".csv\"")
		if err := writeRunArchiveReportCSVWithProfile(report, w, r.URL.Query().Get("profile")); err != nil {
			if strings.Contains(err.Error(), "unknown report profile") {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to write archive report csv: "+err.Error())
		}
		return
	case "pdf":
		profileID := r.URL.Query().Get("profile")
		profile, ok := lookupRunArchiveReportProfile(profileID)
		if !ok {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown report profile %q", profileID))
			return
		}
		if !runArchiveReportProfileSupportsFormat(profile, "pdf") {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("report profile %q does not support pdf export", profile.ID))
			return
		}
		s.attachReleaseTrendForProfile(report, profile.ID)
		// The audit PDF carries the tamper-evidence banner, so re-verify the archive
		// against its recorded artifact hashes before rendering it.
		if profile.ID == "audit" {
			report.Integrity = verifyRunArchiveIntegrity(record, report.Manifest)
		}
		testID := r.PathValue("id")
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", "attachment; filename=\"apexload-archive-report-"+testID+"-"+profile.ID+".pdf\"")
		if err := writeRunArchiveReportPDFWithProfile(report, w, profile.ID); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to write archive report pdf: "+err.Error())
		}
		return
	}
	// The raw JSON report carries the full model, so attach the release-over-release
	// trend unconditionally (still a no-op for runs with no release association).
	s.attachReleaseTrend(report)
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) handleGetRunArchiveAnalysis(w http.ResponseWriter, r *http.Request) {
	currentRecord, ok := s.requireRunArchive(w, r)
	if !ok {
		return
	}
	page, ok := parseArchivePage(w, r)
	if !ok {
		return
	}
	filter, ok := parseTransactionEventFilter(w, r)
	if !ok {
		return
	}
	currentID := r.PathValue("id")
	currentName := currentID
	if test, ok := s.runner.GetTest(currentID); ok {
		currentName = test.Name
	}
	currentRun, err := archiveAnalysisRunFromRecord(currentID, currentName, currentRecord, filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read archive transactions: "+err.Error())
		return
	}

	var baselineRun *archiveAnalysisRun
	if baselineID := strings.TrimSpace(r.URL.Query().Get("baseline_id")); baselineID != "" {
		if _, ok := s.runner.GetTest(baselineID); !ok {
			writeError(w, http.StatusNotFound, "baseline test not found")
			return
		}
		baselineRecord, ok := s.runner.GetRunArchive(baselineID)
		if !ok {
			writeError(w, http.StatusNotFound, "baseline archive not found")
			return
		}
		baselineName := baselineID
		if test, ok := s.runner.GetTest(baselineID); ok {
			baselineName = test.Name
		}
		run, err := archiveAnalysisRunFromRecord(baselineID, baselineName, baselineRecord, filter)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read baseline archive transactions: "+err.Error())
			return
		}
		baselineRun = &run
	}

	writeJSON(w, http.StatusOK, buildRunArchiveAnalysis(currentRun, baselineRun, filter, page))
}

func (s *Server) handleGetRunArchiveComparison(w http.ResponseWriter, r *http.Request) {
	currentRecord, ok := s.requireRunArchive(w, r)
	if !ok {
		return
	}
	if format := queryFormat(r); format != "" && format != "csv" {
		writeError(w, http.StatusBadRequest, "unsupported comparison format")
		return
	}
	breakdown := normalizeArchiveComparisonBreakdown(strings.ToLower(strings.TrimSpace(r.URL.Query().Get("breakdown"))))
	if breakdown == "" {
		writeError(w, http.StatusBadRequest, "unsupported comparison breakdown")
		return
	}
	action := strings.TrimSpace(r.URL.Query().Get("action"))
	if comparisonIDs := strings.TrimSpace(r.URL.Query().Get("comparison_ids")); comparisonIDs != "" {
		s.handleGetRunArchiveMultiComparison(w, r, currentRecord, breakdown, action, comparisonIDs)
		return
	}
	baselineID := strings.TrimSpace(r.URL.Query().Get("baseline_id"))
	if baselineID == "" {
		writeError(w, http.StatusBadRequest, "baseline_id is required")
		return
	}
	if _, ok := s.runner.GetTest(baselineID); !ok {
		writeError(w, http.StatusNotFound, "baseline test not found")
		return
	}
	baselineRecord, ok := s.runner.GetRunArchive(baselineID)
	if !ok {
		writeError(w, http.StatusNotFound, "baseline archive not found")
		return
	}
	currentID := r.PathValue("id")
	currentTest, _ := s.runner.GetTest(currentID)
	currentRun, err := s.archiveComparisonRunFromRecord(currentID, currentTest, currentRecord, action)
	if err != nil {
		s.writeArchiveComparisonRunError(w, err)
		return
	}
	baselineTest, _ := s.runner.GetTest(baselineID)
	baselineRun, err := s.archiveComparisonRunFromRecord(baselineID, baselineTest, baselineRecord, action)
	if err != nil {
		s.writeArchiveComparisonRunError(w, err)
		return
	}
	filename := "apexload-archive-comparison-" + currentID + "-" + baselineID
	if action != "" {
		filename += "-" + sanitizeFilename(action)
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+".csv\"")
	if err := writeArchiveComparisonCSV(w, breakdown,
		action,
		baselineRun.archiveComparisonRun,
		currentRun.archiveComparisonRun,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to write archive comparison csv: "+err.Error())
	}
}

func (s *Server) handleGetRunArchiveMultiComparison(w http.ResponseWriter, r *http.Request, baselineRecord *storage.RunArchiveRecord, breakdown string, action string, rawComparisonIDs string) {
	baselineID := r.PathValue("id")
	ids, ok := parseArchiveComparisonIDs(rawComparisonIDs)
	if !ok {
		writeError(w, http.StatusBadRequest, "comparison_ids contains duplicate run IDs")
		return
	}
	for _, id := range ids {
		if id == baselineID {
			writeError(w, http.StatusBadRequest, "comparison_ids must not include the baseline run")
			return
		}
	}
	totalRuns := len(ids) + 1
	if totalRuns < 2 {
		writeError(w, http.StatusBadRequest, "comparison_ids is required")
		return
	}
	if totalRuns > maxArchiveComparisonRuns {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("archive comparison supports at most %d runs", maxArchiveComparisonRuns))
		return
	}

	runs, err := s.archiveComparisonRunsFromRecords(baselineID, baselineRecord, ids, action)
	if err != nil {
		s.writeArchiveComparisonRunError(w, err)
		return
	}
	sort.SliceStable(runs, func(i, j int) bool {
		left := archiveComparisonRunStartedAt(runs[i])
		right := archiveComparisonRunStartedAt(runs[j])
		if !left.Equal(right) {
			return left.Before(right)
		}
		return runs[i].ID < runs[j].ID
	})

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	filename := "apexload-archive-comparison-" + baselineID + "-" + strconv.Itoa(len(runs)) + "-runs"
	if action != "" {
		filename += "-" + sanitizeFilename(action)
	}
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+".csv\"")
	csvRuns := make([]archiveComparisonRun, 0, len(runs))
	for _, run := range runs {
		csvRuns = append(csvRuns, run.archiveComparisonRun)
	}
	if err := writeArchiveComparisonCSV(w, breakdown, action, csvRuns...); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to write archive comparison csv: "+err.Error())
	}
}

type archiveComparisonRunWithStart struct {
	archiveComparisonRun
	startedAt time.Time
}

func parseArchiveComparisonIDs(raw string) ([]string, bool) {
	parts := strings.Split(raw, ",")
	ids := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		id := strings.TrimSpace(part)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			return nil, false
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids, true
}

func (s *Server) archiveComparisonRunsFromRecords(baselineID string, baselineRecord *storage.RunArchiveRecord, comparisonIDs []string, action string) ([]archiveComparisonRunWithStart, error) {
	baselineTest, ok := s.runner.GetTest(baselineID)
	if !ok {
		return nil, fmt.Errorf("baseline test not found")
	}
	runs := make([]archiveComparisonRunWithStart, 0, len(comparisonIDs)+1)
	baselineRun, err := s.archiveComparisonRunFromRecord(baselineID, baselineTest, baselineRecord, action)
	if err != nil {
		return nil, fmt.Errorf("failed to build baseline archive report: %w", err)
	}
	runs = append(runs, baselineRun)

	for _, id := range comparisonIDs {
		test, ok := s.runner.GetTest(id)
		if !ok {
			return nil, fmt.Errorf("comparison test not found: %s", id)
		}
		record, ok := s.runner.GetRunArchive(id)
		if !ok {
			return nil, fmt.Errorf("comparison archive not found: %s", id)
		}
		if err := ensureArchiveMaterialized(record); err != nil {
			return nil, fmt.Errorf("failed to materialize comparison archive: %w", err)
		}
		run, err := s.archiveComparisonRunFromRecord(id, test, record, action)
		if err != nil {
			return nil, fmt.Errorf("failed to build comparison archive report: %w", err)
		}
		runs = append(runs, run)
	}
	return runs, nil
}

func (s *Server) archiveComparisonRunFromRecord(id string, test *ManagedTest, record *storage.RunArchiveRecord, action string) (archiveComparisonRunWithStart, error) {
	report, err := buildRunArchiveReport(record)
	if err != nil {
		return archiveComparisonRunWithStart{}, err
	}
	name := id
	startedAt := record.CreatedAt
	if test != nil {
		name = test.Name
		if !test.CreatedAt.IsZero() {
			startedAt = test.CreatedAt
		}
		if test.StartedAt != nil && !test.StartedAt.IsZero() {
			startedAt = *test.StartedAt
		}
	}
	if startedAt.IsZero() && report.Manifest != nil {
		startedAt = report.Manifest.CreatedAt
	}
	if startedAt.IsZero() {
		startedAt = record.CompletedAt
	}
	run := archiveComparisonRunWithStart{
		archiveComparisonRun: archiveComparisonRun{ID: id, Name: name, Report: report},
		startedAt:            startedAt,
	}
	if strings.TrimSpace(action) != "" {
		if err := applyArchiveComparisonActionScope(record, &run.archiveComparisonRun, action); err != nil {
			return archiveComparisonRunWithStart{}, err
		}
	}
	return run, nil
}

type archiveComparisonActionNotFoundError struct {
	runID  string
	action string
}

func (err archiveComparisonActionNotFoundError) Error() string {
	return fmt.Sprintf("action %q not found in run %s", err.action, err.runID)
}

func applyArchiveComparisonActionScope(record *storage.RunArchiveRecord, run *archiveComparisonRun, action string) error {
	action = strings.TrimSpace(action)
	if action == "" {
		return nil
	}
	stats, ok := run.Report.ByAction[action]
	if !ok {
		return archiveComparisonActionNotFoundError{runID: run.ID, action: action}
	}
	summaries, err := loadArchiveIntervalSummariesForAction(record, action)
	if err != nil {
		return err
	}
	run.Action = action
	run.ActionStats = &stats
	run.ActionSummaries = summaries
	return nil
}

func (s *Server) writeArchiveComparisonRunError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	message := err.Error()
	var actionErr archiveComparisonActionNotFoundError
	switch {
	case errors.As(err, &actionErr):
		status = http.StatusBadRequest
	case strings.Contains(message, "test not found"):
		status = http.StatusNotFound
	case strings.Contains(message, "archive not found"):
		status = http.StatusNotFound
	}
	writeError(w, status, message)
}

func archiveComparisonRunStartedAt(run archiveComparisonRunWithStart) time.Time {
	return run.startedAt
}

func (s *Server) handleGetTestHTMLReport(w http.ResponseWriter, r *http.Request) {
	record, ok := s.requireRunArchive(w, r)
	if !ok {
		return
	}
	report, err := buildRunArchiveReport(record)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build archive report: "+err.Error())
		return
	}
	profile := r.URL.Query().Get("profile")
	if profile == "" {
		profile = "engineer"
	}
	// Attach release-over-release trend before rendering — only for profiles that
	// actually display the section (executive), so engineer/audit avoid the cost.
	s.attachReleaseTrendForProfile(report, profile)
	// The audit profile renders a tamper-evidence banner, so re-verify the archive
	// against its recorded artifact hashes here. Gated on the audit profile to keep
	// the SHA-256 re-hash off the lighter engineer/executive render paths.
	if profile == "audit" {
		report.Integrity = verifyRunArchiveIntegrity(record, report.Manifest)
	}
	annotations, err := resolveReportAnnotations(s.store, report, r.PathValue("id"), profile)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load report commentary: "+err.Error())
		return
	}
	// The generator-assignment manifest powers Appendix C. It is best-effort:
	// a failure (e.g. an archived run whose live state is gone) must not block
	// the report, so the renderer degrades to an "unavailable" note on nil.
	manifest, manifestErr := s.runner.GetTestManifest(r.PathValue("id"))
	if manifestErr != nil {
		manifest = nil
	}
	html, err := renderRunArchiveHTMLReportWithAnnotations(report, profile, annotations, manifest)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to render archive report: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(html)
}

func (s *Server) requireRunArchive(w http.ResponseWriter, r *http.Request) (*storage.RunArchiveRecord, bool) {
	id := r.PathValue("id")
	if _, ok := s.runner.GetTest(id); !ok {
		writeError(w, http.StatusNotFound, "test not found")
		return nil, false
	}
	record, ok := s.runner.GetRunArchive(id)
	if !ok {
		writeError(w, http.StatusNotFound, "archive not found")
		return nil, false
	}
	if err := ensureArchiveMaterialized(record); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to materialize archive: "+err.Error())
		return nil, false
	}
	return record, true
}

func parseArchivePage(w http.ResponseWriter, r *http.Request) (archivePage, bool) {
	limit := 100
	offset := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			writeError(w, http.StatusBadRequest, "invalid limit")
			return archivePage{}, false
		}
		limit = parsed
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			writeError(w, http.StatusBadRequest, "invalid offset")
			return archivePage{}, false
		}
		offset = parsed
	}
	return archivePage{Limit: limit, Offset: offset}, true
}

func parseTransactionEventFilter(w http.ResponseWriter, r *http.Request) (transactionEventFilter, bool) {
	query := r.URL.Query()
	filter := transactionEventFilter{
		Action:           strings.TrimSpace(query.Get("action")),
		SOAPOperation:    strings.TrimSpace(query.Get("soap_operation")),
		Group:            strings.TrimSpace(query.Get("group")),
		CorrelationID:    strings.TrimSpace(query.Get("correlation_id")),
		TraceID:          strings.TrimSpace(query.Get("trace_id")),
		DependencyType:   strings.TrimSpace(query.Get("dependency_type")),
		DependencyTarget: strings.TrimSpace(query.Get("dependency_target")),
		Status:           strings.TrimSpace(query.Get("status")),
		ErrorContains:    strings.TrimSpace(query.Get("error_contains")),
	}
	if raw := query.Get("success"); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid success")
			return transactionEventFilter{}, false
		}
		filter.Success = &parsed
	}
	if raw := query.Get("time_from"); raw != "" {
		parsed, err := parseArchiveFilterTime(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid time_from")
			return transactionEventFilter{}, false
		}
		filter.TimeFrom = &parsed
	}
	if raw := query.Get("time_to"); raw != "" {
		parsed, err := parseArchiveFilterTime(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid time_to")
			return transactionEventFilter{}, false
		}
		filter.TimeTo = &parsed
	}
	if filter.TimeFrom != nil && filter.TimeTo != nil && filter.TimeFrom.After(*filter.TimeTo) {
		writeError(w, http.StatusBadRequest, "time_from cannot be after time_to")
		return transactionEventFilter{}, false
	}

	var ok bool
	if filter.MinDurationMs, ok = parseOptionalFloatQuery(w, r, "min_duration_ms"); !ok {
		return transactionEventFilter{}, false
	}
	if filter.MaxDurationMs, ok = parseOptionalFloatQuery(w, r, "max_duration_ms"); !ok {
		return transactionEventFilter{}, false
	}
	if filter.MinDurationMs != nil && filter.MaxDurationMs != nil && *filter.MinDurationMs > *filter.MaxDurationMs {
		writeError(w, http.StatusBadRequest, "min_duration_ms cannot be greater than max_duration_ms")
		return transactionEventFilter{}, false
	}
	if filter.MinBytes, ok = parseOptionalInt64Query(w, r, "min_bytes"); !ok {
		return transactionEventFilter{}, false
	}
	if filter.MaxBytes, ok = parseOptionalInt64Query(w, r, "max_bytes"); !ok {
		return transactionEventFilter{}, false
	}
	if filter.MinBytes != nil && filter.MaxBytes != nil && *filter.MinBytes > *filter.MaxBytes {
		writeError(w, http.StatusBadRequest, "min_bytes cannot be greater than max_bytes")
		return transactionEventFilter{}, false
	}

	if raw := strings.TrimSpace(query.Get("sort_by")); raw != "" {
		switch raw {
		case "time", "status", "duration", "bytes", "error":
			filter.SortBy = raw
		default:
			writeError(w, http.StatusBadRequest, "invalid sort_by")
			return transactionEventFilter{}, false
		}
		sortDir := strings.ToLower(strings.TrimSpace(query.Get("sort_dir")))
		switch sortDir {
		case "", "asc":
			filter.SortDir = "asc"
		case "desc":
			filter.SortDir = "desc"
		default:
			writeError(w, http.StatusBadRequest, "invalid sort_dir")
			return transactionEventFilter{}, false
		}
	}
	return filter, true
}

func parseOptionalFloatQuery(w http.ResponseWriter, r *http.Request, name string) (*float64, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return nil, true
	}
	parsed, err := strconv.ParseFloat(raw, 64)
	if err != nil || parsed < 0 {
		writeError(w, http.StatusBadRequest, "invalid "+name)
		return nil, false
	}
	return &parsed, true
}

func parseOptionalInt64Query(w http.ResponseWriter, r *http.Request, name string) (*int64, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return nil, true
	}
	parsed, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || parsed < 0 {
		writeError(w, http.StatusBadRequest, "invalid "+name)
		return nil, false
	}
	return &parsed, true
}

func parseArchiveFilterTime(raw string) (time.Time, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return time.Time{}, fmt.Errorf("empty time")
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02T15:04",
		"2006-01-02 15:04:05",
		"2006-01-02",
	} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported time format")
}

func artifactContentType(name string) string {
	switch filepath.Ext(name) {
	case ".json", ".jsonl":
		return "application/x-ndjson"
	case ".parquet":
		return "application/octet-stream"
	default:
		return "application/octet-stream"
	}
}
