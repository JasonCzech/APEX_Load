package api

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/jasonczech/apexload/pkg/keywrap"
)

// Cluster credentials are sealed with the same envelope scheme as project
// secrets (pkg/secretstore/database.go): a fresh per-record AES-256-GCM data
// key encrypts the value, and the data key is wrapped by the configured
// KeyWrapper — which may be backed by Azure Key Vault, giving rotation and a
// master key that never sits in the database.
//
// They are deliberately NOT stored through secretstore itself. That API is
// project-scoped, and any project chosen to hold a system-scoped cluster
// credential becomes a privilege-escalation channel: project-scoped roles can
// Replace values under /api/v1/projects/{id}/secrets, which would let a project
// administrator repoint the credential for a cluster they do not administer.

// clusterCredentialKind distinguishes the credentials a connection may hold, so
// a sealed AAD client secret cannot be unsealed as a static token, and an AWS
// secret access key cannot be unsealed as a role external ID.
type clusterCredentialKind string

const (
	clusterCredentialAADClientSecret clusterCredentialKind = "aad-client-secret"
	clusterCredentialStaticToken     clusterCredentialKind = "static-token"
	clusterCredentialAWSSecretKey    clusterCredentialKind = "aws-secret-access-key"
	clusterCredentialAWSExternalID   clusterCredentialKind = "aws-role-external-id"

	maxClusterCredentialBytes = 8192
)

// errClusterCredentialEncryptionUnavailable is returned when no key wrapper is
// configured. Callers map it to 503 and refuse the write — storing an
// administrator's cluster credential in plaintext is never an acceptable
// fallback.
var errClusterCredentialEncryptionUnavailable = errors.New("cluster credential encryption is not configured")

// clusterCredentialAAD binds the ciphertext to one connection and one
// credential slot. Without it, an operator with database access could copy a
// credential envelope from a cluster they administer into one they do not.
func clusterCredentialAAD(connectionID string, kind clusterCredentialKind) []byte {
	return []byte("apexload:cluster-connection:" + connectionID + ":" + string(kind))
}

// sealClusterCredential encrypts a credential for storage, returning the
// ciphertext and the wrapped data key.
func sealClusterCredential(ctx context.Context, wrapper keywrap.KeyWrapper, connectionID string, kind clusterCredentialKind, value string) (ciphertext string, wrappedKeyJSON string, err error) {
	if wrapper == nil {
		return "", "", errClusterCredentialEncryptionUnavailable
	}
	if strings.TrimSpace(connectionID) == "" {
		return "", "", errors.New("cluster connection id is required to seal a credential")
	}
	return sealCredentialEnvelope(ctx, wrapper, clusterCredentialAAD(connectionID, kind), "cluster credential", value)
}

func sealCredentialEnvelope(ctx context.Context, wrapper keywrap.KeyWrapper, aad []byte, subject, value string) (ciphertext string, wrappedKeyJSON string, err error) {
	if wrapper == nil {
		return "", "", errClusterCredentialEncryptionUnavailable
	}
	if len(value) == 0 || len(value) > maxClusterCredentialBytes {
		return "", "", fmt.Errorf("%s must contain 1-%d bytes", subject, maxClusterCredentialBytes)
	}

	dek := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, dek); err != nil {
		return "", "", err
	}
	defer func() {
		for i := range dek {
			dek[i] = 0
		}
	}()

	block, err := aes.NewCipher(dek)
	if err != nil {
		return "", "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", "", err
	}
	sealed := aead.Seal(nonce, nonce, []byte(value), aad)

	wrapped, err := wrapper.WrapKey(ctx, dek, aad)
	if err != nil {
		return "", "", err
	}
	wrappedJSON, err := json.Marshal(wrapped)
	if err != nil {
		return "", "", err
	}
	return base64.RawStdEncoding.EncodeToString(sealed), string(wrappedJSON), nil
}

// openClusterCredential decrypts a stored credential. A mismatch between the
// connection ID or credential kind and what was sealed fails AEAD
// authentication rather than returning the wrong secret.
func openClusterCredential(ctx context.Context, wrapper keywrap.KeyWrapper, connectionID string, kind clusterCredentialKind, ciphertext, wrappedKeyJSON string) (string, error) {
	if wrapper == nil {
		return "", errClusterCredentialEncryptionUnavailable
	}
	return openCredentialEnvelope(ctx, wrapper, clusterCredentialAAD(connectionID, kind), "cluster credential", ciphertext, wrappedKeyJSON)
}

func openCredentialEnvelope(ctx context.Context, wrapper keywrap.KeyWrapper, aad []byte, subject, ciphertext, wrappedKeyJSON string) (string, error) {
	if wrapper == nil {
		return "", errClusterCredentialEncryptionUnavailable
	}
	if strings.TrimSpace(ciphertext) == "" || strings.TrimSpace(wrappedKeyJSON) == "" {
		return "", fmt.Errorf("%s is not stored", subject)
	}
	var wrapped keywrap.WrappedKey
	if err := json.Unmarshal([]byte(wrappedKeyJSON), &wrapped); err != nil {
		return "", fmt.Errorf("decode wrapped %s key: %w", subject, err)
	}
	dek, err := wrapper.UnwrapKey(ctx, wrapped, aad)
	if err != nil {
		return "", fmt.Errorf("unwrap %s key: %w", subject, err)
	}
	defer func() {
		for i := range dek {
			dek[i] = 0
		}
	}()

	sealed, err := base64.RawStdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", fmt.Errorf("decode %s ciphertext: %w", subject, err)
	}
	block, err := aes.NewCipher(dek)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(sealed) < aead.NonceSize() {
		return "", fmt.Errorf("%s ciphertext is truncated", subject)
	}
	nonce, body := sealed[:aead.NonceSize()], sealed[aead.NonceSize():]
	plaintext, err := aead.Open(nil, nonce, body, aad)
	if err != nil {
		return "", fmt.Errorf("decrypt %s: %w", subject, err)
	}
	return string(plaintext), nil
}
