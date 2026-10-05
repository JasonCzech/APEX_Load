package analysis

import (
	"errors"
	"math"
	"slices"
	"sort"
	"time"

	"github.com/jasonczech/apexload/pkg/monitorprovider"
)

// BucketSeries aggregates raw scalar evidence without treating gaps as zero.
// Counters are differenced in timestamp order and resets invalidate that bucket.
// Summary/histogram inputs require a separate, explicit statistic adapter.
func BucketSeries(points []MetricPoint, from, to time.Time, seconds int, aggregation string) ([]MetricPoint, error) {
	if from.IsZero() || !to.After(from) || to.Sub(from) > 366*24*time.Hour || seconds < 1 || seconds > 86400 || math.Ceil(to.Sub(from).Seconds()/float64(seconds)) > MaxSeriesPoints || len(points) > MaxSeriesPoints {
		return nil, errors.New("invalid or excessive bucket window")
	}
	if err := ValidateScalarAggregationName(aggregation); err != nil {
		return nil, err
	}
	if len(points) == 0 {
		return []MetricPoint{}, nil
	}
	if points[0].Provider != nil {
		if points[0].Provider.Name == monitorprovider.ProviderPrometheus {
			return bucketPrometheusSeries(points, from, to, seconds, aggregation)
		}
		return bucketProviderSeries(points, from, to, seconds, aggregation)
	}
	kind := points[0].Kind
	if err := ValidateScalarAggregation(kind, aggregation); err != nil {
		return nil, err
	}
	lineage, err := ScalarBucketDerivation(points[0], seconds, aggregation)
	if err != nil {
		return nil, err
	}
	p := append([]MetricPoint(nil), points...)
	sort.Slice(p, func(i, j int) bool { return p[i].TimestampUTC.Before(p[j].TimestampUTC) })
	type acc struct {
		sum, min, max float64
		n             int
		invalid       bool
		values        []float64
	}
	buckets := make(map[int]*acc)
	for i, point := range p {
		if err := point.Validate(); err != nil {
			return nil, err
		}
		if !sameSeries(p[0], point) {
			return nil, errors.New("select one metric series, including its dimensions")
		}
		if point.TimestampUTC.Before(from) || !point.TimestampUTC.Before(to) {
			continue
		}
		index := int(point.TimestampUTC.Sub(from).Seconds() / float64(seconds))
		a := buckets[index]
		if a == nil {
			a = &acc{min: math.Inf(1), max: math.Inf(-1)}
			buckets[index] = a
		}
		valid := point.Quality == QualityValid
		var value float64
		if valid {
			value = *point.Value
		}
		if kind == KindCounter {
			valid = valid && i > 0 && p[i-1].Quality == QualityValid && point.TimestampUTC.After(p[i-1].TimestampUTC) && (i+1 == len(p) || !point.TimestampUTC.Equal(p[i+1].TimestampUTC)) && (i < 2 || !p[i-1].TimestampUTC.Equal(p[i-2].TimestampUTC))
			if valid {
				delta := value - *p[i-1].Value
				valid = delta >= 0
				value = delta / point.TimestampUTC.Sub(p[i-1].TimestampUTC).Seconds()
			}
		}
		if !valid {
			a.invalid = true
			continue
		}
		a.sum += value
		a.min = math.Min(a.min, value)
		a.max = math.Max(a.max, value)
		a.n++
		if aggregation == "p50" || aggregation == "p95" || aggregation == "p99" {
			a.values = append(a.values, value)
		}
	}
	result := make([]MetricPoint, 0, len(buckets))
	n := int(math.Ceil(to.Sub(from).Seconds() / float64(seconds)))
	for index := 0; index < n; index++ {
		point := p[0]
		point.TimestampUTC = from.Add(time.Duration(index*seconds) * time.Second).UTC()
		point.Derivation = slices.Clone(lineage)
		point.Kind = KindGauge
		point.InputKind = kind
		point.Value = nil
		point.Quality = QualityMissing
		point.RelativeTimeMS = nil
		point.ActiveRuntimeMS = nil
		point.ReceivedAt = time.Time{}
		if kind == KindCounter {
			point.Unit += "/s"
		}
		if aggregation == "count" {
			point.Unit = "count"
		}
		a := buckets[index]
		if a != nil && a.n > 0 && !a.invalid {
			value := a.sum / float64(a.n)
			switch aggregation {
			case "min":
				value = a.min
			case "max":
				value = a.max
			case "sum":
				value = a.sum
			case "count":
				value = float64(a.n)
			case "p50", "p95", "p99":
				sort.Float64s(a.values)
				rank := map[string]float64{"p50": .5, "p95": .95, "p99": .99}[aggregation] * float64(len(a.values)-1)
				lo, hi := int(math.Floor(rank)), int(math.Ceil(rank))
				value = a.values[lo] + (a.values[hi]-a.values[lo])*(rank-float64(lo))
			}
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return nil, errors.New("bucket value exceeds numeric range")
			}
			point.Value = &value
			point.Quality = QualityValid
		}
		result = append(result, point)
	}
	return result, nil
}
