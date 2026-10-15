package browser

import (
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/selector"
)

func ts(ms int64) time.Time { return time.UnixMilli(ms) }

func TestInferSync(t *testing.T) {
	for _, tc := range []struct {
		name    string
		network []time.Time
		navs    []NavEvent
		mutate  func([]Step)
		want    bool
	}{
		{name: "single delayed fetch", network: []time.Time{ts(4500)}, want: true},
		{name: "polling burst", network: []time.Time{ts(1200), ts(1400)}, want: true},
		{name: "SPA navigation", navs: []NavEvent{{Timestamp: ts(1500), URL: "https://x/next"}}, want: true},
		{name: "quiet", want: false},
		{name: "explicit later navigation", navs: []NavEvent{{Timestamp: ts(4000), URL: "https://x/manual"}}},
		{name: "SPA navigation followed by explicit navigation", navs: []NavEvent{{Timestamp: ts(1500), URL: "https://x/spa"}, {Timestamp: ts(4000), URL: "https://x/manual"}}},
		{name: "hidden file input", network: []time.Time{ts(1200)}, mutate: func(s []Step) { s[1].Kind = StepSetFiles }},
		{name: "before action", network: []time.Time{ts(500)}},
		{name: "after next action", network: []time.Time{ts(6000)}},
		{name: "other frame", network: []time.Time{ts(1200)}, mutate: func(s []Step) { s[1].FrameSelector = "iframe" }},
		{name: "other window", network: []time.Time{ts(1200)}, mutate: func(s []Step) { s[1].Kind = StepSwitchWindow }},
		{name: "excluded", network: []time.Time{ts(1200)}, mutate: func(s []Step) { s[1].Excluded = true }},
		{name: "same element", network: []time.Time{ts(1200)}, mutate: func(s []Step) { s[1].Candidates = s[0].Candidates }},
		{name: "other section", network: []time.Time{ts(1200)}, mutate: func(s []Step) { s[1].Section = SectionTeardown }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			steps := []Step{
				{Kind: StepClick, Timestamp: ts(1000), Candidates: []selector.Candidate{{Kind: "css", Value: "#go", Confidence: 90}}},
				{Kind: StepClick, Timestamp: ts(5000), Candidates: []selector.Candidate{{Kind: "css", Value: "#result", Confidence: 90}}},
			}
			if tc.mutate != nil {
				tc.mutate(steps)
			}
			got := inferSync(steps, 0, tc.navs, tc.network)
			if tc.want {
				if got == nil || got.Type != "element_visible" || got.Value != "#result" {
					t.Fatalf("sync = %+v", got)
				}
			} else if got != nil {
				t.Fatalf("unexpected sync: %+v", got)
			}
			if got := inferSync(steps, 1, tc.navs, tc.network); got != nil {
				t.Fatalf("final action sync = %+v", got)
			}
		})
	}
}

func TestInferSyncNonInteractionKinds(t *testing.T) {
	steps := []Step{{Seq: 1, Kind: StepAssert, Timestamp: ts(1000)}}
	navs := []NavEvent{{Timestamp: ts(1100), URL: "https://x"}}
	if got := inferSync(steps, 0, navs, nil); got != nil {
		t.Errorf("inferSync() for assert = %+v, want nil", got)
	}
}

func TestExplicitNavigations(t *testing.T) {
	steps := []Step{
		{Seq: 1, Kind: StepClick, Timestamp: ts(1000), URL: "https://x/"},
	}
	navs := []NavEvent{
		{Timestamp: ts(100), URL: "https://x/"},         // initial goto, always excluded
		{Timestamp: ts(500), URL: "https://x/redirect"}, // final pre-step URL, explicit
		{Timestamp: ts(1500), URL: "https://x/next"},    // click-attributed
		{Timestamp: ts(60000), URL: "https://x/typed"},  // typed URL, explicit
	}
	got, _ := explicitNavigations(steps, navs)
	if len(got) != 2 || got[0] != 1 || got[1] != 3 {
		t.Errorf("explicitNavigations() = %v, want [1 3]", got)
	}
}

func TestExplicitNavigationsKeepsOnlyFinalPreFirstNavigation(t *testing.T) {
	steps := []Step{
		{Seq: 1, Kind: StepClick, Timestamp: ts(1000), URL: "https://x/app"},
	}
	navs := []NavEvent{
		{Timestamp: ts(100), URL: "https://x/"},
		{Timestamp: ts(300), URL: "https://x/loading"},
		{Timestamp: ts(600), URL: "https://x/app"},
	}

	got, _ := explicitNavigations(steps, navs)
	if len(got) != 1 || got[0] != 2 {
		t.Errorf("explicitNavigations() = %v, want [2]", got)
	}
}

// A single-page app restates the URL it was just navigated to (a typed
// navigation in a recorded Tableau Public walk became two navigate actions);
// the repeat is not a navigation of its own, unless a step came between. A
// changed fragment is a new route in a hash-routed app.
func TestExplicitNavigationsSkipsRepeatedURLs(t *testing.T) {
	steps := []Step{
		{Seq: 1, Kind: StepClick, Timestamp: ts(1000), URL: "https://x/"},
		{Seq: 2, Kind: StepClick, Timestamp: ts(80000), URL: "https://x/day"},
	}
	navs := []NavEvent{
		{Timestamp: ts(100), URL: "https://x/"},
		{Timestamp: ts(60000), URL: "https://x/day"},       // typed URL, explicit
		{Timestamp: ts(60400), URL: "https://x/day"},       // restated, skipped
		{Timestamp: ts(61000), URL: "https://x/day#/week"}, // new hash route, explicit
		{Timestamp: ts(90000), URL: "https://x/day#/week"}, // after a step, explicit
	}
	got, _ := explicitNavigations(steps, navs)
	if len(got) != 3 || got[0] != 1 || got[1] != 3 || got[2] != 4 {
		t.Errorf("explicitNavigations() = %v, want [1 3 4]", got)
	}
}

// An app's in-page route change (Lightning moving between its pages) is never
// loaded as a navigation, even long after the click that made it; a fragment
// change, which loads nothing, still can be. Before the first step, the last
// document load counts, not the app's route change after it.
func TestExplicitNavigationsLeaveAppRouteChangesOut(t *testing.T) {
	steps := []Step{
		{Seq: 1, Kind: StepClick, Timestamp: ts(1000), URL: "https://x/lightning/page/home", Candidates: []selector.Candidate{{Kind: "css", Value: "#account", Confidence: 90}}},
		{Seq: 2, Kind: StepClick, Timestamp: ts(20000), URL: "https://x/lightning/r/Account/001/view", Candidates: []selector.Candidate{{Kind: "css", Value: "#related", Confidence: 90}}},
	}
	navs := []NavEvent{
		{Timestamp: ts(100), URL: "https://x/"},
		{Timestamp: ts(400), URL: "https://x/lightning/page/home"},                                                        // typed, a document load
		{Timestamp: ts(600), URL: "https://x/lightning/page/home?ws=1", SameDocument: true, NavigationType: "historyApi"}, // the app restates it
		{Timestamp: ts(4000), URL: "https://x/lightning/r/Account/001/view", SameDocument: true, NavigationType: "historyApi"},
		{Timestamp: ts(9000), URL: "https://x/lightning/r/Account/001/related", SameDocument: true},      // unnamed, path changed
		{Timestamp: ts(12000), URL: "https://x/lightning/r/Account/001/related#tab", SameDocument: true}, // unnamed, fragment only
		{Timestamp: ts(15000), URL: "https://x/lightning/r/Account/001/related#notes", SameDocument: true, NavigationType: "fragment"},
	}
	got, _ := explicitNavigations(steps, navs)
	if len(got) != 3 || got[0] != 1 || got[1] != 5 || got[2] != 6 {
		t.Fatalf("explicitNavigations() = %v, want [1 5 6]", got)
	}
	if _, inPage := explicitNavigations(steps, navs); len(inPage) != 2 || inPage[0] != 3 || inPage[1] != 4 {
		t.Fatalf("route changes left out = %v, want [3 4]", inPage)
	}
	// The conversion says how many it left out, of those among the steps it
	// converts.
	session := &Session{StartURL: "https://x/", Steps: steps, NavEvents: navs}
	if warnings := strings.Join(Convert(session, ConvertOptions{}).Warnings, "\n"); !strings.Contains(warnings, "2 route change(s)") {
		t.Fatalf("warnings = %s", warnings)
	}
	if warnings := strings.Join(Convert(session, ConvertOptions{StepRange: StepRange{EndSeq: 1}}).Warnings, "\n"); strings.Contains(warnings, "route change(s)") {
		t.Fatalf("a range without route changes warned: %s", warnings)
	}
	// A route change long after the click still lets the next element sync.
	sync := inferSync(steps, 0, navs[3:5], nil)
	if sync == nil || sync.Type != "element_visible" {
		t.Fatalf("sync = %+v", sync)
	}
}

func TestSameDocumentURL(t *testing.T) {
	if !sameDocumentURL("https://x/app?q=1#before", "https://x/app?q=1#after") {
		t.Fatal("sameDocumentURL should ignore fragments")
	}
	if sameDocumentURL("https://x/app?q=1", "https://x/app?q=2") {
		t.Fatal("sameDocumentURL should preserve query differences")
	}
}

func TestExplicitNavigationsEmpty(t *testing.T) {
	if got, _ := explicitNavigations(nil, nil); len(got) != 0 {
		t.Errorf("explicitNavigations(nil, nil) = %v, want empty", got)
	}
	navs := []NavEvent{{Timestamp: ts(500), URL: "https://x/"}}
	if got, _ := explicitNavigations(nil, navs); len(got) != 0 {
		t.Errorf("initial-only navigation = %v, want empty", got)
	}
}
