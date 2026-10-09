package http

import (
	"context"
	"net/http"
	"strings"

	ntlmssp "github.com/Azure/go-ntlmssp"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// Integrated (challenge-driven) HTTP authentication. "ntlm" and "negotiate"
// answer NTLM and Negotiate challenges (Negotiate carrying NTLM; Kerberos is
// not supported); "negotiate_any" also answers Basic and Digest challenges,
// picking the scheme from the server's WWW-Authenticate as LoadRunner's
// web_set_user does. Credentials are never sent before a challenge.

// IsIntegratedAuthType reports whether an auth type is challenge-driven.
func IsIntegratedAuthType(authType string) bool {
	switch strings.ToLower(strings.TrimSpace(authType)) {
	case "ntlm", "negotiate", "negotiate_any":
		return true
	}
	return false
}

type integratedAuthKey struct{}

type integratedAuthMode struct {
	allowBasic bool
}

// integratedAuthRoundTripper runs the NTLM/Negotiate handshake for requests
// marked with integratedAuthKey and passes every other request through, so
// preemptive Basic and the other auth types are unaffected.
type integratedAuthRoundTripper struct {
	base http.RoundTripper
}

func (rt integratedAuthRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	mode, ok := req.Context().Value(integratedAuthKey{}).(integratedAuthMode)
	if !ok {
		return rt.base.RoundTrip(req)
	}
	return ntlmssp.Negotiator{RoundTripper: rt.base, AllowBasicAuth: mode.allowBasic}.RoundTrip(req)
}

// integratedAuthUser is the NTLM user name: DOMAIN\user when the auth
// config names a domain the user name does not already carry. negotiate_any
// keeps the user name as authored, because the same name answers a Basic
// challenge (write DOMAIN\user to send a domain).
func integratedAuthUser(auth *protocol.AuthConfig) string {
	user := auth.Username
	domain := strings.TrimSpace(auth.Domain)
	if domain != "" && !strings.ContainsAny(user, `\@`) && !strings.EqualFold(strings.TrimSpace(auth.Type), "negotiate_any") {
		user = domain + `\` + user
	}
	return user
}

// applyIntegratedAuth marks req for the challenge-driven handshake with
// auth's credentials. It returns the request to send and, for negotiate_any,
// the digest config that answers a Digest challenge.
func applyIntegratedAuth(req *http.Request, auth *protocol.AuthConfig) (*http.Request, *protocol.AuthConfig) {
	req.SetBasicAuth(integratedAuthUser(auth), auth.Password)
	anyScheme := strings.EqualFold(strings.TrimSpace(auth.Type), "negotiate_any")
	req = req.WithContext(context.WithValue(req.Context(), integratedAuthKey{}, integratedAuthMode{allowBasic: anyScheme}))
	if !anyScheme {
		return req, nil
	}
	return req, &protocol.AuthConfig{Type: "digest", Username: auth.Username, Password: auth.Password, Domain: auth.Domain}
}
