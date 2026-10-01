package api

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

// StatisticalFinding preserves the baseline denominator and window so a finding
// can be explained after archival. It never participates in stop decisions.
type StatisticalFinding struct {
	analysis.AnomalyEvidence
	Timestamp     time.Time `json:"timestamp"`
	BaselineStart time.Time `json:"baseline_start"`
	BaselineEnd   time.Time `json:"baseline_end"`
	Advisory      bool      `json:"advisory"`
}

func diagnosticsProjectEnabled(project string) bool {
	for _, configured := range strings.Split(os.Getenv("APEXLOAD_DIAGNOSTICS_PROJECTS"), ",") {
		if project != "" && strings.TrimSpace(configured) == project {
			return true
		}
	}
	return false
}

// Statistical windows must be consecutive and have the same load and sampling
// interval. A pause, ramp, stale observation, or missing bucket resets qualification.
func statisticalFindings(project, run string, current *LiveMetrics, history []*LiveMetrics) []StatisticalFinding {
	valid := func(p *LiveMetrics) bool {
		return p != nil && !p.Timestamp.IsZero() && p.TotalTx > 0 && p.ActiveVUsers > 0 && p.SampleWindowSeconds > 0 &&
			p.ObservationState != "stale" && (p.Telemetry == nil || p.Telemetry.DroppedResults == 0)
	}
	if !valid(current) {
		return nil
	}
	prior := make([]*LiveMetrics, 0, analysis.DefaultAnomalyMinBuckets)
	next := current.Timestamp
	nextActive := current.ActiveRuntimeSeconds
	for i := len(history) - 1; i >= 0 && len(prior) < analysis.DefaultAnomalyMinBuckets; i-- {
		p := history[i]
		if !valid(p) || !p.Timestamp.Before(next) || p.ActiveVUsers != current.ActiveVUsers || p.SampleWindowSeconds != current.SampleWindowSeconds || !reflect.DeepEqual(p.LoadLevels, current.LoadLevels) {
			break
		}
		delta := next.Sub(p.Timestamp).Seconds()
		if delta < current.SampleWindowSeconds*.8 || delta > current.SampleWindowSeconds*1.2 {
			break
		}
		// Pauses may continue producing fresh wall-clock buckets. Active time
		// must advance with the wall-clock window when the runtime is known.
		if nextActive > 0 && p.ActiveRuntimeSeconds > 0 && nextActive-p.ActiveRuntimeSeconds < delta*.8 {
			break
		}
		prior = append(prior, p)
		next = p.Timestamp
		nextActive = p.ActiveRuntimeSeconds
	}
	if len(prior) < analysis.DefaultAnomalyMinBuckets {
		return nil
	}
	var findings []StatisticalFinding
	for _, metric := range []struct {
		name, unit string
		value      func(*LiveMetrics) float64
	}{
		{"tps", "1/s", func(p *LiveMetrics) float64 { return p.TPS }},
		{"avg_response_ms", "ms", func(p *LiveMetrics) float64 { return p.AvgResponseMs }},
		{"error_pct", "%", func(p *LiveMetrics) float64 { return p.ErrorPct }},
	} {
		point := func(p *LiveMetrics) analysis.MetricPoint {
			value := metric.value(p)
			return analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: project, RunID: run, Source: "execution", TimestampUTC: p.Timestamp.UTC(), ReceivedAt: p.Timestamp.UTC(), MetricName: metric.name, Value: &value, Unit: metric.unit, Kind: analysis.KindGauge, Quality: analysis.QualityValid}
		}
		baseline := make([]analysis.MetricPoint, len(prior))
		for i, p := range prior {
			baseline[len(prior)-1-i] = point(p)
		}
		evidence, scored, err := analysis.DetectAnomaly(point(current), baseline)
		if err == nil && scored && evidence.Exceeded {
			findings = append(findings, StatisticalFinding{AnomalyEvidence: evidence, Timestamp: current.Timestamp, BaselineStart: prior[len(prior)-1].Timestamp, BaselineEnd: prior[0].Timestamp, Advisory: true})
		}
	}
	return findings
}

func advisoryDiagnosticAlerts(live *LiveMetrics) []map[string]any {
	var alerts []map[string]any
	for _, finding := range live.StatisticalFindings {
		alerts = append(alerts, map[string]any{"type": "threshold_alert", "metric": "anomaly/" + finding.MetricName, "value": finding.ZScore, "threshold": finding.Threshold, "action": "advisory", "evidence": finding, "explanation": fmt.Sprintf("%s differs from %d comparable buckets by %.2f standard deviations; association only", finding.MetricName, finding.BaselineBuckets, finding.ZScore)})
	}
	for _, pattern := range live.ErrorPatterns {
		if len(alerts) >= 7 {
			break
		}
		if pattern.Count < 3 || pattern.LatestSeen.Before(live.Timestamp.Add(-time.Duration(max(live.SampleWindowSeconds, 1))*time.Second)) {
			continue
		}
		alerts = append(alerts, map[string]any{"type": "threshold_alert", "metric": "error_pattern", "transaction": pattern.Signature, "value": float64(pattern.Count), "threshold": float64(3), "action": "advisory", "explanation": fmt.Sprintf("A repeated error pattern has %d observations; inspect the retained error evidence.", pattern.Count), "evidence": map[string]any{"signature": pattern.Signature, "count": pattern.Count, "first_seen": pattern.FirstSeen, "latest_seen": pattern.LatestSeen, "evidence_url": pattern.EvidenceURL}})
	}
	return alerts
}

func archiveStatisticalFindings(project, run string, summaries []IntervalSummaryRecord) []StatisticalFinding {
	history := make([]*LiveMetrics, 0, 31)
	findings := make([]StatisticalFinding, 0)
	for _, summary := range summaries {
		point := &LiveMetrics{Timestamp: summary.Timestamp, ObservationState: summary.ObservationState, ActiveVUsers: summary.ActiveVUsers, TPS: summary.TPS, AvgResponseMs: summary.AvgResponseMs, ErrorPct: summary.ErrorPct, TotalTx: summary.TotalTx, SampleWindowSeconds: summary.SampleWindowSeconds, LoadLevels: summary.LoadLevels, Telemetry: summary.Telemetry}
		if summary.ActiveRuntimeSeconds != nil {
			point.ActiveRuntimeSeconds = *summary.ActiveRuntimeSeconds
		}
		if len(findings) < 300 {
			next := statisticalFindings(project, run, point, history)
			findings = append(findings, next[:min(len(next), 300-len(findings))]...)
		}
		history = append(history, point)
		if len(history) > 30 {
			history = history[1:]
		}
	}
	return findings
}
