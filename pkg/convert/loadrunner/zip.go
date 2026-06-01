package loadrunner

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/trueclient"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// ConvertZip processes a ZIP archive containing a LoadRunner script folder
// and returns the converted APEX Load DSL script.
func ConvertZip(zipData []byte, opts ConvertOptions) (*ConvertResult, error) {
	reader, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return nil, fmt.Errorf("failed to open zip archive: %w", err)
	}

	// Read all files from the zip
	files := make(map[string]string)
	for _, f := range reader.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			continue
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
				return newLoadRunnerReviewResult(detected, files, nil, map[string]any{
					"trueclient_archive_inspection": inspection,
					"source_script_type":            scope.DisplayName,
					"source_profile":                scope.APEXLoadShape,
				}), nil
			default:
				detected := DetectProtocol(files, projectConfig, nil)
				return newLoadRunnerReviewResult(detected, files, nil, map[string]any{
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
		parameterSections := make(map[string]map[string]map[string]string)
		for _, parameterFileName := range parameterFileNames {
			content, ok := findFile(files, parameterFileName)
			if !ok {
				continue
			}
			parsed := ParseParameterFile(parameterFileName, content, files)
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
		result.SourceReview = BuildSourceReview(files, sourceReviewMappings(result))
		attachLoadRunnerMetadata(result.Script, result, projectFileName, projectSections, projectConfig, files, runtimeFileName, parameterSections, parameterFileNames, trueClientInspection)
		return result, nil
	}

	// Determine which .c files to parse
	actionFileNames := []string{"Action.c"}
	if projectConfig != nil && len(projectConfig.ActionFiles) > 0 {
		actionFileNames = projectConfig.ActionFiles
	}

	// Also look for vuser_init.c and vuser_end.c
	initFile := "vuser_init.c"
	endFile := "vuser_end.c"

	// Parse action files
	var parsedActions []*ParsedAction

	// Parse init if exists
	if content, ok := findFile(files, initFile); ok {
		parsed, err := ParseActionFile(initFile, content)
		if err == nil && len(parsed.Functions) > 0 {
			parsedActions = append(parsedActions, parsed)
		}
	}

	// Parse main action files
	for _, actionFile := range actionFileNames {
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
			parsed, err := ParseActionFile(actionFile, content)
			if err == nil {
				parsedActions = append(parsedActions, parsed)
			}
		}
	}

	// Parse end if exists
	if content, ok := findFile(files, endFile); ok {
		parsed, err := ParseActionFile(endFile, content)
		if err == nil && len(parsed.Functions) > 0 {
			parsedActions = append(parsedActions, parsed)
		}
	}

	if len(parsedActions) == 0 {
		detected := DetectProtocol(files, projectConfig, nil)
		if detected.ImportMode != ImportModeExecutable || detected.CanonicalLoadRunnerProtocol == "unknown" {
			return newLoadRunnerReviewResult(detected, files, nil, map[string]any{"reason": "no_parseable_c_actions"}), nil
		}
		return nil, fmt.Errorf("no parseable .c action files found in the ZIP archive")
	}

	detected := DetectProtocol(files, projectConfig, parsedActions)
	var result *ConvertResult
	var convertErr error
	switch {
	case detected.ImportMode != ImportModeExecutable && detected.APEXLoadProtocol == "":
		return newLoadRunnerReviewResult(detected, files, parsedActions, map[string]any{"reason": "protocol_review_required"}), nil
	case detected.APEXLoadProtocol == "database":
		result, convertErr = ConvertDatabase(parsedActions, opts, detected)
	case detected.APEXLoadProtocol == "network":
		result, convertErr = ConvertNetwork(parsedActions, opts, detected)
	case detected.APEXLoadProtocol == "http" || detected.APEXLoadProtocol == "soap":
		result, convertErr = Convert(parsedActions, opts)
		if result != nil {
			result.DetectedProtocol = &detected
			if detected.CanonicalLoadRunnerProtocol == "oracle_web" || detected.CanonicalLoadRunnerProtocol == "siebel_web" {
				result.ReviewArtifact = NewReviewArtifact(detected, files, parsedActions, map[string]any{
					"review_pack": "oracle_ebusiness_correlation",
				})
			}
		}
	default:
		return newLoadRunnerReviewResult(detected, files, parsedActions, map[string]any{"reason": "unsupported_protocol"}), nil
	}
	if convertErr != nil {
		return nil, convertErr
	}
	parameterSections := make(map[string]map[string]map[string]string)
	for _, parameterFileName := range parameterFileNames {
		content, ok := findFile(files, parameterFileName)
		if !ok {
			continue
		}
		parsed := ParseParameterFile(parameterFileName, content, files)
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
	result.SourceReview = BuildSourceReview(files, sourceReviewMappings(result))
	attachLoadRunnerMetadata(result.Script, result, projectFileName, projectSections, projectConfig, files, runtimeFileName, parameterSections, parameterFileNames, nil)
	return result, nil
}

func ptrDetectedProtocol(detected DetectedProtocol) *DetectedProtocol {
	return &detected
}

func newLoadRunnerReviewResult(detected DetectedProtocol, files map[string]string, actions []*ParsedAction, metadata map[string]any) *ConvertResult {
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
			if def, ok := lookupLoadRunnerFunction(fn.Name); ok {
				recordLoadRunnerFunctionSupport(result, def.SupportMode)
			} else {
				recordLoadRunnerFunctionSupport(result, LoadRunnerSupportCustomReview)
			}
		}
	}
	if detected.ImportMode == ImportModeReviewOnly || detected.ImportMode == ImportModeExternalRunnerRequired || detected.ImportMode == ImportModeUnsupported {
		result.Warnings = append(result.Warnings, "Import was classified as "+string(detected.ImportMode)+"; no executable APEX Load script was generated.")
	}
	result.Warnings = uniqueSortedStrings(result.Warnings)
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

func attachLoadRunnerMetadata(script *dsl.Script, result *ConvertResult, projectFileName string, projectSections map[string]map[string]string, projectConfig *ProjectConfig, files map[string]string, runtimeFileName string, parameterSections map[string]map[string]map[string]string, parameterFileNames []string, trueClientInspection *trueclient.ArchiveInspection) {
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
		Source:           "loadrunner",
		SourceScriptName: script.Name,
		Preserved:        preserved,
	}
}
