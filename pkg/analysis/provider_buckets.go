package analysis

import (
	"errors"
	"math"
	"sort"
	"time"
)

// Provider buckets are aggregates. Rebucket only complete settled intervals,
// using their declared source weights rather than averaging averages.
func bucketProviderSeries(points []MetricPoint, from, to time.Time, seconds int, aggregation string) ([]MetricPoint, error) {
	switch aggregation {
	case "avg", "min", "max", "sum":
	default:
		return nil, errors.New("provider buckets cannot produce raw counts, rates or percentiles")
	}
	p := append([]MetricPoint(nil), points...)
	sort.Slice(p, func(i, j int) bool { return p[i].TimestampUTC.Before(p[j].TimestampUTC) })
	type accumulator struct {
		sum, weight, covered, min, max float64
		invalid                        bool
	}
	buckets := map[int]*accumulator{}
	previous := time.Time{}
	for _, point := range p {
		if point.Validate() != nil || point.Provider == nil || !sameSeries(p[0], point) || point.Provider.ResolutionSeconds != p[0].Provider.ResolutionSeconds || point.Provider.Statistic != p[0].Provider.Statistic {
			return nil, errors.New("select one consistent provider metric series")
		}
		meta := point.Provider
		if !previous.IsZero() && meta.BucketStart.Before(previous) {
			return nil, errors.New("overlapping provider buckets")
		}
		previous = meta.BucketEnd
		if meta.BucketStart.Before(from) || meta.BucketEnd.After(to) {
			continue
		}
		index := int(meta.BucketStart.Sub(from).Seconds() / float64(seconds))
		start := from.Add(time.Duration(index*seconds) * time.Second)
		end := start.Add(time.Duration(seconds) * time.Second)
		if meta.BucketEnd.After(end) {
			return nil, errors.New("analysis buckets must contain complete source intervals")
		}
		a := buckets[index]
		if a == nil {
			a = &accumulator{min: math.Inf(1), max: math.Inf(-1)}
			buckets[index] = a
		}
		if point.Quality != QualityValid || point.Value == nil || !meta.Settled || !meta.AggregationEligible {
			a.invalid = true
			continue
		}
		value := *point.Value
		a.covered += meta.BucketEnd.Sub(meta.BucketStart).Seconds()
		a.min = math.Min(a.min, value)
		a.max = math.Max(a.max, value)
		if aggregation == "sum" {
			if !providerIntervalSumEligible(meta) {
				return nil, errors.New("sum requires an interval count or sum metric")
			}
			a.sum += value
		} else if meta.Statistic == "average" {
			if meta.Count == nil || *meta.Count <= 0 || meta.Sum == nil {
				return nil, errors.New("source average requires valid sums and counts")
			}
			a.sum += *meta.Sum
			a.weight += *meta.Count
		} else {
			weight := meta.BucketEnd.Sub(meta.BucketStart).Seconds()
			a.sum += value * weight
			a.weight += weight
		}
	}
	out := []MetricPoint{}
	n := int(math.Ceil(to.Sub(from).Seconds() / float64(seconds)))
	for index := 0; index < n; index++ {
		point := p[0]
		point.Provider = nil
		point.ReceivedAt = time.Time{}
		point.RelativeTimeMS = nil
		point.ActiveRuntimeMS = nil
		point.TimestampUTC = from.Add(time.Duration(index*seconds) * time.Second).UTC()
		if isElasticProvider(p[0].Provider) {
			meta := *p[0].Provider
			meta.BucketStart = point.TimestampUTC
			meta.BucketEnd = point.TimestampUTC.Add(time.Duration(seconds) * time.Second)
			meta.ResolutionSeconds = seconds
			meta.WindowKind = "bucket"
			meta.QueryWindow = false
			meta.DerivedAggregation = aggregation
			meta.AggregationEligible = false
			meta.Count, meta.Sum, meta.Min, meta.Max = nil, nil, nil, nil
			point.Provider = &meta
		}
		point.Value = nil
		point.Quality = QualityMissing
		point.Kind = KindGauge
		point.InputKind = KindGauge
		a := buckets[index]
		if a != nil && !a.invalid && a.covered >= float64(seconds) {
			value := a.sum
			switch aggregation {
			case "avg":
				value /= a.weight
			case "min":
				value = a.min
			case "max":
				value = a.max
			}
			if !math.IsNaN(value) && !math.IsInf(value, 0) {
				point.Value = &value
				point.Quality = QualityValid
			}
		}
		out = append(out, point)
	}
	return out, nil
}

func providerIntervalSumEligible(meta *ProviderMeasurement) bool {
	if meta == nil {
		return false
	}
	if meta.Statistic == "sum" {
		return true
	}
	return (meta.Name == "elasticsearch" || meta.Name == "opensearch") && meta.Statistic == "count" && !meta.Approximate &&
		(meta.WindowKind == "bucket" && !meta.QueryWindow || meta.WindowKind == "query_window" && meta.QueryWindow)
}
