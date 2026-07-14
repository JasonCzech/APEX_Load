package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
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
	retentionOnce          sync.Once
	retentionWake          chan string
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
	cacheID     os.FileInfo
	once        sync.Once
}

type archiveMaterializationCacheMarker struct {
	Version    int       `json:"version"`
	SizeBytes  int64     `json:"size_bytes"`
	AccessedAt time.Time `json:"accessed_at,omitempty"`
}

type archiveMaterializationCacheCandidate struct {
	key          string
	root         string
	markerPath   string
	scanRoot     string
	relativeRoot string
	scanIdentity os.FileInfo
	rootIdentity os.FileInfo
	sizeBytes    int64
	lastAccess   time.Time
	expired      bool
}

var archiveMaterializations = newArchiveMaterializationCoordinator()

func newArchiveMaterializationCoordinator() *archiveMaterializationCoordinator {
	return &archiveMaterializationCoordinator{
		entries:                make(map[string]*archiveMaterializationEntry),
		now:                    time.Now,
		lastRetentionSweep:     make(map[string]time.Time),
		retentionSweepInterval: archiveMaterializationRetentionSweepInterval,
		retentionWake:          make(chan string, 16),
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
	var cacheID os.FileInfo
	if strings.TrimSpace(cacheRoot) != "" {
		if root, err := openArchiveDirectoryNoSymlink(cacheRoot); err == nil {
			cacheID, _ = root.Stat(".")
			_ = root.Close()
		}
	}
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
		cacheID:     cacheID,
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
		touchArchiveMaterializationCacheMarkerExpected(l.cacheRoot, l.cacheID, m.now().UTC())
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
	file, _, err := openLocalArchiveRegularFile(path)
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
	if limit < 0 {
		return nil, fmt.Errorf("archive file limit cannot be negative")
	}
	file, info, err := openLocalArchiveRegularFile(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if info.Size() > limit {
		return nil, fmt.Errorf("archive file %s exceeds %d bytes", path, limit)
	}
	data, err := io.ReadAll(io.LimitReader(archiveContextReader{ctx: ctx, r: file}, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("archive file %s exceeds %d bytes", path, limit)
	}
	return data, nil
}

// openLocalArchiveRegularFile converts a path-based call site into a verified
// descriptor read. The parent directory and final inode are checked before and
// after opening, so a final symlink or rename swap is rejected rather than
// followed by subsequent reads or hashes.
func openLocalArchiveRegularFile(path string) (*os.File, os.FileInfo, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil, fmt.Errorf("archive file path is empty")
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, nil, err
	}
	parent := filepath.Dir(absPath)
	root, err := openArchiveDirectoryNoSymlink(parent)
	if err != nil {
		return nil, nil, err
	}
	defer root.Close()
	return openArchiveArtifactForReadAt(root, filepath.Base(absPath))
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
	root, err := openArchiveDirectoryNoSymlink(cacheRoot)
	if err != nil {
		return err
	}
	defer root.Close()
	return writeArchiveMaterializationCacheMarkerAt(root, sizeBytes, accessedAt)
}

func writeArchiveMaterializationCacheMarkerAt(root *os.Root, sizeBytes int64, accessedAt time.Time) error {
	if root == nil {
		return fmt.Errorf("archive materialization cache root is required")
	}
	if sizeBytes < 0 {
		return fmt.Errorf("archive materialization cache size cannot be negative")
	}
	data, err := json.Marshal(archiveMaterializationCacheMarker{
		Version:    archiveMaterializationCacheMarkerVersion,
		SizeBytes:  sizeBytes,
		AccessedAt: accessedAt.UTC(),
	})
	if err != nil {
		return err
	}
	tempName, err := archiveTemporaryName(".", "archive-cache-marker")
	if err != nil {
		return err
	}
	temp, err := root.OpenFile(tempName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	keepTemp := true
	defer func() {
		_ = temp.Close()
		if keepTemp {
			_ = root.Remove(tempName)
		}
	}()
	if _, err := temp.Write(data); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := root.Rename(tempName, archiveMaterializationCacheMarkerSuffix); err != nil {
		return err
	}
	keepTemp = false
	return syncOpenedArchiveDirectory(root)
}

func touchArchiveMaterializationCacheMarker(cacheRoot string, accessedAt time.Time) {
	touchArchiveMaterializationCacheMarkerExpected(cacheRoot, nil, accessedAt)
}

func touchArchiveMaterializationCacheMarkerExpected(cacheRoot string, expected os.FileInfo, accessedAt time.Time) {
	if strings.TrimSpace(cacheRoot) == "" {
		return
	}
	root, err := openArchiveDirectoryNoSymlink(cacheRoot)
	if err != nil {
		return
	}
	defer root.Close()
	if expected != nil {
		actual, err := root.Stat(".")
		if err != nil || !os.SameFile(expected, actual) {
			return
		}
	}
	data, _, err := readArchiveFileAtRootLimitedContext(context.Background(), root, archiveMaterializationCacheMarkerSuffix, 4<<10)
	if err != nil {
		return
	}
	var marker archiveMaterializationCacheMarker
	if err := json.Unmarshal(data, &marker); err != nil || marker.Version != archiveMaterializationCacheMarkerVersion || marker.SizeBytes < 0 {
		return
	}
	_ = writeArchiveMaterializationCacheMarkerAt(root, marker.SizeBytes, accessedAt)
}

func removeArchiveMaterializationCacheMarker(cacheRoot string) {
	if strings.TrimSpace(cacheRoot) == "" {
		return
	}
	root, err := openArchiveDirectoryNoSymlink(cacheRoot)
	if err != nil {
		return
	}
	defer root.Close()
	_ = root.Remove(archiveMaterializationCacheMarkerSuffix)
}

func readArchiveFileAtRootLimitedContext(ctx context.Context, root *os.Root, name string, limit int64) ([]byte, os.FileInfo, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if root == nil || limit < 0 {
		return nil, nil, fmt.Errorf("archive root and non-negative limit are required")
	}
	file, info, err := openArchiveArtifactForReadAt(root, name)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()
	if info.Size() > limit {
		return nil, nil, fmt.Errorf("archive file %s exceeds %d bytes", name, limit)
	}
	data, err := io.ReadAll(io.LimitReader(archiveContextReader{ctx: ctx, r: file}, limit+1))
	if err != nil {
		return nil, nil, err
	}
	if int64(len(data)) > limit {
		return nil, nil, fmt.Errorf("archive file %s exceeds %d bytes", name, limit)
	}
	return data, info, nil
}

func syncOpenedArchiveDirectory(root *os.Root) error {
	if root == nil {
		return fmt.Errorf("archive root is required")
	}
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
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
	root, err := openArchiveDirectoryNoSymlink(scanRoot)
	if err != nil {
		return
	}
	defer root.Close()
	_ = fs.WalkDir(root.FS(), ".", func(relative string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry == nil || relative == "." || !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		name := entry.Name()
		orphan := strings.Contains(name, ".materializing-") || strings.Contains(name, ".previous-") || strings.Contains(name, ".evicting-")
		if !strings.HasPrefix(name, ".") || !orphan {
			return nil
		}
		info, err := entry.Info()
		if err == nil && !info.ModTime().Add(maxAge).After(now) {
			_ = removeArchiveDirectoryAt(root, relative, info)
		}
		return filepath.SkipDir
	})
}

func removeArchiveDirectoryAt(root *os.Root, relative string, expected os.FileInfo) error {
	if root == nil || expected == nil {
		return fmt.Errorf("archive root and directory identity are required")
	}
	relative, err := normalizeArchiveRelativeDirectory(relative)
	if err != nil || relative == "." {
		if err != nil {
			return err
		}
		return fmt.Errorf("archive root cannot be removed")
	}
	parent, name, closeParent, err := openArchiveRelativeParent(root, relative)
	if err != nil {
		return err
	}
	defer closeParent()
	info, err := parent.Lstat(name)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || !os.SameFile(expected, info) {
		return fmt.Errorf("archive directory %q was replaced before deletion", relative)
	}
	tombstone, err := archiveTemporaryName(".", name+".evicting")
	if err != nil {
		return err
	}
	if err := parent.Rename(name, tombstone); err != nil {
		return err
	}
	tombstoneInfo, err := parent.Lstat(tombstone)
	if err != nil || tombstoneInfo.Mode()&os.ModeSymlink != 0 || !tombstoneInfo.IsDir() || !os.SameFile(expected, tombstoneInfo) {
		if _, currentErr := parent.Lstat(name); os.IsNotExist(currentErr) {
			_ = parent.Rename(tombstone, name)
		}
		if err != nil {
			return err
		}
		return fmt.Errorf("archive directory %q changed during deletion", relative)
	}
	if err := removeArchiveDirectoryWithIdentity(parent, tombstone, tombstoneInfo); err != nil {
		if _, currentErr := parent.Lstat(name); os.IsNotExist(currentErr) {
			_ = parent.Rename(tombstone, name)
		}
		return err
	}
	return syncOpenedArchiveDirectory(parent)
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
	m.retentionOnce.Do(func() {
		go func() {
			for root := range m.retentionWake {
				m.enforceRetention(root)
			}
		}()
	})
	select {
	case m.retentionWake <- scanRoot:
	default:
		// The bounded queue is saturated. Permit a later lease release to retry
		// instead of accumulating one goroutine per cache namespace.
		m.mu.Lock()
		delete(m.lastRetentionSweep, scanRoot)
		m.mu.Unlock()
	}
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
	root, err := openArchiveDirectoryNoSymlink(candidate.scanRoot)
	if err != nil {
		return false
	}
	defer root.Close()
	scanInfo, err := root.Stat(".")
	if err != nil || candidate.scanIdentity == nil || !os.SameFile(candidate.scanIdentity, scanInfo) {
		return false
	}
	if candidate.rootIdentity == nil {
		return false
	}
	if err := removeArchiveDirectoryAt(root, candidate.relativeRoot, candidate.rootIdentity); err != nil {
		return false
	}
	return true
}

func scanArchiveMaterializationCache(scanRoot string, now time.Time, ttl time.Duration) []archiveMaterializationCacheCandidate {
	candidates := make([]archiveMaterializationCacheCandidate, 0)
	root, err := openArchiveDirectoryNoSymlink(scanRoot)
	if err != nil {
		return candidates
	}
	defer root.Close()
	scanIdentity, err := root.Stat(".")
	if err != nil {
		return candidates
	}
	absScanRoot, err := filepath.Abs(scanRoot)
	if err != nil {
		return candidates
	}
	_ = fs.WalkDir(root.FS(), ".", func(relative string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry == nil || entry.IsDir() {
			return nil
		}
		if entry.Name() != archiveMaterializationCacheMarkerSuffix || entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		relativeRoot := filepath.Dir(relative)
		base := filepath.Base(relativeRoot)
		if err := validateArchiveRunID(base); err != nil {
			return nil
		}
		rootInfo, err := root.Lstat(relativeRoot)
		if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		data, markerInfo, err := readArchiveFileAtRootLimitedContext(context.Background(), root, relative, 4<<10)
		if err != nil {
			return nil
		}
		var marker archiveMaterializationCacheMarker
		if err := json.Unmarshal(data, &marker); err != nil || marker.Version != archiveMaterializationCacheMarkerVersion || marker.SizeBytes < 0 {
			return nil
		}
		candidateRoot := filepath.Join(absScanRoot, relativeRoot)
		key, err := canonicalArchiveMaterializationKey(candidateRoot)
		if err != nil {
			return nil
		}
		lastAccess := marker.AccessedAt.UTC()
		if lastAccess.IsZero() {
			lastAccess = markerInfo.ModTime().UTC()
		}
		candidates = append(candidates, archiveMaterializationCacheCandidate{
			key:          key,
			root:         candidateRoot,
			markerPath:   filepath.Join(candidateRoot, archiveMaterializationCacheMarkerSuffix),
			scanRoot:     absScanRoot,
			relativeRoot: relativeRoot,
			scanIdentity: scanIdentity,
			rootIdentity: rootInfo,
			sizeBytes:    marker.SizeBytes,
			lastAccess:   lastAccess,
			expired:      ttl > 0 && !lastAccess.Add(ttl).After(now),
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
