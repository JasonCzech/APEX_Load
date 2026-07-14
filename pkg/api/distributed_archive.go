package api

// distributed_archive.go — split from distributed_execution_backend.go (same package, behavior-preserving).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/storage"
	grpcCodes "google.golang.org/grpc/codes"
	grpcStatus "google.golang.org/grpc/status"
)

const (
	remoteArchiveManifestMaxBytes = int64(4 << 20)
	// Archive datasets can legitimately exceed gRPC's 4 MiB default and the old
	// unary call's 64 MiB override. These are safety ceilings, not buffer sizes:
	// production transfers and merges remain bounded to one 1 MiB gRPC chunk.
	remoteArchiveArtifactMaxBytes = int64(16 << 30)
	remoteArchiveTotalMaxBytes    = int64(32 << 30)
	remoteArchiveMaxTargets       = 256
	remoteArchiveFetchConcurrency = 8
	remoteArchiveMergeBufferBytes = 256 << 10
)

type streamingArchiveGeneratorClient interface {
	StreamArchiveArtifact(context.Context, generatorrpc.ArchiveArtifactRequest, io.Writer, int64) (int64, error)
}

func (b *distributedExecutionBackend) stageRemoteArchive(tr *TestRunner, test *ManagedTest, finalStatus, finalError string, completedAt time.Time, requireAll bool) (*storage.RunArchiveRecord, error) {
	if test == nil {
		return nil, fmt.Errorf("test is required")
	}
	if err := validateArchiveRunID(test.ID); err != nil {
		return nil, err
	}
	archiveRoot, err := resolveArchiveDirectoryRoot(resolveArchiveRoot(), true)
	if err != nil {
		return nil, fmt.Errorf("prepare distributed archive root: %w", err)
	}
	archiveParent, err := ensureArchiveRelativeDirectory(archiveRoot, "remote", 0o755)
	if err != nil {
		return nil, fmt.Errorf("prepare distributed archive parent: %w", err)
	}
	archiveDir := filepath.Join(archiveParent, test.ID)
	if archiveInfo, statErr := os.Lstat(archiveDir); statErr == nil {
		if archiveInfo.Mode()&os.ModeSymlink != 0 || !archiveInfo.IsDir() {
			return nil, fmt.Errorf("existing distributed archive run root %q is not a non-symlink directory", archiveDir)
		}
	} else if !os.IsNotExist(statErr) {
		return nil, statErr
	}

	bundles, err := b.fetchRemoteArchiveBundles(tr, test, requireAll)
	if err != nil {
		return nil, err
	}
	defer cleanupRemoteArchiveBundles(bundles)
	if len(bundles) == 0 {
		if requireAll {
			return nil, fmt.Errorf("no remote archive bundles were available")
		}
		return nil, nil
	}

	manifest := mergeRemoteArchiveManifest(tr, test, bundles, finalStatus, finalError, completedAt)
	workingDir, err := createArchiveTemporaryDirectory(archiveParent, filepath.Base(archiveDir)+"-staging", 0o755)
	if err != nil {
		return nil, err
	}
	defer func() { _ = removeArchiveSubdirectory(archiveParent, workingDir) }()

	if err := writeMergedRemoteArchiveFiles(workingDir, &manifest, bundles); err != nil {
		return nil, err
	}
	if err := writeArchiveParquetFiles(workingDir, &manifest); err != nil {
		return nil, err
	}
	if err := updateRunArchiveArtifactIntegrity(workingDir, &manifest); err != nil {
		return nil, err
	}
	manifestContent, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	workingManifestPath := filepath.Join(workingDir, runArchiveManifestFileName)
	if err := writeSyncedArchiveFile(workingManifestPath, manifestContent); err != nil {
		return nil, err
	}
	if err := atomicReplaceArchiveDirectory(workingDir, archiveDir); err != nil {
		return nil, err
	}

	archiveBackend := currentArchiveBackend()
	record := &storage.RunArchiveRecord{
		TestID:               manifest.TestID,
		RefKey:               test.RefKey,
		SchemaVersion:        manifest.SchemaVersion,
		ArchiveBackend:       archiveBackend.Backend,
		ArchivePath:          archiveDir,
		ManifestPath:         filepath.Join(archiveDir, runArchiveManifestFileName),
		ManifestSHA256:       sha256BytesHex(manifestContent),
		ArchiveURI:           archiveBackend.runURI(manifest.TestID),
		ManifestURI:          archiveBackend.manifestURI(manifest.TestID),
		TransactionCount:     manifest.TransactionCount,
		ResourceSampleCount:  manifest.ResourceSampleCount,
		IntervalSummaryCount: manifest.IntervalSummaryCount,
		CreatedAt:            manifest.CreatedAt,
		CompletedAt:          manifest.CompletedAt,
	}

	// Publication is deliberately deferred to the durable archive-finalization
	// outbox. The merged local directory is the recoverable source of truth until
	// adjunct generation, object upload, and the catalog UPSERT all commit.
	return record, nil
}

type remoteArchiveBundle struct {
	target            string
	manifest          RunArchiveManifest
	transactionsPath  string
	resourcesPath     string
	summariesPath     string
	transferStageRoot string
}

func cleanupRemoteArchiveBundles(bundles []remoteArchiveBundle) {
	seen := make(map[string]struct{})
	for _, bundle := range bundles {
		root := strings.TrimSpace(bundle.transferStageRoot)
		if root == "" {
			continue
		}
		if _, ok := seen[root]; ok {
			continue
		}
		seen[root] = struct{}{}
		_ = removeArchiveSubdirectory(filepath.Dir(root), root)
	}
}

func (b *distributedExecutionBackend) fetchRemoteArchiveBundles(tr *TestRunner, test *ManagedTest, requireAll bool) ([]remoteArchiveBundle, error) {
	return b.fetchRemoteArchiveBundlesContext(context.Background(), tr, test, requireAll)
}

type remoteArchiveTransferBudget struct {
	mu        sync.Mutex
	remaining int64
}

func (b *remoteArchiveTransferBudget) reserve(size int64) error {
	if size < 0 {
		return fmt.Errorf("archive transfer size cannot be negative")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if size > b.remaining {
		return fmt.Errorf("remote archive total size limit of %d bytes was exceeded", remoteArchiveTotalMaxBytes)
	}
	b.remaining -= size
	return nil
}

func (b *remoteArchiveTransferBudget) release(size int64) {
	if size <= 0 {
		return
	}
	b.mu.Lock()
	b.remaining += size
	b.mu.Unlock()
}

type remoteArchiveFetchResult struct {
	index   int
	bundle  remoteArchiveBundle
	missing bool
	err     error
}

func (b *distributedExecutionBackend) fetchRemoteArchiveBundlesContext(ctx context.Context, tr *TestRunner, test *ManagedTest, requireAll bool) ([]remoteArchiveBundle, error) {
	if test == nil {
		return nil, fmt.Errorf("test is required")
	}
	if err := validateArchiveRunID(test.ID); err != nil {
		return nil, err
	}
	clients, err := b.resolveClientsLocked(tr, test)
	if err != nil {
		return nil, err
	}
	if len(clients) > remoteArchiveMaxTargets {
		return nil, fmt.Errorf("remote archive target count %d exceeds limit %d", len(clients), remoteArchiveMaxTargets)
	}
	tempRoot, err := resolveArchiveDirectoryRoot(os.TempDir(), false)
	if err != nil {
		return nil, fmt.Errorf("resolve remote archive transfer root: %w", err)
	}
	transferStageRoot, err := createArchiveTemporaryDirectory(tempRoot, "apexload-remote-archive", 0o700)
	if err != nil {
		return nil, fmt.Errorf("create remote archive transfer staging directory: %w", err)
	}
	keepStaging := false
	defer func() {
		if !keepStaging {
			_ = removeArchiveSubdirectory(tempRoot, transferStageRoot)
		}
	}()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	targets := orderedGeneratorTargets(clients, b.configuredTargets())
	results := make(chan remoteArchiveFetchResult, len(targets))
	semaphore := make(chan struct{}, remoteArchiveFetchConcurrency)
	budget := &remoteArchiveTransferBudget{remaining: remoteArchiveTotalMaxBytes}
	var workers sync.WaitGroup
	for index, target := range targets {
		workers.Add(1)
		go func(index int, target string) {
			defer workers.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				results <- remoteArchiveFetchResult{index: index, err: ctx.Err()}
				return
			}
			bundle, missing, err := b.fetchRemoteArchiveBundleTarget(ctx, clients[target], test.ID, target, index, transferStageRoot, requireAll, budget)
			results <- remoteArchiveFetchResult{index: index, bundle: bundle, missing: missing, err: err}
		}(index, target)
	}
	go func() {
		workers.Wait()
		close(results)
	}()
	ordered := make([]remoteArchiveFetchResult, len(targets))
	var firstErr error
	for result := range results {
		ordered[result.index] = result
		if result.err != nil && firstErr == nil {
			firstErr = result.err
			cancel()
		}
	}
	if firstErr != nil {
		return nil, firstErr
	}
	bundles := make([]remoteArchiveBundle, 0, len(targets))
	missingTargets := make([]string, 0)
	for index, result := range ordered {
		if result.missing {
			missingTargets = append(missingTargets, targets[index])
			continue
		}
		bundles = append(bundles, result.bundle)
	}
	if len(bundles) == 0 {
		if requireAll {
			if len(missingTargets) > 0 {
				return nil, fmt.Errorf("archive not available from targets: %s", strings.Join(missingTargets, ", "))
			}
			return nil, fmt.Errorf("no remote archive bundles were available")
		}
		return nil, nil
	}
	if requireAll && len(missingTargets) > 0 {
		return nil, fmt.Errorf("archive not available from targets: %s", strings.Join(missingTargets, ", "))
	}
	keepStaging = true
	return bundles, nil
}

func (b *distributedExecutionBackend) fetchRemoteArchiveBundleTarget(ctx context.Context, client generatorClient, testID, target string, index int, transferStageRoot string, requireAll bool, budget *remoteArchiveTransferBudget) (remoteArchiveBundle, bool, error) {
	targetCtx, cancel := context.WithTimeout(ctx, remoteArchiveTargetTimeout(b.timeout))
	defer cancel()
	targetDir, err := ensureArchiveRelativeDirectory(transferStageRoot, fmt.Sprintf("target-%03d", index), 0o700)
	if err != nil {
		return remoteArchiveBundle{}, false, err
	}
	var manifestBuffer bytes.Buffer
	_, err = copyRemoteArchiveArtifactWithBudget(targetCtx, client, generatorrpc.ArchiveArtifactRequest{TestID: testID, Name: runArchiveManifestFileName}, &manifestBuffer, remoteArchiveManifestMaxBytes, budget)
	if err != nil {
		code := grpcStatus.Code(err)
		if code == grpcCodes.NotFound || code == grpcCodes.Unimplemented || (!requireAll && (code == grpcCodes.Canceled || code == grpcCodes.Unavailable)) {
			return remoteArchiveBundle{}, true, nil
		}
		return remoteArchiveBundle{}, false, fmt.Errorf("fetch archive manifest from %s: %w", target, err)
	}
	var manifest RunArchiveManifest
	if err := json.Unmarshal(manifestBuffer.Bytes(), &manifest); err != nil {
		return remoteArchiveBundle{}, false, fmt.Errorf("decode archive manifest from %s: %w", target, err)
	}
	if strings.TrimSpace(manifest.TestID) != strings.TrimSpace(testID) {
		return remoteArchiveBundle{}, false, fmt.Errorf("validate archive manifest from %s: test_id %q does not match requested test %q", target, manifest.TestID, testID)
	}
	transactionsName, err := requiredArchiveArtifactName("transactions_file", manifest.TransactionsFile)
	if err != nil {
		return remoteArchiveBundle{}, false, fmt.Errorf("validate archive manifest from %s: %w", target, err)
	}
	resourcesName, err := requiredArchiveArtifactName("resource_samples_file", manifest.ResourceSamplesFile)
	if err != nil {
		return remoteArchiveBundle{}, false, fmt.Errorf("validate archive manifest from %s: %w", target, err)
	}
	summariesName, err := requiredArchiveArtifactName("interval_summaries_file", manifest.IntervalSummariesFile)
	if err != nil {
		return remoteArchiveBundle{}, false, fmt.Errorf("validate archive manifest from %s: %w", target, err)
	}
	paths := []string{filepath.Join(targetDir, "transactions.jsonl"), filepath.Join(targetDir, "resources.jsonl"), filepath.Join(targetDir, "interval_summaries.jsonl")}
	names := []string{transactionsName, resourcesName, summariesName}
	labels := []string{"transaction", "resource", "interval summaries"}
	for i := range paths {
		if _, err := fetchRemoteArchiveArtifactFileWithBudget(targetCtx, client, testID, names[i], paths[i], remoteArchiveArtifactMaxBytes, budget); err != nil {
			return remoteArchiveBundle{}, false, fmt.Errorf("fetch %s archive from %s: %w", labels[i], target, err)
		}
	}
	return remoteArchiveBundle{target: target, manifest: manifest, transactionsPath: paths[0], resourcesPath: paths[1], summariesPath: paths[2], transferStageRoot: transferStageRoot}, false, nil
}

func remoteArchiveTargetTimeout(base time.Duration) time.Duration {
	if base <= 0 {
		base = 10 * time.Second
	}
	timeout := base * 60
	if timeout < 2*time.Minute {
		timeout = 2 * time.Minute
	}
	if timeout > 30*time.Minute {
		timeout = 30 * time.Minute
	}
	return timeout
}

func minArchiveTransferLimit(perArtifact, remainingTotal int64) int64 {
	if remainingTotal <= 0 {
		return 0
	}
	if perArtifact < remainingTotal {
		return perArtifact
	}
	return remainingTotal
}

type boundedArchiveWriter struct {
	destination io.Writer
	remaining   int64
	written     int64
	totalBudget *remoteArchiveTransferBudget
}

func (w *boundedArchiveWriter) Write(content []byte) (int, error) {
	if int64(len(content)) > w.remaining {
		return 0, fmt.Errorf("archive artifact exceeds configured transfer limit")
	}
	if w.totalBudget != nil {
		if err := w.totalBudget.reserve(int64(len(content))); err != nil {
			return 0, err
		}
	}
	n, err := w.destination.Write(content)
	if w.totalBudget != nil && n < len(content) {
		w.totalBudget.release(int64(len(content) - n))
	}
	w.remaining -= int64(n)
	w.written += int64(n)
	return n, err
}

func copyRemoteArchiveArtifact(ctx context.Context, client generatorClient, request generatorrpc.ArchiveArtifactRequest, destination io.Writer, maxBytes int64) (int64, error) {
	return copyRemoteArchiveArtifactWithBudget(ctx, client, request, destination, maxBytes, nil)
}

func copyRemoteArchiveArtifactWithBudget(ctx context.Context, client generatorClient, request generatorrpc.ArchiveArtifactRequest, destination io.Writer, maxBytes int64, totalBudget *remoteArchiveTransferBudget) (int64, error) {
	if maxBytes <= 0 {
		return 0, fmt.Errorf("remote archive total size limit of %d bytes was exceeded", remoteArchiveTotalMaxBytes)
	}
	bounded := &boundedArchiveWriter{destination: destination, remaining: maxBytes, totalBudget: totalBudget}
	if streaming, ok := client.(streamingArchiveGeneratorClient); ok {
		copied, err := streaming.StreamArchiveArtifact(ctx, request, bounded, maxBytes)
		if err != nil {
			if totalBudget != nil {
				totalBudget.release(bounded.written)
			}
			return bounded.written, err
		}
		if copied != bounded.written {
			if totalBudget != nil {
				totalBudget.release(bounded.written)
			}
			return bounded.written, fmt.Errorf("archive artifact stream reported %d bytes but wrote %d", copied, bounded.written)
		}
		return copied, nil
	}

	// Compatibility path for in-process test adapters and older generator
	// implementations. Production *generatorrpc.Client values use the streaming
	// path above, so realistic artifacts are never buffered controller-side.
	content, err := client.GetArchiveArtifact(ctx, request)
	if err != nil {
		return 0, err
	}
	if int64(len(content)) > maxBytes {
		return 0, fmt.Errorf("archive artifact is %d bytes, limit is %d", len(content), maxBytes)
	}
	n, err := bounded.Write(content)
	if err != nil && totalBudget != nil {
		totalBudget.release(bounded.written)
	}
	return int64(n), err
}

func fetchRemoteArchiveArtifactFile(ctx context.Context, client generatorClient, testID, name, destinationPath string, maxBytes int64) (copied int64, err error) {
	return fetchRemoteArchiveArtifactFileWithBudget(ctx, client, testID, name, destinationPath, maxBytes, nil)
}

func fetchRemoteArchiveArtifactFileWithBudget(ctx context.Context, client generatorClient, testID, name, destinationPath string, maxBytes int64, totalBudget *remoteArchiveTransferBudget) (copied int64, err error) {
	if err := validateArchiveRunID(testID); err != nil {
		return 0, err
	}
	if _, err := normalizeArchiveArtifactName(name); err != nil {
		return 0, err
	}
	file, err := createArchiveArtifactExclusive(filepath.Dir(destinationPath), filepath.Base(destinationPath), 0o600, false)
	if err != nil {
		return 0, err
	}
	keep := false
	defer func() {
		if closeErr := file.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
		if !keep {
			_ = os.Remove(destinationPath)
		}
	}()
	copied, err = copyRemoteArchiveArtifactWithBudget(ctx, client, generatorrpc.ArchiveArtifactRequest{TestID: testID, Name: name}, file, maxBytes, totalBudget)
	if err != nil {
		return copied, err
	}
	if err := file.Sync(); err != nil {
		return copied, err
	}
	keep = true
	return copied, nil
}

func mergeRemoteArchiveManifest(tr *TestRunner, test *ManagedTest, bundles []remoteArchiveBundle, finalStatus, finalError string, completedAt time.Time) RunArchiveManifest {
	manifest := RunArchiveManifest{
		SchemaVersion:                archiveSchemaVersion,
		TestID:                       test.ID,
		TestName:                     test.Name,
		CreatedAt:                    test.CreatedAt,
		CompletedAt:                  completedAt,
		Status:                       finalStatus,
		Error:                        finalError,
		Provenance:                   buildRunArchiveProvenance(tr, test),
		TransactionsFile:             "transactions.jsonl",
		TransactionsParquetFile:      "transactions.parquet",
		ResourceSamplesFile:          "resources.jsonl",
		ResourceSamplesParquetFile:   "resources.parquet",
		IntervalSummariesFile:        "interval_summaries.jsonl",
		IntervalSummariesParquetFile: "interval_summaries.parquet",
	}
	manifest.SLABreached, manifest.SLADetails = distributedArchiveSLA(tr, test)
	for index, bundle := range bundles {
		if index == 0 || bundle.manifest.CreatedAt.Before(manifest.CreatedAt) {
			manifest.CreatedAt = bundle.manifest.CreatedAt
		}
		manifest.TransactionCount += bundle.manifest.TransactionCount
		manifest.ResourceSampleCount += bundle.manifest.ResourceSampleCount
		manifest.IntervalSummaryCount += bundle.manifest.IntervalSummaryCount
	}
	return manifest
}

func distributedArchiveSLA(tr *TestRunner, test *ManagedTest) (bool, []string) {
	if test == nil {
		return false, nil
	}
	var goal *GoalStatus
	if tr != nil {
		tr.mu.RLock()
		if current := tr.tests[test.ID]; current == test && test.LiveMetrics != nil && test.LiveMetrics.GoalStatus != nil {
			goal = cloneGoalStatus(test.LiveMetrics.GoalStatus)
		}
		tr.mu.RUnlock()
	} else if test.LiveMetrics != nil && test.LiveMetrics.GoalStatus != nil {
		goal = cloneGoalStatus(test.LiveMetrics.GoalStatus)
	}
	if goal == nil {
		return false, nil
	}
	details := make([]string, 0, 10)
	if goal.LatencyBreached {
		details = append(details, "Latency breached")
	}
	if goal.ErrorBreached {
		details = append(details, "Error rate breached")
	}
	if goal.ServerProcessingBreached {
		details = append(details, "Server processing breached")
	}
	if goal.TransferBreached {
		details = append(details, "Transfer time breached")
	}
	if goal.ErrorCountBreached {
		details = append(details, "Total error count breached")
	}
	if goal.ThrottledRequestsBreached {
		details = append(details, "Event Hubs throttled requests breached")
	}
	if goal.EndToEndLatencyBreached {
		details = append(details, "Event Hubs end-to-end latency breached")
	}
	if goal.PartitionSkewBreached {
		details = append(details, "Event Hubs partition skew breached")
	}
	if goal.RedeliveryRateBreached {
		details = append(details, "Messaging redelivery rate breached")
	}
	if goal.DeadLetterRateBreached {
		details = append(details, "Messaging dead-letter rate breached")
	}
	return len(details) > 0, details
}

func writeMergedRemoteArchiveFiles(rootDir string, manifest *RunArchiveManifest, bundles []remoteArchiveBundle) error {
	for _, dataset := range []struct {
		name string
		path func(remoteArchiveBundle) string
	}{
		{name: manifest.TransactionsFile, path: func(bundle remoteArchiveBundle) string { return bundle.transactionsPath }},
		{name: manifest.ResourceSamplesFile, path: func(bundle remoteArchiveBundle) string { return bundle.resourcesPath }},
		{name: manifest.IntervalSummariesFile, path: func(bundle remoteArchiveBundle) string { return bundle.summariesPath }},
	} {
		destinationPath, err := archiveArtifactPath(rootDir, dataset.name)
		if err != nil {
			return err
		}
		if err := mergeRemoteArchiveDataset(destinationPath, bundles, dataset.path); err != nil {
			return err
		}
	}
	return nil
}

func mergeRemoteArchiveDataset(destinationPath string, bundles []remoteArchiveBundle, sourcePath func(remoteArchiveBundle) string) (err error) {
	destination, err := createArchiveArtifactExclusive(filepath.Dir(destinationPath), filepath.Base(destinationPath), 0o644, false)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := destination.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	buffer := make([]byte, remoteArchiveMergeBufferBytes)
	for _, bundle := range bundles {
		path := sourcePath(bundle)
		source, _, openErr := openArchiveArtifactForRead(filepath.Dir(path), filepath.Base(path))
		if openErr != nil {
			return openErr
		}
		info, statErr := source.Stat()
		if statErr != nil {
			_ = source.Close()
			return statErr
		}
		if _, copyErr := io.CopyBuffer(destination, source, buffer); copyErr != nil {
			_ = source.Close()
			return copyErr
		}
		if info.Size() > 0 {
			if _, seekErr := source.Seek(-1, io.SeekEnd); seekErr != nil {
				_ = source.Close()
				return seekErr
			}
			last := []byte{0}
			if _, readErr := io.ReadFull(source, last); readErr != nil {
				_ = source.Close()
				return readErr
			}
			if last[0] != '\n' {
				if _, writeErr := destination.Write([]byte{'\n'}); writeErr != nil {
					_ = source.Close()
					return writeErr
				}
			}
		}
		if closeErr := source.Close(); closeErr != nil {
			return closeErr
		}
	}
	return destination.Sync()
}
