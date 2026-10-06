package api

// handlers_import_jmeter — split from handlers_scripts.go (same package, behavior-preserving).

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/convert/jmeter"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/internal/archiveingest"
	"github.com/jasonczech/apexload/pkg/storage"
)

const jmeterImportSaveMaxScripts = 1000

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
	// Scope the drafts here; the shared augmentation pass in the writer reads the
	// scope back off them and owns source review, detection, and preflight.
	for index := range result.Scripts {
		result.Scripts[index].ProjectID = normalizeProjectID(importReq.ProjectID)
	}
	if result.Script != nil {
		result.Script.ProjectID = normalizeProjectID(importReq.ProjectID)
	}

	s.writeJMeterImportResponse(w, r, http.StatusOK, result, importReq)
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
	// Scope the drafts here; the shared augmentation pass in the writer reads the
	// scope back off them and owns source review, detection, and preflight.
	for index := range result.Scripts {
		result.Scripts[index].ProjectID = normalizeProjectID(importReq.ProjectID)
	}
	if result.Script != nil {
		result.Script.ProjectID = normalizeProjectID(importReq.ProjectID)
	}

	s.writeJMeterImportResponse(w, r, http.StatusOK, result, importReq)
}

func (s *Server) handleJMeterSupportMatrix(w http.ResponseWriter, r *http.Request) {
	s.writeImporterSupportMatrix(w, r, "jmeter")
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

	var bindings map[string]string
	if raw := r.FormValue("variable_bindings"); raw != "" {
		if len(raw) > 16384 || json.Unmarshal([]byte(raw), &bindings) != nil || len(bindings) > 128 {
			writeError(w, http.StatusBadRequest, "invalid variable_bindings object")
			return nil, false
		}
	}
	if len(data) == 0 {
		writeError(w, http.StatusBadRequest, "empty file uploaded")
		return nil, false
	}
	if isJMeterZipUpload(fileName, contentType, data) {
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
				TimeMode: r.FormValue("time_mode"), Timezone: r.FormValue("timezone"), VariableBindings: bindings,
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
			TimeMode: r.FormValue("time_mode"), Timezone: r.FormValue("timezone"), VariableBindings: bindings,
			ScriptName:        r.FormValue("name"),
			BaseURL:           r.FormValue("base_url"),
			IncludeAssertions: r.FormValue("include_assertions") == "true",
			ScenarioDraft:     r.FormValue("scenario_draft") == "true",
			FileName:          fileName,
			SidecarFiles:      sidecarFiles,
		},
	}, true
}

// isJMeterZipUpload reports whether the upload is a ZIP of JMX plans. A
// LoadRunner Enterprise JMeter script arrives as a .usz (a ZIP holding the .jmx
// and VuGen project files); a JMX document is XML and never starts with the
// ZIP local-file signature.
func isJMeterZipUpload(fileName string, contentType string, data []byte) bool {
	ext := filepath.Ext(fileName)
	return strings.EqualFold(ext, ".zip") || strings.EqualFold(ext, ".usz") ||
		strings.HasPrefix(contentType, "application/zip") ||
		strings.HasPrefix(contentType, "application/x-zip-compressed") ||
		bytes.HasPrefix(data, []byte("PK\x03\x04"))
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
	credentialRegistry := credentialpolicy.NewNameRegistry()
	for _, document := range documents {
		opts := request.Opts
		opts.FileName = document.FileName
		opts.CredentialRegistry = credentialRegistry
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
		if len(documents) > 1 && opts.ScenarioDraft {
			for _, script := range converted.Scripts {
				if script.Config.JMeter != nil {
					return nil, fmt.Errorf("messaging scenario import requires one JMX document; combining independent source schedules is not supported")
				}
			}
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
		combined.ReviewOutcomes = append(combined.ReviewOutcomes, converted.ReviewOutcomes...)
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
		combined.Stats.EnabledWorkloadSamplers += converted.Stats.EnabledWorkloadSamplers
		combined.Stats.DisabledWorkloadSamplers += converted.Stats.DisabledWorkloadSamplers
		combined.Stats.ConvertedWorkloadSamplers += converted.Stats.ConvertedWorkloadSamplers
		combined.Stats.BlockedWorkloadSamplers += converted.Stats.BlockedWorkloadSamplers
		combined.Stats.ConfigurationContributors += converted.Stats.ConfigurationContributors
		combined.Stats.IgnoredReportingElements += converted.Stats.IgnoredReportingElements
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
	// ConvertJMX guarantees uniqueness inside one plan, but an archive may hold
	// independent plans whose Thread Groups have the same authored name. Apply
	// the disclosure-safe allocator once more across the assembled batch and
	// rewrite scenario references before parameterization or save validation sees
	// the result.
	jmeter.SanitizeDisplayLabels(combined.Scripts, combined.ScenarioDraft)
	if len(combined.Scripts) > 0 {
		combined.Script = combined.Scripts[0]
	}
	if len(documents) == 1 {
		combined.Summary.FileName = documents[0].FileName
	} else {
		combined.Summary.FileName = fmt.Sprintf("%d JMX files", len(documents))
	}
	combined.Summary.VariablesDiscovered = uniqueSortedStrings(combined.Summary.VariablesDiscovered)
	combined.Summary.CSVSourcesDiscovered = uniqueSortedStrings(combined.Summary.CSVSourcesDiscovered)
	combined.SetReviewEvidence(combined.ReviewOutcomes)
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

func buildJMeterProtocolCompatReport(result *jmeter.ConvertResult) *protocolCompatReport {
	if result == nil {
		return nil
	}
	reviewGated := result.Stats.ReviewRequired + result.Stats.AdapterRequired
	if result.ReviewSummary != nil {
		reviewGated = result.ReviewSummary.BlockingBehaviors + result.ReviewSummary.ReviewBehaviors
	}
	warningCount := len(result.Warnings)
	if result.ReviewSummary != nil {
		warningCount = result.ReviewSummary.Warnings
	}
	status := protocolPromotionStatus("executable", reviewGated, warningCount)
	return &protocolCompatReport{
		SourceFormat:         "JMeter JMX",
		CanonicalProtocol:    "jmeter",
		ImportMode:           "executable",
		MappedSteps:          result.Stats.Mapped,
		ReviewGatedSteps:     reviewGated,
		UnsupportedFragments: append([]string{}, result.UnsupportedElements...),
		RuntimeParity:        protocolRuntimeParityFromScript(result.Script),
		DataSources:          result.Stats.CSVSources,
		CorrelationCoverage:  protocolCorrelationCoverage(countJMeterExtractors(result), result.Stats.Samplers),
		PromotionStatus:      status,
		NextActions:          protocolNextActions(status, "Validate converted JMeter thread groups and scenario draft before promotion."),
	}
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
				for _, branch := range action.Control.ActionBranches() {
					walk(branch.Actions)
				}
			}
		}
	}
	for _, script := range result.Scripts {
		walk(script.Actions)
	}
	return total
}

func (s *Server) writeJMeterImportResponse(w http.ResponseWriter, r *http.Request, status int, result *jmeter.ConvertResult, requests ...*jmeterImportRequest) {
	// Every preview gets a public, opaque operation identity. Normal clients
	// echo it with the scripts on save, allowing a lost successful response to
	// converge without collapsing two intentional previews of identical input.
	stampJMeterDirectImportOperation(result, uuid.NewString())
	unsupportedElements := append([]string{}, result.UnsupportedElements...)
	protocolCompat := buildJMeterProtocolCompatReport(result)
	for _, script := range result.Scripts {
		persistProtocolCompatReport(script, "jmeter", protocolCompat)
	}
	draft := importDraft{
		Source:           "jmeter",
		SourceScriptName: result.Summary.FileName,
		ProjectID:        jmeterDraftProjectID(result),
		Script:           result.Script,
		Scripts:          result.Scripts,
		SourceReview:     result.SourceReview,
		Unsupported:      result.Diagnostics,
		Warnings:         result.Warnings,
		Accounting:       result.Accounting(),
	}
	augmented := draft.augment(r)
	result.Warnings = augmented.Warnings
	applyJMeterReviewPreflight(augmented.Preflight, result)
	if len(result.Scripts) == 1 {
		attachImportReplayPreflight(result.Script, "jmeter", augmented.Preflight)
	}
	batch, batchErr := s.newJMeterBatch(r, result, requests)
	if batchErr != nil {
		writeError(w, 503, "Cannot secure JMeter batch")
		return
	}
	if batch != nil {
		r = withImportBatchExpiry(r, batch.ExpiresAt)
	}
	response := s.newScriptImportResponseWithSourceSecrets(r, scriptImportResponseOptions{
		Result:                 result,
		Scripts:                result.Scripts,
		Script:                 result.Script,
		ScenarioDraft:          result.ScenarioDraft,
		Warnings:               result.Warnings,
		UnsupportedElements:    unsupportedElements,
		Diagnostics:            result.Diagnostics,
		ReviewSummary:          result.ReviewSummary,
		ReviewSections:         augmented.Sections,
		Assumptions:            result.Assumptions,
		Stats:                  result.Stats,
		Summary:                result.Summary,
		SourceReview:           result.SourceReview,
		ProtocolCompat:         protocolCompat,
		CredentialRequirements: result.SecretRequirements,
		Parameterization:       buildMultiScriptParameterization(result.Scripts),
		Preflight:              augmented.Preflight,
	})
	if batch != nil && response[importDraftResponseFailureKey] == nil {
		if err := s.saveJMeterBatch(r, batch); err != nil {
			writeError(w, 503, "Cannot secure JMeter batch")
			return
		}
		response["batch"] = batch.Identity
		projected := make([]*dsl.Script, len(batch.Scripts))
		for i, script := range batch.Scripts {
			projected[i] = batchDisclosure(script)
			if projected[i] == nil {
				writeError(w, 500, "Cannot project protected batch")
				return
			}
		}
		response["scripts"] = projected
		response["script"] = projected[0]
		if nested, ok := response["result"].(map[string]any); ok {
			nested["scripts"] = projected
			nested["script"] = projected[0]
		}
	}
	writeScriptImportResponse(w, status, response)
}

// jmeterDraftProjectID is the scope the handler already applied to the plan's
// scripts. This writer also serves the validate route, which has no request-time
// scope of its own, so it reads the scope back off the drafts.
func jmeterDraftProjectID(result *jmeter.ConvertResult) string {
	if result == nil {
		return ""
	}
	if result.Script != nil && result.Script.ProjectID != "" {
		return result.Script.ProjectID
	}
	for _, script := range result.Scripts {
		if script != nil && script.ProjectID != "" {
			return script.ProjectID
		}
	}
	return ""
}

// OpenAPI-Responses: 201
// jmeterImportWorkflow names the JMeter save in the shared receipt helpers. Its
// batch is not committed through the generic pipeline: a JMeter plan persists a
// scenario in the same transaction as its scripts.
var jmeterImportWorkflow = importBatchWorkflow{
	Profile: "jmeter",
	Noun:    "JMeter import transaction",
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
	if len(req.Scripts) > jmeterImportSaveMaxScripts {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("JMeter import batch must contain at most %d scripts", jmeterImportSaveMaxScripts))
		return
	}
	// Resolve the browser-authorized destination before replacing disclosure-safe
	// drafts with their server-owned exact candidates. Otherwise the exact
	// candidate's inspect-time folder silently wins over the operator's selection.
	projectID, folderOverride, status, err := s.resolveJMeterBatchDestination(r, req.Scripts)
	if err != nil {
		writeError(w, status, err.Error())
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
	// Fingerprint the disclosure-safe request (including its opaque one-shot
	// capabilities and selected destination) before claiming those capabilities.
	// A retry of a completed receipt can therefore replay even after the first
	// request consumed every token.
	requestScripts := make([]*dsl.Script, 0, len(req.Scripts))
	for index := range req.Scripts {
		requestScripts = append(requestScripts, &req.Scripts[index])
	}
	requestDigest, err := directJMeterImportRequestDigest(s, requestScripts, req.ScenarioDraft)
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
	if receiptReplay {
		storedScripts, storedScenario, loadErr := s.loadDirectImportReceiptAssets(attempt)
		if loadErr != nil {
			writeError(w, http.StatusConflict, "completed JMeter import receipt is inconsistent: "+loadErr.Error())
			return
		}
		writeJSON(w, http.StatusCreated, savedJMeterImportResponse(storedScripts, storedScenario))
		return
	}
	finished := false
	assetsCommitted := false
	var transaction *createImportTransaction
	defer func() {
		if !finished && !assetsCommitted {
			if cleanupErr := attempt.rollback(transaction); cleanupErr != nil {
				slog.Warn("failed to fully compensate JMeter import", "error", cleanupErr)
			}
		}
	}()
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
	plannedScriptIDs := make([]string, len(requestScripts))
	for index := range plannedScriptIDs {
		if assetIdentitySeed != "" {
			plannedScriptIDs[index] = deterministicDirectImportAssetID(assetIdentitySeed, "script", index)
		} else {
			// Legacy clients that neither echo a preview operation ID nor supply
			// Idempotency-Key retain create-another semantics.
			plannedScriptIDs[index] = uuid.NewString()
		}
	}
	plannedScenarioID := ""
	if req.ScenarioDraft != nil && len(req.ScenarioDraft.Groups) > 0 {
		if assetIdentitySeed != "" {
			plannedScenarioID = deterministicDirectImportAssetID(assetIdentitySeed, "scenario", 0)
		} else {
			plannedScenarioID = uuid.NewString()
		}
	}

	// A stale receipt may have been committed by another controller process
	// immediately before that process lost the receipt-completion response. The
	// browser capability is intentionally process-local, so recover receipt-bound
	// deterministic assets before attempting to claim it. A newly-created receipt
	// never enters this branch and therefore cannot use an expired capability to
	// authorize a new write.
	if attempt != nil && !attempt.created {
		recoveryScripts := make([]*dsl.Script, len(requestScripts))
		for index, script := range requestScripts {
			recoveryScripts[index] = cloneScriptDefinition(script)
			if recoveryScripts[index] == nil {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("script %d is invalid", index+1))
				return
			}
			recoveryScripts[index].ID = plannedScriptIDs[index]
			recoveryScripts[index].RefKey = ""
		}
		recoveryScenarioDraft := cloneJMeterScenarioDraft(req.ScenarioDraft)
		jmeter.SanitizeDisplayLabels(recoveryScripts, recoveryScenarioDraft)
		recoveryScenarioRequest, recoveryErr := buildJMeterScenarioRequest(
			recoveryScripts,
			recoveryScenarioDraft,
		)
		if recoveryErr != nil {
			writeError(w, http.StatusBadRequest, recoveryErr.Error())
			return
		}
		if !s.fenceImportBatchAdoption(w, jmeterImportWorkflow, attempt, projectID, plannedScriptIDs, plannedScenarioID) {
			return
		}
		storedScripts, storedScenario, loadErr := s.loadDirectImportAssetsByIDs(
			projectID,
			plannedScriptIDs,
			plannedScenarioID,
		)
		if loadErr == nil {
			// Existing assets must remain receipt-bound on any verification error;
			// rolling them back here would destroy a previously committed import.
			assetsCommitted = true
			if !recoveredJMeterImportIdentityMatches(
				s,
				storedScripts,
				recoveryScripts,
				storedScenario,
				recoveryScenarioRequest,
			) {
				writeError(w, http.StatusConflict, "an asset from the prior JMeter import changed before recovery; use a new operation id")
				return
			}
			if completeErr := attempt.complete(storedScripts, storedScenario); completeErr != nil {
				writeError(w, http.StatusInternalServerError, "failed to reconcile JMeter import receipt: "+completeErr.Error())
				return
			}
			finished = true
			writeJSON(
				w,
				http.StatusCreated,
				savedJMeterImportResponse(storedScripts, storedScenario),
			)
			return
		}
	}

	claims := make([]*importDraftAttestationClaim, len(req.Scripts))
	defer func() {
		for _, claim := range claims {
			if claim != nil {
				if assetsCommitted {
					s.retainCommittedImportDraftAttestation(claim)
				} else {
					s.releaseImportDraftAttestation(claim)
				}
			}
		}
	}()
	for index := range req.Scripts {
		script := &req.Scripts[index]
		if importDraftAttestationToken(script) == "" {
			fingerprint, fingerprintErr := fingerprintImportDraftCredentials(script)
			if fingerprintErr != nil || fingerprint.HasRawCredentials ||
				scriptHasUnavailableImportCredentialValues(script) {
				writeError(w, http.StatusConflict, "JMeter import draft is missing its server capability; run the import again")
				return
			}
			continue
		}
		claim, claimErr := s.claimImportDraftAttestation(withJMeterBatchSave(r), script)
		if claimErr != nil || claim == nil || claim.Script == nil ||
			!strings.EqualFold(strings.TrimSpace(claim.Profile), "jmeter") {
			if claim != nil {
				s.releaseImportDraftAttestation(claim)
			}
			writeError(w, http.StatusConflict, "JMeter import draft is invalid or expired; run the import again")
			return
		}
		draft := claim.Script
		if err := bindDraftScriptProject(draft, projectID); err != nil {
			s.releaseImportDraftAttestation(claim)
			writeError(w, http.StatusBadRequest, fmt.Sprintf("script %d: %v", index+1, err))
			return
		}
		if status, err := s.applyProtectedImportFolderOverride(draft, folderOverride); err != nil {
			s.releaseImportDraftAttestation(claim)
			writeError(w, status, err.Error())
			return
		}
		restored, replaceErr := replaceUnavailableImportCredentialValues(draft)
		if replaceErr != nil {
			s.releaseImportDraftAttestation(claim)
			writeError(w, http.StatusBadRequest, replaceErr.Error())
			return
		}
		req.Scripts[index] = *restored
		claims[index] = claim
	}

	for i, claim := range claims {
		if claim != nil {
			if _, ok := scriptBatch(&req.Scripts[i]); ok {
				ensureScriptImportPreserved(&req.Scripts[i], "jmeter")[importDraftAttestationMetadataKey] = claim.Token
			}
		}
	}
	if err := s.fenceJMeterBatchSave(r, req.Scripts, req.ScenarioDraft); err != nil {
		writeError(w, http.StatusConflict, "JMeter batch revision conflict: "+err.Error())
		return
	}

	// Prepare and validate the whole import before the first durable write. This
	// dedicated, authorized import commit is the trust boundary for retaining the
	// source's executable credentials: the operator already supplied those bytes,
	// so replacing them with a secret reference here would make the destination
	// script require needless re-entry. Ordinary reads and this response are still
	// disclosure-safe projections.
	preparedScripts := make([]*dsl.Script, 0, len(req.Scripts))
	for i := range req.Scripts {
		script := req.Scripts[i]
		safeScript, err := preparePersistentImportedLibraryScript(&script, nil, "jmeter")
		if err != nil {
			writeError(w, http.StatusBadRequest, "failed to prepare imported script: "+err.Error())
			return
		}
		finalizeJMeterBatchScript(safeScript)
		if issues := validateScript(safeScript); len(issues) > 0 {
			writeError(w, http.StatusBadRequest, strings.Join(issues, "; "))
			return
		}
		if strings.TrimSpace(safeScript.Version) == "" {
			safeScript.Version = "1"
		}
		preparedScripts = append(preparedScripts, safeScript)
	}
	// Allocate names across the complete batch before validating scenario
	// references. Independent JMX entries commonly reuse names such as "Users";
	// those are distinct scripts, not an invalid archive.
	jmeter.SanitizeDisplayLabels(preparedScripts, req.ScenarioDraft)
	seenScriptNames := make(map[string]struct{}, len(preparedScripts))
	for _, safeScript := range preparedScripts {
		nameKey := strings.TrimSpace(safeScript.Name)
		identity := strings.ToLower(nameKey)
		if _, duplicate := seenScriptNames[identity]; duplicate {
			writeError(w, http.StatusBadRequest, "JMeter import contains duplicate script name: "+nameKey)
			return
		}
		seenScriptNames[identity] = struct{}{}
	}
	if req.ScenarioDraft != nil {
		for index, group := range req.ScenarioDraft.Groups {
			scriptName := strings.TrimSpace(group.ScriptName)
			if scriptName == "" {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("scenario group %d is missing script_name", index+1))
				return
			}
			if _, ok := seenScriptNames[strings.ToLower(scriptName)]; !ok {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("scenario group %d references unknown imported script %q", index+1, scriptName))
				return
			}
		}
	}
	for _, script := range preparedScripts {
		if normalizeProjectID(script.ProjectID) != projectID {
			writeError(w, http.StatusBadRequest, "JMeter scripts must belong to one project")
			return
		}
	}
	for index, script := range preparedScripts {
		script.ID = plannedScriptIDs[index]
		script.RefKey = ""
	}

	scenarioID := plannedScenarioID
	scenarioRequest, err := buildJMeterScenarioRequest(preparedScripts, req.ScenarioDraft)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// A stale pending receipt can represent a crash after all assets committed
	// but before the receipt was finalized. Heal that state without entering a
	// new rollback journal, but only when every deterministic asset is exact.
	if attempt != nil && !attempt.created {
		// The receipt lease is longer than the write-ahead claim lease. Recover
		// any expired partial transaction before deciding whether its assets are
		// a completed operation that can be adopted.
		if !s.fenceImportBatchAdoption(w, jmeterImportWorkflow, attempt, projectID, deterministicScriptIDs(preparedScripts), scenarioID) {
			return
		}
		storedScripts, storedScenario, loadErr := s.loadDirectImportAssetsByIDs(
			projectID,
			deterministicScriptIDs(preparedScripts),
			scenarioID,
		)
		if loadErr == nil {
			assetsCommitted = true
			if !directImportPreparedAssetsMatch(
				s, storedScripts, preparedScripts, storedScenario, scenarioRequest,
			) {
				writeError(w, http.StatusConflict, "an asset from the prior JMeter import changed before recovery; use a new operation id")
				return
			}
			if completeErr := attempt.complete(storedScripts, storedScenario); completeErr != nil {
				writeError(w, http.StatusInternalServerError, "failed to reconcile JMeter import receipt: "+completeErr.Error())
				return
			}
			completeJMeterImportDraftClaims(s, claims)
			finished = true
			writeJSON(
				w,
				http.StatusCreated,
				savedJMeterImportResponse(storedScripts, storedScenario),
			)
			return
		}
	}

	transaction, err = newCreateImportTransaction(s, "jmeter", preparedScripts, scenarioID, scenarioRequest)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to prepare imported assets: "+err.Error())
		return
	}

	storedScripts := make([]*StoredScript, 0, len(preparedScripts))
	for index, safeScript := range preparedScripts {
		if err := attempt.renew(); err != nil {
			writeError(w, http.StatusConflict, "JMeter import receipt ownership was lost; retry the save")
			return
		}
		if claims[index] != nil {
			if err := s.assertImportDraftAttestationOwned(claims[index]); err != nil {
				writeError(w, http.StatusConflict, "JMeter import draft lease was lost; retry the save")
				return
			}
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
	completeJMeterImportDraftClaims(s, claims)
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

func cloneJMeterScenarioDraft(draft *jmeter.ScenarioDraft) *jmeter.ScenarioDraft {
	if draft == nil {
		return nil
	}
	clone := *draft
	clone.Groups = append([]jmeter.ScenarioDraftGroup(nil), draft.Groups...)
	for i := range clone.Groups {
		clone.Groups[i].SamplerSchedules = append([]dsl.SamplerSchedule(nil), draft.Groups[i].SamplerSchedules...)
		for j := range clone.Groups[i].SamplerSchedules {
			clone.Groups[i].SamplerSchedules[j].Segments = append([]dsl.SamplerRateSegment(nil), draft.Groups[i].SamplerSchedules[j].Segments...)
		}
	}
	if draft.Metadata != nil {
		clone.Metadata = make(map[string]interface{}, len(draft.Metadata))
		for key, value := range draft.Metadata {
			clone.Metadata[key] = value
		}
	}
	return &clone
}

func buildJMeterScenarioRequest(
	scripts []*dsl.Script,
	draft *jmeter.ScenarioDraft,
) (*CreateScenarioRequest, error) {
	if draft == nil || len(draft.Groups) == 0 {
		return nil, nil
	}
	scriptIDsByName := make(map[string]string, len(scripts))
	scriptsByName := make(map[string]*dsl.Script, len(scripts))
	for index, script := range scripts {
		if script == nil {
			return nil, fmt.Errorf("script %d is invalid", index+1)
		}
		identity := strings.ToLower(strings.TrimSpace(script.Name))
		if identity == "" {
			return nil, fmt.Errorf("script %d is missing its name", index+1)
		}
		if _, duplicate := scriptIDsByName[identity]; duplicate {
			return nil, fmt.Errorf("JMeter import contains duplicate script name: %s", strings.TrimSpace(script.Name))
		}
		scriptIDsByName[identity] = strings.TrimSpace(script.ID)
		scriptsByName[identity] = script
	}

	groups := make([]CreateGroupEntry, 0, len(draft.Groups))
	seenMessagingGroups := make(map[string]bool)
	for index, group := range draft.Groups {
		scriptName := strings.TrimSpace(group.ScriptName)
		if scriptName == "" {
			return nil, fmt.Errorf("scenario group %d is missing script_name", index+1)
		}
		scriptIdentity := strings.ToLower(scriptName)
		script := scriptsByName[scriptIdentity]
		if script == nil {
			return nil, fmt.Errorf("scenario group %d references unknown imported script %q", index+1, scriptName)
		}
		if script.Config.JMeter != nil {
			if group.SourceGroupID == "" || group.SourceGroupID != script.Config.JMeter.SourceGroupID {
				return nil, fmt.Errorf("scenario group %d requires the imported source identity", index+1)
			}
			if seenMessagingGroups[scriptIdentity] {
				return nil, fmt.Errorf("scenario duplicates imported messaging group %q", scriptName)
			}
			seenMessagingGroups[scriptIdentity] = true
		}
		if group.SourceGroupID != "" {
			if script.Metadata == nil || script.Metadata.Import == nil || script.Metadata.Import.Preserved["source_group_id"] != group.SourceGroupID {
				return nil, fmt.Errorf("scenario group %d source identity does not match imported script", index+1)
			}
		}
		if script.Config.JMeter != nil || len(group.SamplerSchedules) > 0 {
			actual, _ := json.Marshal(script.Config.SamplerSchedules)
			requested, _ := json.Marshal(group.SamplerSchedules)
			if string(actual) != string(requested) {
				return nil, fmt.Errorf("scenario group %d sampler schedules do not match imported script", index+1)
			}
		}
		groupName := group.Name
		if groupName == "" {
			groupName = script.Name
			if len(draft.Groups) > 1 {
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
			ScriptID:     scriptIDsByName[scriptIdentity],
			Role:         GroupRoleLoad,
			VUsers:       vusers,
			RampUp:       group.RampUp,
			InitStrategy: initialization,
			Iterations: func() *int {
				if script.Config.RunLogic.Iterations > 0 {
					n := script.Config.RunLogic.Iterations
					return &n
				}
				return nil
			}(),
		})
	}
	for identity, script := range scriptsByName {
		if script.Config.JMeter != nil && !seenMessagingGroups[identity] {
			return nil, fmt.Errorf("scenario omits imported messaging group %q", script.Name)
		}
	}
	request := CreateScenarioRequest{
		Name:            firstNonEmpty(draft.Name, "Imported JMeter Scenario"),
		Duration:        draft.Duration,
		TerminationMode: draft.TerminationMode,
		ScenarioMode:    firstNonEmpty(draft.ScenarioMode, "vuser_driven"),
		Groups:          groups,
	}
	affinity := draft.GeneratorAffinity
	var sourceScheduleDuration time.Duration
	for _, script := range scripts {
		if script.Config.JMeter != nil {
			affinity = "single_generator"
			for _, schedule := range script.Config.SamplerSchedules {
				if schedule.Duration() > sourceScheduleDuration {
					sourceScheduleDuration = schedule.Duration()
				}
			}
		}
	}
	if affinity != "" {
		if affinity != "single_generator" {
			return nil, fmt.Errorf("unsupported JMeter generator affinity")
		}
		request.Assignment = &GeneratorAssignmentSettings{SourceAffinity: affinity}
		if sourceScheduleDuration > 0 {
			parsed, err := time.ParseDuration(request.Duration)
			if err != nil || parsed != sourceScheduleDuration {
				return nil, fmt.Errorf("JMeter messaging scenario duration must preserve source sampler schedules")
			}
		}
		if request.Duration == "" {
			return nil, fmt.Errorf("JMeter messaging scenario requires an explicit schedule duration")
		}
	}
	if request.Duration == "" && request.TerminationMode != dsl.TerminationIterations {
		request.Duration = "5m"
	}
	return &request, nil
}

func recoveredJMeterImportIdentityMatches(
	s *Server,
	storedScripts []*StoredScript,
	requestedScripts []*dsl.Script,
	storedScenario *StoredScenario,
	scenarioRequest *CreateScenarioRequest,
) bool {
	if len(storedScripts) != len(requestedScripts) {
		return false
	}
	for index, requested := range requestedScripts {
		stored := storedScripts[index]
		if stored == nil || stored.Script == nil || requested == nil ||
			strings.TrimSpace(stored.ID) != strings.TrimSpace(requested.ID) ||
			strings.TrimSpace(stored.Name) != strings.TrimSpace(requested.Name) ||
			normalizeProjectID(stored.ProjectID) != normalizeProjectID(requested.ProjectID) ||
			!optionalStringValuesEqual(stored.FolderID, requested.FolderID) ||
			!strings.EqualFold(strings.TrimSpace(stored.Protocol), strings.TrimSpace(requested.Protocol)) ||
			strings.TrimSpace(stored.Version) != firstNonEmpty(strings.TrimSpace(requested.Version), "1") ||
			!isCredentialPreservingImport(stored.Script) ||
			stored.Script.Metadata == nil || stored.Script.Metadata.Import == nil ||
			!strings.EqualFold(strings.TrimSpace(stored.Script.Metadata.Import.Source), "jmeter") {
			return false
		}
		normalizedRequested := cloneScriptDefinition(requested)
		if normalizedRequested == nil {
			return false
		}
		if strings.TrimSpace(normalizedRequested.Version) == "" {
			normalizedRequested.Version = "1"
		}
		ensureCredentialPreservingImportMetadata(normalizedRequested, "jmeter")
		finalizeJMeterBatchScript(normalizedRequested)
		storedDigest, storedErr := canonicalImportDraftStructureDigest(stored.Script)
		requestedDigest, requestedErr := canonicalImportDraftStructureDigest(normalizedRequested)
		if storedErr != nil || requestedErr != nil || storedDigest != requestedDigest {
			return false
		}
	}
	if scenarioRequest == nil {
		return storedScenario == nil
	}
	if s == nil || storedScenario == nil {
		return false
	}
	return s.libraryScenarioRequestRecoveryHash(
		createScenarioRequestFromStored(storedScenario),
	) == s.libraryScenarioRequestRecoveryHash(*scenarioRequest)
}

// resolveJMeterBatchDestination binds one project/folder destination across the
// complete browser-visible batch. The returned folder override is always
// non-nil: an empty value explicitly restores the project root after an opaque
// server candidate is claimed.
func (s *Server) resolveJMeterBatchDestination(
	r *http.Request,
	scripts []dsl.Script,
) (string, *string, int, error) {
	if len(scripts) == 0 {
		return "", nil, http.StatusBadRequest, errors.New("at least one script is required")
	}
	projectID := normalizeProjectID(scripts[0].ProjectID)
	if authorized, scoped := authorizedProjectFromRequest(r); scoped {
		projectID = normalizeProjectID(authorized)
	}
	folderID := ""
	if scripts[0].FolderID != nil {
		folderID = strings.TrimSpace(*scripts[0].FolderID)
	}
	for index := range scripts {
		if err := bindDraftScriptProject(&scripts[index], projectID); err != nil {
			return "", nil, http.StatusBadRequest, fmt.Errorf("script %d: %w", index+1, err)
		}
		currentFolderID := ""
		if scripts[index].FolderID != nil {
			currentFolderID = strings.TrimSpace(*scripts[index].FolderID)
		}
		if currentFolderID != folderID {
			return "", nil, http.StatusBadRequest, errors.New("JMeter scripts must use one destination folder")
		}
	}
	if status, err := s.applyProtectedImportFolderOverride(&scripts[0], &folderID); err != nil {
		return "", nil, status, err
	}
	// Canonicalize every visible draft before receipt fingerprinting so nil and
	// empty root selections cannot create two operation identities.
	for index := range scripts {
		if folderID == "" {
			scripts[index].FolderID = nil
		} else {
			scripts[index].FolderID = &folderID
		}
	}
	return projectID, &folderID, 0, nil
}

func completeJMeterImportDraftClaims(s *Server, claims []*importDraftAttestationClaim) {
	if s == nil {
		return
	}
	for index, claim := range claims {
		if claim == nil {
			continue
		}
		if err := s.completeImportDraftAttestation(claim); err != nil {
			claims[index] = nil
			slog.Warn("failed to finalize JMeter import draft capability", "error", err)
			continue
		}
		claims[index] = nil
	}
}
