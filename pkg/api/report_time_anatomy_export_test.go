package api

import (
	"archive/zip"
	"bytes"
	"fmt"
	stdhtml "html"
	"strings"
	"testing"
)

// timeAnatomyExportReport is the consistency-matrix run with a time anatomy:
// Checkout states its tail attribution, Login is too thin to, and seven
// more transactions overflow the printed few.
func timeAnatomyExportReport() *RunArchiveReport {
	report := consistencyMatrixReport()
	p95 := func(v float64) RunArchivePhaseStat { return RunArchivePhaseStat{Samples: 400, P95Ms: v, AvgMs: v / 2} }
	row := func(action string, connect, server, download float64) RunArchiveAnatomyRequest {
		return RunArchiveAnatomyRequest{Key: "1|" + action + "#1", ActionName: action, Occurrence: 1, Samples: 400, Phases: RunArchiveAnatomyPhaseStats{
			Connect: p95(connect), Server: p95(server), Download: p95(download), Total: p95(connect + server + download)}}
	}
	checkout := RunArchiveTransactionAnatomy{Name: "Checkout|Pay", Instances: 400, Duration: p95(980),
		Requests: []RunArchiveAnatomyRequest{row("GET /cart", 12, 210, 30), row("POST /pay", 40, 610, 12)},
		Tail: &RunArchiveTailAttribution{Status: "ok", Instances: 400, Delta: RunArchiveTailDelta{TotalMs: 420},
			Headline: "P95 tail is +420 ms vs median: server +360 ms, connect +45 ms (samples above P95 990 ms average 1,020 ms; samples at or below P50 average 600 ms)"}}
	login := RunArchiveTransactionAnatomy{Name: "Login", Instances: 12, Duration: p95(300), Requests: []RunArchiveAnatomyRequest{row("POST /login", 5, 200, 5)},
		Tail: &RunArchiveTailAttribution{Status: "insufficient_samples", Instances: 12}}
	anatomy := &RunArchiveTimeAnatomy{Schema: timeAnatomySchema, Transactions: []RunArchiveTransactionAnatomy{login, checkout},
		Coverage: RunArchiveTimeAnatomyCoverage{TransactionRows: 412, JoinedTransactionRows: 400}}
	for i := 0; i < 7; i++ {
		var rows []RunArchiveAnatomyRequest
		for j := 0; j < reportTimeAnatomyRows+2; j++ {
			rows = append(rows, row(fmt.Sprintf("GET /item/%d", j), 1, float64(10+j), 1))
		}
		anatomy.Transactions = append(anatomy.Transactions, RunArchiveTransactionAnatomy{Name: fmt.Sprintf("Browse %d", i), Instances: 50, Duration: p95(float64(100 + i)), Requests: rows,
			Tail: &RunArchiveTailAttribution{Status: "no_tail", Instances: 50}})
	}
	report.TimeAnatomy = anatomy
	return report
}

func TestTimeAnatomyExportView(t *testing.T) {
	view := buildReportTimeAnatomyView(timeAnatomyExportReport(), reportTimeAnatomyStatistic)
	if view == nil || len(view.Entries) != reportTimeAnatomyTransactions || view.Omitted != 3 || view.StatisticLabel() != "P95" ||
		!strings.Contains(view.CoverageNote, "400 of 412 transaction rows (97%)") {
		t.Fatalf("%+v", view)
	}
	// The stated tail attribution ranks first; the rest by their P95.
	first, second := view.Entries[0], view.Entries[1]
	if first.Name != "Checkout|Pay" || !first.TailOK || !strings.HasPrefix(first.Tail, "P95 tail is +420 ms vs median: server +360 ms") ||
		second.Name != "Login" || second.TailOK || !strings.Contains(second.Tail, "at least 40 instances") {
		t.Fatalf("%+v / %+v", first, second)
	}
	pay := first.Rows[1]
	if len(first.Rows) != 2 || pay.Label != "POST /pay" || pay.TotalMs != 662 || pay.Phases[1] != 40 || pay.Phases[3] != 610 || len(first.Chart.Stacks) != 2 ||
		first.Chart.ID != reportTimeAnatomyChartPrefix+"1" || strings.Contains(first.Chart.Title, "|") {
		t.Fatalf("%+v", first)
	}
	// A transaction keeps its slowest rows, in request order.
	browse := view.Entries[2]
	if len(browse.Rows) != reportTimeAnatomyRows || browse.MoreRows != 2 || browse.Rows[0].Label != "GET /item/2" || browse.Rows[len(browse.Rows)-1].Label != "GET /item/9" {
		t.Fatalf("%+v", browse.Rows)
	}
	if buildReportTimeAnatomyView(consistencyMatrixReport(), reportTimeAnatomyStatistic) != nil {
		t.Fatal("a run without an anatomy has a view")
	}
}

func TestTimeAnatomyExportFormats(t *testing.T) {
	report := timeAnatomyExportReport()
	manifest := consistencyMatrixManifest()

	html, err := renderRunArchiveHTMLReportWithAnnotations(report, "engineer", nil, manifest)
	if err != nil {
		t.Fatal(err)
	}
	page := stdhtml.UnescapeString(string(html))
	if !strings.Contains(page, `data-report-section="time_anatomy"`) || !strings.Contains(page, `aria-label="Checkout/Pay: request phases at P95 (ms)"`) ||
		!strings.Contains(page, "P95 tail is +420 ms vs median") || !strings.Contains(page, "3 more transactions are in the JSON report") {
		t.Fatal("HTML is missing the time anatomy section")
	}

	profile, _ := lookupRunArchiveReportProfile("engineer")
	lines := buildRunArchiveReportPDFLines(report, profile, nil, manifest)
	text := strings.Join(lines, "\n")
	if !strings.Contains(text, reportDocumentChartMarker+reportTimeAnatomyChartPrefix+"1") || !strings.Contains(text, "|POST /pay|0|40|0|610|12|0|662|") ||
		!strings.Contains(text, "Checkout/Pay: 400 instances, P95 980 ms") {
		t.Fatal(text)
	}
	charts, err := buildReportDocumentChartsForProfile(report, profile)
	if err != nil {
		t.Fatal(err)
	}
	anatomyCharts := 0
	for _, chart := range charts {
		if strings.HasPrefix(chart.ID, reportTimeAnatomyChartPrefix) {
			anatomyCharts++
			if len(chart.PNG) == 0 || chart.Height != reportStackedChartHeight(2)*2 && chart.Height != reportStackedChartHeight(1)*2 && chart.Height != reportStackedChartHeight(reportTimeAnatomyRows)*2 {
				t.Fatalf("%s: %d", chart.ID, chart.Height)
			}
		}
	}
	if anatomyCharts != reportTimeAnatomyTransactions {
		t.Fatalf("%d anatomy charts", anatomyCharts)
	}
	var pdf bytes.Buffer
	if err := writeRunArchiveReportPDFWithProfileAndAnnotations(report, &pdf, "engineer", nil, manifest); err != nil || !bytes.HasPrefix(pdf.Bytes(), []byte("%PDF")) {
		t.Fatalf("pdf: %v", err)
	}

	var docx bytes.Buffer
	if err := writeRunArchiveReportDOCXWithProfileAndAnnotations(report, &docx, "engineer", nil, manifest); err != nil {
		t.Fatal(err)
	}
	archive, err := zip.NewReader(bytes.NewReader(docx.Bytes()), int64(docx.Len()))
	if err != nil {
		t.Fatal(err)
	}
	document := string(readDOCXTestPart(t, archive, "word/document.xml"))
	if !strings.Contains(document, "Time anatomy") || !strings.Contains(document, "POST /pay") {
		t.Fatal("DOCX is missing the time anatomy section")
	}
	images := 0
	for _, file := range archive.File {
		if strings.HasPrefix(file.Name, "word/media/chart-") {
			images++
		}
	}
	if images != len(charts) {
		t.Fatalf("%d chart images for %d charts", images, len(charts))
	}

	var csv bytes.Buffer
	if err := writeRunArchiveReportCSVWithProfileAndAnnotations(report, &csv, "engineer", nil, manifest); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(csv.String(), "time_anatomy,Checkout|Pay,tail_delta_ms,420") && !strings.Contains(csv.String(), `time_anatomy,"Checkout|Pay",tail_delta_ms,420`) {
		t.Fatal(csv.String())
	}
}

func TestTimeAnatomyExportRespectsTheProfile(t *testing.T) {
	report := timeAnatomyExportReport()
	manifest := consistencyMatrixManifest()
	for _, id := range []string{"executive", "audit"} {
		profile, _ := lookupRunArchiveReportProfile(id)
		html, err := renderRunArchiveHTMLReportWithAnnotations(report, id, nil, manifest)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(html), `data-report-section="time_anatomy"`) {
			t.Fatalf("%s rendered the time anatomy", id)
		}
		charts, err := buildReportDocumentChartsForProfile(report, profile)
		if err != nil {
			t.Fatal(err)
		}
		for _, chart := range charts {
			if strings.HasPrefix(chart.ID, reportTimeAnatomyChartPrefix) {
				t.Fatalf("%s drew %s", id, chart.ID)
			}
		}
	}
	// A run without an anatomy names the section as not captured.
	profile, _ := lookupRunArchiveReportProfile("engineer")
	plain := consistencyMatrixReport()
	data := buildRunArchiveReportRenderData(plain, profile, nil, manifest)
	if _, notCaptured := reportApplicableSections(profile, &data); !strings.Contains(strings.Join(notCaptured, ","), "Time anatomy") {
		t.Fatalf("%v", notCaptured)
	}
}
