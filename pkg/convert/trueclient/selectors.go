package trueclient

// selectors.go — split from converter.go (same package, behavior-preserving).

import (
	"strings"

	"github.com/jasonczech/apexload/pkg/selector"
)

// collectObjectDescriptors parses every object-repository file in the archive into
// ranked object descriptors so the converter can apply repository-quality selectors
// instead of relying on the inline locator captured in each step.
func collectObjectDescriptors(files map[string]string) []ObjectDescriptor {
	descriptors := make([]ObjectDescriptor, 0)
	for name, content := range files {
		if classifyArchiveFile(name) == "object_repository" {
			descriptors = append(descriptors, parseObjectRepositoryDescriptors(name, content)...)
		}
	}
	return uniqueObjectDescriptors(descriptors)
}

// resolveSelector returns the best selector for a step. When an object-repository
// descriptor matches the step, the highest-confidence css/xpath candidate (inline +
// descriptor, ranked by confidence) is used; otherwise the inline selector is returned
// unchanged so archives without object repositories behave exactly as before.
func resolveSelector(step truXMLStep, descriptors []ObjectDescriptor, inlineSelector string) string {
	candidates, matched := rankedSelectorCandidates(step, descriptors)
	if matched {
		if best := playwrightSelectorFromCandidates(candidates); best != "" {
			return best
		}
	}
	return inlineSelector
}

// rankedSelectorCandidates combines the inline locators captured on the step with the
// candidates derived from a matching object-repository descriptor, returning them ranked
// by confidence. The boolean reports whether an object descriptor matched the step.
func rankedSelectorCandidates(step truXMLStep, descriptors []ObjectDescriptor) ([]SelectorCandidate, bool) {
	values, _ := parseArgumentValues(step.Arguments)
	candidates := selectorCandidatesFromValues(values)
	matched := false
	if descriptor := matchObjectDescriptor(descriptors, stepObjectDescriptorIDFromValues(step, values), step); descriptor != nil {
		matched = true
		candidates = append(candidates, descriptor.SelectorCandidates...)
	}
	return uniqueSelectorCandidates(candidates), matched
}

// playwrightSelectorFromCandidates returns the highest-confidence candidate that is
// directly usable as a Playwright selector string (css or xpath). Role/text candidates
// are preserved in metadata for review but are not promoted automatically because they
// are not unambiguous selector strings.
func playwrightSelectorFromCandidates(candidates []SelectorCandidate) string {
	return selector.PlaywrightSelector(candidates)
}

// normalizeXPathSelector prefixes a bare XPath expression with the explicit Playwright
// "xpath=" engine prefix so it is never mistaken for a CSS selector.
func normalizeXPathSelector(value string) string {
	return selector.NormalizeXPath(value)
}

func normalizeScrollDirection(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "up", "north":
		return "up"
	case "down", "south":
		return "down"
	case "left", "west":
		return "left"
	case "right", "east":
		return "right"
	default:
		return ""
	}
}
