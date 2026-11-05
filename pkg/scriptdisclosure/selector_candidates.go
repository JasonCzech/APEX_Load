package scriptdisclosure

import (
	"encoding/json"

	"github.com/jasonczech/apexload/pkg/selector"
)

// targetSelectorCandidatesKey holds a drag step's ranked drop-target locators.
const targetSelectorCandidatesKey = "source_target_selector_candidates"

// IsSelectorCandidateList reports whether an action metadata entry is a
// capture surface's ranked locator list (source_selector_candidates,
// source_selector_fallbacks, or a drag's source_target_selector_candidates):
// items of exactly kind, value, confidence and reason. Each value locates the
// element the step's own selector names, and the projection keeps that
// selector as recorded. Scrubbed as free text, a generated Lightning element
// name in a long CSS path read as a token and was masked: a phantom kept
// credential on every hover and click of the page, so deleting any step of a
// recording refused the edit. A list of any other shape is projected as usual;
// a credential the script keeps is still masked inside one of these
// (projectSelectorCandidateList).
func IsSelectorCandidateList(key string, value any) bool {
	switch key {
	case selector.MetadataCandidatesKey, selector.MetadataFallbacksKey, targetSelectorCandidatesKey:
	default:
		return false
	}
	items, ok := value.([]any)
	if !ok {
		return false
	}
	for _, item := range items {
		candidate, ok := item.(map[string]any)
		if !ok {
			return false
		}
		for field, entry := range candidate {
			switch field {
			case "kind", "value", "reason":
				if _, ok := entry.(string); !ok {
					return false
				}
			case "confidence":
				switch entry.(type) {
				case float64, json.Number, int, int64:
				default:
					return false
				}
			default:
				return false
			}
		}
	}
	return true
}

// projectSelectorCandidateList keeps each locator as recorded except where it
// quotes a credential the script keeps: a text locator holds up to the first
// 80 characters of an element's visible text, which can show a one-time code
// or a username the script also types into a field it keeps masked.
func (p *projector) projectSelectorCandidateList(value any) any {
	items, _ := value.([]any)
	out := make([]any, len(items))
	for index, item := range items {
		candidate, _ := item.(map[string]any)
		projected := make(map[string]any, len(candidate))
		for field, entry := range candidate {
			if text, ok := entry.(string); ok && (field == "value" || field == "reason") {
				projected[field] = p.replaceKnown(text)
				continue
			}
			projected[field] = entry
		}
		out[index] = projected
	}
	return out
}
