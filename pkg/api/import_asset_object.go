package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/fileasset"
	"github.com/jasonczech/apexload/pkg/storage"
	"gocloud.dev/blob"
)

type importAssetObjectReader struct {
	io.ReadCloser
	bucket *blob.Bucket
}

func (r *importAssetObjectReader) Close() error {
	readErr := r.ReadCloser.Close()
	bucketErr := r.bucket.Close()
	if readErr != nil {
		return readErr
	}
	return bucketErr
}

func (s *Server) configureImportAssetObjectStore() {
	if s == nil || s.importAssets == nil || currentArchiveBackend().Backend == archiveBackendFilesystem {
		return
	}
	if err := fileasset.RegisterStorageRefOpener("asset", s.openImportAssetStorageRef); err != nil {
		slog.Warn("failed to register object-backed import asset resolver", "error", err)
	}
}

func importAssetStorageRef(projectID, assetID string) (string, error) {
	namespace, err := fileasset.ProjectAssetNamespace(projectID)
	if err != nil {
		return "", err
	}
	return (&url.URL{Scheme: "asset", Host: namespace, Path: "/" + strings.TrimSpace(assetID)}).String(), nil
}

func parseImportAssetStorageRef(ref string) (namespace, id string, err error) {
	parsed, err := url.Parse(strings.TrimSpace(ref))
	if err != nil || parsed.Scheme != "asset" || parsed.Host == "" {
		return "", "", fmt.Errorf("invalid asset storage_ref")
	}
	id = strings.Trim(parsed.Path, "/")
	if id == "" || strings.Contains(id, "/") {
		return "", "", fmt.Errorf("invalid asset identity")
	}
	return parsed.Host, id, nil
}

func (s *Server) openImportAssetStorageRef(ref string, maxBytes int64) (io.ReadCloser, error) {
	if s == nil || s.importAssets == nil || maxBytes <= 0 {
		return nil, fmt.Errorf("object-backed import assets are unavailable")
	}
	namespace, id, err := parseImportAssetStorageRef(ref)
	if err != nil {
		return nil, err
	}
	asset, err := s.importAssets.GetImportAsset(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("import asset is unavailable")
		}
		return nil, err
	}
	wantNamespace, err := fileasset.ProjectAssetNamespace(asset.ProjectID)
	if err != nil || namespace != wantNamespace || asset.State != storage.ImportAssetReady {
		return nil, fmt.Errorf("import asset is unavailable")
	}
	if asset.SizeBytes > maxBytes {
		return nil, fmt.Errorf("import asset exceeds %d byte limit", maxBytes)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	bucket, err := currentArchiveBackend().openBucket(ctx)
	if err != nil {
		return nil, err
	}
	attributes, err := bucket.Attributes(ctx, asset.ObjectKey)
	if err != nil {
		_ = bucket.Close()
		return nil, err
	}
	if attributes.Size > maxBytes {
		_ = bucket.Close()
		return nil, fmt.Errorf("import asset exceeds %d byte limit", maxBytes)
	}
	reader, err := bucket.NewReader(context.Background(), asset.ObjectKey, nil)
	if err != nil {
		_ = bucket.Close()
		return nil, err
	}
	return &importAssetObjectReader{ReadCloser: reader, bucket: bucket}, nil
}

func (s *Server) storeImportAssetObject(ctx context.Context, projectID, importID, digest, contentType string, data []byte) (string, error) {
	if s == nil || s.importAssets == nil {
		return "", fmt.Errorf("import asset metadata storage is unavailable")
	}
	backend := currentArchiveBackend()
	if backend.Backend == archiveBackendFilesystem {
		return "", fmt.Errorf("object storage is not configured")
	}
	asset := &storage.ImportAsset{
		ID: uuid.NewString(), ProjectID: projectID, ImportID: importID, SHA256: digest,
		SizeBytes: int64(len(data)), ContentType: contentType, State: storage.ImportAssetUploading, Revision: 1,
	}
	namespace, err := fileasset.ProjectAssetNamespace(projectID)
	if err != nil {
		return "", err
	}
	asset.ObjectKey = joinArchiveURI(backend.Prefix, "import-assets", "v1", namespace, asset.ID, digest)
	if err := s.importAssets.CreateImportAsset(asset); err != nil {
		return "", err
	}
	bucket, err := backend.openBucket(ctx)
	if err == nil {
		err = bucket.WriteAll(ctx, asset.ObjectKey, data, &blob.WriterOptions{ContentType: contentType})
		_ = bucket.Close()
	}
	expected := asset.Revision
	asset.Revision++
	if err != nil {
		asset.State, asset.LastError = storage.ImportAssetFailed, err.Error()
		_, _ = s.importAssets.TransitionImportAsset(asset, expected)
		return "", err
	}
	asset.State, asset.LastError = storage.ImportAssetReady, ""
	changed, err := s.importAssets.TransitionImportAsset(asset, expected)
	if err != nil || !changed {
		return "", fmt.Errorf("object uploaded but import asset readiness CAS failed: %w", err)
	}
	return importAssetStorageRef(projectID, asset.ID)
}
