package api

import (
	"net/http"
	"sort"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/storage"
)

type DependencyMonitorEvidence struct {
	SourceID   string   `json:"source_id"`
	Name       string   `json:"name"`
	Dependency string   `json:"dependency"`
	State      string   `json:"state"`
	Coverage   string   `json:"coverage"`
	Metrics    []string `json:"metrics"`
}
type DependencyEvidenceResponse struct {
	Timestamp       time.Time                   `json:"timestamp"`
	IntervalSeconds float64                     `json:"interval_seconds"`
	Graph           *LiveDependencyMap          `json:"graph,omitempty"`
	Monitors        []DependencyMonitorEvidence `json:"monitors"`
	Coverage        string                      `json:"coverage"`
	Advisory        bool                        `json:"advisory"`
}

// The graph describes one actual interval, never an average of interval P95s.
// Explicit source service bindings supply association; host-name resemblance
// alone is not evidence of a dependency or a cause.
func (s *Server) handleDependencyEvidence(w http.ResponseWriter, r *http.Request) {
	project, status, message := resolveTestProjectID(r, s.store)
	if status != 0 {
		writeError(w, status, message)
		return
	}
	if !diagnosticsProjectEnabled(project) {
		writeError(w, 409, "dependency diagnostics are not enabled for this project")
		return
	}
	at := time.Now().UTC()
	if raw := r.URL.Query().Get("at"); raw != "" {
		parsed, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			writeError(w, 400, "invalid observation timestamp")
			return
		}
		at = parsed.UTC()
	}
	since := at.Add(-time.Hour)
	test, ok := s.runner.GetTestMetricsWindow(r.PathValue("id"), &since, 3600)
	if !ok {
		writeError(w, 404, "run not found")
		return
	}
	var current *LiveMetrics
	for _, point := range test.History {
		if point != nil && !point.Timestamp.After(at) && (current == nil || current.Timestamp.Before(point.Timestamp)) {
			current = point
		}
	}
	if test.LiveMetrics != nil && !test.LiveMetrics.Timestamp.After(at) && (current == nil || current.Timestamp.Before(test.LiveMetrics.Timestamp)) {
		current = test.LiveMetrics
	}
	if current == nil || current.Timestamp.Before(since) {
		writeError(w, 404, "no retained observation in this time window")
		return
	}
	response := DependencyEvidenceResponse{Timestamp: current.Timestamp, IntervalSeconds: current.SampleWindowSeconds, Graph: current.DependencyMap, Monitors: []DependencyMonitorEvidence{}, Coverage: "observed", Advisory: true}
	if current.ObservationState == "stale" || current.DependencyMap == nil {
		response.Coverage = "incomplete"
	}
	store, ok := s.store.(storage.RunMonitorStore)
	if !ok {
		writeError(w, 503, "monitor evidence unavailable")
		return
	}
	jobs, err := store.ListRunMonitorJobs(project, test.ID)
	if err != nil {
		writeError(w, 503, "monitor evidence unavailable")
		return
	}
	if !s.requireAnalysisBackend(w) {
		return
	}
	readBytes := 0
	readReceipts := 0
	for _, job := range jobs {
		if job.Source.Service == "" {
			continue
		}
		evidence := DependencyMonitorEvidence{SourceID: job.Source.ID, Name: job.Source.Name, Dependency: job.Source.Service, State: job.State, Coverage: "missing", Metrics: []string{}}
		// At most 100 receipts per bound source; requests that need a deeper scan
		// report incomplete coverage instead of pretending missing data is zero.
		receipts, err := store.ListMonitorBatchReceipts(project, test.ID, job.ID, max(int64(-1), job.NextSequence-101), 100)
		if err != nil {
			writeError(w, 503, "cannot read monitor coverage")
			return
		}
		names := map[string]bool{}
		invalid := false
		if job.NextSequence > 100 {
			evidence.Coverage = "scan_limited"
		}
		from := current.Timestamp.Add(-time.Duration(current.SampleWindowSeconds * float64(time.Second)))
		for _, receipt := range receipts {
			if receipt.JobID != job.ID {
				continue
			}
			if receipt.ReceivedAt.Before(from.Add(-30*time.Second)) || receipt.ReceivedAt.After(current.Timestamp.Add(90*time.Second)) {
				continue
			}
			readReceipts++
			if readReceipts > 100 {
				writeError(w, 422, "dependency evidence exceeds the bounded scan limit")
				return
			}
			body, err := readMonitorArtifact(r.Context(), receipt.ArtifactKey, receipt.Digest)
			if err != nil {
				writeError(w, 503, "monitor artifact unavailable")
				return
			}
			readBytes += len(body)
			if readBytes > 16<<20 {
				writeError(w, 422, "dependency evidence exceeds the byte limit")
				return
			}
			evidenceBody, err := decodeMonitorEvidenceBody(body, project, r.PathValue("id"), receipt)
			if err != nil {
				writeError(w, 503, "invalid monitor artifact")
				return
			}
			for _, point := range evidenceBody.Points {
				if point.Source == job.Source.ID && !point.TimestampUTC.Before(from) && !point.TimestampUTC.After(current.Timestamp) {
					if monitorPointQuality(point, receipt.ReceivedAt) == analysis.QualityValid {
						names[point.MetricName] = true
						evidence.Coverage = "observed"
					} else {
						invalid = true
					}
				}
			}
		}
		if invalid {
			evidence.Coverage = "incomplete"
		}
		if evidence.Coverage != "observed" {
			response.Coverage = "incomplete"
		}
		if len(names) > 128 {
			writeError(w, 422, "dependency evidence exceeds the metric limit")
			return
		}
		for name := range names {
			evidence.Metrics = append(evidence.Metrics, name)
		}
		sort.Strings(evidence.Metrics)
		response.Monitors = append(response.Monitors, evidence)
	}
	writeJSON(w, 200, response)
}
