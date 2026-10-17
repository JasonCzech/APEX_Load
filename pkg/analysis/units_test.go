package analysis

import (
	"math"
	"testing"
)

func TestDurationFactsPreserveCanonicalMeasurementConversions(t *testing.T) {
	for _, tc := range []struct {
		unit                   string
		seconds, factor, value float64
	}{
		{"ns", 1e-9, 1e-6, 2000000}, {"us", 1e-6, .001, 2000}, {"ms", .001, 1, 2}, {"s", 1, 1000, .002}, {"min", 60, 60000, 1.0 / 30000},
	} {
		t.Run(tc.unit, func(t *testing.T) {
			seconds, ok := DurationSeconds(tc.unit)
			if !ok || seconds != tc.seconds {
				t.Fatalf("seconds=%v %t", seconds, ok)
			}
			factor, ok := DurationScale(tc.unit, "ms")
			if !ok || math.Abs(factor-tc.factor) > math.Abs(tc.factor)*1e-15 {
				t.Fatalf("scale=%v %t", factor, ok)
			}
			value, ok := ConvertDuration(tc.value, tc.unit, "ms")
			if !ok || math.Abs(value-2) > 1e-12 {
				t.Fatalf("converted=%v %t", value, ok)
			}
		})
	}
	for _, unit := range []string{"seconds", "MicroSecond", "count/s", "MS", ""} {
		if _, ok := DurationSeconds(unit); ok {
			t.Fatalf("adapter spelling %q became a canonical fact", unit)
		}
	}
	if _, ok := DurationScale("s", "unknown"); ok {
		t.Fatal("unknown destination accepted")
	}
}

func TestDurationFactsKeepParquetAllowlistAndScales(t *testing.T) {
	for unit, want := range map[string]float64{"ns": .000001, "us": .001, "s": 1000} {
		canonical, scale, err := NormalizeUnit(unit)
		if err != nil || canonical != "ms" || scale != want {
			t.Fatalf("unit %s=%s %g %v", unit, canonical, scale, err)
		}
	}
	if _, _, err := NormalizeUnit("min"); err == nil {
		t.Fatal("physical fact silently widened the parquet contract")
	}
}
