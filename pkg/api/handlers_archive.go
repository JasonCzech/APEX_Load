package api

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"

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
	writeJSON(w, http.StatusOK, report)
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
	html, err := renderRunArchiveHTMLReport(report)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to render archive report: "+err.Error())
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
