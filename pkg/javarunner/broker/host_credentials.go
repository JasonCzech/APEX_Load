package broker

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/url"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/javarunner"
)

// HostCredentialIssuer owns an operator-provided signing key in the broker.
// Only a fresh server-only leaf key and public broker trust roots leave it.
// Issued credentials cannot authenticate as a broker or renew a host lease.
// This does not provision a CA, authorize a workload, or enable a backend.
type HostCredentialIssuer struct {
	ca          *x509.Certificate
	signer      crypto.Signer
	chain       []byte
	brokerTrust []byte
	trustDomain string
	dnsDomain   string
	now         func() time.Time
}

// NewHostCredentialIssuer copies/parses all material; subsequent mutation of
// input byte slices cannot change the authority. BrokerTrust contains only
// public CA certificates, never a broker client key or a Kubernetes token.
func NewHostCredentialIssuer(certificatePEM, keyPEM, brokerTrustPEM []byte, trustDomain, dnsDomain string) (*HostCredentialIssuer, error) {
	if !credentialDomain(trustDomain) || !credentialDomain(dnsDomain) || len(dnsDomain) > 190 || len(keyPEM) > 32<<10 {
		return nil, ErrState
	}
	chain, e := publicCAs(certificatePEM)
	if e != nil {
		return nil, ErrState
	}
	if _, e = publicCAs(brokerTrustPEM); e != nil {
		return nil, ErrState
	}
	pair, e := tls.X509KeyPair(certificatePEM, keyPEM)
	if e != nil {
		return nil, ErrState
	}
	signer, ok := pair.PrivateKey.(crypto.Signer)
	if !ok {
		return nil, ErrState
	}
	return &HostCredentialIssuer{ca: chain[0], signer: signer, chain: append([]byte(nil), certificatePEM...), brokerTrust: append([]byte(nil), brokerTrustPEM...), trustDomain: trustDomain, dnsDomain: dnsDomain, now: time.Now}, nil
}

func credentialDomain(s string) bool {
	if s == "" || len(s) > 253 {
		return false
	}
	for _, part := range strings.Split(s, ".") {
		if !namespaceName.MatchString(part) {
			return false
		}
	}
	return true
}

func publicCAs(data []byte) ([]*x509.Certificate, error) {
	if len(data) == 0 || len(data) > 128<<10 {
		return nil, ErrState
	}
	var out []*x509.Certificate
	for len(bytes.TrimSpace(data)) > 0 {
		data = bytes.TrimSpace(data)
		// pem.Decode otherwise silently skips leading non-PEM material.
		if !bytes.HasPrefix(data, []byte("-----BEGIN CERTIFICATE-----")) {
			return nil, ErrState
		}
		block, rest := pem.Decode(data)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return nil, ErrState
		}
		cert, e := x509.ParseCertificate(block.Bytes)
		if e != nil || !cert.IsCA || !cert.BasicConstraintsValid || cert.KeyUsage&x509.KeyUsageCertSign == 0 || len(out) >= 8 {
			return nil, ErrState
		}
		out = append(out, cert)
		data = rest
	}
	if len(out) == 0 {
		return nil, ErrState
	}
	return out, nil
}

// HostTransportMaterial deliberately has no exported credential fields and
// redacts formatted output. SecretJSON is the explicit credential export point.
type HostTransportMaterial struct {
	host, epoch, configuration  string
	name, serverName, serverURI string
	certificate, key, trust     []byte
	expires                     time.Time
}

func (m *HostTransportMaterial) String() string       { return "java_host_transport_material" }
func (m *HostTransportMaterial) GoString() string     { return m.String() }
func (m *HostTransportMaterial) SecretName() string   { return m.name }
func (m *HostTransportMaterial) ServerName() string   { return m.serverName }
func (m *HostTransportMaterial) ServerURI() string    { return m.serverURI }
func (m *HostTransportMaterial) ExpiresAt() time.Time { return m.expires }

func hostTransportName(r Record) string {
	sum := sha256.Sum256([]byte(r.BrokerEpoch + "\x00" + r.ID))
	return "java-tls-" + hex.EncodeToString(sum[:20])
}

// Issue creates one fresh key for one reserved host. validUntil must cover the
// operator's full preparation/run/drain allowance and cannot exceed 24 hours.
// There is no in-place credential rotation or transparent session replacement;
// runs exceeding this boundary need a separately qualified credential strategy.
func (i *HostCredentialIssuer) Issue(r Record, validUntil time.Time) (*HostTransportMaterial, error) {
	if i == nil || i.ca == nil || i.signer == nil || i.now == nil || !name.MatchString(r.ID) || !name.MatchString(r.BrokerEpoch) || !validPolicyDigest(r.Binding.ConfigurationSHA256) || r.State != javarunner.HostReserved {
		return nil, ErrState
	}
	now := i.now()
	// X.509 timestamps have second precision: validate the actual encoded expiry.
	validUntil = validUntil.UTC().Truncate(time.Second)
	if !r.LeaseUntil.After(now) || !r.PrepareUntil.After(now) || !validUntil.After(r.PrepareUntil) || validUntil.After(now.Add(24*time.Hour)) || now.Before(i.ca.NotBefore) || validUntil.After(i.ca.NotAfter) {
		return nil, ErrState
	}
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		return nil, ErrState
	}
	serial, e := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if e != nil {
		return nil, ErrState
	}
	serial.Add(serial, big.NewInt(1))
	secretName := hostTransportName(r)
	serverName := secretName + "." + i.dnsDomain
	identity := &url.URL{Scheme: "spiffe", Host: i.trustDomain, Path: "/java-host/" + r.ID}
	notBefore := now.Add(-30 * time.Second)
	if notBefore.Before(i.ca.NotBefore) {
		notBefore = i.ca.NotBefore
	}
	template := &x509.Certificate{SerialNumber: serial, NotBefore: notBefore, NotAfter: validUntil, DNSNames: []string{serverName}, URIs: []*url.URL{identity}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true}
	der, e := x509.CreateCertificate(rand.Reader, template, i.ca, &key.PublicKey, i.signer)
	if e != nil {
		return nil, ErrState
	}
	private, e := x509.MarshalPKCS8PrivateKey(key)
	if e != nil {
		return nil, ErrState
	}
	certificate := append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), i.chain...)
	return &HostTransportMaterial{host: r.ID, epoch: r.BrokerEpoch, configuration: r.Binding.ConfigurationSHA256, name: secretName, serverName: serverName, serverURI: identity.String(), certificate: certificate, key: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}), trust: append([]byte(nil), i.brokerTrust...), expires: validUntil}, nil
}

// SecretJSON exports exactly the files consumed by the supervisor. It is an
// immutable, uniquely named Secret, not a service-account credential. Never log
// this payload. Provisioning must reject conflicts rather than overwrite/adopt.
func (m *HostTransportMaterial) SecretJSON(namespace string, r Record) ([]byte, error) {
	if m == nil || !namespaceName.MatchString(namespace) || m.host != r.ID || m.epoch != r.BrokerEpoch || m.configuration != r.Binding.ConfigurationSHA256 || m.name != hostTransportName(r) || len(m.key) == 0 {
		return nil, ErrState
	}
	return json.Marshal(map[string]any{
		"apiVersion": "v1", "kind": "Secret", "type": "kubernetes.io/tls", "immutable": true,
		"metadata": map[string]any{"name": m.name, "namespace": namespace, "annotations": map[string]string{hostAnnotation: r.ID, epochAnnotation: r.BrokerEpoch}},
		"data":     map[string][]byte{"tls.crt": m.certificate, "tls.key": m.key, "ca.crt": m.trust},
	})
}
