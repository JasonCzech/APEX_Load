package api

// Scenario drafts: scenario-builder progress saved to a project before it is a
// complete scenario. The builder saves one Save: a complete scenario as a
// scenario, anything else as a draft here. A draft is the builder's own
// document, stored as given; it is never a scenario, so no run, plan or
// schedule can launch it. Drafts are visible to everyone in the project, so a
// document carrying inline credentials is refused.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	// scenarioDraftMaxDocumentBytes matches the builder's own draft limit.
	scenarioDraftMaxDocumentBytes = 2_000_000
	scenarioDraftMaxBodyBytes     = scenarioDraftMaxDocumentBytes + 64*1024
	scenarioDraftMaxNameRunes     = 200
	scenarioDraftUntitledName     = "Untitled scenario"
)

type scenarioDraftStore interface {
	CreateScenarioDraft(draft *storage.ScenarioDraft) error
	UpdateScenarioDraftIfRevision(draft *storage.ScenarioDraft, expectedRevision int64) (bool, error)
	GetScenarioDraft(projectID, id string) (*storage.ScenarioDraft, error)
	ListScenarioDrafts(projectID string) ([]*storage.ScenarioDraft, error)
	DeleteScenarioDraft(projectID, id string, expectedRevision int64) (bool, error)
}

// ScenarioDraftSummary is a draft as the library lists it.
type ScenarioDraftSummary struct {
	storage.ScenarioDraft
	// ScenarioName is the scenario an edit draft changes, empty when that
	// scenario no longer exists (the draft then resumes as a new scenario).
	ScenarioName string `json:"scenario_name,omitempty"`
}

// ScenarioDraftResponse is a draft with its document.
type ScenarioDraftResponse struct {
	ScenarioDraftSummary
	Document json.RawMessage `json:"document"`
}

type ScenarioDraftListResponse struct {
	Drafts []ScenarioDraftSummary `json:"drafts"`
	Count  int                    `json:"count"`
}

type saveScenarioDraftRequest struct {
	Name       string          `json:"name"`
	ScenarioID string          `json:"scenario_id,omitempty"`
	Document   json.RawMessage `json:"document"`
	// Revision is the draft's revision the save was made from (updates only).
	Revision int64 `json:"revision,omitempty"`
}

func (s *Server) scenarioDraftStore(w http.ResponseWriter) (scenarioDraftStore, bool) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return nil, false
	}
	store, ok := s.store.(scenarioDraftStore)
	if !ok {
		writeError(w, http.StatusNotImplemented, "scenario drafts are not supported by this storage backend")
		return nil, false
	}
	return store, true
}

func (s *Server) scenarioDraftSummary(draft *storage.ScenarioDraft) ScenarioDraftSummary {
	summary := ScenarioDraftSummary{ScenarioDraft: *draft}
	if draft.ScenarioID != "" {
		if scenario, ok := s.runner.GetScenario(draft.ScenarioID); ok && scenario != nil && normalizeProjectID(scenario.ProjectID) == normalizeProjectID(draft.ProjectID) {
			summary.ScenarioName = scenario.Name
		}
	}
	return summary
}

func (s *Server) handleListScenarioDrafts(w http.ResponseWriter, r *http.Request) {
	store, ok := s.scenarioDraftStore(w)
	if !ok {
		return
	}
	drafts, err := store.ListScenarioDrafts(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list scenario drafts: "+err.Error())
		return
	}
	response := ScenarioDraftListResponse{Drafts: make([]ScenarioDraftSummary, 0, len(drafts))}
	for _, draft := range drafts {
		response.Drafts = append(response.Drafts, s.scenarioDraftSummary(draft))
	}
	response.Count = len(response.Drafts)
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleGetScenarioDraft(w http.ResponseWriter, r *http.Request) {
	store, ok := s.scenarioDraftStore(w)
	if !ok {
		return
	}
	draft, err := store.GetScenarioDraft(r.PathValue("id"), r.PathValue("draftID"))
	if errors.Is(err, storage.ErrScenarioDraftNotFound) {
		writeError(w, http.StatusNotFound, "scenario draft not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read the scenario draft: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, ScenarioDraftResponse{ScenarioDraftSummary: s.scenarioDraftSummary(draft), Document: json.RawMessage(draft.Document)})
}

// scenarioDraftFromRequest validates a save and returns the draft to store.
func (s *Server) scenarioDraftFromRequest(r *http.Request, projectID string, req saveScenarioDraftRequest) (*storage.ScenarioDraft, string) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = scenarioDraftUntitledName
	}
	if utf8.RuneCountInString(name) > scenarioDraftMaxNameRunes {
		return nil, fmt.Sprintf("name must be at most %d characters", scenarioDraftMaxNameRunes)
	}
	document := bytes.TrimSpace(req.Document)
	if len(document) == 0 || document[0] != '{' {
		return nil, "document must be a JSON object"
	}
	if len(document) > scenarioDraftMaxDocumentBytes {
		return nil, fmt.Sprintf("document exceeds the %d byte draft limit", scenarioDraftMaxDocumentBytes)
	}
	var parsed any
	if err := json.Unmarshal(document, &parsed); err != nil {
		return nil, "document must be a JSON object"
	}
	if problem := scenarioDraftInlineCredential(parsed); problem != "" {
		return nil, problem
	}
	scenarioID := strings.TrimSpace(req.ScenarioID)
	if scenarioID != "" {
		scenario, found := s.runner.GetScenario(scenarioID)
		switch {
		case !found || scenario == nil:
			// Deleted since the edit began: the work is kept as a new scenario's draft.
			scenarioID = ""
		case normalizeProjectID(scenario.ProjectID) != normalizeProjectID(projectID):
			return nil, "scenario_id must name a scenario in this project"
		default:
			scenarioID = scenario.ID
		}
	}
	_, _, actorName := s.resolveAuditActor(r)
	return &storage.ScenarioDraft{ProjectID: projectID, ScenarioID: scenarioID, Name: name, Document: string(document), CreatedBy: actorName, UpdatedBy: actorName}, ""
}

func (s *Server) handleCreateScenarioDraft(w http.ResponseWriter, r *http.Request) {
	store, ok := s.scenarioDraftStore(w)
	if !ok {
		return
	}
	var req saveScenarioDraftRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	projectID := normalizeProjectID(r.PathValue("id"))
	draft, problem := s.scenarioDraftFromRequest(r, projectID, req)
	if problem != "" {
		writeError(w, http.StatusBadRequest, problem)
		return
	}
	draft.ID = "scenario-draft-" + uuid.New().String()
	if err := store.CreateScenarioDraft(draft); errors.Is(err, storage.ErrScenarioDraftProjectNotFound) {
		writeError(w, http.StatusNotFound, "project not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save the scenario draft: "+err.Error())
		return
	}
	s.recordAuditEvent(r, "scenario_draft.create", "success", "scenario_draft", draft.ID, draft.ProjectID, map[string]any{"name": draft.Name, "scenario_id": draft.ScenarioID})
	writeJSON(w, http.StatusCreated, ScenarioDraftResponse{ScenarioDraftSummary: s.scenarioDraftSummary(draft), Document: json.RawMessage(draft.Document)})
}

func (s *Server) handleUpdateScenarioDraft(w http.ResponseWriter, r *http.Request) {
	store, ok := s.scenarioDraftStore(w)
	if !ok {
		return
	}
	var req saveScenarioDraftRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if req.Revision < 1 {
		writeError(w, http.StatusBadRequest, "revision is required: the draft revision this save was made from")
		return
	}
	projectID := normalizeProjectID(r.PathValue("id"))
	draft, problem := s.scenarioDraftFromRequest(r, projectID, req)
	if problem != "" {
		writeError(w, http.StatusBadRequest, problem)
		return
	}
	draft.ID = r.PathValue("draftID")
	updated, err := store.UpdateScenarioDraftIfRevision(draft, req.Revision)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save the scenario draft: "+err.Error())
		return
	}
	if !updated {
		if _, readErr := store.GetScenarioDraft(projectID, draft.ID); errors.Is(readErr, storage.ErrScenarioDraftNotFound) {
			writeError(w, http.StatusNotFound, "scenario draft not found")
			return
		} else if readErr != nil {
			writeError(w, http.StatusInternalServerError, "failed to read the scenario draft: "+readErr.Error())
			return
		}
		writeError(w, http.StatusConflict, "the draft was saved by someone else since you opened it; reload it before saving")
		return
	}
	s.recordAuditEvent(r, "scenario_draft.update", "success", "scenario_draft", draft.ID, draft.ProjectID, map[string]any{"name": draft.Name, "revision": draft.Revision})
	writeJSON(w, http.StatusOK, ScenarioDraftResponse{ScenarioDraftSummary: s.scenarioDraftSummary(draft), Document: json.RawMessage(draft.Document)})
}

func (s *Server) handleDeleteScenarioDraft(w http.ResponseWriter, r *http.Request) {
	store, ok := s.scenarioDraftStore(w)
	if !ok {
		return
	}
	projectID := normalizeProjectID(r.PathValue("id"))
	id := r.PathValue("draftID")
	// ?revision= deletes it only if nobody has saved it since that revision.
	var revision int64
	if raw := strings.TrimSpace(r.URL.Query().Get("revision")); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 1 {
			writeError(w, http.StatusBadRequest, "revision must be a positive integer")
			return
		}
		revision = parsed
	}
	deleted, err := store.DeleteScenarioDraft(projectID, id, revision)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete the scenario draft: "+err.Error())
		return
	}
	if !deleted {
		if revision > 0 {
			if _, readErr := store.GetScenarioDraft(projectID, id); readErr == nil {
				writeError(w, http.StatusConflict, "the draft was saved by someone else since that revision; it was kept")
				return
			} else if !errors.Is(readErr, storage.ErrScenarioDraftNotFound) {
				writeError(w, http.StatusInternalServerError, "failed to read the scenario draft: "+readErr.Error())
				return
			}
		}
		writeError(w, http.StatusNotFound, "scenario draft not found")
		return
	}
	s.recordAuditEvent(r, "scenario_draft.delete", "success", "scenario_draft", id, projectID, nil)
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
}

var (
	scenarioDraftCredentialField = regexp.MustCompile(`(?i)^(env|headers|extra_http_headers|cookies|storage_state|additional_vm_parameters|password|token|authorization|client_secret|private_key)$`)
	// A reference may carry the spacing the runtime accepts ({{ secret.NAME }}).
	scenarioDraftSecretReference = regexp.MustCompile(`^\s*\{\{\s*secret\.[\w.-]+\s*\}\}\s*$`)
)

// scenarioDraftInlineCredential mirrors the builder's own refusal
// (assertDraftHasNoInlineCredentials): credential-bearing fields may hold only
// project-secret references, and runtime URLs no user info or query. It says
// why a document is refused, or "" when it may be stored.
func scenarioDraftInlineCredential(value any) string {
	stack := []any{value}
	for visited := 0; len(stack) > 0; visited++ {
		if visited > 100_000 {
			return "document is too large to check"
		}
		item := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		switch typed := item.(type) {
		case []any:
			stack = append(stack, typed...)
		case map[string]any:
			for name, child := range typed {
				if child == nil || child == "" {
					continue
				}
				text, isString := child.(string)
				if scenarioDraftCredentialField.MatchString(name) && !(isString && scenarioDraftSecretReference.MatchString(text)) {
					if object, isObject := child.(map[string]any); isObject {
						if len(object) == 0 || (strings.EqualFold(name, "env") && onlySecretReferences(object)) {
							continue
						}
					}
					if list, isList := child.([]any); isList && len(list) == 0 {
						continue
					}
					return "drafts are visible to the whole project, so they cannot hold inline runtime credentials or environment values; use project-secret references"
				}
				if isString && (strings.HasPrefix(strings.ToLower(text), "http://") || strings.HasPrefix(strings.ToLower(text), "https://")) {
					if parsed, err := url.Parse(text); err == nil && (parsed.User != nil || parsed.RawQuery != "") {
						return "remove credentials and query values from runtime URLs before saving a draft"
					}
				}
				switch child.(type) {
				case map[string]any, []any:
					stack = append(stack, child)
				}
			}
		}
	}
	return ""
}

func onlySecretReferences(values map[string]any) bool {
	for _, value := range values {
		text, ok := value.(string)
		if !ok || !scenarioDraftSecretReference.MatchString(text) {
			return false
		}
	}
	return true
}
