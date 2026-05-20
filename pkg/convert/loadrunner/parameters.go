package loadrunner

import (
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
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
			lower[strings.ToLower(strings.TrimSpace(key))] = strings.TrimSpace(value)
		}

		name := strings.TrimSpace(firstNonEmpty(lower["paramname"], lower["name"], sectionName))
		if name == "" || !looksLikeParameterSection(sectionName, lower) {
			continue
		}

		param := dsl.Parameter{
			Name:         name,
			Update:       mapParameterUpdate(lower["updatevalueon"]),
			Selection:    mapParameterSelection(lower["selectnextrow"]),
			OnExhaustion: mapParameterExhaustion(firstNonEmpty(lower["outofvalues"], lower["whenoutofvalues"])),
		}
		rawType := strings.ToLower(firstNonEmpty(lower["type"], lower["valuetype"], lower["datatype"], lower["paramtype"]))
		switch {
		case strings.Contains(rawType, "random") && strings.Contains(rawType, "number"):
			param.Type = "random_number"
			param.Min = atoiDefault(firstNonEmpty(lower["minimumvalue"], lower["min"], lower["startvalue"]), 0)
			param.Max = atoiDefault(firstNonEmpty(lower["maximumvalue"], lower["max"], lower["endvalue"]), 100)
		case strings.Contains(rawType, "date"):
			param.Type = "date_time"
			param.Format = firstNonEmpty(lower["format"], lower["dateformat"], lower["datetimeformat"])
			param.Timezone = firstNonEmpty(lower["timezone"], lower["time_zone"])
			param.OffsetSeconds = atoiDefault(firstNonEmpty(lower["offsetseconds"], lower["offset"]), 0)
		case strings.Contains(rawType, "timestamp") || strings.Contains(rawType, "time stamp"):
			param.Type = "timestamp"
			param.Format = firstNonEmpty(lower["format"], lower["timestampformat"])
			param.Timezone = firstNonEmpty(lower["timezone"], lower["time_zone"])
			param.OffsetSeconds = atoiDefault(firstNonEmpty(lower["offsetseconds"], lower["offset"]), 0)
		case strings.Contains(rawType, "counter") || lower["increment"] != "":
			param.Type = "counter"
			param.Start = atoiDefault(firstNonEmpty(lower["startvalue"], lower["start"]), 0)
			param.End = atoiDefault(firstNonEmpty(lower["endvalue"], lower["end"]), 0)
			param.Step = atoiDefault(firstNonEmpty(lower["increment"], lower["step"]), 1)
		case strings.Contains(rawType, "uuid"):
			param.Type = "uuid"
		case strings.Contains(rawType, "random") && strings.Contains(rawType, "string"):
			param.Type = "random_string"
			param.Length = atoiDefault(firstNonEmpty(lower["length"], lower["size"]), 12)
			param.Charset = firstNonEmpty(lower["charset"], lower["characterset"])
		default:
			valueList := firstNonEmpty(lower["valuelist"], lower["values"], lower["valuelist1"])
			dataFile := firstNonEmpty(lower["table"], lower["filename"], lower["file"], lower["datafile"], lower["sourcefile"])
			if valueList != "" {
				param.Type = "list"
				param.Values = splitListValues(valueList)
			} else if dataFile != "" {
				param.Type = "file"
				column := firstNonEmpty(lower["columnname"], lower["column"], lower["colname"])
				if dataContent, ok := findFile(archiveFiles, dataFile); ok && column != "" {
					dataSourceName, exists := dataSourceByFile[strings.ToLower(filepath.Base(dataFile))]
					if !exists {
						dataSourceName = sanitizeParameterDataSourceName(dataFile)
						dataSourceByFile[strings.ToLower(filepath.Base(dataFile))] = dataSourceName
						result.DataSources = append(result.DataSources, dsl.DataSource{
							Name:           dataSourceName,
							Type:           parameterFileType(dataFile),
							InlineData:     dataContent,
							Strategy:       "sequential",
							FirstRowHeader: true,
						})
					}
					param.DataSource = dataSourceName
					param.Column = column
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
		if param.Type == "counter" && param.Step == 0 {
			param.Step = 1
		}
		if param.Type != "" {
			result.Parameters = append(result.Parameters, param)
		}
	}

	return result
}

func looksLikeParameterSection(sectionName string, values map[string]string) bool {
	sectionLower := strings.ToLower(sectionName)
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
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

func mapParameterExhaustion(value string) string {
	value = strings.ToLower(value)
	switch {
	case strings.Contains(value, "continue"), strings.Contains(value, "wrap"), strings.Contains(value, "cycle"):
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
