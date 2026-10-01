package api

import (
	"sort"

	"github.com/jasonczech/apexload/pkg/storage"
)

func nativeIntervalEvidence(live *LiveMetrics, actions map[string]*TxMetrics, actionsSupported bool, window float64) *storage.NativeIntervalEvidence {
	out := &storage.NativeIntervalEvidence{Version: 1, WindowSeconds: window, Dimensions: map[string]map[string]storage.NativeIntervalMetric{}}
	dimensions := map[string]map[string]*TxMetrics{"transaction": live.Transactions, "property": live.Properties, "region": live.ByRegion, "dependency": live.DependencyHealth}
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
			values[key] = storage.NativeIntervalMetric{Count: m.Count, Errors: m.Errors, Avg: m.AvgMs, P50: m.P50Ms, P90: m.P90Ms, P95: m.P95Ms, P99: m.P99Ms}
		}
		out.Dimensions[dimension] = values
	}
	if live.Telemetry != nil && (live.Telemetry.DroppedResults > 0 || live.Telemetry.DroppedSubscriberBatches > 0 || live.Telemetry.MissingMetricWindows > 0) {
		out.Incomplete = true
	}
	return out
}
