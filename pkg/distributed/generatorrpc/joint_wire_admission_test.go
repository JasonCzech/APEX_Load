package generatorrpc

import (
	"fmt"
	"math"
	"testing"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/engine"
	"google.golang.org/protobuf/proto"
)

func TestJointWireAdmissionBeforeRetainedCopies(t *testing.T) {
	fixture := func() (*pb.JointMetricsInfo, []*pb.MetricAggregate) {
		return &pb.JointMetricsInfo{Version: engine.JointEvidenceVersion, Population: "action", Dimensions: []string{"name"}}, []*pb.MetricAggregate{{Name: `["login"]`, Count: 2, Errors: 1, AvgMs: 3}}
	}
	for _, tc := range []struct {
		name   string
		mutate func(*pb.JointMetricsInfo, *[]*pb.MetricAggregate)
	}{
		{"negative count", func(i *pb.JointMetricsInfo, m *[]*pb.MetricAggregate) { (*m)[0].Count = -1 }},
		{"errors exceed count", func(i *pb.JointMetricsInfo, m *[]*pb.MetricAggregate) { (*m)[0].Errors = 3 }},
		{"nonfinite average", func(i *pb.JointMetricsInfo, m *[]*pb.MetricAggregate) { (*m)[0].AvgMs = math.NaN() }},
		{"negative percentile", func(i *pb.JointMetricsInfo, m *[]*pb.MetricAggregate) { (*m)[0].P99Ms = -1 }},
		{"version", func(i *pb.JointMetricsInfo, m *[]*pb.MetricAggregate) { i.Version++ }},
		{"dimensions", func(i *pb.JointMetricsInfo, m *[]*pb.MetricAggregate) {
			i.Dimensions = []string{"name", "group", "region", "phase"}
		}},
		{"negative omitted", func(i *pb.JointMetricsInfo, m *[]*pb.MetricAggregate) { i.OmittedTuples = -1 }},
		{"nil metric", func(i *pb.JointMetricsInfo, m *[]*pb.MetricAggregate) { (*m)[0] = nil }},
		{"malformed key", func(i *pb.JointMetricsInfo, m *[]*pb.MetricAggregate) { (*m)[0].Name = "login" }},
		{"noncanonical key", func(i *pb.JointMetricsInfo, m *[]*pb.MetricAggregate) { (*m)[0].Name = `["\u006cogin"]` }},
		{"duplicate key", func(i *pb.JointMetricsInfo, m *[]*pb.MetricAggregate) {
			*m = append(*m, proto.Clone((*m)[0]).(*pb.MetricAggregate))
		}},
		{"cardinality", func(i *pb.JointMetricsInfo, m *[]*pb.MetricAggregate) {
			for n := len(*m); n <= engine.JointMaxKeys; n++ {
				*m = append(*m, &pb.MetricAggregate{Name: fmt.Sprintf(`["key-%d"]`, n), Count: 1})
			}
		}},
		{"digest", func(i *pb.JointMetricsInfo, m *[]*pb.MetricAggregate) {
			(*m)[0].DurationDigest = &pb.TDigestSnapshot{Count: 1, Centroids: make([]*pb.TDigestCentroid, 1025)}
		}},
		{"sketch", func(i *pb.JointMetricsInfo, m *[]*pb.MetricAggregate) {
			(*m)[0].DurationSketch = &pb.DurationSketchSnapshot{Count: 1, Bins: make([]*pb.DurationSketchBin, engine.DurationSketchMaxBuckets+1)}
		}},
		{"other", func(i *pb.JointMetricsInfo, m *[]*pb.MetricAggregate) {
			i.Other = &pb.MetricAggregate{Count: 1, DurationDigest: &pb.TDigestSnapshot{Count: 1, Centroids: make([]*pb.TDigestCentroid, 1025)}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			i, m := fixture()
			tc.mutate(i, &m)
			out, state := jointPublicationFromProto(i, m)
			if out != nil || state == nil || state.Version != 0 || !state.Incomplete {
				t.Fatal("malformed source became measured or absent", out, state)
			}
		})
	}
	i, m := fixture()
	size := proto.Size(&pb.MetricsBatch{JointMetricsInfo: i, JointMetricsV4: m})
	if !admitJointPublicationProto(i, m, size) || admitJointPublicationProto(i, m, size-1) {
		t.Fatal("encoded byte boundary not enforced")
	}
	out, state := jointPublicationFromProto(i, m)
	if len(out) != 1 || out[0].Count != 2 || out[0].Errors != 1 || state.Version != engine.JointEvidenceVersion {
		t.Fatal("valid tuple lost", out, state)
	}
	state.Dimensions[0] = "group"
	if i.Dimensions[0] != "name" {
		t.Fatal("dimension copy aliases wire")
	}
	if out, state := jointPublicationFromProto(nil, nil); out != nil || state != nil {
		t.Fatal("legacy absence became evidence")
	}
	if out, state := jointPublicationFromProto(nil, m); out != nil || state == nil || state.Version != 0 {
		t.Fatal("orphan tuples silently accepted")
	}
}

func TestJointWireAdmissionKeepsExactCardinalityBoundary(t *testing.T) {
	i := &pb.JointMetricsInfo{Version: engine.JointEvidenceVersion, Population: "transaction", Dimensions: []string{"name"}}
	m := make([]*pb.MetricAggregate, engine.JointMaxKeys)
	for n := range m {
		m[n] = &pb.MetricAggregate{Name: fmt.Sprintf(`["key-%d"]`, n), Count: 1}
	}
	out, state := jointPublicationFromProto(i, m)
	if len(out) != engine.JointMaxKeys || state == nil || state.Incomplete {
		t.Fatal("exact source cardinality lost", len(out), state)
	}
}

func TestJointWireAdmissionThroughMetricsStreamPreservesOtherPopulations(t *testing.T) {
	for _, malformed := range []bool{false, true} {
		t.Run(fmt.Sprint(malformed), func(t *testing.T) {
			info := &pb.JointMetricsInfo{Version: engine.JointEvidenceVersion, Population: "action", Dimensions: []string{"name"}, OmittedTuples: 1}
			metrics := []*pb.MetricAggregate{{Name: `["login"]`, Count: 2, Errors: 1}}
			if malformed {
				metrics = append(metrics, proto.Clone(metrics[0]).(*pb.MetricAggregate))
			}
			wire := &pb.MetricsBatch{TestId: "run", BatchSequence: 7, JointMetricsInfo: info, JointMetricsV4: metrics, ActionMetrics: []*pb.MetricAggregate{{Name: "login", Count: 7}}}
			data, err := proto.Marshal(wire)
			if err != nil {
				t.Fatal(err)
			}
			var decoded pb.MetricsBatch
			if err = proto.Unmarshal(data, &decoded); err != nil {
				t.Fatal(err)
			}
			got, err := (&MetricsStream{stream: oneTimingBatchStream{batch: &decoded}}).Receive()
			if err != nil || got.TestID != "run" || got.BatchSequence != 7 || len(got.ActionMetrics) != 1 || got.ActionMetrics[0].Count != 7 {
				t.Fatal("joint refusal damaged independent action evidence", got, err)
			}
			if malformed {
				if got.JointMetrics != nil || got.JointInfo == nil || got.JointInfo.Version != 0 || !got.JointInfo.Incomplete {
					t.Fatal("invalid joint publication became measured", got.JointInfo)
				}
			} else {
				if len(got.JointMetrics) != 1 || got.JointMetrics[0].Count != 2 || got.JointInfo.Version != engine.JointEvidenceVersion || !got.JointInfo.Incomplete || got.JointInfo.OmittedTuples != 1 {
					t.Fatal("valid source or omission disclosure lost", got.JointInfo)
				}
			}
		})
	}
}
