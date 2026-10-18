package http

import (
	"context"
	"fmt"
	stdhttp "net/http"
	"slices"
	"sync"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func lazyVersionHTTP(t *testing.T) *HTTP {
	t.Helper()
	h := &HTTP{}
	if err := h.Init(context.Background(), map[string]any{"tls_insecure_skip_verify": true}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Teardown(context.Background()) })
	return h
}

// Init builds only the session pool's transport; another policy's pool and
// every OAuth client are built when a request or token call first uses them,
// with the same ALPN, hardening and connection budget as before.
func TestHTTPVersionPoolsBuildOnFirstUse(t *testing.T) {
	server := versionTLS(t, stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) { fmt.Fprint(w, r.ProtoMajor) }))
	h := lazyVersionHTTP(t)
	owners := func() int { return len(h.connections.snapshotOwners()) }
	if n := owners(); n != 1 {
		t.Fatalf("budget owners after Init = %d, want the session pool's only", n)
	}
	if h.oauthPool.oauth != nil {
		t.Fatal("OAuth client built before a token call")
	}
	// Shared-session validation has no side effects: it builds no pool.
	if err := h.ValidateSharedSessionAction(&protocol.Action{Type: "GET", Target: server.URL, HTTPVersion: versionOverride(t, VersionHTTP2)}); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if n := owners(); n != 1 {
		t.Fatalf("budget owners after validation = %d, want the session pool's only", n)
	}

	for _, step := range []struct {
		policy string
		owners int
	}{{"1.1", 2}, {"2", 3}, {"1.1", 3}, {"auto", 3}} {
		want := step.policy
		if want == "auto" {
			want = "2"
		}
		if result := executeVersion(t, h, server.URL, step.policy); result.HTTPVersionNegotiated != want {
			t.Fatalf("policy=%s negotiated %s", step.policy, result.HTTPVersionNegotiated)
		}
		if n := owners(); n != step.owners {
			t.Fatalf("after policy=%s budget owners = %d, want %d", step.policy, n, step.owners)
		}
	}

	client := h.forHTTPVersion(VersionHTTP11).OAuthHTTPClient()
	built := h.policyPools[VersionHTTP11].oauth
	if client == nil || built == nil {
		t.Fatal("token call did not build the pool's OAuth client")
	}
	transport := built.transport
	if n := owners(); n != 4 {
		t.Fatalf("budget owners after the token call = %d, want 4", n)
	}
	if client.Jar != nil || transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("OAuth client inherited the session jar or insecure TLS policy")
	}
	if !slices.Equal(transport.TLSClientConfig.NextProtos, []string{"http/1.1"}) {
		t.Fatalf("OAuth ALPN = %v, want http/1.1 only", transport.TLSClientConfig.NextProtos)
	}
	if transport.MaxResponseHeaderBytes != maxOAuthResponseHeaderBytes {
		t.Fatalf("OAuth response header limit = %d", transport.MaxResponseHeaderBytes)
	}
	owned := false
	for _, owner := range h.connections.snapshotOwners() {
		owned = owned || owner.transport == transport
	}
	if !owned || transport == h.policyPools[VersionHTTP11].httpTransport() {
		t.Fatal("OAuth transport is not its own connection-budget owner")
	}
	if h.policyPools["auto"].oauth != nil || h.policyPools[VersionHTTP2].oauth != nil {
		t.Fatal("a token call built other pools' OAuth clients")
	}
}

// Requests and token calls racing to a pool's first use build it once.
func TestHTTPVersionPoolsConcurrentFirstUseBuildsOnce(t *testing.T) {
	h := lazyVersionHTTP(t)
	pool := h.policyPools[VersionHTTP2]
	const callers = 8
	transports := make([]*stdhttp.Transport, callers)
	oauth := make([]*httpOAuthClient, callers)
	var wg sync.WaitGroup
	for i := range callers {
		wg.Go(func() {
			transports[i] = pool.httpTransport()
			oauth[i] = h.forHTTPVersion(VersionHTTP2).oauthPool.oauthClient()
		})
	}
	wg.Wait()
	for i := range callers {
		if transports[i] != transports[0] || oauth[i] != oauth[0] {
			t.Fatal("concurrent first use built the pool more than once")
		}
	}
	if n := len(h.connections.snapshotOwners()); n != 3 {
		t.Fatalf("budget owners = %d, want the session pool's and HTTP/2's transport and OAuth", n)
	}
}

// HTTP/3 pools take their TCP transport and OAuth client from the automatic
// pool, which a session pinned to another version has not built.
func TestHTTPVersionPoolsHTTP3BorrowsUnbuiltAutomaticPool(t *testing.T) {
	h := http3Session(t, map[string]any{"http_version": VersionHTTP11})
	auto := h.policyPools["auto"]
	for _, policy := range []string{VersionHTTP3, VersionAutoH3} {
		if h.policyPools[policy] == nil {
			t.Fatalf("no %s pool: %v", policy, h.http3Err)
		}
	}
	if n := len(h.connections.snapshotOwners()); n != 1 {
		t.Fatalf("budget owners after Init = %d, want the HTTP/1.1 session pool's only", n)
	}
	// An HTTP/3 request uses no TCP transport, so it builds none.
	child := h.forHTTPVersion(VersionHTTP3)
	if child.transport != h.transport || len(h.connections.snapshotOwners()) != 1 {
		t.Fatal("an HTTP/3 request built the automatic TCP pool")
	}
	if child.OAuthHTTPClient() == nil || auto.oauth == nil {
		t.Fatal("an HTTP/3 token call did not build the automatic pool's OAuth client")
	}
	if h.forHTTPVersion(VersionAutoH3).oauthPool.oauthClient() != auto.oauth {
		t.Fatal("auto+h3 does not share the automatic pool's OAuth client")
	}
	if n := len(h.connections.snapshotOwners()); n != 2 {
		t.Fatalf("budget owners = %d, want the session pool's and the automatic pool's OAuth client", n)
	}
	// The automatic pool still builds when it is first used.
	if auto.httpTransport() == nil || len(h.connections.snapshotOwners()) != 3 {
		t.Fatal("the automatic pool did not build on first use")
	}
}

// A request refused before it is sent builds no version pool.
func TestHTTPVersionPoolsRefusedRequestBuildsNoPool(t *testing.T) {
	h := &HTTP{}
	if err := h.Init(context.Background(), map[string]any{"tls_insecure_skip_verify": true, "default_headers": map[string]string{"Connection": "keep-alive"}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Teardown(context.Background()) })
	for name, action := range map[string]*protocol.Action{
		"HTTP/2 connection header": {Type: "GET", Target: "https://127.0.0.1:1/", HTTPVersion: versionOverride(t, VersionHTTP2)},
		"invalid base64 body":      {Type: "POST", Target: "https://127.0.0.1:1/", Body: []byte("%%%"), BodyEncoding: "base64", HTTPVersion: versionOverride(t, VersionHTTP11)},
	} {
		result, err := h.Execute(context.Background(), action)
		if err != nil || result.Success {
			t.Fatalf("%s: request was not refused: success=%v err=%v", name, result.Success, err)
		}
		if n := len(h.connections.snapshotOwners()); n != 1 {
			t.Fatalf("%s: budget owners = %d, want the session pool's only", name, n)
		}
	}
}
