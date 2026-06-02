package api

import (
	"errors"
	"strings"

	"github.com/zalando/go-keyring"
)

type KeyringLibraryCredentialStore struct {
	Service string
}

func NewKeyringLibraryCredentialStore() *KeyringLibraryCredentialStore {
	return &KeyringLibraryCredentialStore{Service: libraryKeyringService}
}

func (s *KeyringLibraryCredentialStore) PutRemoteAPIKey(remoteID string, apiKey string) (string, error) {
	remoteID = strings.TrimSpace(remoteID)
	apiKey = strings.TrimSpace(apiKey)
	if remoteID == "" {
		return "", errors.New("remote id is required")
	}
	if apiKey == "" {
		return "", nil
	}
	account := libraryKeyringAccount(remoteID)
	if err := keyring.Set(firstNonEmpty(s.Service, libraryKeyringService), account, apiKey); err != nil {
		return "", err
	}
	return libraryCredentialRefKeyringPrefix + account, nil
}

func (s *KeyringLibraryCredentialStore) GetRemoteAPIKey(remote LibraryRemote) (string, error) {
	if strings.TrimSpace(remote.ID) == "" {
		return "", errors.New("remote id is required")
	}
	secret, err := keyring.Get(firstNonEmpty(s.Service, libraryKeyringService), libraryKeyringAccount(remote.ID))
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(secret), nil
}

func (s *KeyringLibraryCredentialStore) DeleteRemoteAPIKey(remoteID string) error {
	remoteID = strings.TrimSpace(remoteID)
	if remoteID == "" {
		return nil
	}
	err := keyring.Delete(firstNonEmpty(s.Service, libraryKeyringService), libraryKeyringAccount(remoteID))
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}
