package api

// Fleet management: cloud-managed generator pools, persistent generator
// metadata (labels/region/cordon/pool membership), and cloud provisioning
// requests. All endpoints are system-admin-scoped infrastructure operations.
// Provisioning has no live cloud/Kubernetes backend yet, so requests resolve to
// status "not_configured" with an explanatory event rather than silently
// succeeding — tracked under the fleet_management parity dimension.

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

const defaultFleetListLimit = 100

// ---- fleet pools ---------------------------------------------------------

func (s *Server) handleCreateFleetPool(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	var req storage.FleetPool
	if !decodeJSONBody(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.CloudProvider = strings.TrimSpace(req.CloudProvider)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "fleet pool name is required")
		return
	}
	if req.CloudProvider == "" {
		writeError(w, http.StatusBadRequest, "cloud_provider is required")
		return
	}
	if req.MaxSize > 0 && req.MinSize > req.MaxSize {
		writeError(w, http.StatusBadRequest, "min_size cannot exceed max_size")
		return
	}
	req.ID = uuid.New().String()
	if req.Status == "" {
		req.Status = "active"
	}
	if err := s.store.SaveFleetPool(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(req.Members) > 0 {
		if err := s.saveFleetPoolMembers(req.ID, req.Members); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	s.recordAuditEvent(r, "fleet.pool.create", "success", "fleet_pool", req.ID, "", map[string]any{
		"name": req.Name, "cloud_provider": req.CloudProvider,
	})
	pool, err := s.store.GetFleetPool(req.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, pool)
}

func (s *Server) handleListFleetPools(w http.ResponseWriter, r *http.Request) {
	limit, offset := parseLimitOffsetQuery(r, defaultFleetListLimit)
	if s.store == nil {
		writeJSON(w, http.StatusOK, map[string]any{"fleet_pools": []*storage.FleetPool{}, "count": 0, "limit": limit, "offset": offset})
		return
	}
	pools, err := s.store.ListFleetPools(limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if pools == nil {
		pools = []*storage.FleetPool{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"fleet_pools": pools, "count": len(pools), "limit": limit, "offset": offset,
	})
}

func (s *Server) handleGetFleetPool(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	pool, ok := s.loadFleetPool(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, pool)
}

func (s *Server) handleUpdateFleetPool(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	existing, ok := s.loadFleetPool(w, r)
	if !ok {
		return
	}
	var req storage.FleetPool
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if name := strings.TrimSpace(req.Name); name != "" {
		existing.Name = name
	}
	if req.MaxSize > 0 && req.MinSize > req.MaxSize {
		writeError(w, http.StatusBadRequest, "min_size cannot exceed max_size")
		return
	}
	existing.Description = req.Description
	if cp := strings.TrimSpace(req.CloudProvider); cp != "" {
		existing.CloudProvider = cp
	}
	existing.Region = req.Region
	existing.MinSize = req.MinSize
	existing.MaxSize = req.MaxSize
	existing.DesiredSize = req.DesiredSize
	if req.Status != "" {
		existing.Status = req.Status
	}
	existing.Tags = req.Tags
	if err := s.store.SaveFleetPool(existing); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Members != nil {
		if err := s.saveFleetPoolMembers(existing.ID, req.Members); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	s.recordAuditEvent(r, "fleet.pool.update", "success", "fleet_pool", existing.ID, "", map[string]any{"name": existing.Name})
	pool, err := s.store.GetFleetPool(existing.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, pool)
}

func (s *Server) handleDeleteFleetPool(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	id := r.PathValue("id")
	if err := s.store.DeleteFleetPool(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAuditEvent(r, "fleet.pool.delete", "success", "fleet_pool", id, "", nil)
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
}

func (s *Server) saveFleetPoolMembers(poolID string, members []*storage.FleetPoolMember) error {
	for _, m := range members {
		if m == nil {
			continue
		}
		if strings.TrimSpace(m.ID) == "" {
			m.ID = uuid.New().String()
		}
		m.PoolID = poolID
	}
	return s.store.SaveFleetPoolMembers(poolID, members)
}

func (s *Server) loadFleetPool(w http.ResponseWriter, r *http.Request) (*storage.FleetPool, bool) {
	pool, err := s.store.GetFleetPool(r.PathValue("id"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "fleet pool not found")
			return nil, false
		}
		writeError(w, http.StatusInternalServerError, "failed to load fleet pool")
		return nil, false
	}
	return pool, true
}

// ---- generator metadata --------------------------------------------------

func (s *Server) handleListGeneratorMetadata(w http.ResponseWriter, r *http.Request) {
	limit, offset := parseLimitOffsetQuery(r, defaultFleetListLimit)
	if s.store == nil {
		writeJSON(w, http.StatusOK, map[string]any{"generators": []*storage.GeneratorMetadata{}, "count": 0, "limit": limit, "offset": offset})
		return
	}
	items, err := s.store.ListGeneratorMetadata(limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if items == nil {
		items = []*storage.GeneratorMetadata{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"generators": items, "count": len(items), "limit": limit, "offset": offset,
	})
}

// handlePatchGeneratorMetadata applies a partial update to a generator's
// metadata, creating the record if it does not yet exist. Patch semantics: an
// omitted field is unchanged; for fleet_pool_id an empty string clears membership.
func (s *Server) handlePatchGeneratorMetadata(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	generatorID := r.PathValue("id")
	metadata, err := s.store.GetGeneratorMetadata(generatorID)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusInternalServerError, "failed to load generator metadata")
			return
		}
		metadata = &storage.GeneratorMetadata{GeneratorID: generatorID}
	}
	var patch struct {
		Target         *string           `json:"target"`
		FleetPoolID    *string           `json:"fleet_pool_id"`
		Region         *string           `json:"region"`
		ProviderID     *string           `json:"provider_id"`
		ProviderStatus *string           `json:"provider_status"`
		Cordoned       *bool             `json:"cordoned"`
		Tags           map[string]string `json:"tags"`
	}
	if !decodeJSONBody(w, r, &patch) {
		return
	}
	if patch.Target != nil {
		metadata.Target = *patch.Target
	}
	if patch.FleetPoolID != nil {
		if strings.TrimSpace(*patch.FleetPoolID) == "" {
			metadata.FleetPoolID = nil
		} else {
			poolID := *patch.FleetPoolID
			metadata.FleetPoolID = &poolID
		}
	}
	if patch.Region != nil {
		metadata.Region = *patch.Region
	}
	if patch.ProviderID != nil {
		metadata.ProviderID = *patch.ProviderID
	}
	if patch.ProviderStatus != nil {
		metadata.ProviderStatus = *patch.ProviderStatus
	}
	if patch.Cordoned != nil {
		metadata.Cordoned = *patch.Cordoned
	}
	if patch.Tags != nil {
		metadata.Tags = patch.Tags
	}
	if err := s.store.SaveGeneratorMetadata(metadata); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAuditEvent(r, "generator.metadata.patch", "success", "generator", generatorID, "", map[string]any{
		"cordoned": metadata.Cordoned,
	})
	writeJSON(w, http.StatusOK, metadata)
}

// ---- provisioning requests -----------------------------------------------

func (s *Server) handleCreateProvisioningRequest(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	var req storage.ProvisioningRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	req.FleetPoolID = strings.TrimSpace(req.FleetPoolID)
	if req.FleetPoolID == "" {
		writeError(w, http.StatusBadRequest, "fleet_pool_id is required")
		return
	}
	if req.DesiredCount <= 0 {
		writeError(w, http.StatusBadRequest, "desired_count must be greater than zero")
		return
	}
	pool, err := s.store.GetFleetPool(req.FleetPoolID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "fleet pool not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load fleet pool")
		return
	}
	req.ID = uuid.New().String()
	req.ProjectID = normalizeProjectID(req.ProjectID)
	_, req.RequestedBySubject, _ = s.resolveAuditActor(r)
	now := time.Now()

	if s.fleetScaler != nil && s.fleetScaler.CanProvision() {
		// A real provisioner is wired: actually scale the pool's workload and
		// drive the request through its lifecycle.
		req.Status = "provisioning"
		if err := s.store.SaveProvisioningRequest(&req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.saveProvisioningEvent(req.ID, "requested", "provisioning requested", now)
		s.saveProvisioningEvent(req.ID, "provisioning", fmt.Sprintf("scaling pool %q toward %d generators", pool.Name, req.DesiredCount), now)

		workload, scaledTo, perr := s.fleetScaler.ScalePool(r.Context(), pool, req.DesiredCount)
		done := time.Now()
		req.CompletedAt = &done
		if perr != nil {
			req.Status = "failed"
			_ = s.store.SaveProvisioningRequest(&req)
			s.saveProvisioningEvent(req.ID, "failure", "provisioning failed: "+perr.Error(), done)
		} else {
			req.Status = "success"
			_ = s.store.SaveProvisioningRequest(&req)
			s.saveProvisioningEvent(req.ID, "success", fmt.Sprintf("scaled %s to %d replicas", workload, scaledTo), done)
			pool.DesiredSize = scaledTo
			if err := s.store.SaveFleetPool(pool); err != nil {
				slog.Warn("failed to persist pool desired size after provisioning", "pool_id", pool.ID, "err", err)
			}
		}
		s.recordAuditEvent(r, "provisioning.request.create", "success", "provisioning_request", req.ID, req.ProjectID, map[string]any{
			"fleet_pool_id": req.FleetPoolID, "desired_count": req.DesiredCount, "status": req.Status, "workload": workload,
		})
		s.writeProvisioningRequest(w, req.ID)
		return
	}

	// No live provisioner: resolve the request to a terminal not_configured state
	// with an explanatory event instead of pretending.
	req.Status = "not_configured"
	req.CompletedAt = &now
	req.Events = nil
	if err := s.store.SaveProvisioningRequest(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.saveProvisioningEvent(req.ID, "requested", "provisioning requested", now)
	s.saveProvisioningEvent(req.ID, "not_configured", "no cloud or Kubernetes provisioner is configured for this controller", now)
	s.recordAuditEvent(r, "provisioning.request.create", "success", "provisioning_request", req.ID, req.ProjectID, map[string]any{
		"fleet_pool_id": req.FleetPoolID, "desired_count": req.DesiredCount, "status": req.Status,
	})
	s.writeProvisioningRequest(w, req.ID)
}

func (s *Server) saveProvisioningEvent(requestID, eventType, message string, ts time.Time) {
	if err := s.store.SaveProvisioningEvent(&storage.ProvisioningEvent{
		ID: uuid.New().String(), RequestID: requestID, EventType: eventType, Message: message, Timestamp: ts,
	}); err != nil {
		slog.Warn("failed to persist provisioning event", "request_id", requestID, "event_type", eventType, "err", err)
	}
}

func (s *Server) writeProvisioningRequest(w http.ResponseWriter, id string) {
	created, err := s.store.GetProvisioningRequest(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) handleGetProvisioningRequest(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	req, err := s.store.GetProvisioningRequest(r.PathValue("id"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "provisioning request not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load provisioning request")
		return
	}
	writeJSON(w, http.StatusOK, req)
}
