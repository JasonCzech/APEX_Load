package api

// The run state machine: Start/Stop/Abort/Pause/Resume/AdjustDuration with
// startability + preflight gating, script-shakeout gating, run setup
// (runTest/buildRunEngine) and lifecycle (start/fail/complete), VUser scaling
// and scale-ramps, goal-target control, and pending-test promotion.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/api/testexec"
	"github.com/jasonczech/apexload/pkg/engine"
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
		select {
		case <-test.multiEngine.Ready():
			return true
		default:
			return false
		}
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
	return tr.startTestWithOptionsAndExpectedTerm(id, opts, nil)
}

func (tr *TestRunner) StartTestWithActor(id string, opts StartTestOptions, actor testCreationActor) error {
	opts.actor = actor
	return tr.startTestWithOptionsAndExpectedTerm(id, opts, nil)
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
	return tr.startTestWithOptionsAndExpectedTermCore(id, opts, expectedTerm)
}

func (tr *TestRunner) startTestWithOptionsAndExpectedTermCore(id string, opts StartTestOptions, expectedTerm *executionLeaseTerm) error {
	if err := tr.validateExpectedStartTerm(expectedTerm); err != nil {
		return err
	}
	if err := tr.validateCIRunPolicyForStart(id); err != nil {
		return err
	}
	tr.mu.Lock()
	id = tr.resolveRefKeyLocked(id)
	tr.mu.Unlock()
	if err := tr.refreshTestCapacityBindingFromStore(id); err != nil {
		return err
	}
	tr.mu.Lock()
	blocked, err := tr.preflightStartBlocksLockedWithExpectedTerm(id, opts, expectedTerm)
	tr.mu.Unlock()
	if err != nil {
		return err
	}
	if blocked {
		return nil
	}
	if err := tr.validateStartSecretRequirements(id); err != nil {
		return err
	}

	shakeoutTest, err := tr.beginScriptShakeoutIfNeededWithExpectedTerm(id, opts, expectedTerm)
	if err != nil {
		return err
	}
	if shakeoutTest != nil {
		report := tr.runScriptShakeoutReport(shakeoutTest)
		if !report.Passed {
			return tr.markScriptShakeoutReview(id, report)
		}
		if err := tr.markScriptShakeoutPassed(id, report); err != nil {
			return err
		}
	}
	// Shakeout can take tens of seconds. Revalidate the HA lease immediately
	// before the durable INITIALIZING transition so a stale controller cannot
	// overwrite takeover reconciliation.
	if err := tr.validateExpectedStartTerm(expectedTerm); err != nil {
		return err
	}
	if err := tr.validateCIRunPolicyForStart(id); err != nil {
		return err
	}
	if err := tr.refreshTestCapacityBindingFromStore(id); err != nil {
		return err
	}

	tr.mu.Lock()
	if err := tr.validateExpectedStartTerm(expectedTerm); err != nil {
		tr.mu.Unlock()
		return err
	}
	test, err := tr.prepareTestStartLockedWithExpectedTerm(id, opts, expectedTerm)
	tr.mu.Unlock()
	if err != nil {
		return err
	}
	if test == nil {
		return nil
	}

	go tr.runTest(test)
	return nil
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
// dispatch, or generator assignment. Engine construction repeats the check as
// defense against a value being removed between preflight and launch.
func (tr *TestRunner) validateStartSecretRequirements(id string) error {
	tr.mu.RLock()
	test := tr.tests[id]
	if test == nil {
		tr.mu.RUnlock()
		return fmt.Errorf("test not found: %s", id)
	}
	defaultProjectID := test.ProjectID
	groups := testexec.CloneGroupSpecsWithResultSink(test.groupSpecs, nil)
	runtimeSecrets := make([]map[string]string, len(test.Groups))
	for index := range groups {
		groups[index].Script = cloneScriptDefinition(groups[index].Script)
		if index < len(test.Groups) {
			runtimeSecrets[index] = cloneRuntimeSecretValues(test.Groups[index].runtimeSecretValues)
		}
	}
	tr.mu.RUnlock()

	projectResolvers := make(map[string]engine.SecretResolver)
	resolutionCtx, resolutionCancel := engine.WithSecretResolutionBudget(context.Background())
	defer resolutionCancel()
	for index, group := range groups {
		if group.Script == nil {
			continue
		}
		names, err := engine.ReferencedSecretNames(group.Script)
		if err != nil {
			return &startSecretRequirementError{
				message: fmt.Sprintf("group[%d] %q: inspect project secret references", index, group.Name),
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
		resolver := projectResolvers[projectID]
		if resolver == nil {
			resolver = tr.secretResolverForProject(projectID)
			projectResolvers[projectID] = resolver
		}
		if index < len(runtimeSecrets) {
			resolver = overlayRuntimeSecretResolver(runtimeSecrets[index], resolver)
		}
		_, missing, err := engine.ResolveSecretNames(resolutionCtx, names, resolver)
		if err != nil {
			return &startSecretRequirementError{
				message:   fmt.Sprintf("group[%d] %q: project secret resolution failed: %v", index, group.Name, err),
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
				message:   fmt.Sprintf("group %q: configure the following project secret(s) before starting the test: %s", groupName, strings.Join(missing, ", ")),
				retryable: true,
			}
		}
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
	previousStatus := test.Status
	previousPendingSince := cloneTimePtr(test.PendingSince)
	previousPendingReason := test.PendingReason
	previousQueuePosition := test.QueuePosition
	previousError := test.Error
	now := time.Now()
	test.Status = "PENDING"
	if test.PendingSince == nil || test.PendingSince.IsZero() {
		test.PendingSince = &now
	}
	test.PendingReason = reason
	test.QueuePosition = 0
	test.Error = ""
	if err := tr.saveManagedTestRecord(test); err != nil {
		test.Status = previousStatus
		test.PendingSince = previousPendingSince
		test.PendingReason = previousPendingReason
		test.QueuePosition = previousQueuePosition
		test.Error = previousError
		return fmt.Errorf("persist pending test state: %w", err)
	}
	tr.broadcastEvent("test_pending", test)
	return nil
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
	var err error
	if expectedTerm == nil {
		err = tr.claimTestExecutionFence(test)
	} else {
		err = tr.claimTestExecutionFenceWithTerm(test, *expectedTerm)
	}
	if err != nil {
		return err
	}
	if expectedTerm != nil && expectedTerm.valid() {
		test.executionGeneration = expectedTerm.generation
	}
	test.executionCtx, test.cancel = tr.executionOperationContext(context.Background())
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
	if authorization.CapacityShortfall == nil && authorization.ScriptShakeout == nil {
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
	if conflicts := tr.javaVuserRunnerLaunchConflictsForManagedTestLocked(test); len(conflicts) > 0 {
		conflict := conflicts[0]
		return false, fmt.Errorf("%s: %s", conflict.Code, conflict.Message)
	}
	// The Java runner gate above checks one group at a time. Project placement
	// across every group as a second step so two individually-valid groups
	// cannot both claim the same advertised JVM process slots. Re-run this at
	// StartTest because generator occupancy may have changed since preflight.
	if groupsUseJavaVuserProtocol(test.Groups) {
		if conflicts := tr.generatorPlacementLaunchConflictsLocked(test); len(conflicts) > 0 {
			conflict := conflicts[0]
			return false, fmt.Errorf("%s: %s", conflict.Code, conflict.Message)
		}
	}
	if !opts.SkipLaunchWindowPreflight {
		if err := tr.launchStartWindowErrorLocked(test, time.Now().UTC(), id, ""); err != nil {
			return false, err
		}
	}
	// Re-validate a bound capacity timeslot at the actual launch instant even when
	// the launch-window preflight is skipped: the binding can be invalidated
	// (rejected/re-bound/resized) between create and start, and a scheduled run's
	// window can drift, so the bind-time fit can no longer hold.
	if err := tr.launchTimeslotRevalidationErrorLocked(test, time.Now().UTC()); err != nil {
		return false, err
	}
	if blocked, reason := tr.evaluateQuotaBlockLocked(test, ""); blocked {
		authorizationSnapshot := cloneQueuedStartAuthorization(test.StartAuthorization)
		actorSnapshot, actorChanged := applyTestStartedByActor(test, opts.actor)
		applyRequestedStartAuthorizations(test, opts, time.Now().UTC())
		if err := tr.markTestPendingLocked(test, reason); err != nil {
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

func (tr *TestRunner) preflightStartBlocksLocked(id string, opts StartTestOptions) (bool, error) {
	return tr.preflightStartBlocksLockedWithExpectedTerm(id, opts, nil)
}

func (tr *TestRunner) preflightStartBlocksLockedWithExpectedTerm(id string, opts StartTestOptions, expectedTerm *executionLeaseTerm) (bool, error) {
	if err := tr.shutdownErrorLocked(); err != nil {
		return false, err
	}
	test, ok := tr.tests[id]
	if !ok {
		return false, fmt.Errorf("test not found: %s", id)
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
	// Starting is the ownership boundary for a durable queued/scheduled row that
	// may have been loaded after failover. Claim the current epoch explicitly;
	// lower-level persistence never adopts an empty fence on a caller's behalf.
	var claimErr error
	if expectedTerm == nil {
		claimErr = tr.claimTestExecutionFence(test)
	} else {
		claimErr = tr.claimTestExecutionFenceWithTerm(test, *expectedTerm)
	}
	if claimErr != nil {
		return false, claimErr
	}
	return tr.evaluateStartGatesLocked(id, test, opts)
}

func (tr *TestRunner) beginScriptShakeoutIfNeeded(id string, opts StartTestOptions) (*ManagedTest, error) {
	return tr.beginScriptShakeoutIfNeededWithExpectedTerm(id, opts, nil)
}

func (tr *TestRunner) beginScriptShakeoutIfNeededWithExpectedTerm(id string, opts StartTestOptions, expectedTerm *executionLeaseTerm) (*ManagedTest, error) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if err := tr.shutdownErrorLocked(); err != nil {
		return nil, err
	}

	test, ok := tr.tests[id]
	if !ok {
		return nil, fmt.Errorf("test not found: %s", id)
	}
	if !test.ScriptShakeout {
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
	var claimErr error
	if expectedTerm == nil {
		claimErr = tr.claimTestExecutionFence(test)
	} else {
		claimErr = tr.claimTestExecutionFenceWithTerm(test, *expectedTerm)
	}
	if claimErr != nil {
		return nil, claimErr
	}

	previousStatus := test.Status
	previousPendingSince := cloneTimePtr(test.PendingSince)
	previousPendingReason := test.PendingReason
	previousQueuePosition := test.QueuePosition
	previousError := test.Error
	actorSnapshot, actorChanged := applyTestStartedByActor(test, opts.actor)
	test.Status = "SHAKEOUT"
	test.PendingSince = nil
	test.PendingReason = ""
	test.QueuePosition = 0
	test.Error = ""
	if err := tr.saveManagedTestStartRecord(test); err != nil {
		test.Status = previousStatus
		test.PendingSince = previousPendingSince
		test.PendingReason = previousPendingReason
		test.QueuePosition = previousQueuePosition
		test.Error = previousError
		if actorChanged {
			restoreTestStartedByActor(test, actorSnapshot)
		}
		return nil, fmt.Errorf("persist script shakeout state: %w", err)
	}
	tr.broadcastEvent("script_shakeout_started", test)

	return test, nil
}

func (tr *TestRunner) markScriptShakeoutPassed(id string, _ ScriptShakeoutReport) error {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	test, ok := tr.tests[id]
	if !ok {
		return fmt.Errorf("test not found: %s", id)
	}
	if test.Status != "SHAKEOUT" {
		return fmt.Errorf("test is %s, shakeout result no longer applies", test.Status)
	}
	previousStatus := test.Status
	previousError := test.Error
	test.Status = "QUEUED"
	test.Error = ""
	if err := tr.saveManagedTestRecord(test); err != nil {
		test.Status = previousStatus
		test.Error = previousError
		return fmt.Errorf("persist successful script shakeout: %w", err)
	}
	tr.broadcastEvent("script_shakeout_passed", test)
	return nil
}

func (tr *TestRunner) markScriptShakeoutReview(id string, report ScriptShakeoutReport) error {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	test, ok := tr.tests[id]
	if !ok {
		return fmt.Errorf("test not found: %s", id)
	}
	if test.Status != "SHAKEOUT" {
		return fmt.Errorf("test is %s, shakeout review no longer applies", test.Status)
	}
	previousStatus := test.Status
	previousError := test.Error
	test.Status = "SHAKEOUT_REVIEW"
	test.Error = formatScriptShakeoutEvidence(report)
	if err := tr.saveManagedTestRecord(test); err != nil {
		test.Status = previousStatus
		test.Error = previousError
		return fmt.Errorf("persist script shakeout review: %w", err)
	}
	tr.broadcastEvent("script_shakeout_failed", test)
	return &ScriptShakeoutGateError{TestID: id, Report: &report}
}

func (tr *TestRunner) prepareTestStartLocked(id string, opts StartTestOptions) (*ManagedTest, error) {
	return tr.prepareTestStartLockedWithExpectedTerm(id, opts, nil)
}

func (tr *TestRunner) prepareTestStartLockedWithExpectedTerm(id string, opts StartTestOptions, expectedTerm *executionLeaseTerm) (*ManagedTest, error) {
	if err := tr.shutdownErrorLocked(); err != nil {
		return nil, err
	}
	test, ok := tr.tests[id]
	if !ok {
		return nil, fmt.Errorf("test not found: %s", id)
	}
	if !testexec.IsStartableTestStatus(test.Status) {
		return nil, testexec.StartableTestStatusError(test.Status)
	}
	if err := tr.projectDeletionErrorLocked(test.ProjectID); err != nil {
		return nil, err
	}

	if blocked, err := tr.evaluateStartGatesLocked(id, test, opts); err != nil {
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
	if err := tr.saveManagedTestStartRecord(test); err != nil {
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
		test.StartAuthorization = previousStartAuthorization
		if actorChanged {
			restoreTestStartedByActor(test, actorSnapshot)
		}
		return nil, fmt.Errorf("persist initializing test state: %w", err)
	}
	if previousCancel != nil {
		previousCancel()
	}
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
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.refreshPendingQueueMetadataLocked()
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
	tr.mu.Lock()
	tr.refreshPendingQueueMetadataLocked()
	tr.mu.Unlock()
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
	if test.remoteFinalizing {
		// The distributed completion path owns remote teardown once finalization
		// starts. Treat a concurrent stop as already in progress so it cannot
		// overlap the finalizer's StopTest fan-out.
		tr.mu.Unlock()
		return nil
	}
	status := test.Status
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
		tr.mu.Unlock()
		if err := tr.syncScenarioScheduleLifecycleForTest(test.ID); err != nil {
			slog.Warn("aborted test transition could not refresh scenario occurrence cache", "test_id", test.ID, "error", err)
		}
		releaseErr := tr.releaseCapacityReservationForTerminalStatus(test.ID, "ABORTED")
		if releaseErr != nil && slotID != "" {
			tr.enqueueCapacityReservationReleaseRetry(test.ID, slotID)
		}
		tr.broadcastEvent("test_aborted", test)
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
		if actorChanged {
			restoreTestStoppedByActor(test, actorSnapshot)
		}
		tr.mu.Unlock()
		return fmt.Errorf("test is %s and cannot be stopped", status)
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
				tr.broadcastEvent("test_stopping", test)
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
	stillStopping := test.Status == "STOPPING"
	tr.mu.RUnlock()
	if stillStopping {
		tr.broadcastEvent("test_stopping", test)
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
		if !durableBackgroundRetryable(err) {
			break
		}
		delay = min(delay*2, executionPlanRetryMaxDelay)
	}

	tr.terminalRetryMu.Lock()
	delete(tr.stopTeardownRetries, testID)
	delete(tr.terminalRetrying, retryKey)
	tr.terminalRetryMu.Unlock()
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
	if !tr.isExecutionLeader() {
		return errNotExecutionLeader
	}
	if opts.RampDuration <= 0 {
		return tr.scaleVUsersNow(id, groupName, targetVUsers, true)
	}
	return tr.scheduleVUserScaleRamp(id, groupName, targetVUsers, opts.RampDuration)
}

func (tr *TestRunner) UpdateGoalTargetTPS(id string, groupName string, targetTPS float64) (*GoalTargetUpdate, error) {
	if !tr.isExecutionLeader() {
		return nil, errNotExecutionLeader
	}
	if strings.TrimSpace(groupName) == "" {
		return nil, fmt.Errorf("group name is required")
	}
	if targetTPS <= 0 || math.IsNaN(targetTPS) || math.IsInf(targetTPS, 0) {
		return nil, fmt.Errorf("target_tps must be greater than zero")
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
	if totalTargetTPS <= 0 {
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

	tr.hub.BroadcastJSON(map[string]any{
		"type":               "goal_target_changed",
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
	if _, err := tr.vuserScaleProposalLocked(test, groupName, targetVUsers); err != nil {
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

	delta := targetVUsers - currentVUsers
	steps := testexec.AbsInt(delta)
	if steps == 0 {
		return
	}
	direction := 1
	if delta < 0 {
		direction = -1
	}
	interval := rampDuration / time.Duration(steps)
	if interval <= 0 {
		interval = time.Nanosecond
	}

	for step := 1; step <= steps; step++ {
		if !tr.waitTestActiveDuration(ctx, id, interval) {
			result = ctx.Err()
			if result == nil {
				result = context.Canceled
			}
			return
		}

		nextTarget := currentVUsers + direction*step
		for {
			err := tr.scaleVUsersNow(id, groupName, nextTarget, false)
			if err == nil {
				break
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
	tr.mu.RUnlock()

	for _, group := range groups {
		group := group
		go tr.runStageSchedule(ctx, testID, group.Name, group.Stages, mge)
	}
}

func (tr *TestRunner) runStageSchedule(ctx context.Context, testID string, groupName string, stages []storage.LoadStage, mge *engine.MultiGroupEngine) {
	if mge != nil {
		select {
		case <-ctx.Done():
			return
		case <-mge.Ready():
		}
	}
	for index, stage := range stages {
		stageType := strings.ToLower(strings.TrimSpace(stage.Type))
		duration, err := time.ParseDuration(stage.Duration)
		if err != nil || duration <= 0 {
			slog.Warn("stage schedule stopped on invalid stage duration", "test_id", testID, "group", groupName, "stage", index, "duration", stage.Duration, "error", err)
			return
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
			if ramp == nil {
				if !tr.waitTestActiveDuration(ctx, testID, duration) {
					return
				}
				continue
			}
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
			continue
		case "hold":
			// The engine is already at the hold target: first holds are seeded by
			// compilation, later holds inherit the preceding ramp target.
		default:
			slog.Warn("stage schedule stopped on invalid stage type", "test_id", testID, "group", groupName, "stage", index, "type", stage.Type)
			return
		}
		if !tr.waitTestActiveDuration(ctx, testID, duration) {
			return
		}
	}
}

func (tr *TestRunner) scaleVUsersNow(id string, groupName string, targetVUsers int, cancelExistingRamp bool) error {
	tr.mu.RLock()
	resolvedID := tr.resolveRefKeyLocked(id)
	tr.mu.RUnlock()
	unlockControl := tr.lockDistributedGroupControl(resolvedID, groupName)
	defer unlockControl()
	if !tr.isExecutionLeader() {
		return errNotExecutionLeader
	}

	tr.mu.Lock()
	id = tr.resolveRefKeyLocked(id)
	reservation, err := tr.reserveVUserScaleLocked(id, groupName, targetVUsers)
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
	tr.mu.Unlock()

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
	tr.broadcastEvent("test_stopping", test)
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

// vuserScaleProposalLocked builds and validates the exact post-scale state.
// Caller holds tr.mu. It is the shared admission chokepoint for immediate API
// scales, gradual-ramp steps, stage transitions, and autonomous goal scaling.
func (tr *TestRunner) vuserScaleProposalLocked(test *ManagedTest, groupName string, targetVUsers int) (*ManagedTest, error) {
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
	if !tr.testLoadReadyLocked(test, time.Now().UTC()) {
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
	if err := tr.enforceTestQuotaLocked(proposal.ProjectID, proposal.TotalVUsers, proposal.Duration, proposal.Status); err != nil {
		return nil, err
	}
	if blocked, reason := tr.evaluateQuotaBlockLocked(&proposal, test.ID); blocked {
		return nil, fmt.Errorf("VUser scale admission blocked: %s", reason)
	}
	if err := tr.launchTimeslotRevalidationErrorLocked(&proposal, time.Now().UTC()); err != nil {
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
	if err := tr.runtimeAdjustmentWindowErrorLocked(&proposal, time.Now().UTC()); err != nil {
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

func (tr *TestRunner) reserveVUserScaleLocked(id, groupName string, targetVUsers int) (*vuserScaleReservation, error) {
	test, ok := tr.tests[id]
	if !ok {
		return nil, fmt.Errorf("test not found: %s", id)
	}
	proposal, err := tr.vuserScaleProposalLocked(test, groupName, targetVUsers)
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
	if err := tr.saveManagedTestRecord(test); err != nil {
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
	if err := tr.saveManagedTestRecord(test); err != nil {
		test.Groups[reservation.groupIndex] = currentGroup
		test.TotalVUsers = currentTotal
		test.CapacityAdvisory = currentAdvisory
		return err
	}
	return nil
}

// runtimeAdjustmentWindowErrorLocked validates only the remaining runtime
// window. It avoids re-litigating capacity in the already elapsed portion of a
// run while still covering future schedules, reservations, maintenance and
// availability windows through the proposed end.
func (tr *TestRunner) runtimeAdjustmentWindowErrorLocked(test *ManagedTest, now time.Time) error {
	if test == nil {
		return nil
	}
	start := now.UTC()
	originalStart := calendarTestStart(test)
	if originalStart.IsZero() {
		originalStart = start
	}
	full := launchWindowForTest(test, originalStart.UTC())
	if !full.EndAt.After(start) {
		return nil
	}
	window := full
	window.StartAt = start
	window.DurationSeconds = int(window.EndAt.Sub(window.StartAt).Seconds())
	conflicts := tr.launchWindowConflictsLocked(test, window, start, test.ID, "")
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
		return multiEngine.Pause()
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
	if err := tr.enforceTestQuotaLocked(proposal.ProjectID, proposal.TotalVUsers, proposal.Duration, proposal.Status); err != nil {
		tr.mu.Unlock()
		return err
	}
	if err := tr.launchTimeslotRevalidationErrorLocked(&proposal, time.Now().UTC()); err != nil {
		tr.mu.Unlock()
		return err
	}
	if test.CapacityAdvisory != nil {
		if err := buildStartGovernanceError(test.ID, proposal.CapacityAdvisory, StartTestOptions{}); err != nil {
			tr.mu.Unlock()
			return err
		}
	}
	if err := tr.runtimeAdjustmentWindowErrorLocked(&proposal, time.Now().UTC()); err != nil {
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
	tr.mu.Unlock()

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
	tr.broadcastEvent("test_stopping", test)
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
	if status != "RUNNING" {
		return fmt.Errorf("test is %s, must be RUNNING to pause", status)
	}
	if activationErr != nil {
		return activationErr
	}
	if err := tr.applyPause(test, multiEngine, backend); err != nil {
		var uncertain *distributedPauseUncertainError
		if errors.As(err, &uncertain) {
			return tr.failClosedUncertainPauseControl(test, "RUNNING", backend, err)
		}
		return err
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
	pausedAt := time.Now().UTC()
	test.Status = "PAUSED"
	test.PausedAt = &pausedAt
	if err := tr.saveManagedTestRecord(test); err != nil {
		test.Status = "RUNNING"
		test.PausedAt = nil
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
	tr.mu.Unlock()

	tr.broadcastEvent("test_paused", test)

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
	if status != "PAUSED" {
		tr.mu.Unlock()
		return fmt.Errorf("test is %s, must be PAUSED to resume", status)
	}
	if err := tr.distributedActivationErrorLocked(test); err != nil {
		tr.mu.Unlock()
		return err
	}
	now := time.Now().UTC()
	pausedAt := now
	if test.PausedAt != nil && !test.PausedAt.IsZero() {
		pausedAt = test.PausedAt.UTC()
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
	if err := tr.launchTimeslotRevalidationErrorLocked(&proposal, now); err != nil {
		tr.mu.Unlock()
		return err
	}
	if test.CapacityAdvisory != nil {
		if err := buildStartGovernanceError(test.ID, proposal.CapacityAdvisory, StartTestOptions{}); err != nil {
			tr.mu.Unlock()
			return err
		}
	}
	if blocked, reason := tr.evaluateQuotaBlockLocked(&proposal, test.ID); blocked {
		tr.mu.Unlock()
		return fmt.Errorf("resume admission blocked: %s", reason)
	}
	if err := tr.runtimeAdjustmentWindowErrorLocked(&proposal, now); err != nil {
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
	test.PausedAt = nil
	test.CumulativePause = proposal.CumulativePause
	if err := tr.saveManagedTestRecord(test); err != nil {
		test.Status = "PAUSED"
		test.PausedAt = previousPausedAt
		test.CumulativePause = previousCumulativePause
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
	// Pause publishes an immediate zero-VUser snapshot. Restore the configured
	// live counts as part of the durable resume commit instead of leaving the API
	// and dashboard at zero until the next generator/engine metrics interval.
	if test.LiveMetrics != nil {
		updated := cloneLiveMetrics(test.LiveMetrics)
		activeVUsers := 0
		for _, configuredGroup := range test.Groups {
			vusers := groupCurrentVUsers(configuredGroup)
			activeVUsers += vusers
			if groupMetrics := updated.GroupMetrics[configuredGroup.Name]; groupMetrics != nil {
				groupMetrics.VUsers = vusers
			}
		}
		updated.ActiveVUsers = int64(activeVUsers)
		if updated.GoalStatus != nil && test.goalControl != nil {
			updated.GoalStatus.LastScaleAt = cloneTimePtr(test.goalControl.lastScaleAt)
		}
		replaceLiveMetricsLocked(test, updated)
	}
	tr.mu.Unlock()

	tr.broadcastEvent("test_resumed", test)

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
	test.Error = "remote pause/resume state is uncertain: " + controlErr.Error()
	persistErr := retryDurableTransition(func() error { return tr.saveManagedTestRecord(test) })
	tr.cancelAllVUserScaleRampsLocked(test)
	cancel := test.cancel
	tr.mu.Unlock()

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
	tr.broadcastEvent("test_stopping", test)
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
	defer func() {
		tr.annotateTestLifecycleSpan(span, test.ID, nil)
		span.End()
	}()
	tr.mu.Lock()
	if tr.tests[test.ID] == test && test.executionCtx != nil {
		test.executionCtx = ctx
	}
	tr.mu.Unlock()
	backend.Run(tr, test)
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
	tr.mu.RLock()
	current := tr.tests[test.ID]
	startable := current == test && test.executionGeneration == leaseTerm.generation &&
		test.executionCtx != nil && test.executionCtx.Err() == nil
	tr.mu.RUnlock()
	if !startable || !tr.executionLeaseTermCurrent(leaseTerm) {
		return
	}
	tr.runTest(test)
}

func (tr *TestRunner) buildRunEngine(test *ManagedTest, resultSink engine.ResultSink) (*engine.MultiGroupEngine, error) {
	if err := validateManagedTestWorkloadBounds(test); err != nil {
		return nil, err
	}
	groups := testexec.CloneGroupSpecsWithResultSink(test.groupSpecs, resultSink)
	if err := validateEngineGroupBounds(groups); err != nil {
		return nil, err
	}
	projectResolvers := make(map[string]engine.SecretResolver)
	for index := range groups {
		groups[index].ScenarioID = strings.TrimSpace(test.ScenarioID)
		projectID := test.ProjectID
		if groups[index].Script != nil && strings.TrimSpace(groups[index].Script.ProjectID) != "" {
			projectID = groups[index].Script.ProjectID
		}
		resolver := projectResolvers[projectID]
		if resolver == nil {
			resolver = engine.CacheSecretResolverForRun(tr.secretResolverForProject(projectID))
			projectResolvers[projectID] = resolver
		}
		if index < len(test.Groups) {
			resolver = overlayRuntimeSecretResolver(test.Groups[index].runtimeSecretValues, resolver)
		}
		groups[index].SecretResolver = resolver
	}
	preBaseline, postBaseline := testexec.ResourceBaselineWindows(test.ResourceBaseline)
	preBaseline += testexec.StartDelayWindow(test.StartDelay)
	return engine.NewMultiGroup(engine.MultiGroupConfig{
		Groups:       groups,
		Duration:     test.Duration,
		PreBaseline:  preBaseline,
		PostBaseline: postBaseline,
	})
}

func applyGoalControllerUpdate(tr *TestRunner, test *ManagedTest, live *LiveMetrics, groupVUsers map[string]int, update goalControllerUpdate, scaleVUsers func(groupName string, target int) error) {
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

	targets := distributeTargetVUsersForGoal(groups, groupVUsers, update.TargetTotal, targetStatuses)
	scaledAny := false
	scaleFailureReason := ""
	for _, group := range groups {
		target, exists := targets[group.Name]
		if !exists || groupVUsers[group.Name] == target {
			continue
		}
		// Production goal scaling must pass through the same atomic admission and
		// durable accounting reservation as an operator request. The callback is
		// retained only for isolated unit tests whose runner does not own `test`.
		tr.mu.RLock()
		managed := tr.tests != nil && tr.tests[test.ID] == test
		tr.mu.RUnlock()
		var err error
		if managed {
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
		scaledAny = true
	}

	now := live.Timestamp
	tr.mu.Lock()
	// Keep the durable/controller-side group configuration aligned with every
	// successfully applied backend scale. This is also required for standalone
	// controller tests and extension backends where scaleVUsersNow is not the
	// callback performing the mutation.
	for i := range test.Groups {
		if current, ok := groupVUsers[test.Groups[i].Name]; ok {
			test.Groups[i].VUsers = current
		}
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
	test.multiEngine = mge
	test.StartedAt = &now
	test.EndedAt = nil
	test.Status = "RUNNING"
	test.PendingSince = nil
	test.PendingReason = ""
	test.QueuePosition = 0
	test.Error = ""
	test.goalControl = &goalControllerState{}
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
	tr.mu.Unlock()
	releaseErr := tr.releaseCapacityReservationForTerminalStatus(test.ID, "FAILED")
	if releaseErr != nil && test.TimeslotID != nil {
		tr.enqueueCapacityReservationReleaseRetry(test.ID, strings.TrimSpace(*test.TimeslotID))
	}
	tr.broadcastEvent("test_failed", test)
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
		tr.mu.Unlock()
		return nil
	}
	if err := tr.persistStoredTestStatus(test.ID, test.executionFence, finalStatus, &endTime, finalError); err != nil {
		tr.mu.Unlock()
		if isStoredTestTransitionCancelled(err) {
			// Deletion is a durable tombstone. Treat it as cancellation of this stale
			// completion, not as a successful terminal commit.
			return nil
		}
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
	clearDistributedRunSecretSnapshotsLocked(test)
	test.Archive = archiveRecord
	if archiveRecord != nil {
		tr.archives[test.ID] = archiveRecord
	}
	terminalSnapshot := buildRunLiveStateSnapshotLocked(test, endTime)
	tr.mu.Unlock()
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
		tr.broadcastEvent("test_failed", test)
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
		tr.broadcastEvent("test_aborted", test)
	} else {
		tr.broadcastEvent("test_complete", test)
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
		slog.Warn("pending test promotion failed", "error", err)
	}
}

// promotePendingTestsWithTerm promotes fair-share queue entries under one
// immutable execution lease term. A caller that crossed another durable
// side-effect boundary (for example execution-plan materialization) must retain
// its original term here so an old goroutine cannot adopt leadership after the
// same process reacquires a newer generation.
func (tr *TestRunner) promotePendingTestsWithTerm(leaseTerm executionLeaseTerm) error {
	deferredPending := make(map[string]struct{})
	for {
		if !tr.executionLeaseTermCurrent(leaseTerm) {
			return errNotExecutionLeader
		}
		tr.mu.Lock()
		if tr.shuttingDown {
			tr.mu.Unlock()
			return nil
		}
		pending := tr.nextPromotablePendingTestExcludingLocked(deferredPending)
		if pending == nil {
			tr.mu.Unlock()
			return nil
		}
		pendingID := pending.ID
		tr.mu.Unlock()
		if policyErr := tr.validateCIRunPolicyForStart(pendingID); policyErr != nil {
			message := "controller refused pending CI run before execution: " + policyErr.Error()
			if terminalErr := tr.failCIRunBeforeExecutionDuringPromotion(pendingID, message); terminalErr != nil {
				return terminalErr
			}
			continue
		}
		// The original start attempt can enter PENDING at the quota gate before
		// static project secrets are checked. Resolve them at the promotion
		// boundary so a queued run cannot bypass the ordinary StartTest preflight,
		// including when a formerly valid secret was removed while it waited.
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
		tr.mu.Lock()
		pending = tr.tests[pendingID]
		if pending == nil || pending.Status != "PENDING" {
			tr.mu.Unlock()
			continue
		}
		// Queue promotion is the ownership boundary for pending rows recovered by
		// a successor controller. Fail closed before any shakeout/status write.
		if err := tr.claimTestExecutionFenceWithTerm(pending, leaseTerm); err != nil {
			tr.mu.Unlock()
			slog.Warn("pending test promotion could not claim execution epoch", "test_id", pending.ID, "error", err)
			return err
		}
		if pending.ScriptShakeout && !startOptionsWithAuthorization(pending, StartTestOptions{}).OverrideScriptShakeoutError {
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
			if err := tr.saveManagedTestStartRecord(pending); err != nil {
				pending.Status = previousStatus
				pending.PendingSince = previousPendingSince
				pending.PendingReason = previousPendingReason
				pending.QueuePosition = previousQueuePosition
				pending.Error = previousError
				tr.mu.Unlock()
				slog.Warn("pending test promotion could not persist shakeout", "test_id", pending.ID, "error", err)
				return err
			}
			tr.broadcastEvent("script_shakeout_started", pending)
			tr.mu.Unlock()

			report := tr.runScriptShakeoutReport(pending)
			if !report.Passed {
				if err := tr.markScriptShakeoutReview(pending.ID, report); err != nil {
					var gateErr *ScriptShakeoutGateError
					if !errors.As(err, &gateErr) {
						slog.Warn("pending test promotion could not persist shakeout review", "test_id", pending.ID, "error", err)
					}
				}
				tr.recordSystemTestAudit("test.script_shakeout.review", "blocked", pending, map[string]any{"report": report})
				continue
			}

			tr.mu.Lock()
			current, ok := tr.tests[pending.ID]
			if !ok || current.Status != "SHAKEOUT" || !tr.executionLeaseTermCurrent(leaseTerm) {
				tr.mu.Unlock()
				return errNotExecutionLeader
			}
			pending = current
			// Return to a durable retryable state before attempting INITIALIZING.
			// If that second transition cannot be saved, a later reconciliation can
			// safely rerun the deterministic shakeout instead of stranding SHAKEOUT.
			shakeoutStatus := pending.Status
			shakeoutError := pending.Error
			now := time.Now()
			pending.Status = "PENDING"
			pending.PendingSince = &now
			pending.PendingReason = "awaiting capacity after script shakeout"
			pending.QueuePosition = 0
			pending.Error = ""
			if err := tr.saveManagedTestRecord(pending); err != nil {
				pending.Status = shakeoutStatus
				pending.PendingSince = nil
				pending.PendingReason = ""
				pending.Error = shakeoutError
				tr.mu.Unlock()
				slog.Warn("pending test promotion could not persist successful shakeout", "test_id", pending.ID, "error", err)
				return err
			}
			tr.broadcastEvent("script_shakeout_passed", pending)
			tr.mu.Unlock()
			if policyErr := tr.validateCIRunPolicyForStart(pendingID); policyErr != nil {
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
		// seconds. Re-resolve secrets after it completes (and immediately before
		// final admission) so rotation/removal during shakeout cannot pass on the
		// strength of the earlier preflight. The resolver may perform remote I/O,
		// so it must never run while holding the runner mutex.
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
			return fmt.Errorf("resolve pending test source schedule: %w", sourceScheduleErr)
		}
		tr.mu.Lock()
		pending = tr.tests[pendingID]
		if pending == nil || pending.Status != "PENDING" {
			tr.mu.Unlock()
			continue
		}
		if !tr.executionLeaseTermCurrent(leaseTerm) {
			tr.mu.Unlock()
			return errNotExecutionLeader
		}
		if err := tr.claimTestExecutionFenceWithTerm(pending, leaseTerm); err != nil {
			tr.mu.Unlock()
			slog.Warn("pending test promotion lost execution leadership after secret revalidation", "test_id", pending.ID, "error", err)
			return err
		}
		// Candidate selection above is deliberately optimistic: CI policy and an
		// optional shakeout/secret check run without tr.mu and can take long enough
		// for another test to consume the last global/project/VUser slot. The same
		// delay can also move a scheduled run outside its launch window or invalidate
		// its bound capacity timeslot. Re-run every time-sensitive gate while holding
		// tr.mu immediately before INITIALIZING so the admission decision and the
		// capacity-reserving state transition are one atomic controller operation.
		if !tr.executionLeaseTermCurrent(leaseTerm) {
			tr.mu.Unlock()
			return errNotExecutionLeader
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
		if conflicts := tr.javaVuserRunnerLaunchConflictsForManagedTestLocked(pending); len(conflicts) > 0 {
			conflict := conflicts[0]
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
		if groupsUseJavaVuserProtocol(pending.Groups) {
			if conflicts := tr.generatorPlacementLaunchConflictsLocked(pending); len(conflicts) > 0 {
				conflict := conflicts[0]
				tr.mu.Unlock()
				message := "pending test promotion Java Vuser placement gate failed: " + conflict.Code + ": " + conflict.Message
				if deferErr := tr.deferPendingPromotionGateWithTerm(pendingID, message, leaseTerm); deferErr != nil {
					return deferErr
				}
				deferredPending[pendingID] = struct{}{}
				continue
			}
		}
		if blocked, reason := tr.evaluateQuotaBlockLocked(pending, ""); blocked {
			// Quota pressure is transient. Keep the row durably queued with the
			// current reason; a later completion/reconciliation will retry it.
			if pending.PendingReason != reason {
				if err := tr.markTestPendingLocked(pending, reason); err != nil {
					tr.mu.Unlock()
					return err
				}
			}
			tr.mu.Unlock()
			continue
		}
		now := time.Now().UTC()
		// A queued scenario occurrence is already represented by its parent
		// recurring schedule in the projected launch window. Exclude that source
		// schedule while revalidating the occurrence so it cannot conflict with
		// itself after transient quota pressure clears.
		if err := tr.launchStartWindowErrorLocked(pending, now, pending.ID, sourceScheduleID); err != nil {
			tr.mu.Unlock()
			if terminalErr := tr.failPendingPromotionGateWithTerm(pendingID, "pending test promotion launch window is no longer valid: "+err.Error(), leaseTerm); terminalErr != nil {
				return terminalErr
			}
			continue
		}
		if err := tr.launchTimeslotRevalidationErrorLocked(pending, now); err != nil {
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
		if err := tr.markTestInitializingLockedWithExpectedTerm(pending, &leaseTerm); err != nil {
			tr.mu.Unlock()
			slog.Warn("pending test promotion lost execution leadership", "test_id", pending.ID, "error", err)
			return err
		}
		if err := tr.saveManagedTestStartRecord(pending); err != nil {
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
			tr.mu.Unlock()
			slog.Warn("pending test promotion could not persist initializing state", "test_id", pending.ID, "error", err)
			return err
		}
		if previousCancel != nil {
			previousCancel()
		}
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
	if !tr.executionLeaseTermCurrent(leaseTerm) {
		return errNotExecutionLeader
	}
	tr.mu.Lock()
	test := tr.tests[id]
	if test == nil || test.Status != "PENDING" {
		tr.mu.Unlock()
		return nil
	}
	if err := tr.claimTestExecutionFenceWithTerm(test, leaseTerm); err != nil {
		tr.mu.Unlock()
		return err
	}
	tr.mu.Unlock()
	return tr.completeTestRunWithPendingPromotion(test, "FAILED", time.Now().UTC(), strings.TrimSpace(message), nil, false)
}

const pendingSecretRequirementsReasonPrefix = "pending test promotion secret requirements are no longer satisfied:"
const pendingCapacityAuthorizationReasonPrefix = "pending test promotion requires a new capacity-shortfall approval:"

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
	if err := tr.claimTestExecutionFenceWithTerm(test, leaseTerm); err != nil {
		return err
	}
	if test.PendingReason == reason {
		return nil
	}
	return tr.markTestPendingLocked(test, reason)
}

func (tr *TestRunner) recordSystemTestAudit(action string, status string, test *ManagedTest, details map[string]any) {
	if tr == nil || tr.scenarioStore == nil || test == nil {
		return
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

func (tr *TestRunner) nextPromotablePendingTestLocked() *ManagedTest {
	return tr.nextPromotablePendingTestExcludingLocked(nil)
}

func (tr *TestRunner) nextPromotablePendingTestExcludingLocked(excluded map[string]struct{}) *ManagedTest {
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
	tr.sortPendingTestsForFairShareLocked(pending)
	for _, test := range pending {
		if tr.projectDeletionErrorLocked(test.ProjectID) != nil {
			continue
		}
		tr.refreshCapacityAdvisoryLocked(test)
		if blocked, _ := tr.evaluateQuotaBlockLocked(test, ""); blocked {
			continue
		}
		return test
	}
	return nil
}

func (tr *TestRunner) runScriptShakeoutReport(test *ManagedTest) ScriptShakeoutReport {
	report := ScriptShakeoutReport{
		Passed:    true,
		StartedAt: time.Now().UTC(),
		Results:   make([]ScriptShakeoutResult, 0, len(test.groupSpecs)),
	}
	projectResolvers := make(map[string]engine.SecretResolver)
	for index, group := range test.groupSpecs {
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

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		projectID := shakeoutScript.ProjectID
		if strings.TrimSpace(projectID) == "" {
			projectID = test.ProjectID
		}
		resolver := projectResolvers[projectID]
		if resolver == nil {
			resolver = tr.secretResolverForProject(projectID)
			projectResolvers[projectID] = resolver
		}
		if index < len(test.Groups) {
			resolver = overlayRuntimeSecretResolver(test.Groups[index].runtimeSecretValues, resolver)
		}
		err := engine.RunScriptShakeoutWithSecretResolver(ctx, group.Name, &shakeoutScript, resolver)
		cancel()
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
	return report
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
