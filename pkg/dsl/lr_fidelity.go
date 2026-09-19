package dsl

import "encoding/json"

const FeatureLRFidelityV1 = "lr_fidelity_v1"

func RequiresLRFidelity(s *Script) bool {
	if s == nil {
		return false
	}
	for _, p := range s.Parameters {
		if p.AutoAllocate || (p.Selection == "unique" && p.BlockSize > 0) {
			return true
		}
	}
	var walk func([]Action) bool
	walk = func(actions []Action) bool {
		for _, a := range actions {
			if a.Type == "source_verification" {
				return true
			}
			if _, ok := a.Params["resources"]; ok {
				return true
			}
			if a.Control != nil {
				for _, b := range a.Control.ActionBranches() {
					if walk(b.Actions) {
						return true
					}
				}
			}
			if a.Type == "http_sequence" {
				b, _ := json.Marshal(a.Params["requests"])
				var children []Action
				if json.Unmarshal(b, &children) == nil && walk(children) {
					return true
				}
			}
		}
		return false
	}
	return walk(s.Init) || walk(s.Actions) || walk(s.Teardown)
}
