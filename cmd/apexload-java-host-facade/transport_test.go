package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/javarunner/broker"
	"github.com/jasonczech/apexload/pkg/javasidecar"
)

func TestFacadeCommandMTLSStartupAndShutdown(t *testing.T) {
	dir := t.TempDir()
	caKey, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, e := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if e != nil {
		t.Fatal(e)
	}
	ca, e = x509.ParseCertificate(der)
	if e != nil {
		t.Fatal(e)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	write := func(name string, b []byte) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if e := os.WriteFile(path, b, 0600); e != nil {
			t.Fatal(e)
		}
		return path
	}
	caPath := write("ca.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	cert := func(name, uri string) (tls.Certificate, string, string) {
		t.Helper()
		key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if e != nil {
			t.Fatal(e)
		}
		u, _ := url.Parse(uri)
		leaf := &x509.Certificate{SerialNumber: big.NewInt(int64(len(name) + 2)), NotBefore: ca.NotBefore, NotAfter: ca.NotAfter, URIs: []*url.URL{u}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth}}
		der, e := x509.CreateCertificate(rand.Reader, leaf, ca, &key.PublicKey, caKey)
		if e != nil {
			t.Fatal(e)
		}
		keyDER, e := x509.MarshalPKCS8PrivateKey(key)
		if e != nil {
			t.Fatal(e)
		}
		return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, write(name+".crt", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), write(name+".key", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	}
	serverURI, facadeURI := "spiffe://apexload/java-broker", "spiffe://apexload/java-facade/generator"
	serverCert, _, _ := cert("broker", serverURI)
	_, certPath, keyPath := cert("facade", facadeURI)
	remote := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.TLS.VerifiedChains) == 0 || len(r.TLS.PeerCertificates[0].URIs) != 1 || r.TLS.PeerCertificates[0].URIs[0].String() != facadeURI {
			t.Error("unauthenticated facade")
		}
		var q broker.BrokerRequest
		_ = json.NewDecoder(r.Body).Decode(&q)
		if q.Operation != "capabilities" {
			t.Errorf("unexpected %s", q.Operation)
		}
		_ = json.NewEncoder(w).Encode(broker.BrokerResponse{Contract: broker.BrokerContract, RequestID: q.RequestID, BrokerEpoch: "epoch", Capabilities: &broker.BrokerCapabilities{Mode: "dedicated", Containment: "execution_pod", MaxHosts: 1, MaxVUsers: 1, MaxMemoryBytes: 100, Profiles: []broker.BrokerProfileCapability{{ID: "profile", ConfigurationSHA256: strings.Repeat("a", 64), PolicySHA256: strings.Repeat("b", 64), MemoryBytes: 100, HostLeaseMS: 1000, PreparationMS: 1000, InvocationMS: 1000, MaxRunMS: 10000}}}})
	}))
	remote.TLS = &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{serverCert}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: roots}
	remote.StartTLS()
	defer remote.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cfg := facadeConfig{Listen: "127.0.0.1:0", BrokerEndpoint: remote.URL, BrokerURI: serverURI, FacadeURI: facadeURI, Certificate: certPath, Key: keyPath, CA: caPath, TokenFile: write("token", []byte(strings.Repeat("x", 32))), InvocationMS: 1000}
	ready := make(chan net.Addr, 1)
	done := make(chan error, 1)
	go func() { done <- runConfigured(ctx, cfg, func(a net.Addr) { ready <- a }) }()
	var addr net.Addr
	select {
	case addr = <-ready:
	case e := <-done:
		t.Fatal("startup", e)
	case <-time.After(3 * time.Second):
		t.Fatal("startup blocked")
	}
	client, e := javasidecar.NewBrokerFacadeClient("http://"+addr.String(), strings.Repeat("x", 32), time.Second)
	if e != nil {
		t.Fatal(e)
	}
	defer client.Close()
	caps, e := client.Capabilities(t.Context())
	if e != nil || caps.MaxHosts != 1 {
		t.Fatal(caps, e)
	}
	cancel()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown blocked")
	}
}
