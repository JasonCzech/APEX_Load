package http

import (
	"context"
	"fmt"
	stdhttp "net/http"
	"slices"
	"sync"
	"testing"
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
	if h.oauthPool.builtOAuthTransport() != nil {
		t.Fatal("OAuth client built before a token call")
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
	transport := h.policyPools[VersionHTTP11].builtOAuthTransport()
	if client == nil || transport == nil {
		t.Fatal("token call did not build the pool's OAuth client")
	}
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
	if h.policyPools["auto"].builtOAuthTransport() != nil || h.policyPools[VersionHTTP2].builtOAuthTransport() != nil {
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
