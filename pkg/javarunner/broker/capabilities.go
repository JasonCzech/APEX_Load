package broker

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/jasonczech/apexload/pkg/javarunner"
)

// BrokerCapabilities describes the authenticated principal's approved profiles.
// PolicySHA256 binds the complete operator placement/resource/transport profile;
// ConfigurationSHA256 separately identifies the immutable supervisor image file.
type BrokerCapabilities struct {
	Mode           string                    `json:"mode"`
	Containment    string                    `json:"containment"`
	MaxHosts       int                       `json:"max_hosts"`
	MaxVUsers      int                       `json:"max_vusers"`
	MaxMemoryBytes int64                     `json:"max_memory_bytes"`
	Profiles       []BrokerProfileCapability `json:"profiles"`
}
type BrokerProfileCapability struct {
	ID                  string `json:"id"`
	ConfigurationSHA256 string `json:"configuration_sha256"`
	PolicySHA256        string `json:"policy_sha256"`
	MemoryBytes         int64  `json:"memory_bytes"`
	HostLeaseMS         int64  `json:"host_lease_ms"`
	PreparationMS       int64  `json:"preparation_ms"`
	InvocationMS        int64  `json:"invocation_ms"`
	MaxRunMS            int64  `json:"max_run_ms"`
}

func (s *BrokerService) capabilities(p BrokerPrincipal) *BrokerCapabilities {
	caps := &BrokerCapabilities{Mode: "dedicated", Containment: "execution_pod", MaxHosts: s.store.config.Budget.MaxHosts, MaxVUsers: s.store.config.Budget.MaxVUsers, MaxMemoryBytes: s.store.config.Budget.MaxMemoryBytes}
	for _, id := range p.Profiles {
		profile := s.profiles[id]
		caps.Profiles = append(caps.Profiles, BrokerProfileCapability{ID: id, ConfigurationSHA256: profile.ConfigurationSHA256, PolicySHA256: profile.PolicySHA256, MemoryBytes: profile.MemoryBytes, HostLeaseMS: profile.HostLeaseTTL.Milliseconds(), PreparationMS: profile.PreparationTimeout.Milliseconds(), InvocationMS: profile.InvocationTimeout.Milliseconds(), MaxRunMS: profile.maxRun().Milliseconds()})
	}
	sort.Slice(caps.Profiles, func(i, j int) bool { return caps.Profiles[i].ID < caps.Profiles[j].ID })
	return caps
}
func (p BrokerProfile) maxRun() time.Duration {
	return p.TransportLifetime - p.PreparationTimeout - 2*p.HostLeaseTTL
}
func validCapabilities(c *BrokerCapabilities) bool {
	if c == nil || c.Mode != "dedicated" || c.Containment != "execution_pod" || c.MaxHosts < 1 || c.MaxHosts > 10000 || c.MaxVUsers < 1 || c.MaxVUsers > 10000 || c.MaxMemoryBytes < 1 || len(c.Profiles) == 0 || len(c.Profiles) > 256 {
		return false
	}
	seen := map[string]bool{}
	for _, p := range c.Profiles {
		if !name.MatchString(p.ID) || seen[p.ID] || !validPolicyDigest(p.ConfigurationSHA256) || !validPolicyDigest(p.PolicySHA256) || p.MemoryBytes < 1 || p.HostLeaseMS < 1 || p.HostLeaseMS > 60000 || p.PreparationMS < 1000 || p.PreparationMS > 3600000 || p.InvocationMS < 1 || p.InvocationMS > 3600000 || p.MaxRunMS < 1 || p.MaxRunMS > 86400000 {
			return false
		}
		seen[p.ID] = true
	}
	return true
}
func (c *BrokerClient) Capabilities(ctx context.Context) (BrokerCapabilities, error) {
	if c.terminal.Load() {
		return BrokerCapabilities{}, ErrFence
	}
	out, e := c.exchange(ctx, BrokerRequest{Operation: "capabilities"}, true)
	if e != nil {
		return BrokerCapabilities{}, e
	}
	if !validCapabilities(out.Capabilities) || out.OwnerEpoch != "" || out.OwnerToken != "" || out.State != "" || out.WaveID != "" || len(out.Hosts) != 0 || out.Result != nil || out.LeaseTTLMS != 0 {
		c.terminal.Store(true)
		return BrokerCapabilities{}, ErrBrokerTransport
	}
	c.mu.Lock()
	epoch := c.brokerEpoch
	c.mu.Unlock()
	if epoch != "" && epoch != out.BrokerEpoch {
		c.terminal.Store(true)
		return BrokerCapabilities{}, ErrFence
	}
	return *out.Capabilities, nil
}

// ReserveFor freezes the full profile and a bounded post-commit execution
// lifetime. Duration includes ramp, pauses and teardown. It never extends on
// renewal and may not exceed certificate validity. Cleanup remains separately
// quarantined after the execution deadline.
func (c *BrokerClient) ReserveFor(ctx context.Context, execution, group string, profile BrokerProfileCapability, duration time.Duration, contexts []javarunner.DedicatedHostContext) (*BrokerClientWave, error) {
	if duration < time.Millisecond || duration.Milliseconds() > profile.MaxRunMS || !name.MatchString(profile.ID) || !validPolicyDigest(profile.PolicySHA256) {
		return nil, ErrState
	}
	return c.reserve(ctx, execution, group, profile.ID, profile.ConfigurationSHA256, profile.PolicySHA256, duration, contexts)
}
func (c *BrokerClient) Reserve(ctx context.Context, execution, group, profile, digest string, contexts []javarunner.DedicatedHostContext) (*BrokerClientWave, error) {
	caps, e := c.Capabilities(ctx)
	if e != nil {
		return nil, e
	}
	for _, p := range caps.Profiles {
		if p.ID == profile {
			if p.ConfigurationSHA256 != digest {
				return nil, ErrState
			}
			return c.ReserveFor(ctx, execution, group, p, time.Duration(p.MaxRunMS)*time.Millisecond, contexts)
		}
	}
	return nil, errors.New("java_broker_profile_unavailable")
}

func (c BrokerCapabilities) Validate() error {
	if !validCapabilities(&c) {
		return ErrState
	}
	return nil
}
