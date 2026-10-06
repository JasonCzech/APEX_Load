package collectoragent

import (
	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/credential"
)

func scrubElasticEvidence(e analysis.ProviderEvidence) analysis.ProviderEvidence {
	e.DeepLink = ""
	e.Summary = analysis.ScrubProviderText(e.Summary)
	e.Service = analysis.ScrubProviderText(e.Service)
	e.Status = analysis.ScrubProviderText(e.Status)
	e.Severity = analysis.ScrubProviderText(e.Severity)
	attributes := func(values map[string]analysis.ScalarValue) {
		for key, value := range values {
			if credential.SensitiveName(key) {
				value = analysis.ScalarValue{Kind: "string", StringValue: "[REDACTED]"}
			} else if value.Kind == "string" {
				value.StringValue = analysis.ScrubProviderText(value.StringValue)
			}
			values[key] = value
		}
	}
	if e.Log != nil {
		e.Log.Message = analysis.ScrubProviderText(e.Log.Message)
		e.Log.Level = analysis.ScrubProviderText(e.Log.Level)
		attributes(e.Log.Attributes)
	}
	if e.Span != nil {
		e.Span.Operation = analysis.ScrubProviderText(e.Span.Operation)
		attributes(e.Span.Attributes)
	}
	if e.Transaction != nil {
		e.Transaction.Name = analysis.ScrubProviderText(e.Transaction.Name)
		attributes(e.Transaction.Attributes)
	}
	for key, value := range e.Properties {
		if credential.SensitiveName(key) {
			value = "[REDACTED]"
		} else {
			value = analysis.ScrubProviderText(value)
		}
		e.Properties[key] = value
	}
	return e
}
