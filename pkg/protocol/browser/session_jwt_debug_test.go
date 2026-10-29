package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
	playwright "github.com/mxschmitt/playwright-go"
)

// A failed token fetch must not share its debugger-only error with normal
// waiters through OAuth singleflight, even with the same credential key.
func TestSalesforceJWTDebuggerTokenFailureIsIsolated(t *testing.T) {
	fake := newFakeSalesforce(t)
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		arrived <- struct{}{}
		<-release
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant", "error_description": fake.keyPEM})
	}))
	defer server.Close()
	base := salesforceJWTSession{loginURL: server.URL, clientID: fakeSalesforceClientID, privateKey: fake.keyPEM, username: "debug@example.test", startURL: "/"}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	type outcome struct {
		debug bool
		err   error
	}
	results := make(chan outcome, 2)
	for _, debug := range []bool{false, true} {
		s := base
		s.unredactedDebug = debug
		go func() { _, err := s.frontdoorURL(ctx); results <- outcome{debug, err} }()
	}
	for range 2 {
		select {
		case <-arrived:
		case <-ctx.Done():
			close(release)
			t.Fatal("debug and ordinary requests shared a failing token fetch")
		}
	}
	close(release)
	for range 2 {
		got := <-results
		if got.err == nil {
			t.Fatal("expected token refusal")
		}
		contains := strings.Contains(got.err.Error(), "-----BEGIN RSA PRIVATE KEY-----")
		if contains != got.debug {
			t.Fatalf("debug=%v token error = %v", got.debug, got.err)
		}
	}
}

func TestSalesforceJWTDebuggerBridgeFailureDisclosure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "denied", "error_description": "echo " + strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")})
	}))
	defer server.Close()
	const token = "bridge-token-secret"
	for _, debug := range []bool{false, true} {
		session := &salesforceJWTSession{startURL: "/", unredactedDebug: debug}
		_, rejected, err := session.singleAccess(context.Background(), httpprotocol.OAuthToken{AccessToken: token, InstanceURL: server.URL})
		if rejected || err == nil || strings.Contains(err.Error(), token) != debug {
			t.Fatalf("debug=%v rejected=%v error=%v", debug, rejected, err)
		}
	}
}

type signInErrorBrowser struct {
	playwright.Browser
	context *signInErrorContext
}

func (b *signInErrorBrowser) NewContext(...playwright.BrowserNewContextOptions) (playwright.BrowserContext, error) {
	return b.context, nil
}

type signInErrorContext struct {
	playwright.BrowserContext
	page   *signInErrorPage
	mu     sync.Mutex
	closed bool
}

func (c *signInErrorContext) Route(any, func(playwright.Route), ...int) error { return nil }
func (c *signInErrorContext) NewPage() (playwright.Page, error)               { return c.page, nil }
func (c *signInErrorContext) Close(...playwright.BrowserContextCloseOptions) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

type signInErrorPage struct{ playwright.Page }

func (*signInErrorPage) Goto(url string, _ ...playwright.PageGotoOptions) (playwright.Response, error) {
	return nil, fmt.Errorf("net::ERR_ABORTED opening %s", url)
}

func TestSalesforceJWTDebuggerNavigationFailureDisclosure(t *testing.T) {
	fake := newFakeSalesforce(t)
	for _, debug := range []bool{false, true} {
		b := &Browser{unredactedDebug: debug}
		if err := b.configureSalesforceJWTSession(fake.config("navigation@example.test", "")); err != nil {
			t.Fatal(err)
		}
		ctx := &signInErrorContext{page: &signInErrorPage{}}
		_, err := b.salesforceJWTStorageState(context.Background(), &signInErrorBrowser{context: ctx})
		if err == nil || strings.Contains(err.Error(), "OTP-") != debug {
			t.Fatalf("debug=%v navigation error=%v", debug, err)
		}
		if !ctx.closed {
			t.Fatal("bridge context was not closed")
		}
	}
	wrapped := fmt.Errorf("frontdoor?otp=secret: %w", context.Canceled)
	for _, debug := range []bool{false, true} {
		err := salesforceNavigationError("operator", wrapped, debug)
		if !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "otp=secret") != debug {
			t.Fatalf("debug=%v cancellation error=%v", debug, err)
		}
	}
}

func TestSalesforceJWTDebugModeIsInternalConfiguration(t *testing.T) {
	b := &Browser{}
	if err := b.applyConfig(map[string]any{protocol.ReplayUnredactedDebugConfigKey: true}); err != nil {
		t.Fatal(err)
	}
	if !b.debugDisclosureEnabled() {
		t.Fatal("debug flag not applied")
	}
}
