package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/appdynamics"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestAppDynamicsDirectChunksBoundLongPathsAndCommitProgressLast(t *testing.T) {
	from := time.Now().UTC().Add(-10 * time.Minute).Truncate(time.Minute)
	to := from.Add(5 * time.Minute)
	result := appdynamics.Result{Progress: appdynamics.QueryProgress{NextQuery: 1, TotalQueries: 1, Complete: true}}
	for i := range 256 {
		value := float64(i)
		result.Points = append(result.Points, analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: "p", RunID: "r", Source: "s", TimestampUTC: from, MetricName: "calls", Kind: analysis.KindGauge, Value: &value, Quality: analysis.QualityValid, Provider: &analysis.ProviderMeasurement{Name: "appdynamics", MetricID: "1", CatalogPath: strings.Repeat("x", 4096), BucketStart: from, BucketEnd: from.Add(time.Minute), ResolutionSeconds: 60, Statistic: "sum"}})
	}
	count, chunks := 0, 0
	for len(result.Points) > 0 {
		batch, n, m, err := boundedDirectAppDynamicsBatch(result, storage.RunMonitorJob{State: "reconciling"}, from, to)
		if err != nil || n == 0 || m != 0 {
			t.Fatalf("batch n=%d m=%d err=%v", n, m, err)
		}
		body, _ := json.Marshal(batch)
		if len(body) > 768<<10 || !batch.QueryFromUTC.Equal(from) || !batch.QueryUntilUTC.Equal(to) {
			t.Fatal("unbounded or unfrozen chunk")
		}
		last := n == len(result.Points)
		if (batch.Progress != nil) != last || (batch.QueryWindowEnd != nil) != last || batch.Final != last {
			t.Fatal("progress acknowledged before final chunk")
		}
		count += n
		chunks++
		result.Points = result.Points[n:]
	}
	if count != 256 || chunks < 2 {
		t.Fatalf("lost long-path points count=%d chunks=%d", count, chunks)
	}
}
