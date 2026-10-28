package storage

import (
	"strings"
	"testing"
)

func pinnedBrowserQueryFixture() AnalyticsQuery {
	q := AnalyticsQuery{QueryVersion: 4, BrowserTupleMode: "separate", Population: "browser_document", Metric: "document_lcp", Aggregation: "p75", StartSeconds: 0, EndSeconds: 60, BucketSeconds: 10, ComparisonRunID: "baseline"}
	q.BrowserInputs = &BrowserPanelInputs{Version: 1, QuerySHA256: BrowserPanelQueryDigest(q), Current: BrowserPanelRunInput{RunID: "current", ManifestSHA256: strings.Repeat("a", 64), TupleIDs: []string{strings.Repeat("b", 64)}}, Baseline: &BrowserPanelRunInput{RunID: "baseline", ManifestSHA256: strings.Repeat("c", 64), TupleIDs: []string{strings.Repeat("b", 64)}}}
	return q
}

func TestBrowserPanelInputsBindQueryAndBoundIdentities(t *testing.T) {
	if err := ValidateNativeArchiveQuery(pinnedBrowserQueryFixture()); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		change func(*AnalyticsQuery)
	}{
		{"old version", func(q *AnalyticsQuery) { q.QueryVersion = 3 }},
		{"future version", func(q *AnalyticsQuery) { q.QueryVersion = 6 }},
		{"v5 query with v1 pin", func(q *AnalyticsQuery) { q.QueryVersion = 5 }},
		{"other population", func(q *AnalyticsQuery) { q.Population = "action" }},
		{"live", func(q *AnalyticsQuery) { q.EvidenceMode = "live" }},
		{"pooled", func(q *AnalyticsQuery) { q.BrowserTupleMode = "" }},
		{"changed window", func(q *AnalyticsQuery) { q.EndSeconds = 120 }},
		{"changed filter", func(q *AnalyticsQuery) { q.Filters = map[string]string{"engine": "webkit"} }},
		{"changed metric", func(q *AnalyticsQuery) { q.Metric = "document_fcp" }},
		{"changed aggregation", func(q *AnalyticsQuery) { q.Aggregation = "avg" }},
		{"changed limit", func(q *AnalyticsQuery) { q.MaxSeries = 1 }},
		{"changed baseline", func(q *AnalyticsQuery) { q.ComparisonRunID = "different" }},
		{"missing baseline pin", func(q *AnalyticsQuery) { q.BrowserInputs.Baseline = nil }},
		{"missing publication", func(q *AnalyticsQuery) { q.BrowserInputs.Current.ManifestSHA256 = "" }},
		{"malformed publication", func(q *AnalyticsQuery) { q.BrowserInputs.Current.ManifestSHA256 = strings.Repeat("z", 64) }},
		{"unknown pin version", func(q *AnalyticsQuery) { q.BrowserInputs.Version = 2 }},
		{"missing tuple population", func(q *AnalyticsQuery) { q.BrowserInputs.Current.TupleIDs = nil }},
		{"duplicate tuple", func(q *AnalyticsQuery) {
			q.BrowserInputs.Current.TupleIDs = append(q.BrowserInputs.Current.TupleIDs, q.BrowserInputs.Current.TupleIDs[0])
		}},
		{"unsorted tuples", func(q *AnalyticsQuery) {
			q.BrowserInputs.Current.TupleIDs = []string{strings.Repeat("c", 64), strings.Repeat("b", 64)}
		}},
		{"oversized tuple population", func(q *AnalyticsQuery) { q.BrowserInputs.Current.TupleIDs = make([]string, 21) }},
		{"whitespace run", func(q *AnalyticsQuery) { q.BrowserInputs.Current.RunID = " current" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := pinnedBrowserQueryFixture()
			tc.change(&q)
			if ValidateNativeArchiveQuery(q) == nil {
				t.Fatal("invalid input pin accepted")
			}
		})
	}
}

func TestBrowserPanelInputsPreviewAndCanonicalDefault(t *testing.T) {
	q := pinnedBrowserQueryFixture()
	q.BrowserInputs = nil
	if err := ValidateNativeArchiveQuery(q); err != nil {
		t.Fatal("preview refused", err)
	}
	digest := BrowserPanelQueryDigest(q)
	q.MaxSeries = 20
	if BrowserPanelQueryDigest(q) != digest {
		t.Fatal("save-time default changes input identity")
	}
	q.QueryVersion = 3
	if err := ValidateNativeArchiveQuery(q); err != nil {
		t.Fatal("legacy separate tuples refused", err)
	}
	q = pinnedBrowserQueryFixture()
	q.ComparisonRunID = ""
	q.BrowserInputs.Baseline = nil
	q.BrowserInputs.QuerySHA256 = BrowserPanelQueryDigest(q)
	if err := ValidateNativeArchiveQuery(q); err != nil {
		t.Fatal("single run pin refused", err)
	}
	q.BrowserInputs.Current.TupleIDs = []string{}
	if err := ValidateNativeArchiveQuery(q); err != nil {
		t.Fatal("explicit empty tuple population refused", err)
	}
}

func TestBrowserPanelInputsV2PolicyAndSelectionPins(t *testing.T) {
	pins := []BrowserPanelPolicyPin{
		{Kind: AnalysisDefinitionKindBrowserPolicy, ID: "bp-1", Revision: 2, SHA256: strings.Repeat("1", 64)},
		{Kind: AnalysisDefinitionKindJourneyPolicy, ID: "jp-1", Revision: 1, SHA256: strings.Repeat("2", 64)},
	}
	q := AnalyticsQuery{QueryVersion: 5, BrowserTupleMode: "separate", Population: "browser_document", Metric: "document_lcp", Aggregation: "p75", StartSeconds: 0, EndSeconds: 60, BucketSeconds: 10, ComparisonRunID: "baseline"}
	q.BrowserInputs = &BrowserPanelInputs{
		Version:                2,
		QuerySHA256:            BrowserPanelQueryDigest(q),
		PolicyPins:             pins,
		SelectionReceiptSHA256: strings.Repeat("e", 64),
		Current: BrowserPanelRunInput{
			RunID:          "current",
			ManifestSHA256: strings.Repeat("a", 64),
			TupleIDs:       []string{strings.Repeat("b", 64)},
			PolicyPins:     pins,
		},
		Baseline: &BrowserPanelRunInput{
			RunID:          "baseline",
			ManifestSHA256: strings.Repeat("c", 64),
			TupleIDs:       []string{strings.Repeat("b", 64)},
			PolicyPins:     pins[:1],
		},
	}
	if err := ValidateNativeArchiveQuery(q); err != nil {
		t.Fatalf("valid v2 pins refused: %v", err)
	}
	for _, tc := range []struct {
		name   string
		change func(*AnalyticsQuery)
	}{
		{"v1 pin on v5 query", func(q *AnalyticsQuery) { q.BrowserInputs.Version = 1 }},
		{"v2 pin on v4 query", func(q *AnalyticsQuery) {
			q.QueryVersion = 4
			q.BrowserInputs.QuerySHA256 = BrowserPanelQueryDigest(*q)
		}},
		{"unsorted policy pins", func(q *AnalyticsQuery) {
			reversed := []BrowserPanelPolicyPin{pins[1], pins[0]}
			q.BrowserInputs.PolicyPins = reversed
			q.BrowserInputs.Current.PolicyPins = reversed
		}},
		{"mismatched top-level policy pins", func(q *AnalyticsQuery) {
			q.BrowserInputs.PolicyPins = pins[:1]
		}},
		{"invalid policy kind", func(q *AnalyticsQuery) {
			bad := []BrowserPanelPolicyPin{{Kind: "telemetry_policy", ID: "tp", Revision: 1, SHA256: strings.Repeat("1", 64)}}
			q.BrowserInputs.PolicyPins = bad
			q.BrowserInputs.Current.PolicyPins = bad
		}},
		{"malformed selection receipt", func(q *AnalyticsQuery) {
			q.BrowserInputs.SelectionReceiptSHA256 = "not-a-sha256"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copyQ := q
			copyInputs := *q.BrowserInputs
			copyInputs.PolicyPins = append([]BrowserPanelPolicyPin(nil), q.BrowserInputs.PolicyPins...)
			copyInputs.Current.PolicyPins = append([]BrowserPanelPolicyPin(nil), q.BrowserInputs.Current.PolicyPins...)
			copyQ.BrowserInputs = &copyInputs
			tc.change(&copyQ)
			if ValidateNativeArchiveQuery(copyQ) == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
		})
	}
}

func TestSavedPanelInputsV2GeneralPinsAndFrozenDefaults(t *testing.T) {
	q := AnalyticsQuery{
		Population:      "byte",
		Metric:          "bytes_sent",
		Aggregation:     "p95",
		ComparisonRunID: "run-base",
		Filters:         map[string]string{"protocol": "grpc"},
	}
	NormalizeSavedPanelQueryDefaults(&q)
	if q.QueryVersion != AnalyticsQueryVersion || q.Alignment != "elapsed" || q.CoveragePolicy != "complete_bucket" || q.BucketSeconds != 10 || q.EndSeconds != 60 || q.MaxSeries != 1 {
		t.Fatalf("unexpected frozen defaults: %+v", q)
	}
	q.PanelInputs = &SavedPanelInputs{
		Version:            2,
		CatalogVersion:     q.CatalogVersion,
		Alignment:          q.Alignment,
		CoveragePolicy:     q.CoveragePolicy,
		Population:         q.Population,
		Metric:             q.Metric,
		Aggregation:        q.Aggregation,
		QuerySHA256:        SavedPanelQueryDigest(q),
		ComparisonID:       "cmp-1",
		ComparisonRevision: 2,
		ComparisonSHA256:   strings.Repeat("9", 64),
		Drilldown: SavedPanelDrilldownBounds{
			StartSeconds:  q.StartSeconds,
			EndSeconds:    q.EndSeconds,
			BucketSeconds: q.BucketSeconds,
			MaxSeries:     q.MaxSeries,
		},
		Current: SavedPanelSourcePin{
			RunID:          "run-cur",
			ManifestSHA256: strings.Repeat("a", 64),
			Protocol:       "grpc",
		},
		Baseline: &SavedPanelSourcePin{
			RunID:          "run-base",
			ManifestSHA256: strings.Repeat("b", 64),
			Protocol:       "grpc",
		},
	}
	if err := ValidateNativeArchiveQuery(q); err != nil {
		t.Fatalf("expected valid general SavedPanelInputs v2, got %v", err)
	}

	// Mutating any frozen parameter or pin without updating PanelInputs is rejected.
	for _, tc := range []struct {
		name   string
		mutate func(*AnalyticsQuery)
	}{
		{"alignment mismatch", func(q *AnalyticsQuery) { q.Alignment = "utc" }},
		{"coverage mismatch", func(q *AnalyticsQuery) { q.CoveragePolicy = "required" }},
		{"bucket mismatch", func(q *AnalyticsQuery) { q.BucketSeconds = 30 }},
		{"comparison_run_id mismatch", func(q *AnalyticsQuery) { q.ComparisonRunID = "other" }},
		{"missing current digest", func(q *AnalyticsQuery) { q.PanelInputs.Current.ManifestSHA256 = "" }},
		{"both run and dataset on source pin", func(q *AnalyticsQuery) {
			q.PanelInputs.Current.DatasetID = "ds-1"
			q.PanelInputs.Current.DatasetArtifactSHA256 = strings.Repeat("d", 64)
		}},
		{"invalid comparison pin digest", func(q *AnalyticsQuery) { q.PanelInputs.ComparisonSHA256 = "bad" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cp := q
			cpInputs := *q.PanelInputs
			if q.PanelInputs.Baseline != nil {
				baseCopy := *q.PanelInputs.Baseline
				cpInputs.Baseline = &baseCopy
			}
			cp.PanelInputs = &cpInputs
			tc.mutate(&cp)
			if err := ValidateSavedPanelInputs(cp); err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
		})
	}
}
