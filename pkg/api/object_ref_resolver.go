package api

// Object-repository binding resolution. A browser action may carry an
// object_ref (a logical object name) instead of an inline selector. Because
// distributed load runs execute on generators that have no store — and the
// in-process replay engine likewise consumes a prepared script — the binding is
// resolved here, on the controller, at script-preparation time: each object_ref
// is materialized into a concrete selector plus ranked fallback candidates in
// the action metadata that replay self-heal already understands.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/selector"
	"github.com/jasonczech/apexload/pkg/storage"
)

// objectRepositoryReader is the slice of storage.Store the resolver needs: it
// lists a project's repositories so a logical object name can be materialized
// into a concrete selector. storage.Store satisfies it.
type objectRepositoryReader interface {
	ListObjectRepositories(projectID string, limit int, offset int) ([]*storage.ObjectRepository, error)
}

// objectRefRepositoryMetadataKey records, on a resolved action, which repository
// supplied the binding so replay self-heal can write a healed locator back to
// the originating object.
const objectRefRepositoryMetadataKey = "object_ref_repository"
const objectRefLogicalNameMetadataKey = "object_ref_logical_name"

// resolvedRepositoryObject pairs a repository object with the repository it came
// from, so write-back can target the right repository.
type resolvedRepositoryObject struct {
	repoID string
	object storage.RepositoryObject
}

// resolveObjectRefs materializes every action.ObjectRef in the script into a
// concrete selector plus ranked fallback candidates in action metadata. It
// never mutates the input: when at least one ref is present it returns a deep
// clone; when none are present it returns the input unchanged (no store hit).
//
// Resolution policy:
//   - ref resolves            → embed the object's selector + candidates.
//   - ref unresolved, inline   → keep the inline selector/target, emit a warning.
//     selector/target present
//   - ref unresolved, no       → emit an issue that blocks the run.
//     inline fallback
func resolveObjectRefs(reader objectRepositoryReader, script *dsl.Script) (*dsl.Script, []string, []string) {
	if script == nil || !scriptHasObjectRefs(script) {
		return script, nil, nil
	}
	resolved := cloneScriptDefinition(script)
	byName, byRepo := loadObjectRepositoryIndex(reader, resolved.ProjectID)

	var warnings, issues []string
	resolveActionObjectRefs(resolved.Init, "init", byName, byRepo, &warnings, &issues)
	resolveActionObjectRefs(resolved.Actions, "actions", byName, byRepo, &warnings, &issues)
	resolveActionObjectRefs(resolved.Teardown, "teardown", byName, byRepo, &warnings, &issues)
	return resolved, warnings, issues
}

func scriptHasObjectRefs(script *dsl.Script) bool {
	return actionsHaveObjectRefs(script.Init) || actionsHaveObjectRefs(script.Actions) || actionsHaveObjectRefs(script.Teardown)
}

func actionsHaveObjectRefs(actions []dsl.Action) bool {
	for i := range actions {
		if strings.TrimSpace(actions[i].ObjectRef) != "" {
			return true
		}
		if actions[i].Control != nil {
			if actionsHaveObjectRefs(actions[i].Control.Steps) || actionsHaveObjectRefs(actions[i].Control.Else) {
				return true
			}
		}
	}
	return false
}

// loadObjectRepositoryIndex builds two lookup maps for the project's
// repositories: a global logical-name index (highest BestConfidence wins ties,
// deterministically resolved by repository creation order) and a per-repository
// index for qualified "repoID:name" references.
func loadObjectRepositoryIndex(reader objectRepositoryReader, projectID string) (map[string]resolvedRepositoryObject, map[string]map[string]storage.RepositoryObject) {
	byName := map[string]resolvedRepositoryObject{}
	byRepo := map[string]map[string]storage.RepositoryObject{}
	if reader == nil {
		return byName, byRepo
	}
	repos, err := reader.ListObjectRepositories(normalizeProjectID(projectID), 0, 0)
	if err != nil {
		return byName, byRepo
	}
	sort.SliceStable(repos, func(i, j int) bool {
		if repos[i] == nil || repos[j] == nil {
			return repos[i] != nil
		}
		if !repos[i].CreatedAt.Equal(repos[j].CreatedAt) {
			return repos[i].CreatedAt.Before(repos[j].CreatedAt)
		}
		return repos[i].ID < repos[j].ID
	})
	for _, repo := range repos {
		if repo == nil {
			continue
		}
		rmap := byRepo[repo.ID]
		if rmap == nil {
			rmap = map[string]storage.RepositoryObject{}
			byRepo[repo.ID] = rmap
		}
		for _, obj := range repo.Objects {
			key := strings.ToLower(strings.TrimSpace(obj.LogicalName))
			if key == "" {
				continue
			}
			if _, exists := rmap[key]; !exists {
				rmap[key] = obj
			}
			if existing, ok := byName[key]; !ok || obj.BestConfidence > existing.object.BestConfidence {
				byName[key] = resolvedRepositoryObject{repoID: repo.ID, object: obj}
			}
		}
	}
	return byName, byRepo
}

func resolveActionObjectRefs(actions []dsl.Action, section string, byName map[string]resolvedRepositoryObject, byRepo map[string]map[string]storage.RepositoryObject, warnings, issues *[]string) {
	for i := range actions {
		resolveActionObjectRef(&actions[i], fmt.Sprintf("%s[%d]", section, i), byName, byRepo, warnings, issues)
		if actions[i].Control != nil {
			resolveActionObjectRefs(actions[i].Control.Steps, fmt.Sprintf("%s[%d].control.steps", section, i), byName, byRepo, warnings, issues)
			resolveActionObjectRefs(actions[i].Control.Else, fmt.Sprintf("%s[%d].control.else", section, i), byName, byRepo, warnings, issues)
		}
	}
}

func resolveActionObjectRef(action *dsl.Action, label string, byName map[string]resolvedRepositoryObject, byRepo map[string]map[string]storage.RepositoryObject, warnings, issues *[]string) {
	ref := strings.TrimSpace(action.ObjectRef)
	if ref == "" {
		return
	}
	hasInline := strings.TrimSpace(action.Selector) != "" || strings.TrimSpace(action.Target) != ""
	degrade := func(reason string) {
		if hasInline {
			*warnings = append(*warnings, fmt.Sprintf("%s %q: object_ref %q %s; using inline selector/target", label, action.Name, ref, reason))
		} else {
			*issues = append(*issues, fmt.Sprintf("%s %q: object_ref %q %s and the action has no selector/target fallback", label, action.Name, ref, reason))
		}
	}

	repoID, logicalName := splitObjectRef(ref)
	obj, foundRepoID, ok := lookupRepositoryObject(repoID, logicalName, byName, byRepo)
	if !ok {
		degrade("not found in any project object repository")
		return
	}
	candidates := candidatesForRepositoryObject(obj)
	primary := primarySelectorFromCandidates(candidates)
	if primary == "" {
		degrade("resolves to an object with no usable locator")
		return
	}
	action.Selector = primary
	applyResolvedObjectBinding(action, candidates, primary, foundRepoID, logicalName)
}

// splitObjectRef parses a qualified "repoID:name" reference. Only a prefix that
// looks like a repository id is treated as a qualifier, so a logical name that
// happens to contain a colon is still resolved as a bare name.
func splitObjectRef(ref string) (repoID string, logicalName string) {
	if idx := strings.Index(ref, ":"); idx > 0 {
		prefix := strings.TrimSpace(ref[:idx])
		if strings.HasPrefix(prefix, "object-repo-") {
			return prefix, strings.TrimSpace(ref[idx+1:])
		}
	}
	return "", ref
}

func lookupRepositoryObject(repoID, logicalName string, byName map[string]resolvedRepositoryObject, byRepo map[string]map[string]storage.RepositoryObject) (storage.RepositoryObject, string, bool) {
	key := strings.ToLower(strings.TrimSpace(logicalName))
	if key == "" {
		return storage.RepositoryObject{}, "", false
	}
	if repoID != "" {
		if rmap, ok := byRepo[repoID]; ok {
			if obj, ok := rmap[key]; ok {
				return obj, repoID, true
			}
		}
		return storage.RepositoryObject{}, "", false
	}
	if r, ok := byName[key]; ok {
		return r.object, r.repoID, true
	}
	return storage.RepositoryObject{}, "", false
}

// candidatesForRepositoryObject turns a repository object into ranked selector
// candidates. Stored ranked candidates are preferred; otherwise candidates are
// synthesized from the descriptor fields using the shared confidence ladder.
func candidatesForRepositoryObject(obj storage.RepositoryObject) []selector.Candidate {
	if len(obj.Candidates) > 0 {
		out := make([]selector.Candidate, 0, len(obj.Candidates))
		for _, c := range obj.Candidates {
			out = append(out, selector.Candidate{Kind: c.Kind, Value: c.Value, Confidence: c.Confidence, Reason: c.Reason})
		}
		return selector.Unique(out)
	}
	var out []selector.Candidate
	if v := strings.TrimSpace(obj.CSS); v != "" {
		out = append(out, selector.Candidate{Kind: "css", Value: v, Confidence: selector.ConfidenceCapturedCSS, Reason: "object repository css"})
	}
	if v := strings.TrimSpace(obj.XPath); v != "" {
		out = append(out, selector.Candidate{Kind: "xpath", Value: v, Confidence: selector.ConfidenceXPath, Reason: "object repository xpath"})
	}
	if role := strings.TrimSpace(obj.Role); role != "" {
		if name := strings.TrimSpace(obj.Text); name != "" {
			if rs := selector.RoleSelector(role, name); rs != "" {
				out = append(out, selector.Candidate{Kind: "role", Value: rs, Confidence: selector.ConfidenceRoleName, Reason: "object repository role"})
			}
		}
	}
	if v := strings.TrimSpace(obj.Text); v != "" {
		out = append(out, selector.Candidate{Kind: "text", Value: v, Confidence: selector.ConfidenceText, Reason: "object repository text"})
	}
	return selector.Unique(out)
}

// primarySelectorFromCandidates picks the action selector. A replay self-heal
// that was auto-promoted to primary wins outright — including a role/text locator
// that PlaywrightSelector would otherwise skip — so the autoPromote contract
// ("used on the next run without manual intervention") holds for every kind.
// Absent a promoted heal it keeps the authoring/replay convention: the strongest
// css/xpath candidate, falling back to the first otherwise-usable candidate.
func primarySelectorFromCandidates(candidates []selector.Candidate) string {
	bestPromoted := ""
	bestPromotedConf := -1
	for _, c := range candidates {
		if strings.TrimSpace(c.Reason) != reasonPromotedByHeal {
			continue
		}
		if s, ok := selector.Usable(c); ok && c.Confidence > bestPromotedConf {
			bestPromotedConf = c.Confidence
			bestPromoted = s
		}
	}
	if bestPromoted != "" {
		return bestPromoted
	}
	if s := selector.PlaywrightSelector(candidates); s != "" {
		return s
	}
	for _, c := range candidates {
		if s, ok := selector.Usable(c); ok {
			return s
		}
	}
	return ""
}

// applyResolvedObjectBinding writes the resolved candidates onto the action
// metadata in the exact shape capture surfaces emit (so self-heal consumes it
// unchanged): the full ranked list under MetadataCandidatesKey and every
// non-primary candidate under MetadataFallbacksKey, plus the source repository.
func applyResolvedObjectBinding(action *dsl.Action, candidates []selector.Candidate, primary, repoID, logicalName string) {
	if action.Metadata == nil {
		action.Metadata = map[string]any{}
	}
	action.Metadata[selector.MetadataCandidatesKey] = candidates
	fallbacks := make([]selector.Candidate, 0, len(candidates))
	for _, c := range candidates {
		if u, ok := selector.Usable(c); ok && u == primary {
			continue
		}
		fallbacks = append(fallbacks, c)
	}
	if len(fallbacks) > 0 {
		action.Metadata[selector.MetadataFallbacksKey] = fallbacks
	} else {
		delete(action.Metadata, selector.MetadataFallbacksKey)
	}
	if repoID != "" {
		action.Metadata[objectRefRepositoryMetadataKey] = repoID
	}
	if strings.TrimSpace(logicalName) != "" {
		action.Metadata[objectRefLogicalNameMetadataKey] = strings.TrimSpace(logicalName)
	}
}
