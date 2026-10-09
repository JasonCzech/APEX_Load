package api

import (
	"fmt"
	"testing"
)

func TestComparisonNumberNeverUsesExponentNotation(t *testing.T) {
	cases := []struct {
		format string
		value  float64
		want   string
	}{
		{"%+v ms", 1406.2, "+1406 ms"},
		{"%+v", -12.34, "-12.3"},
		{"%v", 0.0042, "0.0042"},
		{"%v", 2.5, "2.5"},
		{"%+v", 0, "+0"},
		{"%v", -0.0001, "-0.0001"},
		{"%+v", 0.0004, "+0.0004"},
		{"%v", 0.12345, "0.123"},
		{"%v", 1571.6, "1572"},
	}
	for _, tc := range cases {
		if got := fmt.Sprintf(tc.format, comparisonNumber(tc.value)); got != tc.want {
			t.Errorf("Sprintf(%q, %v) = %q, want %q", tc.format, tc.value, got, tc.want)
		}
	}
}
