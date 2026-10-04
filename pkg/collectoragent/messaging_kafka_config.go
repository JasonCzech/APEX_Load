package collectoragent

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	kafkaManagementMaxTrustBytes        = 1 << 20
	kafkaManagementMaxTrustCertificates = 64
)

// KafkaManagementSettings is finite operator-owned routing approval. Broker
// replies cannot add a dial address; no hostname resolver is used by this pack.
type KafkaManagementSettings struct {
	ClusterID     string            `json:"cluster_id"`
	DialAddresses map[string]string `json:"dial_addresses"`
}

func (k *KafkaManagementSettings) clone() *KafkaManagementSettings {
	if k == nil {
		return nil
	}
	x := *k
	x.DialAddresses = make(map[string]string, len(k.DialAddresses))
	for endpoint, address := range k.DialAddresses {
		x.DialAddresses[endpoint] = address
	}
	return &x
}

func (k *KafkaManagementSettings) UnmarshalJSON(data []byte) error {
	type plain KafkaManagementSettings
	var x plain
	if err := strictManagementJSON(data, &x); err != nil {
		return err
	}
	if x.ClusterID == "" || x.DialAddresses == nil || len(x.DialAddresses) < 1 || len(x.DialAddresses) > 16 {
		return errors.New("Kafka management requires cluster identity and finite static routes")
	}
	*k = KafkaManagementSettings(x)
	return nil
}

type kafkaManagementRoute struct {
	endpoint   string
	serverName string
	address    netip.AddrPort
	network    string
}

type kafkaManagementApproval struct {
	routes           map[string]kafkaManagementRoute
	roots            *x509.CertPool
	trustDigest      string
	settingsDigest   string
	certificateCount int
}

func (a *kafkaManagementApproval) clone() *kafkaManagementApproval {
	if a == nil {
		return nil
	}
	x := *a
	x.routes = make(map[string]kafkaManagementRoute, len(a.routes))
	for endpoint, route := range a.routes {
		x.routes[endpoint] = route
	}
	if a.roots != nil {
		x.roots = a.roots.Clone()
	}
	return &x
}

// prepareKafkaManagementTarget runs once at New's owned-config boundary. It
// loads trust from disk without contacting a target; all subsequent validation
// and connection construction use the immutable in-memory approval.
func prepareKafkaManagementTarget(t *ManagementTarget) error {
	if t == nil {
		return errors.New("Kafka management target is missing")
	}
	t.kafkaApproval = nil
	if err := validateKafkaManagementShape(t); err != nil {
		return err
	}
	endpoints := make([]string, 0, len(t.Endpoints))
	routes := make(map[string]kafkaManagementRoute, len(t.Endpoints))
	for _, endpoint := range t.Endpoints {
		canonical, name, _, err := canonicalKafkaEndpoint(endpoint)
		if err != nil {
			return err
		}
		if _, duplicate := routes[canonical]; duplicate {
			return errors.New("duplicate Kafka management endpoint")
		}
		routes[canonical] = kafkaManagementRoute{endpoint: canonical, serverName: name}
		endpoints = append(endpoints, canonical)
	}
	addresses := make(map[string]string, len(t.Kafka.DialAddresses))
	for endpoint, address := range t.Kafka.DialAddresses {
		canonical, _, port, err := canonicalKafkaEndpoint(endpoint)
		if err != nil {
			return err
		}
		if _, duplicate := addresses[canonical]; duplicate {
			return errors.New("ambiguous Kafka static route")
		}
		route, approved := routes[canonical]
		if !approved {
			return errors.New("Kafka static route has no approved endpoint")
		}
		parsed, err := netip.ParseAddrPort(address)
		if err != nil || parsed.Port() != port || parsed.Addr().Zone() != "" {
			return errors.New("invalid Kafka static dial address")
		}
		ip := parsed.Addr().Unmap()
		if !ip.IsValid() || ip.IsUnspecified() || ip.IsMulticast() || ip == netip.MustParseAddr("255.255.255.255") {
			return errors.New("Kafka static dial address must be unicast")
		}
		parsed = netip.AddrPortFrom(ip, parsed.Port())
		if literal, err := netip.ParseAddr(route.serverName); err == nil && literal.Unmap() != ip {
			return errors.New("literal Kafka endpoint cannot select another dial identity")
		}
		route.address = parsed
		route.network = "tcp6"
		if ip.Is4() {
			route.network = "tcp4"
		}
		routes[canonical] = route
		addresses[canonical] = parsed.String()
	}
	if len(addresses) != len(routes) {
		return errors.New("Kafka management requires a static route for every endpoint")
	}
	roots, digest, count, err := loadKafkaManagementTrust(t.TLSCAFile)
	if err != nil {
		return err
	}
	t.Endpoints = endpoints
	t.Kafka.DialAddresses = addresses
	settingsDigest, err := kafkaManagementSettingsDigest(t)
	if err != nil {
		return err
	}
	t.kafkaApproval = &kafkaManagementApproval{routes: routes, roots: roots, trustDigest: digest, settingsDigest: settingsDigest, certificateCount: count}
	return validateKafkaManagementTarget(t)
}

func validateKafkaManagementShape(t *ManagementTarget) error {
	if t == nil || t.Kafka == nil || t.Scope.Kind != "kafka_offsets" {
		return errors.New("Kafka management configuration is missing")
	}
	if err := t.Scope.Validate(); err != nil {
		return err
	}
	if err := t.Identity.Validate(); err != nil {
		return err
	}
	if t.Kafka.ClusterID != t.Scope.Provider || !managementLabel(t.Kafka.ClusterID, 128) || strings.ContainsAny(t.Kafka.ClusterID, " \t") {
		return errors.New("Kafka cluster identity must match approved scope")
	}
	if t.Identity.Mode != "sasl_plain" && t.Identity.Mode != "sasl_scram" || t.Identity.CredentialRef == "" || t.Identity.CredentialVersion == "" {
		return errors.New("Kafka management requires an approved pinned SASL identity")
	}
	if len(t.Endpoints) < 1 || len(t.Endpoints) > 16 || len(t.Kafka.DialAddresses) != len(t.Endpoints) {
		return errors.New("Kafka management requires finite complete static routing")
	}
	if t.MinIntervalSeconds < 0 || t.MinIntervalSeconds > 3600 || t.MinIntervalSeconds > 0 && t.MinIntervalSeconds < 10 {
		return errors.New("Kafka management cadence exceeds bounds")
	}
	if t.TLSCAFile == "" || !filepath.IsAbs(t.TLSCAFile) {
		return errors.New("Kafka management requires approved absolute CA trust")
	}
	return nil
}

// validateKafkaManagementTarget deliberately performs no filesystem or network
// I/O. Readiness cannot silently reload a changed certificate file.
func validateKafkaManagementTarget(t *ManagementTarget) error {
	if err := validateKafkaManagementShape(t); err != nil {
		return err
	}
	a := t.kafkaApproval
	if a == nil || a.roots == nil || a.certificateCount < 1 || a.certificateCount > kafkaManagementMaxTrustCertificates || len(a.roots.Subjects()) != a.certificateCount || len(a.trustDigest) != 64 || len(a.routes) != len(t.Endpoints) {
		return errors.New("Kafka immutable transport approval is unavailable")
	}
	digest, err := kafkaManagementSettingsDigest(t)
	if err != nil || digest != a.settingsDigest {
		return errors.New("Kafka immutable transport approval changed")
	}
	for _, endpoint := range t.Endpoints {
		canonical, name, port, err := canonicalKafkaEndpoint(endpoint)
		if err != nil || canonical != endpoint {
			return errors.New("Kafka endpoint is not canonical")
		}
		route, ok := a.routes[endpoint]
		if !ok || route.endpoint != endpoint || route.serverName != name || route.address.Port() != port || t.Kafka.DialAddresses[endpoint] != route.address.String() || route.network != "tcp4" && route.network != "tcp6" {
			return errors.New("Kafka static transport approval mismatch")
		}
	}
	return nil
}

func kafkaManagementSettingsDigest(t *ManagementTarget) (string, error) {
	body, err := json.Marshal(t)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(body)
	return hex.EncodeToString(hash[:]), nil
}

func canonicalKafkaEndpoint(endpoint string) (string, string, uint16, error) {
	if len(endpoint) > 2048 || !managementLabel(endpoint, 2048) {
		return "", "", 0, errors.New("invalid Kafka TLS endpoint")
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "tls" || u.User != nil || u.Hostname() == "" || u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(endpoint, "#") || u.Opaque != "" {
		return "", "", 0, errors.New("invalid Kafka TLS endpoint")
	}
	if u.Host != net.JoinHostPort(u.Hostname(), u.Port()) {
		return "", "", 0, errors.New("invalid Kafka TLS endpoint authority")
	}
	port64, err := strconv.ParseUint(u.Port(), 10, 16)
	if err != nil || port64 == 0 || strconv.FormatUint(port64, 10) != u.Port() {
		return "", "", 0, errors.New("Kafka TLS endpoint requires an exact numeric port")
	}
	name := strings.ToLower(u.Hostname())
	if ip, err := netip.ParseAddr(name); err == nil {
		if ip.Zone() != "" || ip.IsUnspecified() || ip.IsMulticast() {
			return "", "", 0, errors.New("invalid Kafka endpoint IP")
		}
		name = ip.Unmap().String()
	} else {
		if len(name) > 253 {
			return "", "", 0, errors.New("invalid Kafka endpoint hostname")
		}
		for _, label := range strings.Split(name, ".") {
			if len(label) < 1 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return "", "", 0, errors.New("invalid Kafka endpoint hostname")
			}
			for _, c := range label {
				if c < 'a' || c > 'z' {
					if c < '0' || c > '9' {
						if c != '-' {
							return "", "", 0, errors.New("invalid Kafka endpoint hostname")
						}
					}
				}
			}
		}
	}
	canonical := "tls://" + net.JoinHostPort(name, strconv.FormatUint(port64, 10))
	return canonical, name, uint16(port64), nil
}

func kafkaRouteForEndpoint(t *ManagementTarget, endpoint string) (kafkaManagementRoute, error) {
	if err := validateKafkaManagementTarget(t); err != nil {
		return kafkaManagementRoute{}, err
	}
	route, ok := t.kafkaApproval.routes[endpoint]
	if !ok {
		return kafkaManagementRoute{}, &managementObservationError{reason: "permission_denied"}
	}
	return route, nil
}

func kafkaRouteForBroker(t *ManagementTarget, host string, port int32) (kafkaManagementRoute, error) {
	if port < 1 || port > 65535 || !managementLabel(host, 253) {
		return kafkaManagementRoute{}, &managementObservationError{reason: "permission_denied"}
	}
	endpoint, _, _, err := canonicalKafkaEndpoint("tls://" + net.JoinHostPort(host, strconv.Itoa(int(port))))
	if err != nil {
		return kafkaManagementRoute{}, &managementObservationError{reason: "permission_denied"}
	}
	return kafkaRouteForEndpoint(t, endpoint)
}

func loadKafkaManagementTrust(path string) (*x509.CertPool, string, int, error) {
	if path == "" || !filepath.IsAbs(path) {
		return nil, "", 0, errors.New("Kafka CA trust path must be absolute")
	}
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Size() < 1 || before.Size() > kafkaManagementMaxTrustBytes {
		return nil, "", 0, errors.New("Kafka CA trust file unavailable or exceeds bounds")
	}
	file, err := openKafkaManagementTrustFile(path)
	if err != nil {
		return nil, "", 0, errors.New("Kafka CA trust file unavailable")
	}
	defer file.Close()
	opened, statErr := file.Stat()
	after, pathErr := os.Lstat(path)
	if statErr != nil || pathErr != nil || !opened.Mode().IsRegular() || !after.Mode().IsRegular() || !os.SameFile(before, opened) || !os.SameFile(after, opened) {
		return nil, "", 0, errors.New("Kafka CA trust file identity changed")
	}
	body, err := io.ReadAll(io.LimitReader(file, kafkaManagementMaxTrustBytes+1))
	if err != nil || len(body) < 1 || len(body) > kafkaManagementMaxTrustBytes {
		return nil, "", 0, errors.New("Kafka CA trust exceeds bounds or could not be read")
	}
	roots := x509.NewCertPool()
	remaining := body
	count := 0
	seen := map[[32]byte]bool{}
	for len(bytes.TrimSpace(remaining)) > 0 {
		remaining = bytes.TrimSpace(remaining)
		if !bytes.HasPrefix(remaining, []byte("-----BEGIN CERTIFICATE-----")) {
			return nil, "", 0, errors.New("Kafka CA trust contains malformed PEM")
		}
		// pem.Decode can skip a malformed first block and find a later valid
		// one. Decode only this block so damaged trust is never ignored.
		endMarker := []byte("-----END CERTIFICATE-----")
		end := bytes.Index(remaining, endMarker)
		if end < 0 {
			return nil, "", 0, errors.New("Kafka CA trust contains unterminated PEM")
		}
		end += len(endMarker)
		encoded := remaining[:end]
		if bytes.Count(encoded, []byte("-----BEGIN CERTIFICATE-----")) != 1 {
			return nil, "", 0, errors.New("Kafka CA trust contains nested PEM")
		}
		block, trailing := pem.Decode(encoded)
		if block == nil || len(bytes.TrimSpace(trailing)) != 0 || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return nil, "", 0, errors.New("Kafka CA trust contains invalid PEM")
		}
		rest := remaining[end:]
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil || !cert.IsCA || !cert.BasicConstraintsValid || cert.KeyUsage != 0 && cert.KeyUsage&x509.KeyUsageCertSign == 0 {
			return nil, "", 0, errors.New("Kafka CA trust contains an invalid CA certificate")
		}
		hash := sha256.Sum256(cert.Raw)
		if seen[hash] {
			return nil, "", 0, errors.New("Kafka CA trust contains duplicate certificates")
		}
		seen[hash] = true
		count++
		if count > kafkaManagementMaxTrustCertificates {
			return nil, "", 0, errors.New("Kafka CA trust certificate count exceeds bounds")
		}
		roots.AddCert(cert)
		remaining = rest
	}
	if count == 0 {
		return nil, "", 0, errors.New("Kafka CA trust contains no certificates")
	}
	hash := sha256.Sum256(body)
	return roots, hex.EncodeToString(hash[:]), count, nil
}
