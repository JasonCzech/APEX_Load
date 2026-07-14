package api

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

func (s *Server) handleListDataPools(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSON(w, http.StatusOK, map[string]any{"data_pools": []*storage.SharedDataPool{}, "count": 0})
		return
	}
	includeArchived := r.URL.Query().Get("include_archived") == "true"
	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	var pools []*storage.SharedDataPool
	var err error
	if projectID != "" {
		if scoped, ok := s.store.(interface {
			ListDataPoolsForProject(bool, string) ([]*storage.SharedDataPool, error)
		}); ok {
			pools, err = scoped.ListDataPoolsForProject(includeArchived, projectID)
		} else {
			writeError(w, http.StatusNotImplemented, "project-scoped data pool listing is not supported by this storage backend")
			return
		}
	} else {
		pools, err = s.store.ListDataPools(includeArchived)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if projectID != "" {
		normalizedProjectID := normalizeProjectID(projectID)
		filtered := pools[:0]
		for _, pool := range pools {
			if normalizeProjectID(pool.ProjectID) == normalizedProjectID {
				filtered = append(filtered, pool)
			}
		}
		pools = filtered
	}
	if pools == nil {
		pools = []*storage.SharedDataPool{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data_pools": pools, "count": len(pools)})
}

func (s *Server) handleCreateDataPool(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	var req storage.SharedDataPool
	if !decodeJSONBody(w, r, &req) {
		return
	}
	req.ID = uuid.New().String()
	req.Name = strings.TrimSpace(req.Name)
	req.ProjectID = normalizeProjectID(req.ProjectID)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "data pool name is required")
		return
	}
	now := time.Now()
	req.CreatedAt = now
	req.UpdatedAt = now
	if err := s.store.SaveDataPool(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAuditEvent(r, "data_pool.create", "success", "data_pool", req.ID, req.ProjectID, map[string]any{"name": req.Name})
	writeJSON(w, http.StatusCreated, req)
}

func (s *Server) handleGetDataPool(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	pool, err := s.store.GetDataPool(r.PathValue("id"))
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
		}
		writeError(w, status, "data pool not found")
		return
	}
	rows, err := s.store.ListDataPoolRows(pool.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if rows == nil {
		rows = []*storage.SharedDataPoolRow{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data_pool": pool, "rows": rows})
}

func (s *Server) handleUpdateDataPool(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	id := r.PathValue("id")
	existing, err := s.store.GetDataPool(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "data pool not found")
		return
	}
	var req storage.SharedDataPool
	if !decodeJSONBody(w, r, &req) {
		return
	}
	existing.Name = strings.TrimSpace(req.Name)
	existing.Description = req.Description
	existing.Schema = req.Schema
	existing.ArchivedAt = req.ArchivedAt
	if existing.Name == "" {
		writeError(w, http.StatusBadRequest, "data pool name is required")
		return
	}
	if err := s.store.SaveDataPool(existing); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAuditEvent(r, "data_pool.update", "success", "data_pool", existing.ID, existing.ProjectID, map[string]any{"name": existing.Name})
	writeJSON(w, http.StatusOK, existing)
}

func (s *Server) handleDeleteDataPool(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	id := r.PathValue("id")
	projectID := ""
	name := ""
	if pool, err := s.store.GetDataPool(id); err == nil && pool != nil {
		projectID = pool.ProjectID
		name = pool.Name
	}
	if err := s.store.DeleteDataPool(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAuditEvent(r, "data_pool.delete", "success", "data_pool", id, projectID, map[string]any{"name": name})
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
}

func (s *Server) handleListDataPoolRows(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSON(w, http.StatusOK, map[string]any{"rows": []*storage.SharedDataPoolRow{}, "count": 0})
		return
	}
	rows, err := s.store.ListDataPoolRows(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if rows == nil {
		rows = []*storage.SharedDataPoolRow{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"rows": rows, "count": len(rows)})
}

func (s *Server) handleCreateDataPoolRow(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	var req storage.SharedDataPoolRow
	if !decodeJSONBody(w, r, &req) {
		return
	}
	req.ID = uuid.New().String()
	req.PoolID = r.PathValue("id")
	now := time.Now()
	req.CreatedAt = now
	req.UpdatedAt = now
	if err := s.store.SaveDataPoolRow(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	projectID := ""
	if pool, err := s.store.GetDataPool(req.PoolID); err == nil && pool != nil {
		projectID = pool.ProjectID
	}
	s.recordAuditEvent(r, "data_pool.row.create", "success", "data_pool_row", req.ID, projectID, map[string]any{"pool_id": req.PoolID})
	writeJSON(w, http.StatusCreated, req)
}

func (s *Server) handleUpdateDataPoolRow(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	poolID := r.PathValue("id")
	rowID := r.PathValue("row_id")
	existing, ok := s.requireDataPoolRow(w, poolID, rowID)
	if !ok {
		return
	}
	var req storage.SharedDataPoolRow
	if !decodeJSONBody(w, r, &req) {
		return
	}
	req.ID = rowID
	req.PoolID = poolID
	req.CreatedAt = existing.CreatedAt
	if err := s.store.SaveDataPoolRow(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	projectID := ""
	if pool, err := s.store.GetDataPool(req.PoolID); err == nil && pool != nil {
		projectID = pool.ProjectID
	}
	s.recordAuditEvent(r, "data_pool.row.update", "success", "data_pool_row", req.ID, projectID, map[string]any{"pool_id": req.PoolID})
	writeJSON(w, http.StatusOK, req)
}

func (s *Server) handleDeleteDataPoolRow(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	id := r.PathValue("row_id")
	projectID := ""
	poolID := r.PathValue("id")
	if _, ok := s.requireDataPoolRow(w, poolID, id); !ok {
		return
	}
	if pool, err := s.store.GetDataPool(poolID); err == nil && pool != nil {
		projectID = pool.ProjectID
	}
	if scoped, ok := s.store.(interface {
		DeleteDataPoolRowFromPool(poolID string, id string) (bool, error)
	}); ok {
		deleted, err := scoped.DeleteDataPoolRowFromPool(poolID, id)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if !deleted {
			writeError(w, http.StatusNotFound, "data pool row not found")
			return
		}
	} else if err := s.store.DeleteDataPoolRow(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAuditEvent(r, "data_pool.row.delete", "success", "data_pool_row", id, projectID, map[string]any{"pool_id": poolID})
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
}

func (s *Server) requireDataPoolRow(w http.ResponseWriter, poolID string, rowID string) (*storage.SharedDataPoolRow, bool) {
	if scoped, ok := s.store.(interface {
		GetDataPoolRowFromPool(poolID string, id string) (*storage.SharedDataPoolRow, error)
	}); ok {
		row, err := scoped.GetDataPoolRowFromPool(poolID, rowID)
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "data pool row not found")
			return nil, false
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load data pool row")
			return nil, false
		}
		if row == nil || row.ID != rowID || row.PoolID != poolID {
			writeError(w, http.StatusNotFound, "data pool row not found")
			return nil, false
		}
		return row, true
	}
	rows, err := s.store.ListDataPoolRows(poolID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load data pool rows")
		return nil, false
	}
	for _, row := range rows {
		if row != nil && row.ID == rowID && row.PoolID == poolID {
			return row, true
		}
	}
	// Use the same response for a missing row and a row owned by another pool so
	// callers cannot use this nested route to enumerate cross-project row IDs.
	writeError(w, http.StatusNotFound, "data pool row not found")
	return nil, false
}

func (s *Server) handleListScenarioDataPools(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSON(w, http.StatusOK, map[string]any{"data_pools": []storage.ScenarioDataPoolBinding{}, "count": 0})
		return
	}
	bindings, err := s.store.ListScenarioDataPoolBindings(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if bindings == nil {
		bindings = []storage.ScenarioDataPoolBinding{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data_pools": bindings, "count": len(bindings)})
}

func (s *Server) handleUpdateScenarioDataPools(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	scenarioID := r.PathValue("id")
	scenario, ok := s.runner.GetScenario(scenarioID)
	if !ok {
		writeError(w, http.StatusNotFound, "scenario not found")
		return
	}
	var req struct {
		DataPools []storage.ScenarioDataPoolBinding `json:"data_pools"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	bindings, err := normalizeScenarioDataPoolBindings(scenarioID, req.DataPools)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.validateScenarioDataPoolProjects(scenario.ProjectID, bindings); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.SaveScenarioDataPoolBindings(scenarioID, bindings); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.runner.replaceScenarioDataPools(scenarioID, bindings)
	s.recordAuditEvent(r, "scenario.data_pools.update", "success", "scenario", scenarioID, scenario.ProjectID, map[string]any{"binding_count": len(bindings)})
	writeJSON(w, http.StatusOK, map[string]any{"data_pools": bindings, "count": len(bindings)})
}

func (s *Server) validateScenarioDataPoolProjects(projectID string, bindings []storage.ScenarioDataPoolBinding) error {
	if s.store == nil || len(bindings) == 0 {
		return nil
	}
	expectedProjectID := normalizeProjectID(projectID)
	for _, binding := range bindings {
		pool, err := s.store.GetDataPool(binding.PoolID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("data pool %q not found", binding.PoolID)
			}
			return fmt.Errorf("load data pool %q: %w", binding.PoolID, err)
		}
		if normalizeProjectID(pool.ProjectID) != expectedProjectID {
			return fmt.Errorf("data pool %q does not belong to scenario project", binding.PoolID)
		}
	}
	return nil
}
