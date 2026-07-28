package api

import (
	"encoding/csv"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
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
	merged.DataSources, _ = mergeImportDataSources(existing.DataSources, imported.DataSources)
	merged.Parameters = mergeImportParameters(existing.Parameters, imported.Parameters)
	merged.Config = mergeImportConfig(existing.Config, imported.Config)
	// Preserve manual init/teardown steps (login, env setup, cleanup) the user
	// added to the target. Without this they are silently dropped on a merge
	// re-import — and since the importer's synthesized OAuth-token init is off by
	// default, imported.Init is usually empty, so the user's setup is wiped to
	// nothing. Imported (re-synthesized) lifecycle steps win on a type/name match.
	merged.Init = mergeImportLifecycle(existing.Init, imported.Init)
	merged.Teardown = mergeImportLifecycle(existing.Teardown, imported.Teardown)
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
	existingHeader, existingRows := parseCSVInline(existing.InlineData)
	if len(existingHeader) == 0 {
		return imported, imported.Columns
	}
	importedHeader, importedRows := parseCSVInline(imported.InlineData)
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
	result.InlineData = buildCSVInline(mergedHeader, mergedRows)
	return result, newCols
}

func parseCSVInline(data string) ([]string, [][]string) {
	if strings.TrimSpace(data) == "" {
		return nil, nil
	}
	reader := csv.NewReader(strings.NewReader(data))
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil || len(records) == 0 {
		return nil, nil
	}
	return records[0], records[1:]
}

func buildCSVInline(header []string, rows [][]string) string {
	var b strings.Builder
	writer := csv.NewWriter(&b)
	_ = writer.Write(header)
	for _, row := range rows {
		_ = writer.Write(row)
	}
	writer.Flush()
	return b.String()
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
