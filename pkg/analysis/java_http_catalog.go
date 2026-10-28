package analysis

// Separate population and closed profile prevent generic latency/rate adapters
// from interpreting representation counts as durations or load throughput.
func javaHTTPMetricDefinitions(profiles []NativeQueryProfile) []MetricDefinition {
	var out []MetricDefinition
	for _, m := range []struct {
		name, unit string
		pair       bool
	}{
		{"java_http_input_bytes", "bytes", false}, {"java_http_decoded_request_bytes", "bytes", false},
		{"java_http_prepared_body_bytes", "bytes", false}, {"java_http_retained_response_bytes", "bytes", false},
		{"java_http_response_base64_bytes", "bytes", false}, {"java_http_base64_decode_ms", "ms", false},
		{"java_http_base64_encode_ms", "ms", false}, {"java_http_enclosing_ms", "ms", false},
		{"java_http_decoded_per_encoded", "ratio", true}, {"java_http_prepared_per_decoded", "ratio", true},
		{"serialized_representation_vs_http_body", "ratio", true},
		{"java_http_base64_per_retained", "ratio", true}, {"java_http_base64_work_share", "ratio", true},
		{"java_shallow_object_bytes", "bytes", false}, {"java_reachable_graph_bytes", "bytes", false},
		{"java_serialization_ms", "ms", false}, {"java_deserialization_ms", "ms", false},
	} {
		d := MetricDefinition{Name: m.name, Version: "java_http.diagnostics.v1", InputKinds: []string{KindObservation}, Populations: []string{PopulationJavaHTTPAction}, Unit: m.unit, NativeWireUnit: m.unit, Aggregations: []string{"avg", "min", "max", "p50", "p90", "p95", "p99"}, Derivations: []string{}, RequiredEvidence: []string{"java_http_diagnostics.v1", "same_action_population", "complete_selected_observations"}, MissingRule: MissingUnscored, ResetRule: ResetNotApplicable, Baseline: true}
		if m.pair {
			d.Aggregations = []string{"ratio_of_sums"}
			d.RequiredEvidence = append(d.RequiredEvidence, "java_http_diagnostics.v1_same_sample_pairs")
		}
		for _, p := range profiles {
			if p.Population == PopulationJavaHTTPAction {
				d.QueryProfiles = append(d.QueryProfiles, p.ID)
			}
		}
		out = append(out, d)
	}
	return out
}
