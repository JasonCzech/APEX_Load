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
	for _, version := range []int{3, 4} {
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
