package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

const archiveCleanupAttemptTimeout = 2 * time.Minute
const archiveCleanupRetryInterval = time.Minute
const archiveVersionCleanupSafetyMargin = 5 * time.Minute

type archiveCleanupOutbox interface {
	SaveArchiveCleanupJob(job *storage.ArchiveCleanupJob) error
	GetArchiveCleanupJob(testID string) (*storage.ArchiveCleanupJob, error)
	ListArchiveCleanupJobs(limit int) ([]*storage.ArchiveCleanupJob, error)
	MarkArchiveCleanupJobFailed(testID string, cleanupErr error, updatedAt time.Time) error
	DeleteArchiveCleanupJob(testID string) error
}

type conditionalArchiveCleanupOutbox interface {
	SaveArchiveCleanupJobIfArchiveUnchanged(job *storage.ArchiveCleanupJob) (bool, error)
}

type archiveVersionCleanupOutbox interface {
	SaveArchiveVersionCleanupJob(job *storage.ArchiveVersionCleanupJob) error
	ListArchiveVersionCleanupJobs(limit int) ([]*storage.ArchiveVersionCleanupJob, error)
	MarkArchiveVersionCleanupJobFailed(id string, cleanupErr error, updatedAt time.Time) error
	DeleteArchiveVersionCleanupJob(id string) error
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
	if versionOutbox, ok := s.store.(archiveVersionCleanupOutbox); ok {
		versionFailures, err := s.retryArchiveVersionCleanupJobs(ctx, versionOutbox)
		if err != nil {
			failures = append(failures, err)
		}
		failures = append(failures, versionFailures...)
	}
	return errors.Join(failures...)
}

func (s *Server) retryArchiveVersionCleanupJobs(ctx context.Context, outbox archiveVersionCleanupOutbox) ([]error, error) {
	jobs, err := outbox.ListArchiveVersionCleanupJobs(1000)
	if err != nil {
		return nil, fmt.Errorf("list superseded archive cleanup jobs: %w", err)
	}
	failures := make([]error, 0)
	for _, job := range jobs {
		if job == nil || strings.TrimSpace(job.ID) == "" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return failures, err
		}
		// Other HA replicas may have begun materializing the old catalog pointer
		// immediately before the replacement committed. Keep it for at least one
		// complete materialization deadline before physical retirement.
		if job.CreatedAt.Add(configuredArchiveMaterializationTimeout() + archiveVersionCleanupSafetyMargin).After(time.Now().UTC()) {
			continue
		}
		attemptCtx, cancel := context.WithTimeout(ctx, archiveCleanupAttemptTimeout)
		err := s.pruneSupersededArchiveVersion(attemptCtx, &job.ArchiveRecord)
		cancel()
		if err != nil {
			if markErr := outbox.MarkArchiveVersionCleanupJobFailed(job.ID, err, time.Now().UTC()); markErr != nil {
				slog.Warn("failed to annotate superseded archive cleanup outbox", "job_id", job.ID, "cleanup_error", err, "error", markErr)
			}
			failures = append(failures, fmt.Errorf("%s: %w", job.ID, err))
			continue
		}
		if err := outbox.DeleteArchiveVersionCleanupJob(job.ID); err != nil {
			_ = outbox.MarkArchiveVersionCleanupJobFailed(job.ID, err, time.Now().UTC())
			failures = append(failures, fmt.Errorf("complete %s: %w", job.ID, err))
		}
	}
	return failures, nil
}

func (tr *TestRunner) enqueueArchiveVersionCleanup(projectID string, record *storage.RunArchiveRecord) error {
	if tr == nil || tr.archiveStore == nil || record == nil {
		return nil
	}
	outbox, ok := tr.archiveStore.(archiveVersionCleanupOutbox)
	if !ok {
		return nil
	}
	copy := *record
	return outbox.SaveArchiveVersionCleanupJob(&storage.ArchiveVersionCleanupJob{
		TestID: record.TestID, ProjectID: normalizeProjectID(projectID), ArchiveRecord: copy,
	})
}

// pruneSupersededArchiveVersion removes only the immutable object/local pointer
// serialized in the version cleanup job. It never mutates run_archives or the
// live test pointer, which now identify a newer publication.
func (s *Server) pruneSupersededArchiveVersion(ctx context.Context, record *storage.RunArchiveRecord) error {
	if record == nil || strings.TrimSpace(record.TestID) == "" {
		return fmt.Errorf("superseded run archive record is required")
	}
	if s.runner != nil {
		if !s.runner.isExecutionLeader() {
			return errNotExecutionLeader
		}
		unlock := s.runner.lockArchiveCommitFence(record.TestID)
		defer unlock()
		operationCtx, cancel := s.runner.archiveOperationContext(ctx)
		defer cancel()
		ctx = operationCtx
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var current *storage.RunArchiveRecord
	if s.store != nil {
		loaded, err := s.store.GetRunArchive(record.TestID)
		if err == nil {
			current = loaded
		} else if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("load current archive before superseded cleanup: %w", err)
		}
	}
	if current != nil && sameArchivePublicationPointer(current, record) {
		// A defensive no-op: never delete the version still named by the catalog.
		return nil
	}
	if err := deleteArchiveBackendObjects(ctx, record); err != nil {
		return err
	}
	if current == nil || !sameArchiveLocalPath(current.ArchivePath, record.ArchivePath) {
		if err := removeLocalArchiveDirectory(record.ArchivePath); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func sameArchivePublicationPointer(left, right *storage.RunArchiveRecord) bool {
	if left == nil || right == nil {
		return false
	}
	leftURI := strings.TrimSpace(left.ArchiveURI)
	if leftURI == "" {
		leftURI = strings.TrimSpace(left.ManifestURI)
	}
	rightURI := strings.TrimSpace(right.ArchiveURI)
	if rightURI == "" {
		rightURI = strings.TrimSpace(right.ManifestURI)
	}
	return leftURI != "" && leftURI == rightURI
}

func sameArchiveLocalPath(left, right string) bool {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if left == "" || right == "" {
		return false
	}
	leftAbs, leftErr := filepath.Abs(left)
	rightAbs, rightErr := filepath.Abs(right)
	return leftErr == nil && rightErr == nil && filepath.Clean(leftAbs) == filepath.Clean(rightAbs)
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
