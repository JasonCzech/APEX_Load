package engine

import (
	"encoding/csv"
	"errors"
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// Retained-credential locators for the two executable surfaces an import can
// leave a credential value in: a script parameter's inline value list, and the
// inline data of the data source a parameter reads its column from.
//
// pkg/convert/requestimport publishes these locators instead of overwriting
// those values with the redaction sentinel — rewriting them destroyed runnable
// imported data (a value list rewritten to "[REDACTED]" sends that literal on
// the wire, and a blanked inline data source strands every other column in the
// same CSV). Publishing a locator only works if the runtime can resolve it:
// ImportedCredentialValues is the fail-closed locator-integrity gate that
// engine.New, ReplayScript, NewMultiGroup, RunScriptShakeoutWithSecretResolver
// and the generator RPC staging boundary all call, so a locator field the
// runtime does not understand refuses the run instead of redacting anything.
//
// Resolution mirrors ParameterRuntime.valuesForParameter: inline Values when
// the parameter has them, otherwise the named data-source column. The matcher
// therefore registers what the runtime actually substitutes and nothing else.
const (
	importedParameterValuesTargetPrefix = "parameters["
	importedParameterValuesTargetSuffix = "].values"
	importedDataSourceTargetPrefix      = "data_sources["
	importedDataSourceTargetSuffix      = "].inline_data"
)

// Bounds for locator-driven inline-data parsing. They match the bounds
// pkg/convert/requestimport applies when it parses the same inline data to
// prove a credential column, so neither side can consider a source inspectable
// that the other cannot read. Exceeding them yields no values rather than an
// error: script-sourced run-evidence redaction is best effort and must never
// fail a run (see script_sourced_run_evidence.go), while the locator-integrity
// gate still fails closed on a locator that no longer addresses anything.
const (
	maxImportedDataSourceLocatorBytes = 8 << 20
	maxImportedDataSourceLocatorCells = 100_000
)

// collectScriptParameterValuesTarget resolves a "parameters[N].values" locator.
//
// An out-of-range index fails closed: the locator no longer addresses anything,
// which means the parameter list moved under it and the credential may now live
// in a field nothing is masking. An in-range parameter with no usable value is
// not a failure — a preceding conversion pass may legitimately have emptied it,
// and an empty value carries nothing to redact.
func (collector *importedCredentialCollector) collectScriptParameterValuesTarget(
	field string,
) error {
	index, ok := importedCredentialBracketIndex(field, importedParameterValuesTargetPrefix)
	if !ok {
		return errors.New("retained parameter credential locator is invalid")
	}
	if index >= len(collector.script.Parameters) {
		return errors.New("retained parameter credential locator no longer matches executable script data")
	}
	for _, value := range collector.script.Parameters[index].Values {
		collector.add(value)
	}
	return nil
}

// collectScriptDataSourceTarget resolves a "data_sources[N].inline_data"
// locator. The credential-supplying column travels in the locator's Pointer.
func (collector *importedCredentialCollector) collectScriptDataSourceTarget(
	field string,
	column string,
) error {
	index, ok := importedCredentialBracketIndex(field, importedDataSourceTargetPrefix)
	if !ok {
		return errors.New("retained data source credential locator is invalid")
	}
	if index >= len(collector.script.DataSources) {
		return errors.New("retained data source credential locator no longer matches executable script data")
	}
	if strings.TrimSpace(column) == "" {
		// The locator does not name a column, so nothing identifies which cells
		// the credential came from. Registering the whole sheet instead would
		// redact unrelated columns out of the operator's own run evidence — one
		// "1" or "true" cell becomes a matcher that mangles every log line that
		// contains it — so an unscoped locator contributes no values. It still
		// resolves: an unresolvable locator would refuse the run outright.
		return nil
	}
	for _, value := range importedDataSourceColumnValues(collector.script.DataSources[index], column) {
		collector.add(value)
	}
	return nil
}

// importedDataSourceColumnValues returns the runtime values one inline data
// source column supplies. Column naming and cell trimming mirror loadDataSource
// and DataProvider.GetColumnValues so the matcher sees the same strings a
// parameter substitutes on the wire.
func importedDataSourceColumnValues(source dsl.DataSource, column string) []string {
	if len(source.InlineData) == 0 ||
		len(source.InlineData) > maxImportedDataSourceLocatorBytes {
		return nil
	}
	reader := csv.NewReader(strings.NewReader(source.InlineData))
	reader.Comma = resolveDataSourceDelimiter(source, source.InlineData)
	// Ragged rows are tolerated here even though the runtime loader rejects
	// them: a source that fails to load never runs, but while it is being
	// inspected the rows that did parse are still real imported values.
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true

	records := make([][]string, 0, 16)
	cells := 0
	for {
		record, err := reader.Read()
		if err != nil {
			break
		}
		if len(record) > maxImportedDataSourceLocatorCells-cells {
			break
		}
		cells += len(record)
		records = append(records, record)
	}
	if len(records) == 0 {
		return nil
	}

	var columns []string
	rows := records
	switch {
	case source.FirstRowHeader:
		columns = records[0]
		rows = records[1:]
	case len(source.Columns) > 0:
		columns = source.Columns
	default:
		columns = make([]string, len(records[0]))
		for index := range columns {
			columns[index] = fmt.Sprintf("col_%d", index+1)
		}
	}
	columnIndex := importedDataSourceColumnIndex(columns, column)
	if columnIndex < 0 {
		return nil
	}
	values := make([]string, 0, len(rows))
	for _, row := range rows {
		if columnIndex >= len(row) {
			continue
		}
		if value := strings.TrimSpace(row[columnIndex]); value != "" {
			values = append(values, value)
		}
	}
	return values
}

// importedDataSourceColumnIndex resolves a column name the way both sides of
// the contract do: exactly as the runtime provider keys its columns, then — for
// a header whose case differs from the parameter binding — the importer's
// unique case-insensitive match.
func importedDataSourceColumnIndex(columns []string, column string) int {
	column = strings.TrimSpace(column)
	for index, candidate := range columns {
		if strings.TrimSpace(candidate) == column {
			return index
		}
	}
	found := -1
	for index, candidate := range columns {
		if !strings.EqualFold(strings.TrimSpace(candidate), column) {
			continue
		}
		if found >= 0 {
			return -1
		}
		found = index
	}
	return found
}
