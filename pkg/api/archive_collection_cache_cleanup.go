package api

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

type collectionCacheCleanupCandidate struct {
	testID, token string
	identity      os.FileInfo
}

// File-lock acquisition checks the actual deadline even before a context timer
// publishes Err. Cleanup must use that same authority boundary when deciding
// whether a busy-source timeout belongs to its own retry window or its caller.
func collectionCacheCleanupContextError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if deadline, ok := ctx.Deadline(); ok && !deadline.After(time.Now()) {
		return context.DeadlineExceeded
	}
	return nil
}

// Discover local copies, not global SQL jobs: controllers may use independent
// staging roots. SQL anchors remain available to every controller until normal
// test retention removes them. Unanchored local evidence is never pruned here.
func discoverCollectionCacheCleanupCandidates(ctx context.Context, root *os.Root) ([]collectionCacheCleanupCandidate, error) {
	// Match the physical-admission entry bound so a valid admitted cache cannot
	// strand later candidates behind a smaller discovery limit.
	remaining := 100000
	var candidates []collectionCacheCleanupCandidate
	var walk func(*os.Root, string) error
	walk = func(parent *os.Root, testID string) error {
		dir, err := parent.Open(".")
		if err != nil {
			return err
		}
		defer dir.Close()
		for {
			if err := collectionCacheCleanupContextError(ctx); err != nil {
				return err
			}
			entries, readErr := dir.ReadDir(128)
			for _, entry := range entries {
				remaining--
				if remaining < 0 {
					return errors.New("collection cache cleanup scan limit exceeded")
				}
				if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
					continue
				}
				if testID == "" {
					if validateArchiveRunID(entry.Name()) != nil {
						continue
					}
				} else if validateArchiveIntentToken(entry.Name()) != nil {
					continue
				}
				child, err := openArchiveChildDirectoryAt(parent, entry.Name(), false, 0)
				if err != nil {
					return err
				}
				if testID == "" {
					err = walk(child, entry.Name())
				} else {
					info, statErr := child.Stat(".")
					err = statErr
					if err == nil {
						candidates = append(candidates, collectionCacheCleanupCandidate{testID, entry.Name(), info})
					}
				}
				_ = child.Close()
				if err != nil {
					return err
				}
			}
			if readErr == io.EOF {
				return nil
			}
			if readErr != nil {
				return readErr
			}
		}
	}
	err := walk(root, "")
	return candidates, err
}

func (tr *TestRunner) retirePublishedCollectionCaches(ctx context.Context) error {
	if tr == nil || tr.archiveCollectionStore() == nil || tr.archiveStore == nil || tr.testStore == nil {
		return nil
	}
	path, err := resolveArchiveDirectoryRoot(resolveArchiveRoot(), false)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	archiveRoot, err := openArchiveDirectoryNoSymlink(path)
	if err != nil {
		return err
	}
	defer archiveRoot.Close()
	root, err := openArchiveChildDirectoryAt(archiveRoot, "_collection", false, 0)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer root.Close()
	candidates, scanErr := discoverCollectionCacheCleanupCandidates(ctx, root)
	failures := []error{scanErr}
	for _, candidate := range candidates {
		if err := collectionCacheCleanupContextError(ctx); err != nil {
			return errors.Join(append(failures, err)...)
		}
		if err := tr.retirePublishedCollectionCache(ctx, root, candidate); err != nil {
			failures = append(failures, fmt.Errorf("collection cache %s: %w", candidate.testID, err))
		}
	}
	return errors.Join(failures...)
}

func (tr *TestRunner) retirePublishedCollectionCache(ctx context.Context, root *os.Root, candidate collectionCacheCleanupCandidate) error {
	intent, err := tr.archiveCollectionStore().GetRunArchiveCollection(candidate.testID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if intent == nil || intent.IntentToken != candidate.token || !intent.Resolved || len(intent.CachedSources) == 0 {
		return nil
	}
	lockCtx, cancel := context.WithTimeout(ctx, time.Second)
	release, err := lockArchivePublicationSource(lockCtx, candidate.testID)
	cancel()
	if errors.Is(err, context.DeadlineExceeded) && collectionCacheCleanupContextError(ctx) == nil {
		return nil
	}
	if err != nil {
		return err
	}
	defer release()
	// Reload after waiting: deletion/replacement must not inherit cleanup authority.
	intent, err = tr.archiveCollectionStore().GetRunArchiveCollection(candidate.testID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if intent == nil || intent.IntentToken != candidate.token || !intent.Resolved {
		return nil
	}
	testRoot, err := openArchiveChildDirectoryAt(root, candidate.testID, false, 0)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer testRoot.Close()
	tokenRoot, err := openArchiveChildDirectoryAt(testRoot, candidate.token, false, 0)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer tokenRoot.Close()
	info, err := tokenRoot.Stat(".")
	if err != nil {
		return err
	}
	if !os.SameFile(info, candidate.identity) {
		return errors.New("collection cache directory changed after discovery")
	}
	cache := &collectionSourceCache{root: root, intent: intent}
	localTargets := make(map[string]bool)
	for target, anchor := range intent.CachedSources {
		parent, err := cache.targetRoot(target, false)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		info, err := parent.Lstat(anchor.ReceiptSHA256)
		_ = parent.Close()
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("anchored collection source is not a directory")
		}
		localTargets[target] = true
	}
	if len(localTargets) == 0 {
		return nil
	}
	manifest, err := tr.confirmCollectionCachePublication(ctx, intent, localTargets)
	if err != nil {
		return err
	}
	if manifest == nil {
		return nil
	}
	confirmed := make(map[string]bool)
	for _, source := range manifest.SourceCoverage.Sources {
		confirmed[source.Target] = source.Collected
	}
	targets := make([]string, 0, len(intent.CachedSources))
	for target := range intent.CachedSources {
		targets = append(targets, target)
	}
	sort.Strings(targets)
	for _, target := range targets {
		if !localTargets[target] || !confirmed[target] {
			continue
		}
		if err := collectionCacheCleanupContextError(ctx); err != nil {
			return err
		}
		source, err := cache.load(ctx, target)
		if err != nil {
			return err
		}
		if source == nil {
			continue
		}
		identity, err := source.root.Stat(".")
		if err == nil {
			err = verifyCollectionCacheForCleanup(ctx, source)
		}
		source.close()
		if err != nil {
			return err
		}
		relative := filepath.Join(candidate.testID, candidate.token, sha256BytesHex([]byte(target)), intent.CachedSources[target].ReceiptSHA256)
		unlock, err := lockArchiveNamespace(ctx, "_collection_locks", "capacity.lock")
		if err != nil {
			return err
		}
		err = collectionCacheCleanupContextError(ctx)
		if err == nil {
			err = removeArchiveDirectoryAt(root, relative, identity)
		}
		if err == nil { // Only empty parents may disappear; unanchored siblings remain.
			_ = root.Remove(filepath.Dir(relative))
			_ = root.Remove(filepath.Join(candidate.testID, candidate.token))
			_ = root.Remove(candidate.testID)
		}
		unlock()
		if err != nil {
			return err
		}
	}
	return nil
}

func (tr *TestRunner) confirmCollectionCachePublication(ctx context.Context, intent *storage.RunArchiveCollectionIntent, localTargets map[string]bool) (*RunArchiveManifest, error) {
	row, err := tr.testStore.GetTest(intent.TestID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if row == nil || row.EndTime == nil || !isTerminalTestStatus(row.Status) || normalizeProjectID(row.ProjectID) != normalizeProjectID(intent.ProjectID) || !archiveRunTimestampMatches(row.StartTime, intent.StartTime, tr.testStore) {
		return nil, nil
	}
	if outbox := tr.archiveFinalizationOutbox(); outbox != nil {
		pending, err := outbox.GetArchiveFinalizationJob(intent.TestID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if pending != nil {
			return nil, nil
		}
	} else {
		return nil, nil
	}
	if cleanup, ok := tr.archiveStore.(archiveCleanupOutbox); ok {
		pending, err := cleanup.GetArchiveCleanupJob(intent.TestID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if pending != nil {
			return nil, nil
		}
	}
	record, err := tr.archiveStore.GetRunArchive(intent.TestID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if record == nil {
		return nil, nil
	}
	digest, err := hex.DecodeString(record.ManifestSHA256)
	if err != nil || len(digest) != sha256.Size {
		return nil, errors.New("published archive lacks a manifest trust anchor")
	}
	if record.TestID != intent.TestID || !archiveRunTimestampMatches(record.CompletedAt, *row.EndTime, tr.testStore) {
		return nil, errors.New("published archive belongs to another terminal incarnation")
	}
	var data []byte
	var localRoot *os.Root
	if archiveRecordBackend(record) == archiveBackendFilesystem {
		if err := validateArchivePublicationPaths(record); err != nil {
			return nil, err
		}
		localRoot, err = openArchiveDirectoryNoSymlink(record.ArchivePath)
		if err != nil {
			return nil, err
		}
		defer localRoot.Close()
		data, _, err = readArchiveFileAtRootLimitedContext(ctx, localRoot, runArchiveManifestFileName, maxArchiveManifestDownloadBytes)
	} else {
		if err := validatePublishedArchiveSourceRecord(record); err != nil {
			return nil, err
		}
		backend, err := archiveMaterializationRuntime(currentArchiveBackend(), record)
		if err != nil {
			return nil, err
		}
		bucket, err := backend.openBucket(ctx)
		if err != nil {
			return nil, err
		}
		defer bucket.Close()
		remote, readErr := readArchiveObjectLimited(ctx, bucket, backend.objectKey(record.TestID, runArchiveManifestFileName), maxArchiveManifestDownloadBytes)
		if readErr != nil {
			return nil, readErr
		}
		data = remote
	}
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(sha256BytesHex(data), record.ManifestSHA256) {
		return nil, errors.New("published archive commit marker differs from catalog")
	}
	manifest, _, err := decodeMaterializationManifest(record, data)
	if err != nil {
		return nil, err
	}
	if manifest.Status != row.Status || !archiveRunTimestampMatches(archiveManifestRequestedAt(manifest), row.StartTime, tr.testStore) || !archiveRunTimestampMatches(manifest.CompletedAt, *row.EndTime, tr.testStore) {
		return nil, errors.New("published manifest belongs to another terminal outcome")
	}
	if project := strings.TrimSpace(manifest.Provenance.ProjectID); project != "" && normalizeProjectID(project) != normalizeProjectID(intent.ProjectID) {
		return nil, errors.New("published manifest belongs to another project")
	}
	if manifest.SourceCoverage == nil {
		return nil, errors.New("published archive lacks source coverage")
	}
	collected := make(map[string]bool)
	for _, source := range manifest.SourceCoverage.Sources {
		if _, ok := collected[source.Target]; ok {
			return nil, errors.New("published archive has duplicate source coverage")
		}
		collected[source.Target] = source.Collected
	}
	eligible := false
	for target := range localTargets {
		if collected[target] {
			eligible = true
			break
		}
	}
	if !eligible {
		return nil, nil
	}
	if localRoot != nil {
		if err := validateOrphanArchiveArtifacts(ctx, localRoot, manifest, int64(len(data))); err != nil {
			return nil, err
		}
	}
	return manifest, nil
}

func verifyCollectionCacheForCleanup(ctx context.Context, source *cachedArchiveSource) error {
	directory, err := source.root.Open(".")
	if err != nil {
		return err
	}
	entries, readErr := directory.ReadDir(5)
	_ = directory.Close()
	if readErr != nil && readErr != io.EOF {
		return readErr
	}
	expected := map[string]bool{"receipt.json": true, "transactions.jsonl": true, "resources.jsonl": true, "interval_summaries.jsonl": true}
	if len(entries) != len(expected) {
		return errors.New("collection cache contains unexpected files")
	}
	for _, entry := range entries {
		if !expected[entry.Name()] || entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return errors.New("collection cache contains unexpected entries")
		}
	}
	for _, descriptor := range source.receipt.Files {
		file, info, err := openArchiveArtifactForReadAt(source.root, descriptor.Name)
		if err != nil {
			return err
		}
		if info.Size() != descriptor.Bytes {
			_ = file.Close()
			return errors.New("cached payload size changed before cleanup")
		}
		hash := sha256.New()
		n, err := copyArchiveArtifactContext(ctx, hash, io.LimitReader(file, descriptor.Bytes+1))
		_ = file.Close()
		if err != nil {
			return err
		}
		if n != descriptor.Bytes || hex.EncodeToString(hash.Sum(nil)) != descriptor.SHA256 {
			return errors.New("cached payload changed before cleanup")
		}
	}
	return collectionCacheCleanupContextError(ctx)
}

// Cleanup runs separately from publication retries. Large confirmed copies must
// not delay new durable publication jobs; both workers share source file fences.
func (tr *TestRunner) runCollectionCacheCleanup(ctx context.Context) {
	ticker := time.NewTicker(archiveFinalizationRetryInterval)
	defer ticker.Stop()
	for {
		attempt, cancel := context.WithTimeout(ctx, archiveFinalizationAttemptTimeout)
		err := tr.retirePublishedCollectionCaches(attempt)
		cancel()
		if err != nil && ctx.Err() == nil {
			slog.Warn("archive collection cache cleanup incomplete", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
