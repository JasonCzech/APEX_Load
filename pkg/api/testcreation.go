package api

// The build-a-test pipeline: CreateTest and draft-capacity estimation,
// scenario-request derivation/validation, group normalization + compilation,
// shared data-source/CSV assembly, load-stage/duration parsing, name
// derivation, and the project-ID/runtime-settings/redaction resolvers.

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
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
	tr.mu.RLock()
	defer tr.mu.RUnlock()

	preview, err := tr.buildManagedTestLocked(req, "draft-estimate", "DRAFT")
	if err != nil {
		return nil, err
	}

	return &DraftCapacityEstimate{
		ScenarioMode:          preview.ScenarioMode,
		TotalVUsers:           preview.TotalVUsers,
		Duration:              preview.DurationStr,
		ResourceBaseline:      preview.ResourceBaseline,
		Groups:                preview.Groups,
		Assignment:            cloneGeneratorAssignmentSettings(preview.Assignment),
		Footprint:             buildScenarioFootprint(preview),
		Recommendation:        tr.buildAssignmentRecommendation(preview),
		CapacityAdvisory:      cloneCapacityAdvisory(preview.CapacityAdvisory),
		RuntimeSettingsReview: cloneRuntimeSettingsReview(preview.RuntimeSettingsReview),
	}, nil
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

// createTestWithIDAndTerm is the execution-plan materialization boundary. The
// caller preassigns and durably correlates id before entering this method, and
// the exact leadership term is retained throughout creation so an old goroutine
// cannot adopt a newer term after same-process reacquisition.
func (tr *TestRunner) createTestWithIDAndTerm(req CreateTestRequest, broadcast bool, id string, leaseTerm executionLeaseTerm) (*ManagedTest, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("preassigned test id is required")
	}
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
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if !tr.executionLeaseTermCurrent(leaseTerm) {
		return nil, errNotExecutionLeader
	}
	// A concurrent exact retry can miss the durable/cache preflight above while
	// the winning goroutine is still creating the row. tr.mu serializes that
	// boundary, so re-check after acquiring it and return the winner unchanged.
	if existing := tr.tests[id]; existing != nil {
		if materializationKey == "" || !sameManagedTestMaterialization(existing, id, materializationKey, req.ProjectID) {
			return nil, fmt.Errorf("%w: test %s", errTestIdentityCollision, id)
		}
		adopted := cloneManagedTestForReadLocked(existing)
		adopted.creationAdopted = true
		return adopted, nil
	}

	now := time.Now()
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
	test, err := tr.buildManagedTestLocked(req, id, status)
	if err != nil {
		return nil, err
	}
	test.executionFence = executionFence
	if status == "RESERVED" && test.ScheduledStartAt != nil {
		window := launchWindowForTest(test, test.ScheduledStartAt.UTC())
		if conflicts := tr.launchWindowConflictsLocked(test, window, now.UTC(), "", ""); len(conflicts) > 0 {
			return nil, fmt.Errorf("launch window conflict: %s", conflicts[0].Message)
		}
	}

	// Strict: when a timeslot is supplied, bind it as part of creation. A bind
	// failure aborts the create so a run is never created unprotected.
	if req.TimeslotID != nil && strings.TrimSpace(*req.TimeslotID) != "" {
		if err := tr.validateAndBindTimeslotLocked(strings.TrimSpace(*req.TimeslotID), test); err != nil {
			return nil, err
		}
	}

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
	if err := tr.completeRuntimeOverrideSecretAttempt(runtimeSecretAttempt); err != nil {
		// The durable test is the source of truth. Recovery observes it and only
		// removes the value-free journal; it never compensates committed secrets.
		slog.Warn("runtime credential provisioning journal cleanup deferred", "test_id", test.ID, "error", err)
		tr.scheduleRuntimeOverrideSecretAttempt(runtimeSecretAttempt.ID, runtimeSecretAttempt.LeaseExpiresAt)
	}
	if !tr.executionLeaseTermCurrent(leaseTerm) {
		return nil, errNotExecutionLeader
	}

	tr.tests[test.ID] = test
	tr.indexRefKeyLocked(test.RefKey, test.ID)

	if broadcast {
		tr.broadcastEvent("test_created", test)
	}
	return cloneManagedTestForReadLocked(test), nil
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

func (tr *TestRunner) buildManagedTestLocked(req CreateTestRequest, id string, status string) (*ManagedTest, error) {
	scenarioProjectID := ""
	if req.ScenarioID != "" {
		req.ScenarioID = tr.resolveRefKeyLocked(req.ScenarioID)
		scenario, ok := tr.scenarios[req.ScenarioID]
		if !ok {
			return nil, fmt.Errorf("scenario not found: %s", req.ScenarioID)
		}
		scenarioProjectID = normalizeProjectID(scenario.ProjectID)
		req = createTestRequestFromScenario(req, scenario)
	}

	// Pool-aware assignment: expand a fleet-pool selection into its member targets.
	resolvedAssignment, err := tr.resolvePoolAssignmentLocked(req.Assignment)
	if err != nil {
		return nil, err
	}
	req.Assignment = resolvedAssignment

	dur, err := time.ParseDuration(req.Duration)
	if err != nil {
		return nil, fmt.Errorf("invalid duration: %w", err)
	}
	if dur <= 0 {
		return nil, fmt.Errorf("duration must be greater than zero")
	}

	scenarioMode, err := validateScenarioRequest(req)
	if err != nil {
		return nil, err
	}

	groups, err := normalizeCreateTestGroups(req)
	if err != nil {
		return nil, err
	}
	for index := range groups {
		groups[index].runtimeSecretScope = id
	}
	projectID, err := tr.resolveCreateTestProjectID(req.ProjectID, groups)
	if err != nil {
		return nil, err
	}
	if scenarioProjectID != "" && scenarioProjectID != projectID {
		return nil, fmt.Errorf("scenario and test scripts must belong to one project")
	}
	if err := tr.validateDataPoolProjectsLocked(projectID, req.DataPools); err != nil {
		return nil, err
	}

	sharedDataSources, err := tr.buildSharedDataSourcesLocked(req.DataPools)
	if err != nil {
		return nil, err
	}

	testGroups, engineGroups, totalVUsers, err := tr.compileManagedTestGroupsForModeWithDataSourcesLocked(groups, req.RuntimeSettings, sharedDataSources, scenarioMode)
	if err != nil {
		return nil, err
	}
	runtimeSettingsReview := buildRuntimeSettingsScenarioReview(testGroups, engineGroups)
	if status != "DRAFT" && runtimeSettingsReview != nil && runtimeSettingsReview.Status == "blocked" {
		return nil, fmt.Errorf("runtime settings blocked: %s", runtimeSettingsReview.Summary)
	}

	testName := deriveManagedTestName(req.Name, testGroups)

	if err := tr.projectDeletionErrorLocked(projectID); err != nil {
		return nil, err
	}
	if err := tr.enforceTestQuotaLocked(projectID, totalVUsers, dur, status); err != nil {
		return nil, err
	}
	effectiveRuntimeSettings, err := tr.resolveEffectiveRuntimeSettings(projectID, req.RuntimeSettings)
	if err != nil {
		return nil, err
	}
	protectedRuntimeSettings, _, err := protectRuntimeOverrideSecrets(id, -1, nil, effectiveRuntimeSettings, false)
	if err != nil {
		return nil, fmt.Errorf("protect test runtime credentials: %w", err)
	}

	applicationID, releaseID, err := tr.resolveCreateTestReleaseContext(projectID, req.ApplicationID, req.ReleaseID, status)
	if err != nil {
		return nil, err
	}
	req.ReleaseID = releaseID
	now := time.Now()
	creationActor := normalizeTestCreationActor(req.creationActor)
	refKey := ""
	if status != "DRAFT" {
		sequence, err := tr.nextRunSeqLocked(now)
		if err != nil {
			return nil, fmt.Errorf("allocate run reference key: %w", err)
		}
		refKey = refkey.Run(tr.envCode, now, sequence)
	}
	test := &ManagedTest{
		ID:                    id,
		RefKey:                refKey,
		MaterializationKey:    strings.TrimSpace(req.MaterializationKey),
		Name:                  testName,
		Status:                status,
		ApplicationID:         applicationID,
		ReleaseID:             req.ReleaseID,
		ScenarioID:            req.ScenarioID,
		ProjectID:             projectID,
		FolderID:              req.FolderID,
		Groups:                testGroups,
		Assignment:            cloneGeneratorAssignmentSettings(req.Assignment),
		CapacityAdvisory:      tr.estimateCapacityAdvisoryForAssignment(projectID, testGroups, totalVUsers, dur, req.ResourceBaseline, effectiveRuntimeSettings, req.Assignment),
		RuntimeSettingsReview: cloneRuntimeSettingsReview(runtimeSettingsReview),
		ScenarioMode:          scenarioMode,
		ScriptShakeout:        req.ScriptShakeout,
		ResourceBaseline:      req.ResourceBaseline,
		StartDelay:            req.StartDelay,
		GoalConfig:            cloneGoalConfig(req.GoalConfig),
		RuntimeSettings:       cloneRuntimeSettings(protectedRuntimeSettings),
		TotalVUsers:           totalVUsers,
		Duration:              dur,
		DurationStr:           req.Duration,
		CreatedAt:             now,
		CreatedBySubject:      creationActor.Subject,
		CreatedByDisplayName:  creationActor.DisplayName,
		ScheduledStartAt:      cloneTimePtr(req.ScheduledStartAt),
		groupSpecs:            engineGroups,
	}
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
		if conflicts := tr.javaVuserRunnerLaunchConflictsLocked(test.Groups); len(conflicts) > 0 {
			conflict := conflicts[0]
			return nil, fmt.Errorf("%s: %s", conflict.Code, conflict.Message)
		}
		if conflict := importPromotionLaunchConflict(test.Groups); conflict != nil {
			return nil, fmt.Errorf("%s: %s", conflict.Code, conflict.Message)
		}
	}
	return test, nil
}

const (
	runtimeOverrideSecretProviderTimeout = 10 * time.Second
	runtimeOverrideSecretAttemptLease    = 5 * time.Minute
	runtimeOverrideSecretRecoveryRetry   = time.Minute
)

var errServiceAccountRuntimeSecretWrite = errors.New("service accounts cannot create project secrets from runtime credentials; configure the secret with an authorized human user and reference it with {{secret.NAME}}")
var errUnattributedRuntimeSecretWrite = errors.New("runtime credentials cannot create project secrets without durable human actor attribution; update the schedule or rerun the execution plan as an authorized user")

type testCreationActor struct {
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
	req.Name = firstNonEmpty(req.Name, scenario.Name)
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
	if req.RuntimeSettings == nil {
		req.RuntimeSettings = cloneRuntimeSettings(scenario.RuntimeSettings)
	}
	if req.Assignment == nil {
		req.Assignment = cloneGeneratorAssignmentSettings(scenario.Assignment)
	}
	if len(req.DataPools) == 0 {
		req.DataPools = append([]storage.ScenarioDataPoolBinding(nil), scenario.DataPools...)
	}
	if len(req.Groups) == 0 && req.ScriptID == "" {
		req.Groups = make([]CreateGroupEntry, 0, len(scenario.Scripts))
		for _, script := range scenario.Scripts {
			req.Groups = append(req.Groups, CreateGroupEntry{
				Name:            script.Name,
				ScriptID:        script.ScriptID,
				Role:            script.Role,
				VUsers:          script.VUsers,
				RampUp:          script.RampUp,
				RampDown:        script.RampDown,
				Stages:          cloneLoadStages(script.Stages),
				Iterations:      script.Iterations,
				InitStrategy:    script.Initialization,
				RuntimeSettings: cloneRuntimeSettings(script.RuntimeSettings),
				Assignment:      cloneGeneratorAssignmentSettings(script.Assignment),
			})
		}
	}
	return req
}

func validateScenarioRequest(req CreateTestRequest) (string, error) {
	scenarioMode := normalizeScenarioMode(req.ScenarioMode)
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
		if startDelay <= 0 {
			return "", fmt.Errorf("start_delay must be greater than zero")
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
		testGroup, engineGroup, err := tr.compileManagedTestGroupForModeLocked(i, len(groups), group, runtimeSettings, sharedDataSources, scenarioMode)
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
	script, ok := tr.scripts[group.ScriptID]
	if !ok {
		return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: script not found: %s", index, group.ScriptID)
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
	if group.RuntimeSettings != nil && group.RuntimeSettings.Iterations != nil && *group.RuntimeSettings.Iterations < 1 {
		return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: runtime_settings.iterations must be greater than 0", index)
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
	if group.Iterations != nil && (group.RuntimeSettings == nil || group.RuntimeSettings.Iterations == nil) {
		if effectiveRuntimeSettings == nil {
			effectiveRuntimeSettings = &RuntimeSettingsOverride{}
		}
		iterations := *group.Iterations
		effectiveRuntimeSettings.Iterations = &iterations
	}
	effectiveIterations := group.Iterations
	if effectiveRuntimeSettings != nil && effectiveRuntimeSettings.Iterations != nil {
		iterations := *effectiveRuntimeSettings.Iterations
		effectiveIterations = &iterations
	}

	testGroup := TestGroup{
		Name:            name,
		ScriptID:        group.ScriptID,
		ScriptName:      script.Name,
		Protocol:        script.Protocol,
		Role:            role,
		VUsers:          vusers,
		RampUp:          rampUpValue,
		RampDown:        rampDownValue,
		Stages:          cloneLoadStages(group.Stages),
		Iterations:      effectiveIterations,
		RuntimeSettings: nil,
		Assignment:      cloneGeneratorAssignmentSettings(assignment),
		RunLogicGoals:   runLogicGoalSnapshot(script.Script),
		Initialization:  initStrategy,
		rampUpDur:       engineRampUp,
		stageDriven:     stageDriven,
		initialVUsers:   initialVUsers,
		currentVUsers:   initialVUsers,
	}

	scriptCopy := *script.Script
	// Runtime HAR capture and retention must use the same centrally managed
	// directory. Override any script-supplied path before local or distributed
	// execution so scripts cannot select arbitrary filesystem locations.
	scriptCopy.Config.Browser.HARArtifactDir = strings.TrimSpace(tr.settings.ArchiveRetention.HARArtifactDir)
	if len(sharedDataSources) > 0 {
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
	if effectiveRuntimeSettings != nil && effectiveRuntimeSettings.Iterations != nil {
		if executionRuntimeSettings == nil {
			executionRuntimeSettings = &RuntimeSettingsOverride{}
		}
		iterations := *effectiveRuntimeSettings.Iterations
		executionRuntimeSettings.Iterations = &iterations
	}
	runtimeSecretValues := cloneRuntimeSecretValues(rootRuntimeSecretValues)
	if runtimeSecretValues == nil && len(groupRuntimeSecretValues) > 0 {
		runtimeSecretValues = make(map[string]string, len(groupRuntimeSecretValues))
	}
	for secretName, secretValue := range groupRuntimeSecretValues {
		runtimeSecretValues[secretName] = secretValue
	}
	if executionRuntimeSettings != nil {
		if err := applyRuntimeOverrides(&scriptCopy, executionRuntimeSettings); err != nil {
			return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: apply runtime overrides: %w", index, err)
		}
		if issues := dsl.ValidateScriptIssues(&scriptCopy); len(issues) > 0 {
			return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: %s", index, strings.Join(issues, "; "))
		}
	}
	// Resolve object-repository bindings into concrete selectors before the
	// script is distributed — generators have no store, so an unresolved
	// object_ref would be unrunnable on the generator.
	if resolved, refWarnings, refIssues := resolveObjectRefs(tr.objectRepoStore, &scriptCopy); resolved != nil {
		if len(refIssues) > 0 {
			return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: %s", index, strings.Join(refIssues, "; "))
		}
		for _, w := range refWarnings {
			slog.Warn("object_ref resolution degraded to inline locator", "group", index, "detail", w)
		}
		scriptCopy = *resolved
	}
	if len(scriptCopy.Actions) == 0 {
		if issues := dsl.ValidateScriptIssues(&scriptCopy); len(issues) > 0 {
			return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: %s", index, strings.Join(issues, "; "))
		}
		return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: script must have at least one action", index)
	}
	effectiveRuntimeSettings, err = tr.resolveEffectiveRuntimeSettings(script.ProjectID, effectiveRuntimeSettings)
	if err != nil {
		return TestGroup{}, engine.GroupConfig{}, fmt.Errorf("group[%d]: resolve redaction rules: %w", index, err)
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
	testGroup.script = &scriptCopy
	testGroup.RuntimeSettings = cloneRuntimeSettings(protectedGroupSettings)
	testGroup.redactionRules = cloneRedactionRules(nilIfNoRules(effectiveRuntimeSettings))
	testGroup.runtimeSecretValues = cloneRuntimeSecretValues(runtimeSecretValues)
	secretResolver := overlayRuntimeSecretResolver(runtimeSecretValues, tr.secretResolverForProject(scriptCopy.ProjectID))

	engineGroup := engine.GroupConfig{
		Name:            name,
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

func (tr *TestRunner) buildSharedDataSourcesLocked(bindings []storage.ScenarioDataPoolBinding) ([]dsl.DataSource, error) {
	if len(bindings) == 0 {
		return nil, nil
	}
	if tr.dataPoolStore == nil {
		return nil, fmt.Errorf("data pool storage is not configured")
	}
	sources := make([]dsl.DataSource, 0, len(bindings))
	for _, binding := range bindings {
		alias := strings.TrimSpace(binding.Alias)
		if alias == "" {
			return nil, fmt.Errorf("data pool binding alias is required")
		}
		rows, err := tr.dataPoolStore.ListDataPoolRows(binding.PoolID)
		if err != nil {
			return nil, fmt.Errorf("load data pool %q: %w", alias, err)
		}
		if len(rows) == 0 {
			return nil, fmt.Errorf("data pool %q has no rows", alias)
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
	if len(bindings) == 0 {
		return nil
	}
	if tr.dataPoolStore == nil {
		return fmt.Errorf("data pool storage is not configured")
	}
	expectedProjectID := normalizeProjectID(projectID)
	for _, binding := range bindings {
		poolID := strings.TrimSpace(binding.PoolID)
		if poolID == "" {
			return fmt.Errorf("data pool id is required")
		}
		pool, err := tr.dataPoolStore.GetDataPool(poolID)
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
			values[i] = row.Data[column]
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

func (tr *TestRunner) resolveCreateTestProjectID(requestProjectID string, groups []CreateGroupEntry) (string, error) {
	projectID := strings.TrimSpace(requestProjectID)
	if projectID != "" {
		projectID = normalizeProjectID(projectID)
	}
	for _, group := range groups {
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

func (tr *TestRunner) resolveCreateTestReleaseContext(projectID, requestedApplicationID, requestedReleaseID, status string) (string, string, error) {
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
		// internal bucket rather than a caller-supplied cross-project reference.
		releaseID = storage.LegacyReleaseID
		applicationID = storage.LegacyApplicationID
		legacyFallback = true
	}

	var release *storage.Release
	if explicitRelease {
		if tr.releaseStore != nil {
			var err error
			release, err = tr.releaseStore.GetRelease(releaseID)
			if err != nil || release == nil {
				return "", "", fmt.Errorf("release not found: %s", releaseID)
			}
			if release.ArchivedAt != nil {
				return "", "", fmt.Errorf("release is archived: %s", releaseID)
			}
			if applicationID != "" && applicationID != strings.TrimSpace(release.ApplicationID) {
				return "", "", fmt.Errorf("application_id does not match release")
			}
			applicationID = strings.TrimSpace(release.ApplicationID)
		}
	}

	applicationProjectID := ""
	if applicationID != "" && !legacyFallback {
		if applicationStore, ok := tr.releaseStore.(interface {
			GetApplication(id string) (*storage.Application, error)
		}); ok {
			application, err := applicationStore.GetApplication(applicationID)
			if err != nil || application == nil {
				return "", "", fmt.Errorf("application not found: %s", applicationID)
			}
			applicationProjectID = normalizeProjectID(application.ProjectID)
		}
	}

	if release != nil {
		releaseProjectID := strings.TrimSpace(release.ProjectID)
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

func (tr *TestRunner) resolveEffectiveRedactionRules(projectID string, explicit []engine.RedactionRule) ([]engine.RedactionRule, error) {
	merged := cloneRedactionRules(explicit)
	if tr.projectStore == nil {
		return merged, nil
	}
	projectID = normalizeProjectID(projectID)
	project, err := tr.projectStore.GetProject(projectID)
	if err != nil || project == nil || len(project.Settings.RedactionRules) == 0 {
		return merged, nil
	}
	merged = append(merged, cloneRedactionRules(project.Settings.RedactionRules)...)
	return merged, nil
}
