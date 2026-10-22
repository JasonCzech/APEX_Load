package generatorrpc

import (
	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/engine"
	"google.golang.org/protobuf/proto"
	"math"
)

// This is a retained joint-publication envelope bound, independent of the
// transport's message bound and the controller's aggregate pending queue.
const maxJointEncodedBytes = 64 << 20

// Check the received shape before copying tuples, dimensions or distributions.
// A refused publication remains explicitly unavailable; unrelated batch metrics
// retain their existing admission and population contracts.
func jointPublicationFromProto(info *pb.JointMetricsInfo, metrics []*pb.MetricAggregate) ([]MetricAggregate, *JointMetricsInfo) {
	if info == nil && len(metrics) == 0 {
		return nil, nil
	}
	if !admitJointPublicationProto(info, metrics, maxJointEncodedBytes) {
		return nil, &JointMetricsInfo{Incomplete: true} // Unknown version -> invalid joint evidence.
	}
	state := jointInfoFromProto(info)
	state.Incomplete = state.Incomplete || state.OmittedTuples > 0
	return metricAggregatesFromProto(metrics), state
}

func admitJointPublicationProto(info *pb.JointMetricsInfo, metrics []*pb.MetricAggregate, budget int) bool {
	if info == nil || info.GetVersion() != engine.JointEvidenceVersion || info.GetOmittedTuples() < 0 || len(metrics) > engine.JointMaxKeys || budget <= 0 {
		return false
	}
	projection := engine.JointProjection{Population: info.GetPopulation(), Dimensions: info.GetDimensions()}
	if projection.Validate() != nil {
		return false
	}
	shape := func(m *pb.MetricAggregate) bool {
		if m == nil || m.GetCount() < 0 || m.GetErrors() < 0 || m.GetErrors() > m.GetCount() || len(m.GetDurationDigest().GetCentroids()) > engine.TDigestMaxCentroids || len(m.GetDurationSketch().GetBins()) > engine.DurationSketchMaxBuckets {
			return false
		}
		for _, value := range []float64{m.GetAvgMs(), m.GetP50Ms(), m.GetP90Ms(), m.GetP95Ms(), m.GetP99Ms()} {
			if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
				return false
			}
		}
		return true
	}
	if info.GetOther() != nil && !shape(info.GetOther()) {
		return false
	}
	seen := make(map[string]struct{}, len(metrics))
	for _, metric := range metrics {
		if !shape(metric) {
			return false
		}
		values, ok := engine.JointTupleValues(metric.GetName(), len(projection.Dimensions))
		if !ok {
			return false
		}
		canonical, ok := engine.JointTupleKey(values)
		if !ok || canonical != metric.GetName() {
			return false
		}
		if _, duplicate := seen[canonical]; duplicate {
			return false
		}
		seen[canonical] = struct{}{}
	}
	// Include repeated-field tags and length prefixes, not just message bodies.
	return proto.Size(&pb.MetricsBatch{JointMetricsInfo: info, JointMetricsV4: metrics}) <= budget
}
