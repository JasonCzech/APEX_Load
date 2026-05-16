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

	"github.com/jasonczech/stormrunner/pkg/convert/jmeter"
	"github.com/jasonczech/stormrunner/pkg/convert/loadrunner"
	"github.com/jasonczech/stormrunner/pkg/convert/sample"
	"github.com/jasonczech/stormrunner/pkg/dsl"
)

type jmeterImportRequest struct {
	Data      []byte
	ProjectID string
	Opts      jmeter.ConvertOptions
}

type loadRunnerImportRequest struct {
	Data      []byte
	ProjectID string
	Opts      loadrunner.ConvertOptions
}

type loadRunnerImportSummary struct {
	ProjectFile              string   `json:"project_file,omitempty"`
	MappedRuntimeFile        string   `json:"mapped_runtime_file,omitempty"`
	ProjectSections          []string `json:"project_sections,omitempty"`
	RuntimeFiles             []string `json:"runtime_files,omitempty"`
	ParameterFiles           []string `json:"parameter_files,omitempty"`
	UnsupportedFunctionCount int      `json:"unsupported_function_count"`
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

	writeLoadRunnerImportResponse(w, http.StatusOK, result)
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

	writeLoadRunnerImportResponse(w, http.StatusOK, result)
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

func writeLoadRunnerImportResponse(w http.ResponseWriter, status int, result *loadrunner.ConvertResult) {
	unsupportedElements := uniqueSortedStrings(result.Stats.UnsupportedFuncs)
	summary := buildLoadRunnerImportSummary(result, unsupportedElements)
	reviewSections := buildLoadRunnerImportReviewSections(result, unsupportedElements, summary)
	repairSuggestions := buildLoadRunnerImportRepairSuggestions(result, unsupportedElements, summary)

	writeJSON(w, status, newScriptImportResponse(scriptImportResponseOptions{
		Result:              result,
		Script:              result.Script,
		Warnings:            result.Warnings,
		Stats:               result.Stats,
		UnsupportedElements: unsupportedElements,
		ReviewSections:      reviewSections,
		RepairSuggestions:   repairSuggestions,
		Summary:             summary,
	}))
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
			action = "Replace the JavaScript with browser_click, browser_fill, browser_wait, browser_swipe, or a supported assertion, then validate with browser replay."
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

	return suggestions
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
		case "browser_click", "browser_fill", "browser_tap", "browser_long_press", "browser_swipe":
			selector := firstNonEmpty(action.Selector, action.Target)
			if selector != "" {
				suggestions = append(suggestions, scriptImportRepairSuggestion{
					ID:           fmt.Sprintf("selector:%d:%s", index+1, stableReviewKey(actionID)),
					Category:     "selector",
					Severity:     "warning",
					Title:        "Validate selector for " + actionID,
					Description:  "Imported selectors can be brittle when they came from recorder object metadata or decomposed JavaScript.",
					Action:       "Prefer stable attributes such as data-testid, aria labels, or unique form names; confirm the selector with browser replay.",
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

func buildLoadRunnerImportReviewSections(result *loadrunner.ConvertResult, unsupportedElements []string, summary loadRunnerImportSummary) []scriptImportReviewSection {
	sections := make([]scriptImportReviewSection, 0, 4)
	if result == nil {
		return sections
	}

	warnings := uniqueSortedStrings(result.Warnings)
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

	if result.Script != nil && result.Script.Protocol == "browser" {
		sections = append(sections, scriptImportReviewSection{
			Key:      "browser_import_assumptions",
			Title:    "Browser Import Assumptions",
			Severity: "info",
			Summary:  "TruClient Mobile Web imports map supported steps into browser DSL and require replay validation for selectors, waits, and device behavior.",
			Items: []string{
				"Legacy desktop TruClient remains out of scope.",
				"Simple JavaScript fill/click statements are decomposed when possible; other JavaScript is preserved as review guidance.",
				"Selector fidelity should be checked with browser replay before production use.",
			},
		})
	}

	return sections
}

func buildLoadRunnerImportSummary(result *loadrunner.ConvertResult, unsupportedElements []string) loadRunnerImportSummary {
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

	return summary
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
