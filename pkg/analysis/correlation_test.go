package analysis

import (
	"testing"
	"time"
)

func TestLaggedCorrelationCoverageAndAssociation(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var left, right []MetricPoint
	for i, v := range []float64{1, 4, 2, 8, 3} {
		p := MetricPoint{SchemaVersion: SchemaVersion, ProjectID: "p", RunID: "r", Source: "s", MetricName: "latency", Kind: KindGauge, Unit: "ms", TimestampUTC: start.Add(time.Duration(i) * time.Second), Value: &v, Quality: QualityValid}
		left = append(left, p)
		p.TimestampUTC = p.TimestampUTC.Add(time.Second)
		p.Source = "host"
		p.MetricName = "cpu"
		p.Unit = "percent"
		right = append(right, p)
	}
	scores, err := LaggedCorrelation(left, right, start, start.Add(6*time.Second), 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(scores) != 3 || scores[2].Coefficient == nil || *scores[2].Coefficient < .999 || scores[2].Coverage != 1 {
		t.Fatalf("scores=%+v", scores)
	}
	right[2].Value = nil
	right[2].Quality = QualityMissing
	scores, err = LaggedCorrelation(left, right, start, start.Add(6*time.Second), 1, 1)
	if err != nil || scores[2].Pairs != 4 || scores[2].Coverage != .8 {
		t.Fatalf("missing coverage: %+v %v", scores, err)
	}
	right[0].ProjectID = "other"
	if _, err = LaggedCorrelation(left, right, start, start.Add(6*time.Second), 1, 1); err == nil {
		t.Fatal("cross-project correlation accepted")
	}
}

func TestLaggedCorrelationBoundsAndConstantUnscored(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct{ bucket, lag int }{{0, 0}, {1, -1}, {1, 3601}, {1, 61}, {10, 11}} {
		if _, err := LaggedCorrelation(nil, nil, start, start.Add(time.Minute), tc.bucket, tc.lag); err == nil {
			t.Fatalf("accepted %+v", tc)
		}
	}
	scores, err := LaggedCorrelation(nil, nil, start, start.Add(time.Minute), 10, 0)
	if err != nil || len(scores) != 1 || scores[0].Coefficient != nil || scores[0].Coverage != 0 {
		t.Fatalf("empty: %+v %v", scores, err)
	}
}
