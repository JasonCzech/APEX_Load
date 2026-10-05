package api

import (
	"encoding/json"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	"reflect"
	"testing"
)

func unavailableDocumentMetricFixture(count int64) *protocol.BrowserDocumentSummary {
	return &protocol.BrowserDocumentSummary{Version: protocol.BrowserEvidenceVersion, Metrics: []protocol.BrowserDocumentMetricSummary{{Metric: "lcp", Unit: "ms", Engine: "webkit", Algorithm: protocol.BrowserVitalsAlgorithm, AlgorithmSHA256: protocol.BrowserVitalsAlgorithmSHA256, FrameScope: "main_frame", LifecyclePhase: "navigation", UnavailableFinal: count, ExcludedReasons: map[string]int64{"unsupported": count}}}}
}

func TestBrowserDocumentsLiveLocalAndDistributedRemainIndependentOfActionPopulation(t *testing.T) {
	summary := unavailableDocumentMetricFixture(1)
	if summary.Clone() == nil {
		t.Fatal("invalid fixture")
	}
	local := buildLiveMetricsFromSnapshot(engine.MetricsSnapshot{BrowserDocuments: summary}, nil)
	combined, tps, groups := aggregateDistributedMetricsBatches("browser", map[string]generatorrpc.MetricsBatch{
		"a": {BrowserDocuments: summary}, "b": {BrowserDocuments: unavailableDocumentMetricFixture(2)},
	})
	remote := buildLiveMetricsFromDistributedBatch(&ManagedTest{}, combined, tps, groups)
	if local.Browser != nil || remote.Browser != nil || local.TotalTx != 0 || remote.TotalTx != 0 || local.TPS != 0 || remote.TPS != 0 {
		t.Fatal("standalone documents fabricated action/transaction population")
	}
	if local.BrowserDocuments == nil || local.BrowserDocuments.Metrics[0].UnavailableFinal != 1 || remote.BrowserDocuments == nil || remote.BrowserDocuments.Metrics[0].UnavailableFinal != 3 || remote.BrowserDocuments.Metrics[0].Distribution.Count != 0 {
		t.Fatal("independent unavailable population lost or replaced by zero")
	}
	cloned := cloneLiveMetrics(remote)
	raw, err := json.Marshal(cloned)
	if err != nil {
		t.Fatal(err)
	}
	var restored LiveMetrics
	if err = json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cloned.BrowserDocuments, restored.BrowserDocuments) {
		t.Fatal("history JSON roundtrip lost document evidence")
	}
	remote.BrowserDocuments.Metrics[0].ExcludedReasons["unsupported"] = 99
	if cloned.BrowserDocuments.Metrics[0].ExcludedReasons["unsupported"] != 3 {
		t.Fatal("live history aliases exclusion map")
	}
	summary.Metrics[0].UnavailableFinal = 99
	if local.BrowserDocuments.Metrics[0].UnavailableFinal != 1 {
		t.Fatal("local live document population aliases generator input")
	}
}

func TestBrowserDocumentsMixedAndMalformedPeersExposeIncompleteCoverage(t *testing.T) {
	summary := unavailableDocumentMetricFixture(1)
	malformed := unavailableDocumentMetricFixture(1)
	malformed.Metrics[0].Distribution.Count = 1
	for name, older := range map[string]generatorrpc.MetricsBatch{
		"legacy":    {Browser: &generatorrpc.BrowserMetrics{Samples: 100, AvgLCPMs: 20}},
		"malformed": {BrowserDocuments: malformed},
	} {
		t.Run(name, func(t *testing.T) {
			combined, tps, groups := aggregateDistributedMetricsBatches("browser", map[string]generatorrpc.MetricsBatch{"current": {BrowserDocuments: summary}, "older": older})
			live := buildLiveMetricsFromDistributedBatch(&ManagedTest{}, combined, tps, groups)
			if live.BrowserDocuments == nil || !live.BrowserDocuments.Incomplete || len(live.BrowserDocuments.Metrics) != 1 || live.BrowserDocuments.Metrics[0].UnavailableFinal != 1 || live.BrowserDocuments.Metrics[0].EligibleFinal != 0 {
				t.Fatal("mixed peer supplied false document counts or complete coverage")
			}
		})
	}
	if browserDocumentsForLive(nil, false) != nil {
		t.Fatal("non-browser peer creates document population")
	}
	old := buildLiveMetricsFromSnapshot(engine.MetricsSnapshot{Browser: &engine.BrowserMetricsSummary{Samples: 100}}, nil)
	if old.BrowserDocuments == nil || !old.BrowserDocuments.Incomplete || len(old.BrowserDocuments.Metrics) != 0 || old.Browser == nil || old.Browser.Samples != 100 {
		t.Fatal("legacy local samples became document observations")
	}
}
