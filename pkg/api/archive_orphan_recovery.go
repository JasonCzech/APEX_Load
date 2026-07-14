package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/storage"
)

// orphanArchiveFinalizationStore provides the create-only transition used by
// startup recovery. The condition must be evaluated atomically with the insert:
// a recovery scan is never allowed to overwrite a newer finalization/refresh
// intent or recreate work after the archive catalog has committed.
type orphanArchiveFinalizationStore interface {
	SaveArchiveFinalizationJobIfOrphan(job *storage.ArchiveFinalizationJob) (bool, error)
}

type orphanArchiveCandidate struct {
	path     string
	identity os.FileInfo
}

// recoverOrphanedLocalRunArchives closes the crash window between committing a
// local manifest and committing the durable finalization outbox row. Only the
// execution leader performs recovery, and only a production store exposing an
// atomic create-if-orphan transition is eligible.
func (tr *TestRunner) recoverOrphanedLocalRunArchives(ctx context.Context) error {
	if tr == nil || tr.archiveStore == nil || tr.testStore == nil {
		return nil
	}
	orphanStore, ok := tr.archiveStore.(orphanArchiveFinalizationStore)
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
		archivePath := candidate.path
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		if !tr.isExecutionLeader() {
			return errors.Join(append(failures, errNotExecutionLeader)...)
		}
		record, manifest, err := orphanArchiveRecordFromDirectoryWithIdentity(ctx, archivePath, candidate.identity)
		if err != nil {
			failures = append(failures, fmt.Errorf("inspect orphan archive %q: %w", archivePath, err))
			continue
		}

		testRecord, err := tr.testStore.GetTest(record.TestID)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && testRecord == nil) {
			// A deleted test is not recoverable publication work. Deletion/cleanup
			// owns physical removal; importantly, recovery never recreates metadata.
			continue
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("load test %s for orphan recovery: %w", record.TestID, err))
			continue
		}
		if !isTerminalTestStatus(testRecord.Status) || !isTerminalTestStatus(manifest.Status) {
			continue
		}
		projectID := normalizeProjectID(testRecord.ProjectID)
		manifestProjectID := strings.TrimSpace(manifest.Provenance.ProjectID)
		if manifestProjectID != "" && normalizeProjectID(manifestProjectID) != projectID {
			failures = append(failures, fmt.Errorf("orphan archive %s belongs to project %q, durable test belongs to %q", record.TestID, manifestProjectID, projectID))
			continue
		}
		record.RefKey = strings.TrimSpace(testRecord.RefKey)
		created, err := orphanStore.SaveArchiveFinalizationJobIfOrphan(&storage.ArchiveFinalizationJob{
			TestID:           record.TestID,
			ProjectID:        projectID,
			ArchiveRecord:    *record,
			PersistArtifacts: true,
		})
		if errors.Is(err, sql.ErrNoRows) {
			// The test/catalog state changed after the scan. The conditional
			// transition intentionally turns that race into a harmless no-op.
			continue
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("queue orphan archive %s: %w", record.TestID, err))
			continue
		}
		_ = created // false means a catalog or newer outbox row already won.
	}
	return errors.Join(failures...)
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
	failures := make([]error, 0)
	inspectChildren := func(parent *os.Root, parentPath string) {
		if err := ctx.Err(); err != nil {
			failures = append(failures, err)
			return
		}
		entries, err := fs.ReadDir(parent.FS(), ".")
		if err != nil {
			failures = append(failures, fmt.Errorf("read archive recovery directory %q: %w", parentPath, err))
			return
		}
		for _, entry := range entries {
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
	}

	inspectChildren(rootHandle, absRoot)
	remoteRoot, remoteErr := openArchiveChildDirectoryAt(rootHandle, "remote", false, 0)
	if remoteErr == nil {
		inspectChildren(remoteRoot, filepath.Join(absRoot, "remote"))
		_ = remoteRoot.Close()
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
	return record, &manifest, nil
}
