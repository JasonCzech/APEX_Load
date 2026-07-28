package scriptdisclosure

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"io"
	"strings"

	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
)

const (
	maxDataSourceBytes = 8 << 20
	maxDataSourceCells = 100_000
)

func (p *projector) projectDataSource(raw map[string]any) map[string]any {
	payload, err := json.Marshal(raw)
	if err != nil {
		return raw
	}
	var source dsl.DataSource
	if json.Unmarshal(payload, &source) != nil || strings.TrimSpace(source.InlineData) == "" {
		return raw
	}
	requiredColumns := p.sensitiveSourceColumns[canonicalName(source.Name)]
	sourceSensitive := isSensitiveKey(source.Name)
	if strings.EqualFold(strings.TrimSpace(source.Encoding), "base64") {
		if sourceSensitive || len(requiredColumns) > 0 {
			copied := cloneMap(raw)
			copied["inline_data"] = credential.Redacted
			return copied
		}
		return raw
	}
	projected, sensitive, ok := projectDataSourceTable(source, requiredColumns, sourceSensitive)
	if !ok && (sourceSensitive || len(requiredColumns) > 0) {
		copied := cloneMap(raw)
		copied["inline_data"] = credential.Redacted
		return copied
	}
	if !ok || !sensitive {
		return raw
	}
	copied := cloneMap(raw)
	copied["inline_data"] = projected
	return copied
}

func (c *secretCollector) collectDataSource(raw map[string]any) {
	payload, err := json.Marshal(raw)
	if err != nil {
		return
	}
	var source dsl.DataSource
	if json.Unmarshal(payload, &source) != nil || strings.TrimSpace(source.InlineData) == "" {
		return
	}
	requiredColumns := c.sensitiveSourceColumns[canonicalName(source.Name)]
	sourceSensitive := isSensitiveKey(source.Name)
	if strings.EqualFold(strings.TrimSpace(source.Encoding), "base64") {
		if sourceSensitive || len(requiredColumns) > 0 {
			c.add(source.InlineData)
		}
		return
	}
	records, sensitiveIndexes, dataStart, ok := parseSensitiveDataSource(
		source,
		requiredColumns,
		sourceSensitive,
	)
	if !ok {
		return
	}
	for rowIndex := dataStart; rowIndex < len(records); rowIndex++ {
		for columnIndex, sensitive := range sensitiveIndexes {
			if sensitive && columnIndex < len(records[rowIndex]) {
				c.add(records[rowIndex][columnIndex])
			}
		}
	}
}

func projectDataSourceTable(
	source dsl.DataSource,
	requiredColumns []string,
	sourceSensitive bool,
) (string, bool, bool) {
	records, sensitiveIndexes, dataStart, ok := parseSensitiveDataSource(
		source,
		requiredColumns,
		sourceSensitive,
	)
	if !ok {
		return "", false, false
	}
	sensitive := false
	for _, item := range sensitiveIndexes {
		sensitive = sensitive || item
	}
	if !sensitive {
		return source.InlineData, false, true
	}
	for rowIndex := dataStart; rowIndex < len(records); rowIndex++ {
		for columnIndex, protected := range sensitiveIndexes {
			if !protected || columnIndex >= len(records[rowIndex]) {
				continue
			}
			value := records[rowIndex][columnIndex]
			if value == "" || isPortableBinding(value) ||
				value == credential.Redacted {
				continue
			}
			records[rowIndex][columnIndex] = credential.Redacted
		}
	}

	var output bytes.Buffer
	writer := csv.NewWriter(&output)
	writer.Comma = dsl.ResolveDataSourceDelimiter(source, source.InlineData)
	writer.UseCRLF = strings.Contains(source.InlineData, "\r\n")
	for _, record := range records {
		if err := writer.Write(record); err != nil {
			return "", false, false
		}
	}
	writer.Flush()
	if writer.Error() != nil {
		return "", false, false
	}
	projected := output.String()
	if !strings.HasSuffix(source.InlineData, "\n") {
		projected = strings.TrimSuffix(projected, "\n")
		if writer.UseCRLF {
			projected = strings.TrimSuffix(projected, "\r")
		}
	}
	return projected, true, true
}

func parseSensitiveDataSource(
	source dsl.DataSource,
	requiredColumns []string,
	sourceSensitive bool,
) ([][]string, []bool, int, bool) {
	if len(source.InlineData) > maxDataSourceBytes {
		return nil, nil, 0, false
	}
	reader := csv.NewReader(strings.NewReader(source.InlineData))
	reader.Comma = dsl.ResolveDataSourceDelimiter(source, source.InlineData)
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true

	records := make([][]string, 0, 16)
	cellCount := 0
	maxColumns := 0
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil || len(record) > maxDataSourceCells-cellCount {
			return nil, nil, 0, false
		}
		cellCount += len(record)
		if len(record) > maxColumns {
			maxColumns = len(record)
		}
		records = append(records, record)
	}
	if len(records) == 0 || maxColumns == 0 {
		return records, make([]bool, maxColumns), 0, true
	}

	columns := append([]string(nil), source.Columns...)
	dataStart := 0
	if source.FirstRowHeader {
		columns = records[0]
		dataStart = 1
	} else if len(columns) == 0 && recordLooksLikeCredentialHeader(records[0], requiredColumns) {
		columns = records[0]
		dataStart = 1
	}
	sensitiveIndexes := make([]bool, maxColumns)
	for index, column := range columns {
		if index >= len(sensitiveIndexes) {
			break
		}
		column = strings.TrimPrefix(column, "\ufeff")
		if isSensitiveKey(column) || columnRequired(column, requiredColumns) {
			sensitiveIndexes[index] = true
		}
	}
	knownSensitive := false
	for _, sensitive := range sensitiveIndexes {
		knownSensitive = knownSensitive || sensitive
	}
	if !knownSensitive && (sourceSensitive || len(requiredColumns) > 0) {
		for index := range sensitiveIndexes {
			sensitiveIndexes[index] = true
		}
	}
	return records, sensitiveIndexes, dataStart, true
}

func recordLooksLikeCredentialHeader(record, requiredColumns []string) bool {
	for _, column := range record {
		column = strings.TrimPrefix(column, "\ufeff")
		if isSensitiveKey(column) || columnRequired(column, requiredColumns) {
			return true
		}
	}
	return false
}

func columnRequired(column string, requiredColumns []string) bool {
	column = strings.TrimSpace(strings.TrimPrefix(column, "\ufeff"))
	if column == "" {
		return false
	}
	for _, required := range requiredColumns {
		if required != "" && strings.EqualFold(column, strings.TrimSpace(required)) {
			return true
		}
	}
	return false
}

func cloneMap(value map[string]any) map[string]any {
	out := make(map[string]any, len(value))
	for key, child := range value {
		out[key] = child
	}
	return out
}
