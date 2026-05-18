package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/stormrunner/pkg/dsl"
	"github.com/jasonczech/stormrunner/pkg/storage"
)

type authoringSessionPersistence interface {
	SaveAuthoringSession(session *storage.AuthoringSession) error
	GetAuthoringSession(id string) (*storage.AuthoringSession, error)
	ListAuthoringSessions(projectID string) ([]*storage.AuthoringSession, error)
	DeleteAuthoringSession(id string) error
}

type authoringSessionRequest struct {
	ProjectID   string         `json:"project_id,omitempty"`
	Source      string         `json:"source"`
	Name        string         `json:"name"`
	Status      string         `json:"status,omitempty"`
	Draft       dsl.Script     `json:"draft"`
	ReviewState map[string]any `json:"review_state,omitempty"`
}

type authoringSessionApprovalResponse struct {
	Session         *storage.AuthoringSession `json:"session"`
	Script          *StoredScript             `json:"script"`
	AuthoringHealth AuthoringHealth           `json:"authoring_health"`
}

func (s *Server) handleCreateAuthoringSession(w http.ResponseWriter, r *http.Request) {
	req, ok := readAuthoringSessionRequest(w, r)
	if !ok {
		return
	}
	now := time.Now()
	session := &storage.AuthoringSession{
		ID:             uuid.New().String(),
		ProjectID:      normalizeProjectID(req.ProjectID),
		Source:         normalizeAuthoringSessionSource(req.Source),
		Name:           authoringSessionName(req.Name, &req.Draft),
		Status:         normalizeAuthoringSessionStatus(req.Status),
		Draft:          &req.Draft,
		ReviewState:    cloneReviewState(req.ReviewState),
		CreatedAt:      now,
		UpdatedAt:      now,
		LastAccessedAt: now,
	}
	if session.Draft.ProjectID == "" {
		session.Draft.ProjectID = session.ProjectID
	}
	if err := s.saveAuthoringSession(session); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	enrichAuthoringSession(session)
	s.recordAuditEvent(r, "script.authoring_session.create", "success", "authoring_session", session.ID, session.ProjectID, map[string]any{"name": session.Name, "source": session.Source})
	writeJSON(w, http.StatusCreated, session)
}

func (s *Server) handleListAuthoringSessions(w http.ResponseWriter, r *http.Request) {
	projectID := r.URL.Query().Get("project_id")
	sessions, err := s.listAuthoringSessions(projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	enrichAuthoringSessions(sessions)
	writeJSON(w, http.StatusOK, map[string]any{"sessions": sessions, "count": len(sessions)})
}

func (s *Server) handleGetAuthoringSession(w http.ResponseWriter, r *http.Request) {
	session, ok := s.getAuthoringSession(w, r.PathValue("id"))
	if !ok {
		return
	}
	enrichAuthoringSession(session)
	writeJSON(w, http.StatusOK, session)
}

func (s *Server) handleUpdateAuthoringSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	existing, ok := s.getAuthoringSession(w, id)
	if !ok {
		return
	}
	req, ok := readAuthoringSessionRequest(w, r)
	if !ok {
		return
	}
	existing.ProjectID = normalizeProjectID(firstNonEmpty(req.ProjectID, existing.ProjectID))
	existing.Source = normalizeAuthoringSessionSource(firstNonEmpty(req.Source, existing.Source))
	existing.Name = authoringSessionName(firstNonEmpty(req.Name, existing.Name), &req.Draft)
	existing.Status = normalizeAuthoringSessionStatus(firstNonEmpty(req.Status, existing.Status))
	existing.Draft = &req.Draft
	if existing.Draft.ProjectID == "" {
		existing.Draft.ProjectID = existing.ProjectID
	}
	existing.ReviewState = cloneReviewState(req.ReviewState)
	existing.UpdatedAt = time.Now()
	existing.LastAccessedAt = existing.UpdatedAt
	if err := s.saveAuthoringSession(existing); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	enrichAuthoringSession(existing)
	s.recordAuditEvent(r, "script.authoring_session.update", "success", "authoring_session", existing.ID, existing.ProjectID, map[string]any{"name": existing.Name, "source": existing.Source, "status": existing.Status})
	writeJSON(w, http.StatusOK, existing)
}

func (s *Server) handleDeleteAuthoringSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	session, ok := s.getAuthoringSession(w, id)
	if !ok {
		return
	}
	if err := s.deleteAuthoringSession(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.recordAuditEvent(r, "script.authoring_session.delete", "success", "authoring_session", id, session.ProjectID, map[string]any{"name": session.Name, "source": session.Source})
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
}

func (s *Server) handleApproveAuthoringSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	session, ok := s.getAuthoringSession(w, id)
	if !ok {
		return
	}
	if session.Draft == nil {
		writeError(w, http.StatusBadRequest, "authoring session draft is required")
		return
	}

	validation := buildScriptValidationResponse("", session.Draft)
	if !validation.Valid || validation.AuthoringHealth.Status == "blocked" {
		updateAuthoringSessionReviewReadiness(session, validation.AuthoringHealth)
		_ = s.saveAuthoringSession(session)
		s.recordAuditEvent(r, "script.authoring_session.approve", "failure", "authoring_session", session.ID, session.ProjectID, map[string]any{
			"health_status": validation.AuthoringHealth.Status,
			"next_actions":  session.NextActions,
			"issues":        validation.Issues,
		})
		writeJSON(w, http.StatusBadRequest, validation)
		return
	}

	session.Draft.ProjectID = normalizeProjectID(firstNonEmpty(session.Draft.ProjectID, session.ProjectID))
	stored := s.runner.StoreScript(session.Draft)
	now := time.Now()
	session.Status = "approved"
	session.UpdatedAt = now
	session.LastAccessedAt = now
	session.ReviewState = cloneReviewState(session.ReviewState)
	if session.ReviewState == nil {
		session.ReviewState = map[string]any{}
	}
	session.ReviewState["approved_script_id"] = stored.ID
	session.ReviewState["approved_at"] = now.Format(time.RFC3339Nano)
	session.ReviewState["approval_health_status"] = validation.AuthoringHealth.Status
	updateAuthoringSessionReviewReadiness(session, validation.AuthoringHealth)
	if err := s.saveAuthoringSession(session); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	enrichAuthoringSession(session)

	s.recordAuditEvent(r, "script.authoring_session.approve", "success", "authoring_session", session.ID, session.ProjectID, map[string]any{"script_id": stored.ID, "health_status": validation.AuthoringHealth.Status})
	writeJSON(w, http.StatusOK, authoringSessionApprovalResponse{
		Session:         session,
		Script:          stored,
		AuthoringHealth: validation.AuthoringHealth,
	})
}

func enrichAuthoringSessions(sessions []*storage.AuthoringSession) {
	for _, session := range sessions {
		enrichAuthoringSession(session)
	}
}

func enrichAuthoringSession(session *storage.AuthoringSession) {
	if session == nil {
		return
	}
	if session.Status == "approved" {
		session.ApprovalReady = false
		if len(session.NextActions) == 0 {
			session.NextActions = []string{"Session is already approved and promoted to a stored script."}
		}
		return
	}
	if session.Draft == nil {
		session.ApprovalReady = false
		session.BlockingHealthInputs = []string{"draft"}
		session.NextActions = []string{"Attach or restore a draft before approval."}
		return
	}
	validation := buildScriptValidationResponse("", session.Draft)
	updateAuthoringSessionReviewReadiness(session, validation.AuthoringHealth)
}

func updateAuthoringSessionReviewReadiness(session *storage.AuthoringSession, health AuthoringHealth) {
	if session == nil {
		return
	}
	blocking := authoringHealthInputsWithStatus(health, "blocker")
	warnings := authoringHealthInputsWithStatus(health, "warning")
	session.BlockingHealthInputs = blocking
	session.ApprovalReady = health.Status == "ready" || health.Status == "needs_review"
	session.NextActions = authoringNextActions(health, blocking, warnings)
	if session.ReviewState == nil {
		session.ReviewState = map[string]any{}
	}
	session.ReviewState["approval_health_status"] = health.Status
	session.ReviewState["approval_ready"] = session.ApprovalReady
	session.ReviewState["blocking_health_inputs"] = append([]string(nil), session.BlockingHealthInputs...)
	session.ReviewState["next_actions"] = append([]string(nil), session.NextActions...)
}

func authoringHealthInputsWithStatus(health AuthoringHealth, status string) []string {
	out := make([]string, 0)
	for _, input := range health.Inputs {
		if input.Status == status {
			out = append(out, input.Key)
		}
	}
	return out
}

func authoringNextActions(health AuthoringHealth, blocking []string, warnings []string) []string {
	if len(blocking) == 0 && len(warnings) == 0 {
		return []string{"Approve this draft for reusable script promotion."}
	}
	actions := make([]string, 0, len(blocking)+len(warnings))
	for _, key := range blocking {
		actions = append(actions, authoringHealthInputAction(key, true))
	}
	for _, key := range warnings {
		actions = append(actions, authoringHealthInputAction(key, false))
	}
	if health.Status == "needs_review" && len(actions) == 0 {
		actions = append(actions, "Review warning-level health inputs before approval.")
	}
	return sortedUniqueStrings(actions)
}

func authoringHealthInputAction(key string, blocking bool) string {
	switch key {
	case "validation_issues":
		return "Fix validation issues in the draft script."
	case "runtime_parity":
		return "Review runtime parity findings and resolve incompatible settings."
	case "data_source_fit":
		return "Repair data-source bindings, required columns, or row-count fit."
	case "ai_guardrails":
		return "Attach AI provenance, diff review, validation, and replay evidence."
	case "replay_readiness":
		return "Run or repair replay readiness before promotion."
	case "warnings":
		return "Review validation warnings before promotion."
	default:
		if blocking {
			return "Resolve blocking authoring health input: " + key + "."
		}
		return "Review authoring health input: " + key + "."
	}
}

func readAuthoringSessionRequest(w http.ResponseWriter, r *http.Request) (authoringSessionRequest, bool) {
	var req authoringSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return req, false
	}
	if strings.TrimSpace(req.Source) == "" {
		writeError(w, http.StatusBadRequest, "source is required")
		return req, false
	}
	return req, true
}

func (s *Server) saveAuthoringSession(session *storage.AuthoringSession) error {
	if store, ok := s.store.(authoringSessionPersistence); ok {
		return store.SaveAuthoringSession(session)
	}
	s.authoringMu.Lock()
	defer s.authoringMu.Unlock()
	s.authoring[session.ID] = cloneAuthoringSession(session)
	return nil
}

func (s *Server) getAuthoringSession(w http.ResponseWriter, id string) (*storage.AuthoringSession, bool) {
	session, err := s.loadAuthoringSession(id)
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, "authoring session not found")
			return nil, false
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return nil, false
	}
	if session == nil {
		writeError(w, http.StatusNotFound, "authoring session not found")
		return nil, false
	}
	return session, true
}

func (s *Server) loadAuthoringSession(id string) (*storage.AuthoringSession, error) {
	if store, ok := s.store.(authoringSessionPersistence); ok {
		return store.GetAuthoringSession(id)
	}
	s.authoringMu.Lock()
	defer s.authoringMu.Unlock()
	session := cloneAuthoringSession(s.authoring[id])
	if session == nil {
		return nil, sql.ErrNoRows
	}
	session.LastAccessedAt = time.Now()
	s.authoring[id] = cloneAuthoringSession(session)
	return session, nil
}

func (s *Server) listAuthoringSessions(projectID string) ([]*storage.AuthoringSession, error) {
	if store, ok := s.store.(authoringSessionPersistence); ok {
		return store.ListAuthoringSessions(projectID)
	}
	projectID = strings.TrimSpace(projectID)
	s.authoringMu.Lock()
	defer s.authoringMu.Unlock()
	sessions := make([]*storage.AuthoringSession, 0, len(s.authoring))
	for _, session := range s.authoring {
		if projectID != "" && normalizeProjectID(session.ProjectID) != normalizeProjectID(projectID) {
			continue
		}
		sessions = append(sessions, cloneAuthoringSession(session))
	}
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})
	return sessions, nil
}

func (s *Server) deleteAuthoringSession(id string) error {
	if store, ok := s.store.(authoringSessionPersistence); ok {
		return store.DeleteAuthoringSession(id)
	}
	s.authoringMu.Lock()
	defer s.authoringMu.Unlock()
	delete(s.authoring, id)
	return nil
}

func normalizeAuthoringSessionSource(source string) string {
	return strings.ToLower(strings.TrimSpace(source))
}

func normalizeAuthoringSessionStatus(status string) string {
	status = strings.ToLower(strings.TrimSpace(status))
	switch status {
	case "reviewing", "editing", "blocked", "ready", "approved":
		return status
	default:
		return "editing"
	}
}

func authoringSessionName(name string, script *dsl.Script) string {
	name = strings.TrimSpace(name)
	if name != "" {
		return name
	}
	if script != nil && strings.TrimSpace(script.Name) != "" {
		return strings.TrimSpace(script.Name)
	}
	return "Untitled authoring session"
}

func cloneReviewState(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	data, err := json.Marshal(in)
	if err != nil {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil
	}
	return out
}

func cloneAuthoringSession(in *storage.AuthoringSession) *storage.AuthoringSession {
	if in == nil {
		return nil
	}
	out := *in
	if in.Draft != nil {
		draft := *in.Draft
		out.Draft = &draft
	}
	out.ReviewState = cloneReviewState(in.ReviewState)
	out.NextActions = append([]string(nil), in.NextActions...)
	out.BlockingHealthInputs = append([]string(nil), in.BlockingHealthInputs...)
	return &out
}
