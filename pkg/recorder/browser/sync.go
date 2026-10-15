package browser

import (
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// syncObservationWindow bounds how long after an interaction a navigation or
// network burst is attributed to that interaction.
const syncObservationWindow = 2 * time.Second

// inferSync uses the next recorded element as a readiness hint after async
// activity. Global network-idle is unsuitable for polling SPAs, and a frame
// navigation can be a same-document pushState rather than a document load.
// This is a hint, not a business assertion: operators should record assertions
// for the result of their transaction, particularly after the final action.
func inferSync(steps []Step, i int, navs []NavEvent, network []time.Time) *dsl.BrowserSyncPolicy {
	if i < 0 || i >= len(steps) {
		return nil
	}
	step := steps[i]
	switch step.Kind {
	case StepClick, StepFill, StepPressKey, StepSelect, StepSetFiles:
	default:
		return nil
	}
	// Never cross an excluded step, window switch, section, or frame boundary:
	// the sync policy executes in the triggering action's browsing context.
	if i+1 >= len(steps) {
		return nil
	}
	next := steps[i+1]
	if next.skipForConvert() || next.Kind == StepSwitchWindow || next.Kind == StepSetFiles ||
		normalizeSection(step.Section) != normalizeSection(next.Section) ||
		step.FrameSelector != next.FrameSelector || !slices.Equal(step.FramePath, next.FramePath) {
		return nil
	}
	selector := next.bestSelector()
	if selector == "" || selector == step.bestSelector() {
		return nil
	}
	// Bound attribution by the next interaction, not a two-second guess. A
	// single delayed fetch is enough; background traffic only adds a visible wait.
	active := false
	for _, ts := range network {
		if ts.After(step.Timestamp) && !ts.After(next.Timestamp) {
			active = true
			break
		}
	}
	for index, nav := range navs {
		if nav.Timestamp.After(step.Timestamp) && !nav.Timestamp.After(next.Timestamp) {
			// Such a navigation is emitted separately by explicitNavigations.
			// Waiting for its destination before that action would deadlock.
			// An app's route change never is: the app makes it again on replay.
			if !appRouteChange(navs, index) && nav.Timestamp.Sub(step.Timestamp) > syncObservationWindow {
				return nil
			}
			active = true
		}
	}
	if !active {
		return nil
	}
	return &dsl.BrowserSyncPolicy{Type: "element_visible", Value: selector}
}

// explicitNavigations returns the indexes into navs that should become
// browser_navigate actions: navigations not attributable to a preceding
// interaction (typed URLs, bookmarks), excluding the initial navigation which
// the converter always emits from the session start URL. Before the first
// captured interaction, only the final distinct URL is kept so redirect chains
// collapse without losing a real user-initiated sidebar/bookmark navigation.
// An app's route change never becomes one: loading its URL would reload a
// single-page app (Salesforce Lightning) that the replayed steps drive.
func explicitNavigations(steps []Step, navs []NavEvent) []int {
	explicit, _ := classifyNavigations(steps, navs)
	return explicit
}

// unattributedSameDocumentNavigations counts the app route changes that no
// recorded step made, which explicitNavigations leaves out.
func unattributedSameDocumentNavigations(steps []Step, navs []NavEvent) int {
	_, inPage := classifyNavigations(steps, navs)
	return inPage
}

// appRouteChange reports whether navs[index] is an app's in-page route change
// (history.pushState or replaceState). A fragment change loads nothing either
// way, so like a document load it can still be a navigation of the user's (a
// typed hash route).
func appRouteChange(navs []NavEvent, index int) bool {
	nav := navs[index]
	if !nav.SameDocument {
		return false
	}
	switch nav.NavigationType {
	case "historyApi":
		return true
	case "fragment":
		return false
	}
	// Chromium did not name it: more than the fragment changed.
	return index == 0 || !sameDocumentURL(navs[index-1].URL, nav.URL)
}

func classifyNavigations(steps []Step, navs []NavEvent) ([]int, int) {
	out := make([]int, 0)
	inPage := 0
	firstStepAt, haveFirstStep := firstConvertibleStepTimestamp(steps)
	lastPreFirst := lastPreFirstNavigationIndex(navs, firstStepAt, haveFirstStep)
	initialURL := ""
	if len(navs) > 0 {
		initialURL = navs[0].URL
	}
	for i, nav := range navs {
		if i == 0 {
			continue // initial goto, emitted from StartURL
		}
		if strings.TrimSpace(navs[i-1].URL) == strings.TrimSpace(nav.URL) && !convertibleStepBetween(steps, navs[i-1].Timestamp, nav.Timestamp) {
			// The page moved to the URL it was already on (a single-page app
			// restating its URL after a typed navigation); replaying it twice
			// only reloads the page. A changed fragment is a new route in a
			// hash-routed app, so it still counts.
			continue
		}
		if haveFirstStep && nav.Timestamp.Before(firstStepAt) {
			if i != lastPreFirst || sameDocumentURL(initialURL, nav.URL) {
				continue
			}
		}
		attributed := false
		for _, step := range steps {
			switch step.Kind {
			case StepClick, StepFill, StepPressKey, StepSelect, StepSetFiles:
			default:
				continue
			}
			if !nav.Timestamp.Before(step.Timestamp) && nav.Timestamp.Sub(step.Timestamp) <= syncObservationWindow {
				attributed = true
				break
			}
		}
		switch {
		case attributed:
		case appRouteChange(navs, i):
			inPage++
		default:
			out = append(out, i)
		}
	}
	return out, inPage
}

// convertibleStepBetween reports whether a step that becomes an action was
// recorded after from and no later than to.
func convertibleStepBetween(steps []Step, from, to time.Time) bool {
	for _, step := range steps {
		if !step.skipForConvert() && step.Timestamp.After(from) && !step.Timestamp.After(to) {
			return true
		}
	}
	return false
}

func lastPreFirstNavigationIndex(navs []NavEvent, firstStepAt time.Time, haveFirstStep bool) int {
	if !haveFirstStep {
		return -1
	}
	last := -1
	for i := 1; i < len(navs); i++ {
		// The app's own route changes after its load are not where the user
		// went.
		if navs[i].Timestamp.Before(firstStepAt) && !appRouteChange(navs, i) {
			last = i
		}
	}
	return last
}

func firstConvertibleStepTimestamp(steps []Step) (time.Time, bool) {
	for _, step := range steps {
		if step.skipForConvert() {
			continue
		}
		return step.Timestamp, true
	}
	return time.Time{}, false
}

func sameDocumentURL(before, after string) bool {
	before = strings.TrimSpace(before)
	after = strings.TrimSpace(after)
	if before == "" || after == "" {
		return false
	}
	left, errLeft := url.Parse(before)
	right, errRight := url.Parse(after)
	if errLeft != nil || errRight != nil || left == nil || right == nil {
		return false
	}
	left.Fragment = ""
	right.Fragment = ""
	return left.String() == right.String()
}
