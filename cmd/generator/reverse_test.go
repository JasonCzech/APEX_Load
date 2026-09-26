package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestReverseConnectorConfigRequiresSecureProductionEnrollment(t *testing.T) {
	base := reverseConnectorConfig{
		URL: "https://controller.example", GeneratorID: "generator-1", ExecutionTargetID: "target-1",
		EnrollmentToken: strings.Repeat("a", 32), Production: true,
	}
	if err := base.validate(); err != nil {
		t.Fatalf("valid config: %v", err)
	}
	insecure := base
	insecure.URL = "http://controller.example"
	if err := insecure.validate(); err == nil {
		t.Fatal("production HTTP controller URL was accepted")
	}
	burst := base
	burst.Lifecycle = "burst"
	if err := burst.validate(); err == nil {
		t.Fatal("burst config without launch nonce was accepted")
	}
}

func TestReverseConnectorBuildsGatewayURL(t *testing.T) {
	config := reverseConnectorConfig{URL: "https://controller.example/base/?old=true"}
	if got, want := config.websocketURL(), "wss://controller.example/base/api/v1/generator-gateway/connect"; got != want {
		t.Fatalf("websocketURL = %q, want %q", got, want)
	}
}

func TestReverseEnrollmentRejectionIsPermanent(t *testing.T) {
	for status, want := range map[int]bool{
		http.StatusUnauthorized:        true,
		http.StatusForbidden:           true,
		http.StatusBadRequest:          true,
		http.StatusConflict:            true,
		http.StatusTooManyRequests:     false,
		http.StatusRequestTimeout:      false,
		http.StatusServiceUnavailable:  false,
		http.StatusInternalServerError: false,
	} {
		if got := reverseEnrollmentRejectionIsPermanent(status); got != want {
			t.Errorf("reverseEnrollmentRejectionIsPermanent(%d) = %v, want %v", status, got, want)
		}
	}
	if reverseConnectMaxBackoff > 15*time.Second {
		t.Fatalf("reverse reconnect backoff cap %s exceeds the HA takeover budget", reverseConnectMaxBackoff)
	}
}

// A throttled enrollment must be retried; only a permanent rejection ends the
// connector.
func TestReverseConnectorRetriesRateLimitedEnrollment(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) == 1 {
			http.Error(w, "generator gateway rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		http.Error(w, "generator enrollment was rejected", http.StatusUnauthorized)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := runReverseGeneratorConnector(ctx, nil, reverseConnectorConfig{
		URL: server.URL, GeneratorID: "generator-1", ExecutionTargetID: "target-1",
		EnrollmentToken: strings.Repeat("a", 32),
	})
	if err == nil || !strings.Contains(err.Error(), "HTTP 401") {
		t.Fatalf("connector error = %v, want the permanent 401 rejection", err)
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("enrollment attempts = %d, want 2 (429 retried, 401 final)", got)
	}
}
