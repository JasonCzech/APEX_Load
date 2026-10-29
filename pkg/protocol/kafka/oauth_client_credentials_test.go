package kafka

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestExplicitOAuthValidationCannotFallBackToAmbientIdentity(t *testing.T) {
	valid := config{OAuthMode: "client_credentials", OAuthTokenURL: "https://identity.example.test/token", OAuthClientID: "client", OAuthClientSecret: "credential-canary", OAuthAuthenticationMethod: "client_secret_basic", SASLMechanism: "oauthbearer", SecurityProtocol: "sasl_ssl", AllowedTargetHosts: []string{"identity.example.test"}}
	if err := validateExplicitOAuth(valid); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*config){
		"mode":             func(c *config) { c.OAuthMode = "" },
		"masked":           func(c *config) { c.OAuthClientSecret = "***" },
		"unresolved":       func(c *config) { c.OAuthClientSecret = "{{secret.missing}}" },
		"ambient profile":  func(c *config) { c.Profile = "eventhubs" },
		"unverified TLS":   func(c *config) { c.TLSInsecureSkipVerify = true },
		"plaintext broker": func(c *config) { c.SecurityProtocol = "sasl_plaintext" },
		"plaintext token":  func(c *config) { c.OAuthTokenURL = "http://identity.example.test/token" },
		"userinfo":         func(c *config) { c.OAuthTokenURL = "https://client:credential-canary@identity.example.test/token" },
		"fragment":         func(c *config) { c.OAuthTokenURL += "#fragment" },
		"foreign host":     func(c *config) { c.OAuthTokenURL = "https://outside.example.test/token" },
		"callback method":  func(c *config) { c.OAuthAuthenticationMethod = "uploaded.JavaClass" },
	} {
		t.Run(name, func(t *testing.T) {
			c := valid
			mutate(&c)
			if !hasExplicitOAuth(c) {
				t.Fatal("explicit settings could fall through to ambient credentials")
			}
			err := validateExplicitOAuth(c)
			if err == nil || strings.Contains(err.Error(), "credential-canary") {
				t.Fatal("missing credential-safe validation")
			}
		})
	}
}

func TestExplicitOAuthRetainsTLSVerification(t *testing.T) {
	contacted := false
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { contacted = true }))
	defer server.Close()
	c := config{OAuthMode: "client_credentials", OAuthTokenURL: server.URL, OAuthClientID: "client", OAuthClientSecret: "credential-canary", SASLMechanism: "oauthbearer", SecurityProtocol: "sasl_ssl", AllowedTargetHosts: []string{"127.0.0.1"}}
	_, err := explicitOAuthToken(context.Background(), c)
	if err == nil || contacted {
		t.Fatal("untrusted token endpoint received credentials")
	}
	if strings.Contains(err.Error(), "credential-canary") {
		t.Fatal("TLS failure leaked credential")
	}
}

func TestExplicitOAuthDebugErrorsAreIsolatedAcrossSingleflight(t *testing.T) {
	const secret = "oauth-client-private"
	arrived, release := make(chan struct{}, 2), make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		arrived <- struct{}{}
		<-release
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `{"error":"invalid_client","error_description":"rejected %s"}`, secret)
	}))
	defer server.Close()
	base := config{OAuthMode: "client_credentials", OAuthTokenURL: server.URL, OAuthClientID: "client", OAuthClientSecret: secret, SASLMechanism: "oauthbearer", SecurityProtocol: "sasl_ssl", AllowedTargetHosts: []string{"127.0.0.1"}}
	if err := validateExplicitOAuth(base); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	type outcome struct {
		debug bool
		err   error
	}
	results := make(chan outcome, 2)
	for _, debug := range []bool{false, true} {
		c := base
		c.UnredactedDebugErrors = debug
		go func() {
			_, err := explicitOAuthTokenWithClient(ctx, c, server.Client())
			results <- outcome{debug, err}
		}()
	}
	for i := 0; i < 2; i++ {
		select {
		case <-arrived:
		case <-ctx.Done():
			close(release)
			t.Fatal("debug and ordinary requests shared a singleflight")
		}
	}
	close(release)
	for i := 0; i < 2; i++ {
		got := <-results
		if got.err == nil || strings.Contains(got.err.Error(), secret) != got.debug {
			t.Fatalf("debug=%t err=%v", got.debug, got.err)
		}
	}
}

func TestKafkaInitCarriesInternalDebugCapability(t *testing.T) {
	for _, debug := range []bool{false, true} {
		instance := &Kafka{name: "kafka"}
		if err := instance.Init(context.Background(), map[string]any{"kafka_mock": true, protocol.ReplayUnredactedDebugConfigKey: debug}); err != nil {
			t.Fatal(err)
		}
		if instance.cfg.UnredactedDebugErrors != debug {
			t.Fatalf("debug capability = %t, want %t", instance.cfg.UnredactedDebugErrors, debug)
		}
		if err := instance.Teardown(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}
