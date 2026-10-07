package api

import (
	"crypto/ed25519"
	"github.com/jasonczech/apexload/pkg/collectoragent"
	"net/http"
	"strconv"
	"time"
)

func (s *Server) handlePollProviderInspections(w http.ResponseWriter, r *http.Request) {
	c := s.authorizeMonitorCollector(w, r)
	if c == nil {
		return
	}
	if !prometheusCollectorAllowed(c, c.ProjectID) {
		writeError(w, 403, "Prometheus query pack is not approved for this collector")
		return
	}
	key, err := monitorSigningKey()
	if err != nil {
		writeError(w, 503, err.Error())
		return
	}
	capacity := 4
	if raw := r.URL.Query().Get("capacity"); raw != "" {
		capacity, err = strconv.Atoi(raw)
		if err != nil || capacity < 1 || capacity > 4 {
			writeError(w, 400, "inspection capacity must be between 1 and 4")
			return
		}
	}
	jobs, err := s.pollPrometheusInspections(r, c, capacity, key)
	if err != nil {
		writeError(w, 503, "cannot poll provider inspections")
		return
	}
	writeJSON(w, 200, jobs)
}
func (s *Server) handleProviderInspectionResult(w http.ResponseWriter, r *http.Request) {
	c := s.authorizeMonitorCollector(w, r)
	if c == nil {
		return
	}
	if !prometheusCollectorAllowed(c, c.ProjectID) {
		writeError(w, 403, "Prometheus query pack is not approved for this collector")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var result collectoragent.InspectionResult
	if !decodeMonitorJSON(w, r, &result) {
		return
	}
	key, err := monitorSigningKey()
	if err != nil {
		writeError(w, 503, err.Error())
		return
	}
	job, err := collectoragent.VerifyInspectionJob(result.Job, key.Public().(ed25519.PublicKey), c.ID, time.Now().UTC())
	if err != nil || job.ProjectID != c.ProjectID || job.ID != r.PathValue("operation_id") {
		writeError(w, 403, "inspection authorization rejected")
		return
	}
	s.handlePrometheusInspectionResult(w, r, c, result, job)
}
