package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

const archiveCleanupAttemptTimeout = 2 * time.Minute
const archiveCleanupRetryInterval = time.Minute

type archiveCleanupOutbox interface {
	SaveArchiveCleanupJob(job *storage.ArchiveCleanupJob) error
	GetArchiveCleanupJob(testID string) (*storage.ArchiveCleanupJob, error)
	ListArchiveCleanupJobs(limit int) ([]*storage.ArchiveCleanupJob, error)
	MarkArchiveCleanupJobFailed(testID string, cleanupErr error, updatedAt time.Time) error
	DeleteArchiveCleanupJob(testID string) error
}

func (s *Server) archiveCleanupOutbox() archiveCleanupOutbox {
	if s == nil || s.store == nil {
		return nil
	}
	outbox, _ := s.store.(archiveCleanupOutbox)
	return outbox
}

// cleanupQueuedArchive uses a bounded background context: once metadata deletion
// commits, client cancellation must not interrupt best-effort object cleanup.
// Any failure leaves (and annotates) the durable outbox job for startup retry.
func (s *Server) cleanupQueuedArchive(record *storage.RunArchiveRecord) error {
	if record == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), archiveCleanupAttemptTimeout)
	defer cancel()
	if err := s.pruneRunArchive(ctx, record); err != nil {
		s.markArchiveCleanupFailed(record.TestID, err)
		return err
	}
	if outbox := s.archiveCleanupOutbox(); outbox != nil {
		if err := outbox.DeleteArchiveCleanupJob(record.TestID); err != nil {
			s.markArchiveCleanupFailed(record.TestID, err)
			return fmt.Errorf("complete archive cleanup job: %w", err)
		}
	}
	return nil
}

func (s *Server) cleanupQueuedArchiveUnderFence(record *storage.RunArchiveRecord) error {
	if record == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), archiveCleanupAttemptTimeout)
	defer cancel()
	if s.runner != nil {
		operationCtx, operationCancel := s.runner.archiveOperationContext(ctx)
		defer operationCancel()
		ctx = operationCtx
	}
	if err := s.pruneRunArchiveUnderFence(ctx, record); err != nil {
		s.markArchiveCleanupFailed(record.TestID, err)
		return err
	}
	if outbox := s.archiveCleanupOutbox(); outbox != nil {
		if err := outbox.DeleteArchiveCleanupJob(record.TestID); err != nil {
			s.markArchiveCleanupFailed(record.TestID, err)
			return fmt.Errorf("complete archive cleanup job: %w", err)
		}
	}
	return nil
}

func (s *Server) markArchiveCleanupFailed(testID string, cleanupErr error) {
	outbox := s.archiveCleanupOutbox()
	if outbox == nil {
		return
	}
	if err := outbox.MarkArchiveCleanupJobFailed(testID, cleanupErr, time.Now().UTC()); err != nil {
		slog.Warn("failed to annotate archive cleanup outbox", "test_id", testID, "cleanup_error", cleanupErr, "error", err)
	}
}

func (s *Server) retryArchiveCleanupJobs(ctx context.Context) error {
	outbox := s.archiveCleanupOutbox()
	if outbox == nil {
		return nil
	}
	jobs, err := outbox.ListArchiveCleanupJobs(1000)
	if err != nil {
		return fmt.Errorf("list archive cleanup jobs: %w", err)
	}
	failures := make([]error, 0)
	for _, job := range jobs {
		if job == nil || strings.TrimSpace(job.TestID) == "" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		attemptCtx, cancel := context.WithTimeout(ctx, archiveCleanupAttemptTimeout)
		err := s.pruneRunArchive(attemptCtx, &job.ArchiveRecord)
		cancel()
		if err != nil {
			s.markArchiveCleanupFailed(job.TestID, err)
			failures = append(failures, fmt.Errorf("%s: %w", job.TestID, err))
			continue
		}
		if err := outbox.DeleteArchiveCleanupJob(job.TestID); err != nil {
			s.markArchiveCleanupFailed(job.TestID, err)
			failures = append(failures, fmt.Errorf("complete %s: %w", job.TestID, err))
		}
	}
	return errors.Join(failures...)
}

func (s *Server) startArchiveCleanupRetry() {
	if s == nil || s.archiveCleanupOutbox() == nil || s.archiveCleanupCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.archiveCleanupCancel = cancel
	s.archiveCleanupDone = make(chan struct{})
	go func() {
		defer close(s.archiveCleanupDone)
		ticker := time.NewTicker(archiveCleanupRetryInterval)
		defer ticker.Stop()
		runSweep := func() {
			if !s.isExecutionLeader() {
				return
			}
			sweepCtx, sweepCancel := context.WithTimeout(ctx, 10*time.Minute)
			defer sweepCancel()
			if err := s.retryArchiveCleanupJobs(sweepCtx); err != nil && ctx.Err() == nil {
				slog.Warn("archive cleanup outbox retry incomplete", "error", err)
			}
		}
		runSweep()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runSweep()
			}
		}
	}()
}
