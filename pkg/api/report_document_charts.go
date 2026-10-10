package api

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"strings"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// The same bounded chart set appears in the HTML report and the printable
// profiles. Document charts are rasterized at twice their displayed size so
// they stay legible in Word and in printed PDFs.
const reportDocumentChartMarker = "\x1eapexload-chart:"

type reportDocumentChartSpec struct {
	ID          string
	Title       string
	Unit        string
	StartLabel  string
	EndLabel    string
	Series      []reportChartSeries
	Bars        []reportChartBar
	HighlightAt float64
	Height      int
}

type reportDocumentChart struct {
	ID     string
	Title  string
	Width  int
	Height int
	PNG    []byte
	RGB    []byte // zlib-compressed DeviceRGB pixels for the PDF image XObject
}

func reportDocumentChartSpecs(report *RunArchiveReport) []reportDocumentChartSpec {
	if report == nil {
		return nil
	}
	var specs []reportDocumentChartSpec
	if points := report.SummaryTimeline; len(points) >= 2 {
		series := func(value func(RunArchiveSummaryPoint) float64) []float64 {
			values := make([]float64, len(points))
			for index, point := range points {
				values[index] = value(point)
			}
			return values
		}
		start, end := formatChartClock(points[0].Timestamp), formatChartClock(points[len(points)-1].Timestamp)
		base := func(id, title, unit string, chartSeries ...reportChartSeries) reportDocumentChartSpec {
			return reportDocumentChartSpec{ID: id, Title: title, Unit: unit, StartLabel: start, EndLabel: end, Series: chartSeries, Height: reportChartHeight}
		}
		throughput := []reportChartSeries{{Label: "Actual TPS", Color: reportChartAccent, Values: series(func(p RunArchiveSummaryPoint) float64 { return p.TPS })}}
		for _, point := range points {
			if point.TargetTPS > 0 {
				throughput = append(throughput, reportChartSeries{Label: "Target TPS", Color: reportChartAccent2, Values: series(func(p RunArchiveSummaryPoint) float64 { return p.TargetTPS })})
				break
			}
		}
		specs = append(specs, base("throughput", "Throughput (TPS)", "TPS", throughput...))
		completeP95 := true
		for _, point := range points {
			if !point.HasP95() {
				completeP95 = false
				break
			}
		}
		if completeP95 {
			specs = append(specs, base("response-time", "Response time (P95, ms)", "ms", reportChartSeries{Label: "p95 latency", Color: reportChartAccent, Values: series(func(p RunArchiveSummaryPoint) float64 { return p.P95Ms })}))
		}
		specs = append(specs,
			base("error-rate", "Error rate (%)", "%", reportChartSeries{Label: "Error %", Color: reportChartAccent2, Values: series(func(p RunArchiveSummaryPoint) float64 { return p.ErrorPct })}),
			base("vusers", "Active VUsers", "VUsers", reportChartSeries{Label: "Active VUsers", Color: reportChartAccent, Values: series(func(p RunArchiveSummaryPoint) float64 { return float64(p.ActiveVUsers) })}),
		)
	}
	actions := report.ByAction
	if report.MetricPopulations != nil {
		actions = report.ByRequest
	}
	if bars := topActionLatencyBars(actions, reportActionLatencyLimit); len(bars) > 0 {
		average := report.Overview.AvgResponseMs
		if report.MetricPopulations != nil {
			average = report.MetricPopulations["action"].AvgResponseMs
		}
		specs = append(specs, reportDocumentChartSpec{ID: "action-latency", Title: actionLatencyTitle(len(bars), len(actions)), Unit: "ms", Bars: bars, HighlightAt: average, Height: max(192, 75+len(bars)*31)})
	}
	return specs
}

func buildReportDocumentCharts(report *RunArchiveReport) ([]reportDocumentChart, error) {
	specs := reportDocumentChartSpecs(report)
	if len(specs) == 0 {
		return nil, nil
	}
	regularFont, err := opentype.Parse(goregular.TTF)
	if err != nil {
		return nil, fmt.Errorf("parse report chart font: %w", err)
	}
	boldFont, err := opentype.Parse(gobold.TTF)
	if err != nil {
		return nil, fmt.Errorf("parse report chart bold font: %w", err)
	}
	regular, err := opentype.NewFace(regularFont, &opentype.FaceOptions{Size: 12, DPI: 144, Hinting: font.HintingFull})
	if err != nil {
		return nil, fmt.Errorf("create report chart font: %w", err)
	}
	defer regular.Close()
	bold, err := opentype.NewFace(boldFont, &opentype.FaceOptions{Size: 14, DPI: 144, Hinting: font.HintingFull})
	if err != nil {
		return nil, fmt.Errorf("create report chart bold font: %w", err)
	}
	defer bold.Close()
	charts := make([]reportDocumentChart, 0, len(specs))
	for _, spec := range specs {
		chart, err := rasterizeReportChart(spec, regular, bold)
		if err != nil {
			return nil, err
		}
		charts = append(charts, chart)
	}
	return charts, nil
}

var (
	chartTeal   = color.RGBA{15, 118, 110, 255}
	chartAmber  = color.RGBA{180, 83, 9, 255}
	chartGrid   = color.RGBA{216, 207, 194, 255}
	chartInk    = color.RGBA{31, 41, 51, 255}
	chartMuted  = color.RGBA{91, 104, 117, 255}
	chartBorder = color.RGBA{220, 213, 202, 255}
)

func rasterizeReportChart(spec reportDocumentChartSpec, regular, bold font.Face) (reportDocumentChart, error) {
	const scale = 2
	width, height := reportChartWidth*scale, spec.Height*scale
	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(canvas, canvas.Bounds(), &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	chartFillRect(canvas, 0, 0, width, 2, chartBorder)
	chartFillRect(canvas, 0, height-2, width, height, chartBorder)
	chartFillRect(canvas, 0, 0, 2, height, chartBorder)
	chartFillRect(canvas, width-2, 0, width, height, chartBorder)
	chartText(canvas, bold, 32, 46, chartInk, spec.Title, false)
	if len(spec.Bars) > 0 {
		drawReportBarChart(canvas, spec, regular)
	} else {
		drawReportLineChart(canvas, spec, regular)
	}
	var pngBytes bytes.Buffer
	if err := png.Encode(&pngBytes, canvas); err != nil {
		return reportDocumentChart{}, fmt.Errorf("encode report chart %q: %w", spec.ID, err)
	}
	var rgbBytes bytes.Buffer
	compressor := zlib.NewWriter(&rgbBytes)
	row := make([]byte, width*3)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			pixel := canvas.RGBAAt(x, y)
			row[x*3], row[x*3+1], row[x*3+2] = pixel.R, pixel.G, pixel.B
		}
		if _, err := compressor.Write(row); err != nil {
			return reportDocumentChart{}, err
		}
	}
	if err := compressor.Close(); err != nil {
		return reportDocumentChart{}, err
	}
	return reportDocumentChart{ID: spec.ID, Title: spec.Title, Width: width, Height: height, PNG: pngBytes.Bytes(), RGB: rgbBytes.Bytes()}, nil
}

func drawReportLineChart(img *image.RGBA, spec reportDocumentChartSpec, regular font.Face) {
	const scale = 2
	left, right, top, bottom := 78*scale, (reportChartWidth-24)*scale, 62*scale, (spec.Height-36)*scale
	maxValue := 0.0
	for _, series := range spec.Series {
		for _, value := range series.Values {
			if !math.IsNaN(value) && !math.IsInf(value, 0) && value > maxValue {
				maxValue = value
			}
		}
	}
	if maxValue <= 0 {
		maxValue = 1
	}
	for _, fraction := range []float64{0, 0.5, 1} {
		y := bottom - int(fraction*float64(bottom-top))
		for x := left; x < right; x += 12 {
			chartFillRect(img, x, y, min(x+7, right), y+2, chartGrid)
		}
		chartText(img, regular, left-13, y+7, chartMuted, formatChartValue(maxValue*fraction), true)
	}
	chartText(img, regular, left, bottom+31, chartMuted, spec.StartLabel, false)
	chartText(img, regular, right, bottom+31, chartMuted, spec.EndLabel, true)
	chartText(img, regular, 29, top+14, chartMuted, spec.Unit, false)
	for _, series := range spec.Series {
		if len(series.Values) < 2 {
			continue
		}
		paint := chartTeal
		if series.Color == reportChartAccent2 {
			paint = chartAmber
		}
		for index := 1; index < len(series.Values); index++ {
			previous, current := series.Values[index-1], series.Values[index]
			if math.IsNaN(previous) || math.IsNaN(current) || math.IsInf(previous, 0) || math.IsInf(current, 0) {
				continue
			}
			x1 := left + int(float64(index-1)*float64(right-left)/float64(len(series.Values)-1))
			x2 := left + int(float64(index)*float64(right-left)/float64(len(series.Values)-1))
			y1 := bottom - int(previous/maxValue*float64(bottom-top))
			y2 := bottom - int(current/maxValue*float64(bottom-top))
			chartLine(img, x1, y1, x2, y2, paint)
		}
	}
	if len(spec.Series) > 1 {
		chartFillRect(img, right-232, 30, right-218, 36, chartTeal)
		chartText(img, regular, right-209, 40, chartMuted, spec.Series[0].Label, false)
		chartFillRect(img, right-103, 30, right-89, 36, chartAmber)
		chartText(img, regular, right-80, 40, chartMuted, spec.Series[1].Label, false)
	}
}

func drawReportBarChart(img *image.RGBA, spec reportDocumentChartSpec, regular font.Face) {
	const scale = 2
	left, right := 270*scale, (reportChartWidth-68)*scale
	if spec.HighlightAt > 0 {
		chartText(img, regular, 32, 79, chartMuted, "Amber = at or above run average ("+formatChartValue(spec.HighlightAt)+" ms)", false)
	}
	maxValue := 0.0
	for _, bar := range spec.Bars {
		if bar.Value > maxValue {
			maxValue = bar.Value
		}
	}
	if maxValue <= 0 {
		maxValue = 1
	}
	for index, bar := range spec.Bars {
		y := (65 + index*31) * scale
		chartText(img, regular, left-14, y+20, chartInk, truncateLabel(bar.Label, 32), true)
		barWidth := int(math.Max(2, math.Max(0, bar.Value)/maxValue*float64(right-left)))
		paint := chartTeal
		if spec.HighlightAt > 0 && bar.Value >= spec.HighlightAt {
			paint = chartAmber
		}
		chartFillRect(img, left, y, left+barWidth, y+25, paint)
		chartText(img, regular, left+barWidth+9, y+21, chartMuted, formatChartValue(bar.Value)+" ms", false)
	}
}

func chartFillRect(img *image.RGBA, x0, y0, x1, y1 int, paint color.RGBA) {
	draw.Draw(img, image.Rect(x0, y0, x1, y1), &image.Uniform{C: paint}, image.Point{}, draw.Src)
}

func chartText(img *image.RGBA, face font.Face, x, y int, paint color.RGBA, value string, alignRight bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	drawer := font.Drawer{Dst: img, Src: &image.Uniform{C: paint}, Face: face}
	if alignRight {
		x -= drawer.MeasureString(value).Ceil()
	}
	drawer.Dot = fixed.P(x, y)
	drawer.DrawString(value)
}

func chartLine(img *image.RGBA, x0, y0, x1, y1 int, paint color.RGBA) {
	steps := max(chartAbs(x1-x0), chartAbs(y1-y0))
	if steps == 0 {
		chartFillRect(img, x0-2, y0-2, x0+3, y0+3, paint)
		return
	}
	for step := 0; step <= steps; step++ {
		x := x0 + (x1-x0)*step/steps
		y := y0 + (y1-y0)*step/steps
		chartFillRect(img, x-2, y-2, x+3, y+3, paint)
	}
}

func chartAbs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
