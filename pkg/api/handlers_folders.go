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
	folder := &storage.Folder{
		ID:        uuid.New().String(),
		ProjectID: r.PathValue("pid"),
		ParentID:  req.ParentID,
		Name:      strings.TrimSpace(req.Name),
		Category:  req.Category,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.store.SaveFolder(folder); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
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
	writeJSON(w, http.StatusOK, folder)
}

func (s *Server) handleDeleteFolder(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "folder storage is not configured")
		return
	}
	id := r.PathValue("id")
	if err := s.store.DeleteFolder(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
}
