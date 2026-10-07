package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/elastic"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestProviderEvidenceFiltersAndSparseCanonicalPages(t *testing.T) {
	server, _, job, p, sample := apiElasticMonitorFixture(t)
	record := func(id, service, severity string, at time.Time) analysis.ProviderEvidence {
		d := sample
		d.ID, d.DocumentID = id, id
		d.Service, d.Severity, d.TimestampUTC = service, severity, at
		d.Log = &analysis.ProviderLog{Message: "retained matching document"}
		return d
	}
	batch := storage.ProviderMonitorBatch{ResolvedIndices: []string{sample.Index}, QueryFromUTC: job.StartedAt, QueryUntilUTC: job.StartedAt.Add(5 * time.Minute), Evidence: []analysis.ProviderEvidence{
		record("1", "checkout", "error", p.TimestampUTC),
		record("2", "checkout", "error", p.TimestampUTC.Add(time.Nanosecond)),
		record("3", "other", "info", p.TimestampUTC),
	}}
	if err := server.ingestExpandedElasticBatch(context.Background(), job, 0, batch, executionLeaseTerm{}); err != nil {
		t.Fatal(err)
	}
	base := "/api/v1/tests/" + job.RunID + "/monitor-evidence?signal=log&service=checkout&severity=error&limit=1"
	var page struct {
		Evidence []RunMonitorEvidence `json:"evidence"`
		HasMore  bool                 `json:"has_more"`
		AfterKey string               `json:"after_key"`
	}
	first := timeslotRequest(t, server, http.MethodGet, base, nil)
	if first.Code != 200 || json.Unmarshal(first.Body.Bytes(), &page) != nil || len(page.Evidence) != 1 || !page.HasMore {
		t.Fatalf("first: %d %s", first.Code, first.Body.String())
	}
	id := page.Evidence[0].ID
	second := timeslotRequest(t, server, http.MethodGet, base+"&after_key="+page.AfterKey, nil)
	if second.Code != 200 || json.Unmarshal(second.Body.Bytes(), &page) != nil || len(page.Evidence) != 1 || page.HasMore || page.Evidence[0].ID == id {
		t.Fatalf("second: %d %s", second.Code, second.Body.String())
	}
	for _, query := range []string{"signal=span&kind=log", "signal=arbitrary", "from=2026-10-05T00:00:00-04:00", "from=broken", "from=2026-10-05T00:00:00Z&until=2026-10-05T00:00:00Z", "after_key=" + strings.Repeat("x", 65)} {
		response := timeslotRequest(t, server, http.MethodGet, "/api/v1/tests/"+job.RunID+"/monitor-evidence?"+query, nil)
		if response.Code != 400 {
			t.Fatalf("invalid %s: %d %s", query, response.Code, response.Body.String())
		}
	}
}

func TestRetainedTypedEvidenceScrubbingAndArchiveQueryProjection(t *testing.T) {
	v := 123.0
	e := analysis.ProviderEvidence{ID: "1", Provider: "elasticsearch", Kind: "log", TimestampUTC: time.Now().UTC(), DeepLink: "https://unapproved.example", Summary: "password=hunter2", Properties: map[string]string{"api_key": "secret"}, Log: &analysis.ProviderLog{Message: "token=secret", Attributes: map[string]analysis.ScalarValue{"password": {Kind: "string", StringValue: "hunter2"}, "private_key": {Kind: "number", NumberValue: &v}, "normal": {Kind: "string", StringValue: "retained"}}}}
	safe := scrubRetainedProviderEvidence(e)
	encoded, err := json.Marshal(safe)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "hunter2") || strings.Contains(string(encoded), "unapproved.example") || safe.Log.Attributes["private_key"].NumberValue != nil || safe.Log.Attributes["normal"].StringValue != "retained" {
		t.Fatalf("unsafe projection %s", encoded)
	}
	if e.Log.Attributes["password"].StringValue != "hunter2" {
		t.Fatal("read projection mutated immutable source")
	}
	selection := elastic.Selection{ProjectID: "project", Indices: []string{"logs-*"}, Logs: true, Queries: []elastic.Query{{Name: "counts", Language: "esql", Kind: "metric", ESQL: "FROM logs-* | WHERE user.name == ? | STATS total=COUNT(*)", Params: []any{"private-value"}, Statistic: "sum", Unit: "count"}}}
	metadata := elasticMonitoringMetadata(selection, "elasticsearch")
	body, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "private-value") || strings.Contains(string(body), "FROM") || len(metadata.MappingRevision) != 64 || len(metadata.Queries) != 1 || len(metadata.Queries[0].Revision) != 64 {
		t.Fatalf("unsafe archive metadata %s", body)
	}
	if providerEvidenceVersion(e) == "" || providerEvidenceVersion(analysis.ProviderEvidence{Kind: "event"}) != "" {
		t.Fatal("typed evidence version broke legacy projection")
	}
}

func TestMonitorArchiveCoverageRetainsSafeWatermarks(t *testing.T) {
	at := time.Date(2026, 10, 5, 12, 34, 56, 789, time.UTC)
	coverage := RunMonitorCoverage{SourceID: "elastic-source", QueryWatermark: at, ElasticIngestionWatermark: at.Add(time.Second)}
	body, err := json.Marshal(coverage)
	if err != nil {
		t.Fatal(err)
	}
	var recovered RunMonitorCoverage
	if err = json.Unmarshal(body, &recovered); err != nil || !recovered.QueryWatermark.Equal(at) || !recovered.ElasticIngestionWatermark.Equal(at.Add(time.Second)) {
		t.Fatalf("safe archive watermarks lost %s %v", body, err)
	}
	if strings.Contains(string(body), "pending_ingestion_watermark") || strings.Contains(string(body), "continuation") {
		t.Fatal("private query state disclosed in coverage")
	}
}
