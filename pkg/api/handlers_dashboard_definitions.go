package api

import (
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

type dashboardDefinitionStore interface {
	SaveDashboardDefinition(*storage.DashboardDefinition) error
	GetDashboardDefinition(string) (*storage.DashboardDefinition, error)
	ListDashboardDefinitions(string, int, int) ([]*storage.DashboardDefinition, error)
	UpdateDashboardDefinitionIfRevision(*storage.DashboardDefinition, int64) (bool, error)
	DeleteDashboardDefinitionIfRevision(string, string, int64) (bool, error)
	GetDashboardDefinitionRevision(string, int64) (*storage.DashboardDefinition, error)
	ListDashboardDefinitionRevisions(string) ([]storage.DashboardDefinitionRevision, error)
}

func (s *Server) dashboardDefinitions() (dashboardDefinitionStore, bool) {
	store, ok := s.store.(dashboardDefinitionStore)
	return store, ok
}

// handleCreateDashboardDefinition saves a new definition; a definition refused
// for its content is 400 and a store failure 500.
//
// OpenAPI-Responses: 400, 500
func (s *Server) handleCreateDashboardDefinition(w http.ResponseWriter, r *http.Request) {
	store, configured := s.dashboardDefinitions()
	if !configured {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	var definition storage.DashboardDefinition
	if !decodeJSONBody(w, r, &definition) {
		return
	}
	definition.ID = uuid.NewString()
	definition.ProjectID = authorizedProjectOr(r, definition.ProjectID)
	_, subject, _ := s.resolveAuditActor(r)
	definition.Owner = subject
	definition.Revision = 1
	if err := store.SaveDashboardDefinition(&definition); err != nil {
		writeDashboardDefinitionSaveError(w, err)
		return
	}
	s.recordAuditEvent(r, "monitoring.dashboard_definition.create", "success", "dashboard_definition", definition.ID, definition.ProjectID, nil)
	writeJSON(w, http.StatusCreated, &definition)
}

func (s *Server) handleListDashboardDefinitions(w http.ResponseWriter, r *http.Request) {
	store, configured := s.dashboardDefinitions()
	if !configured {
		writeJSON(w, http.StatusOK, map[string]any{"dashboards": []*storage.DashboardDefinition{}, "count": 0})
		return
	}
	limit, offset := parseLimitOffsetQuery(r, 100)
	projectID := authorizedProjectOr(r, strings.TrimSpace(r.URL.Query().Get("project_id")))
	definitions, err := store.ListDashboardDefinitions(projectID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list dashboard definitions")
		return
	}
	if definitions == nil {
		definitions = []*storage.DashboardDefinition{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"dashboards": definitions, "count": len(definitions), "limit": limit, "offset": offset})
}

// handleGetDashboardDefinition returns a definition, or with ?revision=N
// exactly the revision saved as N (with the current revision beside it), and
// the saved revisions a viewer can reopen.
func (s *Server) handleGetDashboardDefinition(w http.ResponseWriter, r *http.Request) {
	definition, ok := s.loadDashboardDefinition(w, r)
	if !ok {
		return
	}
	store, _ := s.dashboardDefinitions()
	retained, err := store.ListDashboardDefinitionRevisions(definition.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list dashboard definition revisions")
		return
	}
	// The definition and its revision list are separate reads, and an edit
	// can commit between them: the current revision is the newer of the two.
	current := definition.Revision
	if len(retained) > 0 && retained[0].Revision > current {
		current = retained[0].Revision
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("revision")); raw != "" {
		revision, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || revision <= 0 {
			writeError(w, http.StatusBadRequest, "revision must be a positive integer")
			return
		}
		// A definition saved before revisions were kept has no saved row
		// for its current revision; the current row is that revision.
		if revision != definition.Revision {
			saved, err := store.GetDashboardDefinitionRevision(definition.ID, revision)
			if errors.Is(err, storage.ErrDashboardRevisionNotRetained) {
				writeError(w, http.StatusNotFound, "dashboard definition revision is not retained")
				return
			}
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to load dashboard definition revision")
				return
			}
			definition = saved
		}
		// A revision read always says which revision is current.
		definition.CurrentRevision = current
	} else if current != definition.Revision {
		// An edit committed after the definition was read: list only the
		// revisions up to the one returned, never a newer one.
		retained = slices.DeleteFunc(retained, func(item storage.DashboardDefinitionRevision) bool { return item.Revision > definition.Revision })
	}
	definition.RetainedRevisions = retained
	writeJSON(w, http.StatusOK, definition)
}

// handlePatchDashboardDefinition edits a definition at its expected revision;
// a definition refused for its content is 400 and a store failure 500.
//
// OpenAPI-Responses: 400, 409, 500
func (s *Server) handlePatchDashboardDefinition(w http.ResponseWriter, r *http.Request) {
	definition, ok := s.loadDashboardDefinition(w, r)
	if !ok {
		return
	}
	var patch struct {
		Revision          int64                   `json:"revision"`
		Name              *string                 `json:"name"`
		Description       *string                 `json:"description"`
		Visibility        *string                 `json:"visibility"`
		SlideshowInterval *int                    `json:"slideshow_interval_seconds"`
		Tabs              *[]storage.DashboardTab `json:"tabs"`
	}
	if !decodeJSONBody(w, r, &patch) {
		return
	}
	if patch.Revision <= 0 || patch.Revision != definition.Revision {
		writeError(w, http.StatusConflict, "dashboard revision is stale; reload and retry")
		return
	}
	if patch.Name != nil {
		definition.Name = strings.TrimSpace(*patch.Name)
	}
	if patch.Description != nil {
		definition.Description = strings.TrimSpace(*patch.Description)
	}
	if patch.Visibility != nil {
		definition.Visibility = strings.TrimSpace(*patch.Visibility)
	}
	if patch.SlideshowInterval != nil {
		definition.SlideshowInterval = *patch.SlideshowInterval
	}
	if patch.Tabs != nil {
		definition.Tabs = *patch.Tabs
	}
	store, _ := s.dashboardDefinitions()
	updated, err := store.UpdateDashboardDefinitionIfRevision(definition, patch.Revision)
	if err != nil {
		writeDashboardDefinitionSaveError(w, err)
		return
	}
	if !updated {
		writeError(w, http.StatusConflict, "dashboard changed concurrently; reload and retry")
		return
	}
	s.recordAuditEvent(r, "monitoring.dashboard_definition.update", "success", "dashboard_definition", definition.ID, definition.ProjectID, nil)
	writeJSON(w, http.StatusOK, definition)
}

// writeDashboardDefinitionSaveError answers a definition refused for its own
// content with 400 and its reason; a store failure (lock contention, a
// revision write, the commit) is the server's, answered with 500.
func writeDashboardDefinitionSaveError(w http.ResponseWriter, err error) {
	var invalid *storage.DashboardDefinitionInvalid
	if errors.As(err, &invalid) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeError(w, http.StatusInternalServerError, "failed to save dashboard definition: "+err.Error())
}

func (s *Server) handleDeleteDashboardDefinition(w http.ResponseWriter, r *http.Request) {
	definition, ok := s.loadDashboardDefinition(w, r)
	if !ok {
		return
	}
	revision, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("revision")), 10, 64)
	if err != nil || revision <= 0 {
		writeError(w, http.StatusBadRequest, "revision is required")
		return
	}
	store, _ := s.dashboardDefinitions()
	deleted, err := store.DeleteDashboardDefinitionIfRevision(definition.ID, definition.ProjectID, revision)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete dashboard definition")
		return
	}
	if !deleted {
		writeError(w, http.StatusConflict, "dashboard changed concurrently; reload and retry")
		return
	}
	s.recordAuditEvent(r, "monitoring.dashboard_definition.delete", "success", "dashboard_definition", definition.ID, definition.ProjectID, nil)
	writeJSON(w, http.StatusOK, map[string]string{"deleted": definition.ID})
}

func (s *Server) loadDashboardDefinition(w http.ResponseWriter, r *http.Request) (*storage.DashboardDefinition, bool) {
	store, configured := s.dashboardDefinitions()
	if !configured {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return nil, false
	}
	definition, err := store.GetDashboardDefinition(r.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "dashboard definition not found")
		return nil, false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load dashboard definition")
		return nil, false
	}
	return definition, true
}
