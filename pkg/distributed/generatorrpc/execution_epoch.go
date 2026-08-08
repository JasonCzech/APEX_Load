package generatorrpc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	executionGenerationMetadataKey         = "x-apexload-execution-generation"
	metricsTestIDMetadataKey               = "x-apexload-metrics-test-id"
	coordinatedStartControllerMetadataKey  = "x-apexload-controller-sent-unix-nano"
	coordinatedStartClockOffsetMetadataKey = "x-apexload-generator-clock-offset-nano"
	coordinatedStartClockErrorMetadataKey  = "x-apexload-generator-clock-error-nano"
	maxExecutionEpochTTL                   = 10 * time.Minute
	maxCoordinatedStartClockOffset         = 24 * time.Hour
	maxExecutionFenceWorkers               = 16
	maxExecutionLeaseTestIDBytes           = 512
	maxTestExecutionLeaseFences            = 4096
)

// ExecutionGenerationSessionExpiredMessage is the FailedPrecondition detail a
// generator returns once its process-scoped execution epoch has lapsed. Every
// generation-fenced mutation, including assignment-scoped lease renewal, is
// rejected with it. Controllers match it to separate "nobody renewed the
// process epoch on this generator" from an ordinary generation mismatch: the
// two need different operator action, and the assignment-lease call site cannot
// tell them apart from the gRPC code alone.
const ExecutionGenerationSessionExpiredMessage = "execution generation session has expired"

// ExecutionGenerationExpiredMessage is the rejection detail a generator returns
// from a *process-scoped* epoch claim whose generation is already current but
// whose session has lapsed: a lapsed generation is never revived in place, only
// superseded by a higher one. Unlike a fenced mutation this arrives as an
// accepted-false acknowledgement rather than a gRPC status, so the claim path
// and the assignment path report the very same generator state with two
// different shapes. Controllers that re-claim the process epoch before renewing
// an assignment lease therefore see this message, never
// ExecutionGenerationSessionExpiredMessage, when nobody renewed the epoch.
const ExecutionGenerationExpiredMessage = "execution generation has expired; a higher generation is required"

// testExecutionLease is a volatile, assignment-scoped safety fence. The
// durable process epoch remains the controller-term high-water; this narrower
// lease lets one lost test expire without stopping unrelated assignments that
// share the same generator process.
type testExecutionLease struct {
	generation uint64
	leaseOwner string
	expiresAt  time.Time
	token      uint64
	timer      *time.Timer
}

func (s *Service) claimTestExecutionLease(ctx context.Context, testID string, generation uint64, leaseOwner string, expiresAt time.Time) (*pb.ExecutionEpochAck, error) {
	if len(testID) > maxExecutionLeaseTestIDBytes {
		return nil, status.Errorf(codes.InvalidArgument, "test_id exceeds %d bytes", maxExecutionLeaseTestIDBytes)
	}
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	if err := s.validateMutationGenerationLocked(generation); err != nil {
		return nil, err
	}
	now := time.Now()
	if fencedGeneration := max(s.testExecutionLeaseFences[testID], s.testExecutionLeaseFenceAllGeneration); fencedGeneration >= generation {
		return &pb.ExecutionEpochAck{
			Accepted:          false,
			CurrentGeneration: s.executionGeneration,
			ExpiresAtUnixMs:   s.executionExpiresAt.UnixMilli(),
			ErrorMessage:      "test assignment lease has expired and cannot be revived in this execution generation",
			TestLeaseAccepted: true,
		}, nil
	}
	if current, exists := s.testExecutionLeases[testID]; exists && current.generation == generation && !now.Before(current.expiresAt) {
		// Timer delivery is advisory; the timestamp is the semantic deadline. A
		// delayed AfterFunc must not let a renewal replace the expired token and
		// revive work that was already logically fenced.
		s.clearTestExecutionLeaseLocked(testID)
		s.recordTestExecutionLeaseFenceLocked(testID, generation)
		assignment, assigned := s.assignments[testID]
		if assigned && assignment.executionGeneration == generation {
			go s.stopExpiredTestExecutionAssignment(testID, generation)
		}
		return &pb.ExecutionEpochAck{
			Accepted:          false,
			CurrentGeneration: s.executionGeneration,
			ExpiresAtUnixMs:   current.expiresAt.UnixMilli(),
			ErrorMessage:      "test assignment lease has expired and cannot be revived in this execution generation",
			TestLeaseAccepted: true,
		}, nil
	}
	if s.executionLeaseOwner != "" && leaseOwner != s.executionLeaseOwner {
		return &pb.ExecutionEpochAck{
			Accepted:          false,
			CurrentGeneration: s.executionGeneration,
			ExpiresAtUnixMs:   s.executionExpiresAt.UnixMilli(),
			ErrorMessage:      "execution generation belongs to a different controller lease owner",
			TestLeaseAccepted: true,
		}, nil
	}
	assignment, ok := s.assignments[testID]
	if !ok {
		return nil, status.Error(codes.NotFound, "test assignment not found")
	}
	if assignment.executionGeneration != generation {
		return nil, status.Errorf(codes.FailedPrecondition, "test belongs to execution generation %d", assignment.executionGeneration)
	}
	// An assignment lease can never extend beyond its process epoch. This keeps
	// process-level leadership loss the stronger outer fence.
	if s.executionExpiresAt.Before(expiresAt) {
		expiresAt = s.executionExpiresAt
	}
	if current, exists := s.testExecutionLeases[testID]; exists && current.generation == generation && current.leaseOwner == leaseOwner && current.expiresAt.After(expiresAt) {
		expiresAt = current.expiresAt
	}
	s.nextTestLeaseToken++
	if s.nextTestLeaseToken == 0 {
		s.nextTestLeaseToken++
	}
	token := s.nextTestLeaseToken
	if current := s.testExecutionLeases[testID]; current.timer != nil {
		current.timer.Stop()
	}
	lease := testExecutionLease{
		generation: generation,
		leaseOwner: leaseOwner,
		expiresAt:  expiresAt,
		token:      token,
	}
	delay := time.Until(expiresAt)
	if delay < 0 {
		delay = 0
	}
	lease.timer = time.AfterFunc(delay, func() {
		s.expireTestExecutionLease(testID, generation, expiresAt, token)
	})
	s.testExecutionLeases[testID] = lease
	return &pb.ExecutionEpochAck{
		Accepted:          true,
		CurrentGeneration: generation,
		ExpiresAtUnixMs:   expiresAt.UnixMilli(),
		TestLeaseAccepted: true,
	}, nil
}

func (s *Service) expireTestExecutionLease(testID string, generation uint64, expiresAt time.Time, token uint64) {
	s.mu.Lock()
	lease, ok := s.testExecutionLeases[testID]
	if !ok || lease.token != token || lease.generation != generation || !lease.expiresAt.Equal(expiresAt) || time.Now().Before(expiresAt) {
		s.mu.Unlock()
		return
	}
	delete(s.testExecutionLeases, testID)
	s.recordTestExecutionLeaseFenceLocked(testID, generation)
	assignment, assigned := s.assignments[testID]
	s.mu.Unlock()
	if !assigned || assignment.executionGeneration != generation {
		return
	}
	s.stopExpiredTestExecutionAssignment(testID, generation)
}

func (s *Service) recordTestExecutionLeaseFenceLocked(testID string, generation uint64) {
	if _, exists := s.testExecutionLeaseFences[testID]; !exists && len(s.testExecutionLeaseFences) >= maxTestExecutionLeaseFences {
		// Keep memory bounded without weakening an existing tombstone. Exhausting
		// this exceptional-loss budget fences every loss-tolerant assignment for
		// the rest of this controller generation; ordinary work remains available.
		s.testExecutionLeaseFenceAllGeneration = generation
	} else {
		s.testExecutionLeaseFences[testID] = generation
	}
}

func (s *Service) stopExpiredTestExecutionAssignment(testID string, generation uint64) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	_ = s.forceStopTests(ctx, []string{testID}, generation, "FAILED", "test execution lease expired before completion", false)
	cancel()
}

func (s *Service) clearTestExecutionLeaseLocked(testID string) {
	lease, ok := s.testExecutionLeases[testID]
	if !ok {
		return
	}
	delete(s.testExecutionLeases, testID)
	if lease.timer != nil {
		lease.timer.Stop()
	}
}

func (s *Service) testExecutionLeaseCurrentLocked(testID string, generation uint64, now time.Time) bool {
	if max(s.testExecutionLeaseFences[testID], s.testExecutionLeaseFenceAllGeneration) >= generation {
		return false
	}
	lease, ok := s.testExecutionLeases[testID]
	return ok && lease.generation == generation && now.Before(lease.expiresAt)
}

func (s *Service) resetTestExecutionLeasesForGenerationLocked(generation uint64) {
	for testID, lease := range s.testExecutionLeases {
		if lease.generation < generation {
			s.clearTestExecutionLeaseLocked(testID)
		}
	}
	for testID, fencedGeneration := range s.testExecutionLeaseFences {
		if fencedGeneration < generation {
			delete(s.testExecutionLeaseFences, testID)
		}
	}
	if s.testExecutionLeaseFenceAllGeneration < generation {
		s.testExecutionLeaseFenceAllGeneration = 0
	}
}

func (s *Service) validateMutationGenerationLocked(generation uint64) error {
	return s.executionEpochManager.validateMutationLocked(generation, time.Now())
}

func (s *Service) testIDsBeforeGenerationLocked(generation uint64) []string {
	ids := make([]string, 0)
	for testID, assignment := range s.assignments {
		if assignment.executionGeneration < generation {
			ids = append(ids, testID)
		}
	}
	return ids
}

func (s *Service) resetExecutionEpochTimerLocked(generation uint64, expiresAt time.Time) {
	s.executionEpochManager.resetTimerLocked(s, generation, expiresAt)
}

func (s *Service) expireExecutionEpoch(generation uint64, expiresAt time.Time) {
	s.expireExecutionEpochToken(generation, expiresAt, 0, false)
}

func (s *Service) expireExecutionEpochToken(generation uint64, expiresAt time.Time, token uint64, requireToken bool) {
	for {
		s.mu.Lock()
		wait, testIDs, accepted := s.executionEpochManager.planExpiryLocked(s, generation, expiresAt, token, requireToken, time.Now())
		s.mu.Unlock()
		if wait != nil {
			<-wait
			continue
		}
		if !accepted {
			return
		}
		s.metricsBroker.fenceGeneration(generation)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_ = s.forceStopFencedTests(ctx, testIDs, generation)
		cancel()
		return
	}
}

func (s *Service) forceStopFencedTests(ctx context.Context, testIDs []string, maximumGeneration uint64) error {
	return s.forceStopTests(ctx, testIDs, maximumGeneration, "FAILED", "execution generation was fenced before test completion", true)
}

func (s *Service) forceStopTests(ctx context.Context, testIDs []string, maximumGeneration uint64, finalState string, finalError string, trackExecutionEpochFence bool) error {
	if len(testIDs) == 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	// Phase one is deliberately unbounded by the wait-worker pool: detach every
	// matching assignment and fan out cancellation synchronously. If the first
	// maxExecutionFenceWorkers executions have blocked done channels, they must
	// not prevent later assignments from seeing cancellation or remaining
	// detached and runnable after the controller lease has expired.
	stops := s.detachFencedTests(testIDs, maximumGeneration, trackExecutionEpochFence)
	for index := range stops {
		stops[index].finalState = finalState
		stops[index].finalError = finalError
	}
	for _, stop := range stops {
		for _, execution := range stop.executions {
			stopGroupExecution(execution)
		}
	}
	if len(stops) == 0 {
		return nil
	}

	// Phase two bounds only quiescence waits and archive finalization. Every job
	// has already been made non-runnable above, so a slow engine cannot delay the
	// safety-critical cancellation fanout.
	workerCount := min(len(stops), maxExecutionFenceWorkers)
	jobs := make(chan detachedFencedTest, len(stops))
	errs := make(chan error, len(stops))
	for _, stop := range stops {
		jobs <- stop
	}
	close(jobs)
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for stop := range jobs {
				if err := s.finishDetachedFencedTest(ctx, stop); err != nil {
					errs <- fmt.Errorf("stop fenced test %s: %w", stop.testID, err)
				}
			}
		}()
	}
	workers.Wait()
	close(errs)
	joined := make([]error, 0)
	for err := range errs {
		joined = append(joined, err)
	}
	if ctx.Err() != nil {
		joined = append(joined, ctx.Err())
	}
	return errors.Join(joined...)
}

type detachedFencedTest struct {
	testID                   string
	assignment               assignedTest
	executions               []*groupExecution
	writer                   *archiveRecorder
	trackedFinalizer         bool
	trackedExecutionFence    bool
	persistentJavaDrainToken uint64
	finalState               string
	finalError               string
}

func (s *Service) detachFencedTests(testIDs []string, maximumGeneration uint64, trackExecutionEpochFence bool) []detachedFencedTest {
	seen := make(map[string]struct{}, len(testIDs))
	stops := make([]detachedFencedTest, 0, len(testIDs))
	s.mu.Lock()
	for _, testID := range testIDs {
		if _, duplicate := seen[testID]; duplicate {
			continue
		}
		seen[testID] = struct{}{}
		assignment, ok := s.assignments[testID]
		if !ok || assignment.executionGeneration > maximumGeneration {
			continue
		}
		s.markStoppingLocked(testID, assignment.executionGeneration)
		persistentJavaDrainToken := s.reserveAssignmentJavaDrainLocked(assignment)
		s.clearTestExecutionLeaseLocked(testID)
		delete(s.assignments, testID)
		s.stopCompletionTimerLocked(testID)
		s.stopStageTimerLocked(testID)
		executionMap := s.executions[testID]
		delete(s.executions, testID)
		executions := make([]*groupExecution, 0, len(executionMap))
		for _, execution := range executionMap {
			executions = append(executions, execution)
		}
		writer := s.archiveManager.takeWriter(testID)
		// Process-generation expiry keeps the global epoch fenced until this exact
		// detached test quiesces and finalizes. Assignment-lease expiry is already
		// isolated by its per-test tombstone and must not block unrelated mutations.
		if trackExecutionEpochFence {
			s.executionEpochManager.beginDetachedFencesLocked(1)
		}
		stops = append(stops, detachedFencedTest{
			testID:                   testID,
			assignment:               assignment,
			executions:               executions,
			writer:                   writer,
			trackedFinalizer:         writer != nil && s.archiveManager.addFinalizer(),
			trackedExecutionFence:    trackExecutionEpochFence,
			persistentJavaDrainToken: persistentJavaDrainToken,
		})
	}
	s.mu.Unlock()
	return stops
}

func (s *Service) finishDetachedFencedTest(ctx context.Context, stop detachedFencedTest) error {
	if err := waitForGeneratorExecutions(ctx, stop.executions); err != nil {
		finish := func() {
			_ = waitForGeneratorExecutions(context.Background(), stop.executions)
			s.releaseStopDrain(stop.persistentJavaDrainToken)
			s.captureStoppedExecutionMetrics(&stop.assignment, stop.executions)
			s.finishStoppedTestWithState(stop.testID, stop.assignment, stop.writer, stop.finalState, stop.finalError)
			if stop.trackedExecutionFence {
				s.completeDetachedExecutionFence()
			}
		}
		if stop.trackedFinalizer {
			go func() {
				defer s.archiveManager.finalizerDone()
				finish()
			}()
		} else {
			go finish()
		}
		return err
	}
	if stop.trackedFinalizer {
		defer s.archiveManager.finalizerDone()
	}
	if stop.trackedExecutionFence {
		defer s.completeDetachedExecutionFence()
	}
	s.releaseStopDrain(stop.persistentJavaDrainToken)
	s.captureStoppedExecutionMetrics(&stop.assignment, stop.executions)
	_, finalError := s.finishStoppedTestWithState(stop.testID, stop.assignment, stop.writer, stop.finalState, stop.finalError)
	// A FAILED lifecycle reason is expected evidence for a fenced/shutdown test;
	// only a changed error (for example an appended archive-finalization failure)
	// means the cleanup itself failed.
	if finalError != strings.TrimSpace(stop.finalError) {
		return errors.New(finalError)
	}
	return nil
}

func (s *Service) completeDetachedExecutionFence() {
	s.mu.Lock()
	s.executionEpochManager.completeDetachedFenceLocked(s)
	s.mu.Unlock()
}

func outgoingExecutionGenerationContext(ctx context.Context, generation uint64) context.Context {
	return metadata.AppendToOutgoingContext(ctx, executionGenerationMetadataKey, strconv.FormatUint(generation, 10))
}

func incomingExecutionGeneration(ctx context.Context) (uint64, error) {
	values := metadata.ValueFromIncomingContext(ctx, executionGenerationMetadataKey)
	if len(values) == 0 {
		return 0, nil
	}
	raw := strings.TrimSpace(values[len(values)-1])
	generation, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, status.Error(codes.InvalidArgument, "invalid execution generation metadata")
	}
	return generation, nil
}

func outgoingCoordinatedStartContext(ctx context.Context, sentAt time.Time) context.Context {
	return metadata.AppendToOutgoingContext(ctx, coordinatedStartControllerMetadataKey, strconv.FormatInt(sentAt.UnixNano(), 10))
}

func outgoingCoordinatedStartContextWithClockEstimate(ctx context.Context, sentAt time.Time, offset, uncertainty time.Duration) context.Context {
	ctx = outgoingCoordinatedStartContext(ctx, sentAt)
	return metadata.AppendToOutgoingContext(
		ctx,
		coordinatedStartClockOffsetMetadataKey, strconv.FormatInt(int64(offset), 10),
		coordinatedStartClockErrorMetadataKey, strconv.FormatInt(int64(uncertainty), 10),
	)
}

func incomingCoordinatedStartControllerTime(ctx context.Context) (time.Time, bool, error) {
	if ctx == nil {
		return time.Time{}, false, nil
	}
	values := metadata.ValueFromIncomingContext(ctx, coordinatedStartControllerMetadataKey)
	if len(values) == 0 {
		return time.Time{}, false, nil
	}
	if len(values) != 1 {
		return time.Time{}, false, status.Error(codes.InvalidArgument, "coordinated start controller time metadata must have exactly one value")
	}
	raw := strings.TrimSpace(values[0])
	unixNano, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || raw != values[0] || unixNano <= 0 {
		return time.Time{}, false, status.Error(codes.InvalidArgument, "invalid coordinated start controller time metadata")
	}
	return time.Unix(0, unixNano), true, nil
}

func incomingCoordinatedStartClockEstimate(ctx context.Context) (time.Duration, time.Duration, bool, error) {
	if ctx == nil {
		return 0, 0, false, nil
	}
	offsetValues := metadata.ValueFromIncomingContext(ctx, coordinatedStartClockOffsetMetadataKey)
	errorValues := metadata.ValueFromIncomingContext(ctx, coordinatedStartClockErrorMetadataKey)
	if len(offsetValues) == 0 && len(errorValues) == 0 {
		return 0, 0, false, nil
	}
	if len(offsetValues) != 1 || len(errorValues) != 1 {
		return 0, 0, false, status.Error(codes.InvalidArgument, "coordinated start clock estimate metadata must have exactly one offset and uncertainty")
	}
	offsetRaw, uncertaintyRaw := offsetValues[0], errorValues[0]
	offsetNanos, offsetErr := strconv.ParseInt(offsetRaw, 10, 64)
	uncertaintyNanos, uncertaintyErr := strconv.ParseInt(uncertaintyRaw, 10, 64)
	if offsetErr != nil || uncertaintyErr != nil ||
		strconv.FormatInt(offsetNanos, 10) != offsetRaw || strconv.FormatInt(uncertaintyNanos, 10) != uncertaintyRaw {
		return 0, 0, false, status.Error(codes.InvalidArgument, "invalid coordinated start clock estimate metadata")
	}
	offset := time.Duration(offsetNanos)
	uncertainty := time.Duration(uncertaintyNanos)
	if offset < -maxCoordinatedStartClockOffset || offset > maxCoordinatedStartClockOffset ||
		uncertainty < 0 || uncertainty > coordinatedStartClockEstimateMaxUncertainty {
		return 0, 0, false, status.Error(codes.InvalidArgument, "coordinated start clock estimate is outside the accepted bounds")
	}
	return offset, uncertainty, true, nil
}

func outgoingMetricsTestContext(ctx context.Context, testID string) context.Context {
	testID = strings.TrimSpace(testID)
	if testID == "" {
		return ctx
	}
	return metadata.AppendToOutgoingContext(ctx, metricsTestIDMetadataKey, testID)
}

func incomingMetricsTestID(ctx context.Context) (string, error) {
	values := metadata.ValueFromIncomingContext(ctx, metricsTestIDMetadataKey)
	if len(values) == 0 {
		return "", nil
	}
	if len(values) != 1 {
		return "", status.Error(codes.InvalidArgument, "metrics test id metadata must have exactly one value")
	}
	testID := strings.TrimSpace(values[0])
	if testID == "" || testID != values[0] {
		return "", status.Error(codes.InvalidArgument, "invalid metrics test id metadata")
	}
	if err := validateArchiveTestID(testID); err != nil {
		return "", status.Errorf(codes.InvalidArgument, "invalid metrics test id metadata: %v", err)
	}
	return testID, nil
}

func scriptContentHash(content []byte) string {
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:])
}

func verifyExpectedScriptHash(expected string, content []byte) error {
	expected = strings.TrimSpace(expected)
	if expected == "" {
		return nil
	}
	if len(expected) != sha256.Size*2 {
		return fmt.Errorf("expected_script_hash must be a SHA-256 hex digest")
	}
	if _, err := hex.DecodeString(expected); err != nil {
		return fmt.Errorf("expected_script_hash must be a SHA-256 hex digest")
	}
	actual := scriptContentHash(content)
	if !strings.EqualFold(expected, actual) {
		return fmt.Errorf("cached script hash %s does not match expected_script_hash %s", actual, expected)
	}
	return nil
}
