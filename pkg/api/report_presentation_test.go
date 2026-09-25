package api

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestReportHTMLProvidesReviewSummaryAndSectionNavigation(t *testing.T) {
	report := samplePDFRunArchiveReport()
	for _, profileID := range []string{"executive", "engineer", "audit"} {
		t.Run(profileID, func(t *testing.T) {
			html, err := renderRunArchiveHTMLReportWithProfile(report, profileID)
			if err != nil {
				t.Fatal(err)
			}
			body := string(html)
			for _, want := range []string{
				`<main class="content" id="report-main">`,
				`<nav class="report-contents" id="report-contents" aria-label="Report contents">`,
				`<h2 id="review-summary-heading">Review summary</h2>`,
				`<h3>Evidence gaps</h3>`,
			} {
				if !strings.Contains(body, want) {
					t.Errorf("report is missing %q", want)
				}
			}
			profile, _ := lookupRunArchiveReportProfile(profileID)
			for _, section := range reportSectionsForProfile(profile) {
				if !strings.Contains(body, `href="#report-`+section.ID+`"`) || !strings.Contains(body, `id="report-`+section.ID+`"`) {
					t.Errorf("section %q has no linked destination", section.ID)
				}
			}
			if profileID == "audit" && strings.Contains(body, `<b>Archive error rate</b>`) {
				t.Error("audit opening summary disclosed performance metrics")
			}
		})
	}
}

func TestExecutiveOpeningSummaryCarriesCuratedDecisionAcrossFormats(t *testing.T) {
	report := samplePDFRunArchiveReport()
	annotations := defaultReportAnnotations(report, report.Manifest.TestID, "executive")
	annotations.Title = "Checkout release signoff"
	annotations.PreparedBy = "Ana Analyst"
	annotations.SectionComments["decision"] = "Hold release until checkout latency is retested."
	annotations.SectionComments["recommendations"] = "Repeat the steady-state run after the fix."
	html, err := renderRunArchiveHTMLReportWithAnnotations(report, "executive", annotations, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Checkout release signoff", "Ana Analyst", "Decision context:", "Hold release until checkout latency is retested.", "Repeat the steady-state run after the fix."} {
		if !strings.Contains(string(html), want) {
			t.Errorf("HTML opening summary missing %q", want)
		}
	}
	var pdf bytes.Buffer
	if err := writeRunArchiveReportPDFWithProfileAndAnnotations(report, &pdf, "executive", annotations, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pdf.String(), "Checkout release signoff") || !strings.Contains(pdf.String(), "Prepared by: Ana Analyst") || !strings.Contains(pdf.String(), "Decision context: Hold release") || !strings.Contains(pdf.String(), "Recommendation: Repeat") {
		t.Fatal("PDF opening summary omitted curated decision")
	}
	var word bytes.Buffer
	if err := writeRunArchiveReportDOCXWithProfileAndAnnotations(report, &word, "executive", annotations, nil); err != nil {
		t.Fatal(err)
	}
	archive, err := zip.NewReader(bytes.NewReader(word.Bytes()), int64(word.Len()))
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"word/document.xml", "docProps/core.xml"} {
		if !bytes.Contains(readDOCXTestPart(t, archive, part), []byte("Checkout release signoff")) {
			t.Errorf("Word part %s omitted the preview title", part)
		}
	}
	if !bytes.Contains(readDOCXTestPart(t, archive, "word/document.xml"), []byte("Prepared by")) {
		t.Error("Word report omitted the preview preparer")
	}
}

func TestPrintablePerformanceIntervalsChooseUsefulEvidence(t *testing.T) {
	start := time.Date(2026, time.June, 2, 18, 0, 0, 0, time.UTC)
	timeline := []RunArchiveSummaryPoint{
		{Timestamp: start, ActiveVUsers: 1, TPS: 1, P95Ms: 40},
		{Timestamp: start.Add(time.Minute), ActiveVUsers: 3, TPS: 2, P95Ms: 60},
		{Timestamp: start.Add(2 * time.Minute), ActiveVUsers: 8, TPS: 12, P95Ms: 70},
		{Timestamp: start.Add(3 * time.Minute), ActiveVUsers: 8, TPS: 9, P95Ms: 210},
		{Timestamp: start.Add(4 * time.Minute), ActiveVUsers: 2, TPS: 2, P95Ms: 80},
	}
	var lines []string
	appendPerformanceIntervalTable(&lines, timeline)
	if len(lines) != 7 || lines[1] != "|Time (UTC)|VUsers|TPS|p95 ms|Error %|" {
		t.Fatalf("timeline table lines = %#v", lines)
	}
	for _, marker := range []string{"02 Jun 18:00", "02 Jun 18:02", "02 Jun 18:03", "02 Jun 18:04"} {
		if !strings.Contains(strings.Join(lines, "\n"), marker) {
			t.Errorf("timeline table is missing %s", marker)
		}
	}
	lines = nil
	appendPerformanceIntervalTable(&lines, nil)
	if len(lines) != 2 || !strings.Contains(lines[0], "No interval timeline") {
		t.Fatalf("empty timeline lines = %#v", lines)
	}
}

func TestReportTableAndWordKeyValueParsing(t *testing.T) {
	for _, test := range []struct {
		line  string
		cells int
	}{
		{"|Time (UTC)|VUsers|TPS|", 3},
		{"|only one|", 0},
		{"ordinary prose", 0},
	} {
		cells, ok := parseReportTableLine(test.line)
		if ok != (test.cells > 0) || len(cells) != test.cells {
			t.Errorf("parseReportTableLine(%q) = %#v, %v", test.line, cells, ok)
		}
	}
}

func TestReportPDFHasHierarchyBookmarksPageNumbersAndBalancedTail(t *testing.T) {
	var output bytes.Buffer
	if err := writeRunArchiveReportPDFWithProfile(samplePDFRunArchiveReport(), &output, "executive"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Review Summary", "Evidence gaps:", "/Outlines", "/PageMode /UseOutlines", "/Helvetica-Bold", "Page 1 of", "/Info ", "/Title (Checkout Evidence - Executive Summary Report)", `Time \(UTC\)`, "p95 ms"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("PDF missing %q", want)
		}
	}
	lines := make([]pdfReportLine, 53)
	for index := range lines {
		lines[index] = pdfReportLine{Text: "Report evidence line"}
	}
	pages := paginatePDFReportLines(lines)
	if len(pages) != 2 || len(pages[1]) < 12 {
		t.Fatalf("pagination = %d pages with %d lines on last page, want balanced two-page output", len(pages), len(pages[len(pages)-1]))
	}
	paragraph := make([]pdfReportLine, 47)
	for index := range paragraph {
		paragraph[index] = pdfReportLine{Text: "Earlier evidence"}
	}
	paragraph = append(paragraph, pdfReportLine{}, pdfReportLine{Text: "Release Trend", Heading: true}, pdfReportLine{Text: "First sentence"}, pdfReportLine{Text: "Second sentence"}, pdfReportLine{Text: "Final sentence"})
	paragraphPages := paginatePDFReportLines(paragraph)
	if len(paragraphPages) != 2 || !paragraphPages[1][0].Heading {
		t.Fatalf("section was split across pages: %#v", paragraphPages)
	}
}

func TestReportDOCXProvidesSummaryTablesAndRunningFurniture(t *testing.T) {
	var output bytes.Buffer
	if err := writeRunArchiveReportDOCXWithProfileAndAnnotations(samplePDFRunArchiveReport(), &output, "executive", nil, nil); err != nil {
		t.Fatal(err)
	}
	archive, err := zip.NewReader(bytes.NewReader(output.Bytes()), int64(output.Len()))
	if err != nil {
		t.Fatal(err)
	}
	document := readDOCXTestPart(t, archive, "word/document.xml")
	if err := xml.Unmarshal(document, new(any)); err != nil {
		t.Fatalf("invalid Word document XML: %v", err)
	}
	for _, want := range []string{">Review summary<", "<w:tbl>", "<w:tblHeader/>", "Contents", "<w:headerReference", "<w:footerReference"} {
		if !bytes.Contains(document, []byte(want)) {
			t.Errorf("Word document missing %q", want)
		}
	}
	for _, partName := range []string{"word/header1.xml", "word/footer1.xml"} {
		part := readDOCXTestPart(t, archive, partName)
		if err := xml.Unmarshal(part, new(any)); err != nil {
			t.Errorf("invalid %s: %v", partName, err)
		}
	}
}

func TestReportChartsAppearInPDFAndWordOnlyForChartProfiles(t *testing.T) {
	report := samplePDFRunArchiveReport()
	first := report.SummaryTimeline[0]
	second := first
	second.Timestamp = second.Timestamp.Add(time.Minute)
	second.TPS, second.P95Ms, second.ErrorPct, second.ActiveVUsers = 18, 350, 7.5, 12
	report.SummaryTimeline = append(report.SummaryTimeline, second)
	charts, err := buildReportDocumentCharts(report)
	if err != nil {
		t.Fatal(err)
	}
	if len(charts) != 5 {
		t.Fatalf("chart count = %d, want throughput, p95, errors, VUsers, action latency", len(charts))
	}
	specs := reportDocumentChartSpecs(report)
	if specs[4].HighlightAt != report.Overview.AvgResponseMs {
		t.Errorf("action chart threshold = %.1f, want run average %.1f", specs[4].HighlightAt, report.Overview.AvgResponseMs)
	}
	if !strings.Contains(string(buildReportCharts(report).ActionLatency), "Amber = at or above run average") {
		t.Error("HTML action chart does not explain the preview's highlighted bars")
	}
	html, err := renderRunArchiveHTMLReportWithProfile(report, "engineer")
	if err != nil {
		t.Fatal(err)
	}
	if got := bytes.Count(html, []byte(`<svg viewBox=`)); got != len(charts) {
		t.Errorf("HTML chart count = %d, want %d", got, len(charts))
	}
	if !bytes.Contains(html, []byte("--blue:   #0f766e")) {
		t.Error("downloaded HTML does not use the preview's teal palette")
	}
	for _, chart := range charts {
		if chart.Width != reportChartWidth*2 || chart.Height < 360 || len(chart.PNG) == 0 || len(chart.RGB) == 0 {
			t.Errorf("chart %q has invalid image dimensions or payload", chart.ID)
		}
		if _, err := png.Decode(bytes.NewReader(chart.PNG)); err != nil {
			t.Errorf("chart %q is not a valid PNG: %v", chart.ID, err)
		}
	}
	var pdf bytes.Buffer
	if err := writeRunArchiveReportPDFWithProfile(report, &pdf, "engineer"); err != nil {
		t.Fatal(err)
	}
	if got := bytes.Count(pdf.Bytes(), []byte("/Subtype /Image")); got != len(charts) {
		t.Errorf("PDF image count = %d, want %d", got, len(charts))
	}
	if !bytes.Contains(pdf.Bytes(), []byte("/Im1 Do")) || !bytes.Contains(pdf.Bytes(), []byte("/Im5 Do")) {
		t.Error("PDF pages do not place the report charts")
	}
	var document bytes.Buffer
	if err := writeRunArchiveReportDOCXWithProfileAndAnnotations(report, &document, "engineer", nil, nil); err != nil {
		t.Fatal(err)
	}
	archive, err := zip.NewReader(bytes.NewReader(document.Bytes()), int64(document.Len()))
	if err != nil {
		t.Fatal(err)
	}
	contents := readDOCXTestPart(t, archive, "word/document.xml")
	relations := readDOCXTestPart(t, archive, "word/_rels/document.xml.rels")
	for index, chart := range charts {
		name := "word/media/chart-" + strconv.Itoa(index+1) + ".png"
		imageBytes := readDOCXTestPart(t, archive, name)
		if _, err := png.Decode(bytes.NewReader(imageBytes)); err != nil {
			t.Errorf("%s is invalid: %v", name, err)
		}
		if !bytes.Contains(contents, []byte(chart.Title)) || !bytes.Contains(relations, []byte(name[5:])) {
			t.Errorf("Word document does not reference chart %q", chart.Title)
		}
	}
	var auditPDF, auditWord bytes.Buffer
	if err := writeRunArchiveReportPDFWithProfile(report, &auditPDF, "audit"); err != nil {
		t.Fatal(err)
	}
	if err := writeRunArchiveReportDOCXWithProfileAndAnnotations(report, &auditWord, "audit", nil, nil); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(auditPDF.Bytes(), []byte("/Subtype /Image")) {
		t.Error("audit PDF contains profile-excluded charts")
	}
	if bytes.Contains(auditWord.Bytes(), []byte("word/media/chart-")) {
		t.Error("audit Word package contains profile-excluded charts")
	}
	if out := os.Getenv("APEX_REPORT_VISUAL_OUT"); out != "" {
		if err := os.MkdirAll(out, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, data := range map[string][]byte{"technical-with-charts.pdf": pdf.Bytes(), "technical-with-charts.docx": document.Bytes(), "technical-with-charts.html": html} {
			if err := os.WriteFile(filepath.Join(out, name), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
}
