package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
)

const (
	generatorConnectivityModeEnv = "APEXLOAD_GENERATOR_CONNECTIVITY_MODE"
	controllerURLEnv             = "APEXLOAD_CONTROLLER_URL"
	executionTargetIDEnv         = "APEXLOAD_EXECUTION_TARGET_ID"
	gatewayTokenEnv              = "APEXLOAD_GENERATOR_GATEWAY_TOKEN"
	generatorLifecycleEnv        = "APEXLOAD_GENERATOR_LIFECYCLE"
	generatorLaunchNonceEnv      = "APEXLOAD_LAUNCH_NONCE"
)

const (
	reverseConnectInitialBackoff = time.Second
	// reverseConnectMaxBackoff bounds reconnection so a generator whose
	// controller failed over reaches the new leader well inside an HA takeover
	// window rather than sleeping through it.
	reverseConnectMaxBackoff = 15 * time.Second
)

// reverseEnrollmentRejectionIsPermanent reports whether a gateway HTTP status
// means retrying cannot succeed. Rate limiting (429) and request timeouts (408)
// are transient: treating them as permanent made a throttled generator exit.
func reverseEnrollmentRejectionIsPermanent(status int) bool {
	if status < 400 || status >= 500 {
		return false
	}
	switch status {
	case http.StatusRequestTimeout, http.StatusTooManyRequests:
		return false
	}
	return true
}

type reverseConnectorConfig struct {
	URL               string
	GeneratorID       string
	ExecutionTargetID string
	EnrollmentToken   string
	Lifecycle         string
	LaunchNonce       string
	Production        bool
}

func (c reverseConnectorConfig) validate() error {
	if strings.TrimSpace(c.GeneratorID) == "" || strings.TrimSpace(c.ExecutionTargetID) == "" {
		return errors.New("reverse connectivity requires generator and execution target identities")
	}
	if len(strings.TrimSpace(c.EnrollmentToken)) < 32 {
		return errors.New("reverse connectivity requires a target-scoped enrollment token of at least 32 bytes")
	}
	parsed, err := url.Parse(strings.TrimSpace(c.URL))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https" && parsed.Scheme != "ws" && parsed.Scheme != "wss") {
		return errors.New("APEXLOAD_CONTROLLER_URL must be an absolute HTTP(S) or WebSocket URL")
	}
	if c.Production && parsed.Scheme != "https" && parsed.Scheme != "wss" {
		return errors.New("reverse connectivity requires HTTPS/WSS in production")
	}
	if strings.EqualFold(strings.TrimSpace(c.Lifecycle), "burst") && strings.TrimSpace(c.LaunchNonce) == "" {
		return errors.New("burst reverse connectivity requires APEXLOAD_LAUNCH_NONCE")
	}
	return nil
}

func (c reverseConnectorConfig) websocketURL() string {
	parsed, _ := url.Parse(strings.TrimSpace(c.URL))
	if parsed.Scheme == "https" {
		parsed.Scheme = "wss"
	} else if parsed.Scheme == "http" {
		parsed.Scheme = "ws"
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/api/v1/generator-gateway/connect"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func runReverseGeneratorConnector(ctx context.Context, listener *generatorrpc.ConnListener, config reverseConnectorConfig) error {
	if err := config.validate(); err != nil {
		return err
	}
	backoff := reverseConnectInitialBackoff
	for {
		nonceBytes := make([]byte, 32)
		if _, err := rand.Read(nonceBytes); err != nil {
			return fmt.Errorf("generate reverse enrollment session nonce: %w", err)
		}
		headers := http.Header{}
		headers.Set("Authorization", "Bearer "+strings.TrimSpace(config.EnrollmentToken))
		headers.Set("X-Apexload-Generator-ID", strings.TrimSpace(config.GeneratorID))
		headers.Set("X-Apexload-Execution-Target-ID", strings.TrimSpace(config.ExecutionTargetID))
		headers.Set("X-Apexload-Lifecycle", strings.TrimSpace(config.Lifecycle))
		headers.Set("X-Apexload-Session-Nonce", base64.RawURLEncoding.EncodeToString(nonceBytes))
		headers.Set("X-Apexload-Session-Timestamp", strconv.FormatInt(time.Now().UTC().Unix(), 10))
		if config.LaunchNonce != "" {
			headers.Set("X-Apexload-Launch-Nonce", strings.TrimSpace(config.LaunchNonce))
		}
		ws, response, err := websocket.DefaultDialer.DialContext(ctx, config.websocketURL(), headers)
		if err == nil {
			backoff = reverseConnectInitialBackoff
			conn := generatorrpc.NewWebSocketConn(ws)
			if err := listener.Offer(ctx, conn); err != nil {
				_ = conn.Close()
				return err
			}
			select {
			case <-ctx.Done():
				_ = conn.Close()
				return ctx.Err()
			case <-conn.Done():
			}
		} else if response != nil {
			_ = response.Body.Close()
			if reverseEnrollmentRejectionIsPermanent(response.StatusCode) {
				return fmt.Errorf("reverse generator enrollment rejected with HTTP %d", response.StatusCode)
			}
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		backoff = min(backoff*2, reverseConnectMaxBackoff)
	}
}
