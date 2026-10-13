package generatorrpc

import (
	"testing"

	pb "github.com/jasonczech/apexload/api/proto"
)

// Each group's completed iterations ride with the group, from its own
// execution's stats; a group whose execution has not published yet has
// completed none.
func TestGeneratorBatchCarriesEachGroupsCompletedIterations(t *testing.T) {
	service := newIsolatedService(t, 50)
	assignment := assignedTest{
		testID: "run", committed: true, vuserCount: 2,
		groups:     map[string]int32{"checkout": 1, "browse": 1},
		groupStats: map[string]GeneratorStats{"checkout": {CompletedIterations: 7}},
	}
	batch := service.metricsBatchForAssignment("run", assignment, "RUNNING", "")
	got := map[string]*int64{}
	for _, group := range groupMetricsFromProto(batch.GetGroups()) {
		got[group.Name] = group.CompletedIterations
	}
	if got["checkout"] == nil || *got["checkout"] != 7 || got["browse"] == nil || *got["browse"] != 0 {
		t.Fatalf("group iterations = checkout %v, browse %v; want 7 and 0", got["checkout"], got["browse"])
	}
}

// A generator that predates the field sends none, which the controller reads
// as not reported rather than zero; a negative count reads as zero.
func TestGroupCompletedIterationsFromProto(t *testing.T) {
	negative := int64(-3)
	groups := groupMetricsFromProto([]*pb.GroupMetric{{Name: "legacy"}, {Name: "skewed", CompletedIterations: &negative}})
	if groups[0].CompletedIterations != nil {
		t.Fatalf("legacy iterations = %d, want not reported", *groups[0].CompletedIterations)
	}
	if groups[1].CompletedIterations == nil || *groups[1].CompletedIterations != 0 {
		t.Fatalf("skewed iterations = %v, want 0", groups[1].CompletedIterations)
	}
}

// A staged generator's groups have completed no iteration, so they do not
// make the fleet's count unknown; terminal evidence rebuilt without its
// counters leaves the count out instead of reporting zero.
func TestStateBatchCompletedIterations(t *testing.T) {
	service := newIsolatedService(t, 50)
	staged := groupMetricsFromProto(service.metricsBatchForState("run", 1, 0, false, map[string]int32{"checkout": 1}, "STAGED", "").GetGroups())
	if len(staged) != 1 || staged[0].CompletedIterations == nil || *staged[0].CompletedIterations != 0 {
		t.Fatalf("staged groups = %+v, want checkout with 0 iterations", staged)
	}
	terminal := groupMetricsFromProto(service.metricsBatchForState("run", 1, 0, false, map[string]int32{"checkout": 0}, "COMPLETE", "").GetGroups())
	if len(terminal) != 1 || terminal[0].CompletedIterations != nil {
		t.Fatalf("rebuilt terminal groups = %+v, want checkout without iterations", terminal)
	}
}
