package recorder

import (
	"bufio"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
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
			CommonName:   "Stormrunner Recording Proxy CA",
			Organization: []string{"Stormrunner"},
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

	slog.Info("generated recording proxy CA certificate")
	return nil
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
		w.Header().Set("Content-Disposition", `attachment; filename="stormrunner-ca.pem"`)
		w.Write(r.caCertPEM)
		return
	}

	w.Header().Set("Content-Type", "application/x-x509-ca-cert")
	w.Header().Set("Content-Disposition", `attachment; filename="stormrunner-ca.crt"`)
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

	leafCert, err := r.generateLeafCert(hostname)
	if err != nil {
		slog.Error("failed to generate leaf cert", "hostname", hostname, "err", err)
		http.Error(w, "TLS interception error: "+err.Error(), http.StatusBadGateway)
		return
	}

	// Respond 200 to the client to indicate the tunnel is established.
	w.WriteHeader(http.StatusOK)

	// Hijack the connection.
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		slog.Error("response writer does not support hijacking")
		return
	}
	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		slog.Error("failed to hijack connection", "err", err)
		return
	}
	defer clientConn.Close()

	// Wrap the raw connection with TLS using the leaf cert.
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{*leafCert},
	}
	tlsConn := tls.Server(clientConn, tlsConfig)
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
		Timeout:   30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// Read HTTP requests from the TLS connection and forward them.
	reader := bufio.NewReader(tlsConn)
	for {
		clientReq, err := http.ReadRequest(reader)
		if err != nil {
			if err != io.EOF {
				slog.Debug("error reading request from TLS connection", "hostname", hostname, "err", err)
			}
			return
		}

		r.forwardHTTPS(tlsConn, clientReq, hostname, host, client)
	}
}

// forwardHTTPS forwards a single HTTP request read from the intercepted TLS connection
// to the real upstream server and writes the response back to the client.
func (r *Recorder) forwardHTTPS(tlsConn net.Conn, clientReq *http.Request, hostname, host string, client *http.Client) {
	r.mu.RLock()
	session := r.active
	r.mu.RUnlock()

	start := time.Now()

	// Read the request body.
	var bodyBytes []byte
	if clientReq.Body != nil {
		bodyBytes, _ = io.ReadAll(clientReq.Body)
		clientReq.Body.Close()
	}

	// Build the upstream URL using the full host:port from the CONNECT request.
	targetURL := "https://" + host + clientReq.URL.RequestURI()

	upstreamReq, err := http.NewRequest(clientReq.Method, targetURL, strings.NewReader(string(bodyBytes)))
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

	respBody, _ := io.ReadAll(resp.Body)
	elapsed := time.Since(start)

	// Write the response back to the client over the TLS connection.
	var respBuf strings.Builder
	fmt.Fprintf(&respBuf, "HTTP/%d.%d %d %s\r\n", resp.ProtoMajor, resp.ProtoMinor, resp.StatusCode, http.StatusText(resp.StatusCode))
	for k, vv := range resp.Header {
		for _, v := range vv {
			fmt.Fprintf(&respBuf, "%s: %s\r\n", k, v)
		}
	}
	// Ensure Content-Length is set for the response body.
	if resp.Header.Get("Content-Length") == "" && resp.Header.Get("Transfer-Encoding") == "" {
		fmt.Fprintf(&respBuf, "Content-Length: %d\r\n", len(respBody))
	}
	respBuf.WriteString("\r\n")
	tlsConn.Write([]byte(respBuf.String()))
	tlsConn.Write(respBody)

	// Set the request URL to the full HTTPS URL for recording.
	clientReq.URL.Scheme = "https"
	clientReq.URL.Host = hostname
	clientReq.Host = hostname

	// Record the request if there is an active session.
	if session != nil {
		r.recordRequest(session, clientReq, bodyBytes, resp, respBody, elapsed)
	}
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
