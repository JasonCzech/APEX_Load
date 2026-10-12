package api

import (
	"bytes"
	"context"
	"encoding/csv"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/livetable"
)

func TestLiveTableDownloadableReportsContainMetadataWithoutRows(t *testing.T) {
	runner, _, test := newLiveRuntimeFixture(t, "memory", "write_back")
	liveIntegrationPop(t, runner, test)
	if err := runner.finalizeLiveTables(context.Background(), test, "FAILED"); err != nil {
		t.Fatal(err)
	}
	report := samplePDFRunArchiveReport()
	report.Manifest.Provenance.LiveTables = runner.liveTableArchiveSummaries(test.ID)
	for _, profile := range []string{"engineer", "audit"} {
		t.Run(profile, func(t *testing.T) {
			html, err := renderRunArchiveHTMLReportWithProfile(report, profile)
			if err != nil {
				t.Fatal(err)
			}
			var pdf, csvBody bytes.Buffer
			if err := writeRunArchiveReportPDFWithProfile(report, &pdf, profile); err != nil {
				t.Fatal(err)
			}
			if err := writeRunArchiveReportCSVWithProfile(report, &csvBody, profile); err != nil {
				t.Fatal(err)
			}
			for name, body := range map[string]string{"html": string(html), "pdf": pdf.String(), "csv": csvBody.String()} {
				for _, want := range []string{"native_queue", "write_back", "run_not_complete"} {
					if !strings.Contains(body, want) {
						t.Errorf("%s missing %q", name, want)
					}
				}
				for _, secret := range []string{"private-customer", "historical", "lease_token", "wrapped_key", "seed_metadata"} {
					if strings.Contains(body, secret) {
						t.Errorf("%s disclosed runtime material %q", name, secret)
					}
				}
			}
			rows, err := csv.NewReader(bytes.NewReader(csvBody.Bytes())).ReadAll()
			if err != nil {
				t.Fatal(err)
			}
			metrics := map[string]string{}
			for _, row := range rows {
				if len(row) == 4 && row[0] == "live_table" {
					metrics[row[2]] = row[3]
				}
			}
			for metric, want := range map[string]string{"seeded": "2", "available": "1", "popped": "1", "op_count": "1", "op_errors": "0", "finalization_reason": "run_not_complete"} {
				if metrics[metric] != want {
					t.Errorf("CSV %s=%q want=%q", metric, metrics[metric], want)
				}
			}
			for name, body := range map[string]string{"html": string(html), "pdf": pdf.String(), "csv": csvBody.String()} {
				if !strings.Contains(body, "reset after controller adoption") {
					t.Errorf("%s operation telemetry omitted its recovery limitation", name)
				}
			}
		})
	}
}

func TestLiveTableReportMetadataEscapesUntrustedArchiveText(t *testing.T) {
	report := samplePDFRunArchiveReport()
	report.Manifest.Provenance.LiveTables = []RunArchiveLiveTableSummary{{TableID: "table", Alias: "=formula<script>alert(1)</script>", Profile: "native_queue"}}
	html, err := renderRunArchiveHTMLReportWithProfile(report, "audit")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(html), "<script>alert(1)</script>") || !strings.Contains(string(html), "&lt;script&gt;") {
		t.Fatal("HTML did not escape archived alias")
	}
	var output bytes.Buffer
	if err := writeRunArchiveReportCSVWithProfile(report, &output, "audit"); err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(&output).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if len(row) == 4 && row[0] == "live_table" && row[1] != "'=formula<script>alert(1)</script>" {
			t.Fatalf("CSV alias not escaped: %q", row[1])
		}
	}
}

func TestLiveTableReportsRetainBoundedObservationEvidence(t *testing.T) {
	report := samplePDFRunArchiveReport()
	at := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	report.Manifest.Provenance.LiveTables = []RunArchiveLiveTableSummary{{TableID: "work", Alias: "work", Profile: "native_queue", Counts: livetable.Counts{Redeliveries: 3}, Observations: &livetable.Observations{Version: 1, CapturedAt: at, WaitCount: 7, WaitTotalNS: 23000000, EmptyOutcomes: 2, Throttled: 4, UnknownOutcomes: 1, UnknownPopAttempts: 1, RecoveryGaps: 1, Incomplete: true, Events: []livetable.ObservationEvent{{Kind: "recovery_observation_gap", First: at, Last: at.Add(time.Second), Count: 1}}}}}
	html, err := renderRunArchiveHTMLReportWithProfile(report, "audit")
	if err != nil {
		t.Fatal(err)
	}
	var pdf, csvBody bytes.Buffer
	if err := writeRunArchiveReportPDFWithProfile(report, &pdf, "audit"); err != nil {
		t.Fatal(err)
	}
	if err := writeRunArchiveReportCSVWithProfile(report, &csvBody, "audit"); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"HTML": string(html), "PDF": pdf.String(), "CSV": csvBody.String()} {
		if !strings.Contains(body, "recovery_observation_gap") {
			t.Errorf("%s missing retained event", name)
		}
	}
	rows, err := csv.NewReader(bytes.NewReader(csvBody.Bytes())).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	metrics := map[string]string{}
	for _, row := range rows {
		if len(row) == 4 && row[0] == "live_table" {
			metrics[row[2]] = row[3]
		}
	}
	for name, want := range map[string]string{"wait_count": "7", "wait_total_ns": "23000000", "capacity_rejections": "4", "unknown_pop_attempts": "1", "redeliveries": "3", "observation_coverage_incomplete": "true"} {
		if metrics[name] != want {
			t.Errorf("%s=%q want %q", name, metrics[name], want)
		}
	}
	// Export ordering must remain stable for the same retained evidence.
	var second bytes.Buffer
	if err := writeRunArchiveReportCSVWithProfile(report, &second, "audit"); err != nil {
		t.Fatal(err)
	}
	if csvBody.String() != second.String() {
		t.Fatal("retained CSV is nondeterministic")
	}
}

func TestLiveTableReportBoundsUntrustedObservationEvents(t *testing.T) {
	report := samplePDFRunArchiveReport()
	at := time.Now().UTC()
	observed := &livetable.Observations{Version: 1}
	for i := 0; i < 100; i++ {
		observed.Events = append(observed.Events, livetable.ObservationEvent{Kind: "empty", First: at, Last: at, Count: 1})
	}
	observed.Events[0].Kind = "private-unrecognized-event"
	report.Manifest.Provenance.LiveTables = []RunArchiveLiveTableSummary{{Alias: "bounded", Observations: observed}}
	bounded := boundedLiveTableObservations(observed)
	if len(bounded.Events) != 15 || bounded.DroppedEvents != 85 || !bounded.Incomplete || len(observed.Events) != 100 {
		t.Fatalf("invalid event normalization: %+v", bounded)
	}
	html, err := renderRunArchiveHTMLReportWithProfile(report, "audit")
	if err != nil {
		t.Fatal(err)
	}
	var pdf, csvBody bytes.Buffer
	if err := writeRunArchiveReportPDFWithProfile(report, &pdf, "audit"); err != nil {
		t.Fatal(err)
	}
	if err := writeRunArchiveReportCSVWithProfile(report, &csvBody, "audit"); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"HTML": string(html), "PDF": pdf.String(), "CSV": csvBody.String()} {
		if strings.Contains(body, "private-unrecognized-event") || strings.Count(body, "occurrences") > 16 {
			t.Errorf("%s unbounded or unvalidated events", name)
		}
	}
	if strings.Contains(csvBody.String(), "shared_data_event_16") {
		t.Fatal("CSV ignored event bound")
	}
}
