package api

// Object repositories: project-scoped, reusable catalogs of named UI element
// descriptors (the LoadRunner/TruClient object-repository analog). Scripts
// reference objects by logical name so selectors are maintained centrally and
// shared/healed across scripts. Objects are stored inline (single-table family).

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

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
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	var req storage.ObjectRepository
	if err := json.Unmarshal(raw, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	req.ID = existing.ID
	req.ProjectID = existing.ProjectID
	req.CreatedAt = existing.CreatedAt
	// Source is immutable provenance, not an editor-owned field.
	req.Source = existing.Source
	// The update replaces editor-owned fields, so any field the caller omits
	// would be reset to its zero value. Preserve omitted optional fields from the
	// existing record: `auto_promote_on_heal` (a heal preference toggled from a
	// separate control).
	// This stops a partial update — e.g. a candidate promotion that sends only
	// name/description/objects — from silently wiping them, while an explicit
	// toggle (which does send the key) still takes effect.
	var present map[string]json.RawMessage
	if json.Unmarshal(raw, &present) == nil {
		if _, sent := present["auto_promote_on_heal"]; !sent {
			req.AutoPromoteOnHeal = existing.AutoPromoteOnHeal
		}
	}
	if status, msg := validateObjectRepository(&req); status != 0 {
		writeError(w, status, msg)
		return
	}
	updater, supported := s.store.(interface {
		UpdateObjectRepositoryIfUnchanged(repo *storage.ObjectRepository, expectedUpdatedAt time.Time) (bool, error)
	})
	if !supported {
		writeError(w, http.StatusNotImplemented, "optimistic object repository updates are not supported by this storage backend")
		return
	}
	updated, err := updater.UpdateObjectRepositoryIfUnchanged(&req, existing.UpdatedAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !updated {
		writeError(w, http.StatusConflict, "object repository changed while the update was in flight; reload and retry")
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
	repo, err := s.store.GetObjectRepository(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "object repository not found")
		} else {
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	deleted, err := s.deleteObjectRepositoryIfUnreferenced(repo)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !deleted {
		writeError(w, http.StatusConflict, "object repository changed or is referenced by one or more scripts")
		return
	}
	s.recordAuditEvent(r, "object_repository.delete", "success", "object_repository", id, repo.ProjectID, nil)
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
}

func (s *Server) deleteObjectRepositoryIfUnreferenced(repo *storage.ObjectRepository) (bool, error) {
	if repo == nil {
		return false, nil
	}
	if guarded, ok := s.store.(interface {
		DeleteObjectRepositoryIfUnreferencedAndUnchanged(string, time.Time) (bool, error)
	}); ok {
		return guarded.DeleteObjectRepositoryIfUnreferencedAndUnchanged(repo.ID, repo.UpdatedAt)
	}
	return false, errors.New("guarded object repository deletion is not supported by this storage backend")
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
		ScriptID   string `json:"script_id"`
		Name       string `json:"name,omitempty"`
		BindScript bool   `json:"bind_script,omitempty"`
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
	if !requireAuthorizedProjectMatch(w, r, stored.ProjectID) {
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
	// Optionally bind the source script's actions to the new repository so the
	// authoring loop (seed → bind → maintain centrally) closes in one call.
	if req.BindScript {
		bindCopy := cloneScriptDefinition(stored.Script)
		if bound := bindScriptToObjectRepository(bindCopy, repo); bound > 0 {
			_, ok, err := s.runner.UpdateScriptDurableInProject(scriptID, bindCopy, repo.ProjectID)
			if err != nil {
				cleaned, cleanupErr := s.deleteObjectRepositoryIfUnreferenced(repo)
				if cleanupErr == nil && !cleaned {
					cleanupErr = errors.New("repository was changed or adopted by another script")
				}
				if errors.Is(err, errScriptConcurrentChange) {
					if cleanupErr != nil {
						writeError(w, http.StatusInternalServerError, "script changed while repository bindings were in flight and repository cleanup failed: "+cleanupErr.Error())
						return
					}
					writeError(w, http.StatusConflict, err.Error())
					return
				}
				if cleanupErr != nil {
					writeError(w, http.StatusInternalServerError, "failed to save repository bindings and repository cleanup failed: "+cleanupErr.Error())
					return
				}
				writeError(w, http.StatusInternalServerError, "failed to save repository bindings: "+err.Error())
				return
			}
			if !ok {
				cleaned, cleanupErr := s.deleteObjectRepositoryIfUnreferenced(repo)
				if cleanupErr == nil && !cleaned {
					cleanupErr = errors.New("repository was changed or adopted by another script")
				}
				if cleanupErr != nil {
					writeError(w, http.StatusInternalServerError, "source script disappeared while repository bindings were in flight and repository cleanup failed: "+cleanupErr.Error())
					return
				}
				writeError(w, http.StatusConflict, "source script changed while repository bindings were in flight; reload and retry")
				return
			}
			s.recordAuditEvent(r, "object_repository.bind_script", "success", "script", scriptID, repo.ProjectID, map[string]any{
				"object_repository_id": repo.ID, "bound": bound,
			})
		}
	}
	s.recordAuditEvent(r, "object_repository.from_script", "success", "object_repository", repo.ID, repo.ProjectID, map[string]any{
		"name": repo.Name, "objects": len(repo.Objects), "source_script_id": scriptID,
	})

	writeJSON(w, http.StatusCreated, repo)
}

// bindScriptToObjectRepository sets object_ref on every action whose name
// matches a repository object (leaving any inline selector as a fallback) and
// returns the number of actions bound. The reference is qualified with the
// repository id so resolution and self-heal write-back are deterministic.
func bindScriptToObjectRepository(script *dsl.Script, repo *storage.ObjectRepository) int {
	canonical := map[string]string{}
	for _, obj := range repo.Objects {
		key := strings.ToLower(strings.TrimSpace(obj.LogicalName))
		if key != "" {
			canonical[key] = strings.TrimSpace(obj.LogicalName)
		}
	}
	bound := 0
	var walk func(actions []dsl.Action)
	walk = func(actions []dsl.Action) {
		for i := range actions {
			action := &actions[i]
			if strings.TrimSpace(action.ObjectRef) == "" {
				if name, ok := canonical[strings.ToLower(strings.TrimSpace(action.Name))]; ok {
					action.ObjectRef = repo.ID + ":" + name
					bound++
				}
			}
			if action.Control != nil {
				walk(action.Control.Steps)
				walk(action.Control.Else)
			}
		}
	}
	walk(script.Init)
	walk(script.Actions)
	walk(script.Teardown)
	return bound
}

// actionLocatorSignature is a stable fingerprint of an action's locator (inline
// selector plus ranked candidate kinds/values). Two actions with the same
// signature target the same element; differing signatures mean different elements.
func actionLocatorSignature(action dsl.Action) string {
	parts := []string{strings.TrimSpace(action.Selector)}
	if action.Metadata != nil {
		for _, c := range selector.FromMetadataValue(action.Metadata[selector.MetadataCandidatesKey]) {
			parts = append(parts, c.Kind+"="+c.Value)
		}
	}
	return strings.Join(parts, "|")
}

// ambiguousObjectNames returns the set of logical names that two distinct actions
// share with different locator signatures, scanning the same scope (top-level
// init/actions/teardown) that objectRepositoryObjectsFromScript collects from.
func ambiguousObjectNames(script *dsl.Script) map[string]bool {
	firstSig := map[string]string{}
	ambiguous := map[string]bool{}
	scan := func(actions []dsl.Action) {
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
			if logicalName == "" {
				continue
			}
			sig := actionLocatorSignature(action)
			if prev, ok := firstSig[logicalName]; ok {
				if prev != sig {
					ambiguous[logicalName] = true
				}
				continue
			}
			firstSig[logicalName] = sig
		}
	}
	scan(script.Init)
	scan(script.Actions)
	scan(script.Teardown)
	return ambiguous
}

// objectRepositoryObjectsFromScript builds repository objects from a script's
// actions, one per uniquely-named element that carries a selector or ranked
// candidates. Reuses pkg/selector so ranking matches authoring/replay.
func objectRepositoryObjectsFromScript(script *dsl.Script, scriptID string) []storage.RepositoryObject {
	objects := make([]storage.RepositoryObject, 0)
	seen := map[string]bool{}
	// De-dup keeps only the first action's selector for a given logical name, but
	// binding then points every same-named action at that one object. When two
	// distinct actions share a name with DIFFERENT locators (e.g. two "Click
	// Continue" buttons on different pages from the recorder), that silently
	// re-targets the others. Exclude such ambiguous names entirely so no action is
	// mis-bound; the colliding actions keep their inline selectors.
	ambiguous := ambiguousObjectNames(script)
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
			if logicalName == "" || seen[logicalName] || ambiguous[logicalName] {
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
