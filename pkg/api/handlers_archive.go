package api

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

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
	if _, allowed := archiveManifestArtifactNames(manifest)[name]; !allowed {
		writeError(w, http.StatusNotFound, "archive artifact not found")
		return
	}
	fullPath := filepath.Join(record.ArchivePath, name)
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
	if strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("format")), "csv") {
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
	if strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("format")), "csv") {
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
	items, total, err := loadArchiveIntervalSummaries(record, page)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read archive summaries: "+err.Error())
		return
	}
	if strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("format")), "csv") {
		testID := r.PathValue("id")
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", "attachment; filename=\"apexload-archive-summaries-"+testID+".csv\"")
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
	if strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("format")), "csv") {
		testID := r.PathValue("id")
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
	}
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
	breakdown := normalizeArchiveComparisonBreakdown(strings.ToLower(strings.TrimSpace(r.URL.Query().Get("breakdown"))))
	if breakdown == "" {
		writeError(w, http.StatusBadRequest, "unsupported comparison breakdown")
		return
	}
	currentReport, err := buildRunArchiveReport(currentRecord)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build archive report: "+err.Error())
		return
	}
	baselineReport, err := buildRunArchiveReport(baselineRecord)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build baseline archive report: "+err.Error())
		return
	}
	currentID := r.PathValue("id")
	currentName := currentID
	if test, ok := s.runner.GetTest(currentID); ok {
		currentName = test.Name
	}
	baselineName := baselineID
	if test, ok := s.runner.GetTest(baselineID); ok {
		baselineName = test.Name
	}
	if format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format"))); format != "" && format != "csv" {
		writeError(w, http.StatusBadRequest, "unsupported comparison format")
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=\"apexload-archive-comparison-"+currentID+"-"+baselineID+".csv\"")
	if err := writeArchiveComparisonCSV(w, breakdown,
		archiveComparisonRun{ID: baselineID, Name: baselineName, Report: baselineReport},
		archiveComparisonRun{ID: currentID, Name: currentName, Report: currentReport},
	); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to write archive comparison csv: "+err.Error())
	}
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
	html, err := renderRunArchiveHTMLReportWithProfile(report, profile)
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
