package api

// Monitoring collectors: control-plane for restricted-network monitoring agents
// (the SiteScope / monitor-over-firewall analog). Admin endpoints register a
// collector, manage its scoped metric packs and health, and rotate its token.
// The token is hashed (SHA-256) for storage and returned in plaintext only once,
// mirroring service-account API keys. The agent-facing heartbeat (collector-token
// authenticated) is a follow-on tracked under the monitoring_collectors dimension.

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

const defaultMonitoringCollectorListLimit = 100

type monitoringCollectorMutationStore interface {
	PatchMonitoringCollector(id, projectID string, name, description, health *string, allowedMetricPacks *[]string) (bool, error)
	UpdateMonitoringCollectorTokenHashIfUnchanged(id, projectID, tokenHash, expectedTokenHash string) (bool, error)
	DeleteMonitoringCollectorFromProject(id, projectID string) (bool, error)
}

func (s *Server) handleCreateMonitoringCollector(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	var req storage.MonitoringCollector
	if !decodeJSONBody(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "collector name is required")
		return
	}
	if strings.TrimSpace(req.ProjectID) == "" {
		writeError(w, http.StatusBadRequest, "project_id is required")
		return
	}
	token, err := generateAPIKey()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate collector token")
		return
	}
	req.ID = "collector-" + uuid.New().String()
	req.ProjectID = normalizeProjectID(req.ProjectID)
	req.TokenHash = hashAPIKey(token)
	req.Health = "unreachable"
	req.LastSeenAt = nil
	if err := s.store.SaveMonitoringCollector(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAuditEvent(r, "monitoring.collector.create", "success", "monitoring_collector", req.ID, req.ProjectID, map[string]any{"name": req.Name})
	// Plaintext token is shown exactly once.
	writeJSON(w, http.StatusCreated, map[string]any{"collector": req, "token": token})
}

func (s *Server) handleListMonitoringCollectors(w http.ResponseWriter, r *http.Request) {
	limit, offset := parseLimitOffsetQuery(r, defaultMonitoringCollectorListLimit)
	if s.store == nil {
		writeJSON(w, http.StatusOK, map[string]any{"collectors": []*storage.MonitoringCollector{}, "count": 0, "limit": limit, "offset": offset})
		return
	}
	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	collectors, err := s.store.ListMonitoringCollectors(projectID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if collectors == nil {
		collectors = []*storage.MonitoringCollector{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"collectors": collectors, "count": len(collectors), "limit": limit, "offset": offset})
}

func (s *Server) handleGetMonitoringCollector(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	collector, ok := s.loadMonitoringCollector(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, collector)
}

func (s *Server) handlePatchMonitoringCollector(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	collector, ok := s.loadMonitoringCollector(w, r)
	if !ok {
		return
	}
	var patch struct {
		Name               *string   `json:"name"`
		Description        *string   `json:"description"`
		Health             *string   `json:"health"`
		AllowedMetricPacks *[]string `json:"allowed_metric_packs"`
	}
	if !decodeJSONBody(w, r, &patch) {
		return
	}
	if patch.Name != nil {
		name := strings.TrimSpace(*patch.Name)
		if name == "" {
			writeError(w, http.StatusBadRequest, "collector name cannot be empty")
			return
		}
		patch.Name = &name
	}
	if patch.Health != nil {
		if !validCollectorHealth(*patch.Health) {
			writeError(w, http.StatusBadRequest, "health must be healthy, degraded, or unreachable")
			return
		}
	}
	mutationStore, ok := s.store.(monitoringCollectorMutationStore)
	if !ok {
		writeError(w, http.StatusNotImplemented, "monitoring collector storage does not support safe partial updates")
		return
	}
	projectID := authorizedProjectOr(r, collector.ProjectID)
	updated, err := mutationStore.PatchMonitoringCollector(collector.ID, projectID, patch.Name, patch.Description, patch.Health, patch.AllowedMetricPacks)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update monitoring collector")
		return
	}
	if !updated {
		writeError(w, http.StatusConflict, "monitoring collector changed concurrently")
		return
	}
	collector, err = s.store.GetMonitoringCollector(collector.ID)
	if err != nil || collector == nil || !requireAuthorizedProjectMatch(w, r, collector.ProjectID) {
		if err != nil || collector == nil {
			writeError(w, http.StatusInternalServerError, "failed to reload monitoring collector")
		}
		return
	}
	s.recordAuditEvent(r, "monitoring.collector.update", "success", "monitoring_collector", collector.ID, collector.ProjectID, nil)
	writeJSON(w, http.StatusOK, collector)
}

func (s *Server) handleDeleteMonitoringCollector(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	collector, ok := s.loadMonitoringCollector(w, r)
	if !ok {
		return
	}
	mutationStore, ok := s.store.(monitoringCollectorMutationStore)
	if !ok {
		writeError(w, http.StatusNotImplemented, "monitoring collector storage does not support safe deletion")
		return
	}
	projectID := authorizedProjectOr(r, collector.ProjectID)
	deleted, err := mutationStore.DeleteMonitoringCollectorFromProject(collector.ID, projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete monitoring collector")
		return
	}
	if !deleted {
		writeError(w, http.StatusConflict, "monitoring collector changed concurrently")
		return
	}
	s.collectorMetrics.delete(collector.ID)
	s.recordAuditEvent(r, "monitoring.collector.delete", "success", "monitoring_collector", collector.ID, collector.ProjectID, nil)
	writeJSON(w, http.StatusOK, map[string]string{"deleted": collector.ID})
}

func (s *Server) handleRotateMonitoringCollectorToken(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	collector, ok := s.loadMonitoringCollector(w, r)
	if !ok {
		return
	}
	token, err := generateAPIKey()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate collector token")
		return
	}
	mutationStore, ok := s.store.(monitoringCollectorMutationStore)
	if !ok {
		writeError(w, http.StatusNotImplemented, "monitoring collector storage does not support safe token rotation")
		return
	}
	projectID := authorizedProjectOr(r, collector.ProjectID)
	updated, err := mutationStore.UpdateMonitoringCollectorTokenHashIfUnchanged(collector.ID, projectID, hashAPIKey(token), collector.TokenHash)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to rotate collector token")
		return
	}
	if !updated {
		writeError(w, http.StatusConflict, "monitoring collector changed concurrently")
		return
	}
	collector, err = s.store.GetMonitoringCollector(collector.ID)
	if err != nil || collector == nil || !requireAuthorizedProjectMatch(w, r, collector.ProjectID) {
		if err != nil || collector == nil {
			writeError(w, http.StatusInternalServerError, "failed to reload monitoring collector")
		}
		return
	}
	s.recordAuditEvent(r, "monitoring.collector.rotate-token", "success", "monitoring_collector", collector.ID, collector.ProjectID, nil)
	writeJSON(w, http.StatusOK, map[string]any{"collector": collector, "token": token})
}

func validCollectorHealth(health string) bool {
	switch health {
	case "healthy", "degraded", "unreachable":
		return true
	}
	return false
}

func (s *Server) loadMonitoringCollector(w http.ResponseWriter, r *http.Request) (*storage.MonitoringCollector, bool) {
	collector, err := s.store.GetMonitoringCollector(r.PathValue("id"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "monitoring collector not found")
			return nil, false
		}
		writeError(w, http.StatusInternalServerError, "failed to load monitoring collector")
		return nil, false
	}
	if !requireAuthorizedProjectMatch(w, r, collector.ProjectID) {
		return nil, false
	}
	return collector, true
}
