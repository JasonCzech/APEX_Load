package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// A catalog can be discovered without materializing every historical artifact.
// Artifact routes still verify the complete archive before serving its contents.
func storedArchiveCatalogAvailable(record *storage.RunArchiveRecord) bool {
	if record == nil {
		return false
	}
	if archiveRecordBackend(record) == archiveBackendFilesystem {
		return runArchiveRecordAvailable(record)
	}
	_, err := archiveMaterializationRuntime(currentArchiveBackend(), record)
	return err == nil
}

type archiveCatalogIdentity struct {
	executionGeneration uint64
	runIncarnation      string
	projectHash         [32]byte
	requestedAt         time.Time
}

// Only single-run terminal reads consult the shared catalog. List polling retains
// its bounded snapshot scan instead of issuing a catalog query per row.
func (tr *TestRunner) attachTerminalArchiveCatalog(test *ManagedTest) {
	if test == nil || tr.leadership == nil || tr.testStore == nil || tr.archiveStore == nil || !isTerminalTestStatus(test.Status) {
		return
	}
	prior := test.Archive
	test.Archive = nil
	record, found, err := tr.loadRunArchiveFromStore(test.ID)
	if err != nil || !found {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	identity, err := tr.archiveReadCaches().readArchiveCatalogIdentity(ctx, record)
	if err != nil || identity.runIncarnation != managedTestRunIncarnation(test) || identity.executionGeneration != test.durableExecutionGeneration || identity.projectHash != sha256.Sum256([]byte(normalizeProjectID(test.ProjectID))) ||
		(!identity.requestedAt.IsZero() && !test.CreatedAt.IsZero() && !archiveRunTimestampMatches(identity.requestedAt, test.CreatedAt, tr.testStore)) {
		return
	}
	// A peer can replace the run or catalog during manifest I/O. Never attach that
	// earlier version to the replacement identity, including the local canonical.
	durable, err := tr.testStore.GetTest(test.ID)
	if err != nil || !sameArchiveRunIncarnation(test, durable) || !sameStoredTestIdentity(test, durable) || !archiveRunTimestampMatches(test.CreatedAt, durable.StartTime, tr.testStore) {
		return
	}
	latest, found, err := tr.loadRunArchiveFromStore(test.ID)
	if err != nil || !found || archiveCatalogVersion(latest) != archiveCatalogVersion(record) {
		return
	}
	test.Archive = cloneRunArchiveRecord(record)
	tr.mu.Lock()
	if current := tr.tests[test.ID]; sameStoredTestIdentity(current, durable) && sameArchiveRunIncarnation(current, durable) && archiveCatalogVersion(current.Archive) == archiveCatalogVersion(prior) {
		current.Archive = cloneRunArchiveRecord(record)
		tr.archives[test.ID] = cloneRunArchiveRecord(record)
	}
	tr.mu.Unlock()
}

func archiveCatalogVersion(record *storage.RunArchiveRecord) string {
	if record == nil {
		return ""
	}
	data, _ := json.Marshal(record)
	return sha256BytesHex(data)
}

func (c *archiveReadCaches) readArchiveCatalogIdentity(ctx context.Context, record *storage.RunArchiveRecord) (*archiveCatalogIdentity, error) {
	if record == nil {
		return nil, errors.New("archive catalog is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	backend := currentArchiveBackend()
	object := archiveRecordBackend(record) != archiveBackendFilesystem
	if object {
		var err error
		backend, err = archiveMaterializationRuntime(backend, record)
		if err != nil {
			return nil, err
		}
	}
	baseCtx := context.Background()
	if strings.TrimSpace(record.ManifestSHA256) == "" {
		baseCtx = ctx
	}
	load := func() (*archiveCatalogIdentity, error) {
		// Fail closed on saturation; an unbounded number of summary requests must
		// never produce an unbounded number of manifest downloads or decodes.
		select {
		case c.catalogReadSlots <- struct{}{}:
		default:
			return nil, errors.New("archive catalog reader busy")
		}
		defer func() { <-c.catalogReadSlots }()
		loadCtx, cancel := context.WithTimeout(baseCtx, 2*time.Second)
		defer cancel()
		var manifest *RunArchiveManifest
		var err error
		if object {
			bucket, openErr := backend.openBucket(loadCtx)
			if openErr != nil {
				return nil, openErr
			}
			defer bucket.Close()
			data, readErr := readArchiveObjectLimited(loadCtx, bucket, backend.objectKey(record.TestID, runArchiveManifestFileName), maxArchiveManifestDownloadBytes)
			if readErr != nil {
				return nil, readErr
			}
			if expected := strings.TrimSpace(record.ManifestSHA256); expected != "" && !strings.EqualFold(sha256BytesHex(data), expected) {
				return nil, errors.New("archive manifest digest mismatch")
			}
			manifest, _, err = decodeMaterializationManifest(record, data)
		} else {
			manifest, err = loadRunArchiveManifestFromRecord(record)
		}
		if err != nil {
			return nil, err
		}
		if manifest.TestID != record.TestID {
			return nil, errors.New("archive manifest identity mismatch")
		}
		incarnation, err := archiveManifestRunIncarnation(manifest)
		if err != nil {
			return nil, err
		}
		return &archiveCatalogIdentity{executionGeneration: manifest.Provenance.ExecutionGeneration, runIncarnation: incarnation, projectHash: sha256.Sum256([]byte(normalizeProjectID(manifest.Provenance.ProjectID))), requestedAt: manifest.Provenance.RequestedAt}, nil
	}
	// Only digest-anchored identities are immutable. Legacy manifests are read
	// afresh and must not become permanent identity proofs after replacement.
	if strings.TrimSpace(record.ManifestSHA256) == "" {
		return load()
	}
	key := record.TestID + "|" + archiveObjectNamespace(backend) + "|" + archiveCatalogVersion(record)
	return c.catalogIdentities.getOrLoadContext(ctx, key, load)
}
