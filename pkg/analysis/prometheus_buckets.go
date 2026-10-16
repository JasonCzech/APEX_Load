package analysis

import (
	"errors"
	"maps"
	"math"
	"sort"
	"time"

	"github.com/jasonczech/apexload/pkg/monitorprovider"
)

// bucketPrometheusSeries resamples the declared evaluation grid for display.
// It does not infer scrape intervals, rates, totals, or transaction percentiles.
func bucketPrometheusSeries(points []MetricPoint, from, to time.Time, seconds int, aggregation string) ([]MetricPoint, error) {
	if aggregation != "avg" && aggregation != "min" && aggregation != "max" {
		return nil, errors.New("Prometheus grid analysis supports only mean, minimum, or maximum evaluation values")
	}
	p := append([]MetricPoint(nil), points...)
	sort.Slice(p, func(i, j int) bool { return p[i].TimestampUTC.Before(p[j].TimestampUTC) })
	first := p[0]
	if first.Provider == nil || first.Provider.Mode != "evaluation_point" {
		return nil, errors.New("exact-window or derived Prometheus results cannot be rebucketed into intervals")
	}
	if err := first.Validate(); err != nil {
		return nil, err
	}
	step := time.Duration(first.Provider.EvaluationStepSeconds) * time.Second
	if seconds < first.Provider.EvaluationStepSeconds {
		return nil, errors.New("analysis resolution cannot be finer than the Prometheus evaluation step")
	}
	type accumulator struct {
		sum, min, max float64
		count         int64
		invalid       bool
	}
	buckets := map[int]*accumulator{}
	for i, point := range p {
		if point.Validate() != nil || point.Provider == nil || point.Provider.Mode != "evaluation_point" || !sameSeries(first, point) || !samePrometheusIdentity(first, point) {
			return nil, errors.New("select one consistent Prometheus query and label series")
		}
		if point.TimestampUTC.Sub(first.TimestampUTC)%step != 0 || i > 0 && point.TimestampUTC.Equal(p[i-1].TimestampUTC) {
			return nil, errors.New("Prometheus analysis requires unique observations on the declared evaluation grid")
		}
		if point.TimestampUTC.Before(from) || !point.TimestampUTC.Before(to) {
			continue
		}
		index := int(point.TimestampUTC.Sub(from).Seconds() / float64(seconds))
		a := buckets[index]
		if a == nil {
			a = &accumulator{min: math.Inf(1), max: math.Inf(-1)}
			buckets[index] = a
		}
		if point.Quality != QualityValid || point.Value == nil || !point.Provider.Settled || !point.Provider.AggregationEligible {
			a.invalid = true
			continue
		}
		value := *point.Value
		a.sum += value
		a.min = math.Min(a.min, value)
		a.max = math.Max(a.max, value)
		a.count++
	}
	count := int(math.Ceil(to.Sub(from).Seconds() / float64(seconds)))
	out := make([]MetricPoint, 0, count)
	for index := 0; index < count; index++ {
		start := from.Add(time.Duration(index*seconds) * time.Second)
		end := start.Add(time.Duration(seconds) * time.Second)
		if end.After(to) {
			end = to
		}
		// Retain the original grid phase even if the first selected observation
		// occurs after a missing point. Expected cells remain independent of data.
		gridStart := first.TimestampUTC.Add(start.Sub(first.TimestampUTC).Truncate(step))
		if gridStart.Before(start) {
			gridStart = gridStart.Add(step)
		}
		expected := int64(0)
		if gridStart.Before(end) {
			expected = int64((end.Sub(gridStart)-1)/step) + 1
		}
		point := first
		metadata := *first.Provider
		metadata.Mode = "analysis_bucket"
		metadata.AnalysisStatistic = aggregation
		metadata.BucketStart, metadata.BucketEnd = start, end
		metadata.ResolutionSeconds = seconds
		metadata.UnderlyingSampleTime = nil
		metadata.Count, metadata.Sum, metadata.Min, metadata.Max = nil, nil, nil, nil
		metadata.FreshnessKnown = false
		metadata.AggregationEligible = false
		metadata.Settled = false
		point.Provider = &metadata
		point.TimestampUTC = start
		point.ReceivedAt = time.Time{}
		point.RelativeTimeMS, point.ActiveRuntimeMS = nil, nil
		point.Kind, point.InputKind = KindGauge, KindGauge
		point.Value, point.Quality = nil, QualityMissing
		a := buckets[index]
		if a != nil && !a.invalid && expected > 0 && a.count == expected {
			value := a.sum / float64(a.count)
			if aggregation == "min" {
				value = a.min
			} else if aggregation == "max" {
				value = a.max
			}
			if !math.IsNaN(value) && !math.IsInf(value, 0) {
				point.Value, point.Quality = &value, QualityValid
				metadata.Settled = true
			}
		}
		out = append(out, point)
	}
	return out, nil
}

func samePrometheusIdentity(a, b MetricPoint) bool {
	left, right := a.Provider, b.Provider
	return left != nil && right != nil && left.Name == "prometheus" && right.Name == "prometheus" &&
		prometheusIdentityOf(left).equal(prometheusIdentityOf(right)) && maps.Equal(a.Dimensions, b.Dimensions)
}

// CompatibleOverlay compares declared semantics, including preserved Prometheus
// provenance after display resampling. Correlation may intentionally use two
// different metrics and does not use this same-series overlay restriction.
func CompatibleOverlay(left, right MetricPoint) bool {
	if left.Unit == "" || right.Unit == "" || left.Unit != right.Unit || left.Kind != right.Kind || left.InputKind != right.InputKind {
		return false
	}
	leftPrometheus := left.Provider != nil && left.Provider.Name == monitorprovider.ProviderPrometheus
	rightPrometheus := right.Provider != nil && right.Provider.Name == monitorprovider.ProviderPrometheus
	if leftPrometheus || rightPrometheus {
		return leftPrometheus && rightPrometheus && samePrometheusIdentity(left, right)
	}
	return true
}
