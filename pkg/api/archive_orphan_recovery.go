package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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
	for _, archivePath := range candidates {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		if !tr.isExecutionLeader() {
			return errors.Join(append(failures, errNotExecutionLeader)...)
		}
		record, manifest, err := orphanArchiveRecordFromDirectory(ctx, archivePath)
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

func discoverOrphanArchiveDirectories(ctx context.Context, root string) ([]string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, nil
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve archive recovery root: %w", err)
	}
	rootInfo, err := os.Lstat(absRoot)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect archive recovery root: %w", err)
	}
	if !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("archive recovery root is not a non-symlink directory")
	}

	seen := make(map[string]struct{})
	failures := make([]error, 0)
	inspectChildren := func(parent string) {
		if err := ctx.Err(); err != nil {
			failures = append(failures, err)
			return
		}
		entries, err := os.ReadDir(parent)
		if err != nil {
			failures = append(failures, fmt.Errorf("read archive recovery directory %q: %w", parent, err))
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
			candidate := filepath.Join(parent, entry.Name())
			manifestInfo, err := os.Lstat(filepath.Join(candidate, runArchiveManifestFileName))
			if err != nil || !manifestInfo.Mode().IsRegular() || manifestInfo.Mode()&os.ModeSymlink != 0 {
				continue
			}
			seen[candidate] = struct{}{}
		}
	}

	inspectChildren(absRoot)
	remoteRoot := filepath.Join(absRoot, "remote")
	if remoteInfo, err := os.Lstat(remoteRoot); err == nil && remoteInfo.IsDir() && remoteInfo.Mode()&os.ModeSymlink == 0 {
		inspectChildren(remoteRoot)
	}

	candidates := make([]string, 0, len(seen))
	for candidate := range seen {
		candidates = append(candidates, candidate)
	}
	sort.Strings(candidates)
	return candidates, errors.Join(failures...)
}

func orphanArchiveRecordFromDirectory(ctx context.Context, archivePath string) (*storage.RunArchiveRecord, *RunArchiveManifest, error) {
	archivePath, err := filepath.Abs(strings.TrimSpace(archivePath))
	if err != nil {
		return nil, nil, err
	}
	testID := filepath.Base(archivePath)
	if err := validateArchiveRunID(testID); err != nil {
		return nil, nil, err
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
	data, err := readLocalFileLimitedContext(ctx, manifestPath, maxArchiveManifestDownloadBytes)
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
