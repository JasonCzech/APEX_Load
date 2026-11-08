package api

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	// agentSeriesMaxPoints bounds a finding's series.
	agentSeriesMaxPoints = 60
	// agentFindingDetailBytes bounds a finding detail's JSON.
	agentFindingDetailBytes = 40960
	// agentFindingUntrustedRunes bounds a detail's untrusted sample.
	agentFindingUntrustedRunes = 2048
)

// buildAgentFindingDetail is one finding of an unbounded brief with its
// series, samples and related findings.
func buildAgentFindingDetail(brief *AgentBrief, report *RunArchiveReport, findingID string) (*AgentFindingDetail, bool) {
	var finding *AgentFinding
	for i := range brief.Findings {
		if brief.Findings[i].ID == findingID {
			finding = &brief.Findings[i]
			break
		}
	}
	if finding == nil {
		return nil, false
	}
	detail := &AgentFindingDetail{
		Schema:     agentFindingDetailSchema,
		RunID:      brief.Run.ID,
		Finding:    *finding,
		Samples:    []AgentUntrusted{},
		Excerpts:   []RCAEvidenceExcerpt{},
		Related:    []string{},
		Truncation: AgentTruncation{Omitted: map[string]int{}},
	}
	if finding.Untrusted != nil {
		detail.Samples = append(detail.Samples, *finding.Untrusted)
		detail.Finding.Untrusted = nil
	}
	switch finding.Kind {
	case agentKindErrorGroup:
		detail.Samples = agentErrorSamples(report, finding.ID)
	case agentKindEvidenceGap:
		// The brief trims the message; the detail keeps it at full size.
		for _, gap := range report.Completeness.Findings {
			if agentFindingID(agentKindEvidenceGap, gap.Dimension) == finding.ID {
				if sample := agentUntrustedText("completeness", gap.Message, agentFindingUntrustedRunes); sample != nil {
					detail.Samples = []AgentUntrusted{*sample}
				}
				break
			}
		}
	}
	detail.Series = agentFindingSeries(report, *finding)
	for _, other := range brief.Findings {
		if other.ID == finding.ID {
			continue
		}
		if finding.Scope.Transaction != "" && other.Scope.Transaction == finding.Scope.Transaction {
			detail.Related = append(detail.Related, other.ID)
		}
	}
	// Bound the detail: drop series points, then samples, from the end.
	for {
		body, _ := json.Marshal(detail)
		if len(body) <= agentFindingDetailBytes {
			break
		}
		switch {
		case detail.Series != nil && len(detail.Series.Points) > 10:
			detail.Truncation.Omitted["series_points"] += len(detail.Series.Points) / 2
			detail.Series.Points = detail.Series.Points[:len(detail.Series.Points)/2]
		case len(detail.Samples) > 0:
			detail.Samples = detail.Samples[:len(detail.Samples)-1]
			detail.Truncation.Omitted["samples"]++
		case len(detail.Related) > 0:
			detail.Truncation.Omitted["related"] += len(detail.Related)
			detail.Related = []string{}
		default:
			detail.Truncation.Truncated = true
			detail.Truncation.HowToGetMore = "the evidence URIs serve the full data"
			return detail, true
		}
	}
	detail.Truncation.Truncated = len(detail.Truncation.Omitted) > 0
	if detail.Truncation.Truncated {
		detail.Truncation.HowToGetMore = "the evidence URIs serve the full data"
	}
	return detail, true
}

// agentErrorSamples is the error pattern's sample, any captured HTTP network
// trace from its snapshot entries, and its captured snapshot body, each as
// untrusted text.
func agentErrorSamples(report *RunArchiveReport, findingID string) []AgentUntrusted {
	samples := []AgentUntrusted{}
	for _, pattern := range report.ErrorPatterns {
		if agentFindingID(agentKindErrorGroup, pattern.Signature) != findingID {
			continue
		}
		if sample := agentUntrustedText("error_pattern", pattern.Sample, agentFindingUntrustedRunes); sample != nil {
			samples = append(samples, *sample)
		}
		if pattern.Snapshot != nil {
			for _, entry := range pattern.Snapshot.NetworkEntries {
				if entry.Trace != nil {
					if traceBody, err := json.Marshal(entry.Trace); err == nil {
						if sample := agentUntrustedText("network_trace", string(traceBody), agentFindingUntrustedRunes); sample != nil {
							samples = append(samples, *sample)
						}
					}
					break
				}
			}
			if body, err := json.Marshal(pattern.Snapshot); err == nil {
				if sample := agentUntrustedText("error_snapshot", string(body), agentFindingUntrustedRunes); sample != nil {
					samples = append(samples, *sample)
				}
			}
		}
		break
	}
	return samples
}

// agentFindingSeries is the run's summary timeline for the finding's
// metric, downsampled to at most agentSeriesMaxPoints: latency keeps each
// bucket's maximum so peaks survive, other series the mean. The timeline is
// the whole run's, not one transaction's; Metric says which.
func agentFindingSeries(report *RunArchiveReport, finding AgentFinding) *AgentSeries {
	if len(report.SummaryTimeline) == 0 {
		return nil
	}
	metric, unit, value := "overall_p95_ms", "ms", func(p RunArchiveSummaryPoint) float64 { return p.P95Ms }
	peak := true
	name := ""
	if finding.Metric != nil {
		name = finding.Metric.Name
	}
	switch {
	case strings.Contains(name, "error"), strings.Contains(name, "failure"), finding.Kind == agentKindErrorGroup:
		metric, unit, value, peak = "overall_error_pct", "pct", func(p RunArchiveSummaryPoint) float64 { return p.ErrorPct }, false
	case strings.Contains(name, "throughput"), finding.Kind == agentKindThroughputGap:
		metric, unit, value, peak = "overall_tps", "per_s", func(p RunArchiveSummaryPoint) float64 { return p.TPS }, false
	}
	points := report.SummaryTimeline
	if window := finding.Scope.Window; window != nil && !window.From.Equal(window.To) {
		var inWindow []RunArchiveSummaryPoint
		for _, point := range points {
			if !point.Timestamp.Before(window.From) && !point.Timestamp.After(window.To) {
				inWindow = append(inWindow, point)
			}
		}
		if len(inWindow) > 0 {
			points = inWindow
		}
	}
	series := &AgentSeries{Metric: metric, Unit: unit, Points: [][2]float64{}}
	if len(points) > 1 {
		series.IntervalS = agentRound(points[1].Timestamp.Sub(points[0].Timestamp).Seconds(), "")
	}
	bucket := (len(points) + agentSeriesMaxPoints - 1) / agentSeriesMaxPoints
	if bucket < 1 {
		bucket = 1
	}
	series.Downsampled = bucket > 1
	for start := 0; start < len(points); start += bucket {
		end := min(start+bucket, len(points))
		aggregate := 0.0
		for i := start; i < end; i++ {
			v := value(points[i])
			if peak {
				aggregate = max(aggregate, v)
			} else {
				aggregate += v / float64(end-start)
			}
		}
		at := points[start].Timestamp.UTC().Truncate(time.Second).Unix()
		series.Points = append(series.Points, [2]float64{float64(at), agentRound(aggregate, unit)})
	}
	return series
}

// renderAgentFindingMarkdown renders one finding's detail.
func renderAgentFindingMarkdown(detail *AgentFindingDetail) string {
	var b strings.Builder
	finding := detail.Finding
	fmt.Fprintf(&b, "## %s [%s, %s]\n\n%s\n\n", finding.ID, finding.Severity, finding.Confidence, markdownInline(finding.Title))
	if finding.Metric != nil {
		metric := finding.Metric
		fmt.Fprintf(&b, "- metric: %s (%s) observed %s, threshold %s, baseline %s, delta %s\n", metric.Name, firstNonEmpty(metric.Unit, "unitless"),
			ptrText(metric.Observed), ptrText(metric.Threshold), ptrText(metric.Baseline), ptrText(metric.DeltaPct))
	}
	if finding.Scope.Transaction != "" {
		fmt.Fprintf(&b, "- transaction: %s\n", markdownInline(agentSafeName(finding.Scope.Transaction)))
	}
	if window := finding.Scope.Window; window != nil {
		fmt.Fprintf(&b, "- window: %s to %s\n", window.From.Format(time.RFC3339), window.To.Format(time.RFC3339))
	}
	fmt.Fprintf(&b, "- sources: %s · baseline: %s\n", strings.Join(finding.Sources, ", "), finding.BaselineState)
	for _, evidence := range finding.Evidence {
		fmt.Fprintf(&b, "- evidence %s: %s\n", evidence.Ref, evidence.URI)
	}
	if series := detail.Series; series != nil && len(series.Points) > 0 {
		values := make([]string, 0, len(series.Points))
		for _, point := range series.Points {
			values = append(values, fmt.Sprint(point[1]))
		}
		fmt.Fprintf(&b, "\n%s (%s, %d points%s): %s\n", series.Metric, series.Unit, len(series.Points), map[bool]string{true: ", downsampled", false: ""}[series.Downsampled], strings.Join(values, " "))
	}
	if len(detail.Related) > 0 {
		fmt.Fprintf(&b, "\nrelated: %s\n", strings.Join(detail.Related, ", "))
	}
	if len(detail.Samples) > 0 {
		b.WriteString("\n" + agentUntrustedNotice + "\n\n")
		for _, sample := range detail.Samples {
			b.WriteString(renderAgentUntrustedBlock(finding.ID, sample))
		}
	}
	return b.String()
}
