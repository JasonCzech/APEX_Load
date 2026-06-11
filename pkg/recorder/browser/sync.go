package browser

import (
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
// the converter always emits from the session start URL.
func explicitNavigations(steps []Step, navs []NavEvent) []int {
	out := make([]int, 0)
	for i, nav := range navs {
		if i == 0 {
			continue // initial goto, emitted from StartURL
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
