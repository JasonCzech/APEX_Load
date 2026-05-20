package api

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jasonczech/stormrunner/pkg/storage"
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
		w.Header().Set("Content-Disposition", "attachment; filename=\"stormrunner-archive-transactions-"+testID+".csv\"")
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
			"action":         filter.Action,
			"soap_operation": filter.SOAPOperation,
			"group":          filter.Group,
			"success":        filter.Success,
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
		w.Header().Set("Content-Disposition", "attachment; filename=\"stormrunner-archive-resources-"+testID+".csv\"")
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
		w.Header().Set("Content-Disposition", "attachment; filename=\"stormrunner-archive-summaries-"+testID+".csv\"")
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
		w.Header().Set("Content-Disposition", "attachment; filename=\"stormrunner-archive-report-"+testID+".csv\"")
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
	w.Header().Set("Content-Disposition", "attachment; filename=\"stormrunner-archive-comparison-"+currentID+"-"+baselineID+".csv\"")
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
	filter := transactionEventFilter{
		Action:           r.URL.Query().Get("action"),
		SOAPOperation:    r.URL.Query().Get("soap_operation"),
		Group:            r.URL.Query().Get("group"),
		CorrelationID:    r.URL.Query().Get("correlation_id"),
		TraceID:          r.URL.Query().Get("trace_id"),
		DependencyType:   r.URL.Query().Get("dependency_type"),
		DependencyTarget: r.URL.Query().Get("dependency_target"),
	}
	if raw := r.URL.Query().Get("success"); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid success")
			return transactionEventFilter{}, false
		}
		filter.Success = &parsed
	}
	return filter, true
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
