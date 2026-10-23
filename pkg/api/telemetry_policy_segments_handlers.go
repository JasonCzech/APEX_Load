package api

import (
	"database/sql"
	"errors"
	"net/http"
)

// handleListTelemetryPolicySegments returns the run's telemetry policy
// segments, the admission receipt first (TEL-B).
func (s *Server) handleListTelemetryPolicySegments(w http.ResponseWriter, r *http.Request) {
	segments, ok, err := s.runner.TelemetryPolicySegments(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "test not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "the run's telemetry policy segments could not be read")
		return
	}
	if segments == nil {
		segments = []TelemetryPolicySegmentReceipt{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"segments": segments})
}

// handleAppendTelemetryPolicySegment changes a running test's telemetry
// budget policy from now on (TEL-B): 201 with the new segment, or 200 with the
// segment a retried request_id created. A CI run may change it only where the
// project's GitHub Actions policy allows telemetry policy selection, as it may
// choose one at creation.
func (s *Server) handleAppendTelemetryPolicySegment(w http.ResponseWriter, r *http.Request) {
	if !s.requireExecutionLeader(w) {
		return
	}
	var req TelemetryPolicySegmentRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	testID := r.PathValue("id")
	test, ok := s.runner.GetTest(testID)
	if !ok || test == nil {
		writeError(w, http.StatusNotFound, "test not found")
		return
	}
	if _, err := decodeCIRunMetadata(test.MaterializationKey); err == nil {
		project, err := s.store.GetProject(test.ProjectID)
		if err != nil || project == nil {
			writeError(w, http.StatusServiceUnavailable, "the run's project could not be read")
			return
		}
		if !normalizeGitHubProjectPolicy(project.Settings.GitHub).Actions.AllowTelemetryPolicySelection {
			writeError(w, http.StatusForbidden, "a CI run's telemetry budget policy can change only where the project's GitHub Actions policy sets allow_telemetry_policy_selection")
			return
		}
	} else if !errors.Is(err, errNotCIRun) {
		writeError(w, http.StatusServiceUnavailable, "the run's CI metadata could not be read")
		return
	}
	actorType, actorSubject, actorDisplayName := s.resolveAuditActor(r)
	segment, replayed, err := s.runner.AppendTelemetryPolicySegment(testID, req, testCreationActor{Type: actorType, Subject: actorSubject, DisplayName: actorDisplayName})
	if err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			writeError(w, http.StatusNotFound, "test not found")
		case errors.Is(err, errNotExecutionLeader), errors.Is(err, errRunPolicyStoreUnavailable):
			writeError(w, http.StatusServiceUnavailable, err.Error())
		case errors.Is(err, errTelemetrySegmentState), errors.Is(err, errTelemetrySegmentRequestReused), errors.Is(err, errRunPolicyConfiguration):
			writeError(w, http.StatusConflict, err.Error())
		case errors.Is(err, errTelemetrySegmentRequest):
			writeError(w, http.StatusBadRequest, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	if replayed {
		writeJSON(w, http.StatusOK, segment)
		return
	}
	s.recordAuditEvent(r, "test.telemetry_policy.change", "success", "test", testID, test.ProjectID, map[string]any{
		"name": test.Name, "sequence": segment.Sequence, "effective_at": segment.EffectiveAt, "source": segment.Source,
		"policy_id": segment.PolicyID, "policy_hash": segment.Policy.Hash,
		"controller_publication_interval_ms": segment.Policy.ExpectedControllerPublicationIntervalMs,
	})
	writeJSON(w, http.StatusCreated, segment)
}
