package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/monitorprovider"
	"github.com/jasonczech/apexload/pkg/storage"
)

type RunMonitorEvidence struct {
	SourceID string `json:"source_id"`
	monitorprovider.Diagnostic
}

func (s *Server) handleRunMonitorEvidence(w http.ResponseWriter, r *http.Request) {
	project, status, message := resolveTestProjectID(r, s.store)
	if status != 0 {
		writeError(w, status, message)
		return
	}
	store := s.runMonitorRuntimeStore(w)
	if store == nil {
		return
	}
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and 100")
			return
		}
		limit = parsed
	}
	kind := r.URL.Query().Get("kind")
	signal := r.URL.Query().Get("signal")
	if signal != "" {
		if kind != "" && kind != signal {
			writeError(w, 400, "kind and signal filters differ")
			return
		}
		kind = signal
	}
	if kind != "" && kind != "event" && kind != "violation" && kind != "snapshot" && kind != "log" && kind != "transaction" && kind != "target" && kind != "alert" && kind != "problem" && kind != "span" && kind != "trace" && kind != "error" && kind != "health" {
		writeError(w, http.StatusBadRequest, "unsupported evidence kind")
		return
	}
	filter := storage.EvidenceHeadFilter{Kind: kind, Service: r.URL.Query().Get("service"), Severity: r.URL.Query().Get("severity"), TraceID: r.URL.Query().Get("trace_id")}
	for key, target := range map[string]**time.Time{"from": &filter.From, "until": &filter.Until} {
		if raw := r.URL.Query().Get(key); raw != "" {
			at, err := time.Parse(time.RFC3339Nano, raw)
			if err != nil || !strings.HasSuffix(raw, "Z") {
				writeError(w, 400, "evidence times must be UTC RFC3339")
				return
			}
			at = at.UTC()
			*target = &at
		}
	}
	if filter.Validate() != nil || len(r.URL.Query().Get("source_id")) > 256 || len(r.URL.Query().Get("after_key")) > 64 {
		writeError(w, 400, "invalid evidence filters")
		return
	}
	provider, ok := store.(filteredEvidenceProvider)
	if !ok {
		writeError(w, 503, "filtered monitor evidence unavailable")
		return
	}
	page, err := loadCanonicalMonitorData(r.Context(), filteredEvidenceStore{provider, filter}, project, r.PathValue("id"), r.URL.Query().Get("source_id"), "diagnostic", r.URL.Query().Get("after_key"), limit)
	if err != nil {
		writeError(w, 503, "monitor evidence unavailable or corrupt")
		return
	}
	evidence := []RunMonitorEvidence{}
	for i, d := range page.Evidence {
		evidence = append(evidence, RunMonitorEvidence{SourceID: page.EvidenceSources[i], Diagnostic: scrubRetainedProviderEvidence(d)})
	}
	writeJSON(w, 200, map[string]any{"evidence": evidence, "has_more": page.HasMore, "after_key": page.AfterKey, "after_sequence": page.AfterKey})
}

type filteredEvidenceProvider interface {
	providerMonitorStore
	ListProviderEvidenceHeads(string, string, string, string, storage.EvidenceHeadFilter, int) ([]storage.ObservationHead, error)
}
type filteredEvidenceStore struct {
	filteredEvidenceProvider
	filter storage.EvidenceHeadFilter
}

func (s filteredEvidenceStore) ListProviderObservationHeads(project, run, source, kind, after string, limit int) ([]storage.ObservationHead, error) {
	return s.ListProviderEvidenceHeads(project, run, source, after, s.filter, limit)
}

// Retain provider-specific first-sighting history through the filtered adapter.
func (s filteredEvidenceStore) ListProviderFirstDiagnosticHeads(project, run string, keys []string) ([]storage.ObservationHead, error) {
	if first, ok := s.filteredEvidenceProvider.(interface {
		ListProviderFirstDiagnosticHeads(string, string, []string) ([]storage.ObservationHead, error)
	}); ok {
		return first.ListProviderFirstDiagnosticHeads(project, run, keys)
	}
	return nil, nil
}
