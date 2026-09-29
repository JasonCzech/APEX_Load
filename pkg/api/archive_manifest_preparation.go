package api

import (
	"context"
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/storage"
)

type archiveManifestPreparationStore interface {
	PrepareArchiveFinalizationManifestIfToken(string, string, *storage.RunArchiveRecord, string) (bool, error)
}

func (tr *TestRunner) persistArchiveFinalizationArtifacts(ctx context.Context, job *storage.ArchiveFinalizationJob, test *ManagedTest) error {
	budget, err := newArchiveAdjunctBudget(ctx, resolveArchiveRoot())
	if err != nil {
		return err
	}
	preparer, ok := tr.archiveStore.(archiveManifestPreparationStore)
	if !ok || tr.archiveFinalizationOutbox() == nil {
		return tr.persistTestManifestArtifactWithPreparation(test, &job.ArchiveRecord, nil, budget)
	}
	if err := restorePreparedArchiveManifest(ctx, job, budget); err != nil {
		return err
	}
	return tr.persistTestManifestArtifactWithPreparation(test, &job.ArchiveRecord, func(data []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		prepared := job.ArchiveRecord
		prepared.ManifestSHA256 = sha256BytesHex(data)
		current, err := preparer.PrepareArchiveFinalizationManifestIfToken(job.TestID, job.IntentToken, &prepared, string(data))
		if err != nil {
			return fmt.Errorf("prepare archive manifest: %w", err)
		}
		if !current {
			return fmt.Errorf("archive manifest preparation was superseded")
		}
		job.ArchiveRecord = prepared
		job.PreparedManifestJSON = string(data)
		return ctx.Err()
	}, budget)
}

// An interrupted atomic manifest replacement can leave the prior local manifest
// with the next digest already durable. Recovery uses only bytes committed in
// that same database transaction, and checks every declared artifact before
// writing. Legacy jobs without a preparation marker remain fail-closed.
func restorePreparedArchiveManifest(ctx context.Context, job *storage.ArchiveFinalizationJob, budget *archiveOutputBudget) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if job.PreparedManifestJSON == "" {
		return nil
	}
	record := &job.ArchiveRecord
	local, err := readLocalFileLimitedContext(ctx, record.ManifestPath, maxArchiveManifestDownloadBytes)
	if err != nil {
		return err
	}
	if strings.EqualFold(sha256BytesHex(local), strings.TrimSpace(record.ManifestSHA256)) {
		return nil
	}
	data := []byte(job.PreparedManifestJSON)
	if len(data) > maxArchiveManifestDownloadBytes || !strings.EqualFold(sha256BytesHex(data), strings.TrimSpace(record.ManifestSHA256)) {
		return fmt.Errorf("prepared archive manifest does not match durable metadata")
	}
	manifest, names, err := decodeMaterializationManifest(record, data)
	if err != nil {
		return err
	}
	digests := make(map[string]string, len(manifest.Artifacts))
	for _, artifact := range manifest.Artifacts {
		digests[artifact.Name] = strings.TrimSpace(artifact.SHA256)
	}
	for _, name := range names {
		if name != runArchiveManifestFileName && len(digests[name]) != 64 {
			return fmt.Errorf("prepared archive artifact %q lacks a complete trust anchor", name)
		}
	}
	// The prepared bytes were checked above. Verify existing artifact files
	// against them without comparing the intentionally older local manifest.
	files := *record
	files.ManifestSHA256 = ""
	if err := validateMaterializedArchiveWithManifestContext(ctx, &files, record.ArchivePath, record.ManifestPath, manifest); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return writeArchiveArtifactDurableWithBudget(record.ArchivePath, runArchiveManifestFileName, data, 0o644, budget)
}
