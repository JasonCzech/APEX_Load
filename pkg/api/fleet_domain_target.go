package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jasonczech/apexload/pkg/storage"
)

// Domain deployment-target enforcement.
//
// A domain target is both a DEFAULT that new pools inherit and a HARD
// CONSTRAINT. It is checked here on every write path that can change a pool's
// cluster binding, and again fail-closed at scale time in
// registryFleetScalerResolver — so retargeting a domain after a pool was
// created cannot scale generators into a cloud nobody chose.
//
// The single most important rule is the first one: a pool with no domain
// binding is completely unaffected. That is every pool that predates this
// feature.

var errDomainTargetStoreUnavailable = errors.New("this controller's storage backend does not support domain deployment targets")

func (s *Server) validatePoolExecutionTarget(pool *storage.FleetPool) (int, error) {
	if pool == nil || strings.TrimSpace(pool.ExecutionTargetID) == "" {
		return http.StatusOK, nil
	}
	store, ok := s.store.(storage.ExecutionTargetStore)
	if !ok {
		return http.StatusServiceUnavailable, errors.New("this controller's storage backend does not support execution targets")
	}
	target, err := store.GetExecutionTarget(pool.ExecutionTargetID)
	if err != nil || target == nil {
		return http.StatusBadRequest, fmt.Errorf("execution target %q could not be loaded", pool.ExecutionTargetID)
	}
	if target.Status != "active" {
		return http.StatusConflict, fmt.Errorf("execution target %q is %s", target.Name, target.Status)
	}
	provider, known := storage.ProviderForExecutionTargetKind(target.Kind)
	if !known {
		return http.StatusConflict, fmt.Errorf("execution target %q has unsupported kind %q", target.Name, target.Kind)
	}
	if target.Kind == storage.ExecutionTargetKubernetes {
		pool.ClusterID = target.ClusterConnectionID
	} else {
		pool.ClusterID = ""
	}
	if strings.TrimSpace(pool.LifecycleMode) == "" && target.Kind != storage.ExecutionTargetKubernetes && target.Kind != storage.ExecutionTargetStatic {
		if target.Capabilities.CanLaunch {
			pool.LifecycleMode = storage.FleetLifecycleWarmBurst
		} else {
			pool.LifecycleMode = storage.FleetLifecycleStanding
		}
	}
	if target.Kind != storage.ExecutionTargetKubernetes && target.Kind != storage.ExecutionTargetStatic {
		// Reverse sessions and provider observations are currently scoped by
		// execution target. Until the generator enrollment protocol carries a
		// pool identity, binding one PaaS target to multiple pools would let each
		// pool select and drain the other's generators. Refuse that ambiguous
		// topology rather than weakening placement isolation.
		pools, listErr := store.ListFleetPoolsForExecutionTarget(target.ID)
		if listErr != nil {
			return http.StatusInternalServerError, fmt.Errorf("list pools for execution target %q: %w", target.Name, listErr)
		}
		for _, bound := range pools {
			if bound != nil && bound.ID != pool.ID {
				return http.StatusConflict, fmt.Errorf("execution target %q is already bound to fleet pool %q; PaaS execution targets currently support one pool each", target.Name, bound.Name)
			}
		}
	}
	if strings.TrimSpace(pool.CloudProvider) == "" {
		pool.CloudProvider = provider
	} else if normalized := storage.NormalizeCloudProvider(pool.CloudProvider); normalized != provider {
		return http.StatusBadRequest, fmt.Errorf("cloud provider %q does not match execution target %s", pool.CloudProvider, provider)
	} else {
		pool.CloudProvider = normalized
	}
	if pool.RuntimeProfileID != "" {
		found := false
		for _, profile := range target.RuntimeProfiles {
			if profile.ID == pool.RuntimeProfileID {
				found = true
				break
			}
		}
		if !found {
			return http.StatusBadRequest, fmt.Errorf("runtime profile %q is not available on execution target %q", pool.RuntimeProfileID, target.Name)
		}
	}
	if (pool.LifecycleMode == storage.FleetLifecycleOnDemand || pool.LifecycleMode == storage.FleetLifecycleWarmBurst) && !target.Capabilities.CanLaunch {
		return http.StatusConflict, fmt.Errorf("execution target %q does not support burst launches", target.Name)
	}
	return http.StatusOK, nil
}

// domainTargetStore returns the optional capability, or nil when the backend
// does not implement it.
func (s *Server) domainTargetStore() storage.DomainDeploymentTargetStore {
	return s.domainTargets
}

// validatePoolAgainstDomainTarget enforces the domain constraint on a pool
// about to be written, and applies the domain's default cluster when the pool
// names none. It returns an HTTP status alongside the error so each caller can
// respond consistently.
func (s *Server) validatePoolAgainstDomainTarget(pool *storage.FleetPool) (int, error) {
	if pool == nil {
		return http.StatusBadRequest, errors.New("fleet pool is required")
	}
	// Rule 1, the compatibility hinge: an unscoped pool is not subject to any
	// domain constraint. Every pool created before domain targets carries an
	// empty domain_id, so this returns immediately for all of them.
	if strings.TrimSpace(pool.DomainID) == "" {
		return http.StatusOK, nil
	}

	targets := s.domainTargetStore()
	if targets == nil {
		return http.StatusServiceUnavailable, errDomainTargetStoreUnavailable
	}
	if domain, err := s.store.GetDomain(pool.DomainID); err != nil || domain == nil {
		return http.StatusBadRequest, fmt.Errorf("domain %q does not exist", pool.DomainID)
	}

	target, err := targets.GetDomainDeploymentTarget(pool.DomainID)
	if err != nil {
		return http.StatusInternalServerError, fmt.Errorf("load the domain's deployment target: %w", err)
	}
	if target == nil {
		// The domain is scoped but unconstrained. Normalize the provider label
		// so it stays meaningful, and allow the write.
		if normalized := storage.NormalizeCloudProvider(pool.CloudProvider); normalized != "" {
			pool.CloudProvider = normalized
		}
		return http.StatusOK, nil
	}

	if len(target.AllowedExecutionTargetIDs) > 0 {
		if strings.TrimSpace(pool.ExecutionTargetID) == "" {
			pool.ExecutionTargetID = strings.TrimSpace(target.DefaultExecutionTargetID)
		}
		if strings.TrimSpace(pool.ExecutionTargetID) == "" {
			return http.StatusConflict, fmt.Errorf(
				"domain %q requires an allowed execution target, and the domain has no default", pool.DomainID)
		}
		allowed := make(map[string]struct{}, len(target.AllowedExecutionTargetIDs))
		for _, id := range target.AllowedExecutionTargetIDs {
			allowed[strings.TrimSpace(id)] = struct{}{}
		}
		if _, ok := allowed[pool.ExecutionTargetID]; !ok {
			return http.StatusConflict, fmt.Errorf("execution target %q is not allowed for domain %q", pool.ExecutionTargetID, pool.DomainID)
		}
		executionTargets, ok := s.store.(storage.ExecutionTargetStore)
		if !ok {
			return http.StatusServiceUnavailable, errors.New("this controller's storage backend does not support execution targets")
		}
		executionTarget, err := executionTargets.GetExecutionTarget(pool.ExecutionTargetID)
		if err != nil || executionTarget == nil {
			return http.StatusBadRequest, fmt.Errorf("execution target %q could not be loaded", pool.ExecutionTargetID)
		}
		provider, known := storage.ProviderForExecutionTargetKind(executionTarget.Kind)
		if !known {
			return http.StatusConflict, fmt.Errorf("execution target %q has unsupported kind %q", executionTarget.Name, executionTarget.Kind)
		}
		if executionTarget.Kind == storage.ExecutionTargetKubernetes {
			pool.ClusterID = executionTarget.ClusterConnectionID
		} else {
			pool.ClusterID = ""
		}
		if strings.TrimSpace(pool.CloudProvider) == "" {
			pool.CloudProvider = provider
		} else if normalized := storage.NormalizeCloudProvider(pool.CloudProvider); normalized != provider {
			return http.StatusBadRequest, fmt.Errorf("cloud provider %q does not match execution target %s", pool.CloudProvider, provider)
		} else {
			pool.CloudProvider = normalized
		}
		return http.StatusOK, nil
	}

	// Inheritance: a new pool that names no cluster adopts the domain default.
	if strings.TrimSpace(pool.ClusterID) == "" {
		pool.ClusterID = strings.TrimSpace(target.DefaultClusterID)
	}
	if strings.TrimSpace(pool.ClusterID) == "" {
		// An unbound pool scales through the controller's env-configured
		// provisioner — i.e. the controller's OWN cluster — which is exactly
		// what the constraint exists to prevent.
		return http.StatusConflict, fmt.Errorf(
			"domain %q requires deployment into %s, so this pool must be bound to a cluster connection (the domain has no default cluster configured)",
			pool.DomainID, target.Provider)
	}

	clusters, ok := s.store.(storage.ClusterConnectionStore)
	if !ok {
		return http.StatusServiceUnavailable, errors.New("this controller's storage backend does not support cluster connections")
	}
	conn, err := clusters.GetClusterConnection(pool.ClusterID)
	if err != nil || conn == nil {
		return http.StatusBadRequest, fmt.Errorf("cluster connection %q could not be loaded", pool.ClusterID)
	}
	provider, known := storage.ProviderForClusterKind(conn.Kind)
	if !known {
		return http.StatusConflict, fmt.Errorf("cluster %q has kind %q, which maps to no deployment provider", conn.Name, conn.Kind)
	}
	if provider != target.Provider {
		return http.StatusConflict, fmt.Errorf(
			"domain %q targets %s but cluster %q is %s", pool.DomainID, target.Provider, conn.Name, provider)
	}

	// cloud_provider stops being decorative free text for a constrained pool:
	// it is derived when omitted and verified when supplied.
	if strings.TrimSpace(pool.CloudProvider) == "" {
		pool.CloudProvider = target.Provider
	} else if normalized := storage.NormalizeCloudProvider(pool.CloudProvider); normalized != target.Provider {
		return http.StatusBadRequest, fmt.Errorf(
			"cloud provider %q does not match the domain's deployment target %s", pool.CloudProvider, target.Provider)
	} else {
		pool.CloudProvider = normalized
	}
	return http.StatusOK, nil
}

// conflictingPoolsForProvider returns the domain's pools whose bound cluster
// does not match the given provider. It backs the 409 body on a retarget and
// the disabled Save button in the dashboard.
func (s *Server) conflictingPoolsForProvider(domainID, provider string) ([]ConflictingFleetPool, error) {
	targets := s.domainTargetStore()
	clusters, ok := s.store.(storage.ClusterConnectionStore)
	if targets == nil || !ok {
		return nil, nil
	}
	pools, err := targets.ListFleetPoolsForDomain(domainID)
	if err != nil {
		return nil, err
	}
	conflicts := make([]ConflictingFleetPool, 0)
	for _, pool := range pools {
		conflict := ConflictingFleetPool{PoolID: pool.ID, PoolName: pool.Name, ClusterID: pool.ClusterID}
		if strings.TrimSpace(pool.ClusterID) == "" {
			// An unbound pool conflicts with ANY constraint: it would scale in
			// the controller's own cluster.
			conflict.Reason = "the pool is not bound to a cluster connection"
			conflicts = append(conflicts, conflict)
			continue
		}
		conn, err := clusters.GetClusterConnection(pool.ClusterID)
		if err != nil || conn == nil {
			conflict.Reason = "the pool's cluster connection could not be loaded"
			conflicts = append(conflicts, conflict)
			continue
		}
		conflict.ClusterName = conn.Name
		poolProvider, known := storage.ProviderForClusterKind(conn.Kind)
		if !known || poolProvider != provider {
			conflict.Provider = poolProvider
			conflict.Reason = fmt.Sprintf("the pool is bound to a %s cluster", firstNonEmpty(poolProvider, conn.Kind))
			conflicts = append(conflicts, conflict)
		}
	}
	return conflicts, nil
}

func (s *Server) conflictingPoolsForExecutionTargets(domainID string, allowedIDs []string) ([]ConflictingFleetPool, error) {
	targets := s.domainTargetStore()
	executionTargets, ok := s.store.(storage.ExecutionTargetStore)
	if targets == nil || !ok {
		return nil, nil
	}
	pools, err := targets.ListFleetPoolsForDomain(domainID)
	if err != nil {
		return nil, err
	}
	allowed := make(map[string]struct{}, len(allowedIDs))
	for _, id := range allowedIDs {
		allowed[strings.TrimSpace(id)] = struct{}{}
	}
	conflicts := make([]ConflictingFleetPool, 0)
	for _, pool := range pools {
		conflict := ConflictingFleetPool{
			PoolID: pool.ID, PoolName: pool.Name, ClusterID: pool.ClusterID,
			ExecutionTargetID: pool.ExecutionTargetID,
		}
		if strings.TrimSpace(pool.ExecutionTargetID) == "" {
			conflict.Reason = "the pool is not bound to an execution target"
			conflicts = append(conflicts, conflict)
			continue
		}
		target, loadErr := executionTargets.GetExecutionTarget(pool.ExecutionTargetID)
		if loadErr != nil || target == nil {
			conflict.Reason = "the pool's execution target could not be loaded"
			conflicts = append(conflicts, conflict)
			continue
		}
		conflict.ExecutionTargetName = target.Name
		conflict.Provider, _ = storage.ProviderForExecutionTargetKind(target.Kind)
		if _, permitted := allowed[pool.ExecutionTargetID]; !permitted {
			conflict.Reason = "the pool is bound to an execution target outside the proposed allowlist"
			conflicts = append(conflicts, conflict)
		}
	}
	return conflicts, nil
}
