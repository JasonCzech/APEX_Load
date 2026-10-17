package http

import "github.com/jasonczech/apexload/pkg/protocol/httpcontract"

type authHostScope = httpcontract.AuthHostScope

func ValidateAuthHosts(hosts []string) error { return httpcontract.ValidateAuthHosts(hosts) }
func AuthHostScopeAllows(hosts []string, target string) (bool, error) {
	return httpcontract.AuthHostScopeAllows(hosts, target)
}
func parseAuthHostScopes(raw any) ([]authHostScope, error) {
	return httpcontract.ParseAuthHostScopes(raw)
}

func authHostScopesAllow(scopes []authHostScope, target string) bool {
	return httpcontract.AuthHostScopesAllow(scopes, target)
}
