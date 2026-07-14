package api

import (
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/jasonczech/apexload/pkg/engine"
)

const maxGroupMetricsHistoryCacheEntries = 32

var groupMetricsHistoryCaches sync.Map // map[testID]*groupMetricsHistoryCache

type groupFilterQuery struct {
	Names    []string
	Explicit bool
}

type groupMetricsHistoryCache struct {
	mu      sync.Mutex
	entries map[string][]*LiveMetrics
}

func parseGroupFilterQuery(query url.Values) groupFilterQuery {
	values, ok := query["groups"]
	if !ok {
		return groupFilterQuery{}
	}
	seen := make(map[string]struct{})
	names := make([]string, 0)
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			name := strings.TrimSpace(part)
			if name == "" {
				continue
			}
			if _, exists := seen[name]; exists {
				continue
			}
			seen[name] = struct{}{}
			names = append(names, name)
		}
	}
	// A present-but-blank value (e.g. "groups=" or "groups=,") carries no group
	// names and means "no filter / all groups" — NOT "filter to nothing".
	// Treating it as an explicit empty filter would zero out all live metrics
	// and make a healthy running test look dead. Only mark the filter explicit
	// when at least one real group name was provided.
	return groupFilterQuery{Names: names, Explicit: len(names) > 0}
}

func selectedGroupNameSet(names []string) map[string]struct{} {
	set := make(map[string]struct{}, len(names))
	for _, name := range names {
		trimmed := strings.TrimSpace(name)
		if trimmed != "" {
			set[trimmed] = struct{}{}
		}
	}
	return set
}

func filterLiveMetricsForGroups(live *LiveMetrics, filter groupFilterQuery) *LiveMetrics {
	if live == nil || !filter.Explicit {
		return live
	}

	out := *live
	out.GroupMetrics = make(map[string]*GroupLiveMetrics)
	if len(filter.Names) == 0 {
		clearLivePerformanceMetrics(&out)
		return &out
	}

	selected := make([]*GroupLiveMetrics, 0, len(filter.Names))
	for _, name := range filter.Names {
		groupMetrics := live.GroupMetrics[name]
		if groupMetrics == nil {
			continue
		}
		out.GroupMetrics[name] = cloneGroupLiveMetrics(groupMetrics)
		selected = append(selected, groupMetrics)
	}
	applySelectedGroupAggregate(&out, selected)
	return &out
}

func filterLiveMetricsHistoryForGroups(history []*LiveMetrics, filter groupFilterQuery) []*LiveMetrics {
	if len(history) == 0 {
		return nil
	}
	if !filter.Explicit {
		return history
	}
	filtered := make([]*LiveMetrics, 0, len(history))
	for _, point := range history {
		filtered = append(filtered, filterLiveMetricsForGroups(point, filter))
	}
	return filtered
}

func filterLiveMetricsHistoryForGroupsCached(testID string, history []*LiveMetrics, filter groupFilterQuery) []*LiveMetrics {
	if len(history) == 0 {
		return nil
	}
	if !filter.Explicit {
		return history
	}
	cache := getGroupMetricsHistoryCache(testID)
	key := groupMetricsHistoryCacheKey(history, filter)
	cache.mu.Lock()
	if cached := cache.entries[key]; cached != nil {
		out := append([]*LiveMetrics(nil), cached...)
		cache.mu.Unlock()
		return out
	}
	cache.mu.Unlock()

	filtered := filterLiveMetricsHistoryForGroups(history, filter)

	cache.mu.Lock()
	if len(cache.entries) >= maxGroupMetricsHistoryCacheEntries {
		cache.entries = make(map[string][]*LiveMetrics)
	}
	cache.entries[key] = append([]*LiveMetrics(nil), filtered...)
	cache.mu.Unlock()
	return filtered
}

func getGroupMetricsHistoryCache(testID string) *groupMetricsHistoryCache {
	if testID == "" {
		testID = "unknown"
	}
	if value, ok := groupMetricsHistoryCaches.Load(testID); ok {
		return value.(*groupMetricsHistoryCache)
	}
	cache := &groupMetricsHistoryCache{entries: make(map[string][]*LiveMetrics)}
	actual, _ := groupMetricsHistoryCaches.LoadOrStore(testID, cache)
	return actual.(*groupMetricsHistoryCache)
}

func deleteGroupMetricsHistoryCache(testID string) {
	groupMetricsHistoryCaches.Delete(testID)
}

func groupMetricsHistoryCacheKey(history []*LiveMetrics, filter groupFilterQuery) string {
	names := append([]string(nil), filter.Names...)
	sort.Strings(names)
	firstUnixNano := int64(0)
	lastUnixNano := int64(0)
	if len(history) > 0 {
		firstUnixNano = history[0].Timestamp.UnixNano()
		lastUnixNano = history[len(history)-1].Timestamp.UnixNano()
	}
	var builder strings.Builder
	builder.WriteString(strconv.Itoa(len(history)))
	builder.WriteByte('|')
	builder.WriteString(strconv.FormatInt(firstUnixNano, 10))
	builder.WriteByte('|')
	builder.WriteString(strconv.FormatInt(lastUnixNano, 10))
	builder.WriteByte('|')
	builder.WriteString(strings.Join(names, "\x00"))
	return builder.String()
}

func cloneGroupLiveMetrics(metrics *GroupLiveMetrics) *GroupLiveMetrics {
	if metrics == nil {
		return nil
	}
	clone := *metrics
	if metrics.DurationDigest != nil {
		digest := *metrics.DurationDigest
		digest.Centroids = append([]engine.TDigestCentroidSnapshot(nil), metrics.DurationDigest.Centroids...)
		clone.DurationDigest = &digest
	}
	return &clone
}

func clearLivePerformanceMetrics(live *LiveMetrics) {
	live.ActiveVUsers = 0
	live.TPS = 0
	live.TargetTPS = 0
	live.ThroughputGapPct = 0
	live.ThroughputTargetMissed = false
	live.AvgResponseMs = 0
	live.ErrorPct = 0
	live.TotalTx = 0
	live.TotalErrors = 0
	live.P50Ms = 0
	live.P90Ms = 0
	live.P95Ms = 0
	live.P99Ms = 0
}

func applySelectedGroupAggregate(live *LiveMetrics, selected []*GroupLiveMetrics) {
	clearLivePerformanceMetrics(live)
	if len(selected) == 0 {
		return
	}

	var attempts int64
	var weightedDuration float64
	mergedDigest := engine.NewTDigest(200)
	digestCount := int64(0)
	weightedPercentiles := map[string]float64{
		"p50": 0,
		"p90": 0,
		"p95": 0,
		"p99": 0,
	}
	percentileWeight := int64(0)

	for _, group := range selected {
		if group == nil {
			continue
		}
		groupAttempts := group.Transactions + group.Errors
		live.ActiveVUsers += int64(group.VUsers)
		live.TPS += group.TPS
		live.TargetTPS += group.TargetTPS
		live.TotalTx += group.Transactions
		live.TotalErrors += group.Errors
		if groupAttempts > 0 {
			attempts += groupAttempts
			weightedDuration += group.AvgResponseMs * float64(groupAttempts)
			weightedPercentiles["p50"] += group.P50Ms * float64(groupAttempts)
			weightedPercentiles["p90"] += group.P90Ms * float64(groupAttempts)
			weightedPercentiles["p95"] += group.P95Ms * float64(groupAttempts)
			weightedPercentiles["p99"] += group.P99Ms * float64(groupAttempts)
			percentileWeight += groupAttempts
		}
		if group.DurationDigest != nil && group.DurationDigest.Count > 0 {
			mergedDigest.Merge(engine.NewTDigestFromSnapshot(*group.DurationDigest))
			digestCount += group.DurationDigest.Count
		}
		if group.ThroughputTargetMissed {
			live.ThroughputTargetMissed = true
		}
	}

	if attempts > 0 {
		live.AvgResponseMs = weightedDuration / float64(attempts)
		live.ErrorPct = float64(live.TotalErrors) / float64(attempts) * 100
	}
	if digestCount > 0 {
		live.P50Ms = mergedDigest.Quantile(0.50) / 1_000_000
		live.P90Ms = mergedDigest.Quantile(0.90) / 1_000_000
		live.P95Ms = mergedDigest.Quantile(0.95) / 1_000_000
		live.P99Ms = mergedDigest.Quantile(0.99) / 1_000_000
	} else if percentileWeight > 0 {
		live.P50Ms = weightedPercentiles["p50"] / float64(percentileWeight)
		live.P90Ms = weightedPercentiles["p90"] / float64(percentileWeight)
		live.P95Ms = weightedPercentiles["p95"] / float64(percentileWeight)
		live.P99Ms = weightedPercentiles["p99"] / float64(percentileWeight)
	}
	if live.TargetTPS > 0 && live.TPS < live.TargetTPS {
		live.ThroughputGapPct = ((live.TargetTPS - live.TPS) / live.TargetTPS) * 100
		live.ThroughputTargetMissed = live.ThroughputTargetMissed || live.ThroughputGapPct >= 5.0
	}
}

func sortedGroupFilterNames(set map[string]struct{}) []string {
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
