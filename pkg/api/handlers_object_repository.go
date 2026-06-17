package api

// Object repositories: project-scoped, reusable catalogs of named UI element
// descriptors (the LoadRunner/TruClient object-repository analog). Scripts
// reference objects by logical name so selectors are maintained centrally and
// shared/healed across scripts. Objects are stored inline (single-table family).

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/selector"
	"github.com/jasonczech/apexload/pkg/storage"
)

const defaultObjectRepositoryListLimit = 100

func validateObjectRepository(repo *storage.ObjectRepository) (int, string) {
	if strings.TrimSpace(repo.Name) == "" {
		return http.StatusBadRequest, "object repository name is required"
	}
	for i := range repo.Objects {
		if strings.TrimSpace(repo.Objects[i].LogicalName) == "" {
			return http.StatusBadRequest, "every object requires a logical_name"
		}
	}
	return 0, ""
}

func (s *Server) loadObjectRepository(w http.ResponseWriter, r *http.Request) (*storage.ObjectRepository, bool) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return nil, false
	}
	repo, err := s.store.GetObjectRepository(r.PathValue("id"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "object repository not found")
		} else {
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return nil, false
	}
	return repo, true
}

func (s *Server) handleCreateObjectRepository(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	var repo storage.ObjectRepository
	if !decodeJSONBody(w, r, &repo) {
		return
	}
	if status, msg := validateObjectRepository(&repo); status != 0 {
		writeError(w, status, msg)
		return
	}
	repo.ID = "object-repo-" + uuid.New().String()
	repo.ProjectID = normalizeProjectID(repo.ProjectID)
	if err := s.store.SaveObjectRepository(&repo); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAuditEvent(r, "object_repository.create", "success", "object_repository", repo.ID, repo.ProjectID, map[string]any{
		"name": repo.Name, "objects": len(repo.Objects),
	})
	writeJSON(w, http.StatusCreated, repo)
}

func (s *Server) handleListObjectRepositories(w http.ResponseWriter, r *http.Request) {
	limit, offset := parseLimitOffsetQuery(r, defaultObjectRepositoryListLimit)
	if s.store == nil {
		writeJSON(w, http.StatusOK, map[string]any{"object_repositories": []*storage.ObjectRepository{}, "count": 0, "limit": limit, "offset": offset})
		return
	}
	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	repos, err := s.store.ListObjectRepositories(projectID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if repos == nil {
		repos = []*storage.ObjectRepository{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"object_repositories": repos, "count": len(repos), "limit": limit, "offset": offset})
}

func (s *Server) handleGetObjectRepository(w http.ResponseWriter, r *http.Request) {
	repo, ok := s.loadObjectRepository(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, repo)
}

func (s *Server) handleUpdateObjectRepository(w http.ResponseWriter, r *http.Request) {
	existing, ok := s.loadObjectRepository(w, r)
	if !ok {
		return
	}
	var req storage.ObjectRepository
	if !decodeJSONBody(w, r, &req) {
		return
	}
	req.ID = existing.ID
	req.ProjectID = existing.ProjectID
	req.CreatedAt = existing.CreatedAt
	if status, msg := validateObjectRepository(&req); status != 0 {
		writeError(w, status, msg)
		return
	}
	if err := s.store.SaveObjectRepository(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAuditEvent(r, "object_repository.update", "success", "object_repository", req.ID, req.ProjectID, map[string]any{"objects": len(req.Objects)})
	writeJSON(w, http.StatusOK, req)
}

func (s *Server) handleDeleteObjectRepository(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	id := r.PathValue("id")
	projectID := ""
	if repo, err := s.store.GetObjectRepository(id); err == nil && repo != nil {
		projectID = repo.ProjectID
	}
	if err := s.store.DeleteObjectRepository(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAuditEvent(r, "object_repository.delete", "success", "object_repository", id, projectID, nil)
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
}

// handleObjectRepositoryFromScript seeds a new repository from a script's
// element selectors — the descriptors that today are captured at import then
// discarded. It reads each action's ranked selector candidates
// (selector.MetadataCandidatesKey) so the repository inherits the recorded /
// TruClient object-repository fidelity.
func (s *Server) handleObjectRepositoryFromScript(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	var req struct {
		ScriptID string `json:"script_id"`
		Name     string `json:"name,omitempty"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	scriptID := strings.TrimSpace(req.ScriptID)
	if scriptID == "" {
		writeError(w, http.StatusBadRequest, "script_id is required")
		return
	}
	stored, ok := s.runner.GetScript(scriptID)
	if !ok || stored == nil || stored.Script == nil {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	objects := objectRepositoryObjectsFromScript(stored.Script, scriptID)
	if len(objects) == 0 {
		writeError(w, http.StatusBadRequest, "script has no element selectors to import")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = strings.TrimSpace(stored.Name) + " objects"
	}
	repo := &storage.ObjectRepository{
		ID:        "object-repo-" + uuid.New().String(),
		ProjectID: normalizeProjectID(stored.ProjectID),
		Name:      name,
		Source:    "script:" + scriptID,
		Objects:   objects,
	}
	if err := s.store.SaveObjectRepository(repo); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAuditEvent(r, "object_repository.from_script", "success", "object_repository", repo.ID, repo.ProjectID, map[string]any{
		"name": repo.Name, "objects": len(repo.Objects), "source_script_id": scriptID,
	})
	writeJSON(w, http.StatusCreated, repo)
}

// objectRepositoryObjectsFromScript builds repository objects from a script's
// actions, one per uniquely-named element that carries a selector or ranked
// candidates. Reuses pkg/selector so ranking matches authoring/replay.
func objectRepositoryObjectsFromScript(script *dsl.Script, scriptID string) []storage.RepositoryObject {
	objects := make([]storage.RepositoryObject, 0)
	seen := map[string]bool{}
	collect := func(actions []dsl.Action) {
		for _, action := range actions {
			var candidates []selector.Candidate
			if action.Metadata != nil {
				candidates = selector.FromMetadataValue(action.Metadata[selector.MetadataCandidatesKey])
			}
			if len(candidates) == 0 && strings.TrimSpace(action.Selector) == "" {
				continue
			}
			logicalName := strings.TrimSpace(action.Name)
			if logicalName == "" {
				logicalName = strings.TrimSpace(action.Selector)
			}
			if logicalName == "" || seen[logicalName] {
				continue
			}
			seen[logicalName] = true
			obj := storage.RepositoryObject{
				LogicalName:    logicalName,
				CSS:            action.Selector,
				SourceScriptID: scriptID,
			}
			best := 0
			for _, candidate := range candidates {
				obj.Candidates = append(obj.Candidates, storage.RepositoryObjectCandidate{
					Kind: candidate.Kind, Value: candidate.Value, Confidence: candidate.Confidence, Reason: candidate.Reason,
				})
				if candidate.Confidence > best {
					best = candidate.Confidence
				}
				switch candidate.Kind {
				case "css":
					if obj.CSS == "" {
						obj.CSS = candidate.Value
					}
				case "xpath":
					if obj.XPath == "" {
						obj.XPath = candidate.Value
					}
				case "role":
					if obj.Role == "" {
						obj.Role = candidate.Value
					}
				case "text":
					if obj.Text == "" {
						obj.Text = candidate.Value
					}
				}
			}
			obj.BestConfidence = best
			objects = append(objects, obj)
		}
	}
	collect(script.Init)
	collect(script.Actions)
	collect(script.Teardown)
	return objects
}
