package api

import (
	"context"
	"errors"
	"testing"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/secretstore"
)

// devSecretStore is a minimal secretstore.Store whose Get outcome is scripted.
type devSecretStore struct {
	value string
	err   error
	calls int
}

func (s *devSecretStore) Get(context.Context, string, string) (string, secretstore.Metadata, error) {
	s.calls++
	return s.value, secretstore.Metadata{}, s.err
}
func (s *devSecretStore) Create(context.Context, string, string, string, string) (secretstore.Metadata, error) {
	return secretstore.Metadata{}, errors.New("not supported")
}
func (s *devSecretStore) Replace(context.Context, string, string, string, string) (secretstore.Metadata, error) {
	return secretstore.Metadata{}, errors.New("not supported")
}
func (s *devSecretStore) List(context.Context, string) ([]secretstore.Metadata, error) {
	return nil, nil
}
func (s *devSecretStore) Writable() bool   { return false }
func (s *devSecretStore) Provider() string { return "test" }

// Placeholder substitution must never mask a CONFIGURED secret, and must never
// fire on a provider failure: substituting there is cached for the whole run and
// would silently send a fake credential in place of a real one.
func TestDevSecretResolverPrefersStoreAndFailsClosedOnStoreError(t *testing.T) {
	tests := []struct {
		name      string
		store     *devSecretStore
		wantValue string
		wantOK    bool
	}{
		{
			name:      "configured secret wins over the placeholder",
			store:     &devSecretStore{value: "real-token"},
			wantValue: "real-token",
			wantOK:    true,
		},
		{
			name:      "genuine miss falls back to the placeholder",
			store:     &devSecretStore{err: secretstore.ErrNotFound},
			wantValue: "apexload-dev-secret",
			wantOK:    true,
		},
		{
			name:   "provider failure fails closed",
			store:  &devSecretStore{err: errors.New("provider timeout")},
			wantOK: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("APEXLOAD_ENV", "local")
			t.Setenv(devFakeSecretsEnv, "true")
			runner := &TestRunner{projectSecretStore: test.store}

			resolver := runner.secretResolverForProject("project-1")
			value, ok := engine.ResolveSecret(context.Background(), resolver, "API_TOKEN")

			if ok != test.wantOK {
				t.Fatalf("resolved ok = %v, want %v (value=%q)", ok, test.wantOK, value)
			}
			if test.wantOK && value != test.wantValue {
				t.Fatalf("resolved value = %q, want %q", value, test.wantValue)
			}
			if test.store.calls == 0 {
				t.Fatal("the real secret store was never consulted")
			}
		})
	}
}

// Without the explicit opt-in the placeholder path must not be reachable at all.
func TestDevSecretResolverNotUsedWithoutOptIn(t *testing.T) {
	t.Setenv("APEXLOAD_ENV", "local")
	t.Setenv(devFakeSecretsEnv, "")
	store := &devSecretStore{err: secretstore.ErrNotFound}
	runner := &TestRunner{projectSecretStore: store}

	resolver := runner.secretResolverForProject("project-1")
	value, ok := engine.ResolveSecret(context.Background(), resolver, "API_TOKEN")
	if ok && value == "apexload-dev-secret" {
		t.Fatal("placeholder substitution happened without the explicit opt-in")
	}
}
