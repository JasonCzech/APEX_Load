package storage

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Deployment providers. This vocabulary is deliberately more precise than the
// legacy free-text fleet_pools.cloud_provider values: it is a new field with no
// existing rows, and it maps 1:1 onto ClusterConnection.Kind, which is the
// thing a domain target actually constrains.
const (
	DeploymentProviderKubernetes              = "kubernetes"
	DeploymentProviderAzureAKS                = "azure_aks"
	DeploymentProviderAWSEKS                  = "aws_eks"
	DeploymentProviderAzureContainerApps      = "azure_container_apps"
	DeploymentProviderAzureContainerInstances = "azure_container_instances"
	DeploymentProviderAWSECSFargate           = "aws_ecs_fargate"

	// Recognized so pre-existing fleet_pools.cloud_provider rows stay valid,
	// but NOT selectable as a domain deployment target: no cluster kind maps to
	// either, so a domain constrained to one could admit no pool at all.
	DeploymentProviderGCPGKE = "gcp_gke"
	DeploymentProviderStatic = "static"
)

// ErrDomainDeploymentTargetConflict reports that fleet pools scoped to the
// domain are bound to clusters of a different provider.
var ErrDomainDeploymentTargetConflict = errors.New("fleet pools are bound to a cluster of a different provider")

// ErrDomainHasScopedFleetPools blocks deleting a domain that fleet pools are
// still scoped to, mirroring DeleteClusterConnectionIfUnreferenced's refusal.
var ErrDomainHasScopedFleetPools = errors.New("domain still has fleet pools scoped to it")

// ProviderForClusterKind maps a registered cluster's kind to the deployment
// provider a domain target names. The mapping is total over the supported
// kinds; ok is false for an unrecognized kind so callers fail closed rather
// than treating it as unconstrained.
func ProviderForClusterKind(kind string) (string, bool) {
	switch strings.TrimSpace(kind) {
	case ClusterKindInCluster, ClusterKindGeneric:
		return DeploymentProviderKubernetes, true
	case ClusterKindAKS:
		return DeploymentProviderAzureAKS, true
	case ClusterKindEKS:
		return DeploymentProviderAWSEKS, true
	default:
		return "", false
	}
}

// NormalizeCloudProvider folds a legacy fleet_pools.cloud_provider value into
// the canonical vocabulary. It returns "" when the value maps to nothing, which
// callers treat as "unverifiable" rather than as a conflict.
//
// Existing rows are deliberately NOT rewritten: cloud_provider only becomes
// derived-and-verified for pools that opt into a domain.
func NormalizeCloudProvider(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "kubernetes", "k8s":
		return DeploymentProviderKubernetes
	case "azure", "aks", "azure_aks":
		return DeploymentProviderAzureAKS
	case "azure_container_apps", "container_apps":
		return DeploymentProviderAzureContainerApps
	case "azure_container_instances", "container_instances", "aci":
		return DeploymentProviderAzureContainerInstances
	case "aws", "eks", "aws_eks":
		return DeploymentProviderAWSEKS
	case "aws_ecs_fargate", "ecs_fargate", "fargate":
		return DeploymentProviderAWSECSFargate
	case "gcp", "gke", "gcp_gke":
		return DeploymentProviderGCPGKE
	case "static":
		return DeploymentProviderStatic
	default:
		return ""
	}
}

// IsSelectableDeploymentProvider reports whether a provider may be chosen as a
// domain's deployment target.
func IsSelectableDeploymentProvider(provider string) bool {
	switch strings.TrimSpace(provider) {
	case DeploymentProviderKubernetes, DeploymentProviderAzureAKS, DeploymentProviderAWSEKS,
		DeploymentProviderAzureContainerApps, DeploymentProviderAzureContainerInstances, DeploymentProviderAWSECSFargate:
		return true
	default:
		return false
	}
}

// SelectableDeploymentProviders lists the choices a superuser may pick from.
func SelectableDeploymentProviders() []string {
	return []string{DeploymentProviderKubernetes, DeploymentProviderAzureAKS, DeploymentProviderAWSEKS,
		DeploymentProviderAzureContainerApps, DeploymentProviderAzureContainerInstances, DeploymentProviderAWSECSFargate}
}

// DomainDeploymentTarget is a superuser's per-domain deployment constraint: the
// cloud a domain's fleet pools may scale into, and the cluster connection new
// pools inherit.
//
// The ABSENCE of a row means "unconstrained", which is what every domain has
// today and is never seeded — that is the whole backward-compatibility story.
type DomainDeploymentTarget struct {
	DomainID string `json:"domain_id"`
	Provider string `json:"provider"`
	// DefaultClusterID is applied to a new pool that names no cluster. Empty
	// means the administrator must choose one per pool.
	DefaultClusterID string `json:"default_cluster_id,omitempty"`
	// DefaultExecutionTargetID and AllowedExecutionTargetIDs are the normalized
	// policy. Legacy provider/default_cluster_id remain projections until old
	// clients have migrated.
	DefaultExecutionTargetID  string    `json:"default_execution_target_id,omitempty"`
	AllowedExecutionTargetIDs []string  `json:"allowed_execution_target_ids,omitempty"`
	UpdatedBySubject          string    `json:"updated_by_subject,omitempty"`
	CreatedAt                 time.Time `json:"created_at"`
	UpdatedAt                 time.Time `json:"updated_at"`
}

func (t *DomainDeploymentTarget) Validate() error {
	if t == nil {
		return errors.New("deployment target is required")
	}
	if strings.TrimSpace(t.DomainID) == "" {
		return errors.New("domain id is required")
	}
	if !IsSelectableDeploymentProvider(t.Provider) {
		return fmt.Errorf("unsupported deployment provider %q", t.Provider)
	}
	if defaultID := strings.TrimSpace(t.DefaultExecutionTargetID); defaultID != "" {
		allowed := false
		for _, id := range t.AllowedExecutionTargetIDs {
			if strings.TrimSpace(id) == defaultID {
				allowed = true
				break
			}
		}
		if !allowed {
			return errors.New("default execution target must be included in the allowed target list")
		}
	}
	return nil
}

// DomainDeploymentTargetStore is an optional capability, type-asserted from
// Store so existing Store implementations and test doubles keep compiling (the
// same convention as ClusterConnectionStore).
type DomainDeploymentTargetStore interface {
	GetDomainDeploymentTarget(domainID string) (*DomainDeploymentTarget, error)
	ListDomainDeploymentTargets() ([]*DomainDeploymentTarget, error)
	UpsertDomainDeploymentTargetIfUnchanged(target *DomainDeploymentTarget, expectedUpdatedAt time.Time) (bool, error)
	DeleteDomainDeploymentTarget(domainID string) error
	ListFleetPoolsForDomain(domainID string) ([]*FleetPool, error)
}
