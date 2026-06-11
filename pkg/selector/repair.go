package selector

import (
	"fmt"
	"sort"
	"strings"
)

// RepairSeverity mirrors the correlation repair engine's severity bands so the
// dashboard renders both diagnose surfaces consistently.
type RepairSeverity string

const (
	SeverityError   RepairSeverity = "error"
	SeverityWarning RepairSeverity = "warning"
	SeverityInfo    RepairSeverity = "info"
)

// Replay step statuses accepted by Diagnose.
const (
	StatusOK     = "ok"
	StatusFailed = "failed"
	StatusHealed = "healed"
)

// minStableConfidence is the confidence below which a passing selector still
// earns a stability advisory (matches the TruClient replay-diagnostic
// threshold).
const minStableConfidence = 70

// ReplayStep is one browser action's replay outcome: the selector the script
// used, the ranked candidates captured at recording time, and what happened.
type ReplayStep struct {
	ActionName     string      `json:"action_name,omitempty"`
	Selector       string      `json:"selector"`
	Candidates     []Candidate `json:"candidates,omitempty"`
	Status         string      `json:"status"`
	HealedSelector string      `json:"healed_selector,omitempty"`
	Error          string      `json:"error,omitempty"`
}

// RepairSuggestion is one ranked selector repair, shaped after
// correlate.RepairSuggestion so both diagnose surfaces share review semantics.
type RepairSuggestion struct {
	Severity          RepairSeverity `json:"severity"`
	Title             string         `json:"title"`
	Detail            string         `json:"detail"`
	Suggestion        string         `json:"suggestion"`
	ActionName        string         `json:"action_name,omitempty"`
	RecordedSelector  string         `json:"recorded_selector"`
	SuggestedSelector string         `json:"suggested_selector,omitempty"`
	Confidence        int            `json:"confidence,omitempty"`
	ReviewAction      string         `json:"review_action"`
}

// Diagnose analyzes browser replay outcomes and returns ranked selector
// repairs: failed selectors with viable alternates (error), self-healed
// selectors worth promoting (warning, auto-applicable), and passing selectors
// whose best candidate is too weak to trust long-term (info).
func Diagnose(steps []ReplayStep) []RepairSuggestion {
	suggestions := make([]RepairSuggestion, 0)
	for _, step := range steps {
		switch strings.ToLower(strings.TrimSpace(step.Status)) {
		case StatusFailed:
			suggestions = append(suggestions, diagnoseFailed(step))
		case StatusHealed:
			suggestions = append(suggestions, diagnoseHealed(step))
		case StatusOK:
			if suggestion, ok := diagnoseWeakButPassing(step); ok {
				suggestions = append(suggestions, suggestion)
			}
		}
	}
	sort.SliceStable(suggestions, func(i, j int) bool {
		return severityRank(suggestions[i].Severity) < severityRank(suggestions[j].Severity)
	})
	return suggestions
}

func diagnoseFailed(step ReplayStep) RepairSuggestion {
	suggestion := RepairSuggestion{
		Severity:         SeverityError,
		Title:            "Selector failed on replay",
		RecordedSelector: step.Selector,
		ActionName:       step.ActionName,
		ReviewAction:     "manual_review_required",
	}
	detail := fmt.Sprintf("Selector %q did not resolve during replay", step.Selector)
	if step.Error != "" {
		detail += ": " + step.Error
	}
	suggestion.Detail = detail + "."
	if alternate, confidence, ok := bestAlternate(step.Candidates, step.Selector); ok {
		suggestion.SuggestedSelector = alternate
		suggestion.Confidence = confidence
		suggestion.Suggestion = fmt.Sprintf("Replay against the next ranked candidate %q, or re-record the step if the page changed.", alternate)
		suggestion.ReviewAction = "alternate_available"
	} else {
		suggestion.Suggestion = "No ranked alternates were captured for this step; re-record it to refresh the selector candidates."
	}
	return suggestion
}

func diagnoseHealed(step ReplayStep) RepairSuggestion {
	confidence := 0
	for _, candidate := range step.Candidates {
		if usable, ok := Usable(candidate); ok && usable == step.HealedSelector {
			confidence = candidate.Confidence
			break
		}
	}
	return RepairSuggestion{
		Severity:          SeverityWarning,
		Title:             "Selector healed via ranked fallback",
		Detail:            fmt.Sprintf("Primary selector %q failed; replay succeeded with fallback %q.", step.Selector, step.HealedSelector),
		Suggestion:        fmt.Sprintf("Promote %q to the step's primary selector so future replays skip the failing locator.", step.HealedSelector),
		ActionName:        step.ActionName,
		RecordedSelector:  step.Selector,
		SuggestedSelector: step.HealedSelector,
		Confidence:        confidence,
		ReviewAction:      "auto_apply_available",
	}
}

func diagnoseWeakButPassing(step ReplayStep) (RepairSuggestion, bool) {
	if len(step.Candidates) == 0 {
		return RepairSuggestion{}, false
	}
	best := step.Candidates[0]
	if best.Confidence >= minStableConfidence {
		return RepairSuggestion{}, false
	}
	return RepairSuggestion{
		Severity:         SeverityInfo,
		Title:            "Selector passes but is weakly anchored",
		Detail:           fmt.Sprintf("The strongest captured candidate for %q is %s (confidence %d), below the stability threshold of %d.", step.Selector, best.Kind, best.Confidence, minStableConfidence),
		Suggestion:       "Ask the application team for a dedicated test hook (data-testid) on this element to keep the script stable.",
		ActionName:       step.ActionName,
		RecordedSelector: step.Selector,
		Confidence:       best.Confidence,
		ReviewAction:     "stability_advisory",
	}, true
}

// bestAlternate returns the strongest usable candidate that differs from the
// failed selector.
func bestAlternate(candidates []Candidate, failed string) (string, int, bool) {
	for _, candidate := range candidates {
		usable, ok := Usable(candidate)
		if !ok || usable == failed {
			continue
		}
		return usable, candidate.Confidence, true
	}
	return "", 0, false
}

// HasBlockingRepairs reports whether any suggestion is an error, mirroring
// correlate.HasBlockingRepairs for the API contract.
func HasBlockingRepairs(suggestions []RepairSuggestion) bool {
	for _, suggestion := range suggestions {
		if suggestion.Severity == SeverityError {
			return true
		}
	}
	return false
}

func severityRank(severity RepairSeverity) int {
	switch severity {
	case SeverityError:
		return 0
	case SeverityWarning:
		return 1
	default:
		return 2
	}
}
