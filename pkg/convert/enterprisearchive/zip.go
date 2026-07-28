package enterprisearchive

import (
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/devweb"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/convert/trueclient"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
	"github.com/jasonczech/apexload/pkg/internal/archiveingest"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
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

	extracted, err := archiveingest.ReadZIP(zipData, archiveingest.Options{
		Limits: archiveingest.Limits{
			MaxEntries:    maxEnterpriseArchiveEntries,
			MaxEntryBytes: maxEnterpriseArchiveEntryBytes,
			MaxTotalBytes: maxEnterpriseArchiveTotalBytes,
			MaxDepth:      32,
		},
		StripFirstComponent: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to open zip archive (size=%d bytes): %w; the file may be corrupted, incomplete, or use an unsupported ZIP compression method", len(zipData), err)
	}

	// Read all files from the zip
	files := make(map[string]string, len(extracted))
	normalizedFileNames := make(map[string]string, len(extracted))
	for _, file := range extracted {
		files[file.Name] = string(file.Data)
		normalizedFileNames[strings.ToLower(file.Name)] = file.Name
	}

	// Java sources that arrived as .txt are renamed before any pass reads the
	// map, so protocol detection, parsing, source review, and the immutable
	// source manifest all resolve one path per source file.
	if err := normalizeJavaSourceArchiveNames(files, normalizedFileNames); err != nil {
		return nil, err
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

	runtimeFileName, runtimeCfg := resolveRuntimeConfig(files, projectConfig)
	if runtimeCfg != nil {
		callerHTTPConfig := opts.HTTPConfig != nil
		if opts.HTTPConfig == nil {
			opts.HTTPConfig = runtimeCfg.ToHTTPConfig()
		}
		if opts.RuntimeSettings == nil {
			opts.RuntimeSettings = runtimeCfg.ToScriptConfig()
			// HTTPConfig is the caller's explicit transport override. Keep the
			// remaining source runtime settings, but do not let the broader
			// source-generated ScriptConfig silently replace that override.
			// The shared fidelity pass below will attach a durable gate when an
			// explicitly authored source HTTP version conflicts with it.
			if callerHTTPConfig && opts.RuntimeSettings != nil {
				opts.RuntimeSettings.HTTP = dsl.HTTPConfig{}
			}
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
		resolvedActionFile, content, ambiguous := findSourceArchiveFile(files, actionFile)
		phase := sourceActionPhaseForProjectFile(projectConfig, actionFile)
		if resolvedActionFile == "" || len(ambiguous) > 0 {
			kind := "missing_source_file"
			if len(ambiguous) > 0 {
				kind = "ambiguous_source_file"
			}
			parsedActions = append(parsedActions, &ParsedAction{
				Filename: actionFile,
				Phase:    phase,
				ParseFailures: []CSourceParseFailure{{
					Filename: actionFile,
					Phase:    phase,
					Kind:     kind,
				}},
			})
			parseErrors = appendUnique(parseErrors, actionFile)
			continue
		}
		parsed, err := ParseActionFile(resolvedActionFile, inlineSourceStringConstants(content, sourceConstants))
		if parsed != nil {
			parsed.Phase = phase
			for index := range parsed.Functions {
				parsed.Functions[index].Phase = parsed.Phase
			}
			for index := range parsed.ParseFailures {
				if parsed.ParseFailures[index].Phase == "" {
					parsed.ParseFailures[index].Phase = parsed.Phase
				}
			}
			if err == nil || len(parsed.Functions) > 0 || len(parsed.ParseFailures) > 0 {
				parsedActions = append(parsedActions, parsed)
			}
		}
		if err != nil {
			// Parser diagnostics can echo arbitrary source tokens. Persist
			// only the affected filename; detailed durable gates use
			// value-free failure kinds and line numbers.
			parseErrors = appendUnique(parseErrors, actionFile)
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
			ParsedActions:      parsedActions,
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
		opts.deferArchiveFinalization = true
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
	appendCSourceParseFailureGates(result, ctx.ParsedActions)
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
	applyEnterpriseRuntimeBindings(result, scripts, ctx)
	applyJavaVMParameterRejectionReview(result, scripts, ctx.RuntimeConfig, ctx.RuntimeFileName)
	applyEnterpriseRuntimeFidelityReview(result, scripts, ctx)
	result.SourceReview = BuildSourceReview(ctx.Files, sourceReviewMappings(result))
	finalizeEnterpriseSourceDisclosure(result, ctx.Files, ctx.ParameterFileNames)
	for _, script := range scripts {
		redactArchiveMaskedBodyMetadata(script)
		attachSourceMetadata(script, result, ctx.ProjectFileName, ctx.ProjectSections, ctx.ProjectConfig, ctx.Files, ctx.RuntimeFileName, ctx.RuntimeConfig, parameterSections, ctx.ParameterFileNames, ctx.TrueClientInspection)
	}
	attachImportFidelityReport(result, ctx.Files, ctx.ProjectConfig, ctx.ParsedActions)
}

const javaVMParameterExecutionBlocker = "java_vm_parameters_require_safe_replacement"

// applyJavaVMParameterRejectionReview turns an import-time JVM argv rejection
// into the durable action gate enforced by engine, replay, and launch paths.
// Supplying a project secret cannot clear this gate: the engine deliberately
// never resolves secrets into process-observable Java command-line arguments.
func applyJavaVMParameterRejectionReview(
	result *ConvertResult,
	scripts []*dsl.Script,
	runtimeCfg *RuntimeConfig,
	runtimeFileName string,
) {
	if result == nil {
		return
	}
	runtimeFile := normalizeRuntimeArchivePath(runtimeFileName)
	if runtimeFile == "" && runtimeCfg != nil && len(runtimeCfg.RuntimeFiles) > 0 {
		runtimeFile = normalizeRuntimeArchivePath(runtimeCfg.RuntimeFiles[0])
	}
	rejections := make([]javaVMParameterRejection, 0)
	if runtimeCfg != nil {
		rejections = append(rejections, runtimeCfg.javaVMParameterRejections...)
	}
	rejectedAny := false
	for _, script := range scripts {
		if script == nil || !strings.EqualFold(strings.TrimSpace(script.Protocol), "java_vuser") {
			continue
		}
		scriptRejections := append([]javaVMParameterRejection(nil), rejections...)
		if validationIssues := validateImportedJavaVMParameters(
			script.Config.JavaVuser.AdditionalVMParameters,
		); len(validationIssues) > 0 {
			script.Config.JavaVuser.AdditionalVMParameters = nil
			for _, issue := range validationIssues {
				scriptRejections = append(scriptRejections, javaVMParameterRejection{
					sourceKey:  "config.java_vuser.additional_vm_parameters",
					tokenIndex: issue.Index,
					category:   issue.Category,
				})
			}
		}
		if len(scriptRejections) == 0 {
			continue
		}
		rejectedAny = true
		for _, rejection := range scriptRejections {
			pointer := "JavaVM:Options/" + rejection.sourceKey
			if rejection.sourceLine > 0 {
				pointer += fmt.Sprintf(":line-%d", rejection.sourceLine)
			}
			sourceRef := pointer
			if runtimeFile != "" {
				sourceRef = runtimeFile + "#" + pointer
			}
			finding := importreview.Finding{
				Code:       javaVMParameterExecutionBlocker,
				Severity:   diag.Blocker,
				Element:    "Java VM parameter",
				Detail:     fmt.Sprintf("source JVM argument token %d was omitted (%s); process-observable JVM argv has no secure secret-injection equivalent and unsafe process options cannot be imported", rejection.tokenIndex, rejection.category),
				Location:   pointer,
				Source:     "enterprise_script_archive",
				ReasonCode: "java_vm_parameter_omitted",
				SourceRef:  sourceRef,
				Resolution: "replace_behavior_and_remove_gate",
				Gate:       importreview.GateExecution,
			}
			_ = dslwalk.WalkActions(script, func(ref dslwalk.ActionRef) error {
				actionFinding := finding
				actionFinding.ActionPath = ref.Path
				importreview.ApplyToAction(ref.Action, []importreview.Finding{actionFinding})
				return nil
			})
		}
	}
	if !rejectedAny {
		return
	}
	result.Warnings = append(
		result.Warnings,
		"One or more Java VM parameters were omitted by import policy; Java Vuser execution remains blocked until the behavior is safely replaced and reviewed.",
	)
	result.Warnings = uniqueSortedStrings(result.Warnings)
}

func applyEnterpriseRuntimeFidelityReview(
	result *ConvertResult,
	scripts []*dsl.Script,
	ctx archiveFinalizationContext,
) {
	if result == nil || len(scripts) == 0 {
		return
	}
	runtimeFile := normalizeRuntimeArchivePath(ctx.RuntimeFileName)
	runtimePointer := "HTTPVersion"
	if runtimeFile != "" {
		runtimePointer = runtimeFile + "#HTTPVersion"
	}
	source := requestimport.SourceRef{
		Format:  "enterprise_script_archive",
		Path:    runtimeFile,
		Pointer: runtimePointer,
	}
	declaredVersion, declaredVersionSource := enterpriseDeclaredRuntimeHTTPVersion(ctx, source)
	reasons := make(map[string]struct{})
	warningsChanged := false
	for _, script := range scripts {
		if script == nil {
			continue
		}
		switch dsl.ProtocolFamily(script.Protocol) {
		case "http", "soap", "java_http":
		default:
			continue
		}
		// ToHTTPConfig deliberately projects an unsupported source version to
		// automatic negotiation. Restore it only for the shared review pass so
		// that pass can clear it again while attaching a durable gate to every
		// potentially affected request.
		if ctx.RuntimeConfig != nil &&
			strings.TrimSpace(ctx.RuntimeConfig.HTTPVersion) != "" &&
			!httpprotocol.VersionExecutable(ctx.RuntimeConfig.HTTPVersion) {
			script.Config.HTTP.Version = ctx.RuntimeConfig.HTTPVersion
		}
		findings, mappings, err := requestimport.ApplyRuntimeFidelityReview(
			script,
			requestimport.RuntimeFidelityOptions{
				SourceProfile:             "enterprise_script_archive",
				Source:                    source,
				DeclaredHTTPVersion:       conflictingEnterpriseHTTPVersion(script, declaredVersion),
				DeclaredHTTPVersionSource: declaredVersionSource,
			},
		)
		if err != nil {
			result.Warnings = append(
				result.Warnings,
				"HTTP runtime fidelity review could not traverse the generated draft; execution remains unavailable until the script structure is repaired.",
			)
			warningsChanged = true
			continue
		}
		for _, finding := range findings {
			if finding.ReasonCode != "" {
				reasons[finding.ReasonCode] = struct{}{}
			}
		}
		for _, mapping := range mappings {
			sourceFile := normalizeRuntimeArchivePath(mapping.Source.Path)
			if sourceFile == "" {
				sourceFile = runtimeFile
			}
			appendSourceReviewMapping(result, SourceReviewMapping{
				SourceFile: sourceFile,
				Function:   "HTTP runtime fidelity",
				TargetPath: mapping.Target,
				TargetKind: "review",
				Status:     "review_only",
				Notes:      "The imported HTTP behavior was sanitized into a valid draft and remains blocked until the affected behavior is replaced and its review gate is removed.",
			})
		}
	}
	if len(reasons) > 0 {
		ordered := make([]string, 0, len(reasons))
		for reason := range reasons {
			ordered = append(ordered, reason)
		}
		sort.Strings(ordered)
		result.Warnings = append(
			result.Warnings,
			"HTTP import draft requires review before execution ("+strings.Join(ordered, ", ")+").",
		)
		warningsChanged = true
	}
	if warningsChanged {
		result.Warnings = uniqueSortedStrings(result.Warnings)
	}
}

// enterpriseDeclaredRuntimeHTTPVersion returns only an explicitly authored
// exact source version. ParseRuntimeCfg's compatibility default ("1.1") is not
// source evidence, and EnableHTTP2=true describes negotiated fallback semantics
// rather than an exact per-request version.
func enterpriseDeclaredRuntimeHTTPVersion(
	ctx archiveFinalizationContext,
	fallback requestimport.SourceRef,
) (string, requestimport.SourceRef) {
	if ctx.RuntimeConfig == nil || ctx.RuntimeFileName == "" ||
		(ctx.RuntimeConfig.EnableHTTP2 != nil && *ctx.RuntimeConfig.EnableHTTP2) {
		return "", requestimport.SourceRef{}
	}
	content, ok := findFile(ctx.Files, ctx.RuntimeFileName)
	if !ok {
		return "", requestimport.SourceRef{}
	}
	sections := ParseConfigSections(content)
	httpSection := findSection(
		sections,
		"HTTP",
		"Internet Protocol",
		"Preferences",
		"Internet Protocol Preferences",
		"WEB",
		"Web",
	)
	raw, authored := sectionValueCaseInsensitive(httpSection, "HTTPVersion", "HTTPVer", "HttpVer")
	raw = strings.TrimSpace(raw)
	if !authored || raw == "" || !httpprotocol.VersionExecutable(raw) {
		return "", requestimport.SourceRef{}
	}
	return raw, fallback
}

func conflictingEnterpriseHTTPVersion(script *dsl.Script, declared string) string {
	if script == nil || strings.TrimSpace(declared) == "" {
		return ""
	}
	sourceVersion := httpprotocol.NormalizeVersion(declared)
	configuredVersion := httpprotocol.NormalizeVersion(script.Config.HTTP.Version)
	if sourceVersion == configuredVersion {
		return ""
	}
	return declared
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
		resolved, _, _ := findSourceArchiveFile(files, name)
		if resolved == "" && !sourceActionPointerRequired(projectConfig, name) {
			continue
		}
		key := strings.ToLower(normalizeRuntimeArchivePath(name))
		if resolved != "" {
			key = strings.ToLower(normalizeRuntimeArchivePath(resolved))
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		ordered = append(ordered, name)
	}
	return ordered
}

// findSourceArchiveFile resolves lifecycle source pointers with the same
// fail-closed path semantics as runtime profiles. Qualified pointers are exact;
// basename fallback is allowed only for an unqualified pointer with one unique
// archive match.
func findSourceArchiveFile(files map[string]string, requested string) (string, string, []string) {
	return findRuntimeArchiveFile(files, requested)
}

func sourceActionPointerRequired(projectConfig *ProjectConfig, name string) bool {
	if projectConfig == nil || len(projectConfig.SourceActionFiles) == 0 {
		return strings.EqualFold(filepath.Base(strings.TrimSpace(name)), "Action.c")
	}
	normalized := normalizeRuntimeArchivePath(name)
	for _, sourceFile := range projectConfig.SourceActionFiles {
		if strings.EqualFold(normalizeRuntimeArchivePath(sourceFile.Name), normalized) {
			return true
		}
	}
	return false
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

func sourceActionPhaseForProjectFile(projectConfig *ProjectConfig, fileName string) string {
	if projectConfig != nil {
		normalized := normalizeRuntimeArchivePath(fileName)
		for _, sourceFile := range projectConfig.SourceActionFiles {
			if strings.EqualFold(normalizeRuntimeArchivePath(sourceFile.Name), normalized) {
				if phase := strings.TrimSpace(sourceFile.Phase); phase != "" {
					return normalizeSourcePhase(phase)
				}
			}
		}
		// Preserve the historical unqualified-basename fallback only when it is
		// unambiguous. Two declared paths with the same basename may belong to
		// different lifecycle phases and must never alias one another.
		if !strings.Contains(normalized, "/") {
			matchedPhase := ""
			matches := 0
			for _, sourceFile := range projectConfig.SourceActionFiles {
				if !strings.EqualFold(filepath.Base(sourceFile.Name), filepath.Base(fileName)) {
					continue
				}
				matches++
				matchedPhase = normalizeSourcePhase(sourceFile.Phase)
			}
			if matches == 1 && matchedPhase != "" {
				return matchedPhase
			}
		}
	}
	return sourceActionPhase(fileName)
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
	finalizeEnterpriseSourceDisclosure(
		result,
		files,
		findParameterFiles(files, nil),
	)
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

func normalizeRuntimeArchivePath(name string) string {
	normalized := strings.ReplaceAll(cleanRuntimePathValue(name), `\`, "/")
	normalized = strings.TrimSpace(normalized)
	if normalized == "" {
		return ""
	}
	return path.Clean(normalized)
}

func runtimeArchiveBase(name string) string {
	normalized := normalizeRuntimeArchivePath(name)
	if normalized == "" {
		return ""
	}
	return path.Base(normalized)
}

func findRuntimeArchiveFile(files map[string]string, requested string) (string, string, []string) {
	requested = normalizeRuntimeArchivePath(requested)
	if requested == "" {
		return "", "", nil
	}
	fileNames := make([]string, 0, len(files))
	for name := range files {
		fileNames = append(fileNames, name)
	}
	sort.Slice(fileNames, func(i, j int) bool {
		return strings.ToLower(normalizeRuntimeArchivePath(fileNames[i])) <
			strings.ToLower(normalizeRuntimeArchivePath(fileNames[j]))
	})

	exact := make([]string, 0, 1)
	for _, name := range fileNames {
		if strings.EqualFold(normalizeRuntimeArchivePath(name), requested) {
			exact = append(exact, name)
		}
	}
	if len(exact) == 1 {
		return normalizeRuntimeArchivePath(exact[0]), files[exact[0]], nil
	}
	if len(exact) > 1 {
		return "", "", normalizedRuntimeArchivePaths(exact)
	}

	// A directory-qualified runtime pointer identifies one archive-relative
	// profile. Do not silently alias profiles/a/default.cfg to
	// profiles/b/default.cfg merely because only the latter basename exists.
	// Basename fallback is reserved for the common unqualified pointers emitted
	// by .usr/.cfg files.
	if strings.Contains(requested, "/") {
		return "", "", nil
	}

	base := runtimeArchiveBase(requested)
	matches := make([]string, 0, 1)
	for _, name := range fileNames {
		if strings.EqualFold(runtimeArchiveBase(name), base) {
			matches = append(matches, name)
		}
	}
	if len(matches) == 1 {
		return normalizeRuntimeArchivePath(matches[0]), files[matches[0]], nil
	}
	if len(matches) > 1 {
		return "", "", normalizedRuntimeArchivePaths(matches)
	}
	return "", "", nil
}

func normalizedRuntimeArchivePaths(names []string) []string {
	normalized := make([]string, 0, len(names))
	for _, name := range names {
		if candidate := normalizeRuntimeArchivePath(name); candidate != "" {
			normalized = append(normalized, candidate)
		}
	}
	sort.Slice(normalized, func(i, j int) bool {
		return strings.ToLower(normalized[i]) < strings.ToLower(normalized[j])
	})
	return normalized
}

func configuredRuntimeCfgPointer(projectConfig *ProjectConfig) string {
	if projectConfig == nil {
		return ""
	}
	pointer := strings.TrimSpace(projectConfig.DefaultCfg)
	// RuntimeFile predates explicit profile parsing. Use it only when it is
	// unambiguous; ParseUSRFile may otherwise have populated it from the first
	// [CfgFiles] row rather than an active-profile declaration.
	if pointer == "" && len(projectConfig.CfgProfiles) <= 1 {
		pointer = strings.TrimSpace(projectConfig.RuntimeFile)
	}
	if pointer == "" {
		return ""
	}
	for profile, candidate := range projectConfig.CfgProfiles {
		if strings.EqualFold(strings.TrimSpace(profile), pointer) {
			return normalizeRuntimeArchivePath(candidate)
		}
	}
	return normalizeRuntimeArchivePath(pointer)
}

func configuredRuntimeCfgProfileFiles(projectConfig *ProjectConfig) []string {
	if projectConfig == nil {
		return nil
	}
	files := make([]string, 0, len(projectConfig.CfgProfiles))
	for _, candidate := range projectConfig.CfgProfiles {
		normalized := normalizeRuntimeArchivePath(candidate)
		if normalized == "" {
			continue
		}
		duplicate := false
		for _, existing := range files {
			if strings.EqualFold(existing, normalized) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			files = append(files, normalized)
		}
	}
	for _, conflict := range projectConfig.runtimePointerConflicts {
		if !strings.EqualFold(strings.TrimSpace(conflict.section), "CfgFiles") &&
			!(strings.EqualFold(strings.TrimSpace(conflict.section), "General") &&
				(strings.EqualFold(strings.TrimSpace(conflict.key), "DefaultCfg") ||
					strings.EqualFold(strings.TrimSpace(conflict.key), "RuntimeSettings") ||
					strings.EqualFold(strings.TrimSpace(conflict.key), "RuntimeFile"))) {
			continue
		}
		for _, occurrence := range conflict.occurrences {
			normalized := normalizeRuntimeArchivePath(occurrence.value)
			if normalized == "" {
				continue
			}
			duplicate := false
			for _, existing := range files {
				if strings.EqualFold(existing, normalized) {
					duplicate = true
					break
				}
			}
			if !duplicate {
				files = append(files, normalized)
			}
		}
	}
	sort.Slice(files, func(i, j int) bool {
		return strings.ToLower(files[i]) < strings.ToLower(files[j])
	})
	return files
}

func findRuntimeConfig(files map[string]string, projectConfig *ProjectConfig) (string, string, string) {
	if conflict, ok := projectRuntimePointerConflict(projectConfig, "General", "DefaultCfg", "RuntimeSettings", "RuntimeFile"); ok {
		return "", "", fmt.Sprintf(
			"Conflicting case-insensitive .usr runtime cfg pointers (%s); using APEX defaults and requiring review.",
			runtimeConfigConflictEvidence(conflict),
		)
	}
	if conflict, ok := projectRuntimePointerConflict(projectConfig, "CfgFiles"); ok {
		return "", "", fmt.Sprintf(
			"Conflicting case-insensitive .usr cfg profile declarations (%s); using APEX defaults and requiring review.",
			runtimeConfigConflictEvidence(conflict),
		)
	}
	if pointer := configuredRuntimeCfgPointer(projectConfig); pointer != "" {
		if name, content, ambiguous := findRuntimeArchiveFile(files, pointer); name != "" && strings.HasSuffix(strings.ToLower(name), ".cfg") {
			return name, content, ""
		} else if len(ambiguous) > 0 {
			return "", "", fmt.Sprintf("Active runtime cfg %q matched multiple archive files (%s); using APEX defaults and requiring review.", pointer, strings.Join(ambiguous, ", "))
		}
		// An explicit active pointer is authoritative. Applying default.cfg or the
		// sole unrelated cfg after it fails resolution can silently import a
		// different profile's proxy, think-time, and iteration behavior.
		return "", "", fmt.Sprintf("Active runtime cfg %q was not found; using APEX defaults and requiring review.", pointer)
	}
	if projectConfig != nil &&
		strings.TrimSpace(projectConfig.DefaultCfg) == "" &&
		len(projectConfig.CfgProfiles) > 1 {
		candidates := configuredRuntimeCfgProfileFiles(projectConfig)
		detail := ""
		if len(candidates) > 0 {
			detail = fmt.Sprintf(" (%s)", strings.Join(candidates, ", "))
		}
		return "", "", fmt.Sprintf("Multiple runtime cfg profiles%s are declared but [General] DefaultCfg is missing; using APEX defaults and requiring review.", detail)
	}

	cfgNames := make([]string, 0)
	defaultNames := make([]string, 0)
	for name := range files {
		normalized := normalizeRuntimeArchivePath(name)
		if !strings.HasSuffix(strings.ToLower(normalized), ".cfg") {
			continue
		}
		cfgNames = append(cfgNames, normalized)
		if strings.EqualFold(runtimeArchiveBase(normalized), "default.cfg") {
			defaultNames = append(defaultNames, normalized)
		}
	}
	sort.Slice(cfgNames, func(i, j int) bool { return strings.ToLower(cfgNames[i]) < strings.ToLower(cfgNames[j]) })
	sort.Slice(defaultNames, func(i, j int) bool { return strings.ToLower(defaultNames[i]) < strings.ToLower(defaultNames[j]) })
	switch len(defaultNames) {
	case 1:
		name, content, _ := findRuntimeArchiveFile(files, defaultNames[0])
		return name, content, ""
	case 0:
		// Fall through to the sole-cfg rule below.
	default:
		return "", "", fmt.Sprintf("Multiple default runtime cfg files (%s) are present without an unambiguous active profile; using APEX defaults and requiring review.", strings.Join(defaultNames, ", "))
	}

	switch len(cfgNames) {
	case 0:
		return "", "", ""
	case 1:
		name, content, _ := findRuntimeArchiveFile(files, cfgNames[0])
		return name, content, ""
	default:
		return "", "", fmt.Sprintf("Multiple runtime cfg files (%s) are present without an active profile; using APEX defaults and requiring review.", strings.Join(cfgNames, ", "))
	}
}

func projectRuntimePointerConflict(projectConfig *ProjectConfig, section string, keys ...string) (runtimeConfigCaseFoldConflict, bool) {
	if projectConfig == nil {
		return runtimeConfigCaseFoldConflict{}, false
	}
	for _, conflict := range projectConfig.runtimePointerConflicts {
		if !strings.EqualFold(strings.TrimSpace(conflict.section), strings.TrimSpace(section)) {
			continue
		}
		if len(keys) == 0 {
			return conflict, true
		}
		for _, key := range keys {
			if strings.EqualFold(strings.TrimSpace(conflict.key), strings.TrimSpace(key)) {
				return conflict, true
			}
		}
	}
	return runtimeConfigCaseFoldConflict{}, false
}

func configuredRunLogicConflictFiles(projectConfig *ProjectConfig) []string {
	if projectConfig == nil {
		return nil
	}
	var files []string
	for _, conflict := range projectConfig.runtimePointerConflicts {
		if !strings.EqualFold(strings.TrimSpace(conflict.section), "RunLogicFiles") {
			continue
		}
		for _, occurrence := range conflict.occurrences {
			normalized := normalizeRuntimeArchivePath(occurrence.value)
			if normalized == "" {
				continue
			}
			duplicate := false
			for _, existing := range files {
				if strings.EqualFold(existing, normalized) {
					duplicate = true
					break
				}
			}
			if !duplicate {
				files = append(files, normalized)
			}
		}
	}
	sort.Slice(files, func(i, j int) bool {
		return strings.ToLower(files[i]) < strings.ToLower(files[j])
	})
	return files
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
	finalizeEnterpriseSourceDisclosure(
		result,
		files,
		findParameterFiles(files, projectConfig),
	)
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
		lower := strings.ToLower(name)
		if strings.HasSuffix(lower, ".cfg") || strings.HasSuffix(lower, ".usp") {
			fileNames = append(fileNames, name)
		}
	}
	sort.Strings(fileNames)
	for _, name := range fileNames {
		sections[normalizeRuntimeArchivePath(name)] = ParseConfigSections(files[name])
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
		parameters := executableJavaVMParameters(runtimeCfg.JavaVMAdditionalParameters)
		if len(parameters) > 0 {
			settings["additional_vm_parameters"] = parameters
		}
	}
	if runtimeCfg.JavaVMParametersRaw != "" {
		settings["additional_vm_parameters_raw"] = disclosureSafeJavaVMParameterRow(runtimeCfg.JavaVMParametersRaw)
	}
	// LoadRunner spills a long parameter line across numbered companion keys, so
	// the single raw value above cannot round-trip the source. Preserve every
	// row in command-line order alongside it.
	if len(runtimeCfg.JavaVMParameterRows) > 1 {
		rows := make([]string, len(runtimeCfg.JavaVMParameterRows))
		for index, row := range runtimeCfg.JavaVMParameterRows {
			rows[index] = disclosureSafeJavaVMParameterRow(row)
		}
		settings["additional_vm_parameter_rows"] = rows
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
		preserved["project_settings"] = disclosureSafeConfigSections(projectSections)
	}
	if projectConfig != nil && projectConfig.RuntimeFile != "" {
		preserved["declared_runtime_file"] = normalizeRuntimeArchivePath(projectConfig.RuntimeFile)
	}
	runtimeSections := collectRuntimeSections(files)
	if len(runtimeSections) > 0 {
		preserved["runtime_files"] = disclosureSafeRuntimeSections(runtimeSections)
	}
	selectedSections := selectedRuntimeSections(runtimeSections, runtimeFileName, runtimeCfg)
	if parity := applyRuntimeMigrationIssues(buildRTSParityReportForProtocol(selectedSections, script.Protocol), runtimeCfg); len(parity) > 0 {
		preserved["runtime_settings_parity"] = parity
		preserved["runtime_settings_status_counts"] = RuntimeSettingStatusCounts(parity)
	}
	if runtimeFileName != "" {
		preserved["mapped_runtime_file"] = normalizeRuntimeArchivePath(runtimeFileName)
	}
	if runtimeCfg != nil && len(runtimeCfg.RuntimeFiles) > 0 {
		preserved["mapped_runtime_files"] = append([]string(nil), runtimeCfg.RuntimeFiles...)
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
		preserved["parameter_file_sections"] = disclosureSafeParameterSections(parameterSections)
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

func selectedRuntimeSections(all map[string]map[string]map[string]string, runtimeFileName string, runtimeCfg *RuntimeConfig) map[string]map[string]map[string]string {
	var wantedFiles []string
	if runtimeCfg != nil {
		wantedFiles = runtimeCfg.RuntimeFiles
	} else if strings.TrimSpace(runtimeFileName) != "" {
		wantedFiles = []string{runtimeFileName}
	}
	if len(wantedFiles) == 0 {
		return nil
	}
	selected := make(map[string]map[string]map[string]string, len(wantedFiles))
	for _, wanted := range wantedFiles {
		normalizedWanted := normalizeRuntimeArchivePath(wanted)
		if sections, ok := runtimeSectionsByExactPath(all, normalizedWanted); ok {
			selected[normalizedWanted] = sections
			continue
		}
		// Preserve the same pointer semantics used by runtime-profile selection:
		// a directory-qualified pointer identifies one archive-relative file and
		// must not pick up an unrelated same-basename profile for parity metadata.
		if strings.Contains(normalizedWanted, "/") {
			continue
		}
		var matchingNames []string
		for name := range all {
			if strings.EqualFold(runtimeArchiveBase(name), runtimeArchiveBase(normalizedWanted)) {
				matchingNames = append(matchingNames, name)
			}
		}
		if len(matchingNames) == 1 {
			selected[matchingNames[0]] = all[matchingNames[0]]
		}
	}
	return selected
}

func runtimeSectionsByExactPath(all map[string]map[string]map[string]string, wanted string) (map[string]map[string]string, bool) {
	for name, sections := range all {
		if strings.EqualFold(normalizeRuntimeArchivePath(name), wanted) {
			return sections, true
		}
	}
	return nil, false
}
