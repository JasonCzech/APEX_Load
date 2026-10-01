package compute

import "testing"

func TestCapacityPolicyBoundsAndCapabilities(t *testing.T) {
	info := BackendInfo{CanLaunch: true, SupportsSpot: true}
	for _, tc := range []struct {
		policy        CapacityPolicy
		desired, want int
		fail          bool
	}{
		{CapacityPolicy{Selection: "ephemeral", Spot: "required", OnDemandFloor: 2, SpotCeilingPercent: 60}, 10, 6, false},
		{CapacityPolicy{Selection: "ephemeral", Spot: "required", OnDemandFloor: 8, SpotCeilingPercent: 60}, 10, 2, false},
		{CapacityPolicy{Selection: "ephemeral", Spot: "required", SpotCeilingPercent: 20}, 1, 0, true},
		{CapacityPolicy{Selection: "existing", Spot: "required"}, 10, 0, true},
		{CapacityPolicy{Selection: "ephemeral", Spot: "off"}, 10, 0, false},
		{CapacityPolicy{Selection: "ephemeral", Spot: "off", SpotCeilingPercent: 30}, 10, 0, true},
	} {
		got, err := tc.policy.SpotCount(tc.desired, info)
		if (err != nil) != tc.fail || got != tc.want {
			t.Fatalf("%+v: %d %v", tc, got, err)
		}
	}
	info.SupportsSpot = false
	for _, policy := range []string{"required", "preferred"} {
		_, err := (CapacityPolicy{Selection: "ephemeral", Spot: policy, SpotCeilingPercent: 100}).SpotCount(3, info)
		if (err != nil) != (policy == "required") {
			t.Fatalf("unsupported %s: %v", policy, err)
		}
	}
}
