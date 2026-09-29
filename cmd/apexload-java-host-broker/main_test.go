package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/javarunner/broker"
)

func testProfile() profileConfig {
	return profileConfig{ConfigurationSHA256: strings.Repeat("a", 64), Image: "registry.example/java@sha256:" + strings.Repeat("b", 64), ServiceAccount: "java-execution", MemoryBytes: 128 << 20, CPUMilli: 500, ScratchBytes: 1 << 20, HostLeaseMS: 1000, PreparationMS: 10000, InvocationMS: 10000, TransportLifetimeMS: 3600000}
}
func testConfig(t *testing.T) brokerConfig {
	t.Helper()
	return brokerConfig{Contract: broker.BrokerContract, Listen: "127.0.0.1:0", StatePath: filepath.Join(t.TempDir(), "broker.db"), Namespace: "java-test", MaxHosts: 25, MaxVUsers: 25, MaxMemoryBytes: 4 << 30, OwnerLeaseMS: 5000, ControlClientURI: "spiffe://apexload/java-broker", Profiles: map[string]profileConfig{"approved": testProfile()}, Principals: []principalConfig{{URI: "spiffe://apexload/java-facade/generator", OwnerID: "generator", Profiles: []string{"approved"}}}}
}
func saveConfig(t *testing.T, cfg brokerConfig) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "broker.json")
	data, e := json.Marshal(cfg)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, data, 0600); e != nil {
		t.Fatal(e)
	}
	return path
}

func TestBrokerConfigRequiresExplicitBoundedOperatorProfiles(t *testing.T) {
	cfg := testConfig(t)
	path := saveConfig(t, cfg)
	got, e := loadConfig(path)
	if e != nil || got.storeConfig().Budget.MaxHosts != 25 || got.Profiles["approved"].managed(got.ControlClientURI).LeaseTTL != time.Second {
		t.Fatal(e)
	}
	for _, mutate := range []func(*brokerConfig){
		func(c *brokerConfig) { c.OwnerLeaseMS = 0 }, func(c *brokerConfig) { c.StatePath = "relative.db" }, func(c *brokerConfig) { c.Contract = "unknown" },
		func(c *brokerConfig) { p := c.Profiles["approved"]; p.HostLeaseMS = 5000; c.Profiles["approved"] = p },
		func(c *brokerConfig) {
			p := c.Profiles["approved"]
			p.TransportLifetimeMS = 86400001
			c.Profiles["approved"] = p
		},
		func(c *brokerConfig) { p := c.Profiles["approved"]; p.CPUMilli = 0; c.Profiles["approved"] = p },
		func(c *brokerConfig) {
			p := c.Profiles["approved"]
			p.Image = "image:latest"
			c.Profiles["approved"] = p
		},
	} {
		c := testConfig(t)
		mutate(&c)
		if _, e := loadConfig(saveConfig(t, c)); e == nil {
			t.Fatal("invalid configuration accepted")
		}
	}
	data, _ := os.ReadFile(path)
	data = append(data, []byte(`{}`)...)
	if e = os.WriteFile(path, data, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = loadConfig(path); e == nil {
		t.Fatal("trailing JSON accepted")
	}
	if e = os.WriteFile(path, []byte(`{"contract":"apexload.java-broker/v1","unknown":true}`), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = loadConfig(path); e == nil {
		t.Fatal("unknown fields accepted")
	}
}

func commandCertificates(t *testing.T) (tlsFiles, string) {
	t.Helper()
	dir := t.TempDir()
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(25 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, e := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	caPath := filepath.Join(dir, "ca.crt")
	_ = os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600)
	keyDER, _ := x509.MarshalPKCS8PrivateKey(key)
	signingKey := filepath.Join(dir, "ca.key")
	_ = os.WriteFile(signingKey, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0600)
	leafKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	uri, _ := url.Parse("spiffe://apexload/java-broker")
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), NotBefore: ca.NotBefore, NotAfter: ca.NotAfter, URIs: []*url.URL{uri}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth}}
	leafDER, e := x509.CreateCertificate(rand.Reader, leaf, ca, &leafKey.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	private, _ := x509.MarshalPKCS8PrivateKey(leafKey)
	files := tlsFiles{Certificate: filepath.Join(dir, "client.crt"), Key: filepath.Join(dir, "client.key"), CA: caPath}
	_ = os.WriteFile(files.Certificate, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}), 0600)
	_ = os.WriteFile(files.Key, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}), 0600)
	return files, signingKey
}

func TestBrokerCommandStartsAndPreservesExplicitDurableState(t *testing.T) {
	cfg := testConfig(t)
	files, signer := commandCertificates(t)
	cfg.ServerTLS = files
	cfg.ControlTLS = files
	cfg.HostIssuer = tlsFiles{Certificate: files.CA, Key: signer, CA: files.CA}
	cfg.HostTrustDomain = "apexload"
	cfg.HostDNSDomain = "hosts.internal"
	cfg.KubernetesCA = files.CA
	cfg.KubernetesEndpoint = "https://127.0.0.1:1"
	cfg.KubernetesToken = filepath.Join(t.TempDir(), "token")
	_ = os.WriteFile(cfg.KubernetesToken, []byte("test-only-token\n"), 0600)
	cfg.AttestorCA = files.CA
	cfg.AttestorURI = "spiffe://apexload/java-boundary-attestor/qualified"
	cfg.AttestorEndpoint = "https://127.0.0.1:1"
	cfg.AttestorQualificationSHA256 = strings.Repeat("c", 64)
	path := saveConfig(t, cfg)
	if e := run(t.Context(), []string{"--config", path}); e == nil {
		t.Fatal("silently created missing authority")
	}
	for _, initialize := range []bool{true, false} {
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		listening := make(chan struct{})
		go func() { done <- runConfigured(ctx, cfg, initialize, func(net.Addr) { close(listening) }) }()
		// Startup has no execution requests, so fake unavailable Kubernetes and
		// attestor endpoints cannot create code/traffic or supply cleanup proof.
		select {
		case <-listening:
		case e := <-done:
			cancel()
			t.Fatal("broker stopped before listening", e)
		case <-time.After(2 * time.Second):
			cancel()
			t.Fatal("broker did not listen")
		}
		cancel()
		select {
		case e := <-done:
			if e != nil {
				t.Fatal(e)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("broker shutdown stuck")
		}
	}
	if e := run(t.Context(), []string{"--config", path, "--initialize-store"}); e == nil {
		t.Fatal("overwrote existing authority")
	}
}

func TestBrokerCommandBoundedFilesTLSAndCancellation(t *testing.T) {
	files, _ := commandCertificates(t)
	if _, e := clientTLS(files); e != nil {
		t.Fatal(e)
	}
	if _, e := readBounded(files.Certificate, 1); e == nil {
		t.Fatal("oversized file")
	}
	if _, e := readBounded(t.TempDir(), 1024); e == nil {
		t.Fatal("directory opened")
	}
	if _, e := readBounded("relative", 1024); e == nil {
		t.Fatal("relative credential")
	}
	files.CA = files.Key
	if _, e := clientTLS(files); e == nil {
		t.Fatal("private key treated as trust root")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if waitTick(ctx, time.Hour) {
		t.Fatal("ignored cancellation")
	}
	if e := run(ctx, []string{"--unknown"}); e == nil {
		t.Fatal("unknown flag accepted")
	}
}

func TestBrokerInitializeOnlyCreatesOnceWithoutCredentialsOrNetwork(t *testing.T) {
	cfg := testConfig(t)
	path := saveConfig(t, cfg)
	if e := run(t.Context(), []string{"--config", path, "--initialize-store-only"}); e != nil {
		t.Fatal(e)
	}
	if e := run(t.Context(), []string{"--config", path, "--initialize-store-only"}); e == nil {
		t.Fatal("authority overwritten")
	}
	store, e := broker.Open(t.Context(), cfg.StatePath, cfg.storeConfig(), false)
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	if _, e = store.StartEpoch(t.Context()); e != nil {
		t.Fatal(e)
	}
	if e := run(t.Context(), []string{"--config", path, "--initialize-store-only", "--initialize-store"}); e == nil {
		t.Fatal("ambiguous startup flags")
	}
}
