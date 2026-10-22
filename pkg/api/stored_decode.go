package api

import (
	"encoding/json"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

// storedTestGroup extends the public group snapshot with the exact compiled
// runtime inputs that are otherwise process-private. Persisting these fields in
// the existing groups_json column makes a queued/scheduled run restart-safe:
// object-ref resolution, data-pool snapshots, runtime overrides, and effective
// redaction policy must not disappear when a different controller materializes
// the run. Older rows decode with nil RuntimeScript and are rebuilt from their
// current durable script as a compatibility fallback.
type storedTestGroup struct {
	BrowserAuthoredDefinition protocol.BrowserAuthoredDefinition `json:"browser_authored_definition,omitzero"`
	TestGroup
	RuntimeScript     *dsl.Script            `json:"runtime_script,omitempty"`
	BaseScript        *dsl.Script            `json:"base_script,omitempty"`
	SharedDataSources []dsl.DataSource       `json:"shared_data_sources,omitempty"`
	RedactionRules    []engine.RedactionRule `json:"redaction_rules,omitempty"`
}

func encodeStoredGroups(groups []TestGroup) any {
	if len(groups) == 0 {
		return []storedTestGroup{}
	}
	stored := make([]storedTestGroup, 0, len(groups))
	for _, group := range groups {
		entry := storedTestGroup{
			TestGroup:                 group,
			BrowserAuthoredDefinition: group.browserAuthoredDefinition,
			RuntimeScript:             cloneScriptForStoredGroup(group.script),
			BaseScript:                cloneScriptForStoredGroup(group.baseScript),
			SharedDataSources:         cloneDataSources(group.sharedDataSources),
			RedactionRules:            cloneRedactionRules(group.redactionRules),
		}
		stored = append(stored, entry)
	}
	return stored
}

func cloneScriptForStoredGroup(script *dsl.Script) *dsl.Script {
	if script == nil {
		return nil
	}
	payload, err := json.Marshal(script)
	if err != nil {
		return nil
	}
	var cloned dsl.Script
	if err := json.Unmarshal(payload, &cloned); err != nil {
		return nil
	}
	return &cloned
}

func decodeStoredGroups(value any) []TestGroup {
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var stored []storedTestGroup
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil
	}
	groups := make([]TestGroup, 0, len(stored))
	for _, entry := range stored {
		group := entry.TestGroup
		group.browserAuthoredDefinition = entry.BrowserAuthoredDefinition
		group.script = cloneScriptForStoredGroup(entry.RuntimeScript)
		group.baseScript = cloneScriptForStoredGroup(entry.BaseScript)
		group.sharedDataSources = cloneDataSources(entry.SharedDataSources)
		group.redactionRules = cloneRedactionRules(entry.RedactionRules)
		groups = append(groups, group)
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
	if rs, ok := value.(*RuntimeSettingsOverride); ok && rs == nil {
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

func decodeStoredELKCapture(value any) *ELKCaptureConfig {
	if value == nil {
		return nil
	}
	if typed, ok := value.(*ELKCaptureConfig); ok {
		return cloneELKCaptureConfig(typed)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var stored storedELKCapture
	if err := json.Unmarshal(encoded, &stored); err != nil || strings.TrimSpace(stored.ConnectionID) == "" {
		return nil
	}
	return &ELKCaptureConfig{ConnectionID: stored.ConnectionID, ConnectionName: stored.ConnectionName, Endpoint: stored.Endpoint,
		IndexPattern: stored.IndexPattern, TimestampField: stored.TimestampField, LevelField: stored.LevelField,
		MessageField: stored.MessageField, TestIDField: stored.TestIDField, TraceIDField: stored.TraceIDField,
		BaseFilter: stored.BaseFilter, ErrorFilter: stored.ErrorFilter, MetricAggregations: append([]storage.ELKMetricAggregation(nil), stored.MetricAggregations...),
		ProjectID: stored.ProjectID, AuthType: stored.AuthType, APIKeySecretName: stored.APIKeySecretName,
		UsernameSecretName: stored.UsernameSecretName, PasswordSecretName: stored.PasswordSecretName}
}

func decodeStoredDynatraceCapture(value any) *DynatraceCaptureConfig {
	if value == nil {
		return nil
	}
	if typed, ok := value.(*DynatraceCaptureConfig); ok {
		return cloneDynatraceCaptureConfig(typed)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var stored storedDynatraceCapture
	if err := json.Unmarshal(encoded, &stored); err != nil || strings.TrimSpace(stored.ConnectionID) == "" {
		return nil
	}
	return &DynatraceCaptureConfig{ConnectionID: stored.ConnectionID, ConnectionName: stored.ConnectionName, ProjectID: stored.ProjectID,
		EnvironmentURL: stored.EnvironmentURL, PlatformURL: stored.PlatformURL, MetricsEnabled: stored.MetricsEnabled,
		ProblemsEnabled: stored.ProblemsEnabled, LogsEnabled: stored.LogsEnabled, EnvironmentTokenSecret: stored.EnvironmentTokenSecret,
		PlatformAuthType: stored.PlatformAuthType, PlatformTokenSecret: stored.PlatformTokenSecret, OAuthClientIDSecret: stored.OAuthClientIDSecret,
		OAuthClientSecretSecret: stored.OAuthClientSecretSecret, OAuthResource: stored.OAuthResource, OAuthScopes: append([]string(nil), stored.OAuthScopes...),
		MetricQueries: append([]storage.DynatraceMetricQuery(nil), stored.MetricQueries...), ProblemSelector: stored.ProblemSelector,
		ProblemEntitySelector: stored.ProblemEntitySelector, LogBaseFilter: stored.LogBaseFilter, LogTestIDField: stored.LogTestIDField,
		LogTimestampField: stored.LogTimestampField, LogStatusField: stored.LogStatusField, LogMessageField: stored.LogMessageField,
		LogTraceIDField: stored.LogTraceIDField, PollIntervalSeconds: stored.PollIntervalSeconds}
}

func goalConfigForStorage(config *GoalConfig) any {
	if config == nil {
		return nil
	}
	return cloneGoalConfig(config)
}

func decodeStoredGoalConfig(value any) *GoalConfig {
	if value == nil {
		return nil
	}
	if config, ok := value.(*GoalConfig); ok && config == nil {
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

func initialProvenanceForStorage(provenance *RunArchiveProvenance) any {
	if provenance == nil {
		return nil
	}
	masked := maskRunArchiveProvenance(*provenance)
	return &masked
}

func decodeStoredInitialProvenance(value any) *RunArchiveProvenance {
	if value == nil {
		return nil
	}
	if provenance, ok := value.(*RunArchiveProvenance); ok && provenance == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var provenance RunArchiveProvenance
	if err := json.Unmarshal(data, &provenance); err != nil {
		return nil
	}
	masked := maskRunArchiveProvenance(provenance)
	return &masked
}

func startAuthorizationForStorage(authorization *QueuedStartAuthorization) any {
	if authorization == nil {
		return nil
	}
	return cloneQueuedStartAuthorization(authorization)
}

func decodeStoredStartAuthorization(value any) *QueuedStartAuthorization {
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var authorization QueuedStartAuthorization
	if err := json.Unmarshal(data, &authorization); err != nil {
		return nil
	}
	if authorization.CapacityShortfall == nil && authorization.ScriptShakeout == nil && authorization.ScriptShakeoutPass == nil {
		return nil
	}
	return cloneQueuedStartAuthorization(&authorization)
}

func cloneQueuedStartAuthorization(authorization *QueuedStartAuthorization) *QueuedStartAuthorization {
	if authorization == nil {
		return nil
	}
	cloned := *authorization
	if authorization.CapacityShortfall != nil {
		capacity := *authorization.CapacityShortfall
		cloned.CapacityShortfall = &capacity
	}
	if authorization.ScriptShakeout != nil {
		shakeout := *authorization.ScriptShakeout
		cloned.ScriptShakeout = &shakeout
	}
	if authorization.ScriptShakeoutPass != nil {
		pass := *authorization.ScriptShakeoutPass
		cloned.ScriptShakeoutPass = &pass
	}
	return &cloned
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
	normalized, err := normalizeGeneratorAssignmentSettingsBase(&assignment)
	if err != nil {
		return nil
	}
	return normalized
}
