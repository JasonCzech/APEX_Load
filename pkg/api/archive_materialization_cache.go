package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	archiveMaterializationCacheMarkerSuffix      = ".apexload-materialized-cache.json"
	archiveMaterializationCacheMarkerVersion     = 1
	archiveMaterializationRetentionSweepInterval = time.Minute
)

// archiveMaterializationCoordinator serializes downloads per concrete cache
// directory while allowing unrelated archives to materialize concurrently. A
// pending count protects callers waiting for the keyed gate; readers remain
// protected until their lease is released.
type archiveMaterializationCoordinator struct {
	mu                     sync.Mutex
	entries                map[string]*archiveMaterializationEntry
	evictionMu             sync.Mutex
	now                    func() time.Time
	lastRetentionSweep     map[string]time.Time
	retentionSweepInterval time.Duration
}

type archiveMaterializationEntry struct {
	key     string
	gate    chan struct{}
	changed chan struct{}
	pending int
	readers int
}

type archiveMaterializationLease struct {
	coordinator *archiveMaterializationCoordinator
	key         string
	scanRoot    string
	cacheRoot   string
	once        sync.Once
}

type archiveMaterializationCacheMarker struct {
	Version   int   `json:"version"`
	SizeBytes int64 `json:"size_bytes"`
}

type archiveMaterializationCacheCandidate struct {
	key        string
	root       string
	markerPath string
	sizeBytes  int64
	lastAccess time.Time
	expired    bool
}

var archiveMaterializations = newArchiveMaterializationCoordinator()

func newArchiveMaterializationCoordinator() *archiveMaterializationCoordinator {
	return &archiveMaterializationCoordinator{
		entries:                make(map[string]*archiveMaterializationEntry),
		now:                    time.Now,
		lastRetentionSweep:     make(map[string]time.Time),
		retentionSweepInterval: archiveMaterializationRetentionSweepInterval,
	}
}

func newArchiveMaterializationEntry(key string) *archiveMaterializationEntry {
	entry := &archiveMaterializationEntry{
		key:     key,
		gate:    make(chan struct{}, 1),
		changed: make(chan struct{}),
	}
	entry.gate <- struct{}{}
	return entry
}

func (m *archiveMaterializationCoordinator) reserve(ctx context.Context, key string) (*archiveMaterializationEntry, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.Lock()
	entry := m.entries[key]
	if entry == nil {
		entry = newArchiveMaterializationEntry(key)
		m.entries[key] = entry
	}
	entry.pending++
	m.mu.Unlock()

	if err := ctx.Err(); err != nil {
		m.mu.Lock()
		entry.pending--
		m.mu.Unlock()
		m.forgetEntryIfIdle(entry)
		return nil, err
	}
	select {
	case <-ctx.Done():
		m.mu.Lock()
		entry.pending--
		m.mu.Unlock()
		m.forgetEntryIfIdle(entry)
		return nil, ctx.Err()
	case <-entry.gate:
		if err := ctx.Err(); err != nil {
			m.mu.Lock()
			entry.pending--
			m.mu.Unlock()
			entry.gate <- struct{}{}
			m.forgetEntryIfIdle(entry)
			return nil, err
		}
		return entry, nil
	}
}

func (m *archiveMaterializationCoordinator) failReservation(entry *archiveMaterializationEntry) {
	if entry == nil {
		return
	}
	m.mu.Lock()
	entry.pending--
	m.mu.Unlock()
	entry.gate <- struct{}{}
	m.forgetEntryIfIdle(entry)
}

func (m *archiveMaterializationCoordinator) promoteReservation(key, scanRoot, cacheRoot string, entry *archiveMaterializationEntry) *archiveMaterializationLease {
	m.mu.Lock()
	entry.pending--
	entry.readers++
	m.mu.Unlock()
	entry.gate <- struct{}{}
	return &archiveMaterializationLease{
		coordinator: m,
		key:         key,
		scanRoot:    scanRoot,
		cacheRoot:   cacheRoot,
	}
}

func (m *archiveMaterializationCoordinator) waitForNoReaders(ctx context.Context, entry *archiveMaterializationEntry) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		m.mu.Lock()
		if entry.readers == 0 {
			m.mu.Unlock()
			return nil
		}
		changed := entry.changed
		m.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}

func (m *archiveMaterializationCoordinator) notifyEntryLocked(entry *archiveMaterializationEntry) {
	close(entry.changed)
	entry.changed = make(chan struct{})
}

// forgetEntryIfIdle bounds coordinator bookkeeping independently of on-disk
// retention. Taking the keyed gate before the final map check preserves gate
// identity: a waiter that already reserved the entry increments pending and
// prevents deletion; a later reservation safely creates a fresh entry.
func (m *archiveMaterializationCoordinator) forgetEntryIfIdle(entry *archiveMaterializationEntry) {
	if entry == nil {
		return
	}
	select {
	case <-entry.gate:
		defer func() { entry.gate <- struct{}{} }()
	default:
		return
	}
	m.mu.Lock()
	if m.entries[entry.key] == entry && entry.pending == 0 && entry.readers == 0 {
		delete(m.entries, entry.key)
	}
	m.mu.Unlock()
}

func (l *archiveMaterializationLease) Release() {
	if l == nil || l.coordinator == nil {
		return
	}
	l.once.Do(func() {
		m := l.coordinator
		// Refresh recency while the reader is still protected so an eviction
		// sweep cannot race the touch and discard a just-used cache entry.
		touchArchiveMaterializationCacheMarker(l.cacheRoot, m.now().UTC())
		m.mu.Lock()
		entry := m.entries[l.key]
		if entry != nil && entry.readers > 0 {
			entry.readers--
			m.notifyEntryLocked(entry)
		}
		m.mu.Unlock()
		m.forgetEntryIfIdle(entry)
		m.enforceRetentionIfDue(l.scanRoot)
	})
}

func noOpArchiveMaterializationLease() *archiveMaterializationLease {
	return &archiveMaterializationLease{}
}

func canonicalArchiveMaterializationKey(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

func sha256FileContext(ctx context.Context, path string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	buffer := make([]byte, 128*1024)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		read, readErr := file.Read(buffer)
		if read > 0 {
			if _, err := hash.Write(buffer[:read]); err != nil {
				return "", err
			}
		}
		if readErr == io.EOF {
			return hex.EncodeToString(hash.Sum(nil)), nil
		}
		if readErr != nil {
			return "", readErr
		}
	}
}

type archiveContextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r archiveContextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(buffer)
}

func readLocalFileLimitedContext(ctx context.Context, path string, limit int64) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("archive file %s is not a regular file", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(archiveContextReader{ctx: ctx, r: file}, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("archive file %s exceeds %d bytes", path, limit)
	}
	return data, nil
}

func archiveMaterializationCacheMarkerPath(cacheRoot string) string {
	return filepath.Join(cacheRoot, archiveMaterializationCacheMarkerSuffix)
}

func archiveMaterializationCacheScanRoot(_ string) string {
	if root := strings.TrimSpace(os.Getenv("APEXLOAD_ARCHIVE_STAGING_DIR")); root != "" {
		return filepath.Clean(root)
	}
	return filepath.Join(resolveFilesystemArchiveRoot(), "_object_staging")
}

func writeArchiveMaterializationCacheMarker(cacheRoot string, sizeBytes int64, accessedAt time.Time) error {
	if sizeBytes < 0 {
		return fmt.Errorf("archive materialization cache size cannot be negative")
	}
	markerPath := archiveMaterializationCacheMarkerPath(cacheRoot)
	data, err := json.Marshal(archiveMaterializationCacheMarker{
		Version:   archiveMaterializationCacheMarkerVersion,
		SizeBytes: sizeBytes,
	})
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(markerPath), ".archive-cache-marker-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	keep := false
	defer func() {
		_ = temp.Close()
		if !keep {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := temp.Write(data); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, markerPath); err != nil {
		return err
	}
	keep = true
	touchArchiveMaterializationCacheMarker(cacheRoot, accessedAt)
	_ = syncArchiveDirectory(filepath.Dir(markerPath))
	return nil
}

func touchArchiveMaterializationCacheMarker(cacheRoot string, accessedAt time.Time) {
	if strings.TrimSpace(cacheRoot) == "" {
		return
	}
	markerPath := archiveMaterializationCacheMarkerPath(cacheRoot)
	_ = os.Chtimes(markerPath, accessedAt, accessedAt)
}

func removeArchiveMaterializationCacheMarker(cacheRoot string) {
	if strings.TrimSpace(cacheRoot) == "" {
		return
	}
	_ = os.Remove(archiveMaterializationCacheMarkerPath(cacheRoot))
}

func (m *archiveMaterializationCoordinator) enforceRetention(scanRoot string) {
	if strings.TrimSpace(scanRoot) == "" {
		return
	}
	m.evictionMu.Lock()
	defer m.evictionMu.Unlock()

	cleanupArchiveMaterializationOrphans(scanRoot, m.now().UTC())
	candidates := scanArchiveMaterializationCache(scanRoot, m.now().UTC(), configuredArchiveMaterializationCacheTTL())
	if len(candidates) == 0 {
		return
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].lastAccess.Equal(candidates[j].lastAccess) {
			return candidates[i].root < candidates[j].root
		}
		return candidates[i].lastAccess.Before(candidates[j].lastAccess)
	})
	totalBytes := int64(0)
	for _, candidate := range candidates {
		if candidate.sizeBytes > int64(^uint64(0)>>1)-totalBytes {
			totalBytes = int64(^uint64(0) >> 1)
		} else {
			totalBytes += candidate.sizeBytes
		}
	}
	maxBytes := configuredArchiveMaterializationCacheMaxBytes()
	for _, candidate := range candidates {
		if !candidate.expired && totalBytes <= maxBytes {
			continue
		}
		if !m.tryEvict(candidate) {
			continue
		}
		totalBytes -= candidate.sizeBytes
	}
}

func cleanupArchiveMaterializationOrphans(scanRoot string, now time.Time) {
	maxAge := configuredArchiveMaterializationTimeout() + 5*time.Minute
	_ = filepath.WalkDir(scanRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry == nil || path == scanRoot || !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		name := entry.Name()
		orphan := strings.Contains(name, ".materializing-") || strings.Contains(name, ".previous-") || strings.Contains(name, ".evicting-")
		if !strings.HasPrefix(name, ".") || !orphan {
			return nil
		}
		info, err := entry.Info()
		if err == nil && !info.ModTime().Add(maxAge).After(now) {
			_ = os.RemoveAll(path)
		}
		return filepath.SkipDir
	})
}

func (m *archiveMaterializationCoordinator) enforceRetentionIfDue(scanRoot string) {
	if strings.TrimSpace(scanRoot) == "" {
		return
	}
	now := m.now().UTC()
	m.mu.Lock()
	last := m.lastRetentionSweep[scanRoot]
	if !last.IsZero() && now.Sub(last) < m.retentionSweepInterval {
		m.mu.Unlock()
		return
	}
	// Mark before scanning so concurrent releases coalesce behind this sweep
	// instead of queuing serialized O(cache-entry) walks on evictionMu.
	m.lastRetentionSweep[scanRoot] = now
	m.mu.Unlock()
	m.enforceRetention(scanRoot)
}

func (m *archiveMaterializationCoordinator) tryEvict(candidate archiveMaterializationCacheCandidate) bool {
	m.mu.Lock()
	entry := m.entries[candidate.key]
	if entry == nil {
		entry = newArchiveMaterializationEntry(candidate.key)
		m.entries[candidate.key] = entry
	}
	if entry.pending > 0 || entry.readers > 0 {
		m.mu.Unlock()
		return false
	}
	m.mu.Unlock()

	select {
	case <-entry.gate:
		// A reservation may have arrived between the first check and taking the
		// keyed gate. Re-check while the gate prevents its materialization.
	default:
		return false
	}
	defer func() {
		entry.gate <- struct{}{}
		m.forgetEntryIfIdle(entry)
	}()
	m.mu.Lock()
	protected := entry.pending > 0 || entry.readers > 0
	m.mu.Unlock()
	if protected {
		return false
	}
	placeholder, err := os.MkdirTemp(filepath.Dir(candidate.root), "."+filepath.Base(candidate.root)+".evicting-*")
	if err != nil {
		return false
	}
	if err := os.Remove(placeholder); err != nil {
		return false
	}
	tombstone := placeholder
	if err := os.Rename(candidate.root, tombstone); err != nil {
		return false
	}
	if err := os.RemoveAll(candidate.root); err != nil {
		_ = os.Rename(tombstone, candidate.root)
		return false
	}
	_ = os.RemoveAll(tombstone)
	_ = syncArchiveDirectory(filepath.Dir(candidate.root))
	return true
}

func scanArchiveMaterializationCache(scanRoot string, now time.Time, ttl time.Duration) []archiveMaterializationCacheCandidate {
	candidates := make([]archiveMaterializationCacheCandidate, 0)
	_ = filepath.WalkDir(scanRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry == nil || entry.IsDir() {
			return nil
		}
		if entry.Name() != archiveMaterializationCacheMarkerSuffix || entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		root := filepath.Dir(path)
		base := filepath.Base(root)
		if err := validateArchiveRunID(base); err != nil {
			return nil
		}
		rootInfo, err := os.Lstat(root)
		if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		markerInfo, err := entry.Info()
		if err != nil {
			return nil
		}
		if !markerInfo.Mode().IsRegular() {
			return nil
		}
		data, err := readLocalFileLimitedContext(context.Background(), path, 4<<10)
		if err != nil {
			return nil
		}
		var marker archiveMaterializationCacheMarker
		if err := json.Unmarshal(data, &marker); err != nil || marker.Version != archiveMaterializationCacheMarkerVersion || marker.SizeBytes < 0 {
			return nil
		}
		key, err := canonicalArchiveMaterializationKey(root)
		if err != nil {
			return nil
		}
		lastAccess := markerInfo.ModTime().UTC()
		candidates = append(candidates, archiveMaterializationCacheCandidate{
			key:        key,
			root:       root,
			markerPath: path,
			sizeBytes:  marker.SizeBytes,
			lastAccess: lastAccess,
			expired:    ttl > 0 && !lastAccess.Add(ttl).After(now),
		})
		return nil
	})
	return candidates
}

func configuredArchiveMaterializationCacheMaxBytes() int64 {
	raw := strings.TrimSpace(os.Getenv(envArchiveMaterializationCacheMaxBytes))
	if raw == "" {
		return defaultArchiveMaterializationCacheBytes
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return defaultArchiveMaterializationCacheBytes
	}
	return value
}

func configuredArchiveMaterializationCacheTTL() time.Duration {
	raw := strings.TrimSpace(os.Getenv(envArchiveMaterializationCacheTTL))
	if raw == "" {
		return defaultArchiveMaterializationCacheTTL
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		return defaultArchiveMaterializationCacheTTL
	}
	return value
}
