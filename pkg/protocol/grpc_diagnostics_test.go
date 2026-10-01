package protocol

import (
	"encoding/json"
	"math"
	"testing"
)

func TestGRPCDiagnosticAvailabilityAndSummary(t *testing.T) {
	first, budget, ok := 2.0, 100.0, int32(0)
	d := &GRPCDiagnostics{Method: "/example.Service/Call", Mode: "unary", CallMs: 10, FirstMessageMs: &first, DeadlineBudgetMs: &budget, StatusCode: &ok, MessagesSent: 1, MessagesReceived: 1}
	var summary *GRPCSummary
	MergeGRPCDiagnostic(&summary, d)
	clone := d.Clone()
	*clone.FirstMessageMs = 0
	*clone.StatusCode = 7
	if *d.FirstMessageMs != 2 || *d.StatusCode != 0 {
		t.Fatal("clone aliases optional fields")
	}
	MergeGRPCDiagnostic(&summary, clone) // A measured zero must count.
	d.FirstMessageMs, d.StatusCode = nil, nil
	d.MessagesReceived = 0
	MergeGRPCDiagnostic(&summary, d) // Missing measurements must not count.
	if summary.Call.Count != 3 || summary.FirstMessage.Count != 2 || summary.FirstMessage.Sum != 2 || summary.StatusCounts[0] != 1 || summary.StatusCounts[7] != 1 || summary.UnavailableStatus != 1 {
		t.Fatalf("summary=%+v", summary)
	}
	c := summary.Clone()
	c.Call.Buckets[0]++
	if c.Call.Buckets == summary.Call.Buckets {
		t.Fatal("summary clone aliases buckets")
	}
	data, err := json.Marshal(clone)
	if err != nil {
		t.Fatal(err)
	}
	var restored GRPCDiagnostics
	if err := json.Unmarshal(data, &restored); err != nil || restored.FirstMessageMs == nil || *restored.FirstMessageMs != 0 {
		t.Fatalf("zero lost in JSON roundtrip: %s, %v", data, err)
	}
}

func TestGRPCDiagnosticRejectsInvalidEvidence(t *testing.T) {
	for _, change := range []func(*GRPCDiagnostics){
		func(d *GRPCDiagnostics) { d.Method = "/example/Call?token=private" },
		func(d *GRPCDiagnostics) { d.Mode = "unknown" },
		func(d *GRPCDiagnostics) { d.CallMs = math.NaN() },
		func(d *GRPCDiagnostics) { d.CallMs = math.Inf(1) },
		func(d *GRPCDiagnostics) { d.MessagesSent = -1 },
		func(d *GRPCDiagnostics) { d.RequestBytes = 1 << 41 },
		func(d *GRPCDiagnostics) { v := int32(17); d.StatusCode = &v },
		func(d *GRPCDiagnostics) { v := 2.0; d.FirstMessageMs = &v },
	} {
		d := &GRPCDiagnostics{Method: "/example.Service/Call", Mode: "unary", CallMs: 1}
		change(d)
		var summary *GRPCSummary
		MergeGRPCDiagnostic(&summary, d)
		if summary != nil || d.Valid() {
			t.Fatalf("accepted invalid diagnostic: %+v", d)
		}
	}
}

func TestDiagnosticDistributionQuantiles(t *testing.T) {
	var d DiagnosticDistribution
	if _, ok := d.Quantile(.95); ok {
		t.Fatal("empty distribution scored")
	}
	for i := 0; i < 100; i++ {
		d.Add(float64(i))
	}
	d.Add(math.NaN())
	d.Add(math.Inf(1))
	d.Add(-1)
	if d.Count != 100 || d.Sum != 4950 || d.Min != 0 || d.Max != 99 {
		t.Fatalf("distribution=%+v", d)
	}
	p95, ok := d.Quantile(.95)
	if !ok || p95 < 94 || p95 > 99 {
		t.Fatalf("p95=%v available=%v", p95, ok)
	}
	for _, q := range []float64{-1, 2, math.NaN()} {
		if _, ok := d.Quantile(q); ok {
			t.Fatal("invalid quantile accepted")
		}
	}
}
