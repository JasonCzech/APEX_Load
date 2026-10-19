package secretstore

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
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/keywrap"
	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	databaseProvider = "encrypted_database"
	maxSecretBytes   = 64 << 10
)

type DatabaseStore struct {
	Records storage.ProjectSecretCipherStore
	Wrapper keywrap.KeyWrapper
}

func (s *DatabaseStore) Writable() bool { return s != nil && s.Records != nil && s.Wrapper != nil }
func (*DatabaseStore) Provider() string { return databaseProvider }

func (s *DatabaseStore) Create(ctx context.Context, projectID, name, value, actor string) (Metadata, error) {
	if !s.Writable() {
		return Metadata{}, ErrReadOnly
	}
	if engine.ProjectKeyVaultBindingAllowlisted(projectID, name) {
		return Metadata{}, ErrReservedBinding
	}
	record, err := s.encryptedRecord(ctx, projectID, name, value, actor)
	if err != nil {
		return Metadata{}, err
	}
	created, err := s.Records.CreateProjectSecretCipher(record)
	if err != nil {
		return Metadata{}, err
	}
	if !created {
		return Metadata{}, ErrAlreadyExists
	}
	return metadataFromRecord(record, true), nil
}

func (s *DatabaseStore) Replace(ctx context.Context, projectID, name, value, actor string) (Metadata, error) {
	if !s.Writable() {
		return Metadata{}, ErrReadOnly
	}
	projectID, name, err := validateIdentity(projectID, name)
	if err != nil {
		return Metadata{}, err
	}
	existing, err := s.Records.GetProjectSecretCipher(projectID, name)
	if err != nil {
		return Metadata{}, err
	}
	record, err := s.encryptedRecord(ctx, projectID, name, value, actor)
	if err != nil {
		return Metadata{}, err
	}
	record.CreatedAt = existing.CreatedAt
	replaced, err := s.Records.ReplaceProjectSecretCipher(record)
	if err != nil {
		return Metadata{}, err
	}
	if !replaced {
		return Metadata{}, ErrNotFound
	}
	return metadataFromRecord(record, true), nil
}

func (s *DatabaseStore) RollbackCreated(ctx context.Context, projectID, name, createdBy string) (bool, error) {
	if !s.Writable() {
		return false, ErrReadOnly
	}
	projectID, name, err := validateIdentity(projectID, name)
	if err != nil {
		return false, err
	}
	createdBy = strings.TrimSpace(createdBy)
	if createdBy == "" {
		return false, errors.New("secret creator is required for rollback")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	removed, err := s.Records.DeleteProjectSecretCipherIfCreatedBy(projectID, name, createdBy)
	if err != nil || removed {
		return removed, err
	}
	// A previous compensation retry may already have removed the value. Treat
	// absence as success; a surviving differently-owned value is never deleted.
	if _, lookupErr := s.Records.GetProjectSecretCipher(projectID, name); errors.Is(lookupErr, ErrNotFound) {
		return true, nil
	} else if lookupErr != nil {
		return false, lookupErr
	}
	return false, nil
}

func (s *DatabaseStore) encryptedRecord(ctx context.Context, projectID, name, value, actor string) (*storage.ProjectSecretCipher, error) {
	if !s.Writable() {
		return nil, ErrReadOnly
	}
	projectID, name, err := validateIdentity(projectID, name)
	if err != nil {
		return nil, err
	}
	if len(value) == 0 || len(value) > maxSecretBytes {
		return nil, fmt.Errorf("secret value must contain 1-%d bytes", maxSecretBytes)
	}
	if strings.EqualFold(strings.TrimSpace(value), "[REDACTED]") {
		return nil, errors.New("secret value cannot be the redaction sentinel")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dek := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, dek); err != nil {
		return nil, err
	}
	defer zeroBytes(dek)
	block, err := aes.NewCipher(dek)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	aad := secretAAD(projectID, name)
	sealed := aead.Seal(nonce, nonce, []byte(value), aad)
	wrapped, err := s.Wrapper.WrapKey(ctx, dek, aad)
	if err != nil {
		return nil, err
	}
	wrappedJSON, err := json.Marshal(wrapped)
	if err != nil {
		return nil, err
	}
	// The record is stored and returned as the secret's metadata, which the
	// dashboard compares with the listed metadata; PostgreSQL keeps microseconds.
	now := time.Now().UTC().Truncate(time.Microsecond)
	return &storage.ProjectSecretCipher{
		ProjectID:      projectID,
		SecretName:     name,
		Provider:       databaseProvider,
		Ciphertext:     base64.RawStdEncoding.EncodeToString(sealed),
		WrappedKeyJSON: string(wrappedJSON),
		CreatedAt:      now,
		UpdatedAt:      now,
		CreatedBy:      strings.TrimSpace(actor),
	}, nil
}

func (s *DatabaseStore) Get(ctx context.Context, projectID, name string) (string, Metadata, error) {
	if s == nil || s.Records == nil || s.Wrapper == nil {
		return "", Metadata{}, ErrNotFound
	}
	projectID, name, err := validateIdentity(projectID, name)
	if err != nil {
		return "", Metadata{}, err
	}
	record, err := s.Records.GetProjectSecretCipher(projectID, name)
	if err != nil {
		return "", Metadata{}, err
	}
	if record.Provider != databaseProvider {
		return "", Metadata{}, errors.New("unsupported encrypted project secret provider")
	}
	var wrapped keywrap.WrappedKey
	if err := json.Unmarshal([]byte(record.WrappedKeyJSON), &wrapped); err != nil {
		return "", Metadata{}, errors.New("invalid wrapped project secret key")
	}
	aad := secretAAD(projectID, name)
	dek, err := s.Wrapper.UnwrapKey(ctx, wrapped, aad)
	if err != nil {
		return "", Metadata{}, err
	}
	defer zeroBytes(dek)
	block, err := aes.NewCipher(dek)
	if err != nil {
		return "", Metadata{}, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", Metadata{}, err
	}
	sealed, err := base64.RawStdEncoding.DecodeString(record.Ciphertext)
	if err != nil || len(sealed) < aead.NonceSize() {
		return "", Metadata{}, errors.New("invalid encrypted project secret")
	}
	plaintext, err := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], aad)
	if err != nil {
		return "", Metadata{}, errors.New("decrypt project secret")
	}
	if len(plaintext) > maxSecretBytes {
		zeroBytes(plaintext)
		return "", Metadata{}, errors.New("decrypted project secret exceeds runtime limit")
	}
	value := string(plaintext)
	zeroBytes(plaintext)
	return value, metadataFromRecord(record, true), nil
}

func (s *DatabaseStore) List(ctx context.Context, projectID string) ([]Metadata, error) {
	if s == nil || s.Records == nil {
		return []Metadata{}, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	records, err := s.Records.ListProjectSecretMetadata(strings.TrimSpace(projectID))
	if err != nil {
		return nil, err
	}
	result := make([]Metadata, 0, len(records))
	for _, record := range records {
		result = append(result, Metadata{
			ProjectID: record.ProjectID,
			Name:      record.SecretName,
			Provider:  record.Provider,
			Writable:  s.Writable(),
			CreatedAt: record.CreatedAt,
			UpdatedAt: record.UpdatedAt,
			CreatedBy: record.CreatedBy,
		})
	}
	return result, nil
}

func validateIdentity(projectID, name string) (string, string, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return "", "", errors.New("project and secret name are required")
	}
	normalizedName, err := engine.NormalizeLogicalSecretName(name)
	if err != nil {
		return "", "", err
	}
	if len(projectID) > 512 {
		return "", "", errors.New("project or secret name exceeds storage limits")
	}
	return projectID, normalizedName, nil
}

func secretAAD(projectID, name string) []byte {
	return []byte("apexload/project-secret/v1\x00" + projectID + "\x00" + name)
}

func metadataFromRecord(record *storage.ProjectSecretCipher, writable bool) Metadata {
	return Metadata{
		ProjectID: record.ProjectID,
		Name:      record.SecretName,
		Provider:  record.Provider,
		Writable:  writable,
		CreatedAt: record.CreatedAt,
		UpdatedAt: record.UpdatedAt,
		CreatedBy: record.CreatedBy,
	}
}

func zeroBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
