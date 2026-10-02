package api

import (
	"fmt"
	"math"
)

// BaselineTelemetryEvidence is whole-run retained telemetry, separate from the
// selected load windows. Threshold policy and publication cadence are not
// guaranteed comparable, so this evidence never changes regression verdicts.
type BaselineTelemetryEvidence struct {
	MetricIntervals     int64                       `json:"metric_intervals"`
	DroppedResults      *int64                      `json:"dropped_results,omitempty"`
	MaxSampleAgeMs      *float64                    `json:"max_sample_age_ms,omitempty"`
	RunID               string                      `json:"run_id"`
	RetainedIntervals   int64                       `json:"retained_intervals"`
	HealthIntervals     int64                       `json:"health_intervals"`
	HealthCoveragePct   *float64                    `json:"health_coverage_pct,omitempty"`
	BreachedIntervalPct *float64                    `json:"breached_interval_pct,omitempty"`
	Summary             *RunArchiveTelemetrySummary `json:"summary,omitempty"`
	Warnings            []string                    `json:"warnings"`
}

func baselineTelemetryEvidence(runID string, intervals []IntervalSummaryRecord) BaselineTelemetryEvidence {
	out := BaselineTelemetryEvidence{RunID: runID, RetainedIntervals: int64(len(intervals)), Warnings: []string{"Whole-run retained intervals; selected stable load windows do not apply. Threshold policies and sampling cadence may differ; advisory evidence only."}}
	sum := RunArchiveTelemetrySummary{}
	previousDrops := int64(-1)
	reset := false
	dropsAvailable := false
	invalidDrops := false
	for _, p := range intervals {
		if p.Telemetry != nil {
			out.MetricIntervals++
			if p.Telemetry.DroppedResults < 0 {
				invalidDrops = true
			} else if p.Telemetry.DroppedResultsAvailable || p.Telemetry.DroppedResults > 0 {
				dropsAvailable = true
				if previousDrops >= 0 && p.Telemetry.DroppedResults < previousDrops {
					reset = true
				}
				previousDrops = p.Telemetry.DroppedResults
			}
			age := p.Telemetry.SampleAgeMs
			if age >= 0 && !math.IsNaN(age) && !math.IsInf(age, 0) && (age > 0 || !p.Telemetry.CollectedAt.IsZero()) {
				if out.MaxSampleAgeMs == nil || age > *out.MaxSampleAgeMs {
					value := age
					out.MaxSampleAgeMs = &value
				}
			}
		}
		mergeRunArchiveTelemetrySummary(&sum, p)
		if p.TelemetryStatus != nil {
			switch p.TelemetryStatus.Status {
			case "healthy", "degraded", "breached":
				out.HealthIntervals++
			}
		}
	}
	if sum.Samples > 0 {
		out.Summary = finalizeRunArchiveTelemetrySummary(sum)
	}
	if len(intervals) > 0 {
		v := 100 * float64(out.HealthIntervals) / float64(len(intervals))
		out.HealthCoveragePct = &v
	}
	if dropsAvailable {
		value := sum.DroppedResults
		out.DroppedResults = &value
	}
	if out.Summary != nil && out.HealthIntervals == 0 {
		out.Summary.Status = "unavailable"
	}
	if out.HealthIntervals > 0 {
		v := 100 * float64(sum.BreachedIntervals) / float64(out.HealthIntervals)
		out.BreachedIntervalPct = &v
	}
	if out.HealthIntervals < int64(len(intervals)) || len(intervals) == 0 {
		out.Warnings = append(out.Warnings, "Health classification is missing for some or all retained intervals; absence is not healthy or zero.")
	}
	if !dropsAvailable {
		out.Warnings = append(out.Warnings, "Drop-counter availability was not retained; zero loss cannot be inferred from controller metadata.")
	}
	if invalidDrops {
		out.Warnings = append(out.Warnings, "Some retained drop counters are negative and were excluded as invalid evidence.")
	}
	if reset {
		out.Warnings = append(out.Warnings, "Dropped-result counter decreased; peak observed counters do not represent an exact loss total.")
	}
	return out
}

func baselineTelemetryLines(e BaselineTelemetryEvidence) []string {
	number := func(v *float64) string {
		if v == nil {
			return "Unavailable"
		}
		return fmt.Sprintf("%.3g", *v)
	}
	drops := "Unavailable"
	if e.DroppedResults != nil {
		drops = fmt.Sprint(*e.DroppedResults)
	}
	lines := []string{fmt.Sprintf("Whole-run telemetry %s: %d retained / %d classified intervals; coverage %s%%; breached classified intervals %s%%; peak observed drop counter %s; maximum sample age %s ms", e.RunID, e.RetainedIntervals, e.HealthIntervals, number(e.HealthCoveragePct), number(e.BreachedIntervalPct), drops, number(e.MaxSampleAgeMs))}
	return append(lines, e.Warnings...)
}
