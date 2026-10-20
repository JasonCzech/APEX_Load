package api

import (
	"container/list"
	"encoding/json"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/jasonczech/apexload/pkg/engine"
)

const maxGroupFilterNames = 64
const maxGroupFilterNameBytes = 256
const maxGroupFilterQueryBytes = 16 << 10
const maxGroupMetricsHistoryCacheEntriesPerTest = 4
const maxGroupMetricsHistoryCacheEntriesGlobal = 64
const maxGroupMetricsHistoryCacheEntryBytes = 8 << 20
const maxGroupMetricsHistoryCacheBytes = 64 << 20

var groupMetricsHistoryCaches = newGroupMetricsHistoryCacheManager()

type groupFilterQuery struct {
	Names    []string
	Explicit bool
	Invalid  bool
}

type groupMetricsHistoryCacheEntry struct {
	testID  string
	key     string
	history []*LiveMetrics
	bytes   int
}

type groupMetricsHistoryCacheManager struct {
	mu      sync.Mutex
	entries map[string]*list.Element
	perTest map[string]int
	order   *list.List
	total   int
}

func newGroupMetricsHistoryCacheManager() *groupMetricsHistoryCacheManager {
	return &groupMetricsHistoryCacheManager{
		entries: make(map[string]*list.Element),
		perTest: make(map[string]int),
		order:   list.New(),
	}
}

func parseGroupFilterQuery(query url.Values) groupFilterQuery {
	values, ok := query["groups"]
	if !ok {
		return groupFilterQuery{}
	}
	seen := make(map[string]struct{})
	names := make([]string, 0)
	totalBytes := 0
	for _, value := range values {
		totalBytes += len(value)
		if totalBytes > maxGroupFilterQueryBytes {
			return groupFilterQuery{Invalid: true}
		}
		for _, part := range strings.Split(value, ",") {
			name := strings.TrimSpace(part)
			if name == "" {
				continue
			}
			if len(name) > maxGroupFilterNameBytes {
				return groupFilterQuery{Invalid: true}
			}
			if _, exists := seen[name]; exists {
				continue
			}
			if len(names) >= maxGroupFilterNames {
				return groupFilterQuery{Invalid: true}
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
	// Messaging and native evidence are aggregated globally, with no group
	// dimension: omit them rather than return metrics from groups the caller
	// explicitly hid. Transactions keep what is attributable to the selected
	// groups (transactionsForGroups).
	out.JavaHTTP = nil // Action diagnostics have no retained group intersection.
	out.Messaging = nil
	out.NativeEvidence = nil
	// The version mix is run-wide too.
	out.HTTPVersions = nil
	selectedNames := make(map[string]struct{}, len(filter.Names))
	for _, name := range filter.Names {
		selectedNames[name] = struct{}{}
	}
	out.TransactionResponseTimeSLA = make([]TransactionResponseTimeSLAResult, 0, len(live.TransactionResponseTimeSLA))
	for _, row := range live.TransactionResponseTimeSLA {
		if _, selected := selectedNames[row.Group]; selected {
			out.TransactionResponseTimeSLA = append(out.TransactionResponseTimeSLA, row)
		}
	}
	if len(out.TransactionResponseTimeSLA) == 0 {
		out.TransactionResponseTimeSLA = nil
	}
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
	out.Transactions = transactionsForGroups(live.Transactions, selectedNames)
	out.MessagingProgress = messagingProgressForGroups(live.MessagingProgress, filter.Names)
	return &out
}

// transactionsForGroups keeps the transactions the selected groups ran,
// each with exact figures: one only selected groups ran, whole; one shared
// with other groups, the share of the one selected group that ran it. A
// transaction two selected groups share with a hidden one, or one without
// attribution (an older generator's), is left out: its figures cannot be
// split.
func transactionsForGroups(transactions map[string]*TxMetrics, selected map[string]struct{}) map[string]*TxMetrics {
	out := make(map[string]*TxMetrics)
	for name, tx := range transactions {
		if tx == nil || len(tx.Groups) == 0 {
			continue
		}
		chosen := make([]string, 0, len(tx.Groups))
		for _, group := range tx.Groups {
			if _, ok := selected[group]; ok {
				chosen = append(chosen, group)
			}
		}
		switch {
		case len(chosen) == len(tx.Groups):
			out[name] = tx
		case len(chosen) == 1 && tx.ByGroup[chosen[0]] != nil:
			share := tx.ByGroup[chosen[0]]
			out[name] = &TxMetrics{
				Name: tx.Name, DependencyType: tx.DependencyType, Groups: chosen,
				TPS: share.TPS, Count: share.Count, Errors: share.Errors,
				AvgMs: share.AvgMs, P50Ms: share.P50Ms, P90Ms: share.P90Ms, P95Ms: share.P95Ms, P99Ms: share.P99Ms,
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
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

func filterLiveMetricsHistoryForGroupsCached(test *ManagedTest, history []*LiveMetrics, filter groupFilterQuery) []*LiveMetrics {
	if len(history) == 0 {
		return nil
	}
	if !filter.Explicit {
		return history
	}
	if test == nil {
		return filterLiveMetricsHistoryForGroups(history, filter)
	}
	// Keep deletion scoped to the canonical ID, but never reuse a prior run's
	// filtered points when a replacement has identical history timestamps.
	testID := test.ID
	key := storedTestHistoryCacheKey(test) + "|" + groupMetricsHistoryCacheKey(history, filter)
	if cached, ok := groupMetricsHistoryCaches.get(testID, key); ok {
		return cached
	}

	filtered := filterLiveMetricsHistoryForGroups(history, filter)
	for _, point := range filtered {
		compactGroupFilteredMetricsForCache(point)
	}
	groupMetricsHistoryCaches.put(testID, key, filtered)
	return filtered
}

func (c *groupMetricsHistoryCacheManager) get(testID, key string) ([]*LiveMetrics, bool) {
	if c == nil {
		return nil, false
	}
	composite := groupMetricsHistoryCompositeKey(testID, key)
	c.mu.Lock()
	element := c.entries[composite]
	if element == nil {
		c.mu.Unlock()
		return nil, false
	}
	entry := element.Value.(*groupMetricsHistoryCacheEntry)
	c.order.MoveToBack(element)
	out := append([]*LiveMetrics(nil), entry.history...)
	c.mu.Unlock()
	return out, true
}

func (c *groupMetricsHistoryCacheManager) put(testID, key string, history []*LiveMetrics) {
	if c == nil || len(history) == 0 {
		return
	}
	weight := groupMetricsHistoryCacheWeight(history)
	if weight <= 0 || weight > maxGroupMetricsHistoryCacheEntryBytes {
		return
	}
	composite := groupMetricsHistoryCompositeKey(testID, key)
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing := c.entries[composite]; existing != nil {
		c.removeLocked(composite, existing)
	}
	for c.perTest[testID] >= maxGroupMetricsHistoryCacheEntriesPerTest {
		if !c.removeOldestForTestLocked(testID) {
			break
		}
	}
	for len(c.entries) >= maxGroupMetricsHistoryCacheEntriesGlobal || c.total > maxGroupMetricsHistoryCacheBytes-weight {
		oldest := c.order.Front()
		if oldest == nil {
			return
		}
		oldestEntry := oldest.Value.(*groupMetricsHistoryCacheEntry)
		c.removeLocked(groupMetricsHistoryCompositeKey(oldestEntry.testID, oldestEntry.key), oldest)
	}
	entry := &groupMetricsHistoryCacheEntry{
		testID:  testID,
		key:     key,
		history: append([]*LiveMetrics(nil), history...),
		bytes:   weight,
	}
	element := c.order.PushBack(entry)
	c.entries[composite] = element
	c.perTest[testID]++
	c.total += weight
}

func (c *groupMetricsHistoryCacheManager) deleteTest(testID string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	for element := c.order.Front(); element != nil; {
		next := element.Next()
		entry := element.Value.(*groupMetricsHistoryCacheEntry)
		if entry.testID == testID {
			c.removeLocked(groupMetricsHistoryCompositeKey(entry.testID, entry.key), element)
		}
		element = next
	}
	c.mu.Unlock()
}

func (c *groupMetricsHistoryCacheManager) removeOldestForTestLocked(testID string) bool {
	for element := c.order.Front(); element != nil; element = element.Next() {
		entry := element.Value.(*groupMetricsHistoryCacheEntry)
		if entry.testID == testID {
			c.removeLocked(groupMetricsHistoryCompositeKey(entry.testID, entry.key), element)
			return true
		}
	}
	return false
}

func (c *groupMetricsHistoryCacheManager) removeLocked(composite string, element *list.Element) {
	if element == nil {
		return
	}
	entry := element.Value.(*groupMetricsHistoryCacheEntry)
	delete(c.entries, composite)
	c.order.Remove(element)
	c.total -= entry.bytes
	if c.total < 0 {
		c.total = 0
	}
	c.perTest[entry.testID]--
	if c.perTest[entry.testID] <= 0 {
		delete(c.perTest, entry.testID)
	}
}

func groupMetricsHistoryCompositeKey(testID, key string) string {
	if testID == "" {
		testID = "unknown"
	}
	return testID + "\x00" + key
}

func deleteGroupMetricsHistoryCache(testID string) {
	groupMetricsHistoryCaches.deleteTest(testID)
}

func groupMetricsHistoryCacheWeight(history []*LiveMetrics) int {
	encoded, err := json.Marshal(history)
	if err != nil || len(encoded) == 0 {
		return 0
	}
	// JSON bytes undercount Go map/slice headers and allocator overhead. A 4x
	// multiplier plus one KiB per point is intentionally conservative, keeping
	// retained heap comfortably below the configured logical budget.
	if len(encoded) > maxGroupMetricsHistoryCacheEntryBytes/4 {
		return maxGroupMetricsHistoryCacheEntryBytes + 1
	}
	weight := len(encoded)*4 + len(history)*1024
	if weight < 0 {
		return maxGroupMetricsHistoryCacheEntryBytes + 1
	}
	return weight
}

func compactGroupFilteredMetricsForCache(point *LiveMetrics) {
	if point == nil {
		return
	}
	// Error evidence is explicitly excluded from the metrics JSON response and
	// can carry snapshots. Do not retain it through a response-only cache.
	point.ErrorEvidence = nil
	for _, group := range point.GroupMetrics {
		if group != nil {
			group.DurationDigest, group.DurationSketch = nil, nil
		}
	}
}

func groupMetricsHistoryCacheKey(history []*LiveMetrics, filter groupFilterQuery) string {
	names := append([]string(nil), filter.Names...)
	sort.Strings(names)
	firstUnixNano := int64(0)
	lastUnixNano := int64(0)
	for _, point := range history {
		if point != nil {
			firstUnixNano = point.Timestamp.UnixNano()
			break
		}
	}
	for index := len(history) - 1; index >= 0; index-- {
		if history[index] != nil {
			lastUnixNano = history[index].Timestamp.UnixNano()
			break
		}
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
	clone.DurationSketch = engine.CloneDurationSketchSnapshot(metrics.DurationSketch)
	return &clone
}

func clearLivePerformanceMetrics(live *LiveMetrics) {
	live.MessagingProgress = nil
	live.JavaHTTP = nil
	live.Messaging = nil
	live.NativeEvidence = nil
	live.Transactions = nil
	live.ActiveVUsers = 0
	live.TPS = 0
	live.TargetTPS = 0
	live.ThroughputGapPct = 0
	live.ThroughputTargetMissed = false
	live.AvgResponseMs = 0
	live.ErrorPct = 0
	live.TotalTx = 0
	live.TotalErrors = 0
	live.RunTotalTx = nil
	live.RunTotalErrors = nil
	live.RunErrorPct = nil
	live.Requests = 0
	live.RequestErrors = 0
	live.RequestRate = 0
	live.RunTotalRequests = nil
	live.RunRequestErrors = nil
	live.RunRequestErrorPct = nil
	live.P50Ms = 0
	live.P90Ms = 0
	live.P95Ms = 0
	live.P99Ms = 0
	live.PercentileQuality = ""
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
	// Selected groups merge exactly when every one carries a sketch.
	mergedSketch, sketched := engine.NewDurationSketch(), true
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
		live.Requests += group.Requests
		live.RequestErrors += group.RequestErrors
		live.RequestRate += group.RequestRate
		if groupAttempts > 0 {
			attempts += groupAttempts
			weightedDuration += group.AvgResponseMs * float64(groupAttempts)
			weightedPercentiles["p50"] += group.P50Ms * float64(groupAttempts)
			weightedPercentiles["p90"] += group.P90Ms * float64(groupAttempts)
			weightedPercentiles["p95"] += group.P95Ms * float64(groupAttempts)
			weightedPercentiles["p99"] += group.P99Ms * float64(groupAttempts)
			percentileWeight += groupAttempts
			if part := engine.DurationSketchForCount(group.DurationSketch, groupAttempts); part != nil {
				mergedSketch.Merge(part)
			} else {
				sketched = false
			}
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
	applySelectedGroupRunTotals(live, selected)
	switch {
	case sketched && mergedSketch.Count() > 0:
		live.P50Ms = mergedSketch.Quantile(0.50) / 1_000_000
		live.P90Ms = mergedSketch.Quantile(0.90) / 1_000_000
		live.P95Ms = mergedSketch.Quantile(0.95) / 1_000_000
		live.P99Ms = mergedSketch.Quantile(0.99) / 1_000_000
	case digestCount > 0:
		live.P50Ms = mergedDigest.Quantile(0.50) / 1_000_000
		live.P90Ms = mergedDigest.Quantile(0.90) / 1_000_000
		live.P95Ms = mergedDigest.Quantile(0.95) / 1_000_000
		live.P99Ms = mergedDigest.Quantile(0.99) / 1_000_000
		live.PercentileQuality = percentileQualityTDigest
	case percentileWeight > 0:
		live.P50Ms = weightedPercentiles["p50"] / float64(percentileWeight)
		live.P90Ms = weightedPercentiles["p90"] / float64(percentileWeight)
		live.P95Ms = weightedPercentiles["p95"] / float64(percentileWeight)
		live.P99Ms = weightedPercentiles["p99"] / float64(percentileWeight)
		live.PercentileQuality = percentileQualityAveraged
	}
	if live.TargetTPS > 0 && live.TPS < live.TargetTPS {
		live.ThroughputGapPct = ((live.TargetTPS - live.TPS) / live.TargetTPS) * 100
		live.ThroughputTargetMissed = live.ThroughputTargetMissed || live.ThroughputGapPct >= 5.0
	}
}

// applySelectedGroupRunTotals scopes the run-to-date counters to the selected
// groups, like the window counters. They stay absent unless every selected
// group carries them, so a filtered view never mixes in whole-test totals.
func applySelectedGroupRunTotals(live *LiveMetrics, selected []*GroupLiveMetrics) {
	var counts runTotalsCounts
	counted, requestsCounted := false, true
	for _, group := range selected {
		if group == nil {
			continue
		}
		if group.RunTransactions == nil || group.RunErrors == nil {
			return
		}
		counts.tx += *group.RunTransactions
		counts.errors += *group.RunErrors
		if group.RunRequests == nil || group.RunRequestErrors == nil {
			requestsCounted = false
		} else {
			counts.requests += *group.RunRequests
			counts.requestErrors += *group.RunRequestErrors
		}
		counted = true
	}
	if !counted {
		return
	}
	setLiveRunTotals(live, counts, requestsCounted)
}

func sortedGroupFilterNames(set map[string]struct{}) []string {
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
