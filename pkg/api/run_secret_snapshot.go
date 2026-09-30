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
	"sync"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/keywrap"
)

const maxRunPinnedSecretSnapshotBytes = 1 << 20

func runSecretSnapshotAAD(testID string) []byte {
	return []byte("apexload/run-pinned-secrets/v1\x00" + testID)
}

func sealRunPinnedSecrets(ctx context.Context, wrapper keywrap.KeyWrapper, testID string, values map[int]map[string]string) (string, *keywrap.WrappedKey, error) {
	if wrapper == nil {
		return "", nil, errors.New("run secret key wrapper is unavailable")
	}
	plaintext, err := json.Marshal(values)
	if err != nil {
		return "", nil, err
	}
	defer clearRunSnapshotBytes(plaintext)
	if len(plaintext) > maxRunPinnedSecretSnapshotBytes {
		return "", nil, errors.New("run secret snapshot exceeds limit")
	}
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return "", nil, err
	}
	defer clearRunSnapshotBytes(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", nil, err
	}
	aad := runSecretSnapshotAAD(testID)
	sealed := aead.Seal(nonce, nonce, plaintext, aad)
	wrapped, err := wrapper.WrapKey(ctx, key, aad)
	if err != nil {
		return "", nil, err
	}
	return base64.RawStdEncoding.EncodeToString(sealed), &wrapped, nil
}

func openRunPinnedSecrets(ctx context.Context, wrapper keywrap.KeyWrapper, testID, encoded string, wrapped *keywrap.WrappedKey) (map[int]map[string]string, error) {
	if encoded == "" && wrapped == nil {
		return nil, nil
	}
	if encoded == "" || wrapped == nil {
		return nil, errors.New("incomplete run secret snapshot")
	}
	if wrapper == nil {
		return nil, errors.New("run secret key wrapper is unavailable")
	}
	sealed, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil || len(sealed) > maxRunPinnedSecretSnapshotBytes+128 {
		return nil, errors.New("invalid run secret snapshot")
	}
	aad := runSecretSnapshotAAD(testID)
	key, err := wrapper.UnwrapKey(ctx, *wrapped, aad)
	if err != nil {
		return nil, fmt.Errorf("%w: unwrap run secret snapshot: %w", errRunAdoptionKeyAccess, err)
	}
	defer clearRunSnapshotBytes(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil || len(sealed) < aead.NonceSize() {
		return nil, errors.New("invalid run secret snapshot")
	}
	plaintext, err := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], aad)
	if err != nil {
		return nil, errors.New("decrypt run secret snapshot")
	}
	defer clearRunSnapshotBytes(plaintext)
	var values map[int]map[string]string
	if err := json.Unmarshal(plaintext, &values); err != nil {
		return nil, errors.New("decode run secret snapshot")
	}
	return values, nil
}

func clearRunSnapshotBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

// runPinnedSecretState is shared by all copies of one ManagedTest. Values can
// refresh during queued preflight, then remain fixed through execution. They
// are never exposed through API JSON.
type runPinnedSecretState struct {
	mu       sync.RWMutex
	groups   map[int]map[string]string
	retired  bool
	revision uint64
}

func (s *runPinnedSecretState) get(index int) (map[string]string, bool) {
	if s == nil {
		return nil, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.retired {
		return nil, true // A terminal run must never fall back to the live provider.
	}
	if s.groups == nil {
		return nil, false
	}
	values := s.groups[index]
	copy := make(map[string]string, len(values))
	for name, value := range values {
		copy[name] = value
	}
	return copy, true
}

func (s *runPinnedSecretState) snapshot() map[int]map[string]string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.groups == nil {
		return nil
	}
	copy := make(map[int]map[string]string, len(s.groups))
	for index, values := range s.groups {
		copy[index] = make(map[string]string, len(values))
		for name, value := range values {
			copy[index][name] = value
		}
	}
	return copy
}

func (s *runPinnedSecretState) setIfAbsent(values map[int]map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.groups != nil || s.retired {
		return
	}
	s.groups = values
}

// invalidate releases the shared snapshot after an executable edit or terminal
// cleanup. Retiring also fences late admission work from repopulating it.
func (s *runPinnedSecretState) invalidate(retire bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.groups = nil
	s.retired = s.retired || retire
	s.revision++
}

func (s *runPinnedSecretState) version() uint64 {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.revision
}

func (s *runPinnedSecretState) pinRevision(values map[int]map[string]string, revision uint64, refresh bool) bool {
	if s == nil {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.retired || s.revision != revision {
		return false
	}
	if s.groups == nil || refresh {
		s.groups = values
	}
	return true
}

func runSecretApplicationID(test *ManagedTest) string {
	if test == nil || isLegacyTestReleaseContext(test.ApplicationID, test.ReleaseID) {
		return ""
	}
	return strings.TrimSpace(test.ApplicationID)
}

func (tr *TestRunner) secretResolverForRunGroup(test *ManagedTest, index int, projectID string) engine.SecretResolver {
	if test == nil {
		return tr.secretResolverForProject(projectID)
	}
	if values, pinned := test.pinnedSecrets.get(index); pinned {
		return engine.SecretResolverFunc(func(name string) (string, bool) {
			value, ok := values[name]
			return value, ok
		})
	}
	return tr.secretResolverForScope(projectID, runSecretApplicationID(test))
}

// The compiled group retains this resolver before start admission. Resolve
// through the current pin state at each use so a later rotation cannot change
// an active run's values.
func (tr *TestRunner) boundSecretResolverForRunGroup(test *ManagedTest, index int, projectID string) engine.SecretResolver {
	return engine.ContextSecretResolverFunc(func(ctx context.Context, name string) (string, bool) {
		return engine.ResolveSecret(ctx, tr.secretResolverForRunGroup(test, index, projectID), name)
	})
}
