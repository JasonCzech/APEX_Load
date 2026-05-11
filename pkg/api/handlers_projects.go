package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/stormrunner/pkg/storage"
)

func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "project storage is not configured")
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
		writeError(w, http.StatusBadRequest, "project name is required")
		return
	}
	now := time.Now()
	project := &storage.Project{ID: uuid.New().String(), Name: strings.TrimSpace(req.Name), CreatedAt: now, UpdatedAt: now}
	if err := s.store.SaveProject(project); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, project)
}

func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"projects": []*storage.Project{{ID: storage.DefaultProjectID, Name: "Unassigned", CreatedAt: time.Time{}, UpdatedAt: time.Time{}}},
			"count":    1,
		})
		return
	}
	projects, err := s.store.ListProjects()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
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
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "project name is required")
		return
	}
	project.Name = strings.TrimSpace(req.Name)
	if err := s.store.SaveProject(project); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, project)
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
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
}
