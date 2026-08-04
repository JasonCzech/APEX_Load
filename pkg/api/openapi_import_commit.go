package api

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/fileasset"
)

func diffOpenAPIImport(existing *dsl.Script, imported *dsl.Script, replaceMode string) openAPIImportDiff {
	return diffImportByKey(existing, imported, replaceMode, openAPIActionKey)
}

// diffImportByKey summarizes what a commit/replace will change, using keyFn to
// decide which actions correspond across the existing and imported scripts.
// Shared by the OpenAPI and Postman import-commit handlers.
func diffImportByKey(existing *dsl.Script, imported *dsl.Script, replaceMode string, keyFn func(dsl.Action) string) openAPIImportDiff {
	diff := openAPIImportDiff{ReplaceMode: replaceMode}
	if existing == nil || imported == nil {
		return diff
	}
	if replaceMode == "full" {
		diff.Added = len(imported.Actions)
		diff.Removed = len(existing.Actions)
		diff.Items = []string{fmt.Sprintf("Full replace: %d existing action(s) replaced by %d imported action(s)", len(existing.Actions), len(imported.Actions))}
		return diff
	}
	existingKeys := actionKeySet(existing.Actions, keyFn)
	importedKeys := actionKeySet(imported.Actions, keyFn)
	for key := range importedKeys {
		if _, ok := existingKeys[key]; ok {
			diff.Updated++
			diff.Items = append(diff.Items, "Updated "+key)
		} else {
			diff.Added++
			diff.Items = append(diff.Items, "Added "+key)
		}
	}
	for key := range existingKeys {
		if _, ok := importedKeys[key]; !ok {
			diff.Removed++
			diff.Items = append(diff.Items, "Removed "+key)
		}
	}
	for _, action := range existing.Actions {
		if keyFn(action) == "" {
			diff.Preserved++
		}
	}
	// Surface the data customizations the merge preserves so the dry-run diff is
	// honest about what survives (see mergeImportScriptByKey).
	_, newColumns := mergeImportDataSources(existing.DataSources, imported.DataSources)
	diff.PreservedDataSources = len(existing.DataSources)
	diff.PreservedParameters = len(existing.Parameters)
	diff.NewColumns = dedupSortedStrings(newColumns)
	if diff.PreservedDataSources > 0 {
		item := fmt.Sprintf("Preserved %d existing data source(s)", diff.PreservedDataSources)
		if len(diff.NewColumns) > 0 {
			item += fmt.Sprintf("; merged %d new column(s)", len(diff.NewColumns))
		}
		diff.Items = append(diff.Items, item)
	}
	if diff.PreservedParameters > 0 {
		diff.Items = append(diff.Items, fmt.Sprintf("Preserved %d existing parameter(s)", diff.PreservedParameters))
	}
	if merged := mergeImportLifecycle(existing.Init, imported.Init); !reflect.DeepEqual(merged, existing.Init) {
		diff.Items = append(diff.Items, fmt.Sprintf("Updated init lifecycle (%d step(s))", len(merged)))
	}
	if merged := mergeImportLifecycle(existing.Teardown, imported.Teardown); !reflect.DeepEqual(merged, existing.Teardown) {
		diff.Items = append(diff.Items, fmt.Sprintf("Updated teardown lifecycle (%d step(s))", len(merged)))
	}
	sort.Strings(diff.Items)
	return diff
}

func mergeOpenAPIImportScript(existing *dsl.Script, imported *dsl.Script) *dsl.Script {
	return mergeImportScriptByKey(existing, imported, openAPIActionKey)
}

// mergeImportScriptByKey replaces operations matched by keyFn with the imported
// versions, preserves existing actions that keyFn does not key (manual edits),
// and unions data sources / parameters / config so user customizations survive a
// re-import. Shared by the OpenAPI and Postman import-commit handlers.
func mergeImportScriptByKey(existing *dsl.Script, imported *dsl.Script, keyFn func(dsl.Action) string) *dsl.Script {
	if imported == nil {
		return existing
	}
	merged := *imported
	merged.Actions = append([]dsl.Action(nil), imported.Actions...)
	if existing == nil {
		return &merged
	}
	matchedImportedUnkeyed := make([]bool, len(imported.Actions))
	for _, action := range existing.Actions {
		if keyFn(action) != "" {
			continue
		}
		// Importers can emit review placeholders and other source-owned actions
		// that do not have an operation key. A retry after the script write but
		// before protected-source retention observes those actions in both the
		// stored script and the recovered candidate. Treat matching occurrences
		// as the same imported action instead of appending them again. The
		// occurrence-aware match still preserves any additional identical
		// user-authored action.
		matched := false
		for index, importedAction := range imported.Actions {
			if matchedImportedUnkeyed[index] || keyFn(importedAction) != "" ||
				!reflect.DeepEqual(action, importedAction) {
				continue
			}
			matchedImportedUnkeyed[index] = true
			matched = true
			break
		}
		if !matched {
			merged.Actions = append(merged.Actions, action)
		}
	}
	// Preserve user customizations from the target script rather than clobbering
	// them with the fresh import's placeholders: union data sources (keeping
	// existing seed rows and merging in new columns), union parameters by name,
	// and keep the existing config (base URL / think time / default headers).
	blockedDataColumns, unknownExternalSchemas := unavailableExternalCSVColumns(existing.ProjectID, existing.DataSources, imported.DataSources)
	merged.DataSources, _ = mergeImportDataSources(existing.DataSources, imported.DataSources)
	merged.Parameters = mergeImportParameters(existing.Parameters, imported.Parameters)
	blockedParameterColumns := unavailableExternalDataColumnsReferencedByParameters(merged.Parameters, blockedDataColumns)
	if len(blockedDataColumns) > 0 {
		for index := 0; index < len(imported.Actions) && index < len(merged.Actions); index++ {
			gateImportActionForUnavailableDataColumns(&merged.Actions[index], blockedDataColumns, blockedParameterColumns, unknownExternalSchemas)
		}
	}
	merged.Config = mergeImportConfig(existing.Config, imported.Config)
	// Preserve manual init/teardown steps (login, env setup, cleanup) the user
	// added to the target. Without this they are silently dropped on a merge
	// re-import — and since the importer's synthesized OAuth-token init is off by
	// default, imported.Init is usually empty, so the user's setup is wiped to
	// nothing. Imported (re-synthesized) lifecycle steps win on a type/name match.
	importedInit := append([]dsl.Action(nil), imported.Init...)
	importedTeardown := append([]dsl.Action(nil), imported.Teardown...)
	for index := range importedInit {
		gateImportActionForUnavailableDataColumns(&importedInit[index], blockedDataColumns, blockedParameterColumns, unknownExternalSchemas)
	}
	for index := range importedTeardown {
		gateImportActionForUnavailableDataColumns(&importedTeardown[index], blockedDataColumns, blockedParameterColumns, unknownExternalSchemas)
	}
	merged.Init = mergeImportLifecycle(existing.Init, importedInit)
	merged.Teardown = mergeImportLifecycle(existing.Teardown, importedTeardown)
	// Ownership/identity is governed by the replace handler (see
	// preserveReplacedScriptIdentity), but keep sensible fallbacks here too.
	if merged.ProjectID == "" {
		merged.ProjectID = existing.ProjectID
	}
	if merged.FolderID == nil {
		merged.FolderID = existing.FolderID
	}
	if merged.CreatedAt.IsZero() {
		merged.CreatedAt = existing.CreatedAt
	}
	return &merged
}

// mergeImportLifecycle unions imported init/teardown steps with the existing
// (target) ones, keeping the existing lifecycle order and replacing a matching
// importer-managed step in place. Imported steps the user did not already have
// are appended. This keeps synthesized steps fresh without moving them ahead of
// manual setup they may depend on.
func mergeImportLifecycle(existing, imported []dsl.Action) []dsl.Action {
	if len(imported) == 0 {
		return existing
	}
	if len(existing) == 0 {
		return imported
	}
	importedBySig := make(map[string]dsl.Action, len(imported))
	for _, action := range imported {
		importedBySig[lifecycleActionSignature(action)] = action
	}
	used := make(map[string]struct{}, len(imported))
	merged := make([]dsl.Action, 0, len(existing)+len(imported))
	for _, action := range existing {
		sig := lifecycleActionSignature(action)
		if importedAction, ok := importedBySig[sig]; ok {
			merged = append(merged, importedAction)
			used[sig] = struct{}{}
			continue
		}
		merged = append(merged, action)
	}
	for _, action := range imported {
		sig := lifecycleActionSignature(action)
		if _, ok := used[sig]; ok {
			continue
		}
		merged = append(merged, action)
	}
	return merged
}

func lifecycleActionSignature(action dsl.Action) string {
	return strings.Join([]string{action.Type, action.Name, action.Method, action.Target}, "\x00")
}

// preserveReplacedScriptIdentity keeps the target script's identity and
// ownership fields when its content is replaced, so a re-import cannot relocate
// a script to another project or blank its creation time. Used for both full
// replace and merge.
func preserveReplacedScriptIdentity(next *dsl.Script, existing *dsl.Script) {
	if next == nil || existing == nil {
		return
	}
	next.ID = existing.ID
	next.ProjectID = existing.ProjectID
	next.FolderID = existing.FolderID
	if next.CreatedAt.IsZero() {
		next.CreatedAt = existing.CreatedAt
	}
}

// mergeImportParameters keeps every existing parameter and appends imported
// parameters whose name is not already present. Shared across importers.
func mergeImportParameters(existing, imported []dsl.Parameter) []dsl.Parameter {
	if len(existing) == 0 {
		return imported
	}
	out := append([]dsl.Parameter(nil), existing...)
	seen := make(map[string]struct{}, len(existing))
	for _, p := range existing {
		seen[p.Name] = struct{}{}
	}
	for _, p := range imported {
		if _, ok := seen[p.Name]; ok {
			continue
		}
		out = append(out, p)
		seen[p.Name] = struct{}{}
	}
	return out
}

// mergeImportConfig keeps the existing (user-tuned) config and only borrows a
// base URL from the import when the existing one is empty. Shared across importers.
func mergeImportConfig(existing, imported dsl.ScriptConfig) dsl.ScriptConfig {
	if strings.TrimSpace(existing.BaseURL) == "" {
		existing.BaseURL = imported.BaseURL
	}
	return existing
}

// mergeImportDataSources unions data sources by name. A same-named CSV source
// has its columns merged (existing rows and seed values preserved, new columns
// appended); a source with no existing counterpart is added as-is. Returns the
// merged slice and the list of newly added column names. Shared across importers.
func mergeImportDataSources(existing, imported []dsl.DataSource) ([]dsl.DataSource, []string) {
	if len(existing) == 0 {
		return imported, nil
	}
	out := append([]dsl.DataSource(nil), existing...)
	index := make(map[string]int, len(out))
	for i, ds := range out {
		index[ds.Name] = i
	}
	var newColumns []string
	for _, imp := range imported {
		if i, ok := index[imp.Name]; ok {
			merged, added := mergeCSVDataSource(out[i], imp)
			out[i] = merged
			newColumns = append(newColumns, added...)
			continue
		}
		out = append(out, imp)
		index[imp.Name] = len(out) - 1
	}
	return out, newColumns
}

// mergeCSVDataSource unions the columns of two CSV data sources, preserving the
// existing rows and seed values and appending any columns the import introduces
// (seeded from the import's first row). Non-CSV sources keep the existing one.
func mergeCSVDataSource(existing, imported dsl.DataSource) (dsl.DataSource, []string) {
	if !strings.EqualFold(existing.Type, "csv") || !strings.EqualFold(imported.Type, "csv") {
		return existing, nil
	}
	existingHeader, existingRows := parseCSVDataSource(existing)
	if len(existingHeader) == 0 {
		existingHeader = normalizeCSVColumns(existing.Columns)
	}
	// A configured external asset is authoritative. Advertising imported
	// columns without updating that file makes generated bindings fail at
	// runtime, so preserve both the file and its truthful schema unchanged.
	if strings.TrimSpace(existing.InlineData) == "" && hasExternalDataAsset(existing) {
		return existing, nil
	}
	importedHeader, importedRows := parseCSVDataSource(imported)
	if len(importedHeader) == 0 {
		importedHeader = normalizeCSVColumns(imported.Columns)
	}
	have := make(map[string]struct{}, len(existingHeader))
	for _, col := range existingHeader {
		have[col] = struct{}{}
	}
	var newCols []string
	for _, col := range importedHeader {
		if _, ok := have[col]; ok {
			continue
		}
		newCols = append(newCols, col)
		have[col] = struct{}{}
	}
	if len(newCols) == 0 {
		if strings.TrimSpace(existing.InlineData) == "" && len(importedRows) > 0 {
			result := existing
			result.Columns = append([]string(nil), existingHeader...)
			result.InlineData, result.Delimiter = buildCSVDataSourceInline(
				result,
				existingHeader,
				remapCSVRows(importedHeader, importedRows, existingHeader),
			)
			return result, nil
		}
		return existing, nil
	}
	var importedSample map[string]string
	if len(importedRows) > 0 {
		importedSample = csvRowMap(importedHeader, importedRows[0])
	}
	mergedHeader := append(append([]string(nil), existingHeader...), newCols...)
	mergedRows := make([][]string, 0, len(existingRows))
	for _, row := range existingRows {
		merged := make([]string, len(mergedHeader))
		copy(merged, row)
		for i := len(existingHeader); i < len(mergedHeader); i++ {
			merged[i] = importedSample[mergedHeader[i]]
		}
		mergedRows = append(mergedRows, merged)
	}
	if len(mergedRows) == 0 {
		row := make([]string, len(mergedHeader))
		for i, col := range mergedHeader {
			row[i] = importedSample[col]
		}
		mergedRows = [][]string{row}
	}
	result := existing
	result.Columns = mergedHeader
	result.InlineData, result.Delimiter = buildCSVDataSourceInline(result, mergedHeader, mergedRows)
	return result, newCols
}

func hasExternalDataAsset(source dsl.DataSource) bool {
	return strings.TrimSpace(source.Path) != "" || strings.TrimSpace(source.StorageRef) != "" || strings.TrimSpace(source.StagedAsset) != ""
}

func remapCSVRows(sourceHeader []string, rows [][]string, targetHeader []string) [][]string {
	result := make([][]string, 0, len(rows))
	for _, row := range rows {
		values := csvRowMap(sourceHeader, row)
		mapped := make([]string, len(targetHeader))
		for index, column := range targetHeader {
			mapped[index] = values[column]
		}
		result = append(result, mapped)
	}
	return result
}

const maxImportExternalCSVHeaderBytes int64 = 64 << 10

// unavailableExternalCSVColumns compares imported bindings with the schema the
// engine will actually use for a preserved external CSV. Header-backed sources
// must be inspected through the governed file-asset boundary: Columns is only
// authoritative when FirstRowHeader is false. An unreadable header is schema-
// unknown, so every imported candidate column is blocked rather than trusting
// potentially stale metadata.
func unavailableExternalCSVColumns(projectID string, existing, imported []dsl.DataSource) (map[string][]string, map[string]struct{}) {
	existingByName := make(map[string]dsl.DataSource, len(existing))
	for _, source := range existing {
		existingByName[source.Name] = source
	}
	blocked := make(map[string][]string)
	unknown := make(map[string]struct{})
	for _, candidate := range imported {
		current, ok := existingByName[candidate.Name]
		if !ok || !strings.EqualFold(current.Type, "csv") || !strings.EqualFold(candidate.Type, "csv") ||
			strings.TrimSpace(current.InlineData) != "" || !hasExternalDataAsset(current) {
			continue
		}
		importedHeader, _ := parseCSVDataSource(candidate)
		if len(importedHeader) == 0 {
			importedHeader = normalizeCSVColumns(candidate.Columns)
		}
		availableColumns, schemaKnown := externalCSVRuntimeColumns(projectID, current)
		if !schemaKnown {
			blocked[candidate.Name] = dedupSortedStrings(append(blocked[candidate.Name], importedHeader...))
			if len(blocked[candidate.Name]) > 0 {
				unknown[candidate.Name] = struct{}{}
			}
			continue
		}
		available := make(map[string]struct{}, len(availableColumns))
		for _, column := range availableColumns {
			available[column] = struct{}{}
		}
		for _, column := range importedHeader {
			if _, ok := available[column]; !ok {
				blocked[candidate.Name] = append(blocked[candidate.Name], column)
			}
		}
		blocked[candidate.Name] = dedupSortedStrings(blocked[candidate.Name])
		if len(blocked[candidate.Name]) == 0 {
			delete(blocked, candidate.Name)
		}
	}
	if len(blocked) == 0 {
		return nil, nil
	}
	if len(unknown) == 0 {
		unknown = nil
	}
	return blocked, unknown
}

func externalCSVRuntimeColumns(projectID string, source dsl.DataSource) ([]string, bool) {
	if !source.FirstRowHeader && len(source.Columns) > 0 {
		return normalizeCSVColumns(source.Columns), true
	}
	record, err := readGovernedExternalCSVFirstRecord(projectID, source)
	if err != nil || len(record) == 0 {
		return nil, false
	}
	if source.FirstRowHeader {
		return normalizeCSVColumns(record), true
	}
	columns := make([]string, len(record))
	for index := range record {
		columns[index] = fmt.Sprintf("col_%d", index+1)
	}
	return columns, true
}

// readGovernedExternalCSVFirstRecord intentionally refuses arbitrary paths and
// internal staged_asset names. It validates project ownership through
// fileasset, then reads at most one bounded header window. csv.Reader handles
// quoted headers containing newlines; InputOffset distinguishes a complete
// first record from a record truncated at the byte ceiling.
func readGovernedExternalCSVFirstRecord(projectID string, source dsl.DataSource) ([]string, error) {
	path := strings.TrimSpace(source.Path)
	storageRef := strings.TrimSpace(source.StorageRef)
	if strings.TrimSpace(source.StagedAsset) != "" {
		return nil, fmt.Errorf("staged data asset is not available at import commit")
	}
	if (path == "") == (storageRef == "") {
		return nil, fmt.Errorf("exactly one governed path or storage_ref is required")
	}
	var err error
	if path != "" {
		path, err = fileasset.ValidatePathForProject(path, projectID)
	} else {
		path, err = fileasset.ValidateStorageRefForProject(storageRef, projectID)
	}
	if err != nil {
		return nil, err
	}
	readerSource, err := fileasset.OpenPath(path)
	if err != nil {
		return nil, err
	}
	defer readerSource.Close()

	content, err := io.ReadAll(io.LimitReader(readerSource, maxImportExternalCSVHeaderBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read governed CSV header: %w", err)
	}
	if len(content) == 0 {
		return nil, fmt.Errorf("governed CSV is empty")
	}
	csvReader := csv.NewReader(strings.NewReader(string(content)))
	csvReader.Comma = dsl.ResolveDataSourceDelimiter(source, string(content))
	csvReader.FieldsPerRecord = -1
	csvReader.LazyQuotes = true
	csvReader.TrimLeadingSpace = true
	record, err := csvReader.Read()
	if err != nil {
		return nil, fmt.Errorf("read governed CSV header record: %w", err)
	}
	if int64(len(content)) > maxImportExternalCSVHeaderBytes &&
		csvReader.InputOffset() >= int64(len(content)) {
		return nil, fmt.Errorf("governed CSV header exceeds %d byte limit", maxImportExternalCSVHeaderBytes)
	}
	return record, nil
}

func gateImportActionForUnavailableDataColumns(action *dsl.Action, blocked map[string][]string, blockedParameters map[string]map[string][]string, unknownSchemas map[string]struct{}) {
	if action == nil || len(blocked) == 0 {
		return
	}
	referenced := unavailableExternalDataColumnsReferencedByAction(*action, blocked, blockedParameters)
	if len(referenced) == 0 {
		return
	}
	metadata := make(map[string]any, len(action.Metadata)+4)
	for key, value := range action.Metadata {
		metadata[key] = value
	}
	action.Metadata = metadata
	action.Metadata["execution_blocker"] = "import_review_required"
	blockers := importPreservedStringSlice(action.Metadata["execution_blockers"])
	action.Metadata["execution_blockers"] = dedupSortedStrings(append(blockers, "import_review_required"))
	action.Metadata["unavailable_external_data_columns"] = referenced
	priorReview, _ := action.Metadata["import_review"].(map[string]any)
	review := make(map[string]any, len(priorReview)+2)
	for key, value := range priorReview {
		review[key] = value
	}
	reasons := importPreservedStringSlice(review["reason_codes"])
	unknownReferenced := make([]string, 0, len(referenced))
	hasMismatch := false
	for source := range referenced {
		if _, ok := unknownSchemas[source]; ok {
			unknownReferenced = append(unknownReferenced, source)
		} else {
			hasMismatch = true
		}
	}
	if len(unknownReferenced) > 0 {
		sort.Strings(unknownReferenced)
		action.Metadata["unknown_external_data_source_schemas"] = unknownReferenced
		reasons = append(reasons, "external_data_source_schema_unknown")
	}
	if hasMismatch {
		reasons = append(reasons, "external_data_source_schema_mismatch")
	}
	review["reason_codes"] = dedupSortedStrings(reasons)
	review["version"] = 1
	action.Metadata["import_review"] = review
}

func unavailableExternalDataColumnsReferencedByAction(action dsl.Action, blocked map[string][]string, blockedParameters map[string]map[string][]string) map[string][]string {
	encoded, err := json.Marshal(action)
	if err != nil {
		// The DSL action is JSON-backed, so this should be unreachable. If a
		// future metadata value is not serializable, conservatively gate it rather
		// than allowing a known-unavailable binding to execute unchecked.
		return blocked
	}
	result := make(map[string][]string)
	for source, columns := range blocked {
		for _, column := range columns {
			pattern := regexp.MustCompile(`\{\{\s*data\.` + regexp.QuoteMeta(source+"."+column) + `\s*\}\}`)
			if pattern.Match(encoded) {
				result[source] = append(result[source], column)
			}
		}
		if len(result[source]) > 0 {
			result[source] = dedupSortedStrings(result[source])
		}
	}
	for name, dependencies := range blockedParameters {
		pattern := regexp.MustCompile(`\{\{\s*param\.` + regexp.QuoteMeta(name) + `\s*\}\}`)
		if pattern.Match(encoded) {
			mergeUnavailableExternalDataColumns(result, dependencies)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func unavailableExternalDataColumnsReferencedByParameters(parameters []dsl.Parameter, blocked map[string][]string) map[string]map[string][]string {
	if len(parameters) == 0 || len(blocked) == 0 {
		return nil
	}
	dependencies := make(map[string]map[string][]string)
	// Resolve aliases to a fixed point so {{param.alias}} chains remain safe.
	// The number of parameters bounds the useful passes even when aliases cycle.
	for pass := 0; pass <= len(parameters); pass++ {
		changed := false
		for _, parameter := range parameters {
			current := cloneUnavailableExternalDataColumns(dependencies[parameter.Name])
			if columns, ok := blocked[parameter.DataSource]; ok && slicesContain(columns, parameter.Column) {
				current[parameter.DataSource] = append(current[parameter.DataSource], parameter.Column)
			}
			for _, value := range parameter.Values {
				for source, columns := range blocked {
					for _, column := range columns {
						pattern := regexp.MustCompile(`\{\{\s*data\.` + regexp.QuoteMeta(source+"."+column) + `\s*\}\}`)
						if pattern.MatchString(value) {
							current[source] = append(current[source], column)
						}
					}
				}
				for referencedName, referencedDependencies := range dependencies {
					pattern := regexp.MustCompile(`\{\{\s*param\.` + regexp.QuoteMeta(referencedName) + `\s*\}\}`)
					if pattern.MatchString(value) {
						mergeUnavailableExternalDataColumns(current, referencedDependencies)
					}
				}
			}
			for source := range current {
				current[source] = dedupSortedStrings(current[source])
			}
			if len(current) == 0 || reflect.DeepEqual(current, dependencies[parameter.Name]) {
				continue
			}
			dependencies[parameter.Name] = current
			changed = true
		}
		if !changed {
			break
		}
	}
	if len(dependencies) == 0 {
		return nil
	}
	return dependencies
}

func slicesContain(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func cloneUnavailableExternalDataColumns(source map[string][]string) map[string][]string {
	clone := make(map[string][]string, len(source))
	for name, columns := range source {
		clone[name] = append([]string(nil), columns...)
	}
	return clone
}

func mergeUnavailableExternalDataColumns(target, source map[string][]string) {
	for name, columns := range source {
		target[name] = dedupSortedStrings(append(target[name], columns...))
	}
}

// parseCSVDataSource mirrors the engine's delimiter and header semantics. In
// particular, a headerless source keeps its first record as data and uses the
// declared Columns (or the runtime's col_N fallback) as its schema.
func parseCSVDataSource(source dsl.DataSource) ([]string, [][]string) {
	if strings.TrimSpace(source.InlineData) == "" {
		return nil, nil
	}
	reader := csv.NewReader(strings.NewReader(source.InlineData))
	reader.Comma = dsl.ResolveDataSourceDelimiter(source, source.InlineData)
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true
	records, err := reader.ReadAll()
	if err != nil || len(records) == 0 {
		return nil, nil
	}
	if source.FirstRowHeader {
		return normalizeCSVColumns(records[0]), records[1:]
	}
	if len(source.Columns) > 0 {
		return normalizeCSVColumns(source.Columns), records
	}
	columns := make([]string, len(records[0]))
	for index := range records[0] {
		columns[index] = fmt.Sprintf("col_%d", index+1)
	}
	return columns, records
}

func buildCSVDataSourceInline(source dsl.DataSource, header []string, rows [][]string) (string, string) {
	delimiter := dsl.ResolveDataSourceDelimiter(source, source.InlineData)
	var b strings.Builder
	writer := csv.NewWriter(&b)
	writer.Comma = delimiter
	if source.FirstRowHeader {
		_ = writer.Write(header)
	}
	for _, row := range rows {
		_ = writer.Write(row)
	}
	writer.Flush()
	return b.String(), canonicalCSVDelimiter(delimiter)
}

func canonicalCSVDelimiter(delimiter rune) string {
	switch delimiter {
	case ',':
		return "comma"
	case '\t':
		return "tab"
	default:
		return string(delimiter)
	}
}

func normalizeCSVColumns(columns []string) []string {
	if len(columns) == 0 {
		return nil
	}
	normalized := make([]string, len(columns))
	for index, column := range columns {
		normalized[index] = strings.TrimSpace(column)
	}
	return normalized
}

func csvRowMap(header []string, row []string) map[string]string {
	out := make(map[string]string, len(header))
	for i, col := range header {
		if i < len(row) {
			out[col] = row[i]
		}
	}
	return out
}

func dedupSortedStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// actionKeySet collects the non-empty keys produced by keyFn across actions.
func actionKeySet(actions []dsl.Action, keyFn func(dsl.Action) string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, action := range actions {
		if key := keyFn(action); key != "" {
			out[key] = struct{}{}
		}
	}
	return out
}

func openAPIActionKey(action dsl.Action) string {
	if action.Metadata == nil {
		return ""
	}
	raw, ok := action.Metadata["openapi"]
	if !ok {
		return ""
	}
	if typed, ok := raw.(map[string]any); ok {
		if key := strings.TrimSpace(anyString(typed["operation_key"])); key != "" {
			return key
		}
		method := strings.TrimSpace(anyString(typed["method"]))
		path := strings.TrimSpace(anyString(typed["path"]))
		if method != "" && path != "" {
			return strings.ToUpper(method) + " " + path
		}
	}
	if typed, ok := raw.(map[string]string); ok {
		if key := strings.TrimSpace(typed["operation_key"]); key != "" {
			return key
		}
		method := strings.TrimSpace(typed["method"])
		path := strings.TrimSpace(typed["path"])
		if method != "" && path != "" {
			return strings.ToUpper(method) + " " + path
		}
	}
	return ""
}

func anyString(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}
