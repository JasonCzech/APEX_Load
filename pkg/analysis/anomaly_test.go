package analysis

import (
	"testing"
	"time"
)

func TestDetectAnomaly(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	baseline := make([]MetricPoint, 30)
	for i := range baseline {
		baseline[i] = point(start.Add(time.Duration(i)*time.Minute), float64(10+i%3))
	}
	current := point(start.Add(31*time.Minute), 20)
	evidence, ok, err := DetectAnomaly(current, baseline)
	if err != nil || !ok || !evidence.Exceeded || evidence.BaselineBuckets != 30 || evidence.ZScore < 3 {
		t.Fatalf("DetectAnomaly = %+v, %v, %v", evidence, ok, err)
	}
	if _, ok, err := DetectAnomaly(current, baseline[:29]); err != nil || ok {
		t.Fatalf("insufficient baseline = %v, %v", ok, err)
	}
	baseline[0].Quality = QualityStale
	if _, ok, err := DetectAnomaly(current, baseline); err != nil || ok {
		t.Fatalf("stale bucket counted = %v, %v", ok, err)
	}
	baseline[0].Quality = QualityValid
	baseline[0].Unit = "seconds"
	if _, _, err := DetectAnomaly(current, baseline); err == nil {
		t.Fatal("incompatible baseline was scored")
	}
}

func TestDetectAnomalyConstantAndMissing(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	baseline := make([]MetricPoint, 30)
	for i := range baseline {
		baseline[i] = point(start.Add(time.Duration(i)*time.Minute), 10)
	}
	current := point(start.Add(31*time.Minute), 20)
	if _, ok, err := DetectAnomaly(current, baseline); err != nil || ok {
		t.Fatalf("constant baseline = %v, %v", ok, err)
	}
	current.Quality = QualityMissing
	current.Value = nil
	if _, ok, err := DetectAnomaly(current, baseline); err != nil || ok {
		t.Fatalf("missing current = %v, %v", ok, err)
	}
}

func TestAnomalyRejectsUnqualifiedBaselines(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		edit func(*MetricPoint, []MetricPoint)
	}{
		{"different host", func(_ *MetricPoint, p []MetricPoint) {
			for i := range p {
				p[i].Host = "other"
			}
		}},
		{"different dimension", func(_ *MetricPoint, p []MetricPoint) {
			for i := range p {
				p[i].Dimensions = map[string]string{"method": "other"}
			}
		}},
		{"duplicate buckets", func(_ *MetricPoint, p []MetricPoint) { p[1].TimestampUTC = p[0].TimestampUTC }},
		{"current bucket", func(c *MetricPoint, p []MetricPoint) { p[0].TimestampUTC = c.TimestampUTC }},
		{"future bucket", func(c *MetricPoint, p []MetricPoint) { p[0].TimestampUTC = c.TimestampUTC.Add(time.Second) }},
		{"counter", func(c *MetricPoint, p []MetricPoint) {
			c.Kind = KindCounter
			for i := range p {
				p[i].Kind = KindCounter
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			baseline := make([]MetricPoint, 30)
			for i := range baseline {
				baseline[i] = point(start.Add(time.Duration(i)*time.Minute), float64(i%3))
			}
			current := point(start.Add(time.Hour), 20)
			tc.edit(&current, baseline)
			if _, ok, err := DetectAnomaly(current, baseline); ok || err == nil {
				t.Fatalf("unqualified baseline scored: %v, %v", ok, err)
			}
		})
	}
}
