package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

type projectRequest struct {
	Name       string                  `json:"name"`
	DomainID   *string                 `json:"domain_id,omitempty"`
	CategoryID *string                 `json:"category_id,omitempty"`
	Settings   storage.ProjectSettings `json:"settings,omitempty"`
}

func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "project storage is not configured")
		return
	}
	var req projectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "project name is required")
		return
	}
	if err := validateProjectSettings(req.Settings); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	domainID, categoryID, err := s.resolveProjectPlacement(stringPtrValue(req.DomainID), stringPtrValue(req.CategoryID))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project placement: "+err.Error())
		return
	}
	now := time.Now()
	project := &storage.Project{ID: uuid.New().String(), DomainID: domainID, CategoryID: categoryID, Name: strings.TrimSpace(req.Name), Settings: req.Settings, CreatedAt: now, UpdatedAt: now}
	if err := s.store.SaveProject(project); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.recordAuditEvent(r, "project.create", "success", "project", project.ID, project.ID, map[string]any{"name": project.Name})
	writeJSON(w, http.StatusCreated, project)
}

func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"projects": []*storage.Project{{ID: storage.DefaultProjectID, DomainID: storage.DefaultDomainID, CategoryID: storage.DefaultRepositoryCategoryID, Name: "Unassigned", CreatedAt: time.Time{}, UpdatedAt: time.Time{}}},
			"count":    1,
		})
		return
	}
	projects, err := s.store.ListProjects()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if domainID := strings.TrimSpace(r.URL.Query().Get("domain_id")); domainID != "" {
		normalizedDomainID := normalizeDomainID(domainID)
		filtered := make([]*storage.Project, 0, len(projects))
		for _, project := range projects {
			if normalizeDomainID(project.DomainID) == normalizedDomainID {
				filtered = append(filtered, project)
			}
		}
		projects = filtered
	}
	if categoryID := strings.TrimSpace(r.URL.Query().Get("category_id")); categoryID != "" {
		normalizedCategoryID := normalizeRepositoryCategoryID(categoryID)
		filtered := make([]*storage.Project, 0, len(projects))
		for _, project := range projects {
			if normalizeRepositoryCategoryID(project.CategoryID) == normalizedCategoryID {
				filtered = append(filtered, project)
			}
		}
		projects = filtered
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": projects, "count": len(projects)})
}

func (s *Server) handleGetProject(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "project storage is not configured")
		return
	}
	project, err := s.store.GetProject(r.PathValue("id"))
	if err != nil {
		status := http.StatusInternalServerError
		if err == sql.ErrNoRows {
			status = http.StatusNotFound
		}
		writeError(w, status, "project not found")
		return
	}
	writeJSON(w, http.StatusOK, project)
}

func (s *Server) handleUpdateProject(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "project storage is not configured")
		return
	}
	project, err := s.store.GetProject(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}
	var req projectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "project name is required")
		return
	}
	if err := validateProjectSettings(req.Settings); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.DomainID != nil || req.CategoryID != nil {
		domainID := project.DomainID
		categoryID := project.CategoryID
		if req.DomainID != nil {
			domainID = *req.DomainID
		}
		if req.CategoryID != nil {
			categoryID = *req.CategoryID
		}
		resolvedDomainID, resolvedCategoryID, err := s.resolveProjectPlacement(domainID, categoryID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid project placement: "+err.Error())
			return
		}
		project.DomainID = resolvedDomainID
		project.CategoryID = resolvedCategoryID
	}
	project.Name = strings.TrimSpace(req.Name)
	project.Settings = req.Settings
	if err := s.store.SaveProject(project); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.runner.ReconcilePendingTests()
	s.recordAuditEvent(r, "project.update", "success", "project", project.ID, project.ID, map[string]any{"name": project.Name, "max_concurrent_tests": project.Settings.MaxConcurrentTests, "max_vusers": project.Settings.MaxVUsers})
	writeJSON(w, http.StatusOK, project)
}

func stringPtrValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func validateProjectSettings(settings storage.ProjectSettings) error {
	if settings.MaxConcurrentTests < 0 {
		return errProjectSettings("max_concurrent_tests must be greater than or equal to 0")
	}
	if settings.MaxVUsers < 0 {
		return errProjectSettings("max_vusers must be greater than or equal to 0")
	}
	if settings.MaxVUsersPerTest < 0 {
		return errProjectSettings("max_vusers_per_test must be greater than or equal to 0")
	}
	if settings.MaxTestDurationMinutes < 0 {
		return errProjectSettings("max_test_duration_minutes must be greater than or equal to 0")
	}
	if len(settings.RedactionRules) == 0 {
		return nil
	}
	if _, err := engine.NewRedactionConfig(settings.RedactionRules); err != nil {
		return err
	}
	return nil
}

func errProjectSettings(message string) error {
	return &projectSettingsError{message: message}
}

type projectSettingsError struct {
	message string
}

func (e *projectSettingsError) Error() string {
	return e.message
}

func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "project storage is not configured")
		return
	}
	id := r.PathValue("id")
	if err := s.store.DeleteProject(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAuditEvent(r, "project.delete", "success", "project", id, id, nil)
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
}
