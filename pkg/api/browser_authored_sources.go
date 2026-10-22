package api

import "github.com/jasonczech/apexload/pkg/protocol"

func browserAuthoredDefinitionsForGroups(groups []TestGroup) map[string]protocol.BrowserAuthoredDefinition {
	var out map[string]protocol.BrowserAuthoredDefinition
	for _, group := range groups {
		if !group.browserAuthoredDefinition.Valid() {
			continue
		}
		if out == nil {
			out = make(map[string]protocol.BrowserAuthoredDefinition)
		}
		out[group.Name] = group.browserAuthoredDefinition
	}
	return out
}
func cloneBrowserAuthoredDefinitions(values map[string]protocol.BrowserAuthoredDefinition) map[string]protocol.BrowserAuthoredDefinition {
	if values == nil {
		return nil
	}
	out := make(map[string]protocol.BrowserAuthoredDefinition, len(values))
	for name, value := range values {
		out[name] = value
	}
	return out
}
