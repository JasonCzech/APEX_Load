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
	"google.golang.org/protobuf/encoding/protojson"
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
			service.scriptCache.setEntryLimit(scriptCacheMax)
		}
		if service.archiveManager != nil {
			service.archiveManager.configureRetention(archiveMax, archiveTTL)
		}
	}
}

// WithGeneratorScriptCacheByteLimit applies a second, content-size bound in
// addition to the entry count cap.
func WithGeneratorScriptCacheByteLimit(maxBytes int64) ServiceOption {
	return func(service *Service) {
		if maxBytes > 0 {
			service.scriptCache.setByteLimit(maxBytes)
		}
	}
}

// WithGeneratorObjectLimits overrides the hard active-object admission limits.
// It is primarily useful for constrained deployments and deterministic tests.
// Non-positive arguments retain the corresponding production default.
func WithGeneratorObjectLimits(tests, groups, executions, writers int) ServiceOption {
	return func(service *Service) {
		if tests > 0 {
			service.maxAssignedTests = tests
		}
		if groups > 0 {
			service.maxAssignedGroups = groups
		}
		if executions > 0 {
			service.maxExecutions = executions
		}
		if writers > 0 {
			service.archiveManager.configureWriterLimit(writers)
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
	if identity := request.GetSourceIdentity(); identity != "" && !validArchiveSourceIdentity(identity) {
		return nil, nil, status.Error(codes.InvalidArgument, "invalid archive source identity")
	}
	s.sweepArchivedRecords(time.Now())

	manager := s.archiveManager
	lease, retained := manager.acquireCatalogLease(testID)
	if !retained {
		return nil, nil, status.Error(codes.NotFound, "archive not found")
	}

	var file *os.File
	var once sync.Once
	release := func() {
		once.Do(func() {
			if file != nil {
				_ = file.Close()
			}
			s.removeGeneratorArchiveRecords(manager.releaseCatalogLease(testID, time.Now()))
		})
	}
	if lease.namespaceErr != nil || lease.namespace == nil {
		release()
		if lease.namespaceErr == nil {
			lease.namespaceErr = fmt.Errorf("generator archive namespace is unavailable")
		}
		return nil, nil, status.Errorf(codes.Internal, "open generator archive namespace: %v", lease.namespaceErr)
	}
	runRoot, runIdentity, err := openGeneratorArchiveRun(lease.namespace, testID, false)
	if err != nil {
		release()
		if os.IsNotExist(err) {
			return nil, nil, status.Error(codes.NotFound, "archive not found")
		}
		return nil, nil, status.Errorf(codes.FailedPrecondition, "open archive run: %v", err)
	}
	if lease.expectedIdentity == nil || !os.SameFile(lease.expectedIdentity, runIdentity) {
		_ = runRoot.Close()
		release()
		return nil, nil, status.Error(codes.FailedPrecondition, "archive run was replaced")
	}
	if identity := request.GetSourceIdentity(); identity != "" {
		if err := verifyArchiveSourceIdentity(runRoot, testID, identity); err != nil {
			_ = runRoot.Close()
			release()
			return nil, nil, status.Errorf(codes.FailedPrecondition, "verify archive source identity: %v", err)
		}
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
	namespace, namespaceErr := s.archiveManager.namespaceSnapshot()
	if namespaceErr != nil {
		return namespaceErr
	}
	runRoot, runIdentity, err := openGeneratorArchiveRun(namespace, record.TestID, false)
	if err != nil {
		return fmt.Errorf("open finalized generator archive: %w", err)
	}
	_ = runRoot.Close()
	if expectedIdentity != nil && !os.SameFile(expectedIdentity, runIdentity) {
		return fmt.Errorf("generator archive run %q was replaced before registration", record.TestID)
	}
	s.removeGeneratorArchiveRecords(s.archiveManager.registerRecord(record, runIdentity, time.Now()))
	return nil
}

func (s *Service) sweepArchivedRecords(now time.Time) {
	s.removeGeneratorArchiveRecords(s.archiveManager.sweep(now))
}

func (s *Service) generatorArchiveRoot() string {
	if s != nil && s.archiveManager != nil && strings.TrimSpace(s.archiveManager.root()) != "" {
		return s.archiveManager.root()
	}
	return filepath.Join(resolveArchiveRoot(), sanitizeArchiveNamespace(s.generatorID))
}

func (s *Service) removeGeneratorArchiveRecords(records []generatorArchiveRemoval) {
	namespace, _ := s.archiveManager.namespaceSnapshot()
	for _, removal := range records {
		record := removal.record
		if record == nil {
			continue
		}
		restore := func(retain bool) {
			s.archiveManager.completeRemoval(removal, retain)
			if !retain {
				s.metricsBroker.deleteTerminalsForTest(record.TestID)
			}
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
		err := removeGeneratorArchiveRun(namespace, record.TestID, removal.identity)
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
	namespace, namespaceErr := s.archiveManager.namespaceSnapshot()
	maxRecords, archiveTTL := s.archiveManager.retention()
	if namespaceErr != nil || namespace == nil {
		if namespaceErr != nil {
			slog.Warn("scan generator archive root", "path", root, "error", namespaceErr)
		}
		return
	}
	entries, err := readGeneratorArchiveDirectories(namespace)
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
		if archiveTTL > 0 && now.Sub(dir.modTime) > archiveTTL {
			if err := removeGeneratorArchiveRun(namespace, dir.testID, dir.identity); err != nil {
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
	removeCount := len(retained) - maxRecords
	if removeCount < 0 {
		removeCount = 0
	}
	failedRemovals := make([]directory, 0)
	for _, dir := range retained[:removeCount] {
		if err := removeGeneratorArchiveRun(namespace, dir.testID, dir.identity); err != nil {
			slog.Warn("remove excess generator archive directory", "path", dir.path, "error", err)
			failedRemovals = append(failedRemovals, dir)
		}
	}
	toLoad := append([]directory(nil), retained[removeCount:]...)
	toLoad = append(toLoad, failedRemovals...)
	for _, dir := range toLoad {
		record, terminalBatch, err := s.loadRetainedGeneratorArchive(dir.testID, dir.identity)
		if err != nil {
			// Keep a quarantined catalog entry so the test ID remains reserved and
			// normal TTL/count retention can remove the crash-left directory. This
			// prevents a later assignment from silently replacing partial evidence.
			slog.Warn("quarantine incomplete retained generator archive", "path", dir.path, "error", err)
			s.archiveManager.restoreRecord(&storage.RunArchiveRecord{
				TestID:         dir.testID,
				SchemaVersion:  archiveSchemaVersion,
				ArchivePath:    dir.path,
				ManifestPath:   filepath.Join(dir.path, "manifest.json"),
				CreatedAt:      dir.modTime,
				CompletedAt:    dir.modTime,
				ArchiveBackend: "filesystem",
			}, dir.identity)
			continue
		}
		s.archiveManager.restoreRecord(record, dir.identity)
		if terminalBatch != nil {
			s.metricsBroker.restoreTerminal(terminalBatch, record.CompletedAt)
		}
	}
}

func (s *Service) loadRetainedGeneratorArchive(testID string, expectedIdentity os.FileInfo) (*storage.RunArchiveRecord, *pb.MetricsBatch, error) {
	namespace, namespaceErr := s.archiveManager.namespaceSnapshot()
	if namespaceErr != nil {
		return nil, nil, namespaceErr
	}
	runRoot, identity, err := openGeneratorArchiveRun(namespace, testID, false)
	if err != nil {
		return nil, nil, err
	}
	defer runRoot.Close()
	if expectedIdentity == nil || !os.SameFile(expectedIdentity, identity) {
		return nil, nil, fmt.Errorf("generator archive run changed during recovery")
	}
	manifestFile, _, err := openGeneratorArchiveRegularFile(runRoot, "manifest.json")
	if err != nil {
		return nil, nil, err
	}
	defer manifestFile.Close()
	const manifestLimit = int64(8 << 20)
	data, err := io.ReadAll(io.LimitReader(manifestFile, manifestLimit+1))
	if err != nil {
		return nil, nil, err
	}
	if int64(len(data)) > manifestLimit {
		return nil, nil, fmt.Errorf("generator archive manifest exceeds %d bytes", manifestLimit)
	}
	var manifest archiveManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, nil, err
	}
	if manifest.TestID != testID || manifest.SchemaVersion == "" || manifest.CompletedAt.IsZero() {
		return nil, nil, fmt.Errorf("generator archive manifest is incomplete or mismatched")
	}
	record := &storage.RunArchiveRecord{
		TestID: manifest.TestID, SchemaVersion: manifest.SchemaVersion,
		ArchivePath:      filepath.Join(s.generatorArchiveRoot(), testID),
		ManifestPath:     filepath.Join(s.generatorArchiveRoot(), testID, "manifest.json"),
		TransactionCount: manifest.TransactionCount, ResourceSampleCount: manifest.ResourceSampleCount,
		IntervalSummaryCount: manifest.IntervalSummaryCount, CreatedAt: manifest.CreatedAt, CompletedAt: manifest.CompletedAt,
	}
	var terminalBatch *pb.MetricsBatch
	if isGeneratorTerminalLifecycle(manifest.Status) {
		if len(manifest.TerminalMetrics) > 0 {
			terminalBatch = &pb.MetricsBatch{}
			if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(manifest.TerminalMetrics, terminalBatch); err != nil {
				return nil, nil, fmt.Errorf("decode retained terminal metrics: %w", err)
			}
			if terminalBatch.GetTestId() != testID || terminalBatch.GetExecutionGeneration() != manifest.ExecutionGeneration || !isGeneratorTerminalLifecycle(terminalBatch.GetLifecycleState()) {
				return nil, nil, fmt.Errorf("retained terminal metrics identity is incomplete or mismatched")
			}
		} else {
			// Backward compatibility for archives finalized before aggregate terminal
			// evidence was embedded in the manifest.
			groups := make(map[string]int32, len(manifest.ExecutionGroups))
			for _, group := range manifest.ExecutionGroups {
				groups[group] = 0
			}
			terminalBatch = s.metricsBatchForState(testID, manifest.ExecutionGeneration, 0, false, groups, manifest.Status, manifest.Error)
		}
		terminalBatch.LifecycleState = manifest.Status
		terminalBatch.LifecycleError = manifest.Error
		terminalBatch.TimestampUnixMs = manifest.CompletedAt.UnixMilli()
	}
	return record, terminalBatch, nil
}
