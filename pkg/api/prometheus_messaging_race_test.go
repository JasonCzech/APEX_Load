package api

import (
	"strings"
	"testing"
)

// TestPrometheusMessagingSnapshotIsRaceFree covers api-telemetry-1: a /metrics
// scrape of the messaging exporter must not race the publish loop that swaps
// test.LiveMetrics under tr.mu. Run under `go test -race`; without the snapshot
// (liveMetricsSnapshot reads the pointer under the lock) this is a data race.
func TestPrometheusMessagingSnapshotIsRaceFree(t *testing.T) {
	tr := &TestRunner{tests: map[string]*ManagedTest{}}
	mt := &ManagedTest{ID: "t1", Status: "RUNNING"}
	tr.tests["t1"] = mt

	mk := func() *LiveMetrics {
		return &LiveMetrics{DependencyHealth: map[string]*TxMetrics{
			"kafka": {
				Name:             "orders",
				DependencyType:   "kafka_topic",
				DependencyTarget: "orders",
				ServerTelemetry:  &ServerTelemetrySummary{AvgConsumerLagMs: 5},
			},
		}}
	}
	mt.LiveMetrics = mk()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 2000; i++ {
			tr.mu.Lock()
			mt.LiveMetrics = mk() // the publish-loop swap (metrics.go)
			tr.mu.Unlock()
		}
	}()

	const expected = `apexload_messaging_consumer_lag_ms{test_id="t1",protocol="kafka",target="orders"} 5`
	scrapes := 0
	invalidOutput := ""

scrapeLoop:
	for {
		var b strings.Builder
		writeMessagingMetrics(&b, tr.liveMetricsSnapshot())
		scrapes++
		if !strings.Contains(b.String(), expected) && invalidOutput == "" {
			invalidOutput = b.String()
		}
		select {
		case <-done:
			break scrapeLoop
		default:
		}
	}
	if scrapes == 0 {
		t.Fatal("concurrent scrape loop did not execute")
	}
	if invalidOutput != "" {
		t.Fatalf("concurrent scrape omitted the stable messaging series:\n%s", invalidOutput)
	}
}
