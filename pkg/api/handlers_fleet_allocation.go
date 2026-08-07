package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/kubeops"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Host-resource allocation for a fleet pool: CPU/memory requests and limits
// plus node placement, applied to the pool's generator StatefulSet.

// FleetPoolAllocationRequest is the write shape for a pool's allocation.
type FleetPoolAllocationRequest struct {
	ClusterID     string               `json:"cluster_id"`
	CPURequest    string               `json:"cpu_request"`
	CPULimit      string               `json:"cpu_limit"`
	MemoryRequest string               `json:"memory_request"`
	MemoryLimit   string               `json:"memory_limit"`
	NodePool      string               `json:"node_pool"`
	// NodePoolLabel is the node label key NodePool matches. Empty keeps the
	// default "agentpool"; an EKS pool sends eks.amazonaws.com/nodegroup as
	// reported by the cluster capacity response.
	NodePoolLabel string               `json:"node_pool_label"`
	Zone          string               `json:"zone"`
	NodeSelector  map[string]string    `json:"node_selector"`
	Tolerations   []storage.Toleration `json:"tolerations"`
	// UpdatedAt is the pool's optimistic-concurrency token.
	UpdatedAt time.Time `json:"updated_at"`
}

// FleetPoolAllocationResponse reports the stored allocation, the preflight
// verdict, and anything the administrator should know about the target.
type FleetPoolAllocationResponse struct {
	PoolID     string                  `json:"pool_id"`
	ClusterID  string                  `json:"cluster_id,omitempty"`
	Allocation *storage.PoolAllocation `json:"allocation,omitempty"`
	Preflight  *preflightResult        `json:"preflight,omitempty"`
	Applied    bool                    `json:"applied"`
	Container  string                  `json:"container,omitempty"`
	Warnings   []string                `json:"warnings,omitempty"`
}

func (r FleetPoolAllocationRequest) allocation() *storage.PoolAllocation {
	return &storage.PoolAllocation{
		CPURequest:    strings.TrimSpace(r.CPURequest),
		CPULimit:      strings.TrimSpace(r.CPULimit),
		MemoryRequest: strings.TrimSpace(r.MemoryRequest),
		MemoryLimit:   strings.TrimSpace(r.MemoryLimit),
		NodePool:      strings.TrimSpace(r.NodePool),
		NodePoolLabel: strings.TrimSpace(r.NodePoolLabel),
		Zone:          strings.TrimSpace(r.Zone),
		NodeSelector:  r.NodeSelector,
		Tolerations:   r.Tolerations,
	}
}

// validateAllocationQuantities checks the Kubernetes quantity strings and the
// requests-cannot-exceed-limits invariant. Comparison happens on parsed values
// so "1" and "1000m" compare equal rather than as different strings.
func validateAllocationQuantities(allocation *storage.PoolAllocation) error {
	if allocation == nil {
		return nil
	}
	cpuRequest, cpuRequestSet, err := kubeops.ParseCPUMillis(allocation.CPURequest)
	if err != nil {
		return err
	}
	cpuLimit, cpuLimitSet, err := kubeops.ParseCPUMillis(allocation.CPULimit)
	if err != nil {
		return err
	}
	memoryRequest, memoryRequestSet, err := kubeops.ParseMemoryBytes(allocation.MemoryRequest)
	if err != nil {
		return err
	}
	memoryLimit, memoryLimitSet, err := kubeops.ParseMemoryBytes(allocation.MemoryLimit)
	if err != nil {
		return err
	}
	if cpuRequestSet && cpuLimitSet && cpuRequest > cpuLimit {
		return fmt.Errorf("CPU request (%s) cannot exceed the CPU limit (%s)", allocation.CPURequest, allocation.CPULimit)
	}
	if memoryRequestSet && memoryLimitSet && memoryRequest > memoryLimit {
		return fmt.Errorf("memory request (%s) cannot exceed the memory limit (%s)", allocation.MemoryRequest, allocation.MemoryLimit)
	}
	return nil
}

func (s *Server) handleUpdateFleetPoolAllocation(w http.ResponseWriter, r *http.Request) {
	// Allocation mutates a live cluster, so it belongs to the execution leader
	// exactly like scaling does. Cluster CRUD is not gated, being a CAS-guarded
	// database write that is safe on any replica.
	if s.runner == nil || !s.runner.isExecutionLeader() {
		writeError(w, http.StatusServiceUnavailable, "this controller is not the execution leader")
		return
	}
	pool, ok := s.loadFleetPoolForAllocation(w, r)
	if !ok {
		return
	}
	var req FleetPoolAllocationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid allocation payload")
		return
	}
	if req.UpdatedAt.IsZero() {
		writeError(w, http.StatusBadRequest, "updated_at is required to update a pool allocation")
		return
	}
	allocation := req.allocation()
	if err := validateAllocationQuantities(allocation); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := allocation.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	clusterID := firstNonEmpty(strings.TrimSpace(req.ClusterID), pool.ClusterID)
	if clusterID == "" {
		writeError(w, http.StatusBadRequest, "a pool must be bound to a cluster connection before host resources can be allocated")
		return
	}
	conn, err := s.loadClusterForAllocation(clusterID)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	// This route is the ONLY other path that writes pool.ClusterID, so the
	// domain constraint has to be enforced here too — otherwise it would be
	// bypassable by rebinding a pool through the allocation editor. The check
	// runs against a copy carrying the requested binding, before anything is
	// applied to the cluster.
	if candidate := *pool; strings.TrimSpace(candidate.DomainID) != "" {
		candidate.ClusterID = clusterID
		if status, err := s.validatePoolAgainstDomainTarget(&candidate); err != nil {
			writeError(w, status, err.Error())
			return
		}
		// This route also rewrites node placement, which is what actually keeps
		// two domains off the same nodes. Check the requested allocation, not
		// the stored one.
		candidate.Allocation = allocation
		if status, err := s.validatePoolColocation(&candidate); err != nil {
			writeError(w, status, err.Error())
			return
		}
	}

	ctx, cancel := s.runner.executionOperationContext(r.Context())
	defer cancel()

	response := FleetPoolAllocationResponse{PoolID: pool.ID, ClusterID: clusterID, Allocation: allocation}
	container, set, verdict, err := s.prepareAllocationApply(ctx, conn, pool, allocation)
	if err != nil {
		writeAllocationApplyError(w, err)
		return
	}
	response.Container = container
	response.Preflight = &verdict
	response.Warnings = allocationTargetWarnings(set)
	if !verdict.Allowed {
		writeJSONError(w, http.StatusConflict, "the target namespace cannot accommodate this allocation", response)
		return
	}

	client, err := s.clusterClients.ClientFor(ctx, conn)
	if err != nil {
		writeError(w, http.StatusBadGateway, scrubClusterError(err))
		return
	}
	patch := buildAllocationPatch(set, container, allocation)
	if err := client.PatchStatefulSetPodTemplate(ctx, conn.Namespace, set.Name, patch); err != nil {
		writeError(w, http.StatusBadGateway, "failed to apply the allocation: "+scrubClusterError(err))
		return
	}

	// Persist only after the cluster accepted the patch, so a stored allocation
	// always reflects something that was actually applied.
	pool.ClusterID = clusterID
	pool.Allocation = allocation
	updated, err := s.updateFleetPoolAllocation(pool, req.UpdatedAt)
	if err != nil || !updated {
		// The cluster already accepted the patch, so the administrator MUST be
		// told that stored state and live state now disagree. writeError
		// replaces any 5xx message with a generic "internal server error", so
		// this is deliberately a 409: the detail is the whole point of the
		// response, and it contains no internal error text.
		slog.Error("fleet pool allocation applied to the cluster but not persisted",
			"pool_id", pool.ID, "cluster_id", clusterID, "workload", set.Name, "error", err)
		response.Applied = true
		writeJSONError(w, http.StatusConflict,
			"the allocation was applied to the cluster but could not be saved: the fleet pool changed since it was loaded. "+
				"The live workload now differs from the stored allocation — reload the pool and re-apply to reconcile them.",
			response)
		return
	}
	response.Applied = true
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleFleetPoolAllocationPreflight(w http.ResponseWriter, r *http.Request) {
	pool, ok := s.loadFleetPoolForAllocation(w, r)
	if !ok {
		return
	}
	clusterID := firstNonEmpty(strings.TrimSpace(r.URL.Query().Get("cluster_id")), pool.ClusterID)
	if clusterID == "" {
		writeError(w, http.StatusBadRequest, "a pool must be bound to a cluster connection before host resources can be allocated")
		return
	}
	conn, err := s.loadClusterForAllocation(clusterID)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), clusterProbeTimeout)
	defer cancel()

	container, set, verdict, err := s.prepareAllocationApply(ctx, conn, pool, pool.Allocation)
	if err != nil {
		writeAllocationApplyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, FleetPoolAllocationResponse{
		PoolID:     pool.ID,
		ClusterID:  clusterID,
		Allocation: pool.Allocation,
		Preflight:  &verdict,
		Container:  container,
		Warnings:   allocationTargetWarnings(set),
	})
}

// prepareAllocationApply performs the reads every allocation change depends on:
// resolve the target workload, prove it is an APEX Load generator, pick the
// container, and run the quota/pod-fit preflight.
func (s *Server) prepareAllocationApply(ctx context.Context, conn *storage.ClusterConnection, pool *storage.FleetPool,
	allocation *storage.PoolAllocation) (string, kubeops.StatefulSetRef, preflightResult, error) {

	client, err := s.clusterClients.ClientFor(ctx, conn)
	if err != nil {
		return "", kubeops.StatefulSetRef{}, preflightResult{}, err
	}
	// Resolve the same workload the scaler would: a pool tagged with its own
	// generator StatefulSet must have its allocation patched onto that one, not
	// the cluster default, or two pools on one cluster overwrite each other.
	workload := generatorWorkloadForPool(pool, firstNonEmpty(conn.GeneratorStatefulSet, "apexload-generator"))
	set, err := client.GetStatefulSet(ctx, conn.Namespace, workload)
	if err != nil {
		return "", kubeops.StatefulSetRef{}, preflightResult{}, err
	}
	// Refuse an unrelated co-tenant workload before touching anything.
	if err := verifyGeneratorWorkloadOwnership(set, conn.GeneratorSelector); err != nil {
		return "", set, preflightResult{}, err
	}
	container, err := resolveGeneratorContainer(set, conn.GeneratorContainer)
	if err != nil {
		return "", set, preflightResult{}, err
	}

	snapshot := s.captureClusterCapacity(ctx, client, conn, set, container)
	// The allocation changes per-pod resources at the workload's current
	// replica count; scaling is a separate operation with its own preflight.
	verdict := evaluateAllocationPreflight(snapshot, allocation, set.Replicas, set.Replicas)
	return container, set, verdict, nil
}

// captureClusterCapacity gathers the quota and node state a preflight needs.
// Node listing is best-effort: without the optional ClusterRole it is
// forbidden, and the preflight degrades to a warning.
func (s *Server) captureClusterCapacity(ctx context.Context, client *kubeops.Client, conn *storage.ClusterConnection,
	set kubeops.StatefulSetRef, container string) clusterCapacitySnapshot {

	snapshot := clusterCapacitySnapshot{}
	if quotas, err := client.ListResourceQuotas(ctx, conn.Namespace); err == nil {
		snapshot.Quotas = quotas
	}
	if nodes, err := client.ListNodes(ctx, ""); err == nil {
		snapshot.Nodes = nodes
		snapshot.NodesAvailable = true
	}
	for _, item := range set.Containers {
		if item.Name == container {
			snapshot.CurrentRequests = item.Requests
		}
	}
	return snapshot
}

// CheckScale implements allocationQuotaChecker for the scaler decorator, so a
// provisioning request cannot push the namespace past its quota.
func (s *Server) CheckScale(ctx context.Context, conn *storage.ClusterConnection, pool *storage.FleetPool, targetReplicas int) (preflightResult, error) {
	client, err := s.clusterClients.ClientFor(ctx, conn)
	if err != nil {
		return preflightResult{}, err
	}
	// Match ScalePool's workload resolution: the quota check must measure the
	// StatefulSet the provisioning request will actually scale.
	workload := generatorWorkloadForPool(pool, firstNonEmpty(conn.GeneratorStatefulSet, "apexload-generator"))
	set, err := client.GetStatefulSet(ctx, conn.Namespace, workload)
	if err != nil {
		return preflightResult{}, err
	}
	container, err := resolveGeneratorContainer(set, conn.GeneratorContainer)
	if err != nil {
		// A workload whose container cannot be identified still has a quota
		// footprint, so fall back to the workload's declared requests.
		container = ""
	}
	snapshot := s.captureClusterCapacity(ctx, client, conn, set, container)
	allocation := pool.Allocation
	if allocation == nil {
		// With no explicit allocation the pods keep their manifest requests, so
		// project the current per-pod requests forward.
		allocation = &storage.PoolAllocation{
			CPURequest:    snapshot.CurrentRequests["cpu"],
			MemoryRequest: snapshot.CurrentRequests["memory"],
		}
	}
	return evaluateAllocationPreflight(snapshot, allocation, set.Replicas, targetReplicas), nil
}

func (s *Server) loadFleetPoolForAllocation(w http.ResponseWriter, r *http.Request) (*storage.FleetPool, bool) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is unavailable")
		return nil, false
	}
	pool, err := s.store.GetFleetPool(r.PathValue("id"))
	if err != nil || pool == nil {
		writeError(w, http.StatusNotFound, "fleet pool not found")
		return nil, false
	}
	return pool, true
}

func (s *Server) loadClusterForAllocation(clusterID string) (*storage.ClusterConnection, error) {
	if s.clusterStore == nil {
		return nil, errors.New("this controller's storage backend does not support cluster connections")
	}
	conn, err := s.clusterStore.GetClusterConnection(clusterID)
	if err != nil || conn == nil {
		return nil, fmt.Errorf("cluster connection %q could not be loaded", clusterID)
	}
	if conn.Status != "" && conn.Status != "active" {
		return nil, fmt.Errorf("cluster connection %q is %s", conn.Name, conn.Status)
	}
	return conn, nil
}

// updateFleetPoolAllocation persists the pool under its CAS token.
func (s *Server) updateFleetPoolAllocation(pool *storage.FleetPool, expectedUpdatedAt time.Time) (bool, error) {
	updater, ok := s.store.(interface {
		UpdateFleetPoolIfUnchanged(*storage.FleetPool, []*storage.FleetPoolMember, bool, time.Time) (bool, error)
	})
	if !ok {
		return false, errors.New("this controller's storage backend does not support fleet pool updates")
	}
	return updater.UpdateFleetPoolIfUnchanged(pool, nil, false, expectedUpdatedAt)
}

func writeAllocationApplyError(w http.ResponseWriter, err error) {
	var ambiguous *errAmbiguousGeneratorContainer
	if errors.As(err, &ambiguous) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if strings.Contains(err.Error(), "allocation refused") || strings.Contains(err.Error(), "has no container named") {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeError(w, http.StatusBadGateway, scrubClusterError(err))
}

// writeJSONError emits a structured body alongside the error message so the
// dashboard can render the preflight reasons rather than a bare string.
func writeJSONError(w http.ResponseWriter, status int, message string, payload any) {
	writeJSON(w, status, map[string]any{"error": message, "detail": payload})
}
