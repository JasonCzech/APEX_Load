package engine

import (
	"math"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// Page-load measures average over the steps that loaded a document; steps in
// a document an earlier step loaded (a single-page app) count for the step
// measures only.
func TestBrowserSummaryAveragesPageLoadsOverLoads(t *testing.T) {
	steps := []*protocol.ClientMetrics{
		{FCPMs: 400, LCPMs: 1200, TTFBMs: 100, BlockingResourceCount: 4, INPMs: 30, CLSScore: 0.05},
		{SameDocument: true, INPMs: 120, CLSScore: 0.01},
		{SameDocument: true, INPMs: 90},
		{FCPMs: 200, LCPMs: 800, TTFBMs: 60, BlockingResourceCount: 2, INPMs: 60},
	}
	summary := &BrowserMetricsSummary{}
	device := &BrowserDeviceMetrics{}
	for _, step := range steps {
		mergeBrowserMetricsSummary(summary, step)
		mergeBrowserDeviceMetrics(device, step)
	}
	finalizeBrowserMetricsSummary(summary)
	finalizeBrowserDeviceMetrics(device)
	near := func(got, want float64) bool { return math.Abs(got-want) < 1e-9 }
	for name, got := range map[string]BrowserMetricsSummary{
		"summary": *summary,
		"device": {Samples: device.Samples, DocumentLoads: device.DocumentLoads, AvgFCPMs: device.AvgFCPMs, AvgLCPMs: device.AvgLCPMs, AvgTTFBMs: device.AvgTTFBMs,
			AvgBlockingResources: device.AvgBlockingResources, AvgINPMs: device.AvgINPMs, AvgCLSScore: device.AvgCLSScore},
	} {
		if got.Samples != 4 || got.DocumentLoads != 2 || !near(got.AvgLCPMs, 1000) || !near(got.AvgFCPMs, 300) || !near(got.AvgTTFBMs, 80) ||
			!near(got.AvgBlockingResources, 3) || !near(got.AvgINPMs, 75) || !near(got.AvgCLSScore, 0.015) {
			t.Fatalf("%s = %+v", name, got)
		}
	}

	// Steps in one document alone have no page-load averages.
	inPage := &BrowserMetricsSummary{}
	mergeBrowserMetricsSummary(inPage, &protocol.ClientMetrics{SameDocument: true, INPMs: 40})
	finalizeBrowserMetricsSummary(inPage)
	if inPage.DocumentLoads != 0 || inPage.AvgLCPMs != 0 || inPage.AvgINPMs != 40 {
		t.Fatalf("in-page only = %+v", inPage)
	}

	// A summary saved before the count existed averaged every sample as a load.
	if got := BrowserDocumentLoads(0, 5, 0, 900, 0, 0); got != 5 {
		t.Fatalf("legacy loads = %d", got)
	}
	if got := BrowserDocumentLoads(0, 5, 0, 0, 0, 0); got != 0 {
		t.Fatalf("no loads = %d", got)
	}
	if got := BrowserDocumentLoads(2, 5, 0, 900, 0, 0); got != 2 {
		t.Fatalf("counted loads = %d", got)
	}
}
