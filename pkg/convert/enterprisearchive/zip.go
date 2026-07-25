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
	maxEnterpriseArchiveEntries    = 4096
)

// Exported so any caller that rewrites an archive before ConvertZip (for example
// the API's supplemental-file merge) enforces the SAME decompression-bomb bounds.
// A pre-pass that inflates entries without these caps silently defeats them.
const (
	MaxArchiveEntryBytes = maxEnterpriseArchiveEntryBytes
	MaxArchiveTotalBytes = maxEnterpriseArchiveTotalBytes
	MaxArchiveEntries    = maxEnterpriseArchiveEntries
)

// ConvertZip processes a ZIP archive containing enterprise C/VUser source files
// and returns either converted APEX Load DSL or a review artifact.
func ConvertZip(zipData []byte, opts ConvertOptions) (*ConvertResult, error) {
	if len(zipData) < 4 {
		return nil, fmt.Errorf("zip file is too small (%d bytes); expected at least 4 bytes for ZIP magic header", len(zipData))
	}
	// Verify ZIP magic bytes (PK)
	if zipData[0] != 0x50 || zipData[1] != 0x4b {
		return nil, fmt.Errorf("invalid zip file: magic bytes mismatch (expected PK, got %02x%02x); file may be corrupted or truncated during upload", zipData[0], zipData[1])
	}

	reader, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return nil, fmt.Errorf("failed to open zip archive (size=%d bytes): %w; the file may be corrupted, incomplete, or use an unsupported ZIP compression method", len(zipData), err)
	}
	if len(reader.File) > maxEnterpriseArchiveEntries {
		return nil, fmt.Errorf("zip archive contains %d entries; maximum is %d", len(reader.File), maxEnterpriseArchiveEntries)
	}

	// Read all files from the zip
	files := make(map[string]string)
	normalizedFileNames := make(map[string]string)
	var totalUncompressed int64
	for _, f := range reader.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("open zip entry %q: %w", f.Name, err)
		}
		// Read at most one byte beyond the per-entry cap so we can detect overflow.
		data, err := io.ReadAll(io.LimitReader(rc, maxEnterpriseArchiveEntryBytes+1))
		closeErr := rc.Close()
		if err != nil {
			return nil, fmt.Errorf("read zip entry %q: %w", f.Name, err)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close zip entry %q: %w", f.Name, closeErr)
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
		if _, duplicate := files[name]; duplicate {
			return nil, fmt.Errorf("zip archive contains duplicate normalized entry %q", name)
		}
		// Source projects are authored on case-insensitive Windows filesystems,
		// and all downstream archive lookups intentionally use EqualFold. Reject
		// case-only aliases here rather than letting map iteration choose which
		// runtime, project, or source file wins.
		caseFoldedName := strings.ToLower(name)
		if existing, duplicate := normalizedFileNames[caseFoldedName]; duplicate {
			return nil, fmt.Errorf("zip archive contains case-insensitive duplicate normalized entries %q and %q", existing, name)
		}
		normalizedFileNames[caseFoldedName] = name
		files[name] = string(data)
	}

	// Find and parse .usr file for project config
	var projectConfig *ProjectConfig
	var projectFileName string
	var projectSections map[string]map[string]string
	var trueClientScope *trueclient.ScriptTypeScope
	var trueClientInspection *trueclient.ArchiveInspection
	projectFileNames := make([]string, 0, 1)
	for name := range files {
		if strings.HasSuffix(strings.ToLower(name), ".usr") {
			projectFileNames = append(projectFileNames, name)
		}
	}
	sort.Strings(projectFileNames)
	if len(projectFileNames) > 1 {
		return nil, fmt.Errorf(
			"zip archive contains multiple LoadRunner project (.usr) files (%s); import exactly one script project per archive",
			strings.Join(projectFileNames, ", "),
		)
	}
	if len(projectFileNames) == 1 {
		projectFileName = projectFileNames[0]
		content := files[projectFileName]
		projectSections = ParseConfigSections(content)
		cfg, err := ParseUSRFile(content)
		if err != nil {
			return nil, fmt.Errorf("parse LoadRunner project file %q: %w", projectFileName, err)
		}
		projectConfig = cfg
		// Use .usr filename (without extension) as script name if not set
		if opts.ScriptName == "" {
			opts.ScriptName = strings.TrimSuffix(
				filepath.Base(projectFileName),
				filepath.Ext(projectFileName),
			)
		}
	}
	javaArchive := isJavaProtocolsProject(projectConfig) ||
		isJavaVuserArchive(files, projectConfig)
	if sourceZipHasDevWebMain(files) && !javaArchive {
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
	var runtimeCfg *RuntimeConfig
	if runtimeContent != "" {
		runtimeCfg = ParseRuntimeCfg(runtimeContent)
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
				// Propagate the converter's review-only step accounting so downstream
				// ConvertStats consumers see it (and TotalFunctions reconciles against
				// Converted + Skipped + ReviewOnly + Unsupported); the TrueClient branch
				// previously dropped it, unlike the Java/utility/devweb branches.
				ReviewOnlyFunctions: trueClientResult.Stats.ReviewOnlySteps,
				UnsupportedFuncs:    append([]string(nil), trueClientResult.Stats.UnsupportedSteps...),
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
			RuntimeConfig:        runtimeCfg,
			ParameterFileNames:   parameterFileNames,
			TrueClientInspection: trueClientInspection,
		})
		return result, nil
	}
	if javaArchive {
		detected := DetectProtocol(files, projectConfig, nil)
		result, err := ConvertJava(files, opts, detected, projectConfig)
		if err != nil {
			return nil, err
		}
		if opts.RuntimeSettings != nil && result.Script != nil {
			mergeScriptConfig(&result.Script.Config, opts.RuntimeSettings)
		}
		mergeJavaVuserRuntimeSettingsIntoResult(result, opts.RuntimeSettings)
		applyJavaVMRuntimeSettingsReview(result, runtimeCfg, files)
		finalizeEnterpriseArchiveImportResult(result, archiveFinalizationContext{
			Files:              files,
			ProjectFileName:    projectFileName,
			ProjectSections:    projectSections,
			ProjectConfig:      projectConfig,
			RuntimeFileName:    runtimeFileName,
			RuntimeConfig:      runtimeCfg,
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
			RuntimeConfig:      runtimeCfg,
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
		RuntimeConfig:      runtimeCfg,
		ParameterFileNames: parameterFileNames,
		ParsedActions:      parsedActions,
	})
	return result, nil
}

const javaVMParametersReviewBlocker = "Imported Java VM parameters contain malformed quoting; executable VM parameters were not populated and the draft must remain review-blocked until the source runtime setting is corrected."
const javaVMClasspathReviewBlocker = "Imported Java VM classpath contains malformed quoting; executable classpath entries were not populated and the draft must remain review-blocked until the source runtime setting is corrected."
const javaVMOptionsReviewBlocker = "Imported Java VM settings contain an unsupported selector or invalid boolean; the ambiguous setting was not populated and the draft must remain review-blocked until the source runtime setting is corrected."

func applyJavaVMRuntimeSettingsReview(result *ConvertResult, runtimeCfg *RuntimeConfig, files map[string]string) {
	if result == nil || runtimeCfg == nil {
		return
	}

	type reviewIssue struct {
		blocker     string
		evidence    string
		nextAction  string
		metadataKey string
		rawValue    string
	}
	issues := make([]reviewIssue, 0, 4)
	if runtimeCfg.JavaVMParametersParseError != "" {
		issues = append(issues, reviewIssue{
			blocker:     javaVMParametersReviewBlocker,
			evidence:    "Corrected and re-imported Java_VM_Params quoting",
			nextAction:  "Correct the quoted Java VM parameter list in the source runtime settings, then re-import before promotion.",
			metadataKey: "java_vm_parameters_raw",
			rawValue:    runtimeCfg.JavaVMParametersRaw,
		})
	}
	if runtimeCfg.JavaVMClasspathParseError != "" {
		issues = append(issues, reviewIssue{
			blocker:     javaVMClasspathReviewBlocker,
			evidence:    "Corrected and re-imported Java VM classpath quoting",
			nextAction:  "Correct the quoted Java VM classpath in the source runtime settings, then re-import before promotion.",
			metadataKey: "java_vm_classpath_raw",
			rawValue:    runtimeCfg.JavaVMClasspathRaw,
		})
	}
	for _, issue := range []reviewIssue{
		{
			blocker:     javaVMOptionsReviewBlocker,
			evidence:    "Corrected and re-imported Java VM JDK selector",
			nextAction:  "Correct the Java VM JDK selector in the source runtime settings, then re-import before promotion.",
			metadataKey: "java_vm_jdk_selection_raw",
			rawValue:    runtimeCfg.JavaVMJDKSelectionRaw,
		},
		{
			blocker:     javaVMOptionsReviewBlocker,
			evidence:    "Corrected and re-imported Java VM Xboot setting",
			nextAction:  "Correct the Java VM Xboot boolean in the source runtime settings, then re-import before promotion.",
			metadataKey: "java_vm_use_xbootclasspath_raw",
			rawValue:    runtimeCfg.JavaVMUseXBootClasspathRaw,
		},
		{
			blocker:     javaVMOptionsReviewBlocker,
			evidence:    "Corrected and re-imported Java VM class-loader setting",
			nextAction:  "Correct the Java VM dedicated-class-loader boolean in the source runtime settings, then re-import before promotion.",
			metadataKey: "java_vm_dedicated_class_loader_raw",
			rawValue:    runtimeCfg.JavaVMDedicatedClassLoaderRaw,
		},
		{
			blocker:     javaVMOptionsReviewBlocker,
			evidence:    "Corrected and re-imported Java VM skip-parameter-evaluation setting",
			nextAction:  "Correct the Java VM skip-parameter-evaluation boolean in the source runtime settings, then re-import before promotion.",
			metadataKey: "java_vm_skip_parameter_evaluation_raw",
			rawValue:    runtimeCfg.JavaVMSkipParameterEvaluationRaw,
		},
	} {
		var parseError string
		switch issue.metadataKey {
		case "java_vm_jdk_selection_raw":
			parseError = runtimeCfg.JavaVMJDKSelectionParseError
		case "java_vm_use_xbootclasspath_raw":
			parseError = runtimeCfg.JavaVMUseXBootParseError
		case "java_vm_dedicated_class_loader_raw":
			parseError = runtimeCfg.JavaVMDedicatedParseError
		case "java_vm_skip_parameter_evaluation_raw":
			parseError = runtimeCfg.JavaVMSkipParamEvalParseError
		}
		if parseError != "" {
			issues = append(issues, issue)
		}
	}
	if len(issues) == 0 {
		return
	}

	if result.ReviewArtifact == nil {
		detected := DetectedProtocol{
			CanonicalSourceProtocol: "java_vuser",
			APEXLoadProtocol:        "java_vuser",
			ImportMode:              ImportModeReviewOnly,
			Confidence:              DetectionConfidenceHigh,
			DisplayName:             "Java Vuser",
		}
		if result.DetectedProtocol != nil {
			detected = *result.DetectedProtocol
		}
		result.ReviewArtifact = NewReviewArtifact(detected, files, nil, nil)
	}
	if result.ReviewArtifact.Metadata == nil {
		result.ReviewArtifact.Metadata = make(map[string]any)
	}
	blockers := javaReviewArtifactBlockers(result, javaSourceIntegrityKey)
	for _, issue := range issues {
		result.ReviewArtifact.Metadata[issue.metadataKey] = issue.rawValue
		blockers = appendUnique(blockers, issue.blocker)
		if result.DetectedProtocol != nil {
			result.DetectedProtocol.RequiredEvidence = appendUnique(result.DetectedProtocol.RequiredEvidence, issue.evidence)
		}
		result.ReviewArtifact.RequiredEvidence = appendUnique(result.ReviewArtifact.RequiredEvidence, issue.evidence)
		result.ReviewArtifact.Protocol.RequiredEvidence = appendUnique(result.ReviewArtifact.Protocol.RequiredEvidence, issue.evidence)
		result.ReviewArtifact.NextActions = appendUnique(result.ReviewArtifact.NextActions, issue.nextAction)
		result.Warnings = append(result.Warnings, issue.blocker)
	}
	result.ReviewArtifact.Metadata[javaSourceIntegrityKey] = blockers
	result.Warnings = uniqueSortedStrings(result.Warnings)
}

type archiveFinalizationContext struct {
	Files                map[string]string
	ProjectFileName      string
	ProjectSections      map[string]map[string]string
	ProjectConfig        *ProjectConfig
	RuntimeFileName      string
	RuntimeConfig        *RuntimeConfig
	ParameterFileNames   []string
	TrueClientInspection *trueclient.ArchiveInspection
	ParsedActions        []*ParsedAction
}

func finalizeEnterpriseArchiveImportResult(result *ConvertResult, ctx archiveFinalizationContext) {
	if result == nil {
		return
	}
	applyBrowserEmulationRuntimeSettingsReview(result, ctx.RuntimeConfig)
	parameterSections := make(map[string]map[string]map[string]string)
	scripts := result.effectiveScripts()
	if len(scripts) > 0 {
		for _, parameterFileName := range ctx.ParameterFileNames {
			content, ok := findFile(ctx.Files, parameterFileName)
			if !ok {
				continue
			}
			parsed := ParseParameterFile(parameterFileName, content, ctx.Files)
			for _, script := range scripts {
				if script == nil {
					continue
				}
				if len(parsed.Parameters) > 0 {
					script.Parameters = append(script.Parameters, parsed.Parameters...)
				}
				if len(parsed.DataSources) > 0 {
					script.DataSources = append(script.DataSources, parsed.DataSources...)
				}
			}
			if len(parsed.Warnings) > 0 {
				result.Warnings = append(result.Warnings, parsed.Warnings...)
			}
			if len(parsed.Sections) > 0 {
				parameterSections[filepath.Base(parameterFileName)] = parsed.Sections
			}
		}
		for _, script := range scripts {
			if script == nil {
				continue
			}
			applyKnownParameterReferences(script)
			applyParameterizedBaseURL(script)
			removed := len(pruneUnusedWholeBodyExtractors(script))
			if removed > 0 {
				result.Warnings = append(result.Warnings, fmt.Sprintf("%d unused whole-response extractors were omitted to keep large responses within runtime extraction bounds", removed))
				if result.Stats.Extractors >= removed {
					result.Stats.Extractors -= removed
				} else {
					result.Stats.Extractors = 0
				}
			}
		}
		resolveTemplatedBaseURL(result)
	}
	result.SourceReview = BuildSourceReview(ctx.Files, sourceReviewMappings(result))
	for _, script := range scripts {
		redactArchiveMaskedBodyMetadata(script)
		attachSourceMetadata(script, result, ctx.ProjectFileName, ctx.ProjectSections, ctx.ProjectConfig, ctx.Files, ctx.RuntimeFileName, ctx.RuntimeConfig, parameterSections, ctx.ParameterFileNames, ctx.TrueClientInspection)
	}
	attachImportFidelityReport(result, ctx.Files, ctx.ProjectConfig, ctx.ParsedActions)
}

func redactArchiveMaskedBodyMetadata(script *dsl.Script) {
	if script == nil {
		return
	}
	var redactActions func([]dsl.Action)
	redactActions = func(actions []dsl.Action) {
		for i := range actions {
			if len(actions[i].Metadata) > 0 {
				delete(actions[i].Metadata, "masked_body")
				delete(actions[i].Metadata, "masked_body_preview")
				delete(actions[i].Metadata, "masked_body_secret")
				delete(actions[i].Metadata, "masked_body_secrets")
				delete(actions[i].Metadata, "masked_body_reference")
				if len(actions[i].Metadata) == 0 {
					actions[i].Metadata = nil
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
		// Once a parsed project explicitly identifies a non-Java type,
		// language, or runtime, its declared entry point is authoritative.
		// Auxiliary dependencies named Actions.java must not redirect a DevWeb
		// or C project away from its selected converter. Java project types and
		// source-action declarations are handled by isJavaProtocolsProject.
		if strings.TrimSpace(projectConfig.ScriptType) != "" ||
			strings.TrimSpace(projectConfig.ScriptLanguage) != "" ||
			strings.TrimSpace(projectConfig.RunType) != "" {
			return false
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

// preservedJavaVMSettings normalizes the parsed [JavaVM:Options] runtime
// settings into a dedicated preserved-metadata object so the dashboard can
// surface the imported source values (e.g. the VM-parameter picker) without
// re-parsing raw cfg text from preserved["runtime_files"].
func preservedJavaVMSettings(runtimeCfg *RuntimeConfig) map[string]any {
	if runtimeCfg == nil {
		return nil
	}
	settings := make(map[string]any)
	if len(runtimeCfg.JavaVMAdditionalParameters) > 0 {
		settings["additional_vm_parameters"] = append([]string(nil), runtimeCfg.JavaVMAdditionalParameters...)
	}
	if runtimeCfg.JavaVMParametersRaw != "" {
		settings["additional_vm_parameters_raw"] = runtimeCfg.JavaVMParametersRaw
	}
	if len(runtimeCfg.JavaVMClasspath) > 0 {
		settings["classpath"] = append([]string(nil), runtimeCfg.JavaVMClasspath...)
	}
	if runtimeCfg.JavaVMClasspathRaw != "" {
		settings["classpath_raw"] = runtimeCfg.JavaVMClasspathRaw
	}
	if runtimeCfg.JavaVMJDKSelectionRaw != "" {
		settings["jdk_selection_raw"] = runtimeCfg.JavaVMJDKSelectionRaw
	}
	if runtimeCfg.JavaVMSkipParameterEvaluationRaw != "" {
		settings["skip_parameter_evaluation_raw"] = runtimeCfg.JavaVMSkipParameterEvaluationRaw
	}
	if len(settings) == 0 {
		return nil
	}
	return settings
}

// preservedBrowserEmulationSettings captures the raw browser-emulation source
// values (including unmapped browser names and the never-applied
// ClearCacheForSimulateNewUser child key) for round-trip review.
func preservedBrowserEmulationSettings(runtimeCfg *RuntimeConfig) map[string]any {
	if runtimeCfg == nil {
		return nil
	}
	settings := make(map[string]any)
	putRaw := func(key, value string) {
		if value != "" {
			settings[key] = value
		}
	}
	putRaw("browser_type_raw", runtimeCfg.BEBrowserTypeRaw)
	putRaw("browser_version", runtimeCfg.BEBrowserVersion)
	putRaw("platform_raw", runtimeCfg.BEPlatformRaw)
	putRaw("language", runtimeCfg.BELanguage)
	putRaw("use_custom_user_agent_raw", runtimeCfg.BEUseCustomUARaw)
	putRaw("simulate_cache_raw", runtimeCfg.BESimulateCacheRaw)
	putRaw("cache_content_urls_raw", runtimeCfg.BECacheContentURLsRaw)
	putRaw("check_newer_versions_raw", runtimeCfg.BECheckNewerVersionsRaw)
	putRaw("new_user_each_iteration_raw", runtimeCfg.BENewUserEachIterationRaw)
	putRaw("download_non_html_resources_raw", runtimeCfg.BEDownloadNonHTMLRaw)
	putRaw("simulate_prefetch_raw", runtimeCfg.BESimulatePrefetchRaw)
	putRaw("clear_cache_each_iteration_raw", runtimeCfg.BEClearCacheEachIterationRaw)
	if runtimeCfg.BEBrowserTypeRaw != "" && runtimeCfg.BEBrowserType == "" {
		settings["browser_type_unmapped"] = true
	}
	if len(settings) == 0 {
		return nil
	}
	return settings
}

// applyBrowserEmulationRuntimeSettingsReview surfaces malformed
// browser-emulation values as import warnings. Unlike Java VM blockers these
// do not gate promotion: an unparseable boolean simply leaves the setting
// unset, with the raw value preserved in metadata for review.
func applyBrowserEmulationRuntimeSettingsReview(result *ConvertResult, runtimeCfg *RuntimeConfig) {
	if result == nil || runtimeCfg == nil {
		return
	}
	warn := func(parseError, raw string) {
		if parseError == "" {
			return
		}
		result.Warnings = append(result.Warnings, fmt.Sprintf("Browser Emulation import: %s (source value %q); the setting was left unset and preserved for review.", parseError, raw))
	}
	warn(runtimeCfg.BEUseCustomUAParseError, runtimeCfg.BEUseCustomUARaw)
	warn(runtimeCfg.BESimulateCacheParseError, runtimeCfg.BESimulateCacheRaw)
	warn(runtimeCfg.BECacheContentURLsParseError, runtimeCfg.BECacheContentURLsRaw)
	warn(runtimeCfg.BECheckNewerParseError, runtimeCfg.BECheckNewerVersionsRaw)
	warn(runtimeCfg.BENewUserParseError, runtimeCfg.BENewUserEachIterationRaw)
	warn(runtimeCfg.BEDownloadNonHTMLParseError, runtimeCfg.BEDownloadNonHTMLRaw)
	warn(runtimeCfg.BESimulatePrefetchParseError, runtimeCfg.BESimulatePrefetchRaw)
	if runtimeCfg.BEBrowserTypeRaw != "" && runtimeCfg.BEBrowserType == "" {
		result.Warnings = append(result.Warnings, fmt.Sprintf("Browser Emulation import: browser type %q is not in the APEX browser catalog; no specific-browser emulation was configured and the raw value was preserved for review.", runtimeCfg.BEBrowserTypeRaw))
	}
	result.Warnings = uniqueSortedStrings(result.Warnings)
}

func attachSourceMetadata(script *dsl.Script, result *ConvertResult, projectFileName string, projectSections map[string]map[string]string, projectConfig *ProjectConfig, files map[string]string, runtimeFileName string, runtimeCfg *RuntimeConfig, parameterSections map[string]map[string]map[string]string, parameterFileNames []string, trueClientInspection *trueclient.ArchiveInspection) {
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
		if parity := buildRTSParityReportForProtocol(runtimeSections, script.Protocol); len(parity) > 0 {
			preserved["runtime_settings_parity"] = parity
			preserved["runtime_settings_status_counts"] = RuntimeSettingStatusCounts(parity)
		}
	}
	if runtimeFileName != "" {
		preserved["mapped_runtime_file"] = filepath.Base(runtimeFileName)
	}
	// Scope Java VM source metadata to Java Vuser scripts. A mixed/multi-lane
	// archive can share one runtime cfg, so an http/jdbc lane must not carry a
	// java_vm_settings picker source, matching the protocol-scoped RTS parity
	// block below and appendJavaVuserConfig on the engine side.
	if strings.EqualFold(strings.TrimSpace(script.Protocol), "java_vuser") {
		if javaVM := preservedJavaVMSettings(runtimeCfg); javaVM != nil {
			preserved["java_vm_settings"] = javaVM
		}
	}
	if browserEmulation := preservedBrowserEmulationSettings(runtimeCfg); browserEmulation != nil {
		preserved["browser_emulation_settings"] = browserEmulation
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
