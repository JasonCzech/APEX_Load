package api

import (
	"github.com/jasonczech/apexload/pkg/rca"
	"testing"
)

func TestReviewMainRCAEffectPreservesIntegerZeros(t *testing.T) {
	for _, tc := range []struct {
		name   string
		effect float64
		want   string
	}{
		{"hundred", 100, "+100 ms"},
		{"hundred_twenty", 120, "+120 ms"},
		{"thousand", 1000, "+1000 ms"},
		{"negative", -120, "-120 ms"},
		{"zero", 0, "0 ms"},
		{"fraction", 12.5, "+12.5 ms"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := rcaEffectText(rca.Anomaly{Effect: tc.effect, Unit: "ms"}); got != tc.want {
				t.Fatalf("effect %v: got %q, want %q", tc.effect, got, tc.want)
			}
		})
	}
}
