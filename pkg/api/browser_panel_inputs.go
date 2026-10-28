package api

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func browserPanelPolicyPinsFromManifest(manifest *RunArchiveManifest) ([]storage.BrowserPanelPolicyPin, error) {
	if manifest == nil || len(manifest.Provenance.PolicyPins) == 0 {
		return nil, nil
	}
	byKind := map[string]storage.BrowserPanelPolicyPin{}
	for _, pin := range manifest.Provenance.PolicyPins {
		if pin.Kind != storage.AnalysisDefinitionKindBrowserPolicy && pin.Kind != storage.AnalysisDefinitionKindJourneyPolicy {
			continue
		}
		if pin.ID == "" || pin.Revision < 1 || len(pin.SHA256) != 64 {
			return nil, errors.New("archive manifest policy pin is malformed")
		}
		if _, dup := byKind[pin.Kind]; dup {
			return nil, errors.New("archive manifest contains duplicate policy pin kind")
		}
		byKind[pin.Kind] = storage.BrowserPanelPolicyPin{
			Kind:     pin.Kind,
			ID:       pin.ID,
			Revision: pin.Revision,
			SHA256:   strings.ToLower(pin.SHA256),
		}
	}
	if len(byKind) == 0 {
		return nil, nil
	}
	out := make([]storage.BrowserPanelPolicyPin, 0, len(byKind))
	for _, pin := range byKind {
		out = append(out, pin)
	}
	slices.SortFunc(out, func(a, b storage.BrowserPanelPolicyPin) int {
		return cmp.Or(cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.ID, b.ID))
	})
	return out, nil
}

func (s *Server) verifyAndPopulateBrowserPanelV2(ctx context.Context, projectID string, q storage.AnalyticsQuery, response *NativeArchiveResponse) error {
	if q.QueryVersion != 5 || response == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	defStore, hasDefStore := s.store.(storage.AnalysisDefinitionStore)
	verifyPins := func(pins []storage.BrowserPanelPolicyPin) error {
		if len(pins) == 0 {
			return nil
		}
		if !hasDefStore {
			return errors.New("analysis policy store unavailable")
		}
		for _, pin := range pins {
			revDef, err := defStore.GetAnalysisDefinition(projectID, pin.ID, pin.Revision)
			if err != nil || revDef == nil || revDef.Kind != pin.Kind || revDef.Deleted {
				return errors.New("pinned analysis policy revision is unavailable or deleted")
			}
			sum := sha256.Sum256(revDef.Definition)
			if hex.EncodeToString(sum[:]) != pin.SHA256 {
				return errors.New("pinned analysis policy digest mismatch")
			}
			latestDef, err := defStore.GetAnalysisDefinition(projectID, pin.ID, 0)
			if err != nil || latestDef == nil || latestDef.Deleted {
				return errors.New("pinned analysis policy has been deleted")
			}
		}
		return nil
	}
	if err := verifyPins(response.Current.policyPins); err != nil {
		return err
	}
	if response.Baseline != nil {
		if err := verifyPins(response.Baseline.policyPins); err != nil {
			return err
		}
	}
	if rcaStore, ok := s.store.(storage.RCAStore); ok && response.Current.manifest != nil {
		if archiveDigest, err := rcaEvidenceDigest(response.Current.manifest); err == nil {
			var candidates []*storage.RCAAnalysis
			if reader, ok := rcaStore.(completedRCAAnalysesReader); ok {
				candidates, _ = reader.CompletedRCAAnalyses(projectID, response.Current.RunID)
			}
			if len(candidates) == 0 {
				if latest, err := rcaStore.LatestCompletedRCAAnalysis(projectID, response.Current.RunID); err == nil && latest != nil {
					candidates = []*storage.RCAAnalysis{latest}
				}
			}
			for _, cand := range candidates {
				if cand == nil {
					continue
				}
				payload := rcaAnalysisPayloadOf(cand)
				if payload.Assessment == nil || payload.Inputs.Archive != archiveDigest || (payload.BrowserSelection != nil && validateRCABrowserSelection(payload) != nil) {
					continue
				}
				policyOK := true
				for _, pin := range response.Current.policyPins {
					if pin.Kind == storage.AnalysisDefinitionKindBrowserPolicy {
						policyOK = rcaBrowserBaselineMatchesPolicy(response.Current.manifest, payload, BrowserPolicyRef{SHA256: pin.SHA256})
						break
					}
				}
				rcaBaselineID := strings.TrimSpace(payload.Request.BaselineTestID)
				if rcaBaselineID == "" && payload.BrowserSelection != nil {
					rcaBaselineID = strings.TrimSpace(payload.BrowserSelection.SelectedTestID)
				}
				if rcaBaselineID == "" && payload.Assessment.BrowserBaseline != nil {
					rcaBaselineID = strings.TrimSpace(payload.Assessment.BrowserBaseline.Baseline.TestID)
				}
				baselineOK := q.ComparisonRunID == "" || rcaBaselineID == q.ComparisonRunID || (rcaBaselineID == "" && payload.BrowserSelection == nil)
				if policyOK && len(payload.Inputs.BrowserSelection) == 64 {
					if baselineOK {
						response.Current.selectionReceiptSHA256 = strings.ToLower(payload.Inputs.BrowserSelection)
					}
					break
				}
			}
		}
	}
	return nil
}

func browserPanelRunInput(series NativeArchiveSeries, version int) (storage.BrowserPanelRunInput, error) {
	if series.BrowserTuples == nil {
		return storage.BrowserPanelRunInput{}, errors.New("separate browser tuples unavailable")
	}
	input := storage.BrowserPanelRunInput{RunID: series.RunID, ManifestSHA256: series.archiveManifestSHA256, TupleIDs: []string{}}
	for _, tuple := range series.BrowserTuples.Tuples {
		input.TupleIDs = append(input.TupleIDs, tuple.ID)
	}
	slices.Sort(input.TupleIDs)
	if version == 2 && len(series.policyPins) > 0 {
		input.PolicyPins = slices.Clone(series.policyPins)
	}
	return input, nil
}

func sameBrowserPanelRunInput(a, b storage.BrowserPanelRunInput) bool {
	return a.RunID == b.RunID && a.ManifestSHA256 == b.ManifestSHA256 && slices.Equal(a.TupleIDs, b.TupleIDs) && slices.Equal(a.PolicyPins, b.PolicyPins)
}

// bindBrowserPanelInputs records only the publications actually read under the
// native-series materialization leases. Pins never assert baseline eligibility.
func bindBrowserPanelInputs(q storage.AnalyticsQuery, response *NativeArchiveResponse) error {
	if err := validateBrowserPanelUnion(q, response); err != nil {
		return err
	}
	if q.QueryVersion != 4 && q.QueryVersion != 5 {
		return nil
	}
	pinVersion := 1
	if q.QueryVersion == 5 {
		pinVersion = 2
	}
	current, err := browserPanelRunInput(response.Current, pinVersion)
	if err != nil {
		return err
	}
	pins := &storage.BrowserPanelInputs{Version: pinVersion, QuerySHA256: storage.BrowserPanelQueryDigest(q), Current: current}
	if pinVersion == 2 {
		if len(current.PolicyPins) > 0 {
			pins.PolicyPins = slices.Clone(current.PolicyPins)
		}
		pins.SelectionReceiptSHA256 = response.Current.selectionReceiptSHA256
	}
	if response.Baseline != nil {
		baseline, err := browserPanelRunInput(*response.Baseline, pinVersion)
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
		if expected.Version != pins.Version || !slices.Equal(expected.PolicyPins, pins.PolicyPins) || expected.SelectionReceiptSHA256 != pins.SelectionReceiptSHA256 || !sameBrowserPanelRunInput(expected.Current, pins.Current) || (expected.Baseline == nil) != (pins.Baseline == nil) || expected.Baseline != nil && !sameBrowserPanelRunInput(*expected.Baseline, *pins.Baseline) {
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
			if q.BrowserInputs == nil && q.QueryVersion != 4 && q.QueryVersion != 5 {
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
	load := func(id string, q storage.AnalyticsQuery) (NativeArchiveSeries, error) {
		reader, ok := s.store.(interface {
			GetTestContext(context.Context, string) (*storage.TestRecord, error)
			GetRunArchiveContext(context.Context, string) (*storage.RunArchiveRecord, error)
		})
		if !ok {
			return NativeArchiveSeries{}, errors.New("input identity reader unavailable")
		}
		test, err := reader.GetTestContext(ctx, id)
		if err != nil || test == nil || test.ID != id || normalizeProjectID(test.ProjectID) != normalizeProjectID(d.ProjectID) {
			return NativeArchiveSeries{}, errors.New("input run unavailable")
		}
		record, err := reader.GetRunArchiveContext(ctx, id)
		if err != nil || record == nil || record.TestID != id {
			return NativeArchiveSeries{}, errors.New("input archive unavailable")
		}
		return buildNativeArchiveSeriesForTest(ctx, record, q, test, s.store)
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
		if s.verifyAndPopulateBrowserPanelV2(ctx, normalizeProjectID(d.ProjectID), q, &response) != nil {
			writeError(w, 400, "pinned browser inputs are unavailable or changed")
			return false
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
