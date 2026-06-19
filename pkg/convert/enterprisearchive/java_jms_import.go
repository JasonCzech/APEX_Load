package enterprisearchive

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/dsl"
)

var (
	javaLrCallRe         = regexp.MustCompile(`\blr\.([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	javaXMLReplaceEvalRe = regexp.MustCompile(`(?s)\.replace(?:All)?\(\s*"([^"]+)"\s*,\s*lr\.eval_string\(\s*"\{([^"}]+)\}"\s*\)\s*\)`)
)

func isJavaProtocolsProject(projectConfig *ProjectConfig) bool {
	if projectConfig == nil {
		return false
	}
	if profile, _, ok := profileForProjectConfig(projectConfig); ok && profile.Canonical == "java_jms_solace" {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(projectConfig.ScriptType), "Java_protocols") {
		return true
	}
	for _, sourceFile := range projectConfig.SourceActionFiles {
		if strings.EqualFold(sourceFile.Language, "java") || strings.HasSuffix(strings.ToLower(sourceFile.Name), ".java") {
			return true
		}
	}
	return false
}

func convertJavaJMSZip(files map[string]string, opts ConvertOptions, projectConfig *ProjectConfig, projectFileName string, projectSections map[string]map[string]string, runtimeFileName string, parameterFileNames []string) (*ConvertResult, error) {
	detected := DetectProtocol(files, projectConfig, nil)
	if detected.CanonicalSourceProtocol != "java_jms_solace" {
		profile := javaJMSSolaceProfile()
		detected.CanonicalSourceProtocol = profile.Canonical
		detected.APEXLoadProtocol = profile.APEXLoad
		detected.DisplayName = profile.DisplayName
		detected.Bundle = profile.Bundle
		detected.ImportMode = profile.Mode
		detected.RequiredEvidence = appendUnique(detected.RequiredEvidence, profile.Evidence...)
		detected.Warnings = appendUnique(detected.Warnings, profile.Warnings...)
	}
	detected.ImportMode = ImportModeExternalRunnerRequired
	detected.RunnerRequirement = "Real Solace JMS replay requires an approved adapter or governed sidecar runner before this import can be promoted."
	detected.RequiredEvidence = appendUnique(detected.RequiredEvidence, "Approved Solace JMS adapter or governed sidecar runner policy")

	result := &ConvertResult{
		Script: &dsl.Script{
			Version:       "1.0.0",
			Name:          convertutil.FirstNonEmpty(opts.ScriptName, "Converted_Java_JMS_Solace"),
			Protocol:      "jms-solace",
			GeneratedBy:   "apexload-enterprise-script-archive-importer",
			GeneratedFrom: "enterprise_script_archive",
			Config: dsl.ScriptConfig{
				JMS: dsl.JMSConfig{
					Provider:        "solace",
					BrokerURL:       sourceParameterTemplate("serverURL"),
					MessageVPN:      sourceParameterTemplate("vpnName"),
					Username:        sourceParameterTemplate("userNameJNDI"),
					Password:        sourceParameterTemplate("userPasswordJNDI"),
					Destination:     sourceParameterTemplate("requestQueueName"),
					DestinationType: "queue",
					Acknowledgement: "auto",
					DeliveryMode:    "persistent",
					TimeoutMs:       5000,
					Mock:            false,
					Properties: map[string]string{
						"initial_context_factory": sourceParameterTemplate("initialContext"),
						"connection_factory_jndi": sourceParameterTemplate("connectionFactoryJNDI"),
					},
				},
			},
		},
		DetectedProtocol: &detected,
		Warnings:         append([]string(nil), detected.Warnings...),
	}

	javaSources := orderedJavaSourceActionFiles(projectConfig, files)
	javaContent := joinSourceFiles(files, javaSources)
	javaParsedActions := javaSourcesAsParsedActions(javaSources, files)
	result.ReviewArtifact = NewReviewArtifact(detected, files, javaParsedActions, map[string]any{
		"source_script_type": "Java_protocols",
		"source_profile":     "Java JMS/Solace",
		"promotion_gate":     "real_solace_adapter_or_governed_sidecar",
	})
	result.Stats.TotalFunctions = len(javaLrCallRe.FindAllStringSubmatch(javaContent, -1))
	result.Stats.NativeFunctions = result.Stats.TotalFunctions

	parameterSections := make(map[string]map[string]map[string]string)
	for _, parameterFileName := range parameterFileNames {
		content, ok := findFile(files, parameterFileName)
		if !ok {
			continue
		}
		parsed := ParseParameterFile(parameterFileName, content, files)
		result.Script.Parameters = append(result.Script.Parameters, parsed.Parameters...)
		result.Script.DataSources = append(result.Script.DataSources, parsed.DataSources...)
		result.Warnings = append(result.Warnings, parsed.Warnings...)
		if len(parsed.Sections) > 0 {
			parameterSections[filepath.Base(parameterFileName)] = parsed.Sections
		}
	}

	bodyFileName, body := findJavaJMSXMLBody(files)
	if body == "" {
		result.Warnings = append(result.Warnings, "Java JMS/Solace archive did not include a parseable XML sidecar body; publish action body requires review.")
	}
	body = applyJavaXMLReplacements(body, javaContent)
	if truststore := findTruststoreArtifact(files); truststore != "" {
		result.Script.Config.JMS.Properties["truststore"] = filepath.Base(truststore)
	}

	action := dsl.Action{
		Name:    sanitizeName(convertutil.FirstNonEmpty(javaPrimaryActionName(javaSources), "Java_JMS_Solace_Publish")),
		Type:    "jms_publish",
		Target:  sourceParameterTemplate("requestQueueName"),
		Body:    convertParamReferences(unescapeSourceCString(body)),
		Headers: map[string]string{"Content-Type": "application/xml"},
		Params: map[string]any{
			"destination": sourceParameterTemplate("requestQueueName"),
		},
		Metadata: map[string]any{
			"source_function":       "java_jms_publish",
			"source_protocol":       "Java_protocols",
			"source_body_file":      filepath.Base(bodyFileName),
			"Solace_JMS_PROP_ISXML": "true",
		},
	}
	for key, value := range javaJMSMessageProperties(javaContent) {
		action.Metadata[key] = convertParamReferences(value)
	}
	if _, ok := action.Metadata["subscriberNumber"]; !ok {
		action.Metadata["subscriberNumber"] = sourceParameterTemplate("ctn")
	}
	if _, ok := action.Metadata["container"]; !ok {
		action.Metadata["container"] = sourceParameterTemplate("containerName")
	}
	if _, ok := action.Metadata["scheduledStartTime"]; !ok {
		action.Metadata["scheduledStartTime"] = sourceParameterTemplate("scheduledStartTime")
	}
	if _, ok := action.Metadata["guid"]; !ok {
		action.Metadata["guid"] = sourceParameterTemplate("guid")
	}
	result.Script.Actions = append(result.Script.Actions, action)
	result.Stats.ConvertedActions = 1
	result.Stats.RawRequestActions = 1

	applyKnownParameterReferences(result.Script)
	result.SourceReview = BuildSourceReview(files, javaSourceReviewMappings(javaSources))
	attachSourceMetadata(result.Script, result, projectFileName, projectSections, projectConfig, files, runtimeFileName, parameterSections, parameterFileNames, nil)
	attachImportFidelityReport(result, files, projectConfig, javaParsedActions)
	return result, nil
}

func orderedJavaSourceActionFiles(projectConfig *ProjectConfig, files map[string]string) []string {
	candidates := make([]string, 0)
	if projectConfig != nil {
		for _, sourceFile := range projectConfig.SourceActionFiles {
			if strings.EqualFold(sourceFile.Language, "java") || strings.HasSuffix(strings.ToLower(sourceFile.Name), ".java") {
				candidates = append(candidates, sourceFile.Name)
			}
		}
	}
	if len(candidates) == 0 {
		candidates = []string{"vuser_init.java", "Actions.java", "vuser_end.java"}
	}
	ordered := make([]string, 0, len(candidates))
	seen := map[string]struct{}{}
	for _, candidate := range candidates {
		if _, ok := findFile(files, candidate); !ok {
			continue
		}
		key := strings.ToLower(filepath.Base(candidate))
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		ordered = append(ordered, candidate)
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		return javaPhaseOrder(sourceActionPhase(ordered[i])) < javaPhaseOrder(sourceActionPhase(ordered[j]))
	})
	return ordered
}

func javaPhaseOrder(phase string) int {
	switch normalizeSourcePhase(phase) {
	case "init":
		return 0
	case "teardown":
		return 2
	default:
		return 1
	}
}

func joinSourceFiles(files map[string]string, names []string) string {
	var b strings.Builder
	for _, name := range names {
		content, ok := findFile(files, name)
		if !ok {
			continue
		}
		b.WriteString("\n// ")
		b.WriteString(name)
		b.WriteByte('\n')
		b.WriteString(content)
	}
	return b.String()
}

func findJavaJMSXMLBody(files map[string]string) (string, string) {
	names := make([]string, 0)
	for name := range files {
		if strings.HasSuffix(strings.ToLower(name), ".xml") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	bestName := ""
	bestBody := ""
	for _, name := range names {
		content := strings.TrimSpace(files[name])
		lowerBase := strings.ToLower(filepath.Base(name))
		if content == "" || strings.Contains(lowerBase, "bookmark") || strings.Contains(lowerBase, "breakpoint") || strings.Contains(lowerBase, "metadata") {
			continue
		}
		if !strings.Contains(content, "<") {
			continue
		}
		if len(content) > len(bestBody) {
			bestName = name
			bestBody = content
		}
	}
	return bestName, bestBody
}

func applyJavaXMLReplacements(body, javaContent string) string {
	for _, match := range javaXMLReplaceEvalRe.FindAllStringSubmatch(javaContent, -1) {
		if len(match) != 3 {
			continue
		}
		body = strings.ReplaceAll(body, match[1], sourceParameterTemplate(match[2]))
	}
	return body
}

func javaJMSMessageProperties(javaContent string) map[string]string {
	props := map[string]string{}
	for _, args := range javaMethodCallArgs(javaContent, "setStringProperty") {
		if len(args) < 2 {
			continue
		}
		props[cleanConfigValue(args[0])] = convertJavaExpressionToTemplate(args[1])
	}
	for _, args := range javaMethodCallArgs(javaContent, "setBooleanProperty") {
		if len(args) < 2 {
			continue
		}
		props[cleanConfigValue(args[0])] = strings.TrimSpace(args[1])
	}
	return props
}

func javaMethodCallArgs(content, method string) [][]string {
	needle := "." + method + "("
	var out [][]string
	offset := 0
	for {
		index := strings.Index(content[offset:], needle)
		if index < 0 {
			return out
		}
		start := offset + index + len(needle)
		end := scanBalancedCallEnd(content, start)
		if end < 0 {
			offset = start
			continue
		}
		out = append(out, splitCFunctionArgs(content[start:end]))
		offset = end + 1
	}
}

func scanBalancedCallEnd(content string, start int) int {
	depth := 1
	inString := false
	escaped := false
	for i := start; i < len(content); i++ {
		c := content[i]
		switch {
		case escaped:
			escaped = false
		case inString && c == '\\':
			escaped = true
		case c == '"':
			inString = !inString
		case !inString && c == '(':
			depth++
		case !inString && c == ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func convertJavaExpressionToTemplate(value string) string {
	value = strings.TrimSpace(value)
	if param := javaEvalStringParameter(value); param != "" {
		return sourceParameterTemplate(param)
	}
	if strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"") {
		return cleanConfigValue(value)
	}
	value = strings.ReplaceAll(value, "lr.eval_string", "lr_eval_string")
	evalRe := regexp.MustCompile(`lr_eval_string\(\s*"\{([^"}]+)\}"\s*\)`)
	value = evalRe.ReplaceAllStringFunc(value, func(token string) string {
		match := evalRe.FindStringSubmatch(token)
		if len(match) != 2 {
			return token
		}
		return sourceParameterTemplate(match[1])
	})
	value = strings.ReplaceAll(value, "+", "")
	value = strings.ReplaceAll(value, "\"", "")
	value = strings.Join(strings.Fields(value), "")
	if value == "" {
		return ""
	}
	return convertParamReferences(value)
}

func javaEvalStringParameter(value string) string {
	re := regexp.MustCompile(`^lr\.eval_string\(\s*"\{([^"}]+)\}"\s*\)$`)
	match := re.FindStringSubmatch(strings.TrimSpace(value))
	if len(match) != 2 {
		return ""
	}
	return match[1]
}

func sourceParameterTemplate(name string) string {
	canonical := canonicalSourceParameterName(name)
	if canonical == "" {
		return ""
	}
	return "{{param." + canonical + "}}"
}

func findTruststoreArtifact(files map[string]string) string {
	names := make([]string, 0)
	for name := range files {
		if strings.Contains(strings.ToLower(filepath.Base(name)), "truststore") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return ""
	}
	return names[0]
}

func javaPrimaryActionName(javaSources []string) string {
	for _, name := range javaSources {
		if normalizeSourcePhase(sourceActionPhase(name)) == "action" {
			return strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))
		}
	}
	return ""
}

func javaSourceReviewMappings(javaSources []string) []SourceReviewMapping {
	mappings := make([]SourceReviewMapping, 0, len(javaSources))
	for index, name := range javaSources {
		mappings = append(mappings, SourceReviewMapping{
			SourceFile:  name,
			StartLine:   1,
			TargetPath:  fmt.Sprintf("source_files[%d]", index),
			TargetName:  filepath.Base(name),
			TargetKind:  "source",
			Status:      "converted",
			Notes:       "Java lifecycle source was preserved and mapped into the Java JMS/Solace import lane.",
			SupportMode: string(SourceFunctionSupportExecutable),
		})
	}
	return mappings
}

func javaSourcesAsParsedActions(javaSources []string, files map[string]string) []*ParsedAction {
	actions := make([]*ParsedAction, 0, len(javaSources))
	for _, name := range javaSources {
		content, ok := findFile(files, name)
		if !ok {
			continue
		}
		functions := make([]ParsedFunction, 0)
		for _, match := range javaLrCallRe.FindAllStringSubmatchIndex(content, -1) {
			line := 1 + strings.Count(content[:match[0]], "\n")
			fnName := "lr." + content[match[2]:match[3]]
			functions = append(functions, ParsedFunction{
				Name:       fnName,
				Filename:   name,
				Phase:      sourceActionPhase(name),
				LineNumber: line,
				RawBlock:   fnName,
			})
		}
		actions = append(actions, &ParsedAction{
			Filename:  name,
			Phase:     sourceActionPhase(name),
			Functions: functions,
		})
	}
	return actions
}
