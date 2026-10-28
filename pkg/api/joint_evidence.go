package api

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"sort"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
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

// validateJointEvidenceCategories refuses a projected property category that
// none of the run's groups promotes. A run counts property values only for
// the finite categories its scripts promote (results.property_analytics,
// after runtime overrides) and the engine's own cold/warm browser iteration
// category; any other would only ever read as the empty label, so only these
// bounded categories are admitted.
func validateJointEvidenceCategories(projection *engine.JointProjection, groups []TestGroup) error {
	if projection == nil {
		return nil
	}
	for _, dimension := range projection.Dimensions {
		name, ok := engine.JointPropertyCategory(dimension)
		if !ok || name == protocol.BrowserIterationDimension || jointEvidenceCategoryPromoted(groups, name) {
			continue
		}
		return fmt.Errorf("joint_evidence: property category %q is not promoted by any of the run's scripts (results.property_analytics)", name)
	}
	return nil
}

func jointEvidenceCategoryPromoted(groups []TestGroup, name string) bool {
	for _, group := range groups {
		if group.script == nil {
			continue
		}
		for _, rule := range group.script.Config.Results.PropertyAnalytics {
			if rule.Name == name {
				return true
			}
		}
	}
	return false
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

// generatorAcceptsJointEvidence reports whether a generator's probed
// features include joint evidence v4. Only such a generator is sent the
// projection, at Start or as an evacuation replacement; another leaves its
// windows' joint evidence unavailable.
func generatorAcceptsJointEvidence(features []string) bool {
	return stringSliceContainsEqualFold(features, generatorrpc.FeatureJointEvidenceV4)
}

// Joint evidence unavailability reasons.
const (
	jointReasonSourceLacksCapability = "source_lacks_joint_evidence_v4"
	jointReasonProjectionMismatch    = "projection_mismatch"
	jointReasonInvalid               = "invalid_joint_evidence"
)

func nativeJointMetric(m *TxMetrics) storage.NativeIntervalMetric {
	return storage.NativeIntervalMetric{DurationDigest: engine.CloneTDigestSnapshot(m.DurationDigest), DurationSketch: engine.CloneDurationSketchSnapshot(m.DurationSketch), Count: m.Count, Errors: m.Errors, Avg: m.AvgMs, P50: m.P50Ms, P90: m.P90Ms, P95: m.P95Ms, P99: m.P99Ms}
}

// setNativeJointEvidence records a source's joint evidence on interval
// evidence: its validated tuples under storage.NativeJointDimension and the
// description. projection is the run's; a source that published none while
// the run has one is unavailable, as is one counted under another
// projection. Tuples that do not decode to the projection are dropped and
// make the evidence incomplete. incomplete also carries the window's sample
// loss; the marginal dimensions' key caps never make joint evidence partial.
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

// The source batches whose joint evidence waits for the next published point
// are bounded by count and by their estimated in-memory size; the oldest are
// dropped (and counted) first.
const maxPendingJointPublications = 1024

// maxPendingJointBytes is a variable so tests can bound by size.
var maxPendingJointBytes int64 = 64 << 20

// jointPublicationBytes estimates a pending publication's in-memory size:
// its tuple keys, metrics, digest centroids and sketch buckets.
func jointPublicationBytes(p *storage.NativePublicationEvidence) int64 {
	if p == nil {
		return 0
	}
	distributionBytes := func(metric storage.NativeIntervalMetric) int64 {
		size := int64(0)
		if metric.DurationDigest != nil {
			size += int64(len(metric.DurationDigest.Centroids)) * 16
		}
		if metric.DurationSketch != nil {
			size += int64(len(metric.DurationSketch.Bins)) * 16
		}
		return size
	}
	size := int64(256 + len(p.SourceID) + len(p.SourceEpoch))
	for key, metric := range p.Dimensions[storage.NativeJointDimension] {
		size += int64(len(key)) + 160 + distributionBytes(metric)
	}
	if p.Joint != nil && p.Joint.Other != nil {
		size += distributionBytes(*p.Joint.Other)
	}
	return size
}

// nativePublicationIdentity identifies one source window across retained
// and persisted evidence.
func nativePublicationIdentity(p *storage.NativePublicationEvidence) string {
	return fmt.Sprintf("%s/%s/%d/%d", p.SourceID, p.SourceEpoch, p.ExecutionGeneration, p.Sequence)
}

// jointPublicationEvidence is one accepted source batch's joint evidence
// alone, kept until the next published point carries it.
func jointPublicationEvidence(source string, batch generatorrpc.MetricsBatch, projection *engine.JointProjection) *storage.NativePublicationEvidence {
	evidence := &storage.NativeIntervalEvidence{Dimensions: map[string]map[string]storage.NativeIntervalMetric{}}
	published, tuples, other, omitted, incomplete := jointEvidenceFromBatch(batch)
	lost := batch.Telemetry != nil && (batch.Telemetry.DroppedResults > 0 || batch.Telemetry.DroppedSubscriberBatches > 0 || batch.Telemetry.MissingMetricWindows > 0)
	setNativeJointEvidence(evidence, projection, published, tuples, other, omitted, incomplete || lost)
	dimensions := map[string]map[string]storage.NativeIntervalMetric{}
	if values, ok := evidence.Dimensions[storage.NativeJointDimension]; ok {
		dimensions[storage.NativeJointDimension] = values
	}
	return &storage.NativePublicationEvidence{SourceID: source, SourceEpoch: batch.SourceEpoch, Sequence: batch.BatchSequence, ExecutionGeneration: batch.ExecutionGeneration,
		Timestamp: time.UnixMilli(batch.TimestampUnixMs).UTC(), Version: int(batch.ActionMetricsVersion), WindowSeconds: batchWindowSeconds(batch.WindowMs), Dimensions: dimensions, Joint: evidence.Joint}
}

func jointPublicationWindowBounds(p *storage.NativePublicationEvidence) (int64, int64) {
	if p == nil || p.Timestamp.IsZero() {
		return 0, 0
	}
	endMs := p.Timestamp.UnixMilli()
	if endMs < 0 {
		return 0, 0
	}
	windowMs := int64(math.Round(p.WindowSeconds * 1000))
	if windowMs < 0 {
		windowMs = 0
	}
	startMs := endMs - windowMs
	if startMs < 0 {
		startMs = 0
	}
	return startMs, endMs
}

// rememberJointPublicationLocked keeps an accepted batch's joint evidence
// for the next published point; tr.mu is held.
func rememberJointPublicationLocked(test *ManagedTest, source string, batch generatorrpc.MetricsBatch) {
	projection := runJointProjection(test)
	if projection == nil || batch.BatchSequence == 0 {
		return
	}
	p := jointPublicationEvidence(source, batch, projection)
	startMs, endMs := jointPublicationWindowBounds(p)
	size := jointPublicationBytes(p)
	// One publication must fit the same budget as the entire pending queue.
	// Refuse it before evicting useful earlier windows; the existing dropped
	// count keeps its absence visible in the joint archive coverage.
	if size > maxPendingJointBytes {
		test.jointPublicationsDropped++
		telemetrySourceLedgerLocked(test).observeJointDropped(source, batch, startMs, endMs)
		return
	}
	for len(test.pendingJointPublications) > 0 && (len(test.pendingJointPublications) >= maxPendingJointPublications || test.pendingJointBytes+size > maxPendingJointBytes) {
		evicted := test.pendingJointPublications[0]
		test.pendingJointBytes -= jointPublicationBytes(evicted)
		test.pendingJointPublications[0] = nil
		test.pendingJointPublications = test.pendingJointPublications[1:]
		test.jointPublicationsDropped++
		if evicted != nil {
			evStart, evEnd := jointPublicationWindowBounds(evicted)
			telemetrySourceLedgerLocked(test).observeJointDropped(evicted.SourceID, generatorrpc.MetricsBatch{
				ExecutionGeneration: evicted.ExecutionGeneration,
				SourceEpoch:         evicted.SourceEpoch,
				BatchSequence:       evicted.Sequence,
			}, evStart, evEnd)
		}
	}
	test.pendingJointPublications = append(test.pendingJointPublications, p)
	test.pendingJointBytes += size
	telemetrySourceLedgerLocked(test).observeJointAccepted(source, batch, startMs, endMs)
}

// releaseHeldJointPublicationsLocked drops the batches held for the run's
// joint sidecar once no point of this controller will carry them (its archive
// was committed, will not be, or another controller holds the run), counting
// them as dropped so a sidecar written without them is marked truncated;
// tr.mu is held.
func releaseHeldJointPublicationsLocked(test *ManagedTest) {
	for _, p := range test.pendingJointPublications {
		if p != nil {
			startMs, endMs := jointPublicationWindowBounds(p)
			telemetrySourceLedgerLocked(test).observeJointDropped(p.SourceID, generatorrpc.MetricsBatch{
				ExecutionGeneration: p.ExecutionGeneration,
				SourceEpoch:         p.SourceEpoch,
				BatchSequence:       p.Sequence,
			}, startMs, endMs)
		}
	}
	test.jointPublicationsDropped += int64(len(test.pendingJointPublications))
	test.pendingJointPublications, test.pendingJointBytes = nil, 0
}

// takePendingJointPublicationsLocked hands the pending joint evidence to the
// point being installed (tr.mu is held), leaving out the published batch
// itself, which the point carries in full.
func takePendingJointPublicationsLocked(test *ManagedTest, published *storage.NativePublicationEvidence) []*storage.NativePublicationEvidence {
	pending := test.pendingJointPublications
	test.pendingJointPublications, test.pendingJointBytes = nil, 0
	out := make([]*storage.NativePublicationEvidence, 0, len(pending))
	for _, p := range pending {
		if published != nil {
			if nativePublicationIdentity(p) == nativePublicationIdentity(published) {
				continue
			}
			p.ExpectedSources = append([]string(nil), published.ExpectedSources...)
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
