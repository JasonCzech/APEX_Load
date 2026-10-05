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
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/analysis"
)

// SolaceManagementSettings is finite local approval, not provider discovery.
type SolaceManagementSettings struct {
	Profile               string `json:"profile"`
	DialAddress           string `json:"dial_address"`
	AllowOwnSessionLogout bool   `json:"allow_own_session_logout"`
}

func (s *SolaceManagementSettings) clone() *SolaceManagementSettings {
	if s == nil {
		return nil
	}
	x := *s
	return &x
}
func (s *SolaceManagementSettings) UnmarshalJSON(body []byte) error {
	type plain SolaceManagementSettings
	var x plain
	if err := strictManagementJSON(body, &x); err != nil {
		return err
	}
	if x.Profile != "semp_v2" || !x.AllowOwnSessionLogout {
		return solaceManagementConfigError()
	}
	if _, _, err := solaceStaticAddress(x.DialAddress, 0); err != nil {
		return err
	}
	*s = SolaceManagementSettings(x)
	return nil
}

type solaceManagementRoute struct {
	origin, serverName, network string
	address                     netip.AddrPort
	requestURL, requestPath     string
	entity                      analysis.ManagementEntity
}
type solaceManagementApproval struct {
	routes                      map[string]solaceManagementRoute
	logout                      solaceManagementRoute
	roots                       *x509.CertPool
	trustDigest, settingsDigest string
	certificateCount            int
}

func (a *solaceManagementApproval) clone() *solaceManagementApproval {
	if a == nil {
		return nil
	}
	x := *a
	x.routes = make(map[string]solaceManagementRoute, len(a.routes))
	for key, route := range a.routes {
		route.entity = route.entity.Clone()
		x.routes[key] = route
	}
	x.logout.entity = a.logout.entity.Clone()
	if a.roots != nil {
		x.roots = a.roots.Clone()
	}
	return &x
}
func solaceManagementConfigError() error {
	return errors.New("Solace immutable management approval is unavailable or invalid")
}
func prepareSolaceManagementTarget(t *ManagementTarget) error {
	if t == nil {
		return solaceManagementConfigError()
	}
	t.solaceApproval = nil
	if err := validateSolaceManagementShape(t); err != nil {
		return err
	}
	origin, host, port, err := canonicalSolaceOrigin(t.Endpoints[0])
	if err != nil || t.Scope.Provider != host {
		return solaceManagementConfigError()
	}
	address, network, err := solaceStaticAddress(t.Solace.DialAddress, port)
	if err != nil {
		return err
	}
	routes := make(map[string]solaceManagementRoute, len(t.Scope.Entities))
	seen := make(map[string]bool, len(t.Scope.Entities))
	for _, entity := range t.Scope.Entities {
		route, err := buildSolaceManagementRoute(origin, host, address, network, entity)
		if err != nil {
			return err
		}
		key, err := entity.ID()
		if err != nil || seen[route.requestURL] {
			return solaceManagementConfigError()
		}
		seen[route.requestURL] = true
		routes[key] = route
	}
	roots, digest, count, err := loadKafkaManagementTrust(t.TLSCAFile)
	if err != nil {
		return errors.New("Solace approved CA trust is unavailable")
	}
	t.Endpoints = []string{origin}
	t.Solace.DialAddress = address.String()
	settings, err := solaceManagementSettingsDigest(t)
	if err != nil {
		return err
	}
	logout := solaceManagementRoute{origin: origin, serverName: host, network: network, address: address, requestURL: origin + "/SEMP/v2/action/about/user/logout", requestPath: "/SEMP/v2/action/about/user/logout"}
	t.solaceApproval = &solaceManagementApproval{routes: routes, logout: logout, roots: roots, trustDigest: digest, certificateCount: count, settingsDigest: settings}
	return validateSolaceManagementTarget(t)
}
func validateSolaceManagementShape(t *ManagementTarget) error {
	if t == nil || t.Solace == nil || t.Kafka != nil || t.ServiceBus != nil || t.Scope.Kind != analysis.ManagementKindSolace {
		return solaceManagementConfigError()
	}
	if t.Scope.Validate() != nil || t.Identity.Validate() != nil {
		return solaceManagementConfigError()
	}
	if t.Solace.Profile != "semp_v2" || !t.Solace.AllowOwnSessionLogout || t.Identity.Mode != "basic" || strings.Contains(t.Identity.Principal, ":") || t.Identity.CredentialRef == "" || t.Identity.CredentialVersion == "" {
		return solaceManagementConfigError()
	}
	if len(t.Endpoints) != 1 || t.MinIntervalSeconds < 0 || t.MinIntervalSeconds > 3600 || t.MinIntervalSeconds > 0 && t.MinIntervalSeconds < 30 || t.TLSCAFile == "" || !filepath.IsAbs(t.TLSCAFile) {
		return solaceManagementConfigError()
	}
	return nil
}
func validateSolaceManagementTarget(t *ManagementTarget) error {
	if err := validateSolaceManagementShape(t); err != nil {
		return err
	}
	origin, host, port, err := canonicalSolaceOrigin(t.Endpoints[0])
	if err != nil || origin != t.Endpoints[0] || host != t.Scope.Provider {
		return solaceManagementConfigError()
	}
	address, network, err := solaceStaticAddress(t.Solace.DialAddress, port)
	if err != nil || address.String() != t.Solace.DialAddress {
		return solaceManagementConfigError()
	}
	a := t.solaceApproval
	if a == nil || a.roots == nil || a.certificateCount < 1 || a.certificateCount > kafkaManagementMaxTrustCertificates || len(a.roots.Subjects()) != a.certificateCount || len(a.trustDigest) != 64 || len(a.routes) != len(t.Scope.Entities) {
		return solaceManagementConfigError()
	}
	digest, err := solaceManagementSettingsDigest(t)
	if err != nil || digest != a.settingsDigest {
		return solaceManagementConfigError()
	}
	for _, entity := range t.Scope.Entities {
		key, _ := entity.ID()
		expected, err := buildSolaceManagementRoute(origin, host, address, network, entity)
		actual, ok := a.routes[key]
		if err != nil || !ok || !solaceSameRoute(actual, expected) {
			return solaceManagementConfigError()
		}
	}
	expectedLogout := solaceManagementRoute{origin: origin, serverName: host, network: network, address: address, requestURL: origin + "/SEMP/v2/action/about/user/logout", requestPath: "/SEMP/v2/action/about/user/logout"}
	if !solaceSameRoute(a.logout, expectedLogout) {
		return solaceManagementConfigError()
	}
	return nil
}
func solaceSameRoute(a, b solaceManagementRoute) bool {
	if a.origin != b.origin || a.serverName != b.serverName || a.network != b.network || a.address != b.address || a.requestURL != b.requestURL || a.requestPath != b.requestPath {
		return false
	}
	if a.entity.Kind() == "" || b.entity.Kind() == "" {
		return a.entity.Kind() == "" && b.entity.Kind() == ""
	}
	aid, aerr := a.entity.ID()
	bid, berr := b.entity.ID()
	return aerr == nil && berr == nil && aid == bid
}
func solaceManagementSettingsDigest(t *ManagementTarget) (string, error) {
	body, err := json.Marshal(t)
	if err != nil {
		return "", solaceManagementConfigError()
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:]), nil
}
func canonicalSolaceOrigin(endpoint string) (string, string, uint16, error) {
	if !managementLabel(endpoint, 2048) {
		return "", "", 0, solaceManagementConfigError()
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(endpoint, "#") || u.RawPath != "" || u.Path != "" && u.Path != "/" {
		return "", "", 0, solaceManagementConfigError()
	}
	host := strings.ToLower(u.Hostname())
	if host == "" || len(host) > 253 || len(strings.Split(host, ".")) < 2 || net.ParseIP(host) != nil {
		return "", "", 0, solaceManagementConfigError()
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) < 1 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", "", 0, solaceManagementConfigError()
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return "", "", 0, solaceManagementConfigError()
			}
		}
	}
	port := uint16(443)
	if u.Port() != "" {
		n, parseErr := strconv.ParseUint(u.Port(), 10, 16)
		if parseErr != nil || n == 0 || strconv.FormatUint(n, 10) != u.Port() {
			return "", "", 0, solaceManagementConfigError()
		}
		port = uint16(n)
		if u.Host != net.JoinHostPort(u.Hostname(), u.Port()) {
			return "", "", 0, solaceManagementConfigError()
		}
	} else if u.Host != u.Hostname() {
		return "", "", 0, solaceManagementConfigError()
	}
	origin := "https://" + host
	if port != 443 {
		origin += ":" + strconv.Itoa(int(port))
	}
	return origin, host, port, nil
}
func solaceStaticAddress(value string, requiredPort uint16) (netip.AddrPort, string, error) {
	address, err := netip.ParseAddrPort(value)
	if err != nil || address.Port() == 0 || requiredPort != 0 && address.Port() != requiredPort || address.Addr().Zone() != "" {
		return netip.AddrPort{}, "", solaceManagementConfigError()
	}
	ip := address.Addr().Unmap()
	if !ip.IsValid() || ip.IsUnspecified() || ip.IsMulticast() || ip == netip.MustParseAddr("255.255.255.255") {
		return netip.AddrPort{}, "", solaceManagementConfigError()
	}
	address = netip.AddrPortFrom(ip, address.Port())
	if ip.Is4() {
		return address, "tcp4", nil
	}
	return address, "tcp6", nil
}
func solaceIdentitySegment(value string) (string, error) {
	if !managementLabel(value, 256) {
		return "", solaceManagementConfigError()
	}
	escaped := url.PathEscape(value)
	if value == "." {
		escaped = "%2E"
	}
	if value == ".." {
		escaped = "%2E%2E"
	}
	decoded, err := url.PathUnescape(escaped)
	if err != nil || decoded != value || strings.Contains(escaped, "/") {
		return "", solaceManagementConfigError()
	}
	return escaped, nil
}
func buildSolaceManagementRoute(origin, host string, address netip.AddrPort, network string, entity analysis.ManagementEntity) (solaceManagementRoute, error) {
	if entity.Validate() != nil || entity.Solace == nil {
		return solaceManagementRoute{}, solaceManagementConfigError()
	}
	vpn, err := solaceIdentitySegment(entity.Solace.VPN)
	if err != nil {
		return solaceManagementRoute{}, err
	}
	name, kind, identityField := entity.Solace.Queue, "queues", "queueName"
	if name == "" {
		name, kind, identityField = entity.Solace.TopicEndpoint, "topicEndpoints", "topicEndpointName"
	}
	segment, err := solaceIdentitySegment(name)
	if err != nil {
		return solaceManagementRoute{}, err
	}
	query := url.Values{"select": []string{"msgVpnName," + identityField + ",deadMsgQueue,msgs.count"}}.Encode()
	path := "/SEMP/v2/monitor/msgVpns/" + vpn + "/" + kind + "/" + segment
	requestURL := origin + path + "?" + query
	u, err := url.Parse(requestURL)
	if err != nil || u.EscapedPath() != path || u.RequestURI() != path+"?"+query {
		return solaceManagementRoute{}, solaceManagementConfigError()
	}
	return solaceManagementRoute{origin: origin, serverName: host, network: network, address: address, requestURL: requestURL, requestPath: u.RequestURI(), entity: entity.Clone()}, nil
}
func solaceRouteForEntity(t *ManagementTarget, entity analysis.ManagementEntity) (solaceManagementRoute, error) {
	if err := validateSolaceManagementTarget(t); err != nil {
		return solaceManagementRoute{}, err
	}
	key, err := entity.ID()
	if err != nil {
		return solaceManagementRoute{}, solaceManagementConfigError()
	}
	route, ok := t.solaceApproval.routes[key]
	if !ok {
		return solaceManagementRoute{}, solaceManagementConfigError()
	}
	route.entity = route.entity.Clone()
	return route, nil
}
func solaceLogoutRoute(t *ManagementTarget) (solaceManagementRoute, error) {
	if err := validateSolaceManagementTarget(t); err != nil {
		return solaceManagementRoute{}, err
	}
	return t.solaceApproval.logout, nil
}
