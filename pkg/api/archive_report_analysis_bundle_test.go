package api

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"strings"
	"testing"
)

func TestRunArchiveAnalysisBundleProvidesRowBasedProfileTables(t *testing.T) {
	report := samplePDFRunArchiveReport()
	for _, profile := range []string{"executive", "engineer", "audit"} {
		t.Run(profile, func(t *testing.T) {
			var output bytes.Buffer
			if err := writeRunArchiveAnalysisBundle(report, &output, profile, nil, nil); err != nil {
				t.Fatal(err)
			}
			archive, err := zip.NewReader(bytes.NewReader(output.Bytes()), int64(output.Len()))
			if err != nil {
				t.Fatal(err)
			}
			summary, err := csv.NewReader(bytes.NewReader(readDOCXTestPart(t, archive, "run_summary.csv"))).ReadAll()
			if err != nil {
				t.Fatal(err)
			}
			if len(summary) != 2 || summary[1][0] != report.Manifest.TestID || summary[1][1] != profile {
				t.Fatalf("run summary rows = %#v", summary)
			}
			if profile == "audit" {
				if strings.Contains(strings.Join(summary[0], ","), "peak_successful_transaction_tps") {
					t.Fatal("audit table disclosed performance measures")
				}
				if _, err := zipPart(archive, "artifacts.csv"); err != nil {
					t.Fatal(err)
				}
				if _, err := zipPart(archive, "transaction_sla.csv"); err == nil {
					t.Fatal("audit bundle included technical SLA table")
				}
			} else {
				if _, err := zipPart(archive, "transaction_sla.csv"); err != nil {
					t.Fatal(err)
				}
				if _, err := zipPart(archive, "findings.csv"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := zipPart(archive, "README.txt"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRunArchiveAnalysisBundleRejectsIncompleteReportAndUnknownProfile(t *testing.T) {
	var output bytes.Buffer
	if err := writeRunArchiveAnalysisBundle(nil, &output, "executive", nil, nil); err == nil {
		t.Fatal("nil report was accepted")
	}
	if err := writeRunArchiveAnalysisBundle(samplePDFRunArchiveReport(), &output, "unknown", nil, nil); err == nil || !strings.Contains(err.Error(), "unknown report profile") {
		t.Fatalf("unknown profile error = %v", err)
	}
}
