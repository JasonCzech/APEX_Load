package analysis

import (
	"math"
	"strings"
	"testing"
	"time"
)

func point(at time.Time, value float64) MetricPoint {
	return MetricPoint{
		SchemaVersion: SchemaVersion, ProjectID: "project", RunID: "run", Source: "collector",
		TimestampUTC: at, MetricName: "cpu_percent", Kind: KindGauge, Value: &value, Quality: QualityValid,
	}
}

func TestMetricPointValidate(t *testing.T) {
	now := time.Now().UTC()
	cases := []struct {
		name string
		edit func(*MetricPoint)
		want bool
	}{
		{"valid zero", func(p *MetricPoint) { zero := 0.0; p.Value = &zero }, true},
		{"missing is not zero", func(p *MetricPoint) { p.Value = nil; p.Quality = QualityMissing }, true},
		{"missing with value", func(p *MetricPoint) { p.Quality = QualityMissing }, false},
		{"declared population", func(p *MetricPoint) { p.Population = PopulationAction; p.MetricVersion = "v1" }, true},
		{"blank population", func(p *MetricPoint) { p.Population = " " }, false},
		{"NUL metric version", func(p *MetricPoint) { p.MetricVersion = "v\x00x" }, false},
		{"oversized population", func(p *MetricPoint) { p.Population = strings.Repeat("x", maxMetricIdentityBytes+1) }, false},
		{"nonfinite", func(p *MetricPoint) { v := math.Inf(1); p.Value = &v }, false},
		{"unserializable timestamp", func(p *MetricPoint) { p.TimestampUTC = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) }, false},
		{"unknown kind", func(p *MetricPoint) { p.Kind = "percentile" }, false},
		{"non UTC", func(p *MetricPoint) { p.TimestampUTC = p.TimestampUTC.In(time.FixedZone("offset", 3600)) }, false},
		{"unbounded identity", func(p *MetricPoint) { p.Source = strings.Repeat("x", maxMetricIdentityBytes+1) }, false},
		{"too many dimensions", func(p *MetricPoint) {
			p.Dimensions = map[string]string{}
			for i := 0; i <= maxDimensions; i++ {
				p.Dimensions[string(rune('a'+i))] = "value"
			}
		}, false},
		{"relative without run", func(p *MetricPoint) { p.RunID = ""; v := int64(1); p.RelativeTimeMS = &v }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := point(now, 1)
			tc.edit(&p)
			if got := p.Validate() == nil; got != tc.want {
				t.Fatalf("Validate() success = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNormalizeRunClockIncludesPause(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	p := point(start.Add(90*time.Second), 12)
	active := int64(30_000)
	p.ActiveRuntimeMS = &active
	got, err := NormalizeRunClock(p, start)
	if err != nil || got.RelativeTimeMS == nil || *got.RelativeTimeMS != 90_000 || *got.ActiveRuntimeMS != 30_000 {
		t.Fatalf("NormalizeRunClock = %+v, %v", got, err)
	}
	if p.RelativeTimeMS != nil {
		t.Fatal("input mutated")
	}
}

func TestAlignAndPearson(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	left, right := []MetricPoint{}, []MetricPoint{}
	for i := 0; i < 4; i++ {
		left = append(left, point(start.Add(time.Duration(i)*time.Second), float64(i)))
		right = append(right, point(start.Add(time.Duration(i)*time.Second+100*time.Millisecond), float64(i*2)))
	}
	stale := point(start.Add(5*time.Second), 20)
	stale.Quality = QualityStale
	left = append(left, stale)
	pairs, err := Align(left, right, 200*time.Millisecond)
	if err != nil || len(pairs) != 4 || pairs[0].LagMS != 100 {
		t.Fatalf("Align = %+v, %v", pairs, err)
	}
	score, ok := Pearson(pairs)
	if !ok || math.Abs(score-1) > 1e-9 {
		t.Fatalf("Pearson = %v, %v", score, ok)
	}
	if _, err := Align(left, right, -time.Second); err == nil {
		t.Fatal("negative tolerance accepted")
	}
	if _, ok := Pearson(pairs[:2]); ok {
		t.Fatal("two pairs were scored")
	}
	for i := range pairs {
		v := 5.0
		pairs[i].Right.Value = &v
	}
	if _, ok := Pearson(pairs); ok {
		t.Fatal("constant series was scored")
	}
}

func TestStatisticalInputsMustBeSingleQualifiedSeries(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		edit func([]MetricPoint)
	}{
		{"host", func(p []MetricPoint) { p[1].Host = "other" }},
		{"dimension", func(p []MetricPoint) { p[1].Dimensions = map[string]string{"instance": "other"} }},
		{"metric", func(p []MetricPoint) { p[1].MetricName = "memory" }},
		{"population", func(p []MetricPoint) { p[1].Population = PopulationMessage }},
		{"metric version", func(p []MetricPoint) { p[1].MetricVersion = "v2" }},
		{"unit", func(p []MetricPoint) { p[1].Unit = "bytes" }},
		{"dataset", func(p []MetricPoint) { p[1].DatasetID = "external" }},
		{"duplicate time", func(p []MetricPoint) { p[1].TimestampUTC = p[0].TimestampUTC }},
		{"counter", func(p []MetricPoint) {
			for i := range p {
				p[i].Kind = KindCounter
			}
		}},
		{"summary", func(p []MetricPoint) {
			for i := range p {
				p[i].Kind = KindSummary
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			left := []MetricPoint{point(start, 1), point(start.Add(time.Second), 2), point(start.Add(2*time.Second), 3)}
			right := append([]MetricPoint(nil), left...)
			tc.edit(left)
			if _, err := Align(left, right, time.Second); err == nil {
				t.Fatal("invalid series aligned")
			}
			pairs := make([]AlignedPair, len(left))
			for i := range left {
				pairs[i] = AlignedPair{Left: left[i], Right: right[i]}
			}
			if _, ok := Pearson(pairs); ok {
				t.Fatal("invalid series scored")
			}
		})
	}
	if _, err := Align(make([]MetricPoint, MaxSeriesPoints+1), nil, 0); err == nil {
		t.Fatal("unbounded series accepted")
	}
	left, right := []MetricPoint{point(start, 1)}, []MetricPoint{point(start, 2)}
	for _, edit := range []func(*MetricPoint){func(p *MetricPoint) { p.ProjectID = "other" }, func(p *MetricPoint) { p.RunID = "" }, func(p *MetricPoint) { p.DatasetID = "import" }} {
		p := right[0]
		edit(&p)
		if _, err := Align(left, []MetricPoint{p}, 0); err == nil {
			t.Fatal("unassociated evidence aligned")
		}
	}
}

func TestPearsonNumericalRangeAndQuality(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, scale := range []float64{1, 1e150, 1e-200, math.MaxFloat64 / 4} {
		pairs := make([]AlignedPair, 3)
		for i := range pairs {
			at := start.Add(time.Duration(i) * time.Second)
			pairs[i] = AlignedPair{Left: point(at, float64(i+1)*scale), Right: point(at, -float64(i+1)*scale)}
		}
		if score, ok := Pearson(pairs); !ok || math.Abs(score+1) > 1e-12 {
			t.Fatalf("scale %g: score=%g, ok=%v", scale, score, ok)
		}
		pairs[1].Left.Quality = QualityStale
		if _, ok := Pearson(pairs); ok {
			t.Fatal("stale pair scored")
		}
	}
}

func TestAlignMonotonicNearestAndNoMutation(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	left := []MetricPoint{point(start.Add(3*time.Second), 3), point(start.Add(time.Second), 1)}
	right := []MetricPoint{point(start, 0), point(start.Add(2*time.Second), 2), point(start.Add(4*time.Second), 4)}
	pairs, err := Align(left, right, time.Second)
	if err != nil || len(pairs) != 2 || *pairs[0].Right.Value != 0 || *pairs[1].Right.Value != 2 {
		t.Fatalf("pairs=%+v err=%v", pairs, err)
	}
	if *left[0].Value != 3 || right[0].Quality != QualityValid {
		t.Fatal("inputs mutated")
	}
}

func TestNormalizeRunClockRejectsDurationOverflow(t *testing.T) {
	start := time.Date(2, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := NormalizeRunClock(point(start.AddDate(500, 0, 0), 1), start); err == nil {
		t.Fatal("saturated run clock accepted")
	}
}
