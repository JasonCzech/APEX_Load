package perfevidence

import (
	"encoding/json"
	"fmt"
	"github.com/jasonczech/apexload/pkg/engine"
	"math"
	"strings"
	"testing"
)

func javaEvidencePair() (JavaEvidence, JavaEvidence) {
	r := JavaEvidence{Schema: JavaEvidenceSchema, Workload: "light", Profile: "default", Mode: "dedicated", ConfigurationSHA256: strings.Repeat("a", 64),
		Identity: JavaEvidenceIdentity{WorkloadSHA256: strings.Repeat("b", 64), RuntimeSHA256: strings.Repeat("c", 64), Environment: "linux", Backend: "sidecar", ResourceProfile: "2cpu-8gb", Target: "fixture-v1", VUsers: 25, OfferedTPS: 100, WarmupSeconds: 40, MeasurementSeconds: 10, MemoryMethod: "aggregate_process_pss_bytes", AccountingScope: "entire-execution"}, SoakSeconds: 3600, SoakPassed: true}
	for i := 0; i < 5; i++ {
		r.Samples = append(r.Samples, JavaEvidenceSample{MemoryBytes: []float64{1000, 1000}, Transactions: 1000, LatencySamples: 1000, TPS: 100, P95MS: 10, P99MS: 20, Complete: true, FunctionalChecksPassed: true})
	}
	r.Soak = idealJavaSoak(r.Identity.VUsers)
	r.SoakSeconds = r.Soak.ActiveSeconds
	c := r
	c.Mode = "pooled"
	c.Profile = "pooled"
	h := engine.JavaHostEvidence{HostID: "host", Observations: 1, GCStatus: "available", UnflushedTail: true}
	for i := 0; i < c.Identity.VUsers; i++ {
		h.SlotIDs = append(h.SlotIDs, fmt.Sprintf("slot-%d", i))
	}
	c.Soak = idealJavaSoak(c.Identity.VUsers)
	c.Soak.HostDiagnostics = []engine.JavaHostEvidence{h}
	c.Samples = append([]JavaEvidenceSample(nil), r.Samples...)
	for i := range c.Samples {
		c.Samples[i].MemoryBytes = []float64{700, 700}
		c.Samples[i].HostDiagnostics = []engine.JavaHostEvidence{h}
	}
	return r, c
}

func TestJavaGate(t *testing.T) {
	tests := []struct {
		name, status string
		change       func(*JavaEvidence, *JavaEvidence)
	}{
		{"boundary pass", "pass", func(b, c *JavaEvidence) {
			for i := range c.Samples {
				c.Samples[i].P95MS = 10.5
				c.Samples[i].P99MS = 21
				c.Samples[i].TPS = 95
				c.Samples[i].Transactions = 950
			}
		}},
		{"too little memory benefit", "fail", func(b, c *JavaEvidence) {
			for i := range c.Samples {
				c.Samples[i].MemoryBytes = []float64{701, 701}
			}
		}},
		{"latency regression", "fail", func(b, c *JavaEvidence) {
			for i := range c.Samples {
				c.Samples[i].P99MS = 22
			}
		}},
		{"missed offered load", "fail", func(b, c *JavaEvidence) {
			for i := range c.Samples {
				c.Samples[i].TPS = 90
				c.Samples[i].Transactions = 900
			}
		}},
		{"functional failure", "fail", func(b, c *JavaEvidence) { c.Samples[0].FunctionalChecksPassed = false }},
		{"new harness error", "fail", func(b, c *JavaEvidence) { c.Samples[0].HarnessErrors = 1 }},
		{"resource failure", "fail", func(b, c *JavaEvidence) { c.Samples[0].ResourceFailures = 1 }},
		{"different target", "inconclusive", func(b, c *JavaEvidence) { c.Identity.Target = "other" }},
		{"different artifact", "inconclusive", func(b, c *JavaEvidence) { c.Identity.RuntimeSHA256 = strings.Repeat("d", 64) }},
		{"short run count", "inconclusive", func(b, c *JavaEvidence) { c.Samples = c.Samples[:4] }},
		{"dropped metrics", "inconclusive", func(b, c *JavaEvidence) { c.Samples[0].DroppedResults = 1 }},
		{"noise", "inconclusive", func(b, c *JavaEvidence) { c.Samples[0].MemoryBytes = []float64{600, 600} }},
		{"unobserved soak", "inconclusive", func(b, c *JavaEvidence) { c.Soak = nil }},
		{"missing host diagnostics", "inconclusive", func(b, c *JavaEvidence) { c.Samples[0].HostDiagnostics = nil }},
		{"diagnostic loss", "inconclusive", func(b, c *JavaEvidence) { c.Samples[0].HostDiagnostics[0].ReadFailures = 1 }},
		{"missing soak", "inconclusive", func(b, c *JavaEvidence) { c.SoakSeconds = 3599 }},
		{"no warmup", "inconclusive", func(b, c *JavaEvidence) { b.Identity.WarmupSeconds = 0; c.Identity.WarmupSeconds = 0 }},
		{"incomplete", "inconclusive", func(b, c *JavaEvidence) { c.Samples[0].Complete = false }},
		{"invalid finite value", "inconclusive", func(b, c *JavaEvidence) { c.Samples[0].P99MS = math.NaN() }},
		{"counter mismatch", "inconclusive", func(b, c *JavaEvidence) { c.Samples[0].Transactions = 7 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, c := javaEvidencePair()
			tt.change(&b, &c)
			got := EvaluateJavaGate(b, c)
			if got.Status != tt.status || got.ProductionQualified {
				t.Fatalf("gate = %+v", got)
			}
		})
	}
}

func TestReadJavaEvidenceStrict(t *testing.T) {
	b, _ := javaEvidencePair()
	data, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ReadJavaEvidence(strings.NewReader(string(data))); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{string(data) + "{}", `{"unknown":true}`, strings.Repeat(" ", 16<<20) + string(data)} {
		if _, err := ReadJavaEvidence(strings.NewReader(text)); err == nil {
			t.Fatal("accepted invalid evidence")
		}
	}
}
