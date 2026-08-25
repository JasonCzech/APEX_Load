package api

import (
	"database/sql"
	"errors"
	"net/http"
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
}

func (s *Server) dashboardDefinitions() (dashboardDefinitionStore, bool) {
	store, ok := s.store.(dashboardDefinitionStore)
	return store, ok
}

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
		writeError(w, http.StatusBadRequest, err.Error())
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

func (s *Server) handleGetDashboardDefinition(w http.ResponseWriter, r *http.Request) {
	definition, ok := s.loadDashboardDefinition(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, definition)
}

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
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !updated {
		writeError(w, http.StatusConflict, "dashboard changed concurrently; reload and retry")
		return
	}
	s.recordAuditEvent(r, "monitoring.dashboard_definition.update", "success", "dashboard_definition", definition.ID, definition.ProjectID, nil)
	writeJSON(w, http.StatusOK, definition)
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
