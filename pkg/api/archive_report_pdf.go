package api

import (
	"bytes"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
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
	profile, ok := lookupRunArchiveReportProfile(profileID)
	if !ok {
		return fmt.Errorf("unknown report profile %q", profileID)
	}
	if !runArchiveReportProfileSupportsFormat(profile, "pdf") {
		return fmt.Errorf("report profile %q does not support pdf export", profile.ID)
	}
	lines := buildRunArchiveReportPDFLines(report, profile)
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

func buildRunArchiveReportPDFLines(report *RunArchiveReport, profile RunArchiveReportProfile) []string {
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

	sections := stringSet(profile.IncludedSections)
	if hasCSVSection(sections, "overview") {
		lines = append(lines, "Overview")
		lines = append(lines,
			fmt.Sprintf("Transactions: %s", formatPDFInt(report.Overview.Transactions)),
			fmt.Sprintf("Errors: %s (%.2f%%)", formatPDFInt(report.Overview.Errors), report.Overview.ErrorPct),
			fmt.Sprintf("Average response: %.1f ms (min %.1f ms / max %.1f ms)", report.Overview.AvgResponseMs, report.Overview.MinResponseMs, report.Overview.MaxResponseMs),
			fmt.Sprintf("Peak TPS: %.1f", report.Overview.PeakTPS),
			fmt.Sprintf("Peak active VUsers: %s", formatPDFInt(report.Overview.PeakActiveVUsers)),
		)
		if report.Overview.PeakTargetTPS > 0 {
			lines = append(lines, fmt.Sprintf("Peak target TPS: %.1f", report.Overview.PeakTargetTPS))
		}
		if report.Overview.MaxThroughputGapPct > 0 || report.Overview.ThroughputMissedIntervals > 0 {
			lines = append(lines, fmt.Sprintf("Throughput gap: %.1f%% across %d missed interval(s)", report.Overview.MaxThroughputGapPct, report.Overview.ThroughputMissedIntervals))
		}
		lines = append(lines, "")
	}

	if hasCSVSection(sections, "completeness") || hasCSVSection(sections, "overview") {
		lines = append(lines, "Completeness")
		lines = append(lines, fmt.Sprintf("Score: %.0f%% (%s)", report.Completeness.ScorePct, report.Completeness.Status))
		for _, finding := range report.Completeness.Findings {
			lines = append(lines, fmt.Sprintf("- %s: %s", finding.Dimension, finding.Message))
		}
		lines = append(lines, "")
	}

	if hasCSVSection(sections, "evidence_tiers") {
		lines = append(lines, "Evidence Tiers")
		for _, tier := range report.EvidenceTiers {
			lines = append(lines, fmt.Sprintf("- %s: %s (%s)", firstNonEmptyPDFValue(tier.Label, tier.Tier), tier.Status, formatPDFInt(tier.Count)))
		}
		lines = append(lines, "")
	}

	if hasCSVSection(sections, "change_attribution") {
		lines = append(lines, "Change Attribution")
		attribution := report.ChangeAttribution
		appendPDFValueLine(&lines, "Scenario revision", attribution.ScenarioRevision)
		appendPDFValueLine(&lines, "Environment", attribution.Environment)
		appendPDFValueLine(&lines, "Data revision", attribution.DataRevision)
		appendPDFValueLine(&lines, "Script revisions", strings.Join(attribution.ScriptRevisions, ", "))
		appendPDFValueLine(&lines, "Generator versions", strings.Join(attribution.GeneratorVersions, ", "))
		if attribution.RuntimeOverrides {
			appendPDFValueLine(&lines, "Runtime overrides", strings.Join(attribution.RuntimeOverrideFields, ", "))
		}
		lines = append(lines, "")
	}

	if hasCSVSection(sections, "provenance") && report.Manifest != nil {
		lines = append(lines, "Run Identity and Authorization")
		provenance := report.Manifest.Provenance
		appendPDFValueLine(&lines, "Project", provenance.ProjectID)
		appendPDFValueLine(&lines, "Release", provenance.ReleaseID)
		appendPDFValueLine(&lines, "Scenario", provenance.ScenarioID)
		appendPDFValueLine(&lines, "Created by", actorPDFValue(provenance.CreatedByDisplayName, provenance.CreatedBySubject))
		appendPDFValueLine(&lines, "Started by", actorPDFValue(provenance.StartedByDisplayName, provenance.StartedBySubject))
		appendPDFValueLine(&lines, "Stopped by", actorPDFValue(provenance.StoppedByDisplayName, provenance.StoppedBySubject))
		appendPDFValueLine(&lines, "Scheduled start", formatPDFTimePtr(provenance.ScheduledStartAt))
		if provenance.Governance != nil {
			appendPDFValueLine(&lines, "Capacity reservation", provenance.Governance.CapacityReservationID)
			if provenance.Governance.LaunchPreflight != nil {
				appendPDFValueLine(&lines, "Launch preflight", firstNonEmptyPDFValue(provenance.Governance.LaunchPreflight.Status, provenance.Governance.LaunchPreflight.Decision))
			}
			if approval := provenance.Governance.ExecutionPlanApproval; approval != nil {
				appendPDFValueLine(&lines, "Execution-plan approval", strings.TrimSpace(approval.ID+" "+approval.Status))
				appendPDFValueLine(&lines, "Approval policy", approval.Policy)
				appendPDFValueLine(&lines, "Approval requested by", approval.RequestedBy)
				appendPDFValueLine(&lines, "Approval resolved by", approval.ResolvedBy)
				appendPDFValueLine(&lines, "Approval reason", approval.DecisionReason)
				appendPDFValueLine(&lines, "Required role", approval.RequiredRole)
			}
		}
		lines = append(lines, "")
	}

	if hasCSVSection(sections, "schema_compatibility") {
		lines = append(lines, "Schema Compatibility")
		lines = append(lines,
			"Status: "+report.Compatibility.Status,
			"Report schema: "+report.Compatibility.ReportSchemaVersion,
			"Manifest schema: "+report.Compatibility.ManifestSchemaVersion,
		)
		for _, issue := range report.Compatibility.Issues {
			lines = append(lines, fmt.Sprintf("- %s: %s", issue.Severity, issue.Message))
		}
		lines = append(lines, "")
	}

	if hasCSVSection(sections, "artifact_inventory") {
		lines = append(lines, "Artifact Integrity")
		for _, artifact := range report.ArtifactInventory {
			hash := artifact.SHA256
			if hash == "" {
				hash = "not captured"
			}
			lines = append(lines, fmt.Sprintf("- %s: %s %s, %s record(s), %d bytes, sha256=%s",
				artifact.Name,
				artifact.RecordType,
				artifact.Format,
				formatPDFInt(artifact.Count),
				artifact.SizeBytes,
				hash,
			))
		}
		if len(report.ArtifactInventory) == 0 {
			lines = append(lines, "No artifact inventory was available.")
		}
		lines = append(lines, "")
	}

	if hasCSVSection(sections, "actions") {
		appendPDFStatsSection(&lines, "Top Actions", report.ByAction, 12)
		if len(report.BySOAPOperation) > 0 {
			appendPDFStatsSection(&lines, "SOAP Operations", report.BySOAPOperation, 8)
		}
	}

	if hasCSVSection(sections, "summary_timeline") && len(report.SummaryTimeline) > 0 {
		lines = append(lines, "Summary Timeline")
		limit := len(report.SummaryTimeline)
		if limit > 10 {
			limit = 10
		}
		for _, point := range report.SummaryTimeline[:limit] {
			lines = append(lines, fmt.Sprintf("- %s: %s VUsers, %.1f TPS, p95 %.1f ms, %.2f%% errors",
				point.Timestamp.Format(time.RFC3339),
				formatPDFInt(point.ActiveVUsers),
				point.TPS,
				point.P95Ms,
				point.ErrorPct,
			))
		}
		if len(report.SummaryTimeline) > limit {
			lines = append(lines, fmt.Sprintf("... %d additional interval(s) omitted from the PDF profile.", len(report.SummaryTimeline)-limit))
		}
		lines = append(lines, "")
	}

	if hasCSVSection(sections, "browser") && report.Overview.Browser != nil {
		browser := report.Overview.Browser
		lines = append(lines, "Browser Metrics")
		lines = append(lines,
			fmt.Sprintf("Samples: %s", formatPDFInt(int64(browser.Samples))),
			fmt.Sprintf("Average FCP: %.1f ms", browser.AvgFCPMs),
			fmt.Sprintf("Average LCP: %.1f ms", browser.AvgLCPMs),
			fmt.Sprintf("Average CLS: %.3f", browser.AvgCLSScore),
			fmt.Sprintf("Average INP: %.1f ms", browser.AvgINPMs),
		)
		lines = append(lines, "")
	}

	if hasCSVSection(sections, "resources") {
		lines = append(lines, "Resource Peaks")
		lines = append(lines,
			fmt.Sprintf("CPU process: %.1f%%", report.ResourcePeaks.CPUProcessPct),
			fmt.Sprintf("Memory process: %.1f MB", report.ResourcePeaks.MemProcessMB),
			fmt.Sprintf("Network sent/received: %.1f / %.1f Kbps", report.ResourcePeaks.NetSentKbps, report.ResourcePeaks.NetRecvKbps),
		)
		lines = append(lines, "")
	}

	if hasCSVSection(sections, "errors") && len(report.ErrorPatterns) > 0 {
		lines = append(lines, "Error Patterns")
		for _, pattern := range report.ErrorPatterns {
			lines = append(lines, fmt.Sprintf("- %s (%d occurrence(s))", pattern.Sample, pattern.Count))
		}
		lines = append(lines, "")
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

func pdfASCIIText(value string) string {
	var out strings.Builder
	for _, r := range value {
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			out.WriteByte(' ')
		case r >= 32 && r <= 126:
			out.WriteRune(r)
		default:
			out.WriteByte('?')
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
