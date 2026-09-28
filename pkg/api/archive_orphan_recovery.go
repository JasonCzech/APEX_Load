package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// orphanArchiveFinalizationStore provides the create-only transition used by
// startup recovery. The condition must be evaluated atomically with the insert:
// a recovery scan is never allowed to overwrite a newer finalization/refresh
// intent or recreate work after the archive catalog has committed.
type orphanArchiveFinalizationStore interface {
	SaveArchiveFinalizationJobIfOrphan(job *storage.ArchiveFinalizationJob) (bool, error)
}

type incarnationOrphanArchiveFinalizationStore interface {
	SaveArchiveFinalizationJobIfOrphanForRun(*storage.ArchiveFinalizationJob, time.Time, string, string, string) (bool, error)
}

type orphanArchiveCandidate struct {
	path     string
	identity os.FileInfo
}

const maxOrphanArchiveRecoveryCandidates = 10_000

// recoverOrphanedLocalRunArchives closes the crash window between committing a
// local manifest and committing the durable finalization outbox row. Only the
// execution leader performs recovery, and only a production store exposing an
// atomic create-if-orphan transition is eligible.
func (tr *TestRunner) recoverOrphanedLocalRunArchives(ctx context.Context) error {
	if tr == nil || tr.archiveStore == nil || tr.testStore == nil {
		return nil
	}
	orphanStore, ok := tr.archiveStore.(incarnationOrphanArchiveFinalizationStore)
	if !ok {
		return nil
	}
	if !tr.isExecutionLeader() {
		return errNotExecutionLeader
	}
	ctx, cancel := tr.archiveOperationContext(ctx)
	defer cancel()

	candidates, discoveryErr := discoverOrphanArchiveDirectories(ctx, resolveArchiveRoot())
	failures := make([]error, 0)
	if discoveryErr != nil {
		failures = append(failures, discoveryErr)
	}
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		if err := tr.recoverOrphanedLocalRunArchive(ctx, orphanStore, candidate); err != nil {
			failures = append(failures, err)
		}
	}

	return errors.Join(failures...)
}

func (tr *TestRunner) recoverOrphanedLocalRunArchive(ctx context.Context, store incarnationOrphanArchiveFinalizationStore, candidate orphanArchiveCandidate) error {
	if !tr.isExecutionLeader() {
		return errNotExecutionLeader
	}
	release, err := lockArchivePublicationSource(ctx, filepath.Base(candidate.path))
	if err != nil {
		return err
	}
	defer release()
	return tr.recoverOrphanedLocalRunArchiveUnderSourceFence(ctx, store, candidate)
}

func (tr *TestRunner) recoverOrphanedLocalRunArchiveUnderSourceFence(ctx context.Context, store incarnationOrphanArchiveFinalizationStore, candidate orphanArchiveCandidate) error {
	if !tr.isExecutionLeader() {
		return errNotExecutionLeader
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Already cataloged, pending, deleted or resolved runs are not orphans. Check
	// durable ownership before reading payloads so startup never rehashes the
	// entire retained archive history.
	row, err := tr.testStore.GetTest(filepath.Base(candidate.path))
	if errors.Is(err, sql.ErrNoRows) || (err == nil && row == nil) {
		return nil
	}
	if err != nil {
		return err
	}
	if !isTerminalTestStatus(row.Status) {
		return nil
	}
	existing, err := tr.existingCollectedArchive(managedTestFromRecord(row))
	if errors.Is(err, storage.ErrArchiveCleanupPending) {
		return nil
	}
	if err != nil {
		return err
	}
	if existing != nil {
		return nil
	}
	record, manifest, err := orphanArchiveRecordFromDirectoryWithIdentity(ctx, candidate.path, candidate.identity)
	if err != nil {
		return fmt.Errorf("inspect orphan archive %q: %w", candidate.path, err)
	}
	if !isTerminalTestStatus(manifest.Status) {
		return nil
	}
	project := normalizeProjectID(row.ProjectID)
	if declared := strings.TrimSpace(manifest.Provenance.ProjectID); declared != "" && normalizeProjectID(declared) != project {
		return fmt.Errorf("orphan archive %s belongs to another project", record.TestID)
	}
	if row.EndTime == nil || manifest.Status != row.Status || !archiveRunTimestampMatches(archiveManifestRequestedAt(manifest), row.StartTime, tr.testStore) || !archiveRunTimestampMatches(manifest.CompletedAt, *row.EndTime, tr.testStore) {
		return fmt.Errorf("orphan archive %s does not match the durable terminal incarnation", record.TestID)
	}
	lease, owner := "", ""
	if tr.durableExecutionFence {
		_, holder, _, err := tr.generatorExecutionClaim()
		if err != nil {
			return err
		}
		lease, owner = executionLeaseName, holder
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	record.RefKey = strings.TrimSpace(row.RefKey)
	return tr.trackDurableWrite(func() error {
		_, err := store.SaveArchiveFinalizationJobIfOrphanForRun(&storage.ArchiveFinalizationJob{TestID: record.TestID, ProjectID: project, ArchiveRecord: *record, PersistArtifacts: true}, archiveManifestRequestedAt(manifest), manifest.Status, lease, owner)
		return err
	})
}

func discoverOrphanArchiveDirectories(ctx context.Context, root string) ([]orphanArchiveCandidate, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, nil
	}
	absRoot, err := resolveArchiveDirectoryRoot(root, false)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("resolve archive recovery root: %w", err)
	}
	rootHandle, err := openArchiveDirectoryNoSymlink(absRoot)
	if err != nil {
		return nil, fmt.Errorf("inspect archive recovery root: %w", err)
	}
	defer rootHandle.Close()

	seen := make(map[string]orphanArchiveCandidate)
	remaining := maxOrphanArchiveRecoveryCandidates
	failures := make([]error, 0)
	inspectChildren := func(parent *os.Root, parentPath string) {
		if err := ctx.Err(); err != nil {
			failures = append(failures, err)
			return
		}
		directory, err := parent.Open(".")
		if err != nil {
			failures = append(failures, err)
			return
		}
		defer directory.Close()
		for {
			entries, readErr := directory.ReadDir(256)

			for _, entry := range entries {
				remaining--
				if remaining < 0 {
					failures = append(failures, fmt.Errorf("archive orphan recovery entry limit %d exceeded", maxOrphanArchiveRecoveryCandidates))
					return
				}
				if err := ctx.Err(); err != nil {
					failures = append(failures, err)
					return
				}
				if entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() {
					continue
				}
				if err := validateArchiveRunID(entry.Name()); err != nil {
					continue
				}
				runRoot, err := openArchiveChildDirectoryAt(parent, entry.Name(), false, 0)
				if err != nil {
					continue
				}
				identity, identityErr := runRoot.Stat(".")
				manifest, _, manifestErr := openArchiveArtifactForReadAt(runRoot, runArchiveManifestFileName)
				if manifest != nil {
					_ = manifest.Close()
				}
				_ = runRoot.Close()
				if identityErr != nil || manifestErr != nil {
					continue
				}
				candidatePath := filepath.Join(parentPath, entry.Name())
				seen[candidatePath] = orphanArchiveCandidate{path: candidatePath, identity: identity}
			}
			if readErr == io.EOF {
				return
			}
			if readErr != nil {
				failures = append(failures, readErr)
				return
			}
		}
	}

	inspectChildren(rootHandle, absRoot)
	remoteRoot, remoteErr := openArchiveChildDirectoryAt(rootHandle, "remote", false, 0)
	if remoteErr == nil {
		inspectChildren(remoteRoot, filepath.Join(absRoot, "remote"))
		_ = remoteRoot.Close()
	} else if !os.IsNotExist(remoteErr) {
		failures = append(failures, remoteErr)
	}

	candidates := make([]orphanArchiveCandidate, 0, len(seen))
	for candidate := range seen {
		candidates = append(candidates, seen[candidate])
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].path < candidates[j].path })
	return candidates, errors.Join(failures...)
}

func orphanArchiveRecordFromDirectory(ctx context.Context, archivePath string) (*storage.RunArchiveRecord, *RunArchiveManifest, error) {
	return orphanArchiveRecordFromDirectoryWithIdentity(ctx, archivePath, nil)
}

func orphanArchiveRecordFromDirectoryWithIdentity(ctx context.Context, archivePath string, expectedIdentity os.FileInfo) (*storage.RunArchiveRecord, *RunArchiveManifest, error) {
	archivePath, err := filepath.Abs(strings.TrimSpace(archivePath))
	if err != nil {
		return nil, nil, err
	}
	testID := filepath.Base(archivePath)
	if err := validateArchiveRunID(testID); err != nil {
		return nil, nil, err
	}
	parent, err := openArchiveDirectoryNoSymlink(filepath.Dir(archivePath))
	if err != nil {
		return nil, nil, err
	}
	defer parent.Close()
	runRoot, err := openArchiveChildDirectoryAt(parent, testID, false, 0)
	if err != nil {
		return nil, nil, err
	}
	defer runRoot.Close()
	runIdentity, err := runRoot.Stat(".")
	if err != nil {
		return nil, nil, err
	}
	if expectedIdentity != nil && !os.SameFile(expectedIdentity, runIdentity) {
		return nil, nil, fmt.Errorf("orphan archive directory %q was replaced after discovery", archivePath)
	}
	manifestPath := filepath.Join(archivePath, runArchiveManifestFileName)
	record := &storage.RunArchiveRecord{
		TestID:       testID,
		ArchivePath:  archivePath,
		ManifestPath: manifestPath,
	}
	if err := validateArchivePublicationPaths(record); err != nil {
		return nil, nil, err
	}
	data, _, err := readArchiveFileAtRootLimitedContext(ctx, runRoot, runArchiveManifestFileName, maxArchiveManifestDownloadBytes)
	if err != nil {
		return nil, nil, err
	}
	var manifest RunArchiveManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, nil, fmt.Errorf("decode archive manifest: %w", err)
	}
	if strings.TrimSpace(manifest.SchemaVersion) == "" {
		manifest.SchemaVersion = archiveSchemaVersion
	}
	if manifest.TestID != testID {
		return nil, nil, fmt.Errorf("archive manifest test id %q does not match directory %q", manifest.TestID, testID)
	}
	if err := validateRunArchiveManifestArtifacts(&manifest); err != nil {
		return nil, nil, err
	}

	backend := currentArchiveBackend()
	record.SchemaVersion = manifest.SchemaVersion
	record.ArchiveBackend = backend.Backend
	record.ManifestSHA256 = sha256BytesHex(data)
	record.ArchiveURI = backend.runURI(testID)
	record.ManifestURI = backend.manifestURI(testID)
	record.TransactionCount = manifest.TransactionCount
	record.ResourceSampleCount = manifest.ResourceSampleCount
	record.IntervalSummaryCount = manifest.IntervalSummaryCount
	record.CreatedAt = manifest.CreatedAt
	record.CompletedAt = manifest.CompletedAt
	if err := validateOrphanArchiveArtifacts(ctx, runRoot, &manifest, int64(len(data))); err != nil {
		return nil, nil, err
	}
	return record, &manifest, nil
}

// Generator archive creation can differ from the controller request time.
// Legacy manifests without provenance can only use their creation timestamp.
func archiveManifestRequestedAt(manifest *RunArchiveManifest) time.Time {
	if !manifest.Provenance.RequestedAt.IsZero() {
		return manifest.Provenance.RequestedAt
	}
	return manifest.CreatedAt
}
