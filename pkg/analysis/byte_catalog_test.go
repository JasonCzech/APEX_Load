package analysis

import (
	"slices"
	"testing"
)

// Byte metrics declare what runs: one archive-selection profile of the byte
// population, sums and per-request distributions in bytes, rates in bytes/s,
// with no derivation, baseline or correlation (NA-C).
func TestByteMetricsAdvertiseTheArchiveProfile(t *testing.T) {
	catalog := Catalog()
	var profile *NativeQueryProfile
	for i := range catalog.QueryProfiles {
		if catalog.QueryProfiles[i].Population == PopulationByte {
			if profile != nil {
				t.Fatal("more than one byte profile")
			}
			profile = &catalog.QueryProfiles[i]
		}
	}
	if profile == nil || profile.EvidenceMode != "" || !slices.Equal(profile.AllowedFilters, []string{"action"}) || len(profile.RequiredFilters) != 0 || !slices.Equal(profile.RequiredEvidence, []string{ByteEvidenceRequirement}) {
		t.Fatalf("byte profile %+v", profile)
	}
	for name, unit := range map[string]string{"bytes_sent": "bytes", "bytes_received": "bytes", "bytes_sent_rate": "bytes/s", "bytes_received_rate": "bytes/s"} {
		metric, found := LookupMetric(name)
		if !found || metric.Unit != unit || !slices.Equal(metric.Populations, []string{PopulationByte}) || !slices.Equal(metric.QueryProfiles, []string{profile.ID}) || len(metric.Derivations) != 0 || metric.Baseline || metric.Correlation {
			t.Fatalf("%s = %+v", name, metric)
		}
		for _, aggregation := range metric.Aggregations {
			if !slices.Contains(profile.AllowedAggregations, aggregation) {
				t.Fatalf("%s aggregation %s is not executable", name, aggregation)
			}
		}
		rate := name == "bytes_sent_rate" || name == "bytes_received_rate"
		if rate != slices.Equal(metric.Aggregations, []string{"rate"}) {
			t.Fatalf("%s aggregations %v", name, metric.Aggregations)
		}
	}
}
