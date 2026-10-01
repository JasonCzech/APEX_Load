package api

// loaders — split from archive.go (same package, behavior-preserving).

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	maxArchiveAnalysisRecords      = 500_000
	maxArchiveAnalysisBytes        = 128 << 20
	maxArchiveSortWindow           = 100_000
	maxArchivePageSize             = 1_000
	maxArchiveJSONLRecordBytes     = 64 << 20
	maxArchiveAnalysisDimensions   = 10_000
	maxArchiveReportAggregateCells = 1_000_000
	maxArchiveQueryRecords         = 2_000_000
	maxArchiveQueryBytes           = 512 << 20
)

type archiveScanBudget struct {
	ctx        context.Context
	records    int
	bytes      int64
	maxRecords int
	maxBytes   int64
}

func newArchiveScanBudget(ctx context.Context, maxRecords int, maxBytes int64) *archiveScanBudget {
	if ctx == nil {
		ctx = context.Background()
	}
	return &archiveScanBudget{ctx: ctx, maxRecords: maxRecords, maxBytes: maxBytes}
}

func (b *archiveScanBudget) Observe(record []byte) error {
	if err := b.ctx.Err(); err != nil {
		return err
	}
	b.records++
	b.bytes += int64(len(record))
	if b.maxRecords > 0 && b.records > b.maxRecords {
		return fmt.Errorf("archive query exceeds record budget %d", b.maxRecords)
	}
	if b.maxBytes > 0 && b.bytes > b.maxBytes {
		return fmt.Errorf("archive query exceeds byte budget %d", b.maxBytes)
	}
	return nil
}

func newArchiveJSONLScanner(reader io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), maxArchiveJSONLRecordBytes)
	return scanner
}

func loadArchiveTransactions(record *storage.RunArchiveRecord, filter transactionEventFilter, page archivePage) ([]TransactionEvent, int, error) {
	return loadArchiveTransactionsContext(context.Background(), record, filter, page)
}

func loadArchiveTransactionsContext(ctx context.Context, record *storage.RunArchiveRecord, filter transactionEventFilter, page archivePage) ([]TransactionEvent, int, error) {
	lease, err := acquireArchiveMaterialized(ctx, record)
	if err != nil {
		return nil, 0, err
	}
	defer lease.Release()
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		return nil, 0, err
	}
	file, _, err := openArchiveArtifactForRead(record.ArchivePath, manifest.TransactionsFile)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()
	predicate := func(item *TransactionEvent) bool {
		return transactionMatchesArchiveFilter(item, filter)
	}

	if filter.SortBy != "" {
		items, total, err := readJSONLSortedTransactionPageFrom(ctx, file, predicate, page, filter.SortBy, filter.SortDir)
		if err != nil {
			return nil, 0, err
		}
		normalizeArchiveTransactionSnapshots(items)
		return items, total, nil
	}

	items, total, err := readJSONLPageFrom(ctx, file, page, predicate)
	if err != nil {
		return nil, 0, err
	}
	normalizeArchiveTransactionSnapshots(items)
	return items, total, nil
}

// readJSONLSortedTransactionPage performs a bounded top-K selection while
// scanning the archive. It never materializes every matching transaction just
// to return one sorted page.
func readJSONLSortedTransactionPage(path string, predicate func(*TransactionEvent) bool, page archivePage, sortBy, sortDir string) ([]TransactionEvent, int, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()
	return readJSONLSortedTransactionPageFrom(context.Background(), file, predicate, page, sortBy, sortDir)
}

func readJSONLSortedTransactionPageFrom(ctx context.Context, reader io.Reader, predicate func(*TransactionEvent) bool, page archivePage, sortBy, sortDir string) ([]TransactionEvent, int, error) {
	limit := page.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > maxArchivePageSize {
		return nil, 0, fmt.Errorf("archive page limit exceeds %d", maxArchivePageSize)
	}
	if page.Offset < 0 {
		page.Offset = 0
	}
	keep := page.Offset + limit
	if keep < limit || keep > maxArchiveSortWindow {
		return nil, 0, fmt.Errorf("sorted archive window exceeds %d records", maxArchiveSortWindow)
	}
	scanner := newArchiveJSONLScanner(reader)
	budget := newArchiveScanBudget(ctx, maxArchiveQueryRecords, maxArchiveQueryBytes)
	items := make([]TransactionEvent, 0, min(keep, limit*2))
	total := 0
	var sourceOrdinal int64
	for scanner.Scan() {
		if err := budget.Observe(scanner.Bytes()); err != nil {
			return nil, 0, err
		}
		var item TransactionEvent
		if err := json.Unmarshal(scanner.Bytes(), &item); err != nil {
			return nil, 0, err
		}
		sanitizeArchiveDecodedRecordAt(&item, sourceOrdinal)
		sourceOrdinal++
		if predicate != nil && !predicate(&item) {
			continue
		}
		total++
		items = append(items, item)
		if len(items) >= keep*2 {
			sortArchiveTransactions(items, sortBy, sortDir)
			items = items[:keep]
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, 0, err
	}
	sortArchiveTransactions(items, sortBy, sortDir)
	if len(items) > keep {
		items = items[:keep]
	}
	if page.Offset >= len(items) {
		return []TransactionEvent{}, total, nil
	}
	return items[page.Offset:], total, nil
}

func transactionMatchesArchiveFilter(item *TransactionEvent, filter transactionEventFilter) bool {
	if filter.GroupDimension != "" {
		value := map[string]string{"region": item.Region, "dependency": item.DependencyTarget, "network_profile": item.NetworkProfile, "hierarchy": item.Hierarchy, "phase": item.Phase}[filter.GroupDimension]
		if value != filter.GroupValue {
			return false
		}
	}
	if filter.ActionExact != "" && item.ActionName != filter.ActionExact {
		return false
	}
	if filter.TimeToExclusive && filter.TimeTo != nil && !item.Timestamp.Before(*filter.TimeTo) {
		return false
	}
	if filter.Population != "" && archiveEventPopulation(*item) != filter.Population {
		return false
	}
	if filter.Action != "" && !strings.EqualFold(item.ActionName, filter.Action) {
		return false
	}
	if filter.SOAPOperation != "" && !strings.EqualFold(strings.TrimSpace(item.Extractions["soap_operation"]), filter.SOAPOperation) {
		return false
	}
	if filter.Group != "" && !strings.EqualFold(item.Group, filter.Group) {
		return false
	}
	if filter.Region != "" && !strings.EqualFold(item.Region, filter.Region) {
		return false
	}
	if filter.PropertyName != "" && item.PropertyDimensions[filter.PropertyName] != filter.PropertyValue {
		return false
	}
	if filter.NetworkProfile != "" && !strings.EqualFold(item.NetworkProfile, filter.NetworkProfile) {
		return false
	}
	if filter.SourceTransaction != "" && !strings.EqualFold(item.SourceTransaction, filter.SourceTransaction) {
		return false
	}
	if filter.Hierarchy != "" && !strings.EqualFold(item.Hierarchy, filter.Hierarchy) {
		return false
	}
	if filter.Phase != "" && !strings.EqualFold(item.Phase, filter.Phase) {
		return false
	}
	if filter.HasDetail != nil && item.HasDetail != *filter.HasDetail {
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
	return loadArchiveResourceSamplesContext(context.Background(), record, page)
}

func loadArchiveResourceSamplesContext(ctx context.Context, record *storage.RunArchiveRecord, page archivePage) ([]ResourceSample, int, error) {
	lease, err := acquireArchiveMaterialized(ctx, record)
	if err != nil {
		return nil, 0, err
	}
	defer lease.Release()
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		return nil, 0, err
	}
	file, _, err := openArchiveArtifactForRead(record.ArchivePath, manifest.ResourceSamplesFile)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()
	return readJSONLPageFrom[ResourceSample](ctx, file, page, nil)
}

func loadArchiveIntervalSummaries(record *storage.RunArchiveRecord, page archivePage, action string, groupFilter groupFilterQuery) ([]IntervalSummaryRecord, int, error) {
	return loadArchiveIntervalSummariesContext(context.Background(), record, page, action, groupFilter)
}

func loadArchiveIntervalSummariesContext(ctx context.Context, record *storage.RunArchiveRecord, page archivePage, action string, groupFilter groupFilterQuery) ([]IntervalSummaryRecord, int, error) {
	action = strings.TrimSpace(action)
	if action != "" || groupFilter.Explicit {
		summaries, err := loadArchiveIntervalSummariesForFiltersContext(ctx, record, action, groupFilter)
		if err != nil {
			return nil, 0, err
		}
		return pageArchiveItems(summaries, page), len(summaries), nil
	}
	lease, err := acquireArchiveMaterialized(ctx, record)
	if err != nil {
		return nil, 0, err
	}
	defer lease.Release()
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		return nil, 0, err
	}
	file, _, err := openArchiveArtifactForRead(record.ArchivePath, manifest.IntervalSummariesFile)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()
	return readJSONLPageFrom[IntervalSummaryRecord](ctx, file, page, nil)
}

func loadArchiveIntervalSummariesForAction(record *storage.RunArchiveRecord, action string) ([]IntervalSummaryRecord, error) {
	return loadArchiveIntervalSummariesForFilters(record, action, groupFilterQuery{})
}

func loadArchiveIntervalSummariesForFilters(record *storage.RunArchiveRecord, action string, groupFilter groupFilterQuery) ([]IntervalSummaryRecord, error) {
	return loadArchiveIntervalSummariesForFiltersContext(context.Background(), record, action, groupFilter)
}

func loadArchiveIntervalSummariesForFiltersContext(ctx context.Context, record *storage.RunArchiveRecord, action string, groupFilter groupFilterQuery) ([]IntervalSummaryRecord, error) {
	action = strings.TrimSpace(action)
	summaries, err := readAllArchiveSummariesContext(ctx, record)
	if err != nil {
		return nil, err
	}
	if (action == "" && !groupFilter.Explicit) || len(summaries) == 0 {
		return summaries, nil
	}
	var configuredGroups []TestGroup
	if manifest, err := loadRunArchiveManifestFromRecord(record); err == nil && manifest != nil {
		configuredGroups = manifest.Provenance.Groups
	}
	groupSet := selectedGroupNameSet(groupFilter.Names)
	allBuckets := make([]archiveActionSummaryBucket, len(summaries))
	businessBuckets := make([]archiveActionSummaryBucket, len(summaries))
	allDurationQuantiles := make([]*archiveQuantileAccumulator, len(summaries))
	businessDurationQuantiles := make([]*archiveQuantileAccumulator, len(summaries))
	allTransactionSeriesBuckets := make([]map[string]*intervalTransactionAccumulator, len(summaries))
	businessTransactionSeriesBuckets := make([]map[string]*intervalTransactionAccumulator, len(summaries))
	transactionNames := make(map[string]struct{})
	hasBusinessTransactions := false
	transactionSeriesCells := 0
	addTransactionSeries := func(series []map[string]*intervalTransactionAccumulator, index int, name string, tx *TransactionEvent) error {
		if series[index] == nil {
			series[index] = make(map[string]*intervalTransactionAccumulator)
		}
		metric := series[index][name]
		if metric == nil {
			if transactionSeriesCells >= maxArchiveReportAggregateCells {
				return fmt.Errorf("archive summary exceeds %d transaction series cells", maxArchiveReportAggregateCells)
			}
			transactionSeriesCells++
			metric = &intervalTransactionAccumulator{}
			series[index][name] = metric
		}
		metric.count++
		if !tx.Success {
			metric.errors++
		}
		metric.totalDurationMs += tx.DurationMs
		return nil
	}
	includeTransaction := func(tx *TransactionEvent) bool {
		if groupFilter.Explicit {
			if len(groupSet) == 0 {
				return false
			}
			if _, ok := groupSet[tx.Group]; !ok {
				return false
			}
		}
		if action != "" && !strings.EqualFold(tx.ActionName, action) {
			return false
		}
		return true
	}
	addSummaryEvidence := func(buckets []archiveActionSummaryBucket, quantiles []*archiveQuantileAccumulator, index int, tx *TransactionEvent) {
		bucket := &buckets[index]
		bucket.transactions++
		bucket.totalDurationMs += tx.DurationMs
		if !tx.Success {
			bucket.errors++
		}
		if quantiles[index] == nil {
			quantiles[index] = &archiveQuantileAccumulator{}
		}
		quantiles[index].Add(tx.DurationMs)
	}
	if err := forEachArchiveTransactionContext(ctx, record, func(tx *TransactionEvent) error {
		if !includeTransaction(tx) {
			return nil
		}
		index := archiveSummaryBucketIndex(summaries, tx.Timestamp)
		if index < 0 {
			return nil
		}
		addSummaryEvidence(allBuckets, allDurationQuantiles, index, tx)
		transactionName := intervalTransactionName(tx.ActionName)
		if _, exists := transactionNames[transactionName]; !exists {
			if len(transactionNames) >= maxArchiveAnalysisDimensions {
				return fmt.Errorf("archive summary exceeds %d distinct transaction names", maxArchiveAnalysisDimensions)
			}
			transactionNames[transactionName] = struct{}{}
		}
		if err := addTransactionSeries(allTransactionSeriesBuckets, index, transactionName, tx); err != nil {
			return err
		}
		if tx.BusinessTransaction {
			hasBusinessTransactions = true
			addSummaryEvidence(businessBuckets, businessDurationQuantiles, index, tx)
			if err := addTransactionSeries(businessTransactionSeriesBuckets, index, transactionName, tx); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	buckets := allBuckets
	durationQuantiles := allDurationQuantiles
	transactionSeriesBuckets := allTransactionSeriesBuckets
	if hasBusinessTransactions {
		buckets = businessBuckets
		durationQuantiles = businessDurationQuantiles
		transactionSeriesBuckets = businessTransactionSeriesBuckets
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
		windowSeconds := archiveSummaryBucketSeconds(summaries, index)
		record.Transactions = intervalTransactionsFromAccumulators(transactionSeriesBuckets[index], windowSeconds)
		if bucket.transactions > 0 {
			record.TPS = float64(bucket.transactions) / windowSeconds
			record.AvgResponseMs = bucket.totalDurationMs / float64(bucket.transactions)
			record.ErrorPct = float64(bucket.errors) / float64(bucket.transactions) * 100
			quantiles := durationQuantiles[index]
			record.P50Ms = quantiles.Quantile(0.50)
			record.P90Ms = quantiles.Quantile(0.90)
			record.P95Ms = quantiles.Quantile(0.95)
			record.P99Ms = quantiles.Quantile(0.99)
			record.PercentileQuality = archivePercentileQualityRecomputed
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

func forEachArchiveTransaction(record *storage.RunArchiveRecord, fn func(*TransactionEvent) error) error {
	return forEachArchiveTransactionContext(context.Background(), record, fn)
}

func forEachArchiveTransactionContext(ctx context.Context, record *storage.RunArchiveRecord, fn func(*TransactionEvent) error) error {
	return forEachArchiveRecordContext(ctx, record, func(manifest *RunArchiveManifest) string {
		return manifest.TransactionsFile
	}, fn)
}

// forEachArchiveRecord streams a JSONL artifact under a larger, but still hard,
// cumulative budget than materializing helpers. Reports can process archives
// beyond the in-memory limit without turning one request into unbounded CPU.
func forEachArchiveRecord[T any](record *storage.RunArchiveRecord, pathSelector func(*RunArchiveManifest) string, fn func(*T) error) error {
	return forEachArchiveRecordContext(context.Background(), record, pathSelector, fn)
}

func forEachArchiveRecordContext[T any](ctx context.Context, record *storage.RunArchiveRecord, pathSelector func(*RunArchiveManifest) string, fn func(*T) error) error {
	lease, err := acquireArchiveMaterialized(ctx, record)
	if err != nil {
		return err
	}
	defer lease.Release()
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		return err
	}
	file, _, err := openArchiveArtifactForRead(record.ArchivePath, pathSelector(manifest))
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := newArchiveJSONLScanner(file)
	budget := newArchiveScanBudget(ctx, maxArchiveQueryRecords, maxArchiveQueryBytes)
	for scanner.Scan() {
		if err := budget.Observe(scanner.Bytes()); err != nil {
			return err
		}
		var item T
		if err := json.Unmarshal(scanner.Bytes(), &item); err != nil {
			return err
		}
		sanitizeArchiveDecodedRecord(&item)
		if err := fn(&item); err != nil {
			return err
		}
	}
	return scanner.Err()
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
	index := sort.Search(len(summaries), func(i int) bool {
		return !timestamp.After(summaries[i].Timestamp)
	})
	if index < len(summaries) {
		return index
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

func readAllArchiveELKSamples(record *storage.RunArchiveRecord) ([]ELKLogSample, error) {
	samples, err := readAllArchiveRecords[ELKLogSample](record, func(manifest *RunArchiveManifest) string {
		return manifest.ELKEvidenceFile
	})
	if err != nil {
		return nil, err
	}
	for index := range samples {
		samples[index] = sanitizeELKSample(samples[index])
	}
	return samples, nil
}

func readAllArchiveDynatraceEvidence(record *storage.RunArchiveRecord) ([]DynatraceEvidenceRecord, error) {
	records, err := readAllArchiveRecords[DynatraceEvidenceRecord](record, func(manifest *RunArchiveManifest) string {
		return manifest.DynatraceEvidenceFile
	})
	if err != nil {
		return nil, err
	}
	for index := range records {
		if records[index].Log != nil {
			sample := sanitizeDynatraceLog(*records[index].Log)
			records[index].Log = &sample
		}
		if records[index].Problem != nil {
			problem := sanitizeDynatraceProblem(*records[index].Problem)
			records[index].Problem = &problem
		}
	}
	return records, nil
}

func readAllArchiveSummariesContext(ctx context.Context, record *storage.RunArchiveRecord) ([]IntervalSummaryRecord, error) {
	return readAllArchiveRecordsContext[IntervalSummaryRecord](ctx, record, func(manifest *RunArchiveManifest) string {
		return manifest.IntervalSummariesFile
	})
}

func readAllArchiveRecords[T any](record *storage.RunArchiveRecord, pathSelector func(*RunArchiveManifest) string) ([]T, error) {
	return readAllArchiveRecordsContext[T](context.Background(), record, pathSelector)
}

func readAllArchiveRecordsContext[T any](ctx context.Context, record *storage.RunArchiveRecord, pathSelector func(*RunArchiveManifest) string) ([]T, error) {
	lease, err := acquireArchiveMaterialized(ctx, record)
	if err != nil {
		return nil, err
	}
	defer lease.Release()
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		return nil, err
	}
	file, _, err := openArchiveArtifactForRead(record.ArchivePath, pathSelector(manifest))
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := newArchiveJSONLScanner(file)

	items := make([]T, 0)
	totalBytes := 0
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		totalBytes += len(scanner.Bytes())
		if len(items) >= maxArchiveAnalysisRecords || totalBytes > maxArchiveAnalysisBytes {
			return nil, fmt.Errorf("archive exceeds bounded analysis limit (%d records or %d bytes)", maxArchiveAnalysisRecords, maxArchiveAnalysisBytes)
		}
		var item T
		if err := json.Unmarshal(scanner.Bytes(), &item); err != nil {
			return nil, err
		}
		sanitizeArchiveDecodedRecord(&item)
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

	scanner := newArchiveJSONLScanner(file)

	items := make([]T, 0)
	matched := 0
	totalBytes := 0
	for scanner.Scan() {
		totalBytes += len(scanner.Bytes())
		if len(items) >= maxArchiveAnalysisRecords || totalBytes > maxArchiveAnalysisBytes {
			return nil, 0, fmt.Errorf("archive exceeds bounded analysis limit")
		}
		var item T
		if err := json.Unmarshal(scanner.Bytes(), &item); err != nil {
			return nil, 0, err
		}
		sanitizeArchiveDecodedRecord(&item)
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
	if limit > maxArchivePageSize {
		limit = maxArchivePageSize
	}
	offset := page.Offset
	if offset < 0 {
		offset = 0
	}
	if offset >= len(items) {
		return []T{}
	}
	end := len(items)
	if limit < len(items)-offset {
		end = offset + limit
	}
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
	return readJSONLPageFrom(context.Background(), file, page, predicate)
}

func readJSONLPageFrom[T any](ctx context.Context, reader io.Reader, page archivePage, predicate func(*T) bool) ([]T, int, error) {
	limit := page.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > maxArchivePageSize {
		return nil, 0, fmt.Errorf("archive page limit exceeds %d", maxArchivePageSize)
	}
	if page.Offset < 0 {
		page.Offset = 0
	}

	scanner := newArchiveJSONLScanner(reader)
	budget := newArchiveScanBudget(ctx, maxArchiveQueryRecords, maxArchiveQueryBytes)

	items := make([]T, 0, limit)
	matched := 0
	var sourceOrdinal int64
	for scanner.Scan() {
		if err := budget.Observe(scanner.Bytes()); err != nil {
			return nil, 0, err
		}
		// Skip blank lines (e.g. a trailing newline): they are not records, and
		// json.Valid("")/json.Unmarshal("") both report an error, which would
		// otherwise fail every page over a stray empty line.
		if len(bytes.TrimSpace(scanner.Bytes())) == 0 {
			continue
		}
		ordinal := sourceOrdinal
		sourceOrdinal++
		if predicate == nil {
			if matched >= page.Offset && len(items) < limit {
				var item T
				if err := json.Unmarshal(scanner.Bytes(), &item); err != nil {
					return nil, 0, err
				}
				sanitizeArchiveDecodedRecordAt(&item, ordinal)
				items = append(items, item)
			} else if !json.Valid(scanner.Bytes()) {
				// Validate rows outside the requested page too (cheap syntactic
				// check, no unmarshal), so a corrupt line anywhere in the file
				// fails fast and consistently instead of being silently counted in
				// the total and only surfacing when a client happens to page onto
				// it — which made totals overstate and pagination 500 on one page.
				return nil, 0, fmt.Errorf("invalid JSONL line %d", matched+1)
			}
			matched++
			continue
		}
		var item T
		if err := json.Unmarshal(scanner.Bytes(), &item); err != nil {
			return nil, 0, err
		}
		sanitizeArchiveDecodedRecordAt(&item, ordinal)
		if !predicate(&item) {
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

type indexedArchiveSample[T any] struct {
	index int
	value T
	score float64
	set   bool
}

type archiveSampleBucket[T any] struct {
	first  indexedArchiveSample[T]
	last   indexedArchiveSample[T]
	minima []indexedArchiveSample[T]
	maxima []indexedArchiveSample[T]
}

func boundedArchiveScoreFunctions[T any](maxItems int, scoreFunctions []func(*T) float64) ([]func(*T) float64, error) {
	if maxItems < 2 {
		return nil, fmt.Errorf("sample limit must be at least 2")
	}
	maxScoreFunctions := (maxItems - 2) / 2
	if len(scoreFunctions) > maxScoreFunctions {
		scoreFunctions = scoreFunctions[:maxScoreFunctions]
	}
	return scoreFunctions, nil
}

func newArchiveSampleBuckets[T any](maxItems int, scoreFunctionCount int) []archiveSampleBucket[T] {
	slotsPerBucket := 2 + 2*scoreFunctionCount
	bucketCount := maxItems / slotsPerBucket
	if bucketCount < 1 {
		bucketCount = 1
	}
	buckets := make([]archiveSampleBucket[T], bucketCount)
	for index := range buckets {
		buckets[index].minima = make([]indexedArchiveSample[T], scoreFunctionCount)
		buckets[index].maxima = make([]indexedArchiveSample[T], scoreFunctionCount)
	}
	return buckets
}

func recordArchiveSample[T any](buckets []archiveSampleBucket[T], totalHint int, index int, item T, scoreFunctions []func(*T) float64) {
	if totalHint < 1 {
		totalHint = 1
	}
	bucketIndex := int((int64(index) * int64(len(buckets))) / int64(totalHint))
	if bucketIndex >= len(buckets) {
		bucketIndex = len(buckets) - 1
	}
	bucket := &buckets[bucketIndex]
	candidate := indexedArchiveSample[T]{index: index, value: item, set: true}
	if !bucket.first.set {
		bucket.first = candidate
	}
	bucket.last = candidate
	for scoreIndex, scoreFunction := range scoreFunctions {
		score := scoreFunction(&item)
		if math.IsNaN(score) {
			continue
		}
		scoredCandidate := candidate
		scoredCandidate.score = score
		if !bucket.minima[scoreIndex].set || score < bucket.minima[scoreIndex].score {
			bucket.minima[scoreIndex] = scoredCandidate
		}
		if !bucket.maxima[scoreIndex].set || score > bucket.maxima[scoreIndex].score {
			bucket.maxima[scoreIndex] = scoredCandidate
		}
	}
}

func collectArchiveSamples[T any](buckets []archiveSampleBucket[T], maxItems int) []T {
	candidates := collectArchiveSampleCandidates(buckets, maxItems)
	indexes := make([]int, 0, len(candidates))
	for index := range candidates {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	items := make([]T, 0, len(indexes))
	for _, index := range indexes {
		items = append(items, candidates[index])
	}
	return items
}

func collectArchiveSampleCandidates[T any](buckets []archiveSampleBucket[T], maxItems int) map[int]T {
	candidates := make(map[int]T, maxItems)
	for _, bucket := range buckets {
		if bucket.first.set {
			candidates[bucket.first.index] = bucket.first.value
		}
		if bucket.last.set {
			candidates[bucket.last.index] = bucket.last.value
		}
		for extremaIndex := range bucket.minima {
			for _, candidate := range [...]indexedArchiveSample[T]{bucket.minima[extremaIndex], bucket.maxima[extremaIndex]} {
				if candidate.set {
					candidates[candidate.index] = candidate.value
				}
			}
		}
	}
	return candidates
}

func downsampleArchiveItems[T any](items []T, maxItems int, scoreFunctions ...func(*T) float64) ([]T, error) {
	boundedScores, err := boundedArchiveScoreFunctions(maxItems, scoreFunctions)
	if err != nil {
		return nil, err
	}
	if len(items) <= maxItems {
		return append([]T(nil), items...), nil
	}
	buckets := newArchiveSampleBuckets[T](maxItems, len(boundedScores))
	for index, item := range items {
		recordArchiveSample(buckets, len(items), index, item, boundedScores)
	}
	return collectArchiveSamples(buckets, maxItems), nil
}

// readJSONLDownsampled scans an archive artifact once and retains temporal
// endpoints plus independent per-bucket extrema. Unlike offset pagination it
// does not rescan the entire JSONL file for every chart window, and isolated
// spikes remain visible instead of falling between sampled windows.
func readJSONLDownsampled[T any](path string, totalHint, maxItems int, scoreFunctions ...func(*T) float64) ([]T, int, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()
	return readJSONLDownsampledFrom(context.Background(), file, totalHint, maxItems, scoreFunctions...)
}

func readJSONLDownsampledFrom[T any](ctx context.Context, reader io.Reader, totalHint, maxItems int, scoreFunctions ...func(*T) float64) ([]T, int, error) {
	boundedScores, err := boundedArchiveScoreFunctions(maxItems, scoreFunctions)
	if err != nil {
		return nil, 0, err
	}

	if totalHint < 1 {
		totalHint = maxItems
	}
	buckets := newArchiveSampleBuckets[T](maxItems, len(boundedScores))

	scanner := newArchiveJSONLScanner(reader)
	budget := newArchiveScanBudget(ctx, maxArchiveQueryRecords, maxArchiveQueryBytes)
	matched := 0
	for scanner.Scan() {
		if err := budget.Observe(scanner.Bytes()); err != nil {
			return nil, 0, err
		}
		var item T
		if err := json.Unmarshal(scanner.Bytes(), &item); err != nil {
			return nil, 0, err
		}
		sanitizeArchiveDecodedRecord(&item)
		recordArchiveSample(buckets, totalHint, matched, item, boundedScores)
		matched++
	}
	if err := scanner.Err(); err != nil {
		return nil, 0, err
	}
	return collectArchiveSamples(buckets, maxItems), matched, nil
}

func recordFirstFiniteIntervalTransactionIndexes(firstIndexByName map[string]int, item *IntervalSummaryRecord, index int) error {
	if item == nil {
		return nil
	}
	for name, metric := range item.Transactions {
		if metric == nil || math.IsNaN(metric.TPS) || math.IsInf(metric.TPS, 0) || math.IsNaN(metric.AvgMs) || math.IsInf(metric.AvgMs, 0) {
			continue
		}
		name = intervalTransactionName(firstNonEmpty(metric.Name, name))
		if _, exists := firstIndexByName[name]; exists {
			continue
		}
		if len(firstIndexByName) >= maxArchiveAnalysisDimensions {
			return fmt.Errorf("archive summaries exceed %d distinct transaction names", maxArchiveAnalysisDimensions)
		}
		firstIndexByName[name] = index
	}
	return nil
}

func mandatoryIntervalSummaryIndexes(firstIndexByName map[string]int, maxItems int) map[int]struct{} {
	mandatoryBudget := maxItems - 2
	if mandatoryBudget <= 0 {
		return map[int]struct{}{}
	}
	names := make([]string, 0, len(firstIndexByName))
	for name := range firstIndexByName {
		names = append(names, name)
	}
	sort.Strings(names)
	mandatoryIndexes := make(map[int]struct{}, min(len(names), mandatoryBudget))
	for _, name := range names {
		index := firstIndexByName[name]
		if _, exists := mandatoryIndexes[index]; exists {
			continue
		}
		if len(mandatoryIndexes) >= mandatoryBudget {
			break
		}
		mandatoryIndexes[index] = struct{}{}
	}
	return mandatoryIndexes
}

type intervalSummarySamplePlan struct {
	totalHint        int
	baseLimit        int
	scores           []func(*IntervalSummaryRecord) float64
	buckets          []archiveSampleBucket[IntervalSummaryRecord]
	mandatoryIndexes map[int]struct{}
	mandatoryItems   map[int]IntervalSummaryRecord
}

func newIntervalSummarySamplePlan(totalHint, maxItems int, firstIndexByName map[string]int) (*intervalSummarySamplePlan, error) {
	if maxItems < 2 {
		return nil, fmt.Errorf("sample limit must be at least 2")
	}
	mandatoryIndexes := mandatoryIntervalSummaryIndexes(firstIndexByName, maxItems)
	baseLimit := maxItems - len(mandatoryIndexes)
	scores, err := boundedArchiveScoreFunctions(baseLimit, archiveIntervalSummaryScores())
	if err != nil {
		return nil, err
	}
	if totalHint < 1 {
		totalHint = maxItems
	}
	return &intervalSummarySamplePlan{
		totalHint:        totalHint,
		baseLimit:        baseLimit,
		scores:           scores,
		buckets:          newArchiveSampleBuckets[IntervalSummaryRecord](baseLimit, len(scores)),
		mandatoryIndexes: mandatoryIndexes,
		mandatoryItems:   make(map[int]IntervalSummaryRecord, len(mandatoryIndexes)),
	}, nil
}

func (plan *intervalSummarySamplePlan) record(index int, item IntervalSummaryRecord) {
	recordArchiveSample(plan.buckets, plan.totalHint, index, item, plan.scores)
	if _, mandatory := plan.mandatoryIndexes[index]; mandatory {
		plan.mandatoryItems[index] = item
	}
}

func (plan *intervalSummarySamplePlan) items() []IntervalSummaryRecord {
	candidates := collectArchiveSampleCandidates(plan.buckets, plan.baseLimit)
	for mandatoryIndex, item := range plan.mandatoryItems {
		candidates[mandatoryIndex] = item
	}
	indexes := make([]int, 0, len(candidates))
	for candidateIndex := range candidates {
		indexes = append(indexes, candidateIndex)
	}
	sort.Ints(indexes)
	items := make([]IntervalSummaryRecord, 0, len(indexes))
	for _, candidateIndex := range indexes {
		items = append(items, candidates[candidateIndex])
	}
	return items
}

func downsampleIntervalSummaries(items []IntervalSummaryRecord, maxItems int) ([]IntervalSummaryRecord, error) {
	if maxItems < 2 {
		return nil, fmt.Errorf("sample limit must be at least 2")
	}
	firstIndexByName := make(map[string]int)
	for index := range items {
		if err := recordFirstFiniteIntervalTransactionIndexes(firstIndexByName, &items[index], index); err != nil {
			return nil, err
		}
	}
	if len(items) <= maxItems {
		return append([]IntervalSummaryRecord(nil), items...), nil
	}
	plan, err := newIntervalSummarySamplePlan(len(items), maxItems, firstIndexByName)
	if err != nil {
		return nil, err
	}
	for index, item := range items {
		plan.record(index, item)
	}
	return plan.items(), nil
}

// readIntervalSummariesDownsampledFrom reserves one sample for the first
// finite occurrence of each bounded transaction name, then uses the remaining
// budget for the ordinary temporal/extrema sampler. Without these mandatory
// samples a low-volume transaction could disappear from the reloaded chart
// entirely even though its interval metrics were durably archived.
func readIntervalSummariesDownsampledFrom(ctx context.Context, file *os.File, totalHint, maxItems int) ([]IntervalSummaryRecord, int, error) {
	if maxItems < 2 {
		return nil, 0, fmt.Errorf("sample limit must be at least 2")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	firstIndexByName := make(map[string]int)
	firstScanner := newArchiveJSONLScanner(file)
	firstBudget := newArchiveScanBudget(ctx, maxArchiveQueryRecords, maxArchiveQueryBytes)
	total := 0
	for firstScanner.Scan() {
		if err := firstBudget.Observe(firstScanner.Bytes()); err != nil {
			return nil, 0, err
		}
		var item IntervalSummaryRecord
		if err := json.Unmarshal(firstScanner.Bytes(), &item); err != nil {
			return nil, 0, err
		}
		sanitizeArchiveDecodedRecord(&item)
		if err := recordFirstFiniteIntervalTransactionIndexes(firstIndexByName, &item, total); err != nil {
			return nil, 0, err
		}
		total++
	}
	if err := firstScanner.Err(); err != nil {
		return nil, 0, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, 0, err
	}

	if totalHint < 1 {
		totalHint = max(total, maxItems)
	}
	plan, err := newIntervalSummarySamplePlan(totalHint, maxItems, firstIndexByName)
	if err != nil {
		return nil, 0, err
	}
	secondScanner := newArchiveJSONLScanner(file)
	secondBudget := newArchiveScanBudget(ctx, maxArchiveQueryRecords, maxArchiveQueryBytes)
	index := 0
	for secondScanner.Scan() {
		if err := secondBudget.Observe(secondScanner.Bytes()); err != nil {
			return nil, 0, err
		}
		var item IntervalSummaryRecord
		if err := json.Unmarshal(secondScanner.Bytes(), &item); err != nil {
			return nil, 0, err
		}
		sanitizeArchiveDecodedRecord(&item)
		plan.record(index, item)
		index++
	}
	if err := secondScanner.Err(); err != nil {
		return nil, 0, err
	}
	return plan.items(), index, nil
}

func archiveResourceSampleScores() []func(*ResourceSample) float64 {
	return []func(*ResourceSample) float64{
		// Safety-critical container signals are independent dimensions. Keeping
		// them ahead of general utilization also preserves them at small limits.
		func(sample *ResourceSample) float64 { return float64(sample.ContainerOOMKillCountDelta) },
		func(sample *ResourceSample) float64 { return sample.ContainerMemoryUsagePct },
		func(sample *ResourceSample) float64 {
			return math.Max(math.Max(sample.CPUProcessPct, 100-sample.CPUFreePct), math.Max(sample.ContainerCPUUsagePct, sample.CPUIOWaitPct+sample.CPUStealPct))
		},
		func(sample *ResourceSample) float64 { return math.Max(sample.MemProcessMB, sample.HeapAllocMB) },
		func(sample *ResourceSample) float64 {
			return sample.NetSentKbps + sample.NetRecvKbps + sample.NetDropInPerSec + sample.NetDropOutPerSec + sample.TCPRetransmitsPerSec
		},
		func(sample *ResourceSample) float64 {
			return sample.DiskReadKbps + sample.DiskWriteKbps + sample.DiskIOTimePct + sample.DiskWeightedIOTimePct
		},
		func(sample *ResourceSample) float64 { return float64(sample.ActiveVUsers) },
	}
}

func archiveIntervalSummaryScores() []func(*IntervalSummaryRecord) float64 {
	return []func(*IntervalSummaryRecord) float64{
		// Error rate and cumulative error count answer different questions and
		// must not compete for the same extrema slot.
		func(sample *IntervalSummaryRecord) float64 { return sample.ErrorPct },
		func(sample *IntervalSummaryRecord) float64 { return float64(sample.TotalErrors) },
		func(sample *IntervalSummaryRecord) float64 { return sample.TPS },
		func(sample *IntervalSummaryRecord) float64 {
			return archiveIntervalTransactionMax(sample, func(metric *IntervalTransactionMetrics) float64 { return metric.TPS })
		},
		func(sample *IntervalSummaryRecord) float64 {
			return archiveIntervalTransactionMax(sample, func(metric *IntervalTransactionMetrics) float64 { return metric.AvgMs })
		},
		func(sample *IntervalSummaryRecord) float64 {
			return archiveIntervalTransactionMax(sample, func(metric *IntervalTransactionMetrics) float64 {
				if metric.Count <= 0 {
					return 0
				}
				return float64(metric.Errors) / float64(metric.Count) * 100
			})
		},
		func(sample *IntervalSummaryRecord) float64 { return math.Abs(sample.ThroughputGapPct) },
		func(sample *IntervalSummaryRecord) float64 { return math.Max(sample.P99Ms, sample.AvgResponseMs) },
		func(sample *IntervalSummaryRecord) float64 { return float64(sample.ActiveVUsers) },
	}
}

func archiveIntervalTransactionMax(sample *IntervalSummaryRecord, value func(*IntervalTransactionMetrics) float64) float64 {
	if sample == nil || value == nil {
		return 0
	}
	var maximum float64
	for _, metric := range sample.Transactions {
		if metric != nil {
			maximum = math.Max(maximum, value(metric))
		}
	}
	return maximum
}

func loadArchiveResourceSamplesDownsampled(record *storage.RunArchiveRecord, limit int) ([]ResourceSample, int, error) {
	return loadArchiveResourceSamplesDownsampledContext(context.Background(), record, limit)
}

func loadArchiveResourceSamplesDownsampledContext(ctx context.Context, record *storage.RunArchiveRecord, limit int) ([]ResourceSample, int, error) {
	lease, err := acquireArchiveMaterialized(ctx, record)
	if err != nil {
		return nil, 0, err
	}
	defer lease.Release()
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		return nil, 0, err
	}
	file, _, err := openArchiveArtifactForRead(record.ArchivePath, manifest.ResourceSamplesFile)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()
	return readJSONLDownsampledFrom(ctx, file, int(manifest.ResourceSampleCount), limit, archiveResourceSampleScores()...)
}

func loadArchiveIntervalSummariesDownsampled(record *storage.RunArchiveRecord, limit int) ([]IntervalSummaryRecord, int, error) {
	return loadArchiveIntervalSummariesDownsampledContext(context.Background(), record, limit)
}

func loadArchiveIntervalSummariesDownsampledContext(ctx context.Context, record *storage.RunArchiveRecord, limit int) ([]IntervalSummaryRecord, int, error) {
	lease, err := acquireArchiveMaterialized(ctx, record)
	if err != nil {
		return nil, 0, err
	}
	defer lease.Release()
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		return nil, 0, err
	}
	file, _, err := openArchiveArtifactForRead(record.ArchivePath, manifest.IntervalSummariesFile)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()
	return readIntervalSummariesDownsampledFrom(ctx, file, int(manifest.IntervalSummaryCount), limit)
}

func loadArchiveIntervalSummariesForFiltersDownsampled(record *storage.RunArchiveRecord, limit int, action string, groupFilter groupFilterQuery) ([]IntervalSummaryRecord, int, error) {
	return loadArchiveIntervalSummariesForFiltersDownsampledContext(context.Background(), record, limit, action, groupFilter)
}

func loadArchiveIntervalSummariesForFiltersDownsampledContext(ctx context.Context, record *storage.RunArchiveRecord, limit int, action string, groupFilter groupFilterQuery) ([]IntervalSummaryRecord, int, error) {
	summaries, err := loadArchiveIntervalSummariesForFiltersContext(ctx, record, action, groupFilter)
	if err != nil {
		return nil, 0, err
	}
	total := len(summaries)
	items, err := downsampleIntervalSummaries(summaries, limit)
	if err != nil {
		return nil, 0, err
	}
	return items, total, nil
}
