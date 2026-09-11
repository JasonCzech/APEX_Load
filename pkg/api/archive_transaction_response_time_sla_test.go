package api

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func float64Pointer(value float64) *float64 { return &value }

func TestApplyTransactionResponseTimeSLAsRecomputesGroupAwareCompliance(t *testing.T) {
	root := t.TempDir()
	file, err := os.Create(filepath.Join(root, "transactions.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(file)
	events := make([]TransactionEvent, 0, 25)
	for index := 0; index < 20; index++ {
		duration := 3000.0
		if index == 19 {
			duration = 3000.01
		}
		events = append(events, TransactionEvent{Group: "Default", ActionName: "Checkout", BusinessTransaction: true, DurationMs: duration, Success: true})
	}
	events = append(events,
		TransactionEvent{Group: "Default", ActionName: "Checkout", BusinessTransaction: true, DurationMs: 9000, Success: false},
		TransactionEvent{Group: "Default", ActionName: "request", DurationMs: 9000, Success: true},
		TransactionEvent{Group: "Override", ActionName: "Checkout", BusinessTransaction: true, DurationMs: 1001, Success: true},
		TransactionEvent{Group: "Override", ActionName: "Checkout", BusinessTransaction: true, DurationMs: 1002, Success: true},
	)
	for _, event := range events {
		if err := encoder.Encode(event); err != nil {
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	manifest := RunArchiveManifest{
		TransactionsFile: "transactions.jsonl",
		Provenance: RunArchiveProvenance{
			GoalConfig: &GoalConfig{TransactionResponseTimeSLAMs: float64Pointer(3000)},
			Groups: []TestGroup{
				{Name: "Default"},
				{Name: "Override", TransactionResponseTimeSLAMs: float64Pointer(1000)},
				{Name: "Empty"},
			},
		},
	}
	if err := applyTransactionResponseTimeSLAs(root, &manifest); err != nil {
		t.Fatalf("apply response-time SLAs: %v", err)
	}
	if len(manifest.TransactionResponseTimeSLAResults) != 3 {
		t.Fatalf("results = %#v", manifest.TransactionResponseTimeSLAResults)
	}
	byGroup := make(map[string]TransactionResponseTimeSLAResult)
	for _, result := range manifest.TransactionResponseTimeSLAResults {
		byGroup[result.Group] = result
	}
	if got := byGroup["Default"]; got.Status != "pass" || got.EligibleSuccessCount != 20 || got.ExcludedErrorCount != 1 || got.MissedCount != 1 || got.MetPct != 95 {
		t.Errorf("default result = %#v, want exact 95%% pass with error excluded", got)
	}
	if got := byGroup["Override"]; got.Status != "fail" || got.ThresholdMs != 1000 || got.MissedPct != 100 {
		t.Errorf("override result = %#v", got)
	}
	if got := byGroup["Empty"]; got.Status != "not_evaluated" || got.EligibleSuccessCount != 0 {
		t.Errorf("empty result = %#v", got)
	}
	if !manifest.SLABreached || manifest.SLAEvaluated {
		t.Fatalf("verdict evidence: breached=%v evaluated=%v", manifest.SLABreached, manifest.SLAEvaluated)
	}
}

func TestTransactionResponseTimeSLAResultsRenderAcrossReportFormats(t *testing.T) {
	result := buildTransactionResponseTimeSLAResult("Checkout group", "Submit", 800, &transactionResponseTimeSLAAccumulator{total: 10, eligible: 9, excluded: 1, missed: 2})
	report := &RunArchiveReport{Manifest: &RunArchiveManifest{SchemaVersion: archiveSchemaVersion, TestID: "run-sla", TestName: "SLA run"}, TransactionResponseTimeSLAResults: []TransactionResponseTimeSLAResult{result}, ByAction: map[string]RunArchiveActionStats{}}
	html, err := renderRunArchiveHTMLReportWithProfile(report, "engineer")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Transaction response-time SLA", "Checkout group", "Submit", "22.22%", "Required met %"} {
		if !bytes.Contains(html, []byte(want)) {
			t.Errorf("HTML missing %q", want)
		}
	}
	var csv bytes.Buffer
	if err := writeRunArchiveReportCSVWithProfile(report, &csv, "engineer"); err != nil {
		t.Fatal(err)
	}
	if text := csv.String(); !strings.Contains(text, "transaction_response_time_missed_pct") || !strings.Contains(text, "Checkout group / Submit") {
		t.Fatalf("CSV missing response-time SLA evidence: %s", text)
	}
	profile, _ := lookupRunArchiveReportProfile("engineer")
	if text := strings.Join(buildRunArchiveReportPDFLines(report, profile, nil, nil), "\n"); !strings.Contains(text, "Checkout group / Submit") || !strings.Contains(text, "10 total") || !strings.Contains(text, "77.78% met, 95.00% required") {
		t.Fatalf("PDF lines missing response-time SLA evidence: %s", text)
	}
}

func TestTransactionResponseTimeSLADrivesFinalVerdicts(t *testing.T) {
	base := func(result TransactionResponseTimeSLAResult) *RunArchiveReport {
		return &RunArchiveReport{
			Manifest: &RunArchiveManifest{
				TerminationReason:                 "workload_completed",
				SLAEvaluated:                      result.Status != "not_evaluated",
				SLABreached:                       result.Status == "fail",
				TransactionResponseTimeSLAResults: []TransactionResponseTimeSLAResult{result},
				Provenance:                        RunArchiveProvenance{GoalConfig: &GoalConfig{TransactionResponseTimeSLAMs: float64Pointer(800)}},
			},
			ByAction:     map[string]RunArchiveActionStats{},
			Completeness: RunArchiveCompletenessScore{Status: "complete", ScorePct: 100},
		}
	}
	pass := buildTransactionResponseTimeSLAResult("A", "Checkout", 800, &transactionResponseTimeSLAAccumulator{total: 20, eligible: 20, missed: 1})
	fail := buildTransactionResponseTimeSLAResult("A", "Checkout", 800, &transactionResponseTimeSLAAccumulator{total: 20, eligible: 20, missed: 2})
	notEvaluated := buildTransactionResponseTimeSLAResult("A", "", 800, nil)
	for name, test := range map[string]struct {
		result TransactionResponseTimeSLAResult
		want   string
	}{"pass": {pass, reportVerdictPass}, "fail": {fail, reportVerdictFail}, "inconclusive": {notEvaluated, reportVerdictConditional}} {
		t.Run(name, func(t *testing.T) {
			if got := deriveReportVerdict(base(test.result)).Status; got != test.want {
				t.Fatalf("verdict = %q, want %q", got, test.want)
			}
		})
	}
}
