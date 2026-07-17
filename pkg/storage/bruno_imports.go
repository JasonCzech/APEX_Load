package storage

import (
	"errors"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

var (
	ErrBrunoImportConcurrentChange = errors.New("Bruno import changed concurrently")
	ErrBrunoRevisionNotFound       = errors.New("Bruno source revision not found")
)

// BrunoSourceArtifact is immutable, content-addressed metadata for one
// encrypted source object. The bytes themselves live in the configured
// artifact backend; BackendKey is opaque to storage callers.
type BrunoSourceArtifact struct {
	ID                 string         `json:"id"`
	ProjectID          string         `json:"project_id"`
	ArtifactKind       string         `json:"artifact_kind"`
	BackendKey         string         `json:"backend_key"`
	SHA256             string         `json:"sha256"`
	SizeBytes          int64          `json:"size_bytes"`
	ManifestSHA256     string         `json:"manifest_sha256,omitempty"`
	SourceFormat       string         `json:"source_format,omitempty"`
	EncryptionMetadata map[string]any `json:"encryption_metadata,omitempty"`
	LifecycleState     string         `json:"lifecycle_state"`
	CreatedAt          time.Time      `json:"created_at"`
}

// BrunoSourceRevision binds a canonical APEX script to immutable raw and
// sanitized source artifacts. CanonicalScript is retained so rollback can
// atomically restore the source binding without reparsing an untrusted upload.
type BrunoSourceRevision struct {
	ID                    string            `json:"id"`
	ScriptID              string            `json:"script_id"`
	ProjectID             string            `json:"project_id"`
	ParentRevisionID      string            `json:"parent_revision_id,omitempty"`
	Active                bool              `json:"active"`
	RawArtifactID         string            `json:"raw_artifact_id,omitempty"`
	SanitizedArtifactID   string            `json:"sanitized_artifact_id"`
	SourceFormat          string            `json:"source_format"`
	CompatibilityVersion  string            `json:"compatibility_version"`
	IdentityMap           map[string]string `json:"identity_map,omitempty"`
	ImportOptions         map[string]any    `json:"import_options,omitempty"`
	DependencySnapshotRef string            `json:"dependency_snapshot_ref,omitempty"`
	SecurityProfile       map[string]any    `json:"security_profile,omitempty"`
	ChangeSummary         map[string]any    `json:"change_summary,omitempty"`
	SecretBindings        map[string]string `json:"secret_bindings,omitempty"`
	CanonicalScript       *dsl.Script       `json:"canonical_script,omitempty"`
	CreatedBy             string            `json:"created_by,omitempty"`
	CreatedAt             time.Time         `json:"created_at"`
}

type BrunoImportCommit struct {
	Script                   *dsl.Script
	ExpectedScriptUpdatedAt  *time.Time
	ExpectedActiveRevisionID string
	RawArtifact              BrunoSourceArtifact
	SanitizedArtifact        BrunoSourceArtifact
	Revision                 BrunoSourceRevision
}

type BrunoImportCommitResult struct {
	Script   *dsl.Script          `json:"script"`
	Revision *BrunoSourceRevision `json:"revision"`
	Created  bool                 `json:"created"`
}

// BrunoImportStore is intentionally optional instead of widening Store. This
// keeps lightweight test adapters source-compatible while production
// SQLite/PostgreSQL provide the atomic import and rollback boundary.
type BrunoImportStore interface {
	CommitBrunoImport(commit *BrunoImportCommit) (*BrunoImportCommitResult, error)
	GetBrunoSourceArtifact(id string, projectID string) (*BrunoSourceArtifact, error)
	GetBrunoSourceRevision(id string, projectID string) (*BrunoSourceRevision, error)
	GetActiveBrunoSourceRevision(scriptID string, projectID string) (*BrunoSourceRevision, error)
	ListBrunoSourceRevisions(scriptID string, projectID string) ([]*BrunoSourceRevision, error)
	ActivateBrunoSourceRevision(scriptID string, projectID string, revisionID string, expectedActiveRevisionID string) (*BrunoSourceRevision, error)
}

type BrunoArtifactReferenceChecker interface {
	IsBrunoArtifactBackendKeyReferenced(backendKey string) (bool, error)
}
