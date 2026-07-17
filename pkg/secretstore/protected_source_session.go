package secretstore

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/keywrap"
	"github.com/jasonczech/apexload/pkg/storage"
)

const protectedSourceSessionPayloadLimit = 2 << 20
const protectedSourceSessionClaimTTL = time.Minute

var ErrProtectedSourceSessionExpired = errors.New("source secret session expired")

type ProtectedSourceSession struct {
	ID        string
	ProjectID string
	Actor     string
	Values    map[string]string
	ExpiresAt time.Time
}

// ProtectedSourceSessionStore uses the same envelope-key foundation as Bruno
// artifacts and database project secrets, while persisting the encrypted
// handoff in the shared controller database. Consume is atomic in storage, so
// separate HA replicas cannot replay the same session.
type ProtectedSourceSessionStore struct {
	Records storage.ProtectedSourceSecretSessionStore
	Wrapper keywrap.KeyWrapper
}

func (s *ProtectedSourceSessionStore) Available() bool {
	return s != nil && s.Records != nil && s.Wrapper != nil
}

func (s *ProtectedSourceSessionStore) Create(ctx context.Context, session ProtectedSourceSession) error {
	if !s.Available() {
		return errors.New("durable protected source secret sessions are unavailable")
	}
	session.ID = strings.TrimSpace(session.ID)
	session.ProjectID = strings.TrimSpace(session.ProjectID)
	session.Actor = strings.TrimSpace(session.Actor)
	session.ExpiresAt = session.ExpiresAt.UTC().Truncate(time.Microsecond)
	if session.ID == "" || session.ProjectID == "" || session.Actor == "" || len(session.Values) == 0 ||
		session.ExpiresAt.IsZero() || !session.ExpiresAt.After(time.Now().UTC()) {
		return errors.New("protected source secret session scope is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	payload, err := json.Marshal(session)
	if err != nil {
		return err
	}
	defer zeroBytes(payload)
	if len(payload) > protectedSourceSessionPayloadLimit {
		return errors.New("protected source secret session exceeds encrypted payload limit")
	}
	dek := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, dek); err != nil {
		return err
	}
	defer zeroBytes(dek)
	block, err := aes.NewCipher(dek)
	if err != nil {
		return err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return err
	}
	aad := protectedSourceSessionAAD(session.ID, session.ProjectID, session.Actor, session.ExpiresAt)
	sealed := aead.Seal(nonce, nonce, payload, aad)
	wrapped, err := s.Wrapper.WrapKey(ctx, dek, aad)
	if err != nil {
		return fmt.Errorf("wrap protected source secret session key: %w", err)
	}
	wrappedJSON, err := json.Marshal(wrapped)
	if err != nil {
		return err
	}
	defer zeroBytes(wrappedJSON)
	created, err := s.Records.CreateProtectedSourceSecretSession(&storage.ProtectedSourceSecretSessionCipher{
		ID:             session.ID,
		ProjectID:      session.ProjectID,
		Actor:          session.Actor,
		Ciphertext:     base64.RawStdEncoding.EncodeToString(sealed),
		WrappedKeyJSON: string(wrappedJSON),
		ExpiresAt:      session.ExpiresAt,
		CreatedAt:      time.Now().UTC().Truncate(time.Microsecond),
	})
	if err != nil {
		return err
	}
	if !created {
		return storage.ErrProtectedSourceSecretSessionCapacity
	}
	return nil
}

func (s *ProtectedSourceSessionStore) Consume(ctx context.Context, id, projectID, actor string) (*ProtectedSourceSession, error) {
	if !s.Available() {
		return nil, errors.New("durable protected source secret sessions are unavailable")
	}
	id = strings.TrimSpace(id)
	projectID = strings.TrimSpace(projectID)
	actor = strings.TrimSpace(actor)
	if id == "" || projectID == "" || actor == "" {
		return nil, ErrNotFound
	}
	claimBytes := make([]byte, 24)
	if _, err := io.ReadFull(rand.Reader, claimBytes); err != nil {
		return nil, err
	}
	claimToken := base64.RawURLEncoding.EncodeToString(claimBytes)
	zeroBytes(claimBytes)
	now := time.Now().UTC()
	record, err := s.Records.ClaimProtectedSourceSecretSession(id, projectID, actor, claimToken, now, now.Add(protectedSourceSessionClaimTTL))
	if err != nil {
		return nil, err
	}
	claimed := true
	release := func(operationErr error) error {
		if !claimed {
			return operationErr
		}
		claimed = false
		released, releaseErr := s.Records.ReleaseProtectedSourceSecretSessionClaim(id, projectID, actor, claimToken)
		if releaseErr == nil && !released {
			releaseErr = errors.New("protected source secret session claim was lost")
		}
		return errors.Join(operationErr, releaseErr)
	}
	if !record.ExpiresAt.After(now) {
		return nil, release(ErrProtectedSourceSessionExpired)
	}
	sealed, err := base64.RawStdEncoding.DecodeString(record.Ciphertext)
	if err != nil {
		return nil, release(errors.New("invalid encrypted source secret session"))
	}
	var wrapped keywrap.WrappedKey
	if err := json.Unmarshal([]byte(record.WrappedKeyJSON), &wrapped); err != nil {
		zeroBytes(sealed)
		return nil, release(errors.New("invalid wrapped source secret session key"))
	}
	aad := protectedSourceSessionAAD(record.ID, record.ProjectID, record.Actor, record.ExpiresAt)
	dek, err := s.Wrapper.UnwrapKey(ctx, wrapped, aad)
	if err != nil {
		zeroBytes(sealed)
		return nil, release(errors.New("unwrap source secret session key"))
	}
	defer zeroBytes(dek)
	block, err := aes.NewCipher(dek)
	if err != nil {
		zeroBytes(sealed)
		return nil, release(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		zeroBytes(sealed)
		return nil, release(err)
	}
	if len(sealed) < aead.NonceSize() {
		zeroBytes(sealed)
		return nil, release(errors.New("invalid encrypted source secret session"))
	}
	plaintext, err := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], aad)
	zeroBytes(sealed)
	if err != nil {
		return nil, release(errors.New("decrypt source secret session"))
	}
	defer zeroBytes(plaintext)
	if len(plaintext) > protectedSourceSessionPayloadLimit {
		return nil, release(errors.New("decrypted source secret session exceeds payload limit"))
	}
	var session ProtectedSourceSession
	if err := json.Unmarshal(plaintext, &session); err != nil {
		return nil, release(errors.New("decode protected source secret session"))
	}
	if session.ID != record.ID || session.ProjectID != record.ProjectID || session.Actor != record.Actor ||
		!session.ExpiresAt.Equal(record.ExpiresAt) || len(session.Values) == 0 {
		clearStringMap(session.Values)
		return nil, release(errors.New("protected source secret session scope mismatch"))
	}
	finalized, err := s.Records.FinalizeProtectedSourceSecretSession(id, projectID, actor, claimToken)
	if err != nil || !finalized {
		clearStringMap(session.Values)
		if err == nil {
			err = errors.New("protected source secret session claim was lost")
		}
		return nil, release(err)
	}
	claimed = false
	return &session, nil
}

// Discard atomically deletes a scoped protected session without decrypting its
// values. A missing, already-consumed, or differently scoped session is an
// idempotent no-op so UI completion/cancellation cleanup is safe to retry.
func (s *ProtectedSourceSessionStore) Discard(ctx context.Context, id, projectID, actor string) (bool, error) {
	if !s.Available() {
		return false, errors.New("durable protected source secret sessions are unavailable")
	}
	id = strings.TrimSpace(id)
	projectID = strings.TrimSpace(projectID)
	actor = strings.TrimSpace(actor)
	if id == "" || projectID == "" || actor == "" {
		return false, ErrNotFound
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	claimBytes := make([]byte, 24)
	if _, err := io.ReadFull(rand.Reader, claimBytes); err != nil {
		return false, err
	}
	claimToken := base64.RawURLEncoding.EncodeToString(claimBytes)
	zeroBytes(claimBytes)
	now := time.Now().UTC()
	if _, err := s.Records.ClaimProtectedSourceSecretSession(
		id,
		projectID,
		actor,
		claimToken,
		now,
		now.Add(protectedSourceSessionClaimTTL),
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	finalized, err := s.Records.FinalizeProtectedSourceSecretSession(id, projectID, actor, claimToken)
	if err != nil {
		return false, err
	}
	if !finalized {
		return false, errors.New("protected source secret session claim was lost")
	}
	return true, nil
}

func (s *ProtectedSourceSessionStore) DeleteExpired(now time.Time) (int64, error) {
	if !s.Available() {
		return 0, nil
	}
	return s.Records.DeleteExpiredProtectedSourceSecretSessions(now.UTC())
}

func protectedSourceSessionAAD(id, projectID, actor string, expiresAt time.Time) []byte {
	return []byte("apexload/source-secret-session/v1\x00" + strings.TrimSpace(projectID) + "\x00" +
		strings.TrimSpace(id) + "\x00" + strings.TrimSpace(actor) + "\x00" +
		strconv.FormatInt(expiresAt.UTC().Truncate(time.Microsecond).UnixNano(), 10))
}

func clearStringMap(values map[string]string) {
	for key := range values {
		values[key] = ""
		delete(values, key)
	}
}
