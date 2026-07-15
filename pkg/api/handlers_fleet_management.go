package api

// Fleet management: cloud-managed generator pools, persistent generator
// metadata (labels/region/cordon/pool membership), and cloud provisioning
// requests. All endpoints are system-admin-scoped infrastructure operations.
// Provisioning has no live cloud/Kubernetes backend yet, so requests resolve to
// status "not_configured" with an explanatory event rather than silently
// succeeding — tracked under the fleet_management parity dimension.

import (
	"context"
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
	if !s.requireExecutionLeader(w) {
		return
	}
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
	s.broadcastFleetEvent("pool_created", map[string]any{"pool_id": req.ID, "name": req.Name})
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
	if !s.requireExecutionLeader(w) {
		return
	}
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
	s.broadcastFleetEvent("pool_updated", map[string]any{"pool_id": existing.ID, "name": existing.Name, "status": existing.Status})
	pool, err := s.store.GetFleetPool(existing.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, pool)
}

func (s *Server) handleDeleteFleetPool(w http.ResponseWriter, r *http.Request) {
	if !s.requireExecutionLeader(w) {
		return
	}
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
	s.broadcastFleetEvent("pool_deleted", map[string]any{"pool_id": id})
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
	if !s.requireExecutionLeader(w) {
		return
	}
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
			poolID := strings.TrimSpace(*patch.FleetPoolID)
			if _, err := s.store.GetFleetPool(poolID); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					writeError(w, http.StatusNotFound, "fleet pool not found")
					return
				}
				writeError(w, http.StatusInternalServerError, "failed to load fleet pool")
				return
			}
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
	if patch.Cordoned != nil {
		if err := s.reconcileMetadataCordonState(generatorID, *patch.Cordoned); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	s.recordAuditEvent(r, "generator.metadata.patch", "success", "generator", generatorID, "", map[string]any{
		"cordoned": metadata.Cordoned,
	})
	s.broadcastFleetEvent("generator_metadata_updated", map[string]any{"generator_id": generatorID, "cordoned": metadata.Cordoned, "fleet_pool_id": metadata.FleetPoolID})
	writeJSON(w, http.StatusOK, metadata)
}

func (s *Server) reconcileMetadataCordonState(generatorID string, cordoned bool) error {
	generatorID = strings.TrimSpace(generatorID)
	if generatorID == "" || s == nil || s.runner == nil {
		return nil
	}
	backend, ok := s.runner.getExecutionBackend().(drainableExecutionBackend)
	if !ok {
		return nil
	}
	state := "active"
	if cordoned {
		state = "cordoned"
	}
	if identityBackend, ok := backend.(identityDrainableExecutionBackend); ok {
		// If the generator isn't currently mapped (offline, or not yet probed after
		// a restart) the cordon can't be applied to the live drain state now — but
		// the metadata cordon is persisted, and applyMetadataCordonOnReconnect will
		// enforce it once the generator (re)connects. So don't fail the PATCH.
		if identityBackend.GeneratorTargetForID(generatorID) == "" {
			return nil
		}
		if _, err := identityBackend.SetGeneratorDrainStateByID(generatorID, state); err != nil {
			return err
		}
	} else if err := backend.SetGeneratorDrainState(generatorID, state); err != nil {
		return err
	}
	if persistent, ok := backend.(persistentDrainBackend); ok {
		return s.saveGeneratorDrainStates(persistent.GeneratorDrainStates())
	}
	return nil
}

// drainStateReader reads a generator's live drain state by id (implemented by the
// distributed backend). Kept as a narrow optional interface so it need not be
// satisfied by every drainable backend / test stub.
type drainStateReader interface {
	GeneratorDrainStateByID(generatorID string) string
}

// reconcileMetadataCordonOnReconnect reconciles the live drain state to the
// persisted metadata cordon when a generator (re)connects. This is what makes a
// cordon (or un-cordon) issued while a generator was offline actually take effect:
// the PATCH persists the intent in generator metadata, and this hook (registered on
// the backend's identity-record event) enforces it once the generator has a live
// target mapping. It reconciles BOTH directions but never clobbers a non-cordon
// drain state (e.g. "draining" set by the pod watcher).
func (s *Server) applyMetadataCordonOnReconnect(backend identityDrainableExecutionBackend, generatorID string) {
	generatorID = strings.TrimSpace(generatorID)
	if s == nil || s.store == nil || backend == nil || generatorID == "" {
		return
	}
	metadata, err := s.store.GetGeneratorMetadata(generatorID)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			slog.Warn("reconcile cordon on reconnect: metadata lookup failed", "generator_id", generatorID, "err", err)
		}
		return
	}
	if metadata == nil {
		return // no persisted intent to reconcile
	}
	current := "active"
	if reader, ok := backend.(drainStateReader); ok {
		current = reader.GeneratorDrainStateByID(generatorID)
	}
	var desired string
	switch {
	case metadata.Cordoned && current != "cordoned" && current != "draining":
		// Apply a cordon issued while offline. Don't override a live "draining"
		// (pod termination) with "cordoned".
		desired = "cordoned"
	case !metadata.Cordoned && current == "cordoned":
		// Clear a stale operator cordon that was removed while the generator was
		// offline. Leave "draining" and already-"active" untouched.
		desired = "active"
	default:
		return // already consistent, or a non-cordon drain state we must not clobber
	}
	if _, err := backend.SetGeneratorDrainStateByID(generatorID, desired); err != nil {
		slog.Warn("reconcile cordon on reconnect: set drain state failed", "generator_id", generatorID, "desired", desired, "err", err)
		return
	}
	if persistent, ok := backend.(persistentDrainBackend); ok {
		if err := s.saveGeneratorDrainStates(persistent.GeneratorDrainStates()); err != nil {
			slog.Warn("reconcile cordon on reconnect: persist drain states failed", "generator_id", generatorID, "err", err)
		}
	}
}

// ---- provisioning requests -----------------------------------------------

type provisioningRequestCASStore interface {
	CreateProvisioningRequestIfAbsent(req *storage.ProvisioningRequest) (bool, error)
	TransitionProvisioningRequest(req *storage.ProvisioningRequest, expectedRevision int, expectedAttemptToken string) (bool, error)
}

func (s *Server) createProvisioningRequest(req *storage.ProvisioningRequest) error {
	if cas, ok := s.store.(provisioningRequestCASStore); ok {
		created, err := cas.CreateProvisioningRequestIfAbsent(req)
		if err != nil {
			return err
		}
		if !created {
			return fmt.Errorf("provisioning request %s already exists", req.ID)
		}
		return nil
	}
	return s.store.SaveProvisioningRequest(req)
}

func (s *Server) transitionProvisioningRequest(req *storage.ProvisioningRequest, expectedRevision int, expectedAttemptToken string) error {
	if cas, ok := s.store.(provisioningRequestCASStore); ok {
		changed, err := cas.TransitionProvisioningRequest(req, expectedRevision, expectedAttemptToken)
		if err != nil {
			return err
		}
		if !changed {
			return fmt.Errorf("provisioning request changed concurrently")
		}
		return nil
	}
	return s.store.SaveProvisioningRequest(req)
}

func (s *Server) handleCreateProvisioningRequest(w http.ResponseWriter, r *http.Request) {
	if !s.requireExecutionLeader(w) {
		return
	}
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
	if status := strings.ToLower(strings.TrimSpace(pool.Status)); status != "" && status != "active" {
		writeError(w, http.StatusConflict, fmt.Sprintf("fleet pool %q is %s", firstNonEmpty(pool.Name, pool.ID), status))
		return
	}
	req.ID = uuid.New().String()
	req.Revision = 1
	req.AttemptToken = uuid.New().String()
	req.ProjectID = normalizeProjectID(req.ProjectID)
	_, req.RequestedBySubject, _ = s.resolveAuditActor(r)
	now := time.Now()

	if s.fleetScaler != nil && s.fleetScaler.CanProvision() {
		// A real provisioner is wired: actually scale the pool's workload and
		// drive the request through its lifecycle.
		req.Status = "provisioning"
		if err := s.createProvisioningRequest(&req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.saveProvisioningEvent(req.ID, "requested", "provisioning requested", now)
		s.saveProvisioningEvent(req.ID, "provisioning", fmt.Sprintf("scaling pool %q toward %d generators", pool.Name, req.DesiredCount), now)

		scaleCtx, cancelScale := s.runner.executionOperationContext(r.Context())
		workload, scaledTo, perr := s.fleetScaler.ScalePool(scaleCtx, pool, req.DesiredCount)
		cancelScale()
		if s.runner == nil || !s.runner.isExecutionLeader() {
			writeError(w, http.StatusConflict, "execution leadership changed while provisioning; result left for the active leader to reconcile")
			return
		}
		done := time.Now()
		expectedRevision := req.Revision
		expectedAttemptToken := req.AttemptToken
		req.Revision++
		req.CompletedAt = &done
		if perr != nil {
			req.Status = "failed"
			if err := s.transitionProvisioningRequest(&req, expectedRevision, expectedAttemptToken); err != nil {
				slog.Error("failed to persist failed provisioning request", "request_id", req.ID, "error", err)
				writeError(w, http.StatusInternalServerError, "provisioning failed and its terminal state could not be persisted")
				return
			}
			s.saveProvisioningEvent(req.ID, "failure", "provisioning failed: "+perr.Error(), done)
		} else {
			req.Status = "success"
			if err := s.transitionProvisioningRequest(&req, expectedRevision, expectedAttemptToken); err != nil {
				slog.Error("failed to persist successful provisioning request", "request_id", req.ID, "error", err)
				writeError(w, http.StatusInternalServerError, "fleet was scaled but the provisioning result could not be persisted")
				return
			}
			s.saveProvisioningEvent(req.ID, "success", fmt.Sprintf("scaled %s to %d replicas", workload, scaledTo), done)
			pool.DesiredSize = scaledTo
			if err := s.store.SaveFleetPool(pool); err != nil {
				slog.Warn("failed to persist pool desired size after provisioning", "pool_id", pool.ID, "err", err)
			}
		}
		s.recordAuditEvent(r, "provisioning.request.create", "success", "provisioning_request", req.ID, req.ProjectID, map[string]any{
			"fleet_pool_id": req.FleetPoolID, "desired_count": req.DesiredCount, "status": req.Status, "workload": workload,
		})
		s.broadcastFleetEvent("provisioning_request_updated", map[string]any{"request_id": req.ID, "fleet_pool_id": req.FleetPoolID, "status": req.Status, "desired_count": req.DesiredCount})
		s.writeProvisioningRequest(w, req.ID)
		return
	}

	// No live provisioner: resolve the request to a terminal not_configured state
	// with an explanatory event instead of pretending.
	notConfiguredMessage := "no cloud or Kubernetes provisioner is configured for this controller"
	if s.fleetScaler != nil {
		if description := strings.TrimSpace(s.fleetScaler.Describe().Description); description != "" {
			notConfiguredMessage = description
		}
	}
	req.Status = "not_configured"
	req.CompletedAt = &now
	req.Events = nil
	if err := s.createProvisioningRequest(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.saveProvisioningEvent(req.ID, "requested", "provisioning requested", now)
	s.saveProvisioningEvent(req.ID, "not_configured", notConfiguredMessage, now)
	s.recordAuditEvent(r, "provisioning.request.create", "success", "provisioning_request", req.ID, req.ProjectID, map[string]any{
		"fleet_pool_id": req.FleetPoolID, "desired_count": req.DesiredCount, "status": req.Status,
	})
	s.broadcastFleetEvent("provisioning_request_updated", map[string]any{"request_id": req.ID, "fleet_pool_id": req.FleetPoolID, "status": req.Status, "desired_count": req.DesiredCount})
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
	s.writeProvisioningRequestStatus(w, id, http.StatusCreated)
}

func (s *Server) writeProvisioningRequestStatus(w http.ResponseWriter, id string, status int) {
	created, err := s.store.GetProvisioningRequest(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, status, created)
}

func (s *Server) handleListProvisioningRequests(w http.ResponseWriter, r *http.Request) {
	limit, offset := parseLimitOffsetQuery(r, defaultFleetListLimit)
	fleetPoolID := strings.TrimSpace(r.URL.Query().Get("fleet_pool_id"))
	if s.store == nil {
		writeJSON(w, http.StatusOK, map[string]any{"provisioning_requests": []*storage.ProvisioningRequest{}, "count": 0, "limit": limit, "offset": offset})
		return
	}
	items, err := s.store.ListProvisioningRequests(fleetPoolID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list provisioning requests")
		return
	}
	if items == nil {
		items = []*storage.ProvisioningRequest{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"provisioning_requests": items, "count": len(items), "limit": limit, "offset": offset})
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

func (s *Server) handleCancelProvisioningRequest(w http.ResponseWriter, r *http.Request) {
	if !s.requireExecutionLeader(w) {
		return
	}
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
	if strings.EqualFold(req.Status, "cancelled") {
		s.writeProvisioningRequestStatus(w, req.ID, http.StatusOK)
		return
	}
	if !canCancelProvisioningStatus(req.Status) {
		writeError(w, http.StatusConflict, fmt.Sprintf("provisioning request is %s and cannot be cancelled", req.Status))
		return
	}
	now := time.Now()
	expectedRevision := req.Revision
	expectedAttemptToken := req.AttemptToken
	req.Status = "cancelled"
	req.Revision++
	req.AttemptToken = uuid.New().String()
	req.CompletedAt = &now
	if err := s.transitionProvisioningRequest(req, expectedRevision, expectedAttemptToken); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to cancel provisioning request")
		return
	}
	s.saveProvisioningEvent(req.ID, "cancelled", "provisioning request cancelled by operator", now)
	s.recordAuditEvent(r, "provisioning.request.cancel", "success", "provisioning_request", req.ID, req.ProjectID, map[string]any{
		"fleet_pool_id": req.FleetPoolID, "desired_count": req.DesiredCount,
	})
	s.broadcastFleetEvent("provisioning_request_cancelled", map[string]any{"request_id": req.ID, "fleet_pool_id": req.FleetPoolID, "status": req.Status})
	s.writeProvisioningRequestStatus(w, req.ID, http.StatusOK)
}

func (s *Server) handleRetryProvisioningRequest(w http.ResponseWriter, r *http.Request) {
	if !s.requireExecutionLeader(w) {
		return
	}
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
	if !canRetryProvisioningStatus(req.Status) {
		writeError(w, http.StatusConflict, fmt.Sprintf("provisioning request is %s and cannot be retried", req.Status))
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
	if status := strings.ToLower(strings.TrimSpace(pool.Status)); status != "" && status != "active" {
		writeError(w, http.StatusConflict, fmt.Sprintf("fleet pool %q is %s", firstNonEmpty(pool.Name, pool.ID), status))
		return
	}

	now := time.Now()
	expectedRevision := req.Revision
	expectedAttemptToken := req.AttemptToken
	req.CompletedAt = nil
	req.Status = "provisioning"
	req.Revision++
	req.AttemptToken = uuid.New().String()
	if err := s.transitionProvisioningRequest(req, expectedRevision, expectedAttemptToken); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to retry provisioning request")
		return
	}
	s.saveProvisioningEvent(req.ID, "retry", "provisioning retry requested", now)

	scaleCtx, cancelScale := s.runner.executionOperationContext(r.Context())
	status, workload, scaledTo, message := s.executeProvisioningScale(scaleCtx, req, pool)
	cancelScale()
	if s.runner == nil || !s.runner.isExecutionLeader() {
		writeError(w, http.StatusConflict, "execution leadership changed while provisioning; result left for the active leader to reconcile")
		return
	}
	done := time.Now()
	expectedRevision = req.Revision
	expectedAttemptToken = req.AttemptToken
	req.Status = status
	req.Revision++
	req.CompletedAt = &done
	if err := s.transitionProvisioningRequest(req, expectedRevision, expectedAttemptToken); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to persist provisioning retry result")
		return
	}
	eventType := status
	if status == "failed" {
		eventType = "failure"
	}
	s.saveProvisioningEvent(req.ID, eventType, message, done)
	if status == "success" {
		pool.DesiredSize = scaledTo
		if err := s.store.SaveFleetPool(pool); err != nil {
			slog.Warn("failed to persist pool desired size after provisioning retry", "pool_id", pool.ID, "err", err)
		}
	}
	s.recordAuditEvent(r, "provisioning.request.retry", "success", "provisioning_request", req.ID, req.ProjectID, map[string]any{
		"fleet_pool_id": req.FleetPoolID, "desired_count": req.DesiredCount, "status": req.Status, "workload": workload,
	})
	s.broadcastFleetEvent("provisioning_request_updated", map[string]any{"request_id": req.ID, "fleet_pool_id": req.FleetPoolID, "status": req.Status, "desired_count": req.DesiredCount})
	s.writeProvisioningRequestStatus(w, req.ID, http.StatusOK)
}

func (s *Server) executeProvisioningScale(ctx context.Context, req *storage.ProvisioningRequest, pool *storage.FleetPool) (status string, workload string, scaledTo int, message string) {
	if s.fleetScaler != nil && s.fleetScaler.CanProvision() {
		workload, scaledTo, err := s.fleetScaler.ScalePool(ctx, pool, req.DesiredCount)
		if err != nil {
			return "failed", workload, scaledTo, "provisioning failed: " + err.Error()
		}
		return "success", workload, scaledTo, fmt.Sprintf("scaled %s to %d replicas", workload, scaledTo)
	}
	notConfiguredMessage := "no cloud or Kubernetes provisioner is configured for this controller"
	if s.fleetScaler != nil {
		if description := strings.TrimSpace(s.fleetScaler.Describe().Description); description != "" {
			notConfiguredMessage = description
		}
	}
	return "not_configured", "", 0, notConfiguredMessage
}

// reconcileProvisioningRequests resumes durable requests that were left in
// provisioning state when a controller lost execution leadership. The CAS
// transition makes this safe when a takeover races with another worker.
func (s *Server) reconcileProvisioningRequests(ctx context.Context) error {
	if s.store == nil || s.fleetScaler == nil || !s.fleetScaler.CanProvision() || s.runner == nil || !s.runner.holdsExecutionLeadership() {
		return nil
	}
	const pageSize = 250
	for offset := 0; ; offset += pageSize {
		requests, err := s.store.ListProvisioningRequests("", pageSize, offset)
		if err != nil {
			return err
		}
		for _, req := range requests {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if req == nil || strings.ToLower(strings.TrimSpace(req.Status)) != "provisioning" || !s.runner.holdsExecutionLeadership() {
				continue
			}
			pool, err := s.store.GetFleetPool(req.FleetPoolID)
			if err != nil {
				slog.Warn("failed to load fleet pool during provisioning reconcile", "request_id", req.ID, "error", err)
				continue
			}
			// Claim the recovery attempt before touching the external scaler.
			expectedRevision, expectedToken := req.Revision, req.AttemptToken
			req.Revision++
			req.AttemptToken = uuid.New().String()
			if err := s.transitionProvisioningRequest(req, expectedRevision, expectedToken); err != nil {
				continue
			}
			expectedRevision, expectedToken = req.Revision, req.AttemptToken
			status, workload, scaledTo, message := s.executeProvisioningScale(ctx, req, pool)
			if ctx.Err() != nil || !s.runner.holdsExecutionLeadership() {
				return firstNonNilError(ctx.Err(), errNotExecutionLeader)
			}
			now := time.Now()
			req.Status = status
			req.Revision++
			req.CompletedAt = &now
			if err := s.transitionProvisioningRequest(req, expectedRevision, expectedToken); err != nil {
				slog.Warn("provisioning reconcile lost race or failed to persist", "request_id", req.ID, "error", err)
				continue
			}
			s.saveProvisioningEvent(req.ID, map[bool]string{true: "success", false: "failure"}[status == "success"], message, now)
			if status == "success" {
				pool.DesiredSize = scaledTo
				if err := s.store.SaveFleetPool(pool); err != nil {
					slog.Warn("failed to persist pool desired size after provisioning reconcile", "pool_id", pool.ID, "error", err)
				}
			}
			s.broadcastFleetEvent("provisioning_request_updated", map[string]any{"request_id": req.ID, "fleet_pool_id": req.FleetPoolID, "status": req.Status, "desired_count": req.DesiredCount, "workload": workload})
		}
		if len(requests) < pageSize {
			return nil
		}
	}
}

func firstNonNilError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func canCancelProvisioningStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "pending", "accepted", "scaling", "waiting_ready", "provisioning":
		return true
	default:
		return false
	}
}

func canRetryProvisioningStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "failed", "not_configured", "cancelled", "timed_out":
		return true
	default:
		return false
	}
}
