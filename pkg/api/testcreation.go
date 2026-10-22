package api

// The build-a-test pipeline: CreateTest and draft-capacity estimation,
// scenario-request derivation/validation, group normalization + compilation,
// shared data-source/CSV assembly, load-stage/duration parsing, name
// derivation, and the project-ID/runtime-settings/redaction resolvers.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/refkey"
	"github.com/jasonczech/apexload/pkg/secretstore"
	"github.com/jasonczech/apexload/pkg/storage"
)

func (req *CreateTestRequest) UnmarshalJSON(data []byte) error {
	type createTestRequestAlias CreateTestRequest
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	var decoded createTestRequestAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*req = CreateTestRequest(decoded)
	_, req.scriptShakeoutProvided = raw["script_shakeout"]
	_, req.resourceBaselineProvided = raw["resource_baseline"]
	return nil
}

func (tr *TestRunner) EstimateDraftCapacity(req CreateTestRequest) (*DraftCapacityEstimate, error) {
	preview, snapshot, revision, err := tr.buildManagedTestFromSnapshot(req, "draft-estimate", "DRAFT")
	if err != nil {
		return nil, err
	}

	estimate := &DraftCapacityEstimate{
		TerminationMode:       preview.TerminationMode,
		ScenarioMode:          preview.ScenarioMode,
		TotalVUsers:           preview.TotalVUsers,
		Duration:              preview.DurationStr,
		ResourceBaseline:      preview.ResourceBaseline,
		Groups:                preview.Groups,
		Assignment:            cloneGeneratorAssignmentSettings(preview.Assignment),
		Footprint:             buildScenarioFootprint(preview),
		Recommendation:        snapshot.buildAssignmentRecommendation(preview),
		CapacityAdvisory:      cloneCapacityAdvisory(preview.CapacityAdvisory),
		RuntimeSettingsReview: cloneRuntimeSettingsReview(preview.RuntimeSettingsReview),
	}
	tr.mu.RLock()
	estimate.PodPlacements, estimate.PodCapacityWarnings = tr.previewPodPlacementLocked(preview)
	tr.mu.RUnlock()
	if !tr.managedTestBuildFenceCurrent(revision) {
		return nil, fmt.Errorf("controller content changed while draft capacity was being compiled; retry")
	}
	return estimate, nil
}

func (tr *TestRunner) loadCreateTestProjectQuotaEvidence(req CreateTestRequest) projectQuotaEvidence {
	tr.mu.RLock()
	projectID := strings.TrimSpace(req.ProjectID)
	poolID := ""
	if req.Assignment != nil {
		poolID = req.Assignment.PoolID
	}
	if req.RevisionSourceTestID != "" {
		if source := tr.tests[tr.resolveRefKeyLocked(req.RevisionSourceTestID)]; source != nil {
			projectID = source.ProjectID
			if req.Assignment == nil && source.Assignment != nil {
				poolID = source.Assignment.PoolID
			}
		}
	} else if req.ScenarioID != "" {
		if scenario := tr.scenarios[tr.resolveRefKeyLocked(req.ScenarioID)]; scenario != nil {
			projectID = scenario.ProjectID
			if req.Assignment == nil && scenario.Assignment != nil {
				poolID = scenario.Assignment.PoolID
			}
		}
	}
	if projectID == "" && req.ScriptID != "" {
		if script := tr.scripts[tr.resolveRefKeyLocked(req.ScriptID)]; script != nil {
			projectID = script.ProjectID
		}
	}
	if projectID == "" {
		for _, group := range req.Groups {
			if script := tr.scripts[tr.resolveRefKeyLocked(group.ScriptID)]; script != nil {
				projectID = script.ProjectID
				break
			}
		}
	}
	projectStore := tr.projectStore
	tr.mu.RUnlock()
	return loadProjectQuotaEvidence(projectStore, projectID, poolID)
}

type createTestReleaseEvidence struct {
	requestedApplicationID     string
	requestedReleaseID         string
	storeConfigured            bool
	applicationStoreConfigured bool
	loaded                     bool
	releaseResolved            bool
	releaseID                  string
	releaseApplicationID       string
	releaseProjectID           string
	releaseArchived            bool
	releaseUpdatedAt           time.Time
	releaseErr                 error
	applicationResolved        bool
	applicationID              string
	applicationProjectID       string
	applicationUpdatedAt       time.Time
	applicationErr             error
	// The project's own legacy application and release, when the store has
	// them: a run that names no release is filed there (see
	// resolveCreateTestReleaseContextWithEvidence).
	legacyProjectID     string
	legacyApplicationID string
	legacyReleaseID     string
}

type applicationPersistence interface {
	GetApplication(id string) (*storage.Application, error)
}

// loadCreateTestReleaseEvidence captures the effective release/application
// identity inherited from a queued revision while tr.mu is held, then performs
// every durable lookup after releasing it. The scalar result is immutable and
// is identity-checked again by buildManagedTestLocked.
func (tr *TestRunner) loadCreateTestReleaseEvidence(req CreateTestRequest) createTestReleaseEvidence {
	tr.mu.RLock()
	applicationID := strings.TrimSpace(req.ApplicationID)
	releaseID := strings.TrimSpace(req.ReleaseID)
	if sourceID := strings.TrimSpace(req.RevisionSourceTestID); sourceID != "" {
		if source := tr.tests[tr.resolveRefKeyLocked(sourceID)]; source != nil {
			if isLegacyTestReleaseContext(source.ApplicationID, source.ReleaseID) {
				if matchesImplicitRevisionReleaseContext(applicationID, releaseID, source.ApplicationID, source.ReleaseID) {
					applicationID = ""
					releaseID = ""
				}
			} else {
				if applicationID == "" {
					applicationID = strings.TrimSpace(source.ApplicationID)
				}
				if releaseID == "" {
					releaseID = strings.TrimSpace(source.ReleaseID)
				}
			}
		}
	}
	store := tr.releaseStore
	tr.mu.RUnlock()
	return loadCreateTestReleaseEvidence(store, applicationID, releaseID)
}

// withProjectLegacyReleaseEvidence adds the project's own legacy release to
// the evidence of a runnable test that names no release: only such a test is
// filed there, so drafts and previews read nothing more.
func (tr *TestRunner) withProjectLegacyReleaseEvidence(req CreateTestRequest, status string) createTestReleaseEvidence {
	evidence := req.releaseContextEvidence
	if status == "DRAFT" || evidence.requestedApplicationID != "" || evidence.requestedReleaseID != "" || evidence.legacyProjectID != "" {
		return evidence
	}
	tr.mu.RLock()
	store := tr.releaseStore
	tr.mu.RUnlock()
	if store == nil {
		return evidence
	}
	// The project quota evidence has already inferred the run's project
	// (request, revision source, scenario or scripts).
	projectID := normalizeProjectID(req.ProjectID)
	if req.projectQuotaEvidence.loaded {
		projectID = normalizeProjectID(req.projectQuotaEvidence.projectID)
	}
	loadProjectLegacyReleaseEvidence(store, projectID, &evidence)
	return evidence
}

// loadProjectLegacyReleaseEvidence records the project's own legacy release
// and application when the store has them. Every project the API creates has
// them; a store that predates or bypasses that keeps the bare IDs.
func loadProjectLegacyReleaseEvidence(store releasePersistence, projectID string, evidence *createTestReleaseEvidence) {
	release, err := store.GetRelease(storage.LegacyReleaseIDForProject(projectID))
	if err != nil || release == nil || normalizeProjectID(release.ProjectID) != projectID || strings.TrimSpace(release.ApplicationID) == "" {
		return
	}
	evidence.legacyProjectID = projectID
	evidence.legacyApplicationID = strings.TrimSpace(release.ApplicationID)
	evidence.legacyReleaseID = strings.TrimSpace(release.ID)
}

func loadCreateTestReleaseEvidence(store releasePersistence, applicationID, releaseID string) createTestReleaseEvidence {
	applicationID = strings.TrimSpace(applicationID)
	releaseID = strings.TrimSpace(releaseID)
	evidence := createTestReleaseEvidence{
		requestedApplicationID: applicationID,
		requestedReleaseID:     releaseID,
		storeConfigured:        store != nil,
		loaded:                 true,
	}
	if store == nil {
		return evidence
	}

	effectiveApplicationID := applicationID
	if releaseID != "" {
		release, err := store.GetRelease(releaseID)
		if err != nil {
			evidence.releaseErr = err
			return evidence
		}
		if release == nil {
			evidence.releaseErr = errors.New("release not found")
			return evidence
		}
		evidence.releaseResolved = true
		evidence.releaseID = strings.TrimSpace(release.ID)
		evidence.releaseApplicationID = strings.TrimSpace(release.ApplicationID)
		evidence.releaseProjectID = strings.TrimSpace(release.ProjectID)
		evidence.releaseArchived = release.ArchivedAt != nil
		evidence.releaseUpdatedAt = release.UpdatedAt
		if applicationID != "" && applicationID != evidence.releaseApplicationID {
			return evidence
		}
		effectiveApplicationID = evidence.releaseApplicationID
	}

	applicationStore, ok := store.(applicationPersistence)
	evidence.applicationStoreConfigured = ok
	if !ok || effectiveApplicationID == "" {
		return evidence
	}
	application, err := applicationStore.GetApplication(effectiveApplicationID)
	if err != nil {
		evidence.applicationErr = err
		return evidence
	}
	if application == nil {
		evidence.applicationErr = errors.New("application not found")
		return evidence
	}
	evidence.applicationResolved = true
	evidence.applicationID = strings.TrimSpace(application.ID)
	evidence.applicationProjectID = strings.TrimSpace(application.ProjectID)
	evidence.applicationUpdatedAt = application.UpdatedAt
	return evidence
}

// cloneManagedTestCapacitySnapshotLocked retains only the fields used by
// capacity/admission projections. In particular it must not retain live metrics,
// history, or executable group scripts: a single terminal run can own tens of
// MiB of history, and cloning that payload while servicing a polled preflight
// would turn the live runner read lock into a large serialization boundary.
func cloneManagedTestCapacitySnapshotLocked(test *ManagedTest) *ManagedTest {
	if test == nil {
		return nil
	}
	groups := make([]TestGroup, len(test.Groups))
	for index, group := range test.Groups {
		javaCapacityPlan := javaVuserCapacityPlanForGroup(group)
		resourcePlan, resourceErr := runtimeResourcePlanForGroup(group)
		resourceError := ""
		if resourceErr != nil {
			resourceError = "eventhubs processor resource plan invalid"
		}
		groups[index] = TestGroup{
			Name:                            group.Name,
			ScriptID:                        group.ScriptID,
			ScriptName:                      group.ScriptName,
			Protocol:                        group.Protocol,
			Role:                            group.Role,
			VUsers:                          group.VUsers,
			TargetDuration:                  group.TargetDuration,
			EffectiveEndAt:                  cloneTimePtr(group.EffectiveEndAt),
			RuntimeState:                    group.RuntimeState,
			EndedAt:                         cloneTimePtr(group.EndedAt),
			RampUp:                          group.RampUp,
			RampDown:                        group.RampDown,
			StartOffset:                     group.StartOffset,
			Stages:                          cloneLoadStages(group.Stages),
			Initialization:                  group.Initialization,
			Assignment:                      cloneGeneratorAssignmentSettings(group.Assignment),
			TransactionResponseTimeSLAMs:    cloneFloat64Ptr(group.TransactionResponseTimeSLAMs),
			rampUpDur:                       group.rampUpDur,
			stageDriven:                     group.stageDriven,
			initialVUsers:                   group.initialVUsers,
			currentVUsers:                   group.currentVUsers,
			IterationOverrideKnown:          group.IterationOverrideKnown,
			Iterations:                      cloneIntPtr(group.Iterations),
			IterationOverride:               cloneIntPtr(group.IterationOverride),
			javaVuserCapacityPlan:           javaCapacityPlan,
			javaVuserCapacityPlanKnown:      true,
			runtimeResourcePlan:             resourcePlan,
			runtimeResourcePlanKnown:        true,
			runtimeResourcePlanError:        resourceError,
			messagingRuntimeFeatures:        append([]string(nil), requiredMessagingFeaturesForGroup(group)...),
			requiredRuntimeFeature:          groupRequiredRuntimeFeature(group),
			requiredRuntimeFeatureKnown:     true,
			BrowserDocumentCaptureRequested: groupRequestsBrowserDocumentCapture(group),
		}
	}
	return &ManagedTest{
		ID:                           test.ID,
		RefKey:                       test.RefKey,
		MaterializationKey:           test.MaterializationKey,
		Name:                         test.Name,
		Status:                       test.Status,
		TerminationMode:              test.TerminationMode,
		shakeoutAdmissionReservation: test.shakeoutAdmissionReservation,
		ScenarioID:                   test.ScenarioID,
		ProjectID:                    test.ProjectID,
		Groups:                       groups,
		CapacityAdvisory:             cloneCapacityAdvisory(test.CapacityAdvisory),
		Assignment:                   cloneGeneratorAssignmentSettings(test.Assignment),
		ResourceBaseline:             test.ResourceBaseline,
		StartDelay:                   test.StartDelay,
		GoalConfig:                   cloneGoalConfig(test.GoalConfig),
		TrafficProfile:               cloneTrafficProfile(test.TrafficProfile),
		ELKCapture:                   cloneELKCaptureConfig(test.ELKCapture),
		DynatraceCapture:             cloneDynatraceCaptureConfig(test.DynatraceCapture),
		MonitorBindings:              cloneMonitorBindings(test.MonitorBindings),
		TraceContext:                 cloneRunTraceContext(test.TraceContext),
		JointEvidence:                test.JointEvidence.Clone(),
		TotalVUsers:                  test.TotalVUsers,
		Duration:                     test.Duration,
		DurationStr:                  test.DurationStr,
		CreatedAt:                    test.CreatedAt,
		ScheduledStartAt:             cloneTimePtr(test.ScheduledStartAt),
		TimeslotID:                   cloneStringPtr(test.TimeslotID),
		PendingSince:                 cloneTimePtr(test.PendingSince),
		StartedAt:                    cloneTimePtr(test.StartedAt),
		PausedAt:                     cloneTimePtr(test.PausedAt),
		CumulativePause:              test.CumulativePause,
		CapacityWindowStartAt:        cloneTimePtr(test.CapacityWindowStartAt),
		CapacityWindowEndAt:          cloneTimePtr(test.CapacityWindowEndAt),
		EndedAt:                      cloneTimePtr(test.EndedAt),
		StartAuthorization:           cloneQueuedStartAuthorization(test.StartAuthorization),
		ExecutionPlanApproval:        cloneRunArchiveExecutionPlanApprovalSnapshot(test.ExecutionPlanApproval),
		ExecutionPlan:                cloneTestExecutionPlanLink(test.ExecutionPlan),
		ScenarioMode:                 test.ScenarioMode,
	}
}

type managedTestBuildFence struct {
	contentRevision      uint64
	settingsFingerprint  [sha256.Size]byte
	maxTests             int
	identityTestIDs      []string
	admissionFingerprint [sha256.Size]byte
}

func managedTestBuildSettingsFingerprint(settings SystemSettings) [sha256.Size]byte {
	encoded, _ := json.Marshal(settings) // SystemSettings contains only JSON-supported configuration fields.
	return sha256.Sum256(encoded)
}

type managedTestBuildTimeFence struct {
	Set      bool  `json:"set"`
	UnixNano int64 `json:"unix_nano"`
}

type managedTestBuildStageFence struct {
	Type         string `json:"type"`
	Duration     string `json:"duration"`
	TargetVUsers int    `json:"target_vusers"`
}

type managedTestBuildGroupFence struct {
	Protocol               string                       `json:"protocol"`
	VUsers                 int                          `json:"vusers"`
	RequiredRuntimeFeature string                       `json:"required_runtime_feature"`
	RuntimeOccupancyMode   string                       `json:"runtime_occupancy_mode"`
	RuntimeDemand          int64                        `json:"runtime_demand"`
	RampDown               string                       `json:"ramp_down"`
	Assignment             *GeneratorAssignmentSettings `json:"assignment,omitempty"`
	Stages                 []managedTestBuildStageFence `json:"stages,omitempty"`
}

type managedTestBuildTestFence struct {
	ID                    string                       `json:"id"`
	RefKey                string                       `json:"ref_key"`
	MaterializationKey    string                       `json:"materialization_key"`
	Status                string                       `json:"status"`
	TerminationMode       string                       `json:"termination_mode"`
	ProjectID             string                       `json:"project_id"`
	TotalVUsers           int                          `json:"total_vusers"`
	RecommendedGenerators int                          `json:"recommended_generators"`
	Assignment            *GeneratorAssignmentSettings `json:"assignment,omitempty"`
	ResourceBaseline      bool                         `json:"resource_baseline"`
	StartDelay            string                       `json:"start_delay"`
	Duration              int64                        `json:"duration"`
	CreatedAt             managedTestBuildTimeFence    `json:"created_at"`
	ScheduledStartAt      managedTestBuildTimeFence    `json:"scheduled_start_at"`
	PendingSince          managedTestBuildTimeFence    `json:"pending_since"`
	StartedAt             managedTestBuildTimeFence    `json:"started_at"`
	PausedAt              managedTestBuildTimeFence    `json:"paused_at"`
	EndedAt               managedTestBuildTimeFence    `json:"ended_at"`
	CumulativePause       int64                        `json:"cumulative_pause"`
	CapacityWindowStartAt managedTestBuildTimeFence    `json:"capacity_window_start_at"`
	CapacityWindowEndAt   managedTestBuildTimeFence    `json:"capacity_window_end_at"`
	TimeslotID            string                       `json:"timeslot_id"`
	GoalMaxVUsers         int                          `json:"goal_max_vusers"`
	ApprovalID            string                       `json:"approval_id"`
	ApprovalPlanID        string                       `json:"approval_plan_id"`
	ApprovalContentHash   string                       `json:"approval_content_hash"`
	ApprovalStatus        string                       `json:"approval_status"`
	ApprovalPolicy        string                       `json:"approval_policy"`
	ApprovedPeakVUsers    int                          `json:"approved_peak_vusers"`
	ShakeoutPassedAt      managedTestBuildTimeFence    `json:"shakeout_passed_at"`
	ShakeoutPassSHA256    string                       `json:"shakeout_pass_sha256"`
	Groups                []managedTestBuildGroupFence `json:"groups,omitempty"`
}

type managedTestBuildInventoryFence struct {
	JavaHost           *JavaHostCapabilitySummary             `json:"java_host,omitempty"`
	Target             string                                 `json:"target"`
	GeneratorID        string                                 `json:"generator_id"`
	Region             string                                 `json:"region"`
	FleetPoolID        string                                 `json:"fleet_pool_id"`
	Cordoned           bool                                   `json:"cordoned"`
	Status             string                                 `json:"status"`
	SupportedProtocols []string                               `json:"supported_protocols,omitempty"`
	Labels             map[string]string                      `json:"labels,omitempty"`
	CompatibleFeatures []string                               `json:"compatible_features,omitempty"`
	RuntimeCapacities  []generatorrpc.ProtocolRuntimeCapacity `json:"runtime_capacities,omitempty"`
	DrainState         string                                 `json:"drain_state"`
	CapacityState      string                                 `json:"capacity_state"`
	AvailableCapacity  int32                                  `json:"available_capacity"`
	MaxVUsers          int32                                  `json:"max_vusers"`
}

func managedTestBuildTimeFingerprint(value *time.Time) managedTestBuildTimeFence {
	if value == nil {
		return managedTestBuildTimeFence{}
	}
	return managedTestBuildTimeFence{Set: true, UnixNano: value.UnixNano()}
}

func managedTestBuildTestFingerprint(test *ManagedTest) managedTestBuildTestFence {
	if test == nil {
		return managedTestBuildTestFence{}
	}
	row := managedTestBuildTestFence{
		ID:                    test.ID,
		RefKey:                test.RefKey,
		MaterializationKey:    test.MaterializationKey,
		Status:                test.Status,
		TerminationMode:       dsl.CanonicalTerminationMode(test.TerminationMode),
		ProjectID:             normalizeProjectID(test.ProjectID),
		TotalVUsers:           test.TotalVUsers,
		Assignment:            cloneGeneratorAssignmentSettings(test.Assignment),
		ResourceBaseline:      test.ResourceBaseline,
		StartDelay:            test.StartDelay,
		Duration:              int64(test.Duration),
		CreatedAt:             managedTestBuildTimeFingerprint(&test.CreatedAt),
		ScheduledStartAt:      managedTestBuildTimeFingerprint(test.ScheduledStartAt),
		PendingSince:          managedTestBuildTimeFingerprint(test.PendingSince),
		StartedAt:             managedTestBuildTimeFingerprint(test.StartedAt),
		PausedAt:              managedTestBuildTimeFingerprint(test.PausedAt),
		EndedAt:               managedTestBuildTimeFingerprint(test.EndedAt),
		CumulativePause:       int64(test.CumulativePause),
		CapacityWindowStartAt: managedTestBuildTimeFingerprint(test.CapacityWindowStartAt),
		CapacityWindowEndAt:   managedTestBuildTimeFingerprint(test.CapacityWindowEndAt),
		TimeslotID:            stringPtrValue(test.TimeslotID),
	}
	if test.CapacityAdvisory != nil {
		row.RecommendedGenerators = test.CapacityAdvisory.RecommendedGenerators
	}
	if test.GoalConfig != nil && test.GoalConfig.MaxVUsers != nil {
		row.GoalMaxVUsers = *test.GoalConfig.MaxVUsers
	}
	if approval := test.ExecutionPlanApproval; approval != nil {
		row.ApprovalID = approval.ID
		row.ApprovalPlanID = approval.PlanID
		row.ApprovalContentHash = approval.PlanContentHash
		row.ApprovalStatus = approval.Status
		row.ApprovalPolicy = approval.Policy
		row.ApprovedPeakVUsers = approval.ApprovedPeakVUsers
	}
	if authorization := test.StartAuthorization; authorization != nil && authorization.ScriptShakeoutPass != nil {
		row.ShakeoutPassedAt = managedTestBuildTimeFingerprint(&authorization.ScriptShakeoutPass.PassedAt)
		row.ShakeoutPassSHA256 = authorization.ScriptShakeoutPass.EvidenceSHA256
	}
	if len(test.Groups) > 0 {
		row.Groups = make([]managedTestBuildGroupFence, len(test.Groups))
		for index, group := range test.Groups {
			plan := javaVuserCapacityPlanForGroup(group)
			row.Groups[index].Protocol = group.Protocol
			row.Groups[index].VUsers = group.VUsers
			row.Groups[index].RequiredRuntimeFeature = groupRequiredRuntimeFeature(group)
			row.Groups[index].RuntimeOccupancyMode = javaVuserOccupancyModeForGroup(group)
			row.Groups[index].RuntimeDemand = plan.Demand(group.VUsers)
			row.Groups[index].RampDown = group.RampDown
			row.Groups[index].Assignment = cloneGeneratorAssignmentSettings(group.Assignment)
			if len(group.Stages) > 0 {
				row.Groups[index].Stages = make([]managedTestBuildStageFence, len(group.Stages))
				for stageIndex, stage := range group.Stages {
					row.Groups[index].Stages[stageIndex] = managedTestBuildStageFence{
						Type: stage.Type, Duration: stage.Duration, TargetVUsers: stage.TargetVUsers,
					}
				}
			}
		}
	}
	return row
}

// managedTestBuildAdmissionRelevant reports whether a run can affect a fresh
// create/preflight admission decision. Terminal history is deliberately
// excluded: retained completed runs can number in the millions, while none of
// the quota, reservation, launch-window, or pending projections consume them.
func managedTestBuildAdmissionRelevant(test *ManagedTest) bool {
	if test == nil {
		return false
	}
	switch strings.ToUpper(strings.TrimSpace(test.Status)) {
	case "QUEUED", "PENDING", "RESERVED", "SCHEDULED", "SHAKEOUT_REVIEW",
		"INITIALIZING", "STARTING", "SHAKEOUT", "RUNNING", "PAUSED", "STOPPING":
		return true
	default:
		return false
	}
}

func managedTestBuildAdmissionFingerprint(tests map[string]*ManagedTest, identityTestIDs []string, inventory []GeneratorInventoryEntry, inventoryAt time.Time, deletingProjects map[string]struct{}) [sha256.Size]byte {
	selected := make(map[string]struct{}, len(identityTestIDs))
	for _, id := range identityTestIDs {
		if id = strings.TrimSpace(id); id != "" {
			selected[id] = struct{}{}
		}
	}
	ids := make([]string, 0)
	for id, test := range tests {
		if _, identity := selected[id]; identity || managedTestBuildAdmissionRelevant(test) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	testRows := make([]managedTestBuildTestFence, 0, len(ids))
	for _, id := range ids {
		testRows = append(testRows, managedTestBuildTestFingerprint(tests[id]))
	}
	inventoryRows := make([]managedTestBuildInventoryFence, len(inventory))
	for index, entry := range inventory {
		inventoryRows[index] = managedTestBuildInventoryFence{
			Target:             entry.Target,
			GeneratorID:        entry.GeneratorID,
			Region:             entry.Region,
			FleetPoolID:        entry.FleetPoolID,
			Cordoned:           entry.Cordoned,
			Status:             entry.Status,
			SupportedProtocols: append([]string(nil), entry.SupportedProtocols...),
			Labels:             cloneStringMap(entry.Labels),
			CompatibleFeatures: append([]string(nil), entry.CompatibleFeatures...),
			RuntimeCapacities:  cloneProtocolRuntimeCapacities(entry.RuntimeCapacities),
			JavaHost:           cloneJavaHostCapabilitySummary(entry.JavaHost),
			DrainState:         entry.DrainState,
			CapacityState:      entry.CapacityState,
			AvailableCapacity:  entry.AvailableCapacity,
			MaxVUsers:          entry.MaxVUsers,
		}
	}
	sort.Slice(inventoryRows, func(i, j int) bool {
		if inventoryRows[i].Target != inventoryRows[j].Target {
			return inventoryRows[i].Target < inventoryRows[j].Target
		}
		return inventoryRows[i].GeneratorID < inventoryRows[j].GeneratorID
	})
	deleting := make([]string, 0, len(deletingProjects))
	for projectID := range deletingProjects {
		deleting = append(deleting, normalizeProjectID(projectID))
	}
	sort.Strings(deleting)
	payload := struct {
		Tests       []managedTestBuildTestFence      `json:"tests"`
		Inventory   []managedTestBuildInventoryFence `json:"inventory"`
		InventoryAt int64                            `json:"inventory_at"`
		Deleting    []string                         `json:"deleting_projects"`
	}{Tests: testRows, Inventory: inventoryRows, InventoryAt: inventoryAt.UnixNano(), Deleting: deleting}
	encoded, _ := json.Marshal(payload) // payload contains only JSON-supported scalar/map/slice fields.
	return sha256.Sum256(encoded)
}

func (tr *TestRunner) managedTestBuildFenceCurrentLocked(fence managedTestBuildFence) bool {
	if tr == nil || tr.contentRevision != fence.contentRevision || managedTestBuildSettingsFingerprint(tr.settings) != fence.settingsFingerprint || tr.maxTests != fence.maxTests {
		return false
	}
	tr.liveInventoryMu.RLock()
	inventory := cloneGeneratorInventoryEntries(tr.liveInventory)
	inventoryAt := tr.liveInventoryAt
	tr.liveInventoryMu.RUnlock()
	return managedTestBuildAdmissionFingerprint(tr.tests, fence.identityTestIDs, inventory, inventoryAt, tr.deletingProjects) == fence.admissionFingerprint
}

func (tr *TestRunner) managedTestBuildFenceCurrent(fence managedTestBuildFence) bool {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	return tr.managedTestBuildFenceCurrentLocked(fence)
}

// managedTestBuildSnapshotLocked captures only the controller state reachable
// from one build request. Capacity-relevant and pending tests contribute a
// lightweight row; terminal history is omitted, and only the selected revision
// source is fully cloned. Likewise only the selected scenario and scripts
// referenced by the request/source/scenario are retained. Callers hold tr.mu
// (read or write).
func (tr *TestRunner) managedTestBuildSnapshotLocked(req CreateTestRequest) (*TestRunner, CreateTestRequest, managedTestBuildFence) {
	snapshot := &TestRunner{
		tests:                   make(map[string]*ManagedTest),
		scripts:                 make(map[string]*StoredScript),
		scenarios:               make(map[string]*StoredScenario, 1),
		archives:                make(map[string]*storage.RunArchiveRecord),
		deletingProjects:        make(map[string]struct{}, len(tr.deletingProjects)),
		hub:                     nil,
		maxTests:                tr.maxTests,
		settings:                cloneSystemSettings(tr.settings),
		testStore:               tr.testStore,
		scriptStore:             tr.scriptStore,
		dataPoolStore:           tr.dataPoolStore,
		projectStore:            tr.projectStore,
		releaseStore:            tr.releaseStore,
		settingsStore:           tr.settingsStore,
		projectSecretStore:      tr.projectSecretStore,
		elkStore:                tr.elkStore,
		dynatraceStore:          tr.dynatraceStore,
		objectRepoStore:         tr.objectRepoStore,
		fleetStore:              tr.fleetStore,
		leadership:              tr.leadership,
		durableExecutionFence:   tr.durableExecutionFence,
		liveState:               tr.liveState,
		execution:               tr.execution,
		notifier:                nil,
		routeCache:              make(map[string]latencyRouteCacheEntry),
		envCode:                 tr.envCode,
		scriptRefSeqAllocator:   tr.scriptRefSeqAllocator,
		scenarioRefSeqAllocator: tr.scenarioRefSeqAllocator,
		runRefSeqAllocator:      tr.runRefSeqAllocator,
		refKeyIndex:             make(map[string]string),
		capacityAdvisoryCache:   make(map[string]cachedCapacityAdvisory),
		capacityAdvisoryGen:     tr.capacityAdvisoryGen,
		remoteControlGates:      make(map[string]*distributedControlGate),
		seqPlanRuns:             make(map[string]*seqPlanRunState),
		seqPlanByTest:           make(map[string]string),
		seqPlanRetrying:         make(map[string]struct{}),
		terminalRetrying:        make(map[string]struct{}),
		archiveCommitGates:      make(map[string]*archiveCommitGate),
		stopTeardownRetries:     make(map[string]stopTeardownRetryIntent),
		privateCISnapshots:      make(map[string]int),
		snapshotWrites:          make(map[string]*runSnapshotWriteState),
	}
	for id, test := range tr.tests {
		if managedTestBuildAdmissionRelevant(test) {
			snapshot.tests[id] = cloneManagedTestCapacitySnapshotLocked(test)
		}
	}
	for id := range tr.deletingProjects {
		snapshot.deletingProjects[id] = struct{}{}
	}

	prepared := req
	prepared.Groups = append([]CreateGroupEntry(nil), req.Groups...)
	resolve := func(raw string) string {
		raw = strings.TrimSpace(raw)
		resolved := tr.resolveRefKeyLocked(raw)
		if key := normalizeRefKeyCandidate(raw); key != "" && resolved != raw {
			snapshot.refKeyIndex[key] = resolved
		}
		return resolved
	}
	indexRefKey := func(key, id string) {
		if normalized := normalizeRefKeyCandidate(key); normalized != "" {
			snapshot.refKeyIndex[normalized] = id
		}
	}

	revisionSourceID := resolve(prepared.RevisionSourceTestID)
	prepared.RevisionSourceTestID = revisionSourceID
	var revisionSource *ManagedTest
	if revisionSourceID != "" {
		if source := tr.tests[revisionSourceID]; source != nil {
			revisionSource = cloneManagedTestFieldsLocked(source)
			// Revision compilation needs the immutable group/runtime snapshots but
			// never consumes metric history. Drop every metric projection copied by
			// the general read clone so selecting a long-running queued source cannot
			// retain its telemetry payload under the live runner read lock.
			revisionSource.History = nil
			revisionSource.LiveMetrics = nil
			revisionSource.Sparkline = nil
			snapshot.tests[revisionSourceID] = revisionSource
			indexRefKey(source.RefKey, revisionSourceID)
		}
	}
	requestedTestID := strings.TrimSpace(prepared.RequestedTestID)
	prepared.RequestedTestID = requestedTestID
	if requestedTestID != "" && snapshot.tests[requestedTestID] == nil {
		if existing := tr.tests[requestedTestID]; existing != nil {
			snapshot.tests[requestedTestID] = cloneManagedTestCapacitySnapshotLocked(existing)
		}
	}

	selectedScenarioID := resolve(prepared.ScenarioID)
	prepared.ScenarioID = selectedScenarioID
	if selectedScenarioID == "" && revisionSource != nil {
		selectedScenarioID = strings.TrimSpace(revisionSource.ScenarioID)
	}
	var selectedScenario *StoredScenario
	if selectedScenarioID != "" {
		if scenario := tr.scenarios[selectedScenarioID]; scenario != nil {
			selectedScenario = cloneStoredScenarioForReadLocked(scenario)
			snapshot.scenarios[selectedScenarioID] = selectedScenario
			indexRefKey(scenario.RefKey, selectedScenarioID)
		}
	}

	prepared.ScriptID = resolve(prepared.ScriptID)
	for index := range prepared.Groups {
		prepared.Groups[index].ScriptID = resolve(prepared.Groups[index].ScriptID)
	}
	scriptIDs := make(map[string]struct{})
	addScriptID := func(raw string) {
		if id := resolve(raw); id != "" {
			scriptIDs[id] = struct{}{}
		}
	}
	addScriptID(prepared.ScriptID)
	for _, group := range prepared.Groups {
		addScriptID(group.ScriptID)
	}
	if selectedScenario != nil {
		for _, group := range selectedScenario.Scripts {
			addScriptID(group.ScriptID)
		}
	}
	if revisionSource != nil {
		for _, group := range revisionSource.Groups {
			addScriptID(group.ScriptID)
		}
	}
	for scriptID := range scriptIDs {
		if script := tr.scripts[scriptID]; script != nil {
			snapshot.scripts[scriptID] = cloneStoredScriptForReadLocked(script)
			indexRefKey(script.RefKey, scriptID)
		}
	}

	tr.liveInventoryMu.RLock()
	snapshot.liveInventory = cloneGeneratorInventoryEntries(tr.liveInventory)
	snapshot.liveInventoryAt = tr.liveInventoryAt
	tr.liveInventoryMu.RUnlock()
	identityTestIDs := make([]string, 0, 2)
	if revisionSourceID != "" {
		identityTestIDs = append(identityTestIDs, revisionSourceID)
	}
	if requestedTestID != "" && requestedTestID != revisionSourceID {
		identityTestIDs = append(identityTestIDs, requestedTestID)
	}
	fence := managedTestBuildFence{
		contentRevision:      tr.contentRevision,
		settingsFingerprint:  managedTestBuildSettingsFingerprint(snapshot.settings),
		maxTests:             tr.maxTests,
		identityTestIDs:      identityTestIDs,
		admissionFingerprint: managedTestBuildAdmissionFingerprint(snapshot.tests, identityTestIDs, snapshot.liveInventory, snapshot.liveInventoryAt, snapshot.deletingProjects),
	}
	return snapshot, prepared, fence
}

// buildManagedTestFromSnapshot keeps every storage-backed resolver reachable
// from buildManagedTestLocked off the live runner mutex, then fences the result
// with the captured content revision before a caller consumes or installs it.
func (tr *TestRunner) buildManagedTestFromSnapshot(req CreateTestRequest, id, status string) (*ManagedTest, *TestRunner, managedTestBuildFence, error) {
	tr.mu.RLock()
	snapshot, req, revision := tr.managedTestBuildSnapshotLocked(req)
	tr.mu.RUnlock()
	// Read the durable publication fence after capturing the content snapshot.
	// A direct-import claim precedes every asset write, so a concurrent partial
	// batch cannot be compiled into a runnable test through either explicit
	// groups or a just-created scenario.
	if err := tr.validateManagedTestImportPublication(snapshot, req); err != nil {
		return nil, snapshot, revision, err
	}

	if !req.projectQuotaEvidence.loaded {
		req.projectQuotaEvidence = snapshot.loadCreateTestProjectQuotaEvidence(req)
	}
	if !req.releaseContextEvidence.loaded {
		req.releaseContextEvidence = snapshot.loadCreateTestReleaseEvidence(req)
	}
	req.releaseContextEvidence = snapshot.withProjectLegacyReleaseEvidence(req, status)
	snapshot.mu.Lock()
	test, err := snapshot.buildManagedTestLocked(req, id, status)
	snapshot.mu.Unlock()
	return test, snapshot, revision, err
}

func (tr *TestRunner) CreateTest(req CreateTestRequest) (*ManagedTest, error) {
	return tr.createTest(req, true)
}

// createTest can defer the externally visible creation event until a containing
// execution-plan transition is durable. Ordinary API creation broadcasts
// immediately; plan materialization passes false and publishes only after its
// run entry (including the generated test ID) commits.
func (tr *TestRunner) createTest(req CreateTestRequest, broadcast bool) (*ManagedTest, error) {
	leaseTerm, err := tr.captureExecutionLeaseTerm()
	if err != nil {
		return nil, err
	}
	id := strings.TrimSpace(req.RequestedTestID)
	if id == "" {
		id = uuid.New().String()
	} else if parsed, parseErr := uuid.Parse(id); parseErr != nil || parsed.String() != strings.ToLower(id) {
		return nil, fmt.Errorf("requested_test_id must be a canonical UUID")
	}
	req.RequestedTestID = ""
	return tr.createTestWithIDAndTerm(req, broadcast, id, leaseTerm)
}

const managedTestCreateBuildAttempts = 5

// errTestIDOwnedByAnotherProject refuses a requested ID that a deleted test of
// another project used. Authorization, archives and live streams address a run
// by its ID, so an ID never moves between projects.
var errTestIDOwnedByAnotherProject = errors.New("requested_test_id belonged to a deleted test in another project")

type deletedTestIdentityStore interface {
	DeletedTestProject(id string) (string, bool, error)
}

func (tr *TestRunner) ensureTestIDStaysInProject(id, projectID string) error {
	store, ok := tr.testStore.(deletedTestIdentityStore)
	if !ok {
		return nil
	}
	var owner string
	var deleted bool
	err := tr.trackDurableWrite(func() error {
		var lookupErr error
		owner, deleted, lookupErr = store.DeletedTestProject(id)
		return lookupErr
	})
	if err != nil {
		return fmt.Errorf("check deleted test identity: %w", err)
	}
	if deleted && normalizeProjectID(owner) != normalizeProjectID(projectID) {
		return errTestIDOwnedByAnotherProject
	}
	return nil
}

var errManagedTestBuildFenceStale = errors.New("managed test build fence is stale")

// managedTestRunReferenceReservation retains the one externally visible
// identity allocation that may happen between the two admission fences. A
// stale second fence rebuilds the candidate with this exact reference rather
// than consuming a new durable sequence for every transparent retry.
type managedTestRunReferenceReservation struct {
	refKey    string
	createdAt time.Time
}

// createTestWithIDAndTerm is the execution-plan materialization boundary. The
// caller preassigns and durably correlates id before entering this method, and
// the exact leadership term is retained throughout every transparent rebuild so
// an old goroutine cannot adopt a newer term after same-process reacquisition.
func (tr *TestRunner) createTestWithIDAndTerm(req CreateTestRequest, broadcast bool, id string, leaseTerm executionLeaseTerm) (*ManagedTest, error) {
	// Followers read the run's creation time back from PostgreSQL, so the
	// leader's copy takes the precision PostgreSQL stores.
	requestedAt := atMicrosecond(time.Now())
	runReference := managedTestRunReferenceReservation{createdAt: requestedAt}
	var lastErr error
	for attempt := 0; attempt < managedTestCreateBuildAttempts; attempt++ {
		created, err := tr.createTestWithIDAndTermAttempt(req, broadcast, id, leaseTerm, requestedAt, &runReference)
		if !errors.Is(err, errManagedTestBuildFenceStale) {
			return created, err
		}
		lastErr = err
		// A rebuild is safe only while the exact term captured by the caller still
		// owns execution. The attempt also rechecks this term at each mutation
		// boundary; checking here prevents needless compilation after lease loss.
		if !tr.executionLeaseTermCurrent(leaseTerm) {
			return nil, errNotExecutionLeader
		}
		if attempt+1 < managedTestCreateBuildAttempts {
			time.Sleep(time.Duration(attempt+1) * time.Millisecond)
		}
	}
	return nil, fmt.Errorf("controller admission state kept changing across %d test-build attempts: %w", managedTestCreateBuildAttempts, lastErr)
}

// createTestWithIDAndTermAttempt performs no provider or test-store mutation
// before a stale build can be retried. It may retain one reserved run reference
// across a stale second fence, because that durable sequence has already been
// consumed and must identify every rebuild of the same ID/materialization under
// the same lease term. Once runtime-secret provisioning or test persistence
// begins, ordinary commit/adoption semantics are authoritative and the attempt
// is never replayed merely because in-memory admission changed.
func (tr *TestRunner) createTestWithIDAndTermAttempt(req CreateTestRequest, broadcast bool, id string, leaseTerm executionLeaseTerm, requestedAt time.Time, runReference *managedTestRunReferenceReservation) (*ManagedTest, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("preassigned test id is required")
	}
	// Keep the selected identity in the request-scoped fence even though the
	// public field is otherwise only an ID-allocation input. This catches an
	// ownership change without reintroducing every terminal run into the snapshot.
	req.RequestedTestID = id
	unlockControl, err := tr.lockTestLifecycleControl(context.Background(), id)
	if err != nil {
		return nil, err
	}
	defer unlockControl()
	// Tests must be created on the execution leader so the in-memory queue stays
	// coherent with the replica that will actually run them.
	if !tr.isExecutionLeader() || !tr.executionLeaseTermCurrent(leaseTerm) {
		return nil, errNotExecutionLeader
	}
	executionFence := leaseTerm.ownerID
	if tr.durableExecutionFence && !leaseTerm.valid() {
		return nil, errNotExecutionLeader
	}
	materializationKey := strings.TrimSpace(req.MaterializationKey)
	// Deterministic callers (CI and execution-plan recovery) may retry after the
	// original create committed. Adopt the authoritative existing materialization
	// before building or persisting another QUEUED projection. In particular, do
	// not replace a RUNNING in-memory object and detach its live control handles.
	if materializationKey != "" {
		if existing, ok := tr.GetTestSummary(id); ok {
			if !tr.executionLeaseTermCurrent(leaseTerm) {
				return nil, errNotExecutionLeader
			}
			if !sameManagedTestMaterialization(existing, id, materializationKey, req.ProjectID) {
				return nil, fmt.Errorf("%w: test %s", errTestIdentityCollision, id)
			}
			existing.creationAdopted = true
			return existing, nil
		}
	}
	now := requestedAt
	if now.IsZero() {
		now = time.Now()
	}
	status := "QUEUED"
	if req.ReserveLaunchWindow {
		status = "RESERVED"
		start := now
		if req.ScheduledStartAt != nil && !req.ScheduledStartAt.IsZero() {
			start = *req.ScheduledStartAt
		}
		roundedStart := ceilToNextQuarterHour(start)
		req.ScheduledStartAt = &roundedStart
	} else if req.ScheduledStartAt != nil && req.ScheduledStartAt.After(now) {
		status = "SCHEDULED"
	}
	req.deferRunRefKey = true
	test, buildSnapshot, buildRevision, err := tr.buildManagedTestFromSnapshot(req, id, status)
	if err != nil {
		return nil, err
	}
	if runReference == nil {
		return nil, fmt.Errorf("run reference reservation is required")
	}
	// Compilation performs its own clock read. Pin the durable creation identity
	// to the outer request boundary so a retry cannot change either CreatedAt or
	// the day embedded in its reference key.
	test.CreatedAt = runReference.createdAt
	var reservedEvidence launchWindowEvidence
	if status == "RESERVED" && test.ScheduledStartAt != nil {
		reservedEvidence = tr.loadLaunchWindowEvidenceForProject(launchWindowForTest(test, test.ScheduledStartAt.UTC()), test.ProjectID, testAssignedPoolID(test))
	}
	var timeslotEvidence *storage.CapacityReservation
	if req.TimeslotID != nil && strings.TrimSpace(*req.TimeslotID) != "" {
		var timeslotErr error
		timeslotEvidence, timeslotErr = tr.loadCapacityTimeslotEvidence(strings.TrimSpace(*req.TimeslotID))
		if timeslotErr != nil {
			return nil, timeslotErr
		}
	}
	if status == "RESERVED" {
		// Overlap admission is a check-then-create decision across all reserved
		// runs. Keep that boundary serialized without holding tr.mu across secret
		// or storage I/O so two different IDs cannot both commit from the same
		// pre-install launch-window snapshot.
		tr.reservedLaunchCreateMu.Lock()
		defer tr.reservedLaunchCreateMu.Unlock()
	}
	// Keep a same-leader project deletion outside the final evidence check,
	// durable insert, and in-memory install. A cross-replica takeover is fenced
	// by leaseTerm below; a stale creator never installs after losing that term.
	releaseProjectMutation := tr.acquireProjectTestMutation(test.ProjectID)
	defer releaseProjectMutation()
	// First fence the immutable build before reserving a reference sequence. No
	// provider or test-store mutation occurs until the same lease/content identity
	// has won twice around that off-lock sequence reservation.
	tr.mu.Lock()
	if !tr.executionLeaseTermCurrent(leaseTerm) {
		tr.mu.Unlock()
		return nil, errNotExecutionLeader
	}
	if existing := tr.tests[id]; existing != nil {
		if materializationKey == "" || !sameManagedTestMaterialization(existing, id, materializationKey, req.ProjectID) {
			tr.mu.Unlock()
			return nil, fmt.Errorf("%w: test %s", errTestIdentityCollision, id)
		}
		adopted := cloneManagedTestForReadLocked(existing)
		adopted.creationAdopted = true
		tr.mu.Unlock()
		return adopted, nil
	}
	if !tr.managedTestBuildFenceCurrentLocked(buildRevision) {
		tr.mu.Unlock()
		return nil, fmt.Errorf("%w: controller content changed while test creation was being compiled", errManagedTestBuildFenceStale)
	}
	tr.mu.Unlock()

	if runReference.refKey == "" {
		buildSnapshot.mu.Lock()
		sequence, sequenceErr := buildSnapshot.nextRunSeqLocked(test.CreatedAt)
		buildSnapshot.mu.Unlock()
		if sequenceErr != nil {
			return nil, fmt.Errorf("allocate run reference key: %w", sequenceErr)
		}
		runReference.refKey = refkey.Run(buildSnapshot.envCode, test.CreatedAt, sequence)
	}
	test.RefKey = runReference.refKey
	test.executionFence = executionFence

	tr.mu.Lock()
	tr.mergeSchedulerSnapshotSequenceLocked(buildSnapshot)
	if !tr.executionLeaseTermCurrent(leaseTerm) {
		tr.mu.Unlock()
		return nil, errNotExecutionLeader
	}
	if existing := tr.tests[id]; existing != nil {
		if materializationKey == "" || !sameManagedTestMaterialization(existing, id, materializationKey, req.ProjectID) {
			tr.mu.Unlock()
			return nil, fmt.Errorf("%w: test %s", errTestIdentityCollision, id)
		}
		adopted := cloneManagedTestForReadLocked(existing)
		adopted.creationAdopted = true
		tr.mu.Unlock()
		return adopted, nil
	}
	if !tr.managedTestBuildFenceCurrentLocked(buildRevision) {
		tr.mu.Unlock()
		return nil, fmt.Errorf("%w: controller content changed while test creation was being prepared", errManagedTestBuildFenceStale)
	}
	if status == "RESERVED" && test.ScheduledStartAt != nil {
		window := launchWindowForTest(test, test.ScheduledStartAt.UTC())
		if conflicts := tr.launchWindowConflictsWithEvidenceLocked(test, window, now.UTC(), "", "", reservedEvidence); len(conflicts) > 0 {
			tr.mu.Unlock()
			return nil, fmt.Errorf("launch window conflict: %s", conflicts[0].Message)
		}
	}
	// Strict: when a timeslot is supplied, bind it as part of creation. The
	// durable test+binding transaction remains authoritative after this pure
	// evidence check.
	if req.TimeslotID != nil && strings.TrimSpace(*req.TimeslotID) != "" {
		if err := validateAndApplyTimeslotEvidenceLocked(timeslotEvidence, test); err != nil {
			tr.mu.Unlock()
			return nil, err
		}
	}
	tr.mu.Unlock()

	runtimeSecretAttempt, err := tr.persistRuntimeOverrideSecretValues(
		test.ProjectID, test.ID, test.MaterializationKey, test.RefKey, test.Groups, req.creationActor,
	)
	if err != nil {
		return nil, err
	}
	if err := tr.releaseProvisionedRuntimeSecretOverlays(test); err != nil {
		rollbackErr := tr.rollbackRuntimeOverrideSecretAttempt(test.ProjectID, test.ID, runtimeSecretAttempt, "runtime_secret_detach_failed")
		return nil, errors.Join(err, rollbackErr)
	}
	if err := tr.saveNewManagedTestRecordWithTerm(test, leaseTerm); err != nil {
		persistErr := fmt.Errorf("persist test: %w", err)
		rollbackErr := tr.rollbackRuntimeOverrideSecretAttempt(test.ProjectID, test.ID, runtimeSecretAttempt, "test_persistence_failed")
		return nil, errors.Join(persistErr, rollbackErr)
	}
	tr.recordTestCreationActivity(test, req.creationActor)
	if err := tr.completeRuntimeOverrideSecretAttempt(runtimeSecretAttempt); err != nil {
		// The durable test is the source of truth. Recovery observes it and only
		// removes the value-free journal; it never compensates committed secrets.
		slog.Warn("runtime credential provisioning journal cleanup deferred", "test_id", test.ID, "error", err)
		tr.scheduleRuntimeOverrideSecretAttempt(runtimeSecretAttempt.ID, runtimeSecretAttempt.LeaseExpiresAt)
	}
	// The atomic create has committed. A lease loss from this point forward must
	// not hide the newly allocated identity from an ordinary caller: return the
	// exact committed snapshot, but leave installation/reconciliation to the new
	// leader so this stale replica cannot resurrect state deleted by its successor.
	if !tr.executionLeaseTermCurrent(leaseTerm) {
		return cloneManagedTestForReadLocked(test), nil
	}

	tr.mu.Lock()
	if !tr.executionLeaseTermCurrent(leaseTerm) {
		tr.mu.Unlock()
		return cloneManagedTestForReadLocked(test), nil
	}
	if existing := tr.tests[id]; existing != nil {
		if materializationKey == "" || !sameManagedTestMaterialization(existing, id, materializationKey, req.ProjectID) {
			tr.mu.Unlock()
			return nil, fmt.Errorf("%w: test %s", errTestIdentityCollision, id)
		}
		adopted := cloneManagedTestForReadLocked(existing)
		adopted.creationAdopted = true
		tr.mu.Unlock()
		return adopted, nil
	}
	if !tr.managedTestBuildFenceCurrentLocked(buildRevision) {
		slog.Info("controller admission state changed after durable test creation; installing the exact committed snapshot",
			"test_id", test.ID, "ref_key", test.RefKey)
	}
	// executionFence authorized the create-only durable insert. Passive rows do
	// not own runtime work, so retaining that process-private token would prevent
	// a later lease epoch from claiming the queued/scheduled identity.
	releasePassiveExecutionFenceLocked(test)
	tr.tests[test.ID] = test
	tr.indexRefKeyLocked(test.RefKey, test.ID)

	if broadcast && !test.creationAdopted {
		tr.broadcastEvent("test_created", cloneManagedTestLifecycleEvent(test))
	}
	created := cloneManagedTestForReadLocked(test)
	tr.mu.Unlock()
	return created, nil
}

// releaseProvisionedRuntimeSecretOverlays drops the request-time plaintext
// projection after create-only writes have succeeded. Queued and scheduled
// tests must resolve the governed store at launch so rotations are authoritative
// and the original value is not retained for the test's durable lifetime.
func (tr *TestRunner) releaseProvisionedRuntimeSecretOverlays(test *ManagedTest) error {
	if test == nil {
		return nil
	}
	for index := range test.groupSpecs {
		var rules []engine.RedactionRule
		if index < len(test.Groups) {
			rules = test.Groups[index].redactionRules
		}
		redactionConfig, err := engine.NewRedactionConfig(rules)
		if err != nil {
			return fmt.Errorf("group[%d]: detach provisioned runtime credential redaction: %w", index, err)
		}
		test.groupSpecs[index].RedactionConfig = redactionConfig
		test.groupSpecs[index].SecretResolver = nil
	}
	for index := range test.Groups {
		clearRuntimeSecretValueMap(test.Groups[index].runtimeSecretValues)
		test.Groups[index].runtimeSecretValues = nil
	}
	return nil
}

func clearRuntimeSecretValueMap(values map[string]string) {
	for name := range values {
		values[name] = ""
		delete(values, name)
	}
}

func sameManagedTestMaterialization(existing *ManagedTest, id, materializationKey, projectID string) bool {
	if existing == nil || strings.TrimSpace(existing.ID) != strings.TrimSpace(id) {
		return false
	}
	key := strings.TrimSpace(materializationKey)
	if key == "" || strings.TrimSpace(existing.MaterializationKey) != key {
		return false
	}
	projectID = strings.TrimSpace(projectID)
	return projectID == "" || normalizeProjectID(existing.ProjectID) == normalizeProjectID(projectID)
}

func ceilToNextQuarterHour(value time.Time) time.Time {
	if value.IsZero() {
		return value
	}
	if value.Minute()%15 == 0 && value.Second() == 0 && value.Nanosecond() == 0 {
		return value
	}
	floorMinute := value.Minute() / 15 * 15
	floor := time.Date(value.Year(), value.Month(), value.Day(), value.Hour(), floorMinute, 0, 0, value.Location())
	return floor.Add(15 * time.Minute)
}

func isProjectScopedLegacyContextID(value, base string) bool {
	value = strings.TrimSpace(value)
	if value == base {
		return true
	}
	suffix, found := strings.CutPrefix(value, base+"-")
	if !found || len(suffix) != 12 {
		return false
	}
	for _, character := range suffix {
		if !strings.ContainsRune("0123456789abcdef", character) {
			return false
		}
	}
	return true
}

func isLegacyTestReleaseContext(applicationID, releaseID string) bool {
	return isProjectScopedLegacyContextID(applicationID, storage.LegacyApplicationID) &&
		isProjectScopedLegacyContextID(releaseID, storage.LegacyReleaseID)
}

func matchesImplicitRevisionReleaseContext(requestedApplicationID, requestedReleaseID, sourceApplicationID, sourceReleaseID string) bool {
	requestedApplicationID = strings.TrimSpace(requestedApplicationID)
	requestedReleaseID = strings.TrimSpace(requestedReleaseID)
	if !isLegacyTestReleaseContext(sourceApplicationID, sourceReleaseID) {
		return false
	}
	if requestedApplicationID == "" && requestedReleaseID == "" {
		return true
	}
	if requestedApplicationID == "" || requestedReleaseID == "" {
		return false
	}
	applicationMatches := requestedApplicationID == strings.TrimSpace(sourceApplicationID) || requestedApplicationID == storage.LegacyApplicationID
	releaseMatches := requestedReleaseID == strings.TrimSpace(sourceReleaseID) || requestedReleaseID == storage.LegacyReleaseID
	return applicationMatches && releaseMatches
}

// revisionSourceTestLocked resolves and validates the immutable queued-run
// snapshot used by the dashboard's "Create Revised Copy" workflow. Callers
// hold tr.mu (or tr.mu.RLock for draft estimation), so the source cannot change
// status between validation and request derivation.
func (tr *TestRunner) revisionSourceTestLocked(req *CreateTestRequest) (*ManagedTest, error) {
	if req == nil || strings.TrimSpace(req.RevisionSourceTestID) == "" {
		return nil, nil
	}
	if strings.TrimSpace(req.ScriptID) != "" {
		return nil, fmt.Errorf("queued-run revisions require canonical groups with source identities; script_id shorthand is not supported")
	}
	sourceID := tr.resolveRefKeyLocked(strings.TrimSpace(req.RevisionSourceTestID))
	source := tr.tests[sourceID]
	if source == nil {
		return nil, fmt.Errorf("revision source test not found: %s", req.RevisionSourceTestID)
	}
	if strings.ToUpper(strings.TrimSpace(source.Status)) != "QUEUED" {
		return nil, fmt.Errorf("revision source test must be QUEUED, got %s", source.Status)
	}
	req.RevisionSourceTestID = source.ID

	sourceProjectID := normalizeProjectID(source.ProjectID)
	if strings.TrimSpace(req.ProjectID) == "" {
		req.ProjectID = sourceProjectID
	} else if normalizeProjectID(req.ProjectID) != sourceProjectID {
		return nil, fmt.Errorf("revision source test and requested project must belong to one project")
	}

	if strings.TrimSpace(req.ScenarioID) == "" {
		req.ScenarioID = strings.TrimSpace(source.ScenarioID)
	} else if tr.resolveRefKeyLocked(strings.TrimSpace(req.ScenarioID)) != strings.TrimSpace(source.ScenarioID) {
		return nil, fmt.Errorf("revision source test does not belong to the requested scenario")
	} else {
		// Persist the source's canonical scenario identity even when the client used
		// a reference key. A revision is bound to the immutable queued snapshot and
		// must not acquire a different live scenario identity during compilation.
		req.ScenarioID = strings.TrimSpace(source.ScenarioID)
	}
	if isLegacyTestReleaseContext(source.ApplicationID, source.ReleaseID) {
		// Standalone creates use an internal legacy hierarchy. API responses retain
		// the stable public legacy IDs while SQLite scopes the durable rows per
		// project, so a revision may legitimately submit either representation.
		// Keep the context implicit and let release resolution derive the correct
		// project-scoped fallback instead of treating the public IDs as explicit
		// resources in the default project.
		if !matchesImplicitRevisionReleaseContext(req.ApplicationID, req.ReleaseID, source.ApplicationID, source.ReleaseID) {
			return nil, fmt.Errorf("revision source test does not belong to the requested application or release")
		}
		req.ApplicationID = ""
		req.ReleaseID = ""
	} else {
		if strings.TrimSpace(req.ApplicationID) == "" {
			req.ApplicationID = strings.TrimSpace(source.ApplicationID)
		} else if strings.TrimSpace(req.ApplicationID) != strings.TrimSpace(source.ApplicationID) {
			return nil, fmt.Errorf("revision source test does not belong to the requested application")
		}
		if strings.TrimSpace(req.ReleaseID) == "" {
			req.ReleaseID = strings.TrimSpace(source.ReleaseID)
		} else if strings.TrimSpace(req.ReleaseID) != strings.TrimSpace(source.ReleaseID) {
			return nil, fmt.Errorf("revision source test does not belong to the requested release")
		}
	}
	if req.FolderID == nil {
		req.FolderID = cloneStringPtr(source.FolderID)
	} else if stringPtrValue(req.FolderID) != stringPtrValue(source.FolderID) {
		return nil, fmt.Errorf("revision source test does not belong to the requested folder")
	}
	if err := validateRevisionSourceGroupIndexes(req.Groups, source); err != nil {
		return nil, err
	}
	return source, nil
}

func (tr *TestRunner) buildManagedTestLocked(req CreateTestRequest, id string, status string) (*ManagedTest, error) {
	dataPoolsExplicit := req.DataPools != nil
	revisionSource, err := tr.revisionSourceTestLocked(&req)
	if err != nil {
		return nil, err
	}
	scenarioProjectID := ""
	if revisionSource != nil {
		// The queued run is a complete execution snapshot. Do not depend on a fresh
		// in-memory scenario-cache entry while revising it; supported scenario
		// deletion already refuses linked tests, and the source snapshot supplies
		// the canonical scenario/project identity needed here.
		req.ScenarioID = strings.TrimSpace(revisionSource.ScenarioID)
		if req.ScenarioID != "" {
			scenarioProjectID = normalizeProjectID(revisionSource.ProjectID)
		}
		req = createTestRequestFromScenarioRevision(req, nil, revisionSource)
	} else if req.ScenarioID != "" {
		req.ScenarioID = tr.resolveRefKeyLocked(req.ScenarioID)
		scenario, ok := tr.scenarios[req.ScenarioID]
		if !ok {
			return nil, fmt.Errorf("scenario not found: %s", req.ScenarioID)
		}
		scenarioProjectID = normalizeProjectID(scenario.ProjectID)
		req = createTestRequestFromScenarioRevision(req, scenario, revisionSource)
	}
	archiveScripts := []string{tr.resolveRefKeyLocked(req.ScriptID)}
	for _, group := range req.Groups {
		archiveScripts = append(archiveScripts, tr.resolveRefKeyLocked(group.ScriptID))
	}
	archiveScripts = append(archiveScripts, storage.ExecutionSourceScriptIDs(req.MaterializationKey)...)
	if err := tr.checkArchivedExecution(req.ScenarioID, archiveScripts); err != nil {
		return nil, err
	}
	// Normalize after scenario/revision inheritance, but before general request
	// validation, pool lookup, or group compilation. This keeps direct and plan
	// requests on the same binding contract as scenario authoring and ensures the
	// revision comparison below sees canonical defaults and whitespace.
	allowLegacyDataPoolModes := req.allowLegacyDataPoolModes || (!dataPoolsExplicit && (req.ScenarioID != "" || revisionSource != nil))
	var normalizedDataPools []storage.ScenarioDataPoolBinding
	if allowLegacyDataPoolModes {
		normalizedDataPools, err = normalizeLegacyDataPoolBindingIntent(req.DataPools)
	} else {
		normalizedDataPools, err = normalizeDataPoolBindingIntent(req.DataPools)
	}
	if err != nil {
		return nil, fmt.Errorf("invalid test data pool bindings: %w", err)
	}
	if allowLegacyDataPoolModes {
		for index := range req.DataPools {
			mode := strings.TrimSpace(req.DataPools[index].AccessMode)
			if mode == "read_write" || mode == "consume" {
				slog.Warn("canonicalizing persisted legacy data-pool access mode to read_only",
					"test_id", id, "binding_alias", strings.TrimSpace(req.DataPools[index].Alias), "legacy_access_mode", mode)
			}
		}
	}
	req.DataPools = normalizedDataPools

	// Pool-aware assignment: expand a fleet-pool selection into its member targets.
	resolvedAssignment, err := tr.resolvePoolAssignmentLocked(req.Assignment)
	if err != nil {
		return nil, err
	}
	req.Assignment = resolvedAssignment

	dur, err := parseTerminationDuration(req.Duration, req.TerminationMode)
	if err != nil {
		return nil, fmt.Errorf("invalid duration: %w", err)
	}
	if dur <= 0 && req.TerminationMode != dsl.TerminationIterations {
		return nil, fmt.Errorf("duration must be greater than zero")
	}

	scenarioMode, err := validateScenarioRequestWithRevisionSource(req, revisionSource)
	if err != nil {
		return nil, err
	}
	if err := tr.validateGeneratorLossTolerance(req.Assignment); err != nil {
		return nil, err
	}

	groups, err := normalizeCreateTestGroups(req)
	if err != nil {
		return nil, err
	}
	if err := validateTrafficProfile(req.TrafficProfile, groups, req.Duration, scenarioMode, req.GoalConfig); err != nil {
		return nil, err
	}
	for index := range groups {
		groups[index].runtimeSecretScope = id
		groups[index].iterationCompletion = req.TerminationMode == dsl.TerminationIterations
	}
	projectID, err := tr.resolveCreateTestProjectID(req.ProjectID, groups, revisionSource)
	if err != nil {
		return nil, err
	}
	if err := tr.ensureTestIDStaysInProject(id, projectID); err != nil {
		return nil, err
	}
	if scenarioProjectID != "" && scenarioProjectID != projectID {
		return nil, fmt.Errorf("scenario and test scripts must belong to one project")
	}
	if revisionSource != nil && normalizeProjectID(revisionSource.ProjectID) != projectID {
		return nil, fmt.Errorf("revision source test and referenced test resources must belong to one project")
	}
	if err := tr.validateTestMonitorBindings(projectID, requestMonitorBindings(req.MonitorBindings), req.GoalConfig); err != nil {
		return nil, err
	}
	elkCapture, err := tr.resolveELKCaptureForTest(projectID, req.ELKCapture)
	if err != nil {
		return nil, err
	}
	if elkCapture != nil {
		if err = tr.validateLegacyElasticConflict(projectID, req.ScenarioID, elkCapture.ConnectionID, requestMonitorBindings(req.MonitorBindings)); err != nil {
			return nil, err
		}
	}
	traceContext, err := normalizeRunTraceContext(req.TraceContext)
	if err != nil {
		return nil, err
	}
	jointEvidence, err := normalizeJointEvidence(req.JointEvidence)
	if err != nil {
		return nil, err
	}
	dynatraceCapture, err := tr.resolveDynatraceCaptureForTest(projectID, req.DynatraceCapture)
	if err != nil {
		return nil, err
	}
	// Without the project run-monitoring gate, legacy capture keeps its original
	// runtime unchanged. With the gate, it becomes an immutable native binding.
	if dynatraceCapture != nil && runMonitoringEnabled(projectID) {
		bindings, err := tr.translateLegacyDynatraceBindings(projectID, req.ScenarioID, req.MonitorBindings, dynatraceCapture.ConnectionID)
		if err != nil {
			return nil, err
		}
		req.MonitorBindings = &bindings
		// Future tests use only the durable executor. Already active legacy
		// runtimes retain their previously persisted capture snapshots.
		dynatraceCapture = nil
	}
	projectQuota, quotaOK := req.projectQuotaEvidence.quotaFor(&ManagedTest{ProjectID: projectID, Assignment: req.Assignment})
	if !quotaOK && status != "DRAFT" {
		return nil, fmt.Errorf("project quota evidence changed or is unavailable; retry test creation")
	}
	if hasLiveDataPools(req.DataPools) {
		if err := validateLiveTableContinuation(req.DataPools, req.Assignment); err != nil {
			return nil, err
		}
		if err := tr.validateLiveTablePolicy(projectID); err != nil {
			return nil, err
		}
	}
	revisionDataPoolsChanged := revisionSource != nil && !sameRevisionDataPoolBindings(req.DataPools, revisionSource.DataPools)
	var sharedDataSources []dsl.DataSource
	if revisionSource != nil && !revisionDataPoolsChanged {
		var snapshotAvailable bool
		sharedDataSources, snapshotAvailable = revisionSharedDataSourceSnapshot(revisionSource)
		if hasSnapshotDataPools(req.DataPools) && !snapshotAvailable && !allRevisionGroupsHaveRuntimeSnapshots(revisionSource, groups) {
			return nil, fmt.Errorf("revision source test lacks an immutable shared data snapshot for newly added groups")
		}
	} else {
		if err := tr.validateDataPoolProjectsLocked(projectID, req.DataPools); err != nil {
			return nil, err
		}
		sharedDataSources, err = tr.buildSharedDataSourcesLocked(req.DataPools)
		if err != nil {
			return nil, err
		}
	}

	testGroups, engineGroups, totalVUsers, err := tr.compileManagedTestGroupsForModeWithRevisionLocked(groups, req.RuntimeSettings, sharedDataSources, scenarioMode, revisionSource, revisionDataPoolsChanged, projectQuota)
	if err == nil {
		err = validateJointEvidenceCategories(jointEvidence, testGroups)
		// A revised copy may keep a projection its source run was admitted
		// with before categories had to be promoted: one the source's own
		// groups do not promote either. A copy whose changed groups dropped a
		// promotion the source had is refused like any other run.
		if err != nil && revisionSource != nil && reflect.DeepEqual(jointEvidence, revisionSource.JointEvidence) && validateJointEvidenceCategories(jointEvidence, revisionSource.Groups) != nil {
			err = nil
		}
	}
	if err != nil {
		return nil, err
	}
	if err := validateIterationEngineGroups(req.TerminationMode, engineGroups); err != nil {
		return nil, err
	}
	if err := validateTrafficProfileCompiledGroups(req.TrafficProfile, testGroups); err != nil {
		return nil, err
	}
	applyTransactionResponseTimeSLAThresholds(req.GoalConfig, testGroups, engineGroups)
	for index, group := range engineGroups {
		if req.TerminationMode != dsl.TerminationIterations && group.StartOffset > dur {
			return nil, fmt.Errorf("group[%d]: start_offset %s begins after the %s scenario deadline", index, group.StartOffset, dur)
		}
	}
	runtimeSettingsReview := buildRuntimeSettingsScenarioReview(testGroups, engineGroups)
	if status != "DRAFT" && runtimeSettingsReview != nil && runtimeSettingsReview.Status == "blocked" {
		return nil, fmt.Errorf("runtime settings blocked: %s", runtimeSettingsReview.Summary)
	}

	testName := deriveManagedTestName(req.Name, testGroups)
	for index := range testGroups {
		if strings.TrimSpace(testGroups[index].TargetDuration) == "" {
			remaining := max(time.Duration(0), dur-groupStartOffsetDuration(testGroups[index]))
			testGroups[index].TargetDuration = remaining.String()
		}
		if testGroups[index].RuntimeState == "" {
			testGroups[index].RuntimeState = "scheduled"
		}
	}

	if err := tr.projectDeletionErrorLocked(projectID); err != nil {
		return nil, err
	}
	capacityVUsers := trafficProfileCapacityVUsers(req.TrafficProfile, testGroups, dur, req.GoalConfig)
	if capacityVUsers <= 0 {
		capacityVUsers = totalVUsers
	}
	capacityVUsers = saturatingAddNonNegative(capacityVUsers, recoverySpareVUsers(&ManagedTest{Assignment: req.Assignment}))
	if err := enforceTestQuotaWithProjectQuota(projectQuota, capacityVUsers, dur, status); err != nil {
		return nil, err
	}
	totalVUsers = applyTrafficProfileInitialTargets(req.TrafficProfile, testGroups, engineGroups)
	effectiveRuntimeSettings, err := resolveEffectiveRuntimeSettingsWithProjectQuota(req.RuntimeSettings, projectQuota)
	if err != nil {
		return nil, err
	}
	protectedRuntimeSettings, _, err := protectRuntimeOverrideSecrets(id, -1, nil, effectiveRuntimeSettings, false)
	if err != nil {
		return nil, fmt.Errorf("protect test runtime credentials: %w", err)
	}

	applicationID, releaseID, err := resolveCreateTestReleaseContextWithEvidence(projectID, req.ApplicationID, req.ReleaseID, status, req.releaseContextEvidence)
	if err != nil {
		return nil, err
	}
	req.ReleaseID = releaseID
	// HA followers read a run's times back from PostgreSQL, which keeps
	// microseconds; schedules build runs here without createTestWithIDAndTerm.
	now := atMicrosecond(time.Now())
	creationActor := normalizeTestCreationActor(req.creationActor)
	refKey := ""
	if status != "DRAFT" && !req.deferRunRefKey {
		sequence, err := tr.nextRunSeqLocked(now)
		if err != nil {
			return nil, fmt.Errorf("allocate run reference key: %w", err)
		}
		refKey = refkey.Run(tr.envCode, now, sequence)
	}
	test := &ManagedTest{
		ID:                           id,
		RefKey:                       refKey,
		MaterializationKey:           strings.TrimSpace(req.MaterializationKey),
		Name:                         testName,
		Status:                       status,
		ApplicationID:                applicationID,
		ReleaseID:                    req.ReleaseID,
		ScenarioID:                   req.ScenarioID,
		ProjectID:                    projectID,
		FolderID:                     req.FolderID,
		DataPools:                    cloneScenarioDataPoolBindings(req.DataPools),
		Groups:                       testGroups,
		Assignment:                   cloneGeneratorAssignmentSettings(req.Assignment),
		CapacityAdvisory:             tr.estimateCapacityAdvisoryForAssignment(projectID, testGroups, capacityVUsers, dur, req.ResourceBaseline, effectiveRuntimeSettings, req.Assignment),
		RuntimeSettingsReview:        cloneRuntimeSettingsReview(runtimeSettingsReview),
		expectedProjectUpdatedAt:     req.projectQuotaEvidence.updatedAt,
		expectedApplicationUpdatedAt: req.releaseContextEvidence.applicationUpdatedAt,
		expectedReleaseUpdatedAt:     req.releaseContextEvidence.releaseUpdatedAt,
		ScenarioMode:                 scenarioMode,
		ScriptShakeout:               req.ScriptShakeout,
		ResourceBaseline:             req.ResourceBaseline,
		StartDelay:                   req.StartDelay,
		GoalConfig:                   cloneGoalConfig(req.GoalConfig),
		TrafficProfile:               cloneTrafficProfile(req.TrafficProfile),
		RuntimeSettings:              cloneRuntimeSettings(protectedRuntimeSettings),
		ELKCapture:                   elkCapture,
		DynatraceCapture:             dynatraceCapture,
		MonitorBindings:              requestMonitorBindings(req.MonitorBindings),
		TraceContext:                 traceContext,
		JointEvidence:                jointEvidence,
		TotalVUsers:                  totalVUsers,
		Duration:                     dur,
		DurationStr:                  req.Duration,
		TerminationMode:              req.TerminationMode,
		CreatedAt:                    now,
		CreatedBySubject:             creationActor.Subject,
		CreatedByDisplayName:         creationActor.DisplayName,
		ScheduledStartAt:             cloneTimePtr(req.ScheduledStartAt),
		groupSpecs:                   engineGroups,
		pinnedSecrets:                &runPinnedSecretState{},
	}
	for index := range test.groupSpecs {
		if test.groupSpecs[index].Script != nil {
			test.groupSpecs[index].SecretResolver = overlayRuntimeSecretResolver(
				test.Groups[index].runtimeSecretValues,
				tr.boundSecretResolverForRunGroup(test, index, test.groupSpecs[index].Script.ProjectID),
			)
		}
	}
	test.ExecutionPlan = cloneTestExecutionPlanLink(req.ExecutionPlan)
	if req.ExecutionPlanApproval != nil {
		test.ExecutionPlanApproval = runArchiveExecutionPlanApprovalSnapshot(req.ExecutionPlanApproval)
		if test.ExecutionPlanApproval != nil {
			test.ExecutionPlanApproval.ApprovedPeakVUsers = approvedExecutionPlanPeakForTest(test)
		}
	}

	if len(testGroups) == 1 {
		test.ScriptID = testGroups[0].ScriptID
		test.ScriptName = testGroups[0].ScriptName
	}
	if status != "DRAFT" {
		if conflict := externalRunnerLaunchConflict(test.Groups); conflict != nil {
			return nil, fmt.Errorf("%s: %s", conflict.Code, conflict.Message)
		}
		if conflicts := tr.javaVuserRunnerLaunchConflictsForManagedTestLocked(test); len(conflicts) > 0 {
			conflict := conflicts[0]
			return nil, fmt.Errorf("%s: %s", conflict.Code, conflict.Message)
		}
		if conflict := importPromotionLaunchConflict(test.Groups); conflict != nil {
			return nil, fmt.Errorf("%s: %s", conflict.Code, conflict.Message)
		}
	}
	return test, nil
}

func applyTransactionResponseTimeSLAThresholds(goals *GoalConfig, groups []TestGroup, specs []engine.GroupConfig) {
	var defaultMs float64
	if goals != nil && goals.TransactionResponseTimeSLAMs != nil {
		defaultMs = *goals.TransactionResponseTimeSLAMs
	}
	for index := range specs {
		specs[index].TransactionResponseTimeSLAMs = defaultMs
		if index < len(groups) && groups[index].TransactionResponseTimeSLAMs != nil {
			specs[index].TransactionResponseTimeSLAMs = *groups[index].TransactionResponseTimeSLAMs
		}
	}
}

const (
	runtimeOverrideSecretProviderTimeout = 10 * time.Second
	runtimeOverrideSecretAttemptLease    = 5 * time.Minute
	runtimeOverrideSecretRecoveryRetry   = time.Minute
)

var errServiceAccountRuntimeSecretWrite = errors.New("service accounts cannot create project secrets from runtime credentials; configure the secret with an authorized human user and reference it with {{secret.NAME}}")
var errUnattributedRuntimeSecretWrite = errors.New("runtime credentials cannot create project secrets without durable human actor attribution; update the schedule or rerun the execution plan as an authorized user")

type testCreationActor struct {
	// InitiatedBy distinguishes automatic activity from the retained authorization actor.
	InitiatedBy string
	Type        string
	Subject     string
	DisplayName string
}

func systemTestCreationActor(subject string) testCreationActor {
	return normalizeTestCreationActor(testCreationActor{Type: "system", Subject: subject})
}

func normalizeTestCreationActor(actor testCreationActor) testCreationActor {
	actor.Type = strings.ToLower(strings.TrimSpace(actor.Type))
	actor.Subject = strings.TrimSpace(actor.Subject)
	actor.DisplayName = strings.TrimSpace(actor.DisplayName)
	actor.InitiatedBy = strings.TrimSpace(actor.InitiatedBy)
	if actor.Type == "" {
		actor.Type = "system"
	}
	if actor.Subject == "" {
		actor.Subject = "controller:test-creation"
	}
	return actor
}

func runtimeSecretWriteActorError(actor testCreationActor) error {
	actor = normalizeTestCreationActor(actor)
	switch actor.Type {
	case "user", "system", "dev_mode":
		return nil
	case "service_account":
		return errServiceAccountRuntimeSecretWrite
	default:
		return errUnattributedRuntimeSecretWrite
	}
}

type runtimeOverrideSecretRollbackCandidate struct {
	Name      string
	Ambiguous bool
}

type runtimeOverrideSecretAttempt struct {
	ID             string
	Actor          string
	AuditActor     testCreationActor
	ResourceType   string
	ResourceID     string
	Names          []string
	Candidates     []runtimeOverrideSecretRollbackCandidate
	LeaseExpiresAt time.Time
}

type runtimeSecretProvisioningOptions struct {
	ResourceType      string
	CommitFingerprint string
}

func (tr *TestRunner) persistRuntimeOverrideSecretValues(projectID, testID, materializationKey, refKey string, groups []TestGroup, auditActors ...testCreationActor) (runtimeOverrideSecretAttempt, error) {
	auditActor := systemTestCreationActor("controller:test-creation")
	if len(auditActors) > 0 {
		auditActor = normalizeTestCreationActor(auditActors[0])
	}
	return tr.persistRuntimeOverrideSecretValuesForResource(
		projectID, testID, materializationKey, refKey, groups, auditActor,
		runtimeSecretProvisioningOptions{ResourceType: "test"},
	)
}

func (tr *TestRunner) persistRuntimeOverrideSecretValuesForResource(projectID, resourceID, materializationKey, refKey string, groups []TestGroup, auditActor testCreationActor, options runtimeSecretProvisioningOptions) (runtimeOverrideSecretAttempt, error) {
	values := make(map[string]string)
	for _, group := range groups {
		for name, value := range group.runtimeSecretValues {
			if existing, ok := values[name]; ok && existing != value {
				return runtimeOverrideSecretAttempt{}, fmt.Errorf("runtime credential projection conflict for %s", name)
			}
			values[name] = value
		}
	}
	if len(values) == 0 {
		return runtimeOverrideSecretAttempt{}, nil
	}
	resourceType := strings.ToLower(strings.TrimSpace(options.ResourceType))
	if resourceType == "" {
		resourceType = "test"
	}
	auditActor = normalizeTestCreationActor(auditActor)
	// Service-account test execution authority is deliberately narrower than
	// project-secret write authority. Reject before consulting or invoking the
	// configured provider; service accounts may only use existing references.
	if err := runtimeSecretWriteActorError(auditActor); err != nil {
		return runtimeOverrideSecretAttempt{}, err
	}
	store := tr.projectSecretStore
	if store == nil || !secretstore.SupportsCreate(store) {
		return runtimeOverrideSecretAttempt{}, errors.New("runtime credentials were entered, but the configured project secret provider cannot create governed values; configure a writable encrypted project secret store or use existing {{secret.NAME}} references")
	}
	if !secretstore.SupportsCreatedValueRollback(store) {
		return runtimeOverrideSecretAttempt{}, errors.New("runtime credentials were entered, but the configured project secret provider cannot safely roll back a failed create; configure the encrypted project secret store or use existing {{secret.NAME}} references")
	}
	if tr.scenarioStore == nil {
		return runtimeOverrideSecretAttempt{}, fmt.Errorf("audit persistence is required before storing governed runtime credentials")
	}
	if tr.secretAttemptStore == nil {
		return runtimeOverrideSecretAttempt{}, fmt.Errorf("durable provisioning recovery is required before storing governed runtime credentials")
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	attemptID := uuid.New().String()
	actor := "runtime-" + resourceType + ":" + strings.TrimSpace(resourceID) + ":attempt:" + attemptID
	attempt := runtimeOverrideSecretAttempt{
		ID:           attemptID,
		Actor:        actor,
		AuditActor:   auditActor,
		ResourceType: resourceType,
		ResourceID:   strings.TrimSpace(resourceID),
		Names:        append([]string(nil), names...),
		Candidates:   make([]runtimeOverrideSecretRollbackCandidate, 0, len(names)),
	}
	now := time.Now().UTC()
	attempt.LeaseExpiresAt = now.Add(runtimeOverrideSecretAttemptLease)
	created, err := tr.secretAttemptStore.CreateRuntimeSecretProvisioningAttempt(&storage.RuntimeSecretProvisioningAttempt{
		ID:                    attempt.ID,
		ProjectID:             normalizeProjectID(projectID),
		ResourceType:          resourceType,
		TestID:                strings.TrimSpace(resourceID),
		MaterializationKey:    strings.TrimSpace(materializationKey),
		RefKey:                strings.TrimSpace(refKey),
		CommitFingerprint:     strings.TrimSpace(options.CommitFingerprint),
		Actor:                 attempt.Actor,
		AuditActorType:        attempt.AuditActor.Type,
		AuditActorSubject:     attempt.AuditActor.Subject,
		AuditActorDisplayName: attempt.AuditActor.DisplayName,
		SecretNames:           append([]string(nil), names...),
		LeaseExpiresAt:        attempt.LeaseExpiresAt,
		CreatedAt:             now,
	})
	if err != nil {
		return runtimeOverrideSecretAttempt{}, fmt.Errorf("begin durable runtime credential provisioning: %w", err)
	}
	if !created {
		return runtimeOverrideSecretAttempt{}, fmt.Errorf("runtime credential provisioning is already in progress for this %s", resourceType)
	}
	releaseActive := tr.markRuntimeOverrideSecretAttemptActive(attempt.ID)
	defer releaseActive()
	// Arm recovery as soon as the durable journal exists. This remains effective
	// if a provider or audit implementation panics and request-level recovery
	// unwinds the provisioning call before its ordinary rollback path can run.
	tr.scheduleRuntimeOverrideSecretAttempt(attempt.ID, attempt.LeaseExpiresAt)
	ctx, cancel := context.WithTimeout(context.Background(), runtimeOverrideSecretProviderTimeout)
	defer cancel()
	providerErrorRedaction, err := engine.WithSecretValueRedactionChecked(nil, runtimeSecretValueList(values))
	if err != nil {
		return runtimeOverrideSecretAttempt{}, fmt.Errorf("prepare governed runtime credential redaction: %w", err)
	}
	fail := func(operationErr error) (runtimeOverrideSecretAttempt, error) {
		rollbackErr := tr.rollbackRuntimeOverrideSecretAttempt(projectID, resourceID, attempt, "runtime_secret_batch_failed")
		return runtimeOverrideSecretAttempt{}, errors.Join(operationErr, rollbackErr)
	}
	for _, name := range names {
		value := values[name]
		if err := tr.auditRuntimeOverrideSecretOperation(projectID, resourceType, resourceID, name, auditActor, "attempt", "pending"); err != nil {
			return fail(fmt.Errorf("audit governed runtime credential %s before create: %w", name, err))
		}
		if _, err := store.Create(ctx, projectID, name, value, actor); err == nil {
			attempt.Candidates = append(attempt.Candidates, runtimeOverrideSecretRollbackCandidate{Name: name})
			if auditErr := tr.auditRuntimeOverrideSecretOperation(projectID, resourceType, resourceID, name, auditActor, "success", "created"); auditErr != nil {
				return fail(fmt.Errorf("audit governed runtime credential %s after create: %w", name, auditErr))
			}
			continue
		} else if !errors.Is(err, secretstore.ErrAlreadyExists) {
			// A transport error does not prove Create failed before commit. Include
			// the current logical name in ownership-checked compensation; a
			// preexisting or rotated value cannot be removed.
			attempt.Candidates = append(attempt.Candidates, runtimeOverrideSecretRollbackCandidate{Name: name, Ambiguous: true})
			outcome := "provider_error_commit_ambiguous"
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
				outcome = "provider_timeout_commit_ambiguous"
			}
			safeProviderErr := errors.New(providerErrorRedaction.ScrubText(err.Error()))
			if auditErr := tr.auditRuntimeOverrideSecretOperation(projectID, resourceType, resourceID, name, auditActor, "failure", outcome); auditErr != nil {
				return fail(errors.Join(fmt.Errorf("store governed runtime credential %s: %w", name, safeProviderErr), auditErr))
			}
			return fail(fmt.Errorf("store governed runtime credential %s: %w", name, safeProviderErr))
		}
		existing, _, err := store.Get(ctx, projectID, name)
		if err != nil || subtle.ConstantTimeCompare([]byte(existing), []byte(value)) != 1 {
			outcome := "existing_value_conflict"
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
				outcome = "provider_timeout"
			}
			if auditErr := tr.auditRuntimeOverrideSecretOperation(projectID, resourceType, resourceID, name, auditActor, "failure", outcome); auditErr != nil {
				return fail(errors.Join(fmt.Errorf("governed runtime credential %s already exists with a different value", name), auditErr))
			}
			return fail(fmt.Errorf("governed runtime credential %s already exists with a different value", name))
		}
		if err := tr.auditRuntimeOverrideSecretOperation(projectID, resourceType, resourceID, name, auditActor, "success", "existing_value_reused"); err != nil {
			return fail(fmt.Errorf("audit reused governed runtime credential %s: %w", name, err))
		}
	}
	return attempt, nil
}

func (tr *TestRunner) auditRuntimeOverrideSecretOperation(projectID, resourceType, resourceID, name string, actor testCreationActor, status, outcome string) error {
	if tr == nil || tr.scenarioStore == nil {
		return fmt.Errorf("audit persistence is unavailable")
	}
	details := map[string]any{
		"outcome":       strings.TrimSpace(outcome),
		"provider":      secretStoreProvider(tr.projectSecretStore),
		"resource_type": strings.TrimSpace(resourceType),
		"resource_id":   strings.TrimSpace(resourceID),
	}
	if strings.TrimSpace(resourceType) == "test" {
		details["test_id"] = strings.TrimSpace(resourceID)
	} else if strings.TrimSpace(resourceType) == "scenario" {
		details["scenario_id"] = strings.TrimSpace(resourceID)
	}
	if strings.HasSuffix(outcome, "_commit_ambiguous") {
		details["outcome"] = strings.TrimSuffix(outcome, "_commit_ambiguous")
		details["commit_state"] = "ambiguous"
	}
	actor = normalizeTestCreationActor(actor)
	return tr.scenarioStore.SaveAuditEvent(&storage.AuditEvent{
		ID:               uuid.New().String(),
		Action:           "project_secret.runtime_override.create",
		ActorType:        actor.Type,
		ActorSubject:     actor.Subject,
		ActorDisplayName: actor.DisplayName,
		ProjectID:        normalizeProjectID(projectID),
		ResourceType:     "project_secret",
		ResourceID:       strings.TrimSpace(name),
		Status:           strings.TrimSpace(status),
		Details:          details,
		CreatedAt:        time.Now().UTC(),
	})
}

func (tr *TestRunner) rollbackRuntimeOverrideSecretAttempt(projectID, testID string, attempt runtimeOverrideSecretAttempt, trigger string) error {
	rollback, ok := tr.projectSecretStore.(secretstore.CreatedValueRollbackStore)
	if !ok && len(attempt.Candidates) > 0 {
		return secretstore.ErrRollbackUnsupported
	}
	ctx, cancel := context.WithTimeout(context.Background(), runtimeOverrideSecretProviderTimeout)
	defer cancel()
	auditActor := normalizeTestCreationActor(attempt.AuditActor)
	resourceType := strings.TrimSpace(attempt.ResourceType)
	if resourceType == "" {
		resourceType = "test"
	}
	resourceID := strings.TrimSpace(attempt.ResourceID)
	if resourceID == "" {
		resourceID = strings.TrimSpace(testID)
	}
	var rollbackErr error
	for index := len(attempt.Candidates) - 1; index >= 0; index-- {
		candidate := attempt.Candidates[index]
		name := candidate.Name
		removed, err := rollback.RollbackCreated(ctx, projectID, name, attempt.Actor)
		status, outcome := "success", "rolled_back"
		if err != nil {
			status, outcome = "failure", "provider_error"
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
				outcome = "provider_timeout"
			}
			// Provider failures are deliberately not wrapped: provider messages can
			// echo the value passed to Create.
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("roll back governed runtime credential %s: provider operation failed", name))
		} else if !removed {
			if candidate.Ambiguous {
				outcome = "ownership_not_held"
			} else {
				status, outcome = "failure", "ownership_changed"
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("roll back governed runtime credential %s: current value is not owned by this create attempt", name))
			}
		}
		if tr == nil || tr.scenarioStore == nil {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("audit persistence is unavailable for governed runtime credential rollback"))
			continue
		}
		details := map[string]any{
			"outcome":       outcome,
			"trigger":       strings.TrimSpace(trigger),
			"provider":      secretStoreProvider(tr.projectSecretStore),
			"resource_type": resourceType,
			"resource_id":   resourceID,
		}
		if resourceType == "test" {
			details["test_id"] = resourceID
		} else if resourceType == "scenario" {
			details["scenario_id"] = resourceID
		}
		if auditErr := tr.scenarioStore.SaveAuditEvent(&storage.AuditEvent{
			ID:               uuid.New().String(),
			Action:           "project_secret.runtime_override.rollback",
			ActorType:        auditActor.Type,
			ActorSubject:     auditActor.Subject,
			ActorDisplayName: auditActor.DisplayName,
			ProjectID:        normalizeProjectID(projectID),
			ResourceType:     "project_secret",
			ResourceID:       strings.TrimSpace(name),
			Status:           status,
			Details:          details,
			CreatedAt:        time.Now().UTC(),
		}); auditErr != nil {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("audit governed runtime credential rollback %s: %w", name, auditErr))
		}
	}
	if rollbackErr == nil {
		rollbackErr = tr.completeRuntimeOverrideSecretAttempt(attempt)
	}
	return rollbackErr
}

func (tr *TestRunner) completeRuntimeOverrideSecretAttempt(attempt runtimeOverrideSecretAttempt) error {
	if strings.TrimSpace(attempt.ID) == "" {
		return nil
	}
	if tr == nil || tr.secretAttemptStore == nil {
		return fmt.Errorf("runtime credential provisioning journal is unavailable")
	}
	_, err := tr.secretAttemptStore.DeleteRuntimeSecretProvisioningAttempt(attempt.ID, attempt.Actor)
	if err != nil {
		return fmt.Errorf("complete runtime credential provisioning journal: %w", err)
	}
	return nil
}

func createTestRequestFromScenario(req CreateTestRequest, scenario *StoredScenario) CreateTestRequest {
	return createTestRequestFromScenarioRevision(req, scenario, nil)
}

// createTestRequestFromScenarioRevision derives a create request from either a
// stored scenario or an immutable queued-run snapshot. A revision source takes
// precedence for every value that was already materialized on that run,
// especially masked runtime credentials; the live scenario remains a fallback
// only for ordinary scenario creates.
func createTestRequestFromScenarioRevision(req CreateTestRequest, scenario *StoredScenario, revisionSource *ManagedTest) CreateTestRequest {
	if revisionSource != nil {
		req.Name = firstNonEmpty(req.Name, revisionSource.Name)
		req.TerminationMode = firstNonEmpty(req.TerminationMode, revisionSource.TerminationMode)
		req.Duration = firstNonEmpty(req.Duration, revisionSource.DurationStr)
		req.ScenarioMode = firstNonEmpty(req.ScenarioMode, revisionSource.ScenarioMode)
		if !req.scriptShakeoutProvided && !req.ScriptShakeout {
			req.ScriptShakeout = revisionSource.ScriptShakeout
		}
		if !req.resourceBaselineProvided && !req.ResourceBaseline {
			req.ResourceBaseline = revisionSource.ResourceBaseline
		}
		if req.StartDelay == "" {
			req.StartDelay = revisionSource.StartDelay
		}
		if req.MonitorBindings == nil && revisionSource.MonitorBindings != nil {
			b := append([]storage.MonitorBinding{}, revisionSource.MonitorBindings.Sources...)
			req.MonitorBindings = &b
		}
		if req.GoalConfig == nil {
			req.GoalConfig = cloneGoalConfig(revisionSource.GoalConfig)
		}
		if req.TrafficProfile == nil {
			req.TrafficProfile = cloneTrafficProfile(revisionSource.TrafficProfile)
		}
		if req.Assignment == nil {
			req.Assignment = cloneGeneratorAssignmentSettings(revisionSource.Assignment)
		}
		if req.DataPools == nil {
			req.DataPools = cloneScenarioDataPoolBindings(revisionSource.DataPools)
		}
		// The run's joint evidence projection is a materialized setting too:
		// a revised copy keeps it unless the request names another.
		if req.JointEvidence == nil {
			req.JointEvidence = revisionSource.JointEvidence.Clone()
		}
	} else if scenario != nil {
		req.Name = firstNonEmpty(req.Name, scenario.Name)
		req.TerminationMode = firstNonEmpty(req.TerminationMode, scenario.TerminationMode)
		req.Duration = firstNonEmpty(req.Duration, scenario.Duration)
		req.ScenarioMode = firstNonEmpty(req.ScenarioMode, scenario.ScenarioMode)
		if !req.scriptShakeoutProvided && !req.ScriptShakeout {
			req.ScriptShakeout = scenario.ScriptShakeout
		}
		if !req.resourceBaselineProvided && !req.ResourceBaseline {
			req.ResourceBaseline = scenario.ResourceBaseline
		}
		if req.StartDelay == "" {
			req.StartDelay = scenario.StartDelay
		}
		if req.GoalConfig == nil {
			req.GoalConfig = cloneGoalConfig(scenario.GoalConfig)
		}
		if req.TrafficProfile == nil {
			req.TrafficProfile = cloneTrafficProfile(scenario.TrafficProfile)
		}
		if req.Assignment == nil {
			req.Assignment = cloneGeneratorAssignmentSettings(scenario.Assignment)
		}
		if req.DataPools == nil {
			req.DataPools = cloneScenarioDataPoolBindings(scenario.DataPools)
		} else if req.dataPoolOverridesByAlias {
			req.DataPools = overlayDataPoolBindingsByAlias(scenario.DataPools, req.DataPools)
		}
		// Every entry point that runs a scenario (a direct run, a schedule, a
		// plan entry) captures the scenario's joint evidence unless the
		// request names another projection.
		if req.JointEvidence == nil {
			req.JointEvidence = scenario.JointEvidence.Clone()
		}
	}

	var rootRuntime *RuntimeSettingsOverride
	if revisionSource != nil {
		rootRuntime = revisionSource.RuntimeSettings
	} else if scenario != nil {
		rootRuntime = scenario.RuntimeSettings
	}
	if req.RuntimeSettings == nil {
		req.RuntimeSettings = cloneRuntimeSettings(rootRuntime)
	} else {
		// Run-only editors submit a complete clone of a masked runtime profile so
		// advanced siblings survive a one-field edit. A revised queued run must
		// restore those masks from the source run, never from a mutable scenario.
		req.RuntimeSettings = reconcileGroupRuntimeSecrets(req.RuntimeSettings, rootRuntime)
	}

	if len(req.Groups) == 0 && req.ScriptID == "" {
		switch {
		case revisionSource != nil:
			req.Groups = createGroupEntriesFromManagedTest(revisionSource)
		case scenario != nil:
			req.Groups = createGroupEntriesFromStoredScenario(scenario)
		}
	} else if len(req.Groups) > 0 {
		// Reconcile masked credentials by stable group identity. Array position is
		// not an identity: callers may reorder groups, and one script may legitimately
		// appear more than once with distinct runtime credentials.
		for i := range req.Groups {
			if req.Groups[i].RuntimeSettings == nil {
				continue
			}
			var prior *RuntimeSettingsOverride
			if revisionSource != nil {
				prior = existingManagedTestGroupRuntime(revisionSource, req.Groups[i])
			} else {
				prior = existingGroupRuntime(scenario, req.Groups[i])
			}
			req.Groups[i].RuntimeSettings = reconcileGroupRuntimeSecrets(req.Groups[i].RuntimeSettings, prior)
		}
	}
	return req
}

func createGroupEntriesFromStoredScenario(scenario *StoredScenario) []CreateGroupEntry {
	if scenario == nil {
		return nil
	}
	groups := make([]CreateGroupEntry, 0, len(scenario.Scripts))
	for _, script := range scenario.Scripts {
		groups = append(groups, CreateGroupEntry{
			Name:                         script.Name,
			ScriptID:                     script.ScriptID,
			Role:                         script.Role,
			VUsers:                       script.VUsers,
			RampUp:                       script.RampUp,
			RampDown:                     script.RampDown,
			StartOffset:                  script.StartOffset,
			Stages:                       cloneLoadStages(script.Stages),
			Iterations:                   cloneIntPtr(script.Iterations),
			InitStrategy:                 script.Initialization,
			RuntimeSettings:              cloneRuntimeSettings(script.RuntimeSettings),
			Assignment:                   cloneGeneratorAssignmentSettings(script.Assignment),
			TransactionResponseTimeSLAMs: cloneFloat64Ptr(script.TransactionResponseTimeSLAMs),
		})
	}
	return groups
}

func createGroupEntriesFromManagedTest(test *ManagedTest) []CreateGroupEntry {
	if test == nil {
		return nil
	}
	groups := make([]CreateGroupEntry, 0, len(test.Groups))
	for index, group := range test.Groups {
		sourceIndex := index
		var iterationOverride *int
		if group.IterationOverrideKnown {
			iterationOverride = cloneIntPtr(group.IterationOverride)
		}
		groups = append(groups, CreateGroupEntry{
			Name:                         group.Name,
			ScriptID:                     group.ScriptID,
			RevisionSourceGroupIndex:     &sourceIndex,
			Role:                         group.Role,
			VUsers:                       group.VUsers,
			RampUp:                       group.RampUp,
			RampDown:                     group.RampDown,
			StartOffset:                  group.StartOffset,
			Stages:                       cloneLoadStages(group.Stages),
			Iterations:                   iterationOverride,
			InitStrategy:                 group.Initialization,
			RuntimeSettings:              cloneRuntimeSettings(group.RuntimeSettings),
			Assignment:                   cloneGeneratorAssignmentSettings(group.Assignment),
			TransactionResponseTimeSLAMs: cloneFloat64Ptr(group.TransactionResponseTimeSLAMs),
		})
	}
	return groups
}

func existingManagedTestGroupRuntime(test *ManagedTest, group CreateGroupEntry) *RuntimeSettingsOverride {
	if match := existingManagedTestGroup(test, group); match != nil {
		return match.RuntimeSettings
	}
	return nil
}

func existingManagedTestGroup(test *ManagedTest, group CreateGroupEntry) *TestGroup {
	if test == nil {
		return nil
	}
	if group.RevisionNewGroup {
		return nil
	}
	if group.RevisionSourceGroupIndex != nil {
		index := *group.RevisionSourceGroupIndex
		if index >= 0 && index < len(test.Groups) && test.Groups[index].ScriptID == group.ScriptID {
			return &test.Groups[index]
		}
		return nil
	}
	groupName := strings.TrimSpace(group.Name)
	for index := range test.Groups {
		candidate := &test.Groups[index]
		if candidate.ScriptID == group.ScriptID && strings.TrimSpace(candidate.Name) == groupName {
			return candidate
		}
	}
	var match *TestGroup
	count := 0
	for index := range test.Groups {
		if test.Groups[index].ScriptID == group.ScriptID {
			match = &test.Groups[index]
			count++
		}
	}
	if count == 1 {
		return match
	}
	return nil
}

func validateRevisionSourceGroupIndexes(groups []CreateGroupEntry, source *ManagedTest) error {
	if source == nil || len(groups) == 0 {
		return nil
	}
	used := make(map[int]struct{}, len(groups))
	for index, group := range groups {
		if group.RevisionNewGroup {
			if group.RevisionSourceGroupIndex != nil {
				return fmt.Errorf("group[%d]: revision_new_group cannot be combined with revision_source_group_index", index)
			}
			continue
		}
		if group.RevisionSourceGroupIndex == nil {
			return fmt.Errorf("group[%d]: exactly one of revision_source_group_index or revision_new_group is required", index)
		}
		sourceIndex := *group.RevisionSourceGroupIndex
		if sourceIndex < 0 || sourceIndex >= len(source.Groups) {
			return fmt.Errorf("group[%d]: revision_source_group_index %d is out of range", index, sourceIndex)
		}
		if source.Groups[sourceIndex].ScriptID != group.ScriptID {
			return fmt.Errorf("group[%d]: revision source group %d belongs to script %q, not %q", index, sourceIndex, source.Groups[sourceIndex].ScriptID, group.ScriptID)
		}
		if _, duplicate := used[sourceIndex]; duplicate {
			return fmt.Errorf("group[%d]: revision source group %d is referenced more than once", index, sourceIndex)
		}
		used[sourceIndex] = struct{}{}
	}
	return nil
}

func allRevisionGroupsHaveRuntimeSnapshots(test *ManagedTest, groups []CreateGroupEntry) bool {
	if test == nil || len(groups) == 0 {
		return false
	}
	for _, group := range groups {
		match := existingManagedTestGroup(test, group)
		if match == nil || match.script == nil {
			return false
		}
	}
	return true
}

func intPointersEqual(left, right *int) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func sameRevisionDataPoolBindings(left, right []storage.ScenarioDataPoolBinding) bool {
	leftByAlias, leftOK := revisionDataPoolIntentByAlias(left)
	rightByAlias, rightOK := revisionDataPoolIntentByAlias(right)
	if !leftOK || !rightOK || len(leftByAlias) != len(rightByAlias) {
		return false
	}
	for aliasKey, leftIntent := range leftByAlias {
		if rightIntent, found := rightByAlias[aliasKey]; !found || rightIntent != leftIntent {
			return false
		}
	}
	return true
}

type revisionDataPoolIntent struct {
	PoolID            string
	AccessMode        string
	SelectionStrategy string
}

func revisionDataPoolIntentByAlias(bindings []storage.ScenarioDataPoolBinding) (map[string]revisionDataPoolIntent, bool) {
	// A queued source may predate the read-only authoring contract. Its
	// read_write/consume labels described behavior the engine never implemented;
	// compare that historical intent as the read-only snapshot it actually was.
	normalized, err := normalizeLegacyDataPoolBindingIntent(bindings)
	if err != nil {
		return nil, false
	}
	byAlias := make(map[string]revisionDataPoolIntent, len(normalized))
	for _, binding := range normalized {
		aliasKey := strings.ToLower(binding.Alias)
		if _, duplicate := byAlias[aliasKey]; duplicate {
			return nil, false
		}
		byAlias[aliasKey] = revisionDataPoolIntent{
			PoolID:            binding.PoolID,
			AccessMode:        binding.AccessMode,
			SelectionStrategy: binding.SelectionStrategy,
		}
	}
	return byAlias, true
}

func revisionSharedDataSourceSnapshot(test *ManagedTest) ([]dsl.DataSource, bool) {
	if test == nil {
		return nil, false
	}
	if len(test.DataPools) == 0 {
		return nil, true
	}
	for index := range test.Groups {
		if len(test.Groups[index].sharedDataSources) > 0 {
			return cloneDataSources(test.Groups[index].sharedDataSources), true
		}
	}
	return nil, false
}

func runtimeSettingsIterationValue(settings *RuntimeSettingsOverride) (int, bool) {
	if settings == nil {
		return 0, false
	}
	if settings.RunLogic != nil {
		return settings.RunLogic.Iterations, true
	}
	if settings.Iterations != nil {
		return *settings.Iterations, true
	}
	return 0, false
}

func setRuntimeSettingsIteration(settings *RuntimeSettingsOverride, iterations int) *RuntimeSettingsOverride {
	result := cloneRuntimeSettings(settings)
	if result == nil {
		result = &RuntimeSettingsOverride{}
	}
	if result.RunLogic != nil {
		result.RunLogic = cloneRunLogicConfig(result.RunLogic)
		result.RunLogic.Iterations = iterations
		result.Iterations = nil
	} else {
		value := iterations
		result.Iterations = &value
	}
	return result
}

func validateScenarioRequest(req CreateTestRequest) (string, error) {
	return validateScenarioRequestWithRevisionSource(req, nil)
}

// Only the build path supplies the canonical queued source admitted by
// revisionSourceTestLocked. A client-provided source ID alone never authorizes
// the explicit zero-delay clear. Both final build fences recheck that source.
func validateScenarioRequestWithRevisionSource(req CreateTestRequest, revisionSource *ManagedTest) (string, error) {
	if _, err := parseTerminationDuration(req.Duration, req.TerminationMode); req.Duration != "" && err != nil {
		return "", err
	}
	if err := validateIterationRequest(req); err != nil {
		return "", err
	}
	scenarioMode := normalizeScenarioMode(req.ScenarioMode)
	if req.GoalConfig != nil {
		if value := req.GoalConfig.TransactionResponseTimeSLAMs; value != nil && (!finiteFloat(*value) || *value <= 0) {
			return "", fmt.Errorf("transaction_response_time_sla_ms must be greater than 0")
		}
		if err := ValidateTransactionSLARules(req.GoalConfig.TransactionRules); err != nil {
			return "", fmt.Errorf("invalid goal_config: %w", err)
		}
		if err := validateEarlyStopConfig(req.GoalConfig.EarlyStop); err != nil {
			return "", err
		}
		if err := validateWorkloadShapeConfig(req.GoalConfig.WorkloadShapeConfig, scenarioMode, req.Groups, req.GoalConfig.TargetAverageTPS); err != nil {
			return "", err
		}
	}
	for index, group := range req.Groups {
		if value := group.TransactionResponseTimeSLAMs; value != nil && (!finiteFloat(*value) || *value <= 0) {
			return "", fmt.Errorf("group[%d].transaction_response_time_sla_ms must be greater than 0", index)
		}
	}
	if strings.TrimSpace(req.Duration) != "" {
		duration, err := time.ParseDuration(req.Duration)
		if err != nil {
			return "", fmt.Errorf("invalid duration: %w", err)
		}
		if duration <= 0 {
			return "", fmt.Errorf("duration must be greater than zero")
		}
	}
	if _, err := normalizeGeneratorAssignmentSettings(req.Assignment); err != nil {
		return "", err
	}
	if req.StartDelay != "" {
		startDelay, err := time.ParseDuration(req.StartDelay)
		if err != nil {
			return "", fmt.Errorf("invalid start_delay: %w", err)
		}
		completionMode := dsl.CanonicalTerminationMode(req.TerminationMode)
		clearAdmitted := req.StartDelay == "0s" && revisionSource != nil &&
			strings.TrimSpace(req.RevisionSourceTestID) == revisionSource.ID && revisionSource.ID != "" &&
			normalizeProjectID(req.ProjectID) == normalizeProjectID(revisionSource.ProjectID) &&
			revisionSource.Status == "QUEUED" && revisionSource.StartedAt == nil && revisionSource.EndedAt == nil &&
			(completionMode == dsl.TerminationDuration || completionMode == dsl.TerminationIterations) &&
			dsl.CanonicalTerminationMode(revisionSource.TerminationMode) == completionMode
		if startDelay <= 0 && !clearAdmitted {
			return "", fmt.Errorf("start_delay must be greater than zero except an admitted queued revision's explicit 0s clear")
		}
	}
	if scenarioMode != "goal_oriented" {
		return scenarioMode, nil
	}

	if req.GoalConfig == nil || !hasGoalConfig(req.GoalConfig) {
		return "", fmt.Errorf("goal-oriented scenarios require at least one goal or guardrail setting")
	}
	if req.GoalConfig.TargetAverageTPS != nil && (!finiteFloat(*req.GoalConfig.TargetAverageTPS) || *req.GoalConfig.TargetAverageTPS <= 0) {
		return "", fmt.Errorf("goal-oriented scenarios require target_average_tps to be greater than 0")
	}
	if req.GoalConfig.P95LatencyMs != nil && (!finiteFloat(*req.GoalConfig.P95LatencyMs) || *req.GoalConfig.P95LatencyMs <= 0) {
		return "", fmt.Errorf("goal-oriented scenarios require p95_latency_ms to be greater than 0")
	}
	if req.GoalConfig.MaxErrorPct != nil && (!finiteFloat(*req.GoalConfig.MaxErrorPct) || *req.GoalConfig.MaxErrorPct < 0 || *req.GoalConfig.MaxErrorPct > 100) {
		return "", fmt.Errorf("goal-oriented scenarios require max_error_pct to be between 0 and 100")
	}
	if req.GoalConfig.MaxAvgServerProcessingMs != nil && (!finiteFloat(*req.GoalConfig.MaxAvgServerProcessingMs) || *req.GoalConfig.MaxAvgServerProcessingMs <= 0) {
		return "", fmt.Errorf("goal-oriented scenarios require max_avg_server_processing_ms to be greater than 0")
	}
	if req.GoalConfig.MaxAvgTransferMs != nil && (!finiteFloat(*req.GoalConfig.MaxAvgTransferMs) || *req.GoalConfig.MaxAvgTransferMs <= 0) {
		return "", fmt.Errorf("goal-oriented scenarios require max_avg_transfer_ms to be greater than 0")
	}
	if req.GoalConfig.MinVUsers != nil && *req.GoalConfig.MinVUsers < 1 {
		return "", fmt.Errorf("goal-oriented scenarios require min_vusers to be greater than 0")
	}
	if req.GoalConfig.MaxVUsers != nil && *req.GoalConfig.MaxVUsers < 1 {
		return "", fmt.Errorf("goal-oriented scenarios require max_vusers to be greater than 0")
	}
	if req.GoalConfig.MinVUsers != nil && *req.GoalConfig.MinVUsers > maxManagedTestVUsers {
		return "", fmt.Errorf("goal-oriented scenarios require min_vusers to be at most %d", maxManagedTestVUsers)
	}
	if req.GoalConfig.MaxVUsers != nil && *req.GoalConfig.MaxVUsers > maxManagedTestVUsers {
		return "", fmt.Errorf("goal-oriented scenarios require max_vusers to be at most %d", maxManagedTestVUsers)
	}
	if req.GoalConfig.MinVUsers != nil && req.GoalConfig.MaxVUsers != nil && *req.GoalConfig.MinVUsers > *req.GoalConfig.MaxVUsers {
		return "", fmt.Errorf("goal-oriented scenarios require min_vusers to be less than or equal to max_vusers")
	}
	if req.GoalConfig.MaxErrorCount != nil && *req.GoalConfig.MaxErrorCount < 1 {
		return "", fmt.Errorf("goal-oriented scenarios require max_error_count to be greater than 0")
	}
	if req.GoalConfig.MaxLCPMs != nil && (!finiteFloat(*req.GoalConfig.MaxLCPMs) || *req.GoalConfig.MaxLCPMs <= 0) {
		return "", fmt.Errorf("goal-oriented scenarios require max_lcp_ms to be greater than 0")
	}
	if req.GoalConfig.MaxINPMs != nil && (!finiteFloat(*req.GoalConfig.MaxINPMs) || *req.GoalConfig.MaxINPMs <= 0) {
		return "", fmt.Errorf("goal-oriented scenarios require max_inp_ms to be greater than 0")
	}
	if req.GoalConfig.MaxCLS != nil && (!finiteFloat(*req.GoalConfig.MaxCLS) || *req.GoalConfig.MaxCLS <= 0) {
		return "", fmt.Errorf("goal-oriented scenarios require max_cls to be greater than 0")
	}
	if req.GoalConfig.MaxThrottledRequests != nil && *req.GoalConfig.MaxThrottledRequests < 0 {
		return "", fmt.Errorf("goal-oriented scenarios require max_throttled_requests to be zero or greater")
	}
	if req.GoalConfig.MaxEndToEndLatencyMs != nil && (!finiteFloat(*req.GoalConfig.MaxEndToEndLatencyMs) || *req.GoalConfig.MaxEndToEndLatencyMs <= 0) {
		return "", fmt.Errorf("goal-oriented scenarios require max_end_to_end_latency_ms to be greater than 0")
	}
	percentageThresholds := []struct {
		name  string
		value *float64
	}{
		{name: "max_partition_skew_pct", value: req.GoalConfig.MaxPartitionSkewPct},
		{name: "max_redelivery_rate_pct", value: req.GoalConfig.MaxRedeliveryRatePct},
		{name: "max_dead_letter_rate_pct", value: req.GoalConfig.MaxDeadLetterRatePct},
	}
	for _, threshold := range percentageThresholds {
		if threshold.value != nil && (!finiteFloat(*threshold.value) || *threshold.value < 0 || *threshold.value > 100) {
			return "", fmt.Errorf("goal-oriented scenarios require %s to be between 0 and 100", threshold.name)
		}
	}
	if req.GoalConfig.SLABreachWindow != "" {
		window, err := time.ParseDuration(req.GoalConfig.SLABreachWindow)
		if err != nil {
			return "", fmt.Errorf("invalid sla_breach_window: %w", err)
		}
		if window <= 0 {
			return "", fmt.Errorf("sla_breach_window must be greater than zero")
		}
	}
	if req.GoalConfig.StopOnSLABreach && !hasGoalSLAThreshold(req.GoalConfig) {
		return "", fmt.Errorf("goal-oriented scenarios require an SLA threshold when stop_on_sla_breach is enabled")
	}
	if req.GoalConfig.StopOnMaxErrors && req.GoalConfig.MaxErrorCount == nil {
		return "", fmt.Errorf("goal-oriented scenarios require max_error_count when stop_on_max_errors is enabled")
	}
	return scenarioMode, nil
}

func validateEarlyStopConfig(config *EarlyStopConfig) error {
	if config == nil {
		return nil
	}
	if !finiteFloat(config.ErrorRatePct) || config.ErrorRatePct < 0 || config.ErrorRatePct > 100 {
		return fmt.Errorf("early_stop.error_rate_pct must be between 0 and 100")
	}
	window, err := time.ParseDuration(strings.TrimSpace(config.BreachWindow))
	if err != nil {
		return fmt.Errorf("invalid early_stop.breach_window: %w", err)
	}
	if window <= 0 {
		return fmt.Errorf("early_stop.breach_window must be greater than zero")
	}
	return nil
}

func validateWorkloadShapeConfig(config *WorkloadShapeConfig, scenarioMode string, groups []CreateGroupEntry, targetAverageTPS *float64) error {
	if config == nil {
		return nil
	}
	configuredShapes := 0
	if config.SteppedRamp != nil {
		configuredShapes++
	}
	if config.ClosedVUser != nil {
		configuredShapes++
	}
	if config.GoalDriven != nil {
		configuredShapes++
	}
	if configuredShapes > 1 {
		return fmt.Errorf("workload_shape_config cannot contain multiple guided workload shapes")
	}
	if config.GoalDriven != nil {
		if normalizeScenarioMode(scenarioMode) != "goal_oriented" {
			return fmt.Errorf("workload_shape_config.goal_driven requires scenario_mode goal_oriented")
		}
		groupCount := len(groups)
		if groupCount == 0 {
			groupCount = 1
		}
		if len(config.GoalDriven.Groups) != groupCount {
			return fmt.Errorf("workload_shape_config.goal_driven.groups must contain exactly one definition for each of %d groups", groupCount)
		}
		totalTPS := 0.0
		for index := 0; index < groupCount; index++ {
			key := fmt.Sprintf("group_%d", index)
			definition, ok := config.GoalDriven.Groups[key]
			if !ok {
				return fmt.Errorf("workload_shape_config.goal_driven.groups.%s is required", key)
			}
			if !finiteFloat(definition.TargetTPS) || definition.TargetTPS <= 0 {
				return fmt.Errorf("workload_shape_config.goal_driven.groups.%s.target_tps must be greater than zero", key)
			}
			totalTPS += definition.TargetTPS
			if definition.ART != nil {
				if definition.ART.Basis != "manual" && definition.ART.Basis != "baseline" {
					return fmt.Errorf("workload_shape_config.goal_driven.groups.%s.art.basis must be manual or baseline", key)
				}
				if !finiteFloat(definition.ART.ARTMs) || definition.ART.ARTMs <= 0 {
					return fmt.Errorf("workload_shape_config.goal_driven.groups.%s.art.art_ms must be greater than zero", key)
				}
				if definition.ART.Basis == "baseline" && strings.TrimSpace(definition.ART.BaselineTestID) == "" {
					return fmt.Errorf("workload_shape_config.goal_driven.groups.%s.art.baseline_test_id is required for baseline ART", key)
				}
			}
		}
		if targetAverageTPS == nil || !finiteFloat(*targetAverageTPS) || math.Abs(*targetAverageTPS-totalTPS) > 1e-6 {
			return fmt.Errorf("goal_config.target_average_tps must equal the sum of goal-driven group targets")
		}
		return nil
	}
	if config.ClosedVUser == nil {
		return nil
	}
	if config.SteppedRamp != nil {
		return fmt.Errorf("workload_shape_config cannot contain both closed_vuser and stepped_ramp")
	}
	if normalizeScenarioMode(scenarioMode) != "vuser_driven" {
		return fmt.Errorf("workload_shape_config.closed_vuser requires scenario_mode vuser_driven")
	}
	closed := config.ClosedVUser
	if closed.Scope != "scenario" && closed.Scope != "group" {
		return fmt.Errorf("workload_shape_config.closed_vuser.scope must be scenario or group")
	}
	definitions := make(map[string]ClosedVUserDefinition)
	if closed.Scope == "scenario" {
		if closed.Scenario == nil {
			return fmt.Errorf("workload_shape_config.closed_vuser.scenario is required at scenario scope")
		}
		definitions["scenario"] = *closed.Scenario
	} else {
		if len(closed.Groups) == 0 {
			return fmt.Errorf("workload_shape_config.closed_vuser.groups is required at group scope")
		}
		groupCount := len(groups)
		if groupCount == 0 {
			groupCount = 1
		}
		if len(closed.Groups) != groupCount {
			return fmt.Errorf("workload_shape_config.closed_vuser.groups must contain exactly one definition for each of %d groups", groupCount)
		}
		for index := 0; index < groupCount; index++ {
			key := fmt.Sprintf("group_%d", index)
			if _, ok := closed.Groups[key]; !ok {
				return fmt.Errorf("workload_shape_config.closed_vuser.groups.%s is required", key)
			}
		}
		for name, definition := range closed.Groups {
			definitions[name] = definition
		}
	}
	for name, definition := range definitions {
		if definition.PeakVUsers < 1 || definition.PeakVUsers > maxManagedTestVUsers {
			return fmt.Errorf("workload_shape_config.closed_vuser.%s.peak_vusers must be between 1 and %d", name, maxManagedTestVUsers)
		}
		if duration, err := time.ParseDuration(strings.TrimSpace(definition.RampUp)); err != nil || duration <= 0 {
			return fmt.Errorf("workload_shape_config.closed_vuser.%s.ramp_up must be a positive duration", name)
		}
		if duration, err := time.ParseDuration(strings.TrimSpace(definition.Sustain)); err != nil || duration <= 0 {
			return fmt.Errorf("workload_shape_config.closed_vuser.%s.sustain must be a positive duration", name)
		}
		if definition.RampDownEnabled {
			if duration, err := time.ParseDuration(strings.TrimSpace(definition.RampDown)); err != nil || duration <= 0 {
				return fmt.Errorf("workload_shape_config.closed_vuser.%s.ramp_down must be a positive duration when enabled", name)
			}
		}
	}
	return nil
}

func finiteFloat(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func hasGoalSLAThreshold(config *GoalConfig) bool {
	return config != nil && (config.P95LatencyMs != nil ||
		config.MaxErrorPct != nil ||
		config.MaxAvgServerProcessingMs != nil ||
		config.MaxAvgTransferMs != nil ||
		config.MaxLCPMs != nil ||
		config.MaxINPMs != nil ||
		config.MaxCLS != nil ||
		config.MaxThrottledRequests != nil ||
		config.MaxEndToEndLatencyMs != nil ||
		config.MaxPartitionSkewPct != nil ||
		config.MaxRedeliveryRatePct != nil ||
		config.MaxDeadLetterRatePct != nil)
}

func normalizeCreateTestGroups(req CreateTestRequest) ([]CreateGroupEntry, error) {
	if len(req.Groups) > 0 {
		if len(req.Groups) > maxManagedTestGroups {
			return nil, fmt.Errorf("test contains %d groups; limit is %d", len(req.Groups), maxManagedTestGroups)
		}
		return req.Groups, nil
	}
	if req.ScriptID != "" {
		return []CreateGroupEntry{{
			ScriptID: req.ScriptID,
			VUsers:   req.VUsers,
			RampUp:   req.RampUp,
		}}, nil
	}
	return nil, fmt.Errorf("at least one group (or script_id) is required")
}

func (tr *TestRunner) compileManagedTestGroupsLocked(groups []CreateGroupEntry, runtimeSettings *RuntimeSettingsOverride) ([]TestGroup, []engine.GroupConfig, int, error) {
	return tr.compileManagedTestGroupsForModeWithDataSourcesLocked(groups, runtimeSettings, nil, "vuser_driven")
}

func (tr *TestRunner) compileManagedTestGroupsWithDataSourcesLocked(groups []CreateGroupEntry, runtimeSettings *RuntimeSettingsOverride, sharedDataSources []dsl.DataSource) ([]TestGroup, []engine.GroupConfig, int, error) {
	return tr.compileManagedTestGroupsForModeWithDataSourcesLocked(groups, runtimeSettings, sharedDataSources, "vuser_driven")
}

func (tr *TestRunner) compileManagedTestGroupsForModeWithDataSourcesLocked(groups []CreateGroupEntry, runtimeSettings *RuntimeSettingsOverride, sharedDataSources []dsl.DataSource, scenarioMode string) ([]TestGroup, []engine.GroupConfig, int, error) {
	return tr.compileManagedTestGroupsForModeWithRevisionLocked(groups, runtimeSettings, sharedDataSources, scenarioMode, nil, false)
}

func (tr *TestRunner) compileManagedTestGroupsForModeWithRevisionLocked(groups []CreateGroupEntry, runtimeSettings *RuntimeSettingsOverride, sharedDataSources []dsl.DataSource, scenarioMode string, revisionSource *ManagedTest, revisionDataPoolsChanged bool, projectQuotas ...projectQuotaSnapshot) ([]TestGroup, []engine.GroupConfig, int, error) {
	if len(groups) == 0 {
		return nil, nil, 0, fmt.Errorf("at least one group is required")
	}
	if len(groups) > maxManagedTestGroups {
		return nil, nil, 0, fmt.Errorf("test contains %d groups; limit is %d", len(groups), maxManagedTestGroups)
	}
	testGroups := make([]TestGroup, 0, len(groups))
	engineGroups := make([]engine.GroupConfig, 0, len(groups))
	totalVUsers := 0
	groupNameIndexes := make(map[string]int, len(groups))

	for i, group := range groups {
		revisionGroup := existingManagedTestGroup(revisionSource, group)
		runtimeSettingsUnchanged := revisionGroup != nil &&
			reflect.DeepEqual(mergeRuntimeSettings(runtimeSettings, group.RuntimeSettings), mergeRuntimeSettings(revisionSource.RuntimeSettings, revisionGroup.RuntimeSettings))
		iterationOverrideUnchanged := revisionGroup != nil && revisionGroup.IterationOverrideKnown &&
			intPointersEqual(group.Iterations, revisionGroup.IterationOverride)
		if revisionGroup != nil && !revisionGroup.IterationOverrideKnown {
			// Rollout-era snapshots retained only the effective value, so nil and
			// that same effective value are the only requests that can safely mean
			// "leave this layer untouched". Any required recompile is ambiguous
			// unless the caller supplies a genuinely new compact override.
			iterationOverrideUnchanged = group.Iterations == nil || intPointersEqual(group.Iterations, revisionGroup.Iterations)
		}
		runtimeAlreadyApplied := revisionGroup != nil &&
			revisionGroup.script != nil &&
			!revisionDataPoolsChanged &&
			runtimeSettingsUnchanged &&
			iterationOverrideUnchanged
		if revisionGroup != nil && !revisionGroup.IterationOverrideKnown && !runtimeAlreadyApplied &&
			(group.Iterations == nil || intPointersEqual(group.Iterations, revisionGroup.Iterations)) {
			return nil, nil, 0, fmt.Errorf("group[%d]: revision source lacks iteration-override provenance; create a fresh queued source before changing inherited runtime or data-pool settings", i)
		}
		testGroup, engineGroup, err := tr.compileManagedTestGroupForModeWithRevisionLocked(i, len(groups), group, runtimeSettings, sharedDataSources, scenarioMode, revisionGroup, runtimeAlreadyApplied, projectQuotas...)
		if err != nil {
			return nil, nil, 0, err
		}
		nameKey := normalizeEffectiveGroupName(testGroup.Name)
		if previous, exists := groupNameIndexes[nameKey]; exists {
			return nil, nil, 0, fmt.Errorf("group[%d]: effective group name %q duplicates group[%d] after normalization", i, testGroup.Name, previous)
		}
		groupNameIndexes[nameKey] = i
		testGroups = append(testGroups, testGroup)
		engineGroups = append(engineGroups, engineGroup)
		if nonNegativeSumExceedsLimit(totalVUsers, testGroup.VUsers, maxManagedTestVUsers) {
			return nil, nil, 0, fmt.Errorf("test exceeds the hard workload limit of %d VUsers", maxManagedTestVUsers)
		}
		totalVUsers += testGroup.VUsers
	}

	return testGroups, engineGroups, totalVUsers, nil
}

// normalizeEffectiveGroupName mirrors the case-insensitive, human-facing use of
// group names throughout metrics, scaling, and distributed control. Collapsing
// whitespace prevents visually identical names from creating ambiguous keys.
func normalizeEffectiveGroupName(name string) string {
	return strings.ToLower(strings.Join(strings.Fields(name), " "))
}

func (tr *TestRunner) compileManagedTestGroupLocked(index int, totalGroups int, group CreateGroupEntry, runtimeSettings *RuntimeSettingsOverride, sharedDataSources []dsl.DataSource) (TestGroup, engine.GroupConfig, error) {
	return tr.compileManagedTestGroupForModeLocked(index, totalGroups, group, runtimeSettings, sharedDataSources, "vuser_driven")
}

func (tr *TestRunner) compileManagedTestGroupForModeLocked(index int, totalGroups int, group CreateGroupEntry, runtimeSettings *RuntimeSettingsOverride, sharedDataSources []dsl.DataSource, scenarioMode string) (TestGroup, engine.GroupConfig, error) {
	return tr.compileManagedTestGroupForModeWithRevisionLocked(index, totalGroups, group, runtimeSettings, sharedDataSources, scenarioMode, nil, false)
}

func (tr *TestRunner) compileManagedTestGroupForModeWithRevisionLocked(index int, totalGroups int, group CreateGroupEntry, runtimeSettings *RuntimeSettingsOverride, sharedDataSources []dsl.DataSource, scenarioMode string, revisionGroup *TestGroup, runtimeAlreadyApplied bool, projectQuotas ...projectQuotaSnapshot) (TestGroup, engine.GroupConfig, error) {
	script, ok := tr.scripts[group.ScriptID]
	if revisionGroup != nil && revisionGroup.script != nil {
		runtimeScript := revisionGroup.script
		if !runtimeAlreadyApplied {
			if revisionGroup.baseScript == nil {
				return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: revision source lacks an immutable base script required for runtime or data-pool edits", index)
			}
			runtimeScript = revisionGroup.baseScript
		}
		clonedScript := cloneScriptForStoredGroup(runtimeScript)
		if clonedScript != nil {
			script = &StoredScript{
				ID:        group.ScriptID,
				Name:      firstNonEmpty(revisionGroup.ScriptName, clonedScript.Name),
				Protocol:  firstNonEmpty(revisionGroup.Protocol, clonedScript.Protocol),
				Version:   clonedScript.Version,
				ProjectID: clonedScript.ProjectID,
				Script:    clonedScript,
			}
			ok = true
		}
	}
	if !ok {
		return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: script not found: %s", index, group.ScriptID)
	}
	if issues := launchReviewGateIssues(script.Script); len(issues) > 0 && !group.preserveJMeterDraft {
		return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: %s", index, strings.Join(issues, "; "))
	}

	role, err := normalizeGroupRole(group.Role, script.Protocol, index)
	if err != nil {
		return TestGroup{}, engine.GroupConfig{}, err
	}

	vusers := group.VUsers
	if vusers < 0 {
		return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: vusers must be zero or greater", index)
	}
	if vusers > maxManagedTestVUsers {
		return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: vusers cannot exceed %d", index, maxManagedTestVUsers)
	}
	allowStageTargetTPS := normalizeScenarioMode(scenarioMode) != "vuser_driven"
	derivedRampUp, derivedRampDown, derivedVUsers, err := loadStageCompatibility(group.Stages, index, allowStageTargetTPS)
	if err != nil {
		return TestGroup{}, engine.GroupConfig{}, err
	}
	if vusers <= 0 && derivedVUsers > 0 {
		vusers = derivedVUsers
	}
	if vusers <= 0 {
		vusers = 10
		if role == GroupRoleObserver {
			vusers = observerDefaultVUsers
		}
	}
	if vusers > maxManagedTestVUsers {
		return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: vusers cannot exceed %d", index, maxManagedTestVUsers)
	}
	if role == GroupRoleObserver && vusers > observerMaxVUsers {
		return TestGroup{}, engine.GroupConfig{}, fmt.Errorf(
			"group[%d]: observer groups are diagnostic experience canaries capped at %d VUsers (got %d); use a load-role group for browser load",
			index, observerMaxVUsers, vusers)
	}
	rampUpValue := firstNonEmpty(group.RampUp, derivedRampUp)
	rampDownValue := firstNonEmpty(group.RampDown, derivedRampDown)
	stageDriven := normalizeScenarioMode(scenarioMode) == "vuser_driven" && len(group.Stages) > 0
	if len(group.Stages) > 0 && derivedVUsers > 0 && vusers != derivedVUsers {
		return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: vusers must match the maximum target_vusers from stages (%d)", index, derivedVUsers)
	}
	if group.Iterations != nil && *group.Iterations < 1 {
		return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: iterations must be greater than 0", index)
	}
	if group.RuntimeSettings != nil {
		if group.RuntimeSettings.RunLogic == nil && group.RuntimeSettings.Iterations != nil && *group.RuntimeSettings.Iterations < 1 {
			return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: runtime_settings.iterations must be greater than 0", index)
		}
		if group.RuntimeSettings.RunLogic != nil && group.RuntimeSettings.RunLogic.Iterations < 0 {
			return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: runtime_settings.run_logic.iterations must be zero or greater", index)
		}
		if group.RuntimeSettings.RunLogic != nil && group.RuntimeSettings.Iterations != nil && group.RuntimeSettings.RunLogic.Iterations != *group.RuntimeSettings.Iterations {
			return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: runtime_settings.iterations conflicts with runtime_settings.run_logic.iterations", index)
		}
		if group.Iterations != nil {
			if runtimeIterations, present := runtimeSettingsIterationValue(group.RuntimeSettings); present && runtimeIterations != *group.Iterations {
				return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: iterations conflicts with runtime_settings.run_logic.iterations", index)
			}
		}
		// Canonical sections are authoritative. Remove the legacy alias once its
		// value has been checked so later merges cannot retain two representations.
		if group.RuntimeSettings.RunLogic != nil && group.RuntimeSettings.Iterations != nil {
			group.RuntimeSettings = cloneRuntimeSettings(group.RuntimeSettings)
			group.RuntimeSettings.Iterations = nil
		}
	}
	assignment, err := normalizeGroupGeneratorAssignmentSettings(group.Assignment)
	if err != nil {
		return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: %w", index, err)
	}

	rampUp, err := parseOptionalGroupDuration(rampUpValue, index, "ramp_up")
	if err != nil {
		return TestGroup{}, engine.GroupConfig{}, err
	}
	rampDown, err := parseOptionalGroupDuration(rampDownValue, index, "ramp_down")
	if err != nil {
		return TestGroup{}, engine.GroupConfig{}, err
	}
	startOffset, err := parseOptionalGroupDuration(group.StartOffset, index, "start_offset")
	if err != nil {
		return TestGroup{}, engine.GroupConfig{}, err
	}
	engineVUsers := vusers
	engineRampUp := rampUp
	engineRampDown := rampDown
	allowZeroVUsers := false
	initialVUsers := vusers
	if stageDriven {
		initialVUsers = stagedInitialVUsers(group.Stages, vusers)
		engineVUsers = initialVUsers
		engineRampUp = 0
		engineRampDown = 0
		allowZeroVUsers = initialVUsers == 0
	}

	name := group.Name
	if name == "" {
		name = script.Name
		if totalGroups > 1 {
			name = fmt.Sprintf("%s (#%d)", script.Name, index+1)
		}
	}

	initStrategy := strings.ToLower(strings.TrimSpace(group.InitStrategy))
	if initStrategy == "" {
		initStrategy = "at_runtime"
	}
	switch initStrategy {
	case "at_runtime", "at_once":
	default:
		return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: initialization must be at_runtime or at_once", index)
	}
	effectiveRuntimeSettings := mergeRuntimeSettings(runtimeSettings, group.RuntimeSettings)
	// Resolve continue-on-error from the pristine script, before any runtime
	// override has overwritten config.error_handling — otherwise a group override
	// would be indistinguishable from the script's own intent. On a revision that
	// reuses its already-compiled script, that script carries the value a previous
	// compile stamped into it, so read the immutable authoring base instead; using
	// the compiled copy would feed this resolution its own output and report the
	// script as the decision owner for a group that inherited the scenario default.
	policyScript := script.Script
	if runtimeAlreadyApplied && revisionGroup != nil && revisionGroup.baseScript != nil {
		policyScript = revisionGroup.baseScript
	}
	continueOnError := resolveGroupContinueOnError(runtimeSettings, group.RuntimeSettings, policyScript)
	if group.Iterations != nil {
		effectiveRuntimeSettings = setRuntimeSettingsIteration(effectiveRuntimeSettings, *group.Iterations)
	}
	var effectiveIterations *int
	if iterations, present := runtimeSettingsIterationValue(effectiveRuntimeSettings); present && iterations > 0 {
		effectiveIterations = &iterations
	}

	testGroup := TestGroup{
		Name:                         name,
		ScriptID:                     group.ScriptID,
		ScriptName:                   script.Name,
		Protocol:                     script.Protocol,
		Role:                         role,
		VUsers:                       vusers,
		StartOffset:                  group.StartOffset,
		RampUp:                       rampUpValue,
		RampDown:                     rampDownValue,
		Stages:                       cloneLoadStages(group.Stages),
		Iterations:                   effectiveIterations,
		IterationOverride:            cloneIntPtr(group.Iterations),
		IterationOverrideKnown:       true,
		RuntimeSettings:              nil,
		Assignment:                   cloneGeneratorAssignmentSettings(assignment),
		RunLogicGoals:                runLogicGoalSnapshot(script.Script),
		Initialization:               initStrategy,
		TransactionResponseTimeSLAMs: cloneFloat64Ptr(group.TransactionResponseTimeSLAMs),
		ContinueOnError:              &continueOnError,
		rampUpDur:                    engineRampUp,
		stageDriven:                  stageDriven,
		initialVUsers:                initialVUsers,
		currentVUsers:                initialVUsers,
	}

	scriptCopy := *script.Script
	credentialContextBase := cloneScriptDefinition(script.Script)
	// Runtime HAR capture and retention must use the same centrally managed
	// directory. Override any script-supplied path before local or distributed
	// execution so scripts cannot select arbitrary filesystem locations.
	scriptCopy.Config.Browser.HARArtifactDir = strings.TrimSpace(tr.settings.ArchiveRetention.HARArtifactDir)
	var baseScriptSnapshot *dsl.Script
	if revisionGroup != nil {
		baseScriptSnapshot = cloneScriptForStoredGroup(revisionGroup.baseScript)
	} else {
		// Persist an authoring base independently of shared pool rows and runtime
		// overrides. Resolve object-repository selectors now so a later revision
		// never consults a mutable repository merely because pacing/logging changed.
		baseCandidate := cloneScriptForStoredGroup(&scriptCopy)
		if baseCandidate != nil {
			if resolved, _, refIssues := resolveObjectRefs(tr.objectRepoStore, baseCandidate); resolved != nil && len(refIssues) == 0 {
				baseScriptSnapshot = cloneScriptForStoredGroup(resolved)
			} else if len(refIssues) == 0 {
				baseScriptSnapshot = baseCandidate
			} else {
				slog.Warn("immutable revision base unavailable because object references require runtime resolution", "group", index, "issues", strings.Join(refIssues, "; "))
			}
		}
	}
	if !runtimeAlreadyApplied && len(sharedDataSources) > 0 {
		scriptCopy.DataSources = append(cloneDataSources(scriptCopy.DataSources), sharedDataSources...)
	}
	runtimeScope := strings.TrimSpace(group.runtimeSecretScope)
	if runtimeScope == "" {
		runtimeScope = "ephemeral:" + strings.TrimSpace(group.ScriptID)
	}
	protectedRootSettings, rootRuntimeSecretValues, err := protectRuntimeOverrideSecrets(runtimeScope, -1, &scriptCopy, runtimeSettings, true)
	if err != nil {
		return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: protect root runtime credentials: %w", index, err)
	}
	protectedGroupSettings, groupRuntimeSecretValues, err := protectRuntimeOverrideSecrets(runtimeScope, index, &scriptCopy, group.RuntimeSettings, true)
	if err != nil {
		return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: protect group runtime credentials: %w", index, err)
	}
	executionRuntimeSettings := mergeRuntimeSettings(protectedRootSettings, protectedGroupSettings)
	if effectiveIterations != nil {
		executionRuntimeSettings = setRuntimeSettingsIteration(executionRuntimeSettings, *effectiveIterations)
	}
	runtimeSecretValues := cloneRuntimeSecretValues(rootRuntimeSecretValues)
	if runtimeSecretValues == nil && len(groupRuntimeSecretValues) > 0 {
		runtimeSecretValues = make(map[string]string, len(groupRuntimeSecretValues))
	}
	for secretName, secretValue := range groupRuntimeSecretValues {
		runtimeSecretValues[secretName] = secretValue
	}
	if executionRuntimeSettings != nil && !runtimeAlreadyApplied {
		if err := applyRuntimeOverrides(&scriptCopy, executionRuntimeSettings); err != nil {
			return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: apply runtime overrides: %w", index, err)
		}
		if isCredentialPreservingImport(credentialContextBase) {
			if err := validatePreservedImportCredentialEdits(
				&scriptCopy,
				credentialContextBase,
			); err != nil {
				return TestGroup{}, engine.GroupConfig{}, fmt.Errorf(
					"group[%d]: runtime override is unsafe for retained imported credentials: %w",
					index,
					err,
				)
			}
		}
		if issues := validateStoredExecutionScript(&scriptCopy); len(issues) > 0 {
			return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: %s", index, strings.Join(issues, "; "))
		}
	}
	// The scenario's continue-on-error policy is applied after the runtime
	// overrides, and only to that one field. A group's error_handling override
	// replaces the section wholesale, so writing the resolved value here is what
	// keeps an unstated continue_on_error from silently reverting to disabled —
	// while retries and snapshot-on-error keep whatever the override set.
	applyContinueOnErrorResolution(&scriptCopy, continueOnError)
	// Resolve object-repository bindings into concrete selectors before the
	// script is distributed — generators have no store, so an unresolved
	// object_ref would be unrunnable on the generator.
	if !runtimeAlreadyApplied {
		if resolved, refWarnings, refIssues := resolveObjectRefs(tr.objectRepoStore, &scriptCopy); resolved != nil {
			if len(refIssues) > 0 {
				return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: %s", index, strings.Join(refIssues, "; "))
			}
			for _, w := range refWarnings {
				slog.Warn("object_ref resolution degraded to inline locator", "group", index, "detail", w)
			}
			scriptCopy = *resolved
		}
	}
	if stageDriven && engine.HasAllocatedParameters(&scriptCopy) {
		return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: stage-driven rescaling cannot preserve unique parameter cursors or observer identities; use a fixed population with a planned ramp", index)
	}
	if stageDriven && dsl.CanonicalWorkloadModel(scriptCopy.Config.RunLogic.WorkloadModel) == "open" {
		return TestGroup{}, engine.GroupConfig{}, fmt.Errorf(
			"group[%d]: vuser-driven stages are not supported for open workload scripts; configure TPS pacing without VUser scale stages",
			index,
		)
	}
	if len(scriptCopy.Actions) == 0 {
		if issues := validateStoredExecutionScript(&scriptCopy); len(issues) > 0 {
			return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: %s", index, strings.Join(issues, "; "))
		}
		return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: script must have at least one action", index)
	}
	if err := validateSuccessfulMessageDetailFeature(&scriptCopy, projectQuotas); err != nil {
		return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: %w", index, err)
	}
	if err := validateControlParallelFeature(&scriptCopy, projectQuotas); err != nil {
		return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: %w", index, err)
	}
	if runtimeAlreadyApplied && revisionGroup != nil {
		testGroup.Iterations = cloneIntPtr(revisionGroup.Iterations)
		testGroup.IterationOverride = cloneIntPtr(revisionGroup.IterationOverride)
		testGroup.IterationOverrideKnown = revisionGroup.IterationOverrideKnown
		if effectiveRuntimeSettings == nil {
			effectiveRuntimeSettings = &RuntimeSettingsOverride{}
		}
		effectiveRuntimeSettings.RedactionRules = cloneRedactionRules(revisionGroup.redactionRules)
	} else {
		if len(projectQuotas) > 0 {
			effectiveRuntimeSettings, err = resolveEffectiveRuntimeSettingsWithProjectQuota(effectiveRuntimeSettings, projectQuotas[0])
		} else {
			effectiveRuntimeSettings, err = tr.resolveEffectiveRuntimeSettings(script.ProjectID, effectiveRuntimeSettings)
		}
		if err != nil {
			return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: resolve redaction rules: %w", index, err)
		}
	}
	// The scenario schedule, not the script, decides when this group's VUsers
	// stop in duration mode. A script-sourced per-VUser
	// iteration cap would retire this group mid-run while the other groups keep
	// loading. testGroup.Iterations is the effective scenario/group intent and is
	// final by this point (the revision branch above restores it), so it is the
	// signal for "the operator asked for a fixed count".
	//
	// A revision reuses an already-compiled snapshot verbatim and therefore skips
	// the Java Vuser approval re-stamp below, so an approved Java snapshot is left
	// byte-identical rather than invalidating its content hash. Only snapshots
	// queued before this rule existed can still carry a cap there; a fresh create
	// of the same scenario applies it.
	_, javaVuserApprovalRetained := readJavaVuserAuthoringApproval(&scriptCopy)
	if !group.iterationCompletion && (!runtimeAlreadyApplied || !javaVuserApprovalRetained) {
		if overriddenCap := applyScheduleIterationAuthority(&scriptCopy, testGroup.Iterations); overriddenCap > 0 {
			testGroup.scheduleOverriddenScriptIterations = overriddenCap
			slog.Info("scenario schedule overrides the script per-vuser iteration cap",
				"group", name, "script_id", group.ScriptID, "script_iterations", overriddenCap)
		}
	}
	if group.iterationCompletion && testGroup.Iterations == nil {
		iterations := scriptCopy.Config.RunLogic.Iterations
		testGroup.Iterations = &iterations
	}
	redactionConfig, err := buildRuntimeRedactionConfig(effectiveRuntimeSettings)
	if err != nil {
		return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: build redaction config: %w", index, err)
	}
	runtimeSecretValues, err = filterReferencedRuntimeSecretValues(&scriptCopy, runtimeSecretValues)
	if err != nil {
		return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: inspect protected runtime credentials", index)
	}
	redactionConfig, err = engine.WithSecretValueRedactionChecked(redactionConfig, runtimeSecretValueList(runtimeSecretValues))
	if err != nil {
		return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: prepare runtime credential redaction: %w", index, err)
	}
	// The runtime copy diverges from the approved stored script whenever
	// runtime overrides (e.g. the Java Env section) or managed rewrites mutate
	// it, which would spuriously trip the launch-time authoring-approval hash
	// check. When the STORED script still carries a valid approval, restamp
	// that session onto the runtime copy with the copy's own content hash —
	// but ONLY for runtime-safe divergence. A scenario override that changes
	// the executable surface (lifecycle actions, or the code-selecting
	// classpath / JDK / additional VM parameters) must NOT be re-blessed under
	// the reviewer's approval; it is rejected here so the change goes back
	// through authoring review. dsl.ValidateScriptIssues above only checks
	// syntax (no classpath allowlist), so it cannot be the governance boundary.
	// The metadata tree is deep-cloned first because scriptCopy shares the
	// stored script's Metadata pointer and stamping in place would corrupt the
	// stored approval.
	if !runtimeAlreadyApplied && scriptRequiresJavaVuserAuthoringApproval(&scriptCopy) {
		if conflict := tr.javaVuserApprovalConflict(script.Script); conflict == "" {
			if evidence, ok := readJavaVuserAuthoringApproval(script.Script); ok {
				if javaVuserOverrideChangedExecutableSurface(script.Script, &scriptCopy) {
					return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: runtime overrides changed the approved Java Vuser classpath, JDK, VM parameters, or actions, or moved it into LoadRunner thread mode; apply these changes in the script and re-approve it through authoring review before launch", index)
				}
				if detached := cloneScriptForStoredGroup(&scriptCopy); detached != nil {
					scriptCopy = *detached
					stampJavaVuserAuthoringApproval(&scriptCopy, evidence.SessionID)
				}
			}
		}
	}
	testGroup.script = &scriptCopy
	testGroup.baseScript = baseScriptSnapshot
	testGroup.sharedDataSources = cloneDataSources(sharedDataSources)
	testGroup.RuntimeSettings = cloneRuntimeSettings(protectedGroupSettings)
	testGroup.redactionRules = cloneRedactionRules(nilIfNoRules(effectiveRuntimeSettings))
	testGroup.runtimeSecretValues = cloneRuntimeSecretValues(runtimeSecretValues)
	secretResolver := overlayRuntimeSecretResolver(runtimeSecretValues, tr.secretResolverForProject(scriptCopy.ProjectID))

	engineGroup := engine.GroupConfig{
		Name:            name,
		StartOffset:     startOffset,
		Script:          &scriptCopy,
		VUsers:          engineVUsers,
		RampUp:          engineRampUp,
		RampDown:        engineRampDown,
		AllowZeroVUsers: allowZeroVUsers,
		InitStrategy:    initStrategy,
		RedactionConfig: redactionConfig,
		SecretResolver:  secretResolver,
	}

	return testGroup, engineGroup, nil
}

func validateSuccessfulMessageDetailFeature(script *dsl.Script, projectQuotas []projectQuotaSnapshot) error {
	if !scriptRequestsSuccessfulMessageDetail(script) {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(script.Protocol)) {
	case "http", "http/1.1", "http/2", "http/3", "soap", "java_http":
	default:
		return fmt.Errorf("successful message detail capture supports native http protocol only (including SOAP and Java-over-HTTP)")
	}
	if len(projectQuotas) == 0 || !projectQuotas[0].features.SuccessfulMessageDetail {
		return fmt.Errorf("successful message detail capture is disabled for this project; enable settings.features.successful_message_detail before launch")
	}
	return nil
}

func validateControlParallelFeature(script *dsl.Script, projectQuotas []projectQuotaSnapshot) error {
	if !scriptRequestsControlParallel(script) {
		return nil
	}
	if len(projectQuotas) == 0 || !projectQuotas[0].features.ControlParallel {
		return fmt.Errorf("control_parallel is disabled for this project; enable settings.features.control_parallel before launch")
	}
	instance, err := protocol.Get(strings.TrimSpace(script.Protocol))
	if err != nil {
		return err
	}
	safety, ok := instance.(protocol.ParallelSafetyProvider)
	if !ok || !safety.ParallelSafe() {
		return fmt.Errorf("protocol %q does not advertise parallel branch safety", script.Protocol)
	}
	return nil
}

func scriptRequestsControlParallel(script *dsl.Script) bool {
	if script == nil {
		return false
	}
	stack := make([]dsl.Action, 0, len(script.Init)+len(script.Actions)+len(script.Teardown))
	stack = append(stack, script.Init...)
	stack = append(stack, script.Actions...)
	stack = append(stack, script.Teardown...)
	for len(stack) > 0 {
		action := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if dsl.CanonicalActionType(strings.ToLower(strings.TrimSpace(action.Type))) == "control_parallel" {
			return true
		}
		if action.Control != nil {
			for _, branch := range action.Control.ActionBranches() {
				stack = append(stack, branch.Actions...)
			}
		}
	}
	return false
}

func (tr *TestRunner) buildSharedDataSourcesLocked(bindings []storage.ScenarioDataPoolBinding) ([]dsl.DataSource, error) {
	if len(bindings) == 0 {
		return nil, nil
	}
	// This is the final executable boundary and deliberately revalidates even
	// though normal create paths canonicalize earlier. Persisted legacy records
	// and future internal callers must not silently degrade read_write/consume
	// into the current read-only in-memory CSV snapshot behavior.
	normalized, err := normalizeDataPoolBindingIntent(bindings)
	if err != nil {
		return nil, fmt.Errorf("invalid executable data pool bindings: %w", err)
	}
	bindings = normalized
	if tr.dataPoolStore == nil {
		return nil, fmt.Errorf("data pool storage is not configured")
	}
	sources := make([]dsl.DataSource, 0, len(bindings))
	for _, binding := range bindings {
		if binding.AccessMode == "live" {
			continue
		}
		alias := strings.TrimSpace(binding.Alias)
		if alias == "" {
			return nil, fmt.Errorf("data pool binding alias is required")
		}
		pool, err := tr.dataPoolStore.GetDataPool(binding.PoolID)
		if err != nil {
			return nil, fmt.Errorf("load data pool %q: %w", alias, err)
		}
		if err := errDataPoolArchived(pool); err != nil {
			return nil, err
		}
		rows, err := tr.dataPoolStore.ListDataPoolRows(binding.PoolID)
		if err != nil {
			return nil, fmt.Errorf("load data pool %q: %w", alias, err)
		}
		if len(rows) == 0 {
			return nil, fmt.Errorf("data pool %q has no rows", alias)
		}
		rows = activeDataPoolRows(rows)
		if len(rows) == 0 {
			return nil, fmt.Errorf("data pool %q has no active rows", alias)
		}
		inlineData, columns, err := inlineCSVFromDataPoolRows(rows)
		if err != nil {
			return nil, fmt.Errorf("prepare data pool %q: %w", alias, err)
		}
		strategy := binding.SelectionStrategy
		if strategy == "" {
			strategy = "sequential"
		}
		sources = append(sources, dsl.DataSource{
			Name:           alias,
			Type:           "csv",
			InlineData:     inlineData,
			Strategy:       strategy,
			FirstRowHeader: true,
			Columns:        columns,
		})
	}
	return sources, nil
}

// validateDataPoolProjectsLocked prevents a binding from turning a project-
// scoped scenario or run into a read primitive for another project's rows.
// Callers hold tr.mu, so validation and compilation observe one controller
// snapshot; the database ownership check remains authoritative.
func (tr *TestRunner) validateDataPoolProjectsLocked(projectID string, bindings []storage.ScenarioDataPoolBinding) error {
	return validateDataPoolProjects(tr.dataPoolStore, projectID, bindings)
}

func validateDataPoolProjects(poolStore dataPoolPersistence, projectID string, bindings []storage.ScenarioDataPoolBinding) error {
	if len(bindings) == 0 {
		return nil
	}
	if poolStore == nil {
		return fmt.Errorf("data pool storage is not configured")
	}
	expectedProjectID := normalizeProjectID(projectID)
	for _, binding := range bindings {
		poolID := strings.TrimSpace(binding.PoolID)
		if binding.Scope == "project" {
			if err := validateProjectLiveTableReference(poolStore, expectedProjectID, binding); err != nil {
				return err
			}
			continue
		}
		if liveTableBindingHasNoPool(binding) || liveTableBindingUsesFrozenSeed(binding) {
			continue
		}
		if poolID == "" {
			return fmt.Errorf("data pool id is required")
		}
		pool, err := poolStore.GetDataPool(poolID)
		if err != nil || pool == nil {
			if err != nil {
				return fmt.Errorf("load data pool %q: %w", poolID, err)
			}
			return fmt.Errorf("data pool %q not found", poolID)
		}
		if normalizeProjectID(pool.ProjectID) != expectedProjectID {
			return fmt.Errorf("data pool %q does not belong to project %q", poolID, expectedProjectID)
		}
	}
	return nil
}

// validateDataPoolsNotArchivedLocked hard-rejects archived pools on authoring
// and run-creation paths. It is deliberately separate from the project check:
// ValidateScenarioLoadModel needs project isolation but must report archived
// pools as a blocked item in the report, not fail the whole validation call.
func (tr *TestRunner) validateDataPoolsNotArchivedLocked(bindings []storage.ScenarioDataPoolBinding) error {
	return validateDataPoolsNotArchived(tr.dataPoolStore, bindings)
}

func validateDataPoolsNotArchived(poolStore dataPoolPersistence, bindings []storage.ScenarioDataPoolBinding) error {
	if len(bindings) == 0 {
		return nil
	}
	if poolStore == nil {
		return fmt.Errorf("data pool storage is not configured")
	}
	for _, binding := range bindings {
		if liveTableBindingHasNoPool(binding) || liveTableBindingUsesFrozenSeed(binding) {
			continue
		}
		pool, err := poolStore.GetDataPool(strings.TrimSpace(binding.PoolID))
		if err != nil || pool == nil {
			// Existence and ownership are the project validator's concern.
			continue
		}
		if err := errDataPoolArchived(pool); err != nil {
			return err
		}
	}
	return nil
}

func inlineCSVFromDataPoolRows(rows []*storage.SharedDataPoolRow) (string, []string, error) {
	columnSet := map[string]struct{}{}
	for _, row := range rows {
		for key := range row.Data {
			key = strings.TrimSpace(key)
			if key != "" {
				columnSet[key] = struct{}{}
			}
		}
	}
	if len(columnSet) == 0 {
		return "", nil, fmt.Errorf("data pool rows must include at least one column")
	}
	columns := make([]string, 0, len(columnSet))
	for key := range columnSet {
		columns = append(columns, key)
	}
	sort.Strings(columns)

	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)
	if err := writer.Write(columns); err != nil {
		return "", nil, err
	}
	for _, row := range rows {
		values := make([]string, len(columns))
		for i, column := range columns {
			values[i] = canonicalDataPoolRowValue(row.Data, column)
		}
		if err := writer.Write(values); err != nil {
			return "", nil, err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return "", nil, err
	}
	return buf.String(), columns, nil
}

func cloneDataSources(sources []dsl.DataSource) []dsl.DataSource {
	if len(sources) == 0 {
		return nil
	}
	out := make([]dsl.DataSource, len(sources))
	copy(out, sources)
	return out
}

func parseOptionalGroupDuration(value string, index int, fieldName string) (time.Duration, error) {
	if strings.TrimSpace(value) == "" {
		return 0, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("group[%d]: invalid %s: %w", index, fieldName, err)
	}
	if duration < 0 {
		return 0, fmt.Errorf("group[%d]: %s must be zero or greater", index, fieldName)
	}
	return duration, nil
}

func loadStageCompatibility(stages []storage.LoadStage, groupIndex int, allowTargetTPS bool) (string, string, int, error) {
	if len(stages) == 0 {
		return "", "", 0, nil
	}
	if len(stages) > maxManagedLoadStagesPerGroup {
		return "", "", 0, fmt.Errorf("group[%d] contains %d stages; limit is %d", groupIndex, len(stages), maxManagedLoadStagesPerGroup)
	}
	var rampUp time.Duration
	var rampDown time.Duration
	maxVUsers := 0
	for index, stage := range stages {
		stageType := strings.ToLower(strings.TrimSpace(stage.Type))
		if stageType == "" {
			return "", "", 0, fmt.Errorf("group[%d].stages[%d]: type is required", groupIndex, index)
		}
		if stageType != "ramp_up" && stageType != "hold" && stageType != "ramp_down" {
			return "", "", 0, fmt.Errorf("group[%d].stages[%d]: type must be ramp_up, hold, or ramp_down", groupIndex, index)
		}
		duration, err := time.ParseDuration(stage.Duration)
		if err != nil {
			return "", "", 0, fmt.Errorf("group[%d].stages[%d]: duration is invalid: %w", groupIndex, index, err)
		}
		if duration <= 0 {
			return "", "", 0, fmt.Errorf("group[%d].stages[%d]: duration must be greater than zero", groupIndex, index)
		}
		if stage.TargetVUsers < 0 {
			return "", "", 0, fmt.Errorf("group[%d].stages[%d]: target_vusers must be zero or greater", groupIndex, index)
		}
		if stage.TargetVUsers > maxManagedTestVUsers {
			return "", "", 0, fmt.Errorf("group[%d].stages[%d]: target_vusers cannot exceed %d", groupIndex, index, maxManagedTestVUsers)
		}
		if stage.TargetTPS != nil && *stage.TargetTPS <= 0 {
			return "", "", 0, fmt.Errorf("group[%d].stages[%d]: target_tps must be greater than zero", groupIndex, index)
		}
		if stage.TargetTPS != nil && !allowTargetTPS {
			return "", "", 0, fmt.Errorf("group[%d].stages[%d]: target_tps is not supported for vuser-driven stages; use target_vusers or a goal-oriented scenario", groupIndex, index)
		}
		if stage.TargetVUsers == 0 && stage.TargetTPS == nil && stageType != "ramp_down" {
			return "", "", 0, fmt.Errorf("group[%d].stages[%d]: target_vusers or target_tps is required", groupIndex, index)
		}
		if stage.TargetVUsers > maxVUsers {
			maxVUsers = stage.TargetVUsers
		}
		switch stageType {
		case "ramp_up":
			if duration > time.Duration(math.MaxInt64)-rampUp {
				return "", "", 0, fmt.Errorf("group[%d]: cumulative ramp_up duration is too large", groupIndex)
			}
			rampUp += duration
		case "ramp_down":
			if duration > time.Duration(math.MaxInt64)-rampDown {
				return "", "", 0, fmt.Errorf("group[%d]: cumulative ramp_down duration is too large", groupIndex)
			}
			rampDown += duration
		}
	}
	return durationStringOrEmpty(rampUp), durationStringOrEmpty(rampDown), maxVUsers, nil
}

func stagedInitialVUsers(stages []storage.LoadStage, peakVUsers int) int {
	if len(stages) == 0 {
		return peakVUsers
	}
	first := stages[0]
	switch strings.ToLower(strings.TrimSpace(first.Type)) {
	case "ramp_up":
		return 0
	case "hold":
		return first.TargetVUsers
	case "ramp_down":
		if peakVUsers > first.TargetVUsers {
			return peakVUsers
		}
		return first.TargetVUsers
	default:
		return peakVUsers
	}
}

func durationStringOrEmpty(duration time.Duration) string {
	if duration <= 0 {
		return ""
	}
	return duration.String()
}

func cloneLoadStages(stages []storage.LoadStage) []storage.LoadStage {
	if len(stages) == 0 {
		return nil
	}
	out := make([]storage.LoadStage, len(stages))
	copy(out, stages)
	for index := range out {
		if stages[index].TargetTPS != nil {
			value := *stages[index].TargetTPS
			out[index].TargetTPS = &value
		}
	}
	return out
}

func deriveManagedTestName(requestName string, groups []TestGroup) string {
	if requestName != "" {
		return requestName
	}
	if len(groups) == 1 {
		return groups[0].ScriptName
	}
	return fmt.Sprintf("Multi-group (%d groups)", len(groups))
}

func (tr *TestRunner) resolveCreateTestProjectID(requestProjectID string, groups []CreateGroupEntry, revisionSource *ManagedTest) (string, error) {
	projectID := strings.TrimSpace(requestProjectID)
	if projectID != "" {
		projectID = normalizeProjectID(projectID)
	}
	for _, group := range groups {
		// A source-indexed revision group is bound to the queued run's immutable
		// script snapshot. The library row may have been deleted or its ID may
		// have been reused in another project; neither event may change the
		// source-bound group's authorization or execution input. Explicitly new
		// revision groups still fall through to the current library lookup.
		if group.RevisionSourceGroupIndex != nil && existingManagedTestGroup(revisionSource, group) != nil {
			continue
		}
		script, ok := tr.scripts[group.ScriptID]
		if !ok {
			continue
		}
		candidate := normalizeProjectID(script.ProjectID)
		if projectID == "" {
			projectID = candidate
			continue
		}
		if candidate != projectID {
			return "", fmt.Errorf("test scripts and requested project must belong to one project")
		}
	}
	return normalizeProjectID(projectID), nil
}

func resolveCreateTestReleaseContextWithEvidence(projectID, requestedApplicationID, requestedReleaseID, status string, evidence createTestReleaseEvidence) (string, string, error) {
	projectID = normalizeProjectID(projectID)
	applicationID := strings.TrimSpace(requestedApplicationID)
	releaseID := strings.TrimSpace(requestedReleaseID)
	explicitRelease := releaseID != ""
	legacyFallback := false

	if !explicitRelease && status != "DRAFT" {
		if applicationID != "" {
			return "", "", fmt.Errorf("application_id requires an explicit release_id for a runnable test")
		}
		// Preserve the long-standing uncategorized-run fallback. It is an
		// internal bucket rather than a caller-supplied cross-project reference:
		// the run's own project's legacy application and release when the store
		// has them. The bare IDs belong to the default project, so a run filed
		// under them elsewhere could not be opened in its workspace.
		releaseID = storage.LegacyReleaseID
		applicationID = storage.LegacyApplicationID
		if evidence.legacyProjectID != "" && evidence.legacyProjectID == projectID {
			releaseID = evidence.legacyReleaseID
			applicationID = evidence.legacyApplicationID
		}
		legacyFallback = true
	}

	// Implicit runnable-test fallback is controller-owned and requires no
	// external identity lookup. A draft with no context is likewise safe without
	// evidence, which keeps internal capacity-only previews lightweight.
	if !explicitRelease && (legacyFallback || applicationID == "") {
		return applicationID, releaseID, nil
	}
	if !evidence.loaded ||
		evidence.requestedApplicationID != strings.TrimSpace(requestedApplicationID) ||
		evidence.requestedReleaseID != strings.TrimSpace(requestedReleaseID) {
		return "", "", fmt.Errorf("release/application evidence changed or is unavailable; retry test creation")
	}

	releaseApplicationID := ""
	releaseProjectID := ""
	if explicitRelease {
		if evidence.storeConfigured {
			if evidence.releaseErr != nil || !evidence.releaseResolved || evidence.releaseID != releaseID {
				return "", "", fmt.Errorf("release not found: %s", releaseID)
			}
			if evidence.releaseArchived {
				return "", "", fmt.Errorf("release is archived: %s", releaseID)
			}
			if applicationID != "" && applicationID != evidence.releaseApplicationID {
				return "", "", fmt.Errorf("application_id does not match release")
			}
			applicationID = evidence.releaseApplicationID
			releaseApplicationID = evidence.releaseApplicationID
			releaseProjectID = evidence.releaseProjectID
		}
	}

	applicationProjectID := ""
	if applicationID != "" && !legacyFallback {
		if evidence.applicationStoreConfigured {
			if evidence.applicationErr != nil || !evidence.applicationResolved || evidence.applicationID != applicationID {
				return "", "", fmt.Errorf("application not found: %s", applicationID)
			}
			applicationProjectID = normalizeProjectID(evidence.applicationProjectID)
		}
	}

	if releaseApplicationID != "" || releaseProjectID != "" {
		if releaseProjectID == "" {
			releaseProjectID = applicationProjectID
		}
		if releaseProjectID != "" && normalizeProjectID(releaseProjectID) != projectID {
			return "", "", fmt.Errorf("release and test scripts must belong to one project")
		}
	}
	if applicationProjectID != "" && applicationProjectID != projectID {
		return "", "", fmt.Errorf("application and test scripts must belong to one project")
	}
	if applicationID == "" && releaseID != "" {
		applicationID = storage.LegacyApplicationID
	}
	return applicationID, releaseID, nil
}

func (tr *TestRunner) resolveEffectiveRuntimeSettings(projectID string, runtimeSettings *RuntimeSettingsOverride) (*RuntimeSettingsOverride, error) {
	effective := cloneRuntimeSettings(runtimeSettings)
	rules, err := tr.resolveEffectiveRedactionRules(projectID, nilIfNoRules(runtimeSettings))
	if err != nil {
		return nil, err
	}
	if len(rules) == 0 {
		return effective, nil
	}
	if effective == nil {
		effective = &RuntimeSettingsOverride{}
	}
	effective.RedactionRules = rules
	return effective, nil
}

func resolveEffectiveRuntimeSettingsWithProjectQuota(runtimeSettings *RuntimeSettingsOverride, quota projectQuotaSnapshot) (*RuntimeSettingsOverride, error) {
	effective := cloneRuntimeSettings(runtimeSettings)
	rules := cloneRedactionRules(quota.redactionRules)
	if runtimeSettings != nil {
		rules = append(cloneRedactionRules(runtimeSettings.RedactionRules), rules...)
	}
	if len(rules) == 0 {
		return effective, nil
	}
	if effective == nil {
		effective = &RuntimeSettingsOverride{}
	}
	effective.RedactionRules = rules
	return effective, nil
}

func (tr *TestRunner) resolveEffectiveRedactionRules(projectID string, explicit []engine.RedactionRule) ([]engine.RedactionRule, error) {
	merged := cloneRedactionRules(explicit)
	if tr.projectStore == nil {
		return merged, nil
	}
	projectID = normalizeProjectID(projectID)
	project, err := tr.projectStore.GetProject(projectID)
	// A project that does not exist contributes no rules. Any other failure,
	// including settings that no longer decode, must not let a run or replay
	// proceed without the project's redaction policy.
	if errors.Is(err, sql.ErrNoRows) {
		return merged, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load redaction rules for project %q: %w", projectID, err)
	}
	if project == nil || len(project.Settings.RedactionRules) == 0 {
		return merged, nil
	}
	merged = append(merged, cloneRedactionRules(project.Settings.RedactionRules)...)
	return merged, nil
}

// overlayDataPoolBindingsByAlias applies overrides to a scenario's bindings: an
// override replaces the binding with the same alias (case-insensitive), the
// scenario's other bindings stay in their order, and an override for an alias
// the scenario lacks is added after them.
func overlayDataPoolBindingsByAlias(base, overrides []storage.ScenarioDataPoolBinding) []storage.ScenarioDataPoolBinding {
	out := cloneScenarioDataPoolBindings(base)
	positions := make(map[string]int, len(out)+len(overrides))
	for index, binding := range out {
		if key := strings.ToLower(strings.TrimSpace(binding.Alias)); key != "" {
			positions[key] = index
		}
	}
	for _, override := range overrides {
		key := strings.ToLower(strings.TrimSpace(override.Alias))
		if index, ok := positions[key]; ok && key != "" {
			out[index] = override
			continue
		}
		out = append(out, override)
		if key != "" {
			positions[key] = len(out) - 1
		}
	}
	return out
}
