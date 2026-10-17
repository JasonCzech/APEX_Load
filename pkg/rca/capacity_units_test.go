package rca

import "testing"

func TestCapacityDurationAliasesRemainDistinctFromProviderUnits(t *testing.T) {
	for _, tc := range []struct {
		unit    string
		seconds float64
		valid   bool
	}{
		{"s", 1, true}, {" Seconds ", 1, true}, {"millisecond", 1e-3, true},
		{"US", 1e-6, true}, {"µs", 1e-6, true}, {"minutes", 60, true},
		{"ns", 0, false}, {"count/s", 0, false}, {"", 0, false},
	} {
		t.Run(tc.unit, func(t *testing.T) {
			got, valid := secondsPer(tc.unit)
			if got != tc.seconds || valid != tc.valid {
				t.Fatalf("seconds=%g valid=%t", got, valid)
			}
		})
	}
}
