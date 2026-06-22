package api

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
)

// GeneratorProvisioner abstracts how load generators are obtained for a run.
//
// The default static-fleet provisioner uses the pre-configured generator fleet
// and never creates capacity. A real on-demand provisioner (AWS/Azure/GCP/K8s)
// would implement this interface to spin generators up before a run and tear
// the ephemeral ones down afterward — closing the LRE "cloud load generator
// provisioning" gap. This is the seam for that work; only the static default is
// implemented today.
type GeneratorProvisioner interface {
	// Describe reports the provisioner's identity and whether it can create
	// generators on demand. Surfaced at GET /api/v1/fleet/provisioner.
	Describe() ProvisionerInfo
	// Provision ensures generators matching the request are available and returns
	// their addresses. The static provisioner returns a subset of the configured
	// fleet; an on-demand provisioner would create new instances and mark them
	// Ephemeral.
	Provision(ctx context.Context, req ProvisionRequest) ([]ProvisionedGenerator, error)
	// Deprovision releases ephemeral generators created by Provision. No-op for
	// the static fleet.
	Deprovision(ctx context.Context, addresses []string) error
}

// ProvisionRequest describes the generators a run needs.
type ProvisionRequest struct {
	TestID   string `json:"test_id,omitempty"`
	Region   string `json:"region,omitempty"`
	Protocol string `json:"protocol,omitempty"`
	Count    int    `json:"count,omitempty"`
}

// ProvisionedGenerator is a generator made available for a run.
type ProvisionedGenerator struct {
	Address   string `json:"address"`
	Region    string `json:"region,omitempty"`
	Ephemeral bool   `json:"ephemeral"`
}

// ProvisionerInfo is the observable description of the active provisioner.
type ProvisionerInfo struct {
	Name             string   `json:"name"`
	CanProvision     bool     `json:"can_provision"`
	SupportedRegions []string `json:"supported_regions,omitempty"`
	Description      string   `json:"description"`
}

const (
	staticProvisionerName           = "static-fleet"
	provisionerEnvVar               = "APEXLOAD_GENERATOR_PROVISIONER"
	generatorAutoscalingOwnerEnvVar = "APEXLOAD_GENERATOR_AUTOSCALING_OWNER"
)

var (
	provisionerMu        sync.RWMutex
	provisionerFactories = map[string]func(inventory func(context.Context) SystemInventoryResponse) GeneratorProvisioner{
		staticProvisionerName: func(inv func(context.Context) SystemInventoryResponse) GeneratorProvisioner {
			return &staticGeneratorProvisioner{inventory: inv}
		},
	}
)

// RegisterGeneratorProvisioner registers an on-demand provisioner factory under
// a name. A real cloud provisioner calls this from an init(), then operators
// select it via APEXLOAD_GENERATOR_PROVISIONER. Mirrors the protocol registry.
func RegisterGeneratorProvisioner(name string, factory func(inventory func(context.Context) SystemInventoryResponse) GeneratorProvisioner) {
	provisionerMu.Lock()
	defer provisionerMu.Unlock()
	provisionerFactories[strings.TrimSpace(name)] = factory
}

// resolveGeneratorProvisioner builds the provisioner selected by env, falling
// back to the static fleet when unset or unknown.
func resolveGeneratorProvisioner(inventory func(context.Context) SystemInventoryResponse) GeneratorProvisioner {
	name := strings.TrimSpace(os.Getenv(provisionerEnvVar))
	if name == "" {
		name = staticProvisionerName
	}
	provisionerMu.RLock()
	factory, ok := provisionerFactories[name]
	provisionerMu.RUnlock()
	if !ok {
		factory = provisionerFactories[staticProvisionerName]
	}
	return factory(inventory)
}

// staticGeneratorProvisioner uses the pre-configured fleet and never creates
// capacity — the current (and default) behavior.
type staticGeneratorProvisioner struct {
	inventory func(context.Context) SystemInventoryResponse
}

func (p *staticGeneratorProvisioner) Describe() ProvisionerInfo {
	regions := map[string]struct{}{}
	if p.inventory != nil {
		for _, gen := range p.inventory(context.Background()).Generators {
			if strings.TrimSpace(gen.Region) != "" {
				regions[gen.Region] = struct{}{}
			}
		}
	}
	return ProvisionerInfo{
		Name:             staticProvisionerName,
		CanProvision:     false,
		SupportedRegions: sortedKeys(regions),
		Description:      "Uses the pre-configured generator fleet; no on-demand provisioning. Set " + provisionerEnvVar + " to a registered cloud provisioner to enable elastic generators.",
	}
}

func (p *staticGeneratorProvisioner) Provision(ctx context.Context, req ProvisionRequest) ([]ProvisionedGenerator, error) {
	if p.inventory == nil {
		return nil, fmt.Errorf("no generator inventory available")
	}
	out := make([]ProvisionedGenerator, 0)
	for _, gen := range p.inventory(ctx).Generators {
		if req.Region != "" && !strings.EqualFold(gen.Region, req.Region) {
			continue
		}
		if gen.DrainState == "draining" || gen.DrainState == "drained" {
			continue
		}
		out = append(out, ProvisionedGenerator{Address: gen.Target, Region: gen.Region, Ephemeral: false})
		if req.Count > 0 && len(out) >= req.Count {
			break
		}
	}
	return out, nil
}

func (p *staticGeneratorProvisioner) Deprovision(ctx context.Context, addresses []string) error {
	// Static fleet generators are never ephemeral; nothing to release.
	return nil
}

func sortedKeys(set map[string]struct{}) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
