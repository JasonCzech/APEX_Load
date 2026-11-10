package api

import (
	"cmp"
	"fmt"
	"html/template"
	"image"
	"image/color"
	"math"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/image/font"
)

// The Time Anatomy export section: per business transaction, its request
// rows split by phase at one statistic (P95: exports have no statistic
// picker), the report's tail attribution sentence and a stacked bar per
// request row where the format can draw one. It is bounded so a run with
// many transactions still prints compactly.
const (
	reportTimeAnatomyStatistic    = "p95"
	reportTimeAnatomyTransactions = 6
	reportTimeAnatomyRows         = 8
	reportTimeAnatomyChartPrefix  = "time-anatomy-"
)

// reportAnatomyPhase is one drawn phase: its label, its statistic on a row
// and its chart colour (the dashboard's waterfall palette).
type reportAnatomyPhase struct {
	Key   string
	Label string
	Color string
	RGBA  color.RGBA
	stat  func(RunArchiveAnatomyPhaseStats) RunArchivePhaseStat
}

var reportAnatomyPhases = []reportAnatomyPhase{
	{"dns", "DNS", "#0d9488", color.RGBA{13, 148, 136, 255}, func(p RunArchiveAnatomyPhaseStats) RunArchivePhaseStat { return p.DNS }},
	{"connect", "Connect", "#f59e0b", color.RGBA{245, 158, 11, 255}, func(p RunArchiveAnatomyPhaseStats) RunArchivePhaseStat { return p.Connect }},
	{"tls", "TLS", "#8b5cf6", color.RGBA{139, 92, 246, 255}, func(p RunArchiveAnatomyPhaseStats) RunArchivePhaseStat { return p.TLS }},
	{"server", "Server", "#10b981", color.RGBA{16, 185, 129, 255}, func(p RunArchiveAnatomyPhaseStats) RunArchivePhaseStat { return p.Server }},
	{"download", "Download", "#0ea5e9", color.RGBA{14, 165, 233, 255}, func(p RunArchiveAnatomyPhaseStats) RunArchivePhaseStat { return p.Download }},
	{"unsplit", "No TTFB", "#94a3b8", color.RGBA{148, 163, 184, 255}, func(p RunArchiveAnatomyPhaseStats) RunArchivePhaseStat { return p.Unsplit }},
}

// reportAnatomyStat reads one statistic of a phase.
func reportAnatomyStat(stat RunArchivePhaseStat, statistic string) float64 {
	var v float64
	switch statistic {
	case "avg":
		v = stat.AvgMs
	case "p50":
		v = stat.P50Ms
	case "p90":
		v = stat.P90Ms
	case "p99":
		v = stat.P99Ms
	default:
		v = stat.P95Ms
	}
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return 0
	}
	return v
}

// reportTimeAnatomyView is what every format renders.
type reportTimeAnatomyView struct {
	Statistic      string
	Entries        []reportTimeAnatomyEntry
	Omitted        int
	Partial        bool
	PartialReasons string
	CoverageNote   string
}

type reportTimeAnatomyEntry struct {
	Name       string
	Instances  int64
	DurationMs float64
	Tail       string
	TailOK     bool
	Rows       []reportTimeAnatomyRow
	MoreRows   int
	Chart      reportDocumentChartSpec
}

type reportTimeAnatomyRow struct {
	Label   string
	Phases  []float64 // one per reportAnatomyPhases
	TotalMs float64
}

// Cells are the row's phase values formatted for a table.
func (r reportTimeAnatomyRow) Cells() []string {
	cells := make([]string, len(r.Phases))
	for i, v := range r.Phases {
		cells[i] = formatChartValue(math.Round(v*10) / 10)
	}
	return cells
}

// Phases are the drawn phases, for a table header and legend.
func (v *reportTimeAnatomyView) Phases() []reportAnatomyPhase { return reportAnatomyPhases }

// StatisticLabel is the statistic as a column heading.
func (v *reportTimeAnatomyView) StatisticLabel() string {
	return strings.ToUpper(v.Statistic[:1]) + v.Statistic[1:]
}

// Chart is an entry's stacked bar chart as inline SVG.
func (e reportTimeAnatomyEntry) ChartSVG() template.HTML { return svgStackedBarChart(e.Chart) }

// reportTimeAnatomyTailReasons explain a tail attribution the report could
// not state.
var reportTimeAnatomyTailReasons = map[string]string{
	"insufficient_samples": "Tail attribution needs at least 40 instances, with 3 joined above P95 and 10 at or below P50.",
	"no_tail":              "No slow tail to attribute: P95 does not exceed P50.",
	"low_join_coverage":    "Tail attribution withheld: under 80% of the tail or median instances were joined to their requests.",
}

// buildReportTimeAnatomyView ranks the transactions (stated tail
// attributions by their tail delta first, then the rest by their P95) and
// keeps the first few, each with its slowest request rows.
func buildReportTimeAnatomyView(report *RunArchiveReport, statistic string) *reportTimeAnatomyView {
	if report == nil || report.TimeAnatomy == nil || len(report.TimeAnatomy.Transactions) == 0 {
		return nil
	}
	anatomy := report.TimeAnatomy
	view := &reportTimeAnatomyView{Statistic: statistic, Partial: anatomy.Truncation.Truncated, PartialReasons: strings.ReplaceAll(strings.Join(anatomy.Truncation.Reasons, ", "), "_", " ")}
	if rows := anatomy.Coverage.TransactionRows; rows > 0 {
		view.CoverageNote = fmt.Sprintf("%s of %s transaction rows (%.0f%%) were joined to their requests.", formatPDFInt(anatomy.Coverage.JoinedTransactionRows), formatPDFInt(rows), float64(anatomy.Coverage.JoinedTransactionRows)/float64(rows)*100)
	}
	transactions := slices.Clone(anatomy.Transactions)
	stated := func(tx RunArchiveTransactionAnatomy) bool { return tx.Tail != nil && tx.Tail.Status == "ok" }
	slices.SortStableFunc(transactions, func(a, b RunArchiveTransactionAnatomy) int {
		switch sa, sb := stated(a), stated(b); {
		case sa && !sb:
			return -1
		case sb && !sa:
			return 1
		case sa && sb:
			return cmp.Or(cmp.Compare(b.Tail.Delta.TotalMs, a.Tail.Delta.TotalMs), strings.Compare(a.Name, b.Name))
		}
		return cmp.Or(cmp.Compare(reportAnatomyStat(b.Duration, statistic), reportAnatomyStat(a.Duration, statistic)), strings.Compare(a.Name, b.Name))
	})
	if len(transactions) > reportTimeAnatomyTransactions {
		view.Omitted = len(transactions) - reportTimeAnatomyTransactions
		transactions = transactions[:reportTimeAnatomyTransactions]
	}
	for index, tx := range transactions {
		entry := reportTimeAnatomyEntry{Name: tx.Name, Instances: tx.Instances, DurationMs: reportAnatomyStat(tx.Duration, statistic)}
		switch {
		case stated(tx) && tx.Tail.Headline != "":
			entry.Tail, entry.TailOK = tx.Tail.Headline+".", true
		case tx.Tail != nil:
			entry.Tail = reportTimeAnatomyTailReasons[tx.Tail.Status]
		}
		rows := slices.Clone(tx.Requests)
		if tx.OtherRequests != nil {
			rows = append(rows, *tx.OtherRequests)
		}
		// The slowest rows by the statistic, kept in request order.
		order := make([]int, len(rows))
		for i := range order {
			order[i] = i
		}
		slices.SortStableFunc(order, func(a, b int) int {
			return cmp.Compare(reportAnatomyStat(rows[b].Phases.Total, statistic), reportAnatomyStat(rows[a].Phases.Total, statistic))
		})
		if len(order) > reportTimeAnatomyRows {
			entry.MoreRows = len(order) - reportTimeAnatomyRows
			order = order[:reportTimeAnatomyRows]
		}
		slices.Sort(order)
		entry.Chart = reportDocumentChartSpec{ID: reportTimeAnatomyChartPrefix + strconv.Itoa(index+1), Unit: "ms", Height: 0}
		entry.Chart.Title = truncateLabel(reportPDFCell(tx.Name), 48) + ": request phases at " + view.StatisticLabel() + " (ms)"
		for _, i := range order {
			row := rows[i]
			label := row.ActionName
			if tx.OtherRequests != nil && i == len(rows)-1 {
				label = "Other requests"
			} else if row.Occurrence > 1 {
				label += " #" + strconv.Itoa(row.Occurrence)
			}
			out := reportTimeAnatomyRow{Label: label, TotalMs: reportAnatomyStat(row.Phases.Total, statistic)}
			for _, phase := range reportAnatomyPhases {
				out.Phases = append(out.Phases, reportAnatomyStat(phase.stat(row.Phases), statistic))
			}
			entry.Rows = append(entry.Rows, out)
			entry.Chart.Stacks = append(entry.Chart.Stacks, reportChartStack{Label: label, Segments: out.Phases})
		}
		entry.Chart.Height = reportStackedChartHeight(len(entry.Chart.Stacks))
		view.Entries = append(view.Entries, entry)
	}
	return view
}

// TimeAnatomyView is the section's view for the templates.
func (d *runArchiveHTMLReportData) TimeAnatomyView() *reportTimeAnatomyView {
	return buildReportTimeAnatomyView(d.Report, reportTimeAnatomyStatistic)
}

// reportTimeAnatomyChartSpecs are the section's drawable charts: one per
// transaction with at least one request row.
func reportTimeAnatomyChartSpecs(report *RunArchiveReport) []reportDocumentChartSpec {
	view := buildReportTimeAnatomyView(report, reportTimeAnatomyStatistic)
	if view == nil {
		return nil
	}
	var specs []reportDocumentChartSpec
	for _, entry := range view.Entries {
		if len(entry.Chart.Stacks) > 0 {
			specs = append(specs, entry.Chart)
		}
	}
	return specs
}

// reportPDFCell keeps a value from splitting a PDF table row.
func reportPDFCell(value string) string {
	return strings.ReplaceAll(strings.TrimSpace(value), "|", "/")
}

func timeAnatomyPDF(d *runArchiveHTMLReportData, lines *[]string) {
	pdfHeading(lines, reportSectionTitle("time_anatomy"))
	view := d.TimeAnatomyView()
	if view == nil {
		*lines = append(*lines, "No business-transaction time anatomy was captured for this run.", "")
		return
	}
	*lines = append(*lines, "Where each business transaction's time goes: its slowest request rows split by phase at "+view.StatisticLabel()+", and what its slow tail spends extra time on.")
	if view.CoverageNote != "" {
		*lines = append(*lines, view.CoverageNote)
	}
	if view.Partial {
		*lines = append(*lines, "Partial anatomy: the bounded join left some rows out ("+view.PartialReasons+").")
	}
	*lines = append(*lines, "")
	header := "|Request|"
	for _, phase := range reportAnatomyPhases {
		header += phase.Label + "|"
	}
	header += "Total|"
	for _, entry := range view.Entries {
		*lines = append(*lines, fmt.Sprintf("%s: %s instances, %s %s ms", reportPDFCell(entry.Name), formatPDFInt(entry.Instances), view.StatisticLabel(), formatChartValue(math.Round(entry.DurationMs))))
		if entry.Tail != "" {
			*lines = append(*lines, entry.Tail)
		}
		if len(entry.Rows) > 0 {
			*lines = append(*lines, reportDocumentChartMarker+entry.Chart.ID, header)
			for _, row := range entry.Rows {
				*lines = append(*lines, "|"+reportPDFCell(row.Label)+"|"+strings.Join(row.Cells(), "|")+"|"+formatChartValue(math.Round(row.TotalMs))+"|")
			}
		}
		if entry.MoreRows > 0 {
			*lines = append(*lines, fmt.Sprintf("%d more request rows in the JSON report.", entry.MoreRows))
		}
		*lines = append(*lines, "")
	}
	if view.Omitted > 0 {
		*lines = append(*lines, fmt.Sprintf("%d more transactions are in the JSON report.", view.Omitted), "")
	}
}

func timeAnatomyCSV(d *runArchiveHTMLReportData, write reportCSVWriteFunc) error {
	anatomy := d.Report.TimeAnatomy
	if anatomy == nil || len(anatomy.Transactions) == 0 {
		return csvNote(write, "time_anatomy", "No business-transaction time anatomy was captured for this run.")
	}
	// The CSV is a data export: every transaction, not the printed few.
	for _, tx := range anatomy.Transactions {
		rows := [][2]string{
			{"instances", strconv.FormatInt(tx.Instances, 10)},
			{"p95_ms", formatChartValue(tx.Duration.P95Ms)},
		}
		if tx.Tail != nil {
			rows = append(rows, [2]string{"tail_status", tx.Tail.Status})
			if tx.Tail.Status == "ok" {
				rows = append(rows, [2]string{"tail_delta_ms", formatChartValue(tx.Tail.Delta.TotalMs)}, [2]string{"tail_headline", tx.Tail.Headline})
				for _, component := range tx.Tail.Delta.Components {
					rows = append(rows, [2]string{"tail_" + component.Component + "_ms", formatChartValue(component.DeltaMs)})
				}
			}
		}
		for _, row := range rows {
			if err := write("time_anatomy", tx.Name, row[0], row[1]); err != nil {
				return err
			}
		}
	}
	return nil
}

// ---- stacked bar charts ----------------------------------------------------

// reportChartStack is one stacked bar: a label and one segment per drawn
// phase.
type reportChartStack struct {
	Label    string
	Segments []float64
}

func reportStackedChartHeight(bars int) int { return max(120, 76+bars*30) }

func reportStackMax(stacks []reportChartStack) float64 {
	maxValue := 0.0
	for _, stack := range stacks {
		total := 0.0
		for _, v := range stack.Segments {
			total += max(0, v)
		}
		maxValue = max(maxValue, total)
	}
	if maxValue <= 0 {
		return 1
	}
	return maxValue
}

// svgStackedBarChart draws one horizontal stacked bar per row, with a
// phase legend, for the HTML report (and the Word converter, which swaps
// it for the rasterized chart of the same title).
func svgStackedBarChart(spec reportDocumentChartSpec) template.HTML {
	if len(spec.Stacks) == 0 {
		return ""
	}
	const rowH, labelW = 28, 220
	top := reportChartPadT + 26
	height := top + len(spec.Stacks)*rowH + 16
	barLeft := labelW + 12
	barMax := float64(reportChartWidth - barLeft - 70)
	maxValue := reportStackMax(spec.Stacks)
	var b strings.Builder
	fmt.Fprintf(&b, `<svg viewBox="0 0 %d %d" role="img" aria-label="%s" preserveAspectRatio="xMidYMid meet" style="width:100%%;height:auto;background:%s;border:1px solid %s;border-radius:14px">`,
		reportChartWidth, height, svgAttr(spec.Title), reportChartSurface, reportChartGrid)
	fmt.Fprintf(&b, `<text x="%d" y="24" fill="%s" font-family="Avenir Next, Segoe UI, sans-serif" font-size="15" font-weight="600">%s</text>`, reportChartPadL, reportChartInk, svgText(spec.Title))
	x := reportChartPadL
	for _, phase := range reportAnatomyPhases {
		fmt.Fprintf(&b, `<rect x="%d" y="36" width="10" height="10" rx="2" fill="%s"/><text x="%d" y="45" fill="%s" font-family="Avenir Next, Segoe UI, sans-serif" font-size="11">%s</text>`, x, phase.Color, x+14, reportChartMuted, svgText(phase.Label))
		x += 26 + 7*len(phase.Label)
	}
	for i, stack := range spec.Stacks {
		y := top + i*rowH
		fmt.Fprintf(&b, `<text x="%d" y="%d" fill="%s" font-family="Avenir Next, Segoe UI, sans-serif" font-size="12" text-anchor="end">%s</text>`, labelW, y+16, reportChartInk, svgText(truncateLabel(stack.Label, 34)))
		left, total := float64(barLeft), 0.0
		for s, value := range stack.Segments {
			if value <= 0 || s >= len(reportAnatomyPhases) {
				continue
			}
			w := value / maxValue * barMax
			fmt.Fprintf(&b, `<rect x="%.1f" y="%d" width="%.1f" height="16" fill="%s"/>`, left, y+4, w, reportAnatomyPhases[s].Color)
			left += w
			total += value
		}
		fmt.Fprintf(&b, `<text x="%.1f" y="%d" fill="%s" font-family="Avenir Next, Segoe UI, sans-serif" font-size="11">%s %s</text>`, left+6, y+16, reportChartMuted, svgText(formatChartValue(math.Round(total))), svgText(spec.Unit))
	}
	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}

// drawReportStackedBarChart rasterizes the same chart for PDF and Word.
func drawReportStackedBarChart(img *image.RGBA, spec reportDocumentChartSpec, regular font.Face) {
	const scale = 2
	left, right := 270*scale, (reportChartWidth-68)*scale
	x := 32
	for _, phase := range reportAnatomyPhases {
		chartFillRect(img, x, 62, x+20, 82, phase.RGBA)
		chartText(img, regular, x+28, 80, chartMuted, phase.Label, false)
		x += 56 + 15*len(phase.Label)
	}
	maxValue := reportStackMax(spec.Stacks)
	for index, stack := range spec.Stacks {
		y := (76 + index*30) * scale
		chartText(img, regular, left-14, y+20, chartInk, truncateLabel(stack.Label, 32), true)
		at, total := left, 0.0
		for s, value := range stack.Segments {
			if value <= 0 || s >= len(reportAnatomyPhases) {
				continue
			}
			width := int(value / maxValue * float64(right-left))
			chartFillRect(img, at, y, at+width, y+25, reportAnatomyPhases[s].RGBA)
			at += width
			total += value
		}
		chartText(img, regular, at+9, y+21, chartMuted, formatChartValue(math.Round(total))+" ms", false)
	}
}
