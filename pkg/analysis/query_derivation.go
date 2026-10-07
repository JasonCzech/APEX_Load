package analysis

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"time"
)

// QueryDerivation asks a bucketed query for one catalog operation over its
// series (NA-1 executable derivations). The catalog is the validator: only
// operations it declares, with their eligible populations, input kinds and
// bounds, are accepted.
type QueryDerivation struct {
	Operation string `json:"operation"`
	// WindowBuckets and MinimumCoverage parameterize moving_mean and
	// moving_stddev: 2..1000 trailing buckets, coverage in (0, 1].
	WindowBuckets   int     `json:"window_buckets,omitempty"`
	MinimumCoverage float64 `json:"minimum_coverage,omitempty"`
}

// DerivationBucket is one bucket of a series a derivation reads. Value is the
// series' value (nil is missing, never zero). Count is the bucket's
// population (observations, or observed messages) and Subset the counted
// part of it (failures, or the messages a message ratio counts). Complete
// says the bucket's elapsed window and its evidence are complete.
type DerivationBucket struct {
	From     time.Time
	To       time.Time
	Value    *float64
	Count    int64
	Subset   int64
	Complete bool
	// Reason says why a bucket without a value is missing when its input
	// existed (a counter reading that fell inside it).
	Reason string
}

// DerivationSeries is a bucketed series with the measurement it carries.
// Unit is the canonical analytical unit (NormalizeUnit). MetricVersion
// defaults to the catalog version and Population to sample. CounterEpoch
// names a counter's continuous source; counter operations refuse buckets
// whose epoch is unknown.
type DerivationSeries struct {
	ProjectID     string
	Source        string
	MetricName    string
	MetricVersion string
	Population    string
	Kind          string
	Unit          string
	CounterEpoch  string
	Buckets       []DerivationBucket
}

// measurement fills the defaults a derivation's points carry.
func (s DerivationSeries) measurement() DerivationSeries {
	if s.MetricVersion == "" {
		s.MetricVersion = CatalogVersion
	}
	if s.Population == "" {
		s.Population = PopulationSample
	}
	return s
}

// DerivedBucket is one derived value, or the reason it is unscored.
type DerivedBucket struct {
	From   time.Time `json:"from"`
	To     time.Time `json:"to"`
	Value  *float64  `json:"value"`
	Reason string    `json:"reason,omitempty"`
}

// DerivedSeries is a derivation's output on the series' bucket grid.
type DerivedSeries struct {
	Operation string          `json:"operation"`
	Unit      string          `json:"unit"`
	Buckets   []DerivedBucket `json:"buckets"`
}

// Operation aliases keep saved names meaning what they meant.
var derivationAliases = map[string]string{
	"percent_delta": "baseline_percent_delta", "delta_from_baseline": "baseline_delta",
	"moving_average": "moving_mean", "rolling_mean": "moving_mean", "rolling_stddev": "moving_stddev",
	"ack_ratio": "acknowledgement_ratio", "dlq_ratio": "dead_letter_ratio",
}

// CanonicalDerivation resolves an alias to its catalog operation name.
func CanonicalDerivation(name string) string {
	if canonical, ok := derivationAliases[name]; ok {
		return canonical
	}
	return name
}

// messageRatioCounters names the messaging v2 counter each message ratio
// counts; acknowledgement and settlement have no counted evidence yet.
var messageRatioCounters = map[string]string{
	"duplicate_ratio": "duplicate", "redelivery_ratio": "redelivery", "dead_letter_ratio": "dead_letter",
	"acknowledgement_ratio": "", "settlement_ratio": "",
}

// MessageRatioCounter is the messaging counter a message ratio counts, and
// whether the engine records it.
func MessageRatioCounter(operation string) (string, bool) {
	counter, known := messageRatioCounters[CanonicalDerivation(operation)]
	return counter, known && counter != ""
}

// ValidateQueryDerivation checks a derivation against the catalog for a
// query of metric over population. comparison says the query names a
// baseline run; inputKind is the kind the query's series carries.
func ValidateQueryDerivation(metric, population, inputKind string, d QueryDerivation, comparison bool) error {
	name := CanonicalDerivation(d.Operation)
	operation, found := LookupOperation(name)
	// A raw scalar aggregation is not a derivation, except a counter's rate.
	if !found || operation.RawScalar && !(name == "rate" && inputKind == KindCounter) {
		return fmt.Errorf("derivation %q is not a catalog operation", d.Operation)
	}
	if d.WindowBuckets != 0 || d.MinimumCoverage != 0 {
		if name != "moving_mean" && name != "moving_stddev" {
			return errors.New("only moving derivations take a window and coverage")
		}
	}
	switch name {
	case "moving_mean", "moving_stddev":
		if d.WindowBuckets < 2 || d.WindowBuckets > MaxMovingWindowBuckets || !(d.MinimumCoverage > 0 && d.MinimumCoverage <= 1) {
			return fmt.Errorf("moving derivations need 2-%d window buckets and a minimum coverage in (0, 1]", MaxMovingWindowBuckets)
		}
	case "baseline_delta", "baseline_percent_delta":
		if !comparison {
			return errors.New("baseline derivations need a comparison run")
		}
	default:
		// Only a baseline derivation reads the comparison run; any other
		// would derive the current series and leave the baseline raw, two
		// measurements on one axis.
		if comparison {
			return fmt.Errorf("%s derives the current series only; drop the comparison run or use a baseline derivation", name)
		}
	}
	if operation.UnitRule == "ratio" {
		if !slices.Contains(operation.Populations, population) {
			return fmt.Errorf("%s applies to the %v populations", name, operation.Populations)
		}
		return nil
	}
	if !slices.Contains(operation.InputKinds, inputKind) {
		return fmt.Errorf("%s needs %v measurements", name, operation.InputKinds)
	}
	if definition, known := LookupMetric(metric); known && !slices.Contains(definition.Derivations, name) && inputKind != KindCounter {
		return fmt.Errorf("%s does not apply to %s", name, metric)
	}
	return nil
}

// ApplyQueryDerivation evaluates a validated derivation over series with the
// catalog's typed functions. Every bucket that cannot be scored carries its
// reason; a complete bucket is never replaced by zero, and a missing one never
// counts as zero. baseline is the comparison run's series on the same grid.
func ApplyQueryDerivation(series DerivationSeries, d QueryDerivation, baseline *DerivationSeries) (DerivedSeries, error) {
	series = series.measurement()
	if baseline != nil {
		measured := baseline.measurement()
		baseline = &measured
	}
	name := CanonicalDerivation(d.Operation)
	operation, found := LookupOperation(name)
	if !found || len(series.Buckets) > MaxSeriesPoints {
		return DerivedSeries{}, errors.New("unsupported or unbounded derivation")
	}
	out := DerivedSeries{Operation: name, Buckets: make([]DerivedBucket, len(series.Buckets))}
	for i, b := range series.Buckets {
		out.Buckets[i] = DerivedBucket{From: b.From, To: b.To}
	}
	set := func(i int, value DerivedValue) {
		out.Unit = value.Unit
		out.Buckets[i].Value, out.Buckets[i].Reason = value.Value, value.Reason
	}
	switch {
	case operation.UnitRule == "ratio":
		out.Unit = "ratio"
		_, message := messageRatioCounters[name]
		_, counted := MessageRatioCounter(name)
		for i, b := range series.Buckets {
			if message && !counted {
				set(i, unscored("ratio", "evidence_unavailable"))
				continue
			}
			numerator := b.Subset
			if name == "success_ratio" {
				numerator = b.Count - b.Subset
			}
			count := func(n int64) RatioCount {
				return RatioCount{Count: n, Population: series.Population, Scope: "bucket", MetricVersion: CatalogVersion, Quality: QualityValid, Complete: b.Complete}
			}
			value, err := EvaluateRatio(name, count(numerator), count(b.Count))
			if err != nil {
				return DerivedSeries{}, err
			}
			set(i, value)
		}
	case name == "delta" || name == "rate":
		out.Unit = series.Unit
		for i := range series.Buckets {
			if i == 0 {
				out.Buckets[i].Reason = "no_prior_sample"
				continue
			}
			before, after := series.Buckets[i-1], series.Buckets[i]
			if after.Value == nil && after.Reason != "" {
				out.Buckets[i].Reason = after.Reason
				continue
			}
			if before.Value == nil || after.Value == nil {
				out.Buckets[i].Reason = "missing_counter_sample"
				continue
			}
			value, err := EvaluateCounter(name, series.counterSample(before), series.counterSample(after))
			if err != nil {
				out.Buckets[i].Reason = "invalid_counter_sample"
				continue
			}
			set(i, value)
		}
	case name == "baseline_delta" || name == "baseline_percent_delta":
		out.Unit = series.Unit
		if name == "baseline_percent_delta" {
			out.Unit = "percent"
		}
		if baseline == nil {
			return DerivedSeries{}, errors.New("baseline derivations need a comparison series")
		}
		for i, b := range series.Buckets {
			if i >= len(baseline.Buckets) || baseline.Buckets[i].Value == nil {
				out.Buckets[i].Reason = "missing_baseline_bucket"
				continue
			}
			if b.Value == nil {
				out.Buckets[i].Reason = "missing_bucket"
				continue
			}
			value, err := EvaluateBaselineDelta(series.point(b, nil), baseline.point(baseline.Buckets[i], &series), name == "baseline_percent_delta")
			if err != nil {
				out.Buckets[i].Reason = "incompatible_measurement"
				continue
			}
			set(i, value)
		}
	case name == "moving_mean" || name == "moving_stddev":
		out.Unit = series.Unit
		if len(series.Buckets) == 0 {
			return out, nil
		}
		step := series.Buckets[0].To.Sub(series.Buckets[0].From)
		if step < time.Second || step%time.Second != 0 {
			return DerivedSeries{}, errors.New("moving derivations need whole-second buckets")
		}
		// A trailing window is counted in buckets, so the buckets must be
		// contiguous on one grid (the last may end early at the query's end).
		for i := 1; i < len(series.Buckets); i++ {
			if !series.Buckets[i].From.Equal(series.Buckets[i-1].From.Add(step)) {
				return DerivedSeries{}, errors.New("moving derivations need contiguous buckets on one grid")
			}
		}
		points := make([]MetricPoint, 0, len(series.Buckets))
		for _, b := range series.Buckets {
			points = append(points, series.point(b, nil))
		}
		stats, err := MovingStatistics(points, int(step/time.Second), d.WindowBuckets, d.MinimumCoverage)
		if err != nil {
			return DerivedSeries{}, err
		}
		if len(stats) != len(out.Buckets) {
			return DerivedSeries{}, errors.New("moving derivations need contiguous buckets on one grid")
		}
		for i, stat := range stats {
			value := stat.Mean
			if name == "moving_stddev" {
				value = stat.StdDev
			}
			out.Buckets[i].Value, out.Buckets[i].Reason = value, stat.Reason
		}
	default:
		return DerivedSeries{}, fmt.Errorf("derivation %q has no executor", name)
	}
	return out, nil
}

// point is a bucket as a scalar measurement of the series. A bucket without
// a value, or one whose window is incomplete, is missing. shape, when set,
// borrows another series' measurement so a baseline bucket is the same
// measurement as the current one.
func (s DerivationSeries) point(b DerivationBucket, shape *DerivationSeries) MetricPoint {
	measurement := s
	if shape != nil {
		measurement = *shape
	}
	kind := measurement.Kind
	if kind != KindGauge && kind != KindObservation {
		kind = KindGauge
	}
	p := MetricPoint{SchemaVersion: SchemaVersion, ProjectID: measurement.ProjectID, Source: measurement.Source, MetricName: measurement.MetricName, MetricVersion: measurement.MetricVersion,
		Population: measurement.Population, Kind: kind, Unit: measurement.Unit, TimestampUTC: b.From.UTC(), Quality: QualityMissing}
	if b.Value != nil && b.Complete && !math.IsNaN(*b.Value) && !math.IsInf(*b.Value, 0) {
		value := *b.Value
		p.Value, p.Quality = &value, QualityValid
	}
	return p
}

// counterSample is a counter bucket's reading at its end.
func (s DerivationSeries) counterSample(b DerivationBucket) CounterSample {
	value := *b.Value
	return CounterSample{Point: MetricPoint{SchemaVersion: SchemaVersion, ProjectID: s.ProjectID, Source: s.Source, MetricName: s.MetricName, MetricVersion: s.MetricVersion, Population: s.Population,
		Kind: KindCounter, Unit: s.Unit, TimestampUTC: b.To.UTC(), Quality: QualityValid, Value: &value}, Epoch: s.CounterEpoch, Complete: b.Complete}
}
