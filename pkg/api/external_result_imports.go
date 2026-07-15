package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

type externalResultImportPersistence interface {
	SaveExternalResultImport(result *storage.ExternalResultImport) error
	ListExternalResultImports(filter storage.ExternalResultImportFilter) ([]*storage.ExternalResultImport, error)
}

type immutableExternalResultImportPersistence interface {
	CreateExternalResultImportIfAbsent(result *storage.ExternalResultImport) (bool, error)
	GetExternalResultImport(id string) (*storage.ExternalResultImport, error)
	DeleteExternalResultImportIfUnchanged(expected *storage.ExternalResultImport) (bool, error)
}

const (
	maxInMemoryExternalResultImports     = 1024
	maxInMemoryExternalResultImportBytes = int64(64 << 20)
)

var errInMemoryExternalResultCapacity = errors.New("in-memory external result import capacity exceeded")

type ExternalResultImportRequest struct {
	Provider   string         `json:"provider"`
	Name       string         `json:"name,omitempty"`
	ProjectID  string         `json:"project_id,omitempty"`
	ReleaseID  string         `json:"release_id,omitempty"`
	ScenarioID string         `json:"scenario_id,omitempty"`
	Source     string         `json:"source,omitempty"`
	Summary    map[string]any `json:"summary,omitempty"`
	Result     map[string]any `json:"result,omitempty"`
	Results    map[string]any `json:"results,omitempty"`
}

func (s *Server) handleImportExternalResults(w http.ResponseWriter, r *http.Request) {
	var req ExternalResultImportRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	provider := normalizeExternalResultProvider(req.Provider)
	if provider == "" {
		writeError(w, http.StatusBadRequest, "provider must be one of apexload, k6, jmeter, or gatling")
		return
	}
	raw := req.Result
	if len(raw) == 0 {
		raw = req.Results
	}
	summary := normalizeExternalResultSummary(req.Summary)
	if len(summary) == 0 && len(raw) > 0 {
		summary = deriveExternalResultSummary(provider, raw)
	}
	if len(summary) == 0 {
		summary = map[string]any{"status": "imported"}
	}
	summary["provider"] = provider
	projectID := normalizeProjectID(req.ProjectID)
	if projectID == storage.DefaultProjectID && strings.TrimSpace(req.ReleaseID) != "" && s.store != nil {
		if resolvedProjectID, _, _, err := s.releaseContext(req.ReleaseID); err == nil {
			projectID = resolvedProjectID
		}
	}
	result := &storage.ExternalResultImport{
		ID:         uuid.New().String(),
		Provider:   provider,
		Name:       firstNonEmpty(strings.TrimSpace(req.Name), provider+" import"),
		ProjectID:  projectID,
		ReleaseID:  strings.TrimSpace(req.ReleaseID),
		ScenarioID: strings.TrimSpace(req.ScenarioID),
		Source:     strings.TrimSpace(req.Source),
		Summary:    summary,
		RawResult:  raw,
		CreatedAt:  time.Now().UTC(),
	}
	if err := s.saveExternalResultImport(result); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save external result import: "+err.Error())
		return
	}
	s.recordAuditEvent(r, "results.external_import.create", "success", "external_result_import", result.ID, result.ProjectID, map[string]any{
		"provider":   result.Provider,
		"release_id": result.ReleaseID,
		"source":     result.Source,
		"summary":    result.Summary,
	})
	writeJSON(w, http.StatusCreated, map[string]any{"import": result})
}

func (s *Server) handleListExternalResultImports(w http.ResponseWriter, r *http.Request) {
	limit, offset := parseLimitOffsetQuery(r, 100)
	filter := storage.ExternalResultImportFilter{
		ProjectID: strings.TrimSpace(r.URL.Query().Get("project_id")),
		ReleaseID: strings.TrimSpace(r.URL.Query().Get("release_id")),
		Provider:  normalizeExternalResultProvider(r.URL.Query().Get("provider")),
		Limit:     limit,
		Offset:    offset,
	}
	imports, err := s.listExternalResultImports(filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list external result imports: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"imports": imports,
		"count":   len(imports),
		"limit":   limit,
		"offset":  offset,
	})
}

func normalizeExternalResultProvider(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "apexload", "apex-load", "apex_load":
		return "apexload"
	case "k6":
		return "k6"
	case "jmeter", "apache-jmeter":
		return "jmeter"
	case "gatling":
		return "gatling"
	default:
		return ""
	}
}

func normalizeExternalResultSummary(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in)+1)
	for key, value := range in {
		out[strings.TrimSpace(key)] = value
	}
	if _, ok := out["status"]; !ok {
		if errorsValue, ok := numericFromMap(out, "errors"); ok && errorsValue > 0 {
			out["status"] = "failed"
		} else {
			out["status"] = "passed"
		}
	}
	return out
}

func deriveExternalResultSummary(provider string, raw map[string]any) map[string]any {
	switch provider {
	case "k6":
		return deriveK6Summary(raw)
	case "gatling":
		return deriveGatlingSummary(raw)
	case "jmeter":
		return deriveJMeterSummary(raw)
	default:
		return map[string]any{"status": "imported"}
	}
}

func deriveK6Summary(raw map[string]any) map[string]any {
	metrics := mapFromAny(raw["metrics"])
	transactions, _ := metricField(metrics, "http_reqs", "count")
	avg, _ := metricField(metrics, "http_req_duration", "avg")
	p95, _ := metricField(metrics, "http_req_duration", "p(95)", "p95")
	if p95 == 0 {
		if percentiles := mapFromAny(mapFromAny(metrics["http_req_duration"])["percentiles"]); len(percentiles) > 0 {
			p95, _ = numberFromAny(percentiles["95"])
		}
	}
	errorRate, _ := metricField(metrics, "http_req_failed", "rate")
	errors, _ := metricField(metrics, "http_req_failed", "fails")
	if errors == 0 && transactions > 0 && errorRate > 0 {
		errors = transactions * errorRate
	}
	errorPct := errorRate
	if errorPct > 0 && errorPct <= 1 {
		errorPct *= 100
	}
	return externalSummaryMap(transactions, errors, errorPct, avg, p95)
}

func deriveGatlingSummary(raw map[string]any) map[string]any {
	stats := mapFromAny(raw["stats"])
	requests := mapFromAny(stats["numberOfRequests"])
	transactions, _ := numberFromAny(requests["total"])
	errors, _ := numberFromAny(requests["ko"])
	avg, _ := numberFromAny(mapFromAny(stats["meanResponseTime"])["total"])
	p95, _ := numberFromAny(mapFromAny(stats["percentiles3"])["total"])
	errorPct := 0.0
	if transactions > 0 && errors > 0 {
		errorPct = errors / transactions * 100
	}
	return externalSummaryMap(transactions, errors, errorPct, avg, p95)
}

func deriveJMeterSummary(raw map[string]any) map[string]any {
	source := raw
	if aggregate := mapFromAny(raw["aggregate"]); len(aggregate) > 0 {
		source = aggregate
	}
	transactions, _ := firstNumeric(source, "samples", "sample_count", "transactions")
	errors, _ := firstNumeric(source, "errors", "error_count", "failed")
	avg, _ := firstNumeric(source, "avg", "average", "avg_response_ms", "mean")
	p95, _ := firstNumeric(source, "p95", "pct95", "95th", "p95_response_ms")
	errorPct, ok := firstNumeric(source, "error_pct", "error_percentage")
	if !ok && transactions > 0 && errors > 0 {
		errorPct = errors / transactions * 100
	}
	return externalSummaryMap(transactions, errors, errorPct, avg, p95)
}

func externalSummaryMap(transactions float64, errors float64, errorPct float64, avg float64, p95 float64) map[string]any {
	transactions = nonnegativeFiniteExternalMetric(transactions)
	errors = nonnegativeFiniteExternalMetric(errors)
	errorPct = nonnegativeFiniteExternalMetric(errorPct)
	if errorPct > 100 {
		errorPct = 100
	}
	avg = nonnegativeFiniteExternalMetric(avg)
	p95 = nonnegativeFiniteExternalMetric(p95)
	summary := map[string]any{
		"transactions":    externalMetricCount(transactions),
		"errors":          externalMetricCount(errors),
		"error_pct":       errorPct,
		"avg_response_ms": avg,
		"p95_response_ms": p95,
		"status":          "passed",
	}
	if errors > 0 || errorPct > 0 {
		summary["status"] = "failed"
	}
	return summary
}

func nonnegativeFiniteExternalMetric(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return 0
	}
	return value
}

func externalMetricCount(value float64) int64 {
	value = nonnegativeFiniteExternalMetric(value)
	if value >= float64(math.MaxInt64) {
		return math.MaxInt64
	}
	return int64(math.Round(value))
}

func metricField(metrics map[string]any, metric string, fields ...string) (float64, bool) {
	return firstNumeric(mapFromAny(metrics[metric]), fields...)
}

func firstNumeric(values map[string]any, fields ...string) (float64, bool) {
	for _, field := range fields {
		if value, ok := numberFromAny(values[field]); ok {
			return value, true
		}
	}
	return 0, false
}

func numericFromMap(values map[string]any, field string) (float64, bool) {
	return numberFromAny(values[field])
}

func numberFromAny(value any) (float64, bool) {
	finite := func(parsed float64, ok bool) (float64, bool) {
		if !ok || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
			return 0, false
		}
		return parsed, true
	}
	switch typed := value.(type) {
	case float64:
		return finite(typed, true)
	case float32:
		return finite(float64(typed), true)
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		return finite(parsed, err == nil)
	case string:
		parsed, err := json.Number(strings.TrimSpace(typed)).Float64()
		return finite(parsed, err == nil)
	default:
		return 0, false
	}
}

func mapFromAny(value any) map[string]any {
	if typed, ok := value.(map[string]any); ok {
		return typed
	}
	return nil
}

func (s *Server) saveExternalResultImport(result *storage.ExternalResultImport) error {
	if store, ok := s.store.(externalResultImportPersistence); ok {
		return store.SaveExternalResultImport(result)
	}
	copied := cloneExternalResultImport(result)
	weight, err := externalResultImportWeight(copied)
	if err != nil {
		return err
	}
	s.c4Mu.Lock()
	defer s.c4Mu.Unlock()
	return s.storeExternalResultImportInMemoryLocked(copied, weight, false)
}

func (s *Server) createExternalResultImportIfAbsent(result *storage.ExternalResultImport) (bool, *storage.ExternalResultImport, error) {
	if store, ok := s.store.(immutableExternalResultImportPersistence); ok {
		created, err := store.CreateExternalResultImportIfAbsent(result)
		if err != nil {
			return false, nil, err
		}
		if created {
			return true, cloneExternalResultImport(result), nil
		}
		existing, err := store.GetExternalResultImport(result.ID)
		return false, cloneExternalResultImport(existing), err
	}
	if s.store != nil {
		return false, nil, fmt.Errorf("durable external-result store does not support immutable create-only persistence")
	}
	s.c4Mu.Lock()
	defer s.c4Mu.Unlock()
	if existing := s.resultImports[result.ID]; existing != nil {
		return false, cloneExternalResultImport(existing), nil
	}
	copied := cloneExternalResultImport(result)
	weight, err := externalResultImportWeight(copied)
	if err != nil {
		return false, nil, err
	}
	if err := s.storeExternalResultImportInMemoryLocked(copied, weight, true); err != nil {
		return false, nil, err
	}
	return true, cloneExternalResultImport(copied), nil
}

func (s *Server) getExternalResultImport(id string) (*storage.ExternalResultImport, bool, error) {
	if store, ok := s.store.(immutableExternalResultImportPersistence); ok {
		result, err := store.GetExternalResultImport(id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, err
		}
		return cloneExternalResultImport(result), true, nil
	}
	if s.store != nil {
		return nil, false, fmt.Errorf("durable external-result store does not support immutable lookup")
	}
	s.c4Mu.Lock()
	defer s.c4Mu.Unlock()
	result := s.resultImports[id]
	return cloneExternalResultImport(result), result != nil, nil
}

func (s *Server) deleteExternalResultImportIfUnchanged(expected *storage.ExternalResultImport) (bool, error) {
	if expected == nil {
		return false, fmt.Errorf("expected external result import is required")
	}
	if store, ok := s.store.(immutableExternalResultImportPersistence); ok {
		return store.DeleteExternalResultImportIfUnchanged(expected)
	}
	if s.store != nil {
		return false, fmt.Errorf("durable external-result store does not support conditional delete")
	}
	s.c4Mu.Lock()
	defer s.c4Mu.Unlock()
	existing := s.resultImports[expected.ID]
	if !equivalentExternalResultImport(existing, expected) || existing == nil || !existing.CreatedAt.Equal(expected.CreatedAt) {
		return false, nil
	}
	weight, err := externalResultImportWeight(existing)
	if err != nil {
		return false, err
	}
	s.resultImportBytes -= weight
	if s.resultImportBytes < 0 {
		s.resultImportBytes = 0
	}
	delete(s.resultImports, expected.ID)
	return true, nil
}

func externalResultImportWeight(result *storage.ExternalResultImport) (int64, error) {
	if result == nil {
		return 0, fmt.Errorf("external result import is required")
	}
	writer := &archiveJSONCountingWriter{}
	if err := json.NewEncoder(writer).Encode(result); err != nil {
		return 0, fmt.Errorf("measure external result import: %w", err)
	}
	return writer.bytes, nil
}

// storeExternalResultImportInMemoryLocked enforces both dimensions of the
// fallback store's retention budget. Callers hold c4Mu and pass a deep copy so
// later caller mutation cannot invalidate byte accounting.
func (s *Server) storeExternalResultImportInMemoryLocked(result *storage.ExternalResultImport, weight int64, createOnly bool) error {
	if result == nil || strings.TrimSpace(result.ID) == "" {
		return fmt.Errorf("external result import id is required")
	}
	if weight < 0 || weight > maxInMemoryExternalResultImportBytes {
		return errInMemoryExternalResultCapacity
	}
	if s.resultImports == nil {
		s.resultImports = make(map[string]*storage.ExternalResultImport)
	}
	existing := s.resultImports[result.ID]
	if createOnly && existing != nil {
		return fmt.Errorf("external result import already exists")
	}
	if existing == nil && len(s.resultImports) >= maxInMemoryExternalResultImports {
		return errInMemoryExternalResultCapacity
	}
	existingWeight := int64(0)
	if existing != nil {
		var err error
		existingWeight, err = externalResultImportWeight(existing)
		if err != nil {
			return err
		}
	}
	projectedBytes := s.resultImportBytes - existingWeight + weight
	if projectedBytes < 0 || projectedBytes > maxInMemoryExternalResultImportBytes {
		return errInMemoryExternalResultCapacity
	}
	s.resultImports[result.ID] = result
	s.resultImportBytes = projectedBytes
	return nil
}

func (s *Server) listExternalResultImports(filter storage.ExternalResultImportFilter) ([]*storage.ExternalResultImport, error) {
	if store, ok := s.store.(externalResultImportPersistence); ok {
		return store.ListExternalResultImports(filter)
	}
	s.c4Mu.Lock()
	defer s.c4Mu.Unlock()
	projectID := normalizeProjectID(filter.ProjectID)
	out := make([]*storage.ExternalResultImport, 0, len(s.resultImports))
	for _, result := range s.resultImports {
		if result == nil {
			continue
		}
		if filter.ProjectID != "" && normalizeProjectID(result.ProjectID) != projectID {
			continue
		}
		if filter.ReleaseID != "" && result.ReleaseID != filter.ReleaseID {
			continue
		}
		if filter.Provider != "" && result.Provider != filter.Provider {
			continue
		}
		out = append(out, cloneExternalResultImport(result))
	}
	sort.SliceStable(out, func(left, right int) bool {
		return out[left].CreatedAt.After(out[right].CreatedAt)
	})
	if filter.Offset > len(out) {
		return []*storage.ExternalResultImport{}, nil
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	end := filter.Offset + limit
	if end > len(out) {
		end = len(out)
	}
	return out[filter.Offset:end], nil
}

func parseLimitOffsetQuery(r *http.Request, defaultLimit int) (int, int) {
	const maxListLimit = 1000
	limit := defaultLimit
	offset := 0
	if parsed, err := parsePositiveIntQuery(r.URL.Query().Get("limit")); err == nil && parsed > 0 {
		limit = parsed
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}
	if parsed, err := parsePositiveIntQuery(r.URL.Query().Get("offset")); err == nil && parsed >= 0 {
		offset = parsed
	}
	return limit, offset
}

func parsePositiveIntQuery(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, err
	}
	if value < 0 {
		return 0, fmt.Errorf("negative value")
	}
	return value, nil
}
