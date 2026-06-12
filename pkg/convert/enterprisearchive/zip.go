package enterprisearchive

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/devweb"
	"github.com/jasonczech/apexload/pkg/convert/trueclient"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// Decompression-bomb guards: bound the uncompressed size of any single entry
// and of the archive as a whole, independent of the (already capped) compressed
// upload size.
const (
	maxEnterpriseArchiveEntryBytes = 64 << 20  // 64 MiB per file
	maxEnterpriseArchiveTotalBytes = 512 << 20 // 512 MiB total uncompressed
)

// ConvertZip processes a ZIP archive containing enterprise C/VUser source files
// and returns either converted APEX Load DSL or a review artifact.
func ConvertZip(zipData []byte, opts ConvertOptions) (*ConvertResult, error) {
	reader, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return nil, fmt.Errorf("failed to open zip archive: %w", err)
	}

	// Read all files from the zip
	files := make(map[string]string)
	var totalUncompressed int64
	for _, f := range reader.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		// Read at most one byte beyond the per-entry cap so we can detect overflow.
		data, err := io.ReadAll(io.LimitReader(rc, maxEnterpriseArchiveEntryBytes+1))
		rc.Close()
		if err != nil {
			continue
		}
		if int64(len(data)) > maxEnterpriseArchiveEntryBytes {
			return nil, fmt.Errorf("zip entry %q exceeds the maximum allowed uncompressed size", filepath.Base(f.Name))
		}
		totalUncompressed += int64(len(data))
		if totalUncompressed > maxEnterpriseArchiveTotalBytes {
			return nil, fmt.Errorf("zip archive exceeds the maximum allowed total uncompressed size")
		}
		// Normalize path: strip leading directory prefix if all files share one
		name := filepath.Base(f.Name)
		dirName := filepath.Dir(f.Name)
		if dirName != "." {
			// Keep relative path from root dir
			name = strings.TrimPrefix(f.Name, strings.SplitN(f.Name, "/", 2)[0]+"/")
			if name == "" {
				name = filepath.Base(f.Name)
			}
		}
		files[name] = string(data)
	}

	// Find and parse .usr file for project config
	var projectConfig *ProjectConfig
	var projectFileName string
	var projectSections map[string]map[string]string
	var trueClientScope *trueclient.ScriptTypeScope
	var trueClientInspection *trueclient.ArchiveInspection
	for name, content := range files {
		if strings.HasSuffix(strings.ToLower(name), ".usr") {
			projectFileName = name
			projectSections = ParseConfigSections(content)
			cfg, err := ParseUSRFile(content)
			if err == nil {
				projectConfig = cfg
				// Use .usr filename (without extension) as script name if not set
				if opts.ScriptName == "" {
					opts.ScriptName = strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))
				}
			}
			break
		}
	}
	if sourceZipHasDevWebMain(files) {
		return convertDevWebZipViaEnterpriseArchiveImport(zipData, opts, files, projectConfig)
	}
	if projectConfig != nil {
		if scope, ok := trueclient.LookupScriptTypeScope(projectConfig.ScriptType); ok {
			switch scope.Status {
			case trueclient.ScopeSupported, trueclient.ScopeReviewOnly:
				trueClientScope = &scope
				inspection, inspectErr := trueclient.InspectArchive(files, projectConfig.ScriptType)
				if inspectErr != nil {
					return nil, inspectErr
				}
				trueClientInspection = inspection
			case trueclient.ScopePlanned:
				inspection, inspectErr := trueclient.InspectArchive(files, projectConfig.ScriptType)
				if inspectErr != nil {
					return nil, inspectErr
				}
				detected := DetectProtocol(files, projectConfig, nil)
				detected.ImportMode = ImportModeReviewOnly
				detected.RequiredEvidence = appendUnique(detected.RequiredEvidence, scope.RequiredEvidence...)
				detected.Warnings = append(detected.Warnings, fmt.Sprintf("%s import is planned; archive was preserved as a review artifact.", scope.DisplayName))
				return newEnterpriseArchiveReviewResult(detected, files, nil, map[string]any{
					"trueclient_archive_inspection": inspection,
					"source_script_type":            scope.DisplayName,
					"source_profile":                scope.APEXLoadShape,
				}), nil
			default:
				detected := DetectProtocol(files, projectConfig, nil)
				return newEnterpriseArchiveReviewResult(detected, files, nil, map[string]any{
					"source_script_type": scope.DisplayName,
					"source_profile":     scope.APEXLoadShape,
					"scope_status":       string(scope.Status),
				}), nil
			}
		}
	}

	runtimeFileName, runtimeContent := findRuntimeConfig(files, projectConfig)
	if runtimeContent != "" {
		runtimeCfg := ParseRuntimeCfg(runtimeContent)
		if opts.HTTPConfig == nil {
			opts.HTTPConfig = runtimeCfg.ToHTTPConfig()
		}
		if opts.RuntimeSettings == nil {
			opts.RuntimeSettings = runtimeCfg.ToScriptConfig()
		}
	}
	parameterFileNames := findParameterFiles(files, projectConfig)
	if trueClientScope != nil {
		trueClientResult, err := trueclient.ConvertArchive(files, trueclient.ConvertOptions{
			ScriptName:           opts.ScriptName,
			BaseURL:              opts.BaseURL,
			PreserveTransactions: opts.PreserveTransactions,
		})
		if err != nil {
			return nil, err
		}
		if opts.RuntimeSettings != nil {
			mergeScriptConfig(&trueClientResult.Script.Config, opts.RuntimeSettings)
		}
		result := &ConvertResult{
			Script:           trueClientResult.Script,
			Warnings:         append([]string(nil), trueClientResult.Warnings...),
			DetectedProtocol: ptrDetectedProtocol(DetectProtocol(files, projectConfig, nil)),
			Stats: ConvertStats{
				TotalFunctions:   trueClientResult.Stats.TotalSteps,
				ConvertedActions: trueClientResult.Stats.ConvertedActions,
				Skipped:          trueClientResult.Stats.Skipped,
				Transactions:     trueClientResult.Stats.Transactions,
				UnsupportedFuncs: append([]string(nil), trueClientResult.Stats.UnsupportedSteps...),
			},
		}
		if result.DetectedProtocol != nil && result.DetectedProtocol.ImportMode != ImportModeExecutable {
			result.ReviewArtifact = NewReviewArtifact(*result.DetectedProtocol, files, nil, map[string]any{
				"trueclient_archive_inspection": trueClientInspection,
			})
		}
		if trueClientScope.Status == trueclient.ScopeReviewOnly {
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s import is review-only; validate selectors, waits, runtime settings, and replay evidence before promotion.", trueClientScope.DisplayName))
		}
		finalizeEnterpriseArchiveImportResult(result, archiveFinalizationContext{
			Files:                files,
			ProjectFileName:      projectFileName,
			ProjectSections:      projectSections,
			ProjectConfig:        projectConfig,
			RuntimeFileName:      runtimeFileName,
			ParameterFileNames:   parameterFileNames,
			TrueClientInspection: trueClientInspection,
		})
		return result, nil
	}
	if isJavaProtocolsProject(projectConfig) || isJavaVuserArchive(files, projectConfig) {
		detected := DetectProtocol(files, projectConfig, nil)
		result, err := ConvertJava(files, opts, detected)
		if err != nil {
			return nil, err
		}
		if opts.RuntimeSettings != nil && result.Script != nil {
			mergeScriptConfig(&result.Script.Config, opts.RuntimeSettings)
		}
		finalizeEnterpriseArchiveImportResult(result, archiveFinalizationContext{
			Files:              files,
			ProjectFileName:    projectFileName,
			ProjectSections:    projectSections,
			ProjectConfig:      projectConfig,
			RuntimeFileName:    runtimeFileName,
			ParameterFileNames: parameterFileNames,
			ParsedActions:      javaSourcesAsParsedActions(orderedJavaSourceActionFiles(projectConfig, files), files),
		})
		return result, nil
	}

	// Parse each lifecycle/action file once. Some .usr [Actions] blocks include
	// vuser_init.c and vuser_end.c, so a naive "parse init, parse [Actions],
	// parse end" sequence duplicates setup and cleanup requests.
	sourceConstants := collectSourceStringConstants(files)
	var parsedActions []*ParsedAction
	var parseErrors []string
	for _, actionFile := range orderedSourceActionFiles(projectConfig, files) {
		content, ok := findFile(files, actionFile)
		if !ok {
			// Try case-insensitive
			for name, c := range files {
				if strings.EqualFold(filepath.Base(name), actionFile) {
					content = c
					ok = true
					break
				}
			}
		}
		if ok {
			parsed, err := ParseActionFile(actionFile, inlineSourceStringConstants(content, sourceConstants))
			if err == nil {
				parsed.Phase = sourceActionPhase(actionFile)
				for index := range parsed.Functions {
					parsed.Functions[index].Phase = parsed.Phase
				}
				parsedActions = append(parsedActions, parsed)
			} else {
				parseErrors = append(parseErrors, fmt.Sprintf("%s: %v", actionFile, err))
			}
		}
	}

	if len(parsedActions) == 0 {
		detected := DetectProtocol(files, projectConfig, nil)
		if isSAPGUIProject(projectConfig) || detected.CanonicalSourceProtocol == "sap_gui" {
			return sapGUINoParseReview(files, detected), nil
		}
		metadata := map[string]any{"reason": "no_parseable_c_actions"}
		if len(parseErrors) > 0 {
			metadata["parse_errors"] = parseErrors
			detected.ImportMode = ImportModeReviewOnly
			detected.Warnings = append(detected.Warnings, "C source parsing requires review before executable conversion.")
			detected.RequiredEvidence = appendUnique(detected.RequiredEvidence, "Resolved C parser diagnostics or operator-approved review artifact")
			return newEnterpriseArchiveReviewResult(detected, files, nil, metadata), nil
		}
		if detected.ImportMode != ImportModeExecutable || detected.CanonicalSourceProtocol == "unknown" {
			return newEnterpriseArchiveReviewResult(detected, files, nil, metadata), nil
		}
		return nil, fmt.Errorf("no parseable .c action files found in the ZIP archive")
	}

	detected := DetectProtocol(files, projectConfig, parsedActions)
	if isSAPGUIProtocolDetected(detected) || hasSAPGUIFunctions(parsedActions) {
		result, err := ConvertSAPGUI(parsedActions, opts, detected, files)
		if err != nil {
			return nil, err
		}
		if opts.RuntimeSettings != nil && result.Script != nil {
			mergeScriptConfig(&result.Script.Config, opts.RuntimeSettings)
		}
		finalizeEnterpriseArchiveImportResult(result, archiveFinalizationContext{
			Files:              files,
			ProjectFileName:    projectFileName,
			ProjectSections:    projectSections,
			ProjectConfig:      projectConfig,
			RuntimeFileName:    runtimeFileName,
			ParameterFileNames: parameterFileNames,
		})
		attachImportFidelityReport(result, files, projectConfig, parsedActions)
		return result, nil
	}
	var result *ConvertResult
	var convertErr error
	switch {
	case detected.ImportMode != ImportModeExecutable && detected.APEXLoadProtocol == "":
		return newEnterpriseArchiveReviewResult(detected, files, parsedActions, map[string]any{"reason": "protocol_review_required"}), nil
	case detected.APEXLoadProtocol == "database":
		result, convertErr = ConvertDatabase(parsedActions, opts, detected)
	case detected.APEXLoadProtocol == "network":
		result, convertErr = ConvertNetwork(parsedActions, opts, detected)
	case detected.APEXLoadProtocol == "http" || detected.APEXLoadProtocol == "soap":
		opts.UseMaskedBodySecretReferences = true
		result, convertErr = Convert(parsedActions, opts)
		if result != nil {
			result.DetectedProtocol = &detected
			if detected.CanonicalSourceProtocol == "oracle_web" || detected.CanonicalSourceProtocol == "siebel_web" {
				result.ReviewArtifact = NewReviewArtifact(detected, files, parsedActions, map[string]any{
					"review_pack": "oracle_ebusiness_correlation",
				})
			}
		}
	default:
		return newEnterpriseArchiveReviewResult(detected, files, parsedActions, map[string]any{"reason": "unsupported_protocol"}), nil
	}
	if convertErr != nil {
		return nil, convertErr
	}
	finalizeEnterpriseArchiveImportResult(result, archiveFinalizationContext{
		Files:              files,
		ProjectFileName:    projectFileName,
		ProjectSections:    projectSections,
		ProjectConfig:      projectConfig,
		RuntimeFileName:    runtimeFileName,
		ParameterFileNames: parameterFileNames,
		ParsedActions:      parsedActions,
	})
	return result, nil
}

type archiveFinalizationContext struct {
	Files                map[string]string
	ProjectFileName      string
	ProjectSections      map[string]map[string]string
	ProjectConfig        *ProjectConfig
	RuntimeFileName      string
	ParameterFileNames   []string
	TrueClientInspection *trueclient.ArchiveInspection
	ParsedActions        []*ParsedAction
}

func finalizeEnterpriseArchiveImportResult(result *ConvertResult, ctx archiveFinalizationContext) {
	if result == nil {
		return
	}
	parameterSections := make(map[string]map[string]map[string]string)
	if result.Script != nil {
		for _, parameterFileName := range ctx.ParameterFileNames {
			content, ok := findFile(ctx.Files, parameterFileName)
			if !ok {
				continue
			}
			parsed := ParseParameterFile(parameterFileName, content, ctx.Files)
			if len(parsed.Parameters) > 0 {
				result.Script.Parameters = append(result.Script.Parameters, parsed.Parameters...)
			}
			if len(parsed.DataSources) > 0 {
				result.Script.DataSources = append(result.Script.DataSources, parsed.DataSources...)
			}
			if len(parsed.Warnings) > 0 {
				result.Warnings = append(result.Warnings, parsed.Warnings...)
			}
			if len(parsed.Sections) > 0 {
				parameterSections[filepath.Base(parameterFileName)] = parsed.Sections
			}
		}
		applyKnownParameterReferences(result.Script)
		applyParameterizedBaseURL(result.Script)
	}
	result.SourceReview = BuildSourceReview(ctx.Files, sourceReviewMappings(result))
	redactArchiveMaskedBodyMetadata(result.Script)
	attachSourceMetadata(result.Script, result, ctx.ProjectFileName, ctx.ProjectSections, ctx.ProjectConfig, ctx.Files, ctx.RuntimeFileName, parameterSections, ctx.ParameterFileNames, ctx.TrueClientInspection)
	attachImportFidelityReport(result, ctx.Files, ctx.ProjectConfig, ctx.ParsedActions)
}

func redactArchiveMaskedBodyMetadata(script *dsl.Script) {
	if script == nil {
		return
	}
	var redactActions func([]dsl.Action)
	redactActions = func(actions []dsl.Action) {
		for i := range actions {
			if actions[i].Metadata != nil {
				if _, ok := actions[i].Metadata["masked_body_raw"]; ok {
					actions[i].Metadata["masked_body_raw"] = "lr_unmask(\"[REDACTED]\")"
				}
				if _, ok := actions[i].Metadata["masked_body_calls"]; ok {
					actions[i].Metadata["masked_body_calls"] = []string{"lr_unmask(\"[REDACTED]\")"}
				}
			}
			if actions[i].Control != nil {
				redactActions(actions[i].Control.Steps)
				redactActions(actions[i].Control.Else)
			}
		}
	}
	redactActions(script.Init)
	redactActions(script.Actions)
	redactActions(script.Teardown)
}

func orderedSourceActionFiles(projectConfig *ProjectConfig, files map[string]string) []string {
	candidates := []string{"vuser_init.c"}
	if projectConfig != nil && len(projectConfig.SourceActionFiles) > 0 {
		for _, sourceFile := range projectConfig.SourceActionFiles {
			if strings.EqualFold(sourceFile.Language, "c") || strings.HasSuffix(strings.ToLower(sourceFile.Name), ".c") {
				candidates = append(candidates, sourceFile.Name)
			}
		}
	} else if projectConfig != nil && len(projectConfig.ActionFiles) > 0 {
		candidates = append(candidates, projectConfig.ActionFiles...)
	} else {
		candidates = append(candidates, "Action.c")
	}
	candidates = append(candidates, "vuser_end.c")

	ordered := make([]string, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		name := strings.TrimSpace(candidate)
		if name == "" {
			continue
		}
		key := strings.ToLower(filepath.Base(name))
		if _, exists := seen[key]; exists {
			continue
		}
		if _, ok := findFile(files, name); !ok {
			found := false
			for fileName := range files {
				if strings.EqualFold(filepath.Base(fileName), filepath.Base(name)) {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		seen[key] = struct{}{}
		ordered = append(ordered, name)
	}
	return ordered
}

func sourceActionPhase(fileName string) string {
	base := strings.ToLower(filepath.Base(strings.TrimSpace(fileName)))
	switch base {
	case "vuser_init.c", "vuser_init.java":
		return "init"
	case "vuser_end.c", "vuser_end.java":
		return "teardown"
	default:
		return "action"
	}
}

func ptrDetectedProtocol(detected DetectedProtocol) *DetectedProtocol {
	return &detected
}

func newEnterpriseArchiveReviewResult(detected DetectedProtocol, files map[string]string, actions []*ParsedAction, metadata map[string]any) *ConvertResult {
	if detected.ImportMode == "" {
		detected.ImportMode = ImportModeReviewOnly
	}
	result := &ConvertResult{
		DetectedProtocol: &detected,
		Warnings:         append([]string(nil), detected.Warnings...),
		ReviewArtifact:   NewReviewArtifact(detected, files, actions, metadata),
		SourceReview:     BuildSourceReview(files, reviewOnlySourceReviewMappings(actions, "review_only", "Import was gated for protocol review.")),
		Stats:            ConvertStats{},
	}
	for _, action := range actions {
		if action == nil {
			continue
		}
		result.Stats.TotalFunctions += len(action.Functions)
		for _, fn := range action.Functions {
			if def, ok := lookupSourceFunction(fn.Name); ok {
				recordSourceFunctionSupport(result, def.SupportMode)
			} else {
				recordSourceFunctionSupport(result, SourceFunctionSupportCustomReview)
			}
		}
	}
	if detected.ImportMode == ImportModeReviewOnly || detected.ImportMode == ImportModeExternalRunnerRequired || detected.ImportMode == ImportModeUnsupported {
		result.Warnings = append(result.Warnings, "Import was classified as "+string(detected.ImportMode)+"; no executable APEX Load script was generated.")
	}
	result.Warnings = uniqueSortedStrings(result.Warnings)
	attachImportFidelityReport(result, files, nil, actions)
	return result
}

// findFile looks up a file in the map by base name, handling path variations.
func findFile(files map[string]string, name string) (string, bool) {
	name = cleanConfigValue(name)
	// Direct match
	if content, ok := files[name]; ok {
		return content, true
	}
	for key, content := range files {
		if strings.EqualFold(key, name) {
			return content, true
		}
	}
	// Try base name match
	baseName := filepath.Base(name)
	for key, content := range files {
		if filepath.Base(key) == baseName || strings.EqualFold(filepath.Base(key), baseName) {
			return content, true
		}
	}
	return "", false
}

func findRuntimeConfig(files map[string]string, projectConfig *ProjectConfig) (string, string) {
	if projectConfig != nil && projectConfig.RuntimeFile != "" {
		if content, ok := findFile(files, projectConfig.RuntimeFile); ok {
			return projectConfig.RuntimeFile, content
		}
	}

	for name, content := range files {
		if strings.EqualFold(filepath.Base(name), "default.cfg") {
			return name, content
		}
	}

	cfgNames := make([]string, 0)
	for name := range files {
		if strings.HasSuffix(strings.ToLower(name), ".cfg") {
			cfgNames = append(cfgNames, name)
		}
	}
	sort.Strings(cfgNames)
	if len(cfgNames) == 0 {
		return "", ""
	}

	name := cfgNames[0]
	return name, files[name]
}

func findParameterFiles(files map[string]string, projectConfig *ProjectConfig) []string {
	if projectConfig != nil && len(projectConfig.ParamFiles) > 0 {
		resolved := make([]string, 0, len(projectConfig.ParamFiles))
		for _, name := range projectConfig.ParamFiles {
			if _, ok := findFile(files, name); ok {
				resolved = append(resolved, name)
			}
		}
		if len(resolved) > 0 {
			return uniqueStrings(resolved)
		}
	}

	parameterFiles := make([]string, 0)
	for name := range files {
		if strings.HasSuffix(strings.ToLower(name), ".prm") {
			parameterFiles = append(parameterFiles, name)
		}
	}
	sort.Strings(parameterFiles)
	return uniqueStrings(parameterFiles)
}

func sourceZipHasDevWebMain(files map[string]string) bool {
	for name, content := range files {
		if strings.EqualFold(filepath.Base(name), "main.js") && strings.Contains(content, "load.") {
			return true
		}
	}
	return false
}

func isJavaVuserArchive(files map[string]string, projectConfig *ProjectConfig) bool {
	if projectConfig != nil {
		if strings.Contains(strings.ToLower(projectConfig.ScriptLanguage), "java") || strings.Contains(strings.ToLower(projectConfig.RunType), "java") {
			return true
		}
		for _, actionFile := range projectConfig.ActionFiles {
			if strings.HasSuffix(strings.ToLower(actionFile), ".java") {
				return true
			}
		}
	}
	for name, content := range files {
		base := strings.ToLower(filepath.Base(name))
		if base == "actions.java" || base == "vuser_init.java" || base == "vuser_end.java" {
			return true
		}
		if strings.HasSuffix(base, ".java") && strings.Contains(strings.ToLower(content), "lrapi") {
			return true
		}
	}
	return false
}

func convertDevWebZipViaEnterpriseArchiveImport(zipData []byte, opts ConvertOptions, files map[string]string, projectConfig *ProjectConfig) (*ConvertResult, error) {
	devResult, err := devweb.ConvertZip(zipData, devweb.ConvertOptions{
		BaseURL:            opts.BaseURL,
		DefaultThinkTimeMs: int(opts.DefaultThinkTimeMs),
	})
	if err != nil {
		return nil, err
	}
	detected := detectedDevWebProtocol(projectConfig, devResult)
	unsupported := append([]string(nil), devResult.Stats.UnsupportedCalls...)
	unsupported = append(unsupported, devResult.Unsupported.Strings()...)
	result := &ConvertResult{
		Script:           devResult.Script,
		Warnings:         append([]string(nil), devResult.Warnings...),
		DetectedProtocol: &detected,
		SourceReview:     devResult.SourceReview,
		Stats: ConvertStats{
			TotalFunctions:      devResult.Stats.TotalSteps,
			ConvertedActions:    devResult.Stats.ConvertedActions,
			RawRequestActions:   devResult.Stats.HTTPRequests,
			ThinkTimes:          devResult.Stats.ThinkTimes,
			Transactions:        devResult.Stats.Transactions,
			Extractors:          devResult.Stats.Extractors,
			Skipped:             devResult.Stats.Skipped,
			ReviewOnlyFunctions: len(devResult.Unsupported),
			UnsupportedFuncs:    uniqueStrings(unsupported),
		},
	}
	if result.Script != nil {
		preserved := ensureDevWebSourcePreserved(result.Script)
		preserved["detected_protocol"] = result.DetectedProtocol
		preserved["source_endpoint_routed_to"] = "devweb"
		if result.SourceReview != nil {
			preserved["source_review"] = result.SourceReview
		}
		if len(files) > 0 {
			preserved["archive_file_count"] = len(files)
		}
	}
	if len(result.Stats.UnsupportedFuncs) > 0 {
		result.Warnings = append(result.Warnings, "DevWeb archive imported through the enterprise script archive endpoint has review-gated JavaScript or helper constructs.")
	}
	attachImportFidelityReport(result, files, projectConfig, nil)
	return result, nil
}

func detectedDevWebProtocol(projectConfig *ProjectConfig, result *devweb.ConvertResult) DetectedProtocol {
	apexProtocol := "http"
	if result != nil && result.Script != nil && strings.TrimSpace(result.Script.Protocol) != "" {
		apexProtocol = result.Script.Protocol
	}
	signals := []string{"DevWeb main.js archive"}
	confidence := DetectionConfidenceMedium
	sourceType := ""
	if projectConfig != nil {
		sourceType = strings.TrimSpace(projectConfig.ScriptType)
		if strings.EqualFold(sourceType, "devweb") {
			signals = append(signals, ".usr [General] Type=DevWeb")
			confidence = DetectionConfidenceHigh
		}
		for _, activeType := range projectConfig.ActiveTypes {
			if strings.EqualFold(activeType, "devweb") {
				signals = append(signals, ".usr [General] ActiveTypes=DevWeb")
				confidence = DetectionConfidenceHigh
			}
		}
	}
	return DetectedProtocol{
		CanonicalSourceProtocol: "devweb",
		APEXLoadProtocol:        apexProtocol,
		Bundle:                  "Templates Protocol Bundle",
		ImportMode:              ImportModeExecutable,
		Confidence:              confidence,
		DisplayName:             "DevWeb",
		SourceType:              sourceType,
		Signals:                 uniqueSortedStrings(signals),
		RequiredEvidence:        []string{"Native DevWeb import review", "Replay validation evidence"},
	}
}

func ensureDevWebSourcePreserved(script *dsl.Script) map[string]any {
	if script.Metadata == nil {
		script.Metadata = &dsl.ScriptMetadata{}
	}
	if script.Metadata.Import == nil {
		script.Metadata.Import = &dsl.ImportMetadata{Source: "devweb"}
	}
	if script.Metadata.Import.Preserved == nil {
		script.Metadata.Import.Preserved = map[string]any{}
	}
	return script.Metadata.Import.Preserved
}

func collectRuntimeSections(files map[string]string) map[string]map[string]map[string]string {
	sections := make(map[string]map[string]map[string]string)
	fileNames := make([]string, 0)
	for name := range files {
		if strings.HasSuffix(strings.ToLower(name), ".cfg") {
			fileNames = append(fileNames, name)
		}
	}
	sort.Strings(fileNames)
	for _, name := range fileNames {
		sections[filepath.Base(name)] = ParseConfigSections(files[name])
	}
	return sections
}

func uniqueStrings(values []string) []string {
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
	return result
}

func attachSourceMetadata(script *dsl.Script, result *ConvertResult, projectFileName string, projectSections map[string]map[string]string, projectConfig *ProjectConfig, files map[string]string, runtimeFileName string, parameterSections map[string]map[string]map[string]string, parameterFileNames []string, trueClientInspection *trueclient.ArchiveInspection) {
	if script == nil {
		return
	}

	preserved := make(map[string]any)
	if script.Metadata != nil && script.Metadata.Import != nil && len(script.Metadata.Import.Preserved) > 0 {
		for key, value := range script.Metadata.Import.Preserved {
			preserved[key] = value
		}
	}
	if projectFileName != "" {
		preserved["project_file"] = filepath.Base(projectFileName)
	}
	if len(projectSections) > 0 {
		preserved["project_settings"] = projectSections
	}
	if projectConfig != nil && projectConfig.RuntimeFile != "" {
		preserved["declared_runtime_file"] = filepath.Base(projectConfig.RuntimeFile)
	}
	runtimeSections := collectRuntimeSections(files)
	if len(runtimeSections) > 0 {
		preserved["runtime_files"] = runtimeSections
		if parity := BuildRTSParityReport(runtimeSections); len(parity) > 0 {
			preserved["runtime_settings_parity"] = parity
			preserved["runtime_settings_status_counts"] = RuntimeSettingStatusCounts(parity)
		}
	}
	if runtimeFileName != "" {
		preserved["mapped_runtime_file"] = filepath.Base(runtimeFileName)
	}
	if len(parameterFileNames) > 0 {
		files := make([]string, 0, len(parameterFileNames))
		for _, name := range parameterFileNames {
			files = append(files, filepath.Base(name))
		}
		preserved["parameter_files"] = uniqueStrings(files)
	}
	if len(parameterSections) > 0 {
		preserved["parameter_file_sections"] = parameterSections
	}
	if result != nil && len(result.Stats.UnsupportedFuncs) > 0 {
		preserved["unsupported_functions"] = uniqueStrings(result.Stats.UnsupportedFuncs)
	}
	if result != nil && len(result.Warnings) > 0 {
		preserved["warnings"] = append([]string(nil), result.Warnings...)
	}
	if result != nil && result.DetectedProtocol != nil {
		preserved["detected_protocol"] = result.DetectedProtocol
	}
	if result != nil && result.ReviewArtifact != nil {
		preserved["review_artifact"] = result.ReviewArtifact
	}
	if result != nil && result.SourceReview != nil {
		preserved["source_review"] = result.SourceReview
	}
	if result != nil && result.RecordedResourceURLsCaptured {
		preserved["recorded_resource_urls_available"] = true
		preserved["recorded_resource_urls"] = append([]string(nil), result.RecordedResourceURLs...)
	}
	if trueClientInspection != nil {
		preserved["trueclient_archive_inspection"] = trueClientInspection
		if trueClientInspection.ScriptTypeScope != nil {
			preserved["source_script_type"] = trueClientInspection.ScriptTypeScope.DisplayName
			preserved["source_profile"] = trueClientInspection.ScriptTypeScope.APEXLoadShape
		}
	}
	if len(preserved) == 0 {
		return
	}

	if script.Metadata == nil {
		script.Metadata = &dsl.ScriptMetadata{}
	}
	script.Metadata.Import = &dsl.ImportMetadata{
		Source:           "enterprise_script_archive",
		SourceScriptName: script.Name,
		Preserved:        preserved,
	}
}
