package storage

import "testing"

// TestEffectiveNodeSelectorHonorsNodePoolLabel pins the EKS fix. Writing the
// generic "agentpool" key onto an EKS pod produces a selector that matches no
// node, so the generator pods stay Pending forever.
func TestEffectiveNodeSelectorHonorsNodePoolLabel(t *testing.T) {
	for _, tc := range []struct {
		name    string
		alloc   PoolAllocation
		wantKey string
	}{
		{
			name:    "an empty label keeps the pre-EKS default",
			alloc:   PoolAllocation{NodePool: "loadgen"},
			wantKey: NodeSelectorAgentPool,
		},
		{
			name:    "an eks managed node group",
			alloc:   PoolAllocation{NodePool: "loadgen-ng", NodePoolLabel: "eks.amazonaws.com/nodegroup"},
			wantKey: "eks.amazonaws.com/nodegroup",
		},
		{
			name:    "a karpenter node pool",
			alloc:   PoolAllocation{NodePool: "loadgen", NodePoolLabel: "karpenter.sh/nodepool"},
			wantKey: "karpenter.sh/nodepool",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selector := tc.alloc.EffectiveNodeSelector()
			if selector[tc.wantKey] != tc.alloc.NodePool {
				t.Fatalf("selector = %v, want %s=%s", selector, tc.wantKey, tc.alloc.NodePool)
			}
			if len(selector) != 1 {
				t.Fatalf("selector = %v, want exactly one key", selector)
			}
		})
	}
}

// TestNodePoolLabelAloneIsStillAnEmptyAllocation keeps a label with no pool
// name from resurrecting an otherwise-cleared allocation.
func TestNodePoolLabelAloneIsStillAnEmptyAllocation(t *testing.T) {
	alloc := PoolAllocation{NodePoolLabel: "eks.amazonaws.com/nodegroup"}
	if !alloc.IsZero() {
		t.Fatal("IsZero() = false for a label with no node pool")
	}
	if marshalAllocation(&alloc) != "" {
		t.Fatal("a label-only allocation was persisted")
	}
}

// TestValidateRejectsTheEffectiveNodePoolKey pins that the reserved-key check
// follows the label override rather than the literal "agentpool".
func TestValidateRejectsTheEffectiveNodePoolKey(t *testing.T) {
	alloc := PoolAllocation{
		NodePool:      "loadgen-ng",
		NodePoolLabel: "eks.amazonaws.com/nodegroup",
		NodeSelector:  map[string]string{"eks.amazonaws.com/nodegroup": "other"},
	}
	if err := alloc.Validate(); err == nil {
		t.Fatal("Validate() = nil; two settings can fight over the effective node-pool key")
	}

	// With the override in play, the generic key is no longer reserved and is a
	// legitimate free-form selector.
	alloc.NodeSelector = map[string]string{NodeSelectorAgentPool: "unrelated"}
	if err := alloc.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want the generic key to be allowed when it is not the effective one", err)
	}
}

// TestNodePoolLabelRoundTripsThroughAllocationJSON guards the persisted shape.
func TestNodePoolLabelRoundTripsThroughAllocationJSON(t *testing.T) {
	alloc := &PoolAllocation{NodePool: "loadgen-ng", NodePoolLabel: "eks.amazonaws.com/nodegroup"}
	decoded := unmarshalAllocation(marshalAllocation(alloc))
	if decoded == nil || decoded.NodePoolLabel != "eks.amazonaws.com/nodegroup" {
		t.Fatalf("decoded = %+v, want the node pool label to survive", decoded)
	}
}
