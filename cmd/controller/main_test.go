package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
)

type controllerLifecycleStub struct {
	startErr      error
	shutdownErr   error
	shutdownCalls atomic.Int32
}

func (s *controllerLifecycleStub) Start() error { return s.startErr }

func (s *controllerLifecycleStub) Shutdown(context.Context) error {
	s.shutdownCalls.Add(1)
	return s.shutdownErr
}

func TestServeControllerCoordinatesShutdownAfterServeFailure(t *testing.T) {
	serveErr := errors.New("listen failed")
	shutdownErr := errors.New("cleanup failed")
	server := &controllerLifecycleStub{startErr: serveErr, shutdownErr: shutdownErr}

	err := serveController(context.Background(), server, time.Second)
	if !errors.Is(err, serveErr) || !errors.Is(err, shutdownErr) {
		t.Fatalf("serveController error = %v, want joined serve and shutdown errors", err)
	}
	if calls := server.shutdownCalls.Load(); calls != 1 {
		t.Fatalf("Shutdown calls = %d, want 1", calls)
	}
}

func TestRecorderProxyAddrFromEnv(t *testing.T) {
	t.Setenv("APEXLOAD_RECORDER_PROXY_ADDR", " 127.0.0.1:19081 ")

	if got := recorderProxyAddrFromEnv(); got != "127.0.0.1:19081" {
		t.Fatalf("recorder proxy addr = %q, want trimmed address", got)
	}
}

func TestRecorderProxyAddrFromEnvEmpty(t *testing.T) {
	t.Setenv("APEXLOAD_RECORDER_PROXY_ADDR", " \t ")

	if got := recorderProxyAddrFromEnv(); got != "" {
		t.Fatalf("empty recorder proxy addr = %q, want empty string", got)
	}
}

func TestValidateGeneratorTransportStartupFailsClosedInProduction(t *testing.T) {
	tests := []struct {
		name     string
		targets  []string
		env      string
		security generatorrpc.TransportSecurity
		wantErr  bool
	}{
		{name: "unused transport", env: "production", security: generatorrpc.TransportSecurity{}, wantErr: false},
		{name: "local plaintext", targets: []string{"generator:9090"}, env: "local", security: generatorrpc.TransportSecurity{}, wantErr: false},
		{name: "production plaintext", targets: []string{"generator:9090"}, env: "production", security: generatorrpc.TransportSecurity{}, wantErr: true},
		{name: "production alias and whitespace", targets: []string{"generator:9090"}, env: " ProD ", security: generatorrpc.TransportSecurity{TLS: true}, wantErr: true},
		{name: "production token over plaintext", targets: []string{"generator:9090"}, env: "production", security: generatorrpc.TransportSecurity{Token: true}, wantErr: true},
		{name: "production mTLS", targets: []string{"generator:9090"}, env: "production", security: generatorrpc.TransportSecurity{TLS: true, MutualTLS: true}, wantErr: false},
		{name: "production TLS and token", targets: []string{"generator:9090"}, env: "production", security: generatorrpc.TransportSecurity{TLS: true, Token: true}, wantErr: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateGeneratorTransportStartup(tc.targets, tc.env, tc.security)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateGeneratorTransportStartup() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}
