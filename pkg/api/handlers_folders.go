package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

func (s *Server) handleCreateFolder(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "folder storage is not configured")
		return
	}
	var req struct {
		Name     string  `json:"name"`
		Category string  `json:"category"`
		ParentID *string `json:"parent_id,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "folder name is required")
		return
	}
	now := time.Now()
	category := req.Category
	if category == "" {
		category = "general"
	}
	folder := &storage.Folder{
		ID:        uuid.New().String(),
		ProjectID: r.PathValue("pid"),
		ParentID:  req.ParentID,
		Name:      strings.TrimSpace(req.Name),
		Category:  category,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.store.SaveFolder(folder); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAuditEvent(r, "folder.create", "success", "folder", folder.ID, folder.ProjectID, map[string]any{"name": folder.Name, "category": folder.Category})
	writeJSON(w, http.StatusCreated, folder)
}

func (s *Server) handleListFolders(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSON(w, http.StatusOK, map[string]any{"folders": []*storage.Folder{}, "count": 0})
		return
	}
	folders, err := s.store.ListFolders(r.PathValue("pid"), r.URL.Query().Get("category"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"folders": folders, "count": len(folders)})
}

func (s *Server) handleUpdateFolder(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "folder storage is not configured")
		return
	}
	folder, err := s.store.GetFolder(r.PathValue("id"))
	if err != nil {
		status := http.StatusInternalServerError
		if err == sql.ErrNoRows {
			status = http.StatusNotFound
		}
		writeError(w, status, "folder not found")
		return
	}
	var req struct {
		Name     *string `json:"name,omitempty"`
		ParentID *string `json:"parent_id,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if req.Name != nil {
		if strings.TrimSpace(*req.Name) == "" {
			writeError(w, http.StatusBadRequest, "folder name is required")
			return
		}
		folder.Name = strings.TrimSpace(*req.Name)
		if err := s.store.SaveFolder(folder); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if req.ParentID != nil {
		if err := s.store.MoveFolder(folder.ID, req.ParentID); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		folder.ParentID = req.ParentID
	}
	s.recordAuditEvent(r, "folder.update", "success", "folder", folder.ID, folder.ProjectID, map[string]any{"name": folder.Name, "parent_id": folder.ParentID})
	writeJSON(w, http.StatusOK, folder)
}

func (s *Server) handleDeleteFolder(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "folder storage is not configured")
		return
	}
	id := r.PathValue("id")
	projectID := ""
	name := ""
	if folder, err := s.store.GetFolder(id); err == nil && folder != nil {
		projectID = folder.ProjectID
		name = folder.Name
	}
	if err := s.store.DeleteFolder(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAuditEvent(r, "folder.delete", "success", "folder", id, projectID, map[string]any{"name": name})
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
}
