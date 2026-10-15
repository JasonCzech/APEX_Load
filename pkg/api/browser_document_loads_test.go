package api

import (
	"math"
	"testing"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// Fleet and archive merges weigh page-load averages by the loads behind them:
// a generator whose steps all ran in loaded documents adds no LCP, and one
// from before the count existed counts every sample as a load.
func TestBrowserMergesWeighPageLoadsByLoads(t *testing.T) {
	near := func(got, want float64) bool { return math.Abs(got-want) < 1e-6 }
	fleet := generatorrpc.BrowserMetrics{}
	for _, generator := range []generatorrpc.BrowserMetrics{
		{Samples: 10, DocumentLoads: 2, AvgLCPMs: 1000, AvgINPMs: 40},
		{Samples: 10, AvgINPMs: 60},
		{Samples: 4, AvgLCPMs: 2000, AvgINPMs: 50},
	} {
		addGeneratorBrowserMetrics(&fleet, generator)
	}
	finalizeGeneratorBrowserMetrics(&fleet)
	if fleet.Samples != 24 || fleet.DocumentLoads != 6 || !near(fleet.AvgLCPMs, (1000*2+2000*4)/6.0) || !near(fleet.AvgINPMs, (40*10+60*10+50*4)/24.0) {
		t.Fatalf("fleet = %+v", fleet)
	}

	merged := mergeRemoteArchiveSummaryBrowser(&BrowserLiveMetrics{Samples: 10, DocumentLoads: 2, AvgLCPMs: 1000}, &BrowserLiveMetrics{Samples: 10, AvgINPMs: 60})
	if merged.Samples != 20 || merged.DocumentLoads != 2 || !near(merged.AvgLCPMs, 1000) || !near(merged.AvgINPMs, 30) {
		t.Fatalf("archive merge = %+v", merged)
	}

	report := &BrowserLiveMetrics{}
	for _, step := range []*protocol.ClientMetrics{{LCPMs: 900, INPMs: 20}, {SameDocument: true, INPMs: 80}} {
		mergeReportBrowserMetrics(report, step)
	}
	finalizeReportBrowserMetrics(report)
	if report.Samples != 2 || report.DocumentLoads != 1 || !near(report.AvgLCPMs, 900) || !near(report.AvgINPMs, 50) {
		t.Fatalf("report = %+v", report)
	}
}
