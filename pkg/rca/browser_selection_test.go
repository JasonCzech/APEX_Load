package rca

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func selectionFixture() (DetectionInput, *DetectionBaseline) {
	current := documentSeries("lcp", "lcp", "ms", "page", chromium141, browserNoisy(40, 1400, 20), 3)
	baseline := documentSeries("lcp", "lcp", "ms", "page", chromium141, browserNoisy(40, 1000, 20), 3)
	input := browserBaselineInput([]Series{current}, []Series{baseline}, BaselineQualified)
	input.BrowserPolicy = &BrowserBaselinePolicy{Ref: "policy@1", Mode: "latest_compatible"}
	return input, input.Baseline
}

func TestBrowserSelectionUsesExactInstantsAndRejectsNewerIncompatible(t *testing.T) {
	input, baseline := selectionFixture()
	start := time.Date(2026, 10, 22, 14, 0, 0, 0, time.UTC)
	// Offset-equivalent ties sort by canonical ID, preserving nanoseconds.
	newest := start.Add(-time.Nanosecond)
	older := newest.Add(-time.Nanosecond)
	inventory := []BrowserSelectionCandidate{{"z", older.In(time.FixedZone("west", -4*3600))}, {"new", newest}, {"a", older}, {"boundary", start}, {"self", newest}}
	before := append([]BrowserSelectionCandidate(nil), inventory...)
	var calls []string
	receipt, err := SelectBrowserBaseline(context.Background(), "self", start, input, inventory, false, func(_ context.Context, c BrowserSelectionCandidate) (BrowserCandidateInspection, error) {
		calls = append(calls, c.TestID)
		if c.TestID == "new" {
			return BrowserCandidateInspection{Digest: "new-digest", Reasons: []string{BrowserBaselineCohortChanged}}, nil
		}
		b := *baseline
		b.TestID = c.TestID
		return BrowserCandidateInspection{CompletedAt: c.CompletedAt, Digest: c.TestID + "-digest", Baseline: &b}, nil
	})
	if err != nil || receipt.Status != "selected" || receipt.SelectedTestID != "a" || !reflect.DeepEqual(calls, []string{"boundary", "new", "a", "z"}) || !reflect.DeepEqual(before, inventory) {
		t.Fatalf("selection: %+v %v calls=%v", receipt, err, calls)
	}
	if len(receipt.Rejected) != 3 || receipt.Rejected[0].TestID != "boundary" || receipt.Rejected[1].TestID != "new" || receipt.Rejected[2].TestID != "self" {
		t.Fatalf("rejections: %+v", receipt.Rejected)
	}
}

func TestBrowserSelectionRefusesIncompleteInventoryAndMissingProof(t *testing.T) {
	for _, tc := range []struct {
		name   string
		more   bool
		mutate func(*DetectionInput)
		want   string
	}{
		{name: "inventory overflow", more: true, want: "inventory_incomplete"},
		{name: "missing clocks", mutate: func(i *DetectionInput) { i.Series[0].Document.ClockQualification = "" }, want: "current_unqualified"},
		{name: "unknown source coverage", mutate: func(i *DetectionInput) { i.Series[0].Document.SourceCoverageQualification = "" }, want: "current_unqualified"},
		{name: "no policy", mutate: func(i *DetectionInput) { i.BrowserPolicy = nil }, want: "policy_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input, _ := selectionFixture()
			if tc.mutate != nil {
				tc.mutate(&input)
			}
			r, err := SelectBrowserBaseline(context.Background(), "current", time.Now(), input, nil, tc.more, func(context.Context, BrowserSelectionCandidate) (BrowserCandidateInspection, error) {
				t.Fatal("unexpected inspection")
				return BrowserCandidateInspection{}, nil
			})
			if err != nil || r.Status != tc.want {
				t.Fatalf("receipt: %+v %v", r, err)
			}
		})
	}
}

func TestBrowserSelectionInspectsEveryRequiredPopulationAndAcceptsZeroVariance(t *testing.T) {
	input, baseline := selectionFixture()
	baseline.Series[0] = documentSeries("lcp", "lcp", "ms", "page", chromium141, browserNoisy(40, 1000, 0), 3)
	if reasons := browserSelectionCandidateReasons(input, baseline); len(reasons) != 0 {
		t.Fatalf("constant qualified population: %v", reasons)
	}
	other := documentSeries("other", "lcp", "ms", "another-page", chromium141, browserNoisy(40, 1000, 20), 3)
	input.Series = append(input.Series, other)
	if reasons := browserSelectionCandidateReasons(input, baseline); len(reasons) == 0 {
		t.Fatal("one matched population qualified missing second population")
	}
}

func TestBrowserSelectionAbortsOnTransientFailureAndCancellation(t *testing.T) {
	input, _ := selectionFixture()
	start := time.Now()
	inventory := []BrowserSelectionCandidate{{"candidate", start.Add(-time.Second)}}
	unavailable := errors.New("storage unavailable")
	r, err := SelectBrowserBaseline(context.Background(), "current", start, input, inventory, false, func(context.Context, BrowserSelectionCandidate) (BrowserCandidateInspection, error) {
		return BrowserCandidateInspection{}, unavailable
	})
	if r != nil || !errors.Is(err, unavailable) {
		t.Fatalf("transient failure became incompatibility: %+v %v", r, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	r, err = SelectBrowserBaseline(ctx, "current", start, input, inventory, false, func(context.Context, BrowserSelectionCandidate) (BrowserCandidateInspection, error) {
		cancel()
		return BrowserCandidateInspection{}, nil
	})
	if r != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %+v %v", r, err)
	}
}

func TestBrowserSelectionRanksImmutableCompletionBeyondDatabasePrecision(t *testing.T) {
	input, baseline := selectionFixture()
	stored := time.Date(2026, 10, 22, 14, 0, 0, 0, time.UTC)
	start := stored.Add(500 * time.Nanosecond)
	inventory := []BrowserSelectionCandidate{{"a", stored}, {"z", stored}, {"after", stored}}
	exact := map[string]time.Time{"a": stored.Add(100 * time.Nanosecond), "z": stored.Add(400 * time.Nanosecond), "after": stored.Add(600 * time.Nanosecond)}
	receipt, err := SelectBrowserBaseline(context.Background(), "current", start, input, inventory, false, func(_ context.Context, c BrowserSelectionCandidate) (BrowserCandidateInspection, error) {
		b := *baseline
		b.TestID = c.TestID
		return BrowserCandidateInspection{CompletedAt: exact[c.TestID], Digest: c.TestID, Baseline: &b}, nil
	})
	if err != nil || receipt.SelectedTestID != "z" || len(receipt.Rejected) != 1 || receipt.Rejected[0].TestID != "after" {
		t.Fatalf("exact admission: %+v %v", receipt, err)
	}
}
