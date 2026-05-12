package recorder

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTLSRecorder(t *testing.T) *Recorder {
	t.Helper()
	dir := t.TempDir()
	return NewWithCAFiles(":0", filepath.Join(dir, "ca-cert.pem"), filepath.Join(dir, "ca-key.pem"))
}

func TestInitCA(t *testing.T) {
	r := newTLSRecorder(t)
	if err := r.initCA(); err != nil {
		t.Fatalf("initCA failed: %v", err)
	}

	if r.caCert == nil {
		t.Fatal("expected caCert to be set")
	}
	if r.caKey == nil {
		t.Fatal("expected caKey to be set")
	}
	if r.caCertPEM == nil {
		t.Fatal("expected caCertPEM to be set")
	}
	if !r.caCert.IsCA {
		t.Fatal("expected certificate to be a CA")
	}
	if r.caCert.Subject.CommonName != "Stormrunner Recording Proxy CA" {
		t.Fatalf("unexpected CA CN: %s", r.caCert.Subject.CommonName)
	}

	// Verify PEM can be decoded.
	block, _ := pem.Decode(r.caCertPEM)
	if block == nil {
		t.Fatal("expected valid PEM block")
	}
	if block.Type != "CERTIFICATE" {
		t.Fatalf("expected CERTIFICATE PEM type, got %s", block.Type)
	}
}

func TestInitCAIdempotent(t *testing.T) {
	r := newTLSRecorder(t)
	if err := r.initCA(); err != nil {
		t.Fatalf("first initCA failed: %v", err)
	}
	firstCert := r.caCertPEM

	if err := r.initCA(); err != nil {
		t.Fatalf("second initCA failed: %v", err)
	}
	if string(r.caCertPEM) != string(firstCert) {
		t.Fatal("expected same CA cert on second call (sync.Once)")
	}
}

func TestGetCACertPEM(t *testing.T) {
	r := newTLSRecorder(t)
	if r.GetCACertPEM() != nil {
		t.Fatal("expected nil before init")
	}

	r.initCA()
	pem := r.GetCACertPEM()
	if pem == nil {
		t.Fatal("expected non-nil after init")
	}
	if len(pem) == 0 {
		t.Fatal("expected non-empty PEM")
	}
}

func TestGenerateLeafCert(t *testing.T) {
	r := newTLSRecorder(t)

	cert, err := r.generateLeafCert("example.com")
	if err != nil {
		t.Fatalf("generateLeafCert failed: %v", err)
	}
	if cert == nil {
		t.Fatal("expected non-nil certificate")
	}
	if len(cert.Certificate) != 2 {
		t.Fatalf("expected 2 certs in chain (leaf + CA), got %d", len(cert.Certificate))
	}

	// Parse the leaf cert and verify properties.
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatalf("failed to parse leaf cert: %v", err)
	}
	if leaf.Subject.CommonName != "example.com" {
		t.Fatalf("expected CN=example.com, got %s", leaf.Subject.CommonName)
	}
	if len(leaf.DNSNames) != 1 || leaf.DNSNames[0] != "example.com" {
		t.Fatalf("expected SAN DNS:example.com, got %v", leaf.DNSNames)
	}

	// Verify the leaf is signed by the CA.
	pool := x509.NewCertPool()
	pool.AddCert(r.caCert)
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool}); err != nil {
		t.Fatalf("leaf cert verification failed: %v", err)
	}
}

func TestGenerateLeafCertCaching(t *testing.T) {
	r := newTLSRecorder(t)

	cert1, err := r.generateLeafCert("cached.example.com")
	if err != nil {
		t.Fatal(err)
	}
	cert2, err := r.generateLeafCert("cached.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if cert1 != cert2 {
		t.Fatal("expected same cert pointer from cache")
	}

	// Different hostname should produce a different cert.
	cert3, err := r.generateLeafCert("other.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if cert1 == cert3 {
		t.Fatal("expected different cert for different hostname")
	}
}

func TestGenerateLeafCertIPAddress(t *testing.T) {
	r := newTLSRecorder(t)

	cert, err := r.generateLeafCert("192.168.1.1")
	if err != nil {
		t.Fatalf("generateLeafCert for IP failed: %v", err)
	}

	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(leaf.IPAddresses) != 1 {
		t.Fatalf("expected 1 IP SAN, got %d", len(leaf.IPAddresses))
	}
	if leaf.IPAddresses[0].String() != "192.168.1.1" {
		t.Fatalf("expected IP SAN 192.168.1.1, got %s", leaf.IPAddresses[0])
	}
	if len(leaf.DNSNames) != 0 {
		t.Fatalf("expected no DNS SANs for IP cert, got %v", leaf.DNSNames)
	}
}

func TestHandleCACert(t *testing.T) {
	r := newTLSRecorder(t)

	// Default format: DER (.crt)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/recorder/ca.pem", nil)
	r.HandleCACert(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/x-x509-ca-cert" {
		t.Fatalf("expected Content-Type application/x-x509-ca-cert, got %s", ct)
	}
	if cd := rr.Header().Get("Content-Disposition"); !strings.Contains(cd, "stormrunner-ca.crt") {
		t.Fatalf("expected Content-Disposition with .crt filename, got %s", cd)
	}
	_, err := x509.ParseCertificate(rr.Body.Bytes())
	if err != nil {
		t.Fatalf("expected valid DER certificate: %v", err)
	}

	// PEM format via query param
	rr2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/api/v1/recorder/ca.pem?format=pem", nil)
	r.HandleCACert(rr2, req2)

	if ct := rr2.Header().Get("Content-Type"); ct != "application/x-pem-file" {
		t.Fatalf("expected PEM Content-Type, got %s", ct)
	}
	block, _ := pem.Decode(rr2.Body.Bytes())
	if block == nil {
		t.Fatal("expected valid PEM in response body")
	}
}

func TestHTTPSInterception(t *testing.T) {
	// Start a TLS backend server.
	backend := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/secure" {
			w.WriteHeader(200)
			fmt.Fprint(w, `{"secure":true}`)
			return
		}
		w.WriteHeader(200)
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer backend.Close()

	// Create the recorder with a session.
	rec := newTLSRecorder(t)
	var captured []SessionEvent
	rec.StartSession("https-test", "HTTPS Test", SessionFilters{ExcludeStatic: true}, func(evt SessionEvent) {
		captured = append(captured, evt)
	})

	// Initialize CA so we can build a trust pool for the client.
	if err := rec.initCA(); err != nil {
		t.Fatalf("initCA failed: %v", err)
	}

	// Start the proxy server.
	proxyServer := httptest.NewServer(http.HandlerFunc(rec.handleProxy))
	defer proxyServer.Close()

	// Parse backend URL to get host:port.
	backendURL, _ := url.Parse(backend.URL)
	backendHost := backendURL.Host

	// Create a pipe to simulate the CONNECT tunnel.
	clientConn, proxyConn := net.Pipe()
	defer clientConn.Close()
	defer proxyConn.Close()

	// Run the proxy-side handler in a goroutine.
	go func() {
		// Build an HTTP request for CONNECT.
		connectReq := &http.Request{
			Method: "CONNECT",
			URL:    &url.URL{Host: backendHost},
			Host:   backendHost,
			Header: make(http.Header),
		}

		// Create a response writer that wraps the pipe connection.
		hijackWriter := &testHijackResponseWriter{
			conn:     proxyConn,
			header:   make(http.Header),
			hijacked: make(chan struct{}),
		}

		rec.handleConnect(hijackWriter, connectReq)
	}()

	// On the client side: read the 200 response, then TLS handshake.
	clientReader := bufio.NewReader(clientConn)
	resp, err := http.ReadResponse(clientReader, nil)
	if err != nil {
		t.Fatalf("failed to read CONNECT response: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 from CONNECT, got %d", resp.StatusCode)
	}

	// Perform TLS handshake with the proxy's generated cert.
	caPool := x509.NewCertPool()
	caPool.AddCert(rec.caCert)

	tlsConn := tls.Client(clientConn, &tls.Config{
		RootCAs:    caPool,
		ServerName: backendURL.Hostname(),
	})
	defer tlsConn.Close()

	if err := tlsConn.Handshake(); err != nil {
		t.Fatalf("TLS handshake failed: %v", err)
	}

	// Send an HTTP request through the TLS tunnel.
	reqStr := fmt.Sprintf("GET /api/secure HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", backendURL.Hostname())
	if _, err := tlsConn.Write([]byte(reqStr)); err != nil {
		t.Fatalf("failed to write request: %v", err)
	}

	// Read the response.
	tlsReader := bufio.NewReader(tlsConn)
	tunnelResp, err := http.ReadResponse(tlsReader, nil)
	if err != nil {
		t.Fatalf("failed to read tunnel response: %v", err)
	}
	defer tunnelResp.Body.Close()

	if tunnelResp.StatusCode != 200 {
		t.Fatalf("expected 200 from tunnel, got %d", tunnelResp.StatusCode)
	}

	body, _ := io.ReadAll(tunnelResp.Body)
	if !strings.Contains(string(body), "secure") {
		t.Fatalf("expected 'secure' in response body, got %s", string(body))
	}

	// Give the recorder a moment to process.
	time.Sleep(100 * time.Millisecond)

	// Verify the request was recorded.
	session, _ := rec.GetSession("https-test")
	if len(session.Requests) != 1 {
		t.Fatalf("expected 1 recorded request, got %d", len(session.Requests))
	}

	recorded := session.Requests[0]
	if recorded.Method != "GET" {
		t.Fatalf("expected GET, got %s", recorded.Method)
	}
	if !strings.Contains(recorded.URL, "/api/secure") {
		t.Fatalf("expected URL containing /api/secure, got %s", recorded.URL)
	}
	if recorded.StatusCode != 200 {
		t.Fatalf("expected status 200, got %d", recorded.StatusCode)
	}
}

func TestInitCAPersistsAcrossRecorderInstances(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "ca-cert.pem")
	keyPath := filepath.Join(dir, "ca-key.pem")

	first := NewWithCAFiles(":0", certPath, keyPath)
	if err := first.initCA(); err != nil {
		t.Fatalf("first initCA failed: %v", err)
	}
	firstPEM := string(first.GetCACertPEM())

	second := NewWithCAFiles(":0", certPath, keyPath)
	if err := second.initCA(); err != nil {
		t.Fatalf("second initCA failed: %v", err)
	}

	if got := string(second.GetCACertPEM()); got != firstPEM {
		t.Fatal("expected recorder CA to persist across recorder instances")
	}
}

func TestWriteHTTPError(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	go func() {
		writeHTTPError(serverConn, http.StatusBadGateway, "test error")
		serverConn.Close()
	}()

	reader := bufio.NewReader(clientConn)
	resp, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatalf("failed to read error response: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "test error" {
		t.Fatalf("expected 'test error', got %s", string(body))
	}
}

// testHijackResponseWriter implements http.ResponseWriter and http.Hijacker
// for testing the CONNECT handler with a net.Pipe connection.
type testHijackResponseWriter struct {
	conn       net.Conn
	header     http.Header
	statusCode int
	hijacked   chan struct{}
}

func (w *testHijackResponseWriter) Header() http.Header {
	return w.header
}

func (w *testHijackResponseWriter) Write(data []byte) (int, error) {
	return w.conn.Write(data)
}

func (w *testHijackResponseWriter) WriteHeader(statusCode int) {
	w.statusCode = statusCode
	// Write the HTTP response line so the client can read it.
	fmt.Fprintf(w.conn, "HTTP/1.1 %d %s\r\n\r\n", statusCode, http.StatusText(statusCode))
}

func (w *testHijackResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	close(w.hijacked)
	rw := bufio.NewReadWriter(bufio.NewReader(w.conn), bufio.NewWriter(w.conn))
	return w.conn, rw, nil
}
