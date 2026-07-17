package storage

import (
	"errors"
	"time"
)

var ErrProtectedSourceSecretSessionCapacity = errors.New("protected source secret session capacity reached")

// ProjectSecretCipher is encrypted provider material. Plaintext secret values
// never cross the storage package boundary.
type ProjectSecretCipher struct {
	ProjectID      string
	SecretName     string
	Provider       string
	Ciphertext     string
	WrappedKeyJSON string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	CreatedBy      string
}

type ProjectSecretMetadata struct {
	ProjectID  string    `json:"project_id"`
	SecretName string    `json:"secret_name"`
	Provider   string    `json:"provider"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	CreatedBy  string    `json:"created_by,omitempty"`
}

// ProjectSecretCipherStore is optional so existing Store test doubles do not
// gain plaintext-secret responsibilities. SQLiteStore and PostgresStore
// implement it.
type ProjectSecretCipherStore interface {
	CreateProjectSecretCipher(*ProjectSecretCipher) (bool, error)
	ReplaceProjectSecretCipher(*ProjectSecretCipher) (bool, error)
	// DeleteProjectSecretCipherIfCreatedBy is the compensation boundary for a
	// failed multi-step create. It must compare and delete atomically so a retry,
	// preexisting value, or administrator rotation can never be removed.
	DeleteProjectSecretCipherIfCreatedBy(projectID, secretName, createdBy string) (bool, error)
	GetProjectSecretCipher(projectID, secretName string) (*ProjectSecretCipher, error)
	ListProjectSecretMetadata(projectID string) ([]ProjectSecretMetadata, error)
}

// ProjectSecretMetadataStore is the durable, value-free index for managed
// external providers such as Azure Key Vault. Its atomic create claim prevents
// Set Secret from silently versioning an existing logical name.
type ProjectSecretMetadataStore interface {
	CreateProjectSecretMetadata(*ProjectSecretMetadata) (bool, error)
	ReplaceProjectSecretMetadata(*ProjectSecretMetadata) (bool, error)
	GetProjectSecretMetadata(projectID, secretName string) (*ProjectSecretMetadata, error)
	ListExternalProjectSecretMetadata(projectID string) ([]ProjectSecretMetadata, error)
	DeleteProjectSecretMetadata(projectID, secretName string) error
}

// ProtectedSourceSecretSessionCipher is the encrypted, short-lived handoff
// used between import inspection and source-secret migration. The actor,
// project, and expiry are value-free scope metadata; Values never cross the
// storage boundary outside Ciphertext.
type ProtectedSourceSecretSessionCipher struct {
	ID             string
	ProjectID      string
	Actor          string
	Ciphertext     string
	WrappedKeyJSON string
	ExpiresAt      time.Time
	CreatedAt      time.Time
}

// ProtectedSourceSecretSessionStore provides a shared, atomic single-use
// boundary for controller HA. A consumer first acquires a bounded claim, then
// either finalizes deletion after successful decryption or releases the claim
// after a transient failure. Implementations must compare claim tokens on every
// terminal operation so a stale controller cannot delete a re-claimed session.
type ProtectedSourceSecretSessionStore interface {
	CreateProtectedSourceSecretSession(*ProtectedSourceSecretSessionCipher) (bool, error)
	ClaimProtectedSourceSecretSession(id, projectID, actor, claimToken string, now, claimExpiresAt time.Time) (*ProtectedSourceSecretSessionCipher, error)
	FinalizeProtectedSourceSecretSession(id, projectID, actor, claimToken string) (bool, error)
	ReleaseProtectedSourceSecretSessionClaim(id, projectID, actor, claimToken string) (bool, error)
	DeleteExpiredProtectedSourceSecretSessions(now time.Time) (int64, error)
}
