package api

// distributed_archive.go — split from distributed_execution_backend.go (same package, behavior-preserving).

import (
	"bytes"
	"container/heap"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
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

	// Generator metrics archives are emitted on a one-second cadence. Fleet
	// windows begin at the earliest pending publication and admit peers only
	// within this half-cadence clock-skew bound. The fixed window start prevents
	// chained timestamps from widening the window, and at most one generator
	// snapshot contributes to it.
	remoteArchiveSummaryCadence      = time.Second
	remoteArchiveSummaryMaxClockSkew = remoteArchiveSummaryCadence / 2
)

var errUnconfirmedArchiveCollectionSource = errors.New("unconfirmed merged archive source requires publication recovery")

type archiveArtifactClient interface {
	GetArchiveArtifact(context.Context, generatorrpc.ArchiveArtifactRequest) ([]byte, error)
}

type streamingArchiveGeneratorClient interface {
	StreamArchiveArtifact(context.Context, generatorrpc.ArchiveArtifactRequest, io.Writer, int64) (int64, error)
}

func (b *distributedExecutionBackend) stageRemoteArchive(tr *TestRunner, test *ManagedTest, finalStatus, finalError string, completedAt time.Time, requireAll bool) (*storage.RunArchiveRecord, error) {
	return b.stageRemoteArchiveWithTargetHistory(tr, test, finalStatus, finalError, completedAt, requireAll, snapshotRemoteArchiveTargetHistory(tr, test))
}

func (b *distributedExecutionBackend) stageRemoteArchiveWithTargetHistory(tr *TestRunner, test *ManagedTest, finalStatus, finalError string, completedAt time.Time, requireAll bool, targetHistory []remoteArchiveTargetSnapshot) (*storage.RunArchiveRecord, error) {
	return b.stageRemoteArchiveWithTargetHistoryContext(context.Background(), tr, test, finalStatus, finalError, completedAt, requireAll, targetHistory)
}

// One attempt budget covers source-lock acquisition and every queued download.
// Local merge stages check cancellation before replacing the recoverable source.
func (b *distributedExecutionBackend) stageRemoteArchiveWithTargetHistoryContext(ctx context.Context, tr *TestRunner, test *ManagedTest, finalStatus, finalError string, completedAt time.Time, requireAll bool, targetHistory []remoteArchiveTargetSnapshot) (*storage.RunArchiveRecord, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if tr != nil && !tr.isExecutionLeader() {
		return nil, errNotExecutionLeader
	}
	ctx, cancelOperation := tr.archiveOperationContext(ctx)
	defer cancelOperation()
	sourceCtx, cancelSource := context.WithTimeout(ctx, archiveFinalizationAttemptTimeout)
	defer cancelSource()
	if err := sourceCtx.Err(); err != nil {
		return nil, err
	}
	if test == nil {
		return nil, fmt.Errorf("test is required")
	}
	if err := validateArchiveRunID(test.ID); err != nil {
		return nil, err
	}
	releaseSource, err := lockArchivePublicationSource(sourceCtx, test.ID)
	if err != nil {
		return nil, err
	}
	defer releaseSource()
	return b.stageRemoteArchiveUnderSourceFence(sourceCtx, tr, test, finalStatus, finalError, completedAt, requireAll, targetHistory, false)
}

// The caller owns the source fence and operation deadline. Recovery uses a
// detached durable terminal snapshot and never overwrites an unconfirmed source.
func (b *distributedExecutionBackend) stageRemoteArchiveUnderSourceFence(sourceCtx context.Context, tr *TestRunner, test *ManagedTest, finalStatus, finalError string, completedAt time.Time, requireAll bool, targetHistory []remoteArchiveTargetSnapshot, recovered bool) (*storage.RunArchiveRecord, error) {
	if err := sourceCtx.Err(); err != nil {
		return nil, err
	}
	if tr != nil && !tr.isExecutionLeader() {
		return nil, errNotExecutionLeader
	}
	archiveRoot, err := resolveArchiveDirectoryRoot(resolveArchiveRoot(), true)
	if err != nil {
		return nil, fmt.Errorf("prepare distributed archive root: %w", err)
	}
	if existing, err := tr.existingCollectedArchive(test); err != nil || existing != nil {
		return existing, err
	}
	mergeOutput, err := newArchiveMergeBudget(archiveRoot)
	if err != nil {
		return nil, err
	}
	defer mergeOutput.close()
	mergeOutput.ctx = sourceCtx
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

	cache, err := tr.openCollectionSourceCache(sourceCtx, test)
	if err != nil {
		return nil, err
	}
	if cache != nil {
		defer cache.close()
	}
	bundles, coverage, err := b.fetchRemoteArchiveBundlesWithCache(sourceCtx, tr, test, requireAll, cache)
	if err != nil {
		return nil, err
	}
	defer cleanupRemoteArchiveBundles(bundles)
	if err := sourceCtx.Err(); err != nil {
		return nil, err
	}
	if len(bundles) == 0 {
		if requireAll {
			return nil, fmt.Errorf("no remote archive bundles were available")
		}
		return nil, nil
	}

	manifest := mergeRemoteArchiveManifest(tr, test, bundles, finalStatus, finalError, completedAt)
	manifest.SourceCoverage = coverage
	if !requireAll {
		manifest.CSVExportIncomplete = "not all generator archives were required; complete export cannot be verified"
	}
	if recovered {
		// Generator receipts cannot reconstruct volatile controller-only evidence.
		manifest.SourceCoverage.Mode = "best_effort"
		manifest.CSVExportIncomplete = "archive recovered from retained generator sources; controller-only timeline and telemetry evidence may be unavailable"
	}
	workingDir, err := createArchiveTemporaryDirectory(archiveParent, filepath.Base(archiveDir)+"-staging", 0o755)
	if err != nil {
		return nil, err
	}
	defer func() { _ = removeArchiveSubdirectory(archiveParent, workingDir) }()

	if err := writeMergedRemoteArchiveFilesWithBudget(workingDir, &manifest, bundles, targetHistory, mergeOutput, test); err != nil {
		return nil, err
	}
	if err := writeDistributedELKEvidence(workingDir, &manifest, tr, test, completedAt, mergeOutput); err != nil {
		return nil, err
	}
	if err := writeDistributedDynatraceEvidence(workingDir, &manifest, tr, test, completedAt, mergeOutput); err != nil {
		return nil, err
	}
	if err := writeDistributedProviderLookups(workingDir, &manifest, tr, test, mergeOutput); err != nil {
		return nil, err
	}
	if err := applyTransactionSLARules(workingDir, &manifest); err != nil {
		return nil, err
	}
	if err := applyTransactionResponseTimeSLAs(workingDir, &manifest); err != nil {
		return nil, err
	}
	conversion, err := newArchiveConversionBudget(archiveRoot)
	if err != nil {
		return nil, err
	}
	conversion.backlog = mergeOutput.backlog
	defer conversion.close()
	conversion.ctx = sourceCtx
	if err := writeArchiveParquetFiles(workingDir, &manifest, conversion); err != nil {
		return nil, err
	}
	if err := materializeDeclaredCSVExports(workingDir, &manifest, conversion); err != nil {
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
	if err := writeDurableArchiveMergeManifest(workingManifestPath, manifestContent, mergeOutput); err != nil {
		return nil, err
	}
	if err := sourceCtx.Err(); err != nil {
		return nil, err
	}
	if existing, err := tr.existingCollectedArchive(test); err != nil || existing != nil {
		return existing, err
	}
	if err := sourceCtx.Err(); err != nil {
		return nil, err
	}
	// A previous collector may have committed this directory but crashed before
	// recording its publication job. The process-local commit fence does not
	// survive that crash; preserving the unconfirmed source closes that handoff
	// gap. Recovery must validate and publish it before another collection.
	if _, err := os.Lstat(archiveDir); err == nil {
		return nil, errUnconfirmedArchiveCollectionSource
	} else if !os.IsNotExist(err) {
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

func writeDistributedELKEvidence(root string, manifest *RunArchiveManifest, tr *TestRunner, test *ManagedTest, completedAt time.Time, budgets ...*archiveOutputBudget) error {
	if manifest == nil || test == nil || test.ELKCapture == nil {
		return nil
	}
	outputBudget, err := mergeBudget(root, budgets)
	if err != nil {
		return err
	}
	if len(budgets) == 0 {
		defer outputBudget.close()
	}
	manifest.ELKEvidenceFile = "elk_evidence.jsonl"
	manifest.ELKObservation = elkObservation(manifest.ELKEvidenceFile)
	var artifacts *runArtifacts
	if tr != nil {
		tr.mu.RLock()
		artifacts = test.elkArtifacts
		tr.mu.RUnlock()
	} else {
		artifacts = test.elkArtifacts
	}
	var samples []ELKLogSample
	var metrics map[string]float64
	if artifacts != nil {
		artifacts.stopMonitoring()
		artifacts.elkMu.Lock()
		samples = append(samples, artifacts.elkSamples...)
		if artifacts.elkStatus != nil {
			metrics = cloneFloat64Map(artifacts.elkStatus.Metrics)
		}
		artifacts.elkMu.Unlock()
	}
	if err := writeArchiveMergeJSONL(filepath.Join(root, manifest.ELKEvidenceFile), outputBudget, func(encoder *json.Encoder) error {
		for _, sample := range samples {
			sample = sanitizeELKSample(sample)
			if err := encoder.Encode(sample); err != nil {
				return err
			}
			manifest.ELKEvidenceCount++
		}
		return nil
	}); err != nil {
		return err
	}
	if len(metrics) == 0 {
		return nil
	}
	resourcePath, err := archiveArtifactPath(root, manifest.ResourceSamplesFile)
	if err != nil {
		return err
	}
	file, err := openArchiveArtifactForAppend(filepath.Dir(resourcePath), filepath.Base(resourcePath))
	if err != nil {
		return err
	}
	record := ResourceSample{Timestamp: completedAt, TestID: test.ID, TestName: test.Name, External: elkExternalMetrics(test.ELKCapture.ConnectionID, metrics)}
	encodeErr := json.NewEncoder(&archiveOutputFile{file: file, budget: outputBudget}).Encode(record)
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(encodeErr, syncErr, closeErr); err != nil {
		return err
	}
	manifest.ResourceSampleCount++
	return nil
}

func writeDistributedDynatraceEvidence(root string, manifest *RunArchiveManifest, tr *TestRunner, test *ManagedTest, completedAt time.Time, budgets ...*archiveOutputBudget) error {
	if manifest == nil || test == nil || test.DynatraceCapture == nil {
		return nil
	}
	outputBudget, err := mergeBudget(root, budgets)
	if err != nil {
		return err
	}
	if len(budgets) == 0 {
		defer outputBudget.close()
	}
	manifest.DynatraceEvidenceFile = "dynatrace_evidence.jsonl"
	var artifacts *runArtifacts
	if tr != nil {
		tr.mu.RLock()
		artifacts = test.dynatraceArtifacts
		tr.mu.RUnlock()
	} else {
		artifacts = test.dynatraceArtifacts
	}
	var records []DynatraceEvidenceRecord
	var metrics map[string]float64
	if artifacts != nil {
		artifacts.stopMonitoring()
		artifacts.dynatraceMu.Lock()
		for index := range artifacts.dynatraceLogs {
			logSample := artifacts.dynatraceLogs[index]
			records = append(records, DynatraceEvidenceRecord{Type: "log_sample", Log: &logSample})
		}
		for index := range artifacts.dynatraceProblems {
			problem := artifacts.dynatraceProblems[index]
			records = append(records, DynatraceEvidenceRecord{Type: "problem_summary", Problem: &problem})
		}
		if artifacts.dynatraceStatus != nil {
			metrics = cloneFloat64Map(artifacts.dynatraceStatus.Metrics)
		}
		artifacts.dynatraceMu.Unlock()
	}
	if err := writeArchiveMergeJSONL(filepath.Join(root, manifest.DynatraceEvidenceFile), outputBudget, func(encoder *json.Encoder) error {
		for _, record := range records {
			if record.Log != nil {
				sample := sanitizeDynatraceLog(*record.Log)
				record.Log = &sample
			}
			if record.Problem != nil {
				problem := sanitizeDynatraceProblem(*record.Problem)
				record.Problem = &problem
			}
			if err := encoder.Encode(record); err != nil {
				return err
			}
			manifest.DynatraceEvidenceCount++
		}
		return nil
	}); err != nil {
		return err
	}
	if len(metrics) == 0 {
		return nil
	}
	resourcePath, err := archiveArtifactPath(root, manifest.ResourceSamplesFile)
	if err != nil {
		return err
	}
	file, err := openArchiveArtifactForAppend(filepath.Dir(resourcePath), filepath.Base(resourcePath))
	if err != nil {
		return err
	}
	record := ResourceSample{Timestamp: completedAt, TestID: test.ID, TestName: test.Name, External: dynatraceExternalMetrics(test.DynatraceCapture.ConnectionID, metrics)}
	encodeErr := json.NewEncoder(&archiveOutputFile{file: file, budget: outputBudget}).Encode(record)
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(encodeErr, syncErr, closeErr); err != nil {
		return err
	}
	manifest.ResourceSampleCount++
	return nil
}

type remoteArchiveBundle struct {
	// Set only from controller-owned admission provenance, never the remote manifest.
	processorProgressExpected bool
	target                    string
	region                    string
	manifestData              []byte
	manifest                  RunArchiveManifest
	transactionsPath          string
	resourcesPath             string
	summariesPath             string
	transferStageRoot         string
	transferLease             *archiveTransferLease
}

func cleanupRemoteArchiveBundles(bundles []remoteArchiveBundle) {
	seen := make(map[string]struct{})
	leases := make(map[*archiveTransferLease]struct{})
	for _, bundle := range bundles {
		if bundle.transferLease != nil {
			if _, ok := leases[bundle.transferLease]; ok {
				continue
			}
			leases[bundle.transferLease] = struct{}{}
			_ = bundle.transferLease.close()
			continue
		}
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
	lease     *archiveTransferLease
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
	bundles, _, err := b.fetchRemoteArchiveBundlesWithCoverage(ctx, tr, test, requireAll)
	return bundles, err
}

func (b *distributedExecutionBackend) fetchRemoteArchiveBundlesWithCoverage(ctx context.Context, tr *TestRunner, test *ManagedTest, requireAll bool) ([]remoteArchiveBundle, *RunArchiveSourceCoverage, error) {
	return b.fetchRemoteArchiveBundlesWithCache(ctx, tr, test, requireAll, nil)
}

func (b *distributedExecutionBackend) fetchRemoteArchiveBundlesWithCache(ctx context.Context, tr *TestRunner, test *ManagedTest, requireAll bool, cache *collectionSourceCache) ([]remoteArchiveBundle, *RunArchiveSourceCoverage, error) {
	if test == nil {
		return nil, nil, fmt.Errorf("test is required")
	}
	if err := validateArchiveRunID(test.ID); err != nil {
		return nil, nil, err
	}
	var (
		clientsByTarget map[string]generatorClient
		archivedClients map[string]generatorClient
		singleClient    generatorClient
		remoteTarget    string
		shards          []GeneratorShardSummary
	)
	if tr != nil {
		tr.mu.RLock()
		clientsByTarget = cloneClientMap(test.remoteClientsByTarget)
		archivedClients = cloneClientMap(test.remoteArchiveClientsByTarget)
		singleClient = test.remoteClient
		remoteTarget = test.remoteTarget
		shards = append([]GeneratorShardSummary(nil), test.GeneratorShards...)
		tr.mu.RUnlock()
	} else {
		clientsByTarget = cloneClientMap(test.remoteClientsByTarget)
		archivedClients = cloneClientMap(test.remoteArchiveClientsByTarget)
		singleClient = test.remoteClient
		remoteTarget = test.remoteTarget
		shards = append([]GeneratorShardSummary(nil), test.GeneratorShards...)
	}
	// Snapshot active and retired-archive transports in one critical section.
	// Otherwise a concurrent evacuation could move a source between the two maps
	// between reads and make the final evidence merge omit it.
	clients, snapshotErr := b.clientsFromSnapshot(clientsByTarget, singleClient, remoteTarget)
	if clients == nil {
		clients = make(map[string]generatorClient, len(archivedClients))
	}
	for target, client := range archivedClients {
		if clients[target] == nil {
			clients[target] = client
		}
	}
	if target := strings.TrimSpace(remoteTarget); target != "" {
		if _, exists := clients[target]; !exists {
			clients[target] = nil
		}
	}
	// A known assignment whose transport disappeared must remain an expected
	// source. Do not mistake the surviving client map for the whole run.
	for _, shard := range shards {
		if target := strings.TrimSpace(shard.Target); target != "" {
			if _, exists := clients[target]; !exists {
				clients[target] = nil
			}
		}
	}
	var sourceIdentities map[string]string
	if tr != nil {
		intent, err := tr.runArchiveCollectionIntent(test)
		if err != nil {
			return nil, nil, err
		}
		if intent != nil {
			sourceIdentities = intent.Sources
			for target := range sourceIdentities {
				if _, exists := clients[target]; !exists {
					clients[target] = nil
				}
			}
			for target := range clients {
				if sourceIdentities[target] == "" {
					return nil, nil, fmt.Errorf("archive target %s has no durable source identity", target)
				}
			}
		}
	}
	if snapshotErr != nil && len(clients) == 0 {
		return nil, nil, snapshotErr
	}
	if len(clients) > remoteArchiveMaxTargets {
		return nil, nil, fmt.Errorf("remote archive target count %d exceeds limit %d", len(clients), remoteArchiveMaxTargets)
	}
	limit := remoteArchiveTotalMaxBytes
	if cache != nil {
		limit = cache.intent.CacheLimitBytes
	}
	lease, err := reserveArchiveTransfer(ctx, limit)
	if err != nil {
		return nil, nil, err
	}
	transferStageRoot := lease.path
	keepStaging := false
	defer func() {
		if !keepStaging {
			_ = lease.close()
		}
	}()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	targets := orderedGeneratorTargets(clients, b.configuredTargets())
	results := make(chan remoteArchiveFetchResult, len(targets))
	semaphore := make(chan struct{}, remoteArchiveFetchConcurrency)
	budget := &remoteArchiveTransferBudget{remaining: limit, lease: lease}
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
			var client archiveArtifactClient = clients[target]
			var cached *cachedArchiveSource
			var err error
			if cache != nil {
				cached, err = cache.load(ctx, target)
				if err != nil {
					results <- remoteArchiveFetchResult{index: index, err: err}
					return
				}
				if cached != nil {
					defer cached.close()
					client = cached
				}
			}
			bundle, missing, err := b.fetchRemoteArchiveBundleTargetForSource(ctx, client, test.ID, target, sourceIdentities[target], index, transferStageRoot, requireAll, budget)
			if cache != nil && cached == nil && err == nil && !missing {
				err = cache.save(ctx, bundle)
			}
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
			if cache == nil {
				cancel()
			}
		}
	}
	if firstErr != nil {
		return nil, nil, firstErr
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	bundles := make([]remoteArchiveBundle, 0, len(targets))
	missingTargets := make([]string, 0)
	coverage := &RunArchiveSourceCoverage{Mode: "best_effort", Sources: make([]RunArchiveSource, 0, len(targets))}
	if requireAll {
		coverage.Mode = "required"
	}
	for index, result := range ordered {
		coverage.Sources = append(coverage.Sources, RunArchiveSource{Target: targets[index], Collected: !result.missing})
		if result.missing {
			missingTargets = append(missingTargets, targets[index])
			continue
		}
		result.bundle.transferLease = lease
		bundles = append(bundles, result.bundle)
	}
	if len(bundles) == 0 {
		if requireAll {
			if len(missingTargets) > 0 {
				return nil, nil, fmt.Errorf("archive not available from targets: %s", strings.Join(missingTargets, ", "))
			}
			return nil, nil, fmt.Errorf("no remote archive bundles were available")
		}
		return nil, coverage, nil
	}
	if requireAll && len(missingTargets) > 0 {
		return nil, nil, fmt.Errorf("archive not available from targets: %s", strings.Join(missingTargets, ", "))
	}
	keepStaging = true
	return bundles, coverage, nil
}

func (b *distributedExecutionBackend) fetchRemoteArchiveBundleTarget(ctx context.Context, client archiveArtifactClient, testID, target string, index int, transferStageRoot string, requireAll bool, budget *remoteArchiveTransferBudget) (remoteArchiveBundle, bool, error) {
	return b.fetchRemoteArchiveBundleTargetForSource(ctx, client, testID, target, "", index, transferStageRoot, requireAll, budget)
}
func (b *distributedExecutionBackend) fetchRemoteArchiveBundleTargetForSource(ctx context.Context, client archiveArtifactClient, testID, target, sourceIdentity string, index int, transferStageRoot string, requireAll bool, budget *remoteArchiveTransferBudget) (remoteArchiveBundle, bool, error) {
	if err := ctx.Err(); err != nil {
		return remoteArchiveBundle{}, false, err
	}
	if client == nil {
		return remoteArchiveBundle{}, true, nil
	}
	targetCtx, cancel := context.WithTimeout(ctx, remoteArchiveTargetTimeout(b.timeout))
	defer cancel()
	targetDir, err := ensureArchiveRelativeDirectory(transferStageRoot, fmt.Sprintf("target-%03d", index), 0o700)
	if err != nil {
		return remoteArchiveBundle{}, false, err
	}
	var manifestBuffer bytes.Buffer
	_, err = copyRemoteArchiveArtifactWithBudget(targetCtx, client, generatorrpc.ArchiveArtifactRequest{TestID: testID, Name: runArchiveManifestFileName, SourceIdentity: sourceIdentity}, &manifestBuffer, remoteArchiveManifestMaxBytes, budget)
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
	if sourceIdentity != "" {
		var sourceProof struct {
			SourceIdentity string `json:"source_identity"`
		}
		if err := json.Unmarshal(manifestBuffer.Bytes(), &sourceProof); err != nil || sourceProof.SourceIdentity != sourceIdentity {
			return remoteArchiveBundle{}, false, fmt.Errorf("archive source from %s does not match the durable assignment identity", target)
		}
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
		if _, err := fetchRemoteArchiveArtifactFileForSource(targetCtx, client, testID, names[i], sourceIdentity, paths[i], remoteArchiveArtifactMaxBytes, budget); err != nil {
			return remoteArchiveBundle{}, false, fmt.Errorf("fetch %s archive from %s: %w", labels[i], target, err)
		}
	}
	return remoteArchiveBundle{target: target, region: strings.TrimSpace(b.regionForTarget(target)), manifest: manifest, manifestData: append([]byte(nil), manifestBuffer.Bytes()...), transactionsPath: paths[0], resourcesPath: paths[1], summariesPath: paths[2], transferStageRoot: transferStageRoot}, false, nil
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
	if n < 0 || n > len(content) {
		// An invalid writer count cannot justify refunding reserved capacity.
		w.remaining -= int64(len(content))
		w.written += int64(len(content))
		return 0, fmt.Errorf("archive writer returned invalid byte count")
	}
	if n < len(content) && err == nil {
		err = io.ErrShortWrite
	}
	if w.totalBudget != nil && n < len(content) {
		w.totalBudget.release(int64(len(content) - n))
	}
	w.remaining -= int64(n)
	w.written += int64(n)
	return n, err
}

func copyRemoteArchiveArtifact(ctx context.Context, client archiveArtifactClient, request generatorrpc.ArchiveArtifactRequest, destination io.Writer, maxBytes int64) (int64, error) {
	return copyRemoteArchiveArtifactWithBudget(ctx, client, request, destination, maxBytes, nil)
}

func copyRemoteArchiveArtifactWithBudget(ctx context.Context, client archiveArtifactClient, request generatorrpc.ArchiveArtifactRequest, destination io.Writer, maxBytes int64, totalBudget *remoteArchiveTransferBudget) (int64, error) {
	if maxBytes <= 0 {
		return 0, fmt.Errorf("remote archive total size limit of %d bytes was exceeded", remoteArchiveTotalMaxBytes)
	}
	bounded := &boundedArchiveWriter{destination: destination, remaining: maxBytes, totalBudget: totalBudget}
	if streaming, ok := client.(streamingArchiveGeneratorClient); ok {
		copied, err := streaming.StreamArchiveArtifact(ctx, request, bounded, maxBytes)
		if err != nil {
			// Failed file cleanup is not guaranteed; keep written bytes charged
			// until this operation releases its entire spool reservation.
			return bounded.written, err
		}
		if copied != bounded.written {
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
	return int64(n), err
}

func fetchRemoteArchiveArtifactFile(ctx context.Context, client archiveArtifactClient, testID, name, destinationPath string, maxBytes int64) (copied int64, err error) {
	return fetchRemoteArchiveArtifactFileWithBudget(ctx, client, testID, name, destinationPath, maxBytes, nil)
}

func fetchRemoteArchiveArtifactFileWithBudget(ctx context.Context, client archiveArtifactClient, testID, name, destinationPath string, maxBytes int64, totalBudget *remoteArchiveTransferBudget) (copied int64, err error) {
	return fetchRemoteArchiveArtifactFileForSource(ctx, client, testID, name, "", destinationPath, maxBytes, totalBudget)
}
func fetchRemoteArchiveArtifactFileForSource(ctx context.Context, client archiveArtifactClient, testID, name, sourceIdentity, destinationPath string, maxBytes int64, totalBudget *remoteArchiveTransferBudget) (copied int64, err error) {
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
	var destination io.Writer = file
	if totalBudget != nil && totalBudget.lease != nil {
		destination = archiveTransferFileWriter{ctx: ctx, lease: totalBudget.lease, file: file}
	}
	copied, err = copyRemoteArchiveArtifactWithBudget(ctx, client, generatorrpc.ArchiveArtifactRequest{TestID: testID, Name: name, SourceIdentity: sourceIdentity}, destination, maxBytes, totalBudget)
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
	var initialProvenance *RunArchiveProvenance
	var continuity *RunContinuityStatus
	if tr != nil {
		tr.mu.RLock()
		if test != nil && test.initialArchiveProvenance != nil {
			initial := maskRunArchiveProvenance(*test.initialArchiveProvenance)
			initialProvenance = &initial
		}
		continuity = archivedRunContinuity(test)
		tr.mu.RUnlock()
	} else if test != nil && test.initialArchiveProvenance != nil {
		initial := maskRunArchiveProvenance(*test.initialArchiveProvenance)
		initialProvenance = &initial
	}
	manifest := RunArchiveManifest{
		SchemaVersion:                archiveSchemaVersion,
		TestID:                       test.ID,
		TestName:                     test.Name,
		CreatedAt:                    test.CreatedAt,
		CompletedAt:                  completedAt,
		Status:                       finalStatus,
		Error:                        finalError,
		Provenance:                   buildRunArchiveProvenance(tr, test),
		InitialProvenance:            initialProvenance,
		controllerInitialProvenance:  initialProvenance,
		TransactionsFile:             "transactions.jsonl",
		TransactionsParquetFile:      "transactions.parquet",
		ResourceSamplesFile:          "resources.jsonl",
		ResourceSamplesParquetFile:   "resources.parquet",
		IntervalSummariesFile:        "interval_summaries.jsonl",
		IntervalSummariesParquetFile: "interval_summaries.parquet",
	}
	manifest.Continuity = continuity
	manifest.TerminationReason = "unknown"
	switch finalStatus {
	case "ABORTED":
		manifest.TerminationReason = "aborted"
	case "FAILED":
		manifest.TerminationReason = "failed"
	case "COMPLETE":
		if manifest.Provenance.StoppedBySubject != "" {
			manifest.TerminationReason = "operator_stopped"
		} else if finalError != "" {
			manifest.TerminationReason = "stopped_early"
		} else {
			if tr != nil {
				tr.mu.RLock()
			}
			completed := test != nil && test.remoteWorkloadCompleted
			if tr != nil {
				tr.mu.RUnlock()
			}
			if completed {
				manifest.TerminationReason = "workload_completed"
			}

		}
	}
	manifest.SLAEvaluated, manifest.SLABreached, manifest.SLADetails = distributedArchiveSLA(tr, test)
	for index, bundle := range bundles {
		if index == 0 || bundle.manifest.CreatedAt.Before(manifest.CreatedAt) {
			manifest.CreatedAt = bundle.manifest.CreatedAt
		}
		manifest.TransactionCount += bundle.manifest.TransactionCount
		manifest.ResourceSampleCount += bundle.manifest.ResourceSampleCount
		manifest.IntervalSummaryCount += bundle.manifest.IntervalSummaryCount
		mergeRunArchiveMessageDetailCapture(&manifest, bundle.manifest.MessageDetailCapture)
		if manifest.InitialProvenance == nil && bundle.manifest.InitialProvenance != nil {
			initial := maskRunArchiveProvenance(*bundle.manifest.InitialProvenance)
			manifest.InitialProvenance = &initial
		}
	}
	return manifest
}

func mergeRunArchiveMessageDetailCapture(manifest *RunArchiveManifest, incoming *RunArchiveMessageDetailCapture) {
	if manifest == nil || incoming == nil {
		return
	}
	if manifest.MessageDetailCapture == nil {
		manifest.MessageDetailCapture = &RunArchiveMessageDetailCapture{Dropped: make(map[string]int64)}
	}
	destination := manifest.MessageDetailCapture
	destination.RetainedItems += incoming.RetainedItems
	destination.RetainedBytes += incoming.RetainedBytes
	destination.TruncatedItems += incoming.TruncatedItems
	if destination.Dropped == nil {
		destination.Dropped = make(map[string]int64)
	}
	for reason, count := range incoming.Dropped {
		destination.Dropped[reason] += count
	}
}

func distributedArchiveSLA(tr *TestRunner, test *ManagedTest) (bool, bool, []string) {
	if test == nil {
		return false, false, nil
	}
	var goal *GoalStatus
	var evaluated, breached bool
	var details []string
	if tr != nil {
		tr.mu.RLock()
		if current := tr.tests[test.ID]; current == test {
			evaluated = test.ciSLAEvaluated
			breached = test.ciSLABreached
			details = append([]string(nil), test.ciSLADetails...)
			if test.LiveMetrics != nil {
				evaluated = evaluated || ciSLAEvidenceComplete(test.GoalConfig, test.LiveMetrics)
				goal = cloneGoalStatus(test.LiveMetrics.GoalStatus)
			}
		}
		tr.mu.RUnlock()
	} else {
		evaluated = test.ciSLAEvaluated
		breached = test.ciSLABreached
		details = append([]string(nil), test.ciSLADetails...)
		if test.LiveMetrics != nil {
			evaluated = evaluated || ciSLAEvidenceComplete(test.GoalConfig, test.LiveMetrics)
			goal = cloneGoalStatus(test.LiveMetrics.GoalStatus)
		}
	}
	if goal == nil {
		return evaluated, breached, compactNonEmptyStrings(details...)
	}
	currentBreached, currentDetails := ciLiveSLABreach(goal)
	return evaluated, breached || currentBreached, compactNonEmptyStrings(append(details, currentDetails...)...)
}

// remoteArchiveTargetSnapshot is controller-owned interval evidence. Generators
// publish observed rates but do not know the goal controller's time-varying
// target or the controller-derived telemetry-health state, so finalization
// overlays only values actually installed in the controller's published
// live-history row at the corresponding timestamp.
type remoteArchiveTargetSnapshot struct {
	timestamp       time.Time
	targetTPS       float64
	groups          map[string]float64
	telemetry       *TelemetryHealth
	telemetryStatus *TelemetryRunHealth
}

func snapshotRemoteArchiveTargetHistory(tr *TestRunner, test *ManagedTest) []remoteArchiveTargetSnapshot {
	if test == nil {
		return nil
	}
	snapshots := make([]remoteArchiveTargetSnapshot, 0, 16)
	capture := func(live *LiveMetrics) {
		if live == nil || live.Timestamp.IsZero() {
			return
		}
		snapshot := remoteArchiveTargetSnapshot{
			timestamp:       live.Timestamp.UTC(),
			targetTPS:       live.TargetTPS,
			telemetry:       cloneTelemetryHealth(live.Telemetry),
			telemetryStatus: cloneTelemetryRunHealth(live.TelemetryStatus),
		}
		for name, group := range live.GroupMetrics {
			if group == nil || group.TargetTPS <= 0 {
				continue
			}
			if snapshot.groups == nil {
				snapshot.groups = make(map[string]float64)
			}
			snapshot.groups[name] = group.TargetTPS
		}
		snapshots = append(snapshots, snapshot)
	}
	if tr != nil {
		tr.mu.RLock()
		if current := tr.tests[test.ID]; current == test {
			for _, live := range test.History {
				capture(live)
			}
			if test.LiveMetrics != nil && (len(test.History) == 0 || test.History[len(test.History)-1] != test.LiveMetrics) {
				capture(test.LiveMetrics)
			}
		}
		tr.mu.RUnlock()
	} else {
		for _, live := range test.History {
			capture(live)
		}
		if test.LiveMetrics != nil && (len(test.History) == 0 || test.History[len(test.History)-1] != test.LiveMetrics) {
			capture(test.LiveMetrics)
		}
	}
	if len(snapshots) == 0 {
		return nil
	}
	sort.SliceStable(snapshots, func(i, j int) bool { return snapshots[i].timestamp.Before(snapshots[j].timestamp) })
	compacted := snapshots[:0]
	for _, snapshot := range snapshots {
		if len(compacted) > 0 && compacted[len(compacted)-1].timestamp.Equal(snapshot.timestamp) {
			compacted[len(compacted)-1] = snapshot
			continue
		}
		compacted = append(compacted, snapshot)
	}
	return compacted
}

func writeMergedRemoteArchiveFiles(rootDir string, manifest *RunArchiveManifest, bundles []remoteArchiveBundle, targetHistory []remoteArchiveTargetSnapshot, tests ...*ManagedTest) error {
	budget, err := newArchiveMergeBudget(rootDir)
	if err != nil {
		return err
	}
	defer budget.close()
	return writeMergedRemoteArchiveFilesWithBudget(rootDir, manifest, bundles, targetHistory, budget, tests...)
}

// Bind expectations from immutable controller admission claims. A peer that has
// never published progress (including a legacy peer) is still an expected
// processor source; unrelated producer/HTTP peers are not. Copy the bundle view
// so this qualification cannot mutate a caller's transfer bookkeeping.
func remoteArchiveBundlesWithProgressExpectations(manifest *RunArchiveManifest, bundles []remoteArchiveBundle) []remoteArchiveBundle {
	expected := make(map[string]bool)
	add := func(provenance RunArchiveProvenance) {
		for _, shard := range provenance.GeneratorShards {
			claim := shard.RuntimeResourceClaim
			if claim != nil && claim.Requested != nil && claim.Requested.Processors > 0 {
				expected[shard.Target] = true
			}
		}
	}
	if manifest != nil {
		add(manifest.Provenance)
		if manifest.controllerInitialProvenance != nil {
			add(*manifest.controllerInitialProvenance)
		}
	}
	out := append([]remoteArchiveBundle(nil), bundles...)
	for index := range out {
		out[index].processorProgressExpected = expected[out[index].target]
	}
	return out
}

func writeMergedRemoteArchiveFilesWithBudget(rootDir string, manifest *RunArchiveManifest, bundles []remoteArchiveBundle, targetHistory []remoteArchiveTargetSnapshot, budget *archiveOutputBudget, tests ...*ManagedTest) error {
	bundles = remoteArchiveBundlesWithProgressExpectations(manifest, bundles)
	transactionsPath, err := archiveArtifactPath(rootDir, manifest.TransactionsFile)
	if err != nil {
		return err
	}
	var test *ManagedTest
	if len(tests) > 0 {
		test = tests[0]
	}
	if err := mergeRemoteArchiveTransactions(transactionsPath, manifest, bundles, messageDetailBudgetsForTest(test), budget); err != nil {
		return err
	}

	for _, dataset := range []struct {
		name          string
		kind          string
		path          func(remoteArchiveBundle) string
		expectedCount func(remoteArchiveBundle) int64
	}{
		{
			name: manifest.ResourceSamplesFile, kind: "resource sample",
			path:          func(bundle remoteArchiveBundle) string { return bundle.resourcesPath },
			expectedCount: func(bundle remoteArchiveBundle) int64 { return bundle.manifest.ResourceSampleCount },
		},
	} {
		destinationPath, err := archiveArtifactPath(rootDir, dataset.name)
		if err != nil {
			return err
		}
		if err := mergeRemoteArchiveDataset(destinationPath, bundles, dataset.kind, dataset.path, dataset.expectedCount, budget); err != nil {
			return err
		}
	}
	summariesPath, err := archiveArtifactPath(rootDir, manifest.IntervalSummariesFile)
	if err != nil {
		return err
	}
	count, err := mergeRemoteArchiveIntervalSummariesWithCoverage(summariesPath, bundles, targetHistory, manifest.SourceCoverage, budget)
	if err != nil {
		return err
	}
	manifest.IntervalSummaryCount = count
	return nil
}

const (
	controllerMessageDetailDefaultMaxItems      = int64(128)
	controllerMessageDetailDefaultMaxItemBytes  = int64(1 << 20)
	controllerMessageDetailDefaultMaxTotalBytes = int64(16 << 20)
)

type controllerMessageDetailBudget struct {
	maxItems      int64
	maxItemBytes  int64
	maxTotalBytes int64
	adaptive      bool
	retainedItems int64
	retainedBytes int64
	priorityItems map[int]int64
	priorityBytes map[int]int64
}

func messageDetailBudgetsForTest(test *ManagedTest) map[string]*controllerMessageDetailBudget {
	if test == nil {
		return nil
	}
	budgets := make(map[string]*controllerMessageDetailBudget)
	for _, group := range test.Groups {
		if group.script == nil || group.script.Config.Results.MessageDetail == nil {
			continue
		}
		policy := group.script.Config.Results.MessageDetail
		mode := strings.ToLower(strings.TrimSpace(policy.Mode))
		if mode != "sampled" && mode != "adaptive" {
			continue
		}
		budget := &controllerMessageDetailBudget{
			maxItems:      int64(policy.MaxItems),
			maxItemBytes:  policy.MaxItemBytes,
			maxTotalBytes: policy.MaxTotalBytes,
			adaptive:      mode == "adaptive",
			priorityItems: make(map[int]int64),
			priorityBytes: make(map[int]int64),
		}
		if budget.maxItems <= 0 {
			budget.maxItems = controllerMessageDetailDefaultMaxItems
		}
		if budget.maxItemBytes <= 0 {
			budget.maxItemBytes = controllerMessageDetailDefaultMaxItemBytes
		}
		if budget.maxTotalBytes <= 0 {
			budget.maxTotalBytes = controllerMessageDetailDefaultMaxTotalBytes
		}
		budgets[strings.TrimSpace(group.Name)] = budget
	}
	return budgets
}

func messageDetailBudgetForEvent(budgets map[string]*controllerMessageDetailBudget, group string) *controllerMessageDetailBudget {
	if len(budgets) == 0 {
		return nil
	}
	if budget := budgets[strings.TrimSpace(group)]; budget != nil {
		return budget
	}
	if strings.TrimSpace(group) == "" && len(budgets) == 1 {
		for _, budget := range budgets {
			return budget
		}
	}
	return nil
}

func mergeRemoteArchiveTransactions(destinationPath string, manifest *RunArchiveManifest, bundles []remoteArchiveBundle, budgets map[string]*controllerMessageDetailBudget, outputs ...*archiveOutputBudget) (err error) {
	outputBudget, err := mergeBudget(filepath.Dir(destinationPath), outputs)
	if err != nil {
		return err
	}
	if len(outputs) == 0 {
		defer outputBudget.close()
	}
	destination, err := createArchiveArtifactExclusive(filepath.Dir(destinationPath), filepath.Base(destinationPath), 0o644, false)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := destination.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	output := newArchiveMergeOutput(destination, outputBudget)
	encoder := json.NewEncoder(output)
	var ordinal int64
	if len(budgets) > 0 {
		if manifest.MessageDetailCapture == nil {
			manifest.MessageDetailCapture = &RunArchiveMessageDetailCapture{}
		}
		manifest.MessageDetailCapture.RetainedItems = 0
		manifest.MessageDetailCapture.RetainedBytes = 0
		if manifest.MessageDetailCapture.Dropped == nil {
			manifest.MessageDetailCapture.Dropped = make(map[string]int64)
		}
	}
	for _, bundle := range bundles {
		source, _, openErr := openArchiveArtifactForRead(filepath.Dir(bundle.transactionsPath), filepath.Base(bundle.transactionsPath))
		if openErr != nil {
			return openErr
		}
		scanner := newArchiveJSONLScanner(source)
		var count int64
		for scanner.Scan() {
			var event TransactionEvent
			if decodeErr := json.Unmarshal(scanner.Bytes(), &event); decodeErr != nil {
				_ = source.Close()
				return fmt.Errorf("decode remote transaction record for target %q: %w", bundle.target, decodeErr)
			}
			sanitizeArchivedTransaction(&event)
			// Region is controller-owned placement metadata. Never accept a label
			// supplied by a generator archive, even when that label looks valid.
			event.Region = bundle.region
			if event.Success && event.Snapshot != nil && len(budgets) > 0 {
				if budget := messageDetailBudgetForEvent(budgets, event.Group); budget != nil {
					enforceControllerMessageDetailBudget(&event, budget, manifest.MessageDetailCapture)
				} else {
					manifest.MessageDetailCapture.Dropped["controller_unknown_group"]++
					event.Snapshot = nil
				}
			}
			finalizeTransactionEventDerivedFields(&event, ordinal)
			if encodeErr := encoder.Encode(&event); encodeErr != nil {
				_ = source.Close()
				return encodeErr
			}
			ordinal++
			count++
		}
		if scanErr := scanner.Err(); scanErr != nil {
			_ = source.Close()
			return fmt.Errorf("scan remote transaction records for target %q: %w", bundle.target, scanErr)
		}
		if closeErr := source.Close(); closeErr != nil {
			return closeErr
		}
		if count != bundle.manifest.TransactionCount {
			return fmt.Errorf("remote transaction count for target %q is %d, manifest declares %d", bundle.target, count, bundle.manifest.TransactionCount)
		}
	}
	if err := output.Flush(); err != nil {
		return err
	}
	return destination.Sync()
}

func enforceControllerMessageDetailBudget(event *TransactionEvent, budget *controllerMessageDetailBudget, stats *RunArchiveMessageDetailCapture) {
	if event == nil || budget == nil || stats == nil || event.Snapshot == nil {
		return
	}
	drop := func(reason string) {
		stats.Dropped[reason]++
		event.Snapshot = nil
	}
	priority := 0
	priorityItemLimit, priorityByteLimit := budget.maxItems, budget.maxTotalBytes
	if budget.adaptive {
		priority = min(max(event.MessageDetailPriority, 1), 3)
		priorityItemLimit = adaptiveMessageDetailPriorityLimit(budget.maxItems, priority)
		priorityByteLimit = adaptiveMessageDetailPriorityLimit(budget.maxTotalBytes, priority)
		if priorityItemLimit == 0 || budget.priorityItems[priority] >= priorityItemLimit {
			drop(fmt.Sprintf("controller_priority_%d_item_limit", priority))
			return
		}
		if priorityByteLimit == 0 || budget.priorityBytes[priority] >= priorityByteLimit {
			drop(fmt.Sprintf("controller_priority_%d_byte_limit", priority))
			return
		}
	}
	if budget.retainedItems >= budget.maxItems {
		drop("controller_item_limit")
		return
	}
	remaining := budget.maxTotalBytes - budget.retainedBytes
	if priority > 0 {
		remaining = min(remaining, priorityByteLimit-budget.priorityBytes[priority])
	}
	if remaining <= 0 {
		drop("controller_total_byte_limit")
		return
	}
	limit := min(budget.maxItemBytes, remaining)
	before := archiveMessageDetailSnapshotBytes(event.Snapshot)
	if before > limit {
		event.Snapshot = engine.BoundErrorSnapshot(event.Snapshot, int(limit))
		stats.TruncatedItems++
	}
	retained := archiveMessageDetailSnapshotBytes(event.Snapshot)
	if event.Snapshot == nil || retained <= 0 || retained > limit {
		drop("controller_item_byte_limit")
		return
	}
	budget.retainedItems++
	budget.retainedBytes += retained
	if priority > 0 {
		budget.priorityItems[priority]++
		budget.priorityBytes[priority] += retained
	}
	stats.RetainedItems++
	stats.RetainedBytes += retained
}

type remoteArchiveSummaryCursor struct {
	file      *os.File
	decoder   *json.Decoder
	record    IntervalSummaryRecord
	timestamp time.Time
	index     int
	region    string
	decoded   int64
}

type remoteArchiveSourceWindow struct {
	end        time.Time
	fleetIndex int
	seconds    float64
}

type remoteArchiveSummaryHeap []*remoteArchiveSummaryCursor

func (h remoteArchiveSummaryHeap) Len() int { return len(h) }
func (h remoteArchiveSummaryHeap) Less(i, j int) bool {
	if !h[i].timestamp.Equal(h[j].timestamp) {
		return h[i].timestamp.Before(h[j].timestamp)
	}
	return h[i].index < h[j].index
}
func (h remoteArchiveSummaryHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *remoteArchiveSummaryHeap) Push(value any) {
	*h = append(*h, value.(*remoteArchiveSummaryCursor))
}
func (h *remoteArchiveSummaryHeap) Pop() any {
	old := *h
	last := old[len(old)-1]
	*h = old[:len(old)-1]
	return last
}

// mergeRemoteArchiveIntervalSummaries performs a bounded k-way merge over the
// already time-ordered generator streams. Each window is anchored to the
// earliest pending publication and has a fixed maximum width, so publications
// at :00.499 and :00.501 align without depending on wall-clock second edges.
//
// Fleet percentile scalars are never averaged: quantiles are properties of a
// distribution and cannot be merged from p50/p95 values. For archives within
// the existing analysis record/byte budgets, fleet interval counts, errors,
// average latency, TPS, and quantiles are rebuilt from transaction rows. Larger
// archives retain generator rate/count data but mark multi-generator
// percentiles unavailable.
func mergeRemoteArchiveIntervalSummaries(destinationPath string, bundles []remoteArchiveBundle) (count int64, err error) {
	return mergeRemoteArchiveIntervalSummariesWithTargets(destinationPath, bundles, nil)
}

func mergeRemoteArchiveIntervalSummariesWithTargets(destinationPath string, bundles []remoteArchiveBundle, targetHistory []remoteArchiveTargetSnapshot, budgets ...*archiveOutputBudget) (count int64, err error) {
	return mergeRemoteArchiveIntervalSummariesWithCoverage(destinationPath, bundles, targetHistory, nil, budgets...)
}

func mergeRemoteArchiveIntervalSummariesWithCoverage(destinationPath string, bundles []remoteArchiveBundle, targetHistory []remoteArchiveTargetSnapshot, sourceCoverage *RunArchiveSourceCoverage, budgets ...*archiveOutputBudget) (count int64, err error) {
	outputBudget, err := mergeBudget(filepath.Dir(destinationPath), budgets)
	if err != nil {
		return 0, err
	}
	if len(budgets) == 0 {
		defer outputBudget.close()
	}
	provisionalName := "." + filepath.Base(destinationPath) + ".fleet-merge"
	provisionalPath := filepath.Join(filepath.Dir(destinationPath), provisionalName)
	provisional, err := createArchiveArtifactExclusive(filepath.Dir(provisionalPath), filepath.Base(provisionalPath), 0o600, false)
	if err != nil {
		return 0, err
	}
	provisionalOpen := true
	defer func() {
		if provisionalOpen {
			if closeErr := provisional.Close(); err == nil && closeErr != nil {
				err = closeErr
			}
		}
		if removeErr := os.Remove(provisionalPath); err == nil && removeErr != nil && !os.IsNotExist(removeErr) {
			err = removeErr
		}
	}()

	cursors := make(remoteArchiveSummaryHeap, 0, len(bundles))
	openedFiles := make([]*os.File, 0, len(bundles))
	defer func() {
		for _, file := range openedFiles {
			_ = file.Close()
		}
	}()
	for index, bundle := range bundles {
		path := bundle.summariesPath
		file, _, openErr := openArchiveArtifactForRead(filepath.Dir(path), filepath.Base(path))
		if openErr != nil {
			return 0, openErr
		}
		openedFiles = append(openedFiles, file)
		cursor := &remoteArchiveSummaryCursor{file: file, decoder: json.NewDecoder(file), index: index, region: bundle.region}
		if decodeErr := cursor.decoder.Decode(&cursor.record); decodeErr != nil {
			if decodeErr == io.EOF {
				_ = file.Close()
				if bundle.manifest.IntervalSummaryCount != 0 {
					return 0, fmt.Errorf("remote interval summary count for target %q is 0, manifest declares %d", bundle.target, bundle.manifest.IntervalSummaryCount)
				}
				continue
			}
			_ = file.Close()
			return 0, decodeErr
		}
		cursor.decoded = 1
		cursor.record.ByRegion = controllerApprovedIntervalRegion(cursor.record.ByRegion, cursor.region)
		cursor.timestamp = cursor.record.Timestamp.UTC()
		cursors = append(cursors, cursor)
	}
	if len(cursors) == 0 {
		if err := provisional.Sync(); err != nil {
			return 0, err
		}
		if err := provisional.Close(); err != nil {
			return 0, err
		}
		provisionalOpen = false
		return writeRemoteArchiveFleetSummaries(destinationPath, provisionalPath, nil, false, targetHistory, outputBudget)
	}

	windowCapacity := len(cursors)
	singleGenerator := len(bundles) == 1
	missingSourceCoverage := false
	if sourceCoverage != nil {
		for _, source := range sourceCoverage.Sources {
			missingSourceCoverage = missingSourceCoverage || !source.Collected
		}
	}
	heap.Init(&cursors)

	output := newArchiveMergeOutput(provisional, outputBudget)
	encoder := json.NewEncoder(output)
	windowEnds := make([]time.Time, 0, min(len(bundles)*16, maxArchiveAnalysisRecords))
	collectWindowEnds := true
	sourceWindows := make([][]remoteArchiveSourceWindow, len(bundles))
	collectSourceWindows := true
	sourceWindowCount := 0
	windowGeneratorMarks := make([]int64, len(bundles))
	windowRecords := make([]IntervalSummaryRecord, 0, windowCapacity)
	windowOrigins := make([]string, 0, windowCapacity)
	progressExpected := make([]bool, len(bundles))
	for index, bundle := range bundles {
		progressExpected[index] = bundle.processorProgressExpected
	}
	sourceCumulativeTx := make([]int64, len(bundles))
	sourceIterations := make([]int64, len(bundles))
	previousSourceTimestamps := make([]time.Time, len(bundles))
	recordSourceWindow := func(cursor *remoteArchiveSummaryCursor, fleetIndex int) {
		sourceIterations[cursor.index] = cursor.record.CompletedIterations
		if !collectSourceWindows {
			return
		}
		if sourceWindowCount >= maxArchiveAnalysisRecords {
			sourceWindows = nil
			collectSourceWindows = false
			return
		}
		sourceWindowTx := cursor.record.TotalTx - sourceCumulativeTx[cursor.index]
		windowSeconds := remoteArchiveSummaryCadence.Seconds()
		if sourceWindowTx > 0 && cursor.record.TPS > 0 {
			windowSeconds = float64(sourceWindowTx) / cursor.record.TPS
		} else if previous := previousSourceTimestamps[cursor.index]; !previous.IsZero() {
			if seconds := cursor.timestamp.Sub(previous).Seconds(); seconds > 0 {
				windowSeconds = seconds
			}
		}
		if windowSeconds <= 0 {
			windowSeconds = remoteArchiveSummaryCadence.Seconds()
		}
		sourceWindows[cursor.index] = append(sourceWindows[cursor.index], remoteArchiveSourceWindow{
			end: cursor.timestamp, fleetIndex: fleetIndex, seconds: windowSeconds,
		})
		sourceWindowCount++
		sourceCumulativeTx[cursor.index] = cursor.record.TotalTx
		previousSourceTimestamps[cursor.index] = cursor.timestamp
	}
	var cumulativeTx, cumulativeErrors int64
	var previousFleetTimestamp time.Time
	for cursors.Len() > 0 {
		windowMark := count + 1
		anchor := heap.Pop(&cursors).(*remoteArchiveSummaryCursor)
		windowEnd := anchor.timestamp.Add(remoteArchiveSummaryMaxClockSkew)
		windowRecords = windowRecords[:0]
		windowOrigins = windowOrigins[:0]
		windowRecords = append(windowRecords, anchor.record)
		windowOrigins = append(windowOrigins, bundles[anchor.index].target)
		if progress := anchor.record.MessagingProgress; progress != nil {
			progressExpected[anchor.index] = progress.Lifecycle != "released"
		}
		windowGeneratorMarks[anchor.index] = windowMark
		recordSourceWindow(anchor, int(count))
		advanced, advanceErr := advanceRemoteArchiveSummaryCursor(anchor, bundles)
		if advanceErr != nil {
			return count, advanceErr
		}
		if advanced {
			heap.Push(&cursors, anchor)
		}
		deferred := make([]*remoteArchiveSummaryCursor, 0, len(bundles))
		for cursors.Len() > 0 && !cursors[0].timestamp.After(windowEnd) {
			cursor := heap.Pop(&cursors).(*remoteArchiveSummaryCursor)
			// Keep later rows from a generator already represented in this fleet
			// window pending while searching for peer publications behind it in the
			// heap. This pairs periodic and terminal drains by source ordinal instead
			// of producing [A], [A+B], [B] for tightly spaced 2x2 sequences.
			if windowGeneratorMarks[cursor.index] == windowMark {
				deferred = append(deferred, cursor)
				continue
			}
			windowRecords = append(windowRecords, cursor.record)
			windowOrigins = append(windowOrigins, bundles[cursor.index].target)
			if progress := cursor.record.MessagingProgress; progress != nil {
				progressExpected[cursor.index] = progress.Lifecycle != "released"
			}
			windowGeneratorMarks[cursor.index] = windowMark
			recordSourceWindow(cursor, int(count))
			advanced, advanceErr := advanceRemoteArchiveSummaryCursor(cursor, bundles)
			if advanceErr != nil {
				return count, advanceErr
			}
			if advanced {
				heap.Push(&cursors, cursor)
			}
		}
		for _, cursor := range deferred {
			heap.Push(&cursors, cursor)
		}
		var aggregate IntervalSummaryRecord
		var weightedResponse float64
		var attempts, bucketTx, bucketErrors int64
		var durationDigest *engine.TDigest
		durationDigestComplete := true
		var sourceThroughputGap float64
		var sourceThroughputMissed bool
		// Missing known peers or their interval rows do not prove zero work.
		// Retain observed volumes and moments without qualifying fleet skew.
		messagingIncomplete := missingSourceCoverage || len(windowRecords) < len(bundles)
		dropCounterAvailable := !messagingIncomplete
		for recordIndex, record := range windowRecords {
			if record.MessagingProgress != nil {
				protocol.MergeMessagingProgress(&aggregate.MessagingProgress, record.MessagingProgress, windowOrigins[recordIndex])
			}
			if record.Telemetry == nil || !record.Telemetry.DroppedResultsAvailable || record.Telemetry.DroppedResults < 0 {
				dropCounterAvailable = false
			}
			if aggregate.TestID == "" {
				aggregate.TestID, aggregate.TestName = record.TestID, record.TestName
			}
			if record.Timestamp.After(aggregate.Timestamp) {
				aggregate.Timestamp = record.Timestamp.UTC()
			}
			aggregate.ActiveVUsers += record.ActiveVUsers
			aggregate.TPS += record.TPS
			aggregate.TargetTPS += record.TargetTPS
			if record.ThroughputGapPct > sourceThroughputGap {
				sourceThroughputGap = record.ThroughputGapPct
			}
			sourceThroughputMissed = sourceThroughputMissed || record.ThroughputTargetMissed
			aggregate.Groups = mergeRemoteArchiveSummaryGroups(aggregate.Groups, record.Groups)
			if aggregate.Messaging == nil {
				aggregate.Messaging = map[string]*protocol.MessagingSummary{}
			}
			if len(record.Messaging) == 0 {
				messagingIncomplete = true
			}
			for name, s := range protocol.CloneMessagingSummaries(record.Messaging) {
				existing := aggregate.Messaging[name]
				protocol.MergeMessagingSummary(&existing, s)
				aggregate.Messaging[name] = existing
			}
			aggregate.Messaging = protocol.CloneMessagingSummaries(aggregate.Messaging)
			aggregate.Transactions = mergeIntervalTransactions(aggregate.Transactions, record.Transactions)
			aggregate.ByRegion = mergeIntervalTransactions(aggregate.ByRegion, record.ByRegion)
			aggregate.Browser = mergeRemoteArchiveSummaryBrowser(aggregate.Browser, record.Browser)
			aggregate.BrowserByDevice = mergeRemoteArchiveSummaryBrowserByDevice(aggregate.BrowserByDevice, record.BrowserByDevice)
			aggregate.Telemetry = mergeRemoteArchiveSummaryTelemetry(aggregate.Telemetry, record.Telemetry)
			aggregate.TelemetryStatus = mergeRemoteArchiveSummaryTelemetryStatus(aggregate.TelemetryStatus, record.TelemetryStatus)
			rowAttempts := record.TotalTx + record.TotalErrors
			if rowAttempts > 0 {
				if record.DurationDigest == nil || record.DurationDigest.Count <= 0 || record.DurationDigest.Count != rowAttempts {
					durationDigestComplete = false
				} else {
					if durationDigest == nil {
						durationDigest = engine.NewTDigest(record.DurationDigest.Compression)
					}
					durationDigest.Merge(engine.NewTDigestFromSnapshot(*record.DurationDigest))
				}
			}
			attempts += rowAttempts
			weightedResponse += record.AvgResponseMs * float64(rowAttempts)
			bucketTx += record.TotalTx
			bucketErrors += record.TotalErrors
			cumulativeTx += record.TotalTx
			cumulativeErrors += record.TotalErrors
			if singleGenerator {
				aggregate.P50Ms = record.P50Ms
				aggregate.P90Ms = record.P90Ms
				aggregate.P95Ms = record.P95Ms
				aggregate.P99Ms = record.P99Ms
				aggregate.PercentileQuality = archivePercentileQualitySourceGenerator
			} else if durationDigestComplete && durationDigest != nil && durationDigest.Count() == attempts {
				aggregate.P50Ms = durationDigest.Quantile(0.50) / float64(time.Millisecond)
				aggregate.P90Ms = durationDigest.Quantile(0.90) / float64(time.Millisecond)
				aggregate.P95Ms = durationDigest.Quantile(0.95) / float64(time.Millisecond)
				aggregate.P99Ms = durationDigest.Quantile(0.99) / float64(time.Millisecond)
				aggregate.PercentileQuality = archivePercentileQualityMergedDigest
			} else {
				aggregate.P50Ms, aggregate.P90Ms, aggregate.P95Ms, aggregate.P99Ms = 0, 0, 0, 0
				aggregate.PercentileQuality = archivePercentileQualityUnavailable
			}
		}
		progressMissing := false
		for index, expected := range progressExpected {
			if !expected {
				continue
			}
			found := false
			for recordIndex, record := range windowRecords {
				if windowOrigins[recordIndex] == bundles[index].target && record.MessagingProgress != nil {
					found = true
					break
				}
			}
			progressMissing = progressMissing || !found
		}
		if progressMissing && aggregate.MessagingProgress == nil {
			aggregate.MessagingProgress = protocol.NewUnavailableMessagingProgress("source_missing")
		}
		if aggregate.MessagingProgress != nil && (progressMissing || missingSourceCoverage) {
			aggregate.MessagingProgress.Incomplete = true
			aggregate.MessagingProgress.UnavailableReason = "source_missing"
		}
		if messagingIncomplete {
			for _, s := range aggregate.Messaging {
				if s != nil {
					s.Incomplete = true
				}
			}
		}
		aggregate.Transactions = compactIntervalTransactions(aggregate.Transactions)
		aggregate.ByRegion = compactIntervalTransactions(aggregate.ByRegion)
		for _, iterations := range sourceIterations {
			aggregate.CompletedIterations += iterations
		}
		if aggregate.Telemetry != nil {
			aggregate.Telemetry.DroppedResultsAvailable = dropCounterAvailable && aggregate.Telemetry.DroppedResultsAvailable
		}
		aggregate.TotalTx = cumulativeTx
		aggregate.TotalErrors = cumulativeErrors
		if attempts > 0 {
			aggregate.AvgResponseMs = weightedResponse / float64(attempts)
			aggregate.ErrorPct = float64(bucketErrors) / float64(bucketTx+bucketErrors) * 100
		}
		if aggregate.TargetTPS > 0 {
			aggregate.ThroughputGapPct, aggregate.ThroughputTargetMissed = throughputGap(aggregate.TPS, aggregate.TargetTPS)
		} else {
			aggregate.ThroughputGapPct = sourceThroughputGap
			aggregate.ThroughputTargetMissed = sourceThroughputMissed
		}
		if !previousFleetTimestamp.IsZero() && !aggregate.Timestamp.After(previousFleetTimestamp) {
			aggregate.Timestamp = previousFleetTimestamp.Add(time.Nanosecond)
		}
		previousFleetTimestamp = aggregate.Timestamp
		if err := encoder.Encode(aggregate); err != nil {
			return count, err
		}
		count++
		if collectWindowEnds {
			if len(windowEnds) >= maxArchiveAnalysisRecords {
				windowEnds = nil
				collectWindowEnds = false
			} else {
				windowEnds = append(windowEnds, aggregate.Timestamp)
			}
		}
	}
	if err := output.Flush(); err != nil {
		return count, err
	}
	if err := provisional.Sync(); err != nil {
		return count, err
	}
	if err := provisional.Close(); err != nil {
		return count, err
	}
	provisionalOpen = false

	intervals, exact, err := remoteArchiveFleetIntervalMetrics(
		bundles, windowEnds, sourceWindows, collectWindowEnds && collectSourceWindows,
	)
	if err != nil {
		return count, err
	}
	written, err := writeRemoteArchiveFleetSummaries(destinationPath, provisionalPath, intervals, exact, targetHistory, outputBudget)
	if err != nil {
		return count, err
	}
	if written != count {
		return count, fmt.Errorf("remote interval summary rewrite count %d does not match merged count %d", written, count)
	}
	return count, nil
}

func mergeRemoteArchiveSummaryGroups(destination, source map[string]*GroupLiveMetrics) map[string]*GroupLiveMetrics {
	if len(source) == 0 {
		return destination
	}
	if destination == nil {
		destination = make(map[string]*GroupLiveMetrics, len(source))
	}
	for name, incoming := range source {
		if incoming == nil {
			continue
		}
		current := destination[name]
		if current == nil {
			current = cloneGroupLiveMetrics(incoming)
			if current.Name == "" {
				current.Name = name
			}
			if current.PercentileQuality == "" {
				current.PercentileQuality = archivePercentileQualitySourceGenerator
			}
			destination[name] = current
			continue
		}

		currentAttempts := current.Transactions + current.Errors
		incomingAttempts := incoming.Transactions + incoming.Errors
		combinedAttempts := currentAttempts + incomingAttempts
		if combinedAttempts > 0 {
			current.AvgResponseMs = mergeRemoteArchiveWeightedAverage(
				current.AvgResponseMs, currentAttempts, incoming.AvgResponseMs, incomingAttempts,
			)
		}
		// A quantile cannot be combined by weighting generator quantile scalars.
		// The exact raw-event pass below replaces these values when group labels
		// are available; otherwise make the missing fleet distribution explicit.
		current.P50Ms, current.P90Ms, current.P95Ms, current.P99Ms = 0, 0, 0, 0
		current.PercentileQuality = archivePercentileQualityUnavailable
		sourceGap := current.ThroughputGapPct
		if incoming.ThroughputGapPct > sourceGap {
			sourceGap = incoming.ThroughputGapPct
		}
		sourceMissed := current.ThroughputTargetMissed || incoming.ThroughputTargetMissed
		current.VUsers += incoming.VUsers
		current.Transactions += incoming.Transactions
		current.Errors += incoming.Errors
		current.TPS += incoming.TPS
		current.TargetTPS += incoming.TargetTPS
		if current.Name == "" {
			current.Name = firstNonEmpty(incoming.Name, name)
		}
		if current.TargetTPS > 0 {
			current.ThroughputGapPct, current.ThroughputTargetMissed = throughputGap(current.TPS, current.TargetTPS)
		} else {
			current.ThroughputGapPct = sourceGap
			current.ThroughputTargetMissed = sourceMissed
		}
	}
	if len(destination) == 0 {
		return nil
	}
	return destination
}

func mergeRemoteArchiveWeightedAverage(left float64, leftWeight int64, right float64, rightWeight int64) float64 {
	totalWeight := leftWeight + rightWeight
	if totalWeight <= 0 {
		return 0
	}
	return (left*float64(leftWeight) + right*float64(rightWeight)) / float64(totalWeight)
}

func mergeRemoteArchiveSummaryBrowser(current, incoming *BrowserLiveMetrics) *BrowserLiveMetrics {
	if incoming == nil || incoming.Samples <= 0 {
		return current
	}
	if current == nil || current.Samples <= 0 {
		return cloneBrowserLiveMetrics(incoming)
	}
	currentSamples := current.Samples
	totalSamples := currentSamples + incoming.Samples
	current.AvgFCPMs = mergeRemoteArchiveWeightedAverage(current.AvgFCPMs, currentSamples, incoming.AvgFCPMs, incoming.Samples)
	current.AvgLCPMs = mergeRemoteArchiveWeightedAverage(current.AvgLCPMs, currentSamples, incoming.AvgLCPMs, incoming.Samples)
	current.AvgCLSScore = mergeRemoteArchiveWeightedAverage(current.AvgCLSScore, currentSamples, incoming.AvgCLSScore, incoming.Samples)
	current.AvgINPMs = mergeRemoteArchiveWeightedAverage(current.AvgINPMs, currentSamples, incoming.AvgINPMs, incoming.Samples)
	current.AvgTTFBMs = mergeRemoteArchiveWeightedAverage(current.AvgTTFBMs, currentSamples, incoming.AvgTTFBMs, incoming.Samples)
	current.AvgBlockingResources = mergeRemoteArchiveWeightedAverage(current.AvgBlockingResources, currentSamples, incoming.AvgBlockingResources, incoming.Samples)
	current.AvgLongTaskCount = mergeRemoteArchiveWeightedAverage(current.AvgLongTaskCount, currentSamples, incoming.AvgLongTaskCount, incoming.Samples)
	current.AvgJSHeapUsedMB = mergeRemoteArchiveWeightedAverage(current.AvgJSHeapUsedMB, currentSamples, incoming.AvgJSHeapUsedMB, incoming.Samples)
	current.AvgBrowserProcessCPU = mergeRemoteArchiveWeightedAverage(current.AvgBrowserProcessCPU, currentSamples, incoming.AvgBrowserProcessCPU, incoming.Samples)
	current.AvgBrowserProcessMem = mergeRemoteArchiveWeightedAverage(current.AvgBrowserProcessMem, currentSamples, incoming.AvgBrowserProcessMem, incoming.Samples)
	current.Samples = totalSamples
	return current
}

func mergeRemoteArchiveSummaryBrowserByDevice(destination, source map[string]*BrowserDeviceLiveMetrics) map[string]*BrowserDeviceLiveMetrics {
	if len(source) == 0 {
		return destination
	}
	if destination == nil {
		destination = make(map[string]*BrowserDeviceLiveMetrics, len(source))
	}
	for name, incoming := range source {
		if incoming == nil || incoming.Samples <= 0 {
			continue
		}
		name = strings.TrimSpace(name)
		if name == "" {
			name = strings.TrimSpace(incoming.DeviceProfile)
		}
		if name == "" {
			continue
		}
		current := destination[name]
		if current == nil || current.Samples <= 0 {
			copy := *incoming
			if copy.DeviceProfile == "" {
				copy.DeviceProfile = name
			}
			destination[name] = &copy
			continue
		}
		currentSamples := current.Samples
		current.AvgFCPMs = mergeRemoteArchiveWeightedAverage(current.AvgFCPMs, currentSamples, incoming.AvgFCPMs, incoming.Samples)
		current.AvgLCPMs = mergeRemoteArchiveWeightedAverage(current.AvgLCPMs, currentSamples, incoming.AvgLCPMs, incoming.Samples)
		current.AvgCLSScore = mergeRemoteArchiveWeightedAverage(current.AvgCLSScore, currentSamples, incoming.AvgCLSScore, incoming.Samples)
		current.AvgINPMs = mergeRemoteArchiveWeightedAverage(current.AvgINPMs, currentSamples, incoming.AvgINPMs, incoming.Samples)
		current.AvgTTFBMs = mergeRemoteArchiveWeightedAverage(current.AvgTTFBMs, currentSamples, incoming.AvgTTFBMs, incoming.Samples)
		current.AvgBlockingResources = mergeRemoteArchiveWeightedAverage(current.AvgBlockingResources, currentSamples, incoming.AvgBlockingResources, incoming.Samples)
		current.AvgLongTaskCount = mergeRemoteArchiveWeightedAverage(current.AvgLongTaskCount, currentSamples, incoming.AvgLongTaskCount, incoming.Samples)
		current.AvgJSHeapUsedMB = mergeRemoteArchiveWeightedAverage(current.AvgJSHeapUsedMB, currentSamples, incoming.AvgJSHeapUsedMB, incoming.Samples)
		current.AvgBrowserProcessCPU = mergeRemoteArchiveWeightedAverage(current.AvgBrowserProcessCPU, currentSamples, incoming.AvgBrowserProcessCPU, incoming.Samples)
		current.AvgBrowserProcessMem = mergeRemoteArchiveWeightedAverage(current.AvgBrowserProcessMem, currentSamples, incoming.AvgBrowserProcessMem, incoming.Samples)
		current.Samples += incoming.Samples
		if current.DeviceProfile == "" {
			current.DeviceProfile = firstNonEmpty(incoming.DeviceProfile, name)
		}
		if current.DevicePlatform == "" {
			current.DevicePlatform = incoming.DevicePlatform
		}
		if current.DeviceTier == "" {
			current.DeviceTier = incoming.DeviceTier
		}
		if current.BrowserEngine == "" {
			current.BrowserEngine = incoming.BrowserEngine
		}
	}
	if len(destination) == 0 {
		return nil
	}
	return destination
}

func mergeRemoteArchiveSummaryTelemetry(current, incoming *TelemetryHealth) *TelemetryHealth {
	if incoming == nil {
		if current != nil {
			current.DroppedResultsAvailable = false
		}
		return current
	}
	if current == nil {
		current = cloneTelemetryHealth(incoming)
		var validDrops bool
		current.DroppedResults, validDrops = mergeRetainedDropCounters(0, incoming.DroppedResults)
		current.DroppedResultsAvailable = current.DroppedResultsAvailable && validDrops
		return current
	}
	var validDrops bool
	current.DroppedResults, validDrops = mergeRetainedDropCounters(current.DroppedResults, incoming.DroppedResults)
	current.DroppedResultsAvailable = current.DroppedResultsAvailable && incoming.DroppedResultsAvailable && validDrops
	current.SheddedArrivals += incoming.SheddedArrivals
	current.DroppedSubscriberBatches += incoming.DroppedSubscriberBatches
	current.MissingMetricWindows += incoming.MissingMetricWindows
	if incoming.HistoryPoints > current.HistoryPoints {
		current.HistoryPoints = incoming.HistoryPoints
	}
	current.WebsocketClients += incoming.WebsocketClients
	current.WebsocketDroppedMessages += incoming.WebsocketDroppedMessages
	current.WebsocketDroppedEvents += incoming.WebsocketDroppedEvents
	current.CollectedAt = remoteArchiveEarlierNonZeroTime(current.CollectedAt, incoming.CollectedAt)
	current.GeneratorPublishedAt = remoteArchiveLaterTime(current.GeneratorPublishedAt, incoming.GeneratorPublishedAt)
	current.ControllerReceivedAt = remoteArchiveLaterTime(current.ControllerReceivedAt, incoming.ControllerReceivedAt)
	current.PublishedAt = remoteArchiveLaterTime(current.PublishedAt, incoming.PublishedAt)
	current.GeneratorProcessingLagMs = max(current.GeneratorProcessingLagMs, incoming.GeneratorProcessingLagMs)
	current.TransportLagMs = max(current.TransportLagMs, incoming.TransportLagMs)
	current.ControllerFanoutLagMs = max(current.ControllerFanoutLagMs, incoming.ControllerFanoutLagMs)
	current.BrowserRenderLagMs = max(current.BrowserRenderLagMs, incoming.BrowserRenderLagMs)
	current.SampleAgeMs = max(current.SampleAgeMs, incoming.SampleAgeMs)
	return current
}

func remoteArchiveEarlierNonZeroTime(current, incoming time.Time) time.Time {
	if current.IsZero() || (!incoming.IsZero() && incoming.Before(current)) {
		return incoming
	}
	return current
}

func remoteArchiveLaterTime(current, incoming time.Time) time.Time {
	if incoming.After(current) {
		return incoming
	}
	return current
}

func mergeRemoteArchiveSummaryTelemetryStatus(current, incoming *TelemetryRunHealth) *TelemetryRunHealth {
	if incoming == nil {
		return current
	}
	if current == nil {
		return cloneTelemetryRunHealth(incoming)
	}
	if remoteArchivePreferTelemetryStatus(incoming, current) {
		if incoming.Status != "" {
			current.Status = incoming.Status
		}
		current.Summary = incoming.Summary
		current.CurrentSignature = incoming.CurrentSignature
	}
	if remoteArchiveTelemetrySeverityRank(incoming.Severity) > remoteArchiveTelemetrySeverityRank(current.Severity) {
		current.Severity = incoming.Severity
	} else if current.Severity == "" {
		current.Severity = incoming.Severity
	}
	conditions := make(map[string]struct{}, len(current.Conditions)+len(incoming.Conditions))
	for _, condition := range current.Conditions {
		if condition = strings.TrimSpace(condition); condition != "" {
			conditions[condition] = struct{}{}
		}
	}
	for _, condition := range incoming.Conditions {
		if condition = strings.TrimSpace(condition); condition != "" {
			conditions[condition] = struct{}{}
		}
	}
	current.Conditions = current.Conditions[:0]
	for condition := range conditions {
		current.Conditions = append(current.Conditions, condition)
	}
	sort.Strings(current.Conditions)
	current.BreachActive = current.BreachActive || incoming.BreachActive
	current.SustainedBreach = current.SustainedBreach || incoming.SustainedBreach
	if incoming.BreachDurationSec > current.BreachDurationSec {
		current.BreachDurationSec = incoming.BreachDurationSec
	}
	if incoming.BreachStartedAt != nil && (current.BreachStartedAt == nil || incoming.BreachStartedAt.Before(*current.BreachStartedAt)) {
		startedAt := *incoming.BreachStartedAt
		current.BreachStartedAt = &startedAt
	}
	current.LastObservedAt = remoteArchiveLaterTime(current.LastObservedAt, incoming.LastObservedAt)
	current.LastTransitionAt = remoteArchiveLaterTime(current.LastTransitionAt, incoming.LastTransitionAt)
	return current
}

func remoteArchivePreferTelemetryStatus(candidate, current *TelemetryRunHealth) bool {
	candidateStatus, currentStatus := remoteArchiveTelemetryStatusRank(candidate.Status), remoteArchiveTelemetryStatusRank(current.Status)
	if candidateStatus != currentStatus {
		return candidateStatus > currentStatus
	}
	candidateSeverity, currentSeverity := remoteArchiveTelemetrySeverityRank(candidate.Severity), remoteArchiveTelemetrySeverityRank(current.Severity)
	if candidateSeverity != currentSeverity {
		return candidateSeverity > currentSeverity
	}
	return candidate.LastObservedAt.After(current.LastObservedAt)
}

func remoteArchiveTelemetryStatusRank(status string) int {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "breached":
		return 3
	case "degraded":
		return 2
	case "healthy":
		return 1
	default:
		return 0
	}
}

func remoteArchiveTelemetrySeverityRank(severity string) int {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "danger":
		return 3
	case "warning":
		return 2
	case "healthy":
		return 1
	default:
		return 0
	}
}

func advanceRemoteArchiveSummaryCursor(cursor *remoteArchiveSummaryCursor, bundles []remoteArchiveBundle) (bool, error) {
	previousTimestamp := cursor.timestamp
	var next IntervalSummaryRecord
	if err := cursor.decoder.Decode(&next); err != nil {
		if err == io.EOF {
			_ = cursor.file.Close()
			expected := bundles[cursor.index].manifest.IntervalSummaryCount
			if cursor.decoded != expected {
				return false, fmt.Errorf("remote interval summary count for target %q is %d, manifest declares %d", bundles[cursor.index].target, cursor.decoded, expected)
			}
			return false, nil
		}
		return false, err
	}
	cursor.decoded++
	next.ByRegion = controllerApprovedIntervalRegion(next.ByRegion, cursor.region)
	nextTimestamp := next.Timestamp.UTC()
	if nextTimestamp.Before(previousTimestamp) {
		return false, fmt.Errorf("remote interval summaries for target %q are not time ordered", bundles[cursor.index].target)
	}
	cursor.record = next
	cursor.timestamp = nextTimestamp
	return true, nil
}

func controllerApprovedIntervalRegion(in map[string]*IntervalTransactionMetrics, region string) map[string]*IntervalTransactionMetrics {
	region = strings.TrimSpace(region)
	if region == "" || len(in) == 0 {
		return nil
	}
	out := make(map[string]*IntervalTransactionMetrics, 1)
	for _, metric := range in {
		mergeIntervalTransactionMetric(out, region, metric)
	}
	return out
}

// remoteArchiveFleetIntervalMetrics recomputes interval evidence only while the
// source data stays inside the same explicit bounds used by archive analysis.
// This keeps finalization memory predictable. Returning exact=false is a normal
// outcome; callers preserve generator counts/rates and expose unavailable
// percentile quality instead of fabricating a weighted percentile.
type remoteArchiveFleetIntervalMetric struct {
	successes       int64
	errors          int64
	totalDurationMs float64
	p50             float64
	p90             float64
	p95             float64
	p99             float64
	hasPercentiles  bool
	groups          map[string]*remoteArchiveFleetGroupMetric
	transactions    map[string]*intervalTransactionAccumulator
}

type remoteArchiveFleetGroupMetric struct {
	successes       int64
	errors          int64
	totalDurationMs float64
	durations       []float64
	p50             float64
	p90             float64
	p95             float64
	p99             float64
	hasPercentiles  bool
}

type remoteArchiveFleetRawMetric struct {
	successes       int64
	errors          int64
	totalDurationMs float64
	durations       []float64
	groups          map[string]*remoteArchiveFleetGroupMetric
}

func addRemoteArchiveFleetRawMetric(metric *remoteArchiveFleetRawMetric, transaction TransactionEvent) {
	if metric == nil {
		return
	}
	if transaction.Success {
		metric.successes++
	} else {
		metric.errors++
	}
	metric.totalDurationMs += transaction.DurationMs
	metric.durations = append(metric.durations, transaction.DurationMs)
	groupName := strings.TrimSpace(transaction.Group)
	if groupName == "" {
		return
	}
	if metric.groups == nil {
		metric.groups = make(map[string]*remoteArchiveFleetGroupMetric)
	}
	group := metric.groups[groupName]
	if group == nil {
		group = &remoteArchiveFleetGroupMetric{}
		metric.groups[groupName] = group
	}
	if transaction.Success {
		group.successes++
	} else {
		group.errors++
	}
	group.totalDurationMs += transaction.DurationMs
	group.durations = append(group.durations, transaction.DurationMs)
}

func mergeRemoteArchiveFleetRawMetric(interval *remoteArchiveFleetIntervalMetric, durations *[]float64, incoming remoteArchiveFleetRawMetric) {
	if interval == nil || durations == nil {
		return
	}
	interval.successes += incoming.successes
	interval.errors += incoming.errors
	interval.totalDurationMs += incoming.totalDurationMs
	*durations = append(*durations, incoming.durations...)
	if len(incoming.groups) == 0 {
		return
	}
	if interval.groups == nil {
		interval.groups = make(map[string]*remoteArchiveFleetGroupMetric)
	}
	for name, source := range incoming.groups {
		if source == nil {
			continue
		}
		group := interval.groups[name]
		if group == nil {
			group = &remoteArchiveFleetGroupMetric{}
			interval.groups[name] = group
		}
		group.successes += source.successes
		group.errors += source.errors
		group.totalDurationMs += source.totalDurationMs
		group.durations = append(group.durations, source.durations...)
	}
}

func remoteArchiveFleetIntervalMetrics(bundles []remoteArchiveBundle, windowEnds []time.Time, sourceWindows [][]remoteArchiveSourceWindow, completeWindows bool) ([]remoteArchiveFleetIntervalMetric, bool, error) {
	if !completeWindows || len(windowEnds) == 0 {
		return nil, false, nil
	}
	if len(sourceWindows) != len(bundles) {
		return nil, false, fmt.Errorf("remote archive source-window plan has %d targets, want %d", len(sourceWindows), len(bundles))
	}
	var totalBytes int64
	for _, bundle := range bundles {
		path := strings.TrimSpace(bundle.transactionsPath)
		if path == "" {
			return nil, false, nil
		}
		file, info, err := openArchiveArtifactForRead(filepath.Dir(path), filepath.Base(path))
		if err != nil {
			return nil, false, err
		}
		_ = file.Close()
		if info.Size() < 0 || totalBytes > maxArchiveAnalysisBytes-info.Size() {
			return nil, false, nil
		}
		totalBytes += info.Size()
	}

	intervals := make([]remoteArchiveFleetIntervalMetric, len(windowEnds))
	durations := make([][]float64, len(windowEnds))
	groupNames := make(map[string]struct{})
	transactionNames := make(map[string]struct{})
	transactionCount := 0
	for bundleIndex, bundle := range bundles {
		bundleMetrics := make([]remoteArchiveFleetRawMetric, len(intervals))
		bundleBusinessMetrics := make([]remoteArchiveFleetRawMetric, len(intervals))
		bundleTransactions := make([]map[string]*intervalTransactionAccumulator, len(intervals))
		bundleBusinessTransactions := make([]map[string]*intervalTransactionAccumulator, len(intervals))
		bundleHasBusinessTransactions := false
		path := bundle.transactionsPath
		file, _, err := openArchiveArtifactForRead(filepath.Dir(path), filepath.Base(path))
		if err != nil {
			return nil, false, err
		}
		decoder := json.NewDecoder(file)
		bundleTransactionCount := int64(0)
		for {
			var transaction TransactionEvent
			decodeErr := decoder.Decode(&transaction)
			if decodeErr == io.EOF {
				break
			}
			if decodeErr != nil {
				_ = file.Close()
				return nil, false, decodeErr
			}
			transactionCount++
			bundleTransactionCount++
			if transactionCount > maxArchiveAnalysisRecords {
				_ = file.Close()
				return nil, false, nil
			}
			windows := sourceWindows[bundleIndex]
			if len(windows) == 0 {
				_ = file.Close()
				return nil, false, fmt.Errorf("remote archive target %q has transactions but no interval summary windows", bundle.target)
			}
			sourceIndex := sort.Search(len(windows), func(index int) bool {
				return !transaction.Timestamp.After(windows[index].end)
			})
			if sourceIndex == len(windows) {
				sourceIndex = len(windows) - 1
			}
			index := windows[sourceIndex].fleetIndex
			if index < 0 || index >= len(intervals) {
				_ = file.Close()
				return nil, false, fmt.Errorf("remote archive target %q maps a source interval to invalid fleet window %d", bundle.target, index)
			}
			addRemoteArchiveFleetRawMetric(&bundleMetrics[index], transaction)
			if transaction.BusinessTransaction {
				addRemoteArchiveFleetRawMetric(&bundleBusinessMetrics[index], transaction)
			}
			transactionName := intervalTransactionName(transaction.ActionName)
			if _, exists := transactionNames[transactionName]; !exists {
				if len(transactionNames) >= maxArchiveAnalysisDimensions {
					_ = file.Close()
					return nil, false, fmt.Errorf("remote archive exceeds %d distinct transaction names", maxArchiveAnalysisDimensions)
				}
				transactionNames[transactionName] = struct{}{}
			}
			addIntervalTransaction := func(metricsByInterval []map[string]*intervalTransactionAccumulator) {
				if metricsByInterval[index] == nil {
					metricsByInterval[index] = make(map[string]*intervalTransactionAccumulator)
				}
				metrics := metricsByInterval[index]
				transactionMetric := metrics[transactionName]
				if transactionMetric == nil {
					transactionMetric = &intervalTransactionAccumulator{sourceWindows: 1}
					metrics[transactionName] = transactionMetric
				}
				transactionMetric.count++
				if !transaction.Success {
					transactionMetric.errors++
				} else if windows[sourceIndex].seconds > 0 {
					transactionMetric.sourceTPS += 1 / windows[sourceIndex].seconds
				}
				transactionMetric.totalDurationMs += transaction.DurationMs
			}
			addIntervalTransaction(bundleTransactions)
			if transaction.BusinessTransaction {
				addIntervalTransaction(bundleBusinessTransactions)
				bundleHasBusinessTransactions = true
			}
			if groupName := strings.TrimSpace(transaction.Group); groupName != "" {
				if _, exists := groupNames[groupName]; !exists {
					if len(groupNames) >= maxArchiveAnalysisDimensions {
						_ = file.Close()
						return nil, false, fmt.Errorf("remote archive exceeds %d distinct transaction groups", maxArchiveAnalysisDimensions)
					}
					groupNames[groupName] = struct{}{}
				}
			}
		}
		if err := file.Close(); err != nil {
			return nil, false, err
		}
		if bundleTransactionCount != bundle.manifest.TransactionCount {
			return nil, false, fmt.Errorf("remote transaction count for target %q is %d, manifest declares %d", bundle.target, bundleTransactionCount, bundle.manifest.TransactionCount)
		}
		chosenTransactions := bundleTransactions
		chosenMetrics := bundleMetrics
		if bundleHasBusinessTransactions {
			chosenTransactions = bundleBusinessTransactions
			chosenMetrics = bundleBusinessMetrics
		}
		for intervalIndex, metrics := range chosenMetrics {
			mergeRemoteArchiveFleetRawMetric(&intervals[intervalIndex], &durations[intervalIndex], metrics)
		}
		for intervalIndex, metrics := range chosenTransactions {
			if len(metrics) == 0 {
				continue
			}
			if intervals[intervalIndex].transactions == nil {
				intervals[intervalIndex].transactions = make(map[string]*intervalTransactionAccumulator)
			}
			for name, incoming := range metrics {
				current := intervals[intervalIndex].transactions[name]
				if current == nil {
					current = &intervalTransactionAccumulator{}
					intervals[intervalIndex].transactions[name] = current
				}
				current.count += incoming.count
				current.errors += incoming.errors
				current.totalDurationMs += incoming.totalDurationMs
				current.sourceTPS += incoming.sourceTPS
				current.sourceWindows += incoming.sourceWindows
			}
		}
	}
	for index, values := range durations {
		if len(values) == 0 {
			continue
		}
		sort.Float64s(values)
		intervals[index].p50 = percentileFromSortedSamples(values, 0.50)
		intervals[index].p90 = percentileFromSortedSamples(values, 0.90)
		intervals[index].p95 = percentileFromSortedSamples(values, 0.95)
		intervals[index].p99 = percentileFromSortedSamples(values, 0.99)
		intervals[index].hasPercentiles = true
		for _, group := range intervals[index].groups {
			if len(group.durations) == 0 {
				continue
			}
			sort.Float64s(group.durations)
			group.p50 = percentileFromSortedSamples(group.durations, 0.50)
			group.p90 = percentileFromSortedSamples(group.durations, 0.90)
			group.p95 = percentileFromSortedSamples(group.durations, 0.95)
			group.p99 = percentileFromSortedSamples(group.durations, 0.99)
			group.hasPercentiles = true
			group.durations = nil
		}
	}
	return intervals, true, nil
}

func writeRemoteArchiveFleetSummaries(destinationPath, provisionalPath string, intervals []remoteArchiveFleetIntervalMetric, exact bool, targetHistory []remoteArchiveTargetSnapshot, budgets ...*archiveOutputBudget) (count int64, err error) {
	outputBudget, err := mergeBudget(filepath.Dir(destinationPath), budgets)
	if err != nil {
		return 0, err
	}
	if len(budgets) == 0 {
		defer outputBudget.close()
	}
	source, _, err := openArchiveArtifactForRead(filepath.Dir(provisionalPath), filepath.Base(provisionalPath))
	if err != nil {
		return 0, err
	}
	defer source.Close()
	destination, err := createArchiveArtifactExclusive(filepath.Dir(destinationPath), filepath.Base(destinationPath), 0o644, false)
	if err != nil {
		return 0, err
	}
	defer func() {
		if closeErr := destination.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	decoder := json.NewDecoder(source)
	output := newArchiveMergeOutput(destination, outputBudget)
	encoder := json.NewEncoder(output)
	var sourceCumulativeTx, exactCumulativeTx, exactCumulativeErrors int64
	var previousTimestamp time.Time
	for {
		var record IntervalSummaryRecord
		if err := decoder.Decode(&record); err != nil {
			if err == io.EOF {
				break
			}
			return count, err
		}
		if exact && int(count) < len(intervals) {
			interval := intervals[count]
			sourceWindowTx := record.TotalTx - sourceCumulativeTx
			sourceCumulativeTx = record.TotalTx

			// Preserve the generator's effective rate window when it is knowable
			// from its success count and TPS. This retains the generator-side clamp
			// for short terminal remainders while replacing the count with the exact
			// raw transaction count. Timestamp distance and the declared cadence are
			// bounded fallbacks for zero-count or legacy source rows.
			windowSeconds := remoteArchiveSummaryCadence.Seconds()
			if sourceWindowTx > 0 && record.TPS > 0 {
				windowSeconds = float64(sourceWindowTx) / record.TPS
			} else if !previousTimestamp.IsZero() {
				if seconds := record.Timestamp.Sub(previousTimestamp).Seconds(); seconds > 0 {
					windowSeconds = seconds
				}
			}
			if windowSeconds <= 0 {
				windowSeconds = remoteArchiveSummaryCadence.Seconds()
			}

			exactCumulativeTx += interval.successes
			exactCumulativeErrors += interval.errors
			record.TotalTx = exactCumulativeTx
			record.TotalErrors = exactCumulativeErrors
			record.TPS = float64(interval.successes) / windowSeconds
			attempts := interval.successes + interval.errors
			if attempts > 0 {
				record.AvgResponseMs = interval.totalDurationMs / float64(attempts)
				record.ErrorPct = float64(interval.errors) / float64(attempts) * 100
			} else {
				record.AvgResponseMs = 0
				record.ErrorPct = 0
			}
			if interval.hasPercentiles {
				record.P50Ms = interval.p50
				record.P90Ms = interval.p90
				record.P95Ms = interval.p95
				record.P99Ms = interval.p99
				record.PercentileQuality = archivePercentileQualityRecomputed
			} else {
				record.P50Ms, record.P90Ms, record.P95Ms, record.P99Ms = 0, 0, 0, 0
				record.PercentileQuality = archivePercentileQualityUnavailable
			}
			if record.TargetTPS > 0 {
				record.ThroughputGapPct, record.ThroughputTargetMissed = throughputGap(record.TPS, record.TargetTPS)
			}
			record.Groups = recomputeRemoteArchiveSummaryGroups(record.Groups, interval.groups, windowSeconds)
			record.Transactions = intervalTransactionsFromAccumulators(interval.transactions, windowSeconds)
		}
		applyRemoteArchiveTargetSnapshot(&record, targetHistory)
		if err := encoder.Encode(record); err != nil {
			return count, err
		}
		previousTimestamp = record.Timestamp
		count++
	}
	if err := output.Flush(); err != nil {
		return count, err
	}
	if err := destination.Sync(); err != nil {
		return count, err
	}
	return count, nil
}

func applyRemoteArchiveTargetSnapshot(record *IntervalSummaryRecord, snapshots []remoteArchiveTargetSnapshot) {
	if record == nil || record.Timestamp.IsZero() || len(snapshots) == 0 {
		return
	}
	index := sort.Search(len(snapshots), func(index int) bool {
		return !snapshots[index].timestamp.Before(record.Timestamp)
	})
	best := -1
	bestDistance := time.Duration(1<<63 - 1)
	for _, candidate := range []int{index - 1, index} {
		if candidate < 0 || candidate >= len(snapshots) {
			continue
		}
		distance := snapshots[candidate].timestamp.Sub(record.Timestamp)
		if distance < 0 {
			distance = -distance
		}
		if distance < bestDistance {
			best, bestDistance = candidate, distance
		}
	}
	// The controller and generator both stamp the same accepted batch window.
	// A tolerance only accommodates sub-cadence clock skew and the 1ns terminal
	// ordering adjustment; it must not carry a target across a missing history
	// interval or invent a constant target timeline.
	if best < 0 || bestDistance > remoteArchiveSummaryMaxClockSkew {
		return
	}
	snapshot := snapshots[best]
	if snapshot.telemetry != nil {
		archived := record.Telemetry
		record.Telemetry = cloneTelemetryHealth(snapshot.telemetry)
		record.Telemetry.DroppedResultsAvailable = snapshot.telemetry.DroppedResultsAvailable && snapshot.telemetry.DroppedResults >= 0
		// A complete controller cohort supplies independent counter evidence.
		// Metadata-only snapshots must not erase known archive measurements,
		// and a skew-tolerant match must not erase a higher observed loss peak.
		if archived != nil {
			record.Telemetry.DroppedResultsAvailable = record.Telemetry.DroppedResultsAvailable || (archived.DroppedResultsAvailable && archived.DroppedResults >= 0)
			record.Telemetry.DroppedResults = max(record.Telemetry.DroppedResults, archived.DroppedResults)
		}
	}
	if snapshot.telemetryStatus != nil {
		record.TelemetryStatus = cloneTelemetryRunHealth(snapshot.telemetryStatus)
	}
	record.TargetTPS = snapshot.targetTPS
	record.ThroughputGapPct, record.ThroughputTargetMissed = throughputGap(record.TPS, record.TargetTPS)
	if record.Groups == nil && len(snapshot.groups) > 0 {
		record.Groups = make(map[string]*GroupLiveMetrics, len(snapshot.groups))
	}
	for name, group := range record.Groups {
		if group == nil {
			continue
		}
		group.TargetTPS = snapshot.groups[name]
		group.ThroughputGapPct, group.ThroughputTargetMissed = throughputGap(group.TPS, group.TargetTPS)
	}
	for name, targetTPS := range snapshot.groups {
		if record.Groups[name] != nil {
			continue
		}
		gap, missed := throughputGap(0, targetTPS)
		record.Groups[name] = &GroupLiveMetrics{
			Name: name, TargetTPS: targetTPS, ThroughputGapPct: gap, ThroughputTargetMissed: missed,
			PercentileQuality: archivePercentileQualityUnavailable,
		}
	}
}

func recomputeRemoteArchiveSummaryGroups(
	groups map[string]*GroupLiveMetrics,
	exact map[string]*remoteArchiveFleetGroupMetric,
	windowSeconds float64,
) map[string]*GroupLiveMetrics {
	if len(exact) == 0 {
		return groups
	}
	if groups == nil {
		groups = make(map[string]*GroupLiveMetrics, len(exact))
	}
	for name, interval := range exact {
		if interval == nil {
			continue
		}
		group := groups[name]
		if group == nil {
			group = &GroupLiveMetrics{Name: name}
			groups[name] = group
		}
		group.Transactions = interval.successes
		group.Errors = interval.errors
		if windowSeconds > 0 {
			group.TPS = float64(interval.successes) / windowSeconds
		} else {
			group.TPS = 0
		}
		attempts := interval.successes + interval.errors
		if attempts > 0 {
			group.AvgResponseMs = interval.totalDurationMs / float64(attempts)
		} else {
			group.AvgResponseMs = 0
		}
		if interval.hasPercentiles {
			group.P50Ms = interval.p50
			group.P90Ms = interval.p90
			group.P95Ms = interval.p95
			group.P99Ms = interval.p99
			group.PercentileQuality = archivePercentileQualityRecomputed
		} else {
			group.P50Ms, group.P90Ms, group.P95Ms, group.P99Ms = 0, 0, 0, 0
			group.PercentileQuality = archivePercentileQualityUnavailable
		}
		if group.TargetTPS > 0 {
			group.ThroughputGapPct, group.ThroughputTargetMissed = throughputGap(group.TPS, group.TargetTPS)
		}
	}
	if len(groups) == 0 {
		return nil
	}
	return groups
}

func mergeRemoteArchiveDataset(
	destinationPath string,
	bundles []remoteArchiveBundle,
	kind string,
	sourcePath func(remoteArchiveBundle) string,
	expectedCount func(remoteArchiveBundle) int64,
	budgets ...*archiveOutputBudget,
) (err error) {
	outputBudget, err := mergeBudget(filepath.Dir(destinationPath), budgets)
	if err != nil {
		return err
	}
	if len(budgets) == 0 {
		defer outputBudget.close()
	}
	destination, err := createArchiveArtifactExclusive(filepath.Dir(destinationPath), filepath.Base(destinationPath), 0o644, false)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := destination.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	output := newArchiveMergeOutput(destination, outputBudget)
	for _, bundle := range bundles {
		path := sourcePath(bundle)
		source, _, openErr := openArchiveArtifactForRead(filepath.Dir(path), filepath.Base(path))
		if openErr != nil {
			return openErr
		}
		scanner := newArchiveJSONLScanner(source)
		var count int64
		for scanner.Scan() {
			line := scanner.Bytes()
			var object struct{}
			if decodeErr := json.Unmarshal(line, &object); decodeErr != nil {
				_ = source.Close()
				return fmt.Errorf("decode remote %s record for target %q: %w", kind, bundle.target, decodeErr)
			}
			if _, writeErr := output.Write(line); writeErr != nil {
				_ = source.Close()
				return writeErr
			}
			if _, writeErr := output.Write([]byte{'\n'}); writeErr != nil {
				_ = source.Close()
				return writeErr
			}
			count++
		}
		if scanErr := scanner.Err(); scanErr != nil {
			_ = source.Close()
			return fmt.Errorf("scan remote %s records for target %q: %w", kind, bundle.target, scanErr)
		}
		if closeErr := source.Close(); closeErr != nil {
			return closeErr
		}
		expected := expectedCount(bundle)
		if count != expected {
			return fmt.Errorf("remote %s count for target %q is %d, manifest declares %d", kind, bundle.target, count, expected)
		}
	}
	if err := output.Flush(); err != nil {
		return err
	}
	return destination.Sync()
}

// writeDistributedProviderLookups archives the controller's lookup outcomes.
// Like the capture evidence, they live with the run's monitoring artifacts.
func writeDistributedProviderLookups(root string, manifest *RunArchiveManifest, tr *TestRunner, test *ManagedTest, budgets ...*archiveOutputBudget) error {
	if manifest == nil || test == nil || test.ELKCapture == nil && test.DynatraceCapture == nil {
		return nil
	}
	outputBudget, err := mergeBudget(root, budgets)
	if err != nil {
		return err
	}
	if len(budgets) == 0 {
		defer outputBudget.close()
	}
	var artifacts *runArtifacts
	if tr != nil {
		tr.mu.RLock()
		artifacts = test.elkArtifacts
		tr.mu.RUnlock()
	} else {
		artifacts = test.elkArtifacts
	}
	var records []ProviderLookupRecord
	truncated := false
	if artifacts != nil {
		artifacts.stopMonitoring()
		artifacts.lookupMu.Lock()
		records = append(records, artifacts.providerLookups...)
		truncated = artifacts.providerLookupsTruncated
		artifacts.lookupMu.Unlock()
	}
	manifest.ProviderLookupsFile = providerLookupsFileName
	manifest.ProviderLookupsTruncated = truncated
	return writeArchiveMergeJSONL(filepath.Join(root, manifest.ProviderLookupsFile), outputBudget, func(encoder *json.Encoder) error {
		for _, record := range records {
			if err := encoder.Encode(record); err != nil {
				return err
			}
			manifest.ProviderLookupCount++
		}
		return nil
	})
}
