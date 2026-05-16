package loadrunner

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jasonczech/stormrunner/pkg/convert/trueclient"
	"github.com/jasonczech/stormrunner/pkg/dsl"
)

// ConvertZip processes a ZIP archive containing a LoadRunner script folder
// and returns the converted Stormrunner DSL script.
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
			case trueclient.ScopeSupported:
				trueClientScope = &scope
			default:
				if err := trueclient.ImportScopeError(projectConfig.ScriptType); err != nil {
					return nil, err
				}
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
			Script:   trueClientResult.Script,
			Warnings: append([]string(nil), trueClientResult.Warnings...),
			Stats: ConvertStats{
				TotalFunctions:   trueClientResult.Stats.TotalSteps,
				ConvertedActions: trueClientResult.Stats.ConvertedActions,
				Skipped:          trueClientResult.Stats.Skipped,
				Transactions:     trueClientResult.Stats.Transactions,
				UnsupportedFuncs: append([]string(nil), trueClientResult.Stats.UnsupportedSteps...),
			},
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
		attachLoadRunnerMetadata(result.Script, result, projectFileName, projectSections, projectConfig, files, runtimeFileName, parameterSections, parameterFileNames)
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
		return nil, fmt.Errorf("no parseable .c action files found in the ZIP archive")
	}

	// Run the converter
	result, err := Convert(parsedActions, opts)
	if err != nil {
		return nil, err
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

	attachLoadRunnerMetadata(result.Script, result, projectFileName, projectSections, projectConfig, files, runtimeFileName, parameterSections, parameterFileNames)
	return result, nil
}

// findFile looks up a file in the map by base name, handling path variations.
func findFile(files map[string]string, name string) (string, bool) {
	// Direct match
	if content, ok := files[name]; ok {
		return content, true
	}
	// Try base name match
	baseName := filepath.Base(name)
	for key, content := range files {
		if filepath.Base(key) == baseName {
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

func attachLoadRunnerMetadata(script *dsl.Script, result *ConvertResult, projectFileName string, projectSections map[string]map[string]string, projectConfig *ProjectConfig, files map[string]string, runtimeFileName string, parameterSections map[string]map[string]map[string]string, parameterFileNames []string) {
	if script == nil {
		return
	}

	preserved := make(map[string]any)
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
	if result != nil && result.RecordedResourceURLsCaptured {
		preserved["recorded_resource_urls_available"] = true
		preserved["recorded_resource_urls"] = append([]string(nil), result.RecordedResourceURLs...)
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
