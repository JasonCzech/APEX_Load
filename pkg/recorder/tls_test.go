package recorder

import (
	"bufio"
	"context"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/boundedcache"
)

type stalledRecorderBody struct {
	closed chan struct{}
	once   sync.Once
}

type contextOnlyStalledRecorderBody struct {
	ctx    context.Context
	closed chan struct{}
	once   sync.Once
}

func (b *contextOnlyStalledRecorderBody) Read([]byte) (int, error) {
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}

func (b *contextOnlyStalledRecorderBody) Close() error {
	b.once.Do(func() { close(b.closed) })
	return nil
}

func (b *stalledRecorderBody) Read([]byte) (int, error) {
	<-b.closed
	return 0, io.ErrClosedPipe
}

func (b *stalledRecorderBody) Close() error {
	b.once.Do(func() { close(b.closed) })
	return nil
}

func TestInactivityTimeoutBodyBoundsStalledRead(t *testing.T) {
	body := &stalledRecorderBody{closed: make(chan struct{})}
	bounded := &inactivityTimeoutBody{body: body, timeout: 20 * time.Millisecond}
	started := time.Now()
	_, err := bounded.Read(make([]byte, 1))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("stalled read took %s", elapsed)
	}
}

func TestInactivityTimeoutBodyAllowsProgress(t *testing.T) {
	bounded := &inactivityTimeoutBody{body: io.NopCloser(strings.NewReader("response")), timeout: time.Second}
	got, err := io.ReadAll(bounded)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != "response" {
		t.Fatalf("body = %q", got)
	}
}

func TestInactivityTimeoutBodyCancelsOwningRequestContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	body := &contextOnlyStalledRecorderBody{ctx: ctx, closed: make(chan struct{})}
	bounded := &inactivityTimeoutBody{body: body, timeout: 20 * time.Millisecond, cancel: cancel}

	_, err := bounded.Read(make([]byte, 1))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want deadline exceeded", err)
	}
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("request context error = %v, want canceled", ctx.Err())
	}
	// The request handler owns response-body cleanup. The timer must not race
	// that deferred Close after cancellation has already interrupted Read.
	select {
	case <-body.closed:
		t.Fatal("timeout callback closed a request-owned response body")
	default:
	}
	if err := bounded.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case <-body.closed:
	case <-time.After(time.Second):
		t.Fatal("response body was not closed by its owner after request cancellation")
	}
}

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
	if r.caCert.Subject.CommonName != "APEX Load Recording Proxy CA" {
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

func TestReadRecorderCAFileRejectsOversizedMaterial(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oversized.pem")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", int(maxRecorderCAFileBytes)+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readRecorderCAFile(path); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized recorder CA error = %v", err)
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

func TestGenerateLeafCertCacheIsBoundedAndCoalescesConcurrentGeneration(t *testing.T) {
	r := newTLSRecorder(t)
	r.certCache = boundedcache.NewStringCache[*tls.Certificate](2, maxRecorderCertificateHostBytes, 1024)

	first, err := r.generateLeafCert(" First.Example.Test. ")
	if err != nil {
		t.Fatal(err)
	}
	firstAgain, err := r.generateLeafCert("first.example.test")
	if err != nil {
		t.Fatal(err)
	}
	if firstAgain != first {
		t.Fatal("normalized certificate hostname did not hit the cache")
	}
	if _, err := r.generateLeafCert("second.example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.generateLeafCert("third.example.test"); err != nil {
		t.Fatal(err)
	}
	if got := r.certCacheLenForTesting(); got != 2 {
		t.Fatalf("leaf certificate cache entries = %d, want 2", got)
	}

	const callers = 8
	results := make(chan *tls.Certificate, callers)
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cert, err := r.generateLeafCert("concurrent.example.test")
			results <- cert
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var expected *tls.Certificate
	for cert := range results {
		if expected == nil {
			expected = cert
		} else if cert != expected {
			t.Fatal("concurrent generation returned distinct certificates for one hostname")
		}
	}
}

func TestGenerateLeafCertRejectsInvalidHostnameSize(t *testing.T) {
	r := newTLSRecorder(t)
	for _, hostname := range []string{" ", strings.Repeat("a", maxRecorderCertificateHostBytes+1)} {
		if _, err := r.generateLeafCert(hostname); err == nil {
			t.Fatalf("generateLeafCert accepted hostname length %d", len(hostname))
		}
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
	if cd := rr.Header().Get("Content-Disposition"); !strings.Contains(cd, "apexload-ca.crt") {
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
	allowPrivateProxyTargets(t)
	t.Setenv(envRecorderTLSInsecure, "true")
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
	if !strings.Contains(recorded.URL, backendHost) {
		t.Fatalf("expected recorded URL to preserve CONNECT host %s, got %s", backendHost, recorded.URL)
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
	bundleInfo, err := os.Stat(persistentCABundlePath(keyPath))
	if err != nil {
		t.Fatalf("stat atomic CA bundle: %v", err)
	}
	if got := bundleInfo.Mode().Perm(); runtime.GOOS != "windows" && got != 0o600 {
		t.Fatalf("atomic CA bundle mode = %o, want 600", got)
	}

	second := NewWithCAFiles(":0", certPath, keyPath)
	if err := second.initCA(); err != nil {
		t.Fatalf("second initCA failed: %v", err)
	}

	if got := string(second.GetCACertPEM()); got != firstPEM {
		t.Fatal("expected recorder CA to persist across recorder instances")
	}
}

func TestInitCAMigratesLegacyPKCS8Pair(t *testing.T) {
	source := newTLSRecorder(t)
	if err := source.initCA(); err != nil {
		t.Fatalf("initialize source CA: %v", err)
	}

	dir := t.TempDir()
	certPath := filepath.Join(dir, "ca-cert.pem")
	keyPath := filepath.Join(dir, "ca-key.pem")
	if err := os.WriteFile(certPath, source.caCertPEM, 0o644); err != nil {
		t.Fatalf("write legacy CA certificate: %v", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(source.caKey)
	if err != nil {
		t.Fatalf("marshal legacy PKCS#8 key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatalf("write legacy PKCS#8 key: %v", err)
	}

	migrated := NewWithCAFiles(":0", certPath, keyPath)
	if err := migrated.initCA(); err != nil {
		t.Fatalf("initialize recorder from legacy PKCS#8 pair: %v", err)
	}
	if got := string(migrated.GetCACertPEM()); got != string(source.GetCACertPEM()) {
		t.Fatal("legacy PKCS#8 CA was replaced during migration")
	}
	if _, err := loadPersistentCABundle(persistentCABundlePath(keyPath), time.Now()); err != nil {
		t.Fatalf("load migrated atomic CA bundle: %v", err)
	}
}

func TestValidatePersistentCAPair(t *testing.T) {
	first := newTLSRecorder(t)
	if err := first.initCA(); err != nil {
		t.Fatalf("initialize first CA: %v", err)
	}
	second := newTLSRecorder(t)
	if err := second.initCA(); err != nil {
		t.Fatalf("initialize second CA: %v", err)
	}

	withoutCertSign := *first.caCert
	withoutCertSign.KeyUsage &^= x509.KeyUsageCertSign
	notCA := *first.caCert
	notCA.IsCA = false

	tests := []struct {
		name string
		cert *x509.Certificate
		key  *rsa.PrivateKey
		now  time.Time
		want string
	}{
		{
			name: "valid",
			cert: first.caCert,
			key:  first.caKey,
			now:  time.Now(),
		},
		{
			name: "mismatched key",
			cert: first.caCert,
			key:  second.caKey,
			now:  time.Now(),
			want: "does not match",
		},
		{
			name: "invalid key",
			cert: first.caCert,
			key:  &rsa.PrivateKey{},
			now:  time.Now(),
			want: "missing modulus",
		},
		{
			name: "not yet valid",
			cert: first.caCert,
			key:  first.caKey,
			now:  first.caCert.NotBefore.Add(-time.Second),
			want: "not valid before",
		},
		{
			name: "expired",
			cert: first.caCert,
			key:  first.caKey,
			now:  first.caCert.NotAfter,
			want: "expired",
		},
		{
			name: "missing certificate signing usage",
			cert: &withoutCertSign,
			key:  first.caKey,
			now:  time.Now(),
			want: "certificate signing",
		},
		{
			name: "not a CA",
			cert: &notCA,
			key:  first.caKey,
			now:  time.Now(),
			want: "not a valid certificate authority",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validatePersistentCAPair(test.cert, test.key, test.now)
			if test.want == "" {
				if err != nil {
					t.Fatalf("validatePersistentCAPair() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validatePersistentCAPair() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestInitCARotatesMismatchedLegacyPair(t *testing.T) {
	first := newTLSRecorder(t)
	if err := first.initCA(); err != nil {
		t.Fatalf("initialize first CA: %v", err)
	}
	second := newTLSRecorder(t)
	if err := second.initCA(); err != nil {
		t.Fatalf("initialize second CA: %v", err)
	}

	dir := t.TempDir()
	certPath := filepath.Join(dir, "ca-cert.pem")
	keyPath := filepath.Join(dir, "ca-key.pem")
	if err := os.WriteFile(certPath, first.caCertPEM, 0o644); err != nil {
		t.Fatalf("write legacy CA certificate: %v", err)
	}
	secondKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(second.caKey),
	})
	if err := os.WriteFile(keyPath, secondKeyPEM, 0o600); err != nil {
		t.Fatalf("write mismatched legacy CA key: %v", err)
	}

	recovered := NewWithCAFiles(":0", certPath, keyPath)
	if err := recovered.initCA(); err != nil {
		t.Fatalf("initialize recorder with mismatched legacy pair: %v", err)
	}
	if got := string(recovered.GetCACertPEM()); got == string(first.caCertPEM) {
		t.Fatal("recorder loaded a certificate whose persisted private key did not match")
	}

	legacy, err := loadPersistentCAPairFiles(certPath, keyPath, time.Now())
	if err != nil {
		t.Fatalf("load repaired legacy pair: %v", err)
	}
	if string(legacy.cert.Raw) != string(recovered.caCert.Raw) {
		t.Fatal("legacy pair was not repaired to the newly generated CA")
	}
	bundle, err := loadPersistentCABundle(persistentCABundlePath(keyPath), time.Now())
	if err != nil {
		t.Fatalf("load recovered atomic bundle: %v", err)
	}
	if string(bundle.cert.Raw) != string(recovered.caCert.Raw) {
		t.Fatal("atomic bundle does not contain the recovered CA")
	}
}

func TestInitCARotatesIncompleteLegacyPair(t *testing.T) {
	source := newTLSRecorder(t)
	if err := source.initCA(); err != nil {
		t.Fatalf("initialize source CA: %v", err)
	}

	dir := t.TempDir()
	certPath := filepath.Join(dir, "ca-cert.pem")
	keyPath := filepath.Join(dir, "ca-key.pem")
	if err := os.WriteFile(certPath, source.caCertPEM, 0o644); err != nil {
		t.Fatalf("write incomplete legacy CA certificate: %v", err)
	}

	recovered := NewWithCAFiles(":0", certPath, keyPath)
	if err := recovered.initCA(); err != nil {
		t.Fatalf("initialize recorder with incomplete legacy pair: %v", err)
	}
	if got := string(recovered.GetCACertPEM()); got == string(source.GetCACertPEM()) {
		t.Fatal("recorder reused an incomplete persisted CA generation")
	}
	if _, err := loadPersistentCAPairFiles(certPath, keyPath, time.Now()); err != nil {
		t.Fatalf("load repaired legacy pair: %v", err)
	}
	if _, err := loadPersistentCABundle(persistentCABundlePath(keyPath), time.Now()); err != nil {
		t.Fatalf("load recovered atomic bundle: %v", err)
	}
}

func TestInitCARecoversAfterInterruptedLegacyMirrorCommit(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "ca-cert.pem")
	keyPath := filepath.Join(dir, "ca-key.pem")

	original := NewWithCAFiles(":0", certPath, keyPath)
	if err := original.initCA(); err != nil {
		t.Fatalf("initialize original CA: %v", err)
	}
	originalPEM := string(original.GetCACertPEM())

	replacement := newTLSRecorder(t)
	if err := replacement.initCA(); err != nil {
		t.Fatalf("initialize replacement CA: %v", err)
	}
	replacementKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(replacement.caKey),
	})
	// Simulate a crash after the committed bundle was published but after only
	// one of the two historical compatibility files was updated.
	if err := os.WriteFile(keyPath, replacementKeyPEM, 0o600); err != nil {
		t.Fatalf("write partially committed legacy key: %v", err)
	}
	if err := os.WriteFile(persistentCABundlePath(keyPath)+".tmp-interrupted", []byte("partial"), 0o600); err != nil {
		t.Fatalf("write abandoned temporary bundle: %v", err)
	}

	restarted := NewWithCAFiles(":0", certPath, keyPath)
	if err := restarted.initCA(); err != nil {
		t.Fatalf("initialize recorder after interrupted mirror commit: %v", err)
	}
	if got := string(restarted.GetCACertPEM()); got != originalPEM {
		t.Fatal("recorder did not load the last atomically committed CA generation")
	}

	repaired, err := loadPersistentCAPairFiles(certPath, keyPath, time.Now())
	if err != nil {
		t.Fatalf("load repaired compatibility pair: %v", err)
	}
	if string(repaired.cert.Raw) != string(original.caCert.Raw) {
		t.Fatal("compatibility pair was not repaired from the committed bundle")
	}
}

func TestInitCALoadsLegacyPairAfterInterruptedBundleWrite(t *testing.T) {
	source := newTLSRecorder(t)
	if err := source.initCA(); err != nil {
		t.Fatalf("initialize source CA: %v", err)
	}

	dir := t.TempDir()
	certPath := filepath.Join(dir, "ca-cert.pem")
	keyPath := filepath.Join(dir, "ca-key.pem")
	if err := os.WriteFile(certPath, source.caCertPEM, 0o644); err != nil {
		t.Fatalf("write legacy CA certificate: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(source.caKey),
	})
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatalf("write legacy CA key: %v", err)
	}
	bundlePath := persistentCABundlePath(keyPath)
	if err := os.WriteFile(bundlePath+".tmp-interrupted", []byte("partial"), 0o600); err != nil {
		t.Fatalf("write abandoned temporary bundle: %v", err)
	}

	restarted := NewWithCAFiles(":0", certPath, keyPath)
	if err := restarted.initCA(); err != nil {
		t.Fatalf("initialize recorder after interrupted bundle write: %v", err)
	}
	if got := string(restarted.GetCACertPEM()); got != string(source.GetCACertPEM()) {
		t.Fatal("recorder did not preserve the valid legacy CA after an interrupted bundle write")
	}
	if _, err := loadPersistentCABundle(bundlePath, time.Now()); err != nil {
		t.Fatalf("legacy CA was not migrated to a valid atomic bundle: %v", err)
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

func TestStripDefaultHTTPSPort(t *testing.T) {
	cases := []struct{ in, want string }{
		{"example.com:443", "example.com"},
		{"example.com:8443", "example.com:8443"},
		{"example.com", "example.com"},
		{"[::1]:443", "[::1]"},
		{"[2001:db8::1]:8443", "[2001:db8::1]:8443"},
	}
	for _, tc := range cases {
		if got := stripDefaultHTTPSPort(tc.in); got != tc.want {
			t.Errorf("stripDefaultHTTPSPort(%q) = %q, want %q", tc.in, got, tc.want)
		}
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
