package enterprisearchive

import (
	"encoding/csv"
	"encoding/json"
	filepath "path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
)

type ParsedParameterFile struct {
	AllocationBlockers []string
	Parameters         []dsl.Parameter
	DataSources        []dsl.DataSource
	Sections           map[string]map[string]string
	Warnings           []string
}

type parameterDefinitionCollision struct {
	Name              string
	ExistingSource    string
	ConflictingSource string
	AlternateName     string
}

type parameterMergeState struct {
	parameterOrigins      map[string]string
	parameterIndexes      map[string]int
	parameterDefinitions  map[string]int
	usedParameterNames    map[string]struct{}
	dataSourceIndexes     map[string]int
	dataSourceDefinitions map[string]int
	usedDataSourceNames   map[string]struct{}
}

func newParameterMergeState(script *dsl.Script) *parameterMergeState {
	state := &parameterMergeState{
		parameterOrigins:      make(map[string]string),
		parameterIndexes:      make(map[string]int),
		parameterDefinitions:  make(map[string]int),
		usedParameterNames:    make(map[string]struct{}),
		dataSourceIndexes:     make(map[string]int),
		dataSourceDefinitions: make(map[string]int),
		usedDataSourceNames:   make(map[string]struct{}),
	}
	if script == nil {
		return state
	}
	for index, parameter := range script.Parameters {
		name := canonicalSourceParameterName(parameter.Name)
		if name != "" {
			state.parameterOrigins[name] = "converted script"
			if _, exists := state.parameterIndexes[name]; !exists {
				state.parameterIndexes[name] = index
			}
			state.parameterDefinitions[importedParameterDefinitionKey(name, parameter)] = index
			state.usedParameterNames[name] = struct{}{}
		}
	}
	for index, source := range script.DataSources {
		name := strings.ToLower(strings.TrimSpace(source.Name))
		if name != "" {
			if _, exists := state.dataSourceIndexes[name]; !exists {
				state.dataSourceIndexes[name] = index
			}
			state.dataSourceDefinitions[importedDataSourceDefinitionKey(name, source)] = index
			state.usedDataSourceNames[name] = struct{}{}
		}
	}
	return state
}

// mergeParsedParameterFile adds one .prm file without allowing its local name
// registry to collide with parameters or data sources already attached to the
// script. Identical definitions are reused. Conflicting parameter definitions
// are retained under deterministic alternate names and reported to the caller,
// which installs a replay gate because an action-level {{param.NAME}} reference
// cannot identify which source profile LoadRunner intended.
func mergeParsedParameterFile(
	script *dsl.Script,
	parsed ParsedParameterFile,
	sourceFile string,
	state *parameterMergeState,
) []parameterDefinitionCollision {
	if script == nil {
		return nil
	}
	if state == nil {
		state = newParameterMergeState(script)
	}
	sourceFile = filepath.Base(strings.TrimSpace(sourceFile))
	if sourceFile == "" {
		sourceFile = "parameter file"
	}

	dataSourceRemap := mergeParameterDataSources(script, parsed.DataSources, state)

	type mergeEntry struct {
		originalName string
		parameter    dsl.Parameter
		hadExisting  bool
	}
	entries := make([]mergeEntry, 0, len(parsed.Parameters))
	for _, sourceParameter := range parsed.Parameters {
		parameter := sourceParameter
		originalName := canonicalSourceParameterName(parameter.Name)
		if originalName == "" {
			continue
		}
		if remapped, exists := dataSourceRemap[strings.ToLower(strings.TrimSpace(parameter.DataSource))]; exists {
			parameter.DataSource = remapped
		}
		_, hadExisting := state.parameterIndexes[originalName]
		entries = append(entries, mergeEntry{
			originalName: originalName,
			parameter:    parameter,
			hadExisting:  hadExisting,
		})
	}

	// Resolve destination names before appending definitions. A SameLineAs
	// dependency can point forward, so a later conflict can force an otherwise
	// identical earlier parameter to become a distinct definition too. Revisit
	// only dependents of names whose allocation changed; this keeps a long
	// dependency chain linear instead of rescanning every parameter per depth.
	nameRemap := make(map[string]string, len(entries))
	alternateNames := make(map[int]string, len(entries))
	for _, entry := range entries {
		if existingIndex, exists := state.parameterIndexes[entry.originalName]; exists {
			nameRemap[entry.originalName] = script.Parameters[existingIndex].Name
		} else {
			nameRemap[entry.originalName] = uniqueImportedParameterName(
				entry.originalName,
				state.usedParameterNames,
			)
		}
	}
	dependents := make(map[string][]int, len(entries))
	for index, entry := range entries {
		if dependency := canonicalSourceParameterName(entry.parameter.SameLineAs); dependency != "" {
			dependents[dependency] = append(dependents[dependency], index)
		}
	}
	recalculate := func(index int) bool {
		entry := entries[index]
		parameter := entry.parameter
		if remapped, exists := nameRemap[canonicalSourceParameterName(parameter.SameLineAs)]; exists {
			parameter.SameLineAs = remapped
		}
		desired := nameRemap[entry.originalName]
		if existingIndex, exists := state.parameterDefinitions[importedParameterDefinitionKey(entry.originalName, parameter)]; exists {
			desired = script.Parameters[existingIndex].Name
			if alternate := alternateNames[index]; alternate != "" && alternate != desired {
				delete(state.usedParameterNames, canonicalSourceParameterName(alternate))
				delete(alternateNames, index)
			}
		} else if entry.hadExisting {
			desired = alternateNames[index]
			if desired == "" {
				desired = uniqueImportedParameterName(entry.originalName, state.usedParameterNames)
				alternateNames[index] = desired
			}
		}
		if nameRemap[entry.originalName] == desired {
			return false
		}
		nameRemap[entry.originalName] = desired
		return true
	}
	queue := make([]string, 0, len(entries))
	pending := make(map[string]bool, len(entries))
	enqueue := func(name string) {
		if name == "" || pending[name] {
			return
		}
		pending[name] = true
		queue = append(queue, name)
	}
	for index, entry := range entries {
		if recalculate(index) {
			enqueue(entry.originalName)
		}
	}
	for offset := 0; offset < len(queue); offset++ {
		changedName := queue[offset]
		pending[changedName] = false
		for _, dependentIndex := range dependents[changedName] {
			if recalculate(dependentIndex) {
				enqueue(entries[dependentIndex].originalName)
			}
		}
	}

	collisions := make([]parameterDefinitionCollision, 0)
	for _, entry := range entries {
		parameter := entry.parameter
		if remapped, exists := nameRemap[canonicalSourceParameterName(parameter.SameLineAs)]; exists {
			parameter.SameLineAs = remapped
		}
		definitionKey := importedParameterDefinitionKey(entry.originalName, parameter)
		if _, exists := state.parameterDefinitions[definitionKey]; exists {
			continue
		}
		parameter.Name = nameRemap[entry.originalName]
		if entry.hadExisting {
			collisions = append(collisions, parameterDefinitionCollision{
				Name:              entry.originalName,
				ExistingSource:    state.parameterOrigins[entry.originalName],
				ConflictingSource: sourceFile,
				AlternateName:     parameter.Name,
			})
		}
		index := len(script.Parameters)
		script.Parameters = append(script.Parameters, parameter)
		state.parameterDefinitions[definitionKey] = index
		destinationName := canonicalSourceParameterName(parameter.Name)
		state.parameterIndexes[destinationName] = index
		state.parameterOrigins[destinationName] = sourceFile
		if !entry.hadExisting {
			state.parameterIndexes[entry.originalName] = index
			state.parameterOrigins[entry.originalName] = sourceFile
		}
	}
	return collisions
}

func mergeParameterDataSources(
	script *dsl.Script,
	incoming []dsl.DataSource,
	state *parameterMergeState,
) map[string]string {
	remap := make(map[string]string, len(incoming))
	if script == nil {
		return remap
	}
	if state == nil {
		state = newParameterMergeState(script)
	}
	for _, source := range incoming {
		originalName := strings.TrimSpace(source.Name)
		nameKey := strings.ToLower(originalName)
		if nameKey == "" {
			continue
		}
		definitionKey := importedDataSourceDefinitionKey(nameKey, source)
		if existingIndex, exists := state.dataSourceDefinitions[definitionKey]; exists {
			remap[nameKey] = script.DataSources[existingIndex].Name
			continue
		}
		if _, exists := state.dataSourceIndexes[nameKey]; exists {
			source.Name = uniqueImportedDataSourceName(originalName, state.usedDataSourceNames)
		} else {
			source.Name = originalName
			state.usedDataSourceNames[nameKey] = struct{}{}
		}
		remap[nameKey] = source.Name
		index := len(script.DataSources)
		state.dataSourceIndexes[strings.ToLower(strings.TrimSpace(source.Name))] = index
		state.dataSourceDefinitions[definitionKey] = index
		script.DataSources = append(script.DataSources, source)
	}
	return remap
}

func importedDataSourceDefinitionKey(sourceName string, source dsl.DataSource) string {
	source.Name = ""
	encoded, _ := json.Marshal(source)
	return strings.ToLower(strings.TrimSpace(sourceName)) + "\x00" + string(encoded)
}

func importedParameterDefinitionKey(sourceName string, parameter dsl.Parameter) string {
	parameter.Name = ""
	encoded, _ := json.Marshal(parameter)
	return canonicalSourceParameterName(sourceName) + "\x00" + string(encoded)
}

func uniqueImportedDataSourceName(base string, used map[string]struct{}) string {
	base = strings.TrimSpace(base)
	if base == "" {
		base = "data"
	}
	if _, exists := used[strings.ToLower(base)]; !exists {
		used[strings.ToLower(base)] = struct{}{}
		return base
	}
	for suffix := 2; ; suffix++ {
		candidate := base + "_" + strconv.Itoa(suffix)
		key := strings.ToLower(candidate)
		if _, exists := used[key]; exists {
			continue
		}
		used[key] = struct{}{}
		return candidate
	}
}

func uniqueImportedParameterName(base string, used map[string]struct{}) string {
	base = canonicalSourceParameterName(base)
	if base == "" {
		base = "parameter"
	}
	if _, exists := used[base]; !exists {
		used[base] = struct{}{}
		return base
	}
	for suffix := 2; ; suffix++ {
		candidate := base + "_" + strconv.Itoa(suffix)
		if _, exists := used[candidate]; exists {
			continue
		}
		used[candidate] = struct{}{}
		return candidate
	}
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
	dataSourceSignatureByFile := make(map[string]string)
	usedDataSourceNames := make(map[string]struct{})
	sectionNames := make([]string, 0, len(sections))
	for sectionName := range sections {
		sectionNames = append(sectionNames, sectionName)
	}
	sort.Slice(sectionNames, func(i, j int) bool {
		left, right := strings.ToLower(sectionNames[i]), strings.ToLower(sectionNames[j])
		if left == right {
			return sectionNames[i] < sectionNames[j]
		}
		return left < right
	})
	for _, sectionName := range sectionNames {
		values := sections[sectionName]
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
		automatic, validAuto := parseBoolExplicit(autoAllocateRaw)
		if selection == "unique" && autoAllocateRaw != "" && !validAuto {
			result.Warnings = append(result.Warnings, "parameter "+name+" has invalid auto_allocate_block_size="+strconv.Quote(autoAllocateRaw)+"; allocation requires review")
			result.AllocationBlockers = append(result.AllocationBlockers, name)
		}

		param := dsl.Parameter{
			Name:              name,
			AutoAllocate:      selection == "unique" && automatic && validAuto,
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
		case isSourceUniqueNumberType(rawType):
			param.Type = "counter"
			param.Selection = "unique"
			param.Step = 1
			param.Start, _ = strconv.Atoi(convertutil.FirstNonEmpty(lower["startvalue"], lower["start"]))
			param.BlockSize, _ = strconv.Atoi(convertutil.FirstNonEmpty(lower["blocksize"], lower["block_size"]))
			param.PadWidth = sourceIntegerFormatPadWidth(lower["format"])
			if !sourceUniqueNumberSettingsValid(lower, param) {
				result.Warnings = append(result.Warnings, "parameter "+name+" has invalid or unsupported Unique number settings; block allocation requires review")
				result.AllocationBlockers = append(result.AllocationBlockers, name)
				// Retain a structurally valid blocked definition, rather than
				// dropping the source parameter or enabling a guessed range.
				param.Start, param.BlockSize, param.PadWidth, param.AutoAllocate = 0, 1, 0, false
			}
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
				resolvedPath, dataContent, resolved := findFileEntry(archiveFiles, dataFile)
				if resolved && column != "" {
					firstRowHeader, columns := inferParameterDataSourceColumns(dataContent, column, lower["delimiter"], name, resolvedPath)
					signature := normalizeParameterDelimiter(lower["delimiter"]) + "|" + strconv.FormatBool(firstRowHeader) + "|" + strings.Join(columns, "\x00")
					fileKey := strings.ToLower(filepath.Clean(resolvedPath))
					dataSourceKey := fileKey + "\x00" + signature
					dataSourceName, exists := dataSourceByFile[dataSourceKey]
					if !exists {
						baseName := sanitizeParameterDataSourceName(resolvedPath)
						dataSourceName = uniqueParameterDataSourceName(baseName, usedDataSourceNames)
						dataSourceByFile[dataSourceKey] = dataSourceName
						if prior, conflicting := dataSourceSignatureByFile[fileKey]; conflicting && prior != signature {
							result.Warnings = append(result.Warnings, "parameter file "+filepath.Base(filename)+": data file "+resolvedPath+" is referenced with conflicting delimiter/column layouts; separate deterministic data sources were created")
						}
						dataSourceSignatureByFile[fileKey] = signature
						result.DataSources = append(result.DataSources, buildParameterDataSource(dataSourceName, resolvedPath, dataContent, column, lower["delimiter"], name))
					}
					param.DataSource = dataSourceName
					param.Column = resolveParameterColumn(dataContent, column, lower["delimiter"], name, resolvedPath)
				} else if resolved {
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

		preserveSourceTimeParameterFormat(&param, lower)
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

func uniqueParameterDataSourceName(base string, used map[string]struct{}) string {
	if _, exists := used[base]; !exists {
		used[base] = struct{}{}
		return base
	}
	for suffix := 2; ; suffix++ {
		candidate := base + "_" + strconv.Itoa(suffix)
		if _, exists := used[candidate]; !exists {
			used[candidate] = struct{}{}
			return candidate
		}
	}
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
	// Preserve the importer's resolved delimiter even when it is a comma.
	// An empty delimiter is not neutral for .dat sources: the shared runtime
	// resolver interprets it as tab for LoadRunner compatibility.
	dataSource.Delimiter = normalizeParameterDelimiter(delimiter)
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

func isSourceUniqueNumberType(rawType string) bool {
	normalized := normalizeParameterType(rawType)
	return normalized == "unique" || normalized == "uniquenumber"
}

func sourceUniqueNumberSettingsValid(values map[string]string, param dsl.Parameter) bool {
	if _, err := strconv.Atoi(convertutil.FirstNonEmpty(values["startvalue"], values["start"])); err != nil {
		return false
	}
	if param.BlockSize <= 0 || param.BlockSize > dsl.MaxParameterRowIndex || param.PadWidth < 0 || param.PadWidth > dsl.MaxGeneratedParameterBytes {
		return false
	}
	format := strings.TrimSpace(values["format"])
	if format != "" && format != "%d" && format != "%u" && !(sourceUniquePaddedFormatPattern.MatchString(format) && param.PadWidth > 0) {
		return false
	}
	if raw := convertutil.FirstNonEmpty(values["updatevalueon"], values["generatenewval"]); raw != "" && mapParameterUpdate(raw) == "" {
		return false
	}
	if raw := convertutil.FirstNonEmpty(values["outofvalues"], values["whenoutofvalues"], values["outofrangepolicy"]); raw != "" && mapParameterExhaustion(raw) == "" {
		return false
	}
	return values["increment"] == "" && values["endvalue"] == "" && !param.AutoAllocate
}

var sourceUniquePaddedFormatPattern = regexp.MustCompile(`^%0[0-9]+d$`)

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
