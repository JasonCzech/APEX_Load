// Package uniquify provides a shared post-conversion pass that guarantees DSL
// action names are unique under case-folding, for every script importer.
//
// The dashboard authoring-assist flags duplicate action names by lowercasing
// each name (scriptAuthoringAssist.normalizeTokenName = value.trim().toLowerCase()).
// Importers historically deduped names case-sensitively and only on a subset of
// action-creation paths, so case-variant or wrapper (http_sequence) names slipped
// through and surfaced as false "Action name ... is duplicated" warnings.
//
// DeduplicateActionNames mutates a converted script so its action names are
// unique under strings.ToLower, matching the dashboard contract. It is the single
// place that knows the dedup rule; converters and the API import boundary both
// call it.
package uniquify

import (
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// Rename records one action-name change applied by DeduplicateActionNames so
// callers can reconcile side data (e.g. enterprise-archive source-review
// mappings whose TargetName mirrors the action name).
type Rename struct {
	OldName string
	NewName string
}

// DeduplicateActionNames makes every action name in script unique under
// case-folding and returns the renames it applied. It mutates script in place.
//
// Scoping:
//   - Top-level names are uniquified across Init, then Actions, then Teardown
//     using one shared registry — the contract the dashboard validates and the
//     engine indexes. The deterministic phase order keeps output stable.
//   - Nested action containers (control_if/control_loop Steps and Else, and
//     http_sequence children carried in Params["requests"]) are uniquified
//     within their own container scope, since nested actions are addressed
//     structurally rather than by a flat global index.
//
// The function is idempotent: a second run finds no case-folded collisions and
// returns nil. Empty names are left untouched (a separate validation flags the
// "name required" error).
func DeduplicateActionNames(script *dsl.Script) []Rename {
	if script == nil {
		return nil
	}
	var renames []Rename
	topLevel := map[string]int{}
	dedupeActionList(script.Init, topLevel, &renames)
	dedupeActionList(script.Actions, topLevel, &renames)
	dedupeActionList(script.Teardown, topLevel, &renames)
	return renames
}

// DeduplicateScriptNames makes the human-visible names in a converted script
// batch unique under the same trim-and-case-fold identity used by import save
// handlers. The first occurrence keeps its source name; later occurrences get
// deterministic _N suffixes. Nil scripts and empty names are left for the
// normal validator.
//
// A converter that emits more than one script must run this before it creates
// credential targets or scenario references, so those side records bind to the
// final, unambiguous script names.
func DeduplicateScriptNames(scripts []*dsl.Script) []Rename {
	seen := make(map[string]int, len(scripts))
	var renames []Rename
	for _, script := range scripts {
		if script == nil || strings.TrimSpace(script.Name) == "" {
			continue
		}
		original := script.Name
		unique := uniqueScriptName(original, seen)
		if unique == original {
			continue
		}
		script.Name = unique
		renames = append(renames, Rename{OldName: original, NewName: unique})
	}
	return renames
}

func uniqueScriptName(name string, seen map[string]int) string {
	base := strings.TrimSpace(name)
	key := strings.ToLower(base)
	count := seen[key]
	if count == 0 {
		seen[key] = 1
		return name
	}
	for {
		count++
		candidate := fmt.Sprintf("%s_%d", base, count)
		candidateKey := strings.ToLower(strings.TrimSpace(candidate))
		if seen[candidateKey] == 0 {
			seen[key] = count
			seen[candidateKey] = 1
			return candidate
		}
	}
}

// dedupeActionList uniquifies the names of actions against the shared seen
// registry, then recurses into each action's nested scopes with a fresh
// per-container registry.
func dedupeActionList(actions []dsl.Action, seen map[string]int, renames *[]Rename) {
	for i := range actions {
		original := actions[i].Name
		applyRename(&actions[i].Name, seen, renames)
		if actions[i].Name != original {
			pinTransactionMarkerName(&actions[i], original)
		}

		if actions[i].Control != nil {
			// Every branch in one control container shares a scope.
			child := map[string]int{}
			for _, branch := range actions[i].Control.ActionBranches() {
				dedupeActionList(branch.Actions, child, renames)
			}
		}
		dedupeSequenceRequests(actions[i], renames)
	}
}

// pinTransactionMarkerName keeps a renamed transaction marker paired. A start
// or end marker without arguments names its transaction by its step name (the
// engine's hand-authored shorthand), so renaming the step would change which
// transaction it opens or closes; the original name moves into the call's
// argument instead.
func pinTransactionMarkerName(action *dsl.Action, name string) {
	if dsl.CanonicalActionType(action.Type) != "source_transaction" {
		return
	}
	if args, ok := action.Params["args"]; ok && !emptyArgs(args) {
		return
	}
	function, _ := action.Params["function"].(string)
	if strings.TrimSpace(function) == "" {
		function, _ = action.Metadata["source_function"].(string)
	}
	switch strings.ToLower(strings.TrimSpace(function)) {
	case "lr_start_transaction", "lr_end_transaction", "lr_end_sub_transaction", "lr_end_sub_trans":
		if action.Params == nil {
			action.Params = map[string]any{}
		}
		action.Params["args"] = []any{strings.TrimSpace(name)}
	}
}

func emptyArgs(args any) bool {
	switch list := args.(type) {
	case nil:
		return true
	case []any:
		return len(list) == 0
	case []string:
		return len(list) == 0
	default:
		return false
	}
}

// dedupeSequenceRequests uniquifies the "name" fields of the child requests an
// http_sequence action carries in Params["requests"]. grouping.go builds these
// as []map[string]any in-process; a JSON round-trip yields []any of
// map[string]any, so both shapes are handled.
func dedupeSequenceRequests(action dsl.Action, renames *[]Rename) {
	if action.Params == nil {
		return
	}
	raw, ok := action.Params["requests"]
	if !ok {
		return
	}
	seen := map[string]int{}
	switch list := raw.(type) {
	case []map[string]any:
		for _, child := range list {
			renameChildName(child, seen, renames)
		}
	case []any:
		for _, item := range list {
			if child, ok := item.(map[string]any); ok {
				renameChildName(child, seen, renames)
			}
		}
	}
}

func renameChildName(child map[string]any, seen map[string]int, renames *[]Rename) {
	name, ok := child["name"].(string)
	if !ok {
		return
	}
	newName := name
	applyRename(&newName, seen, renames)
	if newName != name {
		child["name"] = newName
	}
}

// applyRename uniquifies *name against seen, recording a Rename when it changes.
func applyRename(name *string, seen map[string]int, renames *[]Rename) {
	original := *name
	if strings.TrimSpace(original) == "" {
		return
	}
	unique := uniqueName(original, seen)
	if unique != original {
		*name = unique
		*renames = append(*renames, Rename{OldName: original, NewName: unique})
	}
}

// uniqueName returns name unchanged the first time its folded form is seen;
// otherwise it appends an incrementing _N suffix, re-checking the folded
// candidate so a generated suffix never collides with an existing literal
// "..._N" (the cascade hole the old per-branch dedup left open).
func uniqueName(name string, seen map[string]int) string {
	base := strings.TrimSpace(name)
	key := strings.ToLower(base)
	count := seen[key]
	if count == 0 {
		seen[key] = 1
		return name
	}
	for {
		count++
		candidate := fmt.Sprintf("%s_%d", base, count)
		candidateKey := strings.ToLower(strings.TrimSpace(candidate))
		if seen[candidateKey] == 0 {
			seen[key] = count
			seen[candidateKey] = 1
			return candidate
		}
	}
}
