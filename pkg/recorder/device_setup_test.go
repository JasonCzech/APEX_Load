package recorder

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Listen rebinds the proxy with a new token and keeps its sessions;
// CloseListener lets it bind again, while StopProxy ends it for good.
func TestListenRebindsAndKeepsSessions(t *testing.T) {
	t.Setenv(envProxyAllowPrivateTargets, "")
	rec := New("127.0.0.1:0")
	t.Cleanup(func() { _ = rec.StopProxy() })
	if err := rec.Listen("127.0.0.1:0", "first-password"); err != nil {
		t.Fatal(err)
	}
	first := rec.ProxyAddr()
	if _, err := rec.StartSession("kept", "Kept", SessionFilters{}, nil); err != nil {
		t.Fatal(err)
	}
	if got := proxyRequestStatus(t, first, "http://10.1.2.3/", "first-password"); got != http.StatusForbidden {
		t.Fatalf("authorized request = %d, want the private-target refusal", got)
	}
	if err := rec.Listen("127.0.0.1:0", "second-password"); err != nil {
		t.Fatal(err)
	}
	second := rec.ProxyAddr()
	if conn, err := net.DialTimeout("tcp", first, time.Second); err == nil && first != second {
		conn.Close()
		t.Fatalf("the first listener %s still accepts", first)
	}
	if got := proxyRequestStatus(t, second, "http://10.1.2.3/", "first-password"); got != http.StatusProxyAuthRequired {
		t.Fatalf("the old password = %d, want 407", got)
	}
	if _, ok := rec.GetSession("kept"); !ok {
		t.Fatal("rebinding dropped the session")
	}
	if err := rec.CloseListener(); err != nil || rec.ProxyRunning() {
		t.Fatalf("close listener: %v, running %v", err, rec.ProxyRunning())
	}
	if err := rec.Listen("127.0.0.1:0", "third-password"); err != nil {
		t.Fatalf("listen after close: %v", err)
	}
	if err := rec.StopProxy(); err != nil {
		t.Fatal(err)
	}
	if err := rec.Listen("127.0.0.1:0", "fourth-password"); !errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("listen after stop = %v, want ErrServerClosed", err)
	}
}

// The setup page and certificate are served to a device only in device
// mode, without the proxy password; other requests to the proxy itself
// still need it.
func TestDeviceSetupIsServedOnlyInDeviceMode(t *testing.T) {
	t.Setenv("APEXLOAD_RECORDER_CA_DIR", t.TempDir())
	rec := New("127.0.0.1:0")
	t.Cleanup(func() { _ = rec.StopProxy() })
	if err := rec.Listen("127.0.0.1:0", "device-password"); err != nil {
		t.Fatal(err)
	}
	get := func(path string) (int, string) {
		t.Helper()
		resp, err := http.Get("http://" + rec.ProxyAddr() + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}
	if code, _ := get("/"); code != http.StatusProxyAuthRequired {
		t.Fatalf("setup page outside device mode = %d, want 407", code)
	}
	rec.SetDeviceSetup(true)
	if code, body := get("/"); code != http.StatusOK || !strings.Contains(body, DeviceSetupCertificatePath) || strings.Contains(body, "device-password") {
		t.Fatalf("setup page = %d: %s", code, body)
	}
	if code, body := get(DeviceSetupCertificatePath); code != http.StatusOK || len(body) == 0 {
		t.Fatalf("certificate = %d (%d bytes)", code, len(body))
	}
	if code, _ := get("/other"); code != http.StatusNotFound {
		t.Fatalf("another path = %d, want 404", code)
	}
	// Through the proxy, the device asks for the proxy's own address.
	if got := proxyRequestStatus(t, rec.ProxyAddr(), "http://"+rec.ProxyAddr()+"/", ""); got != http.StatusOK {
		t.Fatalf("setup page through the proxy = %d", got)
	}
	// Anything else still needs the password.
	if got := proxyRequestStatus(t, rec.ProxyAddr(), "http://example.test/", ""); got != http.StatusProxyAuthRequired {
		t.Fatalf("an unauthorized proxied request = %d, want 407", got)
	}
}

// Requests the host filters leave out are counted, so a capture shows that
// the device's traffic arrives.
func TestFilteredRequestsAreCounted(t *testing.T) {
	t.Setenv(envProxyAllowPrivateTargets, "true")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()
	rec := New("127.0.0.1:0")
	if err := rec.StartProxy(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rec.StopProxy() })
	session, err := rec.StartSession("filtered", "Filtered", SessionFilters{IncludeHosts: []string{"salesforce.com"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := proxyRequestStatus(t, rec.ProxyAddr(), upstream.URL+"/telemetry", ""); got != http.StatusOK {
		t.Fatalf("status = %d", got)
	}
	snapshot := session.snapshot()
	if snapshot.FilteredRequests != 1 || snapshot.LastFilteredHost != "127.0.0.1" || snapshot.RequestCount != 0 {
		t.Fatalf("filtered = %d last = %q recorded = %d", snapshot.FilteredRequests, snapshot.LastFilteredHost, snapshot.RequestCount)
	}
}
