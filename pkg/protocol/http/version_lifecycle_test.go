package http

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	stdhttp "net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func waitVersionSockets(t *testing.T, open *atomic.Int64, maximum int64) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for open.Load() > maximum && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if n := open.Load(); n > maximum {
		t.Fatalf("physical sockets=%d, maximum=%d", n, maximum)
	}
}

func TestHTTPVersionPoolsOAuthSamePolicyIdleSwitch(t *testing.T) {
	server := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		if r.URL.Path == "/seed" {
			stdhttp.SetCookie(w, &stdhttp.Cookie{Name: "session", Value: "synthetic", Path: "/"})
		}
		if r.URL.Path == "/token" && r.Header.Get("Cookie") != "" {
			w.WriteHeader(400)
			return
		}
		fmt.Fprint(w, r.ProtoMajor)
	}))
	defer server.Close()
	h := versionHTTP(t, map[string]any{"http_version": "1.1", "max_conns_per_host": 1})
	open, peak := instrumentVersionDials(h)
	executeVersion(t, h, server.URL+"/seed", "1.1")
	for range 3 {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		request, _ := stdhttp.NewRequestWithContext(ctx, "GET", server.URL+"/token", nil)
		response, err := h.OAuthHTTPClient().Do(request)
		if err != nil {
			cancel()
			t.Fatalf("same-policy OAuth pool stalled behind load idle socket: %v", err)
		}
		body, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		cancel()
		if readErr != nil || response.StatusCode != 200 || string(body) != "1" {
			t.Fatalf("OAuth response=%d %q read=%v", response.StatusCode, body, readErr)
		}
		executeVersion(t, h, server.URL, "1.1")
	}
	if peak.Load() != 1 {
		t.Fatalf("same-policy physical ceiling=%d", peak.Load())
	}
	_ = h.Teardown(context.Background())
	waitVersionSockets(t, open, 0)
}

func TestHTTPVersionPoolsIdleLimitSharedAcrossVersionsAndHosts(t *testing.T) {
	handler := stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) { fmt.Fprint(w, r.ProtoMajor) })
	one, two := versionTLS(t, handler), versionTLS(t, handler)
	h := versionHTTP(t, map[string]any{"max_idle_conns": 1, "max_conns_per_host": 3})
	open, _ := instrumentVersionDials(h)
	for _, target := range []string{one.URL, two.URL, one.URL} {
		for _, policy := range []string{"1.1", "2", "auto"} {
			executeVersion(t, h, target, policy)
			waitVersionSockets(t, open, 1)
		}
	}
	_ = h.Teardown(context.Background())
	waitVersionSockets(t, open, 0)
}

func TestHTTPVersionPoolsConfiguredResourcesUseLeafPolicyAndCache(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	server := versionTLS(t, stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		mu.Lock()
		seen = append(seen, fmt.Sprintf("%s:%d", r.URL.Path, r.ProtoMajor))
		mu.Unlock()
		if r.URL.Path == "/page" {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<html><img src="/automatic"><script src="/explicit"></script></html>`)
			return
		}
		w.Header().Set("Cache-Control", "max-age=3600")
		fmt.Fprint(w, "asset")
	}))
	for _, tc := range []struct{ inherited, leaf, major string }{{"2", "1.1", "1"}, {"1.1", "2", "2"}} {
		t.Run(tc.inherited+"_to_"+tc.leaf, func(t *testing.T) {
			mu.Lock()
			seen = nil
			mu.Unlock()
			h := versionHTTP(t, map[string]any{"http_version": tc.inherited, "http_download_non_html_resources": true, "http_cache_enabled": true, "max_conns_per_host": 1})
			for range 2 {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				result, err := h.Execute(ctx, &protocol.Action{Type: "GET", Target: server.URL + "/page", HTTPVersion: versionOverride(t, tc.leaf), Params: map[string]any{"resources": []any{map[string]any{"url": "/manual"}}}})
				cancel()
				if err != nil || !result.Success || result.HTTPVersionNegotiated != tc.leaf {
					t.Fatalf("page policy lost: result=%v err=%v", result.ProtocolEvidence, err)
				}
				if result.ProtocolEvidence["resource_requests"] == nil || result.ProtocolEvidence["transcript"] != nil {
					t.Fatalf("native resource evidence lost or raw HTTP payload expanded: %v", result.ProtocolEvidence)
				}
			}
			mu.Lock()
			observations := append([]string(nil), seen...)
			mu.Unlock()
			counts := make(map[string]int)
			for _, observation := range observations {
				counts[observation]++
			}
			for _, path := range []string{"/automatic", "/explicit", "/manual"} {
				if counts[path+":"+tc.major] != 1 {
					t.Fatalf("resource wire/cache policy lost: %v", observations)
				}
			}
			if len(observations) != 5 || counts["/page:"+tc.major] != 2 {
				t.Fatalf("unexpected resource traffic: %v", observations)
			}
		})
	}
}

func TestHTTPVersionConnectionBudgetRetirementClosesInflightDial(t *testing.T) {
	budget := newHTTPConnectionBudget(1, 1)
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	finish := func() { releaseOnce.Do(func() { close(release) }) }
	defer finish()
	one, two := net.Pipe()
	defer two.Close()
	owner := budget.track(&stdhttp.Transport{}, func(context.Context, string, string) (net.Conn, error) {
		close(entered)
		<-release
		return one, nil
	})
	result := make(chan error, 1)
	go func() { _, err := owner.dialContext(context.Background(), "tcp", "synthetic:443"); result <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("retired dial fixture never started")
	}
	owner.retire()
	finish()
	if err := <-result; !errors.Is(err, net.ErrClosed) {
		t.Fatalf("retired inflight dial error=%v", err)
	}
	if _, err := owner.dialContext(context.Background(), "tcp", "synthetic:443"); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("retired owner admitted a new dial: %v", err)
	}
	budget.mu.Lock()
	counts, owners := len(budget.counts), len(budget.owners)
	budget.mu.Unlock()
	if counts != 0 || owners != 0 || owner.open.Load() != 0 {
		t.Fatalf("retired owner retained lease: counts=%d owners=%d open=%d", counts, owners, owner.open.Load())
	}
	if _, err := two.Write([]byte("closed")); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("retired socket still open: %v", err)
	}
}

func TestHTTPVersionPoolsPACConcurrentClonesSharePhysicalCeiling(t *testing.T) {
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
	open, peak := new(atomic.Int64), new(atomic.Int64)
	// Instrument the PAC first-hop dial before its derived transport is leased;
	// the fixed version pools are never used by these clients.
	baseDial := h.connections.snapshotOwners()[0].baseDial
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := baseDial(ctx, network, address)
		if err != nil {
			return conn, err
		}
		n := open.Add(1)
		for old := peak.Load(); n > old; old = peak.Load() {
			if peak.CompareAndSwap(old, n) {
				break
			}
		}
		return &versionCountedConn{Conn: conn, open: open}, nil
	}
	clients := make(map[string]*stdhttp.Client)
	for policy, pool := range h.policyPools {
		clients[policy] = &stdhttp.Client{Transport: &pacRoundTripper{source: `function FindProxyForURL(url, host) { return "DIRECT"; }`, allowed: []string{"127.0.0.1"}, base: pool.httpTransport(), budget: h.connections, baseDial: dial}, Timeout: 3 * time.Second}
	}
	first := make(chan error, 1)
	go func() {
		response, err := clients["1.1"].Get(server.URL + "/hold")
		if err == nil {
			_, err = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
		}
		first <- err
	}()
	select {
	case <-entered:
	case err := <-first:
		t.Fatalf("PAC held request failed before server: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("PAC held request never reached server")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	request, _ := stdhttp.NewRequestWithContext(ctx, "GET", server.URL, nil)
	_, err := clients["2"].Do(request)
	cancel()
	if err == nil {
		t.Fatal("PAC blocked request ignored cancellation")
	}
	finish()
	if err := <-first; err != nil {
		t.Fatalf("PAC held request failed: %v", err)
	}
	for _, policy := range []string{"2", "1.1", "auto", "2"} {
		response, err := clients[policy].Get(server.URL)
		if err != nil {
			t.Fatalf("PAC clone policy=%s stalled: %v", policy, err)
		}
		body, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		want := "2"
		if policy == "1.1" {
			want = "1"
		}
		if readErr != nil || string(body) != want {
			t.Fatalf("PAC policy=%s observed=%q err=%v", policy, body, readErr)
		}
	}
	if peak.Load() != 1 {
		t.Fatalf("PAC physical ceiling=%d", peak.Load())
	}
	waitVersionSockets(t, open, 0)
	deadline := time.Now().Add(time.Second)
	for len(h.connections.snapshotOwners()) != 6 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if owners := len(h.connections.snapshotOwners()); owners != 6 {
		t.Fatalf("PAC canceled/finished owners retained: %d", owners)
	}
}

func TestHTTPVersionPoolsProxyNativeQueueCanEvictOwnIdleDestination(t *testing.T) {
	for _, policy := range []string{"1.1", "2", "auto"} {
		t.Run(policy, func(t *testing.T) {
			handler := stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) { fmt.Fprint(w, r.ProtoMajor) })
			one, two := versionTLS(t, handler), versionTLS(t, handler)
			proxy := versionConnectProxy(t)
			h := versionHTTP(t, map[string]any{"proxy_mode": "custom", "proxy_url": proxy.URL, "max_conns_per_host": 1})
			pool := h.policyPools[policy]
			originalProxy := pool.httpTransport().Proxy
			entered, release := make(chan struct{}, 2), make(chan struct{})
			var releaseOnce sync.Once
			finish := func() { releaseOnce.Do(func() { close(release) }) }
			defer finish()
			// Hold both callers before Go's per-destination queue starts. Both
			// operations are live, but only one gets a DialContext callback.
			pool.httpTransport().Proxy = func(request *stdhttp.Request) (*url.URL, error) {
				if request.URL.String() == two.URL+"/" {
					select {
					case entered <- struct{}{}:
					default:
					}
					<-release
				}
				return originalProxy(request)
			}
			executeVersion(t, h, one.URL, policy)
			results := make(chan *protocol.Result, 2)
			for range 2 {
				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), time.Second)
					defer cancel()
					result, _ := h.Execute(ctx, &protocol.Action{Type: "GET", Target: two.URL + "/", HTTPVersion: versionOverride(t, policy)})
					results <- result
				}()
			}
			for range 2 {
				select {
				case <-entered:
				case <-time.After(time.Second):
					finish()
					t.Fatal("concurrent proxy calls did not reach the native queue")
				}
			}
			finish()
			for range 2 {
				if result := <-results; !result.Success {
					t.Fatalf("same-pool native queue stalled behind an idle proxy socket: %s", result.ErrorMessage)
				}
			}
		})
	}
}

func TestHTTPVersionPoolsProxyCanEvictIdleBesideActiveDestination(t *testing.T) {
	for _, policy := range []string{"1.1", "2", "auto"} {
		t.Run(policy, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			finish := func() { releaseOnce.Do(func() { close(release) }) }
			defer finish()
			handler := stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) { fmt.Fprint(w, r.ProtoMajor) })
			idle, next := versionTLS(t, handler), versionTLS(t, handler)
			held := versionTLS(t, stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
				close(entered)
				<-release
				fmt.Fprint(w, r.ProtoMajor)
			}))
			proxy := versionConnectProxy(t)
			h := versionHTTP(t, map[string]any{"proxy_mode": "custom", "proxy_url": proxy.URL, "max_conns_per_host": 2})
			open, peak := instrumentVersionDials(h)
			executeVersion(t, h, idle.URL, policy)
			first := make(chan *protocol.Result, 1)
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				result, _ := h.Execute(ctx, &protocol.Action{Type: "GET", Target: held.URL, HTTPVersion: versionOverride(t, policy)})
				first <- result
			}()
			select {
			case <-entered:
			case result := <-first:
				t.Fatalf("held request failed before server: %s", result.ErrorMessage)
			case <-time.After(3 * time.Second):
				t.Fatal("held request never reached server")
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			result, err := h.Execute(ctx, &protocol.Action{Type: "GET", Target: next.URL, HTTPVersion: versionOverride(t, policy)})
			cancel()
			if err != nil || !result.Success {
				finish()
				t.Fatalf("idle destination was protected by a different active socket: %s err=%v", result.ErrorMessage, err)
			}
			select {
			case premature := <-first:
				finish()
				t.Fatalf("active socket was retired to satisfy another destination: %s", premature.ErrorMessage)
			default:
			}
			if peak.Load() != 2 || open.Load() > 2 {
				finish()
				t.Fatalf("physical proxy ceiling lost: peak=%d open=%d", peak.Load(), open.Load())
			}
			finish()
			if result := <-first; !result.Success {
				t.Fatalf("held socket did not survive idle eviction: %s", result.ErrorMessage)
			}
			_ = h.Teardown(context.Background())
			waitVersionSockets(t, open, 0)
		})
	}
}

func TestHTTPVersionConnectionActivityDetachedDialDoesNotReactivate(t *testing.T) {
	budget := newHTTPConnectionBudget(2, 2)
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	finish := func() { releaseOnce.Do(func() { close(release) }) }
	defer finish()
	one, two := net.Pipe()
	defer two.Close()
	owner := budget.track(&stdhttp.Transport{}, func(context.Context, string, string) (net.Conn, error) {
		close(entered)
		<-release
		return one, nil
	})
	activity := &httpConnectionActivity{owner: owner}
	ctx := context.WithValue(context.Background(), httpConnectionActivityKey{}, activity)
	type dialResult struct {
		conn net.Conn
		err  error
	}
	result := make(chan dialResult, 1)
	go func() { conn, err := owner.dialContext(ctx, "tcp", "synthetic:443"); result <- dialResult{conn, err} }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("speculative dial fixture never started")
	}
	if pending := owner.pendingFresh.Load(); pending != 1 {
		t.Fatalf("fresh dial was not protected before assignment: %d", pending)
	}
	// Native transport selected another connection while this speculative dial
	// was in flight. Retry/failure hooks arriving later cannot revive a terminal
	// request, and the physical lease must still survive until socket close.
	activity.gotConn()
	activity.finish()
	activity.getConn()
	activity.gotConn()
	if guard := activity.beginDial(); guard != nil {
		t.Fatal("a detached callback revived the finished activity")
	}
	finish()
	dial := <-result
	if dial.err != nil {
		t.Fatal(dial.err)
	}
	if owner.pendingFresh.Load() != 0 || owner.open.Load() != 1 {
		t.Fatalf("late successful dial changed activity/physical accounting: pending=%d open=%d", owner.pendingFresh.Load(), owner.open.Load())
	}
	_ = dial.conn.Close()
	_ = dial.conn.Close()
	if owner.pendingFresh.Load() != 0 || owner.open.Load() != 0 {
		t.Fatalf("late socket close leaked or double-released accounting: pending=%d open=%d", owner.pendingFresh.Load(), owner.open.Load())
	}
}

func TestHTTPVersionConnectionActivityMultipleDialsAndFailure(t *testing.T) {
	budget := newHTTPConnectionBudget(2, 2)
	owner := budget.track(&stdhttp.Transport{}, func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("synthetic dial failure")
	})
	activity := &httpConnectionActivity{owner: owner}
	ctx := context.WithValue(context.Background(), httpConnectionActivityKey{}, activity)
	first := activity.beginDial()
	second := activity.beginDial()
	first.clear()
	first.clear()
	if owner.pendingFresh.Load() != 1 {
		t.Fatal("one failed speculative dial cleared a sibling's fresh guard")
	}
	activity.gotConn()
	second.clear()
	if owner.pendingFresh.Load() != 0 {
		t.Fatal("assignment did not clear all fresh guards exactly once")
	}
	activity.getConn()
	for range 1000 {
		if _, err := owner.dialContext(ctx, "tcp", "synthetic:443"); err == nil {
			t.Fatal("failed native retry unexpectedly succeeded")
		}
		owner.mu.Lock()
		retained := len(activity.pending)
		owner.mu.Unlock()
		if retained > 1 {
			t.Fatalf("failed speculative retries retained inactive guards: %d", retained)
		}
	}
	if owner.pendingFresh.Load() != 0 || owner.open.Load() != 0 {
		t.Fatal("dial failure leaked its guard or physical lease")
	}
	activity.finish()
}

func TestHTTPVersionConnectionOwnerComposesCallerTrace(t *testing.T) {
	server := versionTLS(t, stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) { fmt.Fprint(w, r.ProtoMajor) }))
	h := versionHTTP(t, map[string]any{"max_conns_per_host": 1})
	for _, policy := range []string{"1.1", "2", "auto"} {
		var get, got, connect, handshake atomic.Int64
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
			GetConn:           func(string) { get.Add(1) },
			GotConn:           func(httptrace.GotConnInfo) { got.Add(1) },
			ConnectStart:      func(string, string) { connect.Add(1) },
			TLSHandshakeStart: func() { handshake.Add(1) },
		})
		request, _ := stdhttp.NewRequestWithContext(ctx, "GET", server.URL, nil)
		response, err := h.policyPools[policy].httpClient().Do(request)
		if err == nil {
			_, err = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
		}
		cancel()
		if err != nil {
			t.Fatalf("policy=%s caller trace request failed: %v", policy, err)
		}
		if get.Load() == 0 || got.Load() == 0 || connect.Load() == 0 || handshake.Load() == 0 {
			t.Fatalf("policy=%s caller trace hooks lost: get=%d got=%d connect=%d TLS=%d", policy, get.Load(), got.Load(), connect.Load(), handshake.Load())
		}
	}
}

func TestHTTPVersionPoolsDetachedH2HandoffRetriesPOSTBeforeBodyConsumption(t *testing.T) {
	var allowH2 atomic.Bool
	var posts atomic.Int64
	holdOne, holdTwo := make(chan struct{}), make(chan struct{})
	releaseOne, releaseTwo, releaseDial := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var onceOne, onceTwo, onceDial sync.Once
	finishOne := func() { onceOne.Do(func() { close(releaseOne) }) }
	finishTwo := func() { onceTwo.Do(func() { close(releaseTwo) }) }
	finishDial := func() { onceDial.Do(func() { close(releaseDial) }) }
	defer finishOne()
	defer finishTwo()
	defer finishDial()
	server := httptest.NewUnstartedServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		switch r.URL.Path {
		case "/hold-one":
			close(holdOne)
			<-releaseOne
		case "/hold-two":
			close(holdTwo)
			<-releaseTwo
		default:
			body, err := io.ReadAll(r.Body)
			if r.Method != "POST" || err != nil || string(body) != "one-write" {
				w.WriteHeader(400)
				return
			}
			posts.Add(1)
		}
		fmt.Fprint(w, r.ProtoMajor)
	}))
	server.EnableHTTP2 = true
	server.TLS = &tls.Config{GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
		config := server.TLS.Clone()
		config.GetConfigForClient = nil
		if !allowH2.Load() {
			config.NextProtos = []string{"http/1.1"}
		}
		return config, nil
	}}
	server.StartTLS()
	t.Cleanup(server.Close)
	h := versionHTTP(t, map[string]any{"max_conns_per_host": 2})
	open, peak := instrumentVersionDials(h)
	pool := h.policyPools["auto"]
	var owner *httpConnectionOwner
	for _, candidate := range h.connections.snapshotOwners() {
		if candidate.transport == pool.httpTransport() {
			owner = candidate
			break
		}
	}
	if owner == nil {
		t.Fatal("automatic pool has no physical-budget owner")
	}
	originalDial := owner.baseDial
	var dials atomic.Int64
	dialEntered := make(chan struct{})
	owner.baseDial = func(ctx context.Context, network, address string) (net.Conn, error) {
		if dials.Add(1) == 2 {
			close(dialEntered)
			select {
			case <-releaseDial:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return originalDial(ctx, network, address)
	}
	originalUpgrade := pool.httpTransport().TLSNextProto["h2"]
	var upgrades, handoffs atomic.Int64
	pool.httpTransport().TLSNextProto["h2"] = func(authority string, conn *tls.Conn) stdhttp.RoundTripper {
		roundTripper := originalUpgrade(authority, conn)
		if upgrades.Add(1) == 1 {
			// This TCP dial's initiating POST already reused the H1 socket and
			// completed. Retire the detached H2 socket after native pool insertion
			// and before any receiving request reserves its first stream.
			h.connections.evictIdle()
			return roundTripFunc(func(request *stdhttp.Request) (*stdhttp.Response, error) {
				if request.URL.Path == "/receiver" {
					handoffs.Add(1)
				}
				return roundTripper.RoundTrip(request)
			})
		}
		return roundTripper
	}
	receiverGetConn := make(chan struct{})
	var receiverTraceOnce sync.Once
	do := func(path, method string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		var body io.Reader
		if method == "POST" {
			body = strings.NewReader("one-write")
		}
		if path == "/receiver" {
			ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{GetConn: func(string) { receiverTraceOnce.Do(func() { close(receiverGetConn) }) }})
		}
		request, _ := stdhttp.NewRequestWithContext(ctx, method, server.URL+path, body)
		response, err := pool.httpClient().Do(request)
		if err != nil {
			return "", err
		}
		data, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if response.StatusCode != 200 {
			return string(data), fmt.Errorf("status %d", response.StatusCode)
		}
		return string(data), err
	}
	wait := func(entered <-chan struct{}, name string) {
		t.Helper()
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			t.Fatalf("%s never entered", name)
		}
	}
	first := make(chan error, 1)
	go func() { _, err := do("/hold-one", "GET"); first <- err }()
	wait(holdOne, "initial H1 request")
	origin := make(chan error, 1)
	go func() {
		body, err := do("/origin", "POST")
		if err == nil && body != "1" {
			err = fmt.Errorf("origin wire version %s", body)
		}
		origin <- err
	}()
	wait(dialEntered, "speculative dial")
	finishOne()
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if err := <-origin; err != nil {
		t.Fatalf("origin did not reuse existing H1 connection: %v", err)
	}
	allowH2.Store(true)
	second := make(chan error, 1)
	go func() { _, err := do("/hold-two", "GET"); second <- err }()
	wait(holdTwo, "second H1 request")
	receiver := make(chan error, 1)
	go func() {
		body, err := do("/receiver", "POST")
		if err == nil && body != "2" {
			err = fmt.Errorf("receiver wire version %s", body)
		}
		receiver <- err
	}()
	wait(receiverGetConn, "receiver native connection admission")
	finishDial()
	if err := <-receiver; err != nil {
		finishTwo()
		t.Fatalf("detached H2 handoff exposed an unauthored POST failure: %v", err)
	}
	if posts.Load() != 2 || upgrades.Load() < 2 || handoffs.Load() != 1 || peak.Load() > 2 {
		finishTwo()
		t.Fatalf("handoff/retry missing, duplicated POST, or lost ceiling: posts=%d upgrades=%d handoffs=%d peak=%d", posts.Load(), upgrades.Load(), handoffs.Load(), peak.Load())
	}
	finishTwo()
	if err := <-second; err != nil {
		t.Fatal(err)
	}
	_ = h.Teardown(context.Background())
	waitVersionSockets(t, open, 0)
}
