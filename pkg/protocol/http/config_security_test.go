package http

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/fileasset"
)

func TestBuildTLSConfigReadsOnlyGovernedCredentialFiles(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := writeHTTPTestCertificate(t, dir)
	if err := fileasset.RegisterRoot(dir); err != nil {
		t.Fatal(err)
	}
	tlsConfig, err := buildTLSConfig(map[string]any{
		"tls_client_cert_file": certPath,
		"tls_client_key_file":  keyPath,
		"tls_ca_cert_file":     certPath,
	})
	if err != nil {
		t.Fatalf("buildTLSConfig: %v", err)
	}
	if len(tlsConfig.Certificates) != 1 || tlsConfig.RootCAs == nil {
		t.Fatalf("TLS material = certificates:%d roots:%v", len(tlsConfig.Certificates), tlsConfig.RootCAs)
	}

	outside := filepath.Join(t.TempDir(), "outside.pem")
	if err := os.WriteFile(outside, []byte("not governed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := buildTLSConfig(map[string]any{"tls_ca_cert_file": outside}); err == nil || !strings.Contains(err.Error(), "outside allowed roots") {
		t.Fatalf("outside-root TLS error = %v", err)
	}
}

func TestBuildTLSConfigRejectsIncompleteAndOversizedCredentials(t *testing.T) {
	if _, err := buildTLSConfig(map[string]any{"tls_client_cert_file": "/missing/cert.pem"}); err == nil || !strings.Contains(err.Error(), "configured together") {
		t.Fatalf("incomplete TLS pair error = %v", err)
	}
	dir := t.TempDir()
	if err := fileasset.RegisterRoot(dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "oversized.pem")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", int(fileasset.MaxCredentialBytes)+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := buildTLSConfig(map[string]any{"tls_ca_cert_file": path}); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized TLS credential error = %v", err)
	}
}

func TestBuildTLSConfigBoundsPerVUserSessionCache(t *testing.T) {
	tlsConfig, err := buildTLSConfig(nil)
	if err != nil {
		t.Fatalf("buildTLSConfig: %v", err)
	}
	for index := 0; index <= maxTLSClientSessionsPerVUser; index++ {
		tlsConfig.ClientSessionCache.Put(fmt.Sprintf("host-%d", index), &tls.ClientSessionState{})
	}
	if _, ok := tlsConfig.ClientSessionCache.Get("host-0"); ok {
		t.Fatalf("oldest session remained after exceeding %d-entry cache", maxTLSClientSessionsPerVUser)
	}
	if _, ok := tlsConfig.ClientSessionCache.Get(fmt.Sprintf("host-%d", maxTLSClientSessionsPerVUser)); !ok {
		t.Fatal("newest TLS session was not retained")
	}
}

func writeHTTPTestCertificate(t *testing.T, dir string) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "apexload-test"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPath := filepath.Join(dir, "client.pem")
	keyPath := filepath.Join(dir, "client-key.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath
}
