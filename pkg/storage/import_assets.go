package storage

import "time"

const (
	ImportAssetUploading = "uploading"
	ImportAssetReady     = "ready"
	ImportAssetFailed    = "failed"
)

type ImportAsset struct {
	ID          string    `json:"id"`
	ProjectID   string    `json:"project_id"`
	ImportID    string    `json:"import_id"`
	ObjectKey   string    `json:"-"`
	SHA256      string    `json:"sha256"`
	SizeBytes   int64     `json:"size_bytes"`
	ContentType string    `json:"content_type,omitempty"`
	State       string    `json:"state"`
	LastError   string    `json:"last_error,omitempty"`
	Revision    int       `json:"revision"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type ImportAssetStore interface {
	CreateImportAsset(*ImportAsset) error
	GetImportAsset(id string) (*ImportAsset, error)
	TransitionImportAsset(asset *ImportAsset, expectedRevision int) (bool, error)
}
