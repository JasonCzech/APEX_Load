package enterprisearchive

import (
	"encoding/base64"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/devweb"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/convert/trueclient"
	"github.com/jasonczech/apexload/pkg/convert/uniquify"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
	"github.com/jasonczech/apexload/pkg/internal/archiveingest"
	"github.com/jasonczech/apexload/pkg/javarunner"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
	"github.com/jasonczech/apexload/pkg/scrub"
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

// readEnterpriseArchive opens the uploaded archive and returns its files keyed
// by path.
//
// Java sources that arrived as .txt are renamed here, before any pass reads the
// map, so protocol detection, parsing, source review, and the immutable source
// manifest all resolve one path per source file. The lower-cased index that
// rename needs is built and discarded here; nothing downstream reads it.
func readEnterpriseArchive(zipData []byte) (map[string]string, error) {
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

	files := make(map[string]string, len(extracted))
	normalizedFileNames := make(map[string]string, len(extracted))
	for _, file := range extracted {
		files[file.Name] = string(file.Data)
		normalizedFileNames[strings.ToLower(file.Name)] = file.Name
	}
	if err := normalizeJavaSourceArchiveNames(files, normalizedFileNames); err != nil {
		return nil, err
	}
	return files, nil
}

// ConvertZip processes a ZIP archive containing enterprise C/VUser source files
// and returns either converted APEX Load DSL or a review artifact.
func ConvertZip(zipData []byte, opts ConvertOptions) (*ConvertResult, error) {
	files, err := readEnterpriseArchive(zipData)
	if err != nil {
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
				path.Base(projectFileName),
				path.Ext(projectFileName),
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
		if err := finalizeEnterpriseArchiveImportResult(result, archiveFinalizationContext{
			Files:                files,
			ProjectFileName:      projectFileName,
			ProjectSections:      projectSections,
			ProjectConfig:        projectConfig,
			RuntimeFileName:      runtimeFileName,
			RuntimeConfig:        runtimeCfg,
			ParameterFileNames:   parameterFileNames,
			TrueClientInspection: trueClientInspection,
		}); err != nil {
			return nil, err
		}
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
		if err := finalizeEnterpriseArchiveImportResult(result, archiveFinalizationContext{
			Files:              files,
			ProjectFileName:    projectFileName,
			ProjectSections:    projectSections,
			ProjectConfig:      projectConfig,
			RuntimeFileName:    runtimeFileName,
			RuntimeConfig:      runtimeCfg,
			ParameterFileNames: parameterFileNames,
			ParsedActions:      javaSourcesAsParsedActions(orderedJavaSourceActionFiles(projectConfig, files), files),
		}); err != nil {
			return nil, err
		}
		return result, nil
	}

	// Parse each lifecycle/action file once. Some .usr [Actions] blocks include
	// vuser_init.c and vuser_end.c, so a naive "parse init, parse [Actions],
	// parse end" sequence duplicates setup and cleanup requests.
	sourceConstants := collectSourceStringConstants(files)
	var parsedActions []*ParsedAction
	var parseErrors []string
	sourceDeclarations := orderedSourceActionFileDeclarations(projectConfig, files)
	declarationsByResolvedFile := sourceDeclarationsByResolvedFile(sourceDeclarations, files)
	type parsedCSource struct {
		action *ParsedAction
		err    error
	}
	parsedByResolvedFile := make(map[string]parsedCSource)
	for _, declaration := range sourceDeclarations {
		actionFile := declaration.Name
		resolvedActionFile, content, ambiguous := findSourceArchiveFile(files, actionFile)
		phase := normalizeSourcePhase(declaration.Phase)
		if phase == "" {
			phase = sourceActionPhaseForProjectFile(projectConfig, actionFile)
		}
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
		cacheKey := strings.ToLower(normalizeRuntimeArchivePath(resolvedActionFile))
		cached, parsedAlready := parsedByResolvedFile[cacheKey]
		if !parsedAlready {
			cached.action, cached.err = ParseActionFile(resolvedActionFile, inlineSourceStringConstants(content, sourceConstants))
			parsedByResolvedFile[cacheKey] = cached
		}
		parsed := sourceActionForLifecycleDeclaration(
			cached.action,
			declaration,
			declarationsByResolvedFile[cacheKey],
		)
		err := cached.err
		if parsed != nil {
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
		if err := finalizeEnterpriseArchiveImportResult(result, archiveFinalizationContext{
			Files:              files,
			ProjectFileName:    projectFileName,
			ProjectSections:    projectSections,
			ProjectConfig:      projectConfig,
			RuntimeFileName:    runtimeFileName,
			RuntimeConfig:      runtimeCfg,
			ParameterFileNames: parameterFileNames,
			ParsedActions:      parsedActions,
		}); err != nil {
			return nil, err
		}
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
	if err := finalizeEnterpriseArchiveImportResult(result, archiveFinalizationContext{
		Files:              files,
		ProjectFileName:    projectFileName,
		ProjectSections:    projectSections,
		ProjectConfig:      projectConfig,
		RuntimeFileName:    runtimeFileName,
		RuntimeConfig:      runtimeCfg,
		ParameterFileNames: parameterFileNames,
		ParsedActions:      parsedActions,
	}); err != nil {
		return nil, err
	}
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

func finalizeEnterpriseArchiveImportResult(
	result *ConvertResult,
	ctx archiveFinalizationContext,
) error {
	if result == nil {
		return nil
	}
	appendCSourceParseFailureGates(result, ctx.ParsedActions)
	applyBrowserEmulationRuntimeSettingsReview(result, ctx.RuntimeConfig)
	parameterSections := make(map[string]map[string]map[string]string)
	scripts := result.effectiveScripts()
	// Multi-lane Java and other enterprise conversions can retain identical
	// source display names. Allocate the complete result batch before creating
	// credential targets so every binding points at one unambiguous script.
	uniquify.DeduplicateScriptNames(scripts)
	if len(scripts) > 0 {
		parameterMergeStates := make(map[*dsl.Script]*parameterMergeState, len(scripts))
		parameterCollisions := make(map[*dsl.Script][]parameterDefinitionCollision, len(scripts))
		for _, script := range scripts {
			if script != nil {
				parameterMergeStates[script] = newParameterMergeState(script)
			}
		}
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
				if len(parsed.Parameters) > 0 || len(parsed.DataSources) > 0 {
					collisions := mergeParsedParameterFile(
						script,
						parsed,
						parameterFileName,
						parameterMergeStates[script],
					)
					parameterCollisions[script] = append(parameterCollisions[script], collisions...)
				}
			}
			if len(parsed.Warnings) > 0 {
				result.Warnings = append(result.Warnings, parsed.Warnings...)
			}
			if len(parsed.Sections) > 0 {
				parameterSections[path.Base(parameterFileName)] = parsed.Sections
			}
		}
		for _, script := range scripts {
			if script == nil {
				continue
			}
			applyKnownParameterReferences(script)
			applyParameterDefinitionCollisionGate(result, script, parameterCollisions[script])
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
	if err := applyJavaVMParameterRejectionReview(
		result,
		scripts,
		ctx.RuntimeConfig,
		ctx.RuntimeFileName,
	); err != nil {
		return err
	}
	// JVM validation can omit structurally unsafe tokens while retaining safe
	// siblings. Project credential locators only after that filtering so their
	// occurrence indexes describe the final executable configuration.
	applyEnterpriseRuntimeBindings(result, scripts, ctx)
	applyRuntimeProfileSelectionReview(result, scripts, ctx)
	applyEnterpriseRuntimeFidelityReview(result, scripts, ctx)
	result.SourceReview = BuildSourceReview(ctx.Files, sourceReviewMappings(result))
	finalizeEnterpriseSourceDisclosure(result, ctx.Files, ctx.ParameterFileNames)
	reviewDeclaredTransactionConservation(result, ctx.ProjectFileName, ctx.ProjectConfig, ctx.RuntimeConfig)
	for _, script := range scripts {
		redactArchiveMaskedBodyMetadata(script)
		attachSourceMetadata(script, result, ctx.ProjectFileName, ctx.ProjectSections, ctx.ProjectConfig, ctx.Files, ctx.RuntimeFileName, ctx.RuntimeConfig, parameterSections, ctx.ParameterFileNames, ctx.TrueClientInspection)
	}
	attachImportFidelityReport(result, ctx.Files, ctx.ProjectConfig, ctx.ParsedActions)
	sanitizeEnterpriseResultReviewText(result)
	return nil
}

const javaVMParameterExecutionBlocker = "java_vm_parameters_require_safe_replacement"

const parameterDefinitionCollisionExecutionBlocker = "parameter_definition_collision_unresolved"

func applyParameterDefinitionCollisionGate(
	result *ConvertResult,
	script *dsl.Script,
	collisions []parameterDefinitionCollision,
) {
	if script == nil || len(collisions) == 0 {
		return
	}
	for _, collision := range collisions {
		detail := fmt.Sprintf(
			"parameter %q has conflicting definitions in %s and %s; the later definition was retained as %q, but source references to the original name are ambiguous",
			collision.Name,
			collision.ExistingSource,
			collision.ConflictingSource,
			collision.AlternateName,
		)
		if result != nil {
			result.Warnings = append(result.Warnings, detail+"; replay is blocked until one definition is selected and references are repaired")
			appendSourceReviewMapping(result, SourceReviewMapping{
				SourceFile:  collision.ConflictingSource,
				Function:    "Parameter " + collision.Name,
				TargetPath:  "parameters." + collision.AlternateName,
				TargetName:  collision.AlternateName,
				TargetKind:  "parameter",
				Status:      "review_only",
				SupportMode: string(SourceFunctionSupportReviewOnly),
				Notes:       "A conflicting parameter definition was retained under a deterministic alternate name; replay is blocked until the intended definition is selected.",
			})
		}
	}
	// Apply one aggregate gate per action. Repeating every collision finding on
	// every action is quadratic for a large multi-file parameter import and adds
	// no execution-safety value; exact per-definition provenance remains in the
	// warnings and source-review mappings above.
	finding := importreview.Finding{
		Code:       parameterDefinitionCollisionExecutionBlocker,
		Severity:   diag.Blocker,
		Element:    "LoadRunner parameters",
		Detail:     parameterDefinitionCollisionSummary(collisions),
		Location:   "parameters",
		Source:     "enterprise_script_archive",
		ReasonCode: parameterDefinitionCollisionExecutionBlocker,
		SourceRef:  parameterDefinitionCollisionSourceSummary(collisions),
		Resolution: "select_parameter_definition_and_remove_gate",
		Gate:       importreview.GateExecution,
	}
	_ = dslwalk.WalkActions(script, func(ref dslwalk.ActionRef) error {
		actionFinding := finding
		actionFinding.ActionPath = ref.Path
		importreview.ApplyToAction(ref.Action, []importreview.Finding{actionFinding})
		return nil
	})
	if result != nil {
		result.Warnings = uniqueSortedStrings(result.Warnings)
	}
}

func parameterDefinitionCollisionSummary(collisions []parameterDefinitionCollision) string {
	if len(collisions) == 0 {
		return "LoadRunner parameter definitions require review."
	}
	const sampleLimit = 3
	samples := make([]string, 0, min(len(collisions), sampleLimit))
	for _, collision := range collisions[:min(len(collisions), sampleLimit)] {
		samples = append(samples, fmt.Sprintf("%q retained as %q", collision.Name, collision.AlternateName))
	}
	detail := fmt.Sprintf(
		"%d conflicting LoadRunner parameter definition(s) were retained under deterministic alternate names (%s)",
		len(collisions),
		strings.Join(samples, "; "),
	)
	if len(collisions) > sampleLimit {
		detail += fmt.Sprintf("; %d additional conflict(s) are listed in Source Review", len(collisions)-sampleLimit)
	}
	return detail + ". Select the intended definitions and repair the original-name references before execution."
}

func parameterDefinitionCollisionSourceSummary(collisions []parameterDefinitionCollision) string {
	if len(collisions) == 0 {
		return "parameter files"
	}
	first := collisions[0].ConflictingSource
	additional := make(map[string]struct{})
	for _, collision := range collisions[1:] {
		if collision.ConflictingSource != "" && collision.ConflictingSource != first {
			additional[collision.ConflictingSource] = struct{}{}
		}
	}
	if len(additional) == 0 {
		return first
	}
	return fmt.Sprintf("%s (+%d parameter file(s); see Source Review)", first, len(additional))
}

const runtimeProfileSelectionReason = "runtime_profile_selection_unresolved"

// applyRuntimeProfileSelectionReview turns an unresolved cfg/run-logic profile
// selection into a durable execution gate. Falling back to runtime defaults is
// structurally safe, but it is not wire- or schedule-equivalent when the source
// selected conflicting profiles. A parity warning alone would therefore leave
// an incomplete conversion runnable.
func applyRuntimeProfileSelectionReview(
	result *ConvertResult,
	scripts []*dsl.Script,
	ctx archiveFinalizationContext,
) {
	if result == nil || ctx.RuntimeConfig == nil {
		return
	}
	selectionIssues := make([]RuntimeMigrationIssue, 0)
	for _, issue := range ctx.RuntimeConfig.RuntimeIssues {
		if issue.ID == "runtime_profile" || issue.profileSelection {
			selectionIssues = append(selectionIssues, issue)
		}
	}
	if len(selectionIssues) == 0 {
		return
	}

	for _, script := range scripts {
		if script == nil {
			continue
		}
		_ = dslwalk.WalkActions(script, func(ref dslwalk.ActionRef) error {
			findings := make([]importreview.Finding, 0, len(selectionIssues))
			for _, issue := range selectionIssues {
				sourceRefs := runtimeProfileSelectionSourceRefs(issue, ctx)
				for _, sourceRef := range sourceRefs {
					findings = append(findings, importreview.Finding{
						Code:       "import_review_required",
						Severity:   diag.Blocker,
						Element:    "runtime profile selection",
						Detail:     "the active runtime profile could not be selected unambiguously; safe defaults were retained only as a review draft",
						Location:   ref.Path,
						ActionPath: ref.Path,
						Source:     "enterprise_script_archive",
						ReasonCode: runtimeProfileSelectionReason,
						SourceRef:  sourceRef,
						Resolution: "replace_behavior_and_remove_gate",
						Gate:       importreview.GateExecution,
					})
				}
			}
			importreview.ApplyToAction(ref.Action, findings)
			for _, finding := range findings {
				appendSourceReviewMapping(result, SourceReviewMapping{
					SourceFile: strings.SplitN(finding.SourceRef, "#", 2)[0],
					Function:   "Runtime profile selection",
					TargetPath: ref.Path + ".metadata.import_review",
					TargetKind: "review",
					Status:     "review_only",
					Notes:      "Runtime defaults were retained in a disclosure-safe draft, but execution remains blocked until the profile conflict is resolved.",
				})
			}
			return nil
		})
	}
	result.Warnings = append(result.Warnings,
		"Runtime profile selection was unresolved; the imported draft uses safe defaults and remains blocked until the profile conflict is resolved.",
	)
	result.Warnings = uniqueSortedStrings(result.Warnings)
}

func runtimeProfileSelectionSourceRefs(
	issue RuntimeMigrationIssue,
	ctx archiveFinalizationContext,
) []string {
	refs := make([]string, 0, len(issue.RuntimeFiles)+1)
	pointer := "runtime_profile"
	if issue.ID == "pacing" {
		pointer = "run_logic_profile"
	}
	if projectFile := normalizeRuntimeArchivePath(ctx.ProjectFileName); projectFile != "" {
		refs = append(refs, projectFile+"#"+pointer)
	}
	for _, runtimeFile := range issue.RuntimeFiles {
		runtimeFile = normalizeRuntimeArchivePath(runtimeFile)
		if runtimeFile != "" {
			refs = append(refs, runtimeFile+"#"+pointer)
		}
	}
	if len(refs) == 0 {
		refs = append(refs, pointer)
	}
	return uniqueSortedStrings(refs)
}

const javaVMParameterExposureReviewCode = "java_vm_parameters_process_table_exposure"

// applyJavaVMParameterRejectionReview distinguishes structurally unsafe JVM
// options from credential-bearing source arguments. Unsafe process-control
// options are omitted and gated. Imported credentials are preserved exactly so
// the destination remains runnable without re-entry; each Java action carries
// value-free, non-blocking review evidence that the argument can be visible in
// the operating-system process table.
func applyJavaVMParameterRejectionReview(
	result *ConvertResult,
	scripts []*dsl.Script,
	runtimeCfg *RuntimeConfig,
	runtimeFileName string,
) error {
	if result == nil {
		return nil
	}
	runtimeFile := normalizeRuntimeArchivePath(runtimeFileName)
	if runtimeFile == "" && runtimeCfg != nil && len(runtimeCfg.RuntimeFiles) > 0 {
		runtimeFile = normalizeRuntimeArchivePath(runtimeCfg.RuntimeFiles[0])
	}
	rejections := make([]javaVMParameterRejection, 0)
	exposureReviews := make([]javaVMParameterRejection, 0)
	if runtimeCfg != nil {
		for _, finding := range runtimeCfg.javaVMParameterRejections {
			if finding.category == javarunner.VMParameterSensitiveProperty {
				exposureReviews = append(exposureReviews, finding)
				continue
			}
			rejections = append(rejections, finding)
		}
		exposureReviews = append(
			exposureReviews,
			runtimeCfg.javaVMParameterExposureReviews...,
		)
	}
	rejectedAny := false
	reviewedExposure := false
	for _, script := range scripts {
		if script == nil || !strings.EqualFold(strings.TrimSpace(script.Protocol), "java_vuser") {
			continue
		}
		scriptRejections := append([]javaVMParameterRejection(nil), rejections...)
		scriptExposureReviews := append([]javaVMParameterRejection(nil), exposureReviews...)
		sourceParameters := append(
			[]string(nil),
			script.Config.JavaVuser.AdditionalVMParameters...,
		)
		var rejectedDirectRows []string
		validationIssues := validateImportedJavaVMParameters(
			script.Config.JavaVuser.AdditionalVMParameters,
		)
		for _, issue := range javaVMParameterIssuesBySensitivity(validationIssues, true) {
			scriptExposureReviews = append(scriptExposureReviews, javaVMParameterRejection{
				sourceKey:  "config.java_vuser.additional_vm_parameters",
				tokenIndex: issue.Index,
				category:   issue.Category,
			})
		}
		if structuralIssues := javaVMParameterIssuesBySensitivity(validationIssues, false); len(structuralIssues) > 0 {
			script.Config.JavaVuser.AdditionalVMParameters =
				javaVMParametersWithoutStructuralIssues(
					script.Config.JavaVuser.AdditionalVMParameters,
					structuralIssues,
				)
			for _, issue := range structuralIssues {
				if issue.Index >= 0 && issue.Index < len(sourceParameters) {
					rejectedDirectRows = append(
						rejectedDirectRows,
						sourceParameters[issue.Index],
					)
				}
				scriptRejections = append(scriptRejections, javaVMParameterRejection{
					sourceKey:  "config.java_vuser.additional_vm_parameters",
					tokenIndex: issue.Index,
					category:   issue.Category,
				})
			}
		}
		preservedSourceRows := rejectedDirectRows
		if runtimeCfg != nil && len(runtimeCfg.javaVMParameterSourceRows) > 0 {
			preservedSourceRows = append(
				append([]string(nil), runtimeCfg.javaVMParameterSourceRows...),
				preservedSourceRows...,
			)
		}
		if len(scriptRejections) > 0 && len(preservedSourceRows) > 0 {
			if err := preserveRejectedJavaVMSourceRows(
				script,
				preservedSourceRows,
			); err != nil {
				return fmt.Errorf("retain rejected Java VM source rows: %w", err)
			}
		}
		if len(scriptExposureReviews) > 0 {
			reviewedExposure = true
			attachJavaVMParameterExposureReview(
				script,
				scriptExposureReviews,
				runtimeFile,
			)
		}
		if len(scriptRejections) > 0 {
			rejectedAny = true
		}
		for _, rejection := range scriptRejections {
			pointer, sourceRef := javaVMParameterReviewSource(rejection, runtimeFile)
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
	if reviewedExposure {
		result.Warnings = append(
			result.Warnings,
			"Credential-bearing Java VM parameters were preserved exactly for runnable import fidelity; review operating-system process-table exposure on the governed Java runner.",
		)
	}
	if rejectedAny {
		result.Warnings = append(
			result.Warnings,
			"One or more structurally unsafe Java VM parameters were omitted by import policy; Java Vuser execution remains blocked until the behavior is safely replaced and reviewed.",
		)
	}
	result.Warnings = uniqueSortedStrings(result.Warnings)
	return nil
}

func attachJavaVMParameterExposureReview(
	script *dsl.Script,
	reviews []javaVMParameterRejection,
	runtimeFile string,
) {
	if script == nil || len(reviews) == 0 {
		return
	}
	tokenIndexes := make([]int, 0, len(reviews))
	sourceRefs := make([]string, 0, len(reviews))
	seenIndexes := make(map[int]struct{}, len(reviews))
	for _, review := range reviews {
		if _, exists := seenIndexes[review.tokenIndex]; !exists {
			seenIndexes[review.tokenIndex] = struct{}{}
			tokenIndexes = append(tokenIndexes, review.tokenIndex)
		}
		_, sourceRef := javaVMParameterReviewSource(review, runtimeFile)
		sourceRefs = append(sourceRefs, sourceRef)
	}
	sort.Ints(tokenIndexes)
	sourceRefs = uniqueSortedStrings(sourceRefs)
	evidence := map[string]any{
		"version":           1,
		"reason_code":       javaVMParameterExposureReviewCode,
		"token_indices":     tokenIndexes,
		"source_refs":       sourceRefs,
		"resolution":        "review_process_table_exposure",
		"value_preserved":   true,
		"execution_blocker": false,
	}
	_ = dslwalk.WalkActions(script, func(ref dslwalk.ActionRef) error {
		if ref.Action.Metadata == nil {
			ref.Action.Metadata = make(map[string]any)
		}
		ref.Action.Metadata["java_vm_parameter_review"] = evidence
		return nil
	})
}

func javaVMParameterReviewSource(
	review javaVMParameterRejection,
	runtimeFile string,
) (string, string) {
	sourceKey := strings.TrimSpace(review.sourceKey)
	if sourceKey == "" {
		sourceKey = "config.java_vuser.additional_vm_parameters"
	}
	pointer := "JavaVM:Options/" + sourceKey
	if review.sourceLine > 0 {
		pointer += fmt.Sprintf(":line-%d", review.sourceLine)
	}
	sourceRef := pointer
	if runtimeFile != "" {
		sourceRef = runtimeFile + "#" + pointer
	}
	return pointer, sourceRef
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

const (
	maxProtectedJavaVMSourceRows       = 256
	maxProtectedJavaVMSourceValueBytes = 64 << 10
	maxProtectedJavaVMSourceTotalBytes = 2 << 20
)

func preserveRejectedJavaVMSourceRows(script *dsl.Script, rows []string) error {
	if script == nil || len(rows) == 0 {
		return nil
	}
	if len(rows) > maxProtectedJavaVMSourceRows {
		return fmt.Errorf(
			"%d rows exceed the %d-item protected-source limit",
			len(rows),
			maxProtectedJavaVMSourceRows,
		)
	}
	if script.Metadata == nil {
		script.Metadata = &dsl.ScriptMetadata{}
	}
	if script.Metadata.Import == nil {
		script.Metadata.Import = &dsl.ImportMetadata{}
	}
	if strings.TrimSpace(script.Metadata.Import.Source) == "" {
		script.Metadata.Import.Source = "enterprise_script_archive"
	}
	if script.Metadata.Import.Preserved == nil {
		script.Metadata.Import.Preserved = make(map[string]any)
	}
	encoded := make([]string, len(rows))
	totalBytes := 0
	for index, row := range rows {
		if len(row) > maxProtectedJavaVMSourceValueBytes {
			return fmt.Errorf(
				"row %d exceeds the %d-byte protected-source value limit",
				index,
				maxProtectedJavaVMSourceValueBytes,
			)
		}
		totalBytes += len(row)
		if totalBytes > maxProtectedJavaVMSourceTotalBytes {
			return fmt.Errorf(
				"rows exceed the %d-byte protected-source total limit",
				maxProtectedJavaVMSourceTotalBytes,
			)
		}
		encoded[index] = base64.StdEncoding.EncodeToString([]byte(row))
	}
	script.Metadata.Import.Preserved[dsl.ImportPreservedEnterpriseJavaVMSourceKey] =
		map[string]any{
			"version":     1,
			"rows_base64": encoded,
		}
	return nil
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
				for _, branch := range actions[i].Control.ActionBranches() {
					redactActions(branch.Actions)
				}
			}
		}
	}
	redactActions(script.Init)
	redactActions(script.Actions)
	redactActions(script.Teardown)
}

func orderedSourceActionFileDeclarations(projectConfig *ProjectConfig, files map[string]string) []SourceActionFile {
	candidates := []SourceActionFile{{
		Name: "vuser_init.c", Phase: "init", Language: "c", Function: "vuser_init",
	}}
	if projectConfig != nil && len(projectConfig.SourceActionFiles) > 0 {
		for _, sourceFile := range projectConfig.SourceActionFiles {
			if strings.EqualFold(sourceFile.Language, "c") || strings.HasSuffix(strings.ToLower(sourceFile.Name), ".c") {
				candidates = append(candidates, sourceFile)
			}
		}
	} else if projectConfig != nil && len(projectConfig.ActionFiles) > 0 {
		for _, actionFile := range projectConfig.ActionFiles {
			candidates = append(candidates, SourceActionFile{
				Name: actionFile, Phase: sourceActionPhase(actionFile), Language: "c",
			})
		}
	} else {
		candidates = append(candidates, SourceActionFile{
			Name: "Action.c", Phase: "action", Language: "c", Function: "Action",
		})
	}
	candidates = append(candidates, SourceActionFile{
		Name: "vuser_end.c", Phase: "teardown", Language: "c", Function: "vuser_end",
	})

	ordered := make([]SourceActionFile, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		name := strings.TrimSpace(candidate.Name)
		if name == "" {
			continue
		}
		resolved, _, _ := findSourceArchiveFile(files, name)
		if resolved == "" && !sourceActionPointerRequired(projectConfig, name) {
			continue
		}
		phase := normalizeSourcePhase(candidate.Phase)
		if phase == "" {
			phase = sourceActionPhaseForProjectFile(projectConfig, name)
		}
		key := strings.ToLower(normalizeRuntimeArchivePath(name))
		if resolved != "" {
			key = strings.ToLower(normalizeRuntimeArchivePath(resolved))
		}
		// One source file may intentionally own init, action, and teardown
		// functions. Deduplicate only within a phase; collapsing by physical
		// path alone silently drops later lifecycle declarations.
		key += "\x00" + phase
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		candidate.Name = name
		candidate.Phase = phase
		if candidate.Language == "" {
			candidate.Language = "c"
		}
		ordered = append(ordered, candidate)
	}
	return ordered
}

func sourceDeclarationsByResolvedFile(
	declarations []SourceActionFile,
	files map[string]string,
) map[string][]SourceActionFile {
	grouped := make(map[string][]SourceActionFile)
	for _, declaration := range declarations {
		resolved, _, ambiguous := findSourceArchiveFile(files, declaration.Name)
		if resolved == "" || len(ambiguous) > 0 {
			continue
		}
		key := strings.ToLower(normalizeRuntimeArchivePath(resolved))
		grouped[key] = append(grouped[key], declaration)
	}
	return grouped
}

func sourceActionForLifecycleDeclaration(
	parsed *ParsedAction,
	declaration SourceActionFile,
	siblings []SourceActionFile,
) *ParsedAction {
	if parsed == nil {
		return nil
	}
	phase := normalizeSourcePhase(declaration.Phase)
	if phase == "" {
		phase = sourceActionPhase(declaration.Name)
	}
	out := &ParsedAction{
		Filename: parsed.Filename,
		Phase:    phase,
	}

	phases := make(map[string]struct{})
	for _, sibling := range siblings {
		siblingPhase := normalizeSourcePhase(sibling.Phase)
		if siblingPhase == "" {
			siblingPhase = sourceActionPhase(sibling.Name)
		}
		phases[siblingPhase] = struct{}{}
	}
	if len(phases) <= 1 {
		out.Functions = append([]ParsedFunction(nil), parsed.Functions...)
	} else {
		for _, function := range parsed.Functions {
			ownerPhase, ok := sourceLifecyclePhaseForFunction(function.sourceFunction, siblings)
			if ok && ownerPhase == phase {
				out.Functions = append(out.Functions, function)
			}
			if !ok {
				out.ParseFailures = appendUniqueCSourceParseFailures(
					out.ParseFailures,
					CSourceParseFailure{
						Filename:   parsed.Filename,
						Phase:      phase,
						LineNumber: function.LineNumber,
						Kind:       "ambiguous_lifecycle_function_ownership",
					},
				)
			}
		}
	}
	for index := range out.Functions {
		out.Functions[index].Phase = phase
	}
	out.ParseFailures = appendUniqueCSourceParseFailures(out.ParseFailures, parsed.ParseFailures...)
	for index := range out.ParseFailures {
		out.ParseFailures[index].Phase = phase
	}
	return out
}

func sourceLifecyclePhaseForFunction(
	functionName string,
	declarations []SourceActionFile,
) (string, bool) {
	functionName = strings.TrimSpace(functionName)
	if functionName == "" {
		return "", false
	}
	matches := make(map[string]struct{})
	for _, declaration := range declarations {
		if !strings.EqualFold(strings.TrimSpace(declaration.Function), functionName) {
			continue
		}
		phase := normalizeSourcePhase(declaration.Phase)
		if phase == "" {
			phase = sourceActionPhase(declaration.Name)
		}
		matches[phase] = struct{}{}
	}
	if len(matches) == 1 {
		for phase := range matches {
			return phase, true
		}
	}
	if len(matches) > 1 {
		return "", false
	}

	var inferred string
	switch strings.ToLower(functionName) {
	case "vuser_init", "init":
		inferred = "init"
	case "vuser_end", "end", "teardown":
		inferred = "teardown"
	case "action":
		inferred = "action"
	default:
		return "", false
	}
	for _, declaration := range declarations {
		phase := normalizeSourcePhase(declaration.Phase)
		if phase == "" {
			phase = sourceActionPhase(declaration.Name)
		}
		if phase == inferred {
			return inferred, true
		}
	}
	return "", false
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
		return strings.EqualFold(path.Base(strings.TrimSpace(name)), "Action.c")
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
	base := strings.ToLower(path.Base(strings.TrimSpace(fileName)))
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
				if !strings.EqualFold(path.Base(sourceFile.Name), path.Base(fileName)) {
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
	_, content, ok := findFileEntry(files, name)
	return content, ok
}

// findFileEntry resolves an archive member without depending on Go map order.
// Ambiguous case-folded/basename matches fail closed unless every candidate has
// byte-identical content, in which case the lexicographically first path is a
// deterministic provenance representative.
func findFileEntry(files map[string]string, name string) (string, string, bool) {
	name = cleanConfigValue(name)
	// Direct match
	if content, ok := files[name]; ok {
		return name, content, true
	}
	keys := make([]string, 0, len(files))
	for key := range files {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var matches []string
	for _, key := range keys {
		if strings.EqualFold(key, name) {
			matches = append(matches, key)
		}
	}
	if key, content, ok := uniqueArchiveFileMatch(files, matches); ok {
		return key, content, true
	} else if len(matches) > 0 {
		return "", "", false
	}
	// Try base name match
	baseName := path.Base(name)
	matches = matches[:0]
	for _, key := range keys {
		if path.Base(key) == baseName || strings.EqualFold(path.Base(key), baseName) {
			matches = append(matches, key)
		}
	}
	return uniqueArchiveFileMatch(files, matches)
}

func uniqueArchiveFileMatch(files map[string]string, matches []string) (string, string, bool) {
	if len(matches) == 0 {
		return "", "", false
	}
	content := files[matches[0]]
	for _, key := range matches[1:] {
		if files[key] != content {
			return "", "", false
		}
	}
	return matches[0], content, true
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
		if strings.EqualFold(path.Base(name), "main.js") && strings.Contains(content, "load.") {
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
		base := strings.ToLower(path.Base(name))
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
		settings["additional_vm_parameters_raw"] = executableJavaVMParameterRow(runtimeCfg.JavaVMParametersRaw)
	}
	// LoadRunner spills a long parameter line across numbered companion keys, so
	// the single raw value above cannot round-trip the source. Preserve every
	// row in command-line order alongside it.
	if len(runtimeCfg.JavaVMParameterRows) > 1 {
		rows := make([]string, len(runtimeCfg.JavaVMParameterRows))
		for index, row := range runtimeCfg.JavaVMParameterRows {
			rows[index] = executableJavaVMParameterRow(row)
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

// reviewDeclaredTransactionConservation cross-checks the transactions the
// project declares ([TransactionsOrder]/[Transactions] in the .usr) against
// the transactions actually started or published in the converted source. A
// declared name with no publisher is either dead configuration or a dropped
// call — both deserve review, not silence.
func reviewDeclaredTransactionConservation(result *ConvertResult, projectFileName string, project *ProjectConfig, runtimeCfg *RuntimeConfig) {
	if result == nil || result.sourceTransactionNames == nil || project == nil || len(project.Transactions) == 0 {
		return
	}
	const maxDeclaredTransactionEvidence = 40
	missingCount := 0
	missingEvidence := make([]string, 0, min(len(project.Transactions), maxDeclaredTransactionEvidence))
	for _, declared := range project.Transactions {
		name := strings.TrimSpace(declared)
		if name == "" {
			continue
		}
		if _, ok := result.sourceTransactionNames[strings.ToLower(name)]; ok {
			continue
		}
		missingCount++
		if len(missingEvidence) < maxDeclaredTransactionEvidence {
			missingEvidence = append(missingEvidence, boundedRuntimeReviewEvidence(scrub.Value(name, scrub.Policy{})))
		}
	}
	if missingCount == 0 {
		return
	}
	if omitted := missingCount - len(missingEvidence); omitted > 0 {
		missingEvidence = append(missingEvidence, fmt.Sprintf("+%d more", omitted))
	}
	note := fmt.Sprintf("The project declares %d transactions (%s) that no converted action starts or publishes; verify the source calls were imported.", missingCount, strings.Join(missingEvidence, ", "))
	if runtimeCfg != nil {
		var sourceFiles []string
		if projectFileName = normalizeRuntimeArchivePath(projectFileName); projectFileName != "" {
			sourceFiles = []string{projectFileName}
		}
		runtimeCfg.RuntimeIssues = append(runtimeCfg.RuntimeIssues, RuntimeMigrationIssue{
			ID:           "transactions",
			Area:         "Transactions",
			Notes:        note,
			RuntimeFiles: sourceFiles,
		})
		return
	}
	result.Warnings = append(result.Warnings, note)
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
		preserved["project_file"] = path.Base(projectFileName)
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
	dispositions := classifyRuntimeSelectedSections(selectedSections, script.Protocol)
	if len(dispositions) > 0 {
		preserved["runtime_setting_dispositions"] = dispositions
	}
	parity := applyRuntimeMigrationIssues(buildRTSParityReportForProtocol(selectedSections, script.Protocol), runtimeCfg)
	parity = appendRuntimeDispositionParityEntries(parity, dispositions)
	if len(parity) > 0 {
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
			files = append(files, path.Base(name))
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
