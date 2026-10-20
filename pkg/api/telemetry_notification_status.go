package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// TelemetryNotificationStatus separates live delivery state from the pinned
// acceptance verdict. It contains no payload, destination or credentials.
type TelemetryNotificationStatus struct {
	Available   bool                                           `json:"available"`
	Reason      string                                         `json:"reason,omitempty"`
	Publication *storage.TelemetryNotificationPublicationState `json:"publication,omitempty"`
}

func (s *Server) handleTelemetryNotificationStatus(w http.ResponseWriter, r *http.Request) {
	store, ok := s.runner.archiveStore.(interface {
		GetTelemetryNotificationPublication(context.Context, string, string) (*storage.TelemetryNotificationPublicationState, error)
	})
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "durable telemetry notification state unavailable")
		return
	}
	run, err := s.store.GetTest(r.PathValue("id"))
	if err != nil || run == nil {
		writeError(w, http.StatusNotFound, "test not found")
		return
	}
	project := normalizeProjectID(run.ProjectID)
	if !requireAuthorizedProjectMatch(w, r, project) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	publication, err := store.GetTelemetryNotificationPublication(ctx, project, run.ID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusServiceUnavailable, "telemetry notification state temporarily unavailable")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if publication == nil {
		writeJSON(w, http.StatusOK, TelemetryNotificationStatus{Reason: "not_recorded"})
		return
	}
	writeJSON(w, http.StatusOK, TelemetryNotificationStatus{Available: true, Publication: publication})
}
