package http

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net"
	stdhttp "net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

type managedPoolRequest struct {
	origin, path, cookie, authorization string
	marker                              []string
	major                               int
}

type managedPoolWire struct {
	mu       sync.Mutex
	requests []managedPoolRequest
}

func (wire *managedPoolWire) record(origin string, r *stdhttp.Request) {
	wire.mu.Lock()
	defer wire.mu.Unlock()
	wire.requests = append(wire.requests, managedPoolRequest{
		origin: origin, path: r.URL.Path, marker: append([]string(nil), r.Header.Values(ManagedRunHeader)...),
		major: r.ProtoMajor, cookie: r.Header.Get("Cookie"), authorization: r.Header.Get("Authorization"),
	})
}

func (wire *managedPoolWire) take() []managedPoolRequest {
	wire.mu.Lock()
	defer wire.mu.Unlock()
	requests := append([]managedPoolRequest(nil), wire.requests...)
	wire.requests = nil
	return requests
}

func assertManagedPoolRequest(t *testing.T, r managedPoolRequest, policy string, marker []string) {
	t.Helper()
	major := 2
	if policy == VersionHTTP11 {
		major = 1
	}
	if r.major != major || !reflect.DeepEqual(r.marker, marker) {
		t.Fatalf("wire %s%s: major=%d marker=%q, want major=%d marker=%q", r.origin, r.path, r.major, r.marker, major, marker)
	}
}

func executeManagedPoolAction(t *testing.T, h *HTTP, action *protocol.Action) *protocol.Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := h.Execute(ctx, action)
	if err != nil || result == nil || !result.Success {
		t.Fatalf("Execute: result=%+v err=%v", result, err)
	}
	return result
}

func TestManagedRunHeaderVersionPoolsAndRedirects(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprintf("enabled=%t", enabled), func(t *testing.T) {
			wire := &managedPoolWire{}
			var approvedURL, foreignURL string
			foreign := versionTLS(t, stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
				wire.record("foreign", r)
				stdhttp.Redirect(w, r, approvedURL+"/final", stdhttp.StatusFound)
			}))
			approved := versionTLS(t, stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
				wire.record("approved", r)
				switch r.URL.Path {
				case "/redirect":
					stdhttp.Redirect(w, r, foreignURL+"/middle", stdhttp.StatusFound)
				case "/digest":
					if !strings.HasPrefix(r.Header.Get("Authorization"), "Digest ") {
						w.Header().Set("WWW-Authenticate", `Digest realm="apex", nonce="synthetic", qop="auth"`)
						w.WriteHeader(stdhttp.StatusUnauthorized)
						return
					}
					fmt.Fprint(w, "digest accepted")
				default:
					fmt.Fprint(w, "final")
				}
			}))
			approvedURL, foreignURL = approved.URL, foreign.URL
			config := map[string]any{"http_version": "1.1", "auth_type": "digest", "auth_username": "synthetic-user", "auth_password": "synthetic-password"}
			if enabled {
				config["apexload_run_correlation_id"] = "run-123"
				config["apexload_run_correlation_origins"] = []string{approved.URL}
			}
			h := versionHTTP(t, config)
			for _, policy := range []string{"2", "auto", "1.1", "2"} {
				for _, path := range []string{"/redirect", "/digest"} {
					action := &protocol.Action{Type: "GET", Target: approved.URL + path, HTTPVersion: versionOverride(t, policy)}
					if !enabled {
						action.Headers = map[string]string{ManagedRunHeader: "authored-baseline"}
					}
					result := executeManagedPoolAction(t, h, action)
					wantVersion := policy
					if policy == "auto" {
						wantVersion = "2"
					}
					if result.HTTPVersionSource != "wire" || result.HTTPVersionNegotiated != wantVersion {
						t.Fatalf("policy receipt lost: %+v", result.ProtocolEvidence)
					}
					requests := wire.take()
					wantCount := 3
					if path == "/digest" {
						wantCount = 2
					}
					if len(requests) != wantCount {
						t.Fatalf("%s flow: %+v", path, requests)
					}
					for _, request := range requests {
						var marker []string
						if enabled && request.origin == "approved" {
							marker = []string{"run-123"}
						} else if !enabled {
							marker = []string{"authored-baseline"}
						}
						assertManagedPoolRequest(t, request, policy, marker)
					}
					if path == "/redirect" && (requests[0].path != "/redirect" || requests[1].origin != "foreign" || requests[2].path != "/final") {
						t.Fatalf("redirect route changed: %+v", requests)
					}
					if path == "/digest" && (requests[0].authorization != "" || !strings.HasPrefix(requests[1].authorization, "Digest ")) {
						t.Fatalf("actual digest retry missing: %+v", requests)
					}
				}
			}
		})
	}
}

func TestManagedRunHeaderVersionPoolsResourcesAndMetaRefresh(t *testing.T) {
	wire := &managedPoolWire{}
	var approvedURL, foreignURL string
	foreign := versionTLS(t, stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		wire.record("foreign", r)
		if r.URL.Path == "/middle" {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprintf(w, `<meta http-equiv="refresh" content="0; url=%s/final">`, approvedURL)
			return
		}
		fmt.Fprint(w, "foreign asset")
	}))
	approved := versionTLS(t, stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		wire.record("approved", r)
		switch r.URL.Path {
		case "/page":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprintf(w, `<html><img src="/automatic"><img src="%s/foreign-asset"></html>`, foreignURL)
		case "/refresh":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprintf(w, `<meta http-equiv="refresh" content="0; url=%s/middle">`, foreignURL)
		default:
			fmt.Fprint(w, "asset")
		}
	}))
	approvedURL, foreignURL = approved.URL, foreign.URL
	h := versionHTTP(t, map[string]any{
		"http_version": "1.1", "http_download_non_html_resources": true, "max_meta_refresh": 3,
		"apexload_run_correlation_id": "run-123", "apexload_run_correlation_origins": []string{approved.URL},
	})
	for _, policy := range []string{"2", "auto", "1.1"} {
		for _, path := range []string{"/page", "/refresh"} {
			action := &protocol.Action{Type: "GET", Target: approved.URL + path, HTTPVersion: versionOverride(t, policy)}
			if path == "/page" {
				action.Params = map[string]any{"resources": []any{map[string]any{"url": "/manual"}}}
			}
			result := executeManagedPoolAction(t, h, action)
			wantVersion := policy
			if policy == "auto" {
				wantVersion = "2"
			}
			if result.HTTPVersionSource != "wire" || result.HTTPVersionNegotiated != wantVersion {
				t.Fatalf("page receipt lost: %+v", result.ProtocolEvidence)
			}
			requests := wire.take()
			wantPaths := map[string]int{"/page": 1, "/automatic": 1, "/manual": 1, "/foreign-asset": 1}
			if path == "/refresh" {
				wantPaths = map[string]int{"/refresh": 1, "/middle": 1, "/final": 1}
			}
			paths := make(map[string]int)
			for _, request := range requests {
				paths[request.path]++
				var marker []string
				if request.origin == "approved" {
					marker = []string{"run-123"}
				}
				assertManagedPoolRequest(t, request, policy, marker)
			}
			if !reflect.DeepEqual(paths, wantPaths) {
				t.Fatalf("%s actual flow paths=%v, want %v", path, paths, wantPaths)
			}
		}
	}
}

func TestManagedRunHeaderOAuthPoolsExcludeApplicationMarker(t *testing.T) {
	wire := &managedPoolWire{}
	var formErrors atomic.Int64
	server := versionTLS(t, stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		wire.record("approved", r)
		if strings.HasPrefix(r.URL.Path, "/token/") {
			if err := r.ParseForm(); err != nil || r.Method != "POST" || r.Form.Get("grant_type") != OAuthGrantClientCredentials || r.Form.Get("client_id") != "client"+r.URL.Path || r.Form.Get("client_secret") != "synthetic-secret" {
				formErrors.Add(1)
				w.WriteHeader(400)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "synthetic-token", "token_type": "Bearer", "expires_in": 300})
			return
		}
		if r.URL.Path == "/seed" {
			stdhttp.SetCookie(w, &stdhttp.Cookie{Name: "session", Value: "synthetic", Path: "/"})
		}
		fmt.Fprint(w, "application")
	}))
	h := versionHTTP(t, map[string]any{
		"http_version": "1.1", "apexload_run_correlation_id": "run-123", "apexload_run_correlation_origins": []string{server.URL},
	})
	// Trust this real TLS fixture without enabling insecure verification on any
	// credential transport. No requests have begun when the fixture is installed.
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	for _, pool := range h.policyPools {
		pool.oauthTransport.TLSClientConfig.RootCAs = roots
		if pool.oauthTransport.TLSClientConfig.InsecureSkipVerify {
			t.Fatal("OAuth inherited insecure application TLS policy")
		}
	}
	executeVersion(t, h, server.URL+"/seed", "1.1")
	wire.take()
	for _, policy := range []string{"2", "auto", "1.1"} {
		path := "/token/" + policy
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		token, err := FetchOAuth2Token(ctx, OAuthTokenRequest{
			TokenURL: server.URL + path, GrantType: OAuthGrantClientCredentials, ClientID: "client" + path,
			ClientSecret: "synthetic-secret", HTTPClient: h.forHTTPVersion(policy).OAuthHTTPClient(),
		})
		cancel()
		if err != nil || token.AccessToken != "synthetic-token" {
			t.Fatalf("policy=%s token=%+v err=%v", policy, token, err)
		}
		executeVersion(t, h, server.URL+"/application", policy)
		requests := wire.take()
		if len(requests) != 2 || requests[0].path != path || requests[1].path != "/application" {
			t.Fatalf("actual OAuth/application requests missing: %+v", requests)
		}
		assertManagedPoolRequest(t, requests[0], policy, nil)
		assertManagedPoolRequest(t, requests[1], policy, []string{"run-123"})
		if requests[0].cookie != "" || requests[1].cookie != "session=synthetic" {
			t.Fatalf("credential/application cookie isolation lost: %+v", requests)
		}
	}
	if formErrors.Load() != 0 {
		t.Fatal("OAuth grant was not sent to actual credential endpoint")
	}
}

func managedPoolPreflightTLS(t *testing.T, connections *atomic.Int64, handler stdhttp.Handler) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	server.EnableHTTP2 = true
	server.Config.ConnState = func(_ net.Conn, state stdhttp.ConnState) {
		if state == stdhttp.StateNew {
			connections.Add(1)
		}
	}
	server.StartTLS()
	t.Cleanup(server.Close)
	return server
}

func TestManagedRunHeaderSharedSessionPreflightRejectsConflicts(t *testing.T) {
	wire := &managedPoolWire{}
	var connections atomic.Int64
	approved := managedPoolPreflightTLS(t, &connections, stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		wire.record("approved", r)
		fmt.Fprint(w, "ok")
	}))
	foreign := managedPoolPreflightTLS(t, &connections, stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		wire.record("foreign", r)
		fmt.Fprint(w, "ok")
	}))
	cases := []struct {
		name       string
		foreign    bool
		wantReject bool
		configure  func(map[string]any, *protocol.Action)
	}{
		{"action map conflict", false, true, func(_ map[string]any, a *protocol.Action) {
			a.Headers = map[string]string{"x-apex-test-run": "another-run"}
		}},
		{"ordered duplicate conflict", false, true, func(_ map[string]any, a *protocol.Action) {
			a.HeaderList = protocol.HeaderList{{Name: ManagedRunHeader, Value: "run-123"}, {Name: "x-APEX-test-run", Value: "another-run"}}
		}},
		{"resolved default map conflict", false, true, func(_ map[string]any, a *protocol.Action) {
			a.ResolvedDefaultHeaders = true
			a.DefaultHeaders = map[string]string{ManagedRunHeader: "another-run"}
		}},
		{"resolved default list conflict", false, true, func(_ map[string]any, a *protocol.Action) {
			a.ResolvedDefaultHeaders = true
			a.DefaultHeaderList = protocol.HeaderList{{Name: ManagedRunHeader, Value: "another-run"}}
		}},
		{"session default conflict", false, true, func(c map[string]any, _ *protocol.Action) {
			c["default_headers"] = map[string]string{ManagedRunHeader: "another-run"}
		}},
		{"matching duplicate normalized", false, false, func(_ map[string]any, a *protocol.Action) {
			a.HeaderList = protocol.HeaderList{{Name: ManagedRunHeader, Value: "run-123"}, {Name: "x-APEX-test-run", Value: "run-123"}}
		}},
		{"action shadows conflicting default", false, false, func(c map[string]any, a *protocol.Action) {
			c["default_headers"] = map[string]string{ManagedRunHeader: "another-run"}
			a.Headers = map[string]string{ManagedRunHeader: "run-123"}
		}},
		{"unapproved conflict stripped", true, false, func(_ map[string]any, a *protocol.Action) {
			a.Headers = map[string]string{ManagedRunHeader: "another-run"}
		}},
	}
	for _, policy := range []string{"2", "auto", "1.1"} {
		for _, tc := range cases {
			t.Run(policy+"/"+tc.name, func(t *testing.T) {
				config := map[string]any{"http_version": "1.1", "apexload_run_correlation_id": "run-123", "apexload_run_correlation_origins": []string{approved.URL}}
				action := &protocol.Action{Type: "GET", Target: approved.URL, HTTPVersion: versionOverride(t, policy)}
				if tc.foreign {
					action.Target = foreign.URL
				}
				tc.configure(config, action)
				h := versionHTTP(t, config)
				before, err := json.Marshal(action)
				if err != nil {
					t.Fatal(err)
				}
				dials := connections.Load()
				for range 3 {
					err = h.ValidateSharedSessionAction(action)
					if tc.wantReject && (err == nil || !strings.Contains(err.Error(), "conflicts")) || !tc.wantReject && err != nil {
						t.Fatalf("preflight err=%v, wantReject=%t", err, tc.wantReject)
					}
				}
				after, marshalErr := json.Marshal(action)
				if marshalErr != nil || string(before) != string(after) || len(wire.take()) != 0 || connections.Load() != dials {
					t.Fatal("private preflight mutated input or performed traffic/dials")
				}
				if tc.wantReject {
					return
				}
				executeManagedPoolAction(t, h, action)
				requests := wire.take()
				if len(requests) != 1 {
					t.Fatalf("admitted request missing: %+v", requests)
				}
				marker := []string{"run-123"}
				if tc.foreign {
					marker = nil
				}
				assertManagedPoolRequest(t, requests[0], policy, marker)
			})
		}
	}
}
