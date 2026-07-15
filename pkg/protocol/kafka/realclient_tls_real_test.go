//go:build kafka_real

package kafka

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/fileasset"
)

func TestKafkaTLSMaterialIsBoundedAndGoverned(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := writeKafkaTestCertificate(t, dir)
	if err := fileasset.RegisterRoot(dir); err != nil {
		t.Fatal(err)
	}
	client := &franzClient{cfg: config{TLSClientCertFile: certPath, TLSClientKeyFile: keyPath, TLSCACertFile: certPath}}
	tlsConfig, err := client.buildTLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	if tlsConfig.MinVersion != tls.VersionTLS12 || len(tlsConfig.Certificates) != 1 || tlsConfig.RootCAs == nil {
		t.Fatalf("TLS config = min:%d certificates:%d roots:%v", tlsConfig.MinVersion, len(tlsConfig.Certificates), tlsConfig.RootCAs)
	}

	outside := filepath.Join(t.TempDir(), "outside.pem")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	client.cfg = config{TLSCACertFile: outside}
	if _, err := client.buildTLSConfig(); err == nil || !strings.Contains(err.Error(), "outside allowed roots") {
		t.Fatalf("outside-root error = %v", err)
	}
}

func TestKafkaTLSRejectsIncompleteAndOversizedMaterial(t *testing.T) {
	client := &franzClient{cfg: config{TLSClientCertFile: "/missing/cert.pem"}}
	if _, err := client.buildTLSConfig(); err == nil || !strings.Contains(err.Error(), "configured together") {
		t.Fatalf("incomplete pair error = %v", err)
	}
	dir := t.TempDir()
	if err := fileasset.RegisterRoot(dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "oversized.pem")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", int(fileasset.MaxCredentialBytes)+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	client.cfg = config{TLSCACertFile: path}
	if _, err := client.buildTLSConfig(); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized credential error = %v", err)
	}
}

func writeKafkaTestCertificate(t *testing.T, dir string) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "apexload-kafka-test"},
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
