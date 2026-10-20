package api

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/jasonczech/apexload/pkg/storage"
)

// Only a missing private source can use this path. A corrupt or unreadable local
// source still follows normal validation and prepared-manifest repair. The immutable manifest is uploaded last; matching
// its durable digest proves this exact publication completed before source loss.
func (tr *TestRunner) recoverUploadedArchiveWithoutSource(ctx context.Context, job *storage.ArchiveFinalizationJob, durable *storage.TestRecord) (bool, error) {
	if durable == nil || tr.archiveFinalizationOutbox() == nil {
		return false, nil
	}
	if _, ok := tr.archiveStore.(incarnationArchiveFinalizationStore); !ok {
		return false, nil
	}
	record := &job.ArchiveRecord
	if archiveRecordBackend(record) == archiveBackendFilesystem {
		return false, nil
	}
	if err := validateArchivePublicationPaths(record); err == nil || !errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if job.TestID != durable.ID || record.TestID != job.TestID || durable.EndTime == nil || !isTerminalTestStatus(durable.Status) {
		return true, errors.New("pending archive recovery requires the exact durable terminal run")
	}
	// A newly queued refresh still points to the previous catalog version. Leave
	// that case to normal catalog materialization, never recover the old version
	// as though it were the new opaque intent.
	expected := *record
	if err := bindArchiveRecordToFinalizationVersion(&expected, job.IntentToken); err != nil {
		return true, err
	}
	if expected.ArchiveURI != record.ArchiveURI || expected.ManifestURI != record.ManifestURI {
		return false, nil
	}
	if err := validatePublishedArchiveSourceRecord(record); err != nil {
		return true, err
	}
	runtime, err := archiveMaterializationRuntime(currentArchiveBackend(), record)
	if err != nil {
		return true, err
	}
	bucket, err := runtime.openBucket(ctx)
	if err != nil {
		return true, err
	}
	defer bucket.Close()
	data, err := readArchiveObjectLimited(ctx, bucket, runtime.objectKey(record.TestID, runArchiveManifestFileName), maxArchiveManifestDownloadBytes)
	if err != nil {
		return true, fmt.Errorf("pending archive source unavailable; read immutable commit marker: %w", err)
	}
	inspection, err := tr.validateArchiveFinalizationSource(job, record, durable, data)
	if err != nil {
		return true, err
	}
	identity := inspection.identity
	if err := tr.bindArchiveFinalizationLease(identity); err != nil {
		return true, err
	}
	// The crash may precede creation of the snapshot-backfill dirty intent.
	// Schedule one normal refresh atomically with catalog completion, preserving
	// any newer dirty token. Never edit an already uploaded immutable version.
	identity.RequireAdjunctRefresh = true
	if err := ctx.Err(); err != nil {
		return true, err
	}
	if !tr.isExecutionLeader() {
		return true, errNotExecutionLeader
	}
	committed, err := tr.commitArchiveFinalization(ctx, job, identity, inspection.manifest)
	if err != nil {
		return true, err
	}
	if committed {
		tr.attachFinalizedRunArchive(job.TestID, record, identity)
	}
	return true, nil
}
