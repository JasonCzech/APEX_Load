package browser

import (
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
	got := explicitNavigations(steps, navs)
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

	got := explicitNavigations(steps, navs)
	if len(got) != 1 || got[0] != 2 {
		t.Errorf("explicitNavigations() = %v, want [2]", got)
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
	if got := explicitNavigations(nil, nil); len(got) != 0 {
		t.Errorf("explicitNavigations(nil, nil) = %v, want empty", got)
	}
	navs := []NavEvent{{Timestamp: ts(500), URL: "https://x/"}}
	if got := explicitNavigations(nil, navs); len(got) != 0 {
		t.Errorf("initial-only navigation = %v, want empty", got)
	}
}
