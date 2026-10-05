package storage

import (
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/dynatrace"
)

func TestDynatraceSelectedCoverageMatchesFrozenWindowAndCapabilities(t *testing.T) {
	from := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	to := from.Add(time.Minute)
	selection := &dynatrace.Selection{Metrics: []dynatrace.MetricSelection{{Name: "cpu"}}, Grail: []dynatrace.GrailQuery{{Name: "logs"}}, Problems: true, Events: true, TopologyMode: "classic"}
	coverage := map[string]analysis.ProviderCoverage{}
	keys := []string{"metrics:cpu", "grail:logs", "problems", "events", "topology"}
	for _, key := range keys {
		coverage[key] = analysis.ProviderCoverage{Status: "complete", From: from, To: to}
	}
	if !dynatraceSelectedCoverageComplete(selection, coverage, from, to) {
		t.Fatal("complete no-data queries must establish query coverage")
	}
	for _, key := range keys {
		original := coverage[key]
		t.Run(key, func(t *testing.T) {
			delete(coverage, key)
			if dynatraceSelectedCoverageComplete(selection, coverage, from, to) {
				t.Fatal("missing selected query established complete coverage")
			}
			for _, altered := range []analysis.ProviderCoverage{
				{Status: "complete", From: from.Add(time.Second), To: to},
				{Status: "complete", From: from, To: to.Add(-time.Second)},
				{Status: "partial", From: from, To: to},
				{Status: "complete", From: from, To: to, Truncated: true},
			} {
				coverage[key] = altered
				if dynatraceSelectedCoverageComplete(selection, coverage, from, to) {
					t.Fatal("incomplete or different-window query established coverage")
				}
			}
			coverage[key] = original
		})
	}
}
