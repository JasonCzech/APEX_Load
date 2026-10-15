package browser

import (
	"net/url"
	"strings"
)

// SalesforceSignInURL reports a Salesforce login or identity-verification
// page: where a recording's sign-in happens, and where an observer whose
// session has expired is sent.
func SalesforceSignInURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	path := parsed.Path
	salesforce := strings.HasSuffix(host, ".salesforce.com") || strings.HasSuffix(host, ".force.com")
	switch {
	case salesforce && strings.HasPrefix(path, "/_ui/identity/verification/"):
		return true
	case host == "login.salesforce.com" || host == "test.salesforce.com":
		return true
	case strings.HasSuffix(host, ".my.salesforce.com"):
		// A My Domain's login form is at its root.
		return path == "" || path == "/" || path == "/login" || strings.HasPrefix(path, "/secur/")
	}
	return false
}
