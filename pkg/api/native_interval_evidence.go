package api

import (
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/engine"
	"sort"
	"time"

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
			values[key] = storage.NativeIntervalMetric{DurationDigest: engine.CloneTDigestSnapshot(m.DurationDigest), Count: m.Count, Errors: m.Errors, Avg: m.AvgMs, P50: m.P50Ms, P90: m.P90Ms, P95: m.P95Ms, P99: m.P99Ms}
		}
		out.Dimensions[dimension] = values
	}
	if live.Telemetry != nil && (live.Telemetry.DroppedResults > 0 || live.Telemetry.DroppedSubscriberBatches > 0 || live.Telemetry.MissingMetricWindows > 0) {
		out.Incomplete = true
	}
	return out
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
		values[key] = storage.NativeIntervalMetric{Count: m.Count, Errors: m.Errors, Avg: m.AvgMs, P50: m.P50Ms, P90: m.P90Ms, P95: m.P95Ms, P99: m.P99Ms, DurationDigest: engine.CloneTDigestSnapshot(m.DurationDigest)}
	}
	out.Dimensions["action_property"] = values
}

// Build a frame from the newly accepted source batch, never from the resident
// latest-per-generator fleet map used to render instantaneous live summaries.
func nativePublicationEvidence(source string, batch generatorrpc.MetricsBatch, peers map[string]generatorrpc.MetricsBatch) *storage.NativePublicationEvidence {
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
	sources := make([]string, 0, len(peers))
	for id := range peers {
		sources = append(sources, id)
	}
	sort.Strings(sources)
	return &storage.NativePublicationEvidence{SourceID: source, Sequence: batch.BatchSequence, ExecutionGeneration: batch.ExecutionGeneration, Timestamp: time.UnixMilli(batch.TimestampUnixMs).UTC(), Version: evidence.Version, WindowSeconds: evidence.WindowSeconds, Dimensions: evidence.Dimensions, Incomplete: evidence.Incomplete, ExpectedSources: sources}
}
