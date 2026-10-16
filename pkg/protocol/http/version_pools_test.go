package http

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func versionOverride(t *testing.T, policy string) protocol.OptionalHTTPVersion {
	t.Helper()
	value, err := protocol.NewHTTPVersionOverride(policy)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func versionTLS(t *testing.T, handler stdhttp.Handler) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)
	return server
}
func versionHTTP(t *testing.T, config map[string]any) *HTTP {
	t.Helper()
	if config == nil {
		config = map[string]any{}
	}
	config["tls_insecure_skip_verify"] = true
	h := &HTTP{}
	if err := h.Init(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Teardown(context.Background()) })
	return h
}
func executeVersion(t *testing.T, h *HTTP, target, policy string) *protocol.Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := h.Execute(ctx, &protocol.Action{Name: "version", Type: "GET", Target: target, HTTPVersion: versionOverride(t, policy)})
	if err != nil || !result.Success {
		t.Fatalf("policy=%s success=%v message=%s err=%v", policy, result.Success, result.ErrorMessage, err)
	}
	return result
}

func TestHTTPVersionPoolsShareCookiesPartitionCacheAndIsolateVUsers(t *testing.T) {
	var calls atomic.Int64
	server := versionTLS(t, stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		calls.Add(1)
		if r.URL.Path == "/seed" {
			stdhttp.SetCookie(w, &stdhttp.Cookie{Name: "session", Value: "synthetic", Path: "/"})
		}
		if r.URL.Path == "/cache" {
			w.Header().Set("Cache-Control", "max-age=3600")
		}
		cookie, _ := r.Cookie("session")
		value := "none"
		if cookie != nil {
			value = cookie.Value
		}
		fmt.Fprintf(w, "%d:%s", r.ProtoMajor, value)
	}))
	h := versionHTTP(t, map[string]any{"http_version": "2", "http_cache_enabled": true, "max_conns_per_host": 1})
	executeVersion(t, h, server.URL+"/seed", "1.1")
	for _, policy := range []string{"2", "1.1", "auto", "2"} {
		r := executeVersion(t, h, server.URL+"/echo", policy)
		if !strings.Contains(r.ResponseBody, ":synthetic") {
			t.Fatalf("cookie lost: %s", r.ResponseBody)
		}
		want := policy
		if want == "auto" {
			want = "2"
		}
		if r.ProtocolEvidence["http_version_negotiated"] != want || r.ProtocolEvidence["http_version_policy"] != policy {
			t.Fatalf("evidence=%v", r.ProtocolEvidence)
		}
	}
	for _, policy := range []string{"1.1", "2", "auto"} {
		first := executeVersion(t, h, server.URL+"/cache", policy)
		second := executeVersion(t, h, server.URL+"/cache", policy)
		if first.HTTPVersionSource != "wire" || second.HTTPVersionSource != "cache" || second.HTTPVersionNegotiated != "" {
			t.Fatalf("cache receipts first=%v second=%v", first.ProtocolEvidence, second.ProtocolEvidence)
		}
	}
	other := versionHTTP(t, nil)
	r := executeVersion(t, other, server.URL+"/echo", "1.1")
	if !strings.Contains(r.ResponseBody, ":none") {
		t.Fatalf("VUser cookie leaked: %s", r.ResponseBody)
	}
	if err := h.ClearCookies(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, policy := range []string{"1.1", "2"} {
		if r := executeVersion(t, h, server.URL+"/echo", policy); !strings.Contains(r.ResponseBody, ":none") {
			t.Fatalf("clear lost: %s", r.ResponseBody)
		}
	}
}

type versionCountedConn struct {
	net.Conn
	open *atomic.Int64
	once sync.Once
}

func (c *versionCountedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { c.open.Add(-1) })
	return err
}
func instrumentVersionDials(h *HTTP) (*atomic.Int64, *atomic.Int64) {
	open, peak := new(atomic.Int64), new(atomic.Int64)
	for _, owner := range h.connections.snapshotOwners() {
		base := owner.baseDial
		owner.baseDial = func(ctx context.Context, network, address string) (net.Conn, error) {
			conn, err := base(ctx, network, address)
			if err != nil {
				return nil, err
			}
			n := open.Add(1)
			for old := peak.Load(); n > old; old = peak.Load() {
				if peak.CompareAndSwap(old, n) {
					break
				}
			}
			return &versionCountedConn{Conn: conn, open: open}, nil
		}
	}
	return open, peak
}
func TestHTTPVersionPoolsCapOneCancellationAndSwitch(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	finish := func() { releaseOnce.Do(func() { close(release) }) }
	defer finish()
	server := versionTLS(t, stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		if r.URL.Path == "/hold" {
			close(entered)
			<-release
		}
		fmt.Fprint(w, r.ProtoMajor)
	}))
	h := versionHTTP(t, map[string]any{"max_conns_per_host": 1})
	open, peak := instrumentVersionDials(h)
	first := make(chan *protocol.Result, 1)
	go func() {
		r, _ := h.Execute(context.Background(), &protocol.Action{Type: "GET", Target: server.URL + "/hold", HTTPVersion: versionOverride(t, "1.1")})
		first <- r
	}()
	select {
	case <-entered:
	case result := <-first:
		t.Fatalf("held request failed before reaching server: %s", result.ErrorMessage)
	case <-time.After(3 * time.Second):
		t.Fatal("held request did not reach server")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	r, err := h.Execute(ctx, &protocol.Action{Type: "GET", Target: server.URL + "/next", HTTPVersion: versionOverride(t, "2")})
	cancel()
	if err != nil || r.Success {
		t.Fatalf("blocked call result=%v err=%v", r.Success, err)
	}
	finish()
	if r := <-first; !r.Success {
		t.Fatalf("held request failed: %s", r.ErrorMessage)
	}
	for _, policy := range []string{"2", "1.1", "auto", "1.1"} {
		executeVersion(t, h, server.URL+"/next", policy)
	}
	if peak.Load() > 1 {
		t.Fatalf("physical ceiling multiplied: peak=%d", peak.Load())
	}
	_ = h.CloseIdleConnections(context.Background())
	deadline := time.Now().Add(time.Second)
	for open.Load() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if open.Load() != 0 {
		t.Fatalf("socket lease leaked: %d", open.Load())
	}
}

func TestHTTPVersionPoolsConcurrentMixedPolicies(t *testing.T) {
	entered, release := make(chan int, 2), make(chan struct{})
	server := versionTLS(t, stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		entered <- r.ProtoMajor
		<-release
		fmt.Fprint(w, r.ProtoMajor)
	}))
	h := versionHTTP(t, map[string]any{"max_conns_per_host": 2})
	_, peak := instrumentVersionDials(h)
	results := make(chan *protocol.Result, 2)
	for _, policy := range []string{"1.1", "2"} {
		override := versionOverride(t, policy)
		go func() {
			r, _ := h.Execute(context.Background(), &protocol.Action{Type: "GET", Target: server.URL, HTTPVersion: override})
			results <- r
		}()
	}
	majors := map[int]bool{}
	for range 2 {
		select {
		case major := <-entered:
			majors[major] = true
		case <-time.After(3 * time.Second):
			close(release)
			t.Fatal("mixed requests did not reach server")
		}
	}
	close(release)
	for range 2 {
		if r := <-results; !r.Success {
			t.Fatalf("concurrent failure: %s", r.ErrorMessage)
		}
	}
	if !majors[1] || !majors[2] || peak.Load() > 2 {
		t.Fatalf("majors=%v peak=%d", majors, peak.Load())
	}
}

func TestHTTPVersionLeafHeadersAndTLSBounds(t *testing.T) {
	var hits atomic.Int64
	server := versionTLS(t, stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) { hits.Add(1); fmt.Fprint(w, r.ProtoMajor) }))
	h := versionHTTP(t, map[string]any{"http_version": "2", "default_headers": map[string]string{"Connection": "keep-alive"}})
	executeVersion(t, h, server.URL, "1.1")
	for _, policy := range []string{"2", "auto"} {
		r, err := h.Execute(context.Background(), &protocol.Action{Type: "GET", Target: server.URL, HTTPVersion: versionOverride(t, policy)})
		if err != nil || r.Success {
			t.Fatalf("invalid headers accepted policy=%s", policy)
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("invalid leaf sent traffic: %d", hits.Load())
	}
	old := httptest.NewUnstartedServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) { fmt.Fprint(w, r.ProtoMajor) }))
	old.TLS = &tls.Config{MinVersion: tls.VersionTLS11, MaxVersion: tls.VersionTLS11}
	old.StartTLS()
	defer old.Close()
	legacy := versionHTTP(t, map[string]any{"http_version": "2", "tls_min_version": "1.1", "tls_max_version": "1.1"})
	executeVersion(t, legacy, old.URL, "1.1")
	if err := legacy.ValidateSharedSessionAction(&protocol.Action{Type: "GET", Target: old.URL, HTTPVersion: versionOverride(t, "2")}); err == nil {
		t.Fatal("impossible H2 TLS policy accepted")
	}
}

func TestHTTPVersionLeafRedirectDigestResourcesAndMetaRefresh(t *testing.T) {
	var mu sync.Mutex
	var majors []int
	server := versionTLS(t, stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		mu.Lock()
		majors = append(majors, r.ProtoMajor)
		mu.Unlock()
		switch r.URL.Path {
		case "/redirect":
			stdhttp.Redirect(w, r, "/digest", 302)
		case "/digest":
			if !strings.HasPrefix(r.Header.Get("Authorization"), "Digest ") {
				w.Header().Set("WWW-Authenticate", `Digest realm="apex", nonce="synthetic", qop="auth"`)
				w.WriteHeader(401)
				return
			}
			fmt.Fprint(w, "digest")
		case "/refresh":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<meta http-equiv="refresh" content="0; url=final">`)
		default:
			fmt.Fprint(w, "final")
		}
	}))
	h := versionHTTP(t, map[string]any{"http_version": "2", "max_meta_refresh": 2, "auth_type": "digest", "auth_username": "u", "auth_password": "p"})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for _, path := range []string{"/redirect", "/refresh"} {
		r, err := h.Execute(ctx, &protocol.Action{Type: "GET", Target: server.URL + path, HTTPVersion: versionOverride(t, "1.1"), Params: map[string]any{"resources": []any{map[string]any{"url": "/resource"}}}})
		if err != nil || !r.Success {
			t.Fatalf("path=%s err=%v message=%s", path, err, r.ErrorMessage)
		}
		if r.ProtocolEvidence["http_version_negotiated"] != "1.1" {
			t.Fatalf("receipt lost: %v", r.ProtocolEvidence)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(majors) < 6 {
		t.Fatalf("flow legs missing: %v", majors)
	}
	for _, major := range majors {
		if major != 1 {
			t.Fatalf("leaf policy lost on flow leg: %v", majors)
		}
	}
}

func versionConnectProxy(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		if r.Method != "CONNECT" {
			stdhttp.Error(w, "CONNECT required", 400)
			return
		}
		upstream, err := net.Dial("tcp", r.Host)
		if err != nil {
			stdhttp.Error(w, "dial", 502)
			return
		}
		client, buffer, err := w.(stdhttp.Hijacker).Hijack()
		if err != nil {
			upstream.Close()
			return
		}
		buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		buffer.Flush()
		go func() { defer client.Close(); defer upstream.Close(); io.Copy(upstream, client) }()
		go func() { defer client.Close(); defer upstream.Close(); io.Copy(client, upstream) }()
	}))
	t.Cleanup(server.Close)
	return server
}
func TestHTTPVersionPoolsProxyCapOneAcrossTargetsAndPolicies(t *testing.T) {
	handler := stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) { fmt.Fprint(w, r.ProtoMajor) })
	one, two := versionTLS(t, handler), versionTLS(t, handler)
	proxy := versionConnectProxy(t)
	h := versionHTTP(t, map[string]any{"proxy_mode": "custom", "proxy_url": proxy.URL, "max_conns_per_host": 1})
	_, peak := instrumentVersionDials(h)
	for _, target := range []string{one.URL, two.URL, one.URL} {
		for _, policy := range []string{"1.1", "2"} {
			executeVersion(t, h, target, policy)
		}
	}
	if peak.Load() > 1 {
		t.Fatalf("proxy physical ceiling multiplied: %d", peak.Load())
	}
}

func TestHTTPVersionPoolsPACClonesKeepPolicyAndBoundOwnership(t *testing.T) {
	server := versionTLS(t, stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) { fmt.Fprint(w, r.ProtoMajor) }))
	pac := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		fmt.Fprint(w, `function FindProxyForURL(url, host) { return "DIRECT"; }`)
	}))
	defer pac.Close()
	h := versionHTTP(t, map[string]any{"proxy_mode": "pac", "proxy_pac_url": pac.URL, "max_conns_per_host": 1, protocol.TargetPolicyConfigKey: []string{"127.0.0.1"}})
	for range 4 {
		for _, policy := range []string{"1.1", "2", "auto"} {
			result := executeVersion(t, h, server.URL, policy)
			want := policy
			if want == "auto" {
				want = "2"
			}
			if result.HTTPVersionNegotiated != want {
				t.Fatalf("PAC version=%s receipt=%s", policy, result.HTTPVersionNegotiated)
			}
		}
	}
	_ = h.CloseIdleConnections(context.Background())
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(h.connections.snapshotOwners()) == 6 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if n := len(h.connections.snapshotOwners()); n != 6 {
		t.Fatalf("disposable PAC owners retained: %d", n)
	}
}

func TestHTTPVersionPoolsAliasesConfigAndLeafPrecedence(t *testing.T) {
	server := versionTLS(t, stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) { fmt.Fprint(w, r.ProtoMajor) }))
	for _, tc := range []struct{ alias, config, want string }{{"http/2", "", "2"}, {"http/1.1", "", "1.1"}, {"http/2", "1.1", "1.1"}, {"http/1.1", "2", "2"}, {"http/2", " ", "2"}} {
		instance, err := protocol.Get(tc.alias)
		if err != nil {
			t.Fatal(err)
		}
		h := instance.(*HTTP)
		config := map[string]any{"tls_insecure_skip_verify": true}
		if tc.config != "" {
			config["http_version"] = tc.config
		}
		if err := h.Init(context.Background(), config); err != nil {
			t.Fatal(err)
		}
		result, err := h.Execute(context.Background(), &protocol.Action{Type: "GET", Target: server.URL})
		if err != nil || !result.Success || result.HTTPVersionNegotiated != tc.want {
			t.Fatalf("alias=%s config=%q result=%v err=%v", tc.alias, tc.config, result.ProtocolEvidence, err)
		}
		executeVersion(t, h, server.URL, "1.1")
		executeVersion(t, h, server.URL, "2")
		if tc.config == " " {
			if result.HTTPVersionPolicy != "auto" {
				t.Fatalf("whitespace config did not override alias: %v", result.ProtocolEvidence)
			}
			h1 := httptest.NewTLSServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) { fmt.Fprint(w, r.ProtoMajor) }))
			r, err := h.Execute(context.Background(), &protocol.Action{Type: "GET", Target: h1.URL})
			h1.Close()
			if err != nil || !r.Success || r.HTTPVersionPolicy != "auto" || r.HTTPVersionNegotiated != "1.1" {
				t.Fatalf("whitespace alias Auto fallback failed: result=%v err=%v", r.ProtocolEvidence, err)
			}
		}
		_ = h.Teardown(context.Background())
	}
}

func TestHTTPVersionConnectionBudgetRejectsEmptyDialAndReleasesFailure(t *testing.T) {
	budget := newHTTPConnectionBudget(1, 1)
	for _, withConn := range []bool{false, true} {
		tr := &stdhttp.Transport{}
		owner := budget.track(tr, func(context.Context, string, string) (net.Conn, error) {
			if !withConn {
				return nil, nil
			}
			one, two := net.Pipe()
			two.Close()
			return one, fmt.Errorf("synthetic dial failure")
		})
		if _, err := owner.dialContext(context.Background(), "tcp", "example.test:443"); err == nil {
			t.Fatal("invalid dial accepted")
		}
		owner.retire()
		budget.mu.Lock()
		n := len(budget.counts)
		owners := len(budget.owners)
		budget.mu.Unlock()
		if n != 0 || owners != 0 {
			t.Fatalf("failed dial lease retained: counts=%d owners=%d", n, owners)
		}
	}
}
