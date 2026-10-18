package http

import (
	"context"
	"io"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestHTTPPolicyPoolsAreIndependentAndInitializeOnlyOnRequest(t *testing.T) {
	server := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
		w.WriteHeader(stdhttp.StatusNoContent)
	}))
	defer server.Close()
	h := versionHTTP(t, map[string]any{"max_conns_per_host": 4})
	assertOwners := func(want int) {
		t.Helper()
		if got := len(h.connections.snapshotOwners()); got != want {
			t.Fatalf("initialized transport owners = %d, want %d", got, want)
		}
	}
	assertOwners(0)
	app := h.forHTTPVersion(VersionHTTP11)
	oauth := app.OAuthHTTPClient()
	assertOwners(0)
	if oauth.Jar != nil || oauth.CheckRedirect == nil {
		t.Fatal("cold OAuth client lost its cookie or redirect policy")
	}
	if err := h.CloseIdleConnections(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertOwners(0)

	request := func(client *stdhttp.Client) {
		t.Helper()
		response, err := client.Get(server.URL)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
	}
	request(app.client)
	assertOwners(1)
	request(oauth)
	assertOwners(2)
	request(h.client)
	assertOwners(3)
	request(h.OAuthHTTPClient())
	assertOwners(4)
	if h.policyPools[VersionHTTP2].transport != nil || h.policyPools[VersionHTTP2].oauthTransport != nil {
		t.Fatal("using other policies initialized the unused HTTP/2 pools")
	}
	if err := h.Teardown(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Teardown closes idle sockets, not the reusable session's policy owners.
	request(app.client)
	assertOwners(4)
}

func TestHTTPPolicyPoolsConcurrentColdRequestsAndClose(t *testing.T) {
	server := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
		w.WriteHeader(stdhttp.StatusNoContent)
	}))
	defer server.Close()
	h := versionHTTP(t, map[string]any{"max_conns_per_host": 8})
	start := make(chan struct{})
	errors := make(chan error, 40)
	var wait sync.WaitGroup
	for worker := 0; worker < 40; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			<-start
			policy := "auto"
			if worker%2 == 0 {
				policy = VersionHTTP11
			}
			client := h.forHTTPVersion(policy).client
			if worker%4 < 2 {
				client = h.forHTTPVersion(policy).OAuthHTTPClient()
			}
			response, err := client.Get(server.URL)
			if err == nil {
				_, err = io.Copy(io.Discard, response.Body)
				_ = response.Body.Close()
			}
			errors <- err
		}(worker)
	}
	wait.Add(1)
	go func() {
		defer wait.Done()
		<-start
		for range 40 {
			_ = h.CloseIdleConnections(context.Background())
		}
	}()
	close(start)
	wait.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := len(h.connections.snapshotOwners()); got != 4 {
		t.Fatalf("concurrent first use created %d owners, want one per used policy/purpose", got)
	}
}

func TestHTTPPolicyLazyPoolsKeepInitializationErrors(t *testing.T) {
	for _, config := range []map[string]any{
		{"http_version": "invalid"},
		{"proxy_mode": "custom"},
		{"tls_ca_cert_file": "missing-certificate"},
	} {
		h := &HTTP{}
		if err := h.Init(context.Background(), config); err == nil {
			t.Fatalf("Init accepted invalid config before any request: %v", config)
		}
	}
}

// Shared-session validation and a request refused before it is sent build no
// transport.
func TestHTTPPolicyPoolsRefusedRequestsBuildNoTransport(t *testing.T) {
	h := versionHTTP(t, map[string]any{"default_headers": map[string]string{"Connection": "keep-alive"}})
	owners := func() int { return len(h.connections.snapshotOwners()) }
	if err := h.ValidateSharedSessionAction(&protocol.Action{Type: "GET", Target: "https://127.0.0.1:1/", HTTPVersion: versionOverride(t, VersionHTTP11)}); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if n := owners(); n != 0 {
		t.Fatalf("validation built %d transports", n)
	}
	for _, refused := range []struct {
		name, message string
		action        *protocol.Action
	}{
		{"HTTP/2 connection header", "connection-specific headers", &protocol.Action{Type: "GET", Target: "https://127.0.0.1:1/", HTTPVersion: versionOverride(t, VersionHTTP2)}},
		{"invalid base64 body", "invalid base64 http body", &protocol.Action{Type: "POST", Target: "https://127.0.0.1:1/", Body: []byte("%%%"), BodyEncoding: "base64", HTTPVersion: versionOverride(t, VersionHTTP11)}},
	} {
		name := refused.name
		result, err := h.Execute(context.Background(), refused.action)
		if err != nil || result == nil || result.Success || !strings.Contains(strings.ToLower(result.ErrorMessage), refused.message) {
			t.Fatalf("%s: request was not refused before sending: result=%+v err=%v", name, result, err)
		}
		if n := owners(); n != 0 {
			t.Fatalf("%s: refused request built %d transports", name, n)
		}
	}
}
