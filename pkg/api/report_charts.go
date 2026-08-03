package api

import (
	"fmt"
	"html"
	"html/template"
	"sort"
	"strings"
	"time"
)

// reportCharts holds the inline SVG graphs embedded in the HTML report. The
// SVGs are generated server-side with pure Go (no external charting or PDF
// dependency), mirroring the hand-rolled report PDF generator. Each field is
// safe to inject as raw HTML because it is built from numeric series and
// escaped labels only.
type reportCharts struct {
	HasTimeline   bool
	Throughput    template.HTML
	ResponseTime  template.HTML
	ErrorRate     template.HTML
	VUsers        template.HTML
	HasActions    bool
	ActionLatency template.HTML
}

type reportChartSeries struct {
	Label  string
	Color  string
	Values []float64
}

type reportChartBar struct {
	Label string
	Value float64
}

const (
	reportChartWidth   = 820
	reportChartHeight  = 260
	reportChartPadL    = 60
	reportChartPadR    = 18
	reportChartPadT    = 40
	reportChartPadB    = 30
	reportChartAccent  = "#1f6feb"
	reportChartAccent2 = "#8a6100"
	reportChartGrid    = "#d4dbe3"
	reportChartInk     = "#1a2330"
	reportChartMuted   = "#4a5568"
	reportChartSurface = "#f3f6f9"
)

// buildReportCharts derives all report graphs from the archived run report.
func buildReportCharts(report *RunArchiveReport) reportCharts {
	charts := reportCharts{}
	if report == nil {
		return charts
	}

	if points := report.SummaryTimeline; len(points) >= 2 {
		charts.HasTimeline = true
		startLabel := formatChartClock(points[0].Timestamp)
		endLabel := formatChartClock(points[len(points)-1].Timestamp)

		tps := make([]float64, len(points))
		target := make([]float64, len(points))
		p95 := make([]float64, len(points))
		errPct := make([]float64, len(points))
		vusers := make([]float64, len(points))
		hasTarget := false
		hasCompleteP95 := true
		for i, p := range points {
			tps[i] = p.TPS
			target[i] = p.TargetTPS
			if p.TargetTPS > 0 {
				hasTarget = true
			}
			if p.HasP95() {
				p95[i] = p.P95Ms
			} else {
				hasCompleteP95 = false
			}
			errPct[i] = p.ErrorPct
			vusers[i] = float64(p.ActiveVUsers)
		}

		throughputSeries := []reportChartSeries{{Label: "Actual TPS", Color: reportChartAccent, Values: tps}}
		if hasTarget {
			throughputSeries = append(throughputSeries, reportChartSeries{Label: "Target TPS", Color: reportChartAccent2, Values: target})
		}
		charts.Throughput = svgLineChart("Throughput (TPS)", "TPS", startLabel, endLabel, throughputSeries)
		if hasCompleteP95 {
			charts.ResponseTime = svgLineChart("Response Time (p95)", "ms", startLabel, endLabel,
				[]reportChartSeries{{Label: "p95 latency", Color: reportChartAccent, Values: p95}})
		}
		charts.ErrorRate = svgLineChart("Error Rate", "%", startLabel, endLabel,
			[]reportChartSeries{{Label: "Error %", Color: reportChartAccent2, Values: errPct}})
		charts.VUsers = svgLineChart("Active VUsers", "VUsers", startLabel, endLabel,
			[]reportChartSeries{{Label: "Active VUsers", Color: reportChartAccent, Values: vusers}})
	}

	if bars := topActionLatencyBars(report.ByAction, 8); len(bars) > 0 {
		charts.HasActions = true
		charts.ActionLatency = svgBarChart("Average Response Time by Action", "ms", bars)
	}

	return charts
}

// topActionLatencyBars selects the busiest actions (by transaction count) and
// orders the resulting bars by average response time descending.
func topActionLatencyBars(actions map[string]RunArchiveActionStats, limit int) []reportChartBar {
	if len(actions) == 0 {
		return nil
	}
	names := make([]string, 0, len(actions))
	for name := range actions {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if actions[names[i]].Transactions != actions[names[j]].Transactions {
			return actions[names[i]].Transactions > actions[names[j]].Transactions
		}
		return names[i] < names[j]
	})
	if len(names) > limit {
		names = names[:limit]
	}
	bars := make([]reportChartBar, 0, len(names))
	for _, name := range names {
		bars = append(bars, reportChartBar{Label: name, Value: actions[name].AvgResponseMs})
	}
	sort.SliceStable(bars, func(i, j int) bool { return bars[i].Value > bars[j].Value })
	return bars
}

// svgLineChart renders one or more series sharing an evenly-spaced x axis.
func svgLineChart(title, unit, startLabel, endLabel string, series []reportChartSeries) template.HTML {
	plotW := reportChartWidth - reportChartPadL - reportChartPadR
	plotH := reportChartHeight - reportChartPadT - reportChartPadB
	baseY := reportChartPadT + plotH

	maxVal := 0.0
	count := 0
	for _, s := range series {
		if len(s.Values) > count {
			count = len(s.Values)
		}
		for _, v := range s.Values {
			if v > maxVal {
				maxVal = v
			}
		}
	}
	if maxVal <= 0 {
		maxVal = 1
	}
	if count < 2 {
		return ""
	}

	var b strings.Builder
	fmt.Fprintf(&b, `<svg viewBox="0 0 %d %d" role="img" aria-label="%s" preserveAspectRatio="xMidYMid meet" style="width:100%%;height:auto;background:%s;border:1px solid %s;border-radius:14px">`,
		reportChartWidth, reportChartHeight, svgAttr(title), reportChartSurface, reportChartGrid)
	fmt.Fprintf(&b, `<text x="%d" y="24" fill="%s" font-family="Avenir Next, Segoe UI, sans-serif" font-size="15" font-weight="600">%s</text>`,
		reportChartPadL, reportChartInk, svgText(title))

	// Horizontal gridlines + y-axis labels at 0, 50%, 100%.
	for _, frac := range []float64{0, 0.5, 1} {
		y := float64(baseY) - frac*float64(plotH)
		fmt.Fprintf(&b, `<line x1="%d" y1="%.1f" x2="%d" y2="%.1f" stroke="%s" stroke-width="1" stroke-dasharray="%s"/>`,
			reportChartPadL, y, reportChartPadL+plotW, y, reportChartGrid, gridDash(frac))
		fmt.Fprintf(&b, `<text x="%d" y="%.1f" fill="%s" font-family="Avenir Next, Segoe UI, sans-serif" font-size="11" text-anchor="end">%s</text>`,
			reportChartPadL-8, y+4, reportChartMuted, svgText(formatChartValue(maxVal*frac)))
	}

	// Series polylines.
	for _, s := range series {
		pts := make([]string, 0, len(s.Values))
		step := float64(plotW) / float64(count-1)
		for i, v := range s.Values {
			x := float64(reportChartPadL) + float64(i)*step
			y := float64(baseY) - (v/maxVal)*float64(plotH)
			pts = append(pts, fmt.Sprintf("%.1f,%.1f", x, y))
		}
		fmt.Fprintf(&b, `<polyline fill="none" stroke="%s" stroke-width="2" stroke-linejoin="round" points="%s"/>`,
			svgAttr(s.Color), strings.Join(pts, " "))
	}

	// X-axis end labels + unit.
	fmt.Fprintf(&b, `<text x="%d" y="%d" fill="%s" font-family="Avenir Next, Segoe UI, sans-serif" font-size="11">%s</text>`,
		reportChartPadL, baseY+20, reportChartMuted, svgText(startLabel))
	fmt.Fprintf(&b, `<text x="%d" y="%d" fill="%s" font-family="Avenir Next, Segoe UI, sans-serif" font-size="11" text-anchor="end">%s</text>`,
		reportChartPadL+plotW, baseY+20, reportChartMuted, svgText(endLabel))

	// Legend (top-right) for multi-series.
	if len(series) > 1 {
		lx := reportChartPadL + plotW
		for i := len(series) - 1; i >= 0; i-- {
			s := series[i]
			fmt.Fprintf(&b, `<rect x="%d" y="14" width="10" height="10" fill="%s"/>`, lx-12, svgAttr(s.Color))
			fmt.Fprintf(&b, `<text x="%d" y="23" fill="%s" font-family="Avenir Next, Segoe UI, sans-serif" font-size="11" text-anchor="end">%s</text>`,
				lx-16, reportChartMuted, svgText(s.Label))
			lx -= 16 + 8*len(s.Label)
		}
	}

	// Y-axis unit label (rotated) on the far left.
	fmt.Fprintf(&b, `<text x="14" y="%d" fill="%s" font-family="Avenir Next, Segoe UI, sans-serif" font-size="11" text-anchor="middle" transform="rotate(-90 14 %d)">%s</text>`,
		reportChartPadT+plotH/2, reportChartMuted, reportChartPadT+plotH/2, svgText(unit))

	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}

// svgBarChart renders a horizontal bar chart (one bar per label).
func svgBarChart(title, unit string, bars []reportChartBar) template.HTML {
	if len(bars) == 0 {
		return ""
	}
	rowH := 28
	top := reportChartPadT
	height := top + len(bars)*rowH + 24
	labelW := 220
	barLeft := labelW + 12
	barMax := reportChartWidth - barLeft - 60

	maxVal := 0.0
	for _, bar := range bars {
		if bar.Value > maxVal {
			maxVal = bar.Value
		}
	}
	if maxVal <= 0 {
		maxVal = 1
	}

	var b strings.Builder
	fmt.Fprintf(&b, `<svg viewBox="0 0 %d %d" role="img" aria-label="%s" preserveAspectRatio="xMidYMid meet" style="width:100%%;height:auto;background:%s;border:1px solid %s;border-radius:14px">`,
		reportChartWidth, height, svgAttr(title), reportChartSurface, reportChartGrid)
	fmt.Fprintf(&b, `<text x="%d" y="24" fill="%s" font-family="Avenir Next, Segoe UI, sans-serif" font-size="15" font-weight="600">%s</text>`,
		reportChartPadL, reportChartInk, svgText(title))

	for i, bar := range bars {
		y := top + i*rowH
		w := (bar.Value / maxVal) * float64(barMax)
		if w < 1 {
			w = 1
		}
		fmt.Fprintf(&b, `<text x="%d" y="%d" fill="%s" font-family="Avenir Next, Segoe UI, sans-serif" font-size="12" text-anchor="end">%s</text>`,
			labelW, y+16, reportChartInk, svgText(truncateLabel(bar.Label, 34)))
		fmt.Fprintf(&b, `<rect x="%d" y="%d" width="%.1f" height="16" rx="3" fill="%s"/>`,
			barLeft, y+4, w, reportChartAccent)
		fmt.Fprintf(&b, `<text x="%.1f" y="%d" fill="%s" font-family="Avenir Next, Segoe UI, sans-serif" font-size="11">%s %s</text>`,
			float64(barLeft)+w+6, y+16, reportChartMuted, svgText(formatChartValue(bar.Value)), svgText(unit))
	}

	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}

func gridDash(frac float64) string {
	if frac == 0 {
		return "0"
	}
	return "4 4"
}

func formatChartClock(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("15:04:05")
}

func formatChartValue(v float64) string {
	if v >= 1000 {
		return fmt.Sprintf("%.0f", v)
	}
	if v == float64(int64(v)) {
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%.1f", v)
}

func truncateLabel(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 1 {
		return s[:max]
	}
	return s[:max-1] + "…"
}

// svgText escapes a string for use as SVG/HTML text content.
func svgText(s string) string {
	return html.EscapeString(s)
}

// svgAttr escapes a string for use inside a double-quoted SVG attribute.
func svgAttr(s string) string {
	return html.EscapeString(s)
}
