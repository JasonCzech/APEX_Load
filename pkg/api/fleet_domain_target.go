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
