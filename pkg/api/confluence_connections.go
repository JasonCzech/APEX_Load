package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

type confluenceConnectionRequest struct {
	Name            string    `json:"name"`
	Enabled         bool      `json:"enabled"`
	SiteURL         string    `json:"site_url"`
	Email           string    `json:"email"`
	SpaceID         string    `json:"space_id"`
	ParentPageID    string    `json:"parent_page_id"`
	TokenSecretName *string   `json:"token_secret_name,omitempty"`
	AutoPublish     bool      `json:"auto_publish"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type confluenceConnectionResponse struct {
	ID               string     `json:"id"`
	ProjectID        string     `json:"project_id"`
	Name             string     `json:"name"`
	Enabled          bool       `json:"enabled"`
	SiteURL          string     `json:"site_url"`
	Email            string     `json:"email"`
	SpaceID          string     `json:"space_id"`
	ParentPageID     string     `json:"parent_page_id"`
	HasToken         bool       `json:"has_token"`
	AutoPublish      bool       `json:"auto_publish"`
	Status           string     `json:"status"`
	LastCheckedAt    *time.Time `json:"last_checked_at,omitempty"`
	LastCheckStatus  string     `json:"last_check_status,omitempty"`
	LastCheckMessage string     `json:"last_check_message,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

func confluenceConnectionView(c *storage.ConfluenceConnection) confluenceConnectionResponse {
	status := "disabled"
	if c.Enabled {
		status = "unverified"
		if c.LastCheckStatus != "" {
			status = c.LastCheckStatus
		}
	}
	return confluenceConnectionResponse{ID: c.ID, ProjectID: c.ProjectID, Name: c.Name, Enabled: c.Enabled, SiteURL: c.SiteURL, Email: c.Email, SpaceID: c.SpaceID, ParentPageID: c.ParentPageID, HasToken: c.TokenSecretName != "", AutoPublish: c.AutoPublish, Status: status, LastCheckedAt: c.LastCheckedAt, LastCheckStatus: c.LastCheckStatus, LastCheckMessage: c.LastCheckMessage, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt}
}

func confluenceSelectableView(c *storage.ConfluenceConnection) confluenceConnectionResponse {
	view := confluenceConnectionView(c)
	view.SiteURL, view.Email, view.SpaceID, view.ParentPageID = "", "", "", ""
	view.HasToken = false
	view.LastCheckMessage = ""
	return view
}

func applyConfluenceConnectionRequest(c *storage.ConfluenceConnection, req confluenceConnectionRequest) {
	c.Name, c.Enabled, c.SiteURL, c.Email, c.SpaceID, c.ParentPageID, c.AutoPublish = req.Name, req.Enabled, req.SiteURL, req.Email, req.SpaceID, req.ParentPageID, req.AutoPublish
	if req.TokenSecretName != nil {
		c.TokenSecretName = *req.TokenSecretName
	}
}

func (s *Server) confluenceStore(w http.ResponseWriter) (storage.ConfluenceStore, bool) {
	store, ok := s.store.(storage.ConfluenceStore)
	if !ok {
		writeError(w, 500, "Confluence storage is unavailable")
	}
	return store, ok
}

func (s *Server) loadConfluenceConnection(w http.ResponseWriter, r *http.Request) (*storage.ConfluenceConnection, storage.ConfluenceStore, bool) {
	store, ok := s.confluenceStore(w)
	if !ok {
		return nil, nil, false
	}
	c, err := store.GetConfluenceConnection(r.PathValue("connection_id"))
	if errors.Is(err, sql.ErrNoRows) || c == nil || c.ProjectID != r.PathValue("id") {
		writeError(w, 404, "Confluence connection not found")
		return nil, nil, false
	}
	if err != nil {
		writeError(w, 500, "failed to load Confluence connection")
		return nil, nil, false
	}
	return c, store, true
}

func (s *Server) handleListConfluenceConnections(w http.ResponseWriter, r *http.Request) {
	store, ok := s.confluenceStore(w)
	if !ok {
		return
	}
	items, err := store.ListConfluenceConnections(r.PathValue("id"))
	if err != nil {
		writeError(w, 500, "failed to list Confluence connections")
		return
	}
	views := make([]confluenceConnectionResponse, 0, len(items))
	projectID := r.PathValue("id")
	user, _ := AuthenticatedUserFromContext(r.Context())
	canManage := s.authSettingsForRequest().Mode == authModeDev || (user != nil && (user.hasGlobalRole(RoleProjectAdmin, RoleSystemAdmin) || userHasProjectOrParentRole(s.store, user, projectID, RoleProjectAdmin)))
	for _, c := range items {
		if canManage {
			views = append(views, confluenceConnectionView(c))
		} else if c.Enabled {
			views = append(views, confluenceSelectableView(c))
		}
	}
	_, publicURLErr := confluenceReportURL(nil, "probe")
	writeJSON(w, 200, map[string]any{"connections": views, "count": len(views), "public_url_configured": publicURLErr == nil})
}

func (s *Server) handleCreateConfluenceConnection(w http.ResponseWriter, r *http.Request) {
	store, ok := s.confluenceStore(w)
	if !ok {
		return
	}
	var req confluenceConnectionRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	c := &storage.ConfluenceConnection{ID: uuid.NewString(), ProjectID: r.PathValue("id")}
	applyConfluenceConnectionRequest(c, req)
	if err := c.Validate(); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if _, err := confluenceReportURL(nil, "probe"); c.AutoPublish && err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if err := store.CreateConfluenceConnection(c); err != nil {
		writeError(w, 409, "failed to create Confluence connection: "+err.Error())
		return
	}
	s.recordAuditEvent(r, "confluence.connection.create", "success", "confluence_connection", c.ID, c.ProjectID, nil)
	writeJSON(w, 201, confluenceConnectionView(c))
}

func (s *Server) handleUpdateConfluenceConnection(w http.ResponseWriter, r *http.Request) {
	c, store, ok := s.loadConfluenceConnection(w, r)
	if !ok {
		return
	}
	var req confluenceConnectionRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if req.UpdatedAt.IsZero() {
		writeError(w, 400, "updated_at is required")
		return
	}
	applyConfluenceConnectionRequest(c, req)
	c.LastCheckedAt = nil
	c.LastCheckStatus = ""
	c.LastCheckMessage = ""
	if err := c.Validate(); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if _, err := confluenceReportURL(nil, "probe"); c.AutoPublish && err != nil {
		writeError(w, 400, err.Error())
		return
	}
	updated, err := store.UpdateConfluenceConnectionIfUnchanged(c, req.UpdatedAt)
	if err != nil {
		writeError(w, 409, "failed to update Confluence connection: "+err.Error())
		return
	}
	if !updated {
		writeError(w, 409, "Confluence connection changed; reload and retry")
		return
	}
	s.recordAuditEvent(r, "confluence.connection.update", "success", "confluence_connection", c.ID, c.ProjectID, nil)
	writeJSON(w, 200, confluenceConnectionView(c))
}

func (s *Server) handleDeleteConfluenceConnection(w http.ResponseWriter, r *http.Request) {
	c, store, ok := s.loadConfluenceConnection(w, r)
	if !ok {
		return
	}
	deleted, err := store.DeleteConfluenceConnection(c.ID, c.ProjectID)
	if err != nil {
		writeError(w, 500, "failed to delete Confluence connection")
		return
	}
	if !deleted {
		writeError(w, 404, "Confluence connection not found")
		return
	}
	s.recordAuditEvent(r, "confluence.connection.delete", "success", "confluence_connection", c.ID, c.ProjectID, nil)
	w.WriteHeader(204)
}

func (s *Server) handleTestConfluenceConnection(w http.ResponseWriter, r *http.Request) {
	c, store, ok := s.loadConfluenceConnection(w, r)
	if !ok {
		return
	}
	if c.SiteURL == "" || c.Email == "" || c.SpaceID == "" || c.ParentPageID == "" || c.TokenSecretName == "" {
		writeError(w, 409, "Confluence connection is incomplete")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	err := s.verifyConfluenceDestination(ctx, c)
	now := time.Now().UTC()
	status, message := "connected", "Parent page and space verified"
	if err != nil {
		status, message = "error", err.Error()
	}
	if updateErr := store.UpdateConfluenceConnectionHealth(c.ID, c.ProjectID, status, message, now, c.UpdatedAt); updateErr != nil {
		if errors.Is(updateErr, sql.ErrNoRows) {
			writeError(w, 409, "Confluence connection changed; test it again")
			return
		}
		writeError(w, 500, "failed to save Confluence connection health")
		return
	}
	if err != nil {
		writeError(w, 502, "Confluence connection test failed: "+message)
		return
	}
	writeJSON(w, 200, map[string]any{"status": status, "message": message, "checked_at": now})
}
