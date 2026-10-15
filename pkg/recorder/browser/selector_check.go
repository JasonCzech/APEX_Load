package browser

import (
	"fmt"
	"slices"
	"sort"

	"github.com/jasonczech/apexload/pkg/selector"
	playwright "github.com/mxschmitt/playwright-go"
)

// The recorder ranks a step's selectors from what the page's own script saw,
// but replay resolves them through Playwright. On a Salesforce Lightning page,
// where the document keeps hidden copies of earlier pages, a selector can match
// several visible elements there. After each such step the recorder counts,
// through Playwright, what its top candidates match, and ranks any that match
// several below the rest. A candidate matching nothing is left alone: the
// element the step used may simply be gone by then.

const (
	// checkedCandidates is how many of a step's top candidates are counted.
	checkedCandidates = 3
	// maxSelectorCheckWorkers bounds the checks in flight.
	maxSelectorCheckWorkers = 4
	// ambiguousConfidence ranks a candidate that matched several elements
	// below every other one.
	ambiguousConfidence = selector.ConfidenceVolatileAuraID - 1
)

// checkLightningCandidatesAsync counts the visible matches of a Lightning
// step's top candidates and demotes the ambiguous ones.
func (r *Recorder) checkLightningCandidatesAsync(page playwright.Page, step Step) {
	if page == nil || step.Element == nil || !step.Element.Lightning || step.FrameSelector != "" || len(step.FramePath) > 0 {
		return
	}
	selectors := make([]string, 0, checkedCandidates)
	for _, candidate := range step.Candidates {
		if len(selectors) == checkedCandidates {
			break
		}
		if value, ok := selector.Usable(candidate); ok {
			selectors = append(selectors, value)
		}
	}
	if len(selectors) == 0 {
		return
	}
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		return
	}
	if r.selectorCheckSlots == nil {
		r.selectorCheckSlots = make(chan struct{}, maxSelectorCheckWorkers)
	}
	select {
	case r.selectorCheckSlots <- struct{}{}:
	default:
		// Busy: this step keeps its ranking.
		r.mu.Unlock()
		return
	}
	r.thumbWG.Add(1)
	r.mu.Unlock()
	go func() {
		defer r.thumbWG.Done()
		defer func() { <-r.selectorCheckSlots }()
		matches := make(map[string]int, len(selectors))
		for _, value := range selectors {
			if count, err := page.Locator(value).Count(); err == nil {
				matches[value] = count
			}
		}
		r.mu.Lock()
		var updated *Step
		if !r.stopped {
			for i := range r.session.Steps {
				if r.session.Steps[i].Seq != step.Seq {
					continue
				}
				// A copy: the step event emitted earlier shares the slice.
				candidates := slices.Clone(r.session.Steps[i].Candidates)
				if demoteAmbiguousCandidates(candidates, matches) {
					r.session.Steps[i].Candidates = candidates
					s := r.session.Steps[i]
					updated = &s
				}
				break
			}
		}
		r.mu.Unlock()
		if updated != nil {
			r.emit(Event{Type: EventStepUpdate, Step: updated})
		}
	}()
}

// demoteAmbiguousCandidates ranks the candidates that matched several elements
// below the rest, keeping the order otherwise, and reports whether any did.
func demoteAmbiguousCandidates(candidates []selector.Candidate, matches map[string]int) bool {
	demoted := false
	for i := range candidates {
		value, ok := selector.Usable(candidates[i])
		if !ok || matches[value] <= 1 || candidates[i].Confidence <= ambiguousConfidence {
			continue
		}
		candidates[i].Confidence = ambiguousConfidence
		candidates[i].Reason = fmt.Sprintf("Matched %d visible elements when recorded; ranked last.", matches[value])
		demoted = true
	}
	if demoted {
		sort.SliceStable(candidates, func(a, b int) bool { return candidates[a].Confidence > candidates[b].Confidence })
	}
	return demoted
}
