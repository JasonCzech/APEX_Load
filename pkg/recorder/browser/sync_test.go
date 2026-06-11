package browser

import (
	"testing"
	"time"
)

func ts(ms int64) time.Time { return time.UnixMilli(ms) }

func TestInferSync(t *testing.T) {
	steps := []Step{
		{Seq: 1, Kind: StepClick, Timestamp: ts(1000)},
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
		{Seq: 1, Kind: StepClick, Timestamp: ts(1000)},
	}
	navs := []NavEvent{
		{Timestamp: ts(500), URL: "https://x/"},        // initial goto, always excluded
		{Timestamp: ts(1500), URL: "https://x/next"},   // click-attributed
		{Timestamp: ts(60000), URL: "https://x/typed"}, // typed URL, explicit
	}
	got := explicitNavigations(steps, navs)
	if len(got) != 1 || got[0] != 2 {
		t.Errorf("explicitNavigations() = %v, want [2]", got)
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
