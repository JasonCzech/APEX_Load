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
	runs, skipped, warnings, err := s.collectApplicationLoadComparisonRuns(r.Context(), applicationID, selectedTestIDs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	response := buildLoadComparison(applicationID, scope, r.URL.Query().Get("key"), runs)
	response.SkippedRuns = skipped
	response.Warnings = warnings
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

func (s *Server) collectApplicationLoadComparisonRuns(ctx context.Context, applicationID string, selectedTestIDs map[string]struct{}) ([]loadComparisonRun, []LoadComparisonSkippedRun, []string, error) {
	tests := s.runner.ListTestSummariesByContext(applicationID, "", "")
	runs := make([]loadComparisonRun, 0, len(tests))
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
		transactionStats, err := streamLoadComparisonTransactionAggregates(ctx, record, summaries)
		if err != nil {
			if ctx.Err() != nil {
				return nil, nil, nil, ctx.Err()
			}
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
		warnings = append(warnings, "one or more unreadable run archives were omitted from this comparison")
		if skippedTotal > len(skipped) {
			warnings = append(warnings, "skipped run details were truncated")
		}
	}
	return runs, skipped, warnings, nil
}

func streamLoadComparisonTransactionAggregates(ctx context.Context, record *storage.RunArchiveRecord, summaries []IntervalSummaryRecord) (map[string]*loadComparisonTransactionAggregate, error) {
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
			interval = &transactionIntervalAccumulator{}
			aggregate.Intervals[intervalKey] = interval
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
