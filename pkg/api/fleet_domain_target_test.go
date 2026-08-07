package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// stubDomainTargetStore is a DomainDeploymentTargetStore double.
type stubDomainTargetStore struct {
	targets map[string]*storage.DomainDeploymentTarget
	pools   map[string][]*storage.FleetPool
	err     error
	reads   int
}

func (s *stubDomainTargetStore) GetDomainDeploymentTarget(domainID string) (*storage.DomainDeploymentTarget, error) {
	s.reads++
	if s.err != nil {
		return nil, s.err
	}
	return s.targets[domainID], nil
}

func (s *stubDomainTargetStore) ListDomainDeploymentTargets() ([]*storage.DomainDeploymentTarget, error) {
	out := make([]*storage.DomainDeploymentTarget, 0, len(s.targets))
	for _, target := range s.targets {
		out = append(out, target)
	}
	return out, nil
}

func (s *stubDomainTargetStore) UpsertDomainDeploymentTargetIfUnchanged(target *storage.DomainDeploymentTarget, _ time.Time) (bool, error) {
	if s.targets == nil {
		s.targets = map[string]*storage.DomainDeploymentTarget{}
	}
	s.targets[target.DomainID] = target
	return true, nil
}

func (s *stubDomainTargetStore) DeleteDomainDeploymentTarget(domainID string) error {
	delete(s.targets, domainID)
	return nil
}

func (s *stubDomainTargetStore) ListFleetPoolsForDomain(domainID string) ([]*storage.FleetPool, error) {
	return s.pools[domainID], nil
}

// stubClusterStore is a minimal ClusterConnectionStore double.
type stubClusterStore struct {
	connections map[string]*storage.ClusterConnection
}

func (s *stubClusterStore) GetClusterConnection(id string) (*storage.ClusterConnection, error) {
	conn, ok := s.connections[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return conn, nil
}

func (s *stubClusterStore) CreateClusterConnection(*storage.ClusterConnection) error { return nil }
func (s *stubClusterStore) UpdateClusterConnectionIfUnchanged(*storage.ClusterConnection, time.Time) (bool, error) {
	return true, nil
}
func (s *stubClusterStore) UpdateClusterConnectionHealth(string, string, string, time.Time) error {
	return nil
}
func (s *stubClusterStore) ListClusterConnections() ([]*storage.ClusterConnection, error) {
	return nil, nil
}
func (s *stubClusterStore) DeleteClusterConnectionIfUnreferenced(string) (bool, error) {
	return true, nil
}
func (s *stubClusterStore) ListFleetPoolsForCluster(string) ([]*storage.FleetPool, error) {
	return nil, nil
}

// TestUnscopedPoolIsUnaffectedByDomainDeploymentTargets is the
// backward-compatibility guarantee for domain targets, the counterpart to
// TestUnboundPoolKeepsTheEnvConfiguredScaler. Every pool that predates this
// feature carries domain_id="", so a constraint that applies to every domain
// must still leave it resolving to exactly the scaler it always used.
// Do not delete this test.
func TestUnscopedPoolIsUnaffectedByDomainDeploymentTargets(t *testing.T) {
	t.Setenv(provisionerEnvVar, "")
	fallback := newFleetPoolScaler(nil, K8sRestartSettings{})

	// A store that would constrain literally any domain it is asked about.
	targets := &stubDomainTargetStore{targets: map[string]*storage.DomainDeploymentTarget{}}
	for _, domain := range []string{"", "default", "domain-1"} {
		targets.targets[domain] = &storage.DomainDeploymentTarget{
			DomainID: domain, Provider: storage.DeploymentProviderAWSEKS, DefaultClusterID: "cluster-eks",
		}
	}
	resolver := &registryFleetScalerResolver{
		fallback:      func() fleetPoolScaler { return fallback },
		domainTargets: targets,
	}

	pool := &storage.FleetPool{ID: "pool-legacy", Name: "legacy"}
	scaler, err := resolver.ScalerForPool(context.Background(), pool)
	if err != nil {
		t.Fatalf("ScalerForPool: %v", err)
	}
	if scaler != fallback {
		t.Fatalf("scaler = %T, want the env-configured fallback unchanged", scaler)
	}
	if targets.reads != 0 {
		t.Fatalf("the domain target store was consulted %d times for an unscoped pool; it must be skipped entirely", targets.reads)
	}
}

// TestDomainRetargetFailsClosedForAConflictingPool pins the scale-time half of
// the hard constraint. Write-time validation cannot cover a domain that was
// retargeted after the pool was bound, so the resolver re-checks and refuses.
func TestDomainRetargetFailsClosedForAConflictingPool(t *testing.T) {
	recorder := &recordingKubeRestarter{}
	fallback := kubernetesFleetScaler{kube: recorder, namespace: "apex", defaultWorkload: "apexload-generator"}

	resolver := &registryFleetScalerResolver{
		fallback: func() fleetPoolScaler { return fallback },
		store: &stubClusterStore{connections: map[string]*storage.ClusterConnection{
			"cluster-aks": {ID: "cluster-aks", Name: "eastus", Kind: storage.ClusterKindAKS, Namespace: "apex"},
		}},
		domainTargets: &stubDomainTargetStore{targets: map[string]*storage.DomainDeploymentTarget{
			// The superuser has since retargeted this domain to AWS.
			"domain-1": {DomainID: "domain-1", Provider: storage.DeploymentProviderAWSEKS},
		}},
	}

	pool := &storage.FleetPool{ID: "pool-1", Name: "bound", DomainID: "domain-1", ClusterID: "cluster-aks"}
	scaler, err := resolver.ScalerForPool(context.Background(), pool)
	if err == nil {
		t.Fatalf("ScalerForPool succeeded (%T); a retargeted domain must fail closed", scaler)
	}
	if !errors.Is(err, errClusterScalerUnavailable) {
		t.Fatalf("error = %v, want errClusterScalerUnavailable", err)
	}
	if recorder.callCount() != 0 {
		t.Fatalf("made %d Kubernetes calls; a refused resolution must touch nothing", recorder.callCount())
	}
}

// TestScopedPoolWithoutAClusterBindingFailsClosed pins that a constrained pool
// can never fall through to the env-configured scaler, which would scale in the
// controller's own cluster — the thing the constraint exists to prevent.
func TestScopedPoolWithoutAClusterBindingFailsClosed(t *testing.T) {
	recorder := &recordingKubeRestarter{}
	fallback := kubernetesFleetScaler{kube: recorder, namespace: "apex", defaultWorkload: "apexload-generator"}
	resolver := &registryFleetScalerResolver{
		fallback: func() fleetPoolScaler { return fallback },
		domainTargets: &stubDomainTargetStore{targets: map[string]*storage.DomainDeploymentTarget{
			"domain-1": {DomainID: "domain-1", Provider: storage.DeploymentProviderAzureAKS},
		}},
	}

	pool := &storage.FleetPool{ID: "pool-1", Name: "unbound-but-scoped", DomainID: "domain-1"}
	if _, err := resolver.ScalerForPool(context.Background(), pool); !errors.Is(err, errClusterScalerUnavailable) {
		t.Fatalf("error = %v, want errClusterScalerUnavailable", err)
	}
	if recorder.callCount() != 0 {
		t.Fatalf("made %d Kubernetes calls, want 0", recorder.callCount())
	}
}

// TestUnreadableDomainTargetFailsClosed pins that a storage error is treated as
// a constraint that might apply, not as an absent one.
func TestUnreadableDomainTargetFailsClosed(t *testing.T) {
	resolver := &registryFleetScalerResolver{
		fallback:      func() fleetPoolScaler { return unconfiguredFleetScaler{} },
		domainTargets: &stubDomainTargetStore{err: errors.New("database is closed")},
	}
	pool := &storage.FleetPool{ID: "pool-1", DomainID: "domain-1", ClusterID: "cluster-aks"}
	if _, err := resolver.ScalerForPool(context.Background(), pool); !errors.Is(err, errClusterScalerUnavailable) {
		t.Fatalf("error = %v, want errClusterScalerUnavailable", err)
	}
}

// TestMatchingProviderResolvesNormally proves the constraint is not simply
// blocking everything.
func TestMatchingProviderResolvesNormally(t *testing.T) {
	resolver := &registryFleetScalerResolver{
		fallback: func() fleetPoolScaler { return unconfiguredFleetScaler{} },
		store: &stubClusterStore{connections: map[string]*storage.ClusterConnection{
			"cluster-eks": {
				ID: "cluster-eks", Name: "us-east-1", Kind: storage.ClusterKindEKS, Namespace: "apex",
				APIServerURL: "https://example.eks.amazonaws.com", TLSMode: storage.ClusterTLSSystemRoots,
				AuthMode: storage.ClusterAuthStaticToken,
			},
		}},
		clients: newClusterClientCache(nil, nil),
		domainTargets: &stubDomainTargetStore{targets: map[string]*storage.DomainDeploymentTarget{
			"domain-1": {DomainID: "domain-1", Provider: storage.DeploymentProviderAWSEKS},
		}},
	}
	pool := &storage.FleetPool{ID: "pool-1", DomainID: "domain-1", ClusterID: "cluster-eks"}
	_, err := resolver.ScalerForPool(context.Background(), pool)
	// Client construction fails for want of a credential, but crucially NOT
	// with a provider mismatch: the domain check passed.
	if err != nil && !errors.Is(err, errClusterScalerUnavailable) {
		t.Fatalf("error = %v", err)
	}
	if err != nil && strings.Contains(err.Error(), "targets") {
		t.Fatalf("a matching provider was reported as a mismatch: %v", err)
	}
}

func TestProviderForClusterKindCoversEveryKind(t *testing.T) {
	for kind, want := range map[string]string{
		storage.ClusterKindInCluster: storage.DeploymentProviderKubernetes,
		storage.ClusterKindGeneric:   storage.DeploymentProviderKubernetes,
		storage.ClusterKindAKS:       storage.DeploymentProviderAzureAKS,
		storage.ClusterKindEKS:       storage.DeploymentProviderAWSEKS,
	} {
		got, ok := storage.ProviderForClusterKind(kind)
		if !ok || got != want {
			t.Errorf("ProviderForClusterKind(%q) = (%q, %v), want (%q, true)", kind, got, ok, want)
		}
	}
	// An unknown kind must fail closed rather than read as unconstrained.
	if _, ok := storage.ProviderForClusterKind("gke"); ok {
		t.Error("ProviderForClusterKind(gke) reported a known provider")
	}
}

func TestNormalizeCloudProviderAcceptsLegacyValues(t *testing.T) {
	for value, want := range map[string]string{
		"kubernetes": storage.DeploymentProviderKubernetes,
		"k8s":        storage.DeploymentProviderKubernetes,
		"azure":      storage.DeploymentProviderAzureAKS,
		"aks":        storage.DeploymentProviderAzureAKS,
		"aws":        storage.DeploymentProviderAWSEKS,
		"eks":        storage.DeploymentProviderAWSEKS,
		"AWS":        storage.DeploymentProviderAWSEKS,
		"gcp":        storage.DeploymentProviderGCPGKE,
		"static":     storage.DeploymentProviderStatic,
		"nonsense":   "",
	} {
		if got := storage.NormalizeCloudProvider(value); got != want {
			t.Errorf("NormalizeCloudProvider(%q) = %q, want %q", value, got, want)
		}
	}
	// gcp and static are recognized so legacy rows stay valid, but must not be
	// selectable: no cluster kind maps to them, so a domain constrained to one
	// could admit no pool at all.
	for _, provider := range []string{storage.DeploymentProviderGCPGKE, storage.DeploymentProviderStatic, ""} {
		if storage.IsSelectableDeploymentProvider(provider) {
			t.Errorf("IsSelectableDeploymentProvider(%q) = true", provider)
		}
	}
	for _, provider := range storage.SelectableDeploymentProviders() {
		if !storage.IsSelectableDeploymentProvider(provider) {
			t.Errorf("SelectableDeploymentProviders included non-selectable %q", provider)
		}
	}
}

// TestFleetPoolUpdateDistinguishesOmittedFromClearedBindings pins the
// presence-aware decode. Without it a pool could never be unscoped: an empty
// domain_id would read as "keep", so the dashboard's "Unscoped" option would
// report success while changing nothing — a silent failure.
func TestFleetPoolUpdateDistinguishesOmittedFromClearedBindings(t *testing.T) {
	for _, tc := range []struct {
		name          string
		body          string
		wantDomainID  string
		wantClusterID string
	}{
		{
			name:          "omitted bindings are kept",
			body:          `{"name":"renamed"}`,
			wantDomainID:  "domain-1",
			wantClusterID: "cluster-eks",
		},
		{
			name:          "an explicit empty string clears the binding",
			body:          `{"name":"renamed","domain_id":"","cluster_id":""}`,
			wantDomainID:  "",
			wantClusterID: "",
		},
		{
			name:          "a value rebinds",
			body:          `{"name":"renamed","domain_id":"domain-2","cluster_id":"cluster-aks"}`,
			wantDomainID:  "domain-2",
			wantClusterID: "cluster-aks",
		},
		{
			name:          "one binding can be cleared without touching the other",
			body:          `{"name":"renamed","domain_id":""}`,
			wantDomainID:  "",
			wantClusterID: "cluster-eks",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var req fleetPoolUpdateRequest
			if err := json.Unmarshal([]byte(tc.body), &req); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			// Mirror the handler's apply step against a pool that starts bound.
			existing := &storage.FleetPool{ID: "pool-1", DomainID: "domain-1", ClusterID: "cluster-eks"}
			if req.DomainID != nil {
				existing.DomainID = strings.TrimSpace(*req.DomainID)
			}
			if req.ClusterID != nil {
				existing.ClusterID = strings.TrimSpace(*req.ClusterID)
			}
			if existing.DomainID != tc.wantDomainID {
				t.Errorf("DomainID = %q, want %q", existing.DomainID, tc.wantDomainID)
			}
			if existing.ClusterID != tc.wantClusterID {
				t.Errorf("ClusterID = %q, want %q", existing.ClusterID, tc.wantClusterID)
			}
			// The embedded pool must not shadow-capture the binding fields, or
			// the pointers above would always be nil.
			if req.FleetPool.DomainID != "" || req.FleetPool.ClusterID != "" {
				t.Errorf("embedded FleetPool captured the binding fields (%q/%q); the outer pointers must win",
					req.FleetPool.DomainID, req.FleetPool.ClusterID)
			}
		})
	}
}
