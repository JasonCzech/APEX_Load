package sharedvalue

import (
	"encoding/csv"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// constantBinding describes where a script keeps the single value behind a
// parameter, so it can be read for display and rewritten on apply.
//
// Two shapes are in use, and both mean "one constant for the whole run":
//   - inline: dsl.Parameter{Type:"list", Values:[one value]} — RAML, Bruno
//     native, and Postman with the parameter data set turned off.
//   - dataSource: a parameter bound to a one-data-row inline CSV — Postman with
//     the parameter data set turned on, which is its default for collections.
//
// Only the one-row case qualifies. A CSV with several rows is real per-VUser
// test data, and collapsing it to a single operator-entered value would destroy
// that data.
type constantBinding struct {
	value string
	// inline is true for Parameter.Values; otherwise the value lives in the
	// named data source's single CSV row under column.
	inline         bool
	dataSourceName string
	column         string
}

func constantParameterBinding(script *dsl.Script, param dsl.Parameter) (constantBinding, bool) {
	if !strings.EqualFold(strings.TrimSpace(param.Type), "list") &&
		!strings.EqualFold(strings.TrimSpace(param.Type), "file") {
		return constantBinding{}, false
	}
	source := strings.TrimSpace(param.DataSource)
	if source == "" {
		if len(param.Values) != 1 {
			return constantBinding{}, false
		}
		return constantBinding{value: param.Values[0], inline: true}, true
	}
	column := strings.TrimSpace(param.Column)
	if column == "" {
		return constantBinding{}, false
	}
	index := dataSourceIndex(script, source)
	if index < 0 {
		return constantBinding{}, false
	}
	header, rows, ok := parseInlineCSV(script.DataSources[index])
	if !ok || len(rows) != 1 {
		return constantBinding{}, false
	}
	columnIndex := columnPosition(header, column)
	if columnIndex < 0 || columnIndex >= len(rows[0]) {
		return constantBinding{}, false
	}
	return constantBinding{value: rows[0][columnIndex], dataSourceName: source, column: column}, true
}

// writeConstantBinding stores value at the binding's location and reports the
// previous value plus whether anything changed.
func writeConstantBinding(script *dsl.Script, param *dsl.Parameter, binding constantBinding, value string) (before string, changed bool) {
	if binding.inline {
		if len(param.Values) == 1 {
			before = param.Values[0]
		}
		if before == value {
			return before, false
		}
		param.Values = []string{value}
		return before, true
	}
	index := dataSourceIndex(script, binding.dataSourceName)
	if index < 0 {
		return "", false
	}
	header, rows, ok := parseInlineCSV(script.DataSources[index])
	if !ok || len(rows) != 1 {
		return "", false
	}
	columnIndex := columnPosition(header, binding.column)
	if columnIndex < 0 || columnIndex >= len(rows[0]) {
		return "", false
	}
	before = rows[0][columnIndex]
	if before == value {
		return before, false
	}
	rows[0][columnIndex] = value
	encoded, ok := encodeInlineCSV(script.DataSources[index], header, rows)
	if !ok {
		return before, false
	}
	script.DataSources[index].InlineData = encoded
	return before, true
}

func dataSourceIndex(script *dsl.Script, name string) int {
	if script == nil {
		return -1
	}
	target := strings.ToLower(strings.TrimSpace(name))
	for i := range script.DataSources {
		if strings.ToLower(strings.TrimSpace(script.DataSources[i].Name)) == target {
			return i
		}
	}
	return -1
}

func columnPosition(header []string, column string) int {
	for i, name := range header {
		if strings.EqualFold(strings.TrimSpace(name), strings.TrimSpace(column)) {
			return i
		}
	}
	return -1
}

// inlineCSVDelimiter resolves the stored delimiter spelling. An ambiguous or
// unrecognized delimiter returns false: guessing would risk rewriting the
// operator's data into a different shape than it was read in.
func inlineCSVDelimiter(source dsl.DataSource) (rune, bool) {
	switch strings.ToLower(strings.TrimSpace(source.Delimiter)) {
	case "", ",", "comma", "auto":
		return ',', true
	case "\t", "tab":
		return '\t', true
	default:
		return 0, false
	}
}

func parseInlineCSV(source dsl.DataSource) (header []string, rows [][]string, ok bool) {
	if !strings.EqualFold(strings.TrimSpace(source.Type), "csv") ||
		strings.TrimSpace(source.InlineData) == "" ||
		strings.TrimSpace(source.StorageRef) != "" {
		return nil, nil, false
	}
	delimiter, ok := inlineCSVDelimiter(source)
	if !ok {
		return nil, nil, false
	}
	reader := csv.NewReader(strings.NewReader(source.InlineData))
	reader.Comma = delimiter
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil || len(records) == 0 {
		return nil, nil, false
	}
	if source.FirstRowHeader {
		return records[0], records[1:], true
	}
	if len(source.Columns) == 0 {
		return nil, nil, false
	}
	return source.Columns, records, true
}

func encodeInlineCSV(source dsl.DataSource, header []string, rows [][]string) (string, bool) {
	delimiter, ok := inlineCSVDelimiter(source)
	if !ok {
		return "", false
	}
	var builder strings.Builder
	writer := csv.NewWriter(&builder)
	writer.Comma = delimiter
	records := rows
	if source.FirstRowHeader {
		records = append([][]string{header}, rows...)
	}
	if err := writer.WriteAll(records); err != nil {
		return "", false
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return "", false
	}
	return builder.String(), true
}
