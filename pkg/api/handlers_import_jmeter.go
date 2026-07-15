package api

// handlers_import_jmeter — split from handlers_scripts.go (same package, behavior-preserving).

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/jmeter"
	"github.com/jasonczech/apexload/pkg/dsl"
)

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
	attachSourceReviewToScripts(result.Scripts, "jmeter", result.Summary.FileName, result.SourceReview)

	writeJMeterImportResponse(w, http.StatusOK, result)
}

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
	attachSourceReviewToScripts(result.Scripts, "jmeter", result.Summary.FileName, result.SourceReview)

	writeJMeterImportResponse(w, http.StatusOK, result)
}

func (s *Server) handleJMeterSupportMatrix(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		// Flat arrays retained for backward compatibility with existing clients.
		"support_matrix": jmeter.GetSupportMatrix(),
		// Structured entries (mirrors the AsyncAPI/k6/DevWeb importers) carry
		// per-construct status, execution_status, and review flags.
		"support_matrix_entries": jmeter.GetSupportMatrixEntries(),
		"notes":                  jmeter.GetSupportMatrixNotes(),
	})
}

func readJMeterImportRequest(w http.ResponseWriter, r *http.Request) (*jmeterImportRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, jmeterImportMaxBodyBytes)

	contentType := r.Header.Get("Content-Type")
	var data []byte
	fileName := r.URL.Query().Get("filename")
	sidecarFiles := map[string][]byte{}

	if strings.HasPrefix(contentType, "application/xml") || strings.HasPrefix(contentType, "text/xml") || strings.HasPrefix(contentType, "application/octet-stream") || strings.HasPrefix(contentType, "application/zip") || strings.HasPrefix(contentType, "application/x-zip-compressed") {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeError(w, http.StatusBadRequest, "failed to read request body: "+err.Error())
			return nil, false
		}
		data = body
	} else {
		if err := r.ParseMultipartForm(importMultipartMemoryBytes); err != nil {
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
		sidecarFiles = readJMeterSidecarFiles(r, "file", fileName)
	}

	if len(data) == 0 {
		writeError(w, http.StatusBadRequest, "empty file uploaded")
		return nil, false
	}
	if isJMeterZipUpload(fileName, contentType) {
		jmxData, jmxFileName, zipSidecars, err := readJMeterZipImport(data)
		if err != nil {
			writeError(w, http.StatusBadRequest, "failed to read JMeter ZIP: "+err.Error())
			return nil, false
		}
		data = jmxData
		fileName = jmxFileName
		sidecarFiles = mergeJMeterSidecarFiles(sidecarFiles, zipSidecars)
	}

	return &jmeterImportRequest{
		Data:      data,
		ProjectID: authorizedImportProjectID(r),
		Opts: jmeter.ConvertOptions{
			ScriptName:        r.FormValue("name"),
			BaseURL:           r.FormValue("base_url"),
			IncludeAssertions: r.FormValue("include_assertions") == "true",
			ScenarioDraft:     r.FormValue("scenario_draft") == "true",
			FileName:          fileName,
			SidecarFiles:      sidecarFiles,
		},
	}, true
}

func isJMeterZipUpload(fileName string, contentType string) bool {
	return strings.EqualFold(filepath.Ext(fileName), ".zip") ||
		strings.HasPrefix(contentType, "application/zip") ||
		strings.HasPrefix(contentType, "application/x-zip-compressed")
}

func readJMeterZipImport(data []byte) ([]byte, string, map[string][]byte, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, "", nil, err
	}
	jmxFiles := map[string][]byte{}
	sidecars := map[string][]byte{}
	for _, entry := range reader.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		file, err := entry.Open()
		if err != nil {
			continue
		}
		content, err := io.ReadAll(file)
		_ = file.Close()
		if err != nil {
			continue
		}
		name := filepath.ToSlash(entry.Name)
		switch strings.ToLower(filepath.Ext(name)) {
		case ".jmx":
			jmxFiles[name] = content
		case ".csv":
			sidecars[name] = content
			sidecars[filepath.Base(name)] = content
		}
	}
	if len(jmxFiles) == 0 {
		return nil, "", nil, fmt.Errorf("no .jmx file found")
	}
	names := make([]string, 0, len(jmxFiles))
	for name := range jmxFiles {
		names = append(names, name)
	}
	sort.Strings(names)
	selected := names[0]
	return jmxFiles[selected], selected, sidecars, nil
}

func mergeJMeterSidecarFiles(first map[string][]byte, second map[string][]byte) map[string][]byte {
	if len(first) == 0 {
		if len(second) == 0 {
			return nil
		}
		out := make(map[string][]byte, len(second))
		for key, value := range second {
			out[key] = value
		}
		return out
	}
	out := make(map[string][]byte, len(first)+len(second))
	for key, value := range first {
		out[key] = value
	}
	for key, value := range second {
		out[key] = value
	}
	return out
}

func readJMeterSidecarFiles(r *http.Request, primaryField string, primaryFileName string) map[string][]byte {
	if r == nil || r.MultipartForm == nil || len(r.MultipartForm.File) == 0 {
		return nil
	}
	files := map[string][]byte{}
	for field, headers := range r.MultipartForm.File {
		if field == primaryField {
			continue
		}
		for _, header := range headers {
			if header == nil || strings.TrimSpace(header.Filename) == "" {
				continue
			}
			file, err := header.Open()
			if err != nil {
				continue
			}
			data, err := io.ReadAll(file)
			_ = file.Close()
			if err != nil || len(data) == 0 {
				continue
			}
			name := strings.TrimSpace(header.Filename)
			if name == primaryFileName {
				continue
			}
			files[name] = data
			files[filepath.Base(name)] = data
		}
	}
	if len(files) == 0 {
		return nil
	}
	return files
}

func buildJMeterProtocolCompatReport(result *jmeter.ConvertResult, unsupported []string) *protocolCompatReport {
	if result == nil {
		return nil
	}
	reviewDiagnostics := countJMeterReviewDiagnostics(result)
	reviewGated := len(unsupported) + reviewDiagnostics
	status := protocolPromotionStatus("executable", reviewGated, len(result.Warnings))
	return &protocolCompatReport{
		SourceFormat:         "JMeter JMX",
		CanonicalProtocol:    "jmeter",
		ImportMode:           "executable",
		MappedSteps:          result.Stats.ConvertedActions,
		ReviewGatedSteps:     reviewGated,
		UnsupportedFragments: uniqueSortedStrings(append(append([]string(nil), unsupported...), jmeterReviewDiagnosticFragments(result)...)),
		RuntimeParity:        protocolRuntimeParityFromScript(result.Script),
		DataSources:          result.Stats.CSVSources,
		CorrelationCoverage:  protocolCorrelationCoverage(countJMeterExtractors(result), result.Stats.Samplers),
		PromotionStatus:      status,
		NextActions:          protocolNextActions(status, "Validate converted JMeter thread groups and scenario draft before promotion."),
	}
}

func countJMeterReviewDiagnostics(result *jmeter.ConvertResult) int {
	if result == nil {
		return 0
	}
	count := 0
	for _, item := range result.Unsupported {
		switch strings.ToLower(strings.TrimSpace(string(item.Severity))) {
		case "warning", "blocker":
			count++
		}
	}
	return count
}

func jmeterReviewDiagnosticFragments(result *jmeter.ConvertResult) []string {
	if result == nil {
		return nil
	}
	fragments := make([]string, 0)
	for _, item := range result.Unsupported {
		switch strings.ToLower(strings.TrimSpace(string(item.Severity))) {
		case "warning", "blocker":
			fragments = append(fragments, item.String())
		}
	}
	return fragments
}

func countJMeterExtractors(result *jmeter.ConvertResult) int {
	if result == nil {
		return 0
	}
	total := 0
	var walk func(actions []dsl.Action)
	walk = func(actions []dsl.Action) {
		for _, action := range actions {
			total += len(action.Extractors)
			if action.Control != nil {
				walk(action.Control.Steps)
				walk(action.Control.Else)
			}
		}
	}
	for _, script := range result.Scripts {
		walk(script.Actions)
	}
	return total
}

func writeJMeterImportResponse(w http.ResponseWriter, status int, result *jmeter.ConvertResult) {
	unsupportedElements := uniqueSortedStrings(append(append([]string{}, result.UnsupportedElements...), result.Unsupported.Strings()...))
	protocolCompat := buildJMeterProtocolCompatReport(result, unsupportedElements)
	for _, script := range result.Scripts {
		persistProtocolCompatReport(script, "jmeter", protocolCompat)
	}
	writeJSON(w, status, newScriptImportResponse(scriptImportResponseOptions{
		Result:              result,
		Scripts:             result.Scripts,
		Script:              result.Script,
		ScenarioDraft:       result.ScenarioDraft,
		Warnings:            result.Warnings,
		UnsupportedElements: unsupportedElements,
		ReviewSections:      importDiagnosticReviewSections(result.Unsupported),
		Assumptions:         result.Assumptions,
		Stats:               result.Stats,
		Summary:             result.Summary,
		SourceReview:        result.SourceReview,
		ProtocolCompat:      protocolCompat,
		Parameterization:    buildMultiScriptParameterization(result.Scripts),
	}))
}

func (s *Server) handleSaveJMeterImport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Scripts       []dsl.Script          `json:"scripts"`
		ScenarioDraft *jmeter.ScenarioDraft `json:"scenario_draft,omitempty"`
	}
	if !decodeJSONBody(w, r, &req) {
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
		stored, err := s.runner.StoreScriptDurable(&script)
		if err != nil {
			for _, prior := range storedScripts {
				if _, cleanupErr := s.runner.DeleteScriptDurable(prior.ID); cleanupErr != nil {
					slog.Warn("failed to roll back imported JMeter script", "script_id", prior.ID, "error", cleanupErr)
				}
			}
			writeError(w, http.StatusInternalServerError, "failed to save imported script: "+err.Error())
			return
		}
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
