package api

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	defaultAuthoringSessionListLimit        = 25
	maxAuthoringSessionListLimit            = 100
	maxAuthoringSessionIdempotencyKeyLength = 256
	authoringSessionIdempotencyReviewKey    = "_apexload_create_idempotency"
)

type authoringSessionPersistence interface {
	SaveAuthoringSession(session *storage.AuthoringSession) error
	GetAuthoringSession(id string) (*storage.AuthoringSession, error)
	ListAuthoringSessions(projectID string) ([]*storage.AuthoringSession, error)
	DeleteAuthoringSession(id string) error
}

type authoringSessionCASPersistence interface {
	CompareAndSwapAuthoringSession(session *storage.AuthoringSession, expectedUpdatedAt time.Time, expectedStatus string) (bool, error)
	DeleteAuthoringSessionUnlessApproving(id string) (bool, error)
}

type authoringSessionCreatePersistence interface {
	CreateAuthoringSession(session *storage.AuthoringSession) (bool, error)
}

type authoringSessionAccessPersistence interface {
	TouchAuthoringSessionAccess(id string, accessedAt time.Time) (bool, error)
}

type authoringSessionRequest struct {
	ProjectID      string         `json:"project_id,omitempty"`
	Source         string         `json:"source"`
	Name           string         `json:"name"`
	Status         string         `json:"status,omitempty"`
	Draft          dsl.Script     `json:"draft"`
	ReviewState    map[string]any `json:"review_state,omitempty"`
	IdempotencyKey string         `json:"idempotency_key,omitempty"`
}

type authoringSessionApprovalResponse struct {
	Session         *storage.AuthoringSession `json:"session"`
	Script          *StoredScript             `json:"script"`
	AuthoringHealth AuthoringHealth           `json:"authoring_health"`
}

// prepareAuthoringSessionDraft applies the same durable secret boundary used
// by script create/update. Executable credentials become structured binding
// requirements, while non-executable import evidence is scrubbed before the
// draft is stored or promoted.
func prepareAuthoringSessionDraft(w http.ResponseWriter, draft, prior *dsl.Script) (*dsl.Script, bool) {
	projected := maskScriptSecrets(draft)
	if projected == nil {
		writeError(w, http.StatusBadRequest, "failed to project authoring draft for secret binding")
		return nil, false
	}
	if unresolved := discoverSecretBindingRequirements(projected, 0); len(unresolved) > 0 {
		writeSecretBindingsRequired(w, unresolved)
		return nil, false
	}
	if err := reconcileMaskedScriptSecrets(projected, prior); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return nil, false
	}
	return projected, true
}

func (s *Server) handleCreateAuthoringSession(w http.ResponseWriter, r *http.Request) {
	req, ok := readAuthoringSessionRequest(w, r)
	if !ok {
		return
	}
	req.IdempotencyKey = strings.TrimSpace(req.IdempotencyKey)
	if len(req.IdempotencyKey) > maxAuthoringSessionIdempotencyKeyLength {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("idempotency_key must be at most %d bytes", maxAuthoringSessionIdempotencyKeyLength))
		return
	}
	if err := validateClientAuthoringSessionStatus(req.Status); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := bindDraftScriptProject(&req.Draft, req.ProjectID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	safeDraft, ok := prepareAuthoringSessionDraft(w, &req.Draft, nil)
	if !ok {
		return
	}
	req.Draft = *safeDraft
	sessionSource := normalizeAuthoringSessionSource(req.Source)
	markImportedJavaVuserAuthoringDraft(&req.Draft, sessionSource)
	now := time.Now()
	session := &storage.AuthoringSession{
		ProjectID:      normalizeProjectID(req.Draft.ProjectID),
		Source:         sessionSource,
		Name:           authoringSessionName(req.Name, &req.Draft),
		Status:         normalizeAuthoringSessionStatus(req.Status),
		Draft:          &req.Draft,
		ReviewState:    cloneReviewState(req.ReviewState),
		CreatedAt:      now,
		UpdatedAt:      now,
		LastAccessedAt: now,
	}
	// Reserve the internal review-state namespace for server-authored
	// idempotency evidence. A client cannot hide arbitrary state behind the
	// response projection or forge the envelope preserved by later edits.
	delete(session.ReviewState, authoringSessionIdempotencyReviewKey)
	if req.IdempotencyKey == "" {
		session.ID = uuid.New().String()
		if err := s.saveAuthoringSession(session); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	} else {
		// A deterministic ID plus an insert-only storage operation makes a
		// retried save converge on one governed review session across controller
		// replicas. The request fingerprint rejects accidental key reuse for a
		// different draft while still returning the original response after a
		// lost network reply.
		session.ID = authoringSessionIdempotentID(session.ProjectID, req.IdempotencyKey)
		requestHash := authoringSessionCreateRequestHash(session)
		stampAuthoringSessionCreateIdempotency(session, req.IdempotencyKey)
		created, err := s.createAuthoringSession(session)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !created {
			existing, err := s.loadAuthoringSession(session.ID)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					writeError(w, http.StatusConflict, "idempotent authoring-session save raced with a deletion; retry the request")
					return
				}
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			if !authoringSessionCreateIdempotencyMatches(existing, req.IdempotencyKey, requestHash) {
				writeError(w, http.StatusConflict, "idempotency_key was already used for a different authoring-session request")
				return
			}
			enrichAuthoringSession(existing)
			s.recordAuditEvent(r, "script.authoring_session.create", "replayed", "authoring_session", existing.ID, existing.ProjectID, map[string]any{"name": existing.Name, "source": existing.Source})
			writeJSON(w, http.StatusOK, maskAuthoringSession(existing))
			return
		}
	}
	enrichAuthoringSession(session)
	s.recordAuditEvent(r, "script.authoring_session.create", "success", "authoring_session", session.ID, session.ProjectID, map[string]any{"name": session.Name, "source": session.Source})
	writeJSON(w, http.StatusCreated, maskAuthoringSession(session))
}

func (s *Server) handleListAuthoringSessions(w http.ResponseWriter, r *http.Request) {
	projectID := r.URL.Query().Get("project_id")
	limit, offset := parseLimitOffsetQuery(r, defaultAuthoringSessionListLimit)
	if limit > maxAuthoringSessionListLimit {
		limit = maxAuthoringSessionListLimit
	}
	sessions, err := s.listAuthoringSessionsPage(projectID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	enrichAuthoringSessions(sessions)
	writeJSON(w, http.StatusOK, map[string]any{"sessions": maskAuthoringSessions(sessions), "count": len(sessions), "limit": limit, "offset": offset})
}

func (s *Server) handleGetAuthoringSession(w http.ResponseWriter, r *http.Request) {
	session, ok := s.getAuthoringSession(w, r.PathValue("id"))
	if !ok {
		return
	}
	enrichAuthoringSession(session)
	writeJSON(w, http.StatusOK, maskAuthoringSession(session))
}

func (s *Server) handleUpdateAuthoringSession(w http.ResponseWriter, r *http.Request) {
	s.authoringMutationMu.Lock()
	defer s.authoringMutationMu.Unlock()
	id := r.PathValue("id")
	existing, ok := s.getAuthoringSession(w, id)
	if !ok {
		return
	}
	if authoringSessionStatusEqual(existing.Status, "approved") {
		writeError(w, http.StatusConflict, "approved authoring sessions are immutable")
		return
	}
	if authoringSessionStatusEqual(existing.Status, "approving") {
		writeError(w, http.StatusConflict, "authoring session approval is in progress")
		return
	}
	expectedUpdatedAt := existing.UpdatedAt
	expectedStatus := existing.Status
	req, ok := readAuthoringSessionRequest(w, r)
	if !ok {
		return
	}
	if err := validateClientAuthoringSessionStatus(req.Status); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := bindStoredScriptProject(&req.Draft, existing.ProjectID, req.ProjectID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	safeDraft, ok := prepareAuthoringSessionDraft(w, &req.Draft, existing.Draft)
	if !ok {
		return
	}
	req.Draft = *safeDraft
	nextSource := normalizeAuthoringSessionSource(firstNonEmpty(req.Source, existing.Source))
	if importedJavaVuserAuthoringSource(existing.Source) {
		// The session's imported origin is server-persisted review context. Do
		// not let draft updates relabel it as manual; otherwise a Java draft
		// could temporarily change protocol, discard import evidence, then
		// change back and bypass the fidelity/source-review gate.
		nextSource = existing.Source
	}
	markImportedJavaVuserAuthoringDraft(&req.Draft, nextSource)
	existing.Source = nextSource
	existing.Name = authoringSessionName(firstNonEmpty(req.Name, existing.Name), &req.Draft)
	existing.Status = normalizeAuthoringSessionStatus(firstNonEmpty(req.Status, existing.Status))
	existing.Draft = &req.Draft
	existing.ReviewState = preserveAuthoringSessionCreateIdempotency(existing.ReviewState, cloneReviewState(req.ReviewState))
	existing.UpdatedAt = time.Now()
	existing.LastAccessedAt = existing.UpdatedAt
	saved, err := s.compareAndSwapAuthoringSession(existing, expectedUpdatedAt, expectedStatus)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !saved {
		writeError(w, http.StatusConflict, "authoring session changed while it was being updated")
		return
	}
	enrichAuthoringSession(existing)
	s.recordAuditEvent(r, "script.authoring_session.update", "success", "authoring_session", existing.ID, existing.ProjectID, map[string]any{"name": existing.Name, "source": existing.Source, "status": existing.Status})
	writeJSON(w, http.StatusOK, maskAuthoringSession(existing))
}

func (s *Server) handleDeleteAuthoringSession(w http.ResponseWriter, r *http.Request) {
	s.authoringMutationMu.Lock()
	defer s.authoringMutationMu.Unlock()
	id := r.PathValue("id")
	session, ok := s.getAuthoringSession(w, id)
	if !ok {
		return
	}
	if authoringSessionStatusEqual(session.Status, "approving") {
		writeError(w, http.StatusConflict, "authoring session approval is in progress")
		return
	}
	if authoringSessionStatusEqual(session.Status, "approved") {
		writeError(w, http.StatusConflict, "approved authoring sessions are immutable and retained for approval verification")
		return
	}
	deleted, err := s.deleteAuthoringSessionUnlessApproving(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !deleted {
		writeError(w, http.StatusConflict, "authoring session changed while it was being deleted")
		return
	}
	s.recordAuditEvent(r, "script.authoring_session.delete", "success", "authoring_session", id, session.ProjectID, map[string]any{"name": session.Name, "source": session.Source})
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
}

func (s *Server) handleApproveAuthoringSession(w http.ResponseWriter, r *http.Request) {
	s.authoringMutationMu.Lock()
	defer s.authoringMutationMu.Unlock()
	id := r.PathValue("id")
	session, ok := s.getAuthoringSession(w, id)
	if !ok {
		return
	}
	if session.Draft == nil {
		writeError(w, http.StatusBadRequest, "authoring session draft is required")
		return
	}
	if err := bindStoredScriptProject(session.Draft, session.ProjectID, ""); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if authoringSessionStatusEqual(session.Status, "approved") {
		scriptID, _ := session.ReviewState["approved_script_id"].(string)
		if strings.TrimSpace(scriptID) == "" {
			scriptID = authoringApprovalScriptID(session.ID)
		}
		stored, found, err := s.runner.GetScriptDurable(scriptID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "load approved script: "+err.Error())
			return
		}
		if !found || stored == nil || normalizeProjectID(stored.ProjectID) != normalizeProjectID(session.ProjectID) {
			writeError(w, http.StatusConflict, "approved authoring session has no matching library script")
			return
		}
		if scriptRequiresJavaVuserAuthoringApproval(session.Draft) {
			// The deterministic approval ID may become reusable if the approved
			// script is later deleted. Never let an unrelated replacement at that
			// ID satisfy an idempotent approval retry: the stored Java approval
			// envelope must still hash to this immutable approved session.
			if conflict := s.runner.javaVuserApprovalConflict(stored.Script); conflict != "" {
				writeError(w, http.StatusConflict, "approved authoring session no longer matches its Java Vuser library script: "+conflict)
				return
			}
		}
		validation := buildScriptValidationResponse("", session.Draft)
		enrichAuthoringSession(session)
		writeJSON(w, http.StatusOK, authoringSessionApprovalResponse{Session: maskAuthoringSession(session), Script: maskStoredScript(stored), AuthoringHealth: validation.AuthoringHealth})
		return
	}
	safeDraft, ok := prepareAuthoringSessionDraft(w, session.Draft, nil)
	if !ok {
		return
	}
	session.Draft = safeDraft

	validation := buildScriptValidationResponse("", session.Draft)
	if !validation.Valid || validation.AuthoringHealth.Status == "blocked" {
		expectedUpdatedAt := session.UpdatedAt
		expectedStatus := session.Status
		updateAuthoringSessionReviewReadiness(session, validation.AuthoringHealth)
		saved, err := s.compareAndSwapAuthoringSession(session, expectedUpdatedAt, expectedStatus)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to persist authoring review state: "+err.Error())
			return
		}
		if !saved {
			writeError(w, http.StatusConflict, "authoring session changed while review state was being persisted")
			return
		}
		s.recordAuditEvent(r, "script.authoring_session.approve", "failure", "authoring_session", session.ID, session.ProjectID, map[string]any{
			"health_status": validation.AuthoringHealth.Status,
			"next_actions":  session.NextActions,
			"issues":        validation.Issues,
		})
		writeJSON(w, http.StatusBadRequest, validation)
		return
	}

	// Persist an idempotent approval intent before writing the script library.
	// If the process stops between the two durable writes, a retry reuses the
	// same script ID and completes the session instead of creating a duplicate.
	// Deriving the ID from the durable session also makes concurrent approvals
	// from separate controller replicas converge on one library record.
	// A draft ID is client-controlled and may name an unrelated library script.
	// Approval always promotes to the ID owned by this durable session, so neither
	// retries nor two different sessions can overwrite another script.
	session.Draft.ID = authoringApprovalScriptID(session.ID)
	if scriptRequiresJavaVuserAuthoringApproval(session.Draft) {
		// createScriptDurable applies this storage default after approval. Apply it
		// before hashing as well so an optional blank version cannot make the
		// newly-created Java script's approval evidence stale immediately.
		if strings.TrimSpace(session.Draft.Version) == "" {
			session.Draft.Version = "1"
		}
		stampJavaVuserAuthoringApproval(session.Draft, session.ID)
	}
	previousStatus := session.Status
	previousReviewState := cloneReviewState(session.ReviewState)
	previousUpdatedAt := session.UpdatedAt
	previousLastAccessedAt := session.LastAccessedAt
	expectedUpdatedAt := session.UpdatedAt
	expectedStatus := session.Status
	now := time.Now()
	session.Status = "approving"
	session.UpdatedAt = now
	session.LastAccessedAt = now
	if session.ReviewState == nil {
		session.ReviewState = map[string]any{}
	}
	session.ReviewState["pending_script_id"] = session.Draft.ID
	session.ReviewState["approval_started_at"] = now.Format(time.RFC3339Nano)
	claimed, err := s.compareAndSwapAuthoringSession(session, expectedUpdatedAt, expectedStatus)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to persist authoring approval intent: "+err.Error())
		return
	}
	if !claimed {
		writeError(w, http.StatusConflict, "authoring session changed while approval was starting")
		return
	}
	intentUpdatedAt := session.UpdatedAt
	stored, err := s.runner.createAuthoringApprovedScriptDurable(session.Draft)
	if err != nil {
		session.Status = previousStatus
		session.ReviewState = previousReviewState
		session.UpdatedAt = previousUpdatedAt
		session.LastAccessedAt = previousLastAccessedAt
		rolledBack, rollbackErr := s.compareAndSwapAuthoringSession(session, intentUpdatedAt, "approving")
		if rollbackErr != nil {
			err = errors.Join(err, fmt.Errorf("rollback approval intent: %w", rollbackErr))
		} else if !rolledBack {
			err = errors.Join(err, errors.New("approval intent changed before rollback"))
		}
		writeError(w, http.StatusInternalServerError, "failed to save approved script: "+err.Error())
		return
	}
	now = time.Now()
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
	delete(session.ReviewState, "pending_script_id")
	delete(session.ReviewState, "approval_started_at")
	updateAuthoringSessionReviewReadiness(session, validation.AuthoringHealth)
	finalized, err := s.compareAndSwapAuthoringSession(session, intentUpdatedAt, "approving")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to finalize authoring approval; retry will resume the durable approval intent: "+err.Error())
		return
	}
	if !finalized {
		writeError(w, http.StatusConflict, "authoring session changed while approval was finalizing")
		return
	}
	enrichAuthoringSession(session)

	s.recordAuditEvent(r, "script.authoring_session.approve", "success", "authoring_session", session.ID, session.ProjectID, map[string]any{"script_id": stored.ID, "health_status": validation.AuthoringHealth.Status})
	writeJSON(w, http.StatusOK, authoringSessionApprovalResponse{
		Session:         maskAuthoringSession(session),
		Script:          maskStoredScript(stored),
		AuthoringHealth: validation.AuthoringHealth,
	})
}

func authoringApprovalScriptID(sessionID string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("apexload:authoring-session:"+strings.TrimSpace(sessionID))).String()
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
	if authoringSessionStatusEqual(session.Status, "approved") {
		session.ApprovalReady = false
		if len(session.NextActions) == 0 {
			session.NextActions = []string{"Session is already approved and promoted to a stored script."}
		}
		return
	}
	if authoringSessionStatusEqual(session.Status, "approving") {
		session.ApprovalReady = false
		session.NextActions = []string{"Retry approval to finish the durable script promotion."}
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
	if err := prepareAuthoringSessionForPersistence(session); err != nil {
		return err
	}
	if store, ok := s.store.(authoringSessionPersistence); ok {
		return store.SaveAuthoringSession(session)
	}
	s.authoringMu.Lock()
	defer s.authoringMu.Unlock()
	s.authoring[session.ID] = cloneAuthoringSession(session)
	return nil
}

func (s *Server) createAuthoringSession(session *storage.AuthoringSession) (bool, error) {
	if err := prepareAuthoringSessionForPersistence(session); err != nil {
		return false, err
	}
	if store, ok := s.store.(authoringSessionCreatePersistence); ok {
		return store.CreateAuthoringSession(session)
	}
	s.authoringMu.Lock()
	defer s.authoringMu.Unlock()
	if s.authoring[session.ID] != nil {
		return false, nil
	}
	s.authoring[session.ID] = cloneAuthoringSession(session)
	return true, nil
}

func (s *Server) compareAndSwapAuthoringSession(session *storage.AuthoringSession, expectedUpdatedAt time.Time, expectedStatus string) (bool, error) {
	if err := prepareAuthoringSessionForPersistence(session); err != nil {
		return false, err
	}
	if store, ok := s.store.(authoringSessionCASPersistence); ok {
		return store.CompareAndSwapAuthoringSession(session, expectedUpdatedAt, expectedStatus)
	}
	// Mutating handlers hold authoringMutationMu, so the in-memory fallback can
	// perform the same version/status check without another lock spanning I/O.
	s.authoringMu.Lock()
	defer s.authoringMu.Unlock()
	current := s.authoring[session.ID]
	if current == nil || current.Status != expectedStatus || !current.UpdatedAt.Equal(expectedUpdatedAt) {
		return false, nil
	}
	now := time.Now()
	session.UpdatedAt = now
	if session.LastAccessedAt.IsZero() {
		session.LastAccessedAt = now
	}
	s.authoring[session.ID] = cloneAuthoringSession(session)
	return true, nil
}

func prepareAuthoringSessionForPersistence(session *storage.AuthoringSession) error {
	if session == nil {
		return nil
	}
	session.ReviewState = projectAuthoringReviewState(session.ReviewState)
	if session.Draft == nil {
		return nil
	}
	projected, err := preparePersistentLibraryScript(session.Draft, nil)
	if err != nil {
		return err
	}
	session.Draft = projected
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
	if accessStore, ok := s.store.(authoringSessionAccessPersistence); ok {
		accessedAt := authoringSessionTimestampForAPI(time.Now())
		touched, err := accessStore.TouchAuthoringSessionAccess(session.ID, accessedAt)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return nil, false
		}
		if !touched {
			writeError(w, http.StatusNotFound, "authoring session not found")
			return nil, false
		}
		session.LastAccessedAt = accessedAt
	}
	return session, true
}

func authoringSessionTimestampForAPI(value time.Time) time.Time {
	return value.UTC().Truncate(time.Microsecond)
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

func (s *Server) listAuthoringSessionsPage(projectID string, limit, offset int) ([]*storage.AuthoringSession, error) {
	if store, ok := s.store.(interface {
		ListAuthoringSessionsPage(string, int, int) ([]*storage.AuthoringSession, error)
	}); ok {
		return store.ListAuthoringSessionsPage(projectID, limit, offset)
	}
	sessions, err := s.listAuthoringSessions(projectID)
	if err != nil {
		return nil, err
	}
	if offset >= len(sessions) {
		return []*storage.AuthoringSession{}, nil
	}
	end := len(sessions)
	if limit < len(sessions)-offset {
		end = offset + limit
	}
	return sessions[offset:end], nil
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

func (s *Server) deleteAuthoringSessionUnlessApproving(id string) (bool, error) {
	if store, ok := s.store.(authoringSessionCASPersistence); ok {
		return store.DeleteAuthoringSessionUnlessApproving(id)
	}
	s.authoringMu.Lock()
	defer s.authoringMu.Unlock()
	session := s.authoring[id]
	if session == nil ||
		authoringSessionStatusEqual(session.Status, "approving") ||
		authoringSessionStatusEqual(session.Status, "approved") {
		return false, nil
	}
	delete(s.authoring, id)
	return true, nil
}

func normalizeAuthoringSessionSource(source string) string {
	return strings.ToLower(strings.TrimSpace(source))
}

func authoringSessionIdempotentID(projectID, idempotencyKey string) string {
	scope := normalizeProjectID(projectID) + "\x00" + strings.TrimSpace(idempotencyKey)
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("apexload:authoring-session-create:"+scope)).String()
}

func authoringSessionCreateRequestHash(session *storage.AuthoringSession) string {
	if session == nil {
		return ""
	}
	reviewState := cloneReviewState(session.ReviewState)
	delete(reviewState, authoringSessionIdempotencyReviewKey)
	payload, err := json.Marshal(struct {
		ProjectID   string         `json:"project_id"`
		Source      string         `json:"source"`
		Name        string         `json:"name"`
		Status      string         `json:"status"`
		Draft       *dsl.Script    `json:"draft"`
		ReviewState map[string]any `json:"review_state,omitempty"`
	}{
		ProjectID:   normalizeProjectID(session.ProjectID),
		Source:      normalizeAuthoringSessionSource(session.Source),
		Name:        strings.TrimSpace(session.Name),
		Status:      normalizeAuthoringSessionStatus(session.Status),
		Draft:       session.Draft,
		ReviewState: reviewState,
	})
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(payload)
	return fmt.Sprintf("%x", digest[:])
}

func authoringSessionIdempotencyKeyHash(idempotencyKey string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(idempotencyKey)))
	return fmt.Sprintf("%x", digest[:])
}

func stampAuthoringSessionCreateIdempotency(session *storage.AuthoringSession, idempotencyKey string) {
	if session == nil {
		return
	}
	requestHash := authoringSessionCreateRequestHash(session)
	if session.ReviewState == nil {
		session.ReviewState = map[string]any{}
	}
	session.ReviewState[authoringSessionIdempotencyReviewKey] = map[string]any{
		"key_hash":     authoringSessionIdempotencyKeyHash(idempotencyKey),
		"request_hash": requestHash,
	}
}

func authoringSessionCreateIdempotencyMatches(session *storage.AuthoringSession, idempotencyKey, requestHash string) bool {
	if session == nil || session.ReviewState == nil {
		return false
	}
	envelope, ok := session.ReviewState[authoringSessionIdempotencyReviewKey].(map[string]any)
	if !ok {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(fmt.Sprint(envelope["key_hash"])), authoringSessionIdempotencyKeyHash(idempotencyKey)) &&
		strings.EqualFold(strings.TrimSpace(fmt.Sprint(envelope["request_hash"])), strings.TrimSpace(requestHash))
}

func preserveAuthoringSessionCreateIdempotency(current, next map[string]any) map[string]any {
	delete(next, authoringSessionIdempotencyReviewKey)
	envelope, ok := current[authoringSessionIdempotencyReviewKey]
	if !ok {
		return next
	}
	if next == nil {
		next = map[string]any{}
	}
	next[authoringSessionIdempotencyReviewKey] = envelope
	return next
}

func importedJavaVuserAuthoringSource(source string) bool {
	switch normalizeAuthoringSessionSource(source) {
	case "enterprise_script_archive", "source_review_artifact":
		return true
	default:
		return false
	}
}

func markImportedJavaVuserAuthoringDraft(draft *dsl.Script, source string) {
	if draft == nil || !scriptRequiresJavaVuserAuthoringApproval(draft) || !importedJavaVuserAuthoringSource(source) {
		return
	}
	// GeneratedFrom is part of the approved content hash and survives storage.
	// Stamp it from the durable session source so removing client-editable
	// metadata.import evidence cannot turn an imported combined draft into an
	// apparently hand-authored one.
	draft.GeneratedFrom = "enterprise_script_archive"
	if draft.Metadata == nil {
		draft.Metadata = &dsl.ScriptMetadata{}
	}
	if draft.Metadata.Import == nil {
		draft.Metadata.Import = &dsl.ImportMetadata{}
	}
	if draft.Metadata.Import.Preserved == nil {
		draft.Metadata.Import.Preserved = map[string]any{}
	}
	// This server-owned marker distinguishes new combined-import authoring
	// sessions from already-saved legacy full_lifecycle scripts. Re-stamping it
	// on every imported-session update prevents changing the mutable method
	// field to full_lifecycle from discarding the combined-review requirement.
	draft.Metadata.Import.Preserved[javaVuserCombinedImportReviewKey] = true
}

func normalizeAuthoringSessionStatus(status string) string {
	status = strings.ToLower(strings.TrimSpace(status))
	switch status {
	case "reviewing", "editing", "blocked", "ready", "approving", "approved":
		return status
	default:
		return "editing"
	}
}

func authoringSessionStatusEqual(status, want string) bool {
	return strings.EqualFold(strings.TrimSpace(status), strings.TrimSpace(want))
}

func validateClientAuthoringSessionStatus(status string) error {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "approving", "approved":
		return fmt.Errorf("authoring session status %q is managed by the approval workflow", strings.TrimSpace(status))
	default:
		return nil
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
