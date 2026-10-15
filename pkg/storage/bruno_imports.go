package storage

import (
	"errors"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

var (
	ErrBrunoImportConcurrentChange = errors.New("Bruno import changed concurrently")
	ErrBrunoRevisionNotFound       = errors.New("Bruno source revision not found")
	ErrBrunoNativeBatchConflict    = errors.New("Bruno native import batch idempotency conflict")
)

// ImportSourceRevision binds a canonical APEX script to immutable raw and
// sanitized source artifacts. CanonicalScript is retained so rollback can
// atomically restore the source binding without reparsing an untrusted upload.
type ImportSourceRevision struct {
	ID                  string `json:"id"`
	ScriptID            string `json:"script_id"`
	ProjectID           string `json:"project_id"`
	ParentRevisionID    string `json:"parent_revision_id,omitempty"`
	Active              bool   `json:"active"`
	RawArtifactID       string `json:"raw_artifact_id,omitempty"`
	SanitizedArtifactID string `json:"sanitized_artifact_id"`
	// SanitizedAvailable reports whether SanitizedArtifactID names a genuinely
	// sanitized document rather than a second reference to the raw one. Formats
	// that retain source raw-only leave this false, and the source download
	// refuses to serve them under the editor-visible sanitized gate.
	SanitizedAvailable    bool              `json:"sanitized_available"`
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
	ExpectedScriptAuthoring  *ScriptAuthoringPrecondition `json:"-"`
	ExpectedScriptUpdatedAt  *time.Time
	ExpectedActiveRevisionID string
	RawArtifact              ImportSourceArtifact
	SanitizedArtifact        ImportSourceArtifact
	// UseRawArtifact keeps the imported source credential values in the
	// execution bundle. The raw object remains protected by the artifact store
	// and raw-source authorization; only the durable runtime reference selects
	// it. Sanitized source remains available for ordinary review/download.
	UseRawArtifact bool
	Revision       ImportSourceRevision
}

type BrunoImportCommitResult struct {
	AuthoringSnapshot *ScriptAuthoringSnapshot `json:"-"`
	Script            *dsl.Script              `json:"script"`
	Revision          *ImportSourceRevision    `json:"revision"`
	Created           bool                     `json:"created"`
}

// BrunoNativeImportBatch is the durable receipt for a create-only native
// per-request import. The receipt is keyed by the protected import session so
// a caller can safely retry after losing the first successful HTTP response.
// Source artifacts are shared by every script in the batch instead of being
// copied into each script's metadata.
type BrunoNativeImportBatch struct {
	ID             string `json:"id"`
	ProjectID      string `json:"project_id"`
	IdempotencyKey string `json:"idempotency_key"`
	// RequestDigestProfile names the hash function the receipt's RequestSHA256
	// was produced with. A replay must verify against the profile the receipt
	// was written under, not whichever profile the server writes today, or
	// receipts predating a profile change would all read as conflicts.
	RequestDigestProfile string    `json:"request_digest_profile,omitempty"`
	RequestSHA256        string    `json:"request_sha256"`
	RawArtifactID        string    `json:"raw_artifact_id,omitempty"`
	SanitizedArtifactID  string    `json:"sanitized_artifact_id"`
	CandidateIDs         []string  `json:"candidate_ids"`
	ScriptIDs            []string  `json:"script_ids"`
	CreatedBy            string    `json:"created_by,omitempty"`
	CreatedAt            time.Time `json:"created_at"`
}

type BrunoNativeImportCommit struct {
	Batch             BrunoNativeImportBatch
	Scripts           []*dsl.Script
	RawArtifact       ImportSourceArtifact
	SanitizedArtifact ImportSourceArtifact
	// RefKeyEnv is the environment segment used when the transaction assigns
	// reference keys to scripts that do not already have one. Allocation lives
	// in the same transaction as the scripts and receipt so a failed batch does
	// not consume durable reference-key sequence values.
	RefKeyEnv string
	// ExpectedUpdatedAt is positional with Scripts; a non-nil entry updates
	// that existing script as a new version. See ImportBatchCommit.
	ExpectedUpdatedAt []*time.Time
}

type BrunoNativeImportCommitResult struct {
	Batch   *BrunoNativeImportBatch `json:"batch"`
	Scripts []*dsl.Script           `json:"scripts"`
	Created bool                    `json:"created"`
}

// ImportSourceStore is intentionally optional instead of widening Store. This
// keeps lightweight test adapters source-compatible while production
// SQLite/PostgreSQL provide the atomic import and rollback boundary.
type ImportSourceStore interface {
	CommitBrunoImport(commit *BrunoImportCommit) (*BrunoImportCommitResult, error)
	GetImportSourceArtifact(id string, projectID string) (*ImportSourceArtifact, error)
	GetImportSourceRevision(id string, projectID string) (*ImportSourceRevision, error)
	GetActiveImportSourceRevision(scriptID string, projectID string) (*ImportSourceRevision, error)
	ListImportSourceRevisions(scriptID string, projectID string) ([]*ImportSourceRevision, error)
	ActivateImportSourceRevision(scriptID string, projectID string, revisionID string, expectedActiveRevisionID string) (*ImportSourceRevision, error)
}

// BrunoNativeImportStore is optional so lightweight stores remain source
// compatible. Production SQLite/PostgreSQL implementations provide a single
// transaction for all native scripts, shared source artifacts, and the
// idempotency receipt.
type BrunoNativeImportStore interface {
	CommitBrunoNativeImport(commit *BrunoNativeImportCommit) (*BrunoNativeImportCommitResult, error)
	GetBrunoNativeImportByIdempotencyKey(idempotencyKey string, projectID string) (*BrunoNativeImportCommitResult, error)
}

type BrunoArtifactReferenceChecker interface {
	IsBrunoArtifactBackendKeyReferenced(backendKey string) (bool, error)
}

// ScriptImportSourceAuthoringStore retains the independent active-source fence
// while optionally claiming the script authored-content nonce in the same TX.
type ScriptImportSourceAuthoringStore interface {
	ActivateImportSourceRevisionIfScriptCurrent(scriptID, projectID, revisionID, expectedActiveRevisionID string, expected ScriptAuthoringPrecondition) (*ImportSourceRevision, *ScriptAuthoringSnapshot, error)
}

var _ ScriptImportSourceAuthoringStore = (*SQLiteStore)(nil)
