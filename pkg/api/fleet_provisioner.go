package api

// Fleet pool scaler: the real (in-cluster Kubernetes) provisioner backend behind
// POST /api/v1/fleet/provision-requests. The default is unconfigured (the existing
// not_configured behavior); when APEXLOAD_GENERATOR_PROVISIONER=kubernetes and the
// controller's in-cluster operations are enabled, a request scales the generator
// StatefulSet's replicas via the SAME service-account client + RBAC that
// /system/restart already uses — no new cloud credentials. AWS/Azure/GCP backends
// remain future via the GeneratorProvisioner registry.

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"

	"github.com/jasonczech/apexload/pkg/storage"
)

const kubernetesProvisionerName = "kubernetes"

var errProvisionerNotConfigured = errors.New("no cloud or Kubernetes provisioner is configured for this controller")

// fleetPoolScaler scales a managed generator pool's workload toward a desired count.
type fleetPoolScaler interface {
	Describe() ProvisionerInfo
	CanProvision() bool
	ScalePool(ctx context.Context, pool *storage.FleetPool, desiredCount int) (workload string, scaledTo int, err error)
}

// unconfiguredFleetScaler keeps the existing not_configured behavior.
type unconfiguredFleetScaler struct{}

func (unconfiguredFleetScaler) CanProvision() bool { return false }

func (unconfiguredFleetScaler) Describe() ProvisionerInfo {
	return ProvisionerInfo{
		Name:         staticProvisionerName,
		CanProvision: false,
		Description:  "Uses the pre-configured generator fleet; no on-demand provisioning. Set " + provisionerEnvVar + "=kubernetes (with Kubernetes operations enabled) to scale the generator StatefulSet on demand.",
	}
}

func (unconfiguredFleetScaler) ScalePool(context.Context, *storage.FleetPool, int) (string, int, error) {
	return "", 0, errProvisionerNotConfigured
}

// kubernetesFleetScaler scales the generator StatefulSet's replicas in-cluster.
type kubernetesFleetScaler struct {
	kube            KubeRestarter
	namespace       string
	defaultWorkload string
}

func (kubernetesFleetScaler) CanProvision() bool { return true }

func (k kubernetesFleetScaler) Describe() ProvisionerInfo {
	return ProvisionerInfo{
		Name:         kubernetesProvisionerName,
		CanProvision: true,
		Description:  "Scales the generator StatefulSet's replicas in-cluster (namespace " + k.namespace + ", default workload " + k.defaultWorkload + ") via the controller service account; new pods join through headless-service discovery.",
	}
}

// workloadForPool resolves the StatefulSet to scale: a pool tag
// "kubernetes.statefulset" overrides the controller's default generator workload.
func (k kubernetesFleetScaler) workloadForPool(pool *storage.FleetPool) string {
	if pool != nil {
		if name := strings.TrimSpace(pool.Tags["kubernetes.statefulset"]); name != "" {
			// Only honor a tag that names a generator StatefulSet: it must equal the
			// configured default workload or share its name prefix (e.g. a per-zone
			// "apexload-generator-eu"). This stops a mistyped or malicious pool tag
			// from scaling an unrelated co-tenant StatefulSet in the controller's
			// namespace; anything else falls back to the default workload.
			if k.defaultWorkload != "" && (name == k.defaultWorkload || strings.HasPrefix(name, k.defaultWorkload+"-")) {
				return name
			}
			slog.Warn("ignoring fleet pool kubernetes.statefulset tag that is not a generator workload",
				"tag", name, "default_workload", k.defaultWorkload)
		}
	}
	return k.defaultWorkload
}

func (k kubernetesFleetScaler) ScalePool(ctx context.Context, pool *storage.FleetPool, desiredCount int) (string, int, error) {
	if k.kube == nil {
		return "", 0, errProvisionerNotConfigured
	}
	workload := k.workloadForPool(pool)
	if strings.TrimSpace(workload) == "" {
		return "", 0, errors.New("no generator StatefulSet configured to scale")
	}
	target := clampPoolDesired(pool, desiredCount)
	if err := k.kube.ScaleStatefulSet(ctx, k.namespace, workload, target); err != nil {
		return workload, 0, err
	}
	return workload, target, nil
}

// clampPoolDesired bounds the desired count to the pool's [MinSize, MaxSize] window.
func clampPoolDesired(pool *storage.FleetPool, desired int) int {
	if desired < 0 {
		desired = 0
	}
	if pool != nil {
		if pool.MaxSize > 0 && desired > pool.MaxSize {
			desired = pool.MaxSize
		}
		if pool.MinSize > 0 && desired < pool.MinSize {
			desired = pool.MinSize
		}
	}
	return desired
}

// newFleetPoolScaler selects the scaler from env + the controller's K8s-ops config.
// Kubernetes is active only when explicitly selected AND in-cluster operations are
// enabled with a client; otherwise the unconfigured default preserves the existing
// not_configured behavior.
func newFleetPoolScaler(kube KubeRestarter, k8s K8sRestartSettings) fleetPoolScaler {
	if strings.TrimSpace(os.Getenv(provisionerEnvVar)) == kubernetesProvisionerName && kube != nil && k8s.Enabled {
		return kubernetesFleetScaler{
			kube:            kube,
			namespace:       k8s.Namespace,
			defaultWorkload: firstNonEmpty(k8s.GeneratorStatefulSet, "apexload-generator"),
		}
	}
	return unconfiguredFleetScaler{}
}
