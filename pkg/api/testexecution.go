package api

// The run state machine: Start/Stop/Abort/Pause/Resume/AdjustDuration with
// startability + preflight gating, script-shakeout gating, run setup
// (runTest/buildRunEngine) and lifecycle (start/fail/complete), VUser scaling
// and scale-ramps, goal-target control, and pending-test promotion.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"runtime/debug"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/api/testexec"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/internal/boundedtext"
	"github.com/jasonczech/apexload/pkg/scrub"
	"github.com/jasonczech/apexload/pkg/secretstore"
	"github.com/jasonczech/apexload/pkg/storage"
	"github.com/jasonczech/apexload/pkg/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

var (
	errDistributedTestActivating = errors.New("distributed test is still activating")
	errTestLoadNotReady          = errors.New("test load window is not ready")
)

// distributedActivationErrorLocked protects the interval between the durable
// controller-side RUNNING transition and the final CommitWork acknowledgement.
// Stop intentionally does not use this gate: it must be able to tear down both
// staged and already-committed assignments at every point in startup.
func (tr *TestRunner) distributedActivationErrorLocked(test *ManagedTest) error {
	if test == nil {
		return nil
	}
	if _, distributed := tr.resolveBackend(test).(*distributedExecutionBackend); distributed && test.remoteActivationPending {
		return fmt.Errorf("%w; retry after every generator acknowledges start", errDistributedTestActivating)
	}
	return nil
}

// testLoadReadyLocked reports whether runtime scaling/goal control may affect
// the active workload. Local engines expose an exact Ready barrier after their
// start-delay/resource-baseline window. Distributed runs use the coordinated
// StartedAt clock, which may be in the future, plus the same pre-load windows.
// Standalone test fixtures that are not owned by this runner retain the pure
// goal-controller behavior used by focused unit tests.
func (tr *TestRunner) testLoadReadyLocked(test *ManagedTest, now time.Time) bool {
	if test == nil {
		return false
	}
	managed := tr.tests != nil && tr.tests[test.ID] == test
	if !managed {
		return test.Status != "PAUSED"
	}
	if test.Status != "RUNNING" {
		return false
	}
	if test.multiEngine != nil {
		return test.multiEngine.ScalingReady()
	}
	if _, distributed := tr.resolveBackend(test).(*distributedExecutionBackend); !distributed {
		// Custom execution backends used by extensions/tests have no common
		// readiness contract. Their RUNNING transition remains authoritative.
		return true
	}
	if test.remoteActivationPending {
		return false
	}
	// Runs created through prepareDistributedTestRun always set remoteActivated.
	// A false/false pair is retained for legacy restored records and lightweight
	// backend adapters that predate the activation phase; their RUNNING state is
	// the only available readiness contract.
	if !test.remoteActivated {
		return true
	}
	if test.StartedAt == nil || test.StartedAt.IsZero() {
		return false
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	startedAt := test.StartedAt.UTC()
	if now.Before(startedAt) {
		return false
	}
	active := now.Sub(startedAt) - test.CumulativePause
	if test.PausedAt != nil && !test.PausedAt.IsZero() && now.After(test.PausedAt.UTC()) {
		pauseStarted := test.PausedAt.UTC()
		if pauseStarted.Before(startedAt) {
			pauseStarted = startedAt
		}
		active -= now.Sub(pauseStarted)
	}
	preBaseline, _ := testexec.ResourceBaselineWindows(test.ResourceBaseline)
	preBaseline += testexec.StartDelayWindow(test.StartDelay)
	return active >= preBaseline
}

func shiftGoalControllerForPause(state *goalControllerState, pauseDelta time.Duration) {
	if state == nil || pauseDelta <= 0 {
		return
	}
	if state.breachStartedAt != nil {
		shifted := state.breachStartedAt.Add(pauseDelta)
		state.breachStartedAt = &shifted
	}
	if state.lastScaleAt != nil {
		shifted := state.lastScaleAt.Add(pauseDelta)
		state.lastScaleAt = &shifted
	}
}

func (tr *TestRunner) StartTest(id string) error {
	return tr.StartTestWithOptions(id, StartTestOptions{})
}

func (tr *TestRunner) StartTestWithOptions(id string, opts StartTestOptions) error {
	if opts.PreparationID != "" {
		return tr.consumePreparation(id, opts)
	}
	return tr.startTestWithOptionsAndExpectedTerm(id, opts, nil)
}

func (tr *TestRunner) StartTestWithActor(id string, opts StartTestOptions, actor testCreationActor) error {
	opts.actor = actor
	return tr.StartTestWithOptions(id, opts)
}

type testActorAttributionSnapshot struct {
	subject     string
	displayName string
}

func applyTestStartedByActor(test *ManagedTest, actor testCreationActor) (testActorAttributionSnapshot, bool) {
	if test == nil {
		return testActorAttributionSnapshot{}, false
	}
	if strings.TrimSpace(actor.Subject) == "" {
		actor.Subject = test.CreatedBySubject
		actor.DisplayName = test.CreatedByDisplayName
	}
	if strings.TrimSpace(actor.Subject) == "" {
		return testActorAttributionSnapshot{}, false
	}
	snapshot := testActorAttributionSnapshot{subject: test.StartedBySubject, displayName: test.StartedByDisplayName}
	test.StartedBySubject = strings.TrimSpace(actor.Subject)
	test.StartedByDisplayName = strings.TrimSpace(actor.DisplayName)
	return snapshot, snapshot.subject != test.StartedBySubject || snapshot.displayName != test.StartedByDisplayName
}

func restoreTestStartedByActor(test *ManagedTest, snapshot testActorAttributionSnapshot) {
	if test == nil {
		return
	}
	test.StartedBySubject = snapshot.subject
	test.StartedByDisplayName = snapshot.displayName
}

func applyTestStoppedByActor(test *ManagedTest, actor testCreationActor) (testActorAttributionSnapshot, bool) {
	if test == nil || strings.TrimSpace(actor.Subject) == "" {
		return testActorAttributionSnapshot{}, false
	}
	snapshot := testActorAttributionSnapshot{subject: test.StoppedBySubject, displayName: test.StoppedByDisplayName}
	// stopped_by identifies the operator who initiated teardown. A later retry
	// may come from another operator (or an automatic recovery worker), but it
	// must not rewrite the durable provenance of the original stop request.
	if strings.TrimSpace(snapshot.subject) != "" {
		return snapshot, false
	}
	test.StoppedBySubject = strings.TrimSpace(actor.Subject)
	test.StoppedByDisplayName = strings.TrimSpace(actor.DisplayName)
	return snapshot, snapshot.subject != test.StoppedBySubject || snapshot.displayName != test.StoppedByDisplayName
}

func restoreTestStoppedByActor(test *ManagedTest, snapshot testActorAttributionSnapshot) {
	if test == nil {
		return
	}
	test.StoppedBySubject = snapshot.subject
	test.StoppedByDisplayName = snapshot.displayName
}

// startTestWithOptionsAndExpectedTerm runs the ordinary start state machine,
// optionally pinning every ownership claim to an immutable execution lease
// term. Execution-plan goroutines use the pinned form so an old goroutine can
// never adopt a later term after this process loses and reacquires leadership.
func (tr *TestRunner) startTestWithOptionsAndExpectedTerm(id string, opts StartTestOptions, expectedTerm *executionLeaseTerm) (startErr error) {
	parent := telemetry.Detach(opts.traceContext)
	ctx, span := otel.Tracer("github.com/jasonczech/apexload/pkg/api").Start(
		parent,
		"apexload.test.start",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(attribute.String("apexload.test.id", telemetry.BoundedIdentifier(id))),
	)
	opts.traceContext = ctx
	defer func() {
		tr.annotateTestLifecycleSpan(span, id, startErr)
		span.End()
	}()
	if expectedTerm == nil && tr != nil && tr.durableExecutionFence {
		// Public starts must first pass the admission gate, then retain the exact
		// durable lease term for the complete operation. A same-process loss and
		// reacquisition may never let an old start adopt the successor term.
		if !tr.isExecutionLeader() {
			return errNotExecutionLeader
		}
		capturedTerm, err := tr.captureExecutionLeaseTerm()
		if err != nil {
			return err
		}
		expectedTerm = &capturedTerm
	}
	return tr.startTestWithOptionsAndExpectedTermCore(id, opts, expectedTerm)
}

func (tr *TestRunner) startTestWithOptionsAndExpectedTermCore(id string, opts StartTestOptions, expectedTerm *executionLeaseTerm) error {
	test, err := tr.prepareTestForExecutionWithOptionsAndExpectedTerm(id, opts, expectedTerm)
	if err != nil {
		return err
	}
	if test != nil {
		go tr.runTest(test)
	}
	return nil
}

func validateExpectedStartRecoveryIdentityLocked(test *ManagedTest, opts StartTestOptions) error {
	if test != nil && test.Status == "QUEUED" && testHasShakeoutAdmissionReservation(test) && test.shakeoutAdmissionReservation != opts.shakeoutAdmissionReservation {
		return errStartAdmissionEvidenceStale
	}
	if opts.expectedRecoveryIdentity == nil {
		return nil
	}
	if !opts.expectedRecoveryIdentity.matches(test) {
		return storage.ErrTestStartIdentityChanged
	}
	return nil
}

// prepareTestForExecutionWithOptionsAndExpectedTerm runs the complete start
// pipeline through the durable INITIALIZING/PENDING/SHAKEOUT boundary without
// dispatching the execution backend. Ordinary starts dispatch the returned
// test immediately; recurring schedules first commit their occurrence and
// parent-schedule projections, then dispatch the same prepared runtime. Keeping
// this as one shared pipeline prevents scheduled runs from bypassing secret
// resolution, script shakeout, or revision-fenced admission evidence.
func (tr *TestRunner) prepareTestForExecutionWithOptionsAndExpectedTerm(id string, opts StartTestOptions, expectedTerm *executionLeaseTerm) (*ManagedTest, error) {
	if err := tr.validateExpectedStartTerm(expectedTerm); err != nil {
		return nil, err
	}
	reservationCtx, cancelReservation := tr.executionOperationContext(context.Background())
	defer cancelReservation()
	opts.shakeoutAdmissionReservation = &shakeoutAdmissionReservation{ctx: reservationCtx}
	defer func() {
		tr.mu.Lock()
		if test := tr.tests[id]; test != nil && test.shakeoutAdmissionReservation == opts.shakeoutAdmissionReservation {
			test.shakeoutAdmissionReservation = nil
		}
		tr.mu.Unlock()
	}()
	tr.mu.Lock()
	id = tr.resolveRefKeyLocked(id)
	if err := validateExpectedStartRecoveryIdentityLocked(tr.tests[id], opts); err != nil {
		tr.mu.Unlock()
		return nil, err
	}
	if test := tr.tests[id]; test != nil {
		if err := tr.validateGeneratorLossTolerance(test.Assignment); err != nil {
			tr.mu.Unlock()
			return nil, err
		}
	}
	tr.mu.Unlock()
	if err := tr.validateRetiredExecution(id, opts.PreparationID); err != nil {
		return nil, err
	}
	// A completed shakeout may still be waiting for its durable outcome write
	// after a transient storage interruption. Finish that transition before any
	// new admission work so retrying Start never replays external actions.
	if err := tr.resumePendingScriptShakeoutOutcomeForIdentity(id, expectedTerm, opts.expectedRecoveryIdentity); err != nil {
		return nil, err
	}
	if err := tr.validateCIRunPolicyForStart(id); err != nil {
		return nil, err
	}
	if err := tr.refreshTestCapacityBindingFromStore(id); err != nil {
		return nil, err
	}
	var blocked bool
	var err error
	for attempt := 0; attempt < startAdmissionPhaseAttempts; attempt++ {
		if err = tr.validateExpectedStartTerm(expectedTerm); err != nil {
			return nil, err
		}
		preflightAt := time.Now().UTC()
		preflightEvidence, evidenceErr := tr.loadStartLaunchEvidence(id, preflightAt)
		if evidenceErr != nil {
			return nil, evidenceErr
		}
		if err = tr.validateCIRunPolicyForStartAtProjectRevision(id, preflightEvidence.launchWindow.projectQuota.updatedAt); err != nil {
			if errors.Is(err, errStartAdmissionEvidenceStale) {
				continue
			}
			return nil, err
		}
		if err = tr.validateExpectedStartTerm(expectedTerm); err != nil {
			return nil, err
		}
		tr.mu.Lock()
		blocked, err = tr.preflightStartBlocksLockedWithExpectedTermAndEvidence(id, opts, expectedTerm, preflightAt, preflightEvidence)
		tr.mu.Unlock()
		if !errors.Is(err, errStartAdmissionEvidenceStale) {
			break
		}
	}
	if err != nil {
		return nil, err
	}
	if blocked {
		return nil, nil
	}
	if err := tr.validateStartSecretRequirements(id); err != nil {
		return nil, err
	}
	if _, err := tr.validateScriptShakeoutPassForStart(id, opts, expectedTerm); err != nil {
		return nil, err
	}
	shakeoutInputFingerprint := scriptShakeoutInputFingerprint{}
	if tr.scriptShakeoutRequiredForStart(id, opts) {
		shakeoutInputFingerprint, err = tr.scriptShakeoutExecutableFingerprint(id)
		if err != nil {
			return nil, err
		}
	}

	// Snapshot PENDING before shakeout admission runs. The check below exists to
	// detect a test that admission just queued during THIS start attempt; without
	// this snapshot it cannot distinguish that from a run that was already PENDING
	// on entry, and it would swallow an operator's explicit start (returning
	// success while dispatching nothing). That silently discarded capacity-shortfall
	// re-approvals: the run stayed PENDING citing the very approval just granted.
	tr.mu.RLock()
	pendingBeforeShakeoutAdmission := tr.tests[id] != nil && tr.tests[id].Status == "PENDING"
	tr.mu.RUnlock()

	shakeoutTest, err := tr.beginScriptShakeoutIfNeededWithExpectedTerm(id, opts, expectedTerm)
	if err != nil {
		return nil, err
	}
	if shakeoutTest == nil {
		tr.mu.RLock()
		queuedByShakeoutAdmission := tr.tests[id] != nil && tr.tests[id].Status == "PENDING" && !pendingBeforeShakeoutAdmission
		tr.mu.RUnlock()
		if queuedByShakeoutAdmission {
			return nil, nil
		}
	}
	if shakeoutTest != nil {
		report, operationErr := tr.runScriptShakeoutReport(shakeoutTest)
		if operationErr != nil {
			if termErr := tr.validateExpectedStartTerm(expectedTerm); termErr != nil {
				return nil, termErr
			}
			return nil, operationErr
		}
		if report.Passed {
			currentSHA256, fingerprintErr := tr.scriptShakeoutExecutableFingerprint(id)
			switch {
			case fingerprintErr != nil:
				if retryableStartSecretRequirement(fingerprintErr) {
					return nil, tr.markScriptShakeoutVerificationPendingWithExpectedTerm(
						id, report, shakeoutInputFingerprint, "QUEUED", nil, "", expectedTerm, fingerprintErr,
						!opts.deferPostShakeoutRecoveryEnqueue,
					)
				}
				report.Passed = false
				report.Summary = "Script shakeout completed, but its executable input evidence could not be verified; full load test has not started."
				report.Results = append(report.Results, ScriptShakeoutResult{GroupName: "executable inputs", Status: "failed", Error: fingerprintErr.Error()})
			case currentSHA256 != shakeoutInputFingerprint:
				report.Passed = false
				report.Summary = "Script shakeout inputs changed while the check was running; full load test has not started."
				report.Results = append(report.Results, ScriptShakeoutResult{GroupName: "executable inputs", Status: "failed", Error: "the executable snapshot or a versioned secret source changed during shakeout"})
			default:
				shakeoutInputFingerprint = currentSHA256
			}
		}
		if !report.Passed {
			return nil, tr.markScriptShakeoutReviewWithExpectedTerm(id, report, expectedTerm)
		}
		if err := tr.markScriptShakeoutPassedWithExpectedTermAndFingerprint(id, report, shakeoutInputFingerprint, expectedTerm); err != nil {
			return nil, err
		}
	}
	// Shakeout can take tens of seconds. Retry only this evidence/commit phase so
	// admission churn never replays already-executed shakeout actions.
	var test *ManagedTest
	for attempt := 0; attempt < startAdmissionPhaseAttempts; attempt++ {
		if err = tr.validateExpectedStartTerm(expectedTerm); err != nil {
			return nil, err
		}
		if err = tr.refreshTestCapacityBindingFromStore(id); err != nil {
			return nil, err
		}
		prepareAt := time.Now().UTC()
		prepareEvidence, evidenceErr := tr.loadStartLaunchEvidence(id, prepareAt)
		if evidenceErr != nil {
			return nil, evidenceErr
		}
		if err = tr.validateCIRunPolicyForStartAtProjectRevision(id, prepareEvidence.launchWindow.projectQuota.updatedAt); err != nil {
			if errors.Is(err, errStartAdmissionEvidenceStale) {
				continue
			}
			return nil, err
		}
		if err = tr.validateExpectedStartTerm(expectedTerm); err != nil {
			return nil, err
		}
		tr.mu.Lock()
		if expectedTerm == nil || tr.executionLeaseTermLocallyCurrent(*expectedTerm) {
			test, err = tr.prepareTestStartLockedWithExpectedTermAndEvidence(id, opts, expectedTerm, prepareAt, prepareEvidence)
		} else {
			err = errNotExecutionLeader
		}
		tr.mu.Unlock()
		if !errors.Is(err, errStartAdmissionEvidenceStale) {
			break
		}
	}
	if err != nil {
		return nil, err
	}
	return test, nil
}

const startAdmissionPhaseAttempts = 3

// startLaunchEvidence combines durable launch-window state with the static
// Java/runtime-placement decision evaluated against one immutable admission
// snapshot. The fence is rechecked under tr.mu immediately before the
// capacity-reserving transition.
type startLaunchEvidence struct {
	launchWindow           launchWindowEvidence
	admissionFence         managedTestBuildFence
	startAdmissionRevision int64
	runtimeGatesLoaded     bool
	javaConflicts          []LaunchConflict
	placementConflicts     []LaunchConflict
}

var errStartAdmissionEvidenceStale = errors.New("controller admission state changed while start evidence was being evaluated; retry")

type startAdmissionRevisionPersistence interface {
	StartAdmissionRevision() (int64, error)
}

// loadStartLaunchEvidence snapshots the start candidate and capacity-relevant
// controller state under a short runner lock, then performs durable reads and
// potentially slow Java approval/runtime checks after releasing tr.mu.
func (tr *TestRunner) loadStartLaunchEvidence(id string, start time.Time) (startLaunchEvidence, error) {
	revisionStore := tr.startAdmissionRevisionStore
	if revisionStore == nil {
		if candidate, ok := tr.testStore.(startAdmissionRevisionPersistence); ok {
			revisionStore = candidate
		}
	}
	if revisionStore == nil {
		if tr.testStore != nil && !tr.testOnlyAllowUnfencedDeploymentLeaseStart {
			return startLaunchEvidence{}, fmt.Errorf("durable start admission revision fence is unavailable")
		}
		return tr.loadStartLaunchEvidenceOnce(id, start)
	}
	for attempt := 0; attempt < startAdmissionPhaseAttempts; attempt++ {
		before, err := revisionStore.StartAdmissionRevision()
		if err != nil {
			return startLaunchEvidence{}, errors.Join(errStartAdmissionEvidenceStale, fmt.Errorf("load start admission revision: %w", err))
		}
		// Scenario projections and global admission settings are process caches.
		// Refresh both inside the database revision seqlock so a remote write can
		// never pair a fresh revision with stale cached launch authority.
		if err := tr.refreshSchedulerContentForHASweep(); err != nil {
			return startLaunchEvidence{}, errors.Join(errStartAdmissionEvidenceStale, err)
		}
		if err := tr.refreshSystemSettingsForStartAdmission(); err != nil {
			return startLaunchEvidence{}, errors.Join(errStartAdmissionEvidenceStale, err)
		}
		evidence, err := tr.loadStartLaunchEvidenceOnce(id, start)
		if err != nil {
			return startLaunchEvidence{}, err
		}
		after, err := revisionStore.StartAdmissionRevision()
		if err != nil {
			return startLaunchEvidence{}, errors.Join(errStartAdmissionEvidenceStale, fmt.Errorf("reload start admission revision: %w", err))
		}
		if before == after {
			evidence.startAdmissionRevision = before
			return evidence, nil
		}
	}
	return startLaunchEvidence{}, errStartAdmissionEvidenceStale
}

func (tr *TestRunner) refreshSystemSettingsForStartAdmission() error {
	if tr == nil || tr.settingsStore == nil {
		return nil
	}
	tr.refreshSystemSettingsFromStore(true)
	raw, err := tr.settingsStore.GetSetting(systemSettingsStoreKey)
	if err != nil {
		return fmt.Errorf("load system settings for start admission: %w", err)
	}
	tr.settingsRefreshMu.Lock()
	applied := tr.systemSettingsRaw == raw
	tr.settingsRefreshMu.Unlock()
	if !applied {
		return fmt.Errorf("durable system settings could not be applied coherently")
	}
	return nil
}

func (tr *TestRunner) loadStartLaunchEvidenceOnce(id string, start time.Time) (startLaunchEvidence, error) {
	tr.mu.Lock()
	// CapacityAdvisory is a derived, in-memory projection and is not stored in
	// the durable test row. A freshly reloaded queued run therefore has no
	// advisory until its first start-gate evaluation. Normalize it before
	// capturing the admission fence; otherwise evaluateStartGatesLocked refreshes
	// the advisory moments later and makes the start attempt appear to conflict
	// with itself. The derivation is already designed to run under tr.mu and does
	// not perform durable storage reads.
	if candidate := tr.tests[id]; candidate != nil {
		tr.refreshCapacityAdvisoryLocked(candidate)
	}
	snapshot, _, fence := tr.managedTestBuildSnapshotLocked(CreateTestRequest{RevisionSourceTestID: id})
	projection := snapshot.tests[id]
	if projection == nil {
		tr.mu.Unlock()
		return startLaunchEvidence{}, fmt.Errorf("test not found: %s", id)
	}
	timeslotID := ""
	if projection.TimeslotID != nil {
		timeslotID = *projection.TimeslotID
	}
	window := launchWindowForTestAt(projection, start.UTC(), start.UTC())
	tr.mu.Unlock()
	evidence := startLaunchEvidence{
		launchWindow:       tr.loadLaunchWindowEvidenceWithBoundTimeslotForProject(window, timeslotID, projection.ProjectID, testAssignedPoolID(projection)),
		admissionFence:     fence,
		runtimeGatesLoaded: true,
		javaConflicts:      snapshot.javaVuserRunnerLaunchConflictsForManagedTestLocked(projection),
	}
	if snapshot.shouldCheckStartPlacement(projection) {
		evidence.placementConflicts = snapshot.generatorPlacementLaunchConflictsLocked(projection)
	}
	return evidence, nil
}

func (tr *TestRunner) loadRuntimeLaunchEvidence(id string, now time.Time, update func(*ManagedTest)) (launchWindowEvidence, error) {
	tr.mu.RLock()
	test := tr.tests[id]
	if test == nil {
		tr.mu.RUnlock()
		return launchWindowEvidence{}, fmt.Errorf("test not found: %s", id)
	}
	projection := *test
	projection.Groups = append([]TestGroup(nil), test.Groups...)
	timeslotID := ""
	if test.TimeslotID != nil {
		timeslotID = *test.TimeslotID
	}
	if update != nil {
		update(&projection)
	}
	start := calendarTestStart(&projection)
	if start.IsZero() {
		start = now
	}
	window := launchWindowForTestAt(&projection, start.UTC(), now.UTC())
	tr.mu.RUnlock()
	return tr.loadLaunchWindowEvidenceWithBoundTimeslotForProject(window, timeslotID, projection.ProjectID, testAssignedPoolID(&projection)), nil
}

type startSecretRequirementError struct {
	message   string
	retryable bool
	cause     error
}

func (e *startSecretRequirementError) Error() string {
	if e == nil {
		return "test secret requirements are not satisfied"
	}
	return e.message
}

func (e *startSecretRequirementError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func retryableStartSecretRequirement(err error) bool {
	var requirementErr *startSecretRequirementError
	return errors.As(err, &requirementErr) && requirementErr.retryable
}

// validateStartSecretRequirements keeps saved scripts independently
// configurable while making StartTest the execution boundary: every static
// {{secret.*}} reference must resolve before shakeout, INITIALIZING, backend
// dispatch, or generator assignment. Admission pins each resolved value for
// the run, including shakeout, so later provider changes do not alter it.
func (tr *TestRunner) validateStartSecretRequirements(id string) error {
	tr.mu.RLock()
	test := tr.tests[id]
	if test == nil {
		tr.mu.RUnlock()
		return fmt.Errorf("test not found: %s", id)
	}
	defaultProjectID := test.ProjectID
	secretRevision := test.pinnedSecrets.version()
	groups := testexec.CloneGroupSpecsWithResultSink(test.groupSpecs, nil)
	runtimeSecrets := make([]map[string]string, len(test.Groups))
	for index := range groups {
		groups[index].Script = cloneScriptDefinition(groups[index].Script)
		if index < len(test.Groups) {
			runtimeSecrets[index] = cloneRuntimeSecretValues(test.Groups[index].runtimeSecretValues)
		}
	}
	tr.mu.RUnlock()

	valuesByGroup := make(map[int]map[string]string, len(groups))
	resolutionCtx, resolutionCancel := engine.WithSecretResolutionBudget(context.Background())
	defer resolutionCancel()
	for index, group := range groups {
		if group.Script == nil {
			continue
		}
		names, err := engine.ReferencedSecretNames(group.Script)
		if err != nil {
			return &startSecretRequirementError{
				message: fmt.Sprintf("group[%d] %q: inspect secret references", index, group.Name),
				cause:   err,
			}
		}
		if len(names) == 0 {
			continue
		}
		projectID := strings.TrimSpace(group.Script.ProjectID)
		if projectID == "" {
			projectID = defaultProjectID
		}
		// Pre-launch verification reads the current provider even when shakeout
		// has already pinned the run's execution value. A removed source must
		// still defer admission, and a changed source must go through review.
		resolver := tr.secretResolverForScope(projectID, runSecretApplicationID(test))
		if index < len(runtimeSecrets) {
			resolver = overlayRuntimeSecretResolver(runtimeSecrets[index], resolver)
		}
		resolved, missing, err := engine.ResolveSecretNames(resolutionCtx, names, resolver)
		if err != nil {
			return &startSecretRequirementError{
				message:   fmt.Sprintf("group[%d] %q: secret resolution failed: %v", index, group.Name, err),
				retryable: true,
				cause:     err,
			}
		}
		if len(missing) > 0 {
			groupName := strings.TrimSpace(group.Name)
			if groupName == "" {
				groupName = group.Script.Name
			}
			return &startSecretRequirementError{
				message:   fmt.Sprintf("group %q: configure the following secret(s) before starting the test: %s", groupName, strings.Join(missing, ", ")),
				retryable: true,
			}
		}
		valuesByGroup[index] = resolved
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.tests[id] != test {
		return errStartAdmissionEvidenceStale
	}
	if test.pinnedSecrets == nil {
		if len(valuesByGroup) == 0 {
			return nil
		}
		return errors.New("run secret pin state is unavailable")
	}
	// A queued attempt that has not performed shakeout has not executed yet.
	// Refresh its pin after a retry so a rotation supplies the first execution.
	// Once shakeout or execution begins, keep its admitted value unchanged.
	status := strings.ToUpper(strings.TrimSpace(test.Status))
	refresh := test.StartedAt == nil && (status == "QUEUED" || status == "PENDING") &&
		test.pendingScriptShakeoutOutcome == nil && test.validatedScriptShakeoutPass == "" &&
		(test.StartAuthorization == nil || (test.StartAuthorization.ScriptShakeoutPass == nil &&
			test.StartAuthorization.ScriptShakeout == nil))
	if !test.pinnedSecrets.pinRevision(valuesByGroup, secretRevision, refresh) {
		return &startSecretRequirementError{message: "test changed during secret resolution; retry start", retryable: true}
	}
	return nil
}

func (tr *TestRunner) validateExpectedStartTerm(expectedTerm *executionLeaseTerm) error {
	if expectedTerm != nil {
		if !tr.executionLeaseTermCurrent(*expectedTerm) {
			return errNotExecutionLeader
		}
		return nil
	}
	if !tr.isExecutionLeader() {
		return errNotExecutionLeader
	}
	return nil
}

// markTestPendingLocked transitions test to PENDING with the given reason and
// broadcasts the change. Shared by the start-gate evaluators so the pending
// transition semantics live in one place. The caller must hold tr.mu.
func (tr *TestRunner) markTestPendingLocked(test *ManagedTest, reason string) error {
	return tr.markTestPendingLockedWithExpectedTerm(test, reason, nil)
}

func (tr *TestRunner) markTestPendingLockedWithExpectedTerm(test *ManagedTest, reason string, expectedTerm *executionLeaseTerm) error {
	if test == nil {
		return errors.New("test is required")
	}
	claimed, err := tr.claimPassiveExecutionFenceLocked(test, expectedTerm)
	if err != nil {
		return err
	}
	if claimed {
		defer releasePassiveExecutionFenceLocked(test)
	}
	operationExecutionFence := test.executionFence
	operationExecutionGeneration := test.executionGeneration
	operationIdentity, _ := queuedStartRecoveryIdentityForTest(test)
	previousStatus := test.Status
	previousPendingSince := cloneTimePtr(test.PendingSince)
	previousPendingReason := test.PendingReason
	previousQueuePosition := test.QueuePosition
	previousError := test.Error
	previousStatusForCAS := strings.ToUpper(strings.TrimSpace(previousStatus))
	now := time.Now()
	test.Status = "PENDING"
	if test.PendingSince == nil || test.PendingSince.IsZero() {
		test.PendingSince = &now
	}
	test.PendingReason = reason
	test.QueuePosition = 0
	test.Error = ""
	rollback := func() {
		test.Status = previousStatus
		test.PendingSince = previousPendingSince
		test.PendingReason = previousPendingReason
		test.QueuePosition = previousQueuePosition
		test.Error = previousError
	}
	if previousStatusForCAS == "PENDING" {
		// pending_reason and queue position are process-local projections today;
		// pending_since is already durable. Do not issue another durable lifecycle
		// CAS with the revision captured by the original QUEUED->PENDING decision,
		// which would retry forever after unrelated admission content advances.
		tr.broadcastEvent("test_pending", cloneManagedTestLifecycleEvent(test))
		return nil
	}
	if tr.testStore != nil {
		pendingStore, ok := tr.testStore.(interface {
			TransitionTestToPending(storage.TestPendingTransition) (storage.TestStatusTransitionResult, error)
		})
		if !ok {
			rollback()
			return errors.New("test persistence does not support identity-fenced pending transitions")
		}
		transition := storage.TestPendingTransition{
			ID:                             test.ID,
			RefKey:                         test.RefKey,
			ProjectID:                      test.ProjectID,
			StartTime:                      test.CreatedAt,
			ExpectedStatus:                 previousStatusForCAS,
			PendingSince:                   *test.PendingSince,
			StartAuthorization:             startAuthorizationForStorage(test.StartAuthorization),
			StartedBySubject:               test.StartedBySubject,
			StartedByDisplayName:           test.StartedByDisplayName,
			ExpectedStartAdmissionRevision: test.expectedStartAdmissionRevision,
		}
		if tr.durableExecutionFence {
			transition.ExecutionLeaseName = executionLeaseName
			transition.ExecutionLeaseOwnerID = test.executionFence
			transition.ExecutionLeaseGeneration = test.executionGeneration
		}
		operationToken := test.passiveOperationToken
		// The targeted transition is guarded by the passive-operation token and
		// durable identity/lease CAS, so storage latency must not hold tr.mu.
		tr.mu.Unlock()
		result, persistErr := pendingStore.TransitionTestToPending(transition)
		var appliedPendingRecord *storage.TestRecord
		if persistErr == nil && result == storage.TestStatusTransitionUnchanged {
			stored, readErr := tr.testStore.GetTest(test.ID)
			if errors.Is(readErr, sql.ErrNoRows) {
				result = storage.TestStatusTransitionMissing
			} else if readErr != nil {
				persistErr = readErr
			} else if stored == nil || stored.ID != transition.ID || strings.TrimSpace(stored.RefKey) != transition.RefKey ||
				!strings.EqualFold(strings.TrimSpace(stored.Status), "PENDING") || stored.PendingSince == nil {
				result = storage.TestStatusTransitionFenced
			} else {
				appliedPendingRecord = stored
			}
		}
		tr.mu.Lock()
		if tr.tests[test.ID] != test || test.passiveOperationToken != operationToken ||
			!strings.EqualFold(strings.TrimSpace(test.Status), "PENDING") {
			// The transition may already be durable even though a successor-term
			// reload changed this process-local incarnation/fence while the storage
			// response was in flight. Retire any token we still own and force the
			// successor to inspect durable state; otherwise its acquire-time scan may
			// have skipped this row while the token was present.
			if tr.tests[test.ID] == test && test.passiveOperationToken == operationToken {
				test.passiveOperationToken = ""
				if tr.durableExecutionFence && !tr.executionLeaseTermLocallyCurrent(executionLeaseTerm{
					ownerID: operationExecutionFence, generation: operationExecutionGeneration,
				}) {
					test.executionFence = ""
					test.executionGeneration = 0
				}
			}
			tr.schedulePassiveTransitionRescanAfterTermChangeLocked(test.ID, operationIdentity, operationExecutionFence, operationExecutionGeneration)
			return errors.Join(errStartAdmissionEvidenceStale, persistErr)
		}
		if persistErr != nil {
			rollback()
			if claimed {
				releasePassiveExecutionFenceLocked(test)
			}
			tr.schedulePassiveTransitionRescanAfterTermChangeLocked(test.ID, operationIdentity, operationExecutionFence, operationExecutionGeneration)
			if errors.Is(persistErr, storage.ErrTestStartAdmissionRevisionChanged) {
				return errStartAdmissionEvidenceStale
			}
			return classifyStartTransitionPersistenceError(fmt.Errorf("persist pending test state: %w", persistErr))
		}
		switch result {
		case storage.TestStatusTransitionUpdated:
			// The local projection already carries the requested state.
		case storage.TestStatusTransitionUnchanged:
			// The original write committed but its response was lost. Adopt the
			// authoritative server-generated timestamp before publishing so local
			// and durable queue state converge exactly.
			test.PendingSince = cloneTimePtr(appliedPendingRecord.PendingSince)
			test.StartAuthorization = decodeStoredStartAuthorization(appliedPendingRecord.StartAuthorization)
			test.StartedBySubject = strings.TrimSpace(appliedPendingRecord.StartedBySubject)
			test.StartedByDisplayName = strings.TrimSpace(appliedPendingRecord.StartedByDisplayName)
		case storage.TestStatusTransitionMissing:
			rollback()
			tr.evictMissingManagedTestIdentityLocked(test.ID, test)
			tr.schedulePassiveTransitionRescanAfterTermChangeLocked(test.ID, operationIdentity, operationExecutionFence, operationExecutionGeneration)
			return fmt.Errorf("%w: test %s disappeared before pending transition", errStoredTestMissing, test.ID)
		default:
			rollback()
			if claimed {
				releasePassiveExecutionFenceLocked(test)
			}
			tr.schedulePassiveTransitionRescanAfterTermChangeLocked(test.ID, operationIdentity, operationExecutionFence, operationExecutionGeneration)
			return errStartAdmissionEvidenceStale
		}
	}
	tr.schedulePassiveTransitionRescanAfterTermChangeLocked(test.ID, operationIdentity, operationExecutionFence, operationExecutionGeneration)
	tr.broadcastEvent("test_pending", cloneManagedTestLifecycleEvent(test))
	return nil
}

// claimPassiveExecutionFenceLocked gives a single state-transition operation
// exclusive ownership of a row that has no live runtime. The durable lease
// token identifies a controller term, not an individual goroutine, so an
// already-present same-term token must be treated as busy rather than adopted:
// another terminalization may be persisting outside tr.mu with that token.
func (tr *TestRunner) claimPassiveExecutionFenceLocked(test *ManagedTest, expectedTerm *executionLeaseTerm) (bool, error) {
	if tr == nil || test == nil {
		return false, nil
	}
	if test.passiveOperationToken != "" || (tr.durableExecutionFence && test.executionFence != "") {
		return false, errStartAdmissionEvidenceStale
	}
	test.passiveOperationToken = uuid.NewString()
	if !tr.durableExecutionFence {
		return true, nil
	}
	var err error
	if expectedTerm == nil {
		err = tr.claimTestExecutionFence(test)
	} else {
		err = tr.claimTestExecutionFenceWithTerm(test, *expectedTerm)
	}
	if err != nil {
		test.passiveOperationToken = ""
	}
	return err == nil, err
}

// markTestInitializingLocked transitions test to INITIALIZING, clears its pending
// fields, and binds its execution backend. The caller persists the record because
// the save timing differs (under the lock for prepare, after unlock for promote).
func (tr *TestRunner) markTestInitializingLocked(test *ManagedTest) error {
	return tr.markTestInitializingLockedWithExpectedTerm(test, nil)
}

func (tr *TestRunner) markTestInitializingLockedWithExpectedTerm(test *ManagedTest, expectedTerm *executionLeaseTerm) error {
	if test == nil {
		return fmt.Errorf("test is required")
	}
	if test.runtimeHydrationError != "" {
		return fmt.Errorf("test runtime could not be restored: %s", test.runtimeHydrationError)
	}
	previousExecutionFence := test.executionFence
	previousExecutionGeneration := test.executionGeneration
	previousPassiveOperationToken := test.passiveOperationToken
	claimed, err := tr.claimPassiveExecutionFenceLocked(test, expectedTerm)
	if err != nil {
		return err
	}
	if err := tr.installTestExecutionContextLocked(test, expectedTerm); err != nil {
		if claimed {
			test.executionFence = previousExecutionFence
			test.executionGeneration = previousExecutionGeneration
			test.passiveOperationToken = previousPassiveOperationToken
		}
		return err
	}
	test.Status = "INITIALIZING"
	test.PendingSince = nil
	test.PendingReason = ""
	test.QueuePosition = 0
	test.Error = ""
	test.executionBackend = tr.execution
	if test.executionBackend == nil {
		test.executionBackend = localExecutionBackend{}
	}
	return nil
}

// startOptionsWithAuthorization reconstructs the explicit start-gate choices
// that were durably recorded before a test entered the fair-share queue. The
// capacity grant is intentionally bounded: the workload must be unchanged and
// neither the recommended fleet nor the shortfall may be worse than what the
// operator approved.
func startOptionsWithAuthorization(test *ManagedTest, opts StartTestOptions) StartTestOptions {
	if test == nil || test.StartAuthorization == nil {
		return opts
	}
	if capacityStartAuthorizationAllows(test.StartAuthorization.CapacityShortfall, test.CapacityAdvisory) {
		opts.OverrideCapacityShortfall = true
	}
	if scriptShakeoutStartAuthorizationAllows(test.StartAuthorization.ScriptShakeout) {
		opts.OverrideScriptShakeoutError = true
	}
	return opts
}

func capacityStartAuthorizationAllows(authorization *CapacityShortfallStartAuthorization, advisory *CapacityAdvisory) bool {
	if authorization == nil || advisory == nil || !advisory.RequiresOverride || authorization.ApprovedAt.IsZero() {
		return false
	}
	return advisory.TotalVUsers == authorization.TotalVUsers &&
		advisory.RecommendedGenerators <= authorization.RecommendedGenerators &&
		advisory.ShortfallGenerators <= authorization.ShortfallGenerators
}

func scriptShakeoutStartAuthorizationAllows(authorization *ScriptShakeoutStartAuthorization) bool {
	return authorization != nil && !authorization.ApprovedAt.IsZero() && strings.TrimSpace(authorization.ReviewEvidenceSHA256) != ""
}

func scriptShakeoutPassAllows(evidence *ScriptShakeoutPassEvidence) bool {
	return evidence != nil && !evidence.PassedAt.IsZero() && strings.TrimSpace(evidence.EvidenceSHA256) != ""
}

func applyScriptShakeoutPass(test *ManagedTest, fingerprint scriptShakeoutInputFingerprint, report ScriptShakeoutReport, term executionLeaseTerm) {
	if test == nil {
		return
	}
	authorization := cloneQueuedStartAuthorization(test.StartAuthorization)
	if authorization == nil {
		authorization = &QueuedStartAuthorization{}
	}
	passedAt := report.EndedAt.UTC()
	if passedAt.IsZero() {
		passedAt = time.Now().UTC()
	}
	authorization.ScriptShakeoutPass = &ScriptShakeoutPassEvidence{
		PassedAt:             passedAt,
		EvidenceSHA256:       strings.TrimSpace(fingerprint.EvidenceSHA256),
		ReusableAfterRestart: fingerprint.ReusableAfterRestart,
	}
	test.StartAuthorization = authorization
	test.validatedScriptShakeoutPass = strings.TrimSpace(fingerprint.ProcessSHA256)
	test.validatedScriptShakeoutTerm = term
}

type scriptShakeoutInputFingerprint struct {
	EvidenceSHA256       string
	ProcessSHA256        string
	ReusableAfterRestart bool
}

type scriptShakeoutExecutableSnapshot struct {
	Version            int                               `json:"version"`
	ProjectID          string                            `json:"project_id"`
	ApplicationID      string                            `json:"application_id,omitempty"`
	ScenarioMode       string                            `json:"scenario_mode,omitempty"`
	Groups             any                               `json:"groups"`
	DataPools          []storage.ScenarioDataPoolBinding `json:"data_pools,omitempty"`
	RuntimeSettings    *RuntimeSettingsOverride          `json:"runtime_settings,omitempty"`
	RuntimeSecretNames [][]string                        `json:"runtime_secret_names,omitempty"`
	SecretSources      []scriptShakeoutSecretSource      `json:"secret_sources,omitempty"`
}

type scriptShakeoutSecretSource struct {
	ProjectID     string `json:"project_id"`
	Scope         string `json:"scope,omitempty"`
	Name          string `json:"name"`
	Provider      string `json:"provider,omitempty"`
	UpdatedAtNano int64  `json:"updated_at_unix_nano,omitempty"`
}

type scriptShakeoutPrivateSnapshot struct {
	Version             int                               `json:"version"`
	ProjectID           string                            `json:"project_id"`
	ApplicationID       string                            `json:"application_id,omitempty"`
	ScenarioMode        string                            `json:"scenario_mode,omitempty"`
	Groups              any                               `json:"groups"`
	DataPools           []storage.ScenarioDataPoolBinding `json:"data_pools,omitempty"`
	RuntimeSettings     *RuntimeSettingsOverride          `json:"runtime_settings,omitempty"`
	RuntimeSecretValues []map[string]string               `json:"runtime_secret_values,omitempty"`
	ResolvedSecrets     []scriptShakeoutResolvedSecret    `json:"resolved_secrets,omitempty"`
}

type scriptShakeoutResolvedSecret struct {
	ProjectID string `json:"project_id"`
	Name      string `json:"name"`
	Value     string `json:"value"`
}

// scriptShakeoutExecutableFingerprint produces two proofs. EvidenceSHA256 is a
// disclosure-safe durable digest over masked executable inputs, authored secret
// names, and value-free provider versions. ProcessSHA256 is an HMAC over the
// exact inputs and resolved values using an ephemeral controller key. Thus an
// unversioned env/runtime secret can proceed in one live term without exposing
// a password oracle, while only fully versioned evidence is reusable after a
// restart or takeover.
func (tr *TestRunner) scriptShakeoutExecutableFingerprint(id string) (scriptShakeoutInputFingerprint, error) {
	tr.mu.RLock()
	id = tr.resolveRefKeyLocked(id)
	test := tr.tests[id]
	if test == nil {
		tr.mu.RUnlock()
		return scriptShakeoutInputFingerprint{}, fmt.Errorf("test not found: %s", id)
	}
	groups := decodeStoredGroups(encodeStoredGroups(test.Groups))
	runtimeSecretValues := make([]map[string]string, len(test.Groups))
	runtimeSecretNames := make([][]string, len(test.Groups))
	for index, group := range test.Groups {
		runtimeSecretValues[index] = cloneRuntimeSecretValues(group.runtimeSecretValues)
		for name := range group.runtimeSecretValues {
			runtimeSecretNames[index] = append(runtimeSecretNames[index], strings.TrimSpace(name))
		}
		sort.Strings(runtimeSecretNames[index])
	}
	projectID := normalizeProjectID(test.ProjectID)
	applicationID := runSecretApplicationID(test)
	pinnedSecretValues := test.pinnedSecrets.snapshot()
	scenarioMode := test.ScenarioMode
	dataPools := cloneScenarioDataPoolBindings(test.DataPools)
	runtimeSettings := cloneRuntimeSettings(test.RuntimeSettings)
	secretStore := tr.projectSecretStore
	applicationSecretStore := tr.applicationSecretStore
	fingerprintKey := tr.scriptShakeoutFingerprintKey
	tr.mu.RUnlock()
	if applicationID != "" && tr.applicationStore != nil {
		application, err := tr.applicationStore.GetApplication(applicationID)
		if err != nil || application == nil || normalizeProjectID(application.ProjectID) != projectID {
			return scriptShakeoutInputFingerprint{}, fmt.Errorf("selected application is unavailable for script shakeout")
		}
	}

	publicGroups := decodeStoredGroups(encodeStoredGroups(groups))
	for index := range publicGroups {
		publicGroups[index].RuntimeSettings = maskRuntimeSettings(publicGroups[index].RuntimeSettings)
		publicGroups[index].script = maskScriptSecrets(publicGroups[index].script)
		publicGroups[index].baseScript = maskScriptSecrets(publicGroups[index].baseScript)
		if len(publicGroups[index].sharedDataSources) > 0 {
			masked := maskScriptSecrets(&dsl.Script{DataSources: cloneDataSources(publicGroups[index].sharedDataSources)})
			if masked != nil {
				publicGroups[index].sharedDataSources = masked.DataSources
			}
		}
	}
	publicSnapshot := scriptShakeoutExecutableSnapshot{
		Version:            2,
		ProjectID:          projectID,
		ApplicationID:      applicationID,
		ScenarioMode:       scenarioMode,
		Groups:             encodeStoredGroups(publicGroups),
		DataPools:          dataPools,
		RuntimeSettings:    maskRuntimeSettings(runtimeSettings),
		RuntimeSecretNames: runtimeSecretNames,
	}
	privateSnapshot := scriptShakeoutPrivateSnapshot{
		Version:             2,
		ProjectID:           projectID,
		ApplicationID:       applicationID,
		ScenarioMode:        scenarioMode,
		Groups:              encodeStoredGroups(groups),
		DataPools:           dataPools,
		RuntimeSettings:     runtimeSettings,
		RuntimeSecretValues: runtimeSecretValues,
	}

	refsByProject := make(map[string]map[string]struct{})
	overlayByProject := make(map[string]map[string]string)
	pinnedByProject := make(map[string]map[string]string)
	for index, group := range groups {
		groupProjectID := projectID
		if group.script != nil && strings.TrimSpace(group.script.ProjectID) != "" {
			groupProjectID = normalizeProjectID(group.script.ProjectID)
		}
		if refsByProject[groupProjectID] == nil {
			refsByProject[groupProjectID] = make(map[string]struct{})
		}
		if overlayByProject[groupProjectID] == nil {
			overlayByProject[groupProjectID] = make(map[string]string)
		}
		if pinnedByProject[groupProjectID] == nil {
			pinnedByProject[groupProjectID] = make(map[string]string)
		}
		for name, value := range pinnedSecretValues[index] {
			if prior, exists := pinnedByProject[groupProjectID][name]; exists && prior != value {
				return scriptShakeoutInputFingerprint{}, fmt.Errorf("secret %q has conflicting pinned values", name)
			}
			pinnedByProject[groupProjectID][name] = value
		}
		if group.script != nil {
			names, err := engine.ReferencedSecretNames(group.script)
			if err != nil {
				return scriptShakeoutInputFingerprint{}, fmt.Errorf("collect executable secret references for group %q: %w", group.Name, err)
			}
			for _, name := range names {
				refsByProject[groupProjectID][strings.TrimSpace(name)] = struct{}{}
			}
		}
		if index < len(runtimeSecretValues) {
			for name, value := range runtimeSecretValues[index] {
				name = strings.TrimSpace(name)
				refsByProject[groupProjectID][name] = struct{}{}
				if prior, exists := overlayByProject[groupProjectID][name]; exists && prior != value {
					return scriptShakeoutInputFingerprint{}, fmt.Errorf("runtime secret %q has conflicting executable values", name)
				}
				overlayByProject[groupProjectID][name] = value
			}
		}
	}

	reusable := true
	projectIDs := make([]string, 0, len(refsByProject))
	for sourceProjectID, names := range refsByProject {
		if len(names) > 0 {
			projectIDs = append(projectIDs, sourceProjectID)
		}
	}
	sort.Strings(projectIDs)
	for _, sourceProjectID := range projectIDs {
		names := make([]string, 0, len(refsByProject[sourceProjectID]))
		for name := range refsByProject[sourceProjectID] {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			value, hasOverlay := overlayByProject[sourceProjectID][name]
			pinned, hasPin := pinnedByProject[sourceProjectID][name]
			if hasPin {
				value = pinned
			}
			metadataProvider := ""
			metadataUpdatedAt := time.Time{}
			sourceScope := ""
			foundSource := false
			if applicationID != "" && applicationSecretStore != nil {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				storedValue, metadata, getErr := applicationSecretStore.Get(ctx, storage.ApplicationSecretScopeID(applicationID), name)
				cancel()
				if getErr == nil && engine.IsUsableResolvedSecretValue(storedValue) {
					foundSource = true
					sourceScope = "application"
					metadataProvider = strings.TrimSpace(metadata.Provider)
					metadataUpdatedAt = metadata.UpdatedAt.UTC()
					if value == "" {
						value = storedValue
					} else if !hmac.Equal([]byte(value), []byte(storedValue)) {
						metadataUpdatedAt = time.Time{}
					}
				} else if getErr != nil && !errors.Is(getErr, secretstore.ErrNotFound) {
					return scriptShakeoutInputFingerprint{}, &startSecretRequirementError{
						message:   fmt.Sprintf("resolve secret source %q in application %q: %v", name, applicationID, getErr),
						retryable: true, cause: getErr,
					}
				}
			}
			if !foundSource && secretStore != nil {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				storedValue, metadata, getErr := secretStore.Get(ctx, sourceProjectID, name)
				cancel()
				if getErr == nil && engine.IsUsableResolvedSecretValue(storedValue) {
					foundSource = true
					sourceScope = "project"
					metadataProvider = strings.TrimSpace(metadata.Provider)
					metadataUpdatedAt = metadata.UpdatedAt.UTC()
					if value != "" || hasOverlay {
						// Provider version metadata only attests to the provider's own
						// value. A same-name execution overlay must not borrow that
						// provenance unless it is exactly the attested value.
						if !hmac.Equal([]byte(value), []byte(storedValue)) {
							metadataUpdatedAt = time.Time{}
						}
					} else {
						value = storedValue
					}
				} else if getErr != nil && !errors.Is(getErr, secretstore.ErrNotFound) {
					// An execution overlay can run without the provider value, but its
					// reusable evidence still depends on knowing whether that value is
					// the exact provider-attested version. Treat provider outages as
					// incomplete evidence even when an overlay is present; otherwise a
					// post-action outage looks like a real version rotation and falsely
					// turns a completed one-shot check into review.
					return scriptShakeoutInputFingerprint{}, &startSecretRequirementError{
						message:   fmt.Sprintf("resolve secret source %q in project %q: %v", name, sourceProjectID, getErr),
						retryable: true,
						cause:     getErr,
					}
				}
			}
			if hasPin && !foundSource {
				ctx, cancel := engine.WithSecretResolutionBudget(context.Background())
				current, ok := engine.ResolveSecret(ctx, tr.secretResolverForScope(sourceProjectID, applicationID), name)
				cancel()
				if !ok {
					return scriptShakeoutInputFingerprint{}, &startSecretRequirementError{
						message:   fmt.Sprintf("resolve executable secret %q in project %q", name, sourceProjectID),
						retryable: true,
					}
				}
				if !hmac.Equal([]byte(current), []byte(pinned)) {
					sourceScope = "changed_unversioned"
				}
			}
			if value == "" {
				ctx, cancel := engine.WithSecretResolutionBudget(context.Background())
				resolved, ok := engine.ResolveSecret(ctx, tr.secretResolverForScope(sourceProjectID, applicationID), name)
				cancel()
				if !ok {
					return scriptShakeoutInputFingerprint{}, &startSecretRequirementError{
						message:   fmt.Sprintf("resolve executable secret %q in project %q", name, sourceProjectID),
						retryable: true,
					}
				}
				value = resolved
			}
			if metadataUpdatedAt.IsZero() {
				reusable = false
				if metadataProvider == "" {
					if applicationID != "" && applicationSecretStore != nil {
						metadataProvider = strings.TrimSpace(applicationSecretStore.Provider())
					} else if secretStore != nil {
						metadataProvider = strings.TrimSpace(secretStore.Provider())
					}
				}
			}
			updatedAtNano := int64(0)
			if !metadataUpdatedAt.IsZero() {
				updatedAtNano = metadataUpdatedAt.UnixNano()
			}
			publicSnapshot.SecretSources = append(publicSnapshot.SecretSources, scriptShakeoutSecretSource{
				ProjectID: sourceProjectID, Scope: sourceScope, Name: name, Provider: metadataProvider, UpdatedAtNano: updatedAtNano,
			})
			privateSnapshot.ResolvedSecrets = append(privateSnapshot.ResolvedSecrets, scriptShakeoutResolvedSecret{
				ProjectID: sourceProjectID, Name: name, Value: value,
			})
		}
	}

	publicEncoded, err := json.Marshal(publicSnapshot)
	if err != nil {
		return scriptShakeoutInputFingerprint{}, fmt.Errorf("encode disclosure-safe script shakeout snapshot: %w", err)
	}
	privateEncoded, err := json.Marshal(privateSnapshot)
	if err != nil {
		return scriptShakeoutInputFingerprint{}, fmt.Errorf("encode private script shakeout snapshot: %w", err)
	}
	mac := hmac.New(sha256.New, fingerprintKey[:])
	_, _ = mac.Write(privateEncoded)
	processSHA256 := hex.EncodeToString(mac.Sum(nil))
	clear(privateEncoded)
	for index := range privateSnapshot.RuntimeSecretValues {
		clearRuntimeSecretValueMap(privateSnapshot.RuntimeSecretValues[index])
	}
	for index := range privateSnapshot.ResolvedSecrets {
		privateSnapshot.ResolvedSecrets[index].Value = ""
	}
	return scriptShakeoutInputFingerprint{
		EvidenceSHA256:       sha256String(string(publicEncoded)),
		ProcessSHA256:        processSHA256,
		ReusableAfterRestart: reusable,
	}, nil
}

func (tr *TestRunner) validateScriptShakeoutPassForStart(id string, opts StartTestOptions, expectedTerm *executionLeaseTerm) (bool, error) {
	tr.mu.RLock()
	test := tr.tests[id]
	if err := validateExpectedStartRecoveryIdentityLocked(test, opts); err != nil {
		tr.mu.RUnlock()
		return false, err
	}
	var evidence *ScriptShakeoutPassEvidence
	validatedProcessSHA256 := ""
	validatedTerm := executionLeaseTerm{}
	if test != nil && test.StartAuthorization != nil && test.StartAuthorization.ScriptShakeoutPass != nil {
		copy := *test.StartAuthorization.ScriptShakeoutPass
		evidence = &copy
		validatedProcessSHA256 = test.validatedScriptShakeoutPass
		validatedTerm = test.validatedScriptShakeoutTerm
	}
	tr.mu.RUnlock()
	if tr.durableExecutionFence && (expectedTerm == nil || validatedTerm != *expectedTerm) {
		validatedProcessSHA256 = ""
	}
	if !scriptShakeoutPassAllows(evidence) {
		return false, nil
	}
	if validatedProcessSHA256 == "" && !evidence.ReusableAfterRestart {
		tr.mu.RLock()
		overrideAllowed := startOptionsWithAuthorization(tr.tests[id], opts).OverrideScriptShakeoutError
		tr.mu.RUnlock()
		if overrideAllowed {
			tr.mu.Lock()
			if current := tr.tests[id]; current != nil {
				if err := validateExpectedStartRecoveryIdentityLocked(current, opts); err != nil {
					tr.mu.Unlock()
					return false, err
				}
				clearScriptShakeoutPass(current)
			}
			tr.mu.Unlock()
			return false, nil
		}
		report := ScriptShakeoutReport{
			Passed:  false,
			Summary: "A prior script shakeout used an unversioned secret source and cannot be trusted after controller restart or takeover. Continue explicitly or run a newly materialized test; shakeout actions were not replayed.",
		}
		return false, &ScriptShakeoutGateError{TestID: id, Report: &report}
	}
	fingerprint, err := tr.scriptShakeoutExecutableFingerprint(id)
	if err != nil {
		return false, err
	}

	tr.mu.Lock()
	defer tr.mu.Unlock()
	test = tr.tests[id]
	if err := validateExpectedStartRecoveryIdentityLocked(test, opts); err != nil {
		return false, err
	}
	if test == nil || test.StartAuthorization == nil || test.StartAuthorization.ScriptShakeoutPass == nil {
		return false, nil
	}
	current := test.StartAuthorization.ScriptShakeoutPass
	if current.PassedAt != evidence.PassedAt || current.EvidenceSHA256 != evidence.EvidenceSHA256 || current.ReusableAfterRestart != evidence.ReusableAfterRestart {
		return false, errStartAdmissionEvidenceStale
	}
	if fingerprint.EvidenceSHA256 != strings.TrimSpace(current.EvidenceSHA256) ||
		(validatedProcessSHA256 != "" && fingerprint.ProcessSHA256 != validatedProcessSHA256) {
		clearScriptShakeoutAuthorizations(test)
		return false, nil
	}
	test.validatedScriptShakeoutPass = fingerprint.ProcessSHA256
	if expectedTerm != nil {
		test.validatedScriptShakeoutTerm = *expectedTerm
	} else {
		test.validatedScriptShakeoutTerm = executionLeaseTerm{}
	}
	return true, nil
}

func clearScriptShakeoutPass(test *ManagedTest) {
	if test == nil {
		return
	}
	if test.StartAuthorization != nil {
		authorization := cloneQueuedStartAuthorization(test.StartAuthorization)
		authorization.ScriptShakeoutPass = nil
		if authorization.CapacityShortfall == nil && authorization.ScriptShakeout == nil {
			test.StartAuthorization = nil
		} else {
			test.StartAuthorization = authorization
		}
	}
	test.validatedScriptShakeoutPass = ""
	test.validatedScriptShakeoutTerm = executionLeaseTerm{}
}

func clearScriptShakeoutAuthorizations(test *ManagedTest) {
	if test == nil {
		return
	}
	if test.StartAuthorization != nil {
		authorization := cloneQueuedStartAuthorization(test.StartAuthorization)
		authorization.ScriptShakeout = nil
		authorization.ScriptShakeoutPass = nil
		if authorization.CapacityShortfall == nil {
			test.StartAuthorization = nil
		} else {
			test.StartAuthorization = authorization
		}
	}
	test.validatedScriptShakeoutPass = ""
	test.validatedScriptShakeoutTerm = executionLeaseTerm{}
}

// applyRequestedStartAuthorizations captures only choices made on the current
// API/start attempt. Promotion reconstructs options from this snapshot but never
// refreshes its approval time or expands its bounds on behalf of the operator.
func applyRequestedStartAuthorizations(test *ManagedTest, opts StartTestOptions, approvedAt time.Time) {
	if test == nil || (!opts.OverrideCapacityShortfall && !opts.OverrideScriptShakeoutError) {
		return
	}
	authorization := cloneQueuedStartAuthorization(test.StartAuthorization)
	if authorization == nil {
		authorization = &QueuedStartAuthorization{}
	}
	subject, displayName := startAuthorizationActor(test, opts.actor)
	approvedAt = approvedAt.UTC()
	if opts.OverrideCapacityShortfall && test.CapacityAdvisory != nil && test.CapacityAdvisory.RequiresOverride {
		advisory := test.CapacityAdvisory
		authorization.CapacityShortfall = &CapacityShortfallStartAuthorization{
			ApprovedAt:            approvedAt,
			ApprovedBySubject:     subject,
			ApprovedByDisplayName: displayName,
			TotalVUsers:           advisory.TotalVUsers,
			RecommendedGenerators: advisory.RecommendedGenerators,
			AvailableGenerators:   advisory.AvailableGenerators,
			ShortfallGenerators:   advisory.ShortfallGenerators,
		}
	}
	if opts.OverrideScriptShakeoutError && test.ScriptShakeout {
		authorization.ScriptShakeout = &ScriptShakeoutStartAuthorization{
			ApprovedAt:            approvedAt,
			ApprovedBySubject:     subject,
			ApprovedByDisplayName: displayName,
			ReviewEvidenceSHA256:  sha256String(strings.TrimSpace(test.Error)),
		}
	}
	if authorization.CapacityShortfall == nil && authorization.ScriptShakeout == nil && authorization.ScriptShakeoutPass == nil {
		return
	}
	test.StartAuthorization = authorization
}

func startAuthorizationActor(test *ManagedTest, actor testCreationActor) (string, string) {
	subject := strings.TrimSpace(actor.Subject)
	displayName := strings.TrimSpace(actor.DisplayName)
	if subject == "" && test != nil {
		subject = strings.TrimSpace(test.StartedBySubject)
		displayName = strings.TrimSpace(test.StartedByDisplayName)
	}
	if subject == "" && test != nil {
		subject = strings.TrimSpace(test.CreatedBySubject)
		displayName = strings.TrimSpace(test.CreatedByDisplayName)
	}
	return subject, displayName
}

// evaluateStartGatesLocked runs the capacity-advisory / governance / launch-window /
// quota gates for a start attempt. It returns blocked=true (after transitioning the
// test to PENDING) when a quota block applies, or an error when a gate rejects the
// start outright. The caller must hold tr.mu and have already validated startability.
func (tr *TestRunner) evaluateStartGatesLocked(id string, test *ManagedTest, opts StartTestOptions) (bool, error) {
	now := time.Now().UTC()
	window := launchWindowForTestAt(test, now, now)
	return tr.evaluateStartGatesLockedWithEvidence(id, test, opts, nil, now, startLaunchEvidence{
		launchWindow: tr.loadLaunchWindowEvidenceForProject(window, test.ProjectID, testAssignedPoolID(test)),
	})
}

func (tr *TestRunner) evaluateStartGatesLockedWithEvidence(id string, test *ManagedTest, opts StartTestOptions, expectedTerm *executionLeaseTerm, now time.Time, evidence startLaunchEvidence) (bool, error) {
	// A recovery spare cannot be admitted without pod reservations. Treat this
	// as a configuration conflict before quota checks can queue the run: that
	// queue would have no KEDA demand and could wait indefinitely.
	if err := recoverySpareSettingsError(test, tr.settings); err != nil {
		return false, err
	}
	for index, group := range test.groupSpecs {
		if issues := launchReviewGateIssues(group.Script); len(issues) > 0 {
			return false, fmt.Errorf("group[%d]: %s", index, strings.Join(issues, "; "))
		}
	}
	tr.refreshCapacityAdvisoryLocked(test)
	effectiveOpts := startOptionsWithAuthorization(test, opts)
	if err := buildStartGovernanceError(id, test.CapacityAdvisory, effectiveOpts); err != nil {
		return false, err
	}
	// Runtime availability and generator eligibility can change after a queued
	// or scheduled test was created. Revalidate at the actual start boundary so
	// an approved combined Java script never launches on a phase-only runner, or
	// because an unrelated capable generator exists outside its assignment.
	javaConflicts, placementConflicts, err := tr.startRuntimeGateConflictsLocked(test, evidence)
	if err != nil {
		return false, err
	}
	if len(javaConflicts) > 0 {
		conflict := javaConflicts[0]
		return false, fmt.Errorf("%s: %s", conflict.Code, conflict.Message)
	}
	if len(placementConflicts) > 0 {
		return false, &StartPlacementConflictError{Conflicts: placementConflicts}
	}
	// Unavailable durable evidence is neither quota pressure nor an
	// authoritative launch conflict. Return the typed retry signal before any
	// PENDING transition so callers install the appropriate recovery worker.
	if err := startLaunchEvidenceRetryError(test, now, evidence.launchWindow); err != nil {
		return false, err
	}
	// The Java runner gate above checks one group at a time. Project placement
	// across every group as a second step so two individually-valid groups
	// cannot both claim the same advertised JVM process slots. Re-run this at
	// StartTest because generator occupancy may have changed since preflight.
	if !opts.SkipLaunchWindowPreflight {
		if err := tr.launchStartWindowErrorWithEvidenceLocked(test, now, id, "", evidence.launchWindow); err != nil {
			return false, err
		}
	}
	// Re-validate a bound capacity timeslot at the actual launch instant even when
	// the launch-window preflight is skipped: the binding can be invalidated
	// (rejected/re-bound/resized) between create and start, and a scheduled run's
	// window can drift, so the bind-time fit can no longer hold.
	if err := tr.launchTimeslotRevalidationErrorWithEvidenceLocked(test, now, evidence.launchWindow); err != nil {
		return false, err
	}
	excludeTestID := ""
	if test.Status == "QUEUED" && testHasShakeoutAdmissionReservation(test) && test.shakeoutAdmissionReservation == opts.shakeoutAdmissionReservation {
		excludeTestID = id
	}
	if blocked, reason := tr.evaluateQuotaBlockWithLaunchEvidenceLocked(test, excludeTestID, now, evidence.launchWindow); blocked {
		authorizationSnapshot := cloneQueuedStartAuthorization(test.StartAuthorization)
		actorSnapshot, actorChanged := applyTestStartedByActor(test, opts.actor)
		applyRequestedStartAuthorizations(test, opts, time.Now().UTC())
		if err := tr.markTestPendingLockedWithExpectedTerm(test, reason, expectedTerm); err != nil {
			test.StartAuthorization = authorizationSnapshot
			if actorChanged {
				restoreTestStartedByActor(test, actorSnapshot)
			}
			return false, err
		}
		return true, nil
	}
	return false, nil
}

// startRuntimeGateConflictsLocked consumes preloaded Java/session/runtime
// evidence for live start paths. Legacy callers that operate on an already
// private runner snapshot leave runtimeGatesLoaded false and evaluate directly;
// no live global mutex or unrelated API request is blocked in that case.
func (tr *TestRunner) startRuntimeGateConflictsLocked(test *ManagedTest, evidence startLaunchEvidence) ([]LaunchConflict, []LaunchConflict, error) {
	if err := tr.validateStartLaunchEvidenceFenceLocked(evidence); err != nil {
		return nil, nil, err
	}
	if evidence.runtimeGatesLoaded {
		return evidence.javaConflicts, evidence.placementConflicts, nil
	}
	javaConflicts := tr.javaVuserRunnerLaunchConflictsForManagedTestLocked(test)
	var placementConflicts []LaunchConflict
	if tr.shouldCheckStartPlacement(test) {
		placementConflicts = tr.generatorPlacementLaunchConflictsLocked(test)
	}
	return javaConflicts, placementConflicts, nil
}

// validateStartLaunchEvidenceFenceLocked is deliberately side-effect free.
// Call it before claiming an execution epoch so a stale off-lock snapshot can
// never stamp a still-queued row with a lease token that a successor cannot
// reclaim. Callers may repeat it later as defense in depth while retaining the
// same runner lock.
func (tr *TestRunner) validateStartLaunchEvidenceFenceLocked(evidence startLaunchEvidence) error {
	if evidence.runtimeGatesLoaded && !tr.managedTestBuildFenceCurrentLocked(evidence.admissionFence) {
		return errStartAdmissionEvidenceStale
	}
	return nil
}

func (tr *TestRunner) preflightStartBlocksLocked(id string, opts StartTestOptions) (bool, error) {
	return tr.preflightStartBlocksLockedWithExpectedTerm(id, opts, nil)
}

func (tr *TestRunner) preflightStartBlocksLockedWithExpectedTerm(id string, opts StartTestOptions, expectedTerm *executionLeaseTerm) (bool, error) {
	now := time.Now().UTC()
	test := tr.tests[id]
	if test == nil {
		return false, fmt.Errorf("test not found: %s", id)
	}
	window := launchWindowForTestAt(test, now, now)
	return tr.preflightStartBlocksLockedWithExpectedTermAndEvidence(id, opts, expectedTerm, now, startLaunchEvidence{
		launchWindow: tr.loadLaunchWindowEvidenceForProject(window, test.ProjectID, testAssignedPoolID(test)),
	})
}

func (tr *TestRunner) preflightStartBlocksLockedWithExpectedTermAndEvidence(id string, opts StartTestOptions, expectedTerm *executionLeaseTerm, now time.Time, evidence startLaunchEvidence) (bool, error) {
	if err := tr.shutdownErrorLocked(); err != nil {
		return false, err
	}
	test, ok := tr.tests[id]
	if !ok {
		return false, fmt.Errorf("test not found: %s", id)
	}
	if err := validateExpectedStartRecoveryIdentityLocked(test, opts); err != nil {
		return false, err
	}
	if !testexec.IsStartableTestStatus(test.Status) {
		return false, testexec.StartableTestStatusError(test.Status)
	}
	if test.runtimeHydrationError != "" {
		return false, fmt.Errorf("test runtime could not be restored: %s", test.runtimeHydrationError)
	}
	if err := tr.projectDeletionErrorLocked(test.ProjectID); err != nil {
		return false, err
	}
	if err := tr.validateStartLaunchEvidenceFenceLocked(evidence); err != nil {
		return false, err
	}
	// Carry the exact project policy/quota revision into the transactional
	// SHAKEOUT/INITIALIZING save. The storage layer rechecks it while holding the
	// project row, closing the cross-replica read-to-commit race.
	test.expectedProjectUpdatedAt = evidence.launchWindow.projectQuota.updatedAt
	test.expectedStartAdmissionRevision = evidence.startAdmissionRevision
	// Preflight is side-effect free unless a quota block must be durably queued.
	// That transition claims its own exclusive passive-row fence immediately
	// around the save; do not stamp a term token merely to inspect admission.
	return tr.evaluateStartGatesLockedWithEvidence(id, test, opts, expectedTerm, now, evidence)
}

func releasePassiveExecutionFenceLocked(test *ManagedTest) {
	if !passiveExecutionFenceReleasable(test) {
		return
	}
	test.passiveOperationToken = ""
	test.executionFence = ""
	test.executionGeneration = 0
}

func releasePassiveOperationTokenLocked(test *ManagedTest) {
	if test != nil {
		test.passiveOperationToken = ""
	}
}

// schedulePassiveTransitionRescanAfterTermChangeLocked closes a narrow
// same-process HA handoff race. Acquire-time reload may have skipped a row while
// an old callback still carried its passive token; once that callback retires,
// a successor-term rescan is required because leaders intentionally do not run
// follower read-refreshes. The caller holds tr.mu; the goroutine cannot inspect
// the map until the caller publishes/clears the token and unlocks.
func (tr *TestRunner) schedulePassiveTransitionRescanAfterTermChangeLocked(testID string, identity queuedStartRecoveryIdentity, ownerID string, generation uint64) {
	if tr == nil || !identity.valid || !tr.durableExecutionFence || strings.TrimSpace(ownerID) == "" || generation == 0 ||
		tr.executionLeaseTermLocallyCurrent(executionLeaseTerm{ownerID: ownerID, generation: generation}) ||
		!tr.holdsExecutionLeadership() {
		return
	}
	go func() {
		if tr.beforePassiveTransitionRescan != nil {
			tr.beforePassiveTransitionRescan(testID)
		}
		var lastErr error
		for attempt := 0; attempt < durableTransitionAttempts; attempt++ {
			if !tr.holdsExecutionLeadership() {
				return
			}
			if err := tr.reloadStoredTests(); err == nil {
				term, termErr := tr.captureExecutionLeaseTerm()
				if termErr != nil {
					return
				}
				tr.mu.Lock()
				current := tr.tests[testID]
				if !identity.matches(current) {
					tr.mu.Unlock()
					return
				}
				status := ""
				if current != nil {
					adoptDurablePendingShakeoutOutcomeLocked(current)
					status = strings.ToUpper(strings.TrimSpace(current.Status))
				}
				tr.mu.Unlock()
				switch status {
				case "QUEUED", "SHAKEOUT", "SHAKEOUT_REVIEW":
					// Explicitly re-arm the exact retired identity. Startup covers CI
					// and correlated scheduled retries, but this post-token rescan also
					// closes a same-process handoff that the acquisition scan skipped.
					// Review is included because an ambiguous rejected shakeout may
					// have committed just before term turnover; recovery must publish
					// its CI failure or recurring-schedule correlation without replay.
					tr.enqueueQueuedCIRecoveryRetryForIdentityWithTerm(testID, identity, term)
				case "PENDING":
					tr.promotePendingTests()
				}
				return
			} else {
				lastErr = err
			}
			if attempt+1 < durableTransitionAttempts {
				time.Sleep(durableTransitionRetryDelay)
			}
		}
		slog.Warn("successor term could not rescan a retired passive transition", "error", lastErr)
	}()
}

// adoptDurablePendingShakeoutOutcomeLocked retires process-private outcome
// state when a successor reload has already adopted that outcome's exact
// durable destination. This is the lost-response case: the actions and write
// both completed, so attempting to persist or execute them again is incorrect.
func adoptDurablePendingShakeoutOutcomeLocked(test *ManagedTest) {
	if test == nil || test.pendingScriptShakeoutOutcome == nil {
		return
	}
	outcome := test.pendingScriptShakeoutOutcome
	desiredStatus := "SHAKEOUT_REVIEW"
	if outcome.Report.Passed {
		desiredStatus = strings.ToUpper(strings.TrimSpace(outcome.PassStatus))
		if desiredStatus == "" {
			desiredStatus = "QUEUED"
		}
	}
	if !strings.EqualFold(strings.TrimSpace(test.Status), desiredStatus) {
		return
	}
	test.pendingScriptShakeoutOutcome = nil
	clearScriptShakeoutContextLocked(test)
	releasePassiveExecutionFenceLocked(test)
}

// retireCommittedStagedStartLocked removes process-private ownership after the
// durable SHAKEOUT/INITIALIZING commit succeeded but this process may no longer
// publish or dispatch it. The durable row is intentionally left untouched; a
// successor-term reload can now adopt and reconcile it instead of skipping a
// permanently tokened STARTING handle.
func retireCommittedStagedStartLocked(test *ManagedTest, durableStatus string) {
	if test == nil {
		return
	}
	if test.cancel != nil {
		test.cancel()
	}
	test.cancel = nil
	test.executionCtx = nil
	test.executionBackend = nil
	test.Status = durableStatus
	test.passiveOperationToken = ""
	test.executionFence = ""
	test.executionGeneration = 0
}

// persistManagedTestStartOutsideLockLocked stages a process-local STARTING
// reservation, persists the immutable execution-capable state without tr.mu,
// then publishes it only while the exact row pointer and passive-operation
// token still own the transition. The caller must hold tr.mu on entry; this
// helper always returns with tr.mu held. owned=false means a concurrent reload
// or leadership transition displaced the staged identity and the caller must
// not roll fields back on that pointer.
func (tr *TestRunner) persistManagedTestStartOutsideLockLocked(test *ManagedTest, durableStatus string) (owned bool, err error) {
	if tr == nil || test == nil || strings.TrimSpace(test.passiveOperationToken) == "" {
		return true, errors.New("start persistence requires an owned passive operation")
	}
	durableStatus = strings.ToUpper(strings.TrimSpace(durableStatus))
	if durableStatus != "SHAKEOUT" && durableStatus != "INITIALIZING" {
		return true, fmt.Errorf("unsupported durable start status %q", durableStatus)
	}
	snapshot := newManagedTestStartPersistenceSnapshot(test)
	var freshAdmission *RunArchiveProvenance
	if durableStatus == "INITIALIZING" {
		fresh := test.initialArchiveProvenance == nil
		initial, err := tr.telemetryAdmissionProvenanceLocked(test)
		if err != nil {
			return true, err
		}
		// Keep the candidate exclusively in the private persistence projection.
		// Concurrent manifest readers must never see a receipt that has not won
		// the durable write, even while SQL waits outside the runner lock.
		snapshot.record.InitialProvenance = initialProvenanceForStorage(initial)
		if fresh {
			freshAdmission = initial
		}
	}
	operationToken := test.passiveOperationToken
	executionFence := test.executionFence
	executionGeneration := test.executionGeneration
	operationIdentity, _ := queuedStartRecoveryIdentityForTest(test)
	test.Status = "STARTING"

	tr.mu.Unlock()
	var persistErr error
	if freshAdmission != nil {
		// Policy pins need a store read, so they are resolved here, outside the
		// runner lock, and frozen into the same write-once admission receipt.
		// A resolution failure refuses Start: silently dropping a fail_ci
		// policy would let CI pass without the telemetry gate it configured.
		pins, pinErr := tr.resolveRunPolicyPins(freshAdmission.ProjectID)
		if errors.Is(pinErr, errRunPolicyConfiguration) {
			persistErr = pinErr
		} else if pinErr != nil {
			// A store read failure is retryable, like a failed receipt write.
			persistErr = errors.Join(errStartAdmissionEvidenceStale, fmt.Errorf("resolve run policies: %w", pinErr))
		} else {
			freshAdmission.PolicyPins = pins
			snapshot.record.InitialProvenance = initialProvenanceForStorage(freshAdmission)
		}
	}
	if persistErr == nil {
		persistErr = tr.saveManagedTestStartSnapshot(snapshot)
	}
	tr.mu.Lock()

	if tr.tests[test.ID] != test {
		// A reload/delete-recreate displaced this process-private handle while
		// storage waited. It can no longer be controlled through tr.tests, so
		// retire its context before returning the stale result.
		retireCommittedStagedStartLocked(test, durableStatus)
		return false, errStartAdmissionEvidenceStale
	}
	if test.passiveOperationToken != operationToken ||
		test.executionFence != executionFence || test.executionGeneration != executionGeneration ||
		!strings.EqualFold(strings.TrimSpace(test.Status), "STARTING") {
		return false, errStartAdmissionEvidenceStale
	}
	if persistErr != nil {
		if errors.Is(persistErr, storage.ErrTestStartIdentityChanged) {
			// The durable row was deleted or replaced while storage waited. Remove
			// only the exact stale pointer, then synchronously reload so a leader
			// (which does not run follower refreshes) cannot remain pinned to it.
			tr.evictMissingManagedTestIdentityLocked(test.ID, test)
			tr.mu.Unlock()
			reloadErr := tr.reloadStoredTests()
			tr.mu.Lock()
			if reloadErr != nil {
				return false, errors.Join(persistErr, fmt.Errorf("reload test after start identity change: %w", reloadErr))
			}
			return false, persistErr
		}
		return true, persistErr
	}
	if tr.shuttingDown {
		// The durable state committed, but shutdown may have canceled the staged
		// runtime while storage was blocked. Do not publish or dispatch it in this
		// process; make the handle reloadable so startup reconciliation can recover
		// the committed row.
		retireCommittedStagedStartLocked(test, durableStatus)
		return false, errRunnerShuttingDown
	}
	if tr.durableExecutionFence && !tr.executionLeaseTermLocallyCurrent(executionLeaseTerm{
		ownerID: executionFence, generation: executionGeneration,
	}) {
		// The durable transaction committed in the old epoch, but this process
		// may no longer publish or dispatch it. Retire the old staged handle so
		// acquisition recovery can adopt the committed row under the successor
		// term even when loss and reacquisition happen in the same process.
		retireCommittedStagedStartLocked(test, durableStatus)
		tr.schedulePassiveTransitionRescanAfterTermChangeLocked(test.ID, operationIdentity, executionFence, executionGeneration)
		return false, errNotExecutionLeader
	}
	if durableStatus == "INITIALIZING" {
		// SQL Start commits return the write-once winner through the owned
		// persistence record. Adopt only after exact pointer/token/term checks,
		// before publishing or dispatching the asynchronous execution backend.
		test.initialArchiveProvenance = decodeStoredInitialProvenance(snapshot.record.InitialProvenance)
	}
	test.Status = durableStatus
	return true, nil
}

func passiveExecutionFenceReleasable(test *ManagedTest) bool {
	return test != nil && test.executionCtx == nil && test.cancel == nil && testexec.IsStartableTestStatus(test.Status)
}

func (tr *TestRunner) beginScriptShakeoutIfNeeded(id string, opts StartTestOptions) (*ManagedTest, error) {
	return tr.beginScriptShakeoutIfNeededWithExpectedTerm(id, opts, nil)
}

func (tr *TestRunner) scriptShakeoutRequiredForStart(id string, opts StartTestOptions) bool {
	if opts.prepareOnly {
		return false
	}
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	test := tr.tests[id]
	if test == nil || !test.ScriptShakeout {
		return false
	}
	if test.StartAuthorization != nil && scriptShakeoutPassAllows(test.StartAuthorization.ScriptShakeoutPass) &&
		test.validatedScriptShakeoutPass != "" {
		return false
	}
	return !startOptionsWithAuthorization(test, opts).OverrideScriptShakeoutError
}

func (tr *TestRunner) beginScriptShakeoutIfNeededWithExpectedTerm(id string, opts StartTestOptions, expectedTerm *executionLeaseTerm) (*ManagedTest, error) {
	if !tr.scriptShakeoutRequiredForStart(id, opts) {
		return nil, nil
	}
	for attempt := 0; attempt < startAdmissionPhaseAttempts; attempt++ {
		if err := tr.validateExpectedStartTerm(expectedTerm); err != nil {
			return nil, err
		}
		shakeoutAt := time.Now().UTC()
		evidence, err := tr.loadStartLaunchEvidence(id, shakeoutAt)
		if err != nil {
			return nil, err
		}
		if err := tr.validateCIRunPolicyForStartAtProjectRevision(id, evidence.launchWindow.projectQuota.updatedAt); err != nil {
			return nil, err
		}
		if err := tr.validateExpectedStartTerm(expectedTerm); err != nil {
			return nil, err
		}
		tr.mu.Lock()
		shakeout, err := tr.beginScriptShakeoutWithEvidenceLocked(id, opts, expectedTerm, shakeoutAt, evidence)
		tr.mu.Unlock()
		if errors.Is(err, errStartAdmissionEvidenceStale) {
			continue
		}
		return shakeout, err
	}
	return nil, errStartAdmissionEvidenceStale
}

// beginScriptShakeoutWithEvidenceLocked atomically revalidates local active
// capacity/inventory and installs the SHAKEOUT reservation. Secret resolution
// and fingerprinting can take provider-scale time; without this final fence two
// starts can both execute one-shot actions from the same earlier snapshot.
func (tr *TestRunner) beginScriptShakeoutWithEvidenceLocked(id string, opts StartTestOptions, expectedTerm *executionLeaseTerm, now time.Time, evidence startLaunchEvidence) (*ManagedTest, error) {
	if err := tr.shutdownErrorLocked(); err != nil {
		return nil, err
	}

	test, ok := tr.tests[id]
	if !ok {
		return nil, fmt.Errorf("test not found: %s", id)
	}
	if err := validateExpectedStartRecoveryIdentityLocked(test, opts); err != nil {
		return nil, err
	}
	if !test.ScriptShakeout {
		return nil, nil
	}
	if test.StartAuthorization != nil && scriptShakeoutPassAllows(test.StartAuthorization.ScriptShakeoutPass) &&
		test.validatedScriptShakeoutPass != "" {
		return nil, nil
	}
	if startOptionsWithAuthorization(test, opts).OverrideScriptShakeoutError {
		return nil, nil
	}
	if !testexec.IsStartableTestStatus(test.Status) {
		return nil, testexec.StartableTestStatusError(test.Status)
	}
	if err := tr.projectDeletionErrorLocked(test.ProjectID); err != nil {
		return nil, err
	}
	if err := tr.validateStartLaunchEvidenceFenceLocked(evidence); err != nil {
		return nil, err
	}
	test.expectedProjectUpdatedAt = evidence.launchWindow.projectQuota.updatedAt
	test.expectedStartAdmissionRevision = evidence.startAdmissionRevision
	blocked, err := tr.evaluateStartGatesLockedWithEvidence(id, test, opts, expectedTerm, now, evidence)
	if err != nil || blocked {
		return nil, err
	}
	previousExecutionFence := test.executionFence
	previousExecutionGeneration := test.executionGeneration
	previousPassiveOperationToken := test.passiveOperationToken
	claimed, claimErr := tr.claimPassiveExecutionFenceLocked(test, expectedTerm)
	if claimErr != nil {
		return nil, claimErr
	}
	if err := tr.installTestExecutionContextLocked(test, expectedTerm); err != nil {
		if claimed {
			test.executionFence = previousExecutionFence
			test.executionGeneration = previousExecutionGeneration
			test.passiveOperationToken = previousPassiveOperationToken
		}
		return nil, err
	}

	previousStatus := test.Status
	previousPendingSince := cloneTimePtr(test.PendingSince)
	previousPendingReason := test.PendingReason
	previousQueuePosition := test.QueuePosition
	previousError := test.Error
	previousStartAuthorization := cloneQueuedStartAuthorization(test.StartAuthorization)
	previousAdmissionReservation := test.shakeoutAdmissionReservation
	actorSnapshot, actorChanged := applyTestStartedByActor(test, opts.actor)
	// SHAKEOUT reserves runtime capacity before executing one-shot actions. The
	// exact bounded operator authorizations that admitted this attempt must be
	// durable with that reservation, so post-action verification recovery can
	// continue with empty process-local options after an outage or failover.
	applyRequestedStartAuthorizations(test, opts, now)
	test.Status = "SHAKEOUT"
	test.shakeoutAdmissionReservation = opts.shakeoutAdmissionReservation
	test.PendingSince = nil
	test.PendingReason = ""
	test.QueuePosition = 0
	test.Error = ""
	if owned, err := tr.persistManagedTestStartOutsideLockLocked(test, "SHAKEOUT"); err != nil {
		if owned {
			clearScriptShakeoutContextLocked(test)
			test.executionFence = previousExecutionFence
			test.executionGeneration = previousExecutionGeneration
			test.passiveOperationToken = previousPassiveOperationToken
			test.Status = previousStatus
			test.PendingSince = previousPendingSince
			test.PendingReason = previousPendingReason
			test.QueuePosition = previousQueuePosition
			test.Error = previousError
			test.StartAuthorization = previousStartAuthorization
			test.shakeoutAdmissionReservation = previousAdmissionReservation
			if actorChanged {
				restoreTestStartedByActor(test, actorSnapshot)
			}
		}
		return nil, fmt.Errorf("persist script shakeout state: %w", err)
	}
	releasePassiveOperationTokenLocked(test)
	tr.broadcastEvent("script_shakeout_started", cloneManagedTestLifecycleEvent(test))

	return test, nil
}

func (tr *TestRunner) installTestExecutionContextLocked(test *ManagedTest, expectedTerm *executionLeaseTerm) error {
	if test == nil {
		return fmt.Errorf("test is required")
	}
	ctx, cancel := tr.executionOperationContext(context.Background())
	if ctx.Err() != nil {
		cancel()
		return errNotExecutionLeader
	}
	if tr.durableExecutionFence {
		contextTerm, ok := executionLeaseTermFromContext(ctx)
		if !ok || (expectedTerm != nil && contextTerm != *expectedTerm) {
			cancel()
			return errNotExecutionLeader
		}
		test.executionGeneration = contextTerm.generation
	}
	test.executionCtx = ctx
	test.cancel = cancel
	return nil
}

func clearScriptShakeoutContextLocked(test *ManagedTest) {
	if test == nil {
		return
	}
	if test.cancel != nil {
		test.cancel()
	}
	test.cancel = nil
	test.executionCtx = nil
}

func (tr *TestRunner) markScriptShakeoutPassed(id string, report ScriptShakeoutReport) error {
	fingerprint, err := tr.scriptShakeoutExecutableFingerprint(id)
	if err != nil {
		return err
	}
	return tr.markScriptShakeoutPassedWithExpectedTermAndFingerprint(id, report, fingerprint, nil)
}

func (tr *TestRunner) markScriptShakeoutPassedWithExpectedTerm(id string, report ScriptShakeoutReport, expectedTerm *executionLeaseTerm) error {
	fingerprint, err := tr.scriptShakeoutExecutableFingerprint(id)
	if err != nil {
		return err
	}
	return tr.markScriptShakeoutPassedWithExpectedTermAndFingerprint(id, report, fingerprint, expectedTerm)
}

func (tr *TestRunner) markScriptShakeoutPassedWithExpectedTermAndFingerprint(id string, report ScriptShakeoutReport, fingerprint scriptShakeoutInputFingerprint, expectedTerm *executionLeaseTerm) error {
	return tr.markScriptShakeoutOutcome(id, pendingScriptShakeoutOutcome{
		Report:                cloneScriptShakeoutReport(report),
		ExecutableFingerprint: fingerprint,
		PassStatus:            "QUEUED",
	}, expectedTerm)
}

func (tr *TestRunner) markPendingScriptShakeoutPassedWithExpectedTerm(id string, report ScriptShakeoutReport, fingerprint scriptShakeoutInputFingerprint, pendingSince time.Time, expectedTerm *executionLeaseTerm) error {
	return tr.markScriptShakeoutOutcome(id, pendingScriptShakeoutOutcome{
		Report:                cloneScriptShakeoutReport(report),
		ExecutableFingerprint: fingerprint,
		PassStatus:            "PENDING",
		PendingSince:          cloneTimePtr(&pendingSince),
		PendingReason:         "awaiting capacity after script shakeout",
	}, expectedTerm)
}

// markScriptShakeoutVerificationPendingWithExpectedTerm records that the
// one-shot actions completed, but their post-action executable fingerprint
// could not be read. The private outcome is deliberately not persisted as a
// pass: retries re-read and compare the fingerprint without executing actions.
func (tr *TestRunner) markScriptShakeoutVerificationPendingWithExpectedTerm(
	id string,
	report ScriptShakeoutReport,
	fingerprint scriptShakeoutInputFingerprint,
	passStatus string,
	pendingSince *time.Time,
	pendingReason string,
	expectedTerm *executionLeaseTerm,
	verificationErr error,
	enqueueRecovery bool,
) error {
	if verificationErr == nil {
		verificationErr = errors.New("post-action executable input evidence is unavailable")
	}
	outcome := pendingScriptShakeoutOutcome{
		Report:                cloneScriptShakeoutReport(report),
		ExecutableFingerprint: fingerprint,
		PassStatus:            passStatus,
		PendingSince:          cloneTimePtr(pendingSince),
		PendingReason:         pendingReason,
		VerificationPending:   true,
	}
	var recoveryIdentity queuedStartRecoveryIdentity
	if enqueueRecovery && !strings.EqualFold(strings.TrimSpace(passStatus), "PENDING") {
		tr.mu.RLock()
		recoveryIdentity, _ = queuedStartRecoveryIdentityForTest(tr.tests[id])
		tr.mu.RUnlock()
	}
	if err := tr.markScriptShakeoutOutcome(id, outcome, expectedTerm); err != nil {
		return err
	}
	// A manual/API start has no CI handler to arrange a second attempt. Once the
	// one-shot actions have completed, schedule bounded background verification
	// so a temporary secret-provider outage cannot reserve SHAKEOUT capacity
	// indefinitely. PENDING promotion owns its own term-bound retry sweep.
	if enqueueRecovery && !strings.EqualFold(strings.TrimSpace(passStatus), "PENDING") {
		term := executionLeaseTerm{}
		if expectedTerm != nil {
			term = *expectedTerm
		} else if captured, err := tr.captureExecutionLeaseTerm(); err == nil {
			term = captured
		}
		tr.enqueueQueuedCIRecoveryRetryForIdentityWithTerm(id, recoveryIdentity, term)
	}
	return errors.Join(
		errStartAdmissionEvidenceStale,
		fmt.Errorf("verify completed script shakeout executable inputs: %w", verificationErr),
	)
}

func (tr *TestRunner) markScriptShakeoutReview(id string, report ScriptShakeoutReport) error {
	return tr.markScriptShakeoutReviewWithExpectedTerm(id, report, nil)
}

func (tr *TestRunner) markScriptShakeoutReviewWithExpectedTerm(id string, report ScriptShakeoutReport, expectedTerm *executionLeaseTerm) error {
	return tr.markScriptShakeoutOutcome(id, pendingScriptShakeoutOutcome{
		Report: cloneScriptShakeoutReport(report),
	}, expectedTerm)
}

func cloneScriptShakeoutReport(report ScriptShakeoutReport) ScriptShakeoutReport {
	cloned := report
	cloned.Results = append([]ScriptShakeoutResult(nil), report.Results...)
	return cloned
}

// markScriptShakeoutOutcome installs the completed result before attempting
// durability. If every bounded write fails, the outcome remains process-local
// and the row remains SHAKEOUT so a retry can finish the write without running
// the one-shot actions again.
func (tr *TestRunner) markScriptShakeoutOutcome(id string, outcome pendingScriptShakeoutOutcome, expectedTerm *executionLeaseTerm) error {
	if outcome.Report.Passed && (strings.TrimSpace(outcome.ExecutableFingerprint.EvidenceSHA256) == "" ||
		strings.TrimSpace(outcome.ExecutableFingerprint.ProcessSHA256) == "") {
		return errors.New("successful script shakeout is missing its executable input fingerprint")
	}
	if err := tr.validateExpectedStartTerm(expectedTerm); err != nil {
		return err
	}
	var capturedTerm executionLeaseTerm
	if tr.durableExecutionFence && expectedTerm == nil {
		var err error
		capturedTerm, err = tr.captureExecutionLeaseTerm()
		if err != nil {
			return err
		}
		expectedTerm = &capturedTerm
	}
	if expectedTerm != nil {
		outcome.ExecutionTerm = *expectedTerm
	}

	tr.mu.Lock()
	defer tr.mu.Unlock()

	test, ok := tr.tests[id]
	if !ok {
		return fmt.Errorf("test not found: %s", id)
	}
	if test.Status != "SHAKEOUT" {
		return fmt.Errorf("test is %s, shakeout result no longer applies", test.Status)
	}
	if test.passiveOperationToken != "" {
		return errStartAdmissionEvidenceStale
	}
	if expectedTerm != nil && tr.durableExecutionFence &&
		(test.executionFence != expectedTerm.ownerID || test.executionGeneration != expectedTerm.generation) {
		return errNotExecutionLeader
	}
	test.pendingScriptShakeoutOutcome = &outcome
	if outcome.VerificationPending {
		// External actions are complete. Cancel their execution context while
		// retaining the execution fence and private outcome so neither a retry nor
		// a concurrent start can execute the one-shot actions again.
		clearScriptShakeoutContextLocked(test)
		return nil
	}
	return tr.persistPendingScriptShakeoutOutcomeLocked(test)
}

func (tr *TestRunner) resumePendingScriptShakeoutOutcome(id string, expectedTerm *executionLeaseTerm) error {
	return tr.resumePendingScriptShakeoutOutcomeForIdentity(id, expectedTerm, nil)
}

func (tr *TestRunner) resumePendingScriptShakeoutOutcomeForIdentity(id string, expectedTerm *executionLeaseTerm, expectedIdentity *queuedStartRecoveryIdentity) error {
	if err := tr.validateExpectedStartTerm(expectedTerm); err != nil {
		return err
	}
	var capturedTerm executionLeaseTerm
	if tr.durableExecutionFence && expectedTerm == nil {
		var err error
		capturedTerm, err = tr.captureExecutionLeaseTerm()
		if err != nil {
			return err
		}
		expectedTerm = &capturedTerm
	}

	tr.mu.Lock()
	test, ok := tr.tests[id]
	if !ok {
		tr.mu.Unlock()
		return nil
	}
	if expectedIdentity != nil && !expectedIdentity.matches(test) {
		tr.mu.Unlock()
		return storage.ErrTestStartIdentityChanged
	}
	if test.pendingScriptShakeoutOutcome == nil {
		tr.mu.Unlock()
		return nil
	}
	if test.passiveOperationToken != "" {
		tr.mu.Unlock()
		return errStartAdmissionEvidenceStale
	}
	if test.Status != "SHAKEOUT" {
		tr.mu.Unlock()
		return fmt.Errorf("test is %s, pending shakeout outcome no longer applies", test.Status)
	}
	if expectedTerm != nil && tr.durableExecutionFence &&
		(test.executionFence != expectedTerm.ownerID || test.executionGeneration != expectedTerm.generation) {
		tr.mu.Unlock()
		return errNotExecutionLeader
	}
	outcome := test.pendingScriptShakeoutOutcome
	if !outcome.VerificationPending {
		defer tr.mu.Unlock()
		return tr.persistPendingScriptShakeoutOutcomeLocked(test)
	}
	// Keep the shared pointer only as an identity token. Everything used while
	// the mutex is released must be copied so concurrent recovery attempts cannot
	// race with the winner that finalizes and clears the private outcome.
	expectedFingerprint := outcome.ExecutableFingerprint
	reportSnapshot := cloneScriptShakeoutReport(outcome.Report)
	tr.mu.Unlock()

	// Secret providers can involve network I/O. Compare outside tr.mu while the
	// exact outcome pointer and execution fence retain ownership of the result.
	currentFingerprint, fingerprintErr := tr.scriptShakeoutExecutableFingerprint(id)
	if err := tr.validateExpectedStartTerm(expectedTerm); err != nil {
		return err
	}
	if fingerprintErr != nil && retryableStartSecretRequirement(fingerprintErr) {
		return errors.Join(
			errStartAdmissionEvidenceStale,
			fmt.Errorf("verify completed script shakeout executable inputs: %w", fingerprintErr),
		)
	}

	report := reportSnapshot
	if fingerprintErr != nil {
		report.Passed = false
		report.Summary = "Script shakeout completed, but its executable input evidence could not be verified; full load test has not started."
		report.Results = append(report.Results, ScriptShakeoutResult{GroupName: "executable inputs", Status: "failed", Error: fingerprintErr.Error()})
	} else if currentFingerprint != expectedFingerprint {
		report.Passed = false
		report.Summary = "Script shakeout inputs changed while the check was running; full load test has not started."
		report.Results = append(report.Results, ScriptShakeoutResult{GroupName: "executable inputs", Status: "failed", Error: "the executable snapshot or a versioned secret source changed during shakeout"})
	}

	tr.mu.Lock()
	defer tr.mu.Unlock()
	test = tr.tests[id]
	if test == nil {
		return nil
	}
	if test.pendingScriptShakeoutOutcome != outcome {
		// A concurrent retry may already have finalized this exact result.
		if test.pendingScriptShakeoutOutcome == nil && test.Status != "SHAKEOUT" {
			return nil
		}
		return errStartAdmissionEvidenceStale
	}
	if test.Status != "SHAKEOUT" {
		return fmt.Errorf("test is %s, pending shakeout outcome no longer applies", test.Status)
	}
	if expectedTerm != nil && tr.durableExecutionFence &&
		(test.executionFence != expectedTerm.ownerID || test.executionGeneration != expectedTerm.generation) {
		return errNotExecutionLeader
	}
	outcome.Report = report
	outcome.VerificationPending = false
	if report.Passed {
		outcome.ExecutableFingerprint = currentFingerprint
	}
	return tr.persistPendingScriptShakeoutOutcomeLocked(test)
}

// persistPendingScriptShakeoutOutcomeLocked makes the completed outcome
// durable and publishes it exactly once. The caller must hold tr.mu.
func (tr *TestRunner) persistPendingScriptShakeoutOutcomeLocked(test *ManagedTest) error {
	if test == nil || test.pendingScriptShakeoutOutcome == nil {
		return nil
	}
	outcome := test.pendingScriptShakeoutOutcome
	if outcome.VerificationPending {
		return errors.Join(errStartAdmissionEvidenceStale, errors.New("script shakeout executable input verification is still pending"))
	}
	if test.passiveOperationToken != "" {
		return errStartAdmissionEvidenceStale
	}
	test.passiveOperationToken = uuid.NewString()
	operationToken := test.passiveOperationToken
	executionFence := test.executionFence
	executionGeneration := test.executionGeneration
	operationIdentity, _ := queuedStartRecoveryIdentityForTest(test)
	previousStatus := test.Status
	previousPendingSince := cloneTimePtr(test.PendingSince)
	previousPendingReason := test.PendingReason
	previousQueuePosition := test.QueuePosition
	previousError := test.Error
	previousStartAuthorization := cloneQueuedStartAuthorization(test.StartAuthorization)
	previousValidatedPass := test.validatedScriptShakeoutPass
	previousValidatedTerm := test.validatedScriptShakeoutTerm

	if outcome.Report.Passed {
		test.Status = outcome.PassStatus
		if test.Status == "" {
			test.Status = "QUEUED"
		}
		test.PendingSince = cloneTimePtr(outcome.PendingSince)
		test.PendingReason = outcome.PendingReason
		test.QueuePosition = 0
		test.Error = ""
		applyScriptShakeoutPass(test, outcome.ExecutableFingerprint, outcome.Report, outcome.ExecutionTerm)
	} else {
		test.Status = "SHAKEOUT_REVIEW"
		test.PendingSince = nil
		test.PendingReason = ""
		test.QueuePosition = 0
		test.Error = formatScriptShakeoutEvidence(outcome.Report)
	}
	durableRecord := buildManagedTestRecord(test)
	durableTransition := storage.TestShakeoutTransition{
		ID:                 durableRecord.ID,
		RefKey:             durableRecord.RefKey,
		ProjectID:          durableRecord.ProjectID,
		StartTime:          durableRecord.StartTime,
		Status:             durableRecord.Status,
		PendingSince:       cloneTimePtr(durableRecord.PendingSince),
		Error:              durableRecord.Error,
		StartAuthorization: durableRecord.StartAuthorization,
	}
	if tr.durableExecutionFence {
		durableTransition.ExecutionLeaseName = executionLeaseName
		durableTransition.ExecutionLeaseOwnerID = executionFence
		durableTransition.ExecutionLeaseGeneration = executionGeneration
	}
	desiredStatus := test.Status
	desiredPendingSince := cloneTimePtr(test.PendingSince)
	desiredPendingReason := test.PendingReason
	desiredQueuePosition := test.QueuePosition
	desiredError := test.Error
	desiredStartAuthorization := cloneQueuedStartAuthorization(test.StartAuthorization)
	desiredValidatedPass := test.validatedScriptShakeoutPass
	desiredValidatedTerm := test.validatedScriptShakeoutTerm

	// Keep the uncommitted outcome private while storage waits. SHAKEOUT remains
	// capacity-reserving, and the operation token excludes another lifecycle
	// mutation without freezing unrelated API reads behind tr.mu.
	test.Status = previousStatus
	test.PendingSince = previousPendingSince
	test.PendingReason = previousPendingReason
	test.QueuePosition = previousQueuePosition
	test.Error = previousError
	test.StartAuthorization = previousStartAuthorization
	test.validatedScriptShakeoutPass = previousValidatedPass
	test.validatedScriptShakeoutTerm = previousValidatedTerm

	tr.mu.Unlock()
	persistErr := retryDurableTransition(func() error {
		return tr.persistManagedTestShakeoutTransition(durableTransition)
	})
	tr.mu.Lock()
	if tr.tests[test.ID] != test || test.pendingScriptShakeoutOutcome != outcome ||
		test.passiveOperationToken != operationToken || test.executionFence != executionFence ||
		test.executionGeneration != executionGeneration || test.Status != previousStatus {
		// A committed transition can be adopted by the successor while this
		// callback is still awaiting its response. Its acquire scan may have seen
		// the operation token and intentionally skipped recovery, so explicitly
		// retire any token/context still owned here and re-arm a durable rescan.
		if tr.tests[test.ID] == test && test.passiveOperationToken == operationToken {
			test.passiveOperationToken = ""
			clearScriptShakeoutContextLocked(test)
			if strings.EqualFold(strings.TrimSpace(test.Status), "QUEUED") ||
				strings.EqualFold(strings.TrimSpace(test.Status), "SHAKEOUT_REVIEW") {
				// reloadStoredTests adopted the durable result while preserving
				// process-private runtime fields. The retained pointer is now stale
				// evidence, not unfinished work.
				test.pendingScriptShakeoutOutcome = nil
			}
			if tr.durableExecutionFence && !tr.executionLeaseTermLocallyCurrent(executionLeaseTerm{
				ownerID: executionFence, generation: executionGeneration,
			}) {
				test.executionFence = ""
				test.executionGeneration = 0
			}
		}
		tr.schedulePassiveTransitionRescanAfterTermChangeLocked(test.ID, operationIdentity, executionFence, executionGeneration)
		return errors.Join(errStartAdmissionEvidenceStale, persistErr)
	}

	if persistErr != nil {
		// The actions have finished, so cancel their context even though the
		// outcome write remains retryable. Retain the execution fence and the
		// private outcome to exclude another operation and prevent a replay.
		test.passiveOperationToken = ""
		clearScriptShakeoutContextLocked(test)
		if tr.durableExecutionFence && !tr.executionLeaseTermLocallyCurrent(executionLeaseTerm{
			ownerID: executionFence, generation: executionGeneration,
		}) {
			test.executionFence = ""
			test.executionGeneration = 0
		}
		tr.schedulePassiveTransitionRescanAfterTermChangeLocked(test.ID, operationIdentity, executionFence, executionGeneration)
		return errors.Join(errStartAdmissionEvidenceStale, fmt.Errorf("persist completed script shakeout outcome: %w", persistErr))
	}

	report := cloneScriptShakeoutReport(outcome.Report)
	test.Status = desiredStatus
	test.PendingSince = desiredPendingSince
	test.PendingReason = desiredPendingReason
	test.QueuePosition = desiredQueuePosition
	test.Error = desiredError
	test.StartAuthorization = desiredStartAuthorization
	test.validatedScriptShakeoutPass = desiredValidatedPass
	test.validatedScriptShakeoutTerm = desiredValidatedTerm
	test.pendingScriptShakeoutOutcome = nil
	clearScriptShakeoutContextLocked(test)
	releasePassiveExecutionFenceLocked(test)
	tr.schedulePassiveTransitionRescanAfterTermChangeLocked(test.ID, operationIdentity, executionFence, executionGeneration)
	if report.Passed {
		tr.broadcastEvent("script_shakeout_passed", cloneManagedTestLifecycleEvent(test))
		return nil
	}
	tr.broadcastEvent("script_shakeout_failed", cloneManagedTestLifecycleEvent(test))
	return &ScriptShakeoutGateError{TestID: test.ID, Report: &report}
}

func (tr *TestRunner) prepareTestStartLocked(id string, opts StartTestOptions) (*ManagedTest, error) {
	return tr.prepareTestStartLockedWithExpectedTerm(id, opts, nil)
}

func (tr *TestRunner) prepareTestStartLockedWithExpectedTerm(id string, opts StartTestOptions, expectedTerm *executionLeaseTerm) (*ManagedTest, error) {
	now := time.Now().UTC()
	test := tr.tests[id]
	if test == nil {
		return nil, fmt.Errorf("test not found: %s", id)
	}
	window := launchWindowForTestAt(test, now, now)
	return tr.prepareTestStartLockedWithExpectedTermAndEvidence(id, opts, expectedTerm, now, startLaunchEvidence{
		launchWindow: tr.loadLaunchWindowEvidenceForProject(window, test.ProjectID, testAssignedPoolID(test)),
	})
}

func (tr *TestRunner) prepareTestStartLockedWithExpectedTermAndEvidence(id string, opts StartTestOptions, expectedTerm *executionLeaseTerm, now time.Time, evidence startLaunchEvidence) (*ManagedTest, error) {
	if err := tr.shutdownErrorLocked(); err != nil {
		return nil, err
	}
	test, ok := tr.tests[id]
	if !ok {
		return nil, fmt.Errorf("test not found: %s", id)
	}
	if err := validateExpectedStartRecoveryIdentityLocked(test, opts); err != nil {
		return nil, err
	}
	if !testexec.IsStartableTestStatus(test.Status) {
		return nil, testexec.StartableTestStatusError(test.Status)
	}
	archiveScripts := []string{}
	for _, group := range test.Groups {
		archiveScripts = append(archiveScripts, group.ScriptID)
	}
	archiveScripts = append(archiveScripts, storage.ExecutionSourceScriptIDs(test.MaterializationKey)...)
	if err := tr.checkArchivedExecution(test.ScenarioID, archiveScripts); err != nil {
		return nil, err
	}
	if err := tr.projectDeletionErrorLocked(test.ProjectID); err != nil {
		return nil, err
	}
	test.expectedProjectUpdatedAt = evidence.launchWindow.projectQuota.updatedAt
	test.expectedStartAdmissionRevision = evidence.startAdmissionRevision

	if blocked, err := tr.evaluateStartGatesLockedWithEvidence(id, test, opts, expectedTerm, now, evidence); err != nil {
		return nil, err
	} else if blocked {
		return nil, nil
	}

	previousStatus := test.Status
	previousPendingSince := cloneTimePtr(test.PendingSince)
	previousPendingReason := test.PendingReason
	previousQueuePosition := test.QueuePosition
	previousError := test.Error
	previousExecutionCtx := test.executionCtx
	previousCancel := test.cancel
	previousBackend := test.executionBackend
	previousExecutionFence := test.executionFence
	previousExecutionGeneration := test.executionGeneration
	previousPassiveOperationToken := test.passiveOperationToken
	previousExpectedStartAdmissionRevision := test.expectedStartAdmissionRevision
	previousStartAuthorization := cloneQueuedStartAuthorization(test.StartAuthorization)
	actorSnapshot, actorChanged := applyTestStartedByActor(test, opts.actor)
	applyRequestedStartAuthorizations(test, opts, time.Now().UTC())
	if err := tr.markTestInitializingLockedWithExpectedTerm(test, expectedTerm); err != nil {
		test.StartAuthorization = previousStartAuthorization
		if actorChanged {
			restoreTestStartedByActor(test, actorSnapshot)
		}
		return nil, err
	}
	if opts.traceContext != nil {
		// The lifecycle is a child of the start span but must not inherit HTTP
		// request cancellation or request-local values. Keep the previous context
		// live until persistence commits so a failed transition can restore it.
		initializingCancel := test.cancel
		test.executionCtx, test.cancel = tr.executionOperationContext(telemetry.Detach(opts.traceContext))
		if initializingCancel != nil {
			// markTestInitializingLocked created this temporary context after the
			// previous cancel function was snapshotted; it is safe to retire now.
			initializingCancel()
		}
	}
	if owned, err := tr.persistManagedTestStartOutsideLockLocked(test, "INITIALIZING"); err != nil {
		if owned {
			if test.cancel != nil {
				test.cancel()
			}
			test.Status = previousStatus
			test.PendingSince = previousPendingSince
			test.PendingReason = previousPendingReason
			test.QueuePosition = previousQueuePosition
			test.Error = previousError
			test.executionCtx = previousExecutionCtx
			test.cancel = previousCancel
			test.executionBackend = previousBackend
			test.executionFence = previousExecutionFence
			test.executionGeneration = previousExecutionGeneration
			test.passiveOperationToken = previousPassiveOperationToken
			test.expectedStartAdmissionRevision = previousExpectedStartAdmissionRevision
			test.StartAuthorization = previousStartAuthorization
			if actorChanged {
				restoreTestStartedByActor(test, actorSnapshot)
			}
		}
		return nil, fmt.Errorf("persist initializing test state: %w", err)
	}
	if previousCancel != nil {
		previousCancel()
	}
	releasePassiveOperationTokenLocked(test)
	tr.recordTestStartActivity(test, opts.actor)
	return test, nil
}

func (tr *TestRunner) ReconcilePendingTests() {
	// Only the execution leader reconciles. On a non-leader replica (HA) this is
	// a no-op; the eventual leader runs it via reloadAndReconcile on acquisition.
	if !tr.isExecutionLeader() {
		return
	}
	// Rebuild sequential plan mappings before pending promotion. Otherwise a
	// recovered plan-owned test could run to completion without its terminal hook
	// knowing which durable sequence to advance.
	leaseTerm, termErr := tr.captureExecutionLeaseTerm()
	if termErr != nil {
		slog.Error("failed to capture execution leadership term for plan recovery", "error", termErr)
		return
	}
	if err := tr.recoverSequentialExecutionPlanRunsWithTerm(leaseTerm); err != nil {
		slog.Error("failed to recover sequential execution-plan runs", "error", err)
		tr.enqueueSequentialExecutionPlanRecoveryRetryWithTerm(leaseTerm)
		return
	}
	tr.promotePendingTests()
	tr.refreshPendingQueueMetadata()
}

func (tr *TestRunner) reconcilePendingTestsDuringAcquire() error {
	if !tr.holdsExecutionLeadership() {
		return errNotExecutionLeader
	}
	if err := tr.recoverSequentialExecutionPlanRuns(); err != nil {
		return err
	}
	// Promotion starts execution and therefore waits for admission to open. The
	// onAdmit callback calls ReconcilePendingTests after reconciling is cleared.
	tr.refreshPendingQueueMetadata()
	// During the leadership callback admission remains closed and onAdmit will
	// perform promotion after recovery completes. Direct callers that already
	// hold an admitted lease (including recovery retries) can promote now.
	if tr.isExecutionLeader() {
		tr.ReconcilePendingTests()
	}
	return nil
}

// reloadAndReconcile refreshes in-memory test state from the database and then
// reconciles. It runs when this replica acquires execution leadership — at boot
// when it wins the lease, or after a failover takeover where the previous
// leader's runs and any tests created elsewhere are not yet in memory.
func (tr *TestRunner) reloadAndReconcile() error {
	if !tr.holdsExecutionLeadership() {
		return errNotExecutionLeader
	}
	if err := tr.repairCapacityReservationBindings(); err != nil {
		return fmt.Errorf("repair capacity timeslot bindings: %w", err)
	}
	if err := tr.reconcileExpiredRuntimeOverrideSecretAttempts(time.Now().UTC()); err != nil {
		return fmt.Errorf("recover runtime credential provisioning: %w", err)
	}
	tr.scheduleRuntimeOverrideSecretAttemptReconciliation()
	if err := tr.reloadStoredScripts(); err != nil {
		return fmt.Errorf("reload scripts: %w", err)
	}
	if err := tr.reloadStoredTests(); err != nil {
		return fmt.Errorf("reload tests: %w", err)
	}
	if err := tr.adoptOrphanedRuns(); err != nil {
		return fmt.Errorf("adopt orphaned runs: %w", err)
	}
	if err := tr.cleanupLeakedCIScriptSnapshots(); err != nil {
		return fmt.Errorf("clean up leaked CI script snapshots: %w", err)
	}
	if err := tr.recoverQueuedCIRuns(); err != nil {
		return fmt.Errorf("recover queued CI runs: %w", err)
	}
	if err := tr.reloadStoredScenarios(); err != nil {
		return fmt.Errorf("reload scenarios: %w", err)
	}
	return tr.reconcilePendingTestsDuringAcquire()
}

// resolveBackend returns the test's execution backend, falling back to the
// runner's shared execution backend when the test has none.
func (tr *TestRunner) resolveBackend(test *ManagedTest) testExecutionBackend {
	if test.executionBackend != nil {
		return test.executionBackend
	}
	return tr.execution
}

// lockedTestStatus resolves a possibly-ref-key id and returns the resolved id, the
// managed test, and its status captured under the read lock. It errors when the
// test is unknown. Callers must re-acquire the write lock before mutating.
func (tr *TestRunner) lockedTestStatus(id string) (string, *ManagedTest, string, error) {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	id = tr.resolveRefKeyLocked(id)
	test, ok := tr.tests[id]
	if !ok {
		return id, nil, "", fmt.Errorf("test not found: %s", id)
	}
	return id, test, test.Status, nil
}

func (tr *TestRunner) StopTest(id string) error {
	return tr.stopTestWithContextAndCIReasonAndActorMode(context.Background(), id, true, "", testCreationActor{}, false)
}

func (tr *TestRunner) StopTestAsActor(id string, actor testCreationActor) error {
	return tr.stopTestWithContextAndCIReasonAndActorMode(context.Background(), id, true, "", actor, false)
}

func (tr *TestRunner) stopTest(id string, requireLeadership bool) error {
	return tr.stopTestWithContextAndCIReason(context.Background(), id, requireLeadership, "")
}

func (tr *TestRunner) stopTestWithContext(ctx context.Context, id string, requireLeadership bool) error {
	return tr.stopTestWithContextAndCIReason(ctx, id, requireLeadership, "")
}

func (tr *TestRunner) AbortCIRun(id string, reason string) error {
	return tr.stopTestWithContextAndCIReasonAndActorMode(context.Background(), id, true, normalizeCIAbortReason(reason), testCreationActor{}, true)
}

func (tr *TestRunner) AbortCIRunAsActor(id string, reason string, actor testCreationActor) error {
	return tr.stopTestWithContextAndCIReasonAndActorMode(context.Background(), id, true, normalizeCIAbortReason(reason), actor, true)
}

func (tr *TestRunner) stopTestWithContextAndCIReason(ctx context.Context, id string, requireLeadership bool, abortReason string) error {
	return tr.stopTestWithContextAndCIReasonAndActor(ctx, id, requireLeadership, abortReason, testCreationActor{})
}

func (tr *TestRunner) stopTestWithContextAndCIReasonAndActor(ctx context.Context, id string, requireLeadership bool, abortReason string, actor testCreationActor) error {
	return tr.stopTestWithContextAndCIReasonAndActorMode(ctx, id, requireLeadership, abortReason, actor, false)
}

func (tr *TestRunner) stopTestWithContextAndCIReasonAndActorMode(ctx context.Context, id string, requireLeadership bool, abortReason string, actor testCreationActor, forceAbort bool) error {
	return tr.stopTestWithContextAndCIReasonAndActorModeAttempt(ctx, id, requireLeadership, abortReason, actor, forceAbort, false)
}

func (tr *TestRunner) stopTestWithContextAndCIReasonAndActorModeAttempt(ctx context.Context, id string, requireLeadership bool, abortReason string, actor testCreationActor, forceAbort bool, retryAttempt bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if requireLeadership && !tr.isExecutionLeader() {
		return errNotExecutionLeader
	}
	tr.mu.RLock()
	id = tr.resolveRefKeyLocked(id)
	tr.mu.RUnlock()
	unlockControl, err := tr.lockTestLifecycleControl(ctx, id)
	if err != nil {
		return err
	}
	defer unlockControl()
	if err := ctx.Err(); err != nil {
		return err
	}
	if requireLeadership && !tr.isExecutionLeader() {
		return errNotExecutionLeader
	}
	// If an earlier ambiguous Stop already owns the one retry worker, record this
	// request before dispatch. Force is monotonic, and the version prevents an
	// in-flight graceful success from retiring without observing the upgrade.
	if !retryAttempt {
		forceAbort = tr.upgradeExistingStopTeardownRetryIntent(id, forceAbort)
	}

	tr.mu.Lock()
	test, ok := tr.tests[id]
	if !ok {
		tr.mu.Unlock()
		return fmt.Errorf("test not found: %s", id)
	}
	if test.passiveOperationToken != "" {
		// A pending/start/shakeout transition is persisting this passive row with
		// tr.mu released. Its storage operation owns an identity/status CAS; a
		// concurrent Stop must not mutate the local projection or race a second
		// durable lifecycle write. The caller may retry after the short operation
		// finishes.
		tr.mu.Unlock()
		return errors.Join(errStartAdmissionEvidenceStale, errors.New("test lifecycle transition is already in progress"))
	}
	if test.remoteFinalizing {
		// The distributed completion path owns remote teardown once finalization
		// starts. Treat a concurrent stop as already in progress so it cannot
		// overlap the finalizer's StopTest fan-out.
		tr.mu.Unlock()
		return nil
	}
	status := test.Status
	control := testexec.ControlStop
	controlResult := "stopped"
	if forceAbort {
		control = testexec.ControlAbort
		controlResult = "aborted"
	}
	if !testexec.AllowsLifecycleControl(status, control) {
		tr.mu.Unlock()
		return fmt.Errorf("test is %s and cannot be %s", status, controlResult)
	}
	if requireLeadership {
		if err := tr.claimTestExecutionFence(test); err != nil {
			tr.mu.Unlock()
			return err
		}
	}
	switch status {
	case "COMPLETE", "FAILED", "ABORTED":
		tr.mu.Unlock()
		return nil
	}
	previousError := test.Error
	actorSnapshot, actorChanged := applyTestStoppedByActor(test, actor)
	if abortReason != "" {
		test.Error = "ci abort reason: " + abortReason
	}
	switch status {
	case "STOPPING":
		// A prior backend stop may have been known-not-applied while the
		// controller's attempt to restore RUNNING/PAUSED failed durably. Keep the
		// fail-closed STOPPING reservation, but retry idempotent backend teardown
		// instead of telling operators that no work remains.
		tr.cancelAllVUserScaleRampsLocked(test)
		if test.Error != previousError || actorChanged {
			if err := tr.saveManagedTestRecord(test); err != nil {
				test.Error = previousError
				if actorChanged {
					restoreTestStoppedByActor(test, actorSnapshot)
				}
				tr.mu.Unlock()
				return fmt.Errorf("persist CI abort reason: %w", err)
			}
		}
	case "QUEUED", "PENDING", "RESERVED", "SCHEDULED", "SHAKEOUT", "SHAKEOUT_REVIEW", "INITIALIZING":
		now := time.Now().UTC()
		previousPendingSince := cloneTimePtr(test.PendingSince)
		previousPendingReason := test.PendingReason
		previousQueuePosition := test.QueuePosition
		previousEndedAt := cloneTimePtr(test.EndedAt)
		previousScheduledStartAt := cloneTimePtr(test.ScheduledStartAt)
		test.Status = "ABORTED"
		test.PendingSince = nil
		test.PendingReason = ""
		test.QueuePosition = 0
		test.EndedAt = &now
		test.ScheduledStartAt = nil
		if test.Error == "" {
			test.Error = "test cancelled before start"
		}
		if err := tr.saveManagedTestRecord(test); err != nil {
			test.Status = status
			test.PendingSince = previousPendingSince
			test.PendingReason = previousPendingReason
			test.QueuePosition = previousQueuePosition
			test.EndedAt = previousEndedAt
			test.ScheduledStartAt = previousScheduledStartAt
			test.Error = previousError
			if actorChanged {
				restoreTestStoppedByActor(test, actorSnapshot)
			}
			tr.mu.Unlock()
			return fmt.Errorf("persist aborted test state: %w", err)
		}
		tr.cancelAllVUserScaleRampsLocked(test)
		if test.cancel != nil {
			test.cancel()
		}
		test.cancel = nil
		test.executionCtx = nil
		slotID := ""
		if test.TimeslotID != nil {
			slotID = strings.TrimSpace(*test.TimeslotID)
		}
		eventSnapshot := cloneManagedTestFieldsLocked(test)
		compactTerminalManagedTestLocked(test)
		tr.mu.Unlock()
		if err := tr.syncScenarioScheduleLifecycleForTest(test.ID); err != nil {
			slog.Warn("aborted test transition could not refresh scenario occurrence cache", "test_id", test.ID, "error", err)
		}
		releaseErr := tr.releaseCapacityReservationForTerminalStatus(test.ID, "ABORTED")
		if releaseErr != nil && slotID != "" {
			tr.enqueueCapacityReservationReleaseRetry(test.ID, slotID)
		}
		tr.broadcastEvent("test_aborted", eventSnapshot)
		if advanceErr := tr.advanceExecutionPlanForTest(test, "ABORTED"); advanceErr != nil {
			return errors.Join(releaseErr, fmt.Errorf("persist aborted execution-plan transition: %w", advanceErr))
		}
		return releaseErr
	case "RUNNING", "PAUSED":
		// Reserve the transition before any blocking backend call. Concurrent
		// pause/resume/start requests now observe STOPPING and cannot overwrite it.
		test.Status = "STOPPING"
		if err := tr.saveManagedTestRecord(test); err != nil {
			test.Status = status
			test.Error = previousError
			if actorChanged {
				restoreTestStoppedByActor(test, actorSnapshot)
			}
			tr.mu.Unlock()
			return fmt.Errorf("persist stopping test state: %w", err)
		}
		tr.cancelAllVUserScaleRampsLocked(test)
	default:
		// The shared status policy above makes this unreachable. Retain a guarded
		// default so a future policy expansion cannot mutate actor metadata without
		// a corresponding transition implementation.
		if actorChanged {
			restoreTestStoppedByActor(test, actorSnapshot)
		}
		tr.mu.Unlock()
		return fmt.Errorf("test is %s and cannot be %s", status, controlResult)
	}
	backend := tr.resolveBackend(test)
	multiEngine := test.multiEngine
	cancel := test.cancel
	tr.mu.Unlock()

	// Force abort is irreversible once the durable STOPPING fence is committed:
	// cancel local work before invoking even a legacy graceful-only backend so a
	// backend waiting for executionCtx can unblock. From this point onward any
	// teardown error remains fail-closed STOPPING and is retried; rolling back to
	// RUNNING/PAUSED would advertise work that has already been hard-cancelled.
	if forceAbort {
		if multiEngine != nil {
			multiEngine.Stop()
		}
		if cancel != nil {
			cancel()
		}
	}

	if multiEngine == nil {
		var stopErr error
		stopSupported := false
		if forceAbort {
			if abortable, ok := backend.(contextAbortableExecutionBackend); ok {
				stopSupported = true
				stopErr = abortable.AbortTestContext(ctx, tr, test)
			} else if abortable, ok := backend.(abortableExecutionBackend); ok {
				stopSupported = true
				if ctx.Done() == nil {
					stopErr = abortable.AbortTest(tr, test)
				} else if cancel == nil {
					stopErr = fmt.Errorf("test backend does not support context-bounded abort")
				}
			}
		}
		if !stopSupported {
			if stoppable, ok := backend.(contextStoppableExecutionBackend); ok {
				stopSupported = true
				stopErr = stoppable.StopTestContext(ctx, tr, test)
			} else if stoppable, ok := backend.(stoppableExecutionBackend); ok {
				stopSupported = true
				if ctx.Done() == nil {
					stopErr = stoppable.StopTest(tr, test)
				} else if cancel == nil {
					// A legacy stop has no cancellation contract. Bounded shutdown must
					// not launch a call that can outlive the controller's deadline.
					stopErr = fmt.Errorf("test backend does not support context-bounded stop")
				}
			} else if cancel == nil {
				stopErr = fmt.Errorf("test backend does not support stop")
			}
		}
		if stopErr != nil {
			if forceAbort || ctx.Err() != nil || distributedStopMayHaveApplied(stopErr) {
				if cancel != nil {
					cancel()
				}
				if ctx.Err() == nil && !retryAttempt {
					tr.enqueueStopTeardownRetry(test.ID, forceAbort)
				}
				tr.mu.RLock()
				stoppingSnapshot := *test
				tr.mu.RUnlock()
				tr.broadcastEvent("test_stopping", &stoppingSnapshot)
				if ctx.Err() != nil {
					return errors.Join(stopErr, ctx.Err())
				}
				return stopErr
			}
			if stopSupported {
				tr.mu.Lock()
				if test.Status == "STOPPING" {
					test.Status = status
					test.Error = previousError
					if actorChanged {
						restoreTestStoppedByActor(test, actorSnapshot)
					}
					if persistErr := tr.saveManagedTestRecord(test); persistErr != nil {
						test.Status = "STOPPING"
						if actorChanged {
							test.StoppedBySubject = strings.TrimSpace(actor.Subject)
							test.StoppedByDisplayName = strings.TrimSpace(actor.DisplayName)
						}
						if abortReason != "" {
							test.Error = "ci abort reason: " + abortReason
						}
						tr.mu.Unlock()
						if !retryAttempt {
							tr.enqueueStopTeardownRetry(test.ID, forceAbort)
						}
						return errors.Join(stopErr, fmt.Errorf("persist stop rollback: %w", persistErr))
					}
				}
				tr.mu.Unlock()
				return stopErr
			}
			tr.mu.Lock()
			if test.Status == "STOPPING" {
				test.Status = status
				test.Error = previousError
				if actorChanged {
					restoreTestStoppedByActor(test, actorSnapshot)
				}
				if persistErr := tr.saveManagedTestRecord(test); persistErr != nil {
					test.Status = "STOPPING"
					if actorChanged {
						test.StoppedBySubject = strings.TrimSpace(actor.Subject)
						test.StoppedByDisplayName = strings.TrimSpace(actor.DisplayName)
					}
					if abortReason != "" {
						test.Error = "ci abort reason: " + abortReason
					}
					tr.mu.Unlock()
					if !retryAttempt {
						tr.enqueueStopTeardownRetry(test.ID, forceAbort)
					}
					return errors.Join(stopErr, fmt.Errorf("persist stop rollback: %w", persistErr))
				}
			}
			tr.mu.Unlock()
			return stopErr
		}
	}
	if multiEngine != nil {
		if !forceAbort {
			// GracefulStop cancels only the engine's arrival/iteration loops, allowing
			// in-flight actions and teardown to finish. Canceling executionCtx here
			// would immediately hard-stop the same engine and erase that distinction.
			multiEngine.GracefulStop()
		}
	} else if !forceAbort && cancel != nil {
		cancel()
	}
	tr.mu.RLock()
	var stoppingSnapshot *ManagedTest
	if test.Status == "STOPPING" {
		snapshot := *test
		stoppingSnapshot = &snapshot
	}
	tr.mu.RUnlock()
	if stoppingSnapshot != nil {
		tr.broadcastEvent("test_stopping", stoppingSnapshot)
	}
	return nil
}

const stopTeardownRetryTimeout = 5 * time.Second

type stopTeardownRetryIntent struct {
	force   bool
	version uint64
}

func (tr *TestRunner) enqueueStopTeardownRetry(testID string, forceAbort bool) {
	if tr == nil || strings.TrimSpace(testID) == "" {
		return
	}
	retryKey := "stop-teardown:" + testID
	tr.terminalRetryMu.Lock()
	if tr.stopTeardownRetries == nil {
		tr.stopTeardownRetries = make(map[string]stopTeardownRetryIntent)
	}
	intent := tr.stopTeardownRetries[testID]
	intent.force = intent.force || forceAbort
	intent.version++
	tr.stopTeardownRetries[testID] = intent
	if tr.terminalRetrying == nil {
		tr.terminalRetrying = make(map[string]struct{})
	}
	if _, running := tr.terminalRetrying[retryKey]; running {
		tr.terminalRetryMu.Unlock()
		return
	}
	tr.terminalRetrying[retryKey] = struct{}{}
	tr.terminalRetryMu.Unlock()

	go tr.runStopTeardownRetryWorker(testID, retryKey)
}

// upgradeExistingStopTeardownRetryIntent records a later request only when an
// ambiguous teardown is already pending. It deliberately increments the
// version even for the same mode: a worker whose call is in flight must recheck
// the latest request before atomically retiring.
func (tr *TestRunner) upgradeExistingStopTeardownRetryIntent(testID string, forceAbort bool) bool {
	if tr == nil || strings.TrimSpace(testID) == "" {
		return forceAbort
	}
	tr.terminalRetryMu.Lock()
	defer tr.terminalRetryMu.Unlock()
	intent, exists := tr.stopTeardownRetries[testID]
	if !exists {
		return forceAbort
	}
	intent.force = intent.force || forceAbort
	intent.version++
	tr.stopTeardownRetries[testID] = intent
	return intent.force
}

func (tr *TestRunner) runStopTeardownRetryWorker(testID, retryKey string) {
	delay := executionPlanRetryInitialDelay
	for tr.executionPlanRetryAllowed() {
		if !tr.waitExecutionPlanRetry(delay) {
			break
		}

		tr.terminalRetryMu.Lock()
		intent, exists := tr.stopTeardownRetries[testID]
		if !exists {
			delete(tr.terminalRetrying, retryKey)
			tr.terminalRetryMu.Unlock()
			return
		}
		tr.terminalRetryMu.Unlock()

		ctx, cancel := context.WithTimeout(context.Background(), stopTeardownRetryTimeout)
		err := tr.stopTestWithContextAndCIReasonAndActorModeAttempt(ctx, testID, true, "", testCreationActor{}, intent.force, true)
		cancel()
		tr.terminalRetryMu.Lock()
		latest, stillPending := tr.stopTeardownRetries[testID]
		if stillPending && latest.version != intent.version {
			tr.terminalRetryMu.Unlock()
			// A request arrived while this call was in flight. Its stronger mode
			// must run even when the stale attempt succeeded or failed with an error
			// that would ordinarily stop background retries.
			delay = executionPlanRetryInitialDelay
			continue
		}
		if err == nil {
			if stillPending {
				delete(tr.stopTeardownRetries, testID)
			}
			delete(tr.terminalRetrying, retryKey)
			tr.terminalRetryMu.Unlock()
			return
		}
		tr.terminalRetryMu.Unlock()

		slog.Warn("terminal transition retry failed", "test_id", testID, "transition", stopTeardownTransition(intent.force), "error", err)
		if stopTeardownRequiresExternalFence(err) {
			if noteErr := tr.recordStopTeardownExternalFenceRequired(testID); noteErr == nil {
				if tr.retireStopTeardownRetryIfUnchanged(testID, retryKey, intent.version) {
					return
				}
				delay = executionPlanRetryInitialDelay
				continue
			} else {
				slog.Warn("could not persist unconfirmed stop state", "test_id", testID, "error", noteErr)
			}
		}
		if !durableBackgroundRetryable(err) {
			if tr.retireStopTeardownRetryIfUnchanged(testID, retryKey, intent.version) {
				return
			}
			delay = executionPlanRetryInitialDelay
			continue
		}
		delay = min(delay*2, executionPlanRetryMaxDelay)
	}

	tr.terminalRetryMu.Lock()
	delete(tr.stopTeardownRetries, testID)
	delete(tr.terminalRetrying, retryKey)
	tr.terminalRetryMu.Unlock()
}

// A new Stop/Abort may arrive after a failed RPC's first version check while
// its durable error is being recorded. Retire only the version just handled.
func (tr *TestRunner) retireStopTeardownRetryIfUnchanged(testID, retryKey string, attemptedVersion uint64) bool {
	tr.terminalRetryMu.Lock()
	defer tr.terminalRetryMu.Unlock()
	if latest, exists := tr.stopTeardownRetries[testID]; exists && latest.version != attemptedVersion {
		return false
	}
	delete(tr.stopTeardownRetries, testID)
	delete(tr.terminalRetrying, retryKey)
	return true
}

const stopTeardownExternalFenceMessage = "remote stop is unconfirmed: the generator identity was rejected or its target is unknown; verify all former generator processes are stopped or fenced before resolving this run"

// A retired process epoch or wrong run identity cannot accept another Stop RPC
// with the same generation and token. Keep STOPPING and its capacity reservation
// until an operator or successor has independent stop/fence proof.
func stopTeardownRequiresExternalFence(err error) bool {
	if errors.Is(err, errDistributedGeneratorTargetUnknown) {
		return true
	}
	var fanout *distributedStopFanoutError
	if !errors.As(err, &fanout) || len(fanout.failedErrors) == 0 {
		return false
	}
	for _, failure := range fanout.failedErrors {
		if !generatorStopIdentityUnusable(failure) {
			// Another target may still accept an idempotent Stop after a
			// transient failure. Keep retrying until every outstanding target
			// either acknowledges or needs independent process-fence proof.
			return false
		}
	}
	return true
}

func (tr *TestRunner) recordStopTeardownExternalFenceRequired(testID string) error {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	test := tr.tests[testID]
	if test == nil || test.Status != "STOPPING" {
		return nil
	}
	previous := test.Error
	priorDetail := previous
	if strings.HasPrefix(priorDetail, stopTeardownExternalFenceMessage) {
		priorDetail = strings.TrimPrefix(priorDetail, stopTeardownExternalFenceMessage)
		priorDetail = strings.TrimPrefix(priorDetail, "; ")
	}
	message := stopTeardownExternalFenceMessage
	if priorDetail != "" {
		message += "; " + scrub.Value(priorDetail, scrub.Policy{})
	}
	message = boundedtext.TruncateUTF8(message, 2048)
	if previous == message {
		return nil
	}
	test.Error = message
	if err := tr.saveManagedTestRecord(test); err != nil {
		test.Error = previous
		return err
	}
	return nil
}

func stopTeardownTransition(force bool) string {
	if force {
		return "backend force-abort teardown"
	}
	return "backend graceful-stop teardown"
}

func (tr *TestRunner) AbortTest(id string) error {
	return tr.stopTestWithContextAndCIReasonAndActorMode(context.Background(), id, true, "", testCreationActor{}, true)
}

func (tr *TestRunner) AbortTestAsActor(id string, actor testCreationActor) error {
	return tr.stopTestWithContextAndCIReasonAndActorMode(context.Background(), id, true, "", actor, true)
}

func (tr *TestRunner) MarkTestAbortReason(id string, reason string) error {
	reason = normalizeCIAbortReason(reason)
	if reason == "" {
		return nil
	}
	tr.mu.Lock()
	test, ok := tr.tests[id]
	if !ok {
		tr.mu.Unlock()
		return fmt.Errorf("test not found: %s", id)
	}
	if isTerminalTestStatus(test.Status) {
		tr.mu.Unlock()
		return nil
	}
	previousError := test.Error
	test.Error = "ci abort reason: " + reason
	if err := tr.saveManagedTestRecord(test); err != nil {
		test.Error = previousError
		tr.mu.Unlock()
		return fmt.Errorf("persist CI abort reason: %w", err)
	}
	tr.mu.Unlock()
	return nil
}

func (tr *TestRunner) ScaleVUsers(id string, groupName string, targetVUsers int) error {
	return tr.ScaleVUsersWithOptions(id, groupName, targetVUsers, ScaleVUsersOptions{})
}

func (tr *TestRunner) ScaleVUsersWithOptions(id string, groupName string, targetVUsers int, opts ScaleVUsersOptions) error {
	if err := tr.rejectIterationControl(id); err != nil {
		return err
	}
	if !tr.isExecutionLeader() {
		return errNotExecutionLeader
	}
	if opts.RampDuration <= 0 {
		return tr.scaleVUsersNow(id, groupName, targetVUsers, true)
	}
	return tr.scheduleVUserScaleRamp(id, groupName, targetVUsers, opts.RampDuration)
}

func (tr *TestRunner) UpdateGoalTargetTPS(id string, groupName string, targetTPS float64) (*GoalTargetUpdate, error) {
	return tr.updateGoalTargetTPS(id, groupName, targetTPS, false)
}

func (tr *TestRunner) updateGoalTargetTPS(id string, groupName string, targetTPS float64, profileOwned bool) (*GoalTargetUpdate, error) {
	if !tr.isExecutionLeader() {
		return nil, errNotExecutionLeader
	}
	if strings.TrimSpace(groupName) == "" {
		return nil, fmt.Errorf("group name is required")
	}
	if targetTPS < 0 || (!profileOwned && targetTPS == 0) || math.IsNaN(targetTPS) || math.IsInf(targetTPS, 0) {
		bound := "greater than zero"
		if profileOwned {
			bound = "zero or greater"
		}
		return nil, fmt.Errorf("target_tps must be %s", bound)
	}

	now := time.Now().UTC()
	tr.mu.Lock()
	if !tr.isExecutionLeader() {
		tr.mu.Unlock()
		return nil, errNotExecutionLeader
	}
	id = tr.resolveRefKeyLocked(id)
	test, ok := tr.tests[id]
	if !ok {
		tr.mu.Unlock()
		return nil, fmt.Errorf("test not found: %s", id)
	}
	if test.remoteFinalizing {
		tr.mu.Unlock()
		return nil, fmt.Errorf("test is finalizing and cannot adjust goal targets")
	}
	if test.Status != "RUNNING" && test.Status != "PAUSED" {
		tr.mu.Unlock()
		return nil, fmt.Errorf("test is %s, must be RUNNING or PAUSED to adjust goal targets", test.Status)
	}
	if test.ScenarioMode != "goal_oriented" {
		tr.mu.Unlock()
		return nil, fmt.Errorf("test is %s, must be goal_oriented to adjust goal targets", firstNonEmpty(test.ScenarioMode, "vuser_driven"))
	}
	groupFound := false
	for _, group := range test.Groups {
		if group.Name == groupName {
			groupFound = true
			break
		}
	}
	if !groupFound {
		tr.mu.Unlock()
		return nil, fmt.Errorf("group not found: %s", groupName)
	}

	targets := currentGoalTargetStatuses(test, now)
	if targets == nil {
		targets = make(map[string]GoalTargetStatus, len(test.Groups))
	}
	targets[groupName] = GoalTargetStatus{
		Group:     groupName,
		TargetTPS: targetTPS,
		UpdatedAt: now,
	}
	totalTargetTPS := sumGoalTargetTPS(targets)
	if totalTargetTPS <= 0 && !profileOwned {
		tr.mu.Unlock()
		return nil, fmt.Errorf("target_average_tps must be greater than zero")
	}

	previousGoalConfig := cloneGoalConfig(test.GoalConfig)
	previousGoalTargets := cloneGoalTargetStatusMap(test.GoalTargets)
	if test.GoalConfig == nil {
		test.GoalConfig = &GoalConfig{}
	}
	test.GoalTargets = cloneGoalTargetStatusMap(targets)
	test.GoalConfig.TargetAverageTPS = &totalTargetTPS
	if err := tr.saveManagedTestRecord(test); err != nil {
		test.GoalConfig = previousGoalConfig
		test.GoalTargets = previousGoalTargets
		tr.mu.Unlock()
		return nil, fmt.Errorf("persist goal target adjustment: %w", err)
	}
	if test.goalControl != nil {
		test.goalControl.lastScaleAt = nil
	}
	if test.LiveMetrics != nil {
		updated := cloneLiveMetrics(test.LiveMetrics)
		applyRuntimeGoalTargetsToLiveMetrics(test, updated)
		replaceLiveMetricsLocked(test, updated)
	}
	projectID := normalizeProjectID(test.ProjectID)
	testName := test.Name
	result := &GoalTargetUpdate{
		TestID:           id,
		Group:            groupName,
		TargetTPS:        targetTPS,
		TargetAverageTPS: totalTargetTPS,
		GoalTargets:      cloneGoalTargetStatusMap(test.GoalTargets),
		Status:           "adjusted",
	}
	tr.mu.Unlock()

	eventType, targetKind := "goal_target_changed", "manual"
	if profileOwned {
		eventType, targetKind = "traffic_profile_target_changed", "tps"
	}
	tr.hub.BroadcastJSON(map[string]any{
		"type":               eventType,
		"target_kind":        targetKind,
		"test_id":            id,
		"project_id":         projectID,
		"test_name":          testName,
		"group":              groupName,
		"target_tps":         targetTPS,
		"target_average_tps": totalTargetTPS,
		"goal_targets":       result.GoalTargets,
		"timestamp":          now,
	})

	return result, nil
}

func (tr *TestRunner) scheduleVUserScaleRamp(id string, groupName string, targetVUsers int, rampDuration time.Duration) error {
	_, err := tr.startVUserScaleRamp(context.Background(), id, groupName, targetVUsers, rampDuration)
	return err
}

func (tr *TestRunner) startVUserScaleRamp(parent context.Context, id string, groupName string, targetVUsers int, rampDuration time.Duration) (*vuserScaleRamp, error) {
	if targetVUsers < 0 {
		return nil, fmt.Errorf("target vusers must be non-negative")
	}
	if targetVUsers > maxManagedTestVUsers {
		return nil, fmt.Errorf("target vusers cannot exceed %d", maxManagedTestVUsers)
	}
	if rampDuration <= 0 {
		return nil, fmt.Errorf("ramp duration must be positive")
	}
	tr.mu.RLock()
	resolvedID := tr.resolveRefKeyLocked(id)
	tr.mu.RUnlock()
	now := time.Now().UTC()
	evidence, err := tr.loadRuntimeLaunchEvidence(resolvedID, now, nil)
	if err != nil {
		return nil, err
	}

	tr.mu.Lock()
	if !tr.isExecutionLeader() {
		tr.mu.Unlock()
		return nil, errNotExecutionLeader
	}
	id = tr.resolveRefKeyLocked(id)
	test, ok := tr.tests[id]
	if !ok {
		tr.mu.Unlock()
		return nil, fmt.Errorf("test not found: %s", id)
	}
	if test.remoteFinalizing {
		tr.mu.Unlock()
		return nil, fmt.Errorf("test is finalizing and cannot be scaled")
	}
	if test.Status != "RUNNING" {
		tr.mu.Unlock()
		return nil, fmt.Errorf("test is %s, must be RUNNING to scale", test.Status)
	}

	currentVUsers, found := testGroupVUsers(test, groupName)
	if !found {
		tr.mu.Unlock()
		return nil, fmt.Errorf("group not found: %s", groupName)
	}
	// A gradual ramp does not reserve an unchecked target up-front: every step
	// goes through reserveVUserScaleLocked below. Validate the final requested
	// target here as well so an impossible ramp is rejected synchronously rather
	// than being accepted and failing minutes later. Concurrent capacity changes
	// are still caught by the per-step recheck.
	if _, err := tr.vuserScaleProposalWithEvidenceLocked(test, groupName, targetVUsers, now, evidence); err != nil {
		tr.mu.Unlock()
		return nil, err
	}
	tr.cancelVUserScaleRampLocked(test, groupName)
	if currentVUsers == targetVUsers {
		tr.mu.Unlock()
		return nil, nil
	}

	startedAt := time.Now()
	rampStatus := VUserRampStatus{
		Group:               groupName,
		StartVUsers:         currentVUsers,
		TargetVUsers:        targetVUsers,
		StartedAt:           startedAt,
		RampDuration:        rampDuration.String(),
		RampDurationSeconds: int64(rampDuration / time.Second),
	}
	ctx, cancel := context.WithCancel(parent)
	ramp := &vuserScaleRamp{
		cancel: cancel,
		status: rampStatus,
		done:   make(chan error, 1),
	}
	if test.vuserScaleRamps == nil {
		test.vuserScaleRamps = make(map[string]*vuserScaleRamp)
	}
	if test.VUserRamps == nil {
		test.VUserRamps = make(map[string]VUserRampStatus)
	}
	test.vuserScaleRamps[groupName] = ramp
	test.VUserRamps[groupName] = rampStatus
	projectID := normalizeProjectID(test.ProjectID)
	tr.mu.Unlock()

	tr.hub.BroadcastJSON(map[string]any{
		"type":                  "vusers_ramp_started",
		"test_id":               id,
		"project_id":            projectID,
		"group":                 rampStatus.Group,
		"current_vusers":        rampStatus.StartVUsers,
		"target_vusers":         rampStatus.TargetVUsers,
		"ramp_duration":         rampStatus.RampDuration,
		"ramp_duration_seconds": rampStatus.RampDurationSeconds,
		"timestamp":             startedAt,
	})

	go tr.runVUserScaleRamp(ctx, ramp, id, groupName, currentVUsers, targetVUsers, rampDuration)
	return ramp, nil
}

func (tr *TestRunner) runVUserScaleRamp(ctx context.Context, ramp *vuserScaleRamp, id string, groupName string, currentVUsers int, targetVUsers int, rampDuration time.Duration) {
	var result error
	defer func() {
		tr.finishVUserScaleRamp(id, groupName, ramp)
		ramp.done <- result
		close(ramp.done)
	}()

	targets := vuserRampTargets(currentVUsers, targetVUsers)
	if len(targets) == 0 {
		return
	}
	interval := rampDuration / time.Duration(len(targets))
	if interval <= 0 {
		interval = time.Nanosecond
	}

	for _, nextTarget := range targets {
		if !tr.waitTestActiveDuration(ctx, id, interval) {
			result = ctx.Err()
			if result == nil {
				result = context.Canceled
			}
			return
		}

		for {
			err := tr.scaleVUsersNow(id, groupName, nextTarget, false)
			if err == nil {
				break
			}
			if errors.Is(err, engine.ErrInitialRampInProgress) {
				if !waitContextDuration(ctx, initialRampScaleRetryInterval) {
					result = ctx.Err()
					if result == nil {
						result = context.Canceled
					}
					return
				}
				continue
			}
			if tr.testExecutionStatus(id) == "PAUSED" && tr.waitTestRunning(ctx, id) {
				continue
			}
			result = err
			slog.Warn("gradual vuser scale stopped", "test_id", id, "group", groupName, "target_vusers", nextTarget, "error", err)
			return
		}
	}
}

func waitContextDuration(ctx context.Context, delay time.Duration) bool {
	if ctx == nil {
		ctx = context.Background()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// A gradual scale used to persist one full test row per VUser. Large changes
// therefore generated thousands of writes and could occupy the lifecycle lock
// for most of the requested ramp. Keep the fine-grained behavior for ordinary
// changes, but bound large ramps while retaining monotonic, exact endpoints.
const maxVUserRampSteps = 100

const initialRampScaleRetryInterval = 200 * time.Millisecond

func vuserRampTargets(current, target int) []int {
	delta := target - current
	distance := testexec.AbsInt(delta)
	if distance == 0 {
		return nil
	}
	steps := min(distance, maxVUserRampSteps)
	targets := make([]int, 0, steps)
	for step := 1; step <= steps; step++ {
		next := int(int64(current) + int64(delta)*int64(step)/int64(steps))
		targets = append(targets, next)
	}
	return targets
}

const executionClockPollInterval = 10 * time.Millisecond

// waitTestActiveDuration waits on the test's active execution clock. The clock
// subtracts both completed and in-progress pauses, so a ramp or stage hold does
// not consume its configured duration while the run is paused.
func (tr *TestRunner) waitTestActiveDuration(ctx context.Context, testID string, duration time.Duration) bool {
	if duration <= 0 {
		return true
	}
	var startClock time.Duration
	started := false
	poll := min(duration, executionClockPollInterval)
	if poll <= 0 {
		poll = time.Nanosecond
	}
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		clock, status, ok := tr.testActiveClock(testID, time.Now().UTC())
		if !ok || isTerminalTestStatus(status) || status == "STOPPING" {
			return false
		}
		if status == "RUNNING" || status == "PAUSED" {
			if !started {
				startClock = clock
				started = true
			}
			if clock-startClock >= duration {
				return status == "RUNNING" || tr.waitTestRunning(ctx, testID)
			}
		}
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
		}
	}
}

func (tr *TestRunner) testActiveClock(testID string, now time.Time) (time.Duration, string, bool) {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	test := tr.tests[testID]
	if test == nil {
		return 0, "", false
	}
	activeClock := time.Duration(now.UnixNano()) - test.CumulativePause
	if test.PausedAt != nil && !test.PausedAt.IsZero() && now.After(*test.PausedAt) {
		activeClock -= now.Sub(*test.PausedAt)
	}
	return activeClock, test.Status, true
}

func (tr *TestRunner) testExecutionStatus(testID string) string {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	if test := tr.tests[testID]; test != nil {
		return test.Status
	}
	return ""
}

func (tr *TestRunner) waitTestRunning(ctx context.Context, testID string) bool {
	ticker := time.NewTicker(executionClockPollInterval)
	defer ticker.Stop()
	for {
		switch tr.testExecutionStatus(testID) {
		case "RUNNING":
			return true
		case "PAUSED", "INITIALIZING":
		default:
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
		}
	}
}

func (tr *TestRunner) startStageSchedules(ctx context.Context, test *ManagedTest, mge *engine.MultiGroupEngine) {
	if !tr.isExecutionLeader() {
		return
	}
	tr.mu.RLock()
	if test == nil || test.ScenarioMode != "vuser_driven" {
		tr.mu.RUnlock()
		return
	}
	testID := test.ID
	groups := make([]TestGroup, 0, len(test.Groups))
	for _, group := range test.Groups {
		if group.stageDriven && len(group.Stages) > 0 {
			group.Stages = cloneLoadStages(group.Stages)
			groups = append(groups, group)
		}
	}
	preBaseline, _ := testexec.ResourceBaselineWindows(test.ResourceBaseline)
	preBaseline += testexec.StartDelayWindow(test.StartDelay)
	tr.mu.RUnlock()

	for _, group := range groups {
		group := group
		startOffset := groupStartOffsetDuration(group)
		if startOffset > time.Duration(math.MaxInt64)-preBaseline {
			startOffset = time.Duration(math.MaxInt64)
		} else {
			startOffset += preBaseline
		}
		go tr.runStageSchedule(ctx, testID, group.Name, group.Stages, startOffset, mge)
	}
}

func (tr *TestRunner) runStageSchedule(ctx context.Context, testID string, groupName string, stages []storage.LoadStage, activeOffset time.Duration, mge *engine.MultiGroupEngine) {
	// Stage definitions are relative to group activation, not controller
	// admission. Anchor them to the same pause-aware run clock as generator-side
	// StartOffset so a delayed group cannot be scaled before it activates. This
	// is an absolute target, so readiness or commit latency does not add drift.
	if !tr.waitTestActiveRuntime(ctx, testID, activeOffset) {
		return
	}
	// A schedule resumed after HA adoption joins a run already in progress: its
	// generators hold the load of the stage that was active, so the readiness
	// probe (which rescales to the initial VUsers) is skipped and completed
	// stages are fast-forwarded against the persisted run clock.
	tr.mu.RLock()
	resuming := false
	var resumeElapsed time.Duration
	if current := tr.tests[testID]; current != nil && current.stageScheduleResume {
		resuming = true
		resumeElapsed = testActiveRuntimeLocked(current, time.Now().UTC()) - activeOffset
	}
	tr.mu.RUnlock()
	if mge != nil {
		select {
		case <-ctx.Done():
			return
		case <-mge.Ready():
		}
		// Ready exposes pause/stop controls before configured VUsers finish their
		// initial preparation/ramp. Stage scaling needs the narrower admission
		// barrier or its first valid stage can be rejected and abandoned.
		ticker := time.NewTicker(executionClockPollInterval)
		defer ticker.Stop()
		for !mge.ScalingReady() {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}
	if mge == nil && !resuming {
		// Distributed generators expose initial-ramp readiness through the typed
		// scale precondition rather than a local ScalingReady channel. Probe the
		// already-configured target and retry only that transient sentinel before
		// starting any stage clock; otherwise a slow Init silently consumes the
		// first hold/ramp duration while no load is running.
		tr.mu.RLock()
		currentTest := tr.tests[testID]
		currentVUsers, found := testGroupVUsers(currentTest, groupName)
		distributed := false
		if currentTest != nil {
			_, distributed = tr.resolveBackend(currentTest).(initialRampReadinessProbeExecutionBackend)
		}
		tr.mu.RUnlock()
		if currentTest == nil || ctx.Err() != nil {
			return
		}
		if distributed && found {
			for {
				err := tr.scaleVUsersNow(testID, groupName, currentVUsers, false)
				if err == nil {
					break
				}
				if errors.Is(err, engine.ErrInitialRampInProgress) {
					if !waitContextDuration(ctx, initialRampScaleRetryInterval) {
						return
					}
					continue
				}
				if tr.testExecutionStatus(testID) == "PAUSED" && tr.waitTestRunning(ctx, testID) {
					continue
				}
				slog.Warn("stage schedule readiness probe failed", "test_id", testID, "group", groupName, "error", err)
				return
			}
		}
	}
	defer tr.clearActiveLoadStage(testID, groupName)
	// level is the last load level announced as a phase event; -1 until one is.
	level := -1
	for index, stage := range stages {
		if !tr.runtimeGroupAcceptsStages(testID, groupName) {
			return
		}
		stageType := strings.ToLower(strings.TrimSpace(stage.Type))
		duration, err := time.ParseDuration(stage.Duration)
		if err != nil || duration <= 0 {
			slog.Warn("stage schedule stopped on invalid stage duration", "test_id", testID, "group", groupName, "stage", index, "duration", stage.Duration, "error", err)
			return
		}
		if resuming {
			if resumeElapsed >= duration {
				if index < len(stages)-1 {
					resumeElapsed -= duration
					continue
				}
				// The entire schedule elapsed during the outage. Reapply its
				// final target, which may never have reached the generators.
				stageType, duration = "hold", 0
			} else {
				duration -= max(resumeElapsed, 0)
			}
			// Resume mid-stage: a hold reapplies its target for the remainder; a
			// ramp continues from the current VUsers over the remaining time.
			resuming = false
		}
		switch stageType {
		case "ramp_up", "ramp_down":
			if !tr.waitTestRunning(ctx, testID) {
				return
			}
			ramp, err := tr.startVUserScaleRamp(ctx, testID, groupName, stage.TargetVUsers, duration)
			if err != nil {
				slog.Warn("stage schedule scale failed", "test_id", testID, "group", groupName, "stage", index, "target_vusers", stage.TargetVUsers, "error", err)
				return
			}
			tr.setActiveLoadStage(testID, groupName, index, stage)
			if ramp == nil {
				// Already at the target: the level holds for this stage.
				if level != stage.TargetVUsers {
					tr.recordLoadPhase(testID, PhaseLevelReached, groupName, stage.TargetVUsers, time.Now())
					level = stage.TargetVUsers
				}
				if !tr.waitTestActiveDuration(ctx, testID, duration) {
					return
				}
				continue
			}
			tr.recordLoadPhase(testID, PhaseRampStart, groupName, stage.TargetVUsers, time.Now())
			select {
			case <-ctx.Done():
				ramp.cancel()
				return
			case err := <-ramp.done:
				if err != nil {
					if errors.Is(err, context.Canceled) {
						return
					}
					slog.Warn("stage schedule ramp stopped", "test_id", testID, "group", groupName, "stage", index, "target_vusers", stage.TargetVUsers, "error", err)
					return
				}
			}
			tr.recordLoadPhase(testID, PhaseLevelReached, groupName, stage.TargetVUsers, time.Now())
			level = stage.TargetVUsers
			continue
		case "hold":
			// The first hold is seeded by compilation, but later holds can define a
			// different explicit target (the dashboard's stair-step model uses
			// consecutive holds for this). Apply that target at the stage boundary
			// instead of assuming every hold inherits the preceding load.
			if !tr.waitTestRunning(ctx, testID) {
				return
			}
			if err := tr.applyStageHoldTarget(ctx, testID, groupName, stage.TargetVUsers); err != nil {
				slog.Warn("stage schedule hold scale failed", "test_id", testID, "group", groupName, "stage", index, "target_vusers", stage.TargetVUsers, "error", err)
				return
			}
			tr.setActiveLoadStage(testID, groupName, index, stage)
			if level != stage.TargetVUsers {
				// A hold with its own target is a step: the level changes at once.
				tr.recordLoadPhase(testID, PhaseLevelChange, groupName, stage.TargetVUsers, time.Now())
				level = stage.TargetVUsers
			}
		default:
			slog.Warn("stage schedule stopped on invalid stage type", "test_id", testID, "group", groupName, "stage", index, "type", stage.Type)
			return
		}
		if !tr.waitTestActiveDuration(ctx, testID, duration) {
			return
		}
	}
	if tr.closedVUserStopsAfterStageSchedule(testID, groupName) {
		if !tr.waitTestRunning(ctx, testID) {
			return
		}
		if err := tr.scaleVUsersNow(testID, groupName, 0, false); err != nil && ctx.Err() == nil {
			slog.Warn("closed VUser group stop failed", "test_id", testID, "group", groupName, "error", err)
		}
	}
}

func (tr *TestRunner) closedVUserStopsAfterStageSchedule(testID, groupName string) bool {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	test := tr.tests[testID]
	if test == nil || test.GoalConfig == nil || test.GoalConfig.WorkloadShapeConfig == nil || test.GoalConfig.WorkloadShapeConfig.ClosedVUser == nil {
		return false
	}
	config := test.GoalConfig.WorkloadShapeConfig.ClosedVUser
	if config.Scope == "scenario" {
		return config.Scenario != nil && !config.Scenario.RampDownEnabled
	}
	for index := range test.Groups {
		if test.Groups[index].Name != groupName {
			continue
		}
		if definition, ok := config.Groups[fmt.Sprintf("group_%d", index)]; ok {
			return !definition.RampDownEnabled
		}
		if definition, ok := config.Groups[groupName]; ok {
			return !definition.RampDownEnabled
		}
		return false
	}
	return false
}

func (tr *TestRunner) setActiveLoadStage(testID, groupName string, index int, stage storage.LoadStage) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	test := tr.tests[testID]
	if test == nil {
		return
	}
	if test.GoalConfig != nil && test.GoalConfig.WorkloadShapeConfig != nil && test.GoalConfig.WorkloadShapeConfig.ClosedVUser != nil && test.GoalConfig.WorkloadShapeConfig.SteppedRamp == nil {
		return
	}
	for groupIndex := range test.Groups {
		if test.Groups[groupIndex].Name != groupName {
			continue
		}
		test.Groups[groupIndex].ActiveStage = &ActiveLoadLevel{
			Group:        groupName,
			StageIndex:   index,
			Name:         stage.Name,
			TargetVUsers: stage.TargetVUsers,
			Duration:     stage.Duration,
			StartedAt:    time.Now().UTC(),
		}
		return
	}
}

func (tr *TestRunner) clearActiveLoadStage(testID, groupName string) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	test := tr.tests[testID]
	if test == nil {
		return
	}
	for groupIndex := range test.Groups {
		if test.Groups[groupIndex].Name == groupName {
			test.Groups[groupIndex].ActiveStage = nil
			return
		}
	}
}

func (tr *TestRunner) runtimeGroupAcceptsStages(testID, groupName string) bool {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	test := tr.tests[testID]
	if test == nil {
		return false
	}
	for _, group := range test.Groups {
		if group.Name == groupName {
			return group.RuntimeState != "stopping" && group.RuntimeState != "ended"
		}
	}
	return false
}

// applyStageHoldTarget enforces the absolute load at a hold boundary. Existing
// ramps must not continue past that boundary, including when the group already
// happens to be at the requested target. A pause can race the readiness check in
// runStageSchedule, so retry after resume instead of abandoning the remaining
// stage schedule on that transient lifecycle transition.
func (tr *TestRunner) applyStageHoldTarget(ctx context.Context, testID string, groupName string, targetVUsers int) error {
	for {
		tr.mu.Lock()
		currentTest := tr.tests[testID]
		if currentTest == nil {
			tr.mu.Unlock()
			return fmt.Errorf("test not found: %s", testID)
		}
		if !runtimeGroupAcceptsStagesLocked(currentTest, groupName) {
			tr.mu.Unlock()
			return context.Canceled
		}
		currentVUsers, found := testGroupVUsers(currentTest, groupName)
		if found && currentVUsers == targetVUsers {
			tr.cancelVUserScaleRampLocked(currentTest, groupName)
			tr.mu.Unlock()
			return nil
		}
		tr.mu.Unlock()
		if !found {
			return fmt.Errorf("group not found: %s", groupName)
		}

		err := tr.scaleVUsersNow(testID, groupName, targetVUsers, true)
		if err == nil {
			return nil
		}
		if errors.Is(err, engine.ErrInitialRampInProgress) {
			if !waitContextDuration(ctx, initialRampScaleRetryInterval) {
				if ctxErr := ctx.Err(); ctxErr != nil {
					return ctxErr
				}
				return err
			}
			continue
		}
		if tr.testExecutionStatus(testID) != "PAUSED" {
			return err
		}
		if !tr.waitTestRunning(ctx, testID) {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			return err
		}
	}
}

func runtimeGroupAcceptsStagesLocked(test *ManagedTest, groupName string) bool {
	if test == nil {
		return false
	}
	for _, group := range test.Groups {
		if group.Name == groupName {
			return group.RuntimeState != "stopping" && group.RuntimeState != "ended"
		}
	}
	return false
}

type vuserScaleAdmissionGuard func(current *ManagedTest) error

func (tr *TestRunner) scaleVUsersNow(id string, groupName string, targetVUsers int, cancelExistingRamp bool) error {
	return tr.scaleVUsersNowWithAdmissionGuard(id, groupName, targetVUsers, cancelExistingRamp, nil)
}

// scaleVUsersNowWithAdmissionGuard lets an autonomous controller bind its
// decision to the exact managed run state that produced it. The guard executes
// under tr.mu after the per-group/topology control gate is acquired and
// immediately before the durable VUser reservation, so a replacement run or a
// newer topology/metrics publication cannot inherit a stale decision.
func (tr *TestRunner) scaleVUsersNowWithAdmissionGuard(id string, groupName string, targetVUsers int, cancelExistingRamp bool, guard vuserScaleAdmissionGuard) error {
	tr.mu.RLock()
	resolvedID := tr.resolveRefKeyLocked(id)
	tr.mu.RUnlock()
	unlockControl := tr.lockDistributedGroupControl(resolvedID, groupName)
	defer unlockControl()
	if !tr.isExecutionLeader() {
		return errNotExecutionLeader
	}
	now := time.Now().UTC()
	evidence, err := tr.loadRuntimeLaunchEvidence(resolvedID, now, nil)
	if err != nil {
		return err
	}

	tr.mu.Lock()
	id = tr.resolveRefKeyLocked(id)
	if guard != nil {
		if err := guard(tr.tests[id]); err != nil {
			tr.mu.Unlock()
			return err
		}
	}
	reservation, err := tr.reserveVUserScaleLockedWithEvidence(id, groupName, targetVUsers, now, evidence)
	if err != nil {
		tr.mu.Unlock()
		return err
	}
	test := reservation.test
	if cancelExistingRamp {
		tr.cancelVUserScaleRampLocked(test, groupName)
	}
	multiEngine := test.multiEngine
	backend := tr.resolveBackend(test)
	totalVUsers := test.TotalVUsers
	projectID := normalizeProjectID(test.ProjectID)
	tr.mu.Unlock()

	var scaleErr error
	if multiEngine == nil {
		scalable, ok := backend.(scalableExecutionBackend)
		if !ok {
			scaleErr = fmt.Errorf("test backend does not support scaling vusers")
		} else {
			scaleErr = scalable.ScaleVUsers(tr, test, groupName, targetVUsers)
		}
	} else {
		scaleErr = multiEngine.ScaleVUsers(groupName, targetVUsers)
	}
	if scaleErr != nil {
		var uncertain *distributedVUserUncertainError
		if errors.As(scaleErr, &uncertain) {
			tr.mu.Lock()
			if test.scaleAdjustmentPrevious != nil {
				delete(test.scaleAdjustmentPrevious, groupName)
			}
			tr.mu.Unlock()
			return tr.failClosedUncertainVUserScale(reservation, backend, scaleErr)
		}
		tr.mu.Lock()
		if test.scaleAdjustmentPrevious != nil {
			delete(test.scaleAdjustmentPrevious, groupName)
		}
		tr.mu.Unlock()
		if rollbackErr := tr.rollbackVUserScaleReservation(reservation); rollbackErr != nil {
			controlErr := errors.Join(scaleErr, fmt.Errorf("rollback VUser scale reservation: %w", rollbackErr))
			return tr.failClosedUncertainVUserScale(reservation, backend, controlErr)
		}
		return scaleErr
	}
	tr.mu.Lock()
	if test.scaleAdjustmentPrevious != nil {
		delete(test.scaleAdjustmentPrevious, groupName)
	}
	tr.mu.Unlock()
	if !tr.isExecutionLeader() {
		// The backend mutation and durable accounting already agree. Do not roll
		// back accounting after an applied scale merely because leadership was
		// lost while the backend acknowledged it.
		return errNotExecutionLeader
	}
	tr.hub.BroadcastJSON(map[string]any{
		"type":          "vusers_changed",
		"test_id":       id,
		"project_id":    projectID,
		"group":         groupName,
		"target_vusers": targetVUsers,
		"total_vusers":  totalVUsers,
		"timestamp":     time.Now(),
	})

	return nil
}

func (tr *TestRunner) failClosedUncertainVUserScale(reservation *vuserScaleReservation, backend testExecutionBackend, controlErr error) error {
	if reservation == nil || reservation.test == nil {
		return controlErr
	}
	test := reservation.test
	tr.mu.Lock()
	if tr.tests[test.ID] != test || (test.Status != "RUNNING" && test.Status != "PAUSED") || reservation.groupIndex < 0 || reservation.groupIndex >= len(test.Groups) {
		current := test.Status
		tr.mu.Unlock()
		return errors.Join(controlErr, fmt.Errorf("cannot persist uncertain VUser adjustment; test is now %s", current))
	}
	// Either the forward absolute target or the compensation may be live. Keep
	// the larger allocation reserved until fail-closed teardown completes.
	if groupCurrentVUsers(reservation.oldGroup) > groupCurrentVUsers(reservation.newGroup) {
		test.Groups[reservation.groupIndex] = reservation.oldGroup
	} else {
		test.Groups[reservation.groupIndex] = reservation.newGroup
	}
	test.TotalVUsers = capacityVUsersForGroups(test.Groups)
	tr.refreshCapacityAdvisoryLocked(test)
	test.Status = "STOPPING"
	test.Error = "remote VUser state is uncertain: " + controlErr.Error()
	persistErr := retryDurableTransition(func() error { return tr.saveManagedTestRecord(test) })
	tr.cancelAllVUserScaleRampsLocked(test)
	cancel := test.cancel
	eventSnapshot := cloneManagedTestFieldsLocked(test)
	tr.mu.Unlock()
	tr.broadcastEvent("test_stopping", eventSnapshot)

	var stopErr error
	if stoppable, ok := backend.(contextStoppableExecutionBackend); ok {
		stopErr = stoppable.StopTestContext(context.Background(), tr, test)
	} else if stoppable, ok := backend.(stoppableExecutionBackend); ok {
		stopErr = stoppable.StopTest(tr, test)
	} else {
		stopErr = fmt.Errorf("test backend does not support fail-closed stop")
	}
	if cancel != nil {
		cancel()
	}
	if stopErr != nil {
		tr.enqueueStopTeardownRetry(test.ID, false)
	}
	var persistWrapped error
	if persistErr != nil {
		persistWrapped = fmt.Errorf("persist fail-closed VUser state: %w", persistErr)
	}
	var stopWrapped error
	if stopErr != nil {
		stopWrapped = fmt.Errorf("stop after uncertain VUser adjustment: %w", stopErr)
	}
	return errors.Join(controlErr, persistWrapped, stopWrapped)
}

type vuserScaleReservation struct {
	test        *ManagedTest
	groupIndex  int
	oldGroup    TestGroup
	newGroup    TestGroup
	oldTotal    int
	newTotal    int
	oldAdvisory *CapacityAdvisory
}

func (tr *TestRunner) vuserScaleProposalWithEvidenceLocked(test *ManagedTest, groupName string, targetVUsers int, now time.Time, evidence launchWindowEvidence) (*ManagedTest, error) {
	if test == nil {
		return nil, fmt.Errorf("test is required")
	}
	if !tr.isExecutionLeader() {
		return nil, errNotExecutionLeader
	}
	if test.remoteFinalizing {
		return nil, fmt.Errorf("test is finalizing and cannot be scaled")
	}
	if test.Status != "RUNNING" {
		return nil, fmt.Errorf("test is %s, must be RUNNING to scale", test.Status)
	}
	if targetVUsers < 0 {
		return nil, fmt.Errorf("target vusers must be non-negative")
	}
	if targetVUsers > maxManagedTestVUsers {
		return nil, fmt.Errorf("target vusers cannot exceed %d", maxManagedTestVUsers)
	}
	if err := tr.distributedActivationErrorLocked(test); err != nil {
		return nil, err
	}
	if !tr.testLoadReadyLocked(test, now) {
		return nil, fmt.Errorf("%w; retry after start delay and resource baseline complete", errTestLoadNotReady)
	}
	proposal := *test
	proposal.Groups = append([]TestGroup(nil), test.Groups...)
	found := false
	for i := range proposal.Groups {
		if proposal.Groups[i].Name != groupName {
			continue
		}
		found = true
		if proposal.Groups[i].RuntimeState == "ended" && targetVUsers > 0 {
			return nil, fmt.Errorf("group %q has ended and cannot be restarted", groupName)
		}
		if proposal.Groups[i].Role == GroupRoleObserver && targetVUsers > observerMaxVUsers {
			return nil, fmt.Errorf("observer group %q cannot exceed %d VUsers", groupName, observerMaxVUsers)
		}
		if proposal.Groups[i].stageDriven {
			proposal.Groups[i].currentVUsers = targetVUsers
		} else {
			proposal.Groups[i].VUsers = targetVUsers
		}
		break
	}
	if !found {
		return nil, fmt.Errorf("group not found: %s", groupName)
	}
	proposal.TotalVUsers = capacityVUsersForGroups(proposal.Groups)
	tr.refreshCapacityAdvisoryLocked(&proposal)
	projectQuota, quotaOK := evidence.projectQuota.quotaFor(&proposal)
	if !quotaOK {
		return nil, fmt.Errorf("project quota evidence changed while VUser scale admission was evaluated; retry")
	}
	if err := enforceTestQuotaWithProjectQuota(projectQuota, managedTestCapacityVUsers(&proposal), proposal.Duration, proposal.Status); err != nil {
		return nil, err
	}
	if blocked, reason := tr.evaluateQuotaBlockWithLaunchEvidenceLocked(&proposal, test.ID, now, evidence); blocked {
		return nil, fmt.Errorf("VUser scale admission blocked: %s", reason)
	}
	if err := tr.launchTimeslotRevalidationErrorWithEvidenceLocked(&proposal, now, evidence); err != nil {
		return nil, err
	}
	// Persisted tests created by the normal API always carry an advisory. Some
	// legacy/restored records (and lightweight backend adapters) predate it; do
	// not manufacture a zero-inventory shortfall for those already-running runs.
	if test.CapacityAdvisory != nil {
		if err := buildStartGovernanceError(test.ID, proposal.CapacityAdvisory, StartTestOptions{}); err != nil {
			return nil, err
		}
	}
	if err := tr.runtimeAdjustmentWindowErrorWithEvidenceLocked(&proposal, now, evidence); err != nil {
		return nil, err
	}
	return &proposal, nil
}

func capacityVUsersForGroups(groups []TestGroup) int {
	total := 0
	for _, group := range groups {
		reserved := group.VUsers
		if group.stageDriven && group.currentVUsers > reserved {
			reserved = group.currentVUsers
		}
		total = saturatingAddNonNegative(total, reserved)
	}
	return total
}

func (tr *TestRunner) reserveVUserScaleLockedWithEvidence(id, groupName string, targetVUsers int, now time.Time, evidence launchWindowEvidence) (*vuserScaleReservation, error) {
	test, ok := tr.tests[id]
	if !ok {
		return nil, fmt.Errorf("test not found: %s", id)
	}
	proposal, err := tr.vuserScaleProposalWithEvidenceLocked(test, groupName, targetVUsers, now, evidence)
	if err != nil {
		return nil, err
	}
	groupIndex := -1
	for i := range test.Groups {
		if test.Groups[i].Name == groupName {
			groupIndex = i
			break
		}
	}
	reservation := &vuserScaleReservation{
		test:        test,
		groupIndex:  groupIndex,
		oldGroup:    test.Groups[groupIndex],
		newGroup:    proposal.Groups[groupIndex],
		oldTotal:    test.TotalVUsers,
		newTotal:    proposal.TotalVUsers,
		oldAdvisory: cloneCapacityAdvisory(test.CapacityAdvisory),
	}
	if test.scaleAdjustmentPrevious == nil {
		test.scaleAdjustmentPrevious = make(map[string]int)
	}
	test.scaleAdjustmentPrevious[groupName] = groupCurrentVUsers(reservation.oldGroup)
	test.Groups[groupIndex] = proposal.Groups[groupIndex]
	test.TotalVUsers = proposal.TotalVUsers
	if reservation.oldAdvisory != nil {
		test.CapacityAdvisory = cloneCapacityAdvisory(proposal.CapacityAdvisory)
	} else {
		test.CapacityAdvisory = nil
	}
	// Only the group workload snapshot and the run total change here, so publish
	// the narrow durable projection: a gradual ramp reserves once per step and
	// the full-row upsert would re-encode every compiled script under tr.mu each
	// time. CapacityAdvisory is process-local and is not persisted either way.
	if err := tr.persistManagedTestVUserScale(test); err != nil {
		delete(test.scaleAdjustmentPrevious, groupName)
		test.Groups[groupIndex] = reservation.oldGroup
		test.TotalVUsers = reservation.oldTotal
		test.CapacityAdvisory = reservation.oldAdvisory
		return nil, fmt.Errorf("persist VUser scale reservation: %w", err)
	}
	return reservation, nil
}

func (tr *TestRunner) rollbackVUserScaleReservation(reservation *vuserScaleReservation) error {
	if reservation == nil || reservation.test == nil {
		return nil
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	test := reservation.test
	if tr.tests[test.ID] != test || reservation.groupIndex < 0 || reservation.groupIndex >= len(test.Groups) {
		return nil
	}
	currentGroup := test.Groups[reservation.groupIndex]
	// Per-group lifecycle control serializes this group, but retain a defensive
	// identity check so rollback can never overwrite a newer target.
	if groupCurrentVUsers(currentGroup) != groupCurrentVUsers(reservation.newGroup) {
		return nil
	}
	currentTotal := test.TotalVUsers
	currentAdvisory := cloneCapacityAdvisory(test.CapacityAdvisory)
	test.Groups[reservation.groupIndex] = reservation.oldGroup
	test.TotalVUsers = capacityVUsersForGroups(test.Groups)
	tr.refreshCapacityAdvisoryLocked(test)
	if err := tr.persistManagedTestVUserScale(test); err != nil {
		test.Groups[reservation.groupIndex] = currentGroup
		test.TotalVUsers = currentTotal
		test.CapacityAdvisory = currentAdvisory
		return err
	}
	return nil
}

func (tr *TestRunner) runtimeAdjustmentWindowErrorWithEvidenceLocked(test *ManagedTest, now time.Time, evidence launchWindowEvidence) error {
	if test == nil {
		return nil
	}
	start := now.UTC()
	originalStart := calendarTestStart(test)
	if originalStart.IsZero() {
		originalStart = start
	}
	// A distributed run transitions to RUNNING before its coordinated commit
	// barrier. During that short interval StartedAt is legitimately in the
	// future, so the remaining runtime cannot begin at the earlier wall clock.
	// Keeping the validation window inside the evidence window also preserves
	// the stale-evidence fence for genuine concurrent duration changes.
	if originalStart.After(start) {
		start = originalStart.UTC()
	}
	full := launchWindowForTestAt(test, originalStart.UTC(), now.UTC())
	if !full.EndAt.After(start) {
		return nil
	}
	window := full
	window.StartAt = start
	window.DurationSeconds = int(window.EndAt.Sub(window.StartAt).Seconds())
	conflicts := tr.launchWindowConflictsWithEvidenceLocked(test, window, start, test.ID, "", evidence)
	if len(conflicts) == 0 {
		return nil
	}
	return fmt.Errorf("runtime adjustment conflicts with launch window: %s", conflicts[0].Message)
}

func (tr *TestRunner) cancelVUserScaleRampLocked(test *ManagedTest, groupName string) {
	if test == nil || test.vuserScaleRamps == nil {
		return
	}
	if ramp, ok := test.vuserScaleRamps[groupName]; ok {
		ramp.cancel()
		delete(test.vuserScaleRamps, groupName)
	}
	if test.VUserRamps != nil {
		delete(test.VUserRamps, groupName)
		if len(test.VUserRamps) == 0 {
			test.VUserRamps = nil
		}
	}
	if len(test.vuserScaleRamps) == 0 {
		test.vuserScaleRamps = nil
	}
}

func (tr *TestRunner) cancelAllVUserScaleRampsLocked(test *ManagedTest) {
	if test == nil || test.vuserScaleRamps == nil {
		return
	}
	for groupName, ramp := range test.vuserScaleRamps {
		ramp.cancel()
		delete(test.vuserScaleRamps, groupName)
	}
	test.vuserScaleRamps = nil
	test.VUserRamps = nil
}

func (tr *TestRunner) finishVUserScaleRamp(id string, groupName string, ramp *vuserScaleRamp) {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	test, ok := tr.tests[id]
	if !ok || test.vuserScaleRamps == nil {
		return
	}
	if test.vuserScaleRamps[groupName] == ramp {
		delete(test.vuserScaleRamps, groupName)
		if test.VUserRamps != nil {
			delete(test.VUserRamps, groupName)
			if len(test.VUserRamps) == 0 {
				test.VUserRamps = nil
			}
		}
	}
	if len(test.vuserScaleRamps) == 0 {
		test.vuserScaleRamps = nil
	}
}

func cloneVUserRampStatusMap(in map[string]VUserRampStatus) map[string]VUserRampStatus {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]VUserRampStatus, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneGoalTargetStatusMap(in map[string]GoalTargetStatus) map[string]GoalTargetStatus {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]GoalTargetStatus, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func testGroupVUsers(test *ManagedTest, groupName string) (int, bool) {
	if test == nil {
		return 0, false
	}
	for _, group := range test.Groups {
		if group.Name == groupName {
			return groupCurrentVUsers(group), true
		}
	}
	return 0, false
}

func groupCurrentVUsers(group TestGroup) int {
	if group.stageDriven {
		return group.currentVUsers
	}
	return group.VUsers
}

func (tr *TestRunner) applyDurationAdjustment(test *ManagedTest, multiEngine *engine.MultiGroupEngine, backend testExecutionBackend, duration time.Duration) error {
	if multiEngine != nil {
		return multiEngine.AdjustDuration(duration)
	}
	adjustable, ok := backend.(durationAdjustableExecutionBackend)
	if !ok {
		return fmt.Errorf("test backend does not support duration changes")
	}
	return adjustable.AdjustDuration(tr, test, duration)
}

func (tr *TestRunner) applyPause(test *ManagedTest, multiEngine *engine.MultiGroupEngine, backend testExecutionBackend) error {
	if multiEngine != nil {
		tr.mu.RLock()
		parent := test.executionCtx
		tr.mu.RUnlock()
		if parent == nil {
			parent = context.Background()
		}
		ctx, cancel := context.WithTimeout(parent, localPauseQuiescenceTimeout)
		defer cancel()
		return multiEngine.PauseAndWait(ctx)
	}
	pausable, ok := backend.(pausableExecutionBackend)
	if !ok {
		return fmt.Errorf("test backend does not support pause")
	}
	return pausable.PauseTest(tr, test)
}

func (tr *TestRunner) applyResume(test *ManagedTest, multiEngine *engine.MultiGroupEngine, backend testExecutionBackend) error {
	if multiEngine != nil {
		return multiEngine.Resume()
	}
	pausable, ok := backend.(pausableExecutionBackend)
	if !ok {
		return fmt.Errorf("test backend does not support resume")
	}
	return pausable.ResumeTest(tr, test)
}

// rollbackDurationReservationLocked restores the controller's pre-adjustment
// duration after a remote adjustment was rejected or compensated. The caller
// holds tr.mu. A different duration means another authoritative transition has
// already superseded this request and must not be overwritten.
func (tr *TestRunner) rollbackDurationReservationLocked(test *ManagedTest, appliedDuration, oldDuration time.Duration, oldDurationStr string, oldAdvisory *CapacityAdvisory) error {
	test.durationAdjustmentPrevious = 0
	if test.Duration != appliedDuration {
		return nil
	}
	currentDuration := test.Duration
	currentDurationStr := test.DurationStr
	currentAdvisory := cloneCapacityAdvisory(test.CapacityAdvisory)
	test.Duration = oldDuration
	test.DurationStr = oldDurationStr
	test.CapacityAdvisory = cloneCapacityAdvisory(oldAdvisory)
	if err := retryDurableTransition(func() error { return tr.saveManagedTestRecord(test) }); err != nil {
		test.Duration = currentDuration
		test.DurationStr = currentDurationStr
		test.CapacityAdvisory = currentAdvisory
		return err
	}
	return nil
}

func (tr *TestRunner) AdjustDuration(id string, newDuration time.Duration) error {
	if err := tr.rejectIterationControl(id); err != nil {
		return err
	}
	if !tr.isExecutionLeader() {
		return errNotExecutionLeader
	}
	tr.mu.RLock()
	id = tr.resolveRefKeyLocked(id)
	tr.mu.RUnlock()
	unlockControl, err := tr.lockTestLifecycleControl(context.Background(), id)
	if err != nil {
		return err
	}
	defer unlockControl()
	unlockTopology := tr.lockDistributedTopologyReadControl(id)
	defer unlockTopology()
	if !tr.isExecutionLeader() {
		return errNotExecutionLeader
	}

	if newDuration <= 0 {
		return fmt.Errorf("duration must be positive")
	}
	now := time.Now().UTC()
	evidence, err := tr.loadRuntimeLaunchEvidence(id, now, func(test *ManagedTest) {
		test.Duration = newDuration
		test.DurationStr = newDuration.String()
	})
	if err != nil {
		return err
	}

	tr.mu.Lock()
	test, ok := tr.tests[id]
	if !ok {
		tr.mu.Unlock()
		return fmt.Errorf("test not found: %s", id)
	}
	status := test.Status
	finalizing := test.remoteFinalizing
	oldDuration := test.Duration
	oldDurationStr := test.DurationStr
	oldAdvisory := cloneCapacityAdvisory(test.CapacityAdvisory)
	if finalizing {
		tr.mu.Unlock()
		return fmt.Errorf("test is finalizing and cannot adjust duration")
	}
	if status != "RUNNING" {
		tr.mu.Unlock()
		return fmt.Errorf("test is %s, must be RUNNING to adjust duration", status)
	}
	if err := tr.distributedActivationErrorLocked(test); err != nil {
		tr.mu.Unlock()
		return err
	}
	proposal := *test
	proposal.Groups = append([]TestGroup(nil), test.Groups...)
	proposal.Duration = newDuration
	proposal.DurationStr = newDuration.String()
	tr.refreshCapacityAdvisoryLocked(&proposal)
	projectQuota, quotaOK := evidence.projectQuota.quotaFor(&proposal)
	if !quotaOK {
		tr.mu.Unlock()
		return fmt.Errorf("project quota evidence changed while duration admission was evaluated; retry")
	}
	if err := enforceTestQuotaWithProjectQuota(projectQuota, managedTestCapacityVUsers(&proposal), proposal.Duration, proposal.Status); err != nil {
		tr.mu.Unlock()
		return err
	}
	if err := tr.launchTimeslotRevalidationErrorWithEvidenceLocked(&proposal, now, evidence); err != nil {
		tr.mu.Unlock()
		return err
	}
	if test.CapacityAdvisory != nil {
		if err := buildStartGovernanceError(test.ID, proposal.CapacityAdvisory, StartTestOptions{}); err != nil {
			tr.mu.Unlock()
			return err
		}
	}
	if err := tr.runtimeAdjustmentWindowErrorWithEvidenceLocked(&proposal, now, evidence); err != nil {
		tr.mu.Unlock()
		return err
	}
	test.Duration = newDuration
	test.DurationStr = newDuration.String()
	test.durationAdjustmentPrevious = oldDuration
	if oldAdvisory != nil {
		test.CapacityAdvisory = cloneCapacityAdvisory(proposal.CapacityAdvisory)
	} else {
		test.CapacityAdvisory = nil
	}
	if err := tr.saveManagedTestRecord(test); err != nil {
		test.Duration = oldDuration
		test.DurationStr = oldDurationStr
		test.CapacityAdvisory = oldAdvisory
		tr.mu.Unlock()
		return fmt.Errorf("persist duration reservation: %w", err)
	}
	multiEngine := test.multiEngine
	backend := tr.resolveBackend(test)
	projectID := normalizeProjectID(test.ProjectID)
	tr.mu.Unlock()
	if err := tr.applyDurationAdjustment(test, multiEngine, backend, newDuration); err != nil {
		var uncertain *distributedDurationUncertainError
		if errors.As(err, &uncertain) {
			tr.mu.Lock()
			test.durationAdjustmentPrevious = 0
			tr.mu.Unlock()
			reserveDuration := newDuration
			if oldDuration > reserveDuration {
				reserveDuration = oldDuration
			}
			return tr.failClosedUncertainDurationAdjustment(test, backend, reserveDuration, err)
		}
		tr.mu.Lock()
		rollbackErr := tr.rollbackDurationReservationLocked(test, newDuration, oldDuration, oldDurationStr, oldAdvisory)
		tr.mu.Unlock()
		if rollbackErr != nil {
			controlErr := errors.Join(err, fmt.Errorf("rollback duration reservation: %w", rollbackErr))
			reserveDuration := max(oldDuration, newDuration)
			return tr.failClosedUncertainDurationAdjustment(test, backend, reserveDuration, controlErr)
		}
		return err
	}

	// Remote finalization can begin independently of the per-test lifecycle
	// mutex while the backend call is in flight. Re-check ownership and state
	// after the ACK; otherwise a late ACK can report success and retain the new
	// durable duration after STOPPING/finalization has already won.
	tr.mu.Lock()
	lostLeadership := !tr.isExecutionLeader()
	currentStatus := test.Status
	finalizing = test.remoteFinalizing
	if !lostLeadership && currentStatus == "RUNNING" && !finalizing {
		test.durationAdjustmentPrevious = 0
		tr.mu.Unlock()
	} else {
		tr.mu.Unlock()
		var stateErr error
		switch {
		case lostLeadership:
			stateErr = errNotExecutionLeader
		case finalizing:
			stateErr = fmt.Errorf("test began finalizing while duration adjustment was being applied")
		default:
			stateErr = fmt.Errorf("test changed from RUNNING to %s while duration adjustment was being applied", currentStatus)
		}

		if rollbackErr := tr.applyDurationAdjustment(test, multiEngine, backend, oldDuration); rollbackErr != nil {
			tr.mu.Lock()
			test.durationAdjustmentPrevious = 0
			tr.mu.Unlock()
			controlErr := errors.Join(stateErr, fmt.Errorf("restore backend duration: %w", rollbackErr))
			if lostLeadership || finalizing || currentStatus != "RUNNING" {
				// Leadership loss and an active finalizer/stop already provide the
				// fail-closed owner. Keep the larger durable reservation and let that
				// owner complete teardown rather than starting an overlapping stop.
				return controlErr
			}
			return tr.failClosedUncertainDurationAdjustment(test, backend, max(oldDuration, newDuration), controlErr)
		}
		if lostLeadership {
			tr.mu.Lock()
			test.durationAdjustmentPrevious = 0
			tr.mu.Unlock()
			// The durable new duration is a conservative reservation. A former
			// leader must not rewrite it after its remote compensation succeeds.
			return stateErr
		}
		tr.mu.Lock()
		rollbackErr := tr.rollbackDurationReservationLocked(test, newDuration, oldDuration, oldDurationStr, oldAdvisory)
		tr.mu.Unlock()
		if rollbackErr != nil {
			return errors.Join(stateErr, fmt.Errorf("rollback duration reservation: %w", rollbackErr))
		}
		return stateErr
	}

	tr.hub.BroadcastJSON(map[string]any{
		"type":         "duration_changed",
		"test_id":      id,
		"project_id":   projectID,
		"new_duration": newDuration.String(),
		"timestamp":    time.Now(),
	})

	return nil
}

func (tr *TestRunner) failClosedUncertainDurationAdjustment(test *ManagedTest, backend testExecutionBackend, reserveDuration time.Duration, controlErr error) error {
	tr.mu.Lock()
	if tr.tests[test.ID] != test || test.Status != "RUNNING" {
		current := test.Status
		tr.mu.Unlock()
		return errors.Join(controlErr, fmt.Errorf("cannot persist uncertain duration adjustment; test is now %s", current))
	}
	test.Duration = reserveDuration
	test.DurationStr = reserveDuration.String()
	tr.refreshCapacityAdvisoryLocked(test)
	test.Status = "STOPPING"
	test.Error = "remote duration state is uncertain: " + controlErr.Error()
	persistErr := retryDurableTransition(func() error { return tr.saveManagedTestRecord(test) })
	tr.cancelAllVUserScaleRampsLocked(test)
	cancel := test.cancel
	eventSnapshot := cloneManagedTestFieldsLocked(test)
	tr.mu.Unlock()
	tr.broadcastEvent("test_stopping", eventSnapshot)

	var stopErr error
	if stoppable, ok := backend.(contextStoppableExecutionBackend); ok {
		stopErr = stoppable.StopTestContext(context.Background(), tr, test)
	} else if stoppable, ok := backend.(stoppableExecutionBackend); ok {
		stopErr = stoppable.StopTest(tr, test)
	} else {
		stopErr = fmt.Errorf("test backend does not support fail-closed stop")
	}
	if cancel != nil {
		cancel()
	}
	if stopErr != nil {
		tr.enqueueStopTeardownRetry(test.ID, false)
	}
	var persistWrapped error
	if persistErr != nil {
		persistWrapped = fmt.Errorf("persist fail-closed duration state: %w", persistErr)
	}
	var stopWrapped error
	if stopErr != nil {
		stopWrapped = fmt.Errorf("stop after uncertain duration adjustment: %w", stopErr)
	}
	return errors.Join(controlErr, persistWrapped, stopWrapped)
}

func (tr *TestRunner) PauseTest(id string) error {
	if !tr.isExecutionLeader() {
		return errNotExecutionLeader
	}
	tr.mu.RLock()
	id = tr.resolveRefKeyLocked(id)
	tr.mu.RUnlock()
	unlockControl, err := tr.lockTestLifecycleControl(context.Background(), id)
	if err != nil {
		return err
	}
	defer unlockControl()
	unlockTopology := tr.lockDistributedTopologyReadControl(id)
	defer unlockTopology()
	if !tr.isExecutionLeader() {
		return errNotExecutionLeader
	}

	tr.mu.RLock()
	test, ok := tr.tests[id]
	if !ok {
		tr.mu.RUnlock()
		return fmt.Errorf("test not found: %s", id)
	}
	status := test.Status
	finalizing := test.remoteFinalizing
	multiEngine := test.multiEngine
	backend := tr.resolveBackend(test)
	activationErr := tr.distributedActivationErrorLocked(test)
	tr.mu.RUnlock()
	if finalizing {
		return fmt.Errorf("test is finalizing and cannot be paused")
	}
	if !testexec.AllowsLifecycleControl(status, testexec.ControlPause) {
		return fmt.Errorf("test is %s, must be RUNNING to pause", status)
	}
	if activationErr != nil {
		return activationErr
	}
	if err := tr.applyPause(test, multiEngine, backend); err != nil {
		var uncertain *distributedPauseUncertainError
		if errors.As(err, &uncertain) || errors.Is(err, engine.ErrPauseQuiescenceUnproven) {
			return tr.failClosedUncertainPauseControl(test, "RUNNING", backend, err)
		}
		return err
	}

	pauseStartedAt := time.Now().UTC()
	if multiEngine != nil {
		started, confirmed := multiEngine.PauseStartedAt()
		if !confirmed {
			err := &engine.PauseQuiescenceUnprovenError{Cause: fmt.Errorf("local pause changed before controller confirmation")}
			return tr.failClosedUncertainPauseControl(test, "RUNNING", backend, err)
		}
		pauseStartedAt = started.UTC()
	}
	tr.mu.Lock()
	if !tr.isExecutionLeader() {
		tr.mu.Unlock()
		if rollbackErr := tr.applyResume(test, multiEngine, backend); rollbackErr != nil {
			controlErr := errors.Join(errNotExecutionLeader, fmt.Errorf("resume backend after leadership loss: %w", rollbackErr))
			return tr.failClosedUncertainPauseControl(test, "RUNNING", backend, controlErr)
		}
		return errNotExecutionLeader
	}
	if test.Status != "RUNNING" || test.remoteFinalizing {
		currentStatus := test.Status
		finalizing := test.remoteFinalizing
		tr.mu.Unlock()
		if rollbackErr := tr.applyResume(test, multiEngine, backend); rollbackErr != nil {
			controlErr := errors.Join(fmt.Errorf("test changed while pause was being applied"), fmt.Errorf("resume backend: %w", rollbackErr))
			if currentStatus == "RUNNING" || currentStatus == "PAUSED" {
				return tr.failClosedUncertainPauseControl(test, currentStatus, backend, controlErr)
			}
			return controlErr
		}
		if finalizing {
			return fmt.Errorf("test began finalizing while pause was being applied")
		}
		return fmt.Errorf("test changed from RUNNING to %s while pause was being applied", currentStatus)
	}
	pausedAt := pauseStartedAt
	previousEarlyStopBreachStartedAt := cloneTimePtr(test.earlyStopBreachStartedAt)
	test.Status = "PAUSED"
	test.PausedAt = &pausedAt
	test.earlyStopBreachStartedAt = nil
	if err := tr.saveManagedTestRecord(test); err != nil {
		test.Status = "RUNNING"
		test.PausedAt = nil
		test.earlyStopBreachStartedAt = previousEarlyStopBreachStartedAt
		tr.mu.Unlock()
		if rollbackErr := tr.applyResume(test, multiEngine, backend); rollbackErr != nil {
			controlErr := errors.Join(fmt.Errorf("persist paused test state: %w", err), fmt.Errorf("resume backend after persistence failure: %w", rollbackErr))
			return tr.failClosedUncertainPauseControl(test, "RUNNING", backend, controlErr)
		}
		return fmt.Errorf("persist paused test state: %w", err)
	}
	if test.LiveMetrics != nil {
		updated := cloneLiveMetrics(test.LiveMetrics)
		updated.ActiveVUsers = 0
		for _, group := range updated.GroupMetrics {
			if group != nil {
				group.VUsers = 0
			}
		}
		replaceLiveMetricsLocked(test, updated)
	}
	eventSnapshot := cloneManagedTestFieldsLocked(test)
	tr.mu.Unlock()

	tr.broadcastEvent("test_paused", eventSnapshot)

	return nil
}

func (tr *TestRunner) ResumeTest(id string) error {
	if !tr.isExecutionLeader() {
		return errNotExecutionLeader
	}
	tr.mu.RLock()
	id = tr.resolveRefKeyLocked(id)
	tr.mu.RUnlock()
	unlockControl, err := tr.lockTestLifecycleControl(context.Background(), id)
	if err != nil {
		return err
	}
	defer unlockControl()
	unlockTopology := tr.lockDistributedTopologyReadControl(id)
	defer unlockTopology()
	if !tr.isExecutionLeader() {
		return errNotExecutionLeader
	}
	now := time.Now().UTC()
	evidence, err := tr.loadRuntimeLaunchEvidence(id, now, func(test *ManagedTest) {
		pausedAt := now
		if test.PausedAt != nil && !test.PausedAt.IsZero() {
			pausedAt = test.PausedAt.UTC()
		}
		if test.StartedAt != nil && pausedAt.Before(test.StartedAt.UTC()) {
			// A distributed run can be paused after CommitWork acknowledges but
			// before its coordinated future barrier. Only post-barrier pause time
			// extends the active duration; the scheduled lead was never active.
			pausedAt = test.StartedAt.UTC()
		}
		pauseDelta := now.Sub(pausedAt)
		if pauseDelta > 0 {
			test.CumulativePause += pauseDelta
		}
		test.Status = "RUNNING"
		test.PausedAt = nil
	})
	if err != nil {
		return err
	}

	tr.mu.Lock()
	test, ok := tr.tests[id]
	if !ok {
		tr.mu.Unlock()
		return fmt.Errorf("test not found: %s", id)
	}
	status := test.Status
	finalizing := test.remoteFinalizing
	if finalizing {
		tr.mu.Unlock()
		return fmt.Errorf("test is finalizing and cannot be resumed")
	}
	if !testexec.AllowsLifecycleControl(status, testexec.ControlResume) {
		tr.mu.Unlock()
		return fmt.Errorf("test is %s, must be PAUSED to resume", status)
	}
	if err := tr.distributedActivationErrorLocked(test); err != nil {
		tr.mu.Unlock()
		return err
	}
	pausedAt := now
	if test.PausedAt != nil && !test.PausedAt.IsZero() {
		pausedAt = test.PausedAt.UTC()
	}
	if test.StartedAt != nil && pausedAt.Before(test.StartedAt.UTC()) {
		pausedAt = test.StartedAt.UTC()
	}
	pauseDelta := now.Sub(pausedAt)
	if pauseDelta < 0 {
		pauseDelta = 0
	}
	proposal := *test
	proposal.Groups = append([]TestGroup(nil), test.Groups...)
	proposal.Status = "RUNNING"
	proposal.CumulativePause += pauseDelta
	proposal.PausedAt = nil
	tr.refreshCapacityAdvisoryLocked(&proposal)
	if err := tr.launchTimeslotRevalidationErrorWithEvidenceLocked(&proposal, now, evidence); err != nil {
		tr.mu.Unlock()
		return err
	}
	if test.CapacityAdvisory != nil {
		if err := buildStartGovernanceError(test.ID, proposal.CapacityAdvisory, StartTestOptions{}); err != nil {
			tr.mu.Unlock()
			return err
		}
	}
	if blocked, reason := tr.evaluateQuotaBlockWithLaunchEvidenceLocked(&proposal, test.ID, now, evidence); blocked {
		tr.mu.Unlock()
		return fmt.Errorf("resume admission blocked: %s", reason)
	}
	if err := tr.runtimeAdjustmentWindowErrorWithEvidenceLocked(&proposal, now, evidence); err != nil {
		tr.mu.Unlock()
		return err
	}
	multiEngine := test.multiEngine
	backend := tr.resolveBackend(test)
	tr.mu.Unlock()
	if err := tr.applyResume(test, multiEngine, backend); err != nil {
		var uncertain *distributedPauseUncertainError
		if errors.As(err, &uncertain) {
			return tr.failClosedUncertainPauseControl(test, "PAUSED", backend, err)
		}
		return err
	}

	tr.mu.Lock()
	if !tr.isExecutionLeader() {
		tr.mu.Unlock()
		if rollbackErr := tr.applyPause(test, multiEngine, backend); rollbackErr != nil {
			controlErr := errors.Join(errNotExecutionLeader, fmt.Errorf("pause backend after leadership loss: %w", rollbackErr))
			return tr.failClosedUncertainPauseControl(test, "PAUSED", backend, controlErr)
		}
		return errNotExecutionLeader
	}
	if test.Status != "PAUSED" || test.remoteFinalizing {
		currentStatus := test.Status
		finalizing := test.remoteFinalizing
		tr.mu.Unlock()
		if rollbackErr := tr.applyPause(test, multiEngine, backend); rollbackErr != nil {
			controlErr := errors.Join(fmt.Errorf("test changed while resume was being applied"), fmt.Errorf("pause backend: %w", rollbackErr))
			if currentStatus == "RUNNING" || currentStatus == "PAUSED" {
				return tr.failClosedUncertainPauseControl(test, currentStatus, backend, controlErr)
			}
			return controlErr
		}
		if finalizing {
			return fmt.Errorf("test began finalizing while resume was being applied")
		}
		return fmt.Errorf("test changed from PAUSED to %s while resume was being applied", currentStatus)
	}
	test.Status = "RUNNING"
	previousPausedAt := cloneTimePtr(test.PausedAt)
	previousCumulativePause := test.CumulativePause
	previousGroups := cloneArchiveTestGroups(test.Groups)
	test.PausedAt = nil
	test.CumulativePause = proposal.CumulativePause
	refreshRuntimeGroupEffectiveEndsLocked(test, calendarTestStart(test))
	if err := tr.saveManagedTestRecord(test); err != nil {
		test.Status = "PAUSED"
		test.PausedAt = previousPausedAt
		test.CumulativePause = previousCumulativePause
		test.Groups = previousGroups
		tr.mu.Unlock()
		if rollbackErr := tr.applyPause(test, multiEngine, backend); rollbackErr != nil {
			controlErr := errors.Join(fmt.Errorf("persist resumed test state: %w", err), fmt.Errorf("pause backend after persistence failure: %w", rollbackErr))
			return tr.failClosedUncertainPauseControl(test, "PAUSED", backend, controlErr)
		}
		return fmt.Errorf("persist resumed test state: %w", err)
	}
	// Goal-control timestamps are wall-clock instants. Shift them by the
	// completed pause so breach windows and scale cooldowns advance only on the
	// run's active clock; otherwise the first post-resume sample can stop or
	// scale immediately because of time spent paused.
	shiftGoalControllerForPause(test.goalControl, pauseDelta)
	// Pause publishes an immediate zero-VUser snapshot. Do not replace that
	// observed lifecycle gauge with configured/desired group targets on resume:
	// during a startup ramp or asynchronous scale-down the targets can differ from
	// the VUsers that have actually activated. The next engine/generator sample is
	// the authoritative post-resume gauge. Goal-clock metadata can still advance
	// independently without changing the active-VUser values.
	if test.LiveMetrics != nil && test.LiveMetrics.GoalStatus != nil && test.goalControl != nil {
		updated := cloneLiveMetrics(test.LiveMetrics)
		updated.GoalStatus.LastScaleAt = cloneTimePtr(test.goalControl.lastScaleAt)
		replaceLiveMetricsLocked(test, updated)
	}
	eventSnapshot := cloneManagedTestFieldsLocked(test)
	tr.mu.Unlock()

	tr.broadcastEvent("test_resumed", eventSnapshot)

	return nil
}

// failClosedUncertainPauseControl records that generator state could not be
// reconciled, then drives the run toward a stop. Advertising RUNNING/PAUSED
// after only a subset of generators applied the opposite state would make both
// accounting and operator controls unsafe. STOPPING is durable and is also
// recovered as a failed run if the controller restarts before remote teardown.
func (tr *TestRunner) failClosedUncertainPauseControl(test *ManagedTest, expectedStatus string, backend testExecutionBackend, controlErr error) error {
	tr.mu.Lock()
	if tr.tests[test.ID] != test || test.Status != expectedStatus {
		current := test.Status
		tr.mu.Unlock()
		return errors.Join(controlErr, fmt.Errorf("cannot persist uncertain remote control from %s; test is now %s", expectedStatus, current))
	}
	test.Status = "STOPPING"
	test.Error = "pause/resume state is uncertain: " + controlErr.Error()
	persistErr := retryDurableTransition(func() error { return tr.saveManagedTestRecord(test) })
	tr.cancelAllVUserScaleRampsLocked(test)
	cancel := test.cancel
	multiEngine := test.multiEngine
	eventSnapshot := cloneManagedTestFieldsLocked(test)
	tr.mu.Unlock()
	tr.broadcastEvent("test_stopping", eventSnapshot)

	var stopErr error
	if multiEngine != nil {
		multiEngine.Stop()
	} else if stoppable, ok := backend.(contextStoppableExecutionBackend); ok {
		stopErr = stoppable.StopTestContext(context.Background(), tr, test)
	} else if stoppable, ok := backend.(stoppableExecutionBackend); ok {
		stopErr = stoppable.StopTest(tr, test)
	} else {
		stopErr = fmt.Errorf("test backend does not support fail-closed stop")
	}
	if cancel != nil {
		cancel()
	}
	if stopErr != nil {
		tr.enqueueStopTeardownRetry(test.ID, false)
	}
	var persistWrapped error
	if persistErr != nil {
		persistWrapped = fmt.Errorf("persist fail-closed remote control state: %w", persistErr)
	}
	var stopWrapped error
	if stopErr != nil {
		stopWrapped = fmt.Errorf("stop after uncertain remote control: %w", stopErr)
	}
	return errors.Join(controlErr, persistWrapped, stopWrapped)
}

func (tr *TestRunner) runTest(test *ManagedTest) {
	// The run driver owns its own goroutine, so an unrecovered panic anywhere in
	// the local execution path (engine build, archive writer, metrics broadcast,
	// backend dispatch) terminates the controller process and takes every other
	// in-flight run on this replica with it. Contain the blast radius to the run
	// that raised it and terminalize that run as FAILED.
	defer func() {
		if recovered := recover(); recovered != nil {
			tr.recoverRunTestPanic(test, recovered, debug.Stack())
		}
	}()
	// Defense in depth: a non-leader must never drive a generator fleet.
	if !tr.isExecutionLeader() {
		return
	}
	tr.mu.RLock()
	current := tr.tests[test.ID]
	startable := !tr.shuttingDown && current == test && test.Status == "INITIALIZING" && test.executionCtx != nil && test.executionCtx.Err() == nil
	ctx := test.executionCtx
	spanAttributes := testLifecycleSpanAttributes(test)
	tr.mu.RUnlock()
	if !startable || !tr.isExecutionLeader() {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	backend := tr.resolveBackend(test)
	if backend == nil {
		backend = localExecutionBackend{}
	}
	spanName, spanScope := testExecutionSpanDescriptor(backend)
	spanAttributes = append(spanAttributes, attribute.String("apexload.test.operation_scope", spanScope))
	ctx, span := otel.Tracer("github.com/jasonczech/apexload/pkg/api").Start(
		ctx,
		spanName,
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(spanAttributes...),
	)
	// Defers unwind LIFO, so this one runs BEFORE the recover above. Two things
	// follow, and both are load-bearing:
	//   1. It must not take tr.mu while a panic is in flight. The panic may have
	//      unwound out of a tr.mu critical section, and annotateTestLifecycleSpan's
	//      RLock would then block forever - stranding the panic here, before the
	//      backstop below ever runs.
	//   2. The run is not FAILED yet at this point (the backstop terminalizes it
	//      afterwards), so reading the live status would export a green span for a
	//      run that crashed. Record the crash outcome directly instead.
	dispatched := false
	defer func() {
		if !dispatched {
			span.SetAttributes(attribute.String("apexload.test.outcome", "panicked"))
			span.SetStatus(codes.Error, "test execution driver panicked")
			span.End()
			return
		}
		tr.annotateTestLifecycleSpan(span, test.ID, nil)
		span.End()
	}()
	tr.mu.Lock()
	if tr.tests[test.ID] == test && test.executionCtx != nil {
		test.executionCtx = ctx
	}
	tr.mu.Unlock()
	backend.Run(tr, test)
	dispatched = true
}

// runTestPanicRecoveryLockTimeout bounds each stage of the run-driver panic
// backstop: first how long it waits for tr.mu to become acquirable, then how
// long the terminal transition itself may take. A panic that unwound out of a
// tr.mu critical section orphans that mutex, and a panic that unwound out of an
// archive-commit fence orphans that gate; no amount of waiting recovers either,
// and blocking forever would strand the run, its capacity reservation, and every
// later taker of the same fence. Re-panicking (so the supervisor restarts the
// process) is strictly better than a controller silently wedged behind a lock
// nothing will ever release.
//
// The bound is elapsed time, which on its own cannot tell an orphaned gate from
// a database that is merely slow: the terminal path's durable writes take no
// context and so have no deadline of their own, and a managed-store failover can
// stall one for far longer than this timeout. Restarting for that case would be
// wrong - the store is still unreachable after the restart, so the process would
// only crash-loop. Both stages therefore run through awaitPanicRecoveryStage,
// which extends the deadline while a durable write is genuinely in flight and
// re-panics when the wait expires with nothing progressing. The acquirability
// stage needs that just as much as the terminal one: the lifecycle paths here
// hold tr.mu across their durable writes, so a stalled store leaves the mutex
// held by a healthy run rather than orphaned by a panic.
var runTestPanicRecoveryLockTimeout = 30 * time.Second

// runTestPanicRecoveryDurableWriteExtensions caps how many times a durable
// write in flight may extend a backstop wait, so the fail-fast bound survives
// the extension: each stage waits at most
// (1+runTestPanicRecoveryDurableWriteExtensions) * runTestPanicRecoveryLockTimeout.
// The cap matters because the in-flight gauge is process-wide - on a busy
// controller some unrelated write can be in flight at almost every expiry, and
// without a cap that could postpone an orphaned gate's re-panic indefinitely.
// The default spans several minutes, which covers a managed-database failover
// while still guaranteeing the run is never stranded forever.
//
// A budget this long is only real if the process survives it. Kubernetes
// liveness probes hit /api/v1/system/health every 20s and kill the container
// after three failures, so a health handler that queued behind the very mutex
// this wait is tolerating would have the pod restarted at ~43s no matter how
// long the extension ran. handleHealthCheck therefore answers from a cached
// snapshot while a durable write is in flight instead of blocking on tr.mu. That
// bypass is bounded by the same budget as this wait
// (healthCheckDurableWriteBypassBudget derives (1+extensions)*timeout from these
// two variables), so both sides stop tolerating a stalled store at the same
// point and a write that never returns still gets the replica restarted.
var runTestPanicRecoveryDurableWriteExtensions = 8

// recoverRunTestPanic terminalizes the run whose driver goroutine panicked.
//
// failTestBeforeRun owns the pre-RUNNING window and no-ops for any other status;
// completeTestRun owns a run that already reached RUNNING and no-ops once some
// other path committed a terminal status. Invoking both in that order covers
// every state the panic could have been raised from without either one
// resurrecting a terminal decision that already won.
//
// Terminalization is best effort, not a guarantee. A panic that orphaned a lock
// the terminal path needs makes it unreachable; that case re-panics so the
// supervisor restarts the process and startup reconciliation finishes the run.
//
// A secondary panic raised while terminalizing is caught inside the helper
// goroutine - a bare goroutine has no caller to recover it, and a crash from
// there is unattributable - but catching it is not the end of the handling. That
// panic most likely unwound out of failTestBeforeRun's or completeTestRun's
// tr.mu critical section, which no recover can unlock, so returning normally
// would leave the process alive with tr.mu write-locked forever: every status
// transition, every listing, and the /api/v1/system/health probe would block
// permanently. The helper therefore records the secondary panic and the driver
// re-panics on it, which is the same fail-fast the orphaned-lock case takes.
// Startup reconciliation finishes the run after the restart; there is no
// in-process reconciliation that could do it while tr.mu is orphaned.
func (tr *TestRunner) recoverRunTestPanic(test *ManagedTest, recovered any, stack []byte) {
	testID := ""
	if test != nil {
		testID = test.ID
	}
	slog.Error("test execution driver panicked; failing the run instead of the controller",
		"test_id", testID, "panic", fmt.Sprintf("%v", recovered), "stack", string(stack))
	if tr == nil || test == nil {
		return
	}
	message := fmt.Sprintf("test execution panicked: %v", recovered)

	// The panic may have unwound out of a tr.mu critical section. Every terminal
	// transition below needs that mutex, so prove from another goroutine that it
	// is still acquirable before committing to them.
	acquirable := make(chan struct{})
	terminalized := make(chan struct{})
	var secondaryPanicked atomic.Bool
	go func() {
		defer close(terminalized)
		// Terminalization walks the same shared state the primary panic may have
		// corrupted and fans out to broadcast, promotion, and execution-plan
		// advancement. A secondary panic here has no caller to recover it, so
		// without this it would escape a bare goroutine and kill the controller
		// with no attribution to the run that caused it.
		//
		// Containment alone is not enough. Defers unwind LIFO, so this one runs
		// before close(terminalized) above: record the failure first, so the
		// driver sees it as soon as the wait below completes and can re-panic.
		// Swallowing it outright would leave the process alive holding whichever
		// lock the secondary panic unwound out of - a silent, permanent wedge that
		// is worse than the crash this backstop replaced.
		defer func() {
			if secondary := recover(); secondary != nil {
				secondaryPanicked.Store(true)
				slog.Error("terminalizing a panicked run panicked; re-panicking so the controller restarts",
					"test_id", testID, "panic", fmt.Sprintf("%v", secondary), "stack", string(debug.Stack()))
			}
		}()
		tr.mu.Lock()
		tr.mu.Unlock()
		close(acquirable)
		tr.terminalizePanickedRun(test, message)
	}()
	// Waiting for tr.mu is bounded for the orphaned-mutex case, but the wait must
	// not mistake a held mutex for an orphaned one. The lifecycle paths in this
	// package issue their durable writes with tr.mu write-locked - terminal
	// transitions through persistStoredTestStatus, a pre-start abort and a
	// fail-closed VUser adjustment through saveManagedTestRecord, and every ramp
	// step through persistManagedTestVUserScale - so during a store failover an
	// unrelated healthy run pins the mutex for the whole stall. Re-panicking
	// there would kill the replica - and every other run on it - only to restart
	// into the same unreachable store, so this stage honours the in-flight gauge
	// exactly as the terminal stage does. Every one of those writes is counted
	// (trackDurableWrite); a gauge that saw only the status write classified the
	// others as orphaned mutexes.
	//
	// The holder does not have to be a run at all. Compatibility authoring paths,
	// deletes, imports, moves, and archive updates can still hold tr.mu across a
	// different store. Those are counted too (beginLockHeldDurableWrite). Ordinary
	// script/scenario saves and their reference allocation deliberately run
	// off-lock and therefore do not participate in this wait.
	if !tr.awaitPanicRecoveryStage(testID, "runner lock acquisition", acquirable) {
		slog.Error("runner lock was not acquirable after a run-driver panic; re-panicking so the controller restarts",
			"test_id", testID)
		panic(recovered)
	}
	// Do not let the driver goroutine exit before the run is terminal: an
	// abandoned INITIALIZING/RUNNING row would otherwise stay live forever.
	// tr.mu being acquirable does not prove the same of every other lock the
	// terminal path takes (the per-test archive-commit fence above all), so
	// this wait is bounded too: parking here forever would strand the run, its
	// capacity reservation, and every later taker of that fence.
	if !tr.awaitPanicRecoveryStage(testID, "terminalization", terminalized) {
		slog.Error("terminalizing a panicked run did not finish in time; re-panicking so the controller restarts",
			"test_id", testID)
		panic(recovered)
	}
	if secondaryPanicked.Load() {
		// The terminal transition died part-way through. It most likely left
		// tr.mu locked, and nothing in this process can unlock it, so fail fast
		// and let startup reconciliation finish the run.
		panic(recovered)
	}
}

// awaitPanicRecoveryStage waits for one stage of the run-driver panic backstop
// to signal completion and reports whether it did.
//
// Each stage is bounded so an orphaned lock or gate cannot strand the run
// forever, but a flat deadline would also fire on a database that is only slow:
// the terminal path's durable writes take no context, so a store failover can
// hold one open for minutes - both while the writer holds tr.mu, which stalls
// the acquirability stage, and while the backstop's own transition runs, which
// stalls the terminalization stage. Re-panicking for either would restart the
// replica into the same unreachable store and crash-loop. So the deadline is
// extended when it expires while a durable write is still inside the storage
// driver - blocked I/O that can still complete - and a wait that expires with
// nothing in flight is treated as an unrecoverable lock or gate.
//
// The extension is capped (see runTestPanicRecoveryDurableWriteExtensions), so
// this is a longer bound, not an unbounded wait: a store that never returns
// still ends in a re-panic rather than a stranded run. The in-flight gauge is
// also process-wide rather than scoped to this run, so an unrelated write racing
// the deadline can buy an extra interval; under a healthy store it clears in
// milliseconds and the next expiry re-panics.
func (tr *TestRunner) awaitPanicRecoveryStage(testID string, stage string, done <-chan struct{}) bool {
	deadline := time.After(runTestPanicRecoveryLockTimeout)
	writeWasInFlight := tr.durableWritesInFlight.Load() > 0
	for extensions := 0; ; extensions++ {
		select {
		case <-done:
			return true
		case <-deadline:
			// Completion and the timer can both be ready. Prefer completed work
			// before treating an expired timer as an orphaned gate.
			select {
			case <-done:
				return true
			default:
			}
			writeInFlight := tr.durableWritesInFlight.Load() > 0
			if (!writeInFlight && !writeWasInFlight) || extensions >= runTestPanicRecoveryDurableWriteExtensions {
				return false
			}
			// A write can return just before this check, while its caller still
			// needs to unlock/publish completion. Allow one settling interval
			// after an observed write, within the same total extension budget.
			writeWasInFlight = writeInFlight
			slog.Warn("recovering a panicked run is still waiting on a durable write; extending the wait instead of restarting the controller",
				"test_id", testID, "stage", stage)
			deadline = time.After(runTestPanicRecoveryLockTimeout)
		}
	}
}

func (tr *TestRunner) terminalizePanickedRun(test *ManagedTest, message string) {
	tr.mu.Lock()
	current := tr.tests[test.ID] == test
	cancel := test.cancel
	tr.mu.Unlock()
	if !current {
		// A reload, delete, or replacement already displaced this handle. It no
		// longer owns any durable row, so it must not publish terminal state.
		return
	}
	if cancel != nil {
		// The driver died mid-flight; stop whatever engine/VUser goroutines it
		// left behind before recording the terminal status.
		cancel()
	}
	if err := tr.failTestBeforeRun(test, message); err != nil {
		slog.Error("could not fail a panicked run at its pre-run boundary", "test_id", test.ID, "error", err)
	}
	if err := tr.completeTestRun(test, "FAILED", time.Now(), message, nil); err != nil {
		slog.Error("could not terminalize a panicked run", "test_id", test.ID, "error", err)
	}
}

func testExecutionSpanDescriptor(backend testExecutionBackend) (name, scope string) {
	if _, distributed := backend.(*distributedExecutionBackend); distributed {
		return "apexload.test.launch", "distributed_launch"
	}
	return "apexload.test.execute", "complete_execution"
}

func testLifecycleSpanAttributes(test *ManagedTest) []attribute.KeyValue {
	if test == nil {
		return nil
	}
	return []attribute.KeyValue{
		attribute.String("apexload.test.id", telemetry.BoundedIdentifier(test.ID)),
		attribute.String("apexload.project.id", telemetry.BoundedIdentifier(test.ProjectID)),
		attribute.String("apexload.scenario.id", telemetry.BoundedIdentifier(test.ScenarioID)),
		attribute.Int("apexload.test.vusers", test.TotalVUsers),
	}
}

// annotateTestLifecycleSpan records stable outcome metadata only. Raw errors are
// deliberately excluded because backend/secret-provider errors can contain
// credentials or customer payloads.
func (tr *TestRunner) annotateTestLifecycleSpan(span trace.Span, id string, operationErr error) {
	if span == nil {
		return
	}
	tr.mu.RLock()
	resolved := tr.resolveRefKeyLocked(id)
	test := tr.tests[resolved]
	testStatus := ""
	if test != nil {
		testStatus = test.Status
		span.SetAttributes(testLifecycleSpanAttributes(test)...)
		span.SetAttributes(attribute.String("apexload.test.outcome", telemetry.BoundedIdentifier(strings.ToLower(testStatus))))
	}
	tr.mu.RUnlock()
	if operationErr != nil {
		span.SetAttributes(attribute.String("error.type", telemetry.BoundedIdentifier(fmt.Sprintf("%T", operationErr))))
		span.SetStatus(codes.Error, "test lifecycle operation failed")
		return
	}
	if testStatus == "FAILED" {
		span.SetStatus(codes.Error, "test execution failed")
		return
	}
	span.SetStatus(codes.Ok, "")
}

func (tr *TestRunner) runScenarioScheduledTestWithTerm(test *ManagedTest, leaseTerm executionLeaseTerm) {
	if !tr.executionLeaseTermCurrent(leaseTerm) {
		return
	}
	// The scheduler releases its preparation gate only after both correlation
	// commits. Reacquire that same gate at the dispatch boundary so a Stop that
	// was already waiting wins before any backend call; without this handoff,
	// runner-mutex scheduling could let dispatch overtake the queued lifecycle
	// operation after the gate was released.
	unlockLifecycle, err := tr.lockTestLifecycleControl(context.Background(), test.ID)
	if err != nil {
		return
	}
	tr.mu.RLock()
	current := tr.tests[test.ID]
	startable := current == test && test.executionGeneration == leaseTerm.generation &&
		test.executionCtx != nil && test.executionCtx.Err() == nil
	tr.mu.RUnlock()
	if !startable || !tr.executionLeaseTermCurrent(leaseTerm) {
		unlockLifecycle()
		return
	}
	// Owning the gate through this revalidation establishes dispatch as the
	// lifecycle winner. Release before Run because execution backends are
	// intentionally long-lived and Stop must remain able to control them.
	unlockLifecycle()
	tr.runTest(test)
}

func (tr *TestRunner) buildRunEngine(test *ManagedTest, resultSink engine.ResultSink) (*engine.MultiGroupEngine, error) {
	// Everything this function reads off the test is guarded by tr.mu. A stop,
	// or a controller shutdown running stopActiveTests, calls
	// compactTerminalManagedTestLocked, which nils groupSpecs and every group's
	// script, data sources, redaction rules, and runtime secrets.
	//
	// runTest releases tr.mu before calling backend.Run, so reading these
	// unlocked raced with that trim. Losing the race meant building an engine
	// from a half-released runtime — no groups, or secrets resolved from a map
	// being nilled underneath — during shutdown, which is the worst possible
	// time to diagnose it.
	//
	// validateManagedTestWorkloadBounds is inside the critical section too: it
	// ranges over test.Groups by value, so it copies the very fields the trim
	// clears. It takes no locks itself, so calling it here cannot deadlock.
	tr.mu.Lock()
	boundsErr := validateManagedTestWorkloadBounds(test)
	for _, g := range test.Groups {
		if javaHostSettings(g) != nil {
			boundsErr = fmt.Errorf("java_host_requires_distributed_backend")
			break
		}
	}
	alreadyTerminal := isTerminalTestStatus(test.Status)
	status := test.Status
	specs := test.groupSpecs
	resourcePeaks, resourcePeakErr := runtimeReservationGroups(test, false)
	runtimeSecrets := make([]map[string]string, len(test.Groups))
	for index := range test.Groups {
		runtimeSecrets[index] = test.Groups[index].runtimeSecretValues
	}
	tr.mu.Unlock()

	if boundsErr != nil {
		return nil, boundsErr
	}
	if resourcePeakErr != nil {
		return nil, resourcePeakErr
	}
	if alreadyTerminal {
		// The runtime this engine needs has already been released, so there is
		// nothing left to build. Fail with a clear reason instead of silently
		// starting a run with no groups.
		return nil, fmt.Errorf("test reached terminal status %s before its engine could be built", status)
	}

	groups := testexec.CloneGroupSpecsWithResultSink(specs, resultSink)
	if err := validateEngineGroupBounds(groups); err != nil {
		return nil, err
	}
	correlation := tr.runCorrelationAssignment(test.ID)
	for index := range groups {
		if index < len(resourcePeaks) {
			groups[index].RuntimeResourcePeakVUsers = resourcePeaks[index].VUsers
		}
		groups[index].ScenarioID = strings.TrimSpace(test.ScenarioID)
		groups[index].RunID = test.ID
		groups[index].RunCorrelationOrigins = append([]string(nil), correlation.Origins...)
		groups[index].CorrelationCarriers = append([]string(nil), correlation.Carriers...)
		groups[index].CorrelationTestName = correlation.TestName
		projectID := test.ProjectID
		if groups[index].Script != nil && strings.TrimSpace(groups[index].Script.ProjectID) != "" {
			projectID = groups[index].Script.ProjectID
		}
		resolver := engine.CacheSecretResolverForRun(tr.secretResolverForRunGroup(test, index, projectID))
		if index < len(runtimeSecrets) {
			resolver = overlayRuntimeSecretResolver(runtimeSecrets[index], resolver)
		}
		groups[index].SecretResolver = resolver
	}
	preBaseline, postBaseline := testexec.ResourceBaselineWindows(test.ResourceBaseline)
	preBaseline += testexec.StartDelayWindow(test.StartDelay)
	return engine.NewMultiGroup(engine.MultiGroupConfig{
		TerminationMode: test.TerminationMode,
		JointProjection: runJointProjection(test),
		Groups:          groups,
		Duration:        test.Duration,
		PreBaseline:     preBaseline,
		PostBaseline:    postBaseline,
	})
}

func applyGoalControllerUpdate(tr *TestRunner, test *ManagedTest, live *LiveMetrics, groupVUsers map[string]int, update goalControllerUpdate, scaleVUsers func(groupName string, target int) error) {
	applyGoalControllerUpdateWithScaler(tr, test, live, groupVUsers, update, scaleVUsers, false)
}

// applyGoalControllerUpdateWithManagedScaler is used when a managed run needs
// stricter scale admission than the ordinary ID-based runner path. The caller's
// scaler remains responsible for the normal durable reservation and backend
// mutation; this function only ensures it is not replaced by scaleVUsersNow.
func applyGoalControllerUpdateWithManagedScaler(tr *TestRunner, test *ManagedTest, live *LiveMetrics, groupVUsers map[string]int, update goalControllerUpdate, scaleVUsers func(groupName string, target int) error) {
	applyGoalControllerUpdateWithScaler(tr, test, live, groupVUsers, update, scaleVUsers, true)
}

func applyGoalControllerUpdateWithScaler(tr *TestRunner, test *ManagedTest, live *LiveMetrics, groupVUsers map[string]int, update goalControllerUpdate, scaleVUsers func(groupName string, target int) error, preferProvidedScaler bool) {
	live.GoalStatus = update.Status
	if !update.ShouldScale {
		return
	}

	// Snapshot the group set + target statuses under the lock. The scaleVUsers
	// engine call must run unlocked, and the resulting test.Groups/goalControl
	// writes are re-taken under the lock because they share state with the API
	// read paths that copy *ManagedTest (GetTest/ListTests/capacity scans).
	tr.mu.RLock()
	groups := append([]TestGroup(nil), test.Groups...)
	targetStatuses := currentGoalTargetStatuses(test, live.Timestamp)
	tr.mu.RUnlock()

	targets := distributeTrafficProfileTargetVUsers(groups, groupVUsers, update.TargetTotal, targetStatuses, test.TrafficProfile)
	scaledAny := false
	appliedTargets := make(map[string]int)
	scaleFailureReason := ""
	for _, group := range groups {
		target, exists := targets[group.Name]
		if !exists || groupVUsers[group.Name] == target {
			continue
		}
		// Production goal scaling must pass through the same atomic admission and
		// durable accounting reservation as an operator request. Distributed goal
		// control supplies a stricter publication-fenced scaler; the ordinary
		// callback remains the fallback for isolated unmanaged test fixtures.
		tr.mu.RLock()
		managed := tr.tests != nil && tr.tests[test.ID] == test
		tr.mu.RUnlock()
		var err error
		if preferProvidedScaler && scaleVUsers != nil {
			err = scaleVUsers(group.Name, target)
		} else if managed {
			err = tr.scaleVUsersNow(test.ID, group.Name, target, false)
		} else if scaleVUsers != nil {
			err = scaleVUsers(group.Name, target)
		} else {
			err = fmt.Errorf("goal scaling backend is unavailable")
		}
		if err != nil {
			slog.Warn("goal-oriented scale failed", "test_id", test.ID, "group", group.Name, "target_vusers", target, "error", err)
			scaleFailureReason = fmt.Sprintf("scale failed: %v", err)
			break
		}
		groupVUsers[group.Name] = target
		appliedTargets[group.Name] = target
		scaledAny = true
	}

	now := live.Timestamp
	tr.mu.Lock()
	// Keep standalone/extension backends aligned only for groups this round
	// actually scaled. Writing every value from the pre-scale snapshot can undo
	// a concurrent operator scale on a group whose goal target was unchanged.
	for i := range test.Groups {
		if current, ok := appliedTargets[test.Groups[i].Name]; ok {
			test.Groups[i].VUsers = current
		}
	}
	for _, group := range test.Groups {
		groupVUsers[group.Name] = groupCurrentVUsers(group)
	}
	if test.goalControl == nil {
		// Defense-in-depth: the distributed path may reach a scale decision
		// before run-state init in pathological orderings; never nil-deref here.
		test.goalControl = &goalControllerState{}
	}
	if scaledAny && scaleFailureReason == "" {
		test.goalControl.lastScaleAt = &now
		test.goalControl.lastScaleDirection = update.ScaleDirection
		test.goalControl.lastScaleReason = update.ScaleReason
	}
	tr.mu.Unlock()
	live.GoalStatus.CurrentTotalVUsers = totalGroupVUsers(groupVUsers)
	if scaleFailureReason != "" {
		// Do not start the cooldown after a failed or partially applied fan-out;
		// the next metrics interval must be allowed to retry the unmet target.
		live.GoalStatus.LastScaleReason = scaleFailureReason
		return
	}
	if scaledAny {
		live.GoalStatus.LastScaleAt = &now
		live.GoalStatus.LastScaleDirection = update.ScaleDirection
		live.GoalStatus.LastScaleReason = update.ScaleReason
	}
	live.GoalStatus.SuggestedTotalVUsers = live.GoalStatus.CurrentTotalVUsers
}

func (tr *TestRunner) requestStopIfRunning(test *ManagedTest, warningMessage string) {
	tr.mu.RLock()
	status := test.Status
	tr.mu.RUnlock()
	if status != "RUNNING" {
		return
	}
	if err := tr.StopTest(test.ID); err != nil {
		slog.Warn(warningMessage, "test_id", test.ID, "error", err)
	}
}

func (tr *TestRunner) requestStopIfRunningWithReason(test *ManagedTest, reason, warningMessage string) {
	tr.mu.Lock()
	if test.Status != "RUNNING" {
		tr.mu.Unlock()
		return
	}
	previousError := test.Error
	test.Error = strings.TrimSpace(reason)
	installedReason := test.Error
	tr.mu.Unlock()
	if err := tr.StopTest(test.ID); err != nil {
		tr.mu.Lock()
		if test.Status == "RUNNING" && test.Error == installedReason {
			test.Error = previousError
		}
		tr.mu.Unlock()
		slog.Warn(warningMessage, "test_id", test.ID, "error", err)
	}
}

func (tr *TestRunner) startManagedTestRun(test *ManagedTest, mge *engine.MultiGroupEngine, artifacts *runArtifacts) (context.Context, chan struct{}, chan time.Time, bool, error) {
	tr.mu.Lock()
	previousExecutionCtx := test.executionCtx
	previousCancel := test.cancel
	ctx := test.executionCtx
	if ctx == nil || test.cancel == nil {
		ctx, test.cancel = tr.executionOperationContext(context.Background())
		test.executionCtx = ctx
	}
	if tr.shuttingDown || test.Status != "INITIALIZING" || ctx.Err() != nil || !tr.isExecutionLeader() {
		tr.mu.Unlock()
		return ctx, nil, nil, false, nil
	}
	tr.mu.Unlock()

	if err := mge.ReserveRuntimeResources(ctx); err != nil {
		return ctx, nil, nil, false, fmt.Errorf("local runtime resource admission: %w", err)
	}
	{
		demands := mge.RuntimeResourceDemands()
		currentVUsers := mge.GetGroupVUsers()
		tr.mu.Lock()
		if tr.tests[test.ID] != test || test.executionCtx != ctx || ctx.Err() != nil {
			tr.mu.Unlock()
			return ctx, nil, nil, false, errors.New("preparation execution no longer current")
		}
		peaks, peakErr := runtimeReservationGroups(test, false)
		if peakErr != nil {
			tr.mu.Unlock()
			return ctx, nil, nil, false, peakErr
		}
		test.GeneratorShards = localRuntimeResourceAllocations(peaks, demands, currentVUsers)
		if test.preparationID != "" {
			test.preparationPeerCheck = func(context.Context) error {
				if !mge.RuntimeResourceReservationsCurrent() {
					return errors.New("prepared runtime resource reservation unavailable")
				}
				return nil
			}
		}
		tr.mu.Unlock()
	}
	if err := tr.awaitPreparedStart(test); err != nil {
		return ctx, nil, nil, false, err
	}

	// Local runtime preflight must complete while the durable state is still
	// INITIALIZING. MultiGroupEngine caches the successful result, so its
	// defensive Run-time check is a no-op and cannot allocate a VUser twice.
	if err := mge.PreflightRuntime(ctx); err != nil {
		if previousExecutionCtx == nil || previousCancel == nil {
			tr.mu.Lock()
			if test.executionCtx == ctx && test.Status == "INITIALIZING" {
				test.cancel()
				test.executionCtx = previousExecutionCtx
				test.cancel = previousCancel
			}
			tr.mu.Unlock()
		}
		return ctx, nil, nil, false, fmt.Errorf("local runtime preflight: %w", err)
	}

	now := time.Now()
	tr.mu.Lock()
	if tr.shuttingDown || test.Status != "INITIALIZING" || ctx.Err() != nil || !tr.isExecutionLeader() {
		tr.mu.Unlock()
		return ctx, nil, nil, false, nil
	}
	previousMultiEngine := test.multiEngine
	previousStartedAt := cloneTimePtr(test.StartedAt)
	previousEndedAt := cloneTimePtr(test.EndedAt)
	previousStatus := test.Status
	previousPendingSince := cloneTimePtr(test.PendingSince)
	previousPendingReason := test.PendingReason
	previousQueuePosition := test.QueuePosition
	previousError := test.Error
	previousGoalControl := test.goalControl
	previousGroups := cloneArchiveTestGroups(test.Groups)
	test.multiEngine = mge
	test.StartedAt = &now
	test.EndedAt = nil
	test.Status = "RUNNING"
	test.PendingSince = nil
	test.PendingReason = ""
	test.QueuePosition = 0
	test.Error = ""
	test.goalControl = &goalControllerState{}
	initializeRuntimeGroupsLocked(test, now)
	if err := tr.saveManagedTestRecord(test); err != nil {
		test.multiEngine = previousMultiEngine
		test.StartedAt = previousStartedAt
		test.EndedAt = previousEndedAt
		test.Status = previousStatus
		test.PendingSince = previousPendingSince
		test.PendingReason = previousPendingReason
		test.QueuePosition = previousQueuePosition
		test.Error = previousError
		test.goalControl = previousGoalControl
		test.Groups = previousGroups
		if previousExecutionCtx == nil || previousCancel == nil {
			test.cancel()
			test.executionCtx = previousExecutionCtx
			test.cancel = previousCancel
		}
		tr.mu.Unlock()
		return ctx, nil, nil, false, fmt.Errorf("persist running test state: %w", err)
	}
	snapshot := buildRunLiveStateSnapshotLocked(test, now)
	eventSnapshot := cloneManagedTestFieldsLocked(test)
	tr.mu.Unlock()

	tr.cacheRunSnapshot(snapshot)
	tr.broadcastEvent("test_started", eventSnapshot)
	tr.startStageSchedules(ctx, test, mge)
	tr.startTrafficProfileSchedules(ctx, test, mge)
	tr.scheduleRuntimeGroupDeadlines(eventSnapshot)

	metricsDone := make(chan struct{})
	metricsStopped := make(chan time.Time, 1)
	go func() {
		metricsStopped <- tr.broadcastMetrics(test, mge, artifacts, metricsDone)
		close(metricsStopped)
	}()

	return ctx, metricsDone, metricsStopped, true, nil
}

func (tr *TestRunner) failTestBeforeRun(test *ManagedTest, message string) error {
	now := time.Now()
	tr.mu.Lock()
	// Startup work runs asynchronously. A stop or leadership-loss cleanup may
	// have won while a backend was compiling scripts, dialing generators, or
	// assigning shards. Never resurrect that terminal decision as FAILED.
	if test == nil || tr.tests[test.ID] != test || test.Status != "INITIALIZING" {
		tr.mu.Unlock()
		return nil
	}
	if err := tr.persistStoredTestStatus(test.ID, test.executionFence, "FAILED", &now, message); err != nil {
		tr.mu.Unlock()
		if isStoredTestTransitionCancelled(err) {
			// A successor deleted the durable row while this stale startup path was
			// finishing. Do not recreate local terminal side effects or retry it.
			return nil
		}
		retryErr := fmt.Errorf("persist pre-run test failure: %w", err)
		slog.Error("failed to persist pre-run test failure; terminal side effects deferred", "test_id", test.ID, "error", retryErr)
		tr.enqueueTerminalRetry(test.ID, "FAILED before run", func() error {
			return tr.failTestBeforeRun(test, message)
		})
		return retryErr
	}
	test.Status = "FAILED"
	test.PendingSince = nil
	test.PendingReason = ""
	test.QueuePosition = 0
	test.Error = message
	test.EndedAt = &now
	test.cancel = nil
	test.executionCtx = nil
	eventSnapshot := cloneManagedTestFieldsLocked(test)
	compactTerminalManagedTestLocked(test)
	tr.mu.Unlock()
	releaseErr := tr.releaseCapacityReservationForTerminalStatus(test.ID, "FAILED")
	if releaseErr != nil && test.TimeslotID != nil {
		tr.enqueueCapacityReservationReleaseRetry(test.ID, strings.TrimSpace(*test.TimeslotID))
	}
	tr.broadcastEvent("test_failed", eventSnapshot)
	tr.promotePendingTests()
	if err := tr.advanceExecutionPlanForTest(test, "FAILED"); err != nil {
		slog.Error("failed to durably advance execution plan after pre-run failure", "test_id", test.ID, "error", err)
		return errors.Join(releaseErr, err)
	}
	return releaseErr
}

func (tr *TestRunner) failTestBeforeRunAndLog(test *ManagedTest, message string) {
	if err := tr.failTestBeforeRun(test, message); err != nil {
		testID := ""
		if test != nil {
			testID = test.ID
		}
		slog.Error("pre-run terminal transition remains retryable after bounded persistence retries", "test_id", testID, "error", err)
	}
}

func (tr *TestRunner) completeTestRun(test *ManagedTest, finalStatus string, endTime time.Time, finalError string, archiveRecord *storage.RunArchiveRecord) error {
	return tr.completeTestRunWithPendingPromotion(test, finalStatus, endTime, finalError, archiveRecord, true)
}

func (tr *TestRunner) completeTestRunWithPendingPromotion(test *ManagedTest, finalStatus string, endTime time.Time, finalError string, archiveRecord *storage.RunArchiveRecord, promotePending bool) error {
	if tr == nil || test == nil {
		return nil
	}
	unlockArchiveCommit := tr.lockArchiveCommitFence(test.ID)
	defer unlockArchiveCommit()
	return tr.completeTestRunUnderArchiveFenceWithPendingPromotion(test, finalStatus, endTime, finalError, archiveRecord, promotePending)
}

// completeTestRunUnderArchiveFence performs the terminal row and archive-map
// commit while the caller owns test.ID's archive commit fence.
func (tr *TestRunner) completeTestRunUnderArchiveFence(test *ManagedTest, finalStatus string, endTime time.Time, finalError string, archiveRecord *storage.RunArchiveRecord) error {
	return tr.completeTestRunUnderArchiveFenceWithPendingPromotion(test, finalStatus, endTime, finalError, archiveRecord, true)
}

func (tr *TestRunner) completeTestRunUnderArchiveFenceWithPendingPromotion(test *ManagedTest, finalStatus string, endTime time.Time, finalError string, archiveRecord *storage.RunArchiveRecord, promotePending bool) error {
	tr.mu.Lock()
	if test == nil || tr.tests[test.ID] != test {
		tr.mu.Unlock()
		return nil
	}
	// Completion retries are responsible only for the terminal test row. Once a
	// terminal transition committed, any execution-plan follow-up has its own
	// idempotent retry worker; replaying terminal side effects here would duplicate
	// WebSocket events, usage snapshots, and queue promotion.
	switch test.Status {
	case "COMPLETE", "FAILED", "ABORTED":
		releasePassiveOperationTokenLocked(test)
		tr.mu.Unlock()
		tr.recordLocalArchiveFailure(test)
		return nil
	}
	if err := tr.persistStoredTestStatus(test.ID, test.executionFence, finalStatus, &endTime, finalError); err != nil {
		if isStoredTestTransitionCancelled(err) {
			// Deletion is a durable tombstone. Treat it as cancellation of this stale
			// completion, not as a successful terminal commit. Drop only this
			// process-local claim before reload convergence; the row identity is still
			// protected by tr.mu and the caller's exact ManagedTest pointer check above.
			releasePassiveOperationTokenLocked(test)
			tr.mu.Unlock()
			return nil
		}
		tr.mu.Unlock()
		retryErr := fmt.Errorf("persist terminal test state: %w", err)
		slog.Error("failed to persist terminal test state; terminal side effects deferred", "test_id", test.ID, "status", finalStatus, "error", retryErr)
		tr.enqueueTestCompletionRetry(test, finalStatus, endTime, finalError, archiveRecord)
		return retryErr
	}
	tr.cancelAllVUserScaleRampsLocked(test)
	test.Status = finalStatus
	test.PendingSince = nil
	test.PendingReason = ""
	test.QueuePosition = 0
	test.Error = finalError
	test.EndedAt = &endTime
	test.cancel = nil
	test.executionCtx = nil
	releasePassiveOperationTokenLocked(test)
	clearDistributedRunSecretSnapshotsLocked(test)
	test.Archive = archiveRecord
	if archiveRecord != nil {
		tr.archives[test.ID] = archiveRecord
	}
	terminalSnapshot := buildRunLiveStateSnapshotLocked(test, endTime)
	eventSnapshot := cloneManagedTestFieldsLocked(test)
	compactTerminalManagedTestLocked(test)
	tr.mu.Unlock()
	tr.recordLocalArchiveFailure(test)
	if finalStatus == "FAILED" {
		if err := tr.syncScenarioScheduleLifecycleForTest(test.ID); err != nil {
			slog.Warn("terminal test transition could not refresh scenario occurrence cache", "test_id", test.ID, "error", err)
		}
	}
	tr.cacheRunSnapshot(terminalSnapshot)
	releaseErr := tr.releaseCapacityReservationForTerminalStatus(test.ID, finalStatus)
	if releaseErr != nil && test.TimeslotID != nil {
		tr.enqueueCapacityReservationReleaseRetry(test.ID, strings.TrimSpace(*test.TimeslotID))
	}
	tr.captureUsageSnapshotForTerminalRun(test.ID, test.ProjectID)
	if finalStatus == "FAILED" {
		tr.broadcastEvent("test_failed", eventSnapshot)
		if promotePending {
			tr.promotePendingTests()
		}
		if err := tr.advanceExecutionPlanForTest(test, finalStatus); err != nil {
			slog.Error("failed to durably advance execution plan after terminal test failure", "test_id", test.ID, "error", err)
			return errors.Join(releaseErr, err)
		}
		return releaseErr
	}
	if finalStatus == "ABORTED" {
		tr.broadcastEvent("test_aborted", eventSnapshot)
	} else {
		tr.broadcastEvent("test_complete", eventSnapshot)
	}
	if promotePending {
		tr.promotePendingTests()
	}
	if err := tr.advanceExecutionPlanForTest(test, finalStatus); err != nil {
		slog.Error("failed to durably advance execution plan after terminal test completion", "test_id", test.ID, "error", err)
		return errors.Join(releaseErr, err)
	}
	return releaseErr
}

func (tr *TestRunner) promotePendingTests() {
	// Promotion starts work, so it is leader-only. Non-leaders never promote;
	// the leader drives the shared queue.
	if !tr.isExecutionLeader() {
		return
	}
	leaseTerm, err := tr.captureExecutionLeaseTerm()
	if err != nil {
		return
	}
	if err := tr.promotePendingTestsWithTerm(leaseTerm); err != nil && !errors.Is(err, errNotExecutionLeader) {
		if errors.Is(err, errStartAdmissionEvidenceStale) {
			tr.enqueuePendingPromotionRetryWithTerm(leaseTerm)
		}
		slog.Warn("pending test promotion failed", "error", err)
	}
}

const (
	pendingPromotionRetryInitialBackoff = 25 * time.Millisecond
	pendingPromotionRetryMaxBackoff     = time.Second
)

func (tr *TestRunner) enqueuePendingPromotionRetryWithTerm(term executionLeaseTerm) {
	if tr == nil || !tr.executionLeaseTermCurrent(term) {
		return
	}
	tr.mu.Lock()
	if tr.shuttingDown || (tr.pendingPromotionRetryActive && tr.pendingPromotionRetryTerm == term) {
		tr.mu.Unlock()
		return
	}
	tr.pendingPromotionRetryActive = true
	tr.pendingPromotionRetryTerm = term
	tr.mu.Unlock()
	go tr.runPendingPromotionRetry(term)
}

func (tr *TestRunner) runPendingPromotionRetry(term executionLeaseTerm) {
	ctx, cancel := tr.executionOperationContext(nil)
	defer cancel()
	if tr.durableExecutionFence {
		contextTerm, ok := executionLeaseTermFromContext(ctx)
		if !ok || contextTerm != term {
			tr.finishPendingPromotionRetry(term)
			return
		}
	}
	defer tr.finishPendingPromotionRetry(term)

	backoff := pendingPromotionRetryInitialBackoff
	for {
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if !tr.executionLeaseTermCurrent(term) {
			return
		}
		tr.mu.RLock()
		shuttingDown := tr.shuttingDown
		hasPending := false
		for _, test := range tr.tests {
			if test != nil && (strings.EqualFold(strings.TrimSpace(test.Status), "PENDING") ||
				(strings.EqualFold(strings.TrimSpace(test.Status), "SHAKEOUT") && test.pendingScriptShakeoutOutcome != nil &&
					test.pendingScriptShakeoutOutcome.PassStatus == "PENDING")) {
				hasPending = true
				break
			}
		}
		tr.mu.RUnlock()
		if shuttingDown || !hasPending {
			return
		}
		err := tr.promotePendingTestsWithTerm(term)
		if err == nil || !errors.Is(err, errStartAdmissionEvidenceStale) {
			if err != nil && !errors.Is(err, errNotExecutionLeader) {
				slog.Warn("pending test promotion retry failed permanently", "error", err)
			}
			return
		}
		backoff = min(backoff*2, pendingPromotionRetryMaxBackoff)
	}
}

func (tr *TestRunner) finishPendingPromotionRetry(term executionLeaseTerm) {
	tr.mu.Lock()
	if tr.pendingPromotionRetryActive && tr.pendingPromotionRetryTerm == term {
		tr.pendingPromotionRetryActive = false
		tr.pendingPromotionRetryTerm = executionLeaseTerm{}
	}
	tr.mu.Unlock()
}

// promotePendingTestsWithTerm promotes fair-share queue entries under one
// immutable execution lease term. A caller that crossed another durable
// side-effect boundary (for example execution-plan materialization) must retain
// its original term here so an old goroutine cannot adopt leadership after the
// same process reacquires a newer generation.
func (tr *TestRunner) promotePendingTestsWithTerm(leaseTerm executionLeaseTerm) error {
	deferredPending := make(map[string]struct{})
	for {
		if !tr.executionLeaseTermLocallyCurrent(leaseTerm) {
			return errNotExecutionLeader
		}
		// A completed pending-row shakeout can remain in SHAKEOUT only while its
		// outcome write is retryable. Finish that exact result before selecting
		// more queue work; never execute the one-shot actions a second time.
		tr.mu.RLock()
		pendingOutcomeID := ""
		for id, test := range tr.tests {
			if test != nil && test.Status == "SHAKEOUT" && test.pendingScriptShakeoutOutcome != nil &&
				test.pendingScriptShakeoutOutcome.PassStatus == "PENDING" {
				pendingOutcomeID = id
				break
			}
		}
		tr.mu.RUnlock()
		if pendingOutcomeID != "" {
			if err := tr.resumePendingScriptShakeoutOutcome(pendingOutcomeID, &leaseTerm); err != nil {
				var gateErr *ScriptShakeoutGateError
				if !errors.As(err, &gateErr) {
					return err
				}
			}
			continue
		}
		tr.mu.RLock()
		selectionState := tr.pendingQueueMetadataStateLocked()
		projectStore := tr.projectStore
		tr.mu.RUnlock()
		selectionAt := time.Now().UTC()
		activeSlots, reservationErr := tr.loadBoundTimeslotsActiveAt(selectionAt)
		projectQuotas := loadPendingProjectQuotaEvidence(projectStore, selectionState)
		if reservationErr != nil {
			return errors.Join(errStartAdmissionEvidenceStale, fmt.Errorf("load pending capacity reservations: %w", reservationErr))
		}
		removedMissingTest := false
		for test, evidence := range projectQuotas {
			if evidence.err != nil {
				if errors.Is(evidence.err, sql.ErrNoRows) {
					tr.evictMissingManagedTestIdentity(test.ID, test)
					removedMissingTest = true
					continue
				}
				return errors.Join(errStartAdmissionEvidenceStale, fmt.Errorf("load pending project %q quota for test %q: %w", evidence.projectID, test.ID, evidence.err))
			}
		}
		if removedMissingTest {
			continue
		}
		if !tr.executionLeaseTermCurrent(leaseTerm) {
			return errNotExecutionLeader
		}
		tr.mu.Lock()
		if tr.shuttingDown {
			tr.mu.Unlock()
			return nil
		}
		if !tr.executionLeaseTermLocallyCurrent(leaseTerm) {
			tr.mu.Unlock()
			return errNotExecutionLeader
		}
		if !samePendingQueueMetadataState(selectionState, tr.pendingQueueMetadataStateLocked()) {
			tr.mu.Unlock()
			continue
		}
		pending := tr.nextPromotablePendingTestExcludingWithTimeslotsLocked(deferredPending, selectionAt, activeSlots, reservationErr, projectQuotas)
		if pending == nil {
			awaitingSecretRepair := tr.pendingSecretRepairAwaitedLocked()
			tr.mu.Unlock()
			if awaitingSecretRepair {
				return errStartAdmissionEvidenceStale
			}
			return nil
		}
		pendingID := pending.ID
		tr.mu.Unlock()
		if policyErr := tr.validateCIRunPolicyForStart(pendingID); policyErr != nil {
			if errors.Is(policyErr, errStartAdmissionEvidenceStale) {
				return policyErr
			}
			message := "controller refused pending CI run before execution: " + policyErr.Error()
			if terminalErr := tr.failCIRunBeforeExecutionDuringPromotion(pendingID, message); terminalErr != nil {
				return terminalErr
			}
			continue
		}
		// The original start attempt can enter PENDING at the quota gate before
		// static secrets are checked. Resolve them at the promotion boundary so
		// a queued run cannot bypass the ordinary StartTest preflight.
		if secretErr := tr.validateStartSecretRequirements(pendingID); secretErr != nil {
			message := "pending test promotion secret requirements are no longer satisfied: " + secretErr.Error()
			if retryableStartSecretRequirement(secretErr) {
				if deferErr := tr.deferPendingPromotionGateWithTerm(pendingID, message, leaseTerm); deferErr != nil {
					return deferErr
				}
				deferredPending[pendingID] = struct{}{}
			} else if terminalErr := tr.failPendingPromotionGateWithTerm(pendingID, message, leaseTerm); terminalErr != nil {
				return terminalErr
			}
			continue
		}
		if _, err := tr.validateScriptShakeoutPassForStart(pendingID, StartTestOptions{}, &leaseTerm); err != nil {
			var gateErr *ScriptShakeoutGateError
			if errors.As(err, &gateErr) || retryableStartSecretRequirement(err) {
				messagePrefix := pendingScriptShakeoutManualReviewReasonPrefix
				if retryableStartSecretRequirement(err) {
					messagePrefix = pendingRetryableShakeoutEvidenceReasonPrefix
				}
				message := messagePrefix + " " + err.Error()
				if deferErr := tr.deferPendingPromotionGateWithTerm(pendingID, message, leaseTerm); deferErr != nil {
					return deferErr
				}
				deferredPending[pendingID] = struct{}{}
				continue
			}
			return err
		}
		if err := tr.refreshTestCapacityBindingFromStore(pendingID); err != nil {
			return err
		}
		shakeoutInputFingerprint := scriptShakeoutInputFingerprint{}
		shakeoutEvidence := startLaunchEvidence{}
		shakeoutEvidenceLoaded := false
		if tr.scriptShakeoutRequiredForStart(pendingID, StartTestOptions{}) {
			fingerprint, fingerprintErr := tr.scriptShakeoutExecutableFingerprint(pendingID)
			if fingerprintErr != nil {
				if retryableStartSecretRequirement(fingerprintErr) {
					message := pendingRetryableShakeoutEvidenceReasonPrefix + " " + fingerprintErr.Error()
					if deferErr := tr.deferPendingPromotionGateWithTerm(pendingID, message, leaseTerm); deferErr != nil {
						return deferErr
					}
					deferredPending[pendingID] = struct{}{}
					continue
				}
				message := "pending test script shakeout inputs could not be fingerprinted; no shakeout actions were executed: " + fingerprintErr.Error()
				if terminalErr := tr.failPendingPromotionGateWithTerm(pendingID, message, leaseTerm); terminalErr != nil {
					return terminalErr
				}
				continue
			}
			shakeoutInputFingerprint = fingerprint
			shakeoutAt := time.Now().UTC()
			var evidenceErr error
			shakeoutEvidence, evidenceErr = tr.loadStartLaunchEvidence(pendingID, shakeoutAt)
			if evidenceErr != nil {
				return evidenceErr
			}
			shakeoutEvidenceLoaded = true
		}
		tr.mu.Lock()
		pending = tr.tests[pendingID]
		if pending == nil || pending.Status != "PENDING" {
			tr.mu.Unlock()
			continue
		}
		shakeoutAlreadyPassed := pending.StartAuthorization != nil && scriptShakeoutPassAllows(pending.StartAuthorization.ScriptShakeoutPass) &&
			pending.validatedScriptShakeoutPass != ""
		if pending.ScriptShakeout && !shakeoutAlreadyPassed && !startOptionsWithAuthorization(pending, StartTestOptions{}).OverrideScriptShakeoutError {
			if !shakeoutEvidenceLoaded {
				tr.mu.Unlock()
				return errStartAdmissionEvidenceStale
			}
			if err := tr.validateStartLaunchEvidenceFenceLocked(shakeoutEvidence); err != nil {
				tr.mu.Unlock()
				return err
			}
			// SHAKEOUT is an execution-capable durable state, so claim only at
			// the transition that persists it. Passive PENDING rows retain no token.
			previousExecutionFence := pending.executionFence
			previousExecutionGeneration := pending.executionGeneration
			previousPassiveOperationToken := pending.passiveOperationToken
			previousExpectedProjectUpdatedAt := pending.expectedProjectUpdatedAt
			previousExpectedStartAdmissionRevision := pending.expectedStartAdmissionRevision
			claimed, err := tr.claimPassiveExecutionFenceLocked(pending, &leaseTerm)
			if err != nil {
				tr.mu.Unlock()
				slog.Warn("pending test promotion could not claim execution epoch", "test_id", pending.ID, "error", err)
				return err
			}
			if err := tr.installTestExecutionContextLocked(pending, &leaseTerm); err != nil {
				if claimed {
					pending.executionFence = previousExecutionFence
					pending.executionGeneration = previousExecutionGeneration
					pending.passiveOperationToken = previousPassiveOperationToken
				}
				tr.mu.Unlock()
				return err
			}
			previousStatus := pending.Status
			previousPendingSince := cloneTimePtr(pending.PendingSince)
			previousPendingReason := pending.PendingReason
			previousQueuePosition := pending.QueuePosition
			previousError := pending.Error
			pending.Status = "SHAKEOUT"
			pending.PendingSince = nil
			pending.PendingReason = ""
			pending.QueuePosition = 0
			pending.Error = ""
			pending.expectedProjectUpdatedAt = shakeoutEvidence.launchWindow.projectQuota.updatedAt
			pending.expectedStartAdmissionRevision = shakeoutEvidence.startAdmissionRevision
			if owned, err := tr.persistManagedTestStartOutsideLockLocked(pending, "SHAKEOUT"); err != nil {
				if owned {
					clearScriptShakeoutContextLocked(pending)
					pending.executionFence = previousExecutionFence
					pending.executionGeneration = previousExecutionGeneration
					pending.passiveOperationToken = previousPassiveOperationToken
					pending.expectedProjectUpdatedAt = previousExpectedProjectUpdatedAt
					pending.expectedStartAdmissionRevision = previousExpectedStartAdmissionRevision
					pending.Status = previousStatus
					pending.PendingSince = previousPendingSince
					pending.PendingReason = previousPendingReason
					pending.QueuePosition = previousQueuePosition
					pending.Error = previousError
				}
				tr.mu.Unlock()
				slog.Warn("pending test promotion could not persist shakeout", "test_id", pending.ID, "error", err)
				return err
			}
			releasePassiveOperationTokenLocked(pending)
			tr.broadcastEvent("script_shakeout_started", cloneManagedTestLifecycleEvent(pending))
			tr.mu.Unlock()

			report, operationErr := tr.runScriptShakeoutReport(pending)
			if operationErr != nil {
				if !tr.executionLeaseTermCurrent(leaseTerm) {
					return errNotExecutionLeader
				}
				return operationErr
			}
			if report.Passed {
				currentSHA256, fingerprintErr := tr.scriptShakeoutExecutableFingerprint(pending.ID)
				switch {
				case fingerprintErr != nil:
					if retryableStartSecretRequirement(fingerprintErr) {
						pendingSince := time.Now().UTC()
						return tr.markScriptShakeoutVerificationPendingWithExpectedTerm(
							pending.ID,
							report,
							shakeoutInputFingerprint,
							"PENDING",
							&pendingSince,
							"awaiting capacity after script shakeout",
							&leaseTerm,
							fingerprintErr,
							false,
						)
					}
					report.Passed = false
					report.Summary = "Script shakeout completed, but its executable input evidence could not be verified; full load test has not started."
					report.Results = append(report.Results, ScriptShakeoutResult{GroupName: "executable inputs", Status: "failed", Error: fingerprintErr.Error()})
				case currentSHA256 != shakeoutInputFingerprint:
					report.Passed = false
					report.Summary = "Script shakeout inputs changed while the check was running; full load test has not started."
					report.Results = append(report.Results, ScriptShakeoutResult{GroupName: "executable inputs", Status: "failed", Error: "the executable snapshot or a versioned secret source changed during shakeout"})
				default:
					shakeoutInputFingerprint = currentSHA256
				}
			}
			if !report.Passed {
				if err := tr.markScriptShakeoutReviewWithExpectedTerm(pending.ID, report, &leaseTerm); err != nil {
					var gateErr *ScriptShakeoutGateError
					if !errors.As(err, &gateErr) {
						slog.Warn("pending test promotion could not persist shakeout review", "test_id", pending.ID, "error", err)
						return err
					}
				}
				tr.recordSystemTestAudit("test.script_shakeout.review", "blocked", pending, map[string]any{"report": report})
				continue
			}

			now := time.Now().UTC()
			if err := tr.markPendingScriptShakeoutPassedWithExpectedTerm(pending.ID, report, shakeoutInputFingerprint, now, &leaseTerm); err != nil {
				slog.Warn("pending test promotion could not persist successful shakeout", "test_id", pending.ID, "error", err)
				return err
			}
			if policyErr := tr.validateCIRunPolicyForStart(pendingID); policyErr != nil {
				if errors.Is(policyErr, errStartAdmissionEvidenceStale) {
					return policyErr
				}
				message := "controller refused pending CI run after script shakeout: " + policyErr.Error()
				if terminalErr := tr.failCIRunBeforeExecutionDuringPromotion(pendingID, message); terminalErr != nil {
					return terminalErr
				}
				continue
			}
			tr.mu.Lock()
			pending = tr.tests[pendingID]
			if pending == nil || pending.Status != "PENDING" {
				tr.mu.Unlock()
				continue
			}
		}
		// Shakeout runs without tr.mu and may invoke user protocols for tens of
		// seconds. Verify its pinned secret snapshot again before final admission.
		// An execution that already began shakeout keeps that snapshot even if a
		// provider changes. Resolution may perform remote I/O and stays outside
		// the runner mutex.
		tr.mu.Unlock()
		if secretErr := tr.validateStartSecretRequirements(pendingID); secretErr != nil {
			message := "pending test promotion secret requirements are no longer satisfied: " + secretErr.Error()
			if retryableStartSecretRequirement(secretErr) {
				if deferErr := tr.deferPendingPromotionGateWithTerm(pendingID, message, leaseTerm); deferErr != nil {
					return deferErr
				}
				deferredPending[pendingID] = struct{}{}
			} else if terminalErr := tr.failPendingPromotionGateWithTerm(pendingID, message, leaseTerm); terminalErr != nil {
				return terminalErr
			}
			continue
		}
		if err := tr.refreshTestCapacityBindingFromStore(pendingID); err != nil {
			return err
		}
		sourceScheduleID, sourceScheduleErr := tr.scenarioScheduleIDForTest(pendingID)
		if sourceScheduleErr != nil {
			if errors.Is(sourceScheduleErr, storage.ErrScenarioScheduleOccurrenceCorrelationAmbiguous) {
				message := "pending test has ambiguous scenario occurrence correlation: " + sourceScheduleErr.Error()
				if terminalErr := tr.failPendingPromotionGateWithTerm(pendingID, message, leaseTerm); terminalErr != nil {
					return terminalErr
				}
				continue
			}
			return errors.Join(errStartAdmissionEvidenceStale, fmt.Errorf("resolve pending test source schedule: %w", sourceScheduleErr))
		}
		promotionAt := time.Now().UTC()
		promotionEvidence, evidenceErr := tr.loadStartLaunchEvidence(pendingID, promotionAt)
		if evidenceErr != nil {
			return evidenceErr
		}
		if policyErr := tr.validateCIRunPolicyForStartAtProjectRevision(pendingID, promotionEvidence.launchWindow.projectQuota.updatedAt); policyErr != nil {
			if errors.Is(policyErr, errStartAdmissionEvidenceStale) {
				return policyErr
			}
			message := "controller refused pending CI run before execution: " + policyErr.Error()
			if terminalErr := tr.failCIRunBeforeExecutionDuringPromotion(pendingID, message); terminalErr != nil {
				return terminalErr
			}
			continue
		}
		if !tr.executionLeaseTermCurrent(leaseTerm) {
			return errNotExecutionLeader
		}
		tr.mu.Lock()
		pending = tr.tests[pendingID]
		if pending == nil || pending.Status != "PENDING" {
			tr.mu.Unlock()
			continue
		}
		if !tr.executionLeaseTermLocallyCurrent(leaseTerm) {
			tr.mu.Unlock()
			return errNotExecutionLeader
		}
		if err := tr.validateStartLaunchEvidenceFenceLocked(promotionEvidence); err != nil {
			tr.mu.Unlock()
			return err
		}
		// Candidate selection above is deliberately optimistic: CI policy and an
		// optional shakeout/secret check run without tr.mu and can take long enough
		// for another test to consume the last global/project/VUser slot. The same
		// delay can also move a scheduled run outside its launch window or invalidate
		// its bound capacity timeslot. Re-run every time-sensitive gate while holding
		// tr.mu immediately before INITIALIZING so the admission decision and the
		// capacity-reserving state transition are one atomic controller operation.
		if !tr.executionLeaseTermLocallyCurrent(leaseTerm) {
			tr.mu.Unlock()
			return errNotExecutionLeader
		}
		if err := recoverySpareSettingsError(pending, tr.settings); err != nil {
			tr.mu.Unlock()
			if terminalErr := tr.failPendingPromotionGateIfWithTerm(pendingID, "pending test promotion configuration is no longer valid: "+err.Error(), leaseTerm, func(test *ManagedTest) bool {
				return recoverySpareSettingsError(test, tr.settings) != nil
			}); terminalErr != nil {
				return terminalErr
			}
			continue
		}
		tr.refreshCapacityAdvisoryLocked(pending)
		effectiveOpts := startOptionsWithAuthorization(pending, StartTestOptions{})
		if err := buildStartGovernanceError(pending.ID, pending.CapacityAdvisory, effectiveOpts); err != nil {
			tr.mu.Unlock()
			message := "pending test promotion governance failed: " + err.Error()
			var capacityErr *CapacityShortfallError
			if errors.As(err, &capacityErr) {
				message = pendingCapacityAuthorizationReasonPrefix + " " + err.Error()
				if deferErr := tr.deferPendingPromotionGateWithTerm(pendingID, message, leaseTerm); deferErr != nil {
					return deferErr
				}
				deferredPending[pendingID] = struct{}{}
			} else if terminalErr := tr.failPendingPromotionGateWithTerm(pendingID, message, leaseTerm); terminalErr != nil {
				return terminalErr
			}
			continue
		}
		javaConflicts, placementConflicts, runtimeEvidenceErr := tr.startRuntimeGateConflictsLocked(pending, promotionEvidence)
		if runtimeEvidenceErr != nil {
			tr.mu.Unlock()
			return runtimeEvidenceErr
		}
		if len(javaConflicts) > 0 {
			conflict := javaConflicts[0]
			tr.mu.Unlock()
			message := "pending test promotion Java Vuser launch gate failed: " + conflict.Code + ": " + conflict.Message
			if conflict.Code == "java_vuser_runner_required" {
				if deferErr := tr.deferPendingPromotionGateWithTerm(pendingID, message, leaseTerm); deferErr != nil {
					return deferErr
				}
				deferredPending[pendingID] = struct{}{}
			} else if terminalErr := tr.failPendingPromotionGateWithTerm(pendingID, message, leaseTerm); terminalErr != nil {
				return terminalErr
			}
			continue
		}
		if len(placementConflicts) > 0 {
			conflict := placementConflicts[0]
			tr.mu.Unlock()
			message := "pending test promotion Java Vuser placement gate failed: " + conflict.Code + ": " + conflict.Message
			if deferErr := tr.deferPendingPromotionGateWithTerm(pendingID, message, leaseTerm); deferErr != nil {
				return deferErr
			}
			deferredPending[pendingID] = struct{}{}
			continue
		}
		if err := startLaunchEvidenceRetryError(pending, promotionAt, promotionEvidence.launchWindow); err != nil {
			tr.mu.Unlock()
			return err
		}
		if blocked, reason := tr.evaluateQuotaBlockWithLaunchEvidenceLocked(pending, "", promotionAt, promotionEvidence.launchWindow); blocked {
			// Quota pressure is transient. Keep the row durably queued with the
			// current reason; a later completion/reconciliation will retry it.
			if pending.PendingReason != reason {
				if err := tr.markTestPendingLockedWithExpectedTerm(pending, reason, &leaseTerm); err != nil {
					tr.mu.Unlock()
					return err
				}
			}
			tr.mu.Unlock()
			continue
		}
		now := promotionAt
		// A queued scenario occurrence is already represented by its parent
		// recurring schedule in the projected launch window. Exclude that source
		// schedule while revalidating the occurrence so it cannot conflict with
		// itself after transient quota pressure clears.
		if err := tr.launchStartWindowErrorWithEvidenceLocked(pending, now, pending.ID, sourceScheduleID, promotionEvidence.launchWindow); err != nil {
			tr.mu.Unlock()
			if terminalErr := tr.failPendingPromotionGateWithTerm(pendingID, "pending test promotion launch window is no longer valid: "+err.Error(), leaseTerm); terminalErr != nil {
				return terminalErr
			}
			continue
		}
		if err := tr.launchTimeslotRevalidationErrorWithEvidenceLocked(pending, now, promotionEvidence.launchWindow); err != nil {
			tr.mu.Unlock()
			if terminalErr := tr.failPendingPromotionGateWithTerm(pendingID, "pending test promotion capacity reservation is no longer valid: "+err.Error(), leaseTerm); terminalErr != nil {
				return terminalErr
			}
			continue
		}
		previousStatus := pending.Status
		previousPendingSince := cloneTimePtr(pending.PendingSince)
		previousPendingReason := pending.PendingReason
		previousQueuePosition := pending.QueuePosition
		previousError := pending.Error
		previousExecutionCtx := pending.executionCtx
		previousCancel := pending.cancel
		previousBackend := pending.executionBackend
		previousExecutionFence := pending.executionFence
		previousExecutionGeneration := pending.executionGeneration
		previousPassiveOperationToken := pending.passiveOperationToken
		previousExpectedProjectUpdatedAt := pending.expectedProjectUpdatedAt
		previousExpectedStartAdmissionRevision := pending.expectedStartAdmissionRevision
		// Every non-active gate has passed. Claim the exact term only at the
		// INITIALIZING transition so a passive queue row never pins an epoch.
		pending.expectedProjectUpdatedAt = promotionEvidence.launchWindow.projectQuota.updatedAt
		pending.expectedStartAdmissionRevision = promotionEvidence.startAdmissionRevision
		if err := tr.markTestInitializingLockedWithExpectedTerm(pending, &leaseTerm); err != nil {
			pending.expectedProjectUpdatedAt = previousExpectedProjectUpdatedAt
			pending.expectedStartAdmissionRevision = previousExpectedStartAdmissionRevision
			tr.mu.Unlock()
			slog.Warn("pending test promotion lost execution leadership", "test_id", pending.ID, "error", err)
			return err
		}
		if owned, err := tr.persistManagedTestStartOutsideLockLocked(pending, "INITIALIZING"); err != nil {
			if owned {
				if pending.cancel != nil {
					pending.cancel()
				}
				pending.Status = previousStatus
				pending.PendingSince = previousPendingSince
				pending.PendingReason = previousPendingReason
				pending.QueuePosition = previousQueuePosition
				pending.Error = previousError
				pending.executionCtx = previousExecutionCtx
				pending.cancel = previousCancel
				pending.executionBackend = previousBackend
				pending.executionFence = previousExecutionFence
				pending.executionGeneration = previousExecutionGeneration
				pending.passiveOperationToken = previousPassiveOperationToken
				pending.expectedProjectUpdatedAt = previousExpectedProjectUpdatedAt
				pending.expectedStartAdmissionRevision = previousExpectedStartAdmissionRevision
			}
			tr.mu.Unlock()
			if errors.Is(err, errRunPolicyConfiguration) {
				// The project's policies refuse this run until they are fixed.
				// Fail it visibly instead of leaving it at the head of the queue,
				// where it would block every other project's queued runs.
				if terminalErr := tr.failPendingPromotionGateWithTerm(pendingID, "pending test promotion refused: "+err.Error(), leaseTerm); terminalErr != nil {
					return terminalErr
				}
				continue
			}
			slog.Warn("pending test promotion could not persist initializing state", "test_id", pending.ID, "error", err)
			return err
		}
		if previousCancel != nil {
			previousCancel()
		}
		releasePassiveOperationTokenLocked(pending)
		tr.mu.Unlock()
		if err := tr.syncScenarioScheduleLifecycleForTest(pending.ID); err != nil {
			// The INITIALIZING row and its correlated occurrence/schedule fields
			// committed atomically. A cache refresh failure must not strand the run;
			// subsequent HA/read refreshes converge from the durable transaction.
			slog.Warn("pending test promotion could not refresh scenario occurrence cache", "test_id", pending.ID, "error", err)
		}
		tr.recordSystemTestAudit("test.pending.promote", "success", pending, map[string]any{
			"status": pending.Status,
			"reason": "fair_share_capacity_available",
		})
		go tr.runTest(pending)
	}
}

// failPendingPromotionGateWithTerm terminalizes a queue entry whose launch-time
// governance is no longer satisfiable. Quota pressure is handled separately and
// remains PENDING; launch-window and timeslot violations must not sit in the fair-
// share queue until a later day and then execute a stale scheduled occurrence.
func (tr *TestRunner) failPendingPromotionGateWithTerm(id, message string, leaseTerm executionLeaseTerm) error {
	return tr.failPendingPromotionGateIfWithTerm(id, message, leaseTerm, nil)
}

// An optional condition is rechecked under tr.mu before claiming the terminal
// transition. A setting can be corrected between queue selection and this write.
func (tr *TestRunner) failPendingPromotionGateIfWithTerm(id, message string, leaseTerm executionLeaseTerm, stillInvalid func(*ManagedTest) bool) error {
	if !tr.executionLeaseTermCurrent(leaseTerm) {
		return errNotExecutionLeader
	}
	tr.mu.Lock()
	test := tr.tests[id]
	if test == nil || test.Status != "PENDING" || (stillInvalid != nil && !stillInvalid(test)) {
		tr.mu.Unlock()
		return nil
	}
	if _, err := tr.claimPassiveExecutionFenceLocked(test, &leaseTerm); err != nil {
		tr.mu.Unlock()
		return err
	}
	tr.mu.Unlock()
	return tr.completeTestRunWithPendingPromotion(test, "FAILED", time.Now().UTC(), strings.TrimSpace(message), nil, false)
}

const pendingSecretRequirementsReasonPrefix = "pending test promotion secret requirements are no longer satisfied:"
const pendingCapacityAuthorizationReasonPrefix = "pending test promotion requires a new capacity-shortfall approval:"
const pendingRetryableShakeoutEvidenceReasonPrefix = "pending test promotion script shakeout evidence is temporarily unavailable:"
const pendingScriptShakeoutManualReviewReasonPrefix = "pending test promotion requires script shakeout override or re-materialization; shakeout actions were not replayed:"

func (tr *TestRunner) pendingSecretRepairAwaitedLocked() bool {
	if tr == nil {
		return false
	}
	for _, test := range tr.tests {
		if test == nil || !strings.EqualFold(strings.TrimSpace(test.Status), "PENDING") {
			continue
		}
		reason := strings.TrimSpace(test.PendingReason)
		if strings.HasPrefix(reason, pendingSecretRequirementsReasonPrefix) ||
			strings.HasPrefix(reason, pendingRetryableShakeoutEvidenceReasonPrefix) {
			return true
		}
	}
	return false
}

// deferPendingPromotionGateWithTerm retains a correctable admission failure in
// the durable queue. Missing values and resolver outages can be repaired by an
// operator/provider without rematerializing an immutable test snapshot; malformed
// script references take the terminal path above instead.
func (tr *TestRunner) deferPendingPromotionGateWithTerm(id, message string, leaseTerm executionLeaseTerm) error {
	if !tr.executionLeaseTermCurrent(leaseTerm) {
		return errNotExecutionLeader
	}
	reason := strings.TrimSpace(message)
	tr.mu.Lock()
	defer tr.mu.Unlock()
	test := tr.tests[id]
	if test == nil || test.Status != "PENDING" {
		return nil
	}
	if test.PendingReason == reason {
		return nil
	}
	return tr.markTestPendingLockedWithExpectedTerm(test, reason, &leaseTerm)
}

func (tr *TestRunner) recordSystemTestAudit(action string, status string, test *ManagedTest, details map[string]any) {
	if tr == nil || tr.scenarioStore == nil || test == nil {
		return
	}
	if lifecycleActivityAction(action) == "start" && test != nil {
		tr.recordTestStartActivity(test, testCreationActor{})
	}
	if normalized := lifecycleActivityAction(action); normalized != "" && normalized != "start" {
		tr.recordBackgroundActivity(&storage.ObjectActivityEvent{ID: uuid.NewString(), Action: normalized, Status: status, ActorType: "system", ActorSubject: "test-runner", ActorDisplayName: "Test Runner", InitiatedBy: test.CreatedBySubject, ProjectID: test.ProjectID, ResourceType: "test", ResourceID: test.ID, ResourceName: test.Name, CreatedAt: time.Now().UTC()})
	}
	if err := tr.scenarioStore.SaveAuditEvent(&storage.AuditEvent{
		ID:               uuid.New().String(),
		Action:           action,
		ActorType:        "system",
		ActorSubject:     "test-runner",
		ActorDisplayName: "Test Runner",
		ProjectID:        normalizeProjectID(test.ProjectID),
		ResourceType:     "test",
		ResourceID:       test.ID,
		Status:           status,
		Details:          details,
		CreatedAt:        time.Now().UTC(),
	}); err != nil {
		slog.Warn("failed to persist system test audit event", "test_id", test.ID, "action", action, "error", err)
	}
}

func (tr *TestRunner) nextPromotablePendingTestExcludingWithTimeslotsLocked(excluded map[string]struct{}, now time.Time, activeSlots []*storage.CapacityReservation, reservationErr error, projectQuotas map[*ManagedTest]projectQuotaEvidence) *ManagedTest {
	pending := make([]*ManagedTest, 0)
	for _, test := range tr.tests {
		if test.Status == "PENDING" {
			if _, skip := excluded[test.ID]; skip {
				continue
			}
			pending = append(pending, test)
		}
	}
	if len(pending) == 0 {
		return nil
	}
	tr.sortPendingTestsForFairShareWithTimeslotsLocked(pending, now, activeSlots)
	for _, test := range pending {
		if tr.projectDeletionErrorLocked(test.ProjectID) != nil {
			continue
		}
		// Configuration conflicts must reach the terminal promotion gate even
		// when ordinary capacity selection would otherwise skip the run forever.
		if recoverySpareSettingsError(test, tr.settings) != nil {
			return test
		}
		tr.refreshCapacityAdvisoryLocked(test)
		projectQuota, quotaOK := projectQuotas[test].quotaFor(test)
		blocked := !quotaOK
		if quotaOK {
			blocked, _ = tr.evaluateQuotaBlockWithTimeslotsAndProjectQuotaLocked(test, "", now, activeSlots, reservationErr, projectQuota)
		}
		if blocked {
			continue
		}
		return test
	}
	return nil
}

func (tr *TestRunner) runScriptShakeoutReport(test *ManagedTest) (ScriptShakeoutReport, error) {
	report := ScriptShakeoutReport{
		Passed:    true,
		StartedAt: time.Now().UTC(),
		Results:   make([]ScriptShakeoutResult, 0, len(test.groupSpecs)),
	}
	tr.mu.RLock()
	operationCtx := test.executionCtx
	tr.mu.RUnlock()
	if operationCtx == nil {
		operationCtx = context.Background()
	}
	for index, group := range test.groupSpecs {
		if err := operationCtx.Err(); err != nil {
			report.EndedAt = time.Now().UTC()
			return report, err
		}
		result := ScriptShakeoutResult{
			GroupName: group.Name,
			Status:    "passed",
		}
		if group.Script == nil {
			result.Status = "failed"
			result.Error = "script is missing"
			report.Passed = false
			report.Results = append(report.Results, result)
			continue
		}
		result.ScriptID = group.Script.ID
		result.ScriptName = group.Script.Name

		shakeoutScript := *group.Script
		shakeoutScript.Config.RunLogic.Iterations = 1

		ctx, cancel := context.WithTimeout(operationCtx, 30*time.Second)
		projectID := shakeoutScript.ProjectID
		if strings.TrimSpace(projectID) == "" {
			projectID = test.ProjectID
		}
		resolver := tr.secretResolverForRunGroup(test, index, projectID)
		if index < len(test.Groups) {
			resolver = overlayRuntimeSecretResolver(test.Groups[index].runtimeSecretValues, resolver)
		}
		err := engine.RunScriptShakeoutWithRunIDAndSecretResolver(ctx, group.Name, &shakeoutScript, test.ID, resolver)
		cancel()
		if operationErr := operationCtx.Err(); operationErr != nil {
			report.EndedAt = time.Now().UTC()
			return report, operationErr
		}
		if err != nil {
			result.Status = "failed"
			result.Error = err.Error()
			report.Passed = false
		}
		report.Results = append(report.Results, result)
	}

	report.EndedAt = time.Now().UTC()
	if report.Passed {
		report.Summary = fmt.Sprintf("Script shakeout passed for %d group%s; full load can start.", len(report.Results), pluralSuffix(len(report.Results)))
	} else {
		report.Summary = "Script shakeout failed; full load test has not started. Review the evidence and choose Continue or Cancel."
	}
	return report, nil
}

func formatScriptShakeoutEvidence(report ScriptShakeoutReport) string {
	if report.Passed {
		return report.Summary
	}
	lines := []string{report.Summary}
	for _, result := range report.Results {
		if result.Status != "failed" {
			continue
		}
		label := result.GroupName
		if result.ScriptName != "" {
			label = fmt.Sprintf("%s (%s)", result.GroupName, result.ScriptName)
		}
		lines = append(lines, fmt.Sprintf("- %s: %s", label, result.Error))
	}
	return strings.Join(lines, "\n")
}
