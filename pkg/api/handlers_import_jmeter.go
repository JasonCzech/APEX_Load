package api

// handlers_import_jmeter — split from handlers_scripts.go (same package, behavior-preserving).

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/convert/jmeter"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/internal/archiveingest"
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

	s.writeJMeterImportResponse(w, r, http.StatusOK, result)
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

	s.writeJMeterImportResponse(w, r, http.StatusOK, result)
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
	const (
		maxJMeterZipEntries    = 1_000
		maxJMeterZipEntryBytes = 32 << 20
		maxJMeterZipTotalBytes = 128 << 20
	)
	// archiveingest owns the shared entry-path policy (traversal, absolute and
	// drive-qualified names, control characters, depth, case-fold duplicates)
	// plus the directory-count and compression-ratio guards. Only the JMeter
	// size budget is local.
	files, err := archiveingest.ReadZIP(data, archiveingest.Options{
		Limits: archiveingest.Limits{
			MaxEntries:    maxJMeterZipEntries,
			MaxEntryBytes: maxJMeterZipEntryBytes,
			MaxTotalBytes: maxJMeterZipTotalBytes,
		},
	})
	if err != nil {
		return nil, "", nil, fmt.Errorf("reading JMeter archive: %w", err)
	}
	jmxFiles := map[string][]byte{}
	sidecars := map[string][]byte{}
	for _, file := range files {
		switch strings.ToLower(path.Ext(file.Name)) {
		case ".jmx":
			jmxFiles[file.Name] = file.Data
		case ".csv":
			sidecars[file.Name] = file.Data
			sidecars[path.Base(file.Name)] = file.Data
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

func (s *Server) writeJMeterImportResponse(w http.ResponseWriter, r *http.Request, status int, result *jmeter.ConvertResult) {
	unsupportedElements := uniqueSortedStrings(append(append([]string{}, result.UnsupportedElements...), result.Unsupported.Strings()...))
	protocolCompat := buildJMeterProtocolCompatReport(result, unsupportedElements)
	for _, script := range result.Scripts {
		persistProtocolCompatReport(script, "jmeter", protocolCompat)
	}
	preflight := buildAndAttachMultiScriptImportPreflight(
		result.Script, result.Scripts, "jmeter", nil, result.Warnings,
	)
	writeJSON(w, status, s.newScriptImportResponseWithSourceSecrets(r, scriptImportResponseOptions{
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
		Preflight:           preflight,
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

	// Prepare and validate the whole import before the first durable write. This
	// avoids leaving an early script behind when a later script has invalid secret
	// bindings or DSL. A deferred compensation guard then covers every subsequent
	// failure, including scenario validation/persistence.
	preparedScripts := make([]*dsl.Script, 0, len(req.Scripts))
	seenScriptNames := make(map[string]struct{}, len(req.Scripts))
	for i := range req.Scripts {
		script := req.Scripts[i]
		safeScript, _, err := applySuggestedSecretBindings(&script, i)
		if err != nil {
			writeError(w, http.StatusBadRequest, "failed to prepare safe secret references: "+err.Error())
			return
		}
		if issues := validateScript(safeScript); len(issues) > 0 {
			writeError(w, http.StatusBadRequest, strings.Join(issues, "; "))
			return
		}
		// Reserve a fresh ID before the durable write-ahead journal is prepared.
		// Create-only persistence and conditional compensation then share the same
		// ownership fence across crashes and concurrent edits.
		safeScript.ID = uuid.NewString()
		safeScript.RefKey = ""
		if strings.TrimSpace(safeScript.Version) == "" {
			safeScript.Version = "1"
		}
		nameKey := strings.TrimSpace(safeScript.Name)
		if _, duplicate := seenScriptNames[nameKey]; duplicate {
			writeError(w, http.StatusBadRequest, "JMeter import contains duplicate script name: "+nameKey)
			return
		}
		seenScriptNames[nameKey] = struct{}{}
		preparedScripts = append(preparedScripts, safeScript)
	}
	if req.ScenarioDraft != nil {
		for index, group := range req.ScenarioDraft.Groups {
			scriptName := strings.TrimSpace(group.ScriptName)
			if scriptName == "" {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("scenario group %d is missing script_name", index+1))
				return
			}
			if _, ok := seenScriptNames[scriptName]; !ok {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("scenario group %d references unknown imported script %q", index+1, scriptName))
				return
			}
		}
	}

	scriptIDsByName := make(map[string]string, len(preparedScripts))
	scriptsByName := make(map[string]*dsl.Script, len(preparedScripts))
	for _, safeScript := range preparedScripts {
		name := strings.TrimSpace(safeScript.Name)
		scriptIDsByName[name] = safeScript.ID
		scriptsByName[name] = safeScript
	}

	var scenarioID string
	var scenarioRequest *CreateScenarioRequest
	if req.ScenarioDraft != nil && len(req.ScenarioDraft.Groups) > 0 {
		groups := make([]CreateGroupEntry, 0, len(req.ScenarioDraft.Groups))
		for index, group := range req.ScenarioDraft.Groups {
			scriptName := strings.TrimSpace(group.ScriptName)
			scriptID := scriptIDsByName[scriptName]
			groupName := group.Name
			if groupName == "" {
				groupName = scriptsByName[scriptName].Name
				if len(req.ScenarioDraft.Groups) > 1 {
					groupName = fmt.Sprintf("%s (#%d)", groupName, index+1)
				}
			}
			vusers := group.VUsers
			if vusers == 0 {
				vusers = 10
			}
			initialization := strings.ToLower(strings.TrimSpace(group.Initialization))
			if initialization == "" {
				initialization = "at_runtime"
			}
			groups = append(groups, CreateGroupEntry{
				Name:         groupName,
				ScriptID:     scriptID,
				Role:         GroupRoleLoad,
				VUsers:       vusers,
				RampUp:       group.RampUp,
				InitStrategy: initialization,
			})
		}
		if len(groups) > 0 {
			scenarioID = uuid.NewString()
			request := CreateScenarioRequest{
				Name:         firstNonEmpty(req.ScenarioDraft.Name, "Imported JMeter Scenario"),
				Duration:     firstNonEmpty(req.ScenarioDraft.Duration, "5m"),
				ScenarioMode: firstNonEmpty(req.ScenarioDraft.ScenarioMode, "vuser_driven"),
				Groups:       groups,
			}
			scenarioRequest = &request
		}
	}

	transaction, err := newCreateImportTransaction(s, "jmeter", preparedScripts, scenarioID, scenarioRequest)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to prepare imported assets: "+err.Error())
		return
	}
	committed := false
	defer func() {
		if !committed {
			if cleanupErr := transaction.rollback(); cleanupErr != nil {
				slog.Warn("failed to fully compensate JMeter import", "error", cleanupErr)
			}
		}
	}()

	storedScripts := make([]*StoredScript, 0, len(preparedScripts))
	for _, safeScript := range preparedScripts {
		stored, err := transaction.createScript(safeScript)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to save imported script: "+err.Error())
			return
		}
		storedScripts = append(storedScripts, stored)
	}

	var storedScenario *StoredScenario
	if scenarioRequest != nil {
		storedScenario, err = transaction.createScenario(scenarioID, *scenarioRequest)
		if err != nil {
			writeError(w, http.StatusBadRequest, "failed to save scenario draft: "+err.Error())
			return
		}
	}
	if err := transaction.commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit imported assets: "+err.Error())
		return
	}
	committed = true

	writeJSON(w, http.StatusCreated, map[string]any{
		"scripts":  storedScripts,
		"scenario": storedScenario,
		"count":    len(storedScripts),
	})
}
