package api

import (
	"bytes"
	"fmt"
	"html/template"
	"sort"
	"strings"
)

// reportCSVWriteFunc writes a single section/key/metric/value CSV row.
type reportCSVWriteFunc func(section, key, metric, value string) error

// reportSectionRenderer is one entry in the shared report section registry. A
// single registry maps a profile-declared section id to the renderer fragments
// for every output format, so the profile metadata (IncludedSections +
// SectionOrder) is the single source of truth for what each format emits and in
// what order. Every registered section MUST supply all three renderers; the
// coverage matrix test (TestRunArchiveReportProfilesAreConsistentAndRenderable)
// fails the build when a profile declares a section that is missing a renderer.
type reportSectionRenderer struct {
	ID    string
	Label string
	// HTML renders the section body fragment (without the outer <section>
	// wrapper, which the shell template adds along with the data-report-section
	// marker). It always emits at least a heading so the section is never blank.
	HTML func(d *runArchiveHTMLReportData) (template.HTML, error)
	// PDF appends the section's text lines (always including a heading line).
	PDF func(d *runArchiveHTMLReportData, lines *[]string)
	// CSV writes the section's rows (always at least one row, falling back to a
	// graceful note so the section is represented even without backing data).
	CSV func(d *runArchiveHTMLReportData, write reportCSVWriteFunc) error
}

// renderedReportSection is a section body already rendered to HTML, ready for
// the shell template to place in profile order.
type renderedReportSection struct {
	ID    string
	Label string
	Body  template.HTML
}

// reportShellData is the top-level context for the unified report shell.
type reportShellData struct {
	Data     *runArchiveHTMLReportData
	Sections []renderedReportSection
}

// reportSectionRegistry holds every renderable report section keyed by its
// canonical (profile-declared) id. It is the single source of truth shared by
// the HTML, PDF, and CSV renderers.
var reportSectionRegistry = buildReportSectionRegistry()

// reportSectionsForProfile returns the registered section renderers a profile
// declares, in SectionOrder, gated by IncludedSections. Sections that are
// declared but not registered are skipped (never fail-open to another profile's
// content); the coverage matrix test guarantees no shipped profile hits that
// path. SectionOrder falls back to IncludedSections when empty.
func reportSectionsForProfile(profile RunArchiveReportProfile) []reportSectionRenderer {
	included := reportSectionSet(profile.IncludedSections)
	order := profile.SectionOrder
	if len(order) == 0 {
		order = profile.IncludedSections
	}
	sections := make([]reportSectionRenderer, 0, len(order))
	seen := make(map[string]struct{}, len(order))
	for _, id := range order {
		if _, ok := included[id]; !ok {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		if renderer, ok := reportSectionRegistry[id]; ok {
			sections = append(sections, renderer)
		}
	}
	return sections
}

// reportProfileSectionsMissingRenderers returns, for a profile, the declared
// section ids that have no registry entry. An empty result means the profile is
// fully renderable. Used by the coverage matrix test.
func reportProfileSectionsMissingRenderers(profile RunArchiveReportProfile) []string {
	var missing []string
	for _, id := range profile.IncludedSections {
		if _, ok := reportSectionRegistry[id]; !ok {
			missing = append(missing, id)
		}
	}
	return missing
}

func reportSectionSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

// htmlSectionRenderer returns an HTML renderer that executes the named fragment
// template against the shared render data.
func htmlSectionRenderer(name string) func(*runArchiveHTMLReportData) (template.HTML, error) {
	return func(d *runArchiveHTMLReportData) (template.HTML, error) {
		var buf bytes.Buffer
		if err := reportSectionTemplates.ExecuteTemplate(&buf, name, d); err != nil {
			return "", err
		}
		return template.HTML(buf.String()), nil
	}
}

// pdfHeading appends a section heading (and ensures a trailing blank line is the
// caller's responsibility).
func pdfHeading(lines *[]string, heading string) {
	*lines = append(*lines, heading)
}

func csvNote(write reportCSVWriteFunc, section, note string) error {
	return write(section, "", "note", note)
}

// csvFindingsRows writes auto/curated findings as CSV rows.
func csvFindingsRows(write reportCSVWriteFunc, section string, findings []ReportFinding) error {
	if len(findings) == 0 {
		return csvNote(write, section, "No material findings were derived from the archive.")
	}
	for _, finding := range findings {
		key := strings.TrimSpace(finding.ID)
		if key == "" {
			key = finding.Title
		}
		rows := [][2]string{
			{"title", finding.Title},
			{"severity", finding.Severity},
			{"observed", finding.Observed},
			{"evidence", finding.Evidence},
			{"root_cause", finding.RootCause},
			{"impact", finding.Impact},
		}
		for _, row := range rows {
			if strings.TrimSpace(row[1]) == "" {
				continue
			}
			if err := write(section, key, row[0], row[1]); err != nil {
				return err
			}
		}
	}
	return nil
}

// csvSLARows writes the SLA compliance table as CSV rows.
func csvSLARows(write reportCSVWriteFunc, section string, rows []SLAComplianceRow) error {
	if len(rows) == 0 {
		return csvNote(write, section, "No per-transaction SLA evidence was archived.")
	}
	for _, row := range rows {
		sla := "n/a"
		if row.HasSLA {
			sla = formatCSVFloat(row.SLAMs)
		}
		entries := [][2]string{
			{"volume", formatCSVInt(row.Volume)},
			{"p95_ms", formatCSVFloat(row.P95Ms)},
			{"sla_ms", sla},
			{"error_pct", formatCSVFloat(row.ErrorPct)},
			{"result", row.Result},
		}
		for _, entry := range entries {
			if err := write(section, row.Name, entry[0], entry[1]); err != nil {
				return err
			}
		}
	}
	return nil
}

func csvTransactionSLARules(write reportCSVWriteFunc, section string, results []TransactionSLARuleResult) error {
	for _, result := range results {
		observed := "n/a"
		if result.Observed != nil {
			observed = formatCSVFloat(*result.Observed)
		}
		entries := [][2]string{
			{"metric", result.Rule.Metric},
			{"comparator", result.Rule.Comparator},
			{"threshold", formatCSVFloat(result.Rule.Threshold)},
			{"observed", observed},
			{"severity", firstNonEmptyReportText(result.Rule.Severity, "fail")},
			{"status", result.Status},
			{"matched_count", formatCSVInt(result.MatchedCount)},
			{"message", result.Message},
		}
		for _, entry := range entries {
			if err := write(section, result.Rule.Transaction, "transaction_rule_"+entry[0], entry[1]); err != nil {
				return err
			}
		}
	}
	return nil
}

// pdfFindingsLines appends findings as PDF lines.
func pdfFindingsLines(lines *[]string, findings []ReportFinding) {
	if len(findings) == 0 {
		*lines = append(*lines, "No material findings were derived from the archive.")
		return
	}
	for _, finding := range findings {
		id := strings.TrimSpace(finding.ID)
		if id != "" {
			id += " "
		}
		*lines = append(*lines, fmt.Sprintf("- %s%s [%s]", id, finding.Title, finding.Severity))
		if detail := firstNonEmptyReportText(finding.Impact, finding.Observed, finding.Evidence); detail != "" {
			*lines = append(*lines, "  "+detail)
		}
	}
}

// pdfSLALines appends the SLA compliance table as PDF lines.
func pdfSLALines(lines *[]string, rows []SLAComplianceRow) {
	if len(rows) == 0 {
		*lines = append(*lines, "No per-transaction SLA evidence was archived.")
		return
	}
	for _, row := range rows {
		sla := "n/a"
		if row.HasSLA {
			sla = fmt.Sprintf("%.0f ms", row.SLAMs)
		}
		*lines = append(*lines, fmt.Sprintf("- %s: %s tx, p95 %.0f ms, SLA %s, %.2f%% errors - %s",
			row.Name, formatPDFInt(row.Volume), row.P95Ms, sla, row.ErrorPct, row.Result))
	}
}

func pdfTransactionSLARuleLines(lines *[]string, results []TransactionSLARuleResult) {
	if len(results) == 0 {
		return
	}
	*lines = append(*lines, "Exact transaction terminal rules")
	for _, result := range results {
		observed := "n/a"
		if result.Observed != nil {
			observed = fmt.Sprintf("%.6g", *result.Observed)
		}
		*lines = append(*lines, fmt.Sprintf("- %s / %s: observed %s, expected %s %.6g [%s] - %s",
			result.Rule.Transaction, result.Rule.Metric, observed, result.Rule.Comparator, result.Rule.Threshold,
			firstNonEmptyReportText(result.Rule.Severity, "fail"), result.Status))
	}
}

func firstNonEmptyReportText(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// buildReportSectionRegistry constructs the registry. Each entry ports the
// markup/lines from the previously-bespoke per-profile renderers into a single
// shared, profile-agnostic fragment.
func buildReportSectionRegistry() map[string]reportSectionRenderer {
	reg := map[string]reportSectionRenderer{}
	add := func(r reportSectionRenderer) {
		r.HTML = htmlSectionRenderer(r.ID)
		reg[r.ID] = r
	}

	// --- Verdict / outcome -------------------------------------------------
	add(reportSectionRenderer{ID: "verdict", Label: "Verdict",
		PDF: func(d *runArchiveHTMLReportData, lines *[]string) {
			pdfHeading(lines, "Verdict")
			*lines = append(*lines, fmt.Sprintf("%s - %s", d.Verdict.Status, d.Verdict.Explanation), "")
		},
		CSV: func(d *runArchiveHTMLReportData, write reportCSVWriteFunc) error {
			if err := write("verdict", "", "status", d.Verdict.Status); err != nil {
				return err
			}
			return write("verdict", "", "explanation", d.Verdict.Explanation)
		}})
	add(reportSectionRenderer{ID: "official_outcome", Label: "Official Outcome",
		PDF: func(d *runArchiveHTMLReportData, lines *[]string) {
			pdfHeading(lines, "Official Outcome")
			*lines = append(*lines, fmt.Sprintf("%s - %s", d.Verdict.Status, d.Verdict.Explanation), "")
		},
		CSV: func(d *runArchiveHTMLReportData, write reportCSVWriteFunc) error {
			if err := write("official_outcome", "", "status", d.Verdict.Status); err != nil {
				return err
			}
			return write("official_outcome", "", "explanation", d.Verdict.Explanation)
		}})

	// --- Overviews ---------------------------------------------------------
	overviewPDF := func(heading string) func(*runArchiveHTMLReportData, *[]string) {
		return func(d *runArchiveHTMLReportData, lines *[]string) {
			report := d.Report
			pdfHeading(lines, heading)
			if report.Manifest != nil {
				reason := report.Manifest.TerminationReason
				if reason == "" {
					reason = "Unknown"
				}
				*lines = append(*lines, "Execution: "+reportExecutionLabel(reason), "Planned duration: "+report.Manifest.Provenance.Duration, "Elapsed: "+reportElapsedDuration(report.Manifest), "Evidence coverage applies to the captured period; workload completion is assessed separately. Population average rates use elapsed execution time, including pauses and idle time.")
			}
			for _, key := range []string{"action", "transaction", "unknown"} {
				if population, ok := report.MetricPopulations[key]; ok && population.Transactions > 0 {
					*lines = append(*lines, fmt.Sprintf("%s: %d records, %d errors, p95 %.1f ms, average %s/s", reportPopulationLabel(key), population.Transactions, population.Errors, population.P95Ms, reportPopulationRate(population.AverageRate)))
				}
			}
			*lines = append(*lines,
				fmt.Sprintf("Archive records: %s", formatPDFInt(report.Overview.Transactions)),
				fmt.Sprintf("Archive record errors: %s (%.2f%%)", formatPDFInt(report.Overview.Errors), report.Overview.ErrorPct),
				fmt.Sprintf("Archive record average: %.1f ms (min %.1f ms / max %.1f ms)", report.Overview.AvgResponseMs, report.Overview.MinResponseMs, report.Overview.MaxResponseMs),
				fmt.Sprintf("Peak successful transaction TPS: %.1f", report.Overview.PeakTPS),
				fmt.Sprintf("Peak active VUsers: %s", formatPDFInt(report.Overview.PeakActiveVUsers)),
			)
			if report.Overview.PeakTargetTPS > 0 {
				*lines = append(*lines, fmt.Sprintf("Peak target TPS: %.1f", report.Overview.PeakTargetTPS))
			}
			*lines = append(*lines, "")
		}
	}
	overviewCSV := func(section string) func(*runArchiveHTMLReportData, reportCSVWriteFunc) error {
		return func(d *runArchiveHTMLReportData, write reportCSVWriteFunc) error {
			report := d.Report
			if err := write(section, "", "schema_version", report.SchemaVersion); err != nil {
				return err
			}
			for _, key := range []string{"action", "transaction", "unknown"} {
				if population, ok := report.MetricPopulations[key]; ok {
					for _, item := range [][2]string{{"count", formatCSVInt(population.Transactions)}, {"errors", formatCSVInt(population.Errors)}, {"error_pct", formatCSVFloat(population.ErrorPct)}, {"p95_ms", formatCSVFloat(population.P95Ms)}, {"average_rate_per_second", reportPopulationRate(population.AverageRate)}} {
						if err := write("metric_populations", key, item[0], item[1]); err != nil {
							return err
						}
					}
				}
			}
			rows := [][2]string{
				{"population", "archive_records"},
				{"transactions", formatCSVInt(report.Overview.Transactions)},
				{"errors", formatCSVInt(report.Overview.Errors)},
				{"error_pct", formatCSVFloat(report.Overview.ErrorPct)},
				{"avg_response_ms", formatCSVFloat(report.Overview.AvgResponseMs)},
				{"min_response_ms", formatCSVFloat(report.Overview.MinResponseMs)},
				{"max_response_ms", formatCSVFloat(report.Overview.MaxResponseMs)},
				{"peak_tps", formatCSVFloat(report.Overview.PeakTPS)},
				{"peak_active_vusers", formatCSVInt(report.Overview.PeakActiveVUsers)},
				{"completeness_score_pct", formatCSVFloat(report.Completeness.ScorePct)},
				{"completeness_status", report.Completeness.Status},
			}
			for _, row := range rows {
				if err := write(section, "", row[0], row[1]); err != nil {
					return err
				}
			}
			return nil
		}
	}
	add(reportSectionRenderer{ID: "executive_overview", Label: "Executive Overview",
		PDF: overviewPDF("Overview"), CSV: overviewCSV("overview")})
	add(reportSectionRenderer{ID: "technical_overview", Label: "Run at a Glance",
		PDF: overviewPDF("Overview"), CSV: overviewCSV("overview")})

	// --- SLA ---------------------------------------------------------------
	add(reportSectionRenderer{ID: "sla_rollup", Label: "SLA Scorecard",
		PDF: func(d *runArchiveHTMLReportData, lines *[]string) {
			pdfHeading(lines, "SLA Scorecard")
			pdfSLALines(lines, d.SLARows)
			pdfTransactionSLARuleLines(lines, d.TransactionRuleResults)
			*lines = append(*lines, "")
		},
		CSV: func(d *runArchiveHTMLReportData, write reportCSVWriteFunc) error {
			if err := csvSLARows(write, "sla", d.SLARows); err != nil {
				return err
			}
			return csvTransactionSLARules(write, "sla", d.TransactionRuleResults)
		}})
	add(reportSectionRenderer{ID: "sla_detail", Label: "SLA Compliance",
		PDF: func(d *runArchiveHTMLReportData, lines *[]string) {
			pdfHeading(lines, "SLA Compliance")
			pdfSLALines(lines, d.SLARows)
			pdfTransactionSLARuleLines(lines, d.TransactionRuleResults)
			*lines = append(*lines, "")
		},
		CSV: func(d *runArchiveHTMLReportData, write reportCSVWriteFunc) error {
			if err := csvSLARows(write, "sla", d.SLARows); err != nil {
				return err
			}
			return csvTransactionSLARules(write, "sla", d.TransactionRuleResults)
		}})

	// --- Performance graphs ------------------------------------------------
	add(reportSectionRenderer{ID: "graphs", Label: "Performance Analysis",
		PDF: func(d *runArchiveHTMLReportData, lines *[]string) {
			pdfHeading(lines, "Performance Analysis")
			*lines = append(*lines, "Time-series graphs are available in the HTML report and live dashboard.", "")
		},
		CSV: func(d *runArchiveHTMLReportData, write reportCSVWriteFunc) error {
			return csvNote(write, "graphs", "Time-series graphs are available in the HTML report; see summary_timeline rows for the underlying data.")
		}})

	add(reportSectionRenderer{ID: "dashboard_panels", Label: "Imported Dashboard Panels",
		PDF: dashboardPanelsPDF,
		CSV: dashboardPanelsCSV})

	// --- Risks / findings / root cause ------------------------------------
	add(reportSectionRenderer{ID: "risk_summary", Label: "Top Risks",
		PDF: func(d *runArchiveHTMLReportData, lines *[]string) {
			pdfHeading(lines, "Top Risks")
			pdfFindingsLines(lines, d.Findings)
			*lines = append(*lines, "")
		},
		CSV: func(d *runArchiveHTMLReportData, write reportCSVWriteFunc) error {
			return csvFindingsRows(write, "risk_summary", d.Findings)
		}})
	add(reportSectionRenderer{ID: "root_cause", Label: "Findings & Root Cause",
		PDF: func(d *runArchiveHTMLReportData, lines *[]string) {
			pdfHeading(lines, "Findings")
			pdfFindingsLines(lines, d.Findings)
			*lines = append(*lines, "")
		},
		CSV: func(d *runArchiveHTMLReportData, write reportCSVWriteFunc) error {
			return csvFindingsRows(write, "findings", d.Findings)
		}})

	// --- Recommendations / sign-off ---------------------------------------
	add(reportSectionRenderer{ID: "recommendations", Label: "Recommendations",
		PDF: func(d *runArchiveHTMLReportData, lines *[]string) {
			pdfHeading(lines, "Recommendations")
			text := strings.TrimSpace(d.Annotations.SectionComments["recommendations"])
			if text == "" {
				text = "Document recommended follow-up actions, owners, and retest scope here."
			}
			*lines = append(*lines, text, "")
		},
		CSV: func(d *runArchiveHTMLReportData, write reportCSVWriteFunc) error {
			text := strings.TrimSpace(d.Annotations.SectionComments["recommendations"])
			if text == "" {
				return csvNote(write, "recommendations", "No recommendations were recorded.")
			}
			return write("recommendations", "", "recommendation", text)
		}})
	add(reportSectionRenderer{ID: "signoff", Label: "Recommendation & Sign-Off",
		PDF: func(d *runArchiveHTMLReportData, lines *[]string) {
			pdfHeading(lines, "Sign-Off")
			*lines = append(*lines, "Prepared by: "+reportProvidedValue(d.Annotations.PreparedBy), "Analyst assessment: "+reportProvidedValue(d.Annotations.VerdictOverride), "Rationale: "+reportProvidedValue(d.Annotations.VerdictExplanation), "Assessment author: "+reportProvidedValue(d.Annotations.UpdatedBy), "")
		},
		CSV: func(d *runArchiveHTMLReportData, write reportCSVWriteFunc) error {
			prepared := strings.TrimSpace(d.Annotations.PreparedBy)
			if prepared == "" {
				prepared = "n/a"
			}
			return write("signoff", "", "prepared_by", prepared)
		}})

	// --- Release trend -----------------------------------------------------
	// The compact release-over-release trend (report.ReleaseTrend) is attached at
	// the storage-aware handler layer ((*Server).attachReleaseTrend); the section
	// degrades to a no-release note when it is nil.
	add(reportSectionRenderer{ID: "release_trend", Label: "Release Trend",
		PDF: func(d *runArchiveHTMLReportData, lines *[]string) {
			if d.Report.ReleaseTrend == nil {
				pdfHeading(lines, "Release Trend")
				*lines = append(*lines, "This run is not associated with a release, so release-over-release trend analysis is unavailable.", "")
				return
			}
			appendReleaseTrendPDFLines(lines, d.Report.ReleaseTrend)
		},
		CSV: func(d *runArchiveHTMLReportData, write reportCSVWriteFunc) error {
			if d.Report.ReleaseTrend == nil {
				return csvNote(write, "release_trend", "This run is not associated with a release; no trend available.")
			}
			return writeReleaseTrendSummaryCSVRows(write, d.Report.ReleaseTrend)
		}})

	// --- Run context & confidence (executive completeness + change context) -
	add(reportSectionRenderer{ID: "run_context", Label: "Run Context & Confidence",
		PDF: func(d *runArchiveHTMLReportData, lines *[]string) {
			pdfHeading(lines, "Run Context & Confidence")
			*lines = append(*lines,
				fmt.Sprintf("Data completeness: %.0f%% (%s)", d.Report.Completeness.ScorePct, d.Report.Completeness.Status),
			)
			appendPDFValueLine(lines, "Environment", reportEnvironment(d.Report))
			if d.Report.ChangeAttribution.RuntimeOverrides {
				appendPDFValueLine(lines, "Runtime overrides", strings.Join(d.Report.ChangeAttribution.RuntimeOverrideFields, ", "))
			} else {
				*lines = append(*lines, "Runtime overrides: none")
			}
			*lines = append(*lines, "")
		},
		CSV: func(d *runArchiveHTMLReportData, write reportCSVWriteFunc) error {
			if err := write("run_context", "", "completeness_score_pct", formatCSVFloat(d.Report.Completeness.ScorePct)); err != nil {
				return err
			}
			if err := write("run_context", "", "completeness_status", d.Report.Completeness.Status); err != nil {
				return err
			}
			return writeChangeAttributionCSVRows(write, d.Report.ChangeAttribution)
		}})

	// --- Completeness ------------------------------------------------------
	add(reportSectionRenderer{ID: "completeness", Label: "Completeness Score",
		PDF: func(d *runArchiveHTMLReportData, lines *[]string) {
			pdfHeading(lines, "Completeness")
			*lines = append(*lines, fmt.Sprintf("Score: %.0f%% (%s)", d.Report.Completeness.ScorePct, d.Report.Completeness.Status))
			for _, finding := range d.Report.Completeness.Findings {
				*lines = append(*lines, fmt.Sprintf("- %s: %s", finding.Dimension, finding.Message))
			}
			*lines = append(*lines, "")
		},
		CSV: func(d *runArchiveHTMLReportData, write reportCSVWriteFunc) error {
			if err := write("completeness", "", "score_pct", formatCSVFloat(d.Report.Completeness.ScorePct)); err != nil {
				return err
			}
			if err := write("completeness", "", "status", d.Report.Completeness.Status); err != nil {
				return err
			}
			for _, finding := range d.Report.Completeness.Findings {
				if err := write("completeness_finding", finding.Dimension, "severity", finding.Severity); err != nil {
					return err
				}
				if err := write("completeness_finding", finding.Dimension, "message", finding.Message); err != nil {
					return err
				}
			}
			return nil
		}})

	// --- Change attribution ------------------------------------------------
	add(reportSectionRenderer{ID: "change_attribution", Label: "Change Attribution",
		PDF: func(d *runArchiveHTMLReportData, lines *[]string) {
			pdfHeading(lines, "Change Attribution")
			attribution := d.Report.ChangeAttribution
			appendPDFValueLine(lines, "Scenario revision", attribution.ScenarioRevision)
			appendPDFValueLine(lines, "Environment", reportEnvironment(d.Report))
			appendPDFValueLine(lines, "Data revision", attribution.DataRevision)
			appendPDFValueLine(lines, "Script revisions", strings.Join(attribution.ScriptRevisions, ", "))
			appendPDFValueLine(lines, "Generator versions", strings.Join(attribution.GeneratorVersions, ", "))
			if attribution.RuntimeOverrides {
				appendPDFValueLine(lines, "Runtime overrides", strings.Join(attribution.RuntimeOverrideFields, ", "))
			}
			*lines = append(*lines, "")
		},
		CSV: func(d *runArchiveHTMLReportData, write reportCSVWriteFunc) error {
			return writeChangeAttributionCSVRows(write, d.Report.ChangeAttribution)
		}})

	// --- Schema compatibility ---------------------------------------------
	add(reportSectionRenderer{ID: "schema_compatibility", Label: "Schema Compatibility",
		PDF: func(d *runArchiveHTMLReportData, lines *[]string) {
			pdfHeading(lines, "Schema Compatibility")
			*lines = append(*lines,
				"Status: "+d.Report.Compatibility.Status,
				"Report schema: "+d.Report.Compatibility.ReportSchemaVersion,
				"Manifest schema: "+d.Report.Compatibility.ManifestSchemaVersion,
			)
			for _, issue := range d.Report.Compatibility.Issues {
				*lines = append(*lines, fmt.Sprintf("- %s: %s", issue.Severity, issue.Message))
			}
			*lines = append(*lines, "")
		},
		CSV: func(d *runArchiveHTMLReportData, write reportCSVWriteFunc) error {
			if err := write("schema_compatibility", "", "status", d.Report.Compatibility.Status); err != nil {
				return err
			}
			if err := write("schema_compatibility", "", "report_schema_version", d.Report.Compatibility.ReportSchemaVersion); err != nil {
				return err
			}
			if err := write("schema_compatibility", "", "manifest_schema_version", d.Report.Compatibility.ManifestSchemaVersion); err != nil {
				return err
			}
			for _, output := range d.Report.Compatibility.Outputs {
				key := output.Format
				if output.Artifact != "" {
					key = key + ":" + output.Artifact
				}
				if err := write("output_compatibility", key, "status", output.Status); err != nil {
					return err
				}
				if output.Reason != "" {
					if err := write("output_compatibility", key, "reason", output.Reason); err != nil {
						return err
					}
				}
			}
			return nil
		}})

	// --- Summary timeline --------------------------------------------------
	add(reportSectionRenderer{ID: "summary_timeline", Label: "Summary Timeline",
		PDF: func(d *runArchiveHTMLReportData, lines *[]string) {
			pdfHeading(lines, "Summary Timeline")
			if len(d.Report.SummaryTimeline) == 0 {
				*lines = append(*lines, "No interval timeline was captured for this run.", "")
				return
			}
			limit := len(d.Report.SummaryTimeline)
			if limit > 10 {
				limit = 10
			}
			for _, point := range d.Report.SummaryTimeline[:limit] {
				p95 := "unavailable"
				if point.HasP95() {
					p95 = fmt.Sprintf("%.1f ms", point.P95Ms)
				}
				*lines = append(*lines, fmt.Sprintf("- %s: %s VUsers, %.1f TPS, p95 %s, %.2f%% errors",
					formatPDFTime(point.Timestamp), formatPDFInt(point.ActiveVUsers), point.TPS, p95, point.ErrorPct))
			}
			if len(d.Report.SummaryTimeline) > limit {
				*lines = append(*lines, fmt.Sprintf("... %d additional interval(s) omitted from the PDF profile.", len(d.Report.SummaryTimeline)-limit))
			}
			*lines = append(*lines, "")
		},
		CSV: func(d *runArchiveHTMLReportData, write reportCSVWriteFunc) error {
			if len(d.Report.SummaryTimeline) == 0 {
				return csvNote(write, "summary_timeline", "No interval timeline was captured for this run.")
			}
			for _, point := range d.Report.SummaryTimeline {
				key := formatPDFTime(point.Timestamp)
				p95 := ""
				if point.HasP95() {
					p95 = formatCSVFloat(point.P95Ms)
				}
				rows := [][2]string{
					{"active_vusers", formatCSVInt(point.ActiveVUsers)},
					{"tps", formatCSVFloat(point.TPS)},
					{"target_tps", formatCSVFloat(point.TargetTPS)},
					{"throughput_gap_pct", formatCSVFloat(point.ThroughputGapPct)},
					{"error_pct", formatCSVFloat(point.ErrorPct)},
					{"p95_ms", p95},
					{"percentile_quality", point.PercentileQuality},
					{"total_transactions", formatCSVInt(point.TotalTx)},
					{"total_errors", formatCSVInt(point.TotalErrors)},
				}
				for _, row := range rows {
					if err := write("summary_timeline", key, row[0], row[1]); err != nil {
						return err
					}
				}
			}
			return nil
		}})

	// --- Actions -----------------------------------------------------------
	add(reportSectionRenderer{ID: "actions", Label: "Action Summary",
		PDF: func(d *runArchiveHTMLReportData, lines *[]string) {
			if len(d.Report.ByAction) == 0 {
				pdfHeading(lines, "Top Actions")
				*lines = append(*lines, "No action-level archive data was recorded.", "")
				return
			}
			appendPDFStatsSection(lines, "Top Actions", d.Report.ByAction, 12)
		},
		CSV: func(d *runArchiveHTMLReportData, write reportCSVWriteFunc) error {
			names := sortedArchiveReportStatKeys(d.Report.ByAction)
			if len(names) == 0 {
				return csvNote(write, "action", "No action-level archive data was recorded.")
			}
			for _, action := range names {
				if err := writeArchiveReportStatsCSVRows(write, "action", action, d.Report.ByAction[action]); err != nil {
					return err
				}
			}
			return nil
		}})

	// --- Network phases ----------------------------------------------------
	add(reportSectionRenderer{ID: "network_phases", Label: "Network Phase Breakdown",
		PDF: func(d *runArchiveHTMLReportData, lines *[]string) {
			pdfHeading(lines, "Network Phase Breakdown")
			overall := d.Report.NetworkPhases.Overall
			if d.Report.NetworkPhases.Samples == 0 {
				*lines = append(*lines, "No network-phase timing was captured for this run.", "")
				return
			}
			*lines = append(*lines,
				fmt.Sprintf("Samples: %s", formatPDFInt(d.Report.NetworkPhases.Samples)),
				fmt.Sprintf("DNS: avg %.1f ms / max %.1f ms", overall.AvgDNSMs, overall.MaxDNSMs),
				fmt.Sprintf("Connect: avg %.1f ms / max %.1f ms", overall.AvgConnectMs, overall.MaxConnectMs),
				fmt.Sprintf("TLS: avg %.1f ms / max %.1f ms", overall.AvgTLSMs, overall.MaxTLSMs),
				fmt.Sprintf("TTFB: avg %.1f ms / max %.1f ms", overall.AvgTTFBMs, overall.MaxTTFBMs),
				"")
		},
		CSV: func(d *runArchiveHTMLReportData, write reportCSVWriteFunc) error {
			if d.Report.NetworkPhases.Samples == 0 {
				return csvNote(write, "network_phases", "No network-phase timing was captured for this run.")
			}
			if err := writeNetworkPhaseCSVRows(write, "overall", d.Report.NetworkPhases.Overall); err != nil {
				return err
			}
			actionKeys := make([]string, 0, len(d.Report.NetworkPhases.ByAction))
			for action := range d.Report.NetworkPhases.ByAction {
				actionKeys = append(actionKeys, action)
			}
			sort.Strings(actionKeys)
			for _, action := range actionKeys {
				if err := writeNetworkPhaseCSVRows(write, action, d.Report.NetworkPhases.ByAction[action]); err != nil {
					return err
				}
			}
			return nil
		}})

	// --- Errors ------------------------------------------------------------
	add(reportSectionRenderer{ID: "errors", Label: "Error Patterns",
		PDF: func(d *runArchiveHTMLReportData, lines *[]string) {
			pdfHeading(lines, "Error Patterns")
			if len(d.Report.ErrorPatterns) == 0 {
				*lines = append(*lines, "No clustered error patterns were recorded.", "")
				return
			}
			for _, pattern := range d.Report.ErrorPatterns {
				*lines = append(*lines, fmt.Sprintf("- %s (%d occurrence(s))", pattern.Sample, pattern.Count))
			}
			*lines = append(*lines, "")
		},
		CSV: func(d *runArchiveHTMLReportData, write reportCSVWriteFunc) error {
			if len(d.Report.ErrorPatterns) == 0 {
				return csvNote(write, "error_pattern", "No clustered error patterns were recorded.")
			}
			for _, pattern := range d.Report.ErrorPatterns {
				key := pattern.Signature
				if key == "" {
					key = pattern.Sample
				}
				if err := write("error_pattern", key, "count", formatCSVInt(pattern.Count)); err != nil {
					return err
				}
				if err := write("error_pattern", key, "sample", pattern.Sample); err != nil {
					return err
				}
				if len(pattern.Actions) > 0 {
					if err := write("error_pattern", key, "actions", strings.Join(pattern.Actions, ";")); err != nil {
						return err
					}
				}
			}
			return nil
		}})

	// --- Resources ---------------------------------------------------------
	add(reportSectionRenderer{ID: "resources", Label: "Resource Peaks",
		PDF: func(d *runArchiveHTMLReportData, lines *[]string) {
			pdfHeading(lines, "Resource Peaks")
			peaks := d.Report.ResourcePeaks
			*lines = append(*lines,
				fmt.Sprintf("CPU process: %.1f%%", peaks.CPUProcessPct),
				fmt.Sprintf("Memory process: %.1f MB", peaks.MemProcessMB),
				fmt.Sprintf("Network sent/received: %.1f / %.1f Kbps", peaks.NetSentKbps, peaks.NetRecvKbps),
				"")
		},
		CSV: func(d *runArchiveHTMLReportData, write reportCSVWriteFunc) error {
			peaks := d.Report.ResourcePeaks
			rows := [][2]string{
				{"cpu_process_pct", formatCSVFloat(peaks.CPUProcessPct)},
				{"mem_process_mb", formatCSVFloat(peaks.MemProcessMB)},
				{"net_sent_kbps", formatCSVFloat(peaks.NetSentKbps)},
				{"net_recv_kbps", formatCSVFloat(peaks.NetRecvKbps)},
			}
			for _, row := range rows {
				if err := write("resource_peaks", "", row[0], row[1]); err != nil {
					return err
				}
			}
			return nil
		}})

	// --- Browser -----------------------------------------------------------
	add(reportSectionRenderer{ID: "browser", Label: "Browser Metrics",
		PDF: func(d *runArchiveHTMLReportData, lines *[]string) {
			pdfHeading(lines, "Browser Metrics")
			browser := d.Report.Overview.Browser
			if browser == nil {
				*lines = append(*lines, "No browser metrics were captured for this run.", "")
				return
			}
			*lines = append(*lines,
				fmt.Sprintf("Samples: %s", formatPDFInt(browser.Samples)),
				fmt.Sprintf("Average FCP: %.1f ms", browser.AvgFCPMs),
				fmt.Sprintf("Average LCP: %.1f ms", browser.AvgLCPMs),
				fmt.Sprintf("Average CLS: %.3f", browser.AvgCLSScore),
				fmt.Sprintf("Average INP: %.1f ms", browser.AvgINPMs),
				"")
		},
		CSV: func(d *runArchiveHTMLReportData, write reportCSVWriteFunc) error {
			browser := d.Report.Overview.Browser
			if browser == nil {
				return csvNote(write, "browser", "No browser metrics were captured for this run.")
			}
			rows := [][2]string{
				{"samples", formatCSVInt(browser.Samples)},
				{"avg_fcp_ms", formatCSVFloat(browser.AvgFCPMs)},
				{"avg_lcp_ms", formatCSVFloat(browser.AvgLCPMs)},
				{"avg_cls_score", formatCSVFloat(browser.AvgCLSScore)},
				{"avg_inp_ms", formatCSVFloat(browser.AvgINPMs)},
			}
			for _, row := range rows {
				if err := write("browser", "", row[0], row[1]); err != nil {
					return err
				}
			}
			return nil
		}})

	// --- SOAP --------------------------------------------------------------
	add(reportSectionRenderer{ID: "soap", Label: "SOAP Diagnostics",
		PDF: func(d *runArchiveHTMLReportData, lines *[]string) {
			pdfHeading(lines, "SOAP Diagnostics")
			if len(d.Report.BySOAPOperation) == 0 && len(d.Report.BySOAPFaultCode) == 0 {
				*lines = append(*lines, "No SOAP diagnostics were captured for this run.", "")
				return
			}
			*lines = append(*lines, "")
			if len(d.Report.BySOAPOperation) > 0 {
				appendPDFStatsSection(lines, "SOAP Operations", d.Report.BySOAPOperation, 8)
			}
			for _, row := range buildHTMLCountRows(d.Report.BySOAPFaultCode) {
				*lines = append(*lines, fmt.Sprintf("- fault %s: %d", row.Name, row.Count))
			}
			*lines = append(*lines, "")
		},
		CSV: func(d *runArchiveHTMLReportData, write reportCSVWriteFunc) error {
			operations := sortedArchiveReportStatKeys(d.Report.BySOAPOperation)
			if len(operations) == 0 && len(d.Report.BySOAPFaultCode) == 0 {
				return csvNote(write, "soap", "No SOAP diagnostics were captured for this run.")
			}
			for _, operation := range operations {
				if err := writeArchiveReportStatsCSVRows(write, "soap_operation", operation, d.Report.BySOAPOperation[operation]); err != nil {
					return err
				}
			}
			for _, row := range buildHTMLCountRows(d.Report.BySOAPFaultCode) {
				if err := write("soap_fault_code", row.Name, "count", formatCSVInt(row.Count)); err != nil {
					return err
				}
			}
			return nil
		}})

	// --- Web2 migration ----------------------------------------------------
	add(reportSectionRenderer{ID: "web2_migration", Label: "Web 2.0 Migration",
		PDF: func(d *runArchiveHTMLReportData, lines *[]string) {
			pdfHeading(lines, "Web 2.0 Migration")
			migration := d.Report.Web2Migration
			if migration == nil {
				*lines = append(*lines, "No TruClient/Web 2.0 migration evidence is present.", "")
				return
			}
			*lines = append(*lines,
				fmt.Sprintf("Status: %s", migration.Status),
				fmt.Sprintf("Imported / replayed / failed steps: %d / %d / %d", migration.ImportedSteps, migration.ReplayedSteps, migration.FailedSteps),
				fmt.Sprintf("HTTP conversion ready: %t", migration.ConversionToHTTPReady),
				"")
		},
		CSV: func(d *runArchiveHTMLReportData, write reportCSVWriteFunc) error {
			migration := d.Report.Web2Migration
			if migration == nil {
				return csvNote(write, "web2_migration", "No TruClient/Web 2.0 migration evidence is present.")
			}
			rows := [][2]string{
				{"status", migration.Status},
				{"imported_steps", fmt.Sprintf("%d", migration.ImportedSteps)},
				{"replayed_steps", fmt.Sprintf("%d", migration.ReplayedSteps)},
				{"failed_steps", fmt.Sprintf("%d", migration.FailedSteps)},
				{"conversion_to_http_ready", fmt.Sprintf("%t", migration.ConversionToHTTPReady)},
				{"transaction_groups", strings.Join(migration.TransactionGroups, ";")},
				{"source_profiles", strings.Join(migration.SourceProfiles, ";")},
				{"next_actions", strings.Join(migration.NextActions, ";")},
			}
			for _, row := range rows {
				if strings.TrimSpace(row[1]) == "" {
					continue
				}
				if err := write("web2_migration", "", row[0], row[1]); err != nil {
					return err
				}
			}
			for _, key := range sortedIntMapKeys(migration.ReplayEvidence) {
				if err := write("web2_migration_evidence", key, "count", fmt.Sprintf("%d", migration.ReplayEvidence[key])); err != nil {
					return err
				}
			}
			return nil
		}})

	// --- Test setup --------------------------------------------------------
	add(reportSectionRenderer{ID: "test_setup", Label: "Test Setup",
		PDF: func(d *runArchiveHTMLReportData, lines *[]string) {
			pdfHeading(lines, "Test Setup")
			attribution := d.Report.ChangeAttribution
			appendPDFValueLine(lines, "Environment", reportEnvironment(d.Report))
			appendPDFValueLine(lines, "Scenario revision", attribution.ScenarioRevision)
			appendPDFValueLine(lines, "Scripts", strings.Join(attribution.ScriptRevisions, ", "))
			appendPDFValueLine(lines, "Generators", strings.Join(attribution.GeneratorVersions, ", "))
			*lines = append(*lines, "")
		},
		CSV: func(d *runArchiveHTMLReportData, write reportCSVWriteFunc) error {
			attribution := d.Report.ChangeAttribution
			rows := [][2]string{
				{"environment", attribution.Environment},
				{"scenario_revision", attribution.ScenarioRevision},
				{"scripts", strings.Join(attribution.ScriptRevisions, ";")},
				{"generators", strings.Join(attribution.GeneratorVersions, ";")},
			}
			wrote := false
			for _, row := range rows {
				if strings.TrimSpace(row[1]) == "" {
					continue
				}
				wrote = true
				if err := write("test_setup", "", row[0], row[1]); err != nil {
					return err
				}
			}
			if !wrote {
				return csvNote(write, "test_setup", "No environment/topology details were captured.")
			}
			return nil
		}})

	// --- Raw exports / data sources ---------------------------------------
	add(reportSectionRenderer{ID: "raw_exports", Label: "Data Sources & Raw Exports",
		PDF: func(d *runArchiveHTMLReportData, lines *[]string) {
			pdfHeading(lines, "Data Sources & Raw Exports")
			*lines = append(*lines,
				fmt.Sprintf("Archive records captured: %s", formatPDFInt(d.Report.Overview.Transactions)),
				fmt.Sprintf("Interval summaries: %d", d.Report.Overview.IntervalSummaries),
				fmt.Sprintf("Resource samples: %d", d.Report.Overview.ResourceSamples),
				"Report schema: "+d.Report.SchemaVersion,
				"")
		},
		CSV: func(d *runArchiveHTMLReportData, write reportCSVWriteFunc) error {
			if d.Report.Manifest != nil {
				if err := writeArchiveArtifactInventoryCSVRows(write, d.Report.Manifest); err != nil {
					return err
				}
			}
			return write("raw_exports", "", "report_schema_version", d.Report.SchemaVersion)
		}})

	// --- Generator assignment manifest ------------------------------------
	add(reportSectionRenderer{ID: "generator_manifest", Label: "Generator Assignment Manifest",
		PDF: func(d *runArchiveHTMLReportData, lines *[]string) {
			pdfHeading(lines, "Generator Assignment Manifest")
			if d.Manifest == nil {
				*lines = append(*lines, "The generator assignment manifest was not available for this run.", "")
				return
			}
			if d.GeneratorCount > 0 {
				*lines = append(*lines, fmt.Sprintf("Generators: %d", d.GeneratorCount))
			}
			for _, shard := range d.Manifest.LoadGenerators.GeneratorShards {
				*lines = append(*lines, fmt.Sprintf("- %s -> %s: %d VUsers", shard.Group, shard.Target, shard.VUsers))
			}
			*lines = append(*lines, "")
		},
		CSV: func(d *runArchiveHTMLReportData, write reportCSVWriteFunc) error {
			if d.Manifest == nil {
				return csvNote(write, "generator_manifest", "The generator assignment manifest was not available for this run.")
			}
			wrote := false
			for _, shard := range d.Manifest.LoadGenerators.GeneratorShards {
				wrote = true
				if err := write("generator_shard", shard.Group, "target", shard.Target); err != nil {
					return err
				}
				if err := write("generator_shard", shard.Group, "vusers", fmt.Sprintf("%d", shard.VUsers)); err != nil {
					return err
				}
			}
			for _, entry := range d.InventoryRows {
				wrote = true
				if err := write("generator_inventory", entry.Target, "status", entry.Status); err != nil {
					return err
				}
				if err := write("generator_inventory", entry.Target, "max_vusers", fmt.Sprintf("%d", entry.MaxVUsers)); err != nil {
					return err
				}
			}
			if !wrote {
				return csvNote(write, "generator_manifest", "No generator assignment was recorded.")
			}
			return nil
		}})

	// --- Audit identity ----------------------------------------------------
	add(reportSectionRenderer{ID: "audit_identity", Label: "Run Identity",
		PDF: func(d *runArchiveHTMLReportData, lines *[]string) {
			pdfHeading(lines, "Run Identity and Authorization")
			report := d.Report
			appendPDFValueLine(lines, "Test ID", report.Manifest.TestID)
			appendPDFValueLine(lines, "Run name", report.Manifest.TestName)
			appendPDFValueLine(lines, "Status", report.Manifest.Status)
			appendPDFValueLine(lines, "Completed", formatPDFTime(report.Manifest.CompletedAt))
			provenance := report.Manifest.Provenance
			appendPDFValueLine(lines, "Project", provenance.ProjectID)
			appendPDFValueLine(lines, "Release", provenance.ReleaseID)
			appendPDFValueLine(lines, "Scenario", provenance.ScenarioID)
			*lines = append(*lines, "")
		},
		CSV: func(d *runArchiveHTMLReportData, write reportCSVWriteFunc) error {
			report := d.Report
			rows := [][2]string{
				{"test_id", report.Manifest.TestID},
				{"run_name", report.Manifest.TestName},
				{"status", report.Manifest.Status},
				{"completed", formatPDFTime(report.Manifest.CompletedAt)},
				{"project", report.Manifest.Provenance.ProjectID},
				{"release", report.Manifest.Provenance.ReleaseID},
				{"scenario", report.Manifest.Provenance.ScenarioID},
			}
			for _, row := range rows {
				if strings.TrimSpace(row[1]) == "" {
					continue
				}
				if err := write("audit_identity", "", row[0], row[1]); err != nil {
					return err
				}
			}
			return nil
		}})

	// --- Authorization -----------------------------------------------------
	add(reportSectionRenderer{ID: "authorization", Label: "Authorization & Sign-off",
		PDF: func(d *runArchiveHTMLReportData, lines *[]string) {
			pdfHeading(lines, "Authorization")
			provenance := d.Report.Manifest.Provenance
			appendPDFValueLine(lines, "Created by", actorPDFValue(provenance.CreatedByDisplayName, provenance.CreatedBySubject))
			appendPDFValueLine(lines, "Started by", actorPDFValue(provenance.StartedByDisplayName, provenance.StartedBySubject))
			appendPDFValueLine(lines, "Stopped by", actorPDFValue(provenance.StoppedByDisplayName, provenance.StoppedBySubject))
			appendPDFValueLine(lines, "Scheduled start", formatPDFTimePtr(provenance.ScheduledStartAt))
			*lines = append(*lines, "")
		},
		CSV: func(d *runArchiveHTMLReportData, write reportCSVWriteFunc) error {
			provenance := d.Report.Manifest.Provenance
			rows := [][2]string{
				{"created_by", actorPDFValue(provenance.CreatedByDisplayName, provenance.CreatedBySubject)},
				{"started_by", actorPDFValue(provenance.StartedByDisplayName, provenance.StartedBySubject)},
				{"stopped_by", actorPDFValue(provenance.StoppedByDisplayName, provenance.StoppedBySubject)},
				{"scheduled_start", formatPDFTimePtr(provenance.ScheduledStartAt)},
			}
			wrote := false
			for _, row := range rows {
				value := strings.TrimSpace(row[1])
				if value == "" || value == "n/a" {
					continue
				}
				wrote = true
				if err := write("authorization", "", row[0], row[1]); err != nil {
					return err
				}
			}
			if !wrote {
				return csvNote(write, "authorization", "No authorization actors were captured.")
			}
			return nil
		}})

	// --- Governance --------------------------------------------------------
	add(reportSectionRenderer{ID: "governance", Label: "Governance & Provenance",
		PDF: func(d *runArchiveHTMLReportData, lines *[]string) {
			pdfHeading(lines, "Governance")
			governance := d.Report.Manifest.Provenance.Governance
			if governance == nil {
				*lines = append(*lines, "No governance provenance was captured for this run.", "")
				return
			}
			appendPDFValueLine(lines, "Capacity reservation", governance.CapacityReservationID)
			if governance.LaunchPreflight != nil {
				appendPDFValueLine(lines, "Launch preflight", firstNonEmptyPDFValue(governance.LaunchPreflight.Status, governance.LaunchPreflight.Decision))
			}
			if approval := governance.ExecutionPlanApproval; approval != nil {
				appendPDFValueLine(lines, "Execution-plan approval", strings.TrimSpace(approval.ID+" "+approval.Status))
				appendPDFValueLine(lines, "Approval policy", approval.Policy)
				appendPDFValueLine(lines, "Approval requested by", approval.RequestedBy)
				appendPDFValueLine(lines, "Approval resolved by", approval.ResolvedBy)
				appendPDFValueLine(lines, "Approval reason", approval.DecisionReason)
				appendPDFValueLine(lines, "Required role", approval.RequiredRole)
			}
			*lines = append(*lines, "")
		},
		CSV: func(d *runArchiveHTMLReportData, write reportCSVWriteFunc) error {
			governance := d.Report.Manifest.Provenance.Governance
			if governance == nil {
				return csvNote(write, "governance", "No governance provenance was captured for this run.")
			}
			if governance.CapacityReservationID != "" {
				if err := write("governance", "", "capacity_reservation_id", governance.CapacityReservationID); err != nil {
					return err
				}
			}
			if governance.LaunchPreflight != nil {
				if err := write("governance", "", "launch_preflight", firstNonEmptyPDFValue(governance.LaunchPreflight.Status, governance.LaunchPreflight.Decision)); err != nil {
					return err
				}
			}
			if approval := governance.ExecutionPlanApproval; approval != nil {
				rows := [][2]string{
					{"execution_plan_approval_id", approval.ID},
					{"execution_plan_approval_plan_id", approval.PlanID},
					{"execution_plan_approval_policy", approval.Policy},
					{"execution_plan_approval_status", approval.Status},
					{"execution_plan_approval_requested_by", approval.RequestedBy},
					{"execution_plan_approval_resolved_by", approval.ResolvedBy},
					{"execution_plan_approval_decision_reason", approval.DecisionReason},
					{"execution_plan_approval_required_role", approval.RequiredRole},
				}
				for _, row := range rows {
					if strings.TrimSpace(row[1]) == "" {
						continue
					}
					if err := write("governance", "", row[0], row[1]); err != nil {
						return err
					}
				}
			}
			return nil
		}})

	// --- Provenance / run configuration -----------------------------------
	add(reportSectionRenderer{ID: "provenance", Label: "Run Configuration & Provenance",
		PDF: func(d *runArchiveHTMLReportData, lines *[]string) {
			pdfHeading(lines, "Run Configuration")
			if d.Report.Manifest == nil {
				*lines = append(*lines, "No run provenance was captured.", "")
				return
			}
			provenance := d.Report.Manifest.Provenance
			appendPDFValueLine(lines, "Scenario mode", provenance.ScenarioMode)
			appendPDFValueLine(lines, "Total VUsers", fmt.Sprintf("%d", provenance.TotalVUsers))
			appendPDFValueLine(lines, "Duration", provenance.Duration)
			appendPDFValueLine(lines, "Start delay", provenance.StartDelay)
			*lines = append(*lines, "")
		},
		CSV: func(d *runArchiveHTMLReportData, write reportCSVWriteFunc) error {
			if d.Report.Manifest == nil {
				return csvNote(write, "provenance", "No run provenance was captured.")
			}
			return writeArchiveProvenanceCSVRows(write, d.Report.Manifest.Provenance)
		}})

	// --- Evidence tiers ----------------------------------------------------
	add(reportSectionRenderer{ID: "evidence_tiers", Label: "Evidence Tiers",
		PDF: func(d *runArchiveHTMLReportData, lines *[]string) {
			pdfHeading(lines, "Evidence Tiers")
			if len(d.Report.EvidenceTiers) == 0 {
				*lines = append(*lines, "No evidence tiers were declared for this run.", "")
				return
			}
			for _, tier := range d.Report.EvidenceTiers {
				*lines = append(*lines, fmt.Sprintf("- %s: %s (%s)", firstNonEmptyPDFValue(tier.Label, tier.Tier), tier.Status, formatPDFInt(tier.Count)))
			}
			*lines = append(*lines, "")
		},
		CSV: func(d *runArchiveHTMLReportData, write reportCSVWriteFunc) error {
			if len(d.Report.EvidenceTiers) == 0 {
				return csvNote(write, "evidence_tier", "No evidence tiers were declared for this run.")
			}
			for _, tier := range d.Report.EvidenceTiers {
				if err := write("evidence_tier", tier.Tier, "status", tier.Status); err != nil {
					return err
				}
				if err := write("evidence_tier", tier.Tier, "count", formatCSVInt(tier.Count)); err != nil {
					return err
				}
			}
			return nil
		}})

	// --- Artifact integrity ------------------------------------------------
	add(reportSectionRenderer{ID: "artifact_integrity", Label: "Artifact Integrity",
		PDF: func(d *runArchiveHTMLReportData, lines *[]string) {
			pdfHeading(lines, "Artifact Integrity")
			if d.Report.Integrity != nil {
				verdict := "VERIFIED"
				if !d.Report.Integrity.Verified {
					verdict = strings.ToUpper(d.Report.Integrity.Status)
				}
				*lines = append(*lines, fmt.Sprintf("Verification: %s - %s", verdict, d.Report.Integrity.Summary))
			}
			if len(d.Report.ArtifactInventory) == 0 {
				*lines = append(*lines, "No artifact inventory was available.", "")
				return
			}
			for _, artifact := range d.Report.ArtifactInventory {
				hash := artifact.SHA256
				if hash == "" {
					hash = "not captured"
				}
				*lines = append(*lines, fmt.Sprintf("- %s: %s %s, %s record(s), %d bytes, sha256=%s",
					artifact.Name, artifact.RecordType, artifact.Format, formatPDFInt(artifact.Count), artifact.SizeBytes, hash))
			}
			*lines = append(*lines, "")
		},
		CSV: func(d *runArchiveHTMLReportData, write reportCSVWriteFunc) error {
			if d.Report.Integrity != nil {
				if err := writeArchiveIntegrityCSVRows(write, d.Report.Integrity); err != nil {
					return err
				}
			}
			if d.Report.Manifest == nil {
				return csvNote(write, "artifact_inventory", "No artifact inventory was available.")
			}
			return writeArchiveArtifactInventoryCSVRows(write, d.Report.Manifest)
		}})

	// --- Report authorship -------------------------------------------------
	add(reportSectionRenderer{ID: "report_authorship", Label: "Report Authorship",
		PDF: func(d *runArchiveHTMLReportData, lines *[]string) {
			pdfHeading(lines, "Report Authorship")
			appendPDFValueLine(lines, "Prepared by", d.Annotations.PreparedBy)
			appendPDFValueLine(lines, "Updated by", d.Annotations.UpdatedBy)
			if strings.TrimSpace(d.Annotations.VerdictOverride) != "" {
				appendPDFValueLine(lines, "Verdict override", d.Annotations.VerdictOverride+" - "+d.Annotations.VerdictExplanation)
			}
			*lines = append(*lines, "")
		},
		CSV: func(d *runArchiveHTMLReportData, write reportCSVWriteFunc) error {
			rows := [][2]string{
				{"prepared_by", d.Annotations.PreparedBy},
				{"updated_by", d.Annotations.UpdatedBy},
				{"verdict_override", d.Annotations.VerdictOverride},
			}
			wrote := false
			for _, row := range rows {
				if strings.TrimSpace(row[1]) == "" {
					continue
				}
				wrote = true
				if err := write("report_authorship", "", row[0], row[1]); err != nil {
					return err
				}
			}
			if !wrote {
				return csvNote(write, "report_authorship", "No report authorship metadata was recorded.")
			}
			return nil
		}})

	return reg
}

func reportProvidedValue(value string) string {
	if strings.TrimSpace(value) == "" {
		return "Not provided"
	}
	return value
}

func reportPopulationLabel(key string) string {
	switch key {
	case "action":
		return "Requests / actions"
	case "transaction":
		return "Named transactions"
	default:
		return "Unclassified legacy records"
	}
}
