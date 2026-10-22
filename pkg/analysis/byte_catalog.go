package analysis

// ByteEvidenceRequirement is the evidence byte metrics read: archived HTTP
// action observations whose writer stamped http_body.v1 byte evidence
// (protocol.ByteEvidenceHTTPBody). Bytes without it, an older writer's or
// another protocol's, are unavailable, never a measured zero (NA-C).
const ByteEvidenceRequirement = "byte_evidence.http_body.v1_raw_observations"

// byteMetricDefinitions are the byte metrics of the byte population, each
// HTTP action's body bytes: their sum per bucket, the per-request
// distribution and, as separate metrics, the sum's rate over the bucket.
// They are not transport bytes, and interval totals never stand in for them.
func byteMetricDefinitions(profiles []NativeQueryProfile) []MetricDefinition {
	var out []MetricDefinition
	for _, m := range []struct {
		name, unit string
		rate       bool
	}{
		{"bytes_sent", "bytes", false}, {"bytes_received", "bytes", false},
		{"bytes_sent_rate", "bytes/s", true}, {"bytes_received_rate", "bytes/s", true},
	} {
		d := MetricDefinition{Name: m.name, Version: CatalogVersion, InputKinds: []string{KindObservation}, Populations: []string{PopulationByte}, Unit: m.unit, NativeWireUnit: m.unit,
			Aggregations: []string{"sum", "avg", "min", "max", "p50", "p90", "p95", "p99"}, Derivations: []string{}, QueryProfiles: []string{},
			RequiredEvidence: []string{ByteEvidenceRequirement, "complete_bucket_for_sum"}, MissingRule: MissingUnscored, ResetRule: ResetNotApplicable}
		if m.rate {
			d.Aggregations = []string{"rate"}
			d.RequiredEvidence = []string{ByteEvidenceRequirement, "complete_elapsed_window"}
		}
		for _, p := range profiles {
			if p.Population == PopulationByte {
				d.QueryProfiles = append(d.QueryProfiles, p.ID)
			}
		}
		out = append(out, d)
	}
	return out
}
