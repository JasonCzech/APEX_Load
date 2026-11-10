package api

import (
	"archive/zip"
	"bytes"
	"fmt"
	stdhtml "html"
	"math"
	"strings"
	"testing"
	"unicode/utf8"
)

// timeAnatomyExportReport is the consistency-matrix run with a time anatomy:
// Checkout states its tail attribution, Login is too thin to, and seven
// more transactions overflow the printed few. A row's P95 total is below
// the sum of its phases' P95s, as percentiles are, and the overflow
// transactions' names share their first 48 runes, so their chart titles
// collide.
const browsePrefix = "Browse the whole catalogue page by page, slowly, item "

func timeAnatomyExportReport() *RunArchiveReport {
	report := consistencyMatrixReport()
	p95 := func(v float64) RunArchivePhaseStat { return RunArchivePhaseStat{Samples: 400, P95Ms: v, AvgMs: v / 2} }
	row := func(action string, connect, server, download float64) RunArchiveAnatomyRequest {
		return RunArchiveAnatomyRequest{Key: "1|" + action + "#1", ActionName: action, Occurrence: 1, Samples: 400, Phases: RunArchiveAnatomyPhaseStats{
			Connect: p95(connect), Server: p95(server), Download: p95(download), Total: p95(math.Round(0.95 * (connect + server + download)))}}
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
		anatomy.Transactions = append(anatomy.Transactions, RunArchiveTransactionAnatomy{Name: fmt.Sprintf("%s%d", browsePrefix, i), Instances: 50, Duration: p95(float64(100 + i)), Requests: rows,
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
	if len(first.Rows) != 2 || pay.Label != "POST /pay" || pay.TotalMs != 629 || first.Chart.Stacks[1].Total != 629 || pay.Phases[1] != 40 || pay.Phases[3] != 610 || len(first.Chart.Stacks) != 2 ||
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
		!strings.Contains(page, "P95 tail is +420 ms vs median") || !strings.Contains(page, "3 more transactions are in the JSON report") ||
		!strings.Contains(page, `data-chart-id="time-anatomy-1"`) || !strings.Contains(page, "Each bar stacks the phases' own P95 values") {
		t.Fatal("HTML is missing the time anatomy section")
	}
	// The bar is labelled with the request's own P95, not the sum of its
	// phases' P95s (662 ms).
	if !strings.Contains(page, ">629 ms</text>") || strings.Contains(page, ">662 ms</text>") {
		t.Fatal("the bar label is not the request's P95")
	}

	profile, _ := lookupRunArchiveReportProfile("engineer")
	lines := buildRunArchiveReportPDFLines(report, profile, nil, manifest)
	text := strings.Join(lines, "\n")
	if !strings.Contains(text, reportDocumentChartMarker+reportTimeAnatomyChartPrefix+"1") || !strings.Contains(text, "|POST /pay|0|40|0|610|12|0|629|") ||
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
	// Every anatomy chart is placed, each once, even where the truncated
	// titles of two transactions collide.
	titles := map[string]int{}
	for index, chart := range charts {
		if !strings.HasPrefix(chart.ID, reportTimeAnatomyChartPrefix) {
			continue
		}
		titles[chart.Title]++
		if embed := fmt.Sprintf(`r:embed="rId%d"`, index+1+3); strings.Count(document, embed) != 1 {
			t.Fatalf("%s (%s) placed %d times", chart.ID, embed, strings.Count(document, embed))
		}
	}
	collided := false
	for _, n := range titles {
		collided = collided || n > 1
	}
	if !collided {
		t.Fatal("fixture: no two anatomy chart titles collide")
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

// A sub-100 ms total reads the same on the bar and in the table, and a
// zero total is labelled zero, not the segment sum; multibyte names are cut
// on rune boundaries.
func TestTimeAnatomyExportLabelsAndMultibyteNames(t *testing.T) {
	stat := func(v float64) RunArchivePhaseStat { return RunArchivePhaseStat{Samples: 100, P95Ms: v} }
	name := strings.Repeat("支払い処理", 12) // 60 three-byte runes
	action := strings.Repeat("Überweisung→", 4)
	report := consistencyMatrixReport()
	report.TimeAnatomy = &RunArchiveTimeAnatomy{Schema: timeAnatomySchema, Transactions: []RunArchiveTransactionAnatomy{{
		Name: name, Instances: 100, Duration: stat(60),
		Requests: []RunArchiveAnatomyRequest{
			{Key: "1|a#1", ActionName: action, Occurrence: 1, Samples: 100, Phases: RunArchiveAnatomyPhaseStats{Server: stat(30), Download: stat(14.6), Total: stat(42.3)}},
			{Key: "2|b#1", ActionName: "GET /zero", Occurrence: 1, Samples: 100, Phases: RunArchiveAnatomyPhaseStats{Server: stat(3)}},
		},
	}}}
	view := buildReportTimeAnatomyView(report, reportTimeAnatomyStatistic)
	chart := view.Entries[0].Chart
	if !utf8.ValidString(chart.Title) || !strings.HasPrefix(chart.Title, string([]rune(name)[:47])+"…: ") {
		t.Fatalf("%q", chart.Title)
	}
	svg := string(svgStackedBarChart(chart))
	if !utf8.ValidString(svg) || !strings.Contains(svg, ">42.3 ms</text>") || !strings.Contains(svg, ">0.0 ms</text>") || strings.Contains(svg, ">3 ms</text>") {
		t.Fatal(svg)
	}
	if label := truncateLabel(action, 34); !utf8.ValidString(label) || utf8.RuneCountInString(label) != 34 {
		t.Fatalf("%q", label)
	}

	html, err := renderRunArchiveHTMLReportWithAnnotations(report, "engineer", nil, consistencyMatrixManifest())
	if err != nil {
		t.Fatal(err)
	}
	if page := string(html); !utf8.ValidString(page) || !strings.Contains(page, `<td class="num">42.3</td></tr>`) {
		t.Fatal("HTML table total does not match the bar label")
	}
	profile, _ := lookupRunArchiveReportProfile("engineer")
	text := strings.Join(buildRunArchiveReportPDFLines(report, profile, nil, consistencyMatrixManifest()), "\n")
	if !strings.Contains(text, "|30|14.6|0|42.3|") {
		t.Fatal(text)
	}
	if _, err := buildReportDocumentChartsForProfile(report, profile); err != nil {
		t.Fatal(err)
	}
}
