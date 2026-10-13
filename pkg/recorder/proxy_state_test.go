package recorder

import (
	"net"
	"net/http"
	"net/url"
	"testing"
)

// ProxyState says whether the proxy listens and, when it does not, why: the
// studio offers HTTP proxy recording only when a browser can use it.
func TestProxyStateReportsWhyTheProxyIsNotRunning(t *testing.T) {
	t.Run("not started", func(t *testing.T) {
		state := New("127.0.0.1:0").ProxyState()
		if state.Running || state.ReasonCode != ProxyNotStarted || state.Reason == "" {
			t.Fatalf("state = %+v, want not running with %q", state, ProxyNotStarted)
		}
	})

	t.Run("production refusal", func(t *testing.T) {
		t.Setenv("APEXLOAD_ENV", "production")
		t.Setenv(envProxyAllowProduction, "")
		rec := New("127.0.0.1:0")
		if err := rec.StartProxy(); err == nil {
			t.Fatal("hosted production proxy started without the opt-in")
		}
		state := rec.ProxyState()
		if state.Running || state.ReasonCode != ProxyProductionDisabled || state.Reason == "" {
			t.Fatalf("state = %+v, want %q with the cause", state, ProxyProductionDisabled)
		}
	})

	t.Run("configuration refusal", func(t *testing.T) {
		t.Setenv(envProxyAuthToken, "")
		t.Setenv(envProxyAllowUnauthenticated, "")
		rec := New("0.0.0.0:0")
		if err := rec.StartProxy(); err == nil {
			t.Fatal("an unauthenticated wide bind started")
		}
		if state := rec.ProxyState(); state.ReasonCode != ProxyConfiguration || state.Loopback {
			t.Fatalf("state = %+v, want %q on a wide bind", state, ProxyConfiguration)
		}
	})

	t.Run("bind failure, then a later start clears it", func(t *testing.T) {
		taken, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		rec := New(taken.Addr().String())
		if err := rec.StartProxy(); err == nil {
			t.Fatal("the proxy bound a port already in use")
		}
		if state := rec.ProxyState(); state.ReasonCode != ProxyBindFailed || !state.Loopback {
			t.Fatalf("state = %+v, want %q on loopback", state, ProxyBindFailed)
		}
		_ = taken.Close()
		if err := rec.StartProxy(); err != nil {
			t.Fatalf("StartProxy() once the port is free: %v", err)
		}
		t.Cleanup(func() { _ = rec.StopProxy() })
		if state := rec.ProxyState(); !state.Running || state.ReasonCode != "" || state.Reason != "" {
			t.Fatalf("state = %+v, want running with no reason", state)
		}
	})

	t.Run("stopped", func(t *testing.T) {
		rec := New("127.0.0.1:0")
		if err := rec.StartProxy(); err != nil {
			t.Fatal(err)
		}
		if err := rec.StopProxy(); err != nil {
			t.Fatal(err)
		}
		if state := rec.ProxyState(); state.Running || state.ReasonCode != ProxyStopped {
			t.Fatalf("state = %+v, want %q", state, ProxyStopped)
		}
	})

	t.Run("private targets", func(t *testing.T) {
		if New("127.0.0.1:0").ProxyState().PrivateTargetsAllowed {
			t.Fatal("private targets reported allowed by default")
		}
		allowPrivateProxyTargets(t)
		if !New("127.0.0.1:0").ProxyState().PrivateTargetsAllowed {
			t.Fatal("private targets not reported allowed with the override")
		}
	})
}

// A request the proxy refuses for its target counts against the recording,
// so an author learns why a private application's traffic never arrived.
func TestRejectedPrivateTargetsAreCountedOnTheRecording(t *testing.T) {
	t.Setenv(envProxyAllowPrivateTargets, "")
	rec := New("127.0.0.1:0")
	if err := rec.StartProxy(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rec.StopProxy() })

	// Not recording: nothing to count against.
	if got := proxyRequestStatus(t, rec.ProxyAddr(), "http://127.0.0.1:1/idle", ""); got != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", got)
	}
	session, err := rec.StartSession("rec-rejected", "Rejected", SessionFilters{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"http://127.0.0.1:1/cart", "http://10.1.2.3/orders"} {
		if got := proxyRequestStatus(t, rec.ProxyAddr(), target, ""); got != http.StatusForbidden {
			t.Fatalf("%s status = %d, want 403", target, got)
		}
	}
	snapshot := session.snapshot()
	if snapshot.RejectedRequests != 2 || snapshot.LastRejectedHost != "10.1.2.3" || snapshot.RequestCount != 0 {
		t.Fatalf("rejected = %d last = %q recorded = %d, want 2, 10.1.2.3, 0", snapshot.RejectedRequests, snapshot.LastRejectedHost, snapshot.RequestCount)
	}

	// Paused: the browser is still refused, but the recording is not charged.
	if _, err := rec.PauseSession("rec-rejected"); err != nil {
		t.Fatal(err)
	}
	_ = proxyRequestStatus(t, rec.ProxyAddr(), "http://10.9.9.9/", "")
	if got := session.snapshot().RejectedRequests; got != 2 {
		t.Fatalf("rejected while paused = %d, want 2", got)
	}
}

// A name that resolves to a private address is refused when the proxy dials
// it; that refusal is recognised through the client's error wrapping.
func TestDialTimeTargetRefusalIsRecognised(t *testing.T) {
	t.Setenv(envProxyAllowPrivateTargets, "")
	dialErr := proxyDialControl("tcp", "10.0.0.7:443", nil)
	if dialErr == nil {
		t.Fatal("dial control allowed a private address")
	}
	wrapped := &url.Error{Op: "Get", URL: "https://intranet.example/", Err: &net.OpError{Op: "dial", Net: "tcp", Err: dialErr}}
	if !isRejectedProxyTarget(wrapped) {
		t.Fatalf("wrapped dial refusal not recognised: %v", wrapped)
	}
	if isRejectedProxyTarget(&url.Error{Op: "Get", URL: "https://intranet.example/", Err: net.ErrClosed}) {
		t.Fatal("an ordinary upstream failure was counted as a refused target")
	}
	if isRejectedProxyTarget(validateProxyTargetURL("ftp://files.example/")) {
		t.Fatal("a malformed target was counted as a refused private target")
	}
}
