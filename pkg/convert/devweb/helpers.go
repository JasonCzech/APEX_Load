package devweb

// helpers.go — split from converter.go (same package, behavior-preserving).

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/dsl"
)

type devWebHelperRequestTemplate struct {
	Module           string   `json:"module"`
	Function         string   `json:"function,omitempty"`
	Method           string   `json:"method"`
	URL              string   `json:"url,omitempty"`
	QueryKeys        []string `json:"query_keys,omitempty"`
	HeaderKeys       []string `json:"header_keys,omitempty"`
	HasBody          bool     `json:"has_body,omitempty"`
	StaticResolvable bool     `json:"static_resolvable"`
}

type devWebHelperInvocation struct {
	Module              string   `json:"module,omitempty"`
	Object              string   `json:"object"`
	Function            string   `json:"function"`
	AssignedTo          string   `json:"assigned_to,omitempty"`
	ArgumentCount       int      `json:"argument_count"`
	Sent                bool     `json:"sent"`
	ExtractorNames      []string `json:"extractor_names,omitempty"`
	TemplateInventoried bool     `json:"template_inventoried"`
}

func devWebHelperRequestTemplates(files map[string]string, helperModules []string, ctx convertContext) []devWebHelperRequestTemplate {
	templates := make([]devWebHelperRequestTemplate, 0)
	for _, module := range helperModules {
		content := files[module]
		if strings.TrimSpace(content) == "" {
			continue
		}
		templates = append(templates, devWebHelperRequestTemplatesForModule(module, content, ctx)...)
	}
	return templates
}

func devWebHelperRequestTemplatesForModule(module string, content string, ctx convertContext) []devWebHelperRequestTemplate {
	requestPattern := regexp.MustCompile(`(?s)new\s+load\.WebRequest\s*\(\s*\{`)
	matches := requestPattern.FindAllStringIndex(content, -1)
	if len(matches) == 0 {
		return nil
	}
	functions := devWebFunctionRanges(content)
	templates := make([]devWebHelperRequestTemplate, 0, len(matches))
	for _, match := range matches {
		openBrace := match[1] - 1
		closeBrace := findMatchingJavaScriptBrace(content, openBrace)
		if closeBrace <= openBrace {
			continue
		}
		objectLiteral := content[openBrace+1 : closeBrace]
		functionName := devWebFunctionForOffset(functions, match[0])
		query := objectStringMapField(objectLiteral, "queryString", ctx)
		headers := objectStringMapField(objectLiteral, "headers", ctx)
		body, hasBody := objectFieldRaw(objectLiteral, "body")
		url := firstNonEmpty(objectStringField(objectLiteral, "url", ctx), objectStringField(objectLiteral, "URL", ctx))
		method := strings.ToUpper(firstNonEmpty(objectStringField(objectLiteral, "method", ctx), "GET"))
		template := devWebHelperRequestTemplate{
			Module:           module,
			Function:         functionName,
			Method:           method,
			URL:              url,
			QueryKeys:        sortedMapKeys(query),
			HeaderKeys:       sortedMapKeys(headers),
			HasBody:          strings.TrimSpace(body) != "" || hasBody,
			StaticResolvable: devWebHelperTemplateStatic(url, method, query, headers, body),
		}
		templates = append(templates, template)
	}
	return templates
}

func devWebHelperInvocations(files map[string]string, sourceFileName string, helperModules []string) []devWebHelperInvocation {
	content := files[sourceFileName]
	if strings.TrimSpace(content) == "" {
		return nil
	}
	aliases := devWebHelperAliases(content, sourceFileName, helperModules)
	if len(aliases) == 0 {
		return nil
	}
	invocations := make([]devWebHelperInvocation, 0)
	callPattern := devWebHelperCallPattern()
	for _, match := range callPattern.FindAllStringSubmatch(content, -1) {
		if len(match) < 6 {
			continue
		}
		objectName := strings.TrimSpace(match[2])
		module := aliases[objectName]
		if module == "" {
			continue
		}
		assignedTo := strings.TrimSpace(match[1])
		functionName := strings.TrimSpace(match[3])
		sent := strings.TrimSpace(match[5]) != "" || devWebHelperInvocationSent(content, assignedTo)
		invocation := devWebHelperInvocation{
			Module:              module,
			Object:              objectName,
			Function:            functionName,
			AssignedTo:          assignedTo,
			ArgumentCount:       len(splitTopLevel(strings.TrimSpace(match[4]), ',')),
			Sent:                sent,
			ExtractorNames:      devWebHelperInvocationExtractors(content, assignedTo),
			TemplateInventoried: devWebHelperModuleHasFunction(files[module], functionName),
		}
		invocations = append(invocations, invocation)
	}
	return invocations
}

func convertedDevWebHelperInvocationActions(block namedBlock, stats *ConvertStats, diagnostics *diag.Collector, ctx convertContext) []parsedStep {
	if len(ctx.files) == 0 || strings.TrimSpace(ctx.sourceFileName) == "" {
		return nil
	}
	helperModules := devWebHelperModules(ctx.files, ctx.sourceFileName)
	if len(helperModules) == 0 {
		return nil
	}
	aliases := devWebHelperAliases(ctx.files[ctx.sourceFileName], ctx.sourceFileName, helperModules)
	if len(aliases) == 0 {
		return nil
	}
	definitions := devWebHelperRequestDefinitions(ctx.files, helperModules)
	if len(definitions) == 0 {
		return nil
	}
	localValues := devWebBlockConstantValues(block.Body, ctx)
	callPattern := devWebHelperCallPattern()
	var steps []parsedStep
	for _, match := range callPattern.FindAllStringSubmatchIndex(block.Body, -1) {
		if len(match) < 12 || match[4] < 0 || match[6] < 0 || match[8] < 0 {
			continue
		}
		objectName := strings.TrimSpace(block.Body[match[4]:match[5]])
		module := aliases[objectName]
		if module == "" {
			continue
		}
		assignedTo := ""
		if match[2] >= 0 && match[3] >= 0 {
			assignedTo = strings.TrimSpace(block.Body[match[2]:match[3]])
		}
		directSend := match[10] >= 0 && match[11] >= 0
		if !directSend && !devWebHelperInvocationSent(block.Body, assignedTo) {
			continue
		}
		functionName := strings.TrimSpace(block.Body[match[6]:match[7]])
		definition, ok := definitions[module+"::"+functionName]
		if !ok {
			continue
		}
		localCtx, ok := devWebHelperInvocationContext(ctx, definition.Params, block.Body[match[8]:match[9]], localValues)
		if !ok {
			continue
		}
		action, ok := parseWebRequest(block.Name, definition.ObjectLiteral, localCtx)
		if !ok || containsTemplateExpression(action.Target) || containsTemplateExpression(action.Body) || stringMapContainsTemplateExpression(action.Headers) {
			continue
		}
		if action.Metadata == nil {
			action.Metadata = map[string]any{}
		}
		action.Metadata["devweb_helper_module"] = definition.Module
		action.Metadata["devweb_helper_function"] = definition.Function
		action.Metadata["devweb_helper_object"] = objectName
		if assignedTo != "" {
			action.Metadata["devweb_request_variable"] = assignedTo
		}
		action.Extractors = append(action.Extractors, devWebHelperInvocationExtractorsFull(block.Body, assignedTo)...)
		stats.TotalSteps++
		stats.HTTPRequests++
		stats.Extractors += len(action.Extractors)
		if diagnostics != nil {
			diagnostics.Add(diag.Info, "DevWeb helper module request", devwebLocation(block, match[0]), fmt.Sprintf("%s.%s was mapped to a native HTTP action", objectName, functionName))
		}
		steps = append(steps, parsedStep{Protocol: "http", Action: action, Function: "load.WebRequest(helper)", StartLine: devwebLine(block, match[0]), EndLine: devwebLine(block, match[1])})
	}
	return steps
}

type devWebHelperRequestDefinition struct {
	Module        string
	Function      string
	Params        []string
	ObjectLiteral string
}

func devWebHelperModules(files map[string]string, sourceFileName string) []string {
	helperModules := make([]string, 0)
	for name := range files {
		if strings.EqualFold(filepath.Ext(name), ".js") && !strings.EqualFold(filepath.Base(name), "main.js") && name != sourceFileName {
			helperModules = append(helperModules, name)
		}
	}
	sortStrings(helperModules)
	return helperModules
}

func devWebHelperRequestDefinitions(files map[string]string, helperModules []string) map[string]devWebHelperRequestDefinition {
	definitions := map[string]devWebHelperRequestDefinition{}
	requestPattern := regexp.MustCompile(`(?s)new\s+load\.WebRequest\s*\(\s*\{`)
	for _, module := range helperModules {
		content := files[module]
		for _, function := range devWebFunctionRanges(content) {
			body := content[function.Start:function.End]
			match := requestPattern.FindStringIndex(body)
			if len(match) == 0 {
				continue
			}
			openBrace := function.Start + match[1] - 1
			closeBrace := findMatchingJavaScriptBrace(content, openBrace)
			if closeBrace <= openBrace {
				continue
			}
			definitions[module+"::"+function.Name] = devWebHelperRequestDefinition{
				Module:        module,
				Function:      function.Name,
				Params:        function.Params,
				ObjectLiteral: content[openBrace+1 : closeBrace],
			}
		}
	}
	return definitions
}

func devWebHelperCallPattern() *regexp.Regexp {
	return regexp.MustCompile(`(?s)(?:(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=\s*)?([A-Za-z_$][\w$]*)\.([A-Za-z_$][\w$]*)\s*\((.*?)\)\s*(?:\.\s*(send|sendSync)\s*\(\s*\))?\s*;`)
}

func devWebHelperInvocationContext(ctx convertContext, params []string, rawArgs string, localValues map[string]string) (convertContext, bool) {
	args := splitTopLevel(strings.TrimSpace(rawArgs), ',')
	if len(args) != len(params) {
		return ctx, false
	}
	local := ctx
	local.parameterTemplates = map[string]string{}
	for key, value := range ctx.parameterTemplates {
		local.parameterTemplates[key] = value
	}
	for i, param := range params {
		value, ok := devWebHelperArgumentValue(args[i], ctx, localValues)
		if !ok {
			return ctx, false
		}
		local.parameterTemplates[param] = value
	}
	return local, true
}

func devWebHelperArgumentValue(raw string, ctx convertContext, localValues map[string]string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	if regexp.MustCompile(`^[A-Za-z_$][\w$]*$`).MatchString(raw) && localValues != nil {
		if value, ok := localValues[raw]; ok {
			return value, true
		}
	}
	return devWebStaticArgumentValue(raw, ctx)
}

func devWebStaticArgumentValue(raw string, ctx convertContext) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	if strings.HasPrefix(raw, "load.params.") {
		name := strings.TrimSpace(strings.TrimPrefix(raw, "load.params."))
		if ctx.parameterTemplates != nil && ctx.parameterTemplates[name] != "" {
			return ctx.parameterTemplates[name], true
		}
		return "{{var." + name + "}}", true
	}
	if regexp.MustCompile(`^-?[0-9]+(?:\.[0-9]+)?$`).MatchString(raw) {
		return raw, true
	}
	if strings.HasPrefix(raw, `"`) || strings.HasPrefix(raw, `'`) || strings.HasPrefix(raw, "`") {
		value := jsStringValue(raw, ctx.parameterTemplates)
		return value, !containsTemplateExpression(value)
	}
	return "", false
}

func devWebBlockConstantValues(content string, ctx convertContext) map[string]string {
	pattern := regexp.MustCompile(`(?m)\b(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=\s*([^;\n]+)\s*;`)
	values := map[string]string{}
	for _, match := range pattern.FindAllStringSubmatch(content, -1) {
		if len(match) < 3 {
			continue
		}
		value, ok := devWebStaticArgumentValue(match[2], ctx)
		if !ok || containsTemplateExpression(value) {
			continue
		}
		values[strings.TrimSpace(match[1])] = value
	}
	if len(values) == 0 {
		return nil
	}
	return values
}

func devWebHelperAliases(content string, sourceFileName string, helperModules []string) map[string]string {
	aliases := map[string]string{}
	pattern := regexp.MustCompile(`(?m)(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=\s*require\s*\(\s*["']([^"']+)["']\s*\)\s*(?:\(\s*\))?`)
	for _, match := range pattern.FindAllStringSubmatch(content, -1) {
		if len(match) < 3 {
			continue
		}
		module := resolveDevWebHelperModule(sourceFileName, match[2], helperModules)
		if module != "" {
			aliases[match[1]] = module
		}
	}
	return aliases
}

func resolveDevWebHelperModule(sourceFileName string, required string, helperModules []string) string {
	required = strings.TrimSpace(strings.TrimSuffix(required, ".js"))
	if required == "" {
		return ""
	}
	sourceDir := filepath.ToSlash(filepath.Dir(sourceFileName))
	candidates := []string{
		filepath.ToSlash(filepath.Join(sourceDir, required+".js")),
		filepath.ToSlash(filepath.Join(sourceDir, required)),
		strings.TrimPrefix(required+".js", "./"),
		strings.TrimPrefix(required, "./"),
		filepath.Base(required) + ".js",
		filepath.Base(required),
	}
	for _, helper := range helperModules {
		normalized := filepath.ToSlash(helper)
		base := filepath.Base(normalized)
		for _, candidate := range candidates {
			candidate = filepath.ToSlash(candidate)
			if strings.EqualFold(normalized, candidate) || strings.EqualFold(base, filepath.Base(candidate)) {
				return helper
			}
		}
	}
	return ""
}

func devWebHelperInvocationSent(content string, assignedTo string) bool {
	if strings.TrimSpace(assignedTo) == "" {
		return false
	}
	pattern := regexp.MustCompile(regexp.QuoteMeta(assignedTo) + `\s*\.\s*(?:send|sendSync)\s*\(`)
	return pattern.MatchString(content)
}

func devWebHelperInvocationExtractors(content string, assignedTo string) []string {
	if strings.TrimSpace(assignedTo) == "" {
		return nil
	}
	pattern := regexp.MustCompile(regexp.QuoteMeta(assignedTo) + `\s*\.\s*extractors\s*\.\s*push\s*\(\s*new\s+load\.(?:JsonPathExtractor|BoundaryExtractor)\s*\(\s*["']([^"']+)["']`)
	names := make([]string, 0)
	for _, match := range pattern.FindAllStringSubmatch(content, -1) {
		if len(match) >= 2 {
			names = append(names, match[1])
		}
	}
	sortStrings(names)
	if len(names) == 0 {
		return nil
	}
	return names
}

func devWebHelperInvocationExtractorsFull(content string, assignedTo string) []dsl.Extractor {
	if strings.TrimSpace(assignedTo) == "" {
		return nil
	}
	var extractors []dsl.Extractor
	boundaryPattern := regexp.MustCompile(regexp.QuoteMeta(assignedTo) + `\s*\.\s*extractors\s*\.\s*push\s*\(\s*new\s+load\.BoundaryExtractor\s*\(\s*["']([^"']+)["']\s*,\s*["']([^"']*)["']\s*,\s*["']([^"']*)["']`)
	for _, match := range boundaryPattern.FindAllStringSubmatch(content, -1) {
		if len(match) < 4 {
			continue
		}
		extractors = append(extractors, dsl.Extractor{
			Name:       match[1],
			Source:     "body",
			Type:       "boundary",
			Expression: match[2] + "|||" + match[3],
		})
	}
	jsonPathPattern := regexp.MustCompile(regexp.QuoteMeta(assignedTo) + `\s*\.\s*extractors\s*\.\s*push\s*\(\s*new\s+load\.JsonPathExtractor\s*\(\s*["']([^"']+)["']\s*,\s*["']([^"']*)["']`)
	for _, match := range jsonPathPattern.FindAllStringSubmatch(content, -1) {
		if len(match) < 3 {
			continue
		}
		extractors = append(extractors, dsl.Extractor{
			Name:       match[1],
			Source:     "body",
			Type:       "jsonpath",
			Expression: match[2],
		})
	}
	return extractors
}

func devWebHelperModuleHasFunction(content string, functionName string) bool {
	if strings.TrimSpace(content) == "" || strings.TrimSpace(functionName) == "" {
		return false
	}
	for _, function := range devWebFunctionRanges(content) {
		if function.Name == functionName {
			return true
		}
	}
	return false
}

type devWebFunctionRange struct {
	Name   string
	Params []string
	Start  int
	End    int
}

func devWebFunctionRanges(content string) []devWebFunctionRange {
	ranges := make([]devWebFunctionRange, 0)
	seen := map[string]struct{}{}
	appendMatches := func(pattern *regexp.Regexp, nameGroup int, paramsGroup int) {
		for _, match := range pattern.FindAllStringSubmatchIndex(content, -1) {
			if len(match) <= paramsGroup*2+1 || match[nameGroup*2] < 0 || match[paramsGroup*2] < 0 {
				continue
			}
			name := content[match[nameGroup*2]:match[nameGroup*2+1]]
			if !isDevWebFunctionRangeName(name) {
				continue
			}
			openBrace := match[1] - 1
			closeBrace := findMatchingJavaScriptBrace(content, openBrace)
			if closeBrace <= openBrace {
				continue
			}
			key := fmt.Sprintf("%s:%d:%d", name, match[0], closeBrace)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			ranges = append(ranges, devWebFunctionRange{
				Name:   name,
				Params: devWebFunctionParams(content[match[paramsGroup*2]:match[paramsGroup*2+1]]),
				Start:  match[0],
				End:    closeBrace,
			})
		}
	}
	appendMatches(regexp.MustCompile(`(?s)function\s+([A-Za-z_$][\w$]*)\s*\(([^)]*)\)\s*\{`), 1, 2)
	appendMatches(regexp.MustCompile(`(?s)(?:^|[,{]\s*)([A-Za-z_$][\w$]*)\s*\(([^)]*)\)\s*\{`), 1, 2)
	appendMatches(regexp.MustCompile(`(?s)(?:^|[,{]\s*)([A-Za-z_$][\w$]*)\s*:\s*function\s*\(([^)]*)\)\s*\{`), 1, 2)
	return ranges
}

func isDevWebFunctionRangeName(name string) bool {
	switch strings.TrimSpace(name) {
	case "", "if", "for", "while", "switch", "catch", "function":
		return false
	default:
		return true
	}
}

func devWebFunctionParams(raw string) []string {
	parts := splitTopLevel(raw, ',')
	params := make([]string, 0, len(parts))
	for _, part := range parts {
		name := strings.TrimSpace(part)
		if regexp.MustCompile(`^[A-Za-z_$][\w$]*$`).MatchString(name) {
			params = append(params, name)
		}
	}
	return params
}

func devWebFunctionForOffset(functions []devWebFunctionRange, offset int) string {
	for _, function := range functions {
		if offset >= function.Start && offset <= function.End {
			return function.Name
		}
	}
	return ""
}

func devWebHelperTemplateStatic(url string, method string, query map[string]string, headers map[string]string, body string) bool {
	if containsTemplateExpression(url) || containsTemplateExpression(method) || containsTemplateExpression(body) || stringMapContainsTemplateExpression(query) || stringMapContainsTemplateExpression(headers) {
		return false
	}
	for _, value := range append([]string{url, method, body}, mapValues(query, headers)...) {
		if strings.Contains(value, "this.") || strings.Contains(value, "=>") {
			return false
		}
	}
	return strings.TrimSpace(url) != ""
}
