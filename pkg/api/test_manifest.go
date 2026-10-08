package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

const testRunManifestSchemaVersion = "apexload.test_manifest.v1"

const (
	testRunManifestInitialConfigurationArchiveProvenance = "archive_provenance"
	testRunManifestInitialConfigurationStoredProvenance  = "stored_initial_provenance"
	testRunManifestInitialConfigurationStoredTestRecord  = "stored_test_record"
	testRunManifestInitialConfigurationRetainedRunState  = "retained_run_state"
)

type TestRunManifest struct {
	TelemetryPolicy                *TelemetryPolicyEvidence               `json:"telemetry_policy,omitempty"`
	SchemaVersion                  string                                 `json:"schema_version"`
	TestID                         string                                 `json:"test_id"`
	RefKey                         string                                 `json:"ref_key,omitempty"`
	TestName                       string                                 `json:"test_name"`
	Status                         string                                 `json:"status"`
	Source                         string                                 `json:"source"`
	GeneratedAt                    time.Time                              `json:"generated_at"`
	Identity                       TestRunManifestIdentity                `json:"identity"`
	InitialConfiguration           TestRunManifestConfiguration           `json:"initial_configuration"`
	InitialConfigurationProvenance TestRunManifestConfigurationProvenance `json:"initial_configuration_provenance"`
	CurrentConfiguration           TestRunManifestConfiguration           `json:"current_configuration"`
	Scripts                        []TestRunManifestScript                `json:"scripts,omitempty"`
	LoadGenerators                 TestRunManifestLoadGenerators          `json:"load_generators"`
	RuntimeChanges                 []TestRunManifestRuntimeChange         `json:"runtime_changes,omitempty"`
	Archive                        *TestRunManifestArchive                `json:"archive,omitempty"`
	ContinueOnError                *TestRunManifestContinueOnError        `json:"continue_on_error,omitempty"`
	Warnings                       []string                               `json:"warnings,omitempty"`
}

// TestRunManifestContinueOnError reports the scenario's continue-on-error policy
// and every group that diverges from it. Continue-on-error is enabled unless the
// scenario explicitly disables it; a group or script may still opt out, and each
// opt-out is listed here so the divergence is visible rather than silent.
type TestRunManifestContinueOnError struct {
	// ScenarioValue is the scenario-level policy value.
	ScenarioValue bool `json:"scenario_value"`
	// ScenarioExplicit is false when ScenarioValue came from the enabled default
	// rather than from the scenario settings.
	ScenarioExplicit bool `json:"scenario_explicit"`
	// Conflicts lists the groups whose effective value differs from
	// ScenarioValue, in group order.
	Conflicts []TestRunManifestContinueOnErrorConflict `json:"conflicts,omitempty"`
}

type TestRunManifestContinueOnErrorConflict struct {
	GroupName  string `json:"group_name"`
	ScriptID   string `json:"script_id,omitempty"`
	ScriptName string `json:"script_name,omitempty"`
	// Effective is the value this group runs with; it wins over ScenarioValue.
	Effective bool `json:"effective"`
	// Source names the layer that supplied Effective ("group" or "script").
	Source string `json:"source"`
	Detail string `json:"detail"`
}

// TestRunManifestConfigurationProvenance tells clients whether the initial
// configuration is backed by a durable run snapshot or was reconstructed from
// mutable retained process state. Clients must not describe the latter as an
// immutable queued snapshot.
type TestRunManifestConfigurationProvenance struct {
	Source    string `json:"source"`
	Immutable bool   `json:"immutable"`
}

type TestRunManifestIdentity struct {
	ApplicationID        string     `json:"application_id,omitempty"`
	ReleaseID            string     `json:"release_id,omitempty"`
	ScenarioID           string     `json:"scenario_id,omitempty"`
	ProjectID            string     `json:"project_id,omitempty"`
	FolderID             *string    `json:"folder_id,omitempty"`
	CreatedAt            time.Time  `json:"created_at,omitempty"`
	ScheduledStartAt     *time.Time `json:"scheduled_start_at,omitempty"`
	StartedAt            *time.Time `json:"started_at,omitempty"`
	EndedAt              *time.Time `json:"ended_at,omitempty"`
	CreatedBySubject     string     `json:"created_by_subject,omitempty"`
	CreatedByDisplayName string     `json:"created_by_display_name,omitempty"`
	StartedBySubject     string     `json:"started_by_subject,omitempty"`
	StartedByDisplayName string     `json:"started_by_display_name,omitempty"`
	StoppedBySubject     string     `json:"stopped_by_subject,omitempty"`
	StoppedByDisplayName string     `json:"stopped_by_display_name,omitempty"`
}

type TestRunManifestConfiguration struct {
	Status                string                       `json:"status,omitempty"`
	Duration              string                       `json:"duration,omitempty"`
	TotalVUsers           int                          `json:"total_vusers,omitempty"`
	ActiveVUsers          int64                        `json:"active_vusers,omitempty"`
	StartDelay            string                       `json:"start_delay,omitempty"`
	ScenarioMode          string                       `json:"scenario_mode,omitempty"`
	ScriptShakeout        bool                         `json:"script_shakeout,omitempty"`
	ResourceBaseline      bool                         `json:"resource_baseline,omitempty"`
	Assignment            *GeneratorAssignmentSettings `json:"assignment,omitempty"`
	GeneratorShards       []GeneratorShardSummary      `json:"generator_shards,omitempty"`
	GoalConfig            *GoalConfig                  `json:"goal_config,omitempty"`
	TrafficProfile        *TrafficProfile              `json:"traffic_profile,omitempty"`
	RuntimeSettings       *RuntimeSettingsOverride     `json:"runtime_settings,omitempty"`
	ELKCapture            *ELKCaptureConfig            `json:"elk_capture,omitempty"`
	DynatraceCapture      *DynatraceCaptureConfig      `json:"dynatrace_capture,omitempty"`
	RuntimeSettingsReview *RuntimeSettingsReview       `json:"runtime_settings_review,omitempty"`
	Groups                []TestGroup                  `json:"groups,omitempty"`
	VUserRamps            map[string]VUserRampStatus   `json:"vuser_ramps,omitempty"`
	BaselinePinned        bool                         `json:"baseline_pinned,omitempty"`
	BaselinePinnedAt      *time.Time                   `json:"baseline_pinned_at,omitempty"`
	StartedAt             *time.Time                   `json:"started_at,omitempty"`
	EndedAt               *time.Time                   `json:"ended_at,omitempty"`
}

type TestRunManifestScript struct {
	GroupName       string                       `json:"group_name"`
	ScriptID        string                       `json:"script_id,omitempty"`
	ScriptName      string                       `json:"script_name,omitempty"`
	Protocol        string                       `json:"protocol,omitempty"`
	VUsers          int                          `json:"vusers,omitempty"`
	RampUp          string                       `json:"ramp_up,omitempty"`
	RampDown        string                       `json:"ramp_down,omitempty"`
	StartOffset     string                       `json:"start_offset,omitempty"`
	Stages          []storage.LoadStage          `json:"stages,omitempty"`
	Iterations      *int                         `json:"iterations,omitempty"`
	RuntimeSettings *RuntimeSettingsOverride     `json:"runtime_settings,omitempty"`
	Assignment      *GeneratorAssignmentSettings `json:"assignment,omitempty"`
	RunLogicGoals   *RunLogicGoalSnapshot        `json:"run_logic_goals,omitempty"`
	Initialization  string                       `json:"initialization,omitempty"`
	ContinueOnError *ContinueOnErrorResolution   `json:"continue_on_error,omitempty"`
}

type TestRunManifestLoadGenerators struct {
	Assignment         *GeneratorAssignmentSettings        `json:"assignment,omitempty"`
	GeneratorShards    []GeneratorShardSummary             `json:"generator_shards,omitempty"`
	GeneratorInventory []GeneratorInventoryEntry           `json:"generator_inventory,omitempty"`
	ExecutionSettings  RunArchiveExecutionSettingsSnapshot `json:"execution_settings"`
	ControllerEnv      map[string]string                   `json:"controller_env,omitempty"`
}

type TestRunManifestRuntimeChange struct {
	ID               string         `json:"id"`
	Action           string         `json:"action"`
	Label            string         `json:"label"`
	Status           string         `json:"status,omitempty"`
	ActorType        string         `json:"actor_type,omitempty"`
	ActorSubject     string         `json:"actor_subject,omitempty"`
	ActorDisplayName string         `json:"actor_display_name,omitempty"`
	Details          map[string]any `json:"details,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
}

type TestRunManifestArchive struct {
	SchemaVersion        string    `json:"schema_version,omitempty"`
	ArchiveBackend       string    `json:"archive_backend,omitempty"`
	ArchivePath          string    `json:"archive_path,omitempty"`
	ManifestPath         string    `json:"manifest_path,omitempty"`
	ArchiveURI           string    `json:"archive_uri,omitempty"`
	ManifestURI          string    `json:"manifest_uri,omitempty"`
	TestManifestFile     string    `json:"test_manifest_file,omitempty"`
	TestNotesFile        string    `json:"test_notes_file,omitempty"`
	TransactionCount     int64     `json:"transaction_count,omitempty"`
	ResourceSampleCount  int64     `json:"resource_sample_count,omitempty"`
	IntervalSummaryCount int64     `json:"interval_summary_count,omitempty"`
	CreatedAt            time.Time `json:"created_at,omitempty"`
	CompletedAt          time.Time `json:"completed_at,omitempty"`
	SLABreached          bool      `json:"sla_breached,omitempty"`
	SLADetails           []string  `json:"sla_details,omitempty"`
	Error                string    `json:"error,omitempty"`
}

func maskTestRunManifest(manifest *TestRunManifest) *TestRunManifest {
	if manifest == nil {
		return nil
	}
	masked := *manifest
	masked.TelemetryPolicy = cloneTelemetryPolicyEvidence(manifest.TelemetryPolicy)
	masked.Archive = maskTestRunManifestArchive(manifest.Archive)
	masked.InitialConfiguration = maskTestRunManifestConfiguration(manifest.InitialConfiguration)
	masked.CurrentConfiguration = maskTestRunManifestConfiguration(manifest.CurrentConfiguration)
	if len(manifest.Scripts) > 0 {
		masked.Scripts = append([]TestRunManifestScript(nil), manifest.Scripts...)
		for i := range masked.Scripts {
			masked.Scripts[i].RuntimeSettings = maskRuntimeSettings(masked.Scripts[i].RuntimeSettings)
		}
	}
	return &masked
}

func maskTestRunManifestArchive(archive *TestRunManifestArchive) *TestRunManifestArchive {
	if archive == nil {
		return nil
	}
	masked := *archive
	masked.ArchiveBackend = ""
	masked.ArchivePath = ""
	masked.ManifestPath = ""
	masked.ArchiveURI = ""
	masked.ManifestURI = ""
	masked.SLADetails = append([]string(nil), archive.SLADetails...)
	return &masked
}

func maskTestRunManifestConfiguration(configuration TestRunManifestConfiguration) TestRunManifestConfiguration {
	configuration.TrafficProfile = cloneTrafficProfile(configuration.TrafficProfile)
	configuration.RuntimeSettings = maskRuntimeSettings(configuration.RuntimeSettings)
	configuration.Groups = maskTestGroupsRuntimeSettings(configuration.Groups)
	return configuration
}

func (tr *TestRunner) GetTestManifest(id string) (*TestRunManifest, error) {
	return tr.buildTestManifest(id, nil, true)
}

func (tr *TestRunner) buildTestManifest(id string, archiveOverride *storage.RunArchiveRecord, preferPersisted bool) (*TestRunManifest, error) {
	test, ok := tr.GetTest(id)
	if !ok {
		return nil, fmt.Errorf("test not found: %s", id)
	}
	return tr.buildTestManifestForManagedTest(test, id, archiveOverride, preferPersisted)
}

func (tr *TestRunner) buildTestManifestForManagedTest(test *ManagedTest, requestedID string, archiveOverride *storage.RunArchiveRecord, preferPersisted bool) (*TestRunManifest, error) {
	if test == nil {
		return nil, fmt.Errorf("test not found: %s", requestedID)
	}
	warnings := []string{}
	var archiveRecord *storage.RunArchiveRecord
	if archiveOverride != nil {
		archiveRecord = archiveOverride
	} else if record, found := tr.GetRunArchive(test.ID); found {
		archiveRecord = record
	}

	var archiveManifest *RunArchiveManifest
	if archiveRecord != nil {
		if archiveOverride != nil {
			// Producer finalization owns a committed local directory that has not
			// necessarily been uploaded yet. Reading it through the materializer
			// would try to fetch the not-yet-published remote manifest and can add a
			// false warning (or block until the remote timeout).
			loaded, err := loadRunArchiveManifestFromRecord(archiveRecord)
			if err != nil {
				warnings = append(warnings, "archive manifest could not be loaded: "+err.Error())
			} else {
				archiveManifest = loaded
			}
		} else {
			lease, err := acquireArchiveMaterialized(context.Background(), archiveRecord)
			if err != nil {
				warnings = append(warnings, "archive record exists but could not be materialized: "+err.Error())
			} else {
				defer lease.Release()
				loaded, err := loadRunArchiveManifestFromRecord(archiveRecord)
				if err != nil {
					warnings = append(warnings, "archive manifest could not be loaded: "+err.Error())
				} else {
					archiveManifest = loaded
				}
			}
		}
		if preferPersisted && archiveManifest != nil && strings.TrimSpace(archiveManifest.TestManifestFile) != "" {
			persisted, err := loadTestRunManifest(filepath.Join(archiveRecord.ArchivePath, archiveManifest.TestManifestFile))
			if err == nil {
				return maskTestRunManifest(persisted), nil
			}
			warnings = append(warnings, "persisted test manifest could not be loaded: "+err.Error())
		}
	}

	status := test.Status
	completedAt := time.Time{}
	source := "live"
	if archiveManifest != nil {
		source = "archive_fallback"
		if strings.TrimSpace(archiveManifest.Status) != "" {
			status = archiveManifest.Status
		}
		completedAt = archiveManifest.CompletedAt
		warnings = append(warnings, "archive does not contain a persisted test_manifest.json; synthesized from archive provenance and retained run state")
	}
	if archiveOverride != nil {
		source = "archive"
		warnings = removeWarningPrefix(warnings, "archive does not contain a persisted")
	}

	initial, initialProvenance, initialWarnings := tr.initialManifestConfiguration(test, archiveManifest)
	warnings = append(warnings, initialWarnings...)
	current := manifestConfigurationFromManagedTest(test)
	current.Status = status
	if current.EndedAt == nil && !completedAt.IsZero() {
		current.EndedAt = &completedAt
	}

	provenance := buildRunArchiveProvenance(tr, test)
	if archiveManifest != nil {
		provenance = archiveManifest.Provenance
	}

	runtimeChanges, auditWarnings := tr.testManifestRuntimeChanges(test.ID, test.RefKey, requestedID)
	warnings = append(warnings, auditWarnings...)

	continueOnError := manifestContinueOnErrorFromGroups(current.Groups)
	if continueOnError != nil {
		for _, conflict := range continueOnError.Conflicts {
			warnings = append(warnings, conflict.Detail)
		}
	}

	policyInitial := test.initialArchiveProvenance
	if archiveManifest != nil {
		policyInitial = archiveManifest.InitialProvenance
	} else if policyInitial == nil {
		if record, err := tr.storedTestRecord(test.ID); err == nil && record != nil && normalizeProjectID(record.ProjectID) == normalizeProjectID(test.ProjectID) {
			policyInitial = decodeStoredInitialProvenance(record.InitialProvenance)
		}
	}
	manifest := &TestRunManifest{
		TelemetryPolicy:                telemetryPolicyEvidenceFromProvenance(policyInitial),
		SchemaVersion:                  testRunManifestSchemaVersion,
		TestID:                         test.ID,
		RefKey:                         test.RefKey,
		TestName:                       test.Name,
		Status:                         status,
		Source:                         source,
		GeneratedAt:                    time.Now().UTC(),
		Identity:                       manifestIdentityFromTest(test, completedAt),
		InitialConfiguration:           initial,
		InitialConfigurationProvenance: initialProvenance,
		CurrentConfiguration:           current,
		Scripts:                        manifestScriptsFromGroups(current.Groups),
		LoadGenerators:                 manifestLoadGeneratorsFromProvenance(provenance),
		RuntimeChanges:                 runtimeChanges,
		Archive:                        manifestArchiveFromRecord(archiveRecord, archiveManifest),
		ContinueOnError:                continueOnError,
		Warnings:                       uniqueStrings(warnings),
	}
	return manifest, nil
}

func (tr *TestRunner) initialManifestConfiguration(test *ManagedTest, archiveManifest *RunArchiveManifest) (TestRunManifestConfiguration, TestRunManifestConfigurationProvenance, []string) {
	if archiveManifest != nil {
		if archiveManifest.InitialProvenance != nil {
			return manifestConfigurationFromProvenance(*archiveManifest.InitialProvenance, archiveManifest.Status, archiveManifest.CompletedAt), TestRunManifestConfigurationProvenance{
				Source:    testRunManifestInitialConfigurationArchiveProvenance,
				Immutable: true,
			}, nil
		}
		// Older archives only carry the final refreshed provenance. Runtime scale
		// and duration controls may already be reflected there, so fail closed
		// instead of labelling it as the immutable queued configuration.
		return manifestConfigurationFromProvenance(archiveManifest.Provenance, archiveManifest.Status, archiveManifest.CompletedAt), TestRunManifestConfigurationProvenance{
			Source:    testRunManifestInitialConfigurationArchiveProvenance,
			Immutable: false,
		}, []string{"archive predates immutable initial provenance; initial configuration may reflect runtime changes"}
	}
	if test != nil && test.initialArchiveProvenance != nil {
		completedAt := time.Time{}
		if test.EndedAt != nil {
			completedAt = *test.EndedAt
		}
		return manifestConfigurationFromProvenance(*test.initialArchiveProvenance, test.Status, completedAt), TestRunManifestConfigurationProvenance{
			Source:    testRunManifestInitialConfigurationStoredProvenance,
			Immutable: true,
		}, nil
	}
	record, err := tr.storedTestRecord(test.ID)
	if err == nil && record != nil {
		if storedInitial := decodeStoredInitialProvenance(record.InitialProvenance); storedInitial != nil {
			completedAt := time.Time{}
			if record.EndTime != nil {
				completedAt = *record.EndTime
			}
			return manifestConfigurationFromProvenance(*storedInitial, record.Status, completedAt), TestRunManifestConfigurationProvenance{
				Source:    testRunManifestInitialConfigurationStoredProvenance,
				Immutable: true,
			}, nil
		}
		return manifestConfigurationFromTestRecord(record), TestRunManifestConfigurationProvenance{
			Source:    testRunManifestInitialConfigurationStoredTestRecord,
			Immutable: false,
		}, []string{"stored test record is mutable; initial configuration may reflect later runtime changes"}
	}
	provenance := TestRunManifestConfigurationProvenance{
		Source:    testRunManifestInitialConfigurationRetainedRunState,
		Immutable: false,
	}
	if err != nil {
		return manifestConfigurationFromManagedTest(test), provenance, []string{"stored initial test record could not be loaded: " + err.Error()}
	}
	return manifestConfigurationFromManagedTest(test), provenance, []string{"stored initial test record is unavailable; initial configuration mirrors current retained run state"}
}

func (tr *TestRunner) storedTestRecord(id string) (*storage.TestRecord, error) {
	if tr.testStore == nil {
		return nil, nil
	}
	canonicalID := tr.resolveTestID(id)
	return tr.testStore.GetTest(canonicalID)
}

func manifestIdentityFromTest(test *ManagedTest, completedAt time.Time) TestRunManifestIdentity {
	identity := TestRunManifestIdentity{
		ApplicationID:        test.ApplicationID,
		ReleaseID:            test.ReleaseID,
		ScenarioID:           test.ScenarioID,
		ProjectID:            normalizeProjectID(test.ProjectID),
		FolderID:             test.FolderID,
		CreatedAt:            test.CreatedAt,
		ScheduledStartAt:     cloneTimePtr(test.ScheduledStartAt),
		StartedAt:            cloneTimePtr(test.StartedAt),
		EndedAt:              cloneTimePtr(test.EndedAt),
		CreatedBySubject:     test.CreatedBySubject,
		CreatedByDisplayName: test.CreatedByDisplayName,
		StartedBySubject:     test.StartedBySubject,
		StartedByDisplayName: test.StartedByDisplayName,
		StoppedBySubject:     test.StoppedBySubject,
		StoppedByDisplayName: test.StoppedByDisplayName,
	}
	if identity.EndedAt == nil && !completedAt.IsZero() {
		identity.EndedAt = &completedAt
	}
	return identity
}

func manifestConfigurationFromManagedTest(test *ManagedTest) TestRunManifestConfiguration {
	if test == nil {
		return TestRunManifestConfiguration{}
	}
	activeVUsers := int64(0)
	if test.LiveMetrics != nil {
		activeVUsers = test.LiveMetrics.ActiveVUsers
	}
	return TestRunManifestConfiguration{
		Status:                test.Status,
		Duration:              durationString(test.Duration, test.DurationStr),
		TotalVUsers:           test.TotalVUsers,
		ActiveVUsers:          activeVUsers,
		StartDelay:            test.StartDelay,
		ScenarioMode:          test.ScenarioMode,
		ScriptShakeout:        test.ScriptShakeout,
		ResourceBaseline:      test.ResourceBaseline,
		Assignment:            cloneGeneratorAssignmentSettings(test.Assignment),
		GeneratorShards:       cloneGeneratorShardSummaries(test.GeneratorShards),
		GoalConfig:            cloneGoalConfig(test.GoalConfig),
		TrafficProfile:        cloneTrafficProfile(test.TrafficProfile),
		RuntimeSettings:       maskRuntimeSettings(test.RuntimeSettings),
		ELKCapture:            cloneELKCaptureConfig(test.ELKCapture),
		DynatraceCapture:      cloneDynatraceCaptureConfig(test.DynatraceCapture),
		RuntimeSettingsReview: cloneRuntimeSettingsReview(test.RuntimeSettingsReview),
		Groups:                maskTestGroupsRuntimeSettings(test.Groups),
		VUserRamps:            cloneVUserRampStatusMap(test.VUserRamps),
		BaselinePinned:        test.BaselinePinned,
		BaselinePinnedAt:      cloneTimePtr(test.BaselinePinnedAt),
		StartedAt:             cloneTimePtr(test.StartedAt),
		EndedAt:               cloneTimePtr(test.EndedAt),
	}
}

func manifestConfigurationFromTestRecord(record *storage.TestRecord) TestRunManifestConfiguration {
	if record == nil {
		return TestRunManifestConfiguration{}
	}
	return TestRunManifestConfiguration{
		Status:           record.Status,
		Duration:         durationString(time.Duration(record.Duration), ""),
		TotalVUsers:      record.TotalVUsers,
		StartDelay:       record.StartDelay,
		ScenarioMode:     normalizeScenarioMode(record.ScenarioMode),
		ScriptShakeout:   record.ScriptShakeout,
		ResourceBaseline: record.ResourceBaseline,
		Assignment:       decodeStoredGeneratorAssignment(record.Assignment),
		GoalConfig:       decodeStoredGoalConfig(record.GoalConfig),
		TrafficProfile:   decodeStoredTrafficProfile(record.TrafficProfile),
		RuntimeSettings:  maskRuntimeSettings(decodeStoredRuntimeSettings(record.RuntimeSettings)),
		ELKCapture:       cloneELKCaptureConfig(decodeStoredELKCapture(record.ELKCapture)),
		DynatraceCapture: cloneDynatraceCaptureConfig(decodeStoredDynatraceCapture(record.DynatraceCapture)),
		Groups:           maskTestGroupsRuntimeSettings(decodeStoredGroups(record.Groups)),
		BaselinePinned:   record.BaselinePinned,
		BaselinePinnedAt: cloneTimePtr(record.BaselinePinnedAt),
		StartedAt:        cloneTimePtr(record.StartedAt),
		EndedAt:          cloneTimePtr(record.EndTime),
	}
}

func manifestConfigurationFromProvenance(provenance RunArchiveProvenance, status string, completedAt time.Time) TestRunManifestConfiguration {
	var endedAt *time.Time
	if !completedAt.IsZero() {
		endedAt = &completedAt
	}
	return TestRunManifestConfiguration{
		Status:                status,
		Duration:              provenance.Duration,
		TotalVUsers:           provenance.TotalVUsers,
		StartDelay:            provenance.StartDelay,
		ScenarioMode:          provenance.ScenarioMode,
		ScriptShakeout:        provenance.ScriptShakeout,
		ResourceBaseline:      provenance.ResourceBaseline,
		Assignment:            cloneGeneratorAssignmentSettings(provenance.Assignment),
		GeneratorShards:       cloneGeneratorShardSummaries(provenance.GeneratorShards),
		GoalConfig:            cloneGoalConfig(provenance.GoalConfig),
		TrafficProfile:        cloneTrafficProfile(provenance.TrafficProfile),
		RuntimeSettings:       maskRuntimeSettings(provenance.RuntimeSettings),
		ELKCapture:            cloneELKCaptureConfig(provenance.ELKCapture),
		DynatraceCapture:      cloneDynatraceCaptureConfig(provenance.DynatraceCapture),
		RuntimeSettingsReview: cloneRuntimeSettingsReview(provenance.RuntimeSettingsReview),
		Groups:                maskTestGroupsRuntimeSettings(provenance.Groups),
		BaselinePinned:        provenance.BaselinePinned,
		BaselinePinnedAt:      cloneTimePtr(provenance.BaselinePinnedAt),
		StartedAt:             cloneTimePtr(provenance.StartedAt),
		EndedAt:               endedAt,
	}
}

func manifestScriptsFromGroups(groups []TestGroup) []TestRunManifestScript {
	if len(groups) == 0 {
		return nil
	}
	scripts := make([]TestRunManifestScript, 0, len(groups))
	for _, group := range groups {
		scripts = append(scripts, TestRunManifestScript{
			GroupName:       group.Name,
			ScriptID:        group.ScriptID,
			ScriptName:      group.ScriptName,
			Protocol:        group.Protocol,
			VUsers:          group.VUsers,
			RampUp:          group.RampUp,
			RampDown:        group.RampDown,
			StartOffset:     group.StartOffset,
			Stages:          append([]storage.LoadStage(nil), group.Stages...),
			Iterations:      cloneIntPtr(group.Iterations),
			RuntimeSettings: maskRuntimeSettings(group.RuntimeSettings),
			Assignment:      cloneGeneratorAssignmentSettings(group.Assignment),
			RunLogicGoals:   cloneRunLogicGoalSnapshot(group.RunLogicGoals),
			Initialization:  group.Initialization,
			ContinueOnError: cloneContinueOnErrorResolution(group.ContinueOnError),
		})
	}
	return scripts
}

func cloneContinueOnErrorResolution(resolution *ContinueOnErrorResolution) *ContinueOnErrorResolution {
	if resolution == nil {
		return nil
	}
	clone := *resolution
	return &clone
}

// manifestContinueOnErrorFromGroups summarizes the scenario's continue-on-error
// policy across the compiled groups. Groups compiled before the policy existed
// carry no resolution; they are skipped rather than guessed at, and a manifest
// with no resolved group at all reports no policy block.
func manifestContinueOnErrorFromGroups(groups []TestGroup) *TestRunManifestContinueOnError {
	summary := &TestRunManifestContinueOnError{}
	resolved := false
	for _, group := range groups {
		resolution := group.ContinueOnError
		if resolution == nil {
			continue
		}
		if !resolved {
			summary.ScenarioValue = resolution.ScenarioValue
			summary.ScenarioExplicit = resolution.ScenarioExplicit
			resolved = true
		}
		if !resolution.Conflict {
			continue
		}
		summary.Conflicts = append(summary.Conflicts, TestRunManifestContinueOnErrorConflict{
			GroupName:  group.Name,
			ScriptID:   group.ScriptID,
			ScriptName: group.ScriptName,
			Effective:  resolution.Effective,
			Source:     resolution.Source,
			Detail:     continueOnErrorConflictDetail(group.Name, *resolution),
		})
	}
	if !resolved {
		return nil
	}
	return summary
}

func manifestLoadGeneratorsFromProvenance(provenance RunArchiveProvenance) TestRunManifestLoadGenerators {
	return TestRunManifestLoadGenerators{
		Assignment:         cloneGeneratorAssignmentSettings(provenance.Assignment),
		GeneratorShards:    cloneGeneratorShardSummaries(provenance.GeneratorShards),
		GeneratorInventory: cloneGeneratorInventoryEntries(provenance.GeneratorInventory),
		ExecutionSettings:  provenance.ExecutionSettings,
		ControllerEnv:      cloneStringMap(provenance.ControllerEnv),
	}
}

func manifestArchiveFromRecord(record *storage.RunArchiveRecord, manifest *RunArchiveManifest) *TestRunManifestArchive {
	if record == nil {
		return nil
	}
	archive := &TestRunManifestArchive{
		SchemaVersion:        record.SchemaVersion,
		ArchiveBackend:       record.ArchiveBackend,
		ArchivePath:          record.ArchivePath,
		ManifestPath:         record.ManifestPath,
		ArchiveURI:           record.ArchiveURI,
		ManifestURI:          record.ManifestURI,
		TransactionCount:     record.TransactionCount,
		ResourceSampleCount:  record.ResourceSampleCount,
		IntervalSummaryCount: record.IntervalSummaryCount,
		CreatedAt:            record.CreatedAt,
		CompletedAt:          record.CompletedAt,
	}
	if manifest != nil {
		archive.SchemaVersion = manifest.SchemaVersion
		archive.TestManifestFile = manifest.TestManifestFile
		archive.TestNotesFile = manifest.TestNotesFile
		archive.SLABreached = manifest.SLABreached
		archive.SLADetails = append([]string(nil), manifest.SLADetails...)
		archive.Error = manifest.Error
	}
	return archive
}

func (tr *TestRunner) testManifestRuntimeChanges(testID string, refKey string, requestedID string) ([]TestRunManifestRuntimeChange, []string) {
	if tr.auditStore == nil {
		return nil, []string{"audit storage is unavailable; runtime changes could not be loaded"}
	}
	resourceIDs := []string{testID, refKey, requestedID}
	seenResourceIDs := map[string]struct{}{}
	seenEvents := map[string]struct{}{}
	auditEvents := []*storage.AuditEvent{}
	warnings := []string{}
	for _, resourceID := range resourceIDs {
		resourceID = strings.TrimSpace(resourceID)
		if resourceID == "" {
			continue
		}
		if _, seen := seenResourceIDs[resourceID]; seen {
			continue
		}
		seenResourceIDs[resourceID] = struct{}{}
		events, err := tr.auditStore.ListAuditEvents(storage.AuditEventFilter{
			Limit:        500,
			ResourceType: "test",
			ResourceID:   resourceID,
		})
		if err != nil {
			warnings = append(warnings, "audit events could not be loaded for "+resourceID+": "+err.Error())
			continue
		}
		for _, event := range events {
			if event == nil || !testManifestAuditAction(event.Action) {
				continue
			}
			key := event.ID
			if key == "" {
				key = event.Action + event.CreatedAt.Format(time.RFC3339Nano)
			}
			if _, seen := seenEvents[key]; seen {
				continue
			}
			seenEvents[key] = struct{}{}
			auditEvents = append(auditEvents, event)
		}
	}
	terminalOperations := map[string]struct{}{}
	for _, event := range auditEvents {
		operationID, _ := event.Details["operation_id"].(string)
		operationID = strings.TrimSpace(operationID)
		if operationID != "" && event.Status != "pending" {
			terminalOperations[operationID] = struct{}{}
		}
	}
	changes := make([]TestRunManifestRuntimeChange, 0, len(auditEvents))
	for _, event := range auditEvents {
		operationID, _ := event.Details["operation_id"].(string)
		operationID = strings.TrimSpace(operationID)
		if event.Status == "pending" && operationID != "" {
			if _, completed := terminalOperations[operationID]; completed {
				continue
			}
		}
		// Required-audit attempts that definitively failed or completed as a
		// no-op are governance evidence, not runtime changes. An unmatched
		// pending intent remains visible because its commit state is ambiguous.
		if event.Action == "test.runtime_settings.update" || event.Action == "test.parameters.update" {
			outcome, _ := event.Details["outcome"].(string)
			if event.Status != "pending" && (event.Status != "success" || strings.TrimSpace(outcome) == "no_changes") {
				continue
			}
		}
		changes = append(changes, TestRunManifestRuntimeChange{
			ID:               event.ID,
			Action:           event.Action,
			Label:            testManifestAuditLabel(event.Action),
			Status:           event.Status,
			ActorType:        event.ActorType,
			ActorSubject:     event.ActorSubject,
			ActorDisplayName: event.ActorDisplayName,
			Details:          safeManifestAuditDetails(event.Details),
			CreatedAt:        event.CreatedAt,
		})
	}
	sort.SliceStable(changes, func(i, j int) bool {
		return changes[i].CreatedAt.Before(changes[j].CreatedAt)
	})
	return changes, warnings
}

func testManifestAuditAction(action string) bool {
	switch action {
	case "test.create", "test.start", "test.pause", "test.resume", "test.stop", "test.abort", "test.vusers.scale", "test.duration.adjust", "test.runtime.adjust",
		"test.runtime_settings.update", "test.parameters.update":
		return true
	default:
		return false
	}
}

func testManifestAuditLabel(action string) string {
	switch action {
	case "test.create":
		return "Run created"
	case "test.start":
		return "Run started"
	case "test.pause":
		return "Run paused"
	case "test.resume":
		return "Run resumed"
	case "test.stop":
		return "Graceful stop requested"
	case "test.abort":
		return "Abort requested"
	case "test.vusers.scale":
		return "VUsers changed"
	case "test.duration.adjust":
		return "Duration changed"
	case "test.runtime.adjust":
		return "Runtime adjusted"
	case "test.runtime_settings.update":
		return "Runtime settings changed"
	case "test.parameters.update":
		return "Parameters changed"
	default:
		return action
	}
}

func safeManifestAuditDetails(details map[string]any) map[string]any {
	if len(details) == 0 {
		return nil
	}
	allowed := map[string]struct{}{
		"action":                 {},
		"available_generators":   {},
		"availability_source":    {},
		"changes":                {},
		"duration":               {},
		"fields":                 {},
		"group":                  {},
		"name":                   {},
		"new_duration":           {},
		"override_requested":     {},
		"parameters":             {},
		"peak_vusers":            {},
		"ramp_duration":          {},
		"reason":                 {},
		"recommended_generators": {},
		"requires_override":      {},
		"scenario_duration":      {},
		"shortfall_generators":   {},
		"status":                 {},
		"target_vusers":          {},
		"total_vusers":           {},
		"warnings":               {},
	}
	out := map[string]any{}
	for key, value := range details {
		if _, ok := allowed[key]; !ok {
			continue
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (tr *TestRunner) persistTestManifestArtifact(test *ManagedTest, record *storage.RunArchiveRecord) error {
	return tr.persistTestManifestArtifactWithPreparation(test, record, nil, nil, false)
}

// persistTestManifestArtifactWithPreparation writes the manifest's adjuncts;
// refresh is an adjunct refresh of an already published archive.
func (tr *TestRunner) persistTestManifestArtifactWithPreparation(test *ManagedTest, record *storage.RunArchiveRecord, prepare func([]byte) error, budget *archiveOutputBudget, refresh bool) error {
	if test == nil || record == nil || strings.TrimSpace(record.ArchivePath) == "" || strings.TrimSpace(record.ManifestPath) == "" {
		return nil
	}
	if budget == nil {
		var err error
		budget, err = newArchiveAdjunctBudgetForRecord(context.Background(), record)
		if err != nil {
			return err
		}
		defer budget.close()
	}
	archiveManifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		return err
	}
	archiveManifest.SLAEvaluated = archiveManifest.SLAEvaluated || test.ciSLAEvaluated
	archiveManifest.SLABreached = archiveManifest.SLABreached || test.ciSLABreached
	archiveManifest.SLADetails = compactNonEmptyStrings(append(archiveManifest.SLADetails, test.ciSLADetails...)...)
	if err := validateImmutableArchiveArtifactIntegrity(record.ArchivePath, archiveManifest); err != nil {
		return err
	}
	if err := tr.persistRunMonitorArchive(test.ProjectID, test.ID, record, archiveManifest, budget); err != nil {
		return err
	}
	if err := tr.persistIntervalDistributionArchive(context.Background(), test.ID, record, archiveManifest); err != nil {
		// The sidecar is additive evidence; a later adjunct refresh retries it.
		// Exemplars wait for it: they read its medians and are written once.
		slog.Warn("failed to archive interval distributions", "test_id", test.ID, "error", err)
	} else if err := tr.persistExemplarArchive(context.Background(), record, archiveManifest); err != nil {
		slog.Warn("failed to archive exemplars", "test_id", test.ID, "error", err)
	}
	if err := tr.persistClockObservationArchive(context.Background(), test, record, archiveManifest); err != nil {
		slog.Warn("failed to archive clock observations", "test_id", test.ID, "error", err)
	}
	if err := tr.persistTelemetrySourceArchive(context.Background(), test, record, archiveManifest); err != nil {
		slog.Warn("failed to archive telemetry source epochs", "test_id", test.ID, "error", err)
	}
	if err := tr.persistTelemetryCoverage(context.Background(), test, record, archiveManifest); err != nil {
		slog.Warn("failed to record telemetry coverage", "test_id", test.ID, "error", err)
	}
	tr.persistTelemetryStageDistributions(test, archiveManifest, refresh)
	tr.persistTelemetryLoss(test, archiveManifest, refresh)
	if err := tr.persistJointIntervalArchive(context.Background(), test, record, archiveManifest); err != nil {
		slog.Warn("failed to archive joint evidence", "test_id", test.ID, "error", err)
	}
	if err := tr.persistPhaseEventArchive(context.Background(), test, record, archiveManifest); err != nil {
		slog.Warn("failed to archive phase events", "test_id", test.ID, "error", err)
	}
	manifest, err := tr.buildTestManifestForManagedTest(test, test.ID, record, false)
	if err != nil {
		return err
	}
	manifest.Source = "archive"
	manifest.GeneratedAt = time.Now().UTC()
	var notes []*storage.TestNote
	if tr.testNotesStore != nil {
		loaded, err := tr.testNotesStore.ListTestNotes(test.ID)
		if err != nil {
			return fmt.Errorf("load test notes: %w", err)
		}
		notes = loaded
	}
	testNotesFile, err := writeTestNotesArtifact(record.ArchivePath, test.ID, notes, budget)
	if err != nil {
		return err
	}
	if manifest.Archive != nil {
		manifest.Archive.TestManifestFile = testRunManifestFileName
		manifest.Archive.TestNotesFile = testNotesFile
	}
	if err := writeArchiveJSONArtifactDurable(record.ArchivePath, testRunManifestFileName, manifest, budget); err != nil {
		return err
	}
	archiveManifest.TestManifestFile = testRunManifestFileName
	archiveManifest.TestNotesFile = testNotesFile
	if err := updateRunArchiveSingleArtifactIntegrity(record.ArchivePath, archiveManifest, RunArchiveArtifact{
		Name: testRunManifestFileName, Kind: "manifest", Format: "json", RecordType: "test_manifest",
	}); err != nil {
		return err
	}
	if err := updateRunArchiveSingleArtifactIntegrity(record.ArchivePath, archiveManifest, RunArchiveArtifact{
		Name: testNotesFile, Kind: "notes", Format: "json", RecordType: "test_notes",
	}); err != nil {
		return err
	}
	manifestData, err := json.MarshalIndent(archiveManifest, "", "  ")
	if err != nil {
		return err
	}
	if prepare != nil {
		if err := prepare(manifestData); err != nil {
			return err
		}
	}
	if err := writeArchiveArtifactDurableWithBudget(record.ArchivePath, runArchiveManifestFileName, manifestData, 0o644, budget); err != nil {
		return err
	}
	if err := refreshRunArchiveManifestTrust(record); err != nil {
		return err
	}
	// Enrichment runs once the archive is final, for gated projects, before
	// provider retention can expire the run's evidence.
	tr.autoEnrichRCAEvidence(test, record, archiveManifest)
	return nil
}

func writeArchiveJSONArtifactDurable(rootPath string, name string, value any, budget *archiveOutputBudget) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeArchiveArtifactDurableWithBudget(rootPath, name, data, 0o644, budget)
}

// validateImmutableArchiveArtifactIntegrity prevents an adjunct refresh from
// re-blessing modified telemetry. The two adjuncts are intentionally mutable;
// every other recorded digest must still match before notes/manifest updates.
func validateImmutableArchiveArtifactIntegrity(root string, manifest *RunArchiveManifest) error {
	if manifest == nil {
		return nil
	}
	for _, artifact := range manifest.Artifacts {
		name := strings.TrimSpace(artifact.Name)
		if name == "" || name == testRunManifestFileName || name == testNotesArchiveFileName || strings.TrimSpace(artifact.SHA256) == "" {
			continue
		}
		file, _, err := openArchiveArtifactForRead(root, name)
		if err != nil {
			return fmt.Errorf("validate immutable archive artifact %q: %w", name, err)
		}
		actual, hashErr := sha256Reader(file)
		closeErr := file.Close()
		if hashErr != nil {
			return fmt.Errorf("hash immutable archive artifact %q: %w", name, hashErr)
		}
		if closeErr != nil {
			return closeErr
		}
		if !strings.EqualFold(actual, strings.TrimSpace(artifact.SHA256)) {
			return fmt.Errorf("immutable archive artifact %q failed SHA-256 validation", name)
		}
	}
	return nil
}

func loadTestRunManifest(path string) (*TestRunManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var manifest TestRunManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}
	if strings.TrimSpace(manifest.SchemaVersion) == "" {
		manifest.SchemaVersion = testRunManifestSchemaVersion
	}
	if strings.TrimSpace(manifest.Source) == "" {
		manifest.Source = "archive"
	}
	return &manifest, nil
}

func writeIndentedJSONFile(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	if dirHandle, err := os.Open(dir); err == nil {
		_ = dirHandle.Sync()
		_ = dirHandle.Close()
	}
	return nil
}

func durationString(duration time.Duration, fallback string) string {
	if strings.TrimSpace(fallback) != "" {
		return fallback
	}
	if duration > 0 {
		return duration.String()
	}
	return ""
}

func cloneIntPtr(value *int) *int {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneRunLogicGoalSnapshot(value *RunLogicGoalSnapshot) *RunLogicGoalSnapshot {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func removeWarningPrefix(warnings []string, prefix string) []string {
	if len(warnings) == 0 {
		return nil
	}
	out := warnings[:0]
	for _, warning := range warnings {
		if !strings.HasPrefix(warning, prefix) {
			out = append(out, warning)
		}
	}
	return out
}
