package api

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
)

const (
	libraryCredentialRefEnvPrefix     = "env:"
	libraryCredentialEnvNamePrefix    = "APEXLOAD_LIBRARY_REMOTE_"
	libraryCredentialRefKeyringPrefix = "keyring:"
	libraryKeyringService             = "APEX Load"
)

// LibraryCredentialStore resolves write-only remote library credentials.
type LibraryCredentialStore interface {
	PutRemoteAPIKey(remoteID string, apiKey string) (string, error)
	GetRemoteAPIKey(remote LibraryRemote) (string, error)
	DeleteRemoteAPIKey(remoteID string) error
}

type EnvLibraryCredentialStore struct{}

func NewEnvLibraryCredentialStore() *EnvLibraryCredentialStore {
	return &EnvLibraryCredentialStore{}
}

func (s *EnvLibraryCredentialStore) PutRemoteAPIKey(_ string, apiKey string) (string, error) {
	if strings.TrimSpace(apiKey) == "" {
		return "", nil
	}
	return "", errors.New("remote API keys cannot be persisted by the env credential resolver; use credential_ref env:VAR_NAME")
}

func (s *EnvLibraryCredentialStore) GetRemoteAPIKey(remote LibraryRemote) (string, error) {
	ref := strings.TrimSpace(remote.CredentialRef)
	if ref == "" {
		return "", nil
	}
	if !strings.HasPrefix(strings.ToLower(ref), libraryCredentialRefEnvPrefix) {
		return "", fmt.Errorf("unsupported credential_ref %q for env resolver", ref)
	}
	name := strings.TrimSpace(ref[len(libraryCredentialRefEnvPrefix):])
	if name == "" {
		return "", errors.New("credential_ref env variable name is empty")
	}
	if !strings.HasPrefix(name, libraryCredentialEnvNamePrefix) {
		return "", fmt.Errorf("credential_ref environment variable must start with %s", libraryCredentialEnvNamePrefix)
	}
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return "", fmt.Errorf("credential_ref %s is not configured", ref)
	}
	return value, nil
}

func (s *EnvLibraryCredentialStore) DeleteRemoteAPIKey(_ string) error {
	return nil
}

type MemoryLibraryCredentialStore struct {
	mu      sync.Mutex
	secrets map[string]string
}

func NewMemoryLibraryCredentialStore() *MemoryLibraryCredentialStore {
	return &MemoryLibraryCredentialStore{secrets: make(map[string]string)}
}

func (s *MemoryLibraryCredentialStore) PutRemoteAPIKey(remoteID string, apiKey string) (string, error) {
	remoteID = strings.TrimSpace(remoteID)
	apiKey = strings.TrimSpace(apiKey)
	if remoteID == "" {
		return "", errors.New("remote id is required")
	}
	if apiKey == "" {
		return "", nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.secrets[remoteID] = apiKey
	return libraryCredentialRefKeyringPrefix + libraryKeyringAccount(remoteID), nil
}

func (s *MemoryLibraryCredentialStore) GetRemoteAPIKey(remote LibraryRemote) (string, error) {
	remoteID := libraryCredentialStorageID(remote)
	if remoteID == "" {
		return "", errors.New("remote id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.TrimSpace(s.secrets[remoteID]), nil
}

func (s *MemoryLibraryCredentialStore) DeleteRemoteAPIKey(remoteID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.secrets, strings.TrimSpace(remoteID))
	return nil
}

func libraryKeyringAccount(remoteID string) string {
	return "library-remote:" + strings.TrimSpace(remoteID)
}

func libraryCredentialStorageID(remote LibraryRemote) string {
	ref := strings.TrimSpace(remote.CredentialRef)
	keyringPrefix := libraryCredentialRefKeyringPrefix + "library-remote:"
	if strings.HasPrefix(strings.ToLower(ref), keyringPrefix) {
		return strings.TrimSpace(ref[len(keyringPrefix):])
	}
	return strings.TrimSpace(remote.ID)
}

func libraryKeyringAccountForRemote(remote LibraryRemote) string {
	ref := strings.TrimSpace(remote.CredentialRef)
	if strings.HasPrefix(strings.ToLower(ref), libraryCredentialRefKeyringPrefix) {
		return strings.TrimSpace(ref[len(libraryCredentialRefKeyringPrefix):])
	}
	return libraryKeyringAccount(remote.ID)
}
