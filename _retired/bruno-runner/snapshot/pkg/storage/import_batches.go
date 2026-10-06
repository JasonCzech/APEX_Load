package storage

import (
	"errors"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

const (
	ImportBatchProfileBrunoNative        = "bruno_native"
	ImportBatchReceiptVersion1           = 1
	ImportBatchDigestBrunoNativeCommitV1 = "bruno-native-commit-v1"
	// ImportBatchDigestBrunoNativeCommitV2 adds shared_values to the hashed
	// request. V1 omitted it, so two commits that differed only in the
	// operator's collection-wide values hashed identically and the idempotent
	// replay path returned the first commit's batch for the second request.
	ImportBatchDigestBrunoNativeCommitV2 = "bruno-native-commit-v2"
)

var (
	ErrImportBatchConflict             = errors.New("import batch idempotency conflict")
	ErrImportBatchConcurrentChange     = errors.New("import batch changed concurrently")
	ErrImportBatchCredentialsUnmanaged = errors.New("import batch contains unmanaged executable credentials")
)

// ImportSourceArtifact is immutable, content-addressed metadata for one
// encrypted import source object. The bytes themselves live in the configured
// artifact backend; BackendKey is opaque to storage callers.
//
// ImportSourceArtifact remains an alias for backwards compatibility.
type ImportSourceArtifact struct {
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

// ImportBatchReceipt is the immutable receipt for one create-only script
// batch. CandidateIDs and ScriptIDs are positional and must retain the
// inspection order supplied by the importer.
type ImportBatchReceipt struct {
	ID                   string    `json:"id"`
	ProjectID            string    `json:"project_id"`
	Profile              string    `json:"profile"`
	ReceiptVersion       int       `json:"receipt_version"`
	IdempotencyKey       string    `json:"idempotency_key"`
	RequestDigestProfile string    `json:"request_digest_profile"`
	RequestSHA256        string    `json:"request_sha256"`
	RawArtifactID        string    `json:"raw_artifact_id,omitempty"`
	SanitizedArtifactID  string    `json:"sanitized_artifact_id"`
	CandidateIDs         []string  `json:"candidate_ids"`
	ScriptIDs            []string  `json:"script_ids"`
	CreatedBy            string    `json:"created_by,omitempty"`
	CreatedAt            time.Time `json:"created_at"`
}

type ImportBatchCommit struct {
	Receipt           ImportBatchReceipt
	Scripts           []*dsl.Script
	RawArtifact       ImportSourceArtifact
	SanitizedArtifact ImportSourceArtifact
	// TrustPreservedExecutableCredentials is a server-owned commit capability.
	// It is intentionally excluded from JSON so a serialized commit or client
	// payload cannot manufacture permission to persist authored credential
	// literals. Storage still requires every accepted script to carry the
	// importer-issued preserved-credential marker.
	TrustPreservedExecutableCredentials bool `json:"-"`
	// RefKeyEnv is the environment segment used when the transaction assigns
	// reference keys to scripts that do not already have one.
	RefKeyEnv string
}

type ImportBatchCommitResult struct {
	Receipt *ImportBatchReceipt `json:"receipt"`
	Scripts []*dsl.Script       `json:"scripts"`
	Created bool                `json:"created"`
}

// ImportBatchStore is optional so lightweight stores remain source compatible.
// Production stores commit all scripts, shared source artifacts, reference-key
// allocations, and the immutable receipt in one transaction.
type ImportBatchStore interface {
	CommitImportBatch(commit *ImportBatchCommit) (*ImportBatchCommitResult, error)
	GetImportBatchByIdempotencyKey(projectID, profile, idempotencyKey string) (*ImportBatchCommitResult, error)
}

type ImportArtifactReferenceChecker interface {
	IsImportArtifactBackendKeyReferenced(backendKey string) (bool, error)
}

var (
	_ ImportBatchStore               = (*SQLiteStore)(nil)
	_ ImportArtifactReferenceChecker = (*SQLiteStore)(nil)
	_ BrunoNativeImportStore         = (*SQLiteStore)(nil)
	_ BrunoArtifactReferenceChecker  = (*SQLiteStore)(nil)
)
