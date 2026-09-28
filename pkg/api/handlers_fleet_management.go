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
	"github.com/jasonczech/apexload/pkg/compute"
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
	if err := validateFleetPoolSizing(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// The domain check runs before the cloud_provider requirement because a
	// domain-scoped pool DERIVES its provider (and may inherit the domain's
	// default cluster) rather than having to restate it.
	if status, err := s.validatePoolAgainstDomainTarget(&req); err != nil {
		writeError(w, status, err.Error())
		return
	}
	if status, err := s.validatePoolExecutionTarget(&req); err != nil {
		writeError(w, status, err.Error())
		return
	}
	if status, err := s.validatePoolColocation(&req); err != nil {
		writeError(w, status, err.Error())
		return
	}
	if req.CloudProvider == "" {
		writeError(w, http.StatusBadRequest, "cloud_provider is required")
		return
	}
	req.ID = uuid.New().String()
	if req.Status == "" {
		req.Status = "active"
	}
	prepareFleetPoolMembers(req.ID, req.Members)
	if conflict := s.checkGeneratorPoolExclusivity(req.ID, memberGeneratorIDs(req.Members)); conflict != nil {
		writeError(w, http.StatusConflict, conflict.Error())
		return
	}
	creator, supported := s.store.(interface {
		CreateFleetPoolWithMembers(pool *storage.FleetPool, members []*storage.FleetPoolMember) error
	})
	if !supported {
		writeError(w, http.StatusNotImplemented, "atomic fleet pool creation is not supported by this storage backend")
		return
	}
	if err := creator.CreateFleetPoolWithMembers(&req, req.Members); err != nil {
		writeFleetMembershipError(w, err, http.StatusBadRequest)
		return
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

// fleetPoolUpdateRequest overlays presence-aware binding fields on the pool
// shape. The embedded struct supplies every replace-on-omit field; the two
// pointers shadow their embedded counterparts during JSON decoding (the
// shallowest field wins), so an omitted binding and an explicitly cleared one
// are distinguishable — which is what makes unscoping possible at all.
type fleetPoolUpdateRequest struct {
	storage.FleetPool
	DomainID                *string `json:"domain_id,omitempty"`
	ClusterID               *string `json:"cluster_id,omitempty"`
	ExecutionTargetID       *string `json:"execution_target_id,omitempty"`
	LifecycleMode           *string `json:"lifecycle_mode,omitempty"`
	WarmFloor               *int    `json:"warm_floor,omitempty"`
	ReadinessTimeoutSeconds *int    `json:"readiness_timeout_seconds,omitempty"`
	IdleGraceSeconds        *int    `json:"idle_grace_seconds,omitempty"`
	RuntimeProfileID        *string `json:"runtime_profile_id,omitempty"`
	CapacityAdmission       *string `json:"capacity_admission,omitempty"`
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
	var req fleetPoolUpdateRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if name := strings.TrimSpace(req.Name); name != "" {
		existing.Name = name
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
	// Presence-aware, following the ClientSecretEnvVar convention: an OMITTED
	// binding is kept, and an explicit empty string clears it.
	//
	// Plain replace-on-omit would silently unbind a pool on any partial update,
	// widening its blast radius. But treating empty as "keep" leaves a pool
	// permanently scoped once set, and the dashboard's "Unscoped" option then
	// reports success while changing nothing — a silent failure.
	if req.DomainID != nil {
		existing.DomainID = strings.TrimSpace(*req.DomainID)
	}
	if req.ClusterID != nil {
		existing.ClusterID = strings.TrimSpace(*req.ClusterID)
	}
	if req.ExecutionTargetID != nil {
		existing.ExecutionTargetID = strings.TrimSpace(*req.ExecutionTargetID)
	}
	if req.LifecycleMode != nil {
		existing.LifecycleMode = strings.TrimSpace(*req.LifecycleMode)
	}
	if req.WarmFloor != nil {
		existing.WarmFloor = *req.WarmFloor
	}
	if req.ReadinessTimeoutSeconds != nil {
		existing.ReadinessTimeoutSeconds = *req.ReadinessTimeoutSeconds
	}
	if req.IdleGraceSeconds != nil {
		existing.IdleGraceSeconds = *req.IdleGraceSeconds
	}
	if req.RuntimeProfileID != nil {
		existing.RuntimeProfileID = strings.TrimSpace(*req.RuntimeProfileID)
	}
	if req.CapacityAdmission != nil {
		existing.CapacityAdmission = strings.TrimSpace(*req.CapacityAdmission)
	}
	if err := validateFleetPoolSizing(existing); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if status, err := s.validatePoolAgainstDomainTarget(existing); err != nil {
		writeError(w, status, err.Error())
		return
	}
	if status, err := s.validatePoolExecutionTarget(existing); err != nil {
		writeError(w, status, err.Error())
		return
	}
	if status, err := s.validatePoolColocation(existing); err != nil {
		writeError(w, status, err.Error())
		return
	}
	prepareFleetPoolMembers(existing.ID, req.Members)
	if conflict := s.checkGeneratorPoolExclusivity(existing.ID, memberGeneratorIDs(req.Members)); conflict != nil {
		writeError(w, http.StatusConflict, conflict.Error())
		return
	}
	updater, supported := s.store.(interface {
		UpdateFleetPoolIfUnchanged(pool *storage.FleetPool, members []*storage.FleetPoolMember, replaceMembers bool, expectedUpdatedAt time.Time) (bool, error)
	})
	if !supported {
		writeError(w, http.StatusNotImplemented, "optimistic fleet pool updates are not supported by this storage backend")
		return
	}
	updated, err := updater.UpdateFleetPoolIfUnchanged(existing, req.Members, req.Members != nil, existing.UpdatedAt)
	if err != nil {
		writeFleetMembershipError(w, err, http.StatusBadRequest)
		return
	}
	if !updated {
		writeError(w, http.StatusConflict, "fleet pool changed while the update was in flight; reload and retry")
		return
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
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "fleet pool not found")
			return
		}
		if errors.Is(err, storage.ErrFleetPoolHasActiveProvisioning) {
			writeError(w, http.StatusConflict, "fleet pool has an active provisioning request")
			return
		}
		if errors.Is(err, storage.ErrFleetPoolHasLiveCapacity) {
			writeError(w, http.StatusConflict, "fleet pool still owns live cloud capacity; drain or terminate it before deleting the pool")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAuditEvent(r, "fleet.pool.delete", "success", "fleet_pool", id, "", nil)
	s.broadcastFleetEvent("pool_deleted", map[string]any{"pool_id": id})
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
}

func (s *Server) saveFleetPoolMembers(poolID string, members []*storage.FleetPoolMember) error {
	prepareFleetPoolMembers(poolID, members)
	return s.store.SaveFleetPoolMembers(poolID, members)
}

func prepareFleetPoolMembers(poolID string, members []*storage.FleetPoolMember) {
	for _, m := range members {
		if m == nil {
			continue
		}
		if strings.TrimSpace(m.ID) == "" {
			m.ID = uuid.New().String()
		}
		m.PoolID = poolID
	}
}

func validateFleetPoolSizing(pool *storage.FleetPool) error {
	if pool == nil {
		return errors.New("fleet pool is required")
	}
	if pool.MinSize < 0 || pool.MaxSize < 0 || pool.DesiredSize < 0 {
		return errors.New("min_size, max_size, and desired_size must be nonnegative")
	}
	if pool.MaxSize > 0 && pool.MinSize > pool.MaxSize {
		return errors.New("min_size cannot exceed max_size")
	}
	if pool.DesiredSize < pool.MinSize {
		return errors.New("desired_size cannot be lower than min_size")
	}
	if pool.MaxSize > 0 && pool.DesiredSize > pool.MaxSize {
		return errors.New("desired_size cannot exceed max_size")
	}
	if pool.WarmFloor < 0 || pool.ReadinessTimeoutSeconds < 0 || pool.IdleGraceSeconds < 0 {
		return errors.New("warm_floor, readiness_timeout_seconds, and idle_grace_seconds must be nonnegative")
	}
	if pool.MaxSize > 0 && pool.WarmFloor > pool.MaxSize {
		return errors.New("warm_floor cannot exceed max_size")
	}
	if pool.LifecycleMode != "" && pool.LifecycleMode != storage.FleetLifecycleStanding &&
		pool.LifecycleMode != storage.FleetLifecycleOnDemand && pool.LifecycleMode != storage.FleetLifecycleWarmBurst {
		return fmt.Errorf("unsupported lifecycle_mode %q", pool.LifecycleMode)
	}
	if pool.CapacityAdmission != "" && pool.CapacityAdmission != storage.CapacityAdmissionStrict &&
		pool.CapacityAdmission != storage.CapacityAdmissionBestEffort {
		return fmt.Errorf("unsupported capacity_admission %q", pool.CapacityAdmission)
	}
	return nil
}

// runtimeFleetPoolTargetCount sizes a pool's runtime membership using the shared
// definition in fleet_pool_membership.go. Provisioning may scale only targets
// operators have associated ahead of time; scaling a StatefulSet does not itself
// create a durable pool binding.
func runtimeFleetPoolTargetCount(store storage.Store, pool *storage.FleetPool) (int, error) {
	if store == nil || pool == nil {
		return 0, nil
	}
	metadata, err := store.ListGeneratorMetadata(0, 0)
	if err != nil {
		return 0, err
	}
	return len(fleetPoolTargets(pool, metadata)), nil
}

func validateProvisioningRuntimeTargets(store storage.Store, pool *storage.FleetPool, desiredCount int) error {
	count, err := runtimeFleetPoolTargetCount(store, pool)
	if err != nil {
		return fmt.Errorf("load fleet pool runtime membership: %w", err)
	}
	if count < desiredCount {
		return fmt.Errorf("fleet pool has %d associated runtime target(s), but desired_count is %d; add generator metadata with a runtime target and this pool assignment before provisioning", count, desiredCount)
	}
	return nil
}

// effectiveProvisioningDesiredCount validates the requested replica count
// against the pool contract before runtime-target validation or external scale
// work begins. Keeping the returned effective count explicit prevents callers
// from validating one count while a provisioner silently applies another.
func effectiveProvisioningDesiredCount(pool *storage.FleetPool, desiredCount int) (int, error) {
	if pool == nil {
		return 0, errors.New("fleet pool is required")
	}
	if desiredCount < 0 {
		return 0, errors.New("desired_count must not be negative")
	}
	if desiredCount == 0 && !isPaaSFleetPool(pool) {
		return 0, errors.New("desired_count must be greater than zero")
	}
	if desiredCount < pool.MinSize {
		return 0, fmt.Errorf("desired_count %d cannot be lower than fleet pool min_size %d", desiredCount, pool.MinSize)
	}
	if pool.MaxSize > 0 && desiredCount > pool.MaxSize {
		return 0, fmt.Errorf("desired_count %d cannot exceed fleet pool max_size %d", desiredCount, pool.MaxSize)
	}
	return desiredCount, nil
}

func isPaaSFleetPool(pool *storage.FleetPool) bool {
	if pool == nil || strings.TrimSpace(pool.ExecutionTargetID) == "" {
		return false
	}
	switch storage.NormalizeCloudProvider(pool.CloudProvider) {
	case storage.DeploymentProviderAzureContainerApps, storage.DeploymentProviderAWSECSFargate:
		return true
	default:
		return false
	}
}

func updateFleetPoolDesiredSize(store storage.Store, poolID string, desiredSize int) error {
	updater, ok := store.(interface {
		UpdateFleetPoolDesiredSize(id string, desiredSize int, updatedAt time.Time) error
	})
	if !ok {
		return errors.New("targeted fleet desired-size updates are not supported by this storage backend")
	}
	return updater.UpdateFleetPoolDesiredSize(poolID, desiredSize, time.Now())
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
	generatorID := strings.TrimSpace(r.PathValue("id"))
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
		target := strings.TrimSpace(*patch.Target)
		patch.Target = &target
	}
	metadataPatch := storage.GeneratorMetadataPatch{Target: patch.Target, Region: patch.Region, ProviderID: patch.ProviderID, ProviderStatus: patch.ProviderStatus, Cordoned: patch.Cordoned, Tags: patch.Tags, TagsSet: patch.Tags != nil}
	if patch.FleetPoolID != nil {
		metadataPatch.FleetPoolIDSet = true
		if strings.TrimSpace(*patch.FleetPoolID) == "" {
			metadataPatch.FleetPoolID = nil
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
			// One generator, one pool: reassigning requires removing it from the
			// pool that holds it, so a generator can never span two domains.
			if conflict := s.checkGeneratorPoolExclusivity(poolID, []string{generatorID}); conflict != nil {
				writeError(w, http.StatusConflict, conflict.Error())
				return
			}
			metadataPatch.FleetPoolID = &poolID
		}
	}
	if err := validateGeneratorMetadataRuntimeTarget(s.store, generatorID, metadataPatch); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	patcher, supported := s.store.(interface {
		PatchGeneratorMetadata(generatorID string, patch storage.GeneratorMetadataPatch) (*storage.GeneratorMetadata, error)
	})
	if !supported {
		writeError(w, http.StatusNotImplemented, "atomic generator metadata patches are not supported by this storage backend")
		return
	}
	previousState := "active"
	liveApplied := false
	if patch.Cordoned != nil {
		previousState = s.currentGeneratorLifecycleState(generatorID, generatorID)
		var err error
		liveApplied, err = s.reconcileMetadataCordonState(generatorID, *patch.Cordoned)
		if err != nil {
			if liveApplied {
				_ = s.restoreGeneratorLifecycleState(generatorID, generatorID, previousState)
			}
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	metadata, err := patcher.PatchGeneratorMetadata(generatorID, metadataPatch)
	if err != nil {
		// Reconcile a lost acknowledgement before compensating the live lifecycle
		// state. The storage PATCH is atomic, so a matching authoritative row means
		// the intended mutation committed despite the returned error.
		if current, readErr := s.store.GetGeneratorMetadata(generatorID); readErr == nil && generatorMetadataPatchApplied(current, metadataPatch) {
			metadata = current
			err = nil
		}
	}
	if err != nil {
		if liveApplied {
			if rollbackErr := s.restoreGeneratorLifecycleState(generatorID, generatorID, previousState); rollbackErr != nil {
				writeError(w, http.StatusInternalServerError, "generator metadata update failed and the prior lifecycle state could not be restored: "+errors.Join(err, rollbackErr).Error())
				return
			}
		}
		if metadataPatch.FleetPoolIDSet && metadataPatch.FleetPoolID != nil && errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "fleet pool not found")
			return
		}
		writeFleetMembershipError(w, err, http.StatusBadRequest)
		return
	}
	s.recordAuditEvent(r, "generator.metadata.patch", "success", "generator", generatorID, "", map[string]any{
		"cordoned": metadata.Cordoned,
	})
	s.broadcastFleetEvent("generator_metadata_updated", map[string]any{"generator_id": generatorID, "cordoned": metadata.Cordoned, "fleet_pool_id": metadata.FleetPoolID})
	writeJSON(w, http.StatusOK, metadata)
}

func validateGeneratorMetadataRuntimeTarget(store storage.Store, generatorID string, patch storage.GeneratorMetadataPatch) error {
	if store == nil {
		return nil
	}
	var current *storage.GeneratorMetadata
	if existing, err := store.GetGeneratorMetadata(generatorID); err == nil {
		current = existing
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("load current generator metadata: %w", err)
	}
	effectiveTarget := ""
	if current != nil {
		effectiveTarget = strings.TrimSpace(current.Target)
	}
	if patch.Target != nil {
		effectiveTarget = strings.TrimSpace(*patch.Target)
	}
	var effectivePoolID *string
	if current != nil {
		effectivePoolID = current.FleetPoolID
	}
	if patch.FleetPoolIDSet {
		effectivePoolID = patch.FleetPoolID
	}
	if effectivePoolID != nil && strings.TrimSpace(*effectivePoolID) != "" && effectiveTarget == "" {
		return errors.New("a generator assigned to a fleet pool requires a non-empty runtime target")
	}
	return nil
}

func generatorMetadataPatchApplied(current *storage.GeneratorMetadata, patch storage.GeneratorMetadataPatch) bool {
	if current == nil {
		return false
	}
	if patch.Target != nil && current.Target != *patch.Target {
		return false
	}
	if patch.FleetPoolIDSet {
		if patch.FleetPoolID == nil && current.FleetPoolID != nil {
			return false
		}
		if patch.FleetPoolID != nil && (current.FleetPoolID == nil || *current.FleetPoolID != *patch.FleetPoolID) {
			return false
		}
	}
	if patch.Region != nil && current.Region != *patch.Region {
		return false
	}
	if patch.ProviderID != nil && current.ProviderID != *patch.ProviderID {
		return false
	}
	if patch.ProviderStatus != nil && current.ProviderStatus != *patch.ProviderStatus {
		return false
	}
	if patch.Cordoned != nil && current.Cordoned != *patch.Cordoned {
		return false
	}
	if patch.TagsSet {
		if len(current.Tags) != len(patch.Tags) {
			return false
		}
		for key, value := range patch.Tags {
			if current.Tags[key] != value {
				return false
			}
		}
	}
	return true
}

func (s *Server) reconcileMetadataCordonState(generatorID string, cordoned bool) (bool, error) {
	generatorID = strings.TrimSpace(generatorID)
	if generatorID == "" || s == nil || s.runner == nil {
		return false, nil
	}
	backend, ok := s.runner.getExecutionBackend().(drainableExecutionBackend)
	if !ok {
		return false, nil
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
			return false, nil
		}
		if _, err := identityBackend.SetGeneratorDrainStateByID(generatorID, state); err != nil {
			return false, err
		}
	} else if err := backend.SetGeneratorDrainState(generatorID, state); err != nil {
		return false, err
	}
	if persistent, ok := backend.(persistentDrainBackend); ok {
		return true, s.saveGeneratorDrainStates(persistent.GeneratorDrainStates())
	}
	return true, nil
}

// drainStateReader reads a generator's live drain state by id (implemented by the
// distributed backend). Kept as a narrow optional interface so it need not be
// satisfied by every drainable backend / test stub.
type drainStateReader interface {
	GeneratorDrainStateByID(generatorID string) string
}

// Operator state is the value to reconcile or roll back. Effective scheduling
// state can additionally be draining because Kubernetes is terminating the pod.
func generatorOperatorDrainState(backend any, generatorID string) string {
	if reader, ok := backend.(interface{ GeneratorOperatorDrainStateByID(string) string }); ok {
		return reader.GeneratorOperatorDrainStateByID(generatorID)
	}
	if reader, ok := backend.(drainStateReader); ok {
		return reader.GeneratorDrainStateByID(generatorID)
	}
	return "active"
}

// reconcileMetadataCordonOnReconnect reconciles the live drain state to the
// persisted metadata cordon when a generator (re)connects. This is what makes a
// cordon (or un-cordon) issued while a generator was offline actually take effect:
// the PATCH persists the intent in generator metadata, and this hook (registered on
// the backend's identity-record event) enforces it once the generator has a live
// target mapping. It reconciles BOTH directions but never clobbers a non-cordon
// operator drain state ("draining"). Pod termination remains a separate overlay.
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
	current := generatorOperatorDrainState(backend, generatorID)
	var desired string
	switch {
	case metadata.Cordoned && current != "cordoned" && current != "draining":
		// Apply offline metadata intent without replacing an operator drain.
		// A transient termination overlay remains independently enforced.
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
	// Metadata already durably records this intent. Discovery also runs on HA
	// followers, whose cached drain maps must never overwrite newer operator
	// settings. Reconciliation therefore changes only the local projection.
}

// ---- provisioning requests -----------------------------------------------

type provisioningRequestCASStore interface {
	CreateProvisioningRequestIfAbsent(req *storage.ProvisioningRequest) (bool, error)
	TransitionProvisioningRequest(req *storage.ProvisioningRequest, expectedRevision int, expectedAttemptToken string) (bool, error)
}

func (s *Server) beginFleetProvisioningAttempt(requestID, attemptToken string) bool {
	s.fleetProvisioningMu.Lock()
	defer s.fleetProvisioningMu.Unlock()
	if s.fleetProvisioningActive == nil {
		s.fleetProvisioningActive = make(map[string]string)
	}
	if _, active := s.fleetProvisioningActive[requestID]; active {
		return false
	}
	s.fleetProvisioningActive[requestID] = attemptToken
	return true
}

func (s *Server) endFleetProvisioningAttempt(requestID, attemptToken string) {
	s.fleetProvisioningMu.Lock()
	defer s.fleetProvisioningMu.Unlock()
	if s.fleetProvisioningActive[requestID] == attemptToken {
		delete(s.fleetProvisioningActive, requestID)
	}
}

func (s *Server) fleetProvisioningAttemptActive(requestID string) bool {
	s.fleetProvisioningMu.Lock()
	defer s.fleetProvisioningMu.Unlock()
	_, active := s.fleetProvisioningActive[requestID]
	return active
}

func writeProvisioningCreateError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, http.StatusNotFound, "fleet pool not found")
	case errors.Is(err, storage.ErrFleetPoolNotActive):
		writeError(w, http.StatusConflict, "fleet pool is not active")
	case errors.Is(err, storage.ErrFleetPoolHasActiveProvisioning):
		writeError(w, http.StatusConflict, "fleet pool already has an active provisioning request")
	case errors.Is(err, storage.ErrFleetPoolPlacementChanged):
		writeError(w, http.StatusConflict, "fleet pool execution target changed while provisioning was being prepared; reload and retry")
	case errors.Is(err, compute.ErrOwnershipConflict):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, compute.ErrUnsupportedOperation):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeError(w, http.StatusBadRequest, err.Error())
	}
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
	if req.DesiredCount < 0 {
		writeError(w, http.StatusBadRequest, "desired_count must not be negative")
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
	effectiveDesiredCount, err := effectiveProvisioningDesiredCount(pool, req.DesiredCount)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.DesiredCount = effectiveDesiredCount
	req.ID = uuid.New().String()
	req.Revision = 1
	req.AttemptToken = uuid.New().String()
	req.ProjectID = normalizeProjectID(req.ProjectID)
	_, req.RequestedBySubject, _ = s.resolveAuditActor(r)
	now := time.Now()

	if handled, err := s.startAsyncCapacityProvisioning(w, r, pool, &req, now); handled {
		if err != nil {
			writeProvisioningCreateError(w, err)
		}
		return
	}

	// Resolve the pool's cluster before deciding whether a provisioner is
	// available: a pool bound to a registered cluster scales there, while an
	// unbound pool keeps the controller's env-configured scaler.
	poolScaler, scalerErr := s.scalerForPool(r.Context(), pool)
	if scalerErr != nil {
		writeError(w, http.StatusConflict, scalerErr.Error())
		return
	}

	if poolScaler != nil && poolScaler.CanProvision() {
		if err := validateProvisioningRuntimeTargets(s.store, pool, effectiveDesiredCount); err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		// A real provisioner is wired: actually scale the pool's workload and
		// drive the request through its lifecycle.
		req.Status = "provisioning"
		if !s.beginFleetProvisioningAttempt(req.ID, req.AttemptToken) {
			writeError(w, http.StatusConflict, "provisioning request already has an active scale attempt")
			return
		}
		defer s.endFleetProvisioningAttempt(req.ID, req.AttemptToken)
		if err := s.createProvisioningRequest(&req); err != nil {
			writeProvisioningCreateError(w, err)
			return
		}
		s.saveProvisioningEvent(req.ID, "requested", "provisioning requested", now)
		s.saveProvisioningEvent(req.ID, "provisioning", fmt.Sprintf("scaling pool %q toward %d generators", pool.Name, req.DesiredCount), now)

		scaleCtx, cancelScale := s.runner.executionOperationContext(r.Context())
		workload, scaledTo, perr := poolScaler.ScalePool(scaleCtx, pool, effectiveDesiredCount)
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
			if err := updateFleetPoolDesiredSize(s.store, pool.ID, scaledTo); err != nil {
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
		writeProvisioningCreateError(w, err)
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

type CapacityInstanceResponse struct {
	ID                    string     `json:"id"`
	ExecutionTargetID     string     `json:"execution_target_id"`
	FleetPoolID           string     `json:"fleet_pool_id"`
	TestID                string     `json:"test_id,omitempty"`
	ProvisioningRequestID string     `json:"provisioning_request_id,omitempty"`
	GeneratorID           string     `json:"generator_id"`
	ProviderID            string     `json:"provider_id,omitempty"`
	State                 string     `json:"state"`
	ArchiveState          string     `json:"archive_state"`
	CleanupDeadline       *time.Time `json:"cleanup_deadline,omitempty"`
	CreatedAt             time.Time  `json:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
}

type CapacityInstanceListResponse struct {
	Instances []CapacityInstanceResponse `json:"instances"`
	Total     int                        `json:"total"`
}

func (s *Server) handleListProvisioningRequestInstances(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	requestID := strings.TrimSpace(r.PathValue("id"))
	request, err := s.store.GetProvisioningRequest(requestID)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "provisioning request not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load provisioning request")
		return
	}
	intentStore, ok := s.store.(storage.CapacityInstanceIntentStore)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "storage does not support capacity instance observations")
		return
	}
	intents, err := intentStore.ListCapacityInstanceIntents(request.ExecutionTargetID, requestID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list capacity instances")
		return
	}
	instances := make([]CapacityInstanceResponse, 0, len(intents))
	for _, intent := range intents {
		if intent == nil {
			continue
		}
		instances = append(instances, CapacityInstanceResponse{
			ID: intent.ID, ExecutionTargetID: intent.ExecutionTargetID, FleetPoolID: intent.FleetPoolID,
			TestID: intent.TestID, ProvisioningRequestID: intent.ProvisioningRequestID,
			GeneratorID: intent.GeneratorID, ProviderID: intent.ProviderID, State: intent.State,
			ArchiveState: intent.ArchiveState, CleanupDeadline: intent.CleanupDeadline,
			CreatedAt: intent.CreatedAt, UpdatedAt: intent.UpdatedAt,
		})
	}
	writeJSON(w, http.StatusOK, CapacityInstanceListResponse{Instances: instances, Total: len(instances)})
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
	isAsyncCapacity := strings.TrimSpace(req.ExecutionTargetID) != "" && s.capacityBackends != nil
	if s.fleetProvisioningAttemptActive(req.ID) && !isAsyncCapacity {
		writeError(w, http.StatusConflict, "provisioning request is currently applying an external scale and cannot be cancelled safely")
		return
	}
	var asyncCancelBackend compute.Backend
	var asyncCancelRequest compute.CapacityRequest
	var asyncCancelIntents []*storage.CapacityInstanceIntent
	if isAsyncCapacity {
		pool, poolErr := s.store.GetFleetPool(req.FleetPoolID)
		if poolErr != nil {
			writeError(w, http.StatusInternalServerError, "failed to load fleet pool for cancellation")
			return
		}
		backend, _, backendErr := s.capacityBackendForMaintenance(r.Context(), pool)
		if backendErr != nil {
			writeError(w, http.StatusBadGateway, "failed to resolve capacity backend: "+backendErr.Error())
			return
		}
		asyncCancelBackend = backend
		asyncCancelRequest = s.asyncCapacityRequest(pool, req)
		if !isAutomaticRunCapacityRequest(req) {
			applyStandingRollback(&asyncCancelRequest, pool, backend.Describe())
		}
		asyncCancelIntents, err = loadCapacityLaunchIntents(s.store, req.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load capacity intents for cancellation")
			return
		}
	}
	now := time.Now().UTC()
	expectedRevision := req.Revision
	expectedAttemptToken := req.AttemptToken
	req.Status = "cancelling"
	req.Revision++
	req.AttemptToken = uuid.New().String()
	if err := s.transitionProvisioningRequest(req, expectedRevision, expectedAttemptToken); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to cancel provisioning request")
		return
	}
	// Changing the durable attempt token fences the old worker. Clear its local
	// ownership entry explicitly; its deferred cleanup cannot clear a newer token.
	s.endFleetProvisioningAttempt(req.ID, expectedAttemptToken)
	if isAsyncCapacity {
		if cancelErr := asyncCancelBackend.Cancel(r.Context(), asyncCancelRequest); cancelErr != nil {
			expectedRevision, expectedAttemptToken = req.Revision, req.AttemptToken
			req.Status = "failed"
			req.LastError = "provider cancellation failed: " + cancelErr.Error()
			req.Revision++
			req.CompletedAt = &now
			_ = s.transitionProvisioningRequest(req, expectedRevision, expectedAttemptToken)
			writeError(w, http.StatusBadGateway, req.LastError)
			return
		}
		s.markCapacityIntentsTerminated(asyncCancelIntents, storage.CapacityArchiveTerminal)
	}
	expectedRevision, expectedAttemptToken = req.Revision, req.AttemptToken
	req.Status = "cancelled"
	req.Revision++
	req.CompletedAt = &now
	if err := s.transitionProvisioningRequest(req, expectedRevision, expectedAttemptToken); err != nil {
		writeError(w, http.StatusConflict, "capacity cancellation completed but the request state changed; the active leader will reconcile it")
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
	effectiveDesiredCount, err := effectiveProvisioningDesiredCount(pool, req.DesiredCount)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	req.DesiredCount = effectiveDesiredCount
	if handled, asyncErr := s.restartAsyncCapacityProvisioning(req, pool); handled {
		if asyncErr != nil {
			writeError(w, http.StatusConflict, asyncErr.Error())
			return
		}
		s.saveProvisioningEvent(req.ID, "retry", "asynchronous capacity retry requested", time.Now().UTC())
		s.writeProvisioningRequestStatus(w, req.ID, http.StatusAccepted)
		return
	}
	if s.fleetScaler != nil && s.fleetScaler.CanProvision() {
		if err := validateProvisioningRuntimeTargets(s.store, pool, effectiveDesiredCount); err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
	}

	now := time.Now()
	expectedRevision := req.Revision
	expectedAttemptToken := req.AttemptToken
	req.CompletedAt = nil
	req.Status = "provisioning"
	req.Revision++
	req.AttemptToken = uuid.New().String()
	if !s.beginFleetProvisioningAttempt(req.ID, req.AttemptToken) {
		writeError(w, http.StatusConflict, "provisioning request already has an active scale attempt")
		return
	}
	defer s.endFleetProvisioningAttempt(req.ID, req.AttemptToken)
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
		if err := updateFleetPoolDesiredSize(s.store, pool.ID, scaledTo); err != nil {
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
	effectiveDesiredCount, err := effectiveProvisioningDesiredCount(pool, req.DesiredCount)
	if err != nil {
		return "failed", "", 0, err.Error()
	}
	// The retry path resolves the pool's cluster the same way the create path
	// does, so a retried request cannot scale a different cluster.
	poolScaler, scalerErr := s.scalerForPool(ctx, pool)
	if scalerErr != nil {
		return "failed", "", 0, scalerErr.Error()
	}
	if poolScaler != nil && poolScaler.CanProvision() {
		if err := validateProvisioningRuntimeTargets(s.store, pool, effectiveDesiredCount); err != nil {
			return "failed", "", 0, err.Error()
		}
		workload, scaledTo, err := poolScaler.ScalePool(ctx, pool, effectiveDesiredCount)
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
	if s.store == nil || s.runner == nil || !s.runner.holdsExecutionLeadership() {
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
			status := ""
			if req != nil {
				status = strings.ToLower(strings.TrimSpace(req.Status))
			}
			if req == nil || (status != "provisioning" && status != "waiting_ready") || !s.runner.holdsExecutionLeadership() {
				continue
			}
			if s.fleetProvisioningAttemptActive(req.ID) {
				continue
			}
			pool, err := s.store.GetFleetPool(req.FleetPoolID)
			if err != nil {
				slog.Warn("failed to load fleet pool during provisioning reconcile", "request_id", req.ID, "error", err)
				continue
			}
			if req.ExecutionTargetID != "" && s.capacityBackends != nil {
				backend, target, backendErr := s.capacityBackends.BackendForPool(ctx, pool)
				if backendErr == nil && target.Kind != storage.ExecutionTargetKubernetes {
					intents, intentErr := loadCapacityLaunchIntents(s.store, req.ID)
					if intentErr != nil {
						slog.Warn("failed to load capacity launch intents during recovery", "request_id", req.ID, "error", intentErr)
						continue
					}
					expectedRevision, expectedToken := req.Revision, req.AttemptToken
					req.Revision++
					req.AttemptToken = uuid.NewString()
					if !s.beginFleetProvisioningAttempt(req.ID, req.AttemptToken) {
						continue
					}
					if err := s.transitionProvisioningRequest(req, expectedRevision, expectedToken); err != nil {
						s.endFleetProvisioningAttempt(req.ID, req.AttemptToken)
						continue
					}
					base := s.asyncCapacityRequest(pool, req)
					if !isAutomaticRunCapacityRequest(req) {
						applyStandingRollback(&base, pool, backend.Describe())
					}
					for _, intent := range intents {
						if strings.TrimSpace(intent.TestID) != "" {
							base.TestID = strings.TrimSpace(intent.TestID)
							break
						}
					}
					base.ConnectivityMode = target.ConnectivityMode
					attachCapacityLaunchIntents(&base, intents)
					if !s.launchBackground(func() { s.reconcileAsyncCapacityOperation(req.ID, pool, backend, base, intents) }) {
						s.endFleetProvisioningAttempt(req.ID, req.AttemptToken)
						return errors.New("controller is stopping and cannot recover capacity operations")
					}
					continue
				}
				if backendErr != nil {
					slog.Warn("failed to resolve capacity backend during recovery", "request_id", req.ID, "error", backendErr)
					continue
				}
			}
			if s.fleetScaler == nil || !s.fleetScaler.CanProvision() {
				continue
			}
			// Claim the recovery attempt before touching the external scaler.
			expectedRevision, expectedToken := req.Revision, req.AttemptToken
			req.Revision++
			req.AttemptToken = uuid.New().String()
			if !s.beginFleetProvisioningAttempt(req.ID, req.AttemptToken) {
				continue
			}
			if err := s.transitionProvisioningRequest(req, expectedRevision, expectedToken); err != nil {
				s.endFleetProvisioningAttempt(req.ID, req.AttemptToken)
				continue
			}
			expectedRevision, expectedToken = req.Revision, req.AttemptToken
			status, workload, scaledTo, message := s.executeProvisioningScale(ctx, req, pool)
			s.endFleetProvisioningAttempt(req.ID, req.AttemptToken)
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
				if err := updateFleetPoolDesiredSize(s.store, pool.ID, scaledTo); err != nil {
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
