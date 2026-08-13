package api

import (
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/scrub"
)

const (
	maxIntervalSummaryTransactions     = 512
	intervalSummaryTransactionOverflow = "__other__"
)

type intervalTransactionAccumulator struct {
	count           int64
	errors          int64
	totalDurationMs float64
	// sourceTPS is the additive successful transaction rate already normalized
	// by each contributing generator's own effective publication window.
	// sourceWindows distinguishes an exact zero rate (for an errors-only
	// transaction) from accumulators that need the caller's fallback window.
	sourceTPS     float64
	sourceWindows int64
}

func intervalTransactionName(name string) string {
	if name = strings.TrimSpace(scrub.ValueLabel(name, scrub.Policy{})); name != "" {
		return name
	}
	return "unnamed"
}

func intervalTransactionsFromLive(metrics map[string]*TxMetrics) map[string]*IntervalTransactionMetrics {
	if len(metrics) == 0 {
		return nil
	}
	result := make(map[string]*IntervalTransactionMetrics, len(metrics))
	for fallbackName, metric := range metrics {
		if metric == nil {
			continue
		}
		name := strings.TrimSpace(metric.Name)
		if name == "" {
			name = intervalTransactionName(fallbackName)
		}
		mergeIntervalTransactionMetric(result, name, &IntervalTransactionMetrics{
			Name:   name,
			TPS:    metric.TPS,
			Count:  metric.Count,
			Errors: metric.Errors,
			AvgMs:  metric.AvgMs,
		})
	}
	return compactIntervalTransactions(result)
}

func intervalTransactionsFromAccumulators(metrics map[string]*intervalTransactionAccumulator, windowSeconds float64) map[string]*IntervalTransactionMetrics {
	if len(metrics) == 0 {
		return nil
	}
	result := make(map[string]*IntervalTransactionMetrics, len(metrics))
	for name, metric := range metrics {
		if metric == nil {
			continue
		}
		attempts := metric.count
		successes := attempts - metric.errors
		if successes < 0 {
			successes = 0
		}
		item := &IntervalTransactionMetrics{
			Name:   intervalTransactionName(name),
			Count:  attempts,
			Errors: metric.errors,
		}
		if attempts > 0 {
			item.AvgMs = metric.totalDurationMs / float64(attempts)
		}
		if metric.sourceWindows > 0 {
			item.TPS = metric.sourceTPS
		} else if windowSeconds > 0 {
			item.TPS = float64(successes) / windowSeconds
		}
		mergeIntervalTransactionMetric(result, item.Name, item)
	}
	return compactIntervalTransactions(result)
}

// mergeIntervalTransactions intentionally does not compact while merging.
// Callers combine a complete interval first and compact once so the retained
// names do not depend on generator or map iteration order.
func mergeIntervalTransactions(destination, source map[string]*IntervalTransactionMetrics) map[string]*IntervalTransactionMetrics {
	if len(source) == 0 {
		return destination
	}
	if destination == nil {
		destination = make(map[string]*IntervalTransactionMetrics, len(source))
	}
	for fallbackName, metric := range source {
		if metric == nil {
			continue
		}
		name := strings.TrimSpace(metric.Name)
		if name == "" {
			name = intervalTransactionName(fallbackName)
		}
		mergeIntervalTransactionMetric(destination, name, metric)
	}
	return destination
}

func mergeIntervalTransactionMetric(destination map[string]*IntervalTransactionMetrics, name string, incoming *IntervalTransactionMetrics) {
	if destination == nil || incoming == nil {
		return
	}
	name = intervalTransactionName(name)
	current := destination[name]
	if current == nil {
		copy := *incoming
		copy.Name = name
		destination[name] = &copy
		return
	}
	combinedCount := current.Count + incoming.Count
	if combinedCount > 0 {
		current.AvgMs = (current.AvgMs*float64(current.Count) + incoming.AvgMs*float64(incoming.Count)) / float64(combinedCount)
	}
	current.Name = name
	current.TPS += incoming.TPS
	current.Count = combinedCount
	current.Errors += incoming.Errors
}

func compactIntervalTransactions(metrics map[string]*IntervalTransactionMetrics) map[string]*IntervalTransactionMetrics {
	if len(metrics) == 0 {
		return nil
	}
	keys := make([]string, 0, len(metrics))
	for name, metric := range metrics {
		if metric != nil && name != intervalSummaryTransactionOverflow {
			keys = append(keys, name)
		}
	}
	sort.Strings(keys)
	overflow := metrics[intervalSummaryTransactionOverflow]
	entryCount := len(keys)
	if overflow != nil {
		entryCount++
	}
	if entryCount <= maxIntervalSummaryTransactions {
		if overflow != nil {
			overflow.Name = intervalSummaryTransactionOverflow
		}
		return metrics
	}

	compacted := make(map[string]*IntervalTransactionMetrics, maxIntervalSummaryTransactions)
	keep := maxIntervalSummaryTransactions - 1
	for index, name := range keys {
		metric := metrics[name]
		if index < keep {
			copy := *metric
			copy.Name = name
			compacted[name] = &copy
			continue
		}
		mergeIntervalTransactionMetric(compacted, intervalSummaryTransactionOverflow, metric)
	}
	if overflow != nil {
		mergeIntervalTransactionMetric(compacted, intervalSummaryTransactionOverflow, overflow)
	}
	return compacted
}
