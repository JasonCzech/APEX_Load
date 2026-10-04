package collectoragent

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/jasonczech/apexload/pkg/analysis"
)

// ServiceBusManagementSettings is local routing approval, never a discovered
// address or a credential-bearing connection string.
type ServiceBusManagementSettings struct {
	DialAddress string `json:"dial_address"`
}

func (s *ServiceBusManagementSettings) clone() *ServiceBusManagementSettings {
	if s == nil {
		return nil
	}
	x := *s
	return &x
}
func (s *ServiceBusManagementSettings) UnmarshalJSON(body []byte) error {
	type plain ServiceBusManagementSettings
	var x plain
	if err := strictManagementJSON(body, &x); err != nil {
		return err
	}
	if _, _, err := serviceBusStaticAddress(x.DialAddress); err != nil {
		return err
	}
	*s = ServiceBusManagementSettings(x)
	return nil
}

type serviceBusManagementRoute struct {
	origin, serverName, network          string
	address                              netip.AddrPort
	resourceURI, requestURL, requestPath string
	entity                               analysis.ManagementEntity
	subscription                         bool
}

type serviceBusManagementApproval struct {
	routes                      map[string]serviceBusManagementRoute
	roots                       *x509.CertPool
	trustDigest, settingsDigest string
	certificateCount            int
}

func (a *serviceBusManagementApproval) clone() *serviceBusManagementApproval {
	if a == nil {
		return nil
	}
	x := *a
	x.routes = make(map[string]serviceBusManagementRoute, len(a.routes))
	for key, route := range a.routes {
		route.entity = route.entity.Clone()
		x.routes[key] = route
	}
	if a.roots != nil {
		x.roots = a.roots.Clone()
	}
	return &x
}

func prepareServiceBusManagementTarget(t *ManagementTarget) error {
	if t == nil {
		return serviceBusManagementConfigError()
	}
	t.serviceBusApproval = nil
	if err := validateServiceBusManagementShape(t); err != nil {
		return err
	}
	origin, host, err := canonicalServiceBusOrigin(t.Endpoints[0])
	if err != nil {
		return err
	}
	if t.Scope.Provider != host {
		return serviceBusManagementConfigError()
	}
	address, network, err := serviceBusStaticAddress(t.ServiceBus.DialAddress)
	if err != nil {
		return err
	}
	routes := make(map[string]serviceBusManagementRoute, len(t.Scope.Entities))
	seenURLs := make(map[string]bool, len(t.Scope.Entities))
	for _, entity := range t.Scope.Entities {
		route, err := buildServiceBusManagementRoute(origin, host, address, network, entity)
		if err != nil {
			return err
		}
		key, err := entity.ID()
		if err != nil {
			return serviceBusManagementConfigError()
		}
		if seenURLs[strings.ToLower(route.requestURL)] {
			return serviceBusManagementConfigError()
		}
		seenURLs[strings.ToLower(route.requestURL)] = true
		routes[key] = route
	}
	// This initial profile requires explicit local CA approval. Never fall back
	// to ambient system trust or reload a changed file inside a granted poll.
	roots, digest, count, err := loadKafkaManagementTrust(t.TLSCAFile)
	if err != nil {
		return errors.New("Service Bus approved CA trust is unavailable")
	}
	t.Endpoints = []string{origin}
	t.ServiceBus.DialAddress = address.String()
	settings, err := serviceBusManagementSettingsDigest(t)
	if err != nil {
		return err
	}
	t.serviceBusApproval = &serviceBusManagementApproval{routes: routes, roots: roots, trustDigest: digest, certificateCount: count, settingsDigest: settings}
	return validateServiceBusManagementTarget(t)
}

func validateServiceBusManagementShape(t *ManagementTarget) error {
	if t == nil || t.ServiceBus == nil || t.Kafka != nil || t.Scope.Kind != analysis.ManagementKindServiceBus {
		return serviceBusManagementConfigError()
	}
	if err := t.Scope.Validate(); err != nil {
		return serviceBusManagementConfigError()
	}
	if err := t.Identity.Validate(); err != nil {
		return serviceBusManagementConfigError()
	}
	if t.Identity.Mode != "shared_key" || !serviceBusPolicyName(t.Identity.Principal) || t.Identity.CredentialRef == "" || t.Identity.CredentialVersion == "" {
		return serviceBusManagementConfigError()
	}
	if len(t.Endpoints) != 1 || t.MinIntervalSeconds < 0 || t.MinIntervalSeconds > 3600 || t.MinIntervalSeconds > 0 && t.MinIntervalSeconds < 30 {
		return serviceBusManagementConfigError()
	}
	if t.TLSCAFile == "" || !filepath.IsAbs(t.TLSCAFile) {
		return serviceBusManagementConfigError()
	}
	return nil
}

// Pure in-memory validation is shared by readiness and every granted request.
func validateServiceBusManagementTarget(t *ManagementTarget) error {
	if err := validateServiceBusManagementShape(t); err != nil {
		return err
	}
	origin, host, err := canonicalServiceBusOrigin(t.Endpoints[0])
	if err != nil || origin != t.Endpoints[0] || t.Scope.Provider != host {
		return serviceBusManagementConfigError()
	}
	address, network, err := serviceBusStaticAddress(t.ServiceBus.DialAddress)
	if err != nil || address.String() != t.ServiceBus.DialAddress {
		return serviceBusManagementConfigError()
	}
	a := t.serviceBusApproval
	if a == nil || a.roots == nil || a.certificateCount < 1 || a.certificateCount > kafkaManagementMaxTrustCertificates || len(a.roots.Subjects()) != a.certificateCount || len(a.trustDigest) != 64 || len(a.routes) != len(t.Scope.Entities) {
		return serviceBusManagementConfigError()
	}
	digest, err := serviceBusManagementSettingsDigest(t)
	if err != nil || digest != a.settingsDigest {
		return serviceBusManagementConfigError()
	}
	for _, entity := range t.Scope.Entities {
		key, _ := entity.ID()
		expected, err := buildServiceBusManagementRoute(origin, host, address, network, entity)
		route, found := a.routes[key]
		if err != nil || !found || route.origin != expected.origin || route.serverName != expected.serverName || route.network != expected.network || route.address != expected.address || route.resourceURI != expected.resourceURI || route.requestURL != expected.requestURL || route.requestPath != expected.requestPath || route.subscription != expected.subscription {
			return serviceBusManagementConfigError()
		}
		actualID, err := route.entity.ID()
		if err != nil || actualID != key {
			return serviceBusManagementConfigError()
		}
	}
	return nil
}
func serviceBusManagementSettingsDigest(t *ManagementTarget) (string, error) {
	body, err := json.Marshal(t)
	if err != nil {
		return "", serviceBusManagementConfigError()
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:]), nil
}
func serviceBusManagementConfigError() error {
	return errors.New("Service Bus immutable management approval is unavailable or invalid")
}

func canonicalServiceBusOrigin(endpoint string) (string, string, error) {
	if !managementLabel(endpoint, 2048) {
		return "", "", serviceBusManagementConfigError()
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(endpoint, "#") || u.RawPath != "" || u.Path != "" && u.Path != "/" {
		return "", "", serviceBusManagementConfigError()
	}
	host := strings.ToLower(u.Hostname())
	if host == "" || len(host) > 253 || len(strings.Split(host, ".")) < 2 || net.ParseIP(host) != nil {
		return "", "", serviceBusManagementConfigError()
	}
	if u.Host != u.Hostname() && u.Host != net.JoinHostPort(u.Hostname(), "443") {
		return "", "", serviceBusManagementConfigError()
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) < 1 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", "", serviceBusManagementConfigError()
		}
		for _, c := range label {
			if !serviceBusASCIIAlphaNumeric(c) && c != '-' {
				return "", "", serviceBusManagementConfigError()
			}
		}
	}
	return "https://" + host, host, nil
}
func serviceBusStaticAddress(value string) (netip.AddrPort, string, error) {
	address, err := netip.ParseAddrPort(value)
	if err != nil || address.Port() != 443 || address.Addr().Zone() != "" {
		return netip.AddrPort{}, "", serviceBusManagementConfigError()
	}
	ip := address.Addr().Unmap()
	if !ip.IsValid() || ip.IsUnspecified() || ip.IsMulticast() || ip == netip.MustParseAddr("255.255.255.255") {
		return netip.AddrPort{}, "", serviceBusManagementConfigError()
	}
	address = netip.AddrPortFrom(ip, 443)
	if ip.Is4() {
		return address, "tcp4", nil
	}
	return address, "tcp6", nil
}
func serviceBusASCIIAlphaNumeric(c rune) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}
func serviceBusEntityPath(name string, subscription bool) (string, error) {
	limit := 256
	if subscription {
		limit = 50
	}
	if len(name) < 1 || len(name) > limit || subscription && strings.Contains(name, "/") {
		return "", serviceBusManagementConfigError()
	}
	parts := strings.Split(name, "/")
	for i, part := range parts {
		if part == "" || !serviceBusASCIIAlphaNumeric(rune(part[0])) || !serviceBusASCIIAlphaNumeric(rune(part[len(part)-1])) {
			return "", serviceBusManagementConfigError()
		}
		for _, c := range part {
			if !serviceBusASCIIAlphaNumeric(c) && c != '.' && c != '-' && c != '_' {
				return "", serviceBusManagementConfigError()
			}
		}
		part = strings.ToLower(part)
		switch part {
		case "subscriptions", "messages", "authorizationrules", "rules":
			return "", serviceBusManagementConfigError()
		}
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/"), nil
}
func buildServiceBusManagementRoute(origin, host string, address netip.AddrPort, network string, entity analysis.ManagementEntity) (serviceBusManagementRoute, error) {
	if entity.ServiceBus == nil || entity.Validate() != nil {
		return serviceBusManagementRoute{}, serviceBusManagementConfigError()
	}
	s := entity.ServiceBus
	var path string
	var err error
	subscription := s.Queue == ""
	if !subscription {
		path, err = serviceBusEntityPath(s.Queue, false)
	} else {
		var topic, sub string
		topic, err = serviceBusEntityPath(s.Topic, false)
		if err == nil {
			sub, err = serviceBusEntityPath(s.Subscription, true)
		}
		path = topic + "/Subscriptions/" + sub
	}
	if err != nil {
		return serviceBusManagementRoute{}, err
	}
	resource := origin + "/" + path
	return serviceBusManagementRoute{origin: origin, serverName: host, network: network, address: address, resourceURI: resource, requestURL: resource + "?api-version=2021-05", requestPath: "/" + path + "?api-version=2021-05", entity: entity.Clone(), subscription: subscription}, nil
}
func serviceBusRouteForEntity(t *ManagementTarget, entity analysis.ManagementEntity) (serviceBusManagementRoute, error) {
	if validateServiceBusManagementTarget(t) != nil {
		return serviceBusManagementRoute{}, &managementObservationError{reason: "permission_denied"}
	}
	key, err := entity.ID()
	if err != nil {
		return serviceBusManagementRoute{}, &managementObservationError{reason: "permission_denied"}
	}
	route, found := t.serviceBusApproval.routes[key]
	if !found {
		return serviceBusManagementRoute{}, &managementObservationError{reason: "permission_denied"}
	}
	route.entity = route.entity.Clone()
	return route, nil
}

func serviceBusPolicyName(name string) bool {
	if len(name) < 1 || len(name) > 50 || !serviceBusASCIIAlphaNumeric(rune(name[0])) || !serviceBusASCIIAlphaNumeric(rune(name[len(name)-1])) {
		return false
	}
	for _, c := range name {
		if !serviceBusASCIIAlphaNumeric(c) && c != '.' && c != '-' && c != '_' {
			return false
		}
	}
	return true
}
