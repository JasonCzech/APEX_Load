package api

import (
	"errors"
	"net/http"
	"strings"
)

func (s *Server) handleListExecutionPlanApprovals(w http.ResponseWriter, r *http.Request) {
	limit, offset := parseLimitOffsetQuery(r, defaultScheduleHistoryListLimit)
	if limit > maxScheduleHistoryListLimit {
		limit = maxScheduleHistoryListLimit
	}
	approvals, err := s.runner.ListExecutionPlanApprovalsPage(r.PathValue("id"), limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"approvals": approvals, "count": len(approvals), "limit": limit, "offset": offset})
}

func (s *Server) handleApproveExecutionPlanApproval(w http.ResponseWriter, r *http.Request) {
	s.resolveExecutionPlanApproval(w, r, true, "execution_plan.approval.approve")
}

func (s *Server) handleRejectExecutionPlanApproval(w http.ResponseWriter, r *http.Request) {
	s.resolveExecutionPlanApproval(w, r, false, "execution_plan.approval.reject")
}

func (s *Server) resolveExecutionPlanApproval(w http.ResponseWriter, r *http.Request, approve bool, action string) {
	planID := r.PathValue("id")
	approvalIDParam := r.PathValue("approval_id")
	// Reason is optional.
	var body struct {
		Reason string `json:"reason"`
	}
	if !decodeOptionalJSONBody(w, r, &body) {
		return
	}
	_, actorSubject, _ := s.resolveAuditActor(r)
	approval, found, err := s.runner.ResolveExecutionPlanApproval(planID, approvalIDParam, approve, actorSubject, strings.TrimSpace(body.Reason))
	if err != nil {
		if errors.Is(err, errNotExecutionLeader) {
			writeError(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		if found {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "execution plan approval not found")
		return
	}
	s.recordAuditEvent(r, action, "success", "execution_plan_approval", approval.ID, approval.ProjectID, map[string]any{"plan_id": planID, "status": approval.Status})
	writeJSON(w, http.StatusOK, approval)
}
