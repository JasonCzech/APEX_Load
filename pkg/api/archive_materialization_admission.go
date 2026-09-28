package api

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/storage"
)

const envArchiveActiveReaderMaxBytes = "APEXLOAD_ARCHIVE_ACTIVE_READER_MAX_BYTES"

// Admission reserves the per-archive download bound for each distinct cache
// directory until its last reader/waiter releases it. Same-directory readers
// share one reservation. This deliberately conservative accounting also covers
// legacy manifests with no trustworthy declared artifact sizes.
func (m *archiveMaterializationCoordinator) admitActiveBytes(ctx context.Context, entry *archiveMaterializationEntry, bytes, limit int64) error {
	if bytes <= 0 || limit <= 0 || bytes > limit {
		return fmt.Errorf("archive materialization bound %d exceeds active reader budget %d", bytes, limit)
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		m.mu.Lock()
		if entry.budgetBytes > 0 {
			same := entry.budgetBytes == bytes
			m.mu.Unlock()
			if !same {
				return fmt.Errorf("active archive admission configuration changed")
			}
			return nil
		}
		used := m.activeBytes
		if used <= limit-bytes {
			m.activeBytes = used + bytes
			entry.budgetBytes = bytes
			m.mu.Unlock()
			return nil
		}
		changed := m.budgetChanged
		m.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}

// Caller holds m.mu and has established no remaining readers or waiters.
func (m *archiveMaterializationCoordinator) releaseActiveBytesLocked(entry *archiveMaterializationEntry) {
	if entry.budgetBytes <= 0 {
		return
	}
	m.activeBytes -= entry.budgetBytes
	entry.budgetBytes = 0
	close(m.budgetChanged)
	m.budgetChanged = make(chan struct{})
}

func configuredArchiveActiveReaderMaxBytes() int64 {
	value, err := strconv.ParseInt(strings.TrimSpace(os.Getenv(envArchiveActiveReaderMaxBytes)), 10, 64)
	if err != nil || value <= 0 {
		return defaultArchiveMaterializationCacheBytes
	}
	return value
}

// Cached archives may predate the current per-archive limit. Check their actual
// payload footprint before hashing or granting a reader lease. Producer/source
// validation is intentionally independent of reader admission settings.
func validateAdmittedArchiveCache(ctx context.Context, record *storage.RunArchiveRecord, root, manifestPath string, maximumBytes int64) error {
	data, err := readLocalFileLimitedContext(ctx, manifestPath, maxArchiveManifestDownloadBytes)
	if err != nil {
		return err
	}
	manifest, names, err := decodeMaterializationManifest(record, data)
	if err != nil {
		return err
	}
	total := int64(len(data))
	if total > maximumBytes {
		return fmt.Errorf("cached archive exceeds admitted byte limit")
	}
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return err
		}
		if name == runArchiveManifestFileName {
			continue
		}
		file, err := archiveArtifactPath(root, name)
		if err != nil {
			return err
		}
		info, err := os.Lstat(filepath.Clean(file))
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > maximumBytes-total {
			return fmt.Errorf("cached artifact %s exceeds admitted byte limit or is not regular", name)
		}
		total += info.Size()
	}
	return validateMaterializedArchiveWithManifestContext(ctx, record, root, manifestPath, manifest)
}
