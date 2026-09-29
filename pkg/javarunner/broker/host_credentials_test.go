package broker

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/javarunner"
)

func credentialCA(t *testing.T) ([]byte, []byte) {
	t.Helper()
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(25 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, e := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	private, e := x509.MarshalPKCS8PrivateKey(key)
	if e != nil {
		t.Fatal(e)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private})
}
func credentialIssuer(t *testing.T) *HostCredentialIssuer {
	t.Helper()
	ca, key := credentialCA(t)
	i, e := NewHostCredentialIssuer(ca, key, ca, "apexload", "hosts.internal")
	if e != nil {
		t.Fatal(e)
	}
	return i
}
func hostMaterial(t *testing.T, r Record) *HostTransportMaterial {
	t.Helper()
	m, e := credentialIssuer(t).Issue(r, time.Now().Add(time.Hour))
	if e != nil {
		t.Fatal(e)
	}
	return m
}

func TestHostCredentialsServerOnlyAndBoundToReservation(t *testing.T) {
	s, epoch, _ := newStore(t)
	r := reserve(t, s, epoch)
	i := credentialIssuer(t)
	m, e := i.Issue(r, time.Now().Add(time.Hour))
	if e != nil {
		t.Fatal(e)
	}
	pair, e := tls.X509KeyPair(m.certificate, m.key)
	if e != nil {
		t.Fatal(e)
	}
	leaf, e := x509.ParseCertificate(pair.Certificate[0])
	if e != nil {
		t.Fatal(e)
	}
	roots := x509.NewCertPool()
	roots.AddCert(i.ca)
	if _, e = leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: m.ServerName(), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); e != nil {
		t.Fatal(e)
	}
	if _, e = leaf.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); e == nil {
		t.Fatal("host key can authenticate as client")
	}
	if len(leaf.URIs) != 1 || leaf.URIs[0].String() != m.ServerURI() || m.ServerURI() != "spiffe://apexload/java-host/"+r.ID || leaf.IsCA {
		t.Fatal("unscoped host authority")
	}
	second, e := i.Issue(r, time.Now().Add(time.Hour))
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Equal(m.key, second.key) || bytes.Equal(m.certificate, second.certificate) {
		t.Fatal("key/certificate reused")
	}
	for _, mutate := range []func(*Record){func(r *Record) { r.ID = "other" }, func(r *Record) { r.BrokerEpoch = "other" }, func(r *Record) { r.Binding.ConfigurationSHA256 = strings.Repeat("b", 64) }} {
		other := r
		mutate(&other)
		if _, e := m.SecretJSON("java-test", other); e == nil {
			t.Fatal("material reused across binding")
		}
	}
	data, e := m.SecretJSON("java-test", r)
	if e != nil {
		t.Fatal(e)
	}
	var secret transportSecret
	if json.Unmarshal(data, &secret) != nil || !secret.Immutable || len(secret.Data) != 3 || !bytes.Equal(secret.Data["ca.crt"], i.brokerTrust) {
		t.Fatal("bad secret")
	}
	if bytes.Contains(secret.Data["ca.crt"], []byte("PRIVATE")) || bytes.Equal(secret.Data["tls.key"], secret.Data["ca.crt"]) {
		t.Fatal("signing authority exported")
	}
	encoded, _ := json.Marshal(m)
	if string(encoded) != "{}" || strings.Contains(fmt.Sprintf("%+v %#v", m, m), "PRIVATE") {
		t.Fatal("credential formatting exposed secrets")
	}
}

func TestHostCredentialIssuerInputAndLifetimeBounds(t *testing.T) {
	ca, key := credentialCA(t)
	for _, tt := range []struct {
		name           string
		ca, key, trust []byte
		domain, dns    string
	}{
		{"empty", nil, key, ca, "apexload", "hosts.internal"},
		{"wrong key", ca, []byte("invalid"), ca, "apexload", "hosts.internal"},
		{"private trust", ca, key, append(append([]byte{}, ca...), key...), "apexload", "hosts.internal"},
		{"leading garbage", append([]byte("secret\n"), ca...), key, ca, "apexload", "hosts.internal"},
		{"wildcard", ca, key, ca, "apexload", "*.internal"},
		{"URI", ca, key, ca, "https://apexload", "hosts.internal"},
		{"huge", ca, bytes.Repeat([]byte("x"), 32769), ca, "apexload", "hosts.internal"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, e := NewHostCredentialIssuer(tt.ca, tt.key, tt.trust, tt.domain, tt.dns); e == nil {
				t.Fatal("invalid issuer accepted")
			}
		})
	}
	i, e := NewHostCredentialIssuer(ca, key, ca, "apexload", "hosts.internal")
	if e != nil {
		t.Fatal(e)
	}
	// Parsing owns its inputs, including the trust bundle.
	for n := range ca {
		ca[n] = 'x'
	}
	for n := range key {
		key[n] = 'x'
	}
	s, epoch, _ := newStore(t)
	r := reserve(t, s, epoch)
	if _, e = i.Issue(r, time.Now().Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	for _, mutate := range []func(*Record, *time.Time){
		func(r *Record, _ *time.Time) { r.State = javarunner.HostCommitted },
		func(r *Record, _ *time.Time) { r.LeaseUntil = time.Now().Add(-time.Second) },
		func(r *Record, _ *time.Time) { r.PrepareUntil = time.Now().Add(-time.Second) },
		func(_ *Record, d *time.Time) { *d = time.Now().Add(25 * time.Hour) },
		func(r *Record, d *time.Time) { *d = r.PrepareUntil },
	} {
		bad := r
		until := time.Now().Add(time.Hour)
		mutate(&bad, &until)
		if _, e = i.Issue(bad, until); e == nil {
			t.Fatal("invalid host/expiry accepted")
		}
	}
	i.ca.NotAfter = time.Now().Add(time.Minute)
	if _, e = i.Issue(r, time.Now().Add(time.Hour)); e == nil {
		t.Fatal("leaf outlives issuer")
	}
}

func TestIssuedHostCredentialsUseSeparateBrokerTrustOverTLS(t *testing.T) {
	ca, key := credentialCA(t)
	brokerCA, _, brokerCert, _, _ := controlCertificates(t)
	i, e := NewHostCredentialIssuer(ca, key, brokerCA, "apexload", "hosts.internal")
	if e != nil {
		t.Fatal(e)
	}
	s, epoch, _ := newStore(t)
	r := reserve(t, s, epoch)
	m, e := i.Issue(r, time.Now().Add(time.Hour))
	if e != nil {
		t.Fatal(e)
	}
	pair, e := tls.X509KeyPair(m.certificate, m.key)
	if e != nil {
		t.Fatal(e)
	}
	cfg := controlConfig()
	cfg.HostID = r.ID
	cfg.BrokerEpoch = r.BrokerEpoch
	cfg.LeaseTTL = 5 * time.Second
	c := newControl(t, cfg, &controlRuntime{})
	waitReady(t, c)
	server := httptest.NewUnstartedServer(c)
	server.TLS, e = SupervisorTLS(pair, m.trust, cfg.BrokerClientURI)
	if e != nil {
		t.Fatal(e)
	}
	server.StartTLS()
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(ca)
	client, e := NewSupervisorClient(server.URL, m.ServerURI(), cfg, &tls.Config{RootCAs: roots, ServerName: m.ServerName(), Certificates: []tls.Certificate{brokerCert}})
	if e != nil {
		t.Fatal(e)
	}
	defer client.Close()
	if _, e = client.Control(t.Context(), "ready"); e != nil {
		t.Fatal(e)
	}
}
