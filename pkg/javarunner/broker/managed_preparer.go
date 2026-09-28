package broker

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net"
	"net/http"
	"net/netip"
	"time"

	"github.com/jasonczech/apexload/pkg/javarunner"
)

// ManagedPreparationConfig is a frozen operator profile for one configuration
// identity. TLSSecret and Pod.Context must be empty; each reserved host receives
// freshly generated transport and its separately bound session context.
// TransportLifetime includes preparation, the admitted run and drain allowance.
// Admission must reject runs that could exceed it, rather than replace sessions.
type ManagedPreparationConfig struct {
	ConfigurationSHA256                                                string
	Pod                                                                ExecutionPodConfig
	BrokerClientURI                                                    string
	LeaseTTL, PreparationTimeout, InvocationTimeout, TransportLifetime time.Duration
}

type ManagedKubernetesPreparer struct {
	store     *Store
	backend   *KubernetesBackend
	epoch     string
	issuer    *HostCredentialIssuer
	tls       *tls.Config
	profile   ManagedPreparationConfig
	contexts  map[string]javarunner.DedicatedHostContext
	newClient func(string, string, SupervisorConfig, *tls.Config) (*SupervisorClient, error)
}

// NewManagedKubernetesPreparer wires the concrete issuer, immutable Secret,
// restricted pod and authenticated readiness path. It has no caller-controlled
// URL, VM arguments, image, Kubernetes credential or certificate signer.
// Session contexts must have been authorized by the upstream broker service.
func NewManagedKubernetesPreparer(store *Store, backend *KubernetesBackend, epoch string, issuer *HostCredentialIssuer, config *tls.Config, profile ManagedPreparationConfig, contexts []javarunner.DedicatedHostContext) (*ManagedKubernetesPreparer, error) {
	if store == nil || backend == nil || store.config.Namespace != backend.namespace || !name.MatchString(epoch) || issuer == nil || issuer.ca == nil || config == nil || config.RootCAs == nil || config.InsecureSkipVerify || profile.Pod.TLSSecret != "" || profile.Pod.Context != nil || !validPolicyDigest(profile.ConfigurationSHA256) || profile.TransportLifetime <= profile.PreparationTimeout || profile.TransportLifetime > 24*time.Hour || len(contexts) == 0 || len(contexts) > maxRecords {
		return nil, ErrState
	}
	control := SupervisorConfig{HostID: "validate", BrokerEpoch: epoch, ConfigurationSHA256: profile.ConfigurationSHA256, BrokerClientURI: profile.BrokerClientURI, LeaseTTL: profile.LeaseTTL, PreparationTimeout: profile.PreparationTimeout, InvocationTimeout: profile.InvocationTimeout}
	if !validSupervisorConfig(control) {
		return nil, ErrState
	}
	// Reuse the actual client policy before any Kubernetes mutation.
	client, e := NewSupervisorClient("https://127.0.0.1:9443", "spiffe://"+issuer.trustDomain+"/java-host/validate", control, config)
	if e != nil {
		return nil, e
	}
	client.Close()
	owned := config.Clone()
	owned.RootCAs = config.RootCAs.Clone()
	profile.Pod.NodeSelector = cloneSelector(profile.Pod.NodeSelector)
	bySession := make(map[string]javarunner.DedicatedHostContext, len(contexts))
	for _, c := range contexts {
		if _, e := javarunner.EncodeDedicatedHostContext(c); e != nil {
			return nil, ErrState
		}
		if _, exists := bySession[c.SessionID]; exists {
			return nil, ErrState
		}
		bySession[c.SessionID] = c
	}
	return &ManagedKubernetesPreparer{store: store, backend: backend, epoch: epoch, issuer: issuer, tls: owned, profile: profile, contexts: bySession, newClient: NewSupervisorClient}, nil
}

func cloneSelector(source map[string]string) map[string]string {
	out := make(map[string]string, len(source))
	for k, v := range source {
		out[k] = v
	}
	return out
}

func (p *ManagedKubernetesPreparer) PrepareHost(ctx context.Context, r Record) (*SupervisorClient, error) {
	if p == nil || p.store == nil || r.BrokerEpoch != p.epoch || r.Binding.ConfigurationSHA256 != p.profile.ConfigurationSHA256 {
		return nil, ErrState
	}
	actual, e := transportRecord(ctx, p.store, p.epoch, r.ID, p.backend.namespace)
	// The reservation carries the only authoritative session and identity.
	if e != nil {
		return nil, e
	}
	if actual.ID != r.ID || actual.BrokerEpoch != r.BrokerEpoch || actual.Binding != r.Binding || actual.SessionID != r.SessionID || actual.MemoryBytes != r.MemoryBytes || actual.PodName != r.PodName || !actual.PrepareUntil.Equal(r.PrepareUntil) || actual.State != javarunner.HostReserved {
		return nil, ErrState
	}
	contextValue, ok := p.contexts[actual.SessionID]
	if !ok {
		return nil, ErrState
	}
	material, e := p.issuer.Issue(actual, time.Now().Add(p.profile.TransportLifetime))
	if e != nil {
		return nil, e
	}
	cfg := p.profile.Pod
	cfg.TLSSecret = material.SecretName()
	cfg.Context = &contextValue
	// Validate the full pod before creating even the transport Secret.
	if _, e = ExecutionPod(p.backend.namespace, actual, cfg); e != nil {
		return nil, e
	}
	preparer := KubernetesHostPreparer{
		Backend: p.backend, Store: p.store, Epoch: p.epoch, PollInterval: 50 * time.Millisecond,
		PodConfig: func(ctx context.Context, _ Record) (ExecutionPodConfig, error) {
			e := p.backend.ProvisionHostTransport(ctx, p.store, p.epoch, r.ID, material)
			return cfg, e
		},
		Connect: func(ctx context.Context, _ Record, receipt PodReceipt) (*SupervisorClient, error) {
			endpoint, e := p.backend.supervisorEndpoint(ctx, receipt)
			if e != nil {
				return nil, e
			}
			control := SupervisorConfig{HostID: r.ID, BrokerEpoch: r.BrokerEpoch, ConfigurationSHA256: r.Binding.ConfigurationSHA256, BrokerClientURI: p.profile.BrokerClientURI, LeaseTTL: p.profile.LeaseTTL, PreparationTimeout: p.profile.PreparationTimeout, InvocationTimeout: p.profile.InvocationTimeout}
			tc := p.tls.Clone()
			tc.ServerName = material.ServerName()
			return p.newClient(endpoint, material.ServerURI(), control, tc)
		},
	}
	return preparer.PrepareHost(ctx, actual)
}

// Resolve only the exact durably bound pod/container. DNS routing is unnecessary:
// the generated name is a TLS verification name, never a discovery authority.
func (k *KubernetesBackend) supervisorEndpoint(ctx context.Context, expected PodReceipt) (string, error) {
	if !validBoundary(expected) || expected.Namespace != k.namespace {
		return "", ErrState
	}
	data, status, e := k.request(ctx, http.MethodGet, expected.Name, nil)
	if e != nil || status != http.StatusOK {
		return "", ErrCleanup
	}
	receipt, e := k.receipt(data, expected.Name)
	if e != nil || receipt != expected {
		return "", ErrCleanup
	}
	var pod struct{ Status struct{ PodIP string } }
	if json.Unmarshal(data, &pod) != nil {
		return "", ErrCleanup
	}
	address, e := netip.ParseAddr(pod.Status.PodIP)
	if e != nil || address.Zone() != "" || !address.IsGlobalUnicast() || address.IsLoopback() {
		return "", ErrCleanup
	}
	return "https://" + net.JoinHostPort(address.String(), "9443"), nil
}
