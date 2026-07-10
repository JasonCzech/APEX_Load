package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/storage"
	"gocloud.dev/blob"
	_ "gocloud.dev/blob/azureblob"
	_ "gocloud.dev/blob/fileblob"
	_ "gocloud.dev/blob/gcsblob"
	_ "gocloud.dev/blob/s3blob"
)

const (
	archiveBackendFilesystem = "filesystem"
	archiveBackendS3         = "s3"
	archiveBackendAzureBlob  = "azure_blob"
	archiveBackendGCS        = "gcs"
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
	}
	switch backend {
	case archiveBackendS3:
		runtime.Bucket = strings.TrimSpace(os.Getenv("APEXLOAD_ARCHIVE_BUCKET"))
		runtime.LocalRoot = objectArchiveStagingRoot(filesystemRoot, backend)
		runtime.URIPrefix = joinArchiveURI("s3://"+runtime.Bucket, prefix)
	case archiveBackendAzureBlob:
		runtime.Container = firstNonEmpty(strings.TrimSpace(os.Getenv("APEXLOAD_ARCHIVE_CONTAINER")), strings.TrimSpace(os.Getenv("APEXLOAD_ARCHIVE_BUCKET")))
		runtime.LocalRoot = objectArchiveStagingRoot(filesystemRoot, backend)
		runtime.URIPrefix = joinArchiveURI("azblob://"+runtime.Container, prefix)
	case archiveBackendGCS:
		runtime.Bucket = strings.TrimSpace(os.Getenv("APEXLOAD_ARCHIVE_BUCKET"))
		runtime.LocalRoot = objectArchiveStagingRoot(filesystemRoot, backend)
		runtime.URIPrefix = joinArchiveURI("gs://"+runtime.Bucket, prefix)
	default:
		runtime.Backend = archiveBackendFilesystem
	}
	return runtime
}

func resolveArchiveRoot() string {
	return currentArchiveBackend().LocalRoot
}

func resolveFilesystemArchiveRoot() string {
	return config.ArchiveDir()
}

func objectArchiveStagingRoot(filesystemRoot string, backend string) string {
	if dir := strings.TrimSpace(os.Getenv("APEXLOAD_ARCHIVE_STAGING_DIR")); dir != "" {
		return dir
	}
	return filepath.Join(filesystemRoot, "_object_staging", backend)
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
	backend := currentArchiveBackend()
	if backend.Backend == archiveBackendFilesystem {
		return nil
	}
	bucket, err := backend.openBucket(ctx)
	if err != nil {
		return err
	}
	defer bucket.Close()
	return filepath.WalkDir(localDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(localDir, path)
		if err != nil {
			return err
		}
		key := backend.objectKey(testID, filepath.ToSlash(relative))
		if err := uploadArchiveFileToBucket(ctx, bucket, key, path); err != nil {
			return fmt.Errorf("upload archive object %s: %w", key, err)
		}
		return nil
	})
}

func uploadArchiveObjects(ctx context.Context, localDir string, testID string, names ...string) error {
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
	if bucket == nil {
		return fmt.Errorf("archive object bucket is required")
	}
	seen := map[string]bool{}
	for _, name := range names {
		name = strings.TrimSpace(filepath.ToSlash(name))
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		localPath, err := archiveArtifactPath(localDir, name)
		if err != nil {
			return err
		}
		key := backend.objectKey(testID, name)
		if err := uploadArchiveFileToBucket(ctx, bucket, key, localPath); err != nil {
			return fmt.Errorf("upload archive object %s: %w", key, err)
		}
	}
	return nil
}

func uploadArchiveFileToBucket(ctx context.Context, bucket *blob.Bucket, key string, localPath string) error {
	file, err := os.Open(localPath)
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

func ensureArchiveMaterialized(record *storage.RunArchiveRecord) error {
	if record == nil {
		return nil
	}
	if strings.TrimSpace(record.ManifestPath) != "" {
		if _, err := os.Stat(record.ManifestPath); err == nil {
			return nil
		}
	}
	backend := currentArchiveBackend()
	if backend.Backend == archiveBackendFilesystem {
		return nil
	}
	ctx := context.Background()
	bucket, err := backend.openBucket(ctx)
	if err != nil {
		return err
	}
	defer bucket.Close()
	localRoot := strings.TrimSpace(record.ArchivePath)
	if localRoot == "" {
		localRoot = filepath.Join(backend.LocalRoot, record.TestID)
		record.ArchivePath = localRoot
	}
	if err := os.MkdirAll(localRoot, 0o755); err != nil {
		return err
	}
	manifestData, err := bucket.ReadAll(ctx, backend.objectKey(record.TestID, "manifest.json"))
	if err != nil {
		return fmt.Errorf("download archive manifest: %w", err)
	}
	if strings.TrimSpace(record.ManifestPath) == "" {
		record.ManifestPath = filepath.Join(localRoot, "manifest.json")
	}
	var manifest RunArchiveManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return err
	}
	artifacts, err := archiveManifestArtifactNames(&manifest)
	if err != nil {
		return err
	}
	for artifact := range artifacts {
		if artifact == "manifest.json" {
			continue
		}
		localPath, err := archiveArtifactPath(localRoot, artifact)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
			return err
		}
		if err := downloadArchiveObjectToFile(ctx, bucket, backend.objectKey(record.TestID, artifact), localPath); err != nil {
			return fmt.Errorf("download archive artifact %s: %w", artifact, err)
		}
	}
	// Write manifest.json LAST and atomically, so the "manifest.json exists"
	// idempotency guard at the top only short-circuits once every artifact is
	// fully materialized. If any artifact download above failed the manifest is
	// never written, forcing a clean re-materialization instead of serving a
	// truncated transactions.jsonl as if the archive were complete.
	if err := writeFileAtomic(record.ManifestPath, manifestData, 0o644); err != nil {
		return err
	}
	return nil
}

func downloadArchiveObjectToFile(ctx context.Context, bucket *blob.Bucket, key string, localPath string) error {
	reader, err := bucket.NewReader(ctx, key, nil)
	if err != nil {
		return err
	}
	defer reader.Close()
	// Stream to a temp file and atomically rename on success so a mid-stream
	// failure (transient network error mid-download) never leaves a truncated
	// file at the final path. A line-boundary truncation would otherwise be
	// consumed silently by the JSONL readers as a complete-but-short artifact.
	tmp, err := os.CreateTemp(filepath.Dir(localPath), ".apexload-archive-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		tmp.Close()
		os.Remove(tmpPath) // no-op after a successful rename
	}()
	if _, err := io.Copy(tmp, reader); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, localPath)
}

// writeFileAtomic writes data to a temp file in the destination directory and
// atomically renames it into place, so a crash or error mid-write never leaves a
// partial file at path.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".apexload-tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		tmp.Close()
		os.Remove(tmpPath) // no-op after a successful rename
	}()
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
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
