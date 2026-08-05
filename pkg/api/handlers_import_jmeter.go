package api

// handlers_import_jmeter — split from handlers_scripts.go (same package, behavior-preserving).

import (
	"bytes"
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

	result, err := convertJMeterImportRequest(importReq)
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
	result, err := convertJMeterImportRequest(importReq)
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
		sidecarFiles, err = readJMeterSidecarFiles(r, "file", fileName)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid JMeter sidecar uploads: "+err.Error())
			return nil, false
		}
	}

	if len(data) == 0 {
		writeError(w, http.StatusBadRequest, "empty file uploaded")
		return nil, false
	}
	if isJMeterZipUpload(fileName, contentType) {
		documents, zipSidecars, err := readJMeterZipImportAll(data)
		if err != nil {
			writeError(w, http.StatusBadRequest, "failed to read JMeter ZIP: "+err.Error())
			return nil, false
		}
		data = documents[0].Data
		fileName = documents[0].FileName
		sidecarFiles, err = mergeJMeterSidecarFiles(sidecarFiles, zipSidecars)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid JMeter multipart/ZIP sidecars: "+err.Error())
			return nil, false
		}
		return &jmeterImportRequest{
			Data: data, Documents: documents, ProjectID: authorizedImportProjectID(r),
			Opts: jmeter.ConvertOptions{
				ScriptName: r.FormValue("name"), BaseURL: r.FormValue("base_url"),
				IncludeAssertions: r.FormValue("include_assertions") == "true",
				ScenarioDraft:     r.FormValue("scenario_draft") == "true", FileName: fileName, SidecarFiles: sidecarFiles,
			},
		}, true
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
	documents, sidecars, err := readJMeterZipImportAll(data)
	if err != nil {
		return nil, "", nil, err
	}
	return documents[0].Data, documents[0].FileName, sidecars, nil
}

func readJMeterZipImportAll(data []byte) ([]jmeterImportDocument, map[string][]byte, error) {
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
		return nil, nil, fmt.Errorf("reading JMeter archive: %w", err)
	}
	jmxFiles := map[string][]byte{}
	sidecars := map[string][]byte{}
	for _, file := range files {
		switch strings.ToLower(path.Ext(file.Name)) {
		case ".jmx":
			jmxFiles[file.Name] = file.Data
		case ".csv":
			sidecars[file.Name] = file.Data
		}
	}
	if len(jmxFiles) == 0 {
		return nil, nil, fmt.Errorf("no .jmx file found")
	}
	names := make([]string, 0, len(jmxFiles))
	for name := range jmxFiles {
		names = append(names, name)
	}
	sort.Strings(names)
	documents := make([]jmeterImportDocument, 0, len(names))
	for _, name := range names {
		documents = append(documents, jmeterImportDocument{Data: jmxFiles[name], FileName: name})
	}
	return documents, sidecars, nil
}

func convertJMeterImportRequest(request *jmeterImportRequest) (*jmeter.ConvertResult, error) {
	if request == nil {
		return nil, fmt.Errorf("JMeter import request is required")
	}
	documents := request.Documents
	if len(documents) == 0 {
		documents = []jmeterImportDocument{{Data: request.Data, FileName: request.Opts.FileName}}
	}
	combined := &jmeter.ConvertResult{}
	for _, document := range documents {
		opts := request.Opts
		opts.FileName = document.FileName
		if len(request.Documents) > 0 {
			opts.SidecarFiles = jmeterSidecarsForDocument(request.Opts.SidecarFiles, document.FileName)
		}
		if len(documents) > 1 && strings.TrimSpace(opts.ScriptName) != "" {
			opts.ScriptName = fmt.Sprintf("%s — %s", strings.TrimSpace(opts.ScriptName), strings.TrimSuffix(path.Base(document.FileName), path.Ext(document.FileName)))
		}
		converted, err := jmeter.ConvertJMX(document.Data, opts)
		if err != nil {
			return nil, fmt.Errorf("convert JMeter ZIP entry %q: %w", document.FileName, err)
		}
		attachSourceReviewToScripts(converted.Scripts, "jmeter", document.FileName, converted.SourceReview)
		for _, script := range converted.Scripts {
			if script.Metadata == nil {
				script.Metadata = &dsl.ScriptMetadata{}
			}
			if script.Metadata.Import == nil {
				script.Metadata.Import = &dsl.ImportMetadata{}
			}
			if script.Metadata.Import.Preserved == nil {
				script.Metadata.Import.Preserved = make(map[string]any)
			}
			script.Metadata.Import.Preserved["source_zip_entry"] = document.FileName
		}
		combined.Scripts = append(combined.Scripts, converted.Scripts...)
		if combined.Script == nil {
			combined.Script = converted.Script
			if len(documents) == 1 {
				combined.SourceReview = converted.SourceReview
			}
		}
		combined.Warnings = append(combined.Warnings, converted.Warnings...)
		combined.UnsupportedElements = append(combined.UnsupportedElements, converted.UnsupportedElements...)
		combined.Unsupported = append(combined.Unsupported, converted.Unsupported...)
		combined.Assumptions = append(combined.Assumptions, converted.Assumptions...)
		combined.Findings = append(combined.Findings, converted.Findings...)
		combined.SecretRequirements = append(combined.SecretRequirements, converted.SecretRequirements...)
		combined.Stats.ThreadGroups += converted.Stats.ThreadGroups
		combined.Stats.Samplers += converted.Stats.Samplers
		combined.Stats.TotalSamplers += converted.Stats.TotalSamplers
		combined.Stats.SkippedSamplers += converted.Stats.SkippedSamplers
		combined.Stats.ConvertedActions += converted.Stats.ConvertedActions
		combined.Stats.Headers += converted.Stats.Headers
		combined.Stats.Variables += converted.Stats.Variables
		combined.Stats.CSVSources += converted.Stats.CSVSources
		combined.Stats.Assertions += converted.Stats.Assertions
		combined.Stats.Timers += converted.Stats.Timers
		combined.Stats.Unsupported += converted.Stats.Unsupported
		combined.Stats.Mapped += converted.Stats.Mapped
		combined.Stats.ReviewRequired += converted.Stats.ReviewRequired
		combined.Stats.AdapterRequired += converted.Stats.AdapterRequired
		combined.Summary.ThreadGroupCount += converted.Summary.ThreadGroupCount
		combined.Summary.SamplerCount += converted.Summary.SamplerCount
		combined.Summary.VariablesDiscovered = append(combined.Summary.VariablesDiscovered, converted.Summary.VariablesDiscovered...)
		combined.Summary.CSVSourcesDiscovered = append(combined.Summary.CSVSourcesDiscovered, converted.Summary.CSVSourcesDiscovered...)
		if converted.ScenarioDraft != nil {
			if combined.ScenarioDraft == nil {
				copy := *converted.ScenarioDraft
				copy.Groups = nil
				combined.ScenarioDraft = &copy
			}
			combined.ScenarioDraft.Groups = append(combined.ScenarioDraft.Groups, converted.ScenarioDraft.Groups...)
		}
	}
	if len(documents) == 1 {
		combined.Summary.FileName = documents[0].FileName
	} else {
		combined.Summary.FileName = fmt.Sprintf("%d JMX files", len(documents))
	}
	combined.Summary.VariablesDiscovered = uniqueSortedStrings(combined.Summary.VariablesDiscovered)
	combined.Summary.CSVSourcesDiscovered = uniqueSortedStrings(combined.Summary.CSVSourcesDiscovered)
	return combined, nil
}

// jmeterSidecarsForDocument presents archive sidecars relative to one JMX
// entry. A basename-only global map is ambiguous when independent plans contain
// same-named CSV files, so each document gets its own deterministic namespace.
func jmeterSidecarsForDocument(files map[string][]byte, documentName string) map[string][]byte {
	if len(files) == 0 {
		return nil
	}
	documentDir := path.Dir(strings.TrimLeft(strings.ReplaceAll(documentName, `\`, `/`), "/"))
	if documentDir == "." {
		documentDir = ""
	}
	out := make(map[string][]byte, len(files)*2)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	// Preserve full archive paths first for plans that authored them explicitly.
	for _, name := range names {
		data := files[name]
		normalized := path.Clean(strings.TrimLeft(strings.ReplaceAll(strings.TrimSpace(name), `\`, `/`), "/"))
		if normalized == "." || normalized == "" {
			continue
		}
		out[normalized] = data
	}
	// Relative aliases are authoritative: a plain data.csv in a JMX means the
	// sibling entry, not an unrelated root or another plan's same-named file.
	for _, name := range names {
		data := files[name]
		normalized := path.Clean(strings.TrimLeft(strings.ReplaceAll(strings.TrimSpace(name), `\`, `/`), "/"))
		if normalized == "." || normalized == "" {
			continue
		}
		relative, err := filepath.Rel(filepath.FromSlash(documentDir), filepath.FromSlash(normalized))
		if err == nil {
			relative = filepath.ToSlash(relative)
			if relative != "." && relative != "" {
				out[relative] = data
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// mergeJMeterSidecarFiles combines optional external multipart sidecars with
// sidecars embedded in a ZIP. Same-basename files wholly inside the ZIP remain
// valid because jmeterSidecarsForDocument scopes them to their neighboring JMX.
// A cross-source exact-name or basename collision is different: an external
// upload could silently replace archive-owned data, so differing bytes fail
// closed while identical exact names deduplicate deterministically.
func mergeJMeterSidecarFiles(first map[string][]byte, second map[string][]byte) (map[string][]byte, error) {
	if len(first) == 0 {
		if len(second) == 0 {
			return nil, nil
		}
		out := make(map[string][]byte, len(second))
		for key, value := range second {
			out[key] = value
		}
		return out, nil
	}
	if len(second) == 0 {
		out := make(map[string][]byte, len(first))
		for key, value := range first {
			out[key] = value
		}
		return out, nil
	}
	out := make(map[string][]byte, len(first)+len(second))
	exactPayloads := make(map[string][]byte, len(first)+len(second))
	multipartBasenames := make(map[string][]byte, len(first))
	firstNames := make([]string, 0, len(first))
	for name := range first {
		firstNames = append(firstNames, name)
	}
	sort.Strings(firstNames)
	for _, originalName := range firstNames {
		data := first[originalName]
		name := normalizeJMeterMultipartSidecarName(originalName)
		if name == "" {
			continue
		}
		exactKey := strings.ToLower(name)
		if existing, duplicate := exactPayloads[exactKey]; duplicate {
			if !bytes.Equal(existing, data) {
				return nil, fmt.Errorf("conflicting multipart sidecars use the same filename %q with different content", name)
			}
			continue
		}
		baseName := path.Base(name)
		baseKey := strings.ToLower(baseName)
		if existing, duplicate := multipartBasenames[baseKey]; duplicate && !bytes.Equal(existing, data) {
			return nil, fmt.Errorf("conflicting multipart sidecars use the same basename %q with different content", baseName)
		}
		exactPayloads[exactKey] = data
		if _, exists := multipartBasenames[baseKey]; !exists {
			multipartBasenames[baseKey] = data
		}
		out[name] = data
	}
	secondNames := make([]string, 0, len(second))
	for name := range second {
		secondNames = append(secondNames, name)
	}
	sort.Strings(secondNames)
	for _, originalName := range secondNames {
		data := second[originalName]
		name := normalizeJMeterMultipartSidecarName(originalName)
		if name == "" {
			continue
		}
		exactKey := strings.ToLower(name)
		if existing, duplicate := exactPayloads[exactKey]; duplicate {
			if !bytes.Equal(existing, data) {
				return nil, fmt.Errorf("multipart and ZIP sidecars use the same filename %q with different content", name)
			}
			// Byte-identical exact names are one logical sidecar. Keep the
			// multipart entry and omit the redundant archive alias.
			continue
		}
		baseName := path.Base(name)
		if existing, duplicate := multipartBasenames[strings.ToLower(baseName)]; duplicate && !bytes.Equal(existing, data) {
			return nil, fmt.Errorf("multipart and ZIP sidecars use the same basename %q with different content", baseName)
		}
		exactPayloads[exactKey] = data
		out[name] = data
	}
	return out, nil
}

// readJMeterSidecarFiles preserves path-qualified upload names so the converter
// can resolve an authored relative path without a lossy basename alias. Two
// uploads with the same normalized name or basename are accepted only when
// their bytes are identical. Identical exact-name duplicates collapse to one
// logical sidecar. Distinct path-qualified names remain available to separate
// JMX plans; the converter permits their basename fallback only because their
// bytes are identical. Differing payloads are ambiguous and fail closed before
// conversion.
func readJMeterSidecarFiles(r *http.Request, primaryField string, primaryFileName string) (map[string][]byte, error) {
	if r == nil || r.MultipartForm == nil || len(r.MultipartForm.File) == 0 {
		return nil, nil
	}
	files := map[string][]byte{}
	exactPayloads := map[string][]byte{}
	basenamePayloads := map[string][]byte{}
	primaryName := normalizeJMeterMultipartSidecarName(primaryFileName)
	fields := make([]string, 0, len(r.MultipartForm.File))
	for field := range r.MultipartForm.File {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	for _, field := range fields {
		headers := r.MultipartForm.File[field]
		if field == primaryField {
			continue
		}
		for _, header := range headers {
			if header == nil {
				continue
			}
			name := normalizeJMeterMultipartSidecarName(header.Filename)
			if name == "" || (primaryName != "" && strings.EqualFold(name, primaryName)) {
				continue
			}
			file, err := header.Open()
			if err != nil {
				return nil, fmt.Errorf("open sidecar %q: %w", name, err)
			}
			data, err := io.ReadAll(file)
			_ = file.Close()
			if err != nil {
				return nil, fmt.Errorf("read sidecar %q: %w", name, err)
			}
			exactKey := strings.ToLower(name)
			if existing, duplicate := exactPayloads[exactKey]; duplicate {
				if !bytes.Equal(existing, data) {
					return nil, fmt.Errorf("conflicting sidecars use the same filename %q with different content", name)
				}
				// Identical exact-name uploads are one logical sidecar. Keep the
				// first normalized spelling selected by sorted form-field order.
				continue
			}
			baseName := path.Base(name)
			baseKey := strings.ToLower(baseName)
			if existing, duplicate := basenamePayloads[baseKey]; duplicate && !bytes.Equal(existing, data) {
				return nil, fmt.Errorf("conflicting sidecars use the same basename %q with different content", baseName)
			}
			exactPayloads[exactKey] = data
			if _, exists := basenamePayloads[baseKey]; !exists {
				basenamePayloads[baseKey] = data
			}
			if len(data) == 0 {
				// Retain the collision identity above, but preserve the historical
				// behavior of omitting an empty sidecar from conversion.
				continue
			}
			files[name] = data
		}
	}
	if len(files) == 0 {
		return nil, nil
	}
	return files, nil
}

func normalizeJMeterMultipartSidecarName(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, `\`, "/"))
	value = strings.TrimLeft(value, "/")
	if value == "" {
		return ""
	}
	cleaned := path.Clean(value)
	if cleaned == "." {
		return ""
	}
	return cleaned
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
		if fenceErr := s.fenceDirectImportAssetAdoption(
			attempt,
			projectID,
			deterministicScriptIDs(preparedScripts),
			scenarioID,
		); fenceErr != nil {
			if errors.Is(fenceErr, errDirectImportAssetsClaimed) {
				w.Header().Set("Retry-After", "2")
				writeError(w, http.StatusConflict, "the prior JMeter import transaction is still finalizing; retry the save")
			} else {
				writeError(w, http.StatusInternalServerError, "failed to inspect prior JMeter import transaction: "+fenceErr.Error())
			}
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
