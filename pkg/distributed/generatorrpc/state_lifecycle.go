package generatorrpc

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/storage"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	envGeneratorScriptCacheMax  = "APEXLOAD_GENERATOR_SCRIPT_CACHE_MAX"
	envGeneratorScriptMaxBytes  = "APEXLOAD_GENERATOR_SCRIPT_CACHE_MAX_BYTES"
	envGeneratorArchiveMax      = "APEXLOAD_GENERATOR_ARCHIVE_MAX"
	envGeneratorArchiveTTL      = "APEXLOAD_GENERATOR_ARCHIVE_TTL"
	envGeneratorArchiveMaxBytes = "APEXLOAD_GENERATOR_ARCHIVE_MAX_BYTES"
	envGeneratorArchiveMinFree  = "APEXLOAD_GENERATOR_ARCHIVE_MIN_FREE_BYTES"

	defaultGeneratorScriptCacheMax  = 1024
	defaultGeneratorScriptMaxBytes  = 64 << 20
	defaultGeneratorArchiveMax      = 256
	defaultGeneratorArchiveTTL      = 24 * time.Hour
	defaultGeneratorArchiveMaxBytes = int64(256 << 30)
	defaultGeneratorArchiveMinFree  = int64(1 << 30)
)

// WithGeneratorStateLimits overrides generator-local cache retention. It is
// useful for small deployments and deterministic lifecycle tests; production
// deployments can use the corresponding APEXLOAD_GENERATOR_* variables.
func WithGeneratorStateLimits(scriptCacheMax, archiveMax int, archiveTTL time.Duration) ServiceOption {
	return func(service *Service) {
		if scriptCacheMax > 0 {
			service.scriptCacheMax = scriptCacheMax
		}
		if archiveMax > 0 {
			service.archiveRecordMax = archiveMax
		}
		if archiveTTL >= 0 {
			service.archiveTTL = archiveTTL
		}
	}
}

// WithGeneratorScriptCacheByteLimit applies a second, content-size bound in
// addition to the entry count cap.
func WithGeneratorScriptCacheByteLimit(maxBytes int64) ServiceOption {
	return func(service *Service) {
		if maxBytes > 0 {
			service.scriptCacheLimit = maxBytes
		}
	}
}

func configuredGeneratorScriptCacheMax() int {
	return positiveGeneratorIntEnv(envGeneratorScriptCacheMax, defaultGeneratorScriptCacheMax)
}

func configuredGeneratorArchiveMax() int {
	return positiveGeneratorIntEnv(envGeneratorArchiveMax, defaultGeneratorArchiveMax)
}

func configuredGeneratorScriptCacheMaxBytes() int64 {
	raw := strings.TrimSpace(os.Getenv(envGeneratorScriptMaxBytes))
	if raw == "" {
		return defaultGeneratorScriptMaxBytes
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return defaultGeneratorScriptMaxBytes
	}
	return value
}

func positiveGeneratorIntEnv(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func configuredGeneratorArchiveTTL() time.Duration {
	raw := strings.TrimSpace(os.Getenv(envGeneratorArchiveTTL))
	if raw == "" {
		return defaultGeneratorArchiveTTL
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value < 0 {
		return defaultGeneratorArchiveTTL
	}
	return value
}

func configuredGeneratorArchiveMaxBytes() int64 {
	return positiveGeneratorInt64Env(envGeneratorArchiveMaxBytes, defaultGeneratorArchiveMaxBytes)
}

func configuredGeneratorArchiveMinFreeBytes() int64 {
	return positiveGeneratorInt64Env(envGeneratorArchiveMinFree, defaultGeneratorArchiveMinFree)
}

func positiveGeneratorInt64Env(name string, fallback int64) int64 {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

// cacheScriptLocked inserts one script while keeping the cache at its hard
// entry cap. Scripts referenced by active assignments are pinned; if every slot
// is pinned, callers receive ResourceExhausted instead of unbounded growth.
// s.mu must be held by the caller.
func (s *Service) cacheScriptLocked(id string, content []byte, contentHash string) error {
	contentBytes := int64(len(content))
	if contentBytes > s.scriptCacheLimit {
		return fmt.Errorf("script %q is %d bytes and exceeds generator cache item limit %d", id, contentBytes, s.scriptCacheLimit)
	}
	if existing, ok := s.scripts[id]; ok {
		requiredBytes := s.scriptCacheBytes - int64(len(existing.content)) + contentBytes
		for requiredBytes > s.scriptCacheLimit {
			if !s.evictOneScriptExceptLocked(id) {
				return fmt.Errorf("generator script cache byte limit %d is pinned by active entries", s.scriptCacheLimit)
			}
			requiredBytes = s.scriptCacheBytes - int64(len(existing.content)) + contentBytes
		}
		s.scriptUseCounter++
		s.scriptCacheBytes -= int64(len(existing.content))
		existing.content = append(existing.content[:0], content...)
		existing.hash = contentHash
		existing.lastUsed = s.scriptUseCounter
		s.scripts[id] = existing
		s.scriptCacheBytes += contentBytes
		return nil
	}
	for len(s.scripts) >= s.scriptCacheMax || s.scriptCacheBytes+contentBytes > s.scriptCacheLimit {
		if !s.evictOneScriptLocked() {
			return fmt.Errorf("generator script cache is full (%d entries, %d bytes)", s.scriptCacheMax, s.scriptCacheLimit)
		}
	}
	s.scriptUseCounter++
	s.scripts[id] = cachedScript{content: append([]byte(nil), content...), hash: contentHash, lastUsed: s.scriptUseCounter}
	s.scriptCacheBytes += contentBytes
	return nil
}

// touchScriptLocked marks a cache entry as recently used. s.mu must be held.
func (s *Service) touchScriptLocked(id string) (cachedScript, bool) {
	cached, ok := s.scripts[id]
	if !ok {
		return cachedScript{}, false
	}
	s.scriptUseCounter++
	cached.lastUsed = s.scriptUseCounter
	s.scripts[id] = cached
	return cached, true
}

func (s *Service) evictOneScriptLocked() bool {
	return s.evictOneScriptExceptLocked("")
}

func (s *Service) evictOneScriptExceptLocked(excludedID string) bool {
	pinned := make(map[string]struct{})
	for _, assignment := range s.assignments {
		for _, scriptID := range assignment.groupScripts {
			pinned[scriptID] = struct{}{}
		}
	}
	victim := ""
	var oldest uint64
	for id, cached := range s.scripts {
		if id == excludedID {
			continue
		}
		if _, active := pinned[id]; active {
			continue
		}
		if victim == "" || cached.lastUsed < oldest || (cached.lastUsed == oldest && id < victim) {
			victim = id
			oldest = cached.lastUsed
		}
	}
	if victim == "" {
		return false
	}
	s.scriptCacheBytes -= int64(len(s.scripts[victim].content))
	delete(s.scripts, victim)
	return true
}

type leasedArchiveArtifact struct {
	file *os.File
	info os.FileInfo
}

// acquireArchiveArtifact opens and verifies the artifact while the stable
// generator namespace descriptor is held. The returned descriptor—not a path
// string—is used for the complete unary read or streaming transfer.
func (s *Service) acquireArchiveArtifact(request *pb.ArchiveArtifactRequest) (*leasedArchiveArtifact, func(), error) {
	testID := request.GetTestId()
	if err := validateArchiveTestID(testID); err != nil {
		return nil, nil, status.Errorf(codes.InvalidArgument, "invalid test_id: %v", err)
	}
	name, err := normalizeGeneratorArchiveArtifactName(request.GetName())
	if err != nil {
		return nil, nil, status.Errorf(codes.InvalidArgument, "invalid artifact name: %v", err)
	}
	s.sweepArchivedRecords(time.Now())

	s.mu.Lock()
	record := s.archiveRecords[testID]
	if record == nil {
		s.mu.Unlock()
		return nil, nil, status.Error(codes.NotFound, "archive not found")
	}
	expectedIdentity := s.archiveIdentities[testID]
	s.archiveLeases[testID]++
	namespace := s.archiveNamespace
	namespaceErr := s.archiveNamespaceErr
	s.mu.Unlock()

	var file *os.File
	var once sync.Once
	release := func() {
		once.Do(func() {
			if file != nil {
				_ = file.Close()
			}
			s.mu.Lock()
			if s.archiveLeases[testID] <= 1 {
				delete(s.archiveLeases, testID)
			} else {
				s.archiveLeases[testID]--
			}
			s.mu.Unlock()
			s.sweepArchivedRecords(time.Now())
		})
	}
	if namespaceErr != nil || namespace == nil {
		release()
		if namespaceErr == nil {
			namespaceErr = fmt.Errorf("generator archive namespace is unavailable")
		}
		return nil, nil, status.Errorf(codes.Internal, "open generator archive namespace: %v", namespaceErr)
	}
	runRoot, runIdentity, err := openGeneratorArchiveRun(namespace, testID, false)
	if err != nil {
		release()
		if os.IsNotExist(err) {
			return nil, nil, status.Error(codes.NotFound, "archive not found")
		}
		return nil, nil, status.Errorf(codes.FailedPrecondition, "open archive run: %v", err)
	}
	if expectedIdentity == nil || !os.SameFile(expectedIdentity, runIdentity) {
		_ = runRoot.Close()
		release()
		return nil, nil, status.Error(codes.FailedPrecondition, "archive run was replaced")
	}
	file, info, err := openGeneratorArchiveRegularFile(runRoot, name)
	_ = runRoot.Close()
	if err != nil {
		release()
		if os.IsNotExist(err) {
			return nil, nil, status.Error(codes.NotFound, "archive artifact not found")
		}
		return nil, nil, status.Errorf(codes.FailedPrecondition, "open archive artifact: %v", err)
	}
	return &leasedArchiveArtifact{file: file, info: info}, release, nil
}

func (s *Service) storeArchiveRecordWithLifecycle(record *storage.RunArchiveRecord) {
	if err := s.storeArchiveRecordWithIdentity(record, nil); err != nil {
		slog.Warn("generator archive record rejected", "test_id", archiveRecordTestID(record), "error", err)
	}
}

func archiveRecordTestID(record *storage.RunArchiveRecord) string {
	if record == nil {
		return ""
	}
	return record.TestID
}

func (s *Service) storeArchiveRecordWithIdentity(record *storage.RunArchiveRecord, expectedIdentity os.FileInfo) error {
	if record == nil {
		return nil
	}
	if err := validateArchiveTestID(record.TestID); err != nil {
		return err
	}
	runRoot, runIdentity, err := openGeneratorArchiveRun(s.archiveNamespace, record.TestID, false)
	if err != nil {
		return fmt.Errorf("open finalized generator archive: %w", err)
	}
	_ = runRoot.Close()
	if expectedIdentity != nil && !os.SameFile(expectedIdentity, runIdentity) {
		return fmt.Errorf("generator archive run %q was replaced before registration", record.TestID)
	}
	s.mu.Lock()
	clone := *record
	s.archiveRecords[record.TestID] = &clone
	s.archiveIdentities[record.TestID] = runIdentity
	removed := s.pruneArchivedRecordsLocked(time.Now())
	s.mu.Unlock()
	s.removeGeneratorArchiveRecords(removed)
	return nil
}

func (s *Service) sweepArchivedRecords(now time.Time) {
	s.mu.Lock()
	removed := s.pruneArchivedRecordsLocked(now)
	s.mu.Unlock()
	s.removeGeneratorArchiveRecords(removed)
}

// pruneArchivedRecordsLocked applies TTL first and then an oldest-first hard
// cap. Leased records are temporarily skipped; their release immediately
// triggers another pass. s.mu must be held.
type generatorArchiveRemoval struct {
	record   *storage.RunArchiveRecord
	identity os.FileInfo
}

func (s *Service) pruneArchivedRecordsLocked(now time.Time) []generatorArchiveRemoval {
	removed := make([]generatorArchiveRemoval, 0)
	if s.archiveTTL > 0 {
		for testID, record := range s.archiveRecords {
			if s.archiveLeases[testID] > 0 || !archiveRecordExpired(record, now, s.archiveTTL) {
				continue
			}
			removed = append(removed, generatorArchiveRemoval{record: cloneRunArchiveRecord(record), identity: s.archiveIdentities[testID]})
			s.archiveDeleting[testID] = struct{}{}
			delete(s.archiveRecords, testID)
			delete(s.archiveIdentities, testID)
		}
	}
	if len(s.archiveRecords) <= s.archiveRecordMax {
		return removed
	}
	type candidate struct {
		testID string
		record *storage.RunArchiveRecord
		at     time.Time
	}
	candidates := make([]candidate, 0, len(s.archiveRecords))
	for testID, record := range s.archiveRecords {
		candidates = append(candidates, candidate{testID: testID, record: record, at: archiveRecordTimestamp(record)})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].at.Equal(candidates[j].at) {
			return candidates[i].testID > candidates[j].testID
		}
		return candidates[i].at.After(candidates[j].at)
	})
	// Keep the newest configured window plus every in-flight lease. This permits
	// a bounded temporary overage for active readers without sacrificing a newer
	// archive merely because the oldest archive is still streaming.
	retained := make(map[string]struct{}, s.archiveRecordMax+len(s.archiveLeases))
	for index, candidate := range candidates {
		if index < s.archiveRecordMax || s.archiveLeases[candidate.testID] > 0 {
			retained[candidate.testID] = struct{}{}
		}
	}
	for _, candidate := range candidates {
		if _, keep := retained[candidate.testID]; keep {
			continue
		}
		removed = append(removed, generatorArchiveRemoval{record: cloneRunArchiveRecord(candidate.record), identity: s.archiveIdentities[candidate.testID]})
		s.archiveDeleting[candidate.testID] = struct{}{}
		delete(s.archiveRecords, candidate.testID)
		delete(s.archiveIdentities, candidate.testID)
	}
	return removed
}

func cloneRunArchiveRecord(record *storage.RunArchiveRecord) *storage.RunArchiveRecord {
	if record == nil {
		return nil
	}
	clone := *record
	return &clone
}

func archiveRecordExpired(record *storage.RunArchiveRecord, now time.Time, ttl time.Duration) bool {
	if record == nil || ttl <= 0 {
		return false
	}
	timestamp := archiveRecordTimestamp(record)
	return !timestamp.IsZero() && now.Sub(timestamp) > ttl
}

func archiveRecordTimestamp(record *storage.RunArchiveRecord) time.Time {
	if record == nil {
		return time.Time{}
	}
	if !record.CompletedAt.IsZero() {
		return record.CompletedAt
	}
	return record.CreatedAt
}

func (s *Service) generatorArchiveRoot() string {
	if s != nil && s.archiveNamespace != nil && strings.TrimSpace(s.archiveNamespace.path) != "" {
		return s.archiveNamespace.path
	}
	return filepath.Join(resolveArchiveRoot(), sanitizeArchiveNamespace(s.generatorID))
}

func (s *Service) removeGeneratorArchiveRecords(records []generatorArchiveRemoval) {
	for _, removal := range records {
		record := removal.record
		if record == nil {
			continue
		}
		restore := func(retain bool) {
			s.mu.Lock()
			if retain {
				if _, exists := s.archiveRecords[record.TestID]; !exists {
					s.archiveRecords[record.TestID] = cloneRunArchiveRecord(record)
					if removal.identity != nil {
						s.archiveIdentities[record.TestID] = removal.identity
					}
				}
			}
			delete(s.archiveDeleting, record.TestID)
			s.mu.Unlock()
		}
		if err := validateArchiveTestID(record.TestID); err != nil {
			slog.Warn("generator archive retention rejected unsafe test id", "test_id", record.TestID, "error", err)
			restore(true)
			continue
		}
		if removal.identity == nil {
			slog.Warn("generator archive retention refused archive without identity", "test_id", record.TestID, "path", record.ArchivePath)
			restore(true)
			continue
		}
		err := removeGeneratorArchiveRun(s.archiveNamespace, record.TestID, removal.identity)
		if os.IsNotExist(err) {
			err = nil
		}
		if err != nil {
			slog.Warn("generator archive retention cleanup failed", "test_id", record.TestID, "path", record.ArchivePath, "error", err)
		}
		restore(err != nil)
	}
}

func removeConfinedGeneratorArchiveDirectory(root, target string) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(rootAbs, targetAbs)
	if err != nil || relative == "." || relative == "" || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("archive path %q is outside generator archive root %q", target, root)
	}
	if strings.Contains(relative, string(filepath.Separator)) {
		return fmt.Errorf("archive path %q is not a direct generator run directory", target)
	}
	if err := validateArchiveTestID(relative); err != nil {
		return err
	}
	canonicalRoot, rootHandle, err := openCanonicalArchiveDirectory(rootAbs, false)
	if err != nil {
		return err
	}
	namespace := &generatorArchiveNamespace{path: canonicalRoot, root: rootHandle}
	defer namespace.close()
	runRoot, identity, err := openGeneratorArchiveRun(namespace, relative, false)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	_ = runRoot.Close()
	return removeGeneratorArchiveRun(namespace, relative, identity)
}

// sweepGeneratorArchiveDirectories bounds orphaned on-disk state left by a
// prior generator process. It runs once during construction, when this service
// has no active writers, and removes expired directories before enforcing the
// same oldest-first archive cap.
func (s *Service) sweepGeneratorArchiveDirectories(now time.Time) {
	root := s.generatorArchiveRoot()
	if s.archiveNamespaceErr != nil || s.archiveNamespace == nil {
		if s.archiveNamespaceErr != nil {
			slog.Warn("scan generator archive root", "path", root, "error", s.archiveNamespaceErr)
		}
		return
	}
	entries, err := readGeneratorArchiveDirectories(s.archiveNamespace)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("scan generator archive root", "path", root, "error", err)
		}
		return
	}
	type directory struct {
		testID   string
		path     string
		modTime  time.Time
		identity os.FileInfo
	}
	retained := make([]directory, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if err := validateArchiveTestID(entry.Name()); err != nil {
			continue
		}
		dir := directory{testID: entry.Name(), path: filepath.Join(root, entry.Name()), modTime: info.ModTime(), identity: info}
		if s.archiveTTL > 0 && now.Sub(dir.modTime) > s.archiveTTL {
			if err := removeGeneratorArchiveRun(s.archiveNamespace, dir.testID, dir.identity); err != nil {
				slog.Warn("remove expired generator archive directory", "path", dir.path, "error", err)
				retained = append(retained, dir)
			}
			continue
		}
		retained = append(retained, dir)
	}
	sort.Slice(retained, func(i, j int) bool {
		if retained[i].modTime.Equal(retained[j].modTime) {
			return retained[i].path < retained[j].path
		}
		return retained[i].modTime.Before(retained[j].modTime)
	})
	removeCount := len(retained) - s.archiveRecordMax
	if removeCount < 0 {
		removeCount = 0
	}
	failedRemovals := make([]directory, 0)
	for _, dir := range retained[:removeCount] {
		if err := removeGeneratorArchiveRun(s.archiveNamespace, dir.testID, dir.identity); err != nil {
			slog.Warn("remove excess generator archive directory", "path", dir.path, "error", err)
			failedRemovals = append(failedRemovals, dir)
		}
	}
	toLoad := append([]directory(nil), retained[removeCount:]...)
	toLoad = append(toLoad, failedRemovals...)
	for _, dir := range toLoad {
		record, err := s.loadRetainedGeneratorArchive(dir.testID, dir.identity)
		if err != nil {
			// Keep a quarantined catalog entry so the test ID remains reserved and
			// normal TTL/count retention can remove the crash-left directory. This
			// prevents a later assignment from silently replacing partial evidence.
			slog.Warn("quarantine incomplete retained generator archive", "path", dir.path, "error", err)
			s.archiveRecords[dir.testID] = &storage.RunArchiveRecord{
				TestID:         dir.testID,
				SchemaVersion:  archiveSchemaVersion,
				ArchivePath:    dir.path,
				ManifestPath:   filepath.Join(dir.path, "manifest.json"),
				CreatedAt:      dir.modTime,
				CompletedAt:    dir.modTime,
				ArchiveBackend: "filesystem",
			}
			s.archiveIdentities[dir.testID] = dir.identity
			continue
		}
		s.archiveRecords[dir.testID] = record
		s.archiveIdentities[dir.testID] = dir.identity
	}
}

func (s *Service) loadRetainedGeneratorArchive(testID string, expectedIdentity os.FileInfo) (*storage.RunArchiveRecord, error) {
	runRoot, identity, err := openGeneratorArchiveRun(s.archiveNamespace, testID, false)
	if err != nil {
		return nil, err
	}
	defer runRoot.Close()
	if expectedIdentity == nil || !os.SameFile(expectedIdentity, identity) {
		return nil, fmt.Errorf("generator archive run changed during recovery")
	}
	manifestFile, _, err := openGeneratorArchiveRegularFile(runRoot, "manifest.json")
	if err != nil {
		return nil, err
	}
	defer manifestFile.Close()
	const manifestLimit = int64(8 << 20)
	data, err := io.ReadAll(io.LimitReader(manifestFile, manifestLimit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > manifestLimit {
		return nil, fmt.Errorf("generator archive manifest exceeds %d bytes", manifestLimit)
	}
	var manifest archiveManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}
	if manifest.TestID != testID || manifest.SchemaVersion == "" || manifest.CompletedAt.IsZero() {
		return nil, fmt.Errorf("generator archive manifest is incomplete or mismatched")
	}
	return &storage.RunArchiveRecord{
		TestID: manifest.TestID, SchemaVersion: manifest.SchemaVersion,
		ArchivePath:      filepath.Join(s.generatorArchiveRoot(), testID),
		ManifestPath:     filepath.Join(s.generatorArchiveRoot(), testID, "manifest.json"),
		TransactionCount: manifest.TransactionCount, ResourceSampleCount: manifest.ResourceSampleCount,
		IntervalSummaryCount: manifest.IntervalSummaryCount, CreatedAt: manifest.CreatedAt, CompletedAt: manifest.CompletedAt,
	}, nil
}
