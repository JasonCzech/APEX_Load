package api

import (
	"bytes"
	"context"
	"encoding/csv"
	"strings"
	"testing"
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
