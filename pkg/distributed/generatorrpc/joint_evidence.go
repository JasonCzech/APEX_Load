package generatorrpc

import (
	"errors"
	"slices"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/engine"
)

// FeatureJointEvidenceV4 counts samples under a run's frozen joint
// evidence projection and publishes the tuples (joint_metrics_v4). The
// controller sends a projection only to peers advertising it; an older peer
// leaves the joint evidence of its windows unavailable.
const FeatureJointEvidenceV4 = "native_joint_evidence_v4"

// JointMetricsInfo describes a batch's joint evidence: the projection it was
// counted under, the samples of tuples past the publication bound (Other)
// and how many distinct tuples were omitted (a lower bound).
type JointMetricsInfo struct {
	Version       uint32
	Population    string
	Dimensions    []string
	Other         *MetricAggregate
	OmittedTuples int64
	Incomplete    bool
	// UnavailableSources is set by the controller's fleet merge: generators
	// whose latest window had no joint evidence. Not sent on the wire.
	UnavailableSources int
}

func jointProjectionToProto(p *engine.JointProjection) *pb.JointEvidenceProjection {
	if p == nil {
		return nil
	}
	return &pb.JointEvidenceProjection{Population: p.Population, Dimensions: append([]string(nil), p.Dimensions...)}
}

// jointProjectionFromProto validates an assignment's projection; nil means
// none was sent.
func jointProjectionFromProto(p *pb.JointEvidenceProjection) (*engine.JointProjection, error) {
	if p == nil {
		return nil, nil
	}
	projection := &engine.JointProjection{Population: p.GetPopulation(), Dimensions: append([]string(nil), p.GetDimensions()...)}
	if err := projection.Validate(); err != nil {
		return nil, err
	}
	return projection, nil
}

func jointInfoToProto(info *JointMetricsInfo) *pb.JointMetricsInfo {
	if info == nil {
		return nil
	}
	return &pb.JointMetricsInfo{Version: info.Version, Population: info.Population, Dimensions: append([]string(nil), info.Dimensions...), Other: metricAggregateToProto(info.Other), OmittedTuples: info.OmittedTuples, Incomplete: info.Incomplete}
}

func jointInfoFromProto(info *pb.JointMetricsInfo) *JointMetricsInfo {
	if info == nil {
		return nil
	}
	return &JointMetricsInfo{Version: info.GetVersion(), Population: info.GetPopulation(), Dimensions: append([]string(nil), info.GetDimensions()...), Other: metricAggregateFromProto(info.GetOther()), OmittedTuples: info.GetOmittedTuples(), Incomplete: info.GetIncomplete()}
}

// jointAggregatesFromEngine converts one engine window's joint evidence.
func jointAggregatesFromEngine(snapshot *engine.JointMetricsSnapshot) ([]MetricAggregate, *JointMetricsInfo) {
	if snapshot == nil {
		return nil, nil
	}
	info := &JointMetricsInfo{Version: uint32(snapshot.Version), Population: snapshot.Population, Dimensions: append([]string(nil), snapshot.Dimensions...), OmittedTuples: snapshot.OmittedTuples, Incomplete: snapshot.Incomplete}
	if snapshot.Other != nil && snapshot.Other.Count > 0 {
		other := jointAggregateFromEngine("other", snapshot.Other)
		info.Other = &other
	}
	names := make([]string, 0, len(snapshot.Tuples))
	for name, metrics := range snapshot.Tuples {
		if metrics != nil && metrics.Count > 0 {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	out := make([]MetricAggregate, 0, len(names))
	for _, name := range names {
		out = append(out, jointAggregateFromEngine(name, snapshot.Tuples[name]))
	}
	return out, info
}

// jointAggregateFromEngine is one tuple as joint evidence publishes it: its
// counts, latency summary and distributions, and nothing else. A tuple's
// encoded size is then bounded by its key and the distribution limits, so the
// largest publication a generator can build stays within the controller's
// joint envelope (maxJointEncodedBytes).
func jointAggregateFromEngine(name string, metrics *engine.ActionMetrics) MetricAggregate {
	return MetricAggregate{
		Name:           name,
		Count:          metrics.Count,
		Errors:         metrics.Errors,
		AvgMs:          float64(metrics.AvgDuration.Microseconds()) / 1000.0,
		P50Ms:          float64(metrics.P50.Microseconds()) / 1000.0,
		P90Ms:          float64(metrics.P90.Microseconds()) / 1000.0,
		P95Ms:          float64(metrics.P95.Microseconds()) / 1000.0,
		P99Ms:          float64(metrics.P99.Microseconds()) / 1000.0,
		DurationDigest: engine.CloneTDigestSnapshot(metrics.DurationDigest),
		DurationSketch: engine.CloneDurationSketchSnapshot(metrics.DurationSketch),
	}
}

// mergeJointEvidence merges the joint evidence of a generator's groups:
// tuples are summed by key (an exact intersection stays exact), the result
// is bounded again to JointMaxKeys with the rest folded into Other, and
// parts counted under a different projection make the batch's joint
// evidence unavailable rather than mixed.
func mergeJointEvidence(parts [][]MetricAggregate, infos []*JointMetricsInfo) ([]MetricAggregate, *JointMetricsInfo, error) {
	var merged *JointMetricsInfo
	var series []MetricAggregate
	var others []MetricAggregate
	for i, info := range infos {
		if info == nil {
			continue
		}
		if merged == nil {
			merged = &JointMetricsInfo{Version: info.Version, Population: info.Population, Dimensions: append([]string(nil), info.Dimensions...)}
		} else if info.Version != merged.Version || info.Population != merged.Population || !slices.Equal(info.Dimensions, merged.Dimensions) {
			return nil, nil, errors.New("groups published joint evidence under different projections")
		}
		merged.Incomplete = merged.Incomplete || info.Incomplete
		merged.OmittedTuples = max(merged.OmittedTuples, info.OmittedTuples)
		if info.Other != nil {
			others = append(others, *info.Other)
		}
		series = append(series, parts[i]...)
	}
	if merged == nil {
		return nil, nil, nil
	}
	return boundJointEvidence(mergeJointSeries(series), merged, others), merged, nil
}

// mergeJointSeries merges aggregates by tuple key with no series cap (the
// generic series merge folds names past 256 alphabetically, which would
// drop tuples without disclosure); boundJointEvidence applies the bound.
func mergeJointSeries(values []MetricAggregate) []MetricAggregate {
	byKey := map[string][]MetricAggregate{}
	keys := []string{}
	for _, value := range values {
		if value.Name == "" || value.Count <= 0 {
			continue
		}
		if _, seen := byKey[value.Name]; !seen {
			keys = append(keys, value.Name)
		}
		byKey[value.Name] = append(byKey[value.Name], value)
	}
	slices.Sort(keys)
	out := make([]MetricAggregate, 0, len(keys))
	for _, key := range keys {
		if merged := mergeMetricAggregates(byKey[key]); merged != nil {
			merged.Name = key
			out = append(out, *merged)
		}
	}
	return out
}

// boundJointEvidence keeps the JointMaxKeys tuples with the most samples
// (ties by key, so every peer keeps the same ones) and folds the rest into
// Other.
func boundJointEvidence(series []MetricAggregate, info *JointMetricsInfo, others []MetricAggregate) []MetricAggregate {
	if len(series) > engine.JointMaxKeys {
		counts := make(map[string]int64, len(series))
		byKey := make(map[string]MetricAggregate, len(series))
		for _, aggregate := range series {
			counts[aggregate.Name] = aggregate.Count
			byKey[aggregate.Name] = aggregate
		}
		keys := engine.SortedJointKeys(counts)
		kept := make([]MetricAggregate, 0, engine.JointMaxKeys)
		for i, key := range keys {
			if i < engine.JointMaxKeys {
				kept = append(kept, byKey[key])
				continue
			}
			others = append(others, byKey[key])
		}
		info.OmittedTuples += int64(len(keys) - engine.JointMaxKeys)
		info.Incomplete = true
		series = kept
		slices.SortFunc(series, func(a, b MetricAggregate) int {
			switch {
			case a.Name < b.Name:
				return -1
			case a.Name > b.Name:
				return 1
			}
			return 0
		})
	}
	if len(others) > 0 {
		if other := mergeMetricAggregates(others); other != nil {
			other.Name = "other"
			info.Other = other
		}
	}
	return series
}

// MergeJointEvidence merges several generators' joint evidence for one
// fleet window, as mergeJointEvidence does for one generator's groups.
func MergeJointEvidence(parts [][]MetricAggregate, infos []*JointMetricsInfo) ([]MetricAggregate, *JointMetricsInfo, error) {
	return mergeJointEvidence(parts, infos)
}
