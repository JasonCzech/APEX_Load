package enterprisearchive

import (
	"fmt"
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

const javaVuserRunnerRequirement = "java_vuser_jvm_runner"

type javaImportState struct {
	env                javaStringEnv
	unrecognizedAction int
	jmsActions         int
	configReview       int
	statementCount     int
}

func ConvertJava(files map[string]string, opts ConvertOptions, detected DetectedProtocol) (*ConvertResult, error) {
	program, parseWarnings := parseJavaProgram(files)
	result := &ConvertResult{
		DetectedProtocol: &detected,
		Warnings:         append([]string(nil), detected.Warnings...),
		Script: &dsl.Script{
			Name:          convertutil.FirstNonEmpty(opts.ScriptName, "Converted_Java_Vuser_JMS_Script"),
			Version:       "1.0.0",
			Protocol:      "jms-solace",
			GeneratedBy:   "apexload-enterprise-script-archive-java-importer",
			GeneratedFrom: "enterprise_script_archive",
			Config: dsl.ScriptConfig{
				JMS: dsl.JMSConfig{
					Provider:        "solace",
					DestinationType: "queue",
					TimeoutMs:       5000,
					Mock:            true,
				},
			},
			Actions: make([]dsl.Action, 0),
		},
	}
	result.Warnings = append(result.Warnings, parseWarnings...)

	if len(program.Methods) == 0 {
		result.Script = nil
		reviewDetected := markDetectedReviewOnly(detected, "No parseable Java lifecycle methods were found.")
		result.DetectedProtocol = &reviewDetected
		result.ReviewArtifact = NewReviewArtifact(reviewDetected, files, nil, map[string]any{"reason": "no_parseable_java_methods"})
		result.Warnings = append(result.Warnings, "Java Vuser import was preserved for review because no lifecycle methods were parseable.")
		return result, nil
	}

	state := &javaImportState{env: newJavaStringEnv()}
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
	attachJavaTruststoreArtifact(result, files)

	if opts.AllowJavaVuserRunner && (state.jmsActions == 0 || state.unrecognizedAction > 0 || state.configReview > 0) {
		return convertJavaVuserRunnerDraft(files, opts, detected, program, result.Warnings, state), nil
	}

	if state.jmsActions == 0 || state.unrecognizedAction > 0 || state.configReview > 0 {
		detected = markDetectedReviewOnly(detected, "")
		detected.APEXLoadProtocol = "jms"
		detected.RequiredEvidence = appendUnique(detected.RequiredEvidence, "Operator review of unmapped Java statements", "JMS broker configuration verification")
		result.DetectedProtocol = &detected
		result.ReviewArtifact = NewReviewArtifact(detected, files, nil, map[string]any{
			"reason":                         "partial_java_mapping",
			"jms_actions":                    state.jmsActions,
			"unrecognized_action_statements": state.unrecognizedAction,
			"configuration_review_items":     state.configReview,
		})
		result.Warnings = append(result.Warnings, "Java Vuser import produced a partial JMS draft and remains review-only until unmapped statements are resolved.")
	} else {
		detected.ImportMode = ImportModeExecutable
		detected.APEXLoadProtocol = "jms"
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
		},
		Metadata: map[string]any{
			"review_required":    true,
			"review_reason":      "java_vuser_sidecar_runner_required",
			"runner_requirement": javaVuserRunnerRequirement,
			"source_protocol":    "java_vuser",
			"sidecar_transport":  "stdio_json_lines",
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
		"sidecar_transport":              "stdio_json_lines",
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
	if state.env.assignDestination(text, files) || state.env.assignLookupDestination(text, files) || state.env.assignProducer(text, files) || state.env.assignMessage(text, files) || state.env.assignString(text, files) {
		appendJavaMapping(result, matchStatement, "", "", "state", "converted", "Mapped Java symbolic state used by downstream JMS actions.", SourceFunctionSupportExecutable)
		return nil
	}
	if isJavaInertStatement(text) {
		return nil
	}
	if convertJavaEnvPut(result, state, files, matchStatement) {
		return nil
	}
	if convertJavaMessageProperty(result, state, files, matchStatement) {
		return nil
	}
	if action, ok := convertJavaLRCall(matchStatement); ok {
		appendJavaAction(result, matchStatement, phase, action, SourceFunctionSupportExecutable, "Mapped Java lr.* call to source-compatible engine action.")
		return nil
	}
	if action, ok := convertJavaJMSAction(state, files, matchStatement); ok {
		appendJavaAction(result, matchStatement, phase, action, SourceFunctionSupportExecutable, "Mapped Java JMS API idiom to native JMS action.")
		state.jmsActions++
		return nil
	}
	appendJavaReview(result, state, statement, phase, "unmapped_java_statement", phase == "action")
	return nil
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
		strings.HasPrefix(normalized, "switch")
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

func convertJavaLRCall(statement javaStatement) (dsl.Action, bool) {
	matches := javaLRCallRe.FindStringSubmatch(statement.Text)
	if len(matches) != 3 {
		return dsl.Action{}, false
	}
	name := "lr_" + javaLRFunctionName(matches[1])
	args := javaArgsToAny(splitJavaArgs(matches[2]))
	actionType := "source_review"
	switch {
	case strings.Contains(name, "transaction"):
		actionType = "source_transaction"
	case strings.Contains(name, "save") || strings.Contains(name, "param"):
		actionType = "source_parameter"
	case strings.Contains(name, "message"):
		actionType = "source_message"
	case strings.Contains(name, "timer"):
		actionType = "source_timer"
	case strings.Contains(name, "think_time") || strings.Contains(name, "exit") || strings.Contains(name, "abort"):
		actionType = "source_runtime"
	}
	return dsl.Action{
		Name: sanitizeName(convertutil.FirstNonEmpty(javaFirstStringArg(args), name)),
		Type: actionType,
		Params: map[string]any{
			"function": name,
			"args":     args,
		},
		Metadata: map[string]any{
			"source":      "java_lr_function",
			"source_file": statement.File,
			"source_line": statement.StartLine,
		},
	}, true
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

func javaArgsToAny(args []string) []any {
	out := make([]any, 0, len(args))
	for _, arg := range args {
		if value, ok := parseJavaStringLiteral(arg); ok {
			out = append(out, convertParamReferences(value))
			continue
		}
		out = append(out, strings.TrimSpace(arg))
	}
	return out
}

func javaFirstStringArg(args []any) string {
	for _, arg := range args {
		if value, ok := arg.(string); ok && strings.TrimSpace(value) != "" && !strings.ContainsAny(value, "()") {
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
