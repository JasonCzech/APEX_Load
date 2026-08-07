package api

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/kubeops"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Per-pool cluster resolution sits in front of the existing env-configured
// scaler rather than replacing it. newFleetPoolScaler is untouched: a pool with
// no cluster binding still resolves to exactly the scaler it always did, which
// is what keeps APEXLOAD_GENERATOR_PROVISIONER and
// APEXLOAD_GENERATOR_AUTOSCALING_OWNER deployments working unchanged.

// errClusterScalerUnavailable is returned when a pool names a cluster the
// controller cannot use. This ALWAYS fails closed — falling back to the
// in-cluster scaler would scale generators in a cluster the administrator did
// not choose.
var errClusterScalerUnavailable = errors.New("the cluster connection for this pool is unavailable")

// clusterScalerResolver picks the scaler for one pool.
type clusterScalerResolver interface {
	ScalerForPool(ctx context.Context, pool *storage.FleetPool) (fleetPoolScaler, error)
}

// registryFleetScalerResolver resolves a pool's cluster binding to a scaler,
// falling back to the env-configured scaler for unbound pools.
type registryFleetScalerResolver struct {
	store   storage.ClusterConnectionStore
	clients *clusterClientCache
	// fallback is the scaler for pools with no cluster binding. It is a
	// getter rather than a captured value so a later reassignment of the
	// server's fleetScaler is honored instead of being silently ignored.
	fallback func() fleetPoolScaler
	// quotaChecker is optional; when set, every resolved scaler is wrapped so
	// both the create and the retry provisioning paths get quota preflight.
	quotaChecker allocationQuotaChecker
	// domainTargets is optional. When set, a pool scoped to a domain must scale
	// into that domain's provider — checked here as well as at write time, so a
	// domain retargeted AFTER a pool was created fails closed rather than
	// scaling into a cloud the administrator no longer permits.
	domainTargets storage.DomainDeploymentTargetStore
}

// domainTargetFor returns the constraint that applies to a pool, or (nil, nil)
// when none does.
//
// An unscoped pool (empty DomainID) and a controller whose storage backend has
// no domain-target support both resolve to "unconstrained". That is not a
// silent bypass: a pool can only carry a non-empty domain_id if a store that
// implements DomainDeploymentTargetStore wrote it in the first place.
func (r *registryFleetScalerResolver) domainTargetFor(pool *storage.FleetPool) (*storage.DomainDeploymentTarget, error) {
	if r == nil || r.domainTargets == nil || pool == nil {
		return nil, nil
	}
	if strings.TrimSpace(pool.DomainID) == "" {
		return nil, nil
	}
	target, err := r.domainTargets.GetDomainDeploymentTarget(pool.DomainID)
	if err != nil {
		// Fail closed: an unreadable constraint is not an absent one.
		return nil, fmt.Errorf("%w: the deployment target for domain %q could not be loaded",
			errClusterScalerUnavailable, pool.DomainID)
	}
	return target, nil
}

// fallbackScaler resolves the unbound-pool scaler, tolerating a nil getter.
func (r *registryFleetScalerResolver) fallbackScaler() fleetPoolScaler {
	if r == nil || r.fallback == nil {
		return nil
	}
	return r.fallback()
}

// allocationQuotaChecker admits or refuses a scale against namespace quota.
type allocationQuotaChecker interface {
	CheckScale(ctx context.Context, conn *storage.ClusterConnection, pool *storage.FleetPool, targetReplicas int) (preflightResult, error)
}

func (r *registryFleetScalerResolver) ScalerForPool(ctx context.Context, pool *storage.FleetPool) (fleetPoolScaler, error) {
	if r == nil {
		return nil, errClusterScalerUnavailable
	}
	// The domain check runs BEFORE the unbound early return, but is a total
	// no-op for a pool with no domain binding — see domainTargetFor.
	target, err := r.domainTargetFor(pool)
	if err != nil {
		return nil, err
	}
	if target != nil && strings.TrimSpace(pool.ClusterID) == "" {
		// Falling through to the env-configured scaler here would scale in the
		// controller's own cluster, which is exactly what the domain
		// constraint exists to prevent.
		return nil, fmt.Errorf("%w: domain %q requires deployment into %s and this pool has no cluster binding",
			errClusterScalerUnavailable, pool.DomainID, target.Provider)
	}
	// An unbound pool keeps the pre-registry behavior exactly.
	if pool == nil || strings.TrimSpace(pool.ClusterID) == "" {
		return r.fallbackScaler(), nil
	}
	if r.store == nil {
		return nil, fmt.Errorf("%w: this controller's storage backend does not support cluster connections", errClusterScalerUnavailable)
	}
	conn, err := r.store.GetClusterConnection(pool.ClusterID)
	if err != nil || conn == nil {
		return nil, fmt.Errorf("%w: cluster %q could not be loaded", errClusterScalerUnavailable, pool.ClusterID)
	}
	// Re-check the provider at scale time. The write-time check cannot cover a
	// domain that was retargeted after this pool was bound.
	if target != nil {
		provider, known := storage.ProviderForClusterKind(conn.Kind)
		if !known || provider != target.Provider {
			return nil, fmt.Errorf("%w: domain %q targets %s but cluster %q is %s",
				errClusterScalerUnavailable, pool.DomainID, target.Provider, conn.Name,
				firstNonEmpty(provider, conn.Kind))
		}
	}
	client, err := r.clients.ClientFor(ctx, conn)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errClusterScalerUnavailable, err)
	}
	scaler := fleetPoolScaler(kubernetesFleetScaler{
		kube:            newKubeopsRestarter(client),
		namespace:       conn.Namespace,
		defaultWorkload: firstNonEmpty(conn.GeneratorStatefulSet, "apexload-generator"),
	})
	if r.quotaChecker != nil {
		scaler = quotaCheckedFleetScaler{inner: scaler, checker: r.quotaChecker, connection: conn}
	}
	return scaler, nil
}

// quotaCheckedFleetScaler refuses a scale that the namespace ResourceQuota
// would reject. It is a decorator rather than a handler-level check so both the
// provisioning-create path and the retry path are covered by construction.
type quotaCheckedFleetScaler struct {
	inner      fleetPoolScaler
	checker    allocationQuotaChecker
	connection *storage.ClusterConnection
}

func (q quotaCheckedFleetScaler) CanProvision() bool { return q.inner.CanProvision() }

func (q quotaCheckedFleetScaler) Describe() ProvisionerInfo { return q.inner.Describe() }

func (q quotaCheckedFleetScaler) ScalePool(ctx context.Context, pool *storage.FleetPool, desiredCount int) (string, int, error) {
	// Check the clamped target, not the raw request, so the verdict matches
	// what would actually be applied.
	target := clampPoolDesired(pool, desiredCount)
	verdict, err := q.checker.CheckScale(ctx, q.connection, pool, target)
	if err != nil {
		return "", 0, fmt.Errorf("cluster capacity preflight failed: %w", err)
	}
	if !verdict.Allowed {
		return "", 0, fmt.Errorf("cluster capacity preflight refused this scale: %s", strings.Join(verdict.Reasons, "; "))
	}
	return q.inner.ScalePool(ctx, pool, desiredCount)
}

// kubeopsRestarter adapts a *kubeops.Client to the KubeRestarter interface the
// existing scaler depends on, so per-cluster clients reuse the same code path
// as the controller's in-cluster client.
type kubeopsRestarter struct {
	client *kubeops.Client
}

func newKubeopsRestarter(client *kubeops.Client) KubeRestarter {
	if client == nil {
		return nil
	}
	return kubeopsRestarter{client: client}
}

func (k kubeopsRestarter) DeletePod(ctx context.Context, namespace, name string) error {
	return k.client.DeletePod(ctx, namespace, name)
}

func (k kubeopsRestarter) RolloutRestart(ctx context.Context, kind, namespace, name string) error {
	return k.client.RolloutRestart(ctx, kind, namespace, name)
}

func (k kubeopsRestarter) ScaleStatefulSet(ctx context.Context, namespace, name string, replicas int) error {
	return k.client.ScaleStatefulSet(ctx, namespace, name, replicas)
}

func (k kubeopsRestarter) PodByIP(ctx context.Context, namespace, selector, ip string) (KubePodRef, error) {
	pod, err := k.client.PodByIP(ctx, namespace, selector, ip)
	if err != nil {
		return KubePodRef{}, err
	}
	return kubePodRefFrom(pod), nil
}

func (k kubeopsRestarter) ListPods(ctx context.Context, namespace, selector string) ([]KubePodRef, error) {
	pods, err := k.client.ListPods(ctx, namespace, selector)
	if err != nil {
		return nil, err
	}
	refs := make([]KubePodRef, 0, len(pods))
	for _, pod := range pods {
		refs = append(refs, kubePodRefFrom(pod))
	}
	return refs, nil
}

func kubePodRefFrom(pod kubeops.PodRef) KubePodRef {
	return KubePodRef{
		Name:        pod.Name,
		Namespace:   pod.Namespace,
		IP:          pod.IP,
		Phase:       pod.Phase,
		Ready:       pod.Ready,
		Terminating: pod.Terminating,
	}
}

// scalerForPool resolves the scaler for a pool, preserving today's behavior
// when no resolver is configured.
func (s *Server) scalerForPool(ctx context.Context, pool *storage.FleetPool) (fleetPoolScaler, error) {
	if s.clusterScalers == nil {
		return s.fleetScaler, nil
	}
	return s.clusterScalers.ScalerForPool(ctx, pool)
}
