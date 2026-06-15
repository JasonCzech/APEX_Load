package devweb

// zipmeta.go — split from converter.go (same package, behavior-preserving).

import (
	"archive/zip"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/dsl"
	"gopkg.in/yaml.v3"
)

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
	parametersPath, content := findDevWebFile(ctx.files, "parameters.yml")
	if strings.TrimSpace(content) == "" {
		return
	}
	var parsed devWebParametersFile
	if err := yaml.Unmarshal([]byte(content), &parsed); err != nil {
		if diagnostics != nil {
			diagnostics.Add(diag.Warning, "DevWeb parameters.yml", parametersPath, "parameters.yml could not be parsed: "+err.Error())
		}
		return
	}
	preserved := ensureDevWebImportPreserved(result.Script)
	preserved["parameters_file"] = parametersPath
	dsByFile := map[string]*dsl.DataSource{}
	for _, param := range parsed.Parameters {
		if !strings.EqualFold(param.Type, "csv") || strings.TrimSpace(param.FileName) == "" {
			continue
		}
		sourceName := sanitizeName(strings.TrimSuffix(filepath.Base(param.FileName), filepath.Ext(param.FileName)))
		ds := dsByFile[param.FileName]
		if ds == nil {
			inline := devWebInlineFile(ctx.files, param.FileName)
			dataSource := dsl.DataSource{
				Name:           sourceName,
				Type:           "csv",
				Path:           param.FileName,
				InlineData:     inline,
				Strategy:       devWebCSVStrategy(param.NextRow),
				Delimiter:      ",",
				FirstRowHeader: true,
			}
			dsByFile[param.FileName] = &dataSource
			result.Script.DataSources = append(result.Script.DataSources, dataSource)
			result.Stats.DataSources++
		}
		column := firstNonEmpty(param.ColumnName, param.Name)
		if column != "" {
			ctx.parameterTemplates[param.Name] = fmt.Sprintf("{{data.%s.%s}}", sourceName, column)
		}
		result.Script.Parameters = append(result.Script.Parameters, dsl.Parameter{
			Name:         param.Name,
			Type:         "file",
			Update:       devWebParameterUpdate(param.NextValue),
			Selection:    devWebCSVStrategy(param.NextRow),
			OnExhaustion: devWebOnExhaustion(param.OnEnd),
			SameLineAs:   devWebSameLineAs(param.NextRow),
			DataSource:   sourceName,
			Column:       column,
		})
	}
	if len(result.Script.DataSources) > 0 {
		preserved["data_source_count"] = len(result.Script.DataSources)
	}
}

func applyDevWebRTS(result *ConvertResult, ctx *convertContext, diagnostics *diag.Collector) {
	rtsPath, content := findDevWebFile(ctx.files, "rts.yml")
	if strings.TrimSpace(content) == "" {
		return
	}
	var raw map[string]any
	if err := yaml.Unmarshal([]byte(content), &raw); err != nil {
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

func findDevWebFile(files map[string]string, base string) (string, string) {
	for name, content := range files {
		if strings.EqualFold(filepath.Base(name), base) {
			return name, content
		}
	}
	return "", ""
}

func devWebInlineFile(files map[string]string, fileName string) string {
	for name, content := range files {
		if strings.EqualFold(filepath.Base(name), filepath.Base(fileName)) {
			return content
		}
	}
	return ""
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

func devWebParameterUpdate(value string) string {
	if strings.TrimSpace(value) == "" {
		return "iteration"
	}
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(value)), " ", "_")
}

func devWebOnExhaustion(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "loop", "cycle":
		return "loop"
	case "stop", "abort":
		return "stop"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func devWebSameLineAs(value string) string {
	lower := strings.ToLower(strings.TrimSpace(value))
	if strings.HasPrefix(lower, "same as ") {
		return strings.TrimSpace(value[len("same as "):])
	}
	return ""
}

func readZipFile(file *zip.File, remainingTotal int64) ([]byte, error) {
	reader, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	// Bound the decompressed read by the smaller of the per-entry cap and the
	// remaining cumulative budget, so a small zip can't inflate to GiBs in memory.
	limit := int64(maxDevWebEntryBytes)
	if remainingTotal < limit {
		limit = remainingTotal
	}
	if limit < 0 {
		limit = 0
	}
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("zip entry %q exceeds the decompression limit (possible zip bomb)", file.Name)
	}
	return body, nil
}
