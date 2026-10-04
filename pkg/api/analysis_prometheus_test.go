package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestPrometheusCanonicalAnalysisUsesEvaluationGrid(t *testing.T) {
	server, _, job, first := apiPrometheusFixture(t)
	from := first.TimestampUTC
	to := from.Add(time.Minute)
	batch := storage.ProviderMonitorBatch{QueryFromUTC: job.StartedAt, QueryUntilUTC: to}
	for index, value := range []float64{0, 10, 20, 30} {
		point := first
		point.TimestampUTC = from.Add(time.Duration(index) * 15 * time.Second)
		point.Value = &value
		batch.Points = append(batch.Points, point)
	}
	if err := server.ingestPrometheusBatch(context.Background(), job, 0, batch, executionLeaseTerm{}); err != nil {
		t.Fatal(err)
	}
	selection := AnalysisSeriesSelection{RunID: job.RunID, SourceID: job.Source.ID, Metric: first.MetricName, Aggregation: "avg"}
	query := AnalysisComparisonRequest{Left: selection, Right: selection, From: from, To: to, BucketSeconds: 30, Mode: "overlay"}
	response := timeslotRequest(t, server, "POST", "/api/v1/analysis/compare?project_id="+job.ProjectID, query)
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	var result struct {
		Left []analysis.MetricPoint `json:"left"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || len(result.Left) != 2 || result.Left[0].Value == nil || *result.Left[0].Value != 5 || result.Left[1].Value == nil || *result.Left[1].Value != 25 || result.Left[0].Provider == nil || result.Left[0].Provider.Mode != "analysis_bucket" || result.Left[0].Provider.DefinitionHash != first.Provider.DefinitionHash {
		t.Fatal("canonical analysis lost evaluation observations", result, err)
	}
	query.Left.Aggregation, query.Right.Aggregation = "sum", "sum"
	response = timeslotRequest(t, server, "POST", "/api/v1/analysis/compare?project_id="+job.ProjectID, query)
	if response.Code != 422 || !strings.Contains(response.Body.String(), "evaluation values") {
		t.Fatal("sum reconstruction accepted", response.Code, response.Body.String())
	}
}
