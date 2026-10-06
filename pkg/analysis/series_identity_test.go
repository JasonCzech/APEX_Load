package analysis

import (
	"encoding/hex"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func seriesIdentityPoint() MetricPoint {
	value := 10.0
	return MetricPoint{
		SchemaVersion: SchemaVersion, ProjectID: "project", DatasetID: "dataset", RunID: "run",
		ScenarioID: "scenario", Source: "source", Host: "host", Service: "service",
		TimestampUTC: time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC),
		MetricName:   "latency", Population: PopulationAction, MetricVersion: "latency.v1",
		Kind: KindGauge, InputKind: KindObservation, Unit: "ms", Value: &value, Quality: QualityValid,
		Derivation: []DerivationStep{{Operation: "avg", Algorithm: "scalar_bucket.v1", BucketSeconds: 60}},
		Dimensions: map[string]string{"route": "checkout", "region": "west"},
	}
}

func copySeriesIdentityPoint(p MetricPoint) MetricPoint {
	p.Dimensions = maps.Clone(p.Dimensions)
	p.Derivation = slices.Clone(p.Derivation)
	if p.Provider != nil {
		provider := *p.Provider
		provider.EntityIDs = maps.Clone(provider.EntityIDs)
		provider.DisplayLabels = maps.Clone(provider.DisplayLabels)
		p.Provider = &provider
	}
	return p
}

func requireSeriesIdentity(t *testing.T, p MetricPoint, observation SeriesObservation) SeriesIdentity {
	t.Helper()
	identity, err := MeasurementSeriesIdentity(p, observation)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := hex.DecodeString(identity.SHA256)
	if err != nil || len(decoded) != 32 || identity.Version != SeriesIdentityVersion {
		t.Fatalf("invalid versioned full SHA256: %+v", identity)
	}
	return identity
}

func TestMeasurementSeriesIdentitySemantics(t *testing.T) {
	base := seriesIdentityPoint()
	observation := SeriesObservation{Mode: "interval", BucketNanos: int64(time.Minute)}
	identity := requireSeriesIdentity(t, base, observation)
	// The durable encoding must change its version if this fixture changes.
	if identity.SHA256 != "0bc8bc7978460d639f2b9b7d4665659198a5276f79f39887f692362305dd4f9c" {
		t.Fatalf("v1 identity encoding changed: %+v", identity)
	}
	for _, tc := range []struct {
		name string
		edit func(*MetricPoint)
	}{
		{"unit alias", func(p *MetricPoint) { p.Unit = "milliseconds" }},
		{"population", func(p *MetricPoint) { p.Population = PopulationMessage }},
		{"metric version", func(p *MetricPoint) { p.MetricVersion = "latency.v2" }},
		{"parent kind", func(p *MetricPoint) { p.InputKind = KindGauge }},
		{"parent statistic", func(p *MetricPoint) { p.Derivation[0].Operation = "p95" }},
		{"algorithm", func(p *MetricPoint) { p.Derivation[0].Algorithm = "other.v1" }},
		{"derivation grid", func(p *MetricPoint) { p.Derivation[0].BucketSeconds = 30 }},
		{"derivation order", func(p *MetricPoint) {
			p.Derivation = append([]DerivationStep{{Operation: "max", Algorithm: "scalar_bucket.v1", BucketSeconds: 60}}, p.Derivation...)
		}},
		{"dimension", func(p *MetricPoint) { p.Dimensions["route"] = "cart" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			other := copySeriesIdentityPoint(base)
			tc.edit(&other)
			if got := requireSeriesIdentity(t, other, observation); got == identity {
				t.Fatal("incompatible semantic identity matched")
			}
		})
	}
	other := copySeriesIdentityPoint(base)
	other.DatasetID, other.RunID = "baseline-dataset", "baseline-run"
	other.TimestampUTC = other.TimestampUTC.Add(time.Hour)
	other.ReceivedAt = other.TimestampUTC.Add(time.Second)
	elapsed := int64(1234)
	other.RelativeTimeMS, other.ActiveRuntimeMS = &elapsed, &elapsed
	value := 99.0
	other.Value, other.Quality = &value, QualityStale
	if got := requireSeriesIdentity(t, other, observation); got != identity {
		t.Fatal("owner, value, quality or clocks changed logical series identity")
	}
	other.Dimensions = map[string]string{"region": "west", "route": "checkout"}
	if got := requireSeriesIdentity(t, other, observation); got != identity {
		t.Fatal("map insertion order changed identity")
	}
}

func TestMeasurementSeriesIdentityObservationBounds(t *testing.T) {
	p := seriesIdentityPoint()
	seen := map[SeriesIdentity]bool{}
	for _, mode := range []string{"interval", "snapshot", "cumulative"} {
		for _, bucket := range []int64{1, int64(time.Minute), int64(24 * time.Hour)} {
			identity := requireSeriesIdentity(t, p, SeriesObservation{Mode: mode, BucketNanos: bucket})
			if seen[identity] {
				t.Fatal("observation mode or exact grid collided")
			}
			seen[identity] = true
		}
	}
	requireSeriesIdentity(t, p, SeriesObservation{Mode: "snapshot"})
	for _, observation := range []SeriesObservation{
		{}, {Mode: "other", BucketNanos: 1}, {Mode: "interval"}, {Mode: "cumulative"},
		{Mode: "snapshot", BucketNanos: -1}, {Mode: "interval", BucketNanos: int64(24*time.Hour) + 1},
		{Mode: "snapshot", BucketNanos: int64(24*time.Hour) + 1}, {Mode: "cumulative", BucketNanos: int64(24*time.Hour) + 1},
	} {
		if identity, err := MeasurementSeriesIdentity(p, observation); err == nil || identity != (SeriesIdentity{}) {
			t.Fatalf("invalid observation accepted: %+v => %+v, %v", observation, identity, err)
		}
	}
	p.Value = nil
	if _, err := MeasurementSeriesIdentity(p, SeriesObservation{Mode: "snapshot"}); err == nil {
		t.Fatal("invalid point accepted")
	}
}

func TestMeasurementSeriesIdentityNilEmptyAndNUL(t *testing.T) {
	base := seriesIdentityPoint()
	base.Dimensions, base.Derivation, base.Provider = nil, nil, nil
	observation := SeriesObservation{Mode: "snapshot"}
	identity := requireSeriesIdentity(t, base, observation)
	other := base
	other.Dimensions, other.Derivation = map[string]string{}, []DerivationStep{}
	if !sameMetric(base, other) || requireSeriesIdentity(t, other, observation) != identity {
		t.Fatal("nil and empty containers differ")
	}
	for _, name := range []string{"appdynamics", "prometheus"} {
		providerPoint := identityProviderPoint(name)
		providerPoint.Provider.EntityIDs = nil
		other := copySeriesIdentityPoint(providerPoint)
		other.Provider.EntityIDs = map[string]string{}
		if !sameMetric(providerPoint, other) || requireSeriesIdentity(t, providerPoint, observation) != requireSeriesIdentity(t, other, observation) {
			t.Fatal("nil and empty provider entity maps differ")
		}
	}
	// Both pairs flatten to the same NUL-delimited bytes; structured encoding
	// must preserve the boundary between otherwise permitted identity strings.
	left, right := base, base
	left.Host, left.Service = "a\x00b", "c"
	right.Host, right.Service = "a", "b\x00c"
	if sameMetric(left, right) || requireSeriesIdentity(t, left, observation) == requireSeriesIdentity(t, right, observation) {
		t.Fatal("embedded NUL collapsed field boundaries")
	}
	left, right = base, base
	left.Dimensions = map[string]string{"a\x00b": "c"}
	right.Dimensions = map[string]string{"a": "b\x00c"}
	if requireSeriesIdentity(t, left, observation) == requireSeriesIdentity(t, right, observation) {
		t.Fatal("embedded NUL collapsed map key/value boundaries")
	}
}

func identityProviderPoint(name string) MetricPoint {
	p := seriesIdentityPoint()
	if name == "prometheus" {
		p.Provider = &ProviderMeasurement{Name: name, MetricID: "display-id", QueryID: "latency-query", DefinitionHash: strings.Repeat("a", 64), BackendProfile: "prometheus", Mode: "evaluation_point", EvaluationStepSeconds: 15, Statistic: "value", EntityIDs: map[string]string{"series": "latency"}, Settled: true, AggregationEligible: true}
	} else {
		p.Provider = &ProviderMeasurement{Name: name, MetricID: "source-id", Backend: "classic", Mode: "interval_bucket", BucketStart: p.TimestampUTC, BucketEnd: p.TimestampUTC.Add(time.Minute), ResolutionSeconds: 60, Statistic: "average", EntityIDs: map[string]string{"service": "checkout"}, Settled: true, AggregationEligible: true}
	}
	return p
}

// The pre-refactor comparisons are an independent parity oracle. Mutating every
// field (including ignored ones) protects provider-specific comparison behavior.
func legacyGeneralProviderIdentity(a, b *ProviderMeasurement) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Name == b.Name && a.Backend == b.Backend && a.Synthetic == b.Synthetic && a.MetricID == b.MetricID && maps.Equal(a.EntityIDs, b.EntityIDs) && a.ResolutionSeconds == b.ResolutionSeconds && a.Statistic == b.Statistic && a.QueryWindow == b.QueryWindow
}
func legacyPrometheusIdentity(a, b MetricPoint) bool {
	left, right := a.Provider, b.Provider
	return left != nil && right != nil && left.Name == "prometheus" && right.Name == "prometheus" && left.QueryID == right.QueryID && left.DefinitionHash == right.DefinitionHash && left.BackendProfile == right.BackendProfile && left.Mode == right.Mode && left.Statistic == right.Statistic && left.AnalysisStatistic == right.AnalysisStatistic && left.EvaluationStepSeconds == right.EvaluationStepSeconds && left.ResolutionSeconds == right.ResolutionSeconds && maps.Equal(left.EntityIDs, right.EntityIDs) && maps.Equal(a.Dimensions, b.Dimensions)
}
func legacyPrometheusProvenance(a, b MetricPoint) bool {
	if a.Provider != nil && a.Provider.Name == "prometheus" || b.Provider != nil && b.Provider.Name == "prometheus" {
		return legacyPrometheusIdentity(a, b)
	}
	return legacyGeneralProviderIdentity(a.Provider, b.Provider)
}
func legacyMeasurementIdentity(a, b MetricPoint) bool {
	provider := legacyPrometheusProvenance(a, b)
	return a.ProjectID == b.ProjectID && a.Source == b.Source && a.ScenarioID == b.ScenarioID && a.Host == b.Host && a.Service == b.Service && a.MetricName == b.MetricName && a.Population == b.Population && a.MetricVersion == b.MetricVersion && slices.Equal(a.Derivation, b.Derivation) && a.Kind == b.Kind && a.InputKind == b.InputKind && a.Unit == b.Unit && maps.Equal(a.Dimensions, b.Dimensions) && provider
}

func changeIdentityTestField(t *testing.T, field reflect.Value) {
	t.Helper()
	switch field.Kind() {
	case reflect.String:
		field.SetString(field.String() + "_other")
	case reflect.Bool:
		field.SetBool(!field.Bool())
	case reflect.Int, reflect.Int64:
		field.SetInt(field.Int() + 1)
	case reflect.Map:
		field.Set(reflect.ValueOf(map[string]string{"different": "identity"}))
	case reflect.Slice:
		field.Set(reflect.ValueOf([]DerivationStep{{Operation: "max", Algorithm: "other", BucketSeconds: 30}}))
	case reflect.Struct:
		field.Set(reflect.ValueOf(field.Interface().(time.Time).Add(time.Second)))
	case reflect.Pointer:
		if field.Type() == reflect.TypeFor[*ProviderMeasurement]() {
			field.SetZero()
			return
		}
		if field.Type() == reflect.TypeFor[*PointTiming]() {
			// Clock provenance says when and against which clock a point was
			// read, not what it measures, so identity ignores it.
			field.Set(reflect.ValueOf(ProviderTiming(60, nil)))
			return
		}
		value := reflect.New(field.Type().Elem())
		switch value.Elem().Kind() {
		case reflect.Float64:
			value.Elem().SetFloat(123)
		case reflect.Int64:
			value.Elem().SetInt(123)
		case reflect.Struct:
			value.Elem().Set(reflect.ValueOf(time.Date(2026, 10, 5, 0, 0, 1, 0, time.UTC)))
		default:
			t.Fatalf("uncovered pointer field %s", field.Type())
		}
		field.Set(value)
	default:
		t.Fatalf("uncovered field type %s", field.Type())
	}
}

func TestMeasurementIdentityComparatorParity(t *testing.T) {
	for _, profile := range []string{"none", "appdynamics", "prometheus"} {
		base := seriesIdentityPoint()
		if profile != "none" {
			base = identityProviderPoint(profile)
		}
		assertParity := func(t *testing.T, other MetricPoint) {
			t.Helper()
			want := legacyMeasurementIdentity(base, other)
			if sameMetric(base, other) != want || compatibleProvider(base.Provider, other.Provider) != legacyGeneralProviderIdentity(base.Provider, other.Provider) || samePrometheusIdentity(base, other) != legacyPrometheusIdentity(base, other) || compatiblePrometheusProvenance(base, other) != legacyPrometheusProvenance(base, other) {
				t.Fatal("existing comparator behavior changed")
			}
			if base.Validate() == nil && other.Validate() == nil {
				observation := SeriesObservation{Mode: "interval", BucketNanos: int64(time.Minute)}
				if got := requireSeriesIdentity(t, base, observation) == requireSeriesIdentity(t, other, observation); got != want {
					t.Fatal("fingerprint differs from valid measurement comparison")
				}
			}
		}
		pointType := reflect.TypeFor[MetricPoint]()
		for index := range pointType.NumField() {
			field := pointType.Field(index)
			t.Run(profile+"/point/"+field.Name, func(t *testing.T) {
				other := copySeriesIdentityPoint(base)
				changeIdentityTestField(t, reflect.ValueOf(&other).Elem().Field(index))
				assertParity(t, other)
			})
		}
		if base.Provider == nil {
			continue
		}
		providerType := reflect.TypeFor[ProviderMeasurement]()
		for index := range providerType.NumField() {
			field := providerType.Field(index)
			t.Run(profile+"/provider/"+field.Name, func(t *testing.T) {
				other := copySeriesIdentityPoint(base)
				changeIdentityTestField(t, reflect.ValueOf(other.Provider).Elem().Field(index))
				assertParity(t, other)
			})
		}
		if got := testing.AllocsPerRun(100, func() {
			if !sameMetric(base, base) {
				panic("identity changed")
			}
		}); got != 0 {
			t.Fatalf("hot comparison allocated %v times", got)
		}
	}
}

func TestMeasurementSeriesIdentityRejectsInvalidUTF8(t *testing.T) {
	invalid := string([]byte{0xff})
	observation := SeriesObservation{Mode: "snapshot"}
	for _, field := range []string{"ProjectID", "Source", "ScenarioID", "Host", "Service", "MetricName", "Population", "MetricVersion", "Unit"} {
		t.Run("point/"+field, func(t *testing.T) {
			p := seriesIdentityPoint()
			reflect.ValueOf(&p).Elem().FieldByName(field).SetString(invalid)
			if p.Validate() != nil {
				t.Fatal("fixture must expose the pre-encoding UTF-8 gap")
			}
			if _, err := MeasurementSeriesIdentity(p, observation); err == nil {
				t.Fatal("invalid UTF-8 accepted")
			}
		})
	}
	for _, name := range []string{"dimension key", "dimension value", "derivation algorithm", "provider entity key", "provider entity value", "provider metric", "provider backend", "provider statistic", "Prometheus query", "Prometheus definition"} {
		t.Run(name, func(t *testing.T) {
			p := identityProviderPoint("appdynamics")
			switch name {
			case "dimension key":
				p.Dimensions = map[string]string{invalid: "value"}
			case "dimension value":
				p.Dimensions = map[string]string{"key": invalid}
			case "derivation algorithm":
				p.Derivation[0].Algorithm = invalid
			case "provider entity key":
				p.Provider.EntityIDs = map[string]string{invalid: "value"}
			case "provider entity value":
				p.Provider.EntityIDs = map[string]string{"key": invalid}
			case "provider metric":
				p.Provider.MetricID = invalid
			case "provider backend":
				p.Provider.Backend = invalid
			case "provider statistic":
				p.Provider.Statistic = invalid
			case "Prometheus query":
				p = identityProviderPoint("prometheus")
				p.Provider.QueryID = invalid
			case "Prometheus definition":
				p = identityProviderPoint("prometheus")
				p.Provider.DefinitionHash = strings.Repeat(invalid, 64)
			}
			if p.Validate() != nil {
				t.Fatal("fixture must expose the pre-encoding UTF-8 gap")
			}
			if _, err := MeasurementSeriesIdentity(p, observation); err == nil {
				t.Fatal("invalid UTF-8 accepted")
			}
		})
	}
	// Enum validation rejects invalid bytes before projection today. Check every
	// projected string too, so a future validation change cannot expose a gap.
	projection := measurementIdentityOf(identityProviderPoint("prometheus"))
	for _, container := range []string{"measurement", "provider"} {
		var fields reflect.Value
		if container == "measurement" {
			fields = reflect.ValueOf(&projection.Fields).Elem()
		} else {
			fields = reflect.ValueOf(&projection.Provider.Fields).Elem()
		}
		for index := range fields.NumField() {
			if fields.Field(index).Kind() != reflect.String {
				continue
			}
			old := fields.Field(index).String()
			fields.Field(index).SetString(invalid)
			if err := projection.validateUTF8(); err == nil {
				t.Fatalf("unvalidated encoded string: %s/%s", container, fields.Type().Field(index).Name)
			}
			fields.Field(index).SetString(old)
		}
	}
	projection.Derivation[0].Operation = invalid
	if err := projection.validateUTF8(); err == nil {
		t.Fatal("unvalidated derivation operation")
	}
}

func TestMeasurementSeriesIdentityDoesNotQualifyLegacyData(t *testing.T) {
	for _, edit := range []func(*MetricPoint){
		func(p *MetricPoint) { p.Population = "" },
		func(p *MetricPoint) { p.MetricVersion = "" },
		func(p *MetricPoint) {
			p.InputKind = KindCounter
			p.Derivation = []DerivationStep{{Operation: legacyDerivedOperation, Algorithm: "unknown_counter_epoch"}}
		},
		func(p *MetricPoint) { p.Kind = KindSummary; p.InputKind = ""; p.Derivation = nil },
		func(p *MetricPoint) { p.Kind = KindHistogram; p.InputKind = ""; p.Derivation = nil },
	} {
		p := seriesIdentityPoint()
		edit(&p)
		requireSeriesIdentity(t, p, SeriesObservation{Mode: "snapshot"})
		if p.Kind == KindSummary || p.Kind == KindHistogram {
			if _, err := EvaluateBaselineDelta(p, p, false); err == nil {
				t.Fatal("unadapted distribution became a scalar baseline")
			}
		} else if err := CompatibleMeasurements(semanticsOf(p), semanticsOf(p)); err == nil {
			t.Fatal("unqualified measurement became comparable")
		}
	}
}

func TestMeasurementSeriesIdentityConcurrentAndImmutable(t *testing.T) {
	point := identityProviderPoint("prometheus")
	before := copySeriesIdentityPoint(point)
	observation := SeriesObservation{Mode: "interval", BucketNanos: int64(time.Minute)}
	want := requireSeriesIdentity(t, point, observation)
	var workers sync.WaitGroup
	errors := make(chan string, 16)
	for range 16 {
		workers.Go(func() {
			for range 25 {
				got, err := MeasurementSeriesIdentity(point, observation)
				if err != nil || got != want {
					errors <- "concurrent identity changed"
					return
				}
			}
		})
	}
	workers.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
	if !reflect.DeepEqual(point, before) {
		t.Fatal("identity mutated the input")
	}
}

func TestMeasurementSeriesIdentityBoundedEscapedLabels(t *testing.T) {
	p := identityProviderPoint("appdynamics")
	text := strings.Repeat("<", maxMetricIdentityBytes)
	p.ProjectID, p.Source, p.ScenarioID, p.Host, p.Service = text, text, text, text, text
	p.MetricName, p.Population, p.MetricVersion, p.Unit = text, text, text, text
	p.Derivation = make([]DerivationStep, MaxDerivationSteps)
	for index := range p.Derivation {
		p.Derivation[index] = DerivationStep{Operation: "avg", Algorithm: text, BucketSeconds: 86400}
	}
	p.Dimensions, p.Provider.EntityIDs = map[string]string{}, map[string]string{}
	for index := range maxDimensions {
		key := strings.Repeat("<", maxDimensionBytes-1) + string(rune('a'+index))
		p.Dimensions[key], p.Provider.EntityIDs[key] = strings.Repeat("<", maxDimensionBytes), strings.Repeat("<", maxDimensionBytes)
	}
	p.Provider.MetricID, p.Provider.Backend, p.Provider.Statistic = text, strings.Repeat("<", 64), strings.Repeat("<", 64)
	requireSeriesIdentity(t, p, SeriesObservation{Mode: "interval", BucketNanos: int64(24 * time.Hour)})
	p.Dimensions["overflow"] = "value"
	if _, err := MeasurementSeriesIdentity(p, SeriesObservation{Mode: "snapshot"}); err == nil {
		t.Fatal("unbounded point labels accepted")
	}
}

func TestMeasurementSeriesIdentityPreservesPermittedNUL(t *testing.T) {
	observation := SeriesObservation{Mode: "snapshot"}
	for _, name := range []string{"ProjectID", "Source", "ScenarioID", "Host", "Service", "MetricName", "Unit"} {
		t.Run(name, func(t *testing.T) {
			left, right := seriesIdentityPoint(), seriesIdentityPoint()
			field := reflect.ValueOf(&right).Elem().FieldByName(name)
			field.SetString(field.String() + "\x00suffix")
			if requireSeriesIdentity(t, left, observation) == requireSeriesIdentity(t, right, observation) {
				t.Fatal("NUL-bearing identity string collapsed")
			}
		})
	}
	for _, name := range []string{"Backend", "MetricID", "Statistic", "QueryID", "DefinitionHash"} {
		t.Run("provider/"+name, func(t *testing.T) {
			left := identityProviderPoint("appdynamics")
			if name == "QueryID" || name == "DefinitionHash" {
				left = identityProviderPoint("prometheus")
			}
			right := copySeriesIdentityPoint(left)
			field := reflect.ValueOf(right.Provider).Elem().FieldByName(name)
			value := field.String() + "\x00suffix"
			if name == "DefinitionHash" {
				value = "\x00" + field.String()[1:]
			}
			field.SetString(value)
			if requireSeriesIdentity(t, left, observation) == requireSeriesIdentity(t, right, observation) {
				t.Fatal("NUL-bearing provider identity string collapsed")
			}
		})
	}
}

func TestMeasurementSeriesIdentityRetainsExpandedElasticSemantics(t *testing.T) {
	p := seriesIdentityPoint()
	p.Provider = &ProviderMeasurement{Name: "elasticsearch", MetricID: "query", QueryID: "query", WindowKind: "bucket", BucketStart: p.TimestampUTC, BucketEnd: p.TimestampUTC.Add(time.Minute), ResolutionSeconds: 60, Statistic: "count"}
	observation := SeriesObservation{Mode: "interval", BucketNanos: int64(time.Minute)}
	first := requireSeriesIdentity(t, p, observation)
	for _, edit := range []func(*ProviderMeasurement){func(m *ProviderMeasurement) { m.QueryID = "other" }, func(m *ProviderMeasurement) { m.CatalogPath = "other" }, func(m *ProviderMeasurement) { m.Approximate = true }, func(m *ProviderMeasurement) { m.DerivedAggregation = "avg" }, func(m *ProviderMeasurement) { m.WindowKind = "query_window" }} {
		copy := copySeriesIdentityPoint(p)
		edit(copy.Provider)
		if identity := requireSeriesIdentity(t, copy, observation); identity == first {
			t.Fatal("Elastic semantics collapsed into one measurement")
		}
	}
}
