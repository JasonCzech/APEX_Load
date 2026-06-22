package api

import (
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// postmanActionKey identifies a Postman-derived action by its folder/item path
// so a re-import can match and replace the same request while preserving manual
// additions. Returns "" for actions the Postman importer did not produce (e.g.
// steps the user added by hand), which the merge then preserves.
func postmanActionKey(action dsl.Action) string {
	if action.Metadata == nil {
		return ""
	}
	item := strings.TrimSpace(anyString(action.Metadata["postman_item"]))
	if item == "" {
		return ""
	}
	if folder := strings.TrimSpace(anyString(action.Metadata["postman_folder"])); folder != "" {
		return folder + " / " + item
	}
	return item
}

// diffPostmanImport summarizes a Postman commit/replace, matching requests by
// their folder/item path.
func diffPostmanImport(existing *dsl.Script, imported *dsl.Script, replaceMode string) openAPIImportDiff {
	return diffImportByKey(existing, imported, replaceMode, postmanActionKey)
}

// mergePostmanImportScript replaces re-imported Postman requests in place while
// preserving manually added actions and user data customizations.
func mergePostmanImportScript(existing *dsl.Script, imported *dsl.Script) *dsl.Script {
	return mergeImportScriptByKey(existing, imported, postmanActionKey)
}
