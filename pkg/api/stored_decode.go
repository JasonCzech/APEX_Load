package api

import "encoding/json"

func decodeStoredGroups(value any) []TestGroup {
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var groups []TestGroup
	if err := json.Unmarshal(data, &groups); err != nil {
		return nil
	}
	return groups
}

// runtimeSettingsForStorage boxes a runtime override into the storage layer's
// `any` field, returning a true nil interface (not a typed-nil pointer) when there
// is no override — so it marshals to JSON null and decodes back to nil rather than
// an empty &RuntimeSettingsOverride{}.
func runtimeSettingsForStorage(rs *RuntimeSettingsOverride) any {
	if rs == nil {
		return nil
	}
	return rs
}

func decodeStoredRuntimeSettings(value any) *RuntimeSettingsOverride {
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var overrides RuntimeSettingsOverride
	if err := json.Unmarshal(data, &overrides); err != nil {
		return nil
	}
	return cloneRuntimeSettings(&overrides)
}

func decodeStoredGoalConfig(value any) *GoalConfig {
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var config GoalConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil
	}
	return cloneGoalConfig(&config)
}

func decodeStoredExecutionPlanApproval(value any) *RunArchiveExecutionPlanApprovalSnapshot {
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var snapshot RunArchiveExecutionPlanApprovalSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil
	}
	if snapshot.ID == "" && snapshot.PlanID == "" && snapshot.Policy == "" && snapshot.Status == "" {
		return nil
	}
	return cloneRunArchiveExecutionPlanApprovalSnapshot(&snapshot)
}

// generatorAssignmentForStorage boxes a generator assignment into the storage
// layer's `any` field, returning a true nil interface (not a typed-nil pointer)
// when there is no override — mirroring runtimeSettingsForStorage. Without this,
// a nil *GeneratorAssignmentSettings boxed into `any` becomes a non-nil interface
// that decodeStoredGeneratorAssignment would normalize into {Mode:"auto"}, silently
// turning an inheriting group into an explicit-auto (fleet-wide) one.
func generatorAssignmentForStorage(a *GeneratorAssignmentSettings) any {
	if a == nil {
		return nil
	}
	return cloneGeneratorAssignmentSettings(a)
}

func decodeStoredGeneratorAssignment(value any) *GeneratorAssignmentSettings {
	if value == nil {
		return nil
	}
	// Defense in depth against a typed-nil *GeneratorAssignmentSettings boxed into
	// the `any` field (interface is non-nil but the pointer is nil): treat it as
	// "no assignment" rather than marshalling it to JSON null and normalizing the
	// resulting empty struct into {Mode:"auto"}.
	if a, ok := value.(*GeneratorAssignmentSettings); ok && a == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var assignment GeneratorAssignmentSettings
	if err := json.Unmarshal(data, &assignment); err != nil {
		return nil
	}
	normalized, err := normalizeGeneratorAssignmentSettings(&assignment)
	if err != nil {
		return nil
	}
	return normalized
}
