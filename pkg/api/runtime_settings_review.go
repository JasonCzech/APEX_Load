package api

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/loadrunner"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
)

type RuntimeSettingsReview struct {
	Status           string                   `json:"status"`
	Summary          string                   `json:"summary,omitempty"`
	StatusCounts     map[string]int           `json:"status_counts,omitempty"`
	Findings         []RuntimeSettingsFinding `json:"findings,omitempty"`
	BlockingFindings []RuntimeSettingsFinding `json:"blocking_findings,omitempty"`
	Warnings         []string                 `json:"warnings,omitempty"`
}

type RuntimeSettingsFinding struct {
	ID                string   `json:"id"`
	Scope             string   `json:"scope"`
	Group             string   `json:"group,omitempty"`
	ScriptID          string   `json:"script_id,omitempty"`
	ScriptName        string   `json:"script_name,omitempty"`
	Protocol          string   `json:"protocol,omitempty"`
	Area              string   `json:"area,omitempty"`
	Concept           string   `json:"concept,omitempty"`
	Status            string   `json:"status"`
	Severity          string   `json:"severity"`
	Summary           string   `json:"summary"`
	RecommendedAction string   `json:"recommended_action,omitempty"`
	Evidence          []string `json:"evidence,omitempty"`
}

type RuntimeSettingsReport struct {
	SchemaVersion string                                 `json:"schema_version"`
	GeneratedAt   time.Time                              `json:"generated_at"`
	Scope         string                                 `json:"scope"`
	TargetID      string                                 `json:"target_id"`
	TargetName    string                                 `json:"target_name,omitempty"`
	Status        string                                 `json:"status"`
	Summary       string                                 `json:"summary,omitempty"`
	StatusCounts  map[string]int                         `json:"status_counts,omitempty"`
	Entries       []loadrunner.RuntimeSettingParityEntry `json:"entries,omitempty"`
	Findings      []RuntimeSettingsFinding               `json:"findings,omitempty"`
	Presets       []RuntimeSettingsProfilePreset         `json:"presets,omitempty"`
	Support       RuntimeSettingsReportSupportRefs       `json:"support"`
	Warnings      []string                               `json:"warnings,omitempty"`
}

type RuntimeSettingsReportSupportRefs struct {
	MatrixEndpoint string   `json:"matrix_endpoint"`
	ExportFormats  []string `json:"export_formats"`
	Docs           []string `json:"docs,omitempty"`
}

func buildRuntimeSettingsReviewForScript(scope string, scriptID string, group string, script *dsl.Script) RuntimeSettingsReview {
	review := RuntimeSettingsReview{Status: "ready"}
	if script == nil {
		review.Summary = "No runtime settings were available for review."
		return review
	}
	parity := buildValidationRuntimeSettingsParity(script)
	review.StatusCounts = loadrunner.RuntimeSettingStatusCounts(parity)
	for _, entry := range parity {
		if entry.Status == loadrunner.RuntimeSettingStatusExecuted || entry.Status == loadrunner.RuntimeSettingStatusNonApplicable {
			continue
		}
		finding := RuntimeSettingsFinding{
			ID:                entry.ID,
			Scope:             scope,
			Group:             group,
			ScriptID:          scriptID,
			ScriptName:        script.Name,
			Protocol:          script.Protocol,
			Area:              entry.Area,
			Concept:           entry.Concept,
			Status:            string(entry.Status),
			Severity:          runtimeSettingSeverity(entry.Status),
			Summary:           runtimeSettingFindingSummary(entry),
			RecommendedAction: runtimeSettingRecommendedAction(entry),
			Evidence:          runtimeSettingEntryEvidence(entry),
		}
		review.Findings = append(review.Findings, finding)
		if finding.Severity == "blocker" {
			review.BlockingFindings = append(review.BlockingFindings, finding)
		}
	}

	for _, warning := range validationWarnings(script) {
		finding := runtimeSettingWarningFinding(scope, scriptID, group, script, warning)
		review.Findings = append(review.Findings, finding)
		review.Warnings = append(review.Warnings, warning)
		if finding.Severity == "blocker" {
			review.BlockingFindings = append(review.BlockingFindings, finding)
		}
	}

	finalizeRuntimeSettingsReview(&review)
	return review
}

func buildRuntimeSettingsScenarioReview(groups []TestGroup, engineGroups []engine.GroupConfig) *RuntimeSettingsReview {
	if len(groups) == 0 && len(engineGroups) == 0 {
		return nil
	}
	review := &RuntimeSettingsReview{Status: "ready"}
	for index, engineGroup := range engineGroups {
		scriptID := ""
		groupName := engineGroup.Name
		if index < len(groups) {
			scriptID = groups[index].ScriptID
			if groupName == "" {
				groupName = groups[index].Name
			}
		}
		groupReview := buildRuntimeSettingsReviewForScript("scenario", scriptID, groupName, engineGroup.Script)
		review.Findings = append(review.Findings, groupReview.Findings...)
		review.BlockingFindings = append(review.BlockingFindings, groupReview.BlockingFindings...)
		review.Warnings = append(review.Warnings, groupReview.Warnings...)
		review.StatusCounts = mergeRuntimeSettingStatusCounts(review.StatusCounts, groupReview.StatusCounts)
	}
	finalizeRuntimeSettingsReview(review)
	if len(review.Findings) == 0 && len(review.StatusCounts) == 0 {
		return nil
	}
	return review
}

func finalizeRuntimeSettingsReview(review *RuntimeSettingsReview) {
	if review == nil {
		return
	}
	review.Findings = uniqueRuntimeSettingsFindings(review.Findings)
	review.BlockingFindings = uniqueRuntimeSettingsFindings(review.BlockingFindings)
	review.Warnings = uniqueStringsPreserveOrder(review.Warnings)
	if len(review.BlockingFindings) > 0 {
		review.Status = "blocked"
		review.Summary = fmt.Sprintf("%d runtime setting blocker%s must be repaired before execution.", len(review.BlockingFindings), pluralSuffix(len(review.BlockingFindings)))
		return
	}
	warnings := 0
	for _, finding := range review.Findings {
		if finding.Severity == "warning" {
			warnings++
		}
	}
	if warnings > 0 {
		review.Status = "needs_review"
		review.Summary = fmt.Sprintf("%d runtime setting warning%s should be reviewed before execution.", warnings, pluralSuffix(warnings))
		return
	}
	review.Status = "ready"
	if len(review.StatusCounts) > 0 {
		review.Summary = "Runtime settings are executable for the selected scripts and overrides."
	} else {
		review.Summary = "No non-default runtime settings require review."
	}
}

func runtimeSettingSeverity(status loadrunner.RuntimeSettingStatus) string {
	switch status {
	case loadrunner.RuntimeSettingStatusBlocked:
		return "blocker"
	case loadrunner.RuntimeSettingStatusPartial, loadrunner.RuntimeSettingStatusCompatibilityOnly:
		return "warning"
	case loadrunner.RuntimeSettingStatusNonApplicable:
		return "info"
	default:
		return "info"
	}
}

func runtimeSettingFindingSummary(entry loadrunner.RuntimeSettingParityEntry) string {
	status := strings.ReplaceAll(string(entry.Status), "_", " ")
	if len(entry.RuntimeFiles) > 0 {
		return fmt.Sprintf("%s runtime setting %s / %s from %s.", strings.Title(status), entry.Area, entry.Concept, strings.Join(entry.RuntimeFiles, ", "))
	}
	return fmt.Sprintf("%s runtime setting %s / %s.", strings.Title(status), entry.Area, entry.Concept)
}

func runtimeSettingRecommendedAction(entry loadrunner.RuntimeSettingParityEntry) string {
	switch entry.Status {
	case loadrunner.RuntimeSettingStatusBlocked:
		return "Change or remove this setting before saving or running the scenario."
	case loadrunner.RuntimeSettingStatusCompatibilityOnly:
		return "Keep for migration documentation, but do not treat it as executed behavior."
	case loadrunner.RuntimeSettingStatusPartial:
		return "Review the supported subset and attach replay evidence before promotion."
	default:
		return "Review the setting against the unified runtime support matrix."
	}
}

func runtimeSettingEntryEvidence(entry loadrunner.RuntimeSettingParityEntry) []string {
	evidence := []string{"/api/v1/runtime-settings/support-matrix"}
	if len(entry.RuntimeFiles) > 0 {
		evidence = append(evidence, entry.RuntimeFiles...)
	}
	return evidence
}

func runtimeSettingWarningFinding(scope string, scriptID string, group string, script *dsl.Script, warning string) RuntimeSettingsFinding {
	finding := RuntimeSettingsFinding{
		ID:                "runtime_settings_warning",
		Scope:             scope,
		Group:             group,
		ScriptID:          scriptID,
		Status:            "needs_review",
		Severity:          "warning",
		Summary:           warning,
		RecommendedAction: "Review the runtime setting before saving or running the scenario.",
		Evidence:          []string{"/api/v1/runtime-settings/support-matrix"},
	}
	if script != nil {
		finding.ScriptName = script.Name
		finding.Protocol = script.Protocol
	}
	if strings.Contains(warning, "download_resources=recorded") || strings.Contains(warning, "recorded-resource metadata") {
		finding.ID = "browser_recorded_resources_metadata"
		finding.Area = "Browser Runtime"
		finding.Concept = "Recorded-only resource metadata"
		finding.Status = "blocked"
		finding.Severity = "blocker"
		finding.RecommendedAction = "Import or record the script with recorded-resource URL metadata, or switch download_resources to all/none."
	}
	if strings.Contains(warning, "streaming") || strings.Contains(warning, "media") {
		finding.ID = "streaming"
		finding.Area = "Streaming"
		finding.Concept = "Adaptive or fixed playback settings"
		finding.Status = "compatibility_only"
		finding.RecommendedAction = "Preserve the setting for migration documentation; do not use it as release-gate media playback evidence."
	}
	return finding
}

func mergeRuntimeSettingStatusCounts(left map[string]int, right map[string]int) map[string]int {
	if len(right) == 0 {
		return left
	}
	if left == nil {
		left = make(map[string]int, len(right))
	}
	for key, value := range right {
		left[key] += value
	}
	return left
}

func uniqueRuntimeSettingsFindings(in []RuntimeSettingsFinding) []RuntimeSettingsFinding {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]RuntimeSettingsFinding, 0, len(in))
	for _, finding := range in {
		key := strings.Join([]string{finding.Scope, finding.Group, finding.ScriptID, finding.ID, finding.Summary}, "\x00")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, finding)
	}
	return out
}

func uniqueStringsPreserveOrder(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func cloneRuntimeSettingsReview(in *RuntimeSettingsReview) *RuntimeSettingsReview {
	if in == nil {
		return nil
	}
	out := &RuntimeSettingsReview{
		Status:           in.Status,
		Summary:          in.Summary,
		StatusCounts:     cloneIntMap(in.StatusCounts),
		Findings:         cloneRuntimeSettingsFindings(in.Findings),
		BlockingFindings: cloneRuntimeSettingsFindings(in.BlockingFindings),
		Warnings:         cloneStringSlice(in.Warnings),
	}
	return out
}

func cloneRuntimeSettingsFindings(in []RuntimeSettingsFinding) []RuntimeSettingsFinding {
	if len(in) == 0 {
		return nil
	}
	out := make([]RuntimeSettingsFinding, len(in))
	for i, finding := range in {
		out[i] = finding
		out[i].Evidence = cloneStringSlice(finding.Evidence)
	}
	return out
}

func cloneIntMap(in map[string]int) map[string]int {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]int, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func runtimeSettingsReportForScript(scriptID string, stored *StoredScript) RuntimeSettingsReport {
	report := RuntimeSettingsReport{
		SchemaVersion: runtimeSettingsReportSchemaVersion,
		GeneratedAt:   time.Now().UTC(),
		Scope:         "script",
		TargetID:      scriptID,
		Support:       runtimeSettingsReportSupportRefs(),
		Presets:       runtimeSettingsProfilePresets(),
	}
	if stored == nil || stored.Script == nil {
		report.Status = "blocked"
		report.Summary = "Script not found."
		return report
	}
	report.TargetName = stored.Name
	report.Entries = buildValidationRuntimeSettingsParity(stored.Script)
	review := buildRuntimeSettingsReviewForScript("script", scriptID, "", stored.Script)
	report.Status = review.Status
	report.Summary = review.Summary
	report.StatusCounts = review.StatusCounts
	report.Findings = review.Findings
	report.Warnings = review.Warnings
	return report
}

func runtimeSettingsReportForScenario(scenarioID string, scenario *StoredScenario, scripts map[string]*StoredScript) RuntimeSettingsReport {
	report := RuntimeSettingsReport{
		SchemaVersion: runtimeSettingsReportSchemaVersion,
		GeneratedAt:   time.Now().UTC(),
		Scope:         "scenario",
		TargetID:      scenarioID,
		Support:       runtimeSettingsReportSupportRefs(),
		Presets:       runtimeSettingsProfilePresets(),
	}
	if scenario == nil {
		report.Status = "blocked"
		report.Summary = "Scenario not found."
		return report
	}
	report.TargetName = scenario.Name
	statusCounts := map[string]int{}
	for _, ref := range scenario.Scripts {
		stored := scripts[ref.ScriptID]
		if stored == nil || stored.Script == nil {
			finding := RuntimeSettingsFinding{
				ID:                "missing_script",
				Scope:             "scenario",
				Group:             ref.Name,
				ScriptID:          ref.ScriptID,
				ScriptName:        ref.ScriptName,
				Protocol:          ref.Protocol,
				Status:            "blocked",
				Severity:          "blocker",
				Summary:           fmt.Sprintf("Scenario references missing script %s.", ref.ScriptID),
				RecommendedAction: "Restore or replace the missing script before execution.",
			}
			report.Findings = append(report.Findings, finding)
			continue
		}
		scriptCopy := *stored.Script
		if scenario.RuntimeSettings != nil {
			_ = applyRuntimeOverrides(&scriptCopy, scenario.RuntimeSettings)
		}
		entries := buildValidationRuntimeSettingsParity(&scriptCopy)
		report.Entries = append(report.Entries, entries...)
		statusCounts = mergeRuntimeSettingStatusCounts(statusCounts, loadrunner.RuntimeSettingStatusCounts(entries))
		review := buildRuntimeSettingsReviewForScript("scenario", ref.ScriptID, ref.Name, &scriptCopy)
		report.Findings = append(report.Findings, review.Findings...)
		report.Warnings = append(report.Warnings, review.Warnings...)
	}
	report.StatusCounts = statusCounts
	report.Findings = uniqueRuntimeSettingsFindings(report.Findings)
	report.Warnings = uniqueStringsPreserveOrder(report.Warnings)
	report.Status = "ready"
	if hasRuntimeSettingsBlocker(report.Findings) {
		report.Status = "blocked"
		report.Summary = "Scenario runtime settings include blockers that must be repaired before execution."
	} else if hasRuntimeSettingsWarning(report.Findings) {
		report.Status = "needs_review"
		report.Summary = "Scenario runtime settings include warnings that should be reviewed before execution."
	} else {
		report.Summary = "Scenario runtime settings are executable for the selected scripts and overrides."
	}
	return report
}

func runtimeSettingsReportSupportRefs() RuntimeSettingsReportSupportRefs {
	return RuntimeSettingsReportSupportRefs{
		MatrixEndpoint: "/api/v1/runtime-settings/support-matrix",
		ExportFormats:  []string{"json", "csv"},
		Docs:           []string{"docs/RUNTIME_SETTINGS_PARITY.md", "docs/LOADRUNNER_RTS_REFERENCE.md"},
	}
}

func hasRuntimeSettingsBlocker(findings []RuntimeSettingsFinding) bool {
	for _, finding := range findings {
		if finding.Severity == "blocker" || finding.Status == "blocked" {
			return true
		}
	}
	return false
}

func hasRuntimeSettingsWarning(findings []RuntimeSettingsFinding) bool {
	for _, finding := range findings {
		if finding.Severity == "warning" {
			return true
		}
	}
	return false
}

func (s *Server) handleGetScriptRuntimeSettingsReport(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	stored, ok := s.runner.GetScript(id)
	if !ok {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	report := runtimeSettingsReportForScript(id, stored)
	writeRuntimeSettingsReport(w, r, report)
}

func (s *Server) handleGetScenarioRuntimeSettingsReport(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	scenario, ok := s.runner.GetScenario(id)
	if !ok {
		writeError(w, http.StatusNotFound, "scenario not found")
		return
	}
	scripts := make(map[string]*StoredScript, len(scenario.Scripts))
	for _, ref := range scenario.Scripts {
		if stored, ok := s.runner.GetScript(ref.ScriptID); ok {
			scripts[ref.ScriptID] = stored
		}
	}
	report := runtimeSettingsReportForScenario(id, scenario, scripts)
	writeRuntimeSettingsReport(w, r, report)
}

func writeRuntimeSettingsReport(w http.ResponseWriter, r *http.Request, report RuntimeSettingsReport) {
	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	if format == "" || format == "json" {
		writeJSON(w, http.StatusOK, report)
		return
	}
	if format != "csv" {
		writeError(w, http.StatusBadRequest, "unsupported runtime settings report format")
		return
	}
	data, err := runtimeSettingsReportCSV(report)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-runtime-settings.csv"`, report.TargetID))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func runtimeSettingsReportCSV(report RuntimeSettingsReport) ([]byte, error) {
	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)
	rows := [][]string{
		{"scope", "target_id", "target_name", "status", "summary"},
		{report.Scope, report.TargetID, report.TargetName, report.Status, report.Summary},
		{},
		{"entry_id", "area", "concept", "status", "fields", "runtime_files", "notes"},
	}
	for _, entry := range report.Entries {
		rows = append(rows, []string{
			entry.ID,
			entry.Area,
			entry.Concept,
			string(entry.Status),
			strings.Join(entry.Fields, "; "),
			strings.Join(entry.RuntimeFiles, "; "),
			entry.Notes,
		})
	}
	rows = append(rows, []string{}, []string{"finding_id", "severity", "status", "scope", "group", "script_id", "script_name", "protocol", "summary", "recommended_action"})
	for _, finding := range report.Findings {
		rows = append(rows, []string{
			finding.ID,
			finding.Severity,
			finding.Status,
			finding.Scope,
			finding.Group,
			finding.ScriptID,
			finding.ScriptName,
			finding.Protocol,
			finding.Summary,
			finding.RecommendedAction,
		})
	}
	for _, row := range rows {
		if err := writer.Write(row); err != nil {
			return nil, err
		}
	}
	writer.Flush()
	return buf.Bytes(), writer.Error()
}

func runtimeSettingsReportEntriesSorted(entries []loadrunner.RuntimeSettingParityEntry) []loadrunner.RuntimeSettingParityEntry {
	out := make([]loadrunner.RuntimeSettingParityEntry, len(entries))
	copy(out, entries)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Area == out[j].Area {
			return out[i].ID < out[j].ID
		}
		return out[i].Area < out[j].Area
	})
	return out
}
