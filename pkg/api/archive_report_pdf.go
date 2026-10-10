package api

import (
	"bytes"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

const (
	pdfPageWidth     = 612
	pdfPageHeight    = 792
	pdfLineWrapWidth = 92
	pdfPageTop       = 748
	pdfPageLeft      = 54
)

func writeRunArchiveReportPDFWithProfile(report *RunArchiveReport, writer io.Writer, profileID string) error {
	return writeRunArchiveReportPDFWithProfileAndAnnotations(report, writer, profileID, nil, nil)
}

// writeRunArchiveReportPDFWithProfileAndAnnotations renders the PDF with the
// analyst-saved annotations (verdict override / official outcome) and the
// generator-assignment manifest, so the exported audit PDF matches the HTML
// report instead of showing the auto-derived outcome and a "manifest
// unavailable" note.
func writeRunArchiveReportPDFWithProfileAndAnnotations(report *RunArchiveReport, writer io.Writer, profileID string, annotations *ReportAnnotations, manifest *TestRunManifest) error {
	profile, ok := lookupRunArchiveReportProfile(profileID)
	if !ok {
		return fmt.Errorf("unknown report profile %q", profileID)
	}
	if !runArchiveReportProfileSupportsFormat(profile, "pdf") {
		return fmt.Errorf("report profile %q does not support pdf export", profile.ID)
	}
	lines := buildRunArchiveReportPDFLines(report, profile, annotations, manifest)
	headings := buildDOCXSectionHeadings(report, profile, annotations, manifest)
	headings["Review summary"] = struct{}{}
	var charts []reportDocumentChart
	if _, hasGraphs := reportSectionSet(profile.IncludedSections)["graphs"]; hasGraphs {
		var err error
		charts, err = buildReportDocumentCharts(report)
		if err != nil {
			return err
		}
	}
	_, err := writer.Write(renderTextPDF(lines, headings, profile.Label, charts))
	return err
}

func runArchiveReportProfileSupportsFormat(profile RunArchiveReportProfile, format string) bool {
	format = strings.ToLower(strings.TrimSpace(format))
	for _, candidate := range profile.ExportFormats {
		if strings.EqualFold(candidate, format) {
			return true
		}
	}
	return false
}

// buildRunArchiveReportPDFLines renders the report as text lines by iterating the
// profile's SectionOrder (gated by IncludedSections) through the shared section
// registry, so the profile metadata is the single source of truth for which
// sections appear and in what order.
func buildRunArchiveReportPDFLines(report *RunArchiveReport, profile RunArchiveReportProfile, annotations *ReportAnnotations, manifest *TestRunManifest) []string {
	if report == nil {
		return []string{"APEX Load Archive Report", "Profile: " + profile.Label, "No archived report evidence is available."}
	}
	data := buildRunArchiveReportRenderData(report, profile, annotations, manifest)
	title := strings.TrimSpace(data.Annotations.Title)
	if title == "" {
		title = "APEX Load Archive Report"
	}
	lines := []string{
		title,
		"Profile: " + profile.Label,
		"Generated: " + formatReportDateTime(time.Now()),
	}
	if preparedBy := strings.TrimSpace(data.Annotations.PreparedBy); preparedBy != "" {
		lines = append(lines, "Prepared by: "+preparedBy)
	}
	lines = append(lines, "")

	if report.Manifest != nil {
		lines = append(lines,
			"Run: "+reportRunName(report),
			"Completed: "+formatPDFDisplayTime(report.Manifest.CompletedAt),
			"",
		)
	}

	lines = append(lines,
		"Review summary",
		"Outcome: "+data.Verdict.Status,
		data.Verdict.Explanation,
		"Environment: "+reportEnvironment(report),
		fmt.Sprintf("Evidence completeness: %.0f%% (%s)", report.Completeness.ScorePct, report.Completeness.Status),
	)
	if profile.ID != "audit" {
		lines = append(lines,
			fmt.Sprintf("Captured records: %s; error rate: %s (%s); peak load: %d VUsers", formatPDFInt(report.Overview.Transactions), formatReportPercentage(reportHeadline(report).ErrorPct), reportHeadline(report).Label, report.Overview.PeakActiveVUsers),
		)
	} else if report.Integrity != nil {
		lines = append(lines, "Artifact integrity: "+report.Integrity.Status)
	}
	if profile.ID == "executive" {
		for _, item := range []struct{ key, label string }{{"decision", "Decision context"}, {"recommendations", "Recommendation"}} {
			value := strings.TrimSpace(data.Annotations.SectionComments[item.key])
			if value != "" && !strings.EqualFold(value, "Not provided") {
				lines = append(lines, item.label+": "+value)
			}
		}
	} else if profile.ID == "engineer" && len(data.Findings) > 0 {
		lines = append(lines, "Priority finding: "+data.Findings[0].Title)
	}
	if len(data.EvidenceGaps) > 0 {
		lines = append(lines, "Evidence gaps:")
		for _, gap := range data.EvidenceGaps {
			lines = append(lines, "- "+gap)
		}
	}
	lines = append(lines, "")
	sections, notCaptured := reportApplicableSections(profile, &data)
	for _, section := range sections {
		section.PDF(&data, &lines)
	}
	if len(notCaptured) > 0 {
		lines = append(lines, "Not captured for this run: "+strings.Join(notCaptured, ", ")+".", "")
	}

	return lines
}

func actorPDFValue(displayName, subject string) string {
	displayName = strings.TrimSpace(displayName)
	subject = strings.TrimSpace(subject)
	if displayName != "" && subject != "" {
		return displayName + " (" + subject + ")"
	}
	return firstNonEmptyPDFValue(displayName, subject)
}

// appendReleaseTrendPDFLines renders the compact release-over-release trend as
// PDF text lines. Called by the release_trend section renderer in the registry.
func appendReleaseTrendPDFLines(lines *[]string, trend *RunArchiveReleaseTrendSummary) {
	if trend == nil {
		return
	}
	*lines = append(*lines, reportSectionTitle("release_trend"))
	label := "Unknown (legacy archive records)"
	if trend.Current.MetricPopulation == "transaction" {
		label = "Named transaction attempts"
	}
	*lines = append(*lines, "Metric population: "+label)
	*lines = append(*lines, fmt.Sprintf("Release: %s (%s)", firstNonEmptyPDFValue(firstNonEmptyReportText(trend.ReleaseName, trend.ReleaseID)), reportCountNoun(int64(trend.RunCount), "archived run", "archived runs")))
	if trend.HasBaseline && trend.Baseline != nil && trend.Deltas != nil {
		baseline := trend.Baseline
		*lines = append(*lines,
			fmt.Sprintf("Baseline (prior run): %s, completed %s", firstNonEmptyPDFValue(baseline.RunName, baseline.RunID), formatPDFDisplayTime(baseline.CompletedAt)),
			fmt.Sprintf("Error rate: %s -> %s (%+.2f pts)", formatReportPercentage(baseline.ErrorPct), formatReportPercentage(trend.Current.ErrorPct), trend.Deltas.ErrorPctDelta),
			fmt.Sprintf("Avg latency: %s -> %s (%+.1f ms / %+.1f%%)", formatReportMs(baseline.AvgResponseMs), formatReportMs(trend.Current.AvgResponseMs), trend.Deltas.AvgResponseMsDelta, trend.Deltas.AvgResponsePctDelta),
			fmt.Sprintf("Peak TPS: %.1f -> %.1f (%+.1f)", baseline.PeakTPS, trend.Current.PeakTPS, trend.Deltas.PeakTPSDelta),
			fmt.Sprintf("Evidence coverage: %.0f%% -> %.0f%% (%+.1f pts)", baseline.CompletenessPct, trend.Current.CompletenessPct, trend.Deltas.CompletenessDelta),
		)
		if trend.Regressed {
			*lines = append(*lines, "Regressions vs prior run: "+strings.Join(trend.Regressions, "; "))
		} else {
			*lines = append(*lines, "No regressions versus the prior run.")
		}
	} else {
		note := trend.Note
		if note == "" {
			note = "First archived run for this release; no prior run to compare against."
		}
		*lines = append(*lines, note)
	}
	*lines = append(*lines, "")
}

func formatPDFTimePtr(value *time.Time) string {
	if value == nil {
		return ""
	}
	return formatPDFTime(*value)
}

func appendPDFStatsSection(lines *[]string, title string, stats map[string]RunArchiveActionStats, limit int) {
	if len(stats) == 0 {
		return
	}
	names := sortedArchiveReportStatKeys(stats)
	sort.SliceStable(names, func(i, j int) bool {
		left := stats[names[i]]
		right := stats[names[j]]
		if left.Transactions != right.Transactions {
			return left.Transactions > right.Transactions
		}
		return names[i] < names[j]
	})
	if len(names) > limit {
		names = names[:limit]
	}
	*lines = append(*lines, title)
	for _, name := range names {
		stat := stats[name]
		*lines = append(*lines, fmt.Sprintf("- %s: %s tx, %s errors (%s), avg %.1f ms, max %.1f ms",
			name,
			formatPDFInt(stat.Transactions),
			formatPDFInt(stat.Errors),
			formatReportPercentage(stat.ErrorPct),
			stat.AvgResponseMs,
			stat.MaxResponseMs,
		))
	}
	*lines = append(*lines, "")
}

type pdfReportLine struct {
	Text        string
	Heading     bool
	Title       bool
	Cells       []string
	TableHeader bool
	ChartID     string
	ChartHeight int
}

func renderTextPDF(lines []string, headings map[string]struct{}, profileLabel string, charts []reportDocumentChart) []byte {
	chartByID := make(map[string]reportDocumentChart, len(charts))
	for _, chart := range charts {
		chartByID[chart.ID] = chart
	}
	wrapped := make([]pdfReportLine, 0, len(lines))
	for index, line := range lines {
		if strings.HasPrefix(line, reportDocumentChartMarker) {
			if chart, ok := chartByID[strings.TrimPrefix(line, reportDocumentChartMarker)]; ok {
				wrapped = append(wrapped, pdfReportLine{ChartID: chart.ID, ChartHeight: chart.Height*504/chart.Width + 18})
			}
			continue
		}
		if cells, ok := parseReportTableLine(line); ok {
			wrapped = append(wrapped, pdfReportLine{Cells: cells, TableHeader: index > 0 && !strings.HasPrefix(lines[index-1], "|")})
			continue
		}
		line = pdfASCIIText(line)
		_, heading := headings[line]
		for partIndex, part := range wrapPDFLines([]string{line}) {
			wrapped = append(wrapped, pdfReportLine{
				Text: part, Heading: heading && partIndex == 0, Title: index == 0 && partIndex == 0,
			})
		}
	}
	if len(wrapped) == 0 {
		wrapped = []pdfReportLine{{}}
	}
	pages := paginatePDFReportLines(wrapped)
	outlineCount := 0
	for _, page := range pages {
		for _, line := range page {
			if line.Heading {
				outlineCount++
			}
		}
	}
	imageStartID := 5 + len(pages)*2
	outlineRootID := imageStartID + len(charts)
	infoID := outlineRootID + outlineCount + 1
	objects := make([][]byte, 0, infoID)
	objects = append(objects,
		[]byte(fmt.Sprintf("<< /Type /Catalog /Pages 2 0 R /Outlines %d 0 R /PageMode /UseOutlines >>", outlineRootID)),
		[]byte(pdfPagesObject(len(pages))),
		[]byte("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>"),
		[]byte("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica-Bold >>"),
	)
	for index, pageLines := range pages {
		pageObjectID := 5 + index*2
		contentObjectID := pageObjectID + 1
		content := pdfPageContent(pageLines, profileLabel, index+1, len(pages), charts)
		var xobjects strings.Builder
		for chartIndex := range charts {
			fmt.Fprintf(&xobjects, " /Im%d %d 0 R", chartIndex+1, imageStartID+chartIndex)
		}
		objects = append(objects,
			[]byte(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %d %d] /Resources << /Font << /F1 3 0 R /F2 4 0 R >> /XObject <<%s >> >> /Contents %d 0 R >>", pdfPageWidth, pdfPageHeight, xobjects.String(), contentObjectID)),
			[]byte(fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content)),
		)
	}
	for _, chart := range charts {
		var object bytes.Buffer
		fmt.Fprintf(&object, "<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /FlateDecode /Length %d >>\nstream\n", chart.Width, chart.Height, len(chart.RGB))
		object.Write(chart.RGB)
		object.WriteString("\nendstream")
		objects = append(objects, object.Bytes())
	}
	if outlineCount == 0 {
		objects = append(objects, []byte("<< /Type /Outlines /Count 0 >>"))
	} else {
		objects = append(objects, []byte(fmt.Sprintf("<< /Type /Outlines /First %d 0 R /Last %d 0 R /Count %d >>", outlineRootID+1, outlineRootID+outlineCount, outlineCount)))
	}
	outlineIndex := 0
	for pageIndex, page := range pages {
		for _, line := range page {
			if !line.Heading {
				continue
			}
			id := outlineRootID + outlineIndex + 1
			var links string
			if outlineIndex > 0 {
				links += fmt.Sprintf(" /Prev %d 0 R", id-1)
			}
			if outlineIndex < outlineCount-1 {
				links += fmt.Sprintf(" /Next %d 0 R", id+1)
			}
			objects = append(objects, []byte(fmt.Sprintf("<< /Title (%s) /Parent %d 0 R /Dest [%d 0 R /FitH %d]%s >>", escapePDFText(line.Text), outlineRootID, 5+pageIndex*2, pdfPageTop, links)))
			outlineIndex++
		}
	}
	objects = append(objects, []byte(fmt.Sprintf("<< /Title (%s) /Author (APEX Load) /Creator (APEX Load) >>", escapePDFText(pdfASCIIText(lines[0])))))

	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects)+1)
	for index, object := range objects {
		objectID := index + 1
		offsets[objectID] = out.Len()
		fmt.Fprintf(&out, "%d 0 obj\n", objectID)
		out.Write(object)
		out.WriteString("\nendobj\n")
	}
	xrefOffset := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n", len(objects)+1)
	out.WriteString("0000000000 65535 f \n")
	for objectID := 1; objectID <= len(objects); objectID++ {
		fmt.Fprintf(&out, "%010d 00000 n \n", offsets[objectID])
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R /Info %d 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, infoID, xrefOffset)
	return out.Bytes()
}

func pdfPagesObject(pageCount int) string {
	kids := make([]string, 0, pageCount)
	for index := 0; index < pageCount; index++ {
		kids = append(kids, strconv.Itoa(5+index*2)+" 0 R")
	}
	return fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), pageCount)
}

func pdfPageContent(lines []pdfReportLine, profileLabel string, pageNumber, pageCount int, charts []reportDocumentChart) string {
	var out strings.Builder
	fmt.Fprintf(&out, "0.06 0.46 0.43 rg\nBT /F2 9 Tf %d 766 Td (APEX LOAD  /  %s) Tj ET\n", pdfPageLeft, escapePDFText(pdfASCIIText(strings.ToUpper(profileLabel))))
	fmt.Fprintf(&out, "0.82 0.86 0.90 RG 0.7 w %d 755 m 558 755 l S\n", pdfPageLeft)
	y := pdfPageTop - 16
	for _, line := range lines {
		if line.ChartID != "" {
			for index, chart := range charts {
				if chart.ID == line.ChartID {
					imageHeight := line.ChartHeight - 18
					fmt.Fprintf(&out, "q 504 0 0 %d %d %d cm /Im%d Do Q\n", imageHeight, pdfPageLeft, y-imageHeight, index+1)
					break
				}
			}
			y -= line.ChartHeight
			continue
		}
		if len(line.Cells) > 0 {
			if line.TableHeader {
				fmt.Fprintf(&out, "0.94 0.96 0.98 rg %d %d 504 18 re f\n", pdfPageLeft, y-5)
			}
			font := "F1"
			if line.TableHeader {
				font = "F2"
			}
			positions := []int{pdfPageLeft + 3, 210, 287, 356, 445}
			for index, cell := range line.Cells {
				if index >= len(positions) {
					break
				}
				fmt.Fprintf(&out, "0.10 0.15 0.21 rg BT /%s 9 Tf %d %d Td (%s) Tj ET\n", font, positions[index], y, escapePDFText(pdfASCIIText(cell)))
			}
			y -= pdfReportLineHeight(line)
			continue
		}
		if line.Text == "" {
			y -= 8
			continue
		}
		if strings.HasPrefix(line.Text, "Outcome: ") {
			fmt.Fprintf(&out, "0.91 0.97 0.95 rg %d %d 504 26 re f\n", pdfPageLeft, y-9)
			fmt.Fprintf(&out, "0.06 0.46 0.43 rg\nBT /F2 11 Tf %d %d Td (%s) Tj ET\n", pdfPageLeft+10, y, escapePDFText(line.Text))
			y -= pdfReportLineHeight(line)
			continue
		}
		font, size, color := "F1", 10, "0.12 0.16 0.20"
		if line.Title {
			font, size, color = "F2", 17, "0.06 0.46 0.43"
		} else if line.Heading {
			font, size, color = "F2", 12, "0.06 0.46 0.43"
		}
		fmt.Fprintf(&out, "%s rg\nBT /%s %d Tf %d %d Td (%s) Tj ET\n", color, font, size, pdfPageLeft, y, escapePDFText(line.Text))
		y -= pdfReportLineHeight(line)
	}
	fmt.Fprintf(&out, "0.82 0.86 0.90 RG 0.7 w %d 48 m 558 48 l S\n", pdfPageLeft)
	fmt.Fprintf(&out, "0.30 0.35 0.42 rg\nBT /F1 8 Tf %d 34 Td (APEX Load  /  %s) Tj ET\n", pdfPageLeft, escapePDFText(pdfASCIIText(profileLabel)))
	fmt.Fprintf(&out, "BT /F1 8 Tf 497 34 Td (Page %d of %d) Tj ET\n", pageNumber, pageCount)
	return out.String()
}

func pdfReportLineHeight(line pdfReportLine) int {
	if line.ChartID != "" {
		return line.ChartHeight
	}
	if len(line.Cells) > 0 {
		return 19
	}
	if line.Text == "" {
		return 8
	}
	if strings.HasPrefix(line.Text, "Outcome: ") {
		return 31
	}
	if line.Title {
		return 25
	}
	if line.Heading {
		return 19
	}
	return 13
}

func parseReportTableLine(line string) ([]string, bool) {
	line = strings.TrimSpace(line)
	if len(line) < 3 || line[0] != '|' || line[len(line)-1] != '|' {
		return nil, false
	}
	cells := strings.Split(line[1:len(line)-1], "|")
	if len(cells) < 2 {
		return nil, false
	}
	for index := range cells {
		cells[index] = strings.TrimSpace(cells[index])
	}
	return cells, true
}

func paginatePDFReportLines(lines []pdfReportLine) [][]pdfReportLine {
	const availableHeight = 664
	pages := [][]pdfReportLine{}
	page := []pdfReportLine{}
	used := 0
	for index, line := range lines {
		height := pdfReportLineHeight(line)
		keepWithNext := 0
		if line.Heading && index+1 < len(lines) {
			keepWithNext = pdfReportLineHeight(lines[index+1])
		}
		if len(page) > 0 && used+height+keepWithNext > availableHeight {
			pages = append(pages, page)
			page = nil
			used = 0
		}
		page = append(page, line)
		used += height
	}
	if len(page) > 0 {
		pages = append(pages, page)
	}
	for index := 1; index < len(pages); index++ {
		previous, current := pages[index-1], pages[index]
		if len(previous) == 0 || len(current) == 0 || current[0].Heading || current[0].Title {
			continue
		}
		start := len(previous) - 1
		for start > 0 && previous[start-1].Text != "" && !previous[start-1].Heading {
			start--
		}
		if start > 0 && previous[start-1].Heading {
			start--
		}
		if start == 0 {
			continue
		}
		movedHeight, currentHeight := 0, 0
		for _, line := range previous[start:] {
			movedHeight += pdfReportLineHeight(line)
		}
		for _, line := range current {
			currentHeight += pdfReportLineHeight(line)
		}
		if movedHeight+currentHeight <= availableHeight {
			pages[index] = append(append([]pdfReportLine{}, previous[start:]...), current...)
			pages[index-1] = previous[:start]
		}
	}
	// A nearly empty last page reads like a pagination mistake. Share its
	// preceding context only when the new page does not begin with a heading.
	if len(pages) > 1 && len(pages[len(pages)-1]) < 8 {
		previous := &pages[len(pages)-2]
		last := &pages[len(pages)-1]
		if len(*last) > 0 && !(*last)[0].Heading {
			lastHeight := 0
			for _, line := range *last {
				lastHeight += pdfReportLineHeight(line)
			}
			for len(*last) < 12 && len(*previous) > 12 {
				moved := (*previous)[len(*previous)-1]
				if moved.ChartID != "" || lastHeight+pdfReportLineHeight(moved) > availableHeight {
					break
				}
				*previous = (*previous)[:len(*previous)-1]
				*last = append([]pdfReportLine{moved}, *last...)
				lastHeight += pdfReportLineHeight(moved)
			}
		}
	}
	return pages
}

func wrapPDFLines(lines []string) []string {
	wrapped := make([]string, 0, len(lines))
	for _, line := range lines {
		line = pdfASCIIText(line)
		if line == "" {
			wrapped = append(wrapped, "")
			continue
		}
		for len(line) > pdfLineWrapWidth {
			breakAt := strings.LastIndex(line[:pdfLineWrapWidth], " ")
			if breakAt < 32 {
				breakAt = pdfLineWrapWidth
			}
			wrapped = append(wrapped, strings.TrimSpace(line[:breakAt]))
			line = strings.TrimSpace(line[breakAt:])
		}
		wrapped = append(wrapped, line)
	}
	return wrapped
}

func escapePDFText(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, "(", `\(`)
	value = strings.ReplaceAll(value, ")", `\)`)
	return value
}

// pdfASCIITransliterations maps common non-ASCII punctuation to ASCII so report
// CONTENT (transaction names, analyst verdict text, annotations — which often
// carry copy-pasted curly quotes, en/em-dashes, ellipses, or non-breaking
// spaces) renders sensibly in the ASCII-only PDF writer instead of as '?'.
var pdfASCIITransliterations = map[rune]string{
	'–': "-", '—': "-", '−': "-", // en/em dash, minus
	'‘': "'", '’': "'", '‚': "'", '′': "'", // single quotes / prime
	'“': "\"", '”': "\"", '„': "\"", '″': "\"", // double quotes
	'…': "...",           // ellipsis
	' ': " ",             // non-breaking space
	'•': "*",             // bullet
	'«': "<<", '»': ">>", // guillemets
	'≥': ">=", '≤': "<=", '÷': "/", '×': "x", // comparison and arithmetic signs
	// Latin letters NFD does not decompose.
	'ß': "ss", 'ẞ': "SS",
	'ø': "o", 'Ø': "O",
	'æ': "ae", 'Æ': "AE",
	'œ': "oe", 'Œ': "OE",
	'đ': "d", 'Đ': "D", 'ð': "d", 'Ð': "D",
	'ł': "l", 'Ł': "L",
	'þ': "th", 'Þ': "TH",
	'ı': "i",
}

func pdfASCIIText(value string) string {
	// Canonically decompose first (é -> e + combining acute) so accented Latin
	// letters fold to their base ASCII letter (the combining mark is dropped
	// below) instead of becoming '?'. Covers the common international-name case.
	value = norm.NFD.String(value)
	var out strings.Builder
	for _, r := range value {
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			out.WriteByte(' ')
		case r >= 32 && r <= 126:
			out.WriteRune(r)
		case unicode.Is(unicode.Mn, r):
			// combining mark from NFD decomposition; base letter already emitted
		default:
			if replacement, ok := pdfASCIITransliterations[r]; ok {
				out.WriteString(replacement)
			} else {
				out.WriteByte('?')
			}
		}
	}
	return strings.TrimRight(out.String(), " ")
}

func appendPDFValueLine(lines *[]string, label string, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	*lines = append(*lines, label+": "+value)
}

func formatPDFInt(value int64) string {
	return strconv.FormatInt(value, 10)
}

// formatPDFTime is a moment in RFC 3339 for the CSV, which stays machine
// readable; PDF lines use formatPDFDisplayTime.
func formatPDFTime(value time.Time) string {
	if value.IsZero() {
		return "n/a"
	}
	return value.UTC().Format(time.RFC3339)
}

// formatPDFDisplayTime states a moment in a PDF line as the other report
// formats do.
func formatPDFDisplayTime(value time.Time) string {
	if value.IsZero() {
		return "Not recorded"
	}
	return formatReportDateTime(value)
}

func firstNonEmptyPDFValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return "n/a"
}
