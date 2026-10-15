package engine

import (
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// resultPropertyDimensions is a result's promoted property dimensions and,
// within a browser iteration that can start cold or warm, which it was.
func (vu *VUser) resultPropertyDimensions(extractions map[string]string) map[string]string {
	dimensions := promotedPropertyDimensions(vu.Script, extractions)
	if vu.iterationTemperature == "" {
		return dimensions
	}
	if dimensions == nil {
		dimensions = make(map[string]string, 1)
	}
	dimensions[protocol.BrowserIterationDimension] = vu.iterationTemperature
	return dimensions
}

func promotedPropertyDimensions(script *dsl.Script, extractions map[string]string) map[string]string {
	if script == nil || len(script.Config.Results.PropertyAnalytics) == 0 {
		return nil
	}
	rules := script.Config.Results.PropertyAnalytics
	if len(dsl.ValidatePropertyAnalytics(rules)) > 0 {
		return nil
	}
	out := make(map[string]string, len(rules))
	for _, rule := range rules {
		value, exists := extractions[rule.Extraction]
		category := "other"
		if !exists {
			category = "missing"
		} else {
			for _, allowed := range rule.Values {
				if value == allowed {
					category = allowed
					break
				}
			}
		}
		out[rule.Name] = category
	}
	return out
}
