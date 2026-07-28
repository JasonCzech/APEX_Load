package api

// handlers_import_jmeter — split from handlers_scripts.go (same package, behavior-preserving).

import (
	"errors"
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
	"github.com/jasonczech/apexload/pkg/storage"
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
	// Every preview gets a public, opaque operation identity. Normal clients
	// echo it with the scripts on save, allowing a lost successful response to
	// converge without collapsing two intentional previews of identical input.
	stampJMeterDirectImportOperation(result, uuid.NewString())
	unsupportedElements := uniqueSortedStrings(append(append([]string{}, result.UnsupportedElements...), result.Unsupported.Strings()...))
	protocolCompat := buildJMeterProtocolCompatReport(result, unsupportedElements)
	for _, script := range result.Scripts {
		persistProtocolCompatReport(script, "jmeter", protocolCompat)
	}
	preflight := buildAndAttachMultiScriptImportPreflight(
		result.Script, result.Scripts, "jmeter", nil, result.Warnings,
	)
	s.writeScriptImportResponseWithSourceSecrets(w, r, status, scriptImportResponseOptions{
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
	})
}

// OpenAPI-Responses: 201
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
	previewOperationID, err := jmeterDirectImportOperationID(req.Scripts)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	operationID := ""
	if headerKey := strings.TrimSpace(r.Header.Get("Idempotency-Key")); headerKey != "" {
		if len(headerKey) > 512 {
			writeError(w, http.StatusBadRequest, "Idempotency-Key must be at most 512 bytes")
			return
		}
		for _, value := range []byte(headerKey) {
			if value < 0x21 || value > 0x7e {
				writeError(w, http.StatusBadRequest, "Idempotency-Key must contain visible ASCII characters only")
				return
			}
		}
		operationID = "header:" + headerKey
	} else if previewOperationID != "" {
		operationID = "preview:" + previewOperationID
	}
	if operationID != "" {
		_, actorSubject, _ := s.resolveAuditActor(r)
		operationID = strings.TrimSpace(actorSubject) + "\x00" + operationID
	}

	// Prepare and validate the whole import before the first durable write. This
	// dedicated, authorized import commit is the trust boundary for retaining the
	// source's executable credentials: the operator already supplied those bytes,
	// so replacing them with a secret reference here would make the destination
	// script require needless re-entry. Ordinary reads and this response are still
	// disclosure-safe projections.
	preparedScripts := make([]*dsl.Script, 0, len(req.Scripts))
	seenScriptNames := make(map[string]struct{}, len(req.Scripts))
	for i := range req.Scripts {
		script := req.Scripts[i]
		safeScript, err := preparePersistentImportedLibraryScript(&script, nil, "jmeter")
		if err != nil {
			writeError(w, http.StatusBadRequest, "failed to prepare imported script: "+err.Error())
			return
		}
		if issues := validateScript(safeScript); len(issues) > 0 {
			writeError(w, http.StatusBadRequest, strings.Join(issues, "; "))
			return
		}
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
	// The executable request/auth fields remain byte-faithful, but a JMeter
	// label can repeat one of those credentials. Project descriptive names only
	// after resolving the caller's original script_name references, then carry
	// the sanitized names into both durable scripts and the scenario.
	jmeter.SanitizeDisplayLabels(preparedScripts, req.ScenarioDraft)

	projectID := normalizeProjectID(preparedScripts[0].ProjectID)
	for _, script := range preparedScripts {
		if normalizeProjectID(script.ProjectID) != projectID {
			writeError(w, http.StatusBadRequest, "JMeter scripts must belong to one project")
			return
		}
	}
	requestDigest, err := directJMeterImportRequestDigest(s, preparedScripts, req.ScenarioDraft)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to fingerprint JMeter import: "+err.Error())
		return
	}
	attempt, receiptReplay, err := s.claimDirectImportAttempt(
		projectID,
		"jmeter",
		operationID,
		requestDigest,
	)
	if err != nil {
		switch {
		case errors.Is(err, storage.ErrDirectImportReceiptBusy):
			w.Header().Set("Retry-After", "2")
			writeError(w, http.StatusConflict, "this JMeter import save is already in progress")
		case errors.Is(err, storage.ErrDirectImportReceiptConflict):
			writeError(w, http.StatusConflict, "Idempotency-Key was already used for a different JMeter import")
		default:
			writeError(w, http.StatusInternalServerError, "failed to reserve JMeter import: "+err.Error())
		}
		return
	}
	assetIdentitySeed := ""
	if attempt != nil && attempt.receipt != nil {
		// The receipt ID is server-random and stable across retries. IDs never
		// derive from caller header text (which an operator might accidentally
		// populate with sensitive material).
		assetIdentitySeed = attempt.receipt.ID
	} else if previewOperationID != "" {
		// Lightweight stores have no receipt support; the preview operation ID
		// is still a server-issued random value.
		assetIdentitySeed = directImportOperationHash(projectID, "jmeter", operationID)
	}
	for index, script := range preparedScripts {
		if assetIdentitySeed != "" {
			script.ID = deterministicDirectImportAssetID(assetIdentitySeed, "script", index)
		} else {
			// Legacy clients that neither echo a preview operation ID nor supply
			// Idempotency-Key retain create-another semantics.
			script.ID = uuid.NewString()
		}
		script.RefKey = ""
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
			if assetIdentitySeed != "" {
				scenarioID = deterministicDirectImportAssetID(assetIdentitySeed, "scenario", 0)
			} else {
				scenarioID = uuid.NewString()
			}
			request := CreateScenarioRequest{
				Name:         firstNonEmpty(req.ScenarioDraft.Name, "Imported JMeter Scenario"),
				Duration:     firstNonEmpty(req.ScenarioDraft.Duration, "5m"),
				ScenarioMode: firstNonEmpty(req.ScenarioDraft.ScenarioMode, "vuser_driven"),
				Groups:       groups,
			}
			scenarioRequest = &request
		}
	}

	if receiptReplay {
		storedScripts, storedScenario, loadErr := s.loadDirectImportReceiptAssets(attempt)
		if loadErr != nil {
			writeError(w, http.StatusConflict, "completed JMeter import receipt is inconsistent: "+loadErr.Error())
			return
		}
		writeJSON(
			w,
			http.StatusCreated,
			savedJMeterImportResponse(storedScripts, storedScenario),
		)
		return
	}

	// A stale pending receipt can represent a crash after all assets committed
	// but before the receipt was finalized. Heal that state without entering a
	// new rollback journal, but only when every deterministic asset is exact.
	if attempt != nil && !attempt.created {
		// The receipt lease is longer than the write-ahead claim lease. Recover
		// any expired partial transaction before deciding whether its assets are
		// a completed operation that can be adopted.
		if recoverErr := s.recoverExpiredLibraryImportClaims(); recoverErr != nil {
			writeError(w, http.StatusInternalServerError, "failed to recover prior JMeter import transaction: "+recoverErr.Error())
			return
		}
		if renewErr := attempt.renew(); renewErr != nil {
			writeError(w, http.StatusInternalServerError, "failed to renew JMeter import receipt after recovery: "+renewErr.Error())
			return
		}
		storedScripts, storedScenario, loadErr := s.loadDirectImportAssetsByIDs(
			projectID,
			deterministicScriptIDs(preparedScripts),
			scenarioID,
		)
		if loadErr == nil && directImportPreparedAssetsMatch(
			s, storedScripts, preparedScripts, storedScenario, scenarioRequest,
		) {
			if completeErr := attempt.complete(storedScripts, storedScenario); completeErr != nil {
				writeError(w, http.StatusInternalServerError, "failed to reconcile JMeter import receipt: "+completeErr.Error())
				return
			}
			writeJSON(
				w,
				http.StatusCreated,
				savedJMeterImportResponse(storedScripts, storedScenario),
			)
			return
		}
	}

	transaction, err := newCreateImportTransaction(s, "jmeter", preparedScripts, scenarioID, scenarioRequest)
	if err != nil {
		if attempt != nil {
			if cleanupErr := attempt.rollback(nil); cleanupErr != nil {
				slog.Warn("failed to release JMeter import receipt", "error", cleanupErr)
			}
		}
		writeError(w, http.StatusInternalServerError, "failed to prepare imported assets: "+err.Error())
		return
	}
	finished := false
	assetsCommitted := false
	defer func() {
		if !finished && !assetsCommitted {
			if cleanupErr := attempt.rollback(transaction); cleanupErr != nil {
				slog.Warn("failed to fully compensate JMeter import", "error", cleanupErr)
			}
		}
	}()

	storedScripts := make([]*StoredScript, 0, len(preparedScripts))
	for _, safeScript := range preparedScripts {
		if err := attempt.renew(); err != nil {
			writeError(w, http.StatusConflict, "JMeter import receipt ownership was lost; retry the save")
			return
		}
		stored, err := transaction.createImportedScript(safeScript, "jmeter")
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to save imported script: "+err.Error())
			return
		}
		storedScripts = append(storedScripts, stored)
	}

	var storedScenario *StoredScenario
	if scenarioRequest != nil {
		if err := attempt.renew(); err != nil {
			writeError(w, http.StatusConflict, "JMeter import receipt ownership was lost; retry the save")
			return
		}
		storedScenario, err = transaction.createScenario(scenarioID, *scenarioRequest)
		if err != nil {
			writeError(w, http.StatusBadRequest, "failed to save scenario draft: "+err.Error())
			return
		}
	}
	if err := attempt.renew(); err != nil {
		writeError(w, http.StatusConflict, "JMeter import receipt ownership was lost; retry the save")
		return
	}
	if err := transaction.commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit imported assets: "+err.Error())
		return
	}
	assetsCommitted = true
	if attempt != nil {
		if err := attempt.complete(storedScripts, storedScenario); err != nil {
			// Keep the pending receipt and deterministic assets. A stale retry
			// will verify and finalize them; compensating after the transaction's
			// durable commit would reintroduce a lost-response race.
			writeError(w, http.StatusInternalServerError, "failed to finalize JMeter import receipt: "+err.Error())
			return
		}
	}
	finished = true
	writeJSON(
		w,
		http.StatusCreated,
		savedJMeterImportResponse(storedScripts, storedScenario),
	)
}

func savedJMeterImportResponse(
	storedScripts []*StoredScript,
	storedScenario *StoredScenario,
) map[string]any {
	return map[string]any{
		"scripts":  maskStoredScripts(storedScripts),
		"scenario": storedScenario,
		"count":    len(storedScripts),
	}
}
