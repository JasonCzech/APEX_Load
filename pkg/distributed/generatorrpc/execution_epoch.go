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
	executionGenerationMetadataKey = "x-apexload-execution-generation"
	metricsTestIDMetadataKey       = "x-apexload-metrics-test-id"
	maxExecutionEpochTTL           = 10 * time.Minute
	maxExecutionFenceWorkers       = 16
)

// ClaimExecutionEpoch installs or renews the generator-side execution fence.
// A higher generation and its exact controller owner are durable across
// generator restarts. A replacement process may reconstruct the volatile
// expiry for that exact term once; an ordinarily expired or displaced live
// session can never be revived.
func (s *Service) ClaimExecutionEpoch(ctx context.Context, claim *pb.ExecutionEpochClaim) (*pb.ExecutionEpochAck, error) {
	if claim == nil || claim.GetExecutionGeneration() == 0 {
		return nil, status.Error(codes.InvalidArgument, "execution_generation must be positive")
	}
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	ttlMs := claim.GetLeaseTtlMilliseconds()
	if ttlMs <= 0 {
		return nil, status.Error(codes.InvalidArgument, "lease_ttl_milliseconds must be positive")
	}
	if ttlMs > maxExecutionEpochTTL.Milliseconds() {
		return nil, status.Errorf(codes.InvalidArgument, "lease_ttl_milliseconds exceeds maximum %d", maxExecutionEpochTTL.Milliseconds())
	}
	generation := claim.GetExecutionGeneration()
	leaseOwner := strings.TrimSpace(claim.GetExecutionLeaseOwnerId())
	if err := validateExecutionLeaseOwner(generation, leaseOwner, true); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "execution_lease_owner_id: %v", err)
	}
	now := time.Now()
	expiresAt := now.Add(time.Duration(ttlMs) * time.Millisecond)

	s.mu.Lock()
	if s.draining {
		s.mu.Unlock()
		return nil, status.Error(codes.Unavailable, "generator is draining")
	}
	if s.executionEpochStateErr != nil || s.executionEpochState == nil {
		err := s.executionEpochStateErr
		if err == nil {
			err = errors.New("durable execution epoch state is unavailable")
		}
		s.mu.Unlock()
		return nil, status.Errorf(codes.FailedPrecondition, "durable execution epoch state: %v", err)
	}
	current := s.executionGeneration
	if generation < current {
		existingExpiry := s.executionExpiresAt
		s.mu.Unlock()
		return &pb.ExecutionEpochAck{
			Accepted:          false,
			CurrentGeneration: current,
			ExpiresAtUnixMs:   existingExpiry.UnixMilli(),
			ErrorMessage:      fmt.Sprintf("execution generation %d has been fenced by generation %d", generation, current),
		}, nil
	}
	if generation == current && current > 0 && s.executionLeaseOwner != "" && leaseOwner != s.executionLeaseOwner {
		existingExpiry := s.executionExpiresAt
		s.mu.Unlock()
		return &pb.ExecutionEpochAck{
			Accepted:          false,
			CurrentGeneration: current,
			ExpiresAtUnixMs:   existingExpiry.UnixMilli(),
			ErrorMessage:      "execution generation belongs to a different controller lease owner",
		}, nil
	}
	revalidatingRestart := generation == current && current > 0 && s.executionRestartRevalidation && (s.executionExpiresAt.IsZero() || !now.Before(s.executionExpiresAt))
	if generation == current && current > 0 && !revalidatingRestart && (s.executionExpiresAt.IsZero() || !now.Before(s.executionExpiresAt)) {
		existingExpiry := s.executionExpiresAt
		s.mu.Unlock()
		return &pb.ExecutionEpochAck{
			Accepted:          false,
			CurrentGeneration: current,
			ExpiresAtUnixMs:   existingExpiry.UnixMilli(),
			ErrorMessage:      "execution generation has expired; a higher generation is required",
		}, nil
	}
	// Version-1 state did not persist the exact owner. Bind it durably on the
	// first replacement-process revalidation before restoring a volatile lease
	// expiry. Once bound, a different controller owner can never claim this
	// generation, including after another process restart.
	if revalidatingRestart && s.executionLeaseOwner == "" {
		if err := ctx.Err(); err != nil {
			s.mu.Unlock()
			return nil, status.FromContextError(err).Err()
		}
		if err := s.executionEpochState.persist(generation, leaseOwner); err != nil {
			s.executionEpochStateErr = err
			s.notifyReadinessChangedLocked()
			s.mu.Unlock()
			return nil, status.Errorf(codes.FailedPrecondition, "persist execution generation %d owner: %v", generation, err)
		}
		s.executionLeaseOwner = leaseOwner
	}
	if generation > current {
		if err := ctx.Err(); err != nil {
			s.mu.Unlock()
			return nil, status.FromContextError(err).Err()
		}
		if err := s.executionEpochState.persist(generation, leaseOwner); err != nil {
			s.executionEpochStateErr = err
			s.notifyReadinessChangedLocked()
			s.mu.Unlock()
			return nil, status.Errorf(codes.FailedPrecondition, "persist execution generation %d: %v", generation, err)
		}
		s.executionGeneration = generation
		s.executionLeaseOwner = leaseOwner
		for _, subscriber := range s.subscribers {
			if subscriber != nil && subscriber.generation < generation {
				subscriber.fence()
			}
		}
		// Persistence is the point of no return for a higher fence. If the RPC
		// was canceled during fsync, retain the durable high-water but do not
		// install an active session; stop all displaced work independently of the
		// canceled request before requiring an even higher generation.
		if err := ctx.Err(); err != nil {
			s.executionExpiresAt = time.Time{}
			lowerTestIDs := s.testIDsBeforeGenerationLocked(generation)
			s.executionEpochFencing = len(lowerTestIDs) > 0
			s.mu.Unlock()
			stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			_ = s.forceStopFencedTests(stopCtx, lowerTestIDs, generation-1)
			cancel()
			return nil, status.FromContextError(err).Err()
		}
	}
	if err := ctx.Err(); err != nil {
		s.mu.Unlock()
		return nil, status.FromContextError(err).Err()
	}
	// Renewals for one generation can arrive out of order across the keeper and
	// active-test connections. A delayed claim based on an older controller
	// lease deadline must not shorten a newer, already-safe expiry and stop live
	// load prematurely. Every installed expiry was independently capped by the
	// same controller generation's local lease deadline, so retaining the later
	// one does not weaken fencing.
	if generation == current && s.executionExpiresAt.After(expiresAt) {
		expiresAt = s.executionExpiresAt
	}
	s.executionExpiresAt = expiresAt
	s.executionRestartRevalidation = false
	s.resetExecutionEpochTimerLocked(generation, expiresAt)
	lowerTestIDs := s.testIDsBeforeGenerationLocked(generation)
	if len(lowerTestIDs) > 0 {
		s.executionEpochFencing = true
	}
	s.mu.Unlock()

	// Persisting a higher generation is a point of no return. Once the durable
	// high-water advances, finish canceling displaced load even if the caller
	// disconnects or its RPC deadline elapses; otherwise stale work could remain
	// live until the new session expires. The independent timeout keeps this
	// post-commit cleanup bounded.
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
	cleanupErr := s.forceStopFencedTests(cleanupCtx, lowerTestIDs, generation-1)
	cleanupCancel()
	if cleanupErr != nil {
		return &pb.ExecutionEpochAck{
			Accepted:          false,
			CurrentGeneration: generation,
			ExpiresAtUnixMs:   expiresAt.UnixMilli(),
			ErrorMessage:      cleanupErr.Error(),
		}, nil
	}

	s.mu.Lock()
	if s.executionGeneration == generation && s.executionFencePending == 0 && len(s.testIDsBeforeGenerationLocked(generation)) == 0 {
		s.executionEpochFencing = false
	}
	accepted := s.executionGeneration == generation && !s.executionEpochFencing && time.Now().Before(s.executionExpiresAt)
	current = s.executionGeneration
	currentExpiry := s.executionExpiresAt
	s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	ack := &pb.ExecutionEpochAck{
		Accepted:          accepted,
		CurrentGeneration: current,
		ExpiresAtUnixMs:   currentExpiry.UnixMilli(),
	}
	if !accepted {
		ack.ErrorMessage = "execution generation changed or fencing did not complete"
	}
	return ack, nil
}

func (s *Service) validateMutationGenerationLocked(generation uint64) error {
	if s.executionEpochStateErr != nil || s.executionEpochState == nil {
		return status.Error(codes.FailedPrecondition, "durable execution epoch state is unavailable")
	}
	if generation == 0 {
		if s.executionGeneration != 0 {
			return status.Errorf(codes.FailedPrecondition, "legacy generation 0 is fenced by execution generation %d", s.executionGeneration)
		}
		return nil
	}
	if generation != s.executionGeneration {
		return status.Errorf(codes.FailedPrecondition, "execution generation %d is not active; current generation is %d", generation, s.executionGeneration)
	}
	if s.executionExpiresAt.IsZero() || !time.Now().Before(s.executionExpiresAt) {
		return status.Error(codes.FailedPrecondition, "execution generation session has expired")
	}
	if s.executionEpochFencing {
		return status.Error(codes.Unavailable, "previous execution generation is still being fenced")
	}
	return nil
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
	if s.executionEpochTimer != nil {
		s.executionEpochTimer.Stop()
	}
	delay := time.Until(expiresAt)
	if delay < 0 {
		delay = 0
	}
	s.executionEpochTimer = time.AfterFunc(delay, func() {
		s.expireExecutionEpoch(generation, expiresAt)
	})
}

func (s *Service) expireExecutionEpoch(generation uint64, expiresAt time.Time) {
	s.mu.Lock()
	if s.executionGeneration != generation || !s.executionExpiresAt.Equal(expiresAt) || time.Now().Before(s.executionExpiresAt) {
		s.mu.Unlock()
		return
	}
	for _, subscriber := range s.subscribers {
		if subscriber != nil && subscriber.generation == generation {
			subscriber.fence()
		}
	}
	testIDs := make([]string, 0)
	for testID, assignment := range s.assignments {
		// Generation zero is legacy unfenced work. Once any positive generation
		// has been claimed it is lower-generation work too, and must not survive
		// the positive session's expiry if an earlier fencing attempt was cut short.
		if assignment.executionGeneration <= generation {
			testIDs = append(testIDs, testID)
		}
	}
	if len(testIDs) > 0 {
		s.executionEpochFencing = true
	}
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	_ = s.forceStopFencedTests(ctx, testIDs, generation)
	cancel()
}

func (s *Service) forceStopFencedTests(ctx context.Context, testIDs []string, maximumGeneration uint64) error {
	return s.forceStopTests(ctx, testIDs, maximumGeneration, "FAILED", "execution generation was fenced before test completion")
}

func (s *Service) forceStopTests(ctx context.Context, testIDs []string, maximumGeneration uint64, finalState string, finalError string) error {
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
	stops := s.detachFencedTests(testIDs, maximumGeneration)
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
	testID           string
	assignment       assignedTest
	executions       []*groupExecution
	writer           *archiveRecorder
	trackedFinalizer bool
	finalState       string
	finalError       string
}

func (s *Service) detachFencedTests(testIDs []string, maximumGeneration uint64) []detachedFencedTest {
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
		s.stoppingTests[testID] = struct{}{}
		delete(s.assignments, testID)
		s.stopCompletionTimerLocked(testID)
		s.stopStageTimerLocked(testID)
		executionMap := s.executions[testID]
		delete(s.executions, testID)
		executions := make([]*groupExecution, 0, len(executionMap))
		for _, execution := range executionMap {
			executions = append(executions, execution)
		}
		writer := s.archiveWriters[testID]
		delete(s.archiveWriters, testID)
		// Keep the epoch fenced until this exact detached test has both quiesced and
		// completed archive finalization. A retry cannot infer safety from the now
		// empty assignments map because the detached engine may still be running.
		s.executionFencePending++
		s.executionEpochFencing = true
		stops = append(stops, detachedFencedTest{
			testID:           testID,
			assignment:       assignment,
			executions:       executions,
			writer:           writer,
			trackedFinalizer: writer != nil && s.addFinalizer(),
		})
	}
	s.mu.Unlock()
	return stops
}

func (s *Service) finishDetachedFencedTest(ctx context.Context, stop detachedFencedTest) error {
	if err := waitForGeneratorExecutions(ctx, stop.executions); err != nil {
		finish := func() {
			_ = waitForGeneratorExecutions(context.Background(), stop.executions)
			s.finishStoppedTestWithState(stop.testID, stop.assignment, stop.writer, stop.finalState, stop.finalError)
			s.completeDetachedExecutionFence()
		}
		if stop.trackedFinalizer {
			go func() {
				defer s.finalizerWG.Done()
				finish()
			}()
		} else {
			go finish()
		}
		return err
	}
	if stop.trackedFinalizer {
		defer s.finalizerWG.Done()
	}
	defer s.completeDetachedExecutionFence()
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
	if s.executionFencePending > 0 {
		s.executionFencePending--
	}
	if s.executionFencePending == 0 && len(s.testIDsBeforeGenerationLocked(s.executionGeneration)) == 0 {
		s.executionEpochFencing = false
	}
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
