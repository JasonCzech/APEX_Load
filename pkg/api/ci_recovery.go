package api

import (
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// recoverQueuedCIRuns closes the durable crash window between queued-test
// materialization and StartTest. Besides CI rows, it re-arms only ordinary rows
// that own an exact queued/start_retrying recurring-schedule occurrence; manual
// queued tests remain inert. It is called only at process startup or after
// acquiring execution leadership. The scan performs no provider calls, so a
// shared provider outage cannot turn acquisition into serial remote timeouts.
func (tr *TestRunner) recoverQueuedCIRuns() error {
	if tr == nil || !tr.holdsExecutionLeadership() {
		return nil
	}
	term, err := tr.captureExecutionLeaseTerm()
	if err != nil {
		return err
	}
	type recoveryCandidate struct {
		id       string
		identity queuedStartRecoveryIdentity
	}
	type scheduledCandidate struct {
		recoveryCandidate
		scenarioID string
		projectID  string
	}
	tr.mu.RLock()
	ciCandidates := make([]recoveryCandidate, 0)
	scheduled := make([]scheduledCandidate, 0)
	for id, test := range tr.tests {
		if !queuedCIStartupRecoveryEligible(test) {
			continue
		}
		if _, err := decodeCIRunMetadata(test.MaterializationKey); err == nil {
			if identity, ok := queuedStartRecoveryIdentityForTest(test); ok {
				ciCandidates = append(ciCandidates, recoveryCandidate{id: id, identity: identity})
			}
			continue
		}
		identity, ok := queuedStartRecoveryIdentityForTest(test)
		if !ok || strings.TrimSpace(test.ScenarioID) == "" {
			continue
		}
		scheduled = append(scheduled, scheduledCandidate{
			recoveryCandidate: recoveryCandidate{id: id, identity: identity}, scenarioID: strings.TrimSpace(test.ScenarioID),
			projectID: normalizeProjectID(test.ProjectID),
		})
	}
	tr.mu.RUnlock()
	for _, candidate := range ciCandidates {
		tr.enqueueQueuedCIRecoveryRetryForIdentityWithTerm(candidate.id, candidate.identity, term)
	}

	// Ordinary queued tests are never recovered merely because they exist. The
	// exact durable recurring occurrence is the authority that makes one row a
	// scheduler-owned retry rather than a manual queue entry. This bounded scan
	// performs one indexed lookup per nonterminal scenario test and revalidates
	// the captured incarnation before enqueueing it.
	occurrenceStore, ok := tr.scenarioStore.(scenarioScheduleOccurrenceByTestStore)
	if !ok || occurrenceStore == nil {
		return nil
	}
	var recoveryErr error
	for _, candidate := range scheduled {
		if !tr.executionLeaseTermCurrent(term) {
			return errNotExecutionLeader
		}
		occurrence, err := occurrenceStore.GetScenarioScheduleOccurrenceByTestID(candidate.id)
		if err != nil {
			recoveryErr = errors.Join(recoveryErr, fmt.Errorf("load queued scheduled occurrence for test %s: %w", candidate.id, err))
			continue
		}
		if occurrence == nil || occurrence.TestID != candidate.id ||
			!strings.EqualFold(strings.TrimSpace(occurrence.Status), "queued") ||
			!strings.EqualFold(strings.TrimSpace(occurrence.ReasonCode), "start_retrying") ||
			strings.TrimSpace(occurrence.ScenarioID) != candidate.scenarioID ||
			normalizeProjectID(occurrence.ProjectID) != candidate.projectID {
			continue
		}
		tr.enqueueQueuedCIRecoveryRetryForIdentityWithTerm(candidate.id, candidate.identity, term)
	}
	return recoveryErr
}

func (tr *TestRunner) recoverQueuedCIRun(id string) error {
	term, err := tr.captureExecutionLeaseTerm()
	if err != nil {
		return err
	}
	return tr.recoverQueuedCIRunWithTerm(id, term)
}

func (tr *TestRunner) recoverQueuedCIRunWithTerm(id string, term executionLeaseTerm) error {
	if tr == nil || !tr.executionLeaseTermCurrent(term) {
		return errNotExecutionLeader
	}
	tr.mu.RLock()
	id = tr.resolveRefKeyLocked(id)
	identity, ok := queuedStartRecoveryIdentityForTest(tr.tests[id])
	tr.mu.RUnlock()
	if !ok {
		return nil
	}
	return tr.recoverQueuedCIRunForIdentityWithTerm(id, identity, term)
}

func (tr *TestRunner) recoverQueuedCIRunForIdentityWithTerm(id string, identity queuedStartRecoveryIdentity, term executionLeaseTerm) error {
	if tr == nil || !tr.executionLeaseTermCurrent(term) {
		return errNotExecutionLeader
	}
	tr.mu.RLock()
	id = tr.resolveRefKeyLocked(id)
	test := tr.tests[id]
	if !identity.matches(test) || !queuedCIStartupRecoveryEligible(test) {
		tr.mu.RUnlock()
		return nil
	}
	metadata, err := decodeCIRunMetadata(test.MaterializationKey)
	hasPendingShakeoutOutcome := test.pendingScriptShakeoutOutcome != nil
	shakeoutReview := strings.EqualFold(strings.TrimSpace(test.Status), "SHAKEOUT_REVIEW")
	shakeoutReviewSummary := strings.TrimSpace(test.Error)
	tr.mu.RUnlock()
	if err != nil {
		return nil
	}
	if shakeoutReview {
		message := "controller recovered a queued CI script shakeout requiring operator review"
		if shakeoutReviewSummary != "" {
			message += ": " + shakeoutReviewSummary
		}
		// The one-shot shakeout already reached a durable semantic decision. A
		// restart must terminalize that decision, never replay external actions.
		return tr.failCIRunBeforeExecutionForIdentityWithTerm(id, message, true, &term, &identity)
	}
	if hasPendingShakeoutOutcome {
		if outcomeErr := tr.resumePendingScriptShakeoutOutcomeForIdentity(id, &term, &identity); outcomeErr != nil {
			if errors.Is(outcomeErr, errStartAdmissionEvidenceStale) {
				tr.enqueueQueuedCIRecoveryRetryForIdentityWithTerm(id, identity, term)
				return outcomeErr
			}
			message := "controller could not finalize queued CI script shakeout: " + outcomeErr.Error()
			if terminalErr := tr.failCIRunBeforeExecutionForIdentityWithTerm(id, message, true, &term, &identity); terminalErr != nil {
				return errors.Join(outcomeErr, terminalErr)
			}
			return nil
		}
		tr.mu.RLock()
		test = tr.tests[id]
		tr.mu.RUnlock()
		if !identity.matches(test) {
			return nil
		}
	}
	if metadata.PublishCheck {
		store := tr.githubCheckStore
		if store == nil {
			return fmt.Errorf("required GitHub Check storage is unavailable")
		}
		link, err := storedCIGitHubCheckForRun(store, test, metadata)
		if err != nil {
			if errors.Is(err, errCIGitHubCheckStoreRead) {
				tr.enqueueQueuedCIRecoveryRetryForIdentityWithTerm(id, identity, term)
				return errors.Join(errStartAdmissionEvidenceStale, fmt.Errorf("load required GitHub Check link: %w", err))
			}
			message := "controller refused queued CI run with invalid GitHub Check state: " + err.Error()
			if terminalErr := tr.failCIRunBeforeExecutionForIdentityWithTerm(id, message, true, &term, &identity); terminalErr != nil {
				return errors.Join(err, terminalErr)
			}
			return nil
		}
		if link == nil {
			// The Check outbox reconciler will call this method after it has
			// recovered the authoritative remote Check and durable link.
			return nil
		}
	}
	if err := tr.startTestWithOptionsAndExpectedTerm(id, StartTestOptions{expectedRecoveryIdentity: &identity}, &term); err != nil {
		if errors.Is(err, errNotExecutionLeader) || errors.Is(err, errRunnerShuttingDown) {
			return err
		}
		// A deterministic CI ID may have been deleted and recreated while the
		// original start commit was in flight. Storage's incarnation CAS already
		// reloaded the replacement; stale recovery authority must end here rather
		// than terminalizing or enqueueing that new row by ID.
		if errors.Is(err, storage.ErrTestStartIdentityChanged) {
			return nil
		}
		// Admission state can legitimately change while durable launch evidence is
		// being loaded. After bounded transparent retries, leave the run QUEUED so
		// a later recovery/start attempt can use a stable snapshot; transient churn
		// must never turn an accepted CI run into a terminal failure.
		if errors.Is(err, errStartAdmissionEvidenceStale) || retryableStartSecretRequirement(err) {
			tr.enqueueQueuedCIRecoveryRetryForIdentityWithTerm(id, identity, term)
			return errors.Join(errStartAdmissionEvidenceStale, err)
		}
		message := "controller could not recover queued CI run: " + err.Error()
		if terminalErr := tr.failCIRunBeforeExecutionForIdentityWithTerm(id, message, true, &term, &identity); terminalErr != nil {
			return errors.Join(err, terminalErr)
		}
		return nil
	}
	return nil
}

const (
	queuedCIRecoveryInitialBackoff = 25 * time.Millisecond
	queuedCIRecoveryMaxBackoff     = time.Second
)

type queuedCIRecoveryRetryState struct {
	term        executionLeaseTerm
	identity    queuedStartRecoveryIdentity
	nextAttempt time.Time
	backoff     time.Duration
}

// queuedStartRecoveryIdentity binds asynchronous recovery to one durable test
// incarnation. CI and recurring schedule IDs are deterministic and may be
// reused after deletion; an ID-only retry would otherwise acquire authority
// over a replacement that the original request never admitted.
type queuedStartRecoveryIdentity struct {
	id                    string
	refKey                string
	projectID             string
	startTime             time.Time
	materializationSHA256 [sha256.Size]byte
	valid                 bool
}

func queuedStartRecoveryIdentityForTest(test *ManagedTest) (queuedStartRecoveryIdentity, bool) {
	if test == nil || strings.TrimSpace(test.ID) == "" || test.CreatedAt.IsZero() {
		return queuedStartRecoveryIdentity{}, false
	}
	return queuedStartRecoveryIdentity{
		id:                    strings.TrimSpace(test.ID),
		refKey:                strings.TrimSpace(test.RefKey),
		projectID:             normalizeProjectID(test.ProjectID),
		startTime:             test.CreatedAt.UTC().Truncate(time.Microsecond),
		materializationSHA256: sha256.Sum256([]byte(test.MaterializationKey)),
		valid:                 true,
	}, true
}

func (identity queuedStartRecoveryIdentity) matches(test *ManagedTest) bool {
	if !identity.valid || test == nil {
		return false
	}
	candidate, ok := queuedStartRecoveryIdentityForTest(test)
	return ok && candidate == identity
}

// enqueueQueuedCIRecoveryRetryWithTerm provides the stable follow-up trigger
// that a bounded in-request admission retry cannot. IDs are deduplicated into a
// single term-bound coordinator so a shared outage cannot create one goroutine,
// timer, and synchronized provider call per accepted row.
func (tr *TestRunner) enqueueQueuedCIRecoveryRetryWithTerm(id string, term executionLeaseTerm) {
	if tr == nil || strings.TrimSpace(id) == "" || !tr.executionLeaseTermCurrent(term) {
		return
	}
	tr.mu.Lock()
	id = tr.resolveRefKeyLocked(strings.TrimSpace(id))
	identity, _ := queuedStartRecoveryIdentityForTest(tr.tests[id])
	tr.enqueueQueuedCIRecoveryRetryForIdentityLocked(id, identity, term)
	tr.mu.Unlock()
}

func (tr *TestRunner) enqueueQueuedCIRecoveryRetryForIdentityWithTerm(id string, identity queuedStartRecoveryIdentity, term executionLeaseTerm) {
	if tr == nil || strings.TrimSpace(id) == "" || !identity.valid || !tr.executionLeaseTermCurrent(term) {
		return
	}
	tr.mu.Lock()
	id = tr.resolveRefKeyLocked(strings.TrimSpace(id))
	if !identity.matches(tr.tests[id]) {
		tr.mu.Unlock()
		return
	}
	tr.enqueueQueuedCIRecoveryRetryForIdentityLocked(id, identity, term)
	tr.mu.Unlock()
}

func (tr *TestRunner) enqueueQueuedCIRecoveryRetryForManagedTestWithTerm(test *ManagedTest, term executionLeaseTerm) {
	if tr == nil || test == nil || !tr.executionLeaseTermCurrent(term) {
		return
	}
	tr.mu.Lock()
	current := tr.tests[test.ID]
	identity, ok := queuedStartRecoveryIdentityForTest(test)
	if !ok || !identity.matches(current) {
		tr.mu.Unlock()
		return
	}
	tr.enqueueQueuedCIRecoveryRetryForIdentityLocked(test.ID, identity, term)
	tr.mu.Unlock()
}

// enqueueQueuedCIRecoveryRetryForIdentityLocked installs one exact-incarnation
// retry. The caller holds tr.mu.
func (tr *TestRunner) enqueueQueuedCIRecoveryRetryForIdentityLocked(id string, identity queuedStartRecoveryIdentity, term executionLeaseTerm) {
	if tr.shuttingDown || !identity.valid || !identity.matches(tr.tests[id]) || !tr.executionLeaseTermLocallyCurrent(term) {
		return
	}
	if tr.ciRecoveryRetrying == nil {
		tr.ciRecoveryRetrying = make(map[string]queuedCIRecoveryRetryState)
	}
	if tr.ciRecoveryRetryWorkerTerm != term {
		// Leadership acquisition runs the durable queued-row scan. Discard stale
		// term schedules when that scan begins populating the successor worker.
		clear(tr.ciRecoveryRetrying)
		tr.ciRecoveryRetryWorkerTerm = term
		tr.ciRecoveryRetryWake = make(chan struct{}, 1)
		tr.ciRecoveryRetryWorkerUp = false
	}
	if existing, exists := tr.ciRecoveryRetrying[id]; !exists || existing.term != term || existing.identity != identity {
		backoff := queuedCIRecoveryInitialBackoff
		tr.ciRecoveryRetrying[id] = queuedCIRecoveryRetryState{
			term:        term,
			identity:    identity,
			backoff:     backoff,
			nextAttempt: time.Now().Add(backoff + queuedCIRecoveryRetryJitter(id, backoff)),
		}
	}
	wake := tr.ciRecoveryRetryWake
	startWorker := !tr.ciRecoveryRetryWorkerUp
	if startWorker {
		tr.ciRecoveryRetryWorkerUp = true
	}
	select {
	case wake <- struct{}{}:
	default:
	}
	if startWorker {
		go tr.runQueuedCIRecoveryCoordinator(term, wake)
	}
}

func queuedCIRecoveryRetryJitter(id string, backoff time.Duration) time.Duration {
	window := backoff / 4
	if window <= 0 {
		return 0
	}
	var hash uint64 = 1469598103934665603
	for index := 0; index < len(id); index++ {
		hash ^= uint64(id[index])
		hash *= 1099511628211
	}
	return time.Duration(hash % uint64(window))
}

// runQueuedCIRecoveryCoordinator performs at most one storage/provider-heavy
// recovery at a time. Every row retains an independent jittered exponential
// schedule, avoiding both starvation and synchronized retry bursts.
func (tr *TestRunner) runQueuedCIRecoveryCoordinator(term executionLeaseTerm, wake <-chan struct{}) {
	ctx, cancel := tr.executionOperationContext(nil)
	defer cancel()
	if tr.durableExecutionFence {
		ctxTerm, ok := executionLeaseTermFromContext(ctx)
		if !ok || ctxTerm != term {
			tr.finishQueuedCIRecoveryCoordinator(term)
			return
		}
	}
	releasedWorker := false
	defer func() {
		if !releasedWorker {
			tr.finishQueuedCIRecoveryCoordinator(term)
		}
	}()

	for {
		if !tr.executionLeaseTermCurrent(term) {
			return
		}
		tr.mu.Lock()
		if tr.shuttingDown || tr.ciRecoveryRetryWorkerTerm != term {
			tr.mu.Unlock()
			return
		}
		now := time.Now()
		nextID := ""
		var nextIdentity queuedStartRecoveryIdentity
		var nextAt time.Time
		for id, state := range tr.ciRecoveryRetrying {
			if state.term != term || !state.identity.matches(tr.tests[id]) || !queuedStartRecoveryCoordinatorEligible(tr.tests[id]) {
				delete(tr.ciRecoveryRetrying, id)
				continue
			}
			if nextID == "" || state.nextAttempt.Before(nextAt) || (state.nextAttempt.Equal(nextAt) && id < nextID) {
				nextID = id
				nextIdentity = state.identity
				nextAt = state.nextAttempt
			}
		}
		if nextID == "" {
			// Clear ownership while the empty observation is still protected by
			// tr.mu. An enqueue racing this handoff will therefore either be seen
			// by this worker or observe workerUp=false and launch its successor.
			// Never defer this clear: an old worker could otherwise clobber the
			// successor's workerUp bit after the successor has started.
			if tr.beforeQueuedCIRecoveryEmptyRelease != nil {
				tr.beforeQueuedCIRecoveryEmptyRelease()
			}
			tr.ciRecoveryRetryWorkerUp = false
			releasedWorker = true
			tr.mu.Unlock()
			return
		}
		wait := nextAt.Sub(now)
		tr.mu.Unlock()
		if wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-wake:
				timer.Stop()
				continue
			case <-timer.C:
			}
		}

		err := tr.recoverQueuedStartForIdentityWithTerm(nextID, nextIdentity, term)
		tr.mu.Lock()
		state, exists := tr.ciRecoveryRetrying[nextID]
		if exists && state.term == term && state.identity == nextIdentity {
			if err != nil && errors.Is(err, errStartAdmissionEvidenceStale) && nextIdentity.matches(tr.tests[nextID]) && queuedStartRecoveryCoordinatorEligible(tr.tests[nextID]) {
				state.backoff = min(state.backoff*2, queuedCIRecoveryMaxBackoff)
				state.nextAttempt = time.Now().Add(state.backoff + queuedCIRecoveryRetryJitter(nextID, state.backoff))
				tr.ciRecoveryRetrying[nextID] = state
			} else {
				delete(tr.ciRecoveryRetrying, nextID)
			}
		}
		tr.mu.Unlock()
	}
}

// recoverQueuedStartWithTerm extends durable CI recovery to an explicitly
// enqueued ordinary start. A SHAKEOUT row first verifies its retained one-shot
// outcome; a QUEUED row may already contain the durable pass produced by that
// verification. Only explicit enqueue sites reach this non-CI path—the durable
// startup scan remains CI-only—so an unrelated manually queued row is never
// started implicitly.
func (tr *TestRunner) recoverQueuedStartWithTerm(id string, term executionLeaseTerm) error {
	if tr == nil || !tr.executionLeaseTermCurrent(term) {
		return errNotExecutionLeader
	}
	tr.mu.RLock()
	id = tr.resolveRefKeyLocked(id)
	identity, ok := queuedStartRecoveryIdentityForTest(tr.tests[id])
	tr.mu.RUnlock()
	if !ok {
		return nil
	}
	return tr.recoverQueuedStartForIdentityWithTerm(id, identity, term)
}

func (tr *TestRunner) recoverQueuedStartForIdentityWithTerm(id string, identity queuedStartRecoveryIdentity, term executionLeaseTerm) error {
	if tr == nil || !tr.executionLeaseTermCurrent(term) {
		return errNotExecutionLeader
	}
	tr.mu.RLock()
	id = tr.resolveRefKeyLocked(id)
	test := tr.tests[id]
	shakeoutReview := test != nil && test.StartedAt == nil && strings.EqualFold(strings.TrimSpace(test.Status), "SHAKEOUT_REVIEW")
	if !identity.matches(test) || (!queuedCIRecoveryEligible(test) && !shakeoutReview) {
		tr.mu.RUnlock()
		return nil
	}
	_, ciMetadataErr := decodeCIRunMetadata(test.MaterializationKey)
	hasPendingShakeoutOutcome := test.pendingScriptShakeoutOutcome != nil
	shakeoutReviewSummary := test.Error
	tr.mu.RUnlock()
	if ciMetadataErr == nil {
		return tr.recoverQueuedCIRunForIdentityWithTerm(id, identity, term)
	}
	if shakeoutReview {
		return tr.reconcileScenarioScheduleShakeoutReviewForIdentityWithTerm(id, shakeoutReviewSummary, &identity, term)
	}
	if hasPendingShakeoutOutcome {
		if err := tr.resumePendingScriptShakeoutOutcomeForIdentity(id, &term, &identity); err != nil {
			var gateErr *ScriptShakeoutGateError
			if errors.As(err, &gateErr) {
				// The retained evidence was conclusively rejected and is now durable
				// review state. Complete any recurring occurrence/schedule correlation
				// before retiring this ID from the term-bound coordinator.
				return tr.reconcileScenarioScheduleShakeoutReviewForIdentityWithTerm(id, gateErr.Error(), &identity, term)
			}
			return err
		}
	}
	startOptions := StartTestOptions{expectedRecoveryIdentity: &identity}
	if scheduledRetry, err := tr.scenarioScheduleStartRetryingForTest(id); err != nil {
		return errors.Join(errStartAdmissionEvidenceStale, fmt.Errorf("load scheduled start-retry correlation: %w", err))
	} else {
		startOptions.SkipLaunchWindowPreflight = scheduledRetry
	}
	if err := tr.startTestWithOptionsAndExpectedTerm(id, startOptions, &term); err != nil {
		if errors.Is(err, errNotExecutionLeader) || errors.Is(err, errRunnerShuttingDown) {
			return err
		}
		if errors.Is(err, storage.ErrTestStartIdentityChanged) {
			return nil
		}
		// A deployment maintenance lease is bounded and intentionally temporary.
		// Keep the exact scheduled retry alive until the lease releases instead of
		// turning maintenance into a permanent failed run.
		if errors.Is(err, errStartAdmissionEvidenceStale) || retryableStartSecretRequirement(err) ||
			errors.Is(err, storage.ErrDeploymentLeaseActive) {
			return errors.Join(errStartAdmissionEvidenceStale, err)
		}
		if correlated, terminalErr := tr.failRecoveredScenarioScheduleStartForIdentityWithTerm(id, err, &identity, term); correlated {
			if terminalErr != nil {
				return errors.Join(err, terminalErr)
			}
			return nil
		}
		return err
	}
	return nil
}

func queuedCIRecoveryEligible(test *ManagedTest) bool {
	if test == nil || test.StartedAt != nil {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(test.Status), "QUEUED") {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(test.Status), "SHAKEOUT") && test.pendingScriptShakeoutOutcome != nil
}

// queuedCIStartupRecoveryEligible includes the terminalization-only review
// state produced after a conclusive shakeout rejection. It deliberately stays
// separate from queuedCIRecoveryEligible: review rows may be coordinated after
// restart, but they must never re-enter shakeout or execution.
func queuedCIStartupRecoveryEligible(test *ManagedTest) bool {
	if queuedCIRecoveryEligible(test) {
		return true
	}
	return test != nil && test.StartedAt == nil && strings.EqualFold(strings.TrimSpace(test.Status), "SHAKEOUT_REVIEW")
}

func queuedStartRecoveryCoordinatorEligible(test *ManagedTest) bool {
	return queuedCIStartupRecoveryEligible(test)
}

func (tr *TestRunner) finishQueuedCIRecoveryCoordinator(term executionLeaseTerm) {
	tr.mu.Lock()
	if tr.ciRecoveryRetryWorkerTerm == term {
		tr.ciRecoveryRetryWorkerUp = false
	}
	tr.mu.Unlock()
}

// validateCIRunPolicyForStart closes the gap between acceptance and execution.
// A queued or pending run is still subject to the exact Actions policy revision
// that authorized it; disabling or revising that policy revokes execution even
// when a controller restart or capacity queue delayed the start.
func (tr *TestRunner) validateCIRunPolicyForStart(id string) error {
	return tr.validateCIRunPolicyForStartAtProjectRevision(id, time.Time{})
}

func (tr *TestRunner) validateCIRunPolicyForStartAtProjectRevision(id string, expectedProjectUpdatedAt time.Time) error {
	if tr == nil {
		return nil
	}
	tr.mu.RLock()
	id = tr.resolveRefKeyLocked(id)
	test := tr.tests[id]
	if test == nil {
		tr.mu.RUnlock()
		return nil
	}
	projectID := test.ProjectID
	candidate := test
	metadata, metadataErr := decodeCIRunMetadata(test.MaterializationKey)
	tr.mu.RUnlock()
	if metadataErr != nil {
		return nil
	}
	if tr.projectStore == nil {
		return fmt.Errorf("CI run trust policy is unavailable")
	}
	project, err := tr.projectStore.GetProject(normalizeProjectID(projectID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			tr.evictMissingManagedTestIdentity(id, candidate)
			return fmt.Errorf("%w: CI run project %q no longer exists", errStoredTestMissing, normalizeProjectID(projectID))
		}
		return errors.Join(errStartAdmissionEvidenceStale, fmt.Errorf("load CI run project trust policy: %w", err))
	}
	if project == nil {
		return fmt.Errorf("CI run project trust policy is unavailable")
	}
	if !expectedProjectUpdatedAt.IsZero() && !project.UpdatedAt.Equal(expectedProjectUpdatedAt) {
		return errStartAdmissionEvidenceStale
	}
	policy := normalizeGitHubProjectPolicy(project.Settings.GitHub).Actions
	if !policy.Enabled {
		return fmt.Errorf("GitHub Actions execution was disabled after this CI run was accepted")
	}
	if policy.Revision != metadata.PolicyRevision {
		return fmt.Errorf("GitHub Actions policy revision changed after this CI run was accepted")
	}
	return nil
}

func (tr *TestRunner) failCIRunBeforeExecution(id, message string) error {
	return tr.failCIRunBeforeExecutionWithPromotion(id, message, true)
}

// failCIRunBeforeExecutionDuringPromotion terminalizes one revoked pending row
// without starting another queue drain. The caller already owns the iterative
// promotion loop; recursively re-entering it grows one stack frame per revoked
// row and can exhaust the controller stack on a large recovered queue.
func (tr *TestRunner) failCIRunBeforeExecutionDuringPromotion(id, message string) error {
	return tr.failCIRunBeforeExecutionWithPromotion(id, message, false)
}

func (tr *TestRunner) failCIRunBeforeExecutionWithPromotion(id, message string, promotePending bool) error {
	return tr.failCIRunBeforeExecutionWithTerm(id, message, promotePending, nil)
}

func (tr *TestRunner) failCIRunBeforeExecutionWithTerm(id, message string, promotePending bool, expectedTerm *executionLeaseTerm) error {
	return tr.failCIRunBeforeExecutionForIdentityWithTerm(id, message, promotePending, expectedTerm, nil)
}

func (tr *TestRunner) failCIRunBeforeExecutionForIdentityWithTerm(
	id, message string,
	promotePending bool,
	expectedTerm *executionLeaseTerm,
	expectedIdentity *queuedStartRecoveryIdentity,
) error {
	if err := tr.validateExpectedStartTerm(expectedTerm); err != nil {
		return err
	}
	tr.mu.Lock()
	current := tr.tests[id]
	if expectedIdentity != nil && !expectedIdentity.matches(current) {
		tr.mu.Unlock()
		return nil
	}
	if current == nil || !ciRunAwaitingExecution(current) {
		tr.mu.Unlock()
		return nil
	}
	if _, err := decodeCIRunMetadata(current.MaterializationKey); err != nil {
		tr.mu.Unlock()
		return fmt.Errorf("test is not a controller-backed CI run")
	}
	_, claimErr := tr.claimPassiveExecutionFenceLocked(current, expectedTerm)
	if claimErr != nil {
		tr.mu.Unlock()
		return claimErr
	}
	tr.mu.Unlock()
	return tr.completeTestRunWithPendingPromotion(current, "FAILED", time.Now().UTC(), strings.TrimSpace(message), nil, promotePending)
}

func ciRunAwaitingExecution(test *ManagedTest) bool {
	if test == nil || test.StartedAt != nil || test.cancel != nil || test.executionCtx != nil {
		return false
	}
	switch strings.ToUpper(strings.TrimSpace(test.Status)) {
	case "QUEUED", "SCHEDULED", "PENDING", "RESERVED", "SHAKEOUT_REVIEW":
		return true
	default:
		return false
	}
}
