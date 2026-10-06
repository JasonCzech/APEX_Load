package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jasonczech/apexload/pkg/storage"
)

type LibraryAssignmentRequest struct {
	ApplicationID    json.RawMessage `json:"application_id"`
	ReleaseIDs       *[]string       `json:"release_ids"`
	ExpectedRevision *int64          `json:"expected_revision"`
}

func (s *Server) handleScriptLibraryAssignment(w http.ResponseWriter, r *http.Request) {
	s.handleLibraryAssignment(w, r, "script")
}
func (s *Server) handleScenarioLibraryAssignment(w http.ResponseWriter, r *http.Request) {
	s.handleLibraryAssignment(w, r, "scenario")
}
func (s *Server) handleLibraryAssignment(w http.ResponseWriter, r *http.Request, kind string) {
	store, supported := s.store.(storage.LibraryAssignmentStore)
	if !supported {
		writeError(w, http.StatusServiceUnavailable, "library assignment storage is unavailable")
		return
	}
	id, projectID := r.PathValue("id"), ""
	if kind == "script" {
		item, ok := s.runner.GetScript(id)
		if !ok {
			writeError(w, http.StatusNotFound, "script not found")
			return
		}
		if isStoredCIScriptSnapshot(item) {
			writeError(w, http.StatusConflict, "private execution snapshots cannot be assigned")
			return
		}
		id, projectID = item.ID, item.ProjectID
	} else {
		item, ok := s.runner.GetScenario(id)
		if !ok {
			writeError(w, http.StatusNotFound, "scenario not found")
			return
		}
		id, projectID = item.ID, item.ProjectID
	}
	if !requireAuthorizedProjectMatch(w, r, projectID) {
		return
	}
	var admission scenarioWriterAdmission
	if kind == "scenario" {
		var admitted bool
		if admission, admitted = s.admitScenarioWriter(w, r, id, projectID); !admitted {
			return
		}
	}
	var req LibraryAssignmentRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	var applicationID *string
	if len(req.ApplicationID) == 0 || req.ReleaseIDs == nil || req.ExpectedRevision == nil || json.Unmarshal(req.ApplicationID, &applicationID) != nil {
		writeError(w, http.StatusBadRequest, "application_id (ID or null), release_ids, and expected_revision are required")
		return
	}
	if kind == "script" {
		// Application secret usage depends on this assignment. Serialize it with
		// usage-reviewed rotation so a shared-use acknowledgement cannot race an
		// assignment change.
		release, err := s.acquireSecretReferenceMutation(projectID)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "secret usage fence is unavailable")
			return
		}
		defer release()
	}
	var before, after *storage.LibraryAssignment
	var revision string
	var err error
	if admission.guarded() {
		before, after, revision, err = admission.store.ReplaceScenarioLibraryAssignmentGuarded(id, authorizedProjectOr(r, projectID), applicationID, *req.ReleaseIDs, *req.ExpectedRevision, admission.guard)
	} else {
		before, after, err = store.ReplaceLibraryAssignment(kind, id, authorizedProjectOr(r, projectID), applicationID, *req.ReleaseIDs, *req.ExpectedRevision)
	}
	if err != nil && kind == "scenario" && writeScenarioWriterError(w, err) {
		return
	}
	if err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, storage.ErrLibraryAssignmentConflict):
			status = http.StatusConflict
		case errors.Is(err, storage.ErrLibraryAssignmentInvalid):
			status = http.StatusBadRequest
		case errors.Is(err, sql.ErrNoRows):
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	s.recordAuditEvent(r, kind+".library_assignment.update", "success", kind, id, projectID, map[string]any{"before": before, "after": after})
	setScenarioWriterETag(w, revision)
	writeJSON(w, http.StatusOK, after)
}

func (s *Server) scriptLibraryAssignments(w http.ResponseWriter, scripts []*StoredScript) bool {
	ids := make([]string, 0, len(scripts))
	for _, item := range scripts {
		if item != nil {
			ids = append(ids, item.ID)
		}
	}
	assignments, err := s.libraryAssignments("script", ids)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "unable to load library assignments")
		return false
	}
	for _, item := range scripts {
		if item != nil {
			item.LibraryAssignment = assignments[item.ID]
		}
	}
	return true
}
func (s *Server) scenarioLibraryAssignments(w http.ResponseWriter, scenarios []*StoredScenario) bool {
	ids := make([]string, 0, len(scenarios))
	for _, item := range scenarios {
		if item != nil {
			ids = append(ids, item.ID)
		}
	}
	assignments, err := s.libraryAssignments("scenario", ids)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "unable to load library assignments")
		return false
	}
	for _, item := range scenarios {
		if item != nil {
			item.LibraryAssignment = assignments[item.ID]
		}
	}
	return true
}
func (s *Server) libraryAssignments(kind string, ids []string) (map[string]*storage.LibraryAssignment, error) {
	if store, ok := s.store.(storage.LibraryAssignmentStore); ok {
		return store.ListLibraryAssignments(kind, ids)
	}
	assignments := make(map[string]*storage.LibraryAssignment, len(ids))
	for _, id := range ids {
		assignments[id] = &storage.LibraryAssignment{Releases: []storage.LibraryAssignmentRelease{}}
	}
	return assignments, nil
}
