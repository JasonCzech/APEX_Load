package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

const archiveCollectionNetworkAttempts = 3
const archiveCollectionTerminalDeadline = 6 * time.Hour

type exhaustedArchiveFailureReconciler interface {
	ReconcileExhaustedRunArchiveFailures(string, string, int) (int64, error)
}

// This worker owns its transports and a detached terminal snapshot. It never
// dispatches VUsers or changes lifecycle status, and cannot block publication.
func (tr *TestRunner) runArchiveCollectionRetry(ctx context.Context) {
	ticker := time.NewTicker(archiveFinalizationRetryInterval)
	defer ticker.Stop()
	for {
		attempt, cancel := context.WithTimeout(ctx, archiveFinalizationAttemptTimeout)
		err := tr.retryRunArchiveCollections(attempt, time.Now().UTC())
		cancel()
		if err != nil && ctx.Err() == nil && !errors.Is(err, errNotExecutionLeader) {
			slog.Warn("archive collection retry incomplete", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (tr *TestRunner) retryRunArchiveCollections(ctx context.Context, now time.Time) error {
	store := tr.archiveCollectionStore()
	if store == nil || tr.archiveStore == nil || tr.testStore == nil {
		return nil
	}
	if !tr.isExecutionLeader() {
		return errNotExecutionLeader
	}
	backend, ok := tr.getExecutionBackend().(*distributedExecutionBackend)
	if !ok {
		return nil
	}
	ctx, cancel := tr.archiveOperationContext(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	if repair, ok := tr.testStore.(exhaustedArchiveFailureReconciler); ok {
		lease, owner := "", ""
		if tr.durableExecutionFence {
			_, holder, _, err := tr.generatorExecutionClaim()
			if err != nil || holder == "" {
				return errNotExecutionLeader
			}
			lease, owner = executionLeaseName, holder
		}
		if err := tr.trackDurableWrite(func() error {
			_, err := repair.ReconcileExhaustedRunArchiveFailures(lease, owner, 100)
			return err
		}); err != nil {
			return fmt.Errorf("repair exhausted archive collection failure: %w", err)
		}
	}
	jobs, err := store.ListRunArchiveCollections(100, now.Add(-archiveFinalizationRetryInterval))
	if err != nil {
		return err
	}
	var failures []error
	for _, job := range jobs {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		if err := tr.retryRunArchiveCollection(ctx, backend, job, now); err != nil {
			failures = append(failures, fmt.Errorf("collection %s: %w", job.TestID, err))
		}
	}
	return errors.Join(failures...)
}

func (tr *TestRunner) retryRunArchiveCollection(ctx context.Context, base *distributedExecutionBackend, listed *storage.RunArchiveCollectionIntent, now time.Time) (attemptErr error) {
	if listed == nil {
		return nil
	}
	ctx, cancel := tr.archiveOperationContext(ctx)
	defer cancel()
	lockCtx, lockCancel := context.WithTimeout(ctx, time.Second)
	release, err := lockArchivePublicationSource(lockCtx, listed.TestID)
	lockCancel()
	if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
		return nil
	}
	if err != nil {
		return err
	}
	defer release()
	store := tr.archiveCollectionStore()
	intent, err := store.GetRunArchiveCollection(listed.TestID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if intent == nil || intent.IntentToken != listed.IntentToken || intent.Attempts != listed.Attempts || intent.Resolved || intent.Exhausted {
		return nil
	}
	if intent.Attempts > 0 && intent.UpdatedAt.After(now.Add(-archiveFinalizationRetryInterval)) {
		return nil
	}
	row, err := tr.testStore.GetTest(intent.TestID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if row == nil || row.EndTime == nil || !isTerminalTestStatus(row.Status) || normalizeProjectID(row.ProjectID) != normalizeProjectID(intent.ProjectID) || !archiveRunTimestampMatches(row.StartTime, intent.StartTime, tr.testStore) {
		return nil
	}
	test := managedTestFromRecord(row)
	if existing, err := tr.existingCollectedArchive(test); err != nil || existing != nil {
		return err
	}
	lease, owner := "", ""
	if tr.durableExecutionFence {
		_, owner, _, err = tr.generatorExecutionClaim()
		if err != nil {
			return err
		}
		lease = executionLeaseName
	}
	final := intent.Attempts >= archiveCollectionNetworkAttempts || now.Sub(*row.EndTime) >= archiveCollectionTerminalDeadline
	if !final || intent.Attempts == 0 {
		claimed, err := store.ClaimRunArchiveCollectionAttempt(intent.TestID, intent.IntentToken, intent.Attempts, lease, owner)
		if err != nil || !claimed {
			return err
		}
		intent.Attempts++
	}
	// A final cache-only decision consumes no new network attempt. Every terminal
	// failure path is exhausted, including corrupt preexisting sources; cancellation
	// leaves it recoverable by the next owner. A successful outbox insert resolves
	// the intent atomically, so this CAS cannot mark it exhausted afterwards.
	defer func() {
		if final && ctx.Err() == nil {
			_, err := store.ExhaustRunArchiveCollection(intent.TestID, intent.IntentToken, intent.Attempts, lease, owner)
			attemptErr = errors.Join(attemptErr, err)
		}
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	// A committed merged source may have survived a crash before its outbox write.
	// Recover it first, without redownloading or replacing it.
	sourcePath := filepath.Join(resolveArchiveRoot(), "remote", test.ID)
	if info, statErr := os.Lstat(sourcePath); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("invalid existing collection source")
		}
		creator, ok := tr.archiveStore.(incarnationOrphanArchiveFinalizationStore)
		if !ok {
			return errors.New("fenced orphan publication unavailable")
		}
		return tr.recoverOrphanedLocalRunArchiveUnderSourceFence(ctx, creator, orphanArchiveCandidate{path: sourcePath, identity: info})
	} else if !os.IsNotExist(statErr) {
		return statErr
	}
	targets := make([]string, 0, len(intent.Sources))
	for target := range intent.Sources {
		targets = append(targets, target)
	}
	sort.Strings(targets)
	backend := newDistributedExecutionBackend(targets, base.timeout, base.dial)
	// Suppress fresh inventory probes when reconstructing historical provenance.
	test.executionBackend = struct{ testExecutionBackend }{backend}
	test.remoteClientsByTarget = make(map[string]generatorClient, len(targets))
	for _, target := range targets {
		test.remoteClientsByTarget[target] = nil
	}
	if !final {
		clients := dialCollectionTargets(ctx, backend, targets)
		for target, client := range clients {
			test.remoteClientsByTarget[target] = client
		}
		defer func() {
			for _, client := range clients {
				if client != nil {
					_ = client.Close()
				}
			}
		}()
	}
	// Private helper requires the source fence held above; it must not reacquire it.
	record, collectionErr := backend.stageRemoteArchiveUnderSourceFence(ctx, tr, test, row.Status, row.Error, *row.EndTime, !final, nil, true)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if record != nil && collectionErr == nil {
		creator, ok := tr.archiveStore.(incarnationOrphanArchiveFinalizationStore)
		if !ok {
			return errors.New("fenced collection publication unavailable")
		}
		saved, err := creator.SaveArchiveFinalizationJobIfOrphanForRun(&storage.ArchiveFinalizationJob{TestID: test.ID, ProjectID: test.ProjectID, ArchiveRecord: *record, PersistArtifacts: true}, row.StartTime, row.Status, lease, owner)
		if err != nil {
			return err
		}
		if !saved {
			return errors.New("collection publication was fenced")
		}
		return nil
	}
	return collectionErr
}

func dialCollectionTargets(ctx context.Context, backend *distributedExecutionBackend, targets []string) map[string]generatorClient {
	clients := make(map[string]generatorClient, len(targets))
	var mu sync.Mutex
	var workers sync.WaitGroup
	semaphore := make(chan struct{}, remoteArchiveFetchConcurrency)
	for _, target := range targets {
		workers.Go(func() {
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				return
			}
			if ctx.Err() != nil {
				return
			}
			dialCtx, cancel := context.WithTimeout(ctx, backend.timeout)
			client, err := backend.dial(dialCtx, target)
			cancel()
			if err != nil {
				if client != nil {
					_ = client.Close()
				}
				return
			}
			if client != nil {
				mu.Lock()
				clients[target] = client
				mu.Unlock()
			}
		})
	}
	workers.Wait()
	return clients
}
