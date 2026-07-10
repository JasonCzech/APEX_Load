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
	pdfPageWidth      = 612
	pdfPageHeight     = 792
	pdfLineWrapWidth  = 92
	pdfLinesPerPage   = 48
	pdfLineHeight     = 13
	pdfPageTop        = 748
	pdfPageLeft       = 54
	pdfContentFontRef = 3
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
	_, err := writer.Write(renderTextPDF(lines))
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
	lines := []string{
		"APEX Load Archive Report",
		"Profile: " + profile.Label,
		"Generated: " + time.Now().UTC().Format(time.RFC3339),
		"",
	}
	if report == nil {
		return append(lines, "No archived report evidence is available.")
	}

	if report.Manifest != nil {
		lines = append(lines,
			"Run: "+firstNonEmptyPDFValue(report.Manifest.TestName, report.Manifest.TestID),
			"Test ID: "+report.Manifest.TestID,
			"Completed: "+formatPDFTime(report.Manifest.CompletedAt),
			"",
		)
	}

	data := buildRunArchiveReportRenderData(report, profile, annotations, manifest)
	for _, section := range reportSectionsForProfile(profile) {
		section.PDF(&data, &lines)
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
	*lines = append(*lines, "Release Trend")
	*lines = append(*lines, fmt.Sprintf("Release: %s (%d archived run(s))", firstNonEmptyPDFValue(trend.ReleaseID), trend.RunCount))
	if trend.HasBaseline && trend.Baseline != nil && trend.Deltas != nil {
		baseline := trend.Baseline
		*lines = append(*lines,
			fmt.Sprintf("Baseline (prior run): %s, completed %s", firstNonEmptyPDFValue(baseline.RunName, baseline.RunID), formatPDFTime(baseline.CompletedAt)),
			fmt.Sprintf("Error rate: %.2f%% -> %.2f%% (%+.2f pp)", baseline.ErrorPct, trend.Current.ErrorPct, trend.Deltas.ErrorPctDelta),
			fmt.Sprintf("Avg response: %.1f ms -> %.1f ms (%+.1f ms / %+.1f%%)", baseline.AvgResponseMs, trend.Current.AvgResponseMs, trend.Deltas.AvgResponseMsDelta, trend.Deltas.AvgResponsePctDelta),
			fmt.Sprintf("Peak TPS: %.1f -> %.1f (%+.1f)", baseline.PeakTPS, trend.Current.PeakTPS, trend.Deltas.PeakTPSDelta),
			fmt.Sprintf("Completeness: %.0f%% -> %.0f%% (%+.1f pts)", baseline.CompletenessPct, trend.Current.CompletenessPct, trend.Deltas.CompletenessDelta),
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
		*lines = append(*lines, fmt.Sprintf("- %s: %s tx, %s errors (%.2f%%), avg %.1f ms, max %.1f ms",
			name,
			formatPDFInt(stat.Transactions),
			formatPDFInt(stat.Errors),
			stat.ErrorPct,
			stat.AvgResponseMs,
			stat.MaxResponseMs,
		))
	}
	*lines = append(*lines, "")
}

func renderTextPDF(lines []string) []byte {
	wrappedLines := wrapPDFLines(lines)
	if len(wrappedLines) == 0 {
		wrappedLines = []string{""}
	}
	pages := paginatePDFLines(wrappedLines)

	objects := make([][]byte, 0, 3+len(pages)*2)
	objects = append(objects,
		[]byte("<< /Type /Catalog /Pages 2 0 R >>"),
		[]byte(pdfPagesObject(len(pages))),
		[]byte("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>"),
	)
	for index, pageLines := range pages {
		pageObjectID := 4 + index*2
		contentObjectID := pageObjectID + 1
		content := pdfPageContent(pageLines)
		objects = append(objects,
			[]byte(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %d %d] /Resources << /Font << /F1 %d 0 R >> >> /Contents %d 0 R >>", pdfPageWidth, pdfPageHeight, pdfContentFontRef, contentObjectID)),
			[]byte(fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content)),
		)
	}

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
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xrefOffset)
	return out.Bytes()
}

func pdfPagesObject(pageCount int) string {
	kids := make([]string, 0, pageCount)
	for index := 0; index < pageCount; index++ {
		kids = append(kids, strconv.Itoa(4+index*2)+" 0 R")
	}
	return fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), pageCount)
}

func pdfPageContent(lines []string) string {
	var out strings.Builder
	fmt.Fprintf(&out, "BT\n/F1 10 Tf\n%d %d Td\n%d TL\n", pdfPageLeft, pdfPageTop, pdfLineHeight)
	for _, line := range lines {
		if line != "" {
			out.WriteString("(")
			out.WriteString(escapePDFText(line))
			out.WriteString(") Tj\n")
		}
		out.WriteString("T*\n")
	}
	out.WriteString("ET")
	return out.String()
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

func paginatePDFLines(lines []string) [][]string {
	pages := make([][]string, 0, (len(lines)/pdfLinesPerPage)+1)
	for start := 0; start < len(lines); start += pdfLinesPerPage {
		end := start + pdfLinesPerPage
		if end > len(lines) {
			end = len(lines)
		}
		pages = append(pages, lines[start:end])
	}
	return pages
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
	'…': "...",     // ellipsis
	' ': " ",       // non-breaking space
	'•': "*",       // bullet
	'«': "<<", '»': ">>", // guillemets
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

func formatPDFTime(value time.Time) string {
	if value.IsZero() {
		return "n/a"
	}
	return value.UTC().Format(time.RFC3339)
}

func firstNonEmptyPDFValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return "n/a"
}
