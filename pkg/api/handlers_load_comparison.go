package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/jasonczech/apexload/pkg/storage"
)

func (s *Server) handleGetApplicationLoadComparison(w http.ResponseWriter, r *http.Request) {
	applicationID := strings.TrimSpace(r.PathValue("id"))
	scope, ok := normalizeLoadComparisonScope(r.URL.Query().Get("scope"))
	if !ok {
		writeError(w, http.StatusBadRequest, "unsupported load comparison scope")
		return
	}
	selectedTestIDs := parseLoadComparisonTestIDFilter(r.URL.Query())
	runs, skipped, warnings, err := s.collectApplicationLoadComparisonRuns(r.Context(), applicationID, scope, selectedTestIDs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	response := buildLoadComparison(applicationID, scope, r.URL.Query().Get("key"), runs)
	response.SkippedRuns = skipped
	response.Warnings = append(response.Warnings, warnings...)
	writeJSON(w, http.StatusOK, response)
}

func parseLoadComparisonTestIDFilter(query url.Values) map[string]struct{} {
	values := append([]string{}, query["test_id"]...)
	values = append(values, query["test_ids"]...)
	if len(values) == 0 {
		return nil
	}
	ids := make(map[string]struct{})
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			id := strings.TrimSpace(part)
			if id != "" {
				ids[id] = struct{}{}
			}
		}
	}
	if len(ids) == 0 {
		return nil
	}
	return ids
}

func loadComparisonTestIDSelected(testID string, selectedTestIDs map[string]struct{}) bool {
	if len(selectedTestIDs) == 0 {
		return true
	}
	_, ok := selectedTestIDs[strings.TrimSpace(testID)]
	return ok
}

// loadComparisonAggregateBudget bounds the aggregate state retained across the
// whole request. Per-run stream limits alone are insufficient because runs are
// all kept until buildLoadComparison finishes.
type loadComparisonAggregateBudget struct {
	maxDimensions int
	maxCells      int
	maxBytes      int64
	dimensions    map[string]struct{}
	cells         int
	bytes         int64
}

func newLoadComparisonAggregateBudget(maxDimensions, maxCells int) *loadComparisonAggregateBudget {
	return newLoadComparisonAggregateBudgetWithLimits(maxDimensions, maxCells, maxArchiveQueryBytes)
}

func newLoadComparisonAggregateBudgetWithLimits(maxDimensions, maxCells int, maxBytes int64) *loadComparisonAggregateBudget {
	return &loadComparisonAggregateBudget{
		maxDimensions: maxDimensions,
		maxCells:      maxCells,
		maxBytes:      maxBytes,
		dimensions:    make(map[string]struct{}),
	}
}

func (b *loadComparisonAggregateBudget) reserveRun(scope string, summaries []IntervalSummaryRecord, transactionStats map[string]*loadComparisonTransactionAggregate) error {
	if b == nil || b.maxDimensions <= 0 || b.maxCells <= 0 || b.maxBytes <= 0 {
		return fmt.Errorf("load comparison aggregate limits must be positive")
	}
	if b.dimensions == nil {
		b.dimensions = make(map[string]struct{})
	}
	newDimensions := make(map[string]struct{})
	cells := 0
	retainedBytes := int64(0)
	if len(summaries) > 0 {
		retainedBytes = archiveJSONWeight(&summaries)
		if retainedBytes < 0 {
			return fmt.Errorf("measure load comparison summary state")
		}
	}
	addBytes := func(count int64) error {
		if count < 0 || count > b.maxBytes-b.bytes-retainedBytes {
			return fmt.Errorf("load comparison exceeds %d aggregate bytes across selected runs", b.maxBytes)
		}
		retainedBytes += count
		return nil
	}
	if retainedBytes > b.maxBytes-b.bytes {
		return fmt.Errorf("load comparison exceeds %d aggregate bytes across selected runs", b.maxBytes)
	}
	addCells := func(count int) error {
		if count < 0 || count > b.maxCells-b.cells-cells {
			return fmt.Errorf("load comparison exceeds %d aggregate cells across selected runs", b.maxCells)
		}
		cells += count
		return nil
	}
	addDimension := func(key string) error {
		key = strings.TrimSpace(key)
		if key == "" {
			return nil
		}
		identity := scope + "\x00" + key
		if _, exists := b.dimensions[identity]; exists {
			return nil
		}
		if _, exists := newDimensions[identity]; exists {
			return nil
		}
		if len(b.dimensions)+len(newDimensions) >= b.maxDimensions {
			return fmt.Errorf("load comparison exceeds %d aggregate dimensions across selected runs", b.maxDimensions)
		}
		newDimensions[identity] = struct{}{}
		return nil
	}
	// Every accepted run retains its summary slice until response assembly, even
	// when the selected scope has no matching groups/transactions. Count those
	// rows as cells so empty or sparse archives cannot bypass the request bound.
	if err := addCells(len(summaries)); err != nil {
		return err
	}

	switch scope {
	case LoadComparisonScopeGroup:
		for _, summary := range summaries {
			if err := addCells(len(summary.Groups)); err != nil {
				return err
			}
			for name, group := range summary.Groups {
				key := strings.TrimSpace(name)
				if group != nil && strings.TrimSpace(group.Name) != "" {
					key = strings.TrimSpace(group.Name)
				}
				if err := addDimension(key); err != nil {
					return err
				}
			}
		}
	case LoadComparisonScopeTransaction:
		// Each per-run aggregate map entry is retained independently even when its
		// identity also appeared in another run. Charge that fixed state as a cell;
		// only the distinct option identity is unioned in the dimension budget.
		if err := addCells(len(transactionStats)); err != nil {
			return err
		}
		for key, aggregate := range transactionStats {
			key = strings.TrimSpace(key)
			if key != "" {
				if err := addDimension(key); err != nil {
					return err
				}
				if err := addBytes(int64(len(key))); err != nil {
					return err
				}
			}
			if aggregate != nil {
				if err := addBytes(int64(len(aggregate.SourceKind))); err != nil {
					return err
				}
				if err := addCells(len(aggregate.Intervals)); err != nil {
					return err
				}
			}
		}
	default:
		return fmt.Errorf("unsupported load comparison scope %q", scope)
	}
	for identity := range newDimensions {
		b.dimensions[identity] = struct{}{}
	}
	b.cells += cells
	b.bytes += retainedBytes
	return nil
}

func (s *Server) collectApplicationLoadComparisonRuns(ctx context.Context, applicationID string, scope string, selectedTestIDs map[string]struct{}) ([]loadComparisonRun, []LoadComparisonSkippedRun, []string, error) {
	tests := s.runner.ListTestSummariesByContext(applicationID, "", "")
	runs := make([]loadComparisonRun, 0, len(tests))
	aggregateBudget := newLoadComparisonAggregateBudget(maxArchiveAnalysisDimensions, maxArchiveReportAggregateCells)
	const maxSkippedRunDetails = 100
	skipped := make([]LoadComparisonSkippedRun, 0)
	var skippedTotal int
	skip := func(testID string, err error) {
		skippedTotal++
		if len(skipped) < maxSkippedRunDetails {
			skipped = append(skipped, LoadComparisonSkippedRun{TestID: testID, Reason: err.Error()})
		}
	}
	releases := make(map[string]*storage.Release)
	for _, test := range tests {
		if test == nil || strings.TrimSpace(test.ReleaseID) == "" {
			continue
		}
		if !loadComparisonTestIDSelected(test.ID, selectedTestIDs) {
			continue
		}
		record, ok := s.runner.GetRunArchiveContext(ctx, test.ID)
		if !ok {
			continue
		}
		summaries, err := readAllArchiveSummariesContext(ctx, record)
		if err != nil {
			if ctx.Err() != nil {
				return nil, nil, nil, ctx.Err()
			}
			skip(test.ID, err)
			continue
		}
		var transactionStats map[string]*loadComparisonTransactionAggregate
		if scope == LoadComparisonScopeTransaction {
			transactionStats, err = streamLoadComparisonTransactionAggregates(ctx, record, summaries)
			if err != nil {
				if ctx.Err() != nil {
					return nil, nil, nil, ctx.Err()
				}
				skip(test.ID, err)
				continue
			}
		}
		if err := aggregateBudget.reserveRun(scope, summaries, transactionStats); err != nil {
			skip(test.ID, err)
			continue
		}
		releaseName := test.ReleaseID
		var releaseCreatedAt = test.CreatedAt
		if release, ok := releases[test.ReleaseID]; ok {
			if release != nil {
				releaseName = firstNonEmpty(release.Name, release.ID)
				releaseCreatedAt = release.CreatedAt
			}
		} else if s.store != nil {
			release, err := s.store.GetRelease(test.ReleaseID)
			if err == nil && release != nil {
				releases[test.ReleaseID] = release
				releaseName = firstNonEmpty(release.Name, release.ID)
				releaseCreatedAt = release.CreatedAt
			} else {
				releases[test.ReleaseID] = nil
			}
		}
		runs = append(runs, loadComparisonRun{
			Test:             test,
			ReleaseID:        test.ReleaseID,
			ReleaseName:      releaseName,
			ReleaseCreatedAt: releaseCreatedAt,
			TransactionStats: transactionStats,
			Summaries:        summaries,
		})
	}
	warnings := []string(nil)
	if skippedTotal > 0 {
		warnings = append(warnings, "one or more run archives were omitted from this comparison")
		if skippedTotal > len(skipped) {
			warnings = append(warnings, "skipped run details were truncated")
		}
	}
	return runs, skipped, warnings, nil
}

func streamLoadComparisonTransactionAggregates(ctx context.Context, record *storage.RunArchiveRecord, summaries []IntervalSummaryRecord) (map[string]*loadComparisonTransactionAggregate, error) {
	return streamLoadComparisonTransactionAggregatesWithLimits(
		ctx, record, summaries, maxArchiveAnalysisDimensions, maxArchiveReportAggregateCells,
	)
}

func streamLoadComparisonTransactionAggregatesWithLimits(ctx context.Context, record *storage.RunArchiveRecord, summaries []IntervalSummaryRecord, maxDimensions int, maxCells int) (map[string]*loadComparisonTransactionAggregate, error) {
	if maxDimensions <= 0 || maxCells <= 0 {
		return nil, fmt.Errorf("load comparison aggregate limits must be positive")
	}
	lease, err := acquireArchiveMaterialized(ctx, record)
	if err != nil {
		return nil, err
	}
	defer lease.Release()
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		return nil, err
	}
	file, _, err := openArchiveArtifactForRead(record.ArchivePath, manifest.TransactionsFile)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	aggregates := make(map[string]*loadComparisonTransactionAggregate)
	aggregateCells := 0
	scanner := newArchiveJSONLScanner(file)
	budget := newArchiveScanBudget(ctx, maxArchiveQueryRecords, maxArchiveQueryBytes)
	for scanner.Scan() {
		if err := budget.Observe(scanner.Bytes()); err != nil {
			return nil, err
		}
		var tx TransactionEvent
		if err := json.Unmarshal(scanner.Bytes(), &tx); err != nil {
			return nil, fmt.Errorf("decode archived transaction: %w", err)
		}
		key, sourceKind := loadComparisonTransactionIdentity(tx)
		if key == "" {
			continue
		}
		aggregate := aggregates[key]
		if aggregate == nil {
			if len(aggregates) >= maxDimensions {
				return nil, fmt.Errorf("load comparison exceeds %d distinct transaction dimensions", maxDimensions)
			}
			aggregate = &loadComparisonTransactionAggregate{SourceKind: sourceKind, Intervals: make(map[transactionIntervalLoadKey]*transactionIntervalAccumulator)}
			aggregates[key] = aggregate
		}
		if aggregate.SourceKind != sourceKind && aggregate.SourceKind != "source_transaction" {
			aggregate.SourceKind = sourceKind
		}
		aggregate.TransactionCount++
		index := archiveSummaryBucketIndex(summaries, tx.Timestamp)
		if index < 0 || index >= len(summaries) {
			continue
		}
		load := transactionLoadForSummary(tx, summaries[index])
		if load <= 0 {
			continue
		}
		intervalKey := transactionIntervalLoadKey{Index: index, Load: load}
		interval := aggregate.Intervals[intervalKey]
		if interval == nil {
			if aggregateCells >= maxCells {
				return nil, fmt.Errorf("load comparison exceeds %d transaction interval cells", maxCells)
			}
			interval = &transactionIntervalAccumulator{}
			aggregate.Intervals[intervalKey] = interval
			aggregateCells++
		}
		interval.Transactions++
		interval.DurationTotal += tx.DurationMs
		if !tx.Success {
			interval.Errors++
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan archived transactions: %w", err)
	}
	return aggregates, nil
}
