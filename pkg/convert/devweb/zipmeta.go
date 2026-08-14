package devweb

// zipmeta.go — split from converter.go (same package, behavior-preserving).

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/dsl"
	"gopkg.in/yaml.v3"
)

const devWebMetadataReviewRequiredKey = "devweb_metadata_review_required"

func markDevWebMetadataReviewRequired(result *ConvertResult) {
	if result == nil || result.Script == nil {
		return
	}
	ensureDevWebImportPreserved(result.Script)[devWebMetadataReviewRequiredKey] = true
}

func selectDevWebMainSource(files map[string]string) (string, string, string) {
	if len(files) == 0 {
		return "", "", ""
	}
	names := make([]string, 0, len(files))
	for name := range files {
		if strings.EqualFold(filepath.Ext(name), ".js") {
			names = append(names, name)
		}
	}
	sortStrings(names)
	for _, name := range names {
		if strings.EqualFold(filepath.Base(name), "main.js") {
			return devWebScriptNameFromFiles(files, name), name, files[name]
		}
	}
	if len(names) == 0 {
		return "", "", ""
	}
	name := names[0]
	return devWebScriptNameFromFiles(files, name), name, files[name]
}

func devWebScriptNameFromFiles(files map[string]string, sourceFile string) string {
	for name, content := range files {
		if !strings.EqualFold(filepath.Ext(name), ".usr") {
			continue
		}
		base := strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))
		if strings.TrimSpace(base) != "" {
			return base
		}
		if parsed := devWebUSRScriptName(content); parsed != "" {
			return parsed
		}
	}
	return strings.TrimSuffix(filepath.Base(sourceFile), filepath.Ext(sourceFile))
}

func devWebUSRScriptName(content string) string {
	for _, line := range strings.Split(content, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(key), "ScriptName") || strings.EqualFold(strings.TrimSpace(key), "Name") {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

type devWebParametersFile struct {
	Parameters []devWebParameter `yaml:"parameters"`
}

type devWebParameter struct {
	Name       string `yaml:"name"`
	Type       string `yaml:"type"`
	FileName   string `yaml:"fileName"`
	ColumnName string `yaml:"columnName"`
	NextRow    string `yaml:"nextRow"`
	NextValue  string `yaml:"nextValue"`
	OnEnd      string `yaml:"onEnd"`
}

func applyDevWebZipMetadata(result *ConvertResult, ctx *convertContext, diagnostics *diag.Collector) {
	if result == nil || result.Script == nil || ctx == nil || len(ctx.files) == 0 {
		return
	}
	preserved := ensureDevWebImportPreserved(result.Script)
	helperModules := make([]string, 0)
	for name := range ctx.files {
		if strings.EqualFold(filepath.Ext(name), ".js") && !strings.EqualFold(filepath.Base(name), "main.js") && name != ctx.sourceFileName {
			helperModules = append(helperModules, name)
		}
	}
	sortStrings(helperModules)
	if len(helperModules) > 0 {
		preserved["helper_modules"] = helperModules
		if diagnostics != nil {
			diagnostics.Add(diag.Warning, "DevWeb helper module", strings.Join(helperModules, ", "), "helper JavaScript modules were preserved for review; only statically visible WebRequest templates are converted")
		}
	}
	ctx.parameterTemplates = map[string]string{}
	applyDevWebParameters(result, ctx, diagnostics)
	if len(helperModules) > 0 {
		if templates := devWebHelperRequestTemplates(ctx.files, helperModules, *ctx); len(templates) > 0 {
			preserved["helper_module_request_templates"] = templates
			if diagnostics != nil {
				diagnostics.Add(diag.Warning, "DevWeb helper module request template", strings.Join(helperModules, ", "), fmt.Sprintf("%d helper WebRequest template(s) were inventoried for review; helper calls remain review-gated until arguments are resolved", len(templates)))
			}
		}
	}
	if invocations := devWebHelperInvocations(ctx.files, ctx.sourceFileName, helperModules); len(invocations) > 0 {
		preserved["helper_module_invocations"] = invocations
		if diagnostics != nil {
			diagnostics.Add(diag.Warning, "DevWeb helper module invocation", ctx.sourceFileName, fmt.Sprintf("%d helper module invocation(s) were inventoried for review; helper calls remain review-gated until the generated request template is reconciled", len(invocations)))
		}
	}
	applyDevWebRTS(result, ctx, diagnostics)
}

func ensureDevWebImportPreserved(script *dsl.Script) map[string]any {
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

func applyDevWebParameters(result *ConvertResult, ctx *convertContext, diagnostics *diag.Collector) {
	parametersPath, content, ambiguousCompanion := findDevWebCompanionFile(
		ctx.files,
		ctx.sourceFileName,
		"parameters.yml",
	)
	if ambiguousCompanion {
		markDevWebMetadataReviewRequired(result)
		if diagnostics != nil {
			diagnostics.Add(
				diag.Warning,
				"DevWeb parameters.yml",
				ctx.sourceFileName,
				"multiple parameters.yml files exist outside the selected script directory; parameter bindings were left unresolved rather than selecting a source file nondeterministically",
			)
		}
		return
	}
	if strings.TrimSpace(content) == "" {
		return
	}
	var parsed devWebParametersFile
	if err := yaml.Unmarshal([]byte(content), &parsed); err != nil {
		markDevWebMetadataReviewRequired(result)
		if diagnostics != nil {
			diagnostics.Add(diag.Warning, "DevWeb parameters.yml", parametersPath, "parameters.yml could not be parsed: "+err.Error())
		}
		return
	}
	preserved := ensureDevWebImportPreserved(result.Script)
	preserved["parameters_file"] = parametersPath
	const maxDevWebParameterDefinitions = 10_000
	if len(parsed.Parameters) > maxDevWebParameterDefinitions {
		markDevWebMetadataReviewRequired(result)
		preserved["parameters_limit_exceeded"] = true
		if diagnostics != nil {
			diagnostics.Add(
				diag.Warning,
				"DevWeb parameters.yml",
				parametersPath,
				fmt.Sprintf(
					"parameters.yml declares %d parameter definitions, exceeding the bounded import limit of %d; parameter bindings were left unresolved",
					len(parsed.Parameters),
					maxDevWebParameterDefinitions,
				),
			)
		}
		return
	}

	sourceNameCounts := make(map[string]int, len(parsed.Parameters))
	for _, param := range parsed.Parameters {
		if strings.EqualFold(param.Type, "csv") && strings.TrimSpace(param.FileName) != "" {
			sourceNameCounts[strings.TrimSpace(param.Name)]++
		}
	}
	usedParameterNames := make(map[string]int, len(parsed.Parameters))
	destinationParameterNames := make(map[int]string, len(parsed.Parameters))
	destinationBySourceName := make(map[string]string, len(parsed.Parameters))
	reportedDuplicateNames := make(map[string]struct{})
	for index, param := range parsed.Parameters {
		if !strings.EqualFold(param.Type, "csv") || strings.TrimSpace(param.FileName) == "" {
			continue
		}
		sourceName := strings.TrimSpace(param.Name)
		if sourceName == "" {
			markDevWebMetadataReviewRequired(result)
			if diagnostics != nil {
				diagnostics.Add(diag.Warning, "DevWeb parameter", parametersPath, "a CSV parameter without a name was ignored")
			}
			continue
		}
		if sourceNameCounts[sourceName] > 1 {
			markDevWebMetadataReviewRequired(result)
			if _, reported := reportedDuplicateNames[sourceName]; !reported && diagnostics != nil {
				diagnostics.Add(
					diag.Warning,
					"DevWeb parameter name collision",
					parametersPath,
					fmt.Sprintf(
						"parameter %q is declared more than once; its runtime binding was left unresolved rather than choosing one source value",
						sourceName,
					),
				)
			}
			reportedDuplicateNames[sourceName] = struct{}{}
			continue
		}
		destinationName := uniqueDevWebIdentifier(
			sanitizeName(sourceName),
			usedParameterNames,
		)
		destinationParameterNames[index] = destinationName
		destinationBySourceName[sourceName] = destinationName
		if destinationName != sourceName && diagnostics != nil {
			diagnostics.Add(
				diag.Info,
				"DevWeb parameter destination name",
				parametersPath,
				fmt.Sprintf(
					"source parameter %q is stored as destination parameter %q to avoid a case-insensitive name collision",
					sourceName,
					destinationName,
				),
			)
		}
	}

	dsByFile := make(map[string]string, len(parsed.Parameters))
	usedDataSourceNames := make(map[string]int, len(parsed.Parameters))
	for index, param := range parsed.Parameters {
		if !strings.EqualFold(param.Type, "csv") || strings.TrimSpace(param.FileName) == "" {
			continue
		}
		destinationParameterName := destinationParameterNames[index]
		if destinationParameterName == "" {
			continue
		}
		inline, resolvedPath, found, ambiguous := devWebInlineFile(
			ctx.files,
			param.FileName,
		)
		if ambiguous {
			markDevWebMetadataReviewRequired(result)
			if diagnostics != nil {
				diagnostics.Add(
					diag.Warning,
					"DevWeb parameter data file collision",
					parametersPath,
					fmt.Sprintf(
						"parameter %q references %q, which matches multiple archive files; its binding was left unresolved",
						strings.TrimSpace(param.Name),
						param.FileName,
					),
				)
			}
			continue
		}
		fileIdentity := normalizeDevWebArchiveReference(param.FileName)
		if found {
			fileIdentity = normalizeDevWebArchiveReference(resolvedPath)
		}
		fileIdentity = strings.ToLower(fileIdentity)
		sourceName := dsByFile[fileIdentity]
		if sourceName == "" {
			nameSource := param.FileName
			if found {
				nameSource = resolvedPath
			}
			sourceName = uniqueDevWebIdentifier(
				sanitizeName(strings.TrimSuffix(filepath.Base(nameSource), filepath.Ext(nameSource))),
				usedDataSourceNames,
			)
			dataSource := dsl.DataSource{
				Name:           sourceName,
				Type:           "csv",
				InlineData:     inline,
				Strategy:       devWebCSVStrategy(param.NextRow),
				Delimiter:      ",",
				FirstRowHeader: true,
			}
			if !found {
				// Path is only meaningful when the rows were not inlined. The
				// archive-relative name is not a resolvable file asset (those must
				// be absolute and registered), so setting it alongside inline data
				// made the script fail validation while the data was already
				// present.
				dataSource.Path = param.FileName
			}
			dsByFile[fileIdentity] = sourceName
			result.Script.DataSources = append(result.Script.DataSources, dataSource)
			result.Stats.DataSources++
		}
		column := convertutil.FirstNonEmpty(param.ColumnName, param.Name)
		if column != "" {
			ctx.parameterTemplates[strings.TrimSpace(param.Name)] = fmt.Sprintf("{{data.%s.%s}}", sourceName, column)
		}
		update, updateKnown := devWebParameterUpdate(param.NextValue)
		if !updateKnown && diagnostics != nil {
			diagnostics.Add(diag.Warning, "DevWeb parameter nextValue", parametersPath,
				fmt.Sprintf("parameter %q declares nextValue %q, which has no APEX equivalent; using %s and requiring review",
					param.Name, strings.TrimSpace(param.NextValue), update))
		}
		onExhaustion, onEndKnown := devWebOnExhaustion(param.OnEnd)
		if !onEndKnown && diagnostics != nil {
			diagnostics.Add(diag.Warning, "DevWeb parameter onEnd", parametersPath,
				fmt.Sprintf("parameter %q declares onEnd %q, which has no APEX equivalent; using %s and requiring review",
					param.Name, strings.TrimSpace(param.OnEnd), onExhaustion))
		}
		sameLineAs := devWebSameLineAs(param.NextRow)
		if sameLineAs != "" {
			mapped, ok := destinationBySourceName[sameLineAs]
			if !ok {
				markDevWebMetadataReviewRequired(result)
				if diagnostics != nil {
					diagnostics.Add(
						diag.Warning,
						"DevWeb parameter same-row binding",
						parametersPath,
						fmt.Sprintf(
							"parameter %q refers to ambiguous or unknown same-row parameter %q; the same-row relationship was left unresolved",
							strings.TrimSpace(param.Name),
							sameLineAs,
						),
					)
				}
				sameLineAs = ""
			} else {
				sameLineAs = mapped
			}
		}
		result.Script.Parameters = append(result.Script.Parameters, dsl.Parameter{
			Name:         destinationParameterName,
			Type:         "file",
			Update:       update,
			Selection:    devWebCSVStrategy(param.NextRow),
			OnExhaustion: onExhaustion,
			SameLineAs:   sameLineAs,
			DataSource:   sourceName,
			Column:       column,
		})
	}
	if len(result.Script.DataSources) > 0 {
		preserved["data_source_count"] = len(result.Script.DataSources)
	}
}

func applyDevWebRTS(result *ConvertResult, ctx *convertContext, diagnostics *diag.Collector) {
	rtsPath, content, ambiguousCompanion := findDevWebCompanionFile(
		ctx.files,
		ctx.sourceFileName,
		"rts.yml",
	)
	if ambiguousCompanion {
		markDevWebMetadataReviewRequired(result)
		if diagnostics != nil {
			diagnostics.Add(
				diag.Warning,
				"DevWeb rts.yml",
				ctx.sourceFileName,
				"multiple rts.yml files exist outside the selected script directory; runtime settings were not imported rather than selecting a source file nondeterministically",
			)
		}
		return
	}
	if strings.TrimSpace(content) == "" {
		return
	}
	var raw map[string]any
	if err := yaml.Unmarshal([]byte(content), &raw); err != nil {
		markDevWebMetadataReviewRequired(result)
		if diagnostics != nil {
			diagnostics.Add(diag.Warning, "DevWeb rts.yml", rtsPath, "rts.yml could not be parsed: "+err.Error())
		}
		return
	}
	preserved := ensureDevWebImportPreserved(result.Script)
	preserved["runtime_file"] = rtsPath
	parity := map[string]string{}
	if httpConnection := mapValue(raw, "httpConnection"); len(httpConnection) > 0 {
		if value := intValue(httpConnection["maxPersistentConnectionsPerHost"]); value > 0 {
			result.Script.Config.HTTP.MaxConnsPerHost = value
			parity["httpConnection.maxPersistentConnectionsPerHost"] = "executed"
		}
		if value := intValue(httpConnection["maxRedirectDepth"]); value != 0 {
			result.Script.Config.HTTP.MaxRedirects = &value
			parity["httpConnection.maxRedirectDepth"] = "executed"
		}
		if value := intValue(httpConnection["connectTimeout"]); value > 0 {
			result.Script.Config.HTTP.ConnectTimeoutMs = value * 1000
			parity["httpConnection.connectTimeout"] = "executed"
		}
		if value := intValue(httpConnection["requestTimeout"]); value > 0 {
			result.Script.Config.HTTP.RequestTimeoutMs = value * 1000
			parity["httpConnection.requestTimeout"] = "executed"
		}
		if value := intValue(httpConnection["keepAliveTimeout"]); value > 0 {
			result.Script.Config.HTTP.IdleTimeoutMs = value * 1000
			parity["httpConnection.keepAliveTimeout"] = "executed"
		}
	}
	if proxy := mapValue(raw, "proxy"); len(proxy) > 0 {
		if boolValue(proxy["useProxy"]) {
			result.Script.Config.HTTP.ProxyMode = "custom"
			result.Script.Config.HTTP.Proxy.URL = stringValue(proxy["proxyServer"])
			result.Script.Config.HTTP.Proxy.Username = stringValue(proxy["proxyUser"])
			result.Script.Config.HTTP.Proxy.Password = stringValue(proxy["proxyPassword"])
			parity["proxy"] = "partial"
		} else {
			result.Script.Config.HTTP.ProxyMode = "none"
			parity["proxy"] = "executed"
		}
	}
	if ssl := mapValue(raw, "ssl"); len(ssl) > 0 {
		if boolValue(ssl["ignoreBadCertificate"]) {
			result.Script.Config.HTTP.TLS.InsecureSkipVerify = true
		}
		if version := stringValue(ssl["tlsMaxVersion"]); version != "" {
			result.Script.Config.HTTP.TLS.MaxVersion = strings.TrimPrefix(strings.ToLower(version), "tls")
		}
		parity["ssl"] = "partial"
	}
	if replay := mapValue(raw, "replay"); len(replay) > 0 {
		if strings.EqualFold(stringValue(replay["saveSnapshots"]), "error") {
			result.Script.Config.ErrorHandling.SnapshotOnError = true
		}
		if !boolValue(replay["useCache"]) {
			setDevWebRuntimeAttribute(result.Script, "devweb.replay.useCache", "false")
		}
		parity["replay"] = "partial"
	}
	if logger := mapValue(raw, "vuserLogger"); len(logger) > 0 {
		if strings.EqualFold(stringValue(logger["logMode"]), "error") {
			result.Script.Config.Log.OnErrorOnly = true
			result.Script.Config.Log.Level = "extended"
		}
		parity["vuserLogger"] = "partial"
	}
	if thinkTime := mapValue(raw, "thinkTime"); len(thinkTime) > 0 {
		if strings.EqualFold(stringValue(thinkTime["type"]), "ignore") {
			result.Script.Config.ThinkTime.Ignore = true
			parity["thinkTime"] = "executed"
		}
	}
	if len(parity) > 0 {
		preserved["runtime_parity"] = parity
	}
}

func findDevWebCompanionFile(
	files map[string]string,
	sourceFile string,
	base string,
) (string, string, bool) {
	var names []string
	for name := range files {
		if strings.EqualFold(filepath.Base(name), base) {
			names = append(names, name)
		}
	}
	sortStrings(names)
	if len(names) == 0 {
		return "", "", false
	}
	sourceDir := strings.ToLower(normalizeDevWebArchiveReference(path.Dir(sourceFile)))
	var colocated []string
	for _, name := range names {
		if strings.ToLower(normalizeDevWebArchiveReference(path.Dir(name))) == sourceDir {
			colocated = append(colocated, name)
		}
	}
	if len(colocated) == 1 {
		return colocated[0], files[colocated[0]], false
	}
	if len(colocated) > 1 || len(names) > 1 {
		return "", "", true
	}
	return names[0], files[names[0]], false
}

func devWebInlineFile(
	files map[string]string,
	fileName string,
) (content string, resolvedPath string, found bool, ambiguous bool) {
	declared := normalizeDevWebArchiveReference(fileName)
	declaredBase := strings.ToLower(path.Base(declared))
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sortStrings(names)
	match := func(predicate func(string) bool) []string {
		var matches []string
		for _, name := range names {
			if predicate(normalizeDevWebArchiveReference(name)) {
				matches = append(matches, name)
			}
		}
		return matches
	}
	matchGroups := [][]string{
		match(func(candidate string) bool { return strings.EqualFold(candidate, declared) }),
		match(func(candidate string) bool {
			return declared != "" && strings.HasSuffix(strings.ToLower(candidate), "/"+strings.ToLower(declared))
		}),
		match(func(candidate string) bool {
			return declaredBase != "" && strings.ToLower(path.Base(candidate)) == declaredBase
		}),
	}
	for _, matches := range matchGroups {
		switch len(matches) {
		case 0:
			continue
		case 1:
			name := matches[0]
			return files[name], name, true, false
		default:
			return "", "", false, true
		}
	}
	return "", "", false, false
}

func normalizeDevWebArchiveReference(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	value = strings.TrimPrefix(path.Clean("/"+value), "/")
	if value == "." {
		return ""
	}
	return value
}

func uniqueDevWebIdentifier(base string, used map[string]int) string {
	base = strings.TrimSpace(base)
	if base == "" {
		base = "devweb_value"
	}
	identity := strings.ToLower(base)
	if used[identity] == 0 {
		used[identity] = 1
		return base
	}
	for suffix := used[identity] + 1; ; suffix++ {
		candidate := fmt.Sprintf("%s_%d", base, suffix)
		candidateIdentity := strings.ToLower(candidate)
		if used[candidateIdentity] != 0 {
			continue
		}
		used[identity] = suffix
		used[candidateIdentity] = 1
		return candidate
	}
}

func setDevWebRuntimeAttribute(script *dsl.Script, key string, value string) {
	if script.Config.WebRuntime.AdditionalAttributes == nil {
		script.Config.WebRuntime.AdditionalAttributes = map[string]string{}
	}
	script.Config.WebRuntime.AdditionalAttributes[key] = value
}

func devWebCSVStrategy(value string) string {
	switch {
	case strings.Contains(strings.ToLower(value), "random"):
		return "random"
	default:
		return "sequential"
	}
}

// devWebParameterUpdate maps a DevWeb "nextValue" to the DSL update vocabulary
// (once, each_iteration, each_occurrence).
//
// These previously passed the DevWeb spelling straight through, so an imported
// parameter carried update="iteration" and on_exhaustion="loop". Those are not
// DSL values, so every zip import with a parameter file produced a script that
// failed validateScript and could not be saved. ok=false means the source value
// was not recognized; callers record a review diagnostic rather than silently
// picking a behavior.
func devWebParameterUpdate(value string) (string, bool) {
	switch strings.ReplaceAll(strings.ToLower(strings.TrimSpace(value)), " ", "_") {
	case "", "iteration", "each_iteration":
		// DevWeb's default is per-iteration.
		return "each_iteration", true
	case "occurrence", "each_occurrence":
		return "each_occurrence", true
	case "once", "unique":
		return "once", true
	default:
		return "each_iteration", false
	}
}

// devWebOnExhaustion maps a DevWeb "onEnd" to the DSL on_exhaustion vocabulary
// (wrap, empty, error, last).
func devWebOnExhaustion(value string) (string, bool) {
	switch strings.ReplaceAll(strings.ToLower(strings.TrimSpace(value)), " ", "_") {
	case "", "loop", "cycle", "cyclic", "wrap":
		return "wrap", true
	case "stop", "abort", "error":
		return "error", true
	case "last", "continue", "keep", "last_value":
		return "last", true
	case "empty", "none":
		return "empty", true
	default:
		return "wrap", false
	}
}

func devWebSameLineAs(value string) string {
	lower := strings.ToLower(strings.TrimSpace(value))
	if strings.HasPrefix(lower, "same as ") {
		return strings.TrimSpace(value[len("same as "):])
	}
	return ""
}
