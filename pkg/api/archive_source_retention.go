package api

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/filelock"
	"github.com/jasonczech/apexload/pkg/storage"
)

const archiveSourceReceiptName = ".apexload-published-source.json"

// Source retirement is opt-in while older controllers without the source-file
// fence may share a staging volume. Enable only after every writer is upgraded.
func archiveSourceRetentionTTL() time.Duration {
	ttl, err := time.ParseDuration(strings.TrimSpace(os.Getenv("APEXLOAD_ARCHIVE_SOURCE_RETENTION_TTL")))
	if err != nil || ttl < time.Minute || ttl > 30*24*time.Hour {
		return 0
	}
	return ttl
}

type publishedArchiveSourceReceipt struct {
	Version        int       `json:"version"`
	ConfirmedAt    time.Time `json:"confirmed_at"`
	TestID         string    `json:"test_id"`
	Backend        string    `json:"backend"`
	ArchiveURI     string    `json:"archive_uri"`
	ManifestURI    string    `json:"manifest_uri"`
	ManifestSHA256 string    `json:"manifest_sha256"`
}

// Keep these lock inodes after retiring data. Replacing/unlinking a lock while a
// waiter holds its descriptor would create two independent ownership domains.
func lockArchivePublicationSource(ctx context.Context, testID string) (func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateArchiveRunID(testID); err != nil {
		return nil, err
	}
	return lockArchiveNamespace(ctx, "_source_locks", sha256BytesHex([]byte(testID))+".lock")
}

// Separate namespaces prevent capacity locks from colliding with run locks.
func lockArchiveNamespace(ctx context.Context, namespace, name string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rootPath, err := resolveArchiveDirectoryRoot(resolveArchiveRoot(), true)
	if err != nil {
		return nil, err
	}
	return lockArchiveNamespaceAt(ctx, rootPath, namespace, name)
}

func lockArchiveNamespaceAt(ctx context.Context, rootPath, namespace, name string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := openArchiveDirectoryNoSymlink(rootPath)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	locks, err := openArchiveChildDirectoryAt(root, namespace, true, 0o700)
	if err != nil {
		return nil, err
	}
	defer locks.Close()
	var expected os.FileInfo
	file, err := locks.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if os.IsExist(err) {
		info, statErr := locks.Lstat(name)
		if statErr != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("archive source lock is not a regular file")
		}
		expected = info
		file, err = locks.OpenFile(name, os.O_RDWR, 0)
	}
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	after, afterErr := locks.Lstat(name)
	if err != nil || afterErr != nil || !after.Mode().IsRegular() || !os.SameFile(info, after) || (expected != nil && !os.SameFile(expected, info)) {
		_ = file.Close()
		return nil, fmt.Errorf("archive source lock changed during open")
	}
	unlock, err := filelock.LockContext(ctx, file)
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	after, err = locks.Lstat(name)
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(info, after) {
		_ = unlock()
		_ = file.Close()
		return nil, fmt.Errorf("archive source lock changed while waiting")
	}
	return func() { _ = unlock(); _ = file.Close() }, nil
}

// Only controller-collected and token-private publication sources are eligible.
// Canonical filesystem archives and downloaded reader caches are never sources.
func managedArchiveSourceRelative(record *storage.RunArchiveRecord) (string, error) {
	if record == nil || archiveRecordBackend(record) == archiveBackendFilesystem {
		return "", fmt.Errorf("object-backed archive source required")
	}
	if err := validateArchiveRunID(record.TestID); err != nil {
		return "", err
	}
	base, err := resolveArchiveDirectoryRoot(resolveArchiveRoot(), false)
	if err != nil {
		return "", err
	}
	path, err := resolveArchiveDirectoryRoot(record.ArchivePath, false)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return "", err
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) == 2 && parts[0] == "remote" && parts[1] == record.TestID {
		return rel, nil
	}
	if len(parts) == 3 && parts[0] == "_publication" && parts[2] == record.TestID && validateArchiveIntentToken(parts[1]) == nil {
		return rel, nil
	}
	return "", fmt.Errorf("archive path is not a managed publication source")
}

// Called only after object upload and the durable catalog CAS both succeeded,
// while the source fence remains held. The marker is local cleanup permission;
// it does not change catalog or object-store retention.
func markPublishedArchiveSource(record *storage.RunArchiveRecord) error {
	if archiveSourceRetentionTTL() == 0 {
		return nil
	}
	if _, err := managedArchiveSourceRelative(record); err != nil {
		return nil
	}
	if err := validatePublishedArchiveSourceRecord(record); err != nil {
		return err
	}
	receipt := publishedArchiveSourceReceipt{Version: 1, ConfirmedAt: time.Now().UTC(), TestID: record.TestID, Backend: record.ArchiveBackend, ArchiveURI: record.ArchiveURI, ManifestURI: record.ManifestURI, ManifestSHA256: record.ManifestSHA256}
	data, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	file, err := replaceArchiveArtifactForWrite(record.ArchivePath, archiveSourceReceiptName, 0o600, false)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	syncErr := file.Sync()
	closeErr := file.Close()
	return errors.Join(syncErr, closeErr, syncArchiveDirectory(record.ArchivePath))
}

func validatePublishedArchiveSourceRecord(record *storage.RunArchiveRecord) error {
	if _, err := archiveMaterializationRuntime(currentArchiveBackend(), record); err != nil {
		return err
	}
	hash, err := hex.DecodeString(record.ManifestSHA256)
	if err != nil || len(hash) != 32 {
		return fmt.Errorf("published source requires a manifest trust anchor")
	}
	parts := strings.Split(record.ArchiveURI, "/")
	n := len(parts)
	if n < 4 || parts[n-4] != archiveVersionSegment || parts[n-3] != archiveVersionSchema || validateArchiveIntentToken(parts[n-2]) != nil || parts[n-1] != record.TestID {
		return fmt.Errorf("published source requires an immutable object version")
	}
	return nil
}

// No remote objects, catalog rows, or unconfirmed producer directories are
// removed. Publication and collection hold the same cross-process file fence.
func (tr *TestRunner) retirePublishedArchiveSource(ctx context.Context, candidate orphanArchiveCandidate, now time.Time, ttl time.Duration) error {
	if archiveSourceRetentionTTL() == 0 || ttl <= 0 || currentArchiveBackend().Backend == archiveBackendFilesystem {
		return nil
	}
	testID := filepath.Base(candidate.path)
	release, err := lockArchivePublicationSource(ctx, testID)
	if err != nil {
		return err
	}
	defer release()
	outbox := tr.archiveFinalizationOutbox()
	if outbox == nil {
		return nil
	}
	pending, err := outbox.GetArchiveFinalizationJob(testID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if pending != nil {
		return nil
	}
	rootPath, err := resolveArchiveDirectoryRoot(resolveArchiveRoot(), false)
	if err != nil {
		return err
	}
	root, err := openArchiveDirectoryNoSymlink(rootPath)
	if err != nil {
		return err
	}
	defer root.Close()
	relative, err := filepath.Rel(rootPath, candidate.path)
	if err != nil {
		return err
	}
	parent, name, closeParent, err := openArchiveRelativeParent(root, relative)
	if err != nil {
		return err
	}
	defer closeParent()
	source, err := openArchiveChildDirectoryAt(parent, name, false, 0)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer source.Close()
	identity, err := source.Stat(".")
	if err != nil {
		return err
	}
	if !os.SameFile(identity, candidate.identity) {
		return fmt.Errorf("published source identity changed")
	}
	data, _, err := readArchiveFileAtRootLimitedContext(ctx, source, archiveSourceReceiptName, 8<<10)
	if os.IsNotExist(err) {
		return tr.recoverPublishedArchiveSourceReceipt(ctx, candidate.path, source)
	}
	if err != nil {
		return err
	}
	var receipt publishedArchiveSourceReceipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		return err
	}
	if receipt.Version != 1 || receipt.TestID != testID || receipt.ConfirmedAt.IsZero() || receipt.ConfirmedAt.After(now) || len(receipt.ManifestSHA256) != 64 {
		return fmt.Errorf("invalid published source receipt")
	}
	if receipt.ConfirmedAt.Add(ttl).After(now) {
		return nil
	}
	record := &storage.RunArchiveRecord{TestID: testID, ArchiveBackend: receipt.Backend, ArchiveURI: receipt.ArchiveURI, ManifestURI: receipt.ManifestURI, ManifestSHA256: receipt.ManifestSHA256, ArchivePath: candidate.path, ManifestPath: filepath.Join(candidate.path, runArchiveManifestFileName)}
	if _, err := managedArchiveSourceRelative(record); err != nil {
		return err
	}
	if err := validatePublishedArchiveSourceRecord(record); err != nil {
		return err
	}
	manifest, _, err := readArchiveFileAtRootLimitedContext(ctx, source, runArchiveManifestFileName, maxArchiveManifestDownloadBytes)
	if err != nil {
		return err
	}
	if sha256BytesHex(manifest) != receipt.ManifestSHA256 {
		return fmt.Errorf("published source changed after confirmation")
	}
	if _, _, err := decodeMaterializationManifest(record, manifest); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// The receipt proves prior remote/catalog confirmation even if a newer
	// publication now owns the catalog. A pending publication above defers cleanup.
	return removeArchiveDirectoryAt(root, relative, identity)
}

// Recover the crash gap between catalog commit and local receipt creation. A
// legacy source is eligible only when the current catalog names this exact path
// and immutable manifest, and the remote commit marker independently matches.
// The next sweep observes the full TTL after this confirmation.
func (tr *TestRunner) recoverPublishedArchiveSourceReceipt(ctx context.Context, path string, source *os.Root) error {
	if tr.archiveStore == nil {
		return nil
	}
	record, err := tr.archiveStore.GetRunArchive(filepath.Base(path))
	if errors.Is(err, sql.ErrNoRows) || (err == nil && record == nil) {
		return nil
	}
	if err != nil {
		return err
	}
	recordedPath, pathErr := resolveArchiveDirectoryRoot(record.ArchivePath, false)
	if pathErr != nil || recordedPath != path {
		return nil
	}
	if _, err := managedArchiveSourceRelative(record); err != nil {
		return nil
	}
	if err := validatePublishedArchiveSourceRecord(record); err != nil {
		return err
	}
	manifest, _, err := readArchiveFileAtRootLimitedContext(ctx, source, runArchiveManifestFileName, maxArchiveManifestDownloadBytes)
	if err != nil {
		return err
	}
	if sha256BytesHex(manifest) != record.ManifestSHA256 {
		return fmt.Errorf("unconfirmed source differs from catalog")
	}
	if _, _, err := decodeMaterializationManifest(record, manifest); err != nil {
		return err
	}
	backend, err := archiveMaterializationRuntime(currentArchiveBackend(), record)
	if err != nil {
		return err
	}
	bucket, err := backend.openBucket(ctx)
	if err != nil {
		return err
	}
	defer bucket.Close()
	remote, err := readArchiveObjectLimited(ctx, bucket, backend.objectKey(record.TestID, runArchiveManifestFileName), maxArchiveManifestDownloadBytes)
	if err != nil {
		return err
	}
	if sha256BytesHex(remote) != record.ManifestSHA256 {
		return fmt.Errorf("remote publication commit marker differs from catalog")
	}
	return markPublishedArchiveSource(record)
}

// Traversal counts every directory entry, not just matching candidates. This
// bounds work even when a staging directory contains many unfinished producers.
func discoverPublishedArchiveSources(ctx context.Context) ([]orphanArchiveCandidate, error) {
	rootPath, err := resolveArchiveDirectoryRoot(resolveArchiveRoot(), false)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	root, err := openArchiveDirectoryNoSymlink(rootPath)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	var candidates []orphanArchiveCandidate
	remaining := 10_000
	var walk func(*os.Root, string, int) error
	walk = func(parent *os.Root, relative string, depth int) error {
		directory, err := parent.Open(".")
		if err != nil {
			return err
		}
		defer directory.Close()
		for {
			entries, readErr := directory.ReadDir(256)
			for _, entry := range entries {
				if err := ctx.Err(); err != nil {
					return err
				}
				remaining--
				if remaining < 0 {
					return fmt.Errorf("published source scan entry limit exceeded")
				}
				if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
					continue
				}
				if depth == 2 {
					if validateArchiveIntentToken(entry.Name()) != nil {
						continue
					}
				} else if validateArchiveRunID(entry.Name()) != nil {
					continue
				}
				child, err := openArchiveChildDirectoryAt(parent, entry.Name(), false, 0)
				if err != nil {
					continue
				}
				next := filepath.Join(relative, entry.Name())
				if depth == 1 {
					info, statErr := child.Stat(".")
					if statErr == nil {
						candidates = append(candidates, orphanArchiveCandidate{path: filepath.Join(rootPath, next), identity: info})
					}
				} else {
					err = walk(child, next, depth-1)
				}
				_ = child.Close()
				if err != nil {
					return err
				}
			}
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			if readErr != nil {
				return readErr
			}
		}
	}
	for _, parent := range []struct {
		name  string
		depth int
	}{{"remote", 1}, {"_publication", 2}} {
		child, err := openArchiveChildDirectoryAt(root, parent.name, false, 0)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return candidates, err
		}
		err = walk(child, parent.name, parent.depth)
		_ = child.Close()
		if err != nil {
			return candidates, err
		}
	}
	return candidates, nil
}

func (tr *TestRunner) retirePublishedArchiveSources(ctx context.Context) error {
	ttl := archiveSourceRetentionTTL()
	if ttl == 0 || tr == nil || tr.archiveFinalizationOutbox() == nil || currentArchiveBackend().Backend == archiveBackendFilesystem {
		return nil
	}
	candidates, scanErr := discoverPublishedArchiveSources(ctx)
	failures := []error{scanErr}
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		attempt, cancel := context.WithTimeout(ctx, time.Second)
		err := tr.retirePublishedArchiveSource(attempt, candidate, time.Now().UTC(), ttl)
		cancel()
		if err != nil && !errors.Is(err, context.DeadlineExceeded) {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
