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

func decodeStoredGeneratorAssignment(value any) *GeneratorAssignmentSettings {
	if value == nil {
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
