package browser

import (
	"net/url"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// syncObservationWindow bounds how long after an interaction a navigation or
// network burst is attributed to that interaction.
const syncObservationWindow = 2 * time.Second

// minNetworkBurst is the number of requests inside the observation window that
// counts as action-triggered network activity rather than background noise
// (analytics beacons, polling).
const minNetworkBurst = 2

// inferSync derives the post-action sync policy for the step at index i from
// the recorded navigation and network event timeline. A main-frame navigation
// inside the window means the action triggered a page load; a network burst
// without navigation means SPA-style async work, which replay waits out with
// network_idle. Quiet windows get no sync policy — the engine's per-action
// auto-waits are enough.
func inferSync(steps []Step, i int, navs []NavEvent, network []time.Time) *dsl.BrowserSyncPolicy {
	if i < 0 || i >= len(steps) {
		return nil
	}
	step := steps[i]
	switch step.Kind {
	case StepClick, StepFill, StepPressKey, StepSelect, StepSetFiles:
		// Interactions that can trigger loads or async work (fills drive
		// autocomplete/search requests in SPAs).
	default:
		return nil
	}
	windowEnd := step.Timestamp.Add(syncObservationWindow)
	if i+1 < len(steps) && steps[i+1].Timestamp.Before(windowEnd) {
		windowEnd = steps[i+1].Timestamp
	}
	for _, nav := range navs {
		if nav.Timestamp.After(step.Timestamp) && !nav.Timestamp.After(windowEnd) {
			if sameDocumentURL(step.URL, nav.URL) {
				continue
			}
			return &dsl.BrowserSyncPolicy{Type: "load"}
		}
	}
	requests := 0
	for _, ts := range network {
		if ts.After(step.Timestamp) && !ts.After(windowEnd) {
			requests++
			if requests >= minNetworkBurst {
				return &dsl.BrowserSyncPolicy{Type: "network_idle"}
			}
		}
	}
	return nil
}

// explicitNavigations returns the indexes into navs that should become
// browser_navigate actions: navigations not attributable to a preceding
// interaction (typed URLs, bookmarks), excluding the initial navigation which
// the converter always emits from the session start URL. Before the first
// captured interaction, only the final distinct URL is kept so redirect chains
// collapse without losing a real user-initiated sidebar/bookmark navigation.
func explicitNavigations(steps []Step, navs []NavEvent) []int {
	out := make([]int, 0)
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
		if !attributed {
			out = append(out, i)
		}
	}
	return out
}

func lastPreFirstNavigationIndex(navs []NavEvent, firstStepAt time.Time, haveFirstStep bool) int {
	if !haveFirstStep {
		return -1
	}
	last := -1
	for i := 1; i < len(navs); i++ {
		if navs[i].Timestamp.Before(firstStepAt) {
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
