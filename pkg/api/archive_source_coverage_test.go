package api

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func coverageArchiveClient(t *testing.T, id string) *archiveStreamingFakeClient {
	t.Helper()
	root := t.TempDir()
	now := time.Now().UTC()
	manifest := RunArchiveManifest{SchemaVersion: archiveSchemaVersion, TestID: id, TestName: "Coverage", CreatedAt: now, CompletedAt: now,
		TransactionsFile: "transactions.jsonl", ResourceSamplesFile: "resources.jsonl", IntervalSummariesFile: "interval_summaries.jsonl",
		TransactionCount: 1, ResourceSampleCount: 1, IntervalSummaryCount: 1,
		// Generator-supplied coverage must never replace the controller's snapshot.
		SourceCoverage: &RunArchiveSourceCoverage{Mode: "required", Sources: []RunArchiveSource{{Target: "forged", Collected: true}}}}
	values := map[string]any{"manifest.json": manifest, "transactions.jsonl": TransactionEvent{Timestamp: now, TestID: id, ActionName: "GET", Success: true, DurationMs: 10},
		"resources.jsonl": ResourceSample{Timestamp: now, TestID: id, ActiveVUsers: 1}, "interval_summaries.jsonl": IntervalSummaryRecord{Timestamp: now, TestID: id, TotalTx: 1, ActiveVUsers: 1, TPS: 1, Telemetry: &TelemetryHealth{}}}
	client := &archiveStreamingFakeClient{artifacts: make(map[string]string)}
	for name, value := range values {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
		client.artifacts[name] = path
	}
	return client
}

func TestArchiveSourceCoveragePersistsMissingAndRetiredSources(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	const id = "partial-source-coverage"
	client := coverageArchiveClient(t, id)
	run := &ManagedTest{ID: id, Name: "Coverage", Status: "FAILED", CreatedAt: time.Now().UTC(),
		remoteClientsByTarget:        map[string]generatorClient{"healthy": client, "missing": &startupLifecycleFakeClient{}},
		GeneratorShards:              []GeneratorShardSummary{{Group: "checkout", Target: "orphaned-assignment", VUsers: 1}},
		remoteArchiveClientsByTarget: map[string]generatorClient{"retired": client, "no-transport": nil}}
	runner := NewTestRunner(1, nil, nil)
	runner.tests[id] = run
	backend := newDistributedExecutionBackend([]string{"healthy", "missing"}, time.Second, nil)
	record, err := backend.stageRemoteArchive(runner, run, "FAILED", "", time.Now().UTC(), false)
	if err != nil || record == nil {
		t.Fatalf("stage: %+v %v", record, err)
	}
	// Read the persisted manifest and report, not the in-memory merge result.
	report, err := buildRunArchiveReport(record)
	if err != nil {
		t.Fatal(err)
	}
	coverage := report.Manifest.SourceCoverage
	if coverage == nil || coverage.Mode != "best_effort" || len(coverage.Sources) != 5 {
		t.Fatalf("coverage: %+v", coverage)
	}
	got := map[string]bool{}
	for _, source := range coverage.Sources {
		got[source.Target] = source.Collected
	}
	if len(got) != 5 || !got["healthy"] || !got["retired"] || got["missing"] || got["no-transport"] || got["orphaned-assignment"] {
		t.Fatalf("sources: %+v", got)
	}
	if report.Overview.Transactions != 2 || report.Manifest.Status != "FAILED" {
		t.Fatalf("lost surviving evidence or outcome: %+v", report.Overview)
	}
	if report.Completeness.Status != "incomplete" {
		t.Fatalf("partial report declared complete: %+v", report.Completeness)
	}
	var csv bytes.Buffer
	if err := writeRunArchiveReportCSV(report, &csv); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(csv.String(), "3 of 5 known generator archive sources were not collected") {
		t.Fatal("CSV omitted source loss")
	}
	html, err := renderRunArchiveHTMLReport(report)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(html, []byte("3 of 5 known generator archive sources were not collected")) {
		t.Fatal("HTML omitted source loss")
	}
	if !strings.Contains(csv.String(), "archive_source,missing,collected,false") || !bytes.Contains(html, []byte("<td>missing</td><td>Not collected</td>")) {
		t.Fatal("exports omitted the missing source identity")
	}
	profile, _ := lookupRunArchiveReportProfile("engineer")
	lines := strings.Join(buildRunArchiveReportPDFLines(report, profile, nil, nil), "\n")
	if !strings.Contains(lines, "missing: not collected") || !strings.Contains(lines, "3 of 5 known generator archive sources") {
		t.Fatal("PDF omitted source coverage")
	}
	clone, err := cloneRunArchiveManifest(report.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	clone.SourceCoverage.Sources[0].Target = "changed"
	if report.Manifest.SourceCoverage.Sources[0].Target == "changed" {
		t.Fatal("manifest clone aliases source coverage")
	}
}

func TestArchiveSourceCoverageCollectionModes(t *testing.T) {
	for _, tc := range []struct {
		name                          string
		required, missing, allMissing bool
		wantError                     bool
	}{
		{"required complete", true, false, false, false}, {"required missing", true, true, false, true},
		{"best effort missing", false, true, false, false}, {"best effort no bundles", false, true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := "coverage-modes"
			client := coverageArchiveClient(t, id)
			clients := map[string]generatorClient{"a": client, "b": client}
			if tc.missing {
				clients["b"] = &startupLifecycleFakeClient{}
			}
			if tc.allMissing {
				clients["a"] = nil
			}
			runner := NewTestRunner(1, nil, nil)
			run := &ManagedTest{ID: id, remoteClientsByTarget: clients}
			runner.tests[id] = run
			backend := newDistributedExecutionBackend([]string{"b", "a"}, time.Second, nil)
			bundles, coverage, err := backend.fetchRemoteArchiveBundlesWithCoverage(context.Background(), runner, run, tc.required)
			defer cleanupRemoteArchiveBundles(bundles)
			if (err != nil) != tc.wantError {
				t.Fatalf("err=%v", err)
			}
			if tc.wantError {
				if len(bundles) != 0 || coverage != nil {
					t.Fatal("required collection accepted partial evidence")
				}
				return
			}
			if coverage == nil || len(coverage.Sources) != 2 || coverage.Sources[0].Target != "b" || coverage.Sources[1].Target != "a" {
				t.Fatalf("coverage: %+v", coverage)
			}
			expected := 2
			if tc.missing {
				expected--
			}
			if tc.allMissing {
				expected--
			}
			if len(bundles) != expected {
				t.Fatalf("bundles=%d want=%d", len(bundles), expected)
			}
			if tc.required && coverage.Mode != "required" {
				t.Fatal("required mode lost")
			}
		})
	}
}

func TestArchiveSourceCoverageCompletenessAndAcceptance(t *testing.T) {
	for _, tc := range []struct {
		name     string
		coverage *RunArchiveSourceCoverage
		legacy   bool
		want     string
	}{
		{"legacy unspecified", nil, false, "complete"}, {"legacy incomplete", nil, true, "needs_review"},
		{"all required", &RunArchiveSourceCoverage{Mode: "required", Sources: []RunArchiveSource{{Target: "a", Collected: true}}}, false, "complete"},
		{"all best effort", &RunArchiveSourceCoverage{Mode: "best_effort", Sources: []RunArchiveSource{{Target: "a", Collected: true}}}, false, "needs_review"},
		{"missing", &RunArchiveSourceCoverage{Mode: "best_effort", Sources: []RunArchiveSource{{Target: "a", Collected: false}}}, false, "incomplete"},
		{"empty", &RunArchiveSourceCoverage{Mode: "required"}, false, "incomplete"},
		{"invalid mode", &RunArchiveSourceCoverage{Mode: "invented", Sources: []RunArchiveSource{{Target: "a", Collected: true}}}, false, "incomplete"},
		{"duplicate", &RunArchiveSourceCoverage{Mode: "required", Sources: []RunArchiveSource{{Target: "a", Collected: true}, {Target: " a ", Collected: true}}}, false, "incomplete"},
		{"blank target", &RunArchiveSourceCoverage{Mode: "required", Sources: []RunArchiveSource{{Collected: true}}}, false, "incomplete"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifest := &RunArchiveManifest{SourceCoverage: tc.coverage, TerminationReason: "workload_completed", SLAEvaluated: true}
			if tc.legacy {
				manifest.CSVExportIncomplete = "private diagnostic must not be echoed"
			}
			manifest.Provenance.GoalConfig = &GoalConfig{P95LatencyMs: ptr(100.0)}
			report := &RunArchiveReport{Manifest: manifest, Overview: RunArchiveOverview{Transactions: 2, Telemetry: &RunArchiveTelemetrySummary{Samples: 1}}, ByAction: map[string]RunArchiveActionStats{"GET": {Transactions: 2, P95Ms: 50}}}
			report.Completeness = buildRunArchiveCompletenessScoreFromCounts(manifest, report, 1, 1, 1)
			if report.Completeness.Status != tc.want {
				t.Fatalf("completeness: %+v", report.Completeness)
			}
			verdict := deriveReportVerdict(report)
			if tc.want == "complete" && verdict.Status != reportVerdictPass {
				t.Fatalf("healthy control verdict: %+v", verdict)
			}
			if tc.want != "complete" && verdict.Status == reportVerdictPass {
				t.Fatal("incomplete source coverage accepted as pass")
			}
			for _, f := range report.Completeness.Findings {
				if strings.Contains(f.Message, "private diagnostic") {
					t.Fatal("raw incomplete reason exposed")
				}
			}
		})
	}
}
