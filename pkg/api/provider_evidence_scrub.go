package api

import (
	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/internal/boundedtext"
)

// Re-scrub retained projections at disclosure boundaries. Immutable artifacts
// from older versions retain compatibility while gaining current redactions.
func scrubRetainedProviderEvidence(e analysis.ProviderEvidence) analysis.ProviderEvidence {
	e.Summary = scrubElasticTextBounded(e.Summary, 4096)
	e.Status = scrubElasticTextBounded(e.Status, 512)
	e.Severity = scrubElasticTextBounded(e.Severity, 512)
	if e.Provider != "elasticsearch" && e.Provider != "opensearch" {
		return e
	}
	e.DeepLink = ""
	e.Service = scrubElasticTextBounded(e.Service, 512)
	properties := make(map[string]string, len(e.Properties))
	for key, value := range e.Properties {
		if credential.SensitiveName(key) {
			properties[key] = "[REDACTED]"
		} else {
			properties[key] = scrubElasticTextBounded(value, 4096)
		}
	}
	e.Properties = properties
	attrs := func(input map[string]analysis.ScalarValue) map[string]analysis.ScalarValue {
		output := make(map[string]analysis.ScalarValue, len(input))
		for key, value := range input {
			if credential.SensitiveName(key) {
				value = analysis.ScalarValue{Kind: "string", StringValue: "[REDACTED]"}
			} else if value.Kind == "string" {
				value.StringValue = scrubElasticTextBounded(value.StringValue, 4096)
			}
			output[key] = value
		}
		return output
	}
	if e.Log != nil {
		record := *e.Log
		record.Message = scrubElasticTextBounded(record.Message, 4096)
		record.Level = scrubElasticTextBounded(record.Level, 128)
		record.Attributes = attrs(record.Attributes)
		e.Log = &record
	}
	if e.Span != nil {
		record := *e.Span
		record.Operation = scrubElasticTextBounded(record.Operation, 256)
		record.Attributes = attrs(record.Attributes)
		e.Span = &record
	}
	if e.Transaction != nil {
		record := *e.Transaction
		record.Name = scrubElasticTextBounded(record.Name, 256)
		record.Attributes = attrs(record.Attributes)
		e.Transaction = &record
	}
	return e
}

func scrubElasticTextBounded(value string, limit int) string {
	return boundedtext.TruncateUTF8(analysis.ScrubProviderText(value), limit)
}
