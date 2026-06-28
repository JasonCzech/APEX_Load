package enterprisearchive

import (
	"fmt"
	"math"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/dsl"
)

var (
	javaLRCallRe             = regexp.MustCompile(`(?i)\blr\.([A-Za-z_][A-Za-z0-9_]*)\s*\((.*)\)\s*;?\s*$`)
	javaEnvPutRe             = regexp.MustCompile(`(?i)\.put\s*\((.+)\)\s*;?\s*$`)
	javaSetMessagePropertyRe = regexp.MustCompile(`(?i)^([A-Za-z_][A-Za-z0-9_]*)\.set(?:String|Boolean|Int|Long)Property\s*\((.+)\)\s*;?\s*$`)
	javaSendOnProducerRe     = regexp.MustCompile(`(?i)^\s*([A-Za-z_][A-Za-z0-9_]*)\.send\s*\((.*)\)\s*;?\s*$`)
	javaSendCallRe           = regexp.MustCompile(`(?i)\.send\s*\((.*)\)\s*;?\s*$`)
	javaReceiveCallRe        = regexp.MustCompile(`(?i)\.receive(?:NoWait)?\s*\((.*)\)\s*;?\s*$`)
	javaAckCallRe            = regexp.MustCompile(`(?i)\.acknowledge\s*\(\s*\)\s*;?\s*$`)
	javaCommitCallRe         = regexp.MustCompile(`(?i)\.commit\s*\(\s*\)\s*;?\s*$`)
	javaRollbackCallRe       = regexp.MustCompile(`(?i)\.rollback\s*\(\s*\)\s*;?\s*$`)
	javaDeclarationOnlyRe    = regexp.MustCompile(`^\s*(?:final\s+)?[A-Za-z_][A-Za-z0-9_<>\[\].]*\s+[A-Za-z_][A-Za-z0-9_]*\s*;\s*$`)
	javaBlockCommentRe       = regexp.MustCompile(`(?s)/\*.*?\*/`)
)

const (
	javaVuserRunnerRequirement = "java_vuser_jvm_runner"
	javaVuserSidecarTransport  = "stdio_json_lines"
	javaVuserEventContract     = "apexload.java_vuser.lrapi_events.v1"
	javaVuserShimClass         = "lrapi.lr"
)

type javaImportState struct {
	env                javaStringEnv
	unrecognizedAction int
	jmsActions         int
	configReview       int
	statementCount     int
	profile            JavaProfile
	skipHTTP           bool
	skipDatabase       bool

	// recognizedLRActions counts lr.* methods that resolved to a documented
	// disposition (so a fully-classified non-JMS lr script can be reported as
	// "classified, no executable protocol target" rather than "unmapped").
	recognizedLRActions int
	// Transaction / think-time state mirrors the C converter loop so Java
	// transactions feed Stats and downstream actions are grouped, and
	// lr.think_time feeds the generated think-time distribution.
	currentTransaction    string
	currentSubTransaction string
	thinkTimes            []float64
}

func ConvertJava(files map[string]string, opts ConvertOptions, detected DetectedProtocol, projectConfig *ProjectConfig) (*ConvertResult, error) {
	program, parseWarnings := parseJavaProgram(files)
	profile := classifyJavaArchive(program, projectConfig, files)
	if opts.UseMaskedBodySecretReferences == false {
		opts.UseMaskedBodySecretReferences = true
	}
	result := &ConvertResult{
		DetectedProtocol: &detected,
		Warnings:         append([]string(nil), detected.Warnings...),
		Script:           newJavaScriptForSurface(opts, profile, profile.Primary),
	}
	result.Warnings = append(result.Warnings, parseWarnings...)

	if len(program.Methods) == 0 {
		if len(program.Unparsed) > 0 {
			state := &javaImportState{env: newJavaStringEnv(), profile: profile}
			for _, statement := range program.Unparsed {
				result.Stats.TotalFunctions++
				state.statementCount++
				appendJavaReview(result, state, statement, "action", "unparsed_java_region", true)
			}
			reviewDetected := markDetectedReviewOnly(detected, "Java source contained unparsed regions with unmatched braces.")
			result.DetectedProtocol = &reviewDetected
			result.ReviewArtifact = NewReviewArtifact(reviewDetected, files, nil, map[string]any{
				"reason":           "unparsed_java_region",
				"unparsed_regions": len(program.Unparsed),
			})
			result.Warnings = append(result.Warnings, "Java Vuser import preserved unparsed Java source regions for review because the scanner could not match their braces.")
			result.Scripts = []*dsl.Script{result.Script}
			return result, nil
		}
		result.Script = nil
		reviewDetected := markDetectedReviewOnly(detected, "No parseable Java lifecycle methods were found.")
		result.DetectedProtocol = &reviewDetected
		result.ReviewArtifact = NewReviewArtifact(reviewDetected, files, nil, map[string]any{"reason": "no_parseable_java_methods"})
		result.Warnings = append(result.Warnings, "Java Vuser import was preserved for review because no lifecycle methods were parseable.")
		return result, nil
	}

	var laneResults []*ConvertResult
	if profile.Surfaces[javaSurfaceHTTP] {
		if httpActions := javaHTTPParsedActions(program); len(flattenParsedFunctions(httpActions)) > 0 {
			httpResult, err := Convert(httpActions, opts)
			if err != nil {
				return nil, err
			}
			if httpResult != nil && httpResult.Script != nil {
				httpDetected := detected
				httpDetected.CanonicalSourceProtocol = "java_over_http"
				httpDetected.APEXLoadProtocol = "http"
				httpDetected.ImportMode = ImportModeExecutable
				httpDetected.DisplayName = "Java Vuser HTTP"
				httpResult.DetectedProtocol = &httpDetected
				httpResult.Script.Name = convertutil.FirstNonEmpty(opts.ScriptName, "Converted_Java_HTTP")
				httpResult.Script.GeneratedBy = "apexload-enterprise-script-archive-java-importer"
				httpResult.Script.GeneratedFrom = "enterprise_script_archive"
				javaEnsureHeaderLists(httpResult.Script)
				laneResults = append(laneResults, httpResult)
			}
		}
	}
	if profile.Surfaces[javaSurfaceDatabase] {
		if dbActions := javaDatabaseParsedActions(program, files); len(flattenParsedFunctions(dbActions)) > 0 {
			dbDetected := detected
			dbDetected.CanonicalSourceProtocol = "java_jdbc"
			dbDetected.APEXLoadProtocol = "database"
			dbDetected.ImportMode = ImportModeExecutable
			dbDetected.DisplayName = "Java Vuser JDBC"
			dbResult, err := ConvertDatabase(dbActions, opts, dbDetected)
			if err != nil {
				return nil, err
			}
			if dbResult != nil && dbResult.Script != nil {
				dbResult.Script.Name = convertutil.FirstNonEmpty(opts.ScriptName, "Converted_Java_JDBC")
				if len(laneResults) > 0 {
					dbResult.Script.Name += "_database"
				}
				laneResults = append(laneResults, dbResult)
			}
		}
	}
	if profile.Surfaces[javaSurfaceSOAP] {
		if soapActions := javaSOAPParsedActions(program, files); len(flattenParsedFunctions(soapActions)) > 0 {
			soapResult, err := Convert(soapActions, opts)
			if err != nil {
				return nil, err
			}
			if soapResult != nil && soapResult.Script != nil {
				soapDetected := detected
				soapDetected.CanonicalSourceProtocol = "java_soap"
				soapDetected.APEXLoadProtocol = soapResult.Script.Protocol
				soapDetected.ImportMode = ImportModeExecutable
				soapDetected.DisplayName = "Java Vuser SOAP"
				soapResult.DetectedProtocol = &soapDetected
				soapResult.Script.Name = convertutil.FirstNonEmpty(opts.ScriptName, "Converted_Java_SOAP")
				if len(laneResults) > 0 {
					soapResult.Script.Name += "_soap"
				}
				soapResult.Script.GeneratedBy = "apexload-enterprise-script-archive-java-importer"
				soapResult.Script.GeneratedFrom = "enterprise_script_archive"
				javaEnsureHeaderLists(soapResult.Script)
				laneResults = append(laneResults, soapResult)
			}
		}
	}
	if profile.Surfaces[javaSurfaceJMS] || len(laneResults) == 0 || profile.Surfaces[javaSurfaceReview] {
		jmsResult, err := convertJavaStatementLane(files, opts, detected, program, parseWarnings, profile, len(laneResults) > 0)
		if err != nil {
			return nil, err
		}
		if jmsResult != nil && jmsResult.Script != nil && (len(jmsResult.Script.Actions)+len(jmsResult.Script.Init)+len(jmsResult.Script.Teardown) > 0 || len(laneResults) == 0) {
			laneResults = append(laneResults, jmsResult)
		}
	}
	if len(laneResults) == 0 {
		result.Script.Actions = append(result.Script.Actions, javaSourceReviewAction(javaStatement{
			Text:      "No executable Java actions were recognized.",
			StartLine: 1,
			EndLine:   1,
		}, "action", "no_java_actions"))
		result.Scripts = []*dsl.Script{result.Script}
		return result, nil
	}
	return mergeJavaLaneResults(laneResults, detected, files, profile), nil
}

func convertJavaStatementLane(files map[string]string, opts ConvertOptions, detected DetectedProtocol, program javaProgram, parseWarnings []string, profile JavaProfile, secondary bool) (*ConvertResult, error) {
	surface := javaSurfaceJMS
	if !profile.Surfaces[javaSurfaceJMS] {
		surface = profile.Primary
	}
	result := &ConvertResult{
		DetectedProtocol: &detected,
		Warnings:         append(append([]string(nil), detected.Warnings...), parseWarnings...),
		Script:           newJavaScriptForSurface(opts, profile, surface),
	}
	if secondary && result.Script != nil {
		result.Script.Name = result.Script.Name + "_review"
	}
	state := &javaImportState{
		env:          newJavaStringEnv(),
		profile:      profile,
		skipHTTP:     profile.Surfaces[javaSurfaceHTTP],
		skipDatabase: profile.Surfaces[javaSurfaceDatabase],
	}
	for _, phase := range []string{"init", "action", "teardown"} {
		statements := javaLifecycleStatements(program, phase)
		for _, statement := range statements {
			state.statementCount++
			result.Stats.TotalFunctions++
			if err := convertJavaStatement(result, state, files, phase, statement); err != nil {
				return nil, err
			}
		}
	}
	appendJavaUnparsedRegions(result, state, program)
	attachJavaTruststoreArtifact(result, files)
	applyJavaThinkTimeDistribution(result, state)

	if opts.AllowJavaVuserRunner && (state.jmsActions == 0 || state.unrecognizedAction > 0 || state.configReview > 0) {
		return convertJavaVuserRunnerDraft(files, opts, detected, program, result.Warnings, state), nil
	}

	if state.jmsActions == 0 || state.unrecognizedAction > 0 || state.configReview > 0 {
		detected = markDetectedReviewOnly(detected, "")
		if profile.Surfaces[javaSurfaceJMS] {
			detected.APEXLoadProtocol = profile.JMSProtocol
		}
		detected.RequiredEvidence = appendUnique(detected.RequiredEvidence, "Operator review of unmapped Java statements", "JMS broker configuration verification")
		result.DetectedProtocol = &detected
		result.ReviewArtifact = NewReviewArtifact(detected, files, nil, map[string]any{
			"reason":                         "partial_java_mapping",
			"jms_actions":                    state.jmsActions,
			"unrecognized_action_statements": state.unrecognizedAction,
			"configuration_review_items":     state.configReview,
			"recognized_lr_actions":          state.recognizedLRActions,
		})
		// Distinguish "every statement classified but no executable protocol
		// target" (a pure lr.* script) from genuinely unmapped statements, so the
		// warning is honest rather than alarming.
		if state.unrecognizedAction == 0 && state.configReview == 0 && state.recognizedLRActions > 0 {
			result.Warnings = append(result.Warnings, "Java Vuser import classified every lr.* statement, but the script has no executable APEX protocol target (no JMS actions); it remains review-only.")
		} else {
			result.Warnings = append(result.Warnings, "Java Vuser import produced a partial JMS draft and remains review-only until unmapped statements are resolved.")
		}
	} else {
		detected.ImportMode = ImportModeExecutable
		detected.APEXLoadProtocol = profile.JMSProtocol
		detected.CanonicalSourceProtocol = "java_vuser"
		result.DetectedProtocol = &detected
	}
	if len(result.Script.Actions) == 0 {
		result.Script.Actions = append(result.Script.Actions, javaSourceReviewAction(javaStatement{
			Text:      "No executable JMS actions were recognized.",
			StartLine: 1,
			EndLine:   1,
		}, "action", "no_jms_actions"))
		result.Stats.ReviewOnlyFunctions++
	}
	return result, nil
}

func appendJavaUnparsedRegions(result *ConvertResult, state *javaImportState, program javaProgram) {
	for _, statement := range program.Unparsed {
		state.statementCount++
		result.Stats.TotalFunctions++
		appendJavaReview(result, state, statement, "action", "unparsed_java_region", true)
	}
}

func mergeJavaLaneResults(lanes []*ConvertResult, detected DetectedProtocol, files map[string]string, profile JavaProfile) *ConvertResult {
	if len(lanes) == 1 {
		lanes[0].Scripts = lanes[0].effectiveScripts()
		return lanes[0]
	}
	merged := &ConvertResult{
		DetectedProtocol: &DetectedProtocol{
			CanonicalSourceProtocol: "java_vuser",
			APEXLoadProtocol:        "multi",
			ImportMode:              ImportModeExecutable,
			Confidence:              detected.Confidence,
			DisplayName:             "Java Vuser Multi-Protocol",
			SourceType:              detected.SourceType,
			Signals:                 detected.Signals,
			Warnings:                detected.Warnings,
			RequiredEvidence:        detected.RequiredEvidence,
		},
		Warnings: append([]string(nil), detected.Warnings...),
		Scripts:  make([]*dsl.Script, 0, len(lanes)),
	}
	for _, lane := range lanes {
		if lane == nil {
			continue
		}
		merged.Warnings = append(merged.Warnings, lane.Warnings...)
		merged.Stats.TotalFunctions += lane.Stats.TotalFunctions
		merged.Stats.ConvertedActions += lane.Stats.ConvertedActions
		merged.Stats.LogicalActions += lane.Stats.LogicalActions
		merged.Stats.RawRequestActions += lane.Stats.RawRequestActions
		merged.Stats.CollapsedRequests += lane.Stats.CollapsedRequests
		merged.Stats.TransactionMarkers += lane.Stats.TransactionMarkers
		merged.Stats.ContributorFunctions += lane.Stats.ContributorFunctions
		merged.Stats.Extractors += lane.Stats.Extractors
		merged.Stats.ThinkTimes += lane.Stats.ThinkTimes
		merged.Stats.Transactions += lane.Stats.Transactions
		merged.Stats.NativeFunctions += lane.Stats.NativeFunctions
		merged.Stats.ReviewOnlyFunctions += lane.Stats.ReviewOnlyFunctions
		merged.Stats.ExternalRequiredFunctions += lane.Stats.ExternalRequiredFunctions
		merged.Stats.CustomCallFunctions += lane.Stats.CustomCallFunctions
		merged.Stats.Skipped += lane.Stats.Skipped
		merged.Stats.UnsupportedFuncs = append(merged.Stats.UnsupportedFuncs, lane.Stats.UnsupportedFuncs...)
		if lane.SourceReview != nil {
			for _, mapping := range lane.SourceReview.Mappings {
				appendSourceReviewMapping(merged, mapping)
			}
		}
		if lane.Script != nil {
			merged.Scripts = append(merged.Scripts, lane.Script)
		}
	}
	if len(merged.Scripts) > 0 {
		merged.Script = merged.Scripts[0]
	}
	if profile.Surfaces[javaSurfaceReview] {
		reviewDetected := *merged.DetectedProtocol
		reviewDetected.ImportMode = ImportModeReviewOnly
		merged.ReviewArtifact = NewReviewArtifact(reviewDetected, files, nil, map[string]any{
			"reason":          "java_named_review_surfaces",
			"review_families": profile.ReviewFamilies,
		})
	}
	merged.Warnings = uniqueSortedStrings(merged.Warnings)
	merged.Stats.UnsupportedFuncs = uniqueSortedStrings(merged.Stats.UnsupportedFuncs)
	return merged
}

func convertJavaVuserRunnerDraft(files map[string]string, opts ConvertOptions, detected DetectedProtocol, program javaProgram, warnings []string, state *javaImportState) *ConvertResult {
	detected.CanonicalSourceProtocol = "java_vuser"
	detected.APEXLoadProtocol = "java_vuser"
	detected.ImportMode = ImportModeExternalRunnerRequired
	detected.DisplayName = convertutil.FirstNonEmpty(detected.DisplayName, "Java Vuser")
	detected.RunnerRequirement = javaVuserRunnerRequirement
	detected.RequiredEvidence = appendUnique(detected.RequiredEvidence,
		"Explicit operator opt-in to JVM sidecar replay",
		"generator-java image with isolated classpath and no ambient secret/environment leakage",
		"java_vuser_jvm_runner entitlement",
	)
	detected.Warnings = append(detected.Warnings, "Java Vuser sidecar replay executes source code and remains gated until a governed runner pool is configured.")

	className := javaVuserClassName(program)
	action := dsl.Action{
		Name:   "Run Java Vuser Lifecycle",
		Type:   "java_vuser_run",
		Target: className,
		Params: map[string]any{
			"method":             "full_lifecycle",
			"source_archive_ref": "enterprise_script_archive",
			"runner_requirement": javaVuserRunnerRequirement,
			"event_contract":     javaVuserEventContract,
		},
		Metadata: map[string]any{
			"review_required":    true,
			"review_reason":      "java_vuser_sidecar_runner_required",
			"runner_requirement": javaVuserRunnerRequirement,
			"source_protocol":    "java_vuser",
			"sidecar_transport":  javaVuserSidecarTransport,
			"event_contract":     javaVuserEventContract,
			"shim_class":         javaVuserShimClass,
			"shim_callbacks":     []string{"transaction_start", "transaction_end", "log", "error"},
			"operator_opt_in":    true,
			"source_class":       className,
		},
	}
	result := &ConvertResult{
		DetectedProtocol: &detected,
		Warnings:         append(append([]string(nil), warnings...), "Java Vuser sidecar fallback draft was generated by explicit opt-in; launch remains blocked until a governed java_vuser runner is enabled."),
		Script: &dsl.Script{
			Name:          convertutil.FirstNonEmpty(opts.ScriptName, "Java_Vuser_Sidecar_Draft"),
			Version:       "1.0.0",
			Protocol:      "java_vuser",
			GeneratedBy:   "apexload-enterprise-script-archive-java-importer",
			GeneratedFrom: "enterprise_script_archive",
			Config: dsl.ScriptConfig{
				JavaVuser: dsl.JavaVuserConfig{
					Enabled:         false,
					HarnessJar:      "runners/java_vuser/apexload-java-vuser-runner.jar",
					MaxProcesses:    25,
					ActionTimeoutMs: 30000,
				},
			},
			Actions: []dsl.Action{action},
		},
		Stats: ConvertStats{
			TotalFunctions:            state.statementCount,
			ConvertedActions:          1,
			ExternalRequiredFunctions: 1,
			ReviewOnlyFunctions:       state.unrecognizedAction + state.configReview,
			CustomCallFunctions:       0,
			Skipped:                   0,
			UnsupportedFuncs:          nil,
		},
	}
	result.ReviewArtifact = NewReviewArtifact(detected, files, nil, map[string]any{
		"reason":                         "java_vuser_sidecar_runner_required",
		"runner_requirement":             javaVuserRunnerRequirement,
		"sidecar_transport":              javaVuserSidecarTransport,
		"event_contract":                 javaVuserEventContract,
		"shim_class":                     javaVuserShimClass,
		"unrecognized_action_statements": state.unrecognizedAction,
		"configuration_review_items":     state.configReview,
	})
	appendJavaMapping(result, javaVuserMappingStatement(program), "actions[0]", action.Name, "action", "review_only", "Preserved Java Vuser lifecycle for gated JVM sidecar replay.", SourceFunctionSupportExternalRequired)
	recordSourceFunctionSupport(result, SourceFunctionSupportExternalRequired)
	return result
}

func javaVuserClassName(program javaProgram) string {
	for _, class := range program.Classes {
		base := strings.TrimSuffix(filepath.Base(class.File), filepath.Ext(class.File))
		if strings.EqualFold(base, "Actions") {
			return base
		}
	}
	if len(program.Classes) > 0 {
		return strings.TrimSuffix(filepath.Base(program.Classes[0].File), filepath.Ext(program.Classes[0].File))
	}
	return "Actions"
}

func javaVuserMappingStatement(program javaProgram) javaStatement {
	for _, methodName := range []string{"action", "init", "end"} {
		if method, ok := program.Methods[methodName]; ok {
			if len(method.Statements) > 0 {
				return method.Statements[0]
			}
			return javaStatement{File: method.File, StartLine: method.StartLine, EndLine: method.EndLine, Text: method.Name}
		}
	}
	for _, class := range program.Classes {
		for _, method := range class.Methods {
			if len(method.Statements) > 0 {
				return method.Statements[0]
			}
			return javaStatement{File: method.File, StartLine: method.StartLine, EndLine: method.EndLine, Text: method.Name}
		}
	}
	return javaStatement{File: "Actions.java", StartLine: 1, EndLine: 1, Text: "Java Vuser lifecycle"}
}

func attachJavaTruststoreArtifact(result *ConvertResult, files map[string]string) {
	if result == nil || result.Script == nil {
		return
	}
	truststore := findTruststoreArtifact(files)
	if truststore == "" {
		return
	}
	if result.Script.Config.JMS.TLS == nil {
		result.Script.Config.JMS.TLS = &dsl.JMSTLSConfig{}
	}
	if result.Script.Config.JMS.TLS.TrustStoreRef == "" {
		result.Script.Config.JMS.TLS.TrustStoreRef = filepath.Base(truststore)
		result.Warnings = append(result.Warnings, fmt.Sprintf("Java archive truststore %q was preserved as config.jms.tls.trust_store_ref; provision it as governed runtime material before replay.", filepath.Base(truststore)))
	}
}

func convertJavaStatement(result *ConvertResult, state *javaImportState, files map[string]string, phase string, statement javaStatement) error {
	text := javaNormalizeStatementText(statement.Text)
	if text == "" {
		return nil
	}
	matchStatement := statement
	matchStatement.Text = text
	if statement.Kind == "block" {
		if javaTransparentBlockHeader(statement.Header) {
			for _, child := range statement.Children {
				if err := convertJavaStatement(result, state, files, phase, child); err != nil {
					return err
				}
			}
			return nil
		}
		appendJavaReview(result, state, statement, phase, "unmapped_java_block", phase == "action")
		return nil
	}
	ctx := &javaMapperContext{
		result:    result,
		state:     state,
		files:     files,
		phase:     phase,
		statement: matchStatement,
		text:      text,
	}
	for _, mapper := range javaStatementMapperRegistry {
		handled, err := mapper.Map(ctx)
		if err != nil {
			return err
		}
		if handled {
			return nil
		}
	}
	return nil
}

func javaStatementLooksLikeDatabase(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "preparedstatement") ||
		strings.Contains(lower, "drivermanager") ||
		strings.Contains(lower, "executequery(") ||
		strings.Contains(lower, "executeupdate(") ||
		strings.Contains(lower, "preparestatement(") ||
		javaStatementIsSQLTransactionControl(text) ||
		strings.Contains(lower, ".setautocommit(false")
}

func javaStatementIsSQLTransactionControl(text string) bool {
	_, ok := javaSQLTransactionControlFunction(text)
	return ok
}

func javaNamedReviewReason(text string) string {
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "javax.ejb") || strings.Contains(lower, "jakarta.ejb"):
		return "ejb_review"
	case strings.Contains(lower, "org.omg.corba") || strings.Contains(lower, "corba.orb"):
		return "corba_review"
	case strings.Contains(lower, "naming.lookup") || strings.Contains(lower, "initialcontext.lookup") || strings.Contains(lower, "javax.rmi"):
		return "rmi_java_review"
	case strings.Contains(lower, "sapgui") || strings.Contains(lower, "com.sap"):
		return "sapgui_java_review"
	case strings.Contains(lower, "jacada"):
		return "jacada_java_review"
	case strings.Contains(lower, "oracle.apps") || strings.Contains(lower, "oracle.forms") || strings.Contains(lower, "oracle.nca"):
		return "oracle_nca_java_review"
	case strings.Contains(lower, "tuxedo") || strings.Contains(lower, "weblogic.wtc"):
		return "tuxedo_java_review"
	case strings.Contains(lower, "javax.xml.ws") || strings.Contains(lower, "soapmessage") || strings.Contains(lower, "soapconnection"):
		return "soap_java_review"
	default:
		return ""
	}
}

func javaTransparentBlockHeader(header string) bool {
	normalized := javaNormalizeBlockHeader(header)
	return strings.HasPrefix(normalized, "try") ||
		strings.HasPrefix(normalized, "catch") ||
		strings.HasPrefix(normalized, "finally") ||
		strings.HasPrefix(normalized, "if") ||
		strings.HasPrefix(normalized, "else") ||
		strings.HasPrefix(normalized, "for") ||
		strings.HasPrefix(normalized, "while") ||
		strings.HasPrefix(normalized, "switch") ||
		strings.HasPrefix(normalized, "synchronized")
}

func javaNormalizeBlockHeader(header string) string {
	header = javaBlockCommentRe.ReplaceAllString(header, " ")
	lines := strings.Split(header, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if idx := strings.Index(line, "//"); idx >= 0 {
			line = line[:idx]
		}
		line = strings.TrimSpace(line)
		if line != "" {
			kept = append(kept, line)
		}
	}
	return strings.ToLower(strings.TrimSpace(strings.Join(kept, " ")))
}

func isJavaInertStatement(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return true
	}
	if javaDeclarationOnlyRe.MatchString(trimmed) {
		return true
	}
	lower := strings.ToLower(trimmed)
	return strings.HasPrefix(lower, "return ") || lower == "return;" || lower == "return 0;" ||
		strings.HasPrefix(lower, "throw ") ||
		strings.HasPrefix(lower, "import ") || strings.HasPrefix(lower, "package ") ||
		strings.Contains(lower, "new hashtable") || strings.Contains(lower, "new properties") ||
		strings.Contains(lower, "initialcontext(") || strings.Contains(lower, "connectionfactory") ||
		strings.Contains(lower, ".createconnection(") || strings.Contains(lower, ".createsession(") ||
		strings.Contains(lower, ".createproducer(") || strings.Contains(lower, ".createconsumer(") ||
		strings.Contains(lower, ".start()") || strings.Contains(lower, ".close()")
}

func convertJavaEnvPut(result *ConvertResult, state *javaImportState, files map[string]string, statement javaStatement) bool {
	matches := javaEnvPutRe.FindStringSubmatch(statement.Text)
	if len(matches) != 2 {
		return false
	}
	args := splitJavaArgs(matches[1])
	if len(args) < 2 {
		return false
	}
	key := normalizeJavaJNDIKey(args[0])
	value, ok := state.env.eval(args[1], files)
	if !ok {
		appendJavaReview(result, state, statement, "init", "unresolved_jndi_value", false)
		state.configReview++
		return true
	}
	switch key {
	case "provider_url":
		result.Script.Config.JMS.BrokerURL = value
	case "security_principal", "username":
		if user, vpn, ok := strings.Cut(value, "@"); ok {
			if result.Script.Config.JMS.Properties == nil {
				result.Script.Config.JMS.Properties = map[string]string{}
			}
			result.Script.Config.JMS.Properties["recorded_username"] = user
			if result.Script.Config.JMS.MessageVPN == "" {
				result.Script.Config.JMS.MessageVPN = vpn
			}
		} else if value != "" {
			if result.Script.Config.JMS.Properties == nil {
				result.Script.Config.JMS.Properties = map[string]string{}
			}
			result.Script.Config.JMS.Properties["recorded_username"] = value
		}
		result.Script.Config.JMS.Username = "{{secret.jms_username}}"
		result.Warnings = append(result.Warnings, fmt.Sprintf("%s line %d: JMS username was replaced with {{secret.jms_username}}.", statement.File, statement.StartLine))
	case "security_credentials", "password":
		result.Script.Config.JMS.Password = "{{secret.jms_password}}"
		result.Warnings = append(result.Warnings, fmt.Sprintf("%s line %d: literal JMS credential was replaced with {{secret.jms_password}}.", statement.File, statement.StartLine))
	case "solace_jms_vpn", "message_vpn":
		result.Script.Config.JMS.MessageVPN = value
	case "trust_store", "truststore", "ssl_trust_store", "ssl_truststore":
		if result.Script.Config.JMS.TLS == nil {
			result.Script.Config.JMS.TLS = &dsl.JMSTLSConfig{}
		}
		result.Script.Config.JMS.TLS.TrustStoreRef = value
		result.Warnings = append(result.Warnings, fmt.Sprintf("%s line %d: JMS truststore %q was preserved as config.jms.tls.trust_store_ref; provision it as a governed secret/file before replay.", statement.File, statement.StartLine, filepath.Base(value)))
	default:
		if result.Script.Config.JMS.Properties == nil {
			result.Script.Config.JMS.Properties = map[string]string{}
		}
		result.Script.Config.JMS.Properties[key] = value
	}
	appendJavaMapping(result, statement, "config.jms", key, "config", "converted", "Mapped Java JNDI environment property into JMS config.", SourceFunctionSupportExecutable)
	return true
}

func normalizeJavaJNDIKey(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.Trim(raw, `"`)
	replacer := strings.NewReplacer("InitialContext.", "", "Context.", "", "SupportedProperty.", "", "javax.naming.", "", "java.naming.", "", "com.solacesystems.jms.", "", "SOLACE_JMS_", "")
	raw = replacer.Replace(raw)
	raw = strings.ToLower(raw)
	raw = strings.TrimPrefix(raw, "solace_jms_")
	raw = strings.NewReplacer(".", "_", "-", "_").Replace(raw)
	switch raw {
	case "provider_url", "initial_context_factory_provider_url":
		return "provider_url"
	case "security_principal":
		return "security_principal"
	case "security_credentials":
		return "security_credentials"
	case "vpn", "message_vpn":
		return "message_vpn"
	default:
		return raw
	}
}

func convertJavaMessageProperty(result *ConvertResult, state *javaImportState, files map[string]string, statement javaStatement) bool {
	matches := javaSetMessagePropertyRe.FindStringSubmatch(statement.Text)
	if len(matches) != 3 {
		return false
	}
	msg := state.env.Messages[matches[1]]
	if msg.Properties == nil {
		msg.Properties = map[string]string{}
	}
	args := splitJavaArgs(matches[2])
	if len(args) >= 2 {
		key, ok1 := state.env.eval(args[0], files)
		value, ok2 := state.env.eval(args[1], files)
		if !ok2 {
			trimmed := strings.TrimSpace(args[1])
			if strings.EqualFold(trimmed, "true") || strings.EqualFold(trimmed, "false") {
				value = strings.ToLower(trimmed)
				ok2 = true
			}
		}
		if ok1 && ok2 {
			msg.Properties[key] = value
			state.env.Messages[matches[1]] = msg
			appendJavaMapping(result, statement, "", key, "headers", "converted", "Mapped TextMessage string property to JMS headers.", SourceFunctionSupportExecutable)
			return true
		}
	}
	appendJavaReview(result, state, statement, "action", "unresolved_message_property", false)
	return true
}

// javaLRToParsedFunction turns a matched Java `lr.<method>(args)` statement into a
// synthetic ParsedFunction so the Java path can reuse the hardened C lr_*
// registry + family router (lookupSourceFunction / convertSourceFunctionAction)
// instead of the old coarse name-substring mapping. The LR Java API is snake_case
// and 1:1 with the C functions, so lr.start_transaction -> lr_start_transaction.
func javaLRToParsedFunction(statement javaStatement, phase string) (ParsedFunction, bool) {
	matches := javaLRCallRe.FindStringSubmatch(statement.Text)
	if len(matches) != 3 {
		return ParsedFunction{}, false
	}
	fn := ParsedFunction{
		Name:       "lr_" + javaLRFunctionName(matches[1]),
		Phase:      phase,
		Filename:   statement.File,
		LineNumber: statement.StartLine,
		EndLine:    statement.EndLine,
		Args:       map[string]string{},
		Headers:    map[string]string{},
	}
	rawArgs := splitJavaArgs(matches[2])
	for i, raw := range rawArgs {
		fn.RawArgs = append(fn.RawArgs, strings.TrimSpace(raw))
		fn.Args[fmt.Sprintf("arg%d", i+1)] = javaArgToSourceArg(raw)
	}
	fn.StepName = javaLRStepName(rawArgs)
	// lr.think_time(n): expose the seconds value under the key the shared
	// ExtractThinkTimeSeconds() helper reads (the C parser sets it directly).
	if fn.Name == "lr_think_time" && len(rawArgs) > 0 {
		fn.Args["seconds"] = strings.TrimSpace(cleanConfigValue(rawArgs[0]))
	}
	return fn, true
}

// routeJavaLRFunction classifies one lr.* call through the shared registry and
// applies stateful transaction/think-time parity. Unknown lr methods are
// preserved for review (never custom_call).
func routeJavaLRFunction(result *ConvertResult, state *javaImportState, phase string, statement javaStatement, fn ParsedFunction) {
	if _, ok := lookupSourceFunction(fn.Name); !ok {
		appendJavaLRReview(result, state, statement, phase, fn)
		return
	}
	state.recognizedLRActions++

	// Stateful parity with the C converter loop (converter.go): feed the
	// transaction counters and think-time distribution so Java transactions
	// behave like C transactions rather than being inert markers.
	switch fn.Name {
	case "lr_start_transaction":
		state.currentTransaction = fn.StepName
		state.currentSubTransaction = ""
		result.Stats.Transactions++
		result.Stats.TransactionMarkers++
	case "lr_end_transaction":
		result.Stats.TransactionMarkers++
		state.currentTransaction = ""
		state.currentSubTransaction = ""
	case "lr_start_sub_transaction":
		state.currentSubTransaction = fn.StepName
		result.Stats.TransactionMarkers++
	case "lr_end_sub_transaction", "lr_end_sub_trans":
		result.Stats.TransactionMarkers++
		state.currentSubTransaction = ""
	case "lr_think_time":
		if sec, ok := ExtractThinkTimeSeconds(fn); ok {
			state.thinkTimes = append(state.thinkTimes, sec)
			result.Stats.ThinkTimes++
		}
	}

	action, mode, _ := convertSourceFunctionAction(fn)
	applyJavaTransactionMetadata(&action, state)
	appendJavaLRAction(result, statement, phase, fn, action, mode, sourceSupportNotes(fn, mode))
}

// applyJavaTransactionMetadata stamps the open source transaction onto an action
// so the dashboard can group Java actions like the C importer groups requests.
func applyJavaTransactionMetadata(action *dsl.Action, state *javaImportState) {
	if state.currentTransaction == "" {
		return
	}
	if action.Metadata == nil {
		action.Metadata = map[string]any{}
	}
	action.Metadata["source_transaction"] = state.currentTransaction
	if state.currentSubTransaction != "" {
		action.Metadata["source_subtransaction"] = state.currentSubTransaction
	}
}

// appendJavaLRAction records the action plus a SourceReview mapping keyed by the
// real lr_ function name (not the generic "java_statement") so per-method
// coverage is visible and the completeness test can assert each method's mode.
func appendJavaLRAction(result *ConvertResult, statement javaStatement, phase string, fn ParsedFunction, action dsl.Action, mode SourceFunctionSupportMode, notes string) {
	index := actionCountForPhase(result.Script, phase)
	appendSourceReviewMapping(result, SourceReviewMapping{
		SourceFile:  statement.File,
		StartLine:   statement.StartLine,
		EndLine:     statement.EndLine,
		Function:    fn.Name,
		TargetPath:  sourceReviewActionPath(phase, index),
		TargetName:  action.Name,
		TargetKind:  "action",
		Status:      statusForSupportMode(mode),
		SupportMode: string(mode),
		Notes:       notes,
	})
	appendActionForPhase(result.Script, phase, action)
	result.Stats.ConvertedActions++
	recordSourceFunctionSupport(result, mode)
}

// appendJavaLRReview preserves an unrecognized lr.* method for manual review
// (keyed by its name) rather than emitting a custom_call stub.
func appendJavaLRReview(result *ConvertResult, state *javaImportState, statement javaStatement, phase string, fn ParsedFunction) {
	action := javaSourceReviewAction(statement, phase, "unrecognized_lr_method")
	index := actionCountForPhase(result.Script, phase)
	appendSourceReviewMapping(result, SourceReviewMapping{
		SourceFile:  statement.File,
		StartLine:   statement.StartLine,
		EndLine:     statement.EndLine,
		Function:    fn.Name,
		TargetPath:  sourceReviewActionPath(phase, index),
		TargetName:  action.Name,
		TargetKind:  "action",
		Status:      "review_only",
		SupportMode: string(SourceFunctionSupportReviewOnly),
		Notes:       "Unrecognized lr.* method preserved for manual review.",
	})
	appendActionForPhase(result.Script, phase, action)
	result.Stats.ConvertedActions++
	recordSourceFunctionSupport(result, SourceFunctionSupportReviewOnly)
	if phase == "action" {
		state.unrecognizedAction++
	}
}

func javaLRFunctionName(name string) string {
	var b strings.Builder
	for i, r := range name {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b.WriteByte('_')
		}
		b.WriteRune(r)
	}
	return strings.ToLower(b.String())
}

// javaArgToSourceArg renders one Java lr argument as a source-arg string: string
// literals are unquoted + parameter-converted, a nested lr.eval_string("{x}") is
// unwrapped to {{var.x}}, and everything else (variables, enum constants like
// lr.AUTO) passes through trimmed.
func javaArgToSourceArg(raw string) string {
	raw = strings.TrimSpace(raw)
	if value, ok := parseJavaStringLiteral(raw); ok {
		return convertParamReferences(value)
	}
	if inner, ok := unwrapJavaEvalString(raw); ok {
		return inner
	}
	return raw
}

func unwrapJavaEvalString(raw string) (string, bool) {
	m := javaLRCallRe.FindStringSubmatch(strings.TrimSpace(raw))
	if len(m) != 3 || javaLRFunctionName(m[1]) != "eval_string" {
		return "", false
	}
	inner := splitJavaArgs(m[2])
	if len(inner) == 0 {
		return "", false
	}
	if value, ok := parseJavaStringLiteral(inner[0]); ok {
		return convertParamReferences(value), true
	}
	return "", false
}

// applyJavaThinkTimeDistribution turns the collected lr.think_time values into a
// normal think-time distribution on the script config, mirroring the C converter.
func applyJavaThinkTimeDistribution(result *ConvertResult, state *javaImportState) {
	if result.Script == nil || len(state.thinkTimes) == 0 {
		return
	}
	mean, std := computeThinkTimeStats(state.thinkTimes)
	result.Script.Config.ThinkTime = dsl.ThinkTimeConfig{
		Type:   "normal",
		MeanMs: mean * 1000,
		StdMs:  std * 1000,
		MinMs:  math.Max(0, (mean-2*std)*1000),
		MaxMs:  (mean + 2*std) * 1000,
	}
}

// javaLRStepName returns the first string-literal argument (the transaction/step
// name), skipping enum constants like lr.AUTO so they never become a step name.
func javaLRStepName(rawArgs []string) string {
	for _, raw := range rawArgs {
		if value, ok := parseJavaStringLiteral(raw); ok {
			return value
		}
	}
	return ""
}

func convertJavaJMSAction(state *javaImportState, files map[string]string, statement javaStatement) (dsl.Action, bool) {
	text := strings.TrimSpace(statement.Text)
	if javaCommitCallRe.MatchString(text) {
		return javaJMSControlAction(statement, "jms_commit"), true
	}
	if javaRollbackCallRe.MatchString(text) {
		return javaJMSControlAction(statement, "jms_rollback"), true
	}
	if javaAckCallRe.MatchString(text) {
		return javaJMSControlAction(statement, "jms_ack"), true
	}
	producerName := ""
	if matches := javaSendOnProducerRe.FindStringSubmatch(text); len(matches) == 3 {
		producerName = strings.TrimSpace(matches[1])
	}
	if matches := javaSendCallRe.FindStringSubmatch(text); len(matches) == 2 {
		args := splitJavaArgs(matches[1])
		target := ""
		msgName := ""
		if len(args) == 1 {
			msgName = strings.TrimSpace(args[0])
			target = state.env.Producers[producerName]
		} else if len(args) >= 2 {
			target = state.resolveDestination(args[0], files)
			msgName = strings.TrimSpace(args[len(args)-1])
		}
		msg := state.env.Messages[msgName]
		action := javaBaseJMSAction(statement, "jms_publish")
		action.Target = convertutil.FirstNonEmpty(target, "{{env.JMS_DESTINATION}}")
		action.Body = msg.Body
		action.Headers = cloneStringMap(msg.Properties)
		if len(action.Headers) == 0 {
			action.Headers = nil
		}
		return action, true
	}
	if matches := javaReceiveCallRe.FindStringSubmatch(text); len(matches) == 2 {
		action := javaBaseJMSAction(statement, "jms_consume")
		action.Target = "{{env.JMS_DESTINATION}}"
		if timeout, ok := javaReceiveTimeout(matches[1]); ok {
			action.Params = map[string]any{"timeout_ms": timeout}
		}
		return action, true
	}
	return dsl.Action{}, false
}

func (state *javaImportState) resolveDestination(expr string, files map[string]string) string {
	expr = strings.TrimSpace(expr)
	if value, ok := state.env.Destinations[expr]; ok {
		return value
	}
	if value, ok := state.env.eval(expr, files); ok {
		return value
	}
	return ""
}

func javaReceiveTimeout(args string) (int, bool) {
	args = strings.TrimSpace(args)
	if args == "" {
		return 0, false
	}
	if value, err := strconv.Atoi(args); err == nil {
		return value, true
	}
	return 0, false
}

func javaJMSControlAction(statement javaStatement, actionType string) dsl.Action {
	return javaBaseJMSAction(statement, actionType)
}

func javaBaseJMSAction(statement javaStatement, actionType string) dsl.Action {
	return dsl.Action{
		Name: sanitizeName(convertutil.FirstNonEmpty(statementName(statement), actionType)),
		Type: actionType,
		Metadata: map[string]any{
			"source":      "java_jms_idiom",
			"source_file": statement.File,
			"source_line": statement.StartLine,
		},
	}
}

func statementName(statement javaStatement) string {
	text := strings.TrimSpace(statement.Text)
	if len(text) > 60 {
		text = text[:60]
	}
	return text
}

func appendJavaAction(result *ConvertResult, statement javaStatement, phase string, action dsl.Action, mode SourceFunctionSupportMode, notes string) {
	index := actionCountForPhase(result.Script, phase)
	appendJavaMapping(result, statement, sourceReviewActionPath(phase, index), action.Name, "action", statusForSupportMode(mode), notes, mode)
	appendActionForPhase(result.Script, phase, action)
	result.Stats.ConvertedActions++
	recordSourceFunctionSupport(result, mode)
}

func appendJavaReview(result *ConvertResult, state *javaImportState, statement javaStatement, phase string, reason string, actionPhase bool) {
	action := javaSourceReviewAction(statement, phase, reason)
	appendJavaAction(result, statement, phase, action, SourceFunctionSupportReviewOnly, "Java statement was preserved for manual review.")
	if actionPhase {
		state.unrecognizedAction++
	}
}

func javaSourceReviewAction(statement javaStatement, phase string, reason string) dsl.Action {
	return dsl.Action{
		Name: sanitizeName(convertutil.FirstNonEmpty(statementName(statement), "Java review")),
		Type: "source_review",
		Params: map[string]any{
			"function": "java_statement",
			"args":     []any{strings.TrimSpace(statement.Text)},
		},
		Metadata: map[string]any{
			"source":        "java_vuser",
			"review_reason": reason,
			"source_file":   statement.File,
			"source_line":   statement.StartLine,
			"phase":         phase,
		},
	}
}

func appendJavaMapping(result *ConvertResult, statement javaStatement, targetPath string, targetName string, targetKind string, status string, notes string, mode SourceFunctionSupportMode) {
	appendSourceReviewMapping(result, SourceReviewMapping{
		SourceFile:  statement.File,
		StartLine:   statement.StartLine,
		EndLine:     statement.EndLine,
		Function:    "java_statement",
		TargetPath:  targetPath,
		TargetName:  targetName,
		TargetKind:  targetKind,
		Status:      status,
		SupportMode: string(mode),
		Notes:       notes,
	})
}
