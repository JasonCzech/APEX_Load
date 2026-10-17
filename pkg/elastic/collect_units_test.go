package elastic

import (
	"math"
	"testing"
)

func TestCollectionDurationUnitsKeepAdapterContractAndArithmetic(t *testing.T) {
	for _, tc := range []struct {
		unit, canonical string
		value, want     float64
	}{
		{"ns", "ms", 2000000, 2}, {"us", "ms", 2000, 2}, {"s", "ms", .002, 2},
		{"ms", "ms", 2, 2}, {"min", "min", 2, 2}, {"Seconds", "Seconds", 2, 2},
		{"count/5min", "count/5min", 2, 2}, {"ns", "ms", math.MaxFloat64, math.MaxFloat64 / 1e6},
	} {
		t.Run(tc.unit, func(t *testing.T) {
			if got := normalizedUnit(tc.unit); got != tc.canonical {
				t.Fatalf("unit=%q", got)
			}
			if got := scaleValue(tc.value, tc.unit); got != tc.want {
				t.Fatalf("value=%g, want %g", got, tc.want)
			}
		})
	}
}
