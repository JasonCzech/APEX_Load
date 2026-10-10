package http

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/h3fixture"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestHTTP3OneConnectionPerHostMultiplexesRequests(t *testing.T) {
	fixture := h3fixture.Start(t, h3fixture.Options{})
	h := http3Session(t, map[string]any{"max_conns_per_host": 1})
	var wg sync.WaitGroup
	results := make([]*protocol.Result, 5)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = run(t, h, h3Get(t, fixture.URL(fmt.Sprintf("/slow?ms=200&i=%d", i)), "3"))
		}()
	}
	wg.Wait()
	for i, result := range results {
		if !result.Success {
			t.Fatalf("request %d: %s", i, result.ErrorMessage)
		}
	}
	if n := fixture.QUICConnections(); n != 1 {
		t.Fatalf("max_conns_per_host=1 opened %d QUIC connections", n)
	}
}

func TestHTTP3SharesTheConnectionBudgetWithTCP(t *testing.T) {
	fixture := h3fixture.Start(t, h3fixture.Options{})
	h := http3Session(t, map[string]any{"max_conns_per_host": 1})
	// An idle TCP connection holds the host's only slot; the QUIC dial
	// evicts it rather than exceeding the budget.
	if r := run(t, h, h3Get(t, fixture.URL("/echo"), "2")); !r.Success {
		t.Fatal(r.ErrorMessage)
	}
	if r := run(t, h, h3Get(t, fixture.URL("/echo"), "3")); !r.Success {
		t.Fatalf("QUIC after an idle TCP connection: %s", r.ErrorMessage)
	}
	key := strings.TrimPrefix(fixture.URL(""), "https://")
	h.connections.mu.Lock()
	held := h.connections.counts[key]
	h.connections.mu.Unlock()
	if held != 1 {
		t.Fatalf("host %s holds %d budget slots, want 1", key, held)
	}
	// And back: the idle QUIC connection gives its slot to a TCP request.
	if r := run(t, h, h3Get(t, fixture.URL("/echo"), "2")); !r.Success {
		t.Fatalf("TCP after an idle QUIC connection: %s", r.ErrorMessage)
	}
}

func TestHTTP3RequestQueuedForTheBudgetCanBeCancelled(t *testing.T) {
	fixture := h3fixture.Start(t, h3fixture.Options{})
	h := http3Session(t, map[string]any{"max_conns_per_host": 1})
	// A slow TCP request keeps the host's only slot busy.
	busy := make(chan *protocol.Result, 1)
	go func() { busy <- run(t, h, h3Get(t, fixture.URL("/slow?ms=1500"), "2")) }()
	time.Sleep(200 * time.Millisecond)
	queued := h3Get(t, fixture.URL("/echo"), "3")
	queued.TimeoutMs = 300
	started := time.Now()
	result := run(t, h, queued)
	if result.Success || time.Since(started) > time.Second {
		t.Fatalf("queued request: success=%v after %s (%s)", result.Success, time.Since(started), result.ErrorMessage)
	}
	if r := <-busy; !r.Success {
		t.Fatalf("busy request: %s", r.ErrorMessage)
	}
}

func TestHTTP3FollowsPACDirectRoutesAndRefusesPACProxies(t *testing.T) {
	fixture := h3fixture.Start(t, h3fixture.Options{})
	for _, tc := range []struct{ pac, want string }{
		{`function FindProxyForURL(url, host) { return "DIRECT"; }`, ""},
		{`function FindProxyForURL(url, host) { return "PROXY 127.0.0.1:9; DIRECT"; }`, "proxy_route"},
	} {
		pac := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, tc.pac) }))
		h := http3Session(t, map[string]any{"proxy_mode": "pac", "proxy_pac_url": pac.URL, protocol.TargetPolicyConfigKey: []string{"127.0.0.1"}})
		result := run(t, h, h3Get(t, fixture.URL("/echo"), "3"))
		pac.Close()
		switch {
		case tc.want == "" && (!result.Success || result.HTTPVersionNegotiated != "3"):
			t.Fatalf("PAC DIRECT: %s", result.ErrorMessage)
		case tc.want != "" && result.ProtocolEvidence["http_version_selection_reason"] != tc.want:
			t.Fatalf("PAC proxy: %v", result.ProtocolEvidence)
		}
	}
}

func TestHTTP3AttemptBudgetCapsLayeredRetries(t *testing.T) {
	fixture := h3fixture.Start(t, h3fixture.Options{})
	h := http3Session(t, nil)
	if r := run(t, h, h3Get(t, fixture.URL("/echo"), "3")); !r.Success {
		t.Fatal(r.ErrorMessage)
	}
	// Two hops, each rejected once: the second hop's retry would be the
	// action's fourth attempt, above its budget of one hop plus one retry
	// plus the redirect.
	redirects := 1
	action := h3Get(t, fixture.URL("/redirect?to="+url.QueryEscape(fixture.URL("/effect?key=b"))), "3")
	action.MaxRedirects = &redirects
	fixture.RejectQUICSequence(true, false, true, false)
	result := run(t, h, action)
	if result.Success || !strings.Contains(result.ErrorMessage, "attempt budget exhausted") {
		t.Fatalf("result: success=%v message=%q evidence=%v", result.Success, result.ErrorMessage, result.ProtocolEvidence)
	}
	if fixture.Ledger.Effects("b") != 0 {
		t.Fatal("the capped request took effect")
	}
	// Within the budget, one rejection is absorbed by quic-go's retry.
	fixture.RejectQUICSequence(true, false)
	if r := run(t, h, h3Get(t, fixture.URL("/effect?key=c"), "3")); !r.Success || r.ProtocolEvidence["attempt_count"] != 2 {
		t.Fatalf("one rejection: %s %v", r.ErrorMessage, r.ProtocolEvidence)
	}
	if !errors.Is(fmt.Errorf("%w (3)", ErrHTTP3AttemptBudget), ErrHTTP3AttemptBudget) {
		t.Fatal("the budget error does not wrap its sentinel")
	}
}
