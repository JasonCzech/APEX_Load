package api

import "github.com/jasonczech/apexload/pkg/storage"

// cloneScenarioDataPoolBindings preserves nil-versus-explicit-empty request
// semantics while preventing persisted or response snapshots from aliasing the
// controller's mutable state.
func cloneScenarioDataPoolBindings(bindings []storage.ScenarioDataPoolBinding) []storage.ScenarioDataPoolBinding {
	if bindings == nil {
		return nil
	}
	cloned := make([]storage.ScenarioDataPoolBinding, len(bindings))
	copy(cloned, bindings)
	for index := range cloned {
		if bindings[index].LiveOptions != nil {
			options := *bindings[index].LiveOptions
			if options.Columns != nil {
				options.Columns = append([]string{}, options.Columns...)
			}
			cloned[index].LiveOptions = &options
		}
	}
	return cloned
}
