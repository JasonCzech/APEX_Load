package api

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/elastic"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestElasticObservedTraceCorrelationIsBoundedWithoutLegacyCapture(t *testing.T) {
	artifacts := &runArtifacts{test: &ManagedTest{ID: "run"}, elasticTraceEnabled: true}
	sink := elkTraceEvidenceSink{artifacts: artifacts}
	for i := range 65 {
		_ = sink.RecordResult(&protocol.Result{CorrelationID: fmt.Sprintf("00-%032x-0000000000000001-01", i+1)})
	}
	server := &Server{runner: &TestRunner{tests: map[string]*ManagedTest{"run": {elkArtifacts: artifacts}}}}
	ids, limited := server.elasticObservedTraceIDs("run")
	if len(ids) != 64 || !limited {
		t.Fatalf("ids=%d limited=%v", len(ids), limited)
	}
	if ids[0] != fmt.Sprintf("%032x", 1) {
		t.Fatal("unstable identifier projection")
	}
	ids[0] = "changed"
	next, _ := server.elasticObservedTraceIDs("run")
	if next[0] == "changed" {
		t.Fatal("caller mutated retained set")
	}
	if ids, limited = server.elasticObservedTraceIDs("missing"); len(ids) != 0 || limited {
		t.Fatal("missing run fabricated correlations")
	}
}

func TestElasticValueRuleRequiresExactAuthoredESQLWindow(t *testing.T) {
	source := storage.MonitorSource{ID: "s", Enabled: true, Pack: "elastic", Elastic: &elastic.Selection{Indices: []string{"logs-*"}, Queries: []elastic.Query{{Name: "latency", Kind: "metric", Language: "esql", ESQL: "FROM logs-* | STATS p99 = PERCENTILE(duration, 99)", Statistic: "p99", Unit: "ms", ValueColumn: "p99"}}}}
	bindings := &storage.MonitorBindings{Sources: []storage.MonitorBinding{{SourceID: "s"}}}
	goals := &GoalConfig{MonitorRules: []analysis.MonitorRule{{SourceID: "s", Metric: "latency", Unit: "ms", Statistic: "avg", EvaluationOperation: "value", Comparator: "lte", Threshold: 100}}}
	if (&TestRunner{}).validateSelectedMonitorRules(bindings, []storage.MonitorSource{source}, goals) == nil {
		t.Fatal("bucketed query accepted for final scalar rule")
	}
	source.Elastic.Queries[0].ExactWindow = true
	if err := (&TestRunner{}).validateSelectedMonitorRules(bindings, []storage.MonitorSource{source}, goals); err != nil {
		t.Fatal(err)
	}
}

func TestElasticTraceObservationUsesFrozenJobsAfterDraftEditsAndDisable(t *testing.T) {
	server, _, job, _, _ := apiElasticMonitorFixture(t)
	test := &ManagedTest{ID: job.RunID, ProjectID: job.ProjectID, MonitorBindings: &storage.MonitorBindings{Sources: []storage.MonitorBinding{}}}
	t.Setenv("APEXLOAD_RUN_MONITOR_PROJECTS", "")
	if !server.runner.testUsesElasticTraceEvidence(test) {
		t.Fatal("draft binding edit or feature disable changed frozen trace observation")
	}
	artifacts := &runArtifacts{test: test, elasticTraceEnabled: server.runner.testUsesElasticTraceEvidence(test)}
	if err := (elkTraceEvidenceSink{artifacts: artifacts}).RecordResult(&protocol.Result{CorrelationID: "00-00000000000000000000000000000001-0000000000000001-01"}); err != nil {
		t.Fatal(err)
	}
	if len(artifacts.elkTraceIDs) != 1 {
		t.Fatal("frozen source stopped collecting observed correlations")
	}
}

func TestElasticTraceObservationPreservesReadyPreparationIncludingOffBindings(t *testing.T) {
	server, store, job, _, _ := apiElasticMonitorFixture(t)
	source := job.Source
	selection := *source.Elastic
	selection.RunHeader = true
	selection.HeaderOrigins = []string{"https://shop.example.com"}
	source.Elastic = &selection
	if _, err := store.SaveMonitorSource(source, source.Revision); err != nil {
		t.Fatal(err)
	}
	preparations := server.store.(storage.PreparationStore)
	for _, enabled := range []bool{false, true} {
		id := fmt.Sprintf("elastic-prepared-%t", enabled)
		bindings := &storage.MonitorBindings{Sources: []storage.MonitorBinding{}}
		if enabled {
			bindings.Sources = append(bindings.Sources, storage.MonitorBinding{SourceID: job.Source.ID})
		}
		record := &storage.TestRecord{ID: id, ProjectID: job.ProjectID, Name: "prepared", Status: "PENDING", MonitorBindings: bindings}
		if err := server.store.SaveTest(record); err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		preparation, err := preparations.CreatePreparation(storage.Preparation{ID: id + "-preparation", TestID: id, ProjectID: job.ProjectID, CreatedAt: now, ExpiresAt: now.Add(time.Hour), ExecutionRevision: strings.Repeat("a", 64), IdempotencyKey: "trace"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = preparations.TransitionPreparation(preparation.ID, "preparing", "ready", preparation.Revision, "", nil); err != nil {
			t.Fatal(err)
		}
		// Reverse the mutable draft. The prepared source snapshot still wins.
		draft := &storage.MonitorBindings{Sources: []storage.MonitorBinding{}}
		if !enabled {
			draft.Sources = append(draft.Sources, storage.MonitorBinding{SourceID: job.Source.ID})
		}
		record.MonitorBindings = draft
		if err := server.store.SaveTest(record); err != nil {
			t.Fatal(err)
		}
		test := &ManagedTest{ID: id, ProjectID: job.ProjectID, MonitorBindings: draft}
		if got := server.runner.testUsesElasticTraceEvidence(test); got != enabled {
			t.Fatalf("ready preparation trace selection enabled=%t got=%t", enabled, got)
		}
		origins := server.runner.runMonitorCorrelationOrigins(id)
		if !enabled && len(origins) != 0 || enabled && (len(origins) != 1 || origins[0] != "https://shop.example.com") {
			t.Fatalf("prepared origins enabled=%t got=%v", enabled, origins)
		}
	}
}
