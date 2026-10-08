package api

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Joint evidence v4 (B5): a run's capture projection. It is chosen before
// Start (joint_evidence on test creation), stored with the test, frozen
// into the run's provenance at Start, sent to native_joint_evidence_v4
// generators and applied to a local run's engine. Generators keep only the
// projected tuples; peers without the capability leave the joint evidence
// of their windows unavailable.

// normalizeJointEvidence validates a requested projection.
func normalizeJointEvidence(projection *engine.JointProjection) (*engine.JointProjection, error) {
	if projection == nil {
		return nil, nil
	}
	if err := projection.Validate(); err != nil {
		return nil, fmt.Errorf("joint_evidence: %w", err)
	}
	return projection.Clone(), nil
}

func jointEvidenceForStorage(projection *engine.JointProjection) any {
	if projection == nil {
		return nil
	}
	return projection.Clone()
}

// decodeStoredJointEvidence reads a stored projection; one that no longer
// validates is dropped rather than applied.
func decodeStoredJointEvidence(raw any) *engine.JointProjection {
	if raw == nil {
		return nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var projection engine.JointProjection
	if err := json.Unmarshal(encoded, &projection); err != nil || projection.Validate() != nil {
		return nil
	}
	return &projection
}

// runJointProjection is the projection a run executes under: the one
// frozen in its provenance at Start, else the test's own.
func runJointProjection(test *ManagedTest) *engine.JointProjection {
	if test == nil {
		return nil
	}
	if test.initialArchiveProvenance != nil && test.initialArchiveProvenance.JointEvidence != nil {
		return test.initialArchiveProvenance.JointEvidence.Clone()
	}
	return test.JointEvidence.Clone()
}

// Joint evidence unavailability reasons.
const (
	jointReasonSourceLacksCapability = "source_lacks_joint_evidence_v4"
	jointReasonProjectionMismatch    = "projection_mismatch"
	jointReasonInvalid               = "invalid_joint_evidence"
)

func nativeJointMetric(m *TxMetrics) storage.NativeIntervalMetric {
	return storage.NativeIntervalMetric{DurationDigest: engine.CloneTDigestSnapshot(m.DurationDigest), Count: m.Count, Errors: m.Errors, Avg: m.AvgMs, P50: m.P50Ms, P90: m.P90Ms, P95: m.P95Ms, P99: m.P99Ms}
}

// setNativeJointEvidence records a source's joint evidence on interval
// evidence: its validated tuples under storage.NativeJointDimension and the
// description. projection is the run's; a source that published none while
// the run has one is unavailable, as is one counted under another
// projection. Tuples that do not decode to the projection are dropped and
// make the evidence incomplete.
func setNativeJointEvidence(out *storage.NativeIntervalEvidence, projection *engine.JointProjection, published *engine.JointProjection, tuples map[string]*TxMetrics, other *TxMetrics, omitted int64, incomplete bool) {
	if out == nil || projection == nil && published == nil {
		return
	}
	reference := projection
	if reference == nil {
		reference = published
	}
	joint := &storage.NativeJointEvidence{Population: reference.Population, Dimensions: append([]string(nil), reference.Dimensions...), Availability: "unavailable"}
	out.Joint = joint
	switch {
	case published == nil:
		joint.Reason = jointReasonSourceLacksCapability
		return
	case published.Validate() != nil:
		joint.Reason = jointReasonInvalid
		return
	case projection != nil && (published.Population != projection.Population || !slices.Equal(published.Dimensions, projection.Dimensions)):
		joint.Reason = jointReasonProjectionMismatch
		return
	}
	joint.Availability, joint.OmittedTuples, joint.Incomplete = "measured", omitted, incomplete
	values := map[string]storage.NativeIntervalMetric{}
	keys := make([]string, 0, len(tuples))
	for key := range tuples {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		metric := tuples[key]
		if _, ok := engine.JointTupleValues(key, len(joint.Dimensions)); !ok || metric == nil || len(values) >= engine.JointMaxKeys {
			joint.Incomplete = true
			continue
		}
		values[key] = nativeJointMetric(metric)
	}
	if other != nil && other.Count > 0 {
		metric := nativeJointMetric(other)
		joint.Other = &metric
	}
	out.Dimensions[storage.NativeJointDimension] = values
}

// jointEvidenceFromBatch reads a source batch's joint evidence.
func jointEvidenceFromBatch(batch generatorrpc.MetricsBatch) (published *engine.JointProjection, tuples map[string]*TxMetrics, other *TxMetrics, omitted int64, incomplete bool) {
	info := batch.JointInfo
	if info == nil {
		return nil, nil, nil, 0, false
	}
	if info.Version != engine.JointEvidenceVersion {
		return &engine.JointProjection{}, nil, nil, 0, true
	}
	tuples = map[string]*TxMetrics{}
	for _, aggregate := range batch.JointMetrics {
		if m := txMetricsFromAggregate(aggregate); m != nil {
			tuples[aggregate.Name] = m
		}
	}
	if info.Other != nil {
		other = txMetricsFromAggregate(*info.Other)
	}
	return &engine.JointProjection{Population: info.Population, Dimensions: append([]string(nil), info.Dimensions...)}, tuples, other, info.OmittedTuples, info.Incomplete
}

// jointEvidenceFromEngine reads a local run window's joint evidence.
func jointEvidenceFromEngine(snapshot *engine.JointMetricsSnapshot, window time.Duration) (published *engine.JointProjection, tuples map[string]*TxMetrics, other *TxMetrics, omitted int64, incomplete bool) {
	if snapshot == nil {
		return nil, nil, nil, 0, false
	}
	tuples = map[string]*TxMetrics{}
	for key, metric := range snapshot.Tuples {
		tuples[key] = buildTxMetricsFromAction(key, metric, window)
	}
	if snapshot.Other != nil {
		other = buildTxMetricsFromAction("other", snapshot.Other, window)
	}
	return &engine.JointProjection{Population: snapshot.Population, Dimensions: append([]string(nil), snapshot.Dimensions...)}, tuples, other, snapshot.OmittedTuples, snapshot.Incomplete
}
