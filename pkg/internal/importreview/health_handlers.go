package importreview

import (
	"github.com/jasonczech/apexload/pkg/convert/enterprisearchive"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	browserprotocol "github.com/jasonczech/apexload/pkg/protocol/browser"
	"strings"
)

type ScriptValidationResponse struct {
	ScriptID                    string                                              `json:"script_id"`
	Valid                       bool                                                `json:"valid"`
	Issues                      []string                                            `json:"issues"`
	Warnings                    []string                                            `json:"warnings,omitempty"`
	Preflight                   *ReplayPreflight                                    `json:"preflight,omitempty"`
	RuntimeSettingsParity       []enterprisearchive.RuntimeSettingParityEntry       `json:"runtime_settings_parity,omitempty"`
	RuntimeSettingsStatusCounts map[string]int                                      `json:"runtime_settings_status_counts,omitempty"`
	MigrationReadiness          *enterprisearchive.RuntimeSettingMigrationReadiness `json:"migration_readiness,omitempty"`
	DataSourceFit               *DataSourceFitReport                                `json:"data_source_fit,omitempty"`
	AIGuardrails                *AIGuardrailReport                                  `json:"ai_guardrails,omitempty"`
	AuthoringHealth             AuthoringHealth                                     `json:"authoring_health"`
	// ValidatedFingerprint echoes the draft fingerprint the client sent, so a
	// result binds to exactly the document it describes.
	ValidatedFingerprint string `json:"validated_fingerprint,omitempty"`
	// ValidatedContentRevision is the stored script revision a stored-script
	// validation read, when the store keeps durable revisions.
	ValidatedContentRevision string `json:"validated_content_revision,omitempty"`
	// ValidationContext is the controller context the result depended on.
	// Issues and Warnings are static findings about the document; a change
	// to this context invalidates the result even for an unchanged document.
	ValidationContext *ValidationContext `json:"validation_context,omitempty"`
}

// ValidationContext identifies the controller context one validation result
// depended on: capabilities, policy, approvals and secret-reference
// availability. Revision changes whenever any component does; components
// name which one changed. All values are opaque keyed digests.
type ValidationContext struct {
	Version    int                         `json:"version"`
	Revision   string                      `json:"revision"`
	Components ValidationContextComponents `json:"components"`
	// Findings are contextual: they depend on the context, not on the
	// document, and are distinct from static issues and from execution
	// admission, which a start always rechecks.
	Findings []ValidationContextFinding `json:"contextual_findings,omitempty"`
	// SecretReferencesTruncated: only the first SecretReferenceLimit
	// referenced secret names (sorted) entered the secret component.
	SecretReferencesTruncated bool `json:"secret_references_truncated,omitempty"`
	SecretReferenceLimit      int  `json:"secret_reference_limit"`
}

type ValidationContextComponents struct {
	Capabilities     string `json:"capabilities"`
	Policy           string `json:"policy"`
	Approvals        string `json:"approvals"`
	SecretReferences string `json:"secret_references"`
}

// ValidationContextFinding is one contextual finding. Cause is a fixed code:
// secret_unavailable, approval_required, approval_stale or
// capability_unavailable.
type ValidationContextFinding struct {
	Cause   string `json:"cause"`
	Subject string `json:"subject,omitempty"`
	Detail  string `json:"detail"`
}

func BuildScriptValidationResponse(scriptID string, script *dsl.Script, resolvers ...engine.SecretResolver) ScriptValidationResponse {
	return BuildScriptValidationResponseWithVariables(scriptID, script, nil, resolvers...)
}

func BuildScriptValidationResponseWithVariables(scriptID string, script *dsl.Script, variables map[string]string, resolvers ...engine.SecretResolver) ScriptValidationResponse {
	issues := ValidateScript(script)
	return BuildScriptValidationResponseFromIssues(
		scriptID,
		script,
		variables,
		issues,
		resolvers...,
	)
}

func BuildDraftScriptValidationResponseWithVariables(
	scriptID string,
	script *dsl.Script,
	variables map[string]string,
	resolvers ...engine.SecretResolver,
) ScriptValidationResponse {
	return BuildScriptValidationResponseFromIssues(
		scriptID,
		script,
		variables,
		dsl.ValidateScriptIssues(script),
		resolvers...,
	)
}

func BuildScriptValidationResponseFromIssues(
	scriptID string,
	script *dsl.Script,
	variables map[string]string,
	issues []string,
	resolvers ...engine.SecretResolver,
) ScriptValidationResponse {
	for _, blocker := range ScriptImportPromotionBlockers(script) {
		issues = append(issues, "import fidelity blocker: "+blocker)
	}
	warnings := ValidationWarnings(script)
	preflight := BuildReplayPreflight(script, variables, issues, warnings, resolvers...)
	parity := BuildValidationRuntimeSettingsParity(script)
	counts := enterprisearchive.RuntimeSettingStatusCounts(parity)
	readiness := enterprisearchive.BuildRTSMigrationReadiness(parity, ValidationRuntimeSettingMigrationPolicy(script))
	dataSourceFit := BuildDataSourceFitReport(script)
	aiGuardrails := BuildAIGuardrailReport(script, len(issues) == 0)

	return ScriptValidationResponse{
		ScriptID:                    scriptID,
		Valid:                       len(issues) == 0,
		Issues:                      issues,
		Warnings:                    warnings,
		Preflight:                   preflight,
		RuntimeSettingsParity:       parity,
		RuntimeSettingsStatusCounts: counts,
		MigrationReadiness:          readiness,
		DataSourceFit:               dataSourceFit,
		AIGuardrails:                aiGuardrails,
		AuthoringHealth:             BuildAuthoringHealth(issues, warnings, counts, dataSourceFit, aiGuardrails, ScriptHasAuthoringProvenance(script), ReplayReviewGateIssues(script)...),
	}
}

func ScriptHasAuthoringProvenance(script *dsl.Script) bool {
	if script == nil {
		return false
	}
	if strings.TrimSpace(script.GeneratedBy) != "" || strings.TrimSpace(script.GeneratedFrom) != "" {
		return true
	}
	return script.Metadata != nil && script.Metadata.Import != nil
}

func ValidationWarnings(script *dsl.Script) []string {
	if script == nil {
		return nil
	}

	warnings := make([]string, 0, 2)
	if script.Protocol == "browser" {
		engine := strings.TrimSpace(script.Config.Browser.Engine)
		if deviceProfile := strings.TrimSpace(script.Config.Browser.DeviceProfile); deviceProfile != "" {
			if profile, ok := browserprotocol.LookupDevice(deviceProfile); ok && profile.BrowserFamily != "" && engine == "" {
				engine = profile.BrowserFamily
			}
		}
		if warning := browserprotocol.RuntimeInstallWarning(engine); warning != "" {
			warnings = append(warnings, warning)
		}
	}

	if NeedsRecordedResourceMetadataWarning(script) {
		warnings = append(warnings, RecordedResourceMetadataWarning())
	}
	if warning := LightningReloadWarning(script); warning != "" {
		warnings = append(warnings, warning)
	}

	return warnings
}

func NeedsRecordedResourceMetadataWarning(script *dsl.Script) bool {
	if script == nil || script.Protocol != "browser" {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(script.Config.Browser.DownloadResources), "recorded") {
		return false
	}
	return !HasRecordedResourceMetadata(script)
}

func HasRecordedResourceMetadata(script *dsl.Script) bool {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil || script.Metadata.Import.Preserved == nil {
		return false
	}
	rawAvailable, ok := script.Metadata.Import.Preserved["recorded_resource_urls_available"]
	if !ok {
		return false
	}
	available, ok := rawAvailable.(bool)
	return ok && available
}

func RecordedResourceMetadataWarning() string {
	return "Browser replay with download_resources=recorded requires preserved recorded-resource metadata. Manually authored scripts or imports without captured resource URLs cannot enforce recorded-only asset loading yet."
}

func BuildValidationRuntimeSettingsParity(script *dsl.Script) []enterprisearchive.RuntimeSettingParityEntry {
	if script == nil {
		return nil
	}

	preserved := RuntimeSettingsParityValue(ValidationImportPreservedValue(script, "runtime_settings_parity"))
	derived := enterprisearchive.BuildRTSParityReportFromScript(script)
	if len(derived) == 0 {
		return preserved
	}
	if len(preserved) == 0 {
		return derived
	}

	preservedByID := make(map[string]enterprisearchive.RuntimeSettingParityEntry, len(preserved))
	for _, entry := range preserved {
		preservedByID[entry.ID] = entry
	}

	merged := make([]enterprisearchive.RuntimeSettingParityEntry, 0, len(derived)+len(preserved))
	for _, entry := range derived {
		if preservedEntry, ok := preservedByID[entry.ID]; ok {
			entry.RuntimeFiles = AppendUniqueStrings(
				append([]string(nil), preservedEntry.RuntimeFiles...),
				entry.RuntimeFiles...,
			)
			if len(entry.Fields) == 0 {
				entry.Fields = append([]string(nil), preservedEntry.Fields...)
			}
			// A script-derived report can prove that the destination field is now
			// configured, but it cannot disprove an import-time fidelity finding
			// (for example RANDOM think time mapped to recorded defaults). Keep
			// that source finding until review relaxes the migration policy.
			if ValidationMustPreserveImportedRuntimeFinding(preservedEntry.Status) {
				entry.Status = preservedEntry.Status
				if preservedEntry.Area != "" {
					entry.Area = preservedEntry.Area
				}
				if preservedEntry.Concept != "" {
					entry.Concept = preservedEntry.Concept
				}
				if len(preservedEntry.Fields) > 0 {
					entry.Fields = append([]string(nil), preservedEntry.Fields...)
				}
				if preservedEntry.Notes != "" {
					entry.Notes = preservedEntry.Notes
				}
			}
			delete(preservedByID, entry.ID)
		}
		merged = append(merged, entry)
	}

	// Some source-only controls deliberately have no destination field and
	// therefore cannot appear in BuildRTSParityReportFromScript. Retain them as
	// review evidence instead of silently dropping them during validation.
	for _, entry := range preserved {
		if _, ok := preservedByID[entry.ID]; !ok {
			continue
		}
		entry.Fields = append([]string(nil), entry.Fields...)
		entry.RuntimeFiles = append([]string(nil), entry.RuntimeFiles...)
		merged = append(merged, entry)
		delete(preservedByID, entry.ID)
	}

	return merged
}

func ValidationMustPreserveImportedRuntimeFinding(status enterprisearchive.RuntimeSettingStatus) bool {
	switch status {
	case enterprisearchive.RuntimeSettingStatusPartial,
		enterprisearchive.RuntimeSettingStatusCompatibilityOnly,
		enterprisearchive.RuntimeSettingStatusNonApplicable,
		enterprisearchive.RuntimeSettingStatusBlocked:
		return true
	default:
		return false
	}
}

func ValidationRuntimeSettingMigrationPolicy(script *dsl.Script) enterprisearchive.RuntimeSettingMigrationPolicy {
	readiness := EnterpriseArchiveMigrationReadinessValue(ValidationImportPreservedValue(script, "runtime_settings_migration_readiness"))
	if readiness != nil && len(readiness.Policy.BlockStatuses) > 0 {
		return readiness.Policy
	}
	return enterprisearchive.DefaultRuntimeSettingMigrationPolicy()
}

func ValidationImportPreservedValue(script *dsl.Script, key string) any {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil || script.Metadata.Import.Preserved == nil {
		return nil
	}
	return script.Metadata.Import.Preserved[key]
}
