package main

import (
	"encoding/xml"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/api"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestBuildJUnitReportNoSLA(t *testing.T) {
	report := buildJUnitReport(junitInput{
		suiteName:    "apexload:order-flow",
		duration:     2 * time.Second,
		transactions: 100,
		errors:       0,
	})

	if len(report.Suites) != 1 {
		t.Fatalf("expected 1 suite, got %d", len(report.Suites))
	}
	suite := report.Suites[0]
	if suite.Tests != 2 {
		t.Fatalf("expected 2 testcases, got %d", suite.Tests)
	}
	if suite.Failures != 0 || suite.Errors != 0 {
		t.Fatalf("expected no failures/errors, got failures=%d errors=%d", suite.Failures, suite.Errors)
	}
	if suite.Skipped != 1 {
		t.Fatalf("expected sla testcase skipped, got skipped=%d", suite.Skipped)
	}
	if report.Failures != 0 || report.Errors != 0 {
		t.Fatalf("aggregate counts wrong: %+v", report)
	}
}

func TestBuildJUnitReportSLAPass(t *testing.T) {
	report := buildJUnitReport(junitInput{
		suiteName: "apexload:x",
		sla:       &SLASummary{Evaluated: true, Breached: false},
	})
	suite := report.Suites[0]
	if suite.Failures != 0 {
		t.Fatalf("expected no failures, got %d", suite.Failures)
	}
	if suite.Skipped != 0 {
		t.Fatalf("expected no skips when SLA evaluated, got %d", suite.Skipped)
	}
	// The sla testcase should exist and be passing (no failure/skip).
	var found bool
	for _, c := range suite.Cases {
		if c.Name == "sla" {
			found = true
			if c.Failure != nil || c.Skipped != nil {
				t.Fatalf("expected passing sla testcase, got %+v", c)
			}
		}
	}
	if !found {
		t.Fatalf("sla testcase missing")
	}
}

func TestBuildJUnitReportSLABreach(t *testing.T) {
	report := buildJUnitReport(junitInput{
		suiteName: "apexload:x",
		sla: &SLASummary{
			Evaluated: true,
			Breached:  true,
			Details:   []string{"P95 latency 900.0ms exceeded 800.0ms", "Error rate 5.00% exceeded 2.00%"},
		},
	})
	suite := report.Suites[0]
	if suite.Failures != 1 {
		t.Fatalf("expected 1 failure, got %d", suite.Failures)
	}
	if report.Failures != 1 {
		t.Fatalf("expected aggregate failures=1, got %d", report.Failures)
	}
	var slaCase *junitTestcase
	for i := range suite.Cases {
		if suite.Cases[i].Name == "sla" {
			slaCase = &suite.Cases[i]
		}
	}
	if slaCase == nil || slaCase.Failure == nil {
		t.Fatalf("expected sla failure, got %+v", slaCase)
	}
	if !strings.Contains(slaCase.Failure.Body, "Error rate") {
		t.Fatalf("expected breach details in body, got %q", slaCase.Failure.Body)
	}
}

func TestBuildJUnitReportPreservesStructuredTransactionRuleEvidence(t *testing.T) {
	observed := 912.0
	report := buildJUnitReport(junitInput{sla: &SLASummary{
		Evaluated: true,
		TransactionRuleResults: []api.TransactionSLARuleResult{{
			Rule:   api.TransactionSLARule{Transaction: "Checkout", Metric: "p95_duration_ms", Comparator: "lte", Threshold: 800, Severity: "warning"},
			Status: "warning", Observed: &observed, MatchedCount: 42, Message: "breached",
		}},
	}})
	properties := report.Suites[0].Properties
	if got := properties[len(properties)-1]; got.Name != "transaction_sla_rule.0" || !strings.Contains(got.Value, `"transaction":"Checkout"`) || !strings.Contains(got.Value, `"status":"warning"`) {
		t.Fatalf("transaction rule property = %#v", got)
	}
}

func TestBuildJUnitReportPartialEvidenceBreachStillFails(t *testing.T) {
	report := buildJUnitReport(junitInput{
		suiteName: "apexload:partial",
		sla: &SLASummary{
			Evaluated: false,
			Breached:  true,
			Details:   []string{"P95 latency 900.0ms exceeded 800.0ms"},
		},
	})
	suite := report.Suites[0]
	if suite.Failures != 1 || suite.Skipped != 0 {
		t.Fatalf("partial-evidence breach counts = failures:%d skipped:%d, want 1/0", suite.Failures, suite.Skipped)
	}
	if suite.Cases[1].Failure == nil || !strings.Contains(suite.Cases[1].Failure.Body, "P95 latency") {
		t.Fatalf("partial-evidence breach was not preserved: %#v", suite.Cases[1])
	}
}

func TestBuildJUnitReportExecutionError(t *testing.T) {
	report := buildJUnitReport(junitInput{
		suiteName: "apexload:x",
		runErr:    errors.New("connection refused to controller"),
	})
	suite := report.Suites[0]
	if suite.Errors != 1 {
		t.Fatalf("expected 1 error, got %d", suite.Errors)
	}
	if report.Errors != 1 {
		t.Fatalf("expected aggregate errors=1, got %d", report.Errors)
	}
}

func TestMarshalJUnitValidXML(t *testing.T) {
	report := buildJUnitReport(junitInput{
		suiteName: "apexload:weird & <name>",
		sla:       &SLASummary{Evaluated: true, Breached: true, Details: []string{"bad < & > chars"}},
	})
	body, err := marshalJUnit(report)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.HasPrefix(string(body), xml.Header) {
		t.Fatalf("expected xml header prefix")
	}
	// Must round-trip through a strict XML parser (proves escaping is correct).
	var rt junitTestsuites
	if err := xml.Unmarshal(body, &rt); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if rt.Suites[0].Name != "apexload:weird & <name>" {
		t.Fatalf("name not preserved through escaping: %q", rt.Suites[0].Name)
	}
}

func TestWriteJUnitReport(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "results.xml")
	err := writeJUnitReport(path, junitInput{
		suiteName: "apexload:x",
		sla:       &SLASummary{Evaluated: true, Breached: false},
	})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var rt junitTestsuites
	if err := xml.Unmarshal(data, &rt); err != nil {
		t.Fatalf("file not valid junit: %v", err)
	}
}

func TestJUnitSuiteName(t *testing.T) {
	cases := []struct {
		name   string
		args   runArgs
		script *dsl.Script
		want   string
	}{
		{"script name wins", runArgs{script: "flows/order.json"}, &dsl.Script{Name: "Order Flow"}, "apexload:Order Flow"},
		{"falls back to file base", runArgs{script: "flows/order.json"}, &dsl.Script{}, "apexload:order"},
		{"empty everything", runArgs{}, nil, "apexload"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := junitSuiteName(tc.args, tc.script); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestTruncateMessage(t *testing.T) {
	long := strings.Repeat("a", 500)
	got := truncateMessage(long)
	if len([]rune(got)) > 240 {
		t.Fatalf("expected truncation to <=240 runes, got %d", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("expected ellipsis suffix")
	}
}

func TestBuildJUnitReportTelemetryAcceptanceCase(t *testing.T) {
	tests := []struct {
		name                    string
		verdict                 *CITelemetryVerdict
		failure, error, skipped bool
	}{
		{name: "enforced failure", verdict: &CITelemetryVerdict{Outcome: "fail", Mode: "fail_ci", Enforced: true, Reasons: []string{"coverage 70.0% is below the 90.0% floor"}}, failure: true},
		{name: "unmapped inconclusive", verdict: &CITelemetryVerdict{Outcome: "inconclusive", Mode: "fail_ci", Enforced: true}, error: true},
		{name: "inconclusive mapped to fail", verdict: &CITelemetryVerdict{Outcome: "inconclusive", Mode: "fail_ci", InconclusivePolicy: "fail", Enforced: true}, failure: true},
		{name: "inconclusive mapped to pass", verdict: &CITelemetryVerdict{Outcome: "inconclusive", Mode: "fail_ci", InconclusivePolicy: "pass"}},
		{name: "advisory failure is skipped", verdict: &CITelemetryVerdict{Outcome: "fail", Mode: "advisory"}, skipped: true},
		{name: "pass", verdict: &CITelemetryVerdict{Outcome: "pass", Mode: "fail_ci"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := buildJUnitReport(junitInput{suiteName: "apexload:ci", telemetry: test.verdict})
			suite := report.Suites[0]
			var testcase *junitTestcase
			for index := range suite.Cases {
				if suite.Cases[index].Name == "telemetry_acceptance" {
					testcase = &suite.Cases[index]
				}
			}
			if testcase == nil {
				t.Fatalf("no telemetry_acceptance test case: %+v", suite.Cases)
			}
			if (testcase.Failure != nil) != test.failure || (testcase.Error != nil) != test.error || (testcase.Skipped != nil) != test.skipped {
				t.Fatalf("test case = %+v", testcase)
			}
			if test.failure && !strings.Contains(testcase.Failure.Message, "telemetry") {
				t.Fatalf("failure message = %q", testcase.Failure.Message)
			}
		})
	}
	// Without a verdict there is no telemetry test case.
	report := buildJUnitReport(junitInput{suiteName: "apexload:ci"})
	for _, testcase := range report.Suites[0].Cases {
		if testcase.Name == "telemetry_acceptance" {
			t.Fatal("a run without a telemetry verdict has a telemetry test case")
		}
	}
}
