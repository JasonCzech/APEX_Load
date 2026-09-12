package api

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

type thresholdEvaluation struct {
	project, test, name string
	at                  time.Time
	incidents           []storage.ThresholdIncident
	covered             bool
}

// One bounded, on-demand worker serializes journal writes away from live metrics
// publication. It exits when drained; slow storage cannot create unbounded goroutines.
type thresholdJournalWriter struct {
	mu      sync.Mutex
	store   storage.ThresholdIncidentStore
	pending []thresholdEvaluation
	running bool
	failure string
	done    chan struct{}
	closed  bool
}

func (w *thresholdJournalWriter) enqueue(e thresholdEvaluation) {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return
	}
	if len(w.pending) >= 1024 {
		w.failure = "Alert history backlog exceeded; some samples may be missing."
		w.mu.Unlock()
		return
	}
	w.pending = append(w.pending, e)
	if !w.running {
		w.running = true
		w.done = make(chan struct{})
		go w.drain()
	}
	w.mu.Unlock()
}
func (w *thresholdJournalWriter) drain() {
	for {
		w.mu.Lock()
		if len(w.pending) == 0 {
			w.running = false
			close(w.done)
			w.pending = nil
			w.mu.Unlock()
			return
		}
		e := w.pending[0]
		w.pending[0] = thresholdEvaluation{}
		w.pending = w.pending[1:]
		w.mu.Unlock()
		if err := w.store.RecordThresholdEvaluation(e.project, e.test, e.name, e.at, e.covered, e.incidents); err != nil {
			slog.Warn("threshold history persistence failed", "test_id", e.test, "error", err)
			w.mu.Lock()
			w.failure = "Some alert history could not be saved. Live alerts may be newer than persisted history."
			w.mu.Unlock()
		}
	}
}
func (w *thresholdJournalWriter) shutdown(ctx context.Context) error {
	w.mu.Lock()
	w.closed = true
	done, running := w.done, w.running
	w.mu.Unlock()
	if !running {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// A tick without observations is not evidence that an enabled policy passed.
func thresholdSampleCovered(live *LiveMetrics, policies ThresholdSettings) bool {
	if live == nil || live.TotalTx <= 0 {
		return false
	}
	if policies.MaxAvgServerProcessingMs != nil || policies.MaxAvgTransferMs != nil {
		for _, tx := range live.Transactions {
			if tx != nil && tx.ServerTelemetry != nil && tx.ServerTelemetry.Samples > 0 {
				return true
			}
		}
		return false
	}
	return true
}

func (tr *TestRunner) journalThresholdEvaluation(test *ManagedTest, live *LiveMetrics, alerts []map[string]any) {
	if tr.thresholdJournal == nil {
		return
	}
	at := live.Timestamp
	if at.IsZero() {
		return
	}
	e := thresholdEvaluation{covered: thresholdSampleCovered(live, tr.GetSystemSettings().Thresholds), project: normalizeProjectID(test.ProjectID), test: test.ID, name: test.Name, at: at, incidents: []storage.ThresholdIncident{}}
	for _, a := range alerts {
		// Persist only the bounded incident fields, not trace samples, host metadata,
		// load-level arrays, or credentials carried by unrelated telemetry messages.
		payload := map[string]any{}
		for _, key := range []string{"metric", "value", "threshold", "target_tps", "gap_pct", "action", "transaction", "dependency_type", "dependency_target"} {
			if value, ok := a[key]; ok {
				payload[key] = value
			}
		}
		key := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s\x00%v\x00%v", test.ID, a["metric"], a["transaction"]))))
		severity := "warning"
		if a["metric"] == "error_pct" || a["action"] == "stop_requested" {
			severity = "danger"
		}
		e.incidents = append(e.incidents, storage.ThresholdIncident{Key: key, Severity: severity, Payload: payload})
	}
	tr.thresholdJournal.enqueue(e)
}

type ThresholdIncidentsResponse struct {
	Incidents           []storage.ThresholdIncident `json:"incidents"`
	ActiveCount         int                         `json:"active_count"`
	UnacknowledgedCount int                         `json:"unacknowledged_count"`
	DangerCount         int                         `json:"danger_count"`
	Limit               int                         `json:"limit"`
	Offset              int                         `json:"offset"`
	GlobalPolicyCount   int                         `json:"global_policy_count"`
	LastEvaluatedAt     *time.Time                  `json:"last_evaluated_at"`
	GeneratedAt         time.Time                   `json:"generated_at"`
	PersistenceWarning  string                      `json:"persistence_warning,omitempty"`
}

func (s *Server) handleListThresholdIncidents(w http.ResponseWriter, r *http.Request) {
	project := strings.TrimSpace(r.URL.Query().Get("project_id"))
	if project == "" {
		writeError(w, 400, "project_id is required")
		return
	}
	store, ok := s.store.(storage.ThresholdIncidentStore)
	if !ok {
		writeError(w, 503, "durable alert history is unavailable")
		return
	}
	limit, offset := parseLimitOffsetQuery(r, 100)
	if limit > 200 {
		limit = 200
	}
	filter := storage.ThresholdIncidentFilter{Status: r.URL.Query().Get("status"), Severity: r.URL.Query().Get("severity"), Query: strings.TrimSpace(r.URL.Query().Get("q"))}
	if filter.Status != "" && filter.Status != "active" && filter.Status != "resolved" && filter.Status != "unacknowledged" && filter.Status != "acknowledged" {
		writeError(w, 400, "invalid status filter")
		return
	}
	if filter.Severity != "" && filter.Severity != "warning" && filter.Severity != "danger" {
		writeError(w, 400, "invalid severity filter")
		return
	}
	if len(filter.Query) > 200 {
		writeError(w, 400, "search is limited to 200 characters")
		return
	}
	if since := r.URL.Query().Get("since"); since != "" {
		at, err := time.Parse(time.RFC3339, since)
		if err != nil {
			writeError(w, 400, "invalid since timestamp")
			return
		}
		filter.Since = &at
	}
	incidents, err := store.ListThresholdIncidents(project, limit, offset, filter)
	if err != nil {
		writeError(w, 500, "failed to read alert history")
		return
	}
	at, err := store.LastThresholdEvaluation(project)
	if err != nil {
		writeError(w, 500, "failed to read evaluation freshness")
		return
	}
	active, unacknowledged, danger, err := store.ThresholdIncidentCounts(project)
	if err != nil {
		writeError(w, 500, "failed to read incident counts")
		return
	}
	policies := s.runner.GetSystemSettings().Thresholds
	count := 0
	for _, value := range []*float64{policies.MaxErrorRatePct, policies.MaxP99LatencyMs, policies.MaxAvgServerProcessingMs, policies.MaxAvgTransferMs} {
		if value != nil {
			count++
		}
	}
	failure := ""
	if writer := s.runner.thresholdJournal; writer != nil {
		writer.mu.Lock()
		failure = writer.failure
		writer.mu.Unlock()
	}
	writeJSON(w, 200, ThresholdIncidentsResponse{Incidents: incidents, ActiveCount: active, UnacknowledgedCount: unacknowledged, DangerCount: danger, Limit: limit, Offset: offset, GlobalPolicyCount: count, LastEvaluatedAt: at, GeneratedAt: time.Now().UTC(), PersistenceWarning: failure})
}

var resolveThresholdIncidentProjectID = entityProjectIDResolver("alert not found", "failed to load alert", func(store storage.Store, id string) (string, error) {
	journal, ok := store.(storage.ThresholdIncidentStore)
	if !ok {
		return "", fmt.Errorf("durable alert history is unavailable")
	}
	incident, err := journal.GetThresholdIncident(id)
	if err != nil {
		return "", err
	}
	return incident.ProjectID, nil
})

func (s *Server) handleAcknowledgeThresholdIncident(w http.ResponseWriter, r *http.Request) {
	store, ok := s.store.(storage.ThresholdIncidentStore)
	if !ok {
		writeError(w, 503, "durable alert history is unavailable")
		return
	}
	incident, err := store.GetThresholdIncident(r.PathValue("id"))
	if err != nil {
		writeError(w, 404, "alert not found")
		return
	}
	var req struct {
		Acknowledged *bool `json:"acknowledged"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if req.Acknowledged == nil {
		writeError(w, 400, "acknowledged is required")
		return
	}
	name, subject, _ := s.resolveAuditActor(r)
	actor := subject
	if actor == "" {
		actor = name
	}
	if actor == "" {
		actor = "local administrator"
	}
	changed, err := store.AcknowledgeThresholdIncident(incident.ID, authorizedProjectOr(r, incident.ProjectID), actor, *req.Acknowledged, time.Now().UTC())
	if err != nil {
		writeError(w, 500, "failed to acknowledge alert")
		return
	}
	if !changed {
		writeError(w, 404, "alert not found")
		return
	}
	s.recordAuditEvent(r, "monitoring.threshold.ack", "success", "threshold_incident", incident.ID, incident.ProjectID, map[string]any{"acknowledged": *req.Acknowledged})
	saved, err := store.GetThresholdIncident(incident.ID)
	if err != nil {
		writeError(w, 500, "failed to read updated alert")
		return
	}
	writeJSON(w, 200, storage.ThresholdIncident(*saved))
}
