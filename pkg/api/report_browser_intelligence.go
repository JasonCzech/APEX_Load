package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/storage"
)

// Browser intelligence report sections (BI-4): browser_findings,
// interaction_map and journey_risk (the browser baseline is part of the rca
// section, with the root-cause analysis it comes from). Their results are read
// at the storage-aware layer (attachBrowserIntelligence), as the release
// trend and root cause are, so buildRunArchiveReport stays a pure function of
// one archive. HTML, PDF, CSV and (through HTML) DOCX all render the same
// views.

const (
	reportSectionBrowserFindings = "browser_findings"
	reportSectionInteractionMap  = "interaction_map"
	reportSectionJourneyRisk     = "journey_risk"

	// Report tables are bounded; the run report and the APIs hold the rest.
	reportBrowserFindingsLimit    = 20
	reportInteractionPatternLimit = 20
	reportInteractionHeatmapLimit = 12
)

// journeyRiskNotPinned is why a run without a pinned journey policy has no
// journey accounting; its section is left out of the report.
const journeyRiskNotPinned = "The run pinned no journey policy."

var browserIntelligenceReportSections = []string{reportSectionBrowserFindings, reportSectionInteractionMap, reportSectionJourneyRisk}

// ReportBrowserIntelligence holds the browser intelligence results a report
// renders. A section whose result could not be read names why in
// Unavailable; a section the profile does not render is not read.
type ReportBrowserIntelligence struct {
	Findings     *RunBrowserFindingsResponse `json:"findings,omitempty"`
	Interactions *BrowserInteractionAnalysis `json:"interactions,omitempty"`
	Journeys     *JourneyRiskReport          `json:"journeys,omitempty"`
	Unavailable  map[string]string           `json:"unavailable,omitempty"`
}

// attachBrowserIntelligenceForProfile reads the browser intelligence results
// the profile renders.
func (s *Server) attachBrowserIntelligenceForProfile(ctx context.Context, report *RunArchiveReport, profileID string) {
	sections := map[string]bool{}
	for _, id := range browserIntelligenceReportSections {
		if profileIncludesSection(profileID, id) {
			sections[id] = true
		}
	}
	s.attachBrowserIntelligence(ctx, report, sections)
}

func (s *Server) attachBrowserIntelligence(ctx context.Context, report *RunArchiveReport, sections map[string]bool) {
	if report == nil || report.Manifest == nil || len(sections) == 0 {
		return
	}
	testID := strings.TrimSpace(report.Manifest.TestID)
	project := normalizeProjectID(report.Manifest.Provenance.ProjectID)
	out := &ReportBrowserIntelligence{Unavailable: map[string]string{}}
	unavailable := func(section, reason string, err error) {
		out.Unavailable[section] = reason
		if err != nil {
			slog.Warn("archive report: browser intelligence section unavailable", "test", testID, "section", section, "err", err)
		}
	}
	if sections[reportSectionBrowserFindings] {
		if store, ok := s.store.(storage.EvaluationJobStore); !ok {
			unavailable(reportSectionBrowserFindings, "Browser evaluations are unavailable on this controller.", nil)
		} else if findings, err := runBrowserFindings(store, project, testID); err != nil {
			unavailable(reportSectionBrowserFindings, "The run's browser findings could not be read.", err)
		} else {
			out.Findings = &findings
		}
	}
	if sections[reportSectionInteractionMap] {
		analysis, err := s.runArchiveBrowserInteractions(ctx, testID)
		switch {
		case err == nil:
			out.Interactions = analysis
		case errors.Is(err, errInteractionArchiveNotFinalized):
			unavailable(reportSectionInteractionMap, "Interaction patterns read the finalized archive, which is not available yet.", nil)
		default:
			unavailable(reportSectionInteractionMap, "The run's interaction evidence could not be read.", err)
		}
	}
	if sections[reportSectionJourneyRisk] {
		journeys, reason, err := s.runJourneyRisk(ctx, testID, project)
		if journeys != nil {
			out.Journeys = journeys
		} else {
			unavailable(reportSectionJourneyRisk, reason, err)
		}
	}
	if len(out.Unavailable) == 0 {
		out.Unavailable = nil
	}
	report.BrowserIntelligence = out
}

// runJourneyRisk accounts the run's journeys under its pinned journey policy.
func (s *Server) runJourneyRisk(ctx context.Context, testID, project string) (*JourneyRiskReport, string, error) {
	store, ok := s.store.(storage.AnalysisDefinitionStore)
	if !ok {
		return nil, "Analysis policies are unavailable on this controller.", nil
	}
	record, ok := s.archiveDomainService().GetRunArchiveContext(ctx, testID)
	if !ok || record == nil {
		return nil, "The run has no archive.", nil
	}
	if strings.TrimSpace(record.ManifestSHA256) == "" {
		return nil, "Journey accounting reads the finalized archive, which is not available yet.", nil
	}
	lease, err := acquireArchiveMaterialized(ctx, record)
	if err != nil {
		return nil, "The run archive could not be materialized.", err
	}
	defer lease.Release()
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		return nil, "The run archive manifest is unreadable.", err
	}
	policy, ref, found, status, err := resolveJourneyPolicy(store, project, manifest, "", "")
	if err != nil {
		// A store failure's cause goes to the log, never into the report.
		if status >= http.StatusInternalServerError {
			return nil, "The run's pinned journey policy could not be read.", err
		}
		return nil, "The run's pinned journey policy could not be used: " + err.Error(), nil
	}
	if !found {
		return nil, journeyRiskNotPinned, nil
	}
	journeys, err := accountJourneys(ctx, record, manifest, policy, ref)
	if err != nil {
		return nil, "The run's transactions could not be read.", err
	}
	return journeys, "", nil
}

// ---- Views -----------------------------------------------------------------

type reportBrowserFindingsView struct {
	Summary string
	// NotCaptured marks a run that produced nothing for this section (no
	// evaluation, interactions or pinned policy): the section is left out of
	// the report and named once under its contents.
	NotCaptured bool
	Findings    []reportBrowserFindingRow
	More        int
	Notes       []string
}

type reportBrowserFindingRow struct {
	Title, State, Severity, Observed, Summary string
}

type reportInteractionMapView struct {
	Summary string
	// NotCaptured marks a run that produced nothing for this section (no
	// evaluation, interactions or pinned policy): the section is left out of
	// the report and named once under its contents.
	NotCaptured bool
	Patterns    []reportInteractionPatternRow
	More        int
	Heatmaps    []reportInteractionHeatmapRow
	MoreMaps    int
	Notes       []string
}

type reportInteractionPatternRow struct {
	Kind, Provenance, Route, Action, Count, Detail string
}

type reportInteractionHeatmapRow struct {
	Route, Layout, Viewport, Engine, Profile, Gestures, Cells, Targets string
}

type reportJourneyRiskView struct {
	Summary string
	// NotCaptured marks a run that produced nothing for this section (no
	// evaluation, interactions or pinned policy): the section is left out of
	// the report and named once under its contents.
	NotCaptured bool
	Journeys    []reportJourneyRow
	Notes       []string
}

type reportJourneyRow struct {
	ID, Name, Eligible, Completed, Failed, Aborted, Skipped, Censored, FailureRate string
}

func (d *runArchiveHTMLReportData) browserIntelligence() *ReportBrowserIntelligence {
	if d == nil || d.Report == nil {
		return nil
	}
	return d.Report.BrowserIntelligence
}

func browserIntelligenceUnavailable(intelligence *ReportBrowserIntelligence, section string) string {
	if intelligence == nil {
		return "This report was generated without browser intelligence results."
	}
	if reason := intelligence.Unavailable[section]; reason != "" {
		return reason
	}
	return ""
}

func reportFloat(value *float64, unit string) string {
	if value == nil {
		return "unavailable"
	}
	if unit == "score" || unit == "" {
		return fmt.Sprintf("%.3g", *value)
	}
	return fmt.Sprintf("%.0f %s", *value, unit)
}

// BrowserFindingsView is the browser_findings section's view of the report.
func (d *runArchiveHTMLReportData) BrowserFindingsView() reportBrowserFindingsView {
	intelligence := d.browserIntelligence()
	if reason := browserIntelligenceUnavailable(intelligence, reportSectionBrowserFindings); reason != "" || intelligence.Findings == nil {
		if reason == "" {
			reason = "The run's browser findings were not read."
		}
		return reportBrowserFindingsView{Summary: reason}
	}
	response := intelligence.Findings
	if response.Result == nil {
		switch response.Status {
		case "pending":
			return reportBrowserFindingsView{Summary: "A browser evaluation of this run is queued or running."}
		case "failed":
			// The job's failure text can carry store or object-store detail;
			// an export says only that it failed (the run report shows the
			// job to those who may see it).
			return reportBrowserFindingsView{Summary: "The run's browser evaluation failed. Its findings were not produced; evaluate the run again."}
		default:
			return reportBrowserFindingsView{Summary: "This run has no browser evaluation. A run that pins a browser policy and captures document evidence is evaluated when its archive is finalized.", NotCaptured: true}
		}
	}
	result := response.Result
	score := "unscored"
	if result.Score.Score != nil {
		score = fmt.Sprintf("%.1f/100 (%s)", *result.Score.Score, strings.ReplaceAll(result.Score.Severity, "_", " "))
	} else if result.Score.UnscoredReason != "" {
		score = "unscored: " + result.Score.UnscoredReason
	}
	view := reportBrowserFindingsView{
		Summary: fmt.Sprintf("Score %s under %s revision %d. %d active, %d unscored, %d suppressed.", score, firstNonEmpty(result.Policy.Name, result.Policy.ID), result.Policy.Revision,
			result.Counts["active"], result.Counts["unscored"], result.Counts["suppressed"]),
		Notes: append([]string(nil), result.Notes...),
	}
	findings := append([]BrowserFinding(nil), result.Findings...)
	order := map[string]int{"active": 0, "unscored": 1, "suppressed": 2}
	sort.SliceStable(findings, func(i, j int) bool { return order[findings[i].State] < order[findings[j].State] })
	for index, finding := range findings {
		if index == reportBrowserFindingsLimit {
			view.More = len(findings) - index
			break
		}
		title := finding.RuleID
		if finding.Metric != "" {
			title = strings.ToUpper(finding.Metric)
			if finding.Cohort != nil {
				title += " · " + strings.Join(nonEmptyStrings(finding.Cohort.Engine, finding.Cohort.DeviceProfile), " / ")
			}
		}
		view.Findings = append(view.Findings, reportBrowserFindingRow{Title: title, State: finding.State, Severity: strings.ReplaceAll(finding.Severity, "_", " "), Observed: reportFloat(finding.Observed, finding.Unit), Summary: finding.Summary})
	}
	return view
}

// InteractionMapView is the interaction_map section's view of the report.
func (d *runArchiveHTMLReportData) InteractionMapView() reportInteractionMapView {
	intelligence := d.browserIntelligence()
	if reason := browserIntelligenceUnavailable(intelligence, reportSectionInteractionMap); reason != "" || intelligence.Interactions == nil {
		if reason == "" {
			reason = "The run's interaction patterns were not read."
		}
		return reportInteractionMapView{Summary: reason}
	}
	analysis := intelligence.Interactions
	switch analysis.Status {
	case "not_captured":
		return reportInteractionMapView{Summary: "This run captured no interaction evidence.", NotCaptured: true}
	case "unavailable":
		return reportInteractionMapView{Summary: "This run captured interactions, but none reached the archive.", Notes: append([]string(nil), analysis.Limitations...)}
	}
	coverage := analysis.Coverage
	view := reportInteractionMapView{
		Summary: fmt.Sprintf("%d patterns and %d heatmaps from %d interactions (%d mapped, %d dropped%s), %s.", len(analysis.Patterns), len(analysis.Heatmaps), coverage.Interactions, coverage.Mapped, coverage.Dropped,
			map[bool]string{true: ", incomplete", false: ""}[coverage.Incomplete], analysis.Version),
		Notes: append(append([]string(nil), analysis.Notes...), analysis.Limitations...),
	}
	if coverage.PatternTruncated > 0 {
		view.Notes = append(view.Notes, fmt.Sprintf("Pattern sample retained %d of %d streamed interactions (%d beyond pattern sample).", coverage.PatternRetained, coverage.StreamedInteractions, coverage.PatternTruncated))
	}
	if analysis.Version == browserInteractionAnalysisVersion3 || analysis.Version == browserInteractionAnalysisVersion4 {
		view.Notes = append(view.Notes, fmt.Sprintf("Pattern-eligible gestures: %d.", coverage.PatternEligible))
		if coverage.MappedViaTransform > 0 {
			view.Notes = append(view.Notes, fmt.Sprintf("Mapped via child-frame transform: %d.", coverage.MappedViaTransform))
		}
		var reasons []string
		for reason := range coverage.PatternExcluded {
			reasons = append(reasons, reason)
		}
		sort.Strings(reasons)
		for _, reason := range reasons {
			view.Notes = append(view.Notes, fmt.Sprintf("Excluded from patterns: %d %s.", coverage.PatternExcluded[reason], reason))
		}
	}
	var excluded []string
	for reason := range coverage.Excluded {
		excluded = append(excluded, reason)
	}
	sort.Strings(excluded)
	for _, reason := range excluded {
		view.Notes = append(view.Notes, fmt.Sprintf("Not mapped: %d %s.", coverage.Excluded[reason], reason))
	}
	for index, pattern := range analysis.Patterns {
		if index == reportInteractionPatternLimit {
			view.More = len(analysis.Patterns) - index
			break
		}
		action, detail := pattern.ActionName, pattern.Detail
		if step := pattern.AuthoredStep; step.Valid() {
			action += " (" + step.StepPath + ")"
			detail += "; authored step " + step.StepID + "; script " + step.ScriptSHA256
		}
		if pattern.OutcomeKind != "" {
			detail += fmt.Sprintf("; outcome %s[%d]", pattern.OutcomeKind, pattern.OutcomeIndex)
		}
		if pattern.MembersTruncated {
			detail += fmt.Sprintf("; %d of %d interaction IDs listed", len(pattern.Interactions), pattern.Count)
		}
		view.Patterns = append(view.Patterns, reportInteractionPatternRow{Kind: pattern.Kind, Provenance: pattern.Provenance, Route: pattern.Route, Action: action, Count: fmt.Sprint(pattern.Count), Detail: detail})
	}
	for index, heatmap := range analysis.Heatmaps {
		if index == reportInteractionHeatmapLimit {
			view.MoreMaps = len(analysis.Heatmaps) - index
			break
		}
		viewport := fmt.Sprintf("%d×%d %s", heatmap.ViewportWidth, heatmap.ViewportHeight, heatmap.Orientation)
		if heatmap.DeviceScaleFactor > 0 {
			viewport += fmt.Sprintf(" @%gx", heatmap.DeviceScaleFactor)
		}
		if scroll := heatmap.Scroll; scroll != nil {
			viewport += fmt.Sprintf("; recorded scroll x %g–%g px, y %g–%g px", scroll.MinX, scroll.MaxX, scroll.MinY, scroll.MaxY)
		}
		switch heatmap.Observation {
		case "native_dispatch":
			viewport += "; native dispatch geometry"
		case "post_action":
			viewport += "; historical post-action geometry"
		}
		for _, note := range heatmap.Notes {
			view.Notes = append(view.Notes, fmt.Sprintf("Heatmap %s (%s): %s.", firstNonEmpty(heatmap.Route, "unrecorded route"), firstNonEmpty(heatmap.LayoutRevision, "unknown"), note))
		}
		view.Heatmaps = append(view.Heatmaps, reportInteractionHeatmapRow{Route: heatmap.Route, Layout: heatmap.LayoutRevision, Viewport: viewport,
			Engine: heatmap.Engine, Profile: heatmap.DeviceProfile, Gestures: fmt.Sprint(heatmap.Points + sumCounts(heatmap.Targets)),
			Cells: fmt.Sprintf("%d of %d×%d", len(heatmap.Cells), heatmap.Columns, heatmap.Rows), Targets: formatReportHeatmapTargets(heatmap.Targets)})
	}
	return view
}

func formatReportHeatmapTargets(targets []BrowserInteractionTargetCount) string {
	if len(targets) == 0 {
		return ""
	}
	parts := make([]string, 0, len(targets))
	for _, target := range targets {
		label := target.Role
		if target.LabelSHA256 != "" {
			short := target.LabelSHA256
			if len(short) > 12 {
				short = short[:12]
			}
			label += " (" + short + ")"
		}
		parts = append(parts, fmt.Sprintf("%s: %d", label, target.Count))
	}
	return strings.Join(parts, ", ")
}

// JourneyRiskView is the journey_risk section's view of the report.
func (d *runArchiveHTMLReportData) JourneyRiskView() reportJourneyRiskView {
	intelligence := d.browserIntelligence()
	if reason := browserIntelligenceUnavailable(intelligence, reportSectionJourneyRisk); reason != "" || intelligence.Journeys == nil {
		if reason == "" {
			reason = "The run's journeys were not accounted."
		}
		return reportJourneyRiskView{Summary: reason, NotCaptured: reason == journeyRiskNotPinned}
	}
	journeys := intelligence.Journeys
	if journeys.Availability != "available" {
		return reportJourneyRiskView{Summary: "Journey accounting is unavailable for this run: " + journeys.Reason + ".", Notes: append([]string(nil), journeys.Notes...)}
	}
	index := "unscored"
	if journeys.WeightedFailureIndex != nil {
		index = fmt.Sprintf("%.3f", *journeys.WeightedFailureIndex)
	} else if journeys.IndexUnscored != "" {
		index = "unscored (" + journeys.IndexUnscored + ")"
	}
	view := reportJourneyRiskView{Summary: fmt.Sprintf("Weighted failure index %s under %s revision %d.", index, firstNonEmpty(journeys.Policy.Name, journeys.Policy.ID), journeys.Policy.Revision), Notes: append([]string(nil), journeys.Notes...)}
	if journeys.Incomplete {
		view.Summary += " Incomplete: " + strings.Join(journeys.IncompleteReasons, "; ") + "."
	}
	for _, journey := range journeys.Journeys {
		rate := "unscored"
		if journey.FailureRate != nil {
			rate = fmt.Sprintf("%.1f%%", *journey.FailureRate*100)
		}
		view.Journeys = append(view.Journeys, reportJourneyRow{ID: journey.ID, Name: firstNonEmpty(journey.Name, journey.ID), Eligible: fmt.Sprint(journey.Eligible), Completed: fmt.Sprint(journey.Completed), Failed: fmt.Sprint(journey.Failed), Aborted: fmt.Sprint(journey.Aborted), Skipped: fmt.Sprint(journey.Skipped), Censored: fmt.Sprint(journey.Censored), FailureRate: rate})
	}
	return view
}

func nonEmptyStrings(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	return out
}

// ---- PDF and CSV -------------------------------------------------------------

func browserFindingsReportPDF(d *runArchiveHTMLReportData, lines *[]string) {
	view := d.BrowserFindingsView()
	pdfHeading(lines, reportSectionTitle(reportSectionBrowserFindings))
	*lines = append(*lines, view.Summary)
	for _, finding := range view.Findings {
		*lines = append(*lines, fmt.Sprintf("- %s: %s %s; observed %s. %s", finding.Title, finding.State, finding.Severity, finding.Observed, finding.Summary))
	}
	if view.More > 0 {
		*lines = append(*lines, fmt.Sprintf("%d more findings are in the run report.", view.More))
	}
	*lines = append(*lines, view.Notes...)
	*lines = append(*lines, "")
}

func browserFindingsReportCSV(d *runArchiveHTMLReportData, write reportCSVWriteFunc) error {
	view := d.BrowserFindingsView()
	rows := [][4]string{{"browser_findings", "", "summary", view.Summary}}
	for index, finding := range view.Findings {
		key := fmt.Sprint(index)
		rows = append(rows, [4]string{"browser_finding", key, "title", finding.Title}, [4]string{"browser_finding", key, "state", finding.State},
			[4]string{"browser_finding", key, "severity", finding.Severity}, [4]string{"browser_finding", key, "observed", finding.Observed}, [4]string{"browser_finding", key, "summary", finding.Summary})
	}
	if view.More > 0 {
		rows = append(rows, [4]string{"browser_findings", "", "more_findings", fmt.Sprint(view.More)})
	}
	return writeReportCSVRows(write, appendReportCSVNotes(rows, "browser_findings", view.Notes))
}

func interactionMapReportPDF(d *runArchiveHTMLReportData, lines *[]string) {
	view := d.InteractionMapView()
	pdfHeading(lines, reportSectionTitle(reportSectionInteractionMap))
	*lines = append(*lines, view.Summary)
	for _, pattern := range view.Patterns {
		*lines = append(*lines, fmt.Sprintf("- %s (%s) on %s, %s: %s gestures. %s", pattern.Kind, pattern.Provenance, firstNonEmpty(pattern.Route, "an unrecorded route"), firstNonEmpty(pattern.Action, "no action"), pattern.Count, pattern.Detail))
	}
	if view.More > 0 {
		*lines = append(*lines, fmt.Sprintf("%d more patterns are in the run report.", view.More))
	}
	for _, heatmap := range view.Heatmaps {
		line := fmt.Sprintf("Heatmap %s (%s) %s %s %s: %s gestures in %s cells", heatmap.Route, heatmap.Layout, heatmap.Viewport, heatmap.Engine, heatmap.Profile, heatmap.Gestures, heatmap.Cells)
		if heatmap.Targets != "" {
			line += "; targets: " + heatmap.Targets
		}
		*lines = append(*lines, line)
	}
	if view.MoreMaps > 0 {
		*lines = append(*lines, fmt.Sprintf("%d more heatmaps are in the run report.", view.MoreMaps))
	}
	*lines = append(*lines, view.Notes...)
	*lines = append(*lines, "")
}

func interactionMapReportCSV(d *runArchiveHTMLReportData, write reportCSVWriteFunc) error {
	view := d.InteractionMapView()
	rows := [][4]string{{"interaction_map", "", "summary", view.Summary}}
	for index, pattern := range view.Patterns {
		key := fmt.Sprint(index)
		for _, item := range [][2]string{{"kind", pattern.Kind}, {"provenance", pattern.Provenance}, {"route", pattern.Route}, {"action", pattern.Action}, {"count", pattern.Count}, {"detail", pattern.Detail}} {
			rows = append(rows, [4]string{"interaction_pattern", key, item[0], item[1]})
		}
	}
	for index, heatmap := range view.Heatmaps {
		key := fmt.Sprint(index)
		for _, item := range [][2]string{{"route", heatmap.Route}, {"layout", heatmap.Layout}, {"viewport", heatmap.Viewport}, {"engine", heatmap.Engine}, {"profile", heatmap.Profile}, {"gestures", heatmap.Gestures}, {"cells", heatmap.Cells}} {
			rows = append(rows, [4]string{"interaction_heatmap", key, item[0], item[1]})
		}
		if heatmap.Targets != "" {
			rows = append(rows, [4]string{"interaction_heatmap", key, "targets", heatmap.Targets})
		}
	}
	if view.More > 0 {
		rows = append(rows, [4]string{"interaction_map", "", "more_patterns", fmt.Sprint(view.More)})
	}
	if view.MoreMaps > 0 {
		rows = append(rows, [4]string{"interaction_map", "", "more_heatmaps", fmt.Sprint(view.MoreMaps)})
	}
	return writeReportCSVRows(write, appendReportCSVNotes(rows, "interaction_map", view.Notes))
}

func journeyRiskReportPDF(d *runArchiveHTMLReportData, lines *[]string) {
	view := d.JourneyRiskView()
	pdfHeading(lines, reportSectionTitle(reportSectionJourneyRisk))
	*lines = append(*lines, view.Summary)
	for _, journey := range view.Journeys {
		*lines = append(*lines, fmt.Sprintf("- %s (%s): %s eligible, %s completed, %s failed, %s aborted, %s skipped, %s censored; failure rate %s", journey.Name, journey.ID, journey.Eligible, journey.Completed, journey.Failed, journey.Aborted, journey.Skipped, journey.Censored, journey.FailureRate))
	}
	*lines = append(*lines, view.Notes...)
	*lines = append(*lines, "")
}

func journeyRiskReportCSV(d *runArchiveHTMLReportData, write reportCSVWriteFunc) error {
	view := d.JourneyRiskView()
	rows := [][4]string{{"journey_risk", "", "summary", view.Summary}}
	// Journey names may repeat; a journey's rows are keyed by its ID.
	for _, journey := range view.Journeys {
		for _, item := range [][2]string{{"name", journey.Name}, {"eligible", journey.Eligible}, {"completed", journey.Completed}, {"failed", journey.Failed}, {"aborted", journey.Aborted}, {"skipped", journey.Skipped}, {"censored", journey.Censored}, {"failure_rate", journey.FailureRate}} {
			rows = append(rows, [4]string{"journey", journey.ID, item[0], item[1]})
		}
	}
	return writeReportCSVRows(write, appendReportCSVNotes(rows, "journey_risk", view.Notes))
}

// appendReportCSVNotes adds a section's notes, as HTML and PDF carry them.
func appendReportCSVNotes(rows [][4]string, section string, notes []string) [][4]string {
	for index, note := range notes {
		rows = append(rows, [4]string{section, fmt.Sprint(index), "note", note})
	}
	return rows
}

func writeReportCSVRows(write reportCSVWriteFunc, rows [][4]string) error {
	for _, row := range rows {
		if err := write(row[0], row[1], row[2], row[3]); err != nil {
			return err
		}
	}
	return nil
}
