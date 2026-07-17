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

type controllerListenerStateStub struct {
	ready atomic.Bool
}

func (s *controllerListenerStateStub) BoundAddrReady() bool {
	return s != nil && s.ready.Load()
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

func TestBrunoArtifactDirFromEnv(t *testing.T) {
	t.Setenv("APEXLOAD_BRUNO_ARTIFACT_ROOT", " /mnt/apexload/bruno-artifacts ")
	if got := brunoArtifactDirFromEnv(); got != "/mnt/apexload/bruno-artifacts" {
		t.Fatalf("Bruno artifact dir = %q, want trimmed path", got)
	}
}

func TestActiveEndpointPublisherFromEnv(t *testing.T) {
	t.Run("disabled", func(t *testing.T) {
		if publisher, err := activeEndpointPublisherFromEnv(true); err != nil || publisher != nil {
			t.Fatalf("publisher = %#v, err = %v; want disabled", publisher, err)
		}
	})

	t.Run("requires HA", func(t *testing.T) {
		t.Setenv("APEXLOAD_ACTIVE_ENDPOINT_SLICE", "apexload-controller-active")
		if _, err := activeEndpointPublisherFromEnv(false); err == nil {
			t.Fatal("configured active endpoint without HA should fail")
		}
	})

	t.Run("configured", func(t *testing.T) {
		t.Setenv("APEXLOAD_ACTIVE_ENDPOINT_SLICE", "apexload-controller-active")
		t.Setenv("APEXLOAD_ACTIVE_ENDPOINT_SERVICE", "apexload-controller-active")
		t.Setenv("APEXLOAD_POD_NAMESPACE", "apex")
		t.Setenv("APEXLOAD_POD_NAME", "controller-0")
		t.Setenv("APEXLOAD_POD_IP", "10.0.0.12")
		t.Setenv("KUBERNETES_SERVICE_HOST", "10.0.0.1")
		t.Setenv("KUBERNETES_SERVICE_PORT", "443")
		publisher, err := activeEndpointPublisherFromEnv(true)
		if err != nil {
			t.Fatalf("activeEndpointPublisherFromEnv: %v", err)
		}
		if publisher == nil {
			t.Fatal("active endpoint publisher is nil")
		}
	})
}

func TestWaitForControllerListener(t *testing.T) {
	t.Run("waits for bind", func(t *testing.T) {
		state := &controllerListenerStateStub{}
		done := make(chan bool, 1)
		go func() {
			done <- waitForControllerListener(context.Background(), state, time.Millisecond)
		}()
		select {
		case <-done:
			t.Fatal("listener wait returned before bind")
		case <-time.After(10 * time.Millisecond):
		}
		state.ready.Store(true)
		select {
		case ready := <-done:
			if !ready {
				t.Fatal("listener wait returned false after bind")
			}
		case <-time.After(time.Second):
			t.Fatal("listener wait did not observe bind")
		}
	})

	t.Run("cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if waitForControllerListener(ctx, &controllerListenerStateStub{}, time.Millisecond) {
			t.Fatal("cancelled listener wait returned true")
		}
	})
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
