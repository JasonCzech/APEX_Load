package ciclient

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type Discovery struct {
	Audience string
	Modes    []string
}

type Token struct {
	Value     string
	ExpiresAt time.Time
	ExpiresIn int64
}

// AuthPorts bind controller transport and the environment-specific identity
// provider. Session owns only discovery/token freshness, not environment access.
type AuthPorts struct {
	APIKey             func() string
	BearerToken        func() string
	SetBearerToken     func(string)
	Discover           func(context.Context) (Discovery, error)
	RequestIDToken     func(context.Context, string) (string, error)
	Exchange           func(context.Context, string, string) (Token, error)
	ConfigurationError func(string) error
	Now                func() time.Time
}

type Session struct {
	Mode, ProjectID string
	Ports           AuthPorts
	expiresAt       time.Time
	discovery       *Discovery
}

func (a *Session) now() time.Time {
	if a.Ports.Now != nil {
		return a.Ports.Now()
	}
	return time.Now()
}

func (a *Session) configurationError(message string) error {
	if a.Ports.ConfigurationError != nil {
		return a.Ports.ConfigurationError(message)
	}
	return fmt.Errorf("%s", message)
}

func (a *Session) Ensure(ctx context.Context) error {
	if a.Mode == "api-key" {
		if strings.TrimSpace(a.Ports.APIKey()) == "" {
			return a.configurationError("APEXLOAD_API_KEY is not set")
		}
		return nil
	}
	if a.Ports.BearerToken() != "" && a.expiresAt.Sub(a.now()) > time.Minute {
		return nil
	}
	if a.discovery == nil {
		discovery, err := a.Ports.Discover(ctx)
		if err != nil {
			return fmt.Errorf("discover controller CI authentication: %w", err)
		}
		oidc := false
		for _, mode := range discovery.Modes {
			if strings.EqualFold(strings.TrimSpace(mode), "oidc") {
				oidc = true
				break
			}
		}
		if !oidc || strings.TrimSpace(discovery.Audience) == "" {
			return a.configurationError("controller does not advertise GitHub Actions OIDC")
		}
		a.discovery = &discovery
	}
	idToken, err := a.Ports.RequestIDToken(ctx, a.discovery.Audience)
	if err != nil {
		return err
	}
	exchanged, err := a.Ports.Exchange(ctx, idToken, a.ProjectID)
	if err != nil {
		return fmt.Errorf("exchange GitHub OIDC token: %w", err)
	}
	a.Ports.SetBearerToken(exchanged.Value)
	a.expiresAt = exchanged.ExpiresAt
	if a.expiresAt.IsZero() && exchanged.ExpiresIn > 0 {
		a.expiresAt = a.now().Add(time.Duration(exchanged.ExpiresIn) * time.Second)
	}
	if a.expiresAt.IsZero() {
		a.expiresAt = a.now().Add(5 * time.Minute)
	}
	return nil
}

func (a *Session) RefreshUnauthorized() bool {
	if a.Mode == "api-key" {
		return false
	}
	a.Ports.SetBearerToken("")
	a.expiresAt = time.Time{}
	return true
}
