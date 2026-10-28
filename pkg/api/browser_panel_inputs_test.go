package api

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

func browserPanelInputFixture() (storage.AnalyticsQuery, NativeArchiveResponse) {
	q := storage.AnalyticsQuery{QueryVersion: 4, BrowserTupleMode: "separate", Population: "browser_document", Metric: "document_lcp", Aggregation: "p75", EndSeconds: 60, BucketSeconds: 10, ComparisonRunID: "baseline"}
	series := func(id, digest string) NativeArchiveSeries {
		return NativeArchiveSeries{RunID: id, archiveManifestSHA256: digest, BrowserTuples: &NativeBrowserTupleSeries{Version: 1, Tuples: []NativeBrowserTuple{{ID: strings.Repeat("b", 64)}, {ID: strings.Repeat("a", 64)}}}}
	}
	baseline := series("baseline", strings.Repeat("d", 64))
	return q, NativeArchiveResponse{Current: series("current", strings.Repeat("c", 64)), Baseline: &baseline}
}

func TestBrowserPanelInputPinsUseEvaluatedPublications(t *testing.T) {
	q, response := browserPanelInputFixture()
	if err := bindBrowserPanelInputs(q, &response); err != nil {
		t.Fatal(err)
	}
	if response.BrowserInputs == nil || response.BrowserInputs.Current.RunID != "current" || response.BrowserInputs.Baseline.RunID != "baseline" || response.BrowserInputs.Current.TupleIDs[0] != strings.Repeat("a", 64) {
		t.Fatal("wrong retained inputs", response.BrowserInputs)
	}
	q.BrowserInputs = response.BrowserInputs
	if err := bindBrowserPanelInputs(q, &response); err != nil {
		t.Fatal("unchanged pins refused", err)
	}
	for _, tc := range []struct {
		name   string
		change func(*NativeArchiveResponse)
	}{
		{"current run", func(r *NativeArchiveResponse) { r.Current.RunID = "other" }},
		{"current publication", func(r *NativeArchiveResponse) { r.Current.archiveManifestSHA256 = strings.Repeat("e", 64) }},
		{"baseline publication", func(r *NativeArchiveResponse) { r.Baseline.archiveManifestSHA256 = strings.Repeat("e", 64) }},
		{"missing publication", func(r *NativeArchiveResponse) { r.Current.archiveManifestSHA256 = "" }},
		{"missing tuples", func(r *NativeArchiveResponse) { r.Current.BrowserTuples = nil }},
		{"changed tuple", func(r *NativeArchiveResponse) { r.Current.BrowserTuples.Tuples[0].ID = strings.Repeat("e", 64) }},
		{"missing baseline", func(r *NativeArchiveResponse) { r.Baseline = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, fresh := browserPanelInputFixture()
			tc.change(&fresh)
			if bindBrowserPanelInputs(q, &fresh) == nil {
				t.Fatal("changed input accepted")
			}
		})
	}
}

func TestBrowserPanelInputPinsV2PolicyAndSelectionPins(t *testing.T) {
	v2Fixture := func() (storage.AnalyticsQuery, NativeArchiveResponse) {
		q, response := browserPanelInputFixture()
		q.QueryVersion = 5
		response.Current.policyPins = []storage.BrowserPanelPolicyPin{
			{Kind: storage.AnalysisDefinitionKindBrowserPolicy, ID: "pol-browser", Revision: 2, SHA256: strings.Repeat("1", 64)},
			{Kind: storage.AnalysisDefinitionKindJourneyPolicy, ID: "pol-journey", Revision: 1, SHA256: strings.Repeat("2", 64)},
		}
		response.Current.selectionReceiptSHA256 = strings.Repeat("3", 64)
		response.Baseline.policyPins = []storage.BrowserPanelPolicyPin{
			{Kind: storage.AnalysisDefinitionKindBrowserPolicy, ID: "pol-browser", Revision: 2, SHA256: strings.Repeat("1", 64)},
		}
		return q, response
	}
	q, response := v2Fixture()
	if err := bindBrowserPanelInputs(q, &response); err != nil {
		t.Fatal(err)
	}
	if response.BrowserInputs == nil || response.BrowserInputs.Version != 2 || len(response.BrowserInputs.PolicyPins) != 2 || response.BrowserInputs.SelectionReceiptSHA256 != strings.Repeat("3", 64) || len(response.BrowserInputs.Baseline.PolicyPins) != 1 {
		t.Fatalf("unexpected v2 pins: %+v", response.BrowserInputs)
	}
	q.BrowserInputs = response.BrowserInputs
	if err := bindBrowserPanelInputs(q, &response); err != nil {
		t.Fatalf("unchanged v2 pins refused: %v", err)
	}
	for _, tc := range []struct {
		name   string
		change func(*NativeArchiveResponse)
	}{
		{"changed current policy revision", func(r *NativeArchiveResponse) { r.Current.policyPins[0].Revision = 3 }},
		{"changed current policy digest", func(r *NativeArchiveResponse) { r.Current.policyPins[0].SHA256 = strings.Repeat("9", 64) }},
		{"changed baseline policy digest", func(r *NativeArchiveResponse) { r.Baseline.policyPins[0].SHA256 = strings.Repeat("9", 64) }},
		{"changed selection receipt", func(r *NativeArchiveResponse) { r.Current.selectionReceiptSHA256 = strings.Repeat("8", 64) }},
		{"missing selection receipt", func(r *NativeArchiveResponse) { r.Current.selectionReceiptSHA256 = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, fresh := v2Fixture()
			tc.change(&fresh)
			if bindBrowserPanelInputs(q, &fresh) == nil {
				t.Fatal("changed v2 input accepted")
			}
		})
	}
}

func TestBrowserPanelInputPinsDoNotAlterOldResponses(t *testing.T) {
	for _, v := range []int{0, 1, 2, 3} {
		q := storage.AnalyticsQuery{QueryVersion: v}
		response := NativeArchiveResponse{}
		if err := bindBrowserPanelInputs(q, &response); err != nil || response.BrowserInputs != nil {
			t.Fatal(v, err)
		}
	}
}

func TestBrowserPanelInputCombinedBounds(t *testing.T) {
	for _, version := range []int{3, 4, 5} {
		for _, tc := range []struct {
			name                                     string
			current, baseline, shift, buckets, limit int
			wantError                                bool
		}{
			{"matched limit", 20, 20, 0, 1000, 0, false},
			{"union exceeds limit", 20, 20, 1, 1000, 0, true},
			{"selected limit", 2, 2, 1, 1, 2, true},
			{"baseline only limit", 0, 20, 0, 1000, 20, false},
			{"row budget", 20, 20, 0, 1001, 20, true},
			{"empty inputs", 0, 0, 0, 1000, 20, false},
		} {
			t.Run(fmt.Sprintf("v%d/%s", version, tc.name), func(t *testing.T) {
				q, response := browserPanelInputFixture()
				q.QueryVersion = version
				q.MaxSeries = tc.limit
				tuples := func(count, shift int) []NativeBrowserTuple {
					result := make([]NativeBrowserTuple, count)
					for i := range result {
						result[i] = NativeBrowserTuple{ID: fmt.Sprintf("%064x", i+shift), Buckets: make([]NativeArchiveBucket, tc.buckets)}
					}
					return result
				}
				response.Current.BrowserTuples.Tuples = tuples(tc.current, 0)
				response.Baseline.BrowserTuples.Tuples = tuples(tc.baseline, tc.shift)
				err := bindBrowserPanelInputs(q, &response)
				if (err != nil) != tc.wantError {
					t.Fatalf("combined bound: %v, want error %v", err, tc.wantError)
				}
				if tc.wantError && response.BrowserInputs != nil {
					t.Fatal("oversized comparison acquired pins")
				}
			})
		}
	}
}
