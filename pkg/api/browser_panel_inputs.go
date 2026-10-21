package api

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func browserPanelRunInput(series NativeArchiveSeries) (storage.BrowserPanelRunInput, error) {
	if series.BrowserTuples == nil {
		return storage.BrowserPanelRunInput{}, errors.New("separate browser tuples unavailable")
	}
	input := storage.BrowserPanelRunInput{RunID: series.RunID, ManifestSHA256: series.archiveManifestSHA256, TupleIDs: []string{}}
	for _, tuple := range series.BrowserTuples.Tuples {
		input.TupleIDs = append(input.TupleIDs, tuple.ID)
	}
	slices.Sort(input.TupleIDs)
	return input, nil
}

func sameBrowserPanelRunInput(a, b storage.BrowserPanelRunInput) bool {
	return a.RunID == b.RunID && a.ManifestSHA256 == b.ManifestSHA256 && slices.Equal(a.TupleIDs, b.TupleIDs)
}

// bindBrowserPanelInputs records only the publications actually read under the
// native-series materialization leases. Pins never assert baseline eligibility.
func bindBrowserPanelInputs(q storage.AnalyticsQuery, response *NativeArchiveResponse) error {
	if err := validateBrowserPanelUnion(q, response); err != nil {
		return err
	}
	if q.QueryVersion != 4 {
		return nil
	}
	current, err := browserPanelRunInput(response.Current)
	if err != nil {
		return err
	}
	pins := &storage.BrowserPanelInputs{Version: 1, QuerySHA256: storage.BrowserPanelQueryDigest(q), Current: current}
	if response.Baseline != nil {
		baseline, err := browserPanelRunInput(*response.Baseline)
		if err != nil {
			return err
		}
		pins.Baseline = &baseline
	}
	checked := q
	checked.BrowserInputs = pins
	if err := storage.ValidateNativeArchiveQuery(checked); err != nil {
		return errors.New("browser input publication identity unavailable")
	}
	if expected := q.BrowserInputs; expected != nil {
		if !sameBrowserPanelRunInput(expected.Current, pins.Current) || (expected.Baseline == nil) != (pins.Baseline == nil) || expected.Baseline != nil && !sameBrowserPanelRunInput(*expected.Baseline, *pins.Baseline) {
			return errors.New("saved browser inputs changed or are unavailable; preview and explicitly save new inputs")
		}
	}
	response.BrowserInputs = pins
	return nil
}

// A save validates at most 20 frozen browser queries within one shared analysis
// admission and deadline. Changes after this read remain detectable on reopen;
// this is not a transaction spanning archive publications and dashboard storage.
func (s *Server) validateSavedBrowserPanelInputs(w http.ResponseWriter, r *http.Request, d *storage.DashboardDefinition) bool {
	queries := []storage.AnalyticsQuery{}
	for _, tab := range d.Tabs {
		for _, panel := range tab.Panels {
			if panel.Query == nil {
				continue
			}
			q := *panel.Query
			if q.BrowserInputs == nil && q.QueryVersion != 4 {
				continue
			}
			if q.BrowserInputs == nil || storage.ValidateNativeArchiveQuery(q) != nil {
				writeError(w, 400, "saved browser panels require valid evaluated input pins")
				return false
			}
			queries = append(queries, q)
			if len(queries) > 20 {
				writeError(w, 400, "at most 20 frozen browser panels may be saved together")
				return false
			}
		}
	}
	if len(queries) == 0 {
		return true
	}
	if !admitAnalysisPipeline(w) {
		return false
	}
	defer func() { <-analysisPipelineJobs }()
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	archives := s.archiveDomainService()
	load := func(id string, q storage.AnalyticsQuery) (NativeArchiveSeries, error) {
		test, ok := archives.GetTest(id)
		if !ok || normalizeProjectID(test.ProjectID) != normalizeProjectID(d.ProjectID) {
			return NativeArchiveSeries{}, errors.New("input run unavailable")
		}
		record, ok := archives.GetRunArchiveContext(ctx, id)
		if !ok {
			return NativeArchiveSeries{}, errors.New("input archive unavailable")
		}
		return buildNativeArchiveSeries(ctx, record, q)
	}
	for _, q := range queries {
		current, err := load(q.BrowserInputs.Current.RunID, q)
		if err != nil {
			writeError(w, 400, "pinned browser inputs are unavailable or changed")
			return false
		}
		response := NativeArchiveResponse{Current: current}
		if q.BrowserInputs.Baseline != nil {
			baseline, err := load(q.BrowserInputs.Baseline.RunID, q)
			if err != nil {
				writeError(w, 400, "pinned browser inputs are unavailable or changed")
				return false
			}
			response.Baseline = &baseline
		}
		if bindBrowserPanelInputs(q, &response) != nil {
			writeError(w, 400, "pinned browser inputs are unavailable or changed")
			return false
		}
	}
	return true
}

// Bound the union rendered by a comparison, including baseline-only tuples.
// Matched tuples share one row per offset with current/baseline columns.
func validateBrowserPanelUnion(q storage.AnalyticsQuery, response *NativeArchiveResponse) error {
	if q.BrowserTupleMode != "separate" {
		return nil
	}
	limit := q.MaxSeries
	if limit <= 0 {
		limit = 20
	}
	limit = min(limit, 20)
	rows := make(map[string]int)
	inputs := []*NativeArchiveSeries{&response.Current}
	if response.Baseline != nil {
		inputs = append(inputs, response.Baseline)
	}
	for _, input := range inputs {
		if input.BrowserTuples == nil || input.BrowserTuples.Version != 1 {
			return errors.New("separate browser tuples unavailable")
		}
		for _, tuple := range input.BrowserTuples.Tuples {
			rows[tuple.ID] = max(rows[tuple.ID], len(tuple.Buckets))
			if len(rows) > limit {
				return errors.New("combined current/baseline browser tuple limit exceeded; narrow the filters")
			}
		}
	}
	total := 0
	for _, count := range rows {
		if count > 20000-total {
			return errors.New("combined current/baseline browser bucket limit exceeded; narrow the window or filters")
		}
		total += count
	}
	return nil
}
