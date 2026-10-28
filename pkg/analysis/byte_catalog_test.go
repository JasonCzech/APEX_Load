package analysis

import (
	"slices"
	"testing"
)

// Byte metrics declare what runs: archive-selection and archive-grouped
// profiles with sums, per-request distributions in bytes, and rates in bytes/s,
// plus live source/merged interval profiles restricted to sum and rate over
// complete buckets (NA-C1).
func TestByteMetricsAdvertiseArchiveAndLiveProfiles(t *testing.T) {
	catalog := Catalog()
	var byteProfiles []NativeQueryProfile
	var archiveExact *NativeQueryProfile
	for i := range catalog.QueryProfiles {
		p := catalog.QueryProfiles[i]
		if p.Population == PopulationByte {
			byteProfiles = append(byteProfiles, p)
			if p.EvidenceMode == "" && p.Grouping == "" {
				archiveExact = &catalog.QueryProfiles[i]
			}
			if p.EvidenceMode == "live" && !slices.Equal(p.AllowedAggregations, []string{"sum", "rate"}) {
				t.Fatalf("live byte profile %s must allow only sum and rate, got %v", p.ID, p.AllowedAggregations)
			}
		}
	}
	if archiveExact == nil || !slices.Equal(archiveExact.AllowedFilters, []string{"action", "protocol"}) || len(archiveExact.RequiredFilters) != 0 || !slices.Equal(archiveExact.RequiredEvidence, []string{ByteEvidenceRequirement, ByteEvidenceGRPCRequirement, ByteEvidenceWebSocketRequirement}) {
		t.Fatalf("archive byte profile %+v", archiveExact)
	}
	if len(byteProfiles) != 6 {
		t.Fatalf("expected 6 byte profiles (archive exact, archive action, live source/merged exact/action), got %d", len(byteProfiles))
	}
	wantProfileIDs := make([]string, 0, len(byteProfiles))
	for _, p := range byteProfiles {
		wantProfileIDs = append(wantProfileIDs, p.ID)
	}
	for name, unit := range map[string]string{"bytes_sent": "bytes", "bytes_received": "bytes", "bytes_sent_rate": "bytes/s", "bytes_received_rate": "bytes/s"} {
		metric, found := LookupMetric(name)
		if !found || metric.Unit != unit || !slices.Equal(metric.Populations, []string{PopulationByte}) || !slices.Equal(metric.QueryProfiles, wantProfileIDs) || len(metric.Derivations) != 0 || metric.Baseline || metric.Correlation {
			t.Fatalf("%s = %+v", name, metric)
		}
		for _, aggregation := range metric.Aggregations {
			if !slices.Contains(archiveExact.AllowedAggregations, aggregation) {
				t.Fatalf("%s aggregation %s is not executable", name, aggregation)
			}
		}
		rate := name == "bytes_sent_rate" || name == "bytes_received_rate"
		if rate != slices.Equal(metric.Aggregations, []string{"rate"}) {
			t.Fatalf("%s aggregations %v", name, metric.Aggregations)
		}
	}
}
