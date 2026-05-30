package api

import (
	"encoding/csv"
	"fmt"
	"io"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

type DataSourceFitReport struct {
	Status       string              `json:"status"`
	StatusCounts map[string]int      `json:"status_counts"`
	Items        []DataSourceFitItem `json:"items"`
}

type DataSourceFitItem struct {
	DataSource    string `json:"data_source"`
	Parameter     string `json:"parameter,omitempty"`
	Column        string `json:"column,omitempty"`
	Status        string `json:"status"`
	RequiredRows  int    `json:"required_rows,omitempty"`
	AvailableRows int    `json:"available_rows,omitempty"`
	Summary       string `json:"summary"`
}

type parsedDataSourceShape struct {
	columns []string
	rows    int
	known   bool
}

func buildDataSourceFitReport(script *dsl.Script) *DataSourceFitReport {
	if script == nil || (len(script.DataSources) == 0 && len(script.Parameters) == 0) {
		return nil
	}

	sources := make(map[string]dsl.DataSource, len(script.DataSources))
	shapes := make(map[string]parsedDataSourceShape, len(script.DataSources))
	items := make([]DataSourceFitItem, 0)
	for _, source := range script.DataSources {
		name := strings.TrimSpace(source.Name)
		if name == "" {
			items = append(items, DataSourceFitItem{Status: "blocked", Summary: "Data source name is required."})
			continue
		}
		sources[name] = source
		shape := parseDataSourceShape(source)
		shapes[name] = shape
		if !shape.known {
			items = append(items, DataSourceFitItem{
				DataSource: name,
				Status:     "needs_review",
				Summary:    "Row count is not available from inline data; verify the external data file before high-volume execution.",
			})
		}
	}

	for _, param := range script.Parameters {
		if strings.TrimSpace(param.DataSource) == "" {
			continue
		}
		items = append(items, evaluateParameterDataSourceFit(script, param, sources, shapes)...)
	}

	if len(items) == 0 {
		return &DataSourceFitReport{Status: "ready", StatusCounts: map[string]int{"ready": 1}, Items: []DataSourceFitItem{}}
	}
	counts := map[string]int{"ready": 0, "needs_review": 0, "blocked": 0}
	for _, item := range items {
		counts[item.Status]++
	}
	status := "ready"
	if counts["blocked"] > 0 {
		status = "blocked"
	} else if counts["needs_review"] > 0 {
		status = "needs_review"
	}
	return &DataSourceFitReport{Status: status, StatusCounts: counts, Items: items}
}

func evaluateParameterDataSourceFit(script *dsl.Script, param dsl.Parameter, sources map[string]dsl.DataSource, shapes map[string]parsedDataSourceShape) []DataSourceFitItem {
	sourceName := strings.TrimSpace(param.DataSource)
	column := strings.TrimSpace(param.Column)
	items := make([]DataSourceFitItem, 0, 2)
	source, ok := sources[sourceName]
	if !ok {
		return []DataSourceFitItem{{
			DataSource: sourceName,
			Parameter:  strings.TrimSpace(param.Name),
			Column:     column,
			Status:     "blocked",
			Summary:    "Referenced data source is not defined on the script.",
		}}
	}

	shape := shapes[sourceName]
	if column != "" && shape.known && !stringSliceContainsFold(shape.columns, column) {
		items = append(items, DataSourceFitItem{
			DataSource: sourceName,
			Parameter:  strings.TrimSpace(param.Name),
			Column:     column,
			Status:     "blocked",
			Summary:    "Referenced column is not present in the data source.",
		})
	}

	requiredRows := requiredRowsForParameter(script, param, source)
	if shape.known {
		status := "ready"
		summary := "Data source has enough rows for the modeled parameter usage."
		if requiresUniqueRows(param, source) && shape.rows < requiredRows {
			status = "blocked"
			summary = "Unique data usage requires more rows than the data source provides."
		} else if requiresUniqueRows(param, source) && strings.EqualFold(param.OnExhaustion, "wrap") {
			status = "needs_review"
			summary = "Unique data usage is configured to wrap on exhaustion; verify duplicate reuse is acceptable."
		}
		items = append(items, DataSourceFitItem{
			DataSource:    sourceName,
			Parameter:     strings.TrimSpace(param.Name),
			Column:        column,
			Status:        status,
			RequiredRows:  requiredRows,
			AvailableRows: shape.rows,
			Summary:       summary,
		})
	} else {
		items = append(items, DataSourceFitItem{
			DataSource:   sourceName,
			Parameter:    strings.TrimSpace(param.Name),
			Column:       column,
			Status:       "needs_review",
			RequiredRows: requiredRows,
			Summary:      "Data source row count is unknown; verify enough rows exist for the load model.",
		})
	}
	return items
}

func parseDataSourceShape(source dsl.DataSource) parsedDataSourceShape {
	inline := strings.TrimSpace(source.InlineData)
	if inline == "" {
		return parsedDataSourceShape{columns: append([]string(nil), source.Columns...)}
	}
	reader := csv.NewReader(strings.NewReader(inline))
	reader.FieldsPerRecord = -1
	if source.Delimiter != "" {
		reader.Comma = authoringDataSourceDelimiter(source.Delimiter, source.Type, inline)
	}
	records, err := reader.ReadAll()
	if err != nil && err != io.EOF {
		return parsedDataSourceShape{known: true}
	}
	if len(records) == 0 {
		return parsedDataSourceShape{known: true}
	}
	columns := append([]string(nil), source.Columns...)
	rowCount := len(records)
	if source.FirstRowHeader {
		columns = trimStringSlice(records[0])
		rowCount--
	}
	if len(columns) == 0 {
		for index := range records[0] {
			columns = append(columns, fmt.Sprintf("col%d", index+1))
		}
	}
	if rowCount < 0 {
		rowCount = 0
	}
	return parsedDataSourceShape{columns: columns, rows: rowCount, known: true}
}

func authoringDataSourceDelimiter(delimiter, sourceType, content string) rune {
	if delimiter == "\t" {
		return '\t'
	}
	switch strings.ToLower(strings.TrimSpace(delimiter)) {
	case "", "auto":
		if detected := detectInlineDataSourceDelimiter(content); detected != 0 {
			return detected
		}
		if sourceType == "dat" {
			return '\t'
		}
		return ','
	case `\t`, "tab":
		return '\t'
	case "comma":
		return ','
	default:
		return []rune(strings.TrimSpace(delimiter))[0]
	}
}

func detectInlineDataSourceDelimiter(content string) rune {
	commaScore := scoreInlineDataSourceDelimiter(content, ',')
	tabScore := scoreInlineDataSourceDelimiter(content, '\t')
	if tabScore == 0 && commaScore == 0 {
		return 0
	}
	if tabScore > commaScore {
		return '\t'
	}
	return ','
}

func scoreInlineDataSourceDelimiter(content string, delimiter rune) int {
	score := 0
	for index, line := range strings.Split(content, "\n") {
		if index >= 20 {
			break
		}
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" {
			continue
		}
		count := strings.Count(line, string(delimiter))
		if count > 0 {
			score += count + 1
		}
	}
	return score
}

func requiredRowsForParameter(script *dsl.Script, param dsl.Parameter, source dsl.DataSource) int {
	required := 1
	if script != nil && script.Config.RunLogic.Iterations > required {
		required = script.Config.RunLogic.Iterations
	}
	if script != nil && script.GoalConfig != nil {
		if script.GoalConfig.MaxVUsers != nil && *script.GoalConfig.MaxVUsers > 0 {
			required *= *script.GoalConfig.MaxVUsers
		} else if script.GoalConfig.MinVUsers != nil && *script.GoalConfig.MinVUsers > 0 {
			required *= *script.GoalConfig.MinVUsers
		}
	}
	if !requiresUniqueRows(param, source) {
		return 1
	}
	if required < 1 {
		return 1
	}
	return required
}

func requiresUniqueRows(param dsl.Parameter, source dsl.DataSource) bool {
	return strings.EqualFold(param.Selection, "unique") || strings.EqualFold(source.Strategy, "unique") || strings.EqualFold(source.Strategy, "pop")
}

func stringSliceContainsFold(values []string, want string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(want)) {
			return true
		}
	}
	return false
}

func trimStringSlice(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, strings.TrimSpace(value))
	}
	return out
}
