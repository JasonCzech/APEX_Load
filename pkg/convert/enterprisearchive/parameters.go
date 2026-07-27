package enterprisearchive

import (
	"encoding/csv"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
)

type ParsedParameterFile struct {
	Parameters  []dsl.Parameter
	DataSources []dsl.DataSource
	Sections    map[string]map[string]string
	Warnings    []string
}

func ParseParameterFile(filename, content string, archiveFiles map[string]string) ParsedParameterFile {
	sections := ParseConfigSections(content)
	result := ParsedParameterFile{
		Parameters:  make([]dsl.Parameter, 0),
		DataSources: make([]dsl.DataSource, 0),
		Sections:    sections,
		Warnings:    make([]string, 0),
	}

	dataSourceByFile := make(map[string]string)
	for sectionName, values := range sections {
		lower := make(map[string]string, len(values))
		for key, value := range values {
			lower[strings.ToLower(strings.TrimSpace(key))] = cleanConfigValue(value)
		}

		name := cleanParameterName(convertutil.FirstNonEmpty(lower["paramname"], lower["name"], sectionName))
		if name == "" || !looksLikeParameterSection(sectionName, lower) {
			continue
		}

		selectNextRow := lower["selectnextrow"]
		selection := mapParameterSelection(selectNextRow)
		autoAllocateRaw := convertutil.FirstNonEmpty(lower["auto_allocate_block_size"], lower["autoallocateblocksize"])
		if selection == "unique" && autoAllocateRaw != "" {
			if automatic, valid := parseBoolExplicit(autoAllocateRaw); valid && automatic {
				result.Warnings = append(result.Warnings,
					"parameter file "+filepath.Base(filename)+": parameter "+name+
						" requests automatic Vuser block allocation; APEX cannot calculate that script-level block without the scenario Vuser count, so block_size was left unset and requires review")
			} else if !valid {
				result.Warnings = append(result.Warnings,
					"parameter file "+filepath.Base(filename)+": parameter "+name+
						" has invalid auto_allocate_block_size="+strconv.Quote(autoAllocateRaw)+"; the value was treated as an allocation flag, not a literal block size, and requires review")
			}
		}
		param := dsl.Parameter{
			Name:              name,
			Update:            mapParameterUpdate(convertutil.FirstNonEmpty(lower["updatevalueon"], lower["generatenewval"])),
			Selection:         selection,
			OnExhaustion:      mapParameterExhaustion(convertutil.FirstNonEmpty(lower["outofvalues"], lower["whenoutofvalues"], lower["outofrangepolicy"])),
			SameLineAs:        parseParameterSameLineAs(selectNextRow),
			StartRow:          atoiDefault(convertutil.FirstNonEmpty(lower["startrow"], lower["start_row"]), 0),
			BlockSize:         parseExplicitParameterBlockSize(lower, selection),
			ValueForEachVUser: parseBool(convertutil.FirstNonEmpty(lower["value_for_each_vuser"], lower["valueforeachvuser"])),
		}
		rawType := strings.ToLower(convertutil.FirstNonEmpty(lower["type"], lower["valuetype"], lower["datatype"], lower["paramtype"]))
		switch {
		case isSourceVUserIDType(rawType):
			param.Type = "vuser_id"
			param.Update = "once"
		case isSourceCurrentIterationType(rawType):
			param.Type = "current_iteration"
			param.Update = "each_iteration"
			param.Start = atoiDefault(convertutil.FirstNonEmpty(lower["startvalue"], lower["start"]), 1)
			param.Step = atoiDefault(convertutil.FirstNonEmpty(lower["increment"], lower["step"]), 1)
			param.PadWidth = sourceIntegerFormatPadWidth(lower["format"])
		case isSourceHostType(rawType):
			param.Type = "host"
			param.Update = "once"
			if originalValue := convertutil.FirstNonEmpty(lower["originalvalue"]); originalValue != "" {
				param.Values = []string{originalValue}
			}
		case isSourceRandomNumberType(rawType, lower):
			param.Type = "random_number"
			param.Min = atoiDefault(convertutil.FirstNonEmpty(lower["minimumvalue"], lower["minvalue"], lower["min"], lower["startvalue"]), 0)
			param.Max = atoiDefault(convertutil.FirstNonEmpty(lower["maximumvalue"], lower["maxvalue"], lower["max"], lower["endvalue"]), 100)
			param.PadWidth = sourceIntegerFormatPadWidth(lower["format"])
		case strings.Contains(rawType, "date"):
			param.Type = "date_time"
			param.Format = mapSourceTimeFormat(convertutil.FirstNonEmpty(lower["format"], lower["dateformat"], lower["datetimeformat"]))
			param.Timezone = convertutil.FirstNonEmpty(lower["timezone"], lower["time_zone"])
			param.OffsetSeconds = atoiDefault(convertutil.FirstNonEmpty(lower["offsetseconds"], lower["offset"]), 0)
		case strings.Contains(rawType, "timestamp") || strings.Contains(rawType, "time stamp"):
			param.Type = "timestamp"
			param.Format = mapSourceTimestampFormat(convertutil.FirstNonEmpty(lower["format"], lower["timestampformat"]))
			param.Timezone = convertutil.FirstNonEmpty(lower["timezone"], lower["time_zone"])
			param.OffsetSeconds = atoiDefault(convertutil.FirstNonEmpty(lower["offsetseconds"], lower["offset"]), 0)
		case isSourceTimeType(rawType):
			param.Type = "time"
			param.Format = mapSourceTimeFormat(convertutil.FirstNonEmpty(lower["format"], lower["dateformat"], lower["datetimeformat"]))
			param.Timezone = convertutil.FirstNonEmpty(lower["timezone"], lower["time_zone"], sourceTimeFormatTimezone(param.Format))
			param.OffsetSeconds = atoiDefault(convertutil.FirstNonEmpty(lower["offsetseconds"], lower["offset"]), 0)
		case strings.Contains(rawType, "counter") || lower["increment"] != "":
			param.Type = "counter"
			param.Start = atoiDefault(convertutil.FirstNonEmpty(lower["startvalue"], lower["start"]), 0)
			param.End = atoiDefault(convertutil.FirstNonEmpty(lower["endvalue"], lower["end"]), 0)
			param.Step = atoiDefault(convertutil.FirstNonEmpty(lower["increment"], lower["step"]), 1)
		case strings.Contains(rawType, "uuid"):
			param.Type = "uuid"
		case strings.Contains(rawType, "random") && strings.Contains(rawType, "string"):
			param.Type = "random_string"
			param.Length = atoiDefault(convertutil.FirstNonEmpty(lower["length"], lower["size"]), 12)
			param.Charset = convertutil.FirstNonEmpty(lower["charset"], lower["characterset"])
		default:
			valueList := convertutil.FirstNonEmpty(lower["valuelist"], lower["values"], lower["valuelist1"])
			dataFile := convertutil.FirstNonEmpty(lower["table"], lower["filename"], lower["file"], lower["datafile"], lower["sourcefile"])
			if valueList != "" {
				param.Type = "list"
				param.Values = splitListValues(valueList)
			} else if dataFile != "" {
				param.Type = "file"
				column := convertutil.FirstNonEmpty(lower["columnname"], lower["column"], lower["colname"])
				if dataContent, ok := findFile(archiveFiles, dataFile); ok && column != "" {
					dataSourceName, exists := dataSourceByFile[strings.ToLower(filepath.Base(dataFile))]
					if !exists {
						dataSourceName = sanitizeParameterDataSourceName(dataFile)
						dataSourceByFile[strings.ToLower(filepath.Base(dataFile))] = dataSourceName
						result.DataSources = append(result.DataSources, buildParameterDataSource(dataSourceName, dataFile, dataContent, column, lower["delimiter"], name))
					}
					param.DataSource = dataSourceName
					param.Column = resolveParameterColumn(dataContent, column, lower["delimiter"], name, dataFile)
				} else if dataContent, ok := findFile(archiveFiles, dataFile); ok {
					param.Type = "list"
					param.Values = parseDataFileValues(dataContent)
				} else {
					result.Warnings = append(result.Warnings, "parameter file "+filepath.Base(filename)+": unable to resolve data file "+dataFile+" for parameter "+name)
				}
			} else {
				result.Warnings = append(result.Warnings, "parameter file "+filepath.Base(filename)+": unsupported parameter section "+sectionName)
				continue
			}
		}

		if param.Update == "" {
			param.Update = "each_iteration"
		}
		if param.Type == "list" || param.Type == "file" {
			if param.Selection == "" {
				param.Selection = "sequential"
			}
			if param.OnExhaustion == "" {
				param.OnExhaustion = "wrap"
			}
		}
		if (param.Type == "counter" || param.Type == "current_iteration") && param.Step == 0 {
			param.Step = 1
		}
		if param.Type != "" {
			result.Parameters = append(result.Parameters, param)
		}
	}

	return result
}

// parseExplicitParameterBlockSize keeps LoadRunner's allocation selector
// separate from its numeric block size. auto_allocate_block_size is a boolean
// flag (despite occasionally containing non-boolean legacy values), not the
// number of rows assigned to each VUser. Block allocation is meaningful only
// for Unique selection; Random, Sequential, and Same-line parameters must
// never be partitioned into VUser blocks.
func parseExplicitParameterBlockSize(values map[string]string, selection string) int {
	if selection != "unique" {
		return 0
	}
	if autoRaw := convertutil.FirstNonEmpty(values["auto_allocate_block_size"], values["autoallocateblocksize"]); autoRaw != "" {
		if automatic, valid := parseBoolExplicit(autoRaw); valid && automatic {
			return 0
		}
	}
	size := atoiDefault(convertutil.FirstNonEmpty(values["blocksize"], values["block_size"]), 0)
	if size <= 0 {
		return 0
	}
	return size
}

func isSourceRandomNumberType(rawType string, values map[string]string) bool {
	if strings.Contains(rawType, "random") && strings.Contains(rawType, "number") {
		return true
	}
	if rawType != "random" {
		return false
	}
	return convertutil.FirstNonEmpty(values["minimumvalue"], values["min"], values["startvalue"], values["minvalue"]) != "" ||
		convertutil.FirstNonEmpty(values["maximumvalue"], values["max"], values["endvalue"], values["maxvalue"]) != ""
}

func buildParameterDataSource(dataSourceName, dataFile, dataContent, column, delimiter, paramName string) dsl.DataSource {
	firstRowHeader, columns := inferParameterDataSourceColumns(dataContent, column, delimiter, paramName, dataFile)
	dataSource := dsl.DataSource{
		Name: dataSourceName,
		Type: parameterFileType(dataFile),
		// An archive member name is provenance, not an executable host path.
		// The actual data is self-contained in InlineData.
		FileName:       filepath.Base(cleanConfigValue(dataFile)),
		InlineData:     dataContent,
		Strategy:       "sequential",
		FirstRowHeader: firstRowHeader,
		Columns:        columns,
	}
	if normalized := normalizeParameterDelimiter(delimiter); normalized != "," {
		dataSource.Delimiter = normalized
	}
	return dataSource
}

func looksLikeParameterSection(sectionName string, values map[string]string) bool {
	sectionLower := strings.ToLower(cleanParameterName(sectionName))
	if sectionLower == "general" || sectionLower == "options" || sectionLower == "global" {
		return false
	}
	for _, key := range []string{"paramname", "type", "valuetype", "datatype", "table", "filename", "valuelist", "updatevalueon", "columnname", "increment"} {
		if values[key] != "" {
			return true
		}
	}
	return sectionName != ""
}

func cleanParameterName(name string) string {
	name = cleanConfigValue(name)
	if strings.HasPrefix(strings.ToLower(name), "parameter:") {
		name = strings.TrimSpace(name[len("parameter:"):])
	}
	return canonicalSourceParameterName(name)
}

func canonicalSourceParameterName(name string) string {
	name = strings.ToLower(strings.TrimSpace(cleanConfigValue(name)))
	name = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			return r
		}
		return '_'
	}, name)
	for strings.Contains(name, "__") {
		name = strings.ReplaceAll(name, "__", "_")
	}
	return strings.Trim(name, "_")
}

var (
	templateVariablePrefixPattern     = regexp.MustCompile(`^\{\{(?:var|param)\.([a-z0-9_]+)\}\}`)
	templateParameterReferencePattern = regexp.MustCompile(`\{\{var\.([a-z0-9_]+)\}\}`)
)

func applyKnownParameterReferences(script *dsl.Script) {
	if script == nil || len(script.Parameters) == 0 {
		return
	}
	names := make(map[string]struct{}, len(script.Parameters))
	for _, param := range script.Parameters {
		name := canonicalSourceParameterName(param.Name)
		if name != "" {
			names[name] = struct{}{}
		}
	}
	if len(names) == 0 {
		return
	}
	_ = dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		if ref.Action != nil {
			rewriteKnownParameterReferencesAction(ref.Action, names)
			return nil
		}
		for key, value := range ref.Request {
			ref.Request[key] = rewriteKnownParameterReferencesAny(value, names)
		}
		return nil
	})
}

func rewriteKnownParameterReferencesAction(action *dsl.Action, names map[string]struct{}) {
	if action == nil {
		return
	}
	action.Target = rewriteKnownParameterReferences(action.Target, names)
	action.Selector = rewriteKnownParameterReferences(action.Selector, names)
	action.ObjectRef = rewriteKnownParameterReferences(action.ObjectRef, names)
	action.Value = rewriteKnownParameterReferences(action.Value, names)
	action.Body = rewriteKnownParameterReferences(action.Body, names)
	action.BodyFile = rewriteKnownParameterReferences(action.BodyFile, names)
	for key, value := range action.Headers {
		action.Headers[key] = rewriteKnownParameterReferences(value, names)
	}
	for index := range action.HeaderList {
		action.HeaderList[index].Value = rewriteKnownParameterReferences(
			action.HeaderList[index].Value,
			names,
		)
	}
	for key, value := range action.Params {
		action.Params[key] = rewriteKnownParameterReferencesAny(value, names)
	}
	for key, value := range action.Metadata {
		action.Metadata[key] = rewriteKnownParameterReferencesAny(value, names)
	}
	for assertionIndex := range action.Assertions {
		action.Assertions[assertionIndex].Value = rewriteKnownParameterReferences(action.Assertions[assertionIndex].Value, names)
		action.Assertions[assertionIndex].Expected = rewriteKnownParameterReferencesAny(action.Assertions[assertionIndex].Expected, names)
	}
	for extractorIndex := range action.Extractors {
		action.Extractors[extractorIndex].Expression = rewriteKnownParameterReferences(action.Extractors[extractorIndex].Expression, names)
	}
	if action.WaitFor != nil {
		action.WaitFor.Value = rewriteKnownParameterReferences(action.WaitFor.Value, names)
	}
	if action.Sync != nil {
		action.Sync.Value = rewriteKnownParameterReferences(action.Sync.Value, names)
	}
	if action.Auth != nil {
		action.Auth.Username = rewriteKnownParameterReferences(action.Auth.Username, names)
		action.Auth.Password = rewriteKnownParameterReferences(action.Auth.Password, names)
		action.Auth.Token = rewriteKnownParameterReferences(action.Auth.Token, names)
		action.Auth.Domain = rewriteKnownParameterReferences(action.Auth.Domain, names)
	}
	if action.SOAP != nil {
		action.SOAP.Action = rewriteKnownParameterReferences(action.SOAP.Action, names)
		action.SOAP.Operation = rewriteKnownParameterReferences(action.SOAP.Operation, names)
		action.SOAP.BodyMode = rewriteKnownParameterReferences(action.SOAP.BodyMode, names)
	}
	if action.Control != nil {
		rewriteKnownParameterCondition(action.Control.Condition, names)
		if action.Control.ForEach != nil {
			rewriteKnownParameterCondition(action.Control.ForEach.Condition, names)
		}
	}
}

func rewriteKnownParameterCondition(condition *dsl.Condition, names map[string]struct{}) {
	if condition == nil {
		return
	}
	condition.Expr = rewriteKnownParameterReferences(condition.Expr, names)
	condition.Left = rewriteKnownParameterReferences(condition.Left, names)
	condition.Right = rewriteKnownParameterReferences(condition.Right, names)
}

func rewriteKnownParameterReferences(value string, names map[string]struct{}) string {
	if value == "" || len(names) == 0 {
		return value
	}
	return templateParameterReferencePattern.ReplaceAllStringFunc(value, func(token string) string {
		match := templateParameterReferencePattern.FindStringSubmatch(token)
		if len(match) != 2 {
			return token
		}
		if _, ok := names[match[1]]; !ok {
			return token
		}
		return "{{param." + match[1] + "}}"
	})
}

func rewriteKnownParameterReferencesAny(value any, names map[string]struct{}) any {
	switch typed := value.(type) {
	case string:
		return rewriteKnownParameterReferences(typed, names)
	case []any:
		for index, item := range typed {
			typed[index] = rewriteKnownParameterReferencesAny(item, names)
		}
		return typed
	case []map[string]any:
		for index, item := range typed {
			typed[index] = rewriteKnownParameterReferencesAny(item, names).(map[string]any)
		}
		return typed
	case []string:
		for index, item := range typed {
			typed[index] = rewriteKnownParameterReferences(item, names)
		}
		return typed
	case map[string]string:
		for key, item := range typed {
			typed[key] = rewriteKnownParameterReferences(item, names)
		}
		return typed
	case dsl.HeaderList:
		for index := range typed {
			typed[index].Value = rewriteKnownParameterReferences(typed[index].Value, names)
		}
		return typed
	case []dsl.HeaderEntry:
		for index := range typed {
			typed[index].Value = rewriteKnownParameterReferences(typed[index].Value, names)
		}
		return typed
	case map[string]any:
		for key, item := range typed {
			typed[key] = rewriteKnownParameterReferencesAny(item, names)
		}
		return typed
	default:
		return value
	}
}

func applyParameterizedBaseURL(script *dsl.Script) {
	if script == nil || !isSourceFallbackBaseURL(script.Config.BaseURL) {
		return
	}
	for _, action := range script.Actions {
		match := templateVariablePrefixPattern.FindStringSubmatch(strings.TrimSpace(action.Target))
		if len(match) != 2 {
			continue
		}
		value, ok := firstConcreteParameterValue(script, match[1])
		if !ok {
			continue
		}
		value = strings.TrimRight(strings.TrimSpace(value), "/")
		if isHTTPBaseURL(value) {
			script.Config.BaseURL = value
			return
		}
	}
}

func isSourceFallbackBaseURL(value string) bool {
	trimmed := strings.TrimSpace(value)
	return trimmed == "" || strings.EqualFold(trimmed, "https://example.com")
}

func isHTTPBaseURL(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

func firstConcreteParameterValue(script *dsl.Script, name string) (string, bool) {
	for _, param := range script.Parameters {
		if param.Name != name {
			continue
		}
		for _, value := range param.Values {
			if strings.TrimSpace(value) != "" {
				return cleanConfigValue(value), true
			}
		}
		if param.DataSource != "" && param.Column != "" {
			return firstParameterDataSourceValue(script.DataSources, param.DataSource, param.Column)
		}
	}
	return "", false
}

func firstParameterDataSourceValue(sources []dsl.DataSource, sourceName, column string) (string, bool) {
	for _, source := range sources {
		if source.Name != sourceName || strings.TrimSpace(source.InlineData) == "" {
			continue
		}
		for _, delimiter := range candidateParameterDelimiters(source.Delimiter) {
			if value, ok := firstParameterDataSourceValueWithDelimiter(source, column, delimiter); ok {
				return value, true
			}
		}
	}
	return "", false
}

func candidateParameterDelimiters(delimiter string) []string {
	if strings.TrimSpace(delimiter) == "" {
		return []string{",", "\t", ";", "|"}
	}
	if normalized := normalizeParameterDelimiter(delimiter); strings.TrimSpace(normalized) != "" {
		return []string{normalized}
	}
	return []string{",", "\t", ";", "|"}
}

func firstParameterDataSourceValueWithDelimiter(source dsl.DataSource, column, delimiter string) (string, bool) {
	lines := nonEmptyParameterLines(source.InlineData)
	if len(lines) == 0 {
		return "", false
	}
	rowIndex := 0
	columnIndex := 0
	if source.FirstRowHeader {
		if len(lines) < 2 {
			return "", false
		}
		headers := parseParameterDataLine(lines[0], delimiter)
		index, ok := indexParameterColumn(headers, column)
		if !ok {
			return "", false
		}
		columnIndex = index
		rowIndex = 1
	} else if len(source.Columns) > 0 {
		index, ok := indexParameterColumn(source.Columns, column)
		if !ok {
			return "", false
		}
		columnIndex = index
	}
	fields := parseParameterDataLine(lines[rowIndex], delimiter)
	if columnIndex >= len(fields) {
		return "", false
	}
	value := cleanConfigValue(fields[columnIndex])
	return value, strings.TrimSpace(value) != ""
}

func nonEmptyParameterLines(content string) []string {
	lines := make([]string, 0)
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(strings.TrimPrefix(line, "\ufeff"))
		if trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	return lines
}

func indexParameterColumn(columns []string, column string) (int, bool) {
	for index, candidate := range columns {
		if strings.EqualFold(cleanConfigValue(candidate), cleanConfigValue(column)) {
			return index, true
		}
	}
	return 0, false
}

func parseParameterDataLine(line, delimiter string) []string {
	reader := csv.NewReader(strings.NewReader(line))
	reader.FieldsPerRecord = -1
	if delimiter != "" {
		if r, _ := utf8.DecodeRuneInString(delimiter); r != utf8.RuneError {
			reader.Comma = r
		}
	}
	fields, err := reader.Read()
	if err != nil {
		fields = strings.Split(line, delimiter)
	}
	for i := range fields {
		fields[i] = cleanConfigValue(fields[i])
	}
	return fields
}

func atoiDefault(value string, fallback int) int {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return fallback
	}
	return parsed
}

func isSourceCurrentIterationType(rawType string) bool {
	normalized := normalizeParameterType(rawType)
	return normalized == "currentiteration" || normalized == "currentiterationnumber"
}

func isSourceVUserIDType(rawType string) bool {
	normalized := normalizeParameterType(rawType)
	return normalized == "userid" || normalized == "vuserid" || normalized == "vuser" || normalized == "vusernumber"
}

func isSourceHostType(rawType string) bool {
	normalized := normalizeParameterType(rawType)
	return normalized == "host" || normalized == "hostname" || normalized == "loadgenerator" || normalized == "loadgeneratorname"
}

func isSourceTimeType(rawType string) bool {
	normalized := normalizeParameterType(rawType)
	return normalized == "time" || normalized == "datetime"
}

func normalizeParameterType(rawType string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		return -1
	}, strings.ToLower(cleanConfigValue(rawType)))
}

func sourceIntegerFormatPadWidth(format string) int {
	format = cleanConfigValue(format)
	if !strings.HasPrefix(format, "%0") {
		return 0
	}
	width := strings.TrimPrefix(format, "%0")
	width = strings.TrimSuffix(width, "d")
	width = strings.TrimSuffix(width, "u")
	width = strings.TrimSuffix(width, "l")
	return atoiDefault(width, 0)
}

func mapSourceTimestampFormat(format string) string {
	format = cleanConfigValue(format)
	switch strings.ToLower(format) {
	case "", "%s":
		return "unix"
	case "%q", "%ms", "%3":
		return "unix_ms"
	default:
		return mapSourceTimeFormat(format)
	}
}

func mapSourceTimeFormat(format string) string {
	format = cleanConfigValue(format)
	if format == "" {
		return ""
	}
	replacements := []struct {
		from string
		to   string
	}{
		{"%%", "\x00"},
		{"%Y", "2006"},
		{"%y", "06"},
		{"%m", "01"},
		{"%d", "02"},
		{"%e", "_2"},
		{"%H", "15"},
		{"%I", "03"},
		{"%M", "04"},
		{"%S", "05"},
		{"%b", "Jan"},
		{"%B", "January"},
		{"%a", "Mon"},
		{"%A", "Monday"},
		{"%p", "PM"},
		{"%z", "-0700"},
		{"%Z", "MST"},
	}
	mapped := format
	for _, replacement := range replacements {
		mapped = strings.ReplaceAll(mapped, replacement.from, replacement.to)
	}
	mapped = strings.ReplaceAll(mapped, "\x00", "%")
	return mapped
}

func sourceTimeFormatTimezone(format string) string {
	format = cleanConfigValue(format)
	if strings.HasSuffix(format, "Z") && !strings.Contains(format, "MST") && !strings.Contains(format, "-0700") {
		return "UTC"
	}
	return ""
}

func mapParameterUpdate(value string) string {
	value = strings.ToLower(value)
	switch {
	case strings.Contains(value, "once"):
		return "once"
	case strings.Contains(value, "occurrence"), strings.Contains(value, "request"):
		return "each_occurrence"
	case strings.Contains(value, "iteration"):
		return "each_iteration"
	default:
		return ""
	}
}

func mapParameterSelection(value string) string {
	value = strings.ToLower(value)
	switch {
	case strings.Contains(value, "random"):
		return "random"
	case strings.Contains(value, "unique"):
		return "unique"
	case strings.Contains(value, "sequential"), strings.Contains(value, "same line"):
		return "sequential"
	default:
		return ""
	}
}

func parseParameterSameLineAs(value string) string {
	value = strings.TrimSpace(cleanConfigValue(value))
	if value == "" {
		return ""
	}
	lower := strings.ToLower(value)
	index := strings.Index(lower, "same line as")
	if index < 0 {
		return ""
	}
	reference := strings.TrimSpace(value[index+len("same line as"):])
	reference = strings.Trim(reference, ` "'=:,;()[]`)
	return cleanParameterName(reference)
}

func mapParameterExhaustion(value string) string {
	value = strings.ToLower(value)
	switch {
	case strings.Contains(value, "continuewithlast"), strings.Contains(value, "continue_with_last"), strings.Contains(value, "continue with last"):
		return "last"
	case strings.Contains(value, "wrap"), strings.Contains(value, "cycle"):
		return "wrap"
	case strings.Contains(value, "continue"):
		return "wrap"
	case strings.Contains(value, "empty"):
		return "empty"
	case strings.Contains(value, "abort"), strings.Contains(value, "error"):
		return "error"
	default:
		return ""
	}
}

func splitListValues(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == '|'
	})
	result := make([]string, 0, len(fields))
	for _, field := range fields {
		trimmed := strings.TrimSpace(strings.Trim(field, `"'`))
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func sanitizeParameterDataSourceName(dataFile string) string {
	dataFile = cleanConfigValue(dataFile)
	base := strings.TrimSuffix(filepath.Base(dataFile), filepath.Ext(dataFile))
	base = strings.ToLower(base)
	base = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		return '_'
	}, base)
	base = strings.Trim(base, "_")
	if base == "" {
		return "parameter_data"
	}
	return base
}

func parameterFileType(dataFile string) string {
	dataFile = cleanConfigValue(dataFile)
	if strings.EqualFold(filepath.Ext(dataFile), ".dat") {
		return "dat"
	}
	return "csv"
}

func parseDataFileValues(content string) []string {
	lines := strings.Split(content, "\n")
	values := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			values = append(values, trimmed)
		}
	}
	return values
}

func inferParameterDataSourceColumns(content, column, delimiter, paramName, dataFile string) (bool, []string) {
	column = cleanConfigValue(column)
	fields := firstParameterDataRowFields(content, delimiter)
	if len(fields) == 0 {
		if column == "" {
			return false, nil
		}
		return false, []string{column}
	}

	if containsParameterColumn(fields, column) {
		return true, nil
	}

	if index, ok := generatedParameterColumnIndex(column); ok {
		if index < len(fields) && looksLikeParameterHeader(fields[index], paramName, dataFile) {
			return true, nil
		}
		if _, ok := findParameterNameHeader(fields, paramName); ok {
			return true, nil
		}
		return false, generatedParameterColumns(len(fields), index+1)
	}

	columns := generatedParameterColumns(len(fields), 1)
	if column != "" {
		columns[0] = column
	}
	return false, columns
}

func resolveParameterColumn(content, column, delimiter, paramName, dataFile string) string {
	column = cleanConfigValue(column)
	fields := firstParameterDataRowFields(content, delimiter)
	if index, ok := generatedParameterColumnIndex(column); ok {
		if index < len(fields) && looksLikeParameterHeader(fields[index], paramName, dataFile) {
			return cleanConfigValue(fields[index])
		}
		if header, ok := findParameterNameHeader(fields, paramName); ok {
			return header
		}
	}
	return column
}

func firstParameterDataRowFields(content, delimiter string) []string {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(strings.TrimPrefix(line, "\ufeff"))
		if trimmed == "" {
			continue
		}
		reader := csv.NewReader(strings.NewReader(trimmed))
		reader.FieldsPerRecord = -1
		if delim := normalizeParameterDelimiter(delimiter); delim != "" {
			if r, _ := utf8.DecodeRuneInString(delim); r != utf8.RuneError {
				reader.Comma = r
			}
		}
		fields, err := reader.Read()
		if err != nil {
			fields = strings.Split(trimmed, normalizeParameterDelimiter(delimiter))
		}
		for i := range fields {
			fields[i] = cleanConfigValue(fields[i])
		}
		return fields
	}
	return nil
}

func normalizeParameterDelimiter(value string) string {
	value = cleanConfigValue(value)
	switch strings.ToLower(value) {
	case "", ",":
		return ","
	case `\t`, "tab":
		return "\t"
	default:
		return value
	}
}

var generatedParameterColumnPattern = regexp.MustCompile(`(?i)^col(?:umn)?\s*(\d+)$`)

func generatedParameterColumnIndex(column string) (int, bool) {
	match := generatedParameterColumnPattern.FindStringSubmatch(strings.TrimSpace(column))
	if len(match) != 2 {
		return 0, false
	}
	index, err := strconv.Atoi(match[1])
	if err != nil || index <= 0 {
		return 0, false
	}
	return index - 1, true
}

func containsParameterColumn(fields []string, column string) bool {
	normalizedColumn := normalizeParameterHeader(column)
	if normalizedColumn == "" {
		return false
	}
	for _, field := range fields {
		if normalizeParameterHeader(field) == normalizedColumn {
			return true
		}
	}
	return false
}

func looksLikeParameterHeader(value, paramName, dataFile string) bool {
	normalized := normalizeParameterHeader(value)
	if normalized == "" {
		return false
	}
	if normalized == normalizeParameterHeader(paramName) {
		return true
	}
	base := strings.TrimSuffix(filepath.Base(cleanConfigValue(dataFile)), filepath.Ext(cleanConfigValue(dataFile)))
	return normalized == normalizeParameterHeader(base)
}

func findParameterNameHeader(fields []string, paramName string) (string, bool) {
	normalizedParam := normalizeParameterHeader(paramName)
	if normalizedParam == "" {
		return "", false
	}
	for _, field := range fields {
		if normalizeParameterHeader(field) == normalizedParam {
			return cleanConfigValue(field), true
		}
	}
	return "", false
}

func generatedParameterColumns(fieldCount, minCount int) []string {
	if fieldCount < minCount {
		fieldCount = minCount
	}
	columns := make([]string, fieldCount)
	for i := range columns {
		columns[i] = "Col " + strconv.Itoa(i+1)
	}
	return columns
}

func normalizeParameterHeader(value string) string {
	value = strings.ToLower(cleanConfigValue(value))
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		return -1
	}, value)
}
