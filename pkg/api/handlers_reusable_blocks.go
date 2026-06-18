package api

// Reusable blocks: user-saved authoring components. Operators capture a bundle of
// script fragments (config/data_sources/parameters/init/actions/teardown) from one
// script and re-insert it into another. Saved blocks are project-scoped and merged
// with the curated built-in catalog by handleAuthoringComponents.

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

type createReusableBlockRequest struct {
	AuthoringComponent
	ProjectID string `json:"project_id"`
}

func (s *Server) handleCreateReusableBlock(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	var req createReusableBlockRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "block name is required")
		return
	}
	if strings.TrimSpace(req.ProjectID) == "" {
		writeError(w, http.StatusBadRequest, "project_id is required")
		return
	}

	component := req.AuthoringComponent
	component.ID = "block-" + uuid.New().String()
	if strings.TrimSpace(component.Kind) == "" {
		component.Kind = "request_group"
	}
	if len(component.Protocols) == 0 {
		component.Protocols = []string{"*"}
	}

	payload, err := json.Marshal(component)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encode reusable block")
		return
	}
	block := &storage.ReusableBlock{
		ID:        component.ID,
		ProjectID: normalizeProjectID(req.ProjectID),
		Name:      component.Name,
		Kind:      component.Kind,
		Component: string(payload),
	}
	if err := s.store.SaveReusableBlock(block); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAuditEvent(r, "scripts.reusable_block.create", "success", "reusable_block", block.ID, block.ProjectID, map[string]any{
		"name": block.Name,
		"kind": block.Kind,
	})
	writeJSON(w, http.StatusCreated, component)
}

func (s *Server) handleDeleteReusableBlock(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	id := r.PathValue("id")
	projectID := ""
	if b, err := s.store.GetReusableBlock(id); err == nil && b != nil {
		projectID = b.ProjectID
	}
	if err := s.store.DeleteReusableBlock(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAuditEvent(r, "scripts.reusable_block.delete", "success", "reusable_block", id, projectID, nil)
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
}

// userReusableBlocks loads a project's saved blocks and decodes each into an
// AuthoringComponent. The stored row id is authoritative for deletes, so it
// overrides whatever id rode along in the JSON. Malformed rows are skipped
// rather than failing the whole catalog response.
func (s *Server) userReusableBlocks(projectID string) []AuthoringComponent {
	if s.store == nil || strings.TrimSpace(projectID) == "" {
		return nil
	}
	blocks, err := s.store.ListReusableBlocks(projectID)
	if err != nil {
		return nil
	}
	components := make([]AuthoringComponent, 0, len(blocks))
	for _, block := range blocks {
		var component AuthoringComponent
		if err := json.Unmarshal([]byte(block.Component), &component); err != nil {
			continue
		}
		component.ID = block.ID
		components = append(components, component)
	}
	return components
}
