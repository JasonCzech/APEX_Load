package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/monitorprovider"
	"github.com/jasonczech/apexload/pkg/storage"
)

// BaselinePrometheusIdentity retains the source query contract. Display spelling
// is presentation only; canonical dimensions and provider series identity match.
type BaselinePrometheusIdentity struct {
	Name                  string            `json:"name"`
	BackendProfile        string            `json:"backend_profile"`
	QueryID               string            `json:"query_id"`
	DefinitionHash        string            `json:"definition_hash"`
	Statistic             string            `json:"statistic"`
	EvaluationStepSeconds int               `json:"evaluation_step_seconds"`
	Dimensions            map[string]string `json:"dimensions"`
	EntityIDs             map[string]string `json:"entity_ids"`
	DisplayLabels         map[string]string `json:"display_labels,omitempty"`
}

func compatibleBaselinePrometheusIdentity(a, b *BaselinePrometheusIdentity) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Name == b.Name && a.BackendProfile == b.BackendProfile && a.QueryID == b.QueryID && a.DefinitionHash == b.DefinitionHash && a.Statistic == b.Statistic && a.EvaluationStepSeconds == b.EvaluationStepSeconds && maps.Equal(a.Dimensions, b.Dimensions) && maps.Equal(a.EntityIDs, b.EntityIDs)
}

func baselinePrometheusIdentity(point analysis.MetricPoint) *BaselinePrometheusIdentity {
	m := point.Provider
	return &BaselinePrometheusIdentity{Name: "prometheus", BackendProfile: m.BackendProfile, QueryID: m.QueryID, DefinitionHash: m.DefinitionHash, Statistic: m.Statistic, EvaluationStepSeconds: m.EvaluationStepSeconds, Dimensions: maps.Clone(point.Dimensions), EntityIDs: maps.Clone(m.EntityIDs), DisplayLabels: maps.Clone(m.DisplayLabels)}
}

func baselinePrometheusResourceKey(point analysis.MetricPoint) (string, string) {
	identity := *baselinePrometheusIdentity(point)
	identity.DisplayLabels = nil
	encoded, _ := json.Marshal(struct {
		Provider BaselinePrometheusIdentity
		Unit     string
		Metric   string
	}{identity, point.Unit, point.MetricName})
	digest := sha256.Sum256(encoded)
	signature := hex.EncodeToString(digest[:])
	return "prometheus/" + point.Source + "/" + point.MetricName + "/" + signature[:16], signature
}

func (s *Server) readBaselinePrometheusResources(ctx context.Context, record *storage.RunArchiveRecord, r *baselineComparisonRun, started time.Time, points *int) error {
	if r.manifest.MonitorEvidenceFile == "" {
		return nil
	}
	seen := map[string]map[int64]bool{}
	return forEachArchiveRecordContext(ctx, record, func(m *RunArchiveManifest) string { return m.MonitorEvidenceFile }, func(point *analysis.MetricPoint) error {
		if point.Provider == nil || point.Provider.Name != monitorprovider.ProviderPrometheus {
			return nil
		}
		if err := point.Validate(); err != nil {
			return fmt.Errorf("invalid archived Prometheus observation: %w", err)
		}
		if point.RunID != "" && point.RunID != record.TestID || point.ProjectID != normalizeProjectID(r.manifest.Provenance.ProjectID) {
			return fmt.Errorf("archived Prometheus observation scope differs from run")
		}
		m := point.Provider
		if m.Mode != "evaluation_point" || m.Statistic == "counter" || point.Kind == analysis.KindCounter {
			return nil
		}
		(*points)++
		if *points > 100000 {
			return fmt.Errorf("resource comparison exceeds 100000 recorded points per run")
		}
		key, signature := baselinePrometheusResourceKey(*point)
		series, exists := r.resources[key]
		if !exists {
			if len(r.resources) >= 2000 {
				return fmt.Errorf("resource comparison exceeds 2000 series")
			}
			series = baselineResourceSeries{name: key, identity: signature, unit: point.Unit, kind: "gauge", source: "prometheus", known: true, instances: 1, prometheus: baselinePrometheusIdentity(*point), gridPhase: prometheusGridAnchor(started).Sub(started).Seconds()}
			seen[key] = map[int64]bool{}
		}
		seconds := point.TimestampUTC.Sub(started).Seconds()
		step := float64(m.EvaluationStepSeconds)
		index := int64(math.Round((seconds - series.gridPhase) / step))
		if math.Abs(seconds-series.gridPhase-float64(index)*step) > 1e-6 || seen[key][index] {
			return fmt.Errorf("archived Prometheus observations must be unique on the frozen run grid")
		}
		seen[key][index] = true
		value := math.NaN()
		if point.Quality == analysis.QualityValid && point.Value != nil && m.Settled && m.AggregationEligible {
			value = *point.Value
		}
		series.points = append(series.points, BaselineMetricPoint{Seconds: seconds, Value: value})
		r.resources[key] = series
		return nil
	})
}

// These are expression evaluations on a saved grid. Missing cells reduce
// coverage; no prior value bridges a gap, and no request percentile is inferred.
func baselinePrometheusResourceMeasurement(series baselineResourceSeries, window BaselineLoadWindow) *BaselineMeasurement {
	step := float64(series.prometheus.EvaluationStepSeconds)
	first := math.Ceil((window.StartSeconds-series.gridPhase)/step - 1e-9)
	end := math.Ceil((window.EndSeconds-series.gridPhase)/step - 1e-9)
	expected := end - first
	if expected <= 0 {
		return nil
	}
	m := &BaselineMeasurement{MeanStatistic: "evaluation_grid_mean", AvailableStatistics: []string{"mean", "min", "max", "samples", "coverage_pct", "instances"}, Min: math.Inf(1), Max: math.Inf(-1), Instances: series.instances}
	for _, point := range series.points {
		if point.Seconds < window.StartSeconds || point.Seconds >= window.EndSeconds || !finiteComparison(point.Value) {
			continue
		}
		m.Mean += point.Value
		m.Min = math.Min(m.Min, point.Value)
		m.Max = math.Max(m.Max, point.Value)
		m.Samples++
		m.Points = append(m.Points, BaselineMetricPoint{Seconds: point.Seconds, Value: point.Value, Load: window.Load})
	}
	if m.Samples == 0 {
		return nil
	}
	m.Mean /= float64(m.Samples)
	m.CoveragePct = math.Min(100, 100*float64(m.Samples)/expected)
	if len(m.Points) > 120 {
		out := make([]BaselineMetricPoint, 120)
		for index := range out {
			out[index] = m.Points[index*(len(m.Points)-1)/119]
		}
		m.Points = out
	}
	return m
}
