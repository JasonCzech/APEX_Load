package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/jasonczech/stormrunner/pkg/storage"
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
	backend := strings.TrimSpace(strings.ToLower(os.Getenv("STORMRUNNER_ARCHIVE_BACKEND")))
	if backend == "" {
		backend = archiveBackendFilesystem
	}
	filesystemRoot := resolveFilesystemArchiveRoot()
	prefix := cleanArchivePrefix(os.Getenv("STORMRUNNER_ARCHIVE_PREFIX"))
	runtime := archiveBackendRuntime{
		Backend:   backend,
		LocalRoot: filesystemRoot,
		Prefix:    prefix,
		Region:    strings.TrimSpace(os.Getenv("STORMRUNNER_ARCHIVE_REGION")),
		Endpoint:  strings.TrimSpace(os.Getenv("STORMRUNNER_ARCHIVE_ENDPOINT")),
	}
	switch backend {
	case archiveBackendS3:
		runtime.Bucket = strings.TrimSpace(os.Getenv("STORMRUNNER_ARCHIVE_BUCKET"))
		runtime.LocalRoot = objectArchiveStagingRoot(filesystemRoot, backend)
		runtime.URIPrefix = joinArchiveURI("s3://"+runtime.Bucket, prefix)
	case archiveBackendAzureBlob:
		runtime.Container = firstNonEmpty(strings.TrimSpace(os.Getenv("STORMRUNNER_ARCHIVE_CONTAINER")), strings.TrimSpace(os.Getenv("STORMRUNNER_ARCHIVE_BUCKET")))
		runtime.LocalRoot = objectArchiveStagingRoot(filesystemRoot, backend)
		runtime.URIPrefix = joinArchiveURI("azblob://"+runtime.Container, prefix)
	case archiveBackendGCS:
		runtime.Bucket = strings.TrimSpace(os.Getenv("STORMRUNNER_ARCHIVE_BUCKET"))
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
	if dir := os.Getenv("STORMRUNNER_ARCHIVE_DIR"); strings.TrimSpace(dir) != "" {
		return dir
	}
	return filepath.Join("artifacts", "archives")
}

func objectArchiveStagingRoot(filesystemRoot string, backend string) string {
	if dir := strings.TrimSpace(os.Getenv("STORMRUNNER_ARCHIVE_STAGING_DIR")); dir != "" {
		return dir
	}
	return filepath.Join(filesystemRoot, "_object_staging", backend)
}

func cleanArchivePrefix(prefix string) string {
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")
	if prefix == "" {
		return "stormrunner/archives"
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
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		key := backend.objectKey(testID, filepath.ToSlash(relative))
		if err := bucket.WriteAll(ctx, key, data, nil); err != nil {
			return fmt.Errorf("upload archive object %s: %w", key, err)
		}
		return nil
	})
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
	if err := os.WriteFile(record.ManifestPath, manifestData, 0o644); err != nil {
		return err
	}
	var manifest RunArchiveManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return err
	}
	for artifact := range archiveManifestArtifactNames(&manifest) {
		if artifact == "manifest.json" {
			continue
		}
		data, err := bucket.ReadAll(ctx, backend.objectKey(record.TestID, artifact))
		if err != nil {
			return fmt.Errorf("download archive artifact %s: %w", artifact, err)
		}
		localPath := filepath.Join(localRoot, filepath.FromSlash(artifact))
		if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(localPath, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func (b archiveBackendRuntime) validate() error {
	switch b.Backend {
	case archiveBackendFilesystem:
		return nil
	case archiveBackendS3, archiveBackendGCS:
		if strings.TrimSpace(b.Bucket) == "" {
			return fmt.Errorf("archive backend %s requires STORMRUNNER_ARCHIVE_BUCKET", b.Backend)
		}
	case archiveBackendAzureBlob:
		if strings.TrimSpace(b.Container) == "" {
			return fmt.Errorf("archive backend azure_blob requires STORMRUNNER_ARCHIVE_CONTAINER")
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
	if value := strings.TrimSpace(os.Getenv("STORMRUNNER_METRICS_RETENTION_BACKEND")); value != "" {
		return value
	}
	if strings.TrimSpace(os.Getenv("STORMRUNNER_PROMETHEUS_REMOTE_WRITE_URL")) != "" {
		return "prometheus_remote_write"
	}
	return "local_scrape"
}
