package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Reserved node-selector keys. The pool's NodePool and Zone fields own these,
// so allowing them in NodeSelector too would let two settings fight over one
// map key with the winner decided by map iteration order.
//
// NodeSelectorAgentPool is only the DEFAULT node-pool key. It is correct for
// AKS, whose nodes carry both the vendor label and this generic one, but EKS
// nodes carry neither: a managed node group is labeled
// eks.amazonaws.com/nodegroup and a Karpenter node karpenter.sh/nodepool.
// PoolAllocation.NodePoolLabel overrides it — see EffectiveNodeSelector.
const (
	NodeSelectorAgentPool = "agentpool"
	NodeSelectorZone      = "topology.kubernetes.io/zone"
)

// Toleration effects Kubernetes accepts. An empty effect matches all effects.
var validTolerationEffects = map[string]struct{}{
	"":                 {},
	"NoSchedule":       {},
	"PreferNoSchedule": {},
	"NoExecute":        {},
}

// Toleration lets generator pods schedule onto tainted nodes — the usual way a
// dedicated load-generation node pool is reserved.
type Toleration struct {
	Key      string `json:"key,omitempty"`
	Operator string `json:"operator,omitempty"` // Equal | Exists
	Value    string `json:"value,omitempty"`
	Effect   string `json:"effect,omitempty"`
}

// PoolAllocation is the host-resource budget and placement for a fleet pool's
// generator pods. It is persisted as a single JSON column: NodeSelector is a
// map and Tolerations a list, neither of which fits discrete columns without a
// child table, and nothing filters on these server-side.
//
// Quantities are Kubernetes quantity strings ("500m", "4Gi") rather than
// numbers, so what the operator typed round-trips unchanged.
type PoolAllocation struct {
	CPURequest    string `json:"cpu_request,omitempty"`
	CPULimit      string `json:"cpu_limit,omitempty"`
	MemoryRequest string `json:"memory_request,omitempty"`
	MemoryLimit   string `json:"memory_limit,omitempty"`
	NodePool      string `json:"node_pool,omitempty"`
	// NodePoolLabel is the label KEY that NodePool is matched against. Empty
	// means NodeSelectorAgentPool, which is what every allocation written
	// before EKS support carries — so existing pools are byte-identical.
	//
	// It is carried per allocation rather than inferred from the cluster's kind
	// because a single cluster can run managed node groups and Karpenter side
	// by side, with a different key for each.
	NodePoolLabel string            `json:"node_pool_label,omitempty"`
	Zone          string            `json:"zone,omitempty"`
	NodeSelector  map[string]string `json:"node_selector,omitempty"`
	Tolerations   []Toleration      `json:"tolerations,omitempty"`
}

// NodePoolSelectorKey is the label key this allocation pins its node pool with.
func (a *PoolAllocation) NodePoolSelectorKey() string {
	if a == nil {
		return NodeSelectorAgentPool
	}
	if label := strings.TrimSpace(a.NodePoolLabel); label != "" {
		return label
	}
	return NodeSelectorAgentPool
}

// IsZero reports whether the allocation carries no settings at all, which is
// the state every pre-existing pool starts in after the column migration.
func (a *PoolAllocation) IsZero() bool {
	if a == nil {
		return true
	}
	// NodePoolLabel is deliberately NOT counted: a label key with no pool name
	// pins nothing, so an allocation carrying only that is still empty.
	return a.CPURequest == "" && a.CPULimit == "" && a.MemoryRequest == "" && a.MemoryLimit == "" &&
		a.NodePool == "" && a.Zone == "" && len(a.NodeSelector) == 0 && len(a.Tolerations) == 0
}

// EffectiveNodeSelector merges the dedicated NodePool and Zone fields into the
// free-form selector map. This is the single source of truth for what lands on
// the pod spec, so the two representations cannot diverge.
func (a *PoolAllocation) EffectiveNodeSelector() map[string]string {
	if a == nil {
		return nil
	}
	selector := make(map[string]string, len(a.NodeSelector)+2)
	for key, value := range a.NodeSelector {
		selector[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	if pool := strings.TrimSpace(a.NodePool); pool != "" {
		selector[a.NodePoolSelectorKey()] = pool
	}
	if zone := strings.TrimSpace(a.Zone); zone != "" {
		selector[NodeSelectorZone] = zone
	}
	if len(selector) == 0 {
		return nil
	}
	return selector
}

// SelectorKeys returns the effective selector keys in a stable order, for
// deterministic patch construction and test assertions.
func (a *PoolAllocation) SelectorKeys() []string {
	selector := a.EffectiveNodeSelector()
	keys := make([]string, 0, len(selector))
	for key := range selector {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// Validate checks the allocation's shape. Quantity strings are validated by the
// API layer against the kubeops parser; this layer enforces the structural
// rules so an unvalidated blob never reaches the database.
func (a *PoolAllocation) Validate() error {
	if a == nil || a.IsZero() {
		return nil
	}
	// The reserved key is the EFFECTIVE one, not the literal "agentpool": on an
	// EKS pool the node-pool field writes eks.amazonaws.com/nodegroup, so a
	// hand-written selector for that key would be the thing that collides.
	nodePoolKey := a.NodePoolSelectorKey()
	for key, value := range a.NodeSelector {
		trimmed := strings.TrimSpace(key)
		if trimmed == "" {
			return errors.New("node selector keys must not be empty")
		}
		if trimmed == nodePoolKey {
			return fmt.Errorf("use the node pool field rather than a %q node selector", nodePoolKey)
		}
		if trimmed == NodeSelectorZone {
			return fmt.Errorf("use the zone field rather than a %q node selector", NodeSelectorZone)
		}
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("node selector %q must have a value", trimmed)
		}
	}
	for index, toleration := range a.Tolerations {
		if _, ok := validTolerationEffects[strings.TrimSpace(toleration.Effect)]; !ok {
			return fmt.Errorf("toleration %d has unsupported effect %q", index+1, toleration.Effect)
		}
		switch strings.TrimSpace(toleration.Operator) {
		case "", "Equal":
			if strings.TrimSpace(toleration.Key) == "" {
				return fmt.Errorf("toleration %d requires a key when the operator is Equal", index+1)
			}
		case "Exists":
			// Kubernetes rejects a value alongside the Exists operator.
			if strings.TrimSpace(toleration.Value) != "" {
				return fmt.Errorf("toleration %d must not set a value when the operator is Exists", index+1)
			}
		default:
			return fmt.Errorf("toleration %d has unsupported operator %q", index+1, toleration.Operator)
		}
	}
	return nil
}

// marshalAllocation encodes an allocation for the allocation_json column. An
// empty allocation stores an empty string rather than "null" so the column's
// NOT NULL DEFAULT ” and a cleared allocation are indistinguishable.
func marshalAllocation(allocation *PoolAllocation) string {
	if allocation == nil || allocation.IsZero() {
		return ""
	}
	encoded, err := json.Marshal(allocation)
	if err != nil {
		return ""
	}
	return string(encoded)
}

// unmarshalAllocation decodes the allocation_json column, failing soft to nil
// the same way unmarshalTags does — a malformed blob must not break listing
// every pool.
func unmarshalAllocation(raw string) *PoolAllocation {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	var allocation PoolAllocation
	if err := json.Unmarshal([]byte(trimmed), &allocation); err != nil {
		return nil
	}
	if allocation.IsZero() {
		return nil
	}
	return &allocation
}
