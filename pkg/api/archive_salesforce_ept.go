package api

import (
	"fmt"
	"sort"

	browserprotocol "github.com/jasonczech/apexload/pkg/protocol/browser"
)

// A browser session in EPT calibration (config.browser.ept_calibration)
// reports Salesforce's Experienced Page Time counter in a step's evidence
// (protocol_evidence.salesforce_ept) when the counter changed. The run report
// sets each step's EPT readings beside the same steps' own times, so a
// qualification run shows how APEX's timings relate to the time Salesforce
// reports.

// maxSalesforceEPTSteps bounds the steps the report compares.
const maxSalesforceEPTSteps = 100

// SalesforceEPTCalibration is one step's EPT readings and the same steps'
// own times.
type SalesforceEPTCalibration struct {
	Step      string  `json:"step"`
	Samples   int64   `json:"samples"`
	AvgEPTMs  float64 `json:"avg_ept_ms"`
	AvgStepMs float64 `json:"avg_step_ms"`
	// AvgDifferenceMs is the step's time minus its EPT reading, on average.
	AvgDifferenceMs float64 `json:"avg_difference_ms"`
}

type salesforceEPTSums struct {
	samples int64
	eptMs   float64
	stepMs  float64
}

type archiveSalesforceEPTAccumulator struct {
	steps map[string]*salesforceEPTSums
}

func newArchiveSalesforceEPTAccumulator() *archiveSalesforceEPTAccumulator {
	return &archiveSalesforceEPTAccumulator{steps: map[string]*salesforceEPTSums{}}
}

func (a *archiveSalesforceEPTAccumulator) merge(tx TransactionEvent) {
	evidence, _ := tx.ProtocolEvidence[browserprotocol.SalesforceEPTEvidenceKey].(map[string]any)
	ept := evidenceFloat(evidence["ms"])
	if ept <= 0 || ept > browserprotocol.MaxSalesforceEPTMs || !tx.Success || tx.ActionName == "" || archiveEventPopulation(tx) != "action" {
		return
	}
	sums := a.steps[tx.ActionName]
	if sums == nil {
		if len(a.steps) >= maxSalesforceEPTSteps {
			return
		}
		sums = &salesforceEPTSums{}
		a.steps[tx.ActionName] = sums
	}
	sums.samples++
	sums.eptMs += ept
	sums.stepMs += tx.DurationMs
}

// result is each step's comparison, by name.
func (a *archiveSalesforceEPTAccumulator) result() []SalesforceEPTCalibration {
	if len(a.steps) == 0 {
		return nil
	}
	out := make([]SalesforceEPTCalibration, 0, len(a.steps))
	for name, sums := range a.steps {
		samples := float64(sums.samples)
		out = append(out, SalesforceEPTCalibration{
			Step: name, Samples: sums.samples,
			AvgEPTMs: sums.eptMs / samples, AvgStepMs: sums.stepMs / samples,
			AvgDifferenceMs: (sums.stepMs - sums.eptMs) / samples,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Step < out[j].Step })
	return out
}

// salesforceEPTCalibrationText is one step's comparison for the PDF report.
func salesforceEPTCalibrationText(row SalesforceEPTCalibration) string {
	return fmt.Sprintf("%s: %d readings, EPT avg %.1f ms, step avg %.1f ms, difference %+.1f ms", row.Step, row.Samples, row.AvgEPTMs, row.AvgStepMs, row.AvgDifferenceMs)
}
