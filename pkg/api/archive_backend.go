package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/internal/archivequota"
	"github.com/jasonczech/apexload/pkg/storage"
	"gocloud.dev/blob"
	_ "gocloud.dev/blob/azureblob"
	_ "gocloud.dev/blob/fileblob"
	_ "gocloud.dev/blob/gcsblob"
	_ "gocloud.dev/blob/s3blob"
)

const (
	maxArchiveManifestDownloadBytes         = 8 << 20
	maxArchiveMaterializedArtifacts         = 256
	defaultArchiveMaterializedBytes         = int64(64 << 30)
	defaultArchiveMaterializeTimeout        = 30 * time.Minute
	defaultArchiveMaterializationCacheBytes = int64(256 << 30)
	defaultArchiveMaterializationCacheTTL   = 24 * time.Hour
	envArchiveMaterializedMaxBytes          = "APEXLOAD_ARCHIVE_MATERIALIZATION_MAX_BYTES"
	envArchiveMaterializeTimeout            = "APEXLOAD_ARCHIVE_MATERIALIZATION_TIMEOUT"
	envArchiveMaterializationCacheMaxBytes  = "APEXLOAD_ARCHIVE_MATERIALIZATION_CACHE_MAX_BYTES"
	envArchiveMaterializationCacheTTL       = "APEXLOAD_ARCHIVE_MATERIALIZATION_CACHE_TTL"
)

const (
	archiveBackendFilesystem = "filesystem"
	archiveBackendS3         = "s3"
	archiveBackendAzureBlob  = "azure_blob"
	archiveBackendGCS        = "gcs"
	archiveVersionSegment    = "_versions"
	archiveVersionSchema     = "v1"
	archiveSourceSegment     = "_source"
	archiveSourceSchema      = "v1"
)

type archiveBackendRuntime struct {
	Backend   string
	LocalRoot string
	URIPrefix string
	Bucket    string
	Container string
	Prefix    string
	Region    string
	Endpoint  string
	Account   string
}

func currentArchiveBackend() archiveBackendRuntime {
	backend := strings.TrimSpace(strings.ToLower(os.Getenv("APEXLOAD_ARCHIVE_BACKEND")))
	if backend == "" {
		backend = archiveBackendFilesystem
	}
	filesystemRoot := resolveFilesystemArchiveRoot()
	prefix := cleanArchivePrefix(os.Getenv("APEXLOAD_ARCHIVE_PREFIX"))
	runtime := archiveBackendRuntime{
		Backend:   backend,
		LocalRoot: filesystemRoot,
		Prefix:    prefix,
		Region:    strings.TrimSpace(os.Getenv("APEXLOAD_ARCHIVE_REGION")),
		Endpoint:  strings.TrimSpace(os.Getenv("APEXLOAD_ARCHIVE_ENDPOINT")),
		Account:   strings.TrimSpace(os.Getenv("AZURE_STORAGE_ACCOUNT")),
	}
	switch backend {
	case archiveBackendS3:
		runtime.Bucket = strings.TrimSpace(os.Getenv("APEXLOAD_ARCHIVE_BUCKET"))
		runtime.LocalRoot = objectArchiveStagingRoot(filesystemRoot, backend)
	case archiveBackendAzureBlob:
		runtime.Container = firstNonEmpty(strings.TrimSpace(os.Getenv("APEXLOAD_ARCHIVE_CONTAINER")), strings.TrimSpace(os.Getenv("APEXLOAD_ARCHIVE_BUCKET")))
		runtime.LocalRoot = objectArchiveStagingRoot(filesystemRoot, backend)
	case archiveBackendGCS:
		runtime.Bucket = strings.TrimSpace(os.Getenv("APEXLOAD_ARCHIVE_BUCKET"))
		runtime.LocalRoot = objectArchiveStagingRoot(filesystemRoot, backend)
	default:
		runtime.Backend = archiveBackendFilesystem
	}
	if runtime.Backend != archiveBackendFilesystem {
		target := runtime.Bucket
		scheme := map[string]string{archiveBackendS3: "s3", archiveBackendAzureBlob: "azblob", archiveBackendGCS: "gs"}[runtime.Backend]
		if runtime.Backend == archiveBackendAzureBlob {
			target = runtime.Container
		}
		runtime.Prefix = joinArchiveURI(prefix, archiveSourceSegment, archiveSourceSchema, archiveObjectSourceIdentity(runtime, prefix))
		runtime.URIPrefix = joinArchiveURI(scheme+"://"+target, runtime.Prefix)
		runtime.LocalRoot = archiveObjectNamespaceRoot(runtime.LocalRoot, runtime)
	}
	return runtime
}

func archiveObjectSourceIdentity(backend archiveBackendRuntime, basePrefix string) string {
	target := backend.Bucket
	if backend.Backend == archiveBackendAzureBlob {
		target = backend.Container
	}
	identity := strings.Join([]string{
		strings.ToLower(strings.TrimSpace(backend.Backend)),
		strings.ToLower(strings.TrimSpace(target)),
		strings.Trim(strings.TrimSpace(basePrefix), "/"),
		strings.TrimRight(strings.ToLower(strings.TrimSpace(backend.Endpoint)), "/"),
		strings.ToLower(strings.TrimSpace(backend.Account)),
	}, "\x00")
	return sha256BytesHex([]byte(identity))
}

func resolveArchiveRoot() string {
	return currentArchiveBackend().LocalRoot
}

func resolveFilesystemArchiveRoot() string {
	return config.ArchiveDir()
}

func objectArchiveStagingRoot(filesystemRoot string, backend string) string {
	if dir := strings.TrimSpace(os.Getenv("APEXLOAD_ARCHIVE_STAGING_DIR")); dir != "" {
		return filepath.Join(dir, backend)
	}
	return filepath.Join(filesystemRoot, "_object_staging", backend)
}

func archiveObjectNamespaceRoot(stagingRoot string, backend archiveBackendRuntime) string {
	return filepath.Join(stagingRoot, archiveObjectNamespace(backend))
}

func archiveObjectNamespace(backend archiveBackendRuntime) string {
	target := backend.Bucket
	if backend.Backend == archiveBackendAzureBlob {
		target = backend.Container
	}
	identity := strings.Join([]string{
		strings.ToLower(strings.TrimSpace(backend.Backend)),
		strings.ToLower(strings.TrimSpace(target)),
		strings.Trim(strings.TrimSpace(backend.Prefix), "/"),
		strings.TrimRight(strings.ToLower(strings.TrimSpace(backend.Endpoint)), "/"),
		strings.ToLower(strings.TrimSpace(backend.Account)),
	}, "\x00")
	return sha256BytesHex([]byte(identity))
}

func cleanArchivePrefix(prefix string) string {
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")
	if prefix == "" {
		return "apexload/archives"
	}
	return prefix
}

func joinArchiveURI(base string, parts ...string) string {
	base = strings.TrimRight(base, "/")
	cleaned := []string{}
	for _, part := range parts {
		part = strings.Trim(part, "/")
		if part != "" {
			cleaned = append(cleaned, part)
		}
	}
	if len(cleaned) == 0 {
		return base
	}
	return base + "/" + strings.Join(cleaned, "/")
}

func (b archiveBackendRuntime) runURI(testID string) string {
	if b.URIPrefix == "" {
		return ""
	}
	return joinArchiveURI(b.URIPrefix, testID)
}

func (b archiveBackendRuntime) manifestURI(testID string) string {
	runURI := b.runURI(testID)
	if runURI == "" {
		return ""
	}
	return joinArchiveURI(runURI, "manifest.json")
}

func (b archiveBackendRuntime) objectKey(testID string, name string) string {
	return strings.Trim(joinArchiveURI(b.Prefix, testID, name), "/")
}

// bindArchiveRecordToFinalizationVersion gives each durable publication intent
// an immutable object namespace. A stale worker may finish writing its own
// token-scoped objects, but it can no longer overwrite bytes referenced by a
// newer catalog record. The resulting URI is persisted with the outbox row
// before upload begins.
func bindArchiveRecordToFinalizationVersion(record *storage.RunArchiveRecord, intentToken string) error {
	if record == nil {
		return fmt.Errorf("archive record is required")
	}
	if archiveRecordBackend(record) == archiveBackendFilesystem {
		return nil
	}
	if err := validateArchiveRunID(record.TestID); err != nil {
		return err
	}
	intentToken = strings.TrimSpace(intentToken)
	if err := validateArchiveIntentToken(intentToken); err != nil {
		return err
	}

	runURI := strings.TrimRight(strings.TrimSpace(record.ArchiveURI), "/")
	if runURI == "" && strings.TrimSpace(record.ManifestURI) != "" {
		manifestURI := strings.TrimRight(strings.TrimSpace(record.ManifestURI), "/")
		if !strings.HasSuffix(manifestURI, "/"+runArchiveManifestFileName) {
			return fmt.Errorf("persisted manifest URI is not a manifest object")
		}
		runURI = strings.TrimSuffix(manifestURI, "/"+runArchiveManifestFileName)
	}
	if runURI == "" {
		return fmt.Errorf("object archive %q has no publication URI", record.TestID)
	}
	parsed, err := url.Parse(runURI)
	if err != nil {
		return fmt.Errorf("parse archive publication URI: %w", err)
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" || strings.TrimSpace(parsed.Host) == "" {
		return fmt.Errorf("archive publication URI is not an object namespace")
	}
	expectedScheme := map[string]string{
		archiveBackendS3:        "s3",
		archiveBackendAzureBlob: "azblob",
		archiveBackendGCS:       "gs",
	}[archiveRecordBackend(record)]
	if expectedScheme == "" || !strings.EqualFold(parsed.Scheme, expectedScheme) {
		return fmt.Errorf("archive publication URI scheme %q does not match backend %q", parsed.Scheme, archiveRecordBackend(record))
	}
	prefix, err := archiveObjectRunPathPrefix(parsed, record.TestID)
	if err != nil {
		return fmt.Errorf("archive publication URI %q: %w", runURI, err)
	}
	segments := []string{}
	if prefix != "" {
		segments = strings.Split(prefix, "/")
	}
	if len(segments) >= 3 && segments[len(segments)-3] == archiveVersionSegment && segments[len(segments)-2] == archiveVersionSchema && validateArchiveIntentToken(segments[len(segments)-1]) == nil {
		segments = segments[:len(segments)-3]
	}
	base := parsed.Scheme + "://" + parsed.Host
	if len(segments) > 0 {
		base = joinArchiveURI(base, strings.Join(segments, "/"))
	}
	versionURI := joinArchiveURI(base, archiveVersionSegment, archiveVersionSchema, intentToken, record.TestID)
	record.ArchiveURI = versionURI
	record.ManifestURI = joinArchiveURI(versionURI, runArchiveManifestFileName)
	return nil
}

func validateArchiveIntentToken(intentToken string) error {
	if intentToken == "" || intentToken != strings.TrimSpace(intentToken) {
		return fmt.Errorf("archive finalization intent token is invalid")
	}
	parsed, err := uuid.Parse(intentToken)
	if err != nil || parsed.String() != strings.ToLower(intentToken) {
		return fmt.Errorf("archive finalization intent token is invalid")
	}
	return nil
}

func archiveObjectRunPathPrefix(parsed *url.URL, testID string) (string, error) {
	if parsed == nil {
		return "", fmt.Errorf("object URI is required")
	}
	if parsed.RawPath != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("object URI must use a canonical path without query or fragment")
	}
	runPath := strings.Trim(parsed.Path, "/")
	if runPath == "" || parsed.Path != "/"+runPath {
		return "", fmt.Errorf("object URI path is not canonical")
	}
	segments := strings.Split(runPath, "/")
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." || strings.ContainsRune(segment, 0) || strings.Contains(segment, `\`) {
			return "", fmt.Errorf("object URI contains an unsafe path component")
		}
		for _, r := range segment {
			if r < 0x20 || r == 0x7f {
				return "", fmt.Errorf("object URI contains an unsafe path component")
			}
		}
	}
	if segments[len(segments)-1] != testID {
		return "", fmt.Errorf("object URI is not scoped to test %q", testID)
	}
	return strings.Join(segments[:len(segments)-1], "/"), nil
}

func validatePersistedArchiveSource(configured archiveBackendRuntime, recordedBackend string, target string, prefix string) error {
	segments := strings.Split(strings.Trim(prefix, "/"), "/")
	baseEnd := len(segments)
	if baseEnd >= 3 && segments[baseEnd-3] == archiveVersionSegment && segments[baseEnd-2] == archiveVersionSchema && validateArchiveIntentToken(segments[baseEnd-1]) == nil {
		baseEnd -= 3
	}
	if baseEnd >= 3 && segments[baseEnd-3] == archiveSourceSegment && segments[baseEnd-2] == archiveSourceSchema {
		persistedIdentity := segments[baseEnd-1]
		basePrefix := strings.Join(segments[:baseEnd-3], "/")
		expected := configured
		expected.Backend = recordedBackend
		expected.Bucket = target
		expected.Container = ""
		if recordedBackend == archiveBackendAzureBlob {
			expected.Bucket = ""
			expected.Container = target
		}
		if persistedIdentity != archiveObjectSourceIdentity(expected, basePrefix) {
			return fmt.Errorf("persisted archive source identity does not match the configured endpoint/account namespace")
		}
		return nil
	}
	// Legacy global S3/GCS URIs remain unambiguous without a custom endpoint.
	// Custom endpoints and Azure container-only URIs are not: require the source
	// marker for those archives instead of silently opening a different account.
	if strings.TrimSpace(configured.Endpoint) != "" || (recordedBackend == archiveBackendAzureBlob && strings.TrimSpace(configured.Account) != "") {
		return fmt.Errorf("persisted archive URI lacks an endpoint/account source identity")
	}
	return nil
}

func (b archiveBackendRuntime) bucketURL() string {
	switch b.Backend {
	case archiveBackendS3:
		bucketURL := "s3://" + b.Bucket
		params := []string{}
		if b.Region != "" {
			params = append(params, "region="+url.QueryEscape(b.Region))
		}
		if b.Endpoint != "" {
			params = append(params, "endpoint="+url.QueryEscape(b.Endpoint))
		}
		if len(params) > 0 {
			bucketURL += "?" + strings.Join(params, "&")
		}
		return bucketURL
	case archiveBackendAzureBlob:
		return "azblob://" + b.Container
	case archiveBackendGCS:
		return "gs://" + b.Bucket
	default:
		return ""
	}
}

func (b archiveBackendRuntime) openBucket(ctx context.Context) (*blob.Bucket, error) {
	if err := b.validate(); err != nil {
		return nil, err
	}
	if b.Backend == archiveBackendFilesystem {
		return nil, nil
	}
	bucket, err := blob.OpenBucket(ctx, b.bucketURL())
	if err != nil {
		return nil, fmt.Errorf("open archive object bucket: %w", err)
	}
	return bucket, nil
}

func uploadArchiveDirectory(ctx context.Context, localDir string, testID string) error {
	if err := validateArchiveRunID(testID); err != nil {
		return err
	}
	backend := currentArchiveBackend()
	if backend.Backend == archiveBackendFilesystem {
		return nil
	}
	root, err := openArchiveDirectoryNoSymlink(localDir)
	if err != nil {
		return fmt.Errorf("inspect archive upload root: %w", err)
	}
	_ = root.Close()
	bucket, err := backend.openBucket(ctx)
	if err != nil {
		return err
	}
	defer bucket.Close()
	names := make([]string, 0)
	err = filepath.WalkDir(localDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("archive artifact %q is a symbolic link", path)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("archive artifact %q is not a regular file", path)
		}
		relative, err := filepath.Rel(localDir, path)
		if err != nil {
			return err
		}
		names = append(names, filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		return err
	}
	return uploadArchiveObjectsToBucket(ctx, bucket, backend, localDir, testID, names...)
}

func uploadArchiveObjects(ctx context.Context, localDir string, testID string, names ...string) error {
	if err := validateArchiveRunID(testID); err != nil {
		return err
	}
	backend := currentArchiveBackend()
	if backend.Backend == archiveBackendFilesystem {
		return nil
	}
	bucket, err := backend.openBucket(ctx)
	if err != nil {
		return err
	}
	defer bucket.Close()
	return uploadArchiveObjectsToBucket(ctx, bucket, backend, localDir, testID, names...)
}

func uploadArchiveObjectsToBucket(ctx context.Context, bucket *blob.Bucket, backend archiveBackendRuntime, localDir string, testID string, names ...string) error {
	if err := validateArchiveRunID(testID); err != nil {
		return err
	}
	if bucket == nil {
		return fmt.Errorf("archive object bucket is required")
	}
	root, err := openArchiveDirectoryNoSymlink(localDir)
	if err != nil {
		return fmt.Errorf("inspect archive upload root: %w", err)
	}
	_ = root.Close()
	seen := map[string]bool{}
	ordered := make([]string, 0, len(names))
	manifestName := ""
	for _, name := range names {
		name = strings.TrimSpace(filepath.ToSlash(name))
		if name == "" || seen[name] {
			continue
		}
		normalized, err := normalizeArchiveArtifactName(name)
		if err != nil {
			return err
		}
		name = normalized
		seen[name] = true
		if name == runArchiveManifestFileName {
			manifestName = name
			continue
		}
		ordered = append(ordered, name)
	}
	// Object storage has no directory transaction. Publish every data and
	// adjunct artifact first, then manifest.json as the commit marker. A failed
	// partial attempt therefore never advertises a manifest whose referenced
	// objects were not all attempted successfully.
	sort.Strings(ordered)
	if manifestName != "" {
		ordered = append(ordered, manifestName)
	}
	for _, name := range ordered {
		key := backend.objectKey(testID, name)
		if err := uploadArchiveFileToBucket(ctx, bucket, key, localDir, name); err != nil {
			return fmt.Errorf("upload archive object %s: %w", key, err)
		}
	}
	return nil
}

func uploadArchiveFileToBucket(ctx context.Context, bucket *blob.Bucket, key string, localRoot string, name string) error {
	file, _, err := openArchiveArtifactForRead(localRoot, name)
	if err != nil {
		return err
	}
	defer file.Close()
	writer, err := bucket.NewWriter(ctx, key, nil)
	if err != nil {
		return err
	}
	if _, err := io.Copy(writer, file); err != nil {
		_ = writer.Close()
		return err
	}
	return writer.Close()
}

// deleteArchiveBackendObjects removes every object belonging to a test run.
// Object backends do not have directories, so deletion must enumerate the
// complete run prefix instead of deleting only the manifest object.
func deleteArchiveBackendObjects(ctx context.Context, record *storage.RunArchiveRecord) error {
	if record == nil {
		return nil
	}
	if err := validateArchiveRunID(record.TestID); err != nil {
		return err
	}
	backend := currentArchiveBackend()
	recordedBackend := archiveRecordBackend(record)
	if recordedBackend != "" && recordedBackend != backend.Backend {
		return fmt.Errorf("archive backend %q is not configured (current backend %q)", recordedBackend, backend.Backend)
	}
	if backend.Backend == archiveBackendFilesystem {
		return nil
	}
	testID := record.TestID
	var err error
	backend, err = archiveDeletionRuntime(backend, record)
	if err != nil {
		return err
	}
	bucket, err := backend.openBucket(ctx)
	if err != nil {
		return err
	}
	defer bucket.Close()
	return deleteArchiveObjectsFromBucket(ctx, bucket, backend, testID)
}

func archiveDeletionRuntime(backend archiveBackendRuntime, record *storage.RunArchiveRecord) (archiveBackendRuntime, error) {
	if record == nil {
		return backend, nil
	}
	if err := validateArchiveRunID(record.TestID); err != nil {
		return backend, err
	}
	if backend.Backend == archiveBackendFilesystem {
		return backend, nil
	}
	testID := record.TestID
	runURI := strings.TrimSpace(record.ArchiveURI)
	if runURI == "" && strings.TrimSpace(record.ManifestURI) != "" {
		runURI = strings.TrimSuffix(strings.TrimSpace(record.ManifestURI), "/manifest.json")
	}
	if runURI == "" {
		return backend, nil
	}
	parsed, err := url.Parse(runURI)
	if err != nil {
		return backend, fmt.Errorf("parse archive URI: %w", err)
	}
	expectedTarget := backend.Bucket
	expectedScheme := ""
	switch backend.Backend {
	case archiveBackendS3:
		expectedScheme = "s3"
	case archiveBackendGCS:
		expectedScheme = "gs"
	case archiveBackendAzureBlob:
		expectedScheme = "azblob"
	}
	if backend.Backend == archiveBackendAzureBlob {
		expectedTarget = backend.Container
	}
	if !strings.EqualFold(parsed.Scheme, expectedScheme) {
		return backend, fmt.Errorf("archive URI scheme %q does not match backend %q", parsed.Scheme, backend.Backend)
	}
	if !strings.EqualFold(parsed.Host, expectedTarget) {
		return backend, fmt.Errorf("archive URI target %q does not match configured target %q", parsed.Host, expectedTarget)
	}
	prefix, err := archiveObjectRunPathPrefix(parsed, testID)
	if err != nil {
		return backend, fmt.Errorf("archive URI %q: %w", runURI, err)
	}
	if err := validatePersistedArchiveSource(backend, backend.Backend, parsed.Host, prefix); err != nil {
		return backend, err
	}
	backend.Prefix = prefix
	return backend, nil
}

func archiveRecordBackend(record *storage.RunArchiveRecord) string {
	if record == nil {
		return ""
	}
	if backend := strings.ToLower(strings.TrimSpace(record.ArchiveBackend)); backend != "" {
		return backend
	}
	switch {
	case strings.HasPrefix(strings.ToLower(strings.TrimSpace(record.ArchiveURI)), "s3://"):
		return archiveBackendS3
	case strings.HasPrefix(strings.ToLower(strings.TrimSpace(record.ArchiveURI)), "azblob://"):
		return archiveBackendAzureBlob
	case strings.HasPrefix(strings.ToLower(strings.TrimSpace(record.ArchiveURI)), "gs://"):
		return archiveBackendGCS
	case strings.TrimSpace(record.ArchiveURI) == "":
		return archiveBackendFilesystem
	default:
		return ""
	}
}

func deleteArchiveObjectsFromBucket(ctx context.Context, bucket *blob.Bucket, backend archiveBackendRuntime, testID string) error {
	if err := validateArchiveRunID(testID); err != nil {
		return err
	}
	if bucket == nil {
		return fmt.Errorf("archive object bucket is required")
	}
	prefix := strings.TrimSuffix(backend.objectKey(testID, ""), "/") + "/"
	iterator := bucket.List(&blob.ListOptions{Prefix: prefix})
	for {
		object, err := iterator.Next(ctx)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("list archive objects for %s: %w", testID, err)
		}
		if object == nil || !strings.HasPrefix(object.Key, prefix) {
			continue
		}
		if err := bucket.Delete(ctx, object.Key); err != nil {
			return fmt.Errorf("delete archive object %s: %w", object.Key, err)
		}
	}
}

// removeLocalArchiveDirectory refuses to recursively delete paths outside the
// configured archive roots. Archive paths are persisted data and must be
// treated as untrusted before passing them to os.RemoveAll.
func removeLocalArchiveDirectory(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	allowedRoots := []string{resolveArchiveRoot(), resolveFilesystemArchiveRoot()}
	if stagingRoot := strings.TrimSpace(os.Getenv("APEXLOAD_ARCHIVE_STAGING_DIR")); stagingRoot != "" {
		allowedRoots = append(allowedRoots, stagingRoot)
	}
	matchedRoot := false
	for _, root := range allowedRoots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		absRoot, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		resolvedRoot, err := resolveArchiveDirectoryRoot(absRoot, false)
		if err != nil {
			if os.IsNotExist(err) {
				// The path may still belong to this root; if no other root
				// holds it, report the missing root rather than an outside path.
				if _, within := archivePathBelowRoot(absRoot, absPath); within {
					matchedRoot = true
				}
				continue
			}
			return fmt.Errorf("resolve configured archive root %q: %w", root, err)
		}
		relative, within := archivePathBelowRoot(absRoot, absPath)
		if !within {
			relative, within = archivePathBelowRoot(resolvedRoot, absPath)
		}
		if !within {
			continue
		}
		relative, err = normalizeArchiveRelativeDirectory(relative)
		if err != nil || relative == "." {
			return fmt.Errorf("archive deletion path %q is not safe", path)
		}
		rootHandle, err := openArchiveDirectoryNoSymlink(resolvedRoot)
		if err != nil {
			return fmt.Errorf("inspect configured archive root %q: %w", root, err)
		}
		components := strings.Split(relative, string(filepath.Separator))
		for _, component := range components[:len(components)-1] {
			child, openErr := openArchiveChildDirectoryAt(rootHandle, component, false, 0)
			if openErr != nil {
				_ = rootHandle.Close()
				if os.IsNotExist(openErr) {
					return nil
				}
				return openErr
			}
			_ = rootHandle.Close()
			rootHandle = child
		}
		targetName := components[len(components)-1]
		targetRoot, openErr := openArchiveChildDirectoryAt(rootHandle, targetName, false, 0)
		if openErr != nil {
			_ = rootHandle.Close()
			if os.IsNotExist(openErr) {
				return nil
			}
			return openErr
		}
		targetIdentity, identityErr := targetRoot.Stat(".")
		_ = targetRoot.Close()
		if identityErr != nil {
			_ = rootHandle.Close()
			return identityErr
		}
		removeErr := removeArchiveDirectoryAt(rootHandle, targetName, targetIdentity)
		closeErr := rootHandle.Close()
		if removeErr != nil {
			return removeErr
		}
		return closeErr
	}
	if matchedRoot {
		return fmt.Errorf("configured archive root for %q does not exist", path)
	}
	return fmt.Errorf("archive path %q is outside configured archive roots", path)
}

// archivePathBelowRoot returns path relative to root when path names an entry
// strictly below root.
func archivePathBelowRoot(root, path string) (string, bool) {
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == "." || relative == "" || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", false
	}
	return relative, true
}

func ensureArchiveMaterialized(record *storage.RunArchiveRecord) error {
	return ensureArchiveMaterializedContext(context.Background(), record)
}

func ensureArchiveMaterializedContext(ctx context.Context, record *storage.RunArchiveRecord) error {
	lease, err := acquireArchiveMaterialized(ctx, record)
	if err != nil {
		return err
	}
	lease.Release()
	return nil
}

func acquireArchiveMaterialized(ctx context.Context, record *storage.RunArchiveRecord) (*archiveMaterializationLease, error) {
	if record == nil {
		return noOpArchiveMaterializationLease(), nil
	}
	recordedBackend := archiveRecordBackend(record)
	if recordedBackend == archiveBackendFilesystem {
		return noOpArchiveMaterializationLease(), nil
	}
	if recordedBackend == "" {
		return nil, fmt.Errorf("archive backend is missing or unsupported")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	backend, err := archiveMaterializationRuntime(currentArchiveBackend(), record)
	if err != nil {
		return nil, err
	}
	backend, err = archiveReaderRuntime(backend)
	if err != nil {
		return nil, err
	}
	localRoot, err := archiveMaterializationLocalRoot(backend, record)
	if err != nil {
		return nil, err
	}
	key, err := canonicalArchiveMaterializationKey(localRoot)
	if err != nil {
		return nil, err
	}
	boundedCtx, cancel := context.WithTimeout(ctx, configuredArchiveMaterializationTimeout())
	defer cancel()
	entry, err := archiveMaterializations.reserve(boundedCtx, key)
	if err != nil {
		return nil, fmt.Errorf("wait to materialize archive %q: %w", record.TestID, err)
	}
	reservationActive := true
	defer func() {
		if reservationActive {
			archiveMaterializations.failReservation(entry)
		}
	}()

	maximumBytes := configuredArchiveMaterializationMaxBytes()
	if err := archiveMaterializations.admitActiveBytes(boundedCtx, entry, maximumBytes, configuredArchiveActiveReaderMaxBytes()); err != nil {
		return nil, fmt.Errorf("admit archive %q reader: %w", record.TestID, err)
	}

	manifestPath := filepath.Join(localRoot, runArchiveManifestFileName)
	// Manifest presence is the materialization commit marker, but archives
	// produced by older controllers wrote it before their artifacts. Validate an
	// existing cache once more so a legacy partial download is repaired instead
	// of being treated as permanently complete.
	if _, err := os.Stat(manifestPath); err == nil {
		cachedRecord := *record
		cachedRecord.ArchivePath = localRoot
		cachedRecord.ManifestPath = manifestPath
		if err := validateAdmittedArchiveCache(boundedCtx, &cachedRecord, localRoot, manifestPath, maximumBytes); err == nil {
			setMaterializedArchiveRecordPaths(record, localRoot, manifestPath)
			touchArchiveMaterializationCacheMarker(localRoot, archiveMaterializations.now().UTC())
			lease := archiveMaterializations.promoteReservation(key, archiveMaterializationCacheScanRoot(backend.LocalRoot), localRoot, entry)
			reservationActive = false
			return lease, nil
		} else if boundedCtx.Err() != nil {
			return nil, boundedCtx.Err()
		}
	}
	// Cache hits may be read concurrently, but replacing a missing, stale, or
	// corrupt cache must wait for every active reader of the previous committed
	// directory. The keyed gate prevents another materializer from overtaking us.
	if err := archiveMaterializations.waitForNoReaders(boundedCtx, entry); err != nil {
		return nil, fmt.Errorf("wait for archive %q readers before materialization: %w", record.TestID, err)
	}
	bucket, err := backend.openBucket(boundedCtx)
	if err != nil {
		return nil, err
	}
	if err := materializeArchiveFromBucketWithLimit(boundedCtx, bucket, backend, record, maximumBytes); err != nil {
		_ = bucket.Close()
		return nil, err
	}
	if err := bucket.Close(); err != nil {
		return nil, fmt.Errorf("close archive object bucket: %w", err)
	}
	setMaterializedArchiveRecordPaths(record, localRoot, manifestPath)
	lease := archiveMaterializations.promoteReservation(key, archiveMaterializationCacheScanRoot(backend.LocalRoot), localRoot, entry)
	lease.downloaded = true
	reservationActive = false
	return lease, nil
}

func setMaterializedArchiveRecordPaths(record *storage.RunArchiveRecord, localRoot, manifestPath string) {
	// Paths become immutable after the first successful materialization. Avoid
	// same-value writes on later concurrent acquisitions so active readers of the
	// shared archive record do not race with cache-hit bookkeeping.
	if record.ArchivePath != localRoot {
		record.ArchivePath = localRoot
	}
	if record.ManifestPath != manifestPath {
		record.ManifestPath = manifestPath
	}
}

// archiveMaterializationRuntime resolves the object namespace from persisted
// archive metadata. The active controller configuration supplies credentials,
// endpoint and local staging settings only; changing its bucket/prefix must not
// silently redirect an old archive to a different object namespace.
func archiveMaterializationRuntime(configured archiveBackendRuntime, record *storage.RunArchiveRecord) (archiveBackendRuntime, error) {
	if record == nil {
		return archiveBackendRuntime{}, fmt.Errorf("archive record is required")
	}
	testID := record.TestID
	if err := validateArchiveRunID(testID); err != nil {
		return archiveBackendRuntime{}, err
	}
	recordedBackend := archiveRecordBackend(record)
	if recordedBackend == "" || recordedBackend == archiveBackendFilesystem {
		return archiveBackendRuntime{}, fmt.Errorf("archive record does not identify an object backend")
	}
	runURI := strings.TrimSpace(record.ArchiveURI)
	manifestURI := strings.TrimSpace(record.ManifestURI)
	if runURI == "" && manifestURI != "" {
		if !strings.HasSuffix(strings.TrimRight(manifestURI, "/"), "/manifest.json") {
			return archiveBackendRuntime{}, fmt.Errorf("persisted manifest URI is not a manifest object")
		}
		runURI = strings.TrimSuffix(strings.TrimRight(manifestURI, "/"), "/manifest.json")
	}
	if runURI == "" {
		return archiveBackendRuntime{}, fmt.Errorf("object archive %q has no persisted archive URI", testID)
	}
	parsed, err := url.Parse(runURI)
	if err != nil {
		return archiveBackendRuntime{}, fmt.Errorf("parse persisted archive URI: %w", err)
	}
	expectedScheme := map[string]string{
		archiveBackendS3:        "s3",
		archiveBackendAzureBlob: "azblob",
		archiveBackendGCS:       "gs",
	}[recordedBackend]
	if expectedScheme == "" || !strings.EqualFold(parsed.Scheme, expectedScheme) {
		return archiveBackendRuntime{}, fmt.Errorf("archive URI scheme %q does not match persisted backend %q", parsed.Scheme, recordedBackend)
	}
	if strings.TrimSpace(parsed.Host) == "" {
		return archiveBackendRuntime{}, fmt.Errorf("persisted archive URI has no object target")
	}
	prefix, err := archiveObjectRunPathPrefix(parsed, testID)
	if err != nil {
		return archiveBackendRuntime{}, fmt.Errorf("archive URI %q: %w", runURI, err)
	}
	if err := validatePersistedArchiveSource(configured, recordedBackend, parsed.Host, prefix); err != nil {
		return archiveBackendRuntime{}, err
	}
	if manifestURI != "" && strings.TrimRight(manifestURI, "/") != joinArchiveURI(strings.TrimRight(runURI, "/"), "manifest.json") {
		return archiveBackendRuntime{}, fmt.Errorf("persisted manifest URI does not belong to archive URI")
	}

	backend := configured
	backend.Backend = recordedBackend
	backend.Bucket = ""
	backend.Container = ""
	if recordedBackend == archiveBackendAzureBlob {
		backend.Container = parsed.Host
	} else {
		backend.Bucket = parsed.Host
	}
	backend.Prefix = prefix
	backend.URIPrefix = strings.TrimSuffix(runURI, "/"+testID)
	backend.LocalRoot = archiveObjectNamespaceRoot(objectArchiveStagingRoot(resolveFilesystemArchiveRoot(), recordedBackend), backend)
	return backend, nil
}

func validateArchiveRunID(testID string) error {
	if archivequota.IsReservedName(testID) {
		return fmt.Errorf("test id is reserved for archive backlog accounting")
	}
	if testID == "" {
		return fmt.Errorf("test id is required")
	}
	if testID != strings.TrimSpace(testID) || strings.ContainsRune(testID, 0) || strings.ContainsAny(testID, `/\\`) || testID == "." || testID == ".." || filepath.IsAbs(testID) || filepath.VolumeName(testID) != "" {
		return fmt.Errorf("test id %q is not a safe archive namespace", testID)
	}
	for _, r := range testID {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("test id %q is not a safe archive namespace", testID)
		}
	}
	return nil
}

// resolveArchiveDirectoryRoot returns a stable, absolute directory for an
// operator-configured archive root. Existing configured roots must be real
// directories rather than links. Missing tail components are created one at a
// time after resolving the deepest existing ancestor, so MkdirAll can never
// silently traverse a link planted inside the archive namespace.
func resolveArchiveDirectoryRoot(root string, create bool) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", fmt.Errorf("archive root is empty")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}

	cursor := filepath.Clean(absRoot)
	missing := make([]string, 0)
	for {
		info, statErr := os.Lstat(cursor)
		if statErr == nil {
			if cursor == absRoot && info.Mode()&os.ModeSymlink != 0 {
				return "", fmt.Errorf("configured archive root %q is a symbolic link", root)
			}
			resolved, resolveErr := filepath.EvalSymlinks(cursor)
			if resolveErr != nil {
				return "", fmt.Errorf("resolve archive root %q: %w", root, resolveErr)
			}
			resolvedInfo, resolveErr := os.Lstat(resolved)
			if resolveErr != nil {
				return "", resolveErr
			}
			if resolvedInfo.Mode()&os.ModeSymlink != 0 || !resolvedInfo.IsDir() {
				return "", fmt.Errorf("archive root component %q is not a regular directory", cursor)
			}
			rootHandle, openErr := openArchiveDirectoryNoSymlink(resolved)
			if openErr != nil {
				return "", openErr
			}
			currentPath := resolved
			for index := len(missing) - 1; index >= 0; index-- {
				component := missing[index]
				child, componentErr := openArchiveChildDirectoryAt(rootHandle, component, create, 0o755)
				if componentErr != nil {
					_ = rootHandle.Close()
					return "", componentErr
				}
				_ = rootHandle.Close()
				rootHandle = child
				currentPath = filepath.Join(currentPath, component)
			}
			if closeErr := rootHandle.Close(); closeErr != nil {
				return "", closeErr
			}
			return currentPath, nil
		}
		if !os.IsNotExist(statErr) {
			return "", statErr
		}
		parent := filepath.Dir(cursor)
		if parent == cursor {
			return "", statErr
		}
		missing = append(missing, filepath.Base(cursor))
		cursor = parent
	}
}

// openArchiveChildDirectoryAt opens one direct child through an already-held
// parent descriptor and proves that the entry observed before and after the
// open is the same directory. Callers can safely continue traversal through
// the returned Root even if an ancestor is concurrently renamed.
func openArchiveChildDirectoryAt(parent *os.Root, name string, create bool, perm os.FileMode) (*os.Root, error) {
	if parent == nil {
		return nil, fmt.Errorf("archive parent root is required")
	}
	if name == "" || name == "." || name == ".." || name != filepath.Base(name) || strings.ContainsAny(name, `/\\`) || strings.ContainsRune(name, 0) {
		return nil, fmt.Errorf("archive directory component %q is not safe", name)
	}
	info, err := parent.Lstat(name)
	if os.IsNotExist(err) && create {
		if mkdirErr := parent.Mkdir(name, perm); mkdirErr != nil && !os.IsExist(mkdirErr) {
			return nil, mkdirErr
		}
		info, err = parent.Lstat(name)
	}
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, fmt.Errorf("archive directory component %q is not a regular directory", name)
	}
	child, err := parent.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	opened, err := child.Stat(".")
	if err != nil {
		_ = child.Close()
		return nil, err
	}
	after, err := parent.Lstat(name)
	if err != nil || after.Mode()&os.ModeSymlink != 0 || !after.IsDir() || !os.SameFile(info, opened) || !os.SameFile(after, opened) {
		_ = child.Close()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("archive directory component %q changed while it was opened", name)
	}
	return child, nil
}

func openArchiveDirectoryNoSymlink(path string) (*os.Root, error) {
	infoBefore, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if infoBefore.Mode()&os.ModeSymlink != 0 || !infoBefore.IsDir() {
		return nil, fmt.Errorf("archive root is not a non-symlink directory: %q", path)
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	openedInfo, err := root.Stat(".")
	if err != nil {
		_ = root.Close()
		return nil, err
	}
	infoAfter, err := os.Lstat(path)
	if err != nil || infoAfter.Mode()&os.ModeSymlink != 0 || !infoAfter.IsDir() || !os.SameFile(infoBefore, openedInfo) || !os.SameFile(infoAfter, openedInfo) {
		_ = root.Close()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("archive directory %q changed while it was opened", path)
	}
	return root, nil
}

func normalizeArchiveRelativeDirectory(relative string) (string, error) {
	relative = filepath.Clean(strings.TrimSpace(relative))
	if relative == "." || relative == "" {
		return ".", nil
	}
	if filepath.IsAbs(relative) || filepath.VolumeName(relative) != "" || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("archive directory %q escapes its root", relative)
	}
	return relative, nil
}

func ensureArchiveRelativeDirectory(rootPath, relative string, perm os.FileMode) (string, error) {
	relative, err := normalizeArchiveRelativeDirectory(relative)
	if err != nil {
		return "", err
	}
	root, err := openArchiveDirectoryNoSymlink(rootPath)
	if err != nil {
		return "", err
	}
	defer root.Close()
	if relative == "." {
		return rootPath, nil
	}
	current := ""
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		if component == "" || component == "." || component == ".." {
			return "", fmt.Errorf("archive directory %q is not safe", relative)
		}
		current = filepath.Join(current, component)
		info, statErr := root.Lstat(current)
		switch {
		case statErr == nil:
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return "", fmt.Errorf("archive directory component %q is not a regular directory", current)
			}
		case os.IsNotExist(statErr):
			if mkdirErr := root.Mkdir(current, perm); mkdirErr != nil && !os.IsExist(mkdirErr) {
				return "", mkdirErr
			}
			info, statErr = root.Lstat(current)
			if statErr != nil {
				return "", statErr
			}
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return "", fmt.Errorf("archive directory component %q is not a regular directory", current)
			}
		default:
			return "", statErr
		}
	}
	return filepath.Join(rootPath, relative), nil
}

func inspectArchiveRelativeParents(root *os.Root, normalizedName string, create bool, perm os.FileMode) error {
	parent := filepath.Dir(filepath.FromSlash(normalizedName))
	if parent == "." {
		return nil
	}
	current := ""
	for _, component := range strings.Split(parent, string(filepath.Separator)) {
		if component == "" || component == "." || component == ".." {
			return fmt.Errorf("archive artifact parent %q is not safe", parent)
		}
		current = filepath.Join(current, component)
		info, err := root.Lstat(current)
		switch {
		case err == nil:
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return fmt.Errorf("archive artifact parent %q is not a regular directory", current)
			}
		case os.IsNotExist(err) && create:
			if mkdirErr := root.Mkdir(current, perm); mkdirErr != nil && !os.IsExist(mkdirErr) {
				return mkdirErr
			}
			info, err = root.Lstat(current)
			if err != nil {
				return err
			}
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return fmt.Errorf("archive artifact parent %q is not a regular directory", current)
			}
		default:
			return err
		}
	}
	return nil
}

func inspectArchiveArtifactTarget(root *os.Root, normalizedName string, allowMissing bool) (os.FileInfo, error) {
	info, err := root.Lstat(filepath.FromSlash(normalizedName))
	if os.IsNotExist(err) && allowMissing {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("archive artifact %q is not a regular file", normalizedName)
	}
	return info, nil
}

func archiveTemporaryName(parent, base string) (string, error) {
	random := make([]byte, 12)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	name := "." + base + ".tmp-" + hex.EncodeToString(random)
	if parent == "." || parent == "" {
		return name, nil
	}
	return filepath.Join(parent, name), nil
}

func createArchiveTemporaryDirectory(rootPath, prefix string, perm os.FileMode) (string, error) {
	if strings.ContainsAny(prefix, `/\\`) || prefix == "" {
		return "", fmt.Errorf("archive temporary directory prefix %q is not safe", prefix)
	}
	root, err := openArchiveDirectoryNoSymlink(rootPath)
	if err != nil {
		return "", err
	}
	defer root.Close()
	for attempt := 0; attempt < 100; attempt++ {
		name, err := archiveTemporaryName(".", prefix)
		if err != nil {
			return "", err
		}
		if err := root.Mkdir(name, perm); err != nil {
			if os.IsExist(err) {
				continue
			}
			return "", err
		}
		info, err := root.Lstat(name)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", fmt.Errorf("archive temporary directory %q is not a regular directory", name)
		}
		return filepath.Join(rootPath, name), nil
	}
	return "", fmt.Errorf("create unique archive temporary directory in %q", rootPath)
}

func removeArchiveSubdirectory(rootPath, targetPath string) error {
	absRoot, err := filepath.Abs(rootPath)
	if err != nil {
		return err
	}
	absTarget, err := filepath.Abs(targetPath)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(absRoot, absTarget)
	if err != nil || relative == "." || relative == "" || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("archive staging path %q is outside %q", targetPath, rootPath)
	}
	root, err := openArchiveDirectoryNoSymlink(absRoot)
	if err != nil {
		return err
	}
	defer root.Close()
	parent, name, closeParent, err := openArchiveRelativeParent(root, relative)
	if err != nil {
		return err
	}
	defer closeParent()
	info, err := parent.Lstat(name)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("archive staging path %q is not a regular directory", targetPath)
	}
	return removeArchiveDirectoryWithIdentity(parent, name, info)
}

// openArchiveRelativeParent traverses every parent component using directory
// descriptors and returns the descriptor/name pair for the final entry. The
// caller must invoke the returned cleanup function, including on error-free
// paths. The supplied root itself is never closed.
func openArchiveRelativeParent(root *os.Root, relative string) (*os.Root, string, func(), error) {
	if root == nil {
		return nil, "", func() {}, fmt.Errorf("archive root is required")
	}
	normalized, err := normalizeArchiveRelativeDirectory(relative)
	if err != nil {
		return nil, "", func() {}, err
	}
	if normalized == "." {
		return nil, "", func() {}, fmt.Errorf("archive root has no relative parent")
	}
	components := strings.Split(normalized, string(filepath.Separator))
	name := components[len(components)-1]
	parent := root
	var owned *os.Root
	cleanup := func() {
		if owned != nil {
			_ = owned.Close()
		}
	}
	for _, component := range components[:len(components)-1] {
		child, openErr := openArchiveChildDirectoryAt(parent, component, false, 0)
		if openErr != nil {
			cleanup()
			return nil, "", func() {}, openErr
		}
		if owned != nil {
			_ = owned.Close()
		}
		owned = child
		parent = child
	}
	return parent, name, cleanup, nil
}

func removeArchiveDirectoryWithIdentity(parent *os.Root, name string, expected os.FileInfo) error {
	if parent == nil || expected == nil {
		return fmt.Errorf("archive directory identity is required")
	}
	before, err := parent.Lstat(name)
	if err != nil {
		return err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.IsDir() || !os.SameFile(before, expected) {
		return fmt.Errorf("archive directory %q changed before deletion", name)
	}
	directory, err := openArchiveChildDirectoryAt(parent, name, false, 0)
	if err != nil {
		return err
	}
	if err := clearArchiveDirectoryRoot(directory); err != nil {
		_ = directory.Close()
		return err
	}
	if err := directory.Close(); err != nil {
		return err
	}
	after, err := parent.Lstat(name)
	if err != nil {
		return err
	}
	if after.Mode()&os.ModeSymlink != 0 || !after.IsDir() || !os.SameFile(after, expected) {
		return fmt.Errorf("archive directory %q changed during deletion", name)
	}
	return parent.Remove(name)
}

func clearArchiveDirectoryRoot(root *os.Root) error {
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	entries, readErr := directory.ReadDir(-1)
	closeErr := directory.Close()
	if readErr != nil {
		return readErr
	}
	if closeErr != nil {
		return closeErr
	}
	for _, entry := range entries {
		info, err := root.Lstat(entry.Name())
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			if err := root.Remove(entry.Name()); err != nil {
				return err
			}
			continue
		}
		if err := removeArchiveDirectoryWithIdentity(root, entry.Name(), info); err != nil {
			return err
		}
	}
	return nil
}

// replaceArchiveArtifactForWrite creates a fresh inode and atomically places
// it at name. It never opens the existing target with O_TRUNC, so a symlink
// swap cannot redirect truncation to another file.
func replaceArchiveArtifactForWrite(rootPath, name string, perm os.FileMode, createParents bool) (*os.File, error) {
	normalized, err := normalizeArchiveArtifactName(name)
	if err != nil {
		return nil, err
	}
	root, err := openArchiveDirectoryNoSymlink(rootPath)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if err := inspectArchiveRelativeParents(root, normalized, createParents, 0o755); err != nil {
		return nil, err
	}
	if _, err := inspectArchiveArtifactTarget(root, normalized, true); err != nil {
		return nil, err
	}
	tempName, err := archiveTemporaryName(filepath.Dir(filepath.FromSlash(normalized)), filepath.Base(normalized))
	if err != nil {
		return nil, err
	}
	file, err := root.OpenFile(tempName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		return nil, err
	}
	if err := root.Rename(tempName, filepath.FromSlash(normalized)); err != nil {
		_ = file.Close()
		_ = root.Remove(tempName)
		return nil, err
	}
	return file, nil
}

func createArchiveArtifactExclusive(rootPath, name string, perm os.FileMode, createParents bool) (*os.File, error) {
	normalized, err := normalizeArchiveArtifactName(name)
	if err != nil {
		return nil, err
	}
	root, err := openArchiveDirectoryNoSymlink(rootPath)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if err := inspectArchiveRelativeParents(root, normalized, createParents, 0o755); err != nil {
		return nil, err
	}
	if _, err := inspectArchiveArtifactTarget(root, normalized, true); err != nil {
		return nil, err
	}
	return root.OpenFile(filepath.FromSlash(normalized), os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
}

func openArchiveArtifactForRead(rootPath, name string) (*os.File, os.FileInfo, error) {
	root, err := openArchiveDirectoryNoSymlink(rootPath)
	if err != nil {
		return nil, nil, err
	}
	defer root.Close()
	return openArchiveArtifactForReadAt(root, name)
}

func loadRunArchiveManifestFromRecord(record *storage.RunArchiveRecord) (*RunArchiveManifest, error) {
	if record == nil {
		return nil, fmt.Errorf("archive record is required")
	}
	relative, err := filepath.Rel(record.ArchivePath, record.ManifestPath)
	if err != nil {
		return nil, err
	}
	name, err := normalizeArchiveArtifactName(filepath.ToSlash(relative))
	if err != nil || name != runArchiveManifestFileName {
		return nil, fmt.Errorf("archive manifest path is not the run manifest")
	}
	file, info, err := openArchiveArtifactForRead(record.ArchivePath, name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if info.Size() > maxArchiveManifestDownloadBytes {
		return nil, fmt.Errorf("archive manifest exceeds %d bytes", maxArchiveManifestDownloadBytes)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxArchiveManifestDownloadBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxArchiveManifestDownloadBytes {
		return nil, fmt.Errorf("archive manifest exceeds %d bytes", maxArchiveManifestDownloadBytes)
	}
	if expected := strings.TrimSpace(record.ManifestSHA256); expected != "" && !strings.EqualFold(sha256BytesHex(data), expected) {
		return nil, fmt.Errorf("archive manifest SHA-256 does not match persisted metadata")
	}
	var manifest RunArchiveManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}
	return &manifest, nil
}

func openArchiveArtifactForReadAt(root *os.Root, name string) (*os.File, os.FileInfo, error) {
	if root == nil {
		return nil, nil, fmt.Errorf("archive root is required")
	}
	normalized, err := normalizeArchiveArtifactName(name)
	if err != nil {
		return nil, nil, err
	}
	if err := inspectArchiveRelativeParents(root, normalized, false, 0); err != nil {
		return nil, nil, err
	}
	info, err := inspectArchiveArtifactTarget(root, normalized, false)
	if err != nil {
		return nil, nil, err
	}
	file, err := root.Open(filepath.FromSlash(normalized))
	if err != nil {
		return nil, nil, err
	}
	openedInfo, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, nil, err
	}
	after, err := root.Lstat(filepath.FromSlash(normalized))
	if err != nil || after.Mode()&os.ModeSymlink != 0 || !after.Mode().IsRegular() || !os.SameFile(info, openedInfo) || !os.SameFile(after, openedInfo) {
		_ = file.Close()
		if err != nil {
			return nil, nil, err
		}
		return nil, nil, fmt.Errorf("archive artifact %q changed while it was opened", normalized)
	}
	return file, openedInfo, nil
}

func validateArchiveArtifactTargets(rootPath string, names ...string) error {
	root, err := openArchiveDirectoryNoSymlink(rootPath)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, name := range names {
		if strings.TrimSpace(name) == "" {
			continue
		}
		normalized, err := normalizeArchiveArtifactName(name)
		if err != nil {
			return err
		}
		if err := inspectArchiveRelativeParents(root, normalized, false, 0); err != nil {
			return err
		}
		if _, err := inspectArchiveArtifactTarget(root, normalized, true); err != nil {
			return err
		}
	}
	return nil
}

func materializeArchiveFromBucket(ctx context.Context, bucket *blob.Bucket, backend archiveBackendRuntime, record *storage.RunArchiveRecord) error {
	return materializeArchiveFromBucketWithLimit(ctx, bucket, backend, record, configuredArchiveMaterializationMaxBytes())
}

func materializeArchiveFromBucketWithLimit(ctx context.Context, bucket *blob.Bucket, backend archiveBackendRuntime, record *storage.RunArchiveRecord, maxBytes int64) error {
	if bucket == nil {
		return fmt.Errorf("archive object bucket is required")
	}
	if record == nil {
		return fmt.Errorf("archive record is required")
	}
	if err := validateArchiveRunID(record.TestID); err != nil {
		return err
	}
	canonicalRoot, err := resolveArchiveDirectoryRoot(backend.LocalRoot, true)
	if err != nil {
		return fmt.Errorf("prepare archive cache root: %w", err)
	}
	backend.LocalRoot = canonicalRoot
	localRoot, err := archiveMaterializationLocalRoot(backend, record)
	if err != nil {
		return err
	}
	parent := filepath.Dir(localRoot)
	tempRoot, err := createArchiveTemporaryDirectory(parent, filepath.Base(localRoot)+".materializing", 0o755)
	if err != nil {
		return fmt.Errorf("create archive materialization temp dir: %w", err)
	}
	keepTemp := false
	defer func() {
		if !keepTemp {
			_ = removeArchiveSubdirectory(parent, tempRoot)
		}
	}()

	manifestData, err := readArchiveObjectLimited(ctx, bucket, backend.objectKey(record.TestID, runArchiveManifestFileName), maxArchiveManifestDownloadBytes)
	if err != nil {
		return fmt.Errorf("download archive manifest: %w", err)
	}
	if expected := strings.TrimSpace(record.ManifestSHA256); expected != "" && !strings.EqualFold(sha256BytesHex(manifestData), expected) {
		return fmt.Errorf("downloaded archive manifest SHA-256 does not match persisted metadata")
	}
	manifest, artifacts, err := decodeMaterializationManifest(record, manifestData)
	if err != nil {
		return err
	}
	totalBytes := int64(len(manifestData))
	if totalBytes > maxBytes {
		return fmt.Errorf("archive manifest exceeds materialization byte limit %d", maxBytes)
	}
	for _, artifact := range artifacts {
		if artifact == runArchiveManifestFileName {
			continue
		}
		written, err := downloadArchiveObjectToFile(ctx, bucket, backend.objectKey(record.TestID, artifact), tempRoot, artifact, maxBytes-totalBytes)
		if err != nil {
			return fmt.Errorf("download archive artifact %s: %w", artifact, err)
		}
		totalBytes += written
	}

	// The manifest is intentionally the final file written. Its presence is the
	// cache's commit marker, so a canceled or failed artifact download cannot make
	// a partial directory look complete.
	tempManifest := filepath.Join(tempRoot, runArchiveManifestFileName)
	if err := writeSyncedArchiveFile(tempManifest, manifestData); err != nil {
		return fmt.Errorf("write archive manifest: %w", err)
	}
	tempRecord := *record
	tempRecord.ArchivePath = tempRoot
	tempRecord.ManifestPath = tempManifest
	if err := validateMaterializedArchiveWithManifest(&tempRecord, tempRoot, tempManifest, manifest); err != nil {
		return fmt.Errorf("verify materialized archive: %w", err)
	}
	// Cache ownership metadata lives inside the staged directory so the archive
	// and its retention marker commit in the same atomic rename. Producer archive
	// directories never receive this marker and therefore remain non-evictable.
	if err := writeArchiveMaterializationCacheMarker(tempRoot, totalBytes, time.Now().UTC()); err != nil {
		return fmt.Errorf("record archive materialization cache entry: %w", err)
	}
	if err := syncArchiveDirectory(tempRoot); err != nil {
		return fmt.Errorf("sync materialized archive: %w", err)
	}
	if err := atomicReplaceArchiveDirectory(tempRoot, localRoot); err != nil {
		return err
	}
	keepTemp = true // tempRoot was renamed to localRoot.
	record.ArchivePath = localRoot
	record.ManifestPath = filepath.Join(localRoot, runArchiveManifestFileName)
	return nil
}

func archiveMaterializationLocalRoot(backend archiveBackendRuntime, record *storage.RunArchiveRecord) (string, error) {
	if record == nil {
		return "", fmt.Errorf("archive record is required")
	}
	if err := validateArchiveRunID(record.TestID); err != nil {
		return "", err
	}
	root := strings.TrimSpace(backend.LocalRoot)
	if root == "" {
		return "", fmt.Errorf("archive materialization root is empty")
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	return filepath.Join(rootAbs, record.TestID), nil
}

func readArchiveObjectLimited(ctx context.Context, bucket *blob.Bucket, key string, limit int64) ([]byte, error) {
	reader, err := bucket.NewReader(ctx, key, nil)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("archive manifest exceeds %d bytes", limit)
	}
	return data, nil
}

func decodeMaterializationManifest(record *storage.RunArchiveRecord, data []byte) (*RunArchiveManifest, []string, error) {
	var manifest RunArchiveManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, nil, fmt.Errorf("decode archive manifest: %w", err)
	}
	if err := validateRunArchiveManifestArtifacts(&manifest); err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(manifest.TestID) == "" || manifest.TestID != record.TestID {
		return nil, nil, fmt.Errorf("archive manifest test id %q does not match record %q", manifest.TestID, record.TestID)
	}
	names, err := materializationArtifactNames(&manifest)
	if err != nil {
		return nil, nil, err
	}
	return &manifest, names, nil
}

func materializationArtifactNames(manifest *RunArchiveManifest) ([]string, error) {
	named, err := archiveManifestArtifactNames(manifest)
	if err != nil {
		return nil, err
	}
	for _, descriptor := range manifest.Artifacts {
		if descriptor.SizeBytes < 0 {
			return nil, fmt.Errorf("archive artifact %q has a negative size", descriptor.Name)
		}
		name, err := requiredArchiveArtifactName("artifacts.name", descriptor.Name)
		if err != nil {
			return nil, err
		}
		named[name] = struct{}{}
	}
	if len(named) > maxArchiveMaterializedArtifacts {
		return nil, fmt.Errorf("archive manifest declares %d artifacts, limit is %d", len(named), maxArchiveMaterializedArtifacts)
	}
	artifacts := make([]string, 0, len(named))
	for name := range named {
		artifacts = append(artifacts, name)
	}
	sort.Strings(artifacts)
	return artifacts, nil
}

func validateMaterializedArchive(record *storage.RunArchiveRecord, root, manifestPath string) error {
	return validateMaterializedArchiveContext(context.Background(), record, root, manifestPath)
}

func validateMaterializedArchiveContext(ctx context.Context, record *storage.RunArchiveRecord, root, manifestPath string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := readLocalFileLimitedContext(ctx, manifestPath, maxArchiveManifestDownloadBytes)
	if err != nil {
		return err
	}
	manifest, _, err := decodeMaterializationManifest(record, data)
	if err != nil {
		return err
	}
	return validateMaterializedArchiveWithManifestContext(ctx, record, root, manifestPath, manifest)
}

func validateMaterializedArchiveWithManifest(record *storage.RunArchiveRecord, root, manifestPath string, manifest *RunArchiveManifest) error {
	return validateMaterializedArchiveWithManifestContext(context.Background(), record, root, manifestPath, manifest)
}

func validateMaterializedArchiveWithManifestContext(ctx context.Context, record *storage.RunArchiveRecord, root, manifestPath string, manifest *RunArchiveManifest) error {
	if strings.TrimSpace(root) == "" {
		return fmt.Errorf("archive root is empty")
	}
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return fmt.Errorf("archive root is not a regular directory")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if expected := strings.TrimSpace(record.ManifestSHA256); expected != "" {
		actual, err := sha256FileContext(ctx, manifestPath)
		if err != nil {
			return err
		}
		if !strings.EqualFold(actual, expected) {
			return fmt.Errorf("archive manifest SHA-256 does not match persisted metadata")
		}
	}
	names, err := materializationArtifactNames(manifest)
	if err != nil {
		return err
	}
	descriptors := make(map[string]RunArchiveArtifact, len(manifest.Artifacts))
	for _, descriptor := range manifest.Artifacts {
		name, err := requiredArchiveArtifactName("artifacts.name", descriptor.Name)
		if err != nil {
			return err
		}
		if _, duplicate := descriptors[name]; duplicate {
			return fmt.Errorf("archive manifest contains duplicate artifact %q", name)
		}
		descriptor.Name = name
		descriptors[name] = descriptor
	}
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return err
		}
		if name == runArchiveManifestFileName {
			continue
		}
		path, err := archiveArtifactPath(root, name)
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("archive artifact %s is missing: %w", name, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("archive artifact %s is not a regular file", name)
		}
		descriptor, described := descriptors[name]
		if !described {
			continue
		}
		if descriptor.SizeBytes > 0 || strings.TrimSpace(descriptor.SHA256) != "" {
			if info.Size() != descriptor.SizeBytes {
				return fmt.Errorf("archive artifact %s size %d does not match expected %d", name, info.Size(), descriptor.SizeBytes)
			}
		}
		if expected := strings.TrimSpace(descriptor.SHA256); expected != "" {
			actual, err := sha256FileContext(ctx, path)
			if err != nil {
				return err
			}
			if !strings.EqualFold(actual, expected) {
				return fmt.Errorf("archive artifact %s SHA-256 does not match manifest", name)
			}
		}
	}
	return nil
}

func writeSyncedArchiveFile(path string, data []byte) error {
	file, err := createArchiveArtifactExclusive(filepath.Dir(path), filepath.Base(path), 0o644, false)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func syncArchiveDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return syncArchiveDirectoryHandle(dir)
}

func atomicReplaceArchiveDirectory(tempRoot, finalRoot string) error {
	absTemp, err := filepath.Abs(tempRoot)
	if err != nil {
		return err
	}
	absFinal, err := filepath.Abs(finalRoot)
	if err != nil {
		return err
	}
	parent := filepath.Dir(absFinal)
	if filepath.Dir(absTemp) != parent {
		return fmt.Errorf("archive staging directory must share the final archive parent")
	}
	root, err := openArchiveDirectoryNoSymlink(parent)
	if err != nil {
		return err
	}
	defer root.Close()
	tempName := filepath.Base(absTemp)
	finalName := filepath.Base(absFinal)
	tempInfo, err := root.Lstat(tempName)
	if err != nil {
		return err
	}
	if tempInfo.Mode()&os.ModeSymlink != 0 || !tempInfo.IsDir() {
		return fmt.Errorf("archive staging root is not a regular directory")
	}

	backupName := ""
	if finalInfo, statErr := root.Lstat(finalName); statErr == nil {
		if finalInfo.Mode()&os.ModeSymlink != 0 || !finalInfo.IsDir() {
			return fmt.Errorf("existing archive run root %q is not a non-symlink directory", finalRoot)
		}
		backupName, err = archiveTemporaryName(".", finalName+".previous")
		if err != nil {
			return err
		}
		if err := root.Rename(finalName, backupName); err != nil {
			return fmt.Errorf("move previous archive cache: %w", err)
		}
	} else if !os.IsNotExist(statErr) {
		return statErr
	}
	if err := root.Rename(tempName, finalName); err != nil {
		if backupName != "" {
			_ = root.Rename(backupName, finalName)
		}
		return fmt.Errorf("commit materialized archive: %w", err)
	}
	if backupName != "" {
		backupInfo, err := root.Lstat(backupName)
		if err != nil {
			return err
		}
		if err := removeArchiveDirectoryWithIdentity(root, backupName, backupInfo); err != nil {
			return fmt.Errorf("remove previous archive cache: %w", err)
		}
	}
	if err := syncArchiveDirectory(parent); err != nil {
		return err
	}
	return nil
}

func downloadArchiveObjectToFile(ctx context.Context, bucket *blob.Bucket, key string, localRoot string, name string, limit int64) (int64, error) {
	if limit < 0 {
		return 0, fmt.Errorf("archive materialization byte limit exceeded")
	}
	reader, err := bucket.NewReader(ctx, key, nil)
	if err != nil {
		return 0, err
	}
	defer reader.Close()
	file, err := createArchiveArtifactExclusive(localRoot, name, 0o644, true)
	if err != nil {
		return 0, err
	}
	written, copyErr := io.Copy(file, io.LimitReader(reader, limit+1))
	if copyErr != nil {
		_ = file.Close()
		return written, copyErr
	}
	if written > limit {
		_ = file.Close()
		return written, fmt.Errorf("archive materialization exceeds configured byte limit")
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return written, err
	}
	if err := file.Close(); err != nil {
		return written, err
	}
	return written, nil
}

func configuredArchiveMaterializationMaxBytes() int64 {
	raw := strings.TrimSpace(os.Getenv(envArchiveMaterializedMaxBytes))
	if raw == "" {
		return defaultArchiveMaterializedBytes
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return defaultArchiveMaterializedBytes
	}
	return value
}

func configuredArchiveMaterializationTimeout() time.Duration {
	raw := strings.TrimSpace(os.Getenv(envArchiveMaterializeTimeout))
	if raw == "" {
		return defaultArchiveMaterializeTimeout
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		return defaultArchiveMaterializeTimeout
	}
	return value
}

func (b archiveBackendRuntime) validate() error {
	switch b.Backend {
	case archiveBackendFilesystem:
		return nil
	case archiveBackendS3, archiveBackendGCS:
		if strings.TrimSpace(b.Bucket) == "" {
			return fmt.Errorf("archive backend %s requires APEXLOAD_ARCHIVE_BUCKET", b.Backend)
		}
	case archiveBackendAzureBlob:
		if strings.TrimSpace(b.Container) == "" {
			return fmt.Errorf("archive backend azure_blob requires APEXLOAD_ARCHIVE_CONTAINER")
		}
	default:
		return fmt.Errorf("unsupported archive backend %q", b.Backend)
	}
	return nil
}

func (b archiveBackendRuntime) readinessDetails() map[string]any {
	details := map[string]any{
		"archive_backend": b.Backend,
		"archive_root":    b.LocalRoot,
	}
	if b.URIPrefix != "" {
		details["archive_uri_prefix"] = b.URIPrefix
	}
	if b.Region != "" {
		details["region"] = b.Region
	}
	if b.Endpoint != "" {
		details["endpoint_configured"] = true
	}
	return details
}

func configuredMetricsRetention() string {
	if value := strings.TrimSpace(os.Getenv("APEXLOAD_METRICS_RETENTION_BACKEND")); value != "" {
		return value
	}
	if strings.TrimSpace(os.Getenv("APEXLOAD_PROMETHEUS_REMOTE_WRITE_URL")) != "" {
		return "prometheus_remote_write"
	}
	return "local_scrape"
}
