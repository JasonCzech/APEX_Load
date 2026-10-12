package dsl

import (
	"fmt"
	"regexp"
)

const FeatureDistributedRendezvousV1 = "distributed_rendezvous_v1"

type DistributedRendezvousSpec struct {
	Key       string `json:"key"`
	Count     int    `json:"count,omitempty"`
	Percent   int    `json:"percent,omitempty"`
	TimeoutMS int    `json:"timeout_ms"`
	OnTimeout string `json:"on_timeout"`
}

var rendezvousKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,127}$`)

func ParseDistributedRendezvous(action Action) (*DistributedRendezvousSpec, error) {
	if CanonicalActionType(action.Type) != "rendezvous" {
		return nil, nil
	}
	raw, present := action.Params["scope"]
	if !present || raw == "" || raw == "local" {
		return nil, nil
	}
	if raw != "distributed_group" {
		return nil, fmt.Errorf("rendezvous scope must be local or distributed_group")
	}
	out := &DistributedRendezvousSpec{}
	out.Key, _ = action.Params["key"].(string)
	if !rendezvousKeyPattern.MatchString(out.Key) {
		return nil, fmt.Errorf("distributed rendezvous requires a static key of 1..128 letters, digits, underscores, dots or hyphens")
	}
	var ok bool
	out.TimeoutMS, ok = actionParamIntValue(action.Params, "timeout_ms")
	if !ok || out.TimeoutMS < 1 || out.TimeoutMS > 120000 {
		return nil, fmt.Errorf("distributed rendezvous timeout_ms must be 1..120000")
	}
	out.OnTimeout, _ = action.Params["on_timeout"].(string)
	if out.OnTimeout != "fail" && out.OnTimeout != "continue" {
		return nil, fmt.Errorf("distributed rendezvous requires on_timeout fail or continue")
	}
	if _, exists := action.Params["count"]; exists {
		out.Count, ok = actionParamIntValue(action.Params, "count")
		if !ok || out.Count < 1 || out.Count > 100000 {
			return nil, fmt.Errorf("distributed rendezvous count must be 1..100000")
		}
	}
	if _, exists := action.Params["percent"]; exists {
		out.Percent, ok = actionParamIntValue(action.Params, "percent")
		if !ok || out.Percent < 1 || out.Percent > 100 || out.Count != 0 {
			return nil, fmt.Errorf("distributed rendezvous percent must be 1..100 and cannot accompany count")
		}
	}
	for key := range action.Params {
		switch key {
		case "scope", "key", "count", "percent", "timeout_ms", "on_timeout":
		default:
			return nil, fmt.Errorf("distributed rendezvous does not accept %s", key)
		}
	}
	return out, nil
}

// A static unique key is the barrier's stable action identity. Reject parallel
// lanes and lifecycle phases whose participants are not the admitted group.
func ScriptDistributedRendezvous(script *Script) ([]DistributedRendezvousSpec, error) {
	if script == nil {
		return nil, nil
	}
	if issues := validateScriptStructureIssues(script); len(issues) > 0 {
		return nil, fmt.Errorf("rendezvous inspection: %s", issues[0])
	}
	var out []DistributedRendezvousSpec
	seen := map[string]bool{}
	var walk func([]Action, bool) error
	walk = func(actions []Action, allowed bool) error {
		for _, action := range actions {
			spec, err := ParseDistributedRendezvous(action)
			if err != nil {
				return err
			}
			if spec != nil {
				if !allowed {
					return fmt.Errorf("distributed rendezvous is supported only in sequential iteration actions")
				}
				if seen[spec.Key] {
					return fmt.Errorf("distributed rendezvous key %q appears more than once", spec.Key)
				}
				if len(out) >= 32 {
					return fmt.Errorf("script exceeds 32 distributed rendezvous actions")
				}
				seen[spec.Key] = true
				out = append(out, *spec)
			}
			if action.Control != nil {
				for _, branch := range action.Control.ActionBranches() {
					if err := walk(branch.Actions, allowed && CanonicalActionType(action.Type) != "control_parallel"); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	if err := walk(script.Init, false); err != nil {
		return nil, err
	}
	if err := walk(script.Actions, true); err != nil {
		return nil, err
	}
	if err := walk(script.Teardown, false); err != nil {
		return nil, err
	}
	return out, nil
}
