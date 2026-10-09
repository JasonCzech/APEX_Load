package generatorrpc

import (
	"fmt"
	"slices"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func jointGroupSnapshot(t *testing.T, projection *engine.JointProjection, results []*protocol.Result) ([]MetricAggregate, *JointMetricsInfo) {
	t.Helper()
	mc := engine.NewMetricsCollector(16)
	mc.SetJointProjection(projection, map[string]string{"a": "http", "b": "http"})
	// One long window: Stop drains every recorded sample into it.
	mc.Start(time.Hour)
	for _, r := range results {
		mc.Record(r)
	}
	mc.Stop()
	snapshot, ok := mc.LatestSnapshot()
	if !ok || snapshot.Joint == nil {
		t.Fatal("no joint snapshot")
	}
	return jointAggregatesFromEngine(snapshot.Joint)
}

func jointSample(name, group, region string, ms int) *protocol.Result {
	return &protocol.Result{ActionName: name, Group: group, Region: region, Success: true, Duration: time.Duration(ms) * time.Millisecond}
}

// Two groups' joint evidence merged on a generator equals counting all the
// samples in one place: tuples sum exactly, by key.
func TestJointEvidenceMergeEqualsIndependentCounters(t *testing.T) {
	projection := &engine.JointProjection{Population: "action", Dimensions: []string{"name", "region"}}
	a, infoA := jointGroupSnapshot(t, projection, []*protocol.Result{jointSample("login", "a", "east", 10), jointSample("login", "a", "east", 30), jointSample("search", "a", "west", 5)})
	b, infoB := jointGroupSnapshot(t, projection, []*protocol.Result{jointSample("login", "b", "east", 20), jointSample("login", "b", "west", 40)})
	merged, info, err := mergeJointEvidence([][]MetricAggregate{a, b}, []*JointMetricsInfo{infoA, infoB})
	if err != nil || info == nil || info.Incomplete || info.Other != nil {
		t.Fatalf("merge %+v %v", info, err)
	}
	counts := map[string]int64{}
	for _, aggregate := range merged {
		counts[aggregate.Name] = aggregate.Count
	}
	eastLogin, _ := engine.JointTupleKey([]string{"login", "east"})
	westLogin, _ := engine.JointTupleKey([]string{"login", "west"})
	westSearch, _ := engine.JointTupleKey([]string{"search", "west"})
	if len(counts) != 3 || counts[eastLogin] != 3 || counts[westLogin] != 1 || counts[westSearch] != 1 {
		t.Fatalf("merged counts %v", counts)
	}
	for _, aggregate := range merged {
		if aggregate.Name == eastLogin && (aggregate.DurationDigest == nil || aggregate.DurationDigest.Count != 3) {
			t.Fatalf("merged digest %+v", aggregate.DurationDigest)
		}
	}
	// A group counted under another projection makes the window's joint
	// evidence unavailable, never mixed.
	_, infoC := jointGroupSnapshot(t, &engine.JointProjection{Population: "action", Dimensions: []string{"name"}}, []*protocol.Result{jointSample("login", "a", "east", 10)})
	if _, _, err := mergeJointEvidence([][]MetricAggregate{a, nil}, []*JointMetricsInfo{infoA, infoC}); err == nil {
		t.Fatal("different projections were merged")
	}
}

// Merged tuples past the bound keep the busiest JointMaxKeys and fold the
// rest into an explicit other bucket.
func TestJointEvidenceMergeRebounds(t *testing.T) {
	info := func() *JointMetricsInfo {
		return &JointMetricsInfo{Version: engine.JointEvidenceVersion, Population: "action", Dimensions: []string{"name"}}
	}
	part := func(offset, n int, count int64) []MetricAggregate {
		out := make([]MetricAggregate, 0, n)
		for i := 0; i < n; i++ {
			key, _ := engine.JointTupleKey([]string{fmt.Sprintf("action-%04d", offset+i)})
			out = append(out, MetricAggregate{Name: key, Count: count, AvgMs: 10})
		}
		return out
	}
	first, second := info(), info()
	second.Other = &MetricAggregate{Name: "other", Count: 7, AvgMs: 10}
	second.OmittedTuples = 2
	merged, merged_info, err := mergeJointEvidence([][]MetricAggregate{part(0, 300, 5), part(300, 300, 1)}, []*JointMetricsInfo{first, second})
	if err != nil {
		t.Fatal(err)
	}
	if len(merged) != engine.JointMaxKeys || !merged_info.Incomplete || merged_info.OmittedTuples != 2+600-engine.JointMaxKeys {
		t.Fatalf("rebound %d tuples incomplete=%v omitted=%d", len(merged), merged_info.Incomplete, merged_info.OmittedTuples)
	}
	if merged_info.Other == nil || merged_info.Other.Count != 7+int64(600-engine.JointMaxKeys) {
		t.Fatalf("other %+v", merged_info.Other)
	}
	busiest, _ := engine.JointTupleKey([]string{"action-0000"})
	if !slices.ContainsFunc(merged, func(a MetricAggregate) bool { return a.Name == busiest }) {
		t.Fatal("the busiest tuples were not kept")
	}
}

func TestJointProjectionOnTheAssignmentWire(t *testing.T) {
	if _, err := jointProjectionFromProto(&pb.JointEvidenceProjection{Population: "action", Dimensions: []string{"name", "host"}}); err == nil {
		t.Fatal("an unsupported dimension was accepted")
	}
	if err := validateAssignmentMetadataBounds(&pb.WorkAssignment{TestId: "t", JointEvidenceProjection: &pb.JointEvidenceProjection{Population: "message", Dimensions: []string{"name"}}}); err == nil {
		t.Fatal("an invalid projection passed assignment validation")
	}
	plain := &pb.WorkAssignment{TestId: "t", GroupName: "g"}
	before := workAssignmentFingerprint(plain)
	projected := &pb.WorkAssignment{TestId: "t", GroupName: "g", JointEvidenceProjection: jointProjectionToProto(&engine.JointProjection{Population: "action", Dimensions: []string{"name"}})}
	if workAssignmentFingerprint(projected) == before {
		t.Fatal("a projection does not change the assignment fingerprint")
	}
	transactions := &pb.WorkAssignment{TestId: "t", GroupName: "g", JointEvidenceProjection: jointProjectionToProto(&engine.JointProjection{Population: "transaction", Dimensions: []string{"name"}})}
	regions := &pb.WorkAssignment{TestId: "t", GroupName: "g", JointEvidenceProjection: jointProjectionToProto(&engine.JointProjection{Population: "action", Dimensions: []string{"region"}})}
	if workAssignmentFingerprint(transactions) == workAssignmentFingerprint(projected) || workAssignmentFingerprint(regions) == workAssignmentFingerprint(projected) {
		t.Fatal("a different population or dimension kept the assignment fingerprint")
	}
	if workAssignmentFingerprint(&pb.WorkAssignment{TestId: "t", GroupName: "g"}) != before {
		t.Fatal("an assignment without a projection changed its fingerprint")
	}
	info := &JointMetricsInfo{Version: 4, Population: "action", Dimensions: []string{"name"}, Other: &MetricAggregate{Name: "other", Count: 2}, OmittedTuples: 3, Incomplete: true}
	if back := jointInfoFromProto(jointInfoToProto(info)); back.Version != 4 || back.Other == nil || back.Other.Count != 2 || back.OmittedTuples != 3 || !back.Incomplete || back.Dimensions[0] != "name" {
		t.Fatalf("info round trip %+v", back)
	}
}
