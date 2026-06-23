package recorder

import (
	"bufio"
	"bytes"
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
	"sync"
	"time"
)

// initCA generates a self-signed root CA certificate and key pair.
// It is called lazily on the first CONNECT request or explicit initialization.
func (r *Recorder) initCA() error {
	r.caOnce.Do(func() {
		r.caErr = r.generateCA()
	})
	return r.caErr
}

func (r *Recorder) generateCA() error {
	if err := r.loadPersistentCA(); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
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

	r.caCert = caCert
	r.caKey = key
	r.caCertPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	if err := r.savePersistentCA(certDER, key); err != nil {
		return err
	}

	slog.Info("generated recording proxy CA certificate")
	return nil
}

func (r *Recorder) loadPersistentCA() error {
	if strings.TrimSpace(r.caCertPath) == "" || strings.TrimSpace(r.caKeyPath) == "" {
		return os.ErrNotExist
	}

	certPEM, err := os.ReadFile(r.caCertPath)
	if err != nil {
		return err
	}
	keyPEM, err := os.ReadFile(r.caKeyPath)
	if err != nil {
		return err
	}

	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil || certBlock.Type != "CERTIFICATE" {
		return fmt.Errorf("parse persisted CA certificate %q: invalid PEM", r.caCertPath)
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return fmt.Errorf("parse persisted CA certificate %q: %w", r.caCertPath, err)
	}
	if !cert.IsCA {
		return fmt.Errorf("persisted CA certificate %q is not a certificate authority", r.caCertPath)
	}

	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return fmt.Errorf("parse persisted CA key %q: invalid PEM", r.caKeyPath)
	}

	var key *rsa.PrivateKey
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
				return fmt.Errorf("persisted CA key %q is not an RSA private key", r.caKeyPath)
			}
		}
	default:
		return fmt.Errorf("parse persisted CA key %q: unsupported PEM type %q", r.caKeyPath, keyBlock.Type)
	}
	if err != nil {
		return fmt.Errorf("parse persisted CA key %q: %w", r.caKeyPath, err)
	}

	r.caCert = cert
	r.caKey = key
	r.caCertPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
	slog.Info("loaded recording proxy CA certificate", "path", r.caCertPath)
	return nil
}

func (r *Recorder) savePersistentCA(certDER []byte, key *rsa.PrivateKey) error {
	if strings.TrimSpace(r.caCertPath) == "" || strings.TrimSpace(r.caKeyPath) == "" {
		return nil
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})

	for _, path := range []string{r.caCertPath, r.caKeyPath} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("create recorder CA directory: %w", err)
		}
	}
	if err := writeFileAtomic(r.caKeyPath, keyPEM, 0o600); err != nil {
		return fmt.Errorf("persist recorder CA key: %w", err)
	}
	if err := writeFileAtomic(r.caCertPath, certPEM, 0o644); err != nil {
		return fmt.Errorf("persist recorder CA certificate: %w", err)
	}
	return nil
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
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
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
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
	if cached, ok := r.certCache.Load(hostname); ok {
		return cached.(*tls.Certificate), nil
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

	r.certCache.Store(hostname, leafCert)
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
	}
	tlsConn := tls.Server(&readBufferedConn{Conn: clientConn, reader: clientReader}, tlsConfig)
	if err := tlsConn.Handshake(); err != nil {
		slog.Debug("TLS handshake with client failed", "hostname", hostname, "err", err)
		return
	}
	defer tlsConn.Close()

	// Create an HTTP client for upstream requests.
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
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
		clientReq, err := http.ReadRequest(reader)
		if err != nil {
			if err != io.EOF {
				slog.Debug("error reading request from TLS connection", "hostname", hostname, "err", err)
			}
			return
		}
		// Clear the idle deadline while the request is actively forwarded (it may
		// be a long upload/download); the next iteration re-arms it.
		_ = tlsConn.SetReadDeadline(time.Time{})

		r.forwardHTTPS(tlsConn, reader, clientReq, hostname, host, client)
	}
}

// connectTunnelIdleTimeout bounds how long a hijacked CONNECT/MITM tunnel may sit
// idle between requests before it is closed, reclaiming the goroutine + fd.
const connectTunnelIdleTimeout = 120 * time.Second

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
		clientReq, err := http.ReadRequest(clientReader)
		if err != nil {
			if err != io.EOF {
				slog.Debug("error reading plaintext CONNECT request", "hostname", hostname, "err", err)
			}
			return
		}
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

	// Read the request body.
	var bodyBytes []byte
	if clientReq.Body != nil {
		bodyBytes, _ = io.ReadAll(clientReq.Body)
		clientReq.Body.Close()
	}

	// Build the upstream URL using the full host:port from the CONNECT request.
	targetURL := "https://" + host + clientReq.URL.RequestURI()

	upstreamReq, err := http.NewRequest(clientReq.Method, targetURL, bytes.NewReader(bodyBytes))
	if err != nil {
		slog.Debug("failed to create upstream request", "err", err)
		writeHTTPError(tlsConn, http.StatusBadGateway, "proxy error: "+err.Error())
		return
	}

	// Copy headers, stripping proxy-specific ones.
	for k, vv := range clientReq.Header {
		for _, v := range vv {
			upstreamReq.Header.Add(k, v)
		}
	}
	upstreamReq.Header.Del("Proxy-Connection")

	resp, err := client.Do(upstreamReq)
	if err != nil {
		slog.Debug("upstream request failed", "url", targetURL, "err", err)
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
		r.streamAndRecordResponse(chunked, nil, session, clientReq, bodyBytes, resp, start)
		chunked.Close()
		return
	}

	respBody, _ := io.ReadAll(resp.Body)
	elapsed := time.Since(start)

	// Write the response back to the client over the TLS connection.
	writeHTTPSResponseHeaders(tlsConn, resp, len(respBody), false)
	tlsConn.Write(respBody)

	// Record the request if there is an active session.
	if session != nil {
		r.recordRequestStarted(session, clientReq, bodyBytes, resp, respBody, elapsed, start)
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

// certCacheForTesting is a helper to expose the cert cache in tests.
// It returns the underlying sync.Map.
func (r *Recorder) certCacheForTesting() *sync.Map {
	return &r.certCache
}
