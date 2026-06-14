package api

// loaders — split from archive.go (same package, behavior-preserving).

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func loadArchiveTransactions(record *storage.RunArchiveRecord, filter transactionEventFilter, page archivePage) ([]TransactionEvent, int, error) {
	if err := ensureArchiveMaterialized(record); err != nil {
		return nil, 0, err
	}
	manifest, err := loadRunArchiveManifest(record.ManifestPath)
	if err != nil {
		return nil, 0, err
	}
	path, err := archiveArtifactPath(record.ArchivePath, manifest.TransactionsFile)
	if err != nil {
		return nil, 0, err
	}
	predicate := func(item *TransactionEvent) bool {
		return transactionMatchesArchiveFilter(item, filter)
	}

	if filter.SortBy != "" {
		items, total, err := readJSONLFiltered(path, predicate)
		if err != nil {
			return nil, 0, err
		}
		sortArchiveTransactions(items, filter.SortBy, filter.SortDir)
		paged := pageArchiveItems(items, page)
		normalizeArchiveTransactionSnapshots(paged)
		return paged, total, nil
	}

	items, total, err := readJSONLPage(path, page, predicate)
	if err != nil {
		return nil, 0, err
	}
	normalizeArchiveTransactionSnapshots(items)
	return items, total, nil
}

func transactionMatchesArchiveFilter(item *TransactionEvent, filter transactionEventFilter) bool {
	if filter.Action != "" && !strings.EqualFold(item.ActionName, filter.Action) {
		return false
	}
	if filter.SOAPOperation != "" && !strings.EqualFold(strings.TrimSpace(item.Extractions["soap_operation"]), filter.SOAPOperation) {
		return false
	}
	if filter.Group != "" && !strings.EqualFold(item.Group, filter.Group) {
		return false
	}
	if filter.CorrelationID != "" && !strings.EqualFold(item.CorrelationID, filter.CorrelationID) {
		return false
	}
	if filter.TraceID != "" && !strings.EqualFold(item.TraceID, filter.TraceID) {
		return false
	}
	if filter.DependencyType != "" && !strings.EqualFold(item.DependencyType, filter.DependencyType) {
		return false
	}
	if filter.DependencyTarget != "" && !strings.EqualFold(item.DependencyTarget, filter.DependencyTarget) {
		return false
	}
	if filter.TimeFrom != nil && item.Timestamp.Before(*filter.TimeFrom) {
		return false
	}
	if filter.TimeTo != nil && item.Timestamp.After(*filter.TimeTo) {
		return false
	}
	if filter.Status != "" && !strings.Contains(strings.ToLower(transactionStatusLabel(item)), strings.ToLower(filter.Status)) {
		return false
	}
	if filter.MinDurationMs != nil && item.DurationMs < *filter.MinDurationMs {
		return false
	}
	if filter.MaxDurationMs != nil && item.DurationMs > *filter.MaxDurationMs {
		return false
	}
	if filter.MinBytes != nil && item.BytesReceived < *filter.MinBytes {
		return false
	}
	if filter.MaxBytes != nil && item.BytesReceived > *filter.MaxBytes {
		return false
	}
	if filter.ErrorContains != "" && !strings.Contains(strings.ToLower(item.ErrorMessage), strings.ToLower(filter.ErrorContains)) {
		return false
	}
	if filter.Success != nil && item.Success != *filter.Success {
		return false
	}
	return true
}

func transactionStatusLabel(item *TransactionEvent) string {
	if item.StatusCode != 0 {
		return fmt.Sprint(item.StatusCode)
	}
	if item.Success {
		return "OK"
	}
	return "ERR"
}

func transactionStatusSortValue(item TransactionEvent) int {
	if item.StatusCode != 0 {
		return item.StatusCode
	}
	if item.Success {
		return 200
	}
	return 599
}

func sortArchiveTransactions(items []TransactionEvent, sortBy string, sortDir string) {
	descending := strings.EqualFold(sortDir, "desc")
	sort.SliceStable(items, func(i, j int) bool {
		result := compareArchiveTransactions(items[i], items[j], sortBy)
		if result == 0 && sortBy != "time" {
			result = compareTime(items[i].Timestamp, items[j].Timestamp)
		}
		if result == 0 {
			result = strings.Compare(items[i].ActionName, items[j].ActionName)
		}
		if descending {
			return result > 0
		}
		return result < 0
	})
}

func compareArchiveTransactions(a TransactionEvent, b TransactionEvent, sortBy string) int {
	switch sortBy {
	case "time":
		return compareTime(a.Timestamp, b.Timestamp)
	case "status":
		return compareInt(transactionStatusSortValue(a), transactionStatusSortValue(b))
	case "duration":
		return compareFloat(a.DurationMs, b.DurationMs)
	case "bytes":
		return compareInt64(a.BytesReceived, b.BytesReceived)
	case "error":
		return strings.Compare(strings.ToLower(a.ErrorMessage), strings.ToLower(b.ErrorMessage))
	default:
		return 0
	}
}

func compareTime(a time.Time, b time.Time) int {
	if a.Before(b) {
		return -1
	}
	if a.After(b) {
		return 1
	}
	return 0
}

func compareFloat(a float64, b float64) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func compareInt(a int, b int) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func compareInt64(a int64, b int64) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func loadArchiveResourceSamples(record *storage.RunArchiveRecord, page archivePage) ([]ResourceSample, int, error) {
	if err := ensureArchiveMaterialized(record); err != nil {
		return nil, 0, err
	}
	manifest, err := loadRunArchiveManifest(record.ManifestPath)
	if err != nil {
		return nil, 0, err
	}
	path, err := archiveArtifactPath(record.ArchivePath, manifest.ResourceSamplesFile)
	if err != nil {
		return nil, 0, err
	}
	return readJSONLPage[ResourceSample](path, page, nil)
}

func loadArchiveIntervalSummaries(record *storage.RunArchiveRecord, page archivePage, action string, groupFilter groupFilterQuery) ([]IntervalSummaryRecord, int, error) {
	action = strings.TrimSpace(action)
	if action != "" || groupFilter.Explicit {
		summaries, err := loadArchiveIntervalSummariesForFilters(record, action, groupFilter)
		if err != nil {
			return nil, 0, err
		}
		return pageArchiveItems(summaries, page), len(summaries), nil
	}
	if err := ensureArchiveMaterialized(record); err != nil {
		return nil, 0, err
	}
	manifest, err := loadRunArchiveManifest(record.ManifestPath)
	if err != nil {
		return nil, 0, err
	}
	path, err := archiveArtifactPath(record.ArchivePath, manifest.IntervalSummariesFile)
	if err != nil {
		return nil, 0, err
	}
	return readJSONLPage[IntervalSummaryRecord](path, page, nil)
}

func loadArchiveIntervalSummariesForAction(record *storage.RunArchiveRecord, action string) ([]IntervalSummaryRecord, error) {
	return loadArchiveIntervalSummariesForFilters(record, action, groupFilterQuery{})
}

func loadArchiveIntervalSummariesForFilters(record *storage.RunArchiveRecord, action string, groupFilter groupFilterQuery) ([]IntervalSummaryRecord, error) {
	action = strings.TrimSpace(action)
	summaries, err := readAllArchiveSummaries(record)
	if err != nil {
		return nil, err
	}
	if (action == "" && !groupFilter.Explicit) || len(summaries) == 0 {
		return summaries, nil
	}
	transactions, err := readAllArchiveTransactions(record)
	if err != nil {
		return nil, err
	}
	var configuredGroups []TestGroup
	if manifest, err := loadRunArchiveManifest(record.ManifestPath); err == nil && manifest != nil {
		configuredGroups = manifest.Provenance.Groups
	}
	groupSet := selectedGroupNameSet(groupFilter.Names)
	buckets := make([]archiveActionSummaryBucket, len(summaries))
	for _, tx := range transactions {
		if groupFilter.Explicit {
			if len(groupSet) == 0 {
				continue
			}
			if _, ok := groupSet[tx.Group]; !ok {
				continue
			}
		}
		if action != "" && !strings.EqualFold(tx.ActionName, action) {
			continue
		}
		index := archiveSummaryBucketIndex(summaries, tx.Timestamp)
		if index < 0 {
			continue
		}
		bucket := &buckets[index]
		bucket.transactions++
		bucket.totalDurationMs += tx.DurationMs
		bucket.durations = append(bucket.durations, tx.DurationMs)
		if !tx.Success {
			bucket.errors++
		}
	}

	filtered := make([]IntervalSummaryRecord, 0, len(summaries))
	var cumulativeTx int64
	var cumulativeErrors int64
	for index, summary := range summaries {
		bucket := buckets[index]
		cumulativeTx += bucket.transactions
		cumulativeErrors += bucket.errors
		record := IntervalSummaryRecord{
			Timestamp:    summary.Timestamp,
			TestID:       summary.TestID,
			TestName:     summary.TestName,
			ActiveVUsers: archiveSummaryActiveVUsersForGroups(summary, groupSet, groupFilter.Explicit, configuredGroups),
			TotalTx:      cumulativeTx,
			TotalErrors:  cumulativeErrors,
			Groups:       archiveSelectedSummaryGroups(summary.Groups, groupSet, groupFilter.Explicit),
		}
		if bucket.transactions > 0 {
			record.TPS = float64(bucket.transactions) / archiveSummaryBucketSeconds(summaries, index)
			record.AvgResponseMs = bucket.totalDurationMs / float64(bucket.transactions)
			record.ErrorPct = float64(bucket.errors) / float64(bucket.transactions) * 100
			sort.Float64s(bucket.durations)
			record.P50Ms = archiveAnalysisPercentile(bucket.durations, 0.50)
			record.P90Ms = archiveAnalysisPercentile(bucket.durations, 0.90)
			record.P95Ms = archiveAnalysisPercentile(bucket.durations, 0.95)
			record.P99Ms = archiveAnalysisPercentile(bucket.durations, 0.99)
		}
		// The whole-test throughput target does not apply to a filtered subset;
		// derive it from the selected groups' own targets and recompute the gap
		// against the filtered TPS to avoid a contradictory missed-target flag.
		record.TargetTPS, record.ThroughputGapPct, record.ThroughputTargetMissed =
			archiveFilteredThroughputTarget(summary, groupSet, groupFilter.Explicit, record.TPS)
		filtered = append(filtered, record)
	}
	return filtered, nil
}

func archiveFilteredThroughputTarget(summary IntervalSummaryRecord, groupSet map[string]struct{}, explicit bool, filteredTPS float64) (float64, float64, bool) {
	if !explicit || len(groupSet) == 0 || len(summary.Groups) == 0 {
		return 0, 0, false
	}
	var targetTPS float64
	for name := range groupSet {
		if group := summary.Groups[name]; group != nil {
			targetTPS += group.TargetTPS
		}
	}
	if targetTPS <= 0 {
		return 0, 0, false
	}
	gap, missed := throughputGap(filteredTPS, targetTPS)
	return targetTPS, gap, missed
}

func archiveSummaryActiveVUsersForGroups(summary IntervalSummaryRecord, groupSet map[string]struct{}, explicit bool, configuredGroups []TestGroup) int64 {
	if !explicit {
		return summary.ActiveVUsers
	}
	if len(groupSet) == 0 {
		return 0
	}
	if len(summary.Groups) > 0 {
		var active int64
		for name := range groupSet {
			if group := summary.Groups[name]; group != nil {
				active += int64(group.VUsers)
			}
		}
		return active
	}
	// No per-group VUser counts were captured for this interval. Estimate the
	// subset proportionally from the configured group sizes when available.
	if summary.ActiveVUsers > 0 && len(configuredGroups) > 0 {
		var selectedConfigured int
		var totalConfigured int
		for _, group := range configuredGroups {
			if group.VUsers <= 0 {
				continue
			}
			totalConfigured += group.VUsers
			if _, selected := groupSet[group.Name]; selected {
				selectedConfigured += group.VUsers
			}
		}
		if selectedConfigured > 0 && totalConfigured > 0 {
			estimated := int64(math.Round(float64(summary.ActiveVUsers) * float64(selectedConfigured) / float64(totalConfigured)))
			if estimated < 0 {
				return 0
			}
			if estimated > summary.ActiveVUsers {
				return summary.ActiveVUsers
			}
			return estimated
		}
	}
	// No basis to attribute VUsers to the subset (no per-group metrics and no
	// usable configured group sizes). Fall back to the run-wide active VUser
	// count so a group that demonstrably ran is not reported as flat zero.
	if summary.ActiveVUsers > 0 {
		return summary.ActiveVUsers
	}
	return 0
}

func archiveSelectedSummaryGroups(groups map[string]*GroupLiveMetrics, groupSet map[string]struct{}, explicit bool) map[string]*GroupLiveMetrics {
	if len(groups) == 0 {
		return nil
	}
	if !explicit {
		return cloneGroupLiveMetricsMap(groups)
	}
	if len(groupSet) == 0 {
		return map[string]*GroupLiveMetrics{}
	}
	selected := make(map[string]*GroupLiveMetrics, len(groupSet))
	for name := range groupSet {
		if group := groups[name]; group != nil {
			selected[name] = cloneGroupLiveMetrics(group)
		}
	}
	return selected
}

func archiveSummaryBucketIndex(summaries []IntervalSummaryRecord, timestamp time.Time) int {
	if len(summaries) == 0 {
		return -1
	}
	for index, summary := range summaries {
		if !timestamp.After(summary.Timestamp) {
			return index
		}
	}
	return len(summaries) - 1
}

func archiveSummaryBucketSeconds(summaries []IntervalSummaryRecord, index int) float64 {
	if index > 0 && index < len(summaries) {
		if seconds := summaries[index].Timestamp.Sub(summaries[index-1].Timestamp).Seconds(); seconds > 0 {
			return seconds
		}
	}
	if index == 0 && len(summaries) > 1 {
		if seconds := summaries[1].Timestamp.Sub(summaries[0].Timestamp).Seconds(); seconds > 0 {
			return seconds
		}
	}
	return 1
}

func archiveActionPeakTPSFromBuckets(summaries []IntervalSummaryRecord, buckets []archiveActionSummaryBucket) float64 {
	var peak float64
	for index, bucket := range buckets {
		if bucket.transactions == 0 {
			continue
		}
		tps := float64(bucket.transactions) / archiveSummaryBucketSeconds(summaries, index)
		if tps > peak {
			peak = tps
		}
	}
	return peak
}

func normalizeArchiveTransactionSnapshots(items []TransactionEvent) {
	for index := range items {
		normalizeArchiveTransactionSnapshot(&items[index])
	}
}

func normalizeArchiveTransactionSnapshot(item *TransactionEvent) {
	if item == nil || item.Snapshot == nil {
		return
	}
	item.Snapshot.RequestURL = resolveArchivedSnapshotRequestURL(item.Snapshot.RequestURL, item.DependencyTarget)
}

func resolveArchivedSnapshotRequestURL(requestURL string, dependencyTarget string) string {
	requestURL = strings.TrimSpace(requestURL)
	if requestURL == "" || !strings.Contains(requestURL, "{{") || !strings.Contains(requestURL, "}}") {
		return requestURL
	}
	match := archiveSnapshotLeadingTemplateBasePattern.FindStringSubmatch(requestURL)
	if len(match) != 2 {
		return requestURL
	}
	dependencyTarget = strings.TrimSpace(dependencyTarget)
	if dependencyTarget == "" {
		return requestURL
	}
	if base, _, ok := strings.Cut(dependencyTarget, "#"); ok {
		dependencyTarget = strings.TrimSpace(base)
	}
	if dependencyTarget == "" {
		return requestURL
	}

	baseURL := dependencyTarget
	if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
		scheme := "https://"
		if strings.HasSuffix(baseURL, ":80") {
			scheme = "http://"
		}
		baseURL = scheme + baseURL
	}

	suffix := strings.TrimSpace(match[1])
	if suffix == "" {
		return baseURL
	}
	if strings.HasPrefix(suffix, "/") || strings.HasPrefix(suffix, "?") || strings.HasPrefix(suffix, "#") {
		return strings.TrimRight(baseURL, "/") + suffix
	}
	return strings.TrimRight(baseURL, "/") + "/" + suffix
}

func readAllArchiveTransactions(record *storage.RunArchiveRecord) ([]TransactionEvent, error) {
	items, err := readAllArchiveRecords[TransactionEvent](record, func(manifest *RunArchiveManifest) string {
		return manifest.TransactionsFile
	})
	if err != nil {
		return nil, err
	}
	normalizeArchiveTransactionSnapshots(items)
	return items, nil
}

func readAllArchiveResources(record *storage.RunArchiveRecord) ([]ResourceSample, error) {
	return readAllArchiveRecords[ResourceSample](record, func(manifest *RunArchiveManifest) string {
		return manifest.ResourceSamplesFile
	})
}

func readAllArchiveSummaries(record *storage.RunArchiveRecord) ([]IntervalSummaryRecord, error) {
	return readAllArchiveRecords[IntervalSummaryRecord](record, func(manifest *RunArchiveManifest) string {
		return manifest.IntervalSummariesFile
	})
}

func readAllArchiveRecords[T any](record *storage.RunArchiveRecord, pathSelector func(*RunArchiveManifest) string) ([]T, error) {
	manifest, err := loadRunArchiveManifest(record.ManifestPath)
	if err != nil {
		return nil, err
	}
	path, err := archiveArtifactPath(record.ArchivePath, pathSelector(manifest))
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 2*1024*1024)

	items := make([]T, 0)
	for scanner.Scan() {
		var item T
		if err := json.Unmarshal(scanner.Bytes(), &item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func readJSONLFiltered[T any](path string, predicate func(*T) bool) ([]T, int, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 2*1024*1024)

	items := make([]T, 0)
	matched := 0
	for scanner.Scan() {
		var item T
		if err := json.Unmarshal(scanner.Bytes(), &item); err != nil {
			return nil, 0, err
		}
		if predicate != nil && !predicate(&item) {
			continue
		}
		items = append(items, item)
		matched++
	}
	if err := scanner.Err(); err != nil {
		return nil, 0, err
	}
	return items, matched, nil
}

func pageArchiveItems[T any](items []T, page archivePage) []T {
	limit := page.Limit
	if limit <= 0 {
		limit = 100
	}
	offset := page.Offset
	if offset < 0 {
		offset = 0
	}
	if offset >= len(items) {
		return []T{}
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

func readJSONLPage[T any](path string, page archivePage, predicate func(*T) bool) ([]T, int, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()

	limit := page.Limit
	if limit <= 0 {
		limit = 100
	}
	if page.Offset < 0 {
		page.Offset = 0
	}

	scanner := bufio.NewScanner(file)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 2*1024*1024)

	items := make([]T, 0, limit)
	matched := 0
	for scanner.Scan() {
		var item T
		if err := json.Unmarshal(scanner.Bytes(), &item); err != nil {
			return nil, 0, err
		}
		if predicate != nil && !predicate(&item) {
			continue
		}
		if matched >= page.Offset && len(items) < limit {
			items = append(items, item)
		}
		matched++
	}
	if err := scanner.Err(); err != nil {
		return nil, 0, err
	}
	return items, matched, nil
}
