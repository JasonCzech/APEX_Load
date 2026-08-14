package storage

import (
	"errors"
	"strings"
	"time"
)

var (
	_ ImportAssetStore = (*SQLiteStore)(nil)
	_ ImportAssetStore = (*PostgresStore)(nil)
)

const importAssetSelect = `SELECT id, project_id, import_id, object_key, sha256, size_bytes,
	COALESCE(content_type, ''), state, COALESCE(last_error, ''), revision, created_at, updated_at
	FROM import_assets`

func (s *SQLiteStore) CreateImportAsset(asset *ImportAsset) error {
	if asset == nil || strings.TrimSpace(asset.ID) == "" || strings.TrimSpace(asset.ProjectID) == "" || strings.TrimSpace(asset.ObjectKey) == "" {
		return errors.New("import asset identity, project, and object key are required")
	}
	if asset.State == "" {
		asset.State = ImportAssetUploading
	}
	if asset.Revision <= 0 {
		asset.Revision = 1
	}
	now := time.Now().UTC()
	if asset.CreatedAt.IsZero() {
		asset.CreatedAt = now
	}
	asset.UpdatedAt = now
	_, err := s.db.Exec(`INSERT INTO import_assets
		(id, project_id, import_id, object_key, sha256, size_bytes, content_type, state, last_error, revision, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, asset.ID, asset.ProjectID, asset.ImportID,
		asset.ObjectKey, asset.SHA256, asset.SizeBytes, asset.ContentType, asset.State, asset.LastError,
		asset.Revision, asset.CreatedAt, asset.UpdatedAt)
	return err
}

func scanImportAsset(row capacityRowScanner) (*ImportAsset, error) {
	var asset ImportAsset
	if err := row.Scan(&asset.ID, &asset.ProjectID, &asset.ImportID, &asset.ObjectKey, &asset.SHA256,
		&asset.SizeBytes, &asset.ContentType, &asset.State, &asset.LastError, &asset.Revision,
		&asset.CreatedAt, &asset.UpdatedAt); err != nil {
		return nil, err
	}
	return &asset, nil
}

func (s *SQLiteStore) GetImportAsset(id string) (*ImportAsset, error) {
	return scanImportAsset(s.db.QueryRow(importAssetSelect+` WHERE id = ?`, strings.TrimSpace(id)))
}

func (s *SQLiteStore) TransitionImportAsset(asset *ImportAsset, expectedRevision int) (bool, error) {
	if asset == nil || expectedRevision <= 0 {
		return false, errors.New("import asset and expected revision are required")
	}
	asset.UpdatedAt = time.Now().UTC()
	result, err := s.db.Exec(`UPDATE import_assets SET state=?, last_error=?, revision=?, updated_at=?
		WHERE id=? AND revision=?`, asset.State, asset.LastError, asset.Revision, asset.UpdatedAt, asset.ID, expectedRevision)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}
