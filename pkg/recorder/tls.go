package recorder

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/boundedcache"
)

const (
	maxRecorderCAFileBytes   int64 = 1 << 20
	persistentCABundleSuffix       = ".pair.pem"
)

// persistentCAMaterial is the validated, normalized representation of the
// recorder interception CA. The canonical on-disk form stores both PEM blocks
// in one private file so one atomic rename commits the complete generation.
// The historical separate certificate/key paths remain compatibility mirrors.
type persistentCAMaterial struct {
	cert    *x509.Certificate
	key     *rsa.PrivateKey
	certPEM []byte
	keyPEM  []byte
}

// initCA generates a self-signed root CA certificate and key pair.
// It is called lazily on the first CONNECT request or explicit initialization.
func (r *Recorder) initCA() error {
	r.caOnce.Do(func() {
		r.caErr = r.generateCA()
	})
	return r.caErr
}

func (r *Recorder) generateCA() error {
	loadErr := r.loadPersistentCA()
	if loadErr == nil {
		return nil
	}
	if loadErr != os.ErrNotExist {
		// Never use malformed, expired, or mismatched persisted material. A new
		// generation is safe (the user may need to trust the newly served CA), and
		// the canonical bundle is committed before compatibility mirrors change.
		slog.Warn("persisted recording proxy CA is unusable; rotating it", "error", loadErr)
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fmt.Errorf("generating CA key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return fmt.Errorf("generating CA serial: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "APEX Load Recording Proxy CA",
			Organization: []string{"APEX Load"},
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return fmt.Errorf("creating CA certificate: %w", err)
	}

	caCert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return fmt.Errorf("parsing CA certificate: %w", err)
	}

	material := &persistentCAMaterial{
		cert:    caCert,
		key:     key,
		certPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER}),
		keyPEM:  pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}),
	}
	if err := validatePersistentCAPair(material.cert, material.key, time.Now()); err != nil {
		return fmt.Errorf("validating generated CA: %w", err)
	}
	if err := r.savePersistentCA(certDER, key); err != nil {
		return err
	}
	r.installPersistentCA(material)

	slog.Info("generated recording proxy CA certificate")
	return nil
}

func (r *Recorder) loadPersistentCA() error {
	if strings.TrimSpace(r.caCertPath) == "" || strings.TrimSpace(r.caKeyPath) == "" {
		return os.ErrNotExist
	}

	bundlePath := persistentCABundlePath(r.caKeyPath)
	bundleMaterial, bundleErr := loadPersistentCABundle(bundlePath, time.Now())
	if bundleErr == nil {
		r.installPersistentCA(bundleMaterial)
		// A crash can happen after the canonical bundle commit but between the two
		// historical mirror updates. Repair those mirrors best-effort; they are not
		// the source of truth anymore, so a read-only legacy path must not prevent
		// the valid committed CA from loading.
		if err := r.repairLegacyCAMirrors(bundleMaterial); err != nil {
			slog.Warn("failed to repair recorder CA compatibility files", "error", err)
		}
		slog.Info("loaded recording proxy CA certificate", "path", bundlePath)
		return nil
	}

	legacyMaterial, legacyErr := loadPersistentCAPairFiles(r.caCertPath, r.caKeyPath, time.Now())
	if legacyErr == nil {
		r.installPersistentCA(legacyMaterial)
		// Transparently migrate compatible installs. Failure to create the new
		// private bundle does not invalidate a pair that was already verified.
		if err := writePersistentCABundle(bundlePath, legacyMaterial); err != nil {
			slog.Warn("failed to migrate recorder CA to atomic storage", "error", err)
		}
		slog.Info("loaded legacy recording proxy CA certificate", "path", r.caCertPath)
		return nil
	}

	if persistencePathsAbsent(bundlePath, r.caCertPath, r.caKeyPath) {
		return os.ErrNotExist
	}
	return errors.Join(
		fmt.Errorf("load atomic recorder CA bundle %q: %w", bundlePath, bundleErr),
		fmt.Errorf("load legacy recorder CA pair: %w", legacyErr),
	)
}

func persistentCABundlePath(keyPath string) string {
	keyPath = strings.TrimSpace(keyPath)
	if keyPath == "" {
		return ""
	}
	return filepath.Clean(keyPath) + persistentCABundleSuffix
}

func persistencePathsAbsent(paths ...string) bool {
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		if _, err := os.Lstat(path); err == nil || !errors.Is(err, os.ErrNotExist) {
			return false
		}
	}
	return true
}

func loadPersistentCAPairFiles(certPath, keyPath string, now time.Time) (*persistentCAMaterial, error) {
	certPEM, err := readRecorderCAFile(certPath)
	if err != nil {
		return nil, fmt.Errorf("read persisted CA certificate %q: %w", certPath, err)
	}
	keyPEM, err := readRecorderCAFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("read persisted CA key %q: %w", keyPath, err)
	}
	return parsePersistentCAPair(certPEM, keyPEM, certPath, keyPath, now)
}

func parsePersistentCAPair(certPEM, keyPEM []byte, certSource, keySource string, now time.Time) (*persistentCAMaterial, error) {
	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil || certBlock.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("parse persisted CA certificate %q: invalid PEM", certSource)
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse persisted CA certificate %q: %w", certSource, err)
	}

	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, fmt.Errorf("parse persisted CA key %q: invalid PEM", keySource)
	}
	key, err := parsePersistentCAKey(keyBlock, keySource)
	if err != nil {
		return nil, err
	}
	if err := validatePersistentCAPair(cert, key, now); err != nil {
		return nil, fmt.Errorf("validate persisted CA pair: %w", err)
	}
	return &persistentCAMaterial{
		cert:    cert,
		key:     key,
		certPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}),
		keyPEM:  pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}),
	}, nil
}

func parsePersistentCAKey(keyBlock *pem.Block, source string) (*rsa.PrivateKey, error) {
	var (
		key *rsa.PrivateKey
		err error
	)
	switch keyBlock.Type {
	case "RSA PRIVATE KEY":
		key, err = x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
	case "PRIVATE KEY":
		var parsed any
		parsed, err = x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
		if err == nil {
			var ok bool
			key, ok = parsed.(*rsa.PrivateKey)
			if !ok {
				return nil, fmt.Errorf("persisted CA key %q is not an RSA private key", source)
			}
		}
	default:
		return nil, fmt.Errorf("parse persisted CA key %q: unsupported PEM type %q", source, keyBlock.Type)
	}
	if err != nil {
		return nil, fmt.Errorf("parse persisted CA key %q: %w", source, err)
	}
	return key, nil
}

func validatePersistentCAPair(cert *x509.Certificate, key *rsa.PrivateKey, now time.Time) error {
	if cert == nil || key == nil {
		return fmt.Errorf("certificate and private key are required")
	}
	if !cert.BasicConstraintsValid || !cert.IsCA {
		return fmt.Errorf("certificate is not a valid certificate authority")
	}
	if cert.KeyUsage&x509.KeyUsageCertSign == 0 {
		return fmt.Errorf("certificate does not permit certificate signing")
	}
	if now.Before(cert.NotBefore) {
		return fmt.Errorf("certificate is not valid before %s", cert.NotBefore.UTC().Format(time.RFC3339))
	}
	if !now.Before(cert.NotAfter) {
		return fmt.Errorf("certificate expired at %s", cert.NotAfter.UTC().Format(time.RFC3339))
	}
	if key.N == nil {
		return fmt.Errorf("private key is invalid: missing modulus")
	}
	if err := key.Validate(); err != nil {
		return fmt.Errorf("private key is invalid: %w", err)
	}
	publicKey, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok || publicKey.N == nil || publicKey.N.Cmp(key.N) != 0 || publicKey.E != key.E {
		return fmt.Errorf("certificate public key does not match the private key")
	}
	return nil
}

func loadPersistentCABundle(path string, now time.Time) (*persistentCAMaterial, error) {
	content, err := readRecorderCAFile(path)
	if err != nil {
		return nil, err
	}
	remaining := bytes.TrimSpace(content)
	certBlock, remaining := pem.Decode(remaining)
	if certBlock == nil || certBlock.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("atomic CA bundle has an invalid certificate PEM block")
	}
	keyBlock, trailing := pem.Decode(remaining)
	if keyBlock == nil || len(bytes.TrimSpace(trailing)) != 0 {
		return nil, fmt.Errorf("atomic CA bundle must contain exactly one certificate and one private key")
	}
	certPEM := pem.EncodeToMemory(certBlock)
	keyPEM := pem.EncodeToMemory(keyBlock)
	return parsePersistentCAPair(certPEM, keyPEM, path, path, now)
}

func (r *Recorder) installPersistentCA(material *persistentCAMaterial) {
	r.caCert = material.cert
	r.caKey = material.key
	r.caCertPEM = append([]byte(nil), material.certPEM...)
}

func readRecorderCAFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("recording proxy CA material is not a regular file")
	}
	if info.Size() > maxRecorderCAFileBytes {
		return nil, fmt.Errorf("recording proxy CA material exceeds %d bytes", maxRecorderCAFileBytes)
	}
	content, err := io.ReadAll(io.LimitReader(file, maxRecorderCAFileBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(content)) > maxRecorderCAFileBytes {
		return nil, fmt.Errorf("recording proxy CA material exceeds %d bytes", maxRecorderCAFileBytes)
	}
	return content, nil
}

func (r *Recorder) savePersistentCA(certDER []byte, key *rsa.PrivateKey) error {
	if strings.TrimSpace(r.caCertPath) == "" || strings.TrimSpace(r.caKeyPath) == "" {
		return nil
	}

	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return fmt.Errorf("parse recorder CA before persistence: %w", err)
	}
	material := &persistentCAMaterial{
		cert:    cert,
		key:     key,
		certPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER}),
		keyPEM:  pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}),
	}
	if err := validatePersistentCAPair(material.cert, material.key, time.Now()); err != nil {
		return fmt.Errorf("validate recorder CA before persistence: %w", err)
	}

	for _, path := range []string{r.caCertPath, r.caKeyPath} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return fmt.Errorf("create recorder CA directory: %w", err)
		}
	}
	// This is the transaction's commit point: the complete pair is one private
	// file, so a crash sees either the prior valid generation or this valid one.
	if err := writePersistentCABundle(persistentCABundlePath(r.caKeyPath), material); err != nil {
		return fmt.Errorf("persist atomic recorder CA bundle: %w", err)
	}
	if err := r.repairLegacyCAMirrors(material); err != nil {
		// The committed bundle remains authoritative and usable. Keep the legacy
		// path failure visible without turning a successfully committed CA into an
		// unavailable one; the next load retries the repair.
		slog.Warn("failed to update recorder CA compatibility files", "error", err)
	}
	return nil
}

func writePersistentCABundle(path string, material *persistentCAMaterial) error {
	if strings.TrimSpace(path) == "" || material == nil {
		return fmt.Errorf("atomic CA bundle path and material are required")
	}
	bundle := make([]byte, 0, len(material.certPEM)+len(material.keyPEM)+1)
	bundle = append(bundle, material.certPEM...)
	if len(bundle) > 0 && bundle[len(bundle)-1] != '\n' {
		bundle = append(bundle, '\n')
	}
	bundle = append(bundle, material.keyPEM...)
	return writeFileAtomic(path, bundle, 0o600)
}

func (r *Recorder) repairLegacyCAMirrors(material *persistentCAMaterial) error {
	if material == nil {
		return fmt.Errorf("recorder CA material is required")
	}
	if existing, err := loadPersistentCAPairFiles(r.caCertPath, r.caKeyPath, time.Now()); err == nil && bytes.Equal(existing.cert.Raw, material.cert.Raw) {
		return nil
	}
	if err := writeFileAtomic(r.caKeyPath, material.keyPEM, 0o600); err != nil {
		return fmt.Errorf("persist recorder CA key compatibility file: %w", err)
	}
	if err := writeFileAtomic(r.caCertPath, material.certPEM, 0o644); err != nil {
		return fmt.Errorf("persist recorder CA certificate compatibility file: %w", err)
	}
	return nil
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := file.Name()
	defer os.Remove(tmpPath)

	if err := file.Chmod(perm); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return replacePersistentCAFile(tmpPath, path)
}

// GetCACertPEM returns the PEM-encoded CA certificate for client installation.
// Returns nil if the CA has not been initialized yet.
func (r *Recorder) GetCACertPEM() []byte {
	return r.caCertPEM
}

// HandleCACert serves the CA certificate for download.
// Serves DER-encoded .crt by default (works with macOS/Windows double-click import).
// Add ?format=pem for PEM format (needed for Firefox and some Linux tools).
func (r *Recorder) HandleCACert(w http.ResponseWriter, req *http.Request) {
	if err := r.initCA(); err != nil {
		http.Error(w, "CA not available: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if req.URL.Query().Get("format") == "pem" {
		w.Header().Set("Content-Type", "application/x-pem-file")
		w.Header().Set("Content-Disposition", `attachment; filename="apexload-ca.pem"`)
		w.Write(r.caCertPEM)
		return
	}

	w.Header().Set("Content-Type", "application/x-x509-ca-cert")
	w.Header().Set("Content-Disposition", `attachment; filename="apexload-ca.crt"`)
	w.Write(r.caCert.Raw)
}

// generateLeafCert creates a TLS certificate for the given hostname, signed by the CA.
// Certificates are cached in r.certCache keyed by hostname.
func (r *Recorder) generateLeafCert(hostname string) (*tls.Certificate, error) {
	hostname = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(hostname)), ".")
	if hostname == "" || len(hostname) > maxRecorderCertificateHostBytes {
		return nil, fmt.Errorf("certificate hostname must contain between 1 and %d bytes", maxRecorderCertificateHostBytes)
	}
	if cached, ok := r.certCache.Load(hostname); ok {
		return cached, nil
	}
	r.certMu.Lock()
	defer r.certMu.Unlock()
	if r.certCache == nil {
		r.certCache = boundedcache.NewStringCache[*tls.Certificate](maxRecorderLeafCertificates, maxRecorderCertificateHostBytes, maxRecorderCertificateCacheKeyBytes)
	}
	if cached, ok := r.certCache.Load(hostname); ok {
		return cached, nil
	}

	if err := r.initCA(); err != nil {
		return nil, fmt.Errorf("initializing CA: %w", err)
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("generating leaf key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("generating leaf serial: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName: hostname,
		},
		NotBefore: time.Now().Add(-1 * time.Hour),
		NotAfter:  time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:  x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{
			x509.ExtKeyUsageServerAuth,
		},
	}

	// Set SAN: if hostname is an IP, add as IPAddress; otherwise add as DNSName.
	if ip := net.ParseIP(hostname); ip != nil {
		template.IPAddresses = []net.IP{ip}
	} else {
		template.DNSNames = []string{hostname}
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, r.caCert, &key.PublicKey, r.caKey)
	if err != nil {
		return nil, fmt.Errorf("creating leaf certificate: %w", err)
	}

	leafCert := &tls.Certificate{
		Certificate: [][]byte{certDER, r.caCert.Raw},
		PrivateKey:  key,
	}

	leafCert, _ = r.certCache.LoadOrStore(hostname, leafCert)
	slog.Debug("generated leaf certificate", "hostname", hostname)
	return leafCert, nil
}

// handleConnectMITM performs HTTPS CONNECT interception (man-in-the-middle).
// It hijacks the connection, performs a TLS handshake with an on-the-fly cert,
// reads HTTP requests from the client, forwards them upstream via HTTPS,
// and records them using the existing recordRequest method.
func (r *Recorder) handleConnectMITM(w http.ResponseWriter, req *http.Request) {
	host := req.URL.Host
	if !strings.Contains(host, ":") {
		host = host + ":443"
	}
	hostname := req.URL.Hostname()
	if err := validateProxyTargetHost(hostname); err != nil {
		http.Error(w, "proxy target rejected: "+err.Error(), http.StatusForbidden)
		return
	}

	slog.Debug("HTTPS CONNECT interception", "host", host, "hostname", hostname)

	// Respond 200 to the client to indicate the tunnel is established.
	w.WriteHeader(http.StatusOK)

	// Hijack the connection.
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		slog.Error("response writer does not support hijacking")
		return
	}
	clientConn, clientRW, err := hijacker.Hijack()
	if err != nil {
		slog.Error("failed to hijack connection", "err", err)
		return
	}
	defer clientConn.Close()
	tunnel := &recorderTunnel{clientConn: clientConn}
	r.mu.RLock()
	if r.active != nil {
		tunnel.sessionID = r.active.ID
	}
	r.mu.RUnlock()
	unregisterTunnel := r.registerTunnel(tunnel)
	defer unregisterTunnel()

	clientReader := clientRW.Reader
	if clientReader == nil {
		clientReader = bufio.NewReader(clientConn)
	}
	firstByte, err := peekConnectTunnelByte(clientConn, clientReader)
	if err != nil {
		if err != io.EOF {
			slog.Debug("failed to inspect CONNECT tunnel", "host", host, "err", err)
		}
		return
	}
	if firstByte != 0x16 {
		r.forwardPlainConnect(clientConn, clientReader, hostname, host)
		return
	}

	leafCert, err := r.generateLeafCert(hostname)
	if err != nil {
		slog.Error("failed to generate leaf cert", "hostname", hostname, "err", err)
		writeHTTPError(clientConn, http.StatusBadGateway, "TLS interception error: "+err.Error())
		return
	}

	// Wrap the raw connection with TLS using the leaf cert.
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{*leafCert},
		MinVersion:   tls.VersionTLS12,
	}
	tlsConn := tls.Server(&readBufferedConn{Conn: clientConn, reader: clientReader}, tlsConfig)
	if err := tlsConn.Handshake(); err != nil {
		slog.Debug("TLS handshake with client failed", "hostname", hostname, "err", err)
		return
	}
	defer tlsConn.Close()

	// Create an HTTP client for upstream requests.
	transport := &http.Transport{
		DialContext:            proxySafeDialer(30 * time.Second).DialContext,
		TLSClientConfig:        recorderUpstreamTLSConfig(hostname),
		TLSHandshakeTimeout:    30 * time.Second,
		ResponseHeaderTimeout:  30 * time.Second,
		IdleConnTimeout:        90 * time.Second,
		MaxResponseHeaderBytes: maxRecorderHeaderBytes,
	}
	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// Read HTTP requests from the TLS connection and forward them. The hijacked
	// connection is detached from the http.Server's Read/Write timeouts, so set an
	// idle deadline before each read; an idle or slow-loris client would otherwise
	// hold the goroutine + fd open forever.
	reader := bufio.NewReader(tlsConn)
	for {
		_ = tlsConn.SetReadDeadline(time.Now().Add(connectTunnelIdleTimeout))
		clientReq, nextReader, err := readRecorderHTTPRequest(reader)
		if err != nil {
			if err != io.EOF {
				slog.Debug("error reading request from TLS connection", "hostname", hostname, "err", err)
			}
			return
		}
		reader = nextReader
		// Clear the idle deadline while the request is actively forwarded (it may
		// be a long upload/download); the next iteration re-arms it.
		_ = tlsConn.SetReadDeadline(time.Time{})

		r.forwardHTTPS(tlsConn, reader, clientReq, hostname, host, client)
	}
}

// connectTunnelIdleTimeout bounds how long a hijacked CONNECT/MITM tunnel may sit
// idle between requests before it is closed, reclaiming the goroutine + fd.
const (
	connectTunnelIdleTimeout              = 120 * time.Second
	recorderUpstreamBodyInactivityTimeout = 30 * time.Second
)

// inactivityTimeoutBody bounds a single stalled body read without imposing a
// total lifetime on responses that continue to make progress. It is applied
// only after streaming responses have been identified and routed away to the
// intentionally long-lived streaming path.
type inactivityTimeoutBody struct {
	body    io.ReadCloser
	timeout time.Duration
	cancel  context.CancelFunc
}

func (b *inactivityTimeoutBody) Read(p []byte) (int, error) {
	if b == nil || b.body == nil {
		return 0, io.EOF
	}
	if b.timeout <= 0 {
		return b.body.Read(p)
	}
	timeoutFinished := make(chan struct{})
	timer := time.AfterFunc(b.timeout, func() {
		// Go's HTTP/1 response body serializes Read and Close with the same
		// mutex, so Close alone cannot reliably interrupt a blocked Read. Cancel
		// the owning request; the transport then closes the connection and
		// releases the read. The handler retains sole ownership of the eventual
		// Body.Close call so the timer callback cannot race its deferred cleanup.
		if b.cancel != nil {
			b.cancel()
		} else {
			// A cancel function is always supplied by recorder request paths. Keep
			// the wrapper useful for standalone readers by falling back to Close
			// when no owning request exists.
			_ = b.body.Close()
		}
		close(timeoutFinished)
	})
	n, err := b.body.Read(p)
	if !timer.Stop() {
		// Wait for the timeout action to finish before returning ownership to
		// the caller. In particular, this prevents a fallback Close from running
		// concurrently with the caller's deferred Close.
		<-timeoutFinished
		return n, fmt.Errorf("upstream response body made no progress for %s: %w", b.timeout, context.DeadlineExceeded)
	}
	return n, err
}

func (b *inactivityTimeoutBody) Close() error {
	if b == nil || b.body == nil {
		return nil
	}
	return b.body.Close()
}

func (r *Recorder) responseBodyInactivityTimeout() time.Duration {
	if r != nil && r.upstreamBodyInactivityTimeout > 0 {
		return r.upstreamBodyInactivityTimeout
	}
	return recorderUpstreamBodyInactivityTimeout
}

func peekConnectTunnelByte(conn net.Conn, reader *bufio.Reader) (byte, error) {
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	defer conn.SetReadDeadline(time.Time{})
	peeked, err := reader.Peek(1)
	if err != nil {
		return 0, err
	}
	return peeked[0], nil
}

func (r *Recorder) forwardPlainConnect(clientConn net.Conn, clientReader *bufio.Reader, hostname, host string) {
	for {
		_ = clientConn.SetReadDeadline(time.Now().Add(connectTunnelIdleTimeout))
		clientReq, nextReader, err := readRecorderHTTPRequest(clientReader)
		if err != nil {
			if err != io.EOF {
				slog.Debug("error reading plaintext CONNECT request", "hostname", hostname, "err", err)
			}
			return
		}
		clientReader = nextReader
		_ = clientConn.SetReadDeadline(time.Time{})

		r.mu.RLock()
		session := r.active
		r.mu.RUnlock()
		start := time.Now()

		if isWebSocketUpgrade(clientReq) {
			r.forwardWebSocketPlainConnect(clientConn, clientReader, clientReq, hostname, host, session, start)
			return
		}

		writeHTTPError(clientConn, http.StatusBadGateway, "unsupported plaintext CONNECT request")
		return
	}
}

type readBufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *readBufferedConn) Read(p []byte) (int, error) {
	return c.reader.Read(p)
}

// forwardHTTPS forwards a single HTTP request read from the intercepted TLS connection
// to the real upstream server and writes the response back to the client.
// stripDefaultHTTPSPort removes an explicit :443 from a host:port so recorded
// HTTPS URLs use the canonical default-port-less form (example.com, not
// example.com:443). Non-default ports and bare hosts are returned unchanged.
func stripDefaultHTTPSPort(host string) string {
	h, port, err := net.SplitHostPort(host)
	if err != nil {
		return host
	}
	if port != "443" {
		return host
	}
	if strings.Contains(h, ":") {
		// IPv6 literal: keep it bracketed for use as a URL host.
		return "[" + h + "]"
	}
	return h
}

func (r *Recorder) forwardHTTPS(tlsConn net.Conn, clientReader *bufio.Reader, clientReq *http.Request, hostname, host string, client *http.Client) {
	r.mu.RLock()
	session := r.active
	r.mu.RUnlock()

	start := time.Now()

	if isWebSocketUpgrade(clientReq) {
		r.forwardWebSocketHTTPS(tlsConn, clientReader, clientReq, hostname, host, session, start)
		return
	}

	reqBodyCapture := newBodyCapture(requestCaptureLimit(session))
	upstreamBody := requestBodyReader(clientReq.Body, reqBodyCapture)

	// Build the upstream URL using the full host:port from the CONNECT request.
	targetURL := "https://" + host + clientReq.URL.RequestURI()

	upstreamCtx, cancelUpstream := context.WithCancel(clientReq.Context())
	defer cancelUpstream()
	upstreamReq, err := http.NewRequestWithContext(upstreamCtx, clientReq.Method, targetURL, upstreamBody)
	if err != nil {
		slog.Debug("failed to create upstream request", "err", recorderLogError(err))
		writeHTTPError(tlsConn, http.StatusBadGateway, "proxy error: "+err.Error())
		return
	}
	upstreamReq.ContentLength = clientReq.ContentLength

	// Copy headers, stripping proxy-specific ones.
	for k, vv := range clientReq.Header {
		for _, v := range vv {
			upstreamReq.Header.Add(k, v)
		}
	}
	stripProxyRequestHeaders(upstreamReq.Header, false)

	resp, err := client.Do(upstreamReq)
	if err != nil {
		slog.Debug("upstream request failed", "url", recorderLogURL(targetURL), "err", recorderLogError(err))
		writeHTTPError(tlsConn, http.StatusBadGateway, "upstream error: "+err.Error())
		return
	}
	defer resp.Body.Close()

	// Set the request URL to the full HTTPS URL for recording before any
	// streaming response can emit its early recording event. Strip the implicit
	// :443 (added to `host` for the upstream dial) so recorded URLs and the
	// derived BaseURL use the canonical default-port-less form.
	recordHost := stripDefaultHTTPSPort(host)
	clientReq.URL.Scheme = "https"
	clientReq.URL.Host = recordHost
	clientReq.Host = recordHost

	if isStreamingResponse(resp) {
		writeHTTPSResponseHeaders(tlsConn, resp, -1, true)
		chunked := &chunkedHTTPWriter{w: tlsConn}
		r.streamAndRecordResponse(chunked, nil, session, clientReq, reqBodyCapture.bytes(), resp, start, recordOptions{
			requestBodySizeBytes:     boundedInt(reqBodyCapture.size()),
			requestBodySizeKnown:     true,
			forceRequestBodyTruncate: reqBodyCapture.truncated(),
		})
		chunked.Close()
		return
	}

	var responseWriter io.Writer = tlsConn
	closeResponseWriter := func() {}
	if resp.ContentLength >= 0 {
		writeHTTPSResponseHeaders(tlsConn, resp, boundedInt(resp.ContentLength), false)
	} else {
		writeHTTPSResponseHeaders(tlsConn, resp, -1, true)
		chunked := &chunkedHTTPWriter{w: tlsConn}
		responseWriter = chunked
		closeResponseWriter = chunked.Close
	}
	boundedBody := &inactivityTimeoutBody{
		body:    resp.Body,
		timeout: r.responseBodyInactivityTimeout(),
		cancel:  cancelUpstream,
	}
	respBody, respBodySize, respBodyTruncated, copyErr := copyAndCaptureBody(responseWriter, boundedBody, responseCaptureLimit(session))
	closeResponseWriter()
	// When the response was framed with a fixed Content-Length but the upstream
	// body came up short (connection reset mid-body, or the server lied about the
	// length), fewer bytes than declared were written. Close the TLS connection
	// so the recorded browser stops waiting for the missing bytes instead of
	// stalling on a half-delivered response until its own timeout.
	if copyErr != nil && resp.ContentLength >= 0 {
		_ = tlsConn.Close()
	}
	elapsed := time.Since(start)

	// Record the request if there is an active session.
	if session != nil {
		r.recordRequestWithOptions(session, clientReq, reqBodyCapture.bytes(), resp, respBody, elapsed, recordOptions{
			requestStartedAt:          start,
			requestBodySizeBytes:      boundedInt(reqBodyCapture.size()),
			requestBodySizeKnown:      true,
			forceRequestBodyTruncate:  reqBodyCapture.truncated(),
			forceResponseBodyTruncate: respBodyTruncated,
			responseBodySizeBytes:     boundedInt(respBodySize),
			responseBodySizeKnown:     true,
		})
	}
}

func writeHTTPSResponseHeaders(conn net.Conn, resp *http.Response, bodyLen int, chunked bool) {
	var respBuf strings.Builder
	fmt.Fprintf(&respBuf, "HTTP/%d.%d %d %s\r\n", resp.ProtoMajor, resp.ProtoMinor, resp.StatusCode, http.StatusText(resp.StatusCode))
	for k, vv := range resp.Header {
		if chunked && (strings.EqualFold(k, "Content-Length") || strings.EqualFold(k, "Transfer-Encoding")) {
			continue
		}
		for _, v := range vv {
			fmt.Fprintf(&respBuf, "%s: %s\r\n", k, v)
		}
	}
	if chunked {
		respBuf.WriteString("Transfer-Encoding: chunked\r\n")
	} else if resp.Header.Get("Content-Length") == "" && resp.Header.Get("Transfer-Encoding") == "" {
		fmt.Fprintf(&respBuf, "Content-Length: %d\r\n", bodyLen)
	}
	respBuf.WriteString("\r\n")
	conn.Write([]byte(respBuf.String()))
}

type chunkedHTTPWriter struct {
	w io.Writer
}

func (w *chunkedHTTPWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if _, err := fmt.Fprintf(w.w, "%x\r\n", len(p)); err != nil {
		return 0, err
	}
	if _, err := w.w.Write(p); err != nil {
		return 0, err
	}
	if _, err := io.WriteString(w.w, "\r\n"); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (w *chunkedHTTPWriter) Close() {
	_, _ = io.WriteString(w.w, "0\r\n\r\n")
}

// writeHTTPError writes a minimal HTTP error response to a raw connection.
func writeHTTPError(conn net.Conn, statusCode int, message string) {
	fmt.Fprintf(conn, "HTTP/1.1 %d %s\r\nContent-Type: text/plain\r\nContent-Length: %d\r\n\r\n%s",
		statusCode, http.StatusText(statusCode), len(message), message)
}

// certCacheLenForTesting exposes only the bounded cache size, keeping the
// synchronization and eviction implementation private.
func (r *Recorder) certCacheLenForTesting() int {
	if r == nil || r.certCache == nil {
		return 0
	}
	return r.certCache.Len()
}
