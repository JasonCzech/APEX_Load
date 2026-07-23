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
	testID                   string
	assignment               assignedTest
	executions               []*groupExecution
	writer                   *archiveRecorder
	trackedFinalizer         bool
	persistentJavaDrainToken uint64
	finalState               string
	finalError               string
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
		s.markStoppingLocked(testID, assignment.executionGeneration)
		persistentJavaDrainToken := s.reserveAssignmentJavaDrainLocked(assignment)
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
		// Keep the epoch fenced until this exact detached test has both quiesced and
		// completed archive finalization. A retry cannot infer safety from the now
		// empty assignments map because the detached engine may still be running.
		s.executionEpochManager.beginDetachedFencesLocked(1)
		stops = append(stops, detachedFencedTest{
			testID:                   testID,
			assignment:               assignment,
			executions:               executions,
			writer:                   writer,
			trackedFinalizer:         writer != nil && s.archiveManager.addFinalizer(),
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
			s.completeDetachedExecutionFence()
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
	defer s.completeDetachedExecutionFence()
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
