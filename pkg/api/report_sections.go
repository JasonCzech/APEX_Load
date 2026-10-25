package api

import (
	"bytes"
	"fmt"
	"html/template"
	"slices"
	"sort"
	"strings"
	"time"
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
	// Applies, when set, says whether the run produced anything for the
	// section. A section that does not apply is left out of the HTML, PDF and
	// Word reports and named once under the contents; the CSV keeps every
	// section so its rows stay a complete data export.
	Applies func(d *runArchiveHTMLReportData) bool
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
	// NotCaptured names the profile's sections that do not apply to the run.
	NotCaptured []string
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

// appendPerformanceIntervalTable keeps a printable evidence sample in PDF and
// Word instead of pointing the reader to another format for every timeline.
func appendPerformanceIntervalTable(lines *[]string, timeline []RunArchiveSummaryPoint) {
	if len(timeline) == 0 {
		*lines = append(*lines, "No interval timeline was captured for this run.", "")
		return
	}
	chosen := map[int]struct{}{0: {}, len(timeline) - 1: {}}
	peakTPS, peakP95 := 0, -1
	for index, point := range timeline {
		if point.TPS > timeline[peakTPS].TPS {
			peakTPS = index
		}
		if point.HasP95() && (peakP95 < 0 || point.P95Ms > timeline[peakP95].P95Ms) {
			peakP95 = index
		}
	}
	chosen[peakTPS] = struct{}{}
	if peakP95 >= 0 {
		chosen[peakP95] = struct{}{}
	}
	indices := make([]int, 0, len(chosen))
	for index := range chosen {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	*lines = append(*lines, "Representative intervals show start, peak throughput, slowest p95, and end.", "|Time (UTC)|VUsers|TPS|p95 ms|Error %|")
	for _, index := range indices {
		point := timeline[index]
		p95 := "n/a"
		if point.HasP95() {
			p95 = fmt.Sprintf("%.0f", point.P95Ms)
		}
		*lines = append(*lines, fmt.Sprintf("|%s|%d|%.1f|%s|%.2f|", point.Timestamp.UTC().Format("02 Jan 15:04"), point.ActiveVUsers, point.TPS, p95, point.ErrorPct))
	}
	*lines = append(*lines, "")
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

// pdfRootCauseLines writes the generated root cause as text lines.
func pdfRootCauseLines(lines *[]string, view *ReportRootCause, hypotheses []ReportRootCauseHypothesis) {
	if view == nil {
		*lines = append(*lines, "No generated root-cause analysis is available for this run.")
		return
	}
	*lines = append(*lines, "Source: "+reportRootCauseSourceLabel(view), "Outcome: "+rcaHumanize(view.Outcome), view.Summary)
	if view.Validity != "" {
		*lines = append(*lines, "Run validity: "+rcaHumanize(view.Validity)+reportJoinedSuffix(view.ValidityReasons))
	}
	*lines = append(*lines, view.Notes...)
	for _, h := range hypotheses {
		strength := h.Confidence
		if strength != "" {
			strength += " confidence"
		} else {
			strength = h.Severity
		}
		*lines = append(*lines, fmt.Sprintf("#%d %s (%s)", h.Rank, h.Title, strength))
		if len(h.Explains) > 0 {
			*lines = append(*lines, "  Explains: "+strings.Join(h.Explains, "; "))
		}
		if h.Detail != "" {
			*lines = append(*lines, "  "+h.Detail)
		}
		for _, step := range h.Chain {
			*lines = append(*lines, "  Evidence: "+step)
		}
		if view.Source == reportRootCauseV2 {
			*lines = append(*lines, "  Contradicting evidence: "+reportListOr(h.Contradicting, "none found"))
			*lines = append(*lines, "  Would disprove it: "+reportProvidedValue(h.Falsification))
		}
		for _, limitation := range h.Limitations {
			*lines = append(*lines, "  Limitation: "+limitation)
		}
	}
	if omitted := len(view.Hypotheses) - len(hypotheses); omitted > 0 {
		*lines = append(*lines, fmt.Sprintf("%d more ranked in the Engineer report.", omitted))
	}
	if b := view.BrowserBaseline; b != nil {
		*lines = append(*lines, b.Summary)
		for _, finding := range b.Findings {
			*lines = append(*lines, "  "+finding)
		}
		for _, qualification := range b.Qualification {
			*lines = append(*lines, "  "+qualification)
		}
		for _, limitation := range b.Limitations {
			*lines = append(*lines, "  Limitation: "+limitation)
		}
	}
	for _, limitation := range view.Limitations {
		*lines = append(*lines, "Limitation: "+limitation)
	}
}

// csvRootCauseRows writes the generated root cause as CSV rows; each
// hypothesis is keyed by its rank.
func csvRootCauseRows(write reportCSVWriteFunc, section string, view *ReportRootCause, hypotheses []ReportRootCauseHypothesis) error {
	if view == nil {
		return csvNote(write, section, "No generated root-cause analysis is available for this run.")
	}
	rows := [][3]string{
		{"", "source", view.Source},
		{"", "schema_version", view.SchemaVersion},
		{"", "analysis_id", view.AnalysisID},
		{"", "outcome", view.Outcome},
		{"", "summary", view.Summary},
		{"", "confidence", view.Confidence},
		{"", "validity", view.Validity},
		{"", "acceptance", view.Acceptance},
	}
	if view.Revision > 0 {
		rows = append(rows, [3]string{"", "revision", fmt.Sprint(view.Revision)})
	}
	for _, note := range view.Notes {
		rows = append(rows, [3]string{"", "note", note})
	}
	for _, h := range hypotheses {
		key := fmt.Sprintf("hypothesis_%d", h.Rank)
		rows = append(rows,
			[3]string{key, "title", h.Title}, [3]string{key, "confidence", h.Confidence}, [3]string{key, "severity", h.Severity},
			[3]string{key, "detail", h.Detail}, [3]string{key, "explains", strings.Join(h.Explains, "; ")},
			[3]string{key, "chain", strings.Join(h.Chain, "; ")}, [3]string{key, "competing", strings.Join(h.Competing, "; ")},
			[3]string{key, "falsification", h.Falsification}, [3]string{key, "limitations", strings.Join(h.Limitations, "; ")},
			[3]string{key, "evidence", strings.Join(h.Evidence, "; ")})
		if view.Source == reportRootCauseV2 {
			rows = append(rows, [3]string{key, "contradicting", reportListOr(h.Contradicting, "none found")})
		}
	}
	if b := view.BrowserBaseline; b != nil {
		rows = append(rows, [3]string{"browser_baseline", "summary", b.Summary})
		for _, finding := range b.Findings {
			rows = append(rows, [3]string{"browser_baseline", "finding", finding})
		}
		for _, qualification := range b.Qualification {
			rows = append(rows, [3]string{"browser_baseline", "qualification", qualification})
		}
		for _, limitation := range b.Limitations {
			rows = append(rows, [3]string{"browser_baseline", "limitation", limitation})
		}
	}
	for _, c := range view.Coverage {
		rows = append(rows, [3]string{"coverage", c.Evidence, strings.TrimSpace(c.Status + " " + c.Detail)})
	}
	for _, limitation := range view.Limitations {
		rows = append(rows, [3]string{"", "limitation", limitation})
	}
	for _, row := range rows {
		if strings.TrimSpace(row[2]) == "" {
			continue
		}
		if err := write(section, row[0], row[1], row[2]); err != nil {
			return err
		}
	}
	return nil
}

func reportListOr(values []string, empty string) string {
	if len(values) == 0 {
		return empty
	}
	return strings.Join(values, "; ")
}

func reportJoinedSuffix(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return " (" + strings.Join(values, "; ") + ")"
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
			{"min_ms", formatCSVFloat(row.MinMs)},
			{"p50_ms", formatCSVFloat(row.P50Ms)},
			{"p90_ms", formatCSVFloat(row.P90Ms)},
			{"p95_ms", formatCSVFloat(row.P95Ms)},
			{"p99_ms", formatCSVFloat(row.P99Ms)},
			{"percentile_quality", reportCSVPercentileQuality(row.PercentileQuality)},
			{"max_ms", formatCSVFloat(row.MaxMs)},
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

func csvTransactionResponseTimeSLARows(write reportCSVWriteFunc, section string, rows []TransactionResponseTimeSLAResult) error {
	for _, row := range rows {
		key := row.Group + " / " + firstNonEmptyReportText(row.Transaction, "not_evaluated")
		entries := [][2]string{
			{"transaction_response_time_threshold_ms", formatCSVFloat(row.ThresholdMs)},
			{"transaction_response_time_total_count", formatCSVInt(row.TotalCount)},
			{"transaction_response_time_eligible_success_count", formatCSVInt(row.EligibleSuccessCount)},
			{"transaction_response_time_excluded_error_count", formatCSVInt(row.ExcludedErrorCount)},
			{"transaction_response_time_missed_count", formatCSVInt(row.MissedCount)},
			{"transaction_response_time_missed_pct", formatCSVFloat(row.MissedPct)},
			{"transaction_response_time_met_pct", formatCSVFloat(row.MetPct)},
			{"transaction_response_time_required_met_pct", formatCSVFloat(row.RequiredMetPct)},
			{"transaction_response_time_status", row.Status},
		}
		for _, entry := range entries {
			if err := write(section, key, entry[0], entry[1]); err != nil {
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
			{"population", firstNonEmptyReportText(result.Rule.Population, "transaction")},
			{"protocol", result.Rule.Protocol},
			{"group", result.Rule.Group},
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
			if err := write(section, result.Rule.Transaction+transactionSLAScope(result.Rule), "transaction_rule_"+entry[0], entry[1]); err != nil {
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
		*lines = append(*lines, fmt.Sprintf("- %s: %s tx, p95 %.0f ms%s, SLA %s, %s errors - %s",
			row.Name, formatPDFInt(row.Volume), row.P95Ms, reportPercentileQualitySuffix(row.PercentileQuality), sla, formatReportPercentage(row.ErrorPct), row.Result))
	}
}

func pdfTransactionResponseTimeSLALines(lines *[]string, rows []TransactionResponseTimeSLAResult) {
	if len(rows) == 0 {
		return
	}
	*lines = append(*lines, "Transaction response-time SLA (95% of successful samples required)")
	for _, row := range rows {
		transaction := firstNonEmptyReportText(row.Transaction, "No named transaction observed")
		*lines = append(*lines, fmt.Sprintf("- %s / %s: %d total, %d eligible, %d excluded errors, %d missed (%.2f%%); %.2f%% met, %.2f%% required; threshold %.0f ms - %s",
			row.Group, transaction, row.TotalCount, row.EligibleSuccessCount, row.ExcludedErrorCount, row.MissedCount, row.MissedPct, row.MetPct, row.RequiredMetPct, row.ThresholdMs, row.Status))
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
			result.Rule.Transaction+transactionSLAScope(result.Rule), result.Rule.Metric, observed, result.Rule.Comparator, result.Rule.Threshold,
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

// reportSectionLabels is the one name of each report section: the contents
// list, the section heading in every format and the PDF fallback all read it,
// so a section never reads differently from its entry in the contents.
var reportSectionLabels = map[string]string{
	"verdict":              "Acceptance assessment",
	"official_outcome":     "Acceptance assessment",
	"executive_overview":   "Executive summary",
	"technical_overview":   "Executive summary",
	"sla_rollup":           "SLA scorecard",
	"sla_detail":           "SLA compliance",
	"graphs":               "Performance analysis",
	"dashboard_panels":     "Imported dashboard panels",
	"risk_summary":         "Top risks",
	"rca":                  "Root-cause analysis",
	"root_cause":           "Findings",
	"recommendations":      "Recommendations",
	"signoff":              "Sign-off",
	"release_trend":        "Release trend",
	"run_context":          "Run context and confidence",
	"telemetry_acceptance": "Telemetry acceptance",
	"browser_findings":     "Browser findings",
	"interaction_map":      "Interaction patterns and heatmaps",
	"journey_risk":         "Journey risk",
	"completeness":         "Evidence coverage",
	"controller_failover":  "Controller failover",
	"change_attribution":   "Change attribution",
	"schema_compatibility": "Schema compatibility",
	"summary_timeline":     "Summary timeline",
	"actions":              "Action summary",
	"network_phases":       "Network phase breakdown",
	"errors":               "Error patterns",
	"resources":            "Resource peaks",
	"browser":              "Browser metrics",
	"soap":                 "SOAP diagnostics",
	"web2_migration":       "Web 2.0 migration",
	"test_setup":           "Test setup",
	"raw_exports":          "Data sources and glossary",
	"generator_manifest":   "Generator assignment manifest",
	"audit_identity":       "Run identity",
	"authorization":        "Authorization and sign-off",
	"governance":           "Governance and provenance",
	"provenance":           "Run configuration and provenance",
	"evidence_tiers":       "Evidence tiers",
	"artifact_integrity":   "Artifact integrity",
	"report_authorship":    "Report authorship",
	"protocol_diagnostics": "Protocol diagnostics",
}

// reportSectionTitle is a section's name, falling back to its id.
func reportSectionTitle(id string) string {
	if label, ok := reportSectionLabels[id]; ok {
		return label
	}
	return id
}

// reportApplicableSections is the profile's sections that apply to the run,
// in order, and the names of those that do not.
func reportApplicableSections(profile RunArchiveReportProfile, d *runArchiveHTMLReportData) ([]reportSectionRenderer, []string) {
	var applicable []reportSectionRenderer
	var notCaptured []string
	for _, section := range reportSectionsForProfile(profile) {
		if section.Applies != nil && !section.Applies(d) {
			notCaptured = append(notCaptured, section.Label)
			continue
		}
		applicable = append(applicable, section)
	}
	return applicable, notCaptured
}

// reportSectionApplies holds the applicability of the sections that only some
// runs produce.
var reportSectionApplies = map[string]func(d *runArchiveHTMLReportData) bool{
	"dashboard_panels": func(d *runArchiveHTMLReportData) bool {
		return d.Report.DashboardPanels != nil && len(d.Report.DashboardPanels.Dashboards) > 0
	},
	"controller_failover": func(d *runArchiveHTMLReportData) bool {
		return d.Report.Manifest != nil && d.Report.Manifest.Continuity != nil && len(d.Report.Manifest.Continuity.Failovers) > 0
	},
	"browser": func(d *runArchiveHTMLReportData) bool {
		return d.Report.Overview.Browser != nil || len(d.Report.AuraActions) > 0 || len(d.Report.BrowserVersionSkews) > 0 || len(d.Report.BrowserIterations) > 0 ||
			len(d.Report.SalesforceEPT) > 0 || len(d.Report.AuraParity) > 0
	},
	"web2_migration": func(d *runArchiveHTMLReportData) bool { return d.Report.Web2Migration != nil },
	"soap": func(d *runArchiveHTMLReportData) bool {
		return len(d.SOAPOperationRows)+len(d.SOAPFaultCodeRows)+len(d.SOAPFaultBuckets)+len(d.SOAPPayloadBuckets) > 0
	},
	"protocol_diagnostics": func(d *runArchiveHTMLReportData) bool {
		return !slices.Contains(protocolReportLines(d.Report), protocolReportNoSamples)
	},
	reportSectionBrowserFindings: func(d *runArchiveHTMLReportData) bool { return !d.BrowserFindingsView().NotCaptured },
	reportSectionInteractionMap:  func(d *runArchiveHTMLReportData) bool { return !d.InteractionMapView().NotCaptured },
	reportSectionJourneyRisk:     func(d *runArchiveHTMLReportData) bool { return !d.JourneyRiskView().NotCaptured },
}

// buildReportSectionRegistry constructs the registry. Each entry ports the
// markup/lines from the previously-bespoke per-profile renderers into a single
// shared, profile-agnostic fragment.
func buildReportSectionRegistry() map[string]reportSectionRenderer {
	reg := map[string]reportSectionRenderer{}
	protocol := protocolDiagnosticReportSection()
	protocol.Label = reportSectionTitle(protocol.ID)
	protocol.Applies = reportSectionApplies[protocol.ID]
	reg[protocol.ID] = protocol
	add := func(r reportSectionRenderer) {
		r.HTML = htmlSectionRenderer(r.ID)
		r.Label = reportSectionTitle(r.ID)
		r.Applies = reportSectionApplies[r.ID]
		reg[r.ID] = r
	}

	// --- Verdict / outcome -------------------------------------------------
	add(reportSectionRenderer{ID: "verdict", Label: "Verdict",
		PDF: func(d *runArchiveHTMLReportData, lines *[]string) {
			pdfHeading(lines, reportSectionTitle("verdict"))
			*lines = append(*lines, fmt.Sprintf("%s - %s", d.Verdict.Status, d.Verdict.Explanation))
			if d.AnalystAssessmentInVerdict() {
				*lines = append(*lines, "Analyst assessment: "+d.Annotations.VerdictOverride+" - "+reportProvidedValue(d.Annotations.VerdictExplanation), "Assessment author: "+reportProvidedValue(d.Annotations.VerdictAuthor()))
			}
			*lines = append(*lines, "")
		},
		CSV: func(d *runArchiveHTMLReportData, write reportCSVWriteFunc) error {
			if err := write("verdict", "", "status", d.Verdict.Status); err != nil {
				return err
			}
			return write("verdict", "", "explanation", d.Verdict.Explanation)
		}})
	add(reportSectionRenderer{ID: "official_outcome", Label: "Official Outcome",
		PDF: func(d *runArchiveHTMLReportData, lines *[]string) {
			pdfHeading(lines, reportSectionTitle("official_outcome"))
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
					*lines = append(*lines, fmt.Sprintf("%s: %d records, %d errors, p95 %.1f ms%s, average %s/s", reportPopulationLabel(key), population.Transactions, population.Errors, population.P95Ms, reportPercentileQualitySuffix(population.PercentileQuality), reportPopulationRate(population.AverageRate)))
				}
			}
			headline := reportHeadline(report)
			*lines = append(*lines,
				fmt.Sprintf("Archive records: %s", formatPDFInt(report.Overview.Transactions)),
				"Headline metrics: "+headline.Label,
				fmt.Sprintf("Error rate: %s (%s of %s)", formatReportPercentage(headline.ErrorPct), formatPDFInt(headline.Errors), formatPDFInt(headline.Transactions)),
				fmt.Sprintf("P95 latency: %s%s; average %s (min %.1f ms / max %.1f ms)", formatReportMs(headline.P95Ms), reportPercentileQualitySuffix(headline.PercentileQuality), formatReportMs(headline.AvgResponseMs), headline.MinResponseMs, headline.MaxResponseMs),
				fmt.Sprintf("Peak TPS (attempts/s): %.1f", headline.PeakTPS),
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
					for _, item := range [][2]string{{"count", formatCSVInt(population.Transactions)}, {"errors", formatCSVInt(population.Errors)}, {"error_pct", formatCSVFloat(population.ErrorPct)}, {"p95_ms", formatCSVFloat(population.P95Ms)}, {"percentile_quality", reportCSVPercentileQuality(population.PercentileQuality)}, {"peak_tps", reportPopulationPeakTPS(population)}, {"average_rate_per_second", reportPopulationRate(population.AverageRate)}} {
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
			pdfHeading(lines, reportSectionTitle("sla_rollup"))
			pdfSLALines(lines, d.SLARows)
			pdfTransactionResponseTimeSLALines(lines, d.TransactionResponseTimeSLAResults)
			pdfTransactionSLARuleLines(lines, d.TransactionRuleResults)
			*lines = append(*lines, "")
		},
		CSV: func(d *runArchiveHTMLReportData, write reportCSVWriteFunc) error {
			if err := csvSLARows(write, "sla", d.SLARows); err != nil {
				return err
			}
			if err := csvTransactionResponseTimeSLARows(write, "sla", d.TransactionResponseTimeSLAResults); err != nil {
				return err
			}
			return csvTransactionSLARules(write, "sla", d.TransactionRuleResults)
		}})
	add(reportSectionRenderer{ID: "sla_detail", Label: "SLA Compliance",
		PDF: func(d *runArchiveHTMLReportData, lines *[]string) {
			pdfHeading(lines, reportSectionTitle("sla_detail"))
			pdfSLALines(lines, d.SLARows)
			pdfTransactionResponseTimeSLALines(lines, d.TransactionResponseTimeSLAResults)
			pdfTransactionSLARuleLines(lines, d.TransactionRuleResults)
			*lines = append(*lines, "")
		},
		CSV: func(d *runArchiveHTMLReportData, write reportCSVWriteFunc) error {
			if err := csvSLARows(write, "sla", d.SLARows); err != nil {
				return err
			}
			if err := csvTransactionResponseTimeSLARows(write, "sla", d.TransactionResponseTimeSLAResults); err != nil {
				return err
			}
			return csvTransactionSLARules(write, "sla", d.TransactionRuleResults)
		}})

	// --- Performance graphs ------------------------------------------------
	add(reportSectionRenderer{ID: "graphs", Label: "Performance Analysis",
		PDF: func(d *runArchiveHTMLReportData, lines *[]string) {
			pdfHeading(lines, reportSectionTitle("graphs"))
			for _, chart := range reportDocumentChartSpecs(d.Report) {
				*lines = append(*lines, reportDocumentChartMarker+chart.ID)
			}
			appendPerformanceIntervalTable(lines, d.Report.SummaryTimeline)
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
			pdfHeading(lines, reportSectionTitle("risk_summary"))
			pdfFindingsLines(lines, d.Findings)
			*lines = append(*lines, "")
		},
		CSV: func(d *runArchiveHTMLReportData, write reportCSVWriteFunc) error {
			return csvFindingsRows(write, "risk_summary", d.Findings)
		}})
	// Generated root cause (rca) and findings (root_cause, analyst-authored or
	// derived) are separate sections, so analyst content never mixes with
	// generated analysis.
	add(reportSectionRenderer{ID: "rca", Label: "Root-Cause Analysis",
		PDF: func(d *runArchiveHTMLReportData, lines *[]string) {
			pdfHeading(lines, reportSectionTitle("rca"))
			pdfRootCauseLines(lines, d.RootCause, d.RootCauseHypotheses)
			*lines = append(*lines, "")
		},
		CSV: func(d *runArchiveHTMLReportData, write reportCSVWriteFunc) error {
			return csvRootCauseRows(write, "rca", d.RootCause, d.RootCauseHypotheses)
		}})
	add(reportSectionRenderer{ID: "root_cause", Label: "Findings",
		PDF: func(d *runArchiveHTMLReportData, lines *[]string) {
			pdfHeading(lines, reportSectionTitle("root_cause"))
			pdfFindingsLines(lines, d.Findings)
			*lines = append(*lines, "")
		},
		CSV: func(d *runArchiveHTMLReportData, write reportCSVWriteFunc) error {
			return csvFindingsRows(write, "findings", d.Findings)
		}})

	// --- Recommendations / sign-off ---------------------------------------
	add(reportSectionRenderer{ID: "recommendations", Label: "Recommendations",
		PDF: func(d *runArchiveHTMLReportData, lines *[]string) {
			pdfHeading(lines, reportSectionTitle("recommendations"))
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
			pdfHeading(lines, reportSectionTitle("signoff"))
			*lines = append(*lines, "Prepared by: "+reportProvidedValue(d.Annotations.PreparedBy), "Analyst assessment: "+reportProvidedValue(d.Annotations.VerdictOverride), "Rationale: "+reportProvidedValue(d.Annotations.VerdictExplanation), "Assessment author: "+reportProvidedValue(d.Annotations.VerdictAuthor()), "")
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
				pdfHeading(lines, reportSectionTitle("release_trend"))
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
			pdfHeading(lines, reportSectionTitle("run_context"))
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
			return writeChangeAttributionCSVRows(write, d.Report.ChangeAttribution, d.Report.IdentityNames.Scenario)
		}})

	// --- Completeness ------------------------------------------------------
	add(reportSectionRenderer{ID: "telemetry_acceptance", Label: "Telemetry Acceptance", PDF: telemetryAcceptanceReportPDF, CSV: telemetryAcceptanceReportCSV})
	// --- Browser intelligence (BI-4) ---------------------------------------
	add(reportSectionRenderer{ID: reportSectionBrowserFindings, Label: "Browser Findings", PDF: browserFindingsReportPDF, CSV: browserFindingsReportCSV})
	add(reportSectionRenderer{ID: reportSectionInteractionMap, Label: "Interaction Patterns and Heatmaps", PDF: interactionMapReportPDF, CSV: interactionMapReportCSV})
	add(reportSectionRenderer{ID: reportSectionJourneyRisk, Label: "Journey Risk", PDF: journeyRiskReportPDF, CSV: journeyRiskReportCSV})
	add(reportSectionRenderer{ID: "completeness", Label: "Completeness Score",
		PDF: func(d *runArchiveHTMLReportData, lines *[]string) {
			pdfHeading(lines, reportSectionTitle("completeness"))
			*lines = append(*lines, fmt.Sprintf("Score: %.0f%% (%s)", d.Report.Completeness.ScorePct, d.Report.Completeness.Status))
			for _, finding := range d.Report.Completeness.Findings {
				*lines = append(*lines, fmt.Sprintf("- %s: %s", finding.Dimension, finding.Message))
			}
			if d.Report.Manifest != nil && d.Report.Manifest.SourceCoverage != nil {
				*lines = append(*lines, "Generator sources known at collection time (not proof of complete shard history):")
				for _, source := range d.Report.Manifest.SourceCoverage.Sources {
					status := "not collected"
					if source.Collected {
						status = "collected"
					}
					*lines = append(*lines, fmt.Sprintf("- %s: %s", source.Label(), status))
				}
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
			if d.Report.Manifest != nil && d.Report.Manifest.SourceCoverage != nil {
				if err := write("archive_source_coverage", "", "mode", d.Report.Manifest.SourceCoverage.Mode); err != nil {
					return err
				}
				for _, source := range d.Report.Manifest.SourceCoverage.Sources {
					if err := write("archive_source", source.Target, "collected", fmt.Sprint(source.Collected)); err != nil {
						return err
					}
					for _, field := range [][2]string{{"generator_id", source.GeneratorID}, {"region", source.Region}, {"groups", strings.Join(source.Groups, "; ")}} {
						if strings.TrimSpace(field[1]) == "" {
							continue
						}
						if err := write("archive_source", source.Target, field[0], field[1]); err != nil {
							return err
						}
					}
				}
			}
			return nil
		}})

	// --- Controller failover (HA run continuity) ---------------------------
	add(reportSectionRenderer{ID: "controller_failover", Label: "Controller Failover",
		PDF: func(d *runArchiveHTMLReportData, lines *[]string) {
			pdfHeading(lines, reportSectionTitle("controller_failover"))
			continuity := d.Report.Manifest.Continuity
			if continuity == nil || len(continuity.Failovers) == 0 {
				*lines = append(*lines, "No controller failover occurred during this run.", "")
				return
			}
			*lines = append(*lines, fmt.Sprintf("Recorded %s. Archive source coverage and measurement completeness are reported separately. Unobserved controller time: %.1f s.", reportCountNoun(int64(len(continuity.Failovers)), "controller failover", "controller failovers"), failoverGapTotalSeconds(continuity)))
			for _, failover := range continuity.Failovers {
				line := fmt.Sprintf("- %s: generation %d -> %d, gap %.1f s, %s adopted, %d finished during the gap",
					formatReportDateTime(failover.AdoptedAt), failover.PriorGeneration, failover.Generation, failover.GapSeconds, reportCountNoun(int64(failover.AdoptedGenerators), "load generator", "load generators"), failover.CompletedGenerators)
				if note := restartedShareNote(failover); note != "" {
					line += "; " + note
				}
				*lines = append(*lines, line)
			}
			*lines = append(*lines, "")
		},
		CSV: func(d *runArchiveHTMLReportData, write reportCSVWriteFunc) error {
			continuity := d.Report.Manifest.Continuity
			count := 0
			if continuity != nil {
				count = len(continuity.Failovers)
			}
			if err := write("controller_failover", "", "failovers", fmt.Sprint(count)); err != nil {
				return err
			}
			if count == 0 {
				return nil
			}
			if err := write("controller_failover", "", "unobserved_seconds", formatCSVFloat(failoverGapTotalSeconds(continuity))); err != nil {
				return err
			}
			for index, failover := range continuity.Failovers {
				key := fmt.Sprintf("%d", index+1)
				for _, row := range [][2]string{
					{"adopted_at", failover.AdoptedAt.UTC().Format(time.RFC3339)},
					{"prior_generation", fmt.Sprint(failover.PriorGeneration)},
					{"generation", fmt.Sprint(failover.Generation)},
					{"gap_seconds", formatCSVFloat(failover.GapSeconds)},
					{"adopted_generators", fmt.Sprint(failover.AdoptedGenerators)},
					{"completed_generators", fmt.Sprint(failover.CompletedGenerators)},
				} {
					if err := write("controller_failover", key, row[0], row[1]); err != nil {
						return err
					}
				}
				if failover.RestartedVUsers > 0 {
					if err := write("controller_failover", key, "restarted_vusers", fmt.Sprint(failover.RestartedVUsers)); err != nil {
						return err
					}
					if err := write("controller_failover", key, "restarted_groups", strings.Join(failover.RestartedGroups, "; ")); err != nil {
						return err
					}
				}
			}
			return nil
		}})

	// --- Change attribution ------------------------------------------------
	add(reportSectionRenderer{ID: "change_attribution", Label: "Change Attribution",
		PDF: func(d *runArchiveHTMLReportData, lines *[]string) {
			pdfHeading(lines, reportSectionTitle("change_attribution"))
			attribution := d.Report.ChangeAttribution
			if attribution.ScenarioRevision != "" {
				appendPDFValueLine(lines, "Scenario", reportScenarioLabel(d.Report))
			}
			appendPDFValueLine(lines, "Environment", reportEnvironment(d.Report))
			appendPDFValueLine(lines, "Data", reportDataLabel(d.Report))
			if len(attribution.ScriptRevisions) > 0 {
				appendPDFValueLine(lines, "Scripts", reportScriptLabels(d.Report))
			}
			appendPDFValueLine(lines, "Generator versions", strings.Join(attribution.GeneratorVersions, ", "))
			if attribution.RuntimeOverrides {
				appendPDFValueLine(lines, "Runtime overrides", strings.Join(attribution.RuntimeOverrideFields, ", "))
			}
			*lines = append(*lines, "")
		},
		CSV: func(d *runArchiveHTMLReportData, write reportCSVWriteFunc) error {
			return writeChangeAttributionCSVRows(write, d.Report.ChangeAttribution, d.Report.IdentityNames.Scenario)
		}})

	// --- Schema compatibility ---------------------------------------------
	add(reportSectionRenderer{ID: "schema_compatibility", Label: "Schema Compatibility",
		PDF: func(d *runArchiveHTMLReportData, lines *[]string) {
			pdfHeading(lines, reportSectionTitle("schema_compatibility"))
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
			pdfHeading(lines, reportSectionTitle("summary_timeline"))
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
				*lines = append(*lines, fmt.Sprintf("- %s: %s VUsers, %.1f TPS, p95 %s, %s errors",
					formatPDFDisplayTime(point.Timestamp), formatReportCount(point.ActiveVUsers), point.TPS, p95, formatReportPercentage(point.ErrorPct)))
			}
			if len(d.Report.SummaryTimeline) > limit {
				*lines = append(*lines, fmt.Sprintf("... %s omitted from the PDF profile.", reportCountNoun(int64(len(d.Report.SummaryTimeline)-limit), "additional interval", "additional intervals")))
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
				pdfHeading(lines, reportSectionTitle("actions"))
				*lines = append(*lines, "No action-level archive data was recorded.", "")
				return
			}
			appendPDFStatsSection(lines, "Top actions", d.Report.ByAction, 12)
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
			pdfHeading(lines, reportSectionTitle("network_phases"))
			overall := d.Report.NetworkPhases.Overall
			if d.Report.NetworkPhases.Samples == 0 {
				*lines = append(*lines, "No network-phase timing was captured for this run.")
			} else {
				*lines = append(*lines,
					fmt.Sprintf("Samples: %s", formatPDFInt(d.Report.NetworkPhases.Samples)),
					fmt.Sprintf("DNS: avg %.1f ms / max %.1f ms", overall.AvgDNSMs, overall.MaxDNSMs),
					fmt.Sprintf("Connect: avg %.1f ms / max %.1f ms", overall.AvgConnectMs, overall.MaxConnectMs),
					fmt.Sprintf("TLS: avg %.1f ms / max %.1f ms", overall.AvgTLSMs, overall.MaxTLSMs),
					fmt.Sprintf("TTFB: avg %.1f ms / max %.1f ms", overall.AvgTTFBMs, overall.MaxTTFBMs))
			}
			*lines = append(*lines, reportProtocolMixLines(d.Report.NetworkPhases)...)
			*lines = append(*lines, reportHostNetworkLines(d.Report.HostNetwork)...)
			*lines = append(*lines, reportHTTP3PreflightLines(d.Report.HTTP3Preflight)...)
			*lines = append(*lines, "")
		},
		CSV: func(d *runArchiveHTMLReportData, write reportCSVWriteFunc) error {
			if d.Report.NetworkPhases.Samples == 0 {
				if err := csvNote(write, "network_phases", "No network-phase timing was captured for this run."); err != nil {
					return err
				}
				if err := writeProtocolMixCSVRows(write, d.Report.NetworkPhases); err != nil {
					return err
				}
				if err := writeHostNetworkCSVRows(write, d.Report.HostNetwork); err != nil {
					return err
				}
				return writeHTTP3PreflightCSVRows(write, d.Report.HTTP3Preflight)
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
			if err := writeProtocolMixCSVRows(write, d.Report.NetworkPhases); err != nil {
				return err
			}
			if err := writeHostNetworkCSVRows(write, d.Report.HostNetwork); err != nil {
				return err
			}
			return writeHTTP3PreflightCSVRows(write, d.Report.HTTP3Preflight)
		}})

	// --- Errors ------------------------------------------------------------
	add(reportSectionRenderer{ID: "errors", Label: "Error Patterns",
		PDF: func(d *runArchiveHTMLReportData, lines *[]string) {
			pdfHeading(lines, reportSectionTitle("errors"))
			if len(d.Report.ErrorPatterns) == 0 {
				*lines = append(*lines, "No clustered error patterns were recorded.", "")
				return
			}
			for _, pattern := range d.Report.ErrorPatterns {
				*lines = append(*lines, fmt.Sprintf("- %s (%s)", pattern.Sample, reportCountNoun(pattern.Count, "occurrence", "occurrences")))
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
			pdfHeading(lines, reportSectionTitle("resources"))
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
			pdfHeading(lines, reportSectionTitle("browser"))
			if browser := d.Report.Overview.Browser; browser == nil {
				*lines = append(*lines, "No browser metrics were captured for this run.", "")
			} else {
				*lines = append(*lines, legacyBrowserSummaryNote)
				*lines = append(*lines,
					fmt.Sprintf("Samples: %s", formatPDFInt(browser.Samples)),
					fmt.Sprintf("Average FCP: %.1f ms", browser.AvgFCPMs),
					fmt.Sprintf("Average LCP: %.1f ms", browser.AvgLCPMs),
					fmt.Sprintf("Average CLS: %.3f", browser.AvgCLSScore),
					fmt.Sprintf("Average INP: %.1f ms", browser.AvgINPMs),
					"")
			}
			if len(d.Report.BrowserIterations) > 0 {
				*lines = append(*lines, "Cold and warm browser iterations (passed transactions; SLAs judge the warm ones):")
				for _, row := range d.Report.BrowserIterations {
					*lines = append(*lines, fmt.Sprintf("  %s: cold %s | warm %s", row.Transaction, browserIterationTimesText(row.Cold), browserIterationTimesText(row.Warm)))
				}
				*lines = append(*lines, "")
			}
			if len(d.Report.AuraParity) > 0 {
				*lines = append(*lines, "Aura parity (median calls per transaction instance, browsers vs HTTP scripts):")
				for _, row := range d.Report.AuraParity {
					*lines = append(*lines, "  "+auraParityTransactionText(row))
					for _, action := range row.Actions {
						if action.Flagged {
							*lines = append(*lines, fmt.Sprintf("    differs: %s browser %.1f, HTTP %.1f", action.Name, action.Browser, action.HTTP))
						}
					}
				}
				*lines = append(*lines, "")
			}
			if len(d.Report.SalesforceEPT) > 0 {
				*lines = append(*lines, "Salesforce EPT calibration (each step's EPT readings beside its own time):")
				for _, row := range d.Report.SalesforceEPT {
					*lines = append(*lines, "  "+salesforceEPTCalibrationText(row))
				}
				*lines = append(*lines, "")
			}
			if len(d.Report.BrowserVersionSkews) > 0 {
				*lines = append(*lines, "Browser version mismatches (the launched browser's major version differs):")
				for _, skew := range d.Report.BrowserVersionSkews {
					*lines = append(*lines, fmt.Sprintf("  %s: launched %s, expected %s, %s session(s)", skew.SourceLabel(), skew.Actual, skew.Expected, formatPDFInt(skew.Sessions)))
				}
				*lines = append(*lines, "")
			}
			if len(d.Report.AuraActions) > 0 {
				*lines = append(*lines, "Salesforce Aura actions by transaction (time of the requests that carried each):")
				for _, row := range d.Report.AuraActions {
					*lines = append(*lines, fmt.Sprintf("  %s | %s: %s calls, %.1f ms, %s errors", auraTransactionLabel(row.Transaction), row.Name, formatPDFInt(row.Count), row.TotalMs, formatPDFInt(row.Errors)))
				}
				*lines = append(*lines, "")
			}
		},
		CSV: func(d *runArchiveHTMLReportData, write reportCSVWriteFunc) error {
			browser := d.Report.Overview.Browser
			if browser == nil {
				if err := csvNote(write, "browser", "No browser metrics were captured for this run."); err != nil {
					return err
				}
			} else {
				rows := [][2]string{
					{"metric_basis", "legacy_action_summary"},
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
			}
			for _, row := range d.Report.BrowserIterations {
				for _, kind := range []struct {
					name  string
					times *BrowserIterationTimes
				}{{"cold", row.Cold}, {"warm", row.Warm}} {
					if kind.times == nil {
						continue
					}
					value := fmt.Sprintf("count=%s;errors=%s;avg_ms=%s;p90_ms=%s", formatCSVInt(kind.times.Count), formatCSVInt(kind.times.Errors), formatCSVFloat(kind.times.AvgMs), formatCSVFloat(kind.times.P90Ms))
					if err := write("browser", "browser_iteration:"+kind.name, row.Transaction, value); err != nil {
						return err
					}
				}
			}
			for _, row := range d.Report.AuraParity {
				for _, action := range row.Actions {
					value := fmt.Sprintf("browser=%s;http=%s;flagged=%t", formatCSVFloat(action.Browser), formatCSVFloat(action.HTTP), action.Flagged)
					if err := write("browser", "aura_parity:"+row.Transaction, action.Name, value); err != nil {
						return err
					}
				}
			}
			for _, row := range d.Report.SalesforceEPT {
				value := fmt.Sprintf("samples=%s;avg_ept_ms=%s;avg_step_ms=%s;avg_difference_ms=%s", formatCSVInt(row.Samples), formatCSVFloat(row.AvgEPTMs), formatCSVFloat(row.AvgStepMs), formatCSVFloat(row.AvgDifferenceMs))
				if err := write("browser", "salesforce_ept", row.Step, value); err != nil {
					return err
				}
			}
			for _, skew := range d.Report.BrowserVersionSkews {
				value := fmt.Sprintf("launched=%s;expected=%s;sessions=%s", skew.Actual, skew.Expected, formatCSVInt(skew.Sessions))
				if err := write("browser", "browser_version_skew", skew.Source, value); err != nil {
					return err
				}
			}
			for _, row := range d.Report.AuraActions {
				value := fmt.Sprintf("calls=%s;total_ms=%s;errors=%s", formatCSVInt(row.Count), formatCSVFloat(row.TotalMs), formatCSVInt(row.Errors))
				if err := write("browser", "aura_actions:"+auraTransactionLabel(row.Transaction), row.Name, value); err != nil {
					return err
				}
			}
			return nil
		}})

	// --- SOAP --------------------------------------------------------------
	add(reportSectionRenderer{ID: "soap", Label: "SOAP Diagnostics",
		PDF: func(d *runArchiveHTMLReportData, lines *[]string) {
			pdfHeading(lines, reportSectionTitle("soap"))
			if len(d.Report.BySOAPOperation) == 0 && len(d.Report.BySOAPFaultCode) == 0 {
				*lines = append(*lines, "No SOAP diagnostics were captured for this run.", "")
				return
			}
			*lines = append(*lines, "")
			if len(d.Report.BySOAPOperation) > 0 {
				appendPDFStatsSection(lines, "SOAP operations", d.Report.BySOAPOperation, 8)
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
			pdfHeading(lines, reportSectionTitle("web2_migration"))
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
			pdfHeading(lines, reportSectionTitle("test_setup"))
			attribution := d.Report.ChangeAttribution
			appendPDFValueLine(lines, "Environment", reportEnvironment(d.Report))
			if attribution.ScenarioRevision != "" {
				appendPDFValueLine(lines, "Scenario", reportScenarioLabel(d.Report))
			}
			if len(attribution.ScriptRevisions) > 0 {
				appendPDFValueLine(lines, "Scripts", reportScriptLabels(d.Report))
			}
			appendPDFValueLine(lines, "Generators", strings.Join(attribution.GeneratorVersions, ", "))
			if d.Report.Manifest != nil {
				appendLiveTableReportPDF(lines, d.Report.Manifest.Provenance.LiveTables)
			}
			*lines = append(*lines, "")
		},
		CSV: func(d *runArchiveHTMLReportData, write reportCSVWriteFunc) error {
			attribution := d.Report.ChangeAttribution
			if d.Report.Manifest != nil {
				if err := writeLiveTableReportCSVRows(write, d.Report.Manifest.Provenance.LiveTables); err != nil {
					return err
				}
			}
			rows := [][2]string{
				{"environment", attribution.Environment},
				{"scenario_revision", attribution.ScenarioRevision},
				{"scenario_name", d.Report.IdentityNames.Scenario},
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
			pdfHeading(lines, reportSectionTitle("raw_exports"))
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
			pdfHeading(lines, reportSectionTitle("generator_manifest"))
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
			pdfHeading(lines, reportSectionTitle("audit_identity"))
			report := d.Report
			appendPDFValueLine(lines, "Test ID", report.Manifest.TestID)
			appendPDFValueLine(lines, "Run name", report.Manifest.TestName)
			appendPDFValueLine(lines, "Status", report.Manifest.Status)
			appendPDFValueLine(lines, "Completed", formatPDFDisplayTime(report.Manifest.CompletedAt))
			provenance := report.Manifest.Provenance
			appendPDFValueLine(lines, "Project", identityName(report.IdentityNames.Project, provenance.ProjectID))
			appendPDFValueLine(lines, "Release", identityName(report.IdentityNames.Release, provenance.ReleaseID))
			appendPDFValueLine(lines, "Scenario", identityName(report.IdentityNames.Scenario, provenance.ScenarioID))
			*lines = append(*lines, "")
		},
		CSV: func(d *runArchiveHTMLReportData, write reportCSVWriteFunc) error {
			report := d.Report
			rows := [][2]string{
				{"test_id", report.Manifest.TestID},
				{"run_name", report.Manifest.TestName},
				{"status", report.Manifest.Status},
				{"completed", formatPDFTime(report.Manifest.CompletedAt)},
				{"project", identityName(report.IdentityNames.Project, report.Manifest.Provenance.ProjectID)},
				{"release", identityName(report.IdentityNames.Release, report.Manifest.Provenance.ReleaseID)},
				{"scenario", identityName(report.IdentityNames.Scenario, report.Manifest.Provenance.ScenarioID)},
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
			pdfHeading(lines, reportSectionTitle("authorization"))
			provenance := d.Report.Manifest.Provenance
			appendPDFValueLine(lines, "Created by", actorPDFValue(provenance.CreatedByDisplayName, provenance.CreatedBySubject))
			appendPDFValueLine(lines, "Started by", actorPDFValue(provenance.StartedByDisplayName, provenance.StartedBySubject))
			appendPDFValueLine(lines, "Stopped by", actorPDFValue(provenance.StoppedByDisplayName, provenance.StoppedBySubject))
			if provenance.ScheduledStartAt != nil {
				appendPDFValueLine(lines, "Scheduled start", formatPDFDisplayTime(*provenance.ScheduledStartAt))
			}
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
			pdfHeading(lines, reportSectionTitle("governance"))
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
			pdfHeading(lines, reportSectionTitle("provenance"))
			if d.Report.Manifest == nil {
				*lines = append(*lines, "No run provenance was captured.", "")
				return
			}
			provenance := d.Report.Manifest.Provenance
			appendPDFValueLine(lines, "Scenario mode", provenance.ScenarioMode)
			appendPDFValueLine(lines, "Total VUsers", fmt.Sprintf("%d", provenance.TotalVUsers))
			appendPDFValueLine(lines, "Duration", provenance.Duration)
			appendPDFValueLine(lines, "Start delay", provenance.StartDelay)
			appendLiveTableReportPDF(lines, provenance.LiveTables)
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
			pdfHeading(lines, reportSectionTitle("evidence_tiers"))
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
			pdfHeading(lines, reportSectionTitle("artifact_integrity"))
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
			pdfHeading(lines, reportSectionTitle("report_authorship"))
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

	reg["baseline_comparison"] = baselineComparisonReportRenderer()
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

// legacyBrowserSummaryNote labels the browser section's action-summary
// averages: they stay for continuity, and are never qualified final document
// measurements.
const legacyBrowserSummaryNote = "Legacy action summaries: averages over browser actions, not qualified final document measurements. Compare document metrics through browser document evidence and baseline comparison."
