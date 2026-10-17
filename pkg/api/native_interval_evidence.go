package api

import (
	"encoding/json"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/engine"
	"sort"
	"time"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/storage"
)

func nativeIntervalEvidence(live *LiveMetrics, actions map[string]*TxMetrics, actionsSupported bool, window float64) *storage.NativeIntervalEvidence {
	out := &storage.NativeIntervalEvidence{Version: 1, WindowSeconds: window, Dimensions: map[string]map[string]storage.NativeIntervalMetric{}}
	dimensions := map[string]map[string]*TxMetrics{"transaction": live.Transactions, "property": live.Properties, "region": live.ByRegion, "dependency": live.DependencyHealth, "network_profile": live.NetworkProfiles}
	if actionsSupported {
		dimensions["action"] = actions
	}
	// Each dimension has its own deterministic cap; truncation is explicit and
	// query handlers reject incomplete selections rather than graphing partial totals.
	for dimension, metrics := range dimensions {
		keys := make([]string, 0, len(metrics))
		for key := range metrics {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		values := map[string]storage.NativeIntervalMetric{}
		for i, key := range keys {
			if i >= 128 {
				out.Incomplete = true
				break
			}
			m := metrics[key]
			if m == nil {
				continue
			}
			if len(key) > 256 {
				out.Incomplete = true
				continue
			}
			values[key] = storage.NativeIntervalMetric{DurationDigest: engine.CloneTDigestSnapshot(m.DurationDigest), DurationSketch: engine.CloneDurationSketchSnapshot(m.DurationSketch), Count: m.Count, Errors: m.Errors, Avg: m.AvgMs, P50: m.P50Ms, P90: m.P90Ms, P95: m.P95Ms, P99: m.P99Ms}
		}
		out.Dimensions[dimension] = values
	}
	if telemetryWindowLost(live.Telemetry) {
		out.Incomplete = true
	}
	return out
}

// telemetryWindowLost reports samples lost from a window (dropped results or
// subscriber batches, missing windows): every dimension of it is partial.
func telemetryWindowLost(telemetry *TelemetryHealth) bool {
	return telemetry != nil && (telemetry.DroppedResults > 0 || telemetry.DroppedSubscriberBatches > 0 || telemetry.MissingMetricWindows > 0)
}

func addNativeJointEvidence(out *storage.NativeIntervalEvidence, joint map[string]*TxMetrics) {
	values := map[string]storage.NativeIntervalMetric{}
	keys := make([]string, 0, len(joint))
	for key := range joint {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for i, key := range keys {
		if i >= 128 || len(key) > 512 || key == "__other__" {
			out.Incomplete = true
			continue
		}
		m := joint[key]
		if m == nil {
			continue
		}
		values[key] = storage.NativeIntervalMetric{Count: m.Count, Errors: m.Errors, Avg: m.AvgMs, P50: m.P50Ms, P90: m.P90Ms, P95: m.P95Ms, P99: m.P99Ms, DurationDigest: engine.CloneTDigestSnapshot(m.DurationDigest), DurationSketch: engine.CloneDurationSketchSnapshot(m.DurationSketch)}
	}
	out.Dimensions["action_property"] = values
}

// Build a frame from the newly accepted source batch, never from the resident
// latest-per-generator fleet map used to render instantaneous live summaries.
func nativePublicationEvidence(source string, batch generatorrpc.MetricsBatch, peers map[string]generatorrpc.MetricsBatch, projection *engine.JointProjection) *storage.NativePublicationEvidence {
	live := &LiveMetrics{}
	convert := func(values []generatorrpc.MetricAggregate) map[string]*TxMetrics {
		out := map[string]*TxMetrics{}
		for _, a := range values {
			if m := txMetricsFromAggregate(a); m != nil {
				out[m.Name] = m
			}
		}
		return out
	}
	live.Transactions = convert(batch.TransactionMetrics)
	live.Properties = convert(batch.PropertyMetrics)
	live.ByRegion = convert(batch.RegionMetrics)
	live.DependencyHealth = convert(batch.DependencyMetrics)
	live.NetworkProfiles = convert(batch.NetworkProfileMetrics)
	if batch.Telemetry != nil {
		live.Telemetry = &TelemetryHealth{DroppedResults: batch.Telemetry.DroppedResults, DroppedSubscriberBatches: batch.Telemetry.DroppedSubscriberBatches, MissingMetricWindows: batch.Telemetry.MissingMetricWindows}
	}
	evidence := nativeIntervalEvidence(live, convert(batch.ActionMetrics), true, batchWindowSeconds(batch.WindowMs))
	evidence.Version = int(batch.ActionMetricsVersion)
	addNativeJointEvidence(evidence, convert(batch.ActionPropertyMetrics))
	if evidence.Version == 3 {
		addNativeDimensionEvidence(evidence, convert(batch.ActionDimensionMetrics))
	}
	published, tuples, other, omitted, incomplete := jointEvidenceFromBatch(batch)
	setNativeJointEvidence(evidence, projection, published, tuples, other, omitted, incomplete || telemetryWindowLost(live.Telemetry))
	sources := make([]string, 0, len(peers))
	for id := range peers {
		sources = append(sources, id)
	}
	sort.Strings(sources)
	return &storage.NativePublicationEvidence{SourceID: source, SourceEpoch: batch.SourceEpoch, Sequence: batch.BatchSequence, ExecutionGeneration: batch.ExecutionGeneration, Timestamp: time.UnixMilli(batch.TimestampUnixMs).UTC(), Version: evidence.Version, WindowSeconds: evidence.WindowSeconds, Dimensions: evidence.Dimensions, Incomplete: evidence.Incomplete, ExpectedSources: sources, Joint: evidence.Joint}
}

func addNativeDimensionEvidence(out *storage.NativeIntervalEvidence, metrics map[string]*TxMetrics) {
	joint := &storage.NativeIntervalEvidence{Dimensions: map[string]map[string]storage.NativeIntervalMetric{}}
	addNativeJointEvidence(joint, metrics)
	out.Incomplete = out.Incomplete || joint.Incomplete
	values := joint.Dimensions["action_property"]
	for key := range values {
		var labels []string
		if json.Unmarshal([]byte(key), &labels) != nil || len(labels) != 4 {
			delete(values, key)
			out.Incomplete = true
			continue
		}
		for _, label := range labels {
			if len(label) > 256 || !utf8.ValidString(label) {
				delete(values, key)
				out.Incomplete = true
				break
			}
		}
	}
	out.Dimensions["action_dimensions"] = values
}
