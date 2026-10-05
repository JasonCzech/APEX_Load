// Package analysis defines the common, bounded measurement contract used by
// collected monitoring data and imported analytical datasets.
package analysis

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

const SchemaVersion = "apexload.metric_point.v1"

const (
	KindUnknown     = "unknown" // Source did not declare measurement semantics.
	KindGauge       = "gauge"
	KindCounter     = "counter"
	KindObservation = "observation"
	KindHistogram   = "histogram"
	KindSummary     = "summary"

	QualityValid       = "valid"
	QualityStale       = "stale"
	QualityClockSkewed = "clock_skewed"
	QualityDropped     = "dropped"
	QualityMissing     = "missing"
)

const (
	maxMetricIdentityBytes = 256
	maxDimensions          = 16
	maxDimensionBytes      = 128
	MaxSeriesPoints        = 10_000
)

// MetricPoint is a measurement, not an aggregated percentile. Value is absent
// when the source reports missing or dropped evidence; zero remains a real value.
type MetricPoint struct {
	SchemaVersion   string               `json:"schema_version"`
	ProjectID       string               `json:"project_id"`
	DatasetID       string               `json:"dataset_id,omitempty"`
	RunID           string               `json:"run_id,omitempty"`
	ScenarioID      string               `json:"scenario_id,omitempty"`
	Source          string               `json:"source"`
	Host            string               `json:"host,omitempty"`
	Service         string               `json:"service,omitempty"`
	TimestampUTC    time.Time            `json:"timestamp_utc"`
	ReceivedAt      time.Time            `json:"received_at,omitempty"`
	RelativeTimeMS  *int64               `json:"relative_time_ms,omitempty"`
	ActiveRuntimeMS *int64               `json:"active_runtime_ms,omitempty"`
	MetricName      string               `json:"metric_name"`
	Population      string               `json:"population,omitempty"`
	MetricVersion   string               `json:"metric_version,omitempty"`
	Derivation      []DerivationStep     `json:"derivation,omitempty"`
	Kind            string               `json:"kind"`
	InputKind       string               `json:"input_kind,omitempty"` // Original semantics for derived bucket gauges.
	Value           *float64             `json:"value,omitempty"`
	Unit            string               `json:"unit,omitempty"`
	Dimensions      map[string]string    `json:"dimensions,omitempty"`
	Quality         string               `json:"quality"`
	Provider        *ProviderMeasurement `json:"provider,omitempty"`
}

// ProviderMeasurement preserves source bucket semantics. A bucket statistic is
// never a raw transaction observation and must not enter a percentile digest.
// QueryWindow values describe a full query interval, which may exceed the
// one-day maximum resolution of an individual source bucket.
type ProviderMeasurement struct {
	Mode                  string            `json:"mode,omitempty"`
	BackendProfile        string            `json:"backend_profile,omitempty"`
	QueryID               string            `json:"query_id,omitempty"`
	DefinitionHash        string            `json:"definition_hash,omitempty"`
	DisplayLabels         map[string]string `json:"display_labels,omitempty"` // Azure display casing; canonical identity remains in Dimensions.
	EvaluationStepSeconds int               `json:"evaluation_step_seconds,omitempty"`
	AnalysisStatistic     string            `json:"analysis_statistic,omitempty"` // Explicit resampling of evaluation-grid values, never a source bucket.
	UnderlyingSampleTime  *time.Time        `json:"underlying_sample_time,omitempty"`
	FreshnessKnown        bool              `json:"freshness_known,omitempty"`
	Name                  string            `json:"name"`
	MetricID              string            `json:"metric_id"`
	CatalogPath           string            `json:"catalog_path,omitempty"`
	EntityIDs             map[string]string `json:"entity_ids,omitempty"`
	BucketStart           time.Time         `json:"bucket_start"`
	BucketEnd             time.Time         `json:"bucket_end"`
	ResolutionSeconds     int               `json:"resolution_seconds"`
	Statistic             string            `json:"statistic"`
	Count                 *float64          `json:"count,omitempty"`
	Sum                   *float64          `json:"sum,omitempty"`
	Min                   *float64          `json:"min,omitempty"`
	Max                   *float64          `json:"max,omitempty"`
	Settled               bool              `json:"settled"`
	AggregationEligible   bool              `json:"aggregation_eligible"`
	QueryWindow           bool              `json:"query_window,omitempty"`
	Backend               string            `json:"backend,omitempty"`
	Synthetic             bool              `json:"synthetic,omitempty"`
}

// Validate rejects non-finite, ambiguous, and unbounded measurements before
// they enter a live payload or durable analysis artifact.
func (p MetricPoint) Validate() error {
	if p.Provider != nil {
		m := p.Provider
		if m.Name != "appdynamics" && m.Name != "prometheus" && m.Name != "dynatrace" || m.MetricID == "" || len(m.MetricID) > 256 || len(m.CatalogPath) > 4096 || len(m.EntityIDs) > 16 || len(m.Statistic) > 64 || len(m.Backend) > 64 {
			return errors.New("invalid provider bucket measurement")
		}
		if m.Name == "prometheus" {
			if err := validatePrometheusMeasurement(p); err != nil {
				return err
			}
		} else if m.Mode != "" && m.Mode != "interval_bucket" && m.Mode != "query_window" || (m.Name == "appdynamics" || m.Name == "dynatrace") && (m.BucketStart.Location() != time.UTC || m.BucketEnd.Location() != time.UTC || m.BucketStart.IsZero() || !m.BucketEnd.After(m.BucketStart) || m.ResolutionSeconds < 1 || (!m.QueryWindow && m.ResolutionSeconds > 86400) || int64(m.ResolutionSeconds) > math.MaxInt64/int64(time.Second) || m.BucketEnd.Year() < 1 || m.BucketEnd.Year() > 9999 || !m.BucketStart.Add(time.Duration(m.ResolutionSeconds)*time.Second).Equal(m.BucketEnd) || !p.TimestampUTC.Equal(m.BucketStart)) {
			return errors.New("invalid provider bucket measurement")
		}
		for k, v := range m.EntityIDs {
			if k == "" || len(k) > 128 || len(v) > 128 {
				return errors.New("invalid provider entity identity")
			}
		}
		for _, v := range []*float64{m.Count, m.Sum, m.Min, m.Max} {
			if v != nil && (math.IsNaN(*v) || math.IsInf(*v, 0)) {
				return errors.New("nonfinite provider statistic")
			}
		}
		if m.Count != nil && *m.Count < 0 {
			return errors.New("negative provider count")
		}
		if m.Min != nil && m.Max != nil && *m.Min > *m.Max {
			return errors.New("inverted provider extrema")
		}
	}
	if p.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported metric point schema %q", p.SchemaVersion)
	}
	for name, value := range map[string]string{
		"project_id": p.ProjectID, "source": p.Source, "metric_name": p.MetricName,
	} {
		if strings.TrimSpace(value) == "" || len(value) > maxMetricIdentityBytes {
			return fmt.Errorf("%s must contain 1-%d bytes", name, maxMetricIdentityBytes)
		}
	}
	for name, value := range map[string]string{
		"dataset_id": p.DatasetID, "run_id": p.RunID, "scenario_id": p.ScenarioID,
		"host": p.Host, "service": p.Service, "unit": p.Unit,
		"population": p.Population, "metric_version": p.MetricVersion,
	} {
		if len(value) > maxMetricIdentityBytes {
			return fmt.Errorf("%s exceeds %d bytes", name, maxMetricIdentityBytes)
		}
	}
	for name, value := range map[string]string{"population": p.Population, "metric_version": p.MetricVersion} {
		if value != "" && (strings.TrimSpace(value) == "" || strings.ContainsRune(value, 0)) {
			return fmt.Errorf("%s must contain nonempty non-NUL text when declared", name)
		}
	}
	if p.TimestampUTC.IsZero() || p.TimestampUTC.Location() != time.UTC || p.TimestampUTC.Year() < 1 || p.TimestampUTC.Year() > 9999 {
		return errors.New("timestamp_utc must be a nonzero UTC time in years 1-9999")
	}
	if !p.ReceivedAt.IsZero() && (p.ReceivedAt.Location() != time.UTC || p.ReceivedAt.Year() < 1 || p.ReceivedAt.Year() > 9999) {
		return errors.New("received_at must be UTC in years 1-9999")
	}
	if p.RelativeTimeMS != nil && p.RunID == "" {
		return errors.New("relative_time_ms requires run_id")
	}
	if p.ActiveRuntimeMS != nil && (p.RunID == "" || *p.ActiveRuntimeMS < 0) {
		return errors.New("active_runtime_ms requires run_id and a nonnegative value")
	}
	switch p.Kind {
	case KindUnknown, KindGauge, KindCounter, KindObservation, KindHistogram, KindSummary:
	default:
		return fmt.Errorf("unsupported metric kind %q", p.Kind)
	}
	if p.InputKind != "" {
		if p.Kind != KindGauge || (p.InputKind != KindCounter && p.InputKind != KindGauge && p.InputKind != KindObservation) {
			return errors.New("invalid derived measurement semantics")
		}
	}
	if len(p.Derivation) > 0 && (p.Kind != KindGauge || p.InputKind == "") {
		return errors.New("measurement derivation requires a gauge with declared input kind")
	}
	if err := validateDerivation(p.Derivation, false); err != nil {
		return err
	}
	switch p.Quality {
	case QualityValid, QualityStale, QualityClockSkewed:
		if p.Value == nil || math.IsNaN(*p.Value) || math.IsInf(*p.Value, 0) {
			return errors.New("measured point requires a finite value")
		}
	case QualityMissing, QualityDropped:
		if p.Value != nil {
			return errors.New("missing or dropped point cannot have a value")
		}
	default:
		return fmt.Errorf("unsupported metric quality %q", p.Quality)
	}
	if len(p.Dimensions) > maxDimensions {
		return fmt.Errorf("dimensions exceed %d", maxDimensions)
	}
	for key, value := range p.Dimensions {
		if key == "" || len(key) > maxDimensionBytes || len(value) > maxDimensionBytes {
			return fmt.Errorf("dimension name and value must contain at most %d bytes", maxDimensionBytes)
		}
	}
	return nil
}

// Evaluation points are PromQL results on a fixed grid, not fabricated source
// buckets. Exact-window aggregates carry their real interval separately.
func validatePrometheusMeasurement(p MetricPoint) error {
	m := p.Provider
	if m.QueryID == "" || len(m.QueryID) > 128 || len(m.DefinitionHash) != 64 || (m.BackendProfile != "prometheus" && m.BackendProfile != "azure_managed") || m.EvaluationStepSeconds < 1 || m.EvaluationStepSeconds > 3600 {
		return errors.New("invalid Prometheus evaluation identity")
	}
	if m.UnderlyingSampleTime != nil && (m.UnderlyingSampleTime.IsZero() || m.UnderlyingSampleTime.Location() != time.UTC || m.UnderlyingSampleTime.After(p.TimestampUTC.Add(30*time.Second))) {
		return errors.New("invalid underlying sample timestamp")
	}
	if m.FreshnessKnown && m.UnderlyingSampleTime == nil {
		return errors.New("known freshness requires an underlying sample timestamp")
	}
	if len(m.DisplayLabels) > 0 {
		if m.BackendProfile != "azure_managed" || len(m.DisplayLabels) != len(p.Dimensions) || len(m.DisplayLabels) > maxDimensions {
			return errors.New("invalid Azure display labels")
		}
		seen := map[string]bool{}
		for key, value := range m.DisplayLabels {
			canonical := strings.ToLower(key)
			if key == "" || len(key) > maxDimensionBytes || len(value) > maxDimensionBytes || strings.ContainsAny(key+value, "\r\n\x00") || seen[canonical] || strings.ToLower(value) != p.Dimensions[canonical] {
				return errors.New("display labels differ from canonical identity")
			}
			if _, exists := p.Dimensions[canonical]; !exists {
				return errors.New("display labels differ from canonical identity")
			}
			seen[canonical] = true
		}
	}
	switch m.Mode {
	case "evaluation_point":
		if !m.BucketStart.IsZero() || !m.BucketEnd.IsZero() || m.ResolutionSeconds != 0 || m.QueryWindow {
			return errors.New("evaluation point cannot claim source bucket boundaries")
		}
	case "query_window":
		if !m.QueryWindow || m.BucketStart.IsZero() || m.BucketStart.Location() != time.UTC || m.BucketEnd.Location() != time.UTC || !m.BucketEnd.After(m.BucketStart) || !p.TimestampUTC.Equal(m.BucketEnd) || m.ResolutionSeconds != 0 {
			return errors.New("invalid exact Prometheus query window")
		}
	case "analysis_bucket":
		if m.AnalysisStatistic != "avg" && m.AnalysisStatistic != "min" && m.AnalysisStatistic != "max" || m.QueryWindow || m.BucketStart.IsZero() || m.BucketStart.Location() != time.UTC || m.BucketEnd.Location() != time.UTC || !m.BucketEnd.After(m.BucketStart) || !p.TimestampUTC.Equal(m.BucketStart) || m.ResolutionSeconds < m.EvaluationStepSeconds || m.ResolutionSeconds > 86400 || m.BucketEnd.After(m.BucketStart.Add(time.Duration(m.ResolutionSeconds)*time.Second)) || m.AggregationEligible || m.UnderlyingSampleTime != nil || m.FreshnessKnown {
			return errors.New("invalid derived Prometheus analysis bucket")
		}
	default:
		return errors.New("unsupported Prometheus measurement mode")
	}
	if m.Mode != "analysis_bucket" && m.AnalysisStatistic != "" {
		return errors.New("source measurements cannot claim an analysis statistic")
	}
	return nil
}

// NormalizeRunClock uses elapsed wall time, including pauses. Active runtime is
// retained separately by the caller and never substituted for elapsed time.
func NormalizeRunClock(p MetricPoint, runStart time.Time) (MetricPoint, error) {
	if p.RunID == "" || runStart.IsZero() {
		return p, errors.New("run_id and run start are required")
	}
	start := runStart.UTC()
	delta := p.TimestampUTC.Sub(start)
	if !start.Add(delta).Equal(p.TimestampUTC) {
		return p, errors.New("run-relative timestamp exceeds duration range")
	}
	elapsed := delta.Milliseconds()
	p.RelativeTimeMS = &elapsed
	return p, p.Validate()
}

// AlignedPair connects one measurement from each series within a bounded
// timestamp tolerance. Each right-hand point is used at most once.
type AlignedPair struct {
	Left  MetricPoint `json:"left"`
	Right MetricPoint `json:"right"`
	LagMS int64       `json:"lag_ms"`
}

// sameMetric compares measurement identity, excluding the dataset/run that
// owns it so explicitly selected baselines may come from earlier runs.
func sameMetric(a, b MetricPoint) bool {
	return measurementIdentityOf(a).equal(measurementIdentityOf(b))
}

func compatiblePrometheusProvenance(a, b MetricPoint) bool {
	if a.Provider != nil && a.Provider.Name == "prometheus" || b.Provider != nil && b.Provider.Name == "prometheus" {
		return samePrometheusIdentity(a, b)
	}
	return compatibleProvider(a.Provider, b.Provider)
}

// ProviderCompatibility excludes bucket clocks and mutable settlement state,
// but retains source identity and the semantics required for valid comparisons.
func compatibleProvider(a, b *ProviderMeasurement) bool {
	return generalProviderIdentityOf(a).equal(generalProviderIdentityOf(b))
}

func sameSeries(a, b MetricPoint) bool {
	return sameMetric(a, b) && a.RunID == b.RunID && a.DatasetID == b.DatasetID
}

// scalarSeries validates a single bounded series before statistical operations.
// Counters need rate conversion; histogram/summary values need an explicit
// statistic selection. Neither is implicitly treated as a gauge here.
func scalarSeries(points []MetricPoint) error {
	if len(points) > MaxSeriesPoints {
		return fmt.Errorf("series exceeds %d points", MaxSeriesPoints)
	}
	seen := make(map[time.Time]bool, len(points))
	for i, p := range points {
		if err := p.Validate(); err != nil {
			return err
		}
		if p.Kind != KindGauge && p.Kind != KindObservation {
			return errors.New("statistical analysis requires scalar gauges or observations")
		}
		if i > 0 && !sameSeries(points[0], p) {
			return errors.New("input contains more than one metric series")
		}
		at := p.TimestampUTC.Round(0)
		if seen[at] {
			return errors.New("series contains duplicate timestamps; aggregate into unique buckets first")
		}
		seen[at] = true
	}
	return nil
}

// Align returns time-ordered, one-to-one nearest pairs within the tolerance.
// Matching is monotonic: once a right point is selected, earlier right points
// are not reused. Each input must be a single series with unique timestamps.
// Missing/stale points never fill gaps. Run/dataset associations must match;
// callers must explicitly authorize and normalize cross-dataset associations.
func Align(left, right []MetricPoint, maxLag time.Duration) ([]AlignedPair, error) {
	if maxLag < 0 {
		return nil, errors.New("max lag must be nonnegative")
	}
	if err := scalarSeries(left); err != nil {
		return nil, err
	}
	if err := scalarSeries(right); err != nil {
		return nil, err
	}
	if len(left) > 0 && len(right) > 0 && !sameAssociation(left[0], right[0]) {
		return nil, errors.New("series belong to different projects, runs, or datasets")
	}
	a := append([]MetricPoint(nil), left...)
	b := append([]MetricPoint(nil), right...)
	sort.Slice(a, func(i, j int) bool { return a[i].TimestampUTC.Before(a[j].TimestampUTC) })
	sort.Slice(b, func(i, j int) bool { return b[i].TimestampUTC.Before(b[j].TimestampUTC) })
	// Remove ineligible samples once so a large gap cannot repeatedly scan them.
	valid := b[:0]
	for _, p := range b {
		if p.Quality == QualityValid {
			valid = append(valid, p)
		}
	}
	b = valid
	result := make([]AlignedPair, 0, min(len(a), len(b)))
	next := 0
	for _, l := range a {
		if l.Quality != QualityValid {
			continue
		}
		for next < len(b) && b[next].TimestampUTC.Before(l.TimestampUTC.Add(-maxLag)) {
			next++
		}
		if next == len(b) {
			break
		}
		// Binary search avoids quadratic scans for wide lag windows.
		j := next + sort.Search(len(b)-next, func(i int) bool { return !b[next+i].TimestampUTC.Before(l.TimestampUTC) })
		best := -1
		for _, candidate := range []int{j - 1, j} {
			if candidate < next || candidate >= len(b) || b[candidate].TimestampUTC.Before(l.TimestampUTC.Add(-maxLag)) || b[candidate].TimestampUTC.After(l.TimestampUTC.Add(maxLag)) {
				continue
			}
			if best < 0 || absDuration(b[candidate].TimestampUTC.Sub(l.TimestampUTC)) < absDuration(b[best].TimestampUTC.Sub(l.TimestampUTC)) {
				best = candidate
			}
		}
		if best >= 0 {
			result = append(result, AlignedPair{Left: l, Right: b[best], LagMS: b[best].TimestampUTC.Sub(l.TimestampUTC).Milliseconds()})
			next = best + 1
		}
	}
	return result, nil
}

func sameAssociation(a, b MetricPoint) bool {
	return a.ProjectID == b.ProjectID && a.RunID == b.RunID && a.DatasetID == b.DatasetID
}

func absDuration(value time.Duration) time.Duration {
	if value < 0 {
		return -value
	}
	return value
}

// Pearson returns a score only when at least three comparable pairs contain
// variance on both sides. A false result means insufficient evidence.
func Pearson(pairs []AlignedPair) (float64, bool) {
	if len(pairs) < 3 || len(pairs) > MaxSeriesPoints {
		return 0, false
	}
	left, right := make([]MetricPoint, len(pairs)), make([]MetricPoint, len(pairs))
	var leftScale, rightScale float64
	for i, pair := range pairs {
		left[i], right[i] = pair.Left, pair.Right
		if pair.Left.Quality != QualityValid || pair.Right.Quality != QualityValid ||
			pair.Left.Value == nil || pair.Right.Value == nil || !sameAssociation(pair.Left, pair.Right) {
			return 0, false
		}
		leftScale = math.Max(leftScale, math.Abs(*pair.Left.Value))
		rightScale = math.Max(rightScale, math.Abs(*pair.Right.Value))
	}
	if scalarSeries(left) != nil || scalarSeries(right) != nil || leftScale == 0 || rightScale == 0 {
		return 0, false
	}
	// Normalize first to avoid overflow/underflow of sums and variance products.
	var leftMean, rightMean float64
	for _, pair := range pairs {
		leftMean += *pair.Left.Value / leftScale
		rightMean += *pair.Right.Value / rightScale
	}
	leftMean /= float64(len(pairs))
	rightMean /= float64(len(pairs))
	var covariance, leftVariance, rightVariance float64
	for _, pair := range pairs {
		x, y := *pair.Left.Value/leftScale-leftMean, *pair.Right.Value/rightScale-rightMean
		covariance += x * y
		leftVariance += x * x
		rightVariance += y * y
	}
	if leftVariance <= 0 || rightVariance <= 0 {
		return 0, false
	}
	score := covariance / math.Sqrt(leftVariance) / math.Sqrt(rightVariance)
	if math.IsNaN(score) || math.IsInf(score, 0) {
		return 0, false
	}
	return math.Max(-1, math.Min(1, score)), true
}
