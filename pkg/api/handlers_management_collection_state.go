package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/storage"
)

type ManagementCollectionStateResponse struct {
	State               string     `json:"state"`
	PollID              string     `json:"poll_id,omitempty"`
	CollectorID         string     `json:"collector_id,omitempty"`
	CollectorInstanceID string     `json:"collector_instance_id,omitempty"`
	Generation          string     `json:"generation,omitempty"`
	PollSequence        string     `json:"poll_sequence,omitempty"`
	SourceRevision      int64      `json:"source_revision,omitempty"`
	Deadline            *time.Time `json:"deadline,omitempty"`
	GrantIssued         bool       `json:"grant_issued"`
}
type ManagementCollectionReleaseRequest struct {
	PollID              string `json:"poll_id"`
	CollectorID         string `json:"collector_id"`
	CollectorInstanceID string `json:"collector_instance_id"`
	Generation          string `json:"generation"`
	ProcessStopped      bool   `json:"process_stopped"`
}

func managementCollectionState(poll *storage.ManagementPoll, now time.Time) ManagementCollectionStateResponse {
	if poll == nil {
		return ManagementCollectionStateResponse{State: "not_collected"}
	}
	state := poll.State
	if state == "collecting" && !poll.Deadline.After(now) {
		state = "draining_unverified"
	}
	deadline := poll.Deadline
	return ManagementCollectionStateResponse{State: state, PollID: poll.ID, CollectorID: poll.CollectorID, CollectorInstanceID: poll.CollectorInstanceID, Generation: strconv.FormatInt(poll.Generation, 10), PollSequence: strconv.FormatInt(poll.Sequence, 10), SourceRevision: poll.Source.Revision, Deadline: &deadline, GrantIssued: poll.GrantIssuedAt != nil}
}

// Administrative process-stop attestation is separate from collector completion
// proof. Expiry alone never grants a second owner management I/O authority.
func (s *Server) handleManagementCollectionState(w http.ResponseWriter, r *http.Request) {
	project, source := r.PathValue("id"), r.PathValue("source_id")
	states, ok := s.store.(storage.ManagementCollectionStateStore)
	management, managementOK := s.store.(storage.ManagementMonitorStore)
	if !ok || !managementOK {
		writeError(w, 501, "management collection state unavailable")
		return
	}
	poll, err := states.GetCurrentManagementPoll(project, source)
	if errors.Is(err, sql.ErrNoRows) {
		poll = nil
		err = nil
	}
	if err != nil {
		writeError(w, 503, "management collection state unavailable")
		return
	}
	now := time.Now().UTC()
	if r.Method == http.MethodGet {
		writeJSON(w, 200, managementCollectionState(poll, now))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var request ManagementCollectionReleaseRequest
	if !decodeJSONBody(w, r, &request) {
		return
	}
	generation, err := analysis.ParseManagementInteger(request.Generation)
	if err != nil || generation < 1 || !request.ProcessStopped || poll == nil {
		writeError(w, 400, "exact ownership and confirmed process stop are required")
		return
	}
	if poll.ID != request.PollID || poll.CollectorID != request.CollectorID || poll.CollectorInstanceID != request.CollectorInstanceID || poll.Generation != generation {
		writeError(w, 409, "management collection ownership changed")
		return
	}
	auditID := uuid.NewString()
	details := map[string]any{"attestation_id": auditID, "poll_id": poll.ID, "collector_id": poll.CollectorID, "collector_instance_id": poll.CollectorInstanceID, "generation": request.Generation, "process_stopped": true}
	if err = s.persistAuditEvent(r, "monitor.management.process-stop", "requested", "monitor_source", source, project, details); err != nil {
		writeError(w, 503, "management stop attestation could not be audited")
		return
	}
	if err = management.AbandonManagementPoll(project, poll.CollectorID, poll.CollectorInstanceID, poll.ID, generation, storage.ManagementDrainProof{Kind: "process_stopped", AuditID: auditID}, now); err != nil {
		writeError(w, 409, "management stop attestation fenced")
		return
	}
	current, err := states.GetCurrentManagementPoll(project, source)
	if err != nil {
		writeError(w, 503, "management completion state unavailable")
		return
	}
	if err = s.persistAuditEvent(r, "monitor.management.process-stop", "complete", "monitor_source", source, project, details); err != nil {
		writeError(w, 503, "management stop attestation completion audit unavailable")
		return
	}
	writeJSON(w, 200, managementCollectionState(current, time.Now().UTC()))
}
