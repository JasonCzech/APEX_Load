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
		{"future version", func(q *AnalyticsQuery) { q.QueryVersion = 5 }},
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
