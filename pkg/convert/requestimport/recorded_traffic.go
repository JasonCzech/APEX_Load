package requestimport

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// Rules shared by the converters that turn recorded traffic (proxy recorder
// sessions and HAR captures, including the browser recorder's) into HTTP
// scripts. They cover recorded bytes that can never be replayed as recorded.
const (
	// IntegratedAuthReasonCode gates a request recorded with an NTLM or
	// Negotiate (Kerberos) handshake. Those tokens are single-use and bound to
	// one connection, so replaying them fails with 401; the request needs
	// credentials configured instead.
	IntegratedAuthReasonCode = "integrated_auth_requires_configuration"
	// RecordedBodyTruncatedReasonCode gates a request whose body was cut off
	// by the recorder's capture limit; replaying it would send a partial body.
	RecordedBodyTruncatedReasonCode = "recorded_body_truncated"
)

// IntegratedAuthScheme returns "ntlm" or "negotiate" when an Authorization (or
// WWW-Authenticate) value carries that scheme, and "" otherwise.
func IntegratedAuthScheme(value string) string {
	scheme, _, _ := strings.Cut(strings.TrimSpace(value), " ")
	switch strings.ToLower(scheme) {
	case "ntlm":
		return "ntlm"
	case "negotiate":
		return "negotiate"
	}
	return ""
}

// IsIntegratedAuthChallenge reports whether a recorded response is an NTLM or
// Negotiate 401 challenge.
func IsIntegratedAuthChallenge(status int, wwwAuthenticate []string) bool {
	if status != http.StatusUnauthorized {
		return false
	}
	for _, value := range wwwAuthenticate {
		for _, part := range strings.Split(value, "\n") {
			if IntegratedAuthScheme(part) != "" {
				return true
			}
		}
	}
	return false
}

// IntegratedAuthFinding marks a request recorded with an integrated-auth
// handshake whose single-use Authorization tokens were removed. The script
// answers the host's challenge itself (IntegratedAuthConfig), so the request
// runs; promotion waits until the credentials are reviewed.
func IntegratedAuthFinding(source, sourceRef, scheme string) importreview.Finding {
	label := map[string]string{"ntlm": "NTLM", "negotiate": "Negotiate (Kerberos)"}[scheme]
	if label == "" {
		label = "integrated"
	}
	return importreview.Finding{
		Code: IntegratedAuthReasonCode, ReasonCode: IntegratedAuthReasonCode,
		Severity: diag.Warning, Gate: importreview.GatePromotion,
		Source: source, SourceRef: sourceRef, Resolution: "configure_integrated_auth",
		Detail: fmt.Sprintf("This request was recorded with a %s authentication handshake. Its single-use tokens were removed; the script answers the host's challenge with negotiate_any auth using the project secrets %s and %s, which must be configured before replay.", label, IntegratedAuthUsernameSecret, IntegratedAuthPasswordSecret),
	}
}

// The project secrets a recording's integrated-auth hosts authenticate
// with.
const (
	IntegratedAuthUsernameSecret = "INTEGRATED_AUTH_USERNAME"
	IntegratedAuthPasswordSecret = "INTEGRATED_AUTH_PASSWORD"
)

// ApplyIntegratedAuth gives a script recorded with NTLM or Negotiate
// handshakes host-scoped negotiate_any auth: it answers whichever challenge
// those hosts send (as VuGen's web_set_user does), with credentials from
// project secrets, never recorded tokens. It returns a warning when the
// script already has different auth.
func ApplyIntegratedAuth(script *dsl.Script, hosts []string) string {
	if script == nil || len(hosts) == 0 {
		return ""
	}
	username := "{{secret." + IntegratedAuthUsernameSecret + "}}"
	password := "{{secret." + IntegratedAuthPasswordSecret + "}}"
	existing := script.Config.HTTP.Auth
	switch {
	case existing.Type == "" && existing.Username == "" && existing.Token == "":
		script.Config.HTTP.Auth = dsl.AuthConfig{Type: "negotiate_any", Username: username, Password: password}
	case existing.Type == "negotiate_any" && existing.Username == username && existing.Password == password:
	default:
		return "the recording's NTLM/Negotiate hosts were not given negotiate_any auth because the script already has other auth settings; configure their credentials by hand"
	}
	auth := &script.Config.HTTP.Auth
	for _, host := range hosts {
		duplicate := false
		for _, existingHost := range auth.Hosts {
			duplicate = duplicate || strings.EqualFold(existingHost, host)
		}
		if !duplicate && host != "" {
			auth.Hosts = append(auth.Hosts, host)
		}
	}
	return ""
}

// IntegratedAuthHost is the auth scope of a recorded request URL: its host
// name, any port.
func IntegratedAuthHost(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed.Hostname())
}

// RecordedBodyTruncatedFinding is the execution gate for a request whose body
// exceeded the recorder's capture limit.
func RecordedBodyTruncatedFinding(source, sourceRef string, recordedBytes int) importreview.Finding {
	size := "an unknown size"
	if recordedBytes > 0 {
		size = fmt.Sprintf("%d bytes", recordedBytes)
	}
	return importreview.Finding{
		Code: RecordedBodyTruncatedReasonCode, ReasonCode: RecordedBodyTruncatedReasonCode,
		Severity: diag.Blocker, Gate: importreview.GateExecution,
		Source: source, SourceRef: sourceRef, Resolution: "recapture_body",
		Detail: fmt.Sprintf("The recorded request body (%s) exceeded the capture limit and was truncated, so replaying it would send a partial body. Re-record with a larger body capture limit or supply the full body.", size),
	}
}

// CookieOrigins tracks which cookies earlier recorded responses set, so a
// later request's Cookie header can leave them to the replay cookie jar
// instead of sending them twice.
type CookieOrigins struct {
	// byName maps a lower-cased cookie name to where it was set.
	byName map[string][]cookieOrigin
}

type cookieOrigin struct {
	domain string
	// hostOnly: set without a Domain attribute, so it is sent to that exact
	// host only (RFC 6265 section 5.3), not to its subdomains.
	hostOnly bool
}

// NewCookieOrigins returns an empty tracker.
func NewCookieOrigins() *CookieOrigins {
	return &CookieOrigins{byName: map[string][]cookieOrigin{}}
}

// Observe records the Set-Cookie values of a response from host.
func (c *CookieOrigins) Observe(host string, setCookies []string) {
	host = strings.ToLower(hostOnly(host))
	for _, value := range setCookies {
		for _, line := range strings.Split(value, "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			header := http.Header{"Set-Cookie": {line}}
			for _, cookie := range (&http.Response{Header: header}).Cookies() {
				origin := cookieOrigin{domain: strings.TrimPrefix(strings.ToLower(cookie.Domain), ".")}
				if origin.domain == "" {
					origin = cookieOrigin{domain: host, hostOnly: true}
				}
				name := strings.ToLower(cookie.Name)
				c.byName[name] = append(c.byName[name], origin)
			}
		}
	}
}

// WithoutJarCookies removes from a Cookie header value the cookies an earlier
// response set for host (the replay jar will send them). It returns the
// remaining header value, which may be empty, and how many were removed.
func (c *CookieOrigins) WithoutJarCookies(host, cookieHeader string) (string, int) {
	host = strings.ToLower(hostOnly(host))
	parts := strings.Split(cookieHeader, ";")
	kept := make([]string, 0, len(parts))
	removed := 0
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		name, _, _ := strings.Cut(trimmed, "=")
		if c.setFor(strings.ToLower(strings.TrimSpace(name)), host) {
			removed++
			continue
		}
		kept = append(kept, trimmed)
	}
	return strings.Join(kept, "; "), removed
}

func (c *CookieOrigins) setFor(name, host string) bool {
	for _, origin := range c.byName[name] {
		if host == origin.domain || (!origin.hostOnly && strings.HasSuffix(host, "."+origin.domain)) {
			return true
		}
	}
	return false
}

func hostOnly(hostport string) string {
	hostport = strings.TrimSpace(hostport)
	if strings.HasPrefix(hostport, "[") {
		if end := strings.Index(hostport, "]"); end > 0 {
			return hostport[1:end]
		}
	}
	if host, _, found := strings.Cut(hostport, ":"); found && !strings.Contains(host, ":") {
		return host
	}
	return hostport
}

// maxPageTitleCheckBytes bounds a recorded title used as a page check.
const maxPageTitleCheckBytes = 200

var pageTitlePattern = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

// RecordedPageTitle returns the text a recorded HTML page's title check
// looks for, as VuGen adds a text check on each page's title: the raw text
// between the page's first <title> and </title>, trimmed. It returns "" when
// the response is not a successful HTML page, has no usable title, or the
// title contains one of the dynamic values the correlation pass found.
func RecordedPageTitle(contentType string, status int, body string, dynamic []string) string {
	if status < 200 || status >= 300 || !strings.Contains(strings.ToLower(contentType), "text/html") {
		return ""
	}
	match := pageTitlePattern.FindStringSubmatch(body)
	if match == nil {
		return ""
	}
	title := strings.TrimSpace(match[1])
	if title == "" || len(title) > maxPageTitleCheckBytes || strings.ContainsAny(title, "<>\r\n") {
		return ""
	}
	for _, value := range dynamic {
		if value = strings.TrimSpace(value); len(value) >= 3 && strings.Contains(title, value) {
			return ""
		}
	}
	return title
}
