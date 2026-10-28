package api

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	if q.QueryVersion != 5 || q.PanelInputs != nil || response == nil {
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
				if payload.Assessment == nil || payload.Inputs.Archive != archiveDigest || validateRCABrowserSelection(payload) != nil {
					continue
				}
				policyOK := true
				for _, pin := range response.Current.policyPins {
					if pin.Kind == storage.AnalysisDefinitionKindBrowserPolicy {
						ref := BrowserPolicyRef{ID: pin.ID, Revision: pin.Revision, SHA256: pin.SHA256}
						var policy BrowserPolicyDefinition
						var loadedPolicy bool
						if hasDefStore {
							if revDef, revErr := defStore.GetAnalysisDefinition(projectID, pin.ID, pin.Revision); revErr == nil && revDef != nil && json.Unmarshal(revDef.Definition, &policy) == nil {
								if policy.Baseline.Mode == "pinned_run" && strings.TrimSpace(policy.Baseline.RunID) != "" && s.store != nil {
									if run, lookupErr := s.store.GetTest(strings.TrimSpace(policy.Baseline.RunID)); lookupErr == nil && run != nil && normalizeProjectID(run.ProjectID) == normalizeProjectID(projectID) {
										policy.Baseline.RunID = run.ID
									}
								}
								loadedPolicy = true
							}
						}
						if loadedPolicy {
							policyOK = rcaBrowserBaselineMatchesPolicyDefinition(response.Current.manifest, payload, ref, policy)
						} else {
							policyOK = rcaBrowserBaselineMatchesPolicy(response.Current.manifest, payload, ref)
						}
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
	if (q.QueryVersion != 4 && q.QueryVersion != 5) || q.PanelInputs != nil {
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

func sameSavedPanelSourcePin(a, b storage.SavedPanelSourcePin) bool {
	return a.RunID == b.RunID &&
		a.ManifestSHA256 == b.ManifestSHA256 &&
		a.DatasetID == b.DatasetID &&
		a.DatasetArtifactSHA256 == b.DatasetArtifactSHA256 &&
		a.SourceID == b.SourceID &&
		a.Protocol == b.Protocol &&
		slices.Equal(a.PolicyPins, b.PolicyPins)
}

func sameSavedPanelInputs(a, b storage.SavedPanelInputs) bool {
	if a.Version != b.Version ||
		a.CatalogVersion != b.CatalogVersion ||
		a.Alignment != b.Alignment ||
		a.CoveragePolicy != b.CoveragePolicy ||
		a.Population != b.Population ||
		a.Metric != b.Metric ||
		a.Aggregation != b.Aggregation ||
		a.QuerySHA256 != b.QuerySHA256 ||
		a.ComparisonID != b.ComparisonID ||
		a.ComparisonRevision != b.ComparisonRevision ||
		a.ComparisonSHA256 != b.ComparisonSHA256 ||
		a.Drilldown != b.Drilldown ||
		!slices.Equal(a.PolicyPins, b.PolicyPins) ||
		!sameSavedPanelSourcePin(a.Current, b.Current) {
		return false
	}
	if (a.Baseline == nil) != (b.Baseline == nil) || (a.Baseline != nil && !sameSavedPanelSourcePin(*a.Baseline, *b.Baseline)) {
		return false
	}
	if (a.Overlay == nil) != (b.Overlay == nil) || (a.Overlay != nil && !sameSavedPanelSourcePin(*a.Overlay, *b.Overlay)) {
		return false
	}
	return true
}

func buildSavedPanelInputsFromArchive(q storage.AnalyticsQuery, response *NativeArchiveResponse) *storage.SavedPanelInputs {
	if response == nil || len(response.Current.archiveManifestSHA256) != 64 {
		return nil
	}
	norm := q
	storage.NormalizeSavedPanelQueryDefaults(&norm)
	protocol := ""
	if norm.Filters != nil {
		protocol = norm.Filters["protocol"]
	}
	current := storage.SavedPanelSourcePin{
		RunID:          response.Current.RunID,
		ManifestSHA256: strings.ToLower(response.Current.archiveManifestSHA256),
		Protocol:       protocol,
	}
	if len(response.Current.policyPins) > 0 {
		current.PolicyPins = slices.Clone(response.Current.policyPins)
	}
	pins := &storage.SavedPanelInputs{
		Version:        2,
		CatalogVersion: norm.CatalogVersion,
		Alignment:      norm.Alignment,
		CoveragePolicy: norm.CoveragePolicy,
		Population:     norm.Population,
		Metric:         norm.Metric,
		Aggregation:    norm.Aggregation,
		QuerySHA256:    storage.SavedPanelQueryDigest(norm),
		PolicyPins:     slices.Clone(current.PolicyPins),
		Drilldown: storage.SavedPanelDrilldownBounds{
			StartSeconds:  norm.StartSeconds,
			EndSeconds:    norm.EndSeconds,
			BucketSeconds: norm.BucketSeconds,
			MaxSeries:     norm.MaxSeries,
		},
		Current: current,
	}
	if response.Baseline != nil && len(response.Baseline.archiveManifestSHA256) == 64 {
		base := storage.SavedPanelSourcePin{
			RunID:          response.Baseline.RunID,
			ManifestSHA256: strings.ToLower(response.Baseline.archiveManifestSHA256),
			Protocol:       protocol,
		}
		if len(response.Baseline.policyPins) > 0 {
			base.PolicyPins = slices.Clone(response.Baseline.policyPins)
		}
		pins.Baseline = &base
	} else if q.PanelInputs != nil && q.PanelInputs.Baseline != nil && q.PanelInputs.Baseline.DatasetID != "" {
		baseCopy := *q.PanelInputs.Baseline
		baseCopy.PolicyPins = slices.Clone(q.PanelInputs.Baseline.PolicyPins)
		pins.Baseline = &baseCopy
	}
	if q.PanelInputs != nil {
		pins.ComparisonID = q.PanelInputs.ComparisonID
		pins.ComparisonRevision = q.PanelInputs.ComparisonRevision
		pins.ComparisonSHA256 = q.PanelInputs.ComparisonSHA256
		pins.Drilldown.MaxLagSeconds = q.PanelInputs.Drilldown.MaxLagSeconds
		if q.PanelInputs.Current.SourceID != "" {
			pins.Current.SourceID = q.PanelInputs.Current.SourceID
		}
		if pins.Baseline != nil && q.PanelInputs.Baseline != nil {
			if q.PanelInputs.Baseline.SourceID != "" {
				pins.Baseline.SourceID = q.PanelInputs.Baseline.SourceID
			}
			if q.PanelInputs.Baseline.Protocol != "" {
				pins.Baseline.Protocol = q.PanelInputs.Baseline.Protocol
			}
		}
		if q.PanelInputs.Overlay != nil {
			overlayCopy := *q.PanelInputs.Overlay
			overlayCopy.PolicyPins = slices.Clone(q.PanelInputs.Overlay.PolicyPins)
			pins.Overlay = &overlayCopy
		}
	}
	return pins
}

func (s *Server) verifySavedPanelPolicyPins(projectID string, pins []storage.BrowserPanelPolicyPin) error {
	if len(pins) == 0 {
		return nil
	}
	defStore, hasDefStore := s.store.(storage.AnalysisDefinitionStore)
	if !hasDefStore {
		return errors.New("stale: analysis policy store unavailable")
	}
	for _, pin := range pins {
		revDef, err := defStore.GetAnalysisDefinition(projectID, pin.ID, pin.Revision)
		if err != nil || revDef == nil || revDef.Kind != pin.Kind || revDef.Deleted {
			return errors.New("stale: pinned analysis policy revision is unavailable or deleted")
		}
		sum := sha256.Sum256(revDef.Definition)
		if hex.EncodeToString(sum[:]) != pin.SHA256 {
			return errors.New("stale: pinned analysis policy digest mismatch")
		}
		latestDef, err := defStore.GetAnalysisDefinition(projectID, pin.ID, 0)
		if err != nil || latestDef == nil || latestDef.Deleted {
			return errors.New("stale: pinned analysis policy has been deleted")
		}
	}
	return nil
}

func (s *Server) verifySavedPanelComparisonPin(projectID string, pins *storage.SavedPanelInputs) (string, string) {
	if pins == nil || pins.ComparisonID == "" {
		return "ready", ""
	}
	defStore, ok := s.store.(storage.AnalysisDefinitionStore)
	if !ok {
		return "stale", "analysis definition store unavailable"
	}
	revDef, err := defStore.GetAnalysisDefinition(projectID, pins.ComparisonID, pins.ComparisonRevision)
	if err != nil || revDef == nil || revDef.Kind != "comparison" || revDef.Deleted {
		return "missing_input", "pinned comparison definition was deleted or is unavailable"
	}
	latestDef, err := defStore.GetAnalysisDefinition(projectID, pins.ComparisonID, 0)
	if err != nil || latestDef == nil || latestDef.Deleted {
		return "missing_input", "pinned comparison definition was deleted"
	}
	sum := sha256.Sum256(revDef.Definition)
	if hex.EncodeToString(sum[:]) != pins.ComparisonSHA256 {
		return "stale", "pinned comparison definition digest changed"
	}
	if latestDef.Revision != pins.ComparisonRevision {
		latestSum := sha256.Sum256(latestDef.Definition)
		if hex.EncodeToString(latestSum[:]) != pins.ComparisonSHA256 {
			return "stale", "pinned comparison definition was modified in a newer revision"
		}
	}
	return "ready", ""
}

func (s *Server) verifySavedPanelSourcePin(ctx context.Context, projectID string, pin storage.SavedPanelSourcePin) (string, string) {
	if err := ctx.Err(); err != nil {
		return "stale", err.Error()
	}
	if pin.DatasetID != "" {
		imports, ok := s.store.(storage.AnalysisImportStore)
		if !ok {
			return "missing_input", "dataset storage unavailable"
		}
		in, err := imports.GetAnalysisImport(pin.DatasetID)
		if err != nil || in == nil || normalizeProjectID(in.ProjectID) != normalizeProjectID(projectID) || in.State != "ready" {
			return "missing_input", "pinned dataset was deleted or is unavailable"
		}
		if current := analysisDatasetArtifactSHA256(in); current != pin.DatasetArtifactSHA256 {
			return "stale", "pinned dataset artifacts changed since save"
		}
		return "ready", ""
	}
	if pin.RunID != "" {
		reader, ok := s.store.(interface {
			GetTestContext(context.Context, string) (*storage.TestRecord, error)
			GetRunArchiveContext(context.Context, string) (*storage.RunArchiveRecord, error)
		})
		if !ok {
			return "stale", "input identity reader unavailable"
		}
		test, err := reader.GetTestContext(ctx, pin.RunID)
		if err != nil || test == nil || test.ID != pin.RunID || normalizeProjectID(test.ProjectID) != normalizeProjectID(projectID) {
			return "missing_input", "pinned run was deleted or is unavailable"
		}
		record, err := reader.GetRunArchiveContext(ctx, pin.RunID)
		if err != nil || record == nil || record.TestID != pin.RunID {
			return "missing_input", "pinned run archive was deleted or is unavailable"
		}
		if strings.ToLower(record.ManifestSHA256) != pin.ManifestSHA256 {
			return "stale", "pinned run archive manifest SHA-256 changed since save"
		}
		manifest, err := loadRunArchiveManifestFromRecord(record)
		if err != nil || !runArchiveManifestMatchesStoredTest(manifest, test, s.store) {
			return "stale", "pinned run archive manifest bytes changed or are unreadable"
		}
		manifestPins, err := browserPanelPolicyPinsFromManifest(manifest)
		if err != nil || !slices.Equal(manifestPins, pin.PolicyPins) {
			return "stale", "pinned run archive policy pins changed"
		}
		if err := s.verifySavedPanelPolicyPins(normalizeProjectID(projectID), pin.PolicyPins); err != nil {
			return "stale", strings.TrimPrefix(err.Error(), "stale: ")
		}
		return "ready", ""
	}
	return "missing_input", "source pin has no run or dataset identity"
}

func (s *Server) evaluateSavedPanelStatus(ctx context.Context, projectID string, q storage.AnalyticsQuery) (string, string) {
	if q.PanelInputs == nil {
		return "stale", "saved panel is missing v2 input pins"
	}
	if err := storage.ValidateSavedPanelInputs(q); err != nil {
		return "stale", err.Error()
	}
	if status, reason := s.verifySavedPanelComparisonPin(projectID, q.PanelInputs); status != "ready" {
		return status, reason
	}
	if status, reason := s.verifySavedPanelSourcePin(ctx, projectID, q.PanelInputs.Current); status != "ready" {
		return status, reason
	}
	if q.PanelInputs.Baseline != nil {
		if status, reason := s.verifySavedPanelSourcePin(ctx, projectID, *q.PanelInputs.Baseline); status != "ready" {
			return status, reason
		}
	}
	if q.PanelInputs.Overlay != nil {
		if status, reason := s.verifySavedPanelSourcePin(ctx, projectID, *q.PanelInputs.Overlay); status != "ready" {
			return status, reason
		}
	}
	return "ready", ""
}

func (s *Server) bindAndVerifySavedPanelInputs(ctx context.Context, projectID string, q storage.AnalyticsQuery, response *NativeArchiveResponse) error {
	if response == nil {
		return nil
	}
	pins := buildSavedPanelInputsFromArchive(q, response)
	if q.PanelInputs == nil {
		if pins != nil {
			response.PanelInputs = pins
		}
		return nil
	}
	if pins == nil {
		return errors.New("stale: archive publication digest unavailable")
	}
	if status, reason := s.verifySavedPanelComparisonPin(projectID, q.PanelInputs); status != "ready" {
		return errors.New(status + ": " + reason)
	}
	if err := s.verifySavedPanelPolicyPins(projectID, response.Current.policyPins); err != nil {
		return err
	}
	if response.Baseline != nil {
		if err := s.verifySavedPanelPolicyPins(projectID, response.Baseline.policyPins); err != nil {
			return err
		}
	}
	checked := q
	storage.NormalizeSavedPanelQueryDefaults(&checked)
	checked.PanelInputs = pins
	if err := storage.ValidateNativeArchiveQuery(checked); err != nil {
		return errors.New("stale: saved panel publication identity unavailable")
	}
	if !sameSavedPanelInputs(*q.PanelInputs, *pins) {
		return errors.New("stale: saved panel inputs changed or are unavailable; preview and explicitly save new inputs")
	}
	response.PanelInputs = pins
	return nil
}

// A save validates at most 20 frozen browser or general panels within one shared analysis
// admission and deadline. Changes after this read remain detectable on reopen;
// this is not a transaction spanning archive publications and dashboard storage.
func (s *Server) validateSavedBrowserPanelInputs(w http.ResponseWriter, r *http.Request, d *storage.DashboardDefinition) bool {
	browserQueries := []storage.AnalyticsQuery{}
	generalQueries := []storage.AnalyticsQuery{}
	for ti := range d.Tabs {
		for pi := range d.Tabs[ti].Panels {
			panel := &d.Tabs[ti].Panels[pi]
			if panel.Query == nil {
				continue
			}
			if panel.Query.PanelInputs != nil {
				storage.NormalizeSavedPanelQueryDefaults(panel.Query)
				q := *panel.Query
				if err := storage.ValidateSavedPanelInputs(q); err != nil {
					writeError(w, 400, err.Error())
					return false
				}
				generalQueries = append(generalQueries, q)
				if len(browserQueries)+len(generalQueries) > 20 {
					writeError(w, 400, "at most 20 frozen browser panels may be saved together")
					return false
				}
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
			browserQueries = append(browserQueries, q)
			if len(browserQueries)+len(generalQueries) > 20 {
				writeError(w, 400, "at most 20 frozen browser panels may be saved together")
				return false
			}
		}
	}
	if len(browserQueries) == 0 && len(generalQueries) == 0 {
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
	for _, q := range browserQueries {
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
	projectID := normalizeProjectID(d.ProjectID)
	for _, q := range generalQueries {
		if status, reason := s.evaluateSavedPanelStatus(ctx, projectID, q); status != "ready" {
			writeError(w, 400, status+": "+reason)
			return false
		}
		if q.Population != "" && q.EvidenceMode == "" && q.PanelInputs.Current.RunID != "" {
			current, err := load(q.PanelInputs.Current.RunID, q)
			if err != nil {
				writeError(w, 400, "stale: pinned panel inputs are unavailable or changed")
				return false
			}
			response := NativeArchiveResponse{Current: current}
			if q.ComparisonRunID != "" && q.PanelInputs.Baseline != nil && q.PanelInputs.Baseline.RunID != "" {
				baseline, err := load(q.PanelInputs.Baseline.RunID, q)
				if err != nil {
					writeError(w, 400, "stale: pinned panel baseline inputs are unavailable or changed")
					return false
				}
				response.Baseline = &baseline
			}
			if err := s.bindAndVerifySavedPanelInputs(ctx, projectID, q, &response); err != nil {
				writeError(w, 400, err.Error())
				return false
			}
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
