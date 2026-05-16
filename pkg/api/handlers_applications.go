package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/stormrunner/pkg/storage"
)

func (s *Server) handleCreateApplication(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	var req struct {
		Name      string `json:"name"`
		ProjectID string `json:"project_id,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "application name is required")
		return
	}
	application := &storage.Application{ID: uuid.New().String(), ProjectID: normalizeProjectID(req.ProjectID), Name: strings.TrimSpace(req.Name), CreatedAt: time.Now()}
	if err := s.store.SaveApplication(application); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAuditEvent(r, "application.create", "success", "application", application.ID, application.ProjectID, map[string]any{"name": application.Name})
	writeJSON(w, http.StatusCreated, application)
}

func (s *Server) handleListApplications(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSON(w, http.StatusOK, map[string]any{"applications": []*storage.Application{}, "count": 0})
		return
	}
	applications, err := s.store.ListApplications()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if projectID := strings.TrimSpace(r.URL.Query().Get("project_id")); projectID != "" {
		normalizedProjectID := normalizeProjectID(projectID)
		filtered := make([]*storage.Application, 0, len(applications))
		for _, application := range applications {
			if normalizeProjectID(application.ProjectID) == normalizedProjectID {
				filtered = append(filtered, application)
			}
		}
		applications = filtered
	}
	writeJSON(w, http.StatusOK, map[string]any{"applications": applications, "count": len(applications)})
}

func (s *Server) handleGetApplication(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	application, err := s.store.GetApplication(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "application not found")
		return
	}
	writeJSON(w, http.StatusOK, application)
}

func (s *Server) handleUpdateApplication(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	application, err := s.store.GetApplication(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "application not found")
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Name) != "" {
		application.Name = strings.TrimSpace(req.Name)
	}
	if err := s.store.SaveApplication(application); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAuditEvent(r, "application.update", "success", "application", application.ID, application.ProjectID, map[string]any{"name": application.Name})
	writeJSON(w, http.StatusOK, application)
}

func (s *Server) handleDeleteApplication(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	id := r.PathValue("id")
	projectID := ""
	name := ""
	if application, err := s.store.GetApplication(id); err == nil && application != nil {
		projectID = application.ProjectID
		name = application.Name
	}
	if err := s.store.DeleteApplication(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAuditEvent(r, "application.delete", "success", "application", id, projectID, map[string]any{"name": name})
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
}

func (s *Server) handleCreateRelease(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "release name is required")
		return
	}
	release := &storage.Release{ID: uuid.New().String(), ApplicationID: r.PathValue("id"), Name: strings.TrimSpace(req.Name), CreatedAt: time.Now()}
	if err := s.store.SaveRelease(release); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	projectID := ""
	if application, err := s.store.GetApplication(release.ApplicationID); err == nil && application != nil {
		projectID = application.ProjectID
	}
	s.recordAuditEvent(r, "release.create", "success", "release", release.ID, projectID, map[string]any{"name": release.Name, "application_id": release.ApplicationID})
	writeJSON(w, http.StatusCreated, release)
}

func (s *Server) handleListReleases(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSON(w, http.StatusOK, map[string]any{"releases": []*storage.Release{}, "count": 0})
		return
	}
	includeArchived := strings.EqualFold(r.URL.Query().Get("include_archived"), "true")
	releases, err := s.store.ListReleases(r.PathValue("id"), includeArchived)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"releases": releases, "count": len(releases)})
}

func (s *Server) handleGetRelease(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	release, err := s.store.GetRelease(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "release not found")
		return
	}
	writeJSON(w, http.StatusOK, release)
}

func (s *Server) handleUpdateRelease(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	release, err := s.store.GetRelease(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "release not found")
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Name) != "" {
		release.Name = strings.TrimSpace(req.Name)
	}
	if err := s.store.SaveRelease(release); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	projectID := ""
	if application, err := s.store.GetApplication(release.ApplicationID); err == nil && application != nil {
		projectID = application.ProjectID
	}
	s.recordAuditEvent(r, "release.update", "success", "release", release.ID, projectID, map[string]any{"name": release.Name, "application_id": release.ApplicationID})
	writeJSON(w, http.StatusOK, release)
}

func (s *Server) handleDeleteRelease(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	id := r.PathValue("id")
	projectID := ""
	name := ""
	applicationID := ""
	if release, err := s.store.GetRelease(id); err == nil && release != nil {
		name = release.Name
		applicationID = release.ApplicationID
		if application, err := s.store.GetApplication(release.ApplicationID); err == nil && application != nil {
			projectID = application.ProjectID
		}
	}
	if err := s.store.DeleteRelease(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAuditEvent(r, "release.delete", "success", "release", id, projectID, map[string]any{"name": name, "application_id": applicationID})
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
}

func (s *Server) handleArchiveRelease(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	id := r.PathValue("id")
	if err := s.store.ArchiveRelease(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	release, err := s.store.GetRelease(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "release not found")
		return
	}
	projectID := ""
	if application, err := s.store.GetApplication(release.ApplicationID); err == nil && application != nil {
		projectID = application.ProjectID
	}
	s.recordAuditEvent(r, "release.archive", "success", "release", release.ID, projectID, map[string]any{"name": release.Name, "application_id": release.ApplicationID})
	writeJSON(w, http.StatusOK, release)
}
