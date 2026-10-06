package api

// These bounded evidence slices can be nil in real typed Go responses. Their
// explicit JSON null is a valid empty population and preserves completeness
// fields; fixed-size distributions and unrelated model slices retain their
// existing schema. Required-field presence remains independent of nullability.
// A run's policy_pins are null for runs admitted before analysis policies.
func applyBrowserEvidenceWireNullability(pkg, name string, schema *oasSchema) {
	if schema == nil {
		return
	}
	if (pkg == "" || pkg == "api") && name == "RunArchiveProvenance" {
		// null marks a run admitted before analysis policies existed; [] means
		// no default policy was configured. Both are valid, so the always
		// present array is nullable.
		if property, ok := schema.Properties["policy_pins"]; ok && property.Type == "array" {
			property.Nullable = true
			schema.Properties["policy_pins"] = property
		}
		return
	}
	if pkg != "protocol" {
		return
	}
	field := ""
	switch name {
	case "BrowserDocumentSummary", "BrowserRuntimeMetricAvailability":
		field = "metrics"
	case "BrowserDocumentEvidence":
		field = "observations"
	case "BrowserResourceEvidence":
		field = "phases"
	}
	if field != "" {
		property, ok := schema.Properties[field]
		if ok && property.Type == "array" {
			property.Nullable = true
			schema.Properties[field] = property
		}
	}
}
