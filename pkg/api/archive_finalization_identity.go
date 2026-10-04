package api

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

type incarnationArchiveFinalizationStore interface {
	CompleteArchiveFinalizationForRun(string, string, string, *storage.RunArchiveRecord, storage.ArchiveFinalizationRunIdentity) (bool, error)
}

// Inspect source identity before any adjunct mutation. On refresh, the verified
// catalog is the source which will be copied; a previous private staging path may
// no longer exist. Prepared bytes remain authoritative after interrupted writes.
func (tr *TestRunner) inspectArchiveFinalizationIdentity(ctx context.Context, job *storage.ArchiveFinalizationJob, durable *storage.TestRecord, useRefreshCatalog bool) (*storage.ArchiveFinalizationRunIdentity, error) {
	if durable == nil {
		return nil, nil
	}
	if durable.EndTime == nil || !isTerminalTestStatus(durable.Status) {
		return nil, errors.New("archive finalization requires a terminal run identity")
	}
	if job.TestID != durable.ID || job.ArchiveRecord.TestID != job.TestID {
		return nil, errors.New("archive finalization record does not match the durable test ID")
	}
	record := &job.ArchiveRecord
	var data []byte
	var err error
	if useRefreshCatalog && job.RefreshToken != "" {
		catalog, catalogErr := tr.archiveStore.GetRunArchive(job.TestID)
		if catalogErr != nil && !errors.Is(catalogErr, sql.ErrNoRows) {
			return nil, catalogErr
		}
		if catalog != nil {
			record = catalog
			if archiveRecordBackend(catalog) == archiveBackendFilesystem {
				data, err = readFinalizationLocalManifest(ctx, catalog)
			} else {
				if digest, e := hex.DecodeString(catalog.ManifestSHA256); e != nil || len(digest) != 32 {
					return nil, errors.New("refresh source lacks manifest trust anchor")
				}
				runtime, e := archiveMaterializationRuntime(currentArchiveBackend(), catalog)
				if e != nil {
					return nil, e
				}
				bucket, e := runtime.openBucket(ctx)
				if e != nil {
					return nil, e
				}
				defer bucket.Close()
				data, err = readArchiveObjectLimited(ctx, bucket, runtime.objectKey(catalog.TestID, runArchiveManifestFileName), maxArchiveManifestDownloadBytes)
			}
		}
	}
	if data == nil && err == nil {
		if job.PreparedManifestJSON != "" {
			data = []byte(job.PreparedManifestJSON)
			if len(data) > maxArchiveManifestDownloadBytes || len(job.ArchiveRecord.ManifestSHA256) != 64 {
				return nil, errors.New("invalid prepared manifest trust anchor")
			}
		} else {
			data, err = readFinalizationLocalManifest(ctx, record)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("read finalization identity: %w", err)
	}
	return tr.validateArchiveFinalizationIdentity(job, record, durable, data)
}

func (tr *TestRunner) validateArchiveFinalizationIdentity(job *storage.ArchiveFinalizationJob, record *storage.RunArchiveRecord, durable *storage.TestRecord, data []byte) (*storage.ArchiveFinalizationRunIdentity, error) {
	if durable == nil || durable.EndTime == nil || !isTerminalTestStatus(durable.Status) {
		return nil, errors.New("archive finalization requires a terminal run identity")
	}
	if job.TestID != durable.ID || record.TestID != job.TestID {
		return nil, errors.New("archive finalization record does not match the durable test ID")
	}
	if record.ManifestSHA256 != "" && !strings.EqualFold(sha256BytesHex(data), record.ManifestSHA256) {
		return nil, errors.New("finalization manifest differs from durable trust anchor")
	}
	manifest, _, err := decodeMaterializationManifest(record, data)
	if err != nil {
		return nil, err
	}
	if normalizeProjectID(job.ProjectID) != normalizeProjectID(durable.ProjectID) ||
		(strings.TrimSpace(manifest.Provenance.ProjectID) != "" && normalizeProjectID(manifest.Provenance.ProjectID) != normalizeProjectID(durable.ProjectID)) ||
		manifest.Status != durable.Status || !archiveRunTimestampMatches(archiveManifestRequestedAt(manifest), durable.StartTime, tr.testStore) ||
		!archiveRunTimestampMatches(manifest.CompletedAt, *durable.EndTime, tr.testStore) ||
		!archiveRunTimestampMatches(record.CompletedAt, *durable.EndTime, tr.testStore) {
		return nil, errors.New("archive finalization source does not match the durable terminal incarnation")
	}
	return &storage.ArchiveFinalizationRunIdentity{ProjectID: normalizeProjectID(durable.ProjectID), StartTime: durable.StartTime, EndTime: *durable.EndTime, Status: durable.Status}, nil
}

func readFinalizationLocalManifest(ctx context.Context, record *storage.RunArchiveRecord) ([]byte, error) {
	if err := validateArchivePublicationPaths(record); err != nil {
		return nil, err
	}
	root, err := openArchiveDirectoryNoSymlink(record.ArchivePath)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	data, _, err := readArchiveFileAtRootLimitedContext(ctx, root, runArchiveManifestFileName, maxArchiveManifestDownloadBytes)
	return data, err
}

func (tr *TestRunner) bindArchiveFinalizationLease(identity *storage.ArchiveFinalizationRunIdentity) error {
	if identity != nil && tr.durableExecutionFence {
		_, owner, _, err := tr.generatorExecutionClaim()
		if err != nil || owner == "" {
			return errNotExecutionLeader
		}
		identity.LeaseName, identity.LeaseOwner = executionLeaseName, owner
	}
	return nil
}

// A false result may mean lease loss with this same intent still current. Only
// an actually superseded token is eligible for version cleanup; never attach it.
func (tr *TestRunner) commitArchiveFinalization(job *storage.ArchiveFinalizationJob, identity *storage.ArchiveFinalizationRunIdentity) (bool, error) {
	outbox := tr.archiveFinalizationOutbox()
	var committed bool
	var err error
	if identity != nil {
		committed, err = tr.archiveStore.(incarnationArchiveFinalizationStore).CompleteArchiveFinalizationForRun(job.TestID, job.IntentToken, job.RefreshToken, &job.ArchiveRecord, *identity)
	} else {
		committed, err = outbox.CompleteArchiveFinalization(job.TestID, job.IntentToken, job.RefreshToken, &job.ArchiveRecord)
	}
	if err != nil {
		return false, fmt.Errorf("commit archive finalization: %w", err)
	}
	if !committed {
		current, err := outbox.GetArchiveFinalizationJob(job.TestID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return false, err
		}
		if current != nil && current.IntentToken == job.IntentToken {
			return false, errors.New("archive completion was fenced; current publication preserved")
		}
		return false, tr.enqueueArchiveVersionCleanup(job.ProjectID, &job.ArchiveRecord)
	}
	name := ""
	initiatedBy := ""
	if tr.testStore != nil {
		if test, err := tr.testStore.GetTest(job.TestID); err == nil && test != nil {
			name = test.Name
			initiatedBy = test.CreatedBySubject
		}
	}
	tr.recordBackgroundActivity(&storage.ObjectActivityEvent{ID: "archive:" + job.TestID + ":" + job.IntentToken, Action: "archive", ActorType: "system", ActorSubject: "archive-finalizer", ActorDisplayName: "Automatic result archiving", InitiatedBy: initiatedBy, ProjectID: job.ProjectID, ResourceType: "test", ResourceID: job.TestID, ResourceName: name, Status: "success", CreatedAt: time.Now().UTC()})
	return true, nil
}
