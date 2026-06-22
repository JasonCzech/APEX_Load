package api

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func diffOpenAPIImport(existing *dsl.Script, imported *dsl.Script, replaceMode string) openAPIImportDiff {
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
	existingKeys := openAPIActionKeySet(existing.Actions)
	importedKeys := openAPIActionKeySet(imported.Actions)
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
		if openAPIActionKey(action) == "" {
			diff.Preserved++
		}
	}
	sort.Strings(diff.Items)
	return diff
}

func mergeOpenAPIImportScript(existing *dsl.Script, imported *dsl.Script) *dsl.Script {
	if imported == nil {
		return existing
	}
	merged := *imported
	merged.Actions = append([]dsl.Action(nil), imported.Actions...)
	if existing == nil {
		return &merged
	}
	for _, action := range existing.Actions {
		if openAPIActionKey(action) == "" {
			merged.Actions = append(merged.Actions, action)
		}
	}
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

func openAPIActionKeySet(actions []dsl.Action) map[string]struct{} {
	out := map[string]struct{}{}
	for _, action := range actions {
		if key := openAPIActionKey(action); key != "" {
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
