package requestimport

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
)

const (
	maxCredentialDataSourceBytes = 8 << 20
	maxCredentialDataSourceCells = 100_000
)

var parameterTemplatePattern = regexp.MustCompile(`\{\{\s*param\.([a-zA-Z0-9_.-]+)`)

type scriptCredentialDataBinding struct {
	source string
	column string
}

func trustedScriptParameterValues(script *dsl.Script) map[string]struct{} {
	if script == nil || len(script.Parameters) == 0 {
		return nil
	}
	nameCounts := make(map[string]int, len(script.Parameters))
	for _, parameter := range script.Parameters {
		name := strings.ToLower(strings.TrimSpace(parameter.Name))
		if name != "" {
			nameCounts[name]++
		}
	}
	trusted := make(map[string]struct{}, len(nameCounts))
	for name, count := range nameCounts {
		if count == 1 {
			trusted["{{param."+name+"}}"] = struct{}{}
		}
	}
	if len(trusted) == 0 {
		return nil
	}
	return trusted
}

// redactSensitiveRuntimeParameterValues preserves the runtime parameter and
// its data-source shape, but removes credential rows from the public draft.
// It intentionally scans every parameter, including unused parameters and
// values in otherwise policy-neutral request locations.
func redactSensitiveRuntimeParameterValues(script *dsl.Script) bool {
	if script == nil {
		return false
	}
	bindings := make([]scriptCredentialDataBinding, 0)
	changed := false
	for index := range script.Parameters {
		parameter := &script.Parameters[index]
		if !credential.SensitiveName(parameter.Name) {
			continue
		}
		for valueIndex, value := range parameter.Values {
			if value == credential.Redacted {
				continue
			}
			parameter.Values[valueIndex] = credential.Redacted
			changed = true
		}
		if source := strings.TrimSpace(parameter.DataSource); source != "" {
			bindings = append(bindings, scriptCredentialDataBinding{
				source: source,
				column: strings.TrimSpace(parameter.Column),
			})
		}
	}
	return redactCredentialDataSourceColumns(script, bindings) || changed
}

func redactCredentialDataSourceColumns(
	script *dsl.Script,
	bindings []scriptCredentialDataBinding,
) bool {
	if script == nil || len(bindings) == 0 {
		return false
	}
	protected := make(map[string][]string, len(bindings))
	for _, binding := range bindings {
		source := strings.ToLower(strings.TrimSpace(binding.source))
		column := strings.TrimSpace(binding.column)
		if source == "" || column == "" {
			continue
		}
		protected[source] = append(protected[source], column)
	}

	changed := false
	for sourceIndex := range script.DataSources {
		source := &script.DataSources[sourceIndex]
		requiredColumns := protected[strings.ToLower(strings.TrimSpace(source.Name))]
		if len(requiredColumns) == 0 || source.InlineData == "" {
			continue
		}
		columns, rows, delimiter, ok := parseCredentialDataSourceForColumns(
			*source,
			requiredColumns,
		)
		if !ok {
			source.InlineData = credential.Redacted
			source.Encoding = ""
			changed = true
			continue
		}
		redactedIndexes := make(map[int]struct{}, len(requiredColumns))
		for _, column := range requiredColumns {
			if index := uniqueFoldedColumnIndex(columns, column); index >= 0 {
				redactedIndexes[index] = struct{}{}
			}
		}
		for rowIndex := range rows {
			for columnIndex := range redactedIndexes {
				if columnIndex >= len(rows[rowIndex]) {
					continue
				}
				rows[rowIndex][columnIndex] = credential.Redacted
				changed = true
			}
		}
		records := rows
		if source.FirstRowHeader {
			records = append([][]string{columns}, rows...)
		}
		var output bytes.Buffer
		writer := csv.NewWriter(&output)
		writer.Comma = delimiter
		writer.WriteAll(records)
		if err := writer.Error(); err != nil {
			source.InlineData = credential.Redacted
			source.Encoding = ""
			changed = true
			continue
		}
		source.InlineData = output.String()
		source.Encoding = ""
		// A detected delimiter must become explicit when the data-source type
		// default would otherwise parse the rewritten rows differently.
		if dsl.ResolveDataSourceDelimiter(*source, source.InlineData) != delimiter {
			source.Delimiter = string(delimiter)
		}
	}
	return changed
}

func parseCredentialDataSourceForColumns(
	source dsl.DataSource,
	requiredColumns []string,
) ([]string, [][]string, rune, bool) {
	for _, delimiter := range credentialDataSourceDelimiterCandidates(source) {
		columns, rows, ok := parseCredentialDataSourceWithDelimiter(source, delimiter)
		if !ok {
			continue
		}
		allPresent := true
		for _, column := range requiredColumns {
			if uniqueFoldedColumnIndex(columns, column) < 0 {
				allPresent = false
				break
			}
		}
		if allPresent {
			return columns, rows, delimiter, true
		}
	}
	return nil, nil, 0, false
}

func credentialDataSourceDelimiterCandidates(source dsl.DataSource) []rune {
	candidates := []rune{dsl.ResolveDataSourceDelimiter(source, source.InlineData)}
	if detected := dsl.DetectDataSourceDelimiter(source.InlineData); detected != 0 {
		candidates = append(candidates, detected)
	}
	candidates = append(candidates, ',', '\t', ';', '|')
	unique := candidates[:0]
	for _, candidate := range candidates {
		if candidate == 0 {
			continue
		}
		seen := false
		for _, existing := range unique {
			if existing == candidate {
				seen = true
				break
			}
		}
		if !seen {
			unique = append(unique, candidate)
		}
	}
	return unique
}

// ReconcileProtectedScriptCredentialSources upgrades requirements created by
// an earlier conversion pass before source parameters were attached. It keeps
// stable requirement identities and targets while moving any now-provable
// static source value into the protected requirement and removing its public
// parameter/data-source representation.
//
// Requirement.Value remains protected material. Callers must not serialize or
// log the returned slice.
func ReconcileProtectedScriptCredentialSources(
	script *dsl.Script,
	requirements []credentialpolicy.Requirement,
	sourceProfile string,
	candidateKey string,
) []credentialpolicy.Requirement {
	reconciled := credentialpolicy.CloneRequirements(requirements)
	if script == nil || len(reconciled) == 0 {
		return reconciled
	}
	sourceVariables := literalScriptSourceVariables(script, sourceProfile)
	candidateRequirements := make([]credentialpolicy.Requirement, 0)
	for index := range reconciled {
		requirement := &reconciled[index]
		if !credentialRequirementTargetsCandidate(*requirement, candidateKey) {
			continue
		}
		sourceExpression := requirement.Value
		if strings.TrimSpace(requirement.DeferredSourceExpression) != "" {
			sourceExpression = requirement.DeferredSourceExpression
		}
		materialized, names := credentialpolicy.MaterializeSourceVariables(
			sourceExpression,
			credentialpolicy.Options{SourceVariables: sourceVariables},
		)
		if len(names) > 0 && !credentialpolicy.HasRuntimeTemplate(materialized) {
			requirement.Value = materialized
			requirement.DeferredSourceExpression = ""
			requirement.SourceVariableNames = mergeSourceVariableNames(
				requirement.SourceVariableNames,
				names,
			)
			requirement.SourceValueAvailable = materialized != ""
		} else if requirement.DeferredSourceExpression != "" {
			// Partial materialization is not an executable project-secret value:
			// a second template pass is not guaranteed, and persisting the
			// expression would falsely claim the source credential was present.
			requirement.Value = ""
			requirement.SourceValueAvailable = false
			requirement.SourceVariableNames = mergeSourceVariableNames(
				requirement.SourceVariableNames,
				names,
			)
		}
		candidateRequirements = append(candidateRequirements, *requirement)
	}
	removeConsumedLiteralScriptParameters(script, candidateRequirements)
	redactCredentialSourceVariables(script, candidateRequirements)
	return reconciled
}

func credentialRequirementTargetsCandidate(
	requirement credentialpolicy.Requirement,
	candidateKey string,
) bool {
	candidateKey = strings.TrimSpace(candidateKey)
	if candidateKey == "" {
		return false
	}
	for _, target := range requirement.Targets {
		requestKey := strings.TrimSpace(target.RequestKey)
		if requestKey == candidateKey ||
			strings.HasPrefix(requestKey, candidateKey+":") {
			return true
		}
	}
	return false
}

func collectParameterReferences(value string, set map[string]struct{}) {
	for _, match := range parameterTemplatePattern.FindAllStringSubmatch(value, -1) {
		if len(match) != 2 {
			continue
		}
		if name := strings.TrimSpace(match[1]); name != "" {
			set[name] = struct{}{}
		}
	}
}

func stringSetContainsFold(set map[string]struct{}, value string) bool {
	value = strings.TrimSpace(value)
	for candidate := range set {
		if strings.EqualFold(strings.TrimSpace(candidate), value) {
			return true
		}
	}
	return false
}

func constantDataSourceParameterVariable(
	script *dsl.Script,
	parameter dsl.Parameter,
	sourceProfile string,
) (credentialpolicy.SourceVariable, bool) {
	sourceName := strings.TrimSpace(parameter.DataSource)
	columnName := strings.TrimSpace(parameter.Column)
	if script == nil || sourceName == "" || columnName == "" {
		return credentialpolicy.SourceVariable{}, false
	}
	sourceIndex := -1
	for index := range script.DataSources {
		if !strings.EqualFold(strings.TrimSpace(script.DataSources[index].Name), sourceName) {
			continue
		}
		if sourceIndex >= 0 {
			// Case-colliding source names are ambiguous at import time.
			return credentialpolicy.SourceVariable{}, false
		}
		sourceIndex = index
	}
	if sourceIndex < 0 {
		return credentialpolicy.SourceVariable{}, false
	}
	columns, rows, _, ok := parseCredentialDataSourceForColumns(
		script.DataSources[sourceIndex],
		[]string{columnName},
	)
	if !ok || len(rows) == 0 {
		return credentialpolicy.SourceVariable{}, false
	}
	columnIndex := uniqueFoldedColumnIndex(columns, columnName)
	if columnIndex < 0 {
		return credentialpolicy.SourceVariable{}, false
	}
	var value string
	for rowIndex, row := range rows {
		if columnIndex >= len(row) {
			return credentialpolicy.SourceVariable{}, false
		}
		if rowIndex == 0 {
			value = row[columnIndex]
			continue
		}
		if row[columnIndex] != value {
			// One project secret cannot preserve a rotating source credential.
			return credentialpolicy.SourceVariable{}, false
		}
	}
	return credentialpolicy.SourceVariable{
		Value: value,
		Source: credentialpolicy.SourceRef{
			Format: sourceProfile,
			Pointer: fmt.Sprintf(
				"/data_sources/%d/inline_data/%s",
				sourceIndex,
				columnName,
			),
		},
	}, true
}

func parseCredentialDataSource(source dsl.DataSource) ([]string, [][]string, bool) {
	return parseCredentialDataSourceWithDelimiter(
		source,
		dsl.ResolveDataSourceDelimiter(source, source.InlineData),
	)
}

func parseCredentialDataSourceWithDelimiter(
	source dsl.DataSource,
	delimiter rune,
) ([]string, [][]string, bool) {
	if len(source.InlineData) == 0 ||
		len(source.InlineData) > maxCredentialDataSourceBytes {
		return nil, nil, false
	}
	reader := csv.NewReader(strings.NewReader(source.InlineData))
	reader.Comma = delimiter
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true

	records := make([][]string, 0, 16)
	cellCount := 0
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil || len(record) > maxCredentialDataSourceCells-cellCount {
			return nil, nil, false
		}
		cellCount += len(record)
		records = append(records, record)
	}
	if len(records) == 0 {
		return nil, nil, false
	}
	if source.FirstRowHeader {
		if len(records) < 2 {
			return nil, nil, false
		}
		return records[0], records[1:], true
	}
	if len(source.Columns) == 0 {
		return nil, nil, false
	}
	return append([]string(nil), source.Columns...), records, true
}

func uniqueFoldedColumnIndex(columns []string, name string) int {
	found := -1
	for index, column := range columns {
		if !strings.EqualFold(strings.TrimSpace(column), strings.TrimSpace(name)) {
			continue
		}
		if found >= 0 {
			return -1
		}
		found = index
	}
	return found
}

func parameterUsesCredentialDataBinding(
	parameter dsl.Parameter,
	bindings []scriptCredentialDataBinding,
) bool {
	source := strings.TrimSpace(parameter.DataSource)
	column := strings.TrimSpace(parameter.Column)
	if source == "" {
		return false
	}
	for _, binding := range bindings {
		if !strings.EqualFold(strings.TrimSpace(binding.source), source) {
			continue
		}
		if strings.TrimSpace(binding.column) == "" ||
			strings.EqualFold(strings.TrimSpace(binding.column), column) {
			return true
		}
	}
	return false
}

func removeCredentialDataSourceColumns(
	script *dsl.Script,
	bindings []scriptCredentialDataBinding,
) {
	if script == nil || len(bindings) == 0 {
		return
	}
	protected := make(map[string]map[string]struct{}, len(bindings))
	for _, binding := range bindings {
		source := strings.ToLower(strings.TrimSpace(binding.source))
		if source == "" {
			continue
		}
		if protected[source] == nil {
			protected[source] = make(map[string]struct{})
		}
		protected[source][strings.ToLower(strings.TrimSpace(binding.column))] = struct{}{}
	}
	if len(protected) == 0 {
		return
	}

	referenced := make(map[string]struct{}, len(script.Parameters))
	for _, parameter := range script.Parameters {
		if source := strings.ToLower(strings.TrimSpace(parameter.DataSource)); source != "" {
			referenced[source] = struct{}{}
		}
	}
	unusable := make(map[string]struct{})
	keptSources := script.DataSources[:0]
	for index := range script.DataSources {
		source := script.DataSources[index]
		sourceName := strings.ToLower(strings.TrimSpace(source.Name))
		columns, affected := protected[sourceName]
		if !affected {
			keptSources = append(keptSources, source)
			continue
		}
		if _, stillReferenced := referenced[sourceName]; !stillReferenced {
			// Omitting an unreferenced source is the only way to guarantee its
			// protected raw column cannot leave the conversion boundary.
			continue
		}
		filtered, ok := filterCredentialDataSourceColumns(source, columns)
		if !ok {
			// External/opaque or malformed sources cannot be safely rewritten.
			// Remove the source and any parameters that depended on it.
			unusable[sourceName] = struct{}{}
			continue
		}
		keptSources = append(keptSources, filtered)
	}
	script.DataSources = keptSources
	if len(unusable) == 0 {
		return
	}
	keptParameters := script.Parameters[:0]
	for _, parameter := range script.Parameters {
		sourceName := strings.ToLower(strings.TrimSpace(parameter.DataSource))
		if _, remove := unusable[sourceName]; remove {
			for index := range parameter.Values {
				parameter.Values[index] = ""
			}
			continue
		}
		keptParameters = append(keptParameters, parameter)
	}
	script.Parameters = keptParameters
}

func filterCredentialDataSourceColumns(
	source dsl.DataSource,
	protected map[string]struct{},
) (dsl.DataSource, bool) {
	columns, _, ok := parseCredentialDataSource(source)
	if !ok {
		return dsl.DataSource{}, false
	}
	drop := make(map[int]struct{}, len(protected))
	protectAll := false
	if _, protectAll = protected[""]; protectAll {
		for index := range columns {
			drop[index] = struct{}{}
		}
	} else {
		for name := range protected {
			index := uniqueFoldedColumnIndex(columns, name)
			if index < 0 {
				return dsl.DataSource{}, false
			}
			drop[index] = struct{}{}
		}
	}
	if len(drop) == 0 || len(drop) == len(columns) {
		return dsl.DataSource{}, false
	}

	reader := csv.NewReader(strings.NewReader(source.InlineData))
	delimiter := dsl.ResolveDataSourceDelimiter(source, source.InlineData)
	reader.Comma = delimiter
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true
	var output bytes.Buffer
	writer := csv.NewWriter(&output)
	writer.Comma = delimiter
	cellCount := 0
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil || len(record) > maxCredentialDataSourceCells-cellCount {
			return dsl.DataSource{}, false
		}
		cellCount += len(record)
		filtered := make([]string, 0, len(record)-len(drop))
		for index, value := range record {
			if _, remove := drop[index]; !remove {
				filtered = append(filtered, value)
			}
		}
		if err := writer.Write(filtered); err != nil {
			return dsl.DataSource{}, false
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return dsl.DataSource{}, false
	}
	source.InlineData = output.String()
	if len(source.Columns) > 0 {
		filteredColumns := make([]string, 0, len(source.Columns)-len(drop))
		for index, column := range source.Columns {
			if _, remove := drop[index]; !remove {
				filteredColumns = append(filteredColumns, column)
			}
		}
		source.Columns = filteredColumns
	}
	return source, true
}
