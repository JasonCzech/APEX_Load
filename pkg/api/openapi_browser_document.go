package api

// These bounded evidence slices can be nil in real typed Go responses. Their
// explicit JSON null is a valid empty population and preserves completeness
// fields; fixed-size distributions and unrelated model slices retain their
// existing schema. Required-field presence remains independent of nullability.
func applyBrowserEvidenceWireNullability(pkg, name string, schema *oasSchema) {
	if pkg != "protocol" || schema == nil {
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
