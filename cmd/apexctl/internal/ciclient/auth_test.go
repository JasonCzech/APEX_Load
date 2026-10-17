package ciclient

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSessionRefreshesTokenAndReusesDiscovery(t *testing.T) {
	now := time.Now()
	bearer := ""
	discovers, tokens, exchanges := 0, 0, 0
	session := &Session{Mode: "oidc", ProjectID: "project", Ports: AuthPorts{
		Now: func() time.Time { return now }, BearerToken: func() string { return bearer }, SetBearerToken: func(value string) { bearer = value },
		Discover: func(context.Context) (Discovery, error) {
			discovers++
			return Discovery{Audience: "audience", Modes: []string{" OIDC "}}, nil
		},
		RequestIDToken: func(_ context.Context, audience string) (string, error) {
			tokens++
			if audience != "audience" {
				t.Errorf("audience=%s", audience)
			}
			return "identity", nil
		},
		Exchange: func(_ context.Context, token, project string) (Token, error) {
			exchanges++
			if token != "identity" || project != "project" {
				t.Errorf("exchange token=%s project=%s", token, project)
			}
			return Token{Value: "access", ExpiresIn: 600}, nil
		},
	}}
	for range 2 {
		if err := session.Ensure(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if discovers != 1 || tokens != 1 || exchanges != 1 {
		t.Fatalf("discovery=%d tokens=%d exchange=%d", discovers, tokens, exchanges)
	}
	now = now.Add(9 * time.Minute)
	if err := session.Ensure(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !session.RefreshUnauthorized() || bearer != "" {
		t.Fatal("unauthorized token retained")
	}
	if err := session.Ensure(t.Context()); err != nil {
		t.Fatal(err)
	}
	if discovers != 1 || tokens != 3 || exchanges != 3 {
		t.Fatalf("discovery=%d tokens=%d exchange=%d", discovers, tokens, exchanges)
	}
}

func TestSessionAPIKeyAndUnsupportedDiscoveryAreConfigurationFailures(t *testing.T) {
	configuration := errors.New("configuration")
	session := &Session{Mode: "api-key", Ports: AuthPorts{APIKey: func() string { return "" }, ConfigurationError: func(string) error { return configuration }}}
	if err := session.Ensure(t.Context()); err != configuration {
		t.Fatalf("missing key=%v", err)
	}
	session.Ports.APIKey = func() string { return "key" }
	if err := session.Ensure(t.Context()); err != nil {
		t.Fatal(err)
	}
	if session.RefreshUnauthorized() {
		t.Fatal("API key authentication refreshed")
	}
	session.Mode = "oidc"
	session.Ports.BearerToken = func() string { return "" }
	session.Ports.Discover = func(context.Context) (Discovery, error) {
		return Discovery{Audience: "audience", Modes: []string{"api-key"}}, nil
	}
	if err := session.Ensure(t.Context()); err != configuration {
		t.Fatalf("unsupported discovery=%v", err)
	}
}

func TestSessionFailedDiscoveryCanRetryAndFallbackExpiryIsBounded(t *testing.T) {
	now := time.Now()
	bearer := ""
	attempts := 0
	session := &Session{Mode: "oidc", Ports: AuthPorts{
		Now: func() time.Time { return now }, BearerToken: func() string { return bearer }, SetBearerToken: func(value string) { bearer = value },
		Discover: func(context.Context) (Discovery, error) {
			attempts++
			if attempts == 1 {
				return Discovery{}, errors.New("unavailable")
			}
			return Discovery{Audience: "audience", Modes: []string{"oidc"}}, nil
		},
		RequestIDToken: func(context.Context, string) (string, error) { return "identity", nil },
		Exchange:       func(context.Context, string, string) (Token, error) { return Token{Value: "access"}, nil },
	}}
	if session.Ensure(t.Context()) == nil {
		t.Fatal("discovery failure ignored")
	}
	if err := session.Ensure(t.Context()); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || !session.expiresAt.Equal(now.Add(5*time.Minute)) {
		t.Fatalf("attempts=%d expiry=%v", attempts, session.expiresAt)
	}
}
