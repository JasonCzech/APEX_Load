package browser

import (
	"testing"
	"time"
)

func ts(ms int64) time.Time { return time.UnixMilli(ms) }

func TestInferSync(t *testing.T) {
	steps := []Step{
		{Seq: 1, Kind: StepClick, Timestamp: ts(1000), URL: "https://x/current"},
		{Seq: 2, Kind: StepFill, Timestamp: ts(5000)},
		{Seq: 3, Kind: StepClick, Timestamp: ts(9000)},
	}
	tests := []struct {
		name    string
		index   int
		navs    []NavEvent
		network []time.Time
		want    string // sync type, "" = nil
	}{
		{
			name:    "navigation inside window wins",
			index:   0,
			navs:    []NavEvent{{Timestamp: ts(1500), URL: "https://x/next"}},
			network: []time.Time{ts(1200), ts(1300)},
			want:    "load",
		},
		{
			name:  "same document hash navigation does not force load",
			index: 0,
			navs:  []NavEvent{{Timestamp: ts(1500), URL: "https://x/current#details"}},
			want:  "",
		},
		{
			name:    "network burst without navigation",
			index:   0,
			network: []time.Time{ts(1200), ts(1400)},
			want:    "network_idle",
		},
		{
			name:    "single request is background noise",
			index:   0,
			network: []time.Time{ts(1200)},
			want:    "",
		},
		{
			name:    "activity after window ignored",
			index:   0,
			navs:    []NavEvent{{Timestamp: ts(4000), URL: "https://x/late"}},
			network: []time.Time{ts(3500), ts(3600)},
			want:    "",
		},
		{
			name:    "window clamps at next step",
			index:   1, // fill at 5000, next step at 9000 > 5000+2s so window is 7000
			network: []time.Time{ts(6500), ts(6900)},
			want:    "network_idle",
		},
		{
			name:    "activity before step ignored",
			index:   2,
			network: []time.Time{ts(8500), ts(8900)},
			want:    "",
		},
		{
			name:  "fill is an interaction kind",
			index: 1,
			navs:  []NavEvent{{Timestamp: ts(5500), URL: "https://x/f"}},
			want:  "load",
		},
		{
			name:  "out of range index",
			index: 7,
			navs:  []NavEvent{{Timestamp: ts(1500), URL: "https://x"}},
			want:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := inferSync(steps, tt.index, tt.navs, tt.network)
			gotType := ""
			if got != nil {
				gotType = got.Type
			}
			if gotType != tt.want {
				t.Errorf("inferSync() = %q, want %q", gotType, tt.want)
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
