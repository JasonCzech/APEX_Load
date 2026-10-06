package api

import (
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/elastic"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestElasticFinalHealthContextPreservesObservationAndApprovedQueries(t *testing.T) {
	end := time.Now().UTC().Add(-2 * time.Minute)
	asOf := end.Add(70 * time.Second)
	selection := elastic.Selection{ClusterHealth: true}
	var query string
	for _, definition := range elastic.QueryDefinitions(selection) {
		if definition.Kind == "health" {
			query = definition.QueryID
		}
	}
	if query == "" {
		t.Fatal("health query unavailable")
	}
	job := storage.RunMonitorJob{State: "reconciling", LoadEndedAt: &end, Source: storage.MonitorSource{Elastic: &selection}, Coverage: map[string]analysis.ProviderCoverage{
		query:   {QueryID: query, Status: "complete", AsOf: &asOf},
		"other": {QueryID: "other", Status: "complete"},
	}}
	coverage := elasticHealthCoverage(job)
	if len(coverage) != 1 || coverage[query].AsOf == nil || !coverage[query].AsOf.Equal(asOf) {
		t.Fatalf("health observation changed: %+v", coverage)
	}
	delete(coverage, query)
	if _, exists := job.Coverage[query]; !exists {
		t.Fatal("private health context mutated durable coverage")
	}
	if got := elasticFinalHealthAfter(job); !got.Equal(end.Add(time.Minute)) {
		t.Fatalf("settlement cutoff %v", got)
	}
	selection.SettleSeconds = 90
	if got := elasticFinalHealthAfter(job); !got.Equal(end.Add(90 * time.Second)) {
		t.Fatalf("configured settlement cutoff %v", got)
	}
	job.State = "collecting"
	if !elasticFinalHealthAfter(job).IsZero() {
		t.Fatal("live observations were frozen")
	}
	job.Source.Elastic = nil
	if elasticHealthCoverage(job) != nil || !elasticFinalHealthAfter(job).IsZero() {
		t.Fatal("non-Elastic source received health context")
	}
}

func TestElasticTerminalWindowsUseFrozenEffectiveOperations(t *testing.T) {
	start := time.Date(2026, 10, 5, 0, 0, 0, 123456000, time.UTC)
	end := start.Add(time.Minute)
	offset := 40
	rule := analysis.MonitorRule{SourceID: "source", Metric: "p95", Unit: "ms", Statistic: "avg", EvaluationOperation: "value", StartOffsetSeconds: 10, EndOffsetSeconds: &offset}
	job := storage.RunMonitorJob{State: "reconciling", StartedAt: start, LoadEndedAt: &end, Source: storage.MonitorSource{ID: "source"}, RulesFrozen: true, FrozenMonitorRules: []analysis.MonitorRule{rule, rule}}
	// No mutable test store is needed when immutable job rules are available.
	windows := (&Server{}).elasticTerminalWindows(job)
	if len(windows) != 1 || windows[0].Metric != "p95" || !windows[0].FromUTC.Equal(start.Add(10*time.Second)) || !windows[0].UntilUTC.Equal(start.Add(40*time.Second)) {
		t.Fatalf("frozen effective rule lost: %+v", windows)
	}
	job.FrozenMonitorRules[0].EvaluationOperation = "avg"
	job.FrozenMonitorRules = job.FrozenMonitorRules[:1]
	if windows = (&Server{}).elasticTerminalWindows(job); len(windows) != 0 {
		t.Fatalf("non-value effective rule received exact terminal task: %+v", windows)
	}
}
func TestElasticAdvancedDocumentReadinessRequiresPITAndStableSort(t *testing.T) {
	for _, kind := range []string{"log", "error", "span", "transaction"} {
		t.Run(kind, func(t *testing.T) {
			selection := elastic.Selection{Indices: []string{"logs-*"}, Queries: []elastic.Query{{Name: "documents", Kind: kind, Language: "dsl"}}}
			caps := elastic.Capabilities{Product: elastic.ProductOpenSearch, DSL: true}
			if validatePreparedElasticCapabilities(selection, caps) == nil {
				t.Fatal("advanced document query bypassed PIT readiness")
			}
			caps.PIT = true
			if validatePreparedElasticCapabilities(selection, caps) == nil {
				t.Fatal("older OpenSearch advanced query bypassed stable sort readiness")
			}
			selection.UniqueSortField = "unique_id"
			if err := validatePreparedElasticCapabilities(selection, caps); err != nil {
				t.Fatal(err)
			}
			caps.ShardDoc = true
			selection.UniqueSortField = ""
			if err := validatePreparedElasticCapabilities(selection, caps); err != nil {
				t.Fatal(err)
			}
		})
	}
}
