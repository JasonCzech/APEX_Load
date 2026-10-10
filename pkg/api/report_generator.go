package api

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"html/template"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type runArchiveHTMLActionRow struct {
	Name  string
	Stats RunArchiveActionStats
}

type runArchiveHTMLCountRow struct {
	Name  string
	Count int64
}

type runArchiveHTMLSOAPFaultBucketRow struct {
	Timestamp  time.Time
	Total      int64
	Codes      string
	Categories string
}

type runArchiveHTMLSOAPPayloadBucketRow struct {
	Timestamp             time.Time
	Samples               int64
	AvgRequestPayloadKB   float64
	AvgResponsePayloadKB  float64
	AvgEnvelopeOverheadKB float64
	MaxEnvelopeOverheadKB float64
}

// runArchiveHTMLReportData is the shared, format-agnostic render context. It is
// built once per report render (see buildRunArchiveReportRenderData) and consumed
// by the section registry across the HTML, PDF, and CSV renderers. Charts are
// populated by the HTML path only.
type runArchiveHTMLReportData struct {
	GeneratedAt        time.Time
	Report             *RunArchiveReport
	Profile            RunArchiveReportProfile
	Annotations        *ReportAnnotations
	CommentarySections []reportCommentarySection
	Charts             reportCharts
	LogoDataURI        template.URL
	Manifest           *TestRunManifest
	Verdict            ReportVerdict
	EvidenceGaps       []string
	Findings           []ReportFinding
	// RootCause is the generated root cause, kept apart from Findings, which
	// hold analyst-authored or derived findings. RootCauseHypotheses is the
	// part of it the profile shows.
	RootCause                         *ReportRootCause
	RootCauseHypotheses               []ReportRootCauseHypothesis
	SLARows                           []SLAComplianceRow
	TransactionResponseTimeSLAResults []TransactionResponseTimeSLAResult
	TransactionRuleResults            []TransactionSLARuleResult
	GeneratorCount                    int
	InventoryRows                     []GeneratorInventoryEntry
	InventoryOverflow                 int
	ActionRows                        []runArchiveHTMLActionRow
	SOAPOperationRows                 []runArchiveHTMLActionRow
	SOAPFaultCodeRows                 []runArchiveHTMLCountRow
	SOAPFaultCategoryRows             []runArchiveHTMLCountRow
	SOAPFaultBuckets                  []runArchiveHTMLSOAPFaultBucketRow
	SOAPPayloadBuckets                []runArchiveHTMLSOAPPayloadBucketRow
	BrowserEngineRows                 []*BrowserEngineLiveMetrics
	BrowserPlatformRows               []*BrowserPlatformLiveMetrics
	BrowserDeviceRows                 []*BrowserDeviceLiveMetrics
}

// reportTokenLabel turns a stored status token ("needs_review") into reader
// text ("Needs review"), so exports never show snake_case enums.
func reportTokenLabel(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "_", " "))
	if value == "" {
		return "Not available"
	}
	return strings.ToUpper(value[:1]) + value[1:]
}

var profileHTMLFuncMap = template.FuncMap{
	"tokenLabel":               reportTokenLabel,
	"legacyBrowserSummaryNote": func() string { return legacyBrowserSummaryNote },
	"reportRunName":            reportRunName,
	"identityName":             identityName,
	"reportEnvironment":        reportEnvironment,
	"populationRate":           reportPopulationRate,
	"headline":                 reportHeadline,
	"fms":                      formatReportMs,
	"executionLabel":           reportExecutionLabel,
	"elapsedDuration":          reportElapsedDuration,
	"formatTime": func(value any) string {
		var t time.Time
		switch typed := value.(type) {
		case time.Time:
			t = typed
		case *time.Time:
			if typed != nil {
				t = *typed
			}
		default:
			return "n/a"
		}
		if t.IsZero() {
			return "n/a"
		}
		return t.UTC().Format(time.RFC3339)
	},
	"formatFloat": func(value float64) string {
		return fmt.Sprintf("%.1f", value)
	},
	"optFloat": func(value *float64) string {
		if value == nil {
			return "—"
		}
		return strconv.FormatFloat(*value, 'f', -1, 64)
	},
	"formatPercent": formatReportPercentage,
	"formatScore": func(value float64) string {
		return fmt.Sprintf("%.3f", value)
	},
	"versionMixRows":        reportHTTPVersionMixRows,
	"versionPhaseRows":      reportHTTPVersionPhaseRows,
	"ratioText":             reportRatioText,
	"countText":             reportCountText,
	"hostNetworkRows":       reportHostNetworkRows,
	"http3PreflightRows":    reportHTTP3PreflightRows,
	"http3PreflightSummary": reportHTTP3PreflightSummary,
	"f0": func(value float64) string {
		return fmt.Sprintf("%.0f", value)
	},
	"hint": func(value int64) string {
		return formatPDFInt(value)
	},
	"join": func(values []string) string {
		return strings.Join(values, ", ")
	},
	// signed/signedPct render run-over-run deltas with an explicit +/- so a
	// regression reads at a glance in the release-trend table.
	"signed": func(value float64) string {
		return fmt.Sprintf("%+.1f", value)
	},
	"signedPct": func(value float64) string {
		return fmt.Sprintf("%+.2f%%", value)
	},
	"rootCauseSource":  reportRootCauseSourceLabel,
	"rootCauseOutcome": rcaHumanize,
	"sub":              func(a, b int) int { return a - b },
	"sevPill": func(severity string) string {
		if strings.EqualFold(severity, "major") {
			return "warn"
		}
		return "info"
	},
	"sevLabel": func(severity string) string {
		switch strings.ToLower(strings.TrimSpace(severity)) {
		case "major":
			return "Major"
		case "minor":
			return "Minor"
		default:
			return "Info"
		}
	},
}

func renderRunArchiveHTMLReport(report *RunArchiveReport) ([]byte, error) {
	return renderRunArchiveHTMLReportWithProfile(report, "engineer")
}

func renderRunArchiveHTMLReportWithProfile(report *RunArchiveReport, profileID string) ([]byte, error) {
	return renderRunArchiveHTMLReportWithAnnotations(report, profileID, nil, nil)
}

// renderRunArchiveHTMLReportWithAnnotations renders the report with analyst
// commentary, an auto-derived verdict and findings, supporting graphs, and a
// generator-assignment manifest appendix. When annotations is nil, generated
// defaults are derived from the report data so the document still reads well
// unedited. When manifest is nil, the manifest appendix renders an
// "unavailable" note rather than failing.
//
// The document is assembled from the shared section registry: the profile's
// SectionOrder (gated by IncludedSections) selects and orders the section
// fragments, so the profile metadata is the single source of truth for what
// renders. There are no per-profile page templates — a 4th profile renders
// exactly its declared sections rather than fail-open to another profile's
// content.
func renderRunArchiveHTMLReportWithAnnotations(report *RunArchiveReport, profileID string, annotations *ReportAnnotations, manifest *TestRunManifest) ([]byte, error) {
	if report == nil || report.Manifest == nil {
		return nil, fmt.Errorf("archive report is incomplete")
	}
	profile, ok := lookupRunArchiveReportProfile(profileID)
	if !ok {
		return nil, fmt.Errorf("unknown report profile %q", profileID)
	}

	data := buildRunArchiveReportRenderData(report, profile, annotations, manifest)
	data.Charts = buildReportCharts(report)

	sections := reportSectionsForProfile(profile)
	rendered := make([]renderedReportSection, 0, len(sections))
	for _, section := range sections {
		body, err := section.HTML(&data)
		if err != nil {
			return nil, fmt.Errorf("render report section %q: %w", section.ID, err)
		}
		rendered = append(rendered, renderedReportSection{ID: section.ID, Label: section.Label, Body: body})
	}

	var buf bytes.Buffer
	if err := runArchiveReportShellTemplate.Execute(&buf, reportShellData{Data: &data, Sections: rendered}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// buildRunArchiveReportRenderData assembles the shared render context used by all
// section renderers. Charts are intentionally left unset (the HTML path fills
// them in); PDF/CSV sections never need them.
func buildRunArchiveReportRenderData(report *RunArchiveReport, profile RunArchiveReportProfile, annotations *ReportAnnotations, manifest *TestRunManifest) runArchiveHTMLReportData {
	testID := ""
	if report != nil && report.Manifest != nil {
		testID = report.Manifest.TestID
	}
	if annotations == nil {
		annotations = defaultReportAnnotations(report, testID, profile.ID)
	}

	actionStats := report.ByAction
	if report.MetricPopulations != nil {
		actionStats = report.ByRequest
	}
	actionRows := make([]runArchiveHTMLActionRow, 0, len(actionStats))
	for name, stats := range actionStats {
		actionRows = append(actionRows, runArchiveHTMLActionRow{Name: name, Stats: stats})
	}
	sort.Slice(actionRows, func(i, j int) bool {
		if actionRows[i].Stats.Transactions == actionRows[j].Stats.Transactions {
			return actionRows[i].Name < actionRows[j].Name
		}
		return actionRows[i].Stats.Transactions > actionRows[j].Stats.Transactions
	})

	soapOperationRows := make([]runArchiveHTMLActionRow, 0, len(report.BySOAPOperation))
	for name, stats := range report.BySOAPOperation {
		soapOperationRows = append(soapOperationRows, runArchiveHTMLActionRow{Name: name, Stats: stats})
	}
	sort.Slice(soapOperationRows, func(i, j int) bool {
		if soapOperationRows[i].Stats.Transactions == soapOperationRows[j].Stats.Transactions {
			return soapOperationRows[i].Name < soapOperationRows[j].Name
		}
		return soapOperationRows[i].Stats.Transactions > soapOperationRows[j].Stats.Transactions
	})

	verdict := deriveReportVerdict(report)
	findings := annotations.Findings
	if len(findings) == 0 {
		findings = deriveReportFindings(report)
	} else {
		findings = normalizeReportFindings(findings)
	}
	inventoryRows, inventoryOverflow := manifestInventoryRows(manifest, 50)
	// A report without an attached root cause renders the v1 summary, so the
	// rca section is never blank.
	rootCause := report.RootCauseView
	if rootCause == nil && slices.Contains(profile.IncludedSections, "rca") {
		v1 := reportRootCauseFromV1(buildRunRootCauseSummary(testID, report))
		rootCause = &v1
	}

	return runArchiveHTMLReportData{
		GeneratedAt:                       time.Now().UTC(),
		Report:                            report,
		Profile:                           profile,
		Annotations:                       annotations,
		CommentarySections:                reportCommentarySectionOrder(profile.ID),
		LogoDataURI:                       reportLogoDataURI(),
		Manifest:                          manifest,
		Verdict:                           verdict,
		EvidenceGaps:                      reportPresentationEvidenceGaps(report, profile.ID, annotations, verdict),
		Findings:                          findings,
		RootCause:                         rootCause,
		RootCauseHypotheses:               reportRootCauseHypotheses(rootCause, profile),
		SLARows:                           buildSLAComplianceRows(report),
		TransactionResponseTimeSLAResults: append([]TransactionResponseTimeSLAResult(nil), report.TransactionResponseTimeSLAResults...),
		TransactionRuleResults:            reportTransactionSLARuleResults(report),
		GeneratorCount:                    manifestGeneratorCount(manifest),
		InventoryRows:                     inventoryRows,
		InventoryOverflow:                 inventoryOverflow,
		ActionRows:                        actionRows,
		SOAPOperationRows:                 soapOperationRows,
		SOAPFaultCodeRows:                 buildHTMLCountRows(report.BySOAPFaultCode),
		SOAPFaultCategoryRows:             buildHTMLCountRows(report.BySOAPFaultCategory),
		SOAPFaultBuckets:                  buildHTMLSOAPFaultBuckets(report.SOAPFaultTimeline),
		SOAPPayloadBuckets:                buildHTMLSOAPPayloadBuckets(report.SOAPPayloadTimeline),
		BrowserEngineRows:                 buildHTMLBrowserEngineRows(report.BrowserByEngine),
		BrowserPlatformRows:               buildHTMLBrowserPlatformRows(report.BrowserByPlatform),
		BrowserDeviceRows:                 buildHTMLBrowserDeviceRows(report.BrowserByDevice),
	}
}

func reportPresentationEvidenceGaps(report *RunArchiveReport, profileID string, annotations *ReportAnnotations, verdict ReportVerdict) []string {
	if report == nil {
		return []string{"Archived run evidence is unavailable."}
	}
	var gaps []string
	if verdict.Status == reportVerdictNotAssessed {
		gaps = append(gaps, "Acceptance criteria were not configured; a pass/fail decision is unavailable.")
	}
	if report.Completeness.ScorePct < 100 {
		gaps = append(gaps, fmt.Sprintf("Archive evidence completeness is %.0f%%; review the completeness section before relying on missing signals.", report.Completeness.ScorePct))
	}
	if profileID == "audit" {
		if report.Integrity == nil {
			gaps = append(gaps, "Artifact integrity verification is unavailable.")
		}
		if annotations == nil || strings.TrimSpace(annotations.PreparedBy) == "" {
			gaps = append(gaps, "The report preparer was not recorded.")
		}
	} else {
		if len(report.SummaryTimeline) == 0 {
			gaps = append(gaps, "No interval timeline was captured for time-series review.")
		}
		if profileID == "executive" && report.ReleaseTrend == nil {
			gaps = append(gaps, "No release trend is available for comparison.")
		}
	}
	return gaps
}

func reportTransactionSLARuleResults(report *RunArchiveReport) []TransactionSLARuleResult {
	if report == nil || report.Manifest == nil {
		return nil
	}
	return append([]TransactionSLARuleResult(nil), report.Manifest.TransactionRuleResults...)
}

// applyVerdictOverride replaces the auto-derived verdict with an analyst-set
// status (and optional explanation), recomputing the conditional/failed styling
// flags from the override label.
func applyVerdictOverride(status, explanation string, base ReportVerdict) ReportVerdict {
	override := ReportVerdict{Status: strings.TrimSpace(status)}
	switch strings.ToLower(override.Status) {
	case "fail", "failed":
		override.Status = reportVerdictFail
		override.Failed = true
	case "conditional pass", "conditional":
		override.Status = reportVerdictConditional
		override.Conditional = true
	case "pass", "passed":
		override.Status = reportVerdictPass
	default:
		// An out-of-vocabulary override (e.g. a direct API client bypassing the
		// constrained dashboard dropdown) must NOT fall through to clean green
		// pass styling on a sign-off document. Surface it as a Conditional Pass so
		// it gets the warn badge while keeping the submitted label visible.
		override.Conditional = true
	}
	if strings.TrimSpace(explanation) != "" {
		override.Explanation = explanation
	} else {
		override.Explanation = base.Explanation
	}
	return override
}

// manifestGeneratorCount returns the number of distinct generator targets in
// the run manifest (preferring the inventory snapshot, falling back to shards).
func manifestGeneratorCount(manifest *TestRunManifest) int {
	if manifest == nil {
		return 0
	}
	seen := make(map[string]struct{})
	for _, entry := range manifest.LoadGenerators.GeneratorInventory {
		if entry.Target != "" {
			seen[entry.Target] = struct{}{}
		}
	}
	if len(seen) == 0 {
		for _, shard := range manifest.LoadGenerators.GeneratorShards {
			if shard.Target != "" {
				seen[shard.Target] = struct{}{}
			}
		}
	}
	return len(seen)
}

// manifestInventoryRows returns the generator-inventory rows for the appendix,
// capped at limit, plus the count of any rows beyond the cap.
func manifestInventoryRows(manifest *TestRunManifest, limit int) ([]GeneratorInventoryEntry, int) {
	if manifest == nil {
		return nil, 0
	}
	inventory := manifest.LoadGenerators.GeneratorInventory
	if limit > 0 && len(inventory) > limit {
		return inventory[:limit], len(inventory) - limit
	}
	return inventory, 0
}

var (
	reportLogoOnce sync.Once
	reportLogoURI  template.URL
)

// reportLogoDataURI returns the APEX Load logo as a base64 data URI so reports
// are self-contained (no external asset fetch). Returns "" if the embedded
// asset cannot be read. Wrapped as template.URL so html/template emits the
// data: URI without rewriting it to a safe placeholder.
func reportLogoDataURI() template.URL {
	reportLogoOnce.Do(func() {
		data, err := StaticFS.ReadFile("static/apex-load-logo.png")
		if err != nil {
			return
		}
		reportLogoURI = template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(data))
	})
	return reportLogoURI
}

func buildHTMLCountRows(source map[string]int64) []runArchiveHTMLCountRow {
	rows := make([]runArchiveHTMLCountRow, 0, len(source))
	for name, count := range source {
		rows = append(rows, runArchiveHTMLCountRow{Name: name, Count: count})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Count == rows[j].Count {
			return rows[i].Name < rows[j].Name
		}
		return rows[i].Count > rows[j].Count
	})
	return rows
}

func buildHTMLSOAPFaultBuckets(source []RunArchiveSOAPFaultTimelineBucket) []runArchiveHTMLSOAPFaultBucketRow {
	rows := make([]runArchiveHTMLSOAPFaultBucketRow, 0, len(source))
	for _, bucket := range source {
		rows = append(rows, runArchiveHTMLSOAPFaultBucketRow{
			Timestamp:  bucket.Timestamp,
			Total:      bucket.Total,
			Codes:      joinCountRows(buildHTMLCountRows(bucket.ByCode)),
			Categories: joinCountRows(buildHTMLCountRows(bucket.ByCategory)),
		})
	}
	return rows
}

func buildHTMLSOAPPayloadBuckets(source []RunArchiveSOAPPayloadTimelineBucket) []runArchiveHTMLSOAPPayloadBucketRow {
	rows := make([]runArchiveHTMLSOAPPayloadBucketRow, 0, len(source))
	for _, bucket := range source {
		rows = append(rows, runArchiveHTMLSOAPPayloadBucketRow{
			Timestamp:             bucket.Timestamp,
			Samples:               bucket.Samples,
			AvgRequestPayloadKB:   bucket.AvgRequestPayloadKB,
			AvgResponsePayloadKB:  bucket.AvgResponsePayloadKB,
			AvgEnvelopeOverheadKB: bucket.AvgEnvelopeOverheadKB,
			MaxEnvelopeOverheadKB: bucket.MaxEnvelopeOverheadKB,
		})
	}
	return rows
}

func buildHTMLBrowserEngineRows(source map[string]*BrowserEngineLiveMetrics) []*BrowserEngineLiveMetrics {
	rows := make([]*BrowserEngineLiveMetrics, 0, len(source))
	for _, row := range source {
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].BrowserEngine < rows[j].BrowserEngine
	})
	return rows
}

func buildHTMLBrowserPlatformRows(source map[string]*BrowserPlatformLiveMetrics) []*BrowserPlatformLiveMetrics {
	rows := make([]*BrowserPlatformLiveMetrics, 0, len(source))
	for _, row := range source {
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].DevicePlatform == rows[j].DevicePlatform {
			return rows[i].BrowserEngine < rows[j].BrowserEngine
		}
		return rows[i].DevicePlatform < rows[j].DevicePlatform
	})
	return rows
}

func buildHTMLBrowserDeviceRows(source map[string]*BrowserDeviceLiveMetrics) []*BrowserDeviceLiveMetrics {
	rows := make([]*BrowserDeviceLiveMetrics, 0, len(source))
	for _, row := range source {
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].DeviceProfile < rows[j].DeviceProfile
	})
	return rows
}

func joinCountRows(rows []runArchiveHTMLCountRow) string {
	if len(rows) == 0 {
		return "n/a"
	}
	parts := make([]string, 0, len(rows))
	for _, row := range rows {
		parts = append(parts, fmt.Sprintf("%s (%d)", row.Name, row.Count))
	}
	return strings.Join(parts, ", ")
}

// Reader-facing context never substitutes internal identifiers for missing names.
func reportRunName(report *RunArchiveReport) string {
	if report != nil && report.Manifest != nil && strings.TrimSpace(report.Manifest.TestName) != "" {
		return strings.TrimSpace(report.Manifest.TestName)
	}
	return "Unnamed test"
}
func reportEnvironment(report *RunArchiveReport) string {
	if report != nil && strings.TrimSpace(report.ChangeAttribution.Environment) != "" {
		return strings.TrimSpace(report.ChangeAttribution.Environment)
	}
	return "Not recorded"
}

func formatReportPercentage(value float64) string {
	if value > 0 && value < 0.01 {
		return "<0.01%"
	}
	return fmt.Sprintf("%.2f%%", value)
}

// identityName returns the display name for a provenance id, falling back to
// the id itself when no name could be resolved.
func identityName(name, id string) string {
	if strings.TrimSpace(name) != "" {
		return name
	}
	if strings.TrimSpace(id) != "" {
		return id
	}
	return "n/a"
}
