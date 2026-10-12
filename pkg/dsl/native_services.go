package dsl

import (
	"fmt"
	"slices"
	"strings"
)

// NativeServicesConfig declares authority required by opaque native code. It
// grants no runtime authority on its own: the admitted binding remains decisive.
type NativeServicesConfig struct {
	LiveTables []NativeLiveTableRequirement `json:"live_tables,omitempty"`
}
type NativeLiveTableRequirement struct {
	Alias      string   `json:"alias"`
	Operations []string `json:"operations"`
}

func validNativeOperation(op string) bool {
	switch op {
	case "read", "next", "push", "pop", "lease", "renew", "ack", "release", "update", "increment", "clear", "rotate", "create_column", "read_column":
		return true
	}
	return false
}

func ScriptNativeServiceRequirements(script *Script) ([]NativeLiveTableRequirement, error) {
	if script == nil || script.Config.NativeServices == nil {
		return nil, nil
	}
	declared := script.Config.NativeServices.LiveTables
	if len(declared) > 16 {
		return nil, fmt.Errorf("config.native_services supports at most 16 live table aliases")
	}
	seen := map[string]bool{}
	result := make([]NativeLiveTableRequirement, 0, len(declared))
	for _, requirement := range declared {
		key := strings.ToLower(requirement.Alias)
		if len(requirement.Alias) > 64 || !liveAliasPattern.MatchString(requirement.Alias) || seen[key] {
			return nil, fmt.Errorf("native service aliases must be distinct static identifiers")
		}
		seen[key] = true
		if len(requirement.Operations) == 0 || len(requirement.Operations) > 14 {
			return nil, fmt.Errorf("native service operations must contain 1..14 distinct operations")
		}
		ops := map[string]bool{}
		for _, op := range requirement.Operations {
			if !validNativeOperation(op) || ops[op] {
				return nil, fmt.Errorf("native service operations must be distinct supported operations")
			}
			ops[op] = true
		}
		requirement.Operations = slices.Clone(requirement.Operations)
		slices.Sort(requirement.Operations)
		result = append(result, requirement)
	}
	slices.SortFunc(result, func(a, b NativeLiveTableRequirement) int { return strings.Compare(a.Alias, b.Alias) })
	return result, nil
}

// NativeServicePermissionClosure adds only trusted lifecycle controls. Guest
// authorization continues to check the unexpanded declaration separately.
func NativeServicePermissionClosure(operations []string) []string {
	result := slices.Clone(operations)
	if slices.Contains(operations, "lease") {
		result = append(result, "renew", "release")
	}
	slices.Sort(result)
	return slices.Compact(result)
}

func RequiredNativeRuntimeFeatures(script *Script) []string {
	if script == nil {
		return nil
	}
	var result []string
	if script.Config.NativeServices != nil && len(script.Config.NativeServices.LiveTables) > 0 {
		result = append(result, "native_data_services_v1")
		if strings.EqualFold(strings.TrimSpace(script.Protocol), "java_vuser") {
			result = append(result, "native_data_java_v1")
		}
	}
	budget := 30000
	var walk func([]Action, int)
	walk = func(actions []Action, depth int) {
		if depth > 128 {
			return
		}
		for _, action := range actions {
			budget--
			if budget < 0 {
				return
			}
			if CanonicalActionType(action.Type) == "native_javascript" && !slices.Contains(result, "native_javascript_v1") {
				result = append(result, "native_javascript_v1")
			}
			if action.Control != nil {
				for _, branch := range action.Control.ActionBranches() {
					walk(branch.Actions, depth+1)
				}
			}
		}
	}
	walk(script.Init, 0)
	walk(script.Actions, 0)
	walk(script.Teardown, 0)
	return result
}
