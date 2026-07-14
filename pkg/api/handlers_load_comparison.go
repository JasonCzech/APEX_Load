package api

import (
	"context"
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
	runs, err := s.collectApplicationLoadComparisonRuns(r.Context(), applicationID, selectedTestIDs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, buildLoadComparison(applicationID, scope, r.URL.Query().Get("key"), runs))
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

func (s *Server) collectApplicationLoadComparisonRuns(ctx context.Context, applicationID string, selectedTestIDs map[string]struct{}) ([]loadComparisonRun, error) {
	tests := s.runner.ListTestsByContext(applicationID, "", "")
	runs := make([]loadComparisonRun, 0, len(tests))
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
		lease, err := acquireArchiveMaterialized(ctx, record)
		if err != nil {
			return nil, err
		}
		transactions, err := readAllArchiveTransactions(record)
		if err != nil {
			lease.Release()
			return nil, err
		}
		summaries, err := readAllArchiveSummaries(record)
		lease.Release()
		if err != nil {
			return nil, err
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
			Transactions:     transactions,
			Summaries:        summaries,
		})
	}
	return runs, nil
}
