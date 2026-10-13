package api

import (
	"strings"
	"testing"
)

// TestAutoAssignmentSummarySaysGroupsSpread: the recommended generator count
// is what the footprint needs, not where groups go. Placement takes each
// group's VUsers off its generator, so a second group goes to the idle one;
// the summary must not read as "everything runs on one host".
func TestAutoAssignmentSummarySaysGroupsSpread(t *testing.T) {
	for _, tc := range []struct {
		needed, configured int
		want               string
	}{
		{1, 2, "needs about 1 generator; 2 are configured."},
		{2, 1, "needs about 2 generators; 1 is configured."},
		{1, 0, "needs about 1 generator; none is configured."},
	} {
		got := autoAssignmentSummary(tc.needed, tc.configured)
		if !strings.HasPrefix(got, "Automatic placement puts each group on the reachable generator with the most free capacity.") || !strings.HasSuffix(got, tc.want) {
			t.Errorf("autoAssignmentSummary(%d, %d) = %q, want it to end %q", tc.needed, tc.configured, got, tc.want)
		}
		if strings.Contains(got, "recommends") {
			t.Errorf("summary %q still reads as a host limit", got)
		}
	}

	// Two equal generators and two groups: the second group goes to the idle one.
	loads := map[string]generatorLoadSnapshot{
		"gen-a:9091": {Target: "gen-a:9091", HeartbeatOK: true, AvailableCapacity: 100},
		"gen-b:9091": {Target: "gen-b:9091", HeartbeatOK: true, AvailableCapacity: 100},
	}
	groups := []TestGroup{{Name: "Browse", Protocol: "http", VUsers: 5}, {Name: "Cart", Protocol: "http", VUsers: 5}}
	targets := planDistributedGroupTargets(groups, loads, []string{"gen-a:9091", "gen-b:9091"}, &GeneratorAssignmentSettings{Mode: generatorAssignmentModeAuto}, nil)
	if targets["Browse"] == "" || targets["Cart"] == "" || targets["Browse"] == targets["Cart"] {
		t.Fatalf("automatic placement = %v, want the two groups on different generators", targets)
	}
}
