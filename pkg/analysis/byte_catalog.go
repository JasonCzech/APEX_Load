package analysis

// ByteEvidenceRequirement and its protocol peers name the evidence byte
// metrics read: archived HTTP body bytes (http_body.v1), gRPC uncompressed
// protobuf payload bytes (grpc_uncompressed_payload.v1), WebSocket application
// message payload bytes (ws_application_payload.v1), and live interval byte
// totals (NA-C). Bytes without a matching provenance stamp are unavailable,
// never a measured zero.
const (
	ByteEvidenceRequirement          = "byte_evidence.http_body.v1_raw_observations"
	ByteEvidenceGRPCRequirement      = "byte_evidence.grpc_uncompressed_payload.v1_raw_observations"
	ByteEvidenceWebSocketRequirement = "byte_evidence.ws_application_payload.v1_raw_observations"
	ByteEvidenceLiveRequirement      = "byte_evidence.live_interval_totals.v1"
)

// byteMetricDefinitions are the byte metrics of the byte population across
// HTTP body bytes, gRPC uncompressed payload bytes, and WebSocket application
// payload bytes: their sum per bucket, the per-request/per-call/per-message
// distribution from retained observations, and, as separate metrics, the sum's
// rate over complete buckets. Live interval byte totals support only sum and
// rate over complete buckets and never synthesize per-request distributions or
// wire/transport byte estimates.
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
			RequiredEvidence: []string{ByteEvidenceRequirement, ByteEvidenceGRPCRequirement, ByteEvidenceWebSocketRequirement, ByteEvidenceLiveRequirement, "complete_bucket_for_sum"}, MissingRule: MissingUnscored, ResetRule: ResetNotApplicable}
		if m.rate {
			d.Aggregations = []string{"rate"}
			d.RequiredEvidence = []string{ByteEvidenceRequirement, ByteEvidenceGRPCRequirement, ByteEvidenceWebSocketRequirement, ByteEvidenceLiveRequirement, "complete_elapsed_window"}
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
