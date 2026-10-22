package rca

import (
	"context"
	"errors"
	"slices"
	"sort"
	"strings"
	"time"
)

const BrowserSelectionVersion = "browser_selection.v1"
const MaxBrowserSelectionCandidates = 128

// BrowserBaselineSelection is an admission receipt, not a claim that frozen
// evidence is still available at execution time. No wall-clock inspection time
// participates in it, so equivalent decisions have identical input digests.
type BrowserBaselineSelection struct {
	Version        string                      `json:"version"`
	Policy         string                      `json:"policy"`
	Status         string                      `json:"status"`
	InventoryCount int                         `json:"inventory_count"`
	SelectedTestID string                      `json:"selected_test_id,omitempty"`
	SelectedDigest string                      `json:"selected_digest,omitempty"`
	Reasons        []string                    `json:"reasons,omitempty"`
	Rejected       []BrowserCandidateRejection `json:"rejected,omitempty"`
}

type BrowserCandidateRejection struct {
	TestID  string   `json:"test_id"`
	Digest  string   `json:"digest,omitempty"`
	Reasons []string `json:"reasons"`
}

type BrowserSelectionCandidate struct {
	TestID      string
	CompletedAt time.Time
}

type BrowserCandidateInspection struct {
	CompletedAt time.Time // Exact immutable archive completion, never the database projection.
	Digest      string
	Baseline    *DetectionBaseline
	Reasons     []string
}

// SelectBrowserBaseline examines one complete bounded inventory. Inspect must
// verify canonical identity, scope, incarnation, policy and archived evidence,
// and release any materialization lease before returning. Transient inspection
// errors abort admission; they must not silently make an older run the latest.
func SelectBrowserBaseline(ctx context.Context, currentID string, started time.Time, current DetectionInput, inventory []BrowserSelectionCandidate, more bool, inspect func(context.Context, BrowserSelectionCandidate) (BrowserCandidateInspection, error)) (*BrowserBaselineSelection, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	receipt := &BrowserBaselineSelection{Version: BrowserSelectionVersion, InventoryCount: len(inventory)}
	if p := current.BrowserPolicy; p == nil || p.Unavailable != "" || p.Mode != "latest_compatible" || p.Ref == "" {
		receipt.Status = "policy_unavailable"
		return receipt, nil
	} else {
		receipt.Policy = p.Ref
	}
	if more || len(inventory) > MaxBrowserSelectionCandidates {
		receipt.Status = "inventory_incomplete"
		return receipt, nil
	}
	if currentID == "" || started.IsZero() {
		receipt.Status = "current_identity_unavailable"
		return receipt, nil
	}
	receipt.Reasons = browserSelectionCurrentReasons(current)
	if len(receipt.Reasons) > 0 {
		receipt.Status = "current_unqualified"
		return receipt, nil
	}
	candidates := slices.Clone(inventory)
	seen := map[string]bool{}
	for _, c := range candidates {
		if c.TestID == "" || len(c.TestID) > 256 || seen[c.TestID] {
			receipt.Status = "inventory_invalid"
			return receipt, nil
		}
		seen[c.TestID] = true
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.CompletedAt.Equal(b.CompletedAt) {
			return a.TestID < b.TestID
		}
		return a.CompletedAt.After(b.CompletedAt)
	})
	reject := func(c BrowserSelectionCandidate, digest string, reasons []string) {
		receipt.Rejected = append(receipt.Rejected, BrowserCandidateRejection{TestID: c.TestID, Digest: digest, Reasons: browserSelectionReasons(reasons)})
	}
	var selectedAt time.Time
	for _, c := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if c.TestID == currentID {
			reject(c, "", []string{"self_baseline"})
			continue
		}
		if inspect == nil {
			return nil, errors.New("browser baseline inspector unavailable")
		}
		result, err := inspect(ctx, c)
		if err != nil {
			return nil, err
		}
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		reasons := result.Reasons
		if len(reasons) == 0 && (result.CompletedAt.IsZero() || !result.CompletedAt.Before(started)) {
			reasons = []string{"completion_not_before_start"}
		}
		if len(reasons) == 0 {
			if result.Digest == "" || result.Baseline == nil || result.Baseline.TestID != c.TestID {
				reasons = []string{"candidate_evidence_unavailable"}
			} else {
				reasons = browserSelectionCandidateReasons(current, result.Baseline)
			}
		}
		if len(reasons) > 0 {
			reject(c, result.Digest, reasons)
			continue
		}
		if selectedAt.IsZero() || result.CompletedAt.After(selectedAt) || (result.CompletedAt.Equal(selectedAt) && c.TestID < receipt.SelectedTestID) {
			selectedAt = result.CompletedAt
			receipt.Status, receipt.SelectedTestID, receipt.SelectedDigest = "selected", c.TestID, result.Digest
		}
	}
	if receipt.Status == "" {
		receipt.Status = "no_compatible_candidate"
	}
	return receipt, nil
}

func browserSelectionCurrentReasons(input DetectionInput) []string {
	var reasons []string
	if input.DocumentsIncomplete || input.DocumentsUnreadable {
		reasons = append(reasons, "current_document_evidence_incomplete")
	}
	if len(input.Levels) == 0 {
		reasons = append(reasons, BrowserBaselineLevelsUnavailable)
	}
	count := 0
	for _, s := range input.Series {
		if s.Kind != SeriesBrowserDocument {
			continue
		}
		count++
		for _, l := range browserDocumentQualificationLimitations(s.Document, false) {
			reasons = append(reasons, l.Code)
		}
		if s.Document != nil && s.Document.Incomplete {
			reasons = append(reasons, BrowserBaselineSourceLoss)
		}
	}
	if count == 0 {
		reasons = append(reasons, "current_document_evidence_unavailable")
	}
	return browserSelectionReasons(reasons)
}

func browserSelectionCandidateReasons(current DetectionInput, baseline *DetectionBaseline) []string {
	current.Baseline = baseline
	out := DetectBrowserBaseline(current, BrowserBaselineParamsFor(current.BrowserPolicy))
	if out == nil {
		return []string{"candidate_comparison_unavailable"}
	}
	var reasons []string
	for _, l := range out.Limitations {
		reasons = append(reasons, l.Code)
	}
	covered := map[string]bool{}
	for _, r := range out.Results {
		// A constant baseline is compatible even though the unchanged estimator
		// cannot produce a z-score from its zero variance.
		if r.State == BrowserBaselineAnomaly || r.State == BrowserBaselineWithinRange || r.Reason == BrowserBaselineZeroVariance {
			covered[r.Series] = true
		} else {
			reasons = append(reasons, r.Reason)
		}
	}
	for _, s := range current.Series {
		if s.Kind == SeriesBrowserDocument && !covered[s.ID] {
			reasons = append(reasons, "current_population_not_comparable")
		}
	}
	if out.Status != DetectionComplete && len(reasons) == 0 {
		reasons = append(reasons, "candidate_comparison_incomplete")
	}
	return browserSelectionReasons(reasons)
}

func browserSelectionReasons(reasons []string) []string {
	reasons = slices.Clone(reasons)
	for i, r := range reasons {
		if strings.TrimSpace(r) == "" {
			reasons[i] = "candidate_comparison_unavailable"
		}
	}
	slices.Sort(reasons)
	reasons = slices.Compact(reasons)
	if len(reasons) > 16 {
		reasons = append(reasons[:15], "reasons_truncated")
	}
	return reasons
}
