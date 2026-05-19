package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/jasonczech/stormrunner/pkg/convert/devweb"
	"github.com/jasonczech/stormrunner/pkg/convert/har"
	"github.com/jasonczech/stormrunner/pkg/convert/jmeter"
	"github.com/jasonczech/stormrunner/pkg/convert/loadrunner"
	"github.com/jasonczech/stormrunner/pkg/convert/sample"
	"github.com/jasonczech/stormrunner/pkg/convert/trueclient"
	"github.com/jasonczech/stormrunner/pkg/dsl"
)

type jmeterImportRequest struct {
	Data      []byte
	ProjectID string
	Opts      jmeter.ConvertOptions
}

type loadRunnerImportRequest struct {
	Data            []byte
	ProjectID       string
	Opts            loadrunner.ConvertOptions
	ReadinessPolicy loadrunner.RuntimeSettingMigrationPolicy
}

type devWebImportRequest struct {
	Data      []byte
	ProjectID string
	Name      string
	IsZip     bool
	Opts      devweb.ConvertOptions
}

type harImportRequest struct {
	Data      []byte
	ProjectID string
	Opts      har.ConvertOptions
}

type loadRunnerImportSummary struct {
	ProjectFile                 string                                       `json:"project_file,omitempty"`
	MappedRuntimeFile           string                                       `json:"mapped_runtime_file,omitempty"`
	ProjectSections             []string                                     `json:"project_sections,omitempty"`
	RuntimeFiles                []string                                     `json:"runtime_files,omitempty"`
	ParameterFiles              []string                                     `json:"parameter_files,omitempty"`
	RuntimeSettingsParity       []loadrunner.RuntimeSettingParityEntry       `json:"runtime_settings_parity,omitempty"`
	RuntimeSettingsStatusCounts map[string]int                               `json:"runtime_settings_status_counts,omitempty"`
	MigrationReadiness          *loadrunner.RuntimeSettingMigrationReadiness `json:"migration_readiness,omitempty"`
	TrueClientInspection        *trueclient.ArchiveInspection                `json:"trueclient_archive_inspection,omitempty"`
	SourceScriptType            string                                       `json:"source_script_type,omitempty"`
	SourceProfile               string                                       `json:"source_profile,omitempty"`
	UnsupportedFunctionCount    int                                          `json:"unsupported_function_count"`
}

// handleImportLoadRunner handles POST /api/v1/scripts/import/loadrunner
// Accepts a multipart form upload of a ZIP file containing a LoadRunner script folder.
func (s *Server) handleImportLoadRunner(w http.ResponseWriter, r *http.Request) {
	importReq, ok := readLoadRunnerImportRequest(w, r)
	if !ok {
		return
	}

	// Run the conversion
	result, err := loadrunner.ConvertZip(importReq.Data, importReq.Opts)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "conversion failed: "+err.Error())
		return
	}
	if result.Script != nil {
		result.Script.ProjectID = normalizeProjectID(importReq.ProjectID)
	}

	writeLoadRunnerImportResponse(w, http.StatusOK, result, importReq.ReadinessPolicy)
}

// handleValidateLoadRunnerImport handles POST /api/v1/scripts/import/loadrunner/validate.
// It parses and converts the ZIP payload without persisting any generated assets.
func (s *Server) handleValidateLoadRunnerImport(w http.ResponseWriter, r *http.Request) {
	importReq, ok := readLoadRunnerImportRequest(w, r)
	if !ok {
		return
	}

	result, err := loadrunner.ConvertZip(importReq.Data, importReq.Opts)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation failed: "+err.Error())
		return
	}
	if result.Script != nil {
		result.Script.ProjectID = normalizeProjectID(importReq.ProjectID)
	}

	writeLoadRunnerImportResponse(w, http.StatusOK, result, importReq.ReadinessPolicy)
}

// handleImportJMeter handles POST /api/v1/scripts/import/jmeter.
// Accepts a multipart form upload or raw body containing a JMeter .jmx test plan.
func (s *Server) handleImportJMeter(w http.ResponseWriter, r *http.Request) {
	importReq, ok := readJMeterImportRequest(w, r)
	if !ok {
		return
	}

	result, err := jmeter.ConvertJMX(importReq.Data, importReq.Opts)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "conversion failed: "+err.Error())
		return
	}
	for index := range result.Scripts {
		result.Scripts[index].ProjectID = normalizeProjectID(importReq.ProjectID)
	}
	if result.Script != nil {
		result.Script.ProjectID = normalizeProjectID(importReq.ProjectID)
	}

	writeJMeterImportResponse(w, http.StatusOK, result)
}

// handleValidateJMeterImport handles POST /api/v1/scripts/import/jmeter/validate.
// It parses and converts the JMX plan without persisting any generated assets.
func (s *Server) handleValidateJMeterImport(w http.ResponseWriter, r *http.Request) {
	importReq, ok := readJMeterImportRequest(w, r)
	if !ok {
		return
	}

	importReq.Opts.ScenarioDraft = r.FormValue("scenario_draft") == "true"
	result, err := jmeter.ConvertJMX(importReq.Data, importReq.Opts)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation failed: "+err.Error())
		return
	}
	for index := range result.Scripts {
		result.Scripts[index].ProjectID = normalizeProjectID(importReq.ProjectID)
	}
	if result.Script != nil {
		result.Script.ProjectID = normalizeProjectID(importReq.ProjectID)
	}

	writeJMeterImportResponse(w, http.StatusOK, result)
}

func (s *Server) handleJMeterSupportMatrix(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"support_matrix": jmeter.GetSupportMatrix(),
	})
}

func (s *Server) handleLoadRunnerSupportMatrix(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"support_matrix":            loadrunner.GetRTSSupportMatrix(),
		"notes":                     loadrunner.GetRTSSupportMatrixNotes(),
		"tested_examples":           loadrunner.GetRTSSupportMatrixExamples(),
		"trueclient_script_types":   trueclient.ScriptTypeScopes(),
		"trueclient_step_matrix":    trueclient.StepScopes(),
		"web2_profile_capabilities": trueclient.Web2ProfileCapabilityScopes(),
		"web2_fixture_contract":     trueclient.Web2FixtureContracts(),
	})
}

func (s *Server) handleAuthoringCapabilities(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, authoringCapabilityResponse())
}

// handleImportDevWeb handles POST /api/v1/scripts/import/devweb.
// Accepts JSON, raw .js, raw .zip, or multipart uploads and returns a generated script preview.
func (s *Server) handleImportDevWeb(w http.ResponseWriter, r *http.Request) {
	importReq, ok := readDevWebImportRequest(w, r)
	if !ok {
		return
	}

	var (
		result *devweb.ConvertResult
		err    error
	)
	if importReq.IsZip {
		result, err = devweb.ConvertZip(importReq.Data, importReq.Opts)
	} else {
		result, err = devweb.Convert(devweb.ImportRequest{
			Name:      importReq.Name,
			ProjectID: importReq.ProjectID,
			Content:   string(importReq.Data),
			Options:   importReq.Opts,
		})
	}
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "conversion failed: "+err.Error())
		return
	}
	if result.Script != nil {
		result.Script.ProjectID = normalizeProjectID(importReq.ProjectID)
	}
	issues := validateScript(result.Script)
	if len(issues) > 0 {
		result.Warnings = append(result.Warnings, issues...)
	}

	writeJSON(w, http.StatusOK, newScriptImportResponse(scriptImportResponseOptions{
		Result:   result,
		Script:   result.Script,
		Warnings: result.Warnings,
		Stats:    result.Stats,
	}))
}

// handleImportHAR handles POST /api/v1/scripts/import/har.
// Accepts JSON, raw HAR, or multipart uploads and returns a generated HTTP script preview.
func (s *Server) handleImportHAR(w http.ResponseWriter, r *http.Request) {
	importReq, ok := readHARImportRequest(w, r)
	if !ok {
		return
	}

	result, err := har.Convert(importReq.Data, importReq.Opts)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "conversion failed: "+err.Error())
		return
	}
	if result.Script != nil {
		result.Script.ProjectID = normalizeProjectID(importReq.ProjectID)
	}
	issues := validateScript(result.Script)
	if len(issues) > 0 {
		result.Warnings = append(result.Warnings, issues...)
	}

	writeJSON(w, http.StatusOK, newScriptImportResponse(scriptImportResponseOptions{
		Result:       result,
		Script:       result.Script,
		Warnings:     result.Warnings,
		Stats:        result.Stats,
		Correlations: result.Correlations,
	}))
}

func readHARImportRequest(w http.ResponseWriter, r *http.Request) (*harImportRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 50<<20)
	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "application/json") {
		var req struct {
			Name      string             `json:"name,omitempty"`
			ProjectID string             `json:"project_id,omitempty"`
			Content   string             `json:"content"`
			Options   har.ConvertOptions `json:"options,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return nil, false
		}
		req.Options.ScriptName = firstNonEmpty(req.Options.ScriptName, req.Name)
		return &harImportRequest{Data: []byte(req.Content), ProjectID: req.ProjectID, Opts: req.Options}, true
	}
	if strings.HasPrefix(contentType, "multipart/form-data") {
		if err := r.ParseMultipartForm(50 << 20); err != nil {
			writeError(w, http.StatusBadRequest, "invalid multipart form: "+err.Error())
			return nil, false
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			writeError(w, http.StatusBadRequest, "file field is required")
			return nil, false
		}
		defer file.Close()
		data, err := io.ReadAll(file)
		if err != nil {
			writeError(w, http.StatusBadRequest, "failed to read uploaded file: "+err.Error())
			return nil, false
		}
		return &harImportRequest{
			Data:      data,
			ProjectID: r.FormValue("project_id"),
			Opts: har.ConvertOptions{
				ScriptName: strings.TrimSuffix(header.Filename, filepath.Ext(header.Filename)),
				BaseURL:    r.FormValue("base_url"),
			},
		}, true
	}
	data, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body: "+err.Error())
		return nil, false
	}
	filename := r.URL.Query().Get("filename")
	return &harImportRequest{
		Data:      data,
		ProjectID: r.URL.Query().Get("project_id"),
		Opts: har.ConvertOptions{
			ScriptName: strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename)),
			BaseURL:    r.URL.Query().Get("base_url"),
		},
	}, true
}

func readDevWebImportRequest(w http.ResponseWriter, r *http.Request) (*devWebImportRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 20<<20)
	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "application/json") {
		var req devweb.ImportRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return nil, false
		}
		return &devWebImportRequest{
			Data:      []byte(req.Content),
			ProjectID: req.ProjectID,
			Name:      req.Name,
			Opts:      req.Options,
		}, true
	}

	if strings.HasPrefix(contentType, "multipart/form-data") {
		if err := r.ParseMultipartForm(20 << 20); err != nil {
			writeError(w, http.StatusBadRequest, "invalid multipart form: "+err.Error())
			return nil, false
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			writeError(w, http.StatusBadRequest, "file field is required")
			return nil, false
		}
		defer file.Close()
		data, err := io.ReadAll(file)
		if err != nil {
			writeError(w, http.StatusBadRequest, "failed to read uploaded file: "+err.Error())
			return nil, false
		}
		opts := devweb.ConvertOptions{
			BaseURL: r.FormValue("base_url"),
		}
		return &devWebImportRequest{
			Data:      data,
			ProjectID: r.FormValue("project_id"),
			Name:      strings.TrimSuffix(header.Filename, filepath.Ext(header.Filename)),
			IsZip:     strings.EqualFold(filepath.Ext(header.Filename), ".zip"),
			Opts:      opts,
		}, true
	}

	data, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body: "+err.Error())
		return nil, false
	}
	filename := r.URL.Query().Get("filename")
	return &devWebImportRequest{
		Data:      data,
		ProjectID: r.URL.Query().Get("project_id"),
		Name:      strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename)),
		IsZip:     strings.EqualFold(filepath.Ext(filename), ".zip") || strings.HasPrefix(contentType, "application/zip"),
		Opts: devweb.ConvertOptions{
			BaseURL: r.URL.Query().Get("base_url"),
		},
	}, true
}

func readJMeterImportRequest(w http.ResponseWriter, r *http.Request) (*jmeterImportRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 50<<20)

	contentType := r.Header.Get("Content-Type")
	var data []byte
	fileName := r.URL.Query().Get("filename")

	if strings.HasPrefix(contentType, "application/xml") || strings.HasPrefix(contentType, "text/xml") || strings.HasPrefix(contentType, "application/octet-stream") {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeError(w, http.StatusBadRequest, "failed to read request body: "+err.Error())
			return nil, false
		}
		data = body
	} else {
		if err := r.ParseMultipartForm(50 << 20); err != nil {
			writeError(w, http.StatusBadRequest, "failed to parse multipart form (max 50MB): "+err.Error())
			return nil, false
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			writeError(w, http.StatusBadRequest, "missing 'file' field in multipart form: "+err.Error())
			return nil, false
		}
		defer file.Close()
		if header != nil {
			fileName = header.Filename
		}
		body, err := io.ReadAll(file)
		if err != nil {
			writeError(w, http.StatusBadRequest, "failed to read uploaded file: "+err.Error())
			return nil, false
		}
		data = body
	}

	if len(data) == 0 {
		writeError(w, http.StatusBadRequest, "empty file uploaded")
		return nil, false
	}

	return &jmeterImportRequest{
		Data:      data,
		ProjectID: firstNonEmpty(r.FormValue("project_id"), r.URL.Query().Get("project_id")),
		Opts: jmeter.ConvertOptions{
			ScriptName:        r.FormValue("name"),
			BaseURL:           r.FormValue("base_url"),
			IncludeAssertions: r.FormValue("include_assertions") == "true",
			ScenarioDraft:     r.FormValue("scenario_draft") == "true",
			FileName:          fileName,
		},
	}, true
}

func readLoadRunnerImportRequest(w http.ResponseWriter, r *http.Request) (*loadRunnerImportRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 50<<20)

	contentType := r.Header.Get("Content-Type")
	var data []byte

	if strings.HasPrefix(contentType, "application/zip") ||
		strings.HasPrefix(contentType, "application/x-zip-compressed") ||
		strings.HasPrefix(contentType, "application/octet-stream") {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeError(w, http.StatusBadRequest, "failed to read request body: "+err.Error())
			return nil, false
		}
		data = body
	} else {
		if err := r.ParseMultipartForm(50 << 20); err != nil {
			writeError(w, http.StatusBadRequest, "failed to parse multipart form (max 50MB): "+err.Error())
			return nil, false
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			writeError(w, http.StatusBadRequest, "missing 'file' field in multipart form: "+err.Error())
			return nil, false
		}
		defer file.Close()
		body, err := io.ReadAll(file)
		if err != nil {
			writeError(w, http.StatusBadRequest, "failed to read uploaded file: "+err.Error())
			return nil, false
		}
		data = body
	}

	if len(data) == 0 {
		writeError(w, http.StatusBadRequest, "empty file uploaded")
		return nil, false
	}

	return &loadRunnerImportRequest{
		Data:      data,
		ProjectID: firstNonEmpty(r.FormValue("project_id"), r.URL.Query().Get("project_id")),
		Opts: loadrunner.ConvertOptions{
			ScriptName:           r.FormValue("name"),
			BaseURL:              r.FormValue("base_url"),
			IncludeAssertions:    r.FormValue("include_assertions") == "true",
			PreserveTransactions: parseBoolDefault(r.FormValue("preserve_transactions"), true),
		},
		ReadinessPolicy: loadrunner.ParseRuntimeSettingMigrationPolicy(r.FormValue("migration_block_statuses")),
	}, true
}

func parseBoolDefault(value string, defaultValue bool) bool {
	if value == "" {
		return defaultValue
	}

	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return defaultValue
	}
}

func writeLoadRunnerImportResponse(w http.ResponseWriter, status int, result *loadrunner.ConvertResult, readinessPolicy loadrunner.RuntimeSettingMigrationPolicy) {
	unsupportedElements := uniqueSortedStrings(result.Stats.UnsupportedFuncs)
	warnings := loadRunnerImportWarnings(result)
	summary := buildLoadRunnerImportSummary(result, unsupportedElements, readinessPolicy)
	persistLoadRunnerImportSummary(result, summary)
	reviewSections := buildLoadRunnerImportReviewSections(warnings, result, unsupportedElements, summary)
	repairSuggestions := buildLoadRunnerImportRepairSuggestions(result, unsupportedElements, summary)

	writeJSON(w, status, newScriptImportResponse(scriptImportResponseOptions{
		Result:              result,
		Script:              result.Script,
		Warnings:            warnings,
		Stats:               result.Stats,
		UnsupportedElements: unsupportedElements,
		ReviewSections:      reviewSections,
		RepairSuggestions:   repairSuggestions,
		Summary:             summary,
	}))
}

func loadRunnerImportWarnings(result *loadrunner.ConvertResult) []string {
	if result == nil {
		return nil
	}
	warnings := append([]string(nil), result.Warnings...)
	if result.Script != nil && hasStreamingCompatibilityOnlySettings(result.Script) {
		warnings = append(warnings, streamingCompatibilityWarning(result.Script))
	}
	return uniqueSortedStrings(warnings)
}

func buildLoadRunnerImportRepairSuggestions(result *loadrunner.ConvertResult, unsupportedElements []string, summary loadRunnerImportSummary) []scriptImportRepairSuggestion {
	suggestions := make([]scriptImportRepairSuggestion, 0)
	if result == nil {
		return suggestions
	}

	for _, element := range unsupportedElements {
		normalized := strings.ToLower(element)
		description := "Review the preserved source construct and replace it with equivalent Stormrunner DSL behavior before relying on this script."
		action := "Open the generated script in the visual editor, inspect preserved metadata, and either remove the unsupported construct or model it with supported actions."
		if strings.Contains(normalized, "evaluate javascript") {
			description = "This TruClient JavaScript statement could not be decomposed into a supported browser action."
			action = "Replace the JavaScript with browser_click, browser_fill, browser_select, browser_wait, browser_swipe, or a supported assertion, then validate with browser replay."
			if loadRunnerWarningsContain(result, "window.scrollto(") {
				description = "This TruClient JavaScript scroll statement could not be decomposed safely into a supported browser action."
				action = "Replace the JavaScript with browser_swipe and any required browser_wait actions, then validate the resulting scroll behavior with browser replay."
			}
		}
		suggestions = append(suggestions, scriptImportRepairSuggestion{
			ID:          "unsupported:" + stableReviewKey(element),
			Category:    "unsupported",
			Severity:    "blocker",
			Title:       "Replace unsupported " + element,
			Description: description,
			Action:      action,
		})
	}

	if result.Script != nil && result.Script.Protocol == "browser" {
		suggestions = append(suggestions, browserImportRepairSuggestions(result.Script)...)
	}

	if isWeb2TrueClientInspection(summary.TrueClientInspection) {
		suggestions = append(suggestions, trueClientWeb2RepairSuggestions(summary)...)
	}

	if len(summary.RuntimeFiles) > 0 || summary.MappedRuntimeFile != "" {
		suggestions = append(suggestions, scriptImportRepairSuggestion{
			ID:          "runtime:review-preserved-settings",
			Category:    "runtime",
			Severity:    "info",
			Title:       "Review preserved runtime settings",
			Description: "The source archive included LoadRunner runtime metadata that may not all execute natively in Stormrunner yet.",
			Action:      "Compare the preserved runtime file and project settings against the generated Stormrunner config before promoting the script.",
		})
	}

	if summary.MigrationReadiness != nil && summary.MigrationReadiness.Blocked {
		suggestions = append(suggestions, scriptImportRepairSuggestion{
			ID:          "runtime:migration-readiness",
			Category:    "runtime",
			Severity:    "blocker",
			Title:       "Resolve blocking runtime settings before promotion",
			Description: "The selected migration block policy matched imported runtime settings that are not fully executable in Stormrunner yet.",
			Action:      "Review the blocking runtime settings, adjust the generated Stormrunner config where possible, or relax the migration block policy before saving the script.",
		})
	}

	return suggestions
}

func trueClientWeb2RepairSuggestions(summary loadRunnerImportSummary) []scriptImportRepairSuggestion {
	inspection := summary.TrueClientInspection
	if inspection == nil {
		return nil
	}
	suggestions := []scriptImportRepairSuggestion{
		{
			ID:          "web2:replay-evidence",
			Category:    "replay",
			Severity:    "warning",
			Title:       "Attach browser replay evidence",
			Description: "TruClient Web 2.0 imports are review-only until selector, wait, runtime, and screenshot evidence is captured.",
			Action:      "Run browser replay from the visual editor and attach the replay report before approving the imported draft.",
		},
		{
			ID:          "web2:object-identification",
			Category:    "selector",
			Severity:    "warning",
			Title:       "Review Web 2.0 object identification",
			Description: fmt.Sprintf("%d object repository file(s) were preserved, but descriptor ranking and fallback selector evidence still require review.", len(inspection.ObjectRepositories)),
			Action:      "Open the object-identification review section, compare imported selectors with replay evidence, and stabilize brittle selectors before promotion.",
		},
		{
			ID:          "web2:http-conversion",
			Category:    "conversion",
			Severity:    "info",
			Title:       "Gate browser-to-HTTP conversion",
			Description: "Web 2.0 browser drafts should not be promoted to high-density HTTP load until network capture and correlation findings are reviewed.",
			Action:      "Capture replay traffic, review conversion blockers and correlation candidates, then generate an HTTP draft only for eligible flows.",
		},
	}
	if len(inspection.ParameterFiles) > 0 {
		suggestions = append(suggestions, scriptImportRepairSuggestion{
			ID:          "web2:parameter-fit",
			Category:    "data",
			Severity:    "info",
			Title:       "Verify Web 2.0 parameter data fit",
			Description: fmt.Sprintf("%d parameter file(s) were discovered for the imported Web 2.0 flow.", len(inspection.ParameterFiles)),
			Action:      "Open parameter data in the script editor, confirm row strategy and exhaustion behavior, and replay with representative data.",
		})
	}
	if len(inspection.UnsupportedFragments) > 0 {
		severity := "warning"
		for _, fragment := range inspection.UnsupportedFragments {
			if fragment.Status == trueclient.ScopeOutOfScope {
				severity = "blocker"
				break
			}
		}
		suggestions = append(suggestions, scriptImportRepairSuggestion{
			ID:          "web2:source-fragments",
			Category:    "unsupported",
			Severity:    severity,
			Title:       "Resolve Web 2.0 source fragments",
			Description: fmt.Sprintf("%d review-only or unsupported TruClient fragment(s) were preserved from the source archive.", len(inspection.UnsupportedFragments)),
			Action:      "Use the Web 2.0 source-fragment review section to replace unsupported JavaScript/API behavior with supported browser actions or documented manual steps.",
		})
	}
	return suggestions
}

func loadRunnerWarningsContain(result *loadrunner.ConvertResult, needle string) bool {
	if result == nil {
		return false
	}
	needle = strings.ToLower(strings.TrimSpace(needle))
	if needle == "" {
		return false
	}
	for _, warning := range result.Warnings {
		if strings.Contains(strings.ToLower(warning), needle) {
			return true
		}
	}
	return false
}

func trueClientWeb2ReviewSections(summary loadRunnerImportSummary) []scriptImportReviewSection {
	inspection := summary.TrueClientInspection
	if inspection == nil {
		return nil
	}
	sourceType := firstNonEmpty(summary.SourceScriptType, inspection.ScriptType)
	sourceProfile := summary.SourceProfile
	if sourceProfile == "" && inspection.ScriptTypeScope != nil {
		sourceProfile = inspection.ScriptTypeScope.StormrunnerShape
	}
	items := []string{
		fmt.Sprintf("Source script type: %s", firstNonEmpty(sourceType, "TruClient archive")),
		fmt.Sprintf("Source profile: %s", firstNonEmpty(sourceProfile, "browser profile")),
		fmt.Sprintf("Executable steps inspected: %d", inspection.StepInventory.Total),
		fmt.Sprintf("Supported step families: %d", len(inspection.StepInventory.Supported)),
	}
	if len(inspection.StepInventory.ReviewOnly) > 0 {
		items = append(items, fmt.Sprintf("Review-only step families: %d", len(inspection.StepInventory.ReviewOnly)))
	}
	if len(inspection.StepInventory.Unsupported) > 0 {
		items = append(items, fmt.Sprintf("Unsupported step families: %d", len(inspection.StepInventory.Unsupported)))
	}
	sections := []scriptImportReviewSection{{
		Key:      "web2_profile_summary",
		Title:    "VuGen Web 2.0 Profile Summary",
		Severity: "warning",
		Summary:  "This TruClient Web 2.0 archive was converted into a review-only browser draft.",
		Items:    items,
	}}

	if len(inspection.UnsupportedFragments) > 0 {
		fragmentItems := make([]string, 0, len(inspection.UnsupportedFragments))
		severity := "warning"
		for _, fragment := range inspection.UnsupportedFragments {
			if fragment.Status == trueclient.ScopeOutOfScope {
				severity = "blocker"
			}
			label := fragment.Action
			if fragment.Index != "" {
				label += " #" + fragment.Index
			}
			fragmentItems = append(fragmentItems, fmt.Sprintf("%s: %s", label, fragment.Reason))
		}
		sections = append(sections, scriptImportReviewSection{
			Key:      "web2_unsupported_source_fragments",
			Title:    "Web 2.0 Source Fragments",
			Severity: severity,
			Summary:  "Review-only or unsupported TruClient fragments were preserved for repair before approval.",
			Items:    fragmentItems,
		})
	}

	objectItems := make([]string, 0, len(inspection.ObjectRepositories)+1)
	for _, file := range inspection.ObjectRepositories {
		objectItems = append(objectItems, "Object repository: "+file.Path)
	}
	if candidateCount := trueClientSelectorCandidateCount(inspection); candidateCount > 0 {
		objectItems = append(objectItems, fmt.Sprintf("Selector candidates extracted: %d", candidateCount))
	}
	objectItems = append(objectItems, "Imported selectors require browser replay before promotion because object descriptor ranking is not complete.")
	sections = append(sections, scriptImportReviewSection{
		Key:      "web2_object_identification",
		Title:    "Object Identification",
		Severity: "warning",
		Summary:  fmt.Sprintf("%d object repository file(s) were preserved for selector review.", len(inspection.ObjectRepositories)),
		Items:    objectItems,
	})

	if len(inspection.ParameterFiles) > 0 {
		parameterItems := make([]string, 0, len(inspection.ParameterFiles))
		for _, file := range inspection.ParameterFiles {
			parameterItems = append(parameterItems, "Parameter data: "+file.Path)
		}
		sections = append(sections, scriptImportReviewSection{
			Key:      "web2_parameter_mapping",
			Title:    "Parameter Mapping",
			Severity: "info",
			Summary:  "Parameter files were discovered and preserved for data-source fit review.",
			Items:    parameterItems,
		})
	}

	sections = append(sections, scriptImportReviewSection{
		Key:      "web2_replay_readiness",
		Title:    "Replay Readiness",
		Severity: "warning",
		Summary:  "Browser replay evidence is required before this review-only Web 2.0 draft is approved.",
		Items: []string{
			"Run browser replay against the target application.",
			"Review screenshots, selector failures, wait behavior, and runtime settings parity.",
			"Do not promote to a reusable production asset until replay evidence is attached.",
		},
	})
	sections = append(sections, scriptImportReviewSection{
		Key:      "web2_http_conversion_eligibility",
		Title:    "HTTP Conversion Eligibility",
		Severity: "info",
		Summary:  "Browser-to-HTTP conversion remains a governed follow-up flow for Web 2.0 imports.",
		Items: []string{
			"Capture network requests during replay before generating an HTTP draft.",
			"Review correlation candidates and conversion blockers before using HTTP for density load.",
		},
	})
	return sections
}

func isWeb2TrueClientInspection(inspection *trueclient.ArchiveInspection) bool {
	if inspection == nil || inspection.ScriptTypeScope == nil {
		return false
	}
	return inspection.ScriptTypeScope.CanonicalName == "truclient2_web" || inspection.ScriptTypeScope.CanonicalName == "truclient_web"
}

func trueClientSelectorCandidateCount(inspection *trueclient.ArchiveInspection) int {
	if inspection == nil {
		return 0
	}
	count := 0
	for _, step := range inspection.Steps {
		count += len(step.SelectorCandidates)
	}
	return count
}

func browserImportRepairSuggestions(script *dsl.Script) []scriptImportRepairSuggestion {
	suggestions := []scriptImportRepairSuggestion{
		{
			ID:          "browser:replay-validation",
			Category:    "replay",
			Severity:    "warning",
			Title:       "Run browser replay before saving as production-ready",
			Description: "Imported browser scripts depend on source selectors, waits, and device assumptions that should be verified against a live page.",
			Action:      "Open the imported draft in the visual editor and run browser replay. Update selectors or waits for any failed action.",
		},
	}

	for index, action := range script.Actions {
		actionID := firstNonEmpty(action.Name, fmt.Sprintf("action_%d", index+1))
		switch action.Type {
		case "browser_click", "browser_fill", "browser_select", "browser_tap", "browser_long_press", "browser_swipe":
			selector := firstNonEmpty(action.Selector, action.Target)
			if selector != "" {
				score, signals := browserSelectorStabilityScore(selector)
				suggestions = append(suggestions, scriptImportRepairSuggestion{
					ID:                     fmt.Sprintf("selector:%d:%s", index+1, stableReviewKey(actionID)),
					Category:               "selector",
					Severity:               "warning",
					Title:                  "Validate selector for " + actionID,
					Description:            fmt.Sprintf("Imported selector stability score is %d/100. Recorder object metadata and decomposed JavaScript selectors should be checked before promotion.", score),
					Action:                 browserSelectorRepairAction(score),
					TargetAction:           actionID,
					SelectorStabilityScore: score,
					SelectorSignals:        signals,
				})
			}
			if action.WaitFor == nil {
				suggestions = append(suggestions, scriptImportRepairSuggestion{
					ID:           fmt.Sprintf("wait-after:%d:%s", index+1, stableReviewKey(actionID)),
					Category:     "wait",
					Severity:     "info",
					Title:        "Add readiness wait after " + actionID,
					Description:  "Browser interaction steps imported without a readiness condition can race dynamic pages during replay.",
					Action:       "Attach a wait_for visible/text/url condition to the action or insert a browser_wait step using the page-ready signal seen in replay.",
					TargetAction: actionID,
				})
			}
		case "browser_wait":
			waitType := ""
			if action.WaitFor != nil {
				waitType = action.WaitFor.Type
			}
			suggestions = append(suggestions, scriptImportRepairSuggestion{
				ID:           fmt.Sprintf("wait:%d:%s", index+1, stableReviewKey(actionID)),
				Category:     "wait",
				Severity:     "info",
				Title:        "Review wait condition for " + actionID,
				Description:  "Imported waits may represent timing intent rather than the most stable page-ready condition.",
				Action:       "Prefer visible selector or text waits over fixed durations when the application exposes a reliable readiness signal.",
				TargetAction: firstNonEmpty(actionID, waitType),
			})
		}
	}

	return suggestions
}

func browserSelectorRepairAction(score int) string {
	if score >= 80 {
		return "Keep the selector if replay is stable; prefer data-testid or aria attributes for future recorder output."
	}
	if score >= 60 {
		return "Prefer stable attributes such as data-testid, aria labels, or unique form names; confirm the selector with browser replay."
	}
	return "Replace this selector with a stable data-testid, aria label, role/name, or unique form-name selector before approving the migrated script."
}

func browserSelectorStabilityScore(selector string) (int, []string) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return 0, []string{"empty selector"}
	}
	lower := strings.ToLower(selector)
	score := 45
	signals := make([]string, 0, 5)

	if strings.Contains(lower, "data-testid") || strings.Contains(lower, "data-test") || strings.Contains(lower, "data-qa") {
		score += 35
		signals = append(signals, "stable test attribute")
	}
	if strings.Contains(lower, "aria-label") || strings.Contains(lower, "role=") {
		score += 25
		signals = append(signals, "accessible selector")
	}
	if strings.Contains(lower, "[name=") || strings.Contains(lower, "[name'") || strings.Contains(lower, "[name\"") {
		score += 20
		signals = append(signals, "named form field")
	}
	if strings.HasPrefix(selector, "#") && !looksGeneratedSelectorToken(selector) {
		score += 20
		signals = append(signals, "simple id selector")
	}
	if strings.Contains(lower, ":nth-child") || strings.Contains(lower, ":nth-of-type") {
		score -= 30
		signals = append(signals, "positional selector")
	}
	if strings.HasPrefix(lower, "/html") || strings.HasPrefix(lower, "html > body") || strings.HasPrefix(lower, "body >") {
		score -= 25
		signals = append(signals, "absolute DOM path")
	}
	if looksGeneratedSelectorToken(selector) {
		score -= 20
		signals = append(signals, "generated-looking token")
	}
	if strings.Contains(selector, " ") || strings.Contains(selector, ">") {
		score -= 5
		signals = append(signals, "composite selector")
	}
	if len(signals) == 0 {
		signals = append(signals, "generic selector")
	}
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}
	return score, signals
}

func looksGeneratedSelectorToken(selector string) bool {
	token := strings.ToLower(selector)
	digitCount := 0
	separatorCount := 0
	for _, r := range token {
		switch {
		case r >= '0' && r <= '9':
			digitCount++
		case r == '-' || r == '_' || r == ':':
			separatorCount++
		}
	}
	if digitCount >= 4 && separatorCount >= 1 {
		return true
	}
	return strings.Contains(token, "ember") || strings.Contains(token, "react-select") || strings.Contains(token, "generated")
}

func stableReviewKey(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "unknown"
	}
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func buildLoadRunnerImportReviewSections(warnings []string, result *loadrunner.ConvertResult, unsupportedElements []string, summary loadRunnerImportSummary) []scriptImportReviewSection {
	sections := make([]scriptImportReviewSection, 0, 4)
	if result == nil {
		return sections
	}

	if len(warnings) > 0 {
		sections = append(sections, scriptImportReviewSection{
			Key:      "warnings",
			Title:    "Conversion Warnings",
			Severity: "warning",
			Summary:  "Review converter warnings before saving or replaying the imported draft.",
			Items:    warnings,
		})
	}

	if len(unsupportedElements) > 0 {
		sections = append(sections, scriptImportReviewSection{
			Key:      "unsupported_elements",
			Title:    "Unsupported LoadRunner Elements",
			Severity: "blocker",
			Summary:  "These source constructs were preserved for review but were not converted into executable Stormrunner actions.",
			Items:    unsupportedElements,
		})
	}

	preservedItems := make([]string, 0)
	if summary.ProjectFile != "" {
		preservedItems = append(preservedItems, "Project file: "+summary.ProjectFile)
	}
	if summary.MappedRuntimeFile != "" {
		preservedItems = append(preservedItems, "Mapped runtime file: "+summary.MappedRuntimeFile)
	}
	for _, parameterFile := range summary.ParameterFiles {
		preservedItems = append(preservedItems, "Parameter file: "+parameterFile)
	}
	for _, runtimeFile := range summary.RuntimeFiles {
		preservedItems = append(preservedItems, "Runtime file: "+runtimeFile)
	}
	for _, projectSection := range summary.ProjectSections {
		preservedItems = append(preservedItems, "Project section: "+projectSection)
	}
	if len(preservedItems) > 0 {
		sections = append(sections, scriptImportReviewSection{
			Key:      "preserved_metadata",
			Title:    "Preserved LoadRunner Metadata",
			Severity: "info",
			Summary:  "Source project metadata was saved with the generated script for audit and repair workflows.",
			Items:    preservedItems,
		})
	}

	if len(summary.RuntimeSettingsParity) > 0 {
		sections = append(sections, scriptImportReviewSection{
			Key:      "runtime_settings_parity",
			Title:    "Runtime Settings Parity",
			Severity: runtimeSettingsParitySeverity(summary.RuntimeSettingsParity),
			Summary:  runtimeSettingsParitySummary(summary.RuntimeSettingsStatusCounts),
			Items:    runtimeSettingsParityItems(summary.RuntimeSettingsParity),
		})
	}

	if summary.MigrationReadiness != nil {
		sections = append(sections, scriptImportReviewSection{
			Key:      "runtime_settings_migration_readiness",
			Title:    "Migration Readiness",
			Severity: migrationReadinessSeverity(summary.MigrationReadiness),
			Summary:  summary.MigrationReadiness.Summary,
			Items:    migrationReadinessItems(summary.MigrationReadiness),
		})
	}

	if isWeb2TrueClientInspection(summary.TrueClientInspection) {
		sections = append(sections, trueClientWeb2ReviewSections(summary)...)
	}

	if result.Script != nil && result.Script.Protocol == "browser" {
		sections = append(sections, scriptImportReviewSection{
			Key:      "browser_import_assumptions",
			Title:    "Browser Import Assumptions",
			Severity: "info",
			Summary:  "TruClient Mobile Web imports map supported steps into browser DSL and require replay validation for selectors, waits, and device behavior; TruClient Web and Web 2.0 are tracked as separate planned profiles.",
			Items: []string{
				"Legacy desktop TruClient remains out of scope; TruClient - Web and TruClient 2.0 - Web are planned under the VuGen Web 2.0 profile.",
				"Simple JavaScript fill/click statements are decomposed when possible; other JavaScript is preserved as review guidance.",
				"Selector fidelity should be checked with browser replay before production use.",
			},
		})
	}

	return sections
}

func buildLoadRunnerImportSummary(result *loadrunner.ConvertResult, unsupportedElements []string, readinessPolicy loadrunner.RuntimeSettingMigrationPolicy) loadRunnerImportSummary {
	summary := loadRunnerImportSummary{
		UnsupportedFunctionCount: len(unsupportedElements),
	}

	if result == nil || result.Script == nil || result.Script.Metadata == nil || result.Script.Metadata.Import == nil {
		return summary
	}

	preserved := result.Script.Metadata.Import.Preserved
	if preserved == nil {
		return summary
	}

	summary.ProjectFile = stringValue(preserved["project_file"])
	summary.MappedRuntimeFile = stringValue(preserved["mapped_runtime_file"])
	summary.ProjectSections = sortedRecordKeys(preserved["project_settings"])
	summary.RuntimeFiles = sortedRecordKeys(preserved["runtime_files"])
	summary.ParameterFiles = stringSliceValue(preserved["parameter_files"])
	summary.RuntimeSettingsParity = runtimeSettingsParityValue(preserved["runtime_settings_parity"])
	summary.RuntimeSettingsStatusCounts = intMapValue(preserved["runtime_settings_status_counts"])
	summary.MigrationReadiness = loadRunnerMigrationReadinessValue(preserved["runtime_settings_migration_readiness"])
	summary.TrueClientInspection = trueClientArchiveInspectionValue(preserved["trueclient_archive_inspection"])
	summary.SourceScriptType = stringValue(preserved["source_script_type"])
	summary.SourceProfile = stringValue(preserved["source_profile"])
	if summary.MigrationReadiness == nil && len(summary.RuntimeSettingsParity) > 0 {
		summary.MigrationReadiness = loadrunner.BuildRTSMigrationReadiness(summary.RuntimeSettingsParity, readinessPolicy)
	}

	return summary
}

func persistLoadRunnerImportSummary(result *loadrunner.ConvertResult, summary loadRunnerImportSummary) {
	if result == nil || result.Script == nil || result.Script.Metadata == nil || result.Script.Metadata.Import == nil {
		return
	}
	if result.Script.Metadata.Import.Preserved == nil {
		result.Script.Metadata.Import.Preserved = make(map[string]any)
	}
	if summary.MigrationReadiness != nil {
		result.Script.Metadata.Import.Preserved["runtime_settings_migration_readiness"] = summary.MigrationReadiness
	}
}

func runtimeSettingsParityValue(value any) []loadrunner.RuntimeSettingParityEntry {
	switch typed := value.(type) {
	case []loadrunner.RuntimeSettingParityEntry:
		return append([]loadrunner.RuntimeSettingParityEntry(nil), typed...)
	case []any:
		entries := make([]loadrunner.RuntimeSettingParityEntry, 0, len(typed))
		for _, item := range typed {
			mapping, ok := item.(map[string]any)
			if !ok {
				continue
			}
			entry := loadrunner.RuntimeSettingParityEntry{
				ID:           stringValue(mapping["id"]),
				Area:         stringValue(mapping["area"]),
				Concept:      stringValue(mapping["concept"]),
				Fields:       stringSliceValue(mapping["fields"]),
				Status:       loadrunner.RuntimeSettingStatus(stringValue(mapping["status"])),
				Notes:        stringValue(mapping["notes"]),
				RuntimeFiles: stringSliceValue(mapping["runtime_files"]),
			}
			if entry.ID == "" {
				continue
			}
			entries = append(entries, entry)
		}
		return entries
	default:
		return nil
	}
}

func loadRunnerMigrationReadinessValue(value any) *loadrunner.RuntimeSettingMigrationReadiness {
	switch typed := value.(type) {
	case *loadrunner.RuntimeSettingMigrationReadiness:
		return typed
	case loadrunner.RuntimeSettingMigrationReadiness:
		clone := typed
		return &clone
	case map[string]any:
		readiness := &loadrunner.RuntimeSettingMigrationReadiness{
			ScorePct:         intValue(typed["score_pct"]),
			Blocked:          boolValue(typed["blocked"]),
			Summary:          stringValue(typed["summary"]),
			BlockingStatuses: runtimeSettingStatusSliceValue(typed["blocking_statuses"]),
			BlockingSettings: runtimeSettingsParityValue(typed["blocking_settings"]),
			Policy: loadrunner.RuntimeSettingMigrationPolicy{
				BlockStatuses: runtimeSettingPolicyStatuses(typed["policy"]),
			},
		}
		if readiness.ScorePct == 0 && !readiness.Blocked && readiness.Summary == "" && len(readiness.BlockingStatuses) == 0 && len(readiness.BlockingSettings) == 0 && len(readiness.Policy.BlockStatuses) == 0 {
			return nil
		}
		return readiness
	default:
		return nil
	}
}

func trueClientArchiveInspectionValue(value any) *trueclient.ArchiveInspection {
	switch typed := value.(type) {
	case *trueclient.ArchiveInspection:
		return typed
	case trueclient.ArchiveInspection:
		return &typed
	default:
		return nil
	}
}

func intMapValue(value any) map[string]int {
	switch typed := value.(type) {
	case map[string]int:
		if len(typed) == 0 {
			return nil
		}
		clone := make(map[string]int, len(typed))
		for key, count := range typed {
			clone[key] = count
		}
		return clone
	case map[string]any:
		if len(typed) == 0 {
			return nil
		}
		clone := make(map[string]int, len(typed))
		for key, raw := range typed {
			switch count := raw.(type) {
			case int:
				clone[key] = count
			case float64:
				clone[key] = int(count)
			}
		}
		if len(clone) == 0 {
			return nil
		}
		return clone
	default:
		return nil
	}
}

func intValue(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case float64:
		return int(typed)
	default:
		return 0
	}
}

func boolValue(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return parseBoolDefault(typed, false)
	default:
		return false
	}
}

func runtimeSettingStatusSliceValue(value any) []loadrunner.RuntimeSettingStatus {
	switch typed := value.(type) {
	case []loadrunner.RuntimeSettingStatus:
		return append([]loadrunner.RuntimeSettingStatus(nil), typed...)
	case []any:
		statuses := make([]loadrunner.RuntimeSettingStatus, 0, len(typed))
		for _, item := range typed {
			status := loadrunner.RuntimeSettingStatus(stringValue(item))
			if status == "" {
				continue
			}
			statuses = append(statuses, status)
		}
		return statuses
	default:
		return nil
	}
}

func runtimeSettingPolicyStatuses(value any) []loadrunner.RuntimeSettingStatus {
	mapping, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	return runtimeSettingStatusSliceValue(mapping["block_statuses"])
}

func runtimeSettingsParitySeverity(entries []loadrunner.RuntimeSettingParityEntry) string {
	severity := "info"
	for _, entry := range entries {
		switch entry.Status {
		case loadrunner.RuntimeSettingStatusBlocked:
			return "blocker"
		case loadrunner.RuntimeSettingStatusPartial, loadrunner.RuntimeSettingStatusCompatibilityOnly:
			severity = "warning"
		}
	}
	return severity
}

func runtimeSettingsParitySummary(counts map[string]int) string {
	if len(counts) == 0 {
		return "Imported runtime settings were classified against Stormrunner's code-owned LoadRunner RTS matrix."
	}
	parts := make([]string, 0, 5)
	for _, status := range []string{
		string(loadrunner.RuntimeSettingStatusExecuted),
		string(loadrunner.RuntimeSettingStatusPartial),
		string(loadrunner.RuntimeSettingStatusCompatibilityOnly),
		string(loadrunner.RuntimeSettingStatusBlocked),
		string(loadrunner.RuntimeSettingStatusNonApplicable),
	} {
		if counts[status] == 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf("%d %s", counts[status], strings.ReplaceAll(status, "_", "-")))
	}
	if len(parts) == 0 {
		return "Imported runtime settings were classified against Stormrunner's code-owned LoadRunner RTS matrix."
	}
	return "Imported runtime settings were classified against Stormrunner's code-owned LoadRunner RTS matrix: " + strings.Join(parts, ", ") + "."
}

func runtimeSettingsParityItems(entries []loadrunner.RuntimeSettingParityEntry) []string {
	items := make([]string, 0, len(entries))
	for _, entry := range entries {
		item := fmt.Sprintf("%s: %s / %s", runtimeSettingStatusLabel(entry.Status), entry.Area, entry.Concept)
		if len(entry.RuntimeFiles) > 0 {
			item += " [" + strings.Join(entry.RuntimeFiles, ", ") + "]"
		}
		items = append(items, item)
	}
	return items
}

func migrationReadinessSeverity(readiness *loadrunner.RuntimeSettingMigrationReadiness) string {
	if readiness == nil {
		return "info"
	}
	if readiness.Blocked {
		return "blocker"
	}
	if readiness.ScorePct < 85 {
		return "warning"
	}
	return "info"
}

func migrationReadinessItems(readiness *loadrunner.RuntimeSettingMigrationReadiness) []string {
	if readiness == nil {
		return nil
	}
	items := []string{
		fmt.Sprintf("Score: %d%%", readiness.ScorePct),
		"Block policy: " + runtimeSettingStatusLabels(readiness.Policy.BlockStatuses),
	}
	if readiness.Blocked {
		items = append(items, "Matched blocking statuses: "+runtimeSettingStatusLabels(readiness.BlockingStatuses))
		for _, entry := range readiness.BlockingSettings {
			items = append(items, fmt.Sprintf("Blocking setting: %s / %s", entry.Area, entry.Concept))
		}
	} else {
		items = append(items, "No imported runtime settings matched the selected block statuses.")
	}
	return items
}

func runtimeSettingStatusLabels(statuses []loadrunner.RuntimeSettingStatus) string {
	if len(statuses) == 0 {
		return "None"
	}
	labels := make([]string, 0, len(statuses))
	for _, status := range statuses {
		labels = append(labels, runtimeSettingStatusLabel(status))
	}
	return strings.Join(labels, ", ")
}

func runtimeSettingStatusLabel(status loadrunner.RuntimeSettingStatus) string {
	switch status {
	case loadrunner.RuntimeSettingStatusExecuted:
		return "Executed"
	case loadrunner.RuntimeSettingStatusPartial:
		return "Partial"
	case loadrunner.RuntimeSettingStatusCompatibilityOnly:
		return "Compatibility-only"
	case loadrunner.RuntimeSettingStatusBlocked:
		return "Blocked"
	case loadrunner.RuntimeSettingStatusNonApplicable:
		return "Non-applicable"
	default:
		label := strings.ReplaceAll(string(status), "_", " ")
		if label == "" {
			return "Unknown"
		}
		return strings.ToUpper(label[:1]) + label[1:]
	}
}

func stringSliceValue(value any) []string {
	switch typed := value.(type) {
	case []string:
		return uniqueSortedStrings(typed)
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		return uniqueSortedStrings(result)
	default:
		return nil
	}
}

func uniqueSortedStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func sortedRecordKeys(value any) []string {
	v := reflect.ValueOf(value)
	if !v.IsValid() || v.Kind() != reflect.Map || v.Type().Key().Kind() != reflect.String {
		return nil
	}

	keys := make([]string, 0, v.Len())
	for _, key := range v.MapKeys() {
		keys = append(keys, key.String())
	}
	sort.Strings(keys)
	return keys
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func writeJMeterImportResponse(w http.ResponseWriter, status int, result *jmeter.ConvertResult) {
	writeJSON(w, status, newScriptImportResponse(scriptImportResponseOptions{
		Result:              result,
		Scripts:             result.Scripts,
		Script:              result.Script,
		ScenarioDraft:       result.ScenarioDraft,
		Warnings:            result.Warnings,
		UnsupportedElements: result.UnsupportedElements,
		Assumptions:         result.Assumptions,
		Stats:               result.Stats,
		Summary:             result.Summary,
	}))
}

func (s *Server) handleSaveJMeterImport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Scripts       []dsl.Script          `json:"scripts"`
		ScenarioDraft *jmeter.ScenarioDraft `json:"scenario_draft,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if len(req.Scripts) == 0 {
		writeError(w, http.StatusBadRequest, "at least one script is required")
		return
	}

	storedScripts := make([]*StoredScript, 0, len(req.Scripts))
	scriptIDsByName := map[string]string{}
	for i := range req.Scripts {
		script := req.Scripts[i]
		if issues := validateScript(&script); len(issues) > 0 {
			writeError(w, http.StatusBadRequest, strings.Join(issues, "; "))
			return
		}
		stored := s.runner.StoreScript(&script)
		storedScripts = append(storedScripts, stored)
		scriptIDsByName[script.Name] = stored.ID
	}

	var storedScenario *StoredScenario
	if req.ScenarioDraft != nil && len(req.ScenarioDraft.Groups) > 0 {
		groups := make([]CreateGroupEntry, 0, len(req.ScenarioDraft.Groups))
		for _, group := range req.ScenarioDraft.Groups {
			scriptID := scriptIDsByName[group.ScriptName]
			if scriptID == "" {
				continue
			}
			groups = append(groups, CreateGroupEntry{
				Name:         group.Name,
				ScriptID:     scriptID,
				VUsers:       group.VUsers,
				RampUp:       group.RampUp,
				InitStrategy: firstNonEmpty(group.Initialization, "at_runtime"),
			})
		}
		if len(groups) > 0 {
			scenario, err := s.runner.StoreScenario(CreateScenarioRequest{
				Name:         firstNonEmpty(req.ScenarioDraft.Name, "Imported JMeter Scenario"),
				Duration:     firstNonEmpty(req.ScenarioDraft.Duration, "5m"),
				ScenarioMode: firstNonEmpty(req.ScenarioDraft.ScenarioMode, "vuser_driven"),
				Groups:       groups,
			})
			if err != nil {
				writeError(w, http.StatusBadRequest, "failed to save scenario draft: "+err.Error())
				return
			}
			storedScenario = scenario
		}
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"scripts":  storedScripts,
		"scenario": storedScenario,
		"count":    len(storedScripts),
	})
}

// handleImportSample handles POST /api/v1/scripts/import/sample.
// Accepts a JSON payload containing a supported sample format and returns a generated script preview.
func (s *Server) handleImportSample(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 5<<20)

	var req sample.ImportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	result, err := sample.Convert(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "sample conversion failed: "+err.Error())
		return
	}
	if result.Script != nil {
		result.Script.ProjectID = normalizeProjectID(req.ProjectID)
	}

	issues := validateScript(result.Script)
	if len(issues) > 0 {
		result.Warnings = append(result.Warnings, issues...)
	}

	writeJSON(w, http.StatusOK, newScriptImportResponse(scriptImportResponseOptions{
		Result:       result,
		Script:       result.Script,
		Warnings:     result.Warnings,
		Stats:        result.Stats,
		Correlations: result.Correlations,
	}))
}

// handleExportScript handles POST /api/v1/scripts/{id}/export
// Exports a script as a JSON DSL file to disk.
func (s *Server) handleExportScript(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	stored, ok := s.runner.GetScript(id)
	if !ok {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}

	// Optional: specify output directory via request body
	var req struct {
		OutputDir string `json:"output_dir,omitempty"`
		Filename  string `json:"filename,omitempty"`
	}
	if r.Body != nil {
		json.NewDecoder(r.Body).Decode(&req)
	}

	outputDir := req.OutputDir
	if outputDir == "" {
		outputDir = "." // Current working directory
	}

	filename := req.Filename
	if filename == "" {
		filename = sanitizeFilename(stored.Name) + ".json"
	}

	// Wrap in the standard script envelope
	envelope := map[string]any{
		"script": stored.Script,
	}

	data, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to marshal script: "+err.Error())
		return
	}

	fullPath := filepath.Join(outputDir, filename)

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create output directory: "+err.Error())
		return
	}

	if err := os.WriteFile(fullPath, data, 0644); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to write script file: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"exported": true,
		"path":     fullPath,
		"filename": filename,
		"size":     len(data),
	})
}

// handleDownloadScript handles GET /api/v1/scripts/{id}/download
// Returns the script JSON as a downloadable file.
func (s *Server) handleDownloadScript(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	stored, ok := s.runner.GetScript(id)
	if !ok {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}

	envelope := map[string]any{
		"script": stored.Script,
	}

	data, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to marshal script: "+err.Error())
		return
	}

	filename := sanitizeFilename(stored.Name) + ".json"
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

// handleUpdateScript handles PUT /api/v1/scripts/{id}
// Updates an existing script in-place.
func (s *Server) handleUpdateScript(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var wrapper struct {
		Script dsl.Script `json:"script"`
	}
	if err := json.NewDecoder(r.Body).Decode(&wrapper); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	script := &wrapper.Script

	if issues := validateScript(script); len(issues) > 0 {
		writeError(w, http.StatusBadRequest, strings.Join(issues, "; "))
		return
	}

	updated, ok := s.runner.UpdateScript(id, script)
	if !ok {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	s.recordAuditEvent(r, "script.update", "success", "script", updated.ID, updated.ProjectID, map[string]any{"name": updated.Name, "folder_id": updated.FolderID})

	writeJSON(w, http.StatusOK, updated)
}

// handleDeleteScript handles DELETE /api/v1/scripts/{id}
func (s *Server) handleDeleteScript(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	stored, ok := s.runner.GetScript(id)
	if !ok {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	if !s.runner.DeleteScript(id) {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	s.recordAuditEvent(r, "script.delete", "success", "script", id, stored.ProjectID, map[string]any{"name": stored.Name, "folder_id": stored.FolderID})
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
}

// sanitizeFilename makes a string safe for use as a filename.
func sanitizeFilename(name string) string {
	replacer := strings.NewReplacer(
		" ", "_",
		"/", "_",
		"\\", "_",
		":", "_",
		"*", "_",
		"?", "_",
		"\"", "_",
		"<", "_",
		">", "_",
		"|", "_",
	)
	result := replacer.Replace(name)
	if result == "" {
		result = "script"
	}
	return result
}
