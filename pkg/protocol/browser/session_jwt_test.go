package browser

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jasonczech/apexload/pkg/protocol"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
)

// fakeSalesforce serves the JWT bearer token endpoint, the single-access UI
// bridge, frontdoor and one Lightning page.
type fakeSalesforce struct {
	key      *rsa.PrivateKey
	keyPEM   string
	server   *httptest.Server
	mu       sync.Mutex
	mints    int
	tokens   map[string]string
	otps     map[string]string
	denied   string
	rejected map[string]bool
}

const fakeSalesforceClientID = "consumer-key"

func newFakeSalesforce(t *testing.T) *fakeSalesforce {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeSalesforce{
		key:      key,
		keyPEM:   string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})),
		tokens:   map[string]string{},
		otps:     map[string]string{},
		rejected: map[string]bool{},
	}
	fake.server = httptest.NewServer(http.HandlerFunc(fake.serve))
	t.Cleanup(fake.server.Close)
	return fake
}

func (f *fakeSalesforce) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	writeJSON := func(status int, body any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}
	switch r.URL.Path {
	case "/services/oauth2/token":
		_ = r.ParseForm()
		claims := jwt.MapClaims{}
		_, err := jwt.ParseWithClaims(r.PostForm.Get("assertion"), claims, func(*jwt.Token) (any, error) { return &f.key.PublicKey, nil }, jwt.WithValidMethods([]string{"RS256"}))
		if err != nil || r.PostForm.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" || claims["iss"] != fakeSalesforceClientID || claims["aud"] != f.server.URL {
			writeJSON(http.StatusBadRequest, map[string]string{"error": "invalid_client", "error_description": "invalid assertion"})
			return
		}
		user, _ := claims["sub"].(string)
		if user == f.denied {
			writeJSON(http.StatusBadRequest, map[string]string{"error": "invalid_grant", "error_description": "user hasn't approved this consumer"})
			return
		}
		f.mints++
		token := fmt.Sprintf("token-%d-%s", f.mints, user)
		f.tokens[token] = user
		writeJSON(http.StatusOK, map[string]string{"access_token": token, "instance_url": f.server.URL, "token_type": "Bearer"})
	case salesforceSingleAccessPath:
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		user, known := f.tokens[token]
		if !known || f.rejected[token] {
			writeJSON(http.StatusUnauthorized, []map[string]string{{"errorCode": "INVALID_SESSION_ID", "message": "Session expired or invalid"}})
			return
		}
		otp := fmt.Sprintf("OTP-%d", len(f.otps)+1)
		f.otps[otp] = user
		writeJSON(http.StatusOK, map[string]string{"frontdoor_uri": f.server.URL + "/secur/frontdoor.jsp?otp=" + otp + "&startURL=" + url.QueryEscape(r.PostFormValue("redirect_uri"))})
	case "/secur/frontdoor.jsp":
		user := f.otps[r.URL.Query().Get("otp")]
		delete(f.otps, r.URL.Query().Get("otp"))
		if user == "" {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "sid", Value: "session-" + user, Path: "/", HttpOnly: true})
		http.Redirect(w, r, r.URL.Query().Get("startURL"), http.StatusFound)
	default:
		who := "anonymous"
		if cookie, err := r.Cookie("sid"); err == nil {
			who = cookie.Value
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<html><body><div id="who">%s</div></body></html>`, who)
	}
}

func (f *fakeSalesforce) config(username, harDir string) map[string]any {
	config := map[string]any{
		"base_url":                  f.server.URL,
		"browser_headless":          true,
		"browser_identity_index":    float64(1),
		"browser_identity_username": username,
		salesforceJWTLoginURLKey:    f.server.URL,
		salesforceJWTClientIDKey:    fakeSalesforceClientID,
		salesforceJWTPrivateKeyKey:  f.keyPEM,
		salesforceJWTStartURLKey:    "/lightning/page/home",
	}
	if harDir != "" {
		config["browser_har_capture"] = HARCaptureAlways
		config["browser_har_dir"] = harDir
	}
	return config
}

// Each new context of a salesforce_jwt observer is signed in as its user
// through the UI bridge; a token the bridge rejects is minted once more; a
// cached token signs the next context in; and neither the token nor the
// frontdoor URL reaches the observer's HAR.
func TestSalesforceJWTSessionSignsEachContextIn(t *testing.T) {
	if testing.Short() {
		t.Skip("browser integration")
	}
	fake := newFakeSalesforce(t)
	// The first token the bridge sees is rejected, as an expired cached one.
	fake.rejected["token-1-perf1@acme.test"] = true
	harDir := t.TempDir()
	for round := 1; round <= 2; round++ {
		b := &Browser{}
		if err := b.Init(context.Background(), fake.config("perf1@acme.test", harDir)); err != nil {
			requireBrowserRuntime(t, "", err)
		}
		result, err := b.Execute(context.Background(), &protocol.Action{Name: "Home", Type: "browser_navigate", Target: "/lightning/page/home"})
		if err != nil || !result.Success {
			t.Fatalf("context %d navigate: %+v %v", round, result, err)
		}
		who, err := b.currentPage().Locator("#who").TextContent()
		if err != nil || who != "session-perf1@acme.test" {
			t.Fatalf("context %d signed in as %q (%v)", round, who, err)
		}
		if err := b.Teardown(context.Background()); err != nil {
			t.Fatal(err)
		}
		artifacts := b.TakeBrowserHARArtifacts()
		if len(artifacts) == 0 {
			t.Fatalf("context %d recorded no HAR", round)
		}
		for _, artifact := range artifacts {
			raw, err := os.ReadFile(artifact.Path)
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{"OTP-", "token-", "frontdoor", "singleaccess"} {
				if strings.Contains(string(raw), secret) {
					t.Fatalf("context %d HAR carries %q", round, secret)
				}
			}
		}
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.mints != 2 {
		t.Fatalf("minted %d tokens: want one rejected and one reused across both contexts", fake.mints)
	}
}

// A user the connected app has not pre-authorized fails the sign-in, named,
// with the token endpoint's reason and never the private key.
func TestSalesforceJWTSessionReportsRefusals(t *testing.T) {
	if testing.Short() {
		t.Skip("browser integration")
	}
	fake := newFakeSalesforce(t)
	fake.denied = "denied@acme.test"
	b := &Browser{}
	err := b.Init(context.Background(), fake.config("denied@acme.test", ""))
	if err == nil {
		var result *protocol.Result
		result, err = b.Execute(context.Background(), &protocol.Action{Name: "Home", Type: "browser_navigate", Target: "/lightning/page/home"})
		if err == nil && result != nil && !result.Success {
			err = fmt.Errorf("%s", result.ErrorMessage)
		}
	} else if !strings.Contains(err.Error(), "salesforce_jwt") {
		requireBrowserRuntime(t, "", err)
	}
	defer func() { _ = b.Teardown(context.Background()) }()
	if err == nil || !strings.Contains(err.Error(), "salesforce_jwt sign-in for denied@acme.test: mint an access token") || !strings.Contains(err.Error(), "invalid_grant") || strings.Contains(err.Error(), "PRIVATE KEY") {
		t.Fatalf("refused sign-in = %v", err)
	}
}

// The bridge refuses a frontdoor URL on another host, and its refusals carry
// their code without the bearer token.
func TestSalesforceSingleAccessGuards(t *testing.T) {
	var frontdoor string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer refuse-me" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`[{"errorCode":"INSUFFICIENT_ACCESS","message":"token refuse-me lacks the web scope"}]`))
			return
		}
		_, _ = fmt.Fprintf(w, `{"frontdoor_uri":%q}`, frontdoor)
	}))
	defer server.Close()
	session := &salesforceJWTSession{startURL: "/lightning/page/home"}
	frontdoor = "https://elsewhere.example/secur/frontdoor.jsp?otp=x"
	if _, _, err := session.singleAccess(context.Background(), httpprotocol.OAuthToken{AccessToken: "ok", InstanceURL: server.URL}); err == nil || !strings.Contains(err.Error(), "not on the token's instance") {
		t.Fatalf("foreign frontdoor = %v", err)
	}
	_, rejected, err := session.singleAccess(context.Background(), httpprotocol.OAuthToken{AccessToken: "refuse-me", InstanceURL: server.URL})
	if rejected || err == nil || !strings.Contains(err.Error(), "403: INSUFFICIENT_ACCESS token [redacted] lacks the web scope") {
		t.Fatalf("refusal = %v (rejected %v)", err, rejected)
	}
	if _, _, err := session.singleAccess(context.Background(), httpprotocol.OAuthToken{AccessToken: "ok", InstanceURL: "http://acme.my.salesforce.com"}); err == nil || !strings.Contains(err.Error(), "no usable instance_url") {
		t.Fatalf("plain-http instance = %v", err)
	}
	session.allowedHosts = []string{"acme.my.salesforce.com"}
	if _, _, err := session.singleAccess(context.Background(), httpprotocol.OAuthToken{AccessToken: "ok", InstanceURL: server.URL}); err == nil || !strings.Contains(err.Error(), "instance is outside allowed target hosts") {
		t.Fatalf("instance outside the policy = %v", err)
	}
}

// An observer's salesforce_jwt config needs its username and resolved
// secrets, and https endpoints.
func TestConfigureSalesforceJWTSession(t *testing.T) {
	base := func() map[string]any {
		return map[string]any{
			salesforceJWTLoginURLKey:    "https://login.salesforce.com",
			salesforceJWTClientIDKey:    "consumer-key",
			salesforceJWTPrivateKeyKey:  "-----BEGIN RSA PRIVATE KEY-----\n...",
			salesforceJWTStartURLKey:    "/lightning/page/home",
			"browser_identity_username": "perf1@acme.test",
		}
	}
	b := &Browser{}
	if err := b.configureSalesforceJWTSession(base()); err != nil || b.salesforceJWT == nil || b.salesforceJWT.username != "perf1@acme.test" {
		t.Fatalf("configure = %v, %+v", err, b.salesforceJWT)
	}
	for name, tc := range map[string]struct {
		key, value, want string
	}{
		"no username":       {"browser_identity_username", "", "no identity username"},
		"unresolved key":    {salesforceJWTPrivateKeyKey, "{{secret.sf_jwt_key}}", "private_key names"},
		"unresolved client": {salesforceJWTClientIDKey, "{{secret.sf_client}}", "client_id names"},
		"plain-http login":  {salesforceJWTLoginURLKey, "http://login.salesforce.com", "login_url must be an https origin"},
		"relative start":    {salesforceJWTStartURLKey, "lightning", "start_url must be a path"},
	} {
		config := base()
		config[tc.key] = tc.value
		if err := b.configureSalesforceJWTSession(config); err == nil || !strings.Contains(err.Error(), tc.want) || b.salesforceJWT != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := b.configureSalesforceJWTSession(map[string]any{}); err != nil || b.salesforceJWT != nil {
		t.Fatalf("no session = %v", err)
	}
	// A script's target policy covers the token endpoint too.
	policy := &Browser{allowedTargetHosts: []string{"acme.my.salesforce.com"}}
	if err := policy.configureSalesforceJWTSession(base()); err == nil || !strings.Contains(err.Error(), "login_url is outside allowed target hosts") {
		t.Fatalf("login outside the policy = %v", err)
	}
	config := base()
	config[salesforceJWTLoginURLKey] = "https://acme.my.salesforce.com"
	if err := policy.configureSalesforceJWTSession(config); err != nil || len(policy.salesforceJWT.allowedHosts) != 1 {
		t.Fatalf("login inside the policy = %v", err)
	}
}
