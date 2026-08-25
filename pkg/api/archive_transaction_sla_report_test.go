package api

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestTransactionSLARuleResultsRenderAcrossReportFormats(t *testing.T) {
	observed := 912.0
	report := &RunArchiveReport{
		SchemaVersion: runArchiveReportSchemaVersion,
		Manifest: &RunArchiveManifest{
			SchemaVersion: archiveSchemaVersion, TestID: "run-rules", TestName: "Rules",
			TransactionRuleResults: []TransactionSLARuleResult{{
				Rule:   TransactionSLARule{Transaction: "Checkout", Metric: "p95_duration_ms", Comparator: "lte", Threshold: 800, Severity: "fail"},
				Status: "fail", Observed: &observed, MatchedCount: 42, Message: "breached",
			}},
		},
		ByAction: make(map[string]RunArchiveActionStats),
	}

	html, err := renderRunArchiveHTMLReportWithProfile(report, "engineer")
	if err != nil {
		t.Fatalf("html: %v", err)
	}
	for _, fragment := range []string{"Exact transaction terminal rules", "Checkout", "p95_duration_ms", "912"} {
		if !bytes.Contains(html, []byte(fragment)) {
			t.Errorf("HTML missing %q", fragment)
		}
	}

	var csv bytes.Buffer
	if err := writeRunArchiveReportCSVWithProfile(report, &csv, "engineer"); err != nil {
		t.Fatalf("csv: %v", err)
	}
	if text := csv.String(); !strings.Contains(text, "transaction_rule_status") || !strings.Contains(text, "Checkout") {
		t.Fatalf("CSV missing rule evidence: %s", text)
	}

	var docx bytes.Buffer
	if err := writeRunArchiveReportDOCXWithProfileAndAnnotations(report, &docx, "engineer", nil, nil); err != nil {
		t.Fatalf("docx: %v", err)
	}
	archive, err := zip.NewReader(bytes.NewReader(docx.Bytes()), int64(docx.Len()))
	if err != nil {
		t.Fatalf("open docx: %v", err)
	}
	documentXML := ""
	for _, file := range archive.File {
		if file.Name != "word/document.xml" {
			continue
		}
		reader, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		_ = reader.Close()
		if err != nil {
			t.Fatal(err)
		}
		documentXML = string(data)
	}
	if !strings.Contains(documentXML, "Exact transaction terminal rules") || !strings.Contains(documentXML, "Checkout") {
		t.Fatalf("DOCX document missing rule evidence: %s", documentXML)
	}
}
